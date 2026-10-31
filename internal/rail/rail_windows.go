//go:build windows

package rail

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unsafe"

	"acetool/internal/web"
	"acetool/internal/win"
)

/* ============================ 外观常量 ============================
 * 尺寸按**逻辑像素**写，创建窗口时乘 `dpiScale`（exe 是 DPI 感知的 ⇒ 150% 屏上要乘 1.5）。
 * 颜色与面板同源（面板 --panel/--line/--fg/--accent 那套），三类色与 home 的按钮同一套语义。
 */
const (
	/* ⭐ 三级（用户 2026-10-16 的口径）：
	 *   ① 默认**特别细**（只有一条颜色码，连字都没有）
	 *   ② **靠近**了显示 1–2 个汉字（简称）
	 *   ③ 悬停到**字上**，在**栏外**（细条左边）弹一个小窗显示全名
	 * ⇒ 只有前两级动宽度，右侧锚点（贴 ACE 右缘）不动、字也不动。
	 */
	levelThinW  = 5  // ① 特别细（就一条颜色码的宽度）
	levelShortW = 34 // ② 两个字
	nearPx      = 52 // "靠近"的判定：光标离右缘多近算靠近（逻辑像素）

	tipFont = 15  // ③ 栏外全名小窗的字号（逻辑像素）
	tipMaxW = 260 // 它的最大宽度（太长的全称走省略号）

	// ⚠️ 给 ACE **自绘标题栏**让位（用户 2026-10-16：「现在挡关闭按钮了」）。
	// 真机实测：ACE 的窗口矩形与客户区**顶差 = 0** ⇒ 它是无边框窗口、关闭按钮画在**客户区**里，
	// 所以"从客户区顶边开始"也躲不开 ⇒ 只能从客户区顶边再往下让一段。42 逻辑像素按 150% 屏估的。
	topInset = 42

	defaultPad = 6 // 上下右留白
	minItemH   = 18

	// ⭐ 颜色**全部抄 web 面板的色板**（用户 2026-10-17：「颜色按照web统一」）——
	// 单一事实源 = `internal/web/assets/style.css` 的 `:root`。GDI 要的是 **0x00BBGGRR**，所以下面是
	// 同一个 hex 的 **BGR 倒序**（注释里写 RGB 原值，方便跟 css 对账）：
	//
	//	--bg     #282c33 → 0x332c28   --panel  #2f333a → 0x3a332f   --panel2 #171a20 → 0x201a17
	//	--line   #3f434a → 0x4a433f   --fg     #d6dae0 → 0xe0dad6   --dim    #9498a0 → 0xa09894
	//	--ok     #5ebd7a → 0x7abd5e   --warn   #d9a441 → 0x41a4d9   --accent #1a73e8 → 0xe8731a
	colorBG    = uintptr(0x332c28) // #282c33（--bg）
	colorTipBG = uintptr(0x3a332f) // #2f333a（--panel）：栏外小窗 / 参数条的底
	// 悬停：web 那边是 `.tool:hover{background:#ffffff0d}`（5% 白叠在 --panel 上）⇒ 这里直接算成实色，
	// 效果一致（#2f333a 叠 5% 白 ≈ #393d44）。
	colorHover  = uintptr(0x443d39) // #393d44（--panel + 5% 白）
	colorBorder = uintptr(0x4a433f) // #3f434a（--line）
	colorText   = uintptr(0xe0dad6) // #d6dae0（--fg）
	colorDim    = uintptr(0xa09894) // #9498a0（--dim）
	colorRun    = uintptr(0x7abd5e) // #5ebd7a（--ok）  绿：点一下就执行
	colorParams = uintptr(0x41a4d9) // #d9a441（--warn）琥珀：要参数
	colorPage   = uintptr(0xe8731a) // #1a73e8（--accent）蓝：开窗口
	// 红：**改工程文件**（`.acep` 文件路线 = 保存→读→改→替换→让 ACE 重新打开）。
	// 用户 2026-10-17：「把需要改工程的放到一起，用同色」+ web 端同步 ⇒ 用 web 已有的 `--bad`，
	// 不新造颜色（不往色板里加第五个语义色）。
	colorFile = uintptr(0x756ce0) // #e06c75（--bad）
	// 参数条里输入框的底色 = web 的 `--panel2`（比面板深，像凹进去的输入位；默认的纯白底太扎眼）
	colorFieldBG = uintptr(0x201a17) // #171a20（--panel2）
)

/* ============================ Win32 ============================ */

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")

	pRegisterClassExW  = user32.NewProc("RegisterClassExW")
	pCreateWindowExW   = user32.NewProc("CreateWindowExW")
	pDefWindowProcW    = user32.NewProc("DefWindowProcW")
	pDestroyWindow     = user32.NewProc("DestroyWindow")
	pPostQuitMessage   = user32.NewProc("PostQuitMessage")
	pShowWindow        = user32.NewProc("ShowWindow")
	pSetWindowPos      = user32.NewProc("SetWindowPos")
	pGetMessageW       = user32.NewProc("GetMessageW")
	pTranslateMessage  = user32.NewProc("TranslateMessage")
	pDispatchMessageW  = user32.NewProc("DispatchMessageW")
	pSetTimer          = user32.NewProc("SetTimer")
	pKillTimer         = user32.NewProc("KillTimer")
	pBeginPaint        = user32.NewProc("BeginPaint")
	pEndPaint          = user32.NewProc("EndPaint")
	pGetClientRect     = user32.NewProc("GetClientRect")
	pInvalidateRect    = user32.NewProc("InvalidateRect")
	pGetDpiForSystem   = user32.NewProc("GetDpiForSystem")
	pGetModuleHandleW  = kernel32.NewProc("GetModuleHandleW")
	pFillRect          = user32.NewProc("FillRect")
	pCreateSolidBrush  = gdi32.NewProc("CreateSolidBrush")
	pDeleteObject      = gdi32.NewProc("DeleteObject")
	pSetBkMode         = gdi32.NewProc("SetBkMode")
	pSetTextColor      = gdi32.NewProc("SetTextColor")
	pCreateFontW       = gdi32.NewProc("CreateFontW")
	pSelectObject      = gdi32.NewProc("SelectObject")
	pDrawTextW         = user32.NewProc("DrawTextW")
	pShellExecuteW     = shell32.NewProc("ShellExecuteW")
	pGetCursorPos      = user32.NewProc("GetCursorPos")
	pSetWindowLongPtrW = user32.NewProc("SetWindowLongPtrW")
	pGetWindowLongPtrW = user32.NewProc("GetWindowLongPtrW")
	// 第五轮（子窗口架构）：SetParent 把细条挂成 ACE 窗口的子窗口；PostMessage 用来给窗口发 WM_CLOSE。
	pSetParent    = user32.NewProc("SetParent")
	pPostMessageW = user32.NewProc("PostMessageW")
	// 参数条（`panel_windows.go`）：EDIT 的文本读写、拿键盘焦点、子类化
	pSetWindowTextW       = user32.NewProc("SetWindowTextW")
	pGetWindowTextW       = user32.NewProc("GetWindowTextW")
	pGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	pSetFocus             = user32.NewProc("SetFocus")
	pSetForegroundWindow  = user32.NewProc("SetForegroundWindow")
	pSendMessageW         = user32.NewProc("SendMessageW")
	pCallWindowProcW      = user32.NewProc("CallWindowProcW")
	pSetBkColor           = gdi32.NewProc("SetBkColor")
)

const (
	wsPopup = 0x80000000
	// wsChild = `WS_CHILD`：细条现在是 ACE 那个窗口的**子窗口**（见 attach 的注释）。
	// ⚠️ `SetParent` **不会**自己改样式 ⇒ 必须自己把 WS_POPUP 换成 WS_CHILD，
	// 否则它仍按顶层窗口对待（层级照旧要打架）。
	wsChild   = 0x40000000
	wsExTop   = 0x00000008
	wsExTool  = 0x00000080
	wsExNoAct = 0x08000000

	// GWL_STYLE = -16（读/写窗口样式）
	gwlStyle   = ^uintptr(15)
	gwlWndProc = ^uintptr(3) // GWLP_WNDPROC = -4（参数条的 EDIT 要子类化）

	wmPaint       = 0x000F
	wmClose       = 0x0010
	wmDestroy     = 0x0002
	wmEraseBkgnd  = 0x0014
	wmKeyDown     = 0x0100
	wmTimer       = 0x0113
	wmSetFont     = 0x0030
	wmActivate    = 0x0006
	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmLButtonUp   = 0x0202
	wmRButtonUp   = 0x0205
	// wmAppRun = **自定义**消息（WM_APP+1）：参数条里"回车 / 点执行"要跑工具，但**不能**在
	// EDIT 或面板自己的窗口过程里直接跑（那等于在自己的消息处理里把窗口销毁掉）。
	// 统一 `PostMessage` 给自己 ⇒ 等这一趟消息处理完再动。
	wmAppRun = 0x8000 + 1
	// wmCtlColorEdit：父窗口定输入框的底色/字色（不处理的话是系统白底黑字，在深色参数条上很扎眼）
	wmCtlColorEdit = 0x0133

	// 子控件样式（参数条的 EDIT）
	wsVisible     = 0x10000000
	wsBorder      = 0x00800000
	wsTabStop     = 0x00010000
	esLeft        = 0x0000
	esAutoHScroll = 0x0080

	swHide            = 0
	swShowNoActivate  = 4
	wsExTrans         = 0x00000020 // WS_EX_TRANSPARENT：鼠标穿透（栏外全名小窗不抢光标）
	swpNoActivate     = 0x0010
	swpShowWindow     = 0x0040
	swpNoZOrder       = 0x0004
	swpNoMove         = 0x0002
	swpNoSize         = 0x0001
	swpFrameChanged   = 0x0020
	dtLeft            = 0x00000000
	dtCenter          = 0x00000001
	dtRight           = 0x00000002
	dtVCenter         = 0x00000004
	dtSingleLine      = 0x00000020
	dtNoPrefix        = 0x00000800
	dtEndEllipsis     = 0x00008000
	transparentBkMode = 1
	fwNormal          = 400
	fwSemiBold        = 600

	timerFollow = 1
	followMS    = 120 // 跟 ACE 的窗口 + 看光标离得多近 ⇒ 两件事共用一个 120ms 定时器
)

