package tools

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// 「轨内复制」的真正作用域（用户 2026-10-07 定死）：
//
//	**`xcopy.within` 作用于轨内所有 clip，遍历绝对时间。**
//
// 所以这里读的是**一整条轨**：它的每个片段 + 每个片段里的音符，
// 而且音符的 `pos` 一律换成 **工程绝对 tick**（`clipBegin + pos`）——
// 三个小节号换算出来的也是绝对 tick ⇒ 配对、报告、参数窗口全走一套绝对坐标，
// **不再**"减掉 clipBegin 换到片段坐标"（旧版那样做，作用域只有编辑器里那一个片段）。
//
// 为什么要这么改：一条轨常常由好几个片段拼成（一句一个片段很常见），
// 只在"当前片段"里按小节搬，跨片段的那一段就**静默搬不到** —— 用户看不到任何提示。
//
// 三个副作用（都是想要的）：
//
//	① 片段类型（sing / 非 sing）**逐音符看它所在的片段**，不再拿"当前片段"当整条轨的类型；
//	② 参数曲线按**绝对窗口**读成一条缓冲，再按目标片段切段写（见 planTrackParams）；
//	③ 报告里的 tick 全是工程绝对 tick（页面画框用的就是它）。
type trackClip struct {
	clipBrief
	NoteCount int
}

type xcopyTrackScope struct {
	TrackIndex int
	TrackName  string
	Clips      []trackClip
	Notes      []xcopyNote    // `Pos` 已经是**工程绝对** tick
	NoteClip   map[string]int // noteUUID → Clips 下标
}

// readTrackScope 读齐"当前这条轨的全部片段与音符"。
//
// 当前轨的判据与 `readNoteScope` 同一套：`editor status` 优先，回退**标记线**所在的轨
// （编辑器没开着时也能用；这也是旧版那句话的判据）。
func readTrackScope(ctx context.Context, rd ReadFunc) (*xcopyTrackScope, error) {
	trackIdx := -1
	located := ""
	var es struct {
		IsAvailable bool `json:"isAvailable"`
		TrackIndex  int  `json:"trackIndex"`
	}
	if err := readJSON(ctx, rd, &es, "editor", "status"); err == nil && es.IsAvailable {
		trackIdx, located = es.TrackIndex, "editor status（编辑器当前那条轨）"
	}
	if trackIdx < 0 {
		var caret struct {
			TrackIndex int `json:"trackIndex"`
		}
		if err := readJSON(ctx, rd, &caret, "caret", "get", "--scope", "arrangement"); err != nil {
			return nil, fmt.Errorf("定位当前轨道：编辑器没开着，`caret get` 也读不到（%w）", err)
		}
		trackIdx, located = caret.TrackIndex, "标记线所在的轨（编辑器没开着）"
	}

	sc := &xcopyTrackScope{TrackIndex: trackIdx, NoteClip: map[string]int{}}
	if tracks, terr := readTracks(ctx, rd); terr == nil {
		sc.TrackName = trackNameOf(tracks, trackIdx)
	}
	if sc.TrackName == "" {
		sc.TrackName = fmt.Sprintf("轨道 %d", trackIdx)
	}

	clips, err := clipListBrief(ctx, rd, trackIdx)
	if err != nil {
		return nil, err
	}
	for _, c := range clips {
		if c.End <= c.Begin {
			continue // 空片段：盖不住任何 tick，参数/音符都不可能有
		}
		sc.Clips = append(sc.Clips, trackClip{clipBrief: c})
	}
	if len(sc.Clips) == 0 {
		return nil, fmt.Errorf("轨「%s」上一个有内容的片段都没有（%s）", sc.TrackName, located)
	}
	for i := range sc.Clips {
		c := sc.Clips[i]
		nc, nerr := readClipNotes(ctx, rd, trackIdx, c.clipBrief)
		if nerr != nil {
			return nil, nerr
		}
		for _, n := range nc.Notes {
			n.Pos += c.Begin // ⭐ 片段内 → **工程绝对**（"遍历绝对时间"）
			sc.Notes = append(sc.Notes, n)
			sc.NoteClip[n.NoteUUID] = i
		}
		sc.Clips[i].NoteCount = len(nc.Notes)
	}
	return sc, nil
}

