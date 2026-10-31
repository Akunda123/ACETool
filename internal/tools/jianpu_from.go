package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"acetool/internal/jianpu"
)

// `jianpu.fromClip` —— 「音符块 → 谱面」那半：把 ACE 里的音符读成**简谱文本**或 **MusicXML**。
//
// ⭐ 自动检测调式**复用** `detectKey()`（我给「和声」移植时从 `和声.js` 的 `Mode()` 直译过来的：
// 12 个旋转里挑"落在黑键上最少"的那个）—— 一套算法两处用，不重写。
// ⚠️ 它**会并列**（实测那台工程的旋律最低分 2 分、6 个旋转并列）⇒ 照「和声」那支的做法，
// 把并列情况**如实报出来**，并给出手动调式的入口。

const jianpuDiagCap = 25

type jianpuFromReport struct {
	Notation string `json:"notation"` // terse / musicxml / blocks
	Scope    string `json:"scope"`

	Clip     string `json:"clip,omitempty"`
	ClipType string `json:"clipType,omitempty"`
	Track    int    `json:"trackIndex"`

	Notes int `json:"notes"`

	KeyHow      string `json:"keyFrom"`
	Key         int    `json:"key"`
	KeyName     string `json:"keyName"`
	AutoKey     int    `json:"autoDetectedKey"`
	AutoKeyName string `json:"autoDetectedKeyName"`
	KeyTies     []int  `json:"keyTiedRotations,omitempty"`

	Meter string `json:"meter"`

	// 产物（文本）
	Output string `json:"output,omitempty"`
	// 编辑器文档（`format=elements` 时**顺带结构化返回一份**）——
	// 让面板不必去 `JSON.parse(output)` 那个字符串（两处解析两处可能出错）。
	Elements []jianpu.Element `json:"elements,omitempty"`
	// 诊断（截断到 jianpuDiagCap 条，并在下面说明总数）
	Diagnostics      []string `json:"diagnostics,omitempty"`
	DiagnosticsTotal int      `json:"diagnosticsTotal,omitempty"`

	Note string `json:"note,omitempty"`
}

// notesToBlocks 把 ACE 的音符（`clip note-content` 那套）折成音符块。
//
// ⚠️ ACE 的 `dur` 是 tick、`pos` 是 **clip-local**；音符块要的是「拍 + tick」，
// 480 PPQ ⇒ `Beats = Dur/480`。小节/拍位按**累计**算（ACE 的音符没有小节号这个概念）。
func notesToBlocks(notes []xcopyNote, meter jianpu.Meter) []jianpu.NoteBlock {
	beatsPerMeasure := 4.0 * float64(meter.Numerator) / float64(meter.Denominator)
	if beatsPerMeasure <= 0 {
		beatsPerMeasure = 4
	}
	sorted := append([]xcopyNote(nil), notes...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Pos < sorted[j].Pos })

	out := make([]jianpu.NoteBlock, 0, len(sorted))
	acc := 0.0
	cursor := int64(0)
	for i, n := range sorted {
		// ⭐ 空拍 = 休止（同 `notesToElements`）：两个音之间的间隙补出**休止块**
		//（`Midi < 0` 就是休止，`jianpu.WriteMusicXML` 会写成 `<rest/>`），否则导出的谱面上
		// 那两个音会挨在一起、小节号也跟着算错。
		if cursor > 0 && n.Pos > cursor {
			for _, r := range gapRestBlocks(n.Pos-cursor, beatsPerMeasure) {
				measure := int(acc/beatsPerMeasure) + 1
				beat := acc - float64(measure-1)*beatsPerMeasure + 1
				r.Measure, r.Beat, r.ElementIndex = measure, beat, len(out)
				out = append(out, r)
				acc += r.Beats
			}
		}
		beats := float64(n.Dur) / 480.0
		measure := int(acc/beatsPerMeasure) + 1
		beat := acc - float64(measure-1)*beatsPerMeasure + 1
		out = append(out, jianpu.NoteBlock{
			Voice: 1, Midi: n.Pitch, Beats: beats, Ticks: n.Dur,
			Lyric: n.Lyric, Measure: measure, Beat: beat,
			ElementIndex: i, Code: n.NoteUUID,
		})
		acc += beats
		if end := n.Pos + n.Dur; end > cursor {
			cursor = end
		}
	}
	return out
}

// gapRestBlocks 把一段空拍折成**休止块**（`Midi < 0`）—— 块的音符口径与 `notesToBlocks` 一致。
func gapRestBlocks(ticks int64, beatsPerMeasure float64) []jianpu.NoteBlock {
	els, _ := restsForGap(ticks) // 复用同一张块表（不与 elements 那条路漂移）
	out := make([]jianpu.NoteBlock, 0, len(els))
	for _, e := range els {
		tk := jianpu.DurationTicks(e.Duration, e.Dots, 480)
		out = append(out, jianpu.NoteBlock{
			Voice: 1, Midi: -1, Degree: 0, // ⚠️ 休止就是 `Midi < 0`（`NoteBlock` 没有 Rest 字段，
			//     `musicxml.go` 那边判的也是 `b.Midi < 0`）
			Beats: float64(tk) / 480.0, Ticks: tk,
		})
	}
	return out
}

