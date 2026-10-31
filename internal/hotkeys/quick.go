package hotkeys

import (
	"strings"
	"sync"
)

// 主页面（`home.html`）那组键位：**1 个「打开主页面」+ 主页面上的每一颗按钮**。
//
// 用户 2026-10-17 先要了「20 个位置（打开 home.html 一个 + 19 个按键），默认都是空」，
// 接着问「以后有新的按钮功能能不能有个扩展选项」。走了一圈之后**用户决定不做了**：
// 「如果麻烦就不改了，每次重下一遍完整软件吧」⇒ **不做插件/安装体系**，
// 新功能的路子就是 ① 在 `internal/tools` 注册工具 ② 在 `home.html` 的 `QUICK` 加一颗按钮
// ③ 重编 exe 重发（`devtools/quick.ps1 -Ui` 一条命令）。
//
// 所以现在清单**只有一份**：下面的 `builtinQuickButtons()`。它和 `home.html` 的 `QUICK`
// 一一对应，`check-ui` 有一条断言逐个对 id（两份表还是两份，但只有这两份，而且错了会立刻炸）。
//
// 三条硬口径没变：
//  1. **默认全空**（`DefaultKey: ""`）：用户明确要求；这些按钮一半是真写/改工程的操作。
//  2. **只能是系统级**（`AlwaysGlobal`）：键要在 ACE 里干活时按响，而"点按钮"只能由页面做 ⇒
//     exe 收热键后**发一条事件**，主页面收到事件去点那颗按钮。
//  3. `ID` = `quick.` + 工具 id，与页面里的 `data-tool` 一一对应。
const (
	// QuickOpenID 是"打开主页面"那一条的 id（固定，不来自清单）。
	QuickOpenID = "quick.open"
	// QuickGroup 是设置页里的分组标题。
	QuickGroup = "主页面按钮"
	// quickPrefix 是动作 id 的前缀。
	quickPrefix = "quick."
)

// Button 是主页面的一颗按钮（`Tool` 同时是动作 id 的后半截与页面里的 `data-tool`）。
type Button struct {
	Tool  string
	Label string
	Note  string // 一句话性质：开子窗口 / 立即执行（会改工程）/ 改工程文件 …
}

// 清单是进程级的：`SetQuickButtons` 写、`QuickActions` 读。
var (
	quickMu      sync.RWMutex
	quickButtons []Button
)

// SetQuickButtons 换一份按钮清单（空 id / 重复 id 的条目会被丢掉），返回清洗后的那份。
//
// 现在**只有一个调用点**（`SeedBuiltin`）。留着这个入口是因为：
// 将来真要按"开关某个按钮"（比如用户想临时藏掉几颗）时，从这里换一份就行，
// 不用再去改 `QuickActions` 的形状。换完调用方要重建动作表（`panel_api.rebuildActions`）。
func SetQuickButtons(list []Button) []Button {
	clean := make([]Button, 0, len(list))
	seen := map[string]bool{}
	for _, b := range list {
		b.Tool = strings.TrimSpace(b.Tool)
		b.Label = strings.TrimSpace(b.Label)
		b.Note = strings.TrimSpace(b.Note)
		if b.Tool == "" || seen[b.Tool] {
			continue
		}
		if b.Label == "" {
			b.Label = b.Tool
		}
		seen[b.Tool] = true
		clean = append(clean, b)
	}
	quickMu.Lock()
	quickButtons = clean
	quickMu.Unlock()
	return clean
}

