package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"acetool/internal/jianpu"
)

// 「编辑器文档」（`[]jianpu.Element`：音符 / `-` 延音 / 小节线）与两边之间的转换。
//
// ⭐ 为什么它是中心：用户 2026-10-05 定了「**不用文本记法了，直接 web 编辑**」+
// 「**连音是 ACE 的 `-` 音符**」⇒ 编辑器文档与 ACE 的表示**本来就是同一件事**：
//
//	ACE：一个音节 = 头音符（带歌词）+ 若干 **tenuto 音符**（歌词字段写字面 `-`）
//	文档：一个音节 = `Element{kind:note}` + 若干 `Element{kind:sustain}`
//
// ⇒ **ACE ⇄ 编辑器是完美往返**（`notesToElements` / `elementsToJpElems` 互为逆），
// 而文本记法（简洁/番茄）降级成两个可选入口。MusicXML 那侧仍然**折叠**（五线谱上简谱的 `1 -` 本来就等于更长的音）。

// ---------- 空拍 = 休止（用户 2026-10-07 的质问：「空拍不是休止吗」） ----------
//
// ⭐ 先把口径说清（这也是界面上要写的）：
//
//	ACE 里**没有"休止符对象"** —— 一段没有音符的时间**就是**休止（空拍）。
//	我们手里的**编辑器文档**有显式的休止元素（面板里一格 `0`，`kind:"rest"`）⇒
//	  · 写进 ACE：休止**跳过但占位置**（`cursor += ticks`）⇒ **音符的时刻不变**；
//	  · 从 ACE 读回：以前**只按音符还原**，于是"两个音之间的空拍"在文档里凭空消失、
//	    谱面上后面的音看着往前挤（其实 ACE 里一点没动）。**这就是用户看到的那句提示**。
//
// ⇒ 现在读回来时**按间隙补出休止**（只在**两个音之间**补，见下面为什么不管头尾）。
//
// ⚠️ 为什么**不补**开头/结尾的空：
//
//	· 开头那段（第一个音之前）补出来会让"生成"时所有音**整体往后挪**（写的时候休止是占位置的），
//	  而用户定的口径是「从播放头所在小节开始生成」——第一条音就该落在那一小节线上；
//	· 结尾那段有多长根本不知道（ACE 不会告诉你"静音到哪"），凭空造一个休止就是编。
var restChunks = func() []restChunk {
	var out []restChunk
	// 只收**能被 TicksToDuration 原样认回来**的块 —— 免得这张表跟换算器各说各话
	for _, d := range []int{1, 2, 4, 8, 16, 32, 64} {
		for _, dots := range []int{0, 1} {
			tk := jianpu.DurationTicks(d, dots, 480)
			if tk <= 0 {
				continue
			}
			if rd, rdots, exact := jianpu.TicksToDuration(tk, 480); exact && rd == d && rdots == dots {
				out = append(out, restChunk{Ticks: tk, Duration: d, Dots: dots})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ticks > out[j].Ticks })
	return out
}()

type restChunk struct {
	Ticks    int64
	Duration int
	Dots     int
}

// restsForGap 把一段"没有音符的时间"切成若干**简谱写得出来**的休止元素。
//
// 返回第二个值是**没能精确表示的 tick 数**（余数比最小的块 30 tick 还小）——
// 如实报给调用方，不假装精确（与音符时值折不进简谱口径时同一条纪律）。
func restsForGap(ticks int64) ([]jianpu.Element, int64) {
	if ticks <= 0 {
		return nil, 0
	}
	var out []jianpu.Element
	rem := ticks
	for rem > 0 {
		picked := false
		for _, c := range restChunks {
			if c.Ticks <= rem {
				out = append(out, jianpu.Element{
					Kind: jianpu.KindRest, Rest: true, Pitch: 0,
					Duration: c.Duration, Dots: c.Dots,
				})
				rem -= c.Ticks
				picked = true
				break
			}
		}
		if !picked {
			break // 余数 < 30 tick：宁可丢掉并报数，也不去编一个表示不出来的时值
		}
	}
	return out, rem
}

// notesToElements 把 ACE 的音符折成**编辑器文档**：
//
//	歌词是 `-` 的音符  → `Element{Kind: KindSustain}`（tenuto 音符，独立元素）
//	其余音符           → `Element{Kind: KindNote}`（音级/八度/变音按时值+调式换算）
//
// ⚠️ 时值要经 `TicksToDuration` 折成简谱口径（几分音符 + 附点）；**折不出来就报诊断**，
// 不假装写对了（420 tick = 0.875 拍那种就落不进这个族）。
func notesToElements(notes []xcopyNote, key int) ([]jianpu.Element, []string) {
	var out []jianpu.Element
	var diags []string
	inexact := 0
	sorted := append([]xcopyNote(nil), notes...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Pos < sorted[j].Pos })
	cursor := int64(0) // 已经铺到哪个 tick（用来发现"两个音之间的空拍"）
	var gapLost int64  // 补出来的休止里没能精确表示的 tick 数
	gaps := 0          // 补了几段空拍
	for _, n := range sorted {
		// ⭐ 空拍 = 休止：两个音之间有间隙就补出休止元素（头尾不补，理由见 restsForGap 上面那段）
		if cursor > 0 && n.Pos > cursor {
			els, lost := restsForGap(n.Pos - cursor)
			out = append(out, els...)
			if len(els) > 0 {
				gaps++
			}
			gapLost += lost
		}
		d, dots, exact := jianpu.TicksToDuration(n.Dur, 480)
		if !exact {
			inexact++
		}
		if strings.TrimSpace(n.Lyric) == "-" {
			// tenuto 音符 ⇒ 独立元素（**这就是 ACE 表示连音的方式**）
			// ⚠️ 这里**不填 Lyric** —— 「延音」由 Kind 表达，填 `-` 会与"真的唱一个字面减号"混淆。
			out = append(out, jianpu.Element{Kind: jianpu.KindSustain, Duration: d, Dots: dots, Code: n.NoteUUID})
		} else {
			deg, oct, acc := jianpu.MidiToDegree(n.Pitch, key, 60)
			out = append(out, jianpu.Element{
				Kind: jianpu.KindNote, Pitch: deg, Octave: oct, Accidental: acc,
				Duration: d, Dots: dots, Code: n.NoteUUID, Lyric: n.Lyric,
			})
		}
		if end := n.Pos + n.Dur; end > cursor {
			cursor = end
		}
	}
	if inexact > 0 {
		diags = append(diags, fmt.Sprintf(
			"有 **%d** 个音符的时值折不进简谱口径（简谱只有「几分音符 + 附点」那几档，"+
				"如 420 tick = 0.875 拍就落不进去）⇒ 已退到最近的可表示值，**位置/时长会略有出入**，请核对", inexact))
	}
	if gaps > 0 {
		diags = append(diags, fmt.Sprintf(
			"两个音之间的**空拍**（ACE 里就是休止）已按间隙还原成 **%d** 段休止（简谱里一格 `0`）", gaps))
	}
	if gapLost > 0 {
		diags = append(diags, fmt.Sprintf(
			"有 **%d** 个 tick 的空拍小于简谱最小的休止块（64 分音符 = 30 tick）⇒ 没能表示出来，"+
				"**后面元素的位置会往前差这一点**（ACE 里没动），要精确就用【元素 JSON】存档", gapLost))
	}
	return out, diags
}

