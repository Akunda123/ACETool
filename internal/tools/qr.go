package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"acetool/internal/qr"
)

// 二维码 → 音符（用户 2026-10-20）。参考 SV 脚本 `二维码.js` + `二维码交接.md`。
//
// 几何（用户定）：基准音 **48**；第 r 行的所有暗模块**整体右移 r 个 tick** ——
// 这样同一 onset 上永远只有一个音（ACE 的 Sing clip 是零容差单音，Instrument 轨虽允许重叠，
// 也没必要制造同 onset 的两个音）；模块宽默认 15 tick，音符时值 = 模块宽 - 1。
func init() {
	register(&Tool{
		ID: "qr.apply", Category: "轨道",
		Title: "生成二维码（写成音符）",
		Desc: "把一段短文本编成二维码，**以音符写进 ACE**：暗模块 = 一个音，亮模块 = 留空。" +
			"几何：基准音 48、模块宽默认 15 tick、第 r 行整体右移 r tick（同 onset 不会有两个音）、" +
			"时值 = 模块宽 - 1。" +
			"⚠️ 默认**新建一条 Instrument 轨**（单轨、可编辑、不锁）；也可给 `trackIndex` 写进已有轨。" +
			"⚠️ 文本越长版本越大（v1 21×21 ≈ 224 音；v3 29×29 ≈ 450 音），音符很多时命令行会超长 ⇒ 会直接拒，" +
			"别指望一次画一本册子。",
		Kind:   KindWrite,
		Source: "自建：`internal/qr`（移植 二维码.js 的 Arase 编码器）+ `track create` / `clip create`",
		Params: []Param{
			textParam("text", "要编码的文本", "", "https://example.com", "短文本最稳（v1–v3）"),
			numParam("moduleTicks", "模块宽度（tick）", "15"),
			numParam("basePitch", "基准音（第 0 行）", "48"),
			numParam("posTicks", "片段起点（tick，0 = 工程开头）", "0"),
			textParam("name", "新轨/片段名", "QR", "QR-clip", "留空用 QR"),
			numParam("trackIndex", "写进已有轨（留空 = 新建 Instrument 轨）", ""),
			textParam("mode", "形态：auto / instrument / sing", "auto", "auto",
				"sing 轨是单音、不能叠 ⇒ 只能**顺序排成旋律**（由二维码派生，不是 2D 图）"),
		},
		Plan: qrPlan,
		Apply: qrApply,
	})
}

type qrNote struct {
	Pos   int64  `json:"pos"`
	Dur   int64  `json:"dur"`
	Pitch int    `json:"pitch"`
	Lyric string `json:"lyric,omitempty"`
}

type qrPlanData struct {
	Text     string
	Version  int
	Size     int
	Dark     int
	Module   int64
	Step     int64
	Base     int
	Pos      int64
	TrackIdx int // -1 = 新建
	Mode     string // instrument（2D 图）/ sing（顺序旋律）
	Name     string
	Notes    []qrNote
	TotalDur int64
	MaxPos   int64
}

