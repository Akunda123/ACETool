package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// 移植 `参数同步变化.js`（AKD「参数-参数同步变化」，265 行）+ 用户 2026-10-07 的口径修正。
//
// 原脚本干的事：让**一个参数随另一个参数变化**。它遍历**目标参数已有的点**，
// 在同一个位置读出源值，按公式算出新值写回去：
//
//	不同参数：newVal = 目标值 + 倍率 × 源值 + 偏移
//	同一参数：newVal = 倍率 × 目标值 + 偏移
//	可选「按数值范围等比映射」；可选筛选（两段条件，> 或 <，AND / OR）：**不通过的点保持原值不动**
//
// ⛔⛔ **本版按用户 2026-10-07 的裁定改了三处**（与上面那段原脚本口径**不同**，别再照原脚本"修回去"）：
//
//	1. **叠加**（AKD 原算法）：不同参数 `newVal = 目标值 + 倍率 × 源值 + 偏移`；
//	   同一参数 `newVal = 倍率 × 目标值 + 偏移`。
//	   ⚠️ 当晚我先按"覆盖"实现过一版，用户随后更正：「改成叠加，原算法，我刚才想错了」——**以叠加为准**。
//	2. **不要等比映射**（用户：「由于 ace 所有范围都一致就不用等比映射了」）⇒ `mapRange` 整个去掉。
//	   （ACE 每个参数自己的 `valueRange` 仍然照读，用来**夹边界**。）
//	3. **写进哪一层是多选**：v1（`verse24`）勾「包络(=`envelope`)/真值(=`direct`)」，两个都勾就都写；
//	   v2（`singing-mamba`）没有这两层，只有 `user` ⇒ 页面上不给勾选框（不是我们藏起来，是它真没有）。
//
// ⛔ 参数表是 **ACE 自己的**（现读 `vocalparam layers`）：`air`/`falsetto`/`tension`/`energy`/
// `formant`（v1）与 `air`/`falsetto`/`tension`/`energy`/`dynamic`（v2）。原脚本那 9 项里 7 项 ACE 没有。
//
// ⚠️ 落地形状完全不同：
//
//	SV 那边参数是**稀疏锚点**，原脚本"遍历目标已有的点逐个算"。
//	ACE 的可写层是 **dense（每个 tick 一个值）** ⇒ 这里读回**整段数组**，逐 tick 算完**整段写回**。
//	所以"会改多少个"是 **tick 数**，不是点数。
//	`null` = 那个 tick **没画**（空档）⇒ 空档要保持空档，绝不能变成 0（那等于"把没画过改成画成 0"）。
//
// ⚠️ 三条 ACE 侧的现实（都来自 `help vocalparam write` / `help vocalparam read` 原文）：
//
//	· **写是"替换该层在这个 tick 范围内的点"** ⇒ 我们只写请求的那一段，范围外**不动**（不必整层回写）；
//	· dense 写**必须给 `--pos-begin`**（漏了被拒 `posBegin is required for a dense write`）；
//	· **每一段连续 run 至少 2 个 tick**（值也好空档也好，1 tick 会被直接拒并点名要修的 tick）
//	  ⇒ 我们在**发之前自己先查**（见 checkDenseRuns），别白跑一趟。
//
// ⚠️ 曲线**不该放在命令行上**（280320 tick 的片段 JSON 有 7.8 MB）⇒ 写临时文件走 `--points @路径`。

// vocalLayerRead 是 `vocalparam read --layer L` 的回包（只取要用的字段）。
type vocalLayerRead struct {
	PosBegin         int64  `json:"posBegin"`
	Count            int    `json:"count"`
	Scale            string `json:"scale"`
	EngineGeneration string `json:"engineGeneration"`
	Fingerprint      string `json:"fingerprint"`
	ValueRange       struct {
		Min float64 `json:"min"`
		Max float64 `json:"max"`
	} `json:"valueRange"`
	Layers []struct {
		Layer  string     `json:"layer"`
		Access string     `json:"access"`
		Shape  string     `json:"shape"`
		Sparse bool       `json:"sparse"`
		Points []*float64 `json:"points"`
	} `json:"layers"`
	// effective：**合并后的实参**（只读、永不落盘）。请求 `--layer effective` 时 `layers` 是空的，
	// 值在这个块里。注意：**没有 merge 的 lane（v2 的 `dynamic`）压根没有这一块**。
	Effective *struct {
		Layer  string     `json:"layer"`
		Shape  string     `json:"shape"`
		Points []*float64 `json:"points"`
	} `json:"effective"`
}

// paramRoster 现读这一份 clip 的参数花名册（**永不缓存** —— 读数会随运行期解析出的声源变）。
type paramRoster struct {
	EngineGeneration  string `json:"engineGeneration"`
	VocalControlRoute string `json:"vocalControlRoute"`
	Params            []struct {
		Param       string `json:"param"`
		DisplayName string `json:"displayName"`
		Available   bool   `json:"available"`
		Unavailable string `json:"unavailableReason"`
		Scale       string `json:"scale"`
		ValueRange  struct {
			Min float64 `json:"min"`
			Max float64 `json:"max"`
		} `json:"valueRange"`
		Layers []struct {
			Layer  string `json:"layer"`
			Access string `json:"access"`
			Role   string `json:"role"`
			Shape  string `json:"shape"`
			Sparse bool   `json:"sparse"`
		} `json:"layers"`
	} `json:"params"`
}

func readParamRoster(ctx context.Context, rd ReadFunc, clipUUID string) (*paramRoster, error) {
	var ro paramRoster
	if err := readJSON(ctx, rd, &ro, "vocalparam", "layers", "--clip-uuid", clipUUID); err != nil {
		return nil, fmt.Errorf("读参数花名册（`vocalparam layers`）：%w", err)
	}
	return &ro, nil
}

