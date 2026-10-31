package jianpu

import (
	"math"
	"strings"
	"testing"
)

// ⭐ 双向化之后**最强的验收**：谱面 → 音符块 → 谱面 → 音符块，两遍必须一致。
//
// ⚠️ 关键设计：**不能拿整个 NoteBlock 比**。往返会把写法规范化（时值写成统一的减时线/附点/增时线组合、
// 变音按调式重选拼法），所以 `Code`（源码原样）与 `ElementIndex`（元素序号）**必然变**；
// 拿它们比等于在测"写出来的文本与原文逐字相同"，那不是我们承诺的性质。
//
// 承诺的是：**音乐内容不变** —— 声部、音级、八度、变音、MIDI、拍数、小节、歌词。
// 所以比的是这个**投影**。投影里刻意**没有** Code / ElementIndex / Ornaments / Beat（位置会因增时线拆分而变）。
type blockKey struct {
	Voice      int
	Degree     int
	Octave     int
	Accidental Accidental
	Midi       int
	Beats      float64
	Measure    int
	Lyric      string
	Hidden     bool
}

func project(bs []NoteBlock) []blockKey {
	out := make([]blockKey, 0, len(bs))
	for _, b := range bs {
		out = append(out, blockKey{
			Voice: b.Voice, Degree: b.Degree, Octave: b.Octave, Accidental: b.Accidental,
			Midi: b.Midi, Beats: math.Round(b.Beats*1e6) / 1e6, Measure: b.Measure,
			Lyric: b.Lyric, Hidden: b.Hidden,
		})
	}
	return out
}

func sameProjection(a, b []blockKey) (int, string) {
	if len(a) != len(b) {
		return -1, "长度不同"
	}
	for i := range a {
		if a[i] != b[i] {
			return i, "字段不同"
		}
	}
	return -1, ""
}

// roundTrip 跑一遍 `Parse → Blocks → Write → Parse → Blocks`。
func roundTrip(t *testing.T, dsl string) ([]NoteBlock, []NoteBlock, *Document) {
	t.Helper()
	d1 := Parse(dsl)
	b1 := Blocks(d1, BlockOptions{TonicPitchClass: 0}) // C 大调
	if len(b1) == 0 {
		t.Fatalf("第一次就没解析出音符块 —— 输入：\n%s\n诊断：%+v", dsl, d1.Diagnostics)
	}
	text, _ := Write(b1, WriteOptions{TonicPitchClass: 0, BaseMidi: 60, Meter: Meter{Numerator: 4, Denominator: 4}})
	d2 := Parse(text)
	b2 := Blocks(d2, BlockOptions{TonicPitchClass: 0})
	return b1, b2, d2
}

func TestRoundTripSynthetic(t *testing.T) {
	// 规整输入：一音一词、时值都在可表示族里 ⇒ 投影应当逐字段相等
	for _, c := range []struct {
		name string
		dsl  string
	}{
		{"裸四分音符", "D: C\nP: 4/4\nQ1: 1 2 3 4 |\nC1: 甲乙丙丁"},
		{"八度与变音", "D: C\nP: 4/4\nQ1: 1 2, 3' 4 |\nC1: 甲乙丙丁"},
		{"减时线与附点", "D: C\nP: 4/4\nQ1: 1/ 2// 3. 4 |\nC1: 甲乙丙丁"},
		{"增时线折进前一个音", "D: C\nP: 4/4\nQ1: 1 - - - |\nC1: 甲"},
		{"休止", "D: C\nP: 4/4\nQ1: 0 1 0 1 |\nC1: 甲乙丙丁"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b1, b2, d2 := roundTrip(t, c.dsl)
			if i, why := sameProjection(project(b1), project(b2)); i >= 0 {
				t.Errorf("往返投影不一致（%s，第 %d 个）：\n  第一遍 %+v\n  第二遍 %+v\n  重解析诊断 %+v",
					why, i, project(b1), project(b2), d2.Diagnostics)
			}
		})
	}
}

// ⭐ 用**真谱子片段**（`test.jps` 的开头几小节）跑往返 —— 只比音乐内容，不比写法。
// 这段有：多声部、八度逗号、连音括号、附点、增时线、歌词 `@` 占位。
func TestRoundTripRealFragment(t *testing.T) {
	const frag = `D: C
P: 4/4
Q1: 0/ 5,/ | (2/ 3//) 3// 0 2/ 3/ 5/ 3/ | 0 0 0 0/ 5,/ |
C1: @我@在@清晨眺望@@@@我
`
	b1, b2, d2 := roundTrip(t, frag)
	if len(b1) < 8 {
		t.Fatalf("真谱片段应当解析出 8 个以上音符块，实得 %d（诊断 %+v）", len(b1), Parse(frag).Diagnostics)
	}
	// 真谱片的歌词对位本身不规整 ⇒ 这里**只比音高与拍数**（歌词单独看）
	type key struct {
		Midi  int
		Beats float64
	}
	proj := func(bs []NoteBlock) []key {
		out := make([]key, 0, len(bs))
		for _, b := range bs {
			out = append(out, key{b.Midi, math.Round(b.Beats*1e6) / 1e6})
		}
		return out
	}
	p1, p2 := proj(b1), proj(b2)
	if len(p1) != len(p2) {
		t.Fatalf("往返后音符块数变了：%d → %d\n第一遍 %+v\n第二遍 %+v\n重解析诊断 %+v",
			len(p1), len(p2), p1, p2, d2.Diagnostics)
	}
	for i := range p1 {
		if p1[i] != p2[i] {
			t.Errorf("第 %d 个音符块音高/拍数不一致：%+v → %+v", i, p1[i], p2[i])
		}
	}
}

