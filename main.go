// acetool —— ACE Studio 工具箱（独立、零依赖、单文件）。
//
// 目标与边界见资料仓的交接文档：..\acetool\HANDOVER.md
//
//	· 只通过 ACE 自带的 acestudio-cli.exe 读写工程
//	· 不碰 Synthesizer V / Instrument X；不注册 ACE 官方 MCP；不做 UI 自动化
//	· 不要求用户安装任何运行时（CGO_ENABLED=0 静态单文件）
//
// 当前是 M0「启动自检」的装配层：找 CLI → 能力探测 → 在线灯 → 起本地只读面板。
// 还没有任何写入路径。
package main

import (
	"strconv"
	"net/url"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"acetool/internal/acecli"
	"acetool/internal/config"
	"acetool/internal/layout"
	"acetool/internal/rail"
	"acetool/internal/tools"
	"acetool/internal/tray"
	"acetool/internal/web"
	"acetool/internal/win"
)

// theRail 是那条细条（退出时要 Stop；`/api/rail` 也要读它）。
var theRail *rail.Rail

// theTray 是系统托盘图标（退出时要 Stop；`/api/tray` 也要读它）。
var theTray *tray.Tray

// appVersion 由 build.ps1 用 -ldflags -X main.appVersion=x.y.z 覆盖
var appVersion = "1.1.0"

func main() {
	var (
		cliFlag = flag.String("cli", "", `acestudio-cli 路径（覆盖自动查找）`)
		// `-no-browser` 只为兼容旧快捷方式/注册表保留；现在**默认就不弹窗口**（用 `-browser` 才弹）。
		_           = flag.Bool("no-browser", false, "（兼容用；默认就不弹窗口，想看窗口用 -browser）")
		openBrowser = flag.Bool("browser", false, "启动时打开面板窗口（默认静默在托盘）")
		noRail      = flag.Bool("no-rail", false, "不要那条贴 ACE 的汉字侧条")
		forceNew    = flag.Bool("force-new", false, "已有面板在跑时也强行再起一个（默认不起，见 instance.go）")
		noTray      = flag.Bool("no-tray", false, "不要系统托盘图标")
		console     = flag.Bool("console", false, "打开一个控制台窗口看日志（发版是 GUI 构建，默认没有黑框）")
	)
	flag.Parse()

	// ⭐ 用户 2026-10-20：「能否将那个 command 窗口隐藏」⇒ 发版用 `-H=windowsgui` 构建（没有黑框）。
	// 想看日志：`-console` 开一个控制台，或者看 `%APPDATA%\ACETool\acetool.log`（下面会重定向过去）。
	if *console {
		win.EnsureConsole()
	} else if dir := filepath.Join(os.Getenv("APPDATA"), "ACETool"); os.MkdirAll(dir, 0o755) == nil {
		if f, ferr := os.OpenFile(filepath.Join(dir, "acetool.log"),
			os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); ferr == nil {
			log.SetOutput(f) // GUI 构建下 stderr 是黑洞 ⇒ 让 log 落到文件（不给 f 关：进程活多久写多久）
		}
	}

	// ⚠️ 先声明 DPI 感知**再建任何窗口**：否则 150% 缩放的屏上汉字是糊的、坐标还会被系统虚拟化
	//（真机 2560x1440 @150% ⇒ 不感知时读到的是 1707x960 的逻辑坐标，细条位置就会偏）。
	win.SetDPIAware()

	// 单实例守卫：已有面板活着 ⇒ 只把浏览器开过去，自己退出（原因见 instance.go 顶部三条）。
	if !*forceNew {
		if cur := existingInstance(1500 * time.Millisecond); cur != nil {
			log.Print(instanceNotice(cur, false))
			// ⭐ 用户 2026-10-20：默认**静默**（托盘常驻）⇒ "已经有实例在跑"时**也不弹窗口**。
			// 以前这里无条件开一个 Home，正是他说的「刚打开还是会弹一个 Home」。
			if *openBrowser {
				if err := openPanelWindow(cur.URL); err != nil {
					log.Printf("打不开浏览器（手动访问 %s）：%v", cur.URL, err)
				} else {
					fmt.Printf("\n  %s\n\n", instanceNotice(cur, true))
				}
			} else {
				log.Printf("已在运行（托盘里）⇒ 这次不弹窗口；要看面板点托盘「打开面板」，或用 -browser 启动")
			}
			return
		}
	}

	cfg, err := config.Load()
	if err != nil {
		log.Printf("配置读取失败（用默认值继续）：%v", err)
	}
	// ⭐ 用户 2026-10-20：侧栏按设置页那份「按钮顺序与显示」渲染（空 = 内置顺序、全显示）。
	// ⛔ 只影响渲染；被隐藏的项快捷键照跑（用户重申过）。
	// ⭐ 内置顺序改版（2026-10-21：按颜色分组、删词/合并置顶）⇒ 老配置里那份 layoutOrder 清一次，
	// 否则用户看到的是"顺序没改"（他以前保存过一份旧顺序）。只清一次，之后以用户保存的为准。
	if cfg.LayoutVersion < 3 {
		cfg.LayoutOrder = nil
		cfg.LayoutVersion = 3
		_ = cfg.Save()
	}
	// ⭐ 2026-10-31：新增「音高线转锚点」= 默认隐藏（同二维码）。老配置里若存过
	// layoutHidden/layoutShown，也把它显式并进 hidden 一次，保证升级后默认看不见。
	if cfg.LayoutVersion < 4 {
		const k = "page:pitchtoanchors.html"
		cfg.LayoutHidden = layout.Clean(append(cfg.LayoutHidden, k))
		cfg.LayoutVersion = 4
		_ = cfg.Save()
	}
	rail.SetLayout(cfg.LayoutOrder, cfg.LayoutHidden, cfg.LayoutShown)
	// 启动后异步查一次更新（不阻塞启动、失败只记在 about 页）
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if i := checkUpdate(ctx, true); i != nil && i.Newer {
			log.Printf("有新版本：%s（当前 %s）", i.Latest, i.Current)
		}
	}()
	if *cliFlag != "" {
		cfg.CLIPath = *cliFlag
	}

	// 找 CLI。找不到也**继续**：面板上要能把「未检测到 ACE Studio」讲清楚（HANDOVER.md §2）。
	// 注意：配置里存了个失效路径时**不要**拿它当结果 —— 那会变成「进程起不来」的假错误，
	// 而真实情况是「没检测到」。
	if cfg.CLIPath != "" {
		if _, err := os.Stat(cfg.CLIPath); err != nil {
			log.Printf("配置里的 CLI 路径已失效，忽略它：%s", cfg.CLIPath)
		}
	}
	path, from := acecli.Locate(cfg.CLIPath)
	if path == "" {
		log.Printf("未检测到 acestudio-cli —— 面板仍会启动，请用手选路径或 --cli 指定")
	} else {
		log.Printf("acestudio-cli：%s（来源：%s）", path, from)
		if from != "配置" {
			cfg.CLIPath = path
			if err := cfg.Save(); err != nil {
				log.Printf("配置保存失败（不影响本次运行）：%v", err)
			}
		}
	}

	client := acecli.New(path, time.Duration(cfg.TimeoutSec)*time.Second)

	mux, st := newMux(client, from) // 路由在 api.go：工具那三条 + 面板自己那几条（panel_api.go）
	defer st.Close()                // 退出时撤掉系统级热键（不撤会留着占用）

	assets, err := web.Assets()
	if err != nil {
		log.Fatalf("面板静态文件读不出来：%v", err)
	}
	// ⚠️ 静态资源**禁用缓存**：面板的 HTML/JS 是嵌在 exe 里的，改了就必须立刻生效 ——
	// 浏览器一旦拿旧副本，用户看到的就是"按钮不存在 / 布局没变"这种假象
	//（实测撞过：`notation.js` 加了新块，页面里却还是旧那一份 ⇒ 得手动 Ctrl+F5 才出来）。
	// `/api/*` 那边早就 set 了 no-store，这里把静态资源也补上。
	assetsHandler := http.FileServer(http.FS(assets))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		// ⭐ 2026-10-17 用户：「停用那个默认的 web 页面，现在起只用 main（主页面）」
		//    以前 `/` 由 `http.FileServer` 落到 **index.html**（老壳页：整份工具清单 + 命令日志，
		//    也就是老壳页那个窗口）。现在入口一律是**主页面 `home.html`**（标题「ACE 工具箱」）：
		//      · `/`（不带路径 ⇒ 所有"只知道端口"的入口：exe 打印的地址、单实例提示、书签）
		//      · `/index.html`（老书签/旧标签页）—— 也 302 过去，而不是给 404 让人以为服务挂了
		//    老壳页的三个文件（index.html / app.js / shortcuts.js）**仍嵌在 exe 里**（`check-served`
		//    还逐字节比对它们，也保着"想翻回去看一眼"的可能），但**不再是任何入口**。
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			http.Redirect(w, r, "/home.html", http.StatusFound)
			return
		}
		assetsHandler.ServeHTTP(w, r)
	}))

	// 沿用上次的端口（老窗口就不会因为端口变了而 Failed to fetch）
	preferPort := 0
	if old := readInstance(); old != nil {
		if u, e := url.Parse(old.URL); e == nil {
			preferPort, _ = strconv.Atoi(u.Port())
		}
	}
	srv, url, err := web.Start(mux, preferPort)
	if err != nil {
		log.Fatalf("起本地面板失败：%v", err)
	}
	// 面板地址要留一份给 `/api/app-window`（app 模式的独立窗口拿它拼页面地址）——
	// 忘了这一句的话 URL 会拼成 `/home.html`，Edge 只会开出一个"新建标签页"（踩过）。
	thePanelURL = url
	// 写下自己的地址，并**在退出时按 pid 认领删除**（只删自己那条，不动后来者）
	if err := writeInstance(instanceInfo{
		App: "acetool", PID: os.Getpid(), URL: url,
		StartedAt: time.Now().Format(time.RFC3339), Tools: tools.Summary(),
	}); err != nil {
		log.Printf("面板发现文件写不进去（不影响使用，只是单实例守卫这次失效）：%v", err)
	}
	defer removeInstance(os.Getpid())

	// 工具概况**现算**（以前写死在这里，加了工具就变成谎话 —— 已经漂过两次）。
	fmt.Printf("\n  ACETool 面板：%s\n  %s\n"+
		"  「查找」页 = 逐条复刻 SV 脚本 [SV2]查找.js；「歌词」页 = 复刻 [SV2]歌词侧栏.js\n"+
		"  写操作默认干跑（查找页的「上一项/下一项/全选」是视图/选区操作，直接执行）\n"+
		"  Ctrl+C 退出\n\n", url, tools.Summary())

	// ⭐ 用户 2026-10-20：「打开后静默在状态栏，**不启动窗口**」——默认**不弹面板窗口**了；
	// 要看面板走托盘「打开面板」/ 侧栏「展开为 web 页」/ 快捷键（`quick.open`）。
	// 老开关 `-no-browser` 仍然接受（开机启动的注册表里就带它，别让它变成未知参数）。
	// ⚠️ 不管开不开窗口，先把**上一个实例留下的面板窗口**收掉（它们连的是死端口）。
	// 不收的话，托盘「打开面板」会 focus 到那个死窗口上 —— 用户看到的就是"打开面板不能用了"。
	closeStaleHomeWindows()
	if *openBrowser {
		if err := openPanelWindow(url); err != nil {
			log.Printf("打不开浏览器（手动访问上面那个地址即可）：%v", err)
		}
	}

	// ⭐ 用户 2026-10-20：**启动后静默在托盘，不弹窗口**；侧栏等 ACE 起来再起
	//   （「ace 启动后或打开时若 ace 启动，启动侧栏」）。
	// 起不起来的判断保留两件事：`config.RailOnBoot` 与 `-no-rail`。
	wantRail := cfg.RailOnBoot && !*noRail
	if wantRail {
		go watchACEAndRail(st, url)
	}

	// ⭐ 系统托盘图标（用户 2026-10-17：「加个状态栏图标」）。
	// 起不来**不影响面板**（如实说一句就继续跑）。
	// ⚠️ 三件事（打开面板/打开设置/退出）先接线：`quick.open`（主页面键位）与 `POST /api/tray`
	//    都要用它，而托盘本体可以被 `-no-tray` 关掉。
	quitCh := make(chan string, 1)
	setupActions(url, quitCh)
	if !*noTray {
		startTray(url)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	notePreviousExit()
	exitLog("exe 起来了 pid=%d url=%s rail=%v tray=%v（现在等信号）",
		os.Getpid(), url, wantRail, theTray != nil)
	// 两条退出通道走**同一段收尾**：Ctrl+C，或托盘菜单「退出 ACETool」。
	select {
	case sig := <-stop:
		// ⭐ 这条日志是 §72 那件"exe 会自己消失"的关键证据（detached 进程的 stderr 看不到）：
		// 走到这里说明是**收到信号**才退的（不是被杀）。日志在 `%APPDATA%\ACETool\exit.log`。
		exitLog("收到信号 %v ⇒ main 要退出了", sig)
	case why := <-quitCh:
		exitLog("收到退出请求（%s）⇒ main 要退出了", why)
	}

	if theTray != nil {
		theTray.Stop()
	}
	if theRail != nil {
		theRail.Stop()
	}
	if theDock != nil {
		theDock.Stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	exitLog("main 正常返回（进程要退了）")
}

// trayActions 是「打开面板 / 打开设置 / 退出」这三件事的**唯一实现**：
// 托盘菜单、`POST /api/tray`、**主页面键位里的「打开主页面」**（`quick.open`）都调这里 ——
// 免得"菜单能用、接口不能"（或者反过来）—— 两套实现必然漂。
var trayActions struct {
	Open     func()
	Settings func()
	Quit     func()
}

// setupActions 把上面那三件事接到具体实现上。
//
// ⚠️ 它**与托盘是否起来无关**：`quick.open`（主页面键位）也要用 `trayActions.Open`，
// 而托盘可以被 `-no-tray` 关掉 —— 所以接线要单独一步，不能塞在 `startTray` 里
// （第一版就是塞在里面，`-no-tray` 时 `quick.open` 会报"没人接"）。
func setupActions(url string, quitCh chan<- string) {
	trayActions.Open = func() { openOrFocusPanel(url) }
	trayActions.Settings = func() {
		// 已经有设置窗口就切过去（每次点都新开一个会攒一堆），没有才开
		if focusPageWindow("ACETool · 设置") {
			return
		}
		w, h := web.PanelSize("settings.html")
		if _, appMode, _, err := win.AppWindow(strings.TrimRight(url, "/")+"/settings.html", -1, -1, w, h); err != nil || !appMode {
			log.Printf("从托盘开设置页没走成 app 模式：%v", err)
		}
	}
	trayActions.Quit = func() {
		// 投给 main：**不在这里直接退**（rail/dock/服务都得按 main 那套顺序收）
		select {
		case quitCh <- "托盘菜单":
		default: // 已经有人在退了
		}
	}
}

// startTray 起托盘图标：左键 = 打开/切到面板窗口，右键菜单 = 打开面板 / 打开设置 / 退出。
// 动作实现在 `setupActions`（已提前接好）。
func startTray(url string) {
	t, err := tray.Start(tray.Options{
		Tip:        "ACETool · " + strings.TrimPrefix(strings.TrimSuffix(url, "/"), "http://"),
		OnOpen:     trayActions.Open,
		OnSettings: trayActions.Settings,
		OnQuit:     trayActions.Quit,
	})
	if err != nil {
		log.Printf("托盘图标没起来（面板照常用）：%v", err)
		return
	}
	theTray = t
	info := t.Info()
	exitLog("托盘图标起来了：hwnd=0x%x 图标=%s added=%v", info.HWND, info.Icon, info.Added)
}

// openOrFocusPanel 托盘左键 / 菜单「打开面板」：**已经有面板窗口就切过去**，没有才新开一个。
//
// 为什么不无脑新开：点一次多一个窗口（用户会攒一堆"同一个页面"的窗口）。
// 为什么找不到就开新的：老窗口可能连的是**上一次 exe 的端口**（已经打不开了），
// 那种窗口由启动时的 `closeStaleHomeWindows` 收掉，这里只当兜底。
func openOrFocusPanel(url string) {
	// 只找**主窗口**（标题完全一致）。以前还"退一步找任何 ACETool 窗口"，
	// 结果托盘「打开面板」会切到「ACETool · 设置」/旧端口留下的死窗口上 ⇒ 看起来就是点不动
	//（用户 2026-10-21：「工具栏图标的打开面板不能用了」）。
	if focusPageWindow(homeTitle) {
		return
	}
	if err := openPanelWindow(url); err != nil {
		log.Printf("从托盘打开面板失败：%v", err)
	}
}

// focusPageWindow 找一个**标题完全一致**的面板窗口并切过去。找到并成功切到前台才返回 true。
func focusPageWindow(title string) bool {
	ws, err := win.FindExact(title)
	if err != nil {
		return false
	}
	for _, w := range ws {
		if w.PID == uint32(os.Getpid()) {
			continue
		}
		if ok, _ := win.Focus(w.Handle); ok {
			return true
		}
	}
	return false
}

// closeStaleHomeWindows 关掉**上一次 exe 留下的**面板主窗口（标题 = `homeTitle`）。
//
// 为什么需要（2026-10-17 真机反复撞到）：重启 exe 后旧窗口不归我们管（它是浏览器窗口），
// 于是桌面上会同时挂着两个标题一样的窗口，旧的那个连的是**已经没了的端口**
// （页面停在"无法访问"）。用户分不清哪个能用，托盘左键也会切到那个死的。
//
// ⚠️ 只认**完全等于**这个标题的窗口：
//   - 浏览器普通窗口的标题会带 ` - 个人 - Microsoft Edge` 尾巴 ⇒ 不会命中（不会关用户的标签页）；
//   - app 窗口的标题就是网页标题本身 ⇒ 命中；
//   - 其它页面的窗口（`ACETool · 查找` 等）**不动** —— 那些是用户开着要用的。
func closeStaleHomeWindows() {
	ws, err := win.FindExact(homeTitle)
	if err != nil {
		return
	}
	for _, w := range ws {
		if err := win.Close(w.Handle); err == nil {
			log.Printf("关掉上一个实例留下的面板窗口：0x%x pid=%d", w.Handle, w.PID)
		}
	}
}

// openPanelWindow 打开面板的**主窗口**（启动时 / 单实例重定向 / 托盘左键 用）。
//
// ⭐ 2026-10-17 用户：「把所有打开的面板隐藏地址栏，把窗口做小」⇒ 这条路原来走
// `web.Open`（`rundll32 FileProtocolHandler`）= **普通浏览器窗口**（有标签栏、地址栏，还按浏览器
// 记的尺寸开）。现在：
//
//	· 优先**浏览器 app 模式**（`--app=`，无地址栏），尺寸取 `web.PanelSize("home.html")`；
//	· 开不出来（没装 Edge/默认浏览器不认 `--app`）⇒ 退回 `web.Open`（**如实说一句**，别假装成功）。
//
// ⚠️ 传进来的 `url` 是**面板根地址**（`http://127.0.0.1:<端口>/`）—— 现在根路径会 302 到
// `home.html`（"停用老壳页"那条，见上面静态资源那段的注释）。
func openPanelWindow(url string) error {
	w, h := web.PanelSize("home.html")
	if _, appMode, _, err := win.AppWindow(url, -1, -1, w, h); err == nil && appMode {
		return nil
	}
	log.Printf("app 模式不可用（没找到认 --app 的浏览器）⇒ 改用普通浏览器窗口（会有地址栏）")
	return web.Open(url)
}

// exitLog 把"进程为什么退出"追加到 `%APPDATA%\ACETool\exit.log`。
//
// 为什么需要它（用户 2026-10-16 报"面板突然没了"）：exe 是 `start-panel.ps1` 经 WMI **detached**
// 起的 ⇒ **stderr 看不到**；而它**会自己消失**（§72/§75 的未解决问题）。Windows 事件日志里
// 也没有任何记录。唯一能分清"收到信号自己退 / 被杀 / 崩"的办法就是自己落一行文件。
func exitLog(format string, a ...any) {
	dir := filepath.Join(os.Getenv("APPDATA"), "ACETool")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "exit.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return // 记不上就算了，不能因为日志把主流程搞崩
	}
	defer f.Close()
	fmt.Fprintf(f, "%s  %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, a...))
}

// notePreviousExit 启动时看一眼：**上一个实例有没有留下退出记录**。
//
// 这是"面板突然没了"最省事的判据：① 有「收到信号」⇒ 它自己收到中断退的（控制台被关之类）；
// ② 有「main 正常返回」⇒ 正常收尾；③ **什么都没有** ⇒ 它是**被强杀**的（任务管理器 / 杀软 /
// 我 rebuild 时的 Stop-Process —— 后者最冤，得能一眼区分）。
func notePreviousExit() {
	p := filepath.Join(os.Getenv("APPDATA"), "ACETool", "exit.log")
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	lastStart := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "exe 起来了") {
			lastStart = i
			break
		}
	}
	if lastStart < 0 {
		return
	}
	sawExit := false
	for _, l := range lines[lastStart+1:] {
		if strings.Contains(l, "收到信号") || strings.Contains(l, "main 正常返回") {
			sawExit = true
		}
	}
	if !sawExit {
		exitLog("⚠️ 上一个实例（%s）**没留下任何退出记录** ⇒ 它是被强杀的（不是自己退的）",
			strings.TrimSpace(strings.SplitN(lines[lastStart], "  ", 2)[0]))
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
