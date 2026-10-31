// 把 ACE 的窗口恢复/唤到前台（自检用）。
//
// 为什么需要它：exe 侧条的**三级**要 ACE 窗口可见才能验（ACE 最小化时细条会正确隐藏）。
// 有它就不必让用户手动去点 ACE；`win.Focus` 会先 `SW_RESTORE` 再抢前台（前台锁失败会如实返回）。
//
// 用法：go run ./devtools/acefocus [进程名，默认 "ACE Studio.exe"]
package main

import (
	"fmt"
	"os"
	"time"

	"acetool/internal/win"
)

func main() {
	exe := "ACE Studio.exe"
	if len(os.Args) > 1 {
		exe = os.Args[1]
	}
	pids, err := win.ByExeName(exe)
	if err != nil || len(pids) == 0 {
		fmt.Printf("没找到进程 %q（%v）\n", exe, err)
		os.Exit(1)
	}
	all, err := win.List()
	if err != nil {
		fmt.Println("列窗口失败：" + err.Error())
		os.Exit(1)
	}
	n := 0
	for _, w := range all {
		for _, p := range pids {
			if w.PID != p {
				continue
			}
			n++
			was := win.Minimized(w.Handle)
			okFocus, err := win.Focus(w.Handle)
			fmt.Printf("0x%x 「%s」 最小化=%v ⇒ Focus=%v %v\n", w.Handle, w.Title, was, okFocus, err)
			time.Sleep(300 * time.Millisecond)
		}
	}
	if n == 0 {
		fmt.Printf("进程 %v 没有可见顶层窗口（可能整体最小化在托盘里）\n", pids)
		os.Exit(1)
	}
}
