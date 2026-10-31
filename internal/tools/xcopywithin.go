package tools

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// 移植 `轨道内复制.js`（AKD「轨道-轨道内复制」，322 行）。
//
// 它和 `跨轨道复制.js`（§23）是**同一个脚本体** —— 连 `copyParameter` / 双指针配对 /
// `capitalizeVocalMode` 都逐字相同。差别只有两处：
//
//	① **寻址**：不是"源轨 ↔ 目标轨"两条轨，而是**同一条轨（当前组）里两块小节范围**；
//	② **输入**：三个**小节号**（源起 / 源止 / 目标起），不是轨道下拉框。
//
// ⇒ 属性那一半（歌词 / 音高 / 音素）走 §23 抽出来的 `planPairProps` + `applyXcopyPairs`，
// 这个文件只负责"把小节号变成范围、把两边配上对"。
//
// 原脚本的参数半（9 条 SV 曲线）与 §23.3 同一个结论：**ACE 对不上，不做**。
//
// 小节号 → tick：原脚本自己写了 `p2b()`（遍历小节线 + 按拍号算），
// 这里改用 ACE 现成的 `convert measure-to-tick` —— 好处是**变拍号也跟着对**
//（原脚本那个公式只用到"目标小节自己的拍号"，跨拍号时其实会算偏）。

// measureToTick 把 **1 起**的小节号（可带小数：5.5 = 第 5 小节一半处）转成工程 tick。
//
// ⚠️ 小数部分是**在相邻两条小节线之间线性插值**得到的（问两次 `convert measure-to-tick`）——
// 这样不用自己假设"一小节多少 tick"，**变拍号也对**。
func measureToTick(ctx context.Context, rd ReadFunc, m float64, label string) (int64, error) {
	if m < 1 {
		return 0, fmt.Errorf("%s = %v：小节号从 **1** 起（原脚本的输入框也是这么标的）", label, m)
	}
	bar := int64(math.Floor(m))
	frac := m - float64(bar)

	at := func(b int64) (int64, error) {
		var out struct {
			Tick int64 `json:"tick"`
		}
		if err := readJSON(ctx, rd, &out, "convert", "measure-to-tick",
			"--bar", strconv.FormatInt(b, 10), "--tick-offset", "0", "--consider-beat-mode", "false"); err != nil {
			return 0, fmt.Errorf("把小节 %d 换成 tick（`convert measure-to-tick`）：%w", b, err)
		}
		return out.Tick, nil
	}
	t0, err := at(bar)
	if err != nil {
		return 0, err
	}
	if frac == 0 {
		return t0, nil
	}
	t1, err := at(bar + 1)
	if err != nil {
		return 0, err
	}
	return t0 + int64(math.Round(frac*float64(t1-t0))), nil
}

// ---------- 「轨内复制」窗口的读口 ----------

// xcopyViewReport 是 `xcopy.view` 的回包：**当前轨**的缩略图数据 + **当前片段**的坐标与小节口径。
//
// 为什么要单独一条（而不是让页面拼 `view.minimap` + `editor status`）：
//
//	页面画的是"**当前轨**的一张图 + 三个小节框"，而写下去的范围是"**当前片段**里的 tick"——
//	片段在轨上的位置、片段内 tick 与工程 tick 的差、一小节多少 tick，必须来自**同一次快照**，
//	否则画出来的框和真写的范围会差一截（分几次读中间用户切了片段就全错）。
type xcopyViewReport struct {
	TrackIndex int    `json:"trackIndex"`
	TrackName  string `json:"trackName,omitempty"`

	ClipUUID   string `json:"clipUuid"`
	ClipName   string `json:"clipName,omitempty"`
	ClipIndex  int    `json:"clipIndex"`
	ClipType   string `json:"clipType,omitempty"`
	ClipBegin  int64  `json:"clipBegin"` // 工程 tick
	ClipEnd    int64  `json:"clipEnd"`
	ClipTicks  int64  `json:"clipTicks"` // 片段内 tick 长度
	ClipStartB int    `json:"clipStartBar"`

	Meter       string  `json:"meter,omitempty"`
	TicksPerBar int64   `json:"ticksPerBar"`
	Tempo       float64 `json:"tempo,omitempty"`
	Duration    int64   `json:"durationTick,omitempty"`
	BarCount    int     `json:"barCount"`

	// 这条轨上的片段（含音符，`pos` 是**片段内** tick，绝对位置 = clip.begin + pos）
	Clips []minimapClip `json:"clips,omitempty"`
	// 当前片段里的音符数（写下去只会碰到它里面的音符）
	ClipNoteCount int `json:"clipNoteCount"`

	// ⭐ 参数花名册（**现读**）：页面据此给"写进哪一层"的勾选框（v1：包络/真值；v2：只有 user），
	// 以及"有没有 dynamic 这个参数"（v1 没有）。
	Engine string      `json:"engineGeneration,omitempty"`
	Params []paramInfo `json:"params,omitempty"`

	// ⭐ 参数表格的列（**由这份花名册推出来**，不写死世代名）：
	// v1 两列（实参 / 包络）、v2 一列（实参 ⇒ 写 `user`）。页面的表头、勾选格、以及发给工具的
	// `paramSel` 的列名都以这里为准 —— 列的含义与落点只在 Go 这一处定义（见 paramsel.go）。
	ParamColumns []paramColumn `json:"paramColumns,omitempty"`

	Note string `json:"note,omitempty"`
}

// paramColumn 是参数表格的一列：`key` 是发给工具的列名，`label` 是界面上的字，
// `dstLayer` 是这一列**落到目标哪个层**（页面上直接写出来，免得用户猜）。
type paramColumn struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	DstLayer string `json:"dstLayer"`
	Hint     string `json:"hint"`
}

// paramColumnsOf 把"这一份花名册该有哪几列"算出来。
// ⭐ 用户 2026-10-19 定稿：**包络在前、实参在后**（「v1 包络大于实参，包络优先」，跨版本同理）——
// 页面的默认勾选也跟着这条走（包络默认勾）。
func paramColumnsOf(ro *paramRoster) []paramColumn {
	effCol, envCol := paramPickColumns(ro)
	var union []string
	for _, p := range ro.Params {
		if p.Available {
			union = append(union, writableAnyLayers(ro, p.Param)...)
		}
	}
	var out []paramColumn
	if envCol {
		out = append(out, paramColumn{
			Key: "envelope", Label: "包络", DstLayer: "envelope",
			Hint: "包络是**乘数层**（围绕 1.0）⇒ 落进目标的 `envelope` 层（v1 才有这一层）",
		})
	}
	if effCol {
		dst := effectiveDstLayer(union)
		hint := "**只搬你画过的点**（v1 `direct` / v2 `user`），值取这些点上的合并实参 ⇒ 落进目标的 `" + dst + "` 层"
		if dst == "direct" {
			hint += "（真值层；实参是绝对值、不能写进包络那种乘数层）"
		} else {
			hint += "（这一代只有它）"
		}
		hint += "；空档不写也不清目标"
		out = append(out, paramColumn{Key: "effective", Label: "真值", DstLayer: dst, Hint: hint})
	}
	return out
}

