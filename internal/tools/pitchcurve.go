package tools

// 音高线 → 锚点（文件路线）：把一段区间里的音高线拟合成锚点，写回同一条 lane。
//
// 为什么走文件：CLI 的 `vocalpitch write` 只收锚点、不收密集曲线；而把“已画出来的线”
// 拟合成锚点需要读到**每个 tick 的音高**，只有 `.acep` 里有（`pitchDelta` 的 data/anchor）。
//
// 语义：区间内的锚点先清空，再按拟合结果**逐音符分段**写回（一段一个 entry，
// 和 ACE 自己画锚点的分段方式一致；合并成一条会把它们连成一条连续曲线）。

import (
	"os"
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"acetool/internal/acep"
	"acetool/internal/cbor"
	"acetool/internal/curve"
)

type pitchAnchorReport struct {
	DryRun  bool     `json:"干跑"`
	Path    string   `json:"工程文件"`
	Clip    string   `json:"片段"`
	Range   string   `json:"区间"`
	Source  string   `json:"曲线来源"`
	Samples int      `json:"采样点"`
	Anchors int      `json:"锚点数"`
	Entries int      `json:"写回分段"`
	MaxErr  float64  `json:"最大误差(半音)"`
	UsedTol float64  `json:"实际容差(半音)"`
	Notes   int      `json:"覆盖音符"`
	Backup  string   `json:"备份,omitempty"`
	Self    []string `json:"自检,omitempty"`
	Hint    string   `json:"提示,omitempty"`
}

func pitchToAnchorsTool() *Tool {
	return &Tool{
		ID: "pitch.toanchors", Category: "工程", Kind: KindWrite,
		Title: "音高线转锚点（文件路线）",
		Desc: "把区间里的**音高线**拟合成**锚点**写回同一片段：读 `pitchDelta` 的密集样本（data）" +
			"与锚点（anchor），按误差上限简化成锚点，再按音符分段写回。" +
			"⚠️ 改工程文件（先备份到临时目录），执行前先「保存工程」。",
		Params: []Param{
			textParam("path", "工程文件（.acep）", "", `C:\…\名字\名字.acep`, "← 页面上一步「保存工程」会给出来"),
			textParam("clipUuid", "片段 uuid（留空 = 当前片段）", "", "{…}", "留空时用「当前片段」定位"),
			numParam("beginBar", "开始小节", "1"),
			numParam("endBar", "结束小节", "1"),
			numParam("tol", "误差上限（半音，0.05 = 5 音分）", "0.05"),
			numParam("maxAnchors", "锚点数量上限（0 = 不限；超过则自动放宽容差）", "0"),
			checkParam("extremes", "保留颤音峰谷", true),
			numParam("dryRun", "干跑（1 = 只算不写）", "1"),
		},
		Plan:  pitchToAnchorsPlan,
		Apply: pitchToAnchorsApply,
		Source: "技能 acep §2.2 / 内部 curve 包（垂直 RDP + 突起量极值）",
	}
}

type pitchAnchorPlan struct {
	cont     *acep.Container
	raw      []byte
	rep      *pitchAnchorReport
	edits    []cbor.Edit
	newPay   []byte
	backup   string
}

