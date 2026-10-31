package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// 歌词侧栏 —— 移植 SV 脚本 `[SV2]歌词侧栏.js`（299 行 SidePanelSection）。
//
// 原脚本做的事情：把当前片段（或选中的音符）按"缺口 / br"切**句**，一页 12 句显示成 12 个
// 可编辑文本框；改哪个框就把那句话按"分割成字符 / 按词"的规则重新分配到那句话的音符上；
// 另有 上一页/下一页、以及一个"跳到某句"的下拉（会移视图 + seek 播放头）。
//
// ⛔ **移植时改了一件根上的事**：原脚本**自己算分句**（看相邻音符有没有缺口、有没有 `br`），
// 而 ACE **有自己的句模型** —— `clip lyrics` 直接报 `sentences[]`（含 index / lyric / noteUuids），
// 而且**写回原语就是按它的下标寻址的**：`lyric fill --sentence <N>`。
// ⇒ 要是沿用原脚本那套启发式分句，`--sentence N` 就会指到**别的句子**上去。
// 所以这里**以 ACE 的句为准**（真实例：4 个音符、1440→1620 有个小缺口，
// 原脚本的启发式会切成 2 句，ACE 认为 1 句 —— 以 ACE 为准才对得上它的面板与写回）。
//
// 另外原脚本的两处毛病**没有照搬**（都在这里写清楚）：
//  1. 字符模式下 `(` 没闭合时，原脚本的 `while (Lyr1[n] != ")")` 会**死循环**（undefined 永不等于 ")"）
//     ⇒ 这里直接报错，绝不 hang。
//  2. 空歌词时 `page` 会被钳到 **-1**，信息行变成「共0页…第-11到0句」⇒ 这里钳到 0 并给一句人话。
func init() {
	// ⚠️ 默认 **auto** 是原脚本的语义：`Lyrics()` 里「有选中的音符就只用选中的，否则用整个片段」。
	// 原脚本面板上**没有范围控件**（它是自动的），所以这里也给 auto 当默认，
	// 面板就不必塞一个原脚本没有的下拉。
	scopeParam := Param{Name: "scope", Label: "范围", Type: ParamSelect, Default: "auto",
		Options: []Option{
			{Value: "auto", Label: "自动（有选中就只算选中的 —— 与原脚本一致）"},
			{Value: "clip", Label: "当前片段（整段）"},
			{Value: "selection", Label: "仅选中的音符所在句"},
		}}

	register(&Tool{
		ID: "lyric.pages", Category: "歌词",
		Title: "歌词侧栏",
		Desc: "逐条复刻 SV 脚本 [SV2]歌词侧栏.js：按**句**分页（默认 12 句/页，**每页 0 = 不分页给全部**），" +
			"每句一行文本，「按字符隔开」决定是按字符还是按词拼。" +
			"⚠️ **句的划分以 ACE 为准**（`clip lyrics` 的 sentences）—— 因为写回原语 " +
			"`lyric fill --sentence N` 就是按它的下标寻址的；原脚本自己那套启发式会指错句子。" +
			"⚠️ 只读。改歌词用「歌词：改一句」。",
		Kind:   KindRead,
		Source: "[SV2]歌词侧栏.js（AKD · 299 行 SidePanelSection）",
		Params: []Param{
			numParam("page", "第几页（0 起）", "0"),
			numParam("perPage", "每页几句（0 = 全部）", "12"),
			splitCharsParam,
			ignoreTenutoParam,
			scopeParam,
		},
		Inspect: func(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
			return runLyricPages(ctx, rd, vals)
		},
	})

	register(&Tool{
		ID: "lyric.setSentence", Category: "歌词",
		Title: "歌词：改一句",
		Desc: "对应原脚本文本框的回调：把**第 N 句**的歌词改成你给的文本。两种写法，**语义不一样**：" +
			"①「按原脚本规则」：逐音符 `note set-lyric --lyrics […]`，按 `o % 词数` 分配 ⇒ " +
			"**词少就循环铺满，一个音符都不会被空着**（原脚本就是这个行为）。" +
			"②「ACE 原生填充」：`lyric fill --sentence N --text …`，一句一条命令、用 ACE 自己的分配规则。" +
			"⭐ **实测（真机 dry-run）②正是 ACE 显示的逆运算**：`-` 那个音符 `filled:false`、`lyricAfter:\"-\"`（连音原样留着），" +
			"`hello` 填回两个音符就是 `hello#1`/`hello#2` ⇒ 所以**忽略连音开着时默认走②**，关掉时走①（逐音符原样，往返精确）。" +
			"⚠️ ②在文本没覆盖到的音符上会**清空歌词**（ACE 自己的行为）。" +
			"⚠️ 写：默认干跑；②自带 `--dry-run`，干跑会把逐音符的「改前 → 改后」摆出来。",
		Kind:   KindWrite,
		Source: "[SV2]歌词侧栏.js 的 textN.setValueChangeCallback()",
		Params: []Param{
			numParam("sentence", "第几句（0 起，ACE 的句下标）", "0"),
			textParam("text", "新歌词文本", "", "ha ppy / 啦 啦 / (b i y)", "按词用空格分；字符模式下写 (…) 折叠到一个音符"),
			{Name: "mode", Label: "写法", Type: ParamSelect, Default: "auto",
				Options: []Option{
					{Value: "auto", Label: "自动（忽略连音 → ACE 原生填充；否则按原脚本逐音符）"},
					{Value: "perNote", Label: "按原脚本规则逐音符（词少就循环铺满，不空音符）"},
					{Value: "fill", Label: "ACE 原生填充（一句一条命令；⚠️ 没覆盖到的音符会被清空）"},
				}},
			splitCharsParam,
			ignoreTenutoParam,
		},
		Build: func(ctx context.Context, rd ReadFunc, vals map[string]string) ([]Call, error) {
			return buildLyricSet(ctx, rd, vals)
		},
	})

	register(&Tool{
		ID: "lyric.goto", Category: "歌词",
		Title: "歌词：跳到某一句",
		Desc: "对应原脚本那个「跳到某句」的下拉：把标记线与播放头都移到该句起点**往前一点**（往前 `leadSec` 秒，默认 0.5），" +
			"便于试听这一句。**只移动，不播放**（要播放用「歌词：试听一句」）。" +
			"⚠️ 提前量按当时的 BPM 折算成 tick（`tempo get` 一个读点；弯折段上差几 tick）。" +
			"⚠️ 写：默认干跑。",
		Kind:   KindWrite,
		Source: "[SV2]歌词侧栏.js 的 combo.setValueChangeCallback()",
		Params: []Param{
			numParam("sentence", "第几句（0 起，ACE 的句下标）", "0"),
			numParam("leadSec", "提前多少秒", "0.5"),
		},
		Build: func(ctx context.Context, rd ReadFunc, vals map[string]string) ([]Call, error) {
			return buildLyricGoto(ctx, rd, vals, false)
		},
	})

	register(&Tool{
		ID: "lyric.audition", Category: "歌词",
		Title: "歌词：试听一句",
		Desc: "**试听一句**（用户 2026-10-05 定的口径）：把播放头移到该句**前 0.5 秒** → 播 → " +
			"**播到该句结束后 0.5 秒暂停**。这里只做前半段（移 + 播）；" +
			"暂停由页面侧按 `transport state` 的 `positionTick` 数到 `endTick + leadTicks` 再调「停止播放」——" +
			"这样 exe 不会被一句 20 秒的歌堵住，用户也能随时点别的。" +
			"⚠️ 写（会真的开始播放），默认干跑。",
		Kind:   KindWrite,
		Source: "[SV2]歌词侧栏.js 的 combo 跳转 + [SV2]填词.js 的 demo（seek 到句前 0.5 s 再 play）",
		Params: []Param{
			numParam("sentence", "第几句（0 起，ACE 的句下标）", "0"),
			numParam("leadSec", "句前留多少秒", "0.5"),
		},
		Build: func(ctx context.Context, rd ReadFunc, vals map[string]string) ([]Call, error) {
			return buildLyricGoto(ctx, rd, vals, true)
		},
	})
}

