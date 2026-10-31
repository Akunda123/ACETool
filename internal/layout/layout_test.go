package layout

import (
	"reflect"
	"testing"
)

// Arrange：order 优先、没提到的保持原相对顺序、hidden 剔除、不认识的忽略。
func TestArrange(t *testing.T) {
	ids := []string{"a", "b", "c", "d"}
	got := Arrange(ids, []string{"c", "a", "zz"}, []string{"b"})
	if want := []string{"c", "a", "d"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("应当 %v，实得 %v", want, got)
	}
	// 全空配置 ⇒ 原样返回（老配置不用迁移）
	if got := Arrange(ids, nil, nil); !reflect.DeepEqual(got, []string{"a", "b", "c", "d"}) {
		t.Errorf("空配置应当原样返回，实得 %v", got)
	}
	// hidden 里连 order 的第一项也剔掉
	if got := Arrange(ids, []string{"d", "c"}, []string{"d"}); !reflect.DeepEqual(got, []string{"c", "a", "b"}) {
		t.Errorf("hidden 应当赢过 order，实得 %v", got)
	}
	// 重复/空白 id 不该进来两次
	if got := Arrange([]string{"x", "y"}, []string{" y ", "y"}, nil); !reflect.DeepEqual(got, []string{"y", "x"}) {
		t.Errorf("去重/去空白错了：%v", got)
	}
}

// Unknown：把配置里"已经没有的按钮"报出来（保存时要当场拒，不静默丢）。
func TestUnknown(t *testing.T) {
	known := []string{"a", "b"}
	if got := Unknown([]string{"a", "gone"}, []string{"dropped", "gone"}, known); !reflect.DeepEqual(got, []string{"gone", "dropped"}) {
		t.Errorf("应当按出现顺序报 %v，实得 %v", []string{"gone", "dropped"}, got)
	}
	if got := Unknown([]string{"a"}, []string{"b"}, known); len(got) != 0 {
		t.Errorf("都认识时不该报，实得 %v", got)
	}
}

func TestClean(t *testing.T) {
	got := Clean([]string{" a ", "", "a", "b", "  "})
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("规整错了：%v", got)
	}
}
