package main

// 更新检测：向 GitHub Releases 问最新 tag，按语义版本比较，结果缓存在内存里。
// 不自动下载、不自动替换 exe —— 只告诉用户"有新版本"和下载页地址。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GitHub 仓库（改这里就能换发布源）。
const updateRepo = "Akunda123/ACETool"

type updateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Newer     bool   `json:"newer"`
	URL       string `json:"url,omitempty"`
	CheckedAt string `json:"checkedAt,omitempty"`
	Error     string `json:"error,omitempty"`
}

var (
	updateMu      sync.Mutex
	updateCache   *updateInfo
	updateCacheAt time.Time
)

// semverLess 比较 x.y.z（允许 v 前缀、后缀 -beta 会被忽略）。
func semverLess(a, b string) bool {
	pa := parseSemver(a)
	pb := parseSemver(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func parseSemver(s string) [3]int {
	var out [3]int
	s = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(s), "v"))
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	for i, part := range strings.Split(s, ".") {
		if i >= 3 {
			break
		}
		n, _ := strconv.Atoi(part)
		out[i] = n
	}
	return out
}

// checkUpdate 问一次 GitHub；10 分钟内重复调用直接用缓存。
func checkUpdate(ctx context.Context, force bool) *updateInfo {
	updateMu.Lock()
	defer updateMu.Unlock()
	if !force && updateCache != nil && time.Since(updateCacheAt) < 10*time.Minute {
		return updateCache
	}
	info := &updateInfo{Current: appVersion}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+updateRepo+"/releases/latest", nil)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ACETool/"+appVersion)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		info.Error = "连不上 GitHub：" + err.Error()
		updateCache, updateCacheAt = info, time.Now()
		return info
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		info.Error = "仓库还没有发布（404）"
		updateCache, updateCacheAt = info, time.Now()
		return info
	}
	if resp.StatusCode != 200 {
		info.Error = fmt.Sprintf("GitHub 返回 %d", resp.StatusCode)
		updateCache, updateCacheAt = info, time.Now()
		return info
	}
	var rel struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		info.Error = "发布信息解不开：" + err.Error()
		updateCache, updateCacheAt = info, time.Now()
		return info
	}
	info.Latest = strings.TrimSpace(rel.TagName)
	info.URL = rel.HTMLURL
	if info.URL == "" {
		info.URL = "https://github.com/" + updateRepo + "/releases"
	}
	info.Newer = info.Latest != "" && semverLess(appVersion, info.Latest)
	info.CheckedAt = time.Now().Format("2006-01-02 15:04")
	updateCache, updateCacheAt = info, time.Now()
	return info
}

// cachedUpdateInfo 给页面用（不触发网络）。
func cachedUpdateInfo() *updateInfo {
	updateMu.Lock()
	defer updateMu.Unlock()
	if updateCache != nil {
		return updateCache
	}
	return &updateInfo{Current: appVersion}
}
