package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// LRC 导出 —— 移植 SV 脚本 `导出lrc歌词至剪贴板.js`（191 行）。
//
// 原脚本做的事：把当前片段（或选中音符）按「缺口 / 换气 `br`」切**行** → 去掉 `-`/`+` →
// 拼出一行文字（中日韩不分隔，其余按空格）→ 生成 `[mm:ss.cc]` → **相邻重复行合并成一行多个时间戳**
// → 可调偏移（分/秒/毫秒）→ 写系统剪贴板 + 弹框显示。
//
// ⛔ **ACE 自己有 `export lrc`，但那份不能直接用**（实测这份工程导出的文件）：
//   - 它**不合并**重复行（第 1、2 行文字一模一样却是两条）
//   - 它给每个气口插了一条**空时间戳行**（45 句导成了 55 行，其中 10 条空的）
//
// 用户口径（2026-10-05）：**照原脚本** —— 合并重复行、气口不插空行。所以时间戳要自己算。
//
// ⭐ 时间戳：ACE 的速度表是**分段**的（这份工程 27 个点：146 → 131 → 127 → 114 → …），
// 所以 tick→秒 必须按段积。**已用 ACE 自己导出的那份 LRC 对过账**：
// tick 14400 在 146.0003 BPM 下 = 14400/(146.0003×8) = 12.3287 s ⇒ `[00:12.32]`，与它的第 1 行逐字相同。
//
// 两处**刻意的移植改动**（都因为宿主的数据模型不同，见 §38 的同类判断）：
//  1. 原脚本用 `phonemes` 里数元音来判断"一个音符唱了几个音节"，多音节的词套 `( )`。
//     ACE 的多音符词是**逐音符带 `#n` 后缀**存的（`hello#1` `hello#2`）⇒ 套括号会得到
//     `(hello#1)(hello#2)`（那是给 SV 歌词框回填用的写法，放进 LRC 是垃圾）。
//     这里改成**把同一个词的后半截收起来**（`hello#1 hello#2` → `hello`），也就是 ACE 自己合并出来的样子。
//  2. 原脚本"中日韩不分隔"是**整串不加空格**（`jinqingzhan`）；这里按**语种**分隔：
//     两个 CJK 词之间紧排、其余留空格 —— 与 ACE 的合并规则一致（`jin qing zhan`）。
//     这两条都靠 `joinLyricsTimed` 复用「填词」那套已验证的拼接（§38.3）。
func init() {
	offsetParams := []Param{
		numParam("offsetMin", "偏移（分）", "0"),
		numParam("offsetSec", "偏移（秒）", "0"),
		numParam("offsetMs", "偏移（毫秒）", "0"),
		// ⭐ 用户 2026-10-05：「增加个忽略短间隙选项」
		// 原脚本是**有任何缝就换行**；实测这份工程最小的缝只有 30 tick（≈26 ms，`hao` 之间的断音）
		// ⇒ 147 行，而 ACE 自带忽略 120 tick 以下的缝 ⇒ 65 行。
		numParam("gapBeats", "忽略短间隙（拍，0 = 不忽略、与原脚本一致）", "0"),
		// ⭐ 用户 2026-10-05：「cjk不加空格，非cjk加」+「做选项」
		checkParam("cjkTight", "CJK 之间不加空格", true),
	}

	register(&Tool{
		ID: "lyric.lrc", Category: "歌词",
		Title: "LRC 导出",
		Desc: "按 SV 脚本「导出 lrc 歌词至剪贴板」的口径生成 LRC：按**缺口/换气**切行、去掉 `-`/`+`、" +
			"**相邻重复行合并成一行多个时间戳**、气口**不插空行**、可加偏移（分/秒/毫秒）。" +
			"⛔ 不用 ACE 自带的 `export lrc`：它不合并重复行、还会给气口插空时间戳行（实测 45 句导成 55 行）。" +
			"⭐ 空格规则（用户口径）：**CJK 之间不加空格、非 CJK 加**（汉语拼音是拉丁字符 ⇒ 加空格）。" +
			"`gapTicks`：**忽略短间隙**（原脚本有任何缝就换行；实测本工程最小的缝 30 tick ⇒ 147 行，" +
			"忽略 120 tick 以下的缝正好得到 ACE 自带那份的 65 行）。" +
			"⭐ 每行还带上**逐字符的时间**（`charSec`）—— 窗口里在哪个字上断开，新行就用那个音符真正的起始时间。" +
			"⚠️ 只读。落盘用「LRC 写成文件」。",
		Kind:   KindRead,
		Source: "导出lrc歌词至剪贴板.js（AKD · 191 行）",
		Params: offsetParams,
		Inspect: func(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
			return runLRCExport(ctx, rd, vals)
		},
	})

	register(&Tool{
		ID: "lyric.lrcFile", Category: "歌词",
		Title: "LRC 写成文件",
		Desc: "把**你给的那份 LRC 文本**写成一个 `.lrc` 文件（UTF-8 **带 BOM** —— Windows 上的中文播放器" +
			"认这个最稳）。写的是**你看得见的那份**，不是重算一份（所以你在窗口里分割/合并的结果会被原样落盘）。" +
			"⚠️ 只收 `.lrc` 结尾的路径（不做一个能往任意路径写字的通用口子）；同名文件**会被覆盖**，" +
			"回报里会说明覆盖没覆盖。⚠️ 写：默认干跑（只报路径/字节数/行数，一个字节都不写）。",
		Kind:   KindWrite,
		Source: "自建（原脚本只写剪贴板；用户要能落盘）",
		// 只落一个文件、**不碰 ACE 撤销栈** ⇒ 没有可撤回的条目，别白读两次栈。
		NoUndoMark: true,
		Params: []Param{
			textParam("path", "写到哪个文件（要 .lrc 结尾）", "", `C:\Users\你\Documents\歌.lrc`, "目录必须已存在；同名会被覆盖"),
			areaParam("text", "LRC 文本（写你看得见的那份）", "", 10, "#EXTMETA 之类不写，只有时间戳行", ""),
		},
		Plan:  lrcFilePlan,
		Apply: lrcFileApply,
	})
}

