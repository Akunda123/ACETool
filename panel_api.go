package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"acetool/internal/acecli"
	"acetool/internal/autostart"
	"acetool/internal/config"
	"acetool/internal/hotkeys"
	"acetool/internal/layout"
	"acetool/internal/rail"
	"acetool/internal/tools"
	"acetool/internal/web"
	"acetool/internal/win"
)

// 面板的**会话状态**与那几个"面板自己"的接口：快捷键、窗口置顶、事件。
//
// 为什么要有这一层：系统级热键按下来时**网页完全不知情**（浏览器在后台），
// 所以 exe 得自己拿得住三样东西：
//
//	① 「面板上最后一次跑的是哪个工具、什么参数」⇒ 热键才知道该跑什么；
//	② 一条 exe → 页面的**推送通道**（这里用 `GET /api/events` 轮询，不引 SSE/WebSocket：
//	   本地单用户、事件稀疏，轮询够用而且断线不需要重连逻辑）；
//	③ 绑定表与它的落盘（`config.json`），外加**逐条**注册结果。
type panelState struct {
	client *acecli.Client
	from   string

	mu         sync.Mutex
	events     []panelEvent
	lastRun    *lastRunState
	bindings   hotkeys.Bindings
	defaults   hotkeys.Bindings
	actions    map[string]hotkeys.Action
	order      []string // 动作 id 的显示顺序（= 目录顺序）
	registered []hotkeys.Result
	loadWarns  []string
	// lastUndo 是**最近一次**写工具留下的撤回标记。热键在 exe 里直接跑时回包没有页面接
	// ⇒ 存一份，主页面起来后从 /api/undo 取（否则「撤回这一批」一直是灰的，2026-10-22 实测）。
	lastUndo *undoMark
	manager    *hotkeys.Manager
	onTop      bool

	// firedCount / lastFired 是**只增不清**的触发记录：事件现在按游标取、不会被取走，
	// 但环形缓冲仍会淘汰老的 ⇒ 这两项是"热键到底响过没有"的**永久**证据（自检与设置页用）。
	firedCount  int
	lastFired   string
	lastFiredAt time.Time

	// 事件序号与环形缓冲（多窗口各自带游标取，见 `/api/events`）
	eventSeq  int64
	startedAt time.Time
	// 页面报上来的窗口大小（`"1536x864"`）
	viewport string

	// gateGen 是「世代门」的当前世代（用户 2026-10-05 提的做法：**请求带上参数，收到时若已过期就不执行**）。
	//
	// 背景：一次"变色"会连发 N 条请求（每条改一条轨）。用户点停止时，那些**已经发出去、还在排队**的
	// 请求是收不回来的 —— 但它们**还没跑到工具**，所以可以在入口处判一下"这条是不是旧世代的"，
	// 是就直接跳过（不 spawn CLI）。这样：前端可以放心用**叠着走**的节奏（看起来快、自然），
	// 而停止能在一瞬间生效（当前正在执行的那一条会跑完，其余全变空操作）。
	gateGen int64
}

// lastRunState 是「面板上最后一次执行」—— 系统级热键的输入。
//
// ⚠️ 它**不是**"当前选中的工具"：那个只有页面知道（换工具不一定跑）。
// 热键按下去跑的是**跑过的那一次**，这样参数一定是用户当时真用的那份。说不清就如实说。
type lastRunState struct {
	ToolID string            `json:"toolId"`
	Title  string            `json:"title"`
	Values map[string]string `json:"values"`
	DryRun bool              `json:"dryRun"`
	At     time.Time         `json:"at"`
}

// panelEvent 是推给页面的一条消息（页面按**序号游标**增量取，见 `/api/events`）。
//
// ⚠️ 原来是"取走即清"的队列 —— 本地单用户下够用，但**多开一个窗口就是 bug**：
// 谁先轮询谁抢走，另一个窗口永远看不到系统级热键的结果（用户 2026-10-05 问"窗口可以开几个"时
// 我把这条列成了已知问题）。现在每条带单调递增的 `Seq`，事件留在**环形缓冲**里、各窗口各带游标取。
type panelEvent struct {
	Seq    int64  `json:"seq"`
	Ts     int64  `json:"ts"`
	Kind   string `json:"kind"`   // ok / bad / info
	Source string `json:"source"` // hotkey 之类
	Action string `json:"action,omitempty"`
	Text   string `json:"text"`
}

// panelEventRing 是环形缓冲容量（多窗口各自回看一小段够用；不涨内存）。
const panelEventRing = 200

func newPanelState(client *acecli.Client, from string) *panelState {
	st := &panelState{client: client, from: from, actions: map[string]hotkeys.Action{}, startedAt: time.Now()}
	// ⭐ 主页面那组键位 = `quick.open` + 编在 exe 里的那份按钮清单。
	// （用户 2026-10-17：「当前版本先不做（插件/安装按钮），以后再说」⇒ 没有"页面报名单"那条路了。）
	hotkeys.SeedBuiltin() // 按钮清单就一份（`hotkeys.quick.go` 的 builtinQuickButtons）
	st.rebuildActions()
	st.defaults = hotkeys.Defaults()
	st.bindings, st.loadWarns = hotkeys.Load()
	// 出厂里有的、而用户配置里没有的也要补进当前绑定（现在出厂表是空的，这段留着兜底）
	for id, b := range st.defaults {
		if _, ok := st.bindings[id]; !ok {
			st.bindings[id] = b
		}
	}
	st.manager = hotkeys.NewManager(st.actions, st.fire)
	st.manager.Start()
	st.registered = st.manager.Apply(st.bindings)

	// 上次开着置顶 ⇒ 这次起来也置上（尽力而为，失败只记一条事件，不打扰）
	// ⚠️ 这是个**全局记忆**（配置里的一个布尔），而置顶现在是按窗口的 ⇒ 开机只能尽力：
	// 走老路（标题含 `ACETool`），命中的多半是 home 那个窗口。
	if cfg, err := config.Load(); err == nil && cfg.PanelOnTop {
		if v, err := st.setTopMost(true, ""); err == nil {
			st.onTop = v.OnTop
		}
	}
	return st
}

// rebuildActions 按**当前按钮清单**重建动作表（`st.actions` / `st.order`）。
//
// 为什么要能重建：主页面那组键位是"跟着按钮清单长"的（用户 2026-10-17：以后新功能要能"装成一个按钮"）
// ⇒ 清单变了（页面报了新的一版 / 装了新按钮）就得重建，否则设置页还显示旧的那 20 行。
func (st *panelState) rebuildActions() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.actions = map[string]hotkeys.Action{}
	st.order = st.order[:0]
	for _, a := range hotkeys.Catalog() {
		st.actions[a.ID] = a
		st.order = append(st.order, a.ID)
	}
}

// Close 退出热键消息循环（不关的话退出时可能留着已注册的系统热键）。
func (st *panelState) Close() {
	if st.manager != nil {
		st.manager.Stop()
	}
}

// ---------- 执行工具（HTTP 与热键**共用这一条**）----------

