package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"acetool/internal/acecli"
)

// 轨道选择：空/current ⇒ 标记线那条轨；否则 0 起下标。
// 这条不能两处各写一遍 —— 写岔了就是复制到错的轨道上，而且**不报错**。
func TestResolveTrackSel(t *testing.T) {
	for _, c := range []struct {
		in      string
		wantIdx int
		wantCur bool
		wantErr bool
	}{
		{"", 0, true, false},
		{"current", 0, true, false},
		{"CURRENT", 0, true, false},
		{"  cur  ", 0, true, false},
		{"当前", 0, true, false},
		{"当前轨道", 0, true, false},
		{"0", 0, false, false},
		{"3", 3, false, false},
		{" 2 ", 2, false, false},
		{"abc", 0, false, true},
		{"-1", 0, false, true},
		{"1.5", 0, false, true},
	} {
		idx, cur, err := resolveTrackSel(map[string]string{"srcTrack": c.in}, "srcTrack")
		if c.wantErr {
			if err == nil {
				t.Errorf("resolveTrackSel(%q) 应当报错，实得 idx=%d cur=%v", c.in, idx, cur)
			}
			continue
		}
		if err != nil {
			t.Errorf("resolveTrackSel(%q) 不该报错：%v", c.in, err)
			continue
		}
		if idx != c.wantIdx || cur != c.wantCur {
			t.Errorf("resolveTrackSel(%q) = (%d,%v)，期望 (%d,%v)", c.in, idx, cur, c.wantIdx, c.wantCur)
		}
	}
}

// ⛔ 这条是本轮**最容易算错**的地方：`note move --pitch` 是**刚性移动**
// （只有锚点音符到目标音高，其余按**同一 delta** 平移）。
//
// 所以绝不能"按目标音高分组"：A=60、B=62 都要变 64 时，一次调用会把 B 推到 66。
// 安全的分组键是 **(当前音高, 目标音高)**。
func TestXcopyPitchGroupsByFromAndTo(t *testing.T) {
	pp := &xcopyPairPlan{}
	pp.addPitch("{a}", 60, 64)
	pp.addPitch("{b}", 62, 64) // 目标相同、当前不同 ⇒ **必须**另起一组
	if len(pp.pitchGroups) != 2 {
		t.Fatalf("目标音高相同但当前音高不同，必须分成 2 组（刚性移动会算错），实得 %d 组", len(pp.pitchGroups))
	}
	// 同一组（from 与 to 都相同）才合并
	pp.addPitch("{c}", 60, 64)
	if len(pp.pitchGroups) != 2 {
		t.Fatalf("(from,to) 完全相同应当合并进已有组，实得 %d 组", len(pp.pitchGroups))
	}
	if n := len(pp.pitchGroups[0].NoteID); n != 2 {
		t.Errorf("第一组应当有 2 个音符，实得 %d", n)
	}
	for _, g := range pp.pitchGroups {
		if g.From == g.To {
			t.Errorf("同一个音高不该进写清单（那是空操作）：%+v", g)
		}
	}
}

func TestSameSymbols(t *testing.T) {
	for _, c := range []struct {
		a, b []string
		want bool
	}{
		{[]string{"m", "ing"}, []string{"m", "ing"}, true},
		{[]string{"m", "ing"}, []string{"m", "ian"}, false},
		{[]string{"m"}, []string{"m", "ing"}, false},
		{nil, nil, true},
		{[]string{}, nil, true},
	} {
		if got := sameSymbols(c.a, c.b); got != c.want {
			t.Errorf("sameSymbols(%v,%v) = %v，期望 %v", c.a, c.b, got, c.want)
		}
	}
}

