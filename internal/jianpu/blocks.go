package jianpu

import (
	"math"
	"strings"
)

// majorOffsets 是大调音级 1..7 到半音的偏移（简谱里 `1` 就是主音）。
var majorOffsets = []int{0, 2, 4, 5, 7, 9, 11}

// naturalPitchClass 是音名到音级（C=0）。
var naturalPitchClass = map[rune]int{'C': 0, 'D': 2, 'E': 4, 'F': 5, 'G': 7, 'A': 9, 'B': 11}

// ModeToPitchClass 把 `D:` 的值解析成音级 0..11（C=0）。
//
// 上游测试要求这四种写法都收：`#C` `$B` `C#` `B$`
// ⇒ 变音记号 `#`（升）/`$`（降）**前后都认**，音名字母只认大写
// （`D: h` 必须是**非法**的，所以小写字母一律不认）。
func ModeToPitchClass(mode string) (int, bool) {
	s := strings.TrimSpace(mode)
	if s == "" {
		return 0, false
	}
	base, delta, seen := 0, 0, false
	for _, r := range s {
		switch r {
		case '#':
			delta++
		case '$':
			delta--
		default:
			pc, ok := naturalPitchClass[r]
			if !ok || seen {
				return 0, false
			}
			base, seen = pc, true
		}
	}
	if !seen {
		return 0, false
	}
	return ((base+delta)%12 + 12) % 12, true
}

// NoteBlock 是从谱面派生的一个音符块（音高/时值/歌词），给 ACE 之类"按音符吃"的宿主用。
type NoteBlock struct {
	Voice        int        `json:"voice"`
	Caption      string     `json:"caption,omitempty"`
	Degree       int        `json:"degree"` // 简谱音级 1..7；0=休止；9=节奏音
	Octave       int        `json:"octave"`
	Accidental   Accidental `json:"accidental,omitempty"`
	Midi         int        `json:"midi"`  // <0 = 算不出来（休止/节奏音/隐藏音/调式未知）
	Beats        float64    `json:"beats"` // 以四分音符为 1 拍；**延音 `-` 已折进来**
	Ticks        int64      `json:"ticks"` // Beats × 480（ACE/常见 PPQ 480）
	Lyric        string     `json:"lyric,omitempty"`
	Hidden       bool       `json:"hidden,omitempty"`
	Ornaments    []string   `json:"ornaments,omitempty"`
	Measure      int        `json:"measure"` // 第几小节（1 起）
	Beat         float64    `json:"beat"`    // 小节内第几拍（1 起）
	ElementIndex int        `json:"elementIndex"`
	Code         string     `json:"code"`
}

// BlockOptions 是 Blocks 的参数。
type BlockOptions struct {
	TonicPitchClass int  // 主音音级 0..11（C=0）；<0 时从 Metadata.Mode 推
	TonicFromMode   bool // 仅文档用，不实现
	Voice           int  // 只要某个声部；0 = 全部
	BaseMidi        int  // 简谱「无八度点」的 1 对应哪个 MIDI；0 = 默认 60（C4）
}

// beatsOf 算一个元素的拍数：`4/Duration × (2 − 2^-Dots)`。
//
//	四分(4).0 ⇒ 1 拍 · 八分(8).0 ⇒ 0.5 拍 · 四分(4).1 ⇒ 1.5 拍 · 十六分(16).. ⇒ 0.4375 拍
func beatsOf(el Element) float64 {
	d := el.Duration
	if d <= 0 {
		d = 4 // 没写时值 ⇒ 按四分音符
	}
	return 4.0 / float64(d) * (2 - 1/math.Pow(2, float64(el.Dots)))
}

