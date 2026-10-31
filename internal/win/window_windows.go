//go:build windows

// Package win 是**面板之外那点窗口操作**：找到面板所在的浏览器窗口、把它置顶、把它调到前台。
//
// 为什么需要它：面板是**系统浏览器里的一个标签**，不是我们自己的窗口 ——
// 用户想在 ACE 里干活时把面板浮在上面，或者用系统级热键把面板唤回来。
// 这两件事都只能由 exe 通过 `user32` 做（网页没有 OS 钩子）。
//
// ⚠️ 三条必须承认的边界（都如实往上报，不假装成功）：
//  1. **浏览器窗口标题跟的是"当前标签页"** ⇒ 用户切到别的标签后标题里就没有 `ACETool` 了，
//     那时按标题找不到窗口。所以这里提供 `Find`（每次重新找）与**缓存句柄 + `IsWindow` 校验**两条路，
//     调用方决定策略；找不到就报"没找到"，不猜。
//  2. `SetWindowPos` 跨进程可用，但**权限/完整性级别不同**（一个管理员一个不是）会被拒 ⇒ 把系统错误码带出来。
//  3. `SetForegroundWindow` 受"前台锁"限制：**后台进程不许抢前台**。这里用
//     `AttachThreadInput` 冒充当前前台线程（经典做法），仍然可能失败 ⇒ **返回布尔值，不假装成功**。
package win

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                    = syscall.NewLazyDLL("user32.dll")
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	pEnumWindows              = user32.NewProc("EnumWindows")
	pGetWindowTextW           = user32.NewProc("GetWindowTextW")
	pGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	pIsWindowVisible          = user32.NewProc("IsWindowVisible")
	pIsWindow                 = user32.NewProc("IsWindow")
	pIsIconic                 = user32.NewProc("IsIconic")
	pSetWindowPos             = user32.NewProc("SetWindowPos")
	pShowWindow               = user32.NewProc("ShowWindow")
	pSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	pBringWindowToTop         = user32.NewProc("BringWindowToTop")
	pGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	pGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	pAttachThreadInput        = user32.NewProc("AttachThreadInput")
	pGetWindowLongPtrW        = user32.NewProc("GetWindowLongPtrW")
	pSetWindowLongPtrW        = user32.NewProc("SetWindowLongPtrW")
	pGetCurrentThreadId       = kernel32.NewProc("GetCurrentThreadId")
	pGetCurrentProcessId      = kernel32.NewProc("GetCurrentProcessId")
	// ⭐ exe 侧栏（贴 ACE 窗口）要用的那几件：矩形 / 类名 / 进程枚举 / 最小化判定
	pGetWindowRect                 = user32.NewProc("GetWindowRect")
	pGetClassNameW                 = user32.NewProc("GetClassNameW")
	pGetWindow                     = user32.NewProc("GetWindow")
	pGetParent                     = user32.NewProc("GetParent")
	pCreateToolhelp32Snapshot      = kernel32.NewProc("CreateToolhelp32Snapshot")
	pProcess32FirstW               = kernel32.NewProc("Process32FirstW")
	pProcess32NextW                = kernel32.NewProc("Process32NextW")
	pCloseHandle                   = kernel32.NewProc("CloseHandle")
	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	pSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	pGetClientRectW                = user32.NewProc("GetClientRect")
	pMapWindowPoints               = user32.NewProc("MapWindowPoints")
	pGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	pGetAsyncKeyState              = user32.NewProc("GetAsyncKeyState")
	pGetDpiForSystem               = user32.NewProc("GetDpiForSystem")
	pPostMessageW                  = user32.NewProc("PostMessageW")
	pWindowFromPoint               = user32.NewProc("WindowFromPoint")
	pGetAncestor                   = user32.NewProc("GetAncestor")
)

// SetDPIAware 声明本进程**DPI 感知**（必须在建任何窗口之前调）。
//
// 为什么要它：150% 缩放的屏上，不感知的进程拿到的是**逻辑坐标**（真机 2560x1440 报成 1707x960），
// 而 GDI 画的汉字会被系统拉伸成糊的。exe 侧条要贴 ACE 的窗口、还要画 1–2 个汉字 ⇒ 必须感知。
// 先试 per-monitor v2（`-4`；老系统上这个 API 不存在 ⇒ 退回 `SetProcessDPIAware()`）。
func SetDPIAware() {
	if err := pSetProcessDpiAwarenessContext.Find(); err == nil {
		if ret, _, _ := pSetProcessDpiAwarenessContext.Call(^uintptr(3)); ret != 0 {
			return
		}
	}
	pSetProcessDPIAware.Call()
}

// DPIScale 返回主显示器的缩放倍数（1.0 = 96 DPI；150% 屏上是 1.5）。
//
// 用途：所有"逻辑像素"尺寸（细条的宽度、吸附的判定距离、让开标题栏的量）都得乘它 ——
// 真机 150% 屏上不乘的话，判定距离会小一半（"靠近了也不亮"就是这么来的，见 §72）。
func DPIScale() float64 {
	dpi := uintptr(0)
	if err := pGetDpiForSystem.Find(); err == nil {
		dpi, _, _ = pGetDpiForSystem.Call()
	}
	if dpi == 0 {
		dpi = 96
	}
	return float64(dpi) / 96.0
}

