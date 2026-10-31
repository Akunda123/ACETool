package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"acetool/internal/acecli"
)

// 片段归属判据（照原脚本）。**边界差一个 tick 就是"多切一刀/少切一刀"，而且不报错** ——
// 所以这两条闭边要单独钉。
func TestSplitVerdict(t *testing.T) {
	for _, c := range []struct {
		name       string
		begin, end int64
		playhead   int64
		want       string
		why        string
	}{
		{"整段在前", 0, 480, 1000, verdictLeft, "结束 < 播放头"},
		{"正好结束在播放头", 0, 1000, 1000, verdictLeft, "结束 == 播放头 ⇒ 归原轨（原脚本 lastEnd <= playhead）"},
		{"整段在后", 2000, 2480, 1000, verdictRight, "开始 > 播放头"},
		{"正好开始于播放头", 1000, 1480, 1000, verdictRight, "开始 == 播放头 ⇒ 归复制轨（原脚本 firstOnset >= playhead）"},
		{"跨越", 500, 1500, 1000, verdictStraddle, "夹在中间"},
		{"播放头在片段内部靠前", 999, 1001, 1000, verdictStraddle, "夹在中间（哪怕只有 1 tick 也算跨界）"},
	} {
		if got := splitVerdict(c.begin, c.end, c.playhead); got != c.want {
			t.Errorf("%s：splitVerdict(%d,%d,%d) = %q，期望 %q（%s）",
				c.name, c.begin, c.end, c.playhead, got, c.want, c.why)
		}
	}
}

// 切开之后"该删谁"的判据 —— 它同时管"没切到的片段"，所以两个方向都要对。
func TestSplitDeletePredicates(t *testing.T) {
	const P = 1000
	// 原轨：删「开始 ≥ P」⇒ 左半/整段在前保留，右半/整段在后删掉
	for _, c := range []struct {
		name       string
		begin, end int64
		delLeft    bool
		delRight   bool
	}{
		{"整段在前", 0, 480, false, true},
		{"正好结束在 P（=原轨的左半边界）", 0, P, false, true},
		{"整段在后", 2000, 2480, true, false},
		{"正好开始于 P（=复制轨的右半边界）", P, 1480, true, false},
		{"切完的原轨左半", 500, P, false, true},
		{"切完的原轨右半", P, 1500, true, false},
	} {
		cb := clipBrief{Name: c.name, Begin: c.begin, End: c.end}
		if got := shouldDeleteFromLeft(cb, P); got != c.delLeft {
			t.Errorf("原轨 %s（[%d,%d)）：shouldDeleteFromLeft = %v，期望 %v", c.name, c.begin, c.end, got, c.delLeft)
		}
		if got := shouldDeleteFromRight(cb, P); got != c.delRight {
			t.Errorf("复制轨 %s（[%d,%d)）：shouldDeleteFromRight = %v，期望 %v", c.name, c.begin, c.end, got, c.delRight)
		}
	}
	// ⭐ 关键性质：**每个片段恰好被一边删掉**（不会两边都留、也不会两边都删）。
	// 切开后的两半也满足这条 ⇒ 合起来正好等于原来的内容。
	for _, c := range []struct {
		name       string
		begin, end int64
	}{
		{"整段在前", 0, 480},
		{"整段在后", 2000, 2480},
		{"正好结束在 P", 0, P},
		{"正好开始于 P", P, 1480},
		{"原轨左半", 500, P},
		{"原轨右半", P, 1500},
	} {
		cb := clipBrief{Name: c.name, Begin: c.begin, End: c.end}
		dl, dr := shouldDeleteFromLeft(cb, P), shouldDeleteFromRight(cb, P)
		if dl == dr {
			t.Errorf("%s（[%d,%d)）：两边删/留必须**正好相反**，实得 原轨删=%v 复制轨删=%v",
				c.name, c.begin, c.end, dl, dr)
		}
	}
}

// 分割工具是**写**工具，而且必须走 Plan+Apply（多步：要先复制、拿到新 uuid 才能改）。
func TestTrackSplitToolRegistered(t *testing.T) {
	tl := Get("track.split")
	if tl == nil {
		t.Fatal("track.split 没注册")
	}
	if tl.Kind != KindWrite {
		t.Errorf("分割轨道是写工具，实得 %s", tl.Kind)
	}
	if tl.Plan == nil || tl.Apply == nil {
		t.Fatal("必须 Plan+Apply 成对 —— 这个操作要边跑边读（先复制、拿到新 uuid 再改），" +
			"`Build` 一次性算完命令做不到")
	}
	if tl.Build != nil || tl.Inspect != nil {
		t.Error("不该同时有 Build/Inspect —— 注册表不变量要求三种实现恰有其一")
	}
	if tl.Category != "轨道编辑" {
		t.Errorf("细分类应当是「轨道编辑」（它被归到「移植脚本」那一栏），实得 %q", tl.Category)
	}
	if tl.Source == "" || !strings.Contains(tl.Desc, "回滚") {
		t.Error("说明里要写清来源与回滚行为")
	}
	// 干跑（Plan）在空表单下要给可读的错，不许 panic
	rd := stubRead(t, map[string]string{})
	if _, err := tl.Plan(context.Background(), rd, map[string]string{}); err == nil {
		t.Error("空表单下 Plan 应当报错（读不到标记线/片段）")
	} else if strings.TrimSpace(err.Error()) == "" {
		t.Error("Plan 报了个空错")
	}

}

