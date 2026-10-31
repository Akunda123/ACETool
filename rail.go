// exe 侧那条**贴 ACE 窗口的汉字细条**的装配层（实现在 `internal/rail`）。
//
// 清单（19 项 / 简称 / 三类颜色）在 `internal/rail/list.go`；
// 这里只做两件事：起它、把它说的话接进面板的事件通道（`/api/events` ⇒ 面板上也能看到、自检也能查）。

package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"acetool/internal/dock"
	"acetool/internal/rail"
	"acetool/internal/win"
)

// thePanelURL 是本面板的地址（`main.go` 起完服务器就塞进来）。
//
// 为什么要有它：app 模式的独立窗口（`/api/app-window`）得拿它拼页面地址；
// 而那条路跟侧条**无关**（侧条关掉时也要能用）⇒ 不能在 `startRail` 里赋值。
var thePanelURL string

// theDock 是"把贴边的 app 窗口吸到 ACE 边上"那次会话（见 `internal/dock`）。
var theDock *dock.Dock

// theAppWin 是**我们自己开出来的那个 web 窗口**（`/api/app-window`）。
//
// 为什么要记住它：切到侧栏时要把它关掉（用户 2026-10-16：「切的时候关掉」），
// 而"按标题找"会误伤用户自己在浏览器里开的面板页 ⇒ **只关我们开的那个**。
var (
	theAppWin     uintptr
	theAppWinName string
)

// closeAppWin 关掉我们开的那个 web 窗口（没开过/已经关了 ⇒ 什么都不做）。
func closeAppWin() bool {
	if theAppWin == 0 || !win.Alive(theAppWin) {
		theAppWin = 0
		return false
	}
	err := win.Close(theAppWin)
	theAppWin = 0
	return err == nil
}

// rememberAppWin 在开完窗口之后，把它认出来并记住句柄。
//
// ⚠️ 判据是"**这次新出现的**那个"（`before` 是开窗之前的句柄快照），不能只按标题前缀认：
// 面板的几个窗口标题互相包含（`ACETool` / `ACETool 侧栏` / 原生细条 `ACETool 细条`）
// ⇒ 只按前缀会**关错窗口**（真机踩过：想关 home，关掉了另一个）。
//
// 为什么不在启动那一刻抓：`RunDetached` 只是把进程拉起来，窗口要几十到几百毫秒才出现
// （Edge 还得先找现成的进程）⇒ 得轮询一会儿。抓不到也**不影响**开窗口本身，
// 只是"切到侧栏时关不掉它"（这条会写进日志，不假装成功）。
func rememberAppWin(titleSub string, before []uintptr) {
	if titleSub == "" {
		return
	}
	seen := map[uintptr]bool{}
	for _, h := range before {
		seen[h] = true
	}
	go func() {
		for i := 0; i < 20; i++ { // 最多约 4 秒
			time.Sleep(200 * time.Millisecond)
			ws, err := win.FindPrefixed(titleSub)
			if err != nil {
				continue
			}
			for _, w := range ws {
				if seen[w.Handle] || win.Minimized(w.Handle) {
					continue
				}
				theAppWin, theAppWinName = w.Handle, w.Title
				log.Printf("记住 web 窗口：0x%x「%s」（切到侧栏时会关掉它）", w.Handle, w.Title)
				return
			}
		}
		log.Printf("没能记住 web 窗口（标题前缀 %q）—— 切到侧栏时不会去关它", titleSub)
	}()
}

// appWinSnapshot 开窗**之前**先记一份"已经存在的同类窗口"，好认出新来的那个。
func appWinSnapshot(titleSub string) []uintptr {
	ws, err := win.FindPrefixed(titleSub)
	if err != nil {
		return nil
	}
	out := make([]uintptr, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.Handle)
	}
	return out
}