// executeTool 跑一个工具，并把这次执行记成"最后一次"。
//
// ⚠️ 从 api.go 的 handler 里**搬出来的**，一字不改地共用：热键那条路如果另写一份，
// 就会慢慢和 HTTP 那条路分叉（回包形状、干跑闸门、错误信封都会不一样）。
func (st *panelState) executeTool(ctx context.Context, toolID string, values map[string]string) *runResponse {
	// ⭐ 世代门：请求带了 `_gen` 且已经过期 ⇒ **直接不执行**（用户在"停止"时把 gen 推进了一格）。
	// 这一步在**任何工具逻辑之前**，所以旧世代的请求连 CLI 都不会 spawn —— 队列能瞬间排空。
	if g := strings.TrimSpace(values["_gen"]); g != "" {
		st.mu.Lock()
		cur := st.gateGen
		st.mu.Unlock()
		if n, err := strconv.ParseInt(g, 10, 64); err == nil && n != cur {
			return &runResponse{
				ToolID: toolID, Title: toolID, Kind: tools.KindRead, CLIPath: st.client.Path,
				OK: true, Calls: []callResult{{Label: "已被叫停（世代替换）", Args: []string{toolID},
					ExitCode: 0, Stdout: "这条请求发出后用户点了停止 ⇒ 按世代门跳过，未执行任何命令。"}},
				Hint: "已叫停，未执行",
			}
		}
	}
	// ⚠️ `_gen` 到此为止：**必须删掉**再往下走。
	// 因为 `recordRun` 会把 values 原样存成"最后一次"，而热键 `run.primary` 就是**重放这份参数**——
	// 留着它的话，热键会带着一个旧世代号重放，被世代门当成过期请求静默跳过（"按了没反应"，最难查的那种）。
	delete(values, "_gen")
	tool := tools.Get(toolID)
	if tool == nil {
		return &runResponse{OK: false, Hint: "没有这个工具：" + toolID, Calls: []callResult{}}
	}
	// 记"最后一次"：**在跑之前记**，这样失败的尝试也算"最后一次用过它"（参数是用户真实给的那份）
	st.recordRun(tool, values)

	// ⭐「撤回刚才的一批」（用户 2026-10-07）：写工具**跑之前**先记下 ACE 撤销栈的游标，
	// 跑完再算游标差值 ⇒ 那个差值就是"刚才这一批压了几条"，面板据此给一颗撤回键。
	// 为什么现在能做了：`history list` 会给出每条条目的**署名**（我们 CLI 写的是「远程控制」，
	// 用户自己在 ACE 里动的是空串）⇒ 撤回时可以**只退自己写的**、一碰到用户的操作就停手。
	// 读一次栈 ~80ms，所以 `track.color`（RGB 循环）与只写文件的工具都标了 `NoUndoMark` 跳过。
	wantMark := tool.Kind == tools.KindWrite && !tool.NoUndoMark && !isTruthy(values["dryRun"])
	anchor := -1
	if wantMark {
		anchor = st.undoCursor(ctx)
	}
	resp := st.runTool(ctx, tool, values)
	if wantMark && anchor >= 0 {
		st.attachUndoMark(ctx, resp, tool, anchor)
	}
	return resp
}

// runTool 是 executeTool 的**主体**（分派到工具的四种实现）。拆出来是为了让"撤回标记"
// 能包在整次执行外面（游标要在跑之前记、跑完之后算差值）。
func (st *panelState) runTool(ctx context.Context, tool *tools.Tool, values map[string]string) *runResponse {
	resp := &runResponse{
		ToolID: tool.ID, Title: tool.Title, Kind: tool.Kind,
		CLIPath: st.client.Path, OK: true, Calls: []callResult{},
	}
	rd := func(ctx context.Context, args ...string) (*acecli.Result, error) {
		return st.client.Read(ctx, args...)
	}

	if tool.Apply != nil {
		if isTruthy(values["dryRun"]) {
			resp.DryRun = true
			v, perr := tool.Plan(ctx, rd, values)
			resp.inProc("进程内干跑（不写任何文件）", tool.ID, v, perr)
		} else {
			v, aerr := tool.Apply(ctx, rd, values)
			resp.inProc("进程内写入（只写副本）", tool.ID, v, aerr)
		}
		return resp
	}
	if tool.Inspect != nil {
		v, ierr := tool.Inspect(ctx, rd, values)
		resp.inProc("进程内读取（不经过 CLI 的写）", tool.ID, v, ierr)
		return resp
	}
	if tool.Build == nil {
		return &runResponse{OK: false, Calls: []callResult{},
			Hint: "工具「" + tool.ID + "」既没有 Build/Inspect 也没有 Plan+Apply —— 注册表坏了"}
	}
	calls, err := tool.Build(ctx, rd, values)
	if err != nil {
		return &runResponse{OK: false, Hint: err.Error(), Calls: []callResult{}}
	}
	if tool.Kind == tools.KindWrite && isTruthy(values["dryRun"]) {
		resp.DryRun = true
		resp.Plan = calls
		return resp
	}
	for _, c := range calls {
		res, err := st.client.Read(ctx, c.Args...)
		if err != nil {
			resp.OK = false
			resp.Calls = append(resp.Calls, callResult{
				Label: c.Label, Args: append([]string{st.client.Path}, c.Args...),
				ExitCode: acecli.ExitSpawnFailed, SpawnErr: err.Error(),
			})
			continue
		}
		resp.Calls = append(resp.Calls, newCallResult(c.Label, res))
		if !res.OK() {
			resp.OK = false
			if resp.Hint == "" {
				resp.Hint = explain(res)
			}
		}
	}
	return resp
}

// ---------- 「撤回刚才的一批」：撤销栈游标 ----------
//
// 机制与真机实测都在 internal/tools/history.go 的头注里。这里只做两件事：
// ① 写工具**跑之前**读一次 `history list` 的游标；② 跑完再读一次，把"这批新增了几条"塞进回包。
//
// ⚠️ 读失败一律**当没有**（`anchor = -1` / 不挂 `Undo`）：撤回键不亮。
// 宁可让用户自己去 ACE 里 Ctrl+Z，也不能给他一颗"看着能撤、其实撤不动"的键。

// undoCursor 读 ACE 撤销栈的游标（= 已生效条目数）。读不到回 -1。
func (st *panelState) undoCursor(ctx context.Context) int {
	stack, err := tools.ReadUndoStack(ctx, st.client.Read)
	if err != nil {
		return -1
	}
	return stack.Index
}

// attachUndoMark 跑完写工具后算"这一批压了几条"，挂到回包上（面板据此点亮撤回键）。
func (st *panelState) attachUndoMark(ctx context.Context, resp *runResponse, tool *tools.Tool, anchor int) {
	if resp == nil || !resp.OK || resp.DryRun {
		return
	}
	stack, err := tools.ReadUndoStack(ctx, st.client.Read)
	if err != nil || stack.Index <= anchor {
		return // 一条都没新增（比如"写下去值没变"）⇒ 没有可撤回的，别点亮
	}
	mark := &undoMark{
		Anchor: anchor, Created: stack.Index - anchor,
		Tool: tool.ID, Label: tool.Title,
	}
	// 条目名字（新的在前）——撤回时用它确认真退的是不是这一批（见 history.go 的 planUndo）
	for i := range stack.Entries {
		e := stack.Entries[i]
		if e.Index >= anchor && e.Index < stack.Index {
			mark.Names = append(mark.Names, e.Name)
		}
	}
	resp.Undo = mark
}

func (st *panelState) recordRun(tool *tools.Tool, values map[string]string) {
	cp := make(map[string]string, len(values))
	for k, v := range values {
		cp[k] = v
	}
	st.mu.Lock()
	st.lastRun = &lastRunState{
		ToolID: tool.ID, Title: tool.Title, Values: cp,
		DryRun: isTruthy(values["dryRun"]), At: time.Now(),
	}
	st.mu.Unlock()
}

// ---------- 系统级热键按下去干什么 ----------

func (st *panelState) fire(action string) {
	// ⭐ 主页面那 20 条（用户 2026-10-17）：exe **不自己点按钮** —— 它发一条事件，
	// 由主页面收事件去点（"点按钮"的语义只在页面里存在：菜单、子窗口、剪贴板都在页面侧）。
	//
	// ⚠️ 2026-10-17 用户「只留我说的那几个快捷键」⇒ 动作表只剩这 20 条，
	// 原来 `panel.focus` / `panel.ontop` 那两条 exe 侧动作也删了（唤起面板=托盘图标/winopen，
	// 置顶=顶栏那颗 📌）。真还有别的 id 走进来，如实说一句，别假装干了活。
	if hotkeys.IsQuickAction(action) {
		st.fireQuick(action)
		return
	}
	st.push("info", action, "这个动作已经不在快捷键表里了（动作表精简过）⇒ 不会做任何事。")
}

