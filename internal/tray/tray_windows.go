//go:build windows

package tray

// Windows **系统托盘（通知区域）图标**：`Shell_NotifyIcon` + 一个隐藏的顶层窗口。
//
// 用户 2026-10-17：「加个状态栏图标，图标使用 `SVAgent\electron\src\ace-mark-anim.svg`
// 这个 svg 里那个黑色的 a」。图标怎么来的见 `icon_windows.go` 顶部
// （无头 Edge 光栅化 → 嵌入 PNG → HICON）。
//
// 为什么需要一个**窗口**：`Shell_NotifyIcon` 的回调不是回调函数，而是"往某个窗口发一条消息"
//   ⇒ 必须有个 HWND 收。这里建一个**从不 Show 的顶层窗口**（不是 message-only 窗口）——
//   message-only（`HWND_MESSAGE`）**收不到广播消息**，而"资源管理器崩了/重启了"时
//   Shell 会广播 `TaskbarCreated`，我们得靠它把图标加回去（不加回去 = 图标永久消失）。
//
// 交互（最小一套，别自己发明更多）：
//   · 左键单击 → 打开/切到面板窗口；
//   · 右键 → 菜单：打开面板 / 打开设置 / 退出 ACETool。
//   ⚠️ 退出走的是 `Options.OnQuit` → main 里那条"请求退出"的通道，和 Ctrl+C 走同一条收尾路径
//   （先 rail.Stop 再 srv.Shutdown），不是 `os.Exit` 硬杀。
import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"acetool/internal/win"
)

// Options 是托盘要的东西：一句话的悬停提示 + 三个动作。
// 动作都在**独立 goroutine** 里跑（开窗口、收尾都是慢活，不能卡住消息循环）。
type Options struct {
	Tip        string
	OnOpen     func()
	OnSettings func()
	OnQuit     func()
}

// Info 是给 `/api/tray` 与排障用的现场快照。
type Info struct {
	Running  bool    `json:"running"`
	HWND     uintptr `json:"hwnd,omitempty"`
	IconSize int     `json:"iconSize"`
	Icon     string  `json:"icon"`
	Added    bool    `json:"added"`
	Tip      string  `json:"tip"`
	DPI      int     `json:"dpi"`
	Err      string  `json:"err,omitempty"`
}

// Tray 是一个跑在**自己线程**上的托盘（消息循环必须有稳定的 OS 线程）。
type Tray struct {
	opts Options

	ready    chan struct{}
	startErr error

	mu      sync.Mutex
	info    Info
	hwnd    uintptr
	hicon   uintptr
	added   bool
	iconSz  int
	stopped bool
}