// ---------- 两个共用参数（显示与写回**必须是同一个**，否则"看到的"和"写回的"不是一回事）----------

// splitCharsParam —— 「按字符隔开」（用户 2026-10-06 定名；原来叫「分割成字符」）。
// 三档而不是勾选框：`auto` 就是那套**语种判断**（歌词大多数为中文就按字符），也是默认。
var splitCharsParam = Param{Name: "splitChars", Label: "按字符隔开", Type: ParamSelect, Default: "auto",
	Options: []Option{
		{Value: "auto", Label: "自动（按语种：歌词大多数为中文就按字符隔开）"},
		{Value: "on", Label: "是（按字符隔开）"},
		{Value: "off", Label: "否（按空格分词）"},
	}}

// ignoreTenutoParam —— 「忽略连音」。
//
// ⭐ 真机取证的两种形态（`devtools/dump-lyrics.js` 打出来的）：
//   - `-`：连音/延音（一个音符把上一个字拖长）—— 这份工程里有 9 个
//   - `hello#1` / `hello#2`：**一个多音符英文词**被拆到两个音符上
//
// 勾上 ⇒ 两者都收起来（`-` 不显示；`hello#1 hello#2` 只显示一个 `hello`）。
// ⚠️ 收起来之后**写回必须走 ACE 原生 `lyric fill`** —— 实测那正是它的逆运算
// （`-` 保持 `filled:false`，`hello` 填回去就是 `hello#1`/`hello#2`）；逐音符写会把它们冲掉。
var ignoreTenutoParam = checkParam("ignoreTenuto", "忽略连音", true)

// ---------- 数据结构 ----------

// aceSentence 是 `clip lyrics` 的一句。
type aceSentence struct {
	Index         int      `json:"index"`
	Lyric         string   `json:"lyric"`
	NoteUuids     []string `json:"noteUuids"`
	SentenceBegin int64    `json:"sentenceBegin"` // **clip-local** tick（实测 timeBase: clip-local）
	SentenceEnd   int64    `json:"sentenceEnd"`
}

type aceLyrics struct {
	ClipUUID      string        `json:"clipUuid"`
	SentenceCount int           `json:"sentenceCount"`
	Sentences     []aceSentence `json:"sentences"`
	// Fingerprint 是 ACE 给这条读的**内容指纹**（域实测是 `clip-note-content`）
	// —— `inspect.revision` 拿它当「宿主改没改」的信号。
	Fingerprint string `json:"fingerprint"`
}

// LyricRow 是页面上的一行 = 一句。字段名必须 ASCII（中文标识符算未导出，序列化会变 `{}`）。
type LyricRow struct {
	Index      int      `json:"index"`    // ACE 的句下标（0 起）—— 写回与跳转都用它
	Text       string   `json:"text"`     // 面板上显示/可编辑的文本（按模式拼好）
	AceLyric   string   `json:"aceLyric"` // ACE 自己报的那串（对账用）
	NoteCount  int      `json:"noteCount"`
	NoteUuids  []string `json:"noteUuids,omitempty"`
	NoteLyrics []string `json:"noteLyrics"` // 逐音符的歌词（字符模式下靠它）
	BeginTick  int64    `json:"beginTick"`  // clip-local
	EndTick    int64    `json:"endTick"`
	AbsBegin   int64    `json:"absBeginTick"`
	AbsEnd     int64    `json:"absEndTick"` // 试听要拿它算"播到句后 0.5 秒停"
	LeadTicks  int64    `json:"leadTicks"`  // 句前留多少 tick（按当时 BPM 折自 leadSec）
	// RoundTrip = 「这一行显示成这样，原样提交回去能不能还原逐音符歌词」。
	// false 时页面会打 ⚠ —— 与其悄悄改坏，不如让用户看见。
	RoundTrip bool   `json:"roundTrip"`
	BarBeat   string `json:"barBeat,omitempty"`
	Selected  bool   `json:"hasSelectedNote,omitempty"`
}

