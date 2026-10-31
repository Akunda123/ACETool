// 手工执行一次「轨道内复制音高线」：锚点（按源条目分开搬）+ 音符颤音属性。
// 用法：manualpatch <acep> <clipUuid> <srcLo> <srcHi> <dstLo> [--write]
// 默认只核对；--write 才写文件（先在同目录留 .bak-时间戳）。
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"acetool/internal/acep"
	"acetool/internal/cbor"
)

func main() {
	args := os.Args[1:]
	write := false
	var pos []string
	for _, a := range args {
		if a == "--write" {
			write = true
		} else {
			pos = append(pos, a)
		}
	}
	if len(pos) < 5 {
		fmt.Println("用法: manualpatch <acep> <clipUuid> <srcLo> <srcHi> <dstLo> [--write]")
		os.Exit(2)
	}
	path, clipUUID := pos[0], pos[1]
	var srcLo, srcHi, dstLo int64
	fmt.Sscan(pos[2], &srcLo)
	fmt.Sscan(pos[3], &srcHi)
	fmt.Sscan(pos[4], &dstLo)
	dHi := dstLo + (srcHi - srcLo)
	delta := dstLo - srcLo

	cont, err := acep.Open(path)
	must(err, "打开工程")
	root, err := cbor.Decode(cont.Payload)
	must(err, "解 payload")
	_, pat := findPattern(root, clipUUID)
	if pat == nil {
		fmt.Println("找不到片段", clipUUID)
		os.Exit(1)
	}
	lane := laneOf(pat, "pitchDelta")
	fmt.Printf("片段 %s；lane %d 条 entry\n", clipUUID, len(lane.Items))

	// ① 颤音：源音符 vibrato → 目标音符（按 pos 平移配对）
	notes := pat.Get("notes")
	srcVib := map[int64]*cbor.Node{}
	byPos := map[int64]*cbor.Node{}
	vibSrc := 0
	if notes != nil && notes.Kind == cbor.KArray {
		for _, n := range notes.Items {
			p, ok := numI64(n.Get("pos"))
			if !ok {
				continue
			}
			if _, dup := byPos[p]; !dup {
				byPos[p] = n
			}
			if p >= srcLo && p < srcHi {
				if vb := n.Get("vibrato"); vb != nil && vb.Kind == cbor.KMap {
					srcVib[p+delta] = vb
					vibSrc++
				}
			}
		}
	}

	var noteBodies [][]byte
	vibHit, vibMiss, changedNotes := 0, 0, 0
	if notes != nil && notes.Kind == cbor.KArray {
		for _, n := range notes.Items {
			p, _ := numI64(n.Get("pos"))
			vb, want := srcVib[p]
			if !want { // 两段起点常有几 tick 量化差 ⇒ 阈值内找最近的一条
				for o := int64(1); o <= 30 && !want; o++ {
					if v, ok := srcVib[p-o]; ok {
						vb, want = v, true
						break
					}
					if v, ok := srcVib[p+o]; ok {
						vb, want = v, true
					}
				}
			}
			if !want || n.Kind != cbor.KMap || p < dstLo || p >= dHi {
				noteBodies = append(noteBodies, cbor.EncodeNode(n))
				continue
			}
			vibHit++
			var body []byte
			count := uint64(0)
			replaced := false
			for i := 0; i < len(n.Keys); i++ {
				k, v := n.Keys[i], n.Values[i]
				if nodeText(k) == "vibrato" {
					body = append(body, cbor.EncodeNode(k)...)
					body = append(body, cbor.EncodeNode(vb)...)
					replaced = true
				} else {
					body = append(body, cbor.EncodeNode(k)...)
					body = append(body, cbor.EncodeNode(v)...)
				}
				count++
			}
			if !replaced {
				body = append(body, cbor.EncodeText("vibrato")...)
				body = append(body, cbor.EncodeNode(vb)...)
				count++
			}
			changedNotes++
			noteBodies = append(noteBodies, append(cbor.EncodeMapHeader(count), body...))
		}
	}
	for p := range srcVib {
		if byPos[p] == nil {
			vibMiss++
		}
	}
	fmt.Printf("DEBUG notes[%d,%d] lane[%d,%d] changed=%d\n", notes.Start, notes.End, lane.Start, lane.End, changedNotes)
	fmt.Printf("颤音：源 %d 个音符带颤音 → 命中目标 %d，未命中 %d\n", vibSrc, vibHit, vibMiss)

	payloadA := cont.Payload
	if notes != nil && notes.Kind == cbor.KArray && changedNotes > 0 {
		var nb []byte
		nb = append(nb, cbor.EncodeArrayHeader(uint64(len(noteBodies)))...)
		for _, b := range noteBodies {
			nb = append(nb, b...)
		}
		out := make([]byte, 0, len(payloadA)-int(notes.End-notes.Start)+len(nb))
		out = append(out, payloadA[:notes.Start]...)
		out = append(out, nb...)
		out = append(out, payloadA[notes.End:]...)
		payloadA = out
	}

	// ② 锚点：在缩放后的 payload 上重新解码，按源条目逐条搬 + 覆盖目标区间
	rootA, err := cbor.Decode(payloadA)
	must(err, "重解（应用颤音后）")
	_, patA := findPattern(rootA, clipUUID)
	laneA := laneOf(patA, "pitchDelta")
	kept, added, srcEntries, srcPts, tgtEntries, tgtPtsBefore, removedPts, removedEntries := splitAnchors(laneA, srcLo, srcHi, dstLo, dHi, delta)
	fmt.Printf("锚点：源 %d 条 entry / %d 点 → 逐条搬 %d 条；目标原有 %d 条 / %d 点（清掉 %d 点 / %d 条）\n",
		srcEntries, srcPts, len(added), tgtEntries, tgtPtsBefore, removedPts, removedEntries)

	all := append(kept, added...)
	sort.SliceStable(all, func(i, j int) bool { return firstTick(all[i]) < firstTick(all[j]) })
	var body []byte
	for _, n := range all {
		body = append(body, cbor.EncodeNode(n)...)
	}
	newLane := append(cbor.EncodeArrayHeader(uint64(len(all))), body...)
	newPayload := make([]byte, 0, len(payloadA)-int(laneA.End-laneA.Start)+len(newLane))
	newPayload = append(newPayload, payloadA[:laneA.Start]...)
	newPayload = append(newPayload, newLane...)
	newPayload = append(newPayload, payloadA[laneA.End:]...)

	// ③ 复核
	root2, err := cbor.Decode(newPayload)
	must(err, "重解新 payload")
	_, pat2 := findPattern(root2, clipUUID)
	lane2 := laneOf(pat2, "pitchDelta")
	tgtAfterEntries, tgtAfterPts := 0, 0
	for _, e := range lane2.Items {
		c := 0
		for _, t := range ticksOf(e) {
			if t >= dstLo && t < dHi {
				c++
			}
		}
		if c > 0 {
			tgtAfterEntries++
			tgtAfterPts += c
		}
	}
	vibAfter := 0
	if ns2 := pat2.Get("notes"); ns2 != nil && ns2.Kind == cbor.KArray {
		for _, n := range ns2.Items {
			p, ok := numI64(n.Get("pos"))
			if !ok || p < dstLo || p >= dHi {
				continue
			}
			if vb := n.Get("vibrato"); vb != nil && vb.Kind == cbor.KMap {
				vibAfter++
			}
		}
	}
	fmt.Printf("DEBUG vib bytes: orig=%d payloadA=%d final=%d\n", bytes.Count(cont.Payload, []byte("vibrato")), bytes.Count(payloadA, []byte("vibrato")), bytes.Count(newPayload, []byte("vibrato")))
	fmt.Printf("复核：目标 anchor %d 条 / %d 点；目标颤音音符 %d 个；lane %d → %d；payload %d → %d B\n",
		tgtAfterEntries, tgtAfterPts, vibAfter, len(laneA.Items), len(lane2.Items), len(cont.Payload), len(newPayload))

	if !write {
		fmt.Println("（只核对，没写文件。要写加 --write）")
		return
	}
	out, err := cont.Repack(newPayload)
	must(err, "重打包")
	bak := filepath.Join(filepath.Dir(path), filepath.Base(path)+".bak-"+time.Now().Format("20060102-150405"))
	must(os.WriteFile(bak, cont.RawBytes(), 0o644), "备份原文件")
	tmp := path + ".tmp"
	must(os.WriteFile(tmp, out, 0o644), "写临时文件")
	must(os.Rename(tmp, path), "替换原文件")
	fmt.Println("已写入：", path)
	fmt.Println("原文件备份：", bak)
}

