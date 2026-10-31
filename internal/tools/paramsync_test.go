package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"acetool/internal/acecli"
)

// ---------- 「参数映射」（param.sync / param.view）的测试 ----------
//
// 口径来自 `help vocalparam write` / `help vocalparam read` 的**原文**（2026-10-07 现场读的）：
//
//	· 写是 "Replace a named writable layer's points over a tick range" ⇒ 只替换这一段，范围外不动；
//	· dense 写必须给 `--pos-begin`；
//	· **每一段连续 run 至少 2 个 tick**（值/空档都算，1 tick 直接被拒）；
//	· `effective` 可读**不可写**（merge 规则归引擎）⇒ 源读实参，写只能写具体层；
//	· `fingerprint` 从读里拿、写时用 `--if-match` 守卫。
//
// 用户 2026-10-07 的三条口径（与 AKD 原脚本**不同**，所以旧测试里那几条断言已经删掉）：
// **覆盖**（没有 `+目标值`）、**不做等比映射**、写哪层是**多选**。

// 假工程：片段 8 个 tick（0..8），4 个音符各 2 tick；源 tension 的实参 = 0..7。
const fakeClipLen = 8

type paramFakeOpts struct {
	engine    string // "verse24"（默认）或 "singing-mamba"
	layerVals map[string]string
	withSel   bool
	writeFail bool
}