// readLayerRange 读某一层的**一段**（clip-local tick，end 开区间）。
// `layer` 传 `effective` 时读的是合并实参（`layers[]` 为空、值在 `effective` 块里）。
func readLayerRange(ctx context.Context, rd ReadFunc, clipUUID, param, layer string, begin, end int64) (*vocalLayerRead, []*float64, error) {
	args := []string{"vocalparam", "read", "--clip-uuid", clipUUID, "--param", param, "--layer", layer}
	if begin >= 0 && end > begin {
		args = append(args, "--range-begin", strconv.FormatInt(begin, 10), "--range-end", strconv.FormatInt(end, 10))
	}
	var out vocalLayerRead
	if err := readJSON(ctx, rd, &out, args...); err != nil {
		// ⚠️ points 形状的层返回的是 [[tick,value],…]，按 dense（每 tick 一个数）解析会直接
		// unmarshal 失败（2026-10-23 实测：tension 的 envelope）。失败就改按锚点读 + 逐 tick 采样。
		if begin >= 0 && end > begin {
			if vr, anchors, perr := readPointsLayer(ctx, rd, clipUUID, param, layer, begin, end); perr == nil {
				return &vocalLayerRead{Fingerprint: vr.Fingerprint}, sampleAnchorsPerTick(anchors, begin, end), nil
			}
		}
		return nil, nil, fmt.Errorf("读参数 %s 的 %s 层：%w", param, layer, err)
	}
	if layer == "effective" {
		if out.Effective == nil || len(out.Effective.Points) == 0 {
			return nil, nil, fmt.Errorf("参数 %s 没有 `effective`（合并实参）—— "+
				"没有 merge 的 lane（例如 v2 的 `dynamic`）就是没有这一块。换一个参数当**源**，"+
				"或者把它那一层 `user` 当源用", param)
		}
		return &out, out.Effective.Points, nil
	}
	for _, l := range out.Layers {
		if l.Layer != layer {
			continue
		}
		if l.Shape != "dense" {
			// ⚠️ ACE 在有些工程/版本里把同一层报成 **points**（锚点）——例如 2026-10-22 那台机器的
			// v1 direct/envelope。按锚点读回来**逐 tick 采样**，调用方拿到的仍是"每 tick 一个值"。
			vr, anchors, perr := readPointsLayer(ctx, rd, clipUUID, param, layer, begin, end)
			if perr != nil {
				return nil, nil, perr
			}
			lo := begin
			if lo < 0 {
				lo = 0
			}
			hi := end
			if hi <= lo {
				if vr != nil && vr.PosBegin >= 0 {
					hi = vr.PosBegin + 1
				}
			}
			return &vocalLayerRead{Fingerprint: vr.Fingerprint}, sampleAnchorsPerTick(anchors, lo, hi), nil
		}
		if l.Access != "read-write" {
			return nil, nil, fmt.Errorf("参数 %s 的 %s 层是**只读**的（`access: %s`）—— 写不了", param, layer, l.Access)
		}
		return &out, l.Points, nil
	}
	var have []string
	for _, l := range out.Layers {
		have = append(have, l.Layer+"/"+l.Access)
	}
	return nil, nil, fmt.Errorf("参数 %s 上**没有 %s 这一层**。这一份 clip 实际有的层：%s "+
		"（层集合随引擎世代变：v1 是 baseline+direct+envelope，v2 是 baseline+user）",
		param, layer, strings.Join(have, "、"))
}

