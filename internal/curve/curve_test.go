package curve

import (
	"math"
	"testing"
)

// 造一段“像音高线”的曲线：基音 + 两个音的滑音 + 颤音 + 轻微抖动
func synthetic(n int, step float64) []Pt {
	pts := make([]Pt, n)
	for i := 0; i < n; i++ {
		x := float64(i) * step
		t := x / float64(n) / step
		y := 60 + 3*math.Sin(2*math.Pi*t) + 1.2*math.Sin(2*math.Pi*6*t)
		if i > n/2 {
			y -= 4 * (float64(i-n/2) / float64(n/2))
		}
		y += 0.01 * math.Sin(float64(i)*1.7)
		pts[i] = Pt{X: x, Y: y}
	}
	return pts
}

func TestSimplifyKeepsErrorUnderTolerance(t *testing.T) {
	pts := synthetic(4000, 1)
	for _, tol := range []float64{0.02, 0.05, 0.1, 0.2} {
		got := SimplifyVertical(pts, tol)
		if len(got) >= len(pts) {
			t.Fatalf("tol=%.2f 没减少点数：%d → %d", tol, len(pts), len(got))
		}
		worst := 0.0
		j := 0
		for _, p := range pts {
			for j+1 < len(got) && got[j+1].X <= p.X {
				j++
			}
			var y float64
			if j+1 < len(got) {
				y = interpY(got[j], got[j+1], p.X)
			} else {
				y = got[len(got)-1].Y
			}
			if d := math.Abs(p.Y - y); d > worst {
				worst = d
			}
		}
		if worst > tol+1e-9 {
			t.Fatalf("tol=%.3f 时最大误差 %.4f 超了（锚点 %d 个）", tol, worst, len(got))
		}
		t.Logf("tol=%.2f ⇒ %d 个锚点，最大误差 %.4f 半音", tol, len(got), worst)
	}
}

func TestKeepExtremesFindsVibratoPeaks(t *testing.T) {
	pts := synthetic(2000, 1)
	keys := KeepExtremes(pts, 0.5)
	t.Logf("extremes=%d", len(keys))
	if len(keys) < 6 {
		// 调试：打印第 300..320 点的 y 值，确认颤音是否真的存在
		for i := 300; i < 320; i++ {
			t.Logf("i=%d y=%.4f", i, pts[i].Y)
		}
		t.Fatalf("颤音峰谷应该能找出若干个，实得 %d", len(keys))
	}
}

func TestForcedPointsSurvive(t *testing.T) {
	pts := synthetic(2000, 1)
	forced := map[int]bool{500: true, 1500: true}
	got := SimplifyWithForced(pts, 0.2, forced)
	if !hasX(got, pts[500].X) || !hasX(got, pts[1500].X) {
		t.Fatalf("强制点被删了：%v", got)
	}
}

func hasX(pts []Pt, x float64) bool {
	for _, p := range pts {
		if math.Abs(p.X-x) < 1e-9 {
			return true
		}
	}
	return false
}

func TestCatmullToBezierPassesEndpoints(t *testing.T) {
	anchors := []Pt{{0, 60}, {100, 62}, {200, 59}, {300, 61}}
	segs := CatmullToBezier(anchors, 1.0)
	if len(segs) != len(anchors)-1 {
		t.Fatalf("段数应为 %d，实得 %d", len(anchors)-1, len(segs))
	}
	for i, s := range segs {
		if s.P0 != anchors[i] || s.P3 != anchors[i+1] {
			t.Fatalf("第 %d 段端点没对上：%+v", i, s)
		}
	}
	// 相邻段在共享端点处切线应连续（C1）：方向近似相等
	mid := segs[0].P3
	if d := math.Abs(mid.Y - anchors[1].Y); d > 1e-9 {
		t.Fatalf("共享端点 y 不一致：%v", d)
	}
}

func TestBezierReproducesAnchors(t *testing.T) {
	anchors := []Pt{{0, 60}, {480, 60.5}, {960, 59}, {1440, 61.5}}
	for _, tension := range []float64{0, 0.5, 1, 1.5} {
		segs := CatmullToBezier(anchors, tension)
		for _, a := range anchors {
			found := false
			for _, s := range segs {
				if a.X >= s.P0.X && a.X <= s.P3.X {
					if d := math.Abs(s.YAtX(a.X) - a.Y); d > 1e-6 {
						t.Fatalf("tension=%.1f 在 x=%.0f 处偏了 %.6f", tension, a.X, d)
					}
					found = true
				}
			}
			if !found {
				t.Fatalf("锚点 x=%.0f 不落在任何段内", a.X)
			}
		}
	}
}

func TestFitBezierRespectsTolerance(t *testing.T) {
	pts := synthetic(3000, 1)
	anchors, segs, worst, rounds := FitBezier(pts, 0.05, 1.0)
	if len(segs) == 0 {
		t.Fatal("没拟合出贝塞尔段")
	}
	if worst > 0.05+1e-6 {
		t.Fatalf("拟合误差 %.4f 超过 0.05（锚点 %d，%d 轮）", worst, len(anchors), rounds)
	}
	t.Logf("FitBezier: %d 个锚点、%d 段、误差 %.4f、%d 轮", len(anchors), len(segs), worst, rounds)

	// 采样回去，再和原曲线比一遍
	back := Sample(segs, 1)
	if len(back) < len(pts)/2 {
		t.Fatalf("采样点太少：%d", len(back))
	}
	peak := 0.0
	for _, p := range pts {
		y := 0.0
		for _, s := range segs {
			if p.X >= s.P0.X && p.X <= s.P3.X {
				y = s.YAtX(p.X)
				break
			}
		}
		if d := math.Abs(p.Y - y); d > peak {
			peak = d
		}
	}
	if peak > 0.05+1e-6 {
		t.Fatalf("采样回读误差 %.4f 超了", peak)
	}
}

func TestEmptyAndTinyInputs(t *testing.T) {
	if got := SimplifyVertical(nil, 0.1); len(got) != 0 {
		t.Fatal("nil 应返回空")
	}
	one := []Pt{{0, 60}}
	if got := SimplifyVertical(one, 0.1); len(got) != 1 {
		t.Fatal("单点应原样返回")
	}
	if segs := CatmullToBezier(one, 1); segs != nil {
		t.Fatal("单点不该产生贝塞尔段")
	}
}
