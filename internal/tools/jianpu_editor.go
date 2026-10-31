package tools

import (
	"context"
	"strings"
)

// `jianpu.editor` —— **谱面编辑器**的取数入口（只读）。
//
// ⭐ 分工（用户 2026-10-05 定的路线）：
//
//	简谱文本 / 谱面编辑器  ⇄  音符块(Element)  ⇄  MusicXML  ⇄  { ACE | 打谱软件 }
//
// 编辑器是**面板里的一个专用 UI**（`notation.js`：简谱 SVG + 五线谱 SVG 双联，
// 音高/时值/歌词三个维度直接在谱面上改），它只需要后端干三件事：
//
//	① 读：把 ACE 当前片段读成**元素文档**（本工具，`format=elements`）
//	② 出：把元素文档换算成 MusicXML / 简洁记法 / 音符块（`jianpu.fromClip` 的 `elements` 入参）
//	③ 写：把元素文档写回 ACE（`jianpu.toNotes` 的 `elements` 入参，写工具、默认干跑）
//
// ⇒ 本工具**只做 ①**，于是它是个纯只读工具（注册表不变量：Build/Inspect/Plan+Apply 恰有其一）。
// ②③ 都是既有工具的能力，不另开新写路径 —— **一份写逻辑**，面板只是调用方。
//
// ⚠️ 为什么不把编辑器的写也塞进这个工具：那会让它变成写工具，而"读"这一步就没有落点了；
// 而且干跑闸门是按工具分的，分开才能既安全又顺手。
func init() {
	register(&Tool{
		ID: "jianpu.editor", Category: jianpuCategory,
		Title: "谱面编辑器（简谱 + 五线谱）",
		Desc: "把 ACE 当前片段读进**双联谱面编辑器**：上排简谱、下排五线谱，共用一套坐标，" +
			"点音符就能改**音高 / 时值 / 歌词**。" +
			"`1 -` 连音 = ACE 的 tenuto 音符，两边一一对应、可往返。" +
			"改完【存回 ACE】（默认干跑）或【导出 MusicXML】。只读，不改工程。",
		Kind:   KindRead,
		Source: "自建：`internal/jianpu` 的元素模型 + 手写 SVG 渲染（零前端依赖，无构建步骤）",
		Params: []Param{
			{Name: "scope", Label: "读进来的范围", Type: ParamSelect, Default: "auto",
				Options: []Option{
					{Value: "auto", Label: "选中的音符（没选中就整个片段）"},
					{Value: "clip", Label: "整个当前片段"},
				}},
			textParam("key", "调式下标", "auto", "auto 或 0..11", "C=0 … B=11；`auto` 用「生成和声」那套自动检测（会并列，面板会如实标出来）"),
			textParam("meter", "拍号", "4/4", "4/4", "决定小节线画在哪、五线谱怎么分小节"),
		},
		Inspect: func(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
			// 只是把「产出」定成元素文档，其余交给同一条读路径 —— 不复制实现
			v := make(map[string]string, len(vals)+1)
			for k, s := range vals {
				v[k] = s
			}
			v["format"] = "elements"
			rep, err := analyzeFromClip(ctx, rd, v)
			if err != nil {
				return nil, err
			}
			// 面板要靠这几项决定怎么画（调式 → 音级；拍号 → 小节线；并列 → 提醒用户）
			rep.Note = strings.TrimPrefix(rep.Note, "只读：没动工程。")
			return rep, nil
		},
	})
}
