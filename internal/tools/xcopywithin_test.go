package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"acetool/internal/acecli"
)

// ---------- 「轨内复制」（xcopy.within / xcopy.view）的测试 ----------
//
// 移植源 `轨道内复制.js` 的**属性那一半**（歌词 / 音高 / 音素）已经和「跨轨复制」共用
// （`planPairProps` + `applyXcopyPairs`），所以这里只钉**这一支独有**的东西：
//
//	① 三个**小节号** → 片段内 tick 的换算（含 clipBegin 偏移、变拍号走 ACE 的 convert）；
//	② 配对判据：源与目标各自减去自己范围的起点后**位置相等**（原脚本的双指针）；
//	③ ⛔ **绝不新建音符**（用户 2026-10-07 明确选的是"只改已存在的"）⇒ 计划命令里不许出现 `note add`；
//	④ 小节号不合法 / 片段类型不支持时怎么报错、怎么跳过。
//
// ⚠️ 真机上"能不能配上"还要看音符是不是真按相对位置对齐 —— 这一条只有真机数据能证，
// 本地用**两段一模一样**的假音符来复现。

// xwFake 造一个"片段时间轴 + 两段音符"的假 CLI。
//
// 假工程：clipBegin=0、8 小节（15360 tick）、4/4；
//
//	notes 由调用方给（`pos` 是片段内 tick）。
func xwFake(t *testing.T, called *[]string, clipBegin int64, notes []xcopyNote, clipType string, phonemes bool) ReadFunc {
	t.Helper()
	if clipType == "" {
		clipType = "sing"
	}
	return func(_ context.Context, args ...string) (*acecli.Result, error) {
		cmd := strings.Join(args, " ")
		*called = append(*called, cmd)
		switch {
		case len(args) >= 2 && args[0] == "editor" && args[1] == "status":
			return okJSON(fmt.Sprintf(`{"isAvailable":true,"trackIndex":0,"clipIndex":0,"clipUuid":"{c1}","clipName":"测试片段","editorType":%q}`, clipType)), nil
		case len(args) >= 2 && args[0] == "clip" && args[1] == "list":
			// ⚠️ `noteCount` 必须给：`view.minimap` 只在 `noteCount > 0` 时才去读那个片段的音符
			//（不给 ⇒ 缩略图里是有片段、但一个音符都没有）
			return okJSON(fmt.Sprintf(`{"clips":[{"clipType":%q,"clipName":"测试片段","clipBegin":%d,"clipEnd":%d,"clipUuid":"{c1}","noteCount":%d}]}`,
				clipType, clipBegin, clipBegin+15360, len(notes))), nil
		case len(args) >= 2 && args[0] == "clip" && args[1] == "note-content":
			var rows []string
			for _, n := range notes {
				// ⚠️ 辅音时长数组**必须原样带出来**：v1 的"手动改过"判据就是它非空
				//（`hasManualConsonants`）。第一版这里没吐这两个字段 ⇒ 工具永远判"没改过"。
				hc, _ := json.Marshal(n.HeadConsLen)
				tc, _ := json.Marshal(n.TailConsLen)
				if n.HeadConsLen == nil {
					hc = []byte("[]")
				}
				if n.TailConsLen == nil {
					tc = []byte("[]")
				}
				rows = append(rows, fmt.Sprintf(`{"noteUuid":%q,"pos":%d,"dur":%d,"endPos":%d,"pitch":%d,"lyric":"la","language":"Chinese","headConsonants":%s,"tailConsonants":%s}`,
					n.NoteUUID, n.Pos, n.Dur, n.EndPos, n.Pitch, hc, tc))
			}
			return okJSON(`{"noteCount":` + strconv.Itoa(len(notes)) + `,"notes":[` + strings.Join(rows, ",") + `]}`), nil
		case len(args) >= 2 && args[0] == "convert" && args[1] == "measure-to-tick":
			bar := int64(1)
			for i, a := range args {
				if a == "--bar" && i+1 < len(args) {
					bar, _ = strconv.ParseInt(args[i+1], 10, 64)
				}
			}
			// 1 起的小节号；4/4 ⇒ 一小节 1920 tick（工程 tick，与 clipBegin 无关）
			return okJSON(fmt.Sprintf(`{"tick":%d}`, (bar-1)*1920)), nil
		case len(args) >= 2 && args[0] == "phoneme" && args[1] == "list":
			if !phonemes {
				return okJSON(`{"notes":[]}`), nil
			}
			var rows []string
			for i, a := range args {
				if a == "--note" && i+1 < len(args) {
					rows = append(rows, fmt.Sprintf(`{"noteUuid":%q,"phonemes":["l","a"],"defaultPhonemes":["l","a"],"isOverride":false,"language":"Chinese"}`, args[i+1]))
				}
			}
			return okJSON(`{"capabilities":{"supportsVowelSpan":true},"modelGeneration":"v2","notes":[` + strings.Join(rows, ",") + `]}`), nil
		case len(args) >= 2 && args[0] == "timesig":
			return okJSON(`{"signatures":[{"numerator":4,"denominator":4}]}`), nil
		case len(args) >= 2 && args[0] == "tempo":
			return okJSON(`{"tempo":120}`), nil
		case len(args) >= 2 && args[0] == "project" && args[1] == "info":
			return okJSON(`{"duration":30720}`), nil
		case len(args) >= 2 && args[0] == "track" && args[1] == "list":
			return okJSON(`{"tracks":[{"trackIndex":0,"trackName":"测试轨","trackType":"Sing","clipCount":1}]}`), nil
		}
		return nil, fmt.Errorf("假 CLI 没准备这条命令：%s", cmd)
	}
}

// runOf 造"第 bar 小节起、每 240 tick 一个、共 n 个"的音符（片段内 tick）。
func runOf(prefix string, startTick int64, n int) []xcopyNote {
	out := make([]xcopyNote, 0, n)
	for i := 0; i < n; i++ {
		p := startTick + int64(i)*240
		out = append(out, xcopyNote{
			NoteUUID: fmt.Sprintf("{%s%d}", prefix, i), Pos: p, Dur: 240, EndPos: p + 240, Pitch: 60, Lyric: "la",
		})
	}
	return out
}

// ① 小节号 → **工程** tick（`convert measure-to-tick` 给的是工程坐标；减 clipBegin 那一步在
// `analyzeWithin` 里做 —— 这两件事分开钉，免得以后有人以为这函数回的是片段内坐标）。
func TestWithinBarToTick(t *testing.T) {
	for _, c := range []struct {
		name      string
		clipBegin int64
		wantTick  int64
	}{
		{"片段从工程 0 起：第 3 小节 = 工程 3840", 0, 3840},
		{"片段从工程第 3 小节起：第 3 小节**仍然是工程 3840**（片段内坐标由调用方减）", 3840, 3840},
	} {
		var called []string
		fake := xwFake(t, &called, c.clipBegin, runOf("s", 0, 4), "sing", false)
		got, err := measureToTick(context.Background(), fake, 3, "源开始小节")
		if err != nil {
			t.Fatalf("%s：不该报错：%v", c.name, err)
		}
		if got != c.wantTick {
			t.Errorf("%s：实得 %d，期望 %d", c.name, got, c.wantTick)
		}
	}
}

