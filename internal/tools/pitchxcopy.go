package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"acetool/internal/cbor"
)

// 跨轨道复制**音高线**：与 `pitch.within` 同一套三类内容（曲线 / 锚点 / 音符颤音），
// 只是"源片段"与"目标片段"是**两条轨**上的两个片段。
//
// 口径（照 `xcopy.notes` 那条：**按绝对时间配对，不新建音符**）：
//
//	· 源窗口与目标窗口取**同一段工程 tick**（跨轨复制没有"错位搬"这回事）⇒ 绝对 delta = 0；
//	· 两边的 pattern 局部坐标各自换算：`local = abs - pattern.pos`（技能 acep §2.2：
//	  lane 里的 tick 是**片段内局部**坐标）；
//	· 曲线：源窗口里的样本写进目标窗口（目标有覆盖就原位覆盖，没有就追加 faithful shift 的 entry）；
//	· 锚点：源窗口里的点平移后追加成一条新 anchor entry；
//	· 音符颤音：源窗口里的音符按**相对位置**配到目标窗口里的音符（目标处没有音符 ⇒ 如实报跳过）。
func init() {
	register(&Tool{
		ID: "pitch.xcopy", Category: "工程",
		Title: "跨轨道复制音高线（文件路线）",
		Desc: "把**源轨**一段小节里的音高线复制到**目标轨**的同一段时间上：曲线 / 锚点 / 音符颤音三类一起搬。" +
			"⚠️ 与「轨内复制音高线」同一条文件路线（CLI 上 `pitch` 恒 available:false）：" +
			"执行会**原地**替换工程文件，原文件先备份到 `%TEMP%\\ACETool\\acep-backup\\`。" +
			"纪律：先「保存工程」→ 执行 → 让 ACE 重新打开。",
		Kind:   KindWrite,
		Source: "技能 acep v1.2.3 §2.2（三种画法）/ §4（安全写入六步）；配对口径照 xcopy.notes",
		Params: []Param{
			textParam("path", "工程文件（.acep）", "", `C:\…\名字\名字.acep`, "← 页面上一步「保存工程」会给出来"),
			numParam("srcTrackIndex", "源轨（0 起）", "0"),
			numParam("dstTrackIndex", "目标轨（0 起）", "0"),
			numParam("srcBeginBar", "源：开始小节", "1"),
			numParam("srcEndBar", "源：结束小节", "1"),
			checkParam("line", "① 曲线", true),
			checkParam("anchor", "② 锚点", true),
			checkParam("vibrato", "③ 音符颤音", true),
		},
		Plan:  pitchXcopyPlan,
		Apply: pitchXcopyApply,
	})
}

