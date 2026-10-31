package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"acetool/internal/acep"
	"acetool/internal/cbor"
)

// 文件路线（`.acep`）—— 三个工具：inspect（判壳）/ dump（解析）/ patch（回填）。
//
// 定位来自技能 `acep` §0：**CLI 能做的永远走 CLI**（撤销栈 / 指纹 / 原子拒写，文件改三样都没有）。
// 文件路线只用在「CLI 没开放的能力」上，当前已知唯一一个是**人声音高曲线**。
//
// 技能 **v1.2.3 §1.0** 的三种壳：① `ACEP2`（在用，CBOR payload）② 历史壳一（有 `compressMethod`，
// 内层 JSON 文本，改法走 set-json）③ 历史壳二（没有 `compressMethod`，加密 ⇒ 不碰）。
// ⚠️ 判据是**有没有 `compressMethod`**，不是版本号。
func init() {
	register(&Tool{
		ID: "acep.inspect", Category: "工程",
		Title: "① 读 .acep 工程文件（只读）",
		Desc: "判壳 + 校验长度字段 + 解 zstd payload + 报首字节编码；`ACEP2` 与历史壳一都读得出，" +
			"历史壳二（加密）只认出、**不尝试解密**。带往返自检（重压 → 解回 → 逐字节比对）。" +
			"⚠️ 只读：不写、不动你的工程，也不碰 ACE 里正开着的那个。",
		Kind:   KindRead,
		Source: "技能 acep v1.2.3（.acep 格式与安全改写）",
		Params: []Param{
			textParam("path", "工程文件路径", "", `C:\…\名字\名字.acep`,
				"绝对路径，指到 .acep 文件本身（填文件夹会帮你把里面的 .acep 列出来）"),
			checkParam("roundTrip", "往返自检（重压 → 解回 → 逐字节比对）", true),
		},
		Inspect: acepInspect,
	})

	register(&Tool{
		ID: "acep.dump", Category: "工程",
		Title: "② 解析 .acep 内容（只读）",
		Desc: "把 payload **解成树**：`ACEP2` 走 CBOR（带字节偏移），历史壳一走 JSON。" +
			"不给路径 ⇒ 报顶层结构（键、各形状计数、真轨道按 `type` 分档）；给路径 ⇒ 取那个值。" +
			"⚠️ 只读。CBOR 的两个坑（half float、double 别二次消费）都有测试卡住。",
		Kind:   KindRead,
		Source: "技能 acep v1.2.3 §2（payload 是 CBOR / §2.5 文档模型）",
		Params: []Param{
			textParam("path", "工程文件路径", "", `C:\…\名字\名字.acep`, "绝对路径"),
			textParam("valuePath", "取值路径（留空 = 只看顶层概要）", "", `tracks[0].name 或 tracks[0].patterns[0].notes[0].syllable`,
				"语法 `a.b[0].c`（`$` 前缀可有可无）。键是整数时也照写，如 `phonemeTimings.0`"),
			numParam("listMax", "列表最多列几项", "8"),
		},
		Inspect: acepDump,
	})

	register(&Tool{
		ID: "acep.patch", Category: "工程",
		Title: "③ 回填 .acep（写 · 默认干跑）",
		Desc: "按路径改一处**文本**，然后重建整个文件：`ACEP2` 走 CBOR 原位覆盖（只改那个 text 自己的长度前缀）；" +
			"历史壳一走 set-json（整树重 stringify + 重 zstd + 重 base64）。" +
			"**干跑默认**：勾着只出自检报告、一个字节都不写；取消勾选才落盘，而且**只写副本**" +
			"（按工程包布局产出 `<输出目录>\\<名字>\\…`），源文件一个字节都不动。",
		Kind:   KindWrite,
		Source: "技能 acep v1.2.3 §4 安全写入六步 / §1.0 的 set-json",
		Params: []Param{
			textParam("path", "源 .acep 路径", "", `C:\…\名字\名字.acep`, "绝对路径；**不会被修改**"),
			textParam("valuePath", "要改的路径", "", `tracks[0].name`,
				"只能改**文本**节点；数组批量覆盖请用 CLI 或后续工具"),
			textParam("newText", "新文本", "", "新值", "空串是合法值（等于清空这个词）；填了 `numbers` 就忽略它"),
			textParam("numbers", "新数值（改 float64 数组时用）", "", "0,0,1.5,-0.25",
				"逗号/空白分隔。⚠️ 改的是 `…values` 这种数值数组：**元素个数必须与原来一模一样**（原位覆盖；改长度等于重写外层容器）"),
			textParam("outDir", "输出目录", "", `C:\…\输出目录`, "必填；产出会写成 `<outDir>\\<名字>\\…` 这个工程包"),
			textParam("name", "工程名字（包目录名）", "", "如 MySong_patched", "留空 = 源文件名 + _patched"),
		},
		Plan:  acepPatchPlan,
		Apply: acepPatchApply,
	})
}

