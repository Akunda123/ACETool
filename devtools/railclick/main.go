// railclick —— 合成点击细条上的某一项，然后**说出它到底干了什么**（开了什么窗口 / 跑了什么工具）。
//
// 来历：为了验「点完之后前台有没有还给 ACE」（用户 2026-10-16），那件事已经结束（`giveFocusBack` 已删除，
// 见 `internal/rail/rail_windows.go`）。现在它换成**更能抓问题**的用法：
// 用户 2026-10-17 报「所有按钮都是打开 webmain」⇒ 那必须能**不用人点**地复现一次，
// 于是这里在点击前后各拍一次窗口快照，把"新冒出来的窗口"的**标题/矩形**打出来 —— 一眼看出开了哪一页。
//
// 用法：go run ./devtools/railclick [-item 5] [-wait 3] [-keep]
//
//	-item N  点第 N 项（0 起，顺序 = `/api/rail/items`）
//	-wait S   点完等几秒再拍快照（窗口要几百毫秒才出来）
//	-keep     点完**留着**（不自动停细条、不清假光标）
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"acetool/internal/win"
)

const apiHeader = "X-ACETool"

type railState struct {
	Running   bool     `json:"running"`
	Level     int      `json:"level"`
	ItemH     int      `json:"itemH"`
	DPI       int      `json:"dpi"`
	Visible   bool     `json:"visible"`
	HWND      uintptr  `json:"hwnd"`
	Rect      win.Rect `json:"rect"`
	Hover     int      `json:"hover"`
	PanelItem int      `json:"panelItem"`
	PanelHWND uintptr  `json:"panelHwnd"`
	PanelRect win.Rect `json:"panelRect"`
	PanelTier int      `json:"panelTier"`
	Note      string   `json:"note"`
}

type item struct {
	Short, Full, Kind, Act, Page, Tool string
}

func base() string {
	p := filepath.Join(os.Getenv("APPDATA"), "ACETool", "panel.json")
	b, err := os.ReadFile(p)
	if err != nil {
		fmt.Println("读不到 panel.json（面板没起？）：" + err.Error())
		os.Exit(1)
	}
	var v struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		fmt.Println("panel.json 解析失败：" + err.Error())
		os.Exit(1)
	}
	return v.URL
}

func get(u string) railState {
	r, err := http.Get(u + "api/rail")
	if err != nil {
		fmt.Println("问不到 /api/rail：" + err.Error())
		os.Exit(1)
	}
	defer r.Body.Close()
	var s railState
	_ = json.NewDecoder(r.Body).Decode(&s)
	return s
}

func items(u string) []item {
	r, err := http.Get(u + "api/rail/items")
	if err != nil {
		return nil
	}
	defer r.Body.Close()
	var v struct {
		Items []item `json:"items"`
	}
	_ = json.NewDecoder(r.Body).Decode(&v)
	return v.Items
}

func post(u, path string, body any) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", u+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(apiHeader, "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("POST " + path + " 失败：" + err.Error())
		os.Exit(1)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	fmt.Printf("  POST %s ⇒ %s\n", path, string(bytes.TrimSpace(out)))
}

// snap 是"看得见的顶层窗口"的一份快照（句柄 → 描述）。
func snap() map[uintptr]string {
	ws, err := win.List()
	if err != nil {
		return nil
	}
	m := map[uintptr]string{}
	for _, w := range ws {
		if w.Class == "ACEToolRail" || w.Class == "ACEToolRailTip" { // 细条自己不算
			continue
		}
		rc, _ := win.RectOf(w.Handle)
		m[w.Handle] = fmt.Sprintf("pid=%d「%s」(%d,%d)-(%d,%d) %dx%d", w.PID, w.Title,
			rc.Left, rc.Top, rc.Right, rc.Bottom, rc.W(), rc.H())
	}
	return m
}

