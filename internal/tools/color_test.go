package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"acetool/internal/acecli"
)

// stubRead 造一个假探针：按「拼起来的 argv」查表回 JSON。
// 表里没有的命令回 UNKNOWN_COMMAND —— 这样"不该发生的读"会以错误暴露出来，
// 而不是静默返回零值（比如"跟随 caret"时若真去读了 caret，就会被抓到）。
func stubRead(t *testing.T, table map[string]string) ReadFunc {
	t.Helper()
	return func(_ context.Context, args ...string) (*acecli.Result, error) {
		key := strings.Join(args, " ")
		payload, ok := table[key]
		if !ok {
			env := &acecli.Envelope{Code: "UNKNOWN_COMMAND", Message: "stub 没有这条命令：" + key}
			body, _ := json.Marshal(map[string]any{"error": env})
			return &acecli.Result{Args: args, ExitCode: acecli.ExitUsage, Stderr: string(body), Err: env}, nil
		}
		return &acecli.Result{
			Args: args, ExitCode: acecli.ExitOK,
			Stdout: payload, JSON: json.RawMessage(payload),
		}, nil
	}
}

func TestPaletteMatchesHelpColor(t *testing.T) {
	if len(palette) != 28 {
		t.Fatalf("调色板应当 28 色，实得 %d", len(palette))
	}
	// 抽查两端与一个中间值（`help color` 的官方列表）
	for i, want := range map[int]string{0: "#EC4F44", 8: "#CCFF31", 27: "#FF59AC"} {
		if palette[i] != want {
			t.Errorf("palette[%d] = %q，期望 %q", i, palette[i], want)
		}
	}
}

func TestPaletteIndexAndWrap(t *testing.T) {
	if got := paletteIndex("#ccff31"); got != 8 { // 大小写不敏感（track get 回小写）
		t.Errorf("paletteIndex(#ccff31) = %d，期望 8", got)
	}
	if got := paletteIndex("#CCFF31"); got != 8 {
		t.Errorf("paletteIndex(#CCFF31) = %d，期望 8", got)
	}
	if got := paletteIndex("#123456"); got != -1 {
		t.Errorf("调色板外的颜色应当回 -1，实得 %d", got)
	}
	if wrapIdx(28) != 0 || wrapIdx(-1) != 27 || wrapIdx(-28) != 0 {
		t.Errorf("wrapIdx 越界处理不对：28->%d, -1->%d, -28->%d", wrapIdx(28), wrapIdx(-1), wrapIdx(-28))
	}
}

