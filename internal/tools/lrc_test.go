package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 时间戳格式：**逐字复刻原脚本的 `formatTime`**（`[mm:ss.cc]`、截断不四舍五入、支持负数）。
//
// ⚠️ 页面侧另有一份同规则的 JS（要实时重渲染），`devtools/check-ui.js` 用**下面这组向量**两边都跑 ——
// 时间戳两边算得不一样的话，用户看到的和复制出去的就是两份东西。
func TestFormatLRCTime(t *testing.T) {
	for _, c := range []struct {
		sec, off float64
		want     string
	}{
		{12.3287, 0, "[00:12.32]"}, // 真机：tick 14400 @146 BPM，ACE 自己导出的第 1 行就是它
		{0, 0, "[00:00.00]"},
		{59.999, 0, "[00:59.99]"}, // 截断不是四舍五入
		{60.5, 0, "[01:00.50]"},
		{65.0, 1.5, "[01:06.50]"},
		{-0.5, 0, "[-00:00.50]"}, // 负偏移把时间推到 0 之前 —— 原脚本会写成负数
		{12.32, -0.5, "[00:11.82]"},
		{3661.25, 0, "[61:01.25]"}, // 分钟不封顶（原脚本就是这么写的：min 可以 > 59）
	} {
		if got := formatLRCTime(c.sec, c.off); got != c.want {
			t.Errorf("formatLRCTime(%v, %v) = %q，期望 %q", c.sec, c.off, got, c.want)
		}
	}
}

// tick → 秒：**分段**积（ACE 的速度表是分段常速）。
func TestTickToSec(t *testing.T) {
	rd := stubRead(t, map[string]string{
		// 120 BPM 管到 9600，之后 60 BPM；并且**同一位置有重复点**（真机就是这样成对出现的）
		"tempo get": `{"points":[
			{"pos":0,"value":120},
			{"pos":9600,"value":120},
			{"pos":9600,"value":60},
			{"pos":19200,"value":60}
		]}`,
	})
	sec := tickToSecFrom(context.Background(), rd)
	if sec == nil {
		t.Fatal("读不到速度表")
	}
	// 480 PPQ ⇒ tick/秒 = BPM×8
	for _, c := range []struct{ tick, want float64 }{
		{0, 0},
		{480, 0.5},  // 120 BPM：960 tick/秒
		{9600, 10},  // 第一段结束
		{14400, 20}, // 第二段：60 BPM ⇒ 480 tick/秒
		{19200, 30}, // 第二段结束
		{24000, 40}, // 过了最后一个点也照算（延续最后一段）
	} {
		if got := sec(c.tick); got < c.want-1e-9 || got > c.want+1e-9 {
			t.Errorf("sec(%v) = %v，期望 %v", c.tick, got, c.want)
		}
	}
	// 读不到速度表 ⇒ 返回 nil（**不许**静默当成匀速）
	if tickToSecFrom(context.Background(), stubRead(t, map[string]string{})) != nil {
		t.Error("读不到速度表时应当返回 nil")
	}
}