// ⛔ **干跑必须只读** —— 这条用**行为**验，不看源码：
// 给 Plan 一个假 CLI，它会响应所有**读**命令，一旦 Plan 去调任何**写**动词就立刻报错。
// 这样"干跑会不会偷偷改工程"是被真正跑出来的，而不是靠读代码猜。
func TestTrackSplitPlanIsReadOnly(t *testing.T) {
	var called []string
	rd := func(_ context.Context, args ...string) (*acecli.Result, error) {
		cmd := strings.Join(args, " ")
		called = append(called, cmd)
		// 写动词一律拒（并把是哪个动词记下来给断言用）
		for _, w := range []string{"duplicate", "split", "delete"} {
			if strings.Contains(cmd, w) {
				return nil, fmt.Errorf("干跑不许调写命令：%s", cmd)
			}
		}
		// 读动词回最小可用的 JSON
		switch {
		case len(args) >= 2 && args[0] == "caret":
			return okJSON(`{"trackIndex":4,"trackUuid":"{trk}"}`), nil
		case len(args) >= 2 && args[0] == "transport":
			return okJSON(`{"positionTick":1000,"status":"stopped"}`), nil
		case len(args) >= 2 && args[0] == "clip" && args[1] == "list":
			// 三个片段：整段在前、跨越播放头、整段在后
			return okJSON(`{"clips":[
				{"clipType":"sing","clipName":"A","clipBegin":0,"clipEnd":480,"clipUuid":"{a}"},
				{"clipType":"sing","clipName":"B","clipBegin":500,"clipEnd":1500,"clipUuid":"{b}"},
				{"clipType":"sing","clipName":"C","clipBegin":2000,"clipEnd":2480,"clipUuid":"{c}"}]}`), nil
		case len(args) >= 2 && args[0] == "timesig":
			return okJSON(`{"signatures":[{"barPos":0,"tick":0,"numerator":4,"denominator":4}]}`), nil
		}
		return nil, fmt.Errorf("假 CLI 没准备这条命令：%s", cmd)
	}

	got, err := Get("track.split").Plan(context.Background(), rd, map[string]string{})
	if err != nil {
		t.Fatalf("干跑不该失败：%v（已调用：%v）", err, called)
	}
	rep, isRep := got.(*splitReport)
	if !isRep {
		t.Fatalf("干跑应当回 *splitReport，实得 %T", got)
	}
	if !rep.DryRun {
		t.Error("干跑结果要标 DryRun")
	}
	// 分类要对：1 前 / 1 跨 / 1 后
	if rep.LeftClips != 1 || rep.Straddling != 1 || rep.RightClips != 1 {
		t.Errorf("分类应当是 前1/跨1/后1，实得 前%d/跨%d/后%d", rep.LeftClips, rep.Straddling, rep.RightClips)
	}
	if len(rep.Steps) == 0 {
		t.Error("干跑要列出打算做的步骤")
	}
	// 每条命令都必须是读
	for _, c := range called {
		for _, w := range []string{"track duplicate", "clip split", "clip delete", "track delete"} {
			if strings.Contains(c, w) {
				t.Errorf("干跑里出现了写命令：%s", c)
			}
		}
	}
	// 干跑里**不能**出现"复制轨已经存在"这种结果
	if rep.NewTrack != nil {
		t.Error("干跑不该有 NewTrack（那要真复制了才有）")
	}
}

// 假 CLI 的回包要能被 readJSON 解出来（ExitCode 0 + JSON）。
func okJSON(s string) *acecli.Result {
	return &acecli.Result{ExitCode: acecli.ExitOK, JSON: json.RawMessage(s)}
}

// 「轨道编辑」这个细分类要归到**移植脚本**那一栏（用户点名的）。
func TestTrackEditIsInPortedGroup(t *testing.T) {
	if got := groupOf("轨道编辑"); got != "移植脚本" {
		t.Errorf("「轨道编辑」应当归到「移植脚本」栏，实得 %q", got)
	}
	// 而自建的 `轨道`（颜色循环）仍在自建那栏
	if got := groupOf("轨道"); got != "自建工具" {
		t.Errorf("「轨道」应当仍在「自建工具」栏，实得 %q", got)
	}
}
