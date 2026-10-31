package hotkeys

import (
	"strings"
	"testing"
)

// 规范形：修饰键固定顺序、别名归一、大小写归一。**前端采集出来的字面必须能被这里认下**
// （两侧是同一份契约，哪边松了都会出现"设置里显示已绑、按下去没反应"）。
func TestNormalizeCanonicalForm(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ctrl+alt+enter", "Ctrl+Alt+Enter"},
		{"Alt+Ctrl+K", "Ctrl+Alt+K"}, // 顺序无关，输出固定 Ctrl→Alt→Shift→Win
		{"shift+ctrl+alt+win+f2", "Ctrl+Alt+Shift+Win+F2"},
		{"Control+Return", "Ctrl+Enter"}, // 别名
		{"esc", "Escape"},
		{"up", "ArrowUp"},
		{"k", "K"},
		{"K", "K"},
		{"1", "1"},
		{"?", "?"},
		{"[", "["},
		{"ctrl+]", "Ctrl+]"},
		{"", ""}, // 空 = 不绑，原样
	}
	for _, c := range cases {
		got, err := Normalize(c.in)
		if err != nil {
			t.Errorf("Normalize(%q) 报错：%v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Normalize(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// 认不出来的主键**必须报错**，不能猜、更不能默默当不存在。
func TestNormalizeRejectsUnknownMain(t *testing.T) {
	for _, in := range []string{"Ctrl+Foo", "Ctrl+", "+K", "Ctrl+Ctrl2", "Hyper+K", "Ctrl+Alt"} {
		if got, err := Normalize(in); err == nil {
			t.Errorf("%q 应当被拒，实得 %q", in, got)
		}
	}
}

// 校验：三类拒绝理由都要说清（浏览器占用 / 系统级必须带修饰 / 裸键会挡输入）。
func TestValidateRules(t *testing.T) {
	// ① 浏览器占着的组合直接拒，且**理由里点名是谁占的**
	for _, k := range []string{"Ctrl+W", "Ctrl+D", "Ctrl+L", "F5", "Alt+F4"} {
		if _, err := Validate(k, false); err == nil {
			t.Errorf("%q 被浏览器/系统占着，应当被拒", k)
		} else if !strings.Contains(err.Error(), "占着") {
			t.Errorf("%q 的拒绝理由要说明「被谁占着」，实得：%v", k, err)
		}
	}
	// ② 系统级必须有修饰键
	if _, err := Validate("K", true); err == nil {
		t.Error("系统级 `K` 应当被拒（会把裸按键从整个系统抢走）")
	}
	if _, err := Validate("F2", true); err == nil {
		t.Error("系统级 `F2` 也应当被拒（同样是无修饰键）")
	}
	// ③ 页面内：裸字母挡打字 ⇒ 拒；裸 F 键与 `?` `[` `]` 放行
	if _, err := Validate("K", false); err == nil {
		t.Error("页面内裸 `K` 应当被拒（会挡住输入框打字）")
	}
	for _, k := range []string{"F2", "?", "[", "]", "F24"} {
		if _, err := Validate(k, false); err != nil {
			t.Errorf("页面内 %q 应当放行（输入框里我们本来就不抢键），实得 %v", k, err)
		}
	}
	// F12 在 reserved 里 ⇒ 单独确认它被拒
	if _, err := Validate("F12", false); err == nil {
		t.Error("F12 是开发者工具，应当被拒")
	}
	// ④ 正常组合放行并规范
	got, err := Validate("ctrl+alt+enter", true)
	if err != nil || got != "Ctrl+Alt+Enter" {
		t.Errorf("Ctrl+Alt+Enter 应当放行并规范化，实得 %q / %v", got, err)
	}
}

// 出厂表：**现在应该是空的**（用户 2026-10-17：「默认都是空」），而且每条动作都得能过校验。
func TestDefaultsEmptyAndActionsValid(t *testing.T) {
	if d := Defaults(); len(d) != 0 {
		t.Fatalf("出厂表应当是空的，实得 %d 条：%+v", len(d), d)
	}
	if d := DefaultsWith(3); len(d) != 0 {
		t.Fatalf("DefaultsWith 现在也应返回空表，实得 %+v", d)
	}
	// 动作表只剩主页面那组 ⇒ 是 `quick.*`，而且默认不绑
	// （清单正常由主页面/配置给；测试里没有页面 ⇒ 用内置那份兜底，也就是"装着 exe 就有的 20 条"）
	SeedBuiltin()
	acts := Catalog()
	if len(acts) != 23 {
		t.Fatalf("动作表应当正好 23 条（主页面键位：1 个打开主页面 + 22 颗按钮），实得 %d", len(acts))
	}
	for _, a := range acts {
		if !strings.HasPrefix(a.ID, "quick.") {
			t.Errorf("动作表里混进了非主页面动作：%s", a.ID)
		}
		if a.DefaultKey != "" {
			t.Errorf("%s 不该有出厂键位，实得 %q", a.ID, a.DefaultKey)
		}
		if !a.CanGlobal || !a.AlwaysGlobal {
			t.Errorf("%s 必须是「只能系统级」（AlwaysGlobal）", a.ID)
		}
		if a.Label == "" || a.What == "" {
			t.Errorf("%s 缺 Label/What（设置页会显示成空白）", a.ID)
		}
	}
}

// 冲突检测：同一个键绑两个动作要报出来（否则后一个静默注册失败，用户只觉得"键没反应"）。
func TestConflictsDetectsDuplicateKeys(t *testing.T) {
	byID := map[string]Action{}
	for _, a := range Catalog() {
		byID[a.ID] = a
	}
	b := Bindings{
		"quick.find.notes": {Key: "Ctrl+Alt+K"},
		"quick.note.join":  {Key: "Ctrl+Alt+K"}, // 撞了
		"quick.lyric.copy": {Key: "Ctrl+Alt+J"},
	}
	c := Conflicts(b, byID)
	if len(c) != 1 {
		t.Fatalf("应当报 1 条冲突，实得 %d：%v", len(c), c)
	}
	if !strings.Contains(c[0], "Ctrl+Alt+K") {
		t.Errorf("冲突提示要带上键位，实得 %q", c[0])
	}
	// 空键位（未绑）不算冲突
	if c := Conflicts(Bindings{"a": {}, "b": {}}, byID); len(c) != 0 {
		t.Errorf("未绑的不该算冲突，实得 %v", c)
	}
}

// 目录：动作 id 唯一、每条都有 Label/What（设置页会显示成空白的那类错）。
func TestCatalogIDsUniqueAndDescribed(t *testing.T) {
	acts := Catalog()
	seen := map[string]bool{}
	for _, a := range acts {
		if seen[a.ID] {
			t.Errorf("动作 id 重复：%s", a.ID)
		}
		seen[a.ID] = true
		if a.Label == "" || a.What == "" {
			t.Errorf("%s 缺 Label/What（设置页会显示成空白）", a.ID)
		}
	}
	// 历史入口 `CatalogWith` 现在与 `Catalog` 等价（跳栏动作已随老壳页删掉）
	if len(CatalogWith([]string{"任意栏"})) != len(acts) {
		t.Errorf("CatalogWith 现在应当和 Catalog 一样")
	}
}
