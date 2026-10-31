package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// 「汉字 → 语调」—— **备用**工具：把一个字变成它的拼音、声调、调值与音高走向。
//
// 为什么是纯本地的：实测 ACE **没有语调面**（`help --search intonation` 零匹配；
// `phoneme g2p` 只给音素、不给声调，且中文 `contextSupported: false` 不做上下文消歧）。
// 所以声调只能本地查表 —— 表由 `tools/genpinyin.js` 从 **pinyin-pro** 生成
// （与 SV 侧技能用的是同一个库，口径一致）。
//
// 三个来源：直接给文本 / 从当前片段取歌词 / 从选中的音符取。
// 后两个会顺带把**音符位置与音高走向**带上 —— 那正是将来要判"倒字"时缺的东西。
//
// ⚠️ **参考级**（照本仓纪律，别让用户以为它是权威）：
//
//	· 多音字给的是**数据源的首选**（`toneMode=library`）或**歌词常用读法**（`toneMode=lyric`，
//	  一张**人工小表**，只收证据清楚的虚字）；
//	· 要精确到词（"银行"不是"yín xíng"）需要词表 —— **不做**，请用 `overrides` 手动指定；
//	· `phoneme g2p` 给的是 **ACE 会唱的音素**，那是权威的（它就算 ACE 的口径），
//	  但它同样不含声调。
func init() {
	register(&Tool{
		ID: "lyric.tones", Category: "歌词",
		Title: "汉字 → 语调",
		Desc: "把一个字变成它的**拼音 / 声调 / 调值 / 音高走向**，备用。" +
			"声调口径统一成 **1 阴平 · 2 阳平 · 3 上声 · 4 去声 · 5 轻声**（与 SV 的 rapAccent 一致）。" +
			"来源可以是直接给的文本、**当前片段的歌词**、或**选中的音符** —— 后两者会顺带带上" +
			"音符位置与音高走向（将来判「倒字」就缺这个）。" +
			"⚠️ 纯本地查表，**参考级**：多音字只给首选或常用读法，**不做按词消歧**" +
			"（要精确请用 `overrides`，例如 `行=hang`）。" +
			"⚠️ 勾「附带 ACE 音素」会多一次 `phoneme g2p`，那部分是 ACE 的权威口径（它不含声调）。",
		Kind:   KindRead,
		Source: "本地拼音表（pinyin-pro 生成）+ 可选 ACE phoneme g2p",
		Params: []Param{
			textParam("text", "文本", "", "燕鸟栖下林", "留空则按「来源」从工程取"),
			{Name: "source", Label: "来源", Type: ParamSelect, Default: "text",
				Options: []Option{
					{Value: "text", Label: "用上面的文本"},
					{Value: "clip", Label: "当前片段的歌词（整个片段）"},
					{Value: "selection", Label: "选中的音符"},
				}},
			{Name: "toneMode", Label: "多音字取哪个读音", Type: ParamSelect, Default: "library",
				Options: []Option{
					{Value: "library", Label: "数据源首选（可复现，有据可查）"},
					{Value: "lyric", Label: "歌词常用读法（人工小表：了→le 地→de 得→de…）"},
				}},
			textParam("overrides", "手动指定读音", "", "行=hang 地=di", "空格分隔的 字=读音，优先级最高"),
			checkParam("withPhonemes", "附带 ACE 音素（多一次调用）", false),
		},
		Inspect: func(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
			return runTones(ctx, rd, vals)
		},
	})
}

