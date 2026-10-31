package tools

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"acetool/internal/cbor"
)

// 语调教（**文件路线**）—— 用户 2026-10-10 的口径 + 参考 SV1 那份 `语调教.js`。
//
// 每个音符按声调写**音高锚点**（技能 acep §2.2 **画法②**：同一条 `pitchDelta` lane 里的
// `type:"anchor"` entry，`points` = `[tick, 绝对音高, …]`、`pointsVUV` 每点一个 0/1）：
//
//	① 先**清**这个音符占的 tick 区间 `[pos, pos+dur)`：区间内的**锚点**拿掉，
//	   区间内的**曲线样本**（画法① `data` 的 `values`）**真删掉**（entry 按需拆成前后两段）；
//	② 再按声调加一条新的 anchor entry。
//
// 调型表**照搬**那份 JS（它用**音分**，这里 ÷100 换成半音；锚点值是**绝对音高** = 音符音高 + 偏移），
// **只有"低平段占时长的比例"按用户 2026-10-10 的裁定改了**（JS 原值 二声 0.3 / 三声 0.7）：
//
//	1 阴平 → (pos,0) (end,0)                                    平
//	2 阳平 → (pos,0) (pos+1,−4) (pos+15%·dur,−4) (end,0)         先降后升（低平段 15%）
//	3 上声 → (pos,0) (pos+1,−8) (pos+50%·dur,−8) (end,0)         降升（214，低平段 50%）
//	4 去声 → (pos,0) (end−1,−12) (end,0)                        全音域下滑
//	轻声 / 延音 / 非中文 / 定不出 → **完全不动**（不清也不写）—— 用户 2026-10-10 裁定
//
// ⚠️ **每个音符一条 anchor entry**（技能 §2.2 实测样本：同一画法画两笔就是两条 entry、按时间接力）
// ⇒ 新 entry 按首点 tick **稳定插入**，不粗暴追加到末尾。
// ⚠️ 音符的 `pos/dur` 与 lane 里的 tick 都是**片段内局部**坐标 ⇒ 直接比，不换算。
// ⚠️ **形状自检**：技能 §3.3 的教训「回读只能证明写进去的 == 打算写的，证明不了打算写的对」
// ⇒ 写完在**新 payload** 上再查一遍：每个目标区间里既没有旧锚点、也没有曲线样本，
// 且能找到**恰好一条**点集与我们打算写的一模一样的 anchor entry。
// ⚠️ 拆 `data` entry 时，**后续段**的 `offset` 向上取整到整 tick（≤1 tick ≈ 0.93 ms 的栅格漂移，
// 方向是"往后"，不会把留着的样本挪进被清的区间）—— 因为 `offset` 在真工程里是**整数**，
// 不敢塞浮点（怕 ACE 的解析器不认）。

const toneSource = "技能 acep v1.2.3 §2.2 画法②（anchor：绝对音高 + pointsVUV）/ §3.3 调型表（移植 SV1「语调教.js」）/ §5.5（手写锚点 ACE 认账）"

func init() {
	register(&Tool{
		ID: "tone.apply", Category: "工程",
		Title: "语调教：把声调写成音高锚点（文件路线）",
		Desc: "按声调给每个音符写**音高锚点**：先清掉这个音符区间里的**锚点**与**曲线样本**，" +
			"再按调型加一条 anchor entry（值是**绝对音高** = 音符音高 + 偏移）。\n" +
			"调型照 SV1「语调教.js」（音分 → 半音；**低平段比例按用户 2026-10-10 裁定：二声 15%、三声 50%**）：" +
			"阴平 平 · 阳平 先 −4 再升 · 上声 先 −8 再升 · 去声 `end−1` 处 −12 · " +
			"**轻声/延音/非中文 = 完全不动**（不清也不写）。\n" +
			"⚠️ 执行会**原地**替换工程文件（先备份到 `%TEMP%\\ACETool\\acep-backup\\`，写完逐字节回读 + **形状自检**）。" +
			"纪律：先「保存工程」→ 执行 → 让 ACE 重新打开。",
		Kind:   KindWrite,
		Source: toneSource,
		Params: []Param{
			textParam("path", "工程文件（.acep）", "", `C:\…\名字\名字.acep`, "← 页面上一步「保存工程」会给出来"),
			textParam("notes", "音符 UUID（按时间顺序，逗号分隔）", "", "{…},{…}", "← 页面读歌词那一步会给出来"),
			textParam("tones", "声调串（1 阴平·2 阳平·3 上声·4 去声·`-` 不动）", "", "1234-",
				"与上面的音符**一一对应**，个数必须相同"),
		},
		Plan:  toneApplyPlan,
		Apply: toneApplyApply,
	})
}

