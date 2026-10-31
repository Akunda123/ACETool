// 细条状态的时间线：每 300ms 打一行，看 `ticks / aceForeground / topMost / IsTopMost` 怎么变。
//
// 起因（用户 2026-10-17「现在显示在钢琴窗的下面了」）：改成"ACE 前台时自己置顶"之后，
// `/api/rail` 读回来是 `aceForeground=true` 而 `topMost=false`；但孤立验证（`devtools/railtop`）
// 证明**置顶是能钉住、也不会被 SWP_NOZORDER 摆位清掉** ⇒ 那就得看细条自己每一轮在干什么。
//
// 用法：go run ./devtools/railtimeline [-n 12] [-gap 300]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"acetool/internal/win"
)

func main() {
	n := flag.Int("n", 12, "打几行")
	gap := flag.Int("gap", 300, "间隔毫秒")
	flag.Parse()

	b, err := os.ReadFile(filepath.Join(os.Getenv("APPDATA"), "ACETool", "panel.json"))
	if err != nil {
		fmt.Println("读不到 panel.json：" + err.Error())
		os.Exit(1)
	}
	var v struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(b, &v)

	type st struct {
		Running       bool    `json:"running"`
		Visible       bool    `json:"visible"`
		HWND          uintptr `json:"hwnd"`
		Level         int     `json:"level"`
		Ticks         int     `json:"ticks"`
		AceForeground bool    `json:"aceForeground"`
		TopMost       bool    `json:"topMost"`
		TopCall       int     `json:"topCall"`
		TopRaw        int     `json:"topRaw"`
		Target        uintptr `json:"targetHwnd"`
	}
	fmt.Printf("前台句柄现在 = 0x%x · ACE 主要窗口 = ", win.Foreground())
	if pids, err := win.ByExeName("ACE Studio.exe"); err == nil {
		if all, err := win.List(); err == nil {
			for _, w := range all {
				for _, p := range pids {
					if w.PID == p {
						fmt.Printf("0x%x ", w.Handle)
					}
				}
			}
		}
	}
	fmt.Println()
	fmt.Printf("%-12s %-6s %-8s %-9s %-9s %-9s %-8s %s\n", "时刻", "ticks", "visible", "level", "aceFg", "topMost", "外部读", "topRaw/topCall")
	for i := 0; i < *n; i++ {
		resp, err := http.Get(v.URL + "api/rail")
		if err != nil {
			fmt.Println("问不到 /api/rail：" + err.Error())
			os.Exit(1)
		}
		var s st
		_ = json.NewDecoder(resp.Body).Decode(&s)
		resp.Body.Close()
		fmt.Printf("%-12s %-6d %-8v %-9d %-9v %-9v %-8v 0x%-6x %d\n",
			time.Now().Format("15:04:05.000"), s.Ticks, s.Visible, s.Level, s.AceForeground, s.TopMost,
			win.IsTopMost(s.HWND), uint32(s.TopRaw), s.TopCall)
		time.Sleep(time.Duration(*gap) * time.Millisecond)
	}
}
