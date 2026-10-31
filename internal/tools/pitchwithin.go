package tools

import (
	"strconv"
	"encoding/json"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"acetool/internal/acep"
	"acetool/internal/cbor"
)

// 轨道内复制**音高线**（文件路线，技能 `acep`）—— 三类内容一起搬：
//
//	① **曲线**（画法①）`patterns[*].parameters.pitchDelta[i]`，`type:"data"` + `offset` + `values`
//	   —— 值是 **delta 半音**，栅格 **15/16 tick/样本**（1024 Hz；技能 §2.2 实测 3095 样本 / offset 360）
//	② **锚点**（画法②）同一条 lane 里的另一条 entry，`type:"anchor"` + `points` + `pointsVUV`
//	   —— 值是**绝对音高**（技能 §2.2 画法②明确「不是 delta」）
//	③ **音符颤音**（画法③）`notes[k].vibrato`（对象，**不在 lane 里**；`startPos` 是音符内偏移）
//
// 为什么走文件：CLI 上 `vocalparam` 的 `pitch` 恒 `available:false`
//（原话：the channel stores a delta while the draw primitive takes absolute pitch, and anchors and vibrato ride on top）
// ⇒ 音高线只有文件里能改（技能 §0 第 1 句）。
//
// ⛔ 纪律（技能 §8）：① 先让宿主落盘（页面先按「保存工程」）② **备份**（本工具把原文件复制到
// `%TEMP%\ACETool\acep-backup\`）③ 只改必要字节 ④ 改完**让 ACE 重新打开**（页面走 `project open --discard-changes`）
// ⑤ **读回核对**（本工具写完自己重读逐值比）。没有 ④⑤ 的改动不算完成。
//
// ⚠️ 本工具是**原地改当前工程文件**（用户 2026-10-08 的流程：「读取路径修改替换操作，自动加载修改好的 acep」），
// 与 `acep.patch`（只写副本）不同 —— 所以备份是**硬要求**，缺了就不写。

// ---------- ① 栅格数学（纯函数，单测卡住） ----------

// pitchDelta 的采样栅格：**15/16 tick 一个样本**（技能 §2.2 实测）。
const (
	pitchTickNum = 15
	pitchTickDen = 16
)

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func ceilDiv(a, b int64) int64 { return -floorDiv(-a, b) }

// pitchSampleWindow 返回**落在 [lo,hi) 里的样本下标区间** `[i0,i1)`（半开；可能为空）。
//
// 样本 i 的 tick = offset + 15i/16 ⇒
//
//	i0 = 最小的 i 使 offset + 15i/16 ≥ lo ⟺ 15i ≥ 16(lo-offset) ⟺ i0 = ceil(16(lo-offset)/15)
//	i1 = 最小的 i 使 offset + 15i/16 ≥ hi
func pitchSampleWindow(offset, count, lo, hi int64) (int64, int64) {
	if hi <= lo {
		return 0, 0
	}
	i0 := ceilDiv(pitchTickDen*(lo-offset), pitchTickNum)
	i1 := ceilDiv(pitchTickDen*(hi-offset), pitchTickNum)
	if i0 < 0 {
		i0 = 0
	}
	if i1 > count {
		i1 = count
	}
	if i1 < i0 {
		i1 = i0
	}
	return i0, i1
}

// pitchSampleTick 返回第 i 个样本的 tick（真值是有理数，这里给 float 只用于回报）。
func pitchSampleTick(offset, i int64) float64 {
	return float64(offset) + float64(pitchTickNum*i)/float64(pitchTickDen)
}

// pitchNearestSample 是"某个 tick 落在哪一条 entry 的第几个样本上"（就近取整；差 > 1 tick 认为不覆盖）。
func pitchNearestSample(offset, count, tick int64) (int64, bool) {
	i := int64(round(float64(pitchTickDen*(tick-offset)) / float64(pitchTickNum)))
	if i < 0 || i >= count {
		return 0, false
	}
	d := pitchSampleTick(offset, i) - float64(tick)
	if d < 0 {
		d = -d
	}
	return i, d <= 1.0
}

func round(f float64) float64 {
	if f < 0 {
		return float64(int64(f - 0.5))
	}
	return float64(int64(f + 0.5))
}

// ---------- ② 报数据结构 ----------

type pitchKindReport struct {
	On      bool   `json:"启用"`
	Summary string `json:"结果"`
}

type pitchCopyReport struct {
	DryRun  bool            `json:"干跑"`
	Path    string          `json:"工程文件"`
	Clip    string          `json:"片段"`
	Track   string          `json:"轨道"`
	Src     string          `json:"源"`
	Dst     string          `json:"目标"`
	Line    pitchKindReport `json:"曲线"`
	Anchor  pitchKindReport `json:"锚点"`
	Vibrato pitchKindReport `json:"音符颤音"`
	Backup  string          `json:"备份,omitempty"`
	Checks  []string        `json:"自检,omitempty"`
	Hint    string          `json:"提示,omitempty"`
	Notes   []string        `json:"逐音符,omitempty"` // 只在需要逐条罗列的工具里用（`tone.apply`）
}

// ---------- ③ 计划（干跑与真写共用） ----------

type pitchPlan struct {
	rep    *pitchCopyReport
	cont   *acep.Container
	raw    []byte     // 原文件字节（备份用）
	newPay []byte     // 改完的 payload
	backup string     // 备份路径（Apply 真写时落盘）
	dstPat *cbor.Node // **目标**片段（新 entry 追加到它的 lane 上）
	edits  []cbor.Edit
	add    []*cbor.Node
	// 覆盖语义：写锚点前先清掉目标区间里的旧锚点
	clipUUID    string
	trimAnchors bool
	trimLo      int64
	trimHi      int64
}