type lyricPage struct {
	Page          int        `json:"page"`
	PerPage       int        `json:"perPage"`
	PageCount     int        `json:"pageCount"`
	SentenceCount int        `json:"sentenceCount"`
	FirstOfPage   int        `json:"firstSentenceNo"` // 1 起（给人看）
	LastOfPage    int        `json:"lastSentenceNo"`
	CanPrev       bool       `json:"canPrev"`
	CanNext       bool       `json:"canNext"`
	Info          string     `json:"info"` // 「共N页 M句歌词 / 当前为第P页 / 第A到B句」
	Mode          string     `json:"mode"`
	SplitChars    bool       `json:"splitChars"`
	SplitAuto     bool       `json:"splitCharsAuto"` // true = 这个值是**按语种判断**出来的
	IgnoreTenuto  bool       `json:"ignoreTenuto"`
	WriteMode     string     `json:"writeMode"` // 改一句时该用哪种写法（跟着忽略连音走）
	Scope         string     `json:"scope"`
	ClipName      string     `json:"clipName,omitempty"`
	ClipUUID      string     `json:"clipUuid,omitempty"`
	ClipBegin     int64      `json:"clipBeginTick,omitempty"`
	LeadTicks     int64      `json:"leadTicks"`
	CharCount     int        `json:"charCount"` // 字数（不含空格与分隔符）—— 服务端也算一份，页面可对账
	SelectedCount int        `json:"selectedCount"`
	Note          string     `json:"note,omitempty"`
	Rows          []LyricRow `json:"rows"`
}

// ---------- 读：分页 ----------

func runLyricPages(ctx context.Context, rd ReadFunc, vals map[string]string) (*lyricPage, error) {
	perPage, err := intValue(vals, "perPage", 12, "每页几句")
	if err != nil {
		return nil, err
	}
	if perPage < 0 {
		return nil, fmt.Errorf("每页几句不能为负，实得 %d（**0 = 不分页，一次给全部**）", perPage)
	}
	page, err := intValue(vals, "page", 0, "第几页")
	if err != nil {
		return nil, err
	}
	ignoreTenuto := boolValue(vals["ignoreTenuto"], true)
	scope := value(vals, "scope", "auto")

	ref, err := currentClip(ctx, rd)
	if err != nil {
		return nil, err
	}
	sentences, err := readSentences(ctx, rd, ref)
	if err != nil {
		return nil, err
	}
	// 逐音符歌词：`clip lyrics` 只给 uuid，字符模式必须知道每个音符唱什么。
	// ⭐ 这里拿到的就是**原始形态**：`-`、`hello#1`、`hello#2` 都在（这是"能读"的那一条）；
	// 顺带带上 `language` —— 语种判断要看它（用户口径：汉语拼音也是中文）。
	allNotes, err := readClipNotesFull(ctx, rd, ref)
	if err != nil {
		return nil, err
	}
	lyricsOf := make(map[string]string, len(allNotes))
	for _, n := range allNotes {
		lyricsOf[n.NoteUUID] = n.Lyric
	}
	selected, err := readSelectedNoteUUIDs(ctx, rd)
	if err != nil {
		return nil, err // 读不到选区不该让整页失败，但要说清楚
	}
	// 原脚本的自动语义（`Lyrics()` 里那段）：有选中的音符就只算选中的，否则算整个片段。
	// ⚠️ 必须在 selected 读完之后再判。
	if scope == "auto" {
		if len(selected) > 0 {
			scope = "selection"
		} else {
			scope = "clip"
		}
	}

	// 「分割成字符」：auto ⇒ 按**文本**判（用户口径：不按语种字段，见 resolveSplitChars 的注释）。
	// 用**整份素材**判，不是逐句判 —— 逐句判会让同一首歌里中英文句各用一套规则，看着就是"有的行紧了有的行松了"。
	var allLyrics []string
	for _, s := range sentences {
		for _, u := range s.NoteUuids {
			allLyrics = append(allLyrics, lyricsOf[u])
		}
	}
	splitChars, splitAuto := resolveSplitChars(vals["splitChars"], allLyrics)

	res := &lyricPage{PerPage: perPage, Scope: scope, ClipName: ref.ClipName, ClipUUID: ref.ClipUUID,
		ClipBegin: ref.ClipBegin, SplitChars: splitChars, SplitAuto: splitAuto,
		IgnoreTenuto: ignoreTenuto, SelectedCount: len(selected)}
	res.Mode = "word"
	if splitChars {
		res.Mode = "char"
	}
	// 写回写法**跟着忽略连音走**（实测：忽略连音开着时 `lyric fill` 正是它的逆运算）
	res.WriteMode = "perNote"
	if ignoreTenuto {
		res.WriteMode = "fill"
	}

	// 句前留多少 tick（默认 0.5 秒）—— 按当时的 BPM 折算；读不到 tempo 就用 1 拍兜底并说明
	leadTicks, leadNote := leadTicksAt(ctx, rd, ref.ClipBegin, 0.5)
	res.LeadTicks = leadTicks
	if leadNote != "" {
		res.Note = leadNote
	}

	rows := make([]LyricRow, 0, len(sentences))
	for _, s := range sentences {
		row := LyricRow{Index: s.Index, AceLyric: s.Lyric, NoteCount: len(s.NoteUuids),
			NoteUuids: s.NoteUuids, BeginTick: s.SentenceBegin, EndTick: s.SentenceEnd,
			AbsBegin: ref.ClipBegin + s.SentenceBegin, AbsEnd: ref.ClipBegin + s.SentenceEnd}
		for _, u := range s.NoteUuids {
			row.NoteLyrics = append(row.NoteLyrics, lyricsOf[u])
			if selected[u] {
				row.Selected = true
			}
		}
		row.Text = joinLyrics(row.NoteLyrics, splitChars, ignoreTenuto)
		row.LeadTicks = leadTicks
		// ⭐ 逐行的「回填得回去吗」自检 —— 两种模式的判据不同（见两个函数的注释）：
		//   开：显示是喂给 `lyric fill` 的（按字符时应当正好等于 ACE 合并出来的那句）
		//   关：显示是逐音符原样，判据是"按同一条规则倒着解能不能 1:1 还原"
		if ignoreTenuto {
			row.RoundTrip = roundTripsCollapsed(row.Text, row.NoteLyrics, s.Lyric, splitChars)
		} else {
			row.RoundTrip = roundTrips(row.Text, row.NoteLyrics, splitChars)
		}
		res.CharCount += countChars(row.Text)
		rows = append(rows, row)
	}

	if scope == "selection" {
		if len(selected) == 0 {
			// ⭐ 用户 2026-10-24：「需要选择音符才能操作的，按 JS 不选择默认 clip 全音符」
			// ⇒ 没选中就当整个片段（和 fill 的默认范围一致），在结果里如实说一句。
			scope = "clip"
			res.Note = "没选中音符 ⇒ 按**整个当前片段**处理（这是 ACE 脚本的默认语义）"
		}
	}
	if scope == "selection" {
		kept := rows[:0]
		res.CharCount = 0
		for _, r := range rows {
			if r.Selected {
				kept = append(kept, r)
				res.CharCount += countChars(r.Text)
			}
		}
		rows = kept
	}

	res.SentenceCount = len(rows)
	// perPage = 0 ⇒ 不分页（页面要"一句话一行"地全给出来）
	if perPage == 0 {
		perPage = len(rows)
		if perPage == 0 {
			perPage = 1
		}
		res.PerPage = perPage
	}
	pg := pageMath(len(rows), perPage, page)
	res.PageCount, res.Page = pg.PageCount, pg.Page
	res.FirstOfPage, res.LastOfPage = pg.First, pg.Last
	res.CanPrev, res.CanNext = pg.CanPrev, pg.CanNext

	// 页码对应的行（照原脚本：本页第 n 行就是第 lo+n 句）
	pageRows := rows[pg.Lo:pg.Hi]
	// barBeat 要拍号表，只在真要显示时才读
	if len(pageRows) > 0 {
		sigs := readSignatures(ctx, rd)
		for i := range pageRows {
			pageRows[i].BarBeat = barBeat(pageRows[i].AbsBegin, sigs)
		}
	}
	res.Rows = pageRows

	// 字数按**本页**算（页面一页就是全部行）
	res.CharCount = 0
	for i := range pageRows {
		res.CharCount += countChars(pageRows[i].Text)
	}

	if len(rows) == 0 {
		res.Info = "共0页0句歌词\n当前没有歌词可以显示"
		if res.Note == "" {
			res.Note = "这一份片段里没有歌词（`clip lyrics` 报 0 句）—— 先在 ACE 里给音符写上歌词"
		}
	} else {
		res.Info = fmt.Sprintf("共%d页%d句歌词\n当前为第%d页\n第%d到%d句",
			pg.PageCount, len(rows), pg.Page+1, pg.First, pg.Last)
	}

	return res, nil
}