type wndClassExW struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSmall  uintptr
}

type rect struct{ Left, Top, Right, Bottom int32 }

type point struct{ X, Y int32 }

type msgT struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

type paintStruct struct {
	Hdc       uintptr
	Erase     int32
	Paint     rect
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}

/* ============================ Rail ============================ */

// Options 是起细条要的那些东西。
type Options struct {
	Items   []Item
	BaseURL string // exe 自己的面板地址（点击走 `/api/run`、开页走它 + 页名）
	Pad     int    // 留白（逻辑像素）；0 = 默认
	// OnNote 把细条里发生的事报给面板的事件通道（`/api/events`）⇒ 面板上也能看见、自检也能查。
	OnNote func(kind, text string)
}

// State 是细条此刻的样子（`GET /api/rail` 就报它 —— 自检的抓手，也是以后设置页要用的）。
type State struct {
	Running     bool     `json:"running"`
	Visible     bool     `json:"visible"`
	Level       int      `json:"level"` // ⭐ 三级：0 = 特别细 / 1 = 靠近显示两个字 / 2 = 悬停到字上（字前显示全名）
	Hover       int      `json:"hover"` // 第③级里光标压在第几项（-1 = 没有）
	HWND        uintptr  `json:"hwnd"`
	Rect        win.Rect `json:"rect"`
	TargetTitle string   `json:"targetTitle,omitempty"`
	TargetHWND  uintptr  `json:"targetHwnd,omitempty"`
	TargetPID   uint32   `json:"targetPid,omitempty"`
	ItemH       int      `json:"itemH"`
	DPI         int      `json:"dpi"`
	Ticks       int      `json:"ticks"`   // 定时器跑了几次（"它到底在不在轮询"的证据）
	CursorX     int      `json:"cursorX"` // 它读到的光标位置（"靠近"判定全靠这个）
	CursorY     int      `json:"cursorY"`
	// TopMost 是**读回来**的实况（细条是子窗口 ⇒ 恒 false；留着给自检核"我们没在置顶"）。
	TopMost bool `json:"topMost"`
	// ParentHWND = 细条挂成了**哪个窗口的子窗口**（就是贴着的那个 ACE 窗口）。
	//
	// ⭐ 第五轮（2026-10-17）的架构：细条走 `SetParent` 变成 ACE 窗口的子窗口。理由与四次翻车都在 follow 的注释里；
	// 一句话：**子窗口天生在父窗口客户区之上、又根本不是顶层窗口** ⇒ "既不许被 ACE 盖住、也不许浮在别的应用上"
	// 这两条**同时**成立，而且全程**零 Z 序操作**（没有拉锯、不会把 ACE 的窗口一起提起来）。
	ParentHWND uintptr `json:"parentHwnd,omitempty"`
	// HitPID / HitClass / HitSelf = **细条那一点上真正最上面的窗口**（`WindowAt` 量的实况，只作诊断/自检）。
	// ⚠️ `visible` 只说我们没 `ShowWindow(SW_HIDE)`：被别的窗口盖住时它**照样是 true**（第四轮踩的坑）。
	// 判据：`hitSelf=true` ⇒ 此刻看得见；`hitPid` 是别的应用 ⇒ 那个应用盖住了 ACE（**按设计如此**）；
	// 而 **`hitPid` 永远不可能是 ACE** —— 子窗口不可能被自己的父窗口盖住，这条就是第四轮那个 bug 的不变量。
	HitPID   uint32 `json:"hitPid"`
	HitClass string `json:"hitClass,omitempty"`
	HitSelf  bool   `json:"hitSelf"`
	// PanelAnchor = 开参数条**那一刻**细条的矩形（参数条就是按它摆到左边的）。
	// 为什么单列一份：用户拖/缩 ACE 窗口时细条的 rect 会变，拿"现在的 rect"去核"当时的摆放"会假报错（真机踩到）。
	PanelAnchor   win.Rect `json:"panelAnchor,omitempty"`
	TargetClient  win.Rect `json:"targetClient"`         // 贴的那个窗口的**客户区**（自检核"让开了标题栏/在客户区里"）
	TargetRect    win.Rect `json:"targetRect"`           // 那个窗口的**整个矩形**（细条贴的是**外缘**，见 follow 的注释）
	TipText       string   `json:"tipText,omitempty"`    // 栏外那个小窗现在写的字（空 = 没弹）
	TipRect       win.Rect `json:"tipRect"`              // 它弹在哪（自检要能核："确实在栏外"）
	FakeCursor    bool     `json:"fakeCursor,omitempty"` // true = 这个光标位置是**自检注入**的
	AceForeground bool     `json:"aceForeground"`        // ACE 是不是前台（诊断；细条的层级**不再**跟着它走）
	// 参数条（`panel_windows.go`）此刻的样子 —— 自检的抓手：开着哪一项、滑块停在第几档、窗口在哪、
	// 框里该是什么字（`panelText` 是开/跑那一刻记下的快照，值来自 `%APPDATA%\ACETool\params.json`）。
	PanelItem int      `json:"panelItem"` // -1 = 没开
	PanelTier int      `json:"panelTier"`
	PanelHWND uintptr  `json:"panelHwnd,omitempty"`
	PanelRect win.Rect `json:"panelRect,omitempty"`
	PanelText []string `json:"panelText,omitempty"`
	FontOK    bool     `json:"fontOk"`   // 字体句柄建出来了没
	FontPx    int      `json:"fontPx"`   // 用的字号（逻辑像素）
	LastDraw  int32    `json:"lastDraw"` // DrawTextW 的返回值：0 = 一个字都没画出来
	LastText  string   `json:"lastText,omitempty"`
	Note      string   `json:"note,omitempty"`
	LastErr   string   `json:"lastErr,omitempty"`
}

// Rail 是那条细条（**全程只有一个**：窗口过程靠包级变量找它）。
type Rail struct {
	opts  Options
	items []Item

	mu        sync.Mutex
	state     State
	lastRect  win.Rect
	hover     int
	noteAt    time.Time
	lastClick time.Time // 点击去抖：同一颗按钮 400ms 内只认一次
	noteKind  string

	hwnd  uintptr
	class *uint16
	title *uint16

	acePIDs   []uint32
	acePIDAt  time.Time
	target    win.Window
	targetSet bool
	// parentHWND = 我们挂成了哪个窗口的**子窗口**（= 贴着的那个 ACE 窗口）。
	// ⚠️ 它跟 `hwnd` 一样**只在消息循环那个线程上读写**（wndProc/tick），HTTP 那边只读 State 快照。
	parentHWND uintptr
	// stopping=true = 是我们主动 `Stop()`（那时销毁窗口要退出消息循环）；
	// false 时窗口被销毁 = **ACE 关了我们被系统一起销毁** ⇒ 不退出，等 ACE 回来重建（见 tick）。
	stopping bool
	hInst    uintptr

	tipHWND  uintptr // 第③级那个"栏外弹全名"的小窗
	tipClass *uint16
	tipText  string

	// ── 参数条（`panel_windows.go`）：点「连接」/「音素」弹出来的临时小窗 ──
	// ⚠️ 这些字段**只在消息循环线程上读写**（`tick`/`panelProc`），只有 `paramItem` 例外
	// （HTTP 那边通过 `wantPanel`/`panelOpen()` 问）。
	paramHWND    uintptr
	paramClass   *uint16
	paramItem    int  // 现在开着第几项（-1 = 没开）
	paramTier    int  // 滑块停在第几档
	paramDrag    bool // 正在拖滑块
	paramActive  bool // 参数条**真的**拿到过焦点（用来挡"还没激活就收到 WA_INACTIVE"）
	paramRunDown bool // 按下时在「执行」按钮上（抬起还在才算点）
	// paramActiveAt = 参数条上一次**真正**拿到焦点的时刻。用来挡"刚激活就被抢走"：
	// 全屏游戏之类会立刻把前台抢回去，那时收起会让参数条闪一下就没、用户来不及打字。
	paramActiveAt time.Time
	fieldBrush    uintptr   // 输入框底色的画刷（`WM_CTLCOLOREDIT` 要回它；建一次就够）
	paramEdits    []uintptr // 两个 EDIT 的句柄
	paramEditOld  []uintptr // 子类化之前它们各自的窗口过程
	wantPanel     int       // -2 = 没请求；-1 = 请收起；>=0 = 请打开第几项（`click()` 里设，`tick()` 里做）
	// railClient = 细条在**父窗口客户区坐标系**里的矩形（子窗口的 `SetWindowPos` 要的是这个坐标）。
	// 屏幕坐标那份在 `state.Rect` 里（三级判定/悬停/小窗对齐都用屏幕坐标）—— 两份都留着，别混。
	railClient rect
	lastClient rect

	// fakeCursor 是**只给自检用**的光标替身（`SetCursorOverride`）。为什么要有它：
	// 三级判定全靠"光标离右缘多近"，而真机自检时**抢用户的鼠标**是不行的（脚本把光标放过去，
	// 150ms 后就被用户真实的手覆盖 —— 实测过）。注入一个假坐标就能确定性地验证三级 + 截图。
	fakeCursor *point
}

// theRail 是唯一实例（窗口过程拿不到 Go 指针，只能从包级变量取 —— 单实例是这个包的设计前提）。
var theRail *Rail

// Start 起细条：在**自己锁死的 OS 线程**上建窗口并跑消息循环（与热键那套互不干扰）。
//
// 返回即代表窗口建好了（建失败会把错误带回来，不静默）。
func Start(opts Options) (*Rail, error) {
	if opts.Items == nil {
		opts.Items = Items()
	}
	if opts.Pad <= 0 {
		opts.Pad = defaultPad
	}
	r := &Rail{opts: opts, items: opts.Items, hover: -1,
		paramItem: -1, wantPanel: -2,
		state: State{Running: true, Hover: -1}}
	theRail = r
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread() // 消息队列认线程 ⇒ 必须锁死
		if err := r.create(); err != nil {
			done <- err
			return
		}
		done <- nil
		r.loop()
	}()
	if err := <-done; err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Rail) setErr(err error) {
	r.mu.Lock()
	if err != nil {
		r.state.LastErr = err.Error()
	}
	r.mu.Unlock()
}

