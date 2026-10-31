package jianpu

import (
	"encoding/xml"
	"math"
	"strings"
	"testing"
)

// 音符块的"音乐内容"投影 —— 往返比较用它（**不拿整个结构体比**：
// MusicXML 往返后 Degree/Octave/Accidental/Code/ElementIndex 都是重算或没有的）。
type mxlKey struct {
	Midi   int
	Ticks  int64
	Lyric  string
	Hidden bool
	Voice  int
}

func mxlProject(bs []NoteBlock) []mxlKey {
	out := make([]mxlKey, 0, len(bs))
	for _, b := range bs {
		h := b.Hidden
		if b.Midi < 0 {
			h = true
		}
		v := b.Voice
		if v <= 0 {
			v = 1
		}
		out = append(out, mxlKey{Midi: b.Midi, Ticks: b.Ticks, Lyric: b.Lyric, Hidden: h, Voice: v})
	}
	return out
}

func mxlRoundTrip(t *testing.T, in []NoteBlock, opt MusicXMLOptions) (string, []NoteBlock) {
	t.Helper()
	x, wd := WriteMusicXML(in, opt)
	if len(wd) > 0 {
		t.Logf("写出诊断：%+v", wd)
	}
	out, rd := ParseMusicXML(x)
	for _, d := range rd {
		if d.Code == "musicxml-parse-error" || d.Code == "musicxml-no-part" {
			t.Fatalf("自己写出来的 MusicXML 都读不回来：%+v\n--- XML ---\n%s", rd, x)
		}
	}
	return x, out
}

// ⭐ 往返：音符块 → MusicXML → 音符块，音乐内容必须一致。
func TestMusicXMLRoundTrip(t *testing.T) {
	opt := MusicXMLOptions{TonicPitchClass: 0, Meter: Meter{Numerator: 4, Denominator: 4}}
	for _, c := range []struct {
		name string
		dsl  string
	}{
		{"五声音阶", "1 2 3 5 6"},
		{"带附点与缩短", "1. 2_ 3 4"},
		{"含休止", "1 0 2 0 3"},
		{"八度", "1q 2 3w 4"},
		{"变音", "1# 2b 3"},
		{"延音折入", "1 - - 2"},
		{"长音超一小节", "1 - - - 2 - - -"},
	} {
		t.Run(c.name, func(t *testing.T) {
			blocks, _ := ParseNotation(c.dsl, NotationOptions{KeyIndex: 0, DefaultLyric: ""})
			_, back := mxlRoundTrip(t, blocks, opt)
			a, b := mxlProject(blocks), mxlProject(back)
			if len(a) != len(b) {
				t.Fatalf("%q：往返后块数变了 %d → %d\n  原始 %+v\n  回来 %+v", c.dsl, len(a), len(b), a, b)
			}
			for i := range a {
				if a[i] != b[i] {
					t.Errorf("%q：第 %d 个块不一致 %+v → %+v", c.dsl, i, a[i], b[i])
				}
			}
		})
	}
}

// ⭐ 长音要靠 `<tie>` 拆，读回来**必须合回一个块** —— 否则往返自己就破了。
func TestMusicXMLSplitsLongNoteIntoTiesAndMergesBack(t *testing.T) {
	const beats = 5.0
	in := []NoteBlock{{Midi: 60, Beats: beats, Ticks: int64(beats * 480), Voice: 1}}
	x, _ := WriteMusicXML(in, MusicXMLOptions{TonicPitchClass: 0, Meter: Meter{Numerator: 4, Denominator: 4}})

	// 写出侧：应当有 >1 个 `<note>` 且带 tie
	if n := strings.Count(x, "<note>"); n < 2 {
		t.Fatalf("5 拍超出一个 whole，应当拆成多个 `<note>` 带 tie，实得 %d 个 note\n%s", n, x)
	}
	if !strings.Contains(x, `<tie type="start"/>`) || !strings.Contains(x, `<tie type="stop"/>`) {
		t.Errorf("拆出来的片要有 tie\n%s", x)
	}
	// 读回侧：必须合回**一个**块、且总时值不变
	back, diags := ParseMusicXML(x)
	if len(back) != 1 {
		t.Fatalf("tie 链要合回 1 个块，实得 %d 个：%+v（诊断 %+v）", len(back), back, diags)
	}
	if math.Abs(back[0].Beats-beats) > 1e-6 {
		t.Errorf("合回来的时值应当是 %g 拍，实得 %g", beats, back[0].Beats)
	}
	if back[0].Midi != 60 {
		t.Errorf("音高应当还是 60，实得 %d", back[0].Midi)
	}
}