// pageInfo 是分页算术的结果。抽成独立函数是为了能单测 ——
// 页数 / 上下页可用性 / 页码钳制这三条最容易错，而且错了只表现为"按钮状态不对"，很难发现。
type pageInfo struct {
	PageCount int
	Page      int
	CanPrev   bool
	CanNext   bool
	First     int // 1 起（给人看）
	Last      int
	Lo, Hi    int // 行区间（0 起，半开）
}

func pageMath(rowCount, perPage, page int) pageInfo {
	var p pageInfo
	if perPage <= 0 {
		perPage = 1
	}
	p.PageCount = (rowCount + perPage - 1) / perPage
	if p.PageCount == 0 {
		// ⚠️ 原脚本在空歌词时会把 page 钳到 **-1**（信息行变成「第-11到0句」）—— 不照搬
		p.Page = 0
	} else {
		if page < 0 {
			page = 0
		}
		if page > p.PageCount-1 {
			page = p.PageCount - 1
		}
		p.Page = page
	}
	p.Lo = p.Page * perPage
	if p.Lo > rowCount {
		p.Lo = rowCount
	}
	p.Hi = p.Lo + perPage
	if p.Hi > rowCount {
		p.Hi = rowCount
	}
	p.First = p.Lo + 1
	p.Last = p.Hi
	p.CanPrev = p.Page > 0
	// 原脚本：`if(page > ceil(len/12)-2) next.setEnabled(false)` ⇒ 最后一页上「下一页」禁用
	p.CanNext = p.PageCount > 0 && p.Page < p.PageCount-1
	return p
}

// currentClip 取当前打开的片段（编辑器跟着标记线走）。与 find.go 的口径一致。
func currentClip(ctx context.Context, rd ReadFunc) (clipRef, error) {
	var es struct {
		IsAvailable bool   `json:"isAvailable"`
		TrackIndex  int    `json:"trackIndex"`
		ClipIndex   int    `json:"clipIndex"`
		ClipName    string `json:"clipName"`
		EditorType  string `json:"editorType"`
		ClipUUID    string `json:"clipUuid"`
	}
	if err := readJSON(ctx, rd, &es, "editor", "status"); err != nil {
		return clipRef{}, err
	}
	if !es.IsAvailable {
		return clipRef{}, fmt.Errorf("当前没有打开的片段 —— 编辑器跟着标记线走：把标记线移进一个片段再试")
	}
	if es.EditorType != "" && !strings.EqualFold(es.EditorType, "sing") {
		return clipRef{}, fmt.Errorf("当前片段类型是 %s，不是 Sing —— 歌词/句子只对 Sing 片段成立"+
			"（`clip lyrics` 与 `lyric fill` 都只认 Sing）", es.EditorType)
	}
	ref := clipRef{Track: es.TrackIndex, Clip: es.ClipIndex, ClipName: es.ClipName,
		ClipType: es.EditorType, ClipUUID: es.ClipUUID}
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
	return ref, nil
}

func readSentences(ctx context.Context, rd ReadFunc, ref clipRef) ([]aceSentence, error) {
	var ly aceLyrics
	if err := readJSON(ctx, rd, &ly, "clip", "lyrics",
		"--track-index", strconv.Itoa(ref.Track), "--clip-index", strconv.Itoa(ref.Clip)); err != nil {
		return nil, fmt.Errorf("读句子失败（轨道 %d 片段 %d）：%w", ref.Track, ref.Clip, err)
	}
	return ly.Sentences, nil
}