func toneApplyPlan(_ context.Context, _ ReadFunc, vals map[string]string) (any, error) {
	plan, err := buildTonePlan(vals)
	if err != nil {
		return nil, err
	}
	if plan.newPay == nil {
		plan.rep.Hint = "没算出要写的东西 —— 一个字节都不会改。"
		return plan.rep, nil
	}
	plan.rep.Hint = "**一个字节都没写**（这是" + "计划" + "那一步）。要改就走执行那一步。"
	return plan.rep, nil
}

func toneApplyApply(_ context.Context, _ ReadFunc, vals map[string]string) (any, error) {
	plan, err := buildTonePlan(vals)
	if err != nil {
		return nil, err
	}
	return applyPitchPlan(plan)
}

// ---------- 计划 ----------

type toneItem struct {
	uuid  string
	tone  byte // '1'..'4'
	pos   int64
	dur   int64
	pitch float64
	label string
}

func buildTonePlan(vals map[string]string) (*pitchPlan, error) {
	p := acepPathParam(vals)
	if p == "" {
		return nil, fmt.Errorf("要一个工程文件路径（path）—— 页面那一步「保存工程」会把它给你（`project save` 的 savedPath）")
	}
	if st, err := os.Stat(p); err != nil {
		return nil, fmt.Errorf("读不到工程文件：%w", err)
	} else if st.IsDir() {
		return nil, fmt.Errorf("%s 是文件夹 —— 要给里面的 .acep 文件", p)
	}
	uuids := splitUUIDs(value(vals, "notes", ""))
	if len(uuids) == 0 {
		return nil, fmt.Errorf("要音符 UUID 列表（notes）—— 页面读歌词那一步会给出来")
	}
	tones := []byte(strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\t' || r == ',' {
			return -1
		}
		return r
	}, value(vals, "tones", "")))
	if len(tones) != len(uuids) {
		return nil, fmt.Errorf("声调串与音符个数对不上：音符 %d 个、声调 %d 个 —— 两边必须一一对应"+
			"（页面是同一次读取生成的；改过 ACE 选区请回页面重新读一次）", len(uuids), len(tones))
	}

	cont, root, err := openACEPRoot(p)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	plan := &pitchPlan{rep: &pitchCopyReport{DryRun: true, Path: p}, cont: cont, raw: raw}
	plan.rep.Src = fmt.Sprintf("范围：%d 个音符", len(uuids))

	// ① 逐音符定位（文件里的 pos/dur/pitch 就是 lane 的局部坐标）
	var patOrder []*cbor.Node
	byPat := map[*cbor.Node][]toneItem{}
	written, skipped := 0, 0
	var miss []string
	for i, id := range uuids {
		pat, note, err := findNoteByUUID(root, id)
		if err != nil {
			miss = append(miss, shortUUID(id)+"（工程文件里没有）")
			continue
		}
		pos, ok1 := nodeInt(note.Get("pos"))
		dur, ok2 := nodeInt(note.Get("dur"))
		pitch, ok3 := nodeFloat(note.Get("pitch"))
		if !ok1 || !ok2 || !ok3 || dur <= 0 {
			miss = append(miss, shortUUID(id)+"（pos/dur/pitch 读不出来）")
			continue
		}
		t := tones[i]
		if t == '-' || t == '0' {
			skipped++
			continue
		}
		if t < '1' || t > '4' {
			return nil, fmt.Errorf("声调串里第 %d 个是 %q —— 只认 `1`/`2`/`3`/`4`（`-` = 不动）", i+1, string(t))
		}
		if _, seen := byPat[pat]; !seen {
			patOrder = append(patOrder, pat)
		}
		byPat[pat] = append(byPat[pat], toneItem{
			uuid: id, tone: t, pos: pos, dur: dur, pitch: pitch,
			label: fmt.Sprintf("%s %s", string(t), orDash(nodeText(note.Get("lyric")))),
		})
		written++
	}
	if written == 0 {
		plan.rep.Dst = fmt.Sprintf("写了 0 个（跳过 %d 个）", skipped)
		plan.rep.Hint = "这批全是「不动」（轻声/延音/非中文）—— 一个字节都不会改。"
		if len(miss) > 0 {
			plan.rep.Checks = append(plan.rep.Checks, "找不到："+strings.Join(miss, "、"))
		}
		return plan, nil
	}
	plan.rep.Dst = fmt.Sprintf("写了 %d 个，跳过 %d 个", written, skipped)

	// ② 逐片段改 lane
	var lineMsg, anchorMsg, notes []string
	for _, pat := range patOrder {
		items := byPat[pat]
		sort.SliceStable(items, func(i, j int) bool { return items[i].pos < items[j].pos })
		name := clipNameOf(pat)
		lane := laneOf(pat, "pitchDelta")
		newLane, st, err := toneLane(lane, items)
		if err != nil {
			return nil, err
		}
		edit, err := toneLaneEdit(pat, lane, newLane)
		if err != nil {
			return nil, err
		}
		plan.edits = append(plan.edits, edit)
		if st.samples > 0 {
			lineMsg = append(lineMsg, fmt.Sprintf("片段「%s」：删掉曲线样本 %d 个、拆成 %d 条 entry",
				name, st.samples, st.splits))
		} else {
			lineMsg = append(lineMsg, fmt.Sprintf("片段「%s」：区间内没有曲线样本（画法①）", name))
		}
		anchorMsg = append(anchorMsg, fmt.Sprintf("片段「%s」：去掉区间内旧锚点 %d 个、加 %d 条 anchor entry（%d 个点）",
			name, st.oldPoints, len(items), st.newPoints))
		for _, it := range items {
			pts, _ := tonePoints(it)
			var pl []string
			for i := 0; i+1 < len(pts); i += 2 {
				t, _ := anyInt(pts[i])
				v, _ := anyFloat(pts[i+1])
				pl = append(pl, fmt.Sprintf("%s→%g", beatLabel(t), v))
			}
			notes = append(notes, fmt.Sprintf("「%s」%s · 音高 %.0f · 第 %s–%s 拍 → 锚点 %s",
				name, it.label, it.pitch, beatLabel(it.pos), beatLabel(it.pos+it.dur), strings.Join(pl, "、")))
		}
	}
	if len(patOrder) == 1 {
		plan.rep.Clip = fmt.Sprintf("片段「%s」", clipNameOf(patOrder[0]))
	} else {
		plan.rep.Clip = fmt.Sprintf("%d 个片段", len(patOrder))
	}
	plan.rep.Line = pitchKindReport{On: true, Summary: strings.Join(lineMsg, "；")}
	plan.rep.Anchor = pitchKindReport{On: true, Summary: strings.Join(anchorMsg, "；")}
	plan.rep.Vibrato = pitchKindReport{On: false, Summary: "没动（用户 2026-10-10 口径：颤音保持原样）"}
	plan.rep.Notes = notes
	if len(miss) > 0 {
		plan.rep.Checks = append(plan.rep.Checks, "找不到/读不出："+strings.Join(miss, "、"))
	}

	plan, err = finishPitchPlan(plan)
	if err != nil {
		return nil, err
	}
	if plan.newPay == nil {
		return plan, nil
	}

	// ③ 形状自检（技能 §3.3 的教训）：在**新 payload** 上核对区间真的干净、锚点真的在里面
	checks, err := toneShapeCheck(plan.newPay, byPat)
	if err != nil {
		return nil, fmt.Errorf("形状自检失败（**不写工程文件**）：%w", err)
	}
	plan.rep.Checks = append(plan.rep.Checks, checks...)
	return plan, nil
}

