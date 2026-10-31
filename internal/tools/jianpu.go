package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"acetool/internal/jianpu"
)

// 把 `internal/jianpu` 接成 ACETool 的工具 —— 「谱面 ⇄ 音符块 ⇄ ACE」这条链的落地。
//
//	`jianpu.toNotes`  —— 编辑器文档（或文本）→ 音符块 → **写进 ACE**
//	`jianpu.fromClip` —— **ACE → 编辑器文档**（元素序列）/ 简谱文本 / MusicXML
//
// ⭐ 用户 2026-10-05 的方向修正：**不用文本记法了，直接 web 编辑**。
// 于是"编辑器文档"就是 `[]jianpu.Element`（音符 / `-` 延音 / 小节线），
// 文本记法降级成它的两个**可选入口**（简洁记法 / 番茄 DSL）。
//
// ⭐ 同日另一条口径修正：**连音是 ACE 的 `-` 音符**。
// ACE 官方（`help note-exclusivity`）：一个"音符组" = 一个**头音符**（带歌词与语言）
// + 零个或多个 **tenuto 音符**，而 tenuto 就写成歌词字段里的字面 `-`。
// ⇒ 简谱的 `1 - -` 到 ACE 是**三个音符**（歌词 `la`/`-`/`-`，音高相同），**不是**一个三拍的长音；
// 反过来 ACE 里的 `-` 歌词读回来就是 `-` 元素 ⇒ **ACE ⇄ 编辑器是完美往返**。

const jianpuCategory = "简谱"

// jianpuKeys 是用户那套记法的十二个调式（与 `输入简谱.js` 的 ComboBox 同序）。
var jianpuKeys = []string{"1=C", "1=#C", "1=D", "1=#D", "1=E", "1=F",
	"1=#F", "1=G", "1=#G", "1=A", "1=#A", "1=B"}

func jianpuKeyOptions() []Option {
	out := make([]Option, 0, len(jianpuKeys)+1)
	out = append(out, Option{Value: "auto", Label: "自动检测（照 12 个旋转挑黑键最少）"})
	for i, k := range jianpuKeys {
		out = append(out, Option{Value: strconv.Itoa(i), Label: k})
	}
	return out
}

// parseMeter 读 `4/4` 这种拍号。
func parseMeter(s string) (jianpu.Meter, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return jianpu.Meter{Numerator: 4, Denominator: 4}, nil
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return jianpu.Meter{}, fmt.Errorf("拍号要写成 `4/4` 这样，收到 %q", s)
	}
	n, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	d, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || n <= 0 || d <= 0 {
		return jianpu.Meter{}, fmt.Errorf("拍号 %q 里不是两个正整数", s)
	}
	return jianpu.Meter{Numerator: n, Denominator: d}, nil
}

// diagLines 把诊断翻成人能读的几行（原样带 code）。
func diagLines(ds []jianpu.Diagnostic) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		loc := ""
		if d.Line > 0 {
			loc = fmt.Sprintf("第 %d 行 ", d.Line)
		}
		out = append(out, fmt.Sprintf("%s[%s] %s", loc, d.Code, d.Message))
	}
	return out
}

// barOfTick 把**工程 tick** 换成 **1 起**的小节号（`convert tick-to-measure`，变拍号也对）。
//
// ⚠️ 两个反直觉的点（都实测过）：
//   - 回包里的 `barPos` 是 **0 起**的（tick 15420 在 4/4 下回 `barPos:8, tickOffset:60`
//     ⇒ 那是**第 9 小节**里第 60 个 tick）⇒ 这里 +1 换成 1 起；
//   - `--consider-beat-mode` 是**必填**参数（漏了直接 `required arguments were not provided`）。
func barOfTickErr(ctx context.Context, rd ReadFunc, tick int64) (int, error) {
	var out struct {
		BarPos     int `json:"barPos"`
		TickOffset int `json:"tickOffset"`
	}
	if err := readJSON(ctx, rd, &out, "convert", "tick-to-measure",
		"--tick", strconv.FormatInt(tick, 10), "--consider-beat-mode", "false"); err != nil {
		return 0, err
	}
	return out.BarPos + 1, nil
}

// barOfTick 是"读不到就回 0"的宽松版（只用来在报告里标一句，不让整件事失败）。
func barOfTick(ctx context.Context, rd ReadFunc, tick int64) int {
	bar, err := barOfTickErr(ctx, rd, tick)
	if err != nil {
		return 0
	}
	return bar
}

// ---------------------------------------------------------------- 元素层

