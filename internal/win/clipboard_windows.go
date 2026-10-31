//go:build windows

package win

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

// Windows 剪贴板（CF_UNICODETEXT）——给"细条上的按钮"用的。
//
// 为什么 exe 自己要会这个：细条上的「复制歌词 / 粘贴歌词」两项，逻辑在网页里是
// `navigator.clipboard.writeText/readText`，而**细条是原生窗口、没有网页** ⇒
// 想让那两颗按钮真干活，只能 exe 自己读写剪贴板（用户 2026-10-17：「把按钮功能先加上」）。
var (
	pOpenClipboard              = user32.NewProc("OpenClipboard")
	pCloseClipboard             = user32.NewProc("CloseClipboard")
	pEmptyClipboard             = user32.NewProc("EmptyClipboard")
	pSetClipboardData           = user32.NewProc("SetClipboardData")
	pGetClipboardData           = user32.NewProc("GetClipboardData")
	pIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	pGlobalAlloc                = kernel32.NewProc("GlobalAlloc")
	pGlobalLock                 = kernel32.NewProc("GlobalLock")
	pGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	pGlobalFree                 = kernel32.NewProc("GlobalFree")
	pGlobalSize                 = kernel32.NewProc("GlobalSize")
	// 用 RtlMoveMemory 往/从 GlobalLock 拿到的**裸地址**搬字节：地址是 syscall 回给我们的
	// uintptr，直接写成 `unsafe.Pointer(ptr)` 会被 `go vet` 判成 misuse（检查脚本里 vet 是硬门）。
	// 走 RtlMoveMemory 就只需要"一个 Go 指针 + 一个整数地址"，两边都干净。
	pRtlMoveMemory = kernel32.NewProc("RtlMoveMemory")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

// openClipboard 开剪贴板（**重试**：它是全局独占资源，输入法/浏览器正开着就开不了）。
func openClipboard() error {
	for i := 0; i < 10; i++ {
		if ret, _, _ := pOpenClipboard.Call(0); ret != 0 {
			return nil
		}
		time.Sleep(30 * time.Millisecond)
	}
	return fmt.Errorf("剪贴板被别的程序占着（OpenClipboard 一直失败）")
}

// SetClipboardText 把一段文本放进剪贴板（覆盖原内容）。
func SetClipboardText(s string) error {
	if err := openClipboard(); err != nil {
		return err
	}
	defer pCloseClipboard.Call()
	if ret, _, err := pEmptyClipboard.Call(); ret == 0 {
		return fmt.Errorf("EmptyClipboard 失败：%v", err)
	}
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return err
	}
	n := uintptr(len(u) * 2) // 含结尾的 0
	h, _, _ := pGlobalAlloc.Call(gmemMoveable, n)
	if h == 0 {
		return fmt.Errorf("GlobalAlloc(%d 字节) 失败", n)
	}
	ptr, _, _ := pGlobalLock.Call(h)
	if ptr == 0 {
		pGlobalFree.Call(h)
		return fmt.Errorf("GlobalLock 失败")
	}
	pRtlMoveMemory.Call(ptr, uintptr(unsafe.Pointer(&u[0])), n)
	pGlobalUnlock.Call(h)
	if ret, _, err := pSetClipboardData.Call(cfUnicodeText, h); ret == 0 {
		pGlobalFree.Call(h) // 交出去失败 ⇒ 内存还是我们的，自己释放
		return fmt.Errorf("SetClipboardData 失败：%v", err)
	}
	// ⚠️ 交出去成功之后**不能再 GlobalFree**：所有权归系统了，再释放就是 double free
	return nil
}

// ClipboardText 读剪贴板里的文本。**没有文本时返回空串 + nil**（让调用方去说"剪贴板是空的"，
// 而不是当成故障）。
func ClipboardText() (string, error) {
	if ret, _, _ := pIsClipboardFormatAvailable.Call(cfUnicodeText); ret == 0 {
		return "", nil
	}
	if err := openClipboard(); err != nil {
		return "", err
	}
	defer pCloseClipboard.Call()
	h, _, _ := pGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", nil
	}
	ptr, _, _ := pGlobalLock.Call(h)
	if ptr == 0 {
		return "", fmt.Errorf("GlobalLock 失败")
	}
	defer pGlobalUnlock.Call(h)
	// 长度按 `GlobalSize` 算（**不要**猜一个上限去 Slice —— 那会越界读到别的东西）
	size, _, _ := pGlobalSize.Call(h)
	if size < 2 {
		return "", nil
	}
	buf := make([]uint16, int(size)/2)
	n := uintptr(len(buf) * 2)
	pRtlMoveMemory.Call(uintptr(unsafe.Pointer(&buf[0])), ptr, n)
	return syscall.UTF16ToString(buf), nil
}
