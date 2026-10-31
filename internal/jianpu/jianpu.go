// Package jianpu 解析**番茄简谱**（Fanqie Jianpu）DSL。
//
// # 来源与许可
//
// 本包是 **open-fanqie** 的 `packages/core/src/parser.ts` 的 Go 移植。
//
//   - 上游项目：https://github.com/Linho1219/open-fanqie
//   - 上游文件：`packages/core/src/parser.ts`（TypeScript）
//   - 上游作者：Linho1219
//   - 许可：**MIT**
//
// 移植说明：上游是 TS 的 `tokenize → parseLine → 组装` 流水线；这里保持同一套
// 数据形状（JSON 标签与上游类型逐字段对齐），但实现改写成 Go 的逐行扫描 +
// "修饰簇无顺序消费"（见 parse.go 顶部），不引任何第三方库、只用标准库。
//
// ⚠️ 本包只做**解析**（DSL → 文档模型）与一个**派生**函数（文档 → 音符块，给 ACE 喂音符）。
// 不做排版/渲染、不做反向写出。
//
// # 已实现 / 明确未实现
//
// 已实现：头部 8 个前缀（`V B Z D P J Y S`）、`#` 注释、页面/分组/声部、音符与修饰簇、
// 9 种小节线拼写、`"..."` 注释（含小节线临时拍号）、连音括号、歌词行（`@ ~ / {}` 与尾部标点）、
// 4 类诊断。
//
// **明确未实现**（碰到位**不崩**，能跳过就跳过、能记 Code 就记）：
// 内联层 `{bz …}` / `{dsb …}`、`Y:` 简写层展开、反复展开、连音与房号**跨行**延续、
// 拍切分 `[...]`、力度线 `<` `>` `~`、倚音 `[2/]` `[h3]`、装饰音 `y` 前缀、
// tuplet 计数、SVG 排版渲染。
package jianpu

