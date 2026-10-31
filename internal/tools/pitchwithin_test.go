package tools

import (
	"testing"

	"acetool/internal/cbor"
)

func mustNode(t *testing.T, payload []byte) *cbor.Node {
	t.Helper()
	n, err := cbor.Decode(payload)
	if err != nil {
		t.Fatalf("解不开：%v", err)
	}
	return n
}

// ---------- ① 栅格数学（技能 acep §2.2：pitchDelta = 15/16 tick/样本） ----------

func TestPitchSampleWindow(t *testing.T) {
	// 真实样本（技能 §2.2 的实测）：offset 360 / 3095 个样本
	const off, cnt = 360, 3095

	// 一个整小节的窗口（tick 0..960）：第一样本在 360 ⇒ i0 被夹到 0；
	// 末尾 = ceil(16*(960-360)/15) = ceil(640) = 640
	i0, i1 := pitchSampleWindow(off, cnt, 0, 960)
	if i0 != 0 || i1 != 640 {
		t.Fatalf("窗口 [0,960) 得到 [%d,%d)，期望 [0,640)", i0, i1)
	}

	// 完全落在中间的窗口：tick 480..960 ⇒ i0 = ceil(16*120/15)=128，i1 = 640
	i0, i1 = pitchSampleWindow(off, cnt, 480, 960)
	if i0 != 128 || i1 != 640 {
		t.Fatalf("窗口 [480,960) 得到 [%d,%d)，期望 [128,640)", i0, i1)
	}

	// 空窗口 / 越界窗口
	if a, b := pitchSampleWindow(off, cnt, 960, 960); a != 0 || b != 0 {
		t.Fatalf("空窗口应当给 [0,0)，得到 [%d,%d)", a, b)
	}
	if a, b := pitchSampleWindow(off, cnt, 0, 10_000_000); a != 0 || b != cnt {
		t.Fatalf("超长窗口应当夹到 [0,%d)，得到 [%d,%d)", cnt, a, b)
	}
}

func TestPitchNearestSampleRoundTrip(t *testing.T) {
	const off, cnt = 360, 3095
	for _, i := range []int64{0, 1, 127, 640, 3094} {
		tick := int64(round(pitchSampleTick(off, i))) // 取整到 tick（±1 tick 内应当还能找回同一个样本）
		j, ok := pitchNearestSample(off, cnt, tick)
		if !ok || j != i {
			t.Fatalf("样本 %d（tick %d）找回 %d（ok=%v）", i, tick, j, ok)
		}
	}
	// 离得太远的（entry 之外）应当报"不覆盖"
	if _, ok := pitchNearestSample(off, cnt, off+cnt); ok {
		t.Fatal("超出 entry 的 tick 不该说覆盖")
	}
}

// ---------- ② 三类内容的计划 + 套用 ----------

// 造一个最小片段：pitchDelta lane（1 条 data + 1 条 anchor）+ 4 个音符（第 2 个带颤音）
func synthPattern(t *testing.T) (*cbor.Node, []byte) {
	t.Helper()
	// data entry：offset 0，8 个样本（tick 0 ≈ 7.5）
	dataEntry := cbor.EncodeMap(
		[2]any{"type", "data"},
		[2]any{"offset", 0},
		[2]any{"values", []float64{1, 2, 3, 4, 5, 6, 7, 8}},
	)
	anchorEntry := cbor.EncodeMap(
		[2]any{"type", "anchor"},
		[2]any{"points", []any{100, 61.5, 200, 63.25}},
		[2]any{"pointsVUV", []float64{1, 0}},
	)
	lane := cbor.EncodeArrayHeader(2)
	lane = append(lane, dataEntry...)
	lane = append(lane, anchorEntry...)

	params := cbor.EncodeMapHeader(1)
	params = append(params, cbor.EncodeText("pitchDelta")...)
	params = append(params, lane...)

	vib := cbor.EncodeMap(
		[2]any{"startPos", 133},
		[2]any{"frequency", 6.0},
		[2]any{"amplitude", 2.0},
	)
	note := func(pos int, pitch int, withVib bool) []byte {
		n := cbor.EncodeMapHeader(3)
		n = append(n, cbor.EncodeText("pos")...)
		n = append(n, cbor.EncodeUint(uint64(pos))...)
		n = append(n, cbor.EncodeText("pitch")...)
		n = append(n, cbor.EncodeUint(uint64(pitch))...)
		n = append(n, cbor.EncodeText("vibrato")...)
		if withVib {
			n = append(n, vib...)
		} else {
			n = append(n, 0xf6) // null（这个音符没有颤音）
		}
		return n
	}
	notes := cbor.EncodeArrayHeader(4)
	notes = append(notes, note(0, 60, false)...)
	notes = append(notes, note(480, 62, true)...)
	notes = append(notes, note(960, 64, false)...)
	notes = append(notes, note(1500, 65, false)...)

	pat := cbor.EncodeMapHeader(3)
	pat = append(pat, cbor.EncodeText("uuid")...)
	pat = append(pat, cbor.EncodeText("{clip-1}")...)
	pat = append(pat, cbor.EncodeText("parameters")...)
	pat = append(pat, params...)
	pat = append(pat, cbor.EncodeText("notes")...)
	pat = append(pat, notes...)
	return mustNode(t, pat), pat
}

