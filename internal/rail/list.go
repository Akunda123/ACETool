// Package rail 是 **exe 侧那条贴在 ACE 窗口边上的汉字细条**（HANDOVER §69 方案 B 的「侧边模式」）。
//
// 用户 2026-10-15 定的口径（原话）：侧栏做成"鼠标滑过展开、离开收起"；**不用图标，改用 1–2 个汉字**；
// 贴的是 **ACE 的窗口**（不是屏幕），ACE 有两个顶层窗口（主窗 + 钢琴窗）⇒ **焦点是哪个就贴哪个**；
// ACE 关了就隐藏。清单 = home 那排**全部**条目（19 项），条目显示「简称 + 全称」。
//
// 颜色口径（用户 2026-10-16：「参数和不带参可以用颜色标识」）—— 与 home 那三类按钮**同一套语义**：
//
// `t🟩 KindRun    在侧栏里就地完成（点一下就执行；带 `Panel` 的就地弹参数条）
// `t🟨 KindParams 要参数、而且侧栏塞不下 ⇒ 点了开主面板去填（现在清单里没有这种）
//
//	🟦 KindPage   打开某个功能窗口（只读/视图/生成类）
//	🟥 KindFile   **改工程文件**（.acep 文件路线）—— 放最后一组、同一色
//
// ⚠️ 色值**不在这个包**：细条的 GDI 颜色在 `rail_windows.go` 顶部，**逐字抄** `internal/web/assets/style.css`
// 的 `:root`（用户 2026-10-17：「颜色按照web统一」）。web 那份是单一事实源。
//
// ⚠️ 条目**集合** = home 那排全部 19 项（`check-rail.js` 会对账"19 项 / 三类齐 / 各自带上该带的字段"），
// 但**顺序是细条自己的**（用户 2026-10-17：「把删除挪到前面去」）—— 细条按常用度排，不再镜像 home。
package rail

import (
	"sort"

	"acetool/internal/layout"
)

// 条目类型（见包注释的三色口径）。
const (
	KindRun    = "run"    // 绿：在侧栏里就地完成（点一下就执行；带 `Panel` 的就地弹参数条）
	KindParams = "params" // 琥珀：要参数，而且**侧栏塞不下** ⇒ 点了开主面板去填（现在清单里没有这种）
	KindPage   = "page"   // 蓝：打开某个功能窗口（只读/视图/生成类）
	KindFile   = "file"   // 红：**改工程文件**（`.acep` 文件路线）—— 见 `Items()` 底部那一组
)

// 参数条（`Panel.Kind`）的两种形态。
const (
	ParamTier   = "tier"   // 一档一档的滑块（连接音符的「缝 ≤ 阈值」）
	ParamFields = "fields" // 若干文本框 + 一个执行按钮（批量替换音素的查找/替换）
)

// TierOption 是滑块上的一档：`Value` 发给工具，`Name` 给人看。
type TierOption struct {
	Value string // 如 "480"（tick，480 = 1 拍）
	Name  string // 如 "四分音符"
}

// Field 是参数条上的一个文本框。
type Field struct {
	Name  string // 参数名（如 find / replace）
	Label string // 画在输入框左边
	Width int    // 逻辑像素
}

// Panel 让一条「要参数」的项**在细条上就地编辑**，不再绕到主面板去填。
//
// ⚠️ 它只能是一个**临时小窗**（不是像细条那样挂在 ACE 窗口里）：输文本必须有键盘焦点，
// 而焦点按线程/活动窗口分 —— 子窗口自己不能被激活（激活的永远是它所在的顶层窗口 = ACE，
// 那是**别的进程**）⇒ 我们的输入框永远收不到键盘。完整理由见 `panel_windows.go` 文件头。
type Panel struct {
	Kind   string       // ParamTier / ParamFields
	Tiers  []TierOption // Kind = tier
	Arg    string       // Kind = tier：选中的档位发给哪个参数名
	Fields []Field      // Kind = fields
	// NeedsInput = 这一项的输入**必填**（如音素的「查找」）⇒ 左键不直接跑，而是**打开参数条**。
	// 没有这一条的（连接音符那种档位）⇒ 左键直接用记着的值跑。
	NeedsInput bool
	RunText    string // 执行按钮上的字（「连接」/「替换」）
	Hint       string // 参数条上的一句说明（画在标题下面）
}

// Item 是细条上的一格。
type Item struct {
	Short  string            // 细条上画的（1–2 个汉字；用户口径）
	Full   string            // 全称（展开时画；与 home 的 label 一致）
	Kind   string            // KindRun / KindParams / KindPage / KindFile —— 给用户看的颜色语义
	Act    string            // 真动作（ActTool/ActClipCopy/ActClipPaste），空 = ActTool
	Page   string            // KindPage / KindFile：面板页文件名（如 `find.html`）
	Tool   string            // KindRun：工具 id（走 `/api/run`）
	Values map[string]string // KindRun：固定参数（如 `{"mark":"-"}`）
	Panel  *Panel            // KindParams：非 nil ⇒ 点它在细条上**就地编辑参数**（不开主面板）
	// Key 是**稳定 id**（设置页「按钮顺序与显示」用它，用户 2026-10-20）：
	// 侧栏这一项与主面板同名按钮共用同一个 key，顺序/隐藏才能跨两边同步。
	// 留空时按 `page:<Page>` / `tool:<Tool>` / `act:<Act>` 自动推（见 KeyOf）。
	Key string
	// DefaultHidden=true 的项**默认不出现在侧栏**，要在设置页勾上才显示（用户 2026-10-20）。
	DefaultHidden bool
}

// KeyOf 给一项算出稳定 id（`Key` 优先，否则按页面/工具/动作推）。
// ⚠️ 主页面的按钮要用**同一套规则**算 key（`page:<文件名>` / `tool:<工具 id>`），不然两边同步不上。
func (it Item) KeyOf() string {
	if it.Key != "" {
		return it.Key
	}
	if it.Page != "" {
		return "page:" + it.Page
	}
	if it.Tool != "" {
		return "tool:" + it.Tool
	}
	if it.Act != "" {
		return "act:" + it.Act
	}
	return ""
}

// 真动作（只有 `Kind=run` 的条目用得上；空 = 老路子，直接跑 `Tool/Values`）。
//
// 为什么单独一个字段而不是再加一种 `Kind`：**`Kind` 是给用户看的颜色语义**
// （用户 2026-10-16 定：绿=点一下就执行 / 琥珀=要参数 / 蓝=开窗口），而"要过一次剪贴板"
// 是**实现细节** —— 复制/粘贴歌词在用户眼里就是"点一下就执行"（🟩），不该为它多出一种颜色。
const (
	ActTool      = ""          // 默认：跑 `Tool/Values`（走 `/api/run`）
	ActClipCopy  = "clipCopy"  // 复制歌词：先跑只读工具拿文本，再写进**系统剪贴板**（exe 自己写）
	ActClipPaste = "clipPaste" // 粘贴歌词：先**读系统剪贴板**，再把文本当参数跑工具
	// ActExpandWeb = 展开为 web 页：开主面板的 web 窗口 + **停掉细条**（与顶栏「侧」互为镜像）。
	ActExpandWeb = "expandWeb"
)

// Action 返回这条的真动作（没写就是 ActTool）。
func (it Item) Action() string {
	if it.Act == "" {
		return ActTool
	}
	return it.Act
}

