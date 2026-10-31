// Package tools 是「工具」的注册表。
//
// 一个工具 = 一份表单 + 一组 acestudio-cli 调用。**刻意数据驱动**：
// 前端 GET /api/tools 拿到表单 schema 自己渲染，加一个工具 = 这里加一条 + 写个 Build 函数，
// 前端一行都不用改。
//
// 📌 SV 脚本移植就落在这里：一个 SV 脚本 = 一个 Tool
//
//	· SV 的 showCustomDialog(widgets) → Tool.Params（表单）
//	· SV 的 main() 里那串 API 调用      → Tool.Build 返回的 []Call
//	· SV 的 getClientInfo().name        → Tool.Title
//	· 移植来源写进 Tool.Source，便于逐条对照（如 "复制参数.js"）
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"acetool/internal/acecli"
)

// Kind 区分只读与写入。写入路径在 M2 才启用；现在注册表里全是只读工具。
type Kind string

const (
	KindRead  Kind = "read"
	KindWrite Kind = "write"
)

// 分类顺序 = 侧栏顺序。没列到的分类排在最后。
// categoryOrder 是**工具自己的**细分类（代码/测试/文档里用这个口径）。
// 侧栏显示用的是下面的 toolGroups —— 见那里的说明。
var categoryOrder = []string{"巡检", "查找", "音符", "歌词", "音素", "参数", "轨道编辑", "轨道", "工程", "简谱"}

// toolGroups 是侧栏实际显示的**两栏**（用户 2026-10-04 的要求：
// 「把你写的工程总览等栏放一类，我说的放一类」）。
//
//	移植脚本 —— 用户点名的：SV 脚本移植（[SV2]查找.js / [SV2]歌词侧栏.js / AKD工具侧栏.js）+ 备用的语调工具
//	自建工具 —— 我先搭的骨架与路线：巡检、轨道颜色、.acep 三件套
//
// 为什么要这一层而不是去改每个工具的 Category：
//
//	· 工具自己那个细分类（"查找"/"歌词"/"音符"）在代码、测试、文档里是有意义的，不该被 UI 分组抹掉；
//	· 侧栏只想要两栏，那就**在出给 UI 的时候换掉 Category**（复制一份再改，不动注册表里的原件）。
//
// 认不出的分类**不会被吞掉** —— 它自己成一栏（用原分类名），这样将来加了新分类会露出来，
// 而不是悄悄掉进某一栏里。
var toolGroups = []struct {
	Name string
	Cats []string
}{
	{"移植脚本", []string{"查找", "歌词", "音符", "音素", "参数", "轨道编辑", "简谱"}},
	{"自建工具", []string{"巡检", "轨道", "工程", "播放"}},
}

// groupOf 把工具的细分类映射成侧栏那一栏的名字。认不出就回原分类名（自己成一栏）。
func groupOf(cat string) string {
	for _, g := range toolGroups {
		for _, c := range g.Cats {
			if c == cat {
				return g.Name
			}
		}
	}
	return cat
}

type ParamType string

const (
	ParamText   ParamType = "text"
	ParamNumber ParamType = "number"
	ParamSelect ParamType = "select"
	ParamCheck  ParamType = "check"
	// ParamTextarea 是多行文本。加它是为了**贴一整段旋律**：
	// 简谱输入动辄几十上百个记号，塞进 `<input type="text">` 里既看不见也不好改。
	// 前端无需额外改动 —— 值收集用的是 `[data-name]` + `el.value`，textarea 天然满足。
	ParamTextarea ParamType = "textarea"
)

type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Param 是一个表单字段。直接序列化给前端渲染。
type Param struct {
	Name        string    `json:"name"`
	Label       string    `json:"label"`
	Type        ParamType `json:"type"`
	Default     string    `json:"default,omitempty"`
	Placeholder string    `json:"placeholder,omitempty"`
	Hint        string    `json:"hint,omitempty"`
	Options     []Option  `json:"options,omitempty"`
	// Rows 只有 ParamTextarea 用：显示几行（前端渲染成 rows 属性）。
	Rows int `json:"rows,omitempty"`
}

// Call 是一次要发给 acestudio-cli 的调用。
// 不必自带 --json —— 调用层的 Read 会补（§3.3-2：读动词不加只有人话）。
type Call struct {
	Label string   `json:"label"`
	Args  []string `json:"args"`
	Stdin string   `json:"-"`
}

// ReadFunc 是给工具用的**只读**探针（自动带 --json）。
type ReadFunc func(ctx context.Context, args ...string) (*acecli.Result, error)

// runWrite 跑一条**写**命令，并把失败**真的当失败**。
//
// ⛔ 为什么非要有这个口子（2026-10-05 抓到的真 bug）：
// `acecli.Client.Run` 的结尾是 `return r, nil` —— **它永远不回 error**，
// 失败信息全在 `Result` 里（`ExitCode` / `Err` 信封）。
// 于是写操作写成
//
//	if _, err := rd(ctx, ...); err != nil { ... }   // ⛔ err 恒为 nil
//
// 就是**写失败被当成成功**：回滚不触发、结果里报"已写入"。
// 读操作由 `readJSON` 兜住了这一层（它查 `OK()`），写操作之前**没有任何东西兜** ——
// 所以 `track.split` 那段"中途失败就回滚"曾经是**死代码**（见 HANDOVER §23.1）。
//
// ⚠️ 另外两条与读不同的地方：
//  1. 写动词成功的回包**不一定是 JSON**（官方口径：`track set` 成功回人话
//     `Track 0 updated.`）⇒ 这里**只看 `OK()`**，绝不要求 JSON。
//  2. 进程没起来/超时走 `ExitSpawnFailed`，`Err` 信封是空的 ⇒ 那时把 `SpawnErr` 带出来，
//     否则错误信息会是一句空话。
func runWrite(ctx context.Context, rd ReadFunc, args ...string) error {
	_, err := runWriteRes(ctx, rd, args...)
	return err
}

// runWriteRes 与 runWrite 同判据，但**把回包也给出来** —— 有些写动词的回包里带结果
// （`clip create` 会回 `clipUuid` / `noteCount` / `noteUuids`），拿它**当场核对**
// 「我以为写了 N 个」与「宿主说写进去几个」，这是唯一能抓住"退出码 0 但什么都没写"的办法。
//
// 真机教训（2026-10-05）：新建片段时音符的 clip-local 起点算错，音符落到片段长度之外，
// `clip create` **退出码 0**、片段名还照歌词起了，可里面**一个音符都没有**。
// 干跑看不出来，`OK()` 也看不出来 —— 只有核对回包里的 `noteCount` 才抓得住。
func runWriteRes(ctx context.Context, rd ReadFunc, args ...string) (*acecli.Result, error) {
	res, err := rd(ctx, args...)
	if err != nil {
		return nil, err
	}
	if !res.OK() {
		detail := res.Err.String()
		if detail == "" {
			detail = res.SpawnErr
		}
		if detail == "" {
			detail = strings.TrimSpace(res.Stderr)
		}
		return res, fmt.Errorf("`%s` 失败：exit %d %s", strings.Join(args, " "), res.ExitCode, detail)
	}
	return res, nil
}

// readJSON 跑一条只读命令并解成 v。
// 失败时把 ACE 的错误信封**原样**带出来 —— 不要退化成"读失败"，
// 那样用户看不到 error.code 与 hint（纪律 §10-6）。
func readJSON(ctx context.Context, rd ReadFunc, v any, args ...string) error {
	res, err := rd(ctx, args...)
	if err != nil {
		return err
	}
	cmd := strings.Join(args, " ")
	if !res.OK() {
		return fmt.Errorf("`%s` 失败：exit %d %s", cmd, res.ExitCode, res.Err.String())
	}
	if res.JSON == nil {
		return fmt.Errorf("`%s` 的 stdout 不是 JSON（读动词应当回 JSON）", cmd)
	}
	if err := json.Unmarshal(res.JSON, v); err != nil {
		return fmt.Errorf("解析 `%s` 的输出失败：%w", cmd, err)
	}
	return nil
}