func buildPitchPlan(ctx context.Context, rd ReadFunc, vals map[string]string) (*pitchPlan, error) {
	p := strings.Trim(strings.TrimSpace(value(vals, "path", "")), `"`)
	if p == "" {
		return nil, fmt.Errorf("要一个工程文件路径（path）—— 页面那一步「保存工程」会把它给你（`project save` 的 savedPath）")
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("读不到工程文件：%w", err)
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s 是文件夹 —— 要给里面的 .acep 文件", p)
	}

	// 源/目标窗口（小节 → 工程 tick；照 xcopy.within 的口径：**含首含尾**）
	srcB, err := parseMeasure(vals, "srcBeginBar", "源开始小节")
	if err != nil {
		return nil, err
	}
	srcE, err := parseMeasure(vals, "srcEndBar", "源结束小节")
	if err != nil {
		return nil, err
	}
	dstB, err := parseMeasure(vals, "dstBeginBar", "目标开始小节")
	if err != nil {
		return nil, err
	}
	if srcE < srcB {
		return nil, fmt.Errorf("源结束小节（%g）不能小于源开始小节（%g）", srcE, srcB)
	}
	sLo, err := measureToTick(ctx, rd, srcB, "源开始小节")
	if err != nil {
		return nil, err
	}
	sHi, err := measureToTick(ctx, rd, srcE+1, "源结束小节")
	if err != nil {
		return nil, err
	}
	dLo, err := measureToTick(ctx, rd, dstB, "目标开始小节")
	if err != nil {
		return nil, err
	}
	dHi := dLo + (sHi - sLo)
	delta := dLo - sLo

	// 定位**当前片段**（页面不传就自己读：`noteScope` 与 xcopy.within 同一套）
	clipUUID := strings.Trim(strings.TrimSpace(value(vals, "clipUuid", "")), `"`)
	clipLabel := ""
	if clipUUID == "" {
		sc, err := readNoteScope(ctx, rd, false)
		if err != nil {
			return nil, fmt.Errorf("定位当前片段失败（也可以直接给 clipUuid）：%w", err)
		}
		clipUUID = sc.ClipUUID
		clipLabel = fmt.Sprintf("轨 %d 片段 %d「%s」", sc.TrackIndex, sc.ClipIndex, sc.ClipName)
	}

	cont, err := acep.Open(p)
	if err != nil {
		return nil, err
	}
	root, err := cbor.Decode(cont.Payload)
	if err != nil {
		return nil, fmt.Errorf("payload 解不开（只支持 `ACEP2` 这一种壳）：%w", err)
	}
	track, pat, err := findPatternByUUID(root, clipUUID)
	if err != nil {
		return nil, err
	}
	if tu := pat.Get("timeUnit"); tu != nil && tu.Kind == cbor.KText && tu.Text == "sec" {
		return nil, fmt.Errorf("这个片段是**音频片段**（timeUnit:sec）—— tick 栅格换算不适用（技能 §2.5），拒绝")
	}

	rep := &pitchCopyReport{
		DryRun: true, Path: p,
		Clip:  clipLabel,
		Track: nodeText(track.Get("name")),
		Src:   fmt.Sprintf("第 %g–%g 小节（工程 tick %d..%d）", srcB, srcE, sLo, sHi),
		Dst:   fmt.Sprintf("从第 %g 小节起（工程 tick %d..%d，同长度 %d tick）", dstB, dLo, dHi, sHi-sLo),
	}
	plan := &pitchPlan{rep: rep, cont: cont, raw: cont.RawBytes(), dstPat: pat, clipUUID: clipUUID}
	if err := fillPitchKindReports(plan, pat, pat, sLo, sHi, dLo, dHi, delta, vals); err != nil {
		return nil, err
	}
	return finishPitchPlan(plan)
}

// openACEPRoot 读工程文件并解出 CBOR 树（`pitch.within` / `pitch.xcopy` 共用）。
func openACEPRoot(p string) (*acep.Container, *cbor.Node, error) {
	cont, err := acep.Open(p)
	if err != nil {
		return nil, nil, err
	}
	root, err := cbor.Decode(cont.Payload)
	if err != nil {
		return nil, nil, fmt.Errorf("payload 解不开（只支持 `ACEP2` 这一种壳）：%w", err)
	}
	return cont, root, nil
}

