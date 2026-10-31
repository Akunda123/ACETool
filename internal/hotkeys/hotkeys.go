// Package hotkeys 是面板快捷键的**单一事实源**：动作表 · 键位记法的规范化与校验 ·
// 绑定的持久化 · Windows 系统级热键（`win.go`，非 Windows 走 `stub.go`）。
//
// 分两层（用户 2026-10-05 定的口径：**两层都做，键位在设置里自己绑**）：
//
//	层 1 页面内：网页自己 keydown 分发。零依赖、零风险，但**只在浏览器标签前台时有效**。
//	层 2 系统级：exe 调 Windows 的 `RegisterHotKey` ⇒ 人在 ACE 里、浏览器在后台也响应。
//	              ⚠️ 网页**做不到**这件事（没有任何 OS 钩子），所以只能由 exe 注册；
//	              代价是：注册上去的组合键会被 exe **系统级吞掉** ⇒ 面板自己就不再处理它
//	              （`Binding.Global` 就是这个开关，前端据此跳过，避免一次按键跑两遍）。
//
// ⚠️ 键位记法两侧必须逐字一致（Go 解析、JS 采集）。规范形：
//
//	修饰键固定顺序 `Ctrl+Alt+Shift+Win`，末段是主键；字母大写、数字原样、命名键用下表那套拼写。
//	例：`Ctrl+Alt+Enter` · `Ctrl+Shift+K` · `F2` · `?`
package hotkeys

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"acetool/internal/config"
)

// Action 是一个可绑定的动作。**这张表也是设置页的全部内容**（前端不写死任何动作）。
type Action struct {
	ID string `json:"id"`
	// Label 是设置页里的名字。
	Label string `json:"label"`
	// Group 只是设置页里的分组标题。
	Group string `json:"group"`
	// DefaultKey 是出厂键位（空 = 默认不绑）。
	DefaultKey string `json:"defaultKey"`
	// CanGlobal=true 表示这个动作**能**绑成系统级（exe 自己就能执行它，不需要网页在场）。
	CanGlobal bool `json:"canGlobal"`
	// AlwaysGlobal=true 表示这个动作**只能**系统级（exe 收热键 → 发事件 → 页面去做）。
	// 设置页据此把「全局」勾上并锁住；`POST /api/shortcuts` 也会强制 `Global=true`。
	// 主页面那 20 条（`quick.go`）就是这一类。
	AlwaysGlobal bool `json:"alwaysGlobal,omitempty"`
	// What 是一句话说明：这个动作到底干什么、有什么前提。
	What string `json:"what"`
}

// Catalog 返回全部动作（顺序 = 设置页的显示顺序）。
//
// ⚠️ 2026-10-17 用户：「只留我说的那几个快捷键」⇒ 动作表**只剩主页面那 20 条**
// （`quick.go`：打开主页面 + 19 颗按钮）。原来那套"面板自己的动作"
// （执行主动作 / 强制干跑 / 上一个工具 / 下一项 / 切干跑 / 清日志 / 打开设置页 / 快捷键清单 /
// 跳到某一栏…）**全部删掉**了，理由：
//
//	· 它们是给**老壳页**（`index.html` + `app.js`）的页面内分发写的，而那页已经停用（§82）；
//	· 其中"执行主动作 / 强制干跑"重放的是"面板上最后一次跑过的工具"，主页面没有这个概念
//	  （每次点按钮都自带参数）⇒ 留着只会给人一个"按了也不知道会跑什么"的键；
//	· 真正还需要的那几件都另有入口：开面板=托盘图标/`panel.focus` 那类热键、置顶=顶栏 📌、
//	  设置=home 顶栏 ⚙、快捷键这页本身就在设置里。
func Catalog() []Action { return QuickActions() }

// CatalogWith 是历史入口（原来用来把"跳栏"动态动作并进目录）。
// 跳栏动作已随老壳页一起删掉 ⇒ 现在它和 `Catalog()` 完全一样；保留签名只为不牵动调用点。
func CatalogWith([]string) []Action { return Catalog() }

