// panelcheck —— 参数条（细条上「连接」/「音素」弹出来的那块）的自检。
//
// 它验的是**这条链子**（每一步都可能悄悄坏）：
//
//	① 点细条上那一项 ⇒ `/api/rail` 报 `panelItem == 那一项`、`panelHwnd != 0`、`panelRect` 在细条左边；
//	② 点击滑块 ⇒ `panelTier` 变（说明鼠标消息真的进到了那块窗口、档位算得对）；
//	③ 音素那条「查找」空着点「替换」⇒ 被挡（`note` 里明说），**不跑工具**（工具是真写，不能误触）；
//	④ 收起（再点细条上同一项 = 切换）⇒ `panelItem == -1`。
//
// 为什么用合成点击而不是人去点：这台机器用户在用，脚本不该抢鼠标；三级/行映射那套假光标机制
// （`POST /api/rail/cursor`）已经把"点得着"变成确定性的了。
//
// 用法：go run ./devtools/panelcheck        （⚠️ 需要 ACE 开着：细条要贴得上）
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"acetool/internal/win"
)

const apiHeader = "X-ACETool"

type state struct {
	Running     bool     `json:"running"`
	Visible     bool     `json:"visible"`
	Level       int      `json:"level"`
	Hover       int      `json:"hover"`
	ItemH       int      `json:"itemH"`
	DPI         int      `json:"dpi"`
	HWND        uintptr  `json:"hwnd"`
	Rect        win.Rect `json:"rect"`
	PanelItem   int      `json:"panelItem"`
	PanelHWND   uintptr  `json:"panelHwnd"`
	PanelRect   win.Rect `json:"panelRect"`
	PanelAnchor win.Rect `json:"panelAnchor"` // 开条那一刻细条的矩形（拿它核"摆在左边"，别拿现在的 rect）
	PanelTier   int      `json:"panelTier"`
	PanelText   []string `json:"panelText"`
	Note        string   `json:"note"`
}

var bad int

func ok(name string, cond bool, extra string) {
	if cond {
		fmt.Println("  ok   " + name + "  — " + extra)
	} else {
		fmt.Println("  FAIL " + name + "  — " + extra)
		bad++
	}
}

func base() string {
	b, err := os.ReadFile(filepath.Join(os.Getenv("APPDATA"), "ACETool", "panel.json"))
	if err != nil {
		fmt.Println("读不到 panel.json（面板没起？）：" + err.Error())
		os.Exit(1)
	}
	var v struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(b, &v)
	return strings.TrimRight(v.URL, "/") + "/"
}

func get(u string) state {
	r, err := http.Get(u + "api/rail")
	if err != nil {
		fmt.Println("问不到 /api/rail：" + err.Error())
		os.Exit(1)
	}
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	var s state
	_ = json.Unmarshal(body, &s)
	return s
}