func capDiags(ds []jianpu.Diagnostic) ([]string, int) {
	lines := diagLines(ds)
	total := len(lines)
	if len(lines) > jianpuDiagCap {
		lines = lines[:jianpuDiagCap]
		lines = append(lines, fmt.Sprintf("…另有 %d 条同类诊断（已省略）", total-jianpuDiagCap))
	}
	return lines, total
}

func analyzeFromClip(ctx context.Context, rd ReadFunc, vals map[string]string) (*jianpuFromReport, error) {
	// ---- 编辑器文档入口：给了 `elements` 就**不去读工程** ----
	// 编辑器里"导出 MusicXML / 看简洁记法"这类操作，文档可能还没写进 ACE、或者与 ACE 已经不一样了
	// ⇒ 必须按发来的文档算，读工程反而会用旧内容盖掉用户刚编辑的东西。
	if strings.TrimSpace(vals["elements"]) != "" {
		return renderFromElements(vals)
	}
	useSel := value(vals, "scope", "auto") != "clip"
	sc, err := readNoteScope(ctx, rd, useSel)
	if err != nil {
		return nil, err
	}
	if len(sc.Notes) == 0 {
		return nil, fmt.Errorf("片段「%s」里没有音符", sc.ClipName)
	}
	meter, err := parseMeter(value(vals, "meter", "4/4"))
	if err != nil {
		return nil, err
	}
	format := value(vals, "format", "terse")
	rep := &jianpuFromReport{
		Notation: format, Scope: sc.Which,
		Clip: sc.ClipName, ClipType: sc.ClipType, Track: sc.TrackIndex,
		Notes: len(sc.Notes), Meter: fmt.Sprintf("%d/%d", meter.Numerator, meter.Denominator),
	}

	blocks := notesToBlocks(sc.Notes, meter)

	// ---- 调式 ----
	pitches := make([]int, 0, len(blocks))
	for _, b := range blocks {
		if b.Midi >= 0 {
			pitches = append(pitches, b.Midi)
		}
	}
	autoKey, scores, ties := detectKey(pitches)
	key := autoKey
	rep.KeyHow = "自动检测（12 个旋转里挑落在黑键上最少的那个 —— 与「生成和声」同一套算法）"
	rep.AutoKey, rep.AutoKeyName = autoKey, jianpuKeys[autoKey]
	rep.KeyTies = ties
	if kv := strings.TrimSpace(vals["key"]); kv != "" && kv != "auto" {
		n, err := strconv.Atoi(kv)
		if err != nil || n < 0 || n > 11 {
			return nil, fmt.Errorf("调式只能是 auto 或 0..11，收到 %q", kv)
		}
		key, rep.KeyHow = n, "手动指定"
	}
	rep.Key, rep.KeyName = key, jianpuKeys[key]

	// ---- 出产物 ----
	switch format {
	case "blocks":
		b, _ := json.MarshalIndent(blocks, "", "  ")
		rep.Output = string(b)
	case "elements":
		// ⭐ 编辑器文档：头音符 + `-` 延音**各自独立成一个元素**（与 ACE 的 tenuto 一一对应）
		els, diags := notesToElements(sc.Notes, key)
		b, _ := json.MarshalIndent(els, "", "  ")
		rep.Output = string(b)
		rep.Elements = els
		rep.Diagnostics = append(rep.Diagnostics, diags...)
	case "musicxml":
		clef := ""
		xmlOpt := jianpu.MusicXMLOptions{
			TonicPitchClass: key, Meter: meter, Clef: clef,
			Title:    value(vals, "title", ""),
			Composer: value(vals, "composer", ""),
			PartName: sc.ClipName,
			// 用户那套记法的调式表**全是升号** ⇒ 两可的调号强制走升号侧
			ForceSharp: true,
		}
		out, diags := jianpu.WriteMusicXML(blocks, xmlOpt)
		rep.Output = out
		var total int
		rep.Diagnostics, total = capDiags(diags)
		rep.DiagnosticsTotal = total
	default: // terse
		out, diags := jianpu.WriteNotation(blocks, jianpu.NotationOptions{
			KeyIndex: key, BaseMidi: 60, Meter: meter,
		})
		rep.Output = out
		var total int
		rep.Diagnostics, total = capDiags(diags)
		rep.DiagnosticsTotal = total
	}

	// ---- 收尾说明 ----
	var b strings.Builder
	fmt.Fprintf(&b, "把 %s 的 %d 个音符读成了**%s**。 调式 = **%s**（%s）。",
		sc.Which, len(sc.Notes), map[string]string{
			"terse": "你那套简洁记法", "musicxml": "MusicXML", "blocks": "音符块 JSON",
			"elements": "编辑器文档（元素序列）",
		}[format], rep.KeyName, rep.KeyHow)
	if len(ties) > 1 {
		// 与「生成和声」同一口径：自动检测**不唯一**时必须说出来
		var names []string
		for _, t := range ties {
			names = append(names, fmt.Sprintf("%d(%s)", t, jianpuKeys[t]))
		}
		fmt.Fprintf(&b, " ⚠️ **自动检测不唯一**：最低分 %d 分、%d 个旋转并列（%s），"+
			"按规则取了下标最小的那个（这串音符不严格落在任何大调上）⇒ 建议手动指定调式。",
			scoreOf(scores, autoKey), len(ties), strings.Join(names, "、"))
	}
	if rep.DiagnosticsTotal > 0 {
		fmt.Fprintf(&b, " ⚠️ 写出时有 %d 条诊断（如「某拍在这套记法里写不出来」），逐条在结果里。", rep.DiagnosticsTotal)
	}
	if format == "musicxml" {
		b.WriteString(" ⭐ 这份 MusicXML 可以直接存成 `.musicxml` 交给打谱软件，或喂给 ACE（`import file` 收 MusicXML）。")
	}
	// ⛔ 别忘把总结挂回去 —— 我第一版就是漏了这一行，于是**并列提示与"写出时有 N 条诊断"
	// 连同整句总结全都悄悄丢了**（只读工具没有其它渠道说这些话，真机干跑才看出来）。
	rep.Note = b.String()
	return rep, nil
}

