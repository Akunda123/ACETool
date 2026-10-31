package jianpu

import (
	"fmt"
	"math"
	"strings"
)

// 移植 `输入简谱.js`（AKD「律制-输入简谱」）—— **用户自己那套简洁记法**。
//
// 它与 `Parse`/`Write` 那套（番茄简谱 DSL）**不是一回事**，别混：
//
//	番茄简谱 DSL   ：多声部、连音线、八度逗号、`&` 装饰、`[fenye]` 换页……（照上游用例对齐）
//	本文件这套记法 ：`1-7` 唱名 · `0` 休止 · `-` 延音 · `=` 延长前一个音 ·
//	                `q`/`w` 升降八度 · `#`/`b` 升降半音 · `.` 附点(×1.5) · `_` 缩短(×0.5)
//	                调式是 `1=C`…`1=B` 十二档
//
// 原脚本的音高算法（照抄，已核）：`pitch = 60 + 调式档位 + yinjie[度数]`，`yinjie = [0,0,2,4,5,7,9,11]`。
// 起音位置 = 播放头（并 `snap()` 对齐网格），逐个往后排。
//
// ⛔⛔ 原脚本有**两个真 bug**，本移植**不照抄**（与 §19.3 的死循环、§19.4 的跳句 off-by-one 同样处置），
// 并把差异做成**干跑就能看见的诊断**：
//
//  1. **修饰符的作用对象错位**。原脚本扫修饰符的区间是 `[第 j 个数字, 第 j+1 个数字)`，
//     再用**区间末尾那个字**决定这个音 —— 于是修饰符实际作用于**下一个**数字：
//     写 `1. 2` 得到「1 不加点、**2 加点**」；只有写成前缀（`.1`）才是附点 1，
//     而这与简谱的书写习惯（`1.`）**相反**，功能说明里也没写。末尾的修饰符还会整段丢掉。
//
//     ⇒ 本移植的**归属规则**（比原脚本宽，也更符合习惯）：
//     · 修饰符串只挨着**一个**数字（`.1` 或 `1.`）⇒ 就归它，**无歧义、不报诊断**；
//     · 修饰符串夹在**两个**数字之间（`1. 2`）⇒ 这才是真歧义：默认归**前一个**数字（`1.` = 附点 1），
//     并报一条聚合诊断说明两种读法的差别；`LegacyPrefixModifiers=true` 可切回原脚本（归后一个）。
//
//  2. **`-`（延音线）会凭空造出一个主音音符**。原脚本里 `-` 也算"数字"，但它落进 else 分支：
//     `Number("-")` 是 `NaN` ⇒ 音高不调整 ⇒ **以主音音高建一个新音符**，而不是延长前一个音。
//     ⇒ 本移植按功能说明（"`-`=延音线"）实现成**延长前一个音符**。

// NotationOptions 是这套简洁记法的参数。
type NotationOptions struct {
	// KeyIndex 调式档位 0..11（对应 `1=C` … `1=B`）。
	KeyIndex int
	// DefaultLyric 每个音符的默认歌词（原脚本默认 "la"）。
	DefaultLyric string
	// BaseMidi 简谱「无八度记号」的 `1` 对应的 MIDI（原脚本是 60 = C4）。
	BaseMidi int
	// Meter 拍号（用来铺小节线）。
	Meter Meter
	// LegacyPrefixModifiers=true 时按**原脚本**的错位读法解析
	// （夹在两个数字之间的修饰符归**后一个**数字）。
	// 默认 false = 归**前一个**数字（与简谱书写习惯一致，如 `1.` = 附点 1）。
	LegacyPrefixModifiers bool
}

// notationKeyNames 是 `1=X` 的十二个档位（原脚本 ComboBox 的顺序）。
var notationKeyNames = []string{"1=C", "1=#C", "1=D", "1=#D", "1=E", "1=F",
	"1=#F", "1=G", "1=#G", "1=A", "1=#A", "1=B"}

// yinjie 是原脚本那张"度数 → 半音"表（下标即度数；0 与 1 都是 0）。
var yinjie = [8]int{0, 0, 2, 4, 5, 7, 9, 11}