func newParamFake2(t *testing.T, called *[]string, o paramFakeOpts) ReadFunc {
	t.Helper()
	if o.engine == "" {
		o.engine = "verse24"
	}
	if o.layerVals == nil {
		// energy 的 direct 层：整段画成 10
		o.layerVals = map[string]string{"energy/direct": "[10,10,10,10,10,10,10,10]"}
	}
	roster := func() string {
		if o.engine == "singing-mamba" {
			return `{"engineGeneration":"singing-mamba","vocalControlRoute":"dynamic","params":[
				{"param":"dynamic","displayName":"Dynamic","available":true,"scale":"control",
				 "valueRange":{"min":-1,"max":1},
				 "layers":[{"layer":"user","access":"read-write","role":"override","shape":"points","sparse":true}]},
				{"param":"tension","displayName":"Tension","available":true,"scale":"model",
				 "valueRange":{"min":0,"max":1},
				 "layers":[{"layer":"baseline","access":"read-only","role":"analyzed-pristine","shape":"dense","sparse":false},
				           {"layer":"user","access":"read-write","role":"override","shape":"dense","sparse":true}]},
				{"param":"energy","displayName":"Energy","available":true,"scale":"model",
				 "valueRange":{"min":0,"max":1},
				 "layers":[{"layer":"baseline","access":"read-only","role":"analyzed-pristine","shape":"dense","sparse":false},
				           {"layer":"user","access":"read-write","role":"override","shape":"dense","sparse":true}]},
				{"param":"formant","displayName":"Formant","available":false,"scale":"model",
				 "unavailableReason":"the clip's singing-mamba engine generation has no formant parameter"}]}`
		}
		return `{"engineGeneration":"verse24","vocalControlRoute":"dynamic","params":[
			{"param":"tension","displayName":"Tension","available":true,"scale":"envelope",
			 "valueRange":{"min":0.3,"max":2.5},
			 "layers":[{"layer":"baseline","access":"read-only","role":"synthesized-default","shape":"dense","sparse":false},
			           {"layer":"direct","access":"read-write","role":"override","shape":"dense","sparse":true},
			           {"layer":"envelope","access":"read-write","role":"multiplier","shape":"dense","sparse":false}]},
			{"param":"energy","displayName":"Energy","available":true,"scale":"envelope",
			 "valueRange":{"min":0,"max":20},
			 "layers":[{"layer":"baseline","access":"read-only","role":"synthesized-default","shape":"dense","sparse":false},
			           {"layer":"direct","access":"read-write","role":"override","shape":"dense","sparse":true},
			           {"layer":"envelope","access":"read-write","role":"multiplier","shape":"dense","sparse":false}]},
			{"param":"formant","displayName":"Formant","available":true,"scale":"envelope",
			 "valueRange":{"min":-1,"max":1},
			 "layers":[{"layer":"envelope","access":"read-write","role":"multiplier","shape":"dense","sparse":false}]},
			{"param":"pitch","displayName":"Pitch","available":false,"scale":"semitones",
			 "unavailableReason":"pitch curves are not on this surface yet"}]}`
	}
	// 三层假曲线：effective（源）、energy/direct（目标，整段 10）、envelope（整段 1）
	curveOf := func(key string) []string {
		n := fakeClipLen
		switch {
		case key == "effective":
			out := make([]string, n)
			for i := range out {
				out[i] = strconv.Itoa(i)
			}
			return out
		case o.layerVals[key] != "":
			return strings.Split(strings.Trim(o.layerVals[key], "[]"), ",")
		default:
			out := make([]string, n)
			for i := range out {
				out[i] = "1"
			}
			return out
		}
	}
	return func(_ context.Context, args ...string) (*acecli.Result, error) {
		cmd := strings.Join(args, " ")
		*called = append(*called, cmd)
		if len(args) >= 2 && args[0] == "vocalparam" && args[1] == "write" {
			if o.writeFail {
				return &acecli.Result{ExitCode: acecli.ExitFailed,
					Err: &acecli.Envelope{Code: "INVALID_ARG", Message: "测试里让它失败"}}, nil
			}
			return okJSON(`{"param":"x","layer":"y","posBegin":0,"count":1,"clearedCount":0,"fingerprint":"f2"}`), nil
		}
		switch {
		case len(args) >= 2 && args[0] == "editor" && args[1] == "status":
			return okJSON(`{"isAvailable":true,"trackIndex":0,"clipIndex":0,"clipUuid":"{c1}","clipName":"测试片段","editorType":"sing"}`), nil
		case len(args) >= 2 && args[0] == "clip" && args[1] == "list":
			return okJSON(`{"clips":[{"clipType":"sing","clipName":"测试片段","clipBegin":0,"clipEnd":8,"clipUuid":"{c1}"}]}`), nil
		case len(args) >= 2 && args[0] == "clip" && args[1] == "note-content":
			var rows []string
			for i := 0; i < 4; i++ {
				rows = append(rows, fmt.Sprintf(`{"noteUuid":"{n%d}","pos":%d,"dur":2,"endPos":%d,"pitch":%d,"lyric":"la","language":"Chinese"}`,
					i+1, i*2, i*2+2, 60+i))
			}
			return okJSON(`{"noteCount":4,"notes":[` + strings.Join(rows, ",") + `]}`), nil
		case len(args) >= 2 && args[0] == "selection" && args[1] == "get":
			if !o.withSel {
				return okJSON(`{"hasSelection":false,"selectionCount":0,"notes":[]}`), nil
			}
			return okJSON(`{"hasSelection":true,"selectionCount":2,"notes":[{"noteUuid":"{n2}","pos":2,"dur":2,"endPos":4},{"noteUuid":"{n3}","pos":4,"dur":2,"endPos":6}]}`), nil
		case len(args) >= 2 && args[0] == "timesig":
			return okJSON(`{"signatures":[{"numerator":4,"denominator":4}]}`), nil
		case len(args) >= 2 && args[0] == "tempo":
			return okJSON(`{"tempo":120}`), nil
		case len(args) >= 2 && args[0] == "vocalparam" && args[1] == "layers":
			return okJSON(roster()), nil
		case len(args) >= 2 && args[0] == "vocalparam" && args[1] == "read":
			var param, layer string
			begin, end := int64(0), int64(fakeClipLen)
			for i, a := range args {
				if i+1 >= len(args) {
					continue
				}
				switch a {
				case "--param":
					param = args[i+1]
				case "--layer":
					layer = args[i+1]
				case "--range-begin":
					begin, _ = strconv.ParseInt(args[i+1], 10, 64)
				case "--range-end":
					end, _ = strconv.ParseInt(args[i+1], 10, 64)
				}
			}
			if end > fakeClipLen {
				end = fakeClipLen
			}
			vals := curveOf(param + "/" + layer)
			if layer == "effective" {
				vals = curveOf("effective")
			}
			pts := vals[begin:end]
			if layer == "effective" {
				return okJSON(fmt.Sprintf(`{"posBegin":%d,"count":%d,"scale":"envelope","engineGeneration":"verse24",
					"valueRange":{"min":0.3,"max":2.5},"fingerprint":"fp-eff",
					"effective":{"layer":"effective","shape":"dense","points":[%s]}}`, begin, len(pts), strings.Join(pts, ","))), nil
			}
			return okJSON(fmt.Sprintf(`{"posBegin":%d,"count":%d,"scale":"envelope","engineGeneration":"verse24",
				"valueRange":{"min":0,"max":20},"fingerprint":"fp-%s-%s",
				"layers":[{"layer":"%s","access":"read-write","shape":"dense","sparse":true,"points":[%s]}]}`,
				begin, len(pts), layer, param, layer, strings.Join(pts, ","))), nil
		}
		return nil, fmt.Errorf("假 CLI 没准备这条命令：%s", cmd)
	}
}

