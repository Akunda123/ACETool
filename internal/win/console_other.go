//go:build !windows

package win

// EnsureConsole 在非 Windows 上什么都不用做（没有控制台子系统这回事）。
func EnsureConsole() bool { return false }
