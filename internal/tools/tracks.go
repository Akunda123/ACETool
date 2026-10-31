package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// 「按播放头分割轨道」—— 移植 SV 脚本 `AKD工具侧栏.js` 的 `splitTrackButton`。
//
// 原脚本做的事：把当前轨道**克隆**一份，然后按播放头把内容切成两半 ——
// 克隆留下的那半在克隆上、另一半在原轨道上，于是"一条轨"变成"两条轨"。
//
// ⛔ **为什么这个工具用 Plan+Apply，而不是像别的写工具那样用 Build**：
// `Build` 是"一次性把所有命令算完" —— 它**看不到任何命令的返回值**。
// 而分割天生要"先复制出一批片段，再改**那些新片段**"（新 uuid 只有跑完才知道），
// 所以必须**边跑边读**。框架里能干这个的只有 `Apply`（进程内写实现，自己拿 rd 一条条跑）。
// `acep.patch` 当初也是为同样的理由走这条路的。
//
// ACE 侧的原语（都核过 help）：
//
//	· `track duplicate --track-index A` —— 连片段与 FX 链一起复制，落在原轨**旁边**（index = A+1），
//	  回包直接给新轨的 `trackIndex` / `trackUuid` ✓
//	· `clip split --clip-uuid U --pos <全局 tick>` —— 在片段内部切一刀（原生，保留音符/曲线/换气…）
//	· `clip delete --clip-uuid U…` —— 按 uuid 删，**全有或全无**
//	· `track delete` —— ⚠️ **`--track-uuid` 只认 video/marker 带**；删**编排区**的轨道要
//	  先 `selection set --tracks [{"trackUuid":…}]` 再 `track delete`（无参）。回滚就靠这个。
func init() {
	register(&Tool{
		ID: "track.split", Category: "轨道编辑",
		Title: "按播放头分割轨道",
		Desc: "对应原脚本的「按时间轴位置分割轨道」：把**当前轨道**在**播放头**处切成两条 ——" +
			"先 `track duplicate` 复制一份（连片段与 FX 链），再让原轨只留播放头**之前**的内容、" +
			"复制轨只留播放头**之后**的内容；跨在播放头上的片段用 `clip split` 原生切开（音符/曲线/换气都保留）。" +
			"⚠️ 判据照原脚本：片段**结束 ≤ 播放头** ⇒ 留在原轨；**开始 ≥ 播放头** ⇒ 留在复制轨；" +
			"夹在中间 ⇒ 切开各留一半。" +
			"⚠️ 用的是 **`caret` 所在轨道**（原脚本是 `getCurrentTrack()`）与 **`transport state` 的播放头**。" +
			"⚠️ 中途失败会**回滚**（把复制出来的那条轨删掉），不会留半成品。" +
			"⚠️ 干跑只能给你**第一步能确定的部分**（片段怎么分、要切哪几个）—— " +
			"后面的步骤要等复制出来、拿到新 uuid 才知道。",
		Kind:   KindWrite,
		Source: "AKD工具侧栏.js 的 splitTrackButton",
		Plan:   trackSplitPlan,
		Apply:  trackSplitApply,
	})
}

type splitClipView struct {
	Index    int     `json:"clipIndex"`
	UUID     string  `json:"clipUuid,omitempty"`
	Name     string  `json:"clipName,omitempty"`
	Type     string  `json:"clipType,omitempty"`
	Begin    int64   `json:"clipBegin"`
	End      int64   `json:"clipEnd"`
	BeginSec float64 `json:"clipBeginSec,omitempty"`
	Verdict  string  `json:"verdict"` // 原轨（播放头之前）/ 复制轨（之后）/ 切开各一半
}

