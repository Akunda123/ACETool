package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// param.view —— 「参数映射」窗口（param.html）要的一整包只读数据。
//
// 为什么单独做一个读工具（而不是让页面自己拼几条 CLI）：
//
//	· 页面要的是**一个坐标系**：当前 clip 的 tick 长度、它从第几 tick 开始、
//	  一小节多少 tick、音符摆在哪儿、选区盖住哪儿 —— 这些必须来自**同一次快照**，
//	  否则画出来的范围会和写下去的范围差一截（分几次读中间用户切了片段就全错）。
//	· 花名册（哪些参数可用、各有哪几层、能不能写、值域多少）**必须现读**
//	  （§1 纪律：同一条 clip 的读数会随运行期解析出的声源变）。
//
// ⚠️ 只读：这条命令一个字节都不往 ACE 写（`vocalparam layers` + `clip note-content` +
// `timesig get` + `tempo get` + `selection get`）。
//
// ⚠️ **拍号与"小节"的口径**：ACE 的 tick 是 **480 PPQ**（四分音符 = 480 tick），
// 一小节 = numerator × (4×480/denominator) tick（4/4 ⇒ 1920）。小节线从**工程 tick 0** 起算，
// 所以"第 N 小节"的工程起点 = (N-1) × ticksPerBar，换算到片段内要减 clipBegin。
// 这一段是**照 minimap.js 的 TICK_PER_BAR = 1920 兜底**对齐的（两处必须一致，
// 否则页面上拖出来的范围和写下去的范围会差一小节）。

type paramLayerInfo struct {
	Layer  string `json:"layer"`
	Access string `json:"access"`
	Role   string `json:"role,omitempty"`
	Shape  string `json:"shape"`
	Sparse bool   `json:"sparse"`
}

type paramInfo struct {
	Param       string           `json:"param"`
	DisplayName string           `json:"displayName"`
	Available   bool             `json:"available"`
	Reason      string           `json:"unavailableReason,omitempty"`
	Scale       string           `json:"scale"`
	Min         float64          `json:"valueMin"`
	Max         float64          `json:"valueMax"`
	Layers      []paramLayerInfo `json:"layers,omitempty"`
	// Writable = 这一份 clip 上**能写、而且是 dense** 的层名（页面据此给勾选框）。
	// ⚠️ `points` 形状的层（v2 的 `dynamic`）不在这里：本工具逐 tick 算，吃不下锚点形状。
	Writable []string `json:"writableLayers,omitempty"`
}

type paramViewReport struct {
	ClipName string `json:"clipName"`
	ClipUUID string `json:"clipUuid"`
	TrackIdx int    `json:"trackIndex"`
	ClipIdx  int    `json:"clipIndex"`
	Located  string `json:"clipLocatedBy,omitempty"`

	ClipBegin int64 `json:"clipBegin"` // 工程 tick（换算小节要用它）
	ClipEnd   int64 `json:"clipEnd"`
	Length    int64 `json:"clipTicks"` // 片段内 tick 长度（参数的 posBegin..count 就是这个）

	Meter       string  `json:"meter,omitempty"`
	TicksPerBar int64   `json:"ticksPerBar"`
	Tempo       float64 `json:"tempo,omitempty"`
	Bars        int     `json:"clipBars"` // 片段跨多少小节（向上取整，给页面画标尺用）

	Notes    []minimapNote `json:"notes,omitempty"`
	MinPitch int           `json:"minPitch,omitempty"`
	MaxPitch int           `json:"maxPitch,omitempty"`

	HasSelection bool  `json:"hasSelection"`
	SelBegin     int64 `json:"selBegin,omitempty"` // 片段内 tick
	SelEnd       int64 `json:"selEnd,omitempty"`
	SelNotes     int   `json:"selNoteCount,omitempty"`

	Engine            string      `json:"engineGeneration,omitempty"`
	VocalControlRoute string      `json:"vocalControlRoute,omitempty"`
	Params            []paramInfo `json:"params,omitempty"`

	Note string `json:"note,omitempty"`
}

