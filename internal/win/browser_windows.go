//go:build windows

package win

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

// 默认浏览器这条路要读注册表（Go 标准库没有注册表 API，就自己包一层 advapi32）。
var (
	advapi32          = syscall.NewLazyDLL("advapi32.dll")
	pRegOpenKeyExW    = advapi32.NewProc("RegOpenKeyExW")
	pRegQueryValueExW = advapi32.NewProc("RegQueryValueExW")
	pRegCloseKey      = advapi32.NewProc("RegCloseKey")
)

const (
	hkeyClassesRoot = 0x80000000
	hkeyCurrentUser = 0x80000001
	keyRead         = 0x20019
)

// DefaultBrowserExe 读"默认浏览器的可执行文件"。
//
// 为什么需要它：面板每个页面各是一个浏览器窗口（📌 置顶按窗口认），而
// `ShellExecuteW` 只会**在已有窗口里开标签页** ⇒ 想"新开一个窗口"只能自己拉起浏览器 exe。
// 两步：`HKCU\...\FileExts\.html\UserChoice` 的 ProgId → `HKCR\<ProgId>\shell\open\command`。
//
// ⚠️ 读不到就**如实报错**（调用方退回 ShellExecuteW，并在提示里说明那是标签页）。
func DefaultBrowserExe() (string, error) {
	prog, err := regString(hkeyCurrentUser,
		`Software\Microsoft\Windows\CurrentVersion\Explorer\FileExts\.html\UserChoice`, "ProgId")
	if err != nil {
		return "", err
	}
	cmd, err := regString(hkeyClassesRoot, prog+`\shell\open\command`, "")
	if err != nil {
		return "", err
	}
	exe := firstExe(cmd)
	if exe == "" {
		return "", fmt.Errorf("默认浏览器（%s）的命令行里没认出 exe 路径：%q", prog, cmd)
	}
	return exe, nil
}

// RunDetached 起一个**不影响我们**的子进程（不等它、不占我们的控制台）。
func RunDetached(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release() // 不 Release 会留个僵尸句柄在 Go 这边
}

func regString(root uintptr, path, name string) (string, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", err
	}
	var h uintptr
	if ret, _, _ := pRegOpenKeyExW.Call(root, uintptr(unsafe.Pointer(p)), 0, keyRead,
		uintptr(unsafe.Pointer(&h))); ret != 0 {
		return "", fmt.Errorf("注册表打不开 %s（错误码 %d）", path, ret)
	}
	defer pRegCloseKey.Call(h)
	buf := make([]uint16, 2048)
	size := uint32(len(buf) * 2)
	var typ uint32
	if ret, _, _ := pRegQueryValueExW.Call(h, uintptr(unsafe.Pointer(n)), 0,
		uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size))); ret != 0 {
		return "", fmt.Errorf("注册表读不到 %s\\%s（错误码 %d）", path, name, ret)
	}
	return syscall.UTF16ToString(buf), nil
}

// firstExe 从命令行里取可执行文件路径（注册表里那条形如
// `"C:\...\msedge.exe" --single-argument %1` 或 `C:\...\chrome.exe -- "%1"`）。
func firstExe(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	if cmd[0] == '"' {
		if i := strings.Index(cmd[1:], `"`); i > 0 {
			return cmd[1 : 1+i]
		}
		return ""
	}
	if i := strings.IndexByte(cmd, ' '); i > 0 {
		return cmd[:i]
	}
	return cmd
}
