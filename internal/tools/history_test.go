package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"acetool/internal/acecli"
)

// fakeUndo 是一个**会动**的假撤销栈：`history undo` 真把游标往下挪一格。
// 这样"退到锚点就停""退到一半碰上别人的条目就停手"才测得出来（只回静态 JSON 测不了）。
type fakeUndo struct {
	cursor  int
	entries []UndoEntry // 新的在前
	undid   int
	redid   int
	calls   []string
	failAt  int // 第几条 undo 开始报错（0 = 不报错）
	// ---- 「按效果判噪音」探针要用的两样：假的 dirty 状态 + 哪些名字"撤了会动工程" ----
	dirty   bool
	content map[string]bool
}

// fakeContent 是"撤了它工程真的会变"的名字（默认按真机的观察：移动/编辑/调整 这类是内容，
// 选择/光标 这类不是）。测试可以自己换 `content`。
func (f *fakeUndo) isContent(name string) bool {
	if f.content != nil {
		return f.content[name]
	}
	switch name {
	case "调整音符大小", "编辑歌词", "移动音符", "移动轨道", "导入文件", "应用曲速和拍号":
		return true
	}
	return false // 选择音符 / 更改选择 / 移动光标 / 滚动视图 … ⇒ 没落在工程里
}

// entryAt 取游标下面那一条（= 下一个会被撤销的）。
func (f *fakeUndo) entryAt(index int) *UndoEntry {
	for i := range f.entries {
		if f.entries[i].Index == index {
			return &f.entries[i]
		}
	}
	return nil
}

func (f *fakeUndo) rd(_ context.Context, args ...string) (*acecli.Result, error) {
	cmd := strings.Join(args, " ")
	f.calls = append(f.calls, cmd)
	switch cmd {
	case "history list":
		var rows []string
		for _, e := range f.entries {
			applied := e.Index < f.cursor
			rows = append(rows, fmt.Sprintf(`{"index":%d,"name":%q,"actor":%q,"applied":%t}`,
				e.Index, e.Name, e.Actor, applied))
		}
		return okJSON(fmt.Sprintf(`{"count":%d,"index":%d,"entries":[%s]}`,
			len(f.entries), f.cursor, strings.Join(rows, ","))), nil
	case "history undo":
		if f.failAt > 0 && f.undid+1 >= f.failAt {
			return &acecli.Result{ExitCode: acecli.ExitFailed,
				Err: &acecli.Envelope{Code: "HANDLER_FAILED", Message: "undo 挂了"}}, nil
		}
		f.undid++
		if e := f.entryAt(f.cursor - 1); e != nil && f.isContent(e.Name) {
			f.dirty = true // 撤了一条**落在工程里**的 ⇒ 工程变脏（探针靠这个判）
		}
		f.cursor--
		return okJSON(fmt.Sprintf(`{"index":%d}`, f.cursor)), nil
	case "history redo":
		f.redid++
		if e := f.entryAt(f.cursor); e != nil && f.isContent(e.Name) {
			f.dirty = true
		}
		f.cursor++
		return okJSON(fmt.Sprintf(`{"index":%d}`, f.cursor)), nil
	case "project save":
		f.dirty = false
		return okJSON(`{"savedPath":"C:/tmp/x.acep"}`), nil
	case "project dirty":
		return okJSON(fmt.Sprintf(`{"dirty":%t,"projectName":"测试","projectPath":"C:/tmp/x.acep"}`, f.dirty)), nil
	}
	return nil, fmt.Errorf("假 CLI 没准备这条命令：%s", cmd)
}

// stack 造一个"三批"的栈：用户在 ACE 里动过手（actor 空）+ 我们两批（远程控制）。
//
//	index 0..3  : 别人的（anchor 之前）
//	index 4,5   : 我们第一批
//	index 6,7   : 我们第二批（这一批 2 条，锚点 = 6）
func sampleStack(cursor int) *fakeUndo {
	return &fakeUndo{
		cursor: cursor,
		// ⚠️ `Applied` 在这个字面量里要显式给 true：`fakeUndo.rd` 是按游标自己算 applied 的，
		// 但**直调 `planUndo`** 的用例（不走 rd）拿的就是这份字面量 ⇒ 得像真回包那样是 applied。
		entries: []UndoEntry{
			{Index: 7, Name: "调整音符大小", Actor: undoActorCLI, Applied: true},
			{Index: 6, Name: "调整音符大小", Actor: undoActorCLI, Applied: true},
			{Index: 5, Name: "编辑歌词", Actor: undoActorCLI, Applied: true},
			{Index: 4, Name: "编辑歌词", Actor: undoActorCLI, Applied: true},
			{Index: 3, Name: "选择音符", Actor: "", Applied: true},
			{Index: 2, Name: "移动轨道", Actor: "", Applied: true},
			{Index: 1, Name: "应用曲速和拍号", Actor: "", Applied: true},
			{Index: 0, Name: "导入文件", Actor: "", Applied: true},
		},
	}
}

