package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// 移植 `批量替换音素.js`（AKD「音素-批量替换音素」，88 行）。
//
// 原脚本：取选中音符（没选中就整组）→ 读 `SV.getPhonemesForGroup()` → 弹窗问"查找/替换" →
// 对**音素串里含查找词**的音符做 `replaceAll(串, 查找, 替换)` → `setPhonemes`。
//
// ⛔ 移植时改了一处**语义**（是改进，理由写在下面）：
//
//	原脚本是在**拼好的音素字符串上做子串替换**（`"m ing".replace("a", ...)` 之类）。
//	ACE 的 `phoneme list` 回的是**符号数组**（`["m","ing"]`）⇒ 这里做**按符号（token）替换**。
//	为什么必须改：子串替换会把 `a` 打进 `ian`/`an`/`ang` 里面，产出一个**没打算要的音素串**，
//	而 ACE 对音素是"要么合法要么直接拒"（不 clamp、不近似）——
//	子串替换要么被拒、要么写出一个用户没预期的发音。**按 token 替换才是"把这个音素换成那个音素"的原意。**
//
// 另外两条 ACE 侧的现实：
//
//	· `phoneme set` **一次只收一个音符**（官方：`--note` "Exactly one —— a symbol list is one
//	  grapheme's pronunciation, so a run is a call per note"）⇒ N 个音符 N 次调用；
//	· 符号必须属于**该音符自己的语言** ⇒ 干跑时先用 `phoneme inventory --language <L>`
//	  **预校验替换词**（这是原脚本没有的一步：原脚本让 SV 自己去拒，我们能在干跑里就告诉你）。
//
// 单位/范围与 §23 那批一致：`pos` 是 **clip-local**。
// （"这批音符是怎么来的"那个 `noteScopeView` 是这一组共用的，定义在 `scope.go`。）

type phonChangeView struct {
	NoteUUID string   `json:"noteUuid"`
	Pos      int64    `json:"pos"` // clip-local
	Pitch    int      `json:"pitch"`
	Lyric    string   `json:"lyric,omitempty"`
	Before   []string `json:"before"`
	After    []string `json:"after"`
	// 这个音符是**人为改写过**的音素（override）还是引擎按歌词推出来的。
	// ⚠️ 两者写下去**含义不同**：对没改写过的音符写音素 = 把发音**冻结**成 override。
	WasOverride bool `json:"wasOverride"`
}

type phonReplaceReport struct {
	Scope noteScopeView `json:"scope"`

	Find    string `json:"find"`
	Replace string `json:"replace"`

	Scanned   int `json:"scanned"`
	Hit       int `json:"hit"`        // 含查找符号的音符数
	WillWrite int `json:"willSet"`    // 真正会写的音符数（替换后在语言里合法、且真的会变）
	Frozen    int `json:"willFreeze"` // 其中"本来没被改写、这次会被冻结成 override"的个数

	Changes []phonChangeView `json:"changes,omitempty"`
	Skips   []string         `json:"skipped,omitempty"`

	// 干跑时给"打算发的命令"；真写时给"已经做了的事"。
	Calls  []string `json:"plannedCalls,omitempty"`
	Did    []string `json:"done,omitempty"`
	DryRun bool     `json:"dryRun,omitempty"`
	Millis int64    `json:"millis,omitempty"`
	Note   string   `json:"note,omitempty"`
}

func init() {
	register(&Tool{
		ID: "phoneme.replace", Category: "音素",
		Title: "批量替换音素",
		Desc: "移植 `批量替换音素.js`：把一批音符音素里的**某个音素符号换成另一个**。" +
			"范围 = **选中的音符**（没有选中就是**整个当前片段**），照原脚本。" +
			"⚠️ 与原脚本**不同的地方**：它是在音素**字符串上做子串替换**，这里是按**符号（token）**替换 —— " +
			"子串替换会把 `a` 打进 `ian`/`an`/`ang` 里，ACE 对这种要么直接拒、要么写出你没预期的发音。" +
			"⚠️ 替换词会先按**该音符自己的语言**预校验（`phoneme inventory`），干跑就能告诉你能不能写。" +
			"⚠️ `phoneme set` 一次只收一个音符 ⇒ 会发 N 条命令；对**本来没被改写**的音符写入等于把发音**冻结**成 override（结果里分开计数）。" +
			"**干跑默认**：勾着只出计划 + 逐条对照，一个字节都不写。",
		Kind:   KindWrite,
		Source: "AKD `批量替换音素.js`",
		Params: []Param{
			textParam("find", "查找音素", "", "ing / a / vn", "**一个音素符号**（按符号整体匹配，不是子串）"),
			textParam("replace", "替换为", "", "in /（留空=删掉这个符号）", "留空表示把这个符号删掉；不可用空列表清空全部"),
			{Name: "scope", Label: "范围", Type: ParamSelect, Default: "auto",
				Options: []Option{
					{Value: "auto", Label: "选中的音符（没选中就整个片段）"},
					{Value: "clip", Label: "整个当前片段"},
				}},
		},
		Plan:  phonReplacePlan,
		Apply: phonReplaceApply,
	})
}

