//go:build windows

package tray

// 托盘图标：**从嵌入的 PNG 现做 HICON**（不落临时文件、不依赖资源编译器）。
//
// 图片来源：`devtools/make-tray-icon.js` 用无头 Edge 把
// `<skills-dir>\electron\src\ace-mark-anim.svg` 里**字母 a** 那条 path 光栅化成 6 档 PNG
// （16/20/24/32/40/48，对应 100%/125%/150%/200%/250%/300% 缩放），黑字透明底。
// 每档都单独渲染而不是"一张大图缩小"：矢量直接按目标像素画最清楚，缩小会糊。
//
// 为什么是这个做法：托盘要的是 `HICON`，而我们是**零依赖单文件**（CGO_ENABLED=0），
// 既不该引 `golang.org/x/sys`，也不该为一张 16px 的图落临时 `.ico`：
//   · PNG → 32 位 BGRA → `CreateDIBSection`；
//   · 再加一张全 0 的 1bpp 掩码（32 位图有 alpha 时掩码只是"形状占位"）；
//   · `CreateIconIndirect` 合成 HICON。
//
// ⚠️ 预乘 alpha 别省：`CreateIconIndirect` 拿到的颜色位图按**预乘**解释，不预乘的话
// 半透明边缘会发白（黑色字形尤其明显 —— 边缘一圈灰雾）。好在 `image.RGBA()` 返回的
// 就是预乘过的值 ⇒ 直接右移 8 位即可，不用再乘一次。
import (
	"bytes"
	"embed"
	"fmt"
	"image/png"
	"strconv"
	"strings"
	"unsafe"
)

//go:embed icon/tray-*.png
var iconFiles embed.FS

// iconSizes 是嵌入的那几档边长（与 `devtools/make-tray-icon.js` 里的 SIZES 对齐）。
var iconSizes = []int{16, 20, 24, 32, 40, 48}

// pickSize 选最接近 `want` 的那一档（want = 16 × DPI 缩放：96dpi→16、144dpi→24、192dpi→32）。
func pickSize(want int) int {
	best := iconSizes[0]
	bestDiff := 1 << 30
	for _, s := range iconSizes {
		d := s - want
		if d < 0 {
			d = -d
		}
		if d < bestDiff {
			bestDiff, best = d, s
		}
	}
	return best
}

// iconPNG 取出某一档的 PNG 字节。
func iconPNG(size int) ([]byte, error) {
	name := "icon/tray-" + strconv.Itoa(size) + ".png"
	b, err := iconFiles.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("嵌入的图标里没有 %s：%w", name, err)
	}
	return b, nil
}

// makeHICON 把一份 PNG 做成 HICON。返回值要 `DestroyIcon`（Stop 里做）。
func makeHICON(size int) (hicon uintptr, err error) {
	b, err := iconPNG(size)
	if err != nil {
		return 0, err
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return 0, fmt.Errorf("图标 PNG 解不开（tray-%d.png）：%w", size, err)
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return 0, fmt.Errorf("图标 PNG 尺寸不对：%dx%d", w, h)
	}
	bgra := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r16, g16, b16, a16 := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			o := (y*w + x) * 4
			bgra[o+0] = byte(b16 >> 8)
			bgra[o+1] = byte(g16 >> 8)
			bgra[o+2] = byte(r16 >> 8)
			bgra[o+3] = byte(a16 >> 8)
		}
	}

	var bi bitmapInfoHeader
	bi.Size = uint32(unsafe.Sizeof(bitmapInfoHeader{}))
	bi.Width = int32(w)
	bi.Height = -int32(h) // 负数 = top-down（与 PNG 的行序一致）
	bi.Planes = 1
	bi.BitCount = 32
	bi.Compression = 0 // BI_RGB
	bi.SizeImage = uint32(len(bgra))

	dc, _, _ := pCreateCompatibleDC.Call(0)
	if dc == 0 {
		return 0, fmt.Errorf("CreateCompatibleDC 失败")
	}
	defer pDeleteDC.Call(dc)

	var bits unsafe.Pointer
	hbmp, _, e := pCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&bi)), 0,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbmp == 0 || bits == nil {
		return 0, fmt.Errorf("CreateDIBSection 失败：%v", e)
	}
	defer pDeleteObject.Call(hbmp)
	copy(unsafe.Slice((*byte)(bits), len(bgra)), bgra)

	// 掩码：全 0 = "按颜色位图的 alpha 来"（32 位图标都这么给）
	mask, _, e := pCreateBitmap.Call(uintptr(w), uintptr(h), 1, 1, 0)
	if mask == 0 {
		return 0, fmt.Errorf("CreateBitmap（掩码）失败：%v", e)
	}
	defer pDeleteObject.Call(mask)

	ii := iconInfo{fIcon: 1, hbmMask: mask, hbmColor: hbmp}
	icon, _, e := pCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	if icon == 0 {
		return 0, fmt.Errorf("CreateIconIndirect 失败：%v", e)
	}
	return icon, nil
}

// iconResource 是"当前用哪个图标"的一句话说明（给 `/api/tray` 与日志看，排障时不用猜）。
func iconResource(size int) string {
	sizes := make([]string, 0, len(iconSizes))
	for _, s := range iconSizes {
		sizes = append(sizes, strconv.Itoa(s))
	}
	return "内嵌 PNG " + strconv.Itoa(size) + "px（可选 " + strings.Join(sizes, "/") + "）"
}

// iconBase 是托盘图标的基准边长（16px @96dpi，Windows 的惯例）。
const iconBase = 16

// 做 HICON 要用的 GDI/User32 入口（`user32` / `gdi32` 两个句柄在 `tray_windows.go` 里建）。
var (
	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pCreateBitmap       = gdi32.NewProc("CreateBitmap")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pCreateIconIndirect = user32.NewProc("CreateIconIndirect")
	pDestroyIcon        = user32.NewProc("DestroyIcon")
)

// bitmapInfoHeader 是 `BITMAPINFOHEADER`（40 字节）。
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

// iconInfo 是 `ICONINFO`（x64 上 32 字节：注意两处 4 字节对齐填充）。
type iconInfo struct {
	fIcon    int32
	xHotspot uint32
	yHotspot uint32
	_        uint32
	hbmMask  uintptr
	hbmColor uintptr
}
