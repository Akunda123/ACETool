package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// 参数表格（用户 2026-10-07）：
//
//	「参数展开，列出所有参数，列个表格，勾选，如果是 v1 就实参和包络列两列」
//
// 页面上 `复制参数曲线` 那一行展开成一张表：**每行一个参数**（花名册现读），
// **每列一条曲线**，格子里勾选。列的含义（页面上就写着落点，这里一一对应）：
//
//	实参（effective）= **合并后的绝对值曲线** ⇒ 写目标的 `direct`（真值层）；目标这一代没有
//	                   `direct` 就退 `user`（v2 只有它）。**绝不会写进 `envelope`** ——
//	                   实参是绝对值、包络是乘数（`ace-params` §3），混着写就是换个意思了。
//	包络（envelope） = **乘数层**（围绕 1.0）    ⇒ 写目标的 `envelope`（只有 v1 有这一层）。
//
// 所以 v1 是两列（真值层 + 包络层都在）、v2 只有"实参"一列（`user`）——
// 这不是写死的：**列由目标那一侧花名册里"哪些层可写"推出来**（见 paramPickColumns），
// 引擎换代/声库换源时会自己跟着变。
type paramPick struct {
	Param     string
	Effective bool
	Envelope  bool
}

// paramJob 是"一格"要搬的东西：哪个参数、**读源**的哪一层、**写目标**的哪一层。
//
// ⚠️ `xcopy.notes`（一对片段）与 `xcopy.within`（整条轨、多片段）**共用 `paramJobsOf` 这一处判定** ——
// 两边各写一份的话，同一张表格在两条路上会得出不同的落点。
type paramJob struct {
	Param    string
	SrcLayer string // 源：`effective`（实参）或某个可写层名（v1 的 `envelope`）
	DstLayer string // 目标：`direct`/`user`（实参那格）或 `envelope`（包络那格）
	// SrcRaw=true ⇒ 实参那一格改读**原始 `direct`/`user` 值**（不是 `effective`）。
	// 用途：同一参数**两列都勾**时，包络会单独写一格；实参若还搬 `effective`（已含包络）就会**乘二**
	// ⇒ 这里改成搬原始值，两列合起来正好等价于源上的「实参 × 包络」（用户 2026-10-20：消除乘二）。
	SrcRaw bool
	// EnvToAbs = 跨代把 v1 的包络乘数折算成绝对值（baseline × envelope），写目标的 override 层（v2 user）
	EnvToAbs bool
}

