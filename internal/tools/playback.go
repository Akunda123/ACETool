package tools

import (
	"context"
)

// 播放控制（ACE 的 `transport`）—— 为「填词」页面的**试听**服务。
//
// 为什么单独一组：试听的口径是「句前 0.5 秒开始播 → 播到**句后 0.5 秒暂停**」。
// "暂停"那一刻不在命令里，而在**时间**里 —— 所以拆成两件事：
//
//	① `lyric.audition`（在 lyric.go 里）：定位 + 开始播
//	② 页面侧轮询本文件的 `transport.state`，数到 `positionTick >= 句尾 + 提前量` 时调 `transport.stop`
//
// ⚠️ **不把 sleep 放进 exe**：一句可能二十秒，`/api/run` 会被堵住（CLI 调用层是串行的），
// 用户中途想点别的也不行。放页面侧，exe 每一笔都是短的。
func init() {
	register(&Tool{
		ID: "transport.state", Category: "播放",
		Title: "播放状态",
		Desc: "读播放头的**状态与位置**（`transport state`）：status 是 `stopped` / `playing` / " +
			"`playing but interrupted`（等合成），position 是秒、positionTick 是 tick。" +
			"⚠️ 试听的「到点暂停」就靠它：页面每 ~150 ms 读一次，数到句尾 + 提前量就停。只读。",
		Kind:   KindRead,
		Source: "自建（试听到点暂停要用）",
		Params: nil,
		Inspect: func(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
			return readTransportState(ctx, rd)
		},
	})

	register(&Tool{
		ID: "transport.stop", Category: "播放",
		Title: "停止播放",
		Desc: "停止（暂停）播放：`transport stop`。试听数到点之后由页面调它，" +
			"也用来在用户点别的行时**掐掉上一段试听**。⚠️ 写，默认干跑。",
		Kind:   KindWrite,
		Source: "自建（试听到点暂停要用）",
		Params: nil,
		Build: func(ctx context.Context, rd ReadFunc, vals map[string]string) ([]Call, error) {
			return []Call{{Label: "停止播放", Args: []string{"transport", "stop"}}}, nil
		},
	})
}

// transportState 是 `transport state` 的回包。
type transportState struct {
	Status       string  `json:"status"`
	Position     float64 `json:"position"`
	PositionTick int64   `json:"positionTick"`
	NativeUnit   string  `json:"nativeUnit"`
}

func readTransportState(ctx context.Context, rd ReadFunc) (*transportState, error) {
	var st transportState
	if err := readJSON(ctx, rd, &st, "transport", "state"); err != nil {
		return nil, err
	}
	return &st, nil
}

// Playing 是给人看的一句话（页面顶栏的灯就吃它）。
func (s *transportState) Playing() bool {
	return s.Status == "playing" || s.Status == "playing but interrupted"
}
