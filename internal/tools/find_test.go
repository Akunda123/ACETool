package tools

import (
	"reflect"
	"strings"
	"testing"
)

// ★ 两条不变量，方向相反，一起立才完整：
//
//	A. **下拉里的每一项都必须真能用** —— 用户 2026-10-04 裁定「把 ace 没有的 key 去掉」，
//	   所以下拉 = 原脚本能实现的 6 条 + ACE 独有 12 条 = 18 条，**一条拒绝的都不许在里面**。
//	B. **做不到的那 16 条仍然要被明确拒绝**（不是"认不出"）—— 直接拿 API 调它们时，
//	   必须得到「ACE 里没有对应概念 + 原因」，不能退化成 0 命中。
//
// 为什么值得单独立：漏写 case 的失败方式是**静默的**（永远 0 命中、不报错）。
func TestEveryAttrOptionIsAnswered(t *testing.T) {
	attr := attrParam(t)
	// 让每个分支都走到"能比较"的那一步（音素、换气都当成已读到）
	n := noteRow{PhRead: true, BreathRead: true, Breath: breathRow{RequestedSec: 0.3, EffectiveSec: 0.3}}
	ref := clipRef{ClipType: "sing", Enabled: true}
	for _, o := range attr.Options {
		if _, _, err := matchNote(o.Value, "eq", "0", n, ref); err != nil {
			t.Errorf("属性 %q（下拉标签 %q）在下拉里却不能用：%v —— 下拉只该放 ACE 真能做的", o.Value, o.Label, err)
		}
	}
	// A：下拉 = 6 + 12，顺序也是
	if len(attr.Options) != 18 {
		t.Fatalf("下拉应当是 6 + 12 = 18 条，实得 %d", len(attr.Options))
	}
	for i, o := range svAttrSupported {
		if attr.Options[i].Value != o.Value || attr.Options[i].Label != o.Label {
			t.Errorf("第 %d 项应当是原脚本可用的 %q/%q，实得 %q/%q",
				i, o.Value, o.Label, attr.Options[i].Value, attr.Options[i].Label)
		}
	}
	for i, o := range aceAttrOptions {
		got := attr.Options[len(svAttrSupported)+i]
		if got.Value != o.Value || got.Label != o.Label {
			t.Errorf("ACE 独有 第 %d 项应当是 %q/%q，实得 %q/%q", i, o.Value, o.Label, got.Value, got.Label)
		}
	}
	// ⚠️ 用户 2026-10-14：「把参数前的星号去掉」⇒ 那 12 条**不再带 `★ ` 前缀**
	//（只有 `ace.` 前缀还在 —— 那是给 API 回包区分的，不是界面标签）
	for _, o := range aceAttrOptions {
		if !strings.HasPrefix(o.Value, "ace.") {
			t.Errorf("ACE 独有属性的 value 要以 ace. 开头：%q/%q", o.Value, o.Label)
		}
		if strings.Contains(o.Label, "★") {
			t.Errorf("标签里不该再有 ★ 了：%q", o.Label)
		}
	}
	// B：做不到的那 16 条**不许在下拉里**，但调它们仍然要被明确拒绝
	inDropdown := map[string]bool{}
	for _, o := range attr.Options {
		inDropdown[o.Value] = true
	}
	for _, o := range svAttrOptions {
		if inDropdown[o.Value] {
			continue
		}
		if _, inRefused := unsupportedAttr[o.Value]; !inRefused {
			t.Errorf("原脚本属性 %q 既不在下拉里、也不在拒绝表里 —— 拿 API 调它会退化成「属性认不出」", o.Value)
		}
		_, _, err := matchNote(o.Value, "eq", "0", n, ref)
		if err == nil {
			t.Errorf("属性 %q 不在下拉里，却又能用 —— 该放回下拉", o.Value)
			continue
		}
		if !strings.Contains(err.Error(), "ACE 里没有对应概念") {
			t.Errorf("属性 %q 的拒绝话术应当是「ACE 里没有对应概念…」，实得：%v", o.Value, err)
		}
		// 拒绝话术里要有**中文名**（attrLabel 要能退到全集里查）
		if label := attrLabel(o.Value); label == o.Value {
			t.Errorf("属性 %q 的中文名查不到 —— 拒绝话术会变成「「%s」在 ACE 里没有…」", o.Value, o.Value)
		}
	}
}

// 拒绝表的键必须**正好**是「原脚本 22 条 − 能实现的 6 条」= 16 条（多一条少一条都是漂移）。
func TestUnsupportedSetIsExactComplement(t *testing.T) {
	supported := map[string]bool{}
	for _, o := range svAttrSupported {
		supported[o.Value] = true
	}
	want := map[string]bool{}
	for _, o := range svAttrOptions {
		if !supported[o.Value] {
			want[o.Value] = true
		}
	}
	if len(want) != 16 {
		t.Fatalf("原脚本 22 条减去能实现的 6 条应当是 16 条，实得 %d", len(want))
	}
	if len(unsupportedAttr) != len(want) {
		t.Errorf("拒绝表有 %d 条，应当是 %d 条", len(unsupportedAttr), len(want))
	}
	for k := range want {
		if _, ok := unsupportedAttr[k]; !ok {
			t.Errorf("原脚本做不到的 %q 不在拒绝表里", k)
		}
	}
	for k := range unsupportedAttr {
		if !want[k] {
			t.Errorf("拒绝表里的 %q 不是「原脚本做不到」的那一类", k)
		}
	}
}

