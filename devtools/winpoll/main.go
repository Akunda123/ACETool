// winpoll —— 每隔 N 毫秒把**匹配某段标题**的顶层窗口打一行（含"有没有/最小化/属主/矩形"）。
//
// 为什么要它：用户报「web 闪一下就没了」（2026-10-17）—— 窗口**出现过又消失**这种症状，
// 单次快照是抓不到的（那一瞬间刚好在/不在都说得通）。这个工具就是连拍：
// 谁在什么时候消失、消失时是"没了"还是"最小化了"，一眼看得出来。
//
// 用法：go run ./devtools/winpoll -match ACETool -ms 300 -n 20
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"acetool/internal/win"
)

func main() {
	match := flag.String("match", "ACETool", "标题里含这段就算命中（大小写不敏感；空 = 全都要）")
	ms := flag.Int("ms", 300, "采样间隔（毫秒）")
	n := flag.Int("n", 20, "采样多少次")
	flag.Parse()
	// 坐标按**物理像素**报（与 winlist 一致；不声明感知会被系统虚拟化成逻辑像素）
	win.SetDPIAware()

	fmt.Printf("每 %dms 采一次、共 %d 次；匹配 %q（本进程 pid=%d）\n\n", *ms, *n, *match, os.Getpid())
	for i := 0; i < *n; i++ {
		ws, err := win.List()
		if err != nil {
			fmt.Printf("第 %2d 次：列窗口失败：%v\n", i+1, err)
			time.Sleep(time.Duration(*ms) * time.Millisecond)
			continue
		}
		var hits []string
		for _, w := range ws {
			if *match != "" && !strings.Contains(strings.ToLower(w.Title), strings.ToLower(*match)) {
				continue
			}
			tag := ""
			if m, _ := win.RectOf(w.Handle); m.Right-m.Left > 0 {
				if win.Minimized(w.Handle) {
					tag = "  [最小化]"
				}
			}
			owner := ""
			if o := win.Owner(w.Handle); o != 0 {
				owner = fmt.Sprintf("  [属主 0x%x]", o)
			}
			rc, _ := win.RectOf(w.Handle)
			hits = append(hits, fmt.Sprintf("0x%x pid=%d 「%s」 (%d,%d)-(%d,%d) %dx%d%s%s",
				w.Handle, w.PID, w.Title, rc.Left, rc.Top, rc.Right, rc.Bottom, rc.W(), rc.H(), tag, owner))
		}
		fmt.Printf("%s  第 %2d 次：%d 个命中\n", time.Now().Format("15:04:05.000"), i+1, len(hits))
		for _, h := range hits {
			fmt.Println("      " + h)
		}
		if i < *n-1 {
			time.Sleep(time.Duration(*ms) * time.Millisecond)
		}
	}
}
