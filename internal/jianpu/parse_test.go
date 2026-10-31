package jianpu

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// 这个文件把上游 `packages/core/tests/parser.test.ts`（**语义规格**，22 个 it）逐条翻成 Go 测试。
//
// 用例名后面的注释写着它对应上游哪一个 it。
// 本包固定 API **表达不了**的那几条，用 `t.Skip` 明确标出来并写清原因 ——
// 这样 `go test` 保持全绿，同时"哪几条没对齐"在 `-v` 下一眼能看见（详见最终回报）。

// ---------- 小工具 ----------

func mustParse(t *testing.T, dsl string) *Document {
	t.Helper()
	return Parse(dsl)
}

func firstVoice(t *testing.T, doc *Document) *Voice {
	t.Helper()
	if len(doc.Pages) == 0 || len(doc.Pages[0].Groups) == 0 || len(doc.Pages[0].Groups[0].Voices) == 0 {
		t.Fatalf("文档里没有第一个声部：%+v", doc)
	}
	return &doc.Pages[0].Groups[0].Voices[0]
}

func diagCodes(doc *Document) []string {
	var out []string
	for _, d := range doc.Diagnostics {
		out = append(out, d.Code)
	}
	return out
}

func codesOf(v *Voice) []string {
	var out []string
	for _, e := range v.Elements {
		out = append(out, e.Code)
	}
	return out
}

func barlineTypes(v *Voice) []string {
	var out []string
	for _, e := range v.Elements {
		if e.Kind == KindBarline {
			out = append(out, e.Type)
		}
	}
	return out
}

func eqStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("%s = %q，期望 %q", what, got, want)
	}
}

// ---------- 上游 it 1 ----------