// readNoteLyrics 拿 uuid → 歌词。字符模式下必须知道每个音符唱什么（`clip lyrics` 只给 uuid）。
func readNoteLyrics(ctx context.Context, rd ReadFunc, ref clipRef) (map[string]string, error) {
	var clip struct {
		Notes []struct {
			Lyric    string `json:"lyric"`
			NoteUUID string `json:"noteUuid"`
		} `json:"notes"`
	}
	if err := readJSON(ctx, rd, &clip, "clip", "note-content",
		"--track-index", strconv.Itoa(ref.Track), "--clip-index", strconv.Itoa(ref.Clip)); err != nil {
		return nil, fmt.Errorf("读音符失败（轨道 %d 片段 %d）：%w", ref.Track, ref.Clip, err)
	}
	out := make(map[string]string, len(clip.Notes))
	for _, n := range clip.Notes {
		out[n.NoteUUID] = n.Lyric
	}
	return out, nil
}

// readSelectedNoteUUIDs 取编辑器里选中的音符 uuid。读不到就给空集（不因此让整页失败）。
func readSelectedNoteUUIDs(ctx context.Context, rd ReadFunc) (map[string]bool, error) {
	var sel struct {
		Notes []struct {
			NoteUUID string `json:"noteUuid"`
			UUID     string `json:"uuid"`
		} `json:"notes"`
	}
	if err := readJSON(ctx, rd, &sel, "selection", "get", "--scope", "editor"); err != nil {
		return map[string]bool{}, nil
	}
	out := make(map[string]bool, len(sel.Notes))
	for _, n := range sel.Notes {
		u := n.NoteUUID
		if u == "" {
			u = n.UUID
		}
		if u != "" {
			out[u] = true
		}
	}
	return out, nil
}

// joinLyrics 拼出**面板上显示的那一行**。
//
// ⭐ 用户 2026-10-06 定稿（第三次，以此为准）：
//
//	「**分割成字符勾选情况下同一个音符有多个字符内容时带括号**」
//
// ⇒ **括号只由 `needsParens` 决定，与「忽略连音」无关**。这是关键修正：
// 以前括号只在「忽略连音 **关**」那一侧出现（那是逐音符写回的路子），于是
// **一勾/一取消忽略连音，整页括号就变了个样**（用户报的 bug：`la la la` ↔ `(la)(la)(la)`）。
// 现在两种模式的显示规则**完全一样**，只差 `-` 与 `#n`（那才是这个开关的意思）：
//
//	sep = 分割成字符 ? "" : " "      ← 两种模式同一套
//	会被分隔符切开的多字词套 ()        ← 两种模式同一套（见 needsParens）
//
// 写回时再各自处理：`lyric fill` 那条路先把显示**折成 ACE 原生形态**（`aceFillText`），
// 逐音符那条路直接解析（`parseLyricText`，括号本来就是它的语法）。
func joinLyrics(lyrics []string, splitChars, ignoreTenuto bool) string {
	toks := lyrics
	if ignoreTenuto {
		toks = collapseTenuto(lyrics) // 只收 `-` 与 `#n`，**不动括号规则**
	}
	sep := " "
	if splitChars {
		sep = ""
	}
	parts := make([]string, 0, len(toks))
	for _, l := range toks {
		if needsParens(l, splitChars) {
			l = "(" + l + ")"
		}
		parts = append(parts, l)
	}
	return strings.Join(parts, sep)
}

// aceFillText 把**面板上显示的那串**折成 ACE 原生 `lyric fill` 要的形态。
//
// 为什么必须有这一步：显示用的是原脚本的记号（按字符时 `(la)(la)`、不留空格），
// 而 `lyric fill` 认的是 ACE 自己的形态（**不带括号、CJK 紧排、其余空格**）。
// 两者不是一回事 —— 直接把显示喂过去，括号会被当成**字面字符**写进歌词。
func aceFillText(text string, splitChars bool) (string, error) {
	toks, err := parseLyricText(text, splitChars)
	if err != nil {
		return "", err
	}
	if len(toks) == 0 {
		return "", fmt.Errorf("解析出来 0 个词 —— 文本是 %q", text)
	}
	return joinCollapsed(toks, true), nil
}

// needsParens 判一个词要不要套 `()`。
//
// ⭐ 用户 2026-10-06 定稿：「**按字符隔开勾选情况下同一个音符有多个字符内容时带括号**」
// ＋「**不按字符隔开时不显示括号，空格就是分隔符**」⇒ 一句话：
//
//	**只有「按字符隔开」时才需要括号** —— 那时分隔符是空串，多字词会被切散，必须括起来保护；
//	不按字符隔开时分隔符是空格，**空格本身就是分隔符**，不需要（也不该）出现括号。
//
// ⚠️ 代价（如实说）：不按字符隔开时，**一个音符的内容本身就带空格**（如 `b iy`）就回填不回去了 ——
// 那时空格分不清是"分隔符"还是"歌词里的空格"。这种情况逐行的 `roundTrip` 自检会打 ⚠，
// 而不是偷偷塞一对括号进去（塞了才是"相邻两个音符看起来一样、填回去却变了"）。
func needsParens(tok string, splitChars bool) bool {
	return splitChars && len([]rune(tok)) > 1
}

// collapseTenuto 把「连音」收起来：丢 `-`，并把同一个多音符词的后半截丢掉。
//
//	["jin","qing","zhan","-","fang"]        → ["jin","qing","zhan","fang"]
//	["hello#1","hello#2"]                   → ["hello"]
//	["hello#1","hello#2","world#1"]         → ["hello","world"]   （不同词各自保留）
func collapseTenuto(lyrics []string) []string {
	out := make([]string, 0, len(lyrics))
	for _, l := range lyrics {
		if l == "-" {
			continue
		}
		base, has := stripSyllable(l)
		if !has {
			out = append(out, l)
			continue
		}
		if n := len(out); n > 0 && out[n-1] == base {
			continue // 同一个词的后半截
		}
		out = append(out, base)
	}
	return out
}

// stripSyllable 认 ACE 的多音符词后缀：`hello#2` → (`hello`, true)。
// 认不出就原样返回 false —— **不猜**（比如歌词里本来就有个 `#`）。
func stripSyllable(l string) (string, bool) {
	i := strings.LastIndex(l, "#")
	if i <= 0 || i == len(l)-1 {
		return l, false
	}
	for _, r := range l[i+1:] {
		if r < '0' || r > '9' {
			return l, false
		}
	}
	return l[:i], true
}

