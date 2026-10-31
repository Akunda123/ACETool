package tools

import (
	"fmt"
	"strconv"
	"strings"
)

// 属性匹配 —— 下拉里**只放 ACE 真能做**的属性：
// 原脚本 22 条里能实现的 6 条 + ACE 独有的 12 条（★）= **18 条**。
//
// 用户 2026-10-04 裁定（两条一起看才完整）：
//   ① 参数「只留 ace 存在的」；
//   ② 属性同理 —— **把 ace 没有的 key 去掉**（之前那版是把 16 条做不到的留在下拉里"明确拒绝"，
//      现在改成**根本不出现在下拉里**）。
//
// ⛔ 但**不做静默失败**这条纪律不变：`unsupportedAttr` 那 16 条的说明**继续留着** ——
// 直接拿 API 调 `attr=detune` 照样得到**带原因的明确拒绝**，不会退化成"0 命中"或"认不出"。
// 有测试钉住：拒绝表的键 == 原脚本 22 条 − 能实现的 6 条，且这 16 条**都不在下拉里**。

// svAttrOptions 是原脚本那 22 个音符属性的**全集**（顺序与 zh-cn 标签一字不改）。
// ⚠️ 它**不是下拉的内容** —— 只作对照表与测试基线用。
var svAttrOptions = []Option{
	{Value: "duration", Label: "持续时间（拍）"},
	{Value: "lyrics", Label: "歌词"},
	{Value: "phRaw", Label: "音素（实际发音）"},
	{Value: "phDefault", Label: "音素（默认发音）"},
	{Value: "pitch", Label: "音高"},
	{Value: "detune", Label: "音分"},
	{Value: "language", Label: "语言"},
	{Value: "rapTone", Label: "说唱音调"},
	{Value: "rapIntonation", Label: "说唱语调"},
	{Value: "rapAccent", Label: "说唱声调（中文普通话）"},
	{Value: "vibratoMod", Label: "颤音调制"},
	{Value: "ptOffset", Label: "音高过渡.偏移"},
	{Value: "ptDurLeft", Label: "音高过渡.时长左"},
	{Value: "ptDurRight", Label: "音高过渡.时长右"},
	{Value: "ptDepthLeft", Label: "音高过渡.深度左"},
	{Value: "ptDepthRight", Label: "音高过渡.深度右"},
	{Value: "vbrStart", Label: "颤音.开始"},
	{Value: "vbrLeft", Label: "颤音.左"},
	{Value: "vbrRight", Label: "颤音.右"},
	{Value: "vbrDepth", Label: "颤音.深度"},
	{Value: "vbrPhase", Label: "颤音.相位"},
	{Value: "vbrFreq", Label: "颤音.频率"},
}

// svAttrSupported 是原脚本 22 条里 **ACE 真能做**的 6 条（相对顺序照原脚本）。
// 下拉里"原脚本部分"就是它，排在 ★ 前面。
var svAttrSupported = []Option{
	{Value: "duration", Label: "持续时间（拍）"},
	{Value: "lyrics", Label: "歌词"},
	{Value: "phRaw", Label: "音素（实际发音）"},
	{Value: "phDefault", Label: "音素（默认发音）"},
	{Value: "pitch", Label: "音高"},
	{Value: "language", Label: "语言"},
}

// aceAttrOptions 是 **ACE 独有**的音符属性 —— 原脚本里没有这些。
//
// 用户 2026-10-04 裁定：**「换气算在音符里吧」** ⇒ 换气几项进**属性下拉**（不是另开一个工具），
// 界面上也**不标"我们加的"**（它们就是音符属性）。`Value` 用 `ace.` 前缀，
// 这样 API 回包里一眼能看出哪些是原脚本的、哪些是 ACE 侧补的。
//
// ⚠️ 用户 2026-10-14：「把参数前的星号去掉」⇒ 标签里**不再带 `★ `**（原来那 12 条带过），
// 与属性/参数的真实身份只靠 `ace.` 前缀（API 侧）区分，**界面上看起来就是一个平铺列表**。
var aceAttrOptions = []Option{
	{Value: "ace.phOverride", Label: "音素覆盖（有/无）"},
	{Value: "ace.headSec", Label: "头辅音长度（秒）"},
	{Value: "ace.tailSec", Label: "尾辅音长度（秒）"},
	{Value: "ace.durTick", Label: "时长（拍）"},
	{Value: "ace.endPos", Label: "结束位置（拍）"},
	{Value: "ace.clipType", Label: "片段类型（人声、乐器、通用、音频）"},
	{Value: "ace.clipEnabled", Label: "片段启用（有/无）"},
	{Value: "ace.hasBreath", Label: "有换气（有/无）"},
	{Value: "ace.breathRequested", Label: "换气请求长度（秒）"},
	{Value: "ace.breathEffective", Label: "换气实际生效（秒）"},
	{Value: "ace.breathSqueezed", Label: "换气被压掉（有/无）"},
	{Value: "ace.breathSpace", Label: "可插换气空间（秒）"},
}

