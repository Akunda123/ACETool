package cbor

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
)

// 编码只做**回填需要的那几样**：text（词/名）、float64（曲线值）、以及给测试用的容器头。
// 刻意不做"整树重编码"—— ACEP2 的写法是原位覆盖（技能 acep §4），整树重编码是另一条路。

// EncodeText 编一个 text 值（头 + UTF-8 字节）。
func EncodeText(s string) []byte {
	body := []byte(s)
	out := appendHeader(nil, 3, uint64(len(body)))
	return append(out, body...)
}

// EncodeBytes 编一个 byte string。
func EncodeBytes(b []byte) []byte {
	out := appendHeader(nil, 2, uint64(len(b)))
	return append(out, b...)
}

// EncodeFloat64 编一个 `0xfb` + 8 字节大端 —— 这正是 ACEP2 参数曲线里每个值的形状（9 字节/值）。
func EncodeFloat64(v float64) []byte {
	out := make([]byte, 9)
	out[0] = 0xfb
	binary.BigEndian.PutUint64(out[1:], math.Float64bits(v))
	return out
}

// EncodeUint 编一个无符号整数。
func EncodeUint(v uint64) []byte { return appendHeader(nil, 0, v) }

// EncodeArrayHeader 编一个定长数组头。
func EncodeArrayHeader(n uint64) []byte { return appendHeader(nil, 4, n) }

// EncodeMapHeader 编一个定长 map 头。**长度是元素个数，不是字节数**（技能 acep §2.3）。
func EncodeMapHeader(n uint64) []byte { return appendHeader(nil, 5, n) }

// EncodeBreak 是不定长容器的结束字节。
func EncodeBreak() []byte { return []byte{0xff} }

// appendHeader 按 CBOR 规则写主类型 + 参数（<24 内联，否则用 1/2/4/8 字节扩展形式）。
func appendHeader(out []byte, major byte, n uint64) []byte {
	m := major << 5
	switch {
	case n < 24:
		return append(out, m|byte(n))
	case n <= math.MaxUint8:
		return append(out, m|24, byte(n))
	case n <= math.MaxUint16:
		return append(out, m|25, byte(n>>8), byte(n))
	case n <= math.MaxUint32:
		return append(out, m|26, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	default:
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, n)
		return append(append(out, m|27), b...)
	}
}

// ---------- 整节点重编码（**变长**写要用的那一半） ----------
//
// 为什么需要它：`SetText` / `SetFloat64Array` 都是**等长**原位覆盖，够改词、够改曲线值；
// 但音高线那三条写法里有两条是**变长**的 ——
//   · 往 `notes[k]` 上加/换 `vibrato` 对象 ⇒ note 这个 map 多一个键（map 头要 +1）
//   · 往 `parameters.pitchDelta` 追加一条 `anchor` / `data` entry ⇒ 数组头要 +1
// 这两件事都必须**把整个子树重新编一遍**再换进去（技能 acep §2.3 的"长度 = 元素个数"性质保证
// 外层容器一个字节都不用动）。
//
// ⚠️ `EncodeNode` 出来的字节**不保证与 ACE 原写法逐字节相同**（半精度会升成 float64、
// 不定长容器会变成定长、整数宽度按值选）—— 值语义等价，字节形态可能不同。
// 所以要**只换必须换的那个子树**，别的字节一个都别碰。

// EncodeNode 把一个已解码的节点重新编码成 CBOR 字节。
func EncodeNode(n *Node) []byte {
	if n == nil {
		return appendHeader(nil, 7, 22) // null
	}
	switch n.Kind {
	case KUint:
		return appendHeader(nil, 0, n.Uint)
	case KInt:
		// 负整数：CBOR 存的是 -1-n
		return appendHeader(nil, 1, uint64(-1-n.Int))
	case KBytes:
		out := appendHeader(nil, 2, uint64(len(n.Bytes)))
		return append(out, n.Bytes...)
	case KText:
		return EncodeText(n.Text)
	case KArray:
		out := appendHeader(nil, 4, uint64(len(n.Items)))
		for _, it := range n.Items {
			out = append(out, EncodeNode(it)...)
		}
		return out
	case KMap:
		out := appendHeader(nil, 5, uint64(len(n.Keys)))
		for i := range n.Keys {
			out = append(out, EncodeNode(n.Keys[i])...)
			out = append(out, EncodeNode(n.Values[i])...)
		}
		return out
	case KTag:
		out := appendHeader(nil, 6, n.Tag)
		return append(out, EncodeNode(n.Inner)...)
	case KBool:
		if n.Bool {
			return []byte{0xf5}
		}
		return []byte{0xf4}
	case KNull:
		return []byte{0xf6}
	case KUndefined:
		return []byte{0xf7}
	case KFloat:
		return EncodeFloat64(n.Float)
	default:
		return []byte{0xe0 | (n.Simple & 0x1f)}
	}
}

// EncodeMap 用**文本键**编一个 map（音高线那几条写法都是这种形状）。
// ⚠️ map 的键顺序会被原样保留（CBOR 不要求有序，ACE 也不在乎，但保持稳定便于 diff）。
func EncodeMap(pairs ...[2]any) []byte {
	out := appendHeader(nil, 5, uint64(len(pairs)))
	for _, kv := range pairs {
		out = append(out, encodeAny(kv[0])...)
		out = append(out, encodeAny(kv[1])...)
	}
	return out
}