// phonInventory 取某个语言的合法符号集（辅音 ∪ 元音）。**只读**，用来在干跑里预校验。
func phonInventory(ctx context.Context, rd ReadFunc, language string) (map[string]bool, error) {
	var inv struct {
		Language   string   `json:"language"`
		Consonants []string `json:"consonants"`
		Vowels     []string `json:"vowels"`
	}
	if err := readJSON(ctx, rd, &inv, "phoneme", "inventory", "--language", language); err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, s := range inv.Consonants {
		out[s] = true
	}
	for _, s := range inv.Vowels {
		out[s] = true
	}
	return out, nil
}

// replaceToken 把符号列表里**等于** find 的项换成 repl；repl 为空则删掉。
// 返回新列表与"改了几处"。
func replaceToken(syms []string, find, repl string) ([]string, int) {
	out := make([]string, 0, len(syms))
	n := 0
	for _, s := range syms {
		if s == find {
			n++
			if repl != "" {
				out = append(out, repl)
			}
			continue
		}
		out = append(out, s)
	}
	return out, n
}

type phonPlanData struct {
	rep     *phonReplaceReport
	scope   *noteScope
	changes []phonChangeView
	find    string
	repl    string
	started int64
}

func analyzePhonReplace(ctx context.Context, rd ReadFunc, vals map[string]string) (*phonPlanData, error) {
	find := strings.TrimSpace(value(vals, "find", ""))
	if find == "" {
		return nil, fmt.Errorf("要给一个**查找音素**（如 `ing`）—— 空的没法替换")
	}
	if strings.ContainsAny(find, " \t") {
		return nil, fmt.Errorf("查找词 %q 里有空格：这里是**一个音素符号**，不是一串（要换多个请分几次）", find)
	}
	repl := strings.TrimSpace(value(vals, "replace", ""))
	if repl == find {
		return nil, fmt.Errorf("查找词和替换词一样（%q），没什么可做的", find)
	}
	useSel := value(vals, "scope", "auto") != "clip"

	sc, err := readNoteScope(ctx, rd, useSel)
	if err != nil {
		return nil, err
	}
	if len(sc.Notes) == 0 {
		return nil, fmt.Errorf("片段「%s」里没有音符", sc.ClipName)
	}

	rep := &phonReplaceReport{
		Scope: noteScopeView{Which: sc.Which, Clip: sc.ClipName, ClipType: sc.ClipType,
			LocatedBy: sc.Located, AllNotes: sc.All},
		Find: find, Replace: repl, Scanned: len(sc.Notes),
	}

	// 读音素（一次调用可以点名很多音符，但**必须同属一个片段** —— 这里本来就是）
	var uuids []string
	for _, n := range sc.Notes {
		uuids = append(uuids, n.NoteUUID)
	}
	ph, err := readClipPhonemes(ctx, rd, uuids, "当前片段「"+sc.ClipName+"」")
	if err != nil {
		return nil, err
	}

	// 按语言预校验替换词（每个语言查一次，缓存）
	invCache := map[string]map[string]bool{}
	invErr := map[string]error{}
	invOf := func(lang string) (map[string]bool, error) {
		if v, ok := invCache[lang]; ok {
			return v, invErr[lang]
		}
		v, e := phonInventory(ctx, rd, lang)
		invCache[lang], invErr[lang] = v, e
		return v, e
	}

	pd := &phonPlanData{rep: rep, scope: sc, find: find, repl: repl}

	for _, n := range sc.Notes {
		p, ok := ph[n.NoteUUID]
		if !ok || len(p.Phonemes) == 0 {
			rep.Skips = append(rep.Skips, fmt.Sprintf("「%s」@%d 读不到音素，跳过", n.NoteUUID, n.Pos))
			continue
		}
		after, hits := replaceToken(p.Phonemes, find, repl)
		if hits == 0 {
			continue // 不含查找符号：原脚本也是静默跳过（不刷屏）
		}
		rep.Hit++

		// 替换词必须属于这个音符自己的语言（ACE 会拒；我们提前说）
		if repl != "" {
			inv, e := invOf(p.Language)
			if e != nil {
				return nil, fmt.Errorf("查 %s 的音素清单（`phoneme inventory`）：%w", p.Language, e)
			}
			if !inv[repl] {
				rep.Skips = append(rep.Skips, fmt.Sprintf(
					"「%s」@%d 跳过：替换词 %q **不在 %s 的音素清单里**（写下去 ACE 会拒 INVALID_ARG）",
					n.NoteUUID, n.Pos, repl, p.Language))
				continue
			}
		}
		if len(after) == 0 {
			// `phoneme set` 明确拒空列表（"An empty list is refused"）
			rep.Skips = append(rep.Skips, fmt.Sprintf(
				"「%s」@%d 跳过：替换后音素列表会**空掉**，而 `phoneme set` 拒空列表（要清回推导音素请用 `phoneme reset-override`）",
				n.NoteUUID, n.Pos))
			continue
		}
		if sameSymbols(after, p.Phonemes) {
			rep.Skips = append(rep.Skips, fmt.Sprintf("「%s」@%d 跳过：替换后和原来一样", n.NoteUUID, n.Pos))
			continue
		}

		rep.WillWrite++
		if !p.IsOverride {
			rep.Frozen++
		}
		cv := phonChangeView{NoteUUID: n.NoteUUID, Pos: n.Pos, Pitch: n.Pitch, Lyric: n.Lyric,
			Before: p.Phonemes, After: after, WasOverride: p.IsOverride}
		rep.Changes = append(rep.Changes, cv)
		pd.changes = append(pd.changes, cv)
	}

	if rep.WillWrite > 0 {
		rep.Calls = append(rep.Calls, fmt.Sprintf("（%d 条 `phoneme set`，每个音符一条 —— ACE 的硬约束）", rep.WillWrite))
	}
	return pd, nil
}