// ToneChar 是一个字的结果。字段名必须 ASCII（中文标识符算未导出，序列化会变 `{}`）。
type ToneChar struct {
	Index     int      `json:"index"`
	Char      string   `json:"char"`
	Pinyin    string   `json:"pinyin"` // 带调号
	Plain     string   `json:"plain"`  // 不带调号
	Tone      int      `json:"tone"`   // 1..5（5 = 轻声）
	ToneName  string   `json:"toneName"`
	ToneValue string   `json:"toneValue"` // 调值（55 / 35 / 214 / 51）
	Contour   string   `json:"contour"`   // 音高走向（— / ↗ / ↘↗ / ↘ / ·）
	All       []string `json:"allReadings,omitempty"`
	Polyphone bool     `json:"polyphonic,omitempty"`

	// 来源是工程时才有
	NoteUUID  string `json:"noteUuid,omitempty"`
	NoteIndex int    `json:"noteIndex,omitempty"`
	Pos       int64  `json:"pos,omitempty"`
	BarBeat   string `json:"barBeat,omitempty"`
	Pitch     int    `json:"pitch,omitempty"`
	MelodyDir string `json:"melodyDir,omitempty"` // 与**前一个音符**的音高比：↑ / ↓ / = / （第一个为空）

	// 勾了「附带 ACE 音素」才有
	ACESung []string `json:"acePhonemes,omitempty"`

	Note string `json:"note,omitempty"` // 「歌词常用读法」「非汉字」之类
}

type toneResult struct {
	Source     string     `json:"source"`
	ToneMode   string     `json:"toneMode"`
	Text       string     `json:"text"`
	Count      int        `json:"count"`
	Summary    string     `json:"summary"` // 燕4 鸟3 栖1 下4 林2
	Polyphones int        `json:"polyphones"`
	NonHanzi   int        `json:"nonHanzi"`
	Overridden int        `json:"overridden"`
	LyricReads int        `json:"lyricReadings"` // 用了歌词常用读法的字数
	ExtraCalls int        `json:"extraCalls"`
	Millis     int64      `json:"millis"`
	Chars      []ToneChar `json:"chars"`
	Note       string     `json:"note,omitempty"`
}

