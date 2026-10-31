package tools

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// 工程缩略图 —— **ACE 编排视图的模拟图**（用户 2026-10-05 点名要做）。
//
// 为什么要它：ACE 的编排区里"哪条轨上有什么片段、片段在哪、彼此什么关系"一眼就看得见，
// 但我们这套面板只有文字清单（轨道清单 / 片段清单）——**看相对位置得自己心算 tick**。
// 这里把同一份信息画成一张缩略图，并且**点一下就在面板里选中那个片段**（当操作范围用）。
//
// ⛔ **选中状态只在面板里**（用户 2026-10-05 的口径原话：「是在 web 端选中片段，用于某些操作范围的选择，
// 选中状态**不影响 ace 侧**」）⇒ 本文件**只读**，一个字节都不往 ACE 写。
// 我一开始顺手做了个 `view.selectClip`（走 `selection set` 把片段变成 ACE 的当前片段）——
// **已按这条口径删掉**。留这句是因为"看起来顺手能加"的功能最容易被后人加回来。
//
// 数据来源与三条已知事实（都实测/文档核过）：
//
//	· `track list` **默认就不列空轨道**（空轨要 `--include-empty` 才出来）⇒ 正好是用户要的"跳过空轨道"。
//	· `clip list --track-index N` 给片段的 begin/end/name/**clipColor**/noteCount（颜色是 ACE 编排区那份）。
//	· `clip note-content --track-index N --clip-index M` 给音符，**`pos` 是片段内 tick**（要加 begin 才是全局）。
//
// ⚠️ 没有"一次读全工程"的命令（`help --search summary|overview` 里没有聚合读），
// 所以代价是 **1 + T + C** 条命令；音符那条按**预算**读（默认 24 个片段），超了就**如实报**多少片段没读。
// 不硬读的理由：几十条轨的工程会拖成几十秒，而缩略图的用途是"看一眼 + 点一下"。

const minimapDefaultNoteBudget = 24

// minimapNote 是**片段内**坐标（与 `clip note-content` 的口径一致）。
type minimapNote struct {
	Pos   int64  `json:"pos"`
	Dur   int64  `json:"dur"`
	Pitch int    `json:"pitch"`
	Lyric string `json:"lyric,omitempty"`
}

type minimapClip struct {
	UUID      string        `json:"uuid"`
	Index     int           `json:"clipIndex"`
	Name      string        `json:"name"`
	Type      string        `json:"type"`
	Begin     int64         `json:"begin"`
	End       int64         `json:"end"`
	Color     string        `json:"color,omitempty"`
	NoteCount int           `json:"noteCount"`
	Notes     []minimapNote `json:"notes,omitempty"`
	// NotesOmitted = 这个片段的音符**没读**（预算用完 或 读失败）—— 前端要如实标出来
	NotesOmitted bool `json:"notesOmitted,omitempty"`
	// 只给"有音符"的片段算：画音符要按它自己的音域映射纵向位置
	MinPitch int `json:"minPitch,omitempty"`
	MaxPitch int `json:"maxPitch,omitempty"`
}

type minimapTrack struct {
	Index       int           `json:"trackIndex"`
	UUID        string        `json:"trackUuid,omitempty"`
	Name        string        `json:"name"`
	Type        string        `json:"type"`
	Region      string        `json:"region,omitempty"`
	SoundSource string        `json:"soundSource,omitempty"`
	Clips       []minimapClip `json:"clips"`
}

type minimapReport struct {
	Scope       string  `json:"scope"`
	Duration    int64   `json:"durationTick"`
	DurationSec float64 `json:"durationSec,omitempty"`
	Meter       string  `json:"meter,omitempty"`
	Tempo       float64 `json:"tempo,omitempty"`
	Playhead    int64   `json:"playheadTick,omitempty"`

	TrackCount int `json:"trackCount"`
	ClipCount  int `json:"clipCount"`
	NoteCount  int `json:"noteCount"`

	// 时间轴的**实际范围**（比 duration 更紧凑：工程末尾常常空一大段）
	FirstTick int64 `json:"firstTick"`
	LastTick  int64 `json:"lastTick"`

	NoteBudget        int `json:"noteBudget"`
	ClipsWithoutNotes int `json:"clipsWithoutNotes"`

	Tracks      []minimapTrack `json:"tracks"`
	Diagnostics []string       `json:"diagnostics,omitempty"`
	Note        string         `json:"note,omitempty"`
}

