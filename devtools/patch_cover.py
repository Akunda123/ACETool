# 给 pitchwithin 加「覆盖目标区间」：写锚点前先清掉目标区间里的旧锚点
import io
import re

p = 'internal/tools/pitchwithin.go'
s = io.open(p, encoding='utf-8').read()

# ① pitchPlan 加字段
if 'trimAnchors' not in s:
    s = s.replace('	edits []cbor.Edit', '	edits []cbor.Edit\n	// trimAnchors = 写锚点前先清掉目标区间里的旧锚点（覆盖语义）\n	trimAnchors bool\n	trimLo      int64\n	trimHi      int64', 1)

# ② plan 里记下目标区间
s = s.replace('''		add = append(add, entries...)
		rep.Anchor = pitchKindReport{On: true, Summary: msg}''',
'''		add = append(add, entries...)
		plan.trimAnchors = true
		plan.trimLo, plan.trimHi = dLo, dHi
		rep.Anchor = pitchKindReport{On: true, Summary: msg}''', 1)

# ③ finishPitchPlan：有 trimAnchors 且没有别的编辑落在 lane 里 ⇒ 整条 lane 重建
s = s.replace('''	// 新 entry **统一插一次**（数组头只改一次；多条一起插，顺序按时间排）
	if len(plan.add) > 0 {
		lane := laneOf(plan.dstPat, "pitchDelta")
		if lane == nil {
			return nil, fmt.Errorf("目标片段没有 pitchDelta lane，却有要追加的 entry（内部不一致）")
		}
		sort.SliceStable(plan.add, func(i, j int) bool { return entryKey(plan.add[i]) < entryKey(plan.add[j]) })
		e, err := appendLaneEntries(lane, plan.add, "音高线：追加 entry")
		if err != nil {
			return nil, err
		}
		plan.edits = append(plan.edits, e...)
	}''',
'''	// 新 entry **统一插一次**（数组头只改一次；多条一起插，顺序按时间排）
	if len(plan.add) > 0 {
		lane := laneOf(plan.dstPat, "pitchDelta")
		if lane == nil {
			return nil, fmt.Errorf("目标片段没有 pitchDelta lane，却有要追加的 entry（内部不一致）")
		}
		sort.SliceStable(plan.add, func(i, j int) bool { return entryKey(plan.add[i]) < entryKey(plan.add[j]) })

		// 覆盖语义：先清掉目标区间里的旧锚点，再写新的（否则重复执行会叠加）
		conflict := false
		if plan.trimAnchors {
			for _, e := range plan.edits {
				if e.Start >= lane.Start && e.End <= lane.End {
					conflict = true
					break
				}
			}
		}
		if plan.trimAnchors && !conflict {
			ed, note, err := rebuildLaneCovering(lane, plan.add, plan.trimLo, plan.trimHi)
			if err != nil {
				return nil, err
			}
			plan.edits = append(plan.edits, ed...)
			rep.Anchor.Summary += "；" + note
		} else {
			if plan.trimAnchors && conflict {
				rep.Anchor.Summary += "；目标区间已同时改了曲线，本次不覆盖旧锚点（避免同一条 lane 两处编辑打架）"
			}
			e, err := appendLaneEntries(lane, plan.add, "音高线：追加 entry")
			if err != nil {
				return nil, err
			}
			plan.edits = append(plan.edits, e...)
		}
	}''', 1)

# ④ 新增 rebuildLaneCovering
if 'func rebuildLaneCovering' not in s:
    s = s.replace('// findPatternByUUID 在 tracks[*].patterns[*] 里按 uuid 找片段',
'''// rebuildLaneCovering 把整条 pitchDelta lane 重建成：
// 原有条目（**目标区间里的锚点先被剔掉**）+ 新 entry，按时间排序。
//
// 这样重复执行只会替换目标区间那一段，不会叠出两条一模一样的 anchor。
func rebuildLaneCovering(lane *cbor.Node, add []*cbor.Node, lo, hi int64) ([]cbor.Edit, string, error) {
	var kept []*cbor.Node
	removedPts, removedEntries := 0, 0
	for _, e := range lane.Items {
		if nodeText(e.Get("type")) != "anchor" {
			kept = append(kept, e)
			continue
		}
		pn := e.Get("points")
		if pn == nil || pn.Kind != cbor.KArray || len(pn.Items)%2 != 0 {
			kept = append(kept, e)
			continue
		}
		vn := e.Get("pointsVUV")
		var pts []any
		var vuv []float64
		for k := 0; k+1 < len(pn.Items); k += 2 {
			t, ok := nodeInt(pn.Items[k])
			if !ok {
				pts = append(pts, pn.Items[k], pn.Items[k+1])
				continue
			}
			if t >= lo && t < hi {
				removedPts++
				continue
			}
			pts = append(pts, pn.Items[k], pn.Items[k+1])
			var v float64
			if vn != nil && vn.Kind == cbor.KArray && len(vn.Items) > k/2 {
				v = vn.Items[k/2].Float
			}
			vuv = append(vuv, v)
		}
		if len(pts) == 0 {
			removedEntries++
			continue
		}
		raw := cbor.EncodeMap(
			[2]any{"type", "anchor"},
			[2]any{"points", pts},
			[2]any{"pointsVUV", vuv},
		)
		n, err := cbor.Decode(raw)
		if err != nil {
			return nil, "", err
		}
		kept = append(kept, n)
	}
	all := append(kept, add...)
	sort.SliceStable(all, func(i, j int) bool { return entryKey(all[i]) < entryKey(all[j]) })
	var body []byte
	for _, n := range all {
		body = append(body, cbor.EncodeNode(n)...)
	}
	out := append(cbor.EncodeArrayHeader(uint64(len(all))), body...)
	note := fmt.Sprintf("覆盖目标区间：清掉 %d 个点/%d 条旧锚点，写 %d 条，lane 共 %d 条",
		removedPts, removedEntries, len(add), len(all))
	return []cbor.Edit{{
		Start: lane.Start, End: lane.End, Bytes: out,
		What: "音高线：" + note,
	}}, note, nil
}

// findPatternByUUID 在 tracks[*].patterns[*] 里按 uuid 找片段''', 1)

io.open(p, 'w', encoding='utf-8').write(s)
print('patched:', 'rebuildLaneCovering' in s, 'trimAnchors' in s)
