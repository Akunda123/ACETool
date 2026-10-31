package jianpu

import (
	"encoding/xml"
	"fmt"
	"math"
	"strings"
)

// ⭐ **MusicXML 做中间形态**（用户 2026-10-05 定）。
//
// 这样整条链是：
//
//	简谱文本 ⇄ 音符块 ⇄ MusicXML ⇄ { ACE `import file` | MuseScore 等打谱软件 | OSMD/VexFlow 渲染 }
//
// 为什么选它当中转：
//
//	· **ACE 收 MusicXML**（`import file --help` 明写 MIDI 与 MusicXML 都收，还支持"把复调拆成多条单声部轨"）；
//	· 五线谱世界的事实标准 —— 打谱软件、渲染库（OSMD/VexFlow/Verovio）都认；
//	· **不引入任何 Go 依赖**：纯文本 XML，标准库就够。
//
// ⚠️ ACE **只能出 MIDI、不出 MusicXML**（`export` 域里没有 musicxml）⇒ 想"从 ACE 拿五线谱"这条路，
// 要么我们把 `clip note-content` 的音符直接写成 MusicXML（就是本文件的正向），要么走 MIDI 中转。
//
// 三处必须说清的语义：
//
//  1. **divisions = 480**（每四分音符的 tick 数），正好等于 `NoteBlock.Ticks` 的口径 ⇒ `<duration>` 直接就是 ticks。
//  2. **长音要拆成连音线**：MusicXML 的 `<type>` 只有 whole/half/quarter/…，一个 5 拍的音没法用一个 `<type>` 表达
//     ⇒ 拆成若干**带 `<tie>` 的音符**。反过来 `ParseMusicXML` 必须把 tie 链**合回一个块**，
//     否则"音符块 → MusicXML → 音符块"会一块变多块（往返不变量会被自己破掉）。
//  3. **`<type>` 可省**：MusicXML 里 `<type>` 是可选的。碰上连音线也表达不了的时值（如三连音 160 tick，
//     或 `.`/`_` 之外的怪值），就**省掉 `<type>` 只给 `<duration>`** —— 这是合法 MusicXML，
//     而且比编一个假的 `<type>` 诚实。

// MusicXMLOptions 控制音符块 → MusicXML。
type MusicXMLOptions struct {
	Title    string
	Composer string
	// TonicPitchClass 主音音级 0..11（决定调号 `<fifths>`）；<0 则不写调号。
	TonicPitchClass int
	// PreferFlat 决定升降号的调号选哪个（C#/Db、F#/Gb 这类两可的）。
	PreferFlat bool
	// Divisions 每四分音符的 tick 数；0 ⇒ 480（与 NoteBlock.Ticks 口径一致）。
	Divisions int64
	// Clef "G2"（高音）/ "F4"（低音）/ "" ⇒ 按音域自动挑。
	Clef string
	// PartName 声部名（默认 "Voice"）。
	PartName string
	// Meter 拍号（决定小节怎么分）。
	Meter Meter
	// ForceSharp 强制调号走**升号侧**。为什么要它：**用户那套记法的调式表全是升号**
	//（`1=#C`/`1=#F`/`1=#G`/`1=#A`），从它转过来时就该写 C#（7 个升号）而不是 Db（5 个降号）——
	// 光靠 `PreferFlat=false` 表达不了这个意图（那只是"不偏好降号"，仍会按调性习惯选 Db）。
	ForceSharp bool
}

// fifthsOf 把主音音级换成 MusicXML 的 `<fifths>`（大调）。
//
// ⚠️ 只有 **pc=1 与 pc=6 是两可的**（C#≡Db、F#≡Gb）：
// `forceSharp` 优先走升号侧；否则按降号偏好（`flatPitchClasses`）。
// 为什么要 `forceSharp`：**用户那套记法的调式表全是升号**（`1=#C`、`1=#F`、`1=#G`、`1=#A`），
// 从它转过来时就该写 C#（7 个升号）而不是 Db（5 个降号）。
//
// ⛔ 但 `forceSharp` **只该管那两档**。曾经的实现是先判 `if !forceSharp { 查降号表 }`，
// 于是 `forceSharp=true` 时 F 大调（pc=5）在升号表里查不到、直接落到 `return 0`
// ⇒ **F 大调被写成 C 大调**（`<fifths>0</fifths>`），而音符还是按 F 大调拼 —— 自相矛盾。
// 现在按「确定档 / 两可档」分开写，不再靠查表顺序碰运气。
func fifthsOf(pc int, preferFlat, forceSharp bool) int {
	sharpOnly := map[int]int{0: 0, 7: 1, 2: 2, 9: 3, 4: 4, 11: 5} // C G D A E B
	flatOnly := map[int]int{5: -1, 10: -2, 3: -3, 8: -4}          // F Bb Eb Ab
	twoWaySharp := map[int]int{1: 7, 6: 6}                        // C# F#
	twoWayFlat := map[int]int{1: -5, 6: -6}                       // Db Gb
	if v, ok := sharpOnly[pc]; ok {
		return v
	}
	if v, ok := flatOnly[pc]; ok {
		return v
	}
	if v, ok := twoWaySharp[pc]; ok {
		if !forceSharp && (preferFlat || flatPitchClasses[pc]) {
			return twoWayFlat[pc]
		}
		return v
	}
	return 0
}