// ---------- 路径解析（三个工具共用） ----------

// resolveACEPPath 取 path 参数并做常见误用的友好处理（填了文件夹 / 文件不存在）。
func resolveACEPPath(vals map[string]string) (string, error) {
	p := strings.Trim(strings.TrimSpace(value(vals, "path", "")), `"`)
	if p == "" {
		return "", fmt.Errorf("要给一个 .acep 文件的绝对路径")
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("读不到这个文件：%w", err)
	}
	if st.IsDir() {
		// 常见误用：把工程**文件夹**填进来了。工程是"包"（`<名字>\<名字>.acep` + autosave + Samples），
		// 所以直接帮他把里面的 .acep 找出来。
		inner := filepath.Join(p, filepath.Base(p)+".acep")
		if _, err := os.Stat(inner); err == nil {
			return "", fmt.Errorf("%s 是**文件夹**、不是 .acep 文件 —— 里面的工程文件是：%s", p, inner)
		}
		if m, _ := filepath.Glob(filepath.Join(p, "*.acep")); len(m) > 0 {
			return "", fmt.Errorf("%s 是文件夹；里面的 .acep 有：%s", p, strings.Join(m, "、"))
		}
		return "", fmt.Errorf("%s 是文件夹，里面没有 .acep", p)
	}
	return p, nil
}

// ---------- ① inspect ----------

type acepReport struct {
	Kind          string   `json:"容器"`
	Note          string   `json:"说明"`
	FileSize      int64    `json:"文件大小"`
	HeadLen       uint64   `json:"头长,omitempty"`
	CompressedLen uint64   `json:"压缩长off16,omitempty"`
	RawLen        uint64   `json:"原始长off24,omitempty"`
	LengthOK      *bool    `json:"长度自检,omitempty"`
	Method        string   `json:"compressMethod,omitempty"`
	LegacyFields  []string `json:"旧壳信封字段,omitempty"`
	FrameLen      int      `json:"zstd帧大小"`
	PayloadLen    int      `json:"payload大小"`
	PayloadHead   string   `json:"payload首字节"`
	Millis        int64    `json:"耗时ms"`

	RoundTripLen  int   `json:"重压后帧大小,omitempty"`
	RoundTripMs   int64 `json:"重压耗时ms,omitempty"`
	RoundTripSame *bool `json:"往返逐字节一致,omitempty"`
}

func acepInspect(_ context.Context, _ ReadFunc, vals map[string]string) (any, error) {
	p, err := resolveACEPPath(vals)
	if err != nil {
		return nil, err
	}
	c, err := acep.Open(p)
	if err != nil {
		return nil, err
	}
	rep := acepReport{
		Kind: string(c.Info.Kind), Note: c.Info.Note, FileSize: c.Info.FileSize,
		Method: c.Info.Method, FrameLen: c.Info.FrameLen,
		PayloadLen: c.Info.PayloadLen, PayloadHead: c.Info.PayloadHead,
		Millis: c.Info.Millis,
	}
	if c.Info.Kind == acep.KindACEP2 {
		ok := c.Info.LengthOK
		rep.HeadLen, rep.CompressedLen, rep.RawLen, rep.LengthOK = c.Info.HeadLen, c.Info.CompressedLen, c.Info.RawLen, &ok
	}
	if c.Info.Kind == acep.KindLegacyEncrypted {
		rep.LegacyFields = c.Info.LegacyFields
		rep.FrameLen, rep.PayloadLen, rep.PayloadHead = 0, 0, "（加密旧壳，不碰）"
	}
	if c.Info.Kind == acep.KindLegacyReadable {
		rep.LegacyFields = c.Info.LegacyFields
	}
	if truthy(value(vals, "roundTrip", "1")) && c.Payload != nil {
		n, ms, same, err := acep.RoundTrip(c.Payload)
		if err != nil {
			return nil, fmt.Errorf("往返自检失败：%w", err)
		}
		rep.RoundTripLen, rep.RoundTripMs, rep.RoundTripSame = n, ms, &same
	}
	return rep, nil
}

