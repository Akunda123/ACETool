package acep

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func encodeFrame(t *testing.T, payload []byte) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("zstd.NewWriter: %v", err)
	}
	defer enc.Close()
	return enc.EncodeAll(payload, nil)
}

// makeACEP2 手工拼一个新壳（头长固定 1280，与实测一致）。
func makeACEP2(frame []byte, rawLen int) []byte {
	const headLen = 1280
	buf := make([]byte, headLen+len(frame))
	copy(buf, "ACEP2")
	buf[5], buf[6], buf[7] = 1, 1, 0
	binary.LittleEndian.PutUint32(buf[8:12], headLen)
	binary.LittleEndian.PutUint64(buf[16:24], uint64(len(frame)))
	binary.LittleEndian.PutUint64(buf[24:32], uint64(rawLen))
	copy(buf[headLen:], frame)
	return buf
}

func TestParseACEP2(t *testing.T) {
	// ★ 用**实测的真实形状**：顶层是 `b8 1a`（map 走扩展长度形式，26 键），不是想当然的 0xba。
	payload := []byte{0xb8, 0x1a, 0x62, 0x68, 0x69}
	c, err := parse(makeACEP2(encodeFrame(t, payload), len(payload)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Info.Kind != KindACEP2 || !c.Info.LengthOK {
		t.Errorf("应当认成 ACEP2 且长度自检通过：%+v", c.Info)
	}
	if !bytes.Equal(c.Payload, payload) {
		t.Errorf("payload 不一致")
	}
	if !strings.Contains(DescribeHead(c.Payload), "CBOR map(26 键)") {
		t.Errorf("首字节描述不对：%s", DescribeHead(c.Payload))
	}
}

func TestParseACEP2LengthMismatchRejected(t *testing.T) {
	payload := []byte("hello")
	buf := makeACEP2(encodeFrame(t, payload), len(payload))
	buf = buf[:len(buf)-3] // 截断
	_, err := parse(buf)
	if err == nil {
		t.Fatal("新壳长度自检失败时应当报错，而不是硬解")
	}
	if !strings.Contains(err.Error(), "长度自检") {
		t.Errorf("错误里应当点名长度自检：%v", err)
	}
}

func TestParseACEP2RawLenMismatchRejected(t *testing.T) {
	payload := []byte("hello")
	_, err := parse(makeACEP2(encodeFrame(t, payload), len(payload)+7)) // 谎报原始长
	if err == nil || !strings.Contains(err.Error(), "off24") {
		t.Fatalf("解压长度与 off24 不符时应当报错，实得 %v", err)
	}
}

// ★ 旧壳：**只认出、不解析**（用户口径 · 技能 acep v1.1.0 §1.0）。
// content 故意放一段**不是 zstd** 的 base64 —— 旧壳我们根本不该碰它。
// ★ 历史壳一（**有** compressMethod）：**可读**，解出来是内层 JSON 文本。
func TestLegacyReadableShellIsDecoded(t *testing.T) {
	inner := []byte(`{"version":1000,"tracks":[]}`)
	env, _ := json.Marshal(map[string]any{
		"compressMethod": "zstd",
		"content":        base64.StdEncoding.EncodeToString(encodeFrame(t, inner)),
		"salt":           "abc",
		"version":        1000,
		"debugInfo":      nil,
	})
	c, err := parse(env)
	if err != nil {
		t.Fatalf("历史壳一应当能读：%v", err)
	}
	if c.Info.Kind != KindLegacyReadable {
		t.Errorf("应当认成历史壳一（可读），实得 %q", c.Info.Kind)
	}
	if !bytes.Equal(c.Payload, inner) {
		t.Errorf("内层 payload 不一致：%q", c.Payload)
	}
	if !strings.Contains(DescribeHead(c.Payload), "JSON 文本") {
		t.Errorf("内层应当是 JSON 文本：%s", DescribeHead(c.Payload))
	}
	if !strings.Contains(c.Info.Note, "set-json") {
		t.Errorf("说明里应当点明改法走 set-json：%s", c.Info.Note)
	}
	joined := strings.Join(c.Info.LegacyFields, ",")
	for _, k := range []string{"compressMethod", "content", "salt", "version", "debugInfo"} {
		if !strings.Contains(joined, k) {
			t.Errorf("信封键名列表里缺 %s：%v", k, c.Info.LegacyFields)
		}
	}
}

// ★ 历史壳二（**没有** compressMethod）：加密、已弃用 ⇒ **只认出、不碰**，而且**不尝试解密**。
func TestLegacyEncryptedShellNotTouched(t *testing.T) {
	// content 故意放一段不是 zstd 的 base64 —— 我们根本不该去解它
	c, err := parse([]byte(`{"content":"lMD+oy3Jeznk","salt":"AAAA","version":2}`))
	if err != nil {
		t.Fatalf("加密壳也该被**认出**而不是报错：%v", err)
	}
	if c.Info.Kind != KindLegacyEncrypted {
		t.Errorf("应当认成历史壳二（加密），实得 %q", c.Info.Kind)
	}
	if c.Payload != nil {
		t.Error("加密壳不该被解析出 payload")
	}
	if !strings.Contains(c.Info.Note, "加密") || !strings.Contains(c.Info.Note, "不尝试解密") {
		t.Errorf("说明里应当讲清「加密、不尝试解密」：%s", c.Info.Note)
	}
	if !strings.Contains(c.Info.Note, "不是版本号") {
		t.Errorf("说明里应当提醒判据不是版本号：%s", c.Info.Note)
	}
}

// 判据是**键在不在**，不是版本号：version:1000 + 有 compressMethod ⇒ 历史壳一（可读）。
func TestShellKindDecidedByMethodKeyNotVersion(t *testing.T) {
	inner := []byte(`{"a":1}`)
	env, _ := json.Marshal(map[string]any{
		"compressMethod": "zstd",
		"content":        base64.StdEncoding.EncodeToString(encodeFrame(t, inner)),
		"version":        2, // ← 就算版本号是 2，只要**有** compressMethod 就按历史壳一读
	})
	c, err := parse(env)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Info.Kind != KindLegacyReadable {
		t.Errorf("有 compressMethod 就该按历史壳一处理（**不看版本号**），实得 %q", c.Info.Kind)
	}
	if !bytes.Equal(c.Payload, inner) {
		t.Errorf("内层 payload 不一致")
	}
}

// 外壳坏掉时不喊"损坏"，也不许误判成"加密壳"（字面扫到 compressMethod 就仍按历史壳一走）。
func TestBrokenReadableShellNotMisjudgedAsEncrypted(t *testing.T) {
	_, err := parse([]byte(`{"compressMethod": "zstd", "content": "unterminated`))
	if err == nil {
		t.Fatal("标了 compressMethod 却读不出信封时应当报错（而不是静默当加密壳）")
	}
	if !strings.Contains(err.Error(), "信封") {
		t.Errorf("错误里应当说清是信封读不出来：%v", err)
	}
}

// 真正连 compressMethod 都没有的坏 JSON：仍算历史壳二，只是键名列表为空。
func TestEncryptedShellToleratesBadJSON(t *testing.T) {
	c, err := parse([]byte(`{"content": "unterminated`))
	if err != nil {
		t.Fatalf("外壳读不全也只该如实说「认得出是加密壳」，不该报错：%v", err)
	}
	if c.Info.Kind != KindLegacyEncrypted {
		t.Errorf("实得 %q", c.Info.Kind)
	}
}

func TestParseUnknownContainer(t *testing.T) {
	if _, err := parse([]byte("NOT-AN-ACEP-FILE-AT-ALL")); err == nil {
		t.Fatal("认不出的容器应当报错")
	}
}

func TestRoundTripIsByteExact(t *testing.T) {
	payload := bytes.Repeat([]byte{0xba, 0x00, 0xff, 0x7f}, 500)
	n, _, same, err := RoundTrip(payload)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if !same {
		t.Error("重压 → 解回 应当逐字节一致")
	}
	if n == 0 {
		t.Error("重压帧大小不该是 0")
	}
}

func TestDescribeHead(t *testing.T) {
	cases := []struct {
		want string
		b    []byte
	}{
		// ★ 实测那份 ACEP2 探针工程的顶层就是 `b8 1a`（map 走扩展长度形式，26 键）
		{"CBOR map(26 键)", []byte{0xb8, 0x1a}},
		{"CBOR map(3 键)", []byte{0xa3}},                             // 小 map 内联长度
		{"CBOR map(70000 键)", []byte{0xba, 0x00, 0x01, 0x11, 0x70}}, // 4 字节长度形式
		{"长度不定或字节不够", []byte{0xbf}},                                 // 不定长
		{"CBOR array(5 项)", []byte{0x85}},
		{"JSON 文本", []byte(`{"version":22,"tracks":[]}`)}, // 首字节与 CBOR text 撞车，靠可打印性区分
		{"major 0", []byte{0x05}},
		{"空 payload", nil},
	}
	for _, c := range cases {
		if got := DescribeHead(c.b); !strings.Contains(got, c.want) {
			t.Errorf("DescribeHead(% x) = %q，期望含 %q", c.b, got, c.want)
		}
	}
}

func TestCborLen(t *testing.T) {
	// ai=24（0x18）= 长度写在紧跟的 1 字节里
	if n, ok := cborLen([]byte{26}, 24); !ok || n != 26 {
		t.Errorf("ai=24 应当读 1 字节长度：n=%d ok=%v", n, ok)
	}
	// ai=26（0x1a）= 长度写在紧跟的 4 字节里
	if n, ok := cborLen([]byte{0x00, 0x01, 0x11, 0x70}, 26); !ok || n != 70000 {
		t.Errorf("ai=26 应当读 4 字节长度：n=%d ok=%v", n, ok)
	}
	if _, ok := cborLen(nil, 24); ok {
		t.Error("字节不够时应当 ok=false，而不是瞎读")
	}
	if _, ok := cborLen(nil, 31); ok {
		t.Error("不定长（ai=31）应当 ok=false")
	}
}
