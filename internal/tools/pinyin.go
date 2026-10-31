package tools

import (
	"strings"
	"sync"
)

// 拼音/声调 —— **纯本地**，不碰 ACE。
//
// 数据源是生成的 `pinyin_table.go`（见 `tools/genpinyin.js`）：U+4E00..U+9FFF 连续一张表，
// 下标 = r - 0x4E00，项内 `,` 分的第一个读音 = **pinyin-pro `multiple:true` 的首选**。
//
// ⚠️ 为什么不能用 ACE 现成的：实测 ACE **没有语调面** ——
//   · `help --search intonation` → 零匹配；
//   · `phoneme g2p`（能汉字→音素）**只给音素、不给声调**，而且 `contextSupported: false`
//     （中文不做上下文消歧）。
// 所以声调只能本地算。反过来 `phoneme g2p` 补的是另一半（**ACE 实际会唱什么音素**），
// 两个合起来才是完整的"这个字唱成什么"。

// toneInfo 是一个声调的四样说法（都是普通话的标准叫法，别自己编）。
type toneInfo struct {
	Name  string // 阴平 / 阳平 / 上声 / 去声 / 轻声
	Value string // 调值（赵元任五度标记法）
	Word  string // 音高走向的说法
}

var toneInfos = map[int]toneInfo{
	1: {"阴平", "55", "高平"},
	2: {"阳平", "35", "上升"},
	3: {"上声", "214", "降升"},
	4: {"去声", "51", "下降"},
	// ⚠️ 轻声同时登记在 **5** 与 **0** 下：对外口径是 5（与 SV 的 rapAccent 一致），
	// 而 pinyin-pro 内部用 0。两边都认，免得某个下游拿到 0 就变成 "?"（测试抓过这条）。
	5: {"轻声", "—", "轻短"},
	0: {"轻声", "—", "轻短"},
}

// toneInfoOf 取声调信息；认不出的声调回一个不会骗人的空壳。
func toneInfoOf(t int) toneInfo {
	if ti, ok := toneInfos[t]; ok {
		return ti
	}
	return toneInfo{Name: "?", Value: "?", Word: "?"}
}

// ---------- 读音表 ----------

var (
	pinyinOnce sync.Once
	pinyinRows []string
)

// pinyinTable 懒解析。**故意不做 init()** —— 不用这个工具就不该为它付启动成本。
func pinyinTable() []string {
	pinyinOnce.Do(func() {
		pinyinRows = strings.Split(strings.Join(pinyinChunks[:], ""), "\n")
	})
	return pinyinRows
}

// readingsOf 取一个汉字的**全部**读音（带调号，第一个是首选）。不是汉字或表里没有就回 nil。
func readingsOf(r rune) []string {
	if r < pinyinLo || r > pinyinHi {
		return nil
	}
	t := pinyinTable()
	i := int(r - pinyinLo)
	if i < 0 || i >= len(t) || t[i] == "" {
		return nil
	}
	raw := strings.Split(t[i], ",")
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		if p != "" {
			out = append(out, markTone(p))
		}
	}
	return out
}

// isHanzi 判断是不是"表里管得着"的汉字。
func isHanzi(r rune) bool {
	return r >= pinyinLo && r <= pinyinHi
}

// ---------- 声调：数字 → 调号 ----------

// toneMarks 是六个元音的四声调号（轻声不加号，所以不需要第 5 行）。
var toneMarks = map[rune][4]rune{
	'a': {'ā', 'á', 'ǎ', 'à'},
	'o': {'ō', 'ó', 'ǒ', 'ò'},
	'e': {'ē', 'é', 'ě', 'è'},
	'i': {'ī', 'í', 'ǐ', 'ì'},
	'u': {'ū', 'ú', 'ǔ', 'ù'},
	'ü': {'ǖ', 'ǘ', 'ǚ', 'ǜ'},
}