// ⭐⭐ 一行文字 + **逐字符时间** —— 这是「分割自动加入新时间轴」的数据来源。
//
// 用恒等 sec 函数（sec(t)=t）方便断言：tick 就是秒。
func TestJoinLyricsTimed(t *testing.T) {
	id := func(tick float64) float64 { return tick }
	note := func(pos int64, lyr string) aceNote { return aceNote{Pos: pos, Lyric: lyr} }

	// 中文：紧排，一字符一时间、一字符一个词号
	txt, charSec, charNote := joinLyricsTimed([]aceNote{note(100, "跳"), note(200, "一"), note(300, "曲")}, true, id, 0)
	if txt != "跳一曲" {
		t.Errorf("中文应当紧排，实得 %q", txt)
	}
	if !eqFloats(charSec, []float64{100, 200, 300}) {
		t.Errorf("逐字符时间 = %v，期望 [100 200 300]", charSec)
	}
	if !eqInts(charNote, []int{0, 1, 2}) {
		t.Errorf("逐字符词号 = %v，期望 [0 1 2]", charNote)
	}

	// ⭐ `#1#2#3`：合成一个词，**只留第一段的时间**，而且**整串同一个词号**
	// （⇒ 从中间断是不可能的：分割点只能落在这个词与下一个词之间）
	txt, charSec, charNote = joinLyricsTimed([]aceNote{
		note(100, "hello#1"), note(200, "hello#2"), note(300, "hello#3")}, true, id, 0)
	if txt != "hello" {
		t.Errorf("`hello#1 hello#2 hello#3` 应当合成 `hello`，实得 %q", txt)
	}
	if !eqFloats(charSec, []float64{100, 100, 100, 100, 100}) {
		t.Errorf("合成词的逐字符时间应当都是**第一段**的 100，实得 %v", charSec)
	}
	if !eqInts(charNote, []int{0, 0, 0, 0, 0}) {
		t.Errorf("合成词应当整串同一个词号，实得 %v", charNote)
	}

	// 两个不同的多音符词：各自一段
	txt, _, _ = joinLyricsTimed([]aceNote{note(1, "hello#1"), note(2, "hello#2"), note(3, "world#1")}, true, id, 0)
	if txt != "hello world" {
		t.Errorf("两个词应当各自保留，实得 %q", txt)
	}

	// 拉丁：空格分开；⭐ **空格归前一个词**（时间与词号都用前一个）
	// ⇒ 光标停在空格后面时，断点落在下一个词的开头（用户口径：断点在两个音符之间的间隙）
	txt, charSec, charNote = joinLyricsTimed([]aceNote{note(10, "jin"), note(20, "qing")}, true, id, 0)
	if txt != "jin qing" {
		t.Errorf("拉丁词必须留空格，实得 %q", txt)
	}
	if !eqFloats(charSec, []float64{10, 10, 10, 10, 20, 20, 20, 20}) {
		t.Errorf("逐字符时间 = %v（空格归前一个词 ⇒ 第 4 个字符是 10）", charSec)
	}
	if !eqInts(charNote, []int{0, 0, 0, 0, 1, 1, 1, 1}) {
		t.Errorf("逐字符词号 = %v（空格算前一个词），实得 %v", []int{0, 0, 0, 0, 1, 1, 1, 1}, charNote)
	}

	// 连音/换气即使在（防御）也要被丢掉
	txt, _, _ = joinLyricsTimed([]aceNote{note(1, "a"), note(2, "-"), note(3, "br"), note(4, "b")}, true, id, 0)
	if txt != "a b" {
		t.Errorf("`-`/`br` 应当丢掉，实得 %q", txt)
	}

	// clipBegin 加到 tick 上（片段不在 0 位置时，时间才是绝对时间）
	_, charSec, _ = joinLyricsTimed([]aceNote{note(100, "x")}, true, id, 5000)
	if !eqFloats(charSec, []float64{5100}) {
		t.Errorf("clipBegin 应当加到位置上，实得 %v", charSec)
	}
}

