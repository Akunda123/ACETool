package cbor

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

// build 手工拼一段 CBOR，用来造各种边角。
func build(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// ★ 坑 1：half float（0xf9）必须消费 2 字节 —— 漏了后面整段错位。
func TestHalfFloatConsumesTwoBytes(t *testing.T) {
	// map(2) { "a": half(1.5) , "b": uint(7) }
	raw := build(
		EncodeMapHeader(2),
		EncodeText("a"), []byte{0xf9, 0x3e, 0x00}, // 1.5
		EncodeText("b"), EncodeUint(7),
	)
	root, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if root.Kind != KMap || len(root.Keys) != 2 {
		t.Fatalf("顶层应当是 2 键 map：%+v", root)
	}
	a := root.Get("a")
	if a == nil || a.Kind != KFloat || !a.IsF16 || a.Float != 1.5 {
		t.Errorf("a 应当是 float16 1.5，实得 %+v", a)
	}
	if b := root.Get("b"); b == nil || b.Uint != 7 {
		t.Errorf("half float 之后应当还能正确读到 b=7（错位就说明少消费了），实得 %+v", b)
	}
}

// ★ 坑 2：float64（0xfb）只能消费 8 字节 —— 多消费一次就整段错位。
// 用"每个值的偏移必须严格 +9"来卡死。
func TestFloat64ArrayOffsetsAreExactlyNineBytesApart(t *testing.T) {
	vals := []float64{0, 0.1, -0.1, 1e-9, math.Pi}
	raw := EncodeArrayHeader(uint64(len(vals)))
	for _, v := range vals {
		raw = append(raw, EncodeFloat64(v)...)
	}
	root, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(root.Items) != len(vals) {
		t.Fatalf("应当 %d 项，实得 %d（错位就会少/多）", len(vals), len(root.Items))
	}
	for i, it := range root.Items {
		if it.Len() != 9 {
			t.Errorf("第 %d 个值占 %d 字节，应当 9", i, it.Len())
		}
		if i > 0 && it.Start-root.Items[i-1].Start != 9 {
			t.Errorf("第 %d 个值起点与上一个差 %d，应当 9", i, it.Start-root.Items[i-1].Start)
		}
		if it.Float != vals[i] {
			t.Errorf("第 %d 个值 = %v，期望 %v", i, it.Float, vals[i])
		}
	}
	// 末尾偏移必须正好等于长度（消费完）
	if root.Items[len(vals)-1].End != len(raw) {
		t.Errorf("最后一个值的 End=%d，应当等于总长 %d", root.Items[len(vals)-1].End, len(raw))
	}
}

// 整数键（ACEP2 的 phonemeTimings 就是 {0:符号, 1:秒}）—— 别假定键都是文本。
func TestIntegerMapKeys(t *testing.T) {
	raw := build(
		EncodeMapHeader(2),
		EncodeUint(0), EncodeText("l"),
		EncodeUint(1), []byte{0xfb, 0x3f, 0xe3, 0x33, 0x33, 0x33, 0x33, 0x33, 0x33}, // 0.6
	)
	root, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := root.Get("0"); got == nil || got.Text != "l" {
		t.Errorf("整数键 0 → %+v", got)
	}
	n := root.Get("1")
	if n == nil || n.Kind != KFloat || math.Abs(n.Float-0.6) > 1e-12 {
		t.Errorf("整数键 1 → %+v", n)
	}
	names := KeyNames(root, 8)
	if strings.Join(names, ",") != "0,1" {
		t.Errorf("键名列表 = %v", names)
	}
}

func TestNegativeIntBoolNullUndefined(t *testing.T) {
	raw := build(
		EncodeMapHeader(4),
		EncodeText("n"), []byte{0x20}, // -1
		EncodeText("t"), []byte{0xf5}, // true
		EncodeText("z"), []byte{0xf6}, // null
		EncodeText("u"), []byte{0xf7}, // undefined
	)
	root, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if n := root.Get("n"); n.Kind != KInt || n.Int != -1 {
		t.Errorf("-1 → %+v", n)
	}
	if b := root.Get("t"); b.Kind != KBool || !b.Bool {
		t.Errorf("true → %+v", b)
	}
	if z := root.Get("z"); z.Kind != KNull {
		t.Errorf("null → %+v", z)
	}
	if u := root.Get("u"); u.Kind != KUndefined {
		t.Errorf("undefined → %+v", u)
	}
}

// 不定长容器（真实 .acep 未必用，但解码器不能一遇就崩）。
func TestIndefiniteContainers(t *testing.T) {
	raw := build(
		[]byte{0xbf},                                                              // map, 不定长
		EncodeText("a"), []byte{0x9f}, EncodeUint(1), EncodeUint(2), []byte{0xff}, // 值是：不定长 array
		EncodeText("b"), []byte{0x7f}, EncodeText("he"), EncodeText("llo"), []byte{0xff}, // 值是不定长 text
		[]byte{0xff}, // 结束 map
	)
	root, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	arr := root.Get("a")
	if arr == nil || arr.Kind != KArray || len(arr.Items) != 2 {
		t.Fatalf("不定长数组 → %+v", arr)
	}
	if got := root.Get("b"); got == nil || got.Text != "hello" {
		t.Fatalf("不定长文本 → %+v", got)
	}
	if len(root.Keys) != 2 {
		t.Errorf("不定长 map 应当有 2 键，实得 %d", len(root.Keys))
	}
}

func TestIndefiniteTextConcatenates(t *testing.T) {
	raw := build(EncodeMapHeader(1), EncodeText("s"),
		[]byte{0x7f}, EncodeText("he"), EncodeText("llo"), []byte{0xff})
	root, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := root.Get("s"); got == nil || got.Text != "hello" {
		t.Errorf("不定长文本应当拼成 hello，实得 %+v", got)
	}
}

func TestTagIsTransparent(t *testing.T) {
	raw := build(EncodeMapHeader(1), EncodeText("x"), []byte{0xc1}, EncodeUint(42)) // tag(1) uint 42
	root, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	x := root.Get("x")
	if x.Kind != KTag || x.Tag != 1 || x.Inner == nil || x.Inner.Uint != 42 {
		t.Errorf("tag → %+v", x)
	}
}

// 必须恰好消费完 —— 多一个字节都要报错（技能 §4 第 ⑥ 步的自检之一）。
func TestDecodeRequiresExactConsumption(t *testing.T) {
	raw := append(EncodeText("hi"), 0x00)
	if _, err := Decode(raw); err == nil {
		t.Fatal("多出字节时应当报错")
	} else if !strings.Contains(err.Error(), "只消费了") {
		t.Errorf("错误应当点明消费不完整：%v", err)
	}
}

func TestDecodePrefixAllowsTrailing(t *testing.T) {
	raw := append(EncodeText("hi"), 0x00)
	n, used, err := DecodePrefix(raw)
	if err != nil {
		t.Fatalf("DecodePrefix: %v", err)
	}
	if n.Text != "hi" || used != 3 {
		t.Errorf("n=%+v used=%d", n, used)
	}
}

func TestGetPath(t *testing.T) {
	// {"tracks":[{"name":"Vocal 1"}], "n":3}
	raw := build(
		EncodeMapHeader(2),
		EncodeText("tracks"),
		EncodeArrayHeader(1),
		EncodeMapHeader(1), EncodeText("name"), EncodeText("Vocal 1"),
		EncodeText("n"), EncodeUint(3),
	)
	root, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	n, err := Get(root, "tracks[0].name")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if n.Text != "Vocal 1" {
		t.Errorf("取值 = %q", n.Text)
	}
	if n, err := Get(root, "$.n"); err != nil || n.Uint != 3 {
		t.Errorf("带 $ 前缀的路径应当也认：%+v %v", n, err)
	}
	if _, err := Get(root, "tracks[9].name"); err == nil {
		t.Error("越界下标应当报错")
	}
	if _, err := Get(root, "nope"); err == nil {
		t.Error("不存在的键应当报错")
	} else if !strings.Contains(err.Error(), "有的键") {
		t.Errorf("错误里应当列出可用的键：%v", err)
	}
}

// ★ 回填：文本换长换短只改自己的长度前缀，外层容器不用动（技能 §2.3 的关键性质）。
func TestSetTextChangesLengthInPlace(t *testing.T) {
	raw := build(
		EncodeMapHeader(2),
		EncodeText("syllable"), EncodeText("l an"),
		EncodeText("tail"), EncodeText("keep-me"),
	)
	out, edits, err := SetText(raw, "syllable", "l a l a") // "l an"(4 字符) → "l a l a"(7 字符)
	if err != nil {
		t.Fatalf("SetText: %v", err)
	}
	// 总长的变化**恰好等于新旧文本编码之差** ⇒ 外层容器一个字节都没动（技能 §2.3 的关键性质）
	wantDelta := len(EncodeText("l a l a")) - len(EncodeText("l an"))
	if len(out) != len(raw)+wantDelta {
		t.Errorf("总长变化应当是 %+d（原 %d → 新 %d），实得 %+d",
			wantDelta, len(raw), len(out), len(out)-len(raw))
	}
	if len(edits) != 1 || !strings.Contains(edits[0].What, "l an") {
		t.Errorf("编辑说明 = %+v", edits)
	}
	root, err := Decode(out)
	if err != nil {
		t.Fatalf("改完应当还能整段解出来：%v", err)
	}
	if got := root.Get("syllable"); got.Text != "l a l a" {
		t.Errorf("回读 syllable = %q", got.Text)
	}
	if got := root.Get("tail"); got.Text != "keep-me" {
		t.Errorf("后面的项不该被动到：%q", got.Text)
	}
}

func TestSetFloat64ArrayOK(t *testing.T) {
	raw := EncodeArrayHeader(3)
	for _, v := range []float64{0, 1, 2} {
		raw = append(raw, EncodeFloat64(v)...)
	}
	out, edits, err := SetFloat64Array(raw, "", []float64{0.5, 1.5, 2.5})
	if err != nil {
		t.Fatalf("SetFloat64Array: %v", err)
	}
	if len(edits) != 3 {
		t.Errorf("应当 3 处编辑，实得 %d", len(edits))
	}
	if len(out) != len(raw) {
		t.Errorf("原位覆盖长度必须不变：%d → %d", len(raw), len(out))
	}
	root, err := Decode(out)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for i, want := range []float64{0.5, 1.5, 2.5} {
		if root.Items[i].Float != want {
			t.Errorf("[%d] = %v，期望 %v", i, root.Items[i].Float, want)
		}
	}
}

func TestSetFloat64ArrayCountMismatchRejected(t *testing.T) {
	raw := append(EncodeArrayHeader(2), append(EncodeFloat64(1), EncodeFloat64(2)...)...)
	_, _, err := SetFloat64Array(raw, "", []float64{1})
	if err == nil {
		t.Fatal("元素个数不一致时应当拒绝")
	}
	if !strings.Contains(err.Error(), "个数不变") {
		t.Errorf("错误应当讲清前提：%v", err)
	}
}

// 非 float64 编码（半精度 / 定长不对）不许被原位覆盖 —— 会写出错的值。
func TestSetFloat64ArrayRejectsNonFloat64(t *testing.T) {
	raw := append(EncodeArrayHeader(1), []byte{0xf9, 0x3e, 0x00}...) // 一个 half float
	_, _, err := SetFloat64Array(raw, "", []float64{1})
	if err == nil {
		t.Fatal("半精度不该被当 float64 覆盖")
	}
}

func TestApplyRejectsOverlap(t *testing.T) {
	payload := []byte("0123456789")
	_, err := Apply(payload, []Edit{
		{Start: 0, End: 5, Bytes: []byte("x")},
		{Start: 3, End: 7, Bytes: []byte("y")},
	})
	if err == nil || !strings.Contains(err.Error(), "重叠") {
		t.Fatalf("重叠区间应当拒绝，实得 %v", err)
	}
}

func TestApplyEmptyIsCopy(t *testing.T) {
	payload := []byte{1, 2, 3}
	out, err := Apply(payload, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	out[0] = 9
	if payload[0] != 1 {
		t.Error("没有编辑时也应当返回副本，不能改到入参")
	}
}

func TestFloat16KnownValues(t *testing.T) {
	cases := map[uint16]float32{
		0x0000: 0, 0x3c00: 1, 0xc000: -2, 0x7c00: float32(math.Inf(1)), 0x3555: 0.333251953125,
	}
	for h, want := range cases {
		if got := float16ToFloat32(h); got != want {
			t.Errorf("half 0x%04x = %v，期望 %v", h, got, want)
		}
	}
}

func TestEncodeTextMatchesKnown(t *testing.T) {
	if got := EncodeText("a"); !bytes.Equal(got, []byte{0x61, 'a'}) {
		t.Errorf("text \"a\" = % x", got)
	}
	long := strings.Repeat("x", 30)
	if got := EncodeText(long); got[0] != 0x78 || got[1] != 30 {
		t.Errorf("30 字节文本的头应当走扩展形式：% x", got[:2])
	}
	if got := EncodeFloat64(1); !bytes.Equal(got, []byte{0xfb, 0x3f, 0xf0, 0, 0, 0, 0, 0, 0}) {
		t.Errorf("float64(1) = % x", got)
	}
	_ = binary.BigEndian
}
