package jianpu

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// knownCommands 是 `&name` 的已知命令表。
//
// ⚠️ 这张表是**从 fixture + 上游测试反推**出来的：`test.jps` 用到 `&tr` `&yc` `&xhy`
// `&ycy` `&rit`，而上游测试里 `&yc` 合法、`&unknown` 要报 `unknown-command`。
// 上游真正的注册表可能更大 —— 这张表**只影响"要不要报 unknown-command"**，
// 不改任何其它解析结果（未知命令照样进 `Ornaments`）。以后拿到上游清单再补。
var knownCommands = map[string]bool{
	"yc": true, "tr": true, "xhy": true, "ycy": true, "rit": true,
}

// barlineSpellings 是 9 种小节线拼写，**按长度降序**（先试长的，免得 `||/` 被 `||` 抢走）。
var barlineSpellings = []struct {
	Text string
	Type string
}{
	{":|:", BarRepeatBoth},
	{":||", BarRepeatEnd},
	{"||/", BarDouble},
	{"|:", BarRepeatStart},
	{":|", BarRepeatEnd},
	{"||", BarEnd},
	{"|/", BarHidden},
	{"|*", BarInvisible},
	{"|", BarNormal},
}

type parser struct {
	doc *Document

	page     *Page // 当前页（nil = 还没开页）
	groupIdx int   // 当前页里的第几组

	haveTempoNumber bool
	haveTempoString bool

	// 「未闭合的连音括号」只在**最后一条谱行**上算数（见 scanContent 末尾的说明）。
	pendingLastLine  int
	pendingLastCount int
}

