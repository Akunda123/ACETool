// 屏幕差异探针：连拍两帧 → 找**变化像素的包围盒** → 报出那块区域是**哪个窗口**（类名 + 标题）。
//
// 为什么需要它（用户 2026-10-16：「一直在闪」「还在闪」）：闪 = 某块屏幕区域在**高频重绘**。
// 光看一帧看不出来，靠猜更不行（我已经猜错一次：以为是细条抢层级）。这个探针直接把
// "哪块区域在变 + 那块的窗口是谁" 报出来 —— 一次就能定位。
//
// 用法：go run ./devtools/screendiff [-gap 250] [-rounds 3]
//
//	-gap     两次采样间隔（毫秒）
//	-rounds  采样几轮（每轮报一次）
//
// 只读：只截屏 + 查询窗口，不动任何东西。
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"acetool/internal/win"
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")
	gdi32  = syscall.NewLazyDLL("gdi32.dll")

	pGetDC            = user32.NewProc("GetDC")
	pReleaseDC        = user32.NewProc("ReleaseDC")
	pGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	pWindowFromPoint  = user32.NewProc("WindowFromPoint")
	pGetClassNameW    = user32.NewProc("GetClassNameW")
	pGetWindowTextW   = user32.NewProc("GetWindowTextW")
	pGetAncestor      = user32.NewProc("GetAncestor")
	pPrintWindow      = user32.NewProc("PrintWindow")

	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pBitBlt             = gdi32.NewProc("BitBlt")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
)

const (
	smCxScreen = 0
	smCyScreen = 1
	srcCopy    = 0x00CC0020
	gaRoot     = 2
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

type point struct{ X, Y int32 }

// grab 截一张全屏（32 位、自顶向下）。
func grab(w, h int) ([]byte, error) {
	screen, _, _ := pGetDC.Call(0)
	if screen == 0 {
		return nil, fmt.Errorf("GetDC(NULL) 失败")
	}
	defer pReleaseDC.Call(0, screen)
	mem, _, _ := pCreateCompatibleDC.Call(screen)
	if mem == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC 失败")
	}
	defer pDeleteDC.Call(mem)

	bi := bitmapInfo{}
	bi.Header.Size = uint32(unsafe.Sizeof(bi.Header))
	bi.Header.Width = int32(w)
	bi.Header.Height = -int32(h) // 负高度 = 自顶向下，省得再翻
	bi.Header.Planes = 1
	bi.Header.BitCount = 32
	bi.Header.Compression = 0 // BI_RGB
	var bits unsafe.Pointer
	dib, _, _ := pCreateDIBSection.Call(mem, uintptr(unsafe.Pointer(&bi)), 0, /*DIB_RGB_COLORS*/
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if dib == 0 || bits == nil {
		return nil, fmt.Errorf("CreateDIBSection 失败")
	}
	defer pDeleteObject.Call(dib)
	old, _, _ := pSelectObject.Call(mem, dib)
	pBitBlt.Call(mem, 0, 0, uintptr(w), uintptr(h), screen, 0, 0, srcCopy)
	pSelectObject.Call(mem, old)
	out := make([]byte, w*h*4)
	copy(out, unsafe.Slice((*byte)(bits), len(out)))
	return out, nil
}

// toPNG 把 buffer 存成 PNG（留证据用）。
func toPNG(buf []byte, w, h int, path string) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			// BGRA → RGBA
			img.Set(x, y, color.RGBA{R: buf[i+2], G: buf[i+1], B: buf[i], A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	_ = png.Encode(f, img)
}

func windowAt(x, y int32) (uintptr, string, string) {
	h, _, _ := pWindowFromPoint.Call(packPoint(x, y))
	if h == 0 {
		return 0, "", ""
	}
	root, _, _ := pGetAncestor.Call(h, gaRoot)
	if root != 0 {
		h = root
	}
	var cbuf [256]uint16
	n, _, _ := pGetClassNameW.Call(h, uintptr(unsafe.Pointer(&cbuf[0])), uintptr(len(cbuf)))
	cls := syscall.UTF16ToString(cbuf[:n])
	var tbuf [512]uint16
	n2, _, _ := pGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&tbuf[0])), uintptr(len(tbuf)))
	title := syscall.UTF16ToString(tbuf[:n2])
	return h, cls, title
}

// packPoint 把两个 int32 打包成一个 64 位整数（Windows 的 POINT 按值传参就是这个布局）。
func packPoint(x, y int32) uintptr {
	return uintptr(uint32(x)) | uintptr(uint32(y))<<32
}

