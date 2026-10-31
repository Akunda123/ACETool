package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// 「撤回刚才这一批」——用户 2026-10-07：「给面板加一个撤回功能，用于撤回刚才的一批操作，
// 用于单选项立即执行的按钮」。
//
// 为什么是**它**而不是给每个工具写反操作：ACE 的撤销栈是**共享**的
// （`history undo` 原文：Undo the top entry of the shared undo stack, **whoever authored it**），
// 而且引擎自己知道怎么把状态还原（连音素**时序**这种我们没存过的东西也能还原）。
// 所以对自己的写操作，**用引擎自己的撤销**永远比我们反着写一遍更准。
//
// 但"退几条"不能靠数——ACE 的栈里混着**用户自己的操作**（真机实测：`选择音符`、`移动轨道`
// 的 actor 是空字符串，我们 CLI 写的是 `远程控制`），盲退就会吃掉用户的东西。做法是**游标划线**：
//
//	① 写工具**跑之前**记下游标 `index`（= 已生效条目数）⇒ 面板回包里带给页面（`undo.anchor`）；
//	② 撤回时只退 **游标 > anchor** 的条目，而且**每退一步都看顶上是谁写的**：
//	   一旦 `actor` 不是「远程控制」⇒ **停手**并如实报（绝不越界退用户的操作）。
//
// 真机实测（2026-10-07，ACE 2.1.8 / CLI 0.17.0）：
//   - `history list` 回 `{count, index, entries:[{index,name,actor,applied}]}`；
//   - `index` = **已生效条目数**（游标），新条目插在这里；`applied:false` 的是"重做侧"；
//   - **一次新写会砍掉重做侧**（实测 count 57 → 28 然后新增一条）；
//   - 逐字段可撤销：`track set` 的 help 原文是 "undoable **per-field**；unchanged values do not
//     push to the undo stack" ⇒ **命令条数 ≠ 撤销条数**，只能从栈的游标差值里读，不能数命令。
type UndoEntry struct {
	Index   int    `json:"index"`
	Name    string `json:"name"`
	Actor   string `json:"actor"`
	Applied bool   `json:"applied"`
}

// UndoStack 是 `history list` 的回包。**导出**是因为面板 exe 也要读它
// （写工具跑前后各读一次，见 ReadUndoStack 与 panel_api.go 的 attachUndoMark）。
type UndoStack struct {
	Count   int         `json:"count"`
	Index   int         `json:"index"`
	Entries []UndoEntry `json:"entries"`
}

// ReadUndoStack 读一次 ACE 撤销栈（只读）。面板的"撤回标记"与 `history.undoBatch` 共用这一份解析。
func ReadUndoStack(ctx context.Context, rd ReadFunc) (*UndoStack, error) {
	var st UndoStack
	if err := readJSON(ctx, rd, &st, "history", "list"); err != nil {
		return nil, err
	}
	return &st, nil
}

// 我们（CLI 写动词）在撤销栈里的署名。真机实测就是这个字符串。
const undoActorCLI = "远程控制"

type undoBatchReport struct {
	Anchor      int      `json:"anchor"`               // 那一批之前的游标
	Cursor      int      `json:"cursor"`               // 现在 ACE 栈的游标
	Max         int      `json:"max"`                  // 锚点以上还剩几条（上限）
	WillUndo    int      `json:"willUndo"`             // 打算退几条（含穿过的噪音）
	Names       []string `json:"names,omitempty"`      // 会退掉哪些**内容**条目（名字，新的在前）
	Skipped     []string `json:"skipped,omitempty"`    // 顺带穿过去的**没落在工程里**的条目（不算内容）
	Probed      []string `json:"probed,omitempty"`     // 按效果探过的（撤了之后 dirty 变没变）
	ToProbe     []string `json:"toProbe,omitempty"`    // 计划里"待探"的（说不准是不是内容）
	ProbeSaves  int      `json:"probeSaves,omitempty"` // 为了探针保存了几次（工程文件也跟着动了）
	Blocked     string   `json:"blocked,omitempty"`    // 连一条都不该退的原因（先说清，别动手）
	Stopped     string   `json:"stopped,omitempty"`    // 退到一半停手的原因
	Undone      []string `json:"undone,omitempty"`     // 真退掉的（新的在前，含穿过的）
	Calls       []string `json:"plannedCalls,omitempty"`
	Did         []string `json:"done,omitempty"`
	DryRun      bool     `json:"dryRun,omitempty"`
	CursorAfter int      `json:"cursorAfter,omitempty"`
	Millis      int64    `json:"millis,omitempty"`
	Note        string   `json:"note,omitempty"`
}

