package tools

import (
	"context"
	"strings"
	"testing"
)

// ⛔ 并列时必须**报出来**：原脚本 `if (k[i] < k[kmin])` 在并列时取下标最小的，那就是**投硬币**。
// 这组音高是**真机读到的**（工程「明天银去行取钱」整段 7 个音符）—— 实测最低分 2 分、
// 5 个旋转并列（1,2,4,6,11）⇒ 正是"不能装作唯一解"的实例。
func TestDetectKeyReportsTies(t *testing.T) {
	pitches := []int{62, 66, 63, 67, 61, 63, 61}
	k, scores, ties := detectKey(pitches)
	if len(ties) < 2 {
		t.Fatalf("这组音应当出现并列，实得 ties=%v（scores=%v）", ties, scores)
	}
	if k != ties[0] {
		t.Errorf("并列时应当取**下标最小**的那个（照原脚本）：期望 %d，实得 %d", ties[0], k)
	}
	for i := 0; i < 12; i++ {
		isTie := false
		for _, x := range ties {
			if x == i {
				isTie = true
			}
		}
		if (scores[i] == scores[k]) != isTie {
			t.Errorf("旋转 %d 分数 %d，却%s在并列表里（并列表 %v 不全）", i, scores[i],
				map[bool]string{true: "", false: "不"}[isTie], ties)
		}
	}
}

func TestBlackKey(t *testing.T) {
	// 黑键 = 音级 {1,3,6,8,10}
	want := map[int]bool{0: false, 1: true, 2: false, 3: true, 4: false, 5: false,
		6: true, 7: false, 8: true, 9: false, 10: true, 11: false}
	for pc, w := range want {
		if got := blackKey(60 + pc); got != w {
			t.Errorf("blackKey(%d)（音级 %d）= %v，期望 %v", 60+pc, pc, got, w)
		}
	}
	// 负数也要给对（Go 的 % 对负数回负值 ⇒ 必须先归一到 0..11）：
	// -1 ≡ 11(B) 是白键、-2 ≡ 10(A#) 是黑键
	if blackKey(-1) || !blackKey(-2) {
		t.Errorf("负音高算错了：blackKey(-1)=%v（期望 false —— -1≡B 白键）、blackKey(-2)=%v（期望 true —— -2≡A# 黑键）",
			blackKey(-1), blackKey(-2))
	}
}

// 自动检测调式：照原脚本 Mode() —— 12 个旋转里挑"落在黑键上最少"的那个。
func TestDetectKey(t *testing.T) {
	// C 大调音阶（全白键）⇒ 旋转 0 命中 0
	cMaj := []int{60, 62, 64, 65, 67, 69, 71}
	if k, scores, _ := detectKey(cMaj); k != 0 {
		t.Errorf("C 大调应当判成 0(C)，实得 %d（命中数 %v）", k, scores)
	}
	// G 大调（含 F#=78）⇒ 相对 G(7) 全落白键
	gMaj := []int{67, 69, 71, 72, 74, 76, 78}
	if k, scores, _ := detectKey(gMaj); k != 7 {
		t.Errorf("G 大调应当判成 7(G)，实得 %d（命中数 %v）", k, scores)
	}
	// 判定结果要能自证：被选中的那个旋转，命中数必须是 12 个里最小的
	_, scores, _ := detectKey(gMaj)
	for i := 1; i < 12; i++ {
		if scores[i] < scores[7] {
			t.Errorf("旋转 %d 的命中数 %d < 被选中的旋转 7 的 %d ⇒ 选错了", i, scores[i], scores[7])
		}
	}
}

// 音级行走：**只有落在调内才算一步**（这是整个和声算法的核心）。
func TestDiatonicWalk(t *testing.T) {
	const C = 0 // C 大调
	for _, c := range []struct {
		name  string
		from  int
		dir   int
		steps int
		want  int
	}{
		{"C 上行三度（2 步）", 60, 1, 2, 64},  // C→D→E
		{"C 上行六度（5 步）", 60, 1, 5, 69},  // C→D→E→F→G→A（第 5 个音级步落到 A）
		{"C 下行三度（2 步）", 60, -1, 2, 57}, // C→B→A
		{"E 上行三度（2 步）", 64, 1, 2, 67},  // E→F→（F# 是黑键、不算）→G
		{"一度（0 步）", 62, 1, 0, 62},      // 不动
	} {
		got, clamped := diatonicWalk(c.from, C, c.dir, c.steps)
		if clamped {
			t.Errorf("%s：不该被夹", c.name)
		}
		if got != c.want {
			t.Errorf("%s：diatonicWalk(%d,dir=%d,%d) = %d，期望 %d", c.name, c.from, c.dir, c.steps, got, c.want)
		}
	}
	// 越界要夹住并**报出来**（ACE 音高是 0..127，越界会被拒）
	if got, clamped := diatonicWalk(126, 0, 1, 5); !clamped || got != 127 {
		t.Errorf("上行越界应当夹到 127 并标记 clamped，实得 %d clamped=%v", got, clamped)
	}
	if got, clamped := diatonicWalk(1, 0, -1, 5); !clamped || got != 0 {
		t.Errorf("下行越界应当夹到 0 并标记 clamped，实得 %d clamped=%v", got, clamped)
	}
}