func main() {
	gap := flag.Int("gap", 200, "两次采样间隔（毫秒）")
	rounds := flag.Int("rounds", 3, "采样几轮")
	watch := flag.Int("watch", 0, "**连续盯 N 秒**（后台跑）：只报「有明显重绘」的那几轮 + 那块是哪个窗口")
	window := flag.String("window", "", "只拍**这个标题前缀的窗口内部**（`PrintWindow`，**被别的窗口盖住也能拍**）")
	hwndFlag := flag.String("hwnd", "", "只拍**这个句柄**的窗口内部（十六进制，如 0x90822）—— ACE 有好几个同名窗口，按句柄最准")
	flag.Parse()

	win.SetDPIAware() // 否则截到的是虚拟分辨率，跟窗口坐标对不上

	if *hwndFlag != "" {
		var h uint64
		if _, err := fmt.Sscanf(strings.TrimPrefix(strings.ToLower(*hwndFlag), "0x"), "%x", &h); err != nil {
			fmt.Println("hwnd 解析失败（要十六进制，如 0x90822）：" + err.Error())
			os.Exit(1)
		}
		probeHandle(uintptr(h), *rounds)
		return
	}
	if *window != "" {
		probeWindow(*window, *rounds)
		return
	}

	// ⚠️ 间隔**不规则**：周期正好等于采样间隔的动画会被"混叠"成 0 差异（我第一版就被坑了一次）。
	gaps := []int{*gap, *gap * 3 / 2, *gap*2/3 + 37}
	cw, _, _ := pGetSystemMetrics.Call(smCxScreen)
	ch, _, _ := pGetSystemMetrics.Call(smCyScreen)
	w, h := int(cw), int(ch)

	// 连续盯模式：后台跑几十秒，只报"有明显重绘"的瞬间。
	// 为什么需要它：钢琴窗闪的时候用户在看着它（ACE 在前台），而这会儿他不可能同时在看我消息
	// ⇒ 前台窗口随时会变，**只有持续采样才抓得到**。
	if *watch > 0 {
		fmt.Printf("连续盯 %d 秒（屏幕 %dx%d）：只报「有明显重绘」的瞬间 + 那块的窗口\n", *watch, w, h)
		deadline := time.Now().Add(time.Duration(*watch) * time.Second)
		n, hits := 0, 0
		for time.Now().Before(deadline) {
			n++
			a, err := grab(w, h)
			if err != nil {
				fmt.Println("截屏失败：" + err.Error())
				os.Exit(1)
			}
			time.Sleep(time.Duration(gaps[n%len(gaps)]) * time.Millisecond)
			b, err := grab(w, h)
			if err != nil {
				fmt.Println("截屏失败：" + err.Error())
				os.Exit(1)
			}
			changed := 0
			hot := map[int]int{} // 块序号 → 变化像素
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					i := (y*w + x) * 4
					if abs(int(a[i])-int(b[i]))+abs(int(a[i+1])-int(b[i+1]))+abs(int(a[i+2])-int(b[i+2])) > 24 {
						changed++
						hot[(y/64)*(w/64+1)+x/64]++
					}
				}
			}
			if changed > 1200 { // 只报"够大"的重绘（VS Code 光标那种几百像素的噪音不报）
				hits++
				var bestK, bestN int
				for k, v := range hot {
					if v > bestN {
						bestK, bestN = k, v
					}
				}
				bx := (bestK % (w/64 + 1)) * 64
				by := (bestK / (w/64 + 1)) * 64
				hw, cls, title := windowAt(int32(bx+32), int32(by+32))
				fmt.Printf("[%s] 变了 %5d 像素 · 最热块 (%4d,%4d) %4d 像素 ⇒ 0x%x %s「%s」\n",
					time.Now().Format("15:04:05"), changed, bx, by, bestN, hw, cls, title)
			}
			time.Sleep(120 * time.Millisecond)
		}
		fmt.Printf("盯完：采样 %d 轮，够大的重绘 %d 次\n", n, hits)
		return
	}

	fmt.Printf("屏幕 %dx%d · 间隔 %v ms · %d 轮\n\n", w, h, gaps, *rounds)

	outDir := filepath.Join(os.TempDir(), "acetool-screendiff")
	_ = os.MkdirAll(outDir, 0o755)
	for r := 1; r <= *rounds; r++ {
		a, err := grab(w, h)
		if err != nil {
			fmt.Println("截屏失败：" + err.Error())
			os.Exit(1)
		}
		time.Sleep(time.Duration(gaps[(r-1)%len(gaps)]) * time.Millisecond)
		b, err := grab(w, h)
		if err != nil {
			fmt.Println("截屏失败：" + err.Error())
			os.Exit(1)
		}
		// 差异：为了抗噪，单个像素颜色差 > 24 才算"变了"
		var n, minX, minY, maxX, maxY int
		minX, minY = w, h
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				i := (y*w + x) * 4
				d := abs(int(a[i])-int(b[i])) + abs(int(a[i+1])-int(b[i+1])) + abs(int(a[i+2])-int(b[i+2]))
				if d > 24 {
					n++
					if x < minX {
						minX = x
					}
					if y < minY {
						minY = y
					}
					if x > maxX {
						maxX = x
					}
					if y > maxY {
						maxY = y
					}
				}
			}
		}
		fmt.Printf("第 %d 轮：变了 %d 像素（包围盒 (%d,%d)-(%d,%d)）\n", r, n, minX, minY, maxX, maxY)
		if n > 0 {
			// ⚠️ 只报"包围盒中心是哪个窗口"在**多处同时变**时会乱指（VS Code 的光标 + 别处）⇒
			//    改成**分块热点**：64×64 一块，逐块数变化像素，把最热的前 5 块连同它的窗口报出来。
			reportHotBlocks(a, b, w, h)
			if n > 4000 {
				p1 := filepath.Join(outDir, fmt.Sprintf("r%d-a.png", r))
				p2 := filepath.Join(outDir, fmt.Sprintf("r%d-b.png", r))
				toPNG(a, w, h, p1)
				toPNG(b, w, h, p2)
				fmt.Printf("            两帧已存：%s / %s\n", p1, p2)
			}
		}
	}
}

