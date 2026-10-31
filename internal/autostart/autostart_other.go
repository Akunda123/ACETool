//go:build !windows

package autostart

import "fmt"

// 非 Windows 上"开机启动"没做：如实说"不支持"，别假装能开。
// （ACETool 只跑在 Windows 上；这个桩只为让 `go build ./...` 在别的机器上也过得去。）

// State 见 windows 实现。
type State struct {
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Stale     bool   `json:"stale"`
	Exe       string `json:"exe"`
	Cmd       string `json:"cmd,omitempty"`
	Want      string `json:"want"`
	Note      string `json:"note,omitempty"`
}

func ExePath() string { return "" }

func Cmd() string { return "" }

func Status() (State, error) {
	return State{Supported: false, Note: "开机启动只在 Windows 上做过"}, nil
}

func Enable() error  { return fmt.Errorf("开机启动只在 Windows 上做过") }
func Disable() error { return fmt.Errorf("开机启动只在 Windows 上做过") }

func Set(bool) (State, error) {
	return State{Supported: false, Note: "开机启动只在 Windows 上做过"},
		fmt.Errorf("开机启动只在 Windows 上做过")
}