func buildQRPlan(vals map[string]string) (*qrPlanData, error) {
	text := strings.TrimSpace(vals["text"])
	if text == "" {
		return nil, fmt.Errorf("要编码的文本是空的")
	}
	module := int64(15)
	if s := strings.TrimSpace(vals["moduleTicks"]); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 2 || n > 480 {
			return nil, fmt.Errorf("模块宽度要在 2..480 tick 之间（收到 %q）", s)
		}
		module = n
	}
	base := 48
	if s := strings.TrimSpace(vals["basePitch"]); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 10 || n > 120 {
			return nil, fmt.Errorf("基准音要在 10..120 之间（收到 %q）", s)
		}
		base = n
	}
	pos := int64(0)
	if s := strings.TrimSpace(vals["posTicks"]); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("片段起点要是非负 tick（收到 %q）", s)
		}
		pos = n
	}
	track := -1
	if s := strings.TrimSpace(vals["trackIndex"]); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("轨道下标要是 ≥0 的整数（收到 %q）", s)
		}
		track = n
	}
	name := strings.TrimSpace(vals["name"])
	if name == "" {
		name = "QR"
	}
	mode := strings.TrimSpace(vals["mode"])
	if mode == "" {
		mode = "auto"
	}
	if mode != "instrument" && mode != "sing" && mode != "auto" {
		return nil, fmt.Errorf("mode 只能是 auto / instrument / sing（收到 %q）", mode)
	}
	// sing 轨（洛天依这种歌声轨）零容差单音 ⇒ 只能**顺序排成旋律**：
	// 每个暗模块一个音、首尾相接（dur = 间距，绝不重叠）。
	// 用户 2026-10-20：「把音符长度放大四倍」⇒ sing 的默认模块宽 = 15×4 = 60 tick。
	if mode == "sing" && strings.TrimSpace(vals["moduleTicks"]) == "" {
		module = 60
	}
	c, err := qr.Encode(text)
	if err != nil {
		return nil, err
	}
	plan := &qrPlanData{
		Text: text, Version: c.Version, Size: c.Size, Dark: c.DarkCount(),
		Module: module, Base: base, Pos: pos, TrackIdx: track, Mode: mode, Name: name,
	}
	if mode == "sing" {
		i := int64(0)
		for r := 0; r < c.Size; r++ {
			for col := 0; col < c.Size; col++ {
				if !c.Module[r][col] {
					continue
				}
				plan.Notes = append(plan.Notes, qrNote{
					Pos: i * module, Dur: module, Pitch: base + r, Lyric: "la",
				})
				i++
			}
		}
		plan.TotalDur = i * module
		plan.Step = module
		if len(plan.Notes) == 0 {
			return nil, fmt.Errorf("这张二维码一个暗模块都没有？")
		}
		if len(plan.Notes) > 400 {
			return nil, fmt.Errorf("这张二维码有 **%d** 个暗模块（v%d），命令行放不下（上限约 400）⇒ 换更短的文本",
				len(plan.Notes), c.Version)
		}
		return plan, nil
	}
	// 暗模块 → 音符：第 r 行整体右移 r tick（用户 2026-10-20：**就 1 tick**，别放大），时值 = 模块宽 - 1。
	// 列间距 = 模块宽（保持格子方正）；行数 > 模块宽时，个别 (行,列) 组合会撞同一个 onset
	// —— Instrument 轨允许复调、不报错；Sing 轨会被拒（这种情况工具会提示换大一点的模块宽）。
	step := module
	plan.Step = step
	for r := 0; r < c.Size; r++ {
		for col := 0; col < c.Size; col++ {
			if !c.Module[r][col] {
				continue
			}
			start := int64(col)*step + int64(r)
			plan.Notes = append(plan.Notes, qrNote{
				Pos: start, Dur: module - 1, Pitch: base + r,
			})
			if start+module-1 > plan.MaxPos {
				plan.MaxPos = start + module - 1
			}
		}
	}
	plan.TotalDur = plan.MaxPos + 1
	if len(plan.Notes) == 0 {
		return nil, fmt.Errorf("这张二维码一个暗模块都没有？")
	}
	// ⚠️ 音符 JSON 是**走命令行**的（`clip create --notes` 不吃 @file）⇒ Windows 32 KB 上限。
	// 留够余量：一条 ~55 字节 ⇒ 上限 ~400 个音，超过就直说，别写到一半失败。
	if len(plan.Notes) > 400 {
		return nil, fmt.Errorf("这张二维码有 **%d** 个暗模块（v%d %d×%d），命令行放不下（上限约 400）⇒ "+
			"换更短的文本，或把模块宽调大后手工裁剪", len(plan.Notes), c.Version, c.Size, c.Size)
	}
	return plan, nil
}

func qrPlan(_ context.Context, _ ReadFunc, vals map[string]string) (any, error) {
	p, err := buildQRPlan(vals)
	if err != nil {
		return nil, err
	}
	return struct {
		Grid            []string `json:"grid"`
		Text            string `json:"text"`
		Version         int    `json:"version"`
		Size            int    `json:"size"`
		Dark            int    `json:"dark"`
		ModuleTicks     int64  `json:"moduleTicks"`
		BasePitch       int    `json:"basePitch"`
		PitchRange      string `json:"pitchRange"`
		ClipTicks       int64  `json:"clipTicks"`
		Target          string `json:"target"`
		Hint            string `json:"hint"`
	}{
		gridRows(p.Text),
		p.Text, p.Version, p.Size, p.Dark, p.Module, p.Base,
		fmt.Sprintf("%d…%d", p.Base, p.Base+p.Size-1),
		p.TotalDur,
		map[bool]string{true: "新建 Instrument 轨", false: fmt.Sprintf("写进轨道 %d", p.TrackIdx)}[p.TrackIdx < 0],
		fmt.Sprintf("%d 个暗模块 ⇒ %d 个音符；片段长 %d tick（%.2f 拍）；每行右移 1 tick（同 onset 不会有两个音）",
			p.Dark, len(p.Notes), p.TotalDur, float64(p.TotalDur)/480),
	}, nil
}