// Binding 是一条绑定。`Key` 为空 = 不绑（用户清掉了）。
//
// ⚠️ 它是 `config.ShortcutBinding` 的**别名**（不是另立一个结构体）：
// 落盘要经 `internal/config`，而 `hotkeys` 又依赖 config ⇒ 结构体定义只能放在 config 那边，
// 否则两个包互相 import。用别名就没有"两套字段要同步改"的隐患。
type Binding = config.ShortcutBinding

// ---------- 规范化与校验 ----------

// modOrder 是规范形里修饰键的固定顺序。
var modOrder = []string{"Ctrl", "Alt", "Shift", "Win"}

// namedKeys 是允许的**命名键**（与前端 `e.code` 的映射表逐条对应）。
//
// ⚠️ 这张表就是两侧的契约：JS 只产出这里有的名字，Go 只认这里有的名字。
var namedKeys = map[string]bool{
	"Enter": true, "Space": true, "Tab": true, "Escape": true, "Backspace": true, "Delete": true,
	"Insert": true, "Home": true, "End": true, "PageUp": true, "PageDown": true,
	"ArrowUp": true, "ArrowDown": true, "ArrowLeft": true, "ArrowRight": true,
	"[": true, "]": true, "/": true, "\\": true, ";": true, "'": true, ",": true, ".": true,
	"-": true, "=": true, "`": true,
	// ⚠️ `?` 必须在这儿：前端是从「按出来的是什么」采集的（`?` = Shift+/），
	// 少了它 `help.shortcuts` 的出厂键位就过不了自己的校验（测试逮到的）。
	"?": true,
}

// bareAllowed 是**允许不加修饰键**的主键：F1-F24 与这几个标点。
//
// 判据不是"不打字"（`[` 当然能在歌词框里打出来），而是"**打字的时候我们本来就不抢**"：
// 分发器在输入框里只放行带 Ctrl/Alt 的组合（见 app.js 的 `isTyping`）。
// 所以这几个键在"没在输入"时当快捷键、在"正在输入"时照常打字，两头都对。
var bareAllowed = map[string]bool{"?": true, "[": true, "]": true}

// reserved 是**浏览器/系统一定先吃掉**的组合 —— 绑了也收不到，所以直接拒绝并说清为什么。
//
// 宁可拒绝也不假装绑定：绑上一个永远不响的键，是"看着成功其实错"的典型。
var reserved = map[string]string{
	"Ctrl+W": "关标签页（浏览器）", "Ctrl+T": "新标签页（浏览器）", "Ctrl+N": "新窗口（浏览器）",
	"Ctrl+R": "刷新（浏览器）", "Ctrl+Shift+R": "强制刷新（浏览器）", "Ctrl+Shift+W": "关窗口（浏览器）",
	"Ctrl+Shift+T": "恢复标签页（浏览器）", "Ctrl+Tab": "切换标签页（浏览器）", "Ctrl+Shift+Tab": "切换标签页（浏览器）",
	"Ctrl+L": "定位地址栏（浏览器）", "Ctrl+D": "收藏本站（浏览器）", "Ctrl+P": "打印（浏览器）",
	"Ctrl+S": "保存页面（浏览器）", "Ctrl+O": "打开文件（浏览器）", "Ctrl+H": "历史（浏览器）",
	"Ctrl+J": "下载（浏览器）", "Ctrl+U": "查看源码（浏览器）", "Ctrl+F": "页内查找（浏览器）",
	"Alt+F4": "关窗口（系统）", "Alt+Tab": "切换窗口（系统）", "F5": "刷新（浏览器）",
	"F11": "全屏（浏览器）", "F12": "开发者工具（浏览器）", "Ctrl+Alt+Delete": "系统安全键（系统）",
	"Win+L": "锁屏（系统）", "Win+D": "显示桌面（系统）", "Win+Tab": "任务视图（系统）",
}