// reportHotBlocks 按 64×64 分块统计变化像素，报最热的前 5 块 + 那块中心是哪个窗口。
func reportHotBlocks(a, b []byte, w, h int) {
	const bs = 64
	type blk struct {
		x, y, n int
	}
	var hot []blk
	for by := 0; by < h; by += bs {
		for bx := 0; bx < w; bx += bs {
			cnt := 0
			for y := by; y < by+bs && y < h; y++ {
				for x := bx; x < bx+bs && x < w; x++ {
					i := (y*w + x) * 4
					if abs(int(a[i])-int(b[i]))+abs(int(a[i+1])-int(b[i+1]))+abs(int(a[i+2])-int(b[i+2])) > 24 {
						cnt++
					}
				}
			}
			if cnt > 40 { // 一块 4096 像素里变 40 个以上才算"在动"
				hot = append(hot, blk{bx, by, cnt})
			}
		}
	}
	if len(hot) == 0 {
		fmt.Println("            没有明显热点（都在 40 像素/块 以下）")
		return
	}
	for i := 0; i < len(hot); i++ { // 选前 5（块数不多，直接选择排序）
		for j := i + 1; j < len(hot); j++ {
			if hot[j].n > hot[i].n {
				hot[i], hot[j] = hot[j], hot[i]
			}
		}
	}
	if len(hot) > 5 {
		hot = hot[:5]
	}
	for _, b2 := range hot {
		hw, cls, title := windowAt(int32(b2.x+bs/2), int32(b2.y+bs/2))
		fmt.Printf("            热点 (%4d,%4d) 变了 %4d 像素 ⇒ 0x%x %s 「%s」\n",
			b2.x, b2.y, b2.n, hw, cls, title)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// probeWindow 用 `PrintWindow` **直接拍某个窗口的内部**（被别的窗口盖住也能拍），连拍两次比差异。
//
// 为什么需要它：这台机器上 VS Code 几乎铺满全屏 ⇒ 拍屏时别的窗口都被盖住，
// 用户说"某个窗口在闪"我这边看不到。`PrintWindow` 拿的是**窗口自己渲染的内容**，跟遮挡无关。
func probeWindow(titleSub string, rounds int) {
	ws, err := win.FindPrefixed(titleSub)
	if err != nil || len(ws) == 0 {
		fmt.Printf("没找到标题前缀 %q 的窗口（%v）\n", titleSub, err)
		os.Exit(1)
	}
	// 挑第一个可见、没最小化的
	var t win.Window
	for _, w := range ws {
		if !win.Minimized(w.Handle) {
			t = w
			break
		}
	}
	if t.Handle == 0 {
		fmt.Printf("标题前缀 %q 的窗口都最小化了\n", titleSub)
		os.Exit(1)
	}
	probeHandle(t.Handle, rounds)
}

// probeHandle 按**句柄**精确指定（ACE 有主窗/钢琴窗/几个小弹窗，标题完全一样 ⇒ 按标题会挑错）。
func probeHandle(hwnd uintptr, rounds int) {
	if !win.Alive(hwnd) {
		fmt.Printf("句柄 0x%x 不是活窗口（ACE 重开过？用 winlist 重新拿一次）\n", hwnd)
		os.Exit(1)
	}
	r, err := win.RectOf(hwnd)
	if err != nil {
		fmt.Println("读矩形失败：" + err.Error())
		os.Exit(1)
	}
	var cls [256]uint16
	n, _, _ := pGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&cls[0])), uintptr(len(cls)))
	var ttl [512]uint16
	n2, _, _ := pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&ttl[0])), uintptr(len(ttl)))
	W, H := int(r.W()), int(r.H())
	fmt.Printf("窗口 0x%x %s 「%s」 最小化=%v %dx%d @(%d,%d)\n",
		hwnd, syscall.UTF16ToString(cls[:n]), syscall.UTF16ToString(ttl[:n2]),
		win.Minimized(hwnd), W, H, r.Left, r.Top)
	if win.Minimized(hwnd) {
		fmt.Println("⚠️ 它现在**最小化**着 —— Qt 最小化时不渲染，量出来会是 0 差异（没意义）。先恢复再量。")
	}
	gaps := []int{137, 211, 173}
	fmt.Printf("PrintWindow（不受遮挡影响）· %d 轮\n\n", rounds)
	for i := 1; i <= rounds; i++ {
		a, err := printWindow(hwnd, W, H)
		if err != nil {
			fmt.Println("PrintWindow 失败：" + err.Error())
			os.Exit(1)
		}
		time.Sleep(time.Duration(gaps[(i-1)%len(gaps)]) * time.Millisecond)
		b, err := printWindow(hwnd, W, H)
		if err != nil {
			fmt.Println("PrintWindow 失败：" + err.Error())
			os.Exit(1)
		}
		nn := 0
		for k := 0; k+3 < len(a) && k+3 < len(b); k += 4 {
			if abs(int(a[k])-int(b[k]))+abs(int(a[k+1])-int(b[k+1]))+abs(int(a[k+2])-int(b[k+2])) > 24 {
				nn++
			}
		}
		pct := float64(nn) * 100 / float64(W*H)
		verdict := "基本没变"
		if pct > 3 {
			verdict = "**大面积重绘 ⇒ 这个窗口在自己刷（就是它在闪）**"
		} else if pct > 0.3 {
			verdict = "局部在动"
		}
		fmt.Printf("第 %d 轮（间隔 %dms）：变了 %d 像素（%.2f%%）⇒ %s\n", i, gaps[(i-1)%len(gaps)], nn, pct, verdict)
	}
}

