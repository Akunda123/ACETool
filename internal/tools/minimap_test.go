package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"acetool/internal/acecli"
)

// 缩略图专用假 CLI —— **自带一套**，不复用 `newScopeFake`：
// 那个是给 scope 类工具准备的，它不认识 `project info` / `track list` / `timesig` / `tempo`，
// 硬塞进去会变成"为了测 A 去改 B 的假数据"，两边都容易失真。
func newMinimapFake(t *testing.T, called *[]string, tracks string, clips map[int]string, notes map[string]string) ReadFunc {
	t.Helper()
	return func(_ context.Context, args ...string) (*acecli.Result, error) {
		cmd := strings.Join(args, " ")
		*called = append(*called, cmd)
		// 缩略图是**只读**工具：任何写动词出现就直接失败（这条比断言"没调用"更早暴露问题）
		if len(args) >= 2 {
			switch args[0] + " " + args[1] {
			case "note add", "note move", "clip create", "clip delete", "track create", "track delete",
				"selection set", "editor open", "midiparam write", "vocalparam write":
				return nil, fmt.Errorf("缩略图不许调写命令：%s", cmd)
			}
		}
		switch {
		case len(args) >= 2 && args[0] == "project" && args[1] == "info":
			return okJSON(`{"duration":278400,"projectName":"测试工程"}`), nil
		case len(args) >= 2 && args[0] == "timesig":
			return okJSON(`{"signatures":[{"numerator":4,"denominator":4}]}`), nil
		case len(args) >= 2 && args[0] == "tempo":
			return okJSON(`{"points":[{"pos":0,"value":120.0,"bend":0.0}]}`), nil
		case len(args) >= 2 && args[0] == "transport":
			return okJSON(`{"positionTick":11760,"status":"stopped"}`), nil
		case len(args) >= 2 && args[0] == "track" && args[1] == "list":
			return okJSON(tracks), nil
		case len(args) >= 2 && args[0] == "clip" && args[1] == "list":
			idx := 0
			for i, a := range args {
				if a == "--track-index" && i+1 < len(args) {
					fmt.Sscanf(args[i+1], "%d", &idx)
				}
			}
			body, ok := clips[idx]
			if !ok {
				return okJSON(`{"clips":[]}`), nil
			}
			return okJSON(body), nil
		case len(args) >= 2 && args[0] == "clip" && args[1] == "note-content":
			key := ""
			for i, a := range args {
				if (a == "--track-index" || a == "--clip-index") && i+1 < len(args) {
					key += args[i+1] + "/"
				}
			}
			if body, ok := notes[key]; ok {
				return okJSON(body), nil
			}
			return okJSON(`{"noteCount":0,"notes":[]}`), nil
		}
		return nil, fmt.Errorf("假 CLI 没准备这条命令：%s", cmd)
	}
}

const mmTracks = `{"contentTrackCount":2,"tracks":[
  {"trackIndex":0,"trackUuid":"{t0}","trackName":"Ember Rose","trackType":"Sing","region":"arrangement","clipCount":1,"soundSourceName":"Ember Rose"},
  {"trackIndex":2,"trackUuid":"{t2}","trackName":"MIDI 音轨","trackType":"GenericMidi","region":"arrangement","clipCount":1}
]}`

func TestMinimapAssemblesTracksClipsAndNotes(t *testing.T) {
	var called []string
	fake := newMinimapFake(t, &called,
		mmTracks,
		map[int]string{
			0: `{"clips":[{"clipIndex":0,"clipUuid":"{c0}","clipName":"啦啦啦啦","clipType":"sing","clipBegin":3120,"clipEnd":10800,"clipColor":"#859C3F","noteCount":2,"enabled":true}]}`,
			2: `{"clips":[{"clipIndex":0,"clipUuid":"{c2}","clipName":"Close Grand 2","clipType":"genericMidi","clipBegin":1440,"clipEnd":228000,"clipColor":"#95FFA8","noteCount":1,"enabled":true}]}`,
		},
		map[string]string{
			"0/0/": `{"noteCount":2,"notes":[
			  {"pos":240,"dur":660,"pitch":64,"lyric":"啦"},
			  {"pos":900,"dur":1020,"pitch":62,"lyric":"啦"}]}`,
			"2/0/": `{"noteCount":1,"notes":[{"pos":0,"dur":480,"pitch":48}]}`,
		})

	got, err := readMinimap(context.Background(), fake, map[string]string{"notes": "24"})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if got.TrackCount != 2 || got.ClipCount != 2 {
		t.Fatalf("应当 2 轨 / 2 片段，实得 %d / %d", got.TrackCount, got.ClipCount)
	}
	if got.TrackCount != len(got.Tracks) {
		t.Errorf("TrackCount 要与 tracks 长度一致：%d vs %d", got.TrackCount, len(got.Tracks))
	}
	if got.Meter != "4/4" || got.Tempo != 120 {
		t.Errorf("拍号/速度没读到：%q / %v", got.Meter, got.Tempo)
	}
	if got.Playhead != 11760 {
		t.Errorf("播放头应当是 11760，实得 %d", got.Playhead)
	}
	// 音符：**按 pos 排序** + 音域要算出来（画音符要用它映射纵向位置）
	c0 := got.Tracks[0].Clips[0]
	if len(c0.Notes) != 2 {
		t.Fatalf("片段 0 应当有 2 个音符，实得 %d", len(c0.Notes))
	}
	if c0.Notes[0].Pos != 240 || c0.Notes[1].Pos != 900 {
		t.Errorf("音符要按 pos 升序：%+v", c0.Notes)
	}
	if c0.MinPitch != 62 || c0.MaxPitch != 64 {
		t.Errorf("音域应当是 62..64，实得 %d..%d", c0.MinPitch, c0.MaxPitch)
	}
	if got.NoteCount != 3 {
		t.Errorf("音符总数应当是 3，实得 %d", got.NoteCount)
	}
	// 时间轴范围取**片段覆盖的区间**（不是工程 duration —— 末尾 50400 tick 是空的）
	if got.FirstTick != 1440 || got.LastTick != 228000 {
		t.Errorf("时间轴范围应当是 1440..228000，实得 %d..%d", got.FirstTick, got.LastTick)
	}
	// ⭐ 只读纪律：**一条写命令都不许出现**（用户口径：选中不影响 ACE 侧）
	for _, c := range called {
		for _, w := range []string{"selection set", "editor open", "note add", "clip create", "track create"} {
			if strings.HasPrefix(c, w) {
				t.Errorf("缩略图不该发写命令：%s", c)
			}
		}
	}
	// 空轨不发 clip list（省一条命令）：只应有 2 次 clip list（轨 0 与轨 2）
	n := 0
	for _, c := range called {
		if strings.HasPrefix(c, "clip list") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("应当只对 2 条有内容的轨发 clip list，实得 %d 次", n)
	}
}

