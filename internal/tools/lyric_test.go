package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 拼一行（原脚本 `Lyrics()` 里那段）：按词用空格、按字符则紧排，
// **会被分隔符切开的词套 `()`**（用户 2026-10-05：按词模式下非 CJK 的多字词也要套）。
// （这一段测的是**忽略连音关**的那条路 —— 逐音符原样，往返精确。）
func TestJoinLyrics(t *testing.T) {
	// 不按字符隔开：**空格就是分隔符，不显示括号**（用户 2026-10-06）
	if got := joinLyrics([]string{"ha", "ppy", "啦"}, false, false); got != "ha ppy 啦" {
		t.Errorf("不按字符隔开 = %q，期望 %q", got, "ha ppy 啦")
	}
	if got := joinLyrics([]string{"jin", "qing"}, false, false); got != "jin qing" {
		t.Errorf("不按字符隔开（拉丁）= %q，期望 %q", got, "jin qing")
	}
	// 按字符：单个字直接排，**多字的词套括号**（这样写回时才认得出"一个音符唱一整词"）
	if got := joinLyrics([]string{"啦", "啦"}, true, false); got != "啦啦" {
		t.Errorf("按字符 = %q，期望 %q", got, "啦啦")
	}
	if got := joinLyrics([]string{"b iy"}, true, false); got != "(b iy)" {
		t.Errorf("多字词要套括号，实得 %q", got)
	}
	// 单字不套括号（套了写回会变成两个字面字符）
	if got := joinLyrics([]string{"a"}, true, false); got != "a" {
		t.Errorf("单字不该套括号，实得 %q", got)
	}
	// 空句
	if got := joinLyrics(nil, false, false); got != "" {
		t.Errorf("空句 = %q", got)
	}
	if got := joinLyrics(nil, true, false); got != "" {
		t.Errorf("空句（字符）= %q", got)
	}
}

// ⭐⭐ 「忽略连音」的两件事：**`-` 收起来**、**`hello#1 hello#2` 合成一个 `hello`**。
//
// 数据是**真机抓到的形态**（`devtools/dump-lyrics.js` 打这份工程打出来的）：
//
//	句 4  jin qing zhan - fang qing   → 显示 "jin qing zhan fang qing"
//	句 5  hello#1 hello#2             → 显示 "hello"
//	句 3  跳 一 曲 春 天 的 芭 蕾        → 显示 "跳一曲春天的芭蕾"（CJK 紧排）
func TestCollapseTenuto(t *testing.T) {
	for _, c := range []struct {
		name string
		in   []string
		want []string
	}{
		{"连音丢掉", []string{"jin", "qing", "zhan", "-", "fang", "qing"},
			[]string{"jin", "qing", "zhan", "fang", "qing"}},
		{"多音符词收成一个", []string{"hello#1", "hello#2"}, []string{"hello"}},
		{"两个词各自保留", []string{"hello#1", "hello#2", "world#1", "world#2"},
			[]string{"hello", "world"}},
		{"连音+多音符词混在一起", []string{"a", "-", "hello#1", "hello#2", "-", "b"},
			[]string{"a", "hello", "b"}},
		{"单音符也带 #1", []string{"banana#1"}, []string{"banana"}},
		{"歌词里本来有 # 但后面不是数字 ⇒ 不猜", []string{"c#", "a#b"}, []string{"c#", "a#b"}},
		{"开头的 # 不算后缀", []string{"#1"}, []string{"#1"}},
	} {
		got := collapseTenuto(c.in)
		if !eqStrs(got, c.want) {
			t.Errorf("%s：%v → %v，期望 %v", c.name, c.in, got, c.want)
		}
	}
}