func TestParseHeadersPagesVoiceGroupsAndLyrics(t *testing.T) {
	doc := mustParse(t, `
V: 1.0
B: 主标题
B: 副标题
Z: 甲
D: F#
P: 6/8 (2/4)
J: 96
Q1"主旋律": 1 2 | 3 4 |
C1: 春~天/hello/world
Q2"低声部": 5, 6, | 7, 1 |
C2: @低@@
[fenye]
Q: 1 - ||
`)

	m := doc.Metadata
	if m.Version != "1.0" {
		t.Errorf("version = %q，期望 1.0", m.Version)
	}
	eqStrings(t, "titles", m.Titles, []string{"主标题", "副标题"})
	eqStrings(t, "authors", m.Authors, []string{"甲"})
	if m.Mode != "F#" {
		t.Errorf("mode = %q，期望 F#", m.Mode)
	}
	if len(m.Meters) != 2 {
		t.Fatalf("meters = %+v，期望 2 个", m.Meters)
	}
	if m.Meters[0] != (Meter{Numerator: 6, Denominator: 8}) {
		t.Errorf("meters[0] = %+v", m.Meters[0])
	}
	if m.Meters[1] != (Meter{Numerator: 2, Denominator: 4, Parenthesized: true}) {
		t.Errorf("meters[1] = %+v", m.Meters[1])
	}
	if len(m.Tempos) != 1 || m.Tempos[0] != float64(96) {
		t.Errorf("tempos = %#v，期望 [96]", m.Tempos)
	}

	if len(doc.Pages) != 2 {
		t.Fatalf("pages 应当 2 页，实得 %d", len(doc.Pages))
	}
	g0 := doc.Pages[0].Groups[0]
	eqStrings(t, "第一组的声音号", []string{itoa(g0.Voices[0].Voice), itoa(g0.Voices[1].Voice)}, []string{"1", "2"})
	if g0.Voices[0].Caption != "主旋律" {
		t.Errorf("caption = %q，期望 主旋律", g0.Voices[0].Caption)
	}
	var texts []string
	for _, s := range g0.Voices[0].Lyrics[0].Syllables {
		texts = append(texts, s.Text)
	}
	eqStrings(t, "C1 的音节", texts, []string{"春天", "hello", "world"})
	if len(doc.Diagnostics) != 0 {
		t.Errorf("不该有诊断：%+v", doc.Diagnostics)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// ---------- 上游 it 2 ----------

func TestGroupsAuxiliaryMetersAndHeaderPrecedence(t *testing.T) {
	doc := mustParse(t, `
B: 标题
P: 4/4
P: 3/4 ( 2/4 1/4 )
J: 80
J: 120
J: 欢快地
J: 抒情地
Q: 1 |
`)
	want := []Meter{
		{Numerator: 3, Denominator: 4},
		{Numerator: 2, Denominator: 4, Parenthesized: true},
		{Numerator: 1, Denominator: 4, Parenthesized: true},
	}
	if len(doc.Metadata.Meters) != len(want) {
		t.Fatalf("meters = %+v，期望 %+v", doc.Metadata.Meters, want)
	}
	for i := range want {
		if doc.Metadata.Meters[i] != want[i] {
			t.Errorf("meters[%d] = %+v，期望 %+v", i, doc.Metadata.Meters[i], want[i])
		}
	}
	if len(doc.Metadata.Tempos) != 2 || doc.Metadata.Tempos[0] != float64(80) || doc.Metadata.Tempos[1] != "欢快地" {
		t.Errorf("tempos = %#v，期望 [80 欢快地]", doc.Metadata.Tempos)
	}
	if len(doc.Diagnostics) != 0 {
		t.Errorf("不该有诊断：%+v", doc.Diagnostics)
	}
}

// ---------- 上游 it 3 ----------

func TestInvalidRepeatedMeterClearsPrevious(t *testing.T) {
	doc := mustParse(t, "P: 4/4\nP: invalid\nQ: 1 |")
	if len(doc.Metadata.Meters) != 0 {
		t.Errorf("meters 应当被清空，实得 %+v", doc.Metadata.Meters)
	}
	found := false
	for _, c := range diagCodes(doc) {
		if c == DiagInvalidMeter {
			found = true
		}
	}
	if !found {
		t.Errorf("应当有 invalid-meter，实得 %v", diagCodes(doc))
	}
}

// ---------- 上游 it 4（四种变音写法） ----------

func TestAcceptsFourAccidentalModeSpellings(t *testing.T) {
	for _, mode := range []string{"#C", "$B", "C#", "B$"} {
		doc := mustParse(t, "D: "+mode+"\nQ: 1 |")
		if doc.Metadata.Mode != mode {
			t.Errorf("D: %s ⇒ mode = %q，期望原样 %q", mode, doc.Metadata.Mode, mode)
		}
		if len(doc.Diagnostics) != 0 {
			t.Errorf("D: %s 不该有诊断：%+v", mode, doc.Diagnostics)
		}
	}
}

// ---------- 上游 it 5 ----------

func TestBarlineAnnotationCodeIsNormalized(t *testing.T) {
	doc := mustParse(t, `Q: 1 |"p:2 / 4" 2 |"foo bar" 3 |`)
	v := firstVoice(t, doc)
	var got []Element
	for _, e := range v.Elements {
		if e.Kind == KindBarline {
			got = append(got, e)
		}
	}
	if len(got) != 3 {
		t.Fatalf("应当有 3 条小节线，实得 %d（%+v）", len(got), got)
	}
	if got[0].Code != "|'p:2/4'" {
		t.Errorf("barline[0].code = %q，期望 |'p:2/4'", got[0].Code)
	}
	if got[0].TemporaryMeter == nil || got[0].TemporaryMeter.Numerator != 2 || got[0].TemporaryMeter.Denominator != 4 {
		t.Errorf("barline[0].temporaryMeter = %+v，期望 2/4", got[0].TemporaryMeter)
	}
	if got[1].Code != "|'foobar'" {
		t.Errorf("barline[1].code = %q，期望 |'foobar'（注释要去掉所有空白）", got[1].Code)
	}
	if got[1].TemporaryMeter != nil {
		t.Errorf("barline[1] 不该有 temporaryMeter：%+v", got[1].TemporaryMeter)
	}
	if got[2].Code != "|" {
		t.Errorf("barline[2].code = %q，期望 |", got[2].Code)
	}
	if len(doc.Diagnostics) != 0 {
		t.Errorf("不该有诊断：%+v", doc.Diagnostics)
	}
}

// ---------- 上游 it 6 ----------

func TestConsecutiveNoteAnnotationsConcatenate(t *testing.T) {
	doc := mustParse(t, `Q: 1"吉他solo""转E调" 2 |`)
	el := firstVoice(t, doc).Elements[0]
	if el.Kind != KindNote {
		t.Fatalf("elements[0].kind = %q", el.Kind)
	}
	if el.Annotation != "吉他solo转E调" {
		t.Errorf("annotation = %q，期望 吉他solo转E调", el.Annotation)
	}
	if el.Code != "1" {
		t.Errorf("code = %q，期望 1（**注释不进 Code**）", el.Code)
	}
	if len(doc.Diagnostics) != 0 {
		t.Errorf("不该有诊断：%+v", doc.Diagnostics)
	}
}

// ---------- 上游 it 7（部分） ----------

func TestAnnotationOnSustain(t *testing.T) {
	doc := mustParse(t, `Q: 1 -"转F调" {bz 2/"键盘"} 3 |`)
	el := firstVoice(t, doc).Elements[1]
	if el.Kind != KindSustain {
		t.Fatalf("elements[1].kind = %q，期望 sustain", el.Kind)
	}
	if el.Annotation != "转F调" {
		t.Errorf("annotation = %q", el.Annotation)
	}
	if el.Code != "-" {
		t.Errorf("code = %q，期望 -", el.Code)
	}
}

func TestInlineLayerBzNotAligned(t *testing.T) {
	t.Skip("上游 it 7/8/10/18 要求 `{bz …}` / `{dsb …}` 解析成 kind:\"inline-layer\"、" +
		"带 role 与内嵌 elements —— 本包**固定的 Element API 里没有 Role / Elements 字段**，" +
		"表达不了（改了就是改 API）。这里按约定**整块跳过且不崩**；" +
		"要真对齐需要给 Element 加 Role + Elements（或另开一个 Layer 类型）。")
}

// ---------- 上游 it 8（未对齐） ----------

func TestGraceNotesHairpinsBeatCutsInlineVoicesNotAligned(t *testing.T) {
	t.Skip("上游 it 8 要求：倚音 `[2/]` `[h3]` 进 graceBefore/graceAfter、" +
		"`|[\"1\" … |]` 的 volta、`(1[2/]<` 的 crescendo、`[ ]` 的 beat-boundary（带 behavior 字段）、" +
		"`{bz}`/`{dsb}` 的 inline-layer。其中 beat-boundary 的 behavior 与 inline-layer 的 role/elements " +
		"**本包固定 API 表达不了**；倚音与力度线属\"本轮明确不要求\"。全部只做\"容忍不崩\"。")
}

// ---------- 上游 it 9 ----------

func TestParsesNotesBarlinesCommandsAndSourceCode(t *testing.T) {
	doc := mustParse(t, `Q: 1'#//..&yc 8 9, 0 2$ 3= - |: :| :|: || ||/ |/ |*`)
	v := firstVoice(t, doc)
	els := v.Elements
	if len(els) == 0 {
		t.Fatal("没有元素")
	}
	first := els[0]
	if first.Kind != KindNote || first.Pitch != 1 || first.Octave != 1 || first.Duration != 16 ||
		first.Dots != 2 || first.Accidental != AccidentalSharp {
		t.Errorf("elements[0] = %+v", first)
	}
	if len(first.Ornaments) != 1 || first.Ornaments[0].Name != "yc" || first.Ornaments[0].Level != 0 {
		t.Errorf("elements[0].ornaments = %+v", first.Ornaments)
	}
	if first.Code != "1'#//..&yc" {
		t.Errorf("elements[0].code = %q", first.Code)
	}
	if !els[1].Hidden || els[1].Kind != KindNote {
		t.Errorf("elements[1] 应当是隐藏音：%+v", els[1])
	}
	if els[2].Sound != "rhythm" || els[2].Pitch != 9 || els[2].Octave != -1 {
		t.Errorf("elements[2] = %+v（期望 pitch 9 / sound rhythm / octave -1）", els[2])
	}
	eqStrings(t, "小节线类型", barlineTypes(v), []string{
		BarRepeatStart, BarRepeatEnd, BarRepeatBoth, BarEnd, BarDouble, BarHidden, BarInvisible,
	})
	if len(doc.Diagnostics) != 0 {
		t.Errorf("不该有诊断：%+v", doc.Diagnostics)
	}
}

// ---------- 上游 it 11 ----------

func TestSlurNestingResolvedAfterAssigningParens(t *testing.T) {
	doc := mustParse(t, `Q: (1 (1) 1) ((2 3) 4) |`)
	v := firstVoice(t, doc)
	eqStrings(t, "codes", codesOf(v), []string{"1(", "1()", "1)", "2((", "3)", "4)", "|"})
	want := []Mark{
		{Type: "slur", Start: 0, End: 1},
		{Type: "slur", Start: 1, End: 2},
		{Type: "slur", Start: 3, End: 4},
		{Type: "slur", Start: 3, End: 5},
	}
	if len(v.Marks) != len(want) {
		t.Fatalf("marks = %+v，期望 %+v", v.Marks, want)
	}
	for i := range want {
		if v.Marks[i] != want[i] {
			t.Errorf("marks[%d] = %+v，期望 %+v", i, v.Marks[i], want[i])
		}
	}
	if len(doc.Diagnostics) != 0 {
		t.Errorf("不该有诊断：%+v", doc.Diagnostics)
	}
}

// ---------- 上游 it 12 ----------

func TestZeroLengthNestedSlursDropped(t *testing.T) {
	doc := mustParse(t, `Q: (1) (1 (2)) ((3) 4) |`)
	v := firstVoice(t, doc)
	type se struct{ s, e int }
	var got []se
	for _, m := range v.Marks {
		got = append(got, se{m.Start, m.End})
	}
	want := []se{{1, 2}, {3, 4}}
	if len(got) != len(want) {
		t.Fatalf("marks 下标 = %+v，期望 %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("marks[%d] = %+v，期望 %+v", i, got[i], want[i])
		}
	}
	if len(doc.Diagnostics) != 0 {
		t.Errorf("不该有诊断：%+v", doc.Diagnostics)
	}
}

// ---------- 上游 it 13 ----------

func TestSustainCountsAsTupletMember(t *testing.T) {
	doc := mustParse(t, `Q: (y1 - 2) |`)
	v := firstVoice(t, doc)
	if len(v.Marks) != 1 {
		t.Fatalf("marks = %+v，期望 1 个 tuplet", v.Marks)
	}
	m := v.Marks[0]
	if m.Type != "tuplet" || m.Caption != "3" {
		t.Errorf("marks[0] = %+v，期望 tuplet caption 3", m)
	}
	if len(doc.Diagnostics) != 0 {
		t.Errorf("不该有诊断：%+v", doc.Diagnostics)
	}
}

// ---------- 上游 it 14 ----------

func TestParensAcrossInterveningBarlines(t *testing.T) {
	doc := mustParse(t, `Q: ( |/ 1 2 |)`)
	v := firstVoice(t, doc)
	eqStrings(t, "codes", codesOf(v), []string{"|/", "1(", "2)", "|"})
	if len(v.Marks) != 1 || v.Marks[0].Type != "slur" || v.Marks[0].Start != 1 || v.Marks[0].End != 2 {
		t.Errorf("marks = %+v，期望 slur 1→2", v.Marks)
	}
	if len(doc.Diagnostics) != 0 {
		t.Errorf("不该有诊断：%+v", doc.Diagnostics)
	}
}

// ---------- 上游 it 15（未对齐，但 Code 保真那一半做了） ----------

func TestHairpinMarksNotAligned(t *testing.T) {
	t.Skip("上游 it 15（`Q: 1> 2/~ 3! |`）要求：`>` `~` 之间形成一个 decrescendo mark、" +
		"且 `elements[3]` 是 `3!` —— 说明上游在元素表里**额外插了边界元素**（本包没有这种 kind）。" +
		"本包只保证 **Code 保真**（`2/~` / `3!` 原样留在 Code 里，见 TestCodeAbsorbsUnmodeledMarks），" +
		"力度线本身不建模。")
}

func TestCodeAbsorbsUnmodeledMarks(t *testing.T) {
	// `=` `~` `<` `>` `!` 本包**进 Code 但不建模**（Code 保源码原样是往返的关键）。
	doc := mustParse(t, `Q: 1> 2/~ 3! 4= 5 |`)
	v := firstVoice(t, doc)
	var notes []string
	for _, e := range v.Elements {
		if e.Kind == KindNote {
			notes = append(notes, e.Code)
		}
	}
	eqStrings(t, "音符的 Code", notes, []string{"1>", "2/~", "3!", "4=", "5"})
}

// ---------- 上游 it 16 ----------

func TestTrailingLyricPunctuationSeparate(t *testing.T) {
	doc := mustParse(t, "Q: 1 2 |\nC: 你，ABC,")
	syls := firstVoice(t, doc).Lyrics[0].Syllables
	if len(syls) != 2 {
		t.Fatalf("音节 = %+v，期望 2 个", syls)
	}
	if syls[0].Text != "你" || syls[0].TrailingPunctuation != "，" {
		t.Errorf("syllables[0] = %+v", syls[0])
	}
	if syls[1].Text != "ABC" || syls[1].TrailingPunctuation != "," {
		t.Errorf("syllables[1] = %+v", syls[1])
	}
	if len(doc.Diagnostics) != 0 {
		t.Errorf("不该有诊断：%+v", doc.Diagnostics)
	}
}

// ---------- 上游 it 17 ----------

func TestLyricBracesDoNotConsumeSlots(t *testing.T) {
	doc := mustParse(t, "Q: 1 2 3 |\nC: {甲乙}丙")
	syls := firstVoice(t, doc).Lyrics[0].Syllables
	if len(syls) != 3 {
		t.Fatalf("音节 = %+v，期望 3 个（花括号不占位）", syls)
	}
	if syls[0].Text != "甲" || !syls[0].LeftBrace {
		t.Errorf("syllables[0] = %+v，期望 甲 + leftBrace", syls[0])
	}
	if syls[1].Text != "乙" || !syls[1].RightBrace {
		t.Errorf("syllables[1] = %+v，期望 乙 + rightBrace", syls[1])
	}
	if syls[2].Text != "丙" || syls[2].LeftBrace || syls[2].RightBrace {
		t.Errorf("syllables[2] = %+v，期望 丙（无花括号）", syls[2])
	}
	if len(doc.Diagnostics) != 0 {
		t.Errorf("不该有诊断：%+v", doc.Diagnostics)
	}
}

// ---------- 上游 it 18 ----------

func TestBarredRepeatEndIsOneBarline(t *testing.T) {
	doc := mustParse(t, `Q: 1 :|| 2 |`)
	v := firstVoice(t, doc)
	var got []string
	for _, e := range v.Elements {
		if e.Kind == KindBarline {
			got = append(got, e.Type+"|"+e.Code)
		}
	}
	eqStrings(t, "小节线", got, []string{"repeat-end|:||", "normal||"})
	if len(doc.Diagnostics) != 0 {
		t.Errorf("不该有诊断：%+v", doc.Diagnostics)
	}
}

// ---------- 上游 it 19 ----------

func TestTildeJoinsPunctuationToPreviousSyllable(t *testing.T) {
	doc := mustParse(t, "Q: 1 2 3 4 5 6 7 |\nC: 喜洋洋~(欧@郎啰")
	syls := firstVoice(t, doc).Lyrics[0].Syllables
	var texts []string
	for _, s := range syls {
		texts = append(texts, s.Text)
	}
	eqStrings(t, "音节文字", texts, []string{"喜", "洋", "洋(", "欧", "", "郎", "啰"})
	if syls[2].TrailingPunctuation != "" {
		t.Errorf("`洋(` 的尾部标点应当是空的（`(` 不是标点），实得 %q", syls[2].TrailingPunctuation)
	}
	if len(doc.Diagnostics) != 0 {
		t.Errorf("不该有诊断：%+v", doc.Diagnostics)
	}
}

// ---------- 上游 it 20 ----------

func TestLyricsBindToMostRecentMusicLine(t *testing.T) {
	doc := mustParse(t, "Q1: 1\nC2: 甲\nQ2: 2\nC1: 乙")
	voices := doc.Pages[0].Groups[0].Voices
	if len(voices) != 2 {
		t.Fatalf("voice 数 = %d，期望 2", len(voices))
	}
	if got := voices[0].Lyrics[0].Syllables[0].Text; got != "甲" {
		t.Errorf("voice0 的第一个音节 = %q，期望 甲（`C2` 的后缀数字不看）", got)
	}
	if got := voices[1].Lyrics[0].Syllables[0].Text; got != "乙" {
		t.Errorf("voice1 的第一个音节 = %q，期望 乙", got)
	}
	if len(doc.Diagnostics) != 0 {
		t.Errorf("不该有诊断：%+v", doc.Diagnostics)
	}
}

// ---------- 上游 it 21（部分：Y/S 对齐，inline-layer 未对齐） ----------

func TestLegacyTemporaryVoiceShorthandHeaders(t *testing.T) {
	doc := mustParse(t, "Y: Piano, Voice\nS: 备注\nQ: { | 6, - } 2, - |")
	eqStrings(t, "instruments", doc.Metadata.Instruments, []string{"Piano", "Voice"})
	eqStrings(t, "remarks", doc.Metadata.Remarks, []string{"备注"})
}

func TestLegacyTemporaryVoiceShorthandLayerNotAligned(t *testing.T) {
	t.Skip("上游 it 21 还要求 `Q: { | 6, - } 2, - |` 的 elements[0] 是 kind:\"inline-layer\"、" +
		"role:\"voice\"、内嵌 notes —— 同上，本包固定 API 没有 Role/Elements，表达不了。")
}

// ---------- 上游 it 22 / 23 / 24（跨行/跨页的延续） ----------

func TestSlursAcrossMusicLinesNotAligned(t *testing.T) {
	t.Skip("上游 it 22（`Q: 1 2 (3 4 |` + `Q: 1 2 3) 4 |`）要求跨行连音打 " +
		"`continuationToNext` / `continuationFromPrevious` —— 本包固定的 Mark API 没有这两个字段。" +
		"本包按**行内**解析连音：跨行的开括号被丢弃且**不报诊断**（这正是让 it 24/25 对得上的口径）。")
}

func TestVoltaAcrossMusicLinesNotAligned(t *testing.T) {
	t.Skip("上游 it 23 要求 `|[\"1.\" …` 的 volta 与跨行延续字段 —— 同上：Mark 无 continuation 字段，" +
		"且 `[ ]` 属\"本轮明确不要求\"（本包整块跳过且不崩）。")
}

// ---------- 上游 it 24 ----------

func TestSlursDoNotCrossPageBreaks(t *testing.T) {
	doc := mustParse(t, "Q: 1 (2 |\n[fenye]\nQ: 3) 4 |")
	if len(doc.Pages) != 2 {
		t.Fatalf("pages = %d，期望 2", len(doc.Pages))
	}
	for i, p := range doc.Pages {
		for _, g := range p.Groups {
			for _, v := range g.Voices {
				if len(v.Marks) != 0 {
					t.Errorf("第 %d 页不该有 marks：%+v", i+1, v.Marks)
				}
			}
		}
	}
	if len(doc.Diagnostics) != 0 {
		t.Errorf("跨页丢弃不该报诊断：%+v", doc.Diagnostics)
	}
}

// ---------- 上游 it 25 ----------

func TestSlursDoNotCrossInterveningVoices(t *testing.T) {
	doc := mustParse(t, "Q3: 1 (2 |\nQ1: 3 4 |\nQ3: 5) 6 |")
	var all []Mark
	for _, g := range doc.Pages[0].Groups {
		for _, v := range g.Voices {
			all = append(all, v.Marks...)
		}
	}
	if len(all) != 0 {
		t.Errorf("不该有 marks：%+v", all)
	}
	if len(doc.Diagnostics) != 0 {
		t.Errorf("不该有诊断：%+v", doc.Diagnostics)
	}
	// 顺带钉住分组与声部：Q3 / Q1 / Q3 ⇒ 组 {3} · {3,1} · {3}（同号再次出现要开新组），共 3 条声部
	var nVoices int
	for _, g := range doc.Pages[0].Groups {
		nVoices += len(g.Voices)
	}
	if nVoices != 3 {
		t.Errorf("声部数 = %d，期望 3", nVoices)
	}
}

// ---------- 上游 it 26 ----------

func TestMalformedInputReportsWithoutThrowing(t *testing.T) {
	doc := mustParse(t, "hello\nD: h\nQ: 1&unknown (2")
	eqStrings(t, "诊断码顺序", diagCodes(doc), []string{
		DiagMissingPrefix, DiagInvalidMode, DiagUnknownCommand, DiagUnclosedMark,
	})
}

// ⭐ 只走**本包这一侧**（Parse → Blocks）的歌词回归：`C:` 行在的时候，歌词必须落到块上。
//
// 这条是为了把"往返里歌词丢了"的归属钉死：如果这里的歌词在、而 `Parse→Blocks→Write→Parse→Blocks`
// 之后没了，那问题在写出器那一侧（例如 `WriteOptions.EmitLyrics` 零值是 false、C 行根本没发出来），
// **不在解析器**。
func TestLyricSurvivesParseToBlocks(t *testing.T) {
	doc := mustParse(t, "D: C\nP: 4/4\nQ1: 1 2 3 4 |\nC1: 甲 乙 丙 丁")
	blocks := Blocks(doc, BlockOptions{TonicPitchClass: 0})
	if len(blocks) != 4 {
		t.Fatalf("块数 = %d，期望 4", len(blocks))
	}
	var got []string
	for _, b := range blocks {
		got = append(got, b.Lyric)
	}
	eqStrings(t, "歌词", got, []string{"甲", "乙", "丙", "丁"})
	// 空格分隔与 `@` 占位都要认（写出器就是这两种写法）
	doc2 := mustParse(t, "D: C\nP: 4/4\nQ1: 1 2 3 |\nC1: 甲 @ 丙")
	got = nil
	for _, b := range Blocks(doc2, BlockOptions{TonicPitchClass: 0}) {
		got = append(got, b.Lyric)
	}
	eqStrings(t, "含 @ 占位的歌词", got, []string{"甲", "", "丙"})
}

// ---------- 真谱子 fixture ----------

func TestFixtureTestJps(t *testing.T) {
	path := filepath.Join("testdata", "test.jps")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skip("没有 testdata/test.jps，跳过（把真谱子放进去就会真跑）")
	}
	// ⭐ 第一层：**不许 panic**（上游外面没有 try/catch，我们也不该崩）
	doc := Parse(string(raw))
	if doc == nil {
		t.Fatal("Parse 回了 nil")
	}

	// 头部
	m := doc.Metadata
	eqStrings(t, "titles", m.Titles, []string{"时　忆", "福州一中 2024届(11)班"})
	eqStrings(t, "authors", m.Authors, []string{"林嘉欣 庄忆 词", "黄若丞 曲"})
	if m.Mode != "C" {
		t.Errorf("mode = %q，期望 C", m.Mode)
	}
	if len(m.Meters) != 1 || m.Meters[0].Numerator != 4 || m.Meters[0].Denominator != 4 {
		t.Errorf("meters = %+v，期望 [4/4]", m.Meters)
	}
	if len(m.Tempos) != 1 || m.Tempos[0] != float64(80) {
		t.Errorf("tempos = %#v，期望 [80]", m.Tempos)
	}
	// `#` 注释行与空行都不算内容 ⇒ 整份谱子应当**一条诊断都没有**
	if len(doc.Diagnostics) != 0 {
		t.Errorf("真谱子不该有诊断，实得：%+v", doc.Diagnostics)
	}

	// 页数：正文里有 **2 个** `[fenye]`（第 38、77 行），加上开头那一段 ⇒ 3 页
	if len(doc.Pages) != 3 {
		t.Errorf("pages = %d，期望 3", len(doc.Pages))
	}

	// 第一条 Q1 行（`Q1"女声": 0/ 5,/ | (2/ 3//) 3// …`）的小节边界
	v := firstVoice(t, doc)
	if v.Caption != "女声" {
		t.Errorf("第一个声部的 caption = %q，期望 女声", v.Caption)
	}
	barCount := 0
	for _, e := range v.Elements {
		if e.Kind == KindBarline {
			barCount++
		}
	}
	// 那一行有 5 条 `|`；但 v.Elements 只到该行结尾 ⇒ 正好 5
	if barCount != 5 {
		t.Errorf("第一条 Q1 行的小节线 = %d，期望 5", barCount)
	}
	// 28 个音符 ↔ 28 个音节（这条同时在验歌词切分器：`@` 占位、汉字逐字）
	noteCount := 0
	for _, e := range v.Elements {
		if e.Kind == KindNote {
			noteCount++
		}
	}
	if noteCount != 28 {
		t.Errorf("第一条 Q1 行的音符 = %d，期望 28", noteCount)
	}
	if len(v.Lyrics) == 0 {
		t.Fatal("第一条 Q1 行没有歌词")
	}
	if got := len(v.Lyrics[0].Syllables); got != 28 {
		t.Errorf("C1 的音节 = %d，期望 28", got)
	}

	// 音符块：整份谱子要能有块，且第一块落在第 1 小节第 1 拍
	blocks := Blocks(doc, BlockOptions{TonicPitchClass: -1})
	if len(blocks) == 0 {
		t.Fatal("Blocks 没产出任何块")
	}
	b0 := blocks[0]
	if b0.Measure != 1 || b0.Beat != 1 {
		t.Errorf("第一块应当在小节 1 第 1 拍，实得 measure=%d beat=%v（%+v）", b0.Measure, b0.Beat, b0)
	}
	if b0.Degree != 0 || b0.Midi != -1 {
		t.Errorf("`0/` 是休止 ⇒ degree 0 / midi -1，实得 %+v", b0)
	}
	// 小节边界：第二条 `|` 之后的音符应当进第 2 小节
	foundMeasure2 := false
	for _, b := range blocks {
		if b.Voice == 1 && b.Measure == 2 {
			foundMeasure2 = true
			if b.Beat != 1 {
				t.Errorf("第 2 小节第一个块应当在第 1 拍，实得 %v", b.Beat)
			}
			break
		}
	}
	if !foundMeasure2 {
		t.Error("没找到第 2 小节的块")
	}
	// 音高抽查：`Q1"女声"` 第 4 个音符是 `2/`（八分），C 大调 ⇒ MIDI 62
	var got2 bool
	for _, b := range blocks {
		if b.Voice == 1 && b.Degree == 2 && b.Code == "2/" {
			if b.Midi != 62 {
				t.Errorf("音级 2 在 C 大调应当 = 62，实得 %d（%+v）", b.Midi, b)
			}
			if b.Beats != 0.5 {
				t.Errorf("`2/` 是八分 ⇒ 0.5 拍，实得 %v", b.Beats)
			}
			got2 = true
			break
		}
	}
	if !got2 {
		t.Error("没找到 `2/` 那个块")
	}
}
