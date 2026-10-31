package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"acetool/internal/cbor"
)

// 颤音模板（**文件路线**）—— 三种画法里的 ③：`notes[k].vibrato`
//（对象，实测 8 个字段；**不在 lane 里**，`startPos` 是音符内偏移 ⇒ 音符一挪颤音跟着走）。
//
// 用户 2026-10-08 的口径：「选中一个音符，设为颤音模板，然后第二次选中一批音符，
// 把第一次保存音符的颤音属性复制到第二次选中的音符上」。
// 页面（`vib.html`）的三步：① ACE 选**一个**音符 → 「保存预设」（显示信息 + 存模板 + 清选中）
// ② ACE **批量**选 → 「保存」（落盘 + 读目标选区）③ 「操作」（写文件 → 让 ACE 重开）。
//
// 三个工具：
//
//	· `vibrato.read`    （只读）读**当前 ACE 选区**的音符基本信息；给了 path 再从文件里读它们的颤音参数
//	· `vibrato.apply`   （写）把模板音符的 `vibrato` 复制到目标音符上（**原地**改工程文件，先备份）
//	· `selection.clear` （写）清空编辑器选区 —— 用户流程里的「清除选中」
//
// ⛔ 纪律同 `pitch.within`（技能 acep §8）：先「保存工程」→ 备份 → 只改必要字节 →
// 让 ACE 重新打开（页面走 `project open --discard-changes`）→ 读回核对。
// ⚠️ 技能 §2.2③：**没有颤音的音符连 `vibrato` 键都没有**（不是空对象/null）
// ⇒ 模板没颤音时**明确拒绝**，绝不猜成"把目标的颤音清掉"。

const vibSource = "技能 acep v1.2.3 §2.2 画法③（`notes[k].vibrato`，8 字段；无颤音=连键都没有）+ §4 安全写入"

// ---------- ① 读：选区基本信息（+ 文件里的颤音参数） ----------

type vibNoteInfo struct {
	UUID  string `json:"音符"`
	Lyric string `json:"歌词"`
	Pitch int    `json:"音高"`
	// ⚠️ 单位口径（用户 2026-10-14）：**对外一律「拍」** ⇒ 这两个字段印的是**拍**（tick/480），
	// 名字里不带 tick（`pos`/`dur` 在文件与 CLI 里仍是 tick，那属于内部）。
	Pos float64 `json:"片段内位置（拍）"`
	Dur float64 `json:"时长（拍）"`
}

type vibReadReport struct {
	Path    string        `json:"工程文件,omitempty"`
	Track   string        `json:"轨道"`
	Clip    string        `json:"片段"`
	Which   string        `json:"范围"`
	Count   int           `json:"选中数"`
	Notes   []vibNoteInfo `json:"音符信息"`
	Vibrato []string      `json:"颤音参数,omitempty"`
	Checks  []string      `json:"自检,omitempty"`
	Hint    string        `json:"提示,omitempty"`
}