func readXcopyView(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	// `trackIndex` 给了就**看那条轨的第一个片段**：跨轨复制窗口要按**目标轨**的花名册给层勾选框，
	// 而目标轨的引擎世代可能与当前轨不同 ⇒ 花名册必须跟着目标走。留空 = 编辑器里那个片段。
	wantTrack := -1
	if s := strings.TrimSpace(vals["trackIndex"]); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			wantTrack = n
		}
	}
	sc, err := readNoteScope(ctx, rd, false)
	if err != nil {
		return nil, err
	}
	if wantTrack >= 0 && wantTrack != sc.TrackIndex {
		clips, cerr := clipListBrief(ctx, rd, wantTrack)
		if cerr != nil {
			return nil, cerr
		}
		if len(clips) == 0 {
			return nil, fmt.Errorf("轨道 %d 上没有片段（换个目标轨，或先给它建一个片段）", wantTrack)
		}
		nc, nerr := readClipNotes(ctx, rd, wantTrack, clips[0])
		if nerr != nil {
			return nil, nerr
		}
		sc = &noteScope{
			TrackIndex: wantTrack, ClipIndex: clips[0].Index, ClipUUID: clips[0].UUID,
			ClipName: clips[0].Name, ClipType: clips[0].Type, Begin: clips[0].Begin, End: clips[0].End,
			Notes: nc.Notes, Located: fmt.Sprintf("指定轨道 %d 的第一个片段", wantTrack),
		}
	}
	if sc.ClipUUID == "" {
		return nil, fmt.Errorf("拿不到当前片段 —— 先在 ACE 里点开一个片段")
	}
	rep := &xcopyViewReport{
		TrackIndex: sc.TrackIndex, ClipUUID: sc.ClipUUID, ClipIndex: sc.ClipIndex,
		ClipName: sc.ClipName, ClipType: sc.ClipType,
		ClipBegin: sc.Begin, ClipEnd: sc.End, ClipTicks: sc.End - sc.Begin,
		ClipNoteCount: len(sc.Notes),
	}

	// 小节口径（读不到就按 4/4 兜底，并如实写在 note 里）
	var vr paramViewReport
	rep.TicksPerBar = ticksPerBarOf(ctx, rd, &vr)
	rep.Meter = vr.Meter
	if rep.Note = vr.Note; rep.Note != "" {
		rep.Note = strings.TrimSpace(rep.Note)
	}

	// 速度 / 工程时长（都容许失败）
	var tp struct {
		Points []struct {
			Value float64 `json:"value"`
		} `json:"points"`
		Tempo float64 `json:"tempo"`
	}
	if err := readJSON(ctx, rd, &tp, "tempo", "get"); err == nil {
		if tp.Tempo > 0 {
			rep.Tempo = tp.Tempo
		} else if len(tp.Points) > 0 {
			rep.Tempo = tp.Points[0].Value
		}
	}
	var info struct {
		Duration float64 `json:"duration"`
	}
	if err := readJSON(ctx, rd, &info, "project", "info"); err == nil {
		rep.Duration = int64(info.Duration)
	}

	// 这一条轨的缩略图（**只要这一条轨**）+ 它的音符
	mm, err := readMinimap(ctx, rd, map[string]string{
		"trackIndex": strconv.Itoa(sc.TrackIndex), "notes": "100",
	})
	if err != nil {
		return nil, err
	}
	if len(mm.Tracks) > 0 {
		rep.TrackName = mm.Tracks[0].Name
		rep.Clips = mm.Tracks[0].Clips
	}
	if rep.Duration <= 0 {
		rep.Duration = mm.Duration
	}
	if rep.TicksPerBar <= 0 {
		rep.TicksPerBar = 1920
	}
	if rep.Duration > 0 {
		rep.BarCount = int((rep.Duration + rep.TicksPerBar - 1) / rep.TicksPerBar)
	}

	// 参数花名册（**每次现读**）：页面要它来决定"写进哪一层"给几个勾选框
	if ro, rerr := readParamRoster(ctx, rd, sc.ClipUUID); rerr == nil {
		rep.Engine = ro.EngineGeneration
		for _, p := range ro.Params {
			pi := paramInfo{
				Param: p.Param, DisplayName: p.DisplayName, Available: p.Available, Reason: p.Unavailable,
				Scale: p.Scale, Min: p.ValueRange.Min, Max: p.ValueRange.Max,
			}
			for _, l := range p.Layers {
				pi.Layers = append(pi.Layers, paramLayerInfo{
					Layer: l.Layer, Access: l.Access, Role: l.Role, Shape: l.Shape, Sparse: l.Sparse,
				})
			}
			pi.Writable = writableAnyLayers(ro, p.Param)
			rep.Params = append(rep.Params, pi)
		}
		rep.ParamColumns = paramColumnsOf(ro)
	} else {
		rep.Note = appendNote(rep.Note, "⚠️ 读不到参数花名册（`vocalparam layers`）：参数那一半在页面上会没有层可选。")
	}
	return rep, nil
}

func init() {
	register(&Tool{
		ID: "xcopy.view", Category: "轨道编辑",
		Title: "轨内复制：当前轨一览（只读）",
		Desc: "给「轨内复制」窗口备一包**同一次快照**的数据：当前轨的名字与它的片段（含音符、片段内坐标）、" +
			"当前片段的工程起点与长度、拍号换算出一小节多少 tick、速度与工程时长。" +
			"⚠️ 只读；小节口径**现读** `timesig get`（读不到按 4/4 并在结果里说明）。",
		Kind:   KindRead,
		Source: "自建：`editor status` + `clip list` + `clip note-content` + `timesig get` + `tempo get` + `project info`",
		Params: []Param{
			textParam("trackIndex", "看哪条轨的片段（留空 = 编辑器里那个）", "", "1",
				"跨轨复制窗口按**目标轨**的花名册给层勾选框 —— 目标轨的引擎世代可能与当前轨不同"),
		},
		Inspect: func(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
			return readXcopyView(ctx, rd, vals)
		},
	})
}