func phonReplaceNote(pd *phonPlanData, dry bool) string {
	rep := pd.rep
	var b strings.Builder
	fmt.Fprintf(&b, "%s 扫了 %d 个音符（%s）。", map[bool]string{true: "干跑：一个字节都没写。", false: "已写入。"}[dry], rep.Scanned, rep.Scope.Which)
	fmt.Fprintf(&b, "含 %q 的 **%d** 个 ⇒ 会写 **%d** 个。", rep.Find, rep.Hit, rep.WillWrite)
	if rep.Frozen > 0 {
		fmt.Fprintf(&b, " ⚠️ 其中 **%d** 个本来**没被改写过**，写下去会把发音**冻结**成 override（以后改歌词不再影响它的发音）。", rep.Frozen)
	}
	if len(rep.Skips) > 0 {
		fmt.Fprintf(&b, " %d 条跳过，原因逐条在下。", len(rep.Skips))
	}
	return b.String()
}

func phonReplacePlan(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	pd, err := analyzePhonReplace(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	pd.rep.DryRun = true
	pd.rep.Note = phonReplaceNote(pd, true)
	return pd.rep, nil
}

func phonReplaceApply(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	pd, err := analyzePhonReplace(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	rep := pd.rep
	var done []string
	for _, c := range pd.changes {
		args := append([]string{"phoneme", "set", "--note", c.NoteUUID, "--phoneme"}, c.After...)
		if err := runWrite(ctx, rd, args...); err != nil {
			// ⛔ 不假装原子：ACE 每个动词独立撤销记录，前面成功的改不回来
			rep.Did = done
			rep.Note = fmt.Sprintf("在音符 %s 这一步失败，**前面已经落的改动没有回滚**（ACE 每个动词是独立撤销记录，Ctrl+Z 可逐步退回）。", c.NoteUUID)
			return rep, nil
		}
		done = append(done, fmt.Sprintf("%s：%s → %s", c.NoteUUID, strings.Join(c.Before, " "), strings.Join(c.After, " ")))
	}
	rep.Did = done
	rep.Note = phonReplaceNote(pd, false)
	return rep, nil
}

// 让变化列表按位置排一下（结果里好读）。
func sortPhonChanges(c []phonChangeView) {
	sort.SliceStable(c, func(i, j int) bool { return c[i].Pos < c[j].Pos })
}