func main() {
	it := flag.Int("item", 5, "点第几项（0 起）")
	wait := flag.Int("wait", 3, "点完等几秒再拍快照")
	keep := flag.Bool("keep", false, "点完留着（不停细条、不清假光标）")
	panelAt := flag.String("panel", "", "再往参数条上点一下（客户区坐标 `x,y`）；用来验滑块/按钮")
	flag.Parse()

	u := base()
	fmt.Println("面板：" + u)
	win.SetDPIAware() // 坐标跟细条一致（物理像素）

	if list := items(u); len(list) > 0 && *it >= 0 {
		if *it >= len(list) {
			fmt.Printf("第 %d 项不存在（清单 %d 项）\n", *it, len(list))
			os.Exit(1)
		}
		q := list[*it]
		fmt.Printf("要点的是 #%d：%s（%s）kind=%s act=%s page=%s tool=%s\n\n",
			*it, q.Short, q.Full, q.Kind, q.Act, q.Page, q.Tool)
	}
	if *it < 0 {
		// `-item -1` = **不点细条**，只做后面的 `-panel`（验滑块/按钮时用：
		// 否则每跑一次都会把参数条先"切换"掉一次）
		fmt.Println("（-item -1 ⇒ 跳过细条那一下，只操作参数条）")
		st0 := get(u)
		fmt.Printf("参数条：panelItem=%d panelTier=%d panelHwnd=0x%x\n", st0.PanelItem, st0.PanelTier, st0.PanelHWND)
		if st0.PanelHWND != 0 && *panelAt != "" {
			var px, py int
			fmt.Sscanf(*panelAt, "%d,%d", &px, &py)
			if err := win.SendClick(st0.PanelHWND, int32(px), int32(py)); err != nil {
				fmt.Println("往参数条点击失败：" + err.Error())
			} else {
				time.Sleep(1200 * time.Millisecond)
				st3 := get(u)
				fmt.Printf("点完：panelItem=%d panelTier=%d note=「%s」\n", st3.PanelItem, st3.PanelTier, st3.Note)
			}
		}
		return
	}

	st := get(u)
	if !st.Running {
		fmt.Println("细条没在跑 ⇒ 先起它")
		post(u, "api/rail", map[string]any{"enable": true})
		time.Sleep(1500)
		st = get(u)
	}
	if !st.Visible || st.ItemH == 0 {
		fmt.Printf("细条没显示（visible=%v itemH=%d）—— ACE 没开或最小化？先 go run ./devtools/acefocus\n", st.Visible, st.ItemH)
		os.Exit(1)
	}
	scale := float64(st.DPI) / 96
	pad := int32(6 * scale)
	// 假光标：离右缘 15 逻辑像素 ⇒ 第③级（压在"字"上），确保点得着
	y := st.Rect.Top + pad + int32(*it)*int32(st.ItemH) + int32(st.ItemH)/2
	x := st.Rect.Right - int32(15*scale)
	fmt.Printf("注入前状态：visible=%v level=%d itemH=%d rect=(%d,%d)-(%d,%d) ⇒ 注入点 (x=%d, y=%d)\n",
		st.Visible, st.Level, st.ItemH, st.Rect.Left, st.Rect.Top, st.Rect.Right, st.Rect.Bottom, x, y)
	if x < 0 || y < 0 {
		fmt.Println("⛔ 算出来的注入点是负坐标 ⇒ `/api/rail/cursor` 会把它当成**取消注入**（x<0 即清除）——")
		fmt.Println("   根因是这一刻读到的 state 是空的（Rect 全 0）。先确认细条 visible=true 且 rect 非空再跑。")
		os.Exit(1)
	}
	// ⚠️ 注入点必须按**当下最新**的矩形算，并且**验到级别真的抬起来**为止：
	// ACE 被还原/移动（比如刚跑过 `acefocus`）时细条的 rect 会在"读状态"和"注入"之间变，
	// 按旧矩形算出来的点会落在新细条外面 ⇒ 级别回 0 ⇒ 点击被合理拒绝（这个坑踩过好几次）。
	var clicked bool
	for attempt := 1; attempt <= 6; attempt++ {
		st = get(u)
		if !st.Visible || st.ItemH == 0 {
			fmt.Println("细条此刻没显示（ACE 最小化了？）—— 先 go run ./devtools/acefocus")
			os.Exit(1)
		}
		scale = float64(st.DPI) / 96
		pad := int32(6 * scale)
		y := st.Rect.Top + pad + int32(*it)*int32(st.ItemH) + int32(st.ItemH)/2
		x := st.Rect.Right - int32(15*scale)
		if x < 0 || y < 0 {
			fmt.Println("注入点是负坐标（state 是空的）⇒ 等一下再来")
			time.Sleep(400 * time.Millisecond)
			continue
		}
		post(u, "api/rail/cursor", map[string]any{"x": x, "y": y})
		time.Sleep(500 * time.Millisecond)
		st = get(u)
		fmt.Printf("第 %d 次：注入 (%d,%d) ⇒ level=%d hover=%d（要 ≥1 才点得着）\n",
			attempt, x, y, st.Level, st.Hover)
		if st.Level >= 1 {
			clicked = true
			break
		}
	}
	if !clicked {
		fmt.Println("⛔ 试了 6 次也没把细条抬到第②级 —— 多半是 ACE 窗口一直在动；放弃这次点击")
		os.Exit(1)
	}
	x = st.Rect.Right - int32(15*float64(st.DPI)/96)
	y = st.Rect.Top + int32(6*float64(st.DPI)/96) + int32(*it)*int32(st.ItemH) + int32(st.ItemH)/2

	// ⚠️ 细条第五轮起是 ACE 窗口的**子窗口** ⇒ `EnumWindows`（`win.FindPrefixed`）**找不到它**
	// （那只枚举顶层窗口）⇒ 句柄只能从 `/api/rail` 要（exe 自己报的 hwnd）。
	h := st.HWND
	if h == 0 || !win.Alive(h) {
		rail, _ := win.FindExact("ACETool 细条")
		if len(rail) == 0 {
			rail, _ = win.FindPrefixed("ACETool 细条")
		}
		if len(rail) == 0 {
			fmt.Println("拿不到细条句柄（`/api/rail` 的 hwnd 是 0x0，标题查找也没找到 —— 子窗口本就不在顶层列表里）")
			os.Exit(1)
		}
		h = rail[0].Handle
	}
	r, _ := win.RectOf(h)
	clientY := y - r.Top
	before := snap()
	fmt.Printf("往细条 0x%x 合成点击：客户区 (5,%d)\n", h, clientY)
	if err := win.SendClick(h, 5, clientY); err != nil {
		fmt.Println("合成点击失败：" + err.Error())
		os.Exit(1)
	}

	time.Sleep(time.Duration(*wait) * time.Second)
	after := snap()
	var news []string
	for k, v := range after {
		if _, had := before[k]; !had {
			news = append(news, fmt.Sprintf("  + 0x%x %s", k, v))
		}
	}
	sort.Strings(news)
	fmt.Printf("\n点完 %d 秒：新出现 %d 个窗口", *wait, len(news))
	if len(news) == 0 {
		fmt.Println("（一个都没有 ⇒ 这项要么跑了工具、要么被拒了；看细条自己那句话）")
	} else {
		fmt.Println("：")
		for _, n := range news {
			fmt.Println(n)
		}
	}
	st2 := get(u)
	fmt.Printf("细条状态：note=「%s」 · 参数条 panelItem=%d panelTier=%d panelHwnd=0x%x panelRect=%d,%d-%d,%d\n",
		st2.Note, st2.PanelItem, st2.PanelTier, st2.PanelHWND,
		st2.PanelRect.Left, st2.PanelRect.Top, st2.PanelRect.Right, st2.PanelRect.Bottom)

	// `-panel x,y`：往**参数条**上按客户区坐标合成一次点击（验滑块 / 按钮；不用人去点）
	if *panelAt != "" {
		var px, py int
		fmt.Sscanf(*panelAt, "%d,%d", &px, &py)
		if st2.PanelHWND == 0 {
			fmt.Println("参数条没开着，没法点它（先点一下细条上那一项）")
		} else if err := win.SendClick(st2.PanelHWND, int32(px), int32(py)); err != nil {
			fmt.Println("往参数条点击失败：" + err.Error())
		} else {
			fmt.Printf("往参数条 0x%x 合成点击：客户区 (%d,%d)\n", st2.PanelHWND, px, py)
			time.Sleep(1200 * time.Millisecond)
			st3 := get(u)
			fmt.Printf("点完：panelItem=%d panelTier=%d note=「%s」\n", st3.PanelItem, st3.PanelTier, st3.Note)
		}
	}

	if !*keep {
		post(u, "api/rail/cursor", map[string]any{"clear": true})
	}
}
