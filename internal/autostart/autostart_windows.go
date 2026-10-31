//go:build windows

package autostart

// 「开机启动」（用户 2026-10-17：「设置里增加一个开机启动的选项」）。
//
// 做法：写 **`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`** 下一个名叫 `ACETool` 的值
// （REG_SZ = `"<exe 路径>" <参数>`）。为什么是它、不是别的：
//   · 只动**当前用户**（HKCU），**不需要管理员权限**、不动系统级配置；
//   · 和"启动文件夹里的快捷方式"等价，但不用造 `.lnk`（那要走 COM，我们零依赖）；
//   · 不引计划任务（`schtasks` 那套要管理员、而且开机 vs 登录的语义更绕）。
//
// ⚠️ 三条口径：
//  1. **注册表就是唯一事实源**：不在 `config.json` 里另存一个布尔 —— 两份状态必然漂
//     （用户可能在「任务管理器 → 启动」里关掉它，那时我们的配置还写着"开"）。
//  2. 值里的路径是**写那一刻的 exe 路径**。exe 换位置后旧值会指着老路径 ⇒ `Status()` 会把
//     "注册表里写的"和"现在跑的这个"一起报出来，界面据此提示"要重新勾一下"。
//  3. 启动时**不弹面板窗口**（带 `-no-browser`）：登录就弹一个窗口太打扰；托盘图标在那儿，
//     要用点它。想改成"开机就把面板开出来"，把 `argNoBrowser` 去掉即可。
import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

const (
	runKey    = `Software\Microsoft\Windows\CurrentVersion\Run`
	valueName = "ACETool"

	hkeyCurrentUser   = 0x80000001
	keyQueryValue     = 0x0001
	keySetValue       = 0x0002
	regSZ             = 1
	errorFileNotFound = 2

	// argNoBrowser：开机启动时**不开**面板窗口（托盘图标在那儿，要用再点）。
	argNoBrowser = "-no-browser"
)

var (
	advapi32         = syscall.NewLazyDLL("advapi32.dll")
	pRegOpenKeyEx    = advapi32.NewProc("RegOpenKeyExW")
	pRegCreateKeyEx  = advapi32.NewProc("RegCreateKeyExW")
	pRegQueryValueEx = advapi32.NewProc("RegQueryValueExW")
	pRegSetValueEx   = advapi32.NewProc("RegSetValueExW")
	pRegDeleteValue  = advapi32.NewProc("RegDeleteValueW")
	pRegCloseKey     = advapi32.NewProc("RegCloseKey")
)

// State 是"开机启动"的现场。
type State struct {
	Supported bool   `json:"supported"`     // 非 Windows 恒 false
	Enabled   bool   `json:"enabled"`       // 注册表里有这个值，且**指向现在这个 exe**
	Stale     bool   `json:"stale"`         // 有值但指向别的路径（exe 挪过位置）
	Exe       string `json:"exe"`           // 现在跑的这个 exe
	Cmd       string `json:"cmd,omitempty"` // 注册表里现在写的那条命令
	Want      string `json:"want"`          // 勾上之后会写成什么
	Note      string `json:"note,omitempty"`
}

// ExePath 返回当前 exe 的绝对路径（`os.Executable()` 会解掉符号链接）。
func ExePath() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}

// Cmd 是"勾上之后注册表里应该写的那条命令"。
func Cmd() string {
	exe := ExePath()
	if exe == "" {
		return ""
	}
	return fmt.Sprintf("%q %s", exe, argNoBrowser)
}

// Status 读现场（只读，不写注册表）。
func Status() (State, error) {
	s := State{Supported: true, Exe: ExePath(), Want: Cmd()}
	cur, err := readValue()
	if err != nil {
		return s, err
	}
	s.Cmd = cur
	if cur == "" {
		return s, nil
	}
	s.Enabled = sameCmd(cur, s.Want)
	s.Stale = !s.Enabled
	if s.Stale {
		s.Note = "注册表里那条指向的是**另一个位置**的 exe（换过目录/重装过？）⇒ 重新勾一下就会改成现在这个"
	}
	return s, nil
}