func runTones(ctx context.Context, rd ReadFunc, vals map[string]string) (*toneResult, error) {
	t0 := time.Now()
	source := value(vals, "source", "text")
	mode := value(vals, "toneMode", "library")
	overrides, err := parseOverrides(vals["overrides"])
	if err != nil {
		return nil, err
	}
	res := &toneResult{Source: source, ToneMode: mode, Chars: []ToneChar{}}

	// ① 收集要处理的"字"（可能同时带上音符信息）
	type unit struct {
		ch   rune
		note ToneChar
	}
	var units []unit
	switch source {
	case "text":
		txt := vals["text"]
		if strings.TrimSpace(txt) == "" {
			return nil, fmt.Errorf("「文本」是空的 —— 写几个汉字，或把「来源」改成「当前片段的歌词 / 选中的音符」")
		}
		res.Text = txt
		for _, r := range txt {
			units = append(units, unit{ch: r})
		}

	case "clip", "selection":
		ref, err := currentClip(ctx, rd)
		if err != nil {
			return nil, err
		}
		var sel map[string]bool
		if source == "selection" {
			sel, _ = readSelectedNoteUUIDs(ctx, rd)
			if len(sel) == 0 {
				return nil, fmt.Errorf("「选中的音符」是空的 —— 先在 ACE 里选几个音符，或把「来源」改成「当前片段的歌词」")
			}
		}
		var clip struct {
			Notes []struct {
				Lyric    string `json:"lyric"`
				NoteUUID string `json:"noteUuid"`
				Pos      int64  `json:"pos"`
				Pitch    int    `json:"pitch"`
			} `json:"notes"`
		}
		if err := readJSON(ctx, rd, &clip, "clip", "note-content",
			"--track-index", strconv.Itoa(ref.Track), "--clip-index", strconv.Itoa(ref.Clip)); err != nil {
			return nil, fmt.Errorf("读片段歌词失败：%w", err)
		}
		sigs := readSignatures(ctx, rd)
		var text strings.Builder
		prevPitch := -1
		prevSet := false
		for i, n := range clip.Notes {
			if source == "selection" && !sel[n.NoteUUID] {
				continue
			}
			lyric := strings.TrimSpace(n.Lyric)
			if lyric == "" || lyric == "-" {
				continue // 延音/空歌词不算字
			}
			dir := ""
			if prevSet {
				switch {
				case n.Pitch > prevPitch:
					dir = "↑"
				case n.Pitch < prevPitch:
					dir = "↓"
				default:
					dir = "="
				}
			}
			base := ToneChar{NoteUUID: n.NoteUUID, NoteIndex: i,
				Pos: ref.ClipBegin + n.Pos, BarBeat: barBeat(ref.ClipBegin+n.Pos, sigs),
				Pitch: n.Pitch, MelodyDir: dir}
			for _, r := range lyric {
				u := unit{ch: r, note: base}
				units = append(units, u)
				text.WriteRune(r)
			}
			prevPitch, prevSet = n.Pitch, true
		}
		res.Text = text.String()
		if len(units) == 0 {
			return nil, fmt.Errorf("这一份片段里没有可分析的歌词（全是空或 `-` 延音）")
		}

	default:
		return nil, fmt.Errorf("来源认不出：%q（可写 text / clip / selection）", source)
	}

	// ② 逐字查表
	for i, u := range units {
		tc := u.note
		tc.Index = i
		tc.Char = string(u.ch)
		if !isHanzi(u.ch) {
			// 非汉字：如实标出来，**不当成没看见**（英文歌词、标点都会走到这）
			if unicode.IsSpace(u.ch) {
				continue
			}
			tc.Note = "非汉字（本地拼音表不管）"
			res.NonHanzi++
			res.Chars = append(res.Chars, tc)
			continue
		}
		all := readingsOf(u.ch)
		if len(all) == 0 {
			tc.Note = "表里没有这个字的读音"
			res.Chars = append(res.Chars, tc)
			continue
		}
		tc.All = all
		tc.Polyphone = len(all) > 1
		if tc.Polyphone {
			res.Polyphones++
		}
		// 选读音：overrides > lyricMode > 数据源首选
		chosen := all[0]
		if ov, ok := overrides[u.ch]; ok {
			// 手动指定的可以是不带调号的写法（hang / háng 都收）
			chosen = normalizeOverride(ov, all)
			tc.Note = "手动指定"
			res.Overridden++
		} else if mode == "lyric" {
			if lr, ok := lyricReading[u.ch]; ok {
				chosen = normalizeOverride(lr, all)
				tc.Note = "歌词常用读法"
				res.LyricReads++
			}
		}
		tc.Pinyin = chosen
		tc.Plain, _ = unmarkTone(chosen)
		tc.Tone = toneOf(chosen)
		ti := toneInfoOf(tc.Tone)
		tc.ToneName, tc.ToneValue = ti.Name, ti.Value
		tc.Contour = contourArrow(tc.Tone) // 走向的**唯一**事实源
		res.Chars = append(res.Chars, tc)
	}

	// ③ 可选的 ACE 音素（**一次调用**拿全部 —— 官方说整串与单字"costs the same derivation"）
	if truthy(vals["withPhonemes"]) {
		hanzis := make([]string, 0, len(res.Chars))
		for _, c := range res.Chars {
			if isHanzi([]rune(c.Char)[0]) {
				hanzis = append(hanzis, c.Char)
			}
		}
		if len(hanzis) > 0 {
			ph, err := readG2P(ctx, rd, hanzis)
			if err != nil {
				res.Note = strings.TrimSpace(res.Note + " 读 ACE 音素失败：" + err.Error())
			} else {
				res.ExtraCalls++
				k := 0
				for i := range res.Chars {
					if !isHanzi([]rune(res.Chars[i].Char)[0]) {
						continue
					}
					if k < len(ph) {
						res.Chars[i].ACESung = ph[k]
					}
					k++
				}
			}
		}
	}

	res.Count = len(res.Chars)
	res.Summary = toneSummary(res.Chars)
	res.Millis = time.Since(t0).Milliseconds()
	if res.NonHanzi > 0 {
		res.Note = strings.TrimSpace(res.Note + fmt.Sprintf(" 有 %d 个非汉字被跳过或如实标出。", res.NonHanzi))
	}
	if res.Polyphones > 0 && mode == "library" && res.Overridden == 0 {
		res.Note = strings.TrimSpace(res.Note + fmt.Sprintf(" 有 %d 个多音字，取的是**数据源首选**；"+
			"要改读音试 `toneMode=lyric` 或 `overrides`（如 `行=hang`）。", res.Polyphones))
	}
	return res, nil
}