// ---------- 读 + 渲染 ----------

type aceNote struct {
	Pos      int64  `json:"pos"`
	Dur      int64  `json:"dur"`
	EndPos   int64  `json:"endPos"`
	Lyric    string `json:"lyric"`
	Language string `json:"language"`
	NoteUUID string `json:"noteUuid"`
}

// lrcRow 是 LRC 的**一行**。
//
// ⚠️ `Times` 是复数：原脚本会把**相邻重复行合并成一行多个时间戳**（卡拉OK 的重复段写法），
// 所以一行可能带好几个时间戳 —— 别把它当成"一行一个时间"。
type lrcRow struct {
	Index     int       `json:"index"`
	Tick      int64     `json:"tick"`      // 绝对 tick（这一行的第一个音符）
	Sec       float64   `json:"sec"`       // 原始秒（**不含偏移**；偏移在渲染时才加）
	Times     []float64 `json:"times"`     // 合并之后这一行的所有时间戳（秒，原始）
	Text      string    `json:"text"`      // 显示/可编辑的那串
	CharSec   []float64 `json:"charSec"`   // 逐**字符**的原始秒（分割用：断点处那个字符的起始时间）
	CharNote  []int     `json:"charNote"`  // 逐**字符**属于第几个词（多音符词算一个）—— 断点只能落在词的边界上
	NoteCount int       `json:"noteCount"` // 这一行（未合并前）有几个音符
}

type lrcResult struct {
	LRC         string   `json:"lrc"`
	Rows        []lrcRow `json:"rows"`
	LineCount   int      `json:"lineCount"`  // 合并之后的行数（= 成品 LRC 的行数）
	RawLines    int      `json:"rawLines"`   // 合并之前有几行（= 切出来的句子数）
	MergedRows  int      `json:"mergedRows"` // 有几行是合并出来的（时间戳 > 1 个）
	Skipped     int      `json:"skipped"`    // 被跳过的空行（原脚本在这里会**崩**，见下）
	IsCJK       bool     `json:"isCJK"`      // ACE 报的语种是不是中日韩（**只作说明**，不再决定空格）
	CJKTight    bool     `json:"cjkTight"`   // 「CJK 之间不加空格」这个选项的实际取值
	GapTicks    int      `json:"gapTicks"`   // 忽略多少 tick 以下的短间隙
	OffsetSec   float64  `json:"offsetSec"`
	ClipName    string   `json:"clipName,omitempty"`
	ClipUUID    string   `json:"clipUuid,omitempty"`
	SuggestPath string   `json:"suggestPath,omitempty"`
	Note        string   `json:"note,omitempty"`
}

