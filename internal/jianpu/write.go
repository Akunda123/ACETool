package jianpu

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// 音符块 → **谱面**（简谱文本）。这是解析的反方向，两支合起来就是"简谱 ⇄ 音符块"的双向桥。
//
// 三条必须说清的语义：
//
//  1. **写音级要先知道调式**。简谱的 `1` 是"主音"，没有调式就没法把 MIDI 变成数字。
//     `TonicPitchClass` 由调用方给（ACETool 那侧用自动检测，或用户指定）。
//
//  2. **变音有两种拼法，必须选一个**：C 大调里 MIDI 10（A#/Bb）既能写 `#6` 也能写 `$7`。
//     规则：**优先往上够**（`#`），除非调式本身是降号调（F/Bb/Eb/Ab/Db/Gb 那一圈）——
//     那时更习惯写 `$`。可用 `PreferFlat` 覆盖。**这是约定不是真理**，所以做成显式选项。
//
//  3. **时值不是所有值都写得出来**。简谱靠「减时线 `/`（砍半）+ 附点 `.`（×1.5 / ×1.75）+ 增时线 `-`（每根一拍）」表达，
//     能精确表示的只有 `4/D × (2 − 2^-dots)` 这一族加整数拍。
//     ⚠️ **三连音 / 五连音这类写不出来** ⇒ 如实报诊断（`unrepresentable-duration`）并退到最近的可表示值，
//     **绝不假装写对了**。与全项目同一条纪律：宁可说不出来，不要悄悄写错。
//
// ⚠️ 一个采集自真谱子的写法细节（往返必须两边同意）：
// **八度记号与变音记号的先后顺序，两种都出现过** ——
// 测试里是 `1'#`（八度在前），而 `test.jps` 里有 `5#,`（变音在前）。
// 所以解析器要**按修饰簇整体吃**（顺序无关），这里则统一写成 `1'#`（与测试一致）。

// WriteOptions 控制音符块 → 简谱文本。
type WriteOptions struct {
	// TonicPitchClass 主音音级 0..11（C=0）。简谱 `1` 就是它。<0 时报诊断（写不出音级）。
	TonicPitchClass int
	// PreferFlat 变音优先写降号（降号调更习惯这么写）。
	PreferFlat bool
	// Meter 拍号，用来放小节线。
	Meter Meter
	// BaseMidi 简谱「无八度点」的 `1` 对应哪个 MIDI（默认 60 = C4）。
	// ⚠️ 必须与 `Blocks` 的同一项一致，否则往返会差八度。
	BaseMidi int

	// 头部（都可空）
	Version string
	Titles  []string
	Authors []string
	// Mode 直接写进 `D:`（如 "C" / "F#"）；空则从 TonicPitchClass 推。
	Mode string
	// Voice 写进 `Q<n>:` 的声部号（默认 1）。
	Voice int
	// Caption 写进 `Q1"…":` 的引号（可空）。
	Caption string
	// SuppressLyrics=true 时不输出 `C<n>:` 歌词行。
	//
	// ⚠️ 这里刻意用**否定式**而不是 `EmitLyrics bool`：Go 的 bool 零值是 `false`，
	// 而 `WriteOptions{TonicPitchClass: 0, BaseMidi: 60, Meter: …}` 这种写法不会去设它 ——
	// 用肯定式就等于"默认不写歌词"，往返时歌词会静默丢掉（这个坑被子代理的对拍抓到了：
	// 5 条往返用例全栽在同一处）。**零值必须是那个合理的默认**。
	SuppressLyrics bool
}

// majorScale 是大调音阶相对主音的半音偏移（简谱 1..7 = do..si）。
var majorScale = [7]int{0, 2, 4, 5, 7, 9, 11}

// flatPitchClasses 是"降号调"的主音音级（F Bb Eb Ab Db Gb）—— 决定变音默认拼法。
var flatPitchClasses = map[int]bool{5: true, 10: true, 3: true, 8: true, 1: true, 6: true}

// pitchToDegree 把一个 MIDI 音高拆成「简谱音级 + 八度 + 变音」。
func pitchToDegree(midi, tonicPC, baseMidi int, preferFlat bool) (int, int, Accidental) {
	base := baseMidi + tonicPC
	rel := midi - base
	oct := int(math.Floor(float64(rel) / 12))
	r := ((rel % 12) + 12) % 12

	for i, s := range majorScale {
		if r == s {
			return i + 1, oct, ""
		}
	}
	upIdx, downIdx := -1, -1
	for i, s := range majorScale {
		if s == (r-1+12)%12 {
			upIdx = i
		}
		if s == (r+1)%12 {
			downIdx = i
		}
	}
	switch {
	case upIdx >= 0 && downIdx < 0:
		return upIdx + 1, oct, "sharp"
	case downIdx >= 0 && upIdx < 0:
		return downIdx + 1, oct, "flat"
	case upIdx >= 0 && downIdx >= 0:
		if preferFlat || flatPitchClasses[tonicPC] {
			return downIdx + 1, oct, "flat"
		}
		return upIdx + 1, oct, "sharp"
	default:
		return 1, oct, ""
	}
}

