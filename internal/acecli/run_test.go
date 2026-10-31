package acecli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// 纪律 §10-4「离线可测」：调用层对着**假 CLI**（返回固定 JSON 的 stub）做测试，不依赖 ACE 也能跑。
//
// 实现方式：让测试二进制**同时**充当假 CLI —— 子进程带着 ACETOOL_STUB 重启自己，
// 就按 argv 回固定输出。这样离线可测，又不用额外造一个 stub 文件 / shell 脚本。
//
// ⚠️ 环境变量必须在 m.Run() **之前**设：父进程已经过了上面那个判断，
// 而子进程会继承到它，于是走进 stub 分支。
func TestMain(m *testing.M) {
	if os.Getenv("ACETOOL_STUB") != "" {
		os.Exit(stubMain(os.Args[1:]))
	}
	os.Setenv("ACETOOL_STUB", "1")
	os.Exit(m.Run())
}

func stubMain(args []string) int {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "--version"):
		fmt.Println("acestudio-cli 0.17.0")
		fmt.Println("Built for ACE Studio 2.1.8")
		fmt.Println("Remote Control Bridge Surface 17.1")
		fmt.Println("Connected to ACE Studio 2.1.8 (surface 17.1)")
		return ExitOK
	case strings.Contains(joined, "project info"):
		fmt.Print(`{"duration":123.5,"projectName":"stub","isNewProject":false,"isTempProject":false}`)
		return ExitOK
	case strings.Contains(joined, "track set"):
		// 写动词：成功，但 stdout 是人话（§3.3-3）
		fmt.Println("Track 0 updated.")
		return ExitOK
	case strings.Contains(joined, "bridge-down"):
		fmt.Fprintln(os.Stderr, `{"error":{"code":"BRIDGE_UNREACHABLE","message":"cannot reach ACE Studio"}}`)
		return ExitBridgeDown
	case strings.Contains(joined, "no-project"):
		fmt.Fprintln(os.Stderr, `{"error":{"code":"NO_PROJECT_OPEN","message":"no project open","hint":"open or create one"}}`)
		return ExitFailed
	case strings.Contains(joined, "bom"):
		fmt.Println("\ufeff{\"ok\":true}")
		return ExitOK
	default:
		fmt.Fprintln(os.Stderr, `{"error":{"code":"UNKNOWN_COMMAND","message":"stub has no such command"}}`)
		return ExitUsage
	}
}

func stubClient(t *testing.T) *Client {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("拿不到测试二进制路径：%v", err)
	}
	return New(exe, 10*time.Second)
}

func TestParseVersion(t *testing.T) {
	v := ParseVersion("acestudio-cli 0.17.0\nBuilt for ACE Studio 2.1.8\n" +
		"Remote Control Bridge Surface 17.1\nConnected to ACE Studio 2.1.8 (surface 17.1)\n")
	if v.CLI != "0.17.0" {
		t.Errorf("CLI = %q", v.CLI)
	}
	if v.ForACE != "ACE Studio 2.1.8" {
		t.Errorf("ForACE = %q", v.ForACE)
	}
	if v.Surface != "Surface 17.1" {
		t.Errorf("Surface = %q", v.Surface)
	}
	if v.Connected != "ACE Studio 2.1.8 (surface 17.1)" {
		t.Errorf("Connected = %q", v.Connected)
	}
}

func TestReadAddsJSONAndParses(t *testing.T) {
	r, err := stubClient(t).Read(context.Background(), "project", "info")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !hasFlag(r.Args, "--json") {
		t.Error("读动词必须自动补 --json（§3.3-2）")
	}
	if !r.OK() {
		t.Fatalf("exit=%d stderr=%s", r.ExitCode, r.Stderr)
	}
	if r.JSON == nil || !strings.Contains(string(r.JSON), "projectName") {
		t.Fatalf("合法 JSON 的 stdout 应该填进 JSON：%s", r.JSON)
	}
}

// 写动词成功回人话 ⇒ JSON 解析失败 ≠ 命令失败，只看退出码（§3.3-3）。
func TestWriteVerbHumanStdoutIsNotFailure(t *testing.T) {
	r, err := stubClient(t).Run(context.Background(), "track", "set", "--track-index", "0", "--color", "#EC4F44")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !r.OK() {
		t.Fatalf("exit=%d", r.ExitCode)
	}
	if r.JSON != nil {
		t.Errorf("人话的 stdout 不该被当成 JSON：%s", r.JSON)
	}
	if !strings.Contains(r.Stdout, "Track 0 updated.") {
		t.Errorf("stdout = %q", r.Stdout)
	}
}

