//go:build windows

package win

import (
	"os"
)

var (
	pAllocConsole     = kernel32.NewProc("AllocConsole")
	pGetConsoleWindow = kernel32.NewProc("GetConsoleWindow")
)

// EnsureConsole 让当前进程拥有一个控制台窗口（GUI 子系统构建下用 `-console` 打开）。
//
// ⚠️ 背景（用户 2026-10-20：「能否将那个 command 窗口隐藏」）：发版 exe 用 `-H=windowsgui`
// 构建，双击时**不再弹黑框**；需要看日志时用 `-console` 启动，或者看
// `%APPDATA%\ACETool\acetool.log`（GUI 构建下 `log` 输出都写那里）。
//
// 已经从命令行启动、继承了控制台时直接复用，不重复分配。
func EnsureConsole() bool {
	if h, _, _ := pGetConsoleWindow.Call(); h != 0 {
		return true
	}
	if r, _, _ := pAllocConsole.Call(); r == 0 {
		if h, _, _ := pGetConsoleWindow.Call(); h == 0 {
			return false
		}
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
	return true
}