// encodeAny 是给 EncodeMap / EncodeArray 用的便利分支（只支持我们真会写的几种类型）。
func encodeAny(v any) []byte {
	switch x := v.(type) {
	case string:
		return EncodeText(x)
	case float64:
		return EncodeFloat64(x)
	case int:
		if x < 0 {
			return appendHeader(nil, 1, uint64(-1-x))
		}
		return appendHeader(nil, 0, uint64(x))
	case int64:
		if x < 0 {
			return appendHeader(nil, 1, uint64(-1-x))
		}
		return appendHeader(nil, 0, uint64(x))
	case uint64:
		return appendHeader(nil, 0, x)
	case bool:
		if x {
			return []byte{0xf5}
		}
		return []byte{0xf4}
	case []float64:
		out := appendHeader(nil, 4, uint64(len(x)))
		for _, f := range x {
			out = append(out, EncodeFloat64(f)...)
		}
		return out
	case []any:
		out := appendHeader(nil, 4, uint64(len(x)))
		for _, it := range x {
			out = append(out, encodeAny(it)...)
		}
		return out
	case *Node:
		return EncodeNode(x)
	default:
		return []byte{0xf6} // null（这里不该出现别的类型）
	}
}

// ---------- 原位覆盖 ----------

// Edit 是一次**原位覆盖**：把 payload 的 [Start,End) 换成 Bytes。
type Edit struct {
	Start, End int
	Bytes      []byte
	What       string // 给人看的说明（进日志/回报）
}

// Apply 套用一批编辑。**从后往前**改，这样前面的偏移仍然有效。
// 区间不许重叠（重叠一定是调用方算错了，直接拒，别猜）。
func Apply(payload []byte, edits []Edit) ([]byte, error) {
	if len(edits) == 0 {
		return append([]byte(nil), payload...), nil
	}
	sorted := append([]Edit(nil), edits...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start > sorted[j].Start })

	out := append([]byte(nil), payload...)
	prevStart := len(payload) + 1
	for _, e := range sorted {
		if e.Start < 0 || e.End > len(out) || e.Start > e.End {
			return nil, fmt.Errorf("编辑区间非法：[%d,%d)（payload %d 字节）", e.Start, e.End, len(out))
		}
		if e.End > prevStart {
			return nil, fmt.Errorf("编辑区间重叠：[%d,%d) 与下一段（起点 %d）", e.Start, e.End, prevStart)
		}
		tail := append([]byte(nil), out[e.End:]...)
		out = append(out[:e.Start], e.Bytes...)
		out = append(out, tail...)
		prevStart = e.Start
	}
	return out, nil
}

// SetText 把 path 上的 text 换成新值。
//
// ✅ 长度可变 —— 技能 acep §2.3 的关键性质：CBOR 的 map/array 长度是**元素个数**，
// 所以换长换短只改这个 text 自己的长度前缀，外层容器一个字节都不用动。
func SetText(payload []byte, path, newText string) ([]byte, []Edit, error) {
	root, err := Decode(payload)
	if err != nil {
		return nil, nil, err
	}
	n, err := Get(root, path)
	if err != nil {
		return nil, nil, err
	}
	if n.Kind != KText {
		return nil, nil, fmt.Errorf("%s 是 %s，不是 text —— SetText 只改文本", path, n.Kind)
	}
	old := n.Text
	e := Edit{
		Start: n.Start, End: n.End, Bytes: EncodeText(newText),
		What: fmt.Sprintf("%s: %q → %q", path, trunc(old, 40), trunc(newText, 40)),
	}
	out, err := Apply(payload, []Edit{e})
	if err != nil {
		return nil, nil, err
	}
	return out, []Edit{e}, nil
}

// SetFloat64Array 覆盖一个 float64 数组。**元素个数必须不变**（原位覆盖的硬前提）。
func SetFloat64Array(payload []byte, path string, vals []float64) ([]byte, []Edit, error) {
	root, err := Decode(payload)
	if err != nil {
		return nil, nil, err
	}
	n, err := Get(root, path)
	if err != nil {
		return nil, nil, err
	}
	if n.Kind != KArray {
		return nil, nil, fmt.Errorf("%s 是 %s，不是数组", path, n.Kind)
	}
	if len(vals) != len(n.Items) {
		return nil, nil, fmt.Errorf("%s 有 %d 个元素，给了 %d 个值 —— 原位覆盖要求**个数不变**（要增删得整段重编码）",
			path, len(n.Items), len(vals))
	}
	edits := make([]Edit, 0, len(vals))
	for i, it := range n.Items {
		if it.Kind != KFloat {
			return nil, nil, fmt.Errorf("%s[%d] 是 %s，不是浮点 —— 这个数组不是 float64 数组", path, i, it.Kind)
		}
		if it.IsF16 || it.Len() != 9 {
			return nil, nil, fmt.Errorf("%s[%d] 不是 9 字节的 float64（%d 字节，f16=%v）—— 原位覆盖不覆盖这种编码",
				path, i, it.Len(), it.IsF16)
		}
		edits = append(edits, Edit{
			Start: it.Start, End: it.End, Bytes: EncodeFloat64(vals[i]),
			What: fmt.Sprintf("%s[%d]: %g → %g", path, i, it.Float, vals[i]),
		})
	}
	out, err := Apply(payload, edits)
	if err != nil {
		return nil, nil, err
	}
	return out, edits, nil
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
