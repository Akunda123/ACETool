package jianpu

import (
	"math"
	"strings"
	"testing"
)

func notationMidis(bs []NoteBlock) []int {
	out := make([]int, 0, len(bs))
	for _, b := range bs {
		out = append(out, b.Midi)
	}
	return out
}

func hasDiag(ds []Diagnostic, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

// 基本记法：`1-7` 唱名 + `yinjie` 表 + `1=C` 档位（原脚本的算法：60 + 档位 + yinjie[度数]）。
func TestNotationBasics(t *testing.T) {
	bs, diags := ParseNotation("1 2 3 4 5 6 7", NotationOptions{KeyIndex: 0})
	if len(bs) != 7 {
		t.Fatalf("应当 7 个音符块，实得 %d（诊断 %+v）", len(bs), diags)
	}
	// C 大调：1 2 3 4 5 6 7 = C D E F G A B = 60 62 64 65 67 69 71
	want := []int{60, 62, 64, 65, 67, 69, 71}
	got := notationMidis(bs)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 个音高 = %d，期望 %d（C 大调音阶）", i, got[i], want[i])
		}
	}
	for _, b := range bs {
		if math.Abs(b.Beats-1) > 1e-9 {
			t.Errorf("裸数字应当是 1 拍，实得 %g", b.Beats)
		}
	}
	// 换调：1=G ⇒ 1 应当落在 G(67)
	bs2, _ := ParseNotation("1", NotationOptions{KeyIndex: 7})
	if bs2[0].Midi != 67 {
		t.Errorf("1=G 时 `1` 应当是 67(G)，实得 %d", bs2[0].Midi)
	}
}

// 修饰符：`q`/`w` 八度、`#`/`b` 半音、`.` 附点、`_` 缩短 —— **写在数字后面**（修好的读法）。
func TestNotationModifiersAfterDigit(t *testing.T) {
	for _, c := range []struct {
		in    string
		midi  int
		beats float64
		why   string
	}{
		{"1", 60, 1, "裸四分"},
		{"1.", 60, 1.5, "附点 ×1.5"},
		{"1_", 60, 0.5, "缩短 ×0.5"},
		{"1._", 60, 0.75, "点 + 缩短"},
		{"1#", 61, 1, "升半音"},
		{"1b", 59, 1, "降半音"},
		{"1q", 72, 1, "升八度"},
		{"1w", 48, 1, "降八度"},
		{"1qq", 84, 1, "升两个八度"},
		{"1#.", 61, 1.5, "组合"},
	} {
		bs, diags := ParseNotation(c.in, NotationOptions{KeyIndex: 0})
		if len(bs) != 1 {
			t.Errorf("%q（%s）应当 1 个块，实得 %d（诊断 %+v）", c.in, c.why, len(bs), diags)
			continue
		}
		if bs[0].Midi != c.midi {
			t.Errorf("%q（%s）音高 = %d，期望 %d", c.in, c.why, bs[0].Midi, c.midi)
		}
		if math.Abs(bs[0].Beats-c.beats) > 1e-9 {
			t.Errorf("%q（%s）拍数 = %g，期望 %g", c.in, c.why, bs[0].Beats, c.beats)
		}
	}
}

// ⛔ **移植时改掉的原脚本 bug 1**：修饰符写在数字前面时，两种读法结果不同 ⇒ 必须报出来。
func TestNotationReportsModifierScopeDifference(t *testing.T) {
	bs, diags := ParseNotation("1. 2", NotationOptions{KeyIndex: 0})
	if !hasDiag(diags, "modifier-scope") {
		t.Fatalf("`1. 2` 在两种读法下结果不同，必须报 modifier-scope，实得 %+v", diags)
	}
	// 修好的读法：附点归 1（与简谱习惯一致）
	if len(bs) != 2 || math.Abs(bs[0].Beats-1.5) > 1e-9 || math.Abs(bs[1].Beats-1) > 1e-9 {
		t.Errorf("修好的读法应当是「1 加附点、2 不加」，实得 %g / %g", bs[0].Beats, bs[1].Beats)
	}
	// 原脚本的读法：附点归 2
	bs2, _ := ParseNotation("1. 2", NotationOptions{KeyIndex: 0, LegacyPrefixModifiers: true})
	if math.Abs(bs2[0].Beats-1) > 1e-9 || math.Abs(bs2[1].Beats-1.5) > 1e-9 {
		t.Errorf("原脚本读法应当是「1 不加、2 加附点」，实得 %g / %g", bs2[0].Beats, bs2[1].Beats)
	}
}

// ⛔ **移植时改掉的原脚本 bug 2**：`-`（延音线）应当**延长前一个音符**，
// 而不是像原脚本那样凭空造一个主音音符。
func TestNotationSustainExtendsPreviousNotPhantomTonic(t *testing.T) {
	bs, diags := ParseNotation("1 - -", NotationOptions{KeyIndex: 0})
	if len(bs) != 1 {
		t.Fatalf("`1 - -` 应当折成 **1 个**音符块（延长两根），实得 %d 个：%+v", len(bs), bs)
	}
	if math.Abs(bs[0].Beats-3) > 1e-9 {
		t.Errorf("1 + 两根延音线应当 3 拍，实得 %g", bs[0].Beats)
	}
	if bs[0].Midi != 60 {
		t.Errorf("音高应当还是 60(C)，实得 %d", bs[0].Midi)
	}
	if len(diags) != 0 {
		t.Errorf("正常输入不该有诊断，实得 %+v", diags)
	}
	// 前面没音符时要说清（原脚本会在这种地方造一个主音音符）
	_, d2 := ParseNotation("- 1", NotationOptions{KeyIndex: 0})
	if !hasDiag(d2, "sustain-without-note") {
		t.Errorf("开头就是延音线时应当报 sustain-without-note，实得 %+v", d2)
	}
}