// homeTitle 是主页面窗口的标题（`home.html` 的 `<title>`）。
// ⚠️ 改标题的话这里要跟着改 —— `closeStaleHomeWindows` / 托盘「打开面板」也用它。
const homeTitle = "ACE 工具箱"

// fireQuick 执行一条"主页面键位"。
//
// `quick.open`：exe 自己就能干（打开/切到主页面窗口）。
// 其余：发事件让主页面去点那颗按钮 —— 但**主页面得在**：
//
//	· 在（有那个窗口）⇒ 直接发；
//	· 不在 ⇒ 先把它开出来，约 2 秒后再发（页面要加载起来才收得到事件）。
//	  这 2 秒是实测的加载时间（app 模式冷启动 1–2s），等到页面第一次轮询之后才稳。
func (st *panelState) fireQuick(action string) {
	if action == hotkeys.QuickOpenID {
		if trayActions.Open == nil {
			st.push("bad", action, "打开主页面这件事现在没人接（托盘/动作没初始化）—— 看日志。")
			return
		}
		go trayActions.Open()
		st.push("ok", action, "已打开/切到主页面窗口。")
		return
	}
	tool := hotkeys.QuickToolID(action)
	if tool == "" {
		st.push("bad", action, "这条键位没对应到任何按钮（动作 id 拼错了？）")
		return
	}
	if !homeWindowAlive() {
		// ⭐ 用户 2026-10-22：「设置了个连接音符的快捷键，打开的却是面板」——
		// 以前主页面没开着就一律先开面板再发事件；对**立即执行**类（连接音符/删词/合并…）
		// 完全没必要弹窗口 ⇒ 直接在这里跑工具（与 /api/run 同一个 executeTool）。
		// ⚠️ 只对**真有工具实体、且不依赖页面**的按钮这么干：
		//   · `undo.batch` 是伪动作（锚点在页面里）；`lyric.paste` 要页面读剪贴板 ⇒ 都不能在 exe 里跑。
		if b, ok := hotkeys.ButtonOf(tool); ok && strings.HasPrefix(b.Note, "立即执行") &&
			tools.Get(tool) != nil && tool != "lyric.paste" && tool != "undo.batch" {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
				defer cancel()
				res := st.executeTool(ctx, tool, map[string]string{})
				if res.Undo != nil {
					st.mu.Lock()
					st.lastUndo = res.Undo
					st.mu.Unlock()
				}
				if res.OK {
					st.push("ok", action, "已执行「"+b.Label+"」（主页面没开着，exe 直接跑的）")
				} else {
					st.push("bad", action, "「"+b.Label+"」失败："+res.Hint)
				}
			}()
			return
		}
		if trayActions.Open == nil {
			st.push("bad", action, "主页面没开着，而且现在开不了它 —— 先手动开一次。")
			return
		}
		go trayActions.Open()
		st.push("info", action, "主页面没开着 ⇒ 先把它打开，约 2 秒后再执行。")
		time.AfterFunc(2*time.Second, func() { st.push("quick", action, tool) })
		return
	}
	st.push("quick", action, tool)
}

// homeWindowAlive 主页面窗口在不在（标题完全一致）。
func homeWindowAlive() bool {
	ws, err := win.FindExact(homeTitle)
	if err != nil {
		return false
	}
	for _, w := range ws {
		if w.PID != uint32(os.Getpid()) { // 排除自己（我们没这种窗口，保险）
			return true
		}
	}
	return false
}

// ---------- 事件（exe → 页面）----------

func (st *panelState) push(kind, action, text string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.eventSeq++
	st.events = append(st.events, panelEvent{
		Seq: st.eventSeq, Ts: time.Now().UnixMilli(), Kind: kind, Source: "hotkey",
		Action: action, Text: text,
	})
	if len(st.events) > panelEventRing { // 环形：淘汰最老的（各窗口靠游标，不靠"取走"）
		st.events = st.events[len(st.events)-panelEventRing:]
	}
	// 触发计数：只增不清（自检用它当"热键到底响过没有"的证据 —— 事件本身会被环形淘汰）
	st.firedCount++
	st.lastFired = action + "：" + text
	st.lastFiredAt = time.Now()
}

// ---------- 窗口置顶 ----------

// onTopView 是置顶接口的回包。
//
// ⭐ 2026-10-16 起**按窗口**：面板每个页面各是一个浏览器窗口（`window.open` 各开一个），
// 页面点 📌 时上报自己的 `document.title` ⇒ 服务端只动**那一个**窗口
// （用户 2026-10-16 选的口径：「每个窗口各自」）。所以回包里要写清
// 动的到底是哪个窗口、以及**凭什么**认出来的（`match` / `picked`）——别让人以为一定"精确匹配过了"。
type onTopView struct {
	OnTop         bool   `json:"onTop"`
	WindowTitle   string `json:"windowTitle,omitempty"`
	WindowMatches int    `json:"windowMatches,omitempty"`
	Match         string `json:"match,omitempty"`  // exact / prefix / contains / legacy：凭什么找的
	Picked        string `json:"picked,omitempty"` // foreground / first：一页匹配到多个时挑了哪个
	Want          bool   `json:"want"`             // 配置里记的意图
	Supported     bool   `json:"supported"`
}

// pickPanelWindow 挑出**这一次该动的那一个**窗口。
//
// title 非空（页面点 📌 时上报自己的标题）时按三段找，**顺序即优先级**：
//  1. `exact`    整标题相等；
//  2. `prefix`   **以该标题开头** —— 真机上真正管用的是这一段：浏览器只会在**后面**加东西
//     （2026-10-15 实测 Edge 给的是 `ACETool 和另外 9 个页面 - 个人 - Microsoft Edge`）；
//  3. `contains` 标题里含这段（兜底：万一浏览器把标题截断或改写）；
//
// title 为空（系统级热键、设置页那条老路：它们上报不了自己是谁）⇒ `legacy`：标题含 `ACETool`。
//
// ⚠️ 一段里匹配到多个时（同一页开了两个窗口、或那个浏览器窗口里恰好有个同名标签）
// **优先前台那个**：用户点 📌 的那一下必然让装着这一页的窗口成为前台（`picked=foreground`）；
// 连前台都对不上才取枚举到的第一个（`picked=first`），并把候选个数报出去，
// 让按钮的悬停提示如实警告"同名窗口有 N 个"。
func pickPanelWindow(title string) (win.Window, int, string, string, error) {
	if strings.TrimSpace(title) == "" {
		// ⚠️ 用**前缀**而不是子串：子串会被 exe 自己控制台那条路径里的 `acetool` 命中
		// （`win.Find` 那边也剔了，这是第二道）。浏览器窗口标题一定**以页面标题开头**。
		//
		// ⚠️ 2026-10-17：主页面标题按用户口径改成「ACE 工具箱」（不再带 `ACETool`），
		// 而其余页面仍是「ACETool · 设置」「ACETool · 查找」… ⇒ 这条老路**两个前缀都试**，
		// 先认主页面那个（用户点 📌 时多半在看它），再退回 `ACETool`。
		var ws []win.Window
		for _, pre := range []string{homeTitle, "ACETool"} {
			got, err := win.FindPrefixed(pre)
			if err != nil {
				return win.Window{}, 0, "legacy", "", err
			}
			ws = append(ws, got...)
		}
		if len(ws) == 0 {
			return win.Window{}, 0, "legacy", "", errNoPanelWindow("")
		}
		w, how := pickFrom(ws)
		return w, len(ws), "legacy", how, nil
	}
	type stage struct {
		kind string
		fn   func(string) ([]win.Window, error)
	}
	for _, s := range []stage{
		{"exact", win.FindExact},
		{"prefix", win.FindPrefixed},
		{"contains", win.Find},
	} {
		ws, err := s.fn(title)
		if err != nil {
			return win.Window{}, 0, s.kind, "", err
		}
		if len(ws) == 0 {
			continue
		}
		w, how := pickFrom(ws)
		return w, len(ws), s.kind, how, nil
	}
	return win.Window{}, 0, "contains", "", errNoPanelWindow(title)
}