func TestUndoBatchApplyUndoesOnlyOurBatch(t *testing.T) {
	f := sampleStack(8) // 游标 8 ⇒ 我们第二批是 index 6,7
	got, err := Get("history.undoBatch").Apply(context.Background(), f.rd, map[string]string{
		"anchor": "6", "count": "2",
	})
	if err != nil {
		t.Fatalf("Apply 不该报错：%v", err)
	}
	rep := got.(*undoBatchReport)
	if len(rep.Undone) != 2 || f.undid != 2 {
		t.Fatalf("应当退 2 条，实得 undone=%v，undo 调了 %d 次", rep.Undone, f.undid)
	}
	if f.cursor != 6 {
		t.Errorf("退到锚点就该停：游标应当 6，实得 %d", f.cursor)
	}
	if rep.CursorAfter != 6 {
		t.Errorf("回包里的 CursorAfter 应当是 6，实得 %d", rep.CursorAfter)
	}
	if !strings.Contains(rep.Note, "退干净") {
		t.Errorf("退到锚点时要明说退干净了，实得 %q", rep.Note)
	}
	// ⚠️ 绝不能退到 index 5（那是我们**上一批**的）—— 有一条 `count` 上限拦着，也要有锚点拦着
	if f.undid > 2 {
		t.Errorf("越过锚点退了 %d 条", f.undid)
	}
}

// 锚点跑到游标上面（用户自己先 Ctrl+Z 了）⇒ **一条都不退**，并把原因说清。
func TestUndoBatchBlocksWhenAnchorIsStale(t *testing.T) {
	f := sampleStack(5) // 游标 5，锚点 6 ⇒ 那一批已经被退掉了
	got, err := Get("history.undoBatch").Apply(context.Background(), f.rd, map[string]string{
		"anchor": "6", "count": "2",
	})
	if err != nil {
		t.Fatalf("不该报错（原因写在报告里）：%v", err)
	}
	rep := got.(*undoBatchReport)
	if f.undid != 0 || len(rep.Undone) != 0 {
		t.Fatalf("一条都不该退，实得 %d 条：%v", f.undid, rep.Undone)
	}
	if !strings.Contains(rep.Note, "已经不在那一批上了") {
		t.Errorf("要如实说是锚点过期了，实得 %q", rep.Note)
	}
}

// 顶上那条是你自己的**内容编辑**（用户在 ACE 里刚动过手）⇒ 探针撤一条看 dirty：
// 发现"真的落在工程里" ⇒ **history redo 放回去** + 停手（绝不留在撤掉的状态）。
func TestUndoBatchProbesForeignContentAndPutsItBack(t *testing.T) {
	f := sampleStack(8)
	// 把顶上换成用户自己的操作（actor 空）——模拟"我们这批之后，用户又在 ACE 里点了一下"
	f.entries = append([]UndoEntry{{Index: 8, Name: "移动音符", Actor: ""}}, f.entries...)
	f.cursor = 9
	got, _ := Get("history.undoBatch").Apply(context.Background(), f.rd, map[string]string{
		"anchor": "6", "count": "2", "checkEffect": "1", // 这条专门验"按效果判"
	})
	rep := got.(*undoBatchReport)
	if f.undid != 1 || f.redid != 1 {
		t.Fatalf("应当是「撤一条探一下 + redo 放回」（undid=1/redid=1），实得 undid=%d redid=%d", f.undid, f.redid)
	}
	if len(rep.Undone) != 0 {
		t.Errorf("放回去的那条不该记进 undone，实得 %v", rep.Undone)
	}
	if !strings.Contains(rep.Stopped, "移动音符") || !strings.Contains(rep.Stopped, "落在工程里") {
		t.Errorf("要说清「这条落在工程里、是你自己的」，实得 %q", rep.Stopped)
	}
	if !strings.Contains(rep.Stopped, "放回") {
		t.Errorf("要说明已经 redo 放回去了，实得 %q", rep.Stopped)
	}
	if len(rep.Probed) != 1 || !strings.Contains(rep.Probed[0], "redo") {
		t.Errorf("探针结论要写进报告，实得 %v", rep.Probed)
	}
}

