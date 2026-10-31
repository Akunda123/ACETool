//go:build !windows

package hotkeys

import (
	"fmt"
	"sort"
)

// 非 Windows 桩：**逐条如实报"本机没做"**（不是静默不生效）。
// 页面内那层（层 1）与平台无关，照常可用。

type Manager struct{}

type Result struct {
	Action string `json:"action"`
	Key    string `json:"key"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

func NewManager(actions map[string]Action, fire func(actionID string)) *Manager { return &Manager{} }

func (m *Manager) Start() {}

func (m *Manager) Stop() {}

func (m *Manager) Apply(b Bindings) []Result {
	var out []Result
	ids := make([]string, 0, len(b))
	for k := range b {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	for _, action := range ids {
		if b[action].Key == "" || !b[action].Global {
			continue
		}
		out = append(out, Result{Action: action, Key: b[action].Key, Error: "系统级热键只在 Windows 上做过（本机不是 Windows）"})
	}
	return out
}

func FocusPanel(titleSubstr string) (string, error) {
	return "", fmt.Errorf("窗口聚焦只在 Windows 上做过（本机不是 Windows）")
}