// 时值族的两个方向必须互为逆（`Blocks` 与 `Write` 共用同一套算法，这里把纯函数钉住）。
func TestDurationBeatsAndCodeAreInverse(t *testing.T) {
	for _, beats := range []float64{0.25, 0.5, 0.75, 1, 1.5, 1.75, 2, 3, 4} {
		suffix, dashes, exact := beatsToCode(beats)
		if !exact {
			t.Errorf("%g 拍应当能精确表示，实得 suffix=%q dashes=%d", beats, suffix, dashes)
			continue
		}
		// 把后缀反推回分母/附点，再算回拍数；增时线每根加一拍
		got := float64(dashes)
		if suffix != "" {
			slashes := strings.Count(suffix, "/")
			dots := strings.Count(suffix, ".")
			d := 4
			for i := 0; i < slashes; i++ {
				d *= 2
			}
			got += durationBeats(d, dots)
		} else {
			got += 1
		}
		if math.Abs(got-beats) > 1e-9 {
			t.Errorf("%g 拍往返成 %g（suffix=%q dashes=%d）", beats, got, suffix, dashes)
		}
	}
}

// ⚠️ 三连音这类简谱写不出来的时值，**必须报诊断**、退到最近值，绝不假装写对。
func TestUnrepresentableDurationIsReported(t *testing.T) {
	third := 4.0 / 3.0 // 一拍三连音
	if _, _, exact := beatsToCode(third); exact {
		t.Fatal("1/3 拍在简谱里写不出来，不该报精确")
	}
	_, diags := Write([]NoteBlock{{Degree: 1, Midi: 60, Beats: third, Measure: 1, Beat: 1}},
		WriteOptions{TonicPitchClass: 0, Meter: Meter{Numerator: 4, Denominator: 4}})
	found := false
	for _, d := range diags {
		if d.Code == "unrepresentable-duration" {
			found = true
		}
	}
	if !found {
		t.Errorf("三连音时值必须报 unrepresentable-duration，实得诊断 %+v", diags)
	}
}

// 没有主音就写不出音级 —— 要**明确报出来**，而不是悄悄按 1 写。
func TestWriteWithoutTonicReports(t *testing.T) {
	_, diags := Write([]NoteBlock{{Degree: 1, Midi: 60, Beats: 1, Measure: 1, Beat: 1}},
		WriteOptions{TonicPitchClass: -1, Meter: Meter{Numerator: 4, Denominator: 4}})
	found := false
	for _, d := range diags {
		if d.Code == "no-tonic" {
			found = true
		}
	}
	if !found {
		t.Errorf("没有主音时必须报 no-tonic，实得 %+v", diags)
	}
}

// 变音拼法：同一批 MIDI 在不同调式/偏好下应写成不同记号，但**都是同一个音**。
func TestAccidentalSpelling(t *testing.T) {
	// C 大调里 70（A#/Bb）：默认写 `#6`，降号调偏好写 `$7`
	deg, _, acc := pitchToDegree(70, 0, 60, false)
	if deg != 6 || acc != "sharp" {
		t.Errorf("C 大调里 70 默认应当是 6#（A#），实得 %d%s", deg, accidentalMark(acc))
	}
	deg, _, acc = pitchToDegree(70, 0, 60, true)
	if deg != 7 || acc != "flat" {
		t.Errorf("要求降号拼法时 70 应当是 7$（Bb），实得 %d%s", deg, accidentalMark(acc))
	}
	// ⚠️ 我原来这里写错了：F 大调（主音 5）里 MIDI 70 **就是 Bb，是调内音**
	//（F 大调音阶 = F G A Bb C D E）⇒ `deg=4, acc=""` 才是对的，不该期望降号。
	// 换一个**真的两可**的音：MIDI 71 = B♮，在 F 大调里既能写 `#4`（升到 fa 位）也能写 `$5`（Cb）。
	deg, _, acc = pitchToDegree(71, 5, 60, false)
	if acc != "flat" {
		t.Errorf("降号调（F）里 71 的两可拼法应当默认取降号（$5 而不是 #4），实得 %d%s", deg, accidentalMark(acc))
	}
	if deg != 5 {
		t.Errorf("降号拼法应当是 5$（Cb），实得 %d", deg)
	}
	// 同一个音在升号调里默认取升号。
	// ⚠️ 又算错一次：C 大调里 MIDI 71 是 B♮ = si(7)，**调内音**，根本没有两可。
	// 真正两可的是变化音：61 = C#/Db ⇒ `#1` 或 `$2`。
	deg, _, acc = pitchToDegree(61, 0, 60, false) // C 大调
	if acc != "sharp" || deg != 1 {
		t.Errorf("C 大调里 61 应当是 1#（C#），实得 %d%s", deg, accidentalMark(acc))
	}
	deg, _, acc = pitchToDegree(61, 0, 60, true) // 要求降号拼法
	if acc != "flat" || deg != 2 {
		t.Errorf("要求降号拼法时 61 应当是 2$（Db），实得 %d%s", deg, accidentalMark(acc))
	}
}