// 退到一半碰上别人的**内容**条目：前面退掉的**不回滚**，并且如实说。
// ⚠️ 这里必须用一个**不在** `skipNames` 里的名字（用户 2026-10-15 起，「选择音符」这类会被穿过去）。
func TestUndoBatchStopsMidwayAndKeepsWhatItUndid(t *testing.T) {
	f := sampleStack(8) // 我们第二批 2 条在 6,7
	// index 6 换成别人的内容操作 ⇒ 退掉 7 之后就该停
	for i := range f.entries {
		if f.entries[i].Index == 6 {
			f.entries[i].Actor = ""
			f.entries[i].Name = "移动轨道"
		}
	}
	got, _ := Get("history.undoBatch").Apply(context.Background(), f.rd, map[string]string{
		"anchor": "6", "count": "2", "actor": undoActorCLI, "checkEffect": "0",
	})
	rep := got.(*undoBatchReport)
	if f.undid != 1 || len(rep.Undone) != 1 {
		t.Fatalf("应当只退掉顶上那一条，实得 %v", rep.Undone)
	}
	if !strings.Contains(rep.Note, "停手") || !strings.Contains(rep.Note, "移动轨道") {
		t.Errorf("要说清停在哪一条上，实得 %q", rep.Note)
	}
	if !strings.Contains(rep.Note, "不会自己回来") {
		t.Errorf("要提醒「前面已退的不回滚」，实得 %q", rep.Note)
	}
}

// ⭐ 用户 2026-10-15：「如果有选中音符什么的批量撤回直接撤」
// ⇒ 顶上是你自己在 ACE 里点出来的「选择音符」，照样**穿过去**把这一批退完（穿过的**不占 count 名额**）。
func TestUndoBatchSkipsSelectionNoiseAtTop(t *testing.T) {
	f := sampleStack(8) // 我们的第二批：index 6,7
	// 用户在我们写完之后又在 ACE 里点了几下：栈顶压了 2 条噪音（署名空）
	f.entries = append([]UndoEntry{
		{Index: 9, Name: "移动光标", Actor: "", Applied: true},
		{Index: 8, Name: "选择音符", Actor: "", Applied: true},
	}, f.entries...)
	f.cursor = 10
	got, _ := Get("history.undoBatch").Apply(context.Background(), f.rd, map[string]string{
		"anchor": "6", "count": "2", // count 只数**内容**条目
	})
	rep := got.(*undoBatchReport)
	if f.undid != 4 {
		t.Fatalf("应当退 4 条（2 条噪音 + 2 条内容），实得 %d：%v", f.undid, rep.Undone)
	}
	if len(rep.Skipped) != 2 || rep.Skipped[0] != "移动光标" || rep.Skipped[1] != "选择音符" {
		t.Errorf("穿过的两条噪音要如实列出来（新的在前），实得 %v", rep.Skipped)
	}
	if len(rep.Names) != 2 || len(rep.Undone) != 4 {
		t.Errorf("内容 2 条 / 总退 4 条，实得 names=%v undone=%v", rep.Names, rep.Undone)
	}
	if !strings.Contains(rep.Note, "顺带穿过了 2 条选择/光标条目") {
		t.Errorf("提示里要说清穿过几条，实得 %q", rep.Note)
	}
	if rep.Stopped != "" || rep.Blocked != "" {
		t.Errorf("穿噪音不该被当成异常：stopped=%q blocked=%q", rep.Stopped, rep.Blocked)
	}
	// 穿过 ≠ 不算撤销：ACE 那边确实少了 4 条
	if rep.CursorAfter != 6 {
		t.Errorf("游标应当回到锚点 6，实得 %d", rep.CursorAfter)
	}
}

