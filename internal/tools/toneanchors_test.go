package tools

import (
	"context"
	"strings"
	"testing"
)

// 列锚点（只读）：把 `tone.apply` 刚写进去的锚点逐点列出来 —— 用户 2026-10-10
// 「语调教锚点有列表吗？打出了确认一下」。
//
// 这里用**显式 scope 参数**（trackIndex/clipBegin/clipEnd）⇒ 工具不碰 CLI，测试能在离线跑。
func TestToneAnchorsListsPoints(t *testing.T) {
	p := writeTempACEP(t, tonePayload(t, realNotes(), 100, manyFloats(3000),
		[]any{500, 61.5, 700, 63.0, 2790, 67.0}, true))
	if _, err := toneApplyApply(context.Background(), nil, map[string]string{
		"path": p, "notes": "{a1},{b2},{c3},{d4}", "tones": "124-",
	}); err != nil {
		t.Fatalf("先写进去失败：%v", err)
	}

	out, err := toneAnchorsInspect(context.Background(), nil, map[string]string{
		"path": p, "trackIndex": "0", "clipBegin": "0", "clipEnd": "4000",
	})
	if err != nil {
		t.Fatalf("列锚点失败：%v", err)
	}
	rep := out.(*toneAnchorReport)

	// lane：曲线拆成 2 条 + 锚点 4 条（3 条新的 9 个点 + 1 条旧的剩 1 点 = 10 个点）
	if !strings.Contains(rep.Summary, "锚点 4 条（10 个点）") || !strings.Contains(rep.Summary, "曲线 2 条") {
		t.Fatalf("小结不对：%s", rep.Summary)
	}
	if len(rep.Points) != 10 {
		t.Fatalf("逐点应 10 个，实际 %d", len(rep.Points))
	}
	// 阴平那条（240/900，音高 62）要能认出「阴平」并且落在「啦」上
	var got []string
	for _, pt := range rep.Points {
		got = append(got, pt.Note)
	}
	if !strings.Contains(strings.Join(got, " "), "「啦」") {
		t.Fatalf("没认出点落在哪个音符上：%v", got)
	}
	lines := strings.Join(rep.Lines, "\n")
	for _, want := range []string{"整条 = 阴平（平）", "整条 = 阳平（先 −4 再升）", "整条 = 去声（end−1 处 −12）"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("没反查出 %s：\n%s", want, lines)
		}
	}
	// 边界点（900 tick = 第 1.88 拍，同时是「啦」的收尾和「明」的起点）要按**entry**认领，不能错位
	// ⚠️ 单位口径（用户 2026-10-14）：列表里印的是**拍**（900/480 = 1.88）
	if !strings.Contains(lines, "第 1.88 拍 → 音高 62（+0 半音）· pointsVUV 1 · 「啦」") {
		t.Fatalf("边界点没按 entry 认领到「啦」：\n%s", lines)
	}
	// 相对音符的偏移：去声那条在 end−1 处应当是 −12
	found := false
	for _, pt := range rep.Points {
		if pt.Tick == 2759 && pt.Offset == -12 {
			found = true
		}
	}
	if !found {
		t.Fatalf("去声的 end−1 点（tick 2759 → −12 半音）没列出来：%+v", rep.Points)
	}
	// 旧锚点里"区间外"的那个（2790 tick = 第 5.81 拍 → 67 = d4 音高 66 的 +1 半音）要能看到
	if !strings.Contains(lines, "第 5.81 拍 → 音高 67（+1 半音）") {
		t.Fatalf("原来那个（区间外的）锚点没列出来：\n%s", lines)
	}
}

func TestToneAnchorsToolRegistered(t *testing.T) {
	tl := Get("tone.anchors")
	if tl == nil || tl.Kind != KindRead {
		t.Fatal("tone.anchors 没注册或不是只读")
	}
}