// --- MusicXML 的时值表示 ---

// mxlBase 是 MusicXML 的基础时值（以四分音符 = 1 拍计）。
var mxlBase = []struct {
	name  string
	beats float64
}{
	{"whole", 4}, {"half", 2}, {"quarter", 1}, {"eighth", 0.5},
	{"16th", 0.25}, {"32nd", 0.125}, {"64th", 0.0625},
}

// mxlPiece 是一个"能用一个 <type>+附点 表达"的片断。
type mxlPiece struct {
	Ticks int64
	Type  string // 空 = 省掉 <type>（只给 duration）
	Dots  int
}

// decomposeTicks 把一个时值拆成若干片（贪心从大到小），每片能用一个 `<type>`+附点 表达。
//
// 拆出来的片要**用连音线连起来**（`WriteMusicXML` 会加 `<tie>`）——
// 这是 MusicXML 表达"超过一个 whole 的音"或"whole + 附点都不够用"的标准做法。
func decomposeTicks(ticks int64, divisions int64) []mxlPiece {
	if ticks <= 0 {
		return []mxlPiece{{Ticks: 0, Type: "quarter"}}
	}
	var out []mxlPiece
	rem := ticks
	// 先按"最大可表达值"贪心；每轮取不超过 rem 的最大 (base × 附点)
	for rem > 0 {
		best := mxlPiece{Ticks: -1}
		for _, b := range mxlBase {
			baseTicks := int64(b.beats * float64(divisions))
			for dots := 0; dots <= 3; dots++ {
				mult := 2 - math.Pow(2, -float64(dots))
				t := int64(math.Round(float64(baseTicks) * mult))
				if t <= rem && t > best.Ticks {
					best = mxlPiece{Ticks: t, Type: b.name, Dots: dots}
				}
			}
		}
		if best.Ticks <= 0 {
			// 剩下的**连音线也表达不了**（三连音那种）⇒ 整块给出、省掉 <type>
			out = append(out, mxlPiece{Ticks: rem, Type: ""})
			break
		}
		out = append(out, best)
		rem -= best.Ticks
	}
	return out
}

// --- 写 ---