// ⭐ 用户 2026-10-15：「只要不是实际落在工程的改动，都是噪音」
// ⇒ 名字**不在**任何清单里也不要紧：按效果判 —— 撤了它 `dirty` 还是 false ⇒ 就是噪音，照样穿过去。
func TestUndoBatchEffectProbePassesUnknownNoise(t *testing.T) {
	f := sampleStack(8)                                                                               // 我们的第二批：index 6,7（调整音符大小）
	f.entries = append([]UndoEntry{{Index: 8, Name: "滚动视图", Actor: "", Applied: true}}, f.entries...) // 名字谁都不认识
	f.cursor = 9
	got, _ := Get("history.undoBatch").Apply(context.Background(), f.rd, map[string]string{
		"anchor": "6", "count": "2", "checkEffect": "1", // 这条专门验"按效果判"
	})
	rep := got.(*undoBatchReport)
	if f.undid != 3 {
		t.Fatalf("应当是「探一条噪音 + 退两条内容」= 3 条，实得 %d：%v", f.undid, rep.Undone)
	}
	if len(rep.Skipped) != 1 || rep.Skipped[0] != "滚动视图" {
		t.Errorf("按效果判出的噪音要记进 skipped，实得 %v", rep.Skipped)
	}
	if len(rep.Probed) != 1 || !strings.Contains(rep.Probed[0], "噪音") {
		t.Errorf("探针结论要如实写（撤了 dirty 还是 false ⇒ 噪音），实得 %v", rep.Probed)
	}
	if len(rep.Names) != 2 || rep.CursorAfter != 6 {
		t.Errorf("内容是那 2 条、游标回锚点 6，实得 names=%v cursorAfter=%d", rep.Names, rep.CursorAfter)
	}
	if rep.ProbeSaves != 1 {
		t.Errorf("探针只该多保存 1 次，实得 %d", rep.ProbeSaves)
	}
	if rep.Stopped != "" || rep.Blocked != "" {
		t.Errorf("这条不该被当成异常：stopped=%q blocked=%q", rep.Stopped, rep.Blocked)
	}
}

// 把 skipNames 清空 ⇒ 回到"严格按条数、一条噪音都不过"的老行为（顶上是别人的就停手、一条都不退）。
func TestUndoBatchNoSkipNamesKeepsStrictBehavior(t *testing.T) {
	f := sampleStack(8)
	f.entries = append([]UndoEntry{{Index: 8, Name: "选择音符", Actor: "", Applied: true}}, f.entries...)
	f.cursor = 9
	got, _ := Get("history.undoBatch").Apply(context.Background(), f.rd, map[string]string{
		"anchor": "6", "count": "2", "skipNames": "", "checkEffect": "0",
	})
	rep := got.(*undoBatchReport)
	if f.undid != 0 {
		t.Fatalf("严格模式：顶上是别人的 ⇒ 一条都不退，实得 %d", f.undid)
	}
	if !strings.Contains(rep.Note, "选择音符") || !strings.Contains(rep.Note, "一条都没退") {
		t.Errorf("要把拦路那条的名字与「一条都没退」说清，实得 %q", rep.Note)
	}
}

// 名字守卫：锚点以上那一批的**名字**要跟页面记的对得上。
// 场景：页面记的是"连接音符那 14 条"（调整音符大小），可这中间热键重放了一次别的工具 ⇒ 顶上名字对不上 ⇒ 一条都不退。
func TestUndoBatchBlocksWhenTopNameIsNotInTheBatch(t *testing.T) {
	defer func() { undoNames = nil }()
	f := sampleStack(8) // index 6,7 = 调整音符大小
	got, _ := Get("history.undoBatch").Apply(context.Background(), f.rd, map[string]string{
		"anchor": "6", "count": "2", "checkEffect": "0", "names": `["编辑歌词"]`, // ← 页面记错了（或栈顶被别人的写入占了）
	})
	rep := got.(*undoBatchReport)
	if f.undid != 0 {
		t.Fatalf("名字对不上 ⇒ 一条都不许退，实得 %d", f.undid)
	}
	if !strings.Contains(rep.Note, "不在这一批的记录里") {
		t.Errorf("要如实说名字对不上，实得 %q", rep.Note)
	}
	// 名字对得上就照退
	f2 := sampleStack(8)
	got2, _ := Get("history.undoBatch").Apply(context.Background(), f2.rd, map[string]string{
		"anchor": "6", "count": "2", "names": `["调整音符大小"]`,
	})
	if f2.undid != 2 || got2.(*undoBatchReport).Stopped != "" {
		t.Errorf("名字对得上就应当退 2 条，实得 %d 条 / %q", f2.undid, got2.(*undoBatchReport).Stopped)
	}
}