type toneStat struct {
	samples   int // 删掉的曲线样本数
	splits    int // 拆出来的 data entry 条数（含保留的首尾段）
	oldPoints int // 区间内被去掉的旧锚点点数
	newPoints int // 新加的锚点点数
}

type toneRange struct{ lo, hi int64 }

func inRanges(t int64, ranges []toneRange) bool {
	for _, r := range ranges {
		if t >= r.lo && t < r.hi {
			return true
		}
	}
	return false
}

// toneLane 返回改写后的 lane（清区间 + 加锚点）；lane 为 nil 时只造锚点。
func toneLane(lane *cbor.Node, items []toneItem) (*cbor.Node, toneStat, error) {
	var st toneStat
	ranges := make([]toneRange, 0, len(items))
	for _, it := range items {
		ranges = append(ranges, toneRange{lo: it.pos, hi: it.pos + it.dur})
	}
	var newEntries []*cbor.Node
	for _, it := range items {
		pts, vuv := tonePoints(it)
		if len(pts) == 0 {
			continue
		}
		n, err := cbor.Decode(cbor.EncodeMap(
			[2]any{"type", "anchor"}, [2]any{"points", pts}, [2]any{"pointsVUV", vuv}))
		if err != nil {
			return nil, st, err
		}
		newEntries = append(newEntries, n)
		st.newPoints += len(vuv)
	}

	var keep []*cbor.Node
	if lane != nil && lane.Kind == cbor.KArray {
		for _, e := range lane.Items {
			switch nodeText(e.Get("type")) {
			case "anchor":
				kept, removed, err := stripAnchorPoints(e, ranges)
				if err != nil {
					return nil, st, err
				}
				st.oldPoints += removed
				if kept != nil {
					keep = append(keep, kept)
				}
			case "data":
				pieces, deleted := stripDataSamples(e, ranges)
				st.samples += deleted
				if deleted > 0 {
					st.splits += len(pieces)
				}
				keep = append(keep, pieces...)
			default:
				keep = append(keep, e) // 不认识的 entry 原样留着
			}
		}
	}
	// 新锚点按首点 tick **稳定插入**（不粗暴追加到末尾）
	for _, ne := range newEntries {
		at := len(keep)
		for i, e := range keep {
			if entryKey(e) > entryKey(ne) {
				at = i
				break
			}
		}
		keep = append(keep, nil)
		copy(keep[at+1:], keep[at:])
		keep[at] = ne
	}
	return &cbor.Node{Kind: cbor.KArray, Items: keep}, st, nil
}

