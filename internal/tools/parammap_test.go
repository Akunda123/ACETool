package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// 这些测试覆盖**合并后的公共判定**（parammap.go）：
// 跨轨复制 / 轨内复制的参数半、以及原先独立的 `xcopy.params`（已下线）用的是同一套。

func rosterFromJSON(t *testing.T, s string) *paramRoster {
	t.Helper()
	var ro paramRoster
	if err := json.Unmarshal([]byte(s), &ro); err != nil {
		t.Fatalf("花名册 JSON 造错了：%v", err)
	}
	return &ro
}

// v1（verse24）：tension 是 envelope 尺度、0.3…2.5，可写层 direct(override)/envelope(multiplier)。
const v1Roster = `{"engineGeneration":"verse24","params":[
  {"param":"tension","displayName":"Tension","available":true,"scale":"envelope",
   "valueRange":{"min":0.3,"max":2.5},
   "layers":[{"layer":"baseline","access":"read-only","role":"synthesized-default","shape":"dense"},
             {"layer":"direct","access":"read-write","role":"override","shape":"dense"},
             {"layer":"envelope","access":"read-write","role":"multiplier","shape":"dense"}]}]}`

// v2（singing-mamba）：tension 是 model 尺度、0…1，可写层只有 user(override)。
const v2Roster = `{"engineGeneration":"singing-mamba","params":[
  {"param":"tension","displayName":"Tension","available":true,"scale":"model",
   "valueRange":{"min":0,"max":1},
   "layers":[{"layer":"baseline","access":"read-only","role":"synthesized-default","shape":"dense"},
             {"layer":"user","access":"read-write","role":"override","shape":"dense"}]}]}`

// ⭐ 用户 2026-10-19：「做数值映射，跨代允许映射」—— 0.3…2.5 → 0…1 要按值域线性换算。
func TestMapValuesByValueRange(t *testing.T) {
	src := []*float64{ptr(0.3), ptr(1.4), ptr(2.5), nil}
	out, clamped, examples, why := mapValues(src, "range", [2]float64{0.3, 2.5}, [2]float64{0, 1})
	if why != "" {
		t.Fatalf("不该拒绝：%s", why)
	}
	if clamped != 0 {
		t.Errorf("都在值域内，不该夹边，实得 %d", clamped)
	}
	want := []float64{0, 0.5, 1, -1}
	for i, w := range want {
		if w < 0 {
			if out[i] != nil {
				t.Errorf("空档必须保持空档（不能变成 0），第 %d 个 = %v", i, *out[i])
			}
			continue
		}
		if out[i] == nil || abs(*out[i]-w) > 1e-9 {
			t.Errorf("第 %d 个应当换算成 %.4g，实得 %v", i, w, out[i])
		}
	}
	if len(examples) != 3 {
		t.Fatalf("样例应当只给 3 个，实得 %d", len(examples))
	}
	if abs(examples[1][0]-1.4) > 1e-9 || abs(examples[1][1]-0.5) > 1e-9 {
		t.Errorf("第 2 个样例应当是 1.4→0.5，实得 %.4g→%.4g", examples[1][0], examples[1][1])
	}
}

// 原值照搬：不做换算，但**越界照样夹**（ACE 越界直接拒写），并且要计数。
func TestMapValuesNoneKeepsRawAndClamps(t *testing.T) {
	src := []*float64{ptr(0.5), ptr(3), nil}
	out, clamped, _, why := mapValues(src, "none", [2]float64{0, 1}, [2]float64{0, 1})
	if why != "" {
		t.Fatalf("不该拒绝：%s", why)
	}
	if out[0] == nil || abs(*out[0]-0.5) > 1e-12 {
		t.Errorf("原值照搬应当保持 0.5，实得 %v", out[0])
	}
	if out[1] == nil || abs(*out[1]-1) > 1e-12 {
		t.Errorf("越界的 3 应当夹到 1，实得 %v", out[1])
	}
	if clamped != 1 {
		t.Errorf("应当记 1 个夹边，实得 %d", clamped)
	}
}

// 值域退化（跨度 ≤ 0）⇒ 明说"算不了"，不猜。
func TestMapValuesRejectsDegenerateRange(t *testing.T) {
	_, _, _, why := mapValues([]*float64{ptr(1)}, "range", [2]float64{1, 1}, [2]float64{0, 1})
	if why == "" || !strings.Contains(why, "跨度") {
		t.Errorf("值域退化要说清「跨度」，实得 %q", why)
	}
}

