//go:build windows

package win

import "testing"

// TestListDoesNotLeakCallbacks 是 §72 那条 bug 的**回归测试**（2026-10-17 定位）。
//
// 病史：`enumWindows` 曾经**每调一次**就 `syscall.NewCallback` 建一个回调，而 Go 的 Windows 回调表是
// **定长（2000）且永不释放**（`syscall.NewCallback` 文档原话：the function is never freed）⇒
// 调满两千次，Go 直接 `throw("too many callback functions")` ⇒ **exit 2**，
// 没有栈、没有日志（detached 进程的 stderr 谁也看不见）⇒ 表现就是"exe 自己没了、什么都不留"。
// 侧条每个 tick（120ms）都要 `List()` 一次来找 ACE 窗口 ⇒ **约 3 分 40 秒**必死，
// 这就是长期挂着的 §72「带侧条之后 exe 会静默退出」。
//
// 这条测试按**超过上限**的次数来跑：
//   - 回调缓存好了（现在的写法）⇒ 几千次随便跑，几秒钟过；
//   - 一旦退回"每次新建"⇒ 跑到约第 2000 次时**整个测试进程被 Go 打死** ⇒ `go test` 报 FAIL。
//
// ⚠️ 别把它加到 `-short` 里跑（要几秒），也别减到 2000 以下（那正好绕过上限、测不出问题）。
func TestListDoesNotLeakCallbacks(t *testing.T) {
	if testing.Short() {
		t.Skip("2500 次枚举要几秒；-short 跳过")
	}
	for i := 0; i < 2500; i++ {
		if _, err := List(); err != nil {
			t.Fatalf("第 %d 次 List() 失败：%v", i+1, err)
		}
	}
}

// TestZOrderDoesNotLeakCallbacks 同上，但走 `ZOrder()`（那条路**含本进程**窗口，是另一个入口）。
// 两条分开测：`includeOwn` 的分支要各自过一遍，免得只修了一条。
func TestZOrderDoesNotLeakCallbacks(t *testing.T) {
	if testing.Short() {
		t.Skip("2500 次枚举要几秒；-short 跳过")
	}
	for i := 0; i < 2500; i++ {
		if _, err := ZOrder(); err != nil {
			t.Fatalf("第 %d 次 ZOrder() 失败：%v", i+1, err)
		}
	}
}
