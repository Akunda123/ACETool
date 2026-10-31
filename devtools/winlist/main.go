// 真机侦察：桌面上有哪些可见顶层窗口、ACE 的那两个窗口各是什么样。
//
// 为什么要有它：exe 侧栏要"贴 ACE 的窗口"，而 ACE 有**主窗 + 钢琴窗**两个顶层窗口
// （用户 2026-10-15：「ace 现在两个窗口：主窗口和钢琴窗」）⇒ 动手前先把它们的
// 句柄 / 类名 / 标题 / 矩形 / 是否最小化 / 谁是前台看清楚，别靠猜。
//
// 用法：go run ./devtools/winlist [进程名子串，默认 "ACE Studio"]
//
// 不动任何窗口，纯读。
package main

import (
	"fmt"
	"os"
	"strings"

	"acetool/internal/win"
)

func main() {
	// ⚠️ 必须 DPI 感知：不然量到的是**虚拟坐标**（1707x960），跟细条/吸附报的物理坐标对不上
	win.SetDPIAware()
	needle := "ACE Studio"
	if len(os.Args) > 1 {
		needle = os.Args[1]
	}

	all, err := win.List()
	if err != nil {
		fmt.Println("列窗口失败：" + err.Error())
		os.Exit(1)
	}
	fg := win.ForegroundWindow()
	fmt.Printf("可见顶层窗口 %d 个（本次进程 pid=%d，前台句柄=0x%x）\n\n", len(all), os.Getpid(), fg)

	// ① 与进程名匹配的那些进程（ACE Studio 的实际进程名是 `ACE Studio.exe`）
	pids := map[uint32]bool{}
	var exePids []uint32
	fmt.Printf("== 进程名含 %q\n", needle)
	for _, name := range []string{needle + ".exe", needle} {
		if ps, err := win.ByExeName(name); err == nil {
			for _, p := range ps {
				if !pids[p] {
					pids[p] = true
					exePids = append(exePids, p)
				}
			}
		}
	}
	if len(exePids) == 0 {
		fmt.Println("  没找到进程（试试别的名字）")
	} else {
		for _, p := range exePids {
			fmt.Printf("  pid %d\n", p)
		}
	}
	fmt.Println()

	// ② 逐个窗口：属于 ACE 的打 ★
	for _, w := range all {
		mark := "  "
		if pids[w.PID] {
			mark = "★ "
		}
		fgMark := ""
		if w.Handle == fg {
			fgMark = "  ← 前台"
		}
		r, err := win.RectOf(w.Handle)
		rect := ""
		if err != nil {
			rect = "取矩形失败：" + err.Error() // ⚠️ err 为 nil 时不能碰它（第一版就是这么崩的）
		} else {
			rect = fmt.Sprintf("(%d,%d)-(%d,%d) %dx%d", r.Left, r.Top, r.Right, r.Bottom, r.W(), r.H())
		}
		min := ""
		if win.Minimized(w.Handle) {
			min = "  [最小化]"
		}
		// ACE 那几个小窗是**弹出/工具窗**（有属主）⇒ 侧栏要贴的是"主窗"，这条是判据之一
		own := ""
		if o := win.Owner(w.Handle); o != 0 {
			own = fmt.Sprintf("  [属主 0x%x]", o)
		}
		verdict := ""
		if pids[w.PID] && !win.Minimized(w.Handle) {
			switch {
			case win.HasOwner(w.Handle):
				verdict = "  → 弹出窗（不贴）"
			case r.W() < 400 || r.H() < 300:
				verdict = "  → 太小（不贴）"
			default:
				verdict = "  → 主窗候选（可以贴）"
			}
		}
		// 客户区 vs 窗口矩形：贴边的小条要**从客户区顶边**开始，否则会压住关闭按钮。
		// 差值 ≈ 0 ⇒ 这个窗口是"自绘标题栏"（关闭按钮画在客户区里）⇒ 还得再让开一段。
		cli := ""
		if c, err := win.ClientRectOf(w.Handle); err == nil {
			cli = fmt.Sprintf("  客户区(%d,%d) 顶差=%d 左差=%d", c.Left, c.Top, c.Top-r.Top, c.Left-r.Left)
		}
		fmt.Printf("%s0x%-8x pid=%-6d %-24s %-34s %s%s%s%s\n",
			mark, w.Handle, w.PID, trunc(w.Class, 24), trunc(w.Title, 34), rect, min, own+verdict, fgMark)
		if pids[w.PID] && cli != "" {
			fmt.Printf("        %s\n", cli)
		}
	}
	fmt.Printf("\n系统标题栏总高（SM_CYCAPTION+边框）= %d px\n", win.SystemCaptionHeight())

	// ③ 结论：ACE 的那几个窗口到底是谁
	fmt.Println()
	if len(exePids) == 0 {
		fmt.Println("结论：没找到 ACE 进程 ⇒ 侧栏应当隐藏（" + "「ACE 关闭时隐藏」" + "这条的判据就是这个）")
		return
	}
	n := 0
	for _, w := range all {
		if pids[w.PID] {
			n++
		}
	}
	fmt.Printf("结论：ACE 有 %d 个可见顶层窗口（进程 %v）⇒ 侧栏跟「焦点那个」走\n", n, exePids)
}

func trunc(s string, n int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
