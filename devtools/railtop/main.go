// 孤立验证：**置顶能不能钉在细条那个窗口上**（不动细条自己的逻辑）。
//
// 为什么要有它：用户 2026-10-17 报「现在显示在钢琴窗的下面了」⇒ 改成"ACE 前台时自己置顶"，
// 但 `/api/rail` 读回来 `aceForeground=true` 而 `topMost=false` ⇒ 像是 `SetWindowPos(HWND_TOPMOST)`
// 没生效。这里绕开细条自己的状态机，直接用 §71 里那套（`win.SetTopMost`）钉一下再读回来，
// 就能分清"是 API 不行"还是"细条自己把它清掉了"。
//
// 用法：go run ./devtools/railtop
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"acetool/internal/win"
)

func main() {
	b, err := os.ReadFile(filepath.Join(os.Getenv("APPDATA"), "ACETool", "panel.json"))
	if err != nil {
		fmt.Println("读不到 panel.json：" + err.Error())
		os.Exit(1)
	}
	var v struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(b, &v)

	// 细条的句柄从 `/api/rail` 拿（它就是自己报的）
	resp, err := http.Get(v.URL + "api/rail")
	if err != nil {
		fmt.Println("问不到 /api/rail：" + err.Error())
		os.Exit(1)
	}
	defer resp.Body.Close()
	var st struct {
		Running   bool    `json:"running"`
		HWND      uintptr `json:"hwnd"`
		Visible   bool    `json:"visible"`
		TopMost   bool    `json:"topMost"`
		TopCall   int     `json:"topCall"`
		TopRaw    int     `json:"topRaw"`
		WidgetTip uintptr `json:"tipHwnd"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&st)
	if !st.Running || st.HWND == 0 {
		fmt.Println("细条没在跑（先 POST /api/rail {enable:true}）")
		os.Exit(1)
	}
	fmt.Printf("细条 0x%x · 它自己报：visible=%v topMost=%v topCall=%d topRaw=0x%x\n",
		st.HWND, st.Visible, st.TopMost, st.TopCall, uint32(st.TopRaw))

	fmt.Printf("外部读（win.IsTopMost）：%v\n", win.IsTopMost(st.HWND))
	fmt.Println("用 win.SetTopMost(true) 钉一下…")
	if err := win.SetTopMost(st.HWND, true); err != nil {
		fmt.Println("  SetTopMost 报错：" + err.Error())
	} else {
		fmt.Printf("  钉完**立刻**回读（不 sleep）：%v\n", win.IsTopMost(st.HWND))
		time.Sleep(30 * time.Millisecond)
		fmt.Printf("  30ms 后：%v\n", win.IsTopMost(st.HWND))
		time.Sleep(200 * time.Millisecond)
		fmt.Printf("  230ms 后：%v\n", win.IsTopMost(st.HWND))
	}

	// ⭐ 关键假设：**细条自己那次"重新摆位"（`SetWindowPos(…, SWP_NOZORDER)`）把置顶清掉了**。
	//    如果是它，那修法就是"摆位时别带 SWP_NOZORDER，改成显式把 hWndInsertAfter 传对"。
	fmt.Println("再钉一次，再用 SWP_NOZORDER 摆一次（细条每 120ms 就是这么干的），看置顶还在不在…")
	_ = win.SetTopMost(st.HWND, true)
	before := win.IsTopMost(st.HWND)
	if err := win.Move(st.HWND, win.Rect{Left: 0, Top: 0, Right: 0, Bottom: 0}); err == nil {
		// win.Move 用的是 SWP_NOZORDER + 真实坐标；这里只想知道 z 序位会不会被它清掉，
		// 坐标传 0 会被夹回原处（它内部按传进来的矩形摆），所以只当"调用一次"用。
	}
	_ = before
	time.Sleep(150 * time.Millisecond)
	fmt.Printf("  走完一次 SWP_NOZORDER 摆位后：%v\n", win.IsTopMost(st.HWND))

	// 等两个跟随周期（细条每 120ms 会自己 SetWindowPos 一次），看它会不会把置顶清掉
	time.Sleep(700 * time.Millisecond)
	fmt.Printf("  等 700ms（细条自己动过几次）后回读：%v  ← false 就说明**是细条自己把它清掉的**\n",
		win.IsTopMost(st.HWND))

	// 复位
	_ = win.SetTopMost(st.HWND, false)
}