// paramJobsOf 把"这一趟要搬哪些格"算出来（表格路径逐格；老路径每个参数各搬实参到选定层）。
//
// 返回：
//   - jobs   —— 要落笔的格；
//   - table  —— **参数表交上来了吗**（`paramSel` 给了，哪怕 `{}`）。给了就只按表写，
//     一格没勾 ⇒ jobs 为空（调用方如实报"一个格子都没勾"），**绝不退回老路径**；
//   - skips  —— 逐格的跳过说明（放不进 jobs 的那些），调用方收进 `pd.paramSkips`；
//   - err    —— 表本身不合法（列名/参数名不认识）時**当场报错**，不静默丢。
func paramJobsOf(vals map[string]string, sro, dro *paramRoster) (jobs []paramJob, table bool, picks []paramPick, skips []string, err error) {
	picks, table, err = parseParamSel(value(vals, "paramSel", ""), sro)
	if err != nil {
		return nil, table, nil, nil, err
	}
	if table {
		// ⭐ 跨代判定（用户 2026-10-20）：「不跨代应该正常复制，包络复制给包络，实参复制给实参」
		// ⇒ 同代一律**层对层**搬原始值（实参格读 `direct`/`user` 原值，不读 `effective`）；
		// 只有跨代且只勾实参时，才用「用户层当掩码 + effective 当值」把包络的影响折进来。
		crossGen := sro.EngineGeneration != "" && dro.EngineGeneration != "" &&
			sro.EngineGeneration != dro.EngineGeneration
		for _, pk := range picks {
			have := writableAnyLayers(dro, pk.Param)
			if len(have) == 0 {
				skips = append(skips, fmt.Sprintf(
					"参数 %s 在**目标**上没有可写的 dense 层 ⇒ 这一行跳过", pk.Param))
				continue
			}
			if pk.Effective {
				if dst := effectiveDstLayer(have); dst != "" {
					jobs = append(jobs, paramJob{Param: pk.Param, SrcLayer: "effective", DstLayer: dst,
						SrcRaw: !crossGen})
				} else {
					skips = append(skips, fmt.Sprintf(
						"参数 %s 的**实参**列：实参是绝对值，只能落进 `direct`（真值）/`user`，而目标可写的是 %s ⇒ 这一格跳过",
						pk.Param, strings.Join(have, "、")))
				}
			}
			if pk.Envelope {
				// ⭐ 跨代：包络**不单独写**——它会被折进实参列（用户 2026-10-20 定的三条折算规则）。
				if crossGen {
					// ⭐ 用户 2026-10-25：「补回包络 → user」。
					// v1 的 envelope 是**乘数**、v2 的 user 是**绝对值** ⇒ 用 baseline × envelope 折算，
					// 写进目标那一代唯一的 override 层（v2 的 user / v1 的 direct）。
					if dst := effectiityOverrideLayer(have); dst != "" {
						jobs = append(jobs, paramJob{Param: pk.Param, SrcLayer: "envelope", DstLayer: dst,
							EnvToAbs: true})
					} else {
						skips = append(skips, fmt.Sprintf(
							"参数 %s 的**包络**列：跨代且目标没有 override 层（可写的是 %s）⇒ 这一格跳过",
							pk.Param, strings.Join(have, "、")))
					}
					continue
				}
				// ⚠️ 源那一代可能**根本没有 `envelope` 层**（v2 只有 `user`）——
				// 用户 2026-10-19 把「包络」设成默认勾的那一列，跨代时最容易撞上这一条，
				// 所以这里的说明要**直接指路**（勾「实参」那一列），别让他去猜为什么被跳过。
				if !hasAnyLayer(sro, pk.Param, "envelope") {
					skips = append(skips, fmt.Sprintf(
						"参数 %s 的**包络**列：源那一代没有 `envelope` 层（v2 只有 `user`）⇒ 这一格跳过；"+
							"跨代搬请勾「实参」那一列（实参两代都有）", pk.Param))
				} else if containsStr(have, "envelope") {
					jobs = append(jobs, paramJob{Param: pk.Param, SrcLayer: "envelope", DstLayer: "envelope"})
				} else {
					skips = append(skips, fmt.Sprintf(
						"参数 %s 的**包络**列：目标这一代没有 `envelope` 层（可写的是 %s）⇒ 这一格跳过",
						pk.Param, strings.Join(have, "、")))
				}
			}
		}
		return jobs, true, picks, skips, nil
	}

	// ---- 老路径（主面板表单：`params=1` + 可选的 `paramLayers`）----
	var layers []string
	for _, s := range strings.Split(value(vals, "paramLayers", ""), ",") {
		if s = strings.TrimSpace(s); s != "" {
			layers = append(layers, s)
		}
	}
	// ⚠️ 这里**不预校验**层名：每个参数能被写哪些层不一样（v1 的 `formant` 只有 envelope），
	// 统一按参数自己的 `denseWritableLayers` 逐条判（判不过就如实跳过并说明）。
	for _, p := range sro.Params {
		if !p.Available || p.Param == "pitch" || p.Param == "dynamic" {
			continue // pitch 在这个 surface 上不可用；dynamic 走锚点那条路
		}
		have := writableAnyLayers(dro, p.Param)
		if len(have) == 0 {
			skips = append(skips, fmt.Sprintf("参数 %s 没有可写的 dense 层，跳过", p.Param))
			continue
		}
		want := layers
		if len(want) == 0 {
			var lerr error
			want, lerr = parseLayerList("", dro, p.Param)
			if lerr != nil {
				skips = append(skips, fmt.Sprintf("参数 %s 推不出默认要写的层（%v）⇒ 跳过", p.Param, lerr))
				continue
			}
		}
		for _, layer := range want {
			if !containsStr(have, layer) {
				skips = append(skips, fmt.Sprintf(
					"参数 %s 上没有可写的 %s 层（它有 %s）⇒ 这一层跳过", p.Param, layer, strings.Join(have, "、")))
				continue
			}
			jobs = append(jobs, paramJob{Param: p.Param, SrcLayer: "effective", DstLayer: layer})
		}
	}
	return jobs, false, nil, skips, nil
}