// markTone 把 pinyin-pro 的 `yan4` / `de0` / `lü4` 变成 `yàn` / `de` / `lǜ`。
//
// 标调位置按《汉语拼音方案》的通行规则：
//
//	有 a 标 a；没 a 有 o/e 就标 o/e；iu 标在 u 上；ui 标在 i 上；其余标在**最后一个**元音上。
//
// 轻声（0）不加调号。认不出声调数字就原样返回（宁可不动，也别猜）。
func markTone(p string) string {
	if p == "" {
		return p
	}
	last := p[len(p)-1]
	if last < '0' || last > '9' {
		return p // 没有声调数字（非汉字之类）⇒ 原样
	}
	tone := int(last - '0')
	body := p[:len(p)-1]
	if tone == 0 || tone > 4 {
		return body // 轻声：去掉那个 0
	}
	runes := []rune(body)
	// 选要标调的元音
	idx := -1
	for i, r := range runes {
		if r == 'a' {
			idx = i
			break
		}
	}
	if idx < 0 {
		for i, r := range runes {
			if r == 'o' || r == 'e' {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		// iu / ui：标准规则是标在**后面那个**上（liú 标 u、huì 标 i）
		// ⛔ 这里踩过：ui 我一开始标到了 u 上（huì → hùi），被用例抓住。
		for i := 0; i+1 < len(runes); i++ {
			if (runes[i] == 'i' && runes[i+1] == 'u') || (runes[i] == 'u' && runes[i+1] == 'i') {
				idx = i + 1
				break
			}
		}
	}
	if idx < 0 {
		for i := len(runes) - 1; i >= 0; i-- {
			if _, ok := toneMarks[runes[i]]; ok {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		// 一个能标调的元音都没有（如 `lv4` —— 表里用的是 `lü4`，不该出现）
		// ⇒ **原样返回**，别把声调数字悄悄吃掉（吃掉就丢信息了）
		return p
	}
	if m, ok := toneMarks[runes[idx]]; ok {
		runes[idx] = m[tone-1]
	}
	return string(runes)
}

// toneOf 从一个带调号的读音里取声调号。
//
// ⚠️ **口径统一成 1..5**（1 阴平 · 2 阳平 · 3 上声 · 4 去声 · **5 轻声**），
// 与 SV 那边 `Note#setRapAccent` 的口径一致（见 skills 里那条"1 阴平 2 阳平 3 上声 4 去声 5 轻声"）。
// pinyin-pro 内部用 **0** 表示轻声，在 markTone 里已经消化掉了 —— 对外只出现 1..5。
//
// 为什么从**调号**反推而不是另存一个字段：调号是给人看、给旋律对的最终形态，
// 两者必须一致；反推就天然一致，省得存两份还怕不同步。
func toneOf(p string) int {
	for _, r := range p {
		switch r {
		case 'ā', 'ō', 'ē', 'ī', 'ū', 'ǖ':
			return 1
		case 'á', 'ó', 'é', 'í', 'ú', 'ǘ':
			return 2
		case 'ǎ', 'ǒ', 'ě', 'ǐ', 'ǔ', 'ǚ':
			return 3
		case 'à', 'ò', 'è', 'ì', 'ù', 'ǜ':
			return 4
		}
	}
	if p == "" {
		return 0
	}
	return 5 // 有拼音但一个调号都没有 ⇒ 轻声
}

// unmarkTone 去掉调号，得到不带声调的拼音（声母韵母本身）。
func unmarkTone(p string) (string, bool) {
	var b strings.Builder
	found := false
	for _, r := range p {
		if repl, ok := plainVowel[r]; ok {
			b.WriteRune(repl)
			found = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String(), found
}

// plainVowel 是带调元音 → 不带调的。
var plainVowel = map[rune]rune{
	'ā': 'a', 'á': 'a', 'ǎ': 'a', 'à': 'a',
	'ō': 'o', 'ó': 'o', 'ǒ': 'o', 'ò': 'o',
	'ē': 'e', 'é': 'e', 'ě': 'e', 'è': 'e',
	'ī': 'i', 'í': 'i', 'ǐ': 'i', 'ì': 'i',
	'ū': 'u', 'ú': 'u', 'ǔ': 'u', 'ù': 'u',
	'ǖ': 'ü', 'ǘ': 'ü', 'ǚ': 'ü', 'ǜ': 'ü',
}

// ---------- 歌词常用读法（**人工小表**，默认不用） ----------

// lyricReading 是「歌词里通常怎么读」与「词典给的首选」不一致的**少数几个字**。
//
// ⛔ 这是一张**人工表**，不是数据源推出来的 —— 所以：
//
//	· 默认**不启用**（`toneMode=library` 用数据源的首选，可复现、有据可查）；
//	· 只有显式选 `toneMode=lyric` 才用它，且报告里会说明"这几个字用的是歌词常用读法"。
//
// 只收**证据清楚**的虚字/多音字；拿不准的一律不收（宁可让用户用 overrides 手动指定）。
var lyricReading = map[rune]string{
	'了': "le",  // 虚字助词：走了、来了（词典首选是 liao3「了解」）
	'地': "de",  // 状语助词：慢慢地走（词典首选是 di4「土地」）
	'得': "de",  // 补语助词：跑得快（词典首选是 de2「得到」）
	'着': "zhe", // 进行态助词：看着（首选 zhe0 本来就对，这里显式钉住）
	'的': "de",
	'呢': "ne",
	'吗': "ma",
	'吧': "ba",
	'啊': "a",
	'么': "me", // 什么 / 怎么
}

// ---------- 语调轮廓（给"倒字"参考用） ----------

// contourArrow 是声调的走向符号 —— 单列一个函数方便测试。
func contourArrow(tone int) string {
	switch tone {
	case 1:
		return "—"
	case 2:
		return "↗"
	case 3:
		return "↘↗"
	case 4:
		return "↘"
	}
	return "·"
}