// TH32CS_SNAPPROCESS / PROCESSENTRY32W：按进程名找 pid 用（`CreateToolhelp32Snapshot` 那条路）。
const TH32CS_SNAPPROCESS = 0x00000002

type PROCESSENTRY32W struct {
	Size            uint32
	CntUsage        uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	CntThreads      uint32
	ParentProcessID uint32
	PriClassBase    int32
	Flags           uint32
	ExeFile         [260]uint16
}

type RECT struct {
	Left, Top, Right, Bottom int32
}

const (
	smCaption     = 4  // GetSystemMetrics：标题栏高度
	smCySizeFrame = 32 // 可调边框高度
	smCyBorder    = 6  // 边框厚度

	GW_OWNER = 4 // GetWindow(hwnd, GW_OWNER)：取属主（有属主 = 弹出/工具窗）

	wmClose      = 0x0010 // WM_CLOSE
	wmLeftDown   = 0x0201 // WM_LBUTTONDOWN（合成点击用）
	wmLeftUp     = 0x0202 // WM_LBUTTONUP
	wmRightDown  = 0x0204 // WM_RBUTTONDOWN（合成右键用）
	wmRightUp    = 0x0205 // WM_RBUTTONUP
	vkLeftButton = 0x01   // GetAsyncKeyState：鼠标左键

	HWND_TOPMOST   = ^uintptr(0) // -1
	HWND_NOTOPMOST = ^uintptr(1) // -2

	SWP_NOSIZE     = 0x0001
	SWP_NOMOVE     = 0x0002
	SWP_NOZORDER   = 0x0004
	SWP_NOACTIVATE = 0x0010
	SWP_SHOWWINDOW = 0x0040

	SW_RESTORE = 9

	GWL_EXSTYLE   = ^uintptr(19) // -20
	WS_EX_TOPMOST = 0x00000008

	// GWLP_HWNDPARENT = -8：设**属主**（对顶层窗口而言就是 owner；对子窗口是父窗口）。
	GWLP_HWNDPARENT = ^uintptr(7)
)

// Window 是一个候选窗口。
type Window struct {
	Handle uintptr
	Title  string
	PID    uint32 // 属于哪个进程（exe 侧栏要认"ACE 的那两个窗口"）
	Class  string // 窗口类名（真机侦察用；也是"别贴错窗口"的一道判据）
}

// Rect 是屏幕坐标下的窗口矩形（`GetWindowRect` 的四个值）。
type Rect struct {
	Left, Top, Right, Bottom int32
}

func (r Rect) W() int32 { return r.Right - r.Left }
func (r Rect) H() int32 { return r.Bottom - r.Top }

// List 列出**所有可见的顶层窗口**（剔除规则同 enumWindows：本进程窗口 + 我们自己控制台那条）。
//
// 用途：exe 侧栏要"找 ACE 的那两个窗口、跟着它的矩形走"，以及真机侦察
// （`devtools/winlist`）时看清桌面上到底有哪些窗口。
func List() ([]Window, error) {
	return enumWindows(func(string) bool { return true })
}

// ZOrder 列出所有可见顶层窗口，**按 z 序从最上面开始**，且**包含我们自己的窗口**。
//
// 跟 `List` 的两点区别都是故意的：
//
//	① 含本进程（细条就是本进程的窗口）—— 要判"细条在 ACE 上面还是下面"必须看得见它；
//	② 顺序有意义：`EnumWindows` 的回调顺序就是 z 序（最上面的先来），所以下标小的在上面。
//
// 用途：`devtools/railprobe` 用它量"细条是不是紧贴在 ACE 之上"——
// 这条判据**不依赖前台**（别的应用全屏在最上面时，细条与 ACE 的**相对**次序依然是它），
// 而 `WindowAt` 那条判据只说明"此刻看不看得见"（会被别人的全屏窗口遮住）。
func ZOrder() ([]Window, error) {
	return enumWindowsAll(true, func(string) bool { return true })
}

// ZAbove 判断 `a` 在 z 序里是不是**在 `b` 之上**（下标更小）。第二个返回值 = 两个窗口是不是都找到了。
//
// 为什么需要它：细条是"贴在 ACE 之上"的一条窄窗，而 Windows 会在很多时机把 ACE 抬到细条前面
// （点它 / 它自己被激活 / 切主窗⇄钢琴窗）⇒ 必须**能问出"我现在是在它上面还是下面"**，
// 才能"只在真的错了的时候补插一次"。用 `WindowAt` 问"那个点上是谁"是不够的：
// 真机实测（2026-10-17）有那种状态 —— 细条**在 ACE 下面**，可那个点上最上面的是**另一个应用**
// （用户的全屏 Edge），于是"被谁盖住"这条判据看见的是 Edge，**看不出细条其实已经被 ACE 压住了**。
func ZAbove(a, b uintptr) (above, both bool) {
	gap, both := ZGap(a, b)
	return gap > 0, both
}

