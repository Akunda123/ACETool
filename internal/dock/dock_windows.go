//go:build windows

// Package dock 把"贴 ACE 边上的那个 app 窗口"**吸**到 ACE 的边上。
//
// 用户 2026-10-16：「这个窗口能和 ace 的侧栏无缝切换吗？把窗口贴到四周的时候自动吸附并转换」。
// 结论（见 HANDOVER §74）：浏览器窗口最窄只能到 178 物理像素 ⇒ 不能缩成头发丝细条，
// 但**够细**⇒ 就用同一个 app 窗口兼任侧栏；要补的就是"自己会吸到 ACE 边上"。
//
// 为什么不复用 `internal/rail`：rail 是**我们自己建的窗口**（自己画、自己接鼠标、
// 而且它现在会让 exe 自己退出 —— §72）；这里动的是**别人进程的窗口**（浏览器 app 窗口），
// 只需要一个轮询循环 + `SetWindowPos`：**不建窗口、不接消息**，风险低得多。
package dock

import (
	"fmt"
	"sync"
	"time"

	"acetool/internal/win"
)

const (
	DefaultSnapPx   = 24 // 离 ACE 的边多近算"要吸"（逻辑像素）
	DefaultPollMS   = 120
	DefaultTopInset = 42 // 让开 ACE **自绘标题栏**（与细条同一口径，见 §72）
	aceExe          = "ACE Studio.exe"
)

// Options 是起吸附要的东西。
type Options struct {
	Title    string // 目标窗口标题前缀（如 `ACETool 侧栏`）
	SnapPx   int
	PollMS   int
	TopInset int
	OnNote   func(kind, text string)
}

// State 是吸附此刻的样子（`GET /api/dock` 报它 —— 自检的抓手）。
type State struct {
	Running  bool     `json:"running"`
	Title    string   `json:"title"`
	Window   string   `json:"window,omitempty"`
	HWND     uintptr  `json:"hwnd,omitempty"`
	AceTitle string   `json:"aceTitle,omitempty"`
	AceHWND  uintptr  `json:"aceHwnd,omitempty"`
	Snapped  bool     `json:"snapped"`
	Edge     string   `json:"edge,omitempty"`    // right / left / top / bottom
	Partial  bool     `json:"partial,omitempty"` // true = 只对齐了位置，尺寸没跟着（上/下边暂时如此）
	Dragging bool     `json:"dragging"`          // 用户正按着左键拖 ⇒ 我们不抢
	Rect     win.Rect `json:"rect"`              // 目标窗口现在在哪
	Want     win.Rect `json:"want,omitempty"`    // 吸附时"应该在哪"
	Ticks    int      `json:"ticks"`
	LastErr  string   `json:"lastErr,omitempty"`
}

// Dock 是一次吸附会话（一个目标窗口）。可以在运行中 Stop。
type Dock struct {
	opts  Options
	stop  chan struct{}
	once  sync.Once
	mu    sync.Mutex
	state State
}

// Start 起一次吸附（只跑一个 goroutine，**不建任何窗口**）。
func Start(opts Options) (*Dock, error) {
	if opts.Title == "" {
		return nil, fmt.Errorf("没给目标窗口标题（不知道该吸谁）")
	}
	if opts.SnapPx <= 0 {
		opts.SnapPx = DefaultSnapPx
	}
	if opts.PollMS <= 0 {
		opts.PollMS = DefaultPollMS
	}
	if opts.TopInset <= 0 {
		opts.TopInset = DefaultTopInset
	}
	d := &Dock{opts: opts, stop: make(chan struct{}), state: State{Running: true, Title: opts.Title}}
	go d.run()
	return d, nil
}

// State 读此刻状态。
func (d *Dock) State() State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

// Stop 停掉吸附（目标窗口**留在原地**，它不是我们建的 ⇒ 不藏也不关）。
func (d *Dock) Stop() {
	d.once.Do(func() { close(d.stop) })
	d.mu.Lock()
	d.state.Running = false
	d.mu.Unlock()
}

func (d *Dock) setErr(err error) {
	d.mu.Lock()
	if err != nil {
		d.state.LastErr = err.Error()
	}
	d.mu.Unlock()
}

func (d *Dock) note(text string) {
	if d.opts.OnNote != nil {
		d.opts.OnNote("info", text)
	}
}

func (d *Dock) run() {
	t := time.NewTicker(time.Duration(d.opts.PollMS) * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-d.stop:
			return
		case <-t.C:
			d.tick()
		}
	}
}

