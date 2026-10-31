package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 移植 `跨轨道复制.js`（AKD 的「轨道-跨轨道复制」）。
//
// 原脚本两个独立的半：
//
//	① 复制参数 —— 按 NoteGroupReference 下标配对，对每条曲线 `removeAll()` 后
//	   把源的点按 `offsetDelta = 源offset − 目标offset` 平移写入（**整体覆盖**）
//	② 复制音符 —— 按**绝对 onset** 双指针配对，逐属性搬
//
// ⛔ 移植前先做了逐条对账（结论写在 HANDOVER §23），**参数那一半在 ACE 里对不上**：
// SV 的 9 条曲线（pitchDelta/vibratoEnv/loudness/tension/breathiness/voicing/
// gender/toneShift/vocalMode_*）里 7 条 ACE 根本没有；只有 `tension` **同名**、
// `breathiness` 勉强对应 `air`，而且**连 tension 都不能照搬数值**
// （SV 是 −1..1；ACE 是 `envelope` 尺度、实测 0.3..2.5 的**乘数**）。
// ⇒ 参数半**不按 SV 的参数名做**（那是"用错的名字写错的值"），
// 改成按 **ACE 自己的参数名**在两个 clip 之间复制层（用户 2026-10-05 裁定：
// 「只做 ace 支持的参数，音高线另做」）—— 见 xcopy_params.go。
//
// 本文件只做**音符半**（歌词 / 音高 / 音素）。
//
// ACE 侧的原语（都核过 help，也都真机读过）：
//
//	· `clip list --track-index N` —— 拿片段的 uuid/begin/end/type（复用 tracks.go 的 clipListBrief）
//	· `clip note-content --track-index N --clip-index I` —— ⚠️ **不是** `--clip-uuid`！
//	  这个动词是**按位置**寻址的（跟其它 clip 动词都不同），回 notes[]：
//	  noteUuid / pos / dur / endPos / pitch / lyric / language / syllable
//	  ⚠️ `pos` 是 **clip-local**（`note move` 的 help 明说那是 clip note-content 报的坐标系）
//	  ⇒ 绝对位置 = clipBegin + pos
//	· `note set-lyric --note-uuid U… --lyrics '["a","b"]'` —— **位置对应的 JSON 数组**，可批量
//	· `note move --note-uuid U… --pitch <MIDI|C4>` —— ⛔ **刚性移动**：只有**锚点**到目标音高，
//	  **其余按同一 delta 平移**。这条是移植时最容易算错的：不能"按目标音高分组"，
//	  否则 A=60、B=62 都要变 64 时，一次调用会把 B 推到 66。安全的分组键是
//	  **(当前音高, 目标音高)** —— 组内当前音高相同 ⇒ delta 相同 ⇒ 全部落位。
//	· `phoneme list --note U…` —— 回 phonemes[]（有效符号串）+ isOverride。
//	  ⛔ **没有整 clip 形式**（官方 hint 原话："a whole clip is hundreds of rows"）
//	· `phoneme set --note U --phoneme s1 s2 …` —— ⚠️ **一次一个音符**，且
//	  "Every symbol must be in the note's own language" ⇒ 语言不同就只能跳过（不替用户改语言）

// 原脚本里那批**移不过来**的音符属性，明确不做（不是漏了）：
//
//	exprGroup（SV 表达式组）· alt（音素力度）—— ACE 没有对应概念
//	dur（音素时长）—— ⚠️ **量纲不同**：SV 是"比例数组"、ACE 是**秒**，技能明确说不能互套
//	tNoteOffset · SV1 的 pitchtransition/vibrato —— 前者 ACE 只有近似的 consonant-timing，
//	  后者在 SV2 上本来就不显示（原脚本自己也是 `if (!isSV2)` 才加）
const xcopyUnportable = "exprGroup · alt · dur（音素时长，SV 是比例、ACE 是秒）· tNoteOffset · SV1 的 pitchtransition/vibrato"

type xcopyNote struct {
	NoteUUID string `json:"noteUuid"`
	Pos      int64  `json:"pos"` // clip-local
	Dur      int64  `json:"dur"`
	EndPos   int64  `json:"endPos"`
	Pitch    int    `json:"pitch"`
	Lyric    string `json:"lyric"`
	Language string `json:"language"`
	// ⚠️ 在 `clip note-content` 里 `headConsonants`/`tailConsonants` 是**辅音时长数组**（官方：v1 模型才有；
	// v2 恒为空数组，辅音时长在 v2 走 "pin"）。
	// 用户 2026-10-07 的口径「**默认的辅音时长不复制**」就靠它判：
	// **非空 = 手动设过**（官方的 `phoneme reset-timing` 原话是 "Clear all consonants"，就是把这些清掉）。
	HeadConsLen []float64 `json:"headConsonants"`
	TailConsLen []float64 `json:"tailConsonants"`
}

// hasManualConsonants 判"这个音符的辅音时长被人手动改过没有"。
//
//	· v1：音符上的 `headConsonants`/`tailConsonants` **长度数组非空** = 手动设过（见上面那条官方口径）。
//	· v2：辅音时长落在 **pin** 上 ⇒ 看 `phoneme list --with-timings` 的 `isPinned`
//	  （官方对 `set-consonant-timing` 的说明：它 "lands on a pin instead, and `pinned` on the response
//	  names every (noteUuid, index) the span it pinned holds"）。
//
// ⚠️ **判不出来就不搬**（用户口径：默认的不复制）—— 宁可少搬，也不要把引擎自己分布出来的时长
// 当成"用户的意图"抄到目标上去。
func (n xcopyNote) hasManualConsonants() bool {
	return len(n.HeadConsLen) > 0 || len(n.TailConsLen) > 0
}

// anyTimingPinned 判 v2 那份 `--with-timings` 里有没有 pin。
func anyTimingPinned(ph xcopyPhoneme) bool {
	for _, t := range ph.Timings {
		if t.IsPinned {
			return true
		}
	}
	return false
}

type xcopyNoteContent struct {
	Fingerprint string      `json:"fingerprint"`
	NoteCount   int         `json:"noteCount"`
	Notes       []xcopyNote `json:"notes"`
}

type xcopyPhoneme struct {
	NoteUUID    string   `json:"noteUuid"`
	Lyric       string   `json:"lyric"`
	Language    string   `json:"language"`
	Phonemes    []string `json:"phonemes"`
	IsOverride  bool     `json:"isOverride"`
	HeadCons    []string `json:"headConsonants"`
	TailCons    []string `json:"tailConsonants"`
	Vowels      []string `json:"vowels"`
	TimingKinds []string `json:"timingKinds"`
	// Timings 只在 `--with-timings` 时才有：**逐音素**的 index/时长（辅音时长复制要用）。
	// 官方原话：写口 `phoneme set-consonant-timing --index` 的 index 就是这里报的那个。
	Timings []xcopyPhonemeTiming `json:"timings,omitempty"`
}

// xcopyPhonemeTiming 是 `--with-timings=durations` 的一行（只留我们用的字段）。
type xcopyPhonemeTiming struct {
	Index       int     `json:"index"`
	Name        string  `json:"name"`
	IsVowel     bool    `json:"isVowel"`
	DurationSec float64 `json:"durationSec"`
	// IsPinned：v2 上"这个音素落在一个 pin 里" ⇒ 辅音时长被手动改过的判据。
	IsPinned bool `json:"isPinned"`
}

type xcopyPhonemeList struct {
	ClipUUID string         `json:"clipUuid"`
	Notes    []xcopyPhoneme `json:"notes"`
}

// xcopyPairView 是"一对 clip"的配对结果（原脚本的 r 那一层）。
type xcopyPairView struct {
	Pair     int    `json:"pair"`
	SrcClip  string `json:"srcClip"`
	DstClip  string `json:"dstClip"`
	SrcType  string `json:"srcClipType,omitempty"`
	DstType  string `json:"dstClipType,omitempty"`
	SrcBegin int64  `json:"srcClipBegin"`
	DstBegin int64  `json:"dstClipBegin"`
	SrcNotes int    `json:"srcNotes"`
	DstNotes int    `json:"dstNotes"`
	Matched  int    `json:"matched"`
	SrcOnly  int    `json:"srcOnly"` // 源有、目标对不上 onset
	DstOnly  int    `json:"dstOnly"` // 目标有、源对不上 onset
	Skip     string `json:"skip,omitempty"`

	// ⭐ 给「轨内复制」窗口画框用的**精确 tick**（用户 2026-10-07：源范围一种颜色、目标范围另一种颜色）。
	// `SrcEndTick` 是**片段内**坐标（与 `note-content` 的 `pos` 同框）；`*AbsTick` 是**工程**坐标
	//（缩略图与小节尺按工程 tick 画）。两个都给，页面就不用自己假设"一小节多少 tick"。
	SrcEndTick int64 `json:"srcEndTick,omitempty"`
	SrcAbsTick int64 `json:"srcAbsTick,omitempty"`
	SrcEndAbs  int64 `json:"srcEndAbsTick,omitempty"`
	DstAbsTick int64 `json:"dstAbsTick,omitempty"`
	DstEndAbs  int64 `json:"dstEndAbsTick,omitempty"`
	ClipAbsBeg int64 `json:"clipBeginAbsTick,omitempty"`
}