func init() {
	register(&Tool{
		ID: "vibrato.read", Category: "工程",
		Title: "读选中的音符（颤音模板 / 目标）",
		Desc: "读**当前 ACE 编辑器选区**里的音符基本信息（uuid / 歌词 / 音高 / 片段内位置 / 时长，来自 " +
			"`clip note-content`）。给了 `path` 就再从工程文件里把它们的 **`vibrato` 颤音参数**读出来" +
			"（起始 / 频率 / 幅度 / 相位 / 进入 / 退出）—— 颤音在文件里，CLI 上读不到。\n" +
			"`single`（默认勾）= 只许**恰好一个**音符（当模板用）；不勾 = 一批（当目标用）。只读。",
		Kind:   KindRead,
		Source: vibSource,
		Params: []Param{
			textParam("path", "工程文件（.acep，留空 = 不读颤音参数）", "", `C:\…\名字\名字.acep`,
				"← 页面上一步「保存」会给出来"),
			checkParam("single", "只许恰好一个音符（当模板）", true),
		},
		Inspect: vibratoReadInspect,
	})

	register(&Tool{
		ID: "vibrato.apply", Category: "工程",
		Title: "把模板颤音复制到目标音符（文件路线）",
		Desc: "把**模板音符**的 `notes[].vibrato` 复制到**目标音符**上（技能 acep §2.2 画法③）。" +
			"⚠️ 执行会**原地**替换工程文件（原文件先备份到 `%TEMP%\\ACETool\\acep-backup\\`，写完逐字节回读核对）。" +
			"纪律：先「保存工程」→ 执行 → 让 ACE 重新打开。\n" +
			"⚠️ 只在**模板所在的片段内**复制（跨片段的 udid 如实报跳过）；模板在文件里没有颤音时**拒绝**。",
		Kind:   KindWrite,
		Source: vibSource,
		Params: []Param{
			textParam("path", "工程文件（.acep）", "", `C:\…\名字\名字.acep`, "← 页面上一步「保存」会给出来"),
			textParam("template", "模板音符 UUID", "", "{…}", "← 页面「保存预设」那一步存下来的"),
			textParam("targets", "目标音符 UUID（逗号分隔）", "", "{…},{…}", "← 页面「保存」那一步从 ACE 选区读来的"),
		},
		Plan:  vibratoApplyPlan,
		Apply: vibratoApplyApply,
	})

	register(&Tool{
		ID: "selection.clear", Category: "工程",
		Title: "清空编辑器里的音符选区",
		Desc: "清空编辑器（钢琴卷帘）的音符选区：" +
			"`selection set --scope editor --mode replace --notes-to-select []`（实测空数组可用，回包 `notesSelected: 0`）。\n" +
			"用途：颤音模板流程里「保存预设」之后要**清除选中**，否则第一个音符还挂在选区里、" +
			"第二次批量选会把模板一起带上。只动选区，不动任何音符。",
		Kind:   KindWrite,
		Source: "acestudio-cli 0.17.0 `help selection`（只有 get/set 两条；set 收空数组）+ 实测",
		Build: func(_ context.Context, _ ReadFunc, _ map[string]string) ([]Call, error) {
			return []Call{
				{Label: "清空选区", Args: []string{"selection", "set", "--scope", "editor", "--mode", "replace",
					"--notes-to-select", "[]"}},
				{Label: "回读选区", Args: []string{"selection", "get", "--scope", "editor"}},
			}, nil
		},
	})
}

func vibratoReadInspect(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	ids, err := readEditorSelection(ctx, rd)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("ACE 里没有选中音符 —— 先在编辑器里选中（模板要**恰好一个**音符，目标要一批）再点")
	}
	if flagOn(vals, "single", true) && len(ids) != 1 {
		return nil, fmt.Errorf("模板只能是**一个**音符，现在选中了 %d 个 —— 只选一个再来", len(ids))
	}

	// 整个片段（拿到 uuid/pos/dur/pitch/lyric），再按选区挑
	sc, err := readNoteScope(ctx, rd, false)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var picked []xcopyNote
	for _, n := range sc.Notes {
		if want[n.NoteUUID] {
			picked = append(picked, n)
		}
	}
	if len(picked) == 0 {
		return nil, fmt.Errorf("选区里的 %d 个音符，一个都不在当前片段「%s」里（%d 个音符中）—— "+
			"多半是编辑器换了片段而选区没跟上，重开一下片段再试", len(ids), sc.ClipName, sc.All)
	}

	rep := &vibReadReport{
		Track: fmt.Sprintf("轨 %d", sc.TrackIndex),
		Clip:  sc.ClipName,
		Which: fmt.Sprintf("选中的 %d 个音符（当前片段「%s」共 %d 个）", len(picked), sc.ClipName, sc.All),
		Count: len(picked),
	}
	for _, n := range picked {
		rep.Notes = append(rep.Notes, vibNoteInfo{UUID: n.NoteUUID, Lyric: n.Lyric, Pitch: n.Pitch,
			Pos: ticksToBeats(n.Pos), Dur: ticksToBeats(n.Dur)})
	}

	p := acepPathParam(vals)
	if p == "" {
		rep.Hint = "没给工程文件路径 ⇒ 只读了基本信息 —— 颤音参数在 `.acep` 文件里，" +
			"页面「保存」那一步会把路径一并读出来。"
		return rep, nil
	}
	_, root, err := openACEPRoot(p)
	if err != nil {
		return nil, err
	}
	rep.Path = p
	for _, n := range picked {
		label := fmt.Sprintf("%s（音高 %d，片段内第 %s 拍）", orDash(n.Lyric), n.Pitch, beatLabel(n.Pos))
		_, note, err := findNoteByUUID(root, n.NoteUUID)
		if err != nil {
			rep.Checks = append(rep.Checks, label+"：工程文件里找不到这个音符（uuid 对不上？先「保存工程」）")
			continue
		}
		vib := note.Get("vibrato")
		if vib == nil {
			rep.Vibrato = append(rep.Vibrato, label+"：**没有颤音**（文件里连 `vibrato` 键都没有）")
			continue
		}
		rep.Vibrato = append(rep.Vibrato, label+"："+vibratoSummary(vib))
	}
	return rep, nil
}