// needsBreath 判断这个属性要不要额外读一次 `breath list`（**按需**，不查不花）。
func needsBreath(attr string) bool {
	return strings.HasPrefix(attr, "ace.hasBreath") || strings.HasPrefix(attr, "ace.breath")
}

// needPhonemes 判断这个属性要不要额外读一次 `phoneme list`。
func needPhonemes(attr string) bool { return attr == "phRaw" || attr == "phDefault" }

// unsupportedAttr 说明 ACE 侧为什么做不到（照着 SV 与 ACE 的实际差异写，不是套话）。
var unsupportedAttr = map[string]string{
	"detune": "ACE 没有「音分 / Detune」这个音符属性 —— 它是 SV 独有的逐音符音高微调；" +
		"ACE 的人声音高曲线整条都不在这套接口上（`vocalparam layers` 里 `pitch` 恒 `available:false`）",

	"rapTone":       "ACE 没有「说唱音调」—— rap 在 ACE 里是控制 lane（挂在歌唱引擎上），不是逐音符属性",
	"rapIntonation": "ACE 没有「说唱语调」—— 同上，ACE 侧读不到逐音符的说唱语调",
	"rapAccent":     "ACE 没有「说唱声调（中文普通话）」—— SV2 用 Note#setRapAccent 写它，ACE 无对应接口",

	"vibratoMod": "ACE 没有「颤音调制」（SV 的 dF0VbrMod）—— 颤音整体不在 ACE 这套接口上",

	"ptOffset":     "ACE 没有「音高过渡.偏移」—— 音高过渡那 12 个字段（PT offset / 左右时长 / 左右深度）是 SV1 的音符属性，ACE 完全没有",
	"ptDurLeft":    "ACE 没有「音高过渡.时长左」—— 同上，SV1 专有",
	"ptDurRight":   "ACE 没有「音高过渡.时长右」—— 同上，SV1 专有",
	"ptDepthLeft":  "ACE 没有「音高过渡.深度左」—— 同上，SV1 专有",
	"ptDepthRight": "ACE 没有「音高过渡.深度右」—— 同上，SV1 专有",

	"vbrStart": "ACE 没有「颤音.开始」—— 颤音的起止不由逐音符属性控制（ACE 侧这条曲线不在接口上）",
	"vbrLeft":  "ACE 没有「颤音.左」—— 同上（SV1 的 tF0VbrLeft）",
	"vbrRight": "ACE 没有「颤音.右」—— 同上（SV1 的 tF0VbrRight）",
	"vbrDepth": "ACE 没有「颤音.深度」—— 同上（SV1 的 dF0Vbr）",
	"vbrPhase": "ACE 没有「颤音.相位」—— 同上（SV1 的 pF0Vbr）",
	"vbrFreq":  "ACE 没有「颤音.频率」—— 同上（SV1 的 fF0Vbr）",
}