// ①b ⭐ 用户 2026-10-07 改的作用域：「作用于轨内所有 clip，遍历绝对时间」——
// 片段从工程第 3 小节（3840）起时，**报告里的范围就是绝对 tick**（不再减 clipBegin 变成片段内坐标）。
func TestWithinRangeIsAbsolute(t *testing.T) {
	// 片段从工程第 3 小节起（3840），片段内 tick 0 就是这个片段的第 1 个音符位置
	notes := append(runOf("a", 0, 4), runOf("b", 3840, 4)...)
	var called []string
	fake := xwFake(t, &called, 3840, notes, "sing", false)
	got, err := Get("xcopy.within").Plan(context.Background(), fake, map[string]string{
		"srcStart": "3", "srcEnd": "4", "dstStart": "5", "lyrics": "1", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	pv := got.(*xcopyReport).Pairs[0]
	// ⭐ 第 3 小节 = **工程** 3840（绝对）——旧版这里会是片段内 0
	if pv.SrcBegin != 3840 || pv.SrcEndTick != 5760 {
		t.Errorf("范围应当是**工程绝对** [3840,5760)，实得 [%d,%d)", pv.SrcBegin, pv.SrcEndTick)
	}
	if pv.SrcAbsTick != 3840 || pv.SrcEndAbs != 5760 || pv.DstAbsTick != 7680 {
		t.Errorf("绝对 tick 不对：%+v", pv)
	}
	// 片段从 3840 起、音符 `a` 在片段内 0..960（绝对 3840..4800）、`b` 在片段内 3840..（绝对 7680..）
	// ⇒ 源窗口 [3840,5760] 里只有 `a` 那 4 个（`b` 在窗口外）
	if pv.SrcNotes != 4 {
		t.Errorf("源窗口里应当是 4 个音符（a 那 4 个；b 在 7680 之后、窗口外），实得 %d", pv.SrcNotes)
	}
}

// ①c ⭐ **多片段**：一条轨由两个片段拼成，源窗口横跨两个片段的部分**都要被搬到**
// （旧版只看编辑器里那一个片段 ⇒ 跨片段那一段会静默搬不到）。
func TestWithinSpansAllClipsOfTrack(t *testing.T) {
	// 片段 1：工程 [0, 4000)，音符在绝对 3840 起 4 个（源窗口 [3840,5760) 的前半）
	// 片段 2：工程 [4000, 12000)，音符在绝对 4800 起 4 个（源窗口的后半）
	// 目标：绝对 7680 起，同样的相对位置
	var called []string
	base := xwFake(t, &called, 0, nil, "sing", false)
	fake := func(ctx context.Context, args ...string) (*acecli.Result, error) {
		cmd := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(cmd, "clip list"):
			return okJSON(`{"clips":[
				{"clipType":"sing","clipName":"片段1","clipBegin":0,"clipEnd":4000,"clipUuid":"{c1}","noteCount":4},
				{"clipType":"sing","clipName":"片段2","clipBegin":4000,"clipEnd":12000,"clipUuid":"{c2}","noteCount":4}]}`), nil
		case strings.Contains(cmd, "clip note-content"):
			// 片段 1 的音符：片段内 3840 起（= 绝对 3840）
			if strings.Contains(cmd, "--clip-index 0") {
				return okJSON(xwNoteRows(runOf("a", 3840, 4))), nil
			}
			// 片段 2 的音符：片段内 800 起（= 绝对 4800）；目标那批在片段内 3680 起（= 绝对 7680）
			return okJSON(xwNoteRows(append(runOf("b", 800, 4), runOf("d", 3680, 4)...))), nil
		}
		return base(ctx, args...)
	}
	got, err := Get("xcopy.within").Plan(context.Background(), fake, map[string]string{
		"srcStart": "3", "srcEnd": "4", "dstStart": "5", "lyrics": "1", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep := got.(*xcopyReport)
	if rep.TrackClips != 2 {
		t.Errorf("作用域应当是整条轨的 2 个片段，实得 %d", rep.TrackClips)
	}
	// 源窗口 [3840,5760]：片段 1 的 4 个（绝对 3840..4560）+ 片段 2 的 4 个（绝对 4800..5520）⇒ 8 个
	// 目标窗口 [7680,9600]：片段 2 里的 4 个（绝对 7680..8400）
	pv := rep.Pairs[0]
	if pv.SrcNotes != 8 || pv.DstNotes != 4 {
		t.Errorf("跨片段的取数不对：源 %d 个（期望 8）、目标 %d 个（期望 4）", pv.SrcNotes, pv.DstNotes)
	}
	if rep.Matched != 4 {
		t.Errorf("相对位置能配上的应当是 4 个，实得 %d", rep.Matched)
	}
	if !strings.Contains(pv.SrcClip, "跨 2 个片段") {
		t.Errorf("报告要写清源窗口跨了几个片段，实得 %q", pv.SrcClip)
	}
}

// xwNoteRows 把音符列表拼成 `clip note-content` 的回包（测试用）。
func xwNoteRows(notes []xcopyNote) string {
	var rows []string
	for _, n := range notes {
		rows = append(rows, fmt.Sprintf(`{"noteUuid":%q,"pos":%d,"dur":%d,"endPos":%d,"pitch":%d,"lyric":"la","language":"Chinese","headConsonants":[],"tailConsonants":[]}`,
			n.NoteUUID, n.Pos, n.Dur, n.EndPos, n.Pitch))
	}
	return `{"noteCount":` + strconv.Itoa(len(notes)) + `,"notes":[` + strings.Join(rows, ",") + `]}`
}

// ② 配对：两段**一模一样**的音符 ⇒ 全部配上；错开半个音符 ⇒ 一个都配不上。
func TestWithinPairsByRelativePosition(t *testing.T) {
	// 源在第 3 小节（tick 3840）起 8 个；目标在第 5 小节（tick 7680）起 8 个 —— 相对位置相同
	notes := append(runOf("a", 3840, 8), runOf("b", 7680, 8)...)
	var called []string
	fake := xwFake(t, &called, 0, notes, "sing", false)
	got, err := Get("xcopy.within").Plan(context.Background(), fake, map[string]string{
		"srcStart": "3", "srcEnd": "4", "dstStart": "5",
		"lyrics": "1", "pitch": "1", "phonemes": "1", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep := got.(*xcopyReport)
	// 源 [3840, 5760]、目标 [7680, 9600]：两段各 8 个、相对位置一一对应 ⇒ 全配上
	if rep.Matched != 8 {
		t.Errorf("相对位置一致的段落应当配上 8 个，实得 matched=%d（源 %d / 目标 %d）",
			rep.Matched, rep.Pairs[0].SrcNotes, rep.Pairs[0].DstNotes)
	}
	// ⛔ 绝不新建音符：计划命令里不许出现 `note add`
	for _, c := range rep.Calls {
		if strings.Contains(c, "note add") {
			t.Errorf("这一支**只改已存在的音符**，不该出现新建命令：%s", c)
		}
	}
	// 画框用的精确 tick 要给出来（页面据此画两个色框）
	pv := rep.Pairs[0]
	if pv.SrcAbsTick != 3840 || pv.SrcEndAbs != 5760 || pv.DstAbsTick != 7680 || pv.DstEndAbs != 9600 {
		t.Errorf("画框的精确 tick 不对：%+v", pv)
	}

	// 把目标挪到"错开 120 tick"的位置 ⇒ 相对位置对不上 ⇒ 一个都配不上
	var called2 []string
	notes2 := append(runOf("a", 3840, 8), runOf("b", 7800, 8)...)
	fake2 := xwFake(t, &called2, 0, notes2, "sing", false)
	got2, err := Get("xcopy.within").Plan(context.Background(), fake2, map[string]string{
		"srcStart": "3", "srcEnd": "4", "dstStart": "5", "lyrics": "1", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	if m := got2.(*xcopyReport).Matched; m != 0 {
		t.Errorf("错开的位置不该配上任何音符，实得 %d", m)
	}
}

// ③ 小节号校验：结束 ≤ 开始要报错；一个属性都不勾也要报错（不静默做没事找事）。
func TestWithinRejectsBadBars(t *testing.T) {
	var called []string
	fake := xwFake(t, &called, 0, runOf("a", 3840, 4), "sing", false)
	if _, err := Get("xcopy.within").Plan(context.Background(), fake, map[string]string{
		"srcStart": "5", "srcEnd": "5", "dstStart": "6", "lyrics": "1", "dryRun": "1",
	}); err == nil || !strings.Contains(err.Error(), "必须大于") {
		t.Errorf("结束 ≤ 开始要报错，实得：%v", err)
	}
	if _, err := Get("xcopy.within").Plan(context.Background(), fake, map[string]string{
		"srcStart": "3", "srcEnd": "4", "dstStart": "6", "dryRun": "1",
	}); err == nil || !strings.Contains(err.Error(), "一个属性都没勾") {
		t.Errorf("属性全不勾要报错，实得：%v", err)
	}
	if _, err := Get("xcopy.within").Plan(context.Background(), fake, map[string]string{
		"srcStart": "0", "srcEnd": "4", "dstStart": "6", "lyrics": "1", "dryRun": "1",
	}); err == nil || !strings.Contains(err.Error(), "从 **1** 起") {
		t.Errorf("小节号 0 要报错，实得：%v", err)
	}
}

// ④ 非 sing 片段：歌词/音素只存在于 sing ⇒ 跳过它们、**音高照做**（如实报在 skipped 里）。
func TestWithinSkipsLyricsOnNonSingClip(t *testing.T) {
	src := runOf("a", 3840, 4)
	dst := runOf("b", 7680, 4)
	for i := range dst {
		dst[i].Pitch = 64 // 目标音高与源不同 ⇒ 计划里应当出现 `note move --pitch`
	}
	notes := append(src, dst...)
	var called []string
	fake := xwFake(t, &called, 0, notes, "instrument", false)
	got, err := Get("xcopy.within").Plan(context.Background(), fake, map[string]string{
		"srcStart": "3", "srcEnd": "4", "dstStart": "5",
		"lyrics": "1", "pitch": "1", "phonemes": "1", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep := got.(*xcopyReport)
	joined := strings.Join(rep.Skipped, " | ")
	// ⭐ 作用域是整条轨之后，判据变成"**这个音符落在哪个片段**里"（不再是"当前片段整体是什么类型"）
	if !strings.Contains(joined, "非 sing 片段") || !strings.Contains(joined, "歌词 / 音素 / 辅音时长") {
		t.Errorf("非 sing 片段要把「跳过歌词/音素/辅音时长」写进 skipped，实得：%v", rep.Skipped)
	}
	// 音高仍然要计划写（`note move --pitch`）
	var hasPitch bool
	for _, c := range rep.Calls {
		if strings.Contains(c, "note move") && strings.Contains(c, "--pitch") {
			hasPitch = true
		}
	}
	if !hasPitch {
		t.Errorf("音高应当照做，计划命令里没看到 note move --pitch：%v", rep.Calls)
	}
}

// ---------- 「除 uuid 外全都要」：时值 / 辅音时长 / 参数曲线 / dynamic ----------
//
// 用户 2026-10-07 的口径：复制内容分两类 —— ① 音符属性（歌词·语种·音高·音素·**时值**·**辅音时长**）
// ② 参数（**所有可用参数**，源读实参、写目标可写层；`dynamic` 是锚点形状、单独一条路）。

// xwFullFake 在 xwFake 的基础上再给：`phoneme list --with-timings`（逐音素时长）、
// `vocalparam layers`（v2 花名册：四个 model 参数只有 user）、`vocalparam read`
// （实参 / user 层 / dynamic 的锚点）。
//
// `overridden` = 源音符的发音是不是**手动改过**的（`isOverride`）—— 用户 2026-10-07 的口径是
// 「只复制手动更改过的」，所以它决定音素那一项该不该写。
// `pinned` = v2 上"辅音时长被手动改过"的判据（pin）。
func xwFullFake(t *testing.T, called *[]string, notes []xcopyNote, dynAnchors string, overridden, pinned bool) ReadFunc {
	t.Helper()
	base := xwFake(t, called, 0, notes, "sing", true)
	return func(ctx context.Context, args ...string) (*acecli.Result, error) {
		cmd := strings.Join(args, " ")
		*called = append(*called, cmd)
		switch {
		case strings.HasPrefix(cmd, "phoneme list") && strings.Contains(cmd, "--with-timings=durations"):
			// ⚠️ `--note` 后面可以跟**多个** uuid（官方语法就是 `--note <UUID>...`）⇒ 要把它们全取出来，
			// 只取第一个会让其余音符"没读出音素"（第一版就是这个 bug，表现为"记 1 个"）。
			var uuids []string
			for i, a := range args {
				if a != "--note" {
					continue
				}
				for j := i + 1; j < len(args) && !strings.HasPrefix(args[j], "--"); j++ {
					uuids = append(uuids, args[j])
				}
			}
			var rows []string
			for k, u := range uuids {
				// 源音符（`{a…}`）才吃 `overridden`；目标音符（`{b…}`）保持默认 ⇒ 两边**不同**才有的写
				ovr := overridden && strings.HasPrefix(u, "{a")
				ph := `["l","a"]`
				if ovr {
					ph = `["l","i"]`
				}
				// ⚠️ 源与目标的辅音时长也要**不一样**（否则按"本来就一样"跳过，测不到写入那一步）
				dur := 0.05 + 0.01*float64(k)
				if strings.HasPrefix(u, "{b") {
					dur = 0.09 + 0.01*float64(k)
				}
				rows = append(rows, fmt.Sprintf(`{"noteUuid":%q,"phonemes":%s,"defaultPhonemes":["l","a"],"isOverride":%v,"language":"Chinese",
					"timings":[{"index":0,"name":"l","isVowel":false,"durationSec":%g,"isPinned":%v},{"index":1,"name":"a","isVowel":true,"durationSec":0.1}]}`,
					u, ph, ovr, dur, pinned))
			}
			return okJSON(`{"modelGeneration":"v2","notes":[` + strings.Join(rows, ",") + `]}`), nil
		case strings.HasPrefix(cmd, "phoneme list"):
			// 不带 `--with-timings` 的那条（音素复制用的）：同样要把**所有** uuid 都回出来，
			// 而且源（`{a…}`）按 `overridden` 给"手动改过"的样子 —— 第一版这里永远回 isOverride:false。
			var uuids []string
			for i, a := range args {
				if a != "--note" {
					continue
				}
				for j := i + 1; j < len(args) && !strings.HasPrefix(args[j], "--"); j++ {
					uuids = append(uuids, args[j])
				}
			}
			var rows []string
			for _, u := range uuids {
				ovr := overridden && strings.HasPrefix(u, "{a")
				ph, def := `["l","a"]`, `["l","a"]`
				if ovr {
					ph = `["l","i"]`
				}
				rows = append(rows, fmt.Sprintf(`{"noteUuid":%q,"phonemes":%s,"defaultPhonemes":%s,"isOverride":%v,"language":"Chinese"}`,
					u, ph, def, ovr))
			}
			return okJSON(`{"modelGeneration":"v2","notes":[` + strings.Join(rows, ",") + `]}`), nil
		case strings.HasPrefix(cmd, "vocalparam layers"):
			return okJSON(`{"engineGeneration":"singing-mamba","vocalControlRoute":"dynamic","params":[
				{"param":"dynamic","displayName":"Dynamic","available":true,"scale":"control",
				 "valueRange":{"min":-1,"max":1},
				 "layers":[{"layer":"user","access":"read-write","role":"override","shape":"points","sparse":true}]},
				{"param":"tension","displayName":"Tension","available":true,"scale":"model","valueRange":{"min":0,"max":1},
				 "layers":[{"layer":"baseline","access":"read-only","shape":"dense"},
				           {"layer":"user","access":"read-write","role":"override","shape":"dense","sparse":true}]},
				{"param":"energy","displayName":"Energy","available":true,"scale":"model","valueRange":{"min":0,"max":1},
				 "layers":[{"layer":"baseline","access":"read-only","shape":"dense"},
				           {"layer":"user","access":"read-write","role":"override","shape":"dense","sparse":true}]},
				{"param":"pitch","displayName":"Pitch","available":false,"scale":"semitones"}]}`), nil
		case strings.HasPrefix(cmd, "vocalparam read") && strings.Contains(cmd, "--param dynamic"):
			return okJSON(`{"posBegin":0,"fingerprint":"fp-dyn","layers":[{"layer":"user","shape":"points","access":"read-write",
				"points":` + dynAnchors + `}]}`), nil
		case strings.HasPrefix(cmd, "vocalparam read") && strings.Contains(cmd, "--layer effective"):
			// 实参：一段递增的密曲线（值域 0..1 内）。
			// ⚠️ 必须**按请求的范围**给长度（`--range-begin/--range-end`）：轨内那支现在按**绝对窗口**
			// 逐片段读，源窗口与目标窗口不是同一段，永远回 1920 个值会让长度校验直接失败。
			lo, hi := xwRange(args)
			var vals []string
			for i := lo; i < hi; i++ {
				vals = append(vals, fmt.Sprintf("%.3f", float64(i%10)/10))
			}
			return okJSON(fmt.Sprintf(`{"posBegin":%d,"count":%d,"fingerprint":"fp-eff","effective":{"layer":"effective","shape":"dense","points":[%s]}}`,
				lo, hi-lo, strings.Join(vals, ","))), nil
		case strings.HasPrefix(cmd, "vocalparam read"):
			lo, hi := xwRange(args)
			var vals []string
			for i := lo; i < hi; i++ {
				vals = append(vals, "null") // 目标层本来没画过 ⇒ 报告里要说清"会变成显式覆盖"
			}
			return okJSON(fmt.Sprintf(`{"posBegin":%d,"count":%d,"fingerprint":"fp-user","layers":[{"layer":"user","access":"read-write","shape":"dense","sparse":true,"points":[%s]}]}`,
				lo, hi-lo, strings.Join(vals, ","))), nil
		}
		return base(ctx, args...)
	}
}

// xwRange 取 `vocalparam read` 请求的 tick 范围（假 CLI 按它给长度；没给就按 0..1920）。
func xwRange(args []string) (int64, int64) {
	lo, hi := int64(0), int64(1920)
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--range-begin":
			lo, _ = strconv.ParseInt(args[i+1], 10, 64)
		case "--range-end":
			hi, _ = strconv.ParseInt(args[i+1], 10, 64)
		}
	}
	if hi <= lo {
		hi = lo + 1
	}
	return lo, hi
}

// 时值 + 辅音时长：源与目标不同才写；同长/同长辅音不写。
func TestWithinCopiesDurAndConsonants(t *testing.T) {
	src := runOf("a", 3840, 3) // 源：dur 240
	dst := runOf("b", 7680, 3) // 目标：dur 240 → 改成 480 才有的写
	for i := range dst {
		dst[i].Dur, dst[i].EndPos = 480, dst[i].Pos+480
	}
	notes := append(src, dst...)
	// ⛔ 用户口径「默认的辅音时长不复制」：源要**标成手动改过**（v1 靠音符上的长度数组、v2 靠 pin）
	for i := range src {
		src[i].HeadConsLen = []float64{0.05}
	}
	notes = append(src, dst...)
	var called []string
	fake := xwFullFake(t, &called, notes, "[]", false, true)
	got, err := Get("xcopy.within").Plan(context.Background(), fake, map[string]string{
		"srcStart": "3", "srcEnd": "4", "dstStart": "5",
		"dur": "1", "consonants": "1", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep := got.(*xcopyReport)
	if rep.WillResize != 3 {
		t.Errorf("三个目标音符的时值都要改（480→240），实得 %d", rep.WillResize)
	}
	var resize, cons int
	for _, c := range rep.Calls {
		if strings.Contains(c, "note resize") && strings.Contains(c, "--dur 240t") {
			resize++
		}
		if strings.Contains(c, "phoneme set-consonant-timing") {
			cons++
		}
	}
	if resize != 1 {
		t.Errorf("同一目标时长应当合成**一条** note resize，实得 %d 条", resize)
	}
	if cons == 0 || rep.WillConsonant == 0 {
		t.Errorf("辅音时长应当有计划（源/目标时长不同），实得 WillConsonant=%d calls=%v", rep.WillConsonant, rep.Calls)
	}
	// 长度按**秒**发（不是 tick）—— 免得把"146 BPM 下的 0.05s"错当 tick 抄过去
	for _, c := range rep.Calls {
		if strings.Contains(c, "set-consonant-timing") && !strings.Contains(c, "s   （") {
			t.Errorf("辅音长度要带秒单位：%s", c)
		}
	}

	// ⛔ 反过来：**没标成手动改过**的（既没有 head/tail 长度、也没有 pin）⇒ 一处都不搬、如实计数
	var called2 []string
	fake2 := xwFullFake(t, &called2, append(runOf("a", 3840, 3), runOf("b", 7680, 3)...), "[]", false, false)
	got2, err := Get("xcopy.within").Plan(context.Background(), fake2, map[string]string{
		"srcStart": "3", "srcEnd": "4", "dstStart": "5", "consonants": "1", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep2 := got2.(*xcopyReport)
	if rep2.WillConsonant != 0 {
		t.Errorf("默认的辅音时长一个都不该搬，实得 %d", rep2.WillConsonant)
	}
	if rep2.SkippedDefaultCons != 3 {
		t.Errorf("三个配对音符都该被记为「还是默认」⇒ 3，实得 %d", rep2.SkippedDefaultCons)
	}
	for _, c := range rep2.Calls {
		if strings.Contains(c, "set-consonant-timing") {
			t.Errorf("默认的辅音时长不该出现写命令：%s", c)
		}
	}
}

// ⛔ 音素同理：**只搬手动改过的**（`isOverride`）—— 默认发音一个都不写、只计数。
func TestWithinCopiesOnlyOverriddenPhonemes(t *testing.T) {
	notes := append(runOf("a", 3840, 3), runOf("b", 7680, 3)...)
	// 源**没**手动改过 ⇒ 音素不该写
	var called []string
	fake := xwFullFake(t, &called, notes, "[]", false, false)
	got, err := Get("xcopy.within").Plan(context.Background(), fake, map[string]string{
		"srcStart": "3", "srcEnd": "4", "dstStart": "5", "phonemes": "1", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep := got.(*xcopyReport)
	if rep.WillPhonemes != 0 || rep.SkippedDefaultPhonemes != 3 {
		t.Errorf("默认发音：应当一处都不写、记 3 个「还是默认」，实得写 %d / 记 %d",
			rep.WillPhonemes, rep.SkippedDefaultPhonemes)
	}
	for _, c := range rep.Calls {
		if strings.Contains(c, "phoneme set ") {
			t.Errorf("默认发音不该出现 phoneme set：%s", c)
		}
	}
	// 源**手动改过**（phonemes 与 defaultPhonemes 不同）⇒ 该写
	var called2 []string
	fake2 := xwFullFake(t, &called2, notes, "[]", true, false)
	got2, err := Get("xcopy.within").Plan(context.Background(), fake2, map[string]string{
		"srcStart": "3", "srcEnd": "4", "dstStart": "5", "phonemes": "1", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep2 := got2.(*xcopyReport)
	if rep2.WillPhonemes != 3 {
		t.Errorf("手动改过的发音应当写 3 个，实得 %d", rep2.WillPhonemes)
	}
}

// 参数曲线：源读实参 ⇒ 按花名册写目标可写层（v2 只有 user）；pitch 不可用、dynamic 走另一条路。
func TestWithinCopiesParamLayers(t *testing.T) {
	notes := append(runOf("a", 3840, 3), runOf("b", 7680, 3)...)
	var called []string
	fake := xwFullFake(t, &called, notes, "[]", false, false)
	got, err := Get("xcopy.within").Plan(context.Background(), fake, map[string]string{
		"srcStart": "3", "srcEnd": "4", "dstStart": "5", "params": "1", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep := got.(*xcopyReport)
	// v2 花名册里能写的只有 tension / energy 两个（各一层 user）；pitch 不可用、dynamic 不在这一路
	if rep.WillParamLayers != 2 {
		t.Errorf("应当写 2 层（tension/energy 的 user），实得 %d", rep.WillParamLayers)
	}
	if rep.ParamTicks != 2*1920 {
		t.Errorf("两层各 1920 tick ⇒ 3840，实得 %d", rep.ParamTicks)
	}
	var writes int
	for _, c := range rep.Calls {
		if strings.Contains(c, "vocalparam write") && strings.Contains(c, "--layer user") {
			writes++
		}
		if strings.Contains(c, "--param pitch") {
			t.Errorf("pitch 在这个 surface 上不可用，不该出现：%s", c)
		}
		if strings.Contains(c, "--param dynamic") {
			t.Errorf("dynamic 是锚点形状、不该走 dense 那条路：%s", c)
		}
	}
	if writes != 2 {
		t.Errorf("应当计划 2 条 vocalparam write，实得 %d", writes)
	}
}

// 指定 `paramLayers=envelope` 而这份 clip 是 v2（只有 user）⇒ **如实跳过**，不硬写。
func TestWithinParamLayerMissingIsSkipped(t *testing.T) {
	notes := append(runOf("a", 3840, 3), runOf("b", 7680, 3)...)
	var called []string
	fake := xwFullFake(t, &called, notes, "[]", false, false)
	got, err := Get("xcopy.within").Plan(context.Background(), fake, map[string]string{
		"srcStart": "3", "srcEnd": "4", "dstStart": "5", "params": "1", "paramLayers": "envelope", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep := got.(*xcopyReport)
	if rep.WillParamLayers != 0 {
		t.Errorf("v2 上没有 envelope 层 ⇒ 一层都不该写，实得 %d", rep.WillParamLayers)
	}
	if joined := strings.Join(rep.Skipped, " | "); !strings.Contains(joined, "没有可写的 envelope 层") {
		t.Errorf("要如实说「这一层没有」，实得：%v", rep.Skipped)
	}
}

// dynamic：读锚点 → **平移**（delta = 目标起点 - 源起点）→ 写回；源没有锚点就如实说"不写"。
func TestWithinCopiesDynamicAnchors(t *testing.T) {
	notes := append(runOf("a", 3840, 3), runOf("b", 7680, 3)...)
	var called []string
	fake := xwFullFake(t, &called, notes, `[[3900,-0.5],[4200,0.8]]`, false, false)
	got, err := Get("xcopy.within").Plan(context.Background(), fake, map[string]string{
		"srcStart": "3", "srcEnd": "4", "dstStart": "5", "dynamic": "1", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep := got.(*xcopyReport)
	if rep.WillDynamic != 2 {
		t.Fatalf("应当搬 2 个锚点，实得 %d", rep.WillDynamic)
	}
	var line string
	for _, c := range rep.Calls {
		if strings.Contains(c, "--param dynamic") {
			line = c
		}
	}
	if line == "" || !strings.Contains(line, "平移 +3840 tick") {
		t.Errorf("dynamic 那条计划要写明平移量（+3840 tick），实得 %q", line)
	}

	// 源没有锚点 ⇒ 一条命令都不发，并说清"也不会去清目标那段"
	var called2 []string
	fake2 := xwFullFake(t, &called2, notes, "[]", false, false)
	got2, err := Get("xcopy.within").Plan(context.Background(), fake2, map[string]string{
		"srcStart": "3", "srcEnd": "4", "dstStart": "5", "dynamic": "1", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep2 := got2.(*xcopyReport)
	if rep2.WillDynamic != 0 {
		t.Errorf("源没锚点就不该写，实得 %d", rep2.WillDynamic)
	}
	for _, c := range rep2.Calls {
		if strings.Contains(c, "--param dynamic") {
			t.Errorf("源没锚点时不该出现 dynamic 写命令：%s", c)
		}
	}
	if joined := strings.Join(rep2.Skipped, " | "); !strings.Contains(joined, "没有 dynamic 锚点") {
		t.Errorf("要说清为什么没写，实得：%v", rep2.Skipped)
	}
}
func TestXcopyViewBundle(t *testing.T) {
	notes := runOf("a", 3840, 6)
	var called []string
	fake := xwFake(t, &called, 0, notes, "sing", false)
	got, err := Get("xcopy.view").Inspect(context.Background(), fake, map[string]string{})
	if err != nil {
		t.Fatalf("xcopy.view 不该失败：%v", err)
	}
	v, ok := got.(*xcopyViewReport)
	if !ok {
		t.Fatalf("应当回 *xcopyViewReport，实得 %T", got)
	}
	if v.TrackIndex != 0 || v.ClipUUID != "{c1}" || v.ClipTicks != 15360 {
		t.Errorf("轨/片段信息不对：%+v", v)
	}
	if v.TicksPerBar != 1920 || v.Meter != "4/4" {
		t.Errorf("4/4 应当算出 1920 tick/小节，实得 tpb=%d meter=%q", v.TicksPerBar, v.Meter)
	}
	if v.ClipNoteCount != 6 {
		t.Errorf("片段音符数应当是 6，实得 %d", v.ClipNoteCount)
	}
	if len(v.Clips) != 1 || len(v.Clips[0].Notes) != 6 {
		t.Errorf("缩略图数据（这条轨的片段+音符）不对：%d 个片段", len(v.Clips))
	}
}

// ---------- 整条轨（多片段、绝对时间）的参数那一半 ----------
//
// ⭐ 用户 2026-10-07：「`xcopy.within` 作用于轨内所有 clip，遍历绝对时间」。
// 参数曲线是**按 tick 的密曲线**、而且 `vocalparam write` 是"替换这一段"⇒ 多片段时要做两件事：
//   ① 源窗口横跨几个片段 ⇒ 把各片段的取值**拼成一条按绝对 tick 索引的缓冲**；
//   ② 目标窗口横跨几个片段（或源侧有空洞）⇒ **逐段**写，空洞处**不写**（保留目标原值）。

// xtScope 造一条"两个片段"的轨：`{c1}` = [0,secondBegin)、`{c2}` = [secondBegin,12000)。
func xtScope(secondBegin int64) *xcopyTrackScope {
	return &xcopyTrackScope{
		TrackIndex: 0, TrackName: "测试轨",
		Clips: []trackClip{
			{clipBrief: clipBrief{Index: 0, UUID: "{c1}", Name: "片段1", Type: "sing", Begin: 0, End: secondBegin}},
			{clipBrief: clipBrief{Index: 1, UUID: "{c2}", Name: "片段2", Type: "sing", Begin: secondBegin, End: 12000}},
		},
		NoteClip: map[string]int{},
	}
}

// xtParamFake：v2 花名册（tension 只有 `user`），`effective` 按**片段**给不同的常数
// （`{c1}` ⇒ 0.25、`{c2}` ⇒ 0.75）—— 缓冲拼错了立刻能看出来。
func xtParamFake(t *testing.T, called *[]string) ReadFunc {
	t.Helper()
	return func(_ context.Context, args ...string) (*acecli.Result, error) {
		cmd := strings.Join(args, " ")
		*called = append(*called, cmd)
		clip := ""
		for i, a := range args {
			if a == "--clip-uuid" && i+1 < len(args) {
				clip = args[i+1]
			}
		}
		switch {
		case strings.HasPrefix(cmd, "vocalparam layers"):
			return okJSON(`{"engineGeneration":"singing-mamba","params":[
				{"param":"tension","displayName":"Tension","available":true,"scale":"model","valueRange":{"min":0,"max":1},
				 "layers":[{"layer":"baseline","access":"read-only","shape":"dense"},
				           {"layer":"user","access":"read-write","role":"override","shape":"dense","sparse":true}]}]}`), nil
		case strings.HasPrefix(cmd, "vocalparam read"):
			lo, hi := xwRange(args)
			val := "0.250"
			if clip == "{c2}" {
				val = "0.750"
			}
			var vals []string
			for i := lo; i < hi; i++ {
				vals = append(vals, val)
			}
			pts := strings.Join(vals, ",")
			if strings.Contains(cmd, "--layer effective") {
				return okJSON(fmt.Sprintf(`{"posBegin":%d,"count":%d,"fingerprint":"fp-eff-%s","effective":{"layer":"effective","shape":"dense","points":[%s]}}`,
					lo, hi-lo, shortID(clip), pts)), nil
			}
			return okJSON(fmt.Sprintf(`{"posBegin":%d,"count":%d,"fingerprint":"fp-user-%s","layers":[{"layer":"user","access":"read-write","shape":"dense","sparse":true,"points":[%s]}]}`,
				lo, hi-lo, shortID(clip), pts)), nil
		}
		return nil, fmt.Errorf("参数假 CLI 没准备这条命令：%s", cmd)
	}
}

// ⭐ 源窗口横跨两个片段 ⇒ 缓冲要**拼**起来；目标窗口落在一个片段里 ⇒ 一笔写下去。
func TestTrackParamsStitchAcrossClips(t *testing.T) {
	var called []string
	fake := xtParamFake(t, &called)
	tc := xtScope(4000)
	pd, rep := &xcopyPlanData{}, &xcopyReport{}
	// 源 [3840,5760)（c1 出 160 tick、c2 出 1760 tick）→ 目标 [7680,9600)（全在 c2 里）
	if err := planTrackParams(context.Background(), fake, map[string]string{"paramSel": `{"tension":["effective"]}`},
		tc, 3840, 5760, 7680, true, false, pd, rep); err != nil {
		t.Fatalf("参数计划不该失败：%v", err)
	}
	if len(pd.paramWrites) != 1 {
		t.Fatalf("目标窗口只落在一个片段里 ⇒ 应当 1 笔写，实得 %d 笔（%+v / %v）", len(pd.paramWrites), pd.paramWrites, pd.paramSkips)
	}
	w := pd.paramWrites[0]
	if w.ClipUUID != "{c2}" || w.Layer != "user" || w.Ticks != 1920 || w.PosBegin != 3680 {
		t.Errorf("落点不对：%+v（期望 {c2} / user / 1920 tick / posBegin 3680）", w)
	}
	if w.IfMatch != "fp-user-c2" {
		t.Errorf("指纹要来自**目标**片段那一层，实得 %q", w.IfMatch)
	}
	// 拼接：前 160 tick 来自 c1（0.25）、之后来自 c2（0.75）
	if *w.Payload[0] != 0.25 || *w.Payload[159] != 0.25 || *w.Payload[160] != 0.75 || *w.Payload[1919] != 0.75 {
		t.Errorf("跨片段的缓冲没拼对：%v/%v/%v/%v", *w.Payload[0], *w.Payload[159], *w.Payload[160], *w.Payload[1919])
	}
	if rep.WillParamLayers != 1 || rep.ParamTicks != 1920 {
		t.Errorf("账不对：%d 层 / %d tick", rep.WillParamLayers, rep.ParamTicks)
	}
}

// ⭐ 源侧两个片段之间**有空档**（不是"lane 没画"、是"那儿根本没有片段"）⇒
// 空洞处**不写**（保留目标原值），两侧各切一段写 —— 并如实报出空洞有多少 tick。
func TestTrackParamsSkipsStructuralHoles(t *testing.T) {
	var called []string
	fake := xtParamFake(t, &called)
	tc := xtScope(6000)    // c2 从 6000 起
	tc.Clips[0].End = 4000 // ……但 c1 只到 4000 ⇒ 源侧 [4000,6000) 是**空洞**
	pd, rep := &xcopyPlanData{}, &xcopyReport{}
	// 源 [3840,7000)（c1 出 160 tick、空洞 2000 tick、c2 出 1000 tick）→ 目标 [7680,10840) 全在 c2 里
	if err := planTrackParams(context.Background(), fake, map[string]string{"paramSel": `{"tension":["effective"]}`},
		tc, 3840, 7000, 7680, true, false, pd, rep); err != nil {
		t.Fatalf("参数计划不该失败：%v", err)
	}
	if len(pd.paramWrites) != 2 {
		t.Fatalf("空洞两侧应当各写一段（共 2 笔），实得 %d 笔；跳过说明：%v", len(pd.paramWrites), pd.paramSkips)
	}
	if pd.paramWrites[0].PosBegin != 1680 || pd.paramWrites[0].Ticks != 160 {
		t.Errorf("第一段应当是 posBegin 1680（= 7680-6000）/ 160 tick，实得 %+v", pd.paramWrites[0])
	}
	if pd.paramWrites[1].PosBegin != 3840 || pd.paramWrites[1].Ticks != 1000 {
		t.Errorf("第二段应当是 posBegin 3840（= 9840-6000）/ 1000 tick，实得 %+v", pd.paramWrites[1])
	}
	joined := strings.Join(pd.paramSkips, " | ")
	if !strings.Contains(joined, "源侧没有片段覆盖") || !strings.Contains(joined, "2000 个 tick") {
		t.Errorf("要如实报出空洞（2000 tick）且说清不写、保留目标原值，实得：%v", pd.paramSkips)
	}
	// 空洞那一段（目标绝对 tick 7840..9840）不该出现在任何一笔写入里
	for _, w := range pd.paramWrites {
		abs := w.PosBegin + 6000
		if abs >= 7840 && abs < 9840 {
			t.Errorf("空洞那一段不该被写：%+v", w)
		}
	}
}

// ⭐ dynamic 锚点：源侧各片段的锚点 → 换成绝对 tick → 平移 → **按目标片段分成几条写**；
// `--if-match` 必须读**目标**那一份的指纹（旧版用的是源的指纹 —— 同片段时代看不出来）。
func TestTrackDynamicAnchorsAcrossClips(t *testing.T) {
	var called []string
	fake := func(_ context.Context, args ...string) (*acecli.Result, error) {
		cmd := strings.Join(args, " ")
		called = append(called, cmd)
		clip := ""
		for i, a := range args {
			if a == "--clip-uuid" && i+1 < len(args) {
				clip = args[i+1]
			}
		}
		switch {
		case strings.HasPrefix(cmd, "vocalparam layers"):
			return okJSON(`{"engineGeneration":"singing-mamba","vocalControlRoute":"dynamic","params":[
				{"param":"dynamic","displayName":"Dynamic","available":true,"scale":"control","valueRange":{"min":-1,"max":1},
				 "layers":[{"layer":"user","access":"read-write","role":"override","shape":"points","sparse":true}]}]}`), nil
		case strings.HasPrefix(cmd, "vocalparam read"):
			if clip == "{c1}" {
				return okJSON(`{"posBegin":0,"fingerprint":"fp-dyn-c1","layers":[{"layer":"user","shape":"points","access":"read-write","points":[[3850,-0.5]]}]}`), nil
			}
			// c2：两个锚点（片段内 100 / 500 ⇒ 绝对 4100 / 4500）。
			// ⚠️ 目标那条指纹也走这里（`planTrackParams` 会再读一次目标片段）⇒ 同一个回包正好。
			return okJSON(`{"posBegin":0,"fingerprint":"fp-dyn-c2","layers":[{"layer":"user","shape":"points","access":"read-write","points":[[100,0.5],[500,0.2]]}]}`), nil
		}
		return nil, fmt.Errorf("dynamic 假 CLI 没准备这条命令：%s", cmd)
	}
	tc := xtScope(4000)
	pd, rep := &xcopyPlanData{}, &xcopyReport{}
	// 源 [3840,5760) 的锚点：c1 的 3850、c2 的 4000+100=4100 与 4000+500=4500
	// 平移 delta = 7680-3840 = 3840 ⇒ 7690 / 7940 / 8340，全落进 c2（[4000,12000)）
	if err := planTrackParams(context.Background(), fake, map[string]string{}, tc, 3840, 5760, 7680,
		false, true, pd, rep); err != nil {
		t.Fatalf("dynamic 计划不该失败：%v", err)
	}
	if len(pd.dynWrites) != 1 {
		t.Fatalf("三个锚点都落进同一个目标片段 ⇒ 应当 1 条写，实得 %d 条（%v）", len(pd.dynWrites), pd.paramSkips)
	}
	w := pd.dynWrites[0]
	if w.ClipUUID != "{c2}" || w.IfMatch != "fp-dyn-c2" || w.Delta != 3840 || len(w.Anchors) != 3 {
		t.Fatalf("dynamic 那条写不对：%+v", w)
	}
	// 锚点要换成**目标片段内**的 tick：7690-4000=3690、7940-4000=3940、8340-4000=4340
	for i, want := range []int64{3690, 3940, 4340} {
		if got, _ := w.Anchors[i][0].(int64); got != want {
			t.Errorf("第 %d 个锚点的 tick 应当是 %d，实得 %v", i+1, want, w.Anchors[i][0])
		}
	}
	if rep.WillDynamic != 3 {
		t.Errorf("应当报 3 个锚点，实得 %d", rep.WillDynamic)
	}
}

// ---------- 参数表格（用户 2026-10-07：「参数展开…v1 就实参和包络两列」） ----------

// xwV1Fake 是 v1 引擎（`verse24`）那一侧的**花名册 + 曲线**：
// tension 有 envelope + direct 两个可写层，formant 只有 envelope（ACE 的 v1 事实）。
// 实参 / 包络给**不同的值**，才测得出"两列读的是两条不同的源曲线"。
func xwV1Fake(t *testing.T, called *[]string) ReadFunc {
	t.Helper()
	return func(ctx context.Context, args ...string) (*acecli.Result, error) {
		cmd := strings.Join(args, " ")
		*called = append(*called, cmd)
		switch {
		case strings.HasPrefix(cmd, "vocalparam layers"):
			return okJSON(`{"engineGeneration":"verse24","vocalControlRoute":"envelope","params":[
				{"param":"tension","displayName":"Tension","available":true,"scale":"model","valueRange":{"min":0,"max":1},
				 "layers":[{"layer":"baseline","access":"read-only","shape":"dense"},
				           {"layer":"direct","access":"read-write","role":"override","shape":"dense","sparse":true},
				           {"layer":"envelope","access":"read-write","role":"multiplier","shape":"dense","sparse":true}]},
				{"param":"formant","displayName":"Formant","available":true,"scale":"model","valueRange":{"min":0.2,"max":2.5},
				 "layers":[{"layer":"envelope","access":"read-write","role":"multiplier","shape":"dense","sparse":true}]},
				{"param":"pitch","displayName":"Pitch","available":false,"scale":"semitones"}]}`), nil
		case strings.HasPrefix(cmd, "vocalparam read"):
			layer, fp, base, step := v1FlagVal(args, "--layer"), "fp-dir", 0.0, 0.0
			// ⭐ 2026-10-20：实参列改成"用户层当掩码 + effective 当值" ⇒ 源侧 `direct` 必须有值，
			// 目标侧 `direct` 保持全空档（"目标本来没画过"）。
			isDst := v1FlagVal(args, "--clip-uuid") == "{d}"
			switch layer {
			case "effective":
				fp, base, step = "fp-eff", 0.1, 0.01
			case "envelope":
				fp, base, step = "fp-env", 1.0, 0.01
			case "direct":
				fp = "fp-dir"
			}
			var vals []string
			for i := 0; i < 1920; i++ {
				if layer == "direct" && isDst {
					vals = append(vals, "null")
					continue
				}
				vals = append(vals, fmt.Sprintf("%.3f", base+step*float64(i%10)))
			}
			pts := `[` + strings.Join(vals, ",") + `]`
			if layer == "effective" {
				return okJSON(`{"posBegin":0,"count":1920,"fingerprint":"` + fp + `",
					"effective":{"layer":"effective","shape":"dense","points":` + pts + `}}`), nil
			}
			return okJSON(`{"posBegin":0,"count":1920,"fingerprint":"` + fp + `","layers":[{"layer":"` + layer +
				`","access":"read-write","shape":"dense","sparse":true,"points":` + pts + `}]}`), nil
		}
		return nil, fmt.Errorf("v1 假引擎没实现：%s", cmd)
	}
}

func v1FlagVal(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// ⭐ 核心：v1 上"实参"列落 `direct`、"包络"列落 `envelope`，而且两列读的是**两条不同的源曲线**。
func TestParamSelTableV1Columns(t *testing.T) {
	var called []string
	fake := xwV1Fake(t, &called)
	vals := map[string]string{"paramSel": `{"tension":["effective","envelope"],"formant":["envelope"]}`}
	pd, rep := &xcopyPlanData{}, &xcopyReport{}
	// 源 [0,1920) → 目标 [3840,5760)（都是片段内 tick）
	if err := planClipParams(context.Background(), fake, vals, "{s}", "{d}", 0, 1920, 3840, 5760, true, false, pd, rep); err != nil {
		t.Fatalf("计划不该失败：%v", err)
	}
	// ⭐ 2026-10-20：两列都勾 ⇒ 3 格都写（实参格改读**原始 direct 值**，包络单独一格）⇒ 不乘二
	if len(pd.paramWrites) != 3 {
		t.Fatalf("应当 3 格（tension 实参+包络、formant 包络），实得 %d：%+v", len(pd.paramWrites), pd.paramSkips)
	}
	byCell := map[string]xcopyParamWrite{}
	for _, w := range pd.paramWrites {
		byCell[w.Param+"/"+w.Layer] = w
	}
	for _, cell := range []string{"tension/direct", "tension/envelope", "formant/envelope"} {
		if _, ok := byCell[cell]; !ok {
			t.Fatalf("少了 %s 这一格；实得 %v（跳过说明：%v）", cell, byCell, pd.paramSkips)
		}
	}
	// 实参格必须搬**原始 direct 值**（假 CLI 里 direct=0.0、effective=0.1…）⇒ 才不会被包络乘二
	if got := *byCell["tension/direct"].Payload[0]; got > 0.001 {
		t.Errorf("两列都勾时实参格该读原始 direct（≈0.0），实得 %v（说明它读成了 effective）", got)
	}
	if got := byCell["tension/direct"].IfMatch; got != "fp-dir" {
		t.Errorf("实参列该写 direct 层（指纹 fp-dir），实得 %q", got)
	}
	if got := *byCell["formant/envelope"].Payload[0]; got < 0.99 || got > 1.01 {
		t.Errorf("包络列的载荷该来自 envelope 层（≈1.0），实得 %v", got)
	}
	if got := byCell["tension/direct"].PosBegin; got != 3840 {
		t.Errorf("目标起点该是 3840，实得 %d", got)
	}
	if rep.WillParamLayers != 3 || rep.ParamTicks != 3*1920 {
		t.Errorf("账不对：%d 层 / %d tick", rep.WillParamLayers, rep.ParamTicks)
	}
}

// 只有 envelope 可写的参数（v1 的 `formant`）勾"实参"列 ⇒ **如实跳过**（实参是绝对值，落不进乘数层）。
func TestParamSelEffectiveCellNeedsDirectOrUser(t *testing.T) {
	var called []string
	fake := xwV1Fake(t, &called)
	vals := map[string]string{"paramSel": `{"formant":["effective"]}`}
	pd, rep := &xcopyPlanData{}, &xcopyReport{}
	if err := planClipParams(context.Background(), fake, vals, "{s}", "{d}", 0, 1920, 3840, 5760, true, false, pd, rep); err != nil {
		t.Fatalf("计划不该失败：%v", err)
	}
	if len(pd.paramWrites) != 0 || rep.WillParamLayers != 0 {
		t.Errorf("formant 只有 envelope ⇒ 实参那一格不该写：%+v", pd.paramWrites)
	}
	joined := strings.Join(pd.paramSkips, " | ")
	if !strings.Contains(joined, "实参") || !strings.Contains(joined, "direct") {
		t.Errorf("要说清为什么这一格跳过（实参只能落 direct/user），实得：%v", pd.paramSkips)
	}
}

// 表交上来了但**一格都没勾** ⇒ 一项都不写（绝不退回"所有参数全搬"）。
func TestParamSelEmptyTableWritesNothing(t *testing.T) {
	var called []string
	fake := xwV1Fake(t, &called)
	pd, rep := &xcopyPlanData{}, &xcopyReport{}
	if err := planClipParams(context.Background(), fake, map[string]string{"paramSel": `{}`},
		"{s}", "{d}", 0, 1920, 3840, 5760, true, false, pd, rep); err != nil {
		t.Fatalf("计划不该失败：%v", err)
	}
	if len(pd.paramWrites) != 0 || rep.WillParamLayers != 0 {
		t.Fatalf("一格都没勾 ⇒ 不该写：%+v", pd.paramWrites)
	}
	if joined := strings.Join(pd.paramSkips, " | "); !strings.Contains(joined, "一个格子都没勾") {
		t.Errorf("要说清是「表里没勾」而不是「读不到」，实得：%v", pd.paramSkips)
	}

	// 反证：**不给** `paramSel` 才是老路径（所有参数各搬实参到选定层）
	pd2, rep2 := &xcopyPlanData{}, &xcopyReport{}
	if err := planClipParams(context.Background(), fake, map[string]string{},
		"{s}", "{d}", 0, 1920, 3840, 5760, true, false, pd2, rep2); err != nil {
		t.Fatalf("老路径不该失败：%v", err)
	}
	if rep2.WillParamLayers == 0 {
		t.Errorf("不给 paramSel 应当走老路径（tension/energy/formant 各按默认层），实得 0 层")
	}
}

// ⭐ **真机花名册**（2026-10-07 实测导出，轨 1「洛天依」`verse24`，逐字照抄）：
// air/falsetto/tension/energy 有 envelope+direct 两个可写 dense 层；**formant 只有 envelope**；pitch 不可用。
// 参数表要给出的东西全在这份数据上钉死：列 = 实参+包络两列，而每个参数**哪几格是可勾的**不一样。
func TestParamColumnsRealV1Roster(t *testing.T) {
	var ro paramRoster
	if err := json.Unmarshal([]byte(`{"engineGeneration":"verse24","params":[
		{"param":"air","displayName":"Air","available":true,"scale":"envelope","valueRange":{"min":0.2,"max":2.5},
		 "layers":[{"layer":"baseline","access":"read-only","shape":"dense"},{"layer":"direct","access":"read-write","shape":"dense"},
		           {"layer":"envelope","access":"read-write","shape":"dense"}]},
		{"param":"falsetto","displayName":"Falsetto","available":true,"scale":"envelope","valueRange":{"min":0.3,"max":3},
		 "layers":[{"layer":"baseline","access":"read-only","shape":"dense"},{"layer":"direct","access":"read-write","shape":"dense"},
		           {"layer":"envelope","access":"read-write","shape":"dense"}]},
		{"param":"tension","displayName":"Tension","available":true,"scale":"envelope","valueRange":{"min":0.3,"max":2.5},
		 "layers":[{"layer":"baseline","access":"read-only","shape":"dense"},{"layer":"direct","access":"read-write","shape":"dense"},
		           {"layer":"envelope","access":"read-write","shape":"dense"}]},
		{"param":"energy","displayName":"Energy","available":true,"scale":"envelope","valueRange":{"min":0,"max":2},
		 "layers":[{"layer":"baseline","access":"read-only","shape":"dense"},{"layer":"direct","access":"read-write","shape":"dense"},
		           {"layer":"envelope","access":"read-write","shape":"dense"}]},
		{"param":"formant","displayName":"Formant","available":true,"scale":"envelope","valueRange":{"min":-1,"max":1},
		 "layers":[{"layer":"envelope","access":"read-write","shape":"dense"}]},
		{"param":"pitch","displayName":"Pitch","available":false,"scale":"semitones","valueRange":{"min":0,"max":0},"layers":[]}]}`), &ro); err != nil {
		t.Fatalf("花名册 JSON 坏了：%v", err)
	}
	cols := paramColumnsOf(&ro)
	// ⭐ 用户 2026-10-19：「v1 包络大于实参，包络优先」⇒ 列序是 **包络在前、实参在后**
	if len(cols) != 2 || cols[0].Key != "envelope" || cols[1].Key != "effective" || cols[1].DstLayer != "direct" {
		t.Fatalf("真机 v1 该是两列（包络→envelope、实参→direct，包络在前），实得 %+v", cols)
	}
	// 每格能不能勾：目标层在不在这个参数的可写 dense 层里
	cell := func(param, dstLayer string) bool { return containsStr(denseWritableLayers(&ro, param), dstLayer) }
	cells := map[string]bool{
		"air/effective": true, "air/envelope": true,
		"falsetto/effective": true, "falsetto/envelope": true,
		"tension/effective": true, "tension/envelope": true,
		"energy/effective": true, "energy/envelope": true,
		"formant/effective": false, // ⭐ formant 只有 envelope ⇒ 实参那一格在页面上是「—」
		"formant/envelope":  true,
	}
	for k, want := range cells {
		parts := strings.Split(k, "/")
		src := "direct"
		if parts[1] == "envelope" {
			src = "envelope"
		}
		if got := cell(parts[0], src); got != want {
			t.Errorf("%s 这一格该是 %v，实得 %v", k, want, got)
		}
	}
	// pitch 不可用、又没有可写层 ⇒ **不进表格**
	if n := len(denseWritableLayers(&ro, "pitch")); n != 0 {
		t.Errorf("pitch 不该有可写层，实得 %d 个", n)
	}
	// 实参列的落点由 `effectiveDstLayer` 定：真机 v1 是 direct
	if got := effectiveDstLayer(denseWritableLayers(&ro, "air")); got != "direct" {
		t.Errorf("v1 的实参该落 direct，实得 %q", got)
	}
	if got := effectiveDstLayer(denseWritableLayers(&ro, "formant")); got != "" {
		t.Errorf("formant 只有 envelope ⇒ 实参没地方落（要返回空串），实得 %q", got)
	}
}

// 列名/参数名不认识 ⇒ **当场报错**（不静默丢，免得"勾了没动静"）。
func TestParamSelRejectsUnknown(t *testing.T) {
	var called []string
	fake := xwV1Fake(t, &called)
	cases := []struct{ sel, want string }{
		{`{"tension":["bogus"]}`, "列名"},
		{`{"nope":["effective"]}`, "花名册"},
		{`not json`, "JSON"},
	}
	for _, c := range cases {
		pd, rep := &xcopyPlanData{}, &xcopyReport{}
		err := planClipParams(context.Background(), fake, map[string]string{"paramSel": c.sel},
			"{s}", "{d}", 0, 1920, 3840, 5760, true, false, pd, rep)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("`paramSel` = %s 应当报错（含 %q），实得 %v", c.sel, c.want, err)
		}
	}
}

// 列本身由**目标那份花名册**推出来（不写死世代名）：v1 两列、v2 一列。
func TestParamColumnsByRoster(t *testing.T) {
	load := func(js string) *paramRoster {
		var ro paramRoster
		if err := json.Unmarshal([]byte(js), &ro); err != nil {
			t.Fatalf("花名册 JSON 坏了：%v", err)
		}
		return &ro
	}
	v1 := load(`{"engineGeneration":"verse24","params":[
		{"param":"tension","available":true,"layers":[
			{"layer":"baseline","access":"read-only","shape":"dense"},
			{"layer":"direct","access":"read-write","shape":"dense"},
			{"layer":"envelope","access":"read-write","shape":"dense"}]},
		{"param":"formant","available":true,"layers":[{"layer":"envelope","access":"read-write","shape":"dense"}]}]}`)
	cols := paramColumnsOf(v1)
	// ⭐ 2026-10-19：「包络优先」⇒ 包络列在前
	if len(cols) != 2 || cols[0].Key != "envelope" || cols[1].Key != "effective" || cols[1].DstLayer != "direct" {
		t.Fatalf("v1 该是两列（包络→envelope、实参→direct，包络在前），实得 %+v", cols)
	}
	v2 := load(`{"engineGeneration":"singing-mamba","params":[
		{"param":"tension","available":true,"layers":[
			{"layer":"baseline","access":"read-only","shape":"dense"},
			{"layer":"user","access":"read-write","shape":"dense"}]}]}`)
	cols2 := paramColumnsOf(v2)
	if len(cols2) != 1 || cols2[0].Key != "effective" || cols2[0].DstLayer != "user" {
		t.Fatalf("v2 该只有一列（实参→user），实得 %+v", cols2)
	}
	// ⭐ 用户 2026-10-22：「dynamic 锚点算在参数曲线里」+ 锚点形状现在也支持
	// ⇒ 只有锚点 lane 时，表格里**仍然出真值列**（写的时候用锚点写），不再当"另一项"排掉。
	dyn := load(`{"engineGeneration":"singing-mamba","params":[
		{"param":"dynamic","available":true,"layers":[{"layer":"user","access":"read-write","shape":"points"}]}]}`)
	if cols3 := paramColumnsOf(dyn); len(cols3) != 1 || cols3[0].Key != "effective" || cols3[0].DstLayer != "user" {
		t.Errorf("只有锚点 lane 时应给一列真值→user（锚点写），实得 %+v", cols3)
	}
}
