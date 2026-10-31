package tools

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"time"
)

// timeNow/timeSince 只是 `time` 的两个薄包装 —— 免得为了一处计时把 time 到处引。
func timeNow() time.Time          { return time.Now() }
func timeSince(t time.Time) int64 { return time.Since(t).Milliseconds() }

// M1 只读巡检工具集：全部 Kind=read，零写入风险。
//
// 命令与 flag 都是照 `acestudio-cli help <命令>` 核过的（2026-10-04 · CLI 0.17.0 / surface 17.1）：
//   · `track list [--type <T>] [--include-empty]`
//   · `clip list (--track-uuid <U> | --track-index <N>)`
//   · `clip note-content --track-index <N> --clip-index <M> [--range-begin/--range-end/--range-scope]`
//   · `clip lyrics` 同上
//   · `vocalparam layers --clip-uuid <U> [--param <P>]`
//   · `sound-source get (--track-uuid <U> | --track-index <N>)`
//   · `history list [--limit <N>]`
//
// ⚠️ 实测踩到的文档漂移：`marker-line` 那篇 topic 通篇讲 `marker get/set/seek`，
// 但这个版本**没有 marker 域** —— 真调用回 exit 2 `unrecognized subcommand 'marker'`，
// CLI 自己提示 "a similar subcommand exists: 'caret'"。所以位置一律用 `caret`。

const catInspect = "巡检"