// isNotationHead 判断是否是"起一个记号"的字符：`0-7`、`-`、`=`。
//
// ⚠️ 原脚本的"起记号"集合是 `0-7` 与 `-`（**不含 `=`**），`=` 靠"落在某个区间的末尾"被认出来 ——
// 那正是它错位读法的一部分。这里把 `=` 提成独立的头，语义照功能说明（"延长前一个音符"）。
func isNotationHead(c byte) bool {
	return (c >= '0' && c <= '7') || c == '-' || c == '='
}

// isNotationModifier 是修饰符集合（原脚本逐个 if 认的那五个）。
// ⚠️ **不含 `=`** —— 它是独立的"延长"记号，不是音符的修饰符。
func isNotationModifier(c byte) bool {
	switch c {
	case 'q', 'w', '.', '_', '#', 'b':
		return true
	}
	return false
}

// notationToken 是一个记号：头字符 + 跟它的修饰符。
type notationToken struct {
	Head string
	Mods string
	Raw  string
}

// scanItem 是扫描出来的原子项：一个"头"或一段"修饰符串"。
type scanItem struct {
	head bool // true = 头（数字/-/=），false = 修饰符串
	text string
}

// scanNotation 把输入扫成原子项序列（**先不做归属**），并报出认不出的字符。
//
// 分隔符：空白与 `|`（`|` 原脚本的扫描根本不认，等于忽略）。
func scanNotation(text string) ([]scanItem, []Diagnostic) {
	var items []scanItem
	var diags []Diagnostic
	line := 1
	i := 0
	for i < len(text) {
		c := text[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r' || c == '|':
			i++
		case isNotationHead(c):
			items = append(items, scanItem{head: true, text: string(c)})
			i++
		case isNotationModifier(c):
			start := i
			for i < len(text) && isNotationModifier(text[i]) {
				i++
			}
			items = append(items, scanItem{head: false, text: text[start:i]})
		default:
			diags = append(diags, Diagnostic{Line: line, Code: "unknown-char",
				Message: fmt.Sprintf("记法里没有这个字符 %q，已忽略（原脚本也是静默忽略）", string(c))})
			i++
		}
	}
	return items, diags
}

// splitNotation 把原子项归到记号上（**归属规则见文件头**）。
//
// 返回：记号序列；"夹在两个数字之间"的修饰符串个数（这才是真歧义）；诊断。
func splitNotation(items []scanItem, legacy bool) ([]notationToken, int, []Diagnostic) {
	var toks []notationToken
	var diags []Diagnostic
	pending := "" // 攒着给"后一个数字"的前缀（只在必要时用）
	ambiguous := 0

	hasHeadAfter := func(from int) bool {
		for k := from + 1; k < len(items); k++ {
			if items[k].head {
				return true
			}
		}
		return false
	}

	for idx, it := range items {
		if !it.head {
			prev := len(toks) > 0
			next := hasHeadAfter(idx)
			switch {
			case prev && next:
				// ⭐ 真歧义：夹在两个数字之间
				ambiguous++
				if legacy {
					pending += it.text // 原脚本读法：归后一个数字
				} else {
					toks[len(toks)-1].Mods += it.text // 修好的读法：归前一个数字
					toks[len(toks)-1].Raw += it.text
				}
			case prev:
				toks[len(toks)-1].Mods += it.text
				toks[len(toks)-1].Raw += it.text
			case next:
				pending += it.text
			default:
				diags = append(diags, Diagnostic{Code: "orphan-modifier",
					Message: fmt.Sprintf("修饰符 %q 两边都没有数字，作用不到任何音符", it.text)})
			}
			continue
		}
		toks = append(toks, notationToken{Head: it.text, Mods: pending, Raw: pending + it.text})
		pending = ""
	}
	if pending != "" {
		diags = append(diags, Diagnostic{Code: "orphan-modifier",
			Message: fmt.Sprintf("修饰符 %q 后面没有数字，作用不到任何音符", pending)})
	}
	return toks, ambiguous, diags
}