// 切行 + 合并 + 去掉空行：一个完整的 `lyric.lrc` 读取。
//
// 这是本功能的核心用例，一次钉住四件事：
//
//	① 按**缺口**切行 ② `-` 不进文字 ③ `#1#2` 合成一个词 ④ **相邻重复行合并成一行多个时间戳**
//	⑤ 空行（末尾换气造成）**跳过而不是崩** —— 原脚本在这里会取 undefined 直接报错。
func TestLRCExportGroupMergeAndSkip(t *testing.T) {
	rd := stubRead(t, map[string]string{
		"editor status": `{"isAvailable":true,"trackIndex":0,"clipIndex":0,"clipName":"歌","editorType":"sing","clipUuid":"{C}"}`,
		"clip note-content --track-index 0 --clip-index 0": `{"notes":[
			{"pos":0,   "dur":480, "endPos":480,  "lyric":"la",       "language":"Chinese"},
			{"pos":480, "dur":480, "endPos":960,  "lyric":"la",       "language":"Chinese"},
			{"pos":960, "dur":480, "endPos":1440, "lyric":"br",       "language":"Chinese"},
			{"pos":1920,"dur":480, "endPos":2400, "lyric":"la",       "language":"Chinese"},
			{"pos":2400,"dur":480, "endPos":2880, "lyric":"la",       "language":"Chinese"},
			{"pos":2880,"dur":480, "endPos":3360, "lyric":"br",       "language":"Chinese"},
			{"pos":3840,"dur":480, "endPos":4320, "lyric":"跳",       "language":"Chinese"},
			{"pos":4320,"dur":480, "endPos":4800, "lyric":"-",        "language":"Chinese"},
			{"pos":4800,"dur":480, "endPos":5280, "lyric":"曲",       "language":"Chinese"},
			{"pos":5760,"dur":480, "endPos":6240, "lyric":"hello#1",  "language":"English"},
			{"pos":6240,"dur":480, "endPos":6720, "lyric":"hello#2",  "language":"English"},
			{"pos":7200,"dur":480, "endPos":7680, "lyric":"la",       "language":"Chinese"},
			{"pos":7680,"dur":480, "endPos":8160, "lyric":"la",       "language":"Chinese"},
			{"pos":8160,"dur":480, "endPos":8640, "lyric":"br",       "language":"Chinese"}
		]}`,
		// 恒速 120 BPM ⇒ 960 tick/秒：0→0 s，1920→2 s，3840→4 s，5760→6 s，7200→7.5 s
		"tempo get": `{"points":[{"pos":0,"value":120}]}`,
	})
	tl := Get("lyric.lrc")
	if tl == nil {
		t.Fatal("lyric.lrc 没注册")
	}
	got, err := tl.Inspect(context.Background(), rd, map[string]string{})
	if err != nil {
		t.Fatalf("导出失败：%v", err)
	}
	res, ok := got.(*lrcResult)
	if !ok {
		t.Fatalf("回包类型不对：%T", got)
	}

	// 切出来的原始行：[la la] [la la] [跳 曲] [hello] [la la] []（末尾换气造成的空行）
	if res.RawLines != 6 {
		t.Errorf("原始行数应当 6（含末尾那条空行），实得 %d", res.RawLines)
	}
	if res.Skipped != 1 {
		t.Errorf("应当跳过 1 条空行（原脚本在这里会崩），实得 %d", res.Skipped)
	}
	// 合并之后：前两条 `la la` **相邻且相同** ⇒ 合成一行两条时间戳
	if res.LineCount != 4 {
		t.Fatalf("合并后应当 4 行，实得 %d：%+v", res.LineCount, res.Rows)
	}
	if res.MergedRows != 1 {
		t.Errorf("应当有 1 行是多时间戳的，实得 %d", res.MergedRows)
	}
	if !eqFloats(res.Rows[0].Times, []float64{0, 2}) {
		t.Errorf("第 1 行应当是两条时间戳 [0 2]（相邻重复行合并），实得 %v", res.Rows[0].Times)
	}
	if res.Rows[1].Text != "跳曲" {
		t.Errorf("`-` 应当不进文字：期望 `跳曲`，实得 %q", res.Rows[1].Text)
	}
	if !eqFloats(res.Rows[1].Times, []float64{4}) {
		t.Errorf("第 2 行时间应当是 4（tick 3840 @120BPM），实得 %v", res.Rows[1].Times)
	}
	// ⭐ `#1#2` 合成一个词 —— 用户 2026-10-05 点名要的
	if res.Rows[2].Text != "hello" {
		t.Errorf("`hello#1 hello#2` 应当合成 `hello`，实得 %q", res.Rows[2].Text)
	}
	if !eqFloats(res.Rows[2].Times, []float64{6}) {
		t.Errorf("第 3 行时间应当是 6（tick 5760），实得 %v", res.Rows[2].Times)
	}
	// 最后一行 la la（tick 7200）与第 1 行文字相同，但**中间隔着别的行** ⇒ 不合并（只合并**相邻**的）
	if res.Rows[3].Text != "la la" || !eqFloats(res.Rows[3].Times, []float64{7.5}) {
		t.Errorf("第 4 行应当是 `la la` 一条时间戳 [7.5]，实得 %q %v", res.Rows[3].Text, res.Rows[3].Times)
	}
	// 成品文本：合并行写成一行多个时间戳；**气口不插空行**
	want := "[00:00.00][00:02.00]la la\n[00:04.00]跳曲\n[00:06.00]hello\n[00:07.50]la la\n"
	if res.LRC != want {
		t.Errorf("LRC 文本不对：\n实得 %q\n期望 %q", res.LRC, want)
	}
	if strings.Contains(res.LRC, "] \n") || strings.Contains(res.LRC, "\n\n") {
		t.Error("气口不该插空行")
	}
	// 逐字符时间：第 3 行 `hello` 五个字符都应当是 6
	if !eqFloats(res.Rows[2].CharSec, []float64{6, 6, 6, 6, 6}) {
		t.Errorf("`hello` 的逐字符时间应当都是 6，实得 %v", res.Rows[2].CharSec)
	}
	// 偏移：三个框折成秒，且作用在渲染上
	got2, err := tl.Inspect(context.Background(), rd, map[string]string{
		"offsetMin": "1", "offsetSec": "2", "offsetMs": "500"})
	if err != nil {
		t.Fatalf("带偏移导出失败：%v", err)
	}
	r2 := got2.(*lrcResult)
	if r2.OffsetSec != 62.5 {
		t.Errorf("偏移应当是 62.5 秒，实得 %v", r2.OffsetSec)
	}
	if !strings.HasPrefix(r2.LRC, "[01:02.50]") {
		t.Errorf("偏移应当体现在时间戳上，实得第一行 %q", strings.SplitN(r2.LRC, "\n", 2)[0])
	}
	// ⚠️ 原始秒不带偏移（页面侧要能改偏移重渲染）
	if r2.Rows[0].Sec != 0 {
		t.Errorf("Rows[].Sec 应当是**原始**秒（不含偏移），实得 %v", r2.Rows[0].Sec)
	}
}

