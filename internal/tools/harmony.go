package tools

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// 移植 `和声.js`（AKD「律制-和声」，211 行）。
//
// 算法部分**纯可移**，而且原脚本那套挺巧：
//
//	① 自动检测调式 `Mode()`：对 C 起算的 12 个旋转 i，数"把所有音高减 i 之后落在黑键上"的音符个数，
//	   取**最少**的那个 i ⇒ 那就是大调的调（调内音全落在白键上）。
//	② 和声音程 = **音级（diatonic step）**数，不是半音数：
//	   `yincheng = [2,5,4,3,1,6,0]` 对应 [3度,6度,5度,4度,2度,7度,1度]。
//	③ 每个音符**一个半音一个半音**往指定方向走，**只有落在调内（相对调不是黑键）才算一步**，走够就停。
//	④ `bimian`（避免不协和）：按"根音音级 + 音程 + 方向"给某些音多走/少走一步。
//
// ⛔ 移植时改了**一个功能点的落地方式**（理由在下面）：
//
//	原脚本的「将和声移动到新建音符组」（**SV 的说法**）= 克隆选区音符到一个新 NoteGroup（**原声与和声都在**）。
//	ACE **没有"音符组"这一层**（ACE 的最小单位是**片段** clip），而且同一条轨上新建片段**不能重叠** ——
//	`clip create --on-occupied` 只有 `fail` / `cover` 两个值，而 `cover` 会把挡路的片段**裁掉**
//	（那就把源片段毁了）。⇒ 改成 **`track duplicate` 复制一条轨，把和声写在那条轨上**：
//	音色/声源/FX 全跟着复制过来（不用再挑声库），而"原声 + 和声都在"这个本意完全一致。
//
// ⚠️ 与 §23.4 同源：`note move --pitch` 是**刚性移动**，
// 所以按 **(当前音高, 目标音高)** 分组，绝不能按目标音高分组。

// blackKey 是 SV 的 `SV.blackKey`：黑键 = 音级 ∈ {1,3,6,8,10}。
// （负数也要给对 —— Go 的 `%` 对负数回负值，所以先归一到 0..11。）
func blackKey(pitch int) bool {
	switch ((pitch % 12) + 12) % 12 {
	case 1, 3, 6, 8, 10:
		return true
	}
	return false
}

// detectKey 照原脚本的 `Mode()`：12 个旋转里挑"落在黑键上的音符最少"的那个。
// 命中数一并带回去 —— 结果里要能给人看"为什么判成这个调"，否则自动检测错了没法查。
//
// ⚠️ 返回的 `ties` 是**并列**的那些旋转（含被选中的那个）。
// 原脚本是 `if (k[i] < k[kmin]) kmin = i` ⇒ 并列时取**下标最小**的 —— 这是**投硬币**，
// 而且实测常常并列（旋律不严格落在任何大调上时，最低分可能是 2 分、五六个旋转并列）。
// 所以移植时**不算完就完**：把并列情况报出来，让人知道"这个调不唯一，建议手动指定"。
func detectKey(pitches []int) (int, []int, []int) {
	scores := make([]int, 12)
	for i := 0; i < 12; i++ {
		for _, p := range pitches {
			if blackKey(p - i) {
				scores[i]++
			}
		}
	}
	kmin := 0
	for i := 1; i < 12; i++ {
		if scores[i] < scores[kmin] { // ⚠️ 严格小于：并列时取**下标小**的（照原脚本）
			kmin = i
		}
	}
	var ties []int
	for i := 0; i < 12; i++ {
		if scores[i] == scores[kmin] {
			ties = append(ties, i)
		}
	}
	return kmin, scores, ties
}

