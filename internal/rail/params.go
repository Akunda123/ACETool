// 细条上「参数条」要记住的值（连接音符的阈值 / 批量替换音素的查找与替换）。
//
// 为什么 exe 自己存一份：细条是**原生窗口、没有网页**，用不到主面板那套 `localStorage`
// （主面板记的是它自己的：`acetool.thresh.*` / `acetool.field.*`，见 `home.html`）。
// 两边**各记各的**是刻意的 —— 主面板里能改的值不该被细条悄悄覆盖，反之亦然。
//
// 落盘：`%APPDATA%\ACETool\params.json`，形状 `{工具id: {参数名: 值}}`。
// 写盘用"临时文件 + 改名"（原子替换），这样半截写坏的那份不会留下来。
package rail

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	paramsMu   sync.Mutex
	paramsData map[string]map[string]string
	paramsRead bool
)

func paramsPath() string {
	return filepath.Join(os.Getenv("APPDATA"), "ACETool", "params.json")
}

// loadParamsLocked 读盘（只在持锁时调）。读不到/坏了 ⇒ 空表，不报错：
// 这里丢了顶多是"回到默认值"，不值得因为一个记忆文件把面板搞坏。
func loadParamsLocked() {
	if paramsRead {
		return
	}
	paramsRead = true
	paramsData = map[string]map[string]string{}
	b, err := os.ReadFile(paramsPath())
	if err != nil {
		return
	}
	var v map[string]map[string]string
	if err := json.Unmarshal(b, &v); err == nil && v != nil {
		paramsData = v
	}
}

// ParamGet 读某个工具某个参数记着的值（没有 ⇒ 空串，调用方自己给默认）。
func ParamGet(tool, name string) string {
	paramsMu.Lock()
	defer paramsMu.Unlock()
	loadParamsLocked()
	return paramsData[tool][name]
}

// ParamSet 记一个值并落盘。
//
// ⚠️ 2026-10-17（自查「假写入」时改的）：第一版**返回 void、把写盘错误全吞掉** ——
// 内存里已经用上新值，所以界面上"看着保存了"，可 `params.json` 没写进去时**下次启动会悄悄变回旧值**。
// 现在把错误交回调用方（参数条那条执行链会落一行 `rail.log` 如实记下）：
// 不阻断这次执行（内存值仍然生效），但**不再假装存住了**。
func ParamSet(tool, name, value string) error {
	paramsMu.Lock()
	loadParamsLocked()
	if paramsData[tool] == nil {
		paramsData[tool] = map[string]string{}
	}
	if paramsData[tool][name] == value {
		paramsMu.Unlock()
		return nil
	}
	paramsData[tool][name] = value
	b, err := json.MarshalIndent(paramsData, "", "  ")
	paramsMu.Unlock()
	if err != nil {
		return fmt.Errorf("参数表序列化失败：%w", err)
	}
	dir := filepath.Dir(paramsPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("建参数表目录失败：%w", err)
	}
	tmp := paramsPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("写参数表（临时文件）失败：%w", err)
	}
	// Windows 上 Rename 覆盖是原子的（同卷）
	if err := os.Rename(tmp, paramsPath()); err != nil {
		return fmt.Errorf("替换参数表失败：%w", err)
	}
	return nil
}
