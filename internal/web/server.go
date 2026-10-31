// Package web 起一个只绑 127.0.0.1 的本地面板。
//
// 选型理由（HANDOVER.md §1）：进程内起随机端口 + 系统浏览器 ⇒ 不用背 Chromium（省掉 Electron 那 180 MB），
// 前端 HTML/JS 用 go:embed 进单文件 exe。
package web

import (
	"embed"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"
)

//go:embed all:assets
var assets embed.FS

// Assets 返回面板静态文件（`home.html` 是主页面；`index.html` 是老壳页，已不是入口）。
func Assets() (fs.FS, error) { return fs.Sub(assets, "assets") }

// Start 监听 127.0.0.1 上的空闲端口 —— 不占固定端口，也不对局域网暴露。
//
// ⭐ 用户 2026-10-24：「② 预览失败：Failed to fetch」——**exe 每次重启换端口**，
// 之前开着的子窗口（找到的/音高线/…）连的还是旧端口，一按按钮就 fetch 失败。
// 现在**尽量沿用上次的端口**（preferPort > 0 且没被占），重启后老窗口继续能用。
func Start(h http.Handler, preferPort int) (*http.Server, string, error) {
	var ln net.Listener
	var err error
	if preferPort > 0 {
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferPort))
	}
	if ln == nil {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return nil, "", err
	}
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	return srv, "http://" + ln.Addr().String() + "/", nil
}

// Open 用系统默认浏览器打开面板。
//
// Windows 上刻意**不用** `cmd /c start`：那要经过 shell、还要跟引号斗（正是 §3.3-1 警告的那类坑）。
// rundll32 的 FileProtocolHandler 直接拿 URL 当参数，不经 shell。
func Open(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