func init() {
	register(&Tool{
		ID: "xcopy.within", Category: "轨道编辑",
		Title: "轨内复制（按小节 · 整条轨）",
		Desc: "移植 `轨道内复制.js`：把一段**小节范围**的内容复制到同一轨的另一处。" +
			"三个输入都是**小节号**（1 起，可带小数：`5.5` = 第 5 小节一半处）。" +
			"⭐ 作用域按用户 2026-10-07 定死：「**作用于轨内所有 clip，遍历绝对时间**」——" +
			"窗口是**工程绝对 tick**，可能横跨好几个片段（旧版只看编辑器里那一个片段，跨片段的部分会静默搬不到）。" +
			"⭐ 复制内容按用户 2026-10-07 的口径「**除 uuid 外全都要**」，分两类：" +
			"① **音符属性**：歌词 · 语种 · 音高 · 音素 · **时值**（`note resize`）· **辅音时长**（`phoneme set-consonant-timing`）；" +
			"② **参数曲线**：所有可用参数的**可写层**（源读**实参 effective**，逐层整段写）+ **dynamic 锚点**（单独一条路）。" +
			"`noteUuid` 不搬（目标音符保留自己的）；`pos` 是配对键（配上了就相等）。" +
			"配对方式照原脚本：源范围里的音符**整体平移到目标起点**，再与目标范围里的音符**按绝对位置**双指针配对。" +
			"⚠️ 只改**已经存在**的音符（照原脚本；目标范围里没有对应位置就什么都不做）。" +
			"⚠️ 参数曲线按目标片段**逐段**写：源侧没有片段覆盖的那段 tick **不写**（保留目标原值，不清空）。" +
			"⚠️ 移不过来的属性（ACE 没有）：" + xcopyUnportable + "。" +
			"**干跑默认**：勾着只出计划 + 逐条命令预览，一个字节都不写。",
		Kind:   KindWrite,
		Source: "AKD `轨道内复制.js`",
		Params: []Param{
			textParam("srcStart", "源开始小节", "", "1", "1 起；可带小数（1.5 = 第 1 小节一半处）"),
			textParam("srcEnd", "源结束小节", "", "3", "必须大于源开始小节"),
			textParam("dstStart", "目标开始小节", "", "5", "内容会整体平移到这个小节起点"),
			checkParam("lyrics", "复制歌词", true),
			checkParam("pitch", "复制音高", true),
		numParam("tolTicks", "配对阈值（tick，起点差在这之内算同一个音）", "30"),
			checkParam("phonemes", "复制音素（会冻结发音为 override）", true),
			// ⭐ 2026-10-07 用户口径「除 uuid 外全都要」补齐的三类
			checkParam("dur", "复制时值（note resize）", false),
			checkParam("consonants", "复制辅音时长（v1 模型才有；逐音符逐辅音一笔）", false),
			checkParam("params", "复制参数曲线（所有可用参数，逐层整段写）", false),
			textParam("paramLayers", "参数写进哪些层（逗号分隔；留空按目标自己推）", "", "envelope,direct",
				"与「参数映射」同一口径：**默认包络**（envelope），真值(direct)要显式勾；v2 只有 user"),
			textParam("paramSel", "⭐ 参数表格（逐参数逐列勾选；窗口里就是那张表）", "",
				"{\"tension\":[\"effective\",\"envelope\"],\"air\":[\"effective\"]}",
				"JSON 对象：键 = 参数名，值 = 要搬的列。两个列名 —— "+
					"`effective`（**实参**，绝对曲线 ⇒ 目标的 `direct` 真值层；没有就 `user`）、"+
					"`envelope`（**包络**，乘数层 ⇒ 目标的 `envelope`，只有 v1 有）。"+
					"**留空** = 老路径（`params=1` + `paramLayers`）"),
			checkParam("dynamic", "复制 dynamic 锚点（v2 的锚点形状 lane，单独一条路）", false),
			// ⭐ 用户 2026-10-19：「做数值映射，跨代允许映射」+「合并」——
			// 这两项只影响**参数那一半**（跨代默认允许，但必须按值域换算）。
			{Name: "map", Label: "数值映射", Type: ParamSelect, Default: "range",
				Options: []Option{
					{Value: "range", Label: "按值域换算（跨代要用它）"},
					{Value: "none", Label: "原值照搬（只在同代安全）"},
				}},
			checkParam("crossGen", "允许跨引擎世代（配『按值域换算』）", true),
			textParam("dynEnv", "dynamic → 包络（跨代映射；`参数=系数`）", "", "falsetto=0.5, tension=-0.3",
				"v2 的 `dynamic`（**默认 1 = 不动**）摊到 v1 的 `envelope` 乘数层：`envelope = 1 + 系数 × (dynamic − 1)`；"+
					"例 `falsetto=0.5, tension=-0.3`（dynamic 1.5 ⇒ ±0.5 的调制量）"),
		},
		Plan:  xcopyWithinPlan,
		Apply: xcopyWithinApply,
	})
}

func parseMeasure(vals map[string]string, name, label string) (float64, error) {
	s := strings.TrimSpace(vals[name])
	if s == "" {
		return 0, fmt.Errorf("要给一个%s（1 起的小节号）", label)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("%s 要是个数字（小节号），收到 %q", label, s)
	}
	return f, nil
}