func xmlEsc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// WriteMusicXML 把音符块写成 MusicXML（score-partwise）。
//
// 按 `Voice` 分 `<part>`（一个声部一个 part，ACE `import file` 就是一条轨一个 part）。
// 小节按拍号累计拍数切。
func WriteMusicXML(blocks []NoteBlock, opt MusicXMLOptions) (string, []Diagnostic) {
	var diags []Diagnostic
	if opt.Divisions <= 0 {
		opt.Divisions = 480
	}
	if opt.Meter.Numerator == 0 {
		opt.Meter = Meter{Numerator: 4, Denominator: 4}
	}
	if opt.PartName == "" {
		opt.PartName = "Voice"
	}

	// 按声部归堆（0 也当 1；保持出现顺序）
	voiceOrder := []int{}
	byVoice := map[int][]NoteBlock{}
	for _, b := range blocks {
		v := b.Voice
		if v <= 0 {
			v = 1
		}
		if _, ok := byVoice[v]; !ok {
			voiceOrder = append(voiceOrder, v)
		}
		byVoice[v] = append(byVoice[v], b)
	}
	if len(voiceOrder) == 0 {
		voiceOrder = []int{1}
	}

	// 谱号：按音域自动挑（低于 B3 用低音谱号）
	clef := opt.Clef
	if clef == "" {
		sum, n := 0, 0
		for _, b := range blocks {
			if b.Midi >= 0 {
				sum += b.Midi
				n++
			}
		}
		if n > 0 && sum/n < 57 {
			clef = "F4"
		} else {
			clef = "G2"
		}
	}

	beatsPerMeasure := 4.0 * float64(opt.Meter.Numerator) / float64(opt.Meter.Denominator)
	if beatsPerMeasure <= 0 {
		beatsPerMeasure = 4
	}

	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	sb.WriteString(`<score-partwise version="4.0">` + "\n")
	if opt.Title != "" {
		fmt.Fprintf(&sb, "  <work><work-title>%s</work-title></work>\n", xmlEsc(opt.Title))
	}
	if opt.Composer != "" {
		fmt.Fprintf(&sb, "  <identification><creator type=\"composer\">%s</creator></identification>\n", xmlEsc(opt.Composer))
	}
	sb.WriteString("  <part-list>\n")
	for i, v := range voiceOrder {
		name := opt.PartName
		if len(voiceOrder) > 1 {
			name = fmt.Sprintf("%s %d", opt.PartName, v)
		}
		fmt.Fprintf(&sb, "    <score-part id=\"P%d\"><part-name>%s</part-name></score-part>\n", i+1, xmlEsc(name))
	}
	sb.WriteString("  </part-list>\n")

	for pi, v := range voiceOrder {
		fmt.Fprintf(&sb, "  <part id=\"P%d\">\n", pi+1)
		measureNo := 1
		acc := 0.0
		fmt.Fprintf(&sb, "    <measure number=\"%d\">\n", measureNo)
		// 第一小节放 attributes
		fmt.Fprintf(&sb, "      <attributes><divisions>%d</divisions>", opt.Divisions)
		if opt.TonicPitchClass >= 0 {
			fmt.Fprintf(&sb, "<key><fifths>%d</fifths></key>", fifthsOf(opt.TonicPitchClass, opt.PreferFlat, opt.ForceSharp))
		}
		fmt.Fprintf(&sb, "<time><beats>%d</beats><beat-type>%d</beat-type></time>", opt.Meter.Numerator, opt.Meter.Denominator)
		clefSign, clefLine := "G", 2
		if strings.HasPrefix(clef, "F") {
			clefSign, clefLine = "F", 4
		}
		if len(clef) == 2 && clef[1] >= '1' && clef[1] <= '5' {
			clefLine = int(clef[1] - '0')
		}
		fmt.Fprintf(&sb, "<clef><sign>%s</sign><line>%d</line></clef></attributes>\n", clefSign, clefLine)

		for _, b := range byVoice[v] {
			ticks := b.Ticks
			if ticks <= 0 {
				ticks = int64(math.Round(b.Beats * float64(opt.Divisions)))
			}
			// ⛔ 同 notation.go：判休止只能看 Midi < 0（ACE 来源的块不设 Degree）
			isRest := b.Midi < 0
			hidden := b.Hidden && b.Midi >= 0
			pieces := decomposeTicks(ticks, opt.Divisions)
			if len(pieces) > 1 {
				diags = append(diags, Diagnostic{Code: "tied-long-note",
					Message: fmt.Sprintf("%g 拍超过单个 `<type>` 能表达的值 ⇒ 拆成 %d 个带 `<tie>` 的音符（MusicXML 的标准做法）",
						b.Beats, len(pieces))})
			}
			for k, p := range pieces {
				// ⚠️ 这里**不能写成 `<note>`**：属性要拼在 `>` 之前。
				// 写成 `<note>` 再拼 ` print-object="no"` 会得到 `<note> print-object="no">` ——
				// 属性变成了**文本内容**，XML 仍然"合法"（Go 的解码器不报错，只是读不到属性），
				// 于是"隐藏音"静默退化成普通音。**是测试逮住这个的。**
				sb.WriteString("      <note")
				if hidden {
					sb.WriteString(` print-object="no"`)
				}
				sb.WriteString(">\n")
				if isRest {
					sb.WriteString("        <rest/>\n")
				} else {
					step, alter, octave := spellMidi(b, opt)
					sb.WriteString("        <pitch>")
					fmt.Fprintf(&sb, "<step>%s</step>", step)
					if alter != 0 {
						fmt.Fprintf(&sb, "<alter>%d</alter>", alter)
					}
					fmt.Fprintf(&sb, "<octave>%d</octave>", octave)
					sb.WriteString("</pitch>\n")
				}
				fmt.Fprintf(&sb, "        <duration>%d</duration>\n", p.Ticks)
				if len(pieces) > 1 {
					// tie：第一片只 start、最后一片只 stop、中间两头顶
					if k > 0 {
						sb.WriteString(`        <tie type="stop"/>` + "\n")
					}
					if k < len(pieces)-1 {
						sb.WriteString(`        <tie type="start"/>` + "\n")
					}
				}
				fmt.Fprintf(&sb, "        <voice>%d</voice>\n", v)
				if p.Type != "" {
					fmt.Fprintf(&sb, "        <type>%s</type>\n", p.Type)
				}
				for d := 0; d < p.Dots; d++ {
					sb.WriteString("        <dot/>\n")
				}
				// ⚠️ `<accidental>` 按 DTD 要排在 `<type>`/`<dot>` **之后**（不是跟着 `<pitch>`）
				if !isRest {
					_, alter, _ := midiToStep(b.Midi)
					switch {
					case b.Accidental == AccidentalSharp || (b.Accidental == AccidentalNone && alter > 0):
						sb.WriteString("        <accidental>sharp</accidental>\n")
					case b.Accidental == AccidentalFlat || (b.Accidental == AccidentalNone && alter < 0):
						sb.WriteString("        <accidental>flat</accidental>\n")
					}
				}
				if len(pieces) > 1 {
					sb.WriteString("        <notations>")
					if k > 0 {
						sb.WriteString(`<tied type="stop"/>`)
					}
					if k < len(pieces)-1 {
						sb.WriteString(`<tied type="start"/>`)
					}
					sb.WriteString("</notations>\n")
				}
				// 歌词只挂在这一串连音线的**第一片**上
				if k == 0 && b.Lyric != "" && !isRest {
					fmt.Fprintf(&sb, "        <lyric number=\"1\"><syllabic>single</syllabic><text>%s</text></lyric>\n", xmlEsc(b.Lyric))
				}
				sb.WriteString("      </note>\n")
			}

			acc += b.Beats
			if acc >= beatsPerMeasure-1e-9 {
				sb.WriteString("    </measure>\n")
				measureNo++
				fmt.Fprintf(&sb, "    <measure number=\"%d\">\n", measureNo)
				acc = 0
			}
		}
		// 收尾：补一根终止线
		if acc > 0 || measureNo == 1 {
			sb.WriteString(`      <barline location="right"><bar-style>light-heavy</bar-style></barline>` + "\n")
		}
		sb.WriteString("    </measure>\n")
		sb.WriteString("  </part>\n")
	}
	sb.WriteString("</score-partwise>\n")
	return sb.String(), diags
}

