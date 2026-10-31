// Package acep 读 ACE Studio 的 `.acep` 工程文件。
//
// **三种壳**（技能 `acep` **v1.2.3 §1.0**）：
//
//	① 当前格式 `ACEP2`：1280 B 头 + 一个 zstd 帧 → payload 是 **CBOR**（顶层 map）。
//	   头字段：off8 u32 头长 · off12 u32 0 · off16 u64 压缩长 · off24 u64 原始长
//	   （off8+off16 == 文件大小，用来判"有没有被截断"）。**完整支持**。
//	   注：顶层键数按**文件现读**（探针工程实测 26 键；技能记的三份工程**并集**是 30 键，两者不矛盾）。
//	② 历史壳一（**可读、可编辑**）：首字节 `{"compressMethod":"zstd"` —— 包装 JSON，
//	   `content` = base64 的 zstd 帧，解出来是内层 **JSON 文本**（不是 CBOR）。
//	   ⚠️ 改它的路子跟 ACEP2 **完全不同**：走 `set-json`（整树改 → 重 stringify → 重 zstd →
//	   重 base64 → 只换 `content`），**不是** ACEP2 那套原位覆盖。本包只做到"读得出来"。
//	③ 历史壳二（**加密、已弃用**）：首字节 `{"content"` 且**没有** `compressMethod` ——
//	   `content` 解 base64 后 zstd/gzip/deflate/brotli 全解不开（熵 7.978 bit/B）。**不尝试解密**。
//
// ⚠️ **②③的判据是"有没有 `compressMethod`"，不是版本号** —— 技能明确说过：`version:2`
// 只是③唯一样本的特征，拿版本号下结论会错。
package acep

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

type Kind string

const (
	// KindACEP2 是**当前格式**：1280 B 头 + 一个 zstd 帧 + CBOR payload（顶层 map）。
	KindACEP2 Kind = "ACEP2"
	// KindLegacyReadable 是**历史壳一**（首字节 '{'，**有** `compressMethod`）：
	// 包装 JSON（content = base64 的 zstd 帧）⇒ **可读**，内层是 **JSON 文本**（不是 CBOR）。
	// 要改它走 `set-json`（整树改 → 重 stringify → 重 zstd → 重 base64 → 重建包装），**不是** §4 的原位覆盖。
	KindLegacyReadable Kind = "旧壳·可读(历史一)"
	// KindLegacyEncrypted 是**历史壳二**（首字节 '{'，**没有** `compressMethod`）：加密、已弃用 ⇒ **不碰**。
	// ⚠️ 判据是"有没有 compressMethod"，**不是版本号**（技能明确：`version:2` 只是它唯一样本的特征）。
	KindLegacyEncrypted Kind = "旧壳·加密(历史二)"
	KindUnknown         Kind = "未知"
)

// Info 是一次读取的元信息（给界面显示 / 给自检用）。
type Info struct {
	Path     string `json:"path"`
	FileSize int64  `json:"fileSize"`
	Kind     Kind   `json:"kind"`
	Note     string `json:"note,omitempty"`
	Method   string `json:"compressMethod,omitempty"`

	// ACEP2 专有（其它容器为零值）
	HeadLen       uint64 `json:"headLen,omitempty"`
	CompressedLen uint64 `json:"compressedLen,omitempty"`
	RawLen        uint64 `json:"rawLen,omitempty"`
	LengthOK      bool   `json:"lengthOk,omitempty"`

	// 两种壳都有
	FrameLen    int    `json:"frameLen"`
	PayloadLen  int    `json:"payloadLen"`
	PayloadHead string `json:"payloadHead"`
	Millis      int64  `json:"millis"`

	// 旧壳专有：只列出信封里出现了哪些键（**不读 content**）
	LegacyFields []string `json:"legacyFields,omitempty"`
}