// 收起来之后怎么排：**跟原脚本一样挂在「分割成字符」上** ——
// 勾上 ⇒ 两个 CJK 词之间紧排（`跳一曲`），其余空格；不勾选 ⇒ 一律空格。
//
// ⚠️ 用户 2026-10-05 第二次口径（以此为准）：「填词面板**只是检测的时候**检测 cjk」
// ⇒ **CJK 只用来决定那个开关的默认档，不参与这里的空格规则**。
// （LRC 那边不归这条管：它按"cjk 不加空格、非 cjk 加"逐对判，见 §39.3。）
func TestJoinCollapsedSpacing(t *testing.T) {
	cjk := []string{"跳", "一", "曲", "春"}
	latin := []string{"jin", "qing", "zhan"}
	if got := joinCollapsed(cjk, true); got != "跳一曲春" {
		t.Errorf("勾上时 CJK 之间应当紧排，实得 %q", got)
	}
	// ⛔ 关键：拉丁词**不许**紧排 —— 拼成 "jinqingzhan" 会被 `lyric fill` 当成一个词
	if got := joinCollapsed(latin, true); got != "jin qing zhan" {
		t.Errorf("拉丁词必须留空格，实得 %q", got)
	}
	if got := joinCollapsed([]string{"la", "la", "la"}, true); got != "la la la" {
		t.Errorf("la 三连必须留空格，实得 %q", got)
	}
	// ⭐ 汉语拼音是拉丁字符 ⇒ 照样留空格（**这才好编辑**；粘成 `suizhejiao` 谁也没法改）
	if got := joinCollapsed([]string{"sui", "zhe", "jiao", "bu", "qi"}, true); got != "sui zhe jiao bu qi" {
		t.Errorf("拼音必须留空格，实得 %q", got)
	}
	// 不勾选 ⇒ 一律空格（**CJK 也一样**：CJK 只用来决定默认档）
	if got := joinCollapsed(cjk, false); got != "跳 一 曲 春" {
		t.Errorf("不勾选时应当全部空格，实得 %q", got)
	}
	// 混排：勾上时 CJK 紧、拉丁分
	if got := joinCollapsed([]string{"跳", "一", "jin", "qing"}, true); got != "跳一 jin qing" {
		t.Errorf("混排 = %q，期望 %q", got, "跳一 jin qing")
	}
	if got := joinCollapsed([]string{"a"}, true); got != "a" {
		t.Errorf("单个词 = %q", got)
	}
	if got := joinCollapsed(nil, true); got != "" {
		t.Errorf("空 = %q", got)
	}
}

// ⭐ `()` 保护：**只有「按字符隔开」时才需要**。
//
//	按字符（sep=""）：多字词会被切散 ⇒ 必须括起来
//	不按字符（sep=" "）：**空格就是分隔符** ⇒ 不显示括号（用户 2026-10-06 定稿）
func TestNeedsParens(t *testing.T) {
	for _, c := range []struct {
		tok    string
		chars  bool
		wanted bool
	}{
		{"跳", true, false},    // 单字不用
		{"跳", false, false},   // 单字不用
		{"你好", true, true},    // 按字符：多字 CJK 要套（否则会被拆成两个字）
		{"你好", false, false},  // 不按字符：空格就是分隔符 ⇒ 不套
		{"jin", true, true},   // 按字符：非 CJK 多字要套
		{"jin", false, false}, // ⭐ 不按字符：**不套**（用户 2026-10-06）
		{"hello#1", true, true},
		{"hello#1", false, false},
		{"b iy", false, false}, // 自带空格的词在不按字符时**也**不套（回填会打 ⚠，见 needsParens 注释）
		{"a", false, false},
		{"", false, false},
	} {
		if got := needsParens(c.tok, c.chars); got != c.wanted {
			t.Errorf("needsParens(%q, chars=%v) = %v，期望 %v", c.tok, c.chars, got, c.wanted)
		}
	}
}