// Items 返回细条当前该画的条目：**先按设置页那份「按钮顺序与显示」过滤/重排**（用户 2026-10-20）。
//
// ⛔ `hidden` 只作用于渲染 —— 被隐藏的项快捷键照跑、工具照用（用户重申过）。
// 配置由 `SetLayout` 在启动时与保存时各喂一次（不在这里读文件：`Items()` 被 120ms 的枚举循环调用）。
// railPref 是细条的默认顺序（用户 2026-10-24）：
//   · 歌词修改那几个挨一起；· 和声跟页面类放一起。
var railPref = []string{
	"删除歌词并前移", "合并歌词并前移", "添加 - 歌词", "复制歌词", "粘贴歌词",
	"选择播放头之后/之前/当前的音符",
	"连接音符", "播放头位置分割轨道", "批量替换音素",
	"RGB 变色", "填词", "LRC 导出", "跨轨复制", "轨内复制", "参数映射",
	"跨轨道复制音高线", "语调教", "复制颤音模板", "轨道内复制音高线",
	"查找", "谱面编辑器", "生成和声（面板）", "生成二维码",
	"设置", "展开为 web 页",
}

// sortByPref 按上面的偏好顺序**稳定**排序（没列到的保持原相对顺序、排在后面）。
func sortByPref(items []Item) []Item {
	rank := make(map[string]int, len(railPref))
	for i, n := range railPref {
		rank[n] = i
	}
	out := make([]Item, len(items))
	copy(out, items)
	sort.SliceStable(out, func(i, j int) bool {
		ra, oka := rank[out[i].Full]
		rb, okb := rank[out[j].Full]
		if !oka && !okb {
			return false
		}
		if !oka {
			return false
		}
		if !okb {
			return true
		}
		return ra < rb
	})
	return out
}

func Items() []Item {
	all := sortByPref(allItems())
	// ⚠️ 不能只判 `layoutHidden`：默认隐藏的项（二维码）不在 hidden 里，
	// 要靠 EffectiveHidden 把「defaultHidden − shown」算进来
	// （2026-10-20 用户实测：侧栏没藏住，因为这里提前 return 了）。
	eff := layout.EffectiveHidden(layoutHidden, layoutShown, defaultHiddenKeys())
	if len(layoutOrder) == 0 && len(eff) == 0 {
		return all
	}
	byKey := make(map[string]Item, len(all))
	keys := make([]string, 0, len(all))
	for _, it := range all {
		k := it.KeyOf()
		byKey[k] = it
		keys = append(keys, k)
	}
	out := make([]Item, 0, len(keys))
	for _, k := range layout.Arrange(keys, layoutOrder, eff) {
		if it, ok := byKey[k]; ok {
			out = append(out, it)
		}
	}
	return out
}

// layoutOrder / layoutHidden 是当前生效的那份配置（空 = 内置顺序、全显示）。
var layoutOrder, layoutHidden, layoutShown []string

// SetLayout 喂一份新配置（启动时从 config 读；设置页保存后立刻再喂一次）。
func SetLayout(order, hidden, shown []string) {
	layoutOrder, layoutHidden, layoutShown = layout.Clean(order), layout.Clean(hidden), layout.Clean(shown)
}

// Entry 是给设置页用的"一个可排序/可隐藏的按钮"（key + 给人看的名字）。
type Entry struct {
	Key           string `json:"key"`
	Name          string `json:"name"`
	DefaultHidden bool   `json:"defaultHidden,omitempty"`
}

// LayoutEntries 返回**出厂清单**的 (key, 名字)（含隐藏项，设置页要能把它勾回来）。
func LayoutEntries() []Entry {
	all := allItems()
	out := make([]Entry, 0, len(all))
	for _, it := range all {
		out = append(out, Entry{Key: it.KeyOf(), Name: it.Full, DefaultHidden: it.DefaultHidden})
	}
	return out
}

// LayoutKeys 返回**当前清单的稳定 id**（设置页拿它校验"配置里没有的按钮"，也用来画那张表）。
func LayoutKeys() []string {
	all := allItems()
	out := make([]string, 0, len(all))
	for _, it := range all {
		out = append(out, it.KeyOf())
	}
	return out
}