// lrcGapTicks 取「忽略短间隙」的阈值。
//
// ⚠️ 单位口径（用户 2026-10-14）：**对外是「拍」** ⇒ 参数是 `gapBeats`（0.25 = 十六分音符）。
// 旧的 `gapTicks` 仍然认（老调用方/脚本不至于当场坏掉），**按 tick 解释**（内部单位）。
func lrcGapTicks(vals map[string]string) (int, error) {
	if s := strings.TrimSpace(value(vals, "gapBeats", "")); s != "" {
		beat, err := strconv.ParseFloat(s, 64)
		if err != nil || beat < 0 {
			return 0, fmt.Errorf("「忽略短间隙（拍）」要是非负数（0.25 = 十六分音符），实得 %q", s)
		}
		return int(beatsToTicks(beat)), nil
	}
	return intValue(vals, "gapTicks", 0, "忽略短间隙（tick）")
}

func runLRCExport(ctx context.Context, rd ReadFunc, vals map[string]string) (*lrcResult, error) {
	offset, err := offsetSeconds(vals)
	if err != nil {
		return nil, err
	}
	gapTicks, err := lrcGapTicks(vals)
	if err != nil {
		return nil, err
	}
	cjkTight := boolValue(vals["cjkTight"], true)
	ref, err := currentClip(ctx, rd)
	if err != nil {
		return nil, err
	}
	notes, err := readClipNotesFull(ctx, rd, ref)
	if err != nil {
		return nil, err
	}
	if len(notes) == 0 {
		return nil, fmt.Errorf("这个片段里没有音符（`clip note-content` 报 0 个）")
	}
	sec := tickToSecFrom(ctx, rd)
	if sec == nil {
		return nil, fmt.Errorf("读不到速度表（`tempo get`）—— 没有它就没法把 tick 换成 LRC 要的秒")
	}

	// 中日韩判定：**看 ACE 报的语种**（用户 2026-10-05：「汉语拼音也是中文」——
	// 拼音音符的 `language` 就是 `Chinese`，靠数字符会把它判成英文）。
	isCJK := majorityCJKLanguage(notes)

	// ① 切行（原脚本的规则：不是换气、不是 `-`/`+` 的音符进当前行；遇到**缺口**或**下一个是换气**就换行）
	//    ⭐ 加上「忽略短间隙」：缝 > gapTicks 才换行（gapTicks = 0 时与原脚本**逐字节相同**）。
	var groups [][]aceNote
	cur := []aceNote{}
	for i := range notes {
		n := notes[i]
		if isBreathLyric(n.Lyric) {
			// ⚠️ 换气音符**什么都不做**（不进当前行、也不触发换行）—— 与原脚本一致；
			// 它前面的那个音符会通过 nextIsBreath 触发出一次换行。
		} else {
			if n.Lyric != "-" && n.Lyric != "+" {
				cur = append(cur, n)
			}
			if i < len(notes)-1 {
				gap := notes[i+1].Pos - n.EndPos
				if gap > int64(gapTicks) || isBreathLyric(notes[i+1].Lyric) {
					groups = append(groups, cur)
					cur = []aceNote{}
				}
			}
		}
	}
	groups = append(groups, cur)

	// ② 每行文字 + 逐字符时间 + 合并相邻重复行
	res := &lrcResult{IsCJK: isCJK, OffsetSec: offset, ClipName: ref.ClipName, ClipUUID: ref.ClipUUID}
	rows := make([]lrcRow, 0, len(groups))
	for _, g := range groups {
		if len(g) == 0 {
			// ⛔ 原脚本在这里会 `group[g][0].Onset` 取 undefined ⇒ **崩**（末尾是换气音符时就会走到）。
			// 这里跳过并记账，绝不让它把整个导出带崩。
			res.Skipped++
			continue
		}
		text, charSec, charNote := joinLyricsTimed(g, cjkTight, sec, ref.ClipBegin)
		at := sec(float64(ref.ClipBegin + g[0].Pos))
		if len(rows) > 0 && rows[len(rows)-1].Text == text {
			// 相邻重复行 ⇒ **合并时间戳**（原脚本的 Merge 分支）
			rows[len(rows)-1].Times = append(rows[len(rows)-1].Times, at)
			continue
		}
		rows = append(rows, lrcRow{
			Index: len(rows), Tick: ref.ClipBegin + g[0].Pos, Sec: at, Times: []float64{at},
			Text: text, CharSec: charSec, CharNote: charNote, NoteCount: len(g),
		})
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("切出来 0 行（%d 个空行被跳过）—— 这个片段的音符都是换气/连音？", res.Skipped)
	}
	res.RawLines = len(groups)
	res.LineCount = len(rows)
	res.GapTicks = gapTicks
	res.CJKTight = cjkTight
	for i := range rows {
		if len(rows[i].Times) > 1 {
			res.MergedRows++
		}
	}
	res.Rows = rows
	res.LRC = renderLRC(rows, offset)

	// 建议落盘路径：文档目录 + 片段名（把文件名里不能用的字符换掉）
	res.SuggestPath = suggestLRCPath(ref.ClipName)
	return res, nil
}