// Container 是解出来的东西。
type Container struct {
	Info    Info
	Payload []byte

	// 重建用（不导出，也不进 JSON）：
	//   header   —— ACEP2 的原头（含那条 1248 B 高熵区），重建时**原样复制**
	//   envelope —— 历史壳一的原包装（只换 content）
	//   raw      —— 原文件字节，供自检做逐字节比对
	header   []byte
	envelope map[string]json.RawMessage
	raw      []byte
}

// Open 读一个 .acep：嗅探容器 → 取 zstd 帧 → 解出 payload。
func Open(path string) (*Container, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	t0 := time.Now()
	c, err := parse(raw)
	if err != nil {
		return nil, err
	}
	c.Info.Path = path
	c.Info.FileSize = int64(len(raw))
	c.Info.PayloadLen = len(c.Payload)
	if c.Payload != nil { // 旧壳不解 payload ⇒ 别报一个假的"空 payload"
		c.Info.PayloadHead = DescribeHead(c.Payload)
	}
	c.Info.Millis = time.Since(t0).Milliseconds()
	return c, nil
}

func parse(buf []byte) (*Container, error) {
	// A) ACEP2
	if len(buf) >= 32 && string(buf[0:5]) == "ACEP2" {
		headLen := binary.LittleEndian.Uint32(buf[8:12])
		compLen := binary.LittleEndian.Uint64(buf[16:24])
		rawLen := binary.LittleEndian.Uint64(buf[24:32])
		info := Info{
			Kind: KindACEP2, HeadLen: uint64(headLen),
			CompressedLen: compLen, RawLen: rawLen,
			Note: "二进制头 + 偏移 headLen 处的裸 zstd 帧",
		}
		if int(headLen)+int(compLen) != len(buf) {
			return nil, fmt.Errorf("ACEP2 长度自检失败：headLen(%d)+compLen(%d)=%d ≠ 文件大小(%d)",
				headLen, compLen, int(headLen)+int(compLen), len(buf))
		}
		if int(headLen) > len(buf) {
			return nil, fmt.Errorf("headLen(%d) 超出文件大小(%d)", headLen, len(buf))
		}
		frame := buf[headLen : int(headLen)+int(compLen)]
		payload, err := inflate(frame)
		if err != nil {
			return nil, fmt.Errorf("ACEP2 的 zstd 帧解不开：%w", err)
		}
		if uint64(len(payload)) != rawLen {
			return nil, fmt.Errorf("解压得到 %d B ≠ off24 声明的 %d B", len(payload), rawLen)
		}
		info.LengthOK = true
		info.FrameLen = len(frame)
		// 留住原头和原文件字节：重建时头要**原样复制**，自检要逐字节比对。
		return &Container{
			Info: info, Payload: payload,
			header: append([]byte(nil), buf[:int(headLen)]...),
			raw:    buf,
		}, nil
	}

	// B) 两种**历史壳**（技能 acep v1.2.3 §1.0）—— 首字节 '{' 的 JSON 信封。
	//
	// ⚠️ **判据是"有没有 `compressMethod`"，不是版本号**。技能明确说过：`version:2` 只是历史二
	//    唯一样本的特征，别拿版本号下结论 —— 所以这里只看那个键在不在。
	if len(buf) > 0 && buf[0] == '{' {
		// 外壳读不全也不喊"损坏"：这不是我们的格式。如实报"认得出是哪一种历史壳"。
		var shell map[string]json.RawMessage
		_ = json.Unmarshal(buf, &shell)
		keys := make([]string, 0, len(shell))
		for k := range shell {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		// ⚠️ 判据是"有没有 compressMethod"，**不是版本号**；外壳解析失败时退回**字面扫一遍**
		//（否则一个坏掉的壳会被误判成"加密壳"）。
		_, hasMethodKey := shell["compressMethod"]
		hasMethod := hasMethodKey ||
			bytes.Contains(buf[:minInt(len(buf), 512)], []byte(`"compressMethod"`))

		if !hasMethod {
			// 历史二：加密、已弃用 ⇒ **不碰**（技能：不尝试解密）
			return &Container{Info: Info{
				Kind: KindLegacyEncrypted, LegacyFields: keys,
				Note: "历史壳二：**加密、已弃用** —— 不尝试解密，也不做任何写入。" +
					"要这份工程只能用当年那版 ACE 打开后另存。" +
					"（判据是**没有 `compressMethod`**，不是版本号。）",
			}, raw: buf}, nil
		}

		// 历史一：**可读** —— content 是 base64 的 zstd 帧，解出来是内层 **JSON 文本**
		var env struct {
			CompressMethod string `json:"compressMethod"`
			Content        string `json:"content"`
		}
		if err := json.Unmarshal(buf, &env); err != nil {
			return nil, fmt.Errorf("有 compressMethod（应当是历史壳一）却读不出信封：%w", err)
		}
		if env.Content == "" {
			return nil, fmt.Errorf("历史壳一里没有 content 字段")
		}
		if env.CompressMethod != "zstd" {
			return nil, fmt.Errorf("历史壳一的 compressMethod 是 %q —— 本包只认 \"zstd\"", env.CompressMethod)
		}
		frame, err := base64.StdEncoding.DecodeString(env.Content)
		if err != nil {
			if f2, err2 := base64.RawStdEncoding.DecodeString(env.Content); err2 == nil {
				frame, err = f2, nil
			}
		}
		if err != nil {
			return nil, fmt.Errorf("历史壳一的 content 不是合法 base64：%w", err)
		}
		if !hasZstdMagic(frame) {
			return nil, fmt.Errorf("历史壳一标了 compressMethod=zstd，但 content 解出来不是 zstd 帧（开头 %s）",
				hexHead(frame, 6))
		}
		payload, err := inflate(frame)
		if err != nil {
			return nil, fmt.Errorf("历史壳一的 zstd 帧解不开：%w", err)
		}
		return &Container{Info: Info{
			Kind: KindLegacyReadable, Method: env.CompressMethod,
			FrameLen: len(frame), LegacyFields: keys,
			Note: "历史壳一：**可读、可编辑**（内层是 JSON 文本，不是 CBOR）。" +
				"改它要走 set-json（整树解析 → 改路径 → 重 stringify → 重 zstd → 重 base64 → 只换 content），" +
				"**不是** ACEP2 那套原位覆盖。",
		}, Payload: payload, envelope: shell, raw: buf}, nil
	}

	return nil, fmt.Errorf("认不出的 .acep 容器：开头 %s（既不是 'ACEP2' 也不是历史壳的 '{'）", hexHead(buf, 8))
}

func inflate(frame []byte) ([]byte, error) {
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	return dec.DecodeAll(frame, nil)
}

// RoundTrip 把 payload 重新压一遍、再解回来，逐字节比对。
//
// 这是**写文件路线的前置探针**（技能 `acep` §4 第 ⑥ 步的自检）：
// 只有「重压 → 解回 → 逐字节一致」成立，才谈得上安全地重建容器。
//
// 顺带记一笔体积账：**只用解码器**时 Go 链接器会把 zstd 的编码表整段丢掉
// （实测 acetool.exe 只涨约 0.24 MB）；一旦用上编码器，代价是另一个量级 ——
// 所以这个函数同时也是"写路线值不值"的实测依据。
func RoundTrip(payload []byte) (frameLen int, millis int64, same bool, err error) {
	t0 := time.Now()
	frame, err := Encode(payload)
	if err != nil {
		return 0, 0, false, err
	}
	back, err := inflate(frame)
	if err != nil {
		return len(frame), time.Since(t0).Milliseconds(), false, err
	}
	return len(frame), time.Since(t0).Milliseconds(), bytes.Equal(payload, back), nil
}

// Encode 把 payload 压成 zstd 帧（写文件时用）。
func Encode(payload []byte) ([]byte, error) {
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		return nil, err
	}
	defer enc.Close()
	return enc.EncodeAll(payload, nil), nil
}