// analyzeWithin 读齐**整条轨**、把三个小节号变成**工程绝对**窗口、配对、算出要写什么。**只读**。
//
// ⭐ 用户 2026-10-07 定的作用域：「`xcopy.within` **作用于轨内所有 clip，遍历绝对时间**」——
// 所以读的是 `readTrackScope`（整条轨的片段 + 音符，`pos` 已换成绝对 tick），
// 三个小节号 `convert measure-to-tick` 出来**直接用**（不再减 clipBegin）。
// 一条轨常常由好几个片段拼成（一句一个片段很常见）：只按"当前片段"搬的话，跨片段那一段会**静默搬不到**。
func analyzeWithin(ctx context.Context, rd ReadFunc, vals map[string]string) (*xcopyPlanData, error) {
	started := time.Now()
	want := xcopyWant{
		Lyric:    truthy(value(vals, "lyrics", "")),
		Pitch:    truthy(value(vals, "pitch", "")),
		Phonemes: truthy(value(vals, "phonemes", "")),
	}
	// ⭐ 2026-10-07 用户口径「除 uuid 外全都要」：这两类一起算"有没有事可做"
	wantDur := truthy(value(vals, "dur", ""))
	wantCons := truthy(value(vals, "consonants", ""))
	wantParams := truthy(value(vals, "params", ""))
	wantDyn := truthy(value(vals, "dynamic", ""))
	// ⭐ 2026-10-19：`dynamic → 包络` 本身就是一项内容（v2 的 dynamic 摊到 v1 的 envelope）
	wantDynEnv := strings.TrimSpace(value(vals, "dynEnv", "")) != ""
	if !want.Lyric && !want.Pitch && !want.Phonemes && !wantDur && !wantCons && !wantParams && !wantDyn && !wantDynEnv {
		return nil, fmt.Errorf("一个属性都没勾（歌词 / 音高 / 音素 / 时值 / 辅音时长 / 参数曲线 / dynamic / dynamic→包络），没什么可复制的")
	}

	ms, err := parseMeasure(vals, "srcStart", "源开始小节")
	if err != nil {
		return nil, err
	}
	me, err := parseMeasure(vals, "srcEnd", "源结束小节")
	if err != nil {
		return nil, err
	}
	mt, err := parseMeasure(vals, "dstStart", "目标开始小节")
	if err != nil {
		return nil, err
	}
	if me <= ms {
		return nil, fmt.Errorf("源结束小节（%v）必须大于源开始小节（%v）", me, ms)
	}

	// ⭐ 作用域 = **整条轨**（用户 2026-10-07：「`xcopy.within` 作用于轨内所有 clip，遍历绝对时间」）：
	// `readTrackScope` 把这条轨的**每个片段**都读进来，音符的 `pos` 一律换成**工程绝对** tick。
	tc, err := readTrackScope(ctx, rd)
	if err != nil {
		return nil, err
	}
	if len(tc.Notes) == 0 {
		return nil, fmt.Errorf("轨「%s」上的 %d 个片段里一个音符都没有（这条轨是空的）", tc.TrackName, len(tc.Clips))
	}

	// 小节号 → **工程绝对** tick（`convert measure-to-tick` 给的本来就是绝对）
	// ⚠️ 不再"减掉 clipBegin 换到片段坐标"：窗口可能横跨好几个片段，片段坐标根本表达不了它。
	srcA, err := measureToTick(ctx, rd, ms, "源开始小节")
	if err != nil {
		return nil, err
	}
	srcB, err := measureToTick(ctx, rd, me, "源结束小节")
	if err != nil {
		return nil, err
	}
	dstA, err := measureToTick(ctx, rd, mt, "目标开始小节")
	if err != nil {
		return nil, err
	}
	if srcB <= srcA {
		return nil, fmt.Errorf("源结束小节（%v）换算出的 tick（%d）不比源开始小节（%v ⇒ %d）大 —— "+
			"这一带上是不是有变拍号？换两个小节号再试", me, srcB, ms, srcA)
	}
	span := srcB - srcA
	dstB := dstA + span

	// 按**绝对** tick 取音符（判据照原脚本：`onset >= 起点 && onset <= 终点`，**两端都含**）
	var srcNotes, dstNotes []xcopyNote
	for _, n := range tc.Notes {
		if n.Pos >= srcA && n.Pos <= srcB {
			srcNotes = append(srcNotes, n)
		}
		if n.Pos >= dstA && n.Pos <= dstB {
			dstNotes = append(dstNotes, n)
		}
	}

	srcClips := clipsCovering(tc, srcA, srcB)
	dstClips := clipsCovering(tc, dstA, dstB)

	rep := &xcopyReport{
		Mode:     "within",
		SrcTrack: tc.TrackIndex, SrcTrackName: tc.TrackName,
		DstTrack: tc.TrackIndex, DstTrackName: tc.TrackName,
		TrackClips: len(tc.Clips),
		Pairs:      []xcopyPairView{}, Unported: xcopyUnportable,
	}
	v := xcopyPairView{
		Pair:    1,
		SrcClip: fmt.Sprintf("轨内第 %v–%v 小节（工程 tick %d..%d · %s）", ms, me, srcA, srcB, clipSpanText(len(srcClips))),
		DstClip: fmt.Sprintf("轨内第 %v 小节起（工程 tick %d..%d · %s）", mt, dstA, dstB, clipSpanText(len(dstClips))),
		SrcType: clipTypeAt(tc, srcA), DstType: clipTypeAt(tc, dstA),
		SrcBegin: srcA, DstBegin: dstA,
		SrcNotes: len(srcNotes), DstNotes: len(dstNotes),
		// 画框要用（⭐ 全是一套**工程绝对** tick —— 页面也是按整条工程画的，两边同一个坐标系）
		SrcEndTick: srcB, ClipAbsBeg: 0,
		SrcAbsTick: srcA, SrcEndAbs: srcB,
		DstAbsTick: dstA, DstEndAbs: dstB,
	}
	rep.PairCount = 1

	// 配对：源整体平移到目标起点 ⇒ 比的是"各自相对范围起点"的位置（**都在绝对时间上**）。
	// `matchByOnset` 配的是 `srcBegin+pos == dstBegin+pos`，
	// 所以传 `-srcA` / `-dstA` 就等价于比"相对位置"。
	pairs, so, do := matchByOnsetTol(srcNotes, dstNotes, -srcA, -dstA, tolTicksOf(vals))
	v.Matched, v.SrcOnly, v.DstOnly = len(pairs), so, do
	rep.Matched, rep.SrcOnly, rep.DstOnly = len(pairs), so, do
	if len(pairs) == 0 {
		v.Skip = "这两段范围里没有位置对得上的音符，整趟都不会动"
	}
	rep.Pairs = append(rep.Pairs, v)

	// 目标窗口的起点落在哪个片段上（`dstClipUUID` 只有单片段那支才用得上；多片段时给第一个）
	dstClipUUID := ""
	if len(dstClips) > 0 {
		dstClipUUID = dstClips[0].UUID
	}
	pd := &xcopyPlanData{rep: rep, srcIdx: tc.TrackIndex, dstIdx: tc.TrackIndex, want: want,
		started: started, dstClipUUID: dstClipUUID}

	// 音素：源与目标都在**同一个片段**里 ⇒ 一次读音素就够（同语言天然成立）
	// ⚠️ 辅音时长也要读音素，但它要的是**带时长**的那份（`--with-timings=durations`）⇒ 单独读一次。
	var srcPh, dstPh map[string]xcopyPhoneme
	if want.Phonemes || wantCons {
		var su, du []string
		for _, p := range pairs {
			su = append(su, srcNotes[p[0]].NoteUUID)
			du = append(du, dstNotes[p[1]].NoteUUID)
		}
		read := readClipPhonemes
		if wantCons {
			read = readClipPhonemeTimings
		}
		srcPh, err = read(ctx, rd, su, "源范围")
		if err != nil {
			return nil, err
		}
		dstPh, err = read(ctx, rd, du, "目标范围")
		if err != nil {
			return nil, err
		}
	}

	// ⭐ 片段类型**逐音符看它落在哪个片段**：作用域是整条轨之后，不能再拿"编辑器里那一个片段"的类型
	// 代表全轨（一条轨上完全可能既有 sing 片段、又有非 sing 片段）。
	// 只影响"写进去只在 sing 片段里有意义"的那三类：歌词 / 音素 / 辅音时长（音高、时值对所有片段都成立）。
	singPairs, otherPairs := [][2]int{}, [][2]int{}
	for _, p := range pairs {
		if c, ok := clipOfNote(tc, dstNotes[p[1]].NoteUUID); ok && isSingClip(c.Type) {
			singPairs = append(singPairs, p)
		} else {
			otherPairs = append(otherPairs, p)
		}
	}

	pp := xcopyPairPlan{view: v}
	if len(otherPairs) > 0 && (want.Lyric || want.Phonemes || wantCons) {
		pp.skips = append(pp.skips, fmt.Sprintf(
			"目标范围里有 %d 个配对音符落在**非 sing 片段**里 ⇒ 歌词 / 音素 / 辅音时长对它们跳过（音高、时值照做）",
			len(otherPairs)))
	}
	lyricOK := want.Lyric && len(singPairs) > 0
	phonOK := want.Phonemes && len(singPairs) > 0
	consOK := wantCons && len(singPairs) > 0
	// ⛔ 最后一个参数 `true` = **只搬手动改过的音素**（用户 2026-10-07 口径）
	planPairProps(&pp, srcNotes, dstNotes, singPairs, srcPh, dstPh, want, lyricOK, phonOK, rep, true)
	if len(otherPairs) > 0 {
		// 非 sing 那批：歌词/音素关掉，其它（音高）照做 —— `want` 的副本，不动原值
		wantOther := want
		wantOther.Lyric, wantOther.Phonemes = false, false
		planPairProps(&pp, srcNotes, dstNotes, otherPairs, srcPh, dstPh, wantOther, false, false, rep, true)
	}

	// ⭐ 时值：只收"真的会变"的，按**目标时长**分组（`note resize` 一次一个 `--dur`）
	if wantDur {
		groups := map[int64]*xcopyResizeGroup{}
		for _, p := range pairs {
			s, d := srcNotes[p[0]], dstNotes[p[1]]
			if s.Dur == d.Dur {
				continue
			}
			g := groups[s.Dur]
			if g == nil {
				g = &xcopyResizeGroup{From: d.Dur, To: s.Dur}
				groups[s.Dur] = g
			}
			g.NoteID = append(g.NoteID, d.NoteUUID)
			rep.WillResize++
		}
		for _, g := range groups {
			pp.resizeGroups = append(pp.resizeGroups, *g)
		}
	}

	// ⭐ 辅音时长：逐音符比"逐个辅音"的时长（源的那份与目标的那份，按 index 对齐）。
	// ⛔ 用户 2026-10-07 的口径：**默认的辅音时长不复制** ⇒ 只搬"被手动改过"的那些
	//（v1：音符上的 head/tailConsonants 长度数组非空；v2：`--with-timings` 里有 pin）。
	if consOK && len(pairs) > 0 {
		n, notManual := 0, 0
		for _, p := range pairs {
			sNote, d := srcNotes[p[0]], dstNotes[p[1]].NoteUUID
			// 辅音时长两边都得是 sing 片段（不在 sing 片段里的音符根本没有辅音时长这回事）
			sClip, sok := clipOfNote(tc, sNote.NoteUUID)
			dClip, dok := clipOfNote(tc, d)
			if !sok || !dok || !isSingClip(sClip.Type) || !isSingClip(dClip.Type) {
				continue
			}
			sm, dm := srcPh[sNote.NoteUUID], dstPh[d]
			if !sNote.hasManualConsonants() && !anyTimingPinned(sm) {
				notManual++ // 引擎自己分布出来的时长 ⇒ 不是用户的意图，不搬
				continue
			}
			for _, tsm := range sm.Timings {
				if tsm.IsVowel {
					continue // 元音没有"自己的长度"可设（官方原话）
				}
				// 目标同一个 index 上也得是个辅音才写（结构不同就跳过，别把辅音写到元音上）
				ok, cur := false, 0.0
				for _, u := range dm.Timings {
					if u.Index == tsm.Index {
						ok, cur = !u.IsVowel, u.DurationSec
					}
				}
				if !ok {
					pp.skips = append(pp.skips, fmt.Sprintf(
						"音符 %s 的第 %d 个音素（%s）在源里是辅音、在目标里不是 ⇒ 跳过", d, tsm.Index, tsm.Name))
					continue
				}
				if math.Abs(cur-tsm.DurationSec) < 1e-6 {
					continue // 本来就一样
				}
				pp.consonantSets = append(pp.consonantSets, xcopyConsonantSet{
					NoteUUID: d, Index: tsm.Index, Name: tsm.Name, LengthMs: tsm.DurationSec * 1000,
				})
				n++
			}
		}
		rep.WillConsonant = n
		rep.SkippedDefaultCons = notManual
		if n == 0 && notManual == 0 {
			pp.skips = append(pp.skips, "辅音时长：配上的音符里没有可写的变化（两边本来就一样）")
		}
	}

	// ⭐ 参数曲线：按**工程绝对窗口**在多片段之间搬（源读实参 → 写目标各片段的可写层）。
	// 面板那张参数表（`paramSel`）的判定与 `xcopy.notes` 共用（`paramJobsOf`）。
	// ⚠️ `dynamic → 包络`（`dynEnv`）也在这条路上 ⇒ 它单独勾着时也得进来一次。
	if wantParams || wantDyn || strings.TrimSpace(value(vals, "dynEnv", "")) != "" {
		if err := planTrackParams(ctx, rd, vals, tc, srcA, srcB, dstA, wantParams, wantDyn, pd, rep); err != nil {
			return nil, err
		}
	}

	rep.Skipped = append(rep.Skipped, pp.skips...)
	// 参数那半的说明单独收在 pd.paramSkips（analyzeXcopy 结尾会重置 rep.Skipped ⇒ 统一在最后合并）
	rep.Skipped = append(rep.Skipped, pd.paramSkips...)
	pd.pairs = append(pd.pairs, pp)
	rep.Calls = pd.planCalls()
	return pd, nil
}