// matchNote 判断一个音符是否命中。返回 (是否命中, 命中值的显示文本, error)。
// error 只在**属性认不出**或**ACE 没有这个概念**时返回 —— 那是"问错了"，不是"没命中"。
func matchNote(attr, cond, want string, n noteRow, ref clipRef) (bool, string, error) {
	if why, bad := unsupportedAttr[attr]; bad {
		return false, "", fmt.Errorf("这个属性在 ACE 里没有对应概念：%s", why)
	}

	switch attr {
	case "duration": // 拍（原脚本口径：四分音符 = 1 拍）
		v := float64(n.Dur) / 480.0
		w, err := wantFloat(want, "持续时间（拍）", "例：1 / 1.5 / 2")
		if err != nil {
			return false, "", err
		}
		if err := numericCond(cond, "持续时间（拍）"); err != nil {
			return false, "", err
		}
		return cmp(v, cond, w), trimNum(v), nil

	case "lyrics":
		return textHit(n.Lyric, cond, want), n.Lyric, nil

	case "phRaw": // 「音素（实际发音）」= 当前生效的音素（phoneme list 的 phonemes；手动改过就是改过的）
		if !n.PhRead {
			return false, "", errNoPhonemes
		}
		return textHit(n.PhRaw, cond, want), n.PhRaw, nil

	case "phDefault": // 「音素（默认发音）」= 没编辑过时它会唱的音素（phoneme list 的 defaultPhonemes）
		if !n.PhRead {
			return false, "", errNoPhonemes
		}
		return textHit(n.PhDefault, cond, want), n.PhDefault, nil

	case "pitch":
		return matchPitch(n.Pitch, cond, want)

	case "language":
		// ⚠️ **两边都要过 langKeyword**：ACE 报的是完整英文名（"English"），用户填的是单字（"英"）
		// —— 只映一边就永远 0 命中（踩过）。认不出的写法原样返回，所以按字面比也仍然成立。
		return textHit(langKeyword(n.Language), cond, langKeyword(want)), n.Language, nil

	// ---------- ACE 独有（★） ----------

	case "ace.phOverride": // 这一条音符有没有被手动改过音素
		w, err := boolWant(want)
		if err != nil {
			return false, "", err
		}
		has := strings.TrimSpace(n.Syllable) != ""
		return has == w, yesNo(has), nil

	case "ace.headSec": // ⚠️ ACE 的单位是**秒**（SV 是"比例数组"，量纲不同、不能互相套）
		return numAttr("头辅音长度（秒）", sumFloats(n.HeadCons), cond, want)

	case "ace.tailSec":
		return numAttr("尾辅音长度（秒）", sumFloats(n.TailCons), cond, want)

	case "ace.durTick":
		// ⚠️ 单位口径（用户 2026-10-14）：**对外一律「拍」** ⇒ 值是 tick/480，标签不提 tick
		return numAttr("时长（拍）", ticksToBeats(n.Dur), cond, want)

	case "ace.endPos":
		return numAttr("结束位置（拍）", ticksToBeats(n.EndPos), cond, want)

	case "ace.clipType":
		// 值仍给 ACE 的原始串（`sing` / `instrument` / `genericmidi`），但**搜索吃中文与易混词**
		//（用户 2026-10-14：「把混淆词也加入搜索比如声乐、器乐等」）⇒ 见 clipTypeHit。
		return clipTypeHit(ref.ClipType, cond, want), ref.ClipType, nil

	case "ace.clipEnabled":
		w, err := boolWant(want)
		if err != nil {
			return false, "", err
		}
		return ref.Enabled == w, yesNo(ref.Enabled), nil

	case "ace.hasBreath":
		if !n.BreathRead {
			return false, "", errNoBreath
		}
		w, err := boolWant(want)
		if err != nil {
			return false, "", err
		}
		return n.Breath.HasBreath == w, yesNo(n.Breath.HasBreath), nil

	case "ace.breathRequested":
		if !n.BreathRead {
			return false, "", errNoBreath
		}
		return numAttr("换气请求长度（秒）", n.Breath.RequestedSec, cond, want)

	case "ace.breathEffective":
		if !n.BreathRead {
			return false, "", errNoBreath
		}
		return numAttr("换气实际生效（秒）", n.Breath.EffectiveSec, cond, want)

	case "ace.breathSqueezed": // 要了却没唱够 —— SV 完全没有这个概念
		if !n.BreathRead {
			return false, "", errNoBreath
		}
		w, err := boolWant(want)
		if err != nil {
			return false, "", err
		}
		sq := n.Breath.RequestedSec > n.Breath.EffectiveSec+1e-9
		return sq == w, yesNo(sq), nil

	case "ace.breathSpace":
		if !n.BreathRead {
			return false, "", errNoBreath
		}
		return numAttr("可插换气空间（秒）", n.Breath.InsertableSpaceSec, cond, want)
	}
	return false, "", fmt.Errorf("属性认不出：%q", attr)
}

