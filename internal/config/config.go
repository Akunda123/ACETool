// Package config 存 ACETool 自己的配置（不碰 ACE 的任何文件）。
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Config 落在 %APPDATA%\ACETool\config.json（macOS/Linux 走 os.UserConfigDir 的对应位置）。
type Config struct {
	CLIPath    string `json:"cliPath"`    // 手选/自动找到的 acestudio-cli 路径
	TimeoutSec int    `json:"timeoutSec"` // 单条命令超时，默认 30（HANDOVER.md §3.3-7）
	DryRun     bool   `json:"dryRun"`     // 干跑默认（纪律 §10-2：写入要显式开关）
	// PanelOnTop 是「面板窗口置顶」这个**选项**的记忆：置顶是 exe 对**浏览器窗口**做的
	// （`SetWindowPos(HWND_TOPMOST)`），不记在这里就没地方记它。
	PanelOnTop bool `json:"panelOnTop,omitempty"`
	// RailOnBoot = 面板启动时**顺手把细条也起来**（侧栏）。默认 true；-no-rail 可以强制不起。
	// 设置页（settings.html 的「工具」）能改它 —— 下次启动生效。
	RailOnBoot bool `json:"railOnBoot"`
	// LastViewport 是**上一次页面报上来的窗口大小**（`"1536x864"`）—— 用来"按这个尺寸做版式"。
	LastViewport string `json:"lastViewport,omitempty"`
	// UIState 是**页面自己的界面状态**（每个按钮选过的档位、版式偏好…），由 `/api/uistate` 读写。
	//
	// ⚠️ 为什么必须落在**服务端**、不能只靠浏览器的 localStorage：
	// localStorage 按 **origin（含端口）**分家，而本工具每次启动**端口都变**（随机空闲端口）
	// ⇒ 只存 localStorage 的话**重启一次 exe 状态就全丢** —— 而用户要的正是"下次打开还是这个状态"。
	UIState map[string]any `json:"uiState,omitempty"`
	// Shortcuts 是快捷键绑定（动作 id → 键位 + 是否系统级）。见 `internal/hotkeys`。
	//
	// ⚠️ 类型定义放在这里、而不是 hotkeys 包里，是为了避免 config ⇄ hotkeys 互相 import
	// （hotkeys 依赖 config 落盘）。`hotkeys.Binding` 就是它的**别名**。
	Shortcuts map[string]ShortcutBinding `json:"shortcuts,omitempty"`

	// ⭐ 用户 2026-10-20：设置页「按钮顺序与显示」—— 侧栏与主面板**同一份配置**。
	// `LayoutOrder` 是显示顺序（按钮 id 列表）；`LayoutHidden` 是隐藏掉的 id。
	// ⛔ 两个都只作用于**渲染**：隐藏的按钮快捷键照跑、工具照用（用户明确要求）。
	// 空 = 用内置顺序、全部显示（老配置不用迁移）。
	LayoutOrder  []string `json:"layoutOrder,omitempty"`
	LayoutHidden []string `json:"layoutHidden,omitempty"`
	// LayoutShown = **默认隐藏**的按钮里，用户显式打开了的（用户 2026-10-20）。
	LayoutShown []string `json:"layoutShown,omitempty"`
	// LayoutVersion 记录"内置顺序是第几版"：老配置里存过 layoutOrder 的，升级到新版会被清一次，
	// 好让新的出厂顺序（按颜色分组）生效（用户 2026-10-21：「顺序没改」）。
	LayoutVersion int `json:"layoutVersion,omitempty"`
}

// ShortcutBinding 是一条快捷键绑定。键位存**规范形**（`Ctrl+Alt+Enter`，见 `hotkeys.Normalize`）。
type ShortcutBinding struct {
	Key string `json:"key"`
	// Global=true ⇒ 交给 exe 做**系统级**注册（`RegisterHotKey`）：浏览器不在前台也响应。
	// 代价：这个组合键被 exe 系统级吞掉，网页再也收不到它（所以前端要跳过 Global 的那些）。
	Global bool `json:"global,omitempty"`
}

func defaults() *Config {
	return &Config{TimeoutSec: 30, DryRun: true, RailOnBoot: true}
}

func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ACETool"), nil
}

func Path() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.json"), nil
}

// Load 永不返回 nil 配置：读不到/坏了都给一份默认值 + 一个 error（调用方决定要不要吱声）。
func Load() (*Config, error) {
	cfg := defaults()
	p, err := Path()
	if err != nil {
		return cfg, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(b, cfg); err != nil {
		return defaults(), err
	}
	if cfg.TimeoutSec <= 0 {
		cfg.TimeoutSec = 30
	}
	return cfg, nil
}

func (c *Config) Save() error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o644)
}
