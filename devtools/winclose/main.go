// winclose —— 按标题（前缀/完全一致）找顶层窗口并给它发 `WM_CLOSE`（就是点那个 X 的效果）。
//
// 用途：复现"关掉某个窗口之后会怎样"这类症状（用户 2026-10-17：「关闭 web 面板后侧栏会消失」）。
// 也可以拿它**关掉我们自己测试时开出来的窗口**（别去关用户的窗口 —— 只认你给的那个标题）。
//
// 用法：go run ./devtools/winclose -title ACETool [-exact] [-dry]
package main

import (
	"flag"
	"fmt"
	"os"

	"acetool/internal/win"
)

func main() {
	title := flag.String("title", "", "标题（必填）")
	exact := flag.Bool("exact", false, "标题必须**完全一致**（默认按前缀）")
	dry := flag.Bool("dry", false, "只列出来，不关")
	flag.Parse()
	// 报出来的坐标必须是**物理像素**（和 winlist 一致）—— 不声明 DPI 感知的话，
	// 系统会把坐标虚拟化成逻辑像素（150% 屏上 1080x1140 会报成 720x760，
	// 曾让人以为"尺寸没摆上"，见 HANDOVER §80）。
	win.SetDPIAware()
	if *title == "" {
		fmt.Println("要给 -title（如 `ACETool`）；-exact 表示标题完全一致，默认按前缀")
		os.Exit(2)
	}
	var ws []win.Window
	var err error
	if *exact {
		ws, err = win.FindExact(*title)
	} else {
		ws, err = win.FindPrefixed(*title)
	}
	if err != nil {
		fmt.Println("找窗口失败：" + err.Error())
		os.Exit(1)
	}
	if len(ws) == 0 {
		fmt.Printf("没有标题%s %q 的可见顶层窗口\n", map[bool]string{true: "正好是", false: "以"}[*exact], *title)
		os.Exit(3)
	}
	for _, w := range ws {
		rc, _ := win.RectOf(w.Handle)
		fmt.Printf("0x%x pid=%d %s「%s」(%d,%d)-(%d,%d)\n", w.Handle, w.PID, w.Class, w.Title, rc.Left, rc.Top, rc.Right, rc.Bottom)
		if *dry {
			continue
		}
		if err := win.Close(w.Handle); err != nil {
			fmt.Printf("  关它失败：%v\n", err)
			continue
		}
		fmt.Println("  已发 WM_CLOSE")
	}
}