// fillPitchKindReports 把三类内容的改动算进 plan（源 = srcPat，目标 = dstPat；同轨时两者是同一个节点）。
func fillPitchKindReports(plan *pitchPlan, srcPat, dstPat *cbor.Node, sLo, sHi, dLo, dHi, delta int64, vals map[string]string) error {
	rep := plan.rep
	var edits []cbor.Edit
	var add []*cbor.Node // 要往**目标** lane 里追加的新 entry（曲线 + 锚点一起，最后统一插一次）
	if flagOn(vals, "line", true) {
		e, entries, msg, err := planCopyCurve(srcPat, dstPat, sLo, sHi, dLo, dHi, delta)
		if err != nil {
			return err
		}
		edits = append(edits, e...)
		add = append(add, entries...)
		rep.Line = pitchKindReport{On: true, Summary: msg}
	} else {
		rep.Line = pitchKindReport{On: false, Summary: "没勾，跳过"}
	}
	if flagOn(vals, "anchor", true) {
		entries, msg, err := planCopyAnchors(srcPat, sLo, sHi, delta)
		if err != nil {
			return err
		}
		add = append(add, entries...)
		plan.trimAnchors = true
		plan.trimLo, plan.trimHi = dLo, dHi
		rep.Anchor = pitchKindReport{On: true, Summary: msg}
	} else {
		rep.Anchor = pitchKindReport{On: false, Summary: "没勾，跳过"}
	}
	if flagOn(vals, "vibrato", true) {
		e, msg, err := planCopyVibrato(srcPat, dstPat, sLo, sHi, delta, tolTicksOf(vals))
		if err != nil {
			return err
		}
		edits = append(edits, e...)
		rep.Vibrato = pitchKindReport{On: true, Summary: msg}
	} else {
		rep.Vibrato = pitchKindReport{On: false, Summary: "没勾，跳过"}
	}
	plan.edits = edits
	plan.add = add
	return nil
}

// finishPitchPlan 追加新 entry → 去重 → 套用 → 自检（干跑与真写共用）。
func finishPitchPlan(plan *pitchPlan) (*pitchPlan, error) {
	rep, cont := plan.rep, plan.cont

	// 新 entry **统一插一次**（数组头只改一次；多条一起插，顺序按时间排）
	if len(plan.add) > 0 {
		lane := laneOf(plan.dstPat, "pitchDelta")
		if lane == nil {
			return nil, fmt.Errorf("目标片段没有 pitchDelta lane，却有要追加的 entry（内部不一致）")
		}
		sort.SliceStable(plan.add, func(i, j int) bool { return entryKey(plan.add[i]) < entryKey(plan.add[j]) })

		// 覆盖语义：先清掉目标区间里的旧锚点，再写新的（否则重复执行会叠加）
		conflict := false
		if plan.trimAnchors {
			for _, e := range plan.edits {
				if e.Start >= lane.Start && e.End <= lane.End {
					conflict = true
					break
				}
			}
		}
		if plan.trimAnchors && !conflict {
			ed, note, err := rebuildLaneCovering(lane, plan.add, plan.trimLo, plan.trimHi)
			if err != nil {
				return nil, err
			}
			plan.edits = append(plan.edits, ed...)
			rep.Anchor.Summary += "；" + note
		} else {
			if plan.trimAnchors && conflict {
				rep.Anchor.Summary += "；目标区间已同时改了曲线，本次不覆盖旧锚点（避免同一条 lane 两处编辑打架）"
			}
			e, err := appendLaneEntries(lane, plan.add, "音高线：追加 entry")
			if err != nil {
				return nil, err
			}
			plan.edits = append(plan.edits, e...)
		}
	}

	if len(plan.edits) == 0 {
		rep.Hint = "这几类都没算出要写的东西 —— 一个字节都不会改。"
		return plan, nil
	}
	edits, err := dedupeEdits(plan.edits)
	if err != nil {
		return nil, err
	}
	newPay, err := cbor.Apply(cont.Payload, edits)
	if err != nil {
		return nil, fmt.Errorf("套用改动失败：%w", err)
	}
	plan.newPay = newPay

	// 自检：改完的 payload 必须还能**恰好解完**（技能 §4 第 ⑥ 步）
	if _, err := cbor.Decode(newPay); err != nil {
		return nil, fmt.Errorf("改完的 payload 自检没过（解不开）：%w", err)
	}
	rep.Checks = append(rep.Checks, fmt.Sprintf("payload 重解通过（%d → %d 字节）", len(cont.Payload), len(newPay)))
	plan.backup = backupPathFor(rep.Path)
	rep.Backup = plan.backup
	return plan, nil
}

// ---------- ① 曲线 ----------