// pickFrom 在候选里挑一个：**前台优先**，否则第一个。
func pickFrom(ws []win.Window) (win.Window, string) {
	if fg := win.Foreground(); fg != 0 {
		for _, w := range ws {
			if w.Handle == fg {
				return w, "foreground"
			}
		}
	}
	return ws[0], "first"
}

// errNoPanelWindow 把"没找到"说成人能照着做的话，并**分清**是哪条路找的。
func errNoPanelWindow(title string) error {
	if strings.TrimSpace(title) == "" {
		return fmt.Errorf("没找到标题里含 `ACETool` 的窗口 —— " +
			"浏览器窗口标题跟的是**当前标签页**，把面板那个标签切到前台再点一次")
	}
	return fmt.Errorf("没找到标题为「%s」的窗口 —— 浏览器窗口标题跟的是**当前标签页**，"+
		"把这个面板窗口点到前台再点一次", title)
}

func (st *panelState) setTopMost(on bool, title string) (onTopView, error) {
	w, n, kind, how, err := pickPanelWindow(title)
	if err != nil {
		return onTopView{Want: on, Match: kind, Picked: how, WindowMatches: n}, err
	}
	// 读回来核对（不回读就等于"我说置顶了"）
	if err := win.SetTopMost(w.Handle, on); err != nil {
		return onTopView{Want: on, Match: kind, Picked: how, WindowMatches: n,
			WindowTitle: w.Title}, err
	}
	st.saveOnTop(on)
	st.mu.Lock()
	st.onTop = win.IsTopMost(w.Handle)
	st.mu.Unlock()
	return onTopView{OnTop: st.onTop, WindowTitle: w.Title, WindowMatches: n,
		Match: kind, Picked: how, Want: on, Supported: true}, nil
}

func (st *panelState) toggleTopMost(title string) (onTopView, error) {
	if v, err := st.currentTopMost(title); err == nil {
		return st.setTopMost(!v.OnTop, title)
	}
	// 问不出来（窗口没找到）⇒ 按配置里的意图取反
	cfg, _ := config.Load()
	want := true
	if cfg != nil {
		want = !cfg.PanelOnTop
	}
	return st.setTopMost(want, title)
}

// currentTopMost 读**实测**状态（不是配置里的意图）：窗口找不到就报错，让上层如实说。
func (st *panelState) currentTopMost(title string) (onTopView, error) {
	w, n, kind, how, err := pickPanelWindow(title)
	if err != nil {
		return onTopView{Match: kind, Picked: how, WindowMatches: n}, err
	}
	cfg, _ := config.Load()
	want := cfg != nil && cfg.PanelOnTop
	return onTopView{
		OnTop: win.IsTopMost(w.Handle), WindowTitle: w.Title,
		WindowMatches: n, Match: kind, Picked: how, Want: want, Supported: true,
	}, nil
}

func (st *panelState) saveOnTop(on bool) {
	cfg, _ := config.Load()
	if cfg == nil {
		return
	}
	cfg.PanelOnTop = on
	_ = cfg.Save()
}

// ---------- 路由 ----------