// xcopyTrackView 让用户在结果里直接看到"有哪些轨、下标是多少"，省得盲填。
type xcopyTrackView struct {
	Index     int    `json:"trackIndex"`
	Name      string `json:"trackName"`
	Type      string `json:"trackType"`
	ClipCount int    `json:"clipCount"`
}

type xcopyReport struct {
	// Mode 区分两支同源脚本：`tracks` = 跨轨道复制（按片段配对）、`within` = 轨内复制（按小节）。
	// 结果里的措辞、以及"源和目标同一条轨"那条警告，都靠它分岔 ——
	// 轨内模式下源和目标**本来就是同一条轨**，那条警告会误报。
	Mode         string           `json:"mode,omitempty"`
	SrcTrack     int              `json:"srcTrack"`
	SrcTrackName string           `json:"srcTrackName,omitempty"`
	DstTrack     int              `json:"dstTrack"`
	DstTrackName string           `json:"dstTrackName,omitempty"`
	Tracks       []xcopyTrackView `json:"tracks,omitempty"`
	// 空槽单独压成一行（`--include-empty` 会把工程里所有空槽都报出来，实测 99 个）。
	EmptySlots    int    `json:"emptySlots,omitempty"`
	EmptySlotList string `json:"emptySlotIndices,omitempty"`
	// ClipCount = 轨内复制那一支的**作用域里有多少个片段**（用户 2026-10-07：作用于轨内**所有** clip）
	TrackClips int             `json:"trackClipCount,omitempty"`
	Pairs      []xcopyPairView `json:"pairs"`
	PairCount  int             `json:"pairCount"`
	// Range 是"可选范围"的人话描述（留空 = 不限范围）—— 结果里必须说清这次到底按哪一段搬的。
	Range       string `json:"range,omitempty"`
	UnpairedSrc int    `json:"unpairedSrcClips"`
	UnpairedDst int    `json:"unpairedDstClips"`
	Matched     int    `json:"notesMatched"`
	SrcOnly     int    `json:"srcOnlyNotes"`
	DstOnly     int    `json:"dstOnlyNotes"`

	// 计划/实际要动的音符数，按属性分开（干跑时是"将会"）。
	WillLyric    int `json:"willSetLyrics,omitempty"`
	WillPitch    int `json:"willSetPitch,omitempty"`
	WillPhonemes int `json:"willSetPhonemes,omitempty"`
	// ⭐ 2026-10-07「除 uuid 外全都要」补的三类计数
	WillResize      int `json:"willResizeNotes,omitempty"`         // 时值会变的音符数
	WillConsonant   int `json:"willSetConsonants,omitempty"`       // 要改的辅音个数
	WillParamLayers int `json:"willWriteParamLayers,omitempty"`    // 要写的参数层数
	ParamTicks      int `json:"paramTicks,omitempty"`              // 参数曲线一共写多少个 tick
	WillDynamic     int `json:"willWriteDynamicAnchors,omitempty"` // dynamic 锚点个数

	// ⛔ 用户 2026-10-07：「复制的内容为**手动更改过的**，默认的音素/辅音时长不复制」
	// ⇒ 这两个计数是"因为还是默认、所以没搬"的个数（如实报出来，别让人以为漏了）。
	SkippedDefaultPhonemes int `json:"skippedDefaultPhonemes,omitempty"`
	SkippedDefaultCons     int `json:"skippedDefaultConsonants,omitempty"`

	Calls    []string `json:"plannedCalls,omitempty"`
	Did      []string `json:"done,omitempty"`
	Skipped  []string `json:"skipped,omitempty"`
	DryRun   bool     `json:"dryRun,omitempty"`
	Unported string   `json:"unportedAttrs,omitempty"`
	Millis   int64    `json:"millis,omitempty"`
	Note     string   `json:"note,omitempty"`
}

// ---------- 轨道选择 ----------

// resolveTrackSel 解析一个"轨道选择"字段。
//
// 空 / `current` / `c` / `当前` ⇒ **标记线（caret）所在轨道**（原脚本的「Current Track」选项）。
// 否则必须是 0 起的下标。抽出来是因为源/目标两侧共用同一套语义，
// 而"空字符串到底指哪条轨"这种事**不能两处各写一遍**（写岔了就是复制到错的轨道上，不报错）。
func resolveTrackSel(vals map[string]string, name string) (int, bool, error) {
	s := strings.ToLower(strings.TrimSpace(vals[name]))
	switch s {
	case "", "current", "c", "cur", "当前", "当前轨道":
		return 0, true, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false, fmt.Errorf("%s 只能是 0 起的轨道下标，或 `current`（当前标记线所在轨道），收到 %q", name, vals[name])
	}
	if n < 0 {
		return 0, false, fmt.Errorf("%s 不能为负（下标 0 起）", name)
	}
	return n, false, nil
}

// readTracks 读轨道花名册（顺带给下标校验和名字）。
//
// ⚠️ 必须带 `--include-empty`：`track list` 默认**不列空轨**，而**空槽也占一个下标**
// （官方原话："An empty slot occupies an index"）⇒ 不带它时列表的下标是**有洞的**，
// 用户看到 "0=洛天依" 也没法知道 1 到底是空槽还是不存在。
// 空槽回 `trackType:"Empty"` 且**没有 uuid**（那个槽下次 `track create` 就被顶替，不配有身份）。
func readTracks(ctx context.Context, rd ReadFunc) ([]xcopyTrackView, error) {
	var t struct {
		Tracks []struct {
			TrackIndex int    `json:"trackIndex"`
			TrackName  string `json:"trackName"`
			TrackType  string `json:"trackType"`
			ClipCount  int    `json:"clipCount"`
		} `json:"tracks"`
	}
	if err := readJSON(ctx, rd, &t, "track", "list", "--include-empty"); err != nil {
		return nil, fmt.Errorf("读轨道列表：%w", err)
	}
	out := make([]xcopyTrackView, 0, len(t.Tracks))
	for _, x := range t.Tracks {
		out = append(out, xcopyTrackView{Index: x.TrackIndex, Name: x.TrackName, Type: x.TrackType, ClipCount: x.ClipCount})
	}
	return out, nil
}

func trackNameOf(tracks []xcopyTrackView, idx int) string {
	for _, t := range tracks {
		if t.Index == idx {
			return t.Name
		}
	}
	return ""
}

// resolveXcopyTracks 把两侧的轨道选择解析成具体下标，并把"这台工程有哪些轨"带回去（报错时直接给用户看）。
func resolveXcopyTracks(ctx context.Context, rd ReadFunc, vals map[string]string) (int, int, []xcopyTrackView, error) {
	tracks, err := readTracks(ctx, rd)
	if err != nil {
		return 0, 0, nil, err
	}
	if len(tracks) == 0 {
		return 0, 0, tracks, fmt.Errorf("这台工程一条轨都没有")
	}
	needCaret := false
	srcIdx, srcCur, err := resolveTrackSel(vals, "srcTrack")
	if err != nil {
		return 0, 0, tracks, err
	}
	dstIdx, dstCur, err := resolveTrackSel(vals, "dstTrack")
	if err != nil {
		return 0, 0, tracks, err
	}
	needCaret = srcCur || dstCur
	if needCaret {
		var caret struct {
			TrackIndex int `json:"trackIndex"`
		}
		if err := readJSON(ctx, rd, &caret, "caret", "get", "--scope", "arrangement"); err != nil {
			return 0, 0, tracks, fmt.Errorf("读标记线（`current` 要知道你在哪条轨上）：%w", err)
		}
		if srcCur {
			srcIdx = caret.TrackIndex
		}
		if dstCur {
			dstIdx = caret.TrackIndex
		}
	}
	if !hasTrack(tracks, srcIdx) {
		return 0, 0, tracks, fmt.Errorf("源轨道 %d 不存在。%s", srcIdx, trackRosterHint(tracks))
	}
	if !hasTrack(tracks, dstIdx) {
		return 0, 0, tracks, fmt.Errorf("目标轨道 %d 不存在。%s", dstIdx, trackRosterHint(tracks))
	}
	for _, t := range tracks {
		if (t.Index == srcIdx || t.Index == dstIdx) && t.Type == "Empty" {
			side := "源"
			if t.Index == dstIdx && t.Index != srcIdx {
				side = "目标"
			}
			return 0, 0, tracks, fmt.Errorf(
				"%s轨道 %d 是**空槽**（`track list --include-empty` 报的 Empty）—— 它占下标但没有片段，不能复制。%s",
				side, t.Index, trackRosterHint(tracks))
		}
	}
	return srcIdx, dstIdx, tracks, nil
}