// splitAnchors 读出源锚点（逐条 entry）、剔掉目标区间旧点，返回 kept/added。
func splitAnchors(lane *cbor.Node, srcLo, srcHi, dstLo, dHi, delta int64) (kept, added []*cbor.Node, srcEntries, srcPts, tgtEntries, tgtPts, removedPts, removedEntries int) {
	for _, e := range lane.Items {
		if nodeText(e.Get("type")) != "anchor" {
			continue
		}
		nSrc, nTgt := 0, 0
		for _, t := range ticksOf(e) {
			if t >= srcLo && t < srcHi {
				nSrc++
			} else if t >= dstLo && t < dHi {
				nTgt++
			}
		}
		if nSrc > 0 {
			srcEntries++
			srcPts += nSrc
		}
		if nTgt > 0 {
			tgtEntries++
			tgtPts += nTgt
		}
	}
	for _, e := range lane.Items {
		if nodeText(e.Get("type")) != "anchor" {
			kept = append(kept, e)
			continue
		}
		items := pointItems(e)
		vn := e.Get("pointsVUV")
		var pts []any
		var vuv []float64
		for i := 0; i+1 < len(items); i += 2 {
			t, ok := numI64(items[i])
			if !ok {
				pts = append(pts, items[i], items[i+1])
				continue
			}
			if t >= dstLo && t < dHi {
				removedPts++
				continue
			}
			pts = append(pts, items[i], items[i+1])
			var v float64
			if vn != nil && vn.Kind == cbor.KArray && len(vn.Items) > i/2 {
				v = vn.Items[i/2].Float
			}
			vuv = append(vuv, v)
		}
		if len(pts) == 0 {
			removedEntries++
			continue
		}
		kept = append(kept, rebuildAnchor(pts, vuv, vn != nil))
	}
	for _, e := range lane.Items {
		if nodeText(e.Get("type")) != "anchor" {
			continue
		}
		items := pointItems(e)
		vn := e.Get("pointsVUV")
		var pts []any
		var vuv []float64
		for i := 0; i+1 < len(items); i += 2 {
			t, ok := numI64(items[i])
			if !ok || t < srcLo || t >= srcHi {
				continue
			}
			pts = append(pts, t+delta, items[i+1])
			var v float64
			if vn != nil && vn.Kind == cbor.KArray && len(vn.Items) > i/2 {
				v = vn.Items[i/2].Float
			}
			vuv = append(vuv, v)
		}
		if len(pts) > 0 {
			added = append(added, rebuildAnchor(pts, vuv, vn != nil))
		}
	}
	return
}