// `=` 的语义是「延长前一个音符」（照原脚本的功能说明）。
func TestNotationEqualsExtendsPrevious(t *testing.T) {
	bs, _ := ParseNotation("1 =", NotationOptions{KeyIndex: 0})
	if len(bs) != 1 || math.Abs(bs[0].Beats-2) > 1e-9 {
		t.Errorf("`1 =` 应当是 2 拍，实得 %d 个块、%g 拍", len(bs), bs[0].Beats)
	}
}

// 休止 `0` 保留位置但**不产生要唱的音**（原脚本对 0 只推进偏移、不建音符）。
func TestNotationRest(t *testing.T) {
	bs, _ := ParseNotation("1 0 2", NotationOptions{KeyIndex: 0})
	if len(bs) != 3 {
		t.Fatalf("应当 3 个元素位（含休止），实得 %d", len(bs))
	}
	if bs[1].Degree != 0 || bs[1].Midi != -1 {
		t.Errorf("中间的休止应当是 Degree 0 / Midi -1，实得 %d / %d", bs[1].Degree, bs[1].Midi)
	}
	if bs[2].Midi != 62 {
		t.Errorf("休止之后的 2 应当是 62，实得 %d", bs[2].Midi)
	}
}

// 小节线 `|` 是分隔符（原脚本的扫描根本不认它，等于忽略）。
func TestNotationIgnoresBarlinesAndUnknownChars(t *testing.T) {
	bs, _ := ParseNotation("1 2 | 3 4 |", NotationOptions{KeyIndex: 0})
	if len(bs) != 4 {
		t.Errorf("`|` 不该产生音符块，实得 %d 个", len(bs))
	}
	_, d := ParseNotation("1 @ 2", NotationOptions{KeyIndex: 0})
	if !hasDiag(d, "unknown-char") {
		t.Errorf("认不出的字符要报 unknown-char（原脚本是静默忽略），实得 %+v", d)
	}
}

// 往返：`ParseNotation → WriteNotation → ParseNotation` 的音乐内容必须一致。
func TestNotationRoundTrip(t *testing.T) {
	for _, in := range []string{
		"1 2 3 5 6",
		"1. 2_ 3 4",
		"1 0 2 0 3",
		"1 2 - 3",   // 延音已经折进块，往返后应当变成等长的写法
		"1q 2 3w 4", // 八度
		"1# 2b 3",
	} {
		opt := NotationOptions{KeyIndex: 0, Meter: Meter{Numerator: 4, Denominator: 4}}
		b1, _ := ParseNotation(in, opt)
		text, wd := WriteNotation(b1, opt)
		b2, pd := ParseNotation(text, opt)

		if len(b1) != len(b2) {
			t.Errorf("%q：往返后块数变了 %d → %d\n  写出：%s\n  写诊断 %+v\n  解析诊断 %+v",
				in, len(b1), len(b2), text, wd, pd)
			continue
		}
		for i := range b1 {
			if b1[i].Midi != b2[i].Midi || math.Abs(b1[i].Beats-b2[i].Beats) > 1e-6 {
				t.Errorf("%q：第 %d 个块不一致（音高 %d→%d、拍数 %g→%g）\n  写出：%s",
					in, i, b1[i].Midi, b2[i].Midi, b1[i].Beats, b2[i].Beats, text)
			}
		}
	}
}

// 时值在这套记法里只能靠 `.`(×1.5) 与 `_`(×0.5) 表达 ⇒ 三连音写不出来，**必须报**。
func TestNotationUnrepresentableDuration(t *testing.T) {
	_, diags := WriteNotation([]NoteBlock{{Degree: 1, Midi: 60, Beats: 4.0 / 3.0}}, NotationOptions{KeyIndex: 0})
	if !hasDiag(diags, "unrepresentable-duration") {
		t.Errorf("三连音时值应当报 unrepresentable-duration，实得 %+v", diags)
	}
}

// 默认歌词（原脚本那个 `Default lyrics` 输入框，默认 "la"）。
func TestNotationDefaultLyric(t *testing.T) {
	bs, _ := ParseNotation("1 2", NotationOptions{KeyIndex: 0, DefaultLyric: "la"})
	for i, b := range bs {
		if b.Lyric != "la" {
			t.Errorf("第 %d 个块应当带上默认歌词 la，实得 %q", i, b.Lyric)
		}
	}
}

// 调式档位与名字要能对上（`1=#C` 是档位 1，不是 0）。
func TestNotationKeyIndexMatchesNames(t *testing.T) {
	if len(notationKeyNames) != 12 {
		t.Fatalf("调式档位应当 12 个，实得 %d", len(notationKeyNames))
	}
	if notationKeyNames[1] != "1=#C" || notationKeyNames[0] != "1=C" {
		t.Errorf("档位名字不对：%v", notationKeyNames)
	}
	// 档位 1（#C）时 `1` 应当是 61
	bs, _ := ParseNotation("1", NotationOptions{KeyIndex: 1})
	if bs[0].Midi != 61 {
		t.Errorf("1=#C 时 `1` 应当是 61，实得 %d", bs[0].Midi)
	}
	// 写出时不该冒出"没有音高"这类无谓诊断
	text, d := WriteNotation(bs, NotationOptions{KeyIndex: 1})
	if len(d) != 0 {
		t.Errorf("正常写出不该有诊断，实得 %+v（文本 %s）", d, text)
	}
	if !strings.Contains(text, "1") {
		t.Errorf("写出文本里应当有 1，实得 %q", text)
	}
}