// diatonicWalk 照原脚本那段 while：一个半音一个半音往 dir 走，
// **只有落在调内**（相对 key 不是黑键）才计一步；走够 steps 步就停。
//
// 与原脚本的两处差异（都是 ACE 侧的硬约束）：
//   - 原脚本用 `safety > 127` 兜死循环；这里按 **MIDI 合法域** 夹，越界即停并**如实报出来**。
//   - 原脚本直接 `setPitch` 出去、越界交给 SV 拒；ACE 音高是 0..127，越界会被拒 ⇒ 这里夹到域内并标记。
func diatonicWalk(pitch, key, dir, steps int) (int, bool) {
	p := pitch
	for j := 0; j < steps; {
		p += dir
		if p < 0 || p > 127 {
			if p < 0 {
				return 0, true
			}
			return 127, true
		}
		if !blackKey(p - key) {
			j++
		}
	}
	return p, false
}

// 原脚本的两个查表 + 名字。
var (
	harmonySteps = []int{2, 5, 4, 3, 1, 6, 0} // [3度,6度,5度,4度,2度,7度,1度]
	harmonyDirs  = []int{1, -1}               // [向上, 向下]
	harmonyNames = []string{"3度", "6度", "5度", "4度", "2度", "7度", "1度"}
	keyNames     = []string{"C Maj/A min", "C# Maj/A# min", "D Maj/B min", "D# Maj/C min",
		"E Maj/C# min", "F Maj/D min", "F# Maj/D# min", "G Maj/E min",
		"G# Maj/F min", "A Maj/F# min", "A# Maj/G min", "B Maj/G# min"}
)

// avoidAdjust 是原脚本 `bimian` 那段的**直译**：按"根音音级 + 音程 + 方向"决定多走还是少走一步。
// 原脚本只给 3 度 / 6 度写了两档，这里也只管这两个（其他音程一律 0）。
func avoidAdjust(pitchClass, intervalIdx, dirIdx int) int {
	if intervalIdx == 0 { // 3 度
		if dirIdx == 0 { // 向上
			if pitchClass == 2 || pitchClass == 7 {
				return 1
			}
		} else {
			if pitchClass == 5 || pitchClass == 9 {
				return 1
			}
		}
	}
	if intervalIdx == 1 { // 6 度
		if dirIdx == 1 { // 向下
			if pitchClass == 2 || pitchClass == 7 {
				return -1
			}
		} else {
			if pitchClass == 5 || pitchClass == 9 {
				return -1
			}
		}
	}
	return 0
}

type harmonyMove struct {
	// 就地改时是**要改的那个音符**；复制轨模式时是**源音符**（新轨的 uuid 要复制完才知道，
	// 而干跑不许复制 —— 所以计划里给"位置+音高"，真写时才映射到新轨的 uuid）。
	NoteUUID string `json:"noteUuid"`
	Pos      int64  `json:"pos"` // clip-local
	// Abs = 工程绝对位置（整轨范围跨片段时用来在新轨上找对应音符）；单片段时可为 0。
	Abs   int64  `json:"absTick,omitempty"`
	Lyric    string `json:"lyric,omitempty"`
	From     int    `json:"fromPitch"`
	To       int    `json:"toPitch"`
	Adj      int    `json:"dissonanceAdjust,omitempty"`
	Clamped  bool   `json:"clampedToMidiRange,omitempty"`
}

type harmonyReport struct {
	Scope noteScopeView `json:"scope"`

	KeyHow      string `json:"keyFrom"`
	Key         int    `json:"key"`
	KeyName     string `json:"keyName"`
	AutoKey     int    `json:"autoDetectedKey"`
	AutoKeyName string `json:"autoDetectedKeyName"`
	KeyScores   []int  `json:"keyBlackKeyScores,omitempty"` // 12 个旋转的黑键命中数
	KeyTies     []int  `json:"keyTiedRotations,omitempty"`  // 并列最低分的旋转（>1 个 ⇒ 自动检测不唯一）

	Direction string `json:"direction"`
	Interval  string `json:"interval"`
	Steps     int    `json:"diatonicSteps"`
	Avoid     bool   `json:"adjustDissonance"`
	Target    string `json:"target"`

	Scanned int `json:"scanned"`
	Will    int `json:"willMove"`
	Same    int `json:"unchanged"`

	Moves    []harmonyMove `json:"moves,omitempty"`
	Skips    []string      `json:"skipped,omitempty"`
	NewTrack *dupTrack     `json:"newTrack,omitempty"`

	Calls  []string `json:"plannedCalls,omitempty"`
	Did    []string `json:"done,omitempty"`
	DryRun bool     `json:"dryRun,omitempty"`
	Note   string   `json:"note,omitempty"`
}

