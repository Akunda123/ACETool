package main

// 更新检测：多源回退，确保国内/国外都能查到。
//
//	① api.github.com 的 releases/latest（国外最快，国内可能被墙）
//	② jsDelivr 上的 VERSION 文件（国内可达，CDN 缓存）
//	③ gh-proxy 转发的 raw.githubusercontent（代理，国内多数可用）
//	④ raw.githubusercontent 直连（保底）
//
// 每次启动都会强制查一次（不走缓存）；页面手动「检查更新」也强制。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const updateRepo = "Akunda123/ACETool"

type updateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Newer     bool   `json:"newer"`
	URL       string `json:"url,omitempty"`
	Source    string `json:"source,omitempty"`
	CheckedAt string `json:"checkedAt,omitempty"`
	Error     string `json:"error,omitempty"`
}

var (
	updateMu      sync.Mutex
	updateCache   *updateInfo
	updateCacheAt time.Time
)

func semverLess(a, b string) bool {
	pa, pb := parseSemver(a), parseSemver(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func parseSemver(s string) [3]int {
	var out [3]int
	s = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "v"))
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	for i, part := range strings.Split(s, ".") {
		if i >= 3 {
			break
		}
		n, _ := strconv.Atoi(strings.TrimSpace(part))
		out[i] = n
	}
	return out
}

// getText 带超时地取一个 URL 的正文（限 64 KB，够读版本号）。
func getText(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ACETool/"+appVersion)
	req.Header.Set("Accept", "*/*")
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// checkUpdate 逐个源尝试；force=false 且 10 分钟内有结果则复用。
func checkUpdate(ctx context.Context, force bool) *updateInfo {
	updateMu.Lock()
	if !force && updateCache != nil && time.Since(updateCacheAt) < 10*time.Minute {
		c := updateCache
		updateMu.Unlock()
		return c
	}
	updateMu.Unlock()

	info := &updateInfo{Current: appVersion}
	var errs []string
	found := ""

	// ① GitHub Releases API
	if txt, err := getText(ctx, "https://api.github.com/repos/"+updateRepo+"/releases/latest"); err == nil {
		var rel struct {
			TagName string `json:"tag_name"`
			HTMLURL string `json:"html_url"`
		}
		if json.Unmarshal([]byte(txt), &rel) == nil && strings.TrimSpace(rel.TagName) != "" {
			found = strings.TrimSpace(rel.TagName)
			info.Source = "GitHub API"
			info.URL = rel.HTMLURL
		} else {
			errs = append(errs, "GitHub API：返回里没有 tag_name")
		}
	} else {
		errs = append(errs, "GitHub API："+err.Error())
	}

	// ② jsDelivr 上的 VERSION
	if found == "" {
		if txt, err := getText(ctx, "https://cdn.jsdelivr.net/gh/"+updateRepo+"@main/VERSION"); err == nil {
			v := strings.TrimSpace(strings.SplitN(txt, "\n", 2)[0])
			if v != "" {
				found = v
				info.Source = "jsDelivr"
			}
		} else {
			errs = append(errs, "jsDelivr："+err.Error())
		}
	}

	// ③ gh-proxy 转发的 raw
	if found == "" {
		if txt, err := getText(ctx, "https://gh-proxy.com/https://raw.githubusercontent.com/"+updateRepo+"/main/VERSION"); err == nil {
			v := strings.TrimSpace(strings.SplitN(txt, "\n", 2)[0])
			if v != "" {
				found = v
				info.Source = "gh-proxy"
			}
		} else {
			errs = append(errs, "gh-proxy："+err.Error())
		}
	}

	// ④ raw.githubusercontent 直连
	if found == "" {
		if txt, err := getText(ctx, "https://raw.githubusercontent.com/"+updateRepo+"/main/VERSION"); err == nil {
			v := strings.TrimSpace(strings.SplitN(txt, "\n", 2)[0])
			if v != "" {
				found = v
				info.Source = "raw.githubusercontent"
			}
		} else {
			errs = append(errs, "raw："+err.Error())
		}
	}

	if found != "" {
		info.Latest = found
		if info.URL == "" {
			info.URL = "https://github.com/" + updateRepo + "/releases"
		}
		info.Newer = semverLess(appVersion, found)
		info.CheckedAt = time.Now().Format("2006-01-02 15:04")
	} else {
		info.Error = strings.Join(errs, "；")
	}

	updateMu.Lock()
	updateCache, updateCacheAt = info, time.Now()
	updateMu.Unlock()
	return info
}

func cachedUpdateInfo() *updateInfo {
	updateMu.Lock()
	defer updateMu.Unlock()
	if updateCache != nil {
		return updateCache
	}
	return &updateInfo{Current: appVersion}
}