// registerPanelRoutes 挂上"面板自己"的那几个接口（不动工具那三条）。
func (st *panelState) registerPanelRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/shortcuts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, st.shortcutsView())
			return
		}
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "只接受 GET / POST")
			return
		}
		if r.Header.Get(apiHeader) == "" {
			writeAPIError(w, http.StatusForbidden, "缺 "+apiHeader+" 头")
			return
		}
		var req struct {
			Bindings map[string]config.ShortcutBinding `json:"bindings"`
			OnTop    *bool                             `json:"panelOnTop,omitempty"`
		}
		body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
		if err := json.NewDecoder(body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "请求体不是合法 JSON："+err.Error())
			return
		}
		// ① 逐条校验 —— **有错就一条都不存**（半存半不存最难查）
		errs := map[string]string{}
		cleaned := hotkeys.Bindings{}
		for id, b := range req.Bindings {
			act, known := st.actions[id]
			if !known {
				errs[id] = "这个动作 id 不认识（绑定表比动作表旧？）"
				continue
			}
			// ⚠️ 主页面那 20 条（`AlwaysGlobal`）**只能**系统级：键要在 ACE 里干活时按响，
			// 而"点按钮"只能由页面做 ⇒ exe 收热键后发事件。前端没勾「全局」也按 true 处理
			// （设置页那边勾是锁住的；这条是服务端的第二道，别让手写请求绕出"绑了但永远不响"）。
			if act.AlwaysGlobal {
				b.Global = true
			}
			nk, err := hotkeys.Validate(b.Key, b.Global)
			if err != nil {
				errs[id] = err.Error()
				continue
			}
			cleaned[id] = hotkeys.Binding{Key: nk, Global: b.Global}
		}
		if len(errs) > 0 {
			writeJSON(w, struct {
				OK     bool              `json:"ok"`
				Errors map[string]string `json:"errors"`
				Hint   string            `json:"hint"`
			}{false, errs, "有键位不合法 ⇒ **一条都没存**（改完再存）"})
			return
		}
		// ② 存 + 重新注册
		if err := hotkeys.Save(cleaned); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "写入配置失败："+err.Error())
			return
		}
		st.mu.Lock()
		st.bindings = cleaned
		st.registered = st.manager.Apply(cleaned)
		reg := append([]hotkeys.Result(nil), st.registered...)
		st.mu.Unlock()
		if req.OnTop != nil {
			if _, err := st.setTopMost(*req.OnTop, ""); err != nil {
				st.push("bad", "panel.ontop", "置顶设置没成功："+err.Error())
			}
		}
		view := st.shortcutsView()
		view.Registered = reg
		writeJSON(w, view)
	})

	// ⭐ 设置页（`settings.html` 的「工具」那一页）——**唯一**能改 exe 自己配置的入口（2026-10-17 加）。
	//
	// GET  ⇒ 现状：配置里的 CLI / **此刻在用**的 CLI / 自动探测到的 CLI（三个值分开报，
	//        这样页面才能把"配置写了但它不生效"讲清楚）、超时、侧栏随启动、面板地址与配置文件路径。
	// POST ⇒ 只认这三个键 `{cliPath?, timeoutSec?, railOnBoot?}`：
	//        **白名单 + 逐个校验，有错就一条都不存**（半存半不存最难查，跟快捷键那边同一口径）。
	//        改完**热应用**（CLI 路径与超时直接改正在用的那个 client；不用重启）。
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		cfg, cerr := config.Load()
		if r.Method == http.MethodGet {
			detected, from := acecli.Locate("") // 只看"机器上有没有/在哪"，不看配置
			writeJSON(w, settingsViewOf(cfg, cerr, st, detected, from))
			return
		}
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "只接受 GET / POST")
			return
		}
		if r.Header.Get(apiHeader) == "" {
			writeAPIError(w, http.StatusForbidden, "缺 "+apiHeader+" 头")
			return
		}
		var req struct {
			CLIPath    *string `json:"cliPath"`
			TimeoutSec *int    `json:"timeoutSec"`
			RailOnBoot *bool   `json:"railOnBoot"`
			// AutoStart = 开机启动（写 HKCU 的 Run 键，见 `internal/autostart`）。
			// ⚠️ 它**不进 config.json**：注册表就是唯一事实源（用户可能在任务管理器里关掉它）。
			AutoStart *bool `json:"autoStart"`
		}
		body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
		if err := json.NewDecoder(body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "请求体不是合法 JSON："+err.Error())
			return
		}
		// ① 校验（一条不对就都不动）
		if req.TimeoutSec != nil {
			if v := *req.TimeoutSec; v < 1 || v > 120 {
				writeAPIError(w, http.StatusBadRequest,
					fmt.Sprintf("命令超时要 1–120 秒（给的是 %d）—— 太短会误杀慢命令，太长卡住时等不起", v))
				return
			}
		}
		if req.CLIPath != nil {
			if p := strings.TrimSpace(*req.CLIPath); p != "" {
				if _, err := os.Stat(p); err != nil {
					writeAPIError(w, http.StatusBadRequest,
						"这个路径不存在："+p+" —— 留空表示「交给自动探测」")
					return
				}
				if !strings.HasSuffix(strings.ToLower(p), ".exe") {
					writeAPIError(w, http.StatusBadRequest, "要指向 acestudio-cli 的可执行文件（.exe）："+p)
					return
				}
			}
		}
		// ② 存 + 热应用
		var applied []string
		if req.TimeoutSec != nil {
			cfg.TimeoutSec = *req.TimeoutSec
			st.client.Timeout = time.Duration(*req.TimeoutSec) * time.Second // 正在用的那个 client（下次调用生效）
			applied = append(applied, fmt.Sprintf("超时 %d 秒", *req.TimeoutSec))
		}
		if req.CLIPath != nil {
			p := strings.TrimSpace(*req.CLIPath)
			cfg.CLIPath = p
			if p == "" {
				// 留空 = 回到自动探测
				if auto, _ := acecli.Locate(""); auto != "" {
					p = auto
				}
			}
			if p != "" {
				st.client.Path = p
			}
			applied = append(applied, "CLI 路径")
		}
		if req.RailOnBoot != nil {
			cfg.RailOnBoot = *req.RailOnBoot
			applied = append(applied, "侧栏随面板启动："+map[bool]string{true: "开", false: "关"}[*req.RailOnBoot]+"（下次启动生效）")
		}
		if err := cfg.Save(); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "写入配置失败："+err.Error())
			return
		}
		// ③ 开机启动：**不写 config.json**，直接动注册表（HKCU\...\Run\ACETool）。
		// 失败要如实说（权限/杀软拦着都可能），不能假装开好了。
		if req.AutoStart != nil {
			if _, err := autostart.Set(*req.AutoStart); err != nil {
				writeAPIError(w, http.StatusInternalServerError,
					"开机启动没设置成功："+err.Error())
				return
			}
			applied = append(applied, "开机启动："+map[bool]string{true: "开（登录时后台起，不弹窗口）", false: "关"}[*req.AutoStart])
		}
		if len(applied) == 0 {
			writeAPIError(w, http.StatusBadRequest, "没给任何要改的键（cliPath / timeoutSec / railOnBoot / autoStart）")
			return
		}
		detected, from := acecli.Locate("")
		v := settingsViewOf(cfg, nil, st, detected, from)
		v.Note = "已保存：" + strings.Join(applied, " · ")
		writeJSON(w, v)
	})

	// 侧条状态（exe 侧那条贴 ACE 窗口的汉字细条，见 rail.go / internal/rail）：
	// 自检的抓手 —— "它现在贴着哪个窗口、多高、展开了没、有没有报错"都得能从外面问出来。
	// 设置页（§69 执行顺序第 ⑤ 步）以后也读它。
	//
	// POST `{enable:true|false}` = **运行时起停**（用户 2026-10-16：「做一个 web 和侧栏的切换按钮就行了」）。
	// ⚠️ 起侧条现在带着 §72 那条未解决问题的风险（建了它 exe 会自己退出），如实告知、不假装没事。
	mux.HandleFunc("/api/rail", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if r.Header.Get(apiHeader) == "" {
				writeAPIError(w, http.StatusForbidden, "缺 "+apiHeader+" 头")
				return
			}
			var req struct {
				Enable *bool `json:"enable"`
			}
			body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
			if err := json.NewDecoder(body).Decode(&req); err != nil {
				writeAPIError(w, http.StatusBadRequest, "请求体不是合法 JSON："+err.Error())
				return
			}
			if req.Enable == nil {
				writeAPIError(w, http.StatusBadRequest, "要 `{enable:true|false}`")
				return
			}
			msg, err := toggleRail(st, *req.Enable)
			if err != nil {
				writeJSON(w, struct {
					OK     bool   `json:"ok"`
					Enable bool   `json:"enable"`
					Error  string `json:"error"`
				}{false, *req.Enable, err.Error()})
				return
			}
			running := theRail != nil
			out := struct {
				OK      bool   `json:"ok"`
				Enable  bool   `json:"enable"`
				Running bool   `json:"running"`
				Message string `json:"message"`
				Warn    string `json:"warn,omitempty"`
			}{true, *req.Enable, running, msg, ""}
			if *req.Enable {
				out.Warn = "侧条一起来，exe 有可能自己退出（HANDOVER §72 那个未解决的问题）；ACE 开着时一般没事"
			}
			writeJSON(w, out)
			return
		}
		if r.Method != http.MethodGet {
			writeAPIError(w, http.StatusMethodNotAllowed, "只接受 GET / POST")
			return
		}
		if theRail == nil {
			writeJSON(w, struct {
				Running bool   `json:"running"`
				Items   int    `json:"items"`
				Error   string `json:"error,omitempty"`
			}{false, len(rail.Items()), "细条没起来（`-no-rail` 起的面板，或起的时候报错了 —— 看 exe 控制台）"})
			return
		}
		writeJSON(w, theRail.State())
	})

	// 贴边吸附（用户 2026-10-16：「把窗口贴到四周的时候自动吸附」）——
	// 动的是**别人进程的窗口**（浏览器 app 窗口），实现见 `internal/dock`（只轮询 + SetWindowPos，
	// 不建窗口、不接消息 ⇒ 跟细条那条"会让 exe 自己退出"的路完全无关）。
	// GET ⇒ 此刻状态；POST `{title, enable}` ⇒ 起/停。
	mux.HandleFunc("/api/dock", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if theDock == nil {
				writeJSON(w, struct {
					Running bool   `json:"running"`
					Hint    string `json:"hint,omitempty"`
				}{false, "还没起过吸附（POST 一个 title 就能起）"})
				return
			}
			writeJSON(w, theDock.State())
			return
		}
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "只接受 GET / POST")
			return
		}
		if r.Header.Get(apiHeader) == "" {
			writeAPIError(w, http.StatusForbidden, "缺 "+apiHeader+" 头")
			return
		}
		var req struct {
			Title  string `json:"title"`
			Enable bool   `json:"enable"`
		}
		body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
		if err := json.NewDecoder(body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "请求体不是合法 JSON："+err.Error())
			return
		}
		if err := setDock(req.Title, req.Enable); err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		if theDock == nil {
			writeJSON(w, struct {
				Running bool `json:"running"`
			}{false})
			return
		}
		writeJSON(w, theDock.State())
	})

	// ⛔ **只给自检用**：注入一个假光标位置，让三级（特别细 / 两个字 / 栏外全名）能被**确定性**验证。
	// 为什么需要它：三级判定全靠"光标离右缘多近"，而真机自检**不能去抢用户的鼠标** ——
	// 实测脚本把光标放过去，150ms 后就被用户真实的手覆盖了（于是永远验不到第②③级）。
	mux.HandleFunc("/api/rail/cursor", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "只接受 POST")
			return
		}
		if r.Header.Get(apiHeader) == "" {
			writeAPIError(w, http.StatusForbidden, "缺 "+apiHeader+" 头")
			return
		}
		if theRail == nil {
			writeAPIError(w, http.StatusConflict, "细条没起来")
			return
		}
		var req struct {
			X     int  `json:"x"`
			Y     int  `json:"y"`
			Clear bool `json:"clear"`
		}
		body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
		if err := json.NewDecoder(body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "请求体不是合法 JSON："+err.Error())
			return
		}
		if req.Clear {
			theRail.SetCursorOverride(-1, -1)
		} else {
			theRail.SetCursorOverride(req.X, req.Y)
		}
		writeJSON(w, theRail.State())
	})

	// 系统托盘图标：`GET` 报现场（**只读**），`POST {action}` 触发托盘菜单里那三个动作。
	//
	// 为什么要这个接口：
	//   · `GET` —— 托盘图标不在我们的页面里（在 Windows 通知区域），光看 exe 日志只能知道
	//     "调了 NIM_ADD"，看不到 Shell 有没有收下；这能把 `added`/图标尺寸/悬停文案报出来，
	//     自检脚本据此断言它确实是活的。
	//   · `POST` —— 菜单那三个动作与这里**共用同一份实现**（`trayActions`，见 main.go）：
	//     自检要验"退出真的能干净退出"时，总不能靠合成点击去点用户的托盘菜单。
	//     ⚠️ `quit` 必须带 `confirm:true`（别的动作不用）—— 少一个笔误就把面板关了太蠢。
	// ⭐ 用户 2026-10-21：「手动路径应该允许外部拖动或浏览」——
	// 浏览器**永远不给网页本地文件真实路径**（`<input type=file>` 只有 File 对象），
	// 所以"浏览"必须由 exe 弹系统对话框。拖动那边同理：拿到的是文件名而不是路径。
	mux.HandleFunc("/api/pick-file", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "只接受 POST")
			return
		}
		if r.Header.Get(apiHeader) == "" {
			writeAPIError(w, http.StatusForbidden, "缺 "+apiHeader+" 头")
			return
		}
		var req struct {
			Title string `json:"title"`
		}
		body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
		_ = json.NewDecoder(body).Decode(&req)
		p, ok := win.PickFile(req.Title)
		writeJSON(w, struct {
			OK       bool   `json:"ok"`
			Path     string `json:"path,omitempty"`
			Canceled bool   `json:"canceled,omitempty"`
		}{true, p, !ok})
	})

	// ⭐ 热键在 exe 里直接跑的那一批：页面拿不到回包 ⇒ 从这里取"最近一批可撤回的"。
	mux.HandleFunc("/api/undo", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		mark := st.lastUndo
		st.mu.Unlock()
		writeJSON(w, struct {
			Undo *undoMark `json:"undo,omitempty"`
		}{mark})
	})

	mux.HandleFunc("/api/layout", func(w http.ResponseWriter, r *http.Request) {
		cfg, _ := config.Load()
		if r.Method == http.MethodGet {
			ents := rail.LayoutEntries()
			hid := map[string]bool{}
			for _, k := range cfg.LayoutHidden {
				hid[k] = true
			}
			type row struct {
				Key           string `json:"key"`
				Name          string `json:"name"`
				Hidden        bool   `json:"hidden"`
				DefaultHidden bool   `json:"defaultHidden,omitempty"`
			}
			rows := make([]row, 0, len(ents))
			known := make([]string, 0, len(ents))
			shown := map[string]bool{}
			for _, k := range cfg.LayoutShown {
				shown[k] = true
			}
			for _, e := range ents {
				h := hid[e.Key] || (e.DefaultHidden && !shown[e.Key])
				rows = append(rows, row{e.Key, e.Name, h, e.DefaultHidden})
				known = append(known, e.Key)
			}
			// ⚠️ 用户没保存过顺序时**不要**把内置顺序当 order 发出去：
			// 主面板/侧栏各有各的默认（自己按 QUICK_PREF / railPref 排），发内置顺序会顶掉默认顺序
			// （2026-10-24 实测：主面板第一颗变成「和声」、删词/合并被挤到最后）。
			orderOut := cfg.LayoutOrder
			if len(orderOut) > 0 {
				orderOut = layout.Arrange(known, cfg.LayoutOrder, nil)
			}
			writeJSON(w, struct {
				Items  []row    `json:"items"`
				Order  []string `json:"order"`
				Hidden []string `json:"hidden"`
			}{rows, orderOut, cfg.LayoutHidden})
			return
		}
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "只接受 GET / POST")
			return
		}
		if r.Header.Get(apiHeader) == "" {
			writeAPIError(w, http.StatusForbidden, "缺 "+apiHeader+" 头")
			return
		}
		var req struct {
			Order  []string `json:"order"`
			Hidden []string `json:"hidden"`
			Shown  []string `json:"shown"`
		}
		body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
		if err := json.NewDecoder(body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "请求体不是合法 JSON："+err.Error())
			return
		}
		known := rail.LayoutKeys()
		// ⛔ 不认识的 id **当场拒**（静默丢会让用户排好的顺序下次又变回去，最难查）
		if bad := layout.Unknown(req.Order, req.Hidden, known); len(bad) > 0 {
			writeAPIError(w, http.StatusBadRequest,
				"这些按钮不在清单里（可能已经被删了）："+strings.Join(bad, "、"))
			return
		}
		cfg.LayoutOrder = layout.Clean(req.Order)
		cfg.LayoutHidden = layout.Clean(req.Hidden)
		cfg.LayoutShown = layout.Clean(req.Shown)
		if err := cfg.Save(); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "写入配置失败："+err.Error())
			return
		}
		rail.SetLayout(cfg.LayoutOrder, cfg.LayoutHidden, cfg.LayoutShown)
		rail.Reload() // 正在跑的侧栏立刻按新顺序/显隐重画（用户 2026-10-20：改完设置就生效）
		// ⭐ 2026-10-20：推一条事件 ⇒ 已开着的页面（主面板）**就地重排**，不用刷新。
		st.push("layout", "", "按钮顺序/显示已更新")
		writeJSON(w, struct {
			OK     bool     `json:"ok"`
			Order  []string `json:"order"`
			Hidden []string `json:"hidden"`
			Note   string   `json:"note"`
		}{true, cfg.LayoutOrder, cfg.LayoutHidden, "已保存（侧栏重启后会立即按新顺序画）"})
	})

	mux.HandleFunc("/api/tray", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if theTray == nil {
				writeJSON(w, struct {
					Running bool   `json:"running"`
					Note    string `json:"note"`
				}{false, "这次启动没起托盘（-no-tray，或起失败了，见日志）"})
				return
			}
			writeJSON(w, theTray.Info())
			return
		}
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "只接受 GET / POST")
			return
		}
		if r.Header.Get(apiHeader) == "" {
			writeAPIError(w, http.StatusForbidden, "缺 "+apiHeader+" 头")
			return
		}
		var req struct {
			Action  string `json:"action"`
			Confirm bool   `json:"confirm"`
		}
		body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
		if err := json.NewDecoder(body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "请求体不是合法 JSON："+err.Error())
			return
		}
		reply := func(note string) {
			writeJSON(w, struct {
				OK     bool   `json:"ok"`
				Action string `json:"action"`
				Note   string `json:"note"`
			}{true, req.Action, note})
		}
		switch req.Action {
		case "open":
			if trayActions.Open == nil {
				writeAPIError(w, http.StatusConflict, "托盘没起来（没有这个动作）")
				return
			}
			go trayActions.Open()
			reply("已在后台打开/切到面板窗口")
		case "settings":
			if trayActions.Settings == nil {
				writeAPIError(w, http.StatusConflict, "托盘没起来（没有这个动作）")
				return
			}
			go trayActions.Settings()
			reply("已在后台打开设置页")
		case "quit":
			if trayActions.Quit == nil {
				writeAPIError(w, http.StatusConflict, "托盘没起来（没有这个动作）")
				return
			}
			if !req.Confirm {
				writeAPIError(w, http.StatusBadRequest, "退出要带 confirm:true —— 免得一次笔误把面板关了")
				return
			}
			reply("正在退出（先收细条/托盘，再关服务；exit.log 里会有记录）")
			go trayActions.Quit()
		default:
			writeAPIError(w, http.StatusBadRequest, "action 只认 open / settings / quit")
		}
	})

	// 窄栏页（`rail.html`）要的那份清单 —— **单一事实源在 `internal/rail/list.go`**
	// （19 项 / 简称 / 全称 / 三类），页面只负责画，不自己抄一份。
	mux.HandleFunc("/api/rail/items", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, http.StatusMethodNotAllowed, "只接受 GET")
			return
		}
		writeJSON(w, struct {
			Items []rail.Item `json:"items"`
		}{rail.Items()})
	})

	// 「单独显示 web 的窗口」（用户 2026-10-16）——
	// 用**浏览器 app 模式**开：没有标签栏/地址栏，看着就是个独立窗口。
	// 选型见 `internal/win/appwindow_windows.go`（零依赖；不引 WebView2 的理由也在那儿）。
	// POST `{page:"home.html", x,y,w,h}`（都可省：默认 home.html + 该页在尺寸表里的值，
	// 位置交给浏览器自己记）；`x/y` 给了才挪位置。
	// ⭐ 尺寸口径（用户 2026-10-17：「把所有打开的面板隐藏地址栏，把窗口做小，大概500px宽左右的合适大小」
	//    +「500只是大概，按你的数据来」）：**单位是 DIP（逻辑像素）**，表在 `internal/web/panelsize.go`，
	//   每页把原本调好的尺寸收成一行；150% 屏上 500 逻辑 = 750 物理。
	//   要的是"小窗"，不是"又一个全屏窗口"—— 所以别再各自传尺寸进来。
	mux.HandleFunc("/api/app-window", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "只接受 POST")
			return
		}
		if r.Header.Get(apiHeader) == "" {
			writeAPIError(w, http.StatusForbidden, "缺 "+apiHeader+" 头")
			return
		}
		var req struct {
			Page string `json:"page"`
			X    int    `json:"x"`
			Y    int    `json:"y"`
			W    int    `json:"w"`
			H    int    `json:"h"`
		}
		body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
		_ = json.NewDecoder(body).Decode(&req) // 空体也要能用（全默认）
		page := req.Page
		if page == "" {
			page = "home.html"
		}
		// ⛔ 只允许开**面板自己的页**：别把这个接口变成"用浏览器打开任意 URL"的口子
		if strings.ContainsAny(page, `/\:`) || !strings.HasSuffix(page, ".html") {
			writeAPIError(w, http.StatusBadRequest, "只认面板自己的页面名（如 home.html / find.html）")
			return
		}
		// 尺寸：页面**自己**的尺寸表（`internal/web/panelsize.go`）说了算；调用方显式给 W/H 才覆盖。
		// ⚠️ 单位是 **DIP（逻辑像素）** —— 实测 `--window-size=500,780` 在 150% 屏上开出 750×1170 物理。
		defW, defH := web.PanelSize(page)
		wpx, hpx := req.W, req.H
		if wpx <= 0 {
			wpx = defW
		}
		if hpx <= 0 {
			hpx = defH
		}
		x, y := -1, -1
		if req.X != 0 || req.Y != 0 {
			x, y = req.X, req.Y
		}
		full := strings.TrimRight(thePanelURL, "/") + "/" + page
		// 开窗**之前**先记一份同类窗口的句柄快照 ⇒ 之后只认"新出现的那个"（免得关错窗口）
		before := appWinSnapshot(dockTitleOf(page))
		exe, appMode, sized, err := win.AppWindow(full, x, y, wpx, hpx)
		out := struct {
			OK      bool   `json:"ok"`
			URL     string `json:"url"`
			Browser string `json:"browser,omitempty"`
			AppMode bool   `json:"appMode"`
			Sized   bool   `json:"sized"`
			W       int    `json:"w"`
			H       int    `json:"h"`
			Hint    string `json:"hint,omitempty"`
			Error   string `json:"error,omitempty"`
		}{URL: full, Browser: exe, AppMode: appMode, Sized: sized, W: wpx, H: hpx}
		if err != nil {
			out.Error = err.Error()
			writeJSON(w, out)
			return
		}
		out.OK = true
		rememberAppWin(dockTitleOf(page), before)
		if !appMode {
			out.Hint = "这个浏览器不认 `--app` ⇒ 开的是**普通窗口**（有标签栏/地址栏）"
		} else if !sized {
			// 尺寸没摆上也要说：Edge 会用"记住的上次尺寸"，用户会以为尺寸设置没生效
			out.Hint = fmt.Sprintf("窗口开了，但没赶上定尺寸（Edge 会记住 app 窗口上次的大小）——"+
				"想要 %d×%d 就再点一次，或先把那个窗口关掉", wpx, hpx)
		}
		writeJSON(w, out)
	})

	// 置顶开关（每个页面顶栏那个 📌 按钮）
	//
	// ⭐ 按窗口（用户 2026-10-16 选的口径）：页面 POST `{title: document.title}` ⇒ 只动那个窗口；
	// GET 带 `?title=` ⇒ 读那个窗口自己的状态（每个窗口的 📌 各亮各的）。
	// 不带 title 的老调用（老壳页 / 设置页 / 系统级热键）保持原样：标题含 `ACETool`。
	mux.HandleFunc("/api/panel/ontop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			v, err := st.currentTopMost(r.URL.Query().Get("title"))
			if err != nil {
				cfg, _ := config.Load()
				v.Want = cfg != nil && cfg.PanelOnTop
				v.Supported = runtime.GOOS == "windows"
				writeJSON(w, struct {
					onTopView
					Error string `json:"error,omitempty"`
				}{v, err.Error()})
				return
			}
			writeJSON(w, v)
			return
		}
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "只接受 GET / POST")
			return
		}
		if r.Header.Get(apiHeader) == "" {
			writeAPIError(w, http.StatusForbidden, "缺 "+apiHeader+" 头")
			return
		}
		var req struct {
			On    *bool  `json:"on"`
			Title string `json:"title"`
		}
		body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
		if err := json.NewDecoder(body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "请求体不是合法 JSON："+err.Error())
			return
		}
		var v onTopView
		var err error
		if req.On == nil {
			v, err = st.toggleTopMost(req.Title)
		} else {
			v, err = st.setTopMost(*req.On, req.Title)
		}
		if err != nil {
			writeJSON(w, struct {
				onTopView
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}{v, false, err.Error()})
			return
		}
		writeJSON(w, struct {
			onTopView
			OK bool `json:"ok"`
		}{v, true})
	})

	// 事件：系统级热键的结果从这里回页面。
	//
	// ⭐ 语义（多窗口友好，用户 2026-10-05 选的做法）：
	//   · 每条事件有单调递增的 `Seq`；客户端带 `?since=N` 来取 `Seq > N` 的那些；
	//   · **不带 `since` = "从此刻起"**（只回 `lastSeq`、不回事件）—— 新开的窗口因此
	//     不会把历史事件当新提示刷一屏，而它拿到 `lastSeq` 之后就能正常增量取；
	//   · 事件**不被取走**（留在环形缓冲里）⇒ 开两个窗口，两边都能收到同一条。
	//   · 缓冲会淘汰：客户端发现 `oldestSeq > since+1` 就知道自己漏了，跳到 `lastSeq` 即可（如实告知）。
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		since := int64(-1)
		if s := strings.TrimSpace(r.URL.Query().Get("since")); s != "" {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				since = n
			}
		}
		st.mu.Lock()
		all := append([]panelEvent(nil), st.events...)
		lastSeq, oldest := st.eventSeq, int64(0)
		if len(st.events) > 0 {
			oldest = st.events[0].Seq
		}
		lr := st.lastRun
		fired, lastFired, lastFiredAt := st.firedCount, st.lastFired, st.lastFiredAt
		st.mu.Unlock()

		out := []panelEvent{}
		missed := int64(0)
		if since >= 0 {
			if len(all) > 0 && since+1 < oldest {
				missed = oldest - since - 1 // 已经被环形淘汰掉的那几条
			}
			for _, e := range all {
				if e.Seq > since {
					out = append(out, e)
				}
			}
		}
		writeJSON(w, struct {
			Events  []panelEvent  `json:"events"`
			LastSeq int64         `json:"lastSeq"`
			Oldest  int64         `json:"oldestSeq"`
			Missed  int64         `json:"missed,omitempty"`
			LastRun *lastRunState `json:"lastRun,omitempty"`
			// 只增不清的触发计数（证据）+ 最近一次
			FiredCount  int    `json:"firedCount"`
			LastFired   string `json:"lastFired,omitempty"`
			LastFiredAt string `json:"lastFiredAt,omitempty"`
		}{out, lastSeq, oldest, missed, lr, fired, lastFired, lastFiredAtStr(lastFiredAt)})
	})

	// 存活探针：给"单实例守卫"用（另一个 exe 启动时问一句"这儿是不是已经有个面板"）。
	// ⚠️ 刻意**不做成 selfcheck**：那条要跑两条 CLI 命令（慢），守卫要的是毫秒级回答。
	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		vp := st.viewport
		st.mu.Unlock()
		writeJSON(w, struct {
			App       string `json:"app"`
			PID       int    `json:"pid"`
			StartedAt string `json:"startedAt"`
			Tools     string `json:"tools"`
			LastSeq   int64  `json:"lastSeq"`
			Viewport  string `json:"viewport,omitempty"`
		}{"acetool", os.Getpid(), st.startedAt.Format(time.RFC3339), tools.Summary(), st.eventSeq, vp})
	})

	// 世代门：`POST /api/gate {gen}` 推进世代（"开始/停止"各调一次）；
	// 排队的旧世代请求到了 `executeTool` 会被跳过（见那里的注释）—— 这是"请求已发出去也能叫停"的做法。
	mux.HandleFunc("/api/gate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			st.mu.Lock()
			g := st.gateGen
			st.mu.Unlock()
			writeJSON(w, map[string]int64{"gen": g})
			return
		}
		if r.Header.Get(apiHeader) == "" {
			writeAPIError(w, http.StatusForbidden, "缺 "+apiHeader+" 头")
			return
		}
		var req struct {
			Gen int64 `json:"gen"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "请求体不是合法 JSON："+err.Error())
			return
		}
		st.mu.Lock()
		st.gateGen = req.Gen
		st.mu.Unlock()
		writeJSON(w, map[string]any{"ok": true, "gen": req.Gen})
	})

	// 窗口大小上报（页面 `window.innerWidth/Height`）——"按现在这个尺寸做版式"要用它。
	// 存内存 + 落 config（重启后还知道上次多大）。
	mux.HandleFunc("/api/viewport", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			st.mu.Lock()
			vp := st.viewport
			st.mu.Unlock()
			writeJSON(w, map[string]string{"viewport": vp})
			return
		}
		if r.Header.Get(apiHeader) == "" {
			writeAPIError(w, http.StatusForbidden, "缺 "+apiHeader+" 头")
			return
		}
		var req struct {
			W  int `json:"w"`
			H  int `json:"h"`
			OW int `json:"ow"`
			OH int `json:"oh"`
			SW int `json:"sw"`
			SH int `json:"sh"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "请求体不是合法 JSON："+err.Error())
			return
		}
		// 主口径是 **Edge 窗口大小**（outer）；inner/screen 一并记下，免得再来回问
		vp := fmt.Sprintf("edge %dx%d · 可视区 %dx%d · 屏幕 %dx%d", req.OW, req.OH, req.W, req.H, req.SW, req.SH)
		st.mu.Lock()
		st.viewport = vp
		st.mu.Unlock()
		if cfg, err := config.Load(); err == nil {
			cfg.LastViewport = vp
			_ = cfg.Save()
		}
		writeJSON(w, map[string]any{"ok": true, "viewport": vp})
	})
}