var (
	errNoPhonemes = fmt.Errorf("没读到这个音符的音素 —— 「音素（实际发音）/（默认发音）」需要整片段读一次 `phoneme list`；" +
		"该片段没能读到（多半是片段 UUID 没取到），请把「查找范围」改成「当前片段」再试")
	errNoBreath = fmt.Errorf("没读到这个音符的换气数据 —— 「换气…」需要整片段读一次 `breath list`；" +
		"该片段没能读到（片段 UUID 没取到，或它不是 Sing 片段 —— `breath list` 只认 Sing 片段）")
)

// numAttr 收掉"数值属性"那几支重复到发腻的样板：取值 → 校验 → 比较 → 回显。
func numAttr(what string, v float64, cond, want string) (bool, string, error) {
	w, err := wantFloat(want, what, "例：0 / 0.5 / 1.2")
	if err != nil {
		return false, "", err
	}
	if err := numericCond(cond, what); err != nil {
		return false, "", err
	}
	return cmp(v, cond, w), trimNum(v), nil
}

func sumFloats(xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s
}

// yesNo 把布尔属性的**命中值**显示成中文（面板上「数值:」那一行要给人看）。
func yesNo(b bool) string {
	if b {
		return "有"
	}
	return "无"
}

// matchPitch 处理音高：数字 / 音名（C4）/ 音级（C）/ 黑白键。
// 这套写法是从原脚本（以及更早的 SV 移植）里搬过来的，别改口径。
func matchPitch(pitch int, cond, want string) (bool, string, error) {
	w := strings.TrimSpace(want)
	if w == "" {
		return false, "", fmt.Errorf("音高要填点东西：MIDI 数字（60）· 音名（C4 / c#4）· 音级（C）· 黑 / 白")
	}
	// 黑/白键：只认 cond=eq（原脚本里它就是"选黑键"这种用法）
	if b, isBool := blackWhite(w); isBool {
		if b {
			return isBlackKey(pitch), noteName(pitch), nil
		}
		return !isBlackKey(pitch), noteName(pitch), nil
	}
	// 音名带八度 → 绝对音高
	if p, ok := parsePitchName(w); ok {
		return cmp(float64(pitch), cond, float64(p)), noteName(pitch), nil
	}
	// 光一个音级（C / C# / Db）→ 同类音
	if pc, ok := parsePitchClass(w); ok {
		return cmp(float64(pitch%12), cond, float64(pc)), noteName(pitch), nil
	}
	// 数字
	v, err := strconv.ParseFloat(w, 64)
	if err != nil {
		return false, "", fmt.Errorf("音高认不出：%q —— 可写 MIDI 数字（60）· 音名（C4 / c#4 / Db4）· 音级（C / C#）· 黑 / 白", want)
	}
	if cond == "contains" {
		return false, "", numericCond(cond, "音高")
	}
	return cmp(float64(pitch), cond, v), noteName(pitch), nil
}

// numericCond 拦住"数值 + 包含"这个**静默陷阱**：cmp() 对 contains 恒返回 false，
// 于是用户会看到「0 命中」而不是「你问错了」—— 那正是本工具最该避免的失败方式。
func numericCond(cond, what string) error {
	if cond == "contains" {
		return fmt.Errorf("%s是数值，不能用「包含」（它只对文本有意义）；请用 大于 / 小于 / 等于", what)
	}
	return nil
}

// textHit 处理文本类：等于 = 全等，包含 = 子串，大小写不敏感（原脚本就是不分大小写）。
func textHit(actual, cond, want string) bool {
	if cond == "contains" {
		return strings.Contains(strings.ToLower(actual), strings.ToLower(want))
	}
	return strings.EqualFold(strings.TrimSpace(actual), strings.TrimSpace(want))
}