// Normalize 把用户/前端给的键位**规范成唯一写法**（修饰键固定顺序、字母大写、别名归一）。
// 空串原样返回（= 不绑）。认不出来的主键报错，**不猜**。
func Normalize(in string) (string, error) {
	s := strings.TrimSpace(in)
	if s == "" {
		return "", nil
	}
	parts := strings.Split(s, "+")
	mods := map[string]bool{}
	main := ""
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return "", fmt.Errorf("键位 %q 里有空段（多余或缺少 `+`）", in)
		}
		switch strings.ToLower(p) {
		case "ctrl", "control":
			mods["Ctrl"] = true
			continue
		case "alt", "option":
			mods["Alt"] = true
			continue
		case "shift":
			mods["Shift"] = true
			continue
		case "win", "meta", "cmd", "super":
			mods["Win"] = true
			continue
		}
		if i != len(parts)-1 {
			return "", fmt.Errorf("键位 %q 里 %q 出现在主键前面 —— 修饰键只能是 Ctrl/Alt/Shift/Win，且必须在前面", in, p)
		}
		k, err := normalizeMain(p)
		if err != nil {
			return "", err
		}
		main = k
	}
	if main == "" {
		return "", fmt.Errorf("键位 %q 只有修饰键、没有主键", in)
	}
	var out []string
	for _, m := range modOrder {
		if mods[m] {
			out = append(out, m)
		}
	}
	out = append(out, main)
	return strings.Join(out, "+"), nil
}

// mainAliases 是常见别名 → 规范主键名。
var mainAliases = map[string]string{
	"return": "Enter", "esc": "Escape", "del": "Delete", "ins": "Insert", "spacebar": "Space",
	"up": "ArrowUp", "down": "ArrowDown", "left": "ArrowLeft", "right": "ArrowRight",
	"pgup": "PageUp", "pgdn": "PageDown", "plus": "=",
}

func normalizeMain(p string) (string, error) {
	if mainAliases[strings.ToLower(p)] != "" {
		return mainAliases[strings.ToLower(p)], nil
	}
	if len(p) == 1 {
		c := p[0]
		switch {
		case c >= 'a' && c <= 'z':
			return strings.ToUpper(p), nil
		case c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			return p, nil
		}
		if namedKeys[p] {
			return p, nil
		}
		return "", fmt.Errorf("主键 %q 不认识（允许：A-Z · 0-9 · F1-F24 · %s）", p, namedKeyList())
	}
	up := strings.ToUpper(p)
	if strings.HasPrefix(up, "F") && len(up) <= 3 {
		var n int
		if _, err := fmt.Sscanf(up[1:], "%d", &n); err == nil && n >= 1 && n <= 24 {
			return up, nil
		}
	}
	// 命名键大小写不敏感（`arrowup` 也认，前端/手写配置都可能给）
	for k := range namedKeys {
		if strings.EqualFold(k, p) {
			return k, nil
		}
	}
	return "", fmt.Errorf("主键 %q 不认识（允许：A-Z · 0-9 · F1-F24 · %s）", p, namedKeyList())
}

