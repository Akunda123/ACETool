// Package acecli 是 `acestudio-cli.exe` 的唯一调用层。
//
// 这里的每条做法都对应 HANDOVER.md §3.3 的「调用纪律」（8 条，全部实测得来），
// 改动前先读那一节，别凭印象改：
//
//  1. 绝不经过 shell —— os/exec 直接传 argv，从根上免掉引号/编码的一整类坑
//  2. 读动词一律加 --json（不加只有人话，caret get 之类拿不到字段）
//  3. 写动词的 stdout 可能是人话 ⇒ JSON 解析失败 ≠ 命令失败，只看退出码
//  4. 剥 BOM
//  5. 批量点数据走文件 / stdin（--points @curve.json / @-），别塞命令行
//  6. 写前读指纹，写时 --if-match <token>
//  7. 串行队列 + 逐条超时（只有一个 ACE host，命令别并发）
//  8. 不抢焦点（CLI 本身不抢；别做截图/鼠标自动化就越界了）
package acecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"acetool/internal/win"
)

// 退出码语义 —— 照 `help exit-codes` 原文（HANDOVER.md §3.4），别凭印象。
// ⚠️ 退出码很粗；细粒度一律看 --json 里的 error.code。
const (
	ExitOK          = 0  // 成功
	ExitFailed      = 1  // 命令失败，错误信封说明原因
	ExitUsage       = 2  // clap 用法错：命令根本没执行
	ExitBridgeDown  = 3  // 桥不可达：ACE 没开 / 没开 Connect to Agents
	ExitWaitTimeout = 4  // job wait 超时 —— 不是失败，作业还在跑、没被取消
	ExitSpawnFailed = -1 // 我们自己连进程都没起来（路径错 / 权限）—— 不是 ACE 的退出码
)

// DefaultTimeout 是单条命令的超时（§3.3-7）。
const DefaultTimeout = 30 * time.Second

// Envelope 是 `--json` 模式下写在 stderr 的错误信封：
//
//	{"error":{"code":"INVALID_ARG","message":"...","details":{...},"hint":"..."}}
//
// ⚠️ 信封走 stderr 而不是 stdout ⇒ 必须分别捕获两个流。
type Envelope struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details,omitempty"`
	Hint    string          `json:"hint,omitempty"`
}

// String 把信封拼成人能读的一行。nil 接收者安全（调用方常直接 r.Err.String()）。
func (e *Envelope) String() string {
	if e == nil {
		return ""
	}
	s := e.Code
	if e.Message != "" {
		s += ": " + e.Message
	}
	if e.Hint != "" {
		s += "（hint: " + e.Hint + "）"
	}
	return s
}

// Result 是一次调用的完整留痕。
// 纪律 §10-6：失败要像错误报告 —— exit code / error.code / error.hint 原样展示，别翻译成「出错了」。
type Result struct {
	Args     []string        `json:"args"`
	Stdout   string          `json:"stdout"`
	Stderr   string          `json:"stderr"`
	ExitCode int             `json:"exitCode"`
	Millis   int64           `json:"millis"`
	TimedOut bool            `json:"timedOut,omitempty"`
	JSON     json.RawMessage `json:"json,omitempty"`       // stdout 整体是合法 JSON 时才有
	Err      *Envelope       `json:"error,omitempty"`      // stderr 里的错误信封
	SpawnErr string          `json:"spawnError,omitempty"` // 进程没起来 / 超时被杀
}

func (r *Result) OK() bool { return r != nil && r.ExitCode == ExitOK }

// Client 是 CLI 的调用层。串行队列（mutex）是刻意的：只有一个 ACE host，命令别并发。
type Client struct {
	Path    string
	Timeout time.Duration
	mu      sync.Mutex
}

func New(path string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{Path: path, Timeout: timeout}
}

// Run 跑一条命令。注意它**不因为非零退出码而返回 error** ——
// 退出码是 ACE 的正常语义（见上面的常量），错误在 Result 里；
// 返回的 error 只表示「我们这边发不出去」（没定位到 CLI）。
func (c *Client) Run(ctx context.Context, args ...string) (*Result, error) {
	if strings.TrimSpace(c.Path) == "" {
		return nil, errors.New("还没定位到 acestudio-cli")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, c.Path, args...)
	// ⚠️ GUI 构建（`-H=windowsgui`）下父进程没有控制台 ⇒ 控制台子进程会**新开一个黑框**。
	// 每次跑 CLI 都闪一下 = 用户说的「点击会一直弹 cmd 窗口」⇒ 统一加 CREATE_NO_WINDOW。
	win.HideWindow(cmd)
	// 分别捕获 stdout / stderr：错误信封在 stderr，混在一起会解析错。
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	r := &Result{
		Args:     append([]string{c.Path}, args...),
		Stdout:   stripBOM(stdout.String()),
		Stderr:   stripBOM(stderr.String()),
		Millis:   elapsed.Milliseconds(),
		ExitCode: ExitOK,
	}

	switch runErr {
	case nil:
		r.ExitCode = ExitOK
	default:
		var ee *exec.ExitError
		var se *exec.Error
		switch {
		case errors.As(runErr, &ee):
			r.ExitCode = ee.ExitCode()
		case errors.As(runErr, &se):
			r.ExitCode = ExitSpawnFailed
			r.SpawnErr = se.Error()
		default:
			r.ExitCode = ExitSpawnFailed
			r.SpawnErr = runErr.Error()
		}
	}
	if errors.Is(cctx.Err(), context.DeadlineExceeded) {
		r.TimedOut = true
		r.SpawnErr = fmt.Sprintf("调用超时（%s）后被终止", c.Timeout)
	}

	parseStdout(r)
	parseStderrEnvelope(r)
	return r, nil
}

// Read 跑一个读动词，自动补 --json（§3.3-2：不加只有人话）。
func (c *Client) Read(ctx context.Context, args ...string) (*Result, error) {
	if !hasFlag(args, "--json") {
		args = append(append([]string{}, args...), "--json")
	}
	return c.Run(ctx, args...)
}

// parseStdout 只在 stdout **整体**是合法 JSON 时才填 JSON。
// ⚠️ 写动词成功可能回人话（"Track 0 updated."）⇒ JSON 为 nil 不代表失败（§3.3-3）。
func parseStdout(r *Result) {
	s := strings.TrimSpace(r.Stdout)
	if s == "" || !json.Valid([]byte(s)) {
		return
	}
	r.JSON = json.RawMessage(s)
}

// parseStderrEnvelope 从 stderr 里捞错误信封。
func parseStderrEnvelope(r *Result) {
	s := strings.TrimSpace(r.Stderr)
	if s == "" {
		return
	}
	var doc struct {
		Error *Envelope `json:"error"`
	}
	if err := json.Unmarshal([]byte(s), &doc); err == nil && doc.Error != nil {
		r.Err = doc.Error
		return
	}
	// 兜底：有的动词把信封本体直接丢在 stderr
	var env Envelope
	if err := json.Unmarshal([]byte(s), &env); err == nil && env.Code != "" {
		r.Err = &env
	}
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

// stripBOM 剥掉 UTF-8 BOM（§3.3-4）。
func stripBOM(s string) string { return strings.TrimPrefix(s, "\ufeff") }