// MidiToDegree 是 pitchToDegree 的**导出壳**（给「音符 → 编辑器文档」用：
// ACE 的音符只有 MIDI 音高，编辑器要的是音级 + 八度 + 变音）。
//
// preferFlat=false 时两可的音按调号定拼法（与写出器同一套规则）⇒ 读进来再写出去不会忽然多出变音号。
func MidiToDegree(midi, tonicPC, baseMidi int) (int, int, Accidental) {
	return pitchToDegree(midi, tonicPC, baseMidi, false)
}

// durationBeats 由分母与附点数算拍数（以四分音符为 1 拍）。
// ⚠️ 必须与 `Blocks` 的算法完全一致，否则往返会漂。
func durationBeats(duration, dots int) float64 {
	if duration <= 0 {
		return 1
	}
	return 4.0 / float64(duration) * (2 - math.Pow(2, -float64(dots)))
}

// beatsToCode 把"拍数"翻成简谱时值写法。
//
//	整数拍 ≥1 ⇒ 裸音符 + (拍数−1) 根增时线 `-`
//	分数拍    ⇒ 在「减时线 × 附点」族里找精确解（0.25 / 0.5 / 0.75 / 1.5 / 1.75 …）
//
// 返回：写在音级后面的修饰串、要追加几根增时线、是否有精确解。
func beatsToCode(beats float64) (suffix string, dashes int, exact bool) {
	const eps = 1e-9
	if beats < eps {
		return "", 0, false
	}
	if math.Abs(beats-math.Round(beats)) < eps && beats >= 1-eps {
		n := int(math.Round(beats))
		return "", n - 1, true
	}
	slashDot := func(d, dots int) string {
		slashes := 0
		for x := d; x > 4; x /= 2 {
			slashes++
		}
		return strings.Repeat("/", slashes) + strings.Repeat(".", dots)
	}
	for _, d := range []int{4, 8, 16, 32, 64} {
		for dots := 0; dots <= 3; dots++ {
			if math.Abs(durationBeats(d, dots)-beats) < eps {
				return slashDot(d, dots), 0, true
			}
		}
	}
	// 写不出来：退到最近的可表示值（并让调用方报诊断）
	best, bestDiff := "", math.Inf(1)
	for _, d := range []int{4, 8, 16, 32} {
		for dots := 0; dots <= 2; dots++ {
			if diff := math.Abs(durationBeats(d, dots) - beats); diff < bestDiff {
				best, bestDiff = slashDot(d, dots), diff
			}
		}
	}
	return best, 0, false
}

func accidentalMark(a Accidental) string {
	switch a {
	case "sharp":
		return "#"
	case "flat":
		return "$"
	}
	return ""
}

func octaveMarks(oct int) string {
	if oct < 0 {
		return strings.Repeat(",", -oct)
	}
	return strings.Repeat("'", oct)
}

func modeFromTonic(pc int) string {
	names := []string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}
	if pc < 0 || pc > 11 {
		return ""
	}
	return names[pc]
}