// applyMods 解析一个记号的修饰符，算出 (八度偏移, 半音偏移, 时值倍数)。照原脚本逐条 if。
func applyMods(mods string) (octave, semitone int, factor float64) {
	factor = 1
	for i := 0; i < len(mods); i++ {
		switch mods[i] {
		case 'q':
			octave++
		case 'w':
			octave--
		case '.':
			factor *= 1.5
		case '_':
			factor *= 0.5
		case '#':
			semitone++
		case 'b':
			semitone--
		}
	}
	return
}

// DegreeSemitone 返回简谱音级（1..7）相对主音的**半音偏移**。
// 导出它是为了让上层（ACETool 的工具）能把「音级 + 八度 + 变音」换算成 MIDI，
// 而不必在别处**抄一张同样的表** —— 两张表早晚会走岔。
func DegreeSemitone(degree int) (int, bool) {
	if degree < 1 || degree > 7 {
		return 0, false
	}
	return yinjie[degree], true
}

// DurationTicks 由 (时值分母, 附点数) 算 tick 数（PPQ 通常 480）。
// 分母口径与 `Element.Duration` 一致：4 = 四分音符、8 = 八分、16 = 十六分……
func DurationTicks(duration, dots int, ppq int64) int64 {
	if ppq <= 0 {
		ppq = 480
	}
	return int64(math.Round(durationBeats(duration, dots) * float64(ppq)))
}

// TicksToDuration 是 `DurationTicks` 的反函数：把 tick 数翻回 (分母, 附点数)。
// 第三个返回值是"是否精确"—— 简谱写不出来的时值（三连音之类）回 `false`，**调用方要如实报**。
//
// ⭐ 它是给"**ACE 音符 → 编辑器文档**"那条路用的：ACE 的 `dur` 是 tick，
// 而编辑器模型用的是简谱口径（几分音符 + 附点）。
func TicksToDuration(ticks int64, ppq int64) (int, int, bool) {
	if ppq <= 0 {
		ppq = 480
	}
	if ticks <= 0 {
		return 4, 0, false
	}
	bestD, bestDots := 4, 0
	bestDiff := math.Inf(1)
	for _, d := range []int{1, 2, 4, 8, 16, 32, 64} {
		for dots := 0; dots <= 3; dots++ {
			diff := math.Abs(float64(DurationTicks(d, dots, ppq) - ticks))
			if diff == 0 {
				return d, dots, true
			}
			if diff < bestDiff {
				bestD, bestDots, bestDiff = d, dots, diff
			}
		}
	}
	return bestD, bestDots, false
}

