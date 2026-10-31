package acep

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"acetool/internal/cbor"
)

// 造一个载荷：{"name":"Track A","notes":[{"lyric":"la"}],"v":[0,1,2]}
func samplePayload(name string) []byte {
	var b []byte
	b = append(b, cbor.EncodeMapHeader(3)...)
	b = append(b, cbor.EncodeText("name")...)
	b = append(b, cbor.EncodeText(name)...)
	b = append(b, cbor.EncodeText("notes")...)
	b = append(b, cbor.EncodeArrayHeader(1)...)
	b = append(b, cbor.EncodeMapHeader(1)...)
	b = append(b, cbor.EncodeText("lyric")...)
	b = append(b, cbor.EncodeText("la")...)
	b = append(b, cbor.EncodeText("v")...)
	b = append(b, cbor.EncodeArrayHeader(3)...)
	for _, v := range []float64{0, 1, 2} {
		b = append(b, cbor.EncodeFloat64(v)...)
	}
	return b
}

func allChecksPass(t *testing.T, res *PatchResult) {
	t.Helper()
	for _, c := range res.Checks {
		if !c.OK {
			t.Errorf("自检没过：%s —— %s", c.Name, c.Note)
		}
	}
	if !res.AllOK {
		t.Errorf("AllOK 应当是 true（检查项：%d 条）", len(res.Checks))
	}
	if len(res.Checks) == 0 {
		t.Error("一条自检都没有 —— 那等于没自检")
	}
}

func TestPatchTextOnACEP2(t *testing.T) {
	payload := samplePayload("Track A")
	raw := makeACEP2(encodeFrame(t, payload), len(payload))
	c, err := parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res, err := c.PatchText("name", "Track B")
	if err != nil {
		t.Fatalf("PatchText: %v", err)
	}
	allChecksPass(t, res)

	// 原文件一个字节都不许动
	if !bytes.Equal(c.raw, raw) {
		t.Error("原文件字节被改动了")
	}
	// 新文件能再解析，且块内只有那一处文本变了
	c2, err := Parse(res.Bytes)
	if err != nil {
		t.Fatalf("新文件解不开：%v", err)
	}
	root, err := cbor.Decode(c2.Payload)
	if err != nil {
		t.Fatalf("新 payload 解不开：%v", err)
	}
	if n := root.Get("name"); n == nil || n.Text != "Track B" {
		t.Errorf("name = %+v", n)
	}
	if n := root.Get("notes"); n == nil || len(n.Items) != 1 {
		t.Errorf("notes 被动了：%+v", n)
	}
	// 载荷只该多出「文本编码之差」
	wantDelta := len(cbor.EncodeText("Track B")) - len(cbor.EncodeText("Track A"))
	if res.NewPayload-res.OldPayload != wantDelta {
		t.Errorf("payload 变化应当是 %+d，实得 %+d", wantDelta, res.NewPayload-res.OldPayload)
	}
}

func TestPatchFloat64ArrayOnACEP2(t *testing.T) {
	payload := samplePayload("T")
	raw := makeACEP2(encodeFrame(t, payload), len(payload))
	c, _ := parse(raw)
	res, err := c.PatchFloat64Array("v", []float64{0.5, 1.5, 2.5})
	if err != nil {
		t.Fatalf("PatchFloat64Array: %v", err)
	}
	allChecksPass(t, res)
	if res.NewPayload != res.OldPayload {
		t.Errorf("原位覆盖长度必须不变：%d → %d", res.OldPayload, res.NewPayload)
	}
	c2, _ := Parse(res.Bytes)
	root, err := cbor.Decode(c2.Payload)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	v, _ := cbor.Get(root, "v")
	for i, want := range []float64{0.5, 1.5, 2.5} {
		if v.Items[i].Float != want {
			t.Errorf("v[%d] = %v，期望 %v", i, v.Items[i].Float, want)
		}
	}
}

// ★ 技能 §1.1 的强断言：头**除 off16/off24** 之外逐字节相同（含 1248 B 高熵区）。
func TestHeaderIsCopiedVerbatimExceptLengths(t *testing.T) {
	payload := samplePayload("Track A")
	raw := makeACEP2(encodeFrame(t, payload), len(payload))
	// 往"高熵区"里塞点非零字节，确保逐字节比对真的有内容可比
	for i := 32; i < 1280; i++ {
		raw[i] = byte(i * 7)
	}
	c, err := parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res, err := c.PatchText("name", "A Much Longer Track Name")
	if err != nil {
		t.Fatalf("PatchText: %v", err)
	}
	allChecksPass(t, res)

	// 头 1280 字节：只有 16..32 允许不同
	for i := 0; i < 1280; i++ {
		if i >= 16 && i < 32 {
			continue
		}
		if res.Bytes[i] != raw[i] {
			t.Fatalf("头第 %d 字节被改了：源 0x%02x 新 0x%02x", i, raw[i], res.Bytes[i])
		}
	}
	// off16/off24 必须等于新值
	if got := binary.LittleEndian.Uint64(res.Bytes[16:24]); int(got) != res.NewFileSize-1280 {
		t.Errorf("off16 = %d，应当是 文件大小-头长 = %d", got, res.NewFileSize-1280)
	}
	if got := binary.LittleEndian.Uint64(res.Bytes[24:32]); int(got) != res.NewPayload {
		t.Errorf("off24 = %d，应当是 %d", got, res.NewPayload)
	}
	if got := binary.LittleEndian.Uint32(res.Bytes[8:12]); got != 1280 {
		t.Errorf("off8 头长不该变，实得 %d", got)
	}
}