// ACE 独有的 12 条（★）要真的能用 —— 逐条给一个"应当命中"的音符验一遍。
func TestACEExtraAttrsMatch(t *testing.T) {
	n := noteRow{
		Dur: 960, EndPos: 1920, Syllable: "l an",
		HeadCons: []float64{0.05, 0.0201}, TailCons: []float64{0.1},
		BreathRead: true, Breath: breathRow{
			HasBreath: true, RequestedSec: 0.4, EffectiveSec: 0.3213, InsertableSpaceSec: 0.5},
	}
	sing := clipRef{ClipType: "sing", Enabled: true}
	cases := []struct{ attr, cond, want string }{
		{"ace.phOverride", "eq", "有"},
		{"ace.headSec", "gt", "0.06"}, // 0.0701
		{"ace.tailSec", "eq", "0.1"},
		// ⚠️ 单位口径（用户 2026-10-14）：对外一律「拍」⇒ 这两个属性的**值**是 拍 = tick/480
		//（音符的 dur 是 960 tick = 2 拍；endPos 1920 tick = 4 拍）
		{"ace.durTick", "eq", "2"},
		{"ace.endPos", "eq", "4"},
		{"ace.clipType", "eq", "sing"},
		{"ace.clipEnabled", "eq", "有"},
		{"ace.hasBreath", "eq", "有"},
		{"ace.breathRequested", "gt", "0.39"},
		{"ace.breathEffective", "lt", "0.4"},
		{"ace.breathSqueezed", "eq", "有"}, // 0.4 > 0.3213 ⇒ 被压掉
		{"ace.breathSpace", "eq", "0.5"},
	}
	for _, c := range cases {
		ok, val, err := matchNote(c.attr, c.cond, c.want, n, sing)
		if err != nil || !ok {
			t.Errorf("%s %s %q 应当命中，实得 ok=%v val=%q err=%v", c.attr, c.cond, c.want, ok, val, err)
		}
		if val == "" {
			t.Errorf("%s 命中了却没给「数值」显示文本", c.attr)
		}
	}
	// 不该命中的也别乱命中
	if ok, _, _ := matchNote("ace.hasBreath", "eq", "无", n, sing); ok {
		t.Error("有换气不该被「无」命中")
	}
	if ok, _, _ := matchNote("ace.clipEnabled", "eq", "无", n, sing); ok {
		t.Error("片段启用不该被「无」命中")
	}
	if ok, _, _ := matchNote("ace.phOverride", "eq", "有", noteRow{Syllable: ""}, sing); ok {
		t.Error("syllable 为空的音符不该算「有音素覆盖」")
	}
	// 头辅音是**数组求和**（0.05 + 0.0201 = 0.0701）
	if ok, _, _ := matchNote("ace.headSec", "eq", "0.0701", n, sing); !ok {
		t.Error("头辅音长度应当是数组求和")
	}
	// 换气那几项在没读到 `breath list` 时必须**明确要求**，不能当成 0
	for _, a := range []string{"ace.hasBreath", "ace.breathRequested", "ace.breathEffective", "ace.breathSqueezed", "ace.breathSpace"} {
		if _, _, err := matchNote(a, "eq", "0", noteRow{BreathRead: false}, sing); err == nil {
			t.Errorf("%s 在没读到换气时应当报错，而不是拿 0 去比", a)
		}
	}
	// 数值类 ★ 属性也要拦住「包含」
	if _, _, err := matchNote("ace.headSec", "contains", "0", n, sing); err == nil {
		t.Error("头辅音长度用「包含」应当报错")
	}
	// needsBreath / needPhonemes 的判据要和实现一致（漏一个就白花或漏读）
	for _, o := range aceAttrOptions {
		wantBreath := strings.HasPrefix(o.Value, "ace.hasBreath") || strings.HasPrefix(o.Value, "ace.breath")
		if needsBreath(o.Value) != wantBreath {
			t.Errorf("%s: needsBreath=%v，期望 %v", o.Value, needsBreath(o.Value), wantBreath)
		}
		if needPhonemes(o.Value) {
			t.Errorf("%s 是 ACE 独有属性，不该要求 phoneme list", o.Value)
		}
	}
}