const (
	wmTrayCB = 0x8000 + 1 // WM_APP + 1

	wmNull      = 0x0000
	wmClose     = 0x0010
	wmDestroy   = 0x0002
	wmLButtonUp = 0x0202
	wmLDouble   = 0x0203
	wmRButtonUp = 0x0205

	menuOpen     = 3
	menuSettings = 4
	menuQuit     = 5

	mfString    = 0x0000
	mfSeparator = 0x0800

	tpmRightButton = 0x0002
	tpmNonotify    = 0x0080
	tpmReturnCmd   = 0x0100

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2

	nifMessage = 0x0001
	nifIcon    = 0x0002
	nifTip     = 0x0004
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	pRegisterClassExW      = user32.NewProc("RegisterClassExW")
	pCreateWindowExW       = user32.NewProc("CreateWindowExW")
	pDefWindowProcW        = user32.NewProc("DefWindowProcW")
	pDestroyWindow         = user32.NewProc("DestroyWindow")
	pPostQuitMessage       = user32.NewProc("PostQuitMessage")
	pPostMessageW          = user32.NewProc("PostMessageW")
	pGetMessageW           = user32.NewProc("GetMessageW")
	pTranslateMessage      = user32.NewProc("TranslateMessage")
	pDispatchMessageW      = user32.NewProc("DispatchMessageW")
	pRegisterWindowMessage = user32.NewProc("RegisterWindowMessageW")
	pGetModuleHandleW      = kernel32.NewProc("GetModuleHandleW")
	pShellNotifyIconW      = shell32.NewProc("Shell_NotifyIconW")
	pCreatePopupMenu       = user32.NewProc("CreatePopupMenu")
	pAppendMenuW           = user32.NewProc("AppendMenuW")
	pDestroyMenu           = user32.NewProc("DestroyMenu")
	pTrackPopupMenu        = user32.NewProc("TrackPopupMenu")
	pSetForegroundWindow   = user32.NewProc("SetForegroundWindow")
	pGetCursorPos          = user32.NewProc("GetCursorPos")
	pSetMenuDefaultItem    = user32.NewProc("SetMenuDefaultItem")
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

type point struct{ X, Y int32 }

type msgT struct {
	HWND    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

// notifyIconDataW 是 `NOTIFYICONDATAW`（Vista+ 完整布局；`cbSize` 就是它的 sizeof）。
//
// ⚠️ 别"精简"字段：`cbSize` 决定 Shell 按哪个版本的布局解释后面那些字节，
// 少写字段 = cbSize 变小 = 老版本语义（`szTip` 只认 64 字节、没 hBalloonIcon 等）。
type notifyIconDataW struct {
	Size         uint32
	Wnd          uintptr
	ID           uint32
	Flags        uint32
	CallbackMsg  uint32
	Icon         uintptr
	Tip          [128]uint16
	State        uint32
	StateMask    uint32
	Info         [256]uint16
	TimeoutOrVer uint32
	InfoTitle    [64]uint16
	InfoFlags    uint32
	GUIDItem     [16]byte
	BalloonIcon  uintptr
}

// 进程级的几样东西：类名、任务栏重建消息、那个**唯一的**窗口过程回调。
//
// ⛔ 窗口过程只建一个回调（`sync.Once`）：Go 的 Windows 回调表**定长且永不释放**（§72）。
var (
	clsOnce    sync.Once
	clsName    = "ACEToolTray"
	taskbarMsg uint32

	procOnce sync.Once
	procAddr uintptr

	currentTray *Tray // 窗口过程要能拿到"我"（同一进程只会有一个托盘）
)

// Start 起托盘。它**阻塞到窗口建好**（成功或失败），之后的动作都在托盘线程里跑。
func Start(o Options) (*Tray, error) {
	t := &Tray{opts: o, ready: make(chan struct{})}
	dpi := int(win.DPIScale() * 96)
	sz := pickSize(int(float64(iconBase) * win.DPIScale()))
	t.mu.Lock()
	t.iconSz = sz
	t.info = Info{Tip: o.Tip, IconSize: sz, Icon: iconResource(sz), DPI: dpi}
	t.mu.Unlock()
	go t.run()
	<-t.ready
	if t.startErr != nil {
		return nil, t.startErr
	}
	return t, nil
}

// run 是托盘线程：锁 OS 线程 → 建窗口 → 加图标 → 消息循环。
func (t *Tray) run() {
	// 消息队列是**线程**的 ⇒ 必须把 goroutine 钉在一个固定的 OS 线程上
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := t.create(); err != nil {
		t.startErr = err
		close(t.ready)
		return
	}
	close(t.ready)
	t.loop()
}

// create 建隐藏窗口 + 做 HICON + 加进托盘。
func (t *Tray) create() error {
	hInst, _, _ := pGetModuleHandleW.Call(0)
	var regErr error
	clsOnce.Do(func() {
		name, _ := syscall.UTF16PtrFromString(clsName)
		wc := wndClassExW{
			Size:      uint32(unsafe.Sizeof(wndClassExW{})),
			WndProc:   wndProcAddrOnce(),
			Instance:  hInst,
			ClassName: name,
		}
		if ret, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); ret == 0 && !isClassExists(err) {
			regErr = fmt.Errorf("RegisterClassExW 失败：%v", err)
			return
		}
		if m, _, _ := pRegisterWindowMessage.Call(uintptr(unsafe.Pointer(utf16("TaskbarCreated")))); m != 0 {
			taskbarMsg = uint32(m)
		}
	})
	if regErr != nil {
		return regErr
	}
	name := utf16(clsName)
	title := utf16("ACETool 托盘")
	// ⚠️ 不传 WS_VISIBLE 且从不 Show：它只是"收消息的落点"，不该出现在屏幕上/任务栏里。
	hwnd, _, err := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(title)),
		0, 0, 0, 0, 0, 0, 0, hInst, 0)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowExW（托盘窗口）失败：%v", err)
	}
	t.mu.Lock()
	t.hwnd = hwnd
	t.info.HWND = hwnd
	t.info.Running = true
	t.mu.Unlock()
	currentTray = t

	hicon, ierr := makeHICON(t.iconSz)
	if ierr != nil {
		// 图标做不出来也**不放弃**：托盘还在（能点开面板/退出），只是没图 —— 如实记下来
		t.setErr(ierr)
	} else {
		t.mu.Lock()
		t.hicon = hicon
		t.mu.Unlock()
	}
	t.add()
	return nil
}