// 路径不存在 / 类型不对时，必须**拒绝**并给出可读的话，而不是改坏文件。
func TestPatchRejectsBadTargets(t *testing.T) {
	payload := samplePayload("Track A")
	raw := makeACEP2(encodeFrame(t, payload), len(payload))
	c, _ := parse(raw)

	if _, err := c.PatchText("nope", "x"); err == nil {
		t.Error("不存在的路径应当报错")
	} else if !strings.Contains(err.Error(), "有的键") {
		t.Errorf("错误里应当列出可用的键：%v", err)
	}
	if _, err := c.PatchText("notes", "x"); err == nil {
		t.Error("对非文本节点做 SetText 应当报错")
	}
	if _, err := c.PatchFloat64Array("v", []float64{1, 2}); err == nil {
		t.Error("元素个数不一致应当报错")
	}
	if _, err := c.PatchFloat64Array("name", []float64{1}); err == nil {
		t.Error("对非数组做数组覆盖应当报错")
	}
}

// 历史壳一：走 set-json（内层是 JSON 文本）。
func TestPatchTextOnLegacyReadableShell(t *testing.T) {
	inner, _ := json.Marshal(map[string]any{
		"version": 1000,
		"tracks":  []any{map[string]any{"name": "Old Name", "gain": 0}},
	})
	env, _ := json.Marshal(map[string]any{
		"compressMethod": "zstd",
		"content":        base64.StdEncoding.EncodeToString(encodeFrame(t, inner)),
		"version":        1000,
	})
	c, err := parse(env)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res, err := c.PatchText("tracks[0].name", "New Name")
	if err != nil {
		t.Fatalf("PatchText: %v", err)
	}
	allChecksPass(t, res)

	c2, err := Parse(res.Bytes)
	if err != nil {
		t.Fatalf("新文件解不开：%v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(c2.Payload, &doc); err != nil {
		t.Fatalf("新内层不是合法 JSON：%v", err)
	}
	tr := doc["tracks"].([]any)[0].(map[string]any)
	if tr["name"] != "New Name" {
		t.Errorf("name = %v", tr["name"])
	}
	if tr["gain"] != float64(0) {
		t.Errorf("别的键被动了：%v", tr)
	}
	if doc["version"] != float64(1000) {
		t.Errorf("顶层别的键被动了：%v", doc["version"])
	}
}

func TestRepackRejectsEncryptedShell(t *testing.T) {
	c, err := parse([]byte(`{"content":"AAAA","version":2}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Info.Kind != KindLegacyEncrypted {
		t.Fatalf("应当认成加密壳，实得 %s", c.Info.Kind)
	}
	if _, err := c.Repack([]byte("whatever")); err == nil {
		t.Error("加密壳不该能重建")
	}
	if _, err := c.PatchText("a", "b"); err == nil {
		t.Error("加密壳不该能改写")
	}
}

// 落盘：只写副本 + 按"工程包"布局（技能 §5.2）。目标已存在时**拒绝覆盖**。
func TestWritePackageLayout(t *testing.T) {
	dir := t.TempDir()
	orig := []byte("ORIGINAL-BYTES")
	patched := []byte("PATCHED-BYTES-LONGER")

	made, err := WritePackage(dir, "我的工程", orig, patched)
	if err != nil {
		t.Fatalf("WritePackage: %v", err)
	}
	if len(made) != 3 {
		t.Fatalf("应当产出 3 项（主文件 / 快照 / Samples），实得 %d：%+v", len(made), made)
	}
	main := filepath.Join(dir, "我的工程", "我的工程.acep")
	if b, err := os.ReadFile(main); err != nil || !bytes.Equal(b, patched) {
		t.Errorf("主文件内容不对：%v", err)
	}
	// Samples 空目录
	if st, err := os.Stat(filepath.Join(dir, "我的工程", "Samples")); err != nil || !st.IsDir() {
		t.Errorf("Samples 目录没建出来：%v", err)
	}
	// autosave 里应当是**改动前**的原文件
	snaps, _ := filepath.Glob(filepath.Join(dir, "我的工程", "autosave", "*.acep"))
	if len(snaps) != 1 {
		t.Fatalf("应当有 1 份快照，实得 %d：%v", len(snaps), snaps)
	}
	if b, _ := os.ReadFile(snaps[0]); !bytes.Equal(b, orig) {
		t.Error("快照应当是改动前的原文件")
	}
	if !strings.Contains(filepath.Base(snaps[0]), "我的工程_save_") {
		t.Errorf("快照命名应当形如 <名字>_save_<时间戳>.acep，实得 %s", filepath.Base(snaps[0]))
	}

	// 再写一次同一个名字 ⇒ 拒绝覆盖
	if _, err := WritePackage(dir, "我的工程", orig, patched); err == nil {
		t.Error("目标已存在时应当拒绝覆盖")
	}
}
