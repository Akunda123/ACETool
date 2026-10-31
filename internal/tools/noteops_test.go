package tools

import (
	"context"
	"strings"
	"testing"
)

// ---------- 粘贴分割规则（原脚本 pasteLyricsButton 里那段） ----------

// 逐条对着原脚本的分支验：① 汉字无假名看空格占比；② 有假名；③ 英文；④ 标点。
func TestSplitLyricsForPaste(t *testing.T) {
	for _, c := range []struct {
		name string
		raw  string
		want []string
	}{
		// 中文：空格占比低 ⇒ **一字一音符**，跳过中文标点
		{"中文一字一音符", "燕鸟栖下林", []string{"燕", "鸟", "栖", "下", "林"}},
		{"中文带标点", "燕鸟，栖下。林！", []string{"燕", "鸟", "栖", "下", "林"}},
		{"中文带空格（占比低）", "燕 鸟", []string{"燕", "鸟"}}, // 1/3 ≈ 0.33 ≥ 0.25 → 走分词，结果一样
		// 中文：空格占比高 ⇒ **按空格分词**（词 = 音符）
		{"中文按词", "燕鸟 栖下 林", []string{"燕鸟", "栖下", "林"}},
		// 英文：有空格 ⇒ 按空格
		{"英文按词", "la la la", []string{"la", "la", "la"}},
		// 英文：没空格 ⇒ 整串一个词
		{"英文整串", "happy", []string{"happy"}},
		// 日语假名：没空格 ⇒ 逐假名
		{"日语逐假名", "さくら", []string{"さ", "く", "ら"}},
		// 日语有空格 ⇒ 按空格
		{"日语按空格", "さくら の", []string{"さくら", "の"}},
		// 换行/制表符当空格（原脚本明说：否则 "world\nGoodbye" 会被拼成一个词）
		{"换行当空格", "world\nGoodbye", []string{"world", "Goodbye"}},
		{"制表当空格", "a\tb", []string{"a", "b"}},
		// 多段空白要滤掉空段
		{"连续空格", "la  la", []string{"la", "la"}},
		// 混合：有汉字 ⇒ 走汉字分支（即使也有英文）
		{"汉字+英文", "燕 鸟 fly", []string{"燕", "鸟", "fly"}},
	} {
		got := splitLyricsForPaste(c.raw)
		if !eqStrs(got, c.want) {
			t.Errorf("%s：splitLyricsForPaste(%q) = %v，期望 %v", c.name, c.raw, got, c.want)
		}
	}
}

// 空格占比的口径：分母是**字符数**，不是字节数（Go 里最容易写错的地方）。
//
// "燕 鸟 栖" 是 5 个字符、2 个空格 ⇒ 0.4 ≥ 0.25 ⇒ 按空格分词。
// 若误用字节数（3 汉字 ×3 + 2 空格 = 11 字节）⇒ 0.18 < 0.25 ⇒ 会走"一字一音符"，
// 结果虽然碰巧一样，但判定路径完全不同 —— 用一条**结果会不同**的样例把它钉死：
// "燕 鸟" = 3 字符 / 1 空格 = 0.33（≥0.25 → 分词：[燕, 鸟]）；
// 按字节 = 7 字节 ⇒ 0.14（<0.25 → 一字一音符，跳过空格，也是 [燕, 鸟]）……还是同结果。
// 所以改用**多字词**：只有"按空格分词"才会保留 "燕鸟" 这个词。
func TestSplitPasteSpaceRatioIsRuneBased(t *testing.T) {
	// "燕鸟 栖下林" = 7 字符、1 空格 ⇒ 0.142 < 0.25 ⇒ 一字一音符（词被拆开）
	if got := splitLyricsForPaste("燕鸟 栖下林"); !eqStrs(got, []string{"燕", "鸟", "栖", "下", "林"}) {
		t.Errorf("空格占比低时应当一字一音符，实得 %v", got)
	}
	// "燕鸟 栖下 林" = 8 字符、2 空格 ⇒ 0.25 ≥ 0.25 ⇒ 按空格分词（保留"燕鸟"）
	if got := splitLyricsForPaste("燕鸟 栖下 林"); !eqStrs(got, []string{"燕鸟", "栖下", "林"}) {
		t.Errorf("空格占比到 0.25 时应当按空格分词，实得 %v", got)
	}
	// 纯中文无空格：一字一音符
	if got := splitLyricsForPaste("燕鸟"); !eqStrs(got, []string{"燕", "鸟"}) {
		t.Errorf("无空格中文应当一字一音符，实得 %v", got)
	}
	// 若按**字节**算占比，上面第二条会是 0.25 以下（11 字节里 2 空格 = 0.18）⇒ 一字一音符。
	// 这条断言就是对字节口径的否决。
}