// ParseNotationElements 与 `ParseNotation` 用**同一套词法**，但产出**元素序列**
// （`KindNote` 音符 / `KindSustain` 的 `-` 延音），**不把 `-` 折进前一个音**。
//
// ⭐ 为什么要这一版（用户 2026-10-05 定：「**连音是 ACE 的 `-` 音符**」）：
// ACE 的官方口径（`help note-exclusivity` §"Note groups: syllables and tenutos"）是
//
//	一个"音符组" = 共享同一个音节的若干 sing 音符：一个 **头音符**（带歌词与语言）
//	+ 零个或多个 **tenuto 音符**（延续这个音节）。**tenuto 音符就写成歌词字段里的字面 `-`**。
//	例：四个音符，歌词 `la`、`-`、`-`、`-`，构成一个持续四个音符的音节。
//
// ⇒ 所以"折叠成长音"是**错的口径**：ACE 那边要的是**一个音一个音符、后续的歌词写 `-`**。
// 而 web 编辑器也要把 `-` 当**独立元素**编辑（画一根增时线、增删自然）。
//
// ⚠️ 折叠只发生在**另一个出口**：MusicXML（五线谱上简谱的 `1 -` 本来就等于一个更长的音符）。
// 两个出口各自正确，模型只保留一份。
func ParseNotationElements(text string, opt NotationOptions) ([]Element, []Diagnostic) {
	if opt.BaseMidi == 0 {
		opt.BaseMidi = 60
	}
	items, diags := scanNotation(text)
	toks, ambiguous, sd := splitNotation(items, opt.LegacyPrefixModifiers)
	diags = append(diags, sd...)
	if ambiguous > 0 && !opt.LegacyPrefixModifiers {
		diags = append(diags, Diagnostic{Code: "modifier-scope",
			Message: fmt.Sprintf("有 **%d** 处修饰符夹在两个数字之间，两种读法结果不同："+
				"按**修好的**读法归**前一个**数字（与简谱习惯一致，`1. 2` = 附点 1 + 2）；"+
				"按**原脚本**的读法归**后一个**数字（`1. 2` 在原脚本里是「1 不加点、2 加点」）。"+
				"**旧谱子若是按原脚本写的，请开 `LegacyPrefixModifiers` 切回去。**", ambiguous)})
	}

	out := make([]Element, 0, len(toks))
	for _, t := range toks {
		oct, semi, factor := applyMods(t.Mods)
		// 倍数 → (分母, 附点)。这套记法只有 ×1.5 与 ×0.5，所以能精确落成 4/8/16…+附点。
		dur, dots := factorToDuration(factor)
		switch t.Head {
		case "=", "-":
			// ⭐ 保留成**独立元素**（不折进前一个音）—— 见函数注释
			out = append(out, Element{Kind: KindSustain, Code: t.Raw, Duration: dur, Dots: dots, Octave: oct})
			continue
		case "0":
			// 休止：`Element` 里用 pitch 0 表达（与番茄 DSL 一致）
			out = append(out, Element{Kind: KindNote, Code: t.Raw, Rest: true, Duration: dur, Dots: dots})
			continue
		}
		deg := int(t.Head[0] - '0')
		if deg < 1 || deg > 7 {
			diags = append(diags, Diagnostic{Code: "bad-degree",
				Message: fmt.Sprintf("记法里没有度数 %q（只有 1-7）", t.Head)})
			continue
		}
		acc := AccidentalNone
		if semi > 0 {
			acc = AccidentalSharp
		} else if semi < 0 {
			acc = AccidentalFlat
		}
		out = append(out, Element{Kind: KindNote, Code: t.Raw, Pitch: deg, Octave: oct,
			Accidental: acc, Duration: dur, Dots: dots})
	}
	return out, diags
}

// factorToDuration 把"时值倍数"翻成 (分母, 附点数)：1→四分、0.5→八分、0.25→十六分、
// 1.5→四分带一个附点、0.75→八分带一个附点……（这套记法只有 `.`/`_`，所以一定能落进这个族）。
func factorToDuration(f float64) (int, int) {
	const eps = 1e-9
	// 先定基准分母（`_` 的个数决定砍半几次），再用附点补足
	for _, d := range []int{4, 8, 16, 32, 64} {
		for dots := 0; dots <= 3; dots++ {
			if math.Abs(durationBeats(d, dots)-f) < eps {
				return d, dots
			}
		}
	}
	// 落不进去（不该发生）：退到最接近的四分音符
	return 4, 0
}