// ticksPerBarOf 把 `timesig get` 的第一条拍号换算成"一小节多少 tick"。
// 拿不到就按 4/4（1920）——**并且如实写在 note 里**，不假装知道。
func ticksPerBarOf(ctx context.Context, rd ReadFunc, rep *paramViewReport) int64 {
	var ts struct {
		Signatures []struct {
			Numerator   int `json:"numerator"`
			Denominator int `json:"denominator"`
		} `json:"signatures"`
	}
	if err := readJSON(ctx, rd, &ts, "timesig", "get"); err == nil && len(ts.Signatures) > 0 {
		n, d := ts.Signatures[0].Numerator, ts.Signatures[0].Denominator
		if n > 0 && d > 0 {
			rep.Meter = fmt.Sprintf("%d/%d", n, d)
			// 四分音符 = 480 tick ⇒ 一小节 = n × (4×480/d)
			return int64(n) * (4 * 480 / int64(d))
		}
	}
	rep.Note = appendNote(rep.Note, "⚠️ 读不到拍号（`timesig get`）⇒ 按 **4/4（一小节 1920 tick）** 算的小节，若你的工程不是 4/4，小节换算会偏。")
	return 1920
}

func appendNote(dst, add string) string {
	if dst == "" {
		return add
	}
	return dst + " " + add
}

// readParamView 是 param.view 的实现（只读）。
func readParamView(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	// 选区：`useSelection=false` ⇒ 只用来问"现在有没有选中音符、盖住哪一段"
	sc, err := readNoteScope(ctx, rd, false)
	if err != nil {
		return nil, err
	}
	if sc.ClipUUID == "" {
		return nil, fmt.Errorf("拿不到当前片段 —— 先在 ACE 里点开一个片段（编辑器里那个）")
	}

	rep := &paramViewReport{
		ClipName: sc.ClipName, ClipUUID: sc.ClipUUID,
		TrackIdx: sc.TrackIndex, ClipIdx: sc.ClipIndex, Located: sc.Located,
		ClipBegin: sc.Begin, ClipEnd: sc.End,
	}
	if rep.Length = sc.End - sc.Begin; rep.Length <= 0 {
		return nil, fmt.Errorf("片段的 tick 长度算出来是 %d（begin=%d end=%d）—— 先确认 ACE 里那个片段是正常打开的",
			rep.Length, sc.Begin, sc.End)
	}

	// 拍号 / 速度（都**容许失败**：少一样也要能画）
	rep.TicksPerBar = ticksPerBarOf(ctx, rd, rep)
	var tp struct {
		Points []struct {
			Value float64 `json:"value"`
		} `json:"points"`
		Tempo float64 `json:"tempo"`
	}
	if err := readJSON(ctx, rd, &tp, "tempo", "get"); err == nil {
		if tp.Tempo > 0 {
			rep.Tempo = tp.Tempo
		} else if len(tp.Points) > 0 {
			rep.Tempo = tp.Points[0].Value
		}
	}
	rep.Bars = int((rep.Length + rep.TicksPerBar - 1) / rep.TicksPerBar)

	// 音符（片段内坐标）+ 音域
	notes, err := readMinimapNotes(ctx, rd, sc.TrackIndex, sc.ClipIndex)
	if err != nil {
		return nil, fmt.Errorf("读片段音符（`clip note-content`）：%w", err)
	}
	rep.Notes = notes
	for _, n := range notes {
		if rep.MinPitch == 0 || n.Pitch < rep.MinPitch {
			rep.MinPitch = n.Pitch
		}
		if n.Pitch > rep.MaxPitch {
			rep.MaxPitch = n.Pitch
		}
	}

	// 选区范围（片段内）：只用来画"所选音符范围"那一段
	var sel struct {
		HasSelection bool `json:"hasSelection"`
		Notes        []struct {
			Pos    int64 `json:"pos"`
			EndPos int64 `json:"endPos"`
		} `json:"notes"`
	}
	if err := readJSON(ctx, rd, &sel, "selection", "get", "--scope", "editor"); err == nil && sel.HasSelection && len(sel.Notes) > 0 {
		lo, hi := sel.Notes[0].Pos, sel.Notes[0].EndPos
		for _, n := range sel.Notes {
			if n.Pos < lo {
				lo = n.Pos
			}
			if n.EndPos > hi {
				hi = n.EndPos
			}
		}
		rep.HasSelection, rep.SelBegin, rep.SelEnd, rep.SelNotes = true, lo, hi, len(sel.Notes)
	}

	// 花名册（**每次现读**）
	ro, err := readParamRoster(ctx, rd, sc.ClipUUID)
	if err != nil {
		return nil, err
	}
	rep.Engine, rep.VocalControlRoute = ro.EngineGeneration, ro.VocalControlRoute
	for _, p := range ro.Params {
		pi := paramInfo{
			Param: p.Param, DisplayName: p.DisplayName, Available: p.Available, Reason: p.Unavailable,
			Scale: p.Scale, Min: p.ValueRange.Min, Max: p.ValueRange.Max,
		}
		for _, l := range p.Layers {
			pi.Layers = append(pi.Layers, paramLayerInfo{
				Layer: l.Layer, Access: l.Access, Role: l.Role, Shape: l.Shape, Sparse: l.Sparse,
			})
		}
		// 可写层用**规范顺序**（包络 → 真值 → user …），与页面上勾选框的顺序一致
		pi.Writable = writableAnyLayers(ro, p.Param)
		rep.Params = append(rep.Params, pi)
	}

	if rep.Engine == "" {
		rep.Note = appendNote(rep.Note, "⚠️ 花名册没报 `engineGeneration` —— 层集合与值域都以这份花名册为准，别照文档猜。")
	}
	return rep, nil
}