// Diagnostic 是一条诊断。**解析永不 panic、永不返回 error** —— 坏输入走这里（上游语义）。
type Diagnostic struct {
	Line    int    `json:"line"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// 诊断码（上游测试钉死的那几条）。
const (
	DiagMissingPrefix  = "missing-prefix"  // 一行既不是注释、也不以合法前缀开头
	DiagInvalidMode    = "invalid-mode"    // `D:` 的值不是音符名
	DiagInvalidMeter   = "invalid-meter"   // `P:` 的值不是 n/m
	DiagUnknownCommand = "unknown-command" // `&name` 不在已知命令表里
	DiagUnclosedMark   = "unclosed-mark"   // 连音括号没配上对
)

// Meter 是拍号。`P: 6/8 (2/4)` 会给两个：前者 `Parenthesized:false`、后者 `true`。
type Meter struct {
	Numerator     int  `json:"numerator"`
	Denominator   int  `json:"denominator"`
	Parenthesized bool `json:"parenthesized,omitempty"`
}

// Metadata 是头部（`V B Z D P J Y S`）。
type Metadata struct {
	Version     string   `json:"version,omitempty"`
	Titles      []string `json:"titles,omitempty"`
	Authors     []string `json:"authors,omitempty"`
	Mode        string   `json:"mode,omitempty"`
	Meters      []Meter  `json:"meters,omitempty"`
	Tempos      []any    `json:"tempos,omitempty"` // 数字或字符串（见测试：一个数字 + 一个词）
	Instruments []string `json:"instruments,omitempty"`
	Remarks     []string `json:"remarks,omitempty"`
}

// Accidental 是变音记号："" | "sharp" | "flat"。
type Accidental string

const (
	AccidentalNone  Accidental = ""
	AccidentalSharp Accidental = "sharp"
	AccidentalFlat  Accidental = "flat"
)

// Ornament 是装饰/命令记号（`&name`）。Level 是名字后面跟的数字（没有就是 0）。
type Ornament struct {
	Name  string `json:"name"`
	Level int    `json:"level"`
}

// GraceNote 是倚音。⚠️ 本轮**未解析**（`[2/]` / `[h3]` 属"明确未实现"），
// 类型保留是为了与上游文档模型逐字段对齐、给后续用。
type GraceNote struct {
	Pitch      int        `json:"pitch"`
	Octave     int        `json:"octave"`
	Accidental Accidental `json:"accidental,omitempty"`
	Duration   int        `json:"duration"`
	Dots       int        `json:"dots,omitempty"`
	After      bool       `json:"after,omitempty"`
}

// Element 的一个元素。Code 保留**源码原样**（上游也这么做，来回对账要用）。
//
// ⚠️ 关于 Code 的两处细节（都是照上游语义定的）：
//   - 连音括号会被**规范化后拼进 Code**：源码 `(1` ⇒ Code `1(`（括号永远跟在主记号后面，
//     先开后闭）。这从连音那三组用例反推出来，三组都对得上。
//   - 小节线上的注释会被**去空白**：源码 `|"p:2 / 4"` ⇒ Code `|'p:2/4'`（引号变单引号）。
type Element struct {
	Kind string `json:"kind"` // "note" | "sustain" | "barline"
	Code string `json:"code"`

	Pitch      int        `json:"pitch,omitempty"`
	Octave     int        `json:"octave,omitempty"`
	Duration   int        `json:"duration,omitempty"` // 音符时值的**分母**：四分=4、八分=8、十六分=16…
	Dots       int        `json:"dots,omitempty"`
	Accidental Accidental `json:"accidental,omitempty"`
	Rest       bool       `json:"rest,omitempty"`
	Hidden     bool       `json:"hidden,omitempty"`
	Sound      string     `json:"sound,omitempty"` // "" | "rhythm"（简谱 9）

	Ornaments   []Ornament  `json:"ornaments,omitempty"`
	GraceBefore []GraceNote `json:"graceBefore,omitempty"`
	GraceAfter  []GraceNote `json:"graceAfter,omitempty"`
	Annotation  string      `json:"annotation,omitempty"`

	// Lyric 是这个元素上唱的字。**上游的解析器不会填它**（番茄 DSL 的歌词是单独一行 `C:`，
	// 按音节顺序对位，不挂在元素上）—— 这一项是**我们这一层**为了「谱面编辑器」加的：
	// 编辑器里音高/时值/歌词是同一条元素上的三个属性，而 ACE 那边**一个音节就是一个音符**，
	// 所以元素必须自带歌词才能与 ACE 完美往返（否则歌词得走旁路，两端会不同步）。
	//
	// ⚠️ `Kind == KindSustain`（`-` 延音）的元素**不用**填它 —— 「延音」这件事由 Kind 表达。
	Lyric string `json:"lyric,omitempty"`

	// 仅 barline
	Type           string `json:"type,omitempty"`
	TemporaryMeter *Meter `json:"temporaryMeter,omitempty"`
}

// 小节线的 Type（8 种；9 种拼写里 `:|` 与 `:||` 都归 repeat-end）。
const (
	BarNormal      = "normal"
	BarRepeatStart = "repeat-start"
	BarRepeatEnd   = "repeat-end"
	BarRepeatBoth  = "repeat-both"
	BarEnd         = "end"
	BarDouble      = "double"
	BarHidden      = "hidden"
	BarInvisible   = "invisible"
)

// 元素的 Kind。
//
// ⚠️ 解析器只会产出 `note` / `sustain` / `barline`（上游语义：**休止是 `note` + `rest:true`**）。
// `KindRest` 是**编辑器方言**：面板里「休止」就是一格 `0`，发元素 JSON 时写成 `kind:"rest"` 更直白。
// Go 侧（`internal/tools` 的 `elementsToJpElems`）**两种都收** —— 收窄一种就会静默丢元素。
const (
	KindNote    = "note"
	KindSustain = "sustain"
	KindBarline = "barline"
	KindRest    = "rest" // 非解析器产出；编辑器方言，等价于 KindNote + Rest
)

// Mark 的下标是**元素下标**（不是音符下标）—— 上游测试 test 12 就是这么定的。
type Mark struct {
	Type    string `json:"type"` // "slur" | ...
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Caption string `json:"caption,omitempty"`
}

// Syllable 是一个歌词音节。
type Syllable struct {
	Text                string `json:"text"`
	TrailingPunctuation string `json:"trailingPunctuation,omitempty"`
	LeftBrace           bool   `json:"leftBrace,omitempty"`
	RightBrace          bool   `json:"rightBrace,omitempty"`
}

// LyricLine 是一条歌词行（一个 `C:` 行）。
type LyricLine struct {
	Syllables []Syllable `json:"syllables"`
}

// Voice 是一条谱行（一个 `Q…:` 行）。
//
// ⚠️ `Lyrics` 是**音节列表**，**不要**当"音节数 == 音符数"去校验 ——
// 真谱子里 `Q3: 1, - - - |` 配 `C3: 呜@呜`（4 个元素只给 3 个 token）是合法的。
type Voice struct {
	Voice    int         `json:"voice"`
	Caption  string      `json:"caption,omitempty"`
	Elements []Element   `json:"elements"`
	Marks    []Mark      `json:"marks,omitempty"`
	Lyrics   []LyricLine `json:"lyrics,omitempty"`
}

// Group 是一组同时演奏的谱行（多声部）。
type Group struct {
	Voices []Voice `json:"voices"`
}

// Page 是一页（`[fenye]` 分页）。
type Page struct {
	Groups []Group `json:"groups"`
}

// Document 是一份解析完的谱子。
type Document struct {
	Metadata    Metadata     `json:"metadata"`
	Pages       []Page       `json:"pages"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}