// 被拒绝的那些，报错必须**说清 ACE 缺什么**（不是一句"不支持"）。
func TestRefusedAttrsExplainWhy(t *testing.T) {
	for attr, why := range unsupportedAttr {
		if len([]rune(why)) < 15 {
			t.Errorf("属性 %q 的说明太短，等于没说：%q", attr, why)
		}
		_, _, err := matchNote(attr, "eq", "0", noteRow{PhRead: true, BreathRead: true}, clipRef{})
		if err == nil {
			t.Errorf("属性 %q 应当被拒绝", attr)
			continue
		}
		if !strings.Contains(err.Error(), why) {
			t.Errorf("属性 %q 的报错没带上说明原文：%v", attr, err)
		}
	}
	// 反向：说明表里不该混进能实现的属性
	for _, o := range attrParam(t).Options {
		if _, bad := unsupportedAttr[o.Value]; bad {
			continue
		}
		if _, _, err := matchNote(o.Value, "eq", "0", noteRow{PhRead: true, BreathRead: true, Breath: breathRow{}}, clipRef{}); err != nil {
			t.Errorf("属性 %q 不在拒绝表里，却报错：%v", o.Value, err)
		}
	}
	// 拒绝表里的 16 条必须**都是原脚本的**（ACE 独有的 12 条不许拒绝 —— 那些是我们自己加的）
	for attr := range unsupportedAttr {
		found := false
		for _, o := range svAttrOptions {
			if o.Value == attr {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("拒绝表里的 %q 不是原脚本的属性 —— 自己加的属性不该有「拒绝」这种状态", attr)
		}
	}
}

// 音素那两个属性：**没读到 `phoneme list` 时要明确要求**，不能当成空串比。
func TestPhonemeAttrsNeedTheRead(t *testing.T) {
	for _, attr := range []string{"phRaw", "phDefault"} {
		if _, _, err := matchNote(attr, "eq", "a", noteRow{PhRead: false}, clipRef{}); err == nil {
			t.Errorf("%s 在没读到音素时应当报错", attr)
		}
	}
	n := noteRow{PhRead: true, PhRaw: "b iy", PhDefault: "b iy"}
	if ok, val, err := matchNote("phRaw", "contains", "iy", n, clipRef{}); err != nil || !ok || val != "b iy" {
		t.Errorf("phRaw contains iy = %v,%q,%v", ok, val, err)
	}
	// 「实际发音」和「默认发音」是两个不同的字段：改过音素的音符上它们会不一样
	n2 := noteRow{PhRead: true, PhRaw: "s iy", PhDefault: "s i"}
	if ok, _, _ := matchNote("phRaw", "eq", "s iy", n2, clipRef{}); !ok {
		t.Error("phRaw 应当比「实际音素」")
	}
	if ok, _, _ := matchNote("phDefault", "eq", "s iy", n2, clipRef{}); ok {
		t.Error("phDefault 不该比「实际音素」—— 它是没编辑过时的音素")
	}
}

// 数值比较：等于就是全等（原脚本口径），并且「包含」对数值必须被拦住。
func TestCmpAndContains(t *testing.T) {
	if !cmp(1, "gt", 0.5) || cmp(0.5, "gt", 0.5) {
		t.Error("大于")
	}
	if !cmp(0.5, "lt", 1) || cmp(1, "lt", 1) {
		t.Error("小于")
	}
	if !cmp(1, "eq", 1) || cmp(1, "eq", 1.0000001) {
		t.Error("等于应当是全等")
	}
	if _, _, err := matchNote("duration", "contains", "1", noteRow{Dur: 480}, clipRef{}); err == nil {
		t.Error("数值属性用「包含」应当报错")
	}
	if _, _, err := matchPitch(60, "contains", "1"); err == nil {
		t.Error("音高用「包含」应当报错")
	}
	// 持续时间口径：480 tick = 1 拍
	if ok, val, err := matchNote("duration", "eq", "1", noteRow{Dur: 480}, clipRef{}); err != nil || !ok || val != "1" {
		t.Errorf("480 tick 应当是 1 拍，实得 %v,%q,%v", ok, val, err)
	}
	if ok, _, _ := matchNote("duration", "gt", "0.5", noteRow{Dur: 960}, clipRef{}); !ok {
		t.Error("960 tick = 2 拍 > 0.5 拍")
	}
}

// 音高的四种写法都要**经 matchNote 走通**（不只是 pitchSpec 认得）。
func TestMatchPitchThroughAttr(t *testing.T) {
	cs4 := noteRow{Pitch: 61} // C#4
	for _, w := range []string{"c#4", "db4"} {
		if ok, val, err := matchNote("pitch", "eq", w, cs4, clipRef{}); err != nil || !ok || val != "C#4" {
			t.Errorf("音高 %q = %v,%q,%v，期望命中 C#4", w, ok, val, err)
		}
	}
	if ok, _, _ := matchNote("pitch", "eq", "黑", cs4, clipRef{}); !ok {
		t.Error("C#4 是黑键")
	}
	if ok, _, _ := matchNote("pitch", "eq", "白", cs4, clipRef{}); ok {
		t.Error("C#4 不是白键")
	}
	// 只给音级：所有八度都算
	if ok, _, _ := matchNote("pitch", "eq", "C#", noteRow{Pitch: 73}, clipRef{}); !ok {
		t.Error("只给音级应当匹配同名音（任意八度）")
	}
	if ok, _, _ := matchNote("pitch", "eq", "60", noteRow{Pitch: 60}, clipRef{}); !ok {
		t.Error("MIDI 数字写法")
	}
}

// 用户裁定（2026-10-04）：**不提供按 noteUuid 查**。
// uuid 只在结果行里给出（`find.select` 全选要用），不作为可查属性 —— 它是内部句柄，不是音乐属性。
func TestNoUUIDAttribute(t *testing.T) {
	for _, o := range attrParam(t).Options {
		if o.Value == "uuid" {
			t.Error("属性下拉里不该有 uuid")
		}
	}
	if _, _, err := matchNote("uuid", "eq", "x", noteRow{}, clipRef{}); err == nil {
		t.Error("attr=uuid 应当被明确拒绝")
	} else if !strings.Contains(err.Error(), "认不出") {
		t.Errorf("拒绝的话术应当是「属性认不出」，实得：%v", err)
	}
	// 而结果行里必须仍然带 noteUuid —— 全选要靠它
	if got := jsonTagOf(t, HitRow{}, "UUID"); got != "noteUuid" {
		t.Errorf("HitRow.UUID 的 json tag 应当是 noteUuid，实得 %q", got)
	}
}

// 音高输入法：这些写法都得**被认出来**（移植自 SV 原脚本那套）。
func TestPitchSpecForms(t *testing.T) {
	for _, w := range []string{"c", "C", "c#", "db", "黑", "白", "black", "white", "C4", "c#4", "Db4", "c-1"} {
		if _, label, found := pitchSpec(w); !found {
			t.Errorf("pitchSpec 应当认出 %q", w)
		} else if label == "" {
			t.Errorf("%q 认出来了但没给说明", w)
		}
	}
	// 不是音高的东西不该被当成音高
	for _, w := range []string{"啦", "x9", ""} {
		if _, _, found := pitchSpec(w); found {
			t.Errorf("%q 不该被认成音高写法", w)
		}
	}
	if p, o := parsePitchName("c4"); !o || p != 60 {
		t.Errorf("c4 = %d,%v，期望 60,true", p, o)
	}
	if p, o := parsePitchName("db4"); !o || p != 61 {
		t.Errorf("db4 = %d,%v，期望 61,true（Db4 = C#4）", p, o)
	}
	if p, o := parsePitchName("c-1"); !o || p != 0 {
		t.Errorf("c-1 = %d,%v，期望 0,true（最低 MIDI）", p, o)
	}
	if pc, o := parsePitchClass("c"); !o || pc != 0 {
		t.Errorf("音级 c = %d,%v", pc, o)
	}
	if pc, o := parsePitchClass("bb"); !o || pc != 10 {
		t.Errorf("音级 bb = %d,%v，期望 10", pc, o)
	}
	if isBlackKey(60) || isBlackKey(62) {
		t.Error("C4 / D4 是白键")
	}
	if !isBlackKey(61) || !isBlackKey(63) {
		t.Error("C#4 / D#4 是黑键")
	}
}

func TestBarBeat(t *testing.T) {
	sigs := []sigEntry{{BarPos: 0, Tick: 0, Numerator: 4, Denominator: 4}}
	for tick, want := range map[int64]string{0: "1.1.0", 480: "1.2.0", 1920: "2.1.0", 12000: "7.2.0"} {
		if got := barBeat(tick, sigs); got != want {
			t.Errorf("barBeat(%d) = %q，期望 %q", tick, got, want)
		}
	}
	if got := barBeat(0, nil); got != "" {
		t.Errorf("没有拍号表时应当回空串，实得 %q", got)
	}
	// 拍号中途变：3/4 从第 5 小节起
	sigs2 := []sigEntry{
		{BarPos: 0, Tick: 0, Numerator: 4, Denominator: 4},
		{BarPos: 4, Tick: 4 * 1920, Numerator: 3, Denominator: 4},
	}
	if got := barBeat(4*1920, sigs2); got != "5.1.0" {
		t.Errorf("拍号变更点 = %q，期望 5.1.0", got)
	}
	if got := barBeat(4*1920+3*480, sigs2); got != "6.1.0" {
		t.Errorf("3/4 下一小节 = %q，期望 6.1.0", got)
	}
}

// 有/无 这类值要宽容（也收 y/n、true/false、是/否）。
func TestBoolWant(t *testing.T) {
	for _, w := range []string{"有", "是", "yes", "y", "true", "1"} {
		if b, err := boolWant(w); err != nil || !b {
			t.Errorf("boolWant(%q) = %v,%v，期望 true", w, b, err)
		}
	}
	for _, w := range []string{"无", "否", "no", "n", "false", "0", ""} {
		if b, err := boolWant(w); err != nil || b {
			t.Errorf("boolWant(%q) = %v,%v，期望 false", w, b, err)
		}
	}
	if _, err := boolWant("大概有吧"); err == nil {
		t.Error("认不出的值应当报错（而不是当成 false）")
	}
}

func TestLangKeyword(t *testing.T) {
	for w, want := range map[string]string{
		"中": "Chinese", "普": "Chinese", "汉": "Chinese", "mandarin": "Chinese",
		"英": "English", "english": "English", "日": "Japanese",
		"粤": "Cantonese", "广": "Cantonese", "西": "Spanish", "韩": "Korean", "朝": "Korean",
	} {
		if got := langKeyword(w); got != want {
			t.Errorf("langKeyword(%q) = %q，期望 %q", w, got, want)
		}
	}
	// 认不出的原样返回（好让上层按字面比）
	if got := langKeyword("klingon"); got != "klingon" {
		t.Errorf("认不出应当原样返回，实得 %q", got)
	}
}

// ---------- 参数层（dense / points）----------

// 载荷形状由 `shape` 决定，**两种形状不能混**：
// dense = 每 tick 一个值（含 null 空档）；points = [[tick, value], …] 锚点。
func TestLayerPayloadShapes(t *testing.T) {
	dense := vocalLayer{Shape: "dense", Sparse: true,
		Points: []byte(`[null,null,0.5,0.5,null]`)}
	pts, ok := dense.dense()
	if !ok || len(pts) != 5 {
		t.Fatalf("dense 解析失败：%v %d", ok, len(pts))
	}
	if pts[0] != nil || pts[2] == nil || *pts[2] != 0.5 || pts[4] != nil {
		t.Error("dense 的 null 空档没保住")
	}
	if _, ok := dense.anchors(); ok {
		t.Error("dense 不该被当成 points")
	}
	if !hasAnyValue(dense) {
		t.Error("dense 里有 0.5，应当算「有值」")
	}
	allNull := vocalLayer{Shape: "dense", Sparse: true, Points: []byte(`[null,null,null]`)}
	if hasAnyValue(allNull) {
		t.Error("全 null 的 sparse 层应当算「没画过」")
	}

	anchors := vocalLayer{Shape: "points",
		Points: []byte(`[[0,0.5],[960,null],[1920,-0.2]]`)}
	as, ok := anchors.anchors()
	if !ok || len(as) != 3 {
		t.Fatalf("points 解析失败：%v %d", ok, len(as))
	}
	if as[0][0] == nil || *as[0][0] != 0 || as[0][1] == nil || *as[0][1] != 0.5 {
		t.Error("锚点 [0,0.5] 没解对")
	}
	if as[1][1] != nil {
		t.Error("锚点 [960,null] 是空档标记，值应当是 nil")
	}
	if _, ok := anchors.dense(); ok {
		t.Error("points 不该被当成 dense")
	}
	// 坏载荷不许崩，也不许冒充解析成功
	for _, bad := range []vocalLayer{
		{Shape: "dense", Points: []byte(`{"nope":1}`)},
		{Shape: "points", Points: []byte(`[1,2,3]`)},
		{Shape: "dense"},
	} {
		if _, ok := bad.dense(); ok {
			t.Errorf("坏 dense 载荷 %s 不该解析成功", bad.Points)
		}
	}
}

// 花名册（`vocalparam layers`）是**可用性的唯一事实源**：参数在不在、哪一层能用，全看它。
// 技能 ace-params §1 的硬纪律：**别按参数名猜**（引擎世代不同，层集合与尺度都变）。
func TestRosterGates(t *testing.T) {
	roster := vocalRoster{EngineGeneration: "verse24", ParamCount: 2, Params: []vocalParamInfo{
		{Param: "tension", Available: true, Scale: "envelope",
			ValueRange: struct {
				Min float64 `json:"min"`
				Max float64 `json:"max"`
			}{0.3, 2.5},
			Layers: []vocalLayer{
				{Layer: "baseline", Access: "read-only", Shape: "dense"},
				{Layer: "direct", Access: "read-write", Shape: "dense", Sparse: true},
				{Layer: "envelope", Access: "read-write", Shape: "dense"},
			}},
		{Param: "pitch", Available: false, Scale: "semitones",
			UnavailableReason: "pitch curves are not on this surface yet"},
	}}
	if findRosterParam(roster, "tension") == nil {
		t.Fatal("tension 应当在花名册里")
	}
	if findRosterParam(roster, "formant") != nil {
		t.Error("花名册里没有 formant，不该凭空找到")
	}
	if p := findRosterParam(roster, "pitch"); p == nil || p.Available || p.UnavailableReason == "" {
		t.Error("pitch 应当是 available:false 且带原因")
	}
	info := findRosterParam(roster, "tension")
	for _, l := range []string{"baseline", "direct", "envelope", "effective"} {
		if !layerAvailable(info, l) {
			t.Errorf("tension 应当有 %s 层（effective 是合并结果，永远可选）", l)
		}
	}
	if layerAvailable(info, "user") {
		t.Error("v1（verse24）没有 user 层 —— 别把 v2 的层集合外推")
	}
	// pitch 没有层：除了 effective，别的都不该通过
	pitch := findRosterParam(roster, "pitch")
	for _, l := range []string{"baseline", "direct", "envelope", "user"} {
		if layerAvailable(pitch, l) {
			t.Errorf("pitch 的 layers 是空的，不该有 %s 层", l)
		}
	}
}

// pickLayer：effective 来自**顶层对象**（不在 layers 矩阵里，且 dynamic 根本没有它）。
func TestPickLayer(t *testing.T) {
	eff := &vocalLayer{Layer: "effective", Shape: "dense", Points: []byte(`[1,1]`)}
	vr := vocalRead{PosBegin: 0, Layers: []vocalLayer{
		{Layer: "baseline", Shape: "dense", Points: []byte(`[0,0]`)},
		{Layer: "direct", Shape: "dense", Sparse: true, Points: []byte(`[null,null]`)},
	}, Effective: eff}

	if got := pickLayer(vr, "effective"); got == nil || got.Layer != "effective" {
		t.Error("effective 应当从顶层字段取")
	}
	if got := pickLayer(vr, "direct"); got == nil || !got.Sparse {
		t.Error("direct 应当从 layers 里取")
	}
	if got := pickLayer(vr, "user"); got != nil {
		t.Error("v1 没有 user 层，应当回 nil（而不是拿错的层）")
	}
	// dynamic 没有 effective
	if got := pickLayer(vocalRead{}, "effective"); got != nil {
		t.Error("没有 effective 时应当回 nil")
	}
}

// ⛔ `paramLayer=auto` 必须**跨引擎世代都能落地**。
//
// 这条是**真机踩出来的**：默认层原来写死 `direct`（v1），项目换到 v2（`singing-mamba`）的 clip 之后
// `tension` 直接查不了 ——「`tension` 上没有「direct」这一层」。默认值不该随引擎换代就整个失效。
func TestResolveLayerAuto(t *testing.T) {
	v1 := &vocalParamInfo{Param: "tension", Available: true, Layers: []vocalLayer{
		{Layer: "baseline", Access: "read-only", Role: "synthesized-default", Shape: "dense"},
		{Layer: "direct", Access: "read-write", Role: "override", Shape: "dense", Sparse: true},
		{Layer: "envelope", Access: "read-write", Role: "multiplier", Shape: "dense"},
	}}
	v2 := &vocalParamInfo{Param: "tension", Available: true, Layers: []vocalLayer{
		{Layer: "baseline", Access: "read-only", Role: "analyzed-pristine", Shape: "dense"},
		{Layer: "user", Access: "read-write", Role: "override", Shape: "dense", Sparse: true},
	}}
	// 一个没有 override 层、但有 effective 的参数
	noOverride := &vocalParamInfo{Param: "x", Available: true, Layers: []vocalLayer{
		{Layer: "baseline", Access: "read-only", Role: "synthesized-default", Shape: "dense"},
	}}
	// `dynamic` 那种：只有 user、形状 points，而且**没有 effective**
	dyn := &vocalParamInfo{Param: "dynamic", Available: true, Layers: []vocalLayer{
		{Layer: "user", Access: "read-write", Role: "override", Shape: "points"},
	}}
	withEff := &vocalRead{Effective: &vocalLayer{Layer: "effective", Shape: "dense"}}
	noEff := &vocalRead{}

	if got, note := resolveLayerAuto(v1, withEff); got != "direct" {
		t.Errorf("v1 的 auto 应当是 direct，实得 %q（%s）", got, note)
	}
	if got, note := resolveLayerAuto(v2, withEff); got != "user" {
		t.Errorf("v2 的 auto 应当是 user，实得 %q", got)
	} else if !strings.Contains(note, "user") {
		t.Errorf("换了层名就要**说出来**（否则用户不知道查的是哪层），实得 %q", note)
	}
	// 没有 override 层 ⇒ 退到 effective，并且要说明
	if got, note := resolveLayerAuto(noOverride, withEff); got != "effective" {
		t.Errorf("没有 override 层时应当退到 effective，实得 %q", got)
	} else if !strings.Contains(note, "生效曲线") {
		t.Errorf("退到 effective 要说明，实得 %q", note)
	}
	// dynamic：有 user（override）就走它，不看 effective
	if got, _ := resolveLayerAuto(dyn, noEff); got != "user" {
		t.Errorf("dynamic 的 auto 应当是 user，实得 %q", got)
	}
	// 实在解不出来 ⇒ 空串，调用方照「没有可用层」拒绝（不是静默拿个错的层）
	empty := &vocalParamInfo{Param: "x", Available: true}
	if got, _ := resolveLayerAuto(empty, noEff); got != "" {
		t.Errorf("解不出层时应当回空串，实得 %q", got)
	}
	// 默认值必须是 auto（这一条是防回退的：谁把默认改回具体层名，这里就响）
	if d := findLayerParam().Default; d != "auto" {
		t.Errorf("参数层默认应当是 auto，实得 %q —— 写死具体层名会在另一代引擎上直接失效", d)
	}
	// auto 必须是第一个选项（下拉默认落在第一项时也要是它）
	if opts := findLayerParam().Options; opts[0].Value != "auto" {
		t.Errorf("auto 应当是参数层的第一个选项，实得 %q", opts[0].Value)
	}
}

// 参数映射：**下拉里只留 ACE 侧真实存在的**（用户 2026-10-04 裁定），
// 但被摘掉的那几条**仍然要能给出带原因的明确拒绝**（直接拿 API 调它们时不许静默 0 命中）。
func TestParamMapCoversRoster(t *testing.T) {
	param := findParamParam()
	aceIDs := map[string]bool{"tension": true, "air": true, "energy": true,
		"falsetto": true, "formant": true, "dynamic": true}
	for _, o := range param.Options {
		m, known := paramMap[o.Value]
		if !known {
			t.Errorf("参数 %q（%s）在 paramMap 里没有条目 —— 会以「参数认不出」失败，不是明确拒绝",
				o.Value, o.Label)
			continue
		}
		if m.Ace == "" {
			t.Errorf("参数 %q 在下拉里，paramMap 却说 ACE 没有对应物 —— 下拉只该留 ACE 实有的", o.Value)
			continue
		}
		if !aceIDs[m.Ace] {
			t.Errorf("参数 %q 映到了 ACE 的 %q，但它不在实测花名册里", o.Value, m.Ace)
		}
	}
	// 下拉 = 6 条，全是 ACE 实有的
	if len(param.Options) != 6 {
		t.Errorf("参数下拉应当只留 6 条（ACE 实有），实得 %d：%v", len(param.Options),
			func() (vs []string) {
				for _, o := range param.Options {
					vs = append(vs, o.Value)
				}
				return
			}())
	}
	// 用户点名的两条映射
	if paramMap["tension"].Ace != "tension" {
		t.Error("张力 → tension")
	}
	if paramMap["breathiness"].Ace != "air" {
		t.Error("气声 → air")
	}
	// ACE 独有的几条都得在
	for _, id := range []string{"energy", "falsetto", "formant", "dynamic"} {
		if m, ok := paramMap[id]; !ok || m.Ace != id {
			t.Errorf("ACE 独有参数 %s 应当能查", id)
		}
	}
	// ★ 被摘掉的原脚本参数：**仍然在 paramMap 里、且带原因**（防静默失败）
	removed := []string{"pitchDelta", "vibratoEnv", "loudness", "voicing", "gender", "toneShift", "mouthOpening"}
	for _, id := range removed {
		m, ok := paramMap[id]
		if !ok {
			t.Errorf("被摘掉的 %q 从 paramMap 里消失了 —— API 直接调它会退化成「参数认不出」，不是明确拒绝", id)
			continue
		}
		if m.Ace != "" {
			t.Errorf("%q 应当没有 ACE 对应物，实得 %q", id, m.Ace)
		}
		if len([]rune(m.Why)) < 15 {
			t.Errorf("被摘掉的 %q 原因太短，等于没说：%q", id, m.Why)
		}
		// 下拉里**不该**再出现它
		for _, o := range findParamParam().Options {
			if o.Value == id {
				t.Errorf("%q 不该在参数下拉里（用户裁定只留 ACE 实有的）", id)
			}
		}
	}
	if paramMap["pitchDelta"].Ace != "" {
		t.Error("音高偏差必须被拒（pitch 恒 available:false）")
	}
}

// 数值参数不能用「包含」—— 那是个静默陷阱（永远 0 命中）。
func TestParamQueryContainsRefused(t *testing.T) {
	if condLabel("contains") != "包含" || condLabel("gt") != "大于" {
		t.Error("条件标签要对上原脚本")
	}
	if label := paramLabel("formant"); label != "音色" {
		t.Errorf("formant 的中文名应当是「音色」，实得 %q", label)
	}
	if label := attrLabel("phDefault"); label != "音素（默认发音）" {
		t.Errorf("phDefault 的标签应当是「音素（默认发音）」，实得 %q", label)
	}
	if label := attrLabel("phRaw"); label != "音素（实际发音）" {
		t.Errorf("phRaw 的标签应当是「音素（实际发音）」，实得 %q", label)
	}
	if label := attrLabel("ace.clipType"); label != "片段类型（人声、乐器、通用、音频）" {
		t.Errorf("片段类型的标签应当是「片段类型（人声、乐器、通用、音频）」，实得 %q", label)
	}
	if label := scopeLabel("project"); label != "当前工程" {
		t.Errorf("project 的标签应当是「当前工程」，实得 %q", label)
	}
}

// 语言：**两边都要过 langKeyword**。
//
// 这条是补的 —— 加它之前有个真 bug：ACE 报的是完整英文名（`English`），用户填的是单字（`英`），
// 只映一边 ⇒ 永远 0 命中（而 0 命中在这个工具里是最危险的失败方式）。
func TestLanguageAttrBothSides(t *testing.T) {
	cases := []struct{ note, want string }{
		{"English", "英"}, {"English", "english"}, {"English", "英文"},
		{"Chinese", "中"}, {"Chinese", "普"}, {"Chinese", "mandarin"},
		{"Japanese", "日"}, {"Cantonese", "粤"}, {"Korean", "韩"},
	}
	for _, c := range cases {
		n := noteRow{Language: c.note}
		ok, val, err := matchNote("language", "eq", c.want, n, clipRef{})
		if err != nil || !ok || val != c.note {
			t.Errorf("语言 %q 查 %q = %v,%q,%v，期望命中", c.note, c.want, ok, val, err)
		}
	}
	// 不该串台
	if ok, _, _ := matchNote("language", "eq", "英", noteRow{Language: "Chinese"}, clipRef{}); ok {
		t.Error("中文音符不该被「英」命中")
	}
	// 认不出的写法按字面比（而不是一律不命中）
	if ok, _, _ := matchNote("language", "eq", "klingon", noteRow{Language: "klingon"}, clipRef{}); !ok {
		t.Error("认不出的语言名应当按字面比")
	}
	// 包含也可以
	if ok, _, _ := matchNote("language", "contains", "eng", noteRow{Language: "English"}, clipRef{}); !ok {
		t.Error("语言也要能用「包含」")
	}
}

// b2p 是原脚本面板「位置:」那一行的格式：`小节.拍内比例`，两位小数。
// ⚠️ 它和 barBeat（`16.1.240`）**是两个不同的东西** —— 别把其中一个当另一个的别名。
func TestB2P(t *testing.T) {
	sigs := []sigEntry{{BarPos: 0, Tick: 0, Numerator: 4, Denominator: 4}}
	for tick, want := range map[int64]string{
		0:     "1.00",  // 第 1 小节头
		480:   "1.25",  // 第 2 拍
		960:   "1.50",  // 第 3 拍
		1920:  "2.00",  // 第 2 小节头
		12000: "7.25",  // 第 7 小节第 2 拍
		29040: "16.13", // 16.125 那一档（JS toFixed(2) 给 16.13）
	} {
		if got := b2p(tick, sigs); got != want {
			t.Errorf("b2p(%d) = %q，期望 %q", tick, got, want)
		}
	}
	if got := b2p(0, nil); got != "" {
		t.Errorf("没有拍号表时应当回空串，实得 %q", got)
	}
	// 3/4：一小节仍然是 1.00 的整数步（分母/拍数对得上）
	sigs34 := []sigEntry{{BarPos: 0, Tick: 0, Numerator: 3, Denominator: 4}}
	for tick, want := range map[int64]string{0: "1.00", 1440: "2.00", 720: "1.50"} {
		if got := b2p(tick, sigs34); got != want {
			t.Errorf("3/4 b2p(%d) = %q，期望 %q", tick, got, want)
		}
	}
	// 6/8：denominator=8 ⇒ 一小节 6 个八分 = 1440 tick；"拍"按八分算
	sigs68 := []sigEntry{{BarPos: 0, Tick: 0, Numerator: 6, Denominator: 8}}
	if got, want := b2p(720, sigs68), "1.50"; got != want {
		t.Errorf("6/8 b2p(720) = %q，期望 %q（第 3 个八分）", got, want)
	}
	// 与 barBeat 明确不同（防止将来有人"顺手统一"）
	if b2p(29040, sigs) == barBeat(29040, sigs) {
		t.Error("b2p 与 barBeat 是两种格式，不该相等")
	}
}

// 起始项规则照抄原脚本 search() 结尾那三行 —— 这不是"从第 1 项开始"。
func TestStartIndexFor(t *testing.T) {
	hits := func(ticks ...int64) []HitRow {
		out := make([]HitRow, len(ticks))
		for i, tk := range ticks {
			out[i] = HitRow{AbsTick: tk}
		}
		return out
	}
	if got := startIndexFor(nil, 500, true); got != 0 {
		t.Errorf("没有命中时应当是 0，实得 %d", got)
	}
	// 片段：index = 标记线**之前**的命中数 ⇒ 落在标记线附近的第一个
	// 命中在 100/200/300，标记线 250 ⇒ 之前有 2 个 ⇒ index=2（即 300 那一条）
	if got := startIndexFor(hits(100, 200, 300), 250, true); got != 2 {
		t.Errorf("片段、标记线 250 = %d，期望 2", got)
	}
	if got := startIndexFor(hits(100, 200, 300), 0, true); got != 0 {
		t.Errorf("标记线在最前面 = %d，期望 0", got)
	}
	// 标记线在所有命中之后 ⇒ index == len ⇒ 修正成最后一个（原脚本的 index--）
	if got := startIndexFor(hits(100, 200, 300), 9999, true); got != 2 {
		t.Errorf("标记线在最后面 = %d，期望 2（那三行的修正）", got)
	}
	// 轨道/工程：index = 标记线**之后**的命中数（显示是倒着数的）
	// 命中在 100/200/300，标记线 150 ⇒ 之后有 2 个 ⇒ index=2
	if got := startIndexFor(hits(100, 200, 300), 150, false); got != 2 {
		t.Errorf("轨道/工程、标记线 150 = %d，期望 2", got)
	}
	if got := startIndexFor(hits(100, 200, 300), 0, false); got != 2 {
		t.Errorf("轨道/工程、标记线 0 = %d，期望 2（三个都在后面 ⇒ index==len ⇒ 修正）", got)
	}
}

func attrParam(t *testing.T) Param {
	t.Helper()
	tool := Get("find.notes")
	if tool == nil {
		t.Fatal("find.notes 没注册")
	}
	for _, p := range tool.Params {
		if p.Name == "attr" {
			if len(p.Options) == 0 {
				t.Fatal("attr 没有选项")
			}
			return p
		}
	}
	t.Fatal("find.notes 没有 attr 属性下拉")
	return Param{}
}

func jsonTagOf(t *testing.T, v any, field string) string {
	t.Helper()
	f, ok := reflect.TypeOf(v).FieldByName(field)
	if !ok {
		t.Fatalf("%T 没有字段 %s", v, field)
	}
	return strings.Split(f.Tag.Get("json"), ",")[0]
}

// 片段类型的标签与**同义词搜索** —— 用户 2026-10-14：
// 「后面写（人声、乐器、通用、音频）把混淆词也加入搜索比如声乐、器乐等」。
func TestClipTypeLabelAndSynonyms(t *testing.T) {
	// ① 标签把四种中文名带上（值仍是 ace.clipType）
	var label string
	for _, o := range aceAttrOptions {
		if o.Value == "ace.clipType" {
			label = o.Label
		}
	}
	if label != "片段类型（人声、乐器、通用、音频）" {
		t.Fatalf("片段类型的标签不对：%q", label)
	}

	sing := clipRef{ClipType: "sing", Enabled: true}
	inst := clipRef{ClipType: "instrument", Enabled: true}
	midi := clipRef{ClipType: "genericMidi", Enabled: true} // 真机给的是这个大小写
	cases := []struct {
		cond, want string
		ref        clipRef
		ok         bool
	}{
		{"eq", "sing", sing, true},
		{"eq", "人声", sing, true},
		{"eq", "声乐", sing, true},
		{"eq", "唱", sing, true},
		{"eq", "vocal", sing, true},
		{"eq", "器乐", sing, false},  // 不是这类 ⇒ 中文词再多也不命中
		{"eq", "人声", midi, false},  //
		{"eq", "器乐", inst, true},   //
		{"eq", "乐器轨", inst, true},  //
		{"eq", "通用", midi, true},   //
		{"eq", "midi", midi, true}, // 「等于」也认原始串（大小写不敏感）
		{"eq", "MIDI", midi, true}, //
		{"contains", "midi", midi, true},
		{"contains", "声", sing, true},  // 中文按「包含」切
		{"contains", "乐", inst, true},  //
		{"contains", "声", inst, false}, //
	}
	for _, c := range cases {
		got, val, err := matchNote("ace.clipType", c.cond, c.want, noteRow{}, c.ref)
		if err != nil {
			t.Fatalf("片段类型 %s/%q 报错：%v", c.cond, c.want, err)
		}
		if got != c.ok {
			t.Errorf("片段类型 %s/%q（实际 %s）= %v，期望 %v", c.cond, c.want, c.ref.ClipType, got, c.ok)
		}
		// ⚠️ 命中了也要给**原始串**（中文只在标签上，不进结果值）
		if val != c.ref.ClipType {
			t.Errorf("结果值应当是原始串 %q，实得 %q", c.ref.ClipType, val)
		}
	}
}
