// probe-xtitle —— 最小复现：**Go 子进程能不能对"别人进程"的窗口做跨进程写操作**（换标题）。
//
// 为什么要它：`SetWindowPos(HWND_TOPMOST)` 从一个 Go 进程里调**返回成功但样式位没变**
// （同一个句柄、同一个会话里，用 PowerShell 的 C# P/Invoke 调同一个 API 却生效）。
// 先把范围缩小：如果**换标题**这种跨进程写也做不到，那就是"Go 进程整体被挡"；
// 如果换标题行、只有置顶不行，那就是"置顶这一位被过滤"。
//
// ⚠️ 会临时改目标窗口的标题，跑完**改回原文**。
//
// 用法：go run ./devtools/probe-xtitle 0x1234
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	u32             = syscall.NewLazyDLL("user32.dll")
	pSetWindowTextW = u32.NewProc("SetWindowTextW")
	pGetWindowTextW = u32.NewProc("GetWindowTextW")
)

func title(h uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := pGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

func setTitle(h uintptr, s string) (bool, error) {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return false, err
	}
	ret, _, e := pSetWindowTextW.Call(h, uintptr(unsafe.Pointer(p)))
	return ret != 0, e
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("用法：go run ./devtools/probe-xtitle 0x1234")
		os.Exit(2)
	}
	h64, err := strconv.ParseUint(strings.TrimPrefix(os.Args[1], "0x"), 16, 64)
	if err != nil {
		fmt.Println("句柄不是十六进制：" + err.Error())
		os.Exit(2)
	}
	h := uintptr(h64)
	orig := title(h)
	fmt.Printf("hwnd=0x%x 原标题=「%s」\n", h, orig)
	ok, e := setTitle(h, orig+" [probe]")
	fmt.Printf("SetWindowTextW 返回=%v err=%v\n", ok, e)
	time.Sleep(300 * time.Millisecond)
	fmt.Printf("  改成后读回=「%s」\n", title(h))
	setTitle(h, orig)
	time.Sleep(300 * time.Millisecond)
	fmt.Printf("  还原后读回=「%s」\n", title(h))
}
