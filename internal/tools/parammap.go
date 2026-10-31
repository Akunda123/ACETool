package tools

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// 参数层搬运的**公共判定**（用户 2026-10-19：「合并」）。
//
// 背景：跨轨复制 / 轨内复制里的「参数那一半」（`xcopy.notes` / `xcopy.within`，页面那张参数表）
// 原来**一遇跨引擎世代就整段跳过**；而临时另起的独立工具 `xcopy.params` 有一套"按值域换算 + 允许跨代 +
// 层按角色配对"。用户点明"这不是一个功能吗"⇒ 两边合并：能力搬到这里一处，`xcopy.params` 下线。
//
// 四条硬口径（照抄真机踩出来的规矩）：
//  1. **空档（nil）永远是空档** —— 不参与换算、也不会变成 0；
//  2. 换算只按**两侧现读的 `valueRange`** 做线性映射，不猜任何"经验倍率"；
//  3. 换算结果**夹进目标值域**并计数（ACE 越界一律 `INVALID_ARG`、不替它 clamp）；
//  4. 层名随世代变（v1 `direct`/`envelope`，v2 `user`）⇒ 目标没有同名层时按**层的角色**配对。

// paramMapModeOf 读「数值映射」：`range`（按值域换算，默认）或 `none`（原值照搬）。
func paramMapModeOf(vals map[string]string) (string, error) {
	m := strings.ToLower(value(vals, "map", "range"))
	if m != "range" && m != "none" {
		return "", fmt.Errorf("「数值映射」只认 `range`（按值域换算）或 `none`（原值照搬），收到 %q", m)
	}
	return m, nil
}

// layerRoleOf 取某一层的角色（v1：`direct`=override、`envelope`=multiplier）。
// 跨代时层名会变，但**角色**是同一个意思 ⇒ 用它来配对。
func layerRoleOf(ro *paramRoster, param, layer string) string {
	for _, p := range ro.Params {
		if p.Param != param {
			continue
		}
		for _, l := range p.Layers {
			if l.Layer == layer {
				return l.Role
			}
		}
	}
	return ""
}

// resolveDstLayer 在目标花名册里挑一个能落的层。返回 (层名, 说明)。
// 顺序：① 与源同名 ② 按**角色**配对（v1 `direct` ↔ v2 `user`）③ 第一个可写层。
// 层名挑不出来 ⇒ 返回空串 + 说明（调用方如实跳过那一格）。
func resolveDstLayer(dstRo *paramRoster, param, srcLayer, srcRole string) (string, string) {
	avail := writableAnyLayers(dstRo, param)
	if len(avail) == 0 {
		return "", fmt.Sprintf("参数 %s 在**目标**上没有可写的 dense 层 ⇒ 这一格跳过", param)
	}
	for _, l := range avail {
		if l == srcLayer {
			return l, ""
		}
	}
	if srcRole != "" {
		for _, l := range avail {
			if layerRoleOf(dstRo, param, l) == srcRole {
				return l, fmt.Sprintf("参数 %s：目标上没有 %q 这个层（可写的是 %s）⇒ 按**层的角色**（%s）用了 %q",
					param, srcLayer, strings.Join(avail, "、"), srcRole, l)
			}
		}
	}
	return avail[0], fmt.Sprintf("参数 %s：目标上没有 %q 这个层（可写的是 %s）⇒ 用了第一个可写层 %q",
		param, srcLayer, strings.Join(avail, "、"), avail[0])
}