func hasTrack(tracks []xcopyTrackView, idx int) bool {
	for _, t := range tracks {
		if t.Index == idx {
			return true
		}
	}
	return false
}

func trackRosterHint(tracks []xcopyTrackView) string {
	real, empties := splitRoster(tracks)
	var parts []string
	for _, t := range real {
		parts = append(parts, fmt.Sprintf("%d=%s(%s, %d 个片段)", t.Index, t.Name, t.Type, t.ClipCount))
	}
	if len(empties) > 0 {
		parts = append(parts, fmt.Sprintf("空槽 %s（共 %d 个，不能当源/目标）", compactInts(empties), len(empties)))
	}
	if len(parts) == 0 {
		return "这台工程没有可用轨道。"
	}
	return "这台工程有：" + strings.Join(parts, " · ")
}

// splitRoster 把「真轨」与「空槽」分开。
//
// ⚠️ `track list --include-empty` 会把工程里**所有**空槽都报出来 ——
// 实测这台工程回了 **99 个**（下标 1–99）⇒ 原样塞进结果里，99 行空槽会把真正有用的信息淹掉。
// 空槽仍然要**知道**（它占下标、不能当源/目标），所以是"分开 + 压成区间"，不是"丢掉"。
func splitRoster(tracks []xcopyTrackView) (real []xcopyTrackView, empties []int) {
	for _, t := range tracks {
		if t.Type == "Empty" {
			empties = append(empties, t.Index)
			continue
		}
		real = append(real, t)
	}
	return real, empties
}

// compactInts 把一串下标压成区间串：`1,2,3,7,9,10` ⇒ `1–3,7,9–10`。
// 纯粹为了可读性 —— 99 个空槽写成一行而不是 99 行。
func compactInts(ns []int) string {
	if len(ns) == 0 {
		return ""
	}
	var parts []string
	start, prev := ns[0], ns[0]
	flush := func() {
		switch {
		case start == prev:
			parts = append(parts, strconv.Itoa(start))
		case prev == start+1:
			parts = append(parts, strconv.Itoa(start), strconv.Itoa(prev))
		default:
			parts = append(parts, fmt.Sprintf("%d–%d", start, prev))
		}
	}
	for _, n := range ns[1:] {
		if n == prev+1 {
			prev = n
			continue
		}
		flush()
		start, prev = n, n
	}
	flush()
	return strings.Join(parts, ",")
}

// ---------- 读音符 / 音素 ----------

// readClipNotes 读音符。⚠️ `clip note-content` 是**按位置**寻址的
// （`--track-index` + `--clip-index`），跟其它 clip 动词收 `--clip-uuid` 不同 ——
// 这里传的 `c.Index` 是 `clip list` 原始数组下标（clipListBrief 排过序，
// 但 `Index` 字段保住了原下标，所以按位置寻址是对的）。
func readClipNotes(ctx context.Context, rd ReadFunc, trackIdx int, c clipBrief) (*xcopyNoteContent, error) {
	var nc xcopyNoteContent
	if err := readJSON(ctx, rd, &nc, "clip", "note-content",
		"--track-index", strconv.Itoa(trackIdx), "--clip-index", strconv.Itoa(c.Index)); err != nil {
		return nil, fmt.Errorf("读片段「%s」的音符：%w", c.Name, err)
	}
	return &nc, nil
}

// matchByOnset 是原脚本那个双指针的**直译**：
//
//	i, j 各自在两边按绝对 onset 升序走；相等才配对（然后两边同时前进）。
//
// ⚠️ 它**假设两边都按 onset 升序**。原脚本直接信宿主的音符顺序；这里**先排一遍**
// （`sort.SliceStable` 保住同 onset 的相对次序），因为 ACE 回包的顺序没有承诺 ——
// 顺序错了双指针会**静默错配**（配到不该配的音符上，不报错）。
func matchByOnset(src, dst []xcopyNote, srcBegin, dstBegin int64) (pairs [][2]int, srcOnly, dstOnly int) {
	return matchByOnsetTol(src, dst, srcBegin, dstBegin, 0)
}

// matchByOnsetTol 同上，但允许起点差 ≤ tol tick 也算同一个音（两段之间常有几 tick 的量化差）。
func matchByOnsetTol(src, dst []xcopyNote, srcBegin, dstBegin, tol int64) (pairs [][2]int, srcOnly, dstOnly int) {
	si := make([]xcopyNote, len(src))
	copy(si, src)
	di := make([]xcopyNote, len(dst))
	copy(di, dst)
	sort.SliceStable(si, func(a, b int) bool { return si[a].Pos < si[b].Pos })
	sort.SliceStable(di, func(a, b int) bool { return di[a].Pos < di[b].Pos })

	// uuid → 原数组下标（uuid 唯一）。不查表就得在配对时线性找一遍 ⇒ O(n²)，
	// 而且"按值找回位置"在**同 onset 同音高**的重复音符上会认错人。
	srcAt := make(map[string]int, len(src))
	for i, n := range src {
		srcAt[n.NoteUUID] = i
	}
	dstAt := make(map[string]int, len(dst))
	for i, n := range dst {
		dstAt[n.NoteUUID] = i
	}

	i, j := 0, 0
	for i < len(si) && j < len(di) {
		on1 := srcBegin + si[i].Pos
		on2 := dstBegin + di[j].Pos
		diff := on1 - on2
		if diff < 0 {
			diff = -diff
		}
		switch {
		case diff <= tol:
			pairs = append(pairs, [2]int{srcAt[si[i].NoteUUID], dstAt[di[j].NoteUUID]})
			i++
			j++
		case on1 < on2:
			i++
		default:
			j++
		}
	}
	srcOnly = len(src) - len(pairs)
	dstOnly = len(dst) - len(pairs)
	return pairs, srcOnly, dstOnly
}

func readClipPhonemes(ctx context.Context, rd ReadFunc, uuids []string, what string) (map[string]xcopyPhoneme, error) {
	out := map[string]xcopyPhoneme{}
	if len(uuids) == 0 {
		return out, nil
	}
	args := append([]string{"phoneme", "list", "--note"}, uuids...)
	var pl xcopyPhonemeList
	if err := readJSON(ctx, rd, &pl, args...); err != nil {
		return nil, fmt.Errorf("读音素（%s）：%w", what, err)
	}
	for _, n := range pl.Notes {
		out[n.NoteUUID] = n
	}
	return out, nil
}

// ---------- 计划 ----------

// xcopyPlanData 是 Plan/Apply 共用的"读齐 + 算清"结果。
type xcopyPlanData struct {
	rep     *xcopyReport
	pairs   []xcopyPairPlan
	srcIdx  int
	dstIdx  int
	want    xcopyWant
	started time.Time

	// ⭐ 参数曲线复制（2026-10-07 用户口径："除 uuid 外全都要" ⇒ 参数那一半也搬）。
	// 与音符无关：按**层**整段写（源读实参 → 写目标的可写层）。
	paramWrites []xcopyParamWrite
	// dynamic 是**锚点(points)形状**、而且没有实参 ⇒ 单独一条路（读锚点 → 平移 → 写回）。
	// ⭐ 2026-10-07：`xcopy.within` 的作用域改成**整条轨**之后，目标窗口可能横跨好几个片段 ⇒
	// 一条路变成**多条**（每个目标片段一条），所以这里是切片而不是单个。
	dynWrites []xcopyDynamicWrite

	// clipLabel 是"uuid → 人话片段名"（多片段时计划预览/落地清单里必须写清是哪个片段，
	// 否则 `vocalparam write … --pos-begin 3840` 在两三个片段里看不出说的是哪一个）。
	clipLabel map[string]string

	// dstClipUUID 是**目标片段**的 uuid：参数曲线（`vocalparam write`）按 clip-uuid 寻址，
	// 而音符那几类是按 note-uuid 寻址的 —— 两条路要的东西不一样。
	dstClipUUID string

	// paramSkips 是"参数那一半"的说明（跨世代拒搬、某层没有、读不到……）。
	// ⚠️ 单独收着：`analyzeXcopy` 结尾有一句 `rep.Skipped = nil` 再逐对汇总 ——
	// 参数那半是**每对片段**各自算的，直接写 rep.Skipped 会被那句清掉（实测栽过一次）。
	paramSkips []string
}