type splitReport struct {
	TrackIndex   int             `json:"trackIndex"`
	TrackName    string          `json:"trackName,omitempty"`
	TrackUUID    string          `json:"trackUuid,omitempty"`
	PlayheadTick int64           `json:"playheadTick"`
	BarBeat      string          `json:"barBeat,omitempty"`
	Clips        []splitClipView `json:"clips"`
	LeftClips    int             `json:"leftClips"`
	RightClips   int             `json:"rightClips"`
	Straddling   int             `json:"straddling"`
	DryRun       bool            `json:"dryRun,omitempty"`
	NewTrack     *dupTrack       `json:"newTrack,omitempty"`
	Steps        []string        `json:"steps,omitempty"`
	Did          []string        `json:"done,omitempty"`
	Rolled       bool            `json:"rolledBack,omitempty"`
	Millis       int64           `json:"millis,omitempty"`
	Note         string          `json:"note,omitempty"`
}

type dupTrack struct {
	TrackIndex int    `json:"trackIndex"`
	TrackName  string `json:"trackName,omitempty"`
	TrackType  string `json:"trackType,omitempty"`
	TrackUUID  string `json:"trackUuid"`
}

// splitCtx 是一次分析的产物。
type splitCtx struct {
	trackIndex int
	trackUUID  string
	trackName  string
	playhead   int64
	clips      []clipBrief
}

type clipBrief struct {
	Index int
	UUID  string
	Name  string
	Type  string
	Begin int64
	End   int64
}

// splitVerdict 判断一个片段在播放头处归哪边。**判据照原脚本**：
//
//	结束 ≤ 播放头  ⇒ 原轨（整段在播放头之前）
//	开始 ≥ 播放头  ⇒ 复制轨（整段在播放头之后）
//	其余（夹在中间）⇒ 切开各一半
//
// ⚠️ 两处边界都是**闭**的（`<=` / `>=`）⇒「正好结束在播放头」归原轨、
// 「正好开始于播放头」归复制轨 —— 与 SV 的 `lastEnd <= playhead` / `firstOnset >= playhead` 一致。
// 抽出来是为了能单测：边界差一个 tick 就是"多切一刀/少切一刀"，而它**不报错**。
func splitVerdict(begin, end, playhead int64) string {
	switch {
	case end <= playhead:
		return verdictLeft
	case begin >= playhead:
		return verdictRight
	default:
		return verdictStraddle
	}
}

const (
	verdictLeft     = "原轨（整段在播放头之前）"
	verdictRight    = "复制轨（整段在播放头之后）"
	verdictStraddle = "切开各一半（跨在播放头上）"
)

// 切开**之后**该删谁。切完判据变简单，而且对"没切到"的片段同样成立：
//
//	原轨  ：删「开始 ≥ 播放头」
//	复制轨：删「结束 ≤ 播放头」
//
// 切开的两半正好按这个分家：原轨左半 `begin < P` 留、右半 `begin == P` 删；
// 复制轨左半 `end == P` 删、右半 `end > P` 留。
func shouldDeleteFromLeft(c clipBrief, playhead int64) bool  { return c.Begin >= playhead }
func shouldDeleteFromRight(c clipBrief, playhead int64) bool { return c.End <= playhead }