// Blocks 把谱面派生成音符块（按声部、按出现顺序；延音折进前一个音符）。
//
// 口径：
//   - 一个 note + **紧跟的连续 `-`** 合成一个块（每个 `-` 加它自己的拍数，裸 `-` 就是 1 拍）；
//   - `Midi = BaseMidi + Tonic + 大调音级偏移[degree-1] + 12×Octave`，升降号再 ±1；
//     **休止（0）/ 节奏音（9）/ 隐藏音（8）给 -1**（它们没有确定的音高）；
//   - 小节号按元素里的 barline 递增，**只有 `normal` 与 `end` 算小节结束**（照约定）；
//   - 歌词取该声部**第一条**歌词行，按**块序号**对齐（不是元素序号 —— 延音不占块）。
//
// ⚠️ 上游明确说过："音节数 == 音符数"**不是**不变量（真谱子里 `Q3: 1, - - - |` 配
// `C3: 呜@呜` 就只有 2 个音节）。所以这里**只按序号取，取不到就留空**，不报错、不补位。
func Blocks(doc *Document, opt BlockOptions) []NoteBlock {
	if doc == nil {
		return nil
	}
	tonic := opt.TonicPitchClass
	if tonic < 0 {
		if pc, ok := ModeToPitchClass(doc.Metadata.Mode); ok {
			tonic = pc
		} else {
			tonic = 0 // 调式未知 ⇒ 按 C 算（不报错，Midi 仍然给得出来）
		}
	}
	base := opt.BaseMidi
	if base == 0 {
		base = 60 // C4
	}

	var out []NoteBlock
	for _, page := range doc.Pages {
		for _, group := range page.Groups {
			for _, v := range group.Voices {
				if opt.Voice > 0 && v.Voice != opt.Voice {
					continue
				}
				out = append(out, blocksOfVoice(v, base, tonic)...)
			}
		}
	}
	return out
}

func blocksOfVoice(v Voice, base, tonic int) []NoteBlock {
	var out []NoteBlock
	var syllables []Syllable
	if len(v.Lyrics) > 0 {
		syllables = v.Lyrics[0].Syllables
	}

	measure := 1
	beatInMeasure := 1.0
	// lastBlock 是"延音要折进去的那个块"在 out 里的下标（-1 = 还没有）
	lastBlock := -1

	for idx, el := range v.Elements {
		switch el.Kind {
		case KindBarline:
			// 只有 normal / end 算小节结束（照约定的口径）
			if el.Type == BarNormal || el.Type == BarEnd || el.Type == "" {
				measure++
				beatInMeasure = 1
			}
			continue
		case KindSustain:
			beats := beatsOf(el)
			if lastBlock >= 0 {
				out[lastBlock].Beats += beats
				out[lastBlock].Ticks = int64(math.Round(out[lastBlock].Beats * 480))
			}
			beatInMeasure += beats
			continue
		}

		beats := beatsOf(el)
		blk := NoteBlock{
			Voice:        v.Voice,
			Caption:      v.Caption,
			Degree:       el.Pitch,
			Octave:       el.Octave,
			Accidental:   el.Accidental,
			Midi:         midiOf(el, base, tonic),
			Beats:        beats,
			Ticks:        int64(math.Round(beats * 480)),
			Hidden:       el.Hidden,
			Measure:      measure,
			Beat:         beatInMeasure,
			ElementIndex: idx,
			Code:         el.Code,
		}
		for _, o := range el.Ornaments {
			blk.Ornaments = append(blk.Ornaments, o.Name)
		}
		if n := len(out); n < len(syllables) {
			// ⚠️ 只取 Text：**尾部标点不塞进歌词**（`你，` ⇒ 歌词 `你`，
			// 标点留在文档模型的 `TrailingPunctuation` 里给调用方自己决定）。
			blk.Lyric = syllables[n].Text
		}
		out = append(out, blk)
		lastBlock = len(out) - 1
		beatInMeasure += beats
	}
	return out
}

// midiOf 把简谱音级换算成 MIDI 音高；算不出来的给 -1。
func midiOf(el Element, base, tonic int) int {
	if el.Rest || el.Hidden {
		return -1
	}
	if el.Sound == "rhythm" {
		return -1
	}
	d := el.Pitch
	if d < 1 || d > 7 {
		return -1
	}
	m := base + tonic + majorOffsets[d-1] + 12*el.Octave
	switch el.Accidental {
	case AccidentalSharp:
		m++
	case AccidentalFlat:
		m--
	}
	return m
}