// elementsToBlocks 把编辑器文档**折成音符块**（`-` 并入前一个音的时值）—— 给 MusicXML / 简谱文本那两个出口用。
//
// 折叠在这里是**正确**的：五线谱上简谱的 `1 -` 本来就等于一个更长的音符。
// （与 ACE 那个出口相反 —— 那边要 tenuto 音符。两个出口各自正确，模型只保留一份。）
func elementsToBlocks(els []jianpu.Element, key int, meter jianpu.Meter) ([]jianpu.NoteBlock, []string) {
	var out []jianpu.NoteBlock
	var diags []string
	beatsPerMeasure := 4.0 * float64(meter.Numerator) / float64(meter.Denominator)
	if beatsPerMeasure <= 0 {
		beatsPerMeasure = 4
	}
	acc := 0.0
	orphan := 0
	for _, e := range els {
		if e.Kind == jianpu.KindBarline {
			// 小节线**不占时值**（它是记号，不是音）—— 当成休止会白白吃掉一拍
			continue
		}
		tk := jianpu.DurationTicks(e.Duration, e.Dots, 480)
		if e.Kind == jianpu.KindSustain {
			if len(out) == 0 {
				orphan++
				acc += float64(tk) / 480
				continue
			}
			last := &out[len(out)-1]
			last.Beats += float64(tk) / 480
			last.Ticks += tk
			acc += float64(tk) / 480
			continue
		}
		measure := int(acc/beatsPerMeasure) + 1
		b := jianpu.NoteBlock{
			Voice: 1, Beats: float64(tk) / 480, Ticks: tk,
			Measure: measure, Beat: acc - float64(measure-1)*beatsPerMeasure + 1,
			ElementIndex: len(out), Hidden: e.Rest, Lyric: e.Lyric,
		}
		if e.Rest || e.Pitch == 0 || e.Kind == jianpu.KindRest {
			b.Midi = -1
			b.Degree = 0
		} else if semi, ok := jianpu.DegreeSemitone(e.Pitch); ok {
			m := 60 + key + semi + 12*e.Octave
			switch e.Accidental {
			case jianpu.AccidentalSharp:
				m++
			case jianpu.AccidentalFlat:
				m--
			}
			b.Midi = m
			b.Degree = e.Pitch
			b.Octave = e.Octave
			b.Accidental = e.Accidental
		} else {
			b.Midi = -1
		}
		out = append(out, b)
		acc += float64(tk) / 480
	}
	if orphan > 0 {
		diags = append(diags, fmt.Sprintf("有 %d 个 `-` 前面没有音符可延长，已忽略", orphan))
	}
	return out, diags
}

