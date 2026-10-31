// railtry —— 试出"到底哪种原语能把细条摆在 ACE 之上、又不浮在别的应用上面"（真机取证）。
//
// 背景（2026-10-17）：细条是**画在 ACE 窗口身上**的一条窄窗。三种老做法都翻过车（见 rail_windows.go
// 的四层教训），第四轮改用 `SetWindowPos(hWndInsertAfter=ACE窗口)` —— 但真机量到：
// **返回成功、z 序却不动**（ACE 那个窗口是 owned 的钢琴窗，Windows 不让外进程窗口插进那个 owned 组）。
//
// 这个工具把候选原语一个一个试，每次试完**用事实核对**（`ZGap`：细条跟 ACE 隔几层）：
//
//	insert-main  插到 ACE 主窗之上
//	insert-top   插到 z 序里**最上面那个 ACE 窗口**之上（= 整个 ACE 组的顶上）
//	insert-other 插到**别的进程**的窗口之上（验"外进程窗口能不能被我们插上去"这条到底行不行）
//	bring        `BringWindowToTop(细条)`
//	topmost      细条置顶（已知可行，留作对照）
//	un-topmost   取消置顶
//	parent       把细条 `SetParent` 成 ACE 主窗的**子窗口**（子窗口天然在父窗客户区之上、且永不盖别的应用）
//	unparent     从父窗口摘出来（`SetParent(细条, 0)`），位置按屏幕坐标还原
//
// 用法：go run ./devtools/railtry -mode parent
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"acetool/internal/win"
)

var (
	user32            = syscall.NewLazyDLL("user32.dll")
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	pSetParent        = user32.NewProc("SetParent")
	pBringWindowToT   = user32.NewProc("BringWindowToTop")
	pScreenToClient   = user32.NewProc("ScreenToClient")
	pGetParent        = user32.NewProc("GetParent")
	pCreateWindowExW  = user32.NewProc("CreateWindowExW")
	pDestroyWindow    = user32.NewProc("DestroyWindow")
	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
)

type railState struct {
	Running bool     `json:"running"`
	Visible bool     `json:"visible"`
	HWND    uintptr  `json:"hwnd"`
	Parent  uintptr  `json:"parentHwnd"`
	Target  uintptr  `json:"targetHwnd"`
	TargetP uint32   `json:"targetPid"`
	LastErr string   `json:"lastErr"`
	Rect    win.Rect `json:"rect"`
}

// makeHostWindow 建一个**临时宿主窗口**（`killparent` 用）：把细条挂上去、再 DestroyWindow(它)
// ⇒ 系统连坐销毁子窗口 —— 这就是"ACE 关掉时我们被一起销毁"的等价情形，但不用碰用户的 ACE。
//
// 用**系统类 `STATIC`**（不必自己注册窗口类）：这个窗口只当容器，不画画、不接消息。
func makeHostWindow() uintptr {
	hInst, _, _ := pGetModuleHandleW.Call(0)
	hwnd, _, _ := pCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(mustPtr("STATIC"))), uintptr(unsafe.Pointer(mustPtr("ACETool 宿主测试"))),
		0x80000000 /*WS_POPUP*/, 0, 0, 200, 200, 0, 0, hInst, 0)
	return hwnd
}

func mustPtr(s string) *uint16 {
	p, err := syscall.UTF16FromString(s)
	if err != nil {
		return nil
	}
	return &p[0]
}

func base() string {
	b, err := os.ReadFile(filepath.Join(os.Getenv("APPDATA"), "ACETool", "panel.json"))
	if err != nil {
		fmt.Println("读不到 panel.json：" + err.Error())
		os.Exit(1)
	}
	var v struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(b, &v)
	return v.URL
}

func state(u string) railState {
	r, err := http.Get(u + "api/rail")
	if err != nil {
		fmt.Println("问不到 /api/rail：" + err.Error())
		os.Exit(1)
	}
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	var st railState
	_ = json.Unmarshal(body, &st)
	return st
}