func (r *Rail) note(text string) {
	r.mu.Lock()
	r.state.Note = text
	r.noteAt = time.Now()
	r.mu.Unlock()
	if r.opts.OnNote != nil {
		r.opts.OnNote("info", text)
	}
}

// State 读此刻状态（`/api/rail`）。
func (r *Rail) State() State {
	r.mu.Lock()
	st := r.state
	panelOpen, edits := r.paramItem, append([]uintptr(nil), r.paramEdits...)
	r.mu.Unlock()
	// 参数条开着时，把两个输入框里**现在**的字读出来（不是开那一刻的快照 ——
	// 自检要能验"输入框真的收到字了"）。同进程跨线程 `GetWindowText` 是安全的（内部走 WM_GETTEXT）。
	if panelOpen >= 0 && len(edits) > 0 {
		txt := make([]string, 0, len(edits))
		for _, e := range edits {
			txt = append(txt, windowTextOf(e))
		}
		st.PanelText = txt
	}
	return st
}

// Note 往细条里塞一条提示（面板那边也能调，比如"这一批已撤回"）。
func (r *Rail) Note(text string) { r.note(text) }

// SetCursorOverride 注入一个**假光标位置**（x<0 或 y<0 = 取消，回到真的鼠标）。
//
// ⛔ 只给自检用：三级判定依赖"光标离右缘多近"，真机自检不能去抢用户的鼠标
// （脚本放过去的光标 150ms 就被真实的手覆盖了）。注入之后三级是**确定性**的，也能截图。
func (r *Rail) SetCursorOverride(x, y int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if x < 0 || y < 0 {
		r.fakeCursor = nil
		r.state.FakeCursor = false
		return
	}
	r.fakeCursor = &point{X: int32(x), Y: int32(y)}
	r.state.FakeCursor = true
}

// isClassExists 判断"注册窗口类失败"是不是**这个类已经注册过了**（ERROR_CLASS_ALREADY_EXISTS = 1410）。
//
// 为什么会有这一出：切换按钮是"停掉侧条 → 再起"⇒ 在**同一个进程**里会第二次 `RegisterClassExW`
// 同一个类名 ⇒ 系统返回 1410。那**不是错误**（我们想要的类就在那儿），但第一版把它当致命错误
// ⇒ 表现是"**第二次切到侧栏静默失败**"（回包 `ok:false`、细条不出现）。真机自检时逮到的。
func isClassExists(err error) bool {
	if e, ok := err.(syscall.Errno); ok {
		return e == 1410
	}
	return false
}

func dpiScale() (int, float64) {
	dpi := uintptr(96)
	if err := pGetDpiForSystem.Find(); err == nil {
		if v, _, _ := pGetDpiForSystem.Call(); v > 0 {
			dpi = v
		}
	}
	return int(dpi), float64(dpi) / 96.0
}

func (r *Rail) create() error {
	dpi, _ := dpiScale()
	r.state.DPI = dpi
	r.state.ItemH = 0
	r.state.PanelItem = -1 // ⚠️ 零值 0 会被误读成"开着第 0 项（连接）" ⇒ 必须显式 -1
	r.class, _ = syscall.UTF16PtrFromString("ACEToolRail")
	r.title, _ = syscall.UTF16PtrFromString("ACETool 细条")
	r.tipClass, _ = syscall.UTF16PtrFromString("ACEToolRailTip")
	r.tipText = "ACETool 全名"
	r.hInst, _, _ = pGetModuleHandleW.Call(0)
	if err := r.makeWindows(); err != nil {
		return err
	}
	// ⚠️ 定时器挂在**线程**上（`SetTimer(0,…)`），不是挂在窗口上：
	// 细条是 ACE 窗口的**子窗口** ⇒ ACE 一关它就被系统一起销毁，挂在窗口上的定时器会跟着消失，
	// 那样"等 ACE 回来重建窗口"这条自救路就断了。线程定时器的 WM_TIMER 是**线程消息**（hwnd=0，
	// 不经过窗口过程）⇒ 由 `loop()` 接（见那里）。
	pSetTimer.Call(0, timerFollow, followMS, 0)
	return nil
}

// railWndProc 是窗口过程的那个**唯一的**回调地址。
//
// ⛔ 别在 `makeWindows` 里每次 `syscall.NewCallback(wndProc)`：Go 的 Windows 回调表**定长**（约两千）且
// **永不释放**，而细条会**重建窗口**（ACE 关掉/换窗口时）⇒ 每重建一次就漏一个。§72 那个
// `fatal error: too many callback functions` 就是同一类问题（见 `internal/win/window_windows.go`
// 的 `enumWindowsAll`，那里的漏得更快）。这里用 `sync.Once` 只建一个。
var (
	wndProcOnce sync.Once
	wndProcAddr uintptr
)

func wndProcCB() uintptr {
	wndProcOnce.Do(func() { wndProcAddr = syscall.NewCallback(wndProc) })
	return wndProcAddr
}

// safe 把一次回调包起来：**panic 就地救回来 + 落一条日志**，而不是让整个 exe 静默退出。
//
// 为什么必须这么做（§72 的教训）：窗口过程 / 定时器 tick 都是**被系统调用回来的**，那里一旦 panic，
// Go 的行为是**直接结束进程**（exit 2），而 detached 进程的 stderr 谁也看不见 ⇒ 用户看到的就是
// "exe 自己没了、什么记录都没有"。§72 的真因（回调表用光）已经修掉，但**兜底不能省**：
// 救回来之后细条还能继续用，日志留在 `%APPDATA%\ACETool\rail.log`。
func safe(what string, f func()) {
	defer func() {
		if e := recover(); e != nil {
			buf := make([]byte, 8192)
			n := runtime.Stack(buf, false)
			railLog("细条里的 %s 出错了（已救回、进程继续跑）：%v\n%s", what, e, string(buf[:n]))
			if theRail != nil {
				theRail.note("侧栏内部出了个错（已救回，明细见 rail.log）")
			}
		}
	}()
	f()
}

// railLog 往 `%APPDATA%\ACETool\rail.log` 追加一条（只在出错时写，免得刷屏）。
func railLog(format string, a ...any) {
	dir := filepath.Join(os.Getenv("APPDATA"), "ACETool")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "rail.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s  %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, a...))
}

// makeWindows 建那两个窗口（细条 + 栏外全名小窗）。
//
// 单独拆出来是为了**能重建**：ACE 一关，子窗口被系统销毁 ⇒ ACE 回来时要再建一个（见 tick）。
func (r *Rail) makeWindows() error {
	hInst := r.hInst
	wc := wndClassExW{
		Size:      uint32(unsafe.Sizeof(wndClassExW{})),
		WndProc:   wndProcCB(),
		Instance:  hInst,
		ClassName: r.class,
	}
	if ret, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); ret == 0 && !isClassExists(err) {
		return fmt.Errorf("RegisterClassExW 失败：%v", err)
	}
	_, scale := dpiScale()
	w := int32(float64(levelThinW) * scale)
	h := int32(float64(400) * scale)
	// ⚠️ 先按 **WS_POPUP** 建（还没挂到 ACE 上时它是独立的顶层窗口）；`attach` 里再换成 WS_CHILD。
	hwnd, _, err := pCreateWindowExW.Call(
		wsExTool|wsExNoAct, uintptr(unsafe.Pointer(r.class)), uintptr(unsafe.Pointer(r.title)),
		wsPopup, 0, 0, uintptr(w), uintptr(h), 0, 0, hInst, 0)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowExW 失败：%v", err)
	}
	r.hwnd = hwnd
	// 第③级那个"栏外弹全名"的小窗：同一个窗口过程，另一套类名与样式。
	// `WS_EX_TRANSPARENT` = **鼠标穿透** —— 它贴在细条左边，不能把光标从细条上抢走。
	wc2 := wndClassExW{
		Size:      uint32(unsafe.Sizeof(wndClassExW{})),
		WndProc:   wndProcCB(),
		Instance:  hInst,
		ClassName: r.tipClass,
	}
	if ret, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc2))); ret == 0 && !isClassExists(err) {
		return fmt.Errorf("RegisterClassExW（全名小窗）失败：%v", err)
	}
	tipTitle, _ := syscall.UTF16PtrFromString(r.tipText)
	tip, _, err := pCreateWindowExW.Call(
		wsExTool|wsExNoAct|wsExTrans, uintptr(unsafe.Pointer(r.tipClass)), uintptr(unsafe.Pointer(tipTitle)),
		wsPopup, 0, 0, 10, 10, 0, 0, hInst, 0)
	if tip == 0 {
		return fmt.Errorf("CreateWindowExW（全名小窗）失败：%v", err)
	}
	r.tipHWND = tip
	r.parentHWND = 0
	r.mu.Lock()
	r.state.HWND = hwnd
	r.state.ParentHWND = 0
	r.mu.Unlock()
	// 先都藏起来：等跟到 ACE 的窗口再显示（别在屏幕左上角闪一下）
	pShowWindow.Call(hwnd, swHide)
	pShowWindow.Call(tip, swHide)
	return nil
}