// analyzeSplit 读齐"当前轨道 + 播放头 + 这条轨的片段"，并给每个片段判归属。
func analyzeSplit(ctx context.Context, rd ReadFunc) (*splitCtx, *splitReport, error) {
	var caret struct {
		TrackIndex int    `json:"trackIndex"`
		TrackUUID  string `json:"trackUuid"`
	}
	if err := readJSON(ctx, rd, &caret, "caret", "get", "--scope", "arrangement"); err != nil {
		return nil, nil, fmt.Errorf("读标记线（要知道分割哪条轨道）：%w", err)
	}
	playhead, err := readPlaybackHead(ctx, rd)
	if err != nil {
		return nil, nil, fmt.Errorf("读播放头（`transport state`）：%w", err)
	}
	var cl struct {
		Clips []struct {
			ClipType     string  `json:"clipType"`
			ClipName     string  `json:"clipName"`
			ClipBegin    int64   `json:"clipBegin"`
			ClipEnd      int64   `json:"clipEnd"`
			ClipUUID     string  `json:"clipUuid"`
			ClipBeginSec float64 `json:"clipBeginSec"`
		} `json:"clips"`
	}
	if err := readJSON(ctx, rd, &cl, "clip", "list", "--track-index", strconv.Itoa(caret.TrackIndex)); err != nil {
		return nil, nil, fmt.Errorf("读轨道 %d 的片段：%w", caret.TrackIndex, err)
	}
	sc := &splitCtx{trackIndex: caret.TrackIndex, trackUUID: caret.TrackUUID, playhead: playhead}
	rep := &splitReport{TrackIndex: caret.TrackIndex, TrackUUID: caret.TrackUUID,
		PlayheadTick: playhead, Clips: []splitClipView{}}
	sigs := readSignatures(ctx, rd)
	rep.BarBeat = barBeat(playhead, sigs)
	for i, c := range cl.Clips {
		sc.clips = append(sc.clips, clipBrief{Index: i, UUID: c.ClipUUID, Name: c.ClipName,
			Type: c.ClipType, Begin: c.ClipBegin, End: c.ClipEnd})
		v := splitClipView{Index: i, UUID: c.ClipUUID, Name: c.ClipName, Type: c.ClipType,
			Begin: c.ClipBegin, End: c.ClipEnd, BeginSec: c.ClipBeginSec}
		v.Verdict = splitVerdict(c.ClipBegin, c.ClipEnd, playhead)
		switch v.Verdict {
		case verdictLeft:
			rep.LeftClips++
		case verdictRight:
			rep.RightClips++
		default:
			rep.Straddling++
		}
		rep.Clips = append(rep.Clips, v)
	}
	return sc, rep, nil
}

// trackSplitPlan 是干跑：**只读**，把"打算怎么切"摆出来，一条命令都不执行。
func trackSplitPlan(ctx context.Context, rd ReadFunc, _ map[string]string) (any, error) {
	sc, rep, err := analyzeSplit(ctx, rd)
	if err != nil {
		return nil, err
	}
	if len(sc.clips) == 0 {
		return nil, fmt.Errorf("轨道 %d 上没有片段，没什么可切的", sc.trackIndex)
	}
	rep.DryRun = true
	rep.Steps = []string{
		fmt.Sprintf("① `track duplicate --track-index %d` —— 复制一条轨（连片段与 FX 链）", sc.trackIndex),
		fmt.Sprintf("② 用 `clip split --pos %dt` 切开跨在播放头上的 %d 个片段（原轨与复制轨各切一次）",
			sc.playhead, rep.Straddling),
		fmt.Sprintf("③ 原轨删掉「开始 ≥ %d」的片段（%d 个）；复制轨删掉「结束 ≤ %d」的片段（%d 个）",
			sc.playhead, rep.LeftClips+rep.Straddling, sc.playhead, rep.RightClips+rep.Straddling),
	}
	rep.Note = "干跑只能给到这一步：②③ 里的**新 uuid**（复制轨、切开后的两半）要等命令真跑完才知道 —— " +
		"所以计划里给的是**条数与判据**，不是每条命令的原文。"
	if rep.LeftClips == 0 {
		rep.Note += " ⚠️ 没有片段留在原轨 —— 切完原轨是**空的**。"
	}
	if rep.RightClips == 0 && rep.Straddling == 0 {
		rep.Note += " ⚠️ 没有片段留在复制轨 —— 切完复制轨是**空的**（播放头在这条轨的最后一个片段之后）。"
	}
	return rep, nil
}

