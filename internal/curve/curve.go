// Package curve 提供「音高线 ⇄ 锚点」与「锚点 ⇄ 贝塞尔曲线」的纯算法实现。
//
// 设计约束（来自 ACE 的实际数据形态）：
//   - 源数据是**逐 tick 的密集曲线**（vocalpitch combined / .acep 的 pitchDelta），
//     y 是半音（可带小数），x 是 tick；
//   - 目标形态是**锚点**（ACE 的 user 层 / .acep 的 anchor points），
//     锚点之间由宿主做插值，所以“简化”必须**按误差上限**来删点，不能按固定步长抽稀；
//   - 音高线的听感误差主要在**半音方向**，x 方向的抖动不该换算成误差
//     ⇒ 距离用「垂直距离」（同一 tick 上 y 的差），而不是点到线段的欧氏距离。
//
// 本包不含任何 ACETool / ACE 依赖，可单独测试（见 curve_test.go）。
package curve

import "math"

// Pt 是曲线上的一个点：X 用 tick，Y 用半音。
type Pt struct {
	X float64
	Y float64
}

// Cubic 是一段三次贝塞尔曲线（P0/P3 端点，P1/P2 控制点），x 方向单调。
type Cubic struct {
	P0, P1, P2, P3 Pt
}

// SimplifyVertical 用「垂直距离」RDP 把密集曲线简化成锚点。
//
// tol 是允许的最大半音误差（例如 0.05 = 5 音分）。
// 端点一定保留；返回的点按 X 升序。
func SimplifyVertical(pts []Pt, tol float64) []Pt {
	if len(pts) <= 2 || tol <= 0 {
		return append([]Pt(nil), pts...)
	}
	keep := make([]bool, len(pts))
	keep[0], keep[len(pts)-1] = true, true
	simplify(pts, 0, len(pts)-1, tol, keep)

	out := make([]Pt, 0, len(pts))
	for i, p := range pts {
		if keep[i] {
			out = append(out, p)
		}
	}
	return out
}

func simplify(pts []Pt, lo, hi int, tol float64, keep []bool) {
	if hi-lo < 2 {
		return
	}
	a, b := pts[lo], pts[hi]
	maxD, idx := 0.0, -1
	for i := lo + 1; i < hi; i++ {
		d := math.Abs(pts[i].Y - interpY(a, b, pts[i].X))
		if d > maxD {
			maxD, idx = d, i
		}
	}
	if idx < 0 || maxD <= tol {
		return
	}
	keep[idx] = true
	simplify(pts, lo, idx, tol, keep)
	simplify(pts, idx, hi, tol, keep)
}

func interpY(a, b Pt, x float64) float64 {
	if b.X == a.X {
		return a.Y
	}
	t := (x - a.X) / (b.X - a.X)
	return a.Y + (b.Y-a.Y)*t
}

// KeepExtremes 找出“值得留成锚点”的局部极值（颤音峰谷），返回下标。
//
// 三步：
//  1. y 做 5 点平滑（去掉逐 tick 抖动，否则斜率符号会被噪声翻掉）；
//  2. 用 ±3 点斜率扫描出**交替的极值序列**（峰、谷、峰…），平台只取一个；
//  3. 用突起量（prominence）过滤：峰到左右相邻谷的较低者高差 >= prominence 才保留。
func KeepExtremes(pts []Pt, prominence float64) map[int]bool {
	out := map[int]bool{}
	if len(pts) < 9 || prominence <= 0 {
		return out
	}
	n := len(pts)
	sm := make([]float64, n)
	for i := 0; i < n; i++ {
		lo, hi := i-2, i+2
		if lo < 0 {
			lo = 0
		}
		if hi >= n {
			hi = n - 1
		}
		sum := 0.0
		for j := lo; j <= hi; j++ {
			sum += pts[j].Y
		}
		sm[i] = sum / float64(hi-lo+1)
	}

	// ① 交替极值序列
	type ext struct {
		idx  int
		peak bool
	}
	var extrema []ext
	for i := 3; i < n-3; i++ {
		left := sm[i] - sm[i-3]
		right := sm[i] - sm[i+3]
		switch {
		case left > 0 && right > 0:
			if k := len(extrema) - 1; k >= 0 && extrema[k].peak {
				if sm[i] > sm[extrema[k].idx] {
					extrema[k].idx = i
				}
				continue
			}
			extrema = append(extrema, ext{i, true})
		case left < 0 && right < 0:
			if k := len(extrema) - 1; k >= 0 && !extrema[k].peak {
				if sm[i] < sm[extrema[k].idx] {
					extrema[k].idx = i
				}
				continue
			}
			extrema = append(extrema, ext{i, false})
		}
	}
	if len(extrema) < 3 {
		return out
	}
	// ② 突起量过滤（只看内部极值，首尾没有两侧邻点）
	for k := 1; k < len(extrema)-1; k++ {
		cur := extrema[k]
		l, r := extrema[k-1], extrema[k+1]
		if cur.peak == l.peak || cur.peak == r.peak {
			continue
		}
		var prom float64
		if cur.peak {
			prom = sm[cur.idx] - math.Max(sm[l.idx], sm[r.idx])
		} else {
			prom = math.Min(sm[l.idx], sm[r.idx]) - sm[cur.idx]
		}
		if prom >= prominence {
			out[cur.idx] = true
		}
	}
	return out
}