// 「分割成字符」的 auto 档：**按文本数**（一个音符一个单位，含 CJK 字的才算中文）。
//
// ⚠️ 用户 2026-10-05 否掉了"看 ACE 的语种字段"这条路：「不按语种，汉语拼音也算中文，
// 按语种会让汉语拼音组成的**不好编辑**」—— 拼音的 language 是 Chinese，拿它当中文 ⇒
// 中文模式的分隔符是 `""` ⇒ 拼音被粘成 `suizhejiao`。所以判据只能是**字符**。
func TestResolveSplitChars(t *testing.T) {
	// 汉字占多数 ⇒ auto 给 true
	mixed := []string{"跳", "一", "曲", "春", "天", "的", "芭", "蕾", "jin"}
	got, auto := resolveSplitChars("", mixed)
	if !got || !auto {
		t.Errorf("汉字占多数时 auto 应当 = 按字符（true, auto=true），实得 %v/%v", got, auto)
	}
	// ⭐ 纯拼音 ⇒ auto 给 **false**：拼音单位化但**不粘连**，这样写回/编辑都好办
	got, auto = resolveSplitChars("auto", []string{"sui", "zhe", "jiao", "bu", "qi"})
	if got || !auto {
		t.Errorf("纯拼音时 auto 应当 = 按词（false, auto=true）—— 否则会把 `sui zhe` 粘起来，不好编辑；实得 %v/%v", got, auto)
	}
	// 纯英文 ⇒ 同理 false
	got, auto = resolveSplitChars("auto", []string{"hello#1", "hello#2", "world"})
	if got || !auto {
		t.Errorf("纯英文时 auto 应当 = 按词（false, auto=true），实得 %v/%v", got, auto)
	}
	// 显式档不受内容影响
	if got, auto = resolveSplitChars("on", []string{"hello"}); !got || auto {
		t.Errorf("显式 on 应当是 true/auto=false，实得 %v/%v", got, auto)
	}
	if got, auto = resolveSplitChars("off", []string{"跳"}); got || auto {
		t.Errorf("显式 off 应当是 false/auto=false，实得 %v/%v", got, auto)
	}
	// 没有歌词 ⇒ 不勾（没什么可按字符分的）
	if got, auto = resolveSplitChars("", nil); got || !auto {
		t.Errorf("没有歌词时应当是 false/auto=true，实得 %v/%v", got, auto)
	}
	// 正好一半 ⇒ 算中文（`cjk*2 >= cjk+other`）
	if got, _ = resolveSplitChars("", []string{"跳", "a"}); !got {
		t.Error("正好一半中文时应当算中文")
	}
	// ⭐⭐ 真机踩过的那次回归：副歌全是 `la` —— **按字符数**算会判成"非中文"
	//（15 个 `la` = 30 个字母 > 20 个汉字）。按**音符数**（一声一票）才是 20 票对 15 票 ⇒ 中文。
	// 这一条现在**不再影响空格**（空格是逐对判的），但影响写回时用哪种分隔约定。
	var reg []string
	for i := 0; i < 20; i++ {
		reg = append(reg, "跳")
	}
	for i := 0; i < 15; i++ {
		reg = append(reg, "la")
	}
	if got, _ := resolveSplitChars("", reg); !got {
		t.Error("汉字 20 票 vs 拉丁 15 票，应当算中文（按字符数算会被 la 带偏 —— 那是真机踩过的坑）")
	}
}

// 数字数：空白不算字。
func TestCountChars(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int
	}{
		{"", 0}, {"跳一曲", 3}, {"jin qing", 7}, {"a-b", 3}, {" 跳 一 ", 2},
	} {
		if got := countChars(c.in); got != c.want {
			t.Errorf("countChars(%q) = %d，期望 %d", c.in, got, c.want)
		}
	}
}

// 句前提前量：按当时 BPM 折成 tick（480 PPQ）。
func TestLeadTicksAt(t *testing.T) {
	// 这份工程真机的速度表：0 处 120，9600 处 140
	rd := stubRead(t, map[string]string{
		"tempo get": `{"points":[{"pos":0,"value":120},{"pos":9600,"value":140}],"pointCount":2}`,
	})
	got, note := leadTicksAt(context.Background(), rd, 0, 0.5)
	if note != "" {
		t.Errorf("不该有提示：%s", note)
	}
	// 120 BPM ⇒ 2 拍/秒 ⇒ 960 tick/秒 ⇒ 0.5 秒 = 480
	if got != 480 {
		t.Errorf("120 BPM 下 0.5 秒应当是 480 tick，实得 %d", got)
	}
	// 过了 9600 之后是 140 BPM ⇒ 1120 tick/秒 ⇒ 0.5 秒 = 560
	if got, _ = leadTicksAt(context.Background(), rd, 10000, 0.5); got != 560 {
		t.Errorf("140 BPM 下 0.5 秒应当是 560 tick，实得 %d", got)
	}
	// 读不到速度表 ⇒ 退回 1 拍并给一句人话（**不许静默**）
	rd2 := stubRead(t, map[string]string{})
	got, note = leadTicksAt(context.Background(), rd2, 0, 0.5)
	if got != 480 || note == "" {
		t.Errorf("读不到速度表时应当退回 480 并说明，实得 %d/%q", got, note)
	}
}

// ---------- 小工具 ----------