func init() {
	register(&Tool{
		ID: "param.view", Category: "参数",
		Title: "参数映射：当前片段一览（只读）",
		Desc: "给「参数映射」窗口备一包**同一次快照**的数据：当前片段的 tick 长度与工程起点、" +
			"拍号换算出的一小节 tick 数、音符（片段内坐标）、选区盖住的范围、" +
			"以及**现读的**参数花名册（每个参数可不可用、值域、有哪几层、哪几层能写且是 dense）。" +
			"⚠️ 只读；花名册与层集合**永不缓存**（随引擎世代与运行期声源变）。",
		Kind:   KindRead,
		Source: "自建：`selection get --scope editor` + `clip note-content` + `timesig get` + `tempo get` + `vocalparam layers`",
		Params: []Param{},
		Inspect: func(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
			return readParamView(ctx, rd, vals)
		},
	})
}

// pickParam 在花名册里找一个参数（找不到/不可用都给**能照着改**的错误）。
func pickParam(ro *paramRoster, name string) (*paramInfo, error) {
	var avail []string
	for _, p := range ro.Params {
		if p.Available {
			avail = append(avail, p.Param)
		}
	}
	sort.Strings(avail)
	for _, p := range ro.Params {
		if p.Param != name {
			continue
		}
		if !p.Available {
			return nil, fmt.Errorf("参数 %q 在这一份 clip 上**不可用**（%s）", name, p.Unavailable)
		}
		return &paramInfo{
			Param: p.Param, DisplayName: p.DisplayName, Scale: p.Scale,
			Min: p.ValueRange.Min, Max: p.ValueRange.Max,
		}, nil
	}
	return nil, fmt.Errorf("参数 %q 不在这一份 clip 的花名册里。实际可用的是：%s "+
		"（花名册随**引擎世代**变，所以是现读的，不是写死的清单）", name, strings.Join(avail, "、"))
}

// denseWritableLayers 列出某参数**能写且是 dense** 的层。
// ⚠️ 顺序是**定死的规范顺序**（`envelope` 包络 → `direct` 真值 → `user` → 其余按花名册），
// 不跟花名册的原始顺序走 —— 用户 2026-10-19：「v1 包络大于实参，**包络优先**」。
// 页面的勾选框、报告里的层名、留空时的默认层，三处都按这个顺序，别各排各的。
func denseWritableLayers(ro *paramRoster, param string) []string {
	var have []string
	for _, p := range ro.Params {
		if p.Param != param {
			continue
		}
		for _, l := range p.Layers {
			if l.Access == "read-write" && l.Shape == "dense" {
				have = append(have, l.Layer)
			}
		}
	}
	var out []string
	for _, pref := range canonicalLayerOrder {
		if containsStr(have, pref) {
			out = append(out, pref)
		}
	}
	for _, l := range have {
		if !containsStr(out, l) {
			out = append(out, l)
		}
	}
	return out
}

// canonicalLayerOrder 是层在界面上/报告里的显示顺序：**包络在前、实参在后**。
// ⭐ 用户 2026-10-19 确认：「v1 包络大于实参，**包络优先**」（包括跨版本时也是）——
// 这条把 2026-10-19 早些时候那版"实参在前"的尝试撤回了。
var canonicalLayerOrder = []string{"envelope", "direct", "user", "global"}

// writableLayerShapes 把"这个参数能写的层 + 形状"拼成人话（`user/points`）——
// 报错时要能一眼看出**为什么**写不了（`points` 形状的层吃不下逐 tick 的算法）。
func writableLayerShapes(ro *paramRoster, param string) string {
	var out []string
	for _, p := range ro.Params {
		if p.Param != param {
			continue
		}
		for _, l := range p.Layers {
			if l.Access == "read-write" {
				out = append(out, l.Layer+"/"+l.Shape)
			}
		}
	}
	if len(out) == 0 {
		return "（一个可写层都没有）"
	}
	return strings.Join(out, "、")
}

