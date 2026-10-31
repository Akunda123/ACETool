// railprobe —— 量"细条那个位置上，最上面到底是哪个窗口"。
//
// 为什么需要它：细条是**画在 ACE 窗口里面**的一条窄窗（贴着 ACE 右缘）。
// 它到底"看得见"还是"被 ACE 盖住了"，从 `/api/rail` 的 `visible` 看不出来 ——
// 那个字段只说"我们没把自己藏起来"（`ShowWindow(SW_HIDE)` 那条路）。
// 判据只能问系统：`WindowFromPoint(细条矩形中点)` 回谁，谁就是那个位置上**真正可见**的窗口。
//
//	回我们自己的细条（类 ACEToolRail）⇒ 看得见 ✅
//	回 ACE（类 Qt6101QWindowIcon）⇒ **被 ACE 盖住了**（用户看到的就是"侧栏消失了"）❌
//	回别的（浏览器/资源管理器…）⇒ 被那个窗口盖住
//
// 用法：go run ./devtools/railprobe [-at x,y] [-n 3] [-ms 400]
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
	pWindowFromPoint  = user32.NewProc("WindowFromPoint")
	pGetClassNameW    = user32.NewProc("GetClassNameW")
	pGetWindowTextW   = user32.NewProc("GetWindowTextW")
	pGetAncestor      = user32.NewProc("GetAncestor")
	pGetWindowThreadP = user32.NewProc("GetWindowThreadProcessId")
)

type point struct{ X, Y int32 }

type railState struct {
	Running  bool     `json:"running"`
	Visible  bool     `json:"visible"`
	Level    int      `json:"level"`
	TopMost  bool     `json:"topMost"`
	AceFore  bool     `json:"aceForeground"`
	HWND     uintptr  `json:"hwnd"`
	Rect     win.Rect `json:"rect"`
	Target   uintptr  `json:"targetHwnd"`
	Covered  uint32   `json:"coveredPid"`
	CoverCls string   `json:"coveredClass"`
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

func text(h uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := pGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

func class(h uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := pGetClassNameW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

func pidOf(h uintptr) uint32 {
	var p uint32
	pGetWindowThreadP.Call(h, uintptr(unsafe.Pointer(&p)))
	return p
}

func main() {
	at := flag.String("at", "", "要探的点（屏幕坐标 `x,y`）；省略 = 用 `/api/rail` 报的细条矩形中点")
	n := flag.Int("n", 3, "探几次")
	ms := flag.Int("ms", 400, "每次间隔（毫秒）")
	flag.Parse()
	win.SetDPIAware()

	u := base()
	var st railState
	for i := 0; i < *n; i++ {
		r, err := http.Get(u + "api/rail")
		if err != nil {
			fmt.Println("问不到 /api/rail：" + err.Error())
			os.Exit(1)
		}
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		_ = json.Unmarshal(body, &st)

		pt := point{}
		if *at != "" {
			fmt.Sscanf(*at, "%d,%d", &pt.X, &pt.Y)
		} else {
			// 细条可能只有 7px 宽 ⇒ 取**靠右**一点（贴 ACE 右缘那一侧），别落到边框外
			pt = point{X: st.Rect.Right - 3, Y: (st.Rect.Top + st.Rect.Bottom) / 2}
		}
		// ⚠️ `WindowFromPoint(POINT)` 的 POINT 是**按值传的结构体**（8 字节，amd64 上走寄存器），
		// 传指针是错的 —— 第一版就踩了：回回都命中 0x0（"那个点上没有窗口"）。
		// 按值传法：两个 int32 打包进一个 uintptr（低 32 位 = x，高 32 位 = y）。
		packed := uintptr(uint32(pt.X)) | uintptr(uint32(pt.Y))<<32
		top, _, _ := pWindowFromPoint.Call(packed)
		root, _, _ := pGetAncestor.Call(top, 2) // GA_ROOT = 2

		verdict := "?"
		switch class(root) {
		case "ACEToolRail", "ACEToolRailTip":
			verdict = "✅ 细条自己在最上面（看得见）"
		case "Qt6101QWindowIcon":
			verdict = "❌ **被 ACE 窗口盖住了**（用户看到的就是「侧栏消失了」）"
		default:
			verdict = "⚠️ 被别的窗口盖住（**按设计如此**：别的应用在 ACE 上面时，细条跟着一起被盖住，不许浮在别人上面）"
		}
		fmt.Printf("#%d 点(%d,%d) ⇒ 此刻最上面 0x%x 「%s」 class=%s pid=%d（根 0x%x）\n",
			i+1, pt.X, pt.Y, top, text(top), class(top), pidOf(top), root)
		fmt.Printf("    细条 hwnd=0x%x running=%v visible=%v level=%d topMost=%v aceForeground=%v\n",
			st.HWND, st.Running, st.Visible, st.Level, st.TopMost, st.AceFore)
		fmt.Printf("    此刻可见性：%s\n", verdict)

		// ⭐ 第二条判据：**细条是谁的子窗口**（第五轮的正解）。
		// ⚠️ 老判据（"z 序里细条在 ACE 之上/之下"）在子窗口架构下**没有意义**：
		// 子窗口**根本不参与顶层 z 序**（`EnumWindows` 都枚举不到它）—— 那正是这个架构的好处。
		// 所以这里改成量"父子关系 + 此刻那个点上是谁"：子窗口**不可能**被自己的父窗口盖住。
		parent := win.ParentOf(st.HWND)
		fmt.Printf("    父子：细条 0x%x 的父窗口=0x%x（ACE 目标 0x%x）⇒ %s\n",
			st.HWND, parent, st.Target, map[bool]string{true: "✅ 就是它（挂在 ACE 里面）", false: "❌ 不是 ACE（没挂上？）"}[parent == st.Target])
		if ws, err := win.ZOrder(); err == nil {
			found := false
			for _, w := range ws {
				if w.Handle == st.HWND {
					found = true
				}
			}
			if found {
				fmt.Printf("    ⚠️ 细条出现在**顶层** z 序里了（子窗口不该出现 ⇒ 它没挂上）\n")
			} else {
				fmt.Printf("    ✅ 细条不在顶层 z 序里（子窗口本就枚举不到 —— 这就是「不可能浮在别的应用上」的根据）\n")
			}
		}
		fmt.Printf("    面板报的：coveredPid=%d coveredClass=%s\n", st.Covered, st.CoverCls)
		// 贴着的那个窗口自己是不是 topmost？—— 非 topmost 的窗口**永远压不过** topmost 的窗口，
		// 那样"插到它上面"会**调用成功但位置不变**（真机 2026-10-17 撞到过：topCall=1 却一直在下面）。
		if st.Target != 0 {
			fmt.Printf("    ACE 目标窗口 0x%x：topmost=%v alive=%v class=%s\n",
				st.Target, win.IsTopMost(st.Target), win.Alive(st.Target), win.ClassOf(st.Target))
		}
		if i < *n-1 {
			time.Sleep(time.Duration(*ms) * time.Millisecond)
		}
	}
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }
