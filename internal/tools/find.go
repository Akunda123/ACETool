package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 「查找」—— **逐条复刻** SV 脚本 `[SV2]查找.js`（SidePanelSection，656 行）。
//
// 复刻口径（用户 2026-10-04 定）：
//
//	· **属性列表照抄**：原脚本 22 个音符属性 + 9 条参数，**顺序与中文标签一字不改**；
//	  ACE 做不到的**保留在列表里**，选中时给**明确说明**（绝不静默 0 命中）。
//	· **行为照抄**：三级范围（当前片段/当前轨道/当前工程）· 四种条件（大于/小于/等于/包含）·
//	  上一项/下一项浏览结果 · 全选目标音符 · 结果文本「序号/轨道/片段/音符序号/位置/数值」。
//	· **只写中文、不建翻译表**（用户 2026-10-04）：原脚本带 ja/en/zh-tw 三套，这里只留 zh-cn 那套的字面。
//
// ACE 侧映射（2026-10-04 真机逐条核过）：
//
//	✅ 对得上 6 条：持续时间（拍）· 歌词 · 音素（实际发音）· 音素（默认发音）· 音高 · 语言
//	❌ 对不上 16 条：音分 1 · 说唱 3 · 颤音调制 1 · 音高过渡 5 · 颤音 6（见 find_match.go 的 unsupportedAttr）
//	⚠️ 参数 9 条里只有 2 条对得上：张力→`tension` · 气声→`air`；另加 ACE 独有的 4 条
//	⛔ **参数可用性一律现读 `vocalparam layers`**（技能 ace-params §1 的硬纪律）：
//	   花名册**随声库的引擎世代变**（v1 `verse24` 有 direct/envelope、无 user；v2 `singing-mamba` 反过来），
//	   同一个参数名在两代上连**尺度和值域都不一样** ⇒ 绝不缓存、绝不按名字猜。
func init() {
	commonFindParams := []Param{
		findCondParam(),
		textParam("value", "查找内容", "", "60 / C4 / 啦 / a / 英", "音高可写 MIDI 数字或音名；黑/白 找黑白键"),
		{Name: "scope", Label: "查找范围", Type: ParamSelect, Default: "clip",
			Options: []Option{
				{Value: "clip", Label: "当前片段"},
				{Value: "track", Label: "当前轨道"},
				{Value: "project", Label: "当前工程"},
			}},
	}
	queryParams := append([]Param{
		{Name: "target", Label: "查找内容", Type: ParamSelect, Default: "note",
			Options: []Option{{Value: "note", Label: "音符属性"}, {Value: "param", Label: "参数"}}},
	}, append([]Param{findAttrParam(), findParamParam(), findLayerParam()}, commonFindParams...)...)

	register(&Tool{
		ID: "find.notes", Category: "查找",
		Title: "查找音符与参数属性",
		Desc: "检索**音符属性**与**参数曲线**，把命中的属性直接标出来：" +
			"条件为 大于 / 小于 / 等于 / 包含，范围可选 当前片段 / 轨道 / 工程。" +
			"命中项在缩略图上高亮标出，并逐条列出对应属性值（点一下跳到该处）。" +
			"⚠️ 属性列表照抄原脚本（音符 22 项 + 参数 12 项）：ACE 做不到的项**保留在列表里**并说明缺什么，" +
			"不拿「0 命中」糊弄。⚠️ 只读；参数命中的是**一段（run）**而非一个点（曲线每 tick 一个值）。",
		Kind:   KindRead,
		Source: "[SV2]查找.js（AKD · 656 行 SidePanelSection）",
		Params: queryParams,
		Inspect: func(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
			return runFind(ctx, rd, vals)
		},
	})

	register(&Tool{
		ID: "find.goto", Category: "查找",
		Title: "跳到某一项",
		Desc: "对应原脚本的「上一项/下一项」：把标记线移到那一项的绝对位置" +
			"（`caret set --tick`），随后回读。给了 `noteUuid` 就**同时**在编辑器里选中它" +
			"（`selection set --scope editor`）。" +
			"⚠️ 原脚本的 resultInfo() 只在「范围=当前片段 **且** 目标=音符属性」时才选音符 —— " +
			"参数命中的是曲线段，没有音符可选，那就只移标记线（所以 noteUuid 可以留空）。" +
			"⚠️ 写：默认干跑，只列出将执行的命令。",
		Kind:   KindWrite,
		Source: "[SV2]查找.js 的 resultInfo()（selectNote + setTimeLeft）",
		Params: []Param{
			textParam("noteUuid", "音符 UUID", "", "{…}", "留空 = 只移标记线（参数命中就是这样）"),
			textParam("absBeat", "绝对位置（拍）", "0", "6", "1 拍 = 一个四分音符；结果的「位置」是片段本地坐标，这里要**工程绝对位置**（拍）"),
		},
		Build: func(_ context.Context, _ ReadFunc, vals map[string]string) ([]Call, error) {
			// 单位口径（用户 2026-10-14）：**对外一律「拍」** ⇒ 这里收拍、内部换算成 tick 给 CLI。
			// 旧名字 `absTick` 仍然认（老调用方/脚本不至于当场坏掉），但**按拍解释**。
			raw := strings.TrimSpace(value(vals, "absBeat", ""))
			if raw == "" {
				raw = strings.TrimSpace(value(vals, "absTick", "0"))
			}
			beat, err := strconv.ParseFloat(raw, 64)
			if err != nil || beat < 0 {
				return nil, fmt.Errorf("绝对位置要是非负的**拍**数（1 拍 = 一个四分音符；如 6 或 4.5），实得 %q", raw)
			}
			tick := beatsToTicks(beat)
			calls := []Call{{Label: fmt.Sprintf("标记线移到第 %s 拍", trimFloat(beat)),
				Args: []string{"caret", "set", "--tick", fmt.Sprintf("%dt", tick)}}}
			uuid := strings.TrimSpace(vals["noteUuid"])
			if uuid == "" {
				// 参数命中：原脚本也是只移视图、不选音符
				return append(calls, Call{Label: "写后回读（标记线）",
					Args: []string{"caret", "get", "--scope", "arrangement"}}), nil
			}
			notes, err := json.Marshal([]map[string]string{{"uuid": uuid}})
			if err != nil {
				return nil, err
			}
			return append(calls,
				Call{Label: "在编辑器里选中这一项",
					Args: []string{"selection", "set", "--scope", "editor", "--mode", "replace",
						"--notes-to-select", string(notes)}},
				Call{Label: "写后回读（标记线）",
					Args: []string{"caret", "get", "--scope", "arrangement"}}), nil
		},
	})

	register(&Tool{
		ID: "find.select", Category: "查找",
		Title: "全选目标音符",
		Desc: "对应原脚本的「全选目标音符」：按同样的条件查一遍，然后把命中的音符**全部选中**" +
			"（`selection set --scope editor --mode replace --notes-to-select`）+ 回读核对。" +
			"⚠️ 编辑器选区只能落在**同一个片段**里 ⇒ 命中跨片段时会拒绝（收窄范围再试）。" +
			"⚠️ 参数命中的是曲线段、不是音符 ⇒ 那种情况请用「跳到某一项」。",
		Kind:   KindWrite,
		Source: "[SV2]查找.js 的 selectAllNotes()",
		Params: queryParams,
		Build: func(ctx context.Context, rd ReadFunc, vals map[string]string) ([]Call, error) {
			if value(vals, "target", "note") == "param" {
				return nil, fmt.Errorf("参数命中的是**曲线段**，不是音符 —— 没法「全选」。请用「跳到某一项」把标记线移过去")
			}
			res, err := runFind(ctx, rd, vals)
			if err != nil {
				return nil, err
			}
			if res.HitCount == 0 {
				return nil, fmt.Errorf("一条都没命中，没什么可选的")
			}
			track, clip, ok := sameClip(res.Hits)
			if !ok {
				return nil, fmt.Errorf("命中的 %d 个音符**跨了多个片段** —— 编辑器的选区只能落在同一个片段里。"+
					"把范围收窄（如「当前片段」）或把条件改得更具体再试", res.HitCount)
			}
			// ⚠️ 形状必须是 `[{"uuid": "{...}"}]` —— 不是裸 uuid 字符串数组。
			// 踩过：传字符串数组回 `INVALID_ARG: field 'notesToSelect': expected object for NoteRef`。
			notes := make([]map[string]string, 0, len(res.Hits))
			for _, h := range res.Hits {
				if h.UUID == "" {
					return nil, fmt.Errorf("有命中项没有 noteUuid（参数曲线段），没法选")
				}
				notes = append(notes, map[string]string{"uuid": h.UUID})
			}
			payload, err := json.Marshal(notes)
			if err != nil {
				return nil, err
			}
			return []Call{
				{Label: fmt.Sprintf("全选 %d 个命中（轨道 %d 片段 %d）", len(notes), track, clip),
					Args: []string{"selection", "set", "--scope", "editor", "--mode", "replace",
						"--notes-to-select", string(payload)}},
				{Label: "写后回读选区", Args: []string{"selection", "get", "--scope", "editor"}},
			}, nil
		},
	})
}

// ---------- 参数定义（照抄原脚本的列表与顺序） ----------

func findCondParam() Param {
	return Param{Name: "cond", Label: "匹配条件", Type: ParamSelect, Default: "eq",
		Options: []Option{
			{Value: "gt", Label: "大于"},
			{Value: "lt", Label: "小于"},
			{Value: "eq", Label: "等于"},
			{Value: "contains", Label: "包含"},
		}}
}

// findAttrParam = **只放 ACE 真能做**的：原脚本能实现的 6 条 + ACE 独有 12 条 = 18 条。
//
// 用户 2026-10-04 裁定：**把 ace 没有的 key 去掉**（前一版把 16 条做不到的留在下拉里"明确拒绝"，
// 现在它们干脆不出现）。但拒绝话术仍在（见 find_match.go 的 unsupportedAttr）——
// 直接拿 API 调 `attr=detune` 照样得到带原因的明确拒绝，只是界面上给不出这个选项。
func findAttrParam() Param {
	return Param{Name: "attr", Label: "音符属性", Type: ParamSelect, Default: "pitch",
		Options: append(append([]Option{}, svAttrSupported...), aceAttrOptions...)}
}

// findParamParam 只留 **ACE 侧真实存在**的参数（用户 2026-10-04 裁定「只留 ace 存在的」）。
//
// ⚠️ 原脚本那 9 条里有 7 条 ACE 根本没有（音高偏差/颤音包络/响度/发声/性别/音区偏移/口型）
// —— 它们已从下拉里摘掉，但 **paramMap 里仍然留着**：
// 直接拿 API 调它们照样会得到**带原因的明确拒绝**，不会静默 0 命中。
func findParamParam() Param {
	return Param{Name: "param", Label: "参数", Type: ParamSelect, Default: "tension",
		Options: []Option{
			{Value: "tension", Label: "张力"},
			{Value: "breathiness", Label: "气声（Air）"},
			{Value: "energy", Label: "能量（Energy）"},
			{Value: "falsetto", Label: "假声（Falsetto）"},
			{Value: "formant", Label: "音色（Formant）"},
			{Value: "dynamic", Label: "整体动态（Dynamic · ACE 2）"},
		}}
}

// findLayerParam 的默认是 **auto**，不是某个具体层名。
//
// ⛔ 为什么：**层集合随引擎世代变** —— v1（`verse24`）是 `direct`、v2（`singing-mamba`）是 `user`，
// 互相都没有对方那个。真机踩过：把默认写死成 v1 的 `direct`，换到 v2 的 clip 上**直接查不了**
// （`tension` 上没有「direct」这一层）。所以默认必须自己去认这一代的那一层。
func findLayerParam() Param {
	return Param{Name: "paramLayer", Label: "参数层", Type: ParamSelect, Default: "auto",
		Options: []Option{
			{Value: "auto", Label: "自动（这一代的「你画过的点」层；没有就退到生效曲线）"},
			{Value: "direct", Label: "direct 层（v1 的「你画过的点」）"},
			{Value: "user", Label: "user 层（v2 的「你画过的点」）"},
			{Value: "effective", Label: "生效曲线（融合后实际唱出来的）"},
			{Value: "baseline", Label: "基线（引擎合成出来的默认）"},
			{Value: "envelope", Label: "envelope 层（乘数 · v1）"},
		}}
}

// resolveLayerAuto 把 `auto` 解成**这一份 clip 上真实存在**的层，并给一句"为什么是它"。
//
// 顺序：① `role=override` 且可写的那层（v1 的 `direct` / v2 的 `user` —— 就是"你画过的点"，
// 与原脚本 `getAllPoints()` 同口径）；② 退到 `effective`（合并结果，永远读得到、也永远不可写）；
// ③ 再退到第一个可写的层（`dynamic` 那种只有 `user`、又没有 `effective` 的 control lane）。
// 解不出来就回空串，调用方照「没有可用层」拒绝。
func resolveLayerAuto(info *vocalParamInfo, vr *vocalRead) (string, string) {
	for _, l := range info.Layers {
		if l.Role == "override" && l.Access == "read-write" {
			if l.Layer == "direct" {
				return l.Layer, ""
			}
			return l.Layer, fmt.Sprintf("这一代的「你画过的点」层是 `%s`（不是 v1 的 `direct`），已用它", l.Layer)
		}
	}
	if vr != nil && vr.Effective != nil {
		return "effective", "这一代没有「你画过的点」层，已退到**生效曲线**（融合后实际唱出来的）"
	}
	for _, l := range info.Layers {
		if l.Access == "read-write" {
			return l.Layer, fmt.Sprintf("这一代没有 override 层、也读不到生效曲线，已退到 `%s`", l.Layer)
		}
	}
	return "", ""
}