// allItems 是**内置**的那份清单（顺序 = 出厂顺序）。
func allItems() []Item {
	return []Item{
		// ⭐ 用户 2026-10-21：「合并的做一个按钮，删的做一个」——两颗分开（同一个工具，靠 mode 区分）。
		// 两颗 key 必须不同（KeyOf 默认按 tool 推，会撞）⇒ 显式给 Key。
		{Short: "删词", Full: "删除歌词并前移", Kind: KindRun, Tool: "lyric.remove",
			Values: map[string]string{"mode": "delete"}, Key: "tool:lyric.remove.delete"},
		{Short: "合并", Full: "合并歌词并前移", Kind: KindRun, Tool: "lyric.remove",
			Values: map[string]string{"mode": "merge"}, Key: "tool:lyric.remove.merge"},
		// ⭐ 用户 2026-10-17：「把删除挪到前面去」⇒ 「删除并前移」从**最后一行**挪到**第一行**。
		// 它是最常用的歌词操作之一，原来在最底下要滑到底才点得到。
		// ⚠️ 这一挪**只动细条**：home 那排按钮的顺序没动（细条是"常用优先"，不是 home 的镜像）。

		// ⭐ 「连接音符」：用户 2026-10-17「我想做成点按钮就用，参数只是附带」
		// ⇒ **左键 = 立刻用当前档位跑**（绿 = 点一下就执行）；阈值退成**附属参数** ⇒ **右键**才弹参数条。
		// 档位与主面板 `home.html` 的 `TIERS` 同一套（480 tick = 1 拍）；默认第 0 档 = 不设限（与工具默认一致）。
		{Short: "和声", Full: "生成和声（面板）", Kind: KindPage, Page: "harmony.html"},
		{Short: "连音", Full: "连接音符", Kind: KindRun, Tool: "note.join", Panel: &Panel{
			Kind: ParamTier, Arg: "maxGap", RunText: "连接",
			Hint: "缝 ≤ 阈值才接（更大的当气口留下）；左键直接连，右键调这个阈值",
			Tiers: []TierOption{
				{"0", "不设限"},
				{"1920", "全音符 4拍"},
				{"960", "二分 2拍"},
				{"480", "四分 1拍"},
				{"240", "八分 ½拍"},
				{"120", "十六分 ¼拍"},
				{"60", "三十二分"},
				{"30", "六十四分"},
				{"15", "一百二十八分"},
			},
		}},
		{Short: "选择", Full: "选择播放头之后/之前/当前的音符", Kind: KindRun, Tool: "select.playhead",
			Panel: &Panel{
				Kind: ParamTier, Arg: "side", RunText: "选择",
				Hint: "左键直接选（默认之后）；右键调方向：之前 / 当前 / 之后",
				Tiers: []TierOption{
					{"after", "之后"},
					{"before", "之前"},
					{"at", "当前"},
				},
			}},
		{Short: "拆轨", Full: "播放头位置分割轨道", Kind: KindRun, Tool: "track.split"},
		// 复制/粘贴歌词：面板里是"读一次/写一次剪贴板"，**细条没有网页**（原生窗口）⇒
		// exe 自己读写系统剪贴板（`internal/win/clipboard_windows.go`），这样它在细条上**也是点一下就完事**。
		{Short: "复词", Full: "复制歌词", Kind: KindRun, Act: ActClipCopy, Tool: "lyric.copy"},
		{Short: "贴词", Full: "粘贴歌词", Kind: KindRun, Act: ActClipPaste, Tool: "lyric.paste"},
		{Short: "延词", Full: "添加 - 歌词", Kind: KindRun, Tool: "lyric.shift",
			Values: map[string]string{"mark": "-"}},
		// 音素：**绿**（用户 2026-10-17「音素也算绿色」）—— 它的参数就在侧栏里弹的那条上填，
		// 不需要去别处拿参数；但「查找」是必填的，所以左键是**打开参数条**（`Panel.NeedsInput`），
		// 不是直接跑。范围固定 = 选中的音符（没选中就整个片段），所以只要两个框。
		{Short: "音素", Full: "批量替换音素", Kind: KindRun, Tool: "phoneme.replace",
			Values: map[string]string{"scope": "auto"},
			Panel: &Panel{
				Kind: ParamFields, RunText: "替换", NeedsInput: true,
				Hint: "按音素符号整体替换（不是子串）；替换留空 = 删掉",
				Fields: []Field{
					{Name: "find", Label: "查找", Width: 96},
					{Name: "replace", Label: "替换为", Width: 96},
				},
			}},
		// ───────── 下面这些是**开窗口**（蓝）：只读/视图/生成类 ─────────
		{Short: "变色", Full: "RGB 变色", Kind: KindPage, Page: "rgb.html"},
		{Short: "填词", Full: "填词", Kind: KindPage, Page: "lyric.html"},
		{Short: "LRC", Full: "LRC 导出", Kind: KindPage, Page: "lrc.html"},
		{Short: "跨复", Full: "跨轨复制", Kind: KindPage, Page: "xcopy.html"},
		{Short: "单复", Full: "轨内复制", Kind: KindPage, Page: "within.html"},
		{Short: "映射", Full: "参数映射", Kind: KindPage, Page: "param.html"},
		{Short: "查找", Full: "查找", Kind: KindPage, Page: "find.html"},
		{Short: "谱面", Full: "谱面编辑器", Kind: KindPage, Page: "notation.html"},
		// ───────── 底部一组：**改工程文件**（红）─────────
		// ⭐ 用户 2026-10-17：「把需要改工程的放到一起，用同色」——
		// 这四项走的是**文件路线**（保存工程 → 读 `.acep` → 改 → 替换 → 让 ACE 重新打开；
		// 音高线/颤音/音高锚点在 CLI 上没有写入通道），跟"开个窗口点点看"完全不同性质 ⇒
		// 单独一色（web 调色板的 `--bad`）、单独一段放最后，免得跟普通页面混在一起。
		// ⚠️ 这四项与 home.html 里 `tone:'file'` 的**同一组**（web 那边也用同一个颜色，见 `.fbtn.file`）。
		{Short: "跨复", Full: "跨轨道复制音高线", Kind: KindFile, Page: "pitchx.html"},
		{Short: "语调", Full: "语调教", Kind: KindFile, Page: "tone.html"},
		{Short: "颤音", Full: "复制颤音模板", Kind: KindFile, Page: "vib.html"},
		{Short: "单复", Full: "轨道内复制音高线", Kind: KindFile, Page: "pitch.html"},
		{Short: "转锚", Full: "音高线转锚点", Kind: KindFile, Page: "pitchtoanchors.html", DefaultHidden: true},
		// ───────── 面板级（2026-10-17：用户「把设置和展开为web页的按钮加到侧栏」）─────────
		// 「设置」= 设置页（快捷键 / 工具=CLI 与侧栏开关 / 页面布局 / 关于）；
		// 「展开」= 与面板顶栏那颗「侧」按钮**互为镜像**：开 web 面板窗口 + 停掉细条。
		// ⚠️ 「展开」也是**蓝**（它确实开了个窗口），但它没有 Page —— 靠 Act 走动作。
		// ⭐ 用户 2026-10-20：生成二维码（**默认隐藏**，在设置→按钮顺序与显示里勾上才出现）。
		{Short: "二维码", Full: "生成二维码", Kind: KindFile, Page: "qr.html", DefaultHidden: true},
		{Short: "设置", Full: "设置", Kind: KindPage, Page: "settings.html"},
		{Short: "展开", Full: "展开为 web 页", Kind: KindPage, Act: ActExpandWeb},
	}
}
// defaultHiddenKeys 是出厂清单里那些**默认隐藏**的 key（设置页勾上才会出现）。
func defaultHiddenKeys() []string {
	all := allItems()
	var out []string
	for _, it := range all {
		if it.DefaultHidden {
			out = append(out, it.KeyOf())
		}
	}
	return out
}