// clipTypeSynonyms：ACE 的**片段类型**原始串 ↔ 中文/易混词。
//
// 用户 2026-10-14：「后面写（人声、乐器、通用、音频），把混淆词也加入搜索比如声乐、器乐等」
// ⇒ 标签上标中文（见 `aceAttrOptions` 的 Label），**搜索也吃这些词**：
//
//	· 「等于」：原始串全等（`sing`）**或** 输入落在该类的中文词里（`人声` / `声乐` / `唱` …）
//	· 「包含」：原始串子串（`midi` 命中 `genericmidi`）**或** 输入是某类词的子串（`声` 命中 人声类、`乐` 命中 乐器类）
//
// ⚠️ 结果里给的**仍是原始串**（`sing` / `genericmidi`），中文只在标签上出现 —— 用户 2026-10-14 选的 A 方案。
// ⚠️ `audio` 片段**没有音符**：范围=轨道/工程时不会被扫到（`trackHasNotes` 只放 sing/instrument/genericmidi）
// ⇒ 它进词表只为"标签上写全"，查不出结果是正常的。
var clipTypeSynonyms = []struct {
	Type  string   // ACE 的原始串（小写）
	Words []string // 也算命中的词（小写、可被「包含」切）
}{
	{Type: "sing", Words: []string{"人声", "声乐", "歌唱", "唱", "vocal", "sing"}},
	{Type: "instrument", Words: []string{"乐器", "器乐", "乐器轨", "instrument"}},
	{Type: "genericmidi", Words: []string{"通用", "通用midi", "midi", "genericmidi"}},
	{Type: "audio", Words: []string{"音频", "波形", "audio"}},
}

func clipTypeHit(actual, cond, want string) bool {
	a := strings.ToLower(strings.TrimSpace(actual))
	w := strings.ToLower(strings.TrimSpace(want))
	if w == "" {
		return false
	}
	if textHit(a, cond, w) {
		return true
	}
	for _, g := range clipTypeSynonyms {
		if g.Type != a {
			continue // 当前这一条不是这一类 ⇒ 中文词再多也不命中
		}
		for _, word := range g.Words {
			if cond == "contains" {
				if strings.Contains(word, w) || strings.Contains(w, word) {
					return true
				}
				continue
			}
			if word == w {
				return true
			}
		}
	}
	return false
}

// cmp 数值比较。等于用全等（原脚本就是全等）—— 参数曲线是 double 直存，JSON 往返不掉精度。
func cmp(v float64, cond string, want float64) bool {
	switch cond {
	case "gt":
		return v > want
	case "lt":
		return v < want
	case "eq":
		return v == want
	case "contains":
		return false // 数值没有"包含"，调用方会先拦
	}
	return false
}

func condLabel(c string) string {
	switch c {
	case "gt":
		return "大于"
	case "lt":
		return "小于"
	case "eq":
		return "等于"
	case "contains":
		return "包含"
	}
	return c
}

// paramLabel 把 ACE 的参数 id 换回原脚本的中文名（结果文本里用中文，方便对账）。
func paramLabel(id string) string {
	switch id {
	case "pitchDelta":
		return "音高偏差"
	case "vibratoEnv":
		return "颤音包络"
	case "loudness":
		return "响度"
	case "tension":
		return "张力"
	case "breathiness":
		return "气声"
	case "voicing":
		return "发声"
	case "gender":
		return "性别"
	case "toneShift":
		return "音区偏移"
	case "mouthOpening":
		return "口型"
	case "energy":
		return "能量"
	case "falsetto":
		return "假声"
	case "formant":
		return "音色"
	case "dynamic":
		return "整体动态"
	}
	return id
}

func wantFloat(want, what, hint string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(want), 64)
	if err != nil {
		return 0, fmt.Errorf("%s要一个数字（%s），实得 %q", what, hint, want)
	}
	return v, nil
}

func trimNum(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// ---------- 音高写法 ----------

var pitchClasses = map[string]int{
	"c": 0, "c#": 1, "db": 1, "d": 2, "d#": 3, "eb": 3, "e": 4, "fb": 4,
	"f": 5, "f#": 6, "gb": 6, "g": 7, "g#": 8, "ab": 8, "a": 9, "a#": 10, "bb": 10, "b": 11, "cb": 11,
}

var noteNames = []string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}