// 换气正则：原脚本的 `/^(l?br\d*)$/`。
func TestIsBreathLyric(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"br", true}, {"br1", true}, {"br12", true}, {"lbr", true}, {"lbr3", true},
		{"breath", false}, {"b", false}, {"brx", false}, {"br-", false}, {"跳", false}, {"", false},
	} {
		if got := isBreathLyric(c.in); got != c.want {
			t.Errorf("isBreathLyric(%q) = %v，期望 %v", c.in, got, c.want)
		}
	}
}

// 落盘：Plan **一个字节都不写**；Apply 写 UTF-8 带 BOM；路径必须 `.lrc` 结尾。
func TestLRCFilePlanApply(t *testing.T) {
	tl := Get("lyric.lrcFile")
	if tl == nil {
		t.Fatal("lyric.lrcFile 没注册")
	}
	if tl.Kind != KindWrite || tl.Plan == nil || tl.Apply == nil {
		t.Fatal("落盘工具必须是 write 且 Plan/Apply 成对")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "歌.lrc")
	text := "[00:01.00]跳一曲\n[00:02.00]hello\n"

	// Plan：报数但**不落盘**
	got, err := tl.Plan(context.Background(), nil, map[string]string{"path": path, "text": text})
	if err != nil {
		t.Fatalf("Plan 失败：%v", err)
	}
	rep := got.(*lrcFileReport)
	if !rep.DryRun || rep.Lines != 2 || rep.Overwrote {
		t.Errorf("干跑报告不对：%+v", rep)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("干跑**不该**产生文件")
	}

	// Apply：真的落盘，而且带 BOM
	if _, err = tl.Apply(context.Background(), nil, map[string]string{"path": path, "text": text}); err != nil {
		t.Fatalf("Apply 失败：%v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回失败：%v", err)
	}
	if len(raw) < 3 || raw[0] != 0xEF || raw[1] != 0xBB || raw[2] != 0xBF {
		t.Error("落盘应当是 UTF-8 带 BOM（Windows 中文播放器最认这个）")
	}
	if string(raw[3:]) != text {
		t.Errorf("落盘内容不一致：%q", string(raw[3:]))
	}
	// 再写一次要报"覆盖了"
	got, err = tl.Apply(context.Background(), nil, map[string]string{"path": path, "text": text})
	if err != nil {
		t.Fatalf("覆盖写失败：%v", err)
	}
	if !got.(*lrcFileReport).Overwrote {
		t.Error("同名文件已存在时应当报 Overwrote")
	}

	// ⛔ 只写 .lrc：别变成一个能往任意路径写字的通用口子
	for _, bad := range []string{filepath.Join(dir, "x.txt"), filepath.Join(dir, "x"), ""} {
		if _, err := tl.Apply(context.Background(), nil, map[string]string{"path": bad, "text": text}); err == nil {
			t.Errorf("路径 %q 应当被拒（只收 .lrc）", bad)
		}
	}
	// 空文本也要拒（否则会把一个空文件写上去）
	if _, err := tl.Apply(context.Background(), nil, map[string]string{"path": path, "text": "  "}); err == nil {
		t.Error("空文本应当被拒")
	}
	// 目录不存在 ⇒ 报错，**不替用户建目录**
	missing := filepath.Join(dir, "没有这个目录", "x.lrc")
	if _, err := tl.Apply(context.Background(), nil, map[string]string{"path": missing, "text": text}); err == nil {
		t.Error("目录不存在时应当报错")
	}
}