// renderFromElements 是「编辑器文档 → 产物」那条路：**不读 ACE**，直接用发来的元素。
//
// 用途：编辑器里"导出 MusicXML"、"看简洁记法"这类操作 —— 文档可能还没写进 ACE，
// 或者与 ACE 里的内容已经不一样了，所以不能去读工程。
func renderFromElements(vals map[string]string) (*jianpuFromReport, error) {
	var els []jianpu.Element
	if err := json.Unmarshal([]byte(strings.TrimSpace(vals["elements"])), &els); err != nil {
		return nil, fmt.Errorf("`elements` 不是合法的元素 JSON：%w", err)
	}
	key := 0
	keyHow := "手动指定（编辑器带来的调式）"
	if kv := strings.TrimSpace(vals["key"]); kv != "" && kv != "auto" {
		n, err := strconvAtoiKey(kv)
		if err != nil {
			return nil, err
		}
		key = n
	}
	meter, err := parseMeter(value(vals, "meter", "4/4"))
	if err != nil {
		return nil, err
	}
	rep := &jianpuFromReport{
		Notation: value(vals, "format", "elements"), Scope: "编辑器文档（未读工程）",
		KeyHow: keyHow, Key: key, KeyName: jianpuKeys[key],
		Meter: fmt.Sprintf("%d/%d", meter.Numerator, meter.Denominator),
		Notes: len(els),
	}
	blocks, foldDiags := elementsToBlocks(els, key, meter)
	rep.Diagnostics = append(rep.Diagnostics, foldDiags...)

	switch rep.Notation {
	case "elements":
		b, _ := json.MarshalIndent(els, "", "  ")
		rep.Output = string(b)
		rep.Elements = els
	case "blocks":
		b, _ := json.MarshalIndent(blocks, "", "  ")
		rep.Output = string(b)
	case "musicxml":
		out, diags := jianpu.WriteMusicXML(blocks, jianpu.MusicXMLOptions{
			TonicPitchClass: key, Meter: meter, ForceSharp: true,
			Title: value(vals, "title", ""), Composer: value(vals, "composer", ""),
			PartName: "Melody",
		})
		rep.Output = out
		lines, total := capDiags(diags)
		rep.Diagnostics = append(rep.Diagnostics, lines...)
		rep.DiagnosticsTotal = total
	default:
		out, diags := jianpu.WriteNotation(blocks, jianpu.NotationOptions{KeyIndex: key, Meter: meter})
		rep.Output = out
		lines, total := capDiags(diags)
		rep.Diagnostics = append(rep.Diagnostics, lines...)
		rep.DiagnosticsTotal = total
	}
	rep.Note = fmt.Sprintf("编辑器文档：%d 个元素（折成 %d 个音符块）。调式 **%s**。%s",
		len(els), len(blocks), rep.KeyName,
		map[string]string{
			"musicxml": "⭐ 这份 MusicXML 可以存成 `.musicxml` 交打谱软件，也能喂 ACE（`import file` 收 MusicXML）。",
			"elements": "原样回吐元素序列（调试用）。",
		}[rep.Notation])
	return rep, nil
}

// strconvAtoiKey 读调式档位（0..11）。
func strconvAtoiKey(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("调式只能是 0..11（C..B），收到 %q", s)
		}
		n = n*10 + int(c-'0')
	}
	if n > 11 {
		return 0, fmt.Errorf("调式只能是 0..11（C..B），收到 %q", s)
	}
	return n, nil
}
