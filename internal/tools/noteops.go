package tools

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// 音符编辑 / 歌词移位 —— 移植 SV 脚本 `AKD工具侧栏.js` 里用户点名的那几项。
//
// 原脚本是一排按钮，每个按钮一段回调；这里一项一个工具（`音符` / `歌词` 分类），
// 另外给一个同版式的按钮面板（`noteopsPanelHTML`）。
//
// ⚠️ 移植时**改变的一处形状**：原脚本这些回调大量依赖"**播放头**"
//（`SV.getPlayback().getPlayhead()`）与"**当前编辑器选区**"。
// ACE 侧对应的是 `transport state`（`positionTick`，**全局** tick）与
// `selection get --scope editor`（只给 uuid/pos/dur/pitch，**不给歌词** ⇒ 歌词要另读 `clip note-content`）。
// 音符的 `pos` 是**片段本地**坐标，所以比较前要把全局播放头减掉 `clipBegin`。
//
// ⚠️ 另一处：`note resize` **一次调用只能给一个 `--dur`**（对所有点名音符生效）。
// 所以"自动拼接"（每个音符的新时长都不一样）**不能一个音符一条命令** ——
// 那样 300 个音符就是 300 次调用（约 20 秒）。改成**按新时长分组**，一组一条命令。

// maxUUIDsPerCall 一次命令行里最多塞多少个 uuid。
// 300 个 uuid × 38 字符 ≈ 11 KB，Windows 命令行上限约 32 KB —— 留足余量。
const maxUUIDsPerCall = 100

// ---------- 共享读取 ----------

type selNote struct {
	UUID  string
	Pos   int64 // **片段本地** tick
	End   int64
	Dur   int64
	Pitch int
	Lyric string
}

type selContext struct {
	Ref      clipRef
	Playhead int64 // 全局 tick
	Local    int64 // 片段本地 tick（= Playhead - Ref.ClipBegin）
	All      []selNote
	Selected []selNote
	UsedAll  bool // 没选中时是否按原脚本退化成"整个片段"
}

// readSelContext 一次把这几样读齐：当前片段、片段音符（含歌词）、选区、播放头。
// wantAllFallback=true 时，没选中就退化成"片段全部"（原脚本多数回调就是这个语义）。
func readSelContext(ctx context.Context, rd ReadFunc, wantAllFallback bool) (*selContext, error) {
	ref, err := currentClip(ctx, rd)
	if err != nil {
		return nil, err
	}
	var clip struct {
		Notes []struct {
			Pos      int64  `json:"pos"`
			Dur      int64  `json:"dur"`
			EndPos   int64  `json:"endPos"`
			Pitch    int    `json:"pitch"`
			Lyric    string `json:"lyric"`
			NoteUUID string `json:"noteUuid"`
		} `json:"notes"`
	}
	if err := readJSON(ctx, rd, &clip, "clip", "note-content",
		"--track-index", strconv.Itoa(ref.Track), "--clip-index", strconv.Itoa(ref.Clip)); err != nil {
		return nil, fmt.Errorf("读片段音符失败：%w", err)
	}
	sc := &selContext{Ref: ref}
	byUUID := map[string]selNote{}
	for _, n := range clip.Notes {
		sn := selNote{UUID: n.NoteUUID, Pos: n.Pos, End: n.EndPos, Dur: n.Dur, Pitch: n.Pitch, Lyric: n.Lyric}
		sc.All = append(sc.All, sn)
		byUUID[n.NoteUUID] = sn
	}
	sort.SliceStable(sc.All, func(i, j int) bool { return sc.All[i].Pos < sc.All[j].Pos })

	selected, _ := readSelectedNoteUUIDs(ctx, rd)
	for _, n := range sc.All {
		if selected[n.UUID] {
			sc.Selected = append(sc.Selected, n)
		}
	}

	if ph, err := readPlaybackHead(ctx, rd); err == nil {
		sc.Playhead = ph
		sc.Local = ph - ref.ClipBegin
	} else {
		return nil, fmt.Errorf("读播放头失败（`transport state`）：%w", err)
	}

	if len(sc.Selected) == 0 {
		if !wantAllFallback {
			return nil, fmt.Errorf("这些操作要先**在 ACE 里选中音符**（当前编辑器选区是空的）—— " +
				"可以先用「选中之后/之前的所有音符」，或在钢琴窗里框选")
		}
		sc.Selected = sc.All
		sc.UsedAll = true
	}
	return sc, nil
}