// planCopyCurve 把源窗口里的曲线样本搬到目标窗口。
//
// ⚠️ **绝对不要**把样本 tick 取整成 int64 再匹配目标样本 —— 15/16 的栅格下，
// 整数 tick 会把 i、i+1 压到同一个 tick 上（实测踩过：5 个样本塌成 3 个，值被后面的盖掉）。
// 这里用**样本下标**做映射（纯整数/有理数运算，无取整损失）：
//
//	源 entry offset=so、第 i 个样本 → 目标 tick = so+delta + 15i/16
//	目标 entry offset=do ⇒ 第 j 个样本的 tick = do + 15j/16
//	两者对齐 ⇒ j = round(16*((so+delta)-do)/15) + i
func planCopyCurve(src, dst *cbor.Node, sLo, sHi, dLo, dHi, delta int64) ([]cbor.Edit, []*cbor.Node, string, error) {
	srcLane := laneOf(src, "pitchDelta")
	if srcLane == nil {
		return nil, nil, "源片段没有 pitchDelta lane（没有音高线）", nil
	}
	lane := laneOf(dst, "pitchDelta")
	if lane == nil {
		return nil, nil, "目标片段没有 pitchDelta lane（没有音高线）", nil
	}
	type dstCover struct {
		offset int64
		count  int64
		node   *cbor.Node
	}
	type pending struct { // 目标没有覆盖的**连续**样本段：攒成"新建一条 entry"
		srcOffset int64
		firstIdx  int64
		vals      []float64
	}
	var covers []dstCover
	var edits []cbor.Edit
	var pend []pending
	srcSamples, inPlace := 0, 0

	// ⚠️ 先把**目标**的所有 data entry 收成"覆盖表"，再跑源样本那一趟 ——
	// 边跑边收的话，源 entry 之后的覆盖还没进表，明明能原位覆盖的也会被当成"没覆盖"去新建。
	for _, e := range lane.Items {
		if nodeText(e.Get("type")) != "data" {
			continue
		}
		off, ok1 := nodeInt(e.Get("offset"))
		vals := e.Get("values")
		if !ok1 || vals == nil || vals.Kind != cbor.KArray {
			continue
		}
		covers = append(covers, dstCover{offset: off, count: int64(len(vals.Items)), node: e})
	}

	for _, e := range srcLane.Items {
		if nodeText(e.Get("type")) != "data" {
			continue
		}
		off, ok1 := nodeInt(e.Get("offset"))
		vals := e.Get("values")
		if !ok1 || vals == nil || vals.Kind != cbor.KArray {
			continue
		}
		count := int64(len(vals.Items))
		i0, i1 := pitchSampleWindow(off, count, sLo, sHi)
		srcSamples += int(i1 - i0)
		run := []float64{}
		runStart := int64(0)
		flush := func() {
			if len(run) == 0 {
				return
			}
			pend = append(pend, pending{srcOffset: off, firstIdx: runStart, vals: run})
			run = []float64{}
		}
		for i := i0; i < i1; i++ {
			v := vals.Items[i].Float
			done := false
			for _, cv := range covers {
				if cv.node == e {
					continue
				}
				base := int64(round(float64(pitchTickDen*((off+delta)-cv.offset)) / float64(pitchTickNum)))
				j := base + i
				if j < 0 || j >= cv.count {
					continue
				}
				// 目标 tick 必须落在目标窗口里。
				// ⚠️ 容差 1 tick：样本的真 tick 是 15/16 的倍数（如 2003.75），取整到 tick 会变成
				// 2004 —— 卡死在窗口边界上（实测踩过：最后一个样本被吃掉、白新建一条 entry）。
				tgtTick := int64(round(pitchSampleTick(cv.offset, j)))
				if tgtTick < dLo-1 || tgtTick > dHi {
					continue
				}
				it := cv.node.Get("values").Items[j]
				if it.IsF16 || it.Len() != 9 {
					continue
				}
				edits = append(edits, cbor.Edit{Start: it.Start, End: it.End, Bytes: cbor.EncodeFloat64(v),
					What: fmt.Sprintf("曲线：目标 tick %d 的样本 → %g", tgtTick, v)})
				inPlace++
				done = true
				break
			}
			if !done {
				if len(run) == 0 {
					runStart = i
				}
				run = append(run, v)
			} else {
				flush()
			}
		}
		flush()
	}

	if srcSamples == 0 {
		return nil, nil, "源区间里没有曲线样本", nil
	}
	var newEntries []*cbor.Node
	newSamples := 0
	for _, g := range pend {
		// 新 entry 的 offset = 段内第一个样本的目标 tick（取整到 tick；栅格相位与源一致）
		off := int64(round(pitchSampleTick(g.srcOffset+delta, g.firstIdx)))
		entry := cbor.EncodeMap(
			[2]any{"type", "data"},
			[2]any{"offset", off},
			[2]any{"values", g.vals},
		)
		n, err := cbor.Decode(entry)
		if err != nil {
			return nil, nil, "", err
		}
		newEntries = append(newEntries, n)
		newSamples += len(g.vals)
	}
	msg := fmt.Sprintf("源 %d 个样本：写进已有覆盖 %d 处；新建 %d 条 entry（%d 个样本）",
		srcSamples, inPlace, len(pend), newSamples)
	if inPlace == 0 && len(pend) == 0 {
		msg = fmt.Sprintf("源 %d 个样本，但目标区间里没有一个能落位（检查小节号）", srcSamples)
	}
	return edits, newEntries, msg, nil
}

// ---------- ② 锚点 ----------

func planCopyAnchors(pat *cbor.Node, sLo, sHi, delta int64) ([]*cbor.Node, string, error) {
	lane := laneOf(pat, "pitchDelta")
	if lane == nil {
		return nil, "这个片段没有 pitchDelta lane", nil
	}
	// 源里的锚点通常是**多条独立 entry**（每段颤音/滑音一条）。
	// 必须逐条平移、逐条写回；合并成一条会让 ACE 把它们连成一条连续曲线。
	var out []*cbor.Node
	srcEntries, srcPts := 0, 0
	for _, e := range lane.Items {
		if nodeText(e.Get("type")) != "anchor" {
			continue
		}
		pn := e.Get("points")
		if pn == nil || pn.Kind != cbor.KArray || len(pn.Items)%2 != 0 {
			continue
		}
		vn := e.Get("pointsVUV")
		var pts []any
		var vuv []float64
		for k := 0; k+1 < len(pn.Items); k += 2 {
			t, ok := nodeInt(pn.Items[k])
			if !ok || t < sLo || t >= sHi {
				continue
			}
			pts = append(pts, t+delta, pn.Items[k+1].Float)
			var v float64
			if vn != nil && vn.Kind == cbor.KArray && len(vn.Items) > k/2 {
				v = vn.Items[k/2].Float
			}
			vuv = append(vuv, v)
		}
		if len(pts) == 0 {
			continue
		}
		out = append(out, encodeAnchor(pts, vuv, vn != nil))
		srcEntries++
		srcPts += len(pts) / 2
	}
	if srcPts == 0 {
		return nil, "源区间里没有锚点", nil
	}
	return out, fmt.Sprintf("源 %d 条 entry / %d 点 → 逐条搬 %d 条（保持分段，值仍是绝对音高）",
		srcEntries, srcPts, len(out)), nil
}