// 产物要是**良构 XML**，而且关键元素都在（自己写自己读只能证明配对，证明不了规范）。
func TestMusicXMLIsWellFormedAndHasRequiredElements(t *testing.T) {
	blocks, _ := ParseNotation("1 2 3 5 6", NotationOptions{KeyIndex: 7}) // 1=G
	x, _ := WriteMusicXML(blocks, MusicXMLOptions{
		TonicPitchClass: 7, Meter: Meter{Numerator: 3, Denominator: 4},
		Title: "测试曲", Composer: "某人", PartName: "主旋律",
	})
	// ① 良构（能整段 unmarshal）
	var any struct {
		XMLName xml.Name
	}
	if err := xml.Unmarshal([]byte(x), &any); err != nil {
		t.Fatalf("写出来的不是良构 XML：%v\n%s", err, x)
	}
	if any.XMLName.Local != "score-partwise" {
		t.Errorf("根元素应当是 score-partwise，实得 %s", any.XMLName.Local)
	}
	// ② 关键元素
	for _, want := range []string{
		"<divisions>480</divisions>",
		"<fifths>1</fifths>", // 1=G ⇒ 一个升号
		"<beats>3</beats><beat-type>4</beat-type>",
		"<clef><sign>G</sign><line>2</line></clef>",
		"<part-name>主旋律</part-name>",
		"<work-title>测试曲</work-title>",
		`<creator type="composer">某人</creator>`,
		"<part-list>",
	} {
		if !strings.Contains(x, want) {
			t.Errorf("产物里缺 %q\n%s", want, x)
		}
	}
	// ③ 音符数要对（5 个音 → 5 个 <note>）
	if n := strings.Count(x, "<note>"); n != 5 {
		t.Errorf("5 个音应当写出 5 个 <note>，实得 %d", n)
	}
	// ④ 第一小节的 attributes 只出一次
	if n := strings.Count(x, "<attributes>"); n != 1 {
		t.Errorf("<attributes> 应当只在开头出一次，实得 %d 次", n)
	}
}

// ⭐⭐ 回归：`forceSharp` **只该管 C#/Db 与 F#/Gb 那两档**。
//
// 曾经的实现是「`if !forceSharp { 查降号表 }` 再查升号表、查不到 `return 0`」——
// 于是 `forceSharp=true` 时 F 大调（pc=5）在升号表里查不到 ⇒ **写成 `<fifths>0</fifths>`（C 大调）**，
// 而音符拼法还按 F 大调走（F 调里的 B♭ 写成 B♭）⇒ 调号与音符**自相矛盾**。
// 老测试只覆盖了 key=0 与 key=1（7 个升号），正好躲开了这个洞。
func TestFifthsOfHandlesFlatKeysAndOnlyTwoWayKeysAreAmbiguous(t *testing.T) {
	cases := []struct {
		pc                int
		preferFlat, force bool
		want              int
		why               string
	}{
		{0, false, false, 0, "C 大调：没有升降号"},
		{7, false, false, 1, "G 大调：1 个升号"},
		{11, false, false, 5, "B 大调：5 个升号"},
		{5, false, false, -1, "F 大调：1 个降号（**不能**被 forceSharp 弄成 0）"},
		{5, false, true, -1, "F 大调 + forceSharp：**仍然是 1 个降号** —— forceSharp 只管两可档"},
		{10, false, true, -2, "Bb 大调 + forceSharp：仍是 2 个降号"},
		{3, false, true, -3, "Eb 大调 + forceSharp：仍是 3 个降号"},
		{8, false, true, -4, "Ab 大调 + forceSharp：仍是 4 个降号"},
		{1, false, false, -5, "C#/Db 两可 ⇒ 默认走降号侧（Db，5 个降号）"},
		{1, false, true, 7, "C#/Db 两可 + forceSharp ⇒ 走升号侧（C#，7 个升号）"},
		{6, false, false, -6, "F#/Gb 两可 ⇒ 默认降号侧（Gb）"},
		{6, false, true, 6, "F#/Gb 两可 + forceSharp ⇒ 升号侧（F#）"},
		{1, true, false, -5, "preferFlat 也走降号侧"},
	}
	for _, c := range cases {
		if got := fifthsOf(c.pc, c.preferFlat, c.force); got != c.want {
			t.Errorf("fifthsOf(%d, preferFlat=%v, forceSharp=%v) = %d，期望 %d（%s）",
				c.pc, c.preferFlat, c.force, got, c.want, c.why)
		}
	}
}