// SimplifyWithForced 与 SimplifyVertical 相同，但额外保留 forced 里的下标
// （音符起止、极值点等“不能让宿主插值抹掉”的位置）。
func SimplifyWithForced(pts []Pt, tol float64, forced map[int]bool) []Pt {
	if len(pts) <= 2 {
		return append([]Pt(nil), pts...)
	}
	keep := make([]bool, len(pts))
	keep[0], keep[len(pts)-1] = true, true
	for i := range forced {
		if i >= 0 && i < len(pts) {
			keep[i] = true
		}
	}
	// 以强制点为界，逐段做 RDP
	idx := []int{0}
	for i := 1; i < len(pts)-1; i++ {
		if keep[i] {
			idx = append(idx, i)
		}
	}
	idx = append(idx, len(pts)-1)
	for k := 0; k < len(idx)-1; k++ {
		simplify(pts, idx[k], idx[k+1], tol, keep)
	}
	out := make([]Pt, 0, len(pts))
	for i, p := range pts {
		if keep[i] {
			out = append(out, p)
		}
	}
	return out
}

// CatmullToBezier 把锚点序列转成 C1 连续的分段三次贝塞尔（Catmull-Rom → Bezier）。
//
// tension: 1 为标准 Catmull-Rom；0 退化成直线（控制点落在端点连线上）。
// 端点用“反射虚拟点”外推，保证首尾也有切线。
func CatmullToBezier(pts []Pt, tension float64) []Cubic {
	if len(pts) < 2 {
		return nil
	}
	if tension < 0 {
		tension = 0
	}
	if tension > 2 {
		tension = 2
	}
	get := func(i int) Pt {
		switch {
		case i < 0:
			return Pt{X: 2*pts[0].X - pts[1].X, Y: 2*pts[0].Y - pts[1].Y}
		case i >= len(pts):
			n := len(pts)
			return Pt{X: 2*pts[n-1].X - pts[n-2].X, Y: 2*pts[n-1].Y - pts[n-2].Y}
		default:
			return pts[i]
		}
	}
	out := make([]Cubic, 0, len(pts)-1)
	for i := 0; i+1 < len(pts); i++ {
		p0, p1 := pts[i], pts[i+1]
		prev, next := get(i-1), get(i+2)
		k := tension / 6.0
		c1 := Pt{X: p0.X + (p1.X-prev.X)*k, Y: p0.Y + (p1.Y-prev.Y)*k}
		c2 := Pt{X: p1.X - (next.X-p0.X)*k, Y: p1.Y - (next.Y-p0.Y)*k}
		out = append(out, Cubic{P0: p0, P1: c1, P2: c2, P3: p1})
	}
	return out
}

// At 取贝塞尔段在参数 t∈[0,1] 上的点（按 x 求值请用 YAtX）。
func (c Cubic) At(t float64) Pt {
	mt := 1 - t
	a := mt * mt * mt
	b := 3 * mt * mt * t
	d := 3 * mt * t * t
	e := t * t * t
	return Pt{
		X: a*c.P0.X + b*c.P1.X + d*c.P2.X + e*c.P3.X,
		Y: a*c.P0.Y + b*c.P1.Y + d*c.P2.Y + e*c.P3.Y,
	}
}

