package tools

// 音素面板（只读）：把当前选中/整段音符的「歌词 + 实际音素」列出来，
// 每个音素附带 IPA、官方四分类、按 IPA 推出的发音方式与例词（internal/phoneme）。
//
// 为什么是只读：这是 P1。写入（逐音符 phoneme set + override 冻结提示）按交接文档 §5 放 P3。

import (
	"context"
	"fmt"
	"strings"

	"acetool/internal/phoneme"
)

type phonTokenView struct {
	Symbol     string   `json:"symbol"`
	Candidates []string `json:"candidates,omitempty"` // 同语言的合法符号，同「发音方式」的排前面
	IPA      string `json:"ipa,omitempty"`
	Official string `json:"official,omitempty"`
	Manner   string `json:"manner,omitempty"`
	Example  string `json:"example,omitempty"`
}

type phonNoteView struct {
	NoteUUID string         `json:"noteUuid"`
	Pos      int64          `json:"pos"`
	DurBlick int64          `json:"dur"`
	Pitch    int            `json:"pitch"`
	Lyric    string         `json:"lyric"`
	Language string         `json:"language"`
	Tokens   []phonTokenView `json:"tokens"`
	Defaults []phonTokenView `json:"defaults,omitempty"`
	Override bool           `json:"isOverride"`
	Timings  []string       `json:"timingKinds,omitempty"`
}

type phonViewReport struct {
	Scope   noteScopeView  `json:"scope"`
	Clip    string         `json:"clip,omitempty"`
	Lang    string         `json:"language,omitempty"`
	Symbols map[string][]string `json:"symbols,omitempty"` // 语言 → 合法符号（面板做下拉用）
	Count   int            `json:"count"`
	Notes   []phonNoteView `json:"notes"`
	Missing []string       `json:"missing,omitempty"`
	Note    string         `json:"note,omitempty"`
}

func init() {
	register(&Tool{
		ID: "phoneme.view", Category: "音素", Kind: KindRead,
		Title: "音素预览（只读）",
		Desc: "列出当前选中（没有选中就整段）音符的**歌词与实际音素**，每个音素给出 IPA、分类、" +
			"发音方式与例词。纯读，不写工程、不进撤销栈。",
		Params: []Param{},
		Inspect: phonViewPlan,
		Source: "交接文档 docs/音素替换-移植交接.md §5 P1；数据 internal/phoneme",
	})
}

func phonViewPlan(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	sc, err := readNoteScope(ctx, rd, true)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "no tracks") || strings.Contains(msg, "no clip") || strings.Contains(msg, "holds no") {
			return &phonViewReport{Note: "当前工程没有可读的轨道/片段（是不是空工程？先在 ACE 里打开一个带 Sing 片段的工程）"}, nil
		}
		return nil, err
	}
	rep := &phonViewReport{
		Scope: noteScopeView{Which: sc.Which, Clip: sc.ClipName, ClipType: sc.ClipType,
			LocatedBy: sc.Located, AllNotes: sc.All},
		Clip: sc.ClipName,
	}
	if sc.ClipUUID == "" {
		return nil, fmt.Errorf("拿不到片段 UUID（%s）", sc.Located)
	}

	// ① 音素：整段读一次，按 noteUuid 索引
	var doc struct {
		Notes []struct {
			NoteUUID        string   `json:"noteUuid"`
			Phonemes        []string `json:"phonemes"`
			DefaultPhonemes []string `json:"defaultPhonemes"`
			IsOverride      bool     `json:"isOverride"`
			TimingKinds     []string `json:"timingKinds"`
			Language        string   `json:"language"`
		} `json:"notes"`
	}
	if err := readJSON(ctx, rd, &doc, "phoneme", "list", "--clip", sc.ClipUUID,
		"--range-begin", fmt.Sprintf("%dt", sc.Begin)); err != nil {
		return nil, fmt.Errorf("读音素失败（phoneme list）：%w", err)
	}
	type phonRow struct {
		toks     []string
		defaults []string
		override bool
		timings  []string
		language string
	}
	rows := map[string]phonRow{}
	langSeen := map[string]int{}
	for _, n := range doc.Notes {
		rows[n.NoteUUID] = phonRow{n.Phonemes, n.DefaultPhonemes, n.IsOverride, n.TimingKinds, n.Language}
		langSeen[n.Language]++
	}

	// ② 每种语言取一次 IPA 提示（音素 → IPA）；取不到就不带 IPA，不阻塞
	hints := map[string]map[string]string{}
	symbols := map[string][]string{}
	langFull := map[string]string{"CHN": "Chinese", "JPN": "Japanese", "ENG": "English", "SPA": "Spanish", "KOR": "Korean"}
	for code := range langSeen {
		full := langFull[strings.ToUpper(code)]
		if full == "" {
			continue
		}
		var inv struct {
			PhoneticsHints map[string]string `json:"phoneticsHints"`
			Consonants     []string          `json:"consonants"`
			Vowels         []string          `json:"vowels"`
		}
		if err := readJSON(ctx, rd, &inv, "phoneme", "inventory", "--language", full, "--json"); err == nil {
			hints[strings.ToUpper(code)] = inv.PhoneticsHints
			symbols[strings.ToUpper(code)] = append(append([]string{}, inv.Consonants...), inv.Vowels...)
		}
	}

	langKey := map[string]string{"CHN": "zh", "JPN": "jp", "ENG": "eng", "SPA": "spa"}
	describe := func(code string, syms []string) []phonTokenView {
		up := strings.ToUpper(code)
		lk := langKey[up]
		out := make([]phonTokenView, 0, len(syms))
		for _, s := range syms {
			e := phoneme.Describe(lk, s, hints[up][s])
			out = append(out, phonTokenView{Symbol: e.Symbol, Candidates: rankCandidates(lk, symbols[up], hints[up], e.Manner, e.Symbol)})
		}
		return out
	}

	for _, n := range sc.Notes {
		row, ok := rows[n.NoteUUID]
		if !ok {
			rep.Missing = append(rep.Missing, n.NoteUUID)
			continue
		}
		code := n.Language
		if code == "" {
			code = row.language
		}
		rep.Notes = append(rep.Notes, phonNoteView{
			NoteUUID: n.NoteUUID, Pos: n.Pos, DurBlick: n.Dur, Pitch: n.Pitch,
			Lyric: n.Lyric, Language: code,
			Tokens:   describe(code, row.toks),
			Defaults: describe(code, row.defaults),
			Override: row.override,
			Timings:  row.timings,
		})
	}
	rep.Symbols = symbols
	rep.Count = len(rep.Notes)
	if rep.Count == 0 {
		rep.Note = "这批音符里没有读到音素（可能不是 Sing 片段）"
	}
	if len(rep.Missing) > 0 {
		rep.Note = fmt.Sprintf("%d 个音符在 phoneme list 里没对应项（已跳过）", len(rep.Missing))
	}
	return rep, nil
}

// rankCandidates 给下拉框排候选：同「发音方式」的排前面，其余按原顺序接在后面。
// 全部合法符号都保留 —— 用户可能就是要挑一个"不同类"的。
func rankCandidates(lang string, all []string, hints map[string]string, curManner, curSym string) []string {
	same := make([]string, 0, len(all))
	other := make([]string, 0, len(all))
	for _, sym := range all {
		e := phoneme.Describe(lang, sym, hints[sym])
		if e.Manner != "" && e.Manner == curManner && sym != curSym {
			same = append(same, sym)
		} else if sym != curSym {
			other = append(other, sym)
		}
	}
	return append(append([]string{curSym}, same...), other...)
}
