package acecli

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"acetool/internal/win"
)

const cliExe = "acestudio-cli.exe"
const cliBin = "acestudio-cli"

// Locate 按 HANDOVER.md §3.1 的顺序找 CLI，返回路径与「来源」（面板上要显示从哪找到的）。
//
// 顺序：已配置 → 环境变量 ACETOOL_CLI → PATH → 平台专属（Windows 注册表 / 常见安装位置）→ 递归兜底
// 全找不到就回空串 —— 这不是错误：面板要能讲清「未检测到 ACE Studio」并让用户手选路径（§2 第一条例外）。
func Locate(configured string) (path, from string) {
	if configured != "" && fileExists(configured) {
		return configured, "配置"
	}
	if env := strings.TrimSpace(os.Getenv("ACETOOL_CLI")); env != "" && fileExists(env) {
		return env, "环境变量 ACETOOL_CLI"
	}
	if p := lookPath(); p != "" {
		return p, "PATH"
	}
	switch runtime.GOOS {
	case "windows":
		return locateWindows()
	case "darwin":
		return locateDarwin()
	default:
		return "", ""
	}
}

func lookPath() string {
	for _, name := range []string{cliExe, cliBin} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// locateDarwin ⚠️ 未验证：我们只在 Windows 核过（HANDOVER.md §7-1），
// 「ACE 的 macOS 版到底带不带 acestudio-cli」还是个未知项，这里的路径是猜的。
func locateDarwin() (string, string) {
	cands := []string{
		"/Applications/ACE Studio.app/Contents/MacOS/" + cliBin,
		"/Applications/ACE Studio.app/Contents/Resources/" + cliBin,
	}
	for _, c := range cands {
		if fileExists(c) {
			return c, "常见位置（macOS，未验证）"
		}
	}
	return "", ""
}

// locateWindows 注册表优先（最可靠，别猜路径）：
//
//	HKLM\SOFTWARE\{WOW6432Node\}Microsoft\Windows\CurrentVersion\Uninstall\*
//	  where Publisher = 'Timedomain Inc.' and DisplayName like 'ACE Studio*'
//	→ InstallLocation（实测 C:\Program Files\ACE Studio\）+ '\acestudio-cli.exe'
//
// ⚠️ DisplayName 会本地化（中文机上是「ACE Studio 版本 2.1.8」）⇒ 只按 ASCII 前缀 "ace studio" 匹配。
func locateWindows() (string, string) {
	roots := []string{
		`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`,
		`HKLM\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`,
		`HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`,
		`HKCU\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`,
	}
	for _, root := range roots {
		out, err := regQuery(root)
		if err != nil {
			continue
		}
		for _, loc := range installLocations(out) {
			p := filepath.Join(loc, cliExe)
			if fileExists(p) {
				return p, "注册表"
			}
		}
	}

	// 兜底 1：常见安装位置
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if base == "" {
			continue
		}
		p := filepath.Join(base, "ACE Studio", cliExe)
		if fileExists(p) {
			return p, "常见位置"
		}
	}

	// 兜底 2：扫 %LOCALAPPDATA%（限深，别让启动自检卡住）
	if p := scanFor(os.Getenv("LOCALAPPDATA"), cliExe, 4); p != "" {
		return p, "扫描 %LOCALAPPDATA%"
	}
	return "", ""
}

var (
	reKey  = regexp.MustCompile(`^HKEY_`)
	reName = regexp.MustCompile(`^\s+DisplayName\s+REG_SZ\s+(.*)$`)
	reLoc  = regexp.MustCompile(`^\s+InstallLocation\s+REG_SZ\s+(.*)$`)
)

// regQuery 直接 exec reg.exe（不经 shell，§3.3-1），/s 递归列出所有值。
// ⚠️ reg.exe 的输出走控制台代码页（中文机是 GBK），非 ASCII 字符会mojibake ——
// 我们只按 ASCII 前缀匹配、且解析出的路径还要过 fileExists，
// 解析失败会自然落到后面的兜底策略，不会误判。
func regQuery(key string) (string, error) {
	exe := filepath.Join(os.Getenv("SystemRoot"), "System32", "reg.exe")
	if !fileExists(exe) {
		exe = "reg.exe"
	}
	cmd := exec.Command(exe, "query", key, "/s")
	win.HideWindow(cmd) // GUI 构建下别让 reg.exe 闪黑框
	out, err := cmd.Output()
	if err != nil {
		// reg query 在「键下没有值」时回 exit 1；当没有处理。
		return "", err
	}
	return string(out), nil
}

// installLocations 按「键块」解析 reg 输出：块内在 DisplayName 命中 ACE Studio 时收下同块的 InstallLocation。
// 分块是必要的：reg 的输出是按值名排序的，不能假定 DisplayName 一定紧邻 InstallLocation。
func installLocations(out string) []string {
	var locs []string
	var name, loc string
	flush := func() {
		if loc != "" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), "ace studio") {
			locs = append(locs, loc)
		}
		name, loc = "", ""
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if reKey.MatchString(line) {
			flush()
			continue
		}
		if m := reName.FindStringSubmatch(line); m != nil {
			name = m[1]
			continue
		}
		if m := reLoc.FindStringSubmatch(line); m != nil {
			loc = strings.TrimSpace(m[1])
		}
	}
	flush()
	return locs
}

var errStop = errors.New("stop walk")

// scanFor 限深扫描找文件，找不到回空串。
func scanFor(root, name string, maxDepth int) string {
	if root == "" {
		return ""
	}
	found := ""
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 权限不足的目录直接跳过，别中断扫描
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return fs.SkipDir
			}
			if strings.Count(strings.TrimPrefix(p, root), string(os.PathSeparator)) >= maxDepth {
				return fs.SkipDir
			}
			return nil
		}
		if strings.EqualFold(d.Name(), name) {
			found = p
			return errStop
		}
		return nil
	})
	return found
}

// skipDir 跳过明显无关的大目录：递归兜底不能把启动自检拖成分钟级。
func skipDir(name string) bool {
	switch strings.ToLower(name) {
	case "node_modules", "$recycle.bin", "temp", "cache", "crashdumps", ".git":
		return true
	}
	return false
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