// mapValues 把源层的一段值换算到目标的值域上。
//
//	`range`：`v' = dLo + (v − sLo) × (dHi − dLo) / (sHi − sLo)`
//	`none` ：原值照搬（不做任何换算）
//
// 返回的 examples 是前 3 个"真值"的对照（源值 → 换算后的值），给报告与说明用；
// why 非空 = 这一格算不了（值域退化等），调用方按"跳过并说明"处理。
func mapValues(src []*float64, mode string, srcRange, dstRange [2]float64) (out []*float64, clamped int, examples [][2]float64, why string) {
	sLo, sHi := srcRange[0], srcRange[1]
	dLo, dHi := dstRange[0], dstRange[1]
	if mode == "range" {
		if sHi <= sLo {
			return nil, 0, nil, fmt.Sprintf("源那一侧的值域是 %.4g…%.4g（跨度 ≤ 0）⇒ 没法按值域换算；"+
				"要么改用「原值照搬」，要么换一个参数", sLo, sHi)
		}
		if dHi <= dLo {
			return nil, 0, nil, fmt.Sprintf("目标那一侧的值域是 %.4g…%.4g（跨度 ≤ 0）⇒ 没法换算", dLo, dHi)
		}
	}
	out = make([]*float64, len(src))
	for i, p := range src {
		if p == nil {
			continue // 空档原样保留（不带值、不写 0）
		}
		v := *p
		if mode == "range" {
			v = dLo + (v-sLo)*(dHi-dLo)/(sHi-sLo)
		}
		if v < dLo {
			v, clamped = dLo, clamped+1
		} else if v > dHi {
			v, clamped = dHi, clamped+1
		}
		q := v
		out[i] = &q
		if len(examples) < 3 {
			examples = append(examples, [2]float64{*p, v})
		}
	}
	return out, clamped, examples, ""
}

// ---------- dynamic → 包络（用户 2026-10-19 的跨代约定）----------

// dynEnvCoef 是「dynamic → 包络」的一条：把 dynamic 摊到哪个参数的 `envelope` 层、系数多少。
type dynEnvCoef struct {
	Param string
	Gain  float64
}

// dynEnvEqSpaces 是「`=` 两侧的空白」——先吃掉再切分，这样 `air = 1` 也算合法。
var dynEnvEqSpaces = regexp.MustCompile(`\s*=\s*`)

// ---------- 「只搬编辑过的位置」：把曲线切成非空档的连续段 ----------

// denseRun 是一段**非空档**的连续 tick（`Begin` 是 clip-local tick）。
type denseRun struct {
	Begin int64
	Pts   []*float64
}

// denseRuns 把一段逐 tick 的值切成"非空档"的连续段（用户 2026-10-20：「没编辑过的位置不要轻易复制」）。
//
// ⛔ 为什么必须切段再写：`vocalparam write` 是**替换这一段**，载荷里的 `null` 会**清掉**目标那一段。
// 整段写过去就等于把目标没画过的地方也一起清空 —— 那正是不该做的事。
// ⚠️ 少于 `minTicks`（ACE 要求每段连续 run ≥2 tick）的段会被丢掉，返回值里报一共丢了多少 tick。
func denseRuns(pts []*float64, posBegin int64, minTicks int) (runs []denseRun, dropped int) {
	if minTicks < 2 {
		minTicks = 2
	}
	i := 0
	for i < len(pts) {
		if pts[i] == nil {
			i++
			continue
		}
		j := i
		for j < len(pts) && pts[j] != nil {
			j++
		}
		if j-i >= minTicks {
			seg := make([]*float64, j-i)
			copy(seg, pts[i:j])
			runs = append(runs, denseRun{Begin: posBegin + int64(i), Pts: seg})
		} else {
			dropped += j - i
		}
		i = j
	}
	return runs, dropped
}

// userLayerOf 说"这个参数的**用户实参层**叫什么"：v1 是 `direct`、v2 是 `user`（都是 read-write 覆盖层）。
// 拿不到（比如只有 envelope 的 `formant`）⇒ 空串，调用方如实跳过。
func userLayerOf(ro *paramRoster, param string) string {
	have := writableAnyLayers(ro, param)
	for _, cand := range []string{"direct", "user"} {
		if containsStr(have, cand) {
			return cand
		}
	}
	return ""
}

