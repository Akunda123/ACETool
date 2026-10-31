package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"acetool/internal/config"
)

// 单实例守卫（用户 2026-10-05 选的做法 ②）。
//
// ACETool 是**一个本地面板**，不是多实例服务。开第二个实例只会带来三件坏事：
//
//	① **系统级热键注册必然失败**（Windows 报 `ERROR_HOTKEY_ALREADY_REGISTERED` = 1409）
//	   ⇒ 第二个实例的快捷键设置页会列一排 ✗（这是**正确**行为，但用户体验是"坏了"）；
//	② `config.json` 是**共享的一个文件**（键位 / 置顶偏好）⇒ 两边各写各的，**后写的赢**；
//	③ `panel.focus` / 置顶都是"按窗口标题找浏览器窗口" ⇒ 两个实例都去动那个窗口，互相抢。
//
// 所以：**启动时先问一句有没有人在跑**，有就只把浏览器开到那个地址、自己退出。
//
// 发现文件放在 `internal/config` 的目录里（`%APPDATA%\ACETool\panel.json`），
// 形状照 ACE 自己那套 `mcp-bridge.json`：一个 JSON，写着 pid / url / startedAt。
//
// ⚠️ 判活**不看 pid**：Windows 上 `os.FindProcess` 对任何 pid 都返回成功（它不做检查），
// 所以 pid 不构成"还活着"的判据。这里改成**打一次 `/api/ping`**，能回 `app == "acetool"`
// 才算活着 —— 顺带排除了"那个端口上蹲着别的程序"这种假阳性。
// ⚠️ 也**不信自己写的文件**：文件在而探不通 ⇒ 当成陈旧记录，覆盖掉继续（并说一句）。
type instanceInfo struct {
	App       string `json:"app"`
	PID       int    `json:"pid"`
	URL       string `json:"url"`
	StartedAt string `json:"startedAt"`
	Tools     string `json:"tools,omitempty"`
}

func instancePath() (string, error) {
	d, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "panel.json"), nil
}

func readInstance() *instanceInfo {
	p, err := instancePath()
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var v instanceInfo
	if json.Unmarshal(b, &v) != nil || v.App != "acetool" || v.URL == "" {
		return nil
	}
	return &v
}

func writeInstance(v instanceInfo) error {
	p, err := instancePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return os.WriteFile(p, b, 0o644)
}

// removeInstance 只删**自己写的**那一条（按 pid 认领）——避免把后来者的记录删掉。
func removeInstance(pid int) {
	p, err := instancePath()
	if err != nil {
		return
	}
	if cur := readInstance(); cur != nil && cur.PID != pid {
		return
	}
	_ = os.Remove(p)
}

// pingInstance 打一次 `/api/ping`；只有回 `app == "acetool"` 才算活着。
//
// ⚠️ 超时给得很短：这是个**启动路径上**的检查，不能因为对方卡住就让新实例迟迟不启动。
func pingInstance(url string, timeout time.Duration) (*instanceInfo, bool) {
	cl := &http.Client{Timeout: timeout}
	resp, err := cl.Get(url + "api/ping")
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, false
	}
	var v instanceInfo
	if json.Unmarshal(body, &v) != nil || v.App != "acetool" {
		return nil, false
	}
	v.URL = url
	return &v, true
}

// existingInstance 返回**确实活着**的那个实例（探不通就返回 nil，让调用方照常启动）。
func existingInstance(timeout time.Duration) *instanceInfo {
	cur := readInstance()
	if cur == nil {
		return nil
	}
	if v, ok := pingInstance(cur.URL, timeout); ok {
		return v
	}
	return nil
}

// instanceNotice 是"已经有面板在跑"时给用户看的那句话（终端 + 面板外都能读懂）。
func instanceNotice(v *instanceInfo, opened bool) string {
	s := fmt.Sprintf("已经有一个 ACETool 面板在跑：%s（PID %d，启动于 %s）", v.URL, v.PID, v.StartedAt)
	if v.Tools != "" {
		s += "\n  " + v.Tools
	}
	if opened {
		s += "\n  ⇒ 已把浏览器开到那个地址；**没有**起第二个实例（否则系统级热键会注册失败、配置也会互相覆盖）。"
	} else {
		s += "\n  ⇒ 没有起第二个实例。要新开窗口，把这个地址再开一个标签页/窗口即可（多窗口是支持的）。"
	}
	return s
}
