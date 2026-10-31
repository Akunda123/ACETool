//go:build windows

package hotkeys

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"acetool/internal/win"
)

// Windows 的 `RegisterHotKey` + 消息循环。**纯 syscall，不用 CGO、不引第三方库**
// （与「单文件 exe · CGO_ENABLED=0」这条线一致）。
//
// 设计要点：
//   - `RegisterHotKey(hwnd=0, ...)` 把 `WM_HOTKEY` 投到**调用线程的消息队列** ⇒
//     连窗口都不用建，锁一个 OS 线程在那儿 `GetMessage` 就行。
//   - 组合键一旦注册成功就被**系统级吞掉**：浏览器根本收不到 ⇒ 前端必须跳过这些键（`Binding.Global`）。
//   - 注册会失败（最常见 `ERROR_HOTKEY_ALREADY_REGISTERED` = 1409，被别的软件占了）。
//     **逐条回报**，绝不整体当成功 —— 否则用户只会觉得"这个键没反应"。
type Manager struct {
	mu      sync.Mutex
	reg     map[int]string // 注册 id → 动作 id
	actions map[string]Action
	fire    func(actionID string)
	tid     uint32
	started bool

	// ⚠️⚠️ 注册/注销**必须在消息循环那个线程上做**：
	// `RegisterHotKey(hwnd=0, …)` 把热键绑到**调用它的线程**上，`WM_HOTKEY` 也投进那个线程的队列。
	// 起初 `Apply` 是直接从 HTTP goroutine 调的 ⇒ ① 热键绑到一个**没人泵消息**的线程上，
	// 按下去**永远不会响**；② 换个 goroutine 再注册同一组合键 ⇒ 报"已被占用"。
	// （真机自检里"重存后三条注册全变 ✗"就是这么抓出来的。）
	// 现在统一走"发消息给循环线程 + 等回执"。
	reqMu   sync.Mutex
	reqs    map[uintptr]*applyReq
	nextReq uintptr
}

type applyReq struct {
	b     Bindings
	reply chan []Result
}

var (
	user32              = syscall.NewLazyDLL("user32.dll")
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	pRegisterHotKey     = user32.NewProc("RegisterHotKey")
	pUnregisterHotKey   = user32.NewProc("UnregisterHotKey")
	pGetMessageW        = user32.NewProc("GetMessageW")
	pPostThreadMessageW = user32.NewProc("PostThreadMessageW")
	pGetCurrentThreadID = kernel32.NewProc("GetCurrentThreadId")
)

const (
	wmHotkey = 0x0312
	wmQuit   = 0x0012
	// wmApply 是"请循环线程去注册"的自定义消息（WM_APP 起）。
	wmApply = 0x8000 + 1

	modAlt      = 0x0001
	modControl  = 0x0002
	modShift    = 0x0004
	modWin      = 0x0008
	modNoRepeat = 0x4000
)

type msgT struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