// readEditorSelection 读 `selection get --scope editor` 的 uuid 列表（**没选中就返回空**，不抛错）。
//
// ⚠️ 字段名不对称（技能/代码里记的那条）：`selection get` 回的是 **`noteUuid`**，
// 而 `selection set` 收的是 **`uuid`** —— 两个都认，兜底不至于全丢。
func readEditorSelection(ctx context.Context, rd ReadFunc) ([]string, error) {
	var sel struct {
		HasSelection   bool `json:"hasSelection"`
		SelectionCount int  `json:"selectionCount"`
		Notes          []struct {
			NoteUUID string `json:"noteUuid"`
			UUID     string `json:"uuid"`
		} `json:"notes"`
	}
	if err := readJSON(ctx, rd, &sel, "selection", "get", "--scope", "editor"); err != nil {
		return nil, fmt.Errorf("读音符选区（`selection get --scope editor`）：%w", err)
	}
	var ids []string
	for _, n := range sel.Notes {
		id := n.NoteUUID
		if id == "" {
			id = n.UUID
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
	if !sel.HasSelection {
		return nil, nil
	}
	return ids, nil
}

// ---------- ② 写：模板 → 目标 ----------

func vibratoApplyPlan(_ context.Context, _ ReadFunc, vals map[string]string) (any, error) {
	plan, err := buildVibratoPlan(vals)
	if err != nil {
		return nil, err
	}
	if plan.newPay == nil {
		plan.rep.Hint = "没算出要写的东西 —— 一个字节都不会改。"
		return plan.rep, nil
	}
	plan.rep.Hint = "**一个字节都没写**（这是" + "计划" + "那一步）。要改就走执行那一步。"
	return plan.rep, nil
}

func vibratoApplyApply(_ context.Context, _ ReadFunc, vals map[string]string) (any, error) {
	plan, err := buildVibratoPlan(vals)
	if err != nil {
		return nil, err
	}
	return applyPitchPlan(plan)
}

func buildVibratoPlan(vals map[string]string) (*pitchPlan, error) {
	p := acepPathParam(vals)
	if p == "" {
		return nil, fmt.Errorf("要一个工程文件路径（path）—— 页面那一步「保存」会把它给你（`project save` 的 savedPath）")
	}
	if st, err := os.Stat(p); err != nil {
		return nil, fmt.Errorf("读不到工程文件：%w", err)
	} else if st.IsDir() {
		return nil, fmt.Errorf("%s 是文件夹 —— 要给里面的 .acep 文件", p)
	}
	tpl := strings.Trim(strings.TrimSpace(value(vals, "template", "")), `"`)
	if normUUID(tpl) == "" {
		return nil, fmt.Errorf("要模板音符的 UUID（template）—— 页面「保存预设」那一步会给出来")
	}
	targets := splitUUIDs(value(vals, "targets", ""))
	if len(targets) == 0 {
		return nil, fmt.Errorf("要目标音符的 UUID 列表（targets）—— 页面「保存」那一步从 ACE 选区读来")
	}

	cont, root, err := openACEPRoot(p)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	plan := &pitchPlan{rep: &pitchCopyReport{DryRun: true, Path: p}, cont: cont, raw: raw}

	tplPat, tplNote, err := findNoteByUUID(root, tpl)
	if err != nil {
		return nil, fmt.Errorf("模板音符在工程文件里找不到（%s）：%w —— 先「保存工程」把 ACE 里的状态落盘", tpl, err)
	}
	vib := tplNote.Get("vibrato")
	if vib == nil {
		return nil, fmt.Errorf("模板音符在文件里**没有颤音**（`notes[].vibrato` 键都不存在）⇒ 不复制。" +
			"技能 §2.2③：没有颤音的音符连键都没有 —— 先在 ACE 里给这个音符画/调出颤音、" +
			"「保存工程」，再来一次（本工具**不猜**成「把目标的颤音清掉」）")
	}
	plan.rep.Src = "模板：" + vibNoteLabel(tplNote)
	plan.rep.Dst = fmt.Sprintf("目标 %d 个音符", len(targets))
	plan.rep.Clip = fmt.Sprintf("片段「%s」", clipNameOf(tplPat))

	var edits []cbor.Edit
	hit := 0
	var miss []string
	tplKey := normUUID(tpl)
	for _, id := range targets {
		if normUUID(id) == tplKey {
			continue // 目标就是模板本身 ⇒ 已经是它了，不必写
		}
		pat, note, err := findNoteByUUID(root, id)
		if err != nil {
			miss = append(miss, shortUUID(id)+"（工程文件里没有）")
			continue
		}
		if pat != tplPat {
			miss = append(miss, shortUUID(id)+"（不在模板所在的片段里；本工具只在同一片段内复制）")
			continue
		}
		pos, _ := nodeInt(note.Get("pos"))
		edits = append(edits, cbor.Edit{
			Start: note.Start, End: note.End, Bytes: cbor.EncodeNode(setVibrato(note, vib)),
			What: fmt.Sprintf("颤音：写到第 %s 拍那个音符上", beatLabel(pos)),
		})
		hit++
	}
	if hit == 0 {
		return nil, fmt.Errorf("一个目标音符都没落到模板所在的片段里（模板在片段「%s」；跳过：%s）—— "+
			"第二次选的音符要和模板在**同一个片段**里", clipNameOf(tplPat), strings.Join(miss, "、"))
	}
	plan.edits = edits
	plan.rep.Vibrato = pitchKindReport{On: true,
		Summary: fmt.Sprintf("把模板颤音写到 **%d 个**音符上（跳过 %d 个）", hit, len(miss))}
	if len(miss) > 0 {
		plan.rep.Checks = append(plan.rep.Checks, "跳过："+strings.Join(miss, "、"))
	}
	plan, err = finishPitchPlan(plan)
	if err != nil {
		return nil, err
	}
	return plan, nil
}

// setVibrato 返回"把 `vibrato` 键换成 vib"的那份 note map（**值语义重编码**：map 头可能要 +1）。
func setVibrato(note, vib *cbor.Node) *cbor.Node {
	keys := append([]*cbor.Node(nil), note.Keys...)
	vals := append([]*cbor.Node(nil), note.Values...)
	for i, k := range keys {
		if k.KeyString() == "vibrato" {
			vals[i] = vib
			return &cbor.Node{Kind: cbor.KMap, Keys: keys, Values: vals}
		}
	}
	keys = append(keys, &cbor.Node{Kind: cbor.KText, Text: "vibrato"})
	vals = append(vals, vib)
	return &cbor.Node{Kind: cbor.KMap, Keys: keys, Values: vals}
}

// ---------- ③ 文件里按 uuid 找音符 ----------

// findNoteByUUID 在 `tracks[*].patterns[*].notes[*]` 里按 uuid 找音符，回 (所在片段, 音符)。
//
// ⚠️ uuid 在两边的写法可能差 `{}` 和大写（CLI 回 `{…}`，文件里也是文本）⇒ **归一化后比**
// （去花括号 + 小写），不靠肉眼。
func findNoteByUUID(root *cbor.Node, id string) (*cbor.Node, *cbor.Node, error) {
	want := normUUID(id)
	if want == "" {
		return nil, nil, fmt.Errorf("空 UUID")
	}
	tracks := root.Get("tracks")
	if tracks == nil || tracks.Kind != cbor.KArray {
		return nil, nil, fmt.Errorf("payload 里没有 tracks")
	}
	for _, tr := range tracks.Items {
		pats := tr.Get("patterns")
		if pats == nil || pats.Kind != cbor.KArray {
			continue
		}
		for _, pat := range pats.Items {
			notes := pat.Get("notes")
			if notes == nil || notes.Kind != cbor.KArray {
				continue
			}
			for _, n := range notes.Items {
				if normUUID(nodeText(n.Get("uuid"))) == want {
					return pat, n, nil
				}
			}
		}
	}
	return nil, nil, fmt.Errorf("工程文件里没有 uuid 为 %s 的音符", shortUUID(id))
}

func normUUID(s string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(s), `"{}`))
}