func init() {
	register(&Tool{
		ID: "jianpu.fromClip", Category: jianpuCategory,
		Title: "ACE 音符 → 简谱 / MusicXML",
		Desc: "反向那半：把 ACE 里的音符读成**简谱文本**（你自己那套记法）、**MusicXML**，或音符块 JSON。" +
			"调式可**自动检测**（复用「生成和声」那套：12 个旋转里挑落在黑键上最少的）或手动指定；" +
			"⚠️ 自动检测**会并列**（旋律不严格落在任何大调上时），那时会**如实报出并列项**并建议手动指定。" +
			"⭐ **MusicXML 是中间形态**：产物可直接存成 `.musicxml` 交给打谱软件看五线谱，" +
			"也可以喂 ACE（`import file` 收 MusicXML）⇒ 导出五线谱与喂 ACE 是同一条路。" +
			"⭐ **空拍 = 休止**：ACE 里没有「休止符对象」（一段没有音符的时间就是休止）⇒ 这里" +
			"**按两个音之间的间隙补出休止**（简谱一格 `0` / MusicXML 的 `<rest/>`），后面的音不会挤到前面。" +
			"⚠️ 简谱写不出来的时值（三连音之类）会**报诊断**并退到最近写法，不静默写错。只读，不改工程。",
		Kind:   KindRead,
		Source: "反向：`internal/jianpu` 的两套写出器（MusicXML 中间形态，用户 2026-10-05 定）",
		Params: []Param{
			{Name: "scope", Label: "范围", Type: ParamSelect, Default: "auto",
				Options: []Option{
					{Value: "auto", Label: "选中的音符（没选中就整个片段）"},
					{Value: "clip", Label: "整个当前片段"},
				}},
			{Name: "format", Label: "产出", Type: ParamSelect, Default: "terse",
				Options: []Option{
					{Value: "terse", Label: "你那套简洁记法（可直接贴回「简谱 → ACE 音符」）"},
					{Value: "musicxml", Label: "MusicXML（给打谱软件 / 喂回 ACE）"},
					{Value: "elements", Label: "编辑器文档 JSON（喂给谱面编辑器）"},
					{Value: "blocks", Label: "音符块 JSON（调试用）"},
				}},
			{Name: "key", Label: "调式", Type: ParamSelect, Default: "auto",
				Options: jianpuKeyOptions()},
			areaParam("elements", "编辑器文档（粘贴则替代读工程）", "", 8,
				`[{"kind":"note","pitch":1,"octave":0,"duration":4,"code":""},…]`,
				"给了它就**不读工程**，直接拿这份文档去算产物 —— 编辑器导出/渲染走这条"),
			textParam("meter", "拍号", "4/4", "4/4", "决定小节怎么切"),
			textParam("title", "标题", "", "曲名", "只写进 MusicXML 的 `<work-title>`"),
			textParam("composer", "作者", "", "词曲", "只写进 MusicXML 的 `<creator>`"),
		},
		Inspect: func(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
			rep, err := analyzeFromClip(ctx, rd, vals)
			if err != nil {
				return nil, err
			}
			rep.Note = "只读：没动工程。" + rep.Note
			return rep, nil
		},
	})
}