// SetOwner 把 `hwnd` 的**属主**设成 `owner`（`GWLP_HWNDPARENT`；0 = 取消属主）。返回老属主。
//
// 为什么要这条原语：细条要"恒在 ACE 之上、但**不许**浮在别的应用上面"。Windows 对 owned 窗口的保证
// 正好就是这个意思：**owned 窗口恒在其属主之上**；别的应用盖住属主时，owned 窗口**跟着一起被盖住**。
//
// ⛔ 历史教训（2026-10-16 第二轮）：当时是"认属主 **+ 换属主时升 Z 序**"——
// **升 Z 序会把属主一起提到最前**（Windows 的既定行为）⇒ 主窗与钢琴窗轮流压对方 ⇒ 整窗闪烁。
// ⇒ 用这条原语时**别**再顺手抬自己的 Z 序。它单独成立不成立，用 `devtools/railtry -mode owner` 验。
func SetOwner(hwnd, owner uintptr) (old uintptr, err error) {
	if !Alive(hwnd) {
		return 0, fmt.Errorf("窗口句柄已失效（0x%x）", hwnd)
	}
	old, _, errno := pSetWindowLongPtrW.Call(hwnd, GWLP_HWNDPARENT, owner)
	if old == 0 {
		if e, ok := errno.(syscall.Errno); ok && e != 0 {
			return 0, fmt.Errorf("SetWindowLongPtr(GWLP_HWNDPARENT) 失败：%v", e)
		}
	}
	return old, nil
}

// RailInsertAbove 把 `hwnd` 插到 `after` **紧贴之上**（`SetWindowPos` 的 `hWndInsertAfter`）。
//
// 为什么单独包一层：这条原语的行为在真机上**跟文档说的不一样**（2026-10-17：回 1/成功，
// 可 z 序里细条仍在那个窗口下面 —— 那个窗口是 ACE **owned** 的钢琴窗）。所以它必须
// **能单独被验**（`devtools/railtry` 就干这个），而不是埋在跟随逻辑里让人猜。
//
// 返回：调用被拒时给非 nil 错（含 GetLastError 的说法）；**成功也代表不了层级真的变了**，
// 要看调用之后用 `ZGap` 读回来的**事实**（这正是踩过的那一脚）。
func RailInsertAbove(hwnd, after uintptr) error {
	if !Alive(hwnd) {
		return fmt.Errorf("窗口句柄已失效（0x%x）", hwnd)
	}
	if !Alive(after) {
		return fmt.Errorf("要插到谁上面？那个句柄已失效（0x%x）", after)
	}
	ret, _, err := pSetWindowPos.Call(hwnd, after, 0, 0, 0, 0, SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE)
	if ret == 0 {
		return fmt.Errorf("SetWindowPos 被拒：%v", err)
	}
	return nil
}

// ZGap 返回 `a` 相对 `b` 在 z 序里的位置：//
//
//	1  = a **紧贴在 b 之上**（相邻，中间没夹别的窗口）
//	>1 = a 在 b 上面，中间还夹着 ZGap-1 个窗口
//	<0 = a 在 b 下面（b 会盖住 a）
//
// 第二个返回值 = 两个窗口是不是都在可见窗口里（缺一个就没法比）。
//
// 为什么要"紧邻"这一档：细条是**画在 ACE 身上**的一条窄窗，只有紧邻它之上才是唯一同时满足两条
// 用户口径的位置 —— ①「不许显示在其他应用上」（比 ACE 更高就会浮在别人上面）②「不许被 ACE 盖住」。
// 真机实测（2026-10-17）：**新建窗口会被系统插到本层最上面**，所以"只在自己被压住时才补插"会漏掉
// "细条浮在终端/浏览器上面"那一半（当时量到细条 #3、ACE #6）。
func ZGap(a, b uintptr) (gap int, both bool) {
	ws, err := ZOrder()
	if err != nil {
		return 0, false
	}
	ai, bi := -1, -1
	for i, w := range ws {
		if w.Handle == a {
			ai = i
		}
		if w.Handle == b {
			bi = i
		}
	}
	if ai < 0 || bi < 0 {
		return 0, false
	}
	return bi - ai, true
}

// ListPID 列出**某个进程**的可见顶层窗口（ACE 主窗 + 钢琴窗就是这么找出来的）。
func ListPID(pid uint32) ([]Window, error) {
	all, err := List()
	if err != nil {
		return nil, err
	}
	out := make([]Window, 0, 4)
	for _, w := range all {
		if w.PID == pid {
			out = append(out, w)
		}
	}
	return out, nil
}