// printWindow 把窗口内容画进一个 DIB 并取回像素（`PW_RENDERFULLCONTENT` = 2，对 Qt/Chromium 也管用）。
func printWindow(hwnd uintptr, w, h int) ([]byte, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("窗口尺寸不对：%dx%d", w, h)
	}
	screen, _, _ := pGetDC.Call(0)
	if screen == 0 {
		return nil, fmt.Errorf("GetDC(NULL) 失败")
	}
	defer pReleaseDC.Call(0, screen)
	mem, _, _ := pCreateCompatibleDC.Call(screen)
	if mem == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC 失败")
	}
	defer pDeleteDC.Call(mem)
	bi := bitmapInfo{}
	bi.Header.Size = uint32(unsafe.Sizeof(bi.Header))
	bi.Header.Width = int32(w)
	bi.Header.Height = -int32(h)
	bi.Header.Planes = 1
	bi.Header.BitCount = 32
	var bits unsafe.Pointer
	dib, _, _ := pCreateDIBSection.Call(mem, uintptr(unsafe.Pointer(&bi)), 0,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if dib == 0 || bits == nil {
		return nil, fmt.Errorf("CreateDIBSection 失败")
	}
	defer pDeleteObject.Call(dib)
	old, _, _ := pSelectObject.Call(mem, dib)
	ret, _, _ := pPrintWindow.Call(hwnd, mem, 2 /*PW_RENDERFULLCONTENT*/)
	pSelectObject.Call(mem, old)
	if ret == 0 {
		return nil, fmt.Errorf("PrintWindow 返回 0（这个窗口不认）")
	}
	out := make([]byte, w*h*4)
	copy(out, unsafe.Slice((*byte)(bits), len(out)))
	return out, nil
}