// report 打一份"事实核对"：细条在不在、跟 ACE 差几层、那个点上真正看得见谁。
func report(tag string, rail, target uintptr, u string) {
	st := state(u)
	gap, both := win.ZGap(rail, target)
	pos := "?"
	switch {
	case !both:
		pos = "（有一个不在可见窗口里）"
	case gap == 1:
		pos = "✅ **紧贴 ACE 之上**"
	case gap > 1:
		pos = fmt.Sprintf("⚠️ 在 ACE 之上但中间夹着 %d 个", gap-1)
	default:
		pos = fmt.Sprintf("❌ 在 ACE **之下** %d 层", -gap)
	}
	at := win.RootOf(win.WindowAt((st.Rect.Left+st.Rect.Right)/2, (st.Rect.Top+st.Rect.Bottom)/2))
	who := "（那个点上没有窗口）"
	if at != 0 {
		who = fmt.Sprintf("0x%x class=%s pid=%d", at, win.ClassOf(at), win.PIDOf(at))
		if at == rail {
			who += " ← 就是细条自己（看得见）"
		} else if win.PIDOf(at) == win.PIDOf(target) {
			who += " ← **ACE 自己**（被 ACE 盖住）"
		}
	}
	parent, _, _ := pGetParent.Call(rail)
	fmt.Printf("  [%s] z 序 gap=%d %s · 父窗口=0x%x · 点上最上面=%s\n", tag, gap, pos, parent, who)
}