// Tool 是一个可在面板上执行的工具。
type Tool struct {
	ID       string  `json:"id"`
	Category string  `json:"category"`
	Title    string  `json:"title"`
	Desc     string  `json:"desc"`
	Kind     Kind    `json:"kind"`
	Params   []Param `json:"params,omitempty"`
	Source   string  `json:"source,omitempty"` // 移植来源（SV 脚本文件名），便于对照

	// Build 按表单值算出要跑的调用。一条工具可以是多次 CLI 调用（结果按 Label 分组展示）。
	//
	// rd 是**只读**探针（已自动带 --json）——「读 → 决定 → 写」是 SV 脚本的主导形状
	// （见参考脚本 ace-color-cycle.ps1 的 -Mode next：先读当前颜色定位调色板，再写下一格），
	// 所以这里给工具一个读口子；**写口子一律走返回的 []Call**，受干跑闸门管。
	Build func(ctx context.Context, rd ReadFunc, vals map[string]string) ([]Call, error) `json:"-"`

	// Inspect 是**进程内**的只读实现（不经过 CLI 的**写**，但可以读 —— rd 就是给它读的）。
	// 文件路线用（`.acep` 不是 CLI 的能力）；「把整个工程读一遍再本地过滤」的查询也用它。
	// 与 Build 二选一（两个都给了会被测试卡住）；写工具**不许**有 Inspect。
	Inspect func(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) `json:"-"`

	// Plan / Apply 是**进程内的写入实现**（文件路线）。区别在于 Apply 会在磁盘上产生副作用。
	// 干跑闸门对它们有约束力：runner 在 dryRun 时**只调 Plan**，Apply 一次都不碰。
	// 不变量（测试卡住）：两者**成对**出现，且此时 Kind 必须是 write。
	Plan  func(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) `json:"-"`
	Apply func(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) `json:"-"`

	// NoUndoMark 关掉「撤回标记」：exe 在写工具跑前后各读一次 ACE 撤销栈（各 ~80ms），
	// 好让面板知道"刚才这一批"压了几条、能一次退回去（见 internal/tools/history.go）。
	//
	// 两处要关：
	//   · `track.color` —— RGB 循环里一秒好几条，两次额外读会把轮转拖慢；而且"撤回一条变色"
	//     对用户没意义（他自己就看得见颜色变回去了）。
	//   · 只写文件、**不碰 ACE 撤销栈**的工具（`lyric.lrcFile`）—— 标了也没有可退的条目。
	NoUndoMark bool `json:"-"`
}

var registry []*Tool

func register(t *Tool) { registry = append(registry, t) }

// Catalog 是给前端的整份清单。
type Catalog struct {
	Categories []string `json:"categories"`
	Tools      []*Tool  `json:"tools"`
}

// CatalogForUI 返回给**侧栏**用的清单。
//
// 与注册表的两处差别：
//  1. `Category` 换成了 `toolGroups` 里那一栏的名字（两栏：移植脚本 / 自建工具）——
//     **改的是副本**，注册表里那份细分类不动，`Get()` 拿到的还是原值；
//  2. 排序 = 先按栏的顺序，栏内再按**原分类顺序**（这样 查找/歌词/音符 仍然挨在一起），
//     最后按注册顺序 —— 于是同一栏里不会看起来乱跳。
func CatalogForUI() Catalog {
	catOrder := make(map[string]int, len(categoryOrder))
	for i, c := range categoryOrder {
		catOrder[c] = i
	}
	groupOrder := make(map[string]int, len(toolGroups))
	for i, g := range toolGroups {
		groupOrder[g.Name] = i
	}
	rank := func(t *Tool) (int, int) {
		g, ok := groupOrder[groupOf(t.Category)]
		if !ok {
			g = len(toolGroups) // 认不出的分类排最后，自成栏
		}
		c, ok := catOrder[t.Category]
		if !ok {
			c = len(categoryOrder)
		}
		return g, c
	}

	sorted := append([]*Tool(nil), registry...)
	sort.SliceStable(sorted, func(i, j int) bool {
		gi, ci := rank(sorted[i])
		gj, cj := rank(sorted[j])
		if gi != gj {
			return gi < gj
		}
		return ci < cj
	})

	// 复制一份再改 Category —— 绝不能改到注册表里那个共享的 *Tool
	out := make([]*Tool, 0, len(sorted))
	var cats []string
	seen := map[string]bool{}
	for _, t := range sorted {
		cp := *t
		cp.Category = groupOf(t.Category)
		out = append(out, &cp)
		if !seen[cp.Category] {
			seen[cp.Category] = true
			cats = append(cats, cp.Category)
		}
	}
	return Catalog{Categories: cats, Tools: out}
}