// writePointsLayer 写一层**锚点(points)形状**的曲线（如 v2 的 `dynamic`）。
//
// ⚠️ 与 dense 写不同：**不给 `--pos-begin`**（锚点自带 tick，给了会被拒），
// 而且写是"替换**首个到末个锚点**盖住的那一段"（`help vocalparam write` 原文）。
// `[tick, null]` 是**空档标记**，原样带着走（不会被插值跨过去）。
func writePointsLayer(ctx context.Context, rd ReadFunc, clipUUID, param, layer string, anchors []any, ifMatch string) error {
	js, err := json.Marshal(anchors)
	if err != nil {
		return fmt.Errorf("序列化锚点：%w", err)
	}
	f, err := os.CreateTemp("", "acetool-anchors-*.json")
	if err != nil {
		return fmt.Errorf("建临时文件放锚点：%w", err)
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(js); err != nil {
		f.Close()
		return fmt.Errorf("写临时锚点文件：%w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关临时锚点文件：%w", err)
	}
	args := []string{"vocalparam", "write",
		"--clip-uuid", clipUUID, "--param", param, "--layer", layer, "--points", "@" + name}
	if strings.TrimSpace(ifMatch) != "" {
		args = append(args, "--if-match", ifMatch)
	}
	return runWrite(ctx, rd, args...)
}

// vocalPointsRead 是**锚点形状**那一层的读回包（`layers[].points` 是 `[[tick, value|null], …]`）。
type vocalPointsRead struct {
	PosBegin    int64  `json:"posBegin"`
	Fingerprint string `json:"fingerprint"`
	Layers      []struct {
		Layer  string            `json:"layer"`
		Shape  string            `json:"shape"`
		Points []json.RawMessage `json:"points"`
	} `json:"layers"`
}

// readPointsLayer 读一层锚点（clip-local tick 段，end 开区间）。
// 返回的每个锚点是 `[tick, value|null]`（value 用 `any`：可能是数字、也可能是 null）。
func readPointsLayer(ctx context.Context, rd ReadFunc, clipUUID, param, layer string, begin, end int64) (*vocalPointsRead, [][]any, error) {
	args := []string{"vocalparam", "read", "--clip-uuid", clipUUID, "--param", param, "--layer", layer}
	if begin >= 0 && end > begin {
		args = append(args, "--range-begin", strconv.FormatInt(begin, 10), "--range-end", strconv.FormatInt(end, 10))
	}
	var out vocalPointsRead
	if err := readJSON(ctx, rd, &out, args...); err != nil {
		return nil, nil, fmt.Errorf("读参数 %s 的 %s 层（锚点）：%w", param, layer, err)
	}
	for _, l := range out.Layers {
		if l.Layer != layer {
			continue
		}
		if l.Shape != "points" {
			return nil, nil, fmt.Errorf("参数 %s 的 %s 层是 **%s** 形状，不是锚点形状 —— 走错路了（这是内部判据，不该发生）",
				param, layer, l.Shape)
		}
		anchors := make([][]any, 0, len(l.Points))
		for _, raw := range l.Points {
			var pair []any
			if err := json.Unmarshal(raw, &pair); err != nil || len(pair) != 2 {
				return nil, nil, fmt.Errorf("参数 %s 的 %s 层里有个锚点看不懂：%s", param, layer, string(raw))
			}
			anchors = append(anchors, pair)
		}
		return &out, anchors, nil
	}
	return nil, nil, fmt.Errorf("参数 %s 上没有 %s 这一层（读锚点用）", param, layer)
}

// readDenseLayer 读**整层**（不带范围）——"整层搬过去"那类用法（如 `xcopy.notes` 的参数表）要它。
// 带范围读的走 `readLayerRange`（本工具自己用）。
func readDenseLayer(ctx context.Context, rd ReadFunc, clipUUID, param, layer string) (*vocalLayerRead, []*float64, error) {
	return readLayerRange(ctx, rd, clipUUID, param, layer, -1, -1)
}

// writeDenseLayer 把一段值写回某一层（**只替换这一段**，范围外不动）。
//
// ⚠️ 走**临时文件 + `@路径`**：曲线是 bulk data，放命令行会顶到 Windows 的长度上限
// （官方口径也是"曲线不该放在命令行上"）。`Client` 没给 stdin 口子，`@文件` 正好够用。
//
// `payload` 收 `any` 而不是 `[]float64`：dense 层里 **`null` = 空档（未画）**，
// 而"把空档搬回去"必须保持 `null`（变成 0 就是另一回事了）。
func writeDenseLayer(ctx context.Context, rd ReadFunc, clipUUID, param, layer string, posBegin int64, payload any, ifMatch string) error {
	js, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化曲线：%w", err)
	}
	f, err := os.CreateTemp("", "acetool-curve-*.json")
	if err != nil {
		return fmt.Errorf("建临时文件放曲线：%w", err)
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(js); err != nil {
		f.Close()
		return fmt.Errorf("写临时曲线文件：%w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关临时曲线文件：%w", err)
	}
	args := []string{"vocalparam", "write",
		"--clip-uuid", clipUUID, "--param", param, "--layer", layer,
		"--pos-begin", strconv.FormatInt(posBegin, 10), "--points", "@" + name}
	// ⚠️ `--if-match`：读回来的指纹带上（防覆盖别人的改动）。没有指纹就不带（不假装有守卫）。
	if strings.TrimSpace(ifMatch) != "" {
		args = append(args, "--if-match", ifMatch)
	}
	return runWrite(ctx, rd, args...)
}

// checkDenseRuns 在**发之前**查"每一段连续 run 至少 2 个 tick"这条硬门槛
// （官方：1 tick 的 run 会被直接拒，错误里点名要修的 tick）。
// 返回第一个犯规的 run：它的起点下标与"是值还是空档"。
// 为什么要自己查：白跑一趟要几秒，而且报错在 CLI 那侧只有英文的一段。
func checkDenseRuns(payload []*float64) (int, string, bool) {
	if len(payload) == 0 {
		return 0, "", false
	}
	kind := func(p *float64) string {
		if p == nil {
			return "空档"
		}
		return "值"
	}
	runStart, runKind := 0, kind(payload[0])
	for i := 1; i <= len(payload); i++ {
		cur := ""
		if i < len(payload) {
			cur = kind(payload[i])
		}
		if cur == runKind {
			continue
		}
		if i-runStart < 2 {
			return runStart, runKind, true
		}
		runStart, runKind = i, cur
	}
	return 0, "", false
}

// filterPass 是原脚本筛选那段的**直译**（保留原样）。
// ⚠️ 照抄了原脚本的**闭边**：选「小于」用的是 `srcVal <= v`、选「大于」用的是 `srcVal > v`
// （一边闭一边开，看着不对称，但那是原脚本的行为，改了就是另一回事）。
// `mode2`: "" = 只看条件1；"and" / "or"。
func filterPass(srcVal, v1 float64, lt1 bool, mode2 string, v2 float64, lt2 bool) bool {
	c1 := srcVal > v1
	if lt1 {
		c1 = srcVal <= v1
	}
	if mode2 == "" {
		return c1
	}
	c2 := srcVal > v2
	if lt2 {
		c2 = srcVal <= v2
	}
	if mode2 == "and" {
		return c1 && c2
	}
	return c1 || c2
}

type paramSyncLayerResult struct {
	Layer string `json:"layer"`
	// Changed = 这个层上真正会变的 tick 数（和原值差 > 1e-9，或原来是空档）
	Changed int `json:"ticksChanged"`
	// Filtered = 被筛掉、**保持原值**的 tick 数（空档保持空档）
	Filtered int `json:"ticksFilteredOut"`
	// Clamped = 算出来越出该参数值域、被夹到边界的 tick 数（ACE 对越界是**直接拒**、不 clamp）
	Clamped int `json:"ticksClamped"`
	// DrawnBefore = 这个层在这一段里本来画过多少个 tick（0 = 整段未画）
	DrawnBefore int `json:"ticksDrawnBefore"`
	// NoSource = 源在这个 tick 上没有值（空档）⇒ 跳过，保持原值
	NoSource int `json:"ticksNoSource"`
}

type paramSyncStats struct {
	SrcMin float64 `json:"srcValueMin"`
	SrcMax float64 `json:"srcValueMax"`
	NewMin float64 `json:"newValueMin"`
	NewMax float64 `json:"newValueMax"`
}

type paramSyncReport struct {
	Clip      string `json:"clip"`
	ClipUUID  string `json:"clipUuid"`
	Engine    string `json:"engineGeneration,omitempty"`
	ScaleName string `json:"scale,omitempty"`

	SrcParam string `json:"srcParam"`
	DstParam string `json:"dstParam"`
	SamePara bool   `json:"sameParam"`
	// 源值是从哪来的：`effective`（合并实参）或某个可写层（源没有 merge 时的退路）
	SrcFrom string `json:"srcFrom"`

	// Mode = add（叠加）/ overwrite（覆盖）：页面据此显示公式
	Mode string `json:"mode,omitempty"`

	Layers []string `json:"layers"`

	Range     string `json:"range"`
	RangeDesc string `json:"tickRange"`
	BeginTick int64  `json:"rangeBegin"`
	EndTick   int64  `json:"rangeEnd"`
	Ticks     int    `json:"ticks"`
	Bars      string `json:"bars,omitempty"` // 指定小节模式下：第几到第几小节（工程口径）

	Scale  float64 `json:"scaleFactor"`
	Offset float64 `json:"offset"`

	DstRangeMin float64 `json:"dstValueMin"`
	DstRangeMax float64 `json:"dstValueMax"`

	FilterDesc string `json:"filter"`

	Results []paramSyncLayerResult `json:"perLayer"`
	Stats   paramSyncStats         `json:"stats"`

	// 前后各取几个做对照（**别把几十万个数塞进结果**）。
	SampleBefore []*float64 `json:"sampleBefore,omitempty"`
	SampleAfter  []*float64 `json:"sampleAfter,omitempty"`

	Roster []string `json:"availableParams,omitempty"`
	Calls  []string `json:"plannedCalls,omitempty"`
	Did    []string `json:"done,omitempty"`
	Skips  []string `json:"skipped,omitempty"`
	DryRun bool     `json:"dryRun,omitempty"`
	Note   string   `json:"note,omitempty"`
}

func init() {
	register(&Tool{
		ID: "param.sync", Category: "参数",
		Title: "参数映射（一个参数按倍率/偏移换算到另一个）",
		Desc: "让**一个参数随另一个参数变化**：逐 tick 读源参数的**实参**（`effective`），" +
			"按 AKD 原算法**叠加**算完，写进目标参数你勾的层（v1：包络/真值可多选；v2 只有 `user`）：" +
			"不同参数 `新值 = 目标值 + 倍率 × 源值 + 偏移`；同一参数 `新值 = 倍率 × 目标值 + 偏移`。" +
			"⛔ 与 AKD 原脚本只有两处不同（用户 2026-10-07 裁定）：**不做等比映射**、写哪层是**多选**。" +
			"筛选（两段条件，> 或 ≤，AND/OR）：**不通过的 tick 保持原值**（空档保持空档）。" +
			"范围 = 整个片段 / 选中音符 / 指定小节（`beginBar..endBar`，1 起、含两端）。" +
			"写只**替换这一段**（`help vocalparam write`：Replace … over a tick range），范围外一个字节都不动。" +
			"**干跑默认**：勾着只出计划 + 前后样本对照，一个字节都不写。",
		Kind:   KindWrite,
		Source: "AKD `参数同步变化.js`（口径按用户 2026-10-07 修正）",
		Params: []Param{
			textParam("srcParam", "源参数（跟着它变）", "tension", "air/falsetto/tension/energy/formant",
				"必须是这一份 clip 的花名册里 `available:true` 的（结果里列出实际可用的）"),
			textParam("dstParam", "目标参数（被改的）", "energy", "同上",
				"留空或与源相同 ⇒ 源就是它自己的实参，等于对这条曲线做倍率/偏移"),
			{Name: "srcLayer", Label: "源读哪一层（v1）", Type: ParamSelect, Default: "auto",
				Options: []Option{
					{Value: "auto", Label: "自动（包络优先，没有包络才用实参）"},
					{Value: "envelope", Label: "包络（乘数层）"},
					{Value: "effective", Label: "实参（合并后的绝对值）"},
				},
				Hint: "用户 2026-10-22：「v1 应该可以选择包络或实参，包络优先」⇒ 默认 auto = 有包络就搬包络"},
			textParam("layers", "写进哪些层（逗号分隔；留空按目标自己推）", "", "envelope,direct",
				"v1 有 `envelope`(包络)/`direct`(真值) 可多选；v2 只有 `user`。留空 = **优先 `envelope`**（包络，乘数层），"+
					"没有就 `direct`，再没有就第一个可写的 dense 层 —— 与页面上「包络默认勾选」保持一致"),
			checkParam("mapRange", "按值域换算（先把源值线性缩放到目标的值域，再套公式）", false),
			{Name: "mode", Label: "计算方式", Type: ParamSelect, Default: "add",
				Options: []Option{
					{Value: "add", Label: "叠加（新值 = 目标值 + 倍率 × 源值 + 偏移）"},
					{Value: "overwrite", Label: "覆盖（新值 = 倍率 × 源值 + 偏移，不看目标原值）"},
				},
				Hint: "用户 2026-10-24：「加一个覆盖模式」——叠加会在已有曲线上累加；覆盖完全由源决定"},
			textParam("scale", "数值倍率", "1", "1", "留空按 1；填了不是数字就报错（不静默当 1）"),
			textParam("offset", "数值偏移", "0", "0", "留空按 0"),
			{Name: "range", Label: "范围", Type: ParamSelect, Default: "clip",
				Options: []Option{
					{Value: "clip", Label: "整个片段"},
					{Value: "sel", Label: "选中音符的范围"},
					{Value: "bars", Label: "指定小节（beginBar..endBar）"},
				}},
			textParam("beginBar", "开始小节（1 起，含）", "1", "1", "只在范围=指定小节时用"),
			textParam("endBar", "结束小节（1 起，含）", "1", "1", "只在范围=指定小节时用"),
			{Name: "filter", Label: "筛选", Type: ParamSelect, Default: "off",
				Options: []Option{
					{Value: "off", Label: "不开筛选"},
					{Value: "gt", Label: "源值 > 阈值"},
					{Value: "lt", Label: "源值 ≤ 阈值（原脚本的「小于」是闭边）"},
				}},
			textParam("filterValue", "筛选阈值", "0", "0", "只在开了筛选时用"),
			{Name: "filter2", Label: "第二段筛选", Type: ParamSelect, Default: "none",
				Options: []Option{
					{Value: "none", Label: "不用第二段"},
					{Value: "and", Label: "与第一段 AND"},
					{Value: "or", Label: "与第一段 OR"},
				}},
			{Name: "filter2Mode", Label: "第二段条件", Type: ParamSelect, Default: "gt",
				Options: []Option{{Value: "gt", Label: "源值 > 阈值"}, {Value: "lt", Label: "源值 ≤ 阈值"}}},
			textParam("filter2Value", "第二段阈值", "0", "0", ""),
		},
		Plan:  paramSyncPlan,
		Apply: paramSyncApply,
	})
}

// paramSyncPlanData 是"算完但还没写"的中间结果（Plan 用它出报告，Apply 用它写）。
type paramSyncPlanData struct {
	rep     *paramSyncReport
	clip    string
	dst     string
	begin   int64
	payload  map[string][]*float64 // layer → 这一段的新值
	match   map[string]string     // layer → 读回来的指纹（--if-match）
	shapes  map[string]string     // layer → dense / points（Apply 按它选写法）
}

func validateFloat(vals map[string]string, name string, def float64, label string) (float64, error) {
	s := strings.TrimSpace(vals[name])
	if s == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("%s 要是个数字，收到 %q（**不静默当默认值** —— 那会算出你没要的结果）", label, s)
	}
	return f, nil
}