func pitchXcopyPlan(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	plan, err := buildXcopyPlan(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	if plan.newPay == nil {
		plan.rep.Hint = "没算出要写的东西 —— 一个字节都不会改。"
		return plan.rep, nil
	}
	plan.rep.Hint = "**一个字节都没写**（这是" + "计划" + "那一步）。要改请调 `pitch.xcopy` 的执行（页面第 ② 步）。"
	return plan.rep, nil
}

func pitchXcopyApply(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	plan, err := buildXcopyPlan(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	return applyPitchPlan(plan)
}

// buildXcopyPlan 解析"源轨片段 / 目标轨片段"两侧，然后把三类内容的改动算出来。
func buildXcopyPlan(ctx context.Context, rd ReadFunc, vals map[string]string) (*pitchPlan, error) {
	p := strings.Trim(strings.TrimSpace(value(vals, "path", "")), `"`)
	if p == "" {
		return nil, fmt.Errorf("要一个工程文件路径（path）—— 页面那一步「保存工程」会把它给你")
	}
	cont, root, err := openACEPRoot(p)
	if err != nil {
		return nil, err
	}

	// 两条轨（按**绝对 tick 窗口**配同一段时间）
	srcT, err := parseIntParam(vals, "srcTrackIndex", "源轨")
	if err != nil {
		return nil, err
	}
	dstT, err := parseIntParam(vals, "dstTrackIndex", "目标轨")
	if err != nil {
		return nil, err
	}
	if srcT == dstT {
		return nil, fmt.Errorf("源轨与目标轨是同一条（%d）—— 同轨请用「轨道内复制音高线」", srcT)
	}
	srcB, err := parseMeasure(vals, "srcBeginBar", "源开始小节")
	if err != nil {
		return nil, err
	}
	srcE, err := parseMeasure(vals, "srcEndBar", "源结束小节")
	if err != nil {
		return nil, err
	}
	if srcE < srcB {
		return nil, fmt.Errorf("源结束小节（%g）不能小于源开始小节（%g）", srcE, srcB)
	}
	loAbs, err := measureToTick(ctx, rd, srcB, "源开始小节")
	if err != nil {
		return nil, err
	}
	hiAbs, err := measureToTick(ctx, rd, srcE+1, "源结束小节")
	if err != nil {
		return nil, err
	}

	// 两侧各挑"与这段窗口有交叠"的片段（多条时如实报出来，只动第一条 —— 不猜）
	srcPats, err := patternsOnTrack(root, srcT)
	if err != nil {
		return nil, err
	}
	dstPats, err := patternsOnTrack(root, dstT)
	if err != nil {
		return nil, err
	}
	srcPat, srcNote := pickPatternForWindow(srcPats, loAbs, hiAbs, "源轨")
	dstPat, dstNote := pickPatternForWindow(dstPats, loAbs, hiAbs, "目标轨")
	if srcPat == nil || dstPat == nil {
		return nil, fmt.Errorf("源轨 %s；目标轨 %s", srcNote, dstNote)
	}

	srcPos, _ := nodeInt(srcPat.Get("pos"))
	dstPos, _ := nodeInt(dstPat.Get("pos"))
	// 各自换成**局部**坐标（lane/notes 的 tick 都是片段内局部）
	sLo, sHi := loAbs-srcPos, hiAbs-srcPos
	dLo, dHi := loAbs-dstPos, hiAbs-dstPos
	delta := dLo - sLo // = srcPos - dstPos（绝对时间不变）

	rep := &pitchCopyReport{
		Path:  p,
		Track: fmt.Sprintf("源轨 %d → 目标轨 %d", srcT, dstT),
		Clip:  fmt.Sprintf("源「%s」→ 目标「%s」", clipNameOf(srcPat), clipNameOf(dstPat)),
		Src:   fmt.Sprintf("轨 %d 第 %g–%g 小节（工程 tick %d..%d）= 片段内 %d..%d", srcT, srcB, srcE, loAbs, hiAbs, sLo, sHi),
		Dst:   fmt.Sprintf("轨 %d 同一段（片段内 %d..%d）", dstT, dLo, dHi),
	}
	if srcNote != "" {
		rep.Hint = srcNote + "；" + dstNote
	}
	plan := &pitchPlan{rep: rep, cont: cont, raw: cont.RawBytes()}
	if err := fillPitchKindReports(plan, srcPat, dstPat, sLo, sHi, dLo, dHi, delta, vals); err != nil {
		return nil, err
	}
	return finishPitchPlan(plan)
}

// patternsOnTrack 取某条轨上的片段（按 `tracks[i]` 的**文件顺序**索引；
// ⚠️ 文件里 tracks 恒 100 个槽位、空槽也在 ⇒ 先用 type 过滤，再按"非空轨"的序号数）。
func patternsOnTrack(root *cbor.Node, trackIndex int) ([]*cbor.Node, error) {
	tracks := root.Get("tracks")
	if tracks == nil || tracks.Kind != cbor.KArray {
		return nil, fmt.Errorf("payload 里没有 tracks")
	}
	real := 0
	for _, tr := range tracks.Items {
		typ := nodeText(tr.Get("type"))
		if typ == "" || typ == "empty" || typ == "data" {
			continue // 占位槽
		}
		if real == trackIndex {
			pats := tr.Get("patterns")
			if pats == nil || pats.Kind != cbor.KArray || len(pats.Items) == 0 {
				return nil, fmt.Errorf("轨 %d（「%s」）上没有片段", trackIndex, nodeText(tr.Get("name")))
			}
			return pats.Items, nil
		}
		real++
	}
	return nil, fmt.Errorf("工程文件里没有第 %d 条轨（真轨只有 %d 条）", trackIndex, real)
}

// pickPatternForWindow 挑与这段绝对 tick 窗口有交叠的片段（多条时挑**最靠前**那条并如实提示）。
func pickPatternForWindow(pats []*cbor.Node, lo, hi int64, who string) (*cbor.Node, string) {
	type cand struct {
		pat  *cbor.Node
		over int64
	}
	var cs []cand
	for _, pat := range pats {
		pos, _ := nodeInt(pat.Get("pos"))
		dur, _ := nodeInt(pat.Get("dur"))
		overlap := min64(hi, pos+dur) - max64(lo, pos)
		if overlap > 0 {
			cs = append(cs, cand{pat: pat, over: overlap})
		}
	}
	if len(cs) == 0 {
		return nil, fmt.Sprintf("%s在这段窗口上没有片段（窗口 tick %d..%d）", who, lo, hi)
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].over > cs[j].over })
	if len(cs) > 1 {
		return cs[0].pat, fmt.Sprintf("%s有 %d 个片段与窗口交叠（动了交叠最多的那个，别的没动）", who, len(cs))
	}
	return cs[0].pat, ""
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func parseIntParam(vals map[string]string, name, label string) (int, error) {
	s := strings.TrimSpace(value(vals, name, ""))
	if s == "" {
		return 0, fmt.Errorf("要给一个「%s」", label)
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, fmt.Errorf("「%s」要是整数，实得 %q", label, s)
	}
	return n, nil
}
