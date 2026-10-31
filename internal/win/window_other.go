//go:build !windows

// Package win 的非 Windows 桩：**如实说"没做"**，不静默变成"没反应"。
//
// 面板与 ACE 都跑在 Windows 上（ACE Studio 只有 Windows/macOS，而本工具这套是照着 Windows 那条链做的），
// 所以非 Windows 只保证**编译得过 + 说清楚**，不假装支持。
package win

import "fmt"

type Window struct {
	Handle uintptr
	Title  string
	PID    uint32
	Class  string
}

// Rect 是屏幕坐标下的窗口矩形（非 Windows 上拿不到，桩里留着类型让上层编译得过）。
type Rect struct {
	Left, Top, Right, Bottom int32
}

func (r Rect) W() int32 { return r.Right - r.Left }
func (r Rect) H() int32 { return r.Bottom - r.Top }

func List() ([]Window, error) { return nil, errNotWindows }

func ListPID(pid uint32) ([]Window, error) { return nil, errNotWindows }

func ByExeName(exe string) ([]uint32, error) { return nil, errNotWindows }

func RectOf(hwnd uintptr) (Rect, error) { return Rect{}, errNotWindows }

func ClientRectOf(hwnd uintptr) (Rect, error) { return Rect{}, errNotWindows }

func SystemCaptionHeight() int32 { return 0 }

func Minimized(hwnd uintptr) bool { return false }

func Visible(hwnd uintptr) bool { return false }

func LeftButtonDown() bool { return false }

func Close(hwnd uintptr) error { return errNotWindows }

func SendClick(hwnd uintptr, x, y int32) error { return errNotWindows }

func SendRightClick(hwnd uintptr, x, y int32) error { return errNotWindows }

func Move(hwnd uintptr, r Rect) error { return errNotWindows }

func Owner(hwnd uintptr) uintptr { return 0 }

func HasOwner(hwnd uintptr) bool { return false }

func ForegroundWindow() uintptr { return 0 }

// SetDPIAware 在非 Windows 上是空操作（细条本身也没做）。
func SetDPIAware() {}

// DPIScale 非 Windows 上给个 1（没有 DPI 缩放的概念）。
func DPIScale() float64 { return 1 }

// AppWindow 见 appwindow_windows.go（非 Windows 上没做）。
func AppWindow(url string, x, y, w, h int) (string, bool, error) {
	return "", false, errNotWindows
}

// DefaultBrowserExe / RunDetached 见 browser_windows.go（非 Windows 上没做）。
func DefaultBrowserExe() (string, error) { return "", errNotWindows }

func RunDetached(exe string, args ...string) error { return errNotWindows }

var errNotWindows = fmt.Errorf("窗口置顶 / 前台／系统级热键只在 Windows 上做过（本机不是 Windows）⇒ 这两项不可用，其余功能不受影响")

func Find(substr string) ([]Window, error) { return nil, errNotWindows }

func FindExact(title string) ([]Window, error) { return nil, errNotWindows }

func FindPrefixed(title string) ([]Window, error) { return nil, errNotWindows }

func Foreground() uintptr { return 0 }

func Alive(hwnd uintptr) bool { return false }

func IsTopMost(hwnd uintptr) bool { return false }

func SetTopMost(hwnd uintptr, on bool) error { return errNotWindows }

func Focus(hwnd uintptr) (bool, error) { return false, errNotWindows }

// 剪贴板见 clipboard_windows.go（非 Windows 上没做）。
func SetClipboardText(s string) error { return errNotWindows }

func ClipboardText() (string, error) { return "", errNotWindows }

// 「那个点上是谁」见 window_windows.go 的 WindowAt（非 Windows 上没做）。
func WindowAt(x, y int32) uintptr { return 0 }

func RootOf(hwnd uintptr) uintptr { return 0 }

func PIDOf(hwnd uintptr) uint32 { return 0 }

func ClassOf(hwnd uintptr) string { return "" }

// ZOrder 见 window_windows.go（非 Windows 上没做）。
func ZOrder() ([]Window, error) { return nil, errNotWindows }

func ZAbove(a, b uintptr) (bool, bool) { return false, false }

func ZGap(a, b uintptr) (int, bool) { return 0, false }

// SetOwner / RailInsertAbove 见 window_windows.go（非 Windows 上没做）。
func SetOwner(hwnd, owner uintptr) (uintptr, error) { return 0, errNotWindows }

func RailInsertAbove(hwnd, after uintptr) error { return errNotWindows }