// encodeAnchor 生成一条 anchor entry。withVUV 决定是否带 `pointsVUV` ——
// 源里没有这个键时**不能**补一个全 0 的数组：0 = 无声，ACE 会当作无音高数据，整段不画。
func encodeAnchor(pts []any, vuv []float64, withVUV bool) *cbor.Node {
	pairs := [][2]any{{"type", "anchor"}, {"points", pts}}
	if withVUV {
		pairs = append(pairs, [2]any{"pointsVUV", vuv})
	}
	n, err := cbor.Decode(cbor.EncodeMap(pairs...))
	if err != nil {
		return nil
	}
	return n
}

// ---------- ③ 音符颤音 ----------

func planCopyVibrato(src, dst *cbor.Node, sLo, sHi, delta int64, tolTicks int64) ([]cbor.Edit, string, error) {
	notes := dst.Get("notes")
	if notes == nil || notes.Kind != cbor.KArray {
		return nil, "目标片段没有音符", nil
	}
	srcNotes := src.Get("notes")
	if srcNotes == nil || srcNotes.Kind != cbor.KArray {
		return nil, "源片段没有音符", nil
	}
	type noteRef struct {
		node  *cbor.Node
		pos   int64
		pitch int64
	}
	var dstRefs []noteRef
	for _, n := range notes.Items {
		if p, ok := nodeInt(n.Get("pos")); ok {
			pitch, _ := nodeInt(n.Get("pitch"))
			dstRefs = append(dstRefs, noteRef{node: n, pos: p, pitch: pitch})
		}
	}
	sort.SliceStable(dstRefs, func(i, j int) bool { return dstRefs[i].pos < dstRefs[j].pos })

	// 段落之间的音符起点常有几 tick 的量化差（实测 5 tick），所以按“最近邻”配对，
	// 不能要求 pos 完全相等；同音高优先，超出容忍窗就不硬配。
	var edits []cbor.Edit
	srcN, wrote, noNear := 0, 0, 0
	var offs []int64
	for _, sn := range srcNotes.Items {
		spos, ok := nodeInt(sn.Get("pos"))
		if !ok || spos < sLo || spos >= sHi {
			continue
		}
		vib := sn.Get("vibrato")
		if vib == nil {
			continue
		}
		srcN++
		want := spos + delta
		spitch, _ := nodeInt(sn.Get("pitch"))
		// 在排序表里找最近的若干条，同音高优先
		best, bestSame := -1, -1
		var bestOff, bestSameOff int64 = 1 << 62, 1 << 62
		i := sort.Search(len(dstRefs), func(k int) bool { return dstRefs[k].pos >= want })
		for k := i - 4; k <= i+4; k++ {
			if k < 0 || k >= len(dstRefs) {
				continue
			}
			off := dstRefs[k].pos - want
			if off < 0 {
				off = -off
			}
			if off > tolTicks {
				continue
			}
			if off < bestOff {
				bestOff, best = off, k
			}
			if dstRefs[k].pitch == spitch && off < bestSameOff {
				bestSameOff, bestSame = off, k
			}
		}
		if bestSame >= 0 { // 同音高优先，其次是最近的
			best = bestSame
		}
		if best < 0 {
			noNear++
			continue
		}
		hit := dstRefs[best]
		offs = append(offs, hit.pos-want)
		keys := append([]*cbor.Node(nil), hit.node.Keys...)
		vals := append([]*cbor.Node(nil), hit.node.Values...)
		replaced := false
		for k, key := range keys {
			if key.KeyString() == "vibrato" {
				vals[k] = vib
				replaced = true
				break
			}
		}
		if !replaced {
			keys = append(keys, &cbor.Node{Kind: cbor.KText, Text: "vibrato"})
			vals = append(vals, vib)
		}
		grown := &cbor.Node{Kind: cbor.KMap, Keys: keys, Values: vals}
		edits = append(edits, cbor.Edit{Start: hit.node.Start, End: hit.node.End, Bytes: cbor.EncodeNode(grown),
			What: fmt.Sprintf("颤音：写到 tick %d 的音符上（起点差 %d tick）", hit.pos, hit.pos-want)})
		wrote++
	}
	if srcN == 0 {
		return nil, "源区间里没有带颤音的音符", nil
	}
	msg := fmt.Sprintf("源 %d 个带颤音的音符 → 写到 %d 个目标音符（按最近邻配对，容忍 ±%d tick）", srcN, wrote, tolTicks)
	if len(offs) > 0 {
		var mn, mx int64 = offs[0], offs[0]
		var sum int64
		for _, o := range offs {
			if o < mn {
				mn = o
			}
			if o > mx {
				mx = o
			}
			sum += o
		}
		msg += fmt.Sprintf("；起点差 %d..%d tick（均值 %d）", mn, mx, sum/int64(len(offs)))
	}
		if noNear > 0 {
		msg += fmt.Sprintf("；%d 个目标处 ±%d tick 内没有音符", noNear, tolTicks)
	}
	return edits, msg, nil
}

// ---------- 公共小工具 ----------

func laneOf(pat *cbor.Node, name string) *cbor.Node {
	ps := pat.Get("parameters")
	if ps == nil || ps.Kind != cbor.KMap {
		return nil
	}
	l := ps.Get(name)
	if l == nil || l.Kind != cbor.KArray {
		return nil
	}
	return l
}

