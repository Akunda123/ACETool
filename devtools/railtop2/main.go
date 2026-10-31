// 验"细条每轮那次重新摆位会不会把置顶清掉"，并把 flags 拆开对比（`SWP_SHOWWINDOW` 是唯一的差别）。
//
// 走法：先 `SetTopMost(true)`（已证明能钉住），再分别用几组 flags 调 `SetWindowPos`，每次都回读。
// 用法：go run ./devtools/railtop2
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"acetool/internal/win"
)

var user32 = syscall.NewLazyDLL("user32.dll")

var pSetWindowPos = user32.NewProc("SetWindowPos")

const (
	swpNoSize      = 0x0001
	swpNoMove      = 0x0002
	swpNoZOrder    = 0x0004
	swpNoActivate  = 0x0010
	swpShowWindow  = 0x0040
	hwndNotTopMost = ^uintptr(1)
)

func main() {
	b, _ := os.ReadFile(filepath.Join(os.Getenv("APPDATA"), "ACETool", "panel.json"))
	var v struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(b, &v)
	resp, err := http.Get(v.URL + "api/rail")
	if err != nil {
		fmt.Println("问不到 /api/rail：" + err.Error())
		os.Exit(1)
	}
	var st struct {
		HWND uintptr `json:"hwnd"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if st.HWND == 0 {
		fmt.Println("拿不到细条句柄")
		os.Exit(1)
	}
	h := st.HWND

	try := func(name string, after uintptr, flags uintptr) {
		// 每次都先确保它**不是**置顶，再钉成置顶，然后走一次待测调用，最后回读
		pSetWindowPos.Call(h, hwndNotTopMost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
		time.Sleep(60 * time.Millisecond)
		if err := win.SetTopMost(h, true); err != nil {
			fmt.Printf("%-46s 钉的时候报错：%v\n", name, err)
			return
		}
		before := win.IsTopMost(h)
		r, _, _ := pSetWindowPos.Call(h, after, 0, 0, 0, 0, flags)
		time.Sleep(80 * time.Millisecond)
		after2 := win.IsTopMost(h)
		verdict := "不受影响"
		if before && !after2 {
			verdict = "**被它清掉了** ←"
		}
		fmt.Printf("%-46s 调用前=%v 调用后=%v（ret=%d） %s\n", name, before, after2, r, verdict)
	}

	// 细条每轮真正用的那组：hWndInsertAfter=0 + NOZORDER + SHOWWINDOW + NOACTIVATE
	try("细条现在的摆位（0 + NOZORDER|SHOWWINDOW）", 0, swpNoZOrder|swpShowWindow|swpNoActivate)
	// 去掉 SHOWWINDOW 对比
	try("去掉 SWP_SHOWWINDOW（0 + NOZORDER）", 0, swpNoZOrder|swpNoActivate)
	// 只留 SHOWWINDOW 对比
	try("只带 SHOWWINDOW（0 + NOZORDER|SHOWWINDOW）", 0, swpNoZOrder|swpShowWindow|swpNoActivate)

	// 复位
	pSetWindowPos.Call(h, hwndNotTopMost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	_ = unsafe.Pointer(nil)
}