// parseDynEnvCoefs 解析页面上的 `dynEnv`：`falsetto=0.5, tension=-0.3`。
// 分隔符认 `,` `;` 和空白；空串 ⇒ 没配（这一项不做）。
func parseDynEnvCoefs(raw string) ([]dynEnvCoef, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	// 先把 `=` 两侧的空白吃掉（`air = 1` 要认），再按分隔符切开。
	clean := dynEnvEqSpaces.ReplaceAllString(raw, "=")
	fields := strings.FieldsFunc(clean, func(r rune) bool {
		return r == ',' || r == ';' || r == '，' || r == '；' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	var out []dynEnvCoef
	for _, f := range fields {
		kv := strings.SplitN(f, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("「dynamic → 包络」要写成 `参数=系数`（如 `falsetto=0.5, tension=-0.3`），看不懂 %q", f)
		}
		name := strings.ToLower(strings.TrimSpace(kv[0]))
		g, err := strconv.ParseFloat(strings.TrimSpace(kv[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("「dynamic → 包络」里 %q 的系数不是数字：%w", f, err)
		}
		out = append(out, dynEnvCoef{Param: name, Gain: g})
	}
	return out, nil
}

// sampleAnchors 把锚点（`[[tick, value], …]`，tick 可以是小数）线性插值成 `[begin, end)` 每 tick 一个值。
// 第一点之前 / 最后一点之后**平推**（与 ACE 自己"锚点外保持平"的口径一致）。
func sampleAnchors(anchors [][2]float64, begin, end int64) []float64 {
	n := int(end - begin)
	if n <= 0 {
		return nil
	}
	out := make([]float64, n)
	if len(anchors) == 0 {
		return out
	}
	j := 0
	for i := 0; i < n; i++ {
		t := float64(begin) + float64(i)
		for j+1 < len(anchors) && anchors[j+1][0] <= t {
			j++
		}
		if t <= anchors[0][0] {
			out[i] = anchors[0][1]
			continue
		}
		if j+1 >= len(anchors) {
			out[i] = anchors[len(anchors)-1][1]
			continue
		}
		a, b := anchors[j], anchors[j+1]
		if b[0] <= a[0] {
			out[i] = b[1]
			continue
		}
		k := (t - a[0]) / (b[0] - a[0])
		out[i] = a[1] + k*(b[1]-a[1])
	}
	return out
}

// dynEnvelopeValues 把一段 dynamic 采样（−1…1，0 = 不动）摊成 envelope 乘数：`1 + k·dyn`，
// 再夹进目标参数的值域（`envelope` 是乘数层，v1 tension 是 0.3…2.5、falsetto 0.3…3 这种）。
// 返回夹边计数（ACE 越界会直接拒写，所以这里必须夹，并且要如实报）。
func dynEnvelopeValues(dyn []float64, gain, lo, hi float64) (out []*float64, clamped int) {
	out = make([]*float64, len(dyn))
	for i, d := range dyn {
		// ⭐ 用户 2026-10-25：「让 dyn-1，以默认值 0 为基准」——
		// v2 的 dynamic 默认值是 **1**（1 = 不动），所以先减 1：dyn 1.1 → 0.1、1.5 → 0.5、0.5 → -0.5，
		// 再乘系数叠到 v1 的包络上：envelope = 1 + k × (dynamic - 1)
		v := 1 + gain*(d-1)
		if v < lo {
			v, clamped = lo, clamped+1
		} else if v > hi {
			v, clamped = hi, clamped+1
		}
		q := v
		out[i] = &q
	}
	return out, clamped
}

// paramMapNote 把"这次怎么换算的"写成人话（进报告的说明区）。
// 跨代时**必须说出来**：换算只对齐值域，对齐不了"语义"。
func paramMapNote(param, mode string, srcRange, dstRange [2]float64, examples [][2]float64, clamped int, crossGen bool) string {
	var b strings.Builder
	if mode == "range" {
		fmt.Fprintf(&b, "参数 %s：按值域换算 %.4g…%.4g → %.4g…%.4g", param, srcRange[0], srcRange[1], dstRange[0], dstRange[1])
		if len(examples) > 0 {
			parts := make([]string, 0, len(examples))
			for _, e := range examples {
				parts = append(parts, fmt.Sprintf("%.4g→%.4g", e[0], e[1]))
			}
			b.WriteString("（例：" + strings.Join(parts, "、") + "）")
		}
		if clamped > 0 {
			fmt.Fprintf(&b, "；有 %d 个值超出目标值域，已夹到边界", clamped)
		}
	} else {
		fmt.Fprintf(&b, "参数 %s：原值照搬（没换算）", param)
		if clamped > 0 {
			fmt.Fprintf(&b, "；有 %d 个值超出目标值域，已夹到边界", clamped)
		}
	}
	if crossGen {
		b.WriteString("。⚠️ 这次是**跨引擎世代**：换算只按值域对齐，同名参数在两代并不是同一个东西 —— 写完请听一遍")
	}
	return b.String()
}