func pitchToAnchorsPlan(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	plan, err := buildPitchAnchorPlan(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	plan.rep.DryRun = true
	if plan.newPay == nil {
		plan.rep.Hint = "没算出要写的东西 —— 一个字节都不会改。"
		return plan.rep, nil
	}
	plan.rep.Hint = "**一个字节都没写**（只要干跑）。要改就取消干跑再执行。"
	return plan.rep, nil
}

func pitchToAnchorsApply(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	plan, err := buildPitchAnchorPlan(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	plan.rep.DryRun = false
	if plan.newPay == nil {
		plan.rep.Hint = "没算出要写的东西 —— 一个字节都没改。"
		return plan.rep, nil
	}
	out, err := plan.cont.Repack(plan.newPay)
	if err != nil {
		return nil, err
	}
	backup, err := writeBackup(plan.rep.Path, plan.raw)
	if err != nil {
		return nil, fmt.Errorf("备份失败，不写工程文件：%w", err)
	}
	plan.rep.Backup = backup
	tmp := plan.rep.Path + ".acetool-tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, plan.rep.Path); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	plan.rep.Self = append(plan.rep.Self, "已原地改写工程文件（备份见上）")
	return plan.rep, nil
}

func buildPitchAnchorPlan(ctx context.Context, rd ReadFunc, vals map[string]string) (*pitchAnchorPlan, error) {
	path := strings.TrimSpace(value(vals, "path", ""))
	if path == "" {
		return nil, fmt.Errorf("要一个工程文件路径（path）")
	}
	begin, err := parseMeasure(vals, "beginBar", "开始小节")
	if err != nil {
		return nil, err
	}
	end, err := parseMeasure(vals, "endBar", "结束小节")
	if err != nil {
		return nil, err
	}
	if end < begin {
		return nil, fmt.Errorf("结束小节（%g）不能小于开始小节（%g）", end, begin)
	}
	lo, err := measureToTick(ctx, rd, begin, "开始小节")
	if err != nil {
		return nil, err
	}
	hi, err := measureToTick(ctx, rd, end+1, "结束小节")
	if err != nil {
		return nil, err
	}
	tol := 0.05
	if v := strings.TrimSpace(value(vals, "tol", "")); v != "" {
		fmt.Sscan(v, &tol)
	}
	if tol <= 0 {
		tol = 0.05
	}
	keepExt := flagOn(vals, "extremes", true)

	cont, err := acep.Open(path)
	if err != nil {
		return nil, err
	}
	root, err := cbor.Decode(cont.Payload)
	if err != nil {
		return nil, err
	}
	clipUUID := strings.TrimSpace(value(vals, "clipUuid", ""))
	if clipUUID == "" {
		sc, err := readNoteScope(ctx, rd, false)
		if err != nil {
			return nil, fmt.Errorf("定位当前片段失败（也可以直接给 clipUuid）：%w", err)
		}
		clipUUID = sc.ClipUUID
	}
	_, pat, err := findPatternByUUID(root, clipUUID)
	if err != nil {
		return nil, err
	}
	lane := laneOf(pat, "pitchDelta")
	if lane == nil {
		return nil, fmt.Errorf("这个片段没有 pitchDelta lane")
	}
	rep := &pitchAnchorReport{
		Path:  path,
		Range: fmt.Sprintf("第 %g–%g 小节（tick %d..%d）", begin, end, lo, hi),
	}
	notes := pat.Get("notes")

	// ① 采样：优先 data（delta），没有就用 anchor 插值
	dense := densePitchSamples(lane, lo, hi)
	if len(dense) > 0 {
		rep.Source = "密集样本（data）"
	} else {
		dense = anchorPitchCurve(lane, lo, hi)
		if len(dense) > 0 {
			rep.Source = "锚点插值（anchor）"
		}
	}
	if len(dense) == 0 {
		rep.Hint = "这段区间里既没有 data 样本、也没有可插值的锚点 —— 没有可转换的音高线。"
		return &pitchAnchorPlan{cont: cont, raw: cont.RawBytes(), rep: rep}, nil
	}
	rep.Samples = len(dense)

	// ② 简化成锚点
	forced := map[int]bool{}
	if notes != nil && notes.Kind == cbor.KArray {
		for _, n := range notes.Items {
			p, ok := nodeInt(n.Get("pos"))
			if !ok {
				continue
			}
			for i, s := range dense {
				if s.X >= float64(p) && s.X < float64(p)+480 { // 音符起点附近强制留点
					forced[i] = true
					break
				}
			}
		}
	}
	if keepExt {
		for i := range curve.KeepExtremes(dense, tol) {
			forced[i] = true
		}
	}
	maxAnchors := 0
	if v := strings.TrimSpace(value(vals, "maxAnchors", "")); v != "" {
		fmt.Sscan(v, &maxAnchors)
	}
	usedTol := tol
	anchors := curve.SimplifyWithForced(dense, usedTol, forced)
	if maxAnchors > 0 && len(anchors) > maxAnchors {
		// 简化程度：锚点太多就逐步放大容差（二分），直到不超上限或容差大到没意义。
		loT, hiT := usedTol, usedTol
		for i := 0; i < 24; i++ {
			hiT *= 2
			a := curve.SimplifyWithForced(dense, hiT, forced)
			if len(a) <= maxAnchors {
				break
			}
			loT = hiT
		}
		for i := 0; i < 20; i++ {
			mid := (loT + hiT) / 2
			a := curve.SimplifyWithForced(dense, mid, forced)
			if len(a) <= maxAnchors {
				hiT = mid
				anchors = a
			} else {
				loT = mid
			}
		}
		usedTol = hiT
	}
	rep.UsedTol = usedTol
	rep.Anchors = len(anchors)

	// ③ 按音符分段
	segments := splitAnchorsByNotes(anchors, notes, lo, hi)
	rep.Entries = len(segments)
	covered := 0
	for _, seg := range segments {
		if len(seg) >= 2 {
			covered++
		}
	}
	rep.Notes = covered
	rep.MaxErr = maxAnchorsErr(dense, anchors, usedTol)

	// ④ 覆盖写：剔除区间内的旧锚点与旧 data 样本，再写新锚点
	var kept []*cbor.Node
	for _, e := range lane.Items {
		t := nodeText(e.Get("type"))
		switch t {
		case "anchor":
			items := pointItems(e)
			vn := e.Get("pointsVUV")
			var pts []any
			var vuv []float64
			for i := 0; i+1 < len(items); i += 2 {
				tk, ok := nodeInt(items[i])
				if !ok {
					pts = append(pts, items[i], items[i+1])
					continue
				}
				if tk >= lo && tk < hi {
					continue
				}
				pts = append(pts, items[i], items[i+1])
				var v float64
				if vn != nil && vn.Kind == cbor.KArray && len(vn.Items) > i/2 {
					v = vn.Items[i/2].Float
				}
				vuv = append(vuv, v)
			}
			if len(pts) == 0 {
				continue
			}
			if n := encodeAnchor(pts, vuv, vn != nil); n != nil {
				kept = append(kept, n)
			}
		case "data":
			// 这段的样本被锚点取代 ⇒ 整条 data entry 若落在区间内就丢掉，否则原样留（区间外样本不动）
			if off, ok := nodeInt(e.Get("offset")); ok {
				n := len(pointItems(e))
				start := float64(off)
				endTick := start + float64(n)*15/16
				if start >= float64(lo) && endTick <= float64(hi) {
					continue
				}
			}
			kept = append(kept, e)
		default:
			kept = append(kept, e)
		}
	}
	for _, seg := range segments {
		if len(seg) < 2 {
			continue
		}
		pts := make([]any, 0, len(seg)*2)
		vuv := make([]float64, 0, len(seg))
		for _, p := range seg {
			pts = append(pts, int64(math.Round(p.X)), p.Y)
			vuv = append(vuv, 1)
		}
		if n := encodeAnchor(pts, vuv, true); n != nil {
			kept = append(kept, n)
		}
	}
	all := kept
	sort.SliceStable(all, func(i, j int) bool { return firstTickOf(all[i]) < firstTickOf(all[j]) })
	var body []byte
	for _, n := range all {
		body = append(body, cbor.EncodeNode(n)...)
	}
	newLane := append(cbor.EncodeArrayHeader(uint64(len(all))), body...)
	newPayload := make([]byte, 0, len(cont.Payload)-(lane.End-lane.Start)+len(newLane))
	newPayload = append(newPayload, cont.Payload[:lane.Start]...)
	newPayload = append(newPayload, newLane...)
	newPayload = append(newPayload, cont.Payload[lane.End:]...)

	if _, err := cbor.Decode(newPayload); err != nil {
		return nil, fmt.Errorf("新 payload 解不开：%w", err)
	}
	return &pitchAnchorPlan{cont: cont, raw: cont.RawBytes(), rep: rep, newPay: newPayload}, nil
}

// densePitchSamples 读出区间内的 data 样本（值是 delta 半音，需要音符音高才能还原成绝对音高）。
func densePitchSamples(lane *cbor.Node, lo, hi int64) []curve.Pt {
	var out []curve.Pt
	for _, e := range lane.Items {
		if nodeText(e.Get("type")) != "data" {
			continue
		}
		off, ok := nodeInt(e.Get("offset"))
		if !ok {
			continue
		}
		vals := pointItems(e)
		for i, v := range vals {
			t := float64(off) + float64(i)*15/16
			if t < float64(lo) || t >= float64(hi) {
				continue
			}
			if v.Kind != cbor.KFloat && v.Kind != cbor.KInt {
				continue
			}
			out = append(out, curve.Pt{X: t, Y: v.Float})
		}
	}
	return out
}

// anchorPitchCurve 把区间内的锚点插值成逐样本曲线（锚点值是绝对音高）。
func anchorPitchCurve(lane *cbor.Node, lo, hi int64) []curve.Pt {
	var pts []curve.Pt
	for _, e := range lane.Items {
		if nodeText(e.Get("type")) != "anchor" {
			continue
		}
		items := pointItems(e)
		for i := 0; i+1 < len(items); i += 2 {
			t, ok := nodeInt(items[i])
			if !ok {
				continue
			}
			pts = append(pts, curve.Pt{X: float64(t), Y: items[i+1].Float})
		}
	}
	if len(pts) < 2 {
		return nil
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].X < pts[j].X })
	var out []curve.Pt
	step := 480.0 / 64 // 每 tick 的 1/64 拍采样，够密
	for x := float64(lo); x < float64(hi); x += step {
		y := interpCurve(pts, x)
		out = append(out, curve.Pt{X: x, Y: y})
	}
	return out
}