// readPlaybackHead 读**播放头**（`transport state` 的 `positionTick`，**全局** tick）。
//
// ⚠️ 与 find.go 的 `readPlayhead` **不是一回事**，别互相替换：
//
//	· 这个 = 播放头（`transport state`），原脚本 `SV.getPlayback().getPlayhead()` 要的就是它；
//	· find.go 那个 = **标记线**（`caret get --scope arrangement`）。
//
// 停止状态下两者通常重合，所以 find 那边用标记线当近似没出过问题；
// 但"选中播放头之后的音符"这类操作必须用**真播放头**，否则播着的时候会选错。
func readPlaybackHead(ctx context.Context, rd ReadFunc) (int64, error) {
	var ts struct {
		PositionTick int64  `json:"positionTick"`
		Status       string `json:"status"`
	}
	if err := readJSON(ctx, rd, &ts, "transport", "state"); err != nil {
		return 0, err
	}
	return ts.PositionTick, nil
}

// playheadIndex 找播放头落在哪个音符上。
//
// ⭐ 用户 2026-10-24 的裁定（比 JS 的严格区间更宽容）：
//   · 点在音符**里面**（onset ≤ ph < end，压在起点算本音符）⇒ 这个音符；
//   · 正好压在前一个音符的结尾、后面还有音符 ⇒ 取**后一个**；
//   · 落在真正的**空隙**里（不在任何音符的终点上）⇒ -1（没有目标）；
//   · 播放头在最后一个音符之后 ⇒ -1（没有目标，工具如实报）。
// 入参必须按 Pos 升序（readSelContext 已经排过）。
func playheadIndex(notes []selNote, local int64) int {
	for i, n := range notes {
		if n.Pos <= local && local < n.End {
			return i
		}
	}
	// 正好压在某个音符的**终点**上：有后一个音符 ⇒ 取后一个；没有（最后一个音符）⇒ 取当前
	for i, n := range notes {
		if n.End == local {
			for j := i + 1; j < len(notes); j++ {
				if notes[j].Pos > local {
					return j
				}
			}
			return i
		}
	}
	// 落在空隙里（不在任何音符的终点上）⇒ **没有目标**（用户 2026-10-24：「落在空隙里算没有目标」）
	return -1
}

// chunkUUIDs 把 uuid 切成每段不超过 maxUUIDsPerCall 段（命令行长度护栏）。
func chunkUUIDs(ids []string) [][]string {
	if len(ids) == 0 {
		return nil
	}
	var out [][]string
	for i := 0; i < len(ids); i += maxUUIDsPerCall {
		j := i + maxUUIDsPerCall
		if j > len(ids) {
			j = len(ids)
		}
		out = append(out, ids[i:j])
	}
	return out
}

// noteRefArgs 拼 `--note-uuid a --note-uuid b …`。
func noteRefArgs(ids []string) []string {
	args := make([]string, 0, len(ids)*2)
	for _, id := range ids {
		args = append(args, "--note-uuid", id)
	}
	return args
}