// stripAnchorPoints 把落在区间里的点去掉（points / pointsVUV 同步）；一个点都不剩就整条丢掉。
func stripAnchorPoints(e *cbor.Node, ranges []toneRange) (*cbor.Node, int, error) {
	pn := e.Get("points")
	if pn == nil || pn.Kind != cbor.KArray || len(pn.Items)%2 != 0 {
		return e, 0, nil // 形态不认识的锚点：原样留着，不猜
	}
	vn := e.Get("pointsVUV")
	pts := &cbor.Node{Kind: cbor.KArray}
	vuv := &cbor.Node{Kind: cbor.KArray}
	removed := 0
	for k := 0; k+1 < len(pn.Items); k += 2 {
		t, ok := nodeInt(pn.Items[k])
		if !ok {
			return nil, 0, fmt.Errorf("锚点里有个位置（tick）读不出来（第 %d 个点）", k/2+1)
		}
		if inRanges(t, ranges) {
			removed++
			continue
		}
		pts.Items = append(pts.Items, pn.Items[k])
		pv, ok := nodeFloat(pn.Items[k+1])
		if !ok {
			return nil, 0, fmt.Errorf("锚点里有个音高读不出来（第 %d 个点）", k/2+1)
		}
		pts.Items = append(pts.Items, floatNode(pv))
		var v float64
		if vn != nil && vn.Kind == cbor.KArray && len(vn.Items) > k/2 {
			v, _ = nodeFloat(vn.Items[k/2])
		}
		vuv.Items = append(vuv.Items, floatNode(v))
	}
	if removed == 0 {
		return e, 0, nil
	}
	if len(pts.Items) == 0 {
		return nil, removed, nil // 整条被清空 ⇒ 丢掉这条 entry
	}
	out := mapPut(e, "points", pts)
	out = mapPut(out, "pointsVUV", vuv)
	return out, removed, nil
}