func interpCurve(pts []curve.Pt, x float64) float64 {
	if x <= pts[0].X {
		return pts[0].Y
	}
	if x >= pts[len(pts)-1].X {
		return pts[len(pts)-1].Y
	}
	i := sort.Search(len(pts), func(k int) bool { return pts[k].X >= x })
	if i <= 0 {
		return pts[0].Y
	}
	a, b := pts[i-1], pts[i]
	if b.X == a.X {
		return b.Y
	}
	return a.Y + (b.Y-a.Y)*(x-a.X)/(b.X-a.X)
}

// splitAnchorsByNotes 把锚点按它落在哪个音符里分段（一个音符一段）。
func splitAnchorsByNotes(pts []curve.Pt, notes *cbor.Node, lo, hi int64) [][]curve.Pt {
	type note struct{ pos, end int64 }
	var ns []note
	if notes != nil && notes.Kind == cbor.KArray {
		for _, n := range notes.Items {
			p, ok1 := nodeInt(n.Get("pos"))
			d, ok2 := nodeInt(n.Get("dur"))
			if !ok1 || !ok2 {
				continue
			}
			ns = append(ns, note{p, p + d})
		}
	}
	sort.Slice(ns, func(i, j int) bool { return ns[i].pos < ns[j].pos })
	var segs [][]curve.Pt
	for _, nt := range ns {
		if nt.end <= lo || nt.pos >= hi {
			continue
		}
		var seg []curve.Pt
		for _, p := range pts {
			if p.X >= float64(nt.pos) && p.X < float64(nt.end) {
				seg = append(seg, p)
			}
		}
		if len(seg) > 0 {
			segs = append(segs, seg)
		}
	}
	return segs
}

