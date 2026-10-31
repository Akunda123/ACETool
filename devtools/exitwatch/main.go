// exitwatch —— 盯着某个进程，**它自己死的时候**把退出码打出来（不动它、不杀它）。
//
// 为什么不用 PowerShell：`Get-Process | WaitForExit()` 那条路实测**拿不到退出码**
// （对 WMI 起的 detached 进程，PS 的 Process 对象上 `WaitForExit` 往往立刻返回、`ExitCode` 是空的），
// 而我上次"拿到退出码"的那两次都是**我自己 Stop-Process**杀的 —— 无效证据。
//
// 这里直接开句柄：`OpenProcess(SYNCHRONIZE|QUERY_LIMITED_INFORMATION)` → `WaitForSingleObject` →
// `GetExitCodeProcess`。它只**等**，不碰目标进程。
//
// §72 的判据速查（`acetool.exe` 这条）：
//
//	0xC0000005 = 访问违例（原生层崩，Go panic 不会是这个码）
//	0xC0000409 = 栈缓冲溢出/快速失败（`__fastfail`，常由 CRT/Qt/驱动侧触发）
//	0xC00000FD = 栈溢出
//	2          = Go 运行时的控制台事件路径（CTRL_CLOSE/注销/关机）或 abort()
//	0          = 正常退出（main 返回 / os.Exit(0)）
//
// 用法：
//
//	go run ./devtools/exitwatch                # 盯 panel.json 里那个 pid
//	go run ./devtools/exitwatch -pid 1234 -max 30m
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	pOpenProcess        = kernel32.NewProc("OpenProcess")
	pWaitForSingleObj   = kernel32.NewProc("WaitForSingleObject")
	pGetExitCodeProcess = kernel32.NewProc("GetExitCodeProcess")
	pCloseHandle        = kernel32.NewProc("CloseHandle")
)

const (
	processQueryLimitedInformation = 0x1000
	synchronize                    = 0x00100000
	infinite                       = 0xFFFFFFFF
)

func describe(code uint32) string {
	switch code {
	case 0:
		return "✅ 0 = 正常退出（main 返回 / os.Exit(0)）"
	case 2:
		return "⇒ 2 = Go 运行时的**控制台事件**那条路（CTRL_CLOSE/注销/关机）或 abort()"
	case 0xC0000005:
		return "⇒ 0xC0000005 = **访问违例**（原生层崩了；Go panic 不会是这个码）"
	case 0xC0000409:
		return "⇒ 0xC0000409 = **快速失败**（__fastfail；常由 CRT/Qt/驱动侧触发）"
	case 0xC00000FD:
		return "⇒ 0xC00000FD = 栈溢出"
	case 0xC000013A:
		return "⇒ 0xC000013A = 被 Ctrl+C 打断"
	case 1:
		return "⇒ 1 = 进程自己 exit(1)"
	default:
		return "⇒ 未收录的退出码（查 Windows 的 NTSTATUS / 应用自己的 exit 约定）"
	}
}

func main() {
	pid := flag.Int("pid", 0, "要盯的 pid（省略 = 读 `%APPDATA%\\ACETool\\panel.json`）")
	max := flag.Duration("max", 2*time.Hour, "最多等多久")
	flag.Parse()

	target := uint32(*pid)
	if target == 0 {
		b, err := os.ReadFile(filepath.Join(os.Getenv("APPDATA"), "ACETool", "panel.json"))
		if err != nil {
			fmt.Println("读不到 panel.json：" + err.Error())
			os.Exit(1)
		}
		var v struct {
			PID int    `json:"pid"`
			URL string `json:"url"`
		}
		_ = json.Unmarshal(b, &v)
		target = uint32(v.PID)
		fmt.Printf("面板：%s（pid=%d）\n", v.URL, v.PID)
	}

	h, _, err := pOpenProcess.Call(processQueryLimitedInformation|synchronize, 0, uintptr(target))
	if h == 0 {
		fmt.Printf("打不开 pid=%d 的句柄（它已经退出了？）：%v\n", target, err)
		os.Exit(1)
	}
	defer pCloseHandle.Call(h)

	fmt.Printf("盯着 pid=%d（最多 %v）—— 它自己死的时候这里会把退出码打出来…\n", target, *max)
	start := time.Now()

	// 等它退出（分片等，好按 max 收尾）
	deadline := time.Now().Add(*max)
	for time.Now().Before(deadline) {
		ret, _, _ := pWaitForSingleObj.Call(h, 2000) // 2s 一片
		if uint32(ret) == 0 {                        // WAIT_OBJECT_0 = 退出了
			var code uint32
			pGetExitCodeProcess.Call(h, uintptr(unsafe.Pointer(&code)))
			alive := time.Since(start)
			fmt.Printf("\n⏱ 它退出了（活了 %s）\n退出码 = %d (0x%08X)\n%s\n",
				alive.Round(time.Second), code, code, describe(code))
			return
		}
	}
	fmt.Printf("等满 %v 还没退出 ⇒ 这段时间里它没死（§72 没露头）。\n", *max)
}