// ByExeName 按**进程可执行文件名**找 pid（大小写不敏感；如 `ACE Studio.exe`）。
//
// 为什么不用 `FindWindow("ACE Studio")`：窗口标题是用户/软件随时会改的东西，
// 而进程名是我们和 ACE 之间最稳的那根锚。
func ByExeName(exe string) ([]uint32, error) {
	want := strings.ToLower(strings.TrimSpace(exe))
	if want == "" {
		return nil, fmt.Errorf("进程名是空的")
	}
	snap, _, err := pCreateToolhelp32Snapshot.Call(TH32CS_SNAPPROCESS, 0)
	if snap == ^uintptr(0) || snap == 0 {
		return nil, fmt.Errorf("CreateToolhelp32Snapshot 失败：%v", err)
	}
	defer pCloseHandle.Call(snap)

	var pe PROCESSENTRY32W
	pe.Size = uint32(unsafe.Sizeof(pe))
	ret, _, err := pProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&pe)))
	if ret == 0 {
		return nil, fmt.Errorf("Process32FirstW 失败：%v", err)
	}
	var out []uint32
	for ret != 0 {
		if strings.ToLower(syscall.UTF16ToString(pe.ExeFile[:])) == want {
			out = append(out, pe.ProcessID)
		}
		pe.Size = uint32(unsafe.Sizeof(pe))
		ret, _, _ = pProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&pe)))
	}
	return out, nil
}

// RectOf 读窗口矩形（屏幕坐标）。
func RectOf(hwnd uintptr) (Rect, error) {
	if !Alive(hwnd) {
		return Rect{}, fmt.Errorf("窗口句柄已失效（0x%x）", hwnd)
	}
	var r RECT
	ret, _, err := pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if ret == 0 {
		return Rect{}, fmt.Errorf("GetWindowRect 失败：%v", err)
	}
	return Rect{r.Left, r.Top, r.Right, r.Bottom}, nil
}

// Minimized 判断窗口是不是最小化了（最小化时不该把侧栏贴在它的矩形上 —— 那是 (-32000,-32000)）。
func Minimized(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	ret, _, _ := pIsIconic.Call(hwnd)
	return ret != 0
}

// Visible 读窗口的 `WS_VISIBLE`（被 `ShowWindow(SW_HIDE)` 藏起来时为 false）。
//
// 为什么需要它：用户 2026-10-16 报"**整个钢琴窗消失又出现**，在操作侧栏期间"——
// 那要么是它真的被藏了（owner 被隐藏 ⇒ Windows 连带隐藏 owned 窗口），要么只是**被主窗盖住**（Z 序被动了）。
// 这两种的解释和修法完全不同，必须分开量：本函数管"真藏了"，`WindowFromPoint` 管"被盖住"。
func Visible(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	ret, _, _ := pIsWindowVisible.Call(hwnd)
	return ret != 0
}

// ClientRectOf 读**客户区**矩形（屏幕坐标）。
//
// 为什么要它：`RectOf` 拿到的是**整个窗口**（含标题栏与边框），而贴边的小条若从窗口顶边开始贴，
// 就会压住**关闭按钮**（用户 2026-10-16：「只留一个小细条，现在挡关闭按钮了」）。
// 做法：`GetClientRect` 拿客户区尺寸 ＋ `MapWindowPoints(…, NULL)` 换算成屏幕坐标。
func ClientRectOf(hwnd uintptr) (Rect, error) {
	if !Alive(hwnd) {
		return Rect{}, fmt.Errorf("窗口句柄已失效（0x%x）", hwnd)
	}
	var r RECT
	if ret, _, err := pGetClientRectW.Call(hwnd, uintptr(unsafe.Pointer(&r))); ret == 0 {
		return Rect{}, fmt.Errorf("GetClientRect 失败：%v", err)
	}
	// cPoints = 2：RECT 的内存就是两个 POINT（left,top / right,bottom）⇒ 一次换算两个点。
	// ⚠️ `MapWindowPoints` 的返回值是**换算过的点数**，不是成败（cPoints=0 时才为 0）⇒ 不看它。
	pMapWindowPoints.Call(hwnd, 0, uintptr(unsafe.Pointer(&r)), 2)
	return Rect{r.Left, r.Top, r.Right, r.Bottom}, nil
}

// SystemCaptionHeight 读系统标题栏总高（标题 + 上边框 + 边框）。
// "自绘标题栏"的应用（客户区里自己画关闭按钮）拿不到这个信息，就用它当**让位量**的参考。
func SystemCaptionHeight() int32 {
	var out int32
	for _, i := range []uintptr{smCaption, smCySizeFrame, smCyBorder} {
		v, _, _ := pGetSystemMetrics.Call(i)
		out += int32(v)
	}
	return out
}

// ForegroundWindow 是当前前台窗口的句柄（0 = 问不到）。
// （`Foreground` 是它的老名字，面板置顶那边在用；这里给 exe 侧栏一个更直白的名字。）
func ForegroundWindow() uintptr { return Foreground() }

// SendClick 往一个窗口**合成一次左键点击**（按下 + 抬起），坐标是**客户区**坐标。
//
// 用途：自检细条（`devtools/railclick`）——「点完之后前台有没有还给 ACE」这件事必须真点一次才知道，
// 而细条只有几像素宽、脚本又抢不到用户的鼠标（实测放过去 150ms 就被真实的手覆盖）。
func SendClick(hwnd uintptr, x, y int32) error {
	if !Alive(hwnd) {
		return fmt.Errorf("窗口句柄已失效（0x%x）", hwnd)
	}
	lp := uintptr(uint32(x)&0xffff | (uint32(y)&0xffff)<<16)
	if ret, _, err := pPostMessageW.Call(hwnd, wmLeftDown, 1 /*MK_LBUTTON*/, lp); ret == 0 {
		return fmt.Errorf("PostMessage(WM_LBUTTONDOWN) 被拒：%v", err)
	}
	time.Sleep(40 * time.Millisecond) // 让窗口先收到"按下"，再收"抬起"（有的实现靠这个区分拖拽）
	if ret, _, err := pPostMessageW.Call(hwnd, wmLeftUp, 0, lp); ret == 0 {
		return fmt.Errorf("PostMessage(WM_LBUTTONUP) 被拒：%v", err)
	}
	return nil
}

