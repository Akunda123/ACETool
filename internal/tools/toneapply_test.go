package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"acetool/internal/acep"
	"acetool/internal/cbor"
)

// ---------- 造一份带 uuid + pos/dur/pitch/lyric 的工程（+ 可选 lane） ----------

type tNoteSpec struct {
	uuid     string
	pos, dur int
	pitch    int
	lyric    string
}

// tonePayload 造 payload：一个真轨一个片段；`dataOff/dataVals` 造一条画法① entry（dataVals 为 nil 就不造）；
// `anchorPts` 是扁平的 [tick, 音高, …]（nil 就不造）；`withParams=false` 时**连 parameters 都不造**。
func tonePayload(t *testing.T, notes []tNoteSpec, dataOff int64, dataVals []any, anchorPts []any, withParams bool) []byte {
	t.Helper()
	arr := cbor.EncodeArrayHeader(uint64(len(notes)))
	for _, n := range notes {
		b := cbor.EncodeMapHeader(5)
		b = append(b, cbor.EncodeText("uuid")...)
		b = append(b, cbor.EncodeText(n.uuid)...)
		b = append(b, cbor.EncodeText("pos")...)
		b = append(b, cbor.EncodeUint(uint64(n.pos))...)
		b = append(b, cbor.EncodeText("dur")...)
		b = append(b, cbor.EncodeUint(uint64(n.dur))...)
		b = append(b, cbor.EncodeText("pitch")...)
		b = append(b, cbor.EncodeUint(uint64(n.pitch))...)
		b = append(b, cbor.EncodeText("lyric")...)
		b = append(b, cbor.EncodeText(n.lyric)...)
		arr = append(arr, b...)
	}

	pat := cbor.EncodeMapHeader(5)
	pat = append(pat, cbor.EncodeText("name")...)
	pat = append(pat, cbor.EncodeText("片段1")...)
	pat = append(pat, cbor.EncodeText("pos")...)
	pat = append(pat, cbor.EncodeUint(0)...)
	pat = append(pat, cbor.EncodeText("dur")...)
	pat = append(pat, cbor.EncodeUint(4000)...)
	pat = append(pat, cbor.EncodeText("notes")...)
	pat = append(pat, arr...)
	pat = append(pat, cbor.EncodeText("parameters")...)
	if !withParams {
		pat = append(pat, cbor.EncodeMapHeader(0)...)
	} else {
		var lane []byte
		n := 0
		var dataEntry, anchorEntry []byte
		if dataVals != nil {
			dataEntry = cbor.EncodeMap([2]any{"type", "data"}, [2]any{"offset", dataOff}, [2]any{"values", dataVals})
			n++
		}
		if anchorPts != nil {
			vuv := make([]float64, len(anchorPts)/2)
			for i := range vuv {
				vuv[i] = 1
			}
			anchorEntry = cbor.EncodeMap([2]any{"type", "anchor"}, [2]any{"points", anchorPts}, [2]any{"pointsVUV", vuv})
			n++
		}
		lane = cbor.EncodeArrayHeader(uint64(n))
		lane = append(lane, dataEntry...)
		lane = append(lane, anchorEntry...)
		params := cbor.EncodeMapHeader(1)
		params = append(params, cbor.EncodeText("pitchDelta")...)
		params = append(params, lane...)
		pat = append(pat, params...)
	}

	pats := cbor.EncodeArrayHeader(1)
	pats = append(pats, pat...)

	tr := cbor.EncodeMapHeader(3)
	tr = append(tr, cbor.EncodeText("name")...)
	tr = append(tr, cbor.EncodeText("轨 0")...)
	tr = append(tr, cbor.EncodeText("type")...)
	tr = append(tr, cbor.EncodeText("sing")...)
	tr = append(tr, cbor.EncodeText("patterns")...)
	tr = append(tr, pats...)

	tracks := cbor.EncodeArrayHeader(1)
	tracks = append(tracks, tr...)

	root := cbor.EncodeMapHeader(1)
	root = append(root, cbor.EncodeText("tracks")...)
	root = append(root, tracks...)
	return root
}

// realNotes 是照技能 §2.2 真实样本抄的一组音符（4 个音、pos/dur 都是实测值）。
func realNotes() []tNoteSpec {
	return []tNoteSpec{
		{uuid: "{a1}", pos: 240, dur: 660, pitch: 62, lyric: "啦"},
		{uuid: "{b2}", pos: 900, dur: 1020, pitch: 64, lyric: "明"},
		{uuid: "{c3}", pos: 1920, dur: 840, pitch: 65, lyric: "天"},
		{uuid: "{d4}", pos: 2760, dur: 1020, pitch: 66, lyric: "咦"},
	}
}

