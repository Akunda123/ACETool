package tools

// 二维码 → **文件路线**（用户 2026-10-20 定：真 2D 图必须这么写）。
//
// 为什么不能走 CLI：Sing clip 是零容差单音，2D 图必然有重叠 ⇒ `clip create` 会被
// `NOTE_OVERLAP` 直接拒。交接文档 §1-C 的形态是"文件能载入、CLI 判非法"：
// 先用 CLI 建一个**合法**的模板片段（1 个音），再只替换文件里的 `notes` 数组。
//
// ⚠️ 写完这个片段就**不能用 CLI 编辑**了（引擎判非法），文件载入时 ACE 会标 `dirty`
// —— 这些都在 Plan 里如实告诉用户。

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"acetool/internal/acep"
	"acetool/internal/cbor"
	"acetool/internal/qr"
)

func init() {
	register(&Tool{
		ID: "qr.acep", Category: "轨道",
		Title: "生成二维码（2D 图，改工程文件）",
		Desc: "把文本编成二维码，**按真正的 2D 摆法**写进一条 **Sing 轨**的片段里：" +
			"暗模块 = 一个音（pos = 列 × 模块宽 + 行，音高 = 基准音 + 行）。" +
			"流程：CLI 建一个**合法**的模板片段（1 个音）→ `project save` → 改 `.acep` 的 notes 数组 → " +
			"`project open` 重载 → 读回核对。" +
			"⚠️ 这个片段之后**不能被 CLI 编辑**（重叠被判非法，交接文档 §1-C 的已知形态）；" +
			"⚠️ 必须在**已保存的工程**上用；⚠️ 会先备份到临时目录。",
		Kind:   KindWrite,
		Source: "自建：`internal/qr` + `internal/acep`（改文件，绕开 Sing 单音校验）",
		Params: []Param{
			textParam("text", "要编码的文本", "", "", "短文本（v1 21×21 = 224 音）"),
			textParam("acepPath", "工程文件路径（.acep，必须先保存）", "", `%TEMP%\…\qr.acep`, "当前工程若还是临时工程，先在 ACE 里另存"),
			numParam("trackIndex", "写进哪条 Sing 轨（0 起）", "0"),
			numParam("moduleTicks", "模块宽（tick）", "60"),
			numParam("basePitch", "基准音（最低，第 0 行）", "48"),
			textParam("name", "片段名", "QR2D", "QR2D", "留空用 QR2D"),
		},
		Plan:  qrFilePlan,
		Apply: qrFileApply,
	})
}

type qrFileData struct {
	Text    string
	Path    string
	Track   int
	Module  int64
	Base    int
	Name    string
	Size    int
	Dark    int
	Notes   []struct{ Pos, Dur int64; Pitch int }
	Stagger int64
	ClipDur int64
	Pos     int64 // -1 = 接在已有片段后面（用户给了 posTicks 就用它）
}