func TestPitchWithinCurveGoesToNewEntryWhenNoCoverage(t *testing.T) {
	pat, payload := synthPattern(t)
	// 源窗口 tick 0..4（前几个样本），目标 shift 1000 ⇒ 目标没有覆盖 ⇒ 应当新建 entry
	edits, entries, msg, err := planCopyCurve(pat, pat, 0, 4, 1000, 1004, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("应当新建 1 条 entry，实得 %d（%s）", len(entries), msg)
	}
	app, err := appendLaneEntries(laneOf(pat, "pitchDelta"), entries, "测试：追加")
	if err != nil {
		t.Fatal(err)
	}
	edits = append(edits, app...)
	if len(edits) == 0 {
		t.Fatal("应当有编辑")
	}
	out, err := cbor.Apply(payload, edits)
	if err != nil {
		t.Fatalf("套用失败：%v", err)
	}
	root := mustNode(t, out)
	lane := laneOf(root, "pitchDelta")
	if len(lane.Items) != 3 {
		t.Fatalf("应当多出一条 entry（2→3），实得 %d", len(lane.Items))
	}
	last := lane.Items[2]
	if nodeText(last.Get("type")) != "data" {
		t.Fatalf("新 entry 的类型不对：%s", nodeText(last.Get("type")))
	}
	if off, _ := nodeInt(last.Get("offset")); off != 1000 {
		t.Fatalf("新 entry offset = %d，期望 1000", off)
	}
	vals := last.Get("values")
	if len(vals.Items) != 5 || vals.Items[0].Float != 1 || vals.Items[4].Float != 5 {
		got := make([]float64, 0, len(vals.Items))
		for _, it := range vals.Items {
			got = append(got, it.Float)
		}
		t.Fatalf("新 entry 的值不对：%v（%s）", got, msg)
	}
}

func TestPitchWithinCurveOverwritesExistingCoverage(t *testing.T) {
	pat, payload := synthPattern(t)
	// 先加一条"目标覆盖"：offset 2000 / 8 个样本（值全 0）
	lane := laneOf(pat, "pitchDelta")
	add, err := appendLaneEntries(lane, []*cbor.Node{mustNode(t, cbor.EncodeMap(
		[2]any{"type", "data"},
		[2]any{"offset", 2000},
		[2]any{"values", []float64{0, 0, 0, 0, 0, 0, 0, 0}},
	))}, "测试：加目标覆盖")
	if err != nil {
		t.Fatal(err)
	}
	payload2, err := cbor.Apply(payload, add)
	if err != nil {
		t.Fatal(err)
	}
	pat2 := mustNode(t, payload2)

	// 源 tick 0..4 → 目标 shift 2000（目标已有覆盖）⇒ **原位覆盖**那 9 字节，不新建 entry
	edits, entries, msg, err := planCopyCurve(pat2, pat2, 0, 4, 2000, 2004, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("有覆盖时不该新建 entry，实得 %d 条（%s）", len(entries), msg)
	}
	// 多个源样本会落到**同一个目标样本**上 ⇒ 先按工具里那一步去重（留最后一次），再套用
	edits, err = dedupeEdits(edits)
	if err != nil {
		t.Fatalf("去重失败：%v", err)
	}
	out, err := cbor.Apply(payload2, edits)
	if err != nil {
		t.Fatalf("套用失败：%v", err)
	}
	root := mustNode(t, out)
	lane2 := laneOf(root, "pitchDelta")
	if len(lane2.Items) != 3 {
		t.Fatalf("原位覆盖不该新增 entry（应当还是 3 条），实得 %d（%s）", len(lane2.Items), msg)
	}
	vals := lane2.Items[2].Get("values")
	// 源窗口 [0,4) 里有 5 个样本（tick 0、0.9375、1.875、2.8125、3.75）⇒ 目标 0..4 号样本被写
	if vals.Items[0].Float != 1 || vals.Items[4].Float != 5 {
		got := make([]float64, 0, len(vals.Items))
		for _, it := range vals.Items {
			got = append(got, it.Float)
		}
		t.Fatalf("目标样本没被写成源值：%v（%s）", got, msg)
	}
	if vals.Items[5].Float != 0 {
		t.Fatalf("窗口外的样本不该被改：%v", vals.Items[5].Float)
	}
}

