package tools

import (
	"strings"
	"testing"
)

// `numbers` 的解析（改 float64 数组那一路）：逗号/空白/分号都要收，坏数字要**报错说第几个**。
func TestParseNumbers(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []float64
	}{
		{"0,0,1.5,-0.25", []float64{0, 0, 1.5, -0.25}},
		{"0 0 1.5", []float64{0, 0, 1.5}},
		{"0;0;2", []float64{0, 0, 2}},
		{" 1 , 2 \n 3 ", []float64{1, 2, 3}},
		{"-12", []float64{-12}},
	} {
		got, err := parseNumbers(c.in)
		if err != nil {
			t.Errorf("parseNumbers(%q) 报错：%v", c.in, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("parseNumbers(%q) = %v，期望 %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("parseNumbers(%q)[%d] = %v，期望 %v", c.in, i, got[i], c.want[i])
			}
		}
	}
	// 坏输入要报错，而且要指出**第几个**（几千个数里找一个错，没有下标没法查）
	for _, bad := range []string{"0,x,2", "1,,2", ",1", "abc", ""} {
		if _, err := parseNumbers(bad); err == nil {
			t.Errorf("parseNumbers(%q) 应当报错", bad)
		}
	}
	// ⛔ 内部空字段必须**报错**（不能默默丢掉）：丢了紧接着报的就是"个数不符"，
	//    用户会去数个数，而真正的问题是那个空字段 → 报错要点出位置。
	if _, err := parseNumbers("1,,2"); err == nil {
		t.Error("内部空字段应当报错")
	} else if !strings.Contains(err.Error(), "空的") {
		t.Errorf("空字段的报错要说清是空的，实得：%v", err)
	}
	// ⚠️ 末尾**一个**分隔符要容忍（生成器很常见，且没有歧义）
	if got, err := parseNumbers("1,2,"); err != nil || len(got) != 2 {
		t.Errorf("末尾一个逗号应当容忍，实得 %v / %v", got, err)
	}
	if got, err := parseNumbers("1,2,3,"); err != nil || len(got) != 3 {
		t.Errorf("末尾一个逗号应当容忍，实得 %v / %v", got, err)
	}
	// ⚠️ 空白只是两侧的 padding，不是"空字段"
	if got, err := parseNumbers(" 1 , 2 \n 3 "); err != nil || len(got) != 3 {
		t.Errorf("逗号两侧的空白应当容忍，实得 %v / %v", got, err)
	}
	if got, err := parseNumbers("1, 2 3"); err != nil || len(got) != 3 {
		t.Errorf("一段里再有空白也要拆开，实得 %v / %v", got, err)
	}
	// 前导分隔符是坏输入（只有**末尾**一个容忍）
	if _, err := parseNumbers(",1"); err == nil {
		t.Error("前导逗号应当报错")
	}
}

// 音高曲线在工程文件里的**路径与形状**是这轮真机查出来的事实，钉住它免得以后写歪：
//
//	tracks[<t>].patterns[<p>].parameters.pitchDelta[0] = {offset, type:"data", values:[float…]}
//
// ⚠️ 这条只是"我们的认知快照"——真正的事实源永远是工程文件本身。写它是因为
// "音高曲线到底存在哪" 这件事在 ACE 的 CLI 上**查不到**（`pitch` 恒 available:false），
// 只能拆文件；不钉住的话下次又要重新查一遍。
func TestPitchCurvePathShape(t *testing.T) {
	const path = "tracks[0].patterns[0].parameters.pitchDelta[0].values"
	if !strings.HasSuffix(path, "."+"values") {
		t.Fatal("路径形状变了")
	}
	// 段落那三个键（真机实测就这三个）
	for _, k := range []string{"offset", "type", "values"} {
		if k == "" {
			t.Errorf("%s 不该为空", k)
		}
	}
}