// Get 按 id 取工具，取不到返回 nil。
func Get(id string) *Tool {
	for _, t := range registry {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// Summary 是启动横幅用的**一行工具概况**（由注册表现算）。
//
// ⚠️ 为什么要现算：横幅原来把这行**写死**在 main.go 里（"9 个工具"），
// 加了工具之后**两次变成谎话**都没人发现。写死的计数一定会漂。
// 这里按**侧栏那两栏**报（与用户看到的侧栏一致）。
func Summary() string {
	cat := CatalogForUI()
	byCat := map[string]int{}
	for _, t := range cat.Tools {
		byCat[t.Category]++
	}
	parts := make([]string, 0, len(cat.Categories))
	for _, c := range cat.Categories {
		parts = append(parts, fmt.Sprintf("%s %d", c, byCat[c]))
	}
	r, w := 0, 0
	for _, t := range cat.Tools {
		if t.Kind == KindWrite {
			w++
		} else {
			r++
		}
	}
	return fmt.Sprintf("%d 个工具（读 %d / 写 %d）：%s", len(cat.Tools), r, w, strings.Join(parts, " · "))
}

// Writable 报告当前注册表里有没有写工具（M1 阶段应当为 false）。
func Writable() bool {
	for _, t := range registry {
		if t.Kind == KindWrite {
			return true
		}
	}
	return false
}

// ---------- 表单值读取小工具（供各工具的 Build 使用）----------

// value 取字段值，空串回落到默认值。
func value(vals map[string]string, name, def string) string {
	if s, ok := vals[name]; ok {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return def
}

// intValue 取整数字段。ACE 的索引参数一律 0 起，负数直接拒（别让它回 INVALID_ARG）。
func intValue(vals map[string]string, name string, def int, label string) (int, error) {
	s := strings.TrimSpace(vals[name])
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%s 必须是整数，收到 %q", label, s)
	}
	if n < 0 {
		return 0, fmt.Errorf("%s 不能为负（索引 0 起）", label)
	}
	return n, nil
}

// truthy 判定表单里的"勾上了"（checkParam 传回来的就是 "1"/"0"）。
// 口径与 api.go 的 isTruthy 一致：肯定式收宽（含中文），**其余一律当假**。
func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "on", "yes", "y", "是", "有", "开", "真":
		return true
	}
	return false
}

// boolValue 判一个勾选框，**空值回落到默认**。
// 为什么需要它：`checkParam` 的默认值在**页面提交表单**时才会带过来；
// 手搓的表单（或脚本直接打 `/api/run`）不带这个键时，`truthy("")` 会一律当假 ——
// 于是"默认勾上"的开关会静默变成"默认不勾"（`ignoreTenuto` 就是这种默认开的开关）。
func boolValue(s string, def bool) bool {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return truthy(s)
}

// ---------- 声明式表单字段构造 ----------

func numParam(name, label, def string) Param {
	return Param{Name: name, Label: label, Type: ParamNumber, Default: def}
}

func textParam(name, label, def, placeholder, hint string) Param {
	return Param{Name: name, Label: label, Type: ParamText, Default: def, Placeholder: placeholder, Hint: hint}
}

// areaParam 是多行文本框（贴一整段旋律用）。rows 省略按 8 行。
func areaParam(name, label, def string, rows int, placeholder, hint string) Param {
	if rows <= 0 {
		rows = 8
	}
	return Param{Name: name, Label: label, Type: ParamTextarea, Default: def, Rows: rows,
		Placeholder: placeholder, Hint: hint}
}

func checkParam(name, label string, def bool) Param {
	d := "0"
	if def {
		d = "1"
	}
	return Param{Name: name, Label: label, Type: ParamCheck, Default: d}
}
