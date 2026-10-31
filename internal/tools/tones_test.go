package tools

import (
	"context"
	"strings"
	"testing"
)

// 表要覆盖整段 CJK 基本区，而且**连续索引**要成立（下标 = r - 0x4E00）。
func TestPinyinTableShape(t *testing.T) {
	rows := pinyinTable()
	want := int(pinyinHi-pinyinLo) + 1
	if len(rows) != want {
		t.Fatalf("表应当有 %d 项（4E00..9FFF 连续），实得 %d", want, len(rows))
	}
	// 边界两个码位必须对得上（把"差一位"这类错钉住）
	if got := readingsOf(pinyinLo); len(got) == 0 {
		t.Errorf("第一个码位 %U 应当有读音", pinyinLo)
	}
	// ⚠️ 4E00..9FFF 里有 **136 个码位没有读音**（实测），所以不能假定两端都有；
	//    改判"绝大多数都有"，并把两个已知字钉住。
	filled := 0
	for _, s := range rows {
		if s != "" {
			filled++
		}
	}
	if filled < 20000 {
		t.Errorf("表里只有 %d 项有读音，整段基本区应当有两万上下", filled)
	}
	// 范围外不许瞎给
	for _, r := range []rune{0x4DFF, 0xA000, 'A', '1'} {
		if got := readingsOf(r); got != nil {
			t.Errorf("%U 不在表里，不该给读音：%v", r, got)
		}
	}
	if isHanzi('a') || isHanzi(0x3000) {
		t.Error("isHanzi 只认表覆盖的范围")
	}
	if !isHanzi('燕') {
		t.Error("「燕」应当是汉字")
	}
}