// add 把图标加进通知区域（`TaskbarCreated` 之后也要再来一次）。
func (t *Tray) add() {
	t.mu.Lock()
	hwnd, hicon, tip := t.hwnd, t.hicon, t.opts.Tip
	t.mu.Unlock()
	if hwnd == 0 {
		return
	}
	var nid notifyIconDataW
	nid.Size = uint32(unsafe.Sizeof(nid))
	nid.Wnd = hwnd
	nid.ID = 1
	nid.Flags = nifMessage | nifIcon | nifTip
	nid.CallbackMsg = wmTrayCB
	nid.Icon = hicon
	copy(nid.Tip[:], syscall.StringToUTF16(tip))
	ret, _, e := pShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	ok := ret != 0
	t.mu.Lock()
	t.added = ok
	t.info.Added = ok
	t.mu.Unlock()
	if !ok {
		t.setErr(fmt.Errorf("Shell_NotifyIcon(NIM_ADD) 失败：%v", e))
	}
}

// Update 改悬停提示（比如端口变了）。`Shell_NotifyIcon` 不要求"调用线程 = 窗口线程"，
// 所以这个可以从任何 goroutine 调。
func (t *Tray) Update(tip string) {
	t.mu.Lock()
	t.opts.Tip = tip
	t.info.Tip = tip
	hwnd, hicon, added := t.hwnd, t.hicon, t.added
	t.mu.Unlock()
	if hwnd == 0 || !added {
		return
	}
	var nid notifyIconDataW
	nid.Size = uint32(unsafe.Sizeof(nid))
	nid.Wnd = hwnd
	nid.ID = 1
	nid.Flags = nifTip | nifIcon
	nid.Icon = hicon
	copy(nid.Tip[:], syscall.StringToUTF16(tip))
	pShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

// Info 返回现场快照（`/api/tray` 用）。
func (t *Tray) Info() Info {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.info
}

// Stop 收掉图标并结束托盘线程（幂等，最多等 2 秒）。
func (t *Tray) Stop() {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return
	}
	t.stopped = true
	hwnd := t.hwnd
	t.mu.Unlock()
	if hwnd == 0 {
		return
	}
	pPostMessageW.Call(hwnd, wmClose, 0, 0)
	for i := 0; i < 40; i++ {
		t.mu.Lock()
		running := t.info.Running
		t.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (t *Tray) setErr(err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	t.info.Err = err.Error()
	t.mu.Unlock()
}

// loop 是消息循环。
func (t *Tray) loop() {
	var m msgT
	for {
		ret, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			return
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func wndProcAddrOnce() uintptr {
	procOnce.Do(func() { procAddr = syscall.NewCallback(trayProc) })
	return procAddr
}

// trayProc 是托盘窗口的窗口过程。
func trayProc(hwnd uintptr, m uint32, w, l uintptr) uintptr {
	t := currentTray
	if t == nil {
		r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(m), w, l)
		return r
	}
	switch m {
	case wmTrayCB:
		switch uint32(l) {
		case wmLButtonUp, wmLDouble:
			if f := t.opts.OnOpen; f != nil {
				go f() // 别在窗口过程里开窗口（会卡住整个托盘线程）
			}
			return 0
		case wmRButtonUp:
			t.menu()
			return 0
		}
		return 0
	case wmClose:
		t.remove()
		pDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	}
	if taskbarMsg != 0 && m == taskbarMsg {
		// 资源管理器重启了：通知区域被清空，重新加一份
		t.add()
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(m), w, l)
	return r
}

// remove 从通知区域删掉图标 + 销毁 HICON。
func (t *Tray) remove() {
	t.mu.Lock()
	hwnd, added, hicon := t.hwnd, t.added, t.hicon
	t.added = false
	t.info.Added = false
	t.hicon = 0
	t.mu.Unlock()
	if hwnd != 0 && added {
		var nid notifyIconDataW
		nid.Size = uint32(unsafe.Sizeof(nid))
		nid.Wnd = hwnd
		nid.ID = 1
		pShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	}
	if hicon != 0 {
		pDestroyIcon.Call(hicon)
	}
	t.mu.Lock()
	t.info.Running = false
	t.mu.Unlock()
}

// menu 弹右键菜单。⚠️ 三段老坑一个都不能少：
//
//	① `SetForegroundWindow(hwnd)` —— 不先变前台，菜单**点外面不会消失**（一直挂着）；
//	② `TPM_RETURNCMD|TPM_NONOTIFY` —— 命令号直接从返回值拿，省掉 WM_COMMAND 那一路；
//	③ 关掉菜单后 `PostMessage(hwnd, WM_NULL)` —— documented workaround，少了它
//	   下一次点托盘图标要**点两下**才响应。
func (t *Tray) menu() {
	hmenu, _, _ := pCreatePopupMenu.Call()
	if hmenu == 0 {
		return
	}
	defer pDestroyMenu.Call(hmenu)
	addItem(hmenu, menuOpen, "打开面板")
	addItem(hmenu, menuSettings, "打开设置")
	pAppendMenuW.Call(hmenu, mfSeparator, 0, 0)
	addItem(hmenu, menuQuit, "退出 ACETool")
	pSetMenuDefaultItem.Call(hmenu, menuOpen, 0) // 默认项加粗

	var p point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	pSetForegroundWindow.Call(t.hwnd)
	cmd, _, _ := pTrackPopupMenu.Call(hmenu,
		tpmReturnCmd|tpmRightButton|tpmNonotify,
		uintptr(p.X), uintptr(p.Y), 0, t.hwnd, 0)
	pPostMessageW.Call(t.hwnd, wmNull, 0, 0)
	switch cmd {
	case menuOpen:
		if f := t.opts.OnOpen; f != nil {
			go f()
		}
	case menuSettings:
		if f := t.opts.OnSettings; f != nil {
			go f()
		}
	case menuQuit:
		if f := t.opts.OnQuit; f != nil {
			go f()
		}
	}
}

func addItem(hmenu uintptr, id uintptr, text string) {
	pAppendMenuW.Call(hmenu, mfString, id, uintptr(unsafe.Pointer(utf16(text))))
}

func utf16(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		p, _ = syscall.UTF16PtrFromString("")
	}
	return p
}

// isClassExists 判断"注册窗口类失败"是不是**这个类已经注册过了**（ERROR_CLASS_ALREADY_EXISTS=1410）——
// 停掉托盘再起（自检/切换）时同一个类名会再注册一次，那不是错误。
func isClassExists(err error) bool {
	if e, ok := err.(syscall.Errno); ok {
		return e == 1410
	}
	return false
}