func init() {
	register(&Tool{
		ID: "tuning.harmony", Category: "音符",
		Title: "生成和声（音阶级进）",
		Desc: "移植 `和声.js`：把**选中的音符**（没选中就是整个当前片段）按**音级**移动到和声音程上。" +
			"调式可**自动检测**（原脚本的 `Mode()`：12 个旋转里挑落在黑键上最少的那个）或手动指定。" +
			"⚠️ 和声音程是**音级步数**、不是半音数（3度=2步、6度=5步…）：每个音符一个半音一个半音走，" +
			"**只有落在调内才算一步**。" +
			"⚠️ 与原脚本**不同的落地方式**：「放到新音符组」（SV 的说法）在 ACE 里改成**复制一条轨**再写和声 —— " +
			"ACE 没有那一层（ACE 的最小单位是**片段**），而同轨新建片段不能重叠（那个开关只有 fail/cover，cover 会**裁掉挡路的片段**＝毁掉源片段）；" +
			"复制轨把音色/声源/FX 一起带过来，而「原声 + 和声都在」这个本意不变。" +
			"**干跑默认**：勾着只出计划（含每个音符 from→to），一个字节都不写。",
		Kind:   KindWrite,
		Source: "AKD `和声.js`",
		Params: []Param{
			{Name: "key", Label: "调式", Type: ParamSelect, Default: "auto",
				Options: append([]Option{{Value: "auto", Label: "自动检测（照原脚本 Mode()）"}}, keyOptions()...)},
			{Name: "direction", Label: "和声方向", Type: ParamSelect, Default: "up",
				Options: []Option{{Value: "up", Label: "向上"}, {Value: "down", Label: "向下"}}},
			{Name: "interval", Label: "和声音程", Type: ParamSelect, Default: "3rd",
				Options: []Option{
					{Value: "3rd", Label: "三度"}, {Value: "6th", Label: "六度"},
					{Value: "5th", Label: "五度"}, {Value: "4th", Label: "四度"},
					{Value: "2nd", Label: "二度"}, {Value: "7th", Label: "七度"},
					{Value: "1st", Label: "一度（不动）"},
				}},
			checkParam("avoid", "为不协和的三度/六度调整一步", false),
			textParam("newTrackIndex", "新轨放在第几行（0 起；留空=紧跟原轨）", "", "0",
				"只在 target=newTrack 时用：复制完用 track reorder --to-index 挪过去"),
			textParam("beginBar", "开始小节（scope=bars 时用，1 起）", "", "1", "与 endBar 一起限定范围"),
			textParam("endBar", "结束小节（含）", "", "4", "与 beginBar 一起限定范围"),
			{Name: "target", Label: "写到哪里", Type: ParamSelect, Default: "inplace",
				Options: []Option{
					{Value: "inplace", Label: "就地改（把选中音符本身变成和声）"},
					{Value: "newTrack", Label: "复制一条轨，和声写在新轨（保留原声）"},
				}},
			{Name: "scope", Label: "范围", Type: ParamSelect, Default: "auto",
				Options: []Option{
					{Value: "auto", Label: "选中的音符（没选中就整个片段）"},
					{Value: "clip", Label: "整个当前片段"},
				{Value: "bars", Label: "指定小节（beginBar..endBar，1 起、含两端）"},
				}},
		},
		Plan:  harmonyPlan,
		Apply: harmonyApply,
	})
}