// ---------- 查询核心 ----------

type sigEntry struct {
	BarPos      int   `json:"barPos"`
	Tick        int64 `json:"tick"`
	Numerator   int   `json:"numerator"`
	Denominator int   `json:"denominator"`
}

type clipRef struct {
	Track     int
	Clip      int
	TrackName string
	ClipName  string
	ClipType  string
	ClipBegin int64
	ClipEnd   int64
	NoteCount int
	ClipUUID  string
	Enabled   bool
	Color     string
}

type noteRow struct {
	Idx        int
	Pos        int64
	AbsTick    int64
	EndPos     int64
	Dur        int64
	Pitch      int
	Lyric      string
	Syllable   string
	Language   string
	UUID       string
	HeadCons   []float64
	TailCons   []float64
	PhRaw      string // 音素（实际发音）= 当前生效的音素（改过就是改过的）
	PhDefault  string // 音素（默认）= 没编辑过时会唱的音素
	PhRead     bool   // 该片段的 `phoneme list` 是否读到了
	Breath     breathRow
	BreathRead bool // 该片段的 `breath list` 是否读到了
}

// breathRow 是 `breath list --clip-uuid` 的一行（每音符一行）。
// ⚠️ 只有 **Sing 片段**能读（官方：`Must be a Sing clip`）。
type breathRow struct {
	NoteUUID           string  `json:"noteUuid"`
	HasBreath          bool    `json:"hasBreath"`
	RequestedSec       float64 `json:"requestedSec"`
	EffectiveSec       float64 `json:"effectiveSec"`
	DrawnSec           float64 `json:"drawnSec"`
	InsertableSpaceSec float64 `json:"insertableSpaceSec"`
	MinLenSec          float64 `json:"minLenSec"`
	MaxLenSec          float64 `json:"maxLenSec"`
	Shown              bool    `json:"shown"`
}

// HitRow 是一条命中。字段名必须 ASCII（中文标识符在 Go 里算未导出，序列化会变 `{}`）。
type HitRow struct {
	Track     int    `json:"track"`
	Clip      int    `json:"clip"`
	TrackName string `json:"trackName,omitempty"`
	ClipName  string `json:"clipName,omitempty"`
	Index     int    `json:"noteIndex"`
	Tick      int64  `json:"tick"`    // 片段本地
	AbsTick   int64  `json:"absTick"` // 绝对（排序与跳转都用它）
	BarBeat   string `json:"barBeat,omitempty"`
	Pitch     int    `json:"pitch"`
	PitchName string `json:"pitchName,omitempty"`
	Dur       int64  `json:"dur,omitempty"`
	Lyric     string `json:"lyric,omitempty"`
	PhRaw     string `json:"phRaw,omitempty"`
	PhDefault string `json:"phDefault,omitempty"`
	Language  string `json:"language,omitempty"`
	Value     string `json:"value"`
	UUID      string `json:"noteUuid,omitempty"`
	RunLen    int64  `json:"runLenTicks,omitempty"` // 参数命中时：这一段多长（tick）
	// PosSv 是原脚本 `b2p()` 的那个格式：`小节.拍内比例`，两位小数（如 `16.13`）。
	// ⚠️ 与 BarBeat（`16.1.240`）**不是一个东西** —— 那个是给人精确定位用的写法，
	// 这个是原脚本面板「位置:」那一行显示的字面。两者都留着，别互相替代。
	PosSv string `json:"posSv,omitempty"`
}

type findResult struct {
	Target     string   `json:"target"`
	Query      string   `json:"query"`
	Scope      string   `json:"scope"`
	ScopeNote  string   `json:"scopeNote,omitempty"`
	Scanned    []string `json:"scanned,omitempty"`
	ClipCount  int      `json:"clipCount"`
	ExtraCalls int      `json:"extraCalls,omitempty"`
	NoteCount  int      `json:"noteCount"`
	HitCount   int      `json:"hitCount"`
	Truncated  bool     `json:"truncated,omitempty"`
	Millis     int64    `json:"millis"`
	Hits       []HitRow `json:"hits"`
	Note       string   `json:"note,omitempty"`

	// PlayheadTick / StartIndex 复刻原脚本 search() 结尾那段：
	// 它**不是从第 1 项开始**，而是从标记线附近开始（片段往后找、轨道/工程往前找），
	// 这样「下一项」是从你当前的位置往前走。规则照抄，连那个 `index==res.length` 的修正一起。
	PlayheadTick int64 `json:"playheadTick"`
	StartIndex   int   `json:"startIndex"`

	// LayerUsed / LayerNote：参数搜索**实际用了哪一层**、以及为什么是它。
	// `paramLayer=auto` 时由引擎世代决定（v1 `direct` / v2 `user`），不报出来用户就不知道查的是哪层。
	LayerUsed string `json:"layerUsed,omitempty"`
	LayerNote string `json:"layerNote,omitempty"`
}