func shortUUID(id string) string {
	s := normUUID(id)
	if len(s) > 8 {
		return "…" + s[len(s)-8:]
	}
	return s
}

func splitUUIDs(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	}) {
		key := normUUID(part)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, strings.Trim(strings.TrimSpace(part), `"`))
	}
	return out
}

// ---------- ④ 人话摘要 ----------

func vibNoteLabel(n *cbor.Node) string {
	p, _ := nodeInt(n.Get("pitch"))
	pos, _ := nodeInt(n.Get("pos"))
	return fmt.Sprintf("%s（音高 %d，片段内第 %s 拍）", orDash(nodeText(n.Get("lyric"))), p, beatLabel(pos))
}

// vibratoSummary 把 `notes[k].vibrato` 的 8 个字段说成人话（技能 §2.2③ 的字段名）。
func vibratoSummary(v *cbor.Node) string {
	if v == nil {
		return "没有颤音"
	}
	if v.Kind != cbor.KMap {
		return "有颤音（不是对象 —— 原样复制）"
	}
	var parts []string
	if x, ok := nodeFloat(v.Get("startPos")); ok {
		parts = append(parts, fmt.Sprintf("起始：音符内第 %s 拍", trimFloat(ticksToBeats(int64(x)))))
	}
	if x, ok := nodeFloat(v.Get("frequency")); ok {
		parts = append(parts, fmt.Sprintf("频率 %.2f Hz", x))
	}
	if x, ok := nodeFloat(v.Get("amplitude")); ok {
		parts = append(parts, fmt.Sprintf("幅度 %.3f 半音", x))
	}
	if x, ok := nodeFloat(v.Get("phase")); ok {
		parts = append(parts, fmt.Sprintf("相位 %.2f", x))
	}
	if x, ok := nodeFloat(v.Get("attackRatio")); ok {
		parts = append(parts, fmt.Sprintf("进入比例 %.2f", x))
	}
	if x, ok := nodeFloat(v.Get("attackLevel")); ok {
		parts = append(parts, fmt.Sprintf("进入电平 %.2f", x))
	}
	if x, ok := nodeFloat(v.Get("releaseRatio")); ok {
		parts = append(parts, fmt.Sprintf("退出比例 %.2f", x))
	}
	if x, ok := nodeFloat(v.Get("releaseLevel")); ok {
		parts = append(parts, fmt.Sprintf("退出电平 %.2f", x))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("有颤音对象（%d 个字段，名字都不认识 —— 原样复制）", len(v.Keys))
	}
	return strings.Join(parts, " · ")
}

func nodeFloat(n *cbor.Node) (float64, bool) {
	if n == nil {
		return 0, false
	}
	switch n.Kind {
	case cbor.KFloat:
		return n.Float, true
	case cbor.KUint:
		return float64(n.Uint), true
	case cbor.KInt:
		return float64(n.Int), true
	}
	return 0, false
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// acepPathParam 取 path 参数（去引号/空白）。
func acepPathParam(vals map[string]string) string {
	return strings.Trim(strings.TrimSpace(value(vals, "path", "")), `"`)
}