// appendLaneEntries 往 lane 数组**追加**若干 entry：数组头 +N，并在**数组末尾**插入元素字节。
// 返回两条编辑（头一条、插入一条）—— 它们互不重叠（头在数组最前、插入在数组最后）。
func appendLaneEntries(lane *cbor.Node, add []*cbor.Node, what string) ([]cbor.Edit, error) {
	if len(add) == 0 {
		return nil, nil
	}
	// 数组头的字节范围：从 lane.Start 开始，长度 = Start..第一个元素的 Start
	headEnd := lane.End
	if len(lane.Items) > 0 {
		headEnd = lane.Items[0].Start
	}
	var body []byte
	for _, n := range add {
		body = append(body, cbor.EncodeNode(n)...)
	}
	head := cbor.EncodeArrayHeader(uint64(len(lane.Items) + len(add)))
	return []cbor.Edit{
		{Start: lane.Start, End: headEnd, Bytes: head, What: what + "：数组头 " + fmt.Sprint(len(lane.Items)) + "→" + fmt.Sprint(len(lane.Items)+len(add))},
		{Start: lane.End, End: lane.End, Bytes: body, What: what + "：插入 " + fmt.Sprint(len(add)) + " 条"},
	}, nil
}

// rebuildLaneCovering 把整条 pitchDelta lane 重建成：
// 原有条目（**目标区间里的锚点先被剔掉**）+ 新 entry，按时间排序。
//
// 这样重复执行只会替换目标区间那一段，不会叠出两条一模一样的 anchor。
func rebuildLaneCovering(lane *cbor.Node, add []*cbor.Node, lo, hi int64) ([]cbor.Edit, string, error) {
	var kept []*cbor.Node
	removedPts, removedEntries := 0, 0
	for _, e := range lane.Items {
		if nodeText(e.Get("type")) != "anchor" {
			kept = append(kept, e)
			continue
		}
		pn := e.Get("points")
		if pn == nil || pn.Kind != cbor.KArray || len(pn.Items)%2 != 0 {
			kept = append(kept, e)
			continue
		}
		vn := e.Get("pointsVUV")
		var pts []any
		var vuv []float64
		for k := 0; k+1 < len(pn.Items); k += 2 {
			t, ok := nodeInt(pn.Items[k])
			if !ok {
				pts = append(pts, pn.Items[k], pn.Items[k+1])
				continue
			}
			if t >= lo && t < hi {
				removedPts++
				continue
			}
			pts = append(pts, pn.Items[k], pn.Items[k+1])
			var v float64
			if vn != nil && vn.Kind == cbor.KArray && len(vn.Items) > k/2 {
				v = vn.Items[k/2].Float
			}
			vuv = append(vuv, v)
		}
		if len(pts) == 0 {
			removedEntries++
			continue
		}
		n := encodeAnchor(pts, vuv, vn != nil)
		if n == nil {
			return nil, "", fmt.Errorf("重建 anchor entry 失败")
		}
		kept = append(kept, n)
	}
	all := append(kept, add...)
	sort.SliceStable(all, func(i, j int) bool { return entryKey(all[i]) < entryKey(all[j]) })
	var body []byte
	for _, n := range all {
		body = append(body, cbor.EncodeNode(n)...)
	}
	out := append(cbor.EncodeArrayHeader(uint64(len(all))), body...)
	note := fmt.Sprintf("覆盖目标区间：清掉 %d 个点/%d 条旧锚点，写 %d 条，lane 共 %d 条",
		removedPts, removedEntries, len(add), len(all))
	return []cbor.Edit{{
		Start: lane.Start, End: lane.End, Bytes: out,
		What: "音高线：" + note,
	}}, note, nil
}

// findPatternByUUID 在 tracks[*].patterns[*] 里按 uuid 找片段（**不按索引** ——
// 文件里 tracks 恒 100 个槽位、空槽也在，索引与 CLI 的 trackIndex 不是一回事）。
func findPatternByUUID(root *cbor.Node, uuid string) (*cbor.Node, *cbor.Node, error) {
	tracks := root.Get("tracks")
	if tracks == nil || tracks.Kind != cbor.KArray {
		return nil, nil, fmt.Errorf("payload 里没有 tracks")
	}
	for _, tr := range tracks.Items {
		pats := tr.Get("patterns")
		if pats == nil || pats.Kind != cbor.KArray {
			continue
		}
		for _, pat := range pats.Items {
			if nodeText(pat.Get("uuid")) == uuid {
				return tr, pat, nil
			}
		}
	}
	return nil, nil, fmt.Errorf("在工程文件里找不到 uuid = %s 的片段（ACE 里那份与文件里这份不一致？先「保存工程」再试）", uuid)
}

func nodeText(n *cbor.Node) string {
	if n == nil {
		return ""
	}
	if n.Kind == cbor.KText {
		return n.Text
	}
	return ""
}

func nodeInt(n *cbor.Node) (int64, bool) {
	if n == nil {
		return 0, false
	}
	switch n.Kind {
	case cbor.KUint:
		return int64(n.Uint), true
	case cbor.KInt:
		return n.Int, true
	case cbor.KFloat:
		return int64(n.Float), true
	}
	return 0, false
}

func countEdits(edits []cbor.Edit, prefix string) int {
	n := 0
	for _, e := range edits {
		if strings.HasPrefix(e.What, prefix) {
			n++
		}
	}
	return n
}