// hasAnyLayer 判某个参数在**这一份花名册**上有没有这一层（不看可写/形状）。
// 用途：跨代时"源那一代压根没有 envelope 层"要提前说清，而不是等读失败再报个 CLI 错。
func hasAnyLayer(ro *paramRoster, param, layer string) bool {
	for _, p := range ro.Params {
		if p.Param != param {
			continue
		}
		for _, l := range p.Layers {
			if l.Layer == layer {
				return true
			}
		}
	}
	return false
}

// paramPickColumns 推这张表该有哪些列：**由花名册的可写层决定**，不写死世代名。
//
//	实参列：只要有任何参数能写 `direct` 或 `user`（两代各有一种）就有这一列；
//	包络列：只要有任何参数能写 `envelope`（v1）就有这一列。
func paramPickColumns(ro *paramRoster) (effectiveCol, envelopeCol bool) {
	for _, p := range ro.Params {
		if !p.Available {
			continue
		}
		// 形状无关：ACE 在不同工程/版本里同一层可能报 dense、也可能报 points（2026-10-22 实测）
		have := writableAnyLayers(ro, p.Param)
		if containsStr(have, "direct") || containsStr(have, "user") {
			effectiveCol = true
		}
		if containsStr(have, "envelope") {
			envelopeCol = true
		}
	}
	return effectiveCol, envelopeCol
}

// effectiveDstLayer 说"实参这一格该写进哪个层"：`direct`（真值）优先，没有就 `user`。
// 两个都没有（比如某个参数只有 envelope 可写，v1 的 `formant`）⇒ 返回空串，调用方如实跳过那一格。
func effectiveDstLayer(have []string) string {
	for _, cand := range []string{"direct", "user"} {
		if containsStr(have, cand) {
			return cand
		}
	}
	return ""
}

// parseParamSel 解析页面来的 `paramSel`（JSON 对象：`{"tension":["effective","envelope"],…}`）。
//
// 规矩：
//   - 空串 ⇒ `present=false`（走老路径：`params=1` + `paramLayers`，主面板表单就是那条路）；
//   - **给了但是 `{}`** ⇒ `present=true` + 空 picks ⇒ "表交上来了、一格都没勾"，
//     调用方就该**什么都不写**并如实报出来 —— 绝不能退回老路径（那会变成"所有参数全搬"，
//     与用户在表上看到的一个勾都没有正好相反）；
//   - 名字不在花名册里 / 列名不认识 ⇒ **当场报错**（不静默丢，免得"勾了没动静"）；
//   - 顺序按**花名册顺序**（页面表格的顺序与它一致），报告里的层序也才稳定。
func parseParamSel(raw string, ro *paramRoster) (picks []paramPick, present bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false, nil
	}
	var sel map[string][]string
	if err := json.Unmarshal([]byte(raw), &sel); err != nil {
		return nil, true, fmt.Errorf("`paramSel` 要是 JSON 对象（如 {\"tension\":[\"effective\"]}）：%w", err)
	}
	avail := map[string]bool{}
	for _, p := range ro.Params {
		if p.Available {
			avail[p.Param] = true
		}
	}
	bad := make([]string, 0, len(sel))
	for k := range sel {
		if !avail[k] {
			bad = append(bad, k)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		var names []string
		for _, p := range ro.Params {
			if p.Available {
				names = append(names, p.Param)
			}
		}
		return nil, true, fmt.Errorf("`paramSel` 里的「%s」不在这一份 clip 的花名册里（可用的是 %s；花名册是现读的）",
			strings.Join(bad, "、"), strings.Join(names, "、"))
	}
	var out []paramPick
	for _, p := range ro.Params { // 按花名册顺序
		cols, ok := sel[p.Param]
		if !ok {
			continue
		}
		pk := paramPick{Param: p.Param}
		for _, c := range cols {
			switch c {
			case "effective":
				pk.Effective = true
			case "envelope":
				pk.Envelope = true
			default:
				return nil, true, fmt.Errorf("参数 %s 的列名「%s」不认识（只有 `effective` 实参 / `envelope` 包络）", p.Param, c)
			}
		}
		if pk.Effective || pk.Envelope {
			out = append(out, pk)
		}
	}
	return out, true, nil
}

// effectiityOverrideLayer 取目标那一代**唯一的 override 层**（v2 是 `user`、v1 是 `direct`）。
// 跨代把 v1 包络折算成绝对值时写进它。
func effectiityOverrideLayer(have []string) string {
	for _, l := range have {
		if l == "user" || l == "direct" {
			return l
		}
	}
	return ""
}