// ⭐ 跨代时层名不一样：源 `direct`（v1，override）落到目标的 `user`（v2，override）。
func TestResolveDstLayerByRole(t *testing.T) {
	got, note := resolveDstLayer(rosterFromJSON(t, v2Roster), "tension", "direct", "override")
	if got != "user" {
		t.Fatalf("应当按角色落到 user，实得 %q", got)
	}
	if !strings.Contains(note, "角色") || !strings.Contains(note, "user") {
		t.Errorf("换层要说明为什么，实得 %q", note)
	}
	// 同名层存在 ⇒ 直接用，不用解释
	got2, note2 := resolveDstLayer(rosterFromJSON(t, v1Roster), "tension", "direct", "override")
	if got2 != "direct" || note2 != "" {
		t.Errorf("同名层应当直接用 direct，实得 %q / %q", got2, note2)
	}
	// 目标上没有可写 dense 层 ⇒ 明说跳过
	ro := rosterFromJSON(t, `{"params":[{"param":"tension","available":true,
		"layers":[{"layer":"baseline","access":"read-only","shape":"dense"}]}]}`)
	got3, note3 := resolveDstLayer(ro, "tension", "direct", "override")
	if got3 != "" || !strings.Contains(note3, "没有可写的 dense 层") {
		t.Errorf("没可写层应当返回空 + 说明，实得 %q / %q", got3, note3)
	}
}

// 说明文字：按值域换算要写出两边的值域与样例；跨代要**明说**。
func TestParamMapNoteSaysWhatHappened(t *testing.T) {
	n := paramMapNote("tension", "range", [2]float64{0.3, 2.5}, [2]float64{0, 1},
		[][2]float64{{1.4, 0.5}}, 0, true)
	for _, want := range []string{"tension", "按值域换算", "0.3…2.5", "0…1", "1.4→0.5", "跨引擎世代"} {
		if !strings.Contains(n, want) {
			t.Errorf("说明里缺 %q：%s", want, n)
		}
	}
	n2 := paramMapNote("tension", "none", [2]float64{0, 1}, [2]float64{0, 1}, nil, 2, false)
	if !strings.Contains(n2, "原值照搬") || !strings.Contains(n2, "夹到边界") {
		t.Errorf("原值照搬也要说明（含夹边计数）：%s", n2)
	}
}

func ptr(f float64) *float64 { return &f }

// ---------- 「只搬编辑过的位置」 ----------

// ⭐ 用户 2026-10-20：「以编辑过的为准，没编辑过的位置不要轻易复制」
// ⇒ 载荷里空档的地方**不参与写入**（`vocalparam write` 是替换整段，null 会清掉目标）。
func TestDenseRunsSkipsGapsAndSingleTicks(t *testing.T) {
	pts := []*float64{
		nil, nil, ptr(1), ptr(2), ptr(3), nil, nil, // 一段 3 tick（从下标 2 开始）
		ptr(9), nil, // 孤立单点 ⇒ 丢掉
		ptr(4), ptr(5), nil, // 一段 2 tick
	}
	runs, dropped := denseRuns(pts, 100, 2)
	if len(runs) != 2 {
		t.Fatalf("应当切成 2 段，实得 %d：%+v", len(runs), runs)
	}
	if runs[0].Begin != 102 || len(runs[0].Pts) != 3 || *runs[0].Pts[0] != 1 {
		t.Errorf("第 1 段应当是 [102, 3 个点, 首值 1]，实得 %+v", runs[0])
	}
	if runs[1].Begin != 109 || len(runs[1].Pts) != 2 || *runs[1].Pts[0] != 4 {
		t.Errorf("第 2 段应当是 [109, 2 个点, 首值 4]，实得 %+v", runs[1])
	}
	if dropped != 1 {
		t.Errorf("应当报 1 个孤立单点被丢，实得 %d", dropped)
	}
	// 全空档 ⇒ 一段都没有（"不写"是正确结果，不是失败）
	if r, d := denseRuns([]*float64{nil, nil}, 0, 2); len(r) != 0 || d != 0 {
		t.Errorf("全空档不该产出段：%+v / %d", r, d)
	}
}

