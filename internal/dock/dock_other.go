//go:build !windows

// Package dock 的非 Windows 桩：**如实说"没做"**（贴 ACE 的窗口操作只在 Windows 上做过）。
package dock

import (
	"fmt"

	"acetool/internal/win"
)

// Options 见 windows 实现。
type Options struct {
	Title    string
	SnapPx   int
	PollMS   int
	TopInset int
	OnNote   func(kind, text string)
}

// State 见 windows 实现。
type State struct {
	Running  bool     `json:"running"`
	Title    string   `json:"title"`
	Window   string   `json:"window,omitempty"`
	HWND     uintptr  `json:"hwnd,omitempty"`
	AceTitle string   `json:"aceTitle,omitempty"`
	AceHWND  uintptr  `json:"aceHwnd,omitempty"`
	Snapped  bool     `json:"snapped"`
	Edge     string   `json:"edge,omitempty"`
	Partial  bool     `json:"partial,omitempty"`
	Dragging bool     `json:"dragging"`
	Rect     win.Rect `json:"rect"`
	Want     win.Rect `json:"want,omitempty"`
	Ticks    int      `json:"ticks"`
	LastErr  string   `json:"lastErr,omitempty"`
}

// Dock 见 windows 实现。
type Dock struct{ state State }

// Start 在非 Windows 上直接报错。
func Start(opts Options) (*Dock, error) {
	return nil, fmt.Errorf("贴边吸附（动别人的窗口）只在 Windows 上做过")
}

func (d *Dock) State() State { return d.state }

func (d *Dock) Stop() {}