// ---------- ② dump ----------

type dumpReport struct {
	Shell       string         `json:"容器"`
	PayloadKind string         `json:"内层编码"`
	PayloadSize int            `json:"payload大小"`
	TopKeys     []string       `json:"顶层键,omitempty"`
	Counts      map[string]any `json:"计数,omitempty"`
	ValuePath   string         `json:"取值路径,omitempty"`
	Value       any            `json:"值,omitempty"`
}

func acepDump(_ context.Context, _ ReadFunc, vals map[string]string) (any, error) {
	p, err := resolveACEPPath(vals)
	if err != nil {
		return nil, err
	}
	c, err := acep.Open(p)
	if err != nil {
		return nil, err
	}
	rep := dumpReport{Shell: string(c.Info.Kind), PayloadSize: len(c.Payload)}
	listMax, _ := strconv.Atoi(value(vals, "listMax", "8"))
	if listMax <= 0 {
		listMax = 8
	}
	valuePath := value(vals, "valuePath", "")

	if c.Info.Kind == acep.KindLegacyEncrypted {
		rep.PayloadKind = "（加密壳，不解析）"
		rep.Counts = map[string]any{"信封字段": c.Info.LegacyFields}
		return rep, nil
	}

	// 历史壳一：内层是 JSON 文本
	if c.Info.Kind == acep.KindLegacyReadable {
		rep.PayloadKind = "JSON 文本"
		if valuePath != "" {
			return nil, fmt.Errorf("历史壳一是**内层 JSON**：本工具暂不按路径取值（要改它用 `acep.patch`，那条路已支持 set-json）")
		}
		keys, counts, err := jsonOverview(c.Payload, listMax)
		if err != nil {
			return nil, err
		}
		rep.TopKeys, rep.Counts = keys, counts
		return rep, nil
	}

	// ACEP2：CBOR
	rep.PayloadKind = c.Info.PayloadHead
	root, err := cbor.Decode(c.Payload)
	if err != nil {
		return nil, fmt.Errorf("payload 解不开（应当恰好消费完）：%w", err)
	}
	if root.Kind != cbor.KMap {
		return nil, fmt.Errorf("顶层应当是 map，实得 %s", cbor.Shape(root))
	}
	if valuePath != "" {
		n, err := cbor.Get(root, valuePath)
		if err != nil {
			return nil, err
		}
		rep.ValuePath = valuePath
		rep.Value = describeNode(n, listMax)
		return rep, nil
	}
	rep.TopKeys = cbor.KeyNames(root, 40)
	rep.Counts = cborOverview(root, listMax)
	return rep, nil
}

// describeNode 把一个节点渲染成"人看得懂 + 能验证"的形状。
func describeNode(n *cbor.Node, max int) any {
	switch n.Kind {
	case cbor.KText:
		return n.Text
	case cbor.KBytes:
		return fmt.Sprintf("bytes(%d)", len(n.Bytes))
	case cbor.KUint:
		return n.Uint
	case cbor.KInt:
		return n.Int
	case cbor.KFloat:
		return n.Float
	case cbor.KBool:
		return n.Bool
	case cbor.KNull:
		return nil
	case cbor.KMap:
		out := map[string]any{"__形状": cbor.Shape(n)}
		for i, k := range n.Keys {
			if i >= max {
				out["__还有"] = len(n.Keys) - max
				break
			}
			out[k.KeyString()] = describeNode(n.Values[i], max)
		}
		return out
	case cbor.KArray:
		out := map[string]any{"__形状": cbor.Shape(n)}
		vals := []any{}
		for i, it := range n.Items {
			if i >= max {
				out["__还有"] = len(n.Items) - max
				break
			}
			vals = append(vals, describeNode(it, max))
		}
		out["__前几项"] = vals
		return out
	default:
		return cbor.Shape(n)
	}
}