// entryKey 是新 entry 的排序键（时间顺序）：`data` 看 offset、`anchor` 看第一个点的 tick。
func entryKey(n *cbor.Node) float64 {
	if nodeText(n.Get("type")) == "anchor" {
		pts := n.Get("points")
		if pts != nil && pts.Kind == cbor.KArray && len(pts.Items) > 0 {
			if t, ok := nodeInt(pts.Items[0]); ok {
				return float64(t)
			}
		}
		return 0
	}
	if off, ok := nodeInt(n.Get("offset")); ok {
		return float64(off)
	}
	return 0
}

// dedupeEdits 把编辑表理成"互不重叠、可以直接交给 cbor.Apply"的一份：
//   - 同一段被写了多次（多个源样本落到同一个目标样本）⇒ 留**最后一次**（源是顺序走的）
//   - 同一个位置的多段**零长插入** ⇒ 合并字节（两条 append 落在同一个插入点）
//   - 剩下的真重叠 ⇒ 报错（不猜）
func dedupeEdits(in []cbor.Edit) ([]cbor.Edit, error) {
	if len(in) == 0 {
		return in, nil
	}
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].Start != in[j].Start {
			return in[i].Start < in[j].Start
		}
		return in[i].End < in[j].End
	})
	out := make([]cbor.Edit, 0, len(in))
	for _, e := range in {
		if n := len(out); n > 0 && out[n-1].Start == e.Start && out[n-1].End == e.End {
			if e.Start == e.End {
				// 零长插入：合并（顺序无关，反正是同一个点的字节）
				out[n-1].Bytes = append(out[n-1].Bytes, e.Bytes...)
				out[n-1].What = out[n-1].What + " + " + e.What
			} else {
				out[n-1] = e // 同一段覆盖：留最后一次
			}
			continue
		}
		out = append(out, e)
	}
	for i := 1; i < len(out); i++ {
		if out[i].Start < out[i-1].End {
			return nil, fmt.Errorf("两处改动区间重叠：[%d,%d)「%s」与 [%d,%d)「%s」—— 拒绝（不猜）",
				out[i-1].Start, out[i-1].End, out[i-1].What, out[i].Start, out[i].End, out[i].What)
		}
	}
	return out, nil
}

// flagOn 读一个勾选框（默认值给"没填时"用）。
func flagOn(vals map[string]string, name string, def bool) bool {
	v := strings.TrimSpace(vals[name])
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "true", "on", "yes", "是":
		return true
	case "0", "false", "off", "no", "否":
		return false
	}
	return def
}

// backupPathFor 是"按时间戳备份到临时目录"的落点（用户 2026-10-08：「在临时文件夹存一个工程备份
// 以便恢复刚才的操作」）。同名同秒会加序号，绝不覆盖旧备份。
func backupPathFor(p string) string {
	dir := filepath.Join(os.TempDir(), "ACETool", "acep-backup")
	base := strings.TrimSuffix(filepath.Base(p), ".acep")
	stamp := time.Now().Format("20060102-150405")
	cand := filepath.Join(dir, base+"-"+stamp+".acep")
	for i := 2; ; i++ {
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
		cand = filepath.Join(dir, fmt.Sprintf("%s-%s-%d.acep", base, stamp, i))
	}
}

// writeBackup 把原字节复制到临时目录，并**回读校验**（大小 + sha256）。
func writeBackup(p string, raw []byte) (string, error) {
	dst := backupPathFor(p)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(dst, raw, 0o644); err != nil {
		return "", err
	}
	back, err := os.ReadFile(dst)
	if err != nil {
		return "", err
	}
	if len(back) != len(raw) || sha256.Sum256(back) != sha256.Sum256(raw) {
		return "", fmt.Errorf("备份回读不一致（%s）—— 不写工程文件", dst)
	}
	return dst, nil
}

func shaOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// ---------- 工具注册 ----------

func init() {
	register(&Tool{
		ID: "pitch.within", Category: "工程",
		Title: "轨道内复制音高线（文件路线）",
		Desc: "把当前片段某一段小节里的**音高线**复制到同一片段另一处：三类一起搬 —— " +
			"① 曲线（`pitchDelta` 的 `data` entry，delta 半音，栅格 15/16 tick/样本）" +
			"② 锚点（同 lane 的 `anchor` entry，**绝对音高**）③ 音符颤音（`notes[k].vibrato`）。" +
			"⚠️ 这是**改工程文件**（CLI 上 `pitch` 恒 available:false）：执行会**原地**替换那份 `.acep`，" +
			"并把原文件备份到 `%TEMP%\\ACETool\\acep-backup\\`。纪律要求先「保存工程」再执行、" +
			"执行完让 ACE 重新打开（页面那条流程会做这三步）。",
		Kind:   KindWrite,
		Source: "技能 acep v1.2.3 §2.2（三种画法）/ §4（安全写入六步）/ §8（纪律）",
		Params: []Param{
			textParam("path", "工程文件（.acep）", "", `C:\…\名字\名字.acep`,
				"← 页面上一步「保存工程」会给出来；**不能留空**"),
			textParam("clipUuid", "片段 uuid（留空 = 当前片段）", "", "{…}", "留空时用「当前片段」定位"),
			numParam("srcBeginBar", "源：开始小节", "1"),
			numParam("srcEndBar", "源：结束小节", "1"),
			numParam("dstBeginBar", "目标：开始小节", "1"),
			checkParam("line", "① 曲线", true),
			checkParam("anchor", "② 锚点", true),
			checkParam("vibrato", "③ 音符颤音", true),
		numParam("tolTicks", "配对阈值（tick，起点差在这之内算同一个音）", "30"),
		},
		Plan:  pitchWithinPlan,
		Apply: pitchWithinApply,
	})
}