func runFind(ctx context.Context, rd ReadFunc, vals map[string]string) (*findResult, error) {
	t0 := time.Now()
	target := value(vals, "target", "note")
	scope := value(vals, "scope", "clip")
	cond := value(vals, "cond", "eq")
	want := vals["value"]
	maxClips := atoiOr(value(vals, "maxClips", ""), 400)
	maxHits := atoiOr(value(vals, "maxHits", ""), 500)

	res := &findResult{Target: target, Scope: scope, Hits: []HitRow{}}
	refs, scopeNote, err := resolveClips(ctx, rd, scope, maxClips)
	if err != nil {
		return nil, err
	}
	res.ScopeNote = scopeNote
	sigs := readSignatures(ctx, rd)

	if target == "param" {
		res.Query = paramQuery(vals, cond)
		return runParamFind(ctx, rd, res, refs, vals, cond, want, maxHits, sigs, t0)
	}
	res.Query = noteQuery(vals, cond)
	attr := value(vals, "attr", "pitch")
	// 属性做不到就**立刻**报错：别扫完几百个音符再说（也省得用户以为"ACE 查过了没有"）
	if why, bad := unsupportedAttr[attr]; bad {
		return nil, fmt.Errorf("「%s」在 ACE 里没有对应概念：%s", attrLabel(attr), why)
	}

	for _, ref := range refs {
		var clip struct {
			Notes []struct {
				Pos            int64     `json:"pos"`
				Dur            int64     `json:"dur"`
				EndPos         int64     `json:"endPos"`
				Pitch          int       `json:"pitch"`
				Lyric          string    `json:"lyric"`
				Syllable       string    `json:"syllable"`
				Language       string    `json:"language"`
				NoteUUID       string    `json:"noteUuid"`
				HeadConsonants []float64 `json:"headConsonants"`
				TailConsonants []float64 `json:"tailConsonants"`
			} `json:"notes"`
		}
		if err := readJSON(ctx, rd, &clip, "clip", "note-content",
			"--track-index", strconv.Itoa(ref.Track), "--clip-index", strconv.Itoa(ref.Clip)); err != nil {
			return nil, fmt.Errorf("读片段（轨道 %d 片段 %d）失败：%w", ref.Track, ref.Clip, err)
		}
		res.ClipCount++
		res.Scanned = append(res.Scanned, fmt.Sprintf("轨%d 片%d「%s」%d 音", ref.Track, ref.Clip, ref.ClipName, len(clip.Notes)))

		// 「音素（实际发音）/（默认发音）」和「换气…」各要整片段读一次对应命令 —— **按需**，
		// 属性不是那几类就一次都不花（报告里单列 `extraCalls`）。
		ph := map[string]phonemeRow{}
		if needPhonemes(attr) {
			if ref.ClipUUID == "" {
				return nil, fmt.Errorf("拿不到片段 UUID（轨道 %d 片段 %d），读不了音素；请把「查找范围」改成「当前片段」再试", ref.Track, ref.Clip)
			}
			ph = readPhonemes(ctx, rd, ref)
			res.ExtraCalls++
		}
		br := map[string]breathRow{}
		if needsBreath(attr) {
			if ref.ClipUUID == "" {
				return nil, fmt.Errorf("拿不到片段 UUID（轨道 %d 片段 %d），读不了换气；请把「查找范围」改成「当前片段」再试", ref.Track, ref.Clip)
			}
			var berr error
			br, berr = readBreaths(ctx, rd, ref)
			if berr != nil {
				// 如实报错，不静默 —— 最常见的失败是拿 Instrument / GenericMidi 片段问换气
				return nil, fmt.Errorf("读换气失败（轨道 %d 片段 %d「%s」，类型 %s）：%w —— "+
					"`breath list` **只认 Sing 片段**", ref.Track, ref.Clip, ref.ClipName, ref.ClipType, berr)
			}
			res.ExtraCalls++
		}

		for i, n := range clip.Notes {
			res.NoteCount++
			row := noteRow{Idx: i, Pos: n.Pos, AbsTick: ref.ClipBegin + n.Pos, EndPos: n.EndPos,
				Dur: n.Dur, Pitch: n.Pitch, Lyric: n.Lyric, Syllable: n.Syllable, Language: n.Language,
				UUID: n.NoteUUID, HeadCons: n.HeadConsonants, TailCons: n.TailConsonants}
			if pr, ok := ph[n.NoteUUID]; ok {
				row.PhRaw, row.PhDefault, row.PhRead = pr.Phonemes, pr.DefaultPhonemes, true
			}
			if b, ok := br[n.NoteUUID]; ok {
				row.Breath, row.BreathRead = b, true
			}
			ok, matched, err := matchNote(attr, cond, want, row, ref)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			if len(res.Hits) >= maxHits {
				res.Truncated = true
				break
			}
			res.Hits = append(res.Hits, hitOf(ref, row, matched, sigs, 0))
		}
		if res.Truncated {
			break
		}
	}
	finishFind(ctx, rd, res, t0, maxHits)
	return res, nil
}

func hitOf(ref clipRef, row noteRow, matched string, sigs []sigEntry, runLen int64) HitRow {
	return HitRow{
		Track: ref.Track, Clip: ref.Clip, TrackName: ref.TrackName, ClipName: ref.ClipName,
		Index: row.Idx, Tick: row.Pos, AbsTick: row.AbsTick, BarBeat: barBeat(row.AbsTick, sigs),
		PosSv: b2p(row.AbsTick, sigs),
		Pitch: row.Pitch, PitchName: noteName(row.Pitch), Dur: row.Dur,
		Lyric: row.Lyric, PhRaw: row.PhRaw, PhDefault: row.PhDefault, Language: row.Language,
		Value: matched, UUID: row.UUID, RunLen: runLen,
	}
}

// finishFind 收尾：**按绝对位置排序**（原脚本用 AddObj 做插入排序，就是按 position）、
// 按原脚本的规则定出起始项、报耗时。
func finishFind(ctx context.Context, rd ReadFunc, res *findResult, t0 time.Time, maxHits int) {
	sort.SliceStable(res.Hits, func(i, j int) bool {
		if res.Hits[i].AbsTick != res.Hits[j].AbsTick {
			return res.Hits[i].AbsTick < res.Hits[j].AbsTick
		}
		return res.Hits[i].Index < res.Hits[j].Index
	})
	res.HitCount = len(res.Hits)
	res.PlayheadTick = readPlayhead(ctx, rd)
	res.StartIndex = startIndexFor(res.Hits, res.PlayheadTick, res.Scope == "clip")
	res.Millis = time.Since(t0).Milliseconds()
	if res.Truncated {
		res.Note = strings.TrimSpace(res.Note + fmt.Sprintf(" ⚠️ 命中已达上限 %d，结果被截断", maxHits))
	}
}

// readPlayhead 取标记线位置（= 原脚本的 playhead）。读不到就回 0，不因为这一条让整个查找失败。
func readPlayhead(ctx context.Context, rd ReadFunc) int64 {
	var caret struct {
		Tick int64 `json:"tick"`
	}
	if err := readJSON(ctx, rd, &caret, "caret", "get", "--scope", "arrangement"); err != nil {
		return 0
	}
	return caret.Tick
}

// startIndexFor 照抄原脚本 search() 结尾那段：
//
//	for (i…) if (isGroup ? (res[i].position < playhead) : (res[i].position > playhead)) index++;
//	if (res.length > 0 && index == res.length) index--;
//
// 即：**片段范围**从标记线**往后**找第一个（index = 标记线之前的命中数）；
// **轨道/工程范围**从标记线**往前**找（index = 标记线之后的命中数，于是显示是倒着数的，
// 原脚本的 displayNum = res.length - index 就是在配合这件事）。
func startIndexFor(hits []HitRow, playhead int64, isGroup bool) int {
	if len(hits) == 0 {
		return 0
	}
	idx := 0
	for _, h := range hits {
		if isGroup {
			if h.AbsTick < playhead {
				idx++
			}
		} else if h.AbsTick > playhead {
			idx++
		}
	}
	if idx == len(hits) {
		idx--
	}
	return idx
}

// ---------- 参数搜索 ----------

type vocalLayer struct {
	Layer  string          `json:"layer"`
	Role   string          `json:"role"`
	Shape  string          `json:"shape"`
	Access string          `json:"access"`
	Sparse bool            `json:"sparse"`
	Points json.RawMessage `json:"points"` // shape=dense → [v|null,…]；shape=points → [[t,v|null],…]
}

// dense 取出 dense 载荷（含 null 空档）。
func (l vocalLayer) dense() ([]*float64, bool) {
	if l.Shape != "dense" || len(l.Points) == 0 {
		return nil, false
	}
	var pts []*float64
	if err := json.Unmarshal(l.Points, &pts); err != nil {
		return nil, false
	}
	return pts, true
}

// anchors 取出 points 载荷（锚点）。空档是 [tick, null]。
func (l vocalLayer) anchors() ([][2]*float64, bool) {
	if l.Shape != "points" || len(l.Points) == 0 {
		return nil, false
	}
	var raw [][]*float64
	if err := json.Unmarshal(l.Points, &raw); err != nil {
		return nil, false
	}
	out := make([][2]*float64, 0, len(raw))
	for _, p := range raw {
		if len(p) != 2 {
			continue
		}
		out = append(out, [2]*float64{p[0], p[1]})
	}
	return out, true
}

type vocalParamInfo struct {
	Param             string       `json:"param"`
	DisplayName       string       `json:"displayName"`
	Available         bool         `json:"available"`
	UnavailableReason string       `json:"unavailableReason"`
	Scale             string       `json:"scale"`
	Layers            []vocalLayer `json:"layers"`
	ValueRange        struct {
		Min float64 `json:"min"`
		Max float64 `json:"max"`
	} `json:"valueRange"`
}

type vocalRoster struct {
	ClipUUID          string           `json:"clipUuid"`
	EngineGeneration  string           `json:"engineGeneration"`
	ParamCount        int              `json:"paramCount"`
	VocalControlRoute string           `json:"vocalControlRoute"`
	Params            []vocalParamInfo `json:"params"`
}

type vocalRead struct {
	Param       string       `json:"param"`
	DisplayName string       `json:"displayName"`
	Scale       string       `json:"scale"`
	PosBegin    int64        `json:"posBegin"`
	Count       int          `json:"count"`
	Layers      []vocalLayer `json:"layers"`
	Effective   *vocalLayer  `json:"effective"`
	ValueRange  struct {
		Min float64 `json:"min"`
		Max float64 `json:"max"`
	} `json:"valueRange"`
}

// paramMap 把原脚本那 9 条参数映到 ACE 的参数 id（+ ACE 独有的 4 条）。
// 第二个字段非空 = **ACE 侧根本没有这个概念**，直接拒绝并说明（连查都不查）。
var paramMap = map[string]struct{ Ace, Why string }{
	"tension":     {"tension", ""},
	"breathiness": {"air", ""},
	"energy":      {"energy", ""},
	"falsetto":    {"falsetto", ""},
	"formant":     {"formant", ""},
	"dynamic":     {"dynamic", ""},

	"pitchDelta": {"", "ACE 的人声音高曲线**不在这套接口上**（`vocalparam layers` 里 `pitch` 恒 `available:false`）。" +
		"官方原话：pitch curves are not on this surface yet: the channel stores a delta while the draw primitive " +
		"takes absolute pitch, and anchors and vibrato ride on top ⇒ 要改音高只能走 `.acep` 文件路线"},
	"vibratoEnv": {"", "ACE 没有「颤音包络」这条参数（花名册里没有它）"},
	"loudness":   {"", "ACE 没有 per-clip 的「响度」曲线 —— 它只有 `dynamic` 这个整体动态宏（双极性，一头柔/气、一头力量/胸腔）"},
	"voicing": {"", "ACE 没有「发声（Voicing）」这条参数 —— 实测花名册两代（v1 `verse24`、v2 `singing-mamba`）里都没有它。" +
		"ACE 侧相近的是 `air`（气声）与 `energy`（能量），但没有逐音符的「发声」这一维"},
	"gender":    {"", "ACE 没有「性别」这条参数 —— 音色方向该用「音色（Formant）」：v1 引擎可用、v2 不可用，以现场花名册为准"},
	"toneShift": {"", "ACE 没有「音区偏移（toneShift）」这条参数 —— 它是 SV 的 Automation，ACE 花名册里没有对应项"},
	"mouthOpening": {"", "ACE 没有「口型（Mouth Opening）」这条参数 —— 花名册里没有它。" +
		"ACE 里管音色/咬字的是 `formant`（音色，v1 可用）与 `air`（气声），没有「口型开合」这一维"},
}

func runParamFind(ctx context.Context, rd ReadFunc, res *findResult, refs []clipRef,
	vals map[string]string, cond, want string, maxHits int, sigs []sigEntry, t0 time.Time) (*findResult, error) {

	name := value(vals, "param", "tension")
	m, known := paramMap[name]
	if !known {
		return nil, fmt.Errorf("参数认不出：%q", name)
	}
	if m.Ace == "" {
		return nil, fmt.Errorf("「%s」在 ACE 里没有对应参数：%s", paramLabel(name), m.Why)
	}
	layerWant := value(vals, "paramLayer", "auto")
	if cond == "contains" {
		return nil, fmt.Errorf("参数曲线是数值，不能用「包含」；请用 大于 / 小于 / 等于")
	}
	wantNum, err := wantFloat(want, fmt.Sprintf("参数「%s」的查找值", paramLabel(name)), "例：1 / 1.5 / 0")
	if err != nil {
		return nil, err
	}

	emptySparse := false
	layerUse := ""
	for _, ref := range refs {
		if ref.ClipUUID == "" {
			res.Note = strings.TrimSpace(res.Note + fmt.Sprintf(" 跳过轨%d片%d（没取到片段 UUID）", ref.Track, ref.Clip))
			continue
		}
		// ⛔ 先读这一份 clip 的花名册 —— 可用性/尺度/层集合都由它给（技能 ace-params §1 的硬纪律）
		var roster vocalRoster
		if err := readJSON(ctx, rd, &roster, "vocalparam", "layers", "--clip-uuid", ref.ClipUUID); err != nil {
			return nil, fmt.Errorf("读参数花名册失败：%w", err)
		}
		res.ExtraCalls++
		info := findRosterParam(roster, m.Ace)
		if info == nil {
			have := make([]string, 0, len(roster.Params))
			for _, p := range roster.Params {
				have = append(have, p.Param)
			}
			return nil, fmt.Errorf("这一份 clip 的参数花名册里没有 `%s`（引擎 %s，共 %d 个：%s）—— "+
				"花名册随**声库的引擎世代**变，请以现场的 `vocalparam layers` 为准",
				m.Ace, roster.EngineGeneration, roster.ParamCount, strings.Join(have, " · "))
		}
		if !info.Available {
			return nil, fmt.Errorf("`%s` 在这一份 clip 上**不可用**：%s", m.Ace, info.UnavailableReason)
		}
		// `auto` 不用在这里把关（它要等读到 effective 才能定），具名层先拦住（省一次读）
		if layerWant != "auto" && !layerAvailable(info, layerWant) {
			return nil, fmt.Errorf("`%s` 上没有「%s」这一层 —— 它有的是：%s（层集合也随引擎世代变）",
				m.Ace, layerWant, layerNames(info))
		}

		var vr vocalRead
		if err := readJSON(ctx, rd, &vr, "vocalparam", "read", "--clip-uuid", ref.ClipUUID, "--param", m.Ace); err != nil {
			return nil, fmt.Errorf("读参数 %s 失败：%w", m.Ace, err)
		}
		scale := vr.Scale
		if scale == "" {
			scale = info.Scale
		}
		rng := vr.ValueRange
		if rng.Min == 0 && rng.Max == 0 {
			rng = info.ValueRange
		}
		res.ClipCount++
		res.Scanned = append(res.Scanned, fmt.Sprintf("轨%d 片%d「%s」%s（%s · %g~%g）",
			ref.Track, ref.Clip, ref.ClipName, vr.DisplayName, scale, rng.Min, rng.Max))

		use := layerWant
		if use == "auto" {
			use, res.LayerNote = resolveLayerAuto(info, &vr)
			if use == "" {
				return nil, fmt.Errorf("`%s` 上找不到可用的参数层 —— 它有的是：%s", m.Ace, layerNames(info))
			}
		}
		layerUse = use
		layer := pickLayer(vr, use)
		if layer == nil {
			res.Note = strings.TrimSpace(res.Note + fmt.Sprintf(" 片段「%s」上没有「%s」层", ref.ClipName, use))
			continue
		}
		if layer.Sparse && !hasAnyValue(*layer) {
			emptySparse = true
			continue
		}
		n := scanLayer(ctx, rd, res, ref, *layer, vr.PosBegin, cond, wantNum, maxHits, sigs)
		if n == 0 && layer.Sparse {
			emptySparse = true
		}
		if res.Truncated {
			break
		}
	}
	res.LayerUsed = layerUse
	if emptySparse && len(res.Hits) == 0 {
		res.Note = strings.TrimSpace(res.Note +
			" 提示：「" + layerUse + "」层是**你画过的点**（与原脚本同口径）—— 它是空的就说明还没画过。" +
			" 想看实际唱出来的曲线，把「参数层」换成「生效曲线」。")
	}
	finishFind(ctx, rd, res, t0, maxHits)
	return res, nil
}

// layerNames 把花名册里的层列成一行（拒绝话术里用）。
func layerNames(info *vocalParamInfo) string {
	names := make([]string, 0, len(info.Layers))
	for _, l := range info.Layers {
		names = append(names, fmt.Sprintf("%s(%s/%s)", l.Layer, l.Access, l.Shape))
	}
	if len(names) == 0 {
		return "（一层都没有）"
	}
	return strings.Join(names, " · ")
}

// scanLayer 把一层扫成"命中段"。dense = 每 tick 一个值 ⇒ 连续命中**压成一段**；points = 每个锚点一条。
func scanLayer(ctx context.Context, rd ReadFunc, res *findResult, ref clipRef, layer vocalLayer,
	posBegin int64, cond string, want float64, maxHits int, sigs []sigEntry) int {

	before := len(res.Hits)
	if pts, ok := layer.dense(); ok {
		runStart := -1
		var runMin, runMax float64
		closeRun := func(endIdx int) {
			if runStart < 0 {
				return
			}
			if len(res.Hits) >= maxHits {
				res.Truncated = true
				runStart = -1
				return
			}
			startTick := ref.ClipBegin + posBegin + int64(runStart)
			endTick := ref.ClipBegin + posBegin + int64(endIdx)
			val := trimNum(runMin)
			if runMin != runMax {
				val = trimNum(runMin) + "~" + trimNum(runMax)
			}
			res.Hits = append(res.Hits, HitRow{
				Track: ref.Track, Clip: ref.Clip, TrackName: ref.TrackName, ClipName: ref.ClipName,
				Index: -1, Tick: posBegin + int64(runStart), AbsTick: startTick,
				BarBeat: barBeat(startTick, sigs), PosSv: b2p(startTick, sigs),
				Value: val, RunLen: endTick - startTick,
			})
			runStart = -1
		}
		for i, p := range pts {
			if p != nil && cmp(*p, cond, want) {
				if runStart < 0 {
					runStart, runMin, runMax = i, *p, *p
				} else {
					if *p < runMin {
						runMin = *p
					}
					if *p > runMax {
						runMax = *p
					}
				}
				continue
			}
			closeRun(i)
		}
		closeRun(len(pts))
		return len(res.Hits) - before
	}
	if anchors, ok := layer.anchors(); ok {
		for _, a := range anchors {
			if a[0] == nil || a[1] == nil {
				continue
			}
			if !cmp(*a[1], cond, want) {
				continue
			}
			if len(res.Hits) >= maxHits {
				res.Truncated = true
				break
			}
			tick := ref.ClipBegin + posBegin + int64(*a[0])
			res.Hits = append(res.Hits, HitRow{
				Track: ref.Track, Clip: ref.Clip, TrackName: ref.TrackName, ClipName: ref.ClipName,
				Index: -1, Tick: posBegin + int64(*a[0]), AbsTick: tick,
				BarBeat: barBeat(tick, sigs), PosSv: b2p(tick, sigs), Value: trimNum(*a[1]),
			})
		}
		return len(res.Hits) - before
	}
	return 0
}

func findRosterParam(r vocalRoster, id string) *vocalParamInfo {
	for i := range r.Params {
		if r.Params[i].Param == id {
			return &r.Params[i]
		}
	}
	return nil
}

func layerAvailable(info *vocalParamInfo, want string) bool {
	if want == "effective" {
		return true // effective 是合并结果，不在 layers 矩阵里（且 dynamic 没有它）
	}
	for _, l := range info.Layers {
		if l.Layer == want {
			return true
		}
	}
	return false
}

func pickLayer(vr vocalRead, want string) *vocalLayer {
	if want == "effective" {
		return vr.Effective
	}
	for i := range vr.Layers {
		if vr.Layers[i].Layer == want {
			return &vr.Layers[i]
		}
	}
	return nil
}

func hasAnyValue(l vocalLayer) bool {
	if pts, ok := l.dense(); ok {
		for _, p := range pts {
			if p != nil {
				return true
			}
		}
		return false
	}
	if anchors, ok := l.anchors(); ok {
		for _, a := range anchors {
			if a[1] != nil {
				return true
			}
		}
	}
	return false
}

// ---------- 音素 ----------

type phonemeRow struct {
	Phonemes        string
	DefaultPhonemes string
}

// readPhonemes 读一个片段的音素（每音符一行）。
//
// ⚠️ `phoneme list` **拒绝"整片段"这种无参形式**（官方 hint：There is no clip-wide form）。
// 合法的最小形式是 `--clip <UUID>` 配 `--range-begin` **或** `--range-end`：另一个默认到片段的端点
// （`--range-scope` 默认 `project` = 全局时间轴，所以给的是**绝对 tick**）。实测一次拿全 25 个音符。
func readPhonemes(ctx context.Context, rd ReadFunc, ref clipRef) map[string]phonemeRow {
	out := map[string]phonemeRow{}
	var doc struct {
		Notes []struct {
			NoteUUID        string   `json:"noteUuid"`
			Phonemes        []string `json:"phonemes"`
			DefaultPhonemes []string `json:"defaultPhonemes"`
			IsOverride      bool     `json:"isOverride"`
		} `json:"notes"`
	}
	// 只要 begin，end 默认到片段末尾 —— 少一个可能算错的边界
	err := readJSON(ctx, rd, &doc, "phoneme", "list", "--clip", ref.ClipUUID,
		"--range-begin", fmt.Sprintf("%dt", ref.ClipBegin))
	if err != nil {
		return out
	}
	for _, n := range doc.Notes {
		out[n.NoteUUID] = phonemeRow{
			Phonemes:        strings.Join(n.Phonemes, " "),
			DefaultPhonemes: strings.Join(n.DefaultPhonemes, " "),
		}
	}
	return out
}

// readBreaths 读一个片段的换气（每音符一行）。⚠️ 只有 **Sing 片段**能读，别的类型会明确报错。
func readBreaths(ctx context.Context, rd ReadFunc, ref clipRef) (map[string]breathRow, error) {
	var doc struct {
		Notes []breathRow `json:"notes"`
	}
	if err := readJSON(ctx, rd, &doc, "breath", "list", "--clip-uuid", ref.ClipUUID); err != nil {
		return nil, err
	}
	out := make(map[string]breathRow, len(doc.Notes))
	for _, n := range doc.Notes {
		out[n.NoteUUID] = n
	}
	return out, nil
}

// ---------- 范围 ----------

func resolveClips(ctx context.Context, rd ReadFunc, scope string, maxClips int) ([]clipRef, string, error) {
	switch scope {
	case "clip":
		var es struct {
			IsAvailable bool   `json:"isAvailable"`
			TrackIndex  int    `json:"trackIndex"`
			ClipIndex   int    `json:"clipIndex"`
			ClipName    string `json:"clipName"`
			EditorType  string `json:"editorType"`
			ClipUUID    string `json:"clipUuid"`
		}
		if err := readJSON(ctx, rd, &es, "editor", "status"); err != nil {
			return nil, "", err
		}
		if !es.IsAvailable {
			return nil, "", fmt.Errorf("当前没有打开的片段 —— 编辑器跟着标记线走：把标记线移进一个片段，" +
				"或把「查找范围」改成「当前轨道 / 当前工程」")
		}
		ref := clipRef{Track: es.TrackIndex, Clip: es.ClipIndex, ClipName: es.ClipName,
			ClipType: es.EditorType, ClipUUID: es.ClipUUID}
		// ⚠️ `editor status` 给 UUID / 索引 / 名字，但**不给 clipBegin / clipEnd**。
		// 而绝对位置（排序·跳转）和音素的 `--range-begin` 都要 clipBegin ⇒ 补一次 `clip list`。
		if got, err := clipsOfTrack(ctx, rd, es.TrackIndex, 500); err == nil {
			for _, c := range got {
				if c.Clip == es.ClipIndex {
					if es.ClipName != "" {
						c.ClipName = es.ClipName
					}
					if c.ClipUUID == "" {
						c.ClipUUID = es.ClipUUID
					}
					ref = c
					break
				}
			}
		}
		return []clipRef{ref},
			fmt.Sprintf("当前片段：轨道 %d 片段 %d「%s」（类型 %s）", es.TrackIndex, es.ClipIndex, es.ClipName, es.EditorType), nil

	case "track":
		var caret struct {
			TrackIndex int `json:"trackIndex"`
		}
		if err := readJSON(ctx, rd, &caret, "caret", "get", "--scope", "arrangement"); err != nil {
			return nil, "", err
		}
		refs, err := clipsOfTrack(ctx, rd, caret.TrackIndex, maxClips)
		if err != nil {
			return nil, "", err
		}
		return refs, fmt.Sprintf("当前轨道：轨道 %d（共 %d 个含音符的片段）", caret.TrackIndex, len(refs)), nil

	case "project":
		var tl struct {
			ContentTrackCount int `json:"contentTrackCount"`
			Tracks            []struct {
				TrackIndex int    `json:"trackIndex"`
				Region     string `json:"region"`
				TrackType  string `json:"trackType"`
				TrackName  string `json:"trackName"`
			} `json:"tracks"`
		}
		if err := readJSON(ctx, rd, &tl, "track", "list"); err != nil {
			return nil, "", err
		}
		var refs []clipRef
		skipped := 0
		for _, t := range tl.Tracks {
			if t.Region != "arrangement" {
				continue
			}
			if !trackHasNotes(t.TrackType) {
				skipped++
				continue
			}
			got, err := clipsOfTrack(ctx, rd, t.TrackIndex, maxClips-len(refs))
			if err != nil {
				return nil, "", err
			}
			for i := range got {
				got[i].TrackName = t.TrackName
			}
			refs = append(refs, got...)
			if len(refs) >= maxClips {
				break
			}
		}
		note := fmt.Sprintf("当前工程：%d 条内容轨（跳过 %d 条不含音符的），扫到 %d 个片段",
			tl.ContentTrackCount, skipped, len(refs))
		if len(refs) >= maxClips {
			note += fmt.Sprintf(" ⚠️ 已到片段上限 %d，后面没扫", maxClips)
		}
		return refs, note, nil
	}
	return nil, "", fmt.Errorf("查找范围认不出：%q（可写 clip / track / project）", scope)
}