// Write 把音符块写成简谱文本（番茄简谱 DSL）。
//
// ⭐ 与 `Parse` 构成往返：`Blocks(Parse(t))` → `Write` → `Parse` → `Blocks` 应当得到同一串音符块。
// 写出来的文本刻意**只用两边都支持的那部分语法**（音符·时值·延音·小节线·歌词），
// 这样往返不会因为"写法选择不同"而误判成不一致。
//
// ⚠️ 歌词的对位口径：**一个音符一个 token**（没歌词写 `@`），**增时线不占 token**。
// 真谱子里 `C:` 行并不严格与音符一一对应（`test.jps` 里 `1, - - -` 配 `呜@呜`），
// 但那是手写习惯；机器写出来的取"一音一 token"这个规整口径。
func Write(blocks []NoteBlock, opt WriteOptions) (string, []Diagnostic) {
	var diags []Diagnostic
	if opt.BaseMidi == 0 {
		opt.BaseMidi = 60
	}
	if opt.TonicPitchClass < 0 {
		diags = append(diags, Diagnostic{Code: "no-tonic",
			Message: "没有主音音级 ⇒ 写不出简谱音级（简谱的 1 就是主音）。请指定调式，或用自动检测"})
	}
	if opt.Voice == 0 {
		opt.Voice = 1
	}
	if opt.Meter.Numerator == 0 {
		opt.Meter = Meter{Numerator: 4, Denominator: 4}
	}

	var b strings.Builder
	if opt.Version != "" {
		fmt.Fprintf(&b, "V: %s\n", opt.Version)
	}
	for _, t := range opt.Titles {
		fmt.Fprintf(&b, "B: %s\n", t)
	}
	for _, a := range opt.Authors {
		fmt.Fprintf(&b, "Z: %s\n", a)
	}
	mode := opt.Mode
	if mode == "" {
		mode = modeFromTonic(opt.TonicPitchClass)
	}
	if mode != "" {
		fmt.Fprintf(&b, "D: %s\n", mode)
	}
	fmt.Fprintf(&b, "P: %d/%d\n", opt.Meter.Numerator, opt.Meter.Denominator)

	var nums, lyrics []string
	beatsPerMeasure := 4.0 * float64(opt.Meter.Numerator) / float64(opt.Meter.Denominator)
	if beatsPerMeasure <= 0 {
		beatsPerMeasure = 4
	}
	acc := 0.0

	sorted := append([]NoteBlock(nil), blocks...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Measure != sorted[j].Measure {
			return sorted[i].Measure < sorted[j].Measure
		}
		return sorted[i].Beat < sorted[j].Beat
	})

	for i, nb := range sorted {
		var code string
		switch {
		case nb.Hidden || nb.Degree == 8:
			code = "8" // 隐藏音（番茄 DSL 的专用写法）
		case nb.Degree == 9:
			code = "9" // 节奏音
		// ⛔ 同 notation.go：判休止**只能看 `Midi < 0`**，不能看 `Degree == 0` ——
		// 从 ACE 音符构造的块不设 `Degree`（int 零值也是 0），看它会把整段旋律写成一片 `0`。
		case nb.Midi < 0:
			code = "0" // 休止
		case nb.Midi >= 0 && opt.TonicPitchClass >= 0:
			deg, oct, accd := pitchToDegree(nb.Midi, opt.TonicPitchClass, opt.BaseMidi, opt.PreferFlat)
			// ⚠️ 顺序：音级 → 八度 → 变音（与 parser 用例 `1'#` 一致）
			code = fmt.Sprintf("%d%s%s", deg, octaveMarks(oct), accidentalMark(accd))
			if nb.Accidental != "" && accd != "" && nb.Accidental != accd {
				diags = append(diags, Diagnostic{Line: i, Code: "accidental-spelling",
					Message: fmt.Sprintf("第 %d 个音符块：原变音是 %s，但按这个调式写成了 %s（简谱的拼法由调式决定）",
						i+1, nb.Accidental, accd)})
			}
		default:
			deg := nb.Degree
			if deg == 0 {
				deg = 1
			}
			code = fmt.Sprintf("%d%s", deg, octaveMarks(nb.Octave))
		}

		suffix, dashes, exact := beatsToCode(nb.Beats)
		if !exact {
			diags = append(diags, Diagnostic{Line: i, Code: "unrepresentable-duration",
				Message: fmt.Sprintf("第 %d 个音符块：%g 拍在简谱里**写不出来**（减时线×附点×增时线只能表达特定几档，"+
					"三连音之类不行）—— 已退到最近的可表示写法 %q，请人工核对", i+1, nb.Beats, code+suffix)})
		}
		nums = append(nums, code+suffix)
		for d := 0; d < dashes; d++ {
			nums = append(nums, "-")
		}
		if nb.Lyric == "" {
			lyrics = append(lyrics, "@")
		} else {
			lyrics = append(lyrics, nb.Lyric)
		}

		acc += nb.Beats
		if acc >= beatsPerMeasure-1e-9 {
			nums = append(nums, "|")
			acc = 0
		}
	}
	if len(nums) > 0 && nums[len(nums)-1] != "|" {
		nums = append(nums, "|")
	}

	head := fmt.Sprintf("Q%d", opt.Voice)
	if opt.Caption != "" {
		head += fmt.Sprintf("%q", opt.Caption)
	}
	fmt.Fprintf(&b, "%s: %s\n", head, strings.Join(nums, " "))
	if !opt.SuppressLyrics && len(lyrics) > 0 {
		fmt.Fprintf(&b, "C%d: %s\n", opt.Voice, strings.Join(lyrics, " "))
	}
	return b.String(), diags
}