// analyzeParamSync 算完整段（只读 + 计算），**一个字节都不写**。
func analyzeParamSync(ctx context.Context, rd ReadFunc, vals map[string]string) (*paramSyncPlanData, error) {
	srcParam := strings.ToLower(strings.TrimSpace(value(vals, "srcParam", "")))
	dstParam := strings.ToLower(strings.TrimSpace(value(vals, "dstParam", "")))
	if srcParam == "" {
		return nil, fmt.Errorf("要指定一个**源参数**（跟着它变）")
	}
	if dstParam == "" {
		dstParam = srcParam
	}
	mode := value(vals, "mode", "add")
	if mode == "normalize" { // 兼容旧值：覆盖 + 换算
		mode = "overwrite"
		vals["mapRange"] = "1"
	}
	if mode != "add" && mode != "overwrite" {
		return nil, fmt.Errorf("mode 认不出：%q（只能 add / overwrite）", mode)
	}
	mapRange := truthy(value(vals, "mapRange", ""))
	scale, err := validateFloat(vals, "scale", 1, "数值倍率")
	if err != nil {
		return nil, err
	}
	offset, err := validateFloat(vals, "offset", 0, "数值偏移")
	if err != nil {
		return nil, err
	}

	// ① 片段（+ 全部音符）：范围统一由下面自己算 —— 不依赖 `readNoteScope` 的"没选区就整段"回退，
	//    因为那正是这里**最不能容忍**的静默降级（用户点「所选音符范围」却写了整段）。
	sc, err := readNoteScope(ctx, rd, false)
	if err != nil {
		return nil, err
	}
	if sc.ClipUUID == "" {
		return nil, fmt.Errorf("拿不到当前片段的 uuid")
	}

	// ② 花名册（**现读**）+ 参数校验
	ro, err := readParamRoster(ctx, rd, sc.ClipUUID)
	if err != nil {
		return nil, err
	}
	if _, err := pickParam(ro, srcParam); err != nil {
		return nil, err
	}
	if _, err := pickParam(ro, dstParam); err != nil {
		return nil, err
	}
	layers, err := parseLayerList(value(vals, "layers", ""), ro, dstParam)
	if err != nil {
		return nil, err
	}

	// ③ 范围 → 片段内 tick（end 开区间）
	length := sc.End - sc.Begin
	begin, end := int64(0), length
	rangeDesc, barsDesc := fmt.Sprintf("整个片段（片段内 tick 0..%d）", length), ""
	switch value(vals, "range", "clip") {
	case "sel":
		// ⚠️ 这里**显式**读一次编辑器里的音符选区：`readNoteScope` 在"没选中"时会回退成整个片段，
		// 而"我点的是所选音符范围、它却写了整段"是最要命的那种静默降级 ⇒ 没选区就明确报错。
		var sel struct {
			HasSelection bool `json:"hasSelection"`
			Notes        []struct {
				Pos    int64 `json:"pos"`
				EndPos int64 `json:"endPos"`
			} `json:"notes"`
		}
		if err := readJSON(ctx, rd, &sel, "selection", "get", "--scope", "editor"); err != nil {
			return nil, fmt.Errorf("读音符选区（`selection get --scope editor`）：%w", err)
		}
		if !sel.HasSelection || len(sel.Notes) == 0 {
			// ⭐ 用户 2026-10-24：没选中音符 ⇒ 按**整个片段**（JS 的默认语义），不报错
			begin, end = 0, length
			rangeDesc = fmt.Sprintf("没选中音符 ⇒ 整个片段（片段内 tick 0..%d）", length)
			break
		}
		begin, end = sel.Notes[0].Pos, sel.Notes[0].EndPos
		for _, n := range sel.Notes {
			if n.Pos < begin {
				begin = n.Pos
			}
			if n.EndPos > end {
				end = n.EndPos
			}
		}
		rangeDesc = fmt.Sprintf("选中的 %d 个音符盖住的范围（片段内 tick %d..%d）", len(sel.Notes), begin, end)
	case "bars":
		var tpbReport paramViewReport
		tpb := ticksPerBarOf(ctx, rd, &tpbReport)
		b1 := atoiOr(value(vals, "beginBar", "1"), 1)
		b2 := atoiOr(value(vals, "endBar", "1"), 1)
		begin, end, err = barSpan(b1, b2, tpb, sc.Begin, length)
		if err != nil {
			return nil, err
		}
		rangeDesc = fmt.Sprintf("第 %d..%d 小节（片段内 tick %d..%d）", b1, b2, begin, end)
		barsDesc = fmt.Sprintf("%d..%d 小节（1 小节 = %d tick，%s）", b1, b2, tpb, valueOr(tpbReport.Meter, "拍号未知，按 4/4 算"))
	}

	if end-begin < 2 {
		return nil, fmt.Errorf("要写的这一段只有 %d 个 tick —— ACE 的 dense 写要求"+
			"**每一段连续 run 至少 2 个 tick**（`help vocalparam write` 原文），1 个 tick 会被直接拒", end-begin)
	}

	// ④ 源曲线：用户 2026-10-22「v1 应该可以选择包络或实参，包络优先」——
	//   · auto（默认）：先试 `envelope`（乘数层），成功就用它；失败再试 `effective`；
	//   · envelope / effective：按用户指定读，读不到就如实报错（不静默换源）。
	srcWant := strings.TrimSpace(value(vals, "srcLayer", "auto"))
	if srcWant != "auto" && srcWant != "envelope" && srcWant != "effective" {
		return nil, fmt.Errorf("srcLayer 只认 auto / envelope / effective（收到 %q）", srcWant)
	}
	srcFrom := "effective"
	var srcPts []*float64
	var srcErr error
	if srcWant == "auto" || srcWant == "envelope" {
		envShape := layerShapeOf(ro, srcParam, "envelope")
		if _, pts, e := readTicksLayer(ctx, rd, sc.ClipUUID, srcParam, "envelope", envShape, begin, end); e == nil {
			srcPts, srcFrom, srcErr = pts, "envelope", nil
		} else {
			srcErr = e
		}
	}
	if srcPts == nil && srcWant != "envelope" {
		shape := layerShapeOf(ro, srcParam, "effective")
		_, srcPts, srcErr = readTicksLayer(ctx, rd, sc.ClipUUID, srcParam, "effective", shape, begin, end)
	}
	if srcPts == nil && srcWant == "envelope" {
		return nil, fmt.Errorf("源参数 %s 读不到 `envelope`（v2 只有 user；也有参数没有包络层）⇒ "+
			"把「源读哪一层」改成「自动 / 实参」。原始错误：%v", srcParam, srcErr)
	}
	err = srcErr
	if srcPts == nil && err != nil {
		// 退路：源参数自己第一个可写的 dense 层（例如 v2 的 `dynamic` 只有 user、没有 merge）
		avail := writableAnyLayers(ro, srcParam)
		if len(avail) == 0 {
			// ⚠️ 真机上踩到过：v2 的 `dynamic` **只有 `user` 一层、形状 `points`（锚点）**，
			// 而本工具是"逐 tick 算完整段写回" ⇒ 吃不下锚点形状。
			// 这里必须把**真实形状**摆出来，别只回一句 CLI 的 INVALID_ARG（用户看不懂该换什么）。
			return nil, fmt.Errorf("源参数 %s 没有 `effective`（没有 merge 的 lane 就没有这一块），"+
				"而它可写的层是 **%s** —— 本工具**逐 tick 算完整段写回**，吃不下锚点(points)形状。"+
				"⇒ 换一个 dense 的源参数（air/falsetto/tension/energy 这类有 `user` dense 层的），"+
				"或者用「跨轨复制」页的参数表（那里按 `paramSel` 逐格搬，同样走 dense 这条路）。原始错误：%v",
				srcParam, writableLayerShapes(ro, srcParam), err)
		}
		srcFrom = avail[0]
		_, srcPts, err = readTicksLayer(ctx, rd, sc.ClipUUID, srcParam, avail[0], layerShapeOf(ro, srcParam, avail[0]), begin, end)
		if err != nil {
			// 报错要能指导下一步：说清"没有 effective、可写层是什么形状"，别只甩 CLI 原文
			return nil, fmt.Errorf("源参数 %s 读不到可用曲线：它没有 `effective`（没有 merge 的 lane 就没有这一块），"+
				"可写层是 **%s**。原始错误：%v", srcParam, writableLayerShapes(ro, srcParam), err)
		}
	}
	nSrc := 0
	for _, v := range srcPts {
		if v != nil {
			nSrc++
		}
	}
	if nSrc == 0 {
		return nil, fmt.Errorf("源参数 %s 的 %s 层在**这一段里一个点都没有**（层是空的）⇒ 什么都不写"+ 
			"（不会去清空目标；先把源上画一点，或换 srcLayer=effective）", srcParam, srcFrom)
	}
	if int64(len(srcPts)) != end-begin {
		return nil, fmt.Errorf("源参数 %s 的 %s 只读到 %d 个 tick（要的是 %d 个：片段内 %d..%d）—— "+
			"读数长度对不上就不敢逐 tick 算", srcParam, srcFrom, len(srcPts), end-begin, begin, end)
	}

	// ⑤ 逐层：读目标层这一段 → 算新值 → 查 run 规则
	dstVR := valueRangeOf(ro, dstParam)
	payloads := map[string][]*float64{}
	matches := map[string]string{}
	rep := &paramSyncReport{
		Clip: sc.ClipName, ClipUUID: sc.ClipUUID, Engine: ro.EngineGeneration,
		ScaleName: valueOr(readScale(ro, dstParam), "?"),
		SrcParam:  srcParam, DstParam: dstParam, SamePara: srcParam == dstParam,
		SrcFrom: srcFrom, Layers: layers,
		Range: value(vals, "range", "clip"), RangeDesc: rangeDesc,
		BeginTick: begin, EndTick: end, Ticks: int(end - begin), Bars: barsDesc,
		Scale: scale, Offset: offset, Mode: mode,
		DstRangeMin: dstVR[0], DstRangeMax: dstVR[1],
	}
	for _, p := range ro.Params {
		if p.Available {
			rep.Roster = append(rep.Roster, p.Param)
		}
	}
	f1 := value(vals, "filter", "off")
	v1, err := validateFloat(vals, "filterValue", 0, "筛选阈值")
	if err != nil {
		return nil, err
	}
	mode2 := value(vals, "filter2", "none")
	if mode2 == "none" {
		mode2 = ""
	}
	v2, err := validateFloat(vals, "filter2Value", 0, "第二段阈值")
	if err != nil {
		return nil, err
	}
	lt2 := value(vals, "filter2Mode", "gt") == "lt"
	if f1 == "off" {
		rep.FilterDesc = "不开筛选"
	} else {
		op1 := ">"
		if f1 == "lt" {
			op1 = "≤"
		}
		rep.FilterDesc = fmt.Sprintf("源值 %s %v", op1, v1)
		if mode2 != "" {
			op2 := ">"
			if lt2 {
				op2 = "≤"
			}
			rep.FilterDesc += fmt.Sprintf(" %s 源值 %s %v", strings.ToUpper(mode2), op2, v2)
		}
	}

	shapes := map[string]string{}
	for _, layer := range layers {
		shape := layerShapeOf(ro, dstParam, layer)
		shapes[layer] = shape
		tRead, dstPts, err := readTicksLayer(ctx, rd, sc.ClipUUID, dstParam, layer, shape, begin, end)
		if err != nil {
			return nil, err
		}
		if len(dstPts) != len(srcPts) {
			return nil, fmt.Errorf("目标参数 %s 的 %s 层读到 %d 个 tick、源读到 %d 个 —— 两边的栅格对不上，不敢硬算",
				dstParam, layer, len(dstPts), len(srcPts))
		}
		res := paramSyncLayerResult{Layer: layer}
		// ⭐ 用户 2026-10-24：「值域单独出来」——换算是一个**独立开关**，叠加/覆盖都能用。
		// ⭐ 用户 2026-10-24：「以包络的默认值，真值不考虑」——
		// 包络是**乘数层**（默认 1.0 表示"不改变"），两侧默认值都是 1.0 ⇒ 包络互搬**不做**参数值域缩放
		// （按参数 min..max 缩放会把 1.0 也挪走，正是之前"越搬越奇怪"的原因）。
		// 只有**真值层**（direct/user，绝对覆盖层）才按各自参数的值域做仿射换算。
		srcUse := srcPts
		if mapRange {
			if layer == "envelope" {
				// 默认值 1.0 为基准：新包络 = 1 + (源包络 − 1) × 1 = 源包络（原样）
				rep.Skips = append(rep.Skips, fmt.Sprintf(
					"参数 %s 的 envelope 层：包络以默认值 1.0 为基准 ⇒ 不做值域缩放（真值层才按参数值域换算）", dstParam))
			} else {
				nv, _, _, why := mapValues(srcPts, "range", valueRangeOf(ro, srcParam), dstVR)
				if why != "" {
					return nil, fmt.Errorf("按值域换算失败：%s", why)
				}
				srcUse = nv
			}
		}
		out := make([]*float64, len(dstPts))
		// 同一参数时走另一条公式（原脚本的分支），先算清楚
		isSame := srcParam == dstParam
		// ⭐ 用户 2026-10-24：「套入公式算一下为什么会变数值」——空档按 0 参与是错的。
		// 包络是乘数层，**没画过 = 1.0 = 不改变**；真值层没画过才是 0。
		base := 0.0
		if layer == "envelope" {
			base = 1.0
		}
		for i := range dstPts {
			srcVal, orig := srcUse[i], dstPts[i]
			dstVal := deref(orig, base)
			if orig != nil {
				res.DrawnBefore++
			}
			if srcVal == nil {
				res.NoSource++
				out[i] = orig // 源在这儿没值 ⇒ 跳过，保持原值（空档保持空档）
				continue
			}
			if f1 != "off" && !filterPass(*srcVal, v1, f1 == "lt", mode2, v2, lt2) {
				res.Filtered++
				out[i] = orig
				continue
			}
			// ⭐ **叠加**（AKD 原算法，用户 2026-10-07 当晚更正：「改成叠加，原算法，我刚才想错了」）：
			//   不同参数：新值 = **目标值** + 倍率 × 源值 + 偏移
			//   同一参数：新值 = 倍率 × **目标值** + 偏移（原脚本写到同一个参数时不再加源值那一项）
			// ⚠️ `dstVal` 是**要被改写的那一层**在这个 tick 的原值（空档按 0 参与算 —— 与原脚本一致，
			//   因为 SV 那边 `Parameter.get()` 在没画过的地方也给一个数）。
			v := dstVal + scale*(*srcVal) + offset
			if isSame {
				v = scale*dstVal + offset
			}
			// ⭐ 覆盖：完全不看目标原值（同参数时 src 就是它自己的那条曲线）
			if mode == "overwrite" {
				v = scale*(*srcVal) + offset
			}
			// 目标空档 + 包络算出来正好是默认值 1.0 ⇒ 保持空档（这就是"不变"；真值层不适用，0 是有效值）
			if layer == "envelope" && orig == nil && math.Abs(v-1.0) < 1e-9 {
				out[i] = nil
				continue
			}
			clamped := v
			if v < dstVR[0] {
				clamped = dstVR[0]
			}
			if v > dstVR[1] {
				clamped = dstVR[1]
			}
			if math.Abs(clamped-v) > 1e-12 {
				res.Clamped++
			}
			out[i] = &clamped
			if orig == nil || math.Abs(clamped-*orig) > 1e-9 {
				res.Changed++
			}
		}
		// ⛔ 发之前自己先查 run 规则（1 tick 的 run 会被 ACE 直接拒）
		if idx, kind, bad := checkDenseRuns(out); bad {
			return nil, fmt.Errorf("算出来的这一段里，第 %d 个 tick 起有一段**只有 1 个 tick** 的%s —— "+
				"ACE 的 dense 写要求每段连续 run 至少 2 个 tick（`help vocalparam write` 原文），"+
				"这样发过去会被直接拒。多半是「筛选」把相邻的 tick 筛出了不同的花样（值/空档交替）"+
				"⇒ 关掉筛选、或把条件调宽、或换个范围，再跑一次", begin+int64(idx), kind)
		}
		payloads[layer] = out
		matches[layer] = tRead.Fingerprint
		rep.Results = append(rep.Results, res)
	}

	rep.Stats = paramSyncStats{
		SrcMin: minOf(srcPts), SrcMax: maxOf(srcPts),
		NewMin: minOfAll(payloads), NewMax: maxOfAll(payloads),
	}
	// 前后样本（各 4 个，别把几十万个数倒出来）
	first := layers[0]
	if len(payloads[first]) > 8 {
		rep.SampleBefore = append(append([]*float64{}, srcPts[0:4]...), srcPts[len(srcPts)-4:]...)
		rep.SampleAfter = append(append([]*float64{}, payloads[first][0:4]...), payloads[first][len(payloads[first])-4:]...)
	} else {
		rep.SampleBefore = append([]*float64{}, srcPts...)
		rep.SampleAfter = append([]*float64{}, payloads[first]...)
	}
	for _, layer := range layers {
		rep.Calls = append(rep.Calls, fmt.Sprintf(
			"vocalparam write --clip-uuid %s --param %s --layer %s --pos-begin %d --points @<临时文件>   （%d 个 tick）",
			sc.ClipUUID, dstParam, layer, begin, int(end-begin)))
	}
	return &paramSyncPlanData{rep: rep, clip: sc.ClipUUID, dst: dstParam, begin: begin,
		payload: payloads, match: matches, shapes: shapes}, nil
}