func TestPitchWithinAnchorsAppendedAsNewEntry(t *testing.T) {
	pat, payload := synthPattern(t)
	// 源 tick 0..300 覆盖那两个锚点（100 / 200），shift 1000
	entries, msg, err := planCopyAnchors(pat, 0, 300, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("应当得到 1 条 anchor entry，实得 %d（%s）", len(entries), msg)
	}
	edits, err := appendLaneEntries(laneOf(pat, "pitchDelta"), entries, "测试：追加锚点")
	if err != nil {
		t.Fatal(err)
	}
	out, err := cbor.Apply(payload, edits)
	if err != nil {
		t.Fatal(err)
	}
	root := mustNode(t, out)
	lane := laneOf(root, "pitchDelta")
	if len(lane.Items) != 3 {
		t.Fatalf("应当追加 1 条 anchor（2→3），实得 %d（%s）", len(lane.Items), msg)
	}
	last := lane.Items[2]
	if nodeText(last.Get("type")) != "anchor" {
		t.Fatalf("新 entry 类型不对：%s", nodeText(last.Get("type")))
	}
	pts := last.Get("points")
	if len(pts.Items) != 4 {
		t.Fatalf("点数不对：%d", len(pts.Items))
	}
	// 值是**绝对音高**（不随 delta 变），tick 平移 1000
	if pts.Items[0].Uint != 1100 || pts.Items[1].Float != 61.5 {
		t.Fatalf("第一个点不对：%v / %v", pts.Items[0].Uint, pts.Items[1].Float)
	}
	if v := last.Get("pointsVUV").Items[0].Float; v != 1 {
		t.Fatalf("pointsVUV 没跟上：%v", v)
	}
}

func TestPitchWithinVibratoCopiedToMatchingNote(t *testing.T) {
	pat, payload := synthPattern(t)
	// 源窗口 tick 400..500（第 2 个音符 pos=480 带颤音），shift 1020 ⇒ 目标 1500（第 4 个音符）
	edits, msg, err := planCopyVibrato(pat, pat, 400, 500, 1020, 30)
	if err != nil {
		t.Fatal(err)
	}
	out, err := cbor.Apply(payload, edits)
	if err != nil {
		t.Fatalf("套用失败：%v", err)
	}
	root := mustNode(t, out)
	notes := root.Get("notes")
	got := notes.Items[3].Get("vibrato")
	if got == nil || got.Kind != cbor.KMap {
		t.Fatalf("第 4 个音符没拿到颤音：%+v（%s）", got, msg)
	}
	if f := got.Get("frequency"); f == nil || f.Float != 6 {
		t.Fatalf("颤音字段不对：%+v", got)
	}
	if sp, _ := nodeInt(got.Get("startPos")); sp != 133 {
		t.Fatalf("startPos 应当是音符内偏移（133），实得 %d", sp)
	}

	// 目标处没有音符 ⇒ 跳过并如实报
	if _, msg2, _ := planCopyVibrato(pat, pat, 400, 500, 5000, 30); msg2 == "" {
		t.Fatal("跳过时也该有说明")
	}
}
