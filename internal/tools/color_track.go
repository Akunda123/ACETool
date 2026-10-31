package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// `track.color` —— **把某条轨设成调色板里的某个颜色**（按索引或按 hex）。
//
// 为什么需要它（而不是都用 `color.cycle`）：
// `color.cycle` 只会"沿 ACE 调色板**走一格**"，它没有"我只要这几个颜色"的概念。
// 而 RGB 子窗口里用户要**勾选参与轮转的颜色** ⇒ 轮转必须由**前端按勾选集驱动**，
// 那就需要一个"设成这个颜色"的原语。前端自己记索引 i，每拍把 checked[i] 发给每条选中轨。
//
// 顺带：因为每条轨收到的是**同一个绝对颜色**，多轨模式天然就是"同一拍各换一格、颜色一致"
// （用户 2026-10-05 选的 A 方案）—— 用 `cycle` 那种"各走一格"反而会因各轨起始色不同而错开。
//
// ⚠️ 颜色只认 `palette` 里的 28 个（`internal/tools/color.go`）——与 `color.cycle` 同一份表、
// 同一个判据（调色板之外的值在 ACE 的 UI 色板里对不上）。
type trackColorReport struct {
	TrackIndex int    `json:"trackIndex"`
	Color      string `json:"color"`
	PalettePos int    `json:"paletteIndex"`
	Via        string `json:"via"` // index / hex
	Note       string `json:"note,omitempty"`
}

func init() {
	register(&Tool{
		ID: "track.color", Category: "轨道",
		Title: "轨道颜色（设成指定颜色）",
		Desc: "把指定轨道设成**调色板里的某个颜色**：给 `index`（0..27，会自动取模）或 `color`（`#RRGGBB`）。" +
			"⚠️ 只认 ACE 那 28 色调色板里的值（与「轨道颜色循环」同一张表）。" +
			"⚠️ 不给 `trackIndex` 就跟 `caret` 所在的轨（与 `color.cycle` 一致）。" +
			"用途：给「RGB 变色」那种**按勾选集驱动**的轮转当原语 —— `color.cycle` 只会沿调色板走一格，" +
			"表达不了\"只用勾中的这几个颜色\"。",
		Kind:   KindWrite,
		Source: "自建：`track set --color`（供 RGB 子窗口按勾选集驱动）",
		// RGB 循环一秒好几条 ⇒ 不加「撤回标记」（跑前后各读一次撤销栈会把轮转拖慢），
		// 而且"撤回一条变色"对用户没意义（他自己就看得见颜色变回去了）。
		NoUndoMark: true,
		Params: []Param{
			numParam("trackIndex", "轨道下标（留空跟 caret）", ""),
			numParam("index", "调色板下标（0..27）", "0"),
			textParam("color", "或直接给颜色（#RRGGBB）", "", "#F6D203", "给了它就优先用 hex"),
		},
		Build: func(ctx context.Context, rd ReadFunc, vals map[string]string) ([]Call, error) {
			// 目标轨：与 color.cycle 同一套（显式给就用，留空问 caret）
			var idx int
			if s := strings.TrimSpace(vals["trackIndex"]); s != "" {
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
					return nil, fmt.Errorf("读 caret 失败（要跟随 caret 就先把标记线放到目标轨道上，或直接填轨道下标）：%w", err)
				}
				idx = caret.TrackIndex
			}

			// 颜色：hex 优先；否则按下标取模
			color := strings.TrimSpace(vals["color"])
			if color == "" {
				n, err := intValue(vals, "index", 0, "调色板下标")
				if err != nil {
					return nil, err
				}
				color = palette[((n%len(palette))+len(palette))%len(palette)]
			}
			pos := paletteIndex(color)
			if pos < 0 {
				return nil, fmt.Errorf("`%s` 不在 ACE 那 28 色调色板里（`help color` 的官方列表）⇒ 换一个；"+
					"要列举合法的值看「轨道颜色循环」工具的说明", color)
			}
			return []Call{{
				Label: fmt.Sprintf("轨道 %d → %s（调色板第 %d 个）", idx, color, pos),
				Args: []string{"track", "set", "--track-index", strconv.Itoa(idx),
					"--region", "arrangement", "--color", color},
			}}, nil
		},
	})
}