// jpElem 是「编辑器模型」那一层的元素：**音符 / `-` 延音 / 休止**。
type jpElem struct {
	Midi    int    // <0 表示休止
	Sustain bool   // `-`：歌词写 `-`，音高沿用前一个音
	Rest    bool   // 休止
	Ticks   int64  // 时值
	Raw     string // 源码原样（对账用）
	Lyric   string // 这个元素上唱的字；空 ⇒ 用 `defaultLyric`
}

// elementsToJpElems 把 `jianpu.Element`（简谱：音级+八度+变音+时值）转成内部元素，**带调式换算**。
//
// ⭐ 抽出来是为了让三条入口共用同一套：简洁记法 · 番茄 DSL · **编辑器直接发来的元素 JSON**。
// 三处必须一致 —— 否则同一个音从不同入口进来会得到不同音高，而且**不报错**。
func elementsToJpElems(els []jianpu.Element, tonic int) ([]jpElem, []string, error) {
	var out []jpElem
	var diagList []string
	for _, e := range els {
		tk := jianpu.DurationTicks(e.Duration, e.Dots, 480)
		switch e.Kind {
		case jianpu.KindSustain:
			out = append(out, jpElem{Sustain: true, Ticks: tk, Raw: e.Code})
		case jianpu.KindBarline:
			// 小节线**不是音符**：不占时值、不产出音符 —— 这不是"丢东西"，所以不报诊断。
			continue
		case jianpu.KindNote, "", jianpu.KindRest:
			// ⚠️ 三种写法都得接住，而且**必须接住**：
			//   * `""`   —— JSON 里 `kind` 省略时（手写 / 别的工具产出）
			//   * `"rest"` —— **编辑器方言**（面板里休止就是一格 `0`，发出来的是 kind:"rest"）
			//   * `"note"` —— 上游语义，休止是 `rest:true`
			// 起初这里只有 KindNote 一个分支，于是编辑器发的 `kind:"rest"` **静默消失**：
			// 休止那一格不占时值 ⇒ **后面的音符整体前移**，而且一个诊断都没有。
			// 真机自检（devtools/e2e.js ④）就是靠"跳过 0 个"这一条把这个抓出来的。
			switch {
			case e.Rest || e.Pitch == 0 || e.Kind == jianpu.KindRest:
				out = append(out, jpElem{Midi: -1, Rest: true, Ticks: tk, Raw: e.Code, Lyric: e.Lyric})
			case e.Pitch == 9 || e.Sound == "rhythm":
				diagList = append(diagList,
					"谱里有 `9`（节奏音/念白）：ACE 的音符没有这个种类 ⇒ 当**休止**处理（占位、不唱）")
				out = append(out, jpElem{Midi: -1, Rest: true, Ticks: tk, Raw: e.Code})
			default:
				semi, ok := jianpu.DegreeSemitone(e.Pitch)
				if !ok {
					return nil, diagList, fmt.Errorf("度数 %d 不在 1..7 里", e.Pitch)
				}
				m := 60 + tonic + semi + 12*e.Octave
				switch e.Accidental {
				case jianpu.AccidentalSharp:
					m++
				case jianpu.AccidentalFlat:
					m--
				}
				out = append(out, jpElem{Midi: m, Ticks: tk, Raw: e.Code, Lyric: e.Lyric})
			}
		default:
			// 不认识的 kind **要报出来**，不能静默吞 —— 静默吞掉的东西体现为"音符莫名少了"，
			// 那是最难查的一类毛病（这条纪律的由来见上面 `"rest"` 那段注释）。
			diagList = append(diagList, fmt.Sprintf(
				"元素里有不认识的 `kind:%q`（时值 %d tick）⇒ **已跳过**，没写进 ACE", e.Kind, tk))
			continue
		}
	}
	return out, diagList, nil
}