// parseLayerList 解析 `layers` 参数（逗号分隔）。空 ⇒ 按目标参数自己推：
// **优先 `envelope`（包络，乘数层、相对改动温和）**，没有就 `direct`，再没有就第一个可写的 dense 层。
// ⚠️ 默认顺序与页面上"包络默认勾选、真值默认不勾"**必须一致**（用户 2026-10-07 的口径）——
// 两条路（主面板表单 / 参数映射窗口）默认不一样的话，同一个工具会写出两种层。
func parseLayerList(raw string, ro *paramRoster, dstParam string) ([]string, error) {
	var out []string
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	// ⚠️ 用 writableAnyLayers（dense 或 points 都算）：ACE 在不同工程/世代里同一层可能报
	// dense、也可能报 points（2026-10-22 实测：v1 的 direct/envelope 报 points）。
	// 写的时候由 paramsync 按形状选 dense / 锚点两条路。
	if len(out) > 0 {
		have := writableAnyLayers(ro, dstParam)
		for _, l := range out {
			if !containsStr(have, l) {
				return nil, fmt.Errorf("目标参数 %s 上没有可写的层 %q。它有：%s "+
					"（层集合随引擎世代变：v1 是 baseline+direct+envelope，v2 是 baseline+user）",
					dstParam, l, strings.Join(have, "、"))
			}
		}
		return out, nil
	}
	have := writableAnyLayers(ro, dstParam)
	if len(have) == 0 {
		return nil, fmt.Errorf("目标参数 %s 上一层可写的层都没有 —— 换一个参数", dstParam)
	}
	for _, pref := range []string{"envelope", "direct"} {
		if containsStr(have, pref) {
			return []string{pref}, nil
		}
	}
	return []string{have[0]}, nil
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// barSpan 把"第几小节到第几小节"（1 起、含两端）折成**片段内 tick**。
// 小节线从工程 tick 0 起算 ⇒ 片段内的起点要减 clipBegin；再与 [0, length) 求交。
func barSpan(beginBar, endBar int, tpb, clipBegin, length int64) (int64, int64, error) {
	if tpb <= 0 {
		return 0, 0, fmt.Errorf("一小节的 tick 数算出来是 %d，不敢换算", tpb)
	}
	if beginBar < 1 || endBar < 1 {
		return 0, 0, fmt.Errorf("小节号从 **1** 起（收到 %d..%d）", beginBar, endBar)
	}
	if endBar < beginBar {
		return 0, 0, fmt.Errorf("结束小节（%d）比开始小节（%d）还小 —— 换一下", endBar, beginBar)
	}
	lo := int64(beginBar-1)*tpb - clipBegin
	hi := int64(endBar)*tpb - clipBegin
	if lo < 0 {
		lo = 0
	}
	if hi > length {
		hi = length
	}
	if lo >= hi {
		return 0, 0, fmt.Errorf("第 %d..%d 小节落在片段之外（片段占工程 tick %d..%d，共 %d tick）—— "+
			"片段外没有参数曲线可写", beginBar, endBar, clipBegin, clipBegin+length, length)
	}
	return lo, hi, nil
}

// barOf 反算：片段内某个 tick 落在**工程第几小节**（1 起）。
func barOf(tick, clipBegin, tpb int64) int {
	if tpb <= 0 {
		return 1
	}
	return int((tick+clipBegin)/tpb) + 1
}

// writableAnyLayers 列出某参数**能写**的层（dense 或 points 都算）。
//
// 用户 2026-10-22「写进是空值」：那台机器的 v1 花名册里 direct/envelope 报的是 **points** 形状，
// 而页面只认 dense ⇒ 一行勾选框都没有。现在两种形状都支持（paramsync 按形状选读/写法）。
func writableAnyLayers(ro *paramRoster, param string) []string {
	var out []string
	for _, p := range ro.Params {
		if p.Param != param {
			continue
		}
		for _, l := range p.Layers {
			if l.Access == "read-write" {
				out = append(out, l.Layer)
			}
		}
	}
	// 规范顺序（包络在前）——页面与测试都按这个断言
	sort.SliceStable(out, func(i, j int) bool { return layerRank(out[i]) < layerRank(out[j]) })
	return out
}

// layerRank 给出层的规范顺序：包络 → 真值/实参 → 其它。
func layerRank(l string) int {
	for i, x := range canonicalLayerOrder {
		if x == l {
			return i
		}
	}
	return 99
}