// 公式（**叠加**，AKD 原算法 —— 用户 2026-10-07 当晚更正：「改成叠加，原算法，我刚才想错了」）：
//
//	不同参数：新值 = **目标值** + 倍率 × 源值 + 偏移
//	同一参数：新值 = 倍率 × **目标值** + 偏移
//
// ⚠️ 这条与"覆盖"版**故意不同**（我按覆盖实现过一版，被用户否了）⇒ 单独立一条测试钉死。
func TestParamSyncFormulaIsAdditive(t *testing.T) {
	var called []string
	fake := newParamFake2(t, &called, paramFakeOpts{})
	got, err := Get("param.sync").Plan(context.Background(), fake, map[string]string{
		"srcLayer": "effective", "srcParam": "tension", "dstParam": "energy", "layers": "direct",
		"scale": "2", "offset": "1", "range": "clip", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep := got.(*paramSyncReport)
	if rep.SrcFrom != "effective" {
		t.Errorf("源应当读 `effective`（实参），实得 %q", rep.SrcFrom)
	}
	if rep.SamePara {
		t.Error("tension → energy 是不同参数，不该走同一参数那条公式")
	}
	if rep.Ticks != fakeClipLen {
		t.Fatalf("应当算 %d 个 tick，实得 %d", fakeClipLen, rep.Ticks)
	}
	// 源 = 0..7、目标层（energy/direct）整段是 10 ⇒ 10 + 2×源 + 1 = 11,13,…,25
	// 再夹进 energy 的 0..20 ⇒ 21/23/25 那三个撞上界
	want := []float64{11, 13, 15, 17, 19, 20, 20, 20}
	for i, w := range want {
		if math.Abs(deref(rep.SampleAfter[i], -999)-w) > 1e-9 {
			t.Errorf("第 %d 个 tick = %v，期望 %v（= 目标值 10 + 2×源值 + 1，再夹到 20）",
				i, deref(rep.SampleAfter[i], -999), w)
		}
	}
	for _, c := range called {
		if strings.HasPrefix(c, "vocalparam write") {
			t.Errorf("干跑里出现了写命令：%s", c)
		}
	}
}

// 同一参数走另一条分支：新值 = 倍率 × 目标值 + 偏移（**不含源值那一项**）。
func TestParamSyncSameParamScalesItself(t *testing.T) {
	var called []string
	fake := newParamFake2(t, &called, paramFakeOpts{
		layerVals: map[string]string{"tension/direct": "[1,1,1,1,1,1,1,1]"},
	})
	got, err := Get("param.sync").Plan(context.Background(), fake, map[string]string{
		"srcLayer": "effective", "srcParam": "tension", "dstParam": "tension", "layers": "direct",
		"scale": "2", "offset": "0.5", "range": "clip", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep := got.(*paramSyncReport)
	if !rep.SamePara {
		t.Fatal("源与目标同名 ⇒ 要标 SamePara")
	}
	// 目标层整段 1 ⇒ 2×1 + 0.5 = 2.5（源实参 0..7 **不参与**）
	for i := 0; i < fakeClipLen; i++ {
		if math.Abs(deref(rep.SampleAfter[i], -999)-2.5) > 1e-9 {
			t.Errorf("第 %d 个 tick = %v，期望 2.5（= 2×目标值 1 + 0.5）", i, deref(rep.SampleAfter[i], -999))
		}
	}
}

// 筛选不通过的 tick **保持原值**（空档保持空档 —— 这是 sparse 层的要害）。
func TestParamSyncFilterKeepsOriginal(t *testing.T) {
	var called []string
	// energy/direct 前两个 tick 是空档、后面是 10（模拟"只画了后半段"）
	fake := newParamFake2(t, &called, paramFakeOpts{
		layerVals: map[string]string{"energy/direct": "[null,null,10,10,10,10,10,10]"},
	})
	got, err := Get("param.sync").Plan(context.Background(), fake, map[string]string{
		"srcLayer": "effective", "srcParam": "tension", "dstParam": "energy", "layers": "direct",
		"scale": "1", "offset": "0", "filter": "gt", "filterValue": "2.5",
		"range": "clip", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep := got.(*paramSyncReport)
	if rep.Results[0].Filtered != 3 {
		t.Errorf("源 0..2 不通过「> 2.5」⇒ 应当筛掉 3 个 tick，实得 %d", rep.Results[0].Filtered)
	}
	// 前 3 个保持原样（空档还是空档、10 还是 10），后 5 个 = 源值
	if rep.SampleAfter[0] != nil || rep.SampleAfter[1] != nil {
		t.Errorf("被筛掉的 tick 原本是空档 ⇒ 必须保持空档，实得 %v / %v",
			rep.SampleAfter[0], rep.SampleAfter[1])
	}
	if math.Abs(deref(rep.SampleAfter[2], -1)-10) > 1e-9 {
		t.Errorf("被筛掉的 tick 原本是 10 ⇒ 保持 10，实得 %v", deref(rep.SampleAfter[2], -1))
	}
	if math.Abs(deref(rep.SampleAfter[3], -1)-13) > 1e-9 {
		// 叠加：10（目标原值）+ 1×源值 3 + 0 = 13
		t.Errorf("通过的 tick 应当是 目标值10 + 源值3 = 13，实得 %v", deref(rep.SampleAfter[3], -1))
	}
}

// ⛔ 1 tick 的 run 要在**发之前**就被我们挡住（ACE 会拒，白跑一趟要几秒）。
// 造法：源只在中间那一个 tick 不通过筛选、而它两侧都在写 ⇒ 中间落单一个"保持原值"的空档。
func TestParamSyncRefusesOneTickRun(t *testing.T) {
	var called []string
	// energy/direct 全空档：被筛掉的那个 tick 就成了"值海里的一个空档"
	fake := newParamFake2(t, &called, paramFakeOpts{
		layerVals: map[string]string{"energy/direct": "[null,null,null,null,null,null,null,null]"},
	})
	// 源 = 0..7，选「小于 ≤ 3」⇒ 0,1,2,3 通过、4..7 不通过 —— 那是**一段**连续空档，不会落单。
	// 要落单得让中间某一个是空档：用「小于」在源值 3 上只放行 0..2，仍是一段。
	// ⇒ 换成两段筛选：源 > 2 且 源 ≤ 3 ⇒ 只有 tick 3 通过，其余不通过（不通过的相邻、仍是一段）。
	// 真正落单的造法：不通过的是**中间一个**，两侧通过 —— 用 OR：(源 ≤ 1) 或 (源 ≥ 5)
	fake = newParamFake2(t, &called, paramFakeOpts{
		layerVals: map[string]string{"energy/direct": "[null,null,null,null,null,null,null,null]"},
	})
	got, err := Get("param.sync").Plan(context.Background(), fake, map[string]string{
		"srcLayer": "effective", "srcParam": "tension", "dstParam": "energy", "layers": "direct",
		"filter": "lt", "filterValue": "1", // 源 ≤ 1（即 tick 0,1）通过
		"filter2": "or", "filter2Mode": "gt", "filter2Value": "5", // 源 > 5（即 tick 6,7）通过
		"range": "clip", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep := got.(*paramSyncReport)
	// 通过的 tick 是 0,1,6,7；不通过的是 2,3,4,5（一段连续空档）⇒ 两端各一段值（0,1 与 6,7）也都 ≥2
	// ⇒ 这条**不该**被拒（先确认"正常情形不被误杀"）
	if rep.Results[0].Filtered != 4 {
		t.Errorf("应当筛掉 4 个 tick，实得 %d", rep.Results[0].Filtered)
	}
	// 再造真正落单的：源在 tick 3 恰好不通过、两侧通过 —— 用两段筛选做成"中间挖一个洞"
	// （源 = 0..7：`> 1` 且 `≤ 5` ⇒ 2,3,4,5 通过；`> 5` ⇒ 6,7 —— 组合不出单洞）
	// ⇒ 直接验 checkDenseRuns 这条纯函数（下面是它的单测）。
}

// checkDenseRuns：1 tick 的 run（值或空档）都要抓出来，并且**点名是第几个 tick**。
func TestCheckDenseRuns(t *testing.T) {
	f := func(vs ...string) []*float64 {
		out := make([]*float64, len(vs))
		for i, s := range vs {
			if s == "n" {
				continue
			}
			x, _ := strconv.ParseFloat(s, 64)
			out[i] = &x
		}
		return out
	}
	for _, c := range []struct {
		name    string
		payload []*float64
		bad     bool
		at      int
		kind    string
	}{
		{"一整段值", f("1", "2", "3"), false, 0, ""},
		{"一整段空档", f("n", "n"), false, 0, ""},
		{"值里戳一个空档（官方文档点名的例子）", f("0.5", "0.5", "n", "0.5", "0.5"), true, 2, "空档"},
		{"空档里冒一个值", f("n", "n", "0.5", "n", "n"), true, 2, "值"},
		{"载荷边上落单的值", f("0.62", "n", "n", "n"), true, 0, "值"},
		{"两个 tick 是最短的合法形状", f("1", "2"), false, 0, ""},
		{"一个元素", f("1"), true, 0, "值"},
	} {
		at, kind, bad := checkDenseRuns(c.payload)
		if bad != c.bad {
			t.Errorf("%s：checkDenseRuns 判定 %v，期望 %v", c.name, bad, c.bad)
			continue
		}
		if bad && (at != c.at || kind != c.kind) {
			t.Errorf("%s：应当点第 %d 个 tick 的%s，实得第 %d 个的%s", c.name, c.at, c.kind, at, kind)
		}
	}
}

// 范围换算：`barSpan` 把"第几小节"折成片段内 tick（1 起、含两端、与 [0,length) 求交）。
func TestBarSpan(t *testing.T) {
	// 4/4 ⇒ 一小节 1920；片段从工程 tick 0 起、长 3840（两小节）
	for _, c := range []struct {
		name                   string
		b1, b2                 int
		tpb, clipBegin, length int64
		wantLo, wantHi         int64
		wantErr                string
	}{
		{"第 1 小节", 1, 1, 1920, 0, 3840, 0, 1920, ""},
		{"第 1..2 小节", 1, 2, 1920, 0, 3840, 0, 3840, ""},
		{"片段从第 3 小节开始（减 clipBegin）", 3, 3, 1920, 3840, 1920, 0, 1920, ""},
		{"超出片段右边界 ⇒ 夹到片段末尾", 2, 3, 1920, 0, 3840, 1920, 3840, ""},
		{"整段落在片段之外", 5, 6, 1920, 0, 3840, 0, 0, "落在片段之外"},
		{"小节号从 1 起", 0, 1, 1920, 0, 3840, 0, 0, "从 **1** 起"},
		{"结束比开始还小", 3, 2, 1920, 0, 3840, 0, 0, "还小"},
	} {
		lo, hi, err := barSpan(c.b1, c.b2, c.tpb, c.clipBegin, c.length)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s：应当报错含 %q，实得 %v", c.name, c.wantErr, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s：不该报错：%v", c.name, err)
			continue
		}
		if lo != c.wantLo || hi != c.wantHi {
			t.Errorf("%s：得到 [%d,%d)，期望 [%d,%d)", c.name, lo, hi, c.wantLo, c.wantHi)
		}
	}
	// 反算：片段内 tick 落在工程第几小节
	if got := barOf(0, 3840, 1920); got != 3 {
		t.Errorf("片段内 0 + clipBegin 3840 ⇒ 第 3 小节，实得 %d", got)
	}
}

// 「写进哪些层」：留空按目标自己推（v1 优先 direct、v2 只有 user）；写不存在的层要报错并列出有的层。
func TestParseLayerList(t *testing.T) {
	// 直接拿花名册 JSON 解出结构体（比逐字段写匿名 struct 清楚得多）
	var v1 paramRoster
	if err := json.Unmarshal([]byte(`{"engineGeneration":"verse24","params":[
		{"param":"energy","available":true,"layers":[
			{"layer":"baseline","access":"read-only","shape":"dense"},
			{"layer":"direct","access":"read-write","shape":"dense"},
			{"layer":"envelope","access":"read-write","shape":"dense"}]}]}`), &v1); err != nil {
		t.Fatalf("假花名册 JSON 坏了：%v", err)
	}
	var v2 paramRoster
	if err := json.Unmarshal([]byte(`{"engineGeneration":"singing-mamba","params":[
		{"param":"energy","available":true,"layers":[
			{"layer":"baseline","access":"read-only","shape":"dense"},
			{"layer":"user","access":"read-write","shape":"dense"}]}]}`), &v2); err != nil {
		t.Fatalf("假花名册 JSON 坏了：%v", err)
	}
	if got, err := parseLayerList("", &v1, "energy"); err != nil || strings.Join(got, ",") != "envelope" {
		// ⭐ 用户 2026-10-07：默认是**包络**（乘数层、相对改动温和），真值(direct) 要显式勾
		t.Errorf("v1 留空应当默认 envelope，实得 %v / %v", got, err)
	}
	// ⭐ 显示顺序是**规范顺序**（包络在前）：假花名册里写的是 direct,envelope，列表要反过来
	if got := denseWritableLayers(&v1, "energy"); strings.Join(got, ",") != "envelope,direct" {
		t.Errorf("可写层要按规范顺序（包络在前），实得 %v", got)
	}
	// 只有 direct 的场合（假想的引擎）也要能推出来
	var onlyDirect paramRoster
	if err := json.Unmarshal([]byte(`{"engineGeneration":"x","params":[{"param":"energy","available":true,"layers":[
		{"layer":"direct","access":"read-write","shape":"dense"}]}]}`), &onlyDirect); err != nil {
		t.Fatalf("假花名册 JSON 坏了：%v", err)
	}
	if got, err := parseLayerList("", &onlyDirect, "energy"); err != nil || strings.Join(got, ",") != "direct" {
		t.Errorf("只有 direct 时应当落到 direct，实得 %v / %v", got, err)
	}
	if got, err := parseLayerList("envelope,direct", &v1, "energy"); err != nil || strings.Join(got, ",") != "envelope,direct" {
		t.Errorf("多选应当原样接受（顺序也保留），实得 %v / %v", got, err)
	}
	if got, err := parseLayerList("", &v2, "energy"); err != nil || strings.Join(got, ",") != "user" {
		t.Errorf("v2 留空应当落到 user，实得 %v / %v", got, err)
	}
	if _, err := parseLayerList("user", &v1, "energy"); err == nil || !strings.Contains(err.Error(), "envelope、direct") {
		t.Errorf("v1 上没有 user：要报错并列出有的层（按显示顺序），实得 %v", err)
	}
	if _, err := parseLayerList("baseline", &v1, "energy"); err == nil {
		t.Error("baseline 是只读层，不该被接受")
	}
}

// 参数不在花名册里 / 不可用 ⇒ 明确报错（**不按写死的清单猜**）。
func TestParamSyncRejectsUnavailableParam(t *testing.T) {
	var called []string
	fake := newParamFake2(t, &called, paramFakeOpts{})
	_, err := Get("param.sync").Plan(context.Background(), fake, map[string]string{
		"srcLayer": "effective", "srcParam": "pitch", "dstParam": "energy", "range": "clip", "dryRun": "1",
	})
	if err == nil || !strings.Contains(err.Error(), "不可用") {
		t.Fatalf("pitch 不可用应当报错并说清，实得：%v", err)
	}
	_, err = Get("param.sync").Plan(context.Background(), fake, map[string]string{
		"srcLayer": "effective", "srcParam": "loudness", "dstParam": "energy", "range": "clip", "dryRun": "1",
	})
	if err == nil || !strings.Contains(err.Error(), "花名册") {
		t.Errorf("不在花名册里要报错并列出可用的，实得：%v", err)
	}
}

// 值域夹取仍然在工作（ACE 对越界是**直接拒**，所以我们先夹好再发，并如实计数）。
func TestParamSyncClampsToValueRange(t *testing.T) {
	var called []string
	fake := newParamFake2(t, &called, paramFakeOpts{})
	// 目标 energy 的值域是 0..20；源 0..7 × 100 ⇒ 0,100,… 全撞上界
	got, err := Get("param.sync").Plan(context.Background(), fake, map[string]string{
		"srcLayer": "effective", "srcParam": "tension", "dstParam": "energy", "layers": "direct",
		"scale": "100", "offset": "0", "range": "clip", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep := got.(*paramSyncReport)
	// 源值 0 × 100 = 0 —— 它**在** energy 的值域 [0,20] 内，只有 1..7 那七个撞上界
	if rep.Results[0].Clamped != rep.Ticks-1 {
		t.Errorf("应当 7 个 tick 被夹（源 0 不越界），实得 Clamped=%d / Ticks=%d", rep.Results[0].Clamped, rep.Ticks)
	}
	if math.Abs(deref(rep.SampleAfter[3], -1)-20) > 1e-9 {
		t.Errorf("第 4 个 tick 应当夹到上界 20，实得 %v", deref(rep.SampleAfter[3], -1))
	}
}

// 范围：「选中音符的范围」没选中就**明确报错**（不静默退化成整个片段）。
func TestParamSyncSelectionRangeNeedsSelection(t *testing.T) {
	var called []string
	fake := newParamFake2(t, &called, paramFakeOpts{withSel: false})
	_, err := Get("param.sync").Plan(context.Background(), fake, map[string]string{
		"srcLayer": "effective", "srcParam": "tension", "dstParam": "energy", "range": "sel", "dryRun": "1",
	})
	// ⭐ 用户 2026-10-24：没选中音符 ⇒ 按整个片段（不再报错）
	if err != nil {
		t.Errorf("没选区时应退化成整个片段，不该失败：%v", err)
	}
	// 有选区 ⇒ 范围 = 选中音符盖住的那段（pos 2..6）
	fake2 := newParamFake2(t, &called, paramFakeOpts{withSel: true})
	got, err := Get("param.sync").Plan(context.Background(), fake2, map[string]string{
		"srcLayer": "effective", "srcParam": "tension", "dstParam": "energy", "range": "sel", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("有选区时不该失败：%v", err)
	}
	rep := got.(*paramSyncReport)
	if rep.BeginTick != 2 || rep.EndTick != 6 || rep.Ticks != 4 {
		t.Errorf("选区范围应当是片段内 [2,6)，实得 [%d,%d) 共 %d", rep.BeginTick, rep.EndTick, rep.Ticks)
	}
}

// 指定小节：写下去的范围要是**那几小节**（本次假工程 8 tick，一小节 1920 ⇒ 第 1 小节盖住整段）。
func TestParamSyncBarsRange(t *testing.T) {
	var called []string
	fake := newParamFake2(t, &called, paramFakeOpts{})
	got, err := Get("param.sync").Plan(context.Background(), fake, map[string]string{
		"srcLayer": "effective", "srcParam": "tension", "dstParam": "energy", "range": "bars",
		"beginBar": "1", "endBar": "1", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep := got.(*paramSyncReport)
	if rep.BeginTick != 0 || rep.EndTick != fakeClipLen {
		t.Errorf("第 1 小节应当盖住整段 [0,%d)，实得 [%d,%d)", fakeClipLen, rep.BeginTick, rep.EndTick)
	}
	if !strings.Contains(rep.Bars, "1..1 小节") || !strings.Contains(rep.Bars, "4/4") {
		t.Errorf("报告里要写清是第几小节、一小节多少 tick、什么拍号，实得 %q", rep.Bars)
	}
	// 片段之外的请求要报错（假工程只有 8 tick，都在第 1 小节里）
	_, err = Get("param.sync").Plan(context.Background(), fake, map[string]string{
		"srcLayer": "effective", "srcParam": "tension", "dstParam": "energy", "range": "bars",
		"beginBar": "3", "endBar": "4", "dryRun": "1",
	})
	if err == nil || !strings.Contains(err.Error(), "落在片段之外") {
		t.Errorf("片段外的小节要报错，实得：%v", err)
	}
}

// 真写：**逐层各发一条** `vocalparam write`，而且带上 `--pos-begin` 与 `--if-match`。
func TestParamSyncApplyWritesEveryLayer(t *testing.T) {
	var called []string
	fake := newParamFake2(t, &called, paramFakeOpts{})
	got, err := Get("param.sync").Apply(context.Background(), fake, map[string]string{
		"srcLayer": "effective", "srcParam": "tension", "dstParam": "energy", "layers": "direct,envelope",
		"scale": "1", "offset": "0", "range": "clip",
	})
	if err != nil {
		t.Fatalf("Apply 不该返回 err：%v", err)
	}
	rep := got.(*paramSyncReport)
	if len(rep.Did) != 2 {
		t.Fatalf("勾了两层就该写两条，实得 %v", rep.Did)
	}
	var writes []string
	for _, c := range called {
		if strings.HasPrefix(c, "vocalparam write") {
			writes = append(writes, c)
		}
	}
	if len(writes) != 2 {
		t.Fatalf("应当正好两条写命令，实得 %v", writes)
	}
	for _, w := range writes {
		if !strings.Contains(w, "--pos-begin 0") {
			t.Errorf("dense 写必须给 --pos-begin：%s", w)
		}
		if !strings.Contains(w, "--if-match fp-") {
			t.Errorf("应当带上读回来的指纹做守卫：%s", w)
		}
		if !strings.Contains(w, "--points @") {
			t.Errorf("曲线要走临时文件（不该塞在命令行上）：%s", w)
		}
	}
	if !strings.Contains(writes[0], "--layer direct") || !strings.Contains(writes[1], "--layer envelope") {
		t.Errorf("两条应当分别写 direct 与 envelope，实得 %v", writes)
	}
}

// v2（`singing-mamba`）：层只有 `baseline`(只读) + `user`(读写) —— **没有** envelope/direct。
// 页面上那两个勾选框在 v2 不出现，不是我们藏起来，是它真没有；这里钉住工具侧的判据。
func TestParamSyncV2HasOnlyUserLayer(t *testing.T) {
	var called []string
	fake := newParamFake2(t, &called, paramFakeOpts{engine: "singing-mamba"})
	got, err := Get("param.sync").Plan(context.Background(), fake, map[string]string{
		"srcLayer": "effective", "srcParam": "tension", "dstParam": "energy", "range": "clip", "dryRun": "1",
	})
	if err != nil {
		t.Fatalf("干跑不该失败：%v", err)
	}
	rep := got.(*paramSyncReport)
	if rep.Engine != "singing-mamba" {
		t.Errorf("引擎世代要如实带回去，实得 %q", rep.Engine)
	}
	if strings.Join(rep.Layers, ",") != "user" {
		t.Errorf("v2 留空应当落到 user 层，实得 %v", rep.Layers)
	}
	if _, err := Get("param.sync").Plan(context.Background(), fake, map[string]string{
		"srcLayer": "effective", "srcParam": "tension", "dstParam": "energy", "layers": "envelope", "range": "clip", "dryRun": "1",
	}); err == nil {
		t.Error("v2 上没有 envelope 层，指定它就该报错")
	}
	// v2 的 formant 不可用（引擎那句话要原样带出来）
	_, err = Get("param.sync").Plan(context.Background(), fake, map[string]string{
		"srcLayer": "effective", "srcParam": "formant", "dstParam": "energy", "range": "clip", "dryRun": "1",
	})
	if err == nil || !strings.Contains(err.Error(), "singing-mamba") {
		t.Errorf("v2 的 formant 不可用要带出引擎给的原因，实得：%v", err)
	}
}

// 源参数**没有 effective、而且可写层是锚点形状**（真机：v2 的 `dynamic` 只有 `user`/`points`）时，
// 报错必须把**真实形状**摆出来，别只回一句 CLI 的 INVALID_ARG —— 用户要的是"那我该换什么"。
func TestParamSyncExplainsAnchorShapedSource(t *testing.T) {
	// 让 effective 读失败、同时 dynamic 只有一个 points 形状的 user 层
	var called []string
	inner := newParamFake2(t, &called, paramFakeOpts{engine: "singing-mamba"})
	fake := func(ctx context.Context, args ...string) (*acecli.Result, error) {
		cmd := strings.Join(args, " ")
		if strings.Contains(cmd, "--param dynamic") {
			called = append(called, cmd)
			return &acecli.Result{ExitCode: acecli.ExitFailed,
				Err: &acecli.Envelope{Code: "INVALID_ARG", Message: "Parameter 'dynamic' has no layer 'effective'"}}, nil
		}
		return inner(ctx, args...)
	}
	_, err := Get("param.sync").Plan(context.Background(), fake, map[string]string{
		"srcLayer": "effective", "srcParam": "dynamic", "dstParam": "energy", "range": "clip", "dryRun": "1",
	})
	if err == nil {
		t.Fatal("dynamic 当源（没有 effective、层是锚点形状）应当报错")
	}
	for _, want := range []string{"没有 `effective`", "user/points", "原始错误"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报错里要提到 %q（让人知道为什么、该换什么），实得：%v", want, err)
		}
	}
}

// param.view：页面要的那一包（同一份快照：tick 长度、拍号、音符、选区、花名册）。
func TestParamViewBundle(t *testing.T) {
	var called []string
	fake := newParamFake2(t, &called, paramFakeOpts{withSel: true})
	got, err := Get("param.view").Inspect(context.Background(), fake, map[string]string{})
	if err != nil {
		t.Fatalf("param.view 不该失败：%v", err)
	}
	v, ok := got.(*paramViewReport)
	if !ok {
		t.Fatalf("应当回 *paramViewReport，实得 %T", got)
	}
	if v.ClipUUID != "{c1}" || v.Length != fakeClipLen {
		t.Errorf("片段 uuid/长度不对：%+v", v)
	}
	if v.TicksPerBar != 1920 || v.Meter != "4/4" {
		t.Errorf("4/4 应当算出 1920 tick/小节，实得 tpb=%d meter=%q", v.TicksPerBar, v.Meter)
	}
	if len(v.Notes) != 4 || v.MinPitch != 60 || v.MaxPitch != 63 {
		t.Errorf("音符/音域不对：%d 个，%d..%d", len(v.Notes), v.MinPitch, v.MaxPitch)
	}
	if !v.HasSelection || v.SelBegin != 2 || v.SelEnd != 6 || v.SelNotes != 2 {
		t.Errorf("选区范围应当是片段内 [2,6)（2 个音符），实得 %+v", v)
	}
	if v.Engine != "verse24" {
		t.Errorf("引擎世代要带回来，实得 %q", v.Engine)
	}
	byParam := map[string]paramInfo{}
	for _, p := range v.Params {
		byParam[p.Param] = p
	}
	if strings.Join(byParam["energy"].Writable, ",") != "envelope,direct" {
		t.Errorf("energy 可写 dense 层应当是 envelope,direct（规范顺序：包络在前），实得 %v", byParam["energy"].Writable)
	}
	if strings.Join(byParam["formant"].Writable, ",") != "envelope" {
		t.Errorf("formant 只有 envelope 可写，实得 %v", byParam["formant"].Writable)
	}
	if byParam["pitch"].Available {
		t.Error("pitch 在这一份 clip 上不可用，页面上不该给")
	}
	for _, c := range called {
		if strings.HasPrefix(c, "vocalparam write") {
			t.Errorf("param.view 是只读的，不该出现写命令：%s", c)
		}
	}
}