// cborOverview 顶层结构摘要：键数、几个重要容器的规模、真轨道按 type 分档。
//
// ⚠️ 技能 §2.5 明确提醒：`tracks` 数组里**绝大多数槽位不是真轨道**（`data`/`empty` 是占位，
// 上百个）⇒ 必须**先按 `type` 过滤**，别按索引数轨道。
func cborOverview(root *cbor.Node, max int) map[string]any {
	out := map[string]any{"顶层键数": len(root.Keys)}
	for _, key := range []string{"tracks", "tempo", "timeSignatures", "videoTracks", "chordTrack", "master"} {
		if n := root.Get(key); n != nil {
			out[key] = cbor.Shape(n)
		}
	}
	if tr := root.Get("tracks"); tr != nil && tr.Kind == cbor.KArray {
		byType := map[string]int{}
		patterns, notes := 0, 0
		for _, t := range tr.Items {
			if t.Kind != cbor.KMap {
				continue
			}
			ty := "(无 type)"
			if n := t.Get("type"); n != nil {
				ty = n.Text
			}
			byType[ty]++
			if ps := t.Get("patterns"); ps != nil && ps.Kind == cbor.KArray {
				patterns += len(ps.Items)
				for _, pt := range ps.Items {
					if ns := pt.Get("notes"); ns != nil && ns.Kind == cbor.KArray {
						notes += len(ns.Items)
					}
				}
			}
		}
		out["轨道按 type 分档"] = byType
		out["pattern 总数"] = patterns
		out["音符总数"] = notes
	}
	if v := root.Get("version"); v != nil {
		out["version"] = describeNode(v, max)
	}
	return out
}

func jsonOverview(payload []byte, max int) ([]string, map[string]any, error) {
	var doc map[string]any
	if err := json.Unmarshal(payload, &doc); err != nil {
		return nil, nil, fmt.Errorf("内层不是合法 JSON：%w", err)
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	counts := map[string]any{"顶层键数": len(keys)}
	for _, k := range []string{"tracks", "tempos", "timeSignatures"} {
		if v, ok := doc[k]; ok {
			counts[k] = fmt.Sprintf("%T", v)
		}
	}
	return keys, counts, nil
}

// ---------- ③ patch ----------

// patchReport 是给定变更的一次完整交代。**干跑与真写的差别只在 Written**。
// （字段名必须 ASCII 大写开头：中文标识符在 Go 里算未导出，序列化会变 `{}`。）
type patchReport struct {
	DryRun  bool               `json:"干跑"`
	Source  string             `json:"源文件"`
	Result  *acep.PatchResult  `json:"结果"`
	Written []acep.WrittenFile `json:"落盘,omitempty"`
	Hint    string             `json:"提示,omitempty"`
}

func acepPatchPlan(_ context.Context, _ ReadFunc, vals map[string]string) (any, error) {
	c, res, err := runPatch(vals)
	if err != nil {
		return nil, err
	}
	_ = c
	out := &patchReport{DryRun: true, Source: c.Info.Path, Result: res}
	if !res.AllOK {
		out.Hint = "⚠️ 自检**有没过的** —— 真跑会被拒绝（修好再看，别硬写）。"
	} else {
		out.Hint = "干跑：**一个字节都没写**。取消勾选「干跑」才会落盘（而且只写副本）。"
	}
	return out, nil
}

func acepPatchApply(_ context.Context, _ ReadFunc, vals map[string]string) (any, error) {
	c, res, err := runPatch(vals)
	if err != nil {
		return nil, err
	}
	if !res.AllOK {
		// 自检没过就**不落盘** —— 这是纪律，不是可选。
		failed := []string{}
		for _, ck := range res.Checks {
			if !ck.OK {
				failed = append(failed, ck.Name)
			}
		}
		return nil, fmt.Errorf("自检有 %d 项没过（%s），**拒绝落盘**",
			len(failed), strings.Join(failed, "、"))
	}

	outDir := strings.Trim(strings.TrimSpace(value(vals, "outDir", "")), `"`)
	if outDir == "" {
		return nil, fmt.Errorf("要一个输出目录（outDir）—— 产出会写成 `<outDir>\\<名字>\\…` 这个工程包；源文件不会被改")
	}
	name := strings.TrimSpace(value(vals, "name", ""))
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(c.Info.Path), ".acep") + "_patched"
	}
	made, err := acep.WritePackage(outDir, name, c.RawBytes(), res.Bytes)
	if err != nil {
		return nil, err
	}
	return &patchReport{
		Source: c.Info.Path, Result: res, Written: made,
		Hint: "只写了副本，**源文件一个字节都没动**。下一步：在 ACE 里打开产物读回来复核（技能 §5.1：文件级自检只证明字节对了，证明不了 ACE 认）。",
	}, nil
}

