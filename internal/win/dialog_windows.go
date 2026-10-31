//go:build windows

package win

import (
	"syscall"
	"unsafe"
)

var (
	comdlg32         = syscall.NewLazyDLL("comdlg32.dll")
	pGetOpenFileName = comdlg32.NewProc("GetOpenFileNameW")
)

// openFileNameW 是 Win32 `OPENFILENAMEW` 的前半段（字段顺序必须与头文件一致）。
type openFileNameW struct {
	lStructSize       uint32
	hwndOwner         uintptr
	hInstance         uintptr
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        uintptr
	dwReserved        uint32
	flagsEx           uint32
}

const (
	ofnExplorer     = 0x00080000
	ofnFileMustExist = 0x00001000
)

// PickFile 弹一个**系统文件选择框**（用户 2026-10-21：「手动路径应该允许…浏览」）。
// 返回选中的绝对路径；取消返回 ok=false。
//
// ⚠️ 为什么非得 exe 这边弹：浏览器出于安全**永远不给网页本地文件的真实路径**
// （`<input type=file>` 只给 File 对象，`files[0].path` 是 Electron 才有的东西）——
// 所以"浏览"只能由进程自己弹 Win32 对话框。
func PickFile(title string) (string, bool) {
	buf := make([]uint16, 4096)
	ofn := openFileNameW{}
	ofn.lStructSize = uint32(unsafe.Sizeof(ofn))
	if title != "" {
		if t, err := syscall.UTF16PtrFromString(title); err == nil {
			ofn.lpstrTitle = t
		}
	}
	// Win32 的过滤器是 **\0 分隔**（不是 `|`）：显示名\0模式\0…\0\0
	filter := "程序|*.exe;*.bat;*.cmd\000所有文件|*.*\000\000"
	_ = filter
	f := make([]uint16, 0, 128)
	for _, part := range []string{"程序", "*.exe;*.bat;*.cmd", "所有文件", "*.*"} {
		u, _ := syscall.UTF16FromString(part)
		f = append(f, u...)
	}
	f = append(f, 0)
	ofn.lpstrFilter = &f[0]
	ofn.lpstrFile = &buf[0]
	ofn.nMaxFile = uint32(len(buf))
	ofn.flags = ofnExplorer | ofnFileMustExist
	r, _, _ := pGetOpenFileName.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return "", false
	}
	return syscall.UTF16ToString(buf), true
}