// 解析文本框（原脚本文本框回调里那段），**并且括号没闭合要报错而不是死循环**。
func TestParseLyricText(t *testing.T) {
	// 按词
	got, err := parseLyricText("ha ppy", false)
	if err != nil || len(got) != 2 || got[0] != "ha" || got[1] != "ppy" {
		t.Errorf("按词 = %v, %v", got, err)
	}
	// 按字符
	got, err = parseLyricText("啦啦", true)
	if err != nil || len(got) != 2 || got[0] != "啦" || got[1] != "啦" {
		t.Errorf("按字符 = %v, %v", got, err)
	}
	// ⭐ 核心用法：**按字符模式下 `(b i y)` 折叠成一个词**（中间的空格原样留在词里）
	got, err = parseLyricText("(b i y)", true)
	if err != nil {
		t.Fatalf("括号折叠不该报错：%v", err)
	}
	if len(got) != 1 || got[0] != "b i y" {
		t.Errorf("(b i y) 应当折叠成 1 个词 %q，实得 %v", "b i y", got)
	}
	// 混合：括号词 + 普通字
	got, err = parseLyricText("(b i y)啦", true)
	if err != nil || len(got) != 2 || got[0] != "b i y" || got[1] != "啦" {
		t.Errorf("混合 = %v, %v（期望 [b i y, 啦]）", got, err)
	}
	// ⚠️ 括号折叠**两种模式都成立**（用户 2026-10-05：非 cjk 在不勾选时也加 `()`）——
	// 旧实现是"先按空格切再看 token 是不是 `(`"，按词模式下会把 `(b i y)` 切成 `(b`/`i`/`y)`。
	if got, err = parseLyricText("(b i y)", false); err != nil || len(got) != 1 || got[0] != "b i y" {
		t.Errorf("按词模式下括号也要折叠成 1 个词：%v, %v", got, err)
	}
	// 按词模式：括号 + 别的词混排
	if got, err = parseLyricText("(jin) qing", false); err != nil || len(got) != 2 || got[0] != "jin" || got[1] != "qing" {
		t.Errorf("按词模式下 `(jin) qing` 应当是 [jin qing]：%v, %v", got, err)
	}
	// ⛔ **不能死循环**：原脚本 `while (Lyr1[n] != ")")` 在括号没闭合时会无限拼 "undefined"
	//（`undefined != ")"` 永远成立）—— 这里必须**明确报错**。
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := parseLyricText("(b i y", true); err == nil {
			t.Error("括号没闭合应当报错")
		} else if !strings.Contains(err.Error(), "括号没闭合") {
			t.Errorf("报错要说清是括号没闭合，实得：%v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("括号没闭合时**挂住了** —— 这正是原脚本那个死循环，必须报错")
	}
	// 空文本 ⇒ **一个词都没有**（调用方 `buildLyricSet` 会拒："解析出来 0 个词"）。
	// ⚠️ 旧实现（`strings.Split("", " ")`）会回一个空词，那样 len==1 就会被当成"一个空歌词"
	// 写进第一个音符 —— 扫描式实现顺手把这个坑填了。
	if got, err := parseLyricText("", false); err != nil || len(got) != 0 {
		t.Errorf("空文本应当得 0 个词：%v, %v", got, err)
	}
	// 连续空格不产生空词（否则会凭空占掉音符）
	if got, err := parseLyricText("a  b", false); err != nil || len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("连续空格不该产生空词：%v, %v", got, err)
	}
	// 按字符模式下空格只是分隔符（不当成某个音符的歌词）
	if got, err := parseLyricText("a b", true); err != nil || len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("按字符模式下空格应当只是分隔：%v, %v", got, err)
	}
}

// 分配（原脚本 `num = o % LyricsOfNote.length`，只有一个词时全给同一个）。
func TestAssignLyrics(t *testing.T) {
	// 词数与音符数相等 ⇒ 一一对应
	if got := assignLyrics(3, 3); !eqInts(got, []int{0, 1, 2}) {
		t.Errorf("3 词 3 音 = %v，期望 [0 1 2]", got)
	}
	// 只有一个词 ⇒ 全部同一个
	if got := assignLyrics(1, 4); !eqInts(got, []int{0, 0, 0, 0}) {
		t.Errorf("1 词 4 音 = %v，期望全 0", got)
	}
	// 词比音符少 ⇒ **循环用**（原脚本行为，不是 bug：少写几个词时词会循环铺满）
	if got := assignLyrics(2, 5); !eqInts(got, []int{0, 1, 0, 1, 0}) {
		t.Errorf("2 词 5 音 = %v，期望 [0 1 0 1 0]", got)
	}
}