func hasZstdMagic(b []byte) bool {
	return len(b) >= 4 && b[0] == 0x28 && b[1] == 0xb5 && b[2] == 0x2f && b[3] == 0xfd
}

// DescribeHead 判断 payload 是什么编码。
//
// ⚠️ **别看一个字节就下结论**（我第一版就这么错过）：
//   - CBOR 的 map/array 长度 >23 时走**扩展形式**：`0xb8`+1 字节 / `0xb9`+2 / `0xba`+4 / `0xbb`+8。
//     实测那份 ACEP2 探针工程的顶层是 `b8 1a` ⇒ **map(26 键)**，而不是想当然的"0xba"。
//   - JSON 的 `{`（0x7b）与 CBOR 的「text(27 字节)」首字节**一模一样** ⇒ 得看后面是不是可打印文本。
func DescribeHead(b []byte) string {
	if len(b) == 0 {
		return "空 payload"
	}
	// 1) 像是可打印的 JSON 文本？（只扫头 96 字节就够区分）
	if b[0] == '{' && printableRatio(b[:minInt(len(b), 96)]) > 0.95 {
		return "JSON 文本（首字节 0x7b '{'）"
	}
	// 2) 按 CBOR 头解
	major := b[0] >> 5
	ai := b[0] & 0x1f
	n, ok := cborLen(b[1:], ai)
	switch major {
	case 5: // map
		if !ok {
			return fmt.Sprintf("CBOR map（首字节 0x%02x，长度不定或字节不够）", b[0])
		}
		return fmt.Sprintf("CBOR map(%d 键)（首字节 0x%02x）", n, b[0])
	case 4: // array
		if !ok {
			return fmt.Sprintf("CBOR array（首字节 0x%02x，长度不定或字节不够）", b[0])
		}
		return fmt.Sprintf("CBOR array(%d 项)（首字节 0x%02x）", n, b[0])
	case 3: // text
		if !ok {
			return fmt.Sprintf("CBOR text（首字节 0x%02x，长度读不出）", b[0])
		}
		return fmt.Sprintf("CBOR text(%d 字节)（首字节 0x%02x）", n, b[0])
	case 2: // byte string
		if !ok {
			return fmt.Sprintf("CBOR byte string（首字节 0x%02x，长度读不出）", b[0])
		}
		return fmt.Sprintf("CBOR byte string(%d 字节)（首字节 0x%02x）", n, b[0])
	}
	return fmt.Sprintf("首字节 0x%02x（CBOR major %d，既不是 map 也不是 array）", b[0], major)
}