func manyFloats(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = float64(i%7) / 4
	}
	return out
}

func loadLane(t *testing.T, path, uuid string) *cbor.Node {
	t.Helper()
	cont, err := acep.Open(path)
	if err != nil {
		t.Fatalf("回读打开：%v", err)
	}
	root, err := cbor.Decode(cont.Payload)
	if err != nil {
		t.Fatalf("回读解码：%v", err)
	}
	pat, err := patternOfUUID(root, uuid)
	if err != nil {
		t.Fatalf("回读找片段：%v", err)
	}
	return laneOf(pat, "pitchDelta")
}

func countData(lane *cbor.Node) (entries, samples int) {
	for _, e := range lane.Items {
		if nodeText(e.Get("type")) != "data" {
			continue
		}
		entries++
		samples += len(e.Get("values").Items)
	}
	return
}

func anchorEntries(lane *cbor.Node) []*cbor.Node {
	var out []*cbor.Node
	for _, e := range lane.Items {
		if nodeText(e.Get("type")) == "anchor" {
			out = append(out, e)
		}
	}
	return out
}

// ---------- 主路径：清区间（锚点 + 曲线样本）+ 加三种调型的锚点 ----------

func TestToneApplyClearsAndAnchors(t *testing.T) {
	p := writeTempACEP(t, tonePayload(t, realNotes(), 100, manyFloats(3000),
		[]any{500, 61.5, 700, 63.0, 1300, 64.25, 2500, 66.0, 2790, 67.0}, true))

	out, err := toneApplyApply(context.Background(), nil, map[string]string{
		"path":  p,
		"notes": "{a1},{b2},{c3},{d4}",
		"tones": "124-", // d4 = 不动
	})
	if err != nil {
		t.Fatalf("执行失败：%v", err)
	}
	rep := out.(*pitchCopyReport)
	if !strings.Contains(rep.Dst, "写了 3 个，跳过 1 个") {
		t.Fatalf("报告不对：%s", rep.Dst)
	}
	if len(rep.Notes) != 3 {
		t.Fatalf("逐音符应 3 条：%v", rep.Notes)
	}
	if !strings.Contains(strings.Join(rep.Checks, " "), "形状自检通过") {
		t.Fatalf("少了形状自检：%v", rep.Checks)
	}

	lane := loadLane(t, p, "{a1}")

	// ① 曲线：被清的三段删掉 2688 个样本 ⇒ 只剩首尾两段 312 个
	entries, samples := countData(lane)
	if entries != 2 || samples != 312 {
		t.Fatalf("曲线拆段不对：entry %d 条、样本 %d 个（期望 2 / 312）", entries, samples)
	}
	for _, e := range lane.Items {
		if nodeText(e.Get("type")) != "data" {
			continue
		}
		off, _ := nodeInt(e.Get("offset"))
		if off == 100 {
			if len(e.Get("values").Items) != 150 {
				t.Fatalf("首段应当是 150 个样本，实际 %d", len(e.Get("values").Items))
			}
		} else if off == 2761 {
			if len(e.Get("values").Items) != 162 {
				t.Fatalf("尾段应当是 162 个样本，实际 %d", len(e.Get("values").Items))
			}
		} else {
			t.Fatalf("冒出一条 offset=%d 的曲线 entry", off)
		}
		// 区间里一个样本都不许剩（与删除同一套栅格数学）
		for _, r := range []toneRange{{240, 900}, {900, 1920}, {1920, 2760}} {
			if left, err := dataSamplesInRanges(e, []toneRange{r}); err != nil || left != 0 {
				t.Fatalf("区间 %v 里还剩 %d 个样本（err=%v）", r, left, err)
			}
		}
	}

	// ② 旧锚点：区间里的 4 个点没了，区间外（2790）那个留着 ⇒ 那条 entry 只剩 1 个点
	anchors := anchorEntries(lane)
	if len(anchors) != 4 { // 3 条新的 + 1 条旧的（剩 1 点）
		t.Fatalf("anchor entry 条数不对：%d", len(anchors))
	}
	old := entryStartingAt(t, anchors, 2790)
	if len(old.Get("points").Items) != 2 || len(old.Get("pointsVUV").Items) != 1 {
		t.Fatalf("旧锚点应当只剩 1 个点：%v", old.Get("points"))
	}

	// ③ 新锚点：逐音符核对点集（调型表：1 平 / 2 先 −4（低平 15%）/ 4 end−1 处 −12）
	want := map[string][]any{
		"{a1}": {int64(240), 62.0, int64(900), 62.0},
		"{b2}": {int64(900), 64.0, int64(901), 60.0, int64(1053), 60.0, int64(1920), 64.0}, // 0.15*1020 = 153
		"{c3}": {int64(1920), 65.0, int64(2759), 53.0, int64(2760), 65.0},
	}
	for uuid, pts := range want {
		vuv := make([]float64, len(pts)/2)
		for i := range vuv {
			vuv[i] = 1
		}
		found := false
		for _, e := range anchors {
			if samePoints(e, pts, vuv) {
				found = true
				break
			}
		}
		if !found {
			var got []string
			for _, e := range anchors {
				got = append(got, fmtPoints(e))
			}
			t.Fatalf("%s 没找到期望的锚点 %v（实际有：%s）", uuid, pts, strings.Join(got, " | "))
		}
	}
	// 备份是硬要求
	if b, err := latestBackupFor(p); err != nil || b == "" {
		t.Fatalf("没找到备份：%v", err)
	}
}

