package tools

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"acetool/internal/acecli"
	"acetool/internal/acep"
	"acetool/internal/cbor"
)

// 端到端：**备份 → 原地改文件 → 回读核对**（这是这个工具唯一"真写"的那条路）。
//
// 造一份最小的 ACEP2 工程（一轨一片段：pitchDelta 一条 data + 一条 anchor、两个音符，
// 第二个带 vibrato），跑 Plan（不写）→ Apply（写）→ 重新读文件核对值。
// `measureToTick` 走 CLI ⇒ 用一个假 ReadFunc（1920 tick/小节）。
func TestPitchWithinApplyBacksUpAndWrites(t *testing.T) {
	dir := t.TempDir()
	projDir := filepath.Join(dir, "歌")
	if err := os.MkdirAll(filepath.Join(projDir, "autosave"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projDir, "歌.acep")
	payload := synthPayload(t)
	cont := wrapACEP2(t, payload)
	if err := os.WriteFile(path, cont, 0o644); err != nil {
		t.Fatal(err)
	}

	rd := func(_ context.Context, args ...string) (*acecli.Result, error) {
		if len(args) >= 2 && args[0] == "convert" && args[1] == "measure-to-tick" {
			bar := int64(1)
			for i, a := range args {
				if a == "--bar" && i+1 < len(args) {
					bar, _ = strconv.ParseInt(args[i+1], 10, 64)
				}
			}
			body := []byte(`{"tick":` + strconv.FormatInt((bar-1)*1920, 10) + `}`)
			return &acecli.Result{Args: args, ExitCode: acecli.ExitOK, Stdout: string(body), JSON: body}, nil
		}
		body := []byte(`{}`)
		return &acecli.Result{Args: args, ExitCode: acecli.ExitOK, Stdout: string(body), JSON: body}, nil
	}

	vals := map[string]string{
		"path": path, "clipUuid": "{clip-1}",
		"srcBeginBar": "1", "srcEndBar": "1", "dstBeginBar": "3",
		"line": "1", "anchor": "1", "vibrato": "1",
	}
	// ① 干跑：一个字节都不写
	planRep, err := pitchWithinPlan(context.Background(), rd, vals)
	if err != nil {
		t.Fatalf("干跑失败：%v", err)
	}
	rep := planRep.(*pitchCopyReport)
	if rep.Line.Summary == "" || rep.Anchor.Summary == "" || rep.Vibrato.Summary == "" {
		t.Fatalf("报告缺项：%+v", rep)
	}
	before, _ := os.ReadFile(path)
	if string(before) != string(cont) {
		t.Fatal("干跑竟然改了文件")
	}

	// ② 真写
	applyRep, err := pitchWithinApply(context.Background(), rd, vals)
	if err != nil {
		t.Fatalf("真写失败：%v", err)
	}
	arep := applyRep.(*pitchCopyReport)
	if arep.Backup == "" {
		t.Fatal("没报备份路径")
	}
	if _, err := os.Stat(arep.Backup); err != nil {
		t.Fatalf("备份文件不在：%v", err)
	}
	back, err := os.ReadFile(arep.Backup)
	if err != nil || string(back) != string(before) {
		t.Fatal("备份内容与改动前不一致")
	}
	// 备份必须落在**临时目录**里（用户 2026-10-08 的要求）
	if tmp := os.TempDir(); len(arep.Backup) < len(tmp) || arep.Backup[:len(tmp)] != tmp {
		t.Fatalf("备份不在临时目录：%s（temp=%s）", arep.Backup, tmp)
	}

	// ③ 回读：文件真的变了，且值对
	after, _ := os.ReadFile(path)
	if string(after) == string(before) {
		t.Fatal("真写之后文件没变")
	}
	c2, err := acep.Open(path)
	if err != nil {
		t.Fatalf("改完的文件打不开：%v", err)
	}
	root, err := cbor.Decode(c2.Payload)
	if err != nil {
		t.Fatalf("改完的 payload 解不开：%v", err)
	}
	_, pat, err := findPatternByUUID(root, "{clip-1}")
	if err != nil {
		t.Fatal(err)
	}
	lane := laneOf(pat, "pitchDelta")
	if lane == nil {
		t.Fatal("找不到 pitchDelta lane")
	}
	// 原来 2 条（data + anchor）；曲线新建 1 条 data、锚点新建 1 条 anchor ⇒ 4 条
	if len(lane.Items) != 4 {
		t.Fatalf("lane 应当变成 4 条 entry，实得 %d", len(lane.Items))
	}
	var newData, newAnchor *cbor.Node
	for _, e := range lane.Items {
		switch nodeText(e.Get("type")) {
		case "data":
			if off, _ := nodeInt(e.Get("offset")); off >= 3840 {
				newData = e
			}
		case "anchor":
			if pts := e.Get("points"); pts != nil && len(pts.Items) >= 2 {
				if t0, _ := nodeInt(pts.Items[0]); t0 >= 3840 {
					newAnchor = e
				}
			}
		}
	}
	if newData == nil {
		t.Fatal("没找到新建的曲线 entry（目标小节那一带）")
	}
	if newAnchor == nil {
		t.Fatal("没找到新建的锚点 entry（目标小节那一带）")
	}
	// 颤音搬到了第 2 个音符（源第 1 个音符带颤音 → 目标 pos = 0+3840）
	notes := pat.Get("notes")
	if len(notes.Items) != 2 {
		t.Fatalf("音符数不对：%d", len(notes.Items))
	}
	if v := notes.Items[1].Get("vibrato"); v == nil || v.Kind != cbor.KMap {
		t.Fatalf("目标音符没拿到颤音：%+v", v)
	}
}

// wrapACEP2 把 payload 包成 `ACEP2` 容器（1280 B 头 + zstd 帧）—— 与 acep 包测试里那份同构。
func wrapACEP2(t *testing.T, payload []byte) []byte {
	t.Helper()
	frame, err := acep.Encode(payload)
	if err != nil {
		t.Fatalf("zstd 失败：%v", err)
	}
	const headLen = 1280
	buf := make([]byte, headLen+len(frame))
	copy(buf, "ACEP2")
	buf[5], buf[6], buf[7] = 1, 1, 0
	binary.LittleEndian.PutUint32(buf[8:12], headLen)
	binary.LittleEndian.PutUint64(buf[16:24], uint64(len(frame)))
	binary.LittleEndian.PutUint64(buf[24:32], uint64(len(payload)))
	copy(buf[headLen:], frame)
	return buf
}

// synthPayload 造一份最小工程（轨名固定"轨 0"）。
func synthPayload(t *testing.T) []byte { return synthPayloadWithName(t, "轨 0") }

// synthPayloadWithName 同上，轨名可换（测试里要造"两个内容不同的合法工程"）。
func synthPayloadWithName(t *testing.T, trackName string) []byte {
	t.Helper()
	dataEntry := cbor.EncodeMap(
		[2]any{"type", "data"},
		[2]any{"offset", 0},
		[2]any{"values", []float64{1, 2, 3, 4, 5, 6, 7, 8}},
	)
	anchorEntry := cbor.EncodeMap(
		[2]any{"type", "anchor"},
		[2]any{"points", []any{100, 61.5}},
		[2]any{"pointsVUV", []float64{1}},
	)
	lane := cbor.EncodeArrayHeader(2)
	lane = append(lane, dataEntry...)
	lane = append(lane, anchorEntry...)
	params := cbor.EncodeMapHeader(1)
	params = append(params, cbor.EncodeText("pitchDelta")...)
	params = append(params, lane...)

	vib := cbor.EncodeMap([2]any{"frequency", 6.0}, [2]any{"amplitude", 2.0})
	note := func(pos int, withVib bool) []byte {
		var n []byte
		if withVib {
			n = cbor.EncodeMapHeader(2)
			n = append(n, cbor.EncodeText("pos")...)
			n = append(n, cbor.EncodeUint(uint64(pos))...)
			n = append(n, cbor.EncodeText("vibrato")...)
			n = append(n, vib...)
			return n
		}
		n = cbor.EncodeMapHeader(1)
		n = append(n, cbor.EncodeText("pos")...)
		n = append(n, cbor.EncodeUint(uint64(pos))...)
		return n
	}
	notes := cbor.EncodeArrayHeader(2)
	notes = append(notes, note(0, true)...)
	notes = append(notes, note(3840, false)...)

	pat := cbor.EncodeMapHeader(3)
	pat = append(pat, cbor.EncodeText("uuid")...)
	pat = append(pat, cbor.EncodeText("{clip-1}")...)
	pat = append(pat, cbor.EncodeText("parameters")...)
	pat = append(pat, params...)
	pat = append(pat, cbor.EncodeText("notes")...)
	pat = append(pat, notes...)

	patterns := cbor.EncodeArrayHeader(1)
	patterns = append(patterns, pat...)
	track := cbor.EncodeMapHeader(2)
	track = append(track, cbor.EncodeText("name")...)
	track = append(track, cbor.EncodeText(trackName)...)
	track = append(track, cbor.EncodeText("patterns")...)
	track = append(track, patterns...)
	tracks := cbor.EncodeArrayHeader(1)
	tracks = append(tracks, track...)

	root := cbor.EncodeMapHeader(2)
	root = append(root, cbor.EncodeText("version")...)
	root = append(root, cbor.EncodeUint(22)...)
	root = append(root, cbor.EncodeText("tracks")...)
	root = append(root, tracks...)
	return root
}