// 用户实参层是谁：v1 是 `direct`、v2 是 `user`；只有 envelope 的参数没有它。
func TestUserLayerOf(t *testing.T) {
	if got := userLayerOf(rosterFromJSON(t, v1Roster), "tension"); got != "direct" {
		t.Errorf("v1 应当是 direct，实得 %q", got)
	}
	if got := userLayerOf(rosterFromJSON(t, v2Roster), "tension"); got != "user" {
		t.Errorf("v2 应当是 user，实得 %q", got)
	}
	onlyEnv := rosterFromJSON(t, `{"params":[{"param":"formant","available":true,
		"layers":[{"layer":"envelope","access":"read-write","shape":"dense"}]}]}`)
	if got := userLayerOf(onlyEnv, "formant"); got != "" {
		t.Errorf("只有 envelope 的参数不该有用户实参层，实得 %q", got)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// ---------- dynamic → 包络（用户 2026-10-19 的跨代约定） ----------

func TestParseDynEnvCoefs(t *testing.T) {
	got, err := parseDynEnvCoefs("falsetto=0.5, tension=-0.3")
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if len(got) != 2 || got[0].Param != "falsetto" || abs(got[0].Gain-0.5) > 1e-12 ||
		got[1].Param != "tension" || abs(got[1].Gain+0.3) > 1e-12 {
		t.Fatalf("解析错了：%+v", got)
	}
	// 分号 / 中文逗号 / 多余空白都认
	if got2, err := parseDynEnvCoefs("  air = 1 ；energy=-1 "); err != nil || len(got2) != 2 {
		t.Errorf("分隔符要宽容：%+v / %v", got2, err)
	}
	// 空串 ⇒ 没配
	if got3, err := parseDynEnvCoefs("   "); err != nil || len(got3) != 0 {
		t.Errorf("空串应当回空：%+v / %v", got3, err)
	}
	// 写错 ⇒ 报错（不静默丢）
	if _, err := parseDynEnvCoefs("falsetto:0.5"); err == nil {
		t.Error("缺 `=` 应当报错")
	}
	if _, err := parseDynEnvCoefs("falsetto=abc"); err == nil {
		t.Error("系数不是数字应当报错")
	}
}

// 锚点线性插值：两端平推、中间按比例。
func TestSampleAnchors(t *testing.T) {
	got := sampleAnchors([][2]float64{{10, 0}, {20, 1}}, 0, 25)
	if len(got) != 25 {
		t.Fatalf("长度该是 25，实得 %d", len(got))
	}
	if abs(got[0]-0) > 1e-12 || abs(got[10]-0) > 1e-12 {
		t.Errorf("第一点之前/处应当平推 0，实得 %.4g / %.4g", got[0], got[10])
	}
	if abs(got[15]-0.5) > 1e-12 {
		t.Errorf("tick 15 应当是 0.5，实得 %.4g", got[15])
	}
	if abs(got[20]-1) > 1e-12 || abs(got[24]-1) > 1e-12 {
		t.Errorf("最后一点之后应当平推 1，实得 %.4g / %.4g", got[20], got[24])
	}
	// 小数 tick 的锚点（真机 dynamic 就是这种：366.958…）
	got2 := sampleAnchors([][2]float64{{0.5, -1}, {1.5, 1}}, 1, 3) // tick 1、2 两个样本
	if len(got2) != 2 || abs(got2[0]-0) > 1e-12 || abs(got2[1]-1) > 1e-12 {
		t.Errorf("小数锚点插值错了：%v", got2)
	}
}

// `1 + k·dyn`，再夹进目标值域。
func TestDynEnvelopeValues(t *testing.T) {
	// v2 的 dynamic **默认 1（1 = 不动）** ⇒ 先减 1，再乘系数
	dyn := []float64{0.5, 1, 1.5}
	out, clamped := dynEnvelopeValues(dyn, 0.5, 0.3, 2.5)
	want := []float64{0.75, 1, 1.25}
	for i, w := range want {
		if out[i] == nil || abs(*out[i]-w) > 1e-12 {
			t.Errorf("第 %d 个应当是 %.4g，实得 %v", i, w, out[i])
		}
	}
	if clamped != 0 {
		t.Errorf("都在 0.3…2.5 里，不该夹边，实得 %d", clamped)
	}
	// 负系数：-0.3 ⇒ 0.5→1.15 / 1→1 / 1.5→0.85
	out2, _ := dynEnvelopeValues(dyn, -0.3, 0.3, 2.5)
	if abs(*out2[0]-1.15) > 1e-12 || abs(*out2[2]-0.85) > 1e-12 {
		t.Errorf("负系数算错了：%v %v", *out2[0], *out2[2])
	}
	// 夹边：下限 1.2 ⇒ 0.5 与 1 两端都被夹到 1.2 并计数（2 个）
	out3, clamped3 := dynEnvelopeValues(dyn, 0.5, 1.2, 2.5)
	if abs(*out3[0]-1.2) > 1e-12 || abs(*out3[1]-1.2) > 1e-12 || clamped3 != 2 {
		t.Errorf("夹边错了：%.4g / %.4g / %d", *out3[0], *out3[1], clamped3)
	}
}
