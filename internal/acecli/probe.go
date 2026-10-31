package acecli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Version 是 `--version` 的三元组（换 ACE 版本后一切以运行时为准，别缓存 —— 纪律 §10-3）。
//
//	acestudio-cli 0.17.0
//	Built for ACE Studio 2.1.8
//	Remote Control Bridge Surface 17.1
//	Connected to ACE Studio 2.1.8 (surface 17.1)
type Version struct {
	Raw       string `json:"raw"`
	CLI       string `json:"cli,omitempty"`       // 0.17.0
	ForACE    string `json:"forAce,omitempty"`    // ACE Studio 2.1.8
	Surface   string `json:"surface,omitempty"`   // Surface 17.1
	Connected string `json:"connected,omitempty"` // ACE Studio 2.1.8 (surface 17.1)
}

func ParseVersion(out string) *Version {
	v := &Version{Raw: strings.TrimSpace(out)}
	for _, line := range strings.Split(v.Raw, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "acestudio-cli "):
			v.CLI = strings.TrimPrefix(line, "acestudio-cli ")
		case strings.HasPrefix(line, "Built for "):
			v.ForACE = strings.TrimPrefix(line, "Built for ")
		case strings.HasPrefix(line, "Remote Control Bridge "):
			v.Surface = strings.TrimPrefix(line, "Remote Control Bridge ")
		case strings.HasPrefix(line, "Connected to "):
			v.Connected = strings.TrimPrefix(line, "Connected to ")
		}
	}
	return v
}

// Check 是自检的一步 —— 面板把它直接渲染成一行。
type Check struct {
	Step   string  `json:"step"`
	OK     bool    `json:"ok"`
	Detail string  `json:"detail"`
	Result *Result `json:"result,omitempty"`
}

// SelfCheckReport 是 M0 的产物：找 CLI → 能力探测 → 在线灯。
type SelfCheckReport struct {
	OK      bool            `json:"ok"`
	CLIPath string          `json:"cliPath"`
	From    string          `json:"foundFrom,omitempty"`
	Online  bool            `json:"online"`
	Version *Version        `json:"version,omitempty"`
	Project json.RawMessage `json:"project,omitempty"`
	Checks  []Check         `json:"checks"`
	Hint    string          `json:"hint,omitempty"`
}

// SelfCheck 跑 M0 启动自检。它**永不返回 error**：每一步的失败都是面板上的一行，
// 因为「没装 ACE / 没开 ACE / 没开工程」都是预期内的四种例外（HANDOVER.md §2）。
func SelfCheck(ctx context.Context, c *Client, from string) *SelfCheckReport {
	rep := &SelfCheckReport{CLIPath: c.Path, From: from}
	add := func(step string, ok bool, detail string, r *Result) {
		rep.Checks = append(rep.Checks, Check{Step: step, OK: ok, Detail: detail, Result: r})
	}

	// ① 定位
	if strings.TrimSpace(c.Path) == "" {
		add("① 定位 acestudio-cli", false,
			`没找到。ACE Studio 装了吗？也可以手动指定：acetool.exe --cli "C:\Program Files\ACE Studio\acestudio-cli.exe"`, nil)
		rep.Hint = "acestudio-cli.exe 是 ACE 装的、不是我们带的（HANDOVER.md §2 第一条例外）。没有它，面板只能看自检。"
		return rep
	}
	detail := c.Path
	if from != "" {
		detail += "（来源：" + from + "）"
	}
	add("① 定位 acestudio-cli", true, detail, nil)

	// ② 能力探测
	vr, err := c.Run(ctx, "--version")
	if err != nil {
		add("② 能力探测（--version）", false, err.Error(), nil)
		rep.Hint = "调用层发不出去，先看上面的路径对不对。"
		return rep
	}
	if vr.OK() {
		v := ParseVersion(vr.Stdout)
		rep.Version = v
		add("② 能力探测（--version）", true, describeVersion(v), vr)
	} else {
		add("② 能力探测（--version）", false, "exit "+fmt.Sprint(vr.ExitCode)+"："+firstLine(vr.Stderr), vr)
	}

	// ③ 在线灯：project info --json 的退出码就是判据（§3.2）
	pr, err := c.Read(ctx, "project", "info")
	if err != nil {
		add("③ 在线灯（project info --json）", false, err.Error(), nil)
		return rep
	}
	switch {
	case pr.OK():
		rep.Online = true
		rep.Project = pr.JSON
		add("③ 在线灯（project info --json）", true, "ACE 在线，工程信息已读到", pr)
	case pr.ExitCode == ExitBridgeDown:
		add("③ 在线灯（project info --json）", false,
			`桥不可达（exit 3）：ACE 没开，或没在应用里打开 "Connect to Agents"`, pr)
		rep.Hint = "启动 ACE Studio，并在应用里打开 Connect to Agents，再点「重新自检」。"
	case pr.Err != nil && pr.Err.Code == "NO_PROJECT_OPEN":
		rep.Online = true
		add("③ 在线灯（project info --json）", false, "ACE 在线，但没有打开/新建工程（NO_PROJECT_OPEN）", pr)
		rep.Hint = "先开一个或新建一个工程，再点「重新自检」。"
	default:
		add("③ 在线灯（project info --json）", false,
			"exit "+fmt.Sprint(pr.ExitCode)+" "+pr.Err.String(), pr)
		rep.Hint = "看下面「原始输出」里的 stderr —— 错误信封原样在里面，别跳过它。"
	}

	rep.OK = true
	for _, ck := range rep.Checks {
		if !ck.OK {
			rep.OK = false
			break
		}
	}
	return rep
}

func describeVersion(v *Version) string {
	if v == nil {
		return ""
	}
	var parts []string
	if v.CLI != "" {
		parts = append(parts, "cli "+v.CLI)
	}
	if v.ForACE != "" {
		parts = append(parts, "built for "+v.ForACE)
	}
	if v.Surface != "" {
		parts = append(parts, v.Surface)
	}
	if len(parts) == 0 {
		return firstLine(v.Raw)
	}
	return strings.Join(parts, " · ")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}