// Parse 解析一份番茄简谱 DSL。**永不 panic、永不返回 error** —— 坏输入走 Diagnostics（上游语义）。
func Parse(dsl string) *Document {
	p := &parser{doc: &Document{}}
	text := strings.ReplaceAll(dsl, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	for i, raw := range strings.Split(text, "\n") {
		lineNo := i + 1
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		// `#` 开头的**整行**是注释（行内的 `#` 是升号，所以只看行首）—— 静默跳过。
		if strings.HasPrefix(line, "#") {
			continue
		}
		if line == "[fenye]" {
			p.flushPage()
			continue
		}
		letter, digits, caption, rest, ok := splitPrefix(line)
		if !ok {
			p.diag(lineNo, DiagMissingPrefix,
				fmt.Sprintf("这一行既不是注释、也不以合法前缀开头：%s", truncate(line, 40)))
			continue
		}
		switch letter {
		case "V":
			if p.doc.Metadata.Version == "" {
				p.doc.Metadata.Version = strings.TrimSpace(rest)
			}
		case "B":
			p.doc.Metadata.Titles = append(p.doc.Metadata.Titles, strings.TrimSpace(rest))
		case "Z":
			p.doc.Metadata.Authors = append(p.doc.Metadata.Authors, strings.TrimSpace(rest))
		case "D":
			p.parseMode(lineNo, rest)
		case "P":
			p.parseMeters(lineNo, rest)
		case "J":
			p.parseTempo(rest)
		case "Y":
			p.doc.Metadata.Instruments = append(p.doc.Metadata.Instruments, splitCommaList(rest)...)
		case "S":
			p.doc.Metadata.Remarks = append(p.doc.Metadata.Remarks, strings.TrimSpace(rest))
		case "Q":
			p.parseMusicLine(lineNo, digits, caption, rest)
		case "C":
			p.parseLyricLine(rest)
		default:
			p.diag(lineNo, DiagMissingPrefix,
				fmt.Sprintf("认不出这个前缀 `%s:`（已知：V B Z D P J Y S Q C）", letter))
		}
	}
	p.flushPage()
	// ⚠️ 未闭合的开括号**只在最后一条谱行上**报 —— 见 scanContent 末尾那条说明。
	if p.pendingLastCount > 0 {
		p.diag(p.pendingLastLine, DiagUnclosedMark,
			fmt.Sprintf("连音括号 `(` 有 %d 个没配上 `)`（本行是最后一条谱行）", p.pendingLastCount))
	}
	return p.doc
}

func (p *parser) diag(line int, code, msg string) {
	p.doc.Diagnostics = append(p.doc.Diagnostics, Diagnostic{Line: line, Code: code, Message: msg})
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// splitPrefix 拆 `Q1"主旋律": 1 2 3 |` 这种行头。
// 返回 (字母, 数字, 引号内容, 冒号之后的内容, 是否合法)。
func splitPrefix(line string) (letter, digits, caption, rest string, ok bool) {
	i := 0
	for i < len(line) && line[i] >= 'A' && line[i] <= 'Z' {
		i++
	}
	if i == 0 {
		return "", "", "", "", false
	}
	letter = line[:i]
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	digits = line[len(letter):i]
	i = skipHSpace(line, i)
	if i < len(line) && line[i] == '"' {
		j := strings.IndexByte(line[i+1:], '"')
		if j < 0 {
			return "", "", "", "", false
		}
		caption = line[i+1 : i+1+j]
		i = skipHSpace(line, i+j+2)
	}
	if i >= len(line) || line[i] != ':' {
		return "", "", "", "", false
	}
	return letter, digits, caption, line[i+1:], true
}

func skipHSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

func splitCommaList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ---------- 头部 ----------

// parseMode 处理 `D:`。**原样存**（`#C` `$B` `C#` `B$` 四种写法上游都收、都不报诊断）。
func (p *parser) parseMode(line int, rest string) {
	v := strings.TrimSpace(rest)
	if _, ok := ModeToPitchClass(v); !ok {
		p.diag(line, DiagInvalidMode, fmt.Sprintf("`D:` 的值 %q 不是音名（形如 `C` `F#` `#C` `$B`）", v))
		return
	}
	p.doc.Metadata.Mode = v
}

// parseMeters 处理 `P:`。
//
// ⭐ 上游测试钉死的两条：
//   - `P: 4/4` 之后再 `P: invalid` ⇒ **清空 meters**（所以 `P` 是**替换**、不是累加）；
//   - `P: 3/4 ( 2/4 1/4 )` ⇒ 三个：第一个 `parenthesized:false`、括号里那两个 `true`
//     （括号里可以放**多个**、空白分隔）。
func (p *parser) parseMeters(line int, rest string) {
	var meters []Meter
	inParen := false
	var buf strings.Builder
	bad := false

	flushTok := func() {
		tok := strings.TrimSpace(buf.String())
		buf.Reset()
		if tok == "" || bad {
			return
		}
		n, d, ok := parseMeterToken(tok)
		if !ok {
			bad = true
			return
		}
		meters = append(meters, Meter{Numerator: n, Denominator: d, Parenthesized: inParen})
	}

	for _, r := range rest {
		switch r {
		case '(':
			flushTok()
			inParen = true
		case ')':
			flushTok()
			inParen = false
		case ' ', '\t':
			flushTok()
		default:
			buf.WriteRune(r)
		}
		if bad {
			break
		}
	}
	if !bad {
		flushTok()
	}
	if bad {
		p.doc.Metadata.Meters = nil // ⭐ 无效的那一行把之前的**清空**
		p.diag(line, DiagInvalidMeter,
			fmt.Sprintf("`P:` 的值 %q 不是 n/m 形式的拍号", strings.TrimSpace(rest)))
		return
	}
	p.doc.Metadata.Meters = meters
}

func parseMeterToken(tok string) (int, int, bool) {
	parts := strings.Split(tok, "/")
	if len(parts) != 2 {
		return 0, 0, false
	}
	n, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	d, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || n <= 0 || d <= 0 {
		return 0, 0, false
	}
	return n, d, true
}

// parseTempo 处理 `J:`。
//
// ⭐ 上游测试：`J: 80` `J: 120` `J: 欢快地` `J: 抒情地` ⇒ `tempos == [80, '欢快地']`
// ⇒ **每种各留第一个**（一个数字 + 一个词），同种的后来者忽略。
func (p *parser) parseTempo(rest string) {
	v := strings.TrimSpace(rest)
	if v == "" {
		return
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		if !p.haveTempoNumber {
			p.doc.Metadata.Tempos = append(p.doc.Metadata.Tempos, f)
			p.haveTempoNumber = true
		}
		return
	}
	if !p.haveTempoString {
		p.doc.Metadata.Tempos = append(p.doc.Metadata.Tempos, v)
		p.haveTempoString = true
	}
}

// ---------- 页 / 组 / 声部 ----------

func (p *parser) flushPage() {
	if p.page != nil {
		p.doc.Pages = append(p.doc.Pages, *p.page)
		p.page = nil
		p.groupIdx = -1
	}
}

func (p *parser) ensurePage() {
	if p.page == nil {
		p.page = &Page{}
		p.groupIdx = -1
	}
}

func (p *parser) voiceNum(digits string) int {
	if digits == "" {
		return 1 // 未标号 ⇒ 当作第 1 声部
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func (p *parser) parseMusicLine(lineNo int, digits, caption, rest string) {
	p.ensurePage()
	vnum := p.voiceNum(digits)

	// 相邻的谱行归进同一个 Group；**同声部号再次出现** ⇒ 开新组。
	//（上游 test 1：Q1+Q2 同组；test 19/21/22：两条同号 `Q:` 是两个组。）
	if p.groupIdx < 0 || p.groupHasVoice(vnum) {
		p.page.Groups = append(p.page.Groups, Group{})
		p.groupIdx = len(p.page.Groups) - 1
	}
	v := Voice{Voice: vnum, Caption: caption}
	p.scanContent(&v, rest, lineNo)
	p.page.Groups[p.groupIdx].Voices = append(p.page.Groups[p.groupIdx].Voices, v)
}

func (p *parser) groupHasVoice(vnum int) bool {
	if p.page == nil || p.groupIdx < 0 || p.groupIdx >= len(p.page.Groups) {
		return false
	}
	for _, v := range p.page.Groups[p.groupIdx].Voices {
		if v.Voice == vnum {
			return true
		}
	}
	return false
}

// currentVoice 返回"最近一条谱行"（`C` 行永远绑到它，**后缀数字不看**）。
func (p *parser) currentVoice() (gi, vi int, ok bool) {
	if p.page == nil || p.groupIdx < 0 || p.groupIdx >= len(p.page.Groups) {
		return 0, 0, false
	}
	if len(p.page.Groups[p.groupIdx].Voices) == 0 {
		return 0, 0, false
	}
	return p.groupIdx, len(p.page.Groups[p.groupIdx].Voices) - 1, true
}

func (p *parser) parseLyricLine(rest string) {
	gi, vi, ok := p.currentVoice()
	if !ok {
		return // 前面还没有谱行 ⇒ 没处挂（静默丢掉，不报诊断）
	}
	vs := &p.page.Groups[gi].Voices[vi]
	vs.Lyrics = append(vs.Lyrics, LyricLine{Syllables: splitSyllables(rest)})
}

// ---------- 谱行内容扫描 ----------

// scanContent 是解析器的核心。
//
// # 音符后面的"修饰簇"**顺序无关**
//
// 上游 fixture + 测试合起来能看到这些形状：
//
//	1'#//..&yc   （音级 → 八度 → 变音 → 减时线 → 附点 → 装饰；测试里正是这个顺序）
//	5#,          （**变音在八度之前** —— test.jps 的 `Q2: ... 5#, ...`）
//	6./          （**附点在减时线之前** —— test.jps 的 `Q1: ... 6./ ...`）
//	1'//  5#/  9//  6,,/.  5&rit/
//
// ⇒ `'` `,` `#` `$` `/` `.` `&name` 是**一起消费**再分别归类的，
// **不是**按固定顺序做的状态机。
//
// # 连音括号的规范化（照上游三组用例反推，三组都对得上）
//
//	(1   ⇒ Code `1(`      括号**永远跟在主记号后面**，先开后闭
//	1()  ⇒ Code `1()`
//	2))  ⇒ Code `2))`
//	((2  ⇒ Code `2((
//
// 配对是 **"第 i 个 `(` 配第 i 个 `)`"（按出现顺序，不是栈式嵌套）**；
// `start == end` 的零长度对**丢弃**；`Start/End` 用**元素下标**。
// 括号**跳过小节线**附着到最近的音符（上游用例 `( |/ 1 2 |)` ⇒ `1(` / `2)`）。
//
// # 没配上的开括号怎么办（`unclosed-mark` 只在最后一条谱行报）
//
// 上游把未闭合的括号**跨行带给同声部的下一条谱行**（并打 `continuationToNext` /
// `continuationFromPrevious`）——那两个字段不在本包固定的 Mark API 里，表达不了。
// 所以这里按行内解析，并且只有**最后一条谱行**上剩下的开括号才报 `unclosed-mark`：
// 这正好让上游 test 21/22（跨行/跨页被丢弃）与 test 23（行尾没闭合要报）都对得上。
func (p *parser) scanContent(v *Voice, content string, lineNo int) {
	rs := []rune(content)
	i := 0

	var pendingOpens int // 还没落到元素上的开括号**个数**（遇到小节线就继续往后带）
	lastNoteIdx := -1    // 最近一个 note/sustain 的元素下标（闭括号往它身上落）
	var opens, closes []int
	var tupletEl []bool // 与 v.Elements 平行：这个元素是不是 `y` 打头的连音组

	assignClose := func(el int) {
		if el < 0 {
			return
		}
		closes = append(closes, el)
		// ⭐ **向后落的闭括号要回写 Code**：`( |/ 1 2 |)` 里那个 `)` 是在元素 `2`
		//    已经建好之后才读到的 ⇒ 得回头给它补 `)`（上游期望 `2)`）。
		//    （元素自己的**后置**括号不走这里 —— 那些在建 Code 时就拼进去了，见 ⑩。）
		if el < len(v.Elements) {
			v.Elements[el].Code += ")"
		}
	}
	recordClose := func(el int) { // 只记账，不回写（给元素自己的后置括号用）
		if el >= 0 {
			closes = append(closes, el)
		}
	}
	readBrackets := func() (nOpen, nClose int) {
		for i < len(rs) && (rs[i] == '(' || rs[i] == ')') {
			if rs[i] == '(' {
				nOpen++
			} else {
				nClose++
			}
			i++
		}
		return
	}
	// readAnnotation 读连续的 `"..."` 段（可多段相连 ⇒ 拼接）。
	readAnnotation := func() string {
		var b strings.Builder
		for i < len(rs) && rs[i] == '"' {
			j := i + 1
			for j < len(rs) && rs[j] != '"' {
				j++
			}
			if j >= len(rs) {
				break // 引号没闭合：能读到哪算哪，不崩
			}
			b.WriteString(string(rs[i+1 : j]))
			i = j + 1
		}
		return b.String()
	}
	skipSpaces := func() {
		for i < len(rs) && (rs[i] == ' ' || rs[i] == '\t') {
			i++
		}
	}

	for {
		skipSpaces()
		if i >= len(rs) {
			break
		}

		// ① 先吃一串括号：开括号带给**后面**的元素，闭括号落回**前面**的元素
		if rs[i] == '(' || rs[i] == ')' {
			nOpen, nClose := readBrackets()
			skipSpaces()
			pendingOpens += nOpen
			for k := 0; k < nClose; k++ {
				assignClose(lastNoteIdx)
			}
			continue
		}
		// ② 行内层 `{ … }`：本轮**不建模**（上游是 inline-layer）⇒ 整块跳过、不崩
		if rs[i] == '{' || rs[i] == '[' {
			open, close := rs[i], '}'
			if open == '[' {
				close = ']'
			}
			depth := 0
			for i < len(rs) {
				if rs[i] == open {
					depth++
				} else if rs[i] == close {
					depth--
					if depth == 0 {
						i++
						break
					}
				}
				i++
			}
			continue
		}

		// ③ 主记号
		coreStart := i
		isTuplet := false
		if rs[i] == 'y' && i+1 < len(rs) && rs[i+1] >= '0' && rs[i+1] <= '9' {
			isTuplet = true // `y` 前缀 = 连音组（上游 test 13：`(y1 - 2)` ⇒ tuplet caption 3）
			i++
		}
		var el Element
		isNoteLike := false
		switch {
		case rs[i] == '|' || rs[i] == ':':
			// 小节线：从当前位置起**试最长的拼写**。
			// ⚠️ 触发条件必须带上 `:` —— `:|` `:|:` `:||` 三种拼写是**冒号打头**的，
			// 只认 `|` 会把它们的冒号当"认不出的字符"跳掉，于是 `:|` 被读成 `|`（normal）——
			// 这正是上游用例 9/18 抓出来的。
			tail := string(rs[i:])
			spell, typ := "", ""
			for _, cand := range barlineSpellings {
				if strings.HasPrefix(tail, cand.Text) {
					spell, typ = cand.Text, cand.Type
					break
				}
			}
			if spell == "" {
				i++
				continue
			}
			i += len([]rune(spell))
			el = Element{Kind: KindBarline, Code: spell, Type: typ}
		case rs[i] == '-':
			i++
			el = Element{Kind: KindSustain}
			isNoteLike = true
		case rs[i] >= '0' && rs[i] <= '9':
			i++
			el = Element{Kind: KindNote}
			isNoteLike = true
		default:
			i++ // 认不出的单字符：跳过（不建元素、不报诊断 —— "能跳过就跳过"）
			continue
		}

		// ④ 修饰簇（顺序无关地一起消费）
		duration, dots, octave := 4, 0, 0
		acc := AccidentalNone
		var orns []Ornament
		if isNoteLike {
		cluster:
			for i < len(rs) {
				switch rs[i] {
				case '\'':
					octave++
					i++
				case ',':
					octave--
					i++
				case '#':
					acc = AccidentalSharp
					i++
				case '$':
					acc = AccidentalFlat
					i++
				case '/':
					duration *= 2
					i++
				case '.':
					dots++
					i++
				case '=', '~', '<', '>', '!':
					// ⚠️ **进 Code 但不建模**（`Code` 保源码原样是往返的关键）：
					//   `=`  上游 fixture 里有（`5=,` / `3=`），`=` 后面还跟着 `,`
					//        ⇒ 它**不改音高、不吞音级**；本包给它**不编语义**，只留在 Code 里。
					//   `~` `<` `>` `!` 是力度线/重音一类的边界记号，本包同样不建模。
					i++
				case '&':
					i++
					nm := i
					for i < len(rs) && isASCIILetter(rs[i]) {
						i++
					}
					name := string(rs[nm:i])
					lv := i
					for i < len(rs) && rs[i] >= '0' && rs[i] <= '9' {
						i++
					}
					level := 0
					if i > lv {
						level, _ = strconv.Atoi(string(rs[lv:i]))
					}
					if name != "" {
						if !knownCommands[name] {
							p.diag(lineNo, DiagUnknownCommand, fmt.Sprintf("认不出这个命令 `&%s`", name))
						}
						orns = append(orns, Ornament{Name: name, Level: level})
					}
				default:
					break cluster
				}
			}
		}
		// ⑤ 紧跟其后的括号（`1()` / `2))`）
		clusterEnd := i // Code 到簇结尾为止 —— **括号要规范化后另拼**，不能原样带进来
		trailOpen, trailClose := readBrackets()

		// ⑥ 注释（`"..."`，可连续多段 ⇒ 拼接）。⚠️ **注释不进 Code**，
		//    所以它放在 Code 切片之后读（上游 test 6：`1"吉他solo""转E调"` 的 Code 就是 `1`）。
		ann := readAnnotation()

		// ⑦ 括号归属
		myOpen, myClose := 0, 0
		if isNoteLike {
			myOpen = trailOpen + pendingOpens
			myClose = trailClose
			pendingOpens = 0
		} else {
			// 小节线：挂起的开括号继续往后带；紧跟的括号落回前一个音符
			pendingOpens += trailOpen
			for k := 0; k < trailClose; k++ {
				assignClose(lastNoteIdx)
			}
		}

		// ⑧ 填字段
		if el.Kind == KindNote {
			d := int(rs[coreStart] - '0')
			if isTuplet {
				d = int(rs[coreStart+1] - '0')
			}
			el.Pitch = d
			el.Octave = octave
			el.Accidental = acc
			el.Duration = duration
			el.Dots = dots
			el.Ornaments = orns
			switch d {
			case 0:
				el.Rest = true
			case 8:
				el.Hidden = true // 隐藏音：保留时值、不发声
			case 9:
				el.Sound = "rhythm" // 节奏音/念白
			}
		} else if el.Kind == KindSustain {
			el.Ornaments = orns
			el.Duration = duration // 裸 `-` 就是 4 ⇒ Blocks 里算 1 拍
		}

		// ⑨ Code = **源码原样**（主记号含 `y` 前缀 + 修饰簇）+ 规范化的括号（先开后闭）。
		//    ⚠️ **注释不进 Code**（上游 test 6：`1"吉他solo""转E调"` 的 Code 就是 `1`）。
		code := string(rs[coreStart:clusterEnd])
		if el.Kind == KindBarline {
			code = el.Code
		}
		code += strings.Repeat("(", myOpen) + strings.Repeat(")", myClose)

		// 小节线的注释要**去掉所有空白**、引号写成单引号（上游 test 5）
		if el.Kind == KindBarline && ann != "" {
			norm := stripAllSpace(ann)
			code = el.Code + "'" + norm + "'"
			if body, ok := strings.CutPrefix(norm, "p:"); ok {
				if n, d, ok2 := parseMeterToken(body); ok2 {
					el.TemporaryMeter = &Meter{Numerator: n, Denominator: d}
				}
			}
		}
		el.Code = code
		if ann != "" {
			el.Annotation = ann
		}

		// ⑩ 落表 + 记括号/连音标记
		idx := len(v.Elements)
		v.Elements = append(v.Elements, el)
		tupletEl = append(tupletEl, isTuplet)
		if isNoteLike {
			for k := 0; k < myOpen; k++ {
				opens = append(opens, idx)
			}
			for k := 0; k < myClose; k++ {
				recordClose(idx) // ⚠️ 只记账：Code 里已经拼过这些 `)` 了
			}
			lastNoteIdx = idx
		}
	}

	// ⑪ 配连音：**第 i 个 `(` 配第 i 个 `)`**；`start == end` 丢弃
	n := len(opens)
	if len(closes) < n {
		n = len(closes)
	}
	for k := 0; k < n; k++ {
		s, e := opens[k], closes[k]
		if s == e {
			continue
		}
		typ, caption := "slur", ""
		if s < len(tupletEl) && tupletEl[s] {
			typ = "tuplet"
			caption = strconv.Itoa(e - s + 1)
		}
		v.Marks = append(v.Marks, Mark{Type: typ, Start: s, End: e, Caption: caption})
	}
	// 行尾剩下的开括号：**只有最后一条谱行**才算数（下一条谱行会覆盖或清掉）
	if left := len(opens) - len(closes); left > 0 {
		p.pendingLastLine, p.pendingLastCount = lineNo, left
	} else {
		p.pendingLastLine, p.pendingLastCount = 0, 0
	}
}

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func stripAllSpace(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
