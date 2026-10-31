package tools

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"acetool/internal/cbor"
)

// 列出**当前片段** `pitchDelta` lane 里的内容（只读）—— 语调教写完之后拿它核对。
//
// 用户 2026-10-10：「语调教锚点有列表吗？打出了确认一下」⇒ 这个工具把 lane 里的
// **锚点逐点**（tick → 绝对音高、pointsVUV、相对音符音高的偏移）+ **曲线 entry 的概况**（offset/样本数/覆盖 tick）
// 打成一份人话列表，并认出"这一点落在哪个音符上""这套点像哪种声调（照 `tone.apply` 的调型表反查）"。
//
// 只读：不写文件、不碰 ACE 内存。片段按**编辑器当前打开的那条**（`editor status`）找，
// 再按交叠把它对回文件里的 pattern（与 `pitch.xcopy` 同一套映射）。
func init() {
	register(&Tool{
		ID: "tone.anchors", Category: "工程",
		Title: "列出音高锚点（语调教的结果，只读）",
		Desc: "读**当前片段**的 `parameters.pitchDelta` lane：**锚点逐点**列出（第几拍 → 绝对音高、" +
			"`pointsVUV`、相对所在音符音高的半音偏移），并认出「这一点落在哪个音符（歌词/音高）上」" +
			"以及「这一套点像哪种声调」（照 `tone.apply` 的调型表反查：阴平/阳平/上声/去声）；" +
			"曲线（画法① `data`）只报概况（offset / 样本数 / 覆盖的拍区间）。\n" +
			"`path` 留空就自己去问当前工程（`project dirty`）。⚠️ 位置一律是**拍**（1 拍 = 四分音符）。只读，不改任何东西。",
		Kind:   KindRead,
		Source: "技能 acep v1.2.3 §2.2（画法①/②）/ §6 `lane-report`（LANE 报表的口径）",
		Params: []Param{
			textParam("path", "工程文件（.acep，留空 = 用当前工程）", "", `C:\…\名字\名字.acep`, "一般留空"),
			numParam("trackIndex", "轨道（0 起，留空 = 编辑器当前那条）", ""),
			numParam("clipBegin", "片段起点（拍，留空 = 编辑器当前那条）", ""),
			numParam("clipEnd", "片段终点（拍，可留空）", ""),
		},
		Inspect: toneAnchorsInspect,
	})
}

type toneAnchorPoint struct {
	Entry  int     `json:"entry"`
	Tick   int64   `json:"tick"`
	Pitch  float64 `json:"音高（绝对）"`
	VUV    float64 `json:"pointsVUV"`
	Offset float64 `json:"相对音符（半音）"`
	Note   string  `json:"落在哪个音符"`
	Tone   string  `json:"像哪种声调,omitempty"`
}

type toneAnchorReport struct {
	Path    string            `json:"工程文件"`
	Track   string            `json:"轨道"`
	Clip    string            `json:"片段"`
	Entries []string          `json:"lane 里的 entry"`
	Points  []toneAnchorPoint `json:"锚点逐点"`
	Lines   []string          `json:"列表"`
	Summary string            `json:"小结"`
	Note    string            `json:"说明,omitempty"`
}

