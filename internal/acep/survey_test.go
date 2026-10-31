package acep

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"acetool/internal/cbor"
)

// 诊断用：把一份 .acep 里**非空**的参数 lane 全列出来（长度、前几个值、offset、type），
// 并顺带算出"值数组长度 ÷ 片段时长"这个比值 —— 音高曲线的**采样分辨率**就靠它推。
//
// ⚠️ 为什么要这么个东西：ACE 的 CLI **不暴露音高曲线**（`vocalparam layers` 里 `pitch`
// 恒 `available:false`，理由是"通道存 delta、画曲线收绝对音高"），所以想知道曲线长什么样、
// 数据怎么排，只能拆工程文件。这条是**只读**诊断，不写任何东西。
//
// 用法：ACEP_SURVEY=<文件路径> go test ./internal/acep/ -run TestSurveyParamLanes -v
func TestSurveyParamLanes(t *testing.T) {
	f := os.Getenv("ACEP_SURVEY")
	if f == "" {
		t.Skip("设 ACEP_SURVEY=<某个 .acep 路径> 来跑（只读诊断）")
	}
	c, err := Open(f)
	if err != nil {
		t.Fatalf("打开失败：%v", err)
	}
	if c.Info.Kind != KindACEP2 {
		t.Fatalf("这条诊断只认 ACEP2，实得 %s", c.Info.Kind)
	}
	root, err := cbor.Decode(c.Payload)
	if err != nil {
		t.Fatalf("解 CBOR 失败：%v", err)
	}
	tracks, err := cbor.Get(root, "tracks")
	if err != nil {
		t.Fatalf("取 tracks 失败：%v", err)
	}

	type row struct {
		track, pat, dur int
		lane            string
		n               int
		offset          float64
		typ             string
		head            string
		nonzero         int
		minV, maxV      float64
	}
	var rows []row
	var ratios []string

	for ti, tr := range tracks.Items {
		ty, _ := cbor.Get(tr, "type")
		if ty == nil || ty.Text != "sing" {
			continue
		}
		pats, _ := cbor.Get(tr, "patterns")
		if pats == nil {
			continue
		}
		for pi, pat := range pats.Items {
			durNode, _ := cbor.Get(pat, "dur")
			dur := 0
			if durNode != nil {
				dur = int(durNode.Float)
			}
			params, _ := cbor.Get(pat, "parameters")
			if params == nil {
				continue
			}
			keys := cbor.KeyNames(params, 999)
			sort.Strings(keys)
			for _, k := range keys {
				lane, _ := cbor.Get(params, k)
				if lane == nil || len(lane.Items) == 0 {
					continue
				}
				for _, seg := range lane.Items {
					offN, _ := cbor.Get(seg, "offset")
					typN, _ := cbor.Get(seg, "type")
					valsN, _ := cbor.Get(seg, "values")
					r := row{track: ti, pat: pi, dur: dur, lane: k}
					if offN != nil {
						r.offset = offN.Float
					}
					if typN != nil {
						r.typ = typN.Text
					}
					if valsN != nil && len(valsN.Items) > 0 {
						r.n = len(valsN.Items)
						// ⚠️ 值的**编码种类**决定"能不能原位覆盖"：
						//   只有整段同一种数值类型（且是定宽的 f64/f32）才能等长覆写。
						kinds := map[string]int{}
						for _, it := range valsN.Items {
							kinds[it.Kind.String()]++
						}
						var kparts []string
						for kk, nn := range kinds {
							kparts = append(kparts, fmt.Sprintf("%s×%d", kk, nn))
						}
						sort.Strings(kparts)
						r.head = strings.Join(kparts, " ")
						r.minV, r.maxV = valsN.Items[0].Float, valsN.Items[0].Float
						for _, it := range valsN.Items {
							v := it.Float
							if v != 0 {
								r.nonzero++
							}
							if v < r.minV {
								r.minV = v
							}
							if v > r.maxV {
								r.maxV = v
							}
						}
						if dur > 0 {
							ratios = append(ratios, fmt.Sprintf("%s: %d/%d = %.4f", k, r.n, dur, float64(r.n)/float64(dur)))
						}
					}
					rows = append(rows, r)
				}
			}
		}
	}

	fmt.Printf("\n文件：%s\n", f)
	if len(rows) == 0 {
		fmt.Println("  （没有非空的参数 lane —— 这条轨什么都没画）")
		return
	}
	fmt.Printf("  %-6s %-6s %-7s %-18s %6s %6s %8s %-9s %-6s %s\n",
		"轨道", "片段", "dur", "lane", "段", "个数", "非零", "offset", "type", "值编码 / 值域")
	for _, r := range rows {
		fmt.Printf("  %-6d %-6d %-7d %-18s %6d %6d %8d %-9g %-6s %s\n",
			r.track, r.pat, r.dur, r.lane, 1, r.n, r.nonzero, r.offset, r.typ,
			fmt.Sprintf("%s … [%g, %g]", r.head, r.minV, r.maxV))
	}
	if len(ratios) > 0 {
		fmt.Printf("\n  值数组长度 ÷ dur（推采样分辨率用）：\n")
		for _, s := range ratios {
			fmt.Printf("    %s\n", s)
		}
	}

	// 顺带把顶层与 note 上"可能跟音高有关"的键也摆出来（免得漏在别处）
	fmt.Printf("\n  顶层键：%s\n", strings.Join(cbor.KeyNames(root, 999), " "))
}

// 把某条 lane 的 values 原样导出成 JSON 数组（给"画曲线"做输入/对照用）。
// 用法：ACEP_SURVEY=<文件> ACEP_LANE=<lane 名，如 pitchDelta> go test ./internal/acep/ -run TestExportLane
func TestExportLane(t *testing.T) {
	f := os.Getenv("ACEP_SURVEY")
	lane := os.Getenv("ACEP_LANE")
	if f == "" || lane == "" {
		t.Skip("设 ACEP_SURVEY=<file> ACEP_LANE=<lane> 来跑")
	}
	c, err := Open(f)
	if err != nil {
		t.Fatal(err)
	}
	root, err := cbor.Decode(c.Payload)
	if err != nil {
		t.Fatal(err)
	}
	n, err := cbor.Get(root, "tracks[0].patterns[0].parameters."+lane+"[0].values")
	if err != nil || n == nil {
		t.Fatalf("取不到 %s：%v", lane, err)
	}
	out := make([]float64, 0, len(n.Items))
	for _, it := range n.Items {
		out = append(out, it.Float)
	}
	j, _ := json.Marshal(out)
	fmt.Printf("lane=%s 个数=%d\n%s\n", lane, len(out), string(j))
}
