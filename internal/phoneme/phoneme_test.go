package phoneme

import "testing"

func TestZhCoreSymbolsCovered(t *testing.T) {
	// 官方 inventory 里的中文声母（23 个）必须全部在表里
	for _, s := range []string{"b", "c", "ch", "d", "f", "g", "h", "j", "k", "l", "m", "n", "p", "q", "r", "s", "sh", "t", "w", "x", "y", "z", "zh"} {
		e := Describe("zh", s, "")
		if e.Symbol != s {
			t.Fatalf("%s: Symbol 丢了", s)
		}
		if e.Official == "" || e.Manner == "" {
			t.Fatalf("%s: 分类/发音方式为空（%+v）", s, e)
		}
	}
}

func TestZhErhuaFallsBackToVowel(t *testing.T) {
	// 官方表缺儿化韵（ar/aor/…），Describe 应当用“以 r 结尾 + IPA”兜底，而不是返回空
	for _, s := range []string{"ar", "angr", "aor", "eer", "ur"} {
		e := Describe("zh", s, "")
		if e.Manner == "" {
			t.Fatalf("%s: 没有兜底出发音方式", s)
		}
		if e.Official != "Vowel" {
			t.Fatalf("%s: 分类应为 Vowel，实得 %q", s, e.Official)
		}
	}
}

func TestCLIIpaOverridesOfficial(t *testing.T) {
	// CLI 的 phoneticsHints 更准，应覆盖官方表里的并列写法
	e := Describe("eng", "aa", "ɑ")
	if e.IPA != "ɑ" {
		t.Fatalf("IPA 应被覆盖为 ɑ，实得 %q", e.IPA)
	}
	if e.Manner != "元音" {
		t.Fatalf("aa 的发音方式应为元音，实得 %q", e.Manner)
	}
}

func TestUnknownSymbolStaysHonest(t *testing.T) {
	e := Describe("zh", "zzz", "")
	if e.Symbol != "zzz" || e.IPA != "" {
		t.Fatalf("未知符号不该编造 IPA：%+v", e)
	}
	if e.Manner != "" || e.Official != "" {
		t.Fatalf("未知符号不该编造分类：%+v", e)
	}
}

func TestMannerFromIPA(t *testing.T) {
	cases := map[string]string{
		"p": "塞音", "tʃ": "塞擦音", "s": "擦音", "m": "鼻音",
		"l": "边音", "r": "颤音", "ɾ": "闪音", "j": "近音", "äi̯": "元音",
	}
	for ipa, want := range cases {
		if got := mannerFromIPA(ipa); got != want {
			t.Fatalf("%q → %q，期望 %q", ipa, got, want)
		}
	}
}