// lastFiredAtStr 只在有记录时给时间串（空时间会显示成 0001 年，很难看）。
func lastFiredAtStr(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("15:04:05")
}

type shortcutsView struct {
	Actions    []hotkeys.Action `json:"actions"`
	Bindings   hotkeys.Bindings `json:"bindings"`
	Defaults   hotkeys.Bindings `json:"defaults"`
	Registered []hotkeys.Result `json:"registered"`
	Conflicts  []string         `json:"conflicts,omitempty"`
	Warnings   []string         `json:"warnings,omitempty"`
	Platform   string           `json:"platform"`
	Windows    bool             `json:"windows"`
	OnTop      onTopView        `json:"onTop"`
}

func (st *panelState) shortcutsView() shortcutsView {
	st.mu.Lock()
	b := hotkeys.Bindings{}
	for k, v := range st.bindings {
		b[k] = v
	}
	reg := append([]hotkeys.Result(nil), st.registered...)
	warns := append([]string(nil), st.loadWarns...)
	st.mu.Unlock()

	acts := make([]hotkeys.Action, 0, len(st.order))
	for _, id := range st.order {
		acts = append(acts, st.actions[id])
	}
	// ⚠️ **不排序**：`st.order` 就是目录里的显示顺序（设置页的分组顺序靠它）。
	// 曾在这里写过一个恒假的 sort 当"声明"，纯噪音，删掉。

	view := shortcutsView{
		Actions: acts, Bindings: b, Defaults: st.defaults, Registered: reg,
		Conflicts: hotkeys.Conflicts(b, st.actions), Warnings: warns,
		Platform: runtime.GOOS, Windows: runtime.GOOS == "windows",
	}
	// ⚠️ 2026-10-17：这里原来还有一条"系统级热键当前会跑「X」（面板上最后一次执行）」——
	// 那是给 `run.primary` / `run.dry`（重放最后一次运行）配的说明。那两条已随
	// 「只留我说的那几个快捷键」删掉 ⇒ 再显示就是假话，去掉（`lastRun` 本身还在 `/api/events` 里
	// 给页面看"最后一次跑了什么"，只是没有热键会重放它了）。
	if v, err := st.currentTopMost(""); err == nil {
		view.OnTop = v
	} else {
		cfg, _ := config.Load()
		view.OnTop = onTopView{Want: cfg != nil && cfg.PanelOnTop, Supported: runtime.GOOS == "windows"}
	}
	return view
}