// ---------- 绝对时间上的几何 ----------

func minI64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// clipsCovering 列出与绝对窗口 [lo,hi) 有交集的片段（按起点升序，clipListBrief 已排过）。
func clipsCovering(sc *xcopyTrackScope, lo, hi int64) []trackClip {
	var out []trackClip
	for _, c := range sc.Clips {
		if c.End <= lo || c.Begin >= hi {
			continue
		}
		out = append(out, c)
	}
	return out
}

// clipCovering 找盖住某个**绝对** tick 的片段。
func clipCovering(sc *xcopyTrackScope, tick int64) (trackClip, bool) {
	for _, c := range sc.Clips {
		if tick >= c.Begin && tick < c.End {
			return c, true
		}
	}
	return trackClip{}, false
}

// clipOfNote 给出某个音符所在的片段（报告/类型判定要用）。
func clipOfNote(sc *xcopyTrackScope, noteUUID string) (trackClip, bool) {
	i, ok := sc.NoteClip[noteUUID]
	if !ok || i < 0 || i >= len(sc.Clips) {
		return trackClip{}, false
	}
	return sc.Clips[i], true
}

// isSingClip 判断片段是不是"能唱歌词/音素"的那种。
// ⚠️ 旧版一处写 `== "sing"`、另一处写 `Contains(..., "sing")` —— 这里统一成一个判据（用 Contains，
// 免得引擎把类型写成 `sing-xxx` 之类时两边判断不一致）。
func isSingClip(t string) bool {
	return strings.Contains(strings.ToLower(t), "sing")
}

// clipSpanText 报告里说清"这段窗口落在几个片段上"（多片段是这一版的重点，必须写出来）。
func clipSpanText(n int) string {
	switch {
	case n <= 0:
		return "没有片段"
	case n == 1:
		return "1 个片段"
	default:
		return fmt.Sprintf("**跨 %d 个片段**", n)
	}
}

// clipTypeAt 给某个**绝对** tick 所处的片段类型（报告里的"源/目标类型"用它；没有片段就给空串）。
func clipTypeAt(sc *xcopyTrackScope, tick int64) string {
	if c, ok := clipCovering(sc, tick); ok {
		return c.Type
	}
	return ""
}

// ---------- 参数那一半：多片段（绝对窗口 → 逐目标片段切段写） ----------