// `-` / `=` 折进前一个块；`0` 占位但 `Midi = -1`（不产生要唱的音）。
//
// ⚠️ 与番茄简谱那套 `Parse` 一样：**永不 panic**，坏输入走诊断。
// ParseNotation 解析用户那套简洁记法 → **音符块**（已把 `-` 折进前一个音）。
//
// ⚠️ 需要"保留 `-` 为独立元素"的场合（喂 ACE、web 编辑器）请用 `ParseNotationElements` —— 见那里的说明。
func ParseNotation(text string, opt NotationOptions) ([]NoteBlock, []Diagnostic) {
	if opt.BaseMidi == 0 {
		opt.BaseMidi = 60
	}
	items, diags := scanNotation(text)
	toks, ambiguous, sd := splitNotation(items, opt.LegacyPrefixModifiers)
	diags = append(diags, sd...)

	// ⭐ 唯一改变行为的地方，**必须报出来**（聚合一条，别把长谱子刷屏）
	if ambiguous > 0 && !opt.LegacyPrefixModifiers {
		diags = append(diags, Diagnostic{Code: "modifier-scope",
			Message: fmt.Sprintf("有 **%d** 处修饰符夹在两个数字之间，两种读法结果不同："+
				"按**修好的**读法归**前一个**数字（与简谱习惯一致，`1. 2` = 附点 1 + 2）；"+
				"按**原脚本**的读法归**后一个**数字（原脚本扫的区间是 [上一个数字, 下一个数字)，再用区间末尾那个字决定音高 "+
				"⇒ `1. 2` 在原脚本里是「1 不加点、2 加点」）。"+
				"**旧谱子若是按原脚本写的，请开 `LegacyPrefixModifiers` 切回去。**", ambiguous)})
	}

	var blocks []NoteBlock
	extend := func(beats float64) bool {
		if len(blocks) == 0 {
			return false
		}
		last := &blocks[len(blocks)-1]
		last.Beats += beats
		last.Ticks = beats2ticks(last.Beats)
		return true
	}

	for _, t := range toks {
		_, semi, factor := applyMods(t.Mods)
		oct, _, _ := applyMods(t.Mods)
		switch t.Head {
		case "=":
			if !extend(factor) {
				diags = append(diags, Diagnostic{Code: "extend-without-note",
					Message: "`=` 的语义是「延长前一个音符」，但前面还没有音符"})
			}
			continue
		case "-":
			// ⛔ 原脚本会在这里凭空造一个主音音符 —— 不照抄
			if !extend(factor) {
				diags = append(diags, Diagnostic{Code: "sustain-without-note",
					Message: "`-`（延音线）前面没有音符可延长（原脚本会在这种地方凭空造一个主音音符）"})
			}
			continue
		case "0":
			blocks = append(blocks, NoteBlock{
				Degree: 0, Midi: -1, Beats: factor, Ticks: beats2ticks(factor),
				Hidden: true, Code: t.Raw, ElementIndex: len(blocks),
			})
			continue
		}
		deg := int(t.Head[0] - '0')
		if deg < 1 || deg > 7 {
			diags = append(diags, Diagnostic{Code: "bad-degree",
				Message: fmt.Sprintf("记法里没有度数 %q（只有 1-7）", t.Head)})
			continue
		}
		midi := opt.BaseMidi + opt.KeyIndex + yinjie[deg] + 12*oct + semi
		acc := Accidental("")
		if semi > 0 {
			acc = "sharp"
		} else if semi < 0 {
			acc = "flat"
		}
		blocks = append(blocks, NoteBlock{
			Degree: deg, Octave: oct, Accidental: acc, Midi: midi,
			Beats: factor, Ticks: beats2ticks(factor),
			Lyric: opt.DefaultLyric, Code: t.Raw, ElementIndex: len(blocks),
		})
	}
	return blocks, diags
}

func beats2ticks(beats float64) int64 {
	return int64(beats*480 + 0.5)
}

// notationDuration 把拍数翻成「修饰符 + 几根增时线 `-`」。
//
// ⚠️ 这套记法里一个记号的**基准就是 1 拍**，长音只能靠 `-`（每根一拍），
// 分数只能靠 `.`(×1.5) / `_`(×0.5) ⇒ 能表达的是 `q·(1) + n·(1)`。
// 取**满足"余数是非负整数"的最大 q**（这样增时线最少、也最符合习惯：2 拍写 `2 -`、2.5 拍写 `2. -`）。
func notationDuration(beats float64) (mods string, dashes int, exact bool) {
	const eps = 1e-9
	if beats <= 0 {
		return "", 0, false
	}
	type cand struct {
		v    float64
		mods string
	}
	var cands []cand
	for dots := 0; dots <= 3; dots++ {
		for halves := 0; halves <= 3; halves++ {
			v := 1.0
			for k := 0; k < dots; k++ {
				v *= 1.5
			}
			for k := 0; k < halves; k++ {
				v *= 0.5
			}
			cands = append(cands, cand{v, strings.Repeat(".", dots) + strings.Repeat("_", halves)})
		}
	}
	bestMods, bestDashes := "", -1
	bestV := -1.0
	bestDiff := math.Inf(1)
	for _, c := range cands {
		rem := beats - c.v
		n := math.Round(rem)
		if n < 0 {
			continue
		}
		diff := math.Abs(rem - n)
		// 优先：精确优先 → q 更大（增时线更少）
		if diff < bestDiff-eps || (diff < eps && bestDiff < eps && c.v > bestV) {
			if diff < bestDiff+eps {
				bestDiff = diff
				bestMods, bestDashes, bestV = c.mods, int(n), c.v
			}
		}
	}
	if bestDashes < 0 {
		return "", 0, false
	}
	return bestMods, bestDashes, bestDiff < eps
}