func TestBridgeDownIsExit3(t *testing.T) {
	r, err := stubClient(t).Read(context.Background(), "bridge-down")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if r.ExitCode != ExitBridgeDown {
		t.Fatalf("exit=%d，期望 %d", r.ExitCode, ExitBridgeDown)
	}
	if r.Err == nil || r.Err.Code != "BRIDGE_UNREACHABLE" {
		t.Fatalf("错误信封在 stderr，必须被解析出来：%+v", r.Err)
	}
}

func TestEnvelopeFromStderr(t *testing.T) {
	r, err := stubClient(t).Read(context.Background(), "no-project")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if r.OK() {
		t.Fatal("NO_PROJECT_OPEN 应该是非零退出码")
	}
	if r.Err == nil || r.Err.Code != "NO_PROJECT_OPEN" || r.Err.Hint == "" {
		t.Fatalf("信封字段没解析全：%+v", r.Err)
	}
}

func TestStripBOM(t *testing.T) {
	r, err := stubClient(t).Run(context.Background(), "bom")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.HasPrefix(r.Stdout, "\ufeff") {
		t.Fatal("BOM 没剥掉（§3.3-4）")
	}
	if !r.OK() || r.JSON == nil {
		t.Fatalf("剥完 BOM 应该能当 JSON 解析：exit=%d json=%s", r.ExitCode, r.JSON)
	}
}

func TestSelfCheckAgainstStub(t *testing.T) {
	rep := SelfCheck(context.Background(), stubClient(t), "stub")
	if !rep.OK || !rep.Online {
		t.Fatalf("对着 stub 的自检应该全绿：%+v", rep)
	}
	if rep.Version == nil || rep.Version.CLI != "0.17.0" {
		t.Fatalf("版本没读到：%+v", rep.Version)
	}
	if rep.Project == nil {
		t.Fatal("在线时应该带回 project info")
	}
}

func TestSelfCheckWithoutCLI(t *testing.T) {
	rep := SelfCheck(context.Background(), New("", time.Second), "")
	if rep.OK {
		t.Fatal("没有 CLI 时自检不该绿")
	}
	if len(rep.Checks) != 1 || rep.Hint == "" {
		t.Fatalf("应该只报第一步行不通 + 给提示：%+v", rep)
	}
}

// reg.exe 的输出解析：块内 DisplayName 命中才收 InstallLocation；
// 且 DisplayName 会本地化（中文机「ACE Studio 版本 2.1.8」）⇒ 只匹配 ASCII 前缀。
func TestInstallLocations(t *testing.T) {
	out := "HKEY_LOCAL_MACHINE\\SOFTWARE\\WOW6432Node\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\{A}\r\n" +
		"    DisplayName    REG_SZ    ACE Studio 版本 2.1.8\r\n" +
		"    InstallLocation    REG_SZ    C:\\Program Files\\ACE Studio\\\r\n" +
		"\r\n" +
		"HKEY_LOCAL_MACHINE\\SOFTWARE\\WOW6432Node\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\{B}\r\n" +
		"    DisplayName    REG_SZ    Something Else\r\n" +
		"    InstallLocation    REG_SZ    C:\\Other\\\r\n"

	locs := installLocations(out)
	if len(locs) != 1 {
		t.Fatalf("应该只收到 ACE Studio 那一条，实得 %#v", locs)
	}
	if locs[0] != `C:\Program Files\ACE Studio\` {
		t.Fatalf("InstallLocation = %q", locs[0])
	}
}

// InstallLocation 排在 DisplayName 前面时也得认（别假定值的顺序）。
func TestInstallLocationsOrderIndependent(t *testing.T) {
	out := "HKEY_LOCAL_MACHINE\\X\\{A}\r\n" +
		"    InstallLocation    REG_SZ    D:\\ACETest\\\r\n" +
		"    DisplayName    REG_SZ    ACE Studio\r\n"
	locs := installLocations(out)
	if len(locs) != 1 || locs[0] != `D:\ACETest\` {
		t.Fatalf("实得 %#v", locs)
	}
}