func init() {
	// ---------- 自动拼接 ----------
	register(&Tool{
		ID: "note.join", Category: "音符",
		Title: "自动拼接音符",
		Desc: "对应原脚本的「自动拼接音符」：把选中（没选中就用**整个片段**）的音符按起点排序，" +
			"把前一个的**时长**改到下一个的起点 ⇒ 消除所有间隙。**最后一个音符的时长不动**（照原脚本）。" +
			"⚠️ `note resize` 一次调用只能给一个 `--dur`，所以这里是**按新时长分组**发命令" +
			"（一组一条），不是一音符一条 —— 否则 300 个音符要跑 20 秒。",
		Kind:   KindWrite,
		Source: "AKD工具侧栏.js 的 joinNotesButton",
		Build:  buildJoinNotes,
	})

	// ---------- 按播放头选音符 ----------
	register(&Tool{
		ID: "select.playhead", Category: "音符",
		Title: "选中播放头之后 / 之前的音符",
		Desc: "对应原脚本的「选中之后的所有音符」，外加它没有的**对称项「之前」**。" +
			"判据照原脚本：**之后** = 音符的 `结束 > 播放头`（含正在唱的那个）；" +
			"**之前** = 音符的 `起点 < 播放头`。⚠️ 用的是 **`transport state` 的播放头**" +
			"（`positionTick`，全局 tick），不是标记线 —— 原脚本也是 `getPlayhead()`。" +
			"⚠️ 一条都没命中时**不**去清空你已有的选区，而是明确报错。",
		Kind:   KindWrite,
		Source: "AKD工具侧栏.js 的 selectAfterPlayheadButton（+ 对称的 before）",
		Params: []Param{
			{Name: "side", Label: "选哪边", Type: ParamSelect, Default: "after",
				Options: []Option{
					{Value: "after", Label: "播放头之后（结束 > 播放头，含正在唱的）"},
					{Value: "before", Label: "播放头之前（起点 < 播放头）"},
					{Value: "at", Label: "当前（只选播放头所在的那一个音符）"},
				}},
		},
		Build: buildSelectPlayhead,
	})

	// ---------- 复制歌词 ----------
	register(&Tool{
		ID: "lyric.copy", Category: "歌词",
		Title: "复制歌词",
		Desc: "对应原脚本的「复制歌词」：把选中（没选中就用整个片段）音符的歌词用**空格**拼接起来。" +
			"⚠️ 只读：**写系统剪贴板是面板干的**（`navigator.clipboard`），" +
			"这个工具只把文本回给你 —— 所以命令行直接调它也不会碰到你的剪贴板。",
		Kind:   KindRead,
		Source: "AKD工具侧栏.js 的 copyLyricsButton",
		Inspect: func(ctx context.Context, rd ReadFunc, _ map[string]string) (any, error) {
			sc, err := readSelContext(ctx, rd, true)
			if err != nil {
				return nil, err
			}
			parts := make([]string, 0, len(sc.Selected))
			lyricNotes := make([]map[string]any, 0, len(sc.Selected))
			for _, n := range sc.Selected {
				parts = append(parts, n.Lyric)
				lyricNotes = append(lyricNotes, map[string]any{
					"noteUuid": n.UUID, "lyric": n.Lyric, "pos": n.Pos, "dur": n.Dur})
			}
			return map[string]any{
				"text":     strings.Join(parts, " "),
				"count":    len(parts),
				"usedAll":  sc.UsedAll,
				"scope":    scopeWord(sc.UsedAll),
				"clipName": sc.Ref.ClipName,
				"notes":    lyricNotes,
				"note": "面板会把 text 写进系统剪贴板（浏览器剪贴板 API）；" +
					"命令行调用只回文本、不动剪贴板。",
			}, nil
		},
	})

	// ---------- 粘贴歌词 ----------
	register(&Tool{
		ID: "lyric.paste", Category: "歌词",
		Title: "粘贴所选音符歌词",
		Desc: "对应原脚本的「粘贴所选音符歌词」：把文本按**原脚本那套分割规则**切开，" +
			"顺序填入选中的音符（**词用完就停，不循环复用**）。分割规则（逐条照抄）：" +
			"① 含汉字无假名：空格占比 ≥25% ⇒ 按空格分词，否则**一字一音符**（跳过空白与中文标点）；" +
			"② 其它：空格占比 ≥25%（或有假名且含空格）⇒ 按空格；③ 日语无空格 ⇒ 逐假名；" +
			"④ 有空格 ⇒ 按空格；⑤ 都没有 ⇒ 整串当一个词。" +
			"⚠️ **没选中音符时按整个片段处理**（原脚本是直接返回；这里按用户 2026-10-24 的口径退化）。" +
			"⚠️ 文本留空时**面板会先用剪贴板填进来**（浏览器剪贴板 API），命令行调用请显式给 `text`。",
		Kind:   KindWrite,
		Source: "AKD工具侧栏.js 的 pasteLyricsButton",
		Params: []Param{
			textParam("text", "要粘贴的文本", "", "啦 啦 啦 / la la la", "留空则由面板从剪贴板取"),
		},
		Build: buildPasteLyrics,
	})

	// ---------- 添加 + / - 并后移 ----------
	register(&Tool{
		ID: "lyric.shift", Category: "歌词",
		Title: "添加 + / - 并后移",
		Desc: "对应原脚本的「添加+并后移」与「添加-并后移」：把**播放头所在音符**的歌词改成 `+`（连音）" +
			"或 `-`（延音），它后面的歌词整体**右移一位**（`[0,1,2,3] → [+,0,1,2]`）。" +
			"⚠️ 只在**选中的音符里**找播放头那个（照原脚本）；找不到就报错（原脚本是静默不动）。",
		Kind:   KindWrite,
		Source: "AKD工具侧栏.js 的 shiftLyricsRightButton / shiftLyricsMinusButton",
		Params: []Param{
			// ⛔ 只留 `-`：**ACE 里没有 `+`**（`+` 是 SV 的"一个词拆到多个音符"）。
			// 照用户 2026-10-05 的口径「删 +」；也符合早先那条「只做 ACE 支持的参数」。
			{Name: "mark", Label: "插哪个标记", Type: ParamSelect, Default: "-",
				Options: []Option{
					{Value: "-", Label: "- 延音（延长上一个字 / ACE 的 tenuto）"},
				}},
		},
		Build: buildLyricShift,
	})

	// ---------- 删除 / 合并并前移 ----------
	register(&Tool{
		ID: "lyric.remove", Category: "歌词",
		Title: "删除 / 合并歌词并前移",
		Desc: "对应原脚本的「删除歌词并前移」与「合并歌词并前移」：" +
			"**删除** = 播放头音符的歌词丢掉，它后面的歌词整体**左移一位**（最后一个音符的歌词**留在原地**，照原脚本）；" +
			"**合并** = 播放头音符的歌词**并上**下一个音符的歌词，再整体左移。",
		Kind:   KindWrite,
		Source: "AKD工具侧栏.js 的 shiftLyricsLeftButton / mergeLyricsButton",
		Params: []Param{
			{Name: "mode", Label: "怎么做", Type: ParamSelect, Default: "delete",
				Options: []Option{
					{Value: "merge", Label: "合并歌词并前移（本音符 = 自己 + 下一个）"},
					{Value: "delete", Label: "删除歌词并前移（丢掉本音符的）"},
				}},
		},
		Build: buildLyricRemove,
	})
}