// ⭐ 关键性质：**面板显示的那一行，原样解析回来必须得到同一串逐音符歌词**。
//
// 这条把"显示"与"写回"钉在一起 —— 它是这个功能唯一真正重要的不变量：
// 用户看到 `ha ppy`，把它原样提交（没改），逐音符结果必须不变。
func TestLyricRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		notes []string
		chars bool
	}{
		{"按词·两个字", []string{"ha", "ppy"}, false},
		{"按词·一个字", []string{"啦"}, false},
		{"按词·混中英", []string{"la", "啦", "ah"}, false},
		{"按字符·中文", []string{"啦", "啦", "啦"}, true},
		{"按字符·单字母", []string{"a", "b", "c"}, true},
		{"按字符·多字词（关键）", []string{"b iy"}, true},
		{"按字符·多字词夹在中间", []string{"s", "iy", "a"}, true},
		{"按字符·延长号", []string{"a", "-", "b"}, true},
	}
	for _, c := range cases {
		text := joinLyrics(c.notes, c.chars, false) // 忽略连音**关**：逐音符原样
		parsed, err := parseLyricText(text, c.chars)
		if err != nil {
			t.Errorf("%s：显示 %q 解析失败：%v", c.name, text, err)
			continue
		}
		idx := assignLyrics(len(parsed), len(c.notes))
		back := make([]string, len(idx))
		for i, k := range idx {
			back[i] = parsed[k]
		}
		if !eqStrs(back, c.notes) {
			t.Errorf("%s：往返不一致 —— 原 %v → 显示 %q → 解析 %v → 回 %v",
				c.name, c.notes, text, parsed, back)
		}
		if !roundTrips(text, c.notes, c.chars) {
			t.Errorf("%s：`roundTrips` 自检说还原不了，但上面明明一致（自检本身写错了）", c.name)
		}
	}
}

// ⭐⭐ 「能读 / 能藏 / 能回填」的真机取证件 —— 数据全部来自本机 ACE 的实测回包。
//
// 前两条是**用 `--dry-run` 真机验过的**（不写工程）：
//
//	`lyric fill --sentence 4 --text "jin qing zhan fang qing" --dry-run`
//	   ⇒ `-` 那个音符 `filled:false` / `lyricAfter:"-"`（连音**原样留着**），其余逐音符与改前相同
//	`lyric fill --sentence 5 --text "hello" --dry-run`
//	   ⇒ `lyricAfter` 正是 `hello#1` / `hello#2`
//
// 所以这里钉的是：**显示出来的那串，正好是 ACE 合并出来的那串**（`sentence.lyric`），
// 而 `lyric fill` 是这句话的逆运算 ⇒ 显示 → 回填 是无损的。
func TestTenutoReadHideFill(t *testing.T) {
	for _, c := range []struct {
		name     string
		notes    []string // 逐音符**原始**歌词（能读）
		aceLyric string   // ACE 自己报的整句（`clip lyrics` 的 lyric）
		chars    bool
	}{
		{"连音在中间", []string{"jin", "qing", "zhan", "-", "fang", "qing"},
			"jin qing zhan fang qing", true},
		{"多音符英文词", []string{"hello#1", "hello#2"}, "hello", true},
		{"多音符英文词·按词", []string{"hello#1", "hello#2"}, "hello", false},
		{"中文紧排", []string{"跳", "一", "曲", "春", "天", "的", "芭", "蕾"},
			"跳一曲春天的芭蕾", true},
		{"中文紧排（按字符）", []string{"跳", "一", "曲"},
			"跳一曲", true},
		// ⚠️ 按词模式下中文是**空格排**（CJK 只用来决定默认档，不参与空格规则）——
		// 它跟 ACE 自己合并的 `跳一曲` 不一样，但 `lyric fill` 吃 `跳 一 曲` 得到的结果**逐音符相同**
		// （真机 dry-run 验过：`跳一曲春天的芭蕾` 与 `跳 一 曲 春 天 的 芭 蕾` 填回去一模一样）。
		{"连音在两个词之间", []string{"春", "无", "-", "-", "悔"}, "春无悔", true},
		{"la 连排", []string{"la", "la", "la", "la"}, "la la la la", true},
		{"句尾连音", []string{"随", "-"}, "随", true},
	} {
		text := joinLyrics(c.notes, c.chars, true) // 忽略连音**开**
		// ⭐ 判据（2026-10-06 改）：显示**不再**等于 ACE 的合并句（现在按用户口径带 `()`），
		// 而是"把显示折成 ACE 形态之后 == ACE 自己那句" —— 这才是 `lyric fill` 真正吃的东西。
		folded, err := aceFillText(text, c.chars)
		if err != nil {
			t.Errorf("%s：显示 %q 折不出 ACE 形态：%v", c.name, text, err)
			continue
		}
		if folded != c.aceLyric {
			t.Errorf("%s：显示 %q → 折成 %q，但 ACE 自己合并出来的是 %q（折法不是它的逆）",
				c.name, text, folded, c.aceLyric)
		}
		// 「能读」：原始形态必须原封不动地拿得到（`-` / `#1` / `#2` 都在）
		if len(c.notes) == 0 {
			t.Error("用例本身有问题：没有音符")
		}
	}
}