func init() {
	register(&Tool{
		ID: "history.undoBatch", Category: "工程",
		Title: "撤回刚才的一批",
		Desc: "把**上一次写操作**（面板上那颗一键按钮压进 ACE 撤销栈的那些条目）一次性退回去。" +
			"走的是 ACE 自己的撤销栈（`history undo`）⇒ 引擎自己知道怎么还原（连音素时序也能还原）。" +
			"⛔ 只退**锚点以上、且署名是「远程控制」**的条目 —— 顶上若是你自己在 ACE 里的操作，就**停手并如实报**，" +
			"绝不越过你去退你的东西。\n" +
			"✅ 但**选择/光标这类噪音可以穿过去**（用户 2026-10-15：「如果有选中音符什么的批量撤回直接撤」）：" +
			"锚点以上碰到「选择音符 / 更改选择 / 移动光标」（`skipNames` 可改）就照样撤掉它们、" +
			"**不占 `count` 名额**，继续把这一批的内容条目退完 —— 撤消只能从栈顶往下，所以「穿过」= 把它们也撤了" +
			"（它们只是选择状态，不是内容编辑）。\n" +
			"✅ **另一个判据是「效果」**（用户 2026-10-15：「只要不是实际落在工程的改动，都是噪音」）：" +
			"`checkEffect`（默认开）会先 `project save` 把 `dirty` 归零，然后**从栈顶往下一条条撤、每撤一条看 `dirty`**：" +
			"还是 false ⇒ 这条**没落在工程里** = 噪音（照样穿过）；变了 true ⇒ **落在工程里** = 内容。" +
			"⚠️ 只在**碰到第一条内容**之前这样探（`dirty` 变 true 之后就不再能区分了）⇒ 全程最多**多保存一次**；" +
			"那第一条内容若是**你自己**的（署名不是我们），会立刻 `history redo` 把它放回去并停手。\n" +
			"⚠️ 锚点（`anchor`）由**面板回包**给出（写工具跑之前记的游标）：`count` 只是上限，退到锚点就停。" +
			"⚠️ 只出计划时只调 `Plan` 那一半；页面上的按钮直接改工程。",
		Kind:   KindWrite,
		Source: "自建（ACE 撤销栈 `history list` / `history undo`）",
		Params: []Param{
			numParam("anchor", "锚点（那一批之前的撤销栈游标；面板回包里的 `undo.anchor`）", ""),
			textParam("count", "最多退几条（0 = 退到锚点为止）", "0", "",
				"给个上限更安全：面板回包里的 `undo.created` 就是当时的条数。"),
			textParam("actor", "只退这个署名的条目", undoActorCLI, "",
				"ACE 撤销栈里的作者。我们 CLI 写的是「"+undoActorCLI+"」，用户自己在 ACE 里动的是空串。"),
			textParam("names", "只退这些名字的条目（JSON 数组，可留空）", "", `["调整音符大小","编辑歌词"]`,
				"面板回包里的 `undo.names`。**顶上那条的名字不在里面就不退** —— 防止「页面上记的是 A 批、"+
					"实际栈顶是别处跑出来的 B 批」时退错东西（比如热键重放了一次别的工具）。"),
			textParam("skipNames", "可以「穿过去」的条目名（逗号分隔）", defaultUndoSkips, "选择音符,更改选择,移动光标",
				"用户 2026-10-15：「如果有选中音符什么的批量撤回直接撤」⇒ 锚点以上碰到这些名字就**穿过去**："+
					"撤掉它们（**不看署名**——它们本来就是你自己在 ACE 里点出来的），但**不占 `count` 的名额**，"+
					"也不当成「这一批的内容」。想严格按条数退（一条噪音都不过）就把这里清空。"),
			checkParam("checkEffect", "按效果判噪音（不是实际落在工程的改动都算噪音）", true),
		},
		Plan:  undoBatchPlan,
		Apply: undoBatchApply,
	})
}