func scopeWord(usedAll bool) string {
	if usedAll {
		return "整个片段（没有选中音符，照原脚本退化）"
	}
	return "选中的音符"
}

// ---------- 自动拼接 ----------

// joinPlan 算出"自动拼接"要改谁、改成多长。
//
// 原脚本：按起点排序后，**前一个**的时长 = 下一个的起点 − 前一个的起点；**最后一个不动**。
// 返回 (新时长 → 该时长的音符 uuid, 退化项个数)。
//
// 为什么返回"按时长分组的映射"而不是逐音符的结果：`note resize` 一次调用只能给一个 `--dur`，
// 分组之后命令数 = **不同时长的种数**（等距音符通常就 1 条），而不是音符数 ——
// 否则 300 个音符就是 300 次调用（约 20 秒）。
func joinPlan(notes []selNote) (map[int64][]string, int) {
	byDur := map[int64][]string{}
	degenerate := 0
	for i := 0; i+1 < len(notes); i++ {
		d := notes[i+1].Pos - notes[i].Pos
		if d <= 0 {
			degenerate++ // 同起点（或倒序）⇒ 时长为 0，ACE 会拒（Must be positive）
			continue
		}
		byDur[d] = append(byDur[d], notes[i].UUID)
	}
	return byDur, degenerate
}

// shiftLyricsRight 是「添加 + / - 并后移」：第 i 个改成 mark，它后面的整体右移一位。
// `[0,1,2,3]`、i=0、mark="+" ⇒ `[+,0,1,2]`（末尾的 "3" 被挤出序列 —— 照原脚本）。
// **不动入参**，返回新切片。
func shiftLyricsRight(lyrics []string, i int, mark string) []string {
	out := append([]string(nil), lyrics...)
	for j := len(out) - 1; j > i; j-- {
		out[j] = out[j-1]
	}
	out[i] = mark
	return out
}

