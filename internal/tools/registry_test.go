package tools

import (
	"context"
	"strings"
	"testing"
)

// 注册表不变量。这几条是**结构性**的，坏掉的方式都很安静（或直接 panic），所以拿测试卡住。
func TestRegistryInvariants(t *testing.T) {
	if len(registry) == 0 {
		t.Fatal("注册表是空的")
	}
	seen := map[string]bool{}
	for _, tl := range registry {
		if tl.ID == "" || tl.Title == "" || tl.Category == "" || tl.Desc == "" {
			t.Errorf("工具 %q 的 ID/Title/Category/Desc 有空的：%+v", tl.ID, tl)
		}
		if seen[tl.ID] {
			t.Errorf("重复的工具 ID：%s", tl.ID)
		}
		seen[tl.ID] = true

		// ★ 三种实现**恰有其一**：
		//   Build        —— 走 CLI 的调用序列
		//   Inspect      —— 进程内只读（文件路线）
		//   Plan + Apply —— 进程内写入（文件路线），两者必须成对
		// 一个都没有 ⇒ 一点就 panic（踩过：api.go 里调 nil Build）；两个都有 ⇒ 语义不明。
		hasBuild, hasInspect := tl.Build != nil, tl.Inspect != nil
		hasApply, hasPlan := tl.Apply != nil, tl.Plan != nil
		impls := 0
		for _, ok := range []bool{hasBuild, hasInspect, hasApply} {
			if ok {
				impls++
			}
		}
		switch {
		case impls == 0:
			t.Errorf("工具 %s 三种实现都没有（Build/Inspect/Apply）—— 调用会 panic", tl.ID)
		case impls > 1:
			t.Errorf("工具 %s 有 %d 种实现（Build=%v Inspect=%v Apply=%v）—— 恰要其一",
				tl.ID, impls, hasBuild, hasInspect, hasApply)
		}
		if hasApply != hasPlan {
			t.Errorf("工具 %s 的 Apply 与 Plan 必须成对（Apply=%v Plan=%v）—— 少了 Plan，干跑就没东西可回",
				tl.ID, hasApply, hasPlan)
		}

		// 写工具不许走进程内**只读**实现；反之 Apply 必须是写工具。
		if tl.Kind == KindWrite && hasInspect {
			t.Errorf("写工具 %s 不能有 Inspect（那会绕过干跑闸门与写后回读）", tl.ID)
		}
		if hasApply && tl.Kind != KindWrite {
			t.Errorf("工具 %s 有 Apply 却不是 KindWrite —— 干跑闸门只认写工具", tl.ID)
		}

		// 参数名唯一、非空。
		pseen := map[string]bool{}
		for _, p := range tl.Params {
			if p.Name == "" || p.Label == "" {
				t.Errorf("工具 %s 有空的参数名/标签：%+v", tl.ID, p)
			}
			if pseen[p.Name] {
				t.Errorf("工具 %s 参数名重复：%s", tl.ID, p.Name)
			}
			pseen[p.Name] = true
			if p.Type == ParamSelect && len(p.Options) == 0 {
				t.Errorf("工具 %s 的 select 参数 %s 没有选项", tl.ID, p.Name)
			}
		}
	}
}

// 每个工具的入口都要在**空表单**下给出可读的错或结果 —— 不许 panic。
func TestAllToolsBuildOrExplain(t *testing.T) {
	rd := stubRead(t, map[string]string{}) // 任何读都会回 UNKNOWN_COMMAND
	for _, tl := range registry {
		check := func(what string, f func() (any, error)) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("工具 %s 的 %s 在空表单下 panic 了：%v", tl.ID, what, r)
				}
			}()
			_, err := f()
			if err != nil && strings.TrimSpace(err.Error()) == "" {
				t.Errorf("工具 %s 的 %s 报了个空错误", tl.ID, what)
			}
		}
		ctx := context.Background()
		if tl.Build != nil {
			check("Build", func() (any, error) {
				calls, err := tl.Build(ctx, rd, map[string]string{})
				return calls, err
			})
		}
		if tl.Inspect != nil {
			check("Inspect", func() (any, error) { return tl.Inspect(ctx, rd, map[string]string{}) })
		}
		if tl.Plan != nil {
			check("Plan", func() (any, error) { return tl.Plan(ctx, rd, map[string]string{}) })
		}
		// ⚠️ **不调 Apply** —— 它会在磁盘上产生副作用，空表单测试不该碰文件系统。
	}
}
