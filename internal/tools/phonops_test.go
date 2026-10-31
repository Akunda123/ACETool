package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"acetool/internal/acecli"
)

// ⛔ 这条是移植时**故意改语义**的地方，必须钉住：
// 原脚本在音素**字符串**上做子串替换，会命中 `ian`/`an`/`ang` 里的 `a`。
// 这里按**符号**替换 —— 换了就是换了，没换就是没换。
func TestReplaceTokenIsTokenWise(t *testing.T) {
	for _, c := range []struct {
		name     string
		syms     []string
		find     string
		repl     string
		want     []string
		wantHits int
	}{
		{"整符号命中", []string{"m", "ing"}, "ing", "in", []string{"m", "in"}, 1},
		{"子串不算命中", []string{"t", "ian"}, "a", "aa", []string{"t", "ian"}, 0},
		{"删掉符号", []string{"m", "ing", "n"}, "ing", "", []string{"m", "n"}, 1},
		{"多处命中", []string{"a", "b", "a"}, "a", "x", []string{"x", "b", "x"}, 2},
		{"一个都不命中", []string{"m", "ing"}, "vn", "v", []string{"m", "ing"}, 0},
	} {
		got, hits := replaceToken(c.syms, c.find, c.repl)
		if hits != c.wantHits {
			t.Errorf("%s：命中数 = %d，期望 %d", c.name, hits, c.wantHits)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s：replaceToken(%v,%q,%q) = %v，期望 %v", c.name, c.syms, c.find, c.repl, got, c.want)
		}
	}
}