func rebuildAnchor(pts []any, vuv []float64, withVUV bool) *cbor.Node {
	pairs := [][2]any{{"type", "anchor"}, {"points", pts}}
	if withVUV {
		pairs = append(pairs, [2]any{"pointsVUV", vuv})
	}
	raw := cbor.EncodeMap(pairs...)
	n, err := cbor.Decode(raw)
	must(err, "重建条目")
	return n
}

func must(err error, what string) {
	if err != nil {
		fmt.Printf("%s失败：%v\n", what, err)
		os.Exit(1)
	}
}

func findPattern(root *cbor.Node, uuid string) (*cbor.Node, *cbor.Node) {
	tracks := root.Get("tracks")
	if tracks == nil || tracks.Kind != cbor.KArray {
		return nil, nil
	}
	for _, t := range tracks.Items {
		ps := t.Get("patterns")
		if ps == nil || ps.Kind != cbor.KArray {
			continue
		}
		for _, p := range ps.Items {
			if nodeText(p.Get("uuid")) == uuid {
				return t, p
			}
		}
	}
	return nil, nil
}

func laneOf(pat *cbor.Node, name string) *cbor.Node {
	ps := pat.Get("parameters")
	if ps == nil || ps.Kind != cbor.KMap {
		return nil
	}
	l := ps.Get(name)
	if l == nil || l.Kind != cbor.KArray {
		return nil
	}
	return l
}

func pointItems(e *cbor.Node) []*cbor.Node {
	pn := e.Get("points")
	if pn == nil || pn.Kind != cbor.KArray {
		return nil
	}
	return pn.Items
}

func ticksOf(e *cbor.Node) []int64 {
	items := pointItems(e)
	var out []int64
	for i := 0; i+1 < len(items); i += 2 {
		if t, ok := numI64(items[i]); ok {
			out = append(out, int64(t))
		}
	}
	return out
}

func firstTick(n *cbor.Node) float64 {
	for _, t := range ticksOf(n) {
		return float64(t)
	}
	return 1e18
}

func nodeText(n *cbor.Node) string {
	if n == nil || n.Kind != cbor.KText {
		return ""
	}
	return n.Text
}

func numI64(n *cbor.Node) (int64, bool) {
	if n == nil {
		return 0, false
	}
	switch n.Kind {
	case cbor.KInt:
		return n.Int, true
	case cbor.KUint:
		return int64(n.Uint), true
	case cbor.KFloat:
		return int64(n.Float), true
	}
	return 0, false
}