func noteName(midi int) string {
	if midi < 0 {
		return strconv.Itoa(midi)
	}
	return fmt.Sprintf("%s%d", noteNames[midi%12], midi/12-1)
}

// blackWhite 认 黑/白 两种写法（含英文）。
func blackWhite(w string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(w)) {
	case "黑", "黑键", "black":
		return true, true
	case "白", "白键", "white":
		return false, true
	}
	return false, false
}

// parsePitchName 解析带八度的音名，如 c4 / C#4 / Db4 / c-1。
func parsePitchName(w string) (int, bool) {
	s := strings.ToLower(strings.TrimSpace(w))
	if s == "" {
		return 0, false
	}
	// 拆字母（含 #/b）+ 数字（可带负号）
	i := 0
	for i < len(s) && (s[i] >= 'a' && s[i] <= 'g' || s[i] == '#' || s[i] == 'b') {
		// 小心：'b' 既是音名 B 也是降号 —— 只在后面还跟着字母时才当降号
		if s[i] == 'b' && i+1 < len(s) && s[i+1] >= 'a' && s[i+1] <= 'g' {
			i++
			continue
		}
		i++
	}
	letter, rest := s[:i], s[i:]
	if letter == "" || rest == "" {
		return 0, false
	}
	pc, ok := pitchClasses[letter]
	if !ok {
		return 0, false
	}
	oct, err := strconv.Atoi(rest)
	if err != nil {
		return 0, false
	}
	midi := (oct+1)*12 + pc
	if midi < 0 || midi > 127 {
		return 0, false
	}
	return midi, true
}

// parsePitchClass 解析光一个音级（不带八度），如 c / bb / f#。
func parsePitchClass(w string) (int, bool) {
	s := strings.ToLower(strings.TrimSpace(w))
	pc, ok := pitchClasses[s]
	return pc, ok
}

func isBlackKey(midi int) bool {
	switch ((midi % 12) + 12) % 12 {
	case 1, 3, 6, 8, 10:
		return true
	}
	return false
}

// pitchSpec 判断一个写法是不是音高（给上层做提示用），返回 (是否绝对音高, 说明, 是否是音高写法)。
func pitchSpec(w string) (bool, string, bool) {
	s := strings.TrimSpace(w)
	if s == "" {
		return false, "", false
	}
	if _, isBW := blackWhite(s); isBW {
		return false, "黑白键筛选", true
	}
	if p, ok := parsePitchName(s); ok {
		return true, fmt.Sprintf("音名 → MIDI %d（%s）", p, noteName(p)), true
	}
	if _, ok := parsePitchClass(s); ok {
		return false, "只给音级 → 匹配同名音（所有八度）", true
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil && v >= 0 && v <= 127 {
		return true, "MIDI 数字", true
	}
	return false, "", false
}

// ---------- 别的宽容解析 ----------

// boolWant 收 有/无 这类值（也收 y/n、true/false、是/否）。认不出**报错**，不当成 false。
func boolWant(w string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(w)) {
	case "有", "是", "yes", "y", "true", "1", "开":
		return true, nil
	case "无", "否", "no", "n", "false", "0", "", "关":
		return false, nil
	}
	return false, fmt.Errorf("要「有 / 无」（也收 是/否、y/n、true/false、1/0），实得 %q", w)
}

// langKeyword 把中文单字缩写换成 ACE 报的语种名（ACE 用完整英文名，如 Chinese / English / Japanese）。
func langKeyword(w string) string {
	switch strings.ToLower(strings.TrimSpace(w)) {
	case "中", "普", "汉", "国语", "中文", "mandarin", "chinese", "zh":
		return "Chinese"
	case "英", "英文", "english", "en":
		return "English"
	case "日", "日文", "japanese", "jp", "ja":
		return "Japanese"
	case "粤", "广", "粤语", "cantonese", "yue":
		return "Cantonese"
	case "西", "西班牙语", "spanish", "es":
		return "Spanish"
	case "韩", "朝", "韩语", "korean", "ko":
		return "Korean"
	}
	return w // 认不出就原样返回，让上层按字面比
}
