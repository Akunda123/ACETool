//go:build windows

package win

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AppWindow 用**浏览器 app 模式**开一个"单独显示这个网页"的窗口（用户 2026-10-16：
// 「我想要一个单独显示web的窗口」）。
//
// 做法：`<chromium 浏览器> --app=<url> --window-size=W,H [--window-position=X,Y]`
// ⇒ 一个**没有标签栏、没有地址栏**的独立窗口，看着就像个桌面应用。
//
// ⛔ 关于尺寸（2026-10-17 实测，别被坑第二次）：
//
//	· `--window-size` 的单位是 **DIP（逻辑像素）**，不是物理像素 —— 实测在本机 150% 缩放下
//	  `--window-size=500,780` 开出的是 **750×1170 物理**。
//	· 更坑的是：**用默认 profile 时这条常被无视** —— Chromium 会**记住 app 窗口上次的尺寸**，
//	  实测三个不同尺寸都开出 1257×1350（只有全新 `--user-data-dir` 时才严格生效）。
//	⇒ 所以真正定尺寸靠的是**开完自己摆**：快照 → 拉起窗口 → 认出"新出现的那个" →
//	  `SetWindowPos` 按逻辑尺寸 ×DPI 缩放后落位。命令行那两个参数只是"给个初值"。
//
// 为什么不内嵌 WebView2：那要写一整套 COM，而且给"单文件零依赖"添一个**运行时依赖**
// （WebView2 Runtime）。app 模式零依赖、复用现成能力（注册表读默认浏览器 + RunDetached）。
//
// 参数：`w,h` 是**逻辑像素**（页面看起来多宽）；`x,y` 给了才挪位置，`-1` = 位置交给浏览器记。
// 返回：用的哪个浏览器 / 是不是真的走了 app 模式 / **尺寸有没有摆上**（没赶上也如实说）。
func AppWindow(url string, x, y, w, h int) (exe string, appMode, sized bool, err error) {
	exe, err = pickChromium()
	if err != nil {
		return "", false, false, err
	}
	before := handleSet()
	args := []string{"--app=" + url}
	if w > 0 && h > 0 {
		args = append(args, fmt.Sprintf("--window-size=%d,%d", w, h))
	}
	// 位置：给了才传（没给就让浏览器自己记 —— app 窗口的位置 Chromium 自己会记）
	if x >= 0 || y >= 0 {
		args = append(args, fmt.Sprintf("--window-position=%d,%d", x, y))
	}
	if err := RunDetached(exe, args...); err != nil {
		return exe, false, false, err
	}
	if w > 0 && h > 0 {
		sized = placeNewWindow(before, x, y, w, h)
	}
	return exe, true, sized, nil
}

// handleSet 记一份"现在有哪些可见顶层窗口"，好认出**新出现的那个**。
func handleSet() map[uintptr]bool {
	m := map[uintptr]bool{}
	if ws, err := List(); err == nil {
		for _, w := range ws {
			m[w.Handle] = true
		}
	}
	return m
}

// placeNewWindow 等新窗口出现，然后按 **逻辑像素 × DPI 缩放** 摆到 (x,y) 并定成 w×h。
//
// 只有**一个**新窗口时才动手（多个同时出现说明有人在同时开窗口，瞎摆会摆错）。
// 位置 x/y < 0 ⇒ 只改尺寸、不动位置（`SWP_NOMOVE`）。
func placeNewWindow(before map[uintptr]bool, x, y, w, h int) bool {
	scale := DPIScale()
	pw, ph := int32(float64(w)*scale), int32(float64(h)*scale)
	for i := 0; i < 25; i++ { // 最多等 5 秒（Edge 冷启动要一两秒）
		time.Sleep(200 * time.Millisecond)
		ws, err := List()
		if err != nil {
			continue
		}
		var fresh []Window
		for _, win := range ws {
			if before[win.Handle] {
				continue
			}
			// ⚠️ Edge/Chromium 的类名是 `Chrome_WidgetWin_1`（**带下划线**）⇒ 只能比 `widgetwin`。
			// 第一版比的是 `chromewidgetwin`（没下划线），永远不命中 ⇒ 尺寸一直没摆上，
			// 直到 `/api/app-window` 回包里 `sized=false` 才露出来。
			if !strings.Contains(strings.ToLower(win.Class), "widgetwin") {
				continue
			}
			// 再排除**普通浏览器窗口**：它们的标题带 `- 个人 - Microsoft Edge` 这种尾巴，
			// app 窗口的标题就是网页标题本身 ⇒ 别把用户自己的浏览器窗口拉过来改尺寸。
			if strings.Contains(strings.ToLower(win.Title), "microsoft edge") ||
				strings.Contains(strings.ToLower(win.Title), " - google chrome") {
				continue
			}
			fresh = append(fresh, win)
		}
		if len(fresh) != 1 {
			continue // 还没出现 / 出现了好几个（没法判断是哪个 ⇒ 再等等）
		}
		hw := fresh[0].Handle
		if x >= 0 && y >= 0 {
			px, py := int32(float64(x)*scale), int32(float64(y)*scale)
			return Move(hw, Rect{Left: px, Top: py, Right: px + pw, Bottom: py + ph}) == nil
		}
		return Resize(hw, pw, ph) == nil
	}
	return false
}

// pickChromium 找一个**认 `--app` 的**浏览器：默认浏览器优先，不是 Chromium 就退到 Edge。
func pickChromium() (string, error) {
	if def, err := DefaultBrowserExe(); err == nil && isChromium(def) {
		return def, nil
	}
	for _, p := range []string{
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	// 最后再问一次注册表里的默认浏览器：不是 Chromium 也**给个结果**，让上层如实说"用不了 app 模式"
	if def, err := DefaultBrowserExe(); err == nil {
		return def, fmt.Errorf("默认浏览器（%s）看起来不是 Chromium 系，认不出 `--app`；也没在本机找到 Edge",
			filepath.Base(def))
	}
	return "", fmt.Errorf("认不出默认浏览器，也没找到 Edge")
}

// isChromium 判断这个 exe 是不是 Chromium 系（认 `--app`）。
func isChromium(exe string) bool {
	n := strings.ToLower(filepath.Base(exe))
	for _, k := range []string{"msedge", "chrome", "brave", "vivaldi", "opera", "thorium", "chromium"} {
		if strings.Contains(n, k) {
			return true
		}
	}
	return false
}