// ⭐ 拼法：**元素自带的音级优先**（那是记谱意图），没有才按调号推。
// `F 大调里的 #4` 必须写成 B♮（不能因为拼法偏好变成 C♭）；`C 大调的 2b` 必须写成 D♭（不是 C#）。
func TestSpellMidiPrefersElementDegreeThenKey(t *testing.T) {
	fSharp := MusicXMLOptions{TonicPitchClass: 5, ForceSharp: true}
	// 元素来源：F 大调 degree 4（= B♭，midi 70）与 #4（= B♮，midi 71）
	if step, alter, oct := spellMidi(NoteBlock{Midi: 70, Degree: 4, Octave: 0}, fSharp); step != "B" || alter != -1 || oct != 4 {
		t.Errorf("F 大调 4（B♭）应当拼成 B♭4，实得 %s alter=%d oct=%d", step, alter, oct)
	}
	if step, alter, oct := spellMidi(NoteBlock{Midi: 71, Degree: 4, Octave: 0}, fSharp); step != "B" || alter != 0 || oct != 4 {
		t.Errorf("F 大调 #4（B♮）应当拼成 B♮4 —— **不能**变成 C♭5，实得 %s alter=%d oct=%d", step, alter, oct)
	}
	// ACE 来源（不设 Degree）⇒ 按调号推：F 大调里的 70 也是 B♭，不是 A#
	if step, alter, _ := spellMidi(NoteBlock{Midi: 70}, fSharp); step != "B" || alter != -1 {
		t.Errorf("F 大调里 midi 70 应当拼成 B♭（不是 A#），实得 %s alter=%d", step, alter)
	}
	// C 大调的 2b（midi 49）= D♭3，不是 C#3
	cMajor := MusicXMLOptions{TonicPitchClass: 0, ForceSharp: true}
	if step, alter, oct := spellMidi(NoteBlock{Midi: 49, Degree: 2, Octave: -1}, cMajor); step != "D" || alter != -1 || oct != 3 {
		t.Errorf("C 大调 2b 应当拼成 D♭3，实得 %s alter=%d oct=%d", step, alter, oct)
	}
	// 低八度也要落对：C 大调 degree 1 octave -1 = C3
	if step, alter, oct := spellMidi(NoteBlock{Midi: 48, Degree: 1, Octave: -1}, cMajor); step != "C" || alter != 0 || oct != 3 {
		t.Errorf("C 大调低八度 1 应当拼成 C3，实得 %s alter=%d oct=%d", step, alter, oct)
	}
	// 没有调性信息 ⇒ 退回升号拼法（老实说不知道）
	if step, alter, _ := spellMidi(NoteBlock{Midi: 61}, MusicXMLOptions{TonicPitchClass: -1}); step != "C" || alter != 1 {
		t.Errorf("没有调性信息时应当退回升号拼法 C#，实得 %s alter=%d", step, alter)
	}
}

func TestMusicXMLRestAndHidden(t *testing.T) {
	in := []NoteBlock{
		{Midi: -1, Degree: 0, Beats: 1, Ticks: 480, Hidden: true, Voice: 1}, // 休止
		{Midi: 60, Beats: 1, Ticks: 480, Voice: 1, Lyric: "明"},              // 正常
		{Midi: 62, Beats: 1, Ticks: 480, Voice: 1, Hidden: true},            // 隐藏音（保留音高）
	}
	x, _ := WriteMusicXML(in, MusicXMLOptions{TonicPitchClass: 0})
	if !strings.Contains(x, "<rest/>") {
		t.Errorf("休止应当写 <rest/>\n%s", x)
	}
	if !strings.Contains(x, `print-object="no"`) {
		t.Errorf("隐藏音应当写 print-object=\"no\"\n%s", x)
	}
	back, _ := ParseMusicXML(x)
	if len(back) != 3 {
		t.Fatalf("应当读回 3 个块，实得 %d：%+v", len(back), back)
	}
	if back[0].Midi != -1 || back[0].Degree != 0 {
		t.Errorf("第一个应当是休止（Midi -1 / Degree 0），实得 %d / %d", back[0].Midi, back[0].Degree)
	}
	if back[1].Lyric != "明" {
		t.Errorf("第二个的歌词应当是「明」，实得 %q", back[1].Lyric)
	}
	if back[2].Midi != 62 || !back[2].Hidden {
		t.Errorf("第三个应当是隐藏音、音高 62，实得 Midi=%d Hidden=%v", back[2].Midi, back[2].Hidden)
	}
}

