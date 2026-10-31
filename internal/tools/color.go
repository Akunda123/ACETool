package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// 28 色调色板 —— 与 `acestudio-cli help color` 的官方列表逐字一致（索引 0..27）。
// 参考脚本 scratch/ace-color-cycle.ps1 用的就是这一份，新轨道也是按这个顺序循环取色。
//
// ⚠️ `help track set` 写着 --color "Must be a value from the palette returned by `color-palette`"
// —— 那是文档 bug：**没有 `color-palette` 这个命令**（§3.5 已记）。权威是 `help color` 这篇 topic。
// handler 其实接受任何合法 `#RRGGBB`，但调色板之外的值在 UI 色板里对不上，所以只走这 28 个。
var palette = []string{
	"#EC4F44", "#FFBABA", "#F66C03", "#A15C27", "#FF9A4E", "#FFC394", "#F6D203", "#F9FF8C",
	"#CCFF31", "#859C3F", "#47EC57", "#95FFA8", "#03F6B1", "#92FFE0", "#03C9F6", "#58DEFF",
	"#98C5FD", "#038DF6", "#5882FF", "#6A19FF", "#9155FF", "#9D03F6", "#C053FF", "#B892FF",
	"#FF93F2", "#F603DA", "#FF56EB", "#FF59AC",
}

// Palette 给前端（色块、调色板预览）用。返回副本，别让调用方改到内部表。
func Palette() []string { return append([]string(nil), palette...) }

func paletteIndex(hex string) int {
	h := strings.ToLower(strings.TrimSpace(hex))
	for i, c := range palette {
		if strings.ToLower(c) == h {
			return i
		}
	}
	return -1
}

// wrapIdx 归一化到 0..len-1 —— Go 的 % 对负数回负数，得绕一圈（参考脚本的 Wrap 同理）。
func wrapIdx(v int) int {
	n := len(palette)
	return ((v % n) + n) % n
}

func init() {
	register(&Tool{
		ID: "color.cycle", Category: "轨道",
		Title: "轨道颜色循环（写）",
		Desc: "把一条轨道的颜色在 28 色调色板上推进一格：定位当前色 → 写下一格。" +
			"⚠️ 这是写路径的冒烟测试，默认就改；每走一步在 ACE 里是**一条撤销记录**（ACE 没有事务 API）。" +
			"**默认一步只发一条 `track set`**：定位当前色那一步靠 `fromHex` / `trackIndex` 复用上一步的结果" +
			"（都留空时本步才去读）。",
		Kind:   KindWrite,
		Source: "ace-color-cycle.ps1（-Mode next / -Loop / -NoReadBack）",
		Params: []Param{
			textParam("trackIndex", "轨道索引（留空 = 跟随 caret）", "", "0", "0 起；留空就先问 caret 现在在哪条轨"),
			textParam("fromHex", "已知当前色（留空 = 本步先读一次）", "", "#ccff31",
				"循环时从第 2 步起把上一步写出去的色传回来 —— 省掉每步的 track get，变成一步一条命令。"+
					"⚠️ 传进来就不再核对真实颜色，外部若同时改了色会从你给的值往下走。"),
			{Name: "direction", Label: "方向", Type: ParamSelect, Default: "next",
				Options: []Option{{Value: "next", Label: "下一格 →"}, {Value: "prev", Label: "← 上一格"}}},
			checkParam("readBack", "写后回读核对（多一次往返，默认关）", false),
		},
		Build: cycleBuild,
	})
}

// cycleBuild 是「读 → 决定 → 写」的形状。
// 注意：**干跑时这一步读也照做** —— 计划本身就需要当前颜色（参考脚本的 dry run 同样只读不写）。
func cycleBuild(ctx context.Context, rd ReadFunc, vals map[string]string) ([]Call, error) {
	// ⚠️ region 固定 arrangement：`--region chord` 会拒 --color（见 help track set）。
	const region = "arrangement"

	// 1) 目标轨道：显式给了索引就用；留空则问 caret（= 参考脚本的默认行为）
	idx := 0
	if s := value(vals, "trackIndex", ""); s != "" {
		n, err := intValue(vals, "trackIndex", 0, "轨道索引")
		if err != nil {
			return nil, err
		}
		idx = n
	} else {
		var caret struct {
			TrackIndex int `json:"trackIndex"`
		}
		if err := readJSON(ctx, rd, &caret, "caret", "get", "--scope", "arrangement"); err != nil {
			return nil, fmt.Errorf("读 caret 失败（要跟随 caret 就先把标记线放到目标轨道上，或直接填轨道索引）：%w", err)
		}
		idx = caret.TrackIndex
	}

	// 2) 当前颜色：**给了 fromHex 就不读** —— 循环从第 2 步起走这条，省掉每步一次 track get。
	//    ⚠️ 代价说清楚：传进来的值不再核对，外部若同时改了色，就会从你给的那个值往下走。
	from := strings.ToLower(strings.TrimSpace(value(vals, "fromHex", "")))
	if from == "" {
		var got struct {
			Color string `json:"color"`
		}
		if err := readJSON(ctx, rd, &got, "track", "get", "--track-index", strconv.Itoa(idx), "--region", region); err != nil {
			return nil, err
		}
		from = strings.ToLower(strings.TrimSpace(got.Color))
	}

	// 3) 定位调色板 + 步进（不在调色板里就从 0 起，与参考脚本一致）
	step := 1
	if value(vals, "direction", "next") == "prev" {
		step = -1
	}
	cur := paletteIndex(from)
	next := 0
	why := ""
	if cur < 0 {
		why = "当前颜色不在调色板里 → 从 palette[0] 起"
	} else {
		next = wrapIdx(cur + step)
		why = fmt.Sprintf("palette[%d] %+d → palette[%d]", cur, step, next)
	}
	to := strings.ToLower(palette[next])

	// 4) 组装调用：写 +（可选）回读核对
	calls := []Call{{
		Label: fmt.Sprintf("轨道 %d：%s → %s（palette[%d]，%s）", idx, from, to, next, why),
		Args:  []string{"track", "set", "--track-index", strconv.Itoa(idx), "--region", region, "--color", to},
	}}
	if truthy(value(vals, "readBack", "0")) {
		calls = append(calls, Call{
			Label: "写后回读核对",
			Args:  []string{"track", "get", "--track-index", strconv.Itoa(idx), "--region", region},
		})
	}
	return calls, nil
}