// cborLen 解 CBOR 头里的长度（additional information）。ok=false 表示不定长或字节不够。
func cborLen(rest []byte, ai byte) (int, bool) {
	switch {
	case ai < 24:
		return int(ai), true
	case ai == 24 && len(rest) >= 1:
		return int(rest[0]), true
	case ai == 25 && len(rest) >= 2:
		return int(binary.BigEndian.Uint16(rest)), true
	case ai == 26 && len(rest) >= 4:
		return int(binary.BigEndian.Uint32(rest)), true
	case ai == 27 && len(rest) >= 8:
		return int(binary.BigEndian.Uint64(rest)), true
	default:
		return 0, false
	}
}

// printableRatio 头一段里可打印字符（含 \n\r\t）的占比。
func printableRatio(b []byte) float64 {
	if len(b) == 0 {
		return 1
	}
	ok := 0
	for _, c := range b {
		if (c >= 32 && c < 127) || c == '\n' || c == '\r' || c == '\t' {
			ok++
		}
	}
	return float64(ok) / float64(len(b))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func hexHead(b []byte, n int) string {
	if len(b) < n {
		n = len(b)
	}
	parts := make([]string, 0, n)
	for _, c := range b[:n] {
		parts = append(parts, fmt.Sprintf("%02x", c))
	}
	return strings.Join(parts, " ")
}