// 调号：升号调出正数、降号调出负数；**两可的**（pc 1 / 6）按偏好选边。
//
// ⚠️ 我原来在这里写错过一次：以为 `preferFlat=false` 就会给 C#（7 个升号），
// 但规则是"**不偏好降号**"≠"偏好升号" —— 降号调（Db/Gb）默认仍走降号侧。
// 要强制升号侧得用 `ForceSharp`（用户那套记法的调式表全是升号，从它转过来就需要）。
func TestMusicXMLKeySignature(t *testing.T) {
	for _, c := range []struct {
		pc     int
		flat   bool
		force  bool
		fifths int
		why    string
	}{
		{0, false, false, 0, "C 无升降"},
		{7, false, false, 1, "G 一个升号"},
		{2, false, false, 2, "D 两个升号"},
		{9, false, false, 3, "A 三个升号"},
		{5, false, false, -1, "F 一个降号"},
		{10, false, false, -2, "Bb 两个降号（降号调默认走降号侧）"},
		{1, false, false, -5, "pc=1 是两可的，默认走降号侧 ⇒ Db 五个降号"},
		{1, false, true, 7, "**强制升号侧** ⇒ C# 七个升号"},
		{6, false, false, -6, "pc=6 是两可的，默认走降号侧 ⇒ Gb 六个降号"},
		{6, false, true, 6, "强制升号侧 ⇒ F# 六个升号"},
	} {
		if got := fifthsOf(c.pc, c.flat, c.force); got != c.fifths {
			t.Errorf("%s：fifthsOf(%d, flat=%v, forceSharp=%v) = %d，期望 %d",
				c.why, c.pc, c.flat, c.force, got, c.fifths)
		}
	}
}

// 多声部 ⇒ 多个 `<part>`（ACE `import file` 就是一条轨一个 part）。
func TestMusicXMLOnePartPerVoice(t *testing.T) {
	in := []NoteBlock{
		{Midi: 60, Beats: 1, Ticks: 480, Voice: 1},
		{Midi: 48, Beats: 1, Ticks: 480, Voice: 2},
	}
	x, _ := WriteMusicXML(in, MusicXMLOptions{TonicPitchClass: 0})
	if n := strings.Count(x, "<part id="); n != 2 {
		t.Errorf("两个声部应当出 2 个 <part>，实得 %d\n%s", n, x)
	}
	back, _ := ParseMusicXML(x)
	if len(back) != 2 {
		t.Fatalf("应当读回 2 个块，实得 %d", len(back))
	}
	if back[0].Voice == back[1].Voice {
		t.Errorf("两个块的声部号不该相同：%+v", back)
	}
}

// 读不动的东西要**报诊断**而不是静默丢（和弦）。
func TestMusicXMLReportsChords(t *testing.T) {
	const chordXML = `<?xml version="1.0"?>
<score-partwise version="4.0"><part-list><score-part id="P1"><part-name>V</part-name></score-part></part-list>
<part id="P1"><measure number="1">
<attributes><divisions>480</divisions><time><beats>4</beats><beat-type>4</beat-type></time></attributes>
<note><pitch><step>C</step><octave>4</octave></pitch><duration>480</duration><type>quarter</type></note>
<note><chord/><pitch><step>E</step><octave>4</octave></pitch><duration>480</duration><type>quarter</type></note>
</measure></part></score-partwise>`
	_, diags := ParseMusicXML(chordXML)
	if !hasDiag(diags, "musicxml-chord-ignored") {
		t.Errorf("和弦音要报 musicxml-chord-ignored，实得 %+v", diags)
	}
}

// 坏输入不 panic、给诊断。
func TestMusicXMLBadInput(t *testing.T) {
	for _, in := range []string{"", "not xml at all", "<foo/>", "<score-partwise></score-partwise>"} {
		out, diags := ParseMusicXML(in)
		if len(out) != 0 {
			t.Errorf("%q 应当读不出音符块", in)
		}
		if len(diags) == 0 {
			t.Errorf("%q 应当给诊断（不静默）", in)
		}
	}
}

// 和简谱写出器接起来：你那套记法 → 音符块 → MusicXML → 音符块，音高与时值不变。
func TestMusicXMLBridgesTerseNotation(t *testing.T) {
	opt := NotationOptions{KeyIndex: 0, Meter: Meter{Numerator: 4, Denominator: 4}, DefaultLyric: ""}
	blocks, _ := ParseNotation("1 2 3 5 6 1q 2w 0 1#", opt)
	x, _ := WriteMusicXML(blocks, MusicXMLOptions{TonicPitchClass: 0, Meter: opt.Meter})
	back, diags := ParseMusicXML(x)
	if len(back) != len(blocks) {
		t.Fatalf("块数应当不变：%d → %d（诊断 %+v）\n%s", len(blocks), len(back), diags, x)
	}
	for i := range blocks {
		if blocks[i].Midi != back[i].Midi {
			t.Errorf("第 %d 个音高 %d → %d", i, blocks[i].Midi, back[i].Midi)
		}
		if blocks[i].Ticks != back[i].Ticks {
			t.Errorf("第 %d 个时值 %d → %d tick", i, blocks[i].Ticks, back[i].Ticks)
		}
	}
}