// 干跑只读 + 替换词不属于该语言时**预校验就拒掉**（原脚本没有这一步）。
func TestPhonemeReplacePlanValidatesAndIsReadOnly(t *testing.T) {
	var called []string
	fake := newScopeFake(t, &called, scopeFakeOpts{})

	got, err := Get("phoneme.replace").Plan(context.Background(), fake, map[string]string{
		"find": "ing", "replace": "in", "scope": "clip", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep, ok := got.(*phonReplaceReport)
	if !ok {
		t.Fatalf("应当回 *phonReplaceReport，实得 %T", got)
	}
	if !rep.DryRun {
		t.Error("干跑结果要标 DryRun")
	}
	if rep.WillWrite != 1 {
		t.Errorf("应当只有 1 个音符会被写（另一个不含 ing），实得 %d", rep.WillWrite)
	}
	if rep.Frozen != 1 {
		t.Errorf("没被改写过的音符写下去会冻结 ⇒ Frozen 应当是 1，实得 %d", rep.Frozen)
	}
	for _, c := range called {
		if strings.HasPrefix(c, "phoneme set") {
			t.Errorf("干跑里出现了写命令：%s", c)
		}
	}
}

// 替换词不在该语言的音素清单里 ⇒ **干跑就拦下来**（并且要说清会被 ACE 拒）
func TestPhonemeReplaceRejectsForeignSymbol(t *testing.T) {
	var called []string
	fake := newScopeFake(t, &called, scopeFakeOpts{})

	got, err := Get("phoneme.replace").Plan(context.Background(), fake, map[string]string{
		"find": "ing", "replace": "zzz", "scope": "clip", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败（应当「拦下来」而不是报错）：%v", err)
	}
	rep := got.(*phonReplaceReport)
	if rep.WillWrite != 0 {
		t.Errorf("替换词不合法 ⇒ 一个都不该写，实得 %d", rep.WillWrite)
	}
	joined := strings.Join(rep.Skips, " | ")
	if !strings.Contains(joined, "不在 Chinese 的音素清单里") {
		t.Errorf("跳过原因要说明符号不属于该语言，实得 %v", rep.Skips)
	}
}

// 查找词与替换词相同 / 空查找词 ⇒ 明确报错（而不是发一堆空计划）
func TestPhonemeReplaceBadInput(t *testing.T) {
	fake := newScopeFake(t, &[]string{}, scopeFakeOpts{})
	for _, vals := range []map[string]string{
		{"find": "", "replace": "a"},
		{"find": "ing", "replace": "ing"},
		{"find": "m ing", "replace": "x"},
	} {
		if _, err := Get("phoneme.replace").Plan(context.Background(), fake, vals); err == nil {
			t.Errorf("这组参数应当报错：%v", vals)
		}
	}
}

// 编辑器没开着 ⇒ 回退到**标记线所在片段**（标记线不依赖编辑器）。
func TestReadNoteScopeFallsBackToCaret(t *testing.T) {
	var called []string
	fake := newScopeFake(t, &called, scopeFakeOpts{editorClosed: true})

	sc, err := readNoteScope(context.Background(), fake, false)
	if err != nil {
		t.Fatalf("回退路径不该失败：%v", err)
	}
	if !strings.Contains(sc.Located, "标记线") {
		t.Errorf("应当说明是回退到标记线定位的，实得 %q", sc.Located)
	}
	if sc.TrackIndex != 0 {
		t.Errorf("轨道应当是 0，实得 %d", sc.TrackIndex)
	}
	if len(sc.Notes) != 2 {
		t.Errorf("片段里应当有 2 个音符，实得 %d", len(sc.Notes))
	}
}

// ⛔ 选区里有音符、但一个 uuid 都读不出来 ⇒ **必须报错**，绝不静默退化成"整个片段"
// （那会让"只想改 3 个音符"变成"改了整段"，而且不报错）。
func TestReadNoteScopeRefusesSilentFallback(t *testing.T) {
	var called []string
	fake := newScopeFake(t, &called, scopeFakeOpts{selectionWithoutUuids: true})

	_, err := readNoteScope(context.Background(), fake, true)
	if err == nil {
		t.Fatal("读不出选区 uuid 时必须报错，不能静默退回整个片段")
	}
	if !strings.Contains(err.Error(), "一个 uuid 都没读出来") {
		t.Errorf("报错要说清是读不出 uuid，实得：%v", err)
	}
}

// 有选区就只处理选区里的那些音符。
func TestReadNoteScopeUsesSelection(t *testing.T) {
	var called []string
	fake := newScopeFake(t, &called, scopeFakeOpts{})

	sc, err := readNoteScope(context.Background(), fake, true)
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if len(sc.Notes) != 1 || sc.Notes[0].NoteUUID != "{n2}" {
		t.Errorf("应当只拿到选区里的 {n2}，实得 %+v", sc.Notes)
	}
	if !strings.Contains(sc.Which, "选中的 1 个音符") {
		t.Errorf("Which 要说明用的是选区，实得 %q", sc.Which)
	}
}

// ---------- 假 CLI（这一批 scope 类工具共用） ----------

type scopeFakeOpts struct {
	editorClosed          bool // 模拟编辑器没开 ⇒ 走标记线回退
	selectionWithoutUuids bool // 模拟"说有选区但读不出 uuid"
	selection             []string
	inventory             []string
}

func newScopeFake(t *testing.T, called *[]string, o scopeFakeOpts) ReadFunc {
	t.Helper()
	if o.selection == nil {
		o.selection = []string{"{n2}"}
	}
	if o.inventory == nil {
		// Chinese 的清单：够用的子集（含 in，不含 zzz）
		o.inventory = []string{"m", "n", "ing", "in", "a", "t", "ian", "vn"}
	}
	return func(_ context.Context, args ...string) (*acecli.Result, error) {
		cmd := strings.Join(args, " ")
		*called = append(*called, cmd)
		if len(args) >= 2 {
			switch args[0] + " " + args[1] {
			case "phoneme set", "note move", "note set-lyric", "note add", "clip create":
				return nil, fmt.Errorf("干跑不许调写命令：%s", cmd)
			}
		}
		switch {
		case len(args) >= 2 && args[0] == "editor" && args[1] == "status":
			if o.editorClosed {
				return &acecli.Result{ExitCode: acecli.ExitFailed,
					Err: &acecli.Envelope{Code: "EDITOR_NOT_OPEN", Message: "no editor is open"}}, nil
			}
			return okJSON(`{"isAvailable":true,"trackIndex":0,"clipIndex":0,"clipUuid":"{c1}","clipName":"测试片段","editorType":"sing"}`), nil
		case len(args) >= 2 && args[0] == "caret":
			// 标记线落在 tick 100 ⇒ 盖住它的片段是 0..960 那个
			return okJSON(`{"trackIndex":0,"tick":100}`), nil
		case len(args) >= 2 && args[0] == "clip" && args[1] == "list":
			return okJSON(`{"clips":[{"clipType":"sing","clipName":"测试片段","clipBegin":0,"clipEnd":960,"clipUuid":"{c1}"}]}`), nil
		case len(args) >= 2 && args[0] == "clip" && args[1] == "note-content":
			return okJSON(`{"noteCount":2,"notes":[
				{"noteUuid":"{n1}","pos":0,"dur":480,"endPos":480,"pitch":62,"lyric":"明","language":"Chinese"},
				{"noteUuid":"{n2}","pos":480,"dur":480,"endPos":960,"pitch":64,"lyric":"天","language":"Chinese"}]}`), nil
		case len(args) >= 2 && args[0] == "selection" && args[1] == "get":
			if o.selectionWithoutUuids {
				// 说有选区，但 notes 里没有可用 uuid（模拟字段名变了）
				return okJSON(`{"hasSelection":true,"selectionCount":3,"notes":[{"pos":0},{"pos":480}]}`), nil
			}
			var rows []string
			for _, u := range o.selection {
				rows = append(rows, fmt.Sprintf(`{"noteUuid":%q,"pos":480,"dur":480,"endPos":960,"pitch":64}`, u))
			}
			return okJSON(fmt.Sprintf(`{"hasSelection":true,"selectionCount":%d,"notes":[%s]}`,
				len(o.selection), strings.Join(rows, ","))), nil
		case len(args) >= 2 && args[0] == "phoneme" && args[1] == "list":
			var rows []string
			for _, a := range args {
				switch a {
				case "{n1}":
					rows = append(rows, `{"noteUuid":"{n1}","language":"Chinese","phonemes":["m","ing"],"isOverride":false}`)
				case "{n2}":
					rows = append(rows, `{"noteUuid":"{n2}","language":"Chinese","phonemes":["t","ian"],"isOverride":false}`)
				}
			}
			return okJSON(`{"notes":[` + strings.Join(rows, ",") + `]}`), nil
		case len(args) >= 2 && args[0] == "phoneme" && args[1] == "inventory":
			var rows []string
			for _, s := range o.inventory {
				rows = append(rows, fmt.Sprintf("%q", s))
			}
			return okJSON(`{"language":"Chinese","consonants":[` + strings.Join(rows, ",") + `],"vowels":[]}`), nil
		}
		return nil, fmt.Errorf("假 CLI 没准备这条命令：%s", cmd)
	}
}