func buildQRFilePlan(vals map[string]string) (*qrFileData, error) {
	text := strings.TrimSpace(vals["text"])
	if text == "" {
		return nil, fmt.Errorf("要编码的文本是空的")
	}
	// 路径**不用用户填**：与其它文件路线工具一样，从 `project dirty` 里拿
	// （用户 2026-10-20：「为什么要输入工程文件路径，你看看之前几个改工程的」）。
	// `acepPath` 只作为可选覆盖（比如临时想改另一份）。
	// 与 pitch/tone 那几页一致：页面显式传 `path`（页面的 ① 保存工程已经拿到路径）
	path := strings.TrimSpace(vals["path"])
	if path == "" {
		path = strings.TrimSpace(vals["acepPath"])
	}
	track := 0
	if s := strings.TrimSpace(vals["trackIndex"]); s != "" {
		n, perr := strconv.Atoi(s)
		if perr != nil || n < 0 {
			return nil, fmt.Errorf("轨道下标要是 ≥0 的整数（收到 %q）", s)
		}
		track = n
	}
	module := int64(60)
	if s := strings.TrimSpace(vals["moduleTicks"]); s != "" {
		n, perr := strconv.ParseInt(s, 10, 64)
		if perr != nil || n < 10 || n > 480 {
			return nil, fmt.Errorf("模块宽要在 10..480 tick（收到 %q）", s)
		}
		module = n
	}
	if module < 21 {
		// 列距 < 行数时，(行, 列) 组合会撞同一个 onset —— 2D 图里那会让两个暗模块叠成一个音。
		return nil, fmt.Errorf("模块宽至少要 21 tick（21 行、每行右移 1 tick；再小会有两个暗模块落在同一 onset）")
	}
	base := 48
	if s := strings.TrimSpace(vals["basePitch"]); s != "" {
		n, perr := strconv.Atoi(s)
		if perr != nil || n < 10 || n > 100 {
			return nil, fmt.Errorf("基准音要在 10..100（收到 %q）", s)
		}
		base = n
	}
	name := strings.TrimSpace(vals["name"])
	if name == "" {
		name = "QR2D"
	}
	code, err := qr.Encode(text)
	if err != nil {
		return nil, err
	}
	p := &qrFileData{Text: text, Path: path, Track: track, Module: module, Base: base, Name: name,
		Size: code.Size, Dark: code.DarkCount(), Stagger: 1, Pos: -1}
	if s := strings.TrimSpace(vals["posTicks"]); s != "" {
		n, perr := strconv.ParseInt(s, 10, 64)
		if perr != nil || n < 0 {
			return nil, fmt.Errorf("posTicks 要是非负整数（收到 %q）", s)
		}
		p.Pos = n
	}
	for r := 0; r < code.Size; r++ {
		for c := 0; c < code.Size; c++ {
			if !code.Module[r][c] {
				continue
			}
			p.Notes = append(p.Notes, struct{ Pos, Dur int64; Pitch int }{
				Pos: int64(c)*module + int64(r), Dur: module - 1, Pitch: base + r,
			})
		}
	}
	p.ClipDur = int64(code.Size-1)*module + int64(code.Size-1) + module
	return p, nil
}

func qrFilePlan(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	p, err := buildQRFilePlan(vals)
	if err != nil {
		return nil, err
	}
	p.Path, err = qrCurrentPath(ctx, rd, p.Path)
	if err != nil {
		return nil, err
	}
	// 顺手确认工程是不是"已保存"，以及轨的类型
	var info struct {
		IsTemp   bool   `json:"isTempProject"`
		Project  string `json:"projectName"`
	}
	_ = readJSON(ctx, rd, &info, "project", "info")
	warn := ""
	if info.IsTemp {
		warn = "⚠️ 当前工程是**临时工程**（未保存）—— 文件路线改不了，请先在 ACE 里另存，再把路径填进 `acepPath`"
	}
	var tl struct {
		Tracks []struct {
			TrackIndex int    `json:"trackIndex"`
			Region     string `json:"region"`
			Type       string `json:"trackType"`
			Name       string `json:"trackName"`
		} `json:"tracks"`
	}
	_ = readJSON(ctx, rd, &tl, "track", "list")
	trackName, trackType := "", ""
	for _, t := range tl.Tracks {
		if t.Region == "arrangement" && t.TrackIndex == p.Track {
			trackName, trackType = t.Name, t.Type
		}
	}
	if trackType != "" && !strings.EqualFold(trackType, "sing") {
		warn = fmt.Sprintf("⚠️ 轨 %d（%s）是 %s，不是 Sing ⇒ 这条路线本来是给 Sing 轨准备的（Instrument 轨请用 `qr.apply`）",
			p.Track, trackName, trackType)
	}
	return map[string]any{
		"grid": gridRows(p.Text),
		"text": p.Text, "version": p.Size, "size": fmt.Sprintf("%d×%d", p.Size, p.Size),
		"dark": p.Dark, "notes": len(p.Notes), "moduleTicks": p.Module, "basePitch": p.Base,
		"pitchRange": fmt.Sprintf("%d…%d", p.Base, p.Base+p.Size-1),
		"clipDur": p.ClipDur, "track": p.Track, "trackName": trackName, "acepPath": p.Path,
		"warn": warn,
		"hint": fmt.Sprintf("写进轨 %d（%s）的片段 %q；写完该片段**不能再用 CLI 编辑**（重叠被判非法，属 §1-C 已知形态）",
			p.Track, trackName, p.Name),
	}, nil
}

