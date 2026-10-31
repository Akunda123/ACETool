//go:build !windows

package win

// PickFile 在非 Windows 上没有系统文件对话框。
func PickFile(title string) (string, bool) { return "", false }