// entryStartingAt 取出首点在指定 tick 的那条 anchor entry（断言别靠数组下标 —— 新 entry 是**按 tick 插入**的）。
func entryStartingAt(t *testing.T, anchors []*cbor.Node, tick int64) *cbor.Node {
	t.Helper()
	for _, e := range anchors {
		pn := e.Get("points")
		if pn == nil || len(pn.Items) < 2 {
			continue
		}
		if v, _ := nodeInt(pn.Items[0]); v == tick {
			return e
		}
	}
	t.Fatalf("找不到首点在 tick %d 的锚点 entry（现有：%d 条）", tick, len(anchors))
	return nil
}

// fmtPoints 把一条 anchor entry 的点打成 "tick:音高,…"（断言失败时给人看）。
func fmtPoints(e *cbor.Node) string {
	pn := e.Get("points")
	if pn == nil {
		return "(无点)"
	}
	var parts []string
	for i := 0; i+1 < len(pn.Items); i += 2 {
		tk, _ := nodeInt(pn.Items[i])
		v, _ := nodeFloat(pn.Items[i+1])
		parts = append(parts, fmt.Sprintf("%d:%g", tk, v))
	}
	return strings.Join(parts, ",")
}

// ---------- 「不动」的音符一点不碰 ----------

func TestToneApplySkipLeavesRangeAlone(t *testing.T) {
	// 只有 d4（区间 2760–3780）里有锚点，且它是「不动」⇒ 那个点必须原样在
	p := writeTempACEP(t, tonePayload(t, realNotes(), 100, manyFloats(3000),
		[]any{2790, 67.0, 2800, 68.0}, true))
	if _, err := toneApplyApply(context.Background(), nil, map[string]string{
		"path": p, "notes": "{a1},{d4}", "tones": "1-",
	}); err != nil {
		t.Fatalf("执行失败：%v", err)
	}
	lane := loadLane(t, p, "{a1}")
	anchors := anchorEntries(lane)
	// d4 那条旧 entry 必须**原样**（2 个点：2790/2800）；a1 的新锚点首点是 240
	old := entryStartingAt(t, anchors, 2790)
	if len(old.Get("points").Items) != 4 {
		t.Fatalf("「不动」的音符区间被动了：那条旧 entry 的点数变成 %d", len(old.Get("points").Items))
	}
	if v, _ := nodeInt(old.Get("points").Items[2]); v != 2800 {
		t.Fatalf("旧 entry 的第 2 个点应当是 2800，实际 %d", v)
	}
	if len(anchors) != 2 {
		t.Fatalf("anchor entry 应当 2 条（a1 新的 + d4 旧的），实际 %d", len(anchors))
	}
	// a1 的区间仍然被清（曲线样本 704 个没了）
	for _, e := range lane.Items {
		if nodeText(e.Get("type")) != "data" {
			continue
		}
		if left, _ := dataSamplesInRanges(e, []toneRange{{240, 900}}); left != 0 {
			t.Fatalf("a1 的区间没清干净：还剩 %d 个样本", left)
		}
	}
}

// ---------- 没有 lane 就造一条 ----------