// scoreOf 取某个旋转的黑键命中数（只用于把话说清楚；越界给 0）。
func scoreOf(scores []int, i int) int {
	if i < 0 || i >= len(scores) {
		return 0
	}
	return scores[i]
}

// rotationList 把并列的旋转写成 `1(C#)、2(D)…`（下标 + 名字，人读得懂）。
func rotationList(idxs []int) string {
	parts := make([]string, 0, len(idxs))
	for _, i := range idxs {
		name := ""
		if i >= 0 && i < len(keyNames) {
			name = keyNames[i]
		}
		parts = append(parts, fmt.Sprintf("%d(%s)", i, name))
	}
	return strings.Join(parts, "、")
}

func keyOptions() []Option {
	out := make([]Option, 0, 12)
	for i, n := range keyNames {
		out = append(out, Option{Value: strconv.Itoa(i), Label: n})
	}
	return out
}

func intervalIdx(v string) int {
	for i, s := range []string{"3rd", "6th", "5th", "4th", "2nd", "7th", "1st"} {
		if v == s {
			return i
		}
	}
	return 0
}

type harmonyPlanData struct {
	rep     *harmonyReport
	scope   *noteScope
	key     int
	dir     int
	target  string
	started int64
}

// analyzeHarmony **纯只读**：读范围、判调式、算每个音符的目标音高。
// ⛔ 复制轨那一步**不在这里**（干跑必须一个字节都不写）—— 它在 Apply 里，见 harmonyApply。
func analyzeHarmony(ctx context.Context, rd ReadFunc, vals map[string]string) (*harmonyPlanData, error) {
	scopeMode := value(vals, "scope", "auto")
	var noteAbs map[string]int64   // 整轨范围：noteUUID → 工程绝对 tick
	useSel := scopeMode == "auto"
	iv := intervalIdx(value(vals, "interval", "3rd"))
	dirIdx := 0
	if value(vals, "direction", "up") == "down" {
		dirIdx = 1
	}
	dir := harmonyDirs[dirIdx]
	avoid := truthy(value(vals, "avoid", ""))
	target := value(vals, "target", "inplace")

	sc, err := readNoteScope(ctx, rd, useSel)
	if err != nil {
		return nil, err
	}
	if scopeMode == "track" {
		// ⭐ 用户 2026-10-24：「范围应该允许当前整个轨道（多个片段）」
		clips, cerr := clipListBrief(ctx, rd, sc.TrackIndex)
		if cerr != nil {
			return nil, cerr
		}
		if len(clips) == 0 {
			return nil, fmt.Errorf("这条轨上没有片段")
		}
		var all []xcopyNote
		noteAbs = map[string]int64{}
		for _, c := range clips {
			nc, nerr := readClipNotes(ctx, rd, sc.TrackIndex, c)
			if nerr != nil {
				return nil, nerr
			}
			for _, n := range nc.Notes {
				noteAbs[n.NoteUUID] = c.Begin + n.Pos
				all = append(all, n)
			}
		}
		sc.Notes = all
		sc.All = len(all)
		sc.Which = fmt.Sprintf("整个轨道（%d 个片段、%d 个音符）", len(clips), len(all))
	}
	if scopeMode == "bars" {
		// 指定小节：绝对 tick 范围 → 只留落在里面的音符
		b1, e1 := 1, 0
		if n, err := strconv.Atoi(strings.TrimSpace(value(vals, "beginBar", "1"))); err == nil && n >= 1 {
			b1 = n
		}
		if n, err := strconv.Atoi(strings.TrimSpace(value(vals, "endBar", ""))); err == nil && n >= b1 {
			e1 = n
		}
		if e1 == 0 {
			return nil, fmt.Errorf("范围选了「指定小节」，但结束小节没给（beginBar=%d）", b1)
		}
		sigs := readSignatures(ctx, rd)
		lo := sigTickAt(b1-1, sigs)
		hi := sigTickAt(e1, sigs)
		kept := sc.Notes[:0]
		for _, n := range sc.Notes {
			abs := sc.Begin + n.Pos
			if abs >= lo && abs < hi {
				kept = append(kept, n)
			}
		}
		sc.Notes = kept
		sc.Which = fmt.Sprintf("第 %d–%d 小节（工程 tick %d..%d）", b1, e1, lo, hi)
	}
	if len(sc.Notes) == 0 {
		return nil, fmt.Errorf("片段「%s」里没有音符", sc.ClipName)
	}

	pitches := make([]int, 0, len(sc.Notes))
	for _, n := range sc.Notes {
		pitches = append(pitches, n.Pitch)
	}
	autoKey, scores, ties := detectKey(pitches)

	keyHow := "自动检测（原脚本 Mode()：12 个旋转里挑落在黑键上最少的那个）"
	key := autoKey
	if kv := value(vals, "key", "auto"); kv != "auto" && kv != "" {
		n, err := strconv.Atoi(kv)
		if err != nil || n < 0 || n > 11 {
			return nil, fmt.Errorf("调式只能是 auto 或 0..11（C..B），收到 %q", kv)
		}
		key, keyHow = n, "手动指定"
	}

	rep := &harmonyReport{
		Scope:       noteScopeView{Which: sc.Which, Clip: sc.ClipName, ClipType: sc.ClipType, LocatedBy: sc.Located, AllNotes: sc.All},
		KeyHow:      keyHow,
		Key:         key,
		KeyName:     keyNames[key],
		AutoKey:     autoKey,
		AutoKeyName: keyNames[autoKey],
		KeyScores:   scores,
		KeyTies:     ties,
		Direction:   map[int]string{1: "向上", -1: "向下"}[dir],
		Interval:    harmonyNames[iv],
		Steps:       harmonySteps[iv],
		Avoid:       avoid,
		Scanned:     len(sc.Notes),
	}
	if target == "newTrack" {
		rep.Target = "newTrack（先 `track duplicate` 复制一条轨，和声写在那条轨上）"
	} else {
		rep.Target = "inplace（就地改选中音符）"
	}

	for _, n := range sc.Notes {
		adj := 0
		if avoid {
			adj = avoidAdjust(((n.Pitch%12)+12)%12, iv, dirIdx)
		}
		steps := harmonySteps[iv] + adj
		if steps < 0 {
			steps = 0
		}
		to, clamped := diatonicWalk(n.Pitch, key, dir, steps)
		if to == n.Pitch {
			rep.Same++
			continue
		}
		rep.Moves = append(rep.Moves, harmonyMove{NoteUUID: n.NoteUUID, Pos: n.Pos, Abs: noteAbs[n.NoteUUID], Lyric: n.Lyric,
			From: n.Pitch, To: to, Adj: adj, Clamped: clamped})
	}
	sort.SliceStable(rep.Moves, func(i, j int) bool { return rep.Moves[i].Pos < rep.Moves[j].Pos })
	rep.Will = len(rep.Moves)

	// 计划里的命令：复制轨模式要**先**复制，再改新轨上的音符
	if target == "newTrack" && rep.Will > 0 {
		rep.Calls = append(rep.Calls, fmt.Sprintf(
			"track duplicate --track-index %d   （复制一条轨：音色/声源/FX 一起带过来）", sc.TrackIndex))
	}
	for _, g := range groupMoves(rep.Moves) {
		rep.Calls = append(rep.Calls, fmt.Sprintf("note move --note-uuid %s --pitch %d   （%d→%d，%d 个）",
			strings.Join(g.NoteID, " "), g.To, g.From, g.To, len(g.NoteID)))
	}
	return &harmonyPlanData{rep: rep, scope: sc, key: key, dir: dir, target: target}, nil
}