// removeLyricsShiftLeft 是「删除歌词并前移」：丢掉第 i 个，后面整体左移；
// ⚠️ **最后一个音符的歌词留在原地**（原脚本的循环只到 len-1）。
// `[0,1,2,3]`、i=1 ⇒ `[0,2,3,3]`。
func removeLyricsShiftLeft(lyrics []string, i int) []string {
	out := append([]string(nil), lyrics...)
	for j := i; j < len(out)-1; j++ {
		out[j] = out[j+1]
	}
	return out
}

// mergeLyricsShiftLeft 是「合并歌词并前移」：第 i 个 = 自己 + 下一个，再整体左移。
// `[0,1,2,3]`、i=1 ⇒ `[0,12,3,3]`。
func mergeLyricsShiftLeft(lyrics []string, i int) []string {
	out := append([]string(nil), lyrics...)
	if i < 0 || i >= len(out)-1 {
		return out // 最后一个没有"下一个"可并
	}
	out[i] = out[i] + out[i+1]
	for j := i + 1; j < len(out)-1; j++ {
		out[j] = out[j+1]
	}
	return out
}

func buildJoinNotes(ctx context.Context, rd ReadFunc, vals map[string]string) ([]Call, error) {
	sc, err := readSelContext(ctx, rd, true)
	if err != nil {
		return nil, err
	}
	notes := sc.Selected
	// 阈值（tick，480 = 1 拍）：**缝 ≤ maxGap 才接**，比它大的当**气口**留着。
	// ⭐ 默认 0 = **不设**限 = 与现在/原脚本逐字节相同（用户 2026-10-05：默认不设，界面用滑块）。
	// 档位（音符时值）：全音符 1920 · 二分 960 · 四分 480 · 八分 240 · 十六分 120 · 三十二分 60 · 六十四分 30 · 一百二十八分 15。
	var maxGap int64
	_, _ = fmt.Sscanf(vals["maxGap"], "%d", &maxGap)
	if maxGap > 0 && len(notes) > 1 {
		sorted := append([]selNote(nil), notes...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Pos < sorted[j].Pos })
		// 按缝隙切段：段内照旧消缝；段与段之间的气口**不动**（每段最后一个音符本就不动）
		var segs [][]selNote
		cur := []selNote{sorted[0]}
		for i := 1; i < len(sorted); i++ {
			if sorted[i].Pos-(sorted[i-1].Pos+sorted[i-1].Dur) > maxGap {
				segs = append(segs, cur)
				cur = []selNote{sorted[i]}
			} else {
				cur = append(cur, sorted[i])
			}
		}
		segs = append(segs, cur)
		notes = notes[:0]
		for _, s := range segs {
			notes = append(notes, s...)
		}
	}
	if len(notes) < 2 {
		return nil, fmt.Errorf("自动拼接至少要 2 个音符，现在只有 %d 个（%s）", len(notes), scopeWord(sc.UsedAll))
	}
	_ = vals
	byDur, degenerate := joinPlan(notes)
	if len(byDur) == 0 {
		return nil, fmt.Errorf("算不出可用的新时长：%d 个音符里有 %d 个与下一个同起点（时长为 0）。"+
			"先把重叠/同起点的音符错开再试", len(notes), degenerate)
	}
	durs := make([]int64, 0, len(byDur))
	for d := range byDur {
		durs = append(durs, d)
	}
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })

	var calls []Call
	for _, d := range durs {
		for _, chunk := range chunkUUIDs(byDur[d]) {
			args := append([]string{"note", "resize"}, noteRefArgs(chunk)...)
			args = append(args, "--dur", fmt.Sprintf("%dt", d))
			calls = append(calls, Call{
				Label: fmt.Sprintf("%d 个音符 → 时长 %d tick（%.3f 拍）", len(chunk), d, float64(d)/480),
				Args:  args,
			})
		}
	}
	if degenerate > 0 {
		calls[0].Label += fmt.Sprintf("（另有 %d 个与下一个同起点，跳过了）", degenerate)
	}
	return calls, nil
}

// ---------- 按播放头选音符 ----------