// planTrackParams 把"源绝对窗口"的参数曲线搬到"目标绝对窗口"，作用于**一条轨的所有片段**。
//
// 与单片段那支（`planClipParams`）的关系：**判定共用**（`paramJobsOf` 决定"搬哪些格、源读哪层、
// 写目标哪层"），差别只在"怎么把绝对窗口落到片段上"：
//
//	① 源：把源窗口按**源片段**切成若干段读进来，拼成一条**按绝对 tick 索引**的缓冲；
//	   没有被任何源片段盖住的 tick 标成"结构空洞"；
//	② 目标：按**目标片段**切段写；`vocalparam write` 是"替换该层这一整段"⇒
//	   写之前把段落里的**结构空洞切出去**（空洞处不写 = 保留目标原值，不清空）；
//	③ 每落一段都要过三道守卫：源/目标的**引擎世代**、目标**这一段所在片段**有没有那个可写层、
//	   目标那一段的**指纹**（`--if-match`）。
//
// ⚠️ 与单片段那支不同的一点（有意为之）：**"源上没有片段" 与 "源片段上这条 lane 没画过" 是两件事** ——
// 前者不写（保留目标原值），后者照旧写 `null`（= 把"没画"如实搬过去，与旧行为一致）。
func planTrackParams(ctx context.Context, rd ReadFunc, vals map[string]string, sc *xcopyTrackScope,
	srcA, srcB, dstA int64, wantParams, wantDyn bool, pd *xcopyPlanData, rep *xcopyReport) error {

	span := srcB - srcA
	if span < 2 {
		pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
			"参数曲线要求跨度至少 2 个 tick（ACE 的 dense 写规则），这段只有 %d ⇒ 跳过", span))
		return nil
	}
	// ⭐ 用户 2026-10-23：「跨轨道的按值域换算是不是没用了」——确实是。
	// 真正执行的这条路径（planTrackParams）以前根本没读 map/crossGen（只有老的 planClipParams 读了）。
	// 现在接上：map=range ⇒ 源值域 → 目标值域线性换算；none ⇒ 原值照搬（只夹边界）。
	mapMode, merr := paramMapModeOf(vals)
	if merr != nil {
		return merr
	}
	srcClips := clipsCovering(sc, srcA, srcB)
	dstClips := clipsCovering(sc, dstA, dstA+span)
	if len(srcClips) == 0 {
		pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
			"参数曲线：源窗口（工程 tick %d..%d）上**一个片段都没有** ⇒ 参数那一半跳过", srcA, srcB))
		return nil
	}
	if len(dstClips) == 0 {
		pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
			"参数曲线：目标窗口（工程 tick %d..%d）上**一个片段都没有** ⇒ 参数那一半跳过", dstA, dstA+span))
		return nil
	}

	// 花名册**现读**（读数的层集合随运行期解析出的声源变）：
	// 源取覆盖源窗口的第一个片段；目标按**每个要写的片段**各读一份（层合法性只看目标）。
	roCache := map[string]*paramRoster{}
	roOf := func(clipUUID string) (*paramRoster, error) {
		if ro, ok := roCache[clipUUID]; ok {
			return ro, nil
		}
		ro, err := readParamRoster(ctx, rd, clipUUID)
		if err != nil {
			return nil, err
		}
		roCache[clipUUID] = ro
		return ro, nil
	}
	sro, err := roOf(srcClips[0].UUID)
	if err != nil {
		return err
	}
	dro, err := roOf(dstClips[0].UUID)
	if err != nil {
		return err
	}

	if wantParams {
		jobs, table, picks, skips, jerr := paramJobsOf(vals, sro, dro)
		if jerr != nil {
			return jerr
		}
		pd.paramSkips = append(pd.paramSkips, skips...)

		for _, jb := range jobs {
			if jb.EnvToAbs && rep != nil {
				rep.Note = strings.TrimSpace(rep.Note + "\n跨代「包络 → user」：v1 的包络是乘数 ⇒ 已按 baseline × 包络折算成绝对值写进目标的 override 层（v2 user）。")
			}
			p, pkerr := pickParam(sro, jb.Param)
			if pkerr != nil {
				pd.paramSkips = append(pd.paramSkips, fmt.Sprintf("%v ⇒ 跳过", pkerr))
				continue
			}
			// ① 源缓冲（绝对 tick：索引 = absTick - srcA）
			buf := make([]*float64, span)
			covered := make([]bool, span)
			srcFallback := ""
			for _, c := range srcClips {
				lo, hi := maxI64(c.Begin, srcA), minI64(c.End, srcB)
				if hi-lo < 1 {
					continue
				}
				srcLayer := jb.SrcLayer
				tRead, pts, rerr := readLayerRange(ctx, rd, c.UUID, jb.Param, srcLayer, lo-c.Begin, hi-c.Begin)
				_ = tRead
				if rerr != nil && srcLayer == "effective" {
					// 实参读不到 ⇒ 退回它自己的第一个可写层，并**如实记下**（不静默换源）
					if alt := writableAnyLayers(sro, jb.Param); len(alt) > 0 {
						if _, pts2, rerr2 := readLayerRange(ctx, rd, c.UUID, jb.Param, alt[0], lo-c.Begin, hi-c.Begin); rerr2 == nil {
							srcLayer, pts, rerr = alt[0], pts2, nil
							srcFallback = alt[0]
						}
					}
				}
				if rerr != nil {
					pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
						"参数 %s：源片段「%s」（tick %d..%d）上读不到（%v）⇒ 那一段不搬",
						jb.Param, c.Name, lo, hi, rerr))
					continue
				}
				if int64(len(pts)) != hi-lo {
					pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
						"参数 %s：源片段「%s」读到 %d 个 tick（要 %d 个）⇒ 那一段不搬（长度对不上不硬算）",
						jb.Param, c.Name, len(pts), hi-lo))
					continue
				}
				// ⭐ 跨代「包络 → user」：v1 包络是乘数、v2 user 是绝对值 ⇒ 先乘 baseline 折成绝对值
				if jb.EnvToAbs {
					base := readBaselineTicks(ctx, rd, c.UUID, jb.Param, lo-c.Begin, hi-c.Begin)
					for k := range pts {
						if pts[k] == nil {
							continue
						}
						b := 1.0
						if k < len(base) && base[k] != nil {
							b = *base[k]
						}
						v := (*pts[k]) * b
						pts[k] = &v
					}
				}
				copy(buf[lo-srcA:hi-srcA], pts)
				for k := lo - srcA; k < hi-srcA; k++ {
					covered[k] = true
				}
			}

			// ② 目标：逐片段、逐"连续有源"的子段写
			for _, c := range dstClips {
				lo, hi := maxI64(c.Begin, dstA), minI64(c.End, dstA+span)
				if hi-lo < 1 {
					continue
				}
				cro, rerr := roOf(c.UUID)
				if rerr != nil {
					pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
						"参数 %s：目标片段「%s」读不到花名册（%v）⇒ 那一段跳过", jb.Param, c.Name, rerr))
					continue
				}
				if cro.EngineGeneration != "" && sro.EngineGeneration != "" && cro.EngineGeneration != sro.EngineGeneration {
					// 跨代不是一律跳过：勾了「按值域换算」才允许搬（下面按两张花名册线性换算）；
					// 没勾就照旧跳过（原值照搬在跨代一定是错的）。
					if mapMode != "range" {
						// 跨代必然换算（页面已不再提供开关）；只有显式传 map=none 才跳过
						pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
							"参数 %s：源是 **%s**、目标片段「%s」是 **%s** —— 跨世代的尺度/范围不同，"+
								"而「按值域换算」没勾（或「允许跨引擎世代」没勾）⇒ 那一段跳过",
							jb.Param, sro.EngineGeneration, c.Name, cro.EngineGeneration))
						continue
					}
				}
				have := writableAnyLayers(cro, jb.Param)
				if !containsStr(have, jb.DstLayer) {
					pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
						"参数 %s：目标片段「%s」上没有可写的 %s 层（它有 %s）⇒ 那一段跳过",
						jb.Param, c.Name, jb.DstLayer, strings.Join(have, "、")))
					continue
				}
				for i := lo; i < hi; {
					if !covered[i-dstA] {
						i++
						continue
					}
					j := i
					for j < hi && covered[j-dstA] {
						j++
					}
					if j-i < 2 {
						// ACE 的 dense 写硬规则：每段连续 run ≥2 tick ⇒ 这 1 个 tick 谁也写不了
						pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
							"参数 %s：目标片段「%s」tick %d..%d 那段只有 %d 个 tick（dense 写要求每段 ≥2 tick）⇒ 那 %d 个 tick 不写",
							jb.Param, c.Name, i, j, j-i, j-i))
						i = j
						continue
					}
					tRead, dstPts, terr := readLayerRange(ctx, rd, c.UUID, jb.Param, jb.DstLayer, i-c.Begin, j-c.Begin)
					if terr != nil {
						pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
							"参数 %s：目标片段「%s」的 %s 层在 tick %d..%d 读不到（%v）⇒ 那一段跳过",
							jb.Param, c.Name, jb.DstLayer, i, j, terr))
						i = j
						continue
					}
					out := make([]*float64, j-i)
					clamped, drawn, mapped := 0, 0, 0
					for k := i; k < j; k++ {
						if int(k-i) < len(dstPts) && dstPts[k-i] != nil {
							drawn++
						}
						v := buf[k-dstA] // ⚠️ 索引按**目标窗口**的偏移（源窗口与它同长，一一对应）
						if v == nil {
							out[k-i] = nil // 源上没值 ⇒ 保持空档（不把"没画"写成 0）
							continue
						}
						x := *v
						// 「按值域换算」：源值域（源花名册）→ 目标值域（目标花名册）线性映射。
						// 同一世代/同一值域时是恒等，不必动；跨代（例：tension v1 0.3..2.5 ↔ v2 0..1）必须算。
						if mapMode == "range" {
							if dp, derr := pickParam(cro, jb.Param); derr == nil &&
								dp.Max > dp.Min && p.Max > p.Min && (dp.Min != p.Min || dp.Max != p.Max) {
								x = dp.Min + (x-p.Min)*(dp.Max-dp.Min)/(p.Max-p.Min)
								mapped++
							}
						}
						if x < p.Min {
							x, clamped = p.Min, clamped+1
						}
						if x > p.Max {
							x, clamped = p.Max, clamped+1
						}
						out[k-i] = &x
					}
					// ⚠️ 2026-10-23 实测：源那一段如果**一个点都没画**（v1 的 envelope 是空的很常见），
					// buf 全是 nil ⇒ out 也全是 nil。以前照样记成"要写"，执行时 anchors 空、一个字节没写，
					// 报告却写"已写入"。这里直接判空并如实跳过。
					nonNil := 0
					for _, v := range out {
						if v != nil {
							nonNil++
						}
					}
					if nonNil == 0 {
						pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
							"参数 %s：源那一段（%s）**一个点都没画**（层是空的）⇒ 目标「%s」tick %d..%d 不写（没东西可搬）",
							jb.Param, jb.SrcLayer, c.Name, i, j))
						i = j
						continue
					}
					if _, kind, bad := checkDenseRuns(out); bad {
						pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
							"参数 %s：目标片段「%s」tick %d..%d 算出来的载荷里有 1 个 tick 的%s（ACE 会拒）⇒ 那一段跳过",
							jb.Param, c.Name, i, j, kind))
						i = j
						continue
					}
					pd.paramWrites = append(pd.paramWrites, xcopyParamWrite{
						ClipUUID: c.UUID, Param: jb.Param, Layer: jb.DstLayer, PosBegin: i - c.Begin,
						Payload: out, IfMatch: tRead.Fingerprint, Ticks: len(out), DrawnBefore: drawn, Clamped: clamped,
						Shape: layerShapeOf(cro, jb.Param, jb.DstLayer),
					})
					if mapped > 0 {
						pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
							"参数 %s：目标「%s」tick %d..%d **按值域换算**了 %d 个 tick（源 %g..%g → 目标 %s）",
							jb.Param, c.Name, i, j, mapped, p.Min, p.Max, dstMinMaxOf(cro, jb.Param)))
					}
					pd.labelClip(c.UUID, c.Name)
					rep.WillParamLayers++
					rep.ParamTicks += len(out)
					i = j
				}
			}

			// ③ 如实交代：结构空洞（源侧没有片段）与"换过源层"
			holes := 0
			for k := 0; k < int(span); k++ {
				if !covered[k] {
					holes++
				}
			}
			if holes > 0 {
				pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
					"参数 %s：目标窗口里有 %d 个 tick 在**源侧没有片段覆盖** ⇒ 那些 tick 不写（保留目标原值，不去清空）",
					jb.Param, holes))
			}
			if srcFallback != "" {
				pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
					"参数 %s 没有实参 ⇒ 源读的是它自己的 %s 层", jb.Param, srcFallback))
			}
		}
		if rep.WillParamLayers == 0 {
			if table && len(picks) == 0 {
				pd.paramSkips = append(pd.paramSkips,
					"参数曲线：参数表交上来了但**一个格子都没勾** ⇒ 这一项什么都没写（这是有意的，不是失败）")
			} else {
				pd.paramSkips = append(pd.paramSkips,
					"参数曲线：这条轨的目标窗口里没有可搬的层（或全都读不到），这一项什么都没做")
			}
		}
	}

	// dynamic：锚点形状、没有实参 ⇒ 读各源片段的锚点（片段内 → 绝对）→ **平移** → 按目标片段分派写回
	if wantDyn {
		hasDyn := false
		for _, p := range sro.Params {
			if p.Param == "dynamic" && p.Available {
				hasDyn = true
			}
		}
		if !hasDyn {
			pd.paramSkips = append(pd.paramSkips, "这份 clip 上没有 `dynamic` 参数（v1 引擎没有它）⇒ 这一项跳过")
			return nil
		}
		type dynAnchor struct {
			abs int64
			val any
		}
		var got []dynAnchor
		for _, c := range srcClips {
			lo, hi := maxI64(c.Begin, srcA), minI64(c.End, srcB)
			if hi-lo < 1 {
				continue
			}
			_, anchors, derr := readPointsLayer(ctx, rd, c.UUID, "dynamic", "user", lo-c.Begin, hi-c.Begin)
			if derr != nil {
				pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
					"dynamic：源片段「%s」（tick %d..%d）的锚点读不到（%v）⇒ 那一段的锚点不搬", c.Name, lo, hi, derr))
				continue
			}
			for _, a := range anchors {
				tick, _ := a[0].(float64)
				got = append(got, dynAnchor{abs: c.Begin + int64(math.Round(tick)), val: a[1]})
			}
		}
		if len(got) == 0 {
			pd.paramSkips = append(pd.paramSkips, "源范围里没有 dynamic 锚点 ⇒ 不写（也不会去清目标那段）")
			return nil
		}
		delta := dstA - srcA
		type dynGroup struct {
			uuid, name string
			w          *xcopyDynamicWrite
		}
		byClip := map[string]*dynGroup{}
		var order []*dynGroup
		lost := 0
		for _, an := range got {
			abs := an.abs + delta
			c, ok := clipCovering(sc, abs)
			if !ok {
				lost++ // 平移后落到没有片段的地方 —— 写不进去，如实报出来
				continue
			}
			g := byClip[c.UUID]
			if g == nil {
				g = &dynGroup{uuid: c.UUID, name: c.Name, w: &xcopyDynamicWrite{ClipUUID: c.UUID, Delta: delta, SrcFrom: "user"}}
				byClip[c.UUID] = g
				order = append(order, g)
			}
			g.w.Anchors = append(g.w.Anchors, []any{abs - c.Begin, an.val})
			rep.WillDynamic++
		}
		if lost > 0 {
			pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
				"dynamic：有 %d 个锚点平移后落在**没有片段**的位置（写不进去）⇒ 丢掉（其它锚点照写）", lost))
		}
		for _, g := range order {
			w := g.w
			lo, hi := int64(math.MaxInt64), int64(math.MinInt64)
			for _, a := range w.Anchors {
				tick, _ := a[0].(int64)
				lo, hi = minI64(lo, tick), maxI64(hi, tick+1)
			}
			// ⚠️ 指纹要读**目标**那一段：`--if-match` 守的是被写的那一份。
			//（旧版（单片段时代）用的是**源**的指纹 —— 同片段时看不出来，多片段/跨轨就是错的守卫。）
			pr, _, perr := readPointsLayer(ctx, rd, g.uuid, "dynamic", "user", lo, hi)
			if perr != nil {
				pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
					"dynamic：目标片段「%s」的锚点 lane 读不到（%v）⇒ 那 %d 个锚点不写", g.name, perr, len(w.Anchors)))
				rep.WillDynamic -= len(w.Anchors)
				continue
			}
			w.IfMatch = pr.Fingerprint
			pd.dynWrites = append(pd.dynWrites, *w)
			pd.labelClip(g.uuid, g.name)
		}
	}
	return nil
}

// clipByUUID 反查片段（报告措辞用）。
func clipByUUID(sc *xcopyTrackScope, uuid string) (trackClip, bool) {
	for _, c := range sc.Clips {
		if c.UUID == uuid {
			return c, true
		}
	}
	return trackClip{}, false
}

// shortID 把 uuid 缩成报告里好认的样子（`{68ea3a85-…}` 太长，日志里看着累）。
func shortID(id string) string {
	s := strings.Trim(id, "{}")
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
// dstMinMaxOf 取某参数在目标花名册里的值域（给报告用）
func dstMinMaxOf(ro *paramRoster, param string) string {
	for _, p := range ro.Params {
		if p.Param == param {
			return fmt.Sprintf("%g..%g", p.ValueRange.Min, p.ValueRange.Max)
		}
	}
	return "?"
}