// xcopyParamWrite 是一层要写的参数曲线。
type xcopyParamWrite struct {
	ClipUUID string // 目标片段（跨轨那支每一对片段都不一样 ⇒ 必须记在每一笔上）
	Param    string
	Layer    string
	PosBegin int64 // 片段内 tick
	Payload  []*float64
	IfMatch  string // 目标层读回来的指纹（写时守卫）
	Ticks    int
	// DrawnBefore = 目标层在这段里本来画过多少 tick（0 = 整段没画过 ⇒ 这次是"变成显式覆盖"）
	DrawnBefore int
	// Clamped = 越出该参数值域、被夹到边界的 tick 数
	Clamped int
	// Shape = 目标层的形状（dense / points）：points 要用锚点写（2026-10-22）
	Shape string
}

// labelClip 记下某个片段的"人话名字"（计划预览与落地清单里要用）。
func (pd *xcopyPlanData) labelClip(uuid, name string) {
	if uuid == "" {
		return
	}
	if pd.clipLabel == nil {
		pd.clipLabel = map[string]string{}
	}
	if name == "" {
		name = shortID(uuid)
	}
	pd.clipLabel[uuid] = name
}

// clipWhere 给某笔写入配一句"写在哪"（跨片段时必须写清；单片段时返回空串，保持旧报告干净）。
func (pd *xcopyPlanData) clipWhere(uuid string) string {
	if len(pd.clipLabel) <= 1 {
		return ""
	}
	if name, ok := pd.clipLabel[uuid]; ok {
		return fmt.Sprintf("片段「%s」· ", name)
	}
	return "片段 " + shortID(uuid) + " · "
}

// xcopyDynamicWrite 是 dynamic 那条锚点 lane 的复制。
type xcopyDynamicWrite struct {
	ClipUUID string  // 目标片段
	Anchors  [][]any // [[tick, value|null], …]，tick 已经平移到目标范围
	IfMatch  string
	Delta    int64 // 平移动了多少 tick（报告里要说清）
	SrcFrom  string
}

type xcopyWant struct {
	Lyric    bool
	Pitch    bool
	Phonemes bool
}

// xcopyPairPlan 是一对 clip 上"算好的写入清单"。
type xcopyPairPlan struct {
	view xcopyPairView

	// 歌词：位置对应的平行数组（uuid ↔ 新歌词），只收**真的会变**的那些。
	lyricUUIDs []string
	lyricTexts []string

	// 音高：按 (当前音高, 目标音高) 分组 —— 见文件头那条刚性移动的说明。
	pitchGroups []xcopyPitchGroup

	// 音素：一个音符一次调用（ACE 的硬约束）。
	phonemeSets []xcopyPhonemeSet
	// 因为语言不同/本来一样而被跳过的，连同原因。
	skips []string

	// ⭐ 以下三类是 2026-10-07 用户点名补的（"除 uuid 外全都要"）：
	// 时值 dur（`note resize`，一条命令能带多个音符，同一目标时长合成一组）
	resizeGroups []xcopyResizeGroup
	// 辅音时长（`phoneme set-consonant-timing`，逐音符、逐辅音一次调用）
	consonantSets []xcopyConsonantSet
}

// xcopyResizeGroup 是"目标时长一样"的那批音符（`note resize` 一次收多个 uuid、但只收一个 `--dur`）。
type xcopyResizeGroup struct {
	From   int64    // 目标现在的时值（对照用）
	To     int64    // 要改成源那个时值
	NoteID []string // 目标音符 uuid
}

// xcopyConsonantSet 是"把某个辅音改到多长"。
// ⚠️ 长度走**秒**（官方：`--length` 收时钟长度或 tick，但"写口被约束的界是秒"）；
// 用 ms/秒而不是 tick，避免把"源在 146 BPM 下的 0.046s"错当成 tick 抄过去。
type xcopyConsonantSet struct {
	NoteUUID string
	Index    int
	Name     string // 只用于报告
	LengthMs float64
}

type xcopyPitchGroup struct {
	From   int
	To     int
	NoteID []string
}

type xcopyPhonemeSet struct {
	NoteUUID string
	Symbols  []string
	Becomes  bool // 目标原本不是 override ⇒ 这次写入会**把发音冻结成 override**
}