func main() {
	mode := flag.String("mode", "report", "insert-main/insert-top/insert-other/bring/topmost/un-topmost/parent/unparent/report")
	wait := flag.Int("wait", 700, "每步之后等多久再核对（毫秒）")
	flag.Parse()
	win.SetDPIAware()

	u := base()
	st := state(u)
	if !st.Running || st.HWND == 0 {
		fmt.Printf("细条没在跑/没句柄（running=%v hwnd=0x%x）\n", st.Running, st.HWND)
		os.Exit(1)
	}
	rail, target := st.HWND, st.Target
	fmt.Printf("细条 0x%x（topmost=%v） · ACE 目标 0x%x pid=%d（topmost=%v）\n",
		rail, win.IsTopMost(rail), target, st.TargetP, win.IsTopMost(target))
	report("开始", rail, target, u)

	// ACE 的"主窗"= z 序里**最下面**那个 ACE 顶层窗口（owned 的那些在它上面）；
	// "顶"= z 序里最上面那个（第四轮量到的就是 owned 的钢琴窗）。
	var main, top uintptr
	if ws, err := win.ZOrder(); err == nil {
		for i := len(ws) - 1; i >= 0; i-- {
			if ws[i].PID == st.TargetP {
				main = ws[i].Handle
				break
			}
		}
		for _, w := range ws {
			if w.PID == st.TargetP {
				top = w.Handle
				break
			}
		}
	}
	fmt.Printf("ACE 组：最上面 0x%x · 最下面（主窗）0x%x\n", top, main)

	switch *mode {
	case "report":
		return
	case "insert-main":
		fmt.Println("插到 ACE 主窗之上：" + errStr(win.RailInsertAbove(rail, main)))
	case "insert-top":
		fmt.Println("插到 ACE 组顶上那个窗口之上：" + errStr(win.RailInsertAbove(rail, top)))
	case "insert-other":
		// 拿**前台窗口**当参照（它通常是别的进程）
		fg := win.Foreground()
		fmt.Printf("插到前台窗口（别的进程）0x%x 之上：%s\n", fg, errStr(win.RailInsertAbove(rail, fg)))
	case "bring":
		ret, _, err := pBringWindowToT.Call(rail)
		fmt.Printf("BringWindowToTop ⇒ ret=%d err=%v\n", ret, err)
	case "topmost":
		fmt.Println("置顶：" + errStr(win.SetTopMost(rail, true)))
	case "un-topmost":
		fmt.Println("取消置顶：" + errStr(win.SetTopMost(rail, false)))
	case "parent":
		// 子窗口的坐标是**父窗口客户区**坐标 ⇒ 先把当前屏幕矩形换算成父客户区坐标再 SetParent，
		// 否则窗口会跳（SetParent 之后原来的屏幕坐标会被当成客户区坐标解释）。
		rc, _ := win.RectOf(rail)
		pt := struct{ X, Y int32 }{rc.Left, rc.Top}
		pScreenToClient.Call(main, uintptr(unsafe.Pointer(&pt)))
		fmt.Printf("SetParent(细条, ACE主窗 0x%x)；屏幕 (%d,%d) ⇒ 主窗客户区 (%d,%d)\n", main, rc.Left, rc.Top, pt.X, pt.Y)
		prev, _, _ := pSetParent.Call(rail, main)
		fmt.Printf("  老父窗口=0x%x\n", prev)
		// 换算后的客户区坐标 + 原尺寸
		win.Move(rail, win.Rect{Left: pt.X, Top: pt.Y, Right: pt.X + rc.W(), Bottom: pt.Y + rc.H()})
	case "unparent":
		prev, _, _ := pSetParent.Call(rail, 0)
		fmt.Printf("SetParent(细条, 0) ⇒ 老父窗口=0x%x\n", prev)
		rc, _ := win.RectOf(rail)
		win.Move(rail, rc)
	case "owner":
		// 认 ACE 主窗当**属主**（`GWLP_HWNDPARENT`）：Windows 保证"owned 窗口恒在其属主之上"，
		// 而别的应用盖住属主时，owned 窗口**跟着一起被盖住** ⇒ 正是要的语义。
		// ⚠️ 历史教训②是"认属主 **+ 每轮升 Z 序**"（升 Z 序会把属主一起提上来 ⇒ 主窗/钢琴窗轮流压对方）。
		// 这里**只认一次、不升**，看这条原语单独成立不成立。
		old, err := win.SetOwner(rail, main)
		fmt.Printf("SetOwner(细条 → ACE主窗 0x%x) ⇒ 老属主=0x%x err=%v\n", main, old, err)
	case "unowner":
		old, err := win.SetOwner(rail, 0)
		fmt.Printf("SetOwner(细条 → 0) ⇒ 老属主=0x%x err=%v\n", old, err)
	case "killparent":
		// 验「父窗口没了 ⇒ 我们被连坐销毁 ⇒ exe 自动重建」这条路，**不用去关用户的 ACE**：
		// 建一个临时宿主窗口，把细条 SetParent 过去，然后立刻 DestroyWindow(宿主) ——
		// 系统会连坐销毁子窗口（细条），exe 下一拍（120ms）应当发现句柄死了就重建、并重新挂回 ACE。
		// ⚠️ 必须**同一次调用里**马上销毁：exe 每 120ms 会把细条重新挂回 ACE（父窗口变了就重挂），
		// 一睡就白验了。
		host := makeHostWindow()
		if host == 0 {
			fmt.Println("建临时宿主窗口失败")
			os.Exit(1)
		}
		fmt.Printf("临时宿主 0x%x 建好 ⇒ SetParent(细条, 宿主)\n", host)
		pSetParent.Call(rail, host)
		fmt.Printf("立刻 DestroyWindow(宿主) ⇒ 细条应当被**连坐销毁**\n")
		pDestroyWindow.Call(host)
		fmt.Println("（下面等 1.5 秒看 exe 有没有自己重建：hwnd 应当变成新句柄、父窗口回到 ACE）")
		time.Sleep(1500 * time.Millisecond)
		st2 := state(u)
		fmt.Printf("  重建后：hwnd=0x%x parentHwnd=0x%x target=0x%x visible=%v running=%v lastErr=「%s」\n",
			st2.HWND, st2.Parent, st2.Target, st2.Visible, st2.Running, st2.LastErr)
		if st2.HWND != 0 && st2.HWND != rail {
			fmt.Println("  ✅ 句柄换了 ⇒ 它确实重建了")
		} else {
			fmt.Println("  ⚠️ 句柄没变（要么没被销毁、要么重建失败）")
		}
		if st2.Parent == st2.Target && st2.Target != 0 {
			fmt.Println("  ✅ 重建后重新挂回了 ACE（父窗口 = 目标窗口）")
		} else {
			fmt.Println("  ⚠️ 重建后没挂回 ACE")
		}
	}
	time.Sleep(time.Duration(*wait) * time.Millisecond)
	report("做完之后", rail, target, u)
}

func errStr(err error) string {
	if err == nil {
		return "调用成功（**但不代表层级真的变了** —— 看下面的事实核对）"
	}
	return "失败：" + err.Error()
}