// trackSplitApply 真跑：多步，边跑边读。
func trackSplitApply(ctx context.Context, rd ReadFunc, _ map[string]string) (any, error) {
	sc, rep, err := analyzeSplit(ctx, rd)
	if err != nil {
		return nil, err
	}
	if len(sc.clips) == 0 {
		return nil, fmt.Errorf("轨道 %d 上没有片段，没什么可切的", sc.trackIndex)
	}
	did := func(format string, a ...any) { rep.Did = append(rep.Did, fmt.Sprintf(format, a...)) }

	// ① 复制轨道
	var dup dupTrack
	if err := readJSON(ctx, rd, &dup, "track", "duplicate",
		"--track-index", strconv.Itoa(sc.trackIndex)); err != nil {
		return nil, fmt.Errorf("复制轨道失败（`track duplicate`）：%w", err)
	}
	rep.NewTrack = &dup
	did("复制出轨道 %d「%s」（%s）", dup.TrackIndex, dup.TrackName, dup.TrackUUID)

	// 回滚：把复制出来的轨道删掉。
	// ⚠️ 编排区的轨道**不能**用 `--track-uuid` 删（那只认 video/marker 带）——
	//    要先把它选中，再 `track delete`（无参）。
	//
	// ⛔ **回滚不是万能的，别让它看起来像万能**：它只能撤掉"复制出这条轨"这一步。
	//    要是失败发生在**删除阶段**，原轨上已经删掉的片段**回不来**
	//  （`clip delete` 每次是全有或全无，但已成功的那次是既成事实）。
	//    两条应对：① 步骤顺序刻意把**最容易失败的 `clip split` 排在删除之前**（见 ②③），
	//    ② 真在删除阶段挂了就**如实说明**，不让用户以为一切复原了。
	deletedLeft, deletedRight := false, false
	rollback := func(reason error) (any, error) {
		selJSON, _ := json.Marshal([]map[string]string{{"trackUuid": dup.TrackUUID}})
		_, _ = rd(ctx, "selection", "set", "--tracks", string(selJSON))
		_, _ = rd(ctx, "track", "delete")
		rep.Rolled = true
		rep.Note = strings.TrimSpace(rep.Note +
			fmt.Sprintf(" ⚠️ 中途失败（%v）。已**回滚**：把复制出来的轨道 %d 删掉了。", reason, dup.TrackIndex))
		if deletedLeft || deletedRight {
			rep.Note += fmt.Sprintf(" ⛔ 但**原轨上已经删掉的片段没有自动还原**"+
				"（原轨侧删过=%v，复制轨侧删过=%v）—— 每一步在 ACE 里都是独立的撤销记录，"+
				"按 Ctrl+Z 可以逐步退回。", deletedLeft, deletedRight)
		} else {
			rep.Note += " 原轨**没动过**（最容易失败的 `clip split` 刻意排在删除之前）。"
		}
		rep.Steps = nil
		return rep, nil
	}

	// ② 切开跨在播放头上的片段 —— 原轨一份、复制轨一份
	splitStraddling := func(track int, clips []clipBrief) error {
		for _, c := range clips {
			if c.Begin < sc.playhead && sc.playhead < c.End {
				if err := runWrite(ctx, rd, "clip", "split", "--clip-uuid", c.UUID,
					"--pos", fmt.Sprintf("%dt", sc.playhead)); err != nil {
					return fmt.Errorf("在轨道 %d 上按 tick %d 切开片段「%s」失败：%w",
						track, sc.playhead, c.Name, err)
				}
				did("轨道 %d 上切开片段「%s」（tick %d）", track, c.Name, sc.playhead)
			}
		}
		return nil
	}
	if err := splitStraddling(sc.trackIndex, sc.clips); err != nil {
		return rollback(err)
	}
	// 复制轨的片段是**复制之前**那批（还没切）⇒ 重新读一次，按同样的判据切
	clipsB, err := clipListBrief(ctx, rd, dup.TrackIndex)
	if err != nil {
		return rollback(err)
	}
	if err := splitStraddling(dup.TrackIndex, clipsB); err != nil {
		return rollback(err)
	}

	// ③ 各自删掉不该留的
	//    ⚠️ 切完之后判据变简单了：原轨删「开始 ≥ P」，复制轨删「结束 ≤ P」
	//   （切开后的两半正好按这个分家，没切到的片段也正好适用）。
	clipsA2, err := clipListBrief(ctx, rd, sc.trackIndex)
	if err != nil {
		return rollback(err)
	}
	if err := deleteWhere(ctx, rd, sc.trackIndex, clipsA2, func(c clipBrief) bool {
		return shouldDeleteFromLeft(c, sc.playhead)
	}, &rep.Did); err != nil {
		return rollback(err)
	}
	deletedLeft = true
	clipsB2, err := clipListBrief(ctx, rd, dup.TrackIndex)
	if err != nil {
		return rollback(err)
	}
	if err := deleteWhere(ctx, rd, dup.TrackIndex, clipsB2, func(c clipBrief) bool {
		return shouldDeleteFromRight(c, sc.playhead)
	}, &rep.Did); err != nil {
		return rollback(err)
	}
	deletedRight = true

	left := countWhere(clipsA2, func(c clipBrief) bool { return !shouldDeleteFromLeft(c, sc.playhead) })
	right := countWhere(clipsB2, func(c clipBrief) bool { return !shouldDeleteFromRight(c, sc.playhead) })
	rep.LeftClips, rep.RightClips = left, right
	rep.Note = fmt.Sprintf("切完：原轨（轨道 %d）留 %d 个片段，复制轨（轨道 %d「%s」）留 %d 个片段。",
		sc.trackIndex, left, dup.TrackIndex, dup.TrackName, right)
	if left == 0 {
		rep.Note += " ⚠️ 原轨现在是**空的**。"
	}
	if right == 0 {
		rep.Note += " ⚠️ 复制轨现在是**空的**。"
	}
	return rep, nil
}

