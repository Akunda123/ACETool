package tools

import "testing"

// 侧栏分组：**两栏**（用户 2026-10-04：把你写的放一类、我说的放一类）。
func TestToolGroupsForUI(t *testing.T) {
	cat := CatalogForUI()
	if len(cat.Categories) != 2 {
		t.Fatalf("侧栏应当恰好两栏，实得 %d 栏：%v", len(cat.Categories), cat.Categories)
	}
	if cat.Categories[0] != "移植脚本" || cat.Categories[1] != "自建工具" {
		t.Errorf("两栏应当是 [移植脚本 自建工具]（顺序也算），实得 %v", cat.Categories)
	}
	// 每一栏里都真的有人，而且工具只属于一栏
	byCat := map[string][]string{}
	for _, tl := range cat.Tools {
		byCat[tl.Category] = append(byCat[tl.Category], tl.ID)
	}
	if len(byCat["移植脚本"]) == 0 || len(byCat["自建工具"]) == 0 {
		t.Fatalf("两栏都不能是空的：%v", byCat)
	}
	// 用户点名的那几项必须在「移植脚本」里
	for _, id := range []string{
		"find.notes", "find.goto", "find.select", // [SV2]查找.js
		"lyric.pages", "lyric.setSentence", "lyric.goto", // [SV2]歌词侧栏.js
		"lyric.tones",                  // 备用的语调工具
		"note.join", "select.playhead", // AKD工具侧栏.js
		"lyric.copy", "lyric.paste", "lyric.shift", "lyric.remove",
	} {
		found := false
		for _, tl := range cat.Tools {
			if tl.ID == id {
				found = true
				if tl.Category != "移植脚本" {
					t.Errorf("%s 应当在「移植脚本」栏，实得 %q", id, tl.Category)
				}
			}
		}
		if !found {
			t.Errorf("%s 不在清单里", id)
		}
	}
	// 我先搭的必须在「自建工具」里
	for _, id := range []string{"inspect.overview", "inspect.tracks", "color.cycle",
		"acep.inspect", "acep.dump", "acep.patch", "inspect.revision"} {
		for _, tl := range cat.Tools {
			if tl.ID == id && tl.Category != "自建工具" {
				t.Errorf("%s 应当在「自建工具」栏，实得 %q", id, tl.Category)
			}
		}
	}
}

// ⛔ CatalogForUI 换 Category 时**必须改副本**。
//
// 注册表里那些 `*Tool` 是共享的：要是直接改 `t.Category`，那么
// 第一次调用之后注册表里就变成栏名了 —— 第二次调用、以及 `Get()`、以及所有
// 依赖细分类的测试都会跟着出错。这条就是钉这个的。
func TestCatalogForUIDoesNotMutateRegistry(t *testing.T) {
	// 先记下细分类
	before := map[string]string{}
	for _, tl := range registry {
		before[tl.ID] = tl.Category
	}
	first := CatalogForUI()
	second := CatalogForUI()

	// 调过两次之后，注册表里的细分类**一个都不能变**
	for _, tl := range registry {
		if tl.Category != before[tl.ID] {
			t.Errorf("CatalogForUI 改到了注册表里的 %s：%q → %q",
				tl.ID, before[tl.ID], tl.Category)
		}
	}
	// Get() 拿到的也必须是细分类（它是给代码用的，不是给 UI 用的）
	if g := Get("lyric.pages"); g == nil || g.Category != "歌词" {
		t.Errorf("Get(\"lyric.pages\").Category 应当是「歌词」，实得 %v", g)
	}
	if g := Get("inspect.overview"); g == nil || g.Category != "巡检" {
		t.Errorf("Get(\"inspect.overview\").Category 应当是「巡检」，实得 %v", g)
	}
	// 两次调用的结果必须一致（改到注册表的话第二次就会不一样）
	if len(first.Tools) != len(second.Tools) {
		t.Fatalf("两次调用工具数不同：%d vs %d", len(first.Tools), len(second.Tools))
	}
	for i := range first.Tools {
		if first.Tools[i].ID != second.Tools[i].ID || first.Tools[i].Category != second.Tools[i].Category {
			t.Errorf("两次调用结果不一致（第 %d 项）：%s/%s vs %s/%s", i,
				first.Tools[i].ID, first.Tools[i].Category,
				second.Tools[i].ID, second.Tools[i].Category)
		}
	}
	// 每次调用返回的指针也应当是新的（不是注册表里那几个）
	for i := range first.Tools {
		if first.Tools[i] == second.Tools[i] {
			t.Errorf("第 %d 项两次拿到同一个指针 —— 说明没复制", i)
		}
	}
	// Summary 也要按两栏报（它走的是同一份 UI 清单）
	s := Summary()
	if !contains(s, "移植脚本") || !contains(s, "自建工具") {
		t.Errorf("Summary 应当按两栏报：%q", s)
	}
}

// groupOf：认不出的分类**不能被吞掉**，要自己成一栏（否则将来加的分类会悄悄掉进某一栏）。
func TestGroupOfUnknown(t *testing.T) {
	if got := groupOf("歌词"); got != "移植脚本" {
		t.Errorf("歌词 → 移植脚本，实得 %q", got)
	}
	if got := groupOf("巡检"); got != "自建工具" {
		t.Errorf("巡检 → 自建工具，实得 %q", got)
	}
	if got := groupOf("将来才有的分类"); got != "将来才有的分类" {
		t.Errorf("认不出的分类应当原样返回（自成栏），实得 %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