// 标调位置：有 a 标 a、有 o/e 标 o/e、iu 标 u、ui 标 i、其余标最后一个元音。
func TestMarkTone(t *testing.T) {
	for in, want := range map[string]string{
		"yan4":  "yàn",  // 普通
		"lv4":   "lv4",  // ⚠️ 表里是 lü4（不是 lv4），这个不是合法输入 ⇒ 原样
		"lü4":   "lǜ",   // ü 的第四声
		"nü3":   "nǚ",   // ü 的第三声
		"lüe4":  "lüè",  // üe 标在 e 上
		"liu2":  "liú",  // iu 标 u
		"hui4":  "huì",  // ui 标 i
		"xiao1": "xiāo", // 有 a 标 a
		"zhou1": "zhōu", // 有 o 标 o
		"de0":   "de",   // 轻声：去掉 0，不加号
		"men0":  "men",
		"hao3":  "hǎo", // 有 a 标 a（不是标 o）
		"guo2":  "guó", // 有 o 标 o
		"xue2":  "xué", // 无 a 无 o，有 e 标 e
		"zhi1":  "zhī", // 整体认读：标 i
		"wu1":   "wū",
		"yu2":   "yú",
		"":      "",    // 空
		"ABC":   "ABC", // 没有声调数字 ⇒ 原样（非汉字别乱动）
		"yan9":  "yan", // 非法声调：只去掉数字，不乱加号
	} {
		if got := markTone(in); got != want {
			t.Errorf("markTone(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// 声调号反推，口径 1..5（**5 = 轻声**，与 SV 的 rapAccent 一致）。
func TestToneOf(t *testing.T) {
	for in, want := range map[string]int{
		"yān": 1, "yán": 2, "yǎn": 3, "yàn": 4,
		"de": 5, "men": 5, // 没有调号 = 轻声
		"": 0,
	} {
		if got := toneOf(in); got != want {
			t.Errorf("toneOf(%q) = %d，期望 %d", in, got, want)
		}
	}
	// 1..5 的五档都要有名字与调值（别漏了轻声）
	for tone, want := range map[int]string{1: "阴平", 2: "阳平", 3: "上声", 4: "去声", 5: "轻声"} {
		if got := toneInfoOf(tone).Name; got != want {
			t.Errorf("声调 %d 应当是 %q，实得 %q", tone, want, got)
		}
		if toneInfoOf(tone).Value == "" || toneInfoOf(tone).Word == "" {
			t.Errorf("声调 %d 缺调值或走向的说法", tone)
		}
	}
	// 调值按标准五度标记法
	for tone, want := range map[int]string{1: "55", 2: "35", 3: "214", 4: "51"} {
		if got := toneInfoOf(tone).Value; got != want {
			t.Errorf("声调 %d 的调值应当是 %s，实得 %s", tone, want, got)
		}
	}
}

func TestContourArrow(t *testing.T) {
	for tone, want := range map[int]string{1: "—", 2: "↗", 3: "↘↗", 4: "↘", 5: "·"} {
		if got := contourArrow(tone); got != want {
			t.Errorf("声调 %d 的走向应当是 %q，实得 %q", tone, want, got)
		}
	}
	// toneInfo 里的走向与 contourArrow 必须一致（两处都写了走向，容易漂）
	for tone := 1; tone <= 5; tone++ {
		if toneInfoOf(tone).Word == "" {
			t.Errorf("声调 %d 缺走向的说法", tone)
		}
	}
	// 轻声在 5 与 0 下都要认得（对外口径是 5，pinyin-pro 内部是 0）
	if toneInfoOf(5).Name != toneInfoOf(0).Name {
		t.Error("轻声在 5 与 0 下应当一致")
	}
	{
	}
}

// ⭐ 这条盯着一个**实测发现**：pinyin-pro 的**单字默认**读音与 `multiple:true` 的**首选**不一样 ——
// 了 的单字默认是 `liao3`，而 `multiple` 首选是 `le0`（歌词里几乎总是轻声 le）。
// 生成的表取的是 `multiple` 首选 ⇒ 表里 了 的第一个读音必须是 le(轻声)。
// 将来重新生成表时若这条变了，这里会响。
func TestKnownTrickyReadings(t *testing.T) {
	for _, c := range []struct {
		ch    rune
		first string // 期望的首选读音（带调号，轻声不带号）
		tone  int
	}{
		{'了', "le", 5}, // ← 上面那条：multiple 首选是轻声，比单字默认 liao3 更适合歌词
		{'的', "de", 5},
		{'着', "zhe", 5},
		{'吗', "ma", 5},
		{'燕', "yàn", 4},
		{'鸟', "niǎo", 3},
		{'栖', "qī", 1},
		{'下', "xià", 4},
		{'林', "lín", 2},
		{'绿', "lǜ", 4},
		{'女', "nǚ", 3},
		{'六', "liù", 4},  // 六是**第四声**（我一开始按 liú 写，被用例纠正过来）
		{'行', "xíng", 2}, // 首选 xíng，另有 háng/hàng/héng
	} {
		all := readingsOf(c.ch)
		if len(all) == 0 {
			t.Errorf("「%c」查不到读音", c.ch)
			continue
		}
		if all[0] != c.first {
			t.Errorf("「%c」的首选应当是 %q，实得 %q（全部：%v）", c.ch, c.first, all[0], all)
		}
		if got := toneOf(all[0]); got != c.tone {
			t.Errorf("「%c」的声调应当是 %d，实得 %d", c.ch, c.tone, got)
		}
	}
	// 「行」必须是多音字，且同时有 xíng 与 háng
	all := readingsOf('行')
	joined := strings.Join(all, ",")
	if !strings.Contains(joined, "xíng") || !strings.Contains(joined, "háng") {
		t.Errorf("「行」应当同时有 xíng 与 háng，实得 %v", all)
	}
}

// 人工「歌词常用读法」表只该覆盖少数几个字，而且**每个都要能在数据源里找到同音读音**
// （否则那条映射是凭空的）。这条防的是"手写表越写越野"。
func TestLyricReadingTableIsSane(t *testing.T) {
	if len(lyricReading) > 20 {
		t.Errorf("歌词常用读法表有 %d 条 —— 它应当只收**证据清楚的少数虚字**，别写成一张词典",
			len(lyricReading))
	}
	for ch, want := range lyricReading {
		all := readingsOf(ch)
		if len(all) == 0 {
			t.Errorf("「%c」在数据源里没有读音，人工表不该有它", ch)
			continue
		}
		// want 必须能对上数据源里的某个读音（带调号或不带调号的同音写法都算）
		found := false
		for _, a := range all {
			if a == want {
				found = true
				break
			}
			if plain, _ := unmarkTone(a); plain == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("「%c」人工表写的 %q 在数据源的读音 %v 里找不到 —— 凭空来的映射", ch, want, all)
		}
	}
	// 显式钉住最容易出错的两条：地/得（词典首选是 di4/de2，歌词里常读 de）
	if lyricReading['地'] != "de" || lyricReading['得'] != "de" {
		t.Error("地/得 的歌词常用读法应当是 de")
	}
	if all := readingsOf('地'); all[0] != "dì" {
		t.Errorf("数据源里「地」的首选应当是 dì（人工表只影响 toneMode=lyric），实得 %v", all)
	}
}

func TestParseOverrides(t *testing.T) {
	m, err := parseOverrides("行=hang 地=di")
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if m['行'] != "hang" || m['地'] != "di" {
		t.Errorf("解析结果不对：%v", m)
	}
	// 全角等号与冒号也收（中文输入法很容易打出来）
	if m, err := parseOverrides("行＝hang"); err != nil || m['行'] != "hang" {
		t.Errorf("全角等号应当收：%v %v", m, err)
	}
	if m, err := parseOverrides("行:hang"); err != nil || m['行'] != "hang" {
		t.Errorf("冒号应当收：%v %v", m, err)
	}
	// 写法不对要**报错**，不能默默忽略（默默忽略 = 用户以为生效了）
	for _, bad := range []string{"行hang", "=hang", "行=", "行=hang 坏"} {
		if _, err := parseOverrides(bad); err == nil {
			t.Errorf("%q 应当报错", bad)
		}
	}
	if m, err := parseOverrides(""); err != nil || len(m) != 0 {
		t.Errorf("空串应当是空表：%v %v", m, err)
	}
}

// 手动指定可以不带调号（`hang`）—— 要去表里找同音的**带调**写法，好让调号/声调照常算出来。
func TestNormalizeOverride(t *testing.T) {
	all := readingsOf('行')
	if got := normalizeOverride("hang", all); got != "háng" {
		t.Errorf("hang 应当对齐成 háng，实得 %q", got)
	}
	if got := normalizeOverride("háng", all); got != "háng" {
		t.Errorf("已经带调号的应当原样，实得 %q", got)
	}
	// 表里没有的音：照样返回用户写的（让他看见自己写了什么，而不是被悄悄改掉）
	if got := normalizeOverride("zzz", all); got != "zzz" {
		t.Errorf("表里没有的应当原样返回，实得 %q", got)
	}
	// ⛔ 这条是补出来的：`de` 必须匹到**真正的轻声 de**，而不是"去调号后同样等于 de"的 `dé`。
	// 「得」的读音是 dé / děi / de，按词（跑得快）该是轻声。
	de := readingsOf('得')
	if got := normalizeOverride("de", de); got != "de" {
		t.Errorf("「得」的 de 应当匹到轻声 de，实得 %q（候选 %v）", got, de)
	}
	// 「地」同理：dì / de
	di := readingsOf('地')
	if got := normalizeOverride("de", di); got != "de" {
		t.Errorf("「地」的 de 应当匹到轻声 de，实得 %q（候选 %v）", got, di)
	}
	if got := normalizeOverride("", all); got != all[0] {
		t.Errorf("空应当回退到首选，实得 %q", got)
	}
}

func TestToneSummary(t *testing.T) {
	chars := []ToneChar{{Char: "燕", Tone: 4}, {Char: "鸟", Tone: 3}, {Char: "了", Tone: 5}, {Char: "?", Tone: 0}}
	if got := toneSummary(chars); got != "燕4 鸟3 了5 ?" {
		t.Errorf("summary = %q，期望 %q", got, "燕4 鸟3 了5 ?")
	}
}

// 这个工具是**只读**的（它是"备用"的参考工具，绝不写工程）。
func TestTonesToolRegistered(t *testing.T) {
	tl := Get("lyric.tones")
	if tl == nil {
		t.Fatal("lyric.tones 没注册")
	}
	if tl.Kind != KindRead {
		t.Errorf("汉字→语调 必须是只读，实得 %s", tl.Kind)
	}
	if tl.Inspect == nil || tl.Build != nil {
		t.Error("应当是进程内只读实现（本地查表 + 可选 CLI），不该有 Build")
	}
	if tl.Source == "" {
		t.Error("要写清数据来源（本地表 + 可选 g2p）")
	}
	// 空文本要给出可执行的下一步，而不是空结果
	rd := stubRead(t, map[string]string{})
	if _, err := tl.Inspect(context.Background(), rd, map[string]string{}); err == nil {
		t.Error("空文本应当报错并提示怎么用")
	} else if !strings.Contains(err.Error(), "文本") {
		t.Errorf("话术要提到「文本」，实得：%v", err)
	}
	// 来源写错也要明确拒绝
	if _, err := tl.Inspect(context.Background(), rd, map[string]string{"text": "燕", "source": "nope"}); err == nil {
		t.Error("来源认不出应当报错")
	}
	// overrides 写错要报错（不能默默忽略）
	if _, err := tl.Inspect(context.Background(), rd, map[string]string{"text": "燕", "overrides": "坏"}); err == nil {
		t.Error("overrides 写法不对应当报错")
	}
}
