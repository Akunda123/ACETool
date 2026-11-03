// 面板窗口的**标准尺寸**（逻辑像素 = DIP）。
//
// ⭐ 2026-10-17 实测定下来的口径（用户：「把所有打开的面板隐藏地址栏，把窗口做小」）：
//
//  1. **`--window-size` 的单位是 DIP（逻辑像素），不是物理像素** —— 实测在本机 150% 缩放下
//     `--window-size=500,780` 开出来的窗口是 **750×1170 物理**（×1.5）。所以这里写的数**就是**
//     页面上看起来的宽度，不用自己乘缩放。
//  2. 每个页面的数字**不是拍脑袋**：跟各页原来的 `window.open(..., 'width=…')` 保持同一套
//     （那些值是逐页调过的：rgb 460×620、lyric 540×720、lrc 620×680、xcopy 680×760…）。
//     统一成一个"小窗"标准 520×700，宽的那些（谱面/语调教/跨轨复制）按它自己需要的宽度给，
//     免得从细条打开时把一个按 680 宽设计的页面塞进 520 里。
//  3. 页面名不认识 ⇒ 用标准尺寸（宁可小，也不要突然蹦出一个全屏窗口）。
package web

const (
	// DefaultPanelW / DefaultPanelH 是"没特别说"的面板标准尺寸（逻辑像素）。
	// 520 是数据挑出来的：现有各页调过的宽度是 460–700，520 落在中间偏窄那侧 ⇒ 看着是"小窗"，
	// 又不至于把一个两栏（比如设置页 132 的导航 + 正文）挤坏。
	DefaultPanelW = 520
	DefaultPanelH = 700
)

// PanelSize 返回某个页面**该开多大**（逻辑像素）。页面名不认识就用标准尺寸。
//
// ⚠️ 加了新页面、且它需要更宽/更矮，就往这张表里补一行 —— 这张表是**唯一**的地方
// （`/api/app-window` 与细条的开页面都从这里取，别再两边各写一套数字）。
func PanelSize(page string) (w, h int) {
	if s, ok := panelSizes[page]; ok {
		return s[0], s[1]
	}
	return DefaultPanelW, DefaultPanelH
}

// panelSizes 是各页**调过**的尺寸（逻辑像素），与前端那些 `window.open(..., 'width=…')` 同源。
var panelSizes = map[string][2]int{
	// 主页面（**现在是唯一入口**，用户 2026-10-17：「停用那个默认的 web 页面，现在起只用主页面」）：
	// 启动窗口、托盘「打开面板」、「展开为 web 页」都是这一页。
	"home.html": {520, 700},
	// 老壳页（整份工具清单 + 命令日志）：**已停用**，`/` 与 `/index.html` 都 302 到 home.html。
	// 尺寸留着只是因为文件还在 exe 里（`check-served` 逐字节比对），万一有人手点 `/index.html.`… 没这回事。
	"index.html":     {720, 760},
	"settings.html":  {640, 560}, // 左侧导航 132 + 正文，520 会挤
	"find.html":      {620, 760},
	"lrc.html":       {620, 680},
	"lyric.html":     {540, 720},
	"notation.html":  {820, 1000}, // 谱面：整张纸要宽
	"param.html":     {620, 760},
	"pitch.html":     {620, 520},
	"pitchtoanchors.html": {620, 520},
	"phoneme.html":        {760, 560},
	"pitchx.html":    {620, 560},
	"rgb.html":       {460, 620},
	"tone.html":      {700, 640},
	"vib.html":       {540, 640},
	"within.html":    {620, 720},
	"xcopy.html":     {680, 760},
	"rail.html":      {160, 700}, // 窄栏页：本来就给窄窗口用的
	"export.html":    {760, 720},
	"shortcuts.html": {520, 700},
}