// readClipNotesFull 读整个片段的逐音符内容（带 EndPos —— 切行要用）。
func readClipNotesFull(ctx context.Context, rd ReadFunc, ref clipRef) ([]aceNote, error) {
	var clip struct {
		Notes []aceNote `json:"notes"`
	}
	if err := readJSON(ctx, rd, &clip, "clip", "note-content",
		"--track-index", strconv.Itoa(ref.Track), "--clip-index", strconv.Itoa(ref.Clip)); err != nil {
		return nil, fmt.Errorf("读音符失败（轨道 %d 片段 %d）：%w", ref.Track, ref.Clip, err)
	}
	return clip.Notes, nil
}

// isBreathLyric 是原脚本那条正则 `/^(l?br\d*)$/`：`br` / `br1` / `lbr` / `lbr12` 都算换气。
func isBreathLyric(s string) bool {
	if !strings.HasPrefix(s, "br") && !strings.HasPrefix(s, "lbr") {
		return false
	}
	rest := strings.TrimPrefix(s, "l")
	rest = strings.TrimPrefix(rest, "br")
	if rest == "" {
		return true
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// majorityCJKLanguage 看这份素材里有没有 CJK 字（**只看文本，不看 ACE 的语种字段** ——
// 用户 2026-10-05：「不按语种，汉语拼音也算中文，按语种会让汉语拼音组成的不好编辑」）。
//
// ⚠️ 它的结果**只作说明**（`res.isCJK`）：**空格规则是按字符逐个判的**（`isCJKToken`），
// 跟这个开关无关 —— 所以拼音既不会被粘成 `suizhejiao`，也不会把中文句拆成 `跳 一 曲`。
func majorityCJKLanguage(notes []aceNote) bool {
	lyrics := make([]string, 0, len(notes))
	for _, n := range notes {
		if n.Lyric == "-" || n.Lyric == "+" || isBreathLyric(n.Lyric) {
			continue
		}
		lyrics = append(lyrics, n.Lyric)
	}
	return mostlyCJK(lyrics)
}

// ---------- tick → 秒（分段积） ----------

// secFunc 把**绝对 tick** 换成秒。
type secFunc func(tick float64) float64

// tickToSecFrom 读一次速度表，造一个分段积的换算函数。
// 分段常速：每个点的 BPM 从它自己的位置管到下一个点（同一位置有多个点时**取最后一个** ——
// ACE 的速度表里那种成对的点就是"这里换成新速度"的意思）。
func tickToSecFrom(ctx context.Context, rd ReadFunc) secFunc {
	var tp struct {
		Points []struct {
			Pos   int64   `json:"pos"`
			Value float64 `json:"value"`
		} `json:"points"`
	}
	if err := readJSON(ctx, rd, &tp, "tempo", "get"); err != nil || len(tp.Points) == 0 {
		return nil
	}
	// 折叠同位置的点（保留最后一个），并按位置排序（ACE 本来就是升序，这里不假设）
	var pos []float64
	var bpm []float64
	for _, p := range tp.Points {
		if p.Value <= 0 {
			continue
		}
		if n := len(pos); n > 0 && pos[n-1] == float64(p.Pos) {
			bpm[n-1] = p.Value
			continue
		}
		pos = append(pos, float64(p.Pos))
		bpm = append(bpm, p.Value)
	}
	if len(pos) == 0 {
		return nil
	}
	// 每段起点对应的"已经过去多少秒"
	cum := make([]float64, len(pos))
	for i := 1; i < len(pos); i++ {
		cum[i] = cum[i-1] + (pos[i]-pos[i-1])/(bpm[i-1]*8) // 480 PPQ：tick/秒 = BPM×8
	}
	return func(tick float64) float64 {
		i := len(pos) - 1
		for k := 1; k < len(pos); k++ {
			if tick < pos[k] {
				i = k - 1
				break
			}
		}
		return cum[i] + (tick-pos[i])/(bpm[i]*8)
	}
}

// ---------- 拼一行 + 逐字符时间 ----------

// timedTok 是拼行时的最小单位：一个词 + 它的起始时间 + 它是第几个词。
type timedTok struct {
	Text string
	Sec  float64
	Idx  int // 第几个词（0 起）—— 一整个多音符词算**一个**（`hello#1 hello#2` 同时开始、不能从中间断）
}

// joinLyricsTimed 与「填词」那套 `collapseTenuto` + `joinCollapsed` **同一条规则**，
// 但额外记下**每个字符**落在哪个词上（`charSec` 时间 + `charNote` 序号）—— 分割要用。
//
// 空格规则（用户 2026-10-05 的口径）：**CJK 之间不加空格、非 CJK 加**（汉语拼音是拉丁字符 ⇒ 加空格）。
// `cjkTight` 就是那个「CJK 之间不加空格」选项：关掉 ⇒ 一律加空格。
//
// 记法：一个词的所有字符都记这个词的时间与序号；**分隔空格归前一个词** ——
// 这样"光标停在空格后面"时，断点落在**下一个词的开头**（用户 2026-10-05：
// 「分割点 = 光标位置之前和之后的那两个音符之间的间隙」）。
func joinLyricsTimed(notes []aceNote, cjkTight bool, sec secFunc, clipBegin int64) (string, []float64, []int) {
	// ① 收起连音：丢掉 `-` / `+` / 换气；多音符词的 `#n` 只留第一段（`hello#1 hello#2` → `hello`）
	toks := make([]timedTok, 0, len(notes))
	for _, n := range notes {
		if n.Lyric == "-" || n.Lyric == "+" || isBreathLyric(n.Lyric) {
			continue // 防御性：调用方已经滤过一遍，但这个函数自己也得站得住
		}
		base, has := stripSyllable(n.Lyric)
		at := sec(float64(clipBegin + n.Pos))
		if has {
			if k := len(toks); k > 0 && toks[k-1].Text == base {
				continue // 同一个词的后半截（`hello#2`）
			}
			toks = append(toks, timedTok{Text: base, Sec: at, Idx: len(toks)})
			continue
		}
		toks = append(toks, timedTok{Text: n.Lyric, Sec: at, Idx: len(toks)})
	}
	// ② 拼：**CJK 之间紧排，其余空格**（用户口径）；`cjkTight` 关掉就一律空格
	var b strings.Builder
	var charSec []float64
	var charNote []int
	for i, t := range toks {
		if i > 0 {
			tight := cjkTight && isCJKToken(toks[i-1].Text) && isCJKToken(t.Text)
			if !tight {
				b.WriteString(" ")
				// ⚠️ 空格**归前一个词**（时间与序号都用前一个词的）：光标停在空格后面时，
				// 断点才会落到下一个词的开头，而不是把空格算进后一行。
				charSec = append(charSec, toks[i-1].Sec)
				charNote = append(charNote, toks[i-1].Idx)
			}
		}
		for range t.Text {
			charSec = append(charSec, t.Sec)
			charNote = append(charNote, t.Idx)
		}
		b.WriteString(t.Text)
	}
	return b.String(), charSec, charNote
}

// ---------- 渲染 ----------

// formatLRCTime 复刻原脚本的 `formatTime`：`[mm:ss.cc]`，**截断**（不四舍五入），支持负数。
// ⚠️ 页面侧另有一份同规则的 JS（要实时重渲染）；`devtools/check-ui.js` 用同一组向量对两边。
func formatLRCTime(seconds, offsetSec float64) string {
	t := seconds + offsetSec
	abs := t
	sign := ""
	if t < 0 {
		abs = -t
		sign = "-"
	}
	min := int(abs / 60)
	s := int(abs) % 60
	cs := int((abs * 100)) % 100 // 与原脚本 parseInt((abs*100)%100) 等价（正数截断）
	return fmt.Sprintf("[%s%02d:%02d.%02d]", sign, min, s, cs)
}

func renderLRC(rows []lrcRow, offsetSec float64) string {
	var b strings.Builder
	for _, r := range rows {
		for _, t := range r.Times {
			b.WriteString(formatLRCTime(t, offsetSec))
		}
		b.WriteString(r.Text)
		b.WriteString("\n")
	}
	return b.String()
}

// offsetSeconds 把「分/秒/毫秒」三个框折成秒（原脚本就是这三个）。
func offsetSeconds(vals map[string]string) (float64, error) {
	m, err := intValue(vals, "offsetMin", 0, "偏移（分）")
	if err != nil {
		return 0, err
	}
	s, err := intValue(vals, "offsetSec", 0, "偏移（秒）")
	if err != nil {
		return 0, err
	}
	ms, err := intValue(vals, "offsetMs", 0, "偏移（毫秒）")
	if err != nil {
		return 0, err
	}
	return float64(m)*60 + float64(s) + float64(ms)/1000, nil
}

// suggestLRCPath 给一个能直接用的落盘路径：文档目录 + 片段名（去掉文件名里不能用的字符）。
func suggestLRCPath(clipName string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(clipName)
	var b strings.Builder
	for _, r := range name {
		switch r {
		case '\\', '/', ':', '*', '?', '"', '<', '>', '|':
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	name = strings.TrimSpace(b.String())
	name = strings.Trim(name, ". ")
	if name == "" {
		name = "歌词"
	}
	if len([]rune(name)) > 40 {
		name = string([]rune(name)[:40])
	}
	return filepath.Join(home, "Documents", name+".lrc")
}

// ---------- 写文件（Plan / Apply） ----------

type lrcFileReport struct {
	DryRun    bool   `json:"dryRun"`
	Path      string `json:"path"`
	Bytes     int    `json:"bytes"`
	Lines     int    `json:"lines"`
	Overwrote bool   `json:"overwrote"`
	Hint      string `json:"hint,omitempty"`
}

// lrcFileArgs 取并校验两个参数：路径必须是 `.lrc` 结尾（不做一个能往任意路径写字的通用口子）。
func lrcFileArgs(vals map[string]string) (path, text string, err error) {
	path = strings.Trim(strings.TrimSpace(vals["path"]), `"`)
	if path == "" {
		return "", "", fmt.Errorf("要给一个落盘路径（要 `.lrc` 结尾）")
	}
	if !strings.EqualFold(filepath.Ext(path), ".lrc") {
		return "", "", fmt.Errorf("路径必须以 `.lrc` 结尾（实得 %q）—— 这个工具只写 LRC，"+
			"不做一个能往任意路径写字的通用口子", filepath.Ext(path))
	}
	text = vals["text"]
	if strings.TrimSpace(text) == "" {
		return "", "", fmt.Errorf("LRC 文本是空的 —— 先在窗口里读一份出来（或者按刷新）")
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n" // LRC 每行一条，末尾补一个换行（播放器不挑，但看着整齐）
	}
	return path, text, nil
}

func lrcFilePlan(_ context.Context, _ ReadFunc, vals map[string]string) (any, error) {
	path, text, err := lrcFileArgs(vals)
	if err != nil {
		return nil, err
	}
	_, statErr := os.Stat(path)
	return &lrcFileReport{
		DryRun: true, Path: path,
		Bytes:     len(text) + 3, // UTF-8 BOM 三个字节
		Lines:     strings.Count(text, "\n"),
		Overwrote: statErr == nil,
		Hint: "干跑：**一个字节都没写**。取消勾选「干跑」才落盘" +
			"（写的是你看得见的那份，UTF-8 带 BOM）。",
	}, nil
}

func lrcFileApply(_ context.Context, _ ReadFunc, vals map[string]string) (any, error) {
	path, text, err := lrcFileArgs(vals)
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(filepath.Dir(path)); statErr != nil {
		return nil, fmt.Errorf("目录不存在：%s —— 先建目录（这个工具**不替你建**，免得写到你没想到的地方）",
			filepath.Dir(path))
	}
	_, statErr := os.Stat(path)
	// UTF-8 **带 BOM**：Windows 上的中文播放器认这个最稳（不带 BOM 时容易被当成 GBK 显示成乱码）。
	body := append([]byte{0xEF, 0xBB, 0xBF}, []byte(text)...)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return nil, fmt.Errorf("写 %s 失败：%w", path, err)
	}
	return &lrcFileReport{
		Path: path, Bytes: len(body), Lines: strings.Count(text, "\n"), Overwrote: statErr == nil,
		Hint: "已落盘（UTF-8 带 BOM）。",
	}, nil
}