// joinCollapsed 拼成 **ACE 自己的形态**：两个 CJK 词之间紧排、其余空格。
//
// ⚠️ 它**不再用于面板显示**（显示见 `joinLyrics`，那套是原脚本的记号、带括号），
// 现在只有一个用处：**`lyric fill` 之前把显示折回 ACE 要的形态**（`aceFillText`）。
// 这正是 ACE 自己合并出来的样子（实测 `clip lyrics` 的 `sentence.lyric` 就是"CJK 紧、拉丁空格"）。
func joinCollapsed(toks []string, cjkTight bool) string {
	var b strings.Builder
	for i, t := range toks {
		if i > 0 {
			tight := cjkTight && isCJKToken(toks[i-1]) && isCJKToken(t)
			if !tight {
				b.WriteString(" ")
			}
		}
		b.WriteString(t)
	}
	return b.String()
}

func isCJKRune(r rune) bool {
	return (r >= 0x3400 && r <= 0x9fff) || // 汉字（含扩展 A）
		(r >= 0xf900 && r <= 0xfaff) || // 兼容汉字
		(r >= 0x3040 && r <= 0x30ff) || // 假名
		(r >= 0xac00 && r <= 0xd7af) // 谚文
}

// isCJKToken：整串没有空白、且至少有一个 CJK 字 —— 才当"按字符排的那类词"。
// 英文词（`hello`）和拼音（`jin`）都不算，所以它们之间永远留着空格（否则拼回去就毁了）。
func isCJKToken(s string) bool {
	if strings.ContainsAny(s, " \t") {
		return false
	}
	for _, r := range s {
		if isCJKRune(r) {
			return true
		}
	}
	return false
}

// countChars 数字数：不算空白（分隔符本来就不是字）。
func countChars(s string) int {
	n := 0
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			n++
		}
	}
	return n
}

// resolveSplitChars 解「分割成字符」这一档：auto ⇒ 按**文本**判（**不用 ACE 的语种字段**）。
//
// ⚠️ 用户 2026-10-05 明确否掉了"看语种"这条路：「**不按语种**，汉语拼音也算中文，
// 按语种会让汉语拼音组成的**不好编辑**」。原因很实在：ACE 把拼音音符的 `language` 报成 `Chinese`，
// 拿它当"中文"⇒ 中文模式的分隔符是 `""` ⇒ 拼音会被粘成 `suizhejiao`，没人能编辑。
//
// 所以判据是**字符**（与空格规则同一个口径）：一个音符一个单位，谁含 CJK 字谁算中文。
// 拼音音符是拉丁字符 ⇒ 不推动"按字符"，但它自己**是一个单位**（不会被拆成字母）。
func resolveSplitChars(raw string, lyrics []string) (split bool, auto bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto", "自动":
		return mostlyCJK(lyrics), true
	case "1", "true", "on", "yes", "y", "是", "有", "开", "真":
		return true, false
	default:
		return false, false
	}
}

func mostlyCJK(lyrics []string) bool {
	cjk, other := 0, 0
	for _, l := range lyrics {
		if l == "" || l == "-" {
			continue
		}
		if base, ok := stripSyllable(l); ok {
			l = base
		}
		hit := false
		for _, r := range l {
			if isCJKRune(r) {
				hit = true
				break
			}
		}
		if hit {
			cjk++
		} else {
			other++
		}
	}
	if cjk+other == 0 {
		return false
	}
	return cjk*2 >= cjk+other // 过半（含正好一半）
}

// leadTicksAt 把「句前留 N 秒」折成 tick：读一次 `tempo get`，取**该位置当时的 BPM**。
// ⚠️ 这是折中的做法：曲子中途变速时，提前量按句首的 BPM 算（0.5 秒的量级上差几 tick，
// 不值得为它多跑几次 `convert`）。读不到 tempo 就退回 1 拍（480 tick）并回一句人话。
func leadTicksAt(ctx context.Context, rd ReadFunc, atTick int64, sec float64) (int64, string) {
	var tp struct {
		Points []struct {
			Pos   int64   `json:"pos"`
			Value float64 `json:"value"`
		} `json:"points"`
		Tempo float64 `json:"tempo"`
	}
	if err := readJSON(ctx, rd, &tp, "tempo", "get"); err != nil || len(tp.Points) == 0 {
		return 480, "读不到速度表（`tempo get`），试听的句前提前量退回 1 拍（480 tick）"
	}
	bpm := tp.Points[0].Value
	for _, p := range tp.Points {
		if p.Pos <= atTick {
			bpm = p.Value
		}
	}
	if bpm <= 0 {
		return 480, "速度表里的 BPM 是 0，试听的句前提前量退回 1 拍（480 tick）"
	}
	// 480 PPQ：一秒的 tick 数 = BPM/60 * 480
	ticks := int64(sec*float64(bpm)/60.0*480.0 + 0.5)
	if ticks < 0 {
		ticks = 0
	}
	return ticks, ""
}

// roundTrips 自检（**只用于「忽略连音关」**）：显示文本按同一条规则倒着解一遍，
// 能不能还原出逐音符歌词。走的就是原脚本写回那条路（`parseLyricText` + `assignLyrics`）。
func roundTrips(text string, notes []string, splitChars bool) bool {
	parsed, err := parseLyricText(text, splitChars)
	if err != nil || len(parsed) == 0 {
		return false
	}
	idx := assignLyrics(len(parsed), len(notes))
	for i, k := range idx {
		if parsed[k] != notes[i] {
			return false
		}
	}
	return true
}