func (r *Rail) loop() {
	var m msgT
	for {
		ret, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			return
		}
		// 线程定时器（`SetTimer(0,…)`）的 WM_TIMER 是**线程消息**：hwnd=0，不经过窗口过程
		// ⇒ 必须在这儿接（这也正是"ACE 关掉、窗口没了，我们还能继续轮询"的原因）。
		if m.Hwnd == 0 && m.Message == wmTimer {
			r.tick()
			continue
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// tick 是定时器那一下：跟一次 ACE 的窗口 + 更新三级。
//
// ⚠️ 它还负责**重建窗口**：细条是 ACE 窗口的子窗口 ⇒ **ACE 一关它就被系统一起销毁**
// （WM_DESTROY 走到 wndProc，但那时 `stopping=false` ⇒ 不退出循环）⇒ ACE 回来时在这儿重建。
func (r *Rail) tick() {
	r.mu.Lock()
	r.state.Ticks++
	r.mu.Unlock()
	safe("定时器 tick", func() {
		if r.hwnd == 0 || !win.Alive(r.hwnd) {
			if err := r.makeWindows(); err != nil {
				r.setErr(err)
				return
			}
		}
		// 参数条的开/收请求在这里做（`click()` 跑在 goroutine 里，而建窗口/拿焦点认线程）
		r.mu.Lock()
		want := r.wantPanel
		r.mu.Unlock()
		if want != -2 {
			r.mu.Lock()
			r.wantPanel = -2
			r.mu.Unlock()
			if want < 0 {
				if r.paramItem >= 0 {
					r.closePanel()
				}
			} else if want != r.paramItem {
				r.openPanel(want)
			} else {
				r.closePanel() // 再点同一项 = 收起（跟菜单一样）
			}
		}
		r.follow()
		r.updateLevel()
	})
}

// Stop 关掉细条（切换按钮 / 进程退出前调）。
//
// ⚠️ 不在调用线程上直接 `DestroyWindow`：那样 WM_DESTROY 会在**调用线程**上派发，
// 于是 `PostQuitMessage` 投给的是**那个线程**的队列（消息循环那个线程收不到 ⇒ 循环空转不退出）。
// 改成给窗口**发 WM_CLOSE**，销毁与退出都在消息循环那个线程上发生。
func (r *Rail) Stop() {
	if r.paramItem >= 0 {
		// 参数条是顶层小窗，不归细条管 ⇒ 得单独收（`Stop` 可能跑在别的线程上，
		// 所以这里用 `WM_CLOSE` 交给消息循环去销毁）
		pPostMessageW.Call(r.paramHWND, wmClose, 0, 0)
	}
	if r.hwnd != 0 {
		pPostMessageW.Call(r.hwnd, wmClose, 0, 0)
	}
	r.mu.Lock()
	r.state.Running = false
	r.mu.Unlock()
}

/* ============================ 第③级：栏外弹全名 ============================ */

// syncTip 决定那个小窗弹不弹、写什么、弹在哪。
//
// 用户 2026-10-16：「能不能让它在栏外弹出全名」⇒ 全名**不回细条里**，而是贴在细条**左边**
// 弹一个小窗、与光标那一行对齐；细条本身只长到"两个字"那么宽（字永远不动）。
func (r *Rail) syncTip(level, hover int) {
	if r.tipHWND == 0 {
		return
	}
	var show bool
	var text string
	var x, y, w, h int32
	if level == 2 && hover >= 0 && hover < len(r.items) {
		r.mu.Lock()
		rc, itemH := r.railClient, int32(r.state.ItemH)
		r.mu.Unlock()
		if itemH > 0 {
			show = true
			text = r.itemTip(r.items[hover])
			_, scale := dpiScale()
			font := int32(float64(tipFont) * scale)
			padX := int32(float64(8) * scale)
			padY := int32(float64(5) * scale)
			// 宽度按"一个汉字 ≈ 一个字号"估（CJK 步进就是这样），再夹上限
			w = int32(float64(len([]rune(text)))*float64(font)*1.05) + 2*padX
			if maxW := int32(float64(tipMaxW) * scale); w > maxW {
				w = maxW
			}
			h = font + 2*padY
			// ⚠️ 坐标是**父窗口客户区**坐标（小窗也是 ACE 的子窗口，见 attach）：
			// 所以这里用 `railClient`（客户区坐标系），不能用 `state.Rect`（屏幕坐标系）。
			rowTop := rc.Top + int32(float64(r.opts.Pad)*scale) + int32(hover)*itemH
			x = rc.Left - w - int32(float64(6)*scale) // **栏外**：细条的左边
			y = rowTop + (itemH-h)/2
			if x < 0 {
				x = 0 // 贴的是父客户区左缘时别跑到客户区外（子窗口超出部分会被裁掉）
			}
		}
	}
	r.mu.Lock()
	if show {
		r.state.TipText = text
	} else {
		r.state.TipText, r.state.TipRect = "", win.Rect{}
	}
	r.mu.Unlock()
	if !show {
		pShowWindow.Call(r.tipHWND, swHide)
		return
	}
	pSetWindowPos.Call(r.tipHWND, 0, uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		swpNoActivate|swpShowWindow|swpNoZOrder)
	// 屏幕矩形读回来填状态（自检核"确实在细条左边/与光标那行对齐"用的就是它）
	if tr, err := win.RectOf(r.tipHWND); err == nil {
		r.mu.Lock()
		r.state.TipRect = tr
		r.mu.Unlock()
	}
	pInvalidateRect.Call(r.tipHWND, 0, 1)
}

func (r *Rail) paintTip() {
	var ps paintStruct
	hdc, _, _ := pBeginPaint.Call(r.tipHWND, uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	defer pEndPaint.Call(r.tipHWND, uintptr(unsafe.Pointer(&ps)))
	var cr rect
	pGetClientRect.Call(r.tipHWND, uintptr(unsafe.Pointer(&cr)))
	bg, _, _ := pCreateSolidBrush.Call(colorTipBG)
	pFillRect.Call(hdc, uintptr(unsafe.Pointer(&cr)), bg)
	pDeleteObject.Call(bg)
	bd, _, _ := pCreateSolidBrush.Call(colorBorder)
	for _, e := range []rect{
		{cr.Left, cr.Top, cr.Right, cr.Top + 1},
		{cr.Left, cr.Bottom - 1, cr.Right, cr.Bottom},
		{cr.Left, cr.Top, cr.Left + 1, cr.Bottom},
		{cr.Right - 1, cr.Top, cr.Right, cr.Bottom},
	} {
		pFillRect.Call(hdc, uintptr(unsafe.Pointer(&e)), bd)
	}
	pDeleteObject.Call(bd)

	_, scale := dpiScale()
	padX := int32(float64(8) * scale)
	f := makeFont(tipFont, fwNormal)
	old, _, _ := pSelectObject.Call(hdc, f)
	pSetBkMode.Call(hdc, transparentBkMode)
	pSetTextColor.Call(hdc, colorText)
	r.mu.Lock()
	txt := r.state.TipText
	r.mu.Unlock()
	r.text(hdc, txt, rect{cr.Left + padX, cr.Top, cr.Right - padX/2, cr.Bottom},
		dtLeft|dtVCenter|dtSingleLine|dtNoPrefix|dtEndEllipsis)
	pSelectObject.Call(hdc, old)
	pDeleteObject.Call(f)
}

/* ============================ 跟随 ACE ============================ */

// aceWindow 找"要贴的那个 ACE 窗口"：pid 属于 ACE、可见、没最小化、尺寸够大（弹出小窗不要）。
//
// ⚠️ 判据用**尺寸**不是"有没有属主"：真机实测钢琴窗（1593x569）**是有属主的**，
// 而它是必须贴的窗口之一；那三个 96x56 / 152x48 / 184x80 的小窗才是弹出窗。
// ACERunning 判 **ACE 的进程在不在**（不看窗口是否最小化 —— 侧栏挂上去要等窗口出现，
// 由 follow 定时器负责）。用户 2026-10-20：启动后静默在托盘，等 ACE 起来再起侧栏。
func ACERunning() bool {
	pids, err := win.ByExeName("ACE Studio.exe")
	return err == nil && len(pids) > 0
}

func (r *Rail) aceWindow() (win.Window, bool) {
	now := time.Now()
	if now.Sub(r.acePIDAt) > 2*time.Second || len(r.acePIDs) == 0 {
		if pids, err := win.ByExeName("ACE Studio.exe"); err == nil {
			r.acePIDs = pids
		}
		r.acePIDAt = now
	}
	if len(r.acePIDs) == 0 {
		return win.Window{}, false
	}
	all, err := win.List()
	if err != nil {
		r.setErr(err)
		return win.Window{}, false
	}
	var cand []win.Window
	for _, w := range all {
		if !containsPID(r.acePIDs, w.PID) || win.Minimized(w.Handle) {
			continue
		}
		rc, err := win.RectOf(w.Handle)
		if err != nil || rc.W() < 600 || rc.H() < 400 {
			continue
		}
		cand = append(cand, w)
	}
	if len(cand) == 0 {
		return win.Window{}, false
	}
	fg := win.Foreground()
	for _, w := range cand {
		if w.Handle == fg {
			return w, true // 焦点是哪个就贴哪个（用户原话）
		}
	}
	// 前台不是 ACE（人在浏览器/编辑器里）⇒ 保留上次那个；它没了就取第一个（枚举序里最靠前的那个大的）
	if r.targetSet {
		for _, w := range cand {
			if w.Handle == r.target.Handle {
				return w, true
			}
		}
	}
	return cand[0], true
}

func containsPID(pids []uint32, pid uint32) bool {
	for _, p := range pids {
		if p == pid {
			return true
		}
	}
	return false
}

// follow 跟一次：定位、贴边、必要时显隐。
func (r *Rail) follow() {
	w, ok := r.aceWindow()
	if !ok {
		r.targetSet = false
		r.hide() // ACE 不在了 ⇒ 藏起来（ACE 关掉时窗口还会被系统连坐销毁，见 tick 里的重建）
		return
	}
	// 挂成 ACE 那个窗口的**子窗口**（换目标时重挂）。挂不上就什么都不画：
	// **宁可看不见，也不要退回"顶层窗口"去跟 ACE 抢层级**（那正是前四轮翻车的地方）。
	if r.parentHWND != w.Handle {
		if err := r.attach(w.Handle); err != nil {
			r.setErr(err)
			r.hide()
			return
		}
	}
	cli, cerr := win.ClientRectOf(w.Handle)
	if cerr != nil {
		r.hide()
		return
	}
	out, err := win.RectOf(w.Handle)
	if err != nil {
		r.setErr(err)
		return
	}
	_, scale := dpiScale()
	pad := int32(float64(r.opts.Pad) * scale)
	r.mu.Lock()
	level := r.state.Level
	r.mu.Unlock()

	// ⛔ 五层教训叠在这一段上，改之前先读完（2026-10-16 三轮 + 2026-10-17 两轮）：
	//  ① 用 `WS_EX_TOPMOST` **常驻**置顶 ⇒ 用户报「**它会显示在其他应用上**」（切到浏览器它还挂着）——❌
	//  ② 改"认 ACE 当属主 + 换属主时升 Z 序" ⇒ 用户报「**整个钢琴窗消失又出现**」：
	//     **把自己的属主一起提到最前是 Windows 的既定行为**，而属主在"主窗 ⇄ 钢琴窗"之间换
	//     ⇒ 它俩被轮流提到前面、全屏主窗一上来就把钢琴窗盖住（`screendiff -watch` 抓到 60 万~113 万
	//     像素的整窗重绘、2–4Hz）——❌
	//  ③ 把 Z 序整个拿掉 ⇒ 用户报「**现在显示在钢琴窗的下面了**」（不抢层级就被它重绘时盖住）——❌
	//  ④ 改"条件置顶"（只在 ACE 是**前台**时 topmost）⇒ 用户 2026-10-17 报「**关闭 web 面板后侧栏会消失**」：
	//     焦点一旦不在 ACE 上就退回普通层，**被 ACE 自己盖住**。——❌
	//     ⚠️ 这一轮最坑的是**判据**：`/api/rail` 一直报 `visible=true`（我们确实没 `ShowWindow(SW_HIDE)`），
	//     只有 `devtools/railprobe`（问"那个点上真正是谁"）才量出真相 —— 真机回的是别的应用/ACE。
	//     **"没藏起来" ≠ "看得见"**。
	//  ⑤ 改"插到紧贴 ACE 之上"（`SetWindowPos(hWndInsertAfter=ACE窗口)`）⇒ 真机量到**回成功、层级不动**：
	//     ACE 那几个窗口是 **owned** 的（钢琴窗的属主是主窗），Windows 不让外进程窗口插进那个 owned 组
	//     （`devtools/railtry -mode insert-*` 把这三种插法都试过，全不动）；
	//     而"认属主"那条虽然层级对了，**却把 ACE 整个抬到别的应用前面**（前台直接变成 ACE 主窗）——❌❌
	// ⇒ **正解（当前做法，第五轮）：把细条 `SetParent` 成 ACE 那个窗口的子窗口。**
	//    子窗口的两条性质正好就是用户那两条口径，而且是**系统保证**、不靠我们抢：
	//      · 子窗口**永远画在父窗口客户区之上** ⇒ 不可能被 ACE 自己盖住（③④的病根）；
	//      · 子窗口**根本不是顶层窗口** ⇒ 别的应用盖住 ACE 时它跟着被盖住，**不可能浮在别的应用上面**（①的病根）；
	//      · 全程**零 Z 序操作** ⇒ 没有拉锯、不会把 ACE 的窗口一起提起来（②的病根）。
	//    代价（明知故犯，都写在这儿）：① 坐标变成"父窗口客户区"坐标（位置都得按客户区算）；
	//      ② **ACE 一关它就被系统连坐销毁** ⇒ 得会重建（`tick` 里那句 `makeWindows`）。
	cw, ch := cli.W(), cli.H() // 父客户区尺寸 = 子窗口坐标系的整个画布
	width := int32(float64(levelWidth(level)) * scale)
	if width > cw {
		width = cw
	}
	// 让开 ACE **自绘标题栏**（关闭按钮画在客户区里，真机实测窗口与客户区顶差 = 0）
	top := int32(float64(topInset) * scale)
	if top < pad {
		top = pad
	}
	h := ch - pad - top
	if h < 60 {
		h = 60
	}
	x := cw - width // 贴父客户区**右缘**（子窗口不许超出客户区，所以这里只能贴客户区，不是窗口外缘）
	y := top
	railClient := rect{Left: x, Top: y, Right: x + width, Bottom: y + h}

	pSetWindowPos.Call(r.hwnd, 0, uintptr(x), uintptr(y), uintptr(width), uintptr(h),
		uintptr(swpNoActivate|swpShowWindow|swpNoZOrder|swpFrameChanged))
	// ⚠️ **每轮都重画**（不是"变了才重画"）：ACE 那个窗口不一定带 `WS_CLIPCHILDREN`
	// ⇒ 它自己重绘时**可能把我们这块一起刷掉**（子窗口只在父窗口之后画，父窗口不裁子窗口就会盖住我们）。
	// 我们的窗口只有几个像素到几十像素宽，8Hz 重画一遍的代价可以忽略；
	// 换来的是"ACE 怎么重绘，细条都不会被它刷没"（那就又变成用户报的"侧栏消失"了）。
	pInvalidateRect.Call(r.hwnd, 0, 1)
	r.lastClient = railClient
	// 屏幕矩形**读回来**（三级判定、悬停行、栏外小窗的位置全用屏幕坐标 ⇒ 不能让状态里是客户区坐标）
	screen, serr := win.RectOf(r.hwnd)
	if serr != nil {
		screen = win.Rect{Left: cli.Left + x, Top: cli.Top + y, Right: cli.Left + x + width, Bottom: cli.Top + y + h}
	}
	// 诊断：那个点上**真正最上面的窗口**是谁（`hitSelf` = 此刻我们自己看得见）。
	at := win.WindowAt((screen.Left+screen.Right)/2, (screen.Top+screen.Bottom)/2)
	hitPID, hitClass, hitSelf := uint32(0), "", false
	if at != 0 {
		hitPID, hitClass = win.PIDOf(at), win.ClassOf(at)
		hitSelf = at == r.hwnd || at == r.tipHWND || hitPID == uint32(os.Getpid())
	}
	ex, _, _ := pGetWindowLongPtrW.Call(r.hwnd, ^uintptr(19) /* GWL_EXSTYLE = -20 */)
	r.mu.Lock()
	r.railClient = railClient
	r.state.TargetTitle = w.Title
	r.state.TargetHWND = w.Handle
	r.state.TargetPID = w.PID
	r.state.Visible = true
	r.state.Rect = screen
	r.state.TargetClient = cli
	r.state.TargetRect = out
	r.state.AceForeground = win.Foreground() == w.Handle
	r.state.TopMost = ex&wsExTop != 0
	r.state.HitPID, r.state.HitClass, r.state.HitSelf = hitPID, hitClass, hitSelf
	r.lastRect = screen
	r.mu.Unlock()
}

// hide 把细条（和栏外小窗）藏起来 + 把"贴着谁"清掉。
func (r *Rail) hide() {
	if r.hwnd != 0 {
		pShowWindow.Call(r.hwnd, swHide)
	}
	if r.tipHWND != 0 {
		pShowWindow.Call(r.tipHWND, swHide) // 全名小窗跟着藏（ACE 关了就一起走）
	}
	if r.paramItem >= 0 { // 参数条是**独立的小窗**，细条一藏就得一起收（不然它会孤零零留在屏幕上）
		r.closePanel()
	}
	r.mu.Lock()
	r.state.Visible = false
	r.state.TargetTitle = ""
	r.state.TipText, r.state.TipRect = "", win.Rect{}
	r.state.HitPID, r.state.HitClass, r.state.HitSelf = 0, "", false
	r.mu.Unlock()
}

// attach 把细条（和栏外小窗）挂成 `parent` 的**子窗口** —— 第五轮的正解，理由见 follow 里那段五层教训。
//
// ⚠️ 三个坑（都踩过或明确知道）：
//
//	① `SetParent` **不会**自己改样式 ⇒ 必须手动把 `WS_POPUP` 换成 `WS_CHILD`，否则它仍按顶层窗口对待；
//	② 换了父窗口，坐标语义立刻从"屏幕坐标"变成"父窗口客户区坐标" ⇒ **先藏起来、算好新坐标再显示**，
//	   否则会看到它跳一下（`follow` 紧接着那一段就是按客户区坐标摆位置）；
//	③ 它是子窗口了 ⇒ **ACE 一关，系统把我们一起销毁**（`wndProc` 的 WM_DESTROY 里认这个情形，见 tick）。
func (r *Rail) attach(parent uintptr) error {
	if !win.Alive(parent) {
		return fmt.Errorf("要挂上去的窗口已失效（0x%x）", parent)
	}
	pShowWindow.Call(r.hwnd, swHide)
	if r.tipHWND != 0 {
		pShowWindow.Call(r.tipHWND, swHide)
	}
	for _, h := range []uintptr{r.hwnd, r.tipHWND} {
		if h == 0 {
			continue
		}
		// ⚠️ 判据只看"**老样式是不是 0**"：`syscall` 的第三个返回值是**上一个错误**，
		// 它可能早就存在（不是这次调用产生的）⇒ 拿它非 0 当失败会误报（真机就误报过一次
		// 「The handle is invalid」，而窗口其实挂上去了）。老样式里一定有 WS_POPUP（0x80000000）⇒ 不会是 0。
		style, _, _ := pGetWindowLongPtrW.Call(h, gwlStyle)
		if style == 0 {
			return fmt.Errorf("读不到窗口样式（0x%x，句柄失效了？）", h)
		}
		if old, _, _ := pSetWindowLongPtrW.Call(h, gwlStyle,
			(style&^uintptr(wsPopup))|uintptr(wsChild)); old == 0 {
			return fmt.Errorf("SetWindowLongPtr(换 WS_CHILD) 失败（0x%x：老样式读回来是 0）", h)
		}
	}
	pSetParent.Call(r.hwnd, parent)
	if r.tipHWND != 0 {
		pSetParent.Call(r.tipHWND, parent)
	}
	// ⭐ 挂上没有**量**出来才算数：`SetParent` 的返回值是**老父窗口**，失败时也可能回 0 不报错。
	if got := win.ParentOf(r.hwnd); got != parent {
		return fmt.Errorf("SetParent 没生效：父窗口读回来是 0x%x，期望 0x%x", got, parent)
	}
	r.parentHWND = parent
	r.mu.Lock()
	r.state.ParentHWND = parent
	r.mu.Unlock()
	return nil
}

// levelWidth 三级各自的宽度（逻辑像素）。
//
// ⚠️ 第③级**不加宽**：全名是**栏外**那个小窗弹的（用户 2026-10-16：「能不能让它在栏外弹出全名」），
// 细条本身只长到"两个字"那么宽 —— 这样字永远不跟着动，也不会糊住 ACE 的内容。
func levelWidth(level int) int {
	switch level {
	case 2, 1:
		return levelShortW
	default:
		return levelThinW
	}
}

// updateLevel 看**光标在哪**决定停在哪一级（用户 2026-10-16 的三级口径）。
//
// 为什么用**轮询光标**而不是鼠标消息：① 极细那一级只有 8px 宽，光标几乎不可能正好落在上面，
// "靠近"根本收不到 `WM_MOUSEMOVE`；② 窗口随级别变宽，靠消息判断"离得多近"会被自己的尺寸绕进去。
// `GetCursorPos` + 120ms 定时器简单、稳，而且不引任何全局钩子。
func (r *Rail) updateLevel() {
	r.mu.Lock()
	rc := r.state.Rect
	visible := r.state.Visible
	oldLevel, oldHover := r.state.Level, r.hover
	r.mu.Unlock()
	if !visible {
		return
	}
	var p point
	if fc := r.fakeCursor; fc != nil {
		p = *fc // 自检注入（见 SetCursorOverride）：不抢用户的鼠标也能验三级
	} else {
		pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	}
	_, scale := dpiScale()
	inY := p.Y >= rc.Top && p.Y <= rc.Bottom
	dist := rc.Right - p.X // 离右缘（锚点）多远
	// ⚠️ 缩放必须**先乘再转**：`int32(scale)` 会把 1.5 截成 1（第一版就是这么错的 ⇒
	// "靠近"区被算成 52px 而不是 78px，人都贴到字上了它也不亮）。
	nearW := int32(float64(nearPx) * scale)
	shortW := int32(float64(levelShortW) * scale)

	level, hover := 0, -1
	if inY {
		switch {
		case dist <= nearW && dist >= -nearW:
			// 靠近了：显示两个字；若光标已经压在**字**那一列上 ⇒ 停在这一行、把全名弹到**栏外**
			level = 1
			if dist <= shortW && dist >= 0 {
				level = 2
				// ⚠️ 光标是**屏幕坐标**，`itemAt` 要的是**窗口内坐标** ⇒ 必须减掉窗口顶边。
				// 第一版拿屏幕 y 直接判行 ⇒ 认到的行整个偏掉（用户 2026-10-16：「鼠标位置有偏移」）。
				hover = r.itemAt(p.Y - rc.Top)
			}
		}
	}
	r.mu.Lock()
	r.state.CursorX, r.state.CursorY = int(p.X), int(p.Y)
	r.state.Ticks++
	r.mu.Unlock()
	if level == oldLevel && hover == oldHover {
		r.syncTip(level, hover) // 级别没变也要跟一下（窗口可能刚被挪过）
		return
	}
	r.mu.Lock()
	r.state.Level = level
	r.state.Hover = hover // ⚠️ 两份都要写：`r.hover` 给画用、`state.Hover` 给 `/api/rail` 报（漏过一次）
	r.hover = hover
	// ⛔ **不要在这里清 `lastRect`**（原来为了"逼着重摆"清过）。宽度是按 `level` 每轮现算的 ⇒
	//    `follow()` 自己就会看到"想要的和上次不一样"并重摆；再强制清一次只是**多一次无谓的
	//    SetWindowPos**，光标停在"靠近/不靠近"边界上时会变成每 120ms 抖一次（用户看到的就是"窗口一直在闪"）。
	r.mu.Unlock()
	r.follow()
	r.syncTip(level, hover)
	pInvalidateRect.Call(r.hwnd, 0, 0)
}

/* ============================ 画 ============================ */

// layout 算每格的高度与字号：**自适应 ACE 窗口的高度**（19 项要在一个窗口里排完）。
func (r *Rail) layout(hdc uintptr, h int32) (itemH int32, fontLog, fontFull int, scale float64) {
	_, scale = dpiScale()
	n := int32(len(r.items))
	if n == 0 {
		return 0, 12, 11, scale
	}
	pad := int32(float64(r.opts.Pad) * scale)
	itemH = (h - 2*pad) / n
	if itemH < int32(float64(minItemH)*scale) {
		itemH = int32(float64(minItemH) * scale)
	}
	fontLog = int(float64(itemH) * 0.5)
	if fontLog < 9 {
		fontLog = 9
	}
	maxFont := (levelShortW - 10) // 两个字那一列（第②级）要塞得下 ⇒ 字号上限跟着它走
	if fontLog > maxFont {
		fontLog = maxFont
	}
	fontFull = fontLog - 1
	if fontFull < 8 {
		fontFull = 8
	}
	r.mu.Lock()
	r.state.ItemH = int(itemH)
	r.mu.Unlock()
	return
}

func (r *Rail) paint() {
	var ps paintStruct
	hdc, _, _ := pBeginPaint.Call(r.hwnd, uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	defer pEndPaint.Call(r.hwnd, uintptr(unsafe.Pointer(&ps)))

	var cr rect
	pGetClientRect.Call(r.hwnd, uintptr(unsafe.Pointer(&cr)))
	// 底 + 左边一条描边（细条贴右边 ⇒ 描边画在**靠 ACE 那一侧**：左边）
	bg, _, _ := pCreateSolidBrush.Call(colorBG)
	pFillRect.Call(hdc, uintptr(unsafe.Pointer(&cr)), bg)
	pDeleteObject.Call(bg)
	line, _, _ := pCreateSolidBrush.Call(colorBorder)
	edge := rect{cr.Left, cr.Top, cr.Left + 1, cr.Bottom}
	pFillRect.Call(hdc, uintptr(unsafe.Pointer(&edge)), line)
	pDeleteObject.Call(line)

	itemH, fontLog, fontFull, scale := r.layout(hdc, cr.Bottom)
	if itemH == 0 {
		return
	}
	pad := int32(float64(r.opts.Pad) * scale)

	r.mu.Lock()
	level := r.state.Level
	hover := r.hover
	note := r.state.Note
	noteFresh := !r.noteAt.IsZero() && time.Since(r.noteAt) < 4*time.Second
	r.mu.Unlock()

	// 第③级里"字"的那一列：右对齐、离右缘固定距离 ⇒ **细条往左长的时候字不动**（不然字会跟着飘）
	shortW := int32(float64(levelShortW-8) * scale)
	shortX := cr.Right - shortW - int32(float64(4)*scale)

	pSetBkMode.Call(hdc, transparentBkMode)
	for i, it := range r.items {
		top := pad + int32(i)*itemH
		if top+itemH > cr.Bottom {
			break // 窗口太矮，尾巴裁掉（`/api/rail` 里能看出 itemH 被压到多少）
		}
		ir := rect{cr.Left + 1, top, cr.Right, top + itemH}
		if level == 2 && i == hover {
			h, _, _ := pCreateSolidBrush.Call(colorHover)
			pFillRect.Call(hdc, uintptr(unsafe.Pointer(&ir)), h)
			pDeleteObject.Call(h)
		}
		// 左起那条**颜色码**：绿=侧栏里就地完成 / 琥珀=要参数(塞不下) / 蓝=开窗口 / 红=改工程文件
		// （用户 2026-10-16 定前三色，2026-10-17 补第四色）
		code := colorRun
		switch it.Kind {
		case KindParams:
			code = colorParams
		case KindPage:
			code = colorPage
		case KindFile:
			code = colorFile // 改工程文件（红）：跟"开个窗口点点看"是两回事，颜色必须分开
		}
		bar, _, _ := pCreateSolidBrush.Call(code)
		bw := int32(float64(3) * scale)
		br := rect{cr.Left + 1, top + 2, cr.Left + 1 + bw, top + itemH - 2}
		pFillRect.Call(hdc, uintptr(unsafe.Pointer(&br)), bar)
		pDeleteObject.Call(bar)

		if level == 0 {
			continue // ① 特别细：只有那条颜色码，一个字都不画
		}
		if level == 1 {
			// ② 靠近：只显示 1–2 个汉字（居中）
			f := makeFont(fontLog, fwSemiBold)
			old, _, _ := pSelectObject.Call(hdc, f)
			pSetTextColor.Call(hdc, colorText)
			r.text(hdc, it.Short, rect{cr.Left + 1, top, cr.Right, top + itemH},
				dtCenter|dtVCenter|dtSingleLine|dtNoPrefix)
			pSelectObject.Call(hdc, old)
			pDeleteObject.Call(f)
			continue
		}
		// ③ 悬停到字上：字**右对齐不动**；只有光标那一行，在**字前**（左边）显示全名
		fLog := makeFont(fontLog, fwSemiBold)
		old, _, _ := pSelectObject.Call(hdc, fLog)
		pSetTextColor.Call(hdc, colorText)
		r.text(hdc, it.Short, rect{shortX, top, shortX + shortW, top + itemH},
			dtLeft|dtVCenter|dtSingleLine|dtNoPrefix)
		pSelectObject.Call(hdc, old)
		pDeleteObject.Call(fLog)

		if i == hover {
			fFull := makeFont(fontFull, fwNormal)
			old2, _, _ := pSelectObject.Call(hdc, fFull)
			pSetTextColor.Call(hdc, colorDim)
			r.text(hdc, it.Full, rect{cr.Left + int32(float64(6)*scale), top, shortX - int32(float64(4)*scale), top + itemH},
				dtRight|dtVCenter|dtSingleLine|dtNoPrefix|dtEndEllipsis)
			pSelectObject.Call(hdc, old2)
			pDeleteObject.Call(fFull)
		}
	}
	// 最近一条提示（点击结果）铺在底部 —— 细条没有别的地方能说话
	if noteFresh && note != "" {
		nb, _, _ := pCreateSolidBrush.Call(colorBG)
		nr := rect{cr.Left + 1, cr.Bottom - int32(float64(18)*scale), cr.Right, cr.Bottom}
		pFillRect.Call(hdc, uintptr(unsafe.Pointer(&nr)), nb)
		pDeleteObject.Call(nb)
		f := makeFont(9, fwNormal)
		old, _, _ := pSelectObject.Call(hdc, f)
		pSetTextColor.Call(hdc, colorText)
		r.text(hdc, note, nr, dtCenter|dtVCenter|dtSingleLine|dtNoPrefix|dtEndEllipsis)
		pSelectObject.Call(hdc, old)
		pDeleteObject.Call(f)
	}
}

func (r *Rail) text(hdc uintptr, s string, rc rect, flags uint32) {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return
	}
	ret, _, _ := pDrawTextW.Call(hdc, uintptr(unsafe.Pointer(p)), ^uintptr(0), uintptr(unsafe.Pointer(&rc)), uintptr(flags))
	// DrawTextW 返回**画出来的高度**；0 = 一个字都没画（字体没选中 / 矩形不对）。
	// 这是"汉字到底画上没有"唯一能问出来的信号 ⇒ 报进 `/api/rail`（自检靠它）。
	r.mu.Lock()
	r.state.LastDraw = int32(ret)
	r.state.LastText = s
	r.mu.Unlock()
}

// makeFont 造一个微软雅黑字体（**汉字靠 GDI 画**，这就是"不引 WebView2"的底气）。
func makeFont(logPx int, weight int) uintptr {
	name, _ := syscall.UTF16PtrFromString("Microsoft YaHei")
	f, _, _ := pCreateFontW.Call(
		uintptr(int32(-logPx)), 0, 0, 0, uintptr(weight), 0, 0, 0,
		134, // GB2312_CHARSET
		0, 0, 5 /*CLEARTYPE_QUALITY*/, 0, uintptr(unsafe.Pointer(name)))
	theRail.mu.Lock()
	theRail.state.FontOK = f != 0
	theRail.state.FontPx = logPx
	theRail.mu.Unlock()
	return f
}

/* ============================ 交互 ============================ */

func (r *Rail) itemAt(y int32) int {
	var cr rect
	pGetClientRect.Call(r.hwnd, uintptr(unsafe.Pointer(&cr)))
	_, scale := dpiScale()
	pad := int32(float64(r.opts.Pad) * scale)
	r.mu.Lock()
	itemH := int32(r.state.ItemH)
	r.mu.Unlock()
	if itemH == 0 {
		return -1
	}
	i := int((y - pad) / itemH)
	if y < pad || i < 0 || i >= len(r.items) {
		return -1
	}
	return i
}

// ⛔ 这里**曾经**有个 `giveFocusBack`：开完浏览器窗口后隔着 200/500/1000ms 把前台抢回 ACE。
//
// 那是照用户 2026-10-16 报的「点过细条上的某一项，整个钢琴窗消失又出现」写的，**但根因判断错了**：
// 那次真正的毛病是**我们在抢 Z 序**（抬一个 owned 窗口会连带抬高它的 owner，主窗/钢琴窗轮流压对方
// ⇒ 每秒几次的闪烁，`screendiff -watch` 量到 60 万–113 万像素事件）。Z 序那条修完（**不再抬**）之后，
// 抢前台就成了纯粹的副作用：ACE 主窗是全屏的，**把前台还给 ACE = 把刚开的面板窗口盖到后面** ⇒
// 用户看到的就是「web 闪一下就没了」（2026-10-17 报）。⇒ 整条删掉：用户点"开窗口"就是要看那个窗口。

// rbutton 是右键那一下：**只用来改附属参数**（左键已经是"直接就干"了）。
//
// 用户 2026-10-17：「连接这个，我想做成点按钮就用，参数只是附带」⇒ 参数退到右键，
// 但**必须在某处说出来**（悬停全名里带「右键改」，见 `itemTip`），否则用户根本不知道有这一层。
func (r *Rail) rbutton(i int) {
	if i < 0 || i >= len(r.items) {
		return
	}
	it := r.items[i]
	if it.Panel == nil {
		r.note(it.Full + "：这一项没有可调参数")
		return
	}
	// 跟左键那套一样：建窗口/拿焦点认线程 ⇒ 只登记请求，`tick()` 去做开/收
	r.mu.Lock()
	r.wantPanel = i
	r.mu.Unlock()
}

func (r *Rail) click(i int) {
	if i < 0 || i >= len(r.items) {
		return
	}
	// ⛔ 第①级（极细）**不接受点击**：那一条只有几个像素宽，误点一下就真跑工具太危险。
	// 用户口径是"靠近 → 显示两个字 → hover 到字上" ⇒ 点得着的时候一定已经在第②③级了。
	r.mu.Lock()
	level := r.state.Level
	// 去抖：同一颗按钮 400ms 内只认一次。真机踩过"点了一下工具却像被连点"的情况，
	// 去抖能把"手抖/消息重复"这类重复触发挡掉（工具是**真写**，重复跑代价很大）。
	if since := time.Since(r.lastClick); since < 400*time.Millisecond {
		r.mu.Unlock()
		return
	}
	r.lastClick = time.Now()
	r.mu.Unlock()
	if level == 0 {
		r.note("先靠近一点（等它显示两个字）再点")
		return
	}
	it := r.items[i]
	// 左键的三条分支（用户 2026-10-17 定）：
	//  ① `Panel.NeedsInput`（音素：输入必填）⇒ 打开参数条（它也是**绿**的：参数就在侧栏里填，
	//     不用去别处拿 —— 但没填「查找」就无从跑起）；
	//  ② `KindParams` 且没参数条 ⇒ 开主面板去填（现在清单里没有这种，留着兜）；
	//  ③ 其余 `Kind=run`（连接音符带档位 / 普通工具）⇒ **直接跑**（档位用记着的值）。
	if it.Panel != nil && (it.Panel.NeedsInput || it.Kind == KindParams) {
		// 建窗口/拿键盘焦点都认线程，所以这里只登记请求，真正的开/收由 `tick()` 在消息循环线程上做。
		r.mu.Lock()
		r.wantPanel = i
		r.mu.Unlock()
		return
	}
	// 附属参数：`Kind=run` 的项如果有档位参数，把**记着的那个值**带上去跑（没记过就用第 0 档）。
	values := it.Values
	if it.Panel != nil && it.Panel.Kind == ParamTier {
		vals := map[string]string{}
		for k, v := range it.Values {
			vals[k] = v
		}
		vals[it.Panel.Arg] = r.tierValueOf(it)
		values = vals
	}
	go func() {
		switch it.Action() {
		case ActClipCopy:
			r.clipCopy(it)
		case ActClipPaste:
			r.clipPaste(it)
		case ActExpandWeb:
			r.expandToWeb()
		default:
			switch it.Kind {
			case KindPage, KindFile: // 红的"改工程文件"那一组也是**开页面**（页面里有保存/执行/恢复那三步）
				how, oerr := openPage(r.opts.BaseURL + it.Page)
				if oerr != nil {
					r.note(it.Full + "：打不开页面 —— " + oerr.Error())
					return
				}
				r.note(it.Full + "：" + how)
			case KindParams:
				// 参数值在页面里（阈值档 / 输入框），exe 不知道你上次设的是什么 ⇒ **不替你挑**，开主面板去填
				how, oerr := openPage(r.opts.BaseURL + "home.html")
				if oerr != nil {
					r.note(it.Full + "：打不开主面板 —— " + oerr.Error())
					return
				}
				r.note(it.Full + "：要参数 ⇒ 已开主面板（" + how + "）")
			default:
				resp, err := r.callTool(it.Tool, values)
				if err != nil {
					r.note(it.Full + "：失败 —— " + err.Error())
					return
				}
				if !resp.OK {
					r.note(it.Full + "：失败 —— " + resp.reason())
					return
				}
				// 带档位的项把"用的是哪一档"说清楚（不然同一个点击在不同档位下结果不同，用户会以为坏了）
				tail := ""
				if it.Panel != nil && it.Panel.Kind == ParamTier {
					tail = "（阈值 " + r.tierNameOf(it) + "）"
				}
				r.note(it.Full + "：" + resp.msg() + tail)
			}
		}
	}()
}

// clipCopy = 细条上的「复制歌词」：先跑只读工具 `lyric.copy` 拿文本，再把文本写进**系统剪贴板**。
//
// 面板里这一步是网页干的（`navigator.clipboard.writeText`），细条是原生窗口、**没有网页** ⇒
// exe 自己写（`internal/win/clipboard_windows.go`）。两边的文本来源完全一样（同一个工具）。
func (r *Rail) clipCopy(it Item) {
	resp, err := r.callTool(it.Tool, it.Values)
	if err != nil {
		r.note(it.Full + "：失败 —— " + err.Error())
		return
	}
	if !resp.OK {
		r.note(it.Full + "：失败 —— " + resp.reason())
		return
	}
	text := resp.firstText()
	n := runeCountNoSpace(text)
	if n == 0 {
		r.note(it.Full + "：没取到歌词（选中的音符是空的？没选中时按整个片段找）")
		return
	}
	if err := win.SetClipboardText(text); err != nil {
		r.note(it.Full + "：歌词取到了，但**写剪贴板失败** —— " + err.Error())
		return
	}
	r.note(fmt.Sprintf("%s：已复制 %d 个字到剪贴板", it.Full, n))
}

// clipPaste = 细条上的「粘贴歌词」：先读**系统剪贴板**，再把文本当参数跑 `lyric.paste`
// （分割规则、要选中音符这些判据全在那个工具里，细条这边不重复一遍）。
func (r *Rail) clipPaste(it Item) {
	text, err := win.ClipboardText()
	if err != nil {
		r.note(it.Full + "：读剪贴板失败 —— " + err.Error())
		return
	}
	if runeCountNoSpace(text) == 0 {
		r.note(it.Full + "：剪贴板里没有文字（先复制一段歌词或文本）")
		return
	}
	vals := map[string]string{}
	for k, v := range it.Values {
		vals[k] = v
	}
	vals["text"] = text
	resp, err := r.callTool(it.Tool, vals)
	if err != nil {
		r.note(it.Full + "：失败 —— " + err.Error())
		return
	}
	if !resp.OK {
		r.note(it.Full + "：失败 —— " + resp.reason())
		return
	}
	r.note(it.Full + "：" + resp.msg())
}

// expandToWeb = 细条上的「展开为 web 页」：**跟面板顶栏那颗「侧」按钮互为镜像**。
//
// 顺序是刻意的：**先开 web 面板窗口，再停掉细条** —— 反过来的话细条先没了、窗口还没出来，
// 用户看到的就是"点了什么也没发生"。
// 两步都走 exe 自己的 API（`/api/app-window` / `/api/rail`），复用面板那条路的全部护栏：
// app 窗口的"只关自己开的那个"、侧栏起停的状态记录。
func (r *Rail) expandToWeb() {
	if err := r.postAPI("/api/app-window", map[string]any{"page": "home.html"}); err != nil {
		r.note("展开为 web 页：开窗口失败 —— " + err.Error())
		return
	}
	r.note("展开为 web 页：已开面板窗口，侧栏收起")
	time.Sleep(350 * time.Millisecond) // 让这句提示先画出来（不然窗口一出来细条就没了，用户看不到）
	if err := r.postAPI("/api/rail", map[string]any{"enable": false}); err != nil {
		r.note("展开为 web 页：窗口开了，但侧栏没停掉 —— " + err.Error())
	}
}

// postAPI 往 exe 自己的 API POST 一个 JSON（rail 自己那几个动作共用）。
func (r *Rail) postAPI(path string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", strings.TrimRight(r.opts.BaseURL, "/")+path, strings.NewReader(string(b)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ACETool", "1")
	cl := &http.Client{Timeout: 30 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if !out.OK {
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// runeCountNoSpace 数**非空白字符**的个数（"有几个字"）—— 中文一字一个、英文一个字母一个，
// 报给用户的就是这个数（剪贴板里是空格拼起来的串，数空格进去会虚高）。
func runeCountNoSpace(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}

// toolResp 是 `/api/run` 回包里细条用得上的那几样（形状照 api.go 的 runResponse）。
type toolResp struct {
	OK    bool   `json:"ok"`
	Hint  string `json:"hint"`
	Calls []struct {
		ExitCode int             `json:"exitCode"`
		JSON     json.RawMessage `json:"json"`
	} `json:"calls"`
	Undo *struct {
		Created int `json:"created"`
	} `json:"undo"`
}

// reason 是失败时给用户看的那一句话。
func (o *toolResp) reason() string {
	if o.Hint != "" {
		return o.Hint
	}
	return "工具返回 ok=false（看面板命令日志）"
}

// msg 是成功那一路的一行小结（跑了几条命令 + 能撤回几条）。
func (o *toolResp) msg() string {
	msg := fmt.Sprintf("已跑（%d 条命令）", len(o.Calls))
	if o.Undo != nil && o.Undo.Created > 0 {
		msg += fmt.Sprintf(" · 可撤回 %d 条", o.Undo.Created)
	}
	return msg
}

// firstText 从**第一条**调用的 JSON 里取 `text`（读工具的结果就是这个形状，见
// `internal/tools/noteops.go` 的 `lyric.copy`）。取不到就是空串（调用方去说"没取到歌词"）。
func (o *toolResp) firstText() string {
	for _, c := range o.Calls {
		if len(c.JSON) == 0 {
			continue
		}
		var v struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(c.JSON, &v); err == nil && v.Text != "" {
			return v.Text
		}
	}
	return ""
}

// callTool 走 exe 自己的 `/api/run`（复用面板那条路的全部护栏：撤回游标、事件留痕、CLI 串行）。
func (r *Rail) callTool(tool string, values map[string]string) (*toolResp, error) {
	if values == nil {
		values = map[string]string{}
	}
	body, _ := json.Marshal(map[string]any{"toolId": tool, "values": values})
	req, err := http.NewRequest("POST", strings.TrimRight(r.opts.BaseURL, "/")+"/api/run", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ACETool", "1")
	cl := &http.Client{Timeout: 120 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out toolResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// openPage 打开面板页 —— **浏览器 app 模式**（没有标签栏/地址栏），尺寸按页面自己的表。
//
// ⭐ 2026-10-17 用户：「把所有打开的面板隐藏地址栏，把窗口做小」⇒ 这条路原来用
// `--new-window`（**普通浏览器窗口，带地址栏**、还按浏览器记忆的尺寸开）—— 换掉：
//
//	· 走 `win.AppWindow`（`--app=`）⇒ 没有地址栏，看起来就是个独立小窗；
//	· 尺寸取 `web.PanelSize(page)`（逻辑像素；实测 `--window-size` 就是 DIP，150% 屏上会自动 ×1.5）；
//	· 位置不传（`-1`）⇒ 让 Chromium 自己记这个 app 窗口的位置（用户挪过一次就一直在那儿）。
//
// 返回一句"怎么开的"，给细条上那句提示用（**如实说**：浏览器不认 `--app` 时会退回普通窗口）。
func openPage(url string) (how string, err error) {
	page := url
	if i := strings.LastIndex(url, "/"); i >= 0 {
		page = url[i+1:]
	}
	w, h := web.PanelSize(page)
	exe, appMode, sized, aerr := win.AppWindow(url, -1, -1, w, h)
	if aerr == nil {
		if !appMode {
			return fmt.Sprintf("已开窗口（%s 不认 --app ⇒ 是普通窗口，带地址栏）", exe), nil
		}
		if sized {
			return fmt.Sprintf("已开小窗（%d×%d，无地址栏）", w, h), nil
		}
		// 窗口出来了但没赶上摆尺寸（Edge 记住了上次的尺寸，或者被别的窗口抢先）
		return "已开小窗（无地址栏）—— 没赶上定尺寸，浏览器按它自己记的开了", nil
	}
	// 退路：老路子（普通窗口，地址栏在）—— 但要说清，不然用户以为"隐藏地址栏"没生效
	if berr := openPageLegacy(url); berr != nil {
		return "", fmt.Errorf("%v（退回普通窗口也失败：%v）", aerr, berr)
	}
	return "已开窗口（app 模式不可用 ⇒ 普通窗口，带地址栏）", nil
}

// openPageLegacy 是换掉之前的做法（普通浏览器新窗口，**有地址栏**），只当 app 模式不可用时兜底。
func openPageLegacy(url string) error {
	exe, err := win.DefaultBrowserExe()
	if err == nil && exe != "" {
		flag := "--new-window"
		if strings.Contains(strings.ToLower(exe), "firefox") {
			flag = "-new-window"
		}
		if err := win.RunDetached(exe, flag, url); err == nil {
			return nil
		}
	}
	u, _ := syscall.UTF16PtrFromString(url)
	op, _ := syscall.UTF16PtrFromString("open")
	ret, _, e := pShellExecuteW.Call(0, uintptr(unsafe.Pointer(op)), uintptr(unsafe.Pointer(u)), 0, 0, 1 /*SW_SHOWNORMAL*/)
	if ret <= 32 {
		return fmt.Errorf("ShellExecuteW 返回 %d（%v）", ret, e)
	}
	return nil
}

/* ============================ 窗口过程 ============================ */

func wndProc(hwnd uintptr, m uint32, w, l uintptr) (ret uintptr) {
	// ⚠️ 外面这一层 `safe` 不是装饰：这是**系统回调**，在这里 panic ⇒ 整个 exe 直接 exit 2 且没有文本
	//（§72 的现场就是这样消失的，见 safe 的注释）。救回来之后细条继续用。
	safe("窗口过程", func() { ret = wndProcBody(hwnd, m, w, l) })
	return ret
}

func wndProcBody(hwnd uintptr, m uint32, w, l uintptr) uintptr {
	r := theRail
	if r == nil {
		ret, _, _ := pDefWindowProcW.Call(hwnd, uintptr(m), w, l)
		return ret
	}
	// 参数条（`panel_windows.go`）：它自己一套消息处理
	if ret, done := r.panelProc(hwnd, m, w, l); done {
		return ret
	}
	// 那个"栏外弹全名"的小窗：只画，不接鼠标（带 WS_EX_TRANSPARENT，鼠标会穿过去）
	if hwnd == r.tipHWND && r.tipHWND != 0 {
		switch m {
		case 0x000F: // WM_PAINT
			r.paintTip()
			return 0
		case 0x0014: // WM_ERASEBKGND
			return 1
		}
		ret, _, _ := pDefWindowProcW.Call(hwnd, uintptr(m), w, l)
		return ret
	}
	switch m {
	case 0x000F: // WM_PAINT
		r.paint()
		return 0
	case 0x0014: // WM_ERASEBKGND：自己画底，别闪
		return 1
	case wmLButtonUp: // 点一格就干一件事（第①级不接受点击，见 click）
		_, y := int32(l&0xffff), int32((l>>16)&0xffff)
		r.click(r.itemAt(y))
		return 0
	case wmRButtonUp:
		// ⭐ 右键 = **改附属参数**（用户 2026-10-17：「连接这个，我想做成点按钮就用，参数只是附带」
		// ⇒ 左键跑、右键调参数）。只有"带参数条"的项有这一层；别的项右键就报一句白。
		_, y := int32(l&0xffff), int32((l>>16)&0xffff)
		r.rbutton(r.itemAt(y))
		return 0
	case wmClose: // 只有我们主动 Stop()（那会给窗口发 WM_CLOSE）才走这儿
		r.stopping = true
		if r.tipHWND != 0 {
			pDestroyWindow.Call(r.tipHWND)
			r.tipHWND = 0
		}
		pDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		// ⚠️ 两种情形必须分开（第五轮的子窗口架构就卡在这儿）：
		//  ① `stopping=true` = **我们自己关**（Stop）⇒ 停定时器、退出消息循环（exe 继续跑）；
		//  ② `stopping=false` = **ACE 被关了、系统把我们子窗口一起销毁** ⇒ **不能退出循环**：
		//     线程定时器还在跑，等 ACE 回来 `tick` 会重建窗口再挂上去。
		//     （这也是定时器必须挂在**线程**上、不能挂在窗口上的原因。）
		wasStopping := r.stopping
		if hwnd == r.hwnd {
			r.hwnd = 0
			r.parentHWND = 0
			r.mu.Lock()
			r.state.HWND, r.state.ParentHWND, r.state.Visible = 0, 0, false
			r.mu.Unlock()
		}
		if hwnd == r.tipHWND {
			r.tipHWND = 0
		}
		if wasStopping {
			pKillTimer.Call(0, timerFollow)
			pPostQuitMessage.Call(0)
		}
		return 0
	}
	ret, _, _ := pDefWindowProcW.Call(hwnd, uintptr(m), w, l)
	return ret
}
// ReloadItems 换一份条目（设置页改了顺序/显隐后调用）：重算格高、重新贴边、重画。
func (r *Rail) ReloadItems(items []Item) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.items = items
	r.state.ItemH = 0 // 让 layout() 按新条数重算
	r.state.Hover = -1
	r.hover = -1
	r.mu.Unlock()
	r.follow()
	pInvalidateRect.Call(r.hwnd, 0, 1)
}

// Reload 把**当前那份配置**重新灌进正在跑的侧栏（用户 2026-10-20「改完设置之后什么时候生效」——
// 以前保存后侧栏要重建才看得到，现在保存即时生效）。
func Reload() {
	if theRail != nil {
		theRail.ReloadItems(Items())
	}
}
