// 真机探针：**浏览器窗口能不能被压成侧条那么窄**（决定"窗口 ⇄ 侧栏 无缝切换"怎么设计）。
//
// 背景：用户问「这个窗口能和 ace 的侧栏无缝切换吗？把窗口贴到四周的时候自动吸附并转换」。
// 两条路：
//
//	A) 同一个**浏览器窗口**缩成细条（那就不需要原生细条了，最"无缝"）—— 前提是 Chromium
//	   允许把窗口缩到几十像素宽（它有最小窗口尺寸，可能自己弹回）。
//	B) 两个窗口接力（浏览器窗口 ⇄ 原生细条）：能贴边吸附，但**内容不连续**（页面状态会丢）。
//
// 这个探针把 A 的前提量出来：依次尝试几个宽度，**每次都回读**（跨进程 SetWindowPos 可能被拒）。
//
// 用法：go run ./devtools/probe-appwin [标题子串，默认 "ACETool"]
// 只动那个窗口的尺寸/位置，跑完**还原**。
package main

import (
	"fmt"
	"os"
	"time"

	"acetool/internal/win"
)

func main() {
	needle := "ACETool"
	if len(os.Args) > 1 {
		needle = os.Args[1]
	}
	ws, err := win.Find(needle)
	if err != nil || len(ws) == 0 {
		fmt.Printf("没找到标题含 %q 的窗口（先把独立窗口开出来）：%v\n", needle, err)
		os.Exit(1)
	}
	w := ws[0]
	orig, err := win.RectOf(w.Handle)
	if err != nil {
		fmt.Println("读矩形失败：" + err.Error())
		os.Exit(1)
	}
	fmt.Printf("目标 0x%x「%s」 原始 (%d,%d) %dx%d\n", w.Handle, w.Title, orig.Left, orig.Top, orig.W(), orig.H())
	defer func() {
		_ = win.Move(w.Handle, orig)
		fmt.Printf("已还原成 %dx%d\n", orig.W(), orig.H())
	}()

	for _, width := range []int32{300, 160, 100, 60, 34, 12} {
		want := win.Rect{Left: orig.Right - width, Top: orig.Top, Right: orig.Right, Bottom: orig.Bottom}
		err := win.Move(w.Handle, want)
		time.Sleep(350 * time.Millisecond) // 给对方一点时间自己弹回（Chromium 会在 WM_WINDOWPOSCHANGING 里纠正）
		got, rerr := win.RectOf(w.Handle)
		note := ""
		if err != nil {
			note = "Move 报错：" + err.Error()
		} else if rerr != nil {
			note = "回读失败：" + rerr.Error()
		} else if got.W() != width {
			note = fmt.Sprintf("**被拒/弹回**（要 %d 实际 %d）", width, got.W())
		}
		fmt.Printf("  想设宽 %4d ⇒ 实际 %4d × %4d  %s\n", width, got.W(), got.H(), note)
	}
}