func (d *Dock) tick() {
	d.mu.Lock()
	d.state.Ticks++
	d.mu.Unlock()

	ace, ok := pickAce()
	if !ok {
		// ACE 不在（关了/最小化）⇒ **一下都不动那个窗口**（别人的窗口，不替他藏、不替他关）；
		// 但目标窗口**还是要报出来** —— 自检和界面得能看见"要吸的是谁、它现在在哪"。
		d.mu.Lock()
		d.state.AceTitle, d.state.AceHWND = "", 0
		d.state.Snapped, d.state.Dragging, d.state.Want = false, false, win.Rect{}
		d.mu.Unlock()
		d.reportTargetOnly()
		return
	}
	targets, err := win.FindPrefixed(d.opts.Title)
	if err != nil {
		d.setErr(err)
		return
	}
	var tw win.Window
	for _, w := range targets {
		if !win.Minimized(w.Handle) {
			tw = w
			break
		}
	}
	if tw.Handle == 0 {
		d.mu.Lock()
		d.state.Window, d.state.HWND = "", 0
		d.state.Snapped, d.state.Want = false, win.Rect{}
		d.mu.Unlock()
		return
	}
	rc, err := win.RectOf(tw.Handle)
	if err != nil {
		d.setErr(err)
		return
	}
	cli, cerr := win.ClientRectOf(ace.Handle)
	if cerr != nil {
		cli = mustRect(ace.Handle)
	}
	scale := win.DPIScale()
	inset := int32(float64(d.opts.TopInset) * scale)
	pad := int32(float64(6) * scale)
	snap := int32(float64(d.opts.SnapPx) * scale)

	// ⛔ 用户正按着左键（在拖那个窗口）⇒ **一下都不动它**：抢鼠标是最讨人厌的行为。
	//    松手之后（下一轮）再判断要不要吸。
	if win.LeftButtonDown() {
		d.mu.Lock()
		d.state.Window, d.state.HWND = tw.Title, tw.Handle
		d.state.AceTitle, d.state.AceHWND = ace.Title, ace.Handle
		d.state.Rect = rc
		d.state.Dragging, d.state.Snapped, d.state.Want = true, false, win.Rect{}
		d.mu.Unlock()
		return
	}

	edge, dist := nearestEdge(rc, cli)
	want := rc
	snapped, partial := false, false
	if dist <= snap {
		snapped = true
		want, partial = flushRect(edge, rc, cli, inset, pad, scale)
		if want != rc {
			if err := win.Move(tw.Handle, want); err != nil {
				d.setErr(err)
			}
		}
	}
	// 先把"想要的位置"发出去，下一次 tick 会看到窗口是否真的到那儿（Chromium 可能拒绝尺寸）
	d.mu.Lock()
	d.state.Window, d.state.HWND = tw.Title, tw.Handle
	d.state.AceTitle, d.state.AceHWND = ace.Title, ace.Handle
	d.state.Rect = rc
	d.state.Snapped, d.state.Edge, d.state.Partial = snapped, edge, partial
	d.state.Want = want
	d.state.Dragging = false
	d.mu.Unlock()
}

// reportTargetOnly 在"ACE 不在"时也要把目标窗口报出来（只读，不动它）。
func (d *Dock) reportTargetOnly() {
	targets, err := win.FindPrefixed(d.opts.Title)
	if err != nil {
		d.setErr(err)
		return
	}
	for _, w := range targets {
		if win.Minimized(w.Handle) {
			continue
		}
		rc, rerr := win.RectOf(w.Handle)
		if rerr != nil {
			continue
		}
		d.mu.Lock()
		d.state.Window, d.state.HWND, d.state.Rect = w.Title, w.Handle, rc
		d.mu.Unlock()
		return
	}
	d.mu.Lock()
	d.state.Window, d.state.HWND, d.state.Rect = "", 0, win.Rect{}
	d.mu.Unlock()
}

// nearestEdge 算目标窗口离 ACE 客户区哪条边最近、多近。
func nearestEdge(r, cli win.Rect) (string, int32) {
	type cand struct {
		name string
		d    int32
	}
	cs := []cand{
		{"right", abs32(r.Right - cli.Right)},
		{"left", abs32(r.Left - cli.Left)},
		{"top", abs32(r.Top - cli.Top)},
		{"bottom", abs32(r.Bottom - cli.Bottom)},
	}
	best := cs[0]
	for _, c := range cs[1:] {
		if c.d < best.d {
			best = c
		}
	}
	return best.name, best.d
}

// flushRect 算"贴着那条边"时的矩形。
//
// 左右边 = **真的贴成侧栏**（宽度保留用户调好的，高度跟着 ACE 客户区）；
// 上下边 = 只有位置对齐、尺寸不动（`partial=true`）—— 因为窄栏页是**竖排**的，
// 横过来那 19 行会挤成一条缝；横排布局是页面侧的事，还没做（如实标出来，别假装做完了）。
func flushRect(edge string, r, cli win.Rect, inset, pad int32, scale float64) (win.Rect, bool) {
	top := cli.Top + inset
	bottom := cli.Bottom - pad
	switch edge {
	case "right":
		return win.Rect{Left: cli.Right - r.W(), Top: top, Right: cli.Right, Bottom: bottom}, false
	case "left":
		return win.Rect{Left: cli.Left, Top: top, Right: cli.Left + r.W(), Bottom: bottom}, false
	case "top":
		return win.Rect{Left: r.Left, Top: top, Right: r.Right, Bottom: top + r.H()}, true
	default: // bottom
		return win.Rect{Left: r.Left, Top: bottom - r.H(), Right: r.Right, Bottom: bottom}, true
	}
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// pickAce 找"要贴的那个 ACE 窗口"：pid 属于 ACE、可见、没最小化、**尺寸够大**（弹出小窗不要）。
//
// ⚠️ 判据用**尺寸**不是"有没有属主"：真机实测**钢琴窗是有属主的**而它必须贴；
// 那三个 96x56 / 152x48 / 184x80 的小窗才是要排除的弹出窗。（与 `internal/rail` 同一套判据。）
func pickAce() (win.Window, bool) {
	pids, err := win.ByExeName(aceExe)
	if err != nil || len(pids) == 0 {
		return win.Window{}, false
	}
	all, err := win.List()
	if err != nil {
		return win.Window{}, false
	}
	var cand []win.Window
	for _, w := range all {
		if !containsPID(pids, w.PID) || win.Minimized(w.Handle) {
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
			return w, true // 焦点是哪个就贴哪个
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

func mustRect(hwnd uintptr) win.Rect {
	r, err := win.RectOf(hwnd)
	if err != nil {
		return win.Rect{}
	}
	return r
}