// YAtX 在段内按 x 求 y（二分反解 t；x 单调时成立）。
func (c Cubic) YAtX(x float64) float64 {
	if x <= c.P0.X {
		return c.P0.Y
	}
	if x >= c.P3.X {
		return c.P3.Y
	}
	lo, hi := 0.0, 1.0
	for i := 0; i < 40; i++ {
		mid := (lo + hi) / 2
		if c.At(mid).X < x {
			lo = mid
		} else {
			hi = mid
		}
	}
	return c.At((lo + hi) / 2).Y
}

// Sample 按固定 tick 步长把贝塞尔段采样回密集曲线。
func Sample(segs []Cubic, step float64) []Pt {
	if len(segs) == 0 || step <= 0 {
		return nil
	}
	var out []Pt
	for _, s := range segs {
		if s.P3.X <= s.P0.X {
			continue
		}
		n := int(math.Ceil((s.P3.X - s.P0.X) / step))
		if n < 1 {
			n = 1
		}
		for i := 0; i < n; i++ {
			x := s.P0.X + float64(i)*step
			out = append(out, Pt{X: x, Y: s.YAtX(x)})
		}
	}
	if len(segs) > 0 {
		out = append(out, segs[len(segs)-1].P3)
	}
	return out
}

// FitBezier 用「自适应加点」拟合：先用 RDP 取锚点，转贝塞尔，
// 测垂直误差，误差最大的段插入一个锚点再拟合，直到全段误差 ≤ tol。
//
// 返回：最终的锚点、贝塞尔段、最大垂直误差（半音）、迭代轮数。
func FitBezier(pts []Pt, tol float64, tension float64) ([]Pt, []Cubic, float64, int) {
	if len(pts) < 2 {
		return append([]Pt(nil), pts...), nil, 0, 0
	}
	anchors := SimplifyVertical(pts, tol*0.5)
	for round := 0; round < 64; round++ {
		segs := CatmullToBezier(anchors, tension)
		worst, worstSeg, worstX := 0.0, -1, 0.0
		for si, s := range segs {
			lo := sortLess(s.P0.X, s.P3.X)
			hi := sortGreater(s.P0.X, s.P3.X)
			for _, p := range pts {
				if p.X < lo || p.X > hi {
					continue
				}
				d := math.Abs(p.Y - s.YAtX(p.X))
				if d > worst {
					worst, worstSeg, worstX = d, si, p.X
				}
			}
		}
		if worst <= tol || worstSeg < 0 {
			return anchors, segs, worst, round
		}
		// 在最差点插一个锚点（用原始曲线的值，保证不漂）
		y := originalYAt(pts, worstX)
		anchors = insertPt(anchors, Pt{X: worstX, Y: y})
	}
	segs := CatmullToBezier(anchors, tension)
	return anchors, segs, maxErr(pts, segs), 64
}

func sortLess(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func sortGreater(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func originalYAt(pts []Pt, x float64) float64 {
	for i := 1; i < len(pts); i++ {
		if pts[i].X >= x {
			return interpY(pts[i-1], pts[i], x)
		}
	}
	return pts[len(pts)-1].Y
}

func insertPt(pts []Pt, p Pt) []Pt {
	for i := 0; i < len(pts); i++ {
		if math.Abs(pts[i].X-p.X) < 1e-9 {
			return pts
		}
		if pts[i].X > p.X {
			out := make([]Pt, 0, len(pts)+1)
			out = append(out, pts[:i]...)
			out = append(out, p)
			out = append(out, pts[i:]...)
			return out
		}
	}
	return append(pts, p)
}

func maxErr(pts []Pt, segs []Cubic) float64 {
	worst := 0.0
	for _, p := range pts {
		for _, s := range segs {
			if p.X < s.P0.X || p.X > s.P3.X {
				continue
			}
			if d := math.Abs(p.Y - s.YAtX(p.X)); d > worst {
				worst = d
			}
			break
		}
	}
	return worst
}