// analyzeXcopy 把两个轨道读齐、配对、算出每个属性要写什么。**只读**。
func analyzeXcopy(ctx context.Context, rd ReadFunc, vals map[string]string) (*xcopyPlanData, error) {
	started := time.Now()
	srcIdx, dstIdx, tracks, err := resolveXcopyTracks(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	want := xcopyWant{
		Lyric:    truthy(value(vals, "lyrics", "")),
		Pitch:    truthy(value(vals, "pitch", "")),
		Phonemes: truthy(value(vals, "phonemes", "")),
	}
	// ⭐ 2026-10-07 用户口径：「复制内容和上一个（轨内复制）相同」⇒ 这一支也补齐这几项。
	wantDur := truthy(value(vals, "dur", ""))
	wantCons := truthy(value(vals, "consonants", ""))
	wantParams := truthy(value(vals, "params", ""))
	wantDyn := truthy(value(vals, "dynamic", ""))
	// ⭐ 2026-10-19：`dynamic → 包络` 本身就是一项内容（v2 的 dynamic 摊到 v1 的 envelope）
	wantDynEnv := strings.TrimSpace(value(vals, "dynEnv", "")) != ""
	if !want.Lyric && !want.Pitch && !want.Phonemes && !wantDur && !wantCons && !wantParams && !wantDyn && !wantDynEnv {
		return nil, fmt.Errorf("一个属性都没勾（歌词 / 音高 / 音素 / 时值 / 辅音时长 / 参数曲线 / dynamic / dynamic→包络），没什么可复制的")
	}

	// ⭐ **可选范围**（用户：「可选范围」）：按**工程小节**给一段；留空 = 不限，整对片段都算。
	// 跨轨这一支两侧本来按**工程时间**对齐 ⇒ 一个工程区间同时约束两边（与参数复制同一口径）。
	rangeLo, rangeHi := int64(math.MinInt64), int64(math.MaxInt64)
	rangeDesc := "不限范围（整对片段）"
	if strings.TrimSpace(value(vals, "beginBar", "")) != "" || strings.TrimSpace(value(vals, "endBar", "")) != "" {
		b1 := atoiOr(value(vals, "beginBar", "1"), 1)
		b2 := atoiOr(value(vals, "endBar", "1"), 1)
		if b1 < 1 || b2 < 1 {
			return nil, fmt.Errorf("小节号从 **1** 起（收到 %d..%d）", b1, b2)
		}
		if b2 < b1 {
			return nil, fmt.Errorf("结束小节（%d）比开始小节（%d）还小 —— 换一下", b2, b1)
		}
		var vr paramViewReport
		tpb := ticksPerBarOf(ctx, rd, &vr)
		rangeLo, rangeHi = int64(b1-1)*tpb, int64(b2)*tpb
		rangeDesc = fmt.Sprintf("第 %d..%d 小节（工程 tick %d..%d，%s）", b1, b2, rangeLo, rangeHi, valueOr(vr.Meter, "按 4/4 算"))
	}

	real, empties := splitRoster(tracks)
	rep := &xcopyReport{
		Mode:     "tracks",
		SrcTrack: srcIdx, SrcTrackName: trackNameOf(tracks, srcIdx),
		DstTrack: dstIdx, DstTrackName: trackNameOf(tracks, dstIdx),
		Tracks: real, EmptySlots: len(empties), EmptySlotList: compactInts(empties),
		Pairs: []xcopyPairView{}, Unported: xcopyUnportable, Range: rangeDesc,
	}

	srcClips, err := clipListBrief(ctx, rd, srcIdx)
	if err != nil {
		return nil, err
	}
	dstClips, err := clipListBrief(ctx, rd, dstIdx)
	if err != nil {
		return nil, err
	}
	if len(srcClips) == 0 {
		return nil, fmt.Errorf("源轨道 %d 上没有片段", srcIdx)
	}
	if len(dstClips) == 0 {
		return nil, fmt.Errorf("目标轨道 %d 上没有片段", dstIdx)
	}

	n := len(srcClips)
	if len(dstClips) < n {
		n = len(dstClips)
	}
	rep.PairCount = n
	rep.UnpairedSrc = len(srcClips) - n
	rep.UnpairedDst = len(dstClips) - n

	pd := &xcopyPlanData{rep: rep, srcIdx: srcIdx, dstIdx: dstIdx, want: want, started: started}
	if len(dstClips) > 0 {
		pd.dstClipUUID = dstClips[0].UUID
	}

	for r := 0; r < n; r++ {
		sc, dc := srcClips[r], dstClips[r]
		v := xcopyPairView{Pair: r + 1, SrcClip: sc.Name, DstClip: dc.Name,
			SrcType: sc.Type, DstType: dc.Type, SrcBegin: sc.Begin, DstBegin: dc.Begin}

		// 歌词/音素只存在于**能唱歌**的片段上；音高在乐器/MIDI 片段上也有
		// ⇒ 按属性**分别门控**，不能因为"这个片段写不了歌词"就把整对跳掉（那会连音高一起丢掉）。
		singOK := sc.Type == "sing" && dc.Type == "sing"
		lyricOK := want.Lyric && singOK
		phonOK := want.Phonemes && singOK

		snc, err := readClipNotes(ctx, rd, srcIdx, sc)
		if err != nil {
			return nil, err
		}
		dnc, err := readClipNotes(ctx, rd, dstIdx, dc)
		if err != nil {
			return nil, err
		}
		v.SrcNotes, v.DstNotes = len(snc.Notes), len(dnc.Notes)

		pairs, _, _ := matchByOnset(snc.Notes, dnc.Notes, sc.Begin, dc.Begin)
		srcIn, dstIn := len(snc.Notes), len(dnc.Notes)
		var rangeSkips []string
		// ⭐ 可选范围：按**绝对**（工程）位置过滤掉范围外的配对。
		// ⚠️ 只在给了小节号时才过滤 —— 不给就是"整对片段"，与老行为一致。
		// ⚠️ 计数也跟着换口径：范围内有多少个音符才是"落单"的分母（范围外的不算落单，只算没参与）。
		if rangeLo != math.MinInt64 {
			kept := pairs[:0:0]
			for _, p := range pairs {
				abs := sc.Begin + snc.Notes[p[0]].Pos
				if abs >= rangeLo && abs < rangeHi {
					kept = append(kept, p)
				}
			}
			if n := len(pairs) - len(kept); n > 0 {
				rangeSkips = append(rangeSkips, fmt.Sprintf("范围外的 %d 个配对不搬（%s）", n, rangeDesc))
			}
			pairs = kept
			srcIn, dstIn = 0, 0
			for _, n := range snc.Notes {
				if abs := sc.Begin + n.Pos; abs >= rangeLo && abs < rangeHi {
					srcIn++
				}
			}
			for _, n := range dnc.Notes {
				if abs := dc.Begin + n.Pos; abs >= rangeLo && abs < rangeHi {
					dstIn++
				}
			}
		}
		v.Matched, v.SrcOnly, v.DstOnly = len(pairs), srcIn-len(pairs), dstIn-len(pairs)
		rep.Matched += len(pairs)
		rep.SrcOnly += srcIn - len(pairs)
		rep.DstOnly += dstIn - len(pairs)
		if len(pairs) == 0 {
			v.Skip = fmt.Sprintf("这一对片段里没有位置对得上的音符（源 %d 个、目标 %d 个；%s）",
				len(snc.Notes), len(dnc.Notes), rangeDesc)
		}

		// ⚠️ 范围提示要挂到**这一对的 pp.skips** 上，不能直接写 rep.Skipped ——
		// analyzeXcopy 结尾有一句 `rep.Skipped = nil` 再逐对汇总，直接写会被它清掉（实测栽过）。
		pp := xcopyPairPlan{view: v}
		pp.skips = append(pp.skips, rangeSkips...)
		if !singOK && (want.Lyric || want.Phonemes) {
			pp.skips = append(pp.skips, fmt.Sprintf(
				"片段「%s」(%s) → 「%s」(%s)：歌词/音素只存在于 sing 片段，**这两个属性跳过**（音高照做）",
				sc.Name, sc.Type, dc.Name, dc.Type))
		}

		// ---- 音素：先在**源**这一侧批量读音素（一次调用覆盖本片段所有配对音符） ----
		var srcPh, dstPh map[string]xcopyPhoneme
		if phonOK {
			var su, du []string
			for _, p := range pairs {
				su = append(su, snc.Notes[p[0]].NoteUUID)
				du = append(du, dnc.Notes[p[1]].NoteUUID)
			}
			srcPh, err = readClipPhonemes(ctx, rd, su, "源片段「"+sc.Name+"」")
			if err != nil {
				return nil, err
			}
			// 目标侧也要读：判断"是不是真的会变"、以及"这次写入会不会把它冻结成 override"。
			dstPh, err = readClipPhonemes(ctx, rd, du, "目标片段「"+dc.Name+"」")
			if err != nil {
				return nil, err
			}
		}

		planPairProps(&pp, snc.Notes, dnc.Notes, pairs, srcPh, dstPh, want, lyricOK, phonOK, rep, true)

		// ⭐ 时值（同一目标时长合成一条）—— 与「轨内复制」同一套
		if wantDur {
			groups := map[int64]*xcopyResizeGroup{}
			for _, p := range pairs {
				s, d := snc.Notes[p[0]], dnc.Notes[p[1]]
				if s.Dur == d.Dur {
					continue
				}
				g := groups[s.Dur]
				if g == nil {
					g = &xcopyResizeGroup{From: d.Dur, To: s.Dur}
					groups[s.Dur] = g
				}
				g.NoteID = append(g.NoteID, d.NoteUUID)
				rep.WillResize++
			}
			for _, g := range groups {
				pp.resizeGroups = append(pp.resizeGroups, *g)
			}
		}

		// ⭐ 辅音时长（只搬手动改过的：v1 看音符上的长度数组、v2 看 pin）
		if wantCons && singOK && len(pairs) > 0 {
			var su, du []string
			for _, p := range pairs {
				su = append(su, snc.Notes[p[0]].NoteUUID)
				du = append(du, dnc.Notes[p[1]].NoteUUID)
			}
			csPh, cerr := readClipPhonemeTimings(ctx, rd, su, "源片段「"+sc.Name+"」")
			if cerr != nil {
				return nil, cerr
			}
			cdPh, cerr := readClipPhonemeTimings(ctx, rd, du, "目标片段「"+dc.Name+"」")
			if cerr != nil {
				return nil, cerr
			}
			n, notManual, nameSkips := 0, 0, 0
			for _, p := range pairs {
				sNote, d := snc.Notes[p[0]], dnc.Notes[p[1]].NoteUUID
				sm, dm := csPh[sNote.NoteUUID], cdPh[d]
				if !sNote.hasManualConsonants() && !anyTimingPinned(sm) {
					notManual++
					continue
				}
				for _, tsm := range sm.Timings {
					if tsm.IsVowel {
						continue
					}
					ok, cur, dstName := false, 0.0, ""
					for _, u := range dm.Timings {
						if u.Index == tsm.Index {
							ok, cur, dstName = !u.IsVowel, u.DurationSec, u.Name
						}
					}
					// ⭐ 2026-10-20：跨代时**按名字核一下**再搬 —— 两侧只按 index 配，
					// 万一两代对同一个词的音素切分不同（index 同、音素不同），就会把时长写到别的辅音上。
					if ok && tsm.Name != "" && dstName != "" && tsm.Name != dstName {
						nameSkips++
						continue
					}
					if !ok || math.Abs(cur-tsm.DurationSec) < 1e-6 {
						continue
					}
					pp.consonantSets = append(pp.consonantSets, xcopyConsonantSet{
						NoteUUID: d, Index: tsm.Index, Name: tsm.Name, LengthMs: tsm.DurationSec * 1000,
					})
					n++
				}
			}
			if nameSkips > 0 {
				pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
					"辅音时长：有 %d 个辅音**两边同名位置上的音素名不同**（index 同、音素不同）⇒ 跳过，"+
						"不把时长写到别的辅音上", nameSkips))
			}
			rep.WillConsonant += n
			rep.SkippedDefaultCons += notManual
		}

		// ⭐ 参数那一半：按**工程时间**对齐（源段与目标段在绝对时间上同一段）
		// ⚠️ `dynamic → 包络`（`dynEnv`）也住在这个函数里 ⇒ 它单独勾着时也得进来一次。
		if wantParams || wantDyn || strings.TrimSpace(value(vals, "dynEnv", "")) != "" {
			absLo, absHi := sc.Begin, sc.End
			if dc.Begin > absLo {
				absLo = dc.Begin
			}
			if dc.End < absHi {
				absHi = dc.End
			}
			if rangeLo != math.MinInt64 {
				if rangeLo > absLo {
					absLo = rangeLo
				}
				if rangeHi < absHi {
					absHi = rangeHi
				}
			}
			if absHi-absLo >= 2 {
				if err := planClipParams(ctx, rd, vals, sc.UUID, dc.UUID,
					absLo-sc.Begin, absHi-sc.Begin, absLo-dc.Begin, absHi-dc.Begin,
					wantParams, wantDyn, pd, rep); err != nil {
					return nil, err
				}
			} else {
				rep.Skipped = append(rep.Skipped, fmt.Sprintf(
					"片段对 %d：源与目标没有重叠的时间段（或范围把它切没了）⇒ 参数那一半跳过", r+1))
			}
		}

		rep.Pairs = append(rep.Pairs, v)
		pd.pairs = append(pd.pairs, pp)
	}

	rep.Skipped = nil
	for _, pp := range pd.pairs {
		rep.Skipped = append(rep.Skipped, pp.skips...)
	}
	// 参数那半的说明（跨世代拒搬、某层没有、读不到……）在这里补回来 ——
	// 它是在循环里逐对算的，而上面那句 `rep.Skipped = nil` 会把直接写进去的清掉。
	rep.Skipped = append(rep.Skipped, pd.paramSkips...)
	return pd, nil
}