// Revision 是一个**便宜的"宿主变了没"信号**，给面板做自动刷新用。
//
// ⚠️ 为什么需要它：SV 脚本能拿到**推送**（`registerSelectionCallback` / `registerClearCallback`，
// 见 `[SV2]歌词侧栏.js` 开头），ACE **没有任何推送** —— 只能轮询。所以这里给一个
// 「一次读就能比出变化」的令牌。
//
// 组成（3 次**便宜的**读，实测每次 ~65 ms，进程启动是主要成本、跟载荷大小几乎无关）：
//
//	editor status                → 当前打开的是哪个片段（标记线移走了要重读）
//	clip lyrics                  → `fingerprint`（域 = `clip-note-content`，覆盖歌词/音符内容）
//	selection get --scope editor → 选中的音符 uuid（用哈希压短；**光看 count 不够**：
//	                                换一个音符选，count 不变但面板要显示的那句可能变了）
type Revision struct {
	Track       int    `json:"trackIndex"`
	Clip        int    `json:"clipIndex"`
	ClipName    string `json:"clipName,omitempty"`
	ClipUUID    string `json:"clipUuid,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Selected    int    `json:"selectedCount"`
	Token       string `json:"token"` // 前端只比这个
	Millis      int64  `json:"millis"`
	Note        string `json:"note,omitempty"`
}

func init() {
	register(&Tool{
		ID: "inspect.revision", Category: catInspect,
		Title: "变更令牌（给自动刷新用）",
		Desc: "一个**便宜的「宿主变了没」信号**：一次读回当前片段的身份 + 内容指纹 + 选中音符，" +
			"合成一个 `token`。前端拿它轮询比对，变了就重读对应页面。" +
			"⚠️ ACE **没有推送**（SV 那套 registerSelectionCallback 这里没有），面板要" +
			"「宿主一改就自动刷新」只能轮询，这就是给轮询用的读点。" +
			"⚠️ 3 次读、实测 **221~243 ms**（每次 ~65 ms，主要是进程启动成本，与载荷大小几乎无关 —— " +
			"329 个音符的片段也是这个量级）。",
		Kind: KindRead,
		Inspect: func(ctx context.Context, rd ReadFunc, _ map[string]string) (any, error) {
			return runRevision(ctx, rd)
		},
	})
}

func runRevision(ctx context.Context, rd ReadFunc) (*Revision, error) {
	t0 := timeNow()
	// ⚠️ 这里**只读 `editor status`**，不走 currentClip() —— 那个还会补一次 `clip list`
	//（为了 clipBegin），而"变更令牌"用不上片段起点，能省一次读就省一次。
	var es struct {
		IsAvailable bool   `json:"isAvailable"`
		TrackIndex  int    `json:"trackIndex"`
		ClipIndex   int    `json:"clipIndex"`
		ClipName    string `json:"clipName"`
		ClipUUID    string `json:"clipUuid"`
		EditorType  string `json:"editorType"`
	}
	if err := readJSON(ctx, rd, &es, "editor", "status"); err != nil {
		return nil, err
	}
	rev := &Revision{Track: es.TrackIndex, Clip: es.ClipIndex, ClipName: es.ClipName, ClipUUID: es.ClipUUID}
	if !es.IsAvailable {
		rev.Note = "当前没有打开的片段（编辑区没聚焦在任何片段上）"
		rev.Token = fmt.Sprintf("none|%d|%d", rev.Track, rev.Clip)
		rev.Millis = timeSince(t0)
		return rev, nil
	}

	var ly aceLyrics
	if err := readJSON(ctx, rd, &ly, "clip", "lyrics",
		"--track-index", strconv.Itoa(es.TrackIndex), "--clip-index", strconv.Itoa(es.ClipIndex)); err != nil {
		// 片段类型不是 Sing 之类 ⇒ 令牌里带上错误，仍然给一个**稳定**的值，
		// 免得前端每次轮询都以为"变了"而反复重读
		rev.Note = "读句子失败（多半不是 Sing 片段）：" + err.Error()
		rev.Token = fmt.Sprintf("%s|%d|%d|err", rev.ClipUUID, rev.Track, rev.Clip)
		rev.Millis = timeSince(t0)
		return rev, nil
	}
	rev.Fingerprint = ly.Fingerprint

	sel, _ := readSelectedNoteUUIDs(ctx, rd) // 读不到选区不算失败（可能是别的片段没开编辑器）
	rev.Selected = len(sel)
	rev.Token = fmt.Sprintf("%s|%d|%d|%s|%d|%s",
		rev.ClipUUID, rev.Track, rev.Clip, ly.Fingerprint, len(sel), hashUUIDs(sel))
	rev.Millis = timeSince(t0)
	return rev, nil
}

// hashUUIDs 把选中音符的 uuid 集合压成一个短串（顺序无关 —— 选择顺序不该算"变了"）。
func hashUUIDs(set map[string]bool) string {
	if len(set) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := fnv.New32a()
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
	}
	return strconv.FormatUint(uint64(h.Sum32()), 16)
}

func init() {
	register(&Tool{
		ID: "inspect.overview", Category: catInspect,
		Title: "工程总览",
		Desc:  "工程元数据 + 是否有未保存改动 + 当前编辑器打开的是哪个片段。开工第一眼先看这个。",
		Kind:  KindRead,
		Build: func(_ context.Context, _ ReadFunc, _ map[string]string) ([]Call, error) {
			return []Call{
				{Label: "工程元数据", Args: []string{"project", "info"}},
				{Label: "未保存改动", Args: []string{"project", "dirty"}},
				{Label: "编辑器状态", Args: []string{"editor", "status"}},
			}, nil
		},
	})

	register(&Tool{
		ID: "inspect.tracks", Category: catInspect,
		Title: "轨道清单",
		Desc:  "全部轨道。默认排除空槽；勾上「包含空轨道槽」才含空位。trackIndex 是按 region 计数的，0 起。",
		Kind:  KindRead,
		Params: []Param{
			checkParam("includeEmpty", "包含空轨道槽", true),
		},
		Build: func(_ context.Context, _ ReadFunc, v map[string]string) ([]Call, error) {
			args := []string{"track", "list"}
			if truthy(value(v, "includeEmpty", "1")) {
				args = append(args, "--include-empty")
			}
			return []Call{{Label: "轨道清单", Args: args}}, nil
		},
	})

	register(&Tool{
		ID: "inspect.clips", Category: catInspect,
		Title: "片段清单",
		Desc:  "某条轨道上的片段（clip 与 pattern 在 ACE 里同义）。回包给 clipUuid / clipBegin / clipEnd / noteCount。",
		Kind:  KindRead,
		Params: []Param{
			numParam("trackIndex", "轨道索引（0 起）", "0"),
		},
		Build: func(_ context.Context, _ ReadFunc, v map[string]string) ([]Call, error) {
			n, err := intValue(v, "trackIndex", 0, "轨道索引")
			if err != nil {
				return nil, err
			}
			return []Call{{
				Label: fmt.Sprintf("轨道 %d 的片段", n),
				Args:  []string{"clip", "list", "--track-index", strconv.Itoa(n)},
			}}, nil
		},
	})

	register(&Tool{
		ID: "inspect.notes", Category: catInspect,
		Title: "片段内音符 + 歌词",
		Desc:  "音符几何/音高（pos·dur·endPos·pitch，片段本地 tick）+ 歌词与音素 + noteUuid。⚠️ 回包带 fingerprint —— 之后任何写音符都要把它当 --if-match 带回来。",
		Kind:  KindRead,
		Params: []Param{
			numParam("trackIndex", "轨道索引（0 起）", "0"),
			numParam("clipIndex", "片段索引（0 起）", "0"),
		},
		Build: func(_ context.Context, _ ReadFunc, v map[string]string) ([]Call, error) {
			ti, err := intValue(v, "trackIndex", 0, "轨道索引")
			if err != nil {
				return nil, err
			}
			ci, err := intValue(v, "clipIndex", 0, "片段索引")
			if err != nil {
				return nil, err
			}
			ref := []string{"--track-index", strconv.Itoa(ti), "--clip-index", strconv.Itoa(ci)}
			notes := append([]string{"clip", "note-content"}, ref...)
			lyrics := append([]string{"clip", "lyrics"}, ref...)
			return []Call{
				{Label: "音符内容", Args: notes},
				{Label: "句子级歌词", Args: lyrics},
			}, nil
		},
	})

	register(&Tool{
		ID: "inspect.tempo", Category: catInspect,
		Title: "速度与拍号",
		Desc:  "完整速度自动化表（points[{pos,value,bend}]）与拍号表（signatures[{barPos,numerator,denominator}]）。",
		Kind:  KindRead,
		Build: func(_ context.Context, _ ReadFunc, _ map[string]string) ([]Call, error) {
			return []Call{
				{Label: "速度自动化", Args: []string{"tempo", "get"}},
				{Label: "拍号表", Args: []string{"timesig", "get"}},
			}, nil
		},
	})

	register(&Tool{
		ID: "inspect.transport", Category: catInspect,
		Title: "走带 / 标记线 / 选区",
		Desc:  "播放状态与播放头、标记线（caret）位置、以及当前选区。做「跟着播放头」的工具前先看这里。",
		Kind:  KindRead,
		Build: func(_ context.Context, _ ReadFunc, _ map[string]string) ([]Call, error) {
			return []Call{
				{Label: "走带状态", Args: []string{"transport", "state"}},
				{Label: "标记线（caret）", Args: []string{"caret", "get"}},
				{Label: "编排区选区", Args: []string{"selection", "get", "--scope", "arrangement"}},
			}, nil
		},
	})

	register(&Tool{
		ID: "inspect.vocalparams", Category: catInspect,
		Title: "人声参数分层",
		Desc:  "某个片段上有哪些参数层（baseline/user/envelope/direct/global 与各自的 shape/access）。⚠️ 层与 shape 随引擎世代与声控路由变，每次现读，永不缓存。",
		Kind:  KindRead,
		Params: []Param{
			textParam("clipUuid", "片段 UUID", "", "{xxxxxxxx-…}", "从「片段清单」里抄 clipUuid；注意用户换过工程后旧 uuid 会失效（NOT_FOUND）"),
		},
		Build: func(_ context.Context, _ ReadFunc, v map[string]string) ([]Call, error) {
			uuid := value(v, "clipUuid", "")
			if uuid == "" {
				return nil, fmt.Errorf("需要片段 UUID —— 先跑「片段清单」拿一个")
			}
			return []Call{{
				Label: "参数分层",
				Args:  []string{"vocalparam", "layers", "--clip-uuid", uuid},
			}}, nil
		},
	})

	register(&Tool{
		ID: "inspect.soundsource", Category: catInspect,
		Title: "轨道音源 / 声库",
		Desc:  "某条轨道挂载的音源及其配置（实测回 model{generation,name}）。",
		Kind:  KindRead,
		Params: []Param{
			numParam("trackIndex", "轨道索引（0 起）", "0"),
		},
		Build: func(_ context.Context, _ ReadFunc, v map[string]string) ([]Call, error) {
			n, err := intValue(v, "trackIndex", 0, "轨道索引")
			if err != nil {
				return nil, err
			}
			i := strconv.Itoa(n)
			return []Call{
				{Label: "音源", Args: []string{"sound-source", "get", "--track-index", i}},
				{Label: "声库", Args: []string{"voice", "get", "--track-index", i}},
			}, nil
		},
	})

	register(&Tool{
		ID: "inspect.history", Category: catInspect,
		Title: "撤销栈",
		Desc:  "共享撤销栈（最新在前），含作者与是否已应用。⚠️ 与 SV 脚本不同：ACE 这边**可以真的撤销**（history undo/redo）。",
		Kind:  KindRead,
		Params: []Param{
			numParam("limit", "只看最新 N 条（0 = 全部）", "30"),
		},
		Build: func(_ context.Context, _ ReadFunc, v map[string]string) ([]Call, error) {
			n, err := intValue(v, "limit", 30, "条数")
			if err != nil {
				return nil, err
			}
			args := []string{"history", "list"}
			if n > 0 {
				args = append(args, "--limit", strconv.Itoa(n))
			}
			return []Call{{Label: "撤销栈", Args: args}}, nil
		},
	})
}
