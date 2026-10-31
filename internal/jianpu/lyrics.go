package jianpu

import (
	"strings"
	"unicode"
)

// lyricPunct 是会被"摘出来"的尾部标点。
//
// ⚠️ 这张表**不能收 `(`** —— 上游 test 20 明确断言 `喜洋洋~(欧…` 的第 3 个音节是 `洋(`
// 且它的 `trailingPunctuation` 是 **undefined**（`(` 是并进文字里的，不是标点）。
var lyricPunct = map[rune]bool{
	'，': true, '。': true, '、': true, '；': true, '：': true, '！': true, '？': true,
	'…': true, '·': true, '「': true, '」': true, '“': true, '”': true,
	',': true, '.': true, ';': true, ':': true, '!': true, '?': true,
}

// splitSyllables 把一条 `C:` 行的正文切成音节。
//
// 规则（都是照上游测试定的）：
//
//	`/`        断音节（`春~天/hello/world` ⇒ 春天 · hello · world）
//	空格/制表  也断
//	`@`        占位：**产出一个空音节**（它占一个音符位）
//	`~`        把前后并成**一个**音节（`洋~(` ⇒ `洋(`）
//	`{` `}`    打花括号标记，**不占**音符位（`{甲乙}丙` ⇒ 甲(左) · 乙(右) · 丙）
//	汉字       一个字一个音节
//	拉丁串     **一整串**算一个音节（`ABC` ⇒ `ABC`，不拆字母）
//	尾部标点   摘进 `TrailingPunctuation`（`你，` ⇒ `你` + `，`）
func splitSyllables(s string) []Syllable {
	var out []Syllable
	var cur strings.Builder
	curOpen, curLatin, curLeft, curRight := false, false, false, false
	pendingLeft, mergeNext := false, false

	push := func() {
		if !curOpen {
			return
		}
		text, punct := splitTrailingPunct(cur.String())
		cur.Reset()
		out = append(out, Syllable{
			Text:                text,
			TrailingPunctuation: punct,
			LeftBrace:           curLeft,
			RightBrace:          curRight,
		})
		curOpen, curLatin, curLeft, curRight = false, false, false, false
	}
	start := func(latin bool) {
		curOpen, curLatin = true, latin
		curLeft = pendingLeft
		pendingLeft = false
	}
	appendRune := func(r rune) {
		if !curOpen {
			start(false)
		}
		cur.WriteRune(r)
	}

	for _, r := range s {
		switch {
		case r == '/' || r == ' ' || r == '\t':
			push()
			mergeNext = false
		case r == '@':
			push()
			start(false)
			push() // 空音节：占一个音符位
			mergeNext = false
		case r == '{':
			if curOpen {
				curLeft = true
			} else {
				pendingLeft = true
			}
		case r == '}':
			if curOpen {
				curRight = true
			} else if n := len(out); n > 0 {
				out[n-1].RightBrace = true
			}
		case r == '~':
			mergeNext = true
		case isHan(r):
			if mergeNext {
				appendRune(r)
			} else {
				push()
				start(false)
				cur.WriteRune(r)
			}
			mergeNext = false
		case isLatinRune(r):
			if mergeNext {
				appendRune(r)
			} else if curOpen && curLatin {
				cur.WriteRune(r)
			} else {
				push()
				start(true)
				cur.WriteRune(r)
			}
			mergeNext = false
		case lyricPunct[r]:
			// 标点跟着**前一个**音节（`你，` 是一段）；前面没有就自成一段
			appendRune(r)
			mergeNext = false
		default:
			// 别的字符（`(` 之类）：`~` 要求并进去就并，否则自成一段
			if mergeNext {
				appendRune(r)
			} else {
				push()
				start(false)
				cur.WriteRune(r)
			}
			mergeNext = false
		}
	}
	push()
	return out
}

func isHan(r rune) bool { return unicode.Is(unicode.Han, r) }

func isLatinRune(r rune) bool {
	return unicode.Is(unicode.Latin, r) || (r >= '0' && r <= '9')
}

// splitTrailingPunct 把末尾连续的一串标点摘出来。
func splitTrailingPunct(text string) (string, string) {
	rs := []rune(text)
	i := len(rs)
	for i > 0 && lyricPunct[rs[i-1]] {
		i--
	}
	if i == len(rs) {
		return text, ""
	}
	return string(rs[:i]), string(rs[i:])
}