func pitchWithinPlan(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	plan, err := buildPitchPlan(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	if plan.newPay == nil {
		plan.rep.Hint = "没算出要写的东西 —— 一个字节都不会改。"
		return plan.rep, nil
	}
	plan.rep.Hint = "**一个字节都没写**（计划那一步）。要改就走「执行」那一步" +
		"（会先备份原文件到临时目录，再原地替换）。"
	return plan.rep, nil
}

// readbackPitchAnchors 用 CLI 读回目标区间里的锚点（ACE 眼里的样子）。
func readbackPitchAnchors(ctx context.Context, rd ReadFunc, clipUUID string, lo, hi int64) (int, string) {
	if clipUUID == "" {
		return 0, ""
	}
	var out struct {
		Layers []struct {
			Layer  string          `json:"layer"`
			Shape  string          `json:"shape"`
			Points json.RawMessage `json:"points"`
		} `json:"layers"`
	}
	if err := readJSON(ctx, rd, &out, "vocalpitch", "read", "--clip-uuid", clipUUID,
		"--level", "user", "--range-begin", fmt.Sprint(lo), "--range-end", fmt.Sprint(hi), "--json"); err != nil {
		return 0, fmt.Sprintf("回读失败（%v）—— 以文件自检为准", err)
	}
	for _, l := range out.Layers {
		if l.Layer != "user" {
			continue
		}
		var pairs [][2]*float64
		if err := json.Unmarshal(l.Points, &pairs); err != nil {
			return 0, "ACE 回读：user 层的点解不开"
		}
		n := 0
		for _, p := range pairs {
			if p[0] != nil && p[1] != nil && *p[0] >= float64(lo) && *p[0] < float64(hi) {
				n++
			}
		}
		return n, fmt.Sprintf("ACE 回读：目标区间里有 %d 个锚点", n)
	}
	return 0, "ACE 回读：user 层没有数据"
}

func pitchWithinApply(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	plan, err := buildPitchPlan(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	got, err := applyPitchPlan(plan)
	if err != nil {
		return nil, err
	}
	if rep, ok := got.(*pitchCopyReport); ok && rep != nil && !rep.DryRun {
		if n, msg := readbackPitchAnchors(ctx, rd, plan.clipUUID, plan.trimLo, plan.trimHi); msg != "" {
			rep.Hint += "\n" + msg
			if plan.trimAnchors && n == 0 {
				rep.Hint += "（写入后 ACE 看不到锚点 —— 让 ACE 重新打开工程再读）"
			}
		}
	}
	return got, nil
}

// applyPitchPlan 是**唯一**真写工程文件的那条路（`pitch.within` / `pitch.xcopy` 共用）：
// ① 备份到临时目录（硬要求）② 重打包 ③ 同目录临时文件 + rename 原地替换 ④ 回读逐字节核对。
func applyPitchPlan(plan *pitchPlan) (any, error) {
	if plan.newPay == nil {
		plan.rep.DryRun = false
		plan.rep.Hint = "没算出要写的东西 —— 一个字节都没改。"
		return plan.rep, nil
	}

	// ① 备份（**硬要求**：用户 2026-10-08「在临时文件夹存一个工程备份以便恢复刚才的操作」）
	backup, err := writeBackup(plan.rep.Path, plan.raw)
	if err != nil {
		return nil, fmt.Errorf("备份失败，**不写工程文件**：%w", err)
	}

	// ② 重打包（头照 §1.1：只改 off16/off24，高熵区原样）
	out, err := plan.cont.Repack(plan.newPay)
	if err != nil {
		return nil, err
	}

	// ③ 原地替换：先写同目录临时文件再 rename（同一分区，失败不会留下半截文件）
	tmp := plan.rep.Path + ".acetool-tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, plan.rep.Path); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}

	// ④ 文件级回读核对：重新打开，payload 必须与**我算的那份**逐字节一致
	c2, err := acep.Open(plan.rep.Path)
	if err != nil {
		return nil, fmt.Errorf("写完回读失败（文件可能坏了 —— 备份在 %s）：%w", backup, err)
	}
	same := len(c2.Payload) == len(plan.newPay)
	if same {
		for i := range c2.Payload {
			if c2.Payload[i] != plan.newPay[i] {
				same = false
				break
			}
		}
	}
	if !same {
		return nil, fmt.Errorf("写完回读**对不上**（payload 不一致 —— 备份在 %s）", backup)
	}

	plan.rep.DryRun = false
	plan.rep.Backup = backup
	plan.rep.Checks = append(plan.rep.Checks,
		fmt.Sprintf("回读：payload 逐字节一致（%d 字节，sha %s）", len(out), shaOf(out)),
		"备份：原文件 sha "+shaOf(plan.raw))
	plan.rep.Hint = "已**原地**改写工程文件（备份见上）。下一步：让 ACE 重新打开它" +
		"（页面会自动做：`project open --discard-changes`），然后读回确认。"
	return plan.rep, nil
}

// tolTicksOf 读配对阈值（tick）。两段之间常有几 tick 的起点量化差，
// 默认 30 tick（= 1/16 四分音符）足够吸住；给 0 则退回“必须完全相等”。
func tolTicksOf(vals map[string]string) int64 {
	raw := strings.TrimSpace(value(vals, "tolTicks", "30"))
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 30
	}
	return n
}