// SendRightClick 在 (x,y) 合成一次**右键**点击（客户区坐标）。
//
// 为什么需要它：细条上"左键 = 直接干、右键 = 改附属参数"（用户 2026-10-17），
// 而自检不能用真鼠标去点（这台机器有人在用）⇒ 跟 `SendClick` 一样走 `PostMessage`。
func SendRightClick(hwnd uintptr, x, y int32) error {
	if !Alive(hwnd) {
		return fmt.Errorf("窗口句柄已失效（0x%x）", hwnd)
	}
	lp := uintptr(uint32(x)&0xffff | (uint32(y)&0xffff)<<16)
	if ret, _, err := pPostMessageW.Call(hwnd, wmRightDown, 2 /*MK_RBUTTON*/, lp); ret == 0 {
		return fmt.Errorf("PostMessage(WM_RBUTTONDOWN) 被拒：%v", err)
	}
	time.Sleep(40 * time.Millisecond)
	if ret, _, err := pPostMessageW.Call(hwnd, wmRightUp, 0, lp); ret == 0 {
		return fmt.Errorf("PostMessage(WM_RBUTTONUP) 被拒：%v", err)
	}
	return nil
}

// Close 礼貌地请一个窗口关掉（`WM_CLOSE`，不是 TerminateProcess）。//
// 用途：切到侧栏时把**我们自己开的那个 web 窗口**关掉（用户 2026-10-16：「切的时候关掉」）。
// ⚠️ 跨进程 `WM_CLOSE` 对正常窗口有效；浏览器会走它自己的"关这一个窗口"逻辑。
// 调用方**必须自己确认关的是哪个窗口**（我们只关自己开的那个，见 `theAppWin`）。
func Close(hwnd uintptr) error {
	if !Alive(hwnd) {
		return fmt.Errorf("窗口句柄已失效（0x%x）", hwnd)
	}
	ret, _, err := pPostMessageW.Call(hwnd, wmClose, 0, 0)
	if ret == 0 {
		return fmt.Errorf("PostMessage(WM_CLOSE) 被拒：%v", err)
	}
	return nil
}

// LeftButtonDown 问一句"鼠标左键现在按着没"。//
// 为什么需要它：贴边吸附要分清**用户正在拖那个窗口**（按着键 ⇒ 别抢）还是**已经松手**（松手了 ⇒ 可以吸）。
// `GetAsyncKeyState` 是问"此刻的物理键状态"，不管焦点在谁那儿 —— 正是我们要的。
func LeftButtonDown() bool {
	v, _, _ := pGetAsyncKeyState.Call(vkLeftButton)
	return v&0x8000 != 0
}

// Move 把窗口搬到指定矩形（**不动 Z 序、不激活**）—— 贴边吸附要用它。
//
// ⚠️ 目标窗口可能是**别的进程**（比如浏览器的 app 窗口）：`SetWindowPos` 跨进程可用，
// 但对方可以拒绝这个尺寸（Chromium 有最小窗口宽度，会自己弹回去）⇒ 调用方**必须回读**再判断。
func Move(hwnd uintptr, r Rect) error {
	if !Alive(hwnd) {
		return fmt.Errorf("窗口句柄已失效（0x%x）", hwnd)
	}
	ret, _, err := pSetWindowPos.Call(hwnd, 0, uintptr(r.Left), uintptr(r.Top),
		uintptr(r.W()), uintptr(r.H()), SWP_NOZORDER|SWP_NOACTIVATE)
	if ret == 0 {
		return fmt.Errorf("SetWindowPos 被拒：%v", err)
	}
	return nil
}

// Find 找出**标题里含 `substr` 且可见**的顶层窗口。
//
// 返回全部匹配（`Windows`），不只是第一个 —— 多窗口浏览器或开了两个面板时会匹配到多个，
// 这个事实要能让用户看见（调用方把它显示出来，而不是默默挑一个）。
func Find(substr string) ([]Window, error) {
	if strings.TrimSpace(substr) == "" {
		return nil, fmt.Errorf("窗口标题匹配串是空的")
	}
	needle := strings.ToLower(substr)
	return enumWindows(func(t string) bool {
		return strings.Contains(strings.ToLower(t), needle)
	})
}

