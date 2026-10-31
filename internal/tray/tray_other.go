//go:build !windows

package tray

import "fmt"

// 非 Windows 上没有系统托盘：**如实说"没做"**，不静默变成"没反应"。
// （ACETool 只跑在 Windows 上；这个桩只为让 `go build ./...` 在别的机器上也过得去。）

// Options 见 windows 实现。
type Options struct {
	Tip        string
	OnOpen     func()
	OnSettings func()
	OnQuit     func()
	OnBalloonClick func() // 点气泡时调用
}

// Info 见 windows 实现。
type Info struct {
	Running  bool    `json:"running"`
	HWND     uintptr `json:"hwnd,omitempty"`
	IconSize int     `json:"iconSize"`
	Icon     string  `json:"icon"`
	Added    bool    `json:"added"`
	Tip      string  `json:"tip"`
	DPI      int     `json:"dpi"`
	Err      string  `json:"err,omitempty"`
}

// Tray 是托盘。
type Tray struct{ info Info }

// Start 在非 Windows 上直接报错。
func Start(o Options) (*Tray, error) {
	return nil, fmt.Errorf("系统托盘图标只在 Windows 上做过")
}

func (t *Tray) Update(tip string) { t.info.Tip = tip }

func (t *Tray) Info() Info { return t.info }

func (t *Tray) Stop() {}

// Balloon 在非 Windows 平台是空操作（只为跨平台编译）。
func (t *Tray) Balloon(title, text string) {}