// 中文标点表要真跳过（原脚本那个字符类逐字对一遍，防止我抄漏）。
func TestCJKPunctSet(t *testing.T) {
	for _, r := range []rune("？！。，、；：“”‘’（）《》〈〉【】…—·～") {
		if !cjkPunct[r] {
			t.Errorf("%q 应当在中文标点表里", r)
		}
	}
	// 不该误伤普通字符
	for _, r := range []rune("燕鸟abc123 ") {
		if cjkPunct[r] {
			t.Errorf("%q 不该被当中文标点", r)
		}
	}
}

// ---------- 自动拼接：按新时长分组 ----------

// `note resize` 一次只能给一个 `--dur` ⇒ 必须按新时长分组。
// 这条盯住"别退化成一音符一条命令"（300 个音符那样要跑 20 秒）。
func TestJoinGroupsByDuration(t *testing.T) {
	// 5 个音符、等距 480 ⇒ 4 个音符的新时长都是 480 ⇒ **应当只有 1 条命令**
	notes := []selNote{
		{UUID: "{a}", Pos: 0}, {UUID: "{b}", Pos: 480}, {UUID: "{c}", Pos: 960},
		{UUID: "{d}", Pos: 1440}, {UUID: "{e}", Pos: 1920},
	}
	byDur, degenerate := joinPlan(notes)
	if degenerate != 0 {
		t.Errorf("不该有退化项，实得 %d", degenerate)
	}
	if len(byDur) != 1 {
		t.Fatalf("等距音符应当只算出 1 种新时长，实得 %d 种：%v", len(byDur), byDur)
	}
	if got := byDur[480]; len(got) != 4 {
		t.Errorf("480 那组应当有 4 个音符（最后那个不动），实得 %d 个", len(got))
	}
	// 时长各不相同 ⇒ 分组数 = 音符数 − 1
	notes2 := []selNote{
		{UUID: "{a}", Pos: 0}, {UUID: "{b}", Pos: 100},
		{UUID: "{c}", Pos: 300}, {UUID: "{d}", Pos: 700},
	}
	byDur2, _ := joinPlan(notes2)
	if len(byDur2) != 3 {
		t.Errorf("三个不同间隔应当算出 3 种时长，实得 %d：%v", len(byDur2), byDur2)
	}
	if _, ok := byDur2[100]; !ok {
		t.Errorf("应当有 100 那组：%v", byDur2)
	}
	// 同起点（时长为 0）要**跳过并计数**，不能塞进命令里（ACE 会拒 Must be positive）
	notes3 := []selNote{
		{UUID: "{a}", Pos: 0}, {UUID: "{b}", Pos: 0}, {UUID: "{c}", Pos: 480},
	}
	byDur3, deg3 := joinPlan(notes3)
	if deg3 != 1 {
		t.Errorf("同起点应当记 1 个退化项，实得 %d", deg3)
	}
	if _, bad := byDur3[0]; bad {
		t.Error("时长为 0 的组不该出现在计划里")
	}
	// 全退化 ⇒ 空计划（调用方会给出可读的错）
	if byDur4, deg4 := joinPlan([]selNote{{UUID: "{a}", Pos: 5}, {UUID: "{b}", Pos: 5}}); len(byDur4) != 0 || deg4 != 1 {
		t.Errorf("全同起点应当是空计划 + 1 退化，实得 %v / %d", byDur4, deg4)
	}
}

// ---------- 播放头命中 ----------