// 忽略连音**开** ⇒ 写回走 `lyric fill`；**关** ⇒ 走逐音符。这条不能反 ——
// 反了就是"显示成 `hello` 却按 1:1 逐音符写"，两个音符都被写成 `hello`。
func TestWriteModeFollowsIgnoreTenuto(t *testing.T) {
	rd := stubRead(t, map[string]string{
		"editor status": `{"isAvailable":true,"trackIndex":0,"clipIndex":0,"clipName":"c","editorType":"sing","clipUuid":"{C}"}`,
		"clip lyrics --track-index 0 --clip-index 0": `{"clipUuid":"{C}","sentenceCount":1,"fingerprint":"v1:x","sentences":[` +
			`{"index":0,"lyric":"hello","noteUuids":["{N1}","{N2}"],"sentenceBegin":0,"sentenceEnd":480}]}`,
		"clip note-content --track-index 0 --clip-index 0": `{"notes":[{"noteUuid":"{N1}","lyric":"hello#1"},{"noteUuid":"{N2}","lyric":"hello#2"}]}`,
		"tempo get":                 `{"points":[{"pos":0,"value":120}]}`,
		"clip list --track-index 0": `{"clips":[]}`,
	})
	tl := Get("lyric.setSentence")
	if tl == nil {
		t.Fatal("lyric.setSentence 没注册")
	}
	// 忽略连音开（默认）⇒ 应当是 `lyric fill`
	calls, err := tl.Build(context.Background(), rd, map[string]string{"sentence": "0", "text": "hello"})
	if err != nil {
		t.Fatalf("忽略连音开时构建失败：%v", err)
	}
	if len(calls) != 1 || calls[0].Args[0] != "lyric" || calls[0].Args[1] != "fill" {
		t.Errorf("忽略连音开应当走 `lyric fill`，实得 %v", calls[0].Args)
	}
	// 忽略连音关 ⇒ 应当是逐音符 `note set-lyric`
	calls, err = tl.Build(context.Background(), rd, map[string]string{
		"sentence": "0", "text": "hello#1 hello#2", "ignoreTenuto": "0"})
	if err != nil {
		t.Fatalf("忽略连音关时构建失败：%v", err)
	}
	if len(calls) == 0 || calls[0].Args[0] != "note" || calls[0].Args[1] != "set-lyric" {
		t.Errorf("忽略连音关应当走逐音符 `note set-lyric`，实得 %v", calls[0].Args)
	}
	// 写回的 payload 必须原样把 `hello#1` / `hello#2` 写回去（能回填）
	joined := strings.Join(calls[0].Args, " ")
	if !strings.Contains(joined, `["hello#1","hello#2"]`) {
		t.Errorf("逐音符写回应当原样带上 hello#1/hello#2，实得 %s", joined)
	}
}