func toneAnchorsInspect(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	p := acepPathParam(vals)
	if p == "" {
		pr, err := readProjectPath(ctx, rd)
		if err != nil {
			return nil, fmt.Errorf("拿当前工程路径失败（`project dirty`）：%w", err)
		}
		p = pr.Path
		if p == "" {
			return nil, fmt.Errorf("这份工程还没保存过（临时工程，拿不到文件路径）—— 先在 ACE 里保存/另存为，再来列")
		}
	}
	// 片段：默认问 ACE（编辑器当前那条）；三条参数都给全了就**不碰 CLI**（测试/离线核对也能用）
	var ref clipRef
	if v := strings.TrimSpace(value(vals, "trackIndex", "")); v != "" {
		ti, err := parseIntParam(vals, "trackIndex", "轨道")
		if err != nil {
			return nil, err
		}
		ref.Track = ti
		ref.Clip = 0
		ref.ClipName = "（参数指定的片段）"
		if s := strings.TrimSpace(value(vals, "clipBegin", "")); s != "" {
			// ⚠️ 单位口径（用户 2026-10-14）：参数收的是**拍**，内部换算成 tick
			if beat, err := strconv.ParseFloat(s, 64); err == nil {
				ref.ClipBegin = beatsToTicks(beat)
			}
		}
		if s := strings.TrimSpace(value(vals, "clipEnd", "")); s != "" {
			if beat, err := strconv.ParseFloat(s, 64); err == nil {
				ref.ClipEnd = beatsToTicks(beat)
			}
		}
	} else {
		r, err := currentClip(ctx, rd)
		if err != nil {
			return nil, err
		}
		ref = r
	}

	_, root, err := openACEPRoot(p)
	if err != nil {
		return nil, err
	}
	pats, err := patternsOnTrack(root, ref.Track)
	if err != nil {
		return nil, err
	}
	hi := ref.ClipEnd
	if hi <= ref.ClipBegin {
		hi = ref.ClipBegin + 1
	}
	pat, note2 := pickPatternForWindow(pats, ref.ClipBegin, hi, "当前片段")
	if pat == nil {
		return nil, fmt.Errorf("在工程文件里找不到与当前片段（轨 %d「%s」第 %s–%s 拍）交叠的 pattern —— %s",
			ref.Track, ref.ClipName, beatLabel(ref.ClipBegin), beatLabel(hi), note2)
	}

	rep := &toneAnchorReport{
		Path:  p,
		Track: fmt.Sprintf("轨 %d（%s）", ref.Track, orDash(ref.TrackName)),
		Clip:  fmt.Sprintf("片段 %d（%s）第 %s–%s 拍", ref.Clip, orDash(ref.ClipName), beatLabel(ref.ClipBegin), beatLabel(hi)),
	}
	notes := noteList(pat)
	lane := laneOf(pat, "pitchDelta")
	if lane == nil || lane.Kind != cbor.KArray {
		rep.Summary = "这个片段**没有 `pitchDelta` lane** —— 没有锚点、也没有曲线。"
		rep.Lines = []string{rep.Summary}
		return rep, nil
	}

	anchorEntries, dataEntries, otherEntries := 0, 0, 0
	pointsTotal, samplesTotal := 0, 0
	for i, e := range lane.Items {
		switch nodeText(e.Get("type")) {
		case "anchor":
			anchorEntries++
			pn := e.Get("points")
			if pn == nil || pn.Kind != cbor.KArray {
				rep.Entries = append(rep.Entries, fmt.Sprintf("entry %d `anchor`：points 读不出来", i))
				continue
			}
			vn := e.Get("pointsVUV")
			var ticks []int64
			// ⚠️ 音符是**首尾相接**的（一个音的收尾 tick 正好是下一个音的起点）⇒ 一个 tick 可能同时落在
			// 两个音符的边界上。所以**按 entry 认领音符**（用它的首点找），这一条里的点都相对那个音符算偏移，
			// 免得"这条锚点到底是哪个音的"被边界搞乱。
			note := noteForTick(notes, firstTick(pn))
			noteDesc, notePitch := "（不在任何音符上）", 0.0
			if note != nil {
				pos, _ := nodeInt(note.Get("pos"))
				dur, _ := nodeInt(note.Get("dur"))
				notePitch, _ = nodeFloat(note.Get("pitch"))
				noteDesc = fmt.Sprintf("「%s」(音高 %g, 第 %s–%s 拍)", orDash(nodeText(note.Get("lyric"))), notePitch, beatLabel(pos), beatLabel(pos+dur))
			}
			for k := 0; k+1 < len(pn.Items); k += 2 {
				t, ok := nodeInt(pn.Items[k])
				if !ok {
					continue
				}
				v, _ := nodeFloat(pn.Items[k+1])
				var vuv float64
				if vn != nil && vn.Kind == cbor.KArray && len(vn.Items) > k/2 {
					vuv, _ = nodeFloat(vn.Items[k/2])
				}
				rep.Points = append(rep.Points, toneAnchorPoint{
					Entry: i, Tick: t, Pitch: v, VUV: vuv, Offset: v - notePitch, Note: noteDesc,
				})
				ticks = append(ticks, t)
			}
			pointsTotal += len(ticks)
			kind := "点集认不出来（可能是 ACE 里手画的）"
			if n := toneNameOfEntry(e, notes, ticks); n != "" {
				kind = "整条 = " + n
			}
			rng := "空"
			if len(ticks) > 0 {
				rng = fmt.Sprintf("第 %s–%s 拍", beatLabel(ticks[0]), beatLabel(ticks[len(ticks)-1]))
			}
			rep.Entries = append(rep.Entries, fmt.Sprintf("entry %d `anchor`：%d 个点 · %s · 覆盖 %s · %s",
				i, len(ticks), rng, noteDesc, kind))
		case "data":
			dataEntries++
			vals := e.Get("values")
			if vals == nil || vals.Kind != cbor.KArray {
				rep.Entries = append(rep.Entries, fmt.Sprintf("entry %d `data`：没有 values", i))
				continue
			}
			off, _ := nodeInt(e.Get("offset"))
			n := int64(len(vals.Items))
			samplesTotal += int(n)
			rep.Entries = append(rep.Entries, fmt.Sprintf("entry %d `data`（音高线曲线）：offset %d · %d 个样本 · 覆盖第 %s–%s 拍",
				i, off, n, beatLabel(off), beatLabel(int64(sampleTickF(off, n-1)))))
		default:
			otherEntries++
			rep.Entries = append(rep.Entries, fmt.Sprintf("entry %d `%s`：不认识的类型（没动它）", i, nodeText(e.Get("type"))))
		}
	}
	rep.Summary = fmt.Sprintf("lane 里 %d 条 entry：锚点 %d 条（%d 个点）· 曲线 %d 条（%d 个样本）· 其它 %d 条",
		len(lane.Items), anchorEntries, pointsTotal, dataEntries, samplesTotal, otherEntries)

	// 人话列表（页面/命令行直接打出来看）
	for _, l := range rep.Entries {
		rep.Lines = append(rep.Lines, l)
	}
	if len(rep.Points) == 0 {
		rep.Lines = append(rep.Lines, "（一条锚点都没有）")
	} else {
		rep.Lines = append(rep.Lines, "锚点逐点：")
		sort.SliceStable(rep.Points, func(i, j int) bool { return rep.Points[i].Tick < rep.Points[j].Tick })
		for _, pt := range rep.Points {
			rep.Lines = append(rep.Lines, fmt.Sprintf("  第 %s 拍 → 音高 %g（%+.0f 半音）· pointsVUV %g · %s",
				beatLabel(pt.Tick), pt.Pitch, pt.Offset, pt.VUV, pt.Note))
		}
	}
	rep.Note = "音高是**绝对音高**（半音，含小数）；「相对音符」= 该音高 − 所在音符的音高。" +
		"声调反查用的是 `tone.apply` 的调型表（阴平 平 / 阳平 先 −4 / 上声 先 −8 / 去声 end−1 处 −12；" +
		"低平段：二声 15%、三声 50%）。"
	return rep, nil
}