func namedKeyList() string {
	out := make([]string, 0, len(namedKeys))
	for k := range namedKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// Validate 判定一条绑定能不能用。**拒绝时一定说清理由**（前端直接显示这句话）。
//
// 判据：
//  1. 规范形（Normalize 过）。
//  2. 系统级绑定**必须有修饰键** —— 否则会把裸按键从整个系统抢走（连打字都受影响）。
//  3. 页面内绑定：无修饰键只允许 `bareAllowed` 里那几个与 F1-F24
//     —— 其余裸字母/裸数字会把输入框堵死。
//  4. 浏览器/系统先吃掉的组合（`reserved`）直接拒。
func Validate(key string, global bool) (string, error) {
	nk, err := Normalize(key)
	if err != nil {
		return "", err
	}
	if nk == "" {
		return "", nil
	}
	if why, bad := reserved[nk]; bad {
		return "", fmt.Errorf("`%s` 被%s占着，绑了也不会响 ⇒ 换一个（面板收不到它就等于没绑）", nk, why)
	}
	hasMod := false
	for _, m := range modOrder {
		if strings.HasPrefix(nk, m+"+") || strings.Contains(nk, "+"+m+"+") {
			hasMod = true
		}
	}
	main := nk
	if i := strings.LastIndex(nk, "+"); i >= 0 {
		main = nk[i+1:]
	}
	if global && !hasMod {
		return "", fmt.Errorf("系统级热键 `%s` 没有修饰键 —— 那会把裸按键从整个系统抢走（打字都会受影响）⇒ 加上 Ctrl/Alt", nk)
	}
	if !hasMod {
		if !bareAllowed[main] && !(strings.HasPrefix(main, "F") && len(main) <= 3) {
			return "", fmt.Errorf("`%s` 没有修饰键，会挡住输入框里打字 ⇒ 加上 Ctrl/Alt"+
				"（F1-F24 与 `?` `[` `]` 可以不修饰）", nk)
		}
	}
	return nk, nil
}

// Bindings 是一整套绑定：动作 id → 绑定。
type Bindings map[string]Binding

// Defaults 是出厂键位 —— **现在是空的**。
//
// ⚠️ 2026-10-17 用户：「强制干跑一次当前工具和执行当前页的主动作删掉吧」→ 紧接着
// 「只留我说的那几个快捷键」⇒ 动作表只剩主页面那 20 条（`quick.go`），**一律出厂不绑**：
// "默认都是空"是用户对那 20 条的原话，而它们里头一半是真写/改工程的操作。
func Defaults() Bindings { return Bindings{} }

// Load 读绑定：配置里没有就用出厂键位；坏掉的条目**丢掉并回报**（不让一条坏记录毁掉整张表）。
//
// ⚠️ **不认识的 id 也要丢掉并回报**：动作表被精简过（2026-10-17 删掉了老壳页那一整套），
// 而用户配置里还留着 `panel.focus` / `group.1` 这些 id ⇒ 不丢的话：
//
//	· `Apply` 每条都要报一次"动作 id 不认识"，设置页一片红；
//	· 设置页保存时会把它们原样发回来，服务端按"有不认识的 id"整单拒收 ⇒ **存不了任何快捷键**。
func Load() (Bindings, []string) {
	known := map[string]bool{}
	for _, a := range Catalog() {
		known[a.ID] = true
	}
	cfg, _ := config.Load()
	out := Defaults()
	if cfg == nil || len(cfg.Shortcuts) == 0 {
		return out, nil
	}
	var warns []string
	for id, b := range cfg.Shortcuts {
		if !known[id] {
			warns = append(warns, fmt.Sprintf("配置里的 `%s` 已经不是一个动作了（动作表精简过）⇒ 已忽略", id))
			continue
		}
		nk, err := Validate(b.Key, b.Global)
		if err != nil {
			warns = append(warns, fmt.Sprintf("配置里的 `%s`（%s）不能用：%s ⇒ 已忽略", id, b.Key, err))
			continue
		}
		if nk == "" {
			delete(out, id) // 显式清空
			continue
		}
		out[id] = Binding{Key: nk, Global: b.Global}
	}
	return out, warns
}

// DefaultsWith 是历史入口（原来还要补上"跳栏"那几个动态出厂键位）。
// 跳栏动作已随老壳页删掉、出厂表也空了 ⇒ 现在和 `Defaults()` 一样；保留签名只为不牵动调用点。
func DefaultsWith(int) Bindings { return Defaults() }

// Save 落盘（走 `internal/config`，不碰 ACE 的任何文件）。
func Save(b Bindings) error {
	cfg, _ := config.Load()
	if cfg == nil {
		cfg = &config.Config{}
	}
	cfg.Shortcuts = map[string]Binding{}
	for id, v := range b {
		cfg.Shortcuts[id] = v
	}
	return cfg.Save()
}

// Conflicts 找**同一个组合键被绑了两次**（含大小写/别名归一后的等价）。
// 冲突必须报出来 —— 送进 `RegisterHotKey` 时后一个会静默变成"注册失败"，用户只会觉得"键没反应"。
func Conflicts(b Bindings, byID map[string]Action) []string {
	seen := map[string]string{}
	var out []string
	ids := make([]string, 0, len(b))
	for id := range b {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		k := b[id].Key
		if k == "" {
			continue
		}
		if prev, dup := seen[k]; dup {
			out = append(out, fmt.Sprintf("`%s` 同时绑给了「%s」和「%s」⇒ 只有一个会生效，请改掉一个",
				k, labelOf(byID, prev), labelOf(byID, id)))
			continue
		}
		seen[k] = id
	}
	return out
}

func labelOf(m map[string]Action, id string) string {
	if a, ok := m[id]; ok {
		return a.Label
	}
	return id
}

// ErrBadKey 是调用方可以判别的"键位不合法"（前端要单独标红那一格）。
var ErrBadKey = errors.New("键位不合法")