// FindExact 找出**标题与 `title` 完全相同**（忽略大小写）的可见顶层窗口。
//
// 为什么要有它（2026-10-16 真机踩到）：`Find` 是**子串**匹配，而面板各页的标题是
// `ACETool` / `ACETool · 设置` 这种包含关系 ⇒ 点「设置」页的 📌 有可能顶起 home 那个窗口。
// 每个页面点 📌 时**上报自己的 `document.title`**，服务端用本函数精确定位**它自己那个窗口**。
//
// ⚠️ 真机上**它几乎不会命中**：浏览器会给窗口标题加尾巴，Edge 是
// `ACETool 和另外 9 个页面 - 个人 - Microsoft Edge`（页面标题 + 标签数 + 配置 + 浏览器名）。
// 所以精确匹配只是第一段，调用方必须接着按**前缀**找（浏览器只会**往后加**，不会往前加）。
func FindExact(title string) ([]Window, error) {
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("窗口标题是空的")
	}
	want := strings.ToLower(strings.TrimSpace(title))
	return enumWindows(func(t string) bool {
		return strings.ToLower(strings.TrimSpace(t)) == want
	})
}

// FindPrefixed 找出**标题以 `title` 开头**的可见顶层窗口（浏览器加尾巴的形态）。
func FindPrefixed(title string) ([]Window, error) {
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("窗口标题是空的")
	}
	want := strings.ToLower(strings.TrimSpace(title))
	return enumWindows(func(t string) bool {
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(t)), want)
	})
}

// Foreground 返回当前前台窗口的句柄（0 = 问不到）。
//
// 用途：**同一页标题匹配到多个窗口时认哪一个**。用户点 📌 的那一下必然让"装着那一页的浏览器窗口"
// 成为前台 ⇒ 前台那个就是他要顶的那个（比"取枚举到的第一个"靠谱得多）。
func Foreground() uintptr {
	h, _, _ := pGetForegroundWindow.Call()
	return h
}

// enumWindows 按谓词枚举可见的顶层窗口。
//
// ⚠️ 两类窗口**无条件剔除**（2026-10-16 真机踩到的）—— exe 自己那个控制台窗口标题就是
// `C:\...\acetool-app\acetool.exe`，里面**含 `acetool`** ⇒ 老的 `Find("ACETool")` 会把它当
// "面板窗口"，于是 📌 去置顶**自己的控制台**（真机上读回来的 `windowTitle` 就是那条 exe 路径）：
//  1. **本进程自己的窗口**（`GetWindowThreadProcessId == 我们的 pid`）；
//  2. **标题里含我们自己 exe 路径的窗口** —— 控制台窗口属于 **conhost / Windows Terminal 进程**，
//     它的 pid **不是**我们的（所以第 1 条抓不到它），只能认标题里那段 exe 路径。
//
// 我们的控制台永远不是"面板所在的浏览器窗口"，没有第二种解释，所以这里无条件剔除。
func enumWindows(match func(string) bool) ([]Window, error) {
	return enumWindowsAll(false, match)
}

// enumWindowsAll 是枚举的**唯一实现**；`includeOwn` = 要不要把本进程的窗口也列进来。
//
// ⚠️ 原有调用一律**不含本进程**（这是老行为，`List`/`FindPrefixed` 那一路都靠它避免"贴到自己身上"）；
// `ZOrder` 是唯一一个显式要含本进程的（理由见它自己的注释）。
//
// ⛔⛔ **这里曾经每调一次就 `syscall.NewCallback` 一次 —— 那是 §72「带细条之后 exe 会静默退出」的真因**：
// Go 的 Windows 回调表是**定长**的（`runtime/syscall_windows.go` 的 `cbMax`，约两千个）而且**永不释放**
// （`syscall.NewCallback` 的文档原话：the function is never freed）。细条**每个 tick（120ms）**都调一次
// `List()`（`aceWindow` 里找 ACE 窗口）⇒ 约 3 分 40 秒就把表用光 ⇒ Go 抛
// `fatal error: too many callback functions` ⇒ **exit 2**；而 detached 进程的 stderr 谁也看不见
// ⇒ 表现就是"exe 自己没了、`exit.log` 一条记录都没有、事件日志里也没有崩溃"。
// 2026-10-17 上午用 `devtools/exitwatch` + **把 stderr 重定向到文件** 抓到的栈：
//
//	fatal error: too many callback functions
//	goroutine 19 [running, locked to thread]:
//	syscall.compileCallback → syscall.NewCallback
//	→ win.enumWindowsAll → win.List → rail.aceWindow → rail.follow → rail.tick → rail.loop
//
// ⇒ 修法：回调**全程只建一个**（`enumCb`，`sync.Once` 里建），每次枚举把"要收什么"放进包级变量
// 再用同一个回调去跑。`enumMu` 保证同一时刻只有一次枚举（回调里的包级变量不会被另一次调用踩坏）。
var (
	enumMu    sync.Mutex
	enumOut   []Window
	enumMatch func(string) bool
	enumOwn   uint32
	enumKeep  bool // includeOwn
	enumOnce  sync.Once
	enumCb    uintptr
)