func post(u, path string, v any) {
	b, _ := json.Marshal(v)
	req, _ := http.NewRequest("POST", u+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(apiHeader, "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("POST " + path + " 失败：" + err.Error())
		os.Exit(1)
	}
	_ = resp.Body.Close()
}

// armRow 把假光标停在细条第 i 行的中线上，并**确认级别抬到 ≥1**（点得着）。
//
// ⚠️ 必须按**当下最新**的 rect 算注入点并重试：ACE 被还原/移动时细条的 rect 会在"读状态"和
// "注入"之间变，按旧矩形算的点会落到细条外面 ⇒ 级别回 0 ⇒ 点击被合理拒绝（踩过好几次）。
func armRow(u string, item int) (state, bool) {
	for attempt := 0; attempt < 6; attempt++ {
		s := get(u)
		if !s.Visible || s.ItemH == 0 {
			return s, false
		}
		scale := float64(s.DPI) / 96
		pad := int32(6 * scale)
		y := s.Rect.Top + pad + int32(item)*int32(s.ItemH) + int32(s.ItemH)/2
		x := s.Rect.Right - int32(15*scale)
		if x < 0 || y < 0 {
			time.Sleep(400 * time.Millisecond)
			continue
		}
		post(u, "api/rail/cursor", map[string]any{"x": x, "y": y})
		time.Sleep(500 * time.Millisecond)
		s = get(u)
		if s.Level >= 1 && s.Hover == item {
			return s, true
		}
	}
	return get(u), false
}

// rowOf 按**全称**查它在细条上的行号。
//
// ⚠️ 不要硬编码行号：细条的顺序是**用户的**（2026-10-17「把删除挪到前面去」把「连接」从第 1 行挤到了第 2 行，
// 直接把写死的 0/8 全打翻）。名字才是稳定的锚。
func rowOf(u, full string) int {
	r, err := http.Get(u + "api/rail/items")
	if err != nil {
		return -1
	}
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	var v struct {
		Items []struct {
			Full string `json:"Full"`
		} `json:"items"`
	}
	_ = json.Unmarshal(body, &v)
	for i, it := range v.Items {
		if it.Full == full {
			return i
		}
	}
	return -1
}

// clickRow 在细条第 item 行上合成一次点击（先 arm 再点）。right=true 时合成**右键**。
func clickRow(u string, item int, right bool) bool {
	s, armed := armRow(u, item)
	if !armed {
		return false
	}
	r, err := win.RectOf(s.HWND)
	if err != nil {
		return false
	}
	scale := float64(s.DPI) / 96
	pad := int32(6 * scale)
	y := s.Rect.Top + pad + int32(item)*int32(s.ItemH) + int32(s.ItemH)/2
	if right {
		return win.SendRightClick(s.HWND, 5, y-r.Top) == nil
	}
	return win.SendClick(s.HWND, 5, y-r.Top) == nil
}

// ensurePanel 反复"点细条第 item 行 + 等"直到参数条真的开到那一项（或放弃）。right=true 时用右键开。
//
// 为什么要重试：细条的点击有 **400ms 去抖**（真写工具不能连点），而"开哪一项"是**切换**语义
// （点同一项 = 收起）⇒ 状态不对时盲点一次可能正好把刚开的关掉。所以每一步都要**看着结果点**。
func ensurePanel(u string, item int, right bool) bool {
	for attempt := 0; attempt < 4; attempt++ {
		if get(u).PanelItem == item {
			return true
		}
		if cur := get(u).PanelItem; cur >= 0 && cur != item {
			// 先收起当前那个（用同样的键：右键开的就用右键收，免得左键顺手把工具跑了）
			clickRow(u, cur, right)
			time.Sleep(700 * time.Millisecond)
			if get(u).PanelItem < 0 {
				continue
			}
		}
		if !clickRow(u, item, right) {
			return false
		}
		time.Sleep(800 * time.Millisecond)
	}
	return get(u).PanelItem == item
}

func main() {
	win.SetDPIAware()
	u := base()
	fmt.Println("面板：" + u)

	s := get(u)
	if !s.Running || !s.Visible {
		fmt.Println("  细条没在跑/没显示（ACE 开着吗？）—— go run ./devtools/acefocus 再试")
		os.Exit(1)
	}
	// 行号**按全称查**（细条的顺序归用户管，硬编码会被"把某项挪到前面"打翻）
	rowLian := rowOf(u, "连接音符")
	rowSu := rowOf(u, "批量替换音素")
	if rowLian < 0 || rowSu < 0 {
		fmt.Println("  FAIL 清单里找不到「连接音符」或「批量替换音素」")
		os.Exit(1)
	}
	// 从干净状态开始：先把可能开着的参数条收起来
	if s.PanelItem >= 0 {
		clickRow(u, s.PanelItem, s.PanelItem == rowLian) // 连接那一项要用右键（左键 = 直接跑）
		time.Sleep(500 * time.Millisecond)
	}

	// ---------- ① 连接（tier）：**右键** ⇒ 参数条弹出来 ----------
	// ⚠️ 用户 2026-10-17「连接这个，我想做成点按钮就用，参数只是附带」⇒ 左键是**真的跑工具**（写工程），
	// 自检**绝不能**去点它（上次误触的教训）。所以这里只验右键这条路。
	fmt.Println("\n== 连接音符（左键=直接跑 · 右键=调阈值）==")
	if !ensurePanel(u, rowLian, true) {
		fmt.Println("  FAIL 右键「连接」没能打开参数条")
		bad++
	} else {
		s = get(u)
		ok("右键「连接」⇒ 参数条打开", s.PanelItem == rowLian && s.PanelHWND != 0,
			fmt.Sprintf("panelItem=%d hwnd=0x%x", s.PanelItem, s.PanelHWND))
		// ⚠️ 用 `panelAnchor`（开条那一刻细条的矩形）而不是 `rect`（现在）：用户拖/缩 ACE 窗口时
		// 细条会跟着动，拿"现在的 rect"去核"当时的摆放"会假报错（真机踩到过一次）。
		ok("参数条摆在**细条左边**", s.PanelRect.Right <= s.PanelAnchor.Left+2 && s.PanelRect.Right > 0,
			fmt.Sprintf("panel 右边=%d 锚(开条时细条左边)=%d", s.PanelRect.Right, s.PanelAnchor.Left))
		ok("默认档位 = 不设限(0)", s.PanelTier == 0, fmt.Sprintf("panelTier=%d", s.PanelTier))

		// ---------- ② 点滑块中段（60% 处）⇒ 档位变 ----------
		if s.PanelHWND != 0 {
			// 滑轨在参数条客户区里：左边 pad(15)、右边留 96*1.5=144 给档位名；y 在标题+说明下面
			x := int32(15 + (480-15-144)*6/10)
			y := int32(15 + 30 + 6 + 24 + 12 + 21)
			ok("往滑块中点合成一次点击", win.SendClick(s.PanelHWND, x, y) == nil, fmt.Sprintf("(%d,%d)", x, y))
			time.Sleep(600 * time.Millisecond)
			s2 := get(u)
			ok("点击滑块 ⇒ 档位变了（不是 0）", s2.PanelTier > 0, fmt.Sprintf("panelTier=%d", s2.PanelTier))
		}
	}

	// ---------- ③ 音素：空「查找」点「替换」⇒ 被挡，不跑工具 ----------
	fmt.Println("\n== 批量替换音素（两个输入框）==")
	if !ensurePanel(u, rowSu, false) {
		fmt.Println("  FAIL 参数条没能开到「音素」那一项")
		bad++
	} else {
		s = get(u)
		ok("点「音素」⇒ 参数条打开、两个框都在", s.PanelItem == rowSu && len(s.PanelText) == 2,
			fmt.Sprintf("panelItem=%d panelText=%v", s.PanelItem, s.PanelText))
		if s.PanelHWND != 0 {
			// 「替换」按钮：在第二个输入框下面（pad 15 + 标题 30+6 + 说明 24+12 + 两行 (42+9)*2 + 半个按钮高）
			runX, runY := int32(70), int32(15+30+6+24+12+2*(42+9)+19)
			if err := win.SendClick(s.PanelHWND, runX, runY); err == nil {
				time.Sleep(900 * time.Millisecond)
				s3 := get(u)
				ok("「查找」空着点「替换」⇒ 明说先填、且**不跑工具**",
					s3.PanelItem == -1 && strings.Contains(s3.Note, "先在参数条里填"),
					"panelItem="+fmt.Sprint(s3.PanelItem)+" note=「"+s3.Note+"」")
			} else {
				ok("点「替换」按钮", false, err.Error())
			}
		}
	}

	// ---------- ④ 再点同一项 = 收起（音素用左键；连接那条只用右键，别把它跑起来）----------
	fmt.Println("\n== 切换收起 ==")
	if s := get(u); s.PanelItem >= 0 {
		clickRow(u, s.PanelItem, s.PanelItem == rowLian)
		time.Sleep(600 * time.Millisecond)
		s4 := get(u)
		ok("再点同一项 ⇒ 参数条收起", s4.PanelItem == -1, fmt.Sprintf("panelItem=%d", s4.PanelItem))
	}
	post(u, "api/rail/cursor", map[string]any{"clear": true}) // 收尾：把假光标取消

	fmt.Println()
	if bad == 0 {
		fmt.Println("全部通过")
	} else {
		fmt.Printf("%d 项失败\n", bad)
		os.Exit(1)
	}
}