// dockTitleOf 从页面文件名推那个窗口的标题前缀（吸附要靠标题认窗口）。
//
// ⚠️ 这就是**页面的 `<title>`**：`rail.html` 的标题是「ACETool 侧栏」。
// 加了新页要记得在这儿补一行（漏了的表现是"吸不上" —— 找不到窗口，自检里看得见）。
func dockTitleOf(page string) string {
	switch page {
	case "rail.html":
		return "ACETool 侧栏"
	case "home.html":
		return "ACETool"
	default:
		return ""
	}
}

// setDock 起/停吸附。enable=false 或 title 为空 ⇒ 停掉（目标窗口留在原地，它不是我们建的）。
func setDock(title string, enable bool) error {
	if theDock != nil {
		theDock.Stop()
		theDock = nil
	}
	if !enable {
		return nil
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("要吸哪个窗口？得给一个标题前缀（如 `ACETool 侧栏`）")
	}
	d, err := dock.Start(dock.Options{
		Title: title,
		OnNote: func(kind, text string) {
			log.Printf("[dock] %s：%s", kind, text)
		},
	})
	if err != nil {
		return err
	}
	theDock = d
	log.Printf("吸附已起：目标窗口标题前缀 %q", title)
	return nil
}

// startRail 起细条。**失败不致命**：面板照常用，只把原因说出来。
// watchACEAndRail 是**启动即静默**那条路（用户 2026-10-20）：
// 托盘先起来、面板窗口不自动弹；这里每 2 秒看一眼 **ACE 进程在不在**，
// 一出现就把侧栏起来（已经起来了就不重复起）——于是"先开 ACE 再开我们"与"先开我们再开 ACE"都一样。
func watchACEAndRail(st *panelState, url string) {
	for {
		if theRail == nil && rail.ACERunning() {
			if err := startRail(st, url); err != nil {
				log.Printf("侧条没起来（面板照常用）：%v", err)
			} else {
				log.Printf("ACE 出现了 ⇒ 侧条已起")
			}
		}
		time.Sleep(2 * time.Second)
	}
}

func startRail(st *panelState, url string) error {
	r, err := rail.Start(rail.Options{
		BaseURL: url,
		OnNote: func(kind, text string) {
			st.push(kind, "rail", text) // 走面板那套事件环形缓冲 ⇒ 面板上会冒 toast、`/api/events` 也查得到
		},
	})
	if err != nil {
		return err
	}
	theRail = r
	log.Printf("侧条已起：%d 项（贴 ACE 窗口的右边；鼠标滑过展开）", len(rail.Items()))
	return nil
}

// toggleRail 运行时起/停细条（用户 2026-10-16：「做一个 web 和侧栏的切换按钮就行了」）。
//
// 为什么要运行时开关：细条原来只在启动时按 `-no-rail` 决定，那样**没法做切换按钮**。
// ⚠️ 已知问题（HANDOVER §72）：**建了细条之后 exe 会自己退出**（不是崩溃、也不是用户关的）——
//
//	在 ACE 开着时能活很久，关着时几秒就没。所以这个开关现在**带着风险**：
//	切到侧栏之后如果 exe 退了，那是 §72 那条没修的问题，不是这个按钮的错。
func toggleRail(st *panelState, on bool) (string, error) {
	if !on {
		if theRail == nil {
			return "侧条本来就没起", nil
		}
		theRail.Stop()
		theRail = nil
		st.push("info", "rail", "侧条已停（exe 继续跑）")
		return "侧条已停", nil
	}
	if theRail != nil {
		return "侧条已经在跑了", nil
	}
	if err := startRail(st, thePanelURL); err != nil {
		return "", err
	}
	// 用户 2026-10-16：「切的时候关掉」—— 切到侧栏就把我们开的那个 web 窗口关掉（**只关自己开的**）
	if closeAppWin() {
		st.push("info", "rail", "侧条已起；顺手关掉了那个 web 窗口（「窗」随时能再开）")
		return "侧条已起（web 窗口已关）", nil
	}
	st.push("info", "rail", "侧条已起（贴 ACE 窗口的右边）")
	return "侧条已起", nil
}