func readMinimap(ctx context.Context, rd ReadFunc, vals map[string]string) (*minimapReport, error) {
	budget := minimapDefaultNoteBudget
	if s := strings.TrimSpace(vals["notes"]); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > 500 {
			return nil, fmt.Errorf("`notes`（最多给多少个片段读音符）只能是 0..500，收到 %q", s)
		}
		budget = n
	}
	// 只看一条轨（留空 = 整个工程）——「轨内复制」那个窗口只要当前轨，少读几条轨就少几秒。
	onlyTrack := -1
	if s := strings.TrimSpace(vals["trackIndex"]); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("`trackIndex` 要是 ≥0 的整数（留空 = 整个工程），收到 %q", s)
		}
		onlyTrack = n
	}

	rep := &minimapReport{Scope: "编排区（arrangement）", NoteBudget: budget}

	// ① 工程时长 / 拍号 / 速度 / 播放头 —— 都**容许失败**：少一样也不该让缩略图画不出来
	var info struct {
		Duration float64 `json:"duration"` // ⚠️ 实测是**浮点 tick**（如 57600）
	}
	if err := readJSON(ctx, rd, &info, "project", "info"); err == nil {
		rep.Duration = int64(info.Duration)
	} else {
		rep.Diagnostics = append(rep.Diagnostics, "读 `project info` 失败（时长按片段末尾推算）："+err.Error())
	}
	var ts struct {
		Signatures []struct {
			Numerator   int `json:"numerator"`
			Denominator int `json:"denominator"`
		} `json:"signatures"`
	}
	if err := readJSON(ctx, rd, &ts, "timesig", "get"); err == nil && len(ts.Signatures) > 0 {
		rep.Meter = fmt.Sprintf("%d/%d", ts.Signatures[0].Numerator, ts.Signatures[0].Denominator)
	}
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
	if ph, err := readPlaybackHead(ctx, rd); err == nil {
		rep.Playhead = ph
	}

	// ② 轨道（**默认不含空轨** —— 这正是用户要的"跳过空轨道"）
	var tl struct {
		Tracks []struct {
			TrackIndex      int    `json:"trackIndex"`
			TrackUUID       string `json:"trackUuid"`
			TrackName       string `json:"trackName"`
			TrackType       string `json:"trackType"`
			Region          string `json:"region"`
			ClipCount       int    `json:"clipCount"`
			SoundSourceName string `json:"soundSourceName"`
		} `json:"tracks"`
	}
	if err := readJSON(ctx, rd, &tl, "track", "list"); err != nil {
		return nil, fmt.Errorf("读 `track list`：%w", err)
	}

	readNotes := 0
	for _, t := range tl.Tracks {
		if t.ClipCount == 0 {
			continue // 空轨：连 clip list 都不用发
		}
		if onlyTrack >= 0 && t.TrackIndex != onlyTrack {
			continue // 只要一条轨（留空才是全工程）
		}
		tr := minimapTrack{
			Index: t.TrackIndex, UUID: t.TrackUUID, Name: t.TrackName, Type: t.TrackType,
			Region: t.Region, SoundSource: t.SoundSourceName, Clips: []minimapClip{},
		}
		var cl struct {
			Clips []struct {
				ClipIndex int     `json:"clipIndex"`
				ClipUUID  string  `json:"clipUuid"`
				ClipName  string  `json:"clipName"`
				ClipType  string  `json:"clipType"`
				ClipBegin int64   `json:"clipBegin"`
				ClipEnd   int64   `json:"clipEnd"`
				ClipSec   float64 `json:"clipBeginSec"`
				ClipColor string  `json:"clipColor"`
				NoteCount int     `json:"noteCount"`
				Enabled   bool    `json:"enabled"`
			} `json:"clips"`
		}
		if err := readJSON(ctx, rd, &cl, "clip", "list", "--track-index", strconv.Itoa(t.TrackIndex)); err != nil {
			rep.Diagnostics = append(rep.Diagnostics, fmt.Sprintf("轨道 %d「%s」的 `clip list` 失败：%v",
				t.TrackIndex, t.TrackName, err))
			continue
		}
		for i, c := range cl.Clips {
			idx := c.ClipIndex
			if idx == 0 && i != 0 {
				// 个别版本不报 clipIndex ⇒ 退回数组下标（clip list 的顺序就是 clip-index 的顺序）
				idx = i
			}
			mc := minimapClip{
				UUID: c.ClipUUID, Index: idx, Name: c.ClipName, Type: c.ClipType,
				Begin: c.ClipBegin, End: c.ClipEnd, Color: c.ClipColor, NoteCount: c.NoteCount,
			}
			// 音符：有预算才读，且**只读有音符的**（noteCount==0 时读了也是空数组，白跑一条命令）
			if c.NoteCount > 0 {
				if readNotes < budget {
					notes, err := readMinimapNotes(ctx, rd, t.TrackIndex, idx)
					if err != nil {
						mc.NotesOmitted = true
						rep.Diagnostics = append(rep.Diagnostics,
							fmt.Sprintf("片段「%s」的 `note-content` 失败：%v", c.ClipName, err))
					} else {
						mc.Notes = notes
						readNotes++
						for _, n := range notes {
							if mc.MinPitch == 0 || n.Pitch < mc.MinPitch {
								mc.MinPitch = n.Pitch
							}
							if n.Pitch > mc.MaxPitch {
								mc.MaxPitch = n.Pitch
							}
							rep.NoteCount++
						}
					}
				} else {
					mc.NotesOmitted = true
					rep.ClipsWithoutNotes++
				}
			}
			tr.Clips = append(tr.Clips, mc)
			rep.ClipCount++
		}
		rep.Tracks = append(rep.Tracks, tr)
	}
	rep.TrackCount = len(rep.Tracks)

	// ③ 时间轴范围：片段覆盖的实际区间（工程 duration 常常比内容长得多）
	first, last := int64(-1), int64(0)
	for _, t := range rep.Tracks {
		for _, c := range t.Clips {
			if first < 0 || c.Begin < first {
				first = c.Begin
			}
			if c.End > last {
				last = c.End
			}
		}
	}
	if first < 0 {
		first = 0
	}
	rep.FirstTick, rep.LastTick = first, last
	if rep.Duration > last && last > 0 {
		rep.Diagnostics = append(rep.Diagnostics, fmt.Sprintf(
			"片段只到 tick %d，工程时长是 %d ⇒ 时间轴按**片段范围**画（末尾那 %d tick 空段没画）",
			last, rep.Duration, rep.Duration-last))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "画了一张工程缩略图：**%d 条轨 / %d 个片段 / %d 个音符**", rep.TrackCount, rep.ClipCount, rep.NoteCount)
	if rep.TrackCount == 0 {
		b.WriteString("。⚠️ 工程里**一条有内容的轨都没有**（空轨按你说的跳过了）")
	}
	if rep.ClipsWithoutNotes > 0 {
		fmt.Fprintf(&b, "。⚠️ 有 **%d** 个片段的音符**没读**（预算 %d 个片段用完了）——"+
			"把「读音符的片段上限」调大可以读全，但工程大时会慢（每片段一条命令）",
			rep.ClipsWithoutNotes, rep.NoteBudget)
	}
	if rep.Meter != "" || rep.Tempo > 0 {
		fmt.Fprintf(&b, "。 %s", strings.TrimSpace(fmt.Sprintf("%s %.1f BPM", rep.Meter, rep.Tempo)))
	}
	b.WriteString("。⭐ **点一下就在面板里选中那个片段**（当操作范围用）——" +
		"⚠️ 这个选中**只在面板里**、不影响 ACE 侧（不改当前片段、不动钢琴窗、不写任何东西）。")
	rep.Note = b.String()
	return rep, nil
}