// readClipPhonemeTimings 读 `phoneme list --with-timings=durations`（要**逐音素的时长**）。
// 官方：`phoneme set-consonant-timing --index` 的 index 就是这里报的那个。
func readClipPhonemeTimings(ctx context.Context, rd ReadFunc, uuids []string, what string) (map[string]xcopyPhoneme, error) {
	out := map[string]xcopyPhoneme{}
	if len(uuids) == 0 {
		return out, nil
	}
	args := append([]string{"phoneme", "list", "--note"}, uuids...)
	args = append(args, "--with-timings=durations")
	var pl xcopyPhonemeList
	if err := readJSON(ctx, rd, &pl, args...); err != nil {
		return nil, fmt.Errorf("读音素时长（%s）：%w", what, err)
	}
	for _, n := range pl.Notes {
		out[n.NoteUUID] = n
	}
	return out, nil
}

// planClipParams 复制"参数那一半"：源片段 [srcLo,srcHi)（**片段内** tick）→ 目标片段 [dstLo,dstHi)。
//
//	· 参数曲线：源读**实参(effective)** → 写目标的可写层（逐层整段写，只覆盖目标那一段）；
//	· `dynamic`：锚点形状、没有实参 ⇒ 读锚点 → **平移** → 写回。
//
// ⚠️ 两侧长度必须相等（逐 tick 平移）：`vocalparam write` 是"替换该层在**这段 tick 范围**里的点"，
// 长度不等就不是"复制"而是"重采样"了 —— 那种事我们不做（不猜引擎的插值）。
func planClipParams(ctx context.Context, rd ReadFunc, vals map[string]string,
	srcClip, dstClip string, srcLo, srcHi, dstLo, dstHi int64, wantParams, wantDyn bool,
	pd *xcopyPlanData, rep *xcopyReport) error {

	if srcHi-srcLo != dstHi-dstLo {
		pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
			"参数曲线：源段 %d tick、目标段 %d tick（长度不等）⇒ 参数那一半跳过", srcHi-srcLo, dstHi-dstLo))
		return nil
	}
	// ⭐ 用户 2026-10-19：「做数值映射，跨代允许映射」+「合并」——
	// 换算方式与跨代开关对整趟生效（不再是"一遇跨代就整段跳过"）。
	mapMode, merr := paramMapModeOf(vals)
	if merr != nil {
		return merr
	}
	crossGen := boolValue(vals["crossGen"], true)
	ro, err := readParamRoster(ctx, rd, srcClip)
	if err != nil {
		return err
	}
	// ⚠️ 跨世代：**不是不能搬，是不能"原值照搬"**（`ace-params` §2.0 第 3 条：同名参数的尺度/范围
	// 随引擎世代变 —— `air` 在 v2 是 `model 0..1`、在 v1 是 `envelope 0.2..2.5`）。
	// 用户 2026-10-19：「做数值映射，跨代允许映射」⇒ 只要开着「按值域换算」就搬（下面按两端值域线性映射），
	// 选「原值照搬」仍跳过（那是"把 0.4 当成另一个 0.4"，而且不报错）。
	//
	// ⚠️ 同时把**目标那一侧的花名册**读出来：**层的合法性只看目标**（写的是目标的层），
	// 而"读哪一层当源"看源。第一版两处都用了源的花名册 —— 源是 v1、目标是 v2 时，
	// 默认层会推成 `envelope` 而目标根本没有那一层（写下去会被 ACE 拒，而不是我们提前跳过）。
	dro := ro
	crossGenHit := false
	if dstClip != srcClip {
		dr, derr := readParamRoster(ctx, rd, dstClip)
		if derr != nil {
			return derr
		}
		dro = dr
		// 用户 2026-10-19：跨代**不再是硬拒** —— 只要开着「按值域换算」就能搬；
		// 选「原值照搬」时仍然跳过（`air` 在 v2 是 `model 0..1`、v1 是 `envelope 0.2..2.5`，
		// 原值搬过去就是另一个意思，而且不报错）。
		if dr.EngineGeneration != "" && ro.EngineGeneration != "" && dr.EngineGeneration != ro.EngineGeneration {
			switch {
			case !crossGen:
				pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
					"参数曲线：源片段是 **%s**、目标是 **%s** —— 同名参数在两代的尺度/范围不同，而「允许跨引擎世代」没勾 ⇒ 参数那一半跳过",
					ro.EngineGeneration, dr.EngineGeneration))
				return nil
			case mapMode != "range":
				pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
					"参数曲线：源片段是 **%s**、目标是 **%s**（跨代），而「数值映射」选的是**原值照搬** ⇒ 参数那一半跳过"+
						"（跨代请把「数值映射」改成『按值域换算』）",
					ro.EngineGeneration, dr.EngineGeneration))
				return nil
			}
			crossGenHit = true
		}
	}
	span := srcHi - srcLo
	if span < 2 {
		pd.paramSkips = append(pd.paramSkips, fmt.Sprintf("参数曲线要求跨度至少 2 个 tick（ACE 的 dense 写规则），这段只有 %d ⇒ 跳过", span))
		return nil
	}

	if wantParams {
		// ⭐ 2026-10-07 用户口径：「**参数展开，列出所有参数，列个表格，勾选；v1 就实参和包络两列**」。
		// 表格路径（`paramSel`，见 paramsel.go）逐格搬；老路径（`params=1` + `paramLayers`，
		// 主面板表单那条）等价于"每个参数 × 实参一列 → 写进 paramLayers（留空按目标默认层）"。
		// ⚠️ **判定这一处共用**（`paramJobsOf`）：`xcopy.notes` 与 `xcopy.within`（多片段那支）都走它，
		// 免得同一张表在两条路上给出不同的落点。
		jobs, table, picks, skips, jerr := paramJobsOf(vals, ro, dro)
		if jerr != nil {
			return jerr
		}
		pd.paramSkips = append(pd.paramSkips, skips...)

		for _, jb := range jobs {
			p, pkerr := pickParam(ro, jb.Param)
			if pkerr != nil {
				pd.paramSkips = append(pd.paramSkips, fmt.Sprintf("%v ⇒ 跳过", pkerr))
				continue
			}
			// 源：**实参列只搬"你画过的点"**（用户 2026-10-20：「以编辑过的为准，没编辑过的位置不要轻易复制」）
			// ⇒ 掩码 = **用户实参层**（v1 `direct` / v2 `user`）∪ **包络层**（v1 `envelope`，用户 2026-10-20：
			//    「envelope 画过的范围也要包含」—— 包络也是人画的，那些位置同样算"编辑过"），
			//    值仍取该点的 `effective`（合并后的绝对值，数值忠实）。
			srcFrom := jb.SrcLayer
			var srcPts []*float64
			var rerr error
			if jb.SrcLayer == "effective" && jb.SrcRaw {
				// ⭐ 两列都勾 ⇒ 实参格读**原始 `direct`/`user` 值**（包络单独写一格）⇒ 合起来不乘二
				rawLayer := userLayerOf(ro, jb.Param)
				if rawLayer == "" {
					pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
						"参数 %s：实参+包络两列都勾了，但源上没有原始实参层（`direct`/`user`）可读 ⇒ 这一格跳过", jb.Param))
					continue
				}
				srcFrom = rawLayer
				_, srcPts, rerr = readLayerRange(ctx, rd, srcClip, jb.Param, rawLayer, srcLo, srcHi)
			} else if jb.SrcLayer == "effective" {
				// ⭐ 跨代折算（用户 2026-10-20 定稿，逐段按"源上画了哪几层"决定）：
				//   只画实参 ⇒ 实参原值；只画包络 ⇒ baseline × 包络；两者都画 ⇒ 实参 × 包络（旧实际值）。
				// 目标那一代只有实参层（没有 envelope），所以这三种都落成"绝对值"写进目标的实参层。
				rawLayer := userLayerOf(ro, jb.Param)
				var direct, env []*float64
				if rawLayer != "" {
					if _, direct, rerr = readLayerRange(ctx, rd, srcClip, jb.Param, rawLayer, srcLo, srcHi); rerr != nil {
						pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
							"参数 %s 的 %s 层读不到（%v）⇒ 这一格跳过", jb.Param, rawLayer, rerr))
						continue
					}
				}
				if containsStr(writableAnyLayers(ro, jb.Param), "envelope") {
					_, env, _ = readLayerRange(ctx, rd, srcClip, jb.Param, "envelope", srcLo, srcHi)
				}
				var base []*float64
				if containsStr(writableAnyLayers(ro, jb.Param), "envelope") || hasAnyLayer(ro, jb.Param, "baseline") {
					_, base, _ = readLayerRange(ctx, rd, srcClip, jb.Param, "baseline", srcLo, srcHi)
				}
				srcPts = make([]*float64, span)
				nDirect, nEnv := 0, 0
				for i := int64(0); i < span; i++ {
					var d, e *float64
					if direct != nil && int(i) < len(direct) {
						d = direct[i]
					}
					if env != nil && int(i) < len(env) {
						e = env[i]
					}
					var b float64
					hasB := base != nil && int(i) < len(base) && base[i] != nil
					if hasB {
						b = *base[i]
					}
					switch {
					case d != nil && e != nil:
						v := *d * *e
						srcPts[i] = &v
						nDirect++
						nEnv++
					case d != nil:
						v := *d
						srcPts[i] = &v
						nDirect++
					case e != nil && hasB:
						v := b * *e
						srcPts[i] = &v
						nEnv++
					}
				}
				if nDirect == 0 && nEnv == 0 {
					pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
						"参数 %s 的**实参**列：源上这几层（%s / envelope）都没有画过的点 ⇒ 这一格跳过", jb.Param, rawLayer))
					continue
				}
				srcFrom = "实参/包络按段折算"
			} else {
				_, srcPts, rerr = readLayerRange(ctx, rd, srcClip, jb.Param, jb.SrcLayer, srcLo, srcHi)
			}
			if rerr == nil {
				// ⚠️ 2026-10-23 实测：v1 的 envelope 层是 **points 且可能是空的**（用户根本没画过），
				// 这时源逐 tick 全是 nil ⇒ 计划里却算了 17280 个 tick，执行时一个锚点都写不出去，
				// 报告却写"已写入"（用户看到的就是"跑了但没效果"）。这里直接判空、如实跳过。
				n := 0
				for _, v := range srcPts {
					if v != nil {
						n++
					}
				}
				if n == 0 {
					pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
						"参数 %s 的源曲线（%s）在这一段**一个点都没画**（层是空的）⇒ 这一格跳过（不是失败，没东西可搬）",
						jb.Param, srcFrom))
					continue
				}
			}
			if rerr != nil {
				pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
					"参数 %s 的源曲线（%s）读不到（%v），跳过", jb.Param, srcFrom, rerr))
				continue
			}
			if int64(len(srcPts)) != span {
				pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
					"参数 %s 的源曲线读到 %d 个 tick（要 %d 个）⇒ 跳过（长度对不上不硬算）", jb.Param, len(srcPts), span))
				continue
			}
			// 目标层现在这一段（报"本来画过多少"；指纹在每段写之前再单独读）
			_, dstPts, terr := readLayerRange(ctx, rd, dstClip, jb.Param, jb.DstLayer, dstLo, dstHi)
			if terr != nil {
				pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
					"参数 %s 的 %s 层读不到（%v），跳过", jb.Param, jb.DstLayer, terr))
				continue
			}
			// 数值映射（用户 2026-10-19）：源值域取**源**花名册、目标值域取**目标**花名册，
			// 按 `mapMode` 线性换算；空档保持空档，越界夹到目标边界并计数。
			dp, dperr := pickParam(dro, jb.Param)
			if dperr != nil {
				pd.paramSkips = append(pd.paramSkips, fmt.Sprintf("%v ⇒ 跳过", dperr))
				continue
			}
			srcRange := [2]float64{p.Min, p.Max}
			dstRange := [2]float64{dp.Min, dp.Max}
			out, clamped, examples, why := mapValues(srcPts, mapMode, srcRange, dstRange)
			if why != "" {
				pd.paramSkips = append(pd.paramSkips, fmt.Sprintf("参数 %s 的 %s 层：%s ⇒ 这一层跳过", jb.Param, jb.DstLayer, why))
				continue
			}
			drawn := 0
			for i := range srcPts {
				if dstPts != nil && i < len(dstPts) && dstPts[i] != nil {
					drawn++
				}
			}
			// 只在"真有必要解释"时才写进说明：跨代，或两侧值域确实不同。
			if crossGenHit || srcRange != dstRange {
				pd.paramSkips = append(pd.paramSkips,
					paramMapNote(jb.Param, mapMode, srcRange, dstRange, examples, clamped, crossGenHit))
			}
			// ⭐ 用户 2026-10-22：「改成覆盖」——不再只搬"画过的连续段"，
			// 整段（源窗口）一次写下去：目标这一段原有的曲线**被整体替换**（源上空档的地方也照写，
			// 等于把那段清掉）。这样搬过去的样子与源**逐 tick 一致**，不会剩一半旧值。
			runs := []denseRun{{Begin: dstLo, Pts: out}}
			wrote := 0
			for _, run := range runs {
				// 每段单独读一次指纹（分段写会改指纹，不能复用同一枚）
				rr, _, rerr := readLayerRange(ctx, rd, dstClip, jb.Param, jb.DstLayer, run.Begin, run.Begin+int64(len(run.Pts)))
				if rerr != nil {
					pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
						"参数 %s 的 %s 层第 %d 段读不到（%v）⇒ 这一段跳过", jb.Param, jb.DstLayer, wrote+1, rerr))
					continue
				}
				pd.paramWrites = append(pd.paramWrites, xcopyParamWrite{
					ClipUUID: dstClip, Param: jb.Param, Layer: jb.DstLayer, PosBegin: run.Begin,
					Payload: run.Pts, IfMatch: rr.Fingerprint, Ticks: len(run.Pts),
					DrawnBefore: drawn, Clamped: clamped, Shape: layerShapeOf(dro, jb.Param, jb.DstLayer),
				})
				wrote++
				rep.ParamTicks += len(run.Pts)
			}
			if wrote == 0 {
				continue
			}
			rep.WillParamLayers++
			if srcFrom != jb.SrcLayer && !jb.SrcRaw {
				pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
					"参数 %s 没有实参 ⇒ 源读的是它自己的 %s 层", jb.Param, srcFrom))
			}
		}
		if rep.WillParamLayers == 0 {
			if table && len(picks) == 0 {
				pd.paramSkips = append(pd.paramSkips,
					"参数曲线：参数表交上来了但**一个格子都没勾** ⇒ 这一项什么都没写（这是有意的，不是失败）")
			} else {
				pd.paramSkips = append(pd.paramSkips, "参数曲线：这一份 clip 上没有可搬的层（或全都读不到），这一项什么都没做")
			}
		}
	}

	// dynamic：锚点形状、没有实参 ⇒ 读它的 user 层锚点，平移后写回
	if wantDyn {
		hasDyn := false
		for _, p := range ro.Params {
			if p.Param == "dynamic" && p.Available {
				hasDyn = true
			}
		}
		if !hasDyn {
			pd.paramSkips = append(pd.paramSkips, "这份 clip 上没有 `dynamic` 参数（v1 引擎没有它）⇒ 这一项跳过")
			return nil
		}
		pRead, anchors, derr := readPointsLayer(ctx, rd, srcClip, "dynamic", "user", srcLo, srcHi)
		if derr != nil {
			pd.paramSkips = append(pd.paramSkips, fmt.Sprintf("读 dynamic 锚点失败（%v）⇒ 这一项跳过", derr))
			return nil
		}
		if len(anchors) == 0 {
			pd.paramSkips = append(pd.paramSkips, "源范围里没有 dynamic 锚点 ⇒ 不写（也不会去清目标那段）")
			return nil
		}
		delta := dstLo - srcLo
		out := make([][]any, 0, len(anchors))
		for _, a := range anchors {
			tick, _ := a[0].(float64)
			out = append(out, []any{int64(math.Round(tick)) + delta, a[1]})
		}
		// ⚠️ `--if-match` 必须带**目标**那一份的指纹（守的是被写的东西）。
		// 这里是单片段那支（源=目标同一个 clip 时两者本来就一样），但跨轨那支（`xcopy.notes`）
		// 源和目标**不是**同一个 clip —— 用源的指纹等于没守（实测过：指纹对不上时 ACE 直接拒写）。
		ifMatch := pRead.Fingerprint
		if dstClip != srcClip {
			if dr, _, derr2 := readPointsLayer(ctx, rd, dstClip, "dynamic", "user", dstLo, dstHi); derr2 == nil {
				ifMatch = dr.Fingerprint
			} else {
				pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
					"dynamic：目标片段的锚点 lane 读不到（%v）⇒ 这一项跳过（拿不到 --if-match 就不写）", derr2))
				return nil
			}
		}
		pd.dynWrites = append(pd.dynWrites, xcopyDynamicWrite{
			ClipUUID: dstClip, Anchors: out, IfMatch: ifMatch, Delta: delta, SrcFrom: "user"})
		rep.WillDynamic = len(out)
	}

	// ⭐ 用户 2026-10-19 的跨代约定之三：**dynamic → 包络**
	// v2 的 `dynamic`（`scale:"control"`、**−1…1**、0 = 不动、锚点形状）在 v1 上**没有对应参数**，
	// 但它可以当**调制源**摊到 v1 的若干条 `envelope`（乘数层）上：`envelope_p(t) = 1 + k_p × dynamic(t)`。
	// 例：`falsetto=0.5, tension=-0.3` ⇒ dynamic=+1 时 falsetto 1.5×、tension 0.7×。
	coefs, cerr := parseDynEnvCoefs(value(vals, "dynEnv", ""))
	if cerr != nil {
		return cerr
	}
	if len(coefs) > 0 {
		srcHasDyn := false
		for _, p := range ro.Params {
			if p.Param == "dynamic" && p.Available {
				srcHasDyn = true
			}
		}
		if !srcHasDyn {
			pd.paramSkips = append(pd.paramSkips, "dynamic → 包络：源这一代没有 `dynamic` 参数（v1 没有它）⇒ 跳过")
		} else {
			_, anchors, derr := readPointsLayer(ctx, rd, srcClip, "dynamic", "user", srcLo, srcHi)
			if derr != nil {
				pd.paramSkips = append(pd.paramSkips, fmt.Sprintf("dynamic → 包络：读源锚点失败（%v）⇒ 跳过", derr))
			} else {
				pts := make([][2]float64, 0, len(anchors))
				for _, a := range anchors {
					t, tok := a[0].(float64)
					v, vok := a[1].(float64) // null 锚点（擦除标记）跳过
					if !tok || !vok {
						continue
					}
					pts = append(pts, [2]float64{t, v})
				}
				if len(pts) == 0 {
					pd.paramSkips = append(pd.paramSkips, "dynamic → 包络：源范围里没有 dynamic 锚点 ⇒ 不写")
				} else {
					dyn := sampleAnchors(pts, srcLo, srcHi)
					for _, cf := range coefs {
						dp, perr := pickParam(dro, cf.Param)
						if perr != nil {
							pd.paramSkips = append(pd.paramSkips, fmt.Sprintf("dynamic → 包络：%v ⇒ 这一项跳过", perr))
							continue
						}
						if !containsStr(writableAnyLayers(dro, cf.Param), "envelope") {
							pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
								"dynamic → 包络：目标参数 %s 上没有可写的 `envelope` 层（这一代只有 %s）⇒ 跳过",
								cf.Param, strings.Join(writableAnyLayers(dro, cf.Param), "、")))
							continue
						}
						payload, clamped := dynEnvelopeValues(dyn, cf.Gain, dp.Min, dp.Max)
						// ⭐ 只写"dynamic 真的推了一把"的段（dyn=0 ⇒ envelope=1 = 不动）：
						// 那些 tick 留空档，目标原来画过的东西不动（用户 2026-10-20）。
						zeroed := 0
						for i, d := range dyn {
							if math.Abs(d) < 1e-9 {
								payload[i] = nil
								zeroed++
							}
						}
						runs, dropped := denseRuns(payload, dstLo, 2)
						if len(runs) == 0 {
							pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
								"dynamic → 包络：%s 上没有一段 ≥2 tick 的「dynamic≠0」⇒ 不写（%d 个 tick 是 0）",
								cf.Param, zeroed))
							continue
						}
						if dropped > 0 {
							pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
								"dynamic → 包络：%s 有 %d 个 tick 是孤立点（每段要 ≥2 tick）⇒ 没写", cf.Param, dropped))
						}
						wroteDyn := 0
						for _, run := range runs {
							// `--if-match`：守的是**被写的那一段**（目标层的指纹，逐段现读）
							tRead, _, terr := readLayerRange(ctx, rd, dstClip, cf.Param, "envelope",
								run.Begin, run.Begin+int64(len(run.Pts)))
							if terr != nil {
								pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
									"dynamic → 包络：%s 的目标 envelope 层第 %d 段读不到（%v）⇒ 跳过这一段",
									cf.Param, wroteDyn+1, terr))
								continue
							}
							pd.paramWrites = append(pd.paramWrites, xcopyParamWrite{
								ClipUUID: dstClip, Param: cf.Param, Layer: "envelope", PosBegin: run.Begin,
								Payload: run.Pts, IfMatch: tRead.Fingerprint, Ticks: len(run.Pts), Clamped: clamped, Shape: layerShapeOf(dro, cf.Param, "envelope"),
							})
							wroteDyn++
							rep.ParamTicks += len(run.Pts)
						}
						if wroteDyn == 0 {
							continue
						}
						rep.WillParamLayers++
						pd.paramSkips = append(pd.paramSkips, fmt.Sprintf(
							"dynamic → 包络：%s 的 envelope = 1 + %.3g × dynamic（%d 段、夹边 %d 个；dynamic=0 的地方不写）",
							cf.Param, cf.Gain, wroteDyn, clamped))
					}
				}
			}
		}
	}
	return nil
}