// stepSemitone 是 MusicXML 的 step → 半音（C=0）。
var stepSemitone = map[string]int{"C": 0, "D": 2, "E": 4, "F": 5, "G": 7, "A": 9, "B": 11}

// midiToStep 把 MIDI 拆成 (step, alter, octave)。**一律升号拼法** —— 只在没有调性信息时用。
func midiToStep(midi int) (string, int, int) {
	names := []struct {
		step  string
		alter int
	}{
		{"C", 0}, {"C", 1}, {"D", 0}, {"D", 1}, {"E", 0}, {"F", 0},
		{"F", 1}, {"G", 0}, {"G", 1}, {"A", 0}, {"A", 1}, {"B", 0},
	}
	if midi < 0 {
		midi = 0
	}
	octave := midi/12 - 1
	pc := ((midi % 12) + 12) % 12
	n := names[pc]
	return n.step, n.alter, octave
}

// stepNames 是七个字母（正史顺序，C 起）。
var stepNames = [7]string{"C", "D", "E", "F", "G", "A", "B"}

// tonicLetterOf 由调号的 `<fifths>` 反推主音字母（0=C … 6=B）。
//
// 五度圈的规律就是这一行：`fifths` 每加 1，主音上行五度（字母 +4 mod 7）——
//
//	f=0 C · 1 G · 2 D · 3 A · 4 E · 5 B · 6 F# · 7 C#；负数侧同样成立：-1 F · -2 Bb · -3 Eb…
//
// 这样**黑键主音（C#/Db）也能落到对的那个字母上**，不用再维护一张只认白键的表。
func tonicLetterOf(fifths int) int {
	return ((4*fifths)%7 + 7) % 7
}