// builtinQuickButtons 是**唯一那份**按钮清单（= `home.html` 的 `QUICK`，顺序也一致）。
//
// ⚠️ 加一颗按钮要动两处：这里 + `home.html` 的 `QUICK`。`check-ui` 有一条断言把两边的
// 19 个 `tool` 逐个对起来（错一个就红），不用靠记性。
func builtinQuickButtons() []Button {
	return []Button{
		{Tool: "tuning.harmony", Label: "和声", Note: "立即执行（会改工程）"},
	{Tool: "select.playhead", Label: "选择之后音符", Note: "立即执行（建立选区）"},
	{Tool: "note.join", Label: "连接音符", Note: "立即执行（会改工程）"},
		{Tool: "track.split", Label: "播放头位置分割轨道", Note: "立即执行（会改工程）"},
		{Tool: "lyric.copy", Label: "复制歌词", Note: "读歌词到剪贴板"},
		{Tool: "lyric.paste", Label: "粘贴歌词", Note: "立即执行（剪贴板由页面读）"},
		{Tool: "lyric.shift", Label: "添加 - 歌词", Note: "立即执行（会改工程）"},
		{Tool: "track.color", Label: "RGB 变色", Note: "开子窗口"},
		{Tool: "lyric.pages", Label: "填词", Note: "开子窗口"},
		{Tool: "lyric.lrc", Label: "LRC 导出", Note: "开子窗口"},
		{Tool: "phoneme.replace", Label: "批量替换音素", Note: "展开菜单（会改工程）"},
		{Tool: "xcopy.notes", Label: "跨轨复制", Note: "开子窗口"},
		{Tool: "xcopy.within", Label: "轨内复制", Note: "开子窗口"},
		{Tool: "param.sync", Label: "参数映射", Note: "开子窗口"},
		{Tool: "pitch.xcopy", Label: "跨轨道复制音高线", Note: "开子窗口（改工程文件）"},
		{Tool: "tone.apply", Label: "语调教", Note: "开子窗口（改工程文件）"},
		{Tool: "vibrato.apply", Label: "复制颤音模板", Note: "开子窗口（改工程文件）"},
		{Tool: "acep.patch", Label: "轨道内复制音高线", Note: "开子窗口（改工程文件）"},
		{Tool: "find.notes", Label: "查找", Note: "开子窗口"},
		{Tool: "jianpu.editor", Label: "谱面编辑器", Note: "开子窗口"},
		{Tool: "lyric.remove", Label: "删除并前移当前歌词", Note: "立即执行（会改工程）"},
		// ⭐ 用户 2026-10-21：「给撤回这一批加快捷键」——它不是工具，是主页面顶栏那颗按钮，
		// 走的是 ACE 自己的撤销栈（`history.undoBatch` + 页面持有的那一批锚点）。
		{Tool: "undo.batch", Label: "撤回这一批", Note: "立即执行（走 ACE 撤销栈）"},
	}
}

// SeedBuiltin 把内置清单装进进程（启动时调一次）。返回是否真的装了（给日志看）。
func SeedBuiltin() bool {
	if len(QuickButtons()) > 0 {
		return false
	}
	SetQuickButtons(builtinQuickButtons())
	return true
}

// QuickButtons 返回当前那份清单（副本，调用方随便改）。
func QuickButtons() []Button {
	quickMu.RLock()
	defer quickMu.RUnlock()
	out := make([]Button, len(quickButtons))
	copy(out, quickButtons)
	return out
}

// QuickActions 返回这一组键位：`quick.open` + 清单里每一项。
//
// 顺序：「打开主页面」在最前，其余**跟随页面顺序**（页面报上来什么顺序就是什么顺序）。
func QuickActions() []Action {
	out := make([]Action, 0, 1+len(QuickButtons()))
	out = append(out, Action{
		ID: QuickOpenID, Label: "打开主页面", Group: QuickGroup, DefaultKey: "",
		CanGlobal: true, AlwaysGlobal: true,
		What: "把主页面（`home.html`）的窗口打开/切到前台；已经开着就只切过去。",
	})
	for _, b := range QuickButtons() {
		what := "= 在主页面点一下「" + b.Label + "」"
		if b.Note != "" {
			// ⚠️ 用 ` · ` 不用括号：Note 里本身常带括号（`立即执行（会改工程）`）⇒ 再包一层就成
			// `（立即执行（会改工程））`，设置页那句话会读得很难受（用户 2026-10-19 那轮改文案时发现）。
			what += " · " + b.Note
		}
		what += "。 需要主页面窗口在；不在的话会先把它打开，约 2 秒后再执行。"
		out = append(out, Action{
			ID: quickPrefix + b.Tool, Label: b.Label, Group: QuickGroup, DefaultKey: "",
			CanGlobal: true, AlwaysGlobal: true, What: what,
		})
	}
	return out
}

// IsQuickAction 判断某个动作 id 是不是"替用户点主页面按钮"那一类（`fire` 里分流用）。
func IsQuickAction(id string) bool {
	return strings.HasPrefix(id, quickPrefix)
}

// QuickToolID 从动作 id 里取回工具 id（`quick.note.join` → `note.join`）；`quick.open` 返回空串。
func QuickToolID(id string) string {
	if id == QuickOpenID || !IsQuickAction(id) {
		return ""
	}
	return strings.TrimPrefix(id, quickPrefix)
}

// ButtonOf 按工具 id 找按钮（exe 侧判"这颗键该直接跑还是该开窗口"用）。
func ButtonOf(tool string) (Button, bool) {
	quickMu.RLock()
	defer quickMu.RUnlock()
	for _, b := range quickButtons {
		if b.Tool == tool {
			return b, true
		}
	}
	return Button{}, false
}