func TestPlayheadIndex(t *testing.T) {
	notes := []selNote{
		{UUID: "{a}", Pos: 0, End: 480},
		{UUID: "{b}", Pos: 480, End: 960},
		{UUID: "{c}", Pos: 1200, End: 1680}, // 960..1200 是间隙
	}
	for _, c := range []struct {
		local int64
		want  int
		why   string
	}{
		{0, 0, "压在起点算本音符（2026-10-24 口径）"},
		{1, 0, "音符 a 之内"},
		{479, 0, "音符 a 末梢"},
		{480, 1, "压在 a 的终点、且后面有 b ⇒ 取后一个"},
		{481, 1, "音符 b 之内"},
		{960, 2, "压在 b 的终点、后面还有 c（有空隙）⇒ 取后一个 c"},
		{1000, -1, "落在空隙里 ⇒ 没有目标（2026-10-24）"},
		{1300, 2, "音符 c 之内"},
		{1680, 2, "压在最后一个音符的终点、后面没有音符 ⇒ 算当前音符"},
	} {
		if got := playheadIndex(notes, c.local); got != c.want {
			t.Errorf("playheadIndex(local=%d) = %d，期望 %d（%s）", c.local, got, c.want, c.why)
		}
	}
}

// ⛔ 选音符**分块**时的 mode 序列：第一块 replace、后续 modify。
//
// 真机踩过：`--mode replace` 是**替换**语义，分块发的时候每一块把上一块顶掉，
// 于是 329 个音符的片段选"之后所有"只选上了最后 27 个 —— 而且**不报错**。
func TestSelectModesFor(t *testing.T) {
	if got := selectModesFor(1); !eqStrs(got, []string{"replace"}) {
		t.Errorf("一块时应当是 [replace]，实得 %v", got)
	}
	if got := selectModesFor(4); !eqStrs(got, []string{"replace", "modify", "modify", "modify"}) {
		t.Errorf("四块时应当是 [replace modify modify modify]，实得 %v（用 replace 会把前面的顶掉）", got)
	}
	if got := selectModesFor(0); len(got) != 0 {
		t.Errorf("零块时应当是空的，实得 %v", got)
	}
	// 分块函数本身：100 个一块、最后一块是余数
	for _, c := range []struct{ n, wantChunks, wantLast int }{
		{0, 0, 0}, {1, 1, 1}, {100, 1, 100}, {101, 2, 1}, {329, 4, 29}, {200, 2, 100},
	} {
		chunks := chunkUUIDs(make([]string, c.n))
		if len(chunks) != c.wantChunks {
			t.Errorf("%d 个 uuid 应当切 %d 块，实得 %d", c.n, c.wantChunks, len(chunks))
			continue
		}
		if c.wantChunks > 0 && len(chunks[len(chunks)-1]) != c.wantLast {
			t.Errorf("%d 个 uuid 的最后一块应当是 %d 个，实得 %d", c.n, c.wantLast, len(chunks[len(chunks)-1]))
		}
	}
	// 每个 uuid 都要**恰好出现一次**（切块不能丢也不能重）
	ids := make([]string, 329)
	for i := range ids {
		ids[i] = "{" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "}"
	}
	seen := map[string]int{}
	for _, ch := range chunkUUIDs(ids) {
		for _, id := range ch {
			seen[id]++
		}
	}
	if len(seen) != len(ids) {
		t.Errorf("切块后只覆盖了 %d/%d 个 uuid", len(seen), len(ids))
	}
}

// ---------- 移位语义（在纯数据上验，不碰 CLI） ----------

// 「添加 + / - 并后移」：`[0,1,2,3] → [+,0,1,2]`。
func TestShiftLyricsRight(t *testing.T) {
	for _, mark := range []string{"+", "-"} {
		ly := []string{"0", "1", "2", "3"}
		got := shiftLyricsRight(ly, 0, mark)
		if !eqStrs(got, []string{mark, "0", "1", "2"}) {
			t.Errorf("mark=%s：shiftRight(i=0) = %v，期望 [%s 0 1 2]", mark, got, mark)
		}
		// 在中间插：i=2 ⇒ 后面那个（"3"）被推掉，因为已经到末尾
		got2 := shiftLyricsRight([]string{"0", "1", "2", "3"}, 2, mark)
		if !eqStrs(got2, []string{"0", "1", mark, "2"}) {
			t.Errorf("mark=%s：shiftRight(i=2) = %v，期望 [0 1 %s 2]", mark, got2, mark)
		}
	}
}

