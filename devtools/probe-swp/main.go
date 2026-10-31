// probe-swp —— 最小复现：**同一个 hwnd 上，Go 的 `SetWindowPos(HWND_TOPMOST)` 到底设没设上**。
//
// 只需要一个句柄（十六进制），不枚举、不用内部包 —— 就是为了排除"我们自己的封装是否有问题"。
//
// 用法：go run ./devtools/probe-swp 0x1234 [topmost|notopmost]
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	u32                = syscall.NewLazyDLL("user32.dll")
	pSetWindowPos      = u32.NewProc("SetWindowPos")
	pGetWindowLongPtrW = u32.NewProc("GetWindowLongPtrW")
)

func ex(h uintptr) uintptr {
	v, _, _ := pGetWindowLongPtrW.Call(h, ^uintptr(19))
	return v
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("用法：go run ./devtools/probe-swp 0x1234 [topmost|notopmost]")
		os.Exit(2)
	}
	h64, err := strconv.ParseUint(strings.TrimPrefix(os.Args[1], "0x"), 16, 64)
	if err != nil {
		fmt.Println("句柄不是十六进制：" + err.Error())
		os.Exit(2)
	}
	h := uintptr(h64)
	// ⚠️ 这两个常量我第一版写错了（写成了 `^uintptr(1)` / `^uintptr(2)` = -2 / -3 ⇒ 传到的是
	//    **HWND_NOTOPMOST(=-2)** 和"非法值"，所以"返回成功但没变化"是必然的 —— 白查了一阵）。
	//    正解：HWND_TOPMOST = -1 = `^uintptr(0)`；HWND_NOTOPMOST = -2 = `^uintptr(1)`。
	after := ^uintptr(0) // HWND_TOPMOST = -1
	if len(os.Args) > 2 && os.Args[2] == "notopmost" {
		after = ^uintptr(1) // HWND_NOTOPMOST = -2
	}
	// 自检"这个调用到底进没进真函数"：给一个**非法标志**（Win32 会拒）
	// ⇒ 如果连非法标志都"返回成功"，说明参数根本没送到 SetWindowPos（这才是真问题）。
	if len(os.Args) > 2 && os.Args[2] == "badflag" {
		ret, _, err2 := pSetWindowPos.Call(h, 0, 0, 0, 0, 0, 0x80000000)
		fmt.Printf("非法标志调用：返回=%v err=%v（期望 false + ERROR_INVALID_PARAMETER）\n", ret != 0, err2)
		return
	}
	fmt.Printf("hwnd=0x%x  设之前 EXSTYLE=0x%08x（TOPMOST=%v）\n", h, ex(h), ex(h)&8 != 0)
	// 连做 3 次，每次之后按 0 / 50 / 500ms 分三次读 —— 把"没生效"和"设了又被改回去"分开
	for i := 1; i <= 3; i++ {
		ret, _, err2 := pSetWindowPos.Call(h, after, 0, 0, 0, 0, 0x0013) // NOSIZE|NOMOVE|NOACTIVATE
		fmt.Printf("第 %d 次：返回=%v err=%v\n", i, ret != 0, err2)
		for _, d := range []time.Duration{0, 50 * time.Millisecond, 450 * time.Millisecond} {
			if d > 0 {
				time.Sleep(d)
			}
			fmt.Printf("    +%4dms  EXSTYLE=0x%08x（TOPMOST=%v）\n", int(d/time.Millisecond), ex(h), ex(h)&8 != 0)
		}
	}
}