// stripDataSamples 把落在区间里的**曲线样本**真删掉：entry 按需拆成（首段 / 中间段 / 尾段）。
//
// 样本 i 的 tick = `offset + 15i/16`（技能 §2.2 的栅格）⇒ 区间 `[lo,hi)` 覆盖的样本下标
// `[ceil(16(lo−offset)/15), ceil(16(hi−offset)/15))`（夹到 `[0,count]`）。
func stripDataSamples(e *cbor.Node, ranges []toneRange) ([]*cbor.Node, int) {
	vals := e.Get("values")
	if vals == nil || vals.Kind != cbor.KArray {
		return []*cbor.Node{e}, 0
	}
	off, ok := nodeInt(e.Get("offset"))
	if !ok {
		return []*cbor.Node{e}, 0
	}
	count := int64(len(vals.Items))
	var dels [][2]int64
	for _, r := range ranges {
		i0 := ceilDiv(pitchTickDen*(r.lo-off), pitchTickNum)
		i1 := ceilDiv(pitchTickDen*(r.hi-off), pitchTickNum)
		if i0 < 0 {
			i0 = 0
		}
		if i1 > count {
			i1 = count
		}
		if i1 > i0 {
			dels = append(dels, [2]int64{i0, i1})
		}
	}
	if len(dels) == 0 {
		return []*cbor.Node{e}, 0
	}
	sort.Slice(dels, func(i, j int) bool { return dels[i][0] < dels[j][0] })
	merged := [][2]int64{dels[0]}
	for _, d := range dels[1:] {
		last := &merged[len(merged)-1]
		if d[0] <= last[1] {
			if d[1] > last[1] {
				last[1] = d[1]
			}
			continue
		}
		merged = append(merged, d)
	}

	var out []*cbor.Node
	total := int64(0)
	prev := int64(0)
	for _, d := range merged {
		total += d[1] - d[0]
		if d[0] > prev {
			out = append(out, resliceData(e, off, vals, prev, d[0]))
		}
		prev = d[1]
	}
	if prev < count {
		out = append(out, resliceData(e, off, vals, prev, count))
	}
	return out, int(total)
}

// resliceData 取 `values[s:e)` 做一条新 entry（其余键原样）；`offset` 按该段第 0 个样本的 tick 定
// （**向上取整**到整 tick，见文件头的说明）。
func resliceData(e *cbor.Node, off int64, vals *cbor.Node, s, e2 int64) *cbor.Node {
	items := make([]*cbor.Node, e2-s)
	copy(items, vals.Items[s:e2])
	out := mapPut(e, "offset", intNode(ceilTick(sampleTickF(off, s))))
	return mapPut(out, "values", &cbor.Node{Kind: cbor.KArray, Items: items})
}

// sampleTickF 返回 entry 内第 s 个样本的 tick（**可能不是整数**：栅格 15/16 tick/样本）。
func sampleTickF(off, s int64) float64 {
	return float64(off) + float64(pitchTickNum*s)/float64(pitchTickDen)
}

func ceilTick(f float64) int64 { return int64(math.Ceil(f - 1e-9)) }

// toneLaneEdit 把新 lane 写成一条 edit（lane 不存在就造 `parameters.pitchDelta`）。
func toneLaneEdit(pat, lane, newLane *cbor.Node) (cbor.Edit, error) {
	if lane != nil {
		return cbor.Edit{Start: lane.Start, End: lane.End, Bytes: cbor.EncodeNode(newLane),
			What: "音高线 lane：清区间（锚点 + 曲线样本）+ 加声调锚点"}, nil
	}
	params := pat.Get("parameters")
	if params != nil && params.Kind == cbor.KMap {
		return cbor.Edit{Start: params.Start, End: params.End,
			Bytes: cbor.EncodeNode(mapPut(params, "pitchDelta", newLane)),
			What:  "parameters：新建 pitchDelta lane（声调锚点）"}, nil
	}
	if params != nil {
		return cbor.Edit{}, fmt.Errorf("片段的 `parameters` 不是 map（%v）—— 不敢动", params.Kind)
	}
	inner := &cbor.Node{Kind: cbor.KMap, Keys: []*cbor.Node{{Kind: cbor.KText, Text: "pitchDelta"}}, Values: []*cbor.Node{newLane}}
	return cbor.Edit{Start: pat.Start, End: pat.End, Bytes: cbor.EncodeNode(mapPut(pat, "parameters", inner)),
		What: "pattern：新建 parameters.pitchDelta（声调锚点）"}, nil
}