func TestToneApplyCreatesLane(t *testing.T) {
	p := writeTempACEP(t, tonePayload(t, realNotes(), 0, nil, nil, false)) // parameters 是空 map
	if _, err := toneApplyApply(context.Background(), nil, map[string]string{
		"path": p, "notes": "{a1}", "tones": "3",
	}); err != nil {
		t.Fatalf("执行失败：%v", err)
	}
	lane := loadLane(t, p, "{a1}")
	anchors := anchorEntries(lane)
	if len(anchors) != 1 {
		t.Fatalf("应当造出 1 条 anchor entry，实际 %d", len(anchors))
	}
	pts := anchors[0].Get("points").Items
	// 3 上声：pos 240 → 62、pos+1 241 → 54、pos+50%*660=570 → 54、end 900 → 62
	wantTicks := []int64{240, 241, 570, 900}
	wantVals := []float64{62, 54, 54, 62}
	if len(pts) != 8 {
		t.Fatalf("点数不对：%d", len(pts))
	}
	for i := 0; i < 4; i++ {
		if v, _ := nodeInt(pts[i*2]); v != wantTicks[i] {
			t.Fatalf("第 %d 点 tick 应为 %d，实际 %d", i, wantTicks[i], v)
		}
		if v, _ := nodeFloat(pts[i*2+1]); v != wantVals[i] {
			t.Fatalf("第 %d 点音高应为 %v，实际 %v", i, wantVals[i], v)
		}
	}
}

// ---------- 负例 ----------

func TestToneApplyRejectsMismatch(t *testing.T) {
	p := writeTempACEP(t, tonePayload(t, realNotes(), 100, manyFloats(100), nil, true))
	if _, err := toneApplyApply(context.Background(), nil, map[string]string{
		"path": p, "notes": "{a1},{b2}", "tones": "1",
	}); err == nil || !strings.Contains(err.Error(), "对不上") {
		t.Fatalf("个数不一致必须拒：%v", err)
	}
	if _, err := toneApplyApply(context.Background(), nil, map[string]string{
		"path": p, "notes": "{a1},{b2}", "tones": "17",
	}); err == nil || !strings.Contains(err.Error(), "只认") {
		t.Fatalf("非法声调必须拒：%v", err)
	}
	// 找不到音符 ⇒ 不报错，但如实说"一个字节都不会改" + 把找不到的列出来
	out, err := toneApplyApply(context.Background(), nil, map[string]string{
		"path": p, "notes": "{没有这个}", "tones": "1",
	})
	if err != nil {
		t.Fatalf("找不到音符不该报错（应当如实报没写）：%v", err)
	}
	rep := out.(*pitchCopyReport)
	if !strings.Contains(rep.Dst, "写了 0 个") ||
		!strings.Contains(strings.Join(rep.Checks, " "), "找不到") {
		t.Fatalf("没如实报：dst=%q checks=%v", rep.Dst, rep.Checks)
	}
}

// ---------- 调型表本身（纯函数） ----------

func TestTonePointsTable(t *testing.T) {
	it := toneItem{pos: 100, dur: 400, pitch: 60}
	cases := []struct {
		tone byte
		want []float64 // 扁平 tick,音高
	}{
		{'1', []float64{100, 60, 500, 60}},
		{'2', []float64{100, 60, 101, 56, 160, 56, 500, 60}}, // 低平段 15%：0.15*400 = 60
		{'3', []float64{100, 60, 101, 52, 300, 52, 500, 60}}, // 低平段 50%：0.5*400 = 200
		{'4', []float64{100, 60, 499, 48, 500, 60}},
	}
	for _, c := range cases {
		it.tone = c.tone
		pts, vuv := tonePoints(it)
		if len(pts) != len(c.want) {
			t.Fatalf("tone %c：点数 %d ≠ %d", c.tone, len(pts), len(c.want))
		}
		for i, w := range c.want {
			var got float64
			if i%2 == 0 {
				v, _ := anyInt(pts[i])
				got = float64(v)
			} else {
				got, _ = anyFloat(pts[i])
			}
			if got != w {
				t.Fatalf("tone %c 第 %d 项：%v ≠ %v", c.tone, i, got, w)
			}
		}
		if len(vuv) != len(pts)/2 {
			t.Fatalf("tone %c：pointsVUV 个数不对", c.tone)
		}
	}
	// 极短音符：点撞在一起也要保持**升序**、不重复
	it.tone = '3'
	it.pos, it.dur = 10, 2
	pts, _ := tonePoints(it)
	last := int64(-1)
	for i := 0; i < len(pts); i += 2 {
		v, _ := anyInt(pts[i])
		if v <= last {
			t.Fatalf("tick 没升序/重复：%v", pts)
		}
		last = v
	}
}

func TestToneToolRegistered(t *testing.T) {
	tl := Get("tone.apply")
	if tl == nil || tl.Kind != KindWrite {
		t.Fatal("tone.apply 没注册或不是写工具")
	}
	for _, p := range tl.Params {
		if p.Name == "dryRun" {
			t.Fatal("干跑已经全删了，tone.apply 不该有 dryRun")
		}
	}
}