// toneSummary 拼一行最紧凑的形态：`燕4 鸟3 栖1 下4 林2`（数字就是 1..5 的声调号）。
func toneSummary(chars []ToneChar) string {
	parts := make([]string, 0, len(chars))
	for _, c := range chars {
		if c.Tone == 0 {
			parts = append(parts, c.Char)
			continue
		}
		parts = append(parts, fmt.Sprintf("%s%d", c.Char, c.Tone))
	}
	return strings.Join(parts, " ")
}

// parseOverrides 解析 `行=hang 地=di` 这种手动指定。
//
// ⛔ 分隔符要按**字符**切，不能 `IndexAny` 拿到字节下标再 `+1` —— 全角 `＝` 占 3 个字节，
// `tok[eq+1:]` 会切在它中间，值直接变乱码（用例抓过这条：`行＝hang` → 值 `\ufffd\ufffdhang`）。
func parseOverrides(s string) (map[rune]string, error) {
	out := map[rune]string{}
	for _, tok := range strings.Fields(s) {
		if tok == "" {
			continue
		}
		sep, sepLen := -1, 0
		for i, r := range tok {
			if r == '=' || r == '＝' || r == ':' || r == '：' {
				sep, sepLen = i, len(string(r))
				break
			}
		}
		if sep <= 0 || sep+sepLen >= len(tok) {
			return nil, fmt.Errorf("手动指定要写成 `字=读音`（如 `行=hang`），这一段认不出：%q", tok)
		}
		key := []rune(strings.TrimSpace(tok[:sep]))
		if len(key) != 1 {
			return nil, fmt.Errorf("`字=读音` 左边要**一个**汉字，这一段是 %q", tok[:sep])
		}
		out[key[0]] = strings.TrimSpace(tok[sep+sepLen:])
	}
	return out, nil
}

// normalizeOverride 把手动/人工表给的读音对齐到表里的写法。
//
// 顺序（这个顺序踩过坑）：
//  1. **先找完全相等的候选** —— 因为 `de` 是「得」的一个真切音（轻声），而 `dé` 去掉调号**也是** `de`。
//     先前先比"去调号后相等"，于是 `de` 匹到了 `dé`，把轻声变成了阳平（用例补出来后修掉）。
//  2. 带调号的写法直接用（`háng` 就是用户想要的那个）。
//  3. 不带调号的，再按"去调号后相等"匹配（`hang` → `háng`）；同分的**优先没调号**的那个。
//  4. 都匹不上就原样返回 —— 让用户看见自己写了什么，而不是被悄悄换掉。
func normalizeOverride(want string, all []string) string {
	want = strings.TrimSpace(want)
	if want == "" {
		return all[0]
	}
	for _, a := range all {
		if a == want {
			return a
		}
	}
	if hasMark(want) {
		return want
	}
	for _, a := range all {
		if plain, _ := unmarkTone(a); plain == want && !hasMark(a) {
			return a
		}
	}
	for _, a := range all {
		if plain, _ := unmarkTone(a); plain == want {
			return a
		}
	}
	return want
}

func hasMark(s string) bool {
	for _, r := range s {
		if _, ok := plainVowel[r]; ok {
			return true
		}
	}
	return false
}

// readG2P 一次拿整串的 ACE 音素（`--grapheme` 可以跟多个值）。
// 官方口径：整串与单字"costs the same derivation"，所以**不必逐字调**。
func readG2P(ctx context.Context, rd ReadFunc, graphemes []string) ([][]string, error) {
	args := []string{"phoneme", "g2p", "--language", "Chinese", "--grapheme"}
	args = append(args, graphemes...)
	var doc struct {
		Syllables []struct {
			Index    int      `json:"index"`
			Grapheme string   `json:"grapheme"`
			Phonemes []string `json:"phonemes"`
			Resolved bool     `json:"resolved"`
		} `json:"syllables"`
	}
	if err := readJSON(ctx, rd, &doc, args...); err != nil {
		return nil, err
	}
	out := make([][]string, len(doc.Syllables))
	for i, s := range doc.Syllables {
		idx := s.Index
		if idx < 0 || idx >= len(out) {
			idx = i
		}
		out[idx] = s.Phonemes
	}
	return out, nil
}