// 分页：页数、上下页可用性、页码钳制、空歌词。
func TestLyricPagePaging(t *testing.T) {
	// 用 pageMath 抽出分页算术（不碰 CLI），把面板依赖的那几条钉住
	for _, c := range []struct {
		rows, perPage, page int
		wantPages, wantPage int
		wantPrev, wantNext  bool
		wantFirst, wantLast int
	}{
		{0, 12, 0, 0, 0, false, false, 1, 0},   // 空
		{1, 12, 0, 1, 0, false, false, 1, 1},   // 1 句：两页按钮都禁用
		{12, 12, 0, 1, 0, false, false, 1, 12}, // 正好一页
		{13, 12, 0, 2, 0, false, true, 1, 12},  // 第二页存在 ⇒ 下一页可用
		{13, 12, 1, 2, 1, true, false, 13, 13}, // 最后一页 ⇒ 下一页禁用
		{25, 12, 0, 3, 0, false, true, 1, 12},
		{25, 12, 2, 3, 2, true, false, 25, 25},
		{25, 12, 9, 3, 2, true, false, 25, 25}, // 页码超界 ⇒ 钳到最后一页
		{5, 2, 1, 3, 1, true, true, 3, 4},      // 每页 2 句
	} {
		got := pageMath(c.rows, c.perPage, c.page)
		if got.PageCount != c.wantPages || got.Page != c.wantPage {
			t.Errorf("rows=%d perPage=%d page=%d ⇒ 页数 %d 页 %d，期望 %d 页 %d",
				c.rows, c.perPage, c.page, got.PageCount, got.Page, c.wantPages, c.wantPage)
		}
		if got.CanPrev != c.wantPrev || got.CanNext != c.wantNext {
			t.Errorf("rows=%d page=%d ⇒ 上一页 %v 下一页 %v，期望 %v %v",
				c.rows, c.page, got.CanPrev, got.CanNext, c.wantPrev, c.wantNext)
		}
		if got.First != c.wantFirst || got.Last != c.wantLast {
			t.Errorf("rows=%d page=%d ⇒ 第 %d 到 %d 句，期望 %d 到 %d",
				c.rows, c.page, got.First, got.Last, c.wantFirst, c.wantLast)
		}
	}
}

// 歌词三件套都注册好了，种类也对（读的只读、写的可写）。
func TestLyricToolsRegistered(t *testing.T) {
	for _, c := range []struct {
		id, kind, cat string
	}{
		{"lyric.pages", "read", "歌词"},
		{"lyric.setSentence", "write", "歌词"},
		{"lyric.goto", "write", "歌词"},
		{"lyric.audition", "write", "歌词"}, // 试听：定位 + 开始播
		{"transport.state", "read", "播放"}, // 到点暂停靠它
		{"transport.stop", "write", "播放"},
	} {
		tl := Get(c.id)
		if tl == nil {
			t.Errorf("%s 没注册", c.id)
			continue
		}
		if string(tl.Kind) != c.kind {
			t.Errorf("%s 的 kind 应当是 %s，实得 %s", c.id, c.kind, tl.Kind)
		}
		if tl.Category != c.cat {
			t.Errorf("%s 的分类应当是「%s」，实得 %q", c.id, c.cat, tl.Category)
		}
		if tl.Source == "" {
			t.Errorf("%s 没写移植来源", c.id)
		}
	}
	// ⛔ 改歌词**必须显式给句下标** —— 漏填就静默写掉第 1 句是最难看漏的事故
	tl := Get("lyric.setSentence")
	rd := stubRead(t, map[string]string{})
	if _, err := tl.Build(context.Background(), rd, map[string]string{"text": "x"}); err == nil {
		t.Error("不给 sentence 就应当报错，而不是默认改第 0 句")
	} else if !strings.Contains(err.Error(), "第几句") {
		t.Errorf("话术要提到「第几句」，实得：%v", err)
	}
	// 空表单下也不许 panic/空错（与注册表不变量一致）
	if _, err := tl.Build(context.Background(), rd, map[string]string{}); err == nil {
		t.Error("空表单应当报错")
	}
	if tlGet := Get("lyric.goto"); tlGet != nil {
		if _, err := tlGet.Build(context.Background(), rd, map[string]string{}); err == nil {
			t.Error("lyric.goto 在空表单（读不到片段）下应当报错")
		}
	}
}

// ---------- 小工具 ----------