func qrApply(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	p, err := buildQRPlan(vals)
	if err != nil {
		return nil, err
	}
	var done []string
	track := p.TrackIdx
	if track < 0 {
		name := p.Name
		res, err := runWriteRes(ctx, rd, "track", "create", "--type", "instrument", "--name", name)
		if err != nil {
			return nil, fmt.Errorf("建轨：%w", err)
		}
		var out struct {
			TrackIndex int `json:"trackIndex"`
		}
		if len(res.JSON) > 0 {
			_ = json.Unmarshal(res.JSON, &out)
		}
		if out.TrackIndex <= 0 && out.TrackIndex < 0 {
			return nil, fmt.Errorf("建轨回包里没有 trackIndex：%s", strings.TrimSpace(string(res.JSON)))
		}
		track = out.TrackIndex
		done = append(done, fmt.Sprintf("新建 Instrument 轨 #%d（%s）", track, name))
	}
	// 让开已有片段（ACE 对 `clip create` 的重叠是硬拒）
	// ⚠️ `clip list` 回的是 `{clips:[{clipBegin,clipEnd,…}]}`，不是裸数组 ⇒ 必须按对象解，
	// 解错就会静默当成"没有片段"，然后撞上 CLIP_OVERLAP（2026-10-20 真踩过）。
	var cl struct {
		Clips []struct {
			Begin int64 `json:"clipBegin"`
			End   int64 `json:"clipEnd"`
		} `json:"clips"`
	}
	if err := readJSON(ctx, rd, &cl, "clip", "list", "--track-index", strconv.Itoa(track)); err != nil {
		return nil, fmt.Errorf("读轨道 %d 的片段列表（要避开已有片段）：%w", track, err)
	}
	clips := make([]clipBrief, 0, len(cl.Clips))
	for _, c := range cl.Clips {
		clips = append(clips, clipBrief{Begin: c.Begin, End: c.End})
	}
	start := freeClipStart(clips, p.Pos, p.TotalDur)
	if start != p.Pos {
		done = append(done, fmt.Sprintf("起点从 %d 顺延到 %d（避开已有片段）", p.Pos, start))
	}
	js, err := json.Marshal(p.Notes)
	if err != nil {
		return nil, err
	}
	if len(js) > 20000 {
		return nil, fmt.Errorf("音符 JSON 有 %d 字节，超过命令行安全上限 ⇒ 换更短的文本", len(js))
	}
	clipType := "instrument"
	if p.Mode == "sing" {
		clipType = "sing"
	}
	res, err := runWriteRes(ctx, rd, "clip", "create", "--track-index", strconv.Itoa(track),
		"--pos", fmt.Sprintf("%dt", start), "--dur", fmt.Sprintf("%dt", p.TotalDur),
		"--type", clipType, "--name", p.Name, "--notes", string(js))
	if err != nil {
		return nil, fmt.Errorf("建片段并写音符：%w", err)
	}
	var out struct {
		ClipUUID  string `json:"clipUuid"`
		NoteCount int    `json:"noteCount"`
	}
	if len(res.JSON) > 0 {
		_ = json.Unmarshal(res.JSON, &out)
	}
	done = append(done, fmt.Sprintf("片段 %s：写了 %d 个音（期望 %d）", out.ClipUUID, out.NoteCount, len(p.Notes)))
	if out.ClipUUID != "" && out.NoteCount != 0 && out.NoteCount != len(p.Notes) {
		return map[string]any{
			"ok": false, "did": done,
			"hint": fmt.Sprintf("宿主回包里只有 %d 个音，而我们要 %d 个 ⇒ 音符可能落在片段外（clip create 退出码仍是 0）", out.NoteCount, len(p.Notes)),
		}, nil
	}
	return map[string]any{
		"ok": true, "track": track, "clipUuid": out.ClipUUID, "notes": len(p.Notes),
		"version": p.Version, "size": p.Size, "dark": p.Dark, "did": done,
		"hint": fmt.Sprintf("v%d %d×%d · 基准音 %d · 模块 %d tick · 第 r 行右移 r tick",
			p.Version, p.Size, p.Size, p.Base, p.Module),
	}, nil
}