// Result 是一条绑定的注册结果（逐条给前端显示）。
type Result struct {
	Action string `json:"action"`
	Key    string `json:"key"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

// NewManager 建一个管理器；`fire` 是按下热键时**在别的 goroutine 里**执行的动作。
func NewManager(actions map[string]Action, fire func(actionID string)) *Manager {
	return &Manager{
		reg: map[int]string{}, actions: actions, fire: fire,
		reqs: map[uintptr]*applyReq{},
	}
}

// Start 起消息循环（幂等）。
func (m *Manager) Start() {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.mu.Unlock()

	ready := make(chan uint32, 1)
	go func() {
		runtime.LockOSThread() // GetMessage 认线程消息队列 ⇒ 必须锁死这个线程
		tid, _, _ := pGetCurrentThreadID.Call()
		m.mu.Lock()
		m.tid = uint32(tid)
		m.mu.Unlock()
		ready <- uint32(tid)

		var msg msgT
		for {
			ret, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(ret) <= 0 { // 0 = WM_QUIT，-1 = 出错
				m.unregisterAllOnThisThread() // 退出前在自己线程上撤干净
				return
			}
			switch msg.Message {
			case wmHotkey:
				// wParam 低 16 位是我们的注册 id
				id := int(msg.WParam & 0xFFFF)
				m.mu.Lock()
				action := m.reg[id]
				m.mu.Unlock()
				if action != "" && m.fire != nil {
					// ⚠️ 别在这个线程里干活：它是消息循环，卡住就再也收不到热键
					go m.fire(action)
				}
			case wmApply:
				// 注册请求：**必须在当前线程上做**（见 Manager 上的那段注释）
				m.reqMu.Lock()
				req := m.reqs[msg.WParam]
				delete(m.reqs, msg.WParam)
				m.reqMu.Unlock()
				if req != nil {
					req.reply <- m.applyOnThisThread(req.b)
				}
			}
		}
	}()
	m.tid = <-ready
}

// Apply 用一整套绑定替换当前的注册。返回**逐条**结果（含失败原因）。
//
// 它自己不在调用方的线程上注册，而是把活儿交给消息循环线程 —— 否则热键会绑到一个
// 没人泵消息的线程上（按下去不响），而且再注册同一组合键会被判"已占用"。
func (m *Manager) Apply(b Bindings) []Result {
	m.Start()
	ch := make(chan []Result, 1)
	m.reqMu.Lock()
	m.nextReq++
	id := m.nextReq
	m.reqs[id] = &applyReq{b: b, reply: ch}
	m.reqMu.Unlock()

	m.mu.Lock()
	tid := m.tid
	m.mu.Unlock()
	if tid == 0 {
		return []Result{{Error: "热键消息循环没起来 ⇒ 系统级热键没注册"}}
	}
	if r, _, err := pPostThreadMessageW.Call(uintptr(tid), wmApply, id, 0); r == 0 {
		return []Result{{Error: fmt.Sprintf("叫不动热键线程（PostThreadMessage 失败：%v）", err)}}
	}
	select {
	case res := <-ch:
		return res
	case <-time.After(3 * time.Second):
		return []Result{{Error: "热键线程 3 秒没回应（消息循环卡住了？）⇒ 这次注册没完成"}}
	}
}

// applyOnThisThread 是真正的注册动作，**只在消息循环那个线程上调用**。
func (m *Manager) applyOnThisThread(b Bindings) []Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 先全部撤掉（重新注册比 diff 简单，也不会留下幽灵注册）
	for id := range m.reg {
		pUnregisterHotKey.Call(0, uintptr(id))
	}
	m.reg = map[int]string{}

	var out []Result
	id := 1
	// 稳定顺序：按动作 id 排一遍，注册 id 才不会每次启动都变（便于排查）
	ids := make([]string, 0, len(b))
	for k := range b {
		ids = append(ids, k)
	}
	sortStrings(ids)
	for _, action := range ids {
		bd := b[action]
		if bd.Key == "" || !bd.Global {
			continue
		}
		a, known := m.actions[action]
		if !known {
			out = append(out, Result{Action: action, Key: bd.Key, Error: "这个动作 id 不认识（绑定表比动作表旧？）"})
			continue
		}
		if !a.CanGlobal {
			out = append(out, Result{Action: action, Key: bd.Key,
				Error: fmt.Sprintf("「%s」只能绑在面板内 —— exe 自己执行不了它（它要动页面）", a.Label)})
			continue
		}
		mods, vk, err := toWinKey(bd.Key)
		if err != nil {
			out = append(out, Result{Action: action, Key: bd.Key, Error: err.Error()})
			continue
		}
		// id 只在本进程内唯一；`MOD_NOREPEAT` 避免按住不放时反复触发（写工具尤其危险）
		r, _, callErr := pRegisterHotKey.Call(0, uintptr(id), uintptr(mods|modNoRepeat), uintptr(vk))
		if r == 0 {
			out = append(out, Result{Action: action, Key: bd.Key, Error: describeHotkeyErr(callErr)})
			id++
			continue
		}
		m.reg[id] = action
		out = append(out, Result{Action: action, Key: bd.Key, OK: true})
		id++
	}
	return out
}

func (m *Manager) unregisterAllOnThisThread() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.reg {
		pUnregisterHotKey.Call(0, uintptr(id))
	}
	m.reg = map[int]string{}
}

// Stop 退出消息循环（面板关闭时用）。注销由循环线程收到 WM_QUIT 之后自己做。
func (m *Manager) Stop() {
	m.mu.Lock()
	tid, started := m.tid, m.started
	m.mu.Unlock()
	if !started || tid == 0 {
		return
	}
	pPostThreadMessageW.Call(uintptr(tid), wmQuit, 0, 0)
	m.mu.Lock()
	m.started = false
	m.mu.Unlock()
}

// FocusPanel 是 `panel.focus` 的实现（放这儿是因为它和热键是一件事：人不在面板上才需要）。
func FocusPanel(titleSubstr string) (string, error) {
	ws, err := win.Find(titleSubstr)
	if err != nil {
		return "", err
	}
	if len(ws) == 0 {
		return "", fmt.Errorf("没找到标题里含 %q 的窗口 —— "+
			"浏览器窗口标题跟的是**当前标签页**，把面板那个标签切到前台再按一次", titleSubstr)
	}
	// 多个候选：挑第一个能聚焦的，并把"匹配到几个"说出来（不静默挑一个）
	var lastErr error
	for _, w := range ws {
		ok, ferr := win.Focus(w.Handle)
		if ok {
			return fmt.Sprintf("已把「%s」调到前台（匹配到 %d 个候选窗口）", w.Title, len(ws)), nil
		}
		lastErr = ferr
	}
	return "", lastErr
}

// describeHotkeyErr 把 Windows 的错误码翻成人话（`ERROR_HOTKEY_ALREADY_REGISTERED`=1409 最常见）。
func describeHotkeyErr(err error) string {
	if err == nil {
		return "RegisterHotKey 被拒（系统没给原因）"
	}
	s := err.Error()
	if strings.Contains(s, "1409") {
		return "这个组合键**已被别的软件占用**（ERROR_HOTKEY_ALREADY_REGISTERED）⇒ 换一个，或先关掉占它的那个程序"
	}
	return "RegisterHotKey 被拒：" + s
}

// toWinKey 把规范键位（`Ctrl+Alt+Enter`）翻成 `RegisterHotKey` 的 (modifiers, virtual-key)。
//
// ⚠️ 认不出来的主键**必须报错**：默默注册成一个错键（比如都映射到 0）会让用户以为绑上了。
func toWinKey(key string) (int, int, error) {
	parts := strings.Split(key, "+")
	mods, main := 0, ""
	for _, p := range parts {
		switch p {
		case "Ctrl":
			mods |= modControl
		case "Alt":
			mods |= modAlt
		case "Shift":
			mods |= modShift
		case "Win":
			mods |= modWin
		default:
			main = p
		}
	}
	vk, ok := vkOf(main)
	if !ok {
		return 0, 0, fmt.Errorf("主键 %q 翻不成 Windows 虚拟键码 ⇒ 这个绑定注册不了（先 Normalize 过再来）", main)
	}
	return mods, vk, nil
}

var vkNamed = map[string]int{
	"Enter": 0x0D, "Space": 0x20, "Tab": 0x09, "Escape": 0x1B, "Backspace": 0x08, "Delete": 0x2E,
	"Insert": 0x2D, "Home": 0x24, "End": 0x23, "PageUp": 0x21, "PageDown": 0x22,
	"ArrowUp": 0x26, "ArrowDown": 0x28, "ArrowLeft": 0x25, "ArrowRight": 0x27,
	"[": 0xDB, "]": 0xDD, "\\": 0xDC, ";": 0xBA, "'": 0xDE, ",": 0xBC, ".": 0xBE,
	"-": 0xBD, "=": 0xBB, "`": 0xC0, "/": 0xBF,
}

func vkOf(main string) (int, bool) {
	if v, ok := vkNamed[main]; ok {
		return v, true
	}
	if len(main) == 1 {
		c := main[0]
		switch {
		case c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			return int(c), true // VK 码就是 ASCII 大写字母/数字
		}
	}
	if strings.HasPrefix(main, "F") {
		var n int
		if _, err := fmt.Sscanf(main[1:], "%d", &n); err == nil && n >= 1 && n <= 24 {
			return 0x70 + n - 1, true // VK_F1 = 0x70
		}
	}
	if main == "?" {
		return 0xBF, true // Shift+/ —— 但裸 `?` 不该做成系统级（Validate 已经拦了）
	}
	return 0, false
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