func minOfAll(m map[string][]*float64) float64 {
	mn := math.Inf(1)
	for _, ps := range m {
		if v := minOf(ps); v < mn {
			mn = v
		}
	}
	if math.IsInf(mn, 1) {
		return 0
	}
	return mn
}

func maxOfAll(m map[string][]*float64) float64 {
	mx := math.Inf(-1)
	for _, ps := range m {
		if v := maxOf(ps); v > mx {
			mx = v
		}
	}
	if math.IsInf(mx, -1) {
		return 0
	}
	return mx
}

func valueRangeOf(ro *paramRoster, param string) [2]float64 {
	for _, p := range ro.Params {
		if p.Param == param {
			return [2]float64{p.ValueRange.Min, p.ValueRange.Max}
		}
	}
	return [2]float64{0, 1}
}

func readScale(ro *paramRoster, param string) string {
	for _, p := range ro.Params {
		if p.Param == param {
			return p.Scale
		}
	}
	return ""
}

func valueOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func deref(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}

func minOf(ps []*float64) float64 {
	m := math.Inf(1)
	for _, p := range ps {
		if p != nil && *p < m {
			m = *p
		}
	}
	if math.IsInf(m, 1) {
		return 0
	}
	return m
}

func maxOf(ps []*float64) float64 {
	m := math.Inf(-1)
	for _, p := range ps {
		if p != nil && *p > m {
			m = *p
		}
	}
	if math.IsInf(m, -1) {
		return 0
	}
	return m
}

