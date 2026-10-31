# pitch.within：锚点改「逐条搬 + 保留 pointsVUV 的有无」
import io

p = 'internal/tools/pitchwithin.go'
s = io.open(p, encoding='utf-8').read()

# ① planCopyAnchors 整体替换
start = s.index('func planCopyAnchors(')
end = s.index('// ---------- ③ 音符颤音 ----------')
new_fn = '''func planCopyAnchors(pat *cbor.Node, sLo, sHi, delta int64) ([]*cbor.Node, string, error) {
	lane := laneOf(pat, "pitchDelta")
	if lane == nil {
		return nil, "这个片段没有 pitchDelta lane", nil
	}
	// 源里的锚点通常是**多条独立 entry**（每段颤音/滑音一条）。
	// 必须逐条平移、逐条写回；合并成一条会让 ACE 把它们连成一条连续曲线。
	var out []*cbor.Node
	srcEntries, srcPts := 0, 0
	for _, e := range lane.Items {
		if nodeText(e.Get("type")) != "anchor" {
			continue
		}
		pn := e.Get("points")
		if pn == nil || pn.Kind != cbor.KArray || len(pn.Items)%2 != 0 {
			continue
		}
		vn := e.Get("pointsVUV")
		var pts []any
		var vuv []float64
		for k := 0; k+1 < len(pn.Items); k += 2 {
			t, ok := nodeInt(pn.Items[k])
			if !ok || t < sLo || t >= sHi {
				continue
			}
			pts = append(pts, t+delta, pn.Items[k+1].Float)
			var v float64
			if vn != nil && vn.Kind == cbor.KArray && len(vn.Items) > k/2 {
				v = vn.Items[k/2].Float
			}
			vuv = append(vuv, v)
		}
		if len(pts) == 0 {
			continue
		}
		out = append(out, encodeAnchor(pts, vuv, vn != nil))
		srcEntries++
		srcPts += len(pts) / 2
	}
	if srcPts == 0 {
		return nil, "源区间里没有锚点", nil
	}
	return out, fmt.Sprintf("源 %d 条 entry / %d 点 → 逐条搬 %d 条（保持分段，值仍是绝对音高）",
		srcEntries, srcPts, len(out)), nil
}

// encodeAnchor 生成一条 anchor entry。withVUV 决定是否带 `pointsVUV` ——
// 源里没有这个键时**不能**补一个全 0 的数组：0 = 无声，ACE 会当作无音高数据，整段不画。
func encodeAnchor(pts []any, vuv []float64, withVUV bool) *cbor.Node {
	pairs := [][2]any{{"type", "anchor"}, {"points", pts}}
	if withVUV {
		pairs = append(pairs, [2]any{"pointsVUV", vuv})
	}
	n, err := cbor.Decode(cbor.EncodeMap(pairs...))
	if err != nil {
		return nil
	}
	return n
}

'''
s = s[:start] + new_fn + s[end:]

# ② rebuildLaneCovering 里改用 encodeAnchor（保留 pointsVUV 的有无）
old = '''		raw := cbor.EncodeMap(
			[2]any{"type", "anchor"},
			[2]any{"points", pts},
			[2]any{"pointsVUV", vuv},
		)
		n, err := cbor.Decode(raw)
		if err != nil {
			return nil, "", err
		}
		kept = append(kept, n)'''
new = '''		n := encodeAnchor(pts, vuv, vn != nil)
		if n == nil {
			return nil, "", fmt.Errorf("重建 anchor entry 失败")
		}
		kept = append(kept, n)'''
assert old in s, 'rebuildLaneCovering 片段没找到'
s = s.replace(old, new, 1)

io.open(p, 'w', encoding='utf-8').write(s)
print('patched')