// sameCmd 比较两条启动命令是否等价（大小写不敏感、忽略多余空格 —— Windows 路径不区分大小写）。
func sameCmd(a, b string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(a), " "), strings.Join(strings.Fields(b), " "))
}

// Enable 写注册表（幂等）。写的就是 `Cmd()`。
func Enable() error {
	cmd := Cmd()
	if cmd == "" {
		return fmt.Errorf("读不到自己的 exe 路径，没法写开机启动")
	}
	return writeValue(cmd)
}

// Disable 删掉那个值（本来就没有也算成功）。
func Disable() error {
	return deleteValue()
}

// Set 按 wanted 开关。
func Set(wanted bool) (State, error) {
	if wanted {
		if err := Enable(); err != nil {
			return State{Supported: true, Exe: ExePath(), Want: Cmd()}, err
		}
	} else {
		if err := Disable(); err != nil {
			return State{Supported: true, Exe: ExePath(), Want: Cmd()}, err
		}
	}
	return Status()
}

func openRunKey(access uint32) (syscall.Handle, error) {
	sub, err := syscall.UTF16PtrFromString(runKey)
	if err != nil {
		return 0, err
	}
	var h syscall.Handle
	r, _, _ := pRegOpenKeyEx.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(sub)), 0, uintptr(access), uintptr(unsafe.Pointer(&h)))
	if r != 0 {
		return 0, fmt.Errorf("打不开 Run 键（错误码 %d）", r)
	}
	return h, nil
}

func createRunKey() (syscall.Handle, error) {
	sub, err := syscall.UTF16PtrFromString(runKey)
	if err != nil {
		return 0, err
	}
	var h syscall.Handle
	var disp uint32
	r, _, _ := pRegCreateKeyEx.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(sub)), 0, 0, 0,
		uintptr(keyQueryValue|keySetValue), 0, uintptr(unsafe.Pointer(&h)), uintptr(unsafe.Pointer(&disp)))
	if r != 0 {
		return 0, fmt.Errorf("建/开 Run 键失败（错误码 %d）", r)
	}
	return h, nil
}

func readValue() (string, error) {
	h, err := openRunKey(keyQueryValue)
	if err != nil {
		// Run 键正常一定存在；打不开就**如实返回**（上层会把 Supported 留着、Enabled=false）
		return "", err
	}
	defer pRegCloseKey.Call(uintptr(h))
	name, _ := syscall.UTF16PtrFromString(valueName)
	buf := make([]uint16, 1024)
	size := uint32(len(buf) * 2)
	var typ uint32
	r, _, _ := pRegQueryValueEx.Call(uintptr(h), uintptr(unsafe.Pointer(name)), 0,
		uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == errorFileNotFound {
		return "", nil // 没这个值 = 没开
	}
	if r != 0 {
		return "", fmt.Errorf("读 Run\\%s 失败（错误码 %d）", valueName, r)
	}
	if typ != regSZ {
		return "", fmt.Errorf("Run\\%s 的类型不是字符串（type=%d）—— 有点奇怪，先别动它", valueName, typ)
	}
	return syscall.UTF16ToString(buf), nil
}

func writeValue(cmd string) error {
	h, err := createRunKey()
	if err != nil {
		return err
	}
	defer pRegCloseKey.Call(uintptr(h))
	name, _ := syscall.UTF16PtrFromString(valueName)
	data, err := syscall.UTF16FromString(cmd)
	if err != nil {
		return err
	}
	r, _, _ := pRegSetValueEx.Call(uintptr(h), uintptr(unsafe.Pointer(name)), 0, regSZ,
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2))
	if r != 0 {
		return fmt.Errorf("写 Run\\%s 失败（错误码 %d）", valueName, r)
	}
	return nil
}

func deleteValue() error {
	h, err := openRunKey(keySetValue)
	if err != nil {
		return err
	}
	defer pRegCloseKey.Call(uintptr(h))
	name, _ := syscall.UTF16PtrFromString(valueName)
	r, _, _ := pRegDeleteValue.Call(uintptr(h), uintptr(unsafe.Pointer(name)))
	if r == errorFileNotFound {
		return nil // 本来就没有
	}
	if r != 0 {
		return fmt.Errorf("删 Run\\%s 失败（错误码 %d）", valueName, r)
	}
	return nil
}
