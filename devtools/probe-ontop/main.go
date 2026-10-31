// probe-ontop —— 探针：**跨进程「置顶」到底生效没**，以及**哪种调用方式算数**。
//
// 为什么要它（2026-10-17）：把面板改成浏览器 **app 模式**窗口之后，`check-ontop.js` 的
// "真窗口往返"那条一度不过 —— 于是怀疑 `SetWindowPos(HWND_TOPMOST)` 在 Chromium app 窗口上
// 不生效。本文件把几种调用方式依次试一遍（每次之间恢复原状），逐条核对 `WS_EX_TOPMOST`。
//
// ⚠️ 这一轮最后查出来的**不是** app 窗口的问题，而是两件我自己的错（见 HANDOVER §80）：
//
//	① 探针里常量写错：`HWND_TOPMOST` 写成了 `^uintptr(1)`（= **-2 = NOTOPMOST**）、
//	   `HWND_NOTOPMOST` 写成 `^uintptr(2)`（= -3 非法）⇒ "返回成功但样式位没变"是必然的；
//	② 还原那段逻辑写反了 ⇒ 探针跑完把窗口**留在了最上面**。
//	正解：`HWND_TOPMOST = -1 = ^uintptr(0)`；`HWND_NOTOPMOST = -2 = ^uintptr(1)`
//	（`internal/win/window_windows.go` 里本来就是对的）。
//
// 顺带记下真机事实：Chromium（Edge）窗口的 `WS_EX` 带 **0x00200000 = WS_EX_NOREDIRECTIONBITMAP**
// （直接走 DirectComposition 合成），ACE 的 Qt 窗口没有这一位；二者都能正常置顶。
//
// 用法：go run ./devtools/probe-ontop [标题子串，默认 "ACETool"] [-nodpi]
package main

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"acetool/internal/win"
)

var (
	u32                = syscall.NewLazyDLL("user32.dll")
	pSetWindowPos      = u32.NewProc("SetWindowPos")
	pGetWindowLongPtrW = u32.NewProc("GetWindowLongPtrW")
	pSetWindowLongPtrW = u32.NewProc("SetWindowLongPtrW")
)

const (
	hwndTopMost   = ^uintptr(0) // HWND_TOPMOST = -1
	hwndNoTopMost = ^uintptr(1) // HWND_NOTOPMOST = -2

	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoActivate = 0x0010
	swpFrameChg   = 0x0020

	gwlExStyle  = ^uintptr(19) // GWL_EXSTYLE = -20
	wsExTopMost = 0x00000008
	wsExToolWin = 0x00000080
	wsExNoAct   = 0x08000000
	wsExAppWin  = 0x00040000
)

func exStyle(h uintptr) uintptr {
	v, _, _ := pGetWindowLongPtrW.Call(h, gwlExStyle)
	return v
}

func try(h uintptr, name string, after uintptr, flags uintptr) {
	ret, _, err := pSetWindowPos.Call(h, after, 0, 0, 0, 0, flags)
	fmt.Printf("  %-46s 返回=%v  置顶=%v", name, ret != 0, win.IsTopMost(h))
	if ret == 0 {
		fmt.Printf("  err=%v", err)
	}
	fmt.Println()
}

func main() {
	needle := "ACETool"
	noDPI := false
	for _, a := range os.Args[1:] {
		if a == "-nodpi" {
			noDPI = true
			continue
		}
		needle = a
	}
	if noDPI {
		fmt.Println("⚠️ 本进程**不**声明 DPI 感知（用来对比：置顶失败是不是 DPI 上下文的锅）")
	} else {
		win.SetDPIAware()
	}
	ws, err := win.Find(needle)
	if err != nil || len(ws) == 0 {
		fmt.Printf("没找到标题含 %q 的窗口（先把面板窗口开出来）：%v\n", needle, err)
		os.Exit(1)
	}
	w := ws[0]
	rc, _ := win.RectOf(w.Handle)
	fmt.Printf("目标 0x%x  pid=%d  class=%s「%s」 %dx%d\n", w.Handle, w.PID, w.Class, w.Title, rc.W(), rc.H())
	ex := exStyle(w.Handle)
	fmt.Printf("  EXSTYLE=0x%08x  （TOPMOST=%v TOOLWINDOW=%v APPWINDOW=%v NOACTIVATE=%v NOREDIR=%v）\n",
		ex, ex&wsExTopMost != 0, ex&wsExToolWin != 0, ex&wsExAppWin != 0, ex&wsExNoAct != 0,
		ex&0x00200000 != 0)
	was := win.IsTopMost(w.Handle)
	fmt.Printf("  起始置顶=%v\n\n", was)

	base := uintptr(swpNoMove | swpNoSize | swpNoActivate)
	try(w.Handle, "A) SetWindowPos TOPMOST + NOMOVE|NOSIZE|NOACTIVATE", hwndTopMost, base)
	try(w.Handle, "A') 立刻再读一次（同一调用，不 sleep）", hwndTopMost, base)
	time.Sleep(300 * time.Millisecond)
	fmt.Printf("     300ms 后置顶=%v\n", win.IsTopMost(w.Handle))
	try(w.Handle, "B) TOPMOST + NOMOVE|NOSIZE（**带激活**）", hwndTopMost, uintptr(swpNoMove|swpNoSize))
	time.Sleep(300 * time.Millisecond)
	fmt.Printf("     300ms 后置顶=%v\n", win.IsTopMost(w.Handle))

	// C) 直接改扩展样式位（跨进程能不能改是另一件事，这里只量结果）
	v := exStyle(w.Handle)
	if _, _, e := pSetWindowLongPtrW.Call(w.Handle, gwlExStyle, v|wsExTopMost); e != nil &&
		e.Error() != "The operation completed successfully." {
		fmt.Printf("  C) SetWindowLongPtrW 失败：%v\n", e)
	} else {
		pSetWindowPos.Call(w.Handle, 0, 0, 0, 0, 0, uintptr(swpNoMove|swpNoSize|swpNoActivate|swpFrameChg))
		fmt.Printf("  %-46s 置顶=%v\n", "C) SetWindowLongPtrW(EXSTYLE|TOPMOST)+FRAMECHANGED", win.IsTopMost(w.Handle))
	}
	// D) 连做两次
	try(w.Handle, "D) TOPMOST 连做两次（第 1 次）", hwndTopMost, base)
	try(w.Handle, "D) TOPMOST 连做两次（第 2 次）", hwndTopMost, base)

	// 恢复原状（**必须核对**：第一版这里写成了 `if !IsTopMost == was`，逻辑反了 ⇒
	// 探针跑完把窗口**留在了最上面**，还得手动点一次 📌 才收回去）
	if win.IsTopMost(w.Handle) == was {
		fmt.Println("\n（已是原状，不用还原）")
	} else if err := win.SetTopMost(w.Handle, was); err != nil {
		fmt.Printf("\n还原失败：%v（⚠️ 这个窗口现在置顶=%v，起始是 %v）\n",
			err, win.IsTopMost(w.Handle), was)
	}
	time.Sleep(200 * time.Millisecond)
	fmt.Printf("\n还原后置顶=%v（起始是 %v）\n", win.IsTopMost(w.Handle), was)
}