// settingsView 是 `GET/POST /api/settings` 的回包（设置页「工具」那一页就按它渲染）。
type settingsView struct {
	OK bool `json:"ok"`
	// 三个 CLI 值**分开报**：配置里写的 / 此刻在用的 / 自动探测到的。
	// 为什么要三个：配置写了个失效路径时，面板会继续用自动探测到的那个，
	// 只看一个值就分不清"你以为设的"和"实际在用的"（HANDOVER §2 踩过这个假象）。
	CLIPath      string `json:"cliPath"`
	UsingCLI     string `json:"usingCli"`
	DetectedCLI  string `json:"detectedCli,omitempty"`
	DetectedFrom string `json:"detectedFrom,omitempty"`
	TimeoutSec   int    `json:"timeoutSec"`
	RailOnBoot   bool   `json:"railOnBoot"`
	RailRunning  bool   `json:"railRunning"`
	// AutoStart 是「开机启动」的现场（注册表里那条 + 现在这个 exe 的对比）。
	AutoStart  autostart.State `json:"autoStart"`
	PanelURL   string          `json:"panelUrl"`
	ConfigPath string          `json:"configPath,omitempty"`
	// Note 是给用户的**一句人话**（保存成功干了什么 / 配置读不出来的原因）。空 = 没什么要说。
	Note string `json:"note,omitempty"`
}

