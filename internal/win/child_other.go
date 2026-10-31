//go:build !windows

package win

import "os/exec"

// HideWindow 在非 Windows 上什么都不用做。
func HideWindow(cmd *exec.Cmd) {}