// spellMidi 决定一个音符块在五线谱上**怎么拼**（letter + alter + 八度）。
//
// 两条判据，优先第一条：
//
//	① **元素自带的音级**（`Degree` 有值 ⇒ 这块是从简谱元素折过来的）：
//	   按「主音字母 + 音级 - 1」定字母 —— 这就是**记谱意图**。`F 大调里的 #4` 因此写成 B♮，
//	   而不会因为我们自己的拼法偏好变成 C♭；`C 大调的 2b` 写成 D♭，不会变成 C#。
//	② 没有音级（ACE 来源的块）⇒ 按**调号**推（降号调偏降、升号调偏升）。
//
// ⛔ 曾经的实现是**一律写升号**（`midiToStep`）：那在 F 大调里把 B♭ 写成 A#，
// 与同一份谱里的 `<fifths>-1</fifths>` **自相矛盾** —— 音高对、记谱错，读谱的人会看懵。
func spellMidi(b NoteBlock, opt MusicXMLOptions) (string, int, int) {
	fifths := 0
	if opt.TonicPitchClass >= 0 {
		fifths = fifthsOf(opt.TonicPitchClass, opt.PreferFlat, opt.ForceSharp)
	}
	letter := 4 // 没调性信息时按 C 大调（= 主音 C）
	if opt.TonicPitchClass >= 0 {
		letter = tonicLetterOf(fifths)
	}
	// C4 = 字母步 28（而 E4 = 30 是五线谱的底线，两者差 2 —— 与渲染那边同一套口径）
	degree, octave := 0, 0
	if b.Degree >= 1 && b.Degree <= 7 {
		degree, octave = b.Degree, b.Octave
	} else if opt.TonicPitchClass >= 0 {
		d, o, _ := pitchToDegree(b.Midi, opt.TonicPitchClass, 60, fifths < 0)
		degree, octave = d, o
	} else {
		return midiToStep(b.Midi)
	}
	idx := 28 + letter + (degree - 1) + 7*octave
	l := ((idx % 7) + 7) % 7
	oct := idx / 7
	if idx < 0 && idx%7 != 0 {
		oct-- // Go 的整数除法向零取整；字母步要用**向下取整**才是科学音高记号法
	}
	natural := 12*(oct+1) + stepSemitone[stepNames[l]]
	return stepNames[l], b.Midi - natural, oct
}

// --- 读 ---

// mxlPitch / mxlNote 用具名类型（**不用匿名结构体**：`tieInfo` 要收它，
// 而匿名结构体的类型恒等要求字段与标签逐字相同，没法维护）。
type mxlPitch struct {
	Step   string `xml:"step"`
	Alter  int    `xml:"alter"`
	Octave int    `xml:"octave"`
}

type mxlNote struct {
	PrintObject string     `xml:"print-object,attr"`
	Rest        *struct{}  `xml:"rest"`
	Chord       *struct{}  `xml:"chord"`
	Pitch       *mxlPitch  `xml:"pitch"`
	Duration    int        `xml:"duration"`
	Type        string     `xml:"type"`
	Dots        []struct{} `xml:"dot"`
	Ties        []struct {
		Type string `xml:"type,attr"`
	} `xml:"notations>tied"`
	Voice string `xml:"voice"`
	Lyric []struct {
		Text string `xml:"text"`
	} `xml:"lyric"`
}

type mxlMeasure struct {
	Number string `xml:"number,attr"`
	Attrs  *struct {
		Divisions int  `xml:"divisions"`
		Fifths    *int `xml:"key>fifths"`
		Time      *struct {
			Beats    int `xml:"beats"`
			BeatType int `xml:"beat-type"`
		} `xml:"time"`
		Clef *struct {
			Sign string `xml:"sign"`
			Line int    `xml:"line"`
		} `xml:"clef"`
	} `xml:"attributes"`
	Notes []mxlNote `xml:"note"`
}

type mxlDoc struct {
	Parts []struct {
		ID       string       `xml:"id,attr"`
		Measures []mxlMeasure `xml:"measure"`
	} `xml:"part"`
}

// tieInfo 判断一个音符的连音线角色（`<notations><tied>` 那一路）。
func tieInfo(n mxlNote) (start, stop bool) {
	for _, t := range n.Ties {
		if t.Type == "start" {
			start = true
		}
		if t.Type == "stop" {
			stop = true
		}
	}
	return
}