// roundTripsCollapsed 自检（**只用于「忽略连音开」**）：这一版显示会先被 `aceFillText`
// 折成 ACE 形态再喂给 `lyric fill` ⇒ 判据是**这条折法无损**：
//
//	① 显示解析回来，必须**正好是收起来的那些词**（数量与内容一一对应）
//	② 折出来的 ACE 形态，必须**等于 ACE 自己报的那句**（`clip lyrics` 的 sentence.lyric）
//
// 这两条合起来才说明"显示 → 回填"是可信的。第 ② 条不再依赖"按字符时显示要等于 ACE 那句话"
// —— 显示现在带括号（用户 2026-10-06 口径），本来就**不该**等于 ACE 的合并句。
func roundTripsCollapsed(text string, notes []string, aceLyric string, splitChars bool) bool {
	toks, err := parseLyricText(text, splitChars)
	if err != nil {
		return false
	}
	want := collapseTenuto(notes)
	if len(toks) != len(want) {
		return false
	}
	for i := range toks {
		if toks[i] != want[i] {
			return false
		}
	}
	return joinCollapsed(toks, true) == aceLyric
}

// parseLyricText 解析文本框里的那一行，**逐字符扫描**（不再按分隔符先切）：
//
//	`(` … `)` 之间的东西**原样算一个词**（里面的空格也算进去）—— 这是"一个音符唱一整词"的写法
//	其余按分隔符切：按字符模式每个字符一个词，按词模式空格分词
//
// ⭐ 为什么要改成扫描：用户 2026-10-05 要求「**非 cjk 在不勾选（按词分）时也加 `()`**」——
// 而旧的实现是"先按空格切、再看某个 token 是不是恰好等于 `(`"，于是按词模式下
// `(jin) qing` 会被切成 `"(jin)"` / `"qing"` 两个词（括号根本没被认出来）。
// 扫描式两种模式下都认括号，**显示与写回才是同一条规则**。
//
// ⛔ 原脚本里 `while (Lyr1[n] != ")")` 在**括号没闭合**时会死循环
// （`undefined != ")"` 永远成立，于是无限拼 "undefined"）—— 这里明确报错。
func parseLyricText(text string, splitChars bool) ([]string, error) {
	var out []string
	var cur strings.Builder
	// ⚠️ **空串不吐**：`(jin) qing` 在 `(` 前会攒出一个空串，吐出去就多一个"空词"，
	// 分配时会凭空占掉一个音符（第一版就是这么错的）。
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '(':
			flush()
			var acc strings.Builder
			i++
			for {
				if i >= len(runes) {
					return nil, fmt.Errorf("括号没闭合：文本里有个 `(` 找不到配对的 `)`（" +
						"要写 `(b i y)` 这样，中间的空格会被算进同一个词）")
				}
				if runes[i] == ')' {
					break
				}
				acc.WriteRune(runes[i])
				i++
			}
			out = append(out, acc.String())
		case splitChars:
			// 按字符：每个字符一个词。空格只当分隔符（**不当成某个音符的歌词**）；
			// 落单的 `)` 照常当一个字符（歌词里本来可能有括号）
			if r == ' ' || r == '\t' {
				flush()
				continue
			}
			cur.WriteRune(r)
			flush()
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out, nil
}

// assignLyrics 是原脚本分配那一段：`num = o % len`（只有一个词时全给同一个）。
//
// ⚠️ 保留原脚本的 `%` 语义（词比音符少就**循环**用）—— 这是它的行为，不是 bug：
// 用户在框里少写几个词时，原脚本就是让词循环铺满那句话。
func assignLyrics(lyricCount, noteCount int) []int {
	idx := make([]int, noteCount)
	if lyricCount == 1 {
		return idx // 全 0
	}
	for o := 0; o < noteCount; o++ {
		idx[o] = o % lyricCount
	}
	return idx
}

// ---------- 写：改一句 ----------

