package tools

import (
	"context"
	"fmt"
)

// 这一批移植自 AKD 那几支脚本的工具（批量替换音素 / 和声 / 参数同步 / 轨内复制），
// **开头都是同一件事**：确定"要处理哪些音符"。原脚本的统一开头是
//
//	选中了音符 ⇒ 用选中的；否则用**整个当前组**
//
// ACE 侧的对应物（都核过 help + 真机读过）：
//
//	· 当前组   → `editor status`
//	             它带 `isAvailable` ⇒ **能先判断编辑器到底开着没**，比 `editor current-clip` 好用
//	· 音符选区 → `selection get --scope editor`
//	             ⚠️ **默认 scope 是 `arrangement`，那是时间轴选区、不是音符** —— 必须显式加 `--scope editor`
//	· 音符内容 → `clip note-content --track-index T --clip-index C`
//	             ⚠️ 这个动词**按位置寻址**（不是 `--clip-uuid`），跟其它 clip 动词不一样
//	· 片段范围 → `clip list --track-index T`（拿 clipBegin/clipEnd；
//	             `note-content` 的 `pos` 是 **clip-local**，绝对位置要加 clipBegin）
//
// ⚠️ 一个实测出来的**不对称**，别互相抄：
//
//	`selection get --scope editor` 回的选中音符是 **`noteUuid`**；
//	`selection set --notes-to-select` 收的却是 **`uuid`**（`help selection set` 原文 `[{"uuid": "{...}"}]`），
//	而且 `--notes-to-deselect` **只在 `--mode modify` 下**。
type noteScope struct {
	TrackIndex int    `json:"trackIndex"`
	ClipIndex  int    `json:"clipIndex"`
	ClipUUID   string `json:"clipUuid"`
	ClipName   string `json:"clipName,omitempty"`
	ClipType   string `json:"clipType,omitempty"`
	Begin      int64  `json:"clipBegin"`
	End        int64  `json:"clipEnd"`

	Notes []xcopyNote `json:"-"`

	Which   string `json:"which"`         // 这批音符是怎么来的（人话）
	Located string `json:"clipLocatedBy"` // 片段是凭什么定位的
	All     int    `json:"clipNoteCount"` // 片段里一共有多少音符（对照"选中了多少"）
}

// noteScopeView 是"这批音符是怎么来的"那段说明 —— 结果里必须给用户看，
// 否则他分不清"我改的是选中的 3 个、还是整段 200 个"。
type noteScopeView struct {
	Which     string `json:"which"`
	Clip      string `json:"clip,omitempty"`
	ClipType  string `json:"clipType,omitempty"`
	LocatedBy string `json:"clipLocatedBy,omitempty"`
	AllNotes  int    `json:"clipNoteCount,omitempty"`
}