func maxAnchorsErr(dense []curve.Pt, anchors []curve.Pt, tol float64) float64 {
	if len(anchors) < 2 {
		return 0
	}
	worst := 0.0
	j := 0
	for _, p := range dense {
		for j+1 < len(anchors) && anchors[j+1].X <= p.X {
			j++
		}
		var y float64
		if j+1 < len(anchors) {
			a, b := anchors[j], anchors[j+1]
			if b.X == a.X {
				y = b.Y
			} else {
				y = a.Y + (b.Y-a.Y)*(p.X-a.X)/(b.X-a.X)
			}
		} else {
			y = anchors[len(anchors)-1].Y
		}
		if d := math.Abs(p.Y - y); d > worst {
			worst = d
		}
	}
	return worst
}

func firstTickOf(n *cbor.Node) float64 {
	for _, t := range ticksOf(n) {
		return float64(t)
	}
	return 1e18
}

// pointItems 取 entry 的 points 数组元素（没有就返回 nil）。
func pointItems(e *cbor.Node) []*cbor.Node {
	pn := e.Get("points")
	if pn == nil || pn.Kind != cbor.KArray {
		return nil
	}
	return pn.Items
}

// ticksOf 取 anchor entry 的点里所有 tick（偶数位）。
func ticksOf(e *cbor.Node) []int64 {
	items := pointItems(e)
	var out []int64
	for i := 0; i+1 < len(items); i += 2 {
		if t, ok := nodeInt(items[i]); ok {
			out = append(out, t)
		}
	}
	return out
}

func init() { register(pitchToAnchorsTool()) }
