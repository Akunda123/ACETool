# 颤音配对：从「pos 必须完全相等」改成「同音高里最近的、±240 tick 内」
import io

p = 'internal/tools/pitchwithin.go'
s = io.open(p, encoding='utf-8').read()

start = s.index('func planCopyVibrato(')
end = s.index('// ---------- 公共小工具 ----------')
new = '''func planCopyVibrato(src, dst *cbor.Node, sLo, sHi, delta int64) ([]cbor.Edit, string, error) {
	notes := dst.Get("notes")
	if notes == nil || notes.Kind != cbor.KArray {
		return nil, "目标片段没有音符", nil
	}
	srcNotes := src.Get("notes")
	if srcNotes == nil || srcNotes.Kind != cbor.KArray {
		return nil, "源片段没有音符", nil
	}
	type noteRef struct {
		node  *cbor.Node
		pos   int64
		pitch int64
	}
	var dstRefs []noteRef
	for _, n := range notes.Items {
		if p, ok := nodeInt(n.Get("pos")); ok {
			pitch, _ := nodeInt(n.Get("pitch"))
			dstRefs = append(dstRefs, noteRef{node: n, pos: p, pitch: pitch})
		}
	}
	sort.SliceStable(dstRefs, func(i, j int) bool { return dstRefs[i].pos < dstRefs[j].pos })

	// 段落之间的音符起点常有几 tick 的量化差（实测 5 tick），所以按“最近邻”配对，
	// 不能要求 pos 完全相等；同音高优先，超出容忍窗就不硬配。
	const tolTicks = 240 // 半个四分音符
	var edits []cbor.Edit
	srcN, wrote, noNear, farOff := 0, 0, 0, 0
	var offs []int64
	for _, sn := range srcNotes.Items {
		spos, ok := nodeInt(sn.Get("pos"))
		if !ok || spos < sLo || spos >= sHi {
			continue
		}
		vib := sn.Get("vibrato")
		if vib == nil {
			continue
		}
		srcN++
		want := spos + delta
		spitch, _ := nodeInt(sn.Get("pitch"))
		// 在排序表里找最近的若干条，同音高优先
		best := -1
		var bestSameOff, bestOff int64 = 1 << 62, 1 << 62
		i := sort.Search(len(dstRefs), func(k int) bool { return dstRefs[k].pos >= want })
		for k := i - 4; k <= i+4; k++ {
			if k < 0 || k >= len(dstRefs) {
				continue
			}
			off := dstRefs[k].pos - want
			if off < 0 {
				off = -off
			}
			if dstRefs[k].pitch == spitch && off < bestSameOff && off <= tolTicks {
				bestSameOff, best = off, k
			}
			if off < bestOff {
				bestOff = off
			}
		}
		if best < 0 {
			if bestOff <= tolTicks {
				farOff++
			} else {
				noNear++
			}
			continue
		}
		hit := dstRefs[best]
		offs = append(offs, hit.pos-want)
		keys := append([]*cbor.Node(nil), hit.node.Keys...)
		vals := append([]*cbor.Node(nil), hit.node.Values...)
		replaced := false
		for k, key := range keys {
			if key.KeyString() == "vibrato" {
				vals[k] = vib
				replaced = true
				break
			}
		}
		if !replaced {
			keys = append(keys, &cbor.Node{Kind: cbor.KText, Text: "vibrato"})
			vals = append(vals, vib)
		}
		grown := &cbor.Node{Kind: cbor.KMap, Keys: keys, Values: vals}
		edits = append(edits, cbor.Edit{Start: hit.node.Start, End: hit.node.End, Bytes: cbor.EncodeNode(grown),
			What: fmt.Sprintf("颤音：写到 tick %d 的音符上（起点差 %d tick）", hit.pos, hit.pos-want)})
		wrote++
	}
	if srcN == 0 {
		return nil, "源区间里没有带颤音的音符", nil
	}
	msg := fmt.Sprintf("源 %d 个带颤音的音符 → 写到 %d 个目标音符（按最近邻配对，容忍 ±%d tick）", srcN, wrote, tolTicks)
	if len(offs) > 0 {
		var mn, mx int64 = offs[0], offs[0]
		var sum int64
		for _, o := range offs {
			if o < mn {
				mn = o
			}
			if o > mx {
				mx = o
			}
			sum += o
		}
		msg += fmt.Sprintf("；起点差 %d..%d tick（均值 %d）", mn, mx, sum/int64(len(offs)))
	}
	if farOff > 0 {
		msg += fmt.Sprintf("；%d 个目标处只有不同音高的音符（未硬配）", farOff)
	}
	if noNear > 0 {
		msg += fmt.Sprintf("；%d 个目标处 ±%d tick 内没有音符", noNear, tolTicks)
	}
	return edits, msg, nil
}

'''
s = s[:start] + new + s[end:]
io.open(p, 'w', encoding='utf-8').write(s)
print('patched vibrato matching')