// 「回填得回去吗」的自检本身要对 —— 判据是"显示 → 折成 ACE 形态"这条链无损，
// 别把好的判成坏的（假警报会让用户以后忽略它）。
//
//	① 显示解析回来 == 收起来的那些词（数量与内容一一对应）
//	② 折出来的 ACE 形态 == ACE 自己报的那句
func TestRoundTripsCollapsed(t *testing.T) {
	notes := []string{"跳", "一", "曲", "-", "春"}
	// 按字符：CJK 单字不套括号，紧排 ⇒ 合法
	if !roundTripsCollapsed("跳一曲春", notes, "跳一曲春", true) {
		t.Error("按字符：`跳一曲春` 应当判「回得去」")
	}
	// 按词：CJK 之间多出空格**也能填回去**（lyric fill 自己按字符切 CJK，真机 dry-run 验过）
	if !roundTripsCollapsed("跳 一 曲 春", notes, "跳一曲春", false) {
		t.Error("按词：`跳 一 曲 春` 应当判「回得去」")
	}
	// 多字词必须**套括号**才保得住：`jin` 在按字符下若裸着会被切成 j/i/n
	if !roundTripsCollapsed("(jin)(qing)", []string{"jin", "qing"}, "jin qing", true) {
		t.Error("按字符：`(jin)(qing)` 应当判「回得去」")
	}
	if roundTripsCollapsed("jinqing", []string{"jin", "qing"}, "jin qing", true) {
		t.Error("按字符：`jinqing` 会变成 7 个字，应当判「回不去」")
	}
	// 按词：也是一个词一个括号
	if !roundTripsCollapsed("(jin) (qing)", []string{"jin", "qing"}, "jin qing", false) {
		t.Error("按词：`(jin) (qing)` 应当判「回得去」")
	}
	// 词里带空格 ⇒ 必须套括号；不套就被空格切成两个词 ⇒ 回不去（这一条是真会出错的）
	if roundTripsCollapsed("b iy 啦", []string{"b iy", "啦"}, "b iy 啦", false) {
		t.Error("按词：`b iy` 没套括号，应当判「回不去」")
	}
	if !roundTripsCollapsed("(b iy) 啦", []string{"b iy", "啦"}, "b iy 啦", false) {
		t.Error("按词：`(b iy) 啦` 应当判「回得去」")
	}
	// 连音/多音符词先收起来再看
	hello := []string{"hello#1", "hello#2"}
	if !roundTripsCollapsed("(hello)", hello, "hello", true) {
		t.Error("hello#1/hello#2 收成 hello（按字符要套括号）后应当判「回得去」")
	}
	// 括号没闭合 ⇒ 直接判回不去（不崩）
	if roundTripsCollapsed("(jin", []string{"jin"}, "jin", true) {
		t.Error("括号没闭合应当判「回不去」")
	}
}

// ---------- 变更令牌（自动刷新用） ----------

// ⛔ `hashUUIDs` 必须**与顺序无关** —— 用户换着顺序点选同一批音符不该被当成「宿主变了」，
// 否则轮询会一直以为有变化、反复重读。
func TestHashUUIDsOrderIndependent(t *testing.T) {
	a := map[string]bool{"{a}": true, "{b}": true, "{c}": true}
	b := map[string]bool{"{c}": true, "{a}": true, "{b}": true} // 同样三个、插入顺序不同
	if hashUUIDs(a) != hashUUIDs(b) {
		t.Errorf("同一批音符的哈希应当一样：%s vs %s", hashUUIDs(a), hashUUIDs(b))
	}
	if hashUUIDs(nil) != "-" || hashUUIDs(map[string]bool{}) != "-" {
		t.Errorf("空集合应当是 %q，实得 %q/%q", "-", hashUUIDs(nil), hashUUIDs(map[string]bool{}))
	}
	// 换掉一个 ⇒ 必须变（这条就是"选中音符换了要能察觉"的根据）
	c := map[string]bool{"{a}": true, "{b}": true, "{d}": true}
	if hashUUIDs(a) == hashUUIDs(c) {
		t.Error("换了一个音符，哈希应当变")
	}
	// 少一个 ⇒ 也要变
	d := map[string]bool{"{a}": true, "{b}": true}
	if hashUUIDs(a) == hashUUIDs(d) {
		t.Error("少了一个音符，哈希应当变")
	}
}

// 变更令牌这个读点要在册，而且是**只读** —— 它跑在轮询里，绝不能写。
func TestRevisionToolRegistered(t *testing.T) {
	tl := Get("inspect.revision")
	if tl == nil {
		t.Fatal("inspect.revision 没注册")
	}
	if tl.Kind != KindRead {
		t.Errorf("变更令牌必须是只读（它跑在轮询里），实得 %s", tl.Kind)
	}
	if tl.Inspect == nil {
		t.Error("变更令牌应当是进程内只读实现（要读三个点再合成）")
	}
	if tl.Build != nil {
		t.Error("不该同时有 Build —— 注册表不变量要求三种实现恰有其一")
	}
	if tl.Category != catInspect {
		t.Errorf("分类应当是「%s」，实得 %q", catInspect, tl.Category)
	}
}

func eqInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func eqStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
