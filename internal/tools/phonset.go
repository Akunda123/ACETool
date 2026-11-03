package tools

// 单个音符写音素（P3 最小版）：面板改下拉后点「应用」，只写这一个音符。
//
// 纪律（交接文档 §2.4/§3）：
//   · `phoneme set` 一次只收**一个**音符；失败不假装原子（前面成功的改不回来，逐条报）；
//   · 写之前先 `phoneme list` 读现状，拿 `isOverride`，告诉用户这次会把发音**冻结**成 override；
//   · 符号必须在该音符语言的 inventory 里 —— 先校验，别让 ACE 去拒；
//   · 全部音素清空 ⇒ 用 `phoneme reset-override`（把音符交还引擎推导）。

import (
	"context"
	"fmt"
	"strings"
)

type phonSetReport struct {
	NoteUUID string   `json:"noteUuid"`
	Lyric    string   `json:"lyric,omitempty"`
	Before   []string `json:"before"`
	After    []string `json:"after"`
	WasOvr   bool     `json:"wasOverride"`
	WillFree bool     `json:"willFreeze"`
	Did      bool     `json:"written,omitempty"`
	Reset    bool     `json:"reset,omitempty"`
	DryRun   bool     `json:"dryRun,omitempty"`
	Note     string   `json:"note,omitempty"`
}

func init() {
	register(&Tool{
		ID: "phoneme.set", Category: "音素", Kind: KindWrite,
		Title: "设置一个音符的音素",
		Desc: "把一个音符的音素替换成给定的符号序列（可一次清空 = 恢复默认）。" +
			"写前会读音素现状并校验符号是否在该语言的 inventory 里。",
		Params: []Param{
			textParam("noteUuid", "音符 UUID", "", "{…}", "从 phoneme.view 的结果里拿"),
			textParam("phonemes", "新音素（空格分隔；留空 = 恢复默认）", "", "m ing", "例：m ing"),
		},
		Plan:   phonSetPlan,
		Apply:  phonSetApply,
		Source: "交接文档 docs/音素替换-移植交接.md §3/§5（P3 最小版）",
	})
}

type phonSetCtx struct {
	rep  *phonSetReport
	args []string
}

func phonSetAnalyze(ctx context.Context, rd ReadFunc, vals map[string]string) (*phonSetCtx, error) {
	uuid := strings.TrimSpace(value(vals, "noteUuid", ""))
	if uuid == "" {
		return nil, fmt.Errorf("要一个音符 UUID（noteUuid）")
	}
	want := strings.Fields(value(vals, "phonemes", ""))

	var doc struct {
		Notes []struct {
			NoteUUID        string   `json:"noteUuid"`
			Phonemes        []string `json:"phonemes"`
			DefaultPhonemes []string `json:"defaultPhonemes"`
			IsOverride      bool     `json:"isOverride"`
			Language        string   `json:"language"`
		} `json:"notes"`
	}
	if err := readJSON(ctx, rd, &doc, "phoneme", "list", "--note", uuid); err != nil {
		return nil, fmt.Errorf("读音素现状失败：%w", err)
	}
	if len(doc.Notes) == 0 {
		return nil, fmt.Errorf("phoneme list 里没有这个音符（%s）", uuid)
	}
	cur := doc.Notes[0]

	rep := &phonSetReport{
		NoteUUID: uuid, Before: cur.Phonemes, After: want,
		WasOvr: cur.IsOverride, Reset: len(want) == 0,
	}
	// 符号校验：只查该音符自己的语言
	if len(want) > 0 {
		full := map[string]string{"CHN": "Chinese", "JPN": "Japanese", "ENG": "English", "SPA": "Spanish", "KOR": "Korean"}[strings.ToUpper(cur.Language)]
		if full == "" {
			full = "Chinese"
		}
		inv, err := phonInventory(ctx, rd, full)
		if err != nil {
			return nil, err
		}
		var bad []string
		for _, s := range want {
			if !inv[s] {
				bad = append(bad, s)
			}
		}
		if len(bad) > 0 {
			return nil, fmt.Errorf("这些符号不在 %s 的合法音素里：%v", full, bad)
		}
	}
	rep.WillFree = len(want) > 0 && !cur.IsOverride
	if rep.WillFree {
		rep.Note = "写下去会把发音固定成 override（以后改歌词不再影响这个音符的发音）"
	}
	if len(want) == 0 {
		rep.Note = "清空音素 = 恢复默认发音（`phoneme reset-override`）"
	}
	return &phonSetCtx{rep: rep, args: want}, nil
}

func phonSetPlan(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	c, err := phonSetAnalyze(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	c.rep.DryRun = true
	return c.rep, nil
}

func phonSetApply(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	c, err := phonSetAnalyze(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	rep := c.rep
	if len(c.args) == 0 {
		if err := runWrite(ctx, rd, "phoneme", "reset-override", "--note", rep.NoteUUID); err != nil {
			return rep, fmt.Errorf("恢复默认失败：%w", err)
		}
	} else {
		args := append([]string{"phoneme", "set", "--note", rep.NoteUUID, "--phoneme"}, c.args...)
		if err := runWrite(ctx, rd, args...); err != nil {
			return rep, fmt.Errorf("写音素失败：%w", err)
		}
	}
	rep.Did = true
	return rep, nil
}