// settingsViewOf 拼这个回包。`loadErr` 非 nil ⇒ 说明配置文件坏了，如实塞进 Note。
func settingsViewOf(cfg *config.Config, loadErr error, st *panelState, detected, from string) *settingsView {
	if cfg == nil {
		cfg, _ = config.Load()
	}
	v := &settingsView{
		OK:           true,
		CLIPath:      cfg.CLIPath,
		UsingCLI:     st.client.Path,
		DetectedCLI:  detected,
		DetectedFrom: from,
		TimeoutSec:   cfg.TimeoutSec,
		RailOnBoot:   cfg.RailOnBoot,
		RailRunning:  theRail != nil,
		PanelURL:     thePanelURL,
	}
	// 开机启动：读注册表现场（读不出来不致命，如实塞 Note）
	if ast, err := autostart.Status(); err == nil {
		v.AutoStart = ast
	} else {
		v.AutoStart = autostart.State{Supported: ast.Supported, Want: autostart.Cmd(),
			Note: "读注册表失败：" + err.Error()}
	}
	if p, err := config.Path(); err == nil {
		v.ConfigPath = p
	}
	if loadErr != nil {
		v.OK = false
		v.Note = "配置文件读不出来（已用默认值继续）：" + loadErr.Error()
	}
	return v
}