// noteForTick 找出覆盖 tick 的音符节点（首尾相接时取 pos == tick 的那条 —— 也就是"从这个音开始"）。
func noteForTick(notes []*cbor.Node, tick int64) *cbor.Node {
	var cover *cbor.Node
	for _, n := range notes {
		pos, ok1 := nodeInt(n.Get("pos"))
		dur, ok2 := nodeInt(n.Get("dur"))
		if !ok1 || !ok2 {
			continue
		}
		if pos == tick {
			return n
		}
		if cover == nil && tick >= pos && tick < pos+dur {
			cover = n
		}
	}
	return cover
}

// firstTick 取一条 anchor entry 的首点 tick（读不出来回 0）。
func firstTick(pn *cbor.Node) int64 {
	if pn == nil || pn.Kind != cbor.KArray || len(pn.Items) < 2 {
		return 0
	}
	t, _ := nodeInt(pn.Items[0])
	return t
}

// toneNameOfEntry 拿整条 entry 的点集去比调型表 —— 比中哪一档就报哪个名字（一个音符一条 entry 的情况最准）。
func toneNameOfEntry(e *cbor.Node, notes []*cbor.Node, ticks []int64) string {
	if len(ticks) == 0 {
		return ""
	}
	// 这条 entry 覆盖在哪个音符上（用首点找）
	var note *cbor.Node
	for _, n := range notes {
		pos, ok1 := nodeInt(n.Get("pos"))
		dur, ok2 := nodeInt(n.Get("dur"))
		if !ok1 || !ok2 {
			continue
		}
		if ticks[0] >= pos && ticks[0] < pos+dur {
			note = n
			break
		}
	}
	if note == nil {
		return ""
	}
	pos, _ := nodeInt(note.Get("pos"))
	dur, _ := nodeInt(note.Get("dur"))
	np, _ := nodeFloat(note.Get("pitch"))
	if dur <= 0 {
		return ""
	}
	for _, t := range []byte{'1', '2', '3', '4'} {
		want, vuv := tonePoints(toneItem{tone: t, pos: pos, dur: dur, pitch: np})
		if samePoints(e, want, vuv) {
			return toneNameZh(t)
		}
	}
	return ""
}

func toneNameZh(t byte) string {
	switch t {
	case '1':
		return "阴平（平）"
	case '2':
		return "阳平（先 −4 再升）"
	case '3':
		return "上声（先 −8 再升）"
	case '4':
		return "去声（end−1 处 −12）"
	}
	return ""
}

// noteList 取 pattern 里的音符节点（按 pos 排序）。
func noteList(pat *cbor.Node) []*cbor.Node {
	notes := pat.Get("notes")
	if notes == nil || notes.Kind != cbor.KArray {
		return nil
	}
	out := append([]*cbor.Node(nil), notes.Items...)
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := nodeInt(out[i].Get("pos"))
		b, _ := nodeInt(out[j].Get("pos"))
		return a < b
	})
	return out
}