// 音符**预算**：超了就如实计数（并把那些片段标成"没读音符"），而不是偷偷不画。
func TestMinimapRespectsNoteBudget(t *testing.T) {
	var called []string
	clips := map[int]string{}
	notes := map[string]string{}
	var trackList strings.Builder
	trackList.WriteString(`{"tracks":[`)
	for i := 0; i < 4; i++ {
		if i > 0 {
			trackList.WriteString(",")
		}
		fmt.Fprintf(&trackList, `{"trackIndex":%d,"trackName":"T%d","trackType":"Sing","region":"arrangement","clipCount":1}`, i, i)
		clips[i] = fmt.Sprintf(`{"clips":[{"clipIndex":0,"clipUuid":"{c%d}","clipName":"C%d","clipType":"sing","clipBegin":0,"clipEnd":1920,"noteCount":1}]}`, i, i)
		notes[fmt.Sprintf("%d/0/", i)] = `{"noteCount":1,"notes":[{"pos":0,"dur":480,"pitch":60}]}`
	}
	trackList.WriteString(`]}`)

	got, err := readMinimap(context.Background(), newMinimapFake(t, &called, trackList.String(), clips, notes),
		map[string]string{"notes": "2"})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if got.ClipsWithoutNotes != 2 {
		t.Errorf("4 个片段、预算 2 ⇒ 应当报 2 个没读，实得 %d", got.ClipsWithoutNotes)
	}
	if got.NoteCount != 2 {
		t.Errorf("只该读到 2 个音符，实得 %d", got.NoteCount)
	}
	var omitted int
	for _, tr := range got.Tracks {
		for _, c := range tr.Clips {
			if c.NotesOmitted {
				omitted++
			}
		}
	}
	if omitted != 2 {
		t.Errorf("2 个片段要标 NotesOmitted（前端据此画斜纹），实得 %d", omitted)
	}
	if !strings.Contains(got.Note, "没读") {
		t.Errorf("收尾说明要如实报「有多少片段没读音符」，实得 %q", got.Note)
	}

	// 预算 0 = 只画片段不画音符
	got0, err := readMinimap(context.Background(), newMinimapFake(t, &called, trackList.String(), clips, notes),
		map[string]string{"notes": "0"})
	if err != nil {
		t.Fatalf("预算 0 不该失败：%v", err)
	}
	if got0.NoteCount != 0 || got0.ClipsWithoutNotes != 4 {
		t.Errorf("预算 0 ⇒ 0 音符 / 4 个没读，实得 %d / %d", got0.NoteCount, got0.ClipsWithoutNotes)
	}
}

// 工具形态：只读、归「自建工具」栏、参数就只有那个预算（**没有 dryRun** —— 它不写）。
func TestMinimapToolIsReadOnlyAndGrouped(t *testing.T) {
	tool := Get("view.minimap")
	if tool == nil {
		t.Fatal("没有注册 view.minimap")
	}
	if tool.Kind != KindRead || tool.Inspect == nil {
		t.Error("view.minimap 应当是只读 + Inspect")
	}
	if tool.Build != nil || tool.Plan != nil || tool.Apply != nil {
		t.Error("只读工具不该有 Build/Plan/Apply")
	}
	if groupOf(tool.Category) != "自建工具" {
		t.Errorf("应当归到「自建工具」栏，实得 %q", groupOf(tool.Category))
	}
	for _, p := range tool.Params {
		if p.Name == "dryRun" {
			t.Error("只读工具不该有 dryRun 参数")
		}
	}
	// ⛔ 那个"在 ACE 里也选中它"的工具**必须不存在**（用户口径：选中不影响 ACE 侧）
	if Get("view.selectClip") != nil {
		t.Error("view.selectClip 不该存在 —— 面板内选中不许碰 ACE 侧")
	}
}