// `names` 不是 JSON 数组 ⇒ 直接拒（不猜、不当空处理）。
func TestUndoBatchRejectsBadNames(t *testing.T) {
	defer func() { undoNames = nil }()
	if _, err := undoBatchPlan(context.Background(), sampleStack(8).rd,
		map[string]string{"anchor": "6", "names": "调整音符大小"}); err == nil {
		t.Error("names 不是 JSON 数组：应当报错")
	}
}

// 干跑：一条写命令都不许发，但要把"会退掉哪些条目"报出来。
func TestUndoBatchPlanIsReadOnly(t *testing.T) {
	f := sampleStack(8)
	got, err := Get("history.undoBatch").Plan(context.Background(), f.rd, map[string]string{
		"anchor": "6", "count": "2", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep := got.(*undoBatchReport)
	if !rep.DryRun || rep.WillUndo != 2 {
		t.Fatalf("干跑应当报 willUndo=2，实得 %+v", rep)
	}
	if len(rep.Names) != 2 || rep.Names[0] != "调整音符大小" {
		t.Errorf("要把会退掉的条目名字列出来（新的在前），实得 %v", rep.Names)
	}
	for _, c := range f.calls {
		if c == "history undo" {
			t.Fatalf("干跑不许发写命令，实发了：%v", f.calls)
		}
	}
}

// `count` 只是上限：既不越过锚点，也不越过 count（count 只数**内容**条目，穿过的噪音不算）。
func TestUndoBatchCountIsAnUpperBound(t *testing.T) {
	undoNames = nil // 这条直接调 planUndo（不走 undoParams），别被别的用例留下的名单影响
	undoSkips = nil
	f := sampleStack(8)
	rep := planUndo(&UndoStack{Index: 8, Entries: f.entries}, 6, 0, undoActorCLI)
	if rep.WillUndo != 2 {
		t.Errorf("count=0 ⇒ 退到锚点为止（2 条），实得 %d", rep.WillUndo)
	}
	rep2 := planUndo(&UndoStack{Index: 8, Entries: f.entries}, 6, 1, undoActorCLI)
	if rep2.WillUndo != 1 {
		t.Errorf("count=1 ⇒ 只退 1 条，实得 %d", rep2.WillUndo)
	}
	rep3 := planUndo(&UndoStack{Index: 8, Entries: f.entries}, 4, 99, undoActorCLI)
	if rep3.WillUndo != 4 {
		t.Errorf("锚点 4、游标 8 ⇒ 最多 4 条，实得 %d", rep3.WillUndo)
	}
}

func TestUndoBatchParamsRejectBadInput(t *testing.T) {
	for _, c := range []struct{ name, anchor, want string }{
		{"没给锚点", "", "要一个锚点"},
		{"锚点不是数", "abc", "0 或正整数"},
		{"锚点是负数", "-1", "0 或正整数"},
	} {
		if _, err := undoBatchPlan(context.Background(), sampleStack(8).rd,
			map[string]string{"anchor": c.anchor}); err == nil {
			t.Errorf("%s：应当报错", c.name)
		} else if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s：错误里要提到 %q，实得 %q", c.name, c.want, err.Error())
		}
	}
	if _, err := undoBatchPlan(context.Background(), sampleStack(8).rd,
		map[string]string{"anchor": "6", "count": "x"}); err == nil {
		t.Error("count 不是数：应当报错")
	}
}

// 一条 `history undo` 失败 ⇒ 停手，并把"前面已退的不回滚"说清（不能让回包看着像全成功）。
func TestUndoBatchReportsWriteFailure(t *testing.T) {
	f := sampleStack(8)
	f.failAt = 1
	got, _ := Get("history.undoBatch").Apply(context.Background(), f.rd, map[string]string{
		"anchor": "6", "count": "2",
	})
	rep := got.(*undoBatchReport)
	if len(rep.Undone) != 0 {
		t.Errorf("一条都没退成功，实得 %v", rep.Undone)
	}
	if !strings.Contains(rep.Note, "失败") || !strings.Contains(rep.Note, "没回滚") {
		t.Errorf("要把失败与「不回滚」如实写出来，实得 %q", rep.Note)
	}
}