func groupMoves(moves []harmonyMove) []xcopyPitchGroup {
	var out []xcopyPitchGroup
	for _, m := range moves {
		merged := false
		for i := range out {
			if out[i].From == m.From && out[i].To == m.To {
				out[i].NoteID = append(out[i].NoteID, m.NoteUUID)
				merged = true
				break
			}
		}
		if !merged {
			out = append(out, xcopyPitchGroup{From: m.From, To: m.To, NoteID: []string{m.NoteUUID}})
		}
	}
	return out
}

func harmonyNote(rep *harmonyReport, dry bool) string {
	var b strings.Builder
	if dry {
		b.WriteString("干跑：一个字节都没写。")
	} else {
		b.WriteString("已写入。")
	}
	fmt.Fprintf(&b, " 调式 = **%s**（%s；自动检测给的是 %s）。方向 %s、音程 %s（= **%d 个音级步**）。",
		rep.KeyName, rep.KeyHow, rep.AutoKeyName, rep.Direction, rep.Interval, rep.Steps)
	if len(rep.KeyTies) > 1 {
		// ⛔ 不能装作唯一解：原脚本按下标最小 tie-break，那就是**投硬币**（实测常常并列）
		fmt.Fprintf(&b, " ⚠️ **自动检测不唯一**：最低分 %d 分，有 %d 个旋转并列（%s），"+
			"按原脚本的规则取了下标最小的那个 —— 这串旋律不严格落在任何大调上 ⇒ **建议手动指定调式**再跑。",
			scoreOf(rep.KeyScores, rep.AutoKey), len(rep.KeyTies), rotationList(rep.KeyTies))
	}
	fmt.Fprintf(&b, " 扫了 %d 个音符 ⇒ 会移 **%d** 个", rep.Scanned, rep.Will)
	if rep.Same > 0 {
		fmt.Fprintf(&b, "（另有 %d 个算出来跟原来一样，不动 —— 比如「一度」）", rep.Same)
	}
	b.WriteString("。")
	if rep.Avoid {
		b.WriteString(" 已开「避免不协和」的 ±1 步调整。")
	}
	if rep.NewTrack != nil {
		fmt.Fprintf(&b, " ⭐ 和声写在**复制出来的轨道 %d**「%s」上，原轨没动。", rep.NewTrack.TrackIndex, rep.NewTrack.TrackName)
	}
	return b.String()
}