func enumWindowsAll(includeOwn bool, match func(string) bool) ([]Window, error) {
	if match == nil {
		match = func(string) bool { return true }
	}
	enumMu.Lock()
	defer enumMu.Unlock()
	enumOut = enumOut[:0]
	enumMatch, enumOwn, enumKeep = match, ownPID(), includeOwn
	enumOnce.Do(func() { enumCb = syscall.NewCallback(enumProc) })
	ret, _, err := pEnumWindows.Call(enumCb, 0)
	out := enumOut
	enumMatch = nil // 别把闭包留着（它会引用调用方的栈变量）
	enumOut = nil
	if ret == 0 {
		return out, fmt.Errorf("EnumWindows 失败：%v", err)
	}
	return out, nil
}

// enumProc 是 `EnumWindows` **唯一那个**回调（见上面那段 §72 的由来 —— 千万别改回"每次新建"）。
func enumProc(hwnd uintptr, _ uintptr) uintptr {
	if vis, _, _ := pIsWindowVisible.Call(hwnd); vis == 0 {
		return 1 // 继续枚举
	}
	pid := windowPID(hwnd)
	if !enumKeep && pid == enumOwn {
		return 1
	}
	t := windowText(hwnd)
	if t == "" || isOwnConsoleTitle(t) {
		return 1
	}
	if enumMatch != nil && enumMatch(t) {
		enumOut = append(enumOut, Window{Handle: hwnd, Title: t, PID: pid, Class: className(hwnd)})
	}
	return 1
}

func ownPID() uint32 {
	p, _, _ := pGetCurrentProcessId.Call()
	return uint32(p)
}

// windowPID 读窗口属于哪个**进程**。
//
// ⛔ 真机踩到的坑（2026-10-16）：`GetWindowThreadProcessId(hwnd, &pid)` 的**返回值是线程 id**，
// 进程 id 是**写进第二个参数**的那个 DWORD。第一版把返回值当 pid 用（还传了 NULL 当出参）⇒
// 所有 pid 都是**线程 id**，"剔除本进程窗口"这条判据**从来没生效过**。
// 查 pid：`Get-Process` 说 ACE 的窗口属于 30600，而窗口枚举报的是 14312（那是个 TID）。
func windowPID(hwnd uintptr) uint32 {
	var pid uint32
	pGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid
}

// Resize 只改窗口大小、**不动位置**（`SWP_NOMOVE|SWP_NOZORDER|SWP_NOACTIVATE`）。
//
// 用途：app 窗口的尺寸命令行常被"记住的旧尺寸"顶掉（见 `AppWindow` 的注释）⇒ 开完自己摆。
func Resize(hwnd uintptr, w, h int32) error {
	if !Alive(hwnd) {
		return fmt.Errorf("窗口句柄已失效（0x%x）", hwnd)
	}
	if w <= 0 || h <= 0 {
		return fmt.Errorf("尺寸不合法：%dx%d", w, h)
	}
	ret, _, err := pSetWindowPos.Call(hwnd, 0, 0, 0, uintptr(w), uintptr(h),
		SWP_NOMOVE|SWP_NOZORDER|SWP_NOACTIVATE)
	if ret == 0 {
		return fmt.Errorf("SetWindowPos(改尺寸) 被拒：%v", err)
	}
	return nil
}

// Owner 返回窗口的属主（`GetWindow(hwnd, GW_OWNER)`）—— 有属主的顶层窗口是**弹出/工具窗**，
// 不是"主窗口"（ACE 那几个 96x56 / 152x48 的小窗就是这么来的）。0 = 没有属主。
func Owner(hwnd uintptr) uintptr {
	h, _, _ := pGetWindow.Call(hwnd, GW_OWNER)
	return h
}

// ParentOf 返回窗口的**父窗口**（`GetParent`；对子窗口是父窗口，对弹出窗是属主）——0 = 没有。
//
// 为什么需要它：细条第五轮改成 ACE 窗口的**子窗口**（`SetParent`）⇒ "到底挂上没有"必须**量**，
// 不能信 `SetParent` 的返回值（它回的是**老父窗口**，而且 `SetParent` 失败时也可能回 0 而不报错）。
func ParentOf(hwnd uintptr) uintptr {
	h, _, _ := pGetParent.Call(hwnd)
	return h
}

// HasOwner 见 Owner：true = 这是弹出窗/工具窗，不是应用主窗。
func HasOwner(hwnd uintptr) bool { return Owner(hwnd) != 0 }

// ownExeLower 缓存我们自己可执行文件的路径（小写）—— 只取一次，枚举里每次都问太浪费。
var (
	ownExeOnce  sync.Once
	ownExeLower string
)

// isOwnConsoleTitle 判断这个窗口标题是不是"我们自己的控制台"（见 enumWindows 的说明）。
func isOwnConsoleTitle(title string) bool {
	ownExeOnce.Do(func() {
		if p, err := os.Executable(); err == nil {
			ownExeLower = strings.ToLower(p)
		}
	})
	return ownExeLower != "" && strings.Contains(strings.ToLower(title), ownExeLower)
}