// ParseMusicXML 读 MusicXML → 音符块。
//
// 覆盖我们自己写出来的那部分（`score-partwise`、`attributes`、`note`/`rest`/`pitch`/`duration`/`type`/`dot`/`tie`/`lyric`），
// 外加一点容错（外部文件常见写法）。读不动的地方**报诊断**，不静默丢。
//
// ⚠️ **tie 链要合回一个块** —— 否则"音符块 → MusicXML → 音符块"会一块变多块，往返不变量就自己破了。
func ParseMusicXML(data string) ([]NoteBlock, []Diagnostic) {
	var diags []Diagnostic
	var doc mxlDoc
	if err := xml.Unmarshal([]byte(data), &doc); err != nil {
		return nil, []Diagnostic{{Code: "musicxml-parse-error",
			Message: "MusicXML 解析失败：" + err.Error()}}
	}
	if len(doc.Parts) == 0 {
		return nil, []Diagnostic{{Code: "musicxml-no-part",
			Message: "这份 MusicXML 里没有 `<part>`（只支持 score-partwise）"}}
	}

	var out []NoteBlock
	divisions := 480
	meter := Meter{Numerator: 4, Denominator: 4}
	if a := doc.Parts[0].Measures; len(a) > 0 && a[0].Attrs != nil {
		if a[0].Attrs.Divisions > 0 {
			divisions = a[0].Attrs.Divisions
		}
		if t := a[0].Attrs.Time; t != nil && t.Beats > 0 && t.BeatType > 0 {
			meter = Meter{Numerator: t.Beats, Denominator: t.BeatType}
		}
	}

	for pi, part := range doc.Parts {
		voice := pi + 1
		measureNo := 0
		beatInMeasure := 1.0
		for _, m := range part.Measures {
			measureNo++
			if m.Attrs != nil {
				if m.Attrs.Divisions > 0 {
					divisions = m.Attrs.Divisions
				}
				if t := m.Attrs.Time; t != nil && t.Beats > 0 && t.BeatType > 0 {
					meter = Meter{Numerator: t.Beats, Denominator: t.BeatType}
				}
			}
			beatInMeasure = 1.0
			for _, n := range m.Notes {
				if n.Chord != nil {
					diags = append(diags, Diagnostic{Code: "musicxml-chord-ignored",
						Message: "这份 MusicXML 里有和弦（`<chord/>`）—— 音符块是单声部的，和弦音被忽略（ACE 那边也是拆成多条轨）"})
				}
				beats := 0.0
				if divisions > 0 {
					beats = float64(n.Duration) / float64(divisions)
				}
				start, stop := tieInfo(n)

				isRest := n.Rest != nil || n.Pitch == nil
				var midi = -1
				if n.Pitch != nil {
					midi = (n.Pitch.Octave+1)*12 + stepSemitone[n.Pitch.Step] + n.Pitch.Alter
				}
				lyric := ""
				if len(n.Lyric) > 0 {
					lyric = n.Lyric[0].Text
				}

				if stop && len(out) > 0 && out[len(out)-1].Voice == voice {
					// 连音线的续片 ⇒ 合回前一个块
					last := &out[len(out)-1]
					last.Beats += beats
					last.Ticks += int64(math.Round(beats * float64(divisions)))
					beatInMeasure += beats
					// ⚠️ 续片若带歌词（不该有，但外部文件可能有），保留第一个的
					continue
				}

				blk := NoteBlock{
					Voice:        voice,
					Midi:         midi,
					Beats:        beats,
					Ticks:        int64(math.Round(beats * float64(divisions))),
					Lyric:        lyric,
					Measure:      measureNo,
					Beat:         beatInMeasure,
					Hidden:       isRest || n.PrintObject == "no",
					ElementIndex: len(out),
				}
				if isRest {
					blk.Degree = 0
					blk.Midi = -1
					blk.Hidden = true
				}
				if n.PrintObject == "no" && !isRest {
					blk.Hidden = true
				}
				// 由音高反推度数（拿不到调式就先留 0；`Blocks` 那侧才算得准）
				if accord := n.Pitch; accord != nil {
					blk.Accidental = AccidentalNone
					if accord.Alter > 0 {
						blk.Accidental = AccidentalSharp
					} else if accord.Alter < 0 {
						blk.Accidental = AccidentalFlat
					}
				}
				out = append(out, blk)
				beatInMeasure += beats
				_ = start
			}
		}
	}
	_ = meter
	return out, diags
}