func paramSyncNote(rep *paramSyncReport, dry bool) string {
	var b strings.Builder
	if dry {
		b.WriteString("干跑：一个字节都没写。")
	} else {
		b.WriteString("已写入。")
	}
	fmt.Fprintf(&b, " 片段「%s」（引擎 %s）。%s 的**实参(%s)** ⇢ %s 的 %s，范围 %s，共 **%d** 个 tick。",
		rep.Clip, rep.Engine, rep.SrcParam, rep.SrcFrom, rep.DstParam,
		strings.Join(rep.Layers, " + "), rep.RangeDesc, rep.Ticks)
	changed, filtered, clamped, noSrc, drawn := 0, 0, 0, 0, 0
	for _, r := range rep.Results {
		changed += r.Changed
		filtered += r.Filtered
		clamped += r.Clamped
		noSrc += r.NoSource
		drawn += r.DrawnBefore
	}
	fmt.Fprintf(&b, " 会改 **%d** 个 tick", changed)
	if filtered > 0 {
		fmt.Fprintf(&b, "（%d 个被筛掉、保持原值）", filtered)
	}
	if noSrc > 0 {
		fmt.Fprintf(&b, "（%d 个源上没值、跳过）", noSrc)
	}
	if clamped > 0 {
		fmt.Fprintf(&b, "（%d 个被夹到 %s 的值域 %g..%g —— ACE 对 vocalparam 越界是**直接拒**、不 clamp，"+
			"所以这里先夹好再发）", clamped, rep.DstParam, rep.DstRangeMin, rep.DstRangeMax)
	}
	b.WriteString("。")
	if drawn == 0 && len(rep.Results) > 0 {
		fmt.Fprintf(&b, " ⚠️ 目标层本来就是**这一段整段未画**（0/%d 个 tick 有值）⇒ 写下去等于把这段变成**显式覆盖**："+
			"原来「没画过」、之后「画成了这些值」——想清再写。", rep.Ticks)
	}
	fmt.Fprintf(&b, " 叠加公式：")
	if rep.SamePara {
		fmt.Fprintf(&b, "同一参数 新值 = %g × 目标值 + %g（源=目标：目标值就是它自己的实参）。", rep.Scale, rep.Offset)
	} else {
		fmt.Fprintf(&b, "不同参数 新值 = 目标值 + %g × 源值 + %g。", rep.Scale, rep.Offset)
	}
	b.WriteString(" ⚠️ 写只**替换这一段**，范围外不动；写进去的值**读回来会变**（f32 精度）⇒ 别按写入值逐位比对。")
	b.WriteString(" ⚠️ 官方提醒：dense 写的**边缘 tick 可能漏**（一段 run 的首尾各压在引擎拿到的 span 边界上）⇒ 真要准就写宽一点、写完读回核对。")
	return b.String()
}