// 双指针是原脚本的直译。这里钉三件事：
// ① 相等才配 ② 一边领先时另一边前进 ③ **输入乱序也要能配**（原脚本信宿主顺序，我们不信）
func TestMatchByOnset(t *testing.T) {
	src := []xcopyNote{
		{NoteUUID: "s1", Pos: 0},
		{NoteUUID: "s2", Pos: 480},
		{NoteUUID: "s3", Pos: 960},
	}
	// 故意乱序 + 少一个
	dst := []xcopyNote{
		{NoteUUID: "d2", Pos: 960},
		{NoteUUID: "d1", Pos: 0},
	}
	pairs, so, do := matchByOnset(src, dst, 0, 0)
	if len(pairs) != 2 {
		t.Fatalf("应当配上 2 对（s1↔d1、s3↔d2），实得 %d", len(pairs))
	}
	if so != 1 {
		t.Errorf("源侧应当有 1 个配不上（s2@480），实得 %d", so)
	}
	if do != 0 {
		t.Errorf("目标侧应当 0 个配不上，实得 %d", do)
	}
	// 配对要指回**原数组下标**（乱序输入下 d1 在下标 1）
	got := map[string]string{}
	for _, p := range pairs {
		got[src[p[0]].NoteUUID] = dst[p[1]].NoteUUID
	}
	if got["s1"] != "d1" || got["s3"] != "d2" {
		t.Errorf("配对结果错了：%v", got)
	}
}

// 片段起点不同 ⇒ 绝对 onset 要带上 clipBegin（这是 `offsetDelta` 那件事的本体）。
func TestMatchByOnsetUsesClipOffset(t *testing.T) {
	src := []xcopyNote{{NoteUUID: "s1", Pos: 480}}
	dst := []xcopyNote{{NoteUUID: "d1", Pos: 0}}
	// 源片段从 0 开始 ⇒ 绝对 480；目标片段从 480 开始 ⇒ 绝对 480 ⇒ 配上
	if pairs, _, _ := matchByOnset(src, dst, 0, 480); len(pairs) != 1 {
		t.Errorf("带上 clipBegin 后应当配上，实得 %d 对", len(pairs))
	}
	// 两边都从 0 开始 ⇒ 绝对 480 vs 0 ⇒ 配不上
	if pairs, _, _ := matchByOnset(src, dst, 0, 0); len(pairs) != 0 {
		t.Errorf("绝对 onset 不同就不该配，实得 %d 对", len(pairs))
	}
}