// readUndoStack 是内部别名（读一次栈）。
func readUndoStack(ctx context.Context, rd ReadFunc) (*UndoStack, error) {
	return ReadUndoStack(ctx, rd)
}

// projectDirty 读 `project dirty` 的布尔（"按效果判噪音"的判据）。
func projectDirty(ctx context.Context, rd ReadFunc) (bool, error) {
	var st struct {
		Dirty *bool `json:"dirty"`
	}
	if err := readJSON(ctx, rd, &st, "project", "dirty"); err != nil {
		return false, err
	}
	if st.Dirty == nil {
		return false, fmt.Errorf("`project dirty` 没给 dirty 字段")
	}
	return *st.Dirty, nil
}

// orEmpty 给报告用：空串换成一句人话。
func orEmpty(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// topApplied 取"下一个会被撤销的条目"（= 游标下面那一条，必须 applied）。
// ⚠️ 不能拿 `entries[0]`：列表是"新的在前"，而重做侧（`applied:false`）也排在前面。
func topApplied(st *UndoStack) *UndoEntry {
	if st == nil || st.Index <= 0 || len(st.Entries) == 0 {
		return nil
	}
	for i := range st.Entries {
		if st.Entries[i].Index == st.Index-1 {
			return &st.Entries[i]
		}
	}
	return nil
}

// above 列出"锚点以上的条目"，新的在前（这些才是这一批的候选）。
func above(st *UndoStack, anchor int) []UndoEntry {
	var out []UndoEntry
	for i := range st.Entries {
		if st.Entries[i].Index >= anchor && st.Entries[i].Index < st.Index && st.Entries[i].Applied {
			out = append(out, st.Entries[i])
		}
	}
	// entries 本身新的在前 ⇒ 不用再排；保险起见按 index 降序
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Index > out[j-1].Index; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func undoParams(vals map[string]string) (anchor, count int, actor string, err error) {
	raw := strings.TrimSpace(vals["anchor"])
	if raw == "" {
		return 0, 0, "", fmt.Errorf("要一个锚点（`anchor`）—— 面板回包里的 `undo.anchor`（写工具跑之前的撤销栈游标）")
	}
	a, perr := strconv.Atoi(raw)
	if perr != nil || a < 0 {
		return 0, 0, "", fmt.Errorf("锚点 `anchor` 要是 0 或正整数，实得 %q", raw)
	}
	c := 0
	if s := strings.TrimSpace(vals["count"]); s != "" {
		n, cerr := strconv.Atoi(s)
		if cerr != nil || n < 0 {
			return 0, 0, "", fmt.Errorf("`count` 要是非负整数，实得 %q", s)
		}
		c = n
	}
	ac := strings.TrimSpace(value(vals, "actor", undoActorCLI))
	if ac == "" {
		ac = undoActorCLI
	}
	var names []string
	if s := strings.TrimSpace(vals["names"]); s != "" {
		if err := json.Unmarshal([]byte(s), &names); err != nil {
			return 0, 0, "", fmt.Errorf("`names` 要是 JSON 数组（如 [\"调整音符大小\"]），实得 %q：%w", s, err)
		}
	}
	undoNames = names
	// ⚠️ 这里**不能**用 `value(vals, "skipNames", default)`：那个 helper 把"显式给空串"也当成没给
	// ⇒ 想严格按条数退（把 skipNames 清空）就永远关不掉。**缺席**才用默认；给了（哪怕空串）就听调用方的。
	if raw, ok := vals["skipNames"]; ok {
		undoSkips = parseSkipNames(raw)
	} else {
		undoSkips = parseSkipNames(defaultUndoSkips)
	}
	// `checkEffect` **默认关**（理由见 undoCheckEffect 那条注释：`dirty` 分不出"乐音改动 vs 选区状态"）
	// ⇒ 只有显式传 `checkEffect=1` 才走"撤一条看 dirty"那条路。
	undoCheckEffect = false
	if raw, ok := vals["checkEffect"]; ok {
		undoCheckEffect = truthy(raw)
	}
	return a, c, ac, nil
}

// defaultUndoSkips 是默认「可以穿过去」的条目名。
//
// 实测（2026-10-15，真机 `history list`）栈里出现过：`选择音符` / `更改选择` / `移动光标`
// —— 都是**用户自己在 ACE 里点出来**的（署名空串，不是「远程控制」），对内容毫无影响。
// ⚠️ 我们 CLI 的 `selection set` / `caret set` **根本不进栈**（探针 `devtools/probe-undo-selection.js` 实测 Δ=0）
// ⇒ 这一条纯粹是为了"用户手点过之后，还能把这一批撤掉"。
const defaultUndoSkips = "选择音符,更改选择,移动光标"

// parseSkipNames 把 "选择音符,更改选择" 拆成清单（也认中文顿号/空格；空串 = 不穿任何条目）。
func parseSkipNames(raw string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '，' || r == '、' || r == ' ' || r == '\t' || r == '\n'
	}) {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// isSkipName 这条名字算不算"可以穿过去的噪音"。
func isSkipName(name string, skips []string) bool {
	for _, s := range skips {
		if s == name {
			return true
		}
	}
	return false
}

// undoNames 是本次调用的"只退这些名字"清单（空 = 不校验名字）。
// ⚠️ 用包级变量是**故意**的：`planUndo` 是纯函数（测试直接调），不想为了一个可选校验把它撑成 5 个参数。
// 单进程单请求串行（CLI 本来就串行化了）⇒ 没有并发覆盖问题。
var undoNames []string

// undoSkips 是本次调用的"可以穿过去"清单（默认见 defaultUndoSkips）。
var undoSkips []string

// undoCheckEffect 本次调用是否**按效果**判噪音（参数 `checkEffect`）。
//
// ⛔ **默认关**（2026-10-15 真机测出来的硬事实）：本来想用
// 「先 `project save` 把 `dirty` 归零 → 撤一条 → 看 `dirty`」判「这条有没有落在工程里」，
// 实测**撤掉一条 `更改选择` 也会让 `dirty` 变 true** —— 因为 **ACE 把选区/光标也存进工程文件**
// ⇒ `dirty` 分不出「乐音改动」和「UI 状态」。开着它会把用户点出来的选择当成内容、
// `redo` 放回去再停手（`devtools/demo-undo-effect.js` 现场复现过）。
// ⇒ 现在**只有显式传 `checkEffect=1`** 才走这条路（留给"判据换成音符内容指纹"的将来）；
// 默认仍是**名字清单**（`skipNames`）—— 真机验过：用户点出来的 `选择音符/更改选择/移动光标` 能穿过去。
var undoCheckEffect = false

func nameAllowed(name string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if a == name {
			return true
		}
	}
	return false
}

// planUndo 只读地算出"能退几条、会不会被挡住"。**一条写命令都不发。**
//
// 走法（用户 2026-10-15 的口径）：从栈顶往下走在 `[anchor, cursor)` 里，
// **选择/光标这类噪音**（`undoSkips`）直接穿过（撤掉、不占 `count` 名额、不看署名）；
// **内容条目**要署名对得上（`actor`）且名字在 `names` 白名单里，退一条算一条，够 `count` 就停。
func planUndo(st *UndoStack, anchor, count int, actor string) *undoBatchReport {
	rep := &undoBatchReport{Anchor: anchor, Cursor: st.Index}
	if st.Index <= anchor {
		rep.Blocked = fmt.Sprintf("ACE 撤销栈的游标（%d）已经不在那一批上了（锚点是 %d）—— "+
			"多半你自己已经在 ACE 里撤过，这一批不能再自动退。", st.Index, anchor)
		return rep
	}
	rep.Max = st.Index - anchor

	content := 0
	ordered := above(st, anchor) // 新的在前
	for i, e := range ordered {
		byName := isSkipName(e.Name, undoSkips)
		ours := e.Actor == actor && nameAllowed(e.Name, undoNames)
		if byName {
			rep.Skipped = append(rep.Skipped, e.Name)
			continue
		}
		if ours {
			content++
			rep.Names = append(rep.Names, e.Name)
			if count > 0 && content >= count {
				break
			}
			continue
		}
		// 说不准（署名不是我们 / 名字不在这批记录里）：**按效果判**模式下先记为"待探"，
		// 真写那一步撤一条看 `dirty` 再定性；关掉这个模式才走老规矩（挡住 / 停手）。
		if undoCheckEffect {
			rep.ToProbe = append(rep.ToProbe, e.Name)
			continue
		}
		who := e.Actor
		if who == "" {
			who = "（空 = 你自己在 ACE 里的操作）"
		}
		if e.Actor != actor {
			if content == 0 {
				rep.Blocked = fmt.Sprintf("撤销栈顶上%s是「%s」，署名 %s —— **不是这一批，所以一条都没退**。"+
					"先在 ACE 里把它撤掉（或接受它），再点撤回。%s", skipPrefix(len(rep.Skipped)), e.Name, who,
					skipHint(len(rep.Skipped)))
				rep.Skipped, rep.ToProbe, rep.WillUndo = nil, nil, 0
				return rep
			}
			rep.Stopped = fmt.Sprintf("退到第 %d 条（内容第 %d 条）碰上不是你我的「%s」（署名 %s）—— 只退前面这些。",
				i+1, content+1, e.Name, who)
			break
		}
		// 名字守卫：页面上记的是"某一批的条目名字"，顶上却是**别的操作**（比如热键重放了一次别的工具）
		if !nameAllowed(e.Name, undoNames) {
			if content == 0 {
				rep.Blocked = fmt.Sprintf("撤销栈顶上%s是「%s」，**不在这一批的记录里**（%s）—— "+
					"一条都没退。多半是这中间又有别的写入（热键重放 / 另一个窗口），先弄清栈顶是什么再来。",
					skipPrefix(len(rep.Skipped)), e.Name, strings.Join(undoNames, "、"))
				rep.Skipped, rep.ToProbe, rep.WillUndo = nil, nil, 0
				return rep
			}
			rep.Stopped = fmt.Sprintf("退到第 %d 条（内容第 %d 条）碰上「%s」，它不在这一批的记录里 —— 只退前面这些。",
				i+1, content+1, e.Name)
			break
		}
		content++
		rep.Names = append(rep.Names, e.Name)
		if count > 0 && content >= count {
			break
		}
	}
	rep.WillUndo = len(rep.Names) + len(rep.Skipped) + len(rep.ToProbe)
	if rep.WillUndo == 0 && rep.Blocked == "" {
		rep.Blocked = "锚点以上没有可退的条目（这一批可能已经被退掉了）。"
	}
	for range rep.Names {
		rep.Calls = append(rep.Calls, "history undo")
	}
	for range rep.Skipped {
		rep.Calls = append(rep.Calls, "history undo")
	}
	for range rep.ToProbe {
		rep.Calls = append(rep.Calls, "history undo（先撤一条看 dirty，实为噪音才继续）")
	}
	return rep
}

// skipPrefix / skipHint 在"被挡住"的消息里如实交代穿过了几条噪音。
func skipPrefix(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("（穿过 %d 条选择/光标条目之后）", n)
}

func skipHint(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("⚠️ 那 %d 条选择/光标条目也**没动**（一条都不退）。", n)
}

func undoBatchPlan(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	anchor, count, actor, err := undoParams(vals)
	if err != nil {
		return nil, err
	}
	st, err := readUndoStack(ctx, rd)
	if err != nil {
		return nil, err
	}
	rep := planUndo(st, anchor, count, actor)
	rep.DryRun = true
	if rep.Blocked != "" {
		rep.Note = "干跑：一个字节都没写。" + rep.Blocked
	} else {
		rep.Note = fmt.Sprintf("干跑：一个字节都没写。会退 %d 条（游离标 %d → %d）。",
			rep.WillUndo, st.Index, st.Index-rep.WillUndo)
	}
	return rep, nil
}

func undoBatchApply(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	anchor, count, actor, err := undoParams(vals)
	if err != nil {
		return nil, err
	}
	st, err := readUndoStack(ctx, rd)
	if err != nil {
		return nil, err
	}
	rep := planUndo(st, anchor, count, actor)
	if rep.WillUndo == 0 {
		rep.Note = rep.Blocked
		return rep, nil
	}
	// ⚠️ 计划里已经预填过 Names/Skipped/Calls（那是"打算"）；真写这条路上**以循环里实际发生的为准**
	// ⇒ 先清空，免得同一批名字被记两遍（踩过：Skipped 里出现 2×2 条）。
	rep.Names, rep.Skipped, rep.Calls, rep.Undone, rep.Did = nil, nil, nil, nil, nil
	// 一条一条退：**每退一条之前重读一次栈**，重新确认顶上还是我们写的那一条。
	// （ACE 没有"事务"，也不能一次退 N 条 ⇒ 只能这样步步为营；读一次栈 ~80ms。）
	// 选择/光标这类噪音（`undoSkips`）**照样退掉**，但不占 `count` 名额 —— 这就是"穿过去"。
	var done []string
	content := 0
	var (
		probed     bool // 已经探出过一条内容 ⇒ dirty 会恒为 true，不再探
		probeSaved int  // 为了探针 `project save` 了几次（报告里如实说）
	)
	zeroDirty := func() bool {
		if probeSaved > 0 {
			return true // 已经归零过一次；探针只在"还没探出内容"时用，中间不会有内容改动把它弄脏
		}
		if err := runWrite(ctx, rd, "project", "save"); err != nil {
			rep.Stopped = fmt.Sprintf("判噪音前 `project save` 失败（一条都没动）：%v", err)
			return false
		}
		probeSaved++
		return true
	}
walk:
	for {
		if count > 0 && content >= count {
			break
		}
		cur, rerr := readUndoStack(ctx, rd)
		if rerr != nil {
			rep.Stopped = fmt.Sprintf("退到第 %d 条时读不到撤销栈了：%v", len(rep.Undone)+1, rerr)
			break
		}
		if cur.Index <= anchor {
			break // 已经退到锚点：这一批退完了
		}
		top := topApplied(cur)
		if top == nil {
			rep.Stopped = fmt.Sprintf("退到第 %d 条时栈里找不到顶上那一条（ACE 的栈变了）", len(rep.Undone)+1)
			break
		}
		// 三种身份：
		//   ours  = **能证明是我们这一批的内容**（署名对得上 + 名字在这一批的记录里）⇒ 直接退，不探
		//   byName= 名字在"已知噪音"清单里 ⇒ 直接穿，不探
		//   其它  = 说不准 ⇒ **按效果判**（撤了看 dirty：没变 = 没落在工程里 = 噪音）
		ours := top.Actor == actor && nameAllowed(top.Name, undoNames)
		byName := isSkipName(top.Name, undoSkips)
		skip := byName
		probe := undoCheckEffect && !probed && !ours && !byName
		if probe && !zeroDirty() {
			break
		}
		if !skip && !probe {
			// 老规矩：署名/名字对不上就不动（这一步是"我们已经能确定它不是这一批"的情况）
			if top.Actor != actor {
				rep.Stopped = fmt.Sprintf("退到第 %d 条时，顶上变成了「%s」（署名 %s）—— 停手，"+
					"**再往下就不是这一批了**。", len(rep.Undone)+1, top.Name, orEmpty(top.Actor, "空 = 你自己在 ACE 里的操作"))
				break
			}
			if !nameAllowed(top.Name, undoNames) {
				rep.Stopped = fmt.Sprintf("退到第 %d 条时，顶上变成了「%s」，它不在这一批的记录里 —— 停手。",
					len(rep.Undone)+1, top.Name)
				break
			}
		}
		if err := runWrite(ctx, rd, "history", "undo"); err != nil {
			rep.Stopped = fmt.Sprintf("第 %d 条 `history undo` 失败（前面已退的**没回滚**）：%v", len(rep.Undone)+1, err)
			break
		}
		if probe {
			d, derr := projectDirty(ctx, rd)
			switch {
			case derr != nil:
				rep.Stopped = fmt.Sprintf("第 %d 条撤了之后读不到 `project dirty`：%v（这条按内容算，已经撤掉）",
					len(rep.Undone)+1, derr)
			case !d:
				// 撤了它工程还是干净的 ⇒ 这条**没落在工程里** = 噪音（用户 2026-10-15 的口径）
				skip = true
				rep.Probed = append(rep.Probed, top.Name+"：撤了 `dirty` 还是 false ⇒ 没落在工程里（噪音）")
			default:
				// 它**落在工程里**了，而且不能证明是我们这一批 ⇒ 是用户自己的内容编辑 ⇒ 放回去 + 停手
				probed = true
				if err := runWrite(ctx, rd, "history", "redo"); err != nil {
					rep.Stopped = fmt.Sprintf("「%s」是你自己的操作（撤了工程真变了）却撤了，而 `history redo` 又失败：%v"+
						" —— 请在 ACE 里 Ctrl+Y 看一眼（工程文件没动）。", top.Name, err)
					break
				}
				rep.Probed = append(rep.Probed, top.Name+"：撤了工程真变了 ⇒ 是内容，且不能证明是我们写的 ⇒ 已 `redo` 放回")
				rep.Stopped = fmt.Sprintf("按效果判：顶上这条「%s」（署名 %s）**真落在工程里**、又不是这一批 —— "+
					"已经 `history redo` 放回去了，**不再往下撤**。", top.Name, orEmpty(top.Actor, "空 = 你自己"))
				break walk // ⚠️ 必须跳出**整个循环**：否则再转一圈会拿通用消息把这句覆盖掉（踩过）
			}
		}
		rep.Undone = append(rep.Undone, top.Name)
		if skip {
			rep.Skipped = append(rep.Skipped, top.Name)
			done = append(done, "history undo → "+top.Name+"（没落在工程里，穿过）")
		} else {
			rep.Names = append(rep.Names, top.Name)
			content++
			done = append(done, "history undo → "+top.Name)
		}
		if rep.Stopped != "" {
			break
		}
	}
	rep.ProbeSaves = probeSaved
	rep.Did = done
	if after, aerr := readUndoStack(ctx, rd); aerr == nil {
		rep.CursorAfter = after.Index
	}
	switch {
	case rep.Stopped != "":
		rep.Note = fmt.Sprintf("退了 %d 条就停手（其中穿过 %d 条选择/光标）：%s ⚠️ 前面已经退的**不会自己回来**（ACE 每条撤销独立）。",
			len(rep.Undone), len(rep.Skipped), rep.Stopped)
	case rep.CursorAfter > 0 && rep.CursorAfter <= anchor:
		rep.Note = fmt.Sprintf("这一批退干净了：游标回到 %d（锚点 %d），共退 %d 条%s。",
			rep.CursorAfter, anchor, len(rep.Undone), skipPhrase(len(rep.Skipped)))
	default:
		rep.Note = fmt.Sprintf("退了 %d 条%s；游标现在 %d（锚点 %d）。"+
			"⚠️ 没退到锚点 —— 要么 `count` 给少了，要么栈中途变了。", len(rep.Undone), skipPhrase(len(rep.Skipped)), rep.CursorAfter, anchor)
	}
	return rep, nil
}

// skipPhrase 报告里那句"顺带穿过了 N 条选择/光标"。
func skipPhrase(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("（顺带穿过了 %d 条选择/光标条目 —— 它们不是内容编辑）", n)
}
