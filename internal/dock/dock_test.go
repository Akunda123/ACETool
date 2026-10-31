//go:build windows

package dock

import (
	"testing"

	"acetool/internal/win"
)

// 贴边吸附的**几何**是纯函数 ⇒ 这层用单元测试钉死（真机那层要 ACE 开着才能验）。
//
// 用户口径（2026-10-16）：「把窗口贴到四周的时候自动吸附」；左右边**真的贴成侧栏**，
// 上下边只有位置对齐（`partial=true`，因为窄栏页是竖排的，横过来没法看）。

func TestNearestEdgePicksClosestSide(t *testing.T) {
	cli := win.Rect{Left: 100, Top: 200, Right: 1500, Bottom: 1000} // ACE 客户区
	cases := []struct {
		name string
		r    win.Rect
		want string
		dist int32
	}{
		{"紧贴右边", win.Rect{Left: 1300, Top: 300, Right: 1502, Bottom: 900}, "right", 2},
		{"紧贴左边", win.Rect{Left: 98, Top: 300, Right: 260, Bottom: 900}, "left", 2},
		{"靠近上边", win.Rect{Left: 400, Top: 190, Right: 900, Bottom: 700}, "top", 10},
		{"靠近下边", win.Rect{Left: 400, Top: 500, Right: 900, Bottom: 1010}, "bottom", 10},
		{"离得都远 ⇒ 最近的仍是 right", win.Rect{Left: 1200, Top: 300, Right: 1400, Bottom: 900}, "right", 100},
	}
	for _, c := range cases {
		edge, dist := nearestEdge(c.r, cli)
		if edge != c.want || dist != c.dist {
			t.Errorf("%s：得到 (%s,%d)，想要 (%s,%d)", c.name, edge, dist, c.want, c.dist)
		}
	}
}

func TestFlushRectRightIsRealDock(t *testing.T) {
	// 右边：宽度**保留用户调好的**，高度跟着 ACE 客户区（顶边让开 topInset=63、底边留 pad=9）
	cli := win.Rect{Left: 100, Top: 200, Right: 1500, Bottom: 1000}
	r := win.Rect{Left: 1300, Top: 300, Right: 1502, Bottom: 900} // 宽 202
	got, partial := flushRect("right", r, cli, 63, 9, 1.5)
	want := win.Rect{Left: 1298, Top: 263, Right: 1500, Bottom: 991}
	if got != want || partial {
		t.Errorf("右边贴齐：得到 %+v partial=%v，想要 %+v partial=false", got, partial, want)
	}
	if got.Right != cli.Right {
		t.Errorf("右边贴齐必须贴住客户区右缘：%d != %d", got.Right, cli.Right)
	}
}

func TestFlushRectLeftIsRealDock(t *testing.T) {
	cli := win.Rect{Left: 100, Top: 200, Right: 1500, Bottom: 1000}
	r := win.Rect{Left: 98, Top: 300, Right: 300, Bottom: 900} // 宽 202
	got, partial := flushRect("left", r, cli, 63, 9, 1.5)
	want := win.Rect{Left: 100, Top: 263, Right: 302, Bottom: 991}
	if got != want || partial {
		t.Errorf("左边贴齐：得到 %+v partial=%v，想要 %+v partial=false", got, partial, want)
	}
}

func TestFlushRectTopBottomOnlyAlignsPosition(t *testing.T) {
	cli := win.Rect{Left: 100, Top: 200, Right: 1500, Bottom: 1000}
	r := win.Rect{Left: 400, Top: 190, Right: 900, Bottom: 700} // 500x510
	got, partial := flushRect("top", r, cli, 63, 9, 1.5)
	if !partial {
		t.Error("上边应当标 partial=true（只对齐位置，尺寸不动）")
	}
	if got.Top != 263 || got.H() != r.H() || got.Left != r.Left {
		t.Errorf("上边对齐：得到 %+v（应为 top=263、尺寸不变、左右不动）", got)
	}
	gotB, partialB := flushRect("bottom", r, cli, 63, 9, 1.5)
	if !partialB || gotB.Bottom != 991 || gotB.H() != r.H() {
		t.Errorf("下边对齐：得到 %+v partial=%v", gotB, partialB)
	}
}

func TestStartRequiresTitle(t *testing.T) {
	if _, err := Start(Options{}); err == nil {
		t.Error("没给标题应当直接报错（不然不知道该吸谁）")
	}
}