func harmonyPlan(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	pd, err := analyzeHarmony(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	pd.rep.DryRun = true
	if pd.target == "newTrack" && pd.rep.Will > 0 {
		pd.rep.Skips = append(pd.rep.Skips,
			"⚠️ 复制轨模式下，**新轨的 uuid 要复制完才知道** ⇒ 干跑里 `note move` 给的是**源音符**的 uuid，"+
				"执行时会改成新轨上对应（同 pos、同音高）的音符。")
	}
	pd.rep.Note = harmonyNote(pd.rep, true)
	return pd.rep, nil
}

func harmonyApply(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	pd, err := analyzeHarmony(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	rep := pd.rep
	var done []string

	groups := groupMoves(rep.Moves)
	if pd.target == "newTrack" && len(rep.Moves) > 0 {
		// ⚠️ 真写才复制（干跑一个字节都不写）。复制完要把每个音符映射到**新轨**上的对应音符：
		// 按 (clip-local pos, 原音高) 找回 —— `track duplicate` 的语义就是"内容一模一样"。
		var dup dupTrack
		if err := readJSON(ctx, rd, &dup, "track", "duplicate", "--track-index", strconv.Itoa(pd.scope.TrackIndex)); err != nil {
			rep.Note = "复制轨道失败（`track duplicate`）—— 一个字节都没写。"
			return rep, fmt.Errorf("复制轨道：%w", err)
		}
		rep.NewTrack = &dup
		done = append(done, fmt.Sprintf("复制出轨道 %d「%s」", dup.TrackIndex, dup.TrackName))

		// ⭐ 用户 2026-10-24：「允许拖动轨道来改变新轨出现的位置」⇒ 复制完用 track reorder 挪过去。
		if idxRaw := strings.TrimSpace(value(vals, "newTrackIndex", "")); idxRaw != "" {
			if to, e := strconv.Atoi(idxRaw); e != nil || to < 0 {
				rep.Note = fmt.Sprintf("新轨位置只认 ≥0 的整数（收到 %q）⇒ 新轨留在原位置", idxRaw)
			} else if e2 := runWrite(ctx, rd, "track", "reorder", "--track-uuid", dup.TrackUUID,
				"--to-index", strconv.Itoa(to)); e2 != nil {
				done = append(done, fmt.Sprintf("⚠️ 把新轨挪到第 %d 行失败（%v）—— 它还在原位置", to, e2))
			} else {
				dup.TrackIndex = to
				done = append(done, fmt.Sprintf("把新轨挪到第 %d 行", to))
			}
		}

		clips, err := clipListBrief(ctx, rd, dup.TrackIndex)
		if err != nil {
			return rep, err
		}
		// 映射键 = (工程绝对 tick, 原音高)：整轨多片段时也能对上（同一片段内 pos 不唯一，但不能跨片段比）
		type absKey struct {
			abs   int64
			pitch int
		}
		byAbs := map[absKey]string{}
		for _, c := range clips {
			nc, nerr := readClipNotes(ctx, rd, dup.TrackIndex, c)
			if nerr != nil {
				return rep, nerr
			}
			for _, d := range nc.Notes {
				byAbs[absKey{c.Begin + d.Pos, d.Pitch}] = d.NoteUUID
			}
		}
		var mapped []harmonyMove
		for _, m := range rep.Moves {
			abs := m.Abs
			if abs == 0 {
				abs = pd.scope.Begin + m.Pos
			}
			found := byAbs[absKey{abs, m.From}]
			if found == "" {
				rep.Skips = append(rep.Skips, fmt.Sprintf(
					"@%d 音高 %d 在新轨上找不到对应音符，跳过", abs, m.From))
				continue
			}
			m.NoteUUID = found
			mapped = append(mapped, m)
		}
		rep.Will = len(mapped)
		groups = groupMoves(mapped)
	}

	for _, g := range groups {
		args := append([]string{"note", "move", "--note-uuid"}, g.NoteID...)
		args = append(args, "--pitch", strconv.Itoa(g.To))
		if err := runWrite(ctx, rd, args...); err != nil {
			rep.Did = done
			rep.Note = fmt.Sprintf("在（%d→%d 那组 %d 个音符）失败，**前面已经落的改动没有回滚**"+
				"（ACE 每个动词是独立撤销记录，Ctrl+Z 可逐步退回）。", g.From, g.To, len(g.NoteID))
			return rep, nil
		}
		done = append(done, fmt.Sprintf("%d 个音符 %d→%d", len(g.NoteID), g.From, g.To))
	}
	rep.Did = done
	rep.Note = harmonyNote(rep, false)
	return rep, nil
}

// sigTickAt 把 0 起的小节号换成**工程绝对 tick**（用 timesig list 的分段签名；没有签名就按 4/4 = 1920）。
func sigTickAt(bar int, sigs []sigEntry) int64 {
	if len(sigs) == 0 {
		return int64(bar) * 1920
	}
	cur := sigs[0]
	for _, sg := range sigs {
		if sg.BarPos <= bar {
			cur = sg
			continue
		}
		break
	}
	meas := int64(cur.Numerator) * 480 * 4 / int64(cur.Denominator)
	if meas <= 0 {
		meas = 1920
	}
	return cur.Tick + int64(bar-cur.BarPos)*meas
}