func buildLyricSet(ctx context.Context, rd ReadFunc, vals map[string]string) ([]Call, error) {
	// ⚠️ **句下标必须显式给**，不给默认 0 —— 否则漏填就静默改掉第 1 句（写错句子很难发现）
	if strings.TrimSpace(vals["sentence"]) == "" {
		return nil, fmt.Errorf("要指定第几句（0 起，ACE 的句下标）—— 先用「歌词侧栏」看一下现在是哪几句。" +
			"这里**故意不给默认值**：漏填就写掉第 1 句是很容易看漏的事故")
	}
	sentence, err := intValue(vals, "sentence", 0, "第几句")
	if err != nil {
		return nil, err
	}
	text := vals["text"]
	mode := value(vals, "mode", "auto")
	ignoreTenuto := boolValue(vals["ignoreTenuto"], true)

	ref, err := currentClip(ctx, rd)
	if err != nil {
		return nil, err
	}
	if ref.ClipUUID == "" {
		return nil, fmt.Errorf("拿不到片段 UUID，写不了歌词（轨道 %d 片段 %d）", ref.Track, ref.Clip)
	}
	sentences, err := readSentences(ctx, rd, ref)
	if err != nil {
		return nil, err
	}
	var target *aceSentence
	for i := range sentences {
		if sentences[i].Index == sentence {
			target = &sentences[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("没有第 %d 句（这一份片段一共 %d 句，下标 0 起：%s）—— "+
			"先用「歌词侧栏」看一下现在是哪几句", sentence, len(sentences), sentenceIndexList(sentences))
	}
	if len(target.NoteUuids) == 0 {
		return nil, fmt.Errorf("第 %d 句里没有音符（ACE 报的 noteUuids 是空的），没地方写", sentence)
	}

	// 分割方式：与**显示**用同一个解（auto 按语种）—— 两边不一致的话，
	// 用户看到的行与写回去的那串就不是一回事了。
	lyricsOf, _ := readNoteLyrics(ctx, rd, ref)
	var line []string
	for _, u := range target.NoteUuids {
		line = append(line, lyricsOf[u])
	}
	splitChars, splitAuto := resolveSplitChars(vals["splitChars"], line)

	// `auto` 的写法：**跟着忽略连音走**（实证：忽略连音开着时显示是 ACE 的合并形态，
	// 而 `lyric fill` 正是它的逆运算；关着时显示是逐音符原样，只能 1:1 写回去）。
	if mode == "auto" {
		if ignoreTenuto {
			mode = "fill"
		} else {
			mode = "perNote"
		}
	}
	why := fmt.Sprintf("（按字符隔开：%s%s）", onOff(splitChars), autoSuffix(splitAuto))

	switch mode {
	case "fill":
		// ✅ 首选：ACE 原生填充。一句一条命令，而且它**自带 --dry-run**（干跑时逐音符给改前→改后）
		// ⚠️ 但喂给它的**不是显示的那串**：显示是原脚本的记号（`(la)(la)`、按字符时不留空格），
		// 而 `lyric fill` 认的是 ACE 形态（不带括号、CJK 紧排）⇒ 先折一道（`aceFillText`）。
		fillText, err := aceFillText(text, splitChars)
		if err != nil {
			return nil, err
		}
		lbl := fmt.Sprintf("填充第 %d 句（%d 个音符）%s", sentence, len(target.NoteUuids), why)
		if fillText != text {
			lbl += fmt.Sprintf("，折成 ACE 形态 %q", fillText)
		}
		return []Call{{Label: lbl,
			Args: []string{"lyric", "fill", "--clip", ref.ClipUUID,
				"--sentence", strconv.Itoa(sentence), "--text", fillText}}}, nil

	case "perNote":
		// 原脚本那条路：自己分词 → 逐音符分配 → note set-lyric --lyrics
		parsed, err := parseLyricText(text, splitChars)
		if err != nil {
			return nil, err
		}
		if len(parsed) == 0 {
			return nil, fmt.Errorf("解析出来 0 个词 —— 文本是 %q", text)
		}
		idx := assignLyrics(len(parsed), len(target.NoteUuids))
		lyrics := make([]string, len(idx))
		for i, k := range idx {
			lyrics[i] = parsed[k]
		}
		// ⭐ 逐音符这条路是**按 o % 词数 铺**的 ⇒ 词数与音符数不等时会循环。
		// 把"会不会循环"先说清楚（用户少写几个词时这是有意的行为，但得知道自己按了什么）。
		warn := ""
		if len(parsed) != len(target.NoteUuids) && len(parsed) != 1 {
			warn = fmt.Sprintf("。⚠️ %d 个词 → %d 个音符，按 o %% 词数 **循环铺满**",
				len(parsed), len(target.NoteUuids))
		}
		payload, err := json.Marshal(lyrics)
		if err != nil {
			return nil, err
		}
		args := []string{"note", "set-lyric"}
		for _, u := range target.NoteUuids {
			args = append(args, "--note-uuid", u)
		}
		args = append(args, "--lyrics", string(payload))
		return []Call{
			{Label: fmt.Sprintf("逐音符写第 %d 句（%d 个音符 ← %d 个词）%s%s",
				sentence, len(target.NoteUuids), len(parsed), why, warn), Args: args},
			{Label: "写后回读句子", Args: []string{"clip", "lyrics",
				"--track-index", strconv.Itoa(ref.Track), "--clip-index", strconv.Itoa(ref.Clip)}},
		}, nil
	}
	return nil, fmt.Errorf("写法认不出：%q（可写 auto / fill / perNote）", mode)
}

func onOff(b bool) string {
	if b {
		return "是"
	}
	return "否"
}

func autoSuffix(auto bool) string {
	if auto {
		return "，按语种自动判断"
	}
	return ""
}

func sentenceIndexList(ss []aceSentence) string {
	if len(ss) == 0 {
		return "（一句都没有）"
	}
	parts := make([]string, 0, len(ss))
	for _, s := range ss {
		parts = append(parts, strconv.Itoa(s.Index))
	}
	return strings.Join(parts, " · ")
}

// buildLyricGoto 定位到某一句：标记线 + 播放头都移到该句起点前 leadSec 秒（play=true 时接着播）。
//
// **试听**（用户口径）：句前 0.5 秒 → 播 → 播到句后 0.5 秒暂停。
// 这个函数只做前半段；**暂停由页面侧数 `transport state` 的 positionTick 到 endTick+leadTicks 再调停止** ——
// 不在这里 sleep 的理由：一句可能 20 秒，exe 的 `/api/run` 会被堵住（CLI 调用层是串行的），
// 而且用户中途想点别的也不行。
func buildLyricGoto(ctx context.Context, rd ReadFunc, vals map[string]string, play bool) ([]Call, error) {
	sentence, err := intValue(vals, "sentence", 0, "第几句")
	if err != nil {
		return nil, err
	}
	leadSec := 0.5
	if s := strings.TrimSpace(vals["leadSec"]); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil && v >= 0 {
			leadSec = v
		} else {
			return nil, fmt.Errorf("提前多少秒要给一个 ≥ 0 的数，收到 %q", s)
		}
	}
	ref, err := currentClip(ctx, rd)
	if err != nil {
		return nil, err
	}
	sentences, err := readSentences(ctx, rd, ref)
	if err != nil {
		return nil, err
	}
	var target *aceSentence
	for i := range sentences {
		if sentences[i].Index == sentence {
			target = &sentences[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("没有第 %d 句（这一份片段一共 %d 句：%s）",
			sentence, len(sentences), sentenceIndexList(sentences))
	}
	lead, leadNote := leadTicksAt(ctx, rd, ref.ClipBegin+target.SentenceBegin, leadSec)
	// 句起点的**绝对** tick；提前 lead 但不越过 0
	pos := ref.ClipBegin + target.SentenceBegin - lead
	if pos < 0 {
		pos = 0
	}
	at := fmt.Sprintf("%dt", pos)
	verb := "标记线移到"
	if play {
		verb = "试听：标记线移到"
	}
	calls := []Call{
		{Label: fmt.Sprintf("%s第 %d 句起点前 %d tick（tick %d）%s", verb, sentence, lead, pos, leadNoteSuffix(leadNote)),
			Args: []string{"caret", "set", "--tick", at, "--scope", "arrangement"}},
		{Label: "播放头 seek 到同一位置", Args: []string{"transport", "seek", "--time", at}},
	}
	if play {
		calls = append(calls,
			Call{Label: "开始播放（页面侧数到句尾 +0.5 秒再停）", Args: []string{"transport", "play"}},
			Call{Label: "回读播放状态（给页面当基准）", Args: []string{"transport", "state"}})
	}
	return calls, nil
}

func leadNoteSuffix(note string) string {
	if note == "" {
		return ""
	}
	return "（" + note + "）"
}