// toNotesElements 解析输入成**元素序列**。三条入口的优先级：
//
//	① `elements`（编辑器直接发来的元素 JSON）—— **最高**，文本记法整段绕过
//	② `text` + `notation=fanqie`（番茄 DSL）
//	③ `text` + `notation=terse`（默认；用户那套简洁记法）
func toNotesElements(vals map[string]string) ([]jpElem, []string, error) {
	keyIdx := 0
	if s := strings.TrimSpace(vals["key"]); s != "" && s != "auto" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > 11 {
			return nil, nil, fmt.Errorf("调式只能是 0..11（C..B），收到 %q", s)
		}
		keyIdx = n
	}

	// ① 编辑器文档
	if s := strings.TrimSpace(vals["elements"]); s != "" {
		var els []jianpu.Element
		if err := json.Unmarshal([]byte(s), &els); err != nil {
			return nil, nil, fmt.Errorf("`elements` 不是合法的元素 JSON：%w（它是编辑器发来的元素序列）", err)
		}
		out, diagList, err := elementsToJpElems(els, keyIdx)
		return out, diagList, err
	}

	text := value(vals, "text", "")
	if strings.TrimSpace(text) == "" {
		return nil, nil, fmt.Errorf("要么给 `elements`（编辑器文档），要么给「简谱文本」")
	}

	switch value(vals, "notation", "terse") {
	case "fanqie":
		doc := jianpu.Parse(text)
		diagList := diagLines(doc.Diagnostics)
		tonic := keyIdx
		if pc, ok := jianpu.ModeToPitchClass(doc.Metadata.Mode); ok {
			tonic = pc // 文本里的 `D:` 优先
		}
		// ⚠️ ACE 的目标是**一个 Sing clip（单声部）** ⇒ 多声部输入只能取第一个声部，
		//    其余**如实报出来**，绝不顺次拼在一起（那会得到一个谁也不认的旋律）。
		var voices int
		var picked []jianpu.Element
		var caption string
		for _, pg := range doc.Pages {
			for _, gp := range pg.Groups {
				for _, v := range gp.Voices {
					voices++
					if picked == nil {
						picked, caption = v.Elements, v.Caption
					}
				}
			}
		}
		if voices > 1 {
			name := caption
			if name == "" {
				name = "（无名）"
			}
			diagList = append(diagList, fmt.Sprintf(
				"这份谱有 %d 个声部，而 ACE 的目标只能是**一个 Sing 片段（单声部）** ⇒ "+
					"只写了第一个声部「%s」，其余 %d 个**没写**", voices, name, voices-1))
		}
		out, extra, err := elementsToJpElems(picked, tonic)
		return out, append(diagList, extra...), err
	default:
		els, diags := jianpu.ParseNotationElements(text, jianpu.NotationOptions{KeyIndex: keyIdx, BaseMidi: 60})
		out, extra, err := elementsToJpElems(els, keyIdx)
		return out, append(diagLines(diags), extra...), err
	}
}

// ---------------------------------------------------------------- 谱 → ACE

// jianpuNoteJSON 是 `note add` / `clip create --notes` 收的形状。
type jianpuNoteJSON struct {
	Pos   int64  `json:"pos"`
	Dur   int64  `json:"dur"`
	Pitch int    `json:"pitch"`
	Lyric string `json:"lyric,omitempty"`
}