// readMinimapNotes 读音符（**片段内**坐标）。
//
// ⚠️ 名字带 `Minimap` 前缀是**必须的**：`xcopy.go` 里已有一个 `readClipNotes`（签名不同），
// 同名直接编译不过 —— 顺带说明"读音符"这个动作在本包里已经有两处需要（各写各的字段形状）。
//
// ⚠️ `clip note-content` 要的是 `--clip-index`（不是 uuid）——实测传 `--clip-uuid` 会得到
// "unexpected argument '--clip-uuid' found"。
func readMinimapNotes(ctx context.Context, rd ReadFunc, trackIndex, clipIndex int) ([]minimapNote, error) {
	var nc struct {
		Notes []struct {
			Pos   int64  `json:"pos"`
			Dur   int64  `json:"dur"`
			Pitch int    `json:"pitch"`
			Lyric string `json:"lyric"`
		} `json:"notes"`
	}
	if err := readJSON(ctx, rd, &nc, "clip", "note-content",
		"--track-index", strconv.Itoa(trackIndex), "--clip-index", strconv.Itoa(clipIndex)); err != nil {
		return nil, err
	}
	out := make([]minimapNote, 0, len(nc.Notes))
	for _, n := range nc.Notes {
		out = append(out, minimapNote{Pos: n.Pos, Dur: n.Dur, Pitch: n.Pitch, Lyric: n.Lyric})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Pos < out[j].Pos })
	return out, nil
}

func init() {
	register(&Tool{
		ID: "view.minimap", Category: "工程",
		Title: "工程缩略图（ACE 编排模拟图）",
		Desc: "把**编排区**画成一张缩略图：一条轨一行、片段按 `clipBegin/clipEnd` 摆位、颜色用 ACE 自己那份 `clipColor`，" +
			"**片段里还画音符**（音高映射到行内高度、`pos/dur` 映射到横向）。" +
			"⭐ **点一下就在面板里选中那个片段**（当操作范围用）。" +
			"⚠️ 这个选中**只在面板里**、**不影响 ACE 侧**（不改当前片段、不动钢琴窗、不写任何东西）。" +
			"⚠️ **空轨道默认就跳过**（`track list` 不列空轨）。" +
			"⚠️ 音符按**预算**读（每片段一条命令）：默认最多 24 个片段，超出的会在图上标出来并计数。" +
			"只读，不改工程。",
		Kind:   KindRead,
		Source: "自建：`track list` + `clip list` + `clip note-content`（三种只读命令拼一张图）",
		Params: []Param{
			numParam("notes", "读音符的片段上限", strconv.Itoa(minimapDefaultNoteBudget)),
			textParam("trackIndex", "只看这一条轨（留空 = 整个工程）", "", "0",
				"「轨内复制」那个窗口只要当前轨 —— 少读几条轨就少几秒"),
		},
		Inspect: func(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
			return readMinimap(ctx, rd, vals)
		},
	})
}