// ⭐ 干跑只读：给 Plan 一个**假 CLI**，它响应所有读命令，
// 一旦 Plan 去调 `note set-lyric` / `note move` / `phoneme set` 就立刻失败。
// ⇒「干跑会不会偷偷改工程」是**真跑出来**的，不是读源码猜的。
func TestXcopyNotesPlanIsReadOnly(t *testing.T) {
	fake := newXcopyFake(t, &[]string{})

	got, err := Get("xcopy.notes").Plan(context.Background(), fake, map[string]string{
		"srcTrack": "0", "dstTrack": "1", "lyrics": "1", "pitch": "1", "phonemes": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep, ok := got.(*xcopyReport)
	if !ok {
		t.Fatalf("干跑应当回 *xcopyReport，实得 %T", got)
	}
	if !rep.DryRun {
		t.Error("干跑结果要标 DryRun")
	}
	if rep.Matched != 2 {
		t.Errorf("应当配上 2 个音符，实得 %d", rep.Matched)
	}
	if rep.WillLyric != 2 || rep.WillPitch != 1 || rep.WillPhonemes != 2 {
		t.Errorf("计划数不对：歌词 %d（期望 2）、音高 %d（期望 1，60→60 是空操作）、音素 %d（期望 2）",
			rep.WillLyric, rep.WillPitch, rep.WillPhonemes)
	}
	if len(rep.Calls) == 0 {
		t.Error("干跑要把打算发的命令列出来")
	}
	if rep.Note == "" {
		t.Error("干跑要有一句收尾说明")
	}
	// 空操作要被说明（而不是静默跳过）
	joined := strings.Join(rep.Skipped, " | ")
	if !strings.Contains(joined, "音高本来就一样") {
		t.Errorf("60→60 这种空操作应当被说明，实得 skipped=%v", rep.Skipped)
	}
	// 移不过来的属性要如实列出来
	if !strings.Contains(rep.Unported, "exprGroup") || !strings.Contains(rep.Unported, "dur") {
		t.Errorf("移不过来的属性要列出来，实得 %q", rep.Unported)
	}
}

// 音素**不能跨语言写** ⇒ 两侧语言不同必须跳过并说明，而且**一个音符都不许写**。
func TestXcopyNotesSkipsCrossLanguagePhonemes(t *testing.T) {
	var called []string
	fake := newXcopyFakeLang(t, &called, "English") // 目标音符是英文

	got, err := Get("xcopy.notes").Plan(context.Background(), fake, map[string]string{
		"srcTrack": "0", "dstTrack": "1", "lyrics": "1", "pitch": "0", "phonemes": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep := got.(*xcopyReport)
	if rep.WillPhonemes != 0 {
		t.Errorf("跨语言音素必须全部跳过，实得 WillPhonemes=%d", rep.WillPhonemes)
	}
	joined := strings.Join(rep.Skipped, " | ")
	if !strings.Contains(joined, "跨语言") {
		t.Errorf("跳过原因要说明是跨语言，实得 skipped=%v", rep.Skipped)
	}
	for _, c := range called {
		if strings.HasPrefix(c, "phoneme set") {
			t.Errorf("跨语言时不该发出音素写入：%s", c)
		}
	}
}

// 勾错方向（都没勾）要**明确报错**，而不是发一堆空计划。
func TestXcopyNotesNothingSelected(t *testing.T) {
	fake := newXcopyFake(t, &[]string{})
	_, err := Get("xcopy.notes").Plan(context.Background(), fake, map[string]string{
		"srcTrack": "0", "dstTrack": "1",
	})
	if err == nil {
		t.Error("一个属性都没勾时应当报错")
	} else if !strings.Contains(err.Error(), "一个属性都没勾") {
		t.Errorf("报错要说清是「一个属性都没勾」，实得：%v", err)
	}
}

// 空槽要压成区间 —— `track list --include-empty` 实测会报 **99 个**空槽（下标 1–99），
// 原样摊开会把结果里有用的信息淹掉。
func TestCompactInts(t *testing.T) {
	for _, c := range []struct {
		in   []int
		want string
	}{
		{nil, ""},
		{[]int{3}, "3"},
		{[]int{1, 2}, "1,2"}, // 两个连着：写成 "1,2" 比 "1–2" 更好认
		{[]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}, "1–20"},
		{[]int{1, 3, 5}, "1,3,5"},
		{[]int{7, 9, 10, 11}, "7,9–11"},
	} {
		if got := compactInts(c.in); got != c.want {
			t.Errorf("compactInts(%v) = %q，期望 %q", c.in, got, c.want)
		}
	}
	// 造一个 1..99 的区间（就是这台工程的真实形状）
	var many []int
	for i := 1; i <= 99; i++ {
		many = append(many, i)
	}
	if got := compactInts(many); got != "1–99" {
		t.Errorf("99 个空槽应当压成「1–99」，实得 %q", got)
	}
}

// 真轨与空槽要分开：空槽仍然要「知道」（占下标、不能当源/目标），但不能摊进真轨列表。
func TestSplitRoster(t *testing.T) {
	tracks := []xcopyTrackView{
		{Index: 0, Name: "洛天依", Type: "Sing", ClipCount: 1},
		{Index: 1, Type: "Empty"},
		{Index: 2, Type: "Empty"},
		{Index: 3, Name: "吉他", Type: "Instrument", ClipCount: 2},
	}
	real, empties := splitRoster(tracks)
	if len(real) != 2 || real[0].Name != "洛天依" || real[1].Name != "吉他" {
		t.Errorf("真轨应当是 洛天依/吉他，实得 %+v", real)
	}
	if len(empties) != 2 || empties[0] != 1 || empties[1] != 2 {
		t.Errorf("空槽下标应当是 1,2，实得 %v", empties)
	}
	hint := trackRosterHint(tracks)
	if !strings.Contains(hint, "1,2") || !strings.Contains(hint, "不能当源/目标") {
		t.Errorf("花名册提示要压出空槽下标并说明不能当源/目标，实得 %q", hint)
	}
}

// 轨道下标不存在时，报错要**把这台工程有哪些轨带上**（省得用户盲猜）。
func TestXcopyNotesBadTrackReportsRoster(t *testing.T) {
	fake := newXcopyFake(t, &[]string{})
	_, err := Get("xcopy.notes").Plan(context.Background(), fake, map[string]string{
		"srcTrack": "9", "dstTrack": "1", "lyrics": "1",
	})
	if err == nil {
		t.Fatal("不存在的轨道应当报错")
	}
	if !strings.Contains(err.Error(), "这台工程有") {
		t.Errorf("报错要列出可用轨道，实得：%v", err)
	}
}

// 工具形态：Plan/Apply 成对 + Kind=write + 参数齐（注册表不变量由 registry_test 通用卡住，这里钉参数名）。
func TestXcopyNotesToolShape(t *testing.T) {
	tool := Get("xcopy.notes")
	if tool == nil {
		t.Fatal("没有注册 xcopy.notes")
	}
	if tool.Kind != KindWrite {
		t.Errorf("xcopy.notes 应当是 write，实得 %v", tool.Kind)
	}
	if tool.Plan == nil || tool.Apply == nil {
		t.Fatal("Plan/Apply 必须成对")
	}
	if tool.Build != nil || tool.Inspect != nil {
		t.Error("Plan+Apply 不该同时有 Build/Inspect")
	}
	names := map[string]bool{}
	for _, p := range tool.Params {
		names[p.Name] = true
	}
	// ⭐ 用户 2026-10-10：「把所有干跑都删掉」⇒ 参数表里**不再有** dryRun（点了就真写）
	for _, want := range []string{"srcTrack", "dstTrack", "lyrics", "pitch", "phonemes"} {
		if !names[want] {
			t.Errorf("缺参数 %s", want)
		}
	}
	if names["dryRun"] {
		t.Error("干跑参数应当已经删掉（用户 2026-10-10）")
	}
	if groupOf(tool.Category) != "移植脚本" {
		t.Errorf("应当归到「移植脚本」栏，实得 %q", groupOf(tool.Category))
	}
}

// ---------- 回归：写失败必须**真的**被当成失败 ----------

// ⛔ 这条是一次真 bug 的回归：`acecli.Client.Run` 结尾是 `return r, nil`
// —— **它永远不回 error**，失败信息全在 Result 里（ExitCode / Err 信封）。
//
// 于是写操作写成 `if _, err := rd(ctx, ...); err != nil` 就是**写失败被当成成功**：
// 回滚不触发、结果里报"已写入"。读操作由 `readJSON` 兜住了，写操作之前**没人兜**。
func TestRunWriteSurfacesFailure(t *testing.T) {
	env := &acecli.Envelope{Code: "USER_BUSY", Message: "an undo bracket is open", Hint: "retry later"}
	rd := func(_ context.Context, args ...string) (*acecli.Result, error) {
		return &acecli.Result{Args: args, ExitCode: acecli.ExitFailed, Stderr: "…", Err: env}, nil
	}
	err := runWrite(context.Background(), rd, "clip", "split", "--pos", "0t")
	if err == nil {
		t.Fatal("写失败必须回 error —— 回 nil 就等于回滚永远不会触发、结果里会报「已写入」")
	}
	// 纪律：ACE 的原话（error.code / message / hint）要原样带出来，别翻译成「出错了」
	for _, want := range []string{"USER_BUSY", "undo bracket", "retry later"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误里要带 ACE 的原话 %q，实得：%v", want, err)
		}
	}

	// 成功 —— 而且**回包是人话不是 JSON**（官方口径：`track set` 成功回 `Track 0 updated.`）
	// ⇒ 绝不能因为"解析不出 JSON"就报失败（那会让用户以为没写进去，其实写了）。
	ok := func(_ context.Context, args ...string) (*acecli.Result, error) {
		return &acecli.Result{ExitCode: acecli.ExitOK, Stdout: "Track 0 updated."}, nil
	}
	if err := runWrite(context.Background(), ok, "track", "set"); err != nil {
		t.Errorf("成功（哪怕是回人话、非 JSON）不该报错：%v", err)
	}

	// 进程没起来（SpawnErr、没有信封）也要给出**有内容**的错误，不能是空话
	spawn := func(_ context.Context, args ...string) (*acecli.Result, error) {
		return &acecli.Result{ExitCode: acecli.ExitSpawnFailed, SpawnErr: "executable file not found"}, nil
	}
	if err := runWrite(context.Background(), spawn, "clip", "split"); err == nil {
		t.Error("进程没起来要报错")
	} else if !strings.Contains(err.Error(), "executable file not found") {
		t.Errorf("SpwanErr 要带出来，实得：%v", err)
	}
}

// 回归：`track.split` 的"中途失败就回滚"**曾经是死代码**
// （它判断写失败用的也是那个恒为 nil 的 err）。这条用假 CLI 让 `clip split` 失败，
// 断言：① 结果里 Rolled=true ② 真发出了"选中复制轨 + track delete"两步。
func TestTrackSplitRollsBackWhenSplitFails(t *testing.T) {
	var called []string
	rd := func(_ context.Context, args ...string) (*acecli.Result, error) {
		cmd := strings.Join(args, " ")
		called = append(called, cmd)
		switch {
		case len(args) >= 2 && args[0] == "caret":
			return okJSON(`{"trackIndex":0,"trackUuid":"{trk}"}`), nil
		case len(args) >= 2 && args[0] == "transport":
			return okJSON(`{"positionTick":1000,"status":"stopped"}`), nil
		case len(args) >= 2 && args[0] == "clip" && args[1] == "list":
			return okJSON(`{"clips":[{"clipType":"sing","clipName":"B","clipBegin":500,"clipEnd":1500,"clipUuid":"{b}"}]}`), nil
		case len(args) >= 2 && args[0] == "timesig":
			return okJSON(`{"signatures":[{"barPos":0,"tick":0,"numerator":4,"denominator":4}]}`), nil
		case len(args) >= 2 && args[0] == "track" && args[1] == "duplicate":
			return okJSON(`{"trackIndex":1,"trackName":"B copy","trackUuid":"{dup}"}`), nil
		case len(args) >= 2 && args[0] == "clip" && args[1] == "split":
			// 最容易失败的那一步，这里就让它失败
			return &acecli.Result{ExitCode: acecli.ExitFailed,
				Err: &acecli.Envelope{Code: "INVALID_ARG", Message: "pos must fall strictly inside the clip"}}, nil
		case len(args) >= 2 && args[0] == "selection":
			return okJSON(`{}`), nil
		case len(args) >= 2 && args[0] == "track" && args[1] == "delete":
			return okJSON(`{}`), nil
		}
		return nil, fmt.Errorf("假 CLI 没准备这条命令：%s", cmd)
	}

	got, err := Get("track.split").Apply(context.Background(), rd, map[string]string{})
	if err != nil {
		t.Fatalf("回滚是**正常收尾**（把结果和说明交出来），不该回 error：%v", err)
	}
	rep, ok := got.(*splitReport)
	if !ok {
		t.Fatalf("应当回 *splitReport，实得 %T", got)
	}
	if !rep.Rolled {
		t.Errorf("clip split 失败后必须回滚（Rolled=true）。⚠️ 若这里是 false，多半是写失败又被当成成功了。called=%v", called)
	}
	// 回滚的两步要真发出去
	var sawSelect, sawDelete bool
	for _, c := range called {
		if strings.HasPrefix(c, "selection set") {
			sawSelect = true
		}
		if strings.HasPrefix(c, "track delete") {
			sawDelete = true
		}
	}
	if !sawSelect || !sawDelete {
		t.Errorf("回滚要把「选中复制轨 + track delete」两步都发；实得 select=%v delete=%v，called=%v",
			sawSelect, sawDelete, called)
	}
	// 失败说明要如实：原轨没动过（split 排在 delete 之前）
	if !strings.Contains(rep.Note, "回滚") {
		t.Errorf("结果里要说清回滚了什么，实得：%q", rep.Note)
	}
}

// ---------- 假 CLI ----------

func flagVal(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

// newXcopyFake 造一个假 CLI：读命令全响应，写命令一律失败（这就是"干跑只读"的探针）。
func newXcopyFake(t *testing.T, called *[]string) ReadFunc {
	return newXcopyFakeLang(t, called, "Chinese")
}

func newXcopyFakeLang(t *testing.T, called *[]string, dstLang string) ReadFunc {
	t.Helper()
	return func(_ context.Context, args ...string) (*acecli.Result, error) {
		cmd := strings.Join(args, " ")
		*called = append(*called, cmd)
		if len(args) >= 2 {
			switch args[0] + " " + args[1] {
			case "note set-lyric", "note move", "phoneme set":
				return nil, fmt.Errorf("干跑不许调写命令：%s", cmd)
			}
		}
		switch {
		case len(args) >= 2 && args[0] == "track" && args[1] == "list":
			return okJSON(`{"tracks":[
				{"trackIndex":0,"trackName":"源","trackType":"Sing","clipCount":1},
				{"trackIndex":1,"trackName":"目标","trackType":"Sing","clipCount":1}]}`), nil
		case len(args) >= 2 && args[0] == "caret":
			return okJSON(`{"trackIndex":1,"trackUuid":"{trk1}"}`), nil
		case len(args) >= 2 && args[0] == "clip" && args[1] == "list":
			tr := flagVal(args, "--track-index")
			name := "源片段"
			if tr == "1" {
				name = "目标片段"
			}
			return okJSON(fmt.Sprintf(
				`{"clips":[{"clipType":"sing","clipName":%q,"clipBegin":0,"clipEnd":960,"clipUuid":"{c%s}"}]}`, name, tr)), nil
		case len(args) >= 2 && args[0] == "clip" && args[1] == "note-content":
			if flagVal(args, "--track-index") == "0" {
				return okJSON(`{"noteCount":2,"notes":[
					{"noteUuid":"{s1}","pos":0,"dur":480,"endPos":480,"pitch":60,"lyric":"明","language":"Chinese"},
					{"noteUuid":"{s2}","pos":480,"dur":480,"endPos":960,"pitch":62,"lyric":"天","language":"Chinese"}]}`), nil
			}
			return okJSON(fmt.Sprintf(`{"noteCount":2,"notes":[
				{"noteUuid":"{d1}","pos":0,"dur":480,"endPos":480,"pitch":60,"lyric":"旧","language":%q},
				{"noteUuid":"{d2}","pos":480,"dur":480,"endPos":960,"pitch":61,"lyric":"歌","language":%q}]}`, dstLang, dstLang)), nil
		case len(args) >= 2 && args[0] == "phoneme" && args[1] == "list":
			// 按点名的音符回音素（源与目标各一套）。
			// ⚠️ 源侧标 `isOverride:true`：用户 2026-10-07 的口径是「**只复制手动更改过的**」——
			// 源还是默认发音时我们**不搬**（那是引擎算出来的，不是用户的意图）⇒ 假数据也得是"改过的"，
			// 否则测到的就不是"复制"而是"跳过"。
			var rows []string
			for _, a := range args {
				switch a {
				case "{s1}":
					rows = append(rows, `{"noteUuid":"{s1}","language":"Chinese","phonemes":["m","ing"],"defaultPhonemes":["m","a"],"isOverride":true}`)
				case "{s2}":
					rows = append(rows, `{"noteUuid":"{s2}","language":"Chinese","phonemes":["t","ian"],"defaultPhonemes":["t","a"],"isOverride":true}`)
				case "{d1}":
					rows = append(rows, fmt.Sprintf(`{"noteUuid":"{d1}","language":%q,"phonemes":["j","iu"],"defaultPhonemes":["j","iu"],"isOverride":false}`, dstLang))
				case "{d2}":
					rows = append(rows, fmt.Sprintf(`{"noteUuid":"{d2}","language":%q,"phonemes":["g","e"],"defaultPhonemes":["g","e"],"isOverride":false}`, dstLang))
				}
			}
			return okJSON(`{"notes":[` + strings.Join(rows, ",") + `]}`), nil
		}
		return nil, fmt.Errorf("假 CLI 没准备这条命令：%s", cmd)
	}
}
