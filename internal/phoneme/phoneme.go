// Package phoneme 提供音素的展示信息：IPA、官方四分类、发音方式、例词。
//
// 数据来源：
//   - table.go 是官方仓 timedomain-tech/ACE_phonemes 的 mapping 表生成结果（不含例词的符号由 SV 表补）；
//   - 官方表对中文儿化/少见韵母覆盖不全（74 个韵母里缺 24 个），缺失的由 Describe 用 CLI 的
//     `phoneticsHints`（音素→IPA）现推分类与发音方式，仍查不到就只显示符号本身 —— 不编造。
package phoneme

import (
	"unicode"
	"regexp"
	"strings"
)

// Describe 取一个音素的展示信息。
//
//	lang  语言键：zh / jp / eng / spa（与 Table 一致）
//	sym   ACE 音素符号（裸 token）
//	ipa   CLI `phoneme inventory` 的 phoneticsHints[sym]，可能为空
//
// 返回的 Entry 保证 Symbol 字段等于 sym；其余字段查不到就是空串。
func Describe(lang, sym, ipa string) Entry {
	e, ok := Table[lang][sym]
	if !ok {
		e = Entry{Symbol: sym}
	}
	if ipa != "" {
		e.IPA = ipa // CLI 的 IPA 最准（官方表里有些是并列写法，如 "ɔ/ɑ"）
	}
	if e.Manner == "" {
		e.Manner = mannerFromIPA(e.IPA)
	}
	if e.Manner == "" {
		if m, ok := specialManner[sym]; ok {
			e.Manner = m
		}
	}
	if e.Manner == "" && looksLikeVowel(sym) {
		e.Manner = "元音"
	}
	if e.Manner == "" && isErhua(sym) {
		e.Manner = "元音（儿化）"
	}
	if e.Official == "" {
		switch {
		case isErhua(sym) || looksLikeVowel(sym):
			e.Official = "Vowel"
		default:
			e.Official = ""
		}
	}
	return e
}

// specialManner 是官方表与 IPA 都说不清的少数符号（都是 ACE 自己的写法）。
var specialManner = map[string]string{
	"dx": "闪音", // 英语 flap t（butter）
	"cl": "塞音", // 日语 っ（喉塞）
	"ax": "元音", // 日语 弱化 a
}

var (
	reStop    = regexp.MustCompile(`^[pbtdkgʔ]$`)
	reAffric  = regexp.MustCompile(`^(tʃ|dʒ|ts|dz|tɕ|dʑ|ʈʂ|ɖʐ|tʂ|dʐ|tr|dr|pf)$`)
	reFric    = regexp.MustCompile(`^(f|v|s|z|ʃ|ʒ|θ|ð|x|ɣ|ç|h|ɸ|β|ɕ|ʑ|ʂ|ʐ|ʝ)$`)
	reNasal   = regexp.MustCompile(`^(m|n|ŋ|ɲ|ɳ|m̩|n̩|ŋ̍)$`)
	reLat     = regexp.MustCompile(`^(l|ɫ|ɭ)$`)
	reTrill   = regexp.MustCompile(`^(r|ʀ|ʁ)$`)
	reTap     = regexp.MustCompile(`^(ɾ|ɽ)$`)
	reApprox  = regexp.MustCompile(`^(j|w|ɥ|ɹ|ɻ|ʋ|ɰ|ɚ)$`)
	reVowelIP = regexp.MustCompile(`^[aeiouyæɑɒɔəɛɜɪʊʌɐɞɘɤɯøœɶɨʉɘɵɤ]`)
)

// normalizeIPA 去掉送气/腭化等附加符与元音的变体写法，只留判断发音方式需要的骨架。
//   tsʰ → ts、ä → a、ɛ → e、ɔ → o、ɑ → a、ɪ → i、ʊ → u、ʌ/ə → e
func normalizeIPA(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 0x0300 && r <= 0x036F: // 组合附加符（̃ ̥ ̩ 等）
			continue
		case unicode.Is(unicode.Lm, r): // ʰ ʲ ʷ ˈ ˑ …（修饰字母）
			continue
		case r == '·':
			continue
		}
		switch r {
		case 'ä', 'ɑ', 'ɐ', 'ʌ':
			b.WriteRune('a')
		case 'ɛ', 'ɜ', 'ə', 'ɘ', 'ɤ', 'ɵ', 'œ', 'ɶ':
			b.WriteRune('e')
		case 'ɔ', 'ɒ', 'ø':
			b.WriteRune('o')
		case 'ɪ', 'ɨ':
			b.WriteRune('i')
		case 'ʊ', 'ʉ', 'ɯ':
			b.WriteRune('u')
		case 'æ':
			b.WriteRune('e')
		case 'ɡ':
			b.WriteRune('g')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// mannerFromIPA 由 IPA 推发音方式（与 devtools/gen_phoneme_table.py 同一套规则）。
func mannerFromIPA(ipa string) string {
	s := strings.TrimSpace(ipa)
	if s == "" {
		return ""
	}
	// IPA 里可能写成 "ɔ/ɑ"、"äi̯" 这种并列/带附加符的，取第一段判断
	first := s
	if i := strings.IndexAny(first, "/,"); i > 0 {
		first = first[:i]
	}
	if i := strings.IndexAny(first, " \t("); i > 0 {
		first = first[:i]
	}
	first = strings.TrimSpace(normalizeIPA(first))
	switch {
	case reStop.MatchString(first):
		return "塞音"
	case reAffric.MatchString(first):
		return "塞擦音"
	case reFric.MatchString(first):
		return "擦音"
	case reNasal.MatchString(first):
		return "鼻音"
	case reLat.MatchString(first):
		return "边音"
	case reTrill.MatchString(first):
		return "颤音"
	case reTap.MatchString(first):
		return "闪音"
	case reApprox.MatchString(first):
		return "近音"
	}
	if reVowelIP.MatchString(first) {
		if strings.ContainsAny(first, "̯") { // 双元音的第二个成分
			return "元音（双元音）"
		}
		return "元音"
	}
	return ""
}

func isErhua(sym string) bool { return strings.HasSuffix(sym, "r") && len(sym) > 1 }

// looksLikeVowel 用 ACE 的韵母命名规律做最后兜底（ae/ao/ou/ei/ia/uo/v…）。
func looksLikeVowel(sym string) bool {
	for _, r := range sym {
		switch r {
		case 'a', 'e', 'i', 'o', 'u', 'v':
		default:
			return false
		}
	}
	return sym != ""
}