// notesJSONChunks 把音符序列切成若干段 JSON。
//
// ⚠️ 为什么要切：`--notes` 只能走**命令行参数**（这个动词没有 `@文件` 形式），
// 而 Windows 的命令行长度上限约 32 KB —— 一首几百个音的曲子 JSON 会顶到它。
// 与其让 ACE 报一个难懂的错误，不如**自己切块**（每块留足余量）。
func notesJSONChunks(notes []jianpuNoteJSON, maxBytes int) ([][]jianpuNoteJSON, error) {
	var out [][]jianpuNoteJSON
	var cur []jianpuNoteJSON
	for _, n := range notes {
		one, err := json.Marshal([]jianpuNoteJSON{n})
		if err != nil {
			return nil, err
		}
		if len(one) > maxBytes {
			return nil, fmt.Errorf("有一个音符的 JSON 本身就超了 %d 字节", maxBytes)
		}
		cur = append(cur, n)
		b, _ := json.Marshal(cur)
		if len(b) > maxBytes {
			cur = cur[:len(cur)-1]
			out = append(out, cur)
			cur = []jianpuNoteJSON{n}
		}
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out, nil
}

type jianpuToNotesReport struct {
	Notation string `json:"notation"`
	Target   string `json:"target"`
	StartAt  string `json:"startAt"`
	Source   string `json:"source"` // elements / fanqie / terse

	Clip     string `json:"clip,omitempty"`
	ClipUUID string `json:"clipUuid,omitempty"`
	Track    int    `json:"trackIndex"`

	StartTick int64 `json:"startTick"`
	ClipBegin int64 `json:"clipBegin"`
	// ⭐ 用户 2026-10-07：「生成音符的位置是当前 clip **从播放头所在小节**开始生成音符」——
	// 起点对齐到**播放头所在小节的起点**（小节线），播放头的原始位置与小节号都如实报出来。
	PlayheadTick int64 `json:"playheadTick,omitempty"`
	StartBar     int   `json:"startBar,omitempty"`     // 1 起的小节号
	StartBarTick int64 `json:"startBarTick,omitempty"` // 那一小节的起点（工程 tick）

	// 新建片段时，原定位置被占（`CLIP_OVERLAP`）⇒ 顺延后的位置。**0 表示没挪过。**
	ShiftedFrom int64 `json:"newClipShiftedFrom,omitempty"`
	ShiftedTo   int64 `json:"newClipShiftedTo,omitempty"`
	// 新建片段真正拿到的 uuid（写成功后从 `clip create` 回包里读出来的）。
	NewClipUUID string `json:"newClipUuid,omitempty"`

	Parsed  int `json:"parsedElements"`
	Write   int `json:"notesToWrite"`
	Skipped int `json:"skipped"`
	// ⭐ `-` 延音在 ACE 里是**独立音符**（歌词写 `-`）⇒ 单独计数。
	Sustains int `json:"sustainNotes,omitempty"`
	// `-` 前面没有音符可延续 ⇒ 跳过并计数。
	SustainOrphan int `json:"orphanSustains,omitempty"`

	MinPitch int   `json:"minPitch,omitempty"`
	MaxPitch int   `json:"maxPitch,omitempty"`
	TotalDur int64 `json:"totalTicks"`

	Preview []string         `json:"preview,omitempty"`
	Notes   []jianpuNoteJSON `json:"-"`

	Diagnostics []string `json:"diagnostics,omitempty"`
	Calls       []string `json:"plannedCalls,omitempty"`
	Did         []string `json:"done,omitempty"`
	DryRun      bool     `json:"dryRun,omitempty"`
	Note        string   `json:"note,omitempty"`
}

// analyzeToNotes 读齐"要写进哪、从哪儿开始"，并算好每个音的 clip-local 位置。**只读**。
func analyzeToNotes(ctx context.Context, rd ReadFunc, vals map[string]string) (*jianpuToNotesReport, []jianpuNoteJSON, error) {
	elem, diagList, err := toNotesElements(vals)
	if err != nil {
		return nil, nil, err
	}
	src := "terse"
	switch {
	case strings.TrimSpace(vals["elements"]) != "":
		src = "elements"
	case value(vals, "notation", "terse") == "fanqie":
		src = "fanqie"
	}
	rep := &jianpuToNotesReport{
		Notation: value(vals, "notation", "terse"), Source: src,
		Target: value(vals, "target", "current"), StartAt: value(vals, "startAt", "playhead"),
		Parsed: len(elem), Diagnostics: diagList,
	}
	if len(elem) == 0 {
		return nil, nil, fmt.Errorf("一个元素都没解析出来（诊断见结果）—— 检查输入后重试")
	}

	sc, err := readNoteScope(ctx, rd, false)
	if err != nil {
		return nil, nil, err
	}
	rep.Clip, rep.ClipUUID, rep.Track, rep.ClipBegin = sc.ClipName, sc.ClipUUID, sc.TrackIndex, sc.Begin

	// 起点：⭐ 用户 2026-10-07 的口径 —— **从播放头所在小节开始**（对齐到那条小节线）。
	//   · `playhead`（默认）= 播放头**所在小节的起点**（往下取整到小节线；变拍号也跟着对，
	//     走 ACE 的 `convert tick-to-measure` + `measure-to-tick`，不自己按 1920 算）；
	//   · `playheadExact` = 播放头的**精确 tick**（旧行为：不吸附）；
	//   · `clipStart` = 片段开头。
	var startGlobal int64
	switch rep.StartAt {
	case "clipStart":
		startGlobal = sc.Begin
	case "playheadExact":
		ph, err := readPlaybackHead(ctx, rd)
		if err != nil {
			return nil, nil, fmt.Errorf("读播放头（`transport state`）：%w", err)
		}
		rep.PlayheadTick, startGlobal = ph, ph
		rep.StartBar = barOfTick(ctx, rd, ph)
		rep.StartBarTick = startGlobal
	default: // playhead：**对齐到播放头所在小节的起点**
		ph, err := readPlaybackHead(ctx, rd)
		if err != nil {
			return nil, nil, fmt.Errorf("读播放头（`transport state`）：%w", err)
		}
		rep.PlayheadTick = ph
		bar, err := barOfTickErr(ctx, rd, ph)
		if err != nil {
			return nil, nil, fmt.Errorf("把播放头 %d 换成小节号（`convert tick-to-measure`）：%w", ph, err)
		}
		line, err := measureToTick(ctx, rd, float64(bar), "播放头所在小节")
		if err != nil {
			return nil, nil, err
		}
		rep.StartBar, rep.StartBarTick, startGlobal = bar, line, line
	}
	// ⚠️ 原脚本起手 `snap()` 一次（吸附到更细的网格）。这里**只对齐到小节线**，
	// 再细的网格不替用户决定 —— 对齐到哪一条线会写进报告（`startBarTick`）。
	rep.StartTick = startGlobal

	// ⭐ 新建片段**不许与轨道上已有片段重叠**（ACE 硬拒 `CLIP_OVERLAP`，真机实测：
	// 从片段头新建会撞上原片段）⇒ 先算出一个放得下的位置。
	//   * 干跑与真写都走这一步，两边给出的 `--pos` 才会一致（否则干跑说 pos 0、真写报错）。
	//   * **不静默挪**：挪了就写进 `rep.ShiftedFrom/To` 并进收尾说明。
	if rep.Target == "newClip" {
		var reserve int64
		for _, e := range elem {
			reserve += e.Ticks
		}
		if clips, cerr := clipListBrief(ctx, rd, sc.TrackIndex); cerr == nil {
			if free := freeClipStart(clips, startGlobal, reserve); free != startGlobal {
				rep.ShiftedFrom, rep.ShiftedTo = startGlobal, free
				startGlobal = free
				rep.StartTick = free
			}
		}
	}

	// ⭐ 一个元素一个 ACE 音符：`-` 展开成**歌词为 `-` 的音符**（音高沿用前一个音）。
	//
	// ⚠️⚠️ 这里的坐标**必须是片段内（clip-local）**的，两种目标算法不一样：
	//   * 加进当前片段 ⇒ 相对**那个片段**的起点：`startGlobal - sc.Begin`
	//   * 新建片段     ⇒ 新片段自己从 `startGlobal` 起 ⇒ **音符从 0 开始**
	// 起初两种都用了 `startGlobal - sc.Begin`。平时（playhead 恰好落在片段头）看不出来，
	// 一旦起点不在片段头（真机实测：顺延到 3840 之后），音符就被写到了 clip-local 3840
	// —— **超出片段长度** ⇒ ACE 把片段建了、命名了（名字还是从歌词推的），却**一个音符都不显示**。
	// 那是"看着成功、其实空"的最坏一种失败：`clip create` 的退出码是 0。
	cursor := startGlobal - sc.Begin
	if rep.Target == "newClip" {
		cursor = 0
	}
	rep.Notes = make([]jianpuNoteJSON, 0, len(elem))
	lastPitch := -1
	resting := false // 前一个元素是不是休止（`0 -` = 休止拖长，**不能**去续上一个音的音高）
	for _, e := range elem {
		if e.Sustain {
			if resting {
				// ⭐ 休止后面的 `-`：**还是休止**（简谱里 `0 -` 就是"空两拍"）——
				// 空过去但占位置，**不算孤儿**（有前文可延续，只是延续的是"静音"）。
				cursor += e.Ticks
				continue
			}
			if lastPitch < 0 { // 前面没有音高可延续
				rep.Skipped++
				rep.SustainOrphan++
				cursor += e.Ticks
				continue
			}
			rep.Notes = append(rep.Notes, jianpuNoteJSON{
				Pos: cursor, Dur: e.Ticks, Pitch: lastPitch, Lyric: "-",
			})
			rep.Sustains++
			rep.TotalDur += e.Ticks
			cursor += e.Ticks
			continue
		}
		if e.Midi < 0 { // 休止：ACE 没有休止符 ⇒ 空过去但**占位置**（音符的时刻不变）
			rep.Skipped++
			cursor += e.Ticks
			resting = true
			lastPitch = -1 // ⚠️ 别让休止后面的 `-` 把**上一个音**的音高续回来
			continue
		}
		resting = false
		if cursor < 0 {
			cursor = 0 // 播放头在片段起点之前 ⇒ 从片段头开始
		}
		// 歌词：**元素自带的字优先**（编辑器里一格一个字），没有才用 `defaultLyric`。
		// 文本记法那两条入口的元素不带歌词 ⇒ 行为与从前完全一致（默认 `la`）。
		lyric := e.Lyric
		if lyric == "" {
			lyric = value(vals, "defaultLyric", "la")
		}
		rep.Notes = append(rep.Notes, jianpuNoteJSON{
			Pos: cursor, Dur: e.Ticks, Pitch: e.Midi, Lyric: lyric,
		})
		lastPitch = e.Midi
		rep.TotalDur += e.Ticks
		if rep.MinPitch == 0 || e.Midi < rep.MinPitch {
			rep.MinPitch = e.Midi
		}
		if e.Midi > rep.MaxPitch {
			rep.MaxPitch = e.Midi
		}
		cursor += e.Ticks
	}
	rep.Write = len(rep.Notes)
	if rep.Write == 0 {
		return nil, nil, fmt.Errorf("解析出了 %d 个元素，但**全是休止** —— ACE 的 `note add` 没有休止符，没东西可写", len(elem))
	}
	for i, n := range rep.Notes {
		if i >= 8 {
			rep.Preview = append(rep.Preview, fmt.Sprintf("…共 %d 个", len(rep.Notes)))
			break
		}
		rep.Preview = append(rep.Preview, fmt.Sprintf("pos %d dur %d pitch %d %s", n.Pos, n.Dur, n.Pitch, n.Lyric))
	}

	chunks, err := notesJSONChunks(rep.Notes, 20000)
	if err != nil {
		return nil, nil, err
	}
	if rep.Target == "newClip" {
		rep.Calls = append(rep.Calls, fmt.Sprintf(
			"clip create --track-index %d --pos %dt --dur %dt --type sing --notes '<%d 个音>'   （新建片段）",
			sc.TrackIndex, startGlobal, rep.TotalDur, len(rep.Notes)))
	} else {
		for i := range chunks {
			rep.Calls = append(rep.Calls, fmt.Sprintf(
				"note add --clip-uuid %s --notes '<第 %d 段，%d 个音>'", sc.ClipUUID, i+1, len(chunks[i])))
		}
	}
	return rep, rep.Notes, nil
}

func toNotesNote(rep *jianpuToNotesReport, dry bool) string {
	var b strings.Builder
	if dry {
		b.WriteString("干跑：一个字节都没写。")
	} else {
		b.WriteString("已写入。")
	}
	srcName := map[string]string{"elements": "编辑器文档（元素序列）", "fanqie": "番茄简谱 DSL", "terse": "简洁记法"}[rep.Source]
	fmt.Fprintf(&b, " 输入 = **%s**；%d 个元素 ⇒ 会写 **%d** 个音符", srcName, rep.Parsed, rep.Write)
	if rep.Sustains > 0 {
		fmt.Fprintf(&b, "（其中 **%d** 个是 `-` 延音 —— ACE 里它就是**歌词为 `-` 的音符**，"+
			"与头音符同属一个音节，不是把音写长）", rep.Sustains)
	}
	if rep.Skipped > 0 {
		fmt.Fprintf(&b, "（%d 个休止被跳过 —— ACE 的 `note add` 没有休止符，只能空过去，位置照样占）", rep.Skipped)
	}
	if rep.SustainOrphan > 0 {
		fmt.Fprintf(&b, " ⚠️ 有 %d 个 `-` 前面没有音符可延续，已跳过。", rep.SustainOrphan)
	}
	fmt.Fprintf(&b, "。 音域 %d..%d、总长 %d tick。", rep.MinPitch, rep.MaxPitch, rep.TotalDur)
	if rep.Target == "newClip" {
		fmt.Fprintf(&b, " 写到**新建的片段**里（轨道 %d，从工程 tick %d 起）。", rep.Track, rep.StartTick)
		if rep.ShiftedTo != 0 && rep.ShiftedTo != rep.ShiftedFrom {
			fmt.Fprintf(&b, " ⚠️ 原定起点 %d 落在一个**已有片段**上（`clip create` 会硬拒 `CLIP_OVERLAP`）"+
				"⇒ **顺延到第一个空位 %d**。要落在别处就先把那儿的片段挪开，或改用「加进当前片段」。",
				rep.ShiftedFrom, rep.ShiftedTo)
		}
	} else {
		fmt.Fprintf(&b, " 加进片段「%s」（轨道 %d），起点是工程 tick %d ⇒ 片段内 tick %d。",
			rep.Clip, rep.Track, rep.StartTick, rep.StartTick-rep.ClipBegin)
	}
	// ⭐ 用户 2026-10-07：「生成音符的位置是当前 clip **从播放头所在小节**开始生成音符」——
	// 起点是怎么定的、对齐到哪条小节线，这里必须说清（否则用户不明白"为什么从我手指的地方往前挪了一点"）。
	switch rep.StartAt {
	case "clipStart":
		b.WriteString(" 起点 = **片段开头**。")
	case "playheadExact":
		fmt.Fprintf(&b, " 起点 = **播放头的精确位置** %d（这一档**不对齐小节线**）。", rep.PlayheadTick)
	default:
		if rep.StartBar > 0 {
			fmt.Fprintf(&b, " 起点 = **播放头所在小节的起点**：播放头 %d 落在第 %d 小节 ⇒ 对齐到小节线 %d（往下取整）。",
				rep.PlayheadTick, rep.StartBar, rep.StartBarTick)
		} else {
			fmt.Fprintf(&b, " 起点 = 播放头所在小节的起点 %d（⚠️ 小节号没读出来，没能标出是第几小节）。", rep.StartTick)
		}
	}
	b.WriteString(" ⚠️ 只对齐到**小节线**，再细的网格不吸附（原脚本会 `snap()`）。")
	if len(rep.Diagnostics) > 0 {
		fmt.Fprintf(&b, " ⚠️ 有 %d 条诊断/提示，逐条在结果里。", len(rep.Diagnostics))
	}
	return b.String()
}

func jianpuToNotesPlan(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	rep, _, err := analyzeToNotes(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	rep.DryRun = true
	rep.Note = toNotesNote(rep, true)
	return rep, nil
}

// freeClipStart 从 `want` 往后找**第一个放得下 dur 长片段**的位置。
//
// 用途：`clip create` 撞上已有片段时 ACE 会硬拒（`CLIP_OVERLAP: A clip at [0, 2880) would overlap clip '…'`）。
// 对一个**新建**片段来说，"往后顺延到空位"是自然的默认（用户要的是"把这段旋律放进工程"，
// 不是"必须落在这一刻"），但**必须报出来** —— 静默挪位置会让人对不上账。
//
// 算法：反复把所有与 [start, start+dur) 相交的片段右端推成新起点，直到不相交；
// 每次至少吃掉一个片段 ⇒ `len(clips)+1` 轮内必然收敛。
func freeClipStart(clips []clipBrief, want, dur int64) int64 {
	start := want
	for round := 0; round <= len(clips); round++ {
		moved := false
		for _, c := range clips {
			if start < c.End && c.Begin < start+dur {
				start = c.End
				moved = true
			}
		}
		if !moved {
			return start
		}
	}
	return start
}

func jianpuToNotesApply(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	rep, notes, err := analyzeToNotes(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	var done []string
	fail := func(step string, e error) (any, error) {
		rep.Did = done
		rep.Note = fmt.Sprintf("在「%s」这一步失败，**前面已经写进去的音符没有回滚**（ACE 每个动词是独立撤销记录，Ctrl+Z 可逐步退回）。", step)
		return rep, fmt.Errorf("%s：%w", step, e)
	}
	if rep.Target == "newClip" {
		js, _ := json.Marshal(notes)
		args := []string{"clip", "create", "--track-index", strconv.Itoa(rep.Track),
			"--pos", fmt.Sprintf("%dt", rep.StartTick), "--dur", fmt.Sprintf("%dt", rep.TotalDur),
			"--type", "sing", "--notes", string(js)}
		res, err := runWriteRes(ctx, rd, args...)
		if err != nil {
			return fail("新建片段并写入音符", err)
		}
		// ⭐ **当场核对回包**：宿主说写进去几个音，是不是我们要的那几个？
		//    `clip create` 的退出码是 0 —— 光看它会把"片段建了、音符一个没落"当成成功。
		var out struct {
			ClipUUID  string `json:"clipUuid"`
			NoteCount int    `json:"noteCount"`
		}
		if len(res.JSON) > 0 {
			_ = json.Unmarshal(res.JSON, &out)
		}
		rep.NewClipUUID = out.ClipUUID
		if out.ClipUUID != "" && out.NoteCount != len(notes) {
			return fail("核对新建片段的音符数", fmt.Errorf(
				"宿主回包里只有 **%d** 个音符，而我们要写 %d 个（片段 uuid %s）⇒ "+
					"音符很可能落在片段长度之外（`clip create` 退出码仍是 0，属于**看着成功其实空**）。"+
					"已建的空片段请自行删掉", out.NoteCount, len(notes), out.ClipUUID))
		}
		done = append(done, fmt.Sprintf("在轨道 %d 上建了片段 %s（%d 个音，已按回包核对）",
			rep.Track, rep.NewClipUUID, out.NoteCount))
	} else {
		chunks, err := notesJSONChunks(notes, 20000)
		if err != nil {
			return nil, err
		}
		for i, c := range chunks {
			js, _ := json.Marshal(c)
			if err := runWrite(ctx, rd, "note", "add", "--clip-uuid", rep.ClipUUID, "--notes", string(js)); err != nil {
				return fail(fmt.Sprintf("写第 %d 段音符", i+1), err)
			}
			done = append(done, fmt.Sprintf("第 %d 段：%d 个音", i+1, len(c)))
		}
	}
	rep.Did = done
	rep.Note = toNotesNote(rep, false)
	return rep, nil
}

func init() {
	register(&Tool{
		ID: "jianpu.toNotes", Category: jianpuCategory,
		Title: "编辑器文档 → ACE 音符",
		Desc: "把**编辑器文档**（元素序列 JSON）或**简谱文本**解析成音符块，再写进 ACE。" +
			"⚠️ 连音按 ACE 的口径走：`-` 延音展开成**歌词为 `-` 的音符**（tenuto），" +
			"与头音符同属一个音节 —— 简谱的 `1 - -` 到 ACE 是**三个音符**，不是一个三拍的长音" +
			"（官方 `help note-exclusivity`：tenuto notes are written as the literal `-` in the lyric field）。" +
			"输入三选一：`elements`（编辑器发的元素 JSON，优先）· 简洁记法文本 · 番茄简谱 DSL 文本。" +
			"⭐ **休止 = 空拍**：ACE 里本来就没有「休止符对象」，一段没有音符的时间**就是**休止 ⇒ " +
			"写的时候休止**跳过但占位置**并计数（**音符的时刻不变**），`0 -`（休止拖长）也照样只占位置、" +
			"**不会**去续上一个音的音高；`9` 节奏音当休止并说明。" +
			"⚠️ 番茄 DSL 多声部时**只取第一个声部**并如实报出（Sing 片段是单声部的）。" +
			"⭐ 起点默认是**播放头所在小节的起点**（用户 2026-10-07：「生成音符的位置是当前 clip " +
			"**从播放头所在小节**开始生成音符」）—— 往下取整到那条小节线，变拍号也跟着对" +
			"（走 ACE 的 `convert tick-to-measure` + `measure-to-tick`，不自己按 1920 算）；" +
			"要旧行为（播放头的精确 tick、不吸附）就选 `startAt=playheadExact`。" +
			"⚠️ 再细的网格**不吸附**（原脚本会 `snap()`）。" +
			"**干跑默认**：勾着只出计划 + 前几个音符预览，一个字节都不写。",
		Kind:   KindWrite,
		Source: "AKD `输入简谱.js`（+ 番茄简谱 DSL 解析器，MIT · Linho1219/open-fanqie）",
		Params: []Param{
			areaParam("elements", "编辑器文档（元素 JSON）", "", 4, `[{"kind":"note","pitch":1,"duration":4}]`,
				"由「简谱编辑器」页面自动填；与 `text` 二选一，**它优先**"),
			{Name: "notation", Label: "文本记法（给 text 用）", Type: ParamSelect, Default: "terse",
				Options: []Option{
					{Value: "terse", Label: "简洁记法（1-7 / 0 / - / = / q w # b . _）"},
					{Value: "fanqie", Label: "番茄简谱 DSL（多声部 / 连音线 / & 装饰）"},
				}},
			areaParam("text", "简谱文本", "", 8, "1 2 3 5 | 6 - - - |",
				"空格与 `|` 都当分隔；只在没给 `elements` 时用"),
			{Name: "key", Label: "调式", Type: ParamSelect, Default: "0", Options: jianpuKeyOptions()[1:]},
			textParam("defaultLyric", "头音符的默认歌词", "la", "la",
				"只给**头音符**；tenuto 音符的歌词固定是 `-`"),
			{Name: "target", Label: "写到哪", Type: ParamSelect, Default: "current",
				Options: []Option{
					{Value: "current", Label: "加进当前片段（照原脚本）"},
					{Value: "newClip", Label: "在当前轨新建一个片段"},
				}},
			{Name: "startAt", Label: "起点", Type: ParamSelect, Default: "playhead",
				Options: []Option{
					{Value: "playhead", Label: "播放头所在小节的起点（对齐小节线）"},
					{Value: "playheadExact", Label: "播放头的精确位置（不吸附）"},
					{Value: "clipStart", Label: "片段开头"},
				}},
		},
		Plan:  jianpuToNotesPlan,
		Apply: jianpuToNotesApply,
	})
}