func buildSelectPlayhead(ctx context.Context, rd ReadFunc, vals map[string]string) ([]Call, error) {
	side := value(vals, "side", "after")
	if side != "after" && side != "before" && side != "at" {
		return nil, fmt.Errorf("side 认不出：%q（只能 after / before / at）", side)
	}
	sc, err := readSelContext(ctx, rd, false) // 这个工具**本来就**不要求先选中
	if err != nil {
		// 没选中不算错 —— 这里是要**建立**选区
		sc, err = readSelContext(ctx, rd, true)
		if err != nil {
			return nil, err
		}
	}
	var ids []string
	if side == "at" {
		// ⭐ 用户 2026-10-24：右键可切「当前」—— 只选**播放头所在的那个音符**
		// 判定与删词/加词同一条（音符内 onset ≤ ph < end；压在终点有后一个取后一个；空隙无目标）
		notes := append([]selNote(nil), sc.All...)
		sort.SliceStable(notes, func(i, j int) bool { return notes[i].Pos < notes[j].Pos })
		if i := playheadIndex(notes, sc.Local); i >= 0 {
			ids = append(ids, notes[i].UUID)
		}
	} else {
		for _, n := range sc.All {
			if (side == "after" && n.End > sc.Local) || (side == "before" && n.Pos < sc.Local) {
				ids = append(ids, n.UUID)
			}
		}
	}
	if len(ids) == 0 {
		where := map[string]string{"after": "之后", "before": "之前", "at": "当前（播放头不在任何音符里）"}[side]
		return nil, fmt.Errorf("播放头（片段内 tick %d）%s一个音符都没有 —— "+
			"**不会**去清空你已有的选区。把播放头挪到有音符的地方再试", sc.Local, where)
	}
	var calls []Call
	// ⛔ `--mode replace` 是**替换**选区 —— 分块发的时候，每一块都会把上一块顶掉，
	// 于是只剩最后一块（真机踩过：329 个音符的片段，选"之后所有"只选上了最后 27 个）。
	// ⇒ 第一块 replace（先清干净），后续块 modify（往上加）。
	for i, chunk := range chunkUUIDs(ids) {
		mode := "replace"
		if i > 0 {
			mode = "modify" // 见 selectModesFor 的说明：replace 会把上一块顶掉
		}
		calls = append(calls, Call{
			Label: fmt.Sprintf("选中 %d 个音符（%s）", len(chunk), mode),
			Args: []string{"selection", "set", "--scope", "editor", "--mode", mode,
				"--notes-to-select", noteRefJSON(chunk)},
		})
	}
	calls = append(calls, Call{Label: "写后回读选区",
		Args: []string{"selection", "get", "--scope", "editor"}})
	return calls, nil
}

// selectModesFor 是"分块选音符"该用什么 mode 的**唯一事实源**（给测试用，别再写第二遍）。
// 第一块 replace（先清干净），后续 modify（往上加）—— `replace` 是替换语义，分块发会互相顶掉。
func selectModesFor(chunks int) []string {
	out := make([]string, 0, chunks)
	for i := 0; i < chunks; i++ {
		if i == 0 {
			out = append(out, "replace")
		} else {
			out = append(out, "modify")
		}
	}
	return out
}

// noteRefJSON 拼 `[{"uuid":"{…}"},…]` —— 形状踩过坑，必须是对象数组。
func noteRefJSON(ids []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"uuid":"`)
		b.WriteString(id)
		b.WriteString(`"}`)
	}
	b.WriteByte(']')
	return b.String()
}

// ---------- 粘贴歌词 ----------