func (pp *xcopyPairPlan) addPitch(uuid string, from, to int) {
	for i := range pp.pitchGroups {
		if pp.pitchGroups[i].From == from && pp.pitchGroups[i].To == to {
			pp.pitchGroups[i].NoteID = append(pp.pitchGroups[i].NoteID, uuid)
			return
		}
	}
	pp.pitchGroups = append(pp.pitchGroups, xcopyPitchGroup{From: from, To: to, NoteID: []string{uuid}})
}

func sameSymbols(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// planCalls 把"要写什么"翻成**具体的命令行**（干跑就把这些原样给用户看）。
// 这是设计上的一个便宜事：跨轨复制的 uuid **在计划阶段就全知道了**
// （不像 `track.split` 要等复制出来）⇒ 计划可以给到每条命令，而不是"条数与判据"。
func (pd *xcopyPlanData) planCalls() []string {
	var out []string
	for _, pp := range pd.pairs {
		if len(pp.lyricUUIDs) > 0 {
			js, _ := json.Marshal(pp.lyricTexts)
			out = append(out, fmt.Sprintf("note set-lyric --note-uuid %s --lyrics %s",
				strings.Join(pp.lyricUUIDs, " "), js))
		}
		for _, g := range pp.pitchGroups {
			out = append(out, fmt.Sprintf("note move --note-uuid %s --pitch %d   （%d→%d，%d 个）",
				strings.Join(g.NoteID, " "), g.To, g.From, g.To, len(g.NoteID)))
		}
		for _, ps := range pp.phonemeSets {
			out = append(out, fmt.Sprintf("phoneme set --note %s --phoneme %s",
				ps.NoteUUID, strings.Join(ps.Symbols, " ")))
		}
		// ⭐ 时值：同一个目标时长合成一条命令（`note resize` 一次收多个 uuid + 一个 `--dur`）
		for _, g := range pp.resizeGroups {
			out = append(out, fmt.Sprintf("note resize --note-uuid %s --dur %dt   （%d→%d，%d 个）",
				strings.Join(g.NoteID, " "), g.To, g.From, g.To, len(g.NoteID)))
		}
		// ⭐ 辅音时长：逐音符、逐辅音一次调用（官方 `--note` 只收一个）
		for _, cs := range pp.consonantSets {
			out = append(out, fmt.Sprintf("phoneme set-consonant-timing --note %s --index %d --length %.6fs   （%s）",
				cs.NoteUUID, cs.Index, cs.LengthMs/1000, cs.Name))
		}
	}
	// ⭐ 参数曲线（与音符无关，整段按层写）
	for _, pw := range pd.paramWrites {
		out = append(out, fmt.Sprintf("vocalparam write %s--param %s --layer %s --pos-begin %d --points @<临时文件>   （%d 个 tick）",
			pd.clipWhere(pw.ClipUUID), pw.Param, pw.Layer, pw.PosBegin, len(pw.Payload)))
	}
	for _, dw := range pd.dynWrites {
		out = append(out, fmt.Sprintf("vocalparam write %s--param dynamic --layer user --points @<临时文件>   （%d 个锚点，已平移 %+d tick）",
			pd.clipWhere(dw.ClipUUID), len(dw.Anchors), dw.Delta))
	}
	return out
}

// ---------- 干跑 ----------

func xcopyNotesPlan(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	pd, err := analyzeXcopy(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	rep := pd.rep
	rep.DryRun = true
	rep.Calls = pd.planCalls()
	rep.Millis = time.Since(pd.started).Milliseconds()
	rep.Note = xcopySummary(pd, true)
	return rep, nil
}

// ---------- 真写 ----------

func xcopyNotesApply(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	pd, err := analyzeXcopy(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	rep := pd.rep
	rep.Calls = pd.planCalls()

	done, aerr := applyXcopyPairs(ctx, rd, pd)
	if aerr != nil {
		// ⛔ 不假装原子：ACE 每个动词是**独立的撤销记录**，前面成功的改不回来。
		//    如实报"已经落地了什么"，让用户知道 Ctrl+Z 要按几次。
		rep.Did = done
		rep.Note = fmt.Sprintf("中途失败（%v）—— **前面已经落地的改动没有回滚**"+
			"（ACE 每个动词是独立的撤销记录，Ctrl+Z 可逐步退回）。", aerr)
		rep.Millis = time.Since(pd.started).Milliseconds()
		return rep, nil
	}

	rep.Did = done
	rep.Millis = time.Since(pd.started).Milliseconds()
	rep.Note = xcopySummary(pd, false)
	return rep, nil
}

// xcopySummary 收尾那句话：如实说清"动了什么 / 什么都没动"，以及**移不过来的属性**。
func xcopySummary(pd *xcopyPlanData, dry bool) string {
	rep := pd.rep
	var b strings.Builder
	if dry {
		b.WriteString("干跑：一个字节都没写。")
	} else {
		b.WriteString("已写入。")
	}
	if rep.Matched == 0 {
		b.WriteString(" ⚠️ **一个音符都没配上 onset** —— 两条轨的片段位置/音符位置对不上，什么都没做。")
	}
	// ⚠️ 只在**跨轨**模式下才警告"自己跟自己" —— 轨内模式下源和目标本来就是同一条轨。
	if rep.Mode != "within" && rep.SrcTrack == rep.DstTrack {
		b.WriteString(" ⚠️ **源和目标同一条轨** —— 这一趟是自己跟自己（多半是把 `目标轨道` 留成了 `current`，" +
			"而你就站在源轨上）。已经一样的东西会被跳过，所以正常情况下什么都不写。")
	}
	if rep.UnpairedSrc > 0 || rep.UnpairedDst > 0 {
		b.WriteString(fmt.Sprintf(" ⚠️ 片段数不一样（源 %d / 目标 %d），按**位置顺序**配了前 %d 对，",
			len(rep.Pairs)+rep.UnpairedSrc, len(rep.Pairs)+rep.UnpairedDst, rep.PairCount))
		b.WriteString("多出来的没动。")
	}
	if rep.SrcOnly > 0 {
		b.WriteString(fmt.Sprintf(" ⚠️ 源侧有 %d 个音符在目标里找不到同 onset 的音符（**不会新建**，照原脚本）。", rep.SrcOnly))
	}
	if rep.DstOnly > 0 {
		b.WriteString(fmt.Sprintf(" 目标侧有 %d 个音符没被碰到。", rep.DstOnly))
	}
	b.WriteString(" 移不过来的属性（ACE 没有）：" + xcopyUnportable + "。")
	return b.String()
}

func init() {
	register(&Tool{
		ID: "xcopy.notes", Category: "轨道编辑",
		Title: "跨轨复制音符",
		Desc: "移植 `跨轨道复制.js` 的**音符半**：把**源轨道**的片段按**位置顺序**与目标轨道的片段配对，" +
			"再在每对片段里按**绝对 onset 双指针**找同位置的音符，把勾选的属性搬过去。" +
			"⭐ 复制内容按用户 2026-10-07 的口径「**与轨内复制相同**」：" +
			"**歌词**（一条命令批量写）· **音高**（`note move --pitch`）· **音素**（`phoneme set`，一次一个音符）·" +
			"**时值**（`note resize`）· **辅音时长**（`phoneme set-consonant-timing`）·" +
			"**参数曲线**（源读实参 → 写目标可写层）· **dynamic 锚点**（平移后写回）。" +
			"⛔ **只搬手动改过的**（用户 2026-10-07）：默认发音（`isOverride=false`）与默认辅音时长（无 pin / 无长度数组）**不搬**。" +
			"⭐ **范围可选**（`beginBar`/`endBar`，工程小节）：留空 = 整对片段；给了就只搬落在那段里的音符（两侧按工程时间对齐）。" +
			"⚠️ 只改**已经存在**的音符 —— **不新建**（照原脚本）。" +
			"⚠️ 移不过来的属性（ACE 没有对应概念）如实写在结果里，不做静默近似。" +
			"⚠️ 音素**不能跨语言写**，两侧语言不同就跳过并说明。" +
			"⚠️ 音素符号本来就一样时**跳过**（这一条是移植时多做的一步判断）：ACE 的 `phoneme set` 一定会落一个 override，" +
			"写下去声音一个音都不会变、只是把发音**冻结**住 —— 要不要冻由你定，不该由复制动作顺手替你决定。" +
			"**干跑默认**：勾着只出计划 + 逐条命令预览，一个字节都不写。",
		Kind:   KindWrite,
		Source: "AKD工具侧栏.js 同门的 `跨轨道复制.js`（音符半）",
		Params: []Param{
			textParam("srcTrack", "源轨道", "0", "0 / current",
				"0 起的轨道下标；填 `current`（或留空）= **标记线所在轨道**。结果里会列出这台工程有哪些轨"),
			textParam("dstTrack", "目标轨道", "current", "1 / current",
				"同上。默认 `current` = 把源轨的内容复制到**你正在看的这条轨**"),
			checkParam("lyrics", "复制歌词", true),
			checkParam("pitch", "复制音高", true),
			checkParam("phonemes", "复制音素（只搬手动改过的；会冻结发音为 override）", true),
			checkParam("dur", "复制时值（note resize）", false),
			checkParam("consonants", "复制辅音时长（只搬手动改过的）", false),
			checkParam("params", "复制参数曲线（所有可用参数，逐层整段写）", false),
			textParam("paramLayers", "参数写进哪些层（逗号分隔；留空按目标自己推）", "", "envelope,direct",
				"与「参数映射」同一口径：**默认包络**（envelope），真值(direct)要显式勾；v2 只有 user"),
			textParam("paramSel", "⭐ 参数表格（逐参数逐列勾选；窗口里就是那张表）", "",
				"{\"tension\":[\"effective\",\"envelope\"],\"air\":[\"effective\"]}",
				"JSON 对象：键 = 参数名，值 = 要搬的列。列名只有两个 —— "+
					"`effective`（**实参**，合并后的绝对曲线 ⇒ 落目标的 `direct` 真值层；那一代没有就 `user`）、"+
					"`envelope`（**包络**，乘数层 ⇒ 落目标的 `envelope`，v1 才有）。"+
					"⭐ 这就是两个复制窗口里那张参数表发过来的东西（列由目标花名册现推，见 `xcopy.view` 的 `paramColumns`）；"+
					"**留空** = 走老路径：`params=1` + `paramLayers`（所有参数各搬实参到选定层）"),
			checkParam("dynamic", "复制 dynamic 锚点（v2 的锚点形状 lane，单独一条路）", false),
			// ⭐ 用户 2026-10-19：「做数值映射，跨代允许映射」+「合并」——
			// 这两项只影响**参数那一半**（跨代默认允许，但必须按值域换算）。
			{Name: "map", Label: "数值映射", Type: ParamSelect, Default: "range",
				Options: []Option{
					{Value: "range", Label: "按值域换算（跨代要用它）"},
					{Value: "none", Label: "原值照搬（只在同代安全）"},
				}},
			checkParam("crossGen", "允许跨引擎世代（配『按值域换算』）", true),
			textParam("dynEnv", "dynamic → 包络（跨代映射；`参数=系数`）", "", "falsetto=0.5, tension=-0.3",
				"v2 的 `dynamic`（−1…1、0 = 不动）摊到 v1 的 `envelope` 乘数层：`envelope = 1 + 系数 × dynamic`；"+
					"例 `falsetto=0.5, tension=-0.3`"),
			textParam("beginBar", "范围：开始小节（留空 = 不限）", "", "1", "工程小节，1 起；与 endBar 配用"),
			textParam("endBar", "范围：结束小节（1 起、含）", "", "4", "留空 = 不限范围（整对片段）"),
		},
		Plan:  xcopyNotesPlan,
		Apply: xcopyNotesApply,
	})
}

// applyXcopyPairs 把"算好的写入清单"真发出去，并**边做边记已经落了什么**。
//
// ⭐ 抽出来是为了让 `xcopy.notes`（跨轨按片段配对）与 `xcopy.within`（轨内按小节）共用 ——
// 两支原脚本（`跨轨道复制.js` / `轨道内复制.js`）本来就是**同一个脚本体**，
// 差别只在"怎么配对"和"怎么寻址"，写入那一半完全一样。
//
// ⛔ 不假装原子：ACE 每个动词是**独立的撤销记录**，中途失败时前面已落的改动回不来 ——
// 所以返回**已经落地的那份清单**，让调用方如实报给用户（Ctrl+Z 要按几次）。
func applyXcopyPairs(ctx context.Context, rd ReadFunc, pd *xcopyPlanData) ([]string, error) {
	var done []string
	for _, pp := range pd.pairs {
		// ① 歌词（一条命令写一批）
		if len(pp.lyricUUIDs) > 0 {
			js, _ := json.Marshal(pp.lyricTexts)
			args := append([]string{"note", "set-lyric", "--note-uuid"}, pp.lyricUUIDs...)
			args = append(args, "--lyrics", string(js))
			if err := runWrite(ctx, rd, args...); err != nil {
				return done, fmt.Errorf("写歌词：%w", err)
			}
			done = append(done, fmt.Sprintf("片段「%s」→「%s」：设了 %d 个歌词", pp.view.SrcClip, pp.view.DstClip, len(pp.lyricUUIDs)))
		}
		// ② 音高（每个 (当前,目标) 组合一条）
		for _, g := range pp.pitchGroups {
			args := append([]string{"note", "move", "--note-uuid"}, g.NoteID...)
			args = append(args, "--pitch", strconv.Itoa(g.To))
			if err := runWrite(ctx, rd, args...); err != nil {
				return done, fmt.Errorf("改音高：%w", err)
			}
			done = append(done, fmt.Sprintf("片段「%s」→「%s」：%d 个音符 %d→%d", pp.view.SrcClip, pp.view.DstClip, len(g.NoteID), g.From, g.To))
		}
		// ③ 音素（一个音符一次 —— ACE 的硬约束）
		for _, ps := range pp.phonemeSets {
			args := append([]string{"phoneme", "set", "--note", ps.NoteUUID, "--phoneme"}, ps.Symbols...)
			if err := runWrite(ctx, rd, args...); err != nil {
				return done, fmt.Errorf("写音素：%w", err)
			}
			done = append(done, fmt.Sprintf("音符 %s 音素 → %s", ps.NoteUUID, strings.Join(ps.Symbols, " ")))
		}
		// ④ 时值（同一目标时长一条命令）
		for _, g := range pp.resizeGroups {
			args := append([]string{"note", "resize", "--note-uuid"}, g.NoteID...)
			args = append(args, "--dur", fmt.Sprintf("%dt", g.To))
			if err := runWrite(ctx, rd, args...); err != nil {
				return done, fmt.Errorf("改时值：%w", err)
			}
			done = append(done, fmt.Sprintf("片段「%s」→「%s」：%d 个音符时值 %d→%d tick", pp.view.SrcClip, pp.view.DstClip, len(g.NoteID), g.From, g.To))
		}
		// ⑤ 辅音时长（逐音符、逐辅音一次 —— `--note` 只收一个）
		for _, cs := range pp.consonantSets {
			// ⭐ 2026-10-20：改用 `runWriteRes` 把回包读出来 —— 官方 `set-consonant-timing` 的回包里
			// `representation` 说这次落成了**长度**还是 **pin**，`pinned` 说那条跨度钉住了哪些
			// (noteUuid, index)（**可能跨音符**）。以前用 `runWrite` 丢掉了，报告里看不见。
			res, werr := runWriteRes(ctx, rd, "phoneme", "set-consonant-timing",
				"--note", cs.NoteUUID, "--index", strconv.Itoa(cs.Index),
				"--length", fmt.Sprintf("%.6fs", cs.LengthMs/1000))
			if werr != nil {
				return done, fmt.Errorf("改辅音时长：%w", werr)
			}
			what := ""
			if len(res.JSON) > 0 {
				var rep struct {
					Representation string            `json:"representation"`
					Pinned         []json.RawMessage `json:"pinned"`
				}
				if jerr := json.Unmarshal(res.JSON, &rep); jerr == nil {
					switch rep.Representation {
					case "pin", "pinned":
						what = "（落成 **pin**"
						if len(rep.Pinned) > 0 {
							what += fmt.Sprintf("，钉住 %d 个音素跨度 —— 可能跨音符", len(rep.Pinned))
						}
						what += "）"
					case "length", "consonant-length":
						what = "（落成音符上的**长度**）"
					case "":
					default:
						what = "（表示：" + rep.Representation + "）"
					}
				}
			}
			done = append(done, fmt.Sprintf("音符 %s 第 %d 个辅音（%s）→ %.3f ms%s",
				cs.NoteUUID, cs.Index, cs.Name, cs.LengthMs, what))
		}
	}
	// ⑥ 参数曲线（与音符无关，整段按层写）
	for _, pw := range pd.paramWrites {
		var werr error
		if pw.Shape == "points" {
			anchors := denseToAnchors(pw.Payload, pw.PosBegin)
			if len(anchors) == 0 {
				// 这一段算出来全是空档（没有可落的值）⇒ 没有锚点可写，ACE 会直接拒
				// （INVALID_ARG: 'points' is empty）。跳过它，别报成失败。
				continue
			}
			werr = writePointsLayer(ctx, rd, pw.ClipUUID, pw.Param, pw.Layer, anchors, pw.IfMatch)
		} else {
			werr = writeDenseLayer(ctx, rd, pw.ClipUUID, pw.Param, pw.Layer, pw.PosBegin, pw.Payload, pw.IfMatch)
		}
		if err := werr; err != nil {
			return done, fmt.Errorf("写参数 %s 的 %s 层：%w", pw.Param, pw.Layer, err)
		}
		done = append(done, fmt.Sprintf("%s参数 %s 的 %s 层写了 %d 个 tick（pos-begin %d）",
			pd.clipWhere(pw.ClipUUID), pw.Param, pw.Layer, len(pw.Payload), pw.PosBegin))
	}
	// ⑦ dynamic 的锚点 lane（**每个目标片段一条** —— 轨内那支可能横跨好几个片段）
	for _, dw := range pd.dynWrites {
		payload := make([]any, 0, len(dw.Anchors))
		for _, a := range dw.Anchors {
			payload = append(payload, a)
		}
		if err := writePointsLayer(ctx, rd, dw.ClipUUID, "dynamic", "user", payload, dw.IfMatch); err != nil {
			return done, fmt.Errorf("写 dynamic 锚点：%w", err)
		}
		done = append(done, fmt.Sprintf("%s参数 dynamic 的 user 层写了 %d 个锚点（平移 %+d tick）",
			pd.clipWhere(dw.ClipUUID), len(payload), dw.Delta))
	}
	return done, nil
}

// planPairProps 是"这一对片段里每个配对音符要写什么"那一段（歌词 / 音高 / 音素）。
//
// ⭐ 抽出来是为了让 `xcopy.notes`（跨轨按片段配对）与 `xcopy.within`（轨内按小节）共用 ——
// 两支原脚本本来就是**同一个脚本体**，属性那一半完全一样。
//
// 改动前它写在 analyzeXcopy 的 `for r` 循环里，直接用了闭包外的名字；
// 现在把那些名字都变成入参（同名），所以函数体**一个字都没改**。
func planPairProps(pp *xcopyPairPlan, srcNotes, dstNotes []xcopyNote, pairs [][2]int,
	srcPh, dstPh map[string]xcopyPhoneme, want xcopyWant, lyricOK, phonOK bool, rep *xcopyReport,
	onlyManual ...bool) {
	// `onlyManual=true`（轨内复制那一支）= 只搬**手动改过**的音素（用户 2026-10-07 口径）；
	// 跨轨复制那一支暂时保持原样（变参是为了不动它的调用点与既有测试）。
	manualOnly := len(onlyManual) > 0 && onlyManual[0]
	for _, p := range pairs {
		s, d := srcNotes[p[0]], dstNotes[p[1]]

		// ---- 歌词：原样搬（不做任何清洗 —— 原脚本也是直接 setLyrics） ----
		if lyricOK {
			if s.Lyric == d.Lyric {
				pp.skips = append(pp.skips, fmt.Sprintf("「%s」@%d 歌词本来就一样（%q），跳过", d.NoteUUID, d.Pos, d.Lyric))
			} else {
				pp.lyricUUIDs = append(pp.lyricUUIDs, d.NoteUUID)
				pp.lyricTexts = append(pp.lyricTexts, s.Lyric)
				rep.WillLyric++
			}
		}

		// ---- 音高：分组（当前音高, 目标音高） ----
		if want.Pitch {
			if s.Pitch == d.Pitch {
				pp.skips = append(pp.skips, fmt.Sprintf("「%s」@%d 音高本来就一样（%d），跳过", d.NoteUUID, d.Pos, d.Pitch))
			} else {
				pp.addPitch(d.NoteUUID, d.Pitch, s.Pitch)
				rep.WillPitch++
			}
		}

		// ---- 音素 ----
		if phonOK {
			sp, ok1 := srcPh[s.NoteUUID]
			dp, ok2 := dstPh[d.NoteUUID]
			switch {
			case !ok1 || len(sp.Phonemes) == 0:
				pp.skips = append(pp.skips, fmt.Sprintf("「%s」源音符没读出音素，跳过", s.NoteUUID))
			case !ok2:
				pp.skips = append(pp.skips, fmt.Sprintf("「%s」目标音符没读出音素，跳过", d.NoteUUID))
			case manualOnly && !sp.IsOverride:
				// ⛔ 用户 2026-10-07：「复制的内容为**手动更改过的**，默认的音素不复制」
				//    `isOverride=false` = 这个音符还在唱它自己算出来的默认发音 ⇒ 那是引擎的产物、
				//    不是用户的意图；搬到目标上只会平白冻住对方的发音。
				rep.SkippedDefaultPhonemes++
			case sp.Language != dp.Language:
				// ⛔ 音素符号必须属于**目标音符自己的语言** ⇒ 语言不同只能跳过。
				//    不替用户改语言：语言是用户比我们清楚的事（playbook §0.6 语言纪律）。
				pp.skips = append(pp.skips, fmt.Sprintf("「%s」源是 %s、目标是 %s —— 音素不能跨语言写，跳过（改语言请用「设语言」那一路）",
					d.NoteUUID, sp.Language, dp.Language))
			case sameSymbols(sp.Phonemes, dp.Phonemes):
				// ⚠️ 这里**比原脚本多做一步判断**（原脚本无条件 `dst.setPhonemes`）。
				// 理由：ACE 的 `phoneme set` 一定会落一个 **override**，而 override 之后
				// 改歌词不再影响这个音符的发音（"发音被冻结"）—— 符号本来就一样时，
				// 写下去**声音一个音都不会变**，只是把发音冻住。所以跳过，并把两种情形分开讲
				// （"要不要冻结"是用户能决定的语义差别，不该由复制动作顺手替他决定）。
				if dp.IsOverride {
					pp.skips = append(pp.skips, fmt.Sprintf("「%s」已经是同样的 override，跳过", d.NoteUUID))
				} else {
					pp.skips = append(pp.skips, fmt.Sprintf(
						"「%s」音素符号已经一样（目标本来没被改写）⇒ 跳过：写下去只会把发音**冻结**成 override，声音不变",
						d.NoteUUID))
				}
			default:
				pp.phonemeSets = append(pp.phonemeSets, xcopyPhonemeSet{
					NoteUUID: d.NoteUUID, Symbols: append([]string{}, sp.Phonemes...), Becomes: !dp.IsOverride,
				})
				rep.WillPhonemes++
			}
		}
	}
}
