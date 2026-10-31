// Package layout 管「按钮顺序与显示」这一份配置（用户 2026-10-20）。
//
// 背景：侧栏（`internal/rail`）与主面板（`home.html`）的按钮**顺序与显隐必须是同一份**，
// 而它们的清单分别长在两个地方。所以这里**只放算法、不放清单**：
// 每个装配点在渲染前调 `Arrange(自己的 id 列表, cfg.LayoutOrder, cfg.LayoutHidden)`。
//
// ⛔ 硬口径（用户明确要求）：`hidden` **只作用于渲染** —— 隐藏的按钮快捷键照跑、工具照用。
// 所以本包不提供"是否启用"这类语义，只有"画不画、排第几"。
package layout

import "strings"

// Arrange 按 `order` 重排 `ids`，并剔除 `hidden` 里的项。
//
//   - `order` 里出现、`ids` 里也有的 ⇒ 按 `order` 的顺序排在前面；
//   - `ids` 里没被 `order` 提到的 ⇒ **保持它们在 `ids` 里的相对次序**，接在后面
//     （这样新增按钮不用改配置也会出现，不会"配过顺序之后新按钮就消失"）；
//   - `order` 里有、`ids` 里没有的 id ⇒ **忽略**（按钮被删了，配置里留着残渣不该报错）；
//   - `hidden` 里的 id ⇒ 直接不返回（不管它在 `order` 里排第几）。
//
// 返回的是新切片，不改入参。
func Arrange(ids []string, order, hidden []string) []string {
	hid := make(map[string]bool, len(hidden))
	for _, h := range hidden {
		if s := strings.TrimSpace(h); s != "" {
			hid[s] = true
		}
	}
	have := make(map[string]bool, len(ids))
	for _, id := range ids {
		have[id] = true
	}

	out := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range order {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] || !have[id] || hid[id] {
			continue
		}
		out = append(out, id)
		seen[id] = true
	}
	for _, id := range ids {
		if seen[id] || hid[id] {
			continue
		}
		out = append(out, id)
		seen[id] = true
	}
	return out
}

// Unknown 报出 `order` / `hidden` 里**不在 `known`** 的 id（去重、保持出现顺序）。
//
// 用途：保存时**当场报错**而不是静默丢 —— 静默丢的话用户在设置页排好的顺序会"看起来保存了、
// 其实丢了一条"，下次打开又变回去，最难查。
func Unknown(order, hidden, known []string) []string {
	k := make(map[string]bool, len(known))
	for _, id := range known {
		k[id] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, group := range [][]string{order, hidden} {
		for _, id := range group {
			id = strings.TrimSpace(id)
			if id == "" || k[id] || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// Clean 规整一份配置：去空白、去空串、去重（保序）。保存前过一遍，别把垃圾写进 config.json。
func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func Clean(ids []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// EffectiveHidden 把「默认隐藏、显式打开」这件事算进 hidden 里（用户 2026-10-20）：
// 生效的隐藏 = hidden ∪ (defaultHidden − shown)。
func EffectiveHidden(hidden, shown, defaultHidden []string) []string {
	sh := map[string]bool{}
	for _, k := range shown {
		sh[strings.TrimSpace(k)] = true
	}
	out := append([]string(nil), Clean(hidden)...)
	for _, k := range Clean(defaultHidden) {
		if !sh[k] && !containsStr(out, k) {
			out = append(out, k)
		}
	}
	return out
}
