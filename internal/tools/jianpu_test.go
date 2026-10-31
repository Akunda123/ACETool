package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"acetool/internal/acecli"
	"acetool/internal/jianpu"
)

// 工具形态：两个方向都要注册、Kind 要对、参数要齐、归到「移植脚本」栏。
func TestJianpuToolsRegistered(t *testing.T) {
	to := Get("jianpu.toNotes")
	if to == nil {
		t.Fatal("没有注册 jianpu.toNotes")
	}
	if to.Kind != KindWrite || to.Plan == nil || to.Apply == nil {
		t.Error("jianpu.toNotes 应当是 write + Plan/Apply 成对")
	}
	if to.Build != nil || to.Inspect != nil {
		t.Error("Plan+Apply 不该同时有 Build/Inspect")
	}
	from := Get("jianpu.fromClip")
	if from == nil {
		t.Fatal("没有注册 jianpu.fromClip")
	}
	if from.Kind != KindRead || from.Inspect == nil {
		t.Error("jianpu.fromClip 应当是只读 + Inspect")
	}
	if from.Plan != nil || from.Apply != nil {
		t.Error("只读工具不该有 Plan/Apply")
	}
	for _, tool := range []*Tool{to, from} {
		if groupOf(tool.Category) != "移植脚本" {
			t.Errorf("%s 应当归到「移植脚本」栏，实得 %q", tool.ID, groupOf(tool.Category))
		}
	}
	// 贴旋律要用多行框
	var hasArea bool
	for _, p := range to.Params {
		if p.Name == "text" && p.Type == ParamTextarea && p.Rows >= 6 {
			hasArea = true
		}
	}
	if !hasArea {
		t.Error("简谱文本应当是 textarea（贴整段旋律用）")
	}
}