// 「读 → 决定 → 写」：留空轨道索引 ⇒ 先问 caret，再读颜色，最后写下一格 + 回读。
func TestCycleBuildNextFollowsCaret(t *testing.T) {
	rd := stubRead(t, map[string]string{
		"caret get --scope arrangement":                  `{"trackIndex":3}`,
		"track get --track-index 3 --region arrangement": `{"color":"#ccff31"}`,
	})
	calls, err := cycleBuild(context.Background(), rd, map[string]string{"direction": "next", "readBack": "1"})
	if err != nil {
		t.Fatalf("cycleBuild: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("勾了回读应当 2 条调用（写 + 回读），实得 %d", len(calls))
	}
	want := []string{"track", "set", "--track-index", "3", "--region", "arrangement", "--color", "#859c3f"}
	if strings.Join(calls[0].Args, " ") != strings.Join(want, " ") {
		t.Errorf("写调用 = %v，期望 %v", calls[0].Args, want)
	}
	if strings.Join(calls[1].Args, " ") != "track get --track-index 3 --region arrangement" {
		t.Errorf("回读调用 = %v", calls[1].Args)
	}
}

// 回读**默认关**（照参考脚本的 -NoReadBack）：每步只发一条 track set。
func TestCycleBuildReadBackOffByDefault(t *testing.T) {
	rd := stubRead(t, map[string]string{
		"track get --track-index 1 --region arrangement": `{"color":"#ccff31"}`,
	})
	calls, err := cycleBuild(context.Background(), rd, map[string]string{"trackIndex": "1"})
	if err != nil {
		t.Fatalf("cycleBuild: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("默认不勾回读时应当只有 1 条调用，实得 %d", len(calls))
	}
	if !strings.HasPrefix(strings.Join(calls[0].Args, " "), "track set ") {
		t.Errorf("唯一那条应当是 track set，实得 %v", calls[0].Args)
	}
}

// 给了 fromHex + trackIndex ⇒ **本步一次读都不发**，只有一条 track set。
// stub 表里故意不放任何读命令：真去读了就会以 UNKNOWN_COMMAND 报错，从而抓到回归。
func TestCycleBuildOneCallWhenFromHexGiven(t *testing.T) {
	rd := stubRead(t, map[string]string{})
	calls, err := cycleBuild(context.Background(), rd, map[string]string{
		"trackIndex": "4", "fromHex": "#CCFF31", "direction": "next",
	})
	if err != nil {
		t.Fatalf("给了 fromHex + trackIndex 就不该再读任何东西：%v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("应当只有 1 条调用，实得 %d：%v", len(calls), calls)
	}
	last := calls[0].Args[len(calls[0].Args)-1]
	if last != "#859c3f" { // palette[8] 下一格 = palette[9]
		t.Errorf("应当写到 palette[9] #859c3f，实得 %s", last)
	}
	if !strings.Contains(strings.Join(calls[0].Args, " "), "--track-index 4") {
		t.Errorf("应当用给进来的轨道索引，实得 %v", calls[0].Args)
	}
}

func TestCycleBuildPrevAndWrap(t *testing.T) {
	rd := stubRead(t, map[string]string{
		"track get --track-index 0 --region arrangement": `{"color":"#ec4f44"}`, // palette[0]
	})
	calls, err := cycleBuild(context.Background(), rd, map[string]string{
		"trackIndex": "0", "direction": "prev", "readBack": "0",
	})
	if err != nil {
		t.Fatalf("cycleBuild: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("关掉回读后应当只有 1 条调用，实得 %d", len(calls))
	}
	if calls[0].Args[len(calls[0].Args)-1] != "#ff59ac" { // palette[0] 往回一格 = palette[27]
		t.Errorf("palette[0] 上一格应当是 palette[27] #ff59ac，实得 %s", calls[0].Args[len(calls[0].Args)-1])
	}
}

// 当前颜色不在调色板里 ⇒ 从 palette[0] 起（参考脚本同款行为）。
func TestCycleBuildOffPaletteStartsAtZero(t *testing.T) {
	rd := stubRead(t, map[string]string{
		"track get --track-index 2 --region arrangement": `{"color":"#123456"}`,
	})
	calls, err := cycleBuild(context.Background(), rd, map[string]string{"trackIndex": "2", "direction": "next"})
	if err != nil {
		t.Fatalf("cycleBuild: %v", err)
	}
	if calls[0].Args[len(calls[0].Args)-1] != "#ec4f44" {
		t.Errorf("调色板外的颜色应当从 palette[0] 起，实得 %s", calls[0].Args[len(calls[0].Args)-1])
	}
}

// 读失败要把 ACE 的错误信封原样带出来（别退化成"读失败"）。
func TestCycleBuildSurfacesErrorCode(t *testing.T) {
	rd := stubRead(t, map[string]string{}) // 表空 ⇒ 任何读都回 UNKNOWN_COMMAND
	_, err := cycleBuild(context.Background(), rd, map[string]string{"trackIndex": "9"})
	if err == nil {
		t.Fatal("应当报错")
	}
	if !strings.Contains(err.Error(), "UNKNOWN_COMMAND") {
		t.Errorf("错误里应当出现 error.code，实得：%v", err)
	}
}

func TestCatalogIncludesWriteTool(t *testing.T) {
	if Get("color.cycle") == nil {
		t.Fatal("color.cycle 没注册上")
	}
	if !Writable() {
		t.Error("注册表里有写工具，Writable() 应当为 true")
	}
}
