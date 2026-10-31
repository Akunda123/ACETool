//go:build !windows

package rail

import (
	"fmt"

	"acetool/internal/win"
)

// 非 Windows 上细条不存在：**如实说"没做"**，不静默变成"没反应"。
// （面板与 ACE 都跑在 Windows 上；这个桩只为让 `go build ./...` 在别的机器上也过得去。）

// Options 见 windows 实现。
type Options struct {
	Items   []Item
	BaseURL string
	Pad     int
	OnNote  func(kind, text string)
}

// State 见 windows 实现。
type State struct {
	Running     bool     `json:"running"`
	Visible     bool     `json:"visible"`
	Level       int      `json:"level"`
	Hover       int      `json:"hover"`
	HWND        uintptr  `json:"hwnd"`
	Rect        win.Rect `json:"rect"`
	TargetTitle string   `json:"targetTitle,omitempty"`
	TargetHWND  uintptr  `json:"targetHwnd,omitempty"`
	TargetPID   uint32   `json:"targetPid,omitempty"`
	ItemH       int      `json:"itemH"`
	DPI         int      `json:"dpi"`
	Note        string   `json:"note,omitempty"`
	LastErr     string   `json:"lastErr,omitempty"`
}

// Rail 是细条。
type Rail struct{ state State }

// Start 在非 Windows 上直接报错。
func Start(opts Options) (*Rail, error) {
	return nil, fmt.Errorf("exe 侧栏（贴 ACE 的汉字细条）只在 Windows 上做过")
}

// ACERunning 在非 Windows 上恒 false（没有 ACE 窗口枚举）。
func ACERunning() bool { return false }

func (r *Rail) State() State { return r.state }

func (r *Rail) Note(text string) {}

func (r *Rail) Stop() {}
// Reload 在非 Windows 上没有侧栏可重载。
func Reload() {}