func clipsOfTrack(ctx context.Context, rd ReadFunc, trackIndex, remain int) ([]clipRef, error) {
	if remain <= 0 {
		return nil, nil
	}
	var cl struct {
		Clips []struct {
			ClipType  string `json:"clipType"`
			ClipName  string `json:"clipName"`
			ClipBegin int64  `json:"clipBegin"`
			ClipEnd   int64  `json:"clipEnd"`
			NoteCount int    `json:"noteCount"`
			ClipUUID  string `json:"clipUuid"`
			Enabled   bool   `json:"enabled"`
			ClipColor string `json:"clipColor"`
		} `json:"clips"`
	}
	if err := readJSON(ctx, rd, &cl, "clip", "list", "--track-index", strconv.Itoa(trackIndex)); err != nil {
		return nil, err
	}
	var refs []clipRef
	for i, c := range cl.Clips {
		if !clipHasNotes(c.ClipType) {
			continue
		}
		refs = append(refs, clipRef{Track: trackIndex, Clip: i, ClipName: c.ClipName, ClipType: c.ClipType,
			ClipBegin: c.ClipBegin, ClipEnd: c.ClipEnd, NoteCount: c.NoteCount, ClipUUID: c.ClipUUID,
			Enabled: c.Enabled, Color: c.ClipColor})
		if len(refs) >= remain {
			break
		}
	}
	return refs, nil
}

// 只有这几种片段带音符；其余（音频等）跳过。
func trackHasNotes(t string) bool {
	switch strings.ToLower(t) {
	case "sing", "instrument", "genericmidi":
		return true
	}
	return false
}

func clipHasNotes(t string) bool { return trackHasNotes(t) }

func readSignatures(ctx context.Context, rd ReadFunc) []sigEntry {
	var doc struct {
		Signatures []sigEntry `json:"signatures"`
	}
	if err := readJSON(ctx, rd, &doc, "timesig", "list"); err != nil {
		return nil
	}
	return doc.Signatures
}

// barBeat 把绝对 tick 算成 `小节.拍.tick偏移`（1 起始）—— 给人精确定位用。
func barBeat(pos int64, sigs []sigEntry) string {
	if len(sigs) == 0 {
		return ""
	}
	cur := sigs[0]
	for _, s := range sigs {
		if s.Tick <= pos {
			cur = s
		}
	}
	if cur.Numerator <= 0 || cur.Denominator <= 0 {
		return ""
	}
	beatTicks := int64(4*480) / int64(cur.Denominator)
	if beatTicks <= 0 {
		return ""
	}
	measTicks := beatTicks * int64(cur.Numerator)
	d := pos - cur.Tick
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%d.%d.%d", cur.BarPos+int(d/measTicks)+1, (d%measTicks)/beatTicks+1, d%measTicks%beatTicks)
}

// b2p 是原脚本 `b2p()` 的逐字移植（面板「位置:」那一行显示的就是它）：
//
//	p = measure.position + measure.denominator*(blick - measure.positionBlick)/(4*measure.numerator*QUARTER) + 1
//	return p.toFixed(2)
//
// 即「小节.拍内比例」，两位小数（4/4 里 1.25 = 第 1 小节第 2 拍）。**不是** barBeat 那个三段式。
// 舍入用 math.Round(v*100)/100 再格式化 —— 与 JS 的 toFixed 在 .xx5 这类边界上一致
// （Go 的 %.2f 本身是 round-half-to-even，16.125 会给 16.12，而 JS 给 16.13）。
func b2p(pos int64, sigs []sigEntry) string {
	if len(sigs) == 0 {
		return ""
	}
	cur := sigs[0]
	for _, s := range sigs {
		if s.Tick <= pos {
			cur = s
		}
	}
	if cur.Numerator <= 0 || cur.Denominator <= 0 {
		return ""
	}
	// 4 * numerator * QUARTER ⇒ 一小节 = 4*numerator 拍；一"拍"按 denominator 分。
	// QUARTER = 480 tick（ACE 的 tick 与 SV 的 blick 在这里口径一致：480 tick = 一拍）。
	p := float64(cur.BarPos) + float64(cur.Denominator)*float64(pos-cur.Tick)/(4*float64(cur.Numerator)*480) + 1
	return strconv.FormatFloat(math.Round(p*100)/100, 'f', 2, 64)
}

func sameClip(hits []HitRow) (track, clip int, ok bool) {
	if len(hits) == 0 {
		return 0, 0, false
	}
	track, clip = hits[0].Track, hits[0].Clip
	for _, h := range hits {
		if h.Track != track || h.Clip != clip {
			return 0, 0, false
		}
	}
	return track, clip, true
}

// ---------- 小工具 ----------

func atoiOr(s string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && v > 0 {
		return v
	}
	return def
}

// optionLabel 从注册表里取下拉项的**中文标签**（结果文本和报错里都用中文，好跟原脚本对账）。
func optionLabel(toolID, paramName, val string) string {
	if t := Get(toolID); t != nil {
		for _, p := range t.Params {
			if p.Name != paramName {
				continue
			}
			for _, o := range p.Options {
				if o.Value == val {
					return o.Label
				}
			}
		}
	}
	return val
}

// attrLabel 取属性的中文名。⚠️ 那 16 条**已经不在下拉里**了（用户裁定去掉），
// 所以查不到时要退到 `svAttrOptions`（原脚本 22 条的全集）——
// 否则 `attr=detune` 的拒绝话术会变成「「detune」在 ACE 里没有…」，白丢了中文名。
func attrLabel(val string) string {
	if s := optionLabel("find.notes", "attr", val); s != val {
		return s
	}
	for _, o := range svAttrOptions {
		if o.Value == val {
			return o.Label
		}
	}
	return val
}

func scopeLabel(val string) string { return optionLabel("find.notes", "scope", val) }

func noteQuery(vals map[string]string, cond string) string {
	return fmt.Sprintf("音符属性「%s」 %s %q · 范围 %s",
		attrLabel(value(vals, "attr", "pitch")), condLabel(cond), vals["value"], scopeLabel(value(vals, "scope", "clip")))
}

func paramQuery(vals map[string]string, cond string) string {
	layer := value(vals, "paramLayer", "auto")
	return fmt.Sprintf("参数「%s」 %s %q · 层 %s · 范围 %s",
		paramLabel(value(vals, "param", "tension")), condLabel(cond), vals["value"],
		optionLabel("find.notes", "paramLayer", layer), scopeLabel(value(vals, "scope", "clip")))
}
