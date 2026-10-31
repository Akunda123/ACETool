package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// `values` 宽进：字符串/数字/布尔/null 都归一成字符串。
//
// 起因是真踩到的：手写脚本把 `absTick` 当**数字**发 ⇒ 整个请求被拒，报
//
//	`请求体不是合法 JSON：json: cannot unmarshal number into Go struct field .values.absTick of type string`
//
// —— 连工具名都没提。表单值本来就是字符串，边界上宽进才不会白踩。
func TestStringifyValues(t *testing.T) {
	// 一个真实的请求体（数字是**故意的**）
	var req struct {
		Values map[string]any `json:"values"`
	}
	raw := `{"values":{"absTick":29520,"noteUuid":"{a-b}","dryRun":true,"empty":null,
	                   "ratio":0.5,"big":1e21,"neg":-3}}`
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatal(err)
	}
	got, err := stringifyValues(req.Values)
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	want := map[string]string{
		"absTick":  "29520", // ⚠️ 不能变成 "29520.0"，更不能变成科学计数法
		"noteUuid": "{a-b}",
		"dryRun":   "1", // 与前端 checkbox 的口径一致
		"empty":    "",
		"ratio":    "0.5",
		"big":      "1000000000000000000000", // 不用科学计数法
		"neg":      "-3",
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q，期望 %q", k, got[k], w)
		}
	}
	// tick 这种大整数不能出现 e 记号（出现了就会被 CLI 当成坏 TIME_VALUE）
	if strings.ContainsAny(got["absTick"], "eE") {
		t.Errorf("absTick 不该带科学计数法：%q", got["absTick"])
	}
	// 嵌套对象/数组仍然报错，但**要点名是哪个 key**
	if _, err := stringifyValues(map[string]any{"nest": map[string]any{"a": 1}}); err == nil {
		t.Error("嵌套对象应当报错")
	} else if !strings.Contains(err.Error(), "nest") {
		t.Errorf("报错要点名是哪个 key，实得：%v", err)
	}
	if _, err := stringifyValues(map[string]any{"arr": []any{1, 2}}); err == nil {
		t.Error("数组应当报错")
	} else if !strings.Contains(err.Error(), "arr") {
		t.Errorf("报错要点名是哪个 key，实得：%v", err)
	}
	// 空 map / nil 不炸
	if m, err := stringifyValues(nil); err != nil || len(m) != 0 {
		t.Errorf("nil 应当给空 map，实得 %v,%v", m, err)
	}
}

// isTruthy 的口径：干跑闸门就靠它，改错了会静默地"该干跑的没干跑"。
func TestIsTruthy(t *testing.T) {
	for _, s := range []string{"1", "true", "yes", "on", "是", "有", " TRUE "} {
		if !isTruthy(s) {
			t.Errorf("isTruthy(%q) 应当为真", s)
		}
	}
	for _, s := range []string{"", "0", "false", "no", "off", "否", "无", "随便"} {
		if isTruthy(s) {
			t.Errorf("isTruthy(%q) 应当为假", s)
		}
	}
}