func withinNote(rep *xcopyReport, dry bool) string {
	var b strings.Builder
	if dry {
		b.WriteString("干跑：一个字节都没写。")
	} else {
		b.WriteString("已写入。")
	}
	pv := rep.Pairs[0]
	// ⭐ 2026-10-07：作用域改成**整条轨**（所有片段、绝对时间）⇒ 这里说的是"哪条轨、两个绝对窗口"，
	// 不再是"哪个片段里的哪一段 tick"。
	fmt.Fprintf(&b, " 轨「%s」（%d 个片段）：%s ⇒ %s。", rep.SrcTrackName, rep.TrackClips, pv.SrcClip, pv.DstClip)
	fmt.Fprintf(&b, " 源范围 %d 个音符、目标范围 %d 个 ⇒ 配上 **%d** 个。", pv.SrcNotes, pv.DstNotes, pv.Matched)
	if rep.SrcOnly > 0 {
		b.WriteString(fmt.Sprintf(" ⚠️ 源侧有 %d 个音符在目标范围里找不到对应位置（**不会新建**，照原脚本）。", rep.SrcOnly))
	}
	if rep.DstOnly > 0 {
		b.WriteString(fmt.Sprintf(" 目标范围里有 %d 个音符没被碰到。", rep.DstOnly))
	}
	// ⭐ 属性那一半的细账（有才算，免得干跑报告里全是 0）
	attrs := []string{}
	if rep.WillLyric > 0 {
		attrs = append(attrs, fmt.Sprintf("歌词 %d 个", rep.WillLyric))
	}
	if rep.WillPitch > 0 {
		attrs = append(attrs, fmt.Sprintf("音高 %d 个", rep.WillPitch))
	}
	if rep.WillPhonemes > 0 {
		attrs = append(attrs, fmt.Sprintf("音素 %d 个（会冻结成 override）", rep.WillPhonemes))
	}
	if rep.WillResize > 0 {
		attrs = append(attrs, fmt.Sprintf("时值 %d 个", rep.WillResize))
	}
	if rep.WillConsonant > 0 {
		attrs = append(attrs, fmt.Sprintf("辅音时长 %d 处", rep.WillConsonant))
	}
	if rep.WillParamLayers > 0 {
		attrs = append(attrs, fmt.Sprintf("参数曲线 %d 层 / %d 个 tick", rep.WillParamLayers, rep.ParamTicks))
	}
	if rep.WillDynamic > 0 {
		attrs = append(attrs, fmt.Sprintf("dynamic 锚点 %d 个", rep.WillDynamic))
	}
	if len(attrs) > 0 {
		b.WriteString(" 要写：" + strings.Join(attrs, " · ") + "。")
	} else {
		b.WriteString(" ⚠️ 一个属性都没算出要写的东西（配上 0 个，或两边本来就一样）。")
	}
	b.WriteString(" ★ 参数那一半：源**包络优先**（没有包络才用实参），逐格写进目标勾的层（默认包络）；" +
		"dynamic 的锚点也算参数曲线，跟「参数曲线」一起搬。**整段覆盖**：写到的段里，目标原有曲线被源逐 tick 替换。")
	b.WriteString(" ⚠️ 移不过来的属性（ACE 没有）：" + xcopyUnportable + "。")
	return b.String()
}

func xcopyWithinPlan(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	pd, err := analyzeWithin(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	pd.rep.DryRun = true
	pd.rep.Note = withinNote(pd.rep, true)
	return pd.rep, nil
}

func xcopyWithinApply(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	pd, err := analyzeWithin(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	rep := pd.rep
	done, aerr := applyXcopyPairs(ctx, rd, pd)
	rep.Did = done
	if aerr != nil {
		rep.Note = fmt.Sprintf("中途失败（%v）—— **前面已经落地的改动没有回滚**"+
			"（ACE 每个动词是独立的撤销记录，Ctrl+Z 可逐步退回）。", aerr)
		return rep, nil
	}
	rep.Note = withinNote(rep, false)
	return rep, nil
}