// tonePoints 按调型算锚点（tick, **绝对音高**）—— 调型表照搬 SV1「语调教.js」，音分 ÷100。
func tonePoints(it toneItem) ([]any, []float64) {
	pos, end := it.pos, it.pos+it.dur
	p := it.pitch
	var ticks []int64
	var vals []float64
	add := func(t int64, v float64) {
		if t < pos {
			t = pos
		}
		if len(ticks) > 0 {
			last := ticks[len(ticks)-1]
			if t == last {
				vals[len(vals)-1] = v // 同 tick：后写的赢（短音符上点会撞）
				return
			}
			if t < last {
				return // 保证**升序**（技能 §3.3 的坑：evalPoints 假定升序）
			}
		}
		ticks = append(ticks, t)
		vals = append(vals, v)
	}
	switch it.tone {
	case '1': // 阴平：平
		add(pos, p)
		add(end, p)
	case '2': // 阳平：先降 −4，再升回（低平段 = 15% —— 用户 2026-10-10 定的比例）
		add(pos, p)
		add(pos+1, p-4)
		add(pos+int64(math.Round(0.15*float64(it.dur))), p-4)
		add(end, p)
	case '3': // 上声：先降 −8，再升回（低平段 = 50% —— 用户 2026-10-10 定的比例）
		add(pos, p)
		add(pos+1, p-8)
		add(pos+int64(math.Round(0.5*float64(it.dur))), p-8)
		add(end, p)
	case '4': // 去声：end−1 处 −12
		add(pos, p)
		add(end-1, p-12)
		add(end, p)
	}
	pts := make([]any, 0, len(ticks)*2)
	vuv := make([]float64, 0, len(ticks))
	for i, t := range ticks {
		pts = append(pts, t, vals[i])
		vuv = append(vuv, 1) // 有声（技能 §2.2②：pointsVUV 每点一个 0/1；手写值 ACE 逐值保留）
	}
	return pts, vuv
}

// ---------- 形状自检 ----------

