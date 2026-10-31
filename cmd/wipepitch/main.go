// 抹除一个 .acep 里**所有轨/片段的音高线**（pitchDelta 的 anchor 与 data 条目全部清空）。
// 用法：wipepitch <acep> [--write]
package main

import (
	"fmt"
	"os"
	"time"

	"acetool/internal/acep"
	"acetool/internal/cbor"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("用法: wipepitch <acep> [--write]")
		os.Exit(2)
	}
	path := os.Args[1]
	write := false
	wipeVib := false
	for _, a := range os.Args[2:] {
		switch a {
		case "--write":
			write = true
		case "--vibrato":
			wipeVib = true
		}
	}

	cont, err := acep.Open(path)
	must(err, "打开工程")
	root, err := cbor.Decode(cont.Payload)
	must(err, "解 payload")

	var edits []cbor.Edit
	vibEdits := []cbor.Edit{}
	lanes := 0
	entries := 0
	tracks := root.Get("tracks")
	if tracks != nil && tracks.Kind == cbor.KArray {
		for ti, t := range tracks.Items {
			ps := t.Get("patterns")
			if ps == nil || ps.Kind != cbor.KArray {
				continue
			}
			for pi, p := range ps.Items {
				lane := laneOf(p, "pitchDelta")
				if lane == nil || len(lane.Items) == 0 {
					continue
				}
				lanes++
				entries += len(lane.Items)
				edits = append(edits, cbor.Edit{
					Start: lane.Start, End: lane.End, Bytes: cbor.EncodeArrayHeader(0),
					What: fmt.Sprintf("轨 %d 片段 %d：清空 %d 条音高", ti, pi, len(lane.Items)),
				})
			}
		}
	}
	if wipeVib && tracks != nil && tracks.Kind == cbor.KArray {
		vibCount := 0
		for _, t := range tracks.Items {
			ps := t.Get("patterns")
			if ps == nil || ps.Kind != cbor.KArray {
				continue
			}
			for _, p := range ps.Items {
				ns := p.Get("notes")
				if ns == nil || ns.Kind != cbor.KArray {
					continue
				}
				for _, n := range ns.Items {
					if n.Kind != cbor.KMap || n.Get("vibrato") == nil {
						continue
					}
					var body []byte
					count := uint64(0)
					for i := range n.Keys {
						if n.Keys[i].KeyString() == "vibrato" {
							continue
						}
						body = append(body, cbor.EncodeNode(n.Keys[i])...)
						body = append(body, cbor.EncodeNode(n.Values[i])...)
						count++
					}
					raw := append(cbor.EncodeMapHeader(count), body...)
					vibEdits = append(vibEdits, cbor.Edit{Start: n.Start, End: n.End, Bytes: raw, What: "去掉颤音"})
					vibCount++
				}
			}
		}
		edits = append(edits, vibEdits...)
		fmt.Printf("待抹颤音：%d 个音符\n", vibCount)
	}
	fmt.Printf("待清空：%d 条 lane / %d 条 entry\n", lanes, entries)
	if len(edits) == 0 {
		fmt.Println("没有音高线，无需处理")
		return
	}
	out, err := cbor.Apply(cont.Payload, edits)
	must(err, "套用编辑")
	root2, err := cbor.Decode(out)
	must(err, "重解新 payload")
	left := 0
	if tracks2 := root2.Get("tracks"); tracks2 != nil {
		for _, t := range tracks2.Items {
			ps := t.Get("patterns")
			if ps == nil {
				continue
			}
			for _, p := range ps.Items {
				if l := laneOf(p, "pitchDelta"); l != nil {
					left += len(l.Items)
				}
			}
		}
	}
	fmt.Printf("复核：剩余音高 entry %d 条\n", left)
	if !write {
		fmt.Println("（只核对，没写。要写加 --write）")
		return
	}
	repacked, err := cont.Repack(out)
	must(err, "重打包")
	bak := path + ".bak-wipe-" + time.Now().Format("20060102-150405")
	must(os.WriteFile(bak, cont.RawBytes(), 0o644), "备份原文件")
	tmp := path + ".wipe-tmp"
	must(os.WriteFile(tmp, repacked, 0o644), "写临时文件")
	must(os.Rename(tmp, path), "替换原文件")
	fmt.Println("已写入：", path)
	fmt.Println("原文件备份：", bak)
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

func must(err error, what string) {
	if err != nil {
		fmt.Printf("%s失败：%v\n", what, err)
		os.Exit(1)
	}
}