func paramSyncPlan(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	pd, err := analyzeParamSync(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	pd.rep.DryRun = true
	pd.rep.Note = paramSyncNote(pd.rep, true)
	return pd.rep, nil
}

func paramSyncApply(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	pd, err := analyzeParamSync(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	rep := pd.rep
	for _, layer := range rep.Layers {
		var werr error
		if pd.shapes[layer] == "points" {
			anchors := denseToAnchors(pd.payload[layer], pd.begin)
			if len(anchors) == 0 {
				// 全空档 ⇒ 没有可落的值；ACE 会拒空 points，我们也**绝不**用它去清空目标
				rep.Did = append(rep.Did, fmt.Sprintf("参数 %s 的 %s 层这一段全是空档 ⇒ 没写（不会清空目标）", pd.dst, layer))
				continue
			}
			werr = writePointsLayer(ctx, rd, pd.clip, pd.dst, layer, anchors, pd.match[layer])
		} else {
			werr = writeDenseLayer(ctx, rd, pd.clip, pd.dst, layer, pd.begin, pd.payload[layer], pd.match[layer])
		}
		if err := werr; err != nil {
			rep.Note = fmt.Sprintf("写 %s 层失败（%v）—— **这一层一个字节都没写**。"+
				"（前面已经写成功的层**不会回滚**，ACE 每次写是独立撤销条目；用顶栏「撤回这一批」可以把写过的退回来。）",
				layer, err)
			return rep, nil
		}
		rep.Did = append(rep.Did, fmt.Sprintf("参数 %s 的 %s 层写入 %d 个 tick（pos-begin %d）",
			pd.dst, layer, len(pd.payload[layer]), pd.begin))
	}
	rep.Note = paramSyncNote(rep, false)
	return rep, nil
}
// layerShapeOf 查某参数某层的形状（"dense" / "points"）；查不到返回空串。
func layerShapeOf(ro *paramRoster, param, layer string) string {
	for _, p := range ro.Params {
		if p.Param != param {
			continue
		}
		for _, l := range p.Layers {
			if l.Layer == layer {
				return l.Shape
			}
		}
	}
	return ""
}

// sampleAnchorsPerTick 把锚点 `[[tick, value|null], …]` 采样成**逐 tick** 的 `[]*float64`（begin 起、end 开区间）。
// 两个数字锚点之间线性插值；`null` 锚点是空档标记（那一段保持空档），照 ACE 的插值语义。
func sampleAnchorsPerTick(anchors [][]any, begin, end int64) []*float64 {
	type pt struct {
		tick int64
		val  *float64
	}
	pts := make([]pt, 0, len(anchors))
	for _, a := range anchors {
		if len(a) != 2 {
			continue
		}
		tick, ok := toInt64(a[0])
		if !ok {
			continue
		}
		var v *float64
		if a[1] != nil {
			if f, ok := toFloat64(a[1]); ok {
				v = &f
			}
		}
		pts = append(pts, pt{tick, v})
	}
	out := make([]*float64, end-begin)
	valAt := func(tick int64) *float64 {
		// 找左右锚点
		var left, right *pt
		for i := range pts {
			if pts[i].tick <= tick {
				left = &pts[i]
			}
			if pts[i].tick >= tick {
				right = &pts[i]
				break
			}
		}
		if left == nil {
			if right != nil {
				return right.val
			}
			return nil
		}
		if left.val == nil {
			return nil
		}
		if right == nil || right.tick == left.tick || right.val == nil {
			return left.val
		}
		ratio := float64(tick-left.tick) / float64(right.tick-left.tick)
		v := *left.val + (*right.val-*left.val)*ratio
		return &v
	}
	for i := int64(0); i < end-begin; i++ {
		out[i] = valAt(begin + i)
	}
	return out
}

// denseToAnchors 把逐 tick 的值压成锚点：只在**值变化/空档开始**处落点，另加首尾。
//
// ⚠️ 上限（2026-10-24 实测）：源是 points 层时采样出来的值**每个 tick 都在变**，逐变化落点会产出
// 十几万个锚点 ⇒ `vocalparam write --points` 30 秒超时。这里做**抽稀**：锚点数上限 maxAnchors，
// 超了就按步长采样（并保留首尾），曲线形状肉眼无差别。
func denseToAnchors(payload []*float64, begin int64) []any {
	const maxAnchors = 1500
	first, last := -1, -1
	for i, v := range payload {
		if v != nil {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return nil // 全空档 ⇒ 没东西可写（调用方会跳过，绝不用它去清空）
	}
	// ① 先按"变化超过 2e-3"落点（保留形状）
	out := make([]any, 0, 256)
	var prev *float64
	for i := first; i <= last; i++ {
		v := payload[i]
		switch {
		case v == nil:
			// ⛔ 不写 gap marker（[t, nil]）：ACE 只在"层本来就有空档"时接受它，
			// 而逐 tick 采样出来的目标层通常是满的 ⇒ `entry N is a gap marker…` 直接拒（2026-10-24 实测）。
			// 空档在这里当"沿用上一个值"（曲线插值），不显式清空。
			prev = nil
		case prev == nil, math.Abs(*v-*prev) > 0.002, i == last:
			out = append(out, []any{begin + int64(i), *v})
			prev = v
		}
	}
	if len(out) <= maxAnchors {
		return out
	}
	// ② 还是太多（源是 points 层逐 tick 采样时常见）⇒ 按步长抽稀
	span := last - first + 1
	stride := (span + maxAnchors - 2) / (maxAnchors - 1)
	out = out[:0]
	for i := first; i <= last; i += stride {
		if v := payload[i]; v != nil {
			out = append(out, []any{begin + int64(i), *v})
		}
	}
	if v := payload[last]; v != nil && toInt64Any(out[len(out)-1]) != begin+int64(last) {
		out = append(out, []any{begin + int64(last), *v})
	}
	return out
}

func toInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case json.Number:
		n, err := x.Int64()
		return n, err == nil
	case int64:
		return x, true
	case int:
		return int64(x), true
	}
	return 0, false
}
func toInt64Any(v any) int64 { if t, ok := toInt64(v); ok { return t }; return -1 }
func toFloat64(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	}
	return 0, false
}
// readTicksLayer 按层的**形状**读成逐 tick 值（dense 直读；points 读锚点后采样）。
func readTicksLayer(ctx context.Context, rd ReadFunc, clipUUID, param, layer, shape string, begin, end int64) (*vocalLayerRead, []*float64, error) {
	if shape == "points" {
		vr, anchors, err := readPointsLayer(ctx, rd, clipUUID, param, layer, begin, end)
		if err != nil {
			return nil, nil, err
		}
		return &vocalLayerRead{Fingerprint: vr.Fingerprint}, sampleAnchorsPerTick(anchors, begin, end), nil
	}
	return readLayerRange(ctx, rd, clipUUID, param, layer, begin, end)
}

// readBaselineTicks 读 **baseline**（合成默认层）。它 access=read-only，走不了 readLayerRange（那条要求
// read-write），但读是允许的 —— 这里直接读 dense 值，给「包络按 baseline 归一」用。
func readBaselineTicks(ctx context.Context, rd ReadFunc, clipUUID, param string, begin, end int64) []*float64 {
	args := []string{"vocalparam", "read", "--clip-uuid", clipUUID, "--param", param, "--layer", "baseline"}
	if begin >= 0 && end > begin {
		args = append(args, "--range-begin", strconv.FormatInt(begin, 10), "--range-end", strconv.FormatInt(end, 10))
	}
	var out vocalLayerRead
	if err := readJSON(ctx, rd, &out, args...); err != nil {
		return nil
	}
	for _, l := range out.Layers {
		if l.Layer == "baseline" {
			return l.Points
		}
	}
	return nil
}