// toneShapeCheck 在**写之前**、对**新 payload** 核对：
// ① 每个目标区间里没有旧锚点、没有曲线样本；② 每个音符都能找到点集与打算写的一模一样的 anchor entry。
func toneShapeCheck(newPay []byte, byPat map[*cbor.Node][]toneItem) ([]string, error) {
	nroot, err := cbor.Decode(newPay)
	if err != nil {
		return nil, err
	}
	checked := 0
	for pat, items := range byPat {
		name := clipNameOf(pat)
		nPat, err := patternOfUUID(nroot, items[0].uuid)
		if err != nil {
			return nil, err
		}
		lane := laneOf(nPat, "pitchDelta")
		if lane == nil {
			return nil, fmt.Errorf("片段「%s」改完居然没有 pitchDelta lane 了", name)
		}
		ranges := make([]toneRange, 0, len(items))
		for _, it := range items {
			ranges = append(ranges, toneRange{lo: it.pos, hi: it.pos + it.dur})
		}
		// 先认领"我们刚写进去的那几条 entry"—— 它们的点当然在区间里（那正是目的）
		mine := map[*cbor.Node]bool{}
		for _, it := range items {
			wantPts, wantVUV := tonePoints(it)
			for _, e := range lane.Items {
				if nodeText(e.Get("type")) == "anchor" && samePoints(e, wantPts, wantVUV) {
					mine[e] = true
				}
			}
		}
		// ① 区间里不许再有**别人的**东西
		for _, e := range lane.Items {
			if mine[e] {
				continue
			}
			switch nodeText(e.Get("type")) {
			case "anchor":
				pn := e.Get("points")
				if pn == nil || pn.Kind != cbor.KArray {
					continue
				}
				for k := 0; k+1 < len(pn.Items); k += 2 {
					if t, ok := nodeInt(pn.Items[k]); ok && inRanges(t, ranges) {
						return nil, fmt.Errorf("片段「%s」：第 %s 拍处还有锚点没清掉", name, beatLabel(t))
					}
				}
			case "data":
				if left, err := dataSamplesInRanges(e, ranges); err != nil {
					return nil, err
				} else if left > 0 {
					return nil, fmt.Errorf("片段「%s」：区间里还剩 %d 个曲线样本没删掉", name, left)
				}
			}
		}
		// ② 我们打算写的锚点，一条不少地在新 lane 里
		for _, it := range items {
			wantPts, wantVUV := tonePoints(it)
			found := false
			for _, e := range lane.Items {
				if nodeText(e.Get("type")) == "anchor" && samePoints(e, wantPts, wantVUV) {
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("片段「%s」：第 %s–%s 拍的声调锚点（%s）没写进去或写歪了",
					name, beatLabel(it.pos), beatLabel(it.pos+it.dur), it.label)
			}
			checked++
		}
	}
	return []string{fmt.Sprintf("形状自检通过：%d 个音符的区间都干净、锚点都在", checked)}, nil
}

// dataSamplesInRanges 数一条 `data` entry 里"按同一套栅格算法仍落在区间内"的样本数（与删除口径一致）。
func dataSamplesInRanges(e *cbor.Node, ranges []toneRange) (int, error) {
	vals := e.Get("values")
	if vals == nil || vals.Kind != cbor.KArray {
		return 0, nil
	}
	off, ok := nodeInt(e.Get("offset"))
	if !ok {
		return 0, fmt.Errorf("有条 data entry 的 offset 读不出来")
	}
	for _, r := range ranges {
		i0 := ceilDiv(pitchTickDen*(r.lo-off), pitchTickNum)
		i1 := ceilDiv(pitchTickDen*(r.hi-off), pitchTickNum)
		if i0 < 0 {
			i0 = 0
		}
		if i1 > int64(len(vals.Items)) {
			i1 = int64(len(vals.Items))
		}
		if i1 > i0 {
			return int(i1 - i0), nil
		}
	}
	return 0, nil
}

func samePoints(e *cbor.Node, wantPts []any, wantVUV []float64) bool {
	pn := e.Get("points")
	if pn == nil || pn.Kind != cbor.KArray || len(pn.Items) != len(wantPts) {
		return false
	}
	for i := range wantPts {
		if i%2 == 0 {
			ai, ok1 := nodeInt(pn.Items[i])
			bi, ok2 := anyInt(wantPts[i])
			if !ok1 || !ok2 || ai != bi {
				return false
			}
			continue
		}
		af, ok1 := nodeFloat(pn.Items[i])
		bf, ok2 := anyFloat(wantPts[i])
		if !ok1 || !ok2 || math.Abs(af-bf) > 1e-9 {
			return false
		}
	}
	vn := e.Get("pointsVUV")
	if vn == nil || vn.Kind != cbor.KArray || len(vn.Items) != len(wantVUV) {
		return false
	}
	for i := range wantVUV {
		v, ok := nodeFloat(vn.Items[i])
		if !ok || math.Abs(v-wantVUV[i]) > 1e-9 {
			return false
		}
	}
	return true
}

// patternOfUUID 在（新）root 里按 uuid 找回音符所在的片段。
func patternOfUUID(root *cbor.Node, id string) (*cbor.Node, error) {
	pat, _, err := findNoteByUUID(root, id)
	if err != nil {
		return nil, err
	}
	return pat, nil
}

// ---------- 节点小工具（map / int / float） ----------

// mapPut 返回"把 key 设成 val"的那份 map（值语义重编码；map 头可能要 +1）。
func mapPut(n *cbor.Node, key string, val *cbor.Node) *cbor.Node {
	if n == nil || n.Kind != cbor.KMap {
		return n
	}
	keys := append([]*cbor.Node(nil), n.Keys...)
	vals := append([]*cbor.Node(nil), n.Values...)
	for i, k := range keys {
		if k.KeyString() == key {
			vals[i] = val
			return &cbor.Node{Kind: cbor.KMap, Keys: keys, Values: vals}
		}
	}
	keys = append(keys, &cbor.Node{Kind: cbor.KText, Text: key})
	vals = append(vals, val)
	return &cbor.Node{Kind: cbor.KMap, Keys: keys, Values: vals}
}

func intNode(v int64) *cbor.Node {
	if v >= 0 {
		return &cbor.Node{Kind: cbor.KUint, Uint: uint64(v)}
	}
	return &cbor.Node{Kind: cbor.KInt, Int: v}
}

func floatNode(v float64) *cbor.Node { return &cbor.Node{Kind: cbor.KFloat, Float: v} }

func anyInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int64:
		return x, true
	case uint64:
		return int64(x), true
	}
	return 0, false
}

func anyFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}
