//go:build windows

package win

import (
	"os/exec"
	"syscall"
)

// createNoWindow 是 CreateProcess 的 CREATE_NO_WINDOW（0x08000000）。
const createNoWindow = 0x08000000

// HideWindow 让**子进程**不留控制台窗口（用户 2026-10-20：「点击会一直弹 cmd 窗口」）。
//
// 为什么要它：exe 现在是 `-H=windowsgui` 构建（自己没有控制台），而 `acestudio-cli`、
// `reg.exe` 都是**控制台程序** —— 父进程没有控制台可继承时，系统会给每个子进程**新开一个黑框**。
// 加 `CREATE_NO_WINDOW` 之后子进程照跑、不带窗口。
func HideWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