func buildPasteLyrics(ctx context.Context, rd ReadFunc, vals map[string]string) ([]Call, error) {
	raw := vals["text"]
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("要粘贴的文本是空的 —— 面板会先尝试读系统剪贴板；" +
			"命令行调用请显式给 `text`（例如 `text=啦 啦 啦`）")
	}
	sc, err := readSelContext(ctx, rd, true) // ⭐ 没选中 ⇒ 按整个片段（用户 2026-10-24：JS 的默认语义）
	if err != nil {
		return nil, err
	}
	cleaned := splitLyricsForPaste(raw)
	if len(cleaned) == 0 {
		return nil, fmt.Errorf("按分割规则切出来 0 段（原文：%q）—— 检查一下是不是只有标点或空白", raw)
	}
	n := len(cleaned)
	if n > len(sc.Selected) {
		n = len(sc.Selected) // 原脚本：词用完就停、多了就丢（不循环复用）
	}
	lyrics := cleaned[:n]
	ids := make([]string, 0, n)
	for _, note := range sc.Selected[:n] {
		ids = append(ids, note.UUID)
	}
	var calls []Call
	for _, chunk := range chunkUUIDs(ids) {
		// 注意：切块的边界要与 lyrics 对齐
		off := indexOfUUID(ids, chunk[0])
		payload := jsonStrings(lyrics[off : off+len(chunk)])
		args := append([]string{"note", "set-lyric"}, noteRefArgs(chunk)...)
		args = append(args, "--lyrics", payload)
		calls = append(calls, Call{
			Label: fmt.Sprintf("%d 个音符 ← %d 个词", len(chunk), len(chunk)),
			Args:  args,
		})
	}
	calls = append(calls, Call{Label: "写后回读歌词", Args: []string{"clip", "lyrics",
		"--track-index", strconv.Itoa(sc.Ref.Track), "--clip-index", strconv.Itoa(sc.Ref.Clip)}})
	if len(cleaned) > len(sc.Selected) {
		calls[0].Label += fmt.Sprintf("（切出 %d 段，但只选中 %d 个音符 —— 多的丢掉，不循环）",
			len(cleaned), len(sc.Selected))
	}
	return calls, nil
}

func indexOfUUID(ids []string, want string) int {
	for i, id := range ids {
		if id == want {
			return i
		}
	}
	return 0
}

// jsonStrings 拼 `["a","b"]`（歌词可能含引号/反斜杠 ⇒ 老老实实转义）。
func jsonStrings(ss []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, s := range ss {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Quote(s))
	}
	b.WriteByte(']')
	return b.String()
}

// ---------- 加 + / - 并后移 ----------

func buildLyricShift(ctx context.Context, rd ReadFunc, vals map[string]string) ([]Call, error) {
	mark := value(vals, "mark", "+")
	if mark != "+" && mark != "-" {
		return nil, fmt.Errorf("标记认不出：%q（只能 + 或 -）", mark)
	}
	sc, err := readSelContext(ctx, rd, true) // 没选中 ⇒ 整个片段
	if err != nil {
		return nil, err
	}
	i := playheadIndex(sc.Selected, sc.Local)
	if i < 0 {
		return nil, fmt.Errorf("播放头（片段内 tick %d）没有落在**选中的**任何音符里 —— "+
			"原脚本这时是静默什么都不做；这里明确报错。把播放头移进一个选中音符，或先选中它", sc.Local)
	}
	notes := sc.Selected
	// 纯逻辑交给 shiftLyricsRight（可单测），这里只管拼命令
	lyrics := make([]string, len(notes))
	for j := range notes {
		lyrics[j] = notes[j].Lyric
	}
	newLyrics := shiftLyricsRight(lyrics, i, mark)
	ids := make([]string, 0, len(notes)-i)
	for j := i; j < len(notes); j++ {
		ids = append(ids, notes[j].UUID)
	}
	calls := []Call{{
		Label: fmt.Sprintf("第 %d 个音符 → %q，其后 %d 个歌词右移一位（%s）",
			i+1, mark, len(notes)-i-1, scopeWord(sc.UsedAll)),
		Args: append(append([]string{"note", "set-lyric"}, noteRefArgs(ids)...),
			"--lyrics", jsonStrings(newLyrics[i:])),
	}}
	calls = append(calls, Call{Label: "写后回读歌词", Args: []string{"clip", "lyrics",
		"--track-index", strconv.Itoa(sc.Ref.Track), "--clip-index", strconv.Itoa(sc.Ref.Clip)}})
	return calls, nil
}

// ---------- 删除 / 合并并前移 ----------