func clipListBrief(ctx context.Context, rd ReadFunc, track int) ([]clipBrief, error) {
	var cl struct {
		Clips []struct {
			ClipType  string `json:"clipType"`
			ClipName  string `json:"clipName"`
			ClipBegin int64  `json:"clipBegin"`
			ClipEnd   int64  `json:"clipEnd"`
			ClipUUID  string `json:"clipUuid"`
		} `json:"clips"`
	}
	if err := readJSON(ctx, rd, &cl, "clip", "list", "--track-index", strconv.Itoa(track)); err != nil {
		return nil, fmt.Errorf("读轨道 %d 的片段：%w", track, err)
	}
	out := make([]clipBrief, 0, len(cl.Clips))
	for i, c := range cl.Clips {
		out = append(out, clipBrief{Index: i, UUID: c.ClipUUID, Name: c.ClipName,
			Type: c.ClipType, Begin: c.ClipBegin, End: c.ClipEnd})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Begin < out[j].Begin })
	return out, nil
}

// deleteWhere 把满足条件的片段**一次删掉**（`clip delete` 支持多个 uuid 且全有或全无）。
func deleteWhere(ctx context.Context, rd ReadFunc, track int, clips []clipBrief,
	hit func(clipBrief) bool, log *[]string) error {
	var ids []string
	for _, c := range clips {
		if hit(c) {
			ids = append(ids, c.UUID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	args := []string{"clip", "delete"}
	for _, id := range ids {
		args = append(args, "--clip-uuid", id)
	}
	if err := runWrite(ctx, rd, args...); err != nil {
		return fmt.Errorf("在轨道 %d 上删 %d 个片段失败：%w", track, len(ids), err)
	}
	*log = append(*log, fmt.Sprintf("轨道 %d 删掉 %d 个片段", track, len(ids)))
	return nil
}

func countWhere(clips []clipBrief, hit func(clipBrief) bool) int {
	n := 0
	for _, c := range clips {
		if hit(c) {
			n++
		}
	}
	return n
}