// 避免不协和的 ±1 步：原脚本只给 3 度 / 6 度写了两档，这里也只管这两个。
func TestAvoidAdjust(t *testing.T) {
	for _, c := range []struct {
		pc, iv, dir, want int
		why               string
	}{
		{2, 0, 0, 1, "三度向上、根音 D ⇒ 多走一步"},
		{7, 0, 0, 1, "三度向上、根音 G ⇒ 多走一步"},
		{5, 0, 1, 1, "三度向下、根音 F ⇒ 多走一步"},
		{9, 0, 1, 1, "三度向下、根音 A ⇒ 多走一步"},
		{2, 1, 1, -1, "六度向下、根音 D ⇒ 少走一步"},
		{7, 1, 1, -1, "六度向下、根音 G ⇒ 少走一步"},
		{5, 1, 0, -1, "六度向上、根音 F ⇒ 少走一步"},
		{9, 1, 0, -1, "六度向上、根音 A ⇒ 少走一步"},
		{0, 0, 0, 0, "根音 C 不受影响"},
		{2, 2, 0, 0, "五度不参与（原脚本只写了两档）"},
		{2, 0, 1, 0, "三度向下、根音 D 不在那两档里"},
	} {
		if got := avoidAdjust(c.pc, c.iv, c.dir); got != c.want {
			t.Errorf("%s：avoidAdjust(%d,%d,%d) = %d，期望 %d", c.why, c.pc, c.iv, c.dir, got, c.want)
		}
	}
}

// 干跑只读 + 默认参数下的移动结果。
// 假 CLI 的音符是 D4(62) 与 E4(64)，都在 C 大调内 ⇒ 自动检测应当是 C。
// 三度向上 2 步：D→F(67)、E→A(69)。
func TestHarmonyPlanIsReadOnly(t *testing.T) {
	var called []string
	fake := newScopeFake(t, &called, scopeFakeOpts{})

	got, err := Get("tuning.harmony").Plan(context.Background(), fake, map[string]string{
		"scope": "clip", "key": "auto", "direction": "up", "interval": "3rd", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep, ok := got.(*harmonyReport)
	if !ok {
		t.Fatalf("应当回 *harmonyReport，实得 %T", got)
	}
	if !rep.DryRun {
		t.Error("干跑结果要标 DryRun")
	}
	if rep.Key != 0 {
		t.Errorf("D/E 都在 C 大调内 ⇒ 应当判成 C(0)，实得 %d（命中数 %v）", rep.Key, rep.KeyScores)
	}
	if len(rep.Moves) != 2 {
		t.Fatalf("应当有 2 个音符要移，实得 %d：%+v", len(rep.Moves), rep.Moves)
	}
	// 按位置排过序：pos 0 = D(62) → F(65)；pos 480 = E(64) → G(67)
	// （C 大调里 D 的上行三度是 F —— D→E→F 三步音级；E 的上行三度是 G，中间那个 F# 是黑键、不算步）
	if rep.Moves[0].From != 62 || rep.Moves[0].To != 65 {
		t.Errorf("第一个音符应当是 62→65(F)，实得 %d→%d", rep.Moves[0].From, rep.Moves[0].To)
	}
	if rep.Moves[1].From != 64 || rep.Moves[1].To != 67 {
		t.Errorf("第二个音符应当是 64→67(G)，实得 %d→%d", rep.Moves[1].From, rep.Moves[1].To)
	}
	for _, c := range called {
		if strings.HasPrefix(c, "note move") || strings.HasPrefix(c, "track duplicate") {
			t.Errorf("干跑里出现了写命令：%s", c)
		}
	}
}

// 一度（0 步）⇒ 全部"跟原来一样"，一个都不移。
func TestHarmonyUnisonMovesNothing(t *testing.T) {
	fake := newScopeFake(t, &[]string{}, scopeFakeOpts{})
	got, err := Get("tuning.harmony").Plan(context.Background(), fake, map[string]string{
		"scope": "clip", "direction": "up", "interval": "1st", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	rep := got.(*harmonyReport)
	if rep.Will != 0 || rep.Same != 2 {
		t.Errorf("一度应当一个都不移（Same=2、Will=0），实得 Will=%d Same=%d", rep.Will, rep.Same)
	}
}

// 手动调式要真的用上（而不是仍走自动检测）。
func TestHarmonyManualKey(t *testing.T) {
	fake := newScopeFake(t, &[]string{}, scopeFakeOpts{})
	got, err := Get("tuning.harmony").Plan(context.Background(), fake, map[string]string{
		"scope": "clip", "key": "7", "direction": "up", "interval": "3rd", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	rep := got.(*harmonyReport)
	if rep.Key != 7 {
		t.Errorf("手动调式应当是 7，实得 %d", rep.Key)
	}
	if !strings.Contains(rep.KeyHow, "手动") {
		t.Errorf("KeyHow 要说明是手动指定的，实得 %q", rep.KeyHow)
	}
	// G 大调下 D(62) 上行三度：F# 在调内 ⇒ D→F#(66)（若按 C 大调则是 67）
	if rep.Moves[0].To != 66 {
		t.Errorf("G 大调里 D 上行三度应当是 66(F#)，实得 %d", rep.Moves[0].To)
	}
}