// runPatch 是干跑与真写共用的一步：读源文件 → 算改动 → 重建 → 自检（全在内存里）。
func runPatch(vals map[string]string) (*acep.Container, *acep.PatchResult, error) {
	p, err := resolveACEPPath(vals)
	if err != nil {
		return nil, nil, err
	}
	valuePath := strings.TrimSpace(value(vals, "valuePath", ""))
	if valuePath == "" {
		return nil, nil, fmt.Errorf("要一个「要改的路径」（valuePath），例如 tracks[0].name")
	}
	// 新文本允许为空串（清空），所以这里不能用 value()（它会把空串当"没填"而回落默认）
	newText := vals["newText"]
	// ⭐ 改 **float64 数组**（音高曲线就长这样：`…parameters.pitchDelta[0].values`）。
	// ⚠️ 用 `SetFloat64Array` 做**原位覆盖** ⇒ 元素个数必须与原来**一模一样**
	//   （ACEP2 的 map/array 长度是元素个数，改长度就等于重写外层容器）。
	nums := strings.TrimSpace(vals["numbers"])

	c, err := acep.Open(p)
	if err != nil {
		return nil, nil, err
	}
	if nums != "" {
		vals64, err := parseNumbers(nums)
		if err != nil {
			return nil, nil, err
		}
		res, err := c.PatchFloat64Array(valuePath, vals64)
		if err != nil {
			return nil, nil, err
		}
		return c, res, nil
	}
	res, err := c.PatchText(valuePath, newText)
	if err != nil {
		return nil, nil, err
	}
	return c, res, nil
}

// parseNumbers 解析 `0,0,1.5,-0.25`。
//
// **口径**（写清楚，免得踩）：
//   - 真正的分隔符是 **逗号 / 分号**；**空白只是两侧的 padding**（`1 , 2` 是 2 个数）。
//   - 一段里再有空白也拆开 ⇒ `0 0 1.5` 和 `1, 2 3` 都收。
//   - ⛔ **内部空段要报错，不能默默丢掉**：这条数组是"元素个数必须一模一样"的原位覆盖，
//     要是把 `1,,2` 悄悄收成 2 个数，接着报的就是"个数不符"——用户会去数个数，
//     而真正的问题是中间那个空段。报错要点出**第几段**。
//   - 只容忍**末尾**那一个分隔符（`1,2,` ⇒ 2 个数；生成器很常见，且没有歧义）。
func parseNumbers(s string) ([]float64, error) {
	coarse := splitOnAny(s, ",;")
	// 末尾一个分隔符 ⇒ 容忍
	if n := len(coarse); n > 0 && strings.TrimSpace(coarse[n-1]) == "" {
		coarse = coarse[:n-1]
	}
	out := make([]float64, 0, len(coarse))
	pos := 0
	for i, seg := range coarse {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			return nil, fmt.Errorf("第 %d 段是空的 —— 分隔符连着写了（像 `1,,2`）。"+
				"要是想写 0 就显式写 `0`（这条数组按位置一一对应，空段没法猜）", i+1)
		}
		// 一段里再用空白拆（`0 0 1.5` 与 `1, 2 3`）
		for _, f := range strings.Fields(seg) {
			v, err := strconv.ParseFloat(f, 64)
			if err != nil {
				return nil, fmt.Errorf("第 %d 个数认不出：%q（要写成 `0,0,1.5,-0.25` 这样）", pos+1, f)
			}
			out = append(out, v)
			pos++
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("`numbers` 里一个数都没有")
	}
	return out, nil
}

// splitOnAny 按 seps 里的**任意一个字符**切，并**保留空段**（这样才知道哪里连着写了）。
func splitOnAny(s, seps string) []string {
	isSep := func(r rune) bool { return strings.ContainsRune(seps, r) }
	var out []string
	var cur strings.Builder
	for _, r := range s {
		if isSep(r) {
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteRune(r)
	}
	out = append(out, cur.String())
	return out
}