func qrFileApply(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {
	p, err := buildQRFilePlan(vals)
	if err != nil {
		return nil, err
	}
	p.Path, err = qrCurrentPath(ctx, rd, p.Path)
	if err != nil {
		return nil, err
	}
	var done []string
	// ① 自由起点（接在已有片段后面）
	var cl struct {
		Clips []struct {
			Name  string `json:"clipName"`
			Begin int64  `json:"clipBegin"`
			End   int64  `json:"clipEnd"`
		} `json:"clips"`
	}
	if err := readJSON(ctx, rd, &cl, "clip", "list", "--track-index", strconv.Itoa(p.Track)); err != nil {
		return nil, fmt.Errorf("读片段列表：%w", err)
	}
	start := int64(0)
	for _, c := range cl.Clips {
		if c.End > start {
			start = c.End
		}
	}
	if p.Pos >= 0 {
		// 用户指定了位置（页面上拖动预览片段）⇒ 必须真的是空位，否则**当场报**，别悄悄挪
		start = p.Pos
		for _, c := range cl.Clips {
			if start < c.End && c.Begin < start+p.ClipDur {
				return nil, fmt.Errorf("这个位置和已有片段 %q [%d,%d) 重叠 ⇒ 换个位置或让它自己顺延（清空 posTicks）",
					c.Name, c.Begin, c.End)
			}
		}
	}
	// ② 建一个**合法**的模板片段（1 个音），名字带时间戳保证唯一
	name := fmt.Sprintf("%s-%d", p.Name, time.Now().Unix()%100000)
	res, err := runWriteRes(ctx, rd, "clip", "create", "--track-index", strconv.Itoa(p.Track),
		"--pos", fmt.Sprintf("%dt", start), "--dur", fmt.Sprintf("%dt", p.ClipDur),
		"--type", "sing", "--name", name,
		"--notes", `[{"pos":0,"dur":60,"pitch":48,"lyric":"la"}]`)
	if err != nil {
		return nil, fmt.Errorf("建模板片段：%w", err)
	}
	var created struct {
		ClipUUID string `json:"clipUuid"`
	}
	_ = json.Unmarshal(res.JSON, &created)
	done = append(done, fmt.Sprintf("建模板片段 %s（1 个音，CLI 合法）", created.ClipUUID))
	// 与其它改工程的工具同一条纪律：**先保存**再改文件（`project dirty` 里才有路径/脏标记）
	if rep, perr := readProjectPath(ctx, rd); perr == nil && rep.Dirty == "有（先「保存工程」再改文件）" {
		done = append(done, "工程本来是脏的 ⇒ 先保存再改文件")
	}
	if _, err := runWriteRes(ctx, rd, "project", "save"); err != nil {
		return nil, fmt.Errorf("保存工程（改文件前必须先落盘）：%w", err)
	}

	// ③ 改文件：**按刚建片段的 uuid 找 pattern**（`findPatternByUUID`，与 pitch 系列同一套），
	//    只替换这个片段的 notes
	cont, err := acep.Open(p.Path)
	if err != nil {
		return nil, err
	}
	root, err := cbor.Decode(cont.Payload)
	if err != nil {
		return nil, err
	}
	track, pat, err := findPatternByUUID(root, created.ClipUUID)
	if err != nil {
		return nil, fmt.Errorf("文件里找不到刚建的片段 %s（`project save` 没落盘？）：%w", created.ClipUUID, err)
	}
	_ = track
	var notesNode *cbor.Node
	for j, k := range pat.Keys {
		if k.KeyString() == "notes" {
			notesNode = pat.Values[j]
		}
	}
	if notesNode == nil || len(notesNode.Items) == 0 {
		return nil, fmt.Errorf("片段 %s 在文件里没有音符（拿不到字段形状）", created.ClipUUID)
	}
	tpl := notesNode.Items[0]
	raw := func(n *cbor.Node) []byte { return cont.Payload[n.Start:n.End] }
	var keys []string
	for _, k := range tpl.Keys {
		keys = append(keys, k.KeyString())
	}
	enc := cbor.EncodeArrayHeader(uint64(len(p.Notes)))
	for _, n := range p.Notes {
		enc = append(enc, cbor.EncodeMapHeader(uint64(len(keys)))...)
		for i, key := range keys {
			enc = append(enc, cbor.EncodeText(key)...)
			switch key {
			case "uuid":
				enc = append(enc, cbor.EncodeText(newNoteUUID())...)
			case "pos":
				enc = append(enc, cbor.EncodeUint(uint64(n.Pos))...)
			case "dur":
				enc = append(enc, cbor.EncodeUint(uint64(n.Dur))...)
			case "pitch":
				enc = append(enc, cbor.EncodeUint(uint64(n.Pitch))...)
			case "lyric":
				enc = append(enc, cbor.EncodeText("la")...)
			default:
				enc = append(enc, raw(tpl.Values[i])...)
			}
		}
	}
	newPay, err := cbor.Apply(cont.Payload, []cbor.Edit{{
		Start: notesNode.Start, End: notesNode.End, Bytes: enc, What: "notes → 二维码",
	}})
	if err != nil {
		return nil, err
	}
	frame, err := cont.Repack(newPay)
	if err != nil {
		return nil, err
	}
	old, err := os.ReadFile(p.Path)
	if err != nil {
		return nil, err
	}
	// 与 pitch/tone 系列同一个备份落点：`%TEMP%\ACETool\acep-backup\`（`writeBackup` 自带回读校验）
	bak, err := writeBackup(p.Path, old)
	if err != nil {
		return nil, fmt.Errorf("备份失败（不写文件）：%w", err)
	}
	tmp := p.Path + ".acetool-tmp"
	if err := os.WriteFile(tmp, frame, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, p.Path); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	done = append(done, fmt.Sprintf("文件已改（%d 个音），备份 %s", len(p.Notes), bak))

	// ④ 文件级自检（**不重开工程** —— 与 pitch/tone 那几页一致：重开由页面调 `project.open` 做，
	//    因为"重开"会清撤销栈，属于用户可见的一步，不该藏在工具里）
	self := []string{}
	if re, err := acep.Open(p.Path); err != nil {
		self = append(self, "重新打开文件失败："+err.Error())
	} else if re.Payload != nil && len(re.Payload) != len(newPay) {
		self = append(self, fmt.Sprintf("回读 payload 长度 %d ≠ 写进去的 %d", len(re.Payload), len(newPay)))
	} else {
		self = append(self, fmt.Sprintf("回读一致：payload %d 字节", len(newPay)))
	}
	done = append(done, self...)
	return map[string]any{
		"ok": true, "轨道": fmt.Sprintf("%d", p.Track), "片段": created.ClipUUID,
		"尺寸": fmt.Sprintf("%d×%d", p.Size, p.Size), "暗模块": p.Dark, "音符": len(p.Notes),
		"片段长": p.ClipDur, "备份": bak, "自检": self, "步骤": done,
		"提示": "这个片段写完**不能再被 CLI 编辑**（重叠被判非法）；要改就删掉重来。" +
			"接下来让 ACE 重开工程（页面会调 `project.open`）才会看到新内容。",
	}, nil
}

func newNoteUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("{%x-%x-%x-%x-%x}", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
// gridRows 把二维码画成 ".#" 行（给页面画缩略图/预览用）。
func gridRows(text string) []string {
	code, err := qr.Encode(text)
	if err != nil {
		return nil
	}
	rows := make([]string, 0, code.Size)
	for _, row := range code.Module {
		b := make([]byte, 0, len(row))
		for _, dark := range row {
			if dark {
				b = append(b, '#')
			} else {
				b = append(b, '.')
			}
		}
		rows = append(rows, string(b))
	}
	return rows
}

// qrCurrentPath 拿"要改的那份工程文件"：优先参数覆盖，否则与其它文件路线工具一样
// 从 `project dirty` 里读当前工程路径（`project info` 没有 path 这个键）。
func qrCurrentPath(ctx context.Context, rd ReadFunc, override string) (string, error) {
	if s := strings.TrimSpace(override); s != "" {
		mapped := map[string]string{"path": s}
		return resolveACEPPath(mapped)
	}
	rep, err := readProjectPath(ctx, rd)
	if err != nil {
		return "", err
	}
	if rep.Path == "" {
		return "", fmt.Errorf("当前工程还没有文件路径（临时工程）⇒ 先在 ACE 里「另存为」一个工程包，再试。%s", rep.Note)
	}
	return rep.Path, nil
}