func buildLyricRemove(ctx context.Context, rd ReadFunc, vals map[string]string) ([]Call, error) {
	mode := value(vals, "mode", "delete")
	if mode != "merge" && mode != "delete" {
		return nil, fmt.Errorf("mode 认不出：%q（只能 merge / delete）", mode)
	}
	sc, err := readSelContext(ctx, rd, true) // 没选中 ⇒ 整个片段
	if err != nil {
		return nil, err
	}
	notes := sc.Selected
	i := playheadIndex(notes, sc.Local)
	if i < 0 {
		return nil, fmt.Errorf("播放头（片段内 tick %d）没有落在**选中的**任何音符里 —— "+
			"原脚本这时是静默什么都不做；这里明确报错。把播放头移进一个选中音符，或先选中它", sc.Local)
	}
	if mode == "merge" && i >= len(notes)-1 {
		return nil, fmt.Errorf("播放头在**最后一个**选中音符上，后面没有音符可以合并（原脚本这时也是不动）")
	}
	// 纯逻辑交给两个可单测的助手
	lyrics := make([]string, len(notes))
	for j := range notes {
		lyrics[j] = notes[j].Lyric
	}
	newLyrics := removeLyricsShiftLeft(lyrics, i)
	if mode == "merge" {
		newLyrics = mergeLyricsShiftLeft(lyrics, i)
	}
	// ⚠️ 最后一个音符的歌词**留在原地**（照原脚本：循环只到 len-1），所以从 i 起整体写回
	ids := make([]string, 0, len(notes)-i)
	for j := i; j < len(notes); j++ {
		ids = append(ids, notes[j].UUID)
	}
	what := "删除歌词并前移"
	if mode == "merge" {
		what = "合并歌词并前移"
	}
	calls := []Call{{
		Label: fmt.Sprintf("%s：从第 %d 个选中音符起，%d 个歌词左移一位", what, i+1, len(notes)-i-1),
		Args: append(append([]string{"note", "set-lyric"}, noteRefArgs(ids)...),
			"--lyrics", jsonStrings(newLyrics[i:])),
	}}
	calls = append(calls, Call{Label: "写后回读歌词", Args: []string{"clip", "lyrics",
		"--track-index", strconv.Itoa(sc.Ref.Track), "--clip-index", strconv.Itoa(sc.Ref.Clip)}})
	return calls, nil
}

// ---------- 粘贴分割规则（逐条照抄原脚本） ----------

// cjkPunct 是原脚本那个字符类 `[？！。，、；：""”（）《》〈〉【】…—·～]`，
// 外加直引号（原脚本里的弯引号在别处编码时容易被改成直的）。
var cjkPunct = map[rune]bool{
	'？': true, '！': true, '。': true, '，': true, '、': true, '；': true, '：': true,
	'“': true, '”': true, '‘': true, '’': true, '「': true, '」': true,
	'"': true, '\'': true,
	'（': true, '）': true, '《': true, '》': true, '〈': true, '〉': true,
	'【': true, '】': true, '…': true, '—': true, '·': true, '～': true,
}

// splitLyricsForPaste 是原脚本 `pasteLyricsButton` 里那段分割规则的逐条移植。
//
// 空格占比按**原始文本**算（换行折算成空格不影响中文判定），用**字符数**当分母
// （原脚本是 JS 的 `raw.length`，对 BMP 字符就是字符数；Go 里要按 rune 数，别用字节数）。
func splitLyricsForPaste(raw string) []string {
	s := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(raw)
	rawRunes := []rune(raw)
	ratio := 0.0
	if len(rawRunes) > 0 {
		ratio = float64(strings.Count(raw, " ")) / float64(len(rawRunes))
	}
	var hasKana, hasHanzi bool
	for _, r := range s {
		switch {
		case r >= 0x3040 && r <= 0x30ff:
			hasKana = true
		case r >= 0x4e00 && r <= 0x9fff:
			hasHanzi = true
		}
	}
	var parts []string
	switch {
	case hasHanzi && !hasKana:
		if ratio >= 0.25 {
			parts = strings.Split(s, " ")
		} else {
			parts = charsSkippingPunct(s)
		}
	case ratio >= 0.25 || (hasKana && strings.Contains(s, " ")):
		parts = strings.Split(s, " ")
	case hasKana:
		parts = charsSkippingPunct(s)
	case strings.Contains(s, " "):
		parts = strings.Split(s, " ")
	default:
		parts = []string{s}
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// charsSkippingPunct 一字一段：去掉所有空白，再跳过中文标点。
func charsSkippingPunct(s string) []string {
	var out []string
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		if cjkPunct[r] {
			continue
		}
		out = append(out, string(r))
	}
	return out
}