// readNoteScope 读齐"这次要处理的音符"。
//
// useSelection=true 时：**有音符选区就用选区**，没有就用整个片段（照原脚本）。
// ⚠️ 但"选区里有音符、我却读不出 uuid"时**必须报错**，绝不静默退化成整个片段 ——
// 那会让"只想改 3 个音符"变成"改了整段"，而且不报错。
func readNoteScope(ctx context.Context, rd ReadFunc, useSelection bool) (*noteScope, error) {
	sc := &noteScope{}

	// ① 先定 (trackIndex, clipIndex)：优先编辑器，回退到标记线
	var es struct {
		IsAvailable bool   `json:"isAvailable"`
		TrackIndex  int    `json:"trackIndex"`
		ClipIndex   int    `json:"clipIndex"`
		ClipUUID    string `json:"clipUuid"`
		ClipName    string `json:"clipName"`
		EditorType  string `json:"editorType"`
	}
	clipIdx := -1
	editorErr := error(nil)
	if err := readJSON(ctx, rd, &es, "editor", "status"); err != nil {
		editorErr = err
	} else if es.IsAvailable {
		sc.TrackIndex, clipIdx = es.TrackIndex, es.ClipIndex
		sc.ClipUUID, sc.ClipName, sc.ClipType = es.ClipUUID, es.ClipName, es.EditorType
		sc.Located = "editor status（编辑器当前那条片段）"
	}
	if clipIdx < 0 {
		// 回退：标记线所在片段。标记线是编排区的，**不依赖编辑器开着**
		// （§17.2 那条：编辑器跟着标记线走，所以两者通常指同一处）。
		var caret struct {
			TrackIndex int   `json:"trackIndex"`
			Tick       int64 `json:"tick"`
		}
		if err := readJSON(ctx, rd, &caret, "caret", "get", "--scope", "arrangement"); err != nil {
			return nil, fmt.Errorf("读标记线：%w", err)
		}
		clips, err := clipListBrief(ctx, rd, caret.TrackIndex)
		if err != nil {
			return nil, err
		}
		for _, c := range clips {
			if caret.Tick >= c.Begin && caret.Tick < c.End {
				clipIdx = c.Index
				break
			}
		}
		if clipIdx < 0 {
			why := ""
			if editorErr != nil {
				why = "（`editor status` 也读不到：" + editorErr.Error() + "）"
			}
			return nil, fmt.Errorf("定位不到要处理的片段：标记线在 tick %d，但轨道 %d 上没有片段盖住它%s。"+
				"把要处理的片段点开，或让标记线落进那个片段里再试", caret.Tick, caret.TrackIndex, why)
		}
		sc.TrackIndex = caret.TrackIndex
		sc.Located = fmt.Sprintf("回退到**标记线所在片段**（tick %d）—— 编辑器没开着", caret.Tick)
	}

	// ② 用 clip list 补齐范围/名字/类型（note-content 的 pos 是 clip-local，绝对位置要 clipBegin）
	clips, err := clipListBrief(ctx, rd, sc.TrackIndex)
	if err != nil {
		return nil, err
	}
	found := false
	for _, c := range clips {
		if c.Index == clipIdx {
			sc.ClipIndex, sc.ClipUUID, sc.ClipName, sc.ClipType = c.Index, c.UUID, c.Name, c.Type
			sc.Begin, sc.End = c.Begin, c.End
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("轨道 %d 上没有第 %d 个片段（`clip list` 只有 %d 个）",
			sc.TrackIndex, clipIdx, len(clips))
	}

	// ③ 音符
	nc, err := readClipNotes(ctx, rd, sc.TrackIndex, clipBrief{Index: sc.ClipIndex, Name: sc.ClipName})
	if err != nil {
		return nil, err
	}
	sc.Notes = nc.Notes
	sc.All = len(nc.Notes)

	// ④ 选区
	if !useSelection {
		sc.Which = fmt.Sprintf("整个片段「%s」的 %d 个音符", sc.ClipName, sc.All)
		return sc, nil
	}
	var sel struct {
		HasSelection   bool `json:"hasSelection"`
		SelectionCount int  `json:"selectionCount"`
		Notes          []struct {
			NoteUUID string `json:"noteUuid"`
			UUID     string `json:"uuid"`
		} `json:"notes"`
	}
	if err := readJSON(ctx, rd, &sel, "selection", "get", "--scope", "editor"); err != nil {
		return nil, fmt.Errorf("读音符选区（`selection get --scope editor`）：%w", err)
	}
	if !sel.HasSelection {
		sc.Which = fmt.Sprintf("没有选中音符 ⇒ 整个片段「%s」的 %d 个音符", sc.ClipName, sc.All)
		return sc, nil
	}
	want := map[string]bool{}
	for _, n := range sel.Notes {
		// `noteUuid` 是实测的那个名字；`uuid` 只作兜底（万一哪天改名了不至于全丢）
		id := n.NoteUUID
		if id == "" {
			id = n.UUID
		}
		if id != "" {
			want[id] = true
		}
	}
	if len(want) == 0 {
		return nil, fmt.Errorf("`selection get --scope editor` 说选中了 %d 个音符，但我一个 uuid 都没读出来"+
			"（字段名可能变了）—— **不猜**：猜错会静默变成「处理整个片段」。原始回包：hasSelection=%v selectionCount=%d",
			sel.SelectionCount, sel.HasSelection, sel.SelectionCount)
	}
	var picked []xcopyNote
	for _, n := range sc.Notes {
		if want[n.NoteUUID] {
			picked = append(picked, n)
		}
	}
	if len(picked) == 0 {
		return nil, fmt.Errorf("选区里的 %d 个音符，一个都不在当前片段「%s」里（%d 个音符中）—— "+
			"多半是编辑器换了片段而选区没跟上，重开一下片段再试", len(want), sc.ClipName, sc.All)
	}
	sc.Notes = picked
	sc.Which = fmt.Sprintf("**选中的 %d 个音符**（当前片段「%s」共 %d 个）", len(picked), sc.ClipName, sc.All)
	return sc, nil
}