// WriteNotation 音符块 → 用户那套简洁记法（反向）。
//
// 记法里的编号 = 度数 + 修饰符，所以先按调式把 MIDI 折成度数
// （复用 `pitchToDegree`，与番茄那套 `Write` 同一套拆音算法 —— 两套记法的**音乐语义**一样，只是写法不同）。
//
// ⚠️ 写不出来的情况如实报（与 `Write` 同一条纪律）：时值不在这套记法能表达的家族里（如三连音）
// ⇒ `unrepresentable-duration`；没有音高 ⇒ `no-pitch` 并写成 `0`。
func WriteNotation(blocks []NoteBlock, opt NotationOptions) (string, []Diagnostic) {
	if opt.BaseMidi == 0 {
		opt.BaseMidi = 60
	}
	if opt.Meter.Numerator == 0 {
		opt.Meter = Meter{Numerator: 4, Denominator: 4}
	}
	var diags []Diagnostic
	var out []string
	beatsPerMeasure := 4.0 * float64(opt.Meter.Numerator) / float64(opt.Meter.Denominator)
	if beatsPerMeasure <= 0 {
		beatsPerMeasure = 4
	}
	acc := 0.0

	for i, b := range blocks {
		var sb strings.Builder
		switch {
		case b.Degree == 9:
			sb.WriteString("9") // 节奏音
		case b.Degree == 8 || (b.Hidden && b.Midi >= 0):
			sb.WriteString("8") // 隐藏音（占时值但不唱）
		// ⛔ 判休止**只能看 `Midi < 0`，不能看 `Degree == 0`**：
		// 从 ACE 音符构造的块（`notesToBlocks`）**根本没设 `Degree`**（只有 `Midi`），
		// 而 `Degree` 是 int、零值就是 0 ⇒ 整段旋律会被写成一片 `0`。
		// 这个 bug 是「ACE 音符 → 简谱」那条测试逮住的（产出 `"0 0 |"`）。
		case b.Midi < 0:
			sb.WriteString("0")
			if !b.Hidden {
				diags = append(diags, Diagnostic{Line: i, Code: "no-pitch",
					Message: fmt.Sprintf("第 %d 个音符块没有音高，写成了休止 `0`", i+1)})
			}
		default:
			deg, oct, accd := pitchToDegree(b.Midi, opt.KeyIndex, opt.BaseMidi, false)
			marks := ""
			for k := 0; k < oct; k++ {
				marks += "q"
			}
			for k := 0; k > oct; k-- {
				marks += "w"
			}
			if accd == "sharp" {
				marks += "#"
			} else if accd == "flat" {
				marks += "b"
			}
			sb.WriteString(fmt.Sprintf("%d%s", deg, marks))
		}
		mods, dashes, exact := notationDuration(b.Beats)
		if !exact {
			diags = append(diags, Diagnostic{Line: i, Code: "unrepresentable-duration",
				Message: fmt.Sprintf("第 %d 个音符块：%g 拍在这套记法里写不出来（只有 `.`(×1.5)、`_`(×0.5) 与增时线 `-`，"+
					"三连音之类表达不了）—— 已退到最近写法", i+1, b.Beats)})
		}
		sb.WriteString(mods)
		out = append(out, sb.String())
		for d := 0; d < dashes; d++ {
			out = append(out, "-")
		}

		acc += b.Beats
		if acc >= beatsPerMeasure-1e-9 {
			out = append(out, "|")
			acc = 0
		}
	}
	if len(out) > 0 && out[len(out)-1] != "|" {
		out = append(out, "|")
	}
	return strings.Join(out, " "), diags
}