// 谱 → 音符块：你那套记法 + clipStart 起点 ⇒ 位置应当是从片段头顺排的 0/480/960。
// 而且**干跑一个字节都不写**。
func TestJianpuToNotesPlanIsReadOnly(t *testing.T) {
	var called []string
	fake := newScopeFake(t, &called, scopeFakeOpts{})

	got, err := Get("jianpu.toNotes").Plan(context.Background(), fake, map[string]string{
		"notation": "terse", "text": "1 2 3", "key": "0",
		"defaultLyric": "la", "target": "current", "startAt": "clipStart", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep, ok := got.(*jianpuToNotesReport)
	if !ok {
		t.Fatalf("应当回 *jianpuToNotesReport，实得 %T", got)
	}
	if !rep.DryRun {
		t.Error("干跑要标 DryRun")
	}
	if rep.Parsed != 3 || rep.Write != 3 {
		t.Fatalf("「1 2 3」应当解析并写出 3 个音，实得 parsed=%d write=%d", rep.Parsed, rep.Write)
	}
	// C 大调 1 2 3 = 60 62 64；clipStart ⇒ 片段内 tick 0 / 480 / 960
	want := []jianpuNoteJSON{
		{Pos: 0, Dur: 480, Pitch: 60, Lyric: "la"},
		{Pos: 480, Dur: 480, Pitch: 62, Lyric: "la"},
		{Pos: 960, Dur: 480, Pitch: 64, Lyric: "la"},
	}
	if len(rep.Notes) != len(want) {
		t.Fatalf("音符数不对：%d", len(rep.Notes))
	}
	for i := range want {
		if rep.Notes[i] != want[i] {
			t.Errorf("第 %d 个音符 = %+v，期望 %+v", i, rep.Notes[i], want[i])
		}
	}
	for _, c := range called {
		if strings.HasPrefix(c, "note add") || strings.HasPrefix(c, "clip create") {
			t.Errorf("干跑里出现了写命令：%s", c)
		}
	}
}

// ⭐⭐ 用户 2026-10-05 的口径修正：「**连音是 ACE 的 `-` 音符**」。
// ACE 官方（`help note-exclusivity`）：一个音符组 = 头音符（带歌词）+ 若干 **tenuto 音符**，
// 而 **tenuto 就写成歌词字段里的字面 `-`** ⇒ 简谱的 `1 - -` 到 ACE 是**三个音符**，
// **不是**一个三拍的长音。这条测试把它钉死。
func TestJianpuToNotesExpandsSustainsIntoTenutoNotes(t *testing.T) {
	var called []string
	fake := newScopeFake(t, &called, scopeFakeOpts{})
	got, err := Get("jianpu.toNotes").Plan(context.Background(), fake, map[string]string{
		"notation": "terse", "text": "1 - -", "key": "0",
		"defaultLyric": "la", "target": "current", "startAt": "clipStart", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	rep := got.(*jianpuToNotesReport)
	if rep.Parsed != 3 || rep.Write != 3 {
		t.Fatalf("`1 - -` 应当解析 3 个元素、写 **3 个音符**（不是 1 个长音），实得 parsed=%d write=%d",
			rep.Parsed, rep.Write)
	}
	if rep.Sustains != 2 {
		t.Errorf("应当认出 2 个 `-` 延音，实得 %d", rep.Sustains)
	}
	want := []jianpuNoteJSON{
		{Pos: 0, Dur: 480, Pitch: 60, Lyric: "la"},
		{Pos: 480, Dur: 480, Pitch: 60, Lyric: "-"}, // tenuto：音高沿用前一个音，歌词写 `-`
		{Pos: 960, Dur: 480, Pitch: 60, Lyric: "-"},
	}
	for i := range want {
		if rep.Notes[i] != want[i] {
			t.Errorf("第 %d 个音符 = %+v，期望 %+v", i, rep.Notes[i], want[i])
		}
	}
	// 音高/时值不能被延音算错
	if rep.MinPitch != 60 || rep.MaxPitch != 60 || rep.TotalDur != 1440 {
		t.Errorf("音域/总长不对：%d..%d / %d", rep.MinPitch, rep.MaxPitch, rep.TotalDur)
	}
	if !strings.Contains(rep.Note, "延长") && !strings.Contains(rep.Note, "延音") {
		t.Errorf("收尾要说明这些是延音音符，实得 %q", rep.Note)
	}
}

// 休止符要**跳过但占位**（ACE 的 `note add` 没有休止符），并如实计数。
func TestJianpuToNotesSkipsRestsButKeepsPositions(t *testing.T) {
	var called []string
	fake := newScopeFake(t, &called, scopeFakeOpts{})
	got, err := Get("jianpu.toNotes").Plan(context.Background(), fake, map[string]string{
		"notation": "terse", "text": "1 0 2", "key": "0",
		"defaultLyric": "la", "target": "current", "startAt": "clipStart", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	rep := got.(*jianpuToNotesReport)
	if rep.Parsed != 3 || rep.Write != 2 || rep.Skipped != 1 {
		t.Fatalf("应当是 3 个块、写 2 个、跳 1 个休止，实得 %d/%d/%d", rep.Parsed, rep.Write, rep.Skipped)
	}
	// 第二个音应当落在 pos 960（中间那个休止**占了位置**）
	if rep.Notes[1].Pos != 960 {
		t.Errorf("休止之后的音应当在 pos 960（休止占位），实得 %d", rep.Notes[1].Pos)
	}
	if !strings.Contains(rep.Note, "休止") {
		t.Errorf("收尾要说明跳过了休止符，实得 %q", rep.Note)
	}
}

// 反向：ACE 音符 → 你那套记法。假 CLI 给的是 pitch 62 / 64（都在 C 大调内）。
func TestJianpuFromClipProducesTerseNotation(t *testing.T) {
	var called []string
	fake := newScopeFake(t, &called, scopeFakeOpts{})
	got, err := Get("jianpu.fromClip").Inspect(context.Background(), fake, map[string]string{
		"scope": "clip", "format": "terse", "key": "auto", "meter": "4/4",
	})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	rep, ok := got.(*jianpuFromReport)
	if !ok {
		t.Fatalf("应当回 *jianpuFromReport，实得 %T", got)
	}
	if rep.Notes != 2 {
		t.Fatalf("假 CLI 的片段有 2 个音符，实得 %d", rep.Notes)
	}
	// 62=D、64=E 都在 C 大调内 ⇒ 自动检测应当是 C（档位 0）
	if rep.Key != 0 {
		t.Errorf("62/64 都在 C 大调内 ⇒ 应当判成 C(0)，实得 %d", rep.Key)
	}
	if !strings.HasPrefix(rep.Output, "2") {
		t.Errorf("产出应当以度数 2（D）开头，实得 %q", rep.Output)
	}
	if !strings.Contains(rep.Output, "|") {
		t.Errorf("产出里应当有小节线，实得 %q", rep.Output)
	}
}

// 反向出 MusicXML：要是良构 XML、且能被自己的解析器读回来。
func TestJianpuFromClipProducesMusicXML(t *testing.T) {
	var called []string
	fake := newScopeFake(t, &called, scopeFakeOpts{})
	got, err := Get("jianpu.fromClip").Inspect(context.Background(), fake, map[string]string{
		"scope": "clip", "format": "musicxml", "key": "0", "meter": "4/4",
		"title": "测试", "composer": "某人",
	})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	rep := got.(*jianpuFromReport)
	for _, want := range []string{"<score-partwise", "<divisions>480</divisions>", "<work-title>测试</work-title>"} {
		if !strings.Contains(rep.Output, want) {
			t.Errorf("MusicXML 产物里缺 %q\n%s", want, rep.Output)
		}
	}
	// 自读一遍（良构 + 元素齐）
	back, diags := jianpu.ParseMusicXML(rep.Output)
	if len(back) != 2 {
		t.Errorf("自己的 MusicXML 应当读回 2 个音符，实得 %d（诊断 %v）", len(back), diags)
	}
	// 调号：1=#C 那种全升号记法要强制走升号侧
	got2, _ := Get("jianpu.fromClip").Inspect(context.Background(), fake, map[string]string{
		"scope": "clip", "format": "musicxml", "key": "1", "meter": "4/4",
	})
	rep2 := got2.(*jianpuFromReport)
	if !strings.Contains(rep2.Output, "<fifths>7</fifths>") {
		t.Errorf("调式档位 1（1=#C）应当写 7 个升号，产物里没有：\n%s", rep2.Output)
	}
}

// ⭐⭐ 回归：**编辑器发的元素 JSON 里，休止必须占位置**。
//
// 这条是被真机自检抓出来的（`devtools/e2e.js` ④「跳过 0 个」那一项）：
// `elementsToJpElems` 起初只认 `kind:"note"`，而编辑器方言写的是 `kind:"rest"`
// ⇒ 休止那一格**既不产出音符、也不推进游标**，于是**后面的音符整体前移**，
// 而且一条诊断都没有 —— 属于"静默算错"，比报错难查得多。
//
// 两种方言都要对：编辑器方言 `kind:"rest"` 与上游语义 `kind:"note" + rest:true`。
func TestJianpuToNotesRestOccupiesTimeInElementsInput(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"编辑器方言 kind=rest", `[{"kind":"note","pitch":1,"duration":4},{"kind":"rest","duration":4},{"kind":"note","pitch":2,"duration":4}]`},
		{"上游语义 kind=note+rest", `[{"kind":"note","pitch":1,"duration":4},{"kind":"note","rest":true,"duration":4},{"kind":"note","pitch":2,"duration":4}]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var called []string
			fake := newScopeFake(t, &called, scopeFakeOpts{})
			got, err := Get("jianpu.toNotes").Plan(context.Background(), fake, map[string]string{
				"elements": c.json, "key": "0", "defaultLyric": "la",
				"target": "current", "startAt": "clipStart", "dryRun": "1",
			})
			if err != nil {
				t.Fatalf("不该失败：%v", err)
			}
			rep := got.(*jianpuToNotesReport)
			if rep.Parsed != 3 {
				t.Fatalf("3 个元素应当都解析出来（休止也要算一个），实得 %d", rep.Parsed)
			}
			if rep.Skipped != 1 {
				t.Errorf("休止应当被计为跳过 1 个，实得 %d", rep.Skipped)
			}
			if len(rep.Notes) != 2 {
				t.Fatalf("应当只写出 2 个音符（休止不写），实得 %d", len(rep.Notes))
			}
			if rep.Notes[0].Pos != 0 || rep.Notes[0].Pitch != 60 {
				t.Errorf("第一个音应当在 pos 0 / pitch 60，实得 %+v", rep.Notes[0])
			}
			// ⭐ 关键：休止占了 480 tick ⇒ 第二个音必须在 960，而不是 480
			if rep.Notes[1].Pos != 960 {
				t.Errorf("休止占了位置 ⇒ 第二个音应当在 pos 960，实得 %d（＝休止被吞了）", rep.Notes[1].Pos)
			}
		})
	}
}

// 不认识的 `kind` **要报诊断**，不能静默吞（静默吞表现为"音符莫名少了"）。
func TestJianpuToNotesUnknownKindIsReportedNotSwallowed(t *testing.T) {
	elems, diags, err := toNotesElements(map[string]string{
		"elements": `[{"kind":"note","pitch":1,"duration":4},{"kind":"whistle","duration":4},{"kind":"note","pitch":2,"duration":4}]`,
		"key":      "0",
	})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if len(elems) != 2 {
		t.Fatalf("不认识的 kind 应当被跳过（剩 2 个），实得 %d", len(elems))
	}
	if len(diags) == 0 || !strings.Contains(strings.Join(diags, " "), "whistle") {
		t.Errorf("要被跳过就得**点名报出来**，实得诊断 %v", diags)
	}
}

// 小节线**不占时值**：把它当休止会白白吃掉一拍。
func TestJianpuElementsBarlineDoesNotConsumeTime(t *testing.T) {
	elems, _, err := toNotesElements(map[string]string{
		"elements": `[{"kind":"note","pitch":1,"duration":4},{"kind":"barline","type":"normal"},{"kind":"note","pitch":2,"duration":4}]`,
		"key":      "0",
	})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if len(elems) != 2 {
		t.Fatalf("小节线不该变成元素（剩 2 个），实得 %d", len(elems))
	}
	// 音符块那一侧同样：小节线不该插进一个"休止块"
	blocks, _ := elementsToBlocks([]jianpu.Element{
		{Kind: jianpu.KindNote, Pitch: 1, Duration: 4},
		{Kind: jianpu.KindBarline, Type: jianpu.BarNormal},
		{Kind: jianpu.KindNote, Pitch: 2, Duration: 4},
	}, 0, jianpu.Meter{Numerator: 4, Denominator: 4})
	if len(blocks) != 2 {
		t.Fatalf("小节线不该变成音符块（剩 2 个），实得 %d", len(blocks))
	}
	if blocks[1].Beat != 2 {
		t.Errorf("小节线不占时值 ⇒ 第二个音应当在第 2 拍，实得 %.2f 拍", blocks[1].Beat)
	}
}

// 新建片段的位置：撞上已有片段要顺延到空位（ACE 会硬拒 `CLIP_OVERLAP`，真机实测过）。
func TestFreeClipStartSkipsOccupiedRanges(t *testing.T) {
	clips := []clipBrief{
		{Begin: 0, End: 2880},
		{Begin: 4800, End: 7680},
	}
	cases := []struct {
		want, dur, expect int64
		why               string
	}{
		{0, 1920, 2880, "起点撞上第一个片段 ⇒ 顺延到它的右端"},
		{1000, 1920, 2880, "落在第一个片段内部 ⇒ 同样顺延"},
		{2880, 2400, 7680, "空档里放不下（会压到下一个片段）⇒ 继续顺延过它"},
		{2880, 1920, 2880, "恰好填满空档（半开区间不相交）⇒ 不动"},
		{2880, 960, 2880, "放得下就不动"},
		{9000, 480, 9000, "尾巴后面本来就空着 ⇒ 不动"},
	}
	for _, c := range cases {
		if got := freeClipStart(clips, c.want, c.dur); got != c.expect {
			t.Errorf("freeClipStart(%d, %d) = %d，期望 %d（%s）", c.want, c.dur, got, c.expect, c.why)
		}
	}
	// 空轨道：原样返回
	if got := freeClipStart(nil, 1234, 480); got != 1234 {
		t.Errorf("空轨道应当原样返回，实得 %d", got)
	}
}

// ⭐⭐ 回归：**新建片段时，音符的起点必须是 clip-local 0**。
//
// 真机踩到的（2026-10-05）：两种目标共用了 `startGlobal - sc.Begin` 当 clip-local 起点，
// 于是把新片段建在 3840 时，音符被写到 clip-local 3840 —— 超出片段自身长度（2880），
// ACE **退出码 0**、片段还照歌词起了名，可里面**一个音符都没有**。
// 干跑与 `OK()` 都看不出来，只有核对 `noteCount` 才抓得住（所以 Apply 里也加了这道核对）。
func TestJianpuNewClipUsesClipLocalZero(t *testing.T) {
	// 共用假 CLI 没有 `transport state`（别的用例不需要播放头）⇒ 这里包一层补上，
	// 其余命令仍走共用实现（它同时**拦住写命令**，干跑才有效）。
	withPlayhead := func(called *[]string) ReadFunc {
		inner := newScopeFake(t, called, scopeFakeOpts{})
		return func(ctx context.Context, args ...string) (*acecli.Result, error) {
			if len(args) >= 2 && args[0] == "transport" {
				*called = append(*called, strings.Join(args, " "))
				return okJSON(`{"positionTick":1920,"status":"stopped"}`), nil
			}
			return withBarMath(inner, called)(ctx, args...)
		}
	}
	var called []string
	fake := withPlayhead(&called)
	// 播放头**不在**片段头（假 CLI 的播放头 1920；片段从 0 起，不到 1920）
	got, err := Get("jianpu.toNotes").Plan(context.Background(), fake, map[string]string{
		"notation": "terse", "text": "1 2", "key": "0", "defaultLyric": "la",
		"target": "newClip", "startAt": "playhead", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	rep := got.(*jianpuToNotesReport)
	if rep.Target != "newClip" {
		t.Fatalf("target 应当是 newClip，实得 %q", rep.Target)
	}
	if rep.Notes[0].Pos != 0 {
		t.Errorf("新建片段里第一个音必须在 clip-local 0（片段自己从 StartTick 起），实得 %d", rep.Notes[0].Pos)
	}
	if rep.Notes[1].Pos != 480 {
		t.Errorf("第二个音应当在 clip-local 480，实得 %d", rep.Notes[1].Pos)
	}
	// 而"加进当前片段"那条路仍然按片段起点算
	var called2 []string
	fake2 := withPlayhead(&called2)
	got2, err := Get("jianpu.toNotes").Plan(context.Background(), fake2, map[string]string{
		"notation": "terse", "text": "1 2", "key": "0", "defaultLyric": "la",
		"target": "current", "startAt": "playhead", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	rep2 := got2.(*jianpuToNotesReport)
	if rep2.Notes[0].Pos != 1920 {
		t.Errorf("加进当前片段时，播放头 1920 处应当是 clip-local 1920，实得 %d", rep2.Notes[0].Pos)
	}
}

// withBarMath 给一个假 CLI 补上**小节换算**那两条命令（`convert tick-to-measure` /
// `measure-to-tick`），口径与真机一致：4/4 ⇒ 一小节 1920 tick，**`barPos` 是 0 起的**。
//
// 真机核对过（2026-10-07）：`convert tick-to-measure --tick 15420 --consider-beat-mode false`
// ⇒ `{"barPos":8,"tickOffset":60}`（tick 15420 在**第 9 小节**里第 60 个 tick），
// 而 `convert measure-to-tick --bar 9 --tick-offset 0 --consider-beat-mode false` ⇒ `{"tick":15360}`。
func withBarMath(inner ReadFunc, called *[]string) ReadFunc {
	return func(ctx context.Context, args ...string) (*acecli.Result, error) {
		if len(args) >= 2 && args[0] == "convert" {
			*called = append(*called, strings.Join(args, " "))
			var tick, bar int64
			for i := 0; i+1 < len(args); i++ {
				switch args[i] {
				case "--tick":
					tick, _ = strconv.ParseInt(args[i+1], 10, 64)
				case "--bar":
					bar, _ = strconv.ParseInt(args[i+1], 10, 64)
				}
			}
			switch args[1] {
			case "tick-to-measure":
				return okJSON(fmt.Sprintf(`{"barPos":%d,"tickOffset":%d}`, tick/1920, tick%1920)), nil
			case "measure-to-tick":
				return okJSON(fmt.Sprintf(`{"tick":%d}`, (bar-1)*1920)), nil
			}
		}
		return inner(ctx, args...)
	}
}

// ⭐ 用户 2026-10-07：「生成音符的位置是当前 clip **从播放头所在小节**开始生成音符」。
// 播放头在第 2 小节中间（2400）⇒ 默认那一档要**往下取整到小节线 1920**；
// 旧的精确档（`playheadExact`）仍然原样用 2400。
func TestJianpuStartAlignsToPlayheadBar(t *testing.T) {
	playheadAt := func(called *[]string, tick int64) ReadFunc {
		inner := newScopeFake(t, called, scopeFakeOpts{})
		inner = withBarMath(inner, called)
		return func(ctx context.Context, args ...string) (*acecli.Result, error) {
			if len(args) >= 2 && args[0] == "transport" {
				*called = append(*called, strings.Join(args, " "))
				return okJSON(fmt.Sprintf(`{"positionTick":%d,"status":"stopped"}`, tick)), nil
			}
			return inner(ctx, args...)
		}
	}
	// ① 默认（playhead）= 对齐到播放头所在小节的起点
	var called []string
	got, err := Get("jianpu.toNotes").Plan(context.Background(), playheadAt(&called, 2400), map[string]string{
		"notation": "terse", "text": "1 2", "key": "0", "defaultLyric": "la",
		"target": "current", "startAt": "playhead", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	rep := got.(*jianpuToNotesReport)
	if rep.PlayheadTick != 2400 || rep.StartBar != 2 || rep.StartBarTick != 1920 || rep.StartTick != 1920 {
		t.Errorf("起点该对齐到第 2 小节线 1920：%+v", rep)
	}
	if rep.Notes[0].Pos != 1920 || rep.Notes[1].Pos != 2400 {
		t.Errorf("音符该从片段内 1920 起顺排，实得 %d / %d", rep.Notes[0].Pos, rep.Notes[1].Pos)
	}
	rep.DryRun = true
	rep.Note = toNotesNote(rep, true)
	if !strings.Contains(rep.Note, "第 2 小节") || !strings.Contains(rep.Note, "对齐到小节线 1920") {
		t.Errorf("报告要说清起点是怎么定的，实得：%s", rep.Note)
	}
	// ② playheadExact = 播放头的精确位置（不吸附）
	var called2 []string
	got2, err := Get("jianpu.toNotes").Plan(context.Background(), playheadAt(&called2, 2400), map[string]string{
		"notation": "terse", "text": "1 2", "key": "0", "defaultLyric": "la",
		"target": "current", "startAt": "playheadExact", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	rep2 := got2.(*jianpuToNotesReport)
	if rep2.StartTick != 2400 || rep2.Notes[0].Pos != 2400 {
		t.Errorf("精确档该原样用 2400，实得 start=%d pos0=%d", rep2.StartTick, rep2.Notes[0].Pos)
	}
}

// 元素文档 → 元素文档：ACE 的 tenuto（歌词 `-`）往返一趟必须还是 tenuto。
// 而且歌词要**跟着元素走**（编辑器里音高/时值/歌词是同一条元素的三个属性）。
func TestJianpuElementsRoundTripKeepsTenutoAndLyrics(t *testing.T) {
	notes := []xcopyNote{
		{Pos: 0, Dur: 480, Pitch: 62, Lyric: "月", NoteUUID: "u1"},
		{Pos: 480, Dur: 480, Pitch: 62, Lyric: "-", NoteUUID: "u2"},
		{Pos: 960, Dur: 960, Pitch: 64, Lyric: "落", NoteUUID: "u3"},
	}
	els, diags := notesToElements(notes, 0)
	if len(els) != 3 {
		t.Fatalf("3 个 ACE 音符应当折成 3 个元素，实得 %d（诊断 %v）", len(els), diags)
	}
	if els[1].Kind != jianpu.KindSustain {
		t.Errorf("歌词为 `-` 的音符要变成**连音元素**，实得 kind=%q", els[1].Kind)
	}
	if els[0].Lyric != "月" || els[2].Lyric != "落" {
		t.Errorf("歌词要挂在元素上，实得 %q / %q", els[0].Lyric, els[2].Lyric)
	}
	// 再折回音符块：连音并入前一个音的时值 ⇒ 2 个块，第一个 2 拍
	blocks, _ := elementsToBlocks(els, 0, jianpu.Meter{Numerator: 4, Denominator: 4})
	if len(blocks) != 2 {
		t.Fatalf("连音要折回前一个音 ⇒ 2 个块，实得 %d", len(blocks))
	}
	if blocks[0].Ticks != 960 {
		t.Errorf("第一个块应当是 960 tick（480 + 连音 480），实得 %d", blocks[0].Ticks)
	}
	if blocks[0].Lyric != "月" {
		t.Errorf("折起来以后歌词要留在头音符上，实得 %q", blocks[0].Lyric)
	}
}

// ⭐ 用户 2026-10-07 的质问：「空拍不是休止吗」——
// ACE 里**没有休止符对象**：一段没有音符的时间**就是**休止。所以读回来必须**按间隙补出休止**，
// 否则编辑器里的休止会凭空消失、谱面上后面的音看着往前挤。
func TestJianpuNotesToElementsRestoresRestsFromGaps(t *testing.T) {
	// 音 1（1 拍）· 空 1 拍 · 音 2（1 拍）· 空 1 拍 · 音 3（1 拍）
	notes := []xcopyNote{
		{Pos: 0, Dur: 480, Pitch: 62, Lyric: "a", NoteUUID: "u1"},
		{Pos: 960, Dur: 480, Pitch: 64, Lyric: "b", NoteUUID: "u2"},
		{Pos: 1920, Dur: 480, Pitch: 65, Lyric: "c", NoteUUID: "u3"},
	}
	els, diags := notesToElements(notes, 0)
	// 期望：音 a · 休止(480) · 音 b · 休止(480) · 音 c  ⇒ 5 个元素
	if len(els) != 5 {
		t.Fatalf("两个空拍应当补成休止 ⇒ 5 个元素，实得 %d（%+v / 诊断 %v）", len(els), els, diags)
	}
	for i, want := range []string{jianpu.KindNote, jianpu.KindRest, jianpu.KindNote, jianpu.KindRest, jianpu.KindNote} {
		if els[i].Kind != want {
			t.Errorf("第 %d 个元素应当是 %s，实得 %s", i+1, want, els[i].Kind)
		}
	}
	if els[1].Duration != 4 || els[3].Duration != 4 { // 480 tick = 四分音符(4)
		t.Errorf("补出来的休止时值不对：%d / %d（期望都是 4 = 四分音符）", els[1].Duration, els[3].Duration)
	}
	// 累加时值必须还是 2400 tick（空拍**占位置**，后面的音不能挤到前面）
	var total int64
	for _, e := range els {
		total += jianpu.DurationTicks(e.Duration, e.Dots, 480)
	}
	if total != 2400 {
		t.Errorf("补了休止之后总长仍应当是 2400 tick，实得 %d", total)
	}
	if joined := strings.Join(diags, " | "); !strings.Contains(joined, "空拍") {
		t.Errorf("要如实报出「补了几段休止」，实得：%v", diags)
	}
	// 反过来：这些元素写回 ACE 时，休止**跳过但占位置** ⇒ 音符的时刻不变
	pd, _, err := analyzeToNotes(context.Background(), jianpuRestFake(t), map[string]string{
		"elements": elementsJSON(t, els), "key": "0", "target": "current", "startAt": "clipStart", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if len(pd.Notes) != 3 {
		t.Fatalf("休止不写音符 ⇒ 应当只写 3 个音，实得 %d", len(pd.Notes))
	}
	if pd.Notes[0].Pos != 0 || pd.Notes[1].Pos != 960 || pd.Notes[2].Pos != 1920 {
		t.Errorf("休止必须**占位置**：三个音应当在 0 / 960 / 1920，实得 %d / %d / %d",
			pd.Notes[0].Pos, pd.Notes[1].Pos, pd.Notes[2].Pos)
	}
	if pd.Skipped != 2 {
		t.Errorf("两个休止应当计进 skipped，实得 %d", pd.Skipped)
	}
}

// ⭐ `0 -`（休止 + 延音线）= "空两拍" ⇒ 只占位置，**不能**去续上一个音的音高
// （这是修掉的一个真 bug：以前 `lastPitch` 在休止处不清，`-` 会把上一个音又写回来）。
func TestJianpuSustainAfterRestStaysSilent(t *testing.T) {
	els := []jianpu.Element{
		{Kind: jianpu.KindNote, Pitch: 1, Duration: 4, Lyric: "la"},
		{Kind: jianpu.KindRest, Rest: true, Duration: 4},
		{Kind: jianpu.KindSustain, Duration: 4},
		{Kind: jianpu.KindNote, Pitch: 2, Duration: 4, Lyric: "la"},
	}
	pd, _, err := analyzeToNotes(context.Background(), jianpuRestFake(t), map[string]string{
		"elements": elementsJSON(t, els), "key": "0", "target": "current", "startAt": "clipStart", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	// 只有第一个音与最后一个音；休止与它后面的 `-` 都不写
	if len(pd.Notes) != 2 {
		t.Fatalf("休止 + 它后面的 `-` 都不该写音符 ⇒ 2 个音，实得 %d（%+v）", len(pd.Notes), pd.Notes)
	}
	if pd.SustainOrphan != 0 {
		t.Errorf("`0 -` 里的 `-` 是「休止拖长」，不该算孤儿，实得 %d", pd.SustainOrphan)
	}
	// 最后一个音落在 1920（= 480 音 + 480 休止 + 480 休止拖长 + 480 音）
	if pd.Notes[1].Pos != 1440 {
		t.Errorf("末尾那个音应当在 1440（休止与 `-` 都占位置），实得 %d", pd.Notes[1].Pos)
	}
	if pd.Notes[0].Pitch == pd.Notes[1].Pitch {
		t.Errorf("休止后面的 `-` 不该把上一个音续回来：%+v", pd.Notes)
	}
}

// elementsJSON 把元素序列编成面板发的那种 JSON（测试用）。
func elementsJSON(t *testing.T, els []jianpu.Element) string {
	t.Helper()
	b, err := json.Marshal(els)
	if err != nil {
		t.Fatalf("元素编不成 JSON：%v", err)
	}
	return string(b)
}

// jianpuRestFake：只够 analyzeToNotes 跑起来（读片段范围 + 播放头），写命令一律拦下。
func jianpuRestFake(t *testing.T) ReadFunc {
	t.Helper()
	inner := newScopeFake(t, &[]string{}, scopeFakeOpts{})
	return func(ctx context.Context, args ...string) (*acecli.Result, error) {
		cmd := strings.Join(args, " ")
		if strings.HasPrefix(cmd, "transport") {
			return okJSON(`{"positionTick":0,"status":"stopped"}`), nil
		}
		return inner(ctx, args...)
	}
}