// 「删除歌词并前移」：丢掉本音符的，后面整体左移；**最后一个留在原地**（照原脚本）。
func TestRemoveLyricsShiftLeft(t *testing.T) {
	// 删除 i=1：["0","1","2","3"] → ["0","2","3","3"]
	got := removeLyricsShiftLeft([]string{"0", "1", "2", "3"}, 1)
	if !eqStrs(got, []string{"0", "2", "3", "3"}) {
		t.Errorf("删除 i=1 = %v，期望 [0 2 3 3]（最后一个留在原地）", got)
	}
	// 删除 i=0：["0","1","2","3"] → ["1","2","3","3"]
	got0 := removeLyricsShiftLeft([]string{"0", "1", "2", "3"}, 0)
	if !eqStrs(got0, []string{"1", "2", "3", "3"}) {
		t.Errorf("删除 i=0 = %v，期望 [1 2 3 3]", got0)
	}
	// 合并 i=1：本音符 = 自己 + 下一个 ⇒ ["0","12","3","3"]
	gotM := mergeLyricsShiftLeft([]string{"0", "1", "2", "3"}, 1)
	if !eqStrs(gotM, []string{"0", "12", "3", "3"}) {
		t.Errorf("合并 i=1 = %v，期望 [0 12 3 3]", gotM)
	}
}

// ---------- 工具在册 ----------

func TestNoteOpsToolsRegistered(t *testing.T) {
	for _, c := range []struct{ id, kind, cat string }{
		{"note.join", "write", "音符"},
		{"select.playhead", "write", "音符"},
		{"lyric.copy", "read", "歌词"},
		{"lyric.paste", "write", "歌词"},
		{"lyric.shift", "write", "歌词"},
		{"lyric.remove", "write", "歌词"},
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
			t.Errorf("%s 的分类应当是 %q（细分类口径），实得 %q", c.id, c.cat, tl.Category)
		}
		if tl.Source == "" {
			t.Errorf("%s 没写移植来源", c.id)
		}
	}
	// 空表单下不许 panic / 空错
	rd := stubRead(t, map[string]string{})
	for _, id := range []string{"note.join", "select.playhead", "lyric.paste", "lyric.shift", "lyric.remove"} {
		tl := Get(id)
		if _, err := tl.Build(context.Background(), rd, map[string]string{}); err == nil {
			t.Errorf("%s 在空表单下应当报错（读不到片段/选区）", id)
		} else if strings.TrimSpace(err.Error()) == "" {
			t.Errorf("%s 报了个空错", id)
		}
	}
	// 参数写错要明确拒绝
	for _, c := range []struct{ id, val, want string }{
		{"select.playhead", "nope", "side"},
		{"lyric.shift", "x", "标记"},
		{"lyric.remove", "x", "mode"},
	} {
		vals := map[string]string{}
		switch c.id {
		case "select.playhead":
			vals["side"] = c.val
		case "lyric.shift":
			vals["mark"] = c.val
		case "lyric.remove":
			vals["mode"] = c.val
		}
		if _, err := Get(c.id).Build(context.Background(), rd, vals); err == nil {
			t.Errorf("%s 参数 %q 应当报错", c.id, c.val)
		} else if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s 的报错要提到 %q，实得：%v", c.id, c.want, err)
		}
	}
	// 粘贴：文本为空要给出可执行的下一步（而不是静默不动）
	if _, err := Get("lyric.paste").Build(context.Background(), rd, map[string]string{}); err == nil {
		t.Error("粘贴空文本应当报错")
	} else if !strings.Contains(err.Error(), "text") {
		t.Errorf("粘贴的报错要提到 `text`，实得：%v", err)
	}
}