func windowText(hwnd uintptr) string {
	n, _, _ := pGetWindowTextLengthW.Call(hwnd)
	buf := make([]uint16, int(n)+2)
	if len(buf) == 0 {
		return ""
	}
	pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

// className 读窗口类名（真机侦察用：ACE 主窗与钢琴窗的类名能把它们区分开）。
func className(hwnd uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := pGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

// Alive 判断句柄还是不是个活窗口（缓存句柄前必须问一句 —— 句柄会被系统复用，不能盲信）。
func Alive(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	ret, _, _ := pIsWindow.Call(hwnd)
	return ret != 0
}

// IsTopMost 读这个窗口现在是不是置顶（看 `WS_EX_TOPMOST` 扩展样式）。
func IsTopMost(hwnd uintptr) bool {
	v, _, _ := pGetWindowLongPtrW.Call(hwnd, GWL_EXSTYLE)
	return v&WS_EX_TOPMOST != 0
}

// PIDOf 读窗口属于哪个进程（`Window.PID` 也是它；单独导出是给"这个点上是谁"那类调用用）。
func PIDOf(hwnd uintptr) uint32 { return windowPID(hwnd) }

// ClassOf 读窗口类名（导出给自检/诊断用）。
func ClassOf(hwnd uintptr) string { return className(hwnd) }

// RootOf 从任意（子）窗口找到它的**顶层窗口**（`GA_ROOT`）。
//
// 为什么需要：`WindowFromPoint` 命中的常常是子窗口（真机实测：细条那个点上回的是
// `Chrome_RenderWidgetHostHWND`，那只是浏览器的一个子窗）⇒ 要判"谁盖住了我"得抬到顶层再比。
func RootOf(hwnd uintptr) uintptr {
	if hwnd == 0 {
		return 0
	}
	root, _, _ := pGetAncestor.Call(hwnd, gaRoot)
	return root
}

// gaRoot 是 `GetAncestor` 的 `GA_ROOT`（顶层窗口）。
const gaRoot = 2

// WindowAt 返回**屏幕坐标那一点上真正可见的窗口**（最上面那个），0 = 那个点上没有窗口。
//
// 用途：细条是**画在 ACE 窗口里面**的一条窄窗，"它现在到底看不看得见"从我们自己的状态里读不出来
// （`ShowWindow` 没藏它 ≠ 它没被别的窗口盖住）。用户 2026-10-17 报「关闭 web 面板后侧栏会消失」
// 就是这种：`/api/rail` 报 `visible=true`，而 `WindowAt` 在那个点上回的是别的窗口
// （真机量到过 Edge 与 ACE 两种）⇒ `devtools/railprobe` 把这条判据摆出来了。
//
// ⚠️ `WindowFromPoint(POINT)` 的 `POINT` 是**按值传的结构体**（8 字节，amd64 走寄存器）⇒
// 把两个 int32 打包进一个 uintptr 传（低 32 位 = x，高 32 位 = y）。传指针是错的（会回 0）。
func WindowAt(x, y int32) uintptr {
	packed := uintptr(uint32(x)) | uintptr(uint32(y))<<32
	h, _, _ := pWindowFromPoint.Call(packed)
	return h
}

// SetTopMost 置顶 / 取消置顶。**不激活、不移动、不改大小**（只动 Z 序那一位）。
func SetTopMost(hwnd uintptr, on bool) error {
	if !Alive(hwnd) {
		return fmt.Errorf("窗口句柄已失效（0x%x）—— 重新找一次", hwnd)
	}
	after := HWND_NOTOPMOST
	if on {
		after = HWND_TOPMOST
	}
	ret, _, err := pSetWindowPos.Call(hwnd, after, 0, 0, 0, 0,
		SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE)
	if ret == 0 {
		return fmt.Errorf("SetWindowPos 被拒：%v（常见原因：ACE 与浏览器**权限级别不同**，一个以管理员运行、一个不是）", err)
	}
	return nil
}

// Focus 把窗口调到前台（最小化就先还原）。**返回是否真的成功了** —— 前台锁可能不让抢。
func Focus(hwnd uintptr) (bool, error) {
	if !Alive(hwnd) {
		return false, fmt.Errorf("窗口句柄已失效（0x%x）—— 重新找一次", hwnd)
	}
	if ic, _, _ := pIsIconic.Call(hwnd); ic != 0 {
		pShowWindow.Call(hwnd, SW_RESTORE)
	}
	// 前台锁：后台进程不许抢前台。经典绕法 = 把自己的输入队列**附加到当前前台线程**上冒充它。
	fg, _, _ := pGetForegroundWindow.Call()
	cur, _, _ := pGetCurrentThreadId.Call()
	fgThread, _, _ := pGetWindowThreadProcessId.Call(fg, 0)
	attached := false
	if fgThread != 0 && fgThread != cur {
		if r, _, _ := pAttachThreadInput.Call(cur, fgThread, 1); r != 0 {
			attached = true
		}
	}
	pBringWindowToTop.Call(hwnd)
	ret, _, _ := pSetForegroundWindow.Call(hwnd)
	if attached {
		pAttachThreadInput.Call(cur, fgThread, 0)
	}
	if ret == 0 {
		return false, fmt.Errorf("SetForegroundWindow 被前台锁挡下（Windows 只让「刚收到输入」的进程抢前台）" +
			"—— 从任务栏点一下那个浏览器窗口即可")
	}
	return true, nil
}