// LRC 两个工具都在册、种类对、来源写了。
func TestLRCToolsRegistered(t *testing.T) {
	for _, c := range []struct{ id, kind, cat string }{
		{"lyric.lrc", "read", "歌词"},
		{"lyric.lrcFile", "write", "歌词"},
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
}

// 「忽略短间隙」（用户 2026-10-05：「增加个忽略短间隙选项」）。
//
// 原脚本是**有任何缝就换行**；实测真机最小的缝只有 30 tick（`hao` 之间的断音）⇒ 一行一个音节。
// gapTicks = 0 必须与原脚本**逐字节相同**；调大就只把大缝当换行。
func TestLRCGapOption(t *testing.T) {
	// 两个 30 tick 的小缝 + 一个 480 tick 的大缝
	notes := `{"notes":[
		{"pos":0,   "dur":480, "endPos":480,  "lyric":"a", "language":"English"},
		{"pos":510, "dur":480, "endPos":990,  "lyric":"b", "language":"English"},
		{"pos":990, "dur":480, "endPos":1470, "lyric":"c", "language":"English"},
		{"pos":1950,"dur":480, "endPos":2430, "lyric":"d", "language":"English"}
	]}`
	rd := stubRead(t, map[string]string{
		"editor status": `{"isAvailable":true,"trackIndex":0,"clipIndex":0,"clipName":"g","editorType":"sing","clipUuid":"{C}"}`,
		"clip note-content --track-index 0 --clip-index 0": notes,
		"tempo get": `{"points":[{"pos":0,"value":120}]}`, // 960 tick/秒
	})
	tl := Get("lyric.lrc")

	// gapTicks = 0（默认，= 原脚本）：两处缝都换行 ⇒ 3 行
	got, err := tl.Inspect(context.Background(), rd, map[string]string{})
	if err != nil {
		t.Fatalf("导出失败：%v", err)
	}
	res := got.(*lrcResult)
	if res.LineCount != 3 {
		t.Errorf("gapTicks=0 应当切 3 行（有任何缝就换行），实得 %d：%+v", res.LineCount, res.Rows)
	}
	if res.LRC != "[00:00.00]a\n[00:00.53]b c\n[00:02.03]d\n" {
		t.Errorf("gapTicks=0 的成品不对：%q", res.LRC)
	}

	// gapTicks = 60：30 tick 的小缝被忽略（并进上一行），480 的大缝仍换行 ⇒ 2 行
	got, err = tl.Inspect(context.Background(), rd, map[string]string{"gapTicks": "60"})
	if err != nil {
		t.Fatalf("带 gapTicks 导出失败：%v", err)
	}
	res = got.(*lrcResult)
	if res.GapTicks != 60 {
		t.Errorf("回包应当带上 GapTicks=60，实得 %d", res.GapTicks)
	}
	if res.LineCount != 2 {
		t.Errorf("gapTicks=60 应当切 2 行，实得 %d：%+v", res.LineCount, res.Rows)
	}
	if res.Rows[0].Text != "a b c" {
		t.Errorf("小缝被忽略后第一行应当是 `a b c`，实得 %q", res.Rows[0].Text)
	}
	if res.LRC != "[00:00.00]a b c\n[00:02.03]d\n" {
		t.Errorf("gapTicks=60 的成品不对：%q", res.LRC)
	}
}

// 「CJK 之间不加空格」这个选项：默认开（`跳一曲`），关掉就一律空格。
// ⚠️ 空格规则**跟 ACE 的语种字段无关**（用户：「不按语种」）—— 拼音是拉丁字符，两边照样加空格。
func TestLRCCJKTightOption(t *testing.T) {
	notes := `{"notes":[
		{"pos":0,   "dur":480, "endPos":480,  "lyric":"跳",   "language":"Chinese"},
		{"pos":480, "dur":480, "endPos":960,  "lyric":"一",   "language":"Chinese"},
		{"pos":960, "dur":480, "endPos":1440, "lyric":"sui",  "language":"Chinese"},
		{"pos":1440,"dur":480, "endPos":1920, "lyric":"zhe",  "language":"Chinese"}
	]}`
	rd := stubRead(t, map[string]string{
		"editor status": `{"isAvailable":true,"trackIndex":0,"clipIndex":0,"clipName":"c","editorType":"sing","clipUuid":"{C}"}`,
		"clip note-content --track-index 0 --clip-index 0": notes,
		"tempo get": `{"points":[{"pos":0,"value":120}]}`,
	})
	tl := Get("lyric.lrc")
	// 默认开：汉字紧排、拉丁（拼音）留空格 —— **拼音的 language 是 Chinese 也不管**
	got, err := tl.Inspect(context.Background(), rd, map[string]string{})
	if err != nil {
		t.Fatalf("导出失败：%v", err)
	}
	res := got.(*lrcResult)
	if res.Rows[0].Text != "跳一 sui zhe" {
		t.Errorf("默认应当是 `跳一 sui zhe`（汉字紧、拉丁分），实得 %q", res.Rows[0].Text)
	}
	if !res.CJKTight {
		t.Error("默认应当是 CJK 紧排开着")
	}
	// 关掉：一律空格
	got, _ = tl.Inspect(context.Background(), rd, map[string]string{"cjkTight": "0"})
	res = got.(*lrcResult)
	if res.Rows[0].Text != "跳 一 sui zhe" {
		t.Errorf("关掉 CJK 紧排后应当是 `跳 一 sui zhe`，实得 %q", res.Rows[0].Text)
	}
}

func eqFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] < b[i]-1e-9 || a[i] > b[i]+1e-9 {
			return false
		}
	}
	return true
}
