// Package cbor 是一个**带字节偏移**的最小 CBOR 解码器，专为 `.acep` 的 payload 服务。
//
// 为什么不引通用库：ACEP2 的写法是**原位覆盖**（技能 acep §2.3 / §4）——
// 文本项只改自己的长度前缀、float64 数组只要元素个数不变就逐值覆盖。
// 那需要**每个节点的字节范围**；通用库一般只给值不给偏移。
//
// ⚠️ 技能 acep §2.3 记的两个坑（他们都栽过，这里用测试卡死）：
//
//  1. **必须处理 half float（`0xf9`）** —— 漏了会少消费 2 字节，后面整段错位。
//  2. **读浮点时别二次消费负载** —— 他们的 JS 版 `head()` 已经把负载读进 `val`，
//     `case 7` 又 `read()` 一次 ⇒ 每个 double 多吃 8 字节，解到 `minBpm` 就崩。
//     Go 这边用**单个游标一次推进**，并有专门的用例（一个只有 doubles 的数组，逐值校验偏移）。
//
// 另一个关键性质（技能 §2.3）：**CBOR 的 map/array 长度是"元素个数"、不是字节数**
// ⇒ 把一个 text 换长换短，只改它自己的长度前缀，外层容器一个字节都不用动。
package cbor

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

type Kind uint8

const (
	KUint Kind = iota
	KInt
	KBytes
	KText
	KArray
	KMap
	KTag
	KBool
	KNull
	KUndefined
	KFloat
	KSimple
)

func (k Kind) String() string {
	switch k {
	case KUint:
		return "uint"
	case KInt:
		return "negint"
	case KBytes:
		return "bytes"
	case KText:
		return "text"
	case KArray:
		return "array"
	case KMap:
		return "map"
	case KTag:
		return "tag"
	case KBool:
		return "bool"
	case KNull:
		return "null"
	case KUndefined:
		return "undefined"
	case KFloat:
		return "float"
	default:
		return "simple"
	}
}

// Node 是一个 CBOR 值 + 它在 payload 里的**字节范围**（半开区间 [Start,End)）。
type Node struct {
	Kind  Kind
	Start int
	End   int

	Uint   uint64
	Int    int64
	Float  float64
	IsF16  bool // 半精度（0xf9）—— 写回时要注意别把它当 f64 覆盖
	Bool   bool
	Text   string
	Bytes  []byte
	Simple byte
	Tag    uint64

	Keys   []*Node // map：键（保持出现顺序）
	Values []*Node // map：值
	Items  []*Node // array
	Inner  *Node   // tag：被标记的值
}

// Len 是节点占用的字节数。
func (n *Node) Len() int { return n.End - n.Start }

// KeyString 把 map 的键渲染成路径里能用的字符串。
// ⚠️ ACEP2 里确实有**整数键**（`phonemeTimings` 的键就是 0/1），别假定键都是文本。
func (n *Node) KeyString() string {
	switch n.Kind {
	case KText:
		return n.Text
	case KUint:
		return strconv.FormatUint(n.Uint, 10)
	case KInt:
		return strconv.FormatInt(n.Int, 10)
	default:
		return n.Kind.String()
	}
}

// Get 按键取 map 的值。
func (n *Node) Get(key string) *Node {
	if n.Kind != KMap {
		return nil
	}
	for i, k := range n.Keys {
		if k.KeyString() == key {
			return n.Values[i]
		}
	}
	return nil
}

// Decode 解一个 payload，要求**恰好消费完所有字节**（技能 §4 第 ⑥ 步的自检之一）。
func Decode(b []byte) (*Node, error) {
	d := &decoder{buf: b}
	n, err := d.value()
	if err != nil {
		return nil, err
	}
	if d.pos != len(b) {
		return nil, fmt.Errorf("CBOR 只消费了 %d/%d 字节（多出 %d）—— 解码器与数据对不上",
			d.pos, len(b), len(b)-d.pos)
	}
	return n, nil
}

// DecodePrefix 解一个值，不要求消费完（用于 payload 是"一个值后面跟别的"的场景）。
func DecodePrefix(b []byte) (*Node, int, error) {
	d := &decoder{buf: b}
	n, err := d.value()
	if err != nil {
		return nil, 0, err
	}
	return n, d.pos, nil
}

type decoder struct {
	buf []byte
	pos int
}

func (d *decoder) need(n int) error {
	if d.pos+n > len(d.buf) {
		return io.ErrUnexpectedEOF
	}
	return nil
}

// arg 读 additional information 的参数（ai 24/25/26/27 各跟 1/2/4/8 字节）。
func (d *decoder) arg(ai byte) (uint64, error) {
	switch {
	case ai < 24:
		return uint64(ai), nil
	case ai == 24:
		if err := d.need(1); err != nil {
			return 0, err
		}
		v := uint64(d.buf[d.pos])
		d.pos++
		return v, nil
	case ai == 25:
		if err := d.need(2); err != nil {
			return 0, err
		}
		v := uint64(binary.BigEndian.Uint16(d.buf[d.pos:]))
		d.pos += 2
		return v, nil
	case ai == 26:
		if err := d.need(4); err != nil {
			return 0, err
		}
		v := uint64(binary.BigEndian.Uint32(d.buf[d.pos:]))
		d.pos += 4
		return v, nil
	case ai == 27:
		if err := d.need(8); err != nil {
			return 0, err
		}
		v := binary.BigEndian.Uint64(d.buf[d.pos:])
		d.pos += 8
		return v, nil
	}
	return 0, fmt.Errorf("非法的 additional information %d（28..30 保留）", ai)
}

// argOrIndef 用于字符串/数组/map：ai==31 表示不定长。
func (d *decoder) argOrIndef(ai byte) (n uint64, indefinite bool, err error) {
	if ai == 31 {
		return 0, true, nil
	}
	n, err = d.arg(ai)
	return n, false, err
}

func (d *decoder) value() (*Node, error) {
	if d.pos >= len(d.buf) {
		return nil, io.ErrUnexpectedEOF
	}
	start := d.pos
	ib := d.buf[d.pos]
	d.pos++
	major := ib >> 5
	ai := ib & 0x1f

	switch major {
	case 0: // 无符号整数
		n, err := d.arg(ai)
		if err != nil {
			return nil, err
		}
		return &Node{Kind: KUint, Start: start, End: d.pos, Uint: n}, nil

	case 1: // 负整数：值 = -1 - n
		n, err := d.arg(ai)
		if err != nil {
			return nil, err
		}
		return &Node{Kind: KInt, Start: start, End: d.pos, Int: -1 - int64(n)}, nil

	case 2, 3: // 字节串 / 文本串
		n, indefinite, err := d.argOrIndef(ai)
		if err != nil {
			return nil, err
		}
		var raw []byte
		if indefinite {
			// 不定长：逐段拼，直到 break(0xff)
			for {
				if d.pos >= len(d.buf) {
					return nil, io.ErrUnexpectedEOF
				}
				if d.buf[d.pos] == 0xff {
					d.pos++
					break
				}
				chunk, err := d.chunk(major)
				if err != nil {
					return nil, err
				}
				raw = append(raw, chunk...)
			}
		} else {
			if n > uint64(len(d.buf)-d.pos) {
				return nil, fmt.Errorf("字符串声明 %d 字节，但只剩 %d 字节", n, len(d.buf)-d.pos)
			}
			raw = d.buf[d.pos : d.pos+int(n)]
			d.pos += int(n)
		}
		node := &Node{Start: start, End: d.pos}
		if major == 2 {
			node.Kind = KBytes
			node.Bytes = raw
		} else {
			node.Kind = KText
			node.Text = string(raw)
		}
		return node, nil

	case 4: // 数组
		n, indefinite, err := d.argOrIndef(ai)
		if err != nil {
			return nil, err
		}
		node := &Node{Kind: KArray, Start: start}
		if indefinite {
			for {
				if d.pos >= len(d.buf) {
					return nil, io.ErrUnexpectedEOF
				}
				if d.buf[d.pos] == 0xff {
					d.pos++
					break
				}
				it, err := d.value()
				if err != nil {
					return nil, err
				}
				node.Items = append(node.Items, it)
			}
		} else {
			for i := uint64(0); i < n; i++ {
				it, err := d.value()
				if err != nil {
					return nil, fmt.Errorf("数组第 %d 项：%w", i, err)
				}
				node.Items = append(node.Items, it)
			}
		}
		node.End = d.pos
		return node, nil

	case 5: // map
		n, indefinite, err := d.argOrIndef(ai)
		if err != nil {
			return nil, err
		}
		node := &Node{Kind: KMap, Start: start}
		readPair := func() error {
			k, err := d.value()
			if err != nil {
				return err
			}
			v, err := d.value()
			if err != nil {
				return err
			}
			node.Keys = append(node.Keys, k)
			node.Values = append(node.Values, v)
			return nil
		}
		if indefinite {
			for {
				if d.pos >= len(d.buf) {
					return nil, io.ErrUnexpectedEOF
				}
				if d.buf[d.pos] == 0xff {
					d.pos++
					break
				}
				if err := readPair(); err != nil {
					return nil, err
				}
			}
		} else {
			for i := uint64(0); i < n; i++ {
				if err := readPair(); err != nil {
					return nil, fmt.Errorf("map 第 %d 对：%w", i, err)
				}
			}
		}
		node.End = d.pos
		return node, nil

	case 6: // tag：记下标签号，继续读被标记的值（偏移范围含标签）
		tag, err := d.arg(ai)
		if err != nil {
			return nil, err
		}
		inner, err := d.value()
		if err != nil {
			return nil, err
		}
		return &Node{Kind: KTag, Start: start, End: d.pos, Tag: tag, Inner: inner}, nil

	default: // major 7：简单值 / 浮点
		node := &Node{Kind: KSimple, Start: start}
		switch ai {
		case 20:
			node.Kind, node.Bool = KBool, false
		case 21:
			node.Kind, node.Bool = KBool, true
		case 22:
			node.Kind = KNull
		case 23:
			node.Kind = KUndefined
		case 24: // 1 字节 simple
			if err := d.need(1); err != nil {
				return nil, err
			}
			node.Simple = d.buf[d.pos]
			d.pos++
		case 25: // ★ half float —— 漏了它会少消费 2 字节，后面整段错位
			if err := d.need(2); err != nil {
				return nil, err
			}
			node.Kind = KFloat
			node.IsF16 = true
			node.Float = float64(float16ToFloat32(binary.BigEndian.Uint16(d.buf[d.pos:])))
			d.pos += 2
		case 26:
			if err := d.need(4); err != nil {
				return nil, err
			}
			node.Kind = KFloat
			node.Float = float64(math.Float32frombits(binary.BigEndian.Uint32(d.buf[d.pos:])))
			d.pos += 4
		case 27: // ★ float64：**只在这里消费 8 字节**（别像 JS 版那样先读一次再读一次）
			if err := d.need(8); err != nil {
				return nil, err
			}
			node.Kind = KFloat
			node.Float = math.Float64frombits(binary.BigEndian.Uint64(d.buf[d.pos:]))
			d.pos += 8
		case 31:
			return nil, fmt.Errorf("意外的 break(0xff)：不在不定长容器里")
		default: // 0..19 保留的简单值
			node.Simple = ai
		}
		node.End = d.pos
		return node, nil
	}
}

// chunk 读不定长字符串的一段。
func (d *decoder) chunk(major byte) ([]byte, error) {
	if d.pos >= len(d.buf) {
		return nil, io.ErrUnexpectedEOF
	}
	ib := d.buf[d.pos]
	d.pos++
	if ib>>5 != major || ib&0x1f == 31 {
		return nil, fmt.Errorf("不定长字符串的段头不对：0x%02x", ib)
	}
	n, err := d.arg(ib & 0x1f)
	if err != nil {
		return nil, err
	}
	if n > uint64(len(d.buf)-d.pos) {
		return nil, io.ErrUnexpectedEOF
	}
	out := d.buf[d.pos : d.pos+int(n)]
	d.pos += int(n)
	return out, nil
}

// float16ToFloat32 半精度 → 单精度（IEEE 754 binary16）。
func float16ToFloat32(h uint16) float32 {
	sign := uint32(h>>15) & 1
	exp := uint32(h>>10) & 0x1f
	frac := uint32(h) & 0x3ff
	switch exp {
	case 0:
		if frac == 0 {
			return math.Float32frombits(sign << 31)
		}
		// 非规格化
		e := uint32(127 - 15 + 1)
		for frac&0x400 == 0 {
			frac <<= 1
			e--
		}
		frac &= 0x3ff
		return math.Float32frombits(sign<<31 | e<<23 | frac<<13)
	case 0x1f:
		return math.Float32frombits(sign<<31 | 0xff<<23 | frac<<13)
	default:
		return math.Float32frombits(sign<<31 | (exp-15+127)<<23 | frac<<13)
	}
}

// ---------- 路径查找 ----------

// Get 按 `a.b[0].c` 形式的路径取值。键名不做转义（ACEP2 的键都是普通标识符）。
func Get(root *Node, path string) (*Node, error) {
	cur := root
	rest := strings.TrimPrefix(strings.TrimSpace(path), "$")
	rest = strings.TrimPrefix(rest, ".")
	for rest != "" {
		var seg string
		if rest[0] == '[' {
			i := strings.IndexByte(rest, ']')
			if i < 0 {
				return nil, fmt.Errorf("路径 %q 的 [] 没闭合", path)
			}
			idx, err := strconv.Atoi(strings.TrimSpace(rest[1:i]))
			if err != nil {
				return nil, fmt.Errorf("路径 %q 里的下标不是整数：%q", path, rest[1:i])
			}
			if cur.Kind != KArray {
				return nil, fmt.Errorf("路径 %q：%s 不是数组（是 %s），不能取 [%d]", path, segName(cur, path), cur.Kind, idx)
			}
			if idx < 0 || idx >= len(cur.Items) {
				return nil, fmt.Errorf("路径 %q：下标 %d 越界（数组长 %d）", path, idx, len(cur.Items))
			}
			cur = cur.Items[idx]
			rest = rest[i+1:]
			rest = strings.TrimPrefix(rest, ".")
			continue
		}
		j := strings.IndexAny(rest, ".[")
		if j < 0 {
			seg, rest = rest, ""
		} else {
			seg, rest = rest[:j], strings.TrimPrefix(rest[j:], ".")
		}
		if cur.Kind != KMap {
			return nil, fmt.Errorf("路径 %q：这层不是 map（是 %s），取不到键 %q", path, cur.Kind, seg)
		}
		next := cur.Get(seg)
		if next == nil {
			return nil, fmt.Errorf("路径 %q：这层 map 里没有键 %q（有的键：%s）", path, seg, strings.Join(KeyNames(cur, 12), ", "))
		}
		cur = next
	}
	return cur, nil
}

func segName(n *Node, path string) string {
	if n.Kind == KMap {
		return "map"
	}
	return n.Kind.String()
}

// KeyNames 列出 map 的键名（最多 max 个），用于错误信息。
func KeyNames(n *Node, max int) []string {
	if n.Kind != KMap {
		return nil
	}
	out := make([]string, 0, len(n.Keys))
	for i, k := range n.Keys {
		if i >= max {
			out = append(out, fmt.Sprintf("…共 %d 个", len(n.Keys)))
			break
		}
		out = append(out, k.KeyString())
	}
	return out
}

// Shape 给一个值一句人话（用于 dump 展示）。
func Shape(n *Node) string {
	switch n.Kind {
	case KMap:
		return fmt.Sprintf("map(%d 键)", len(n.Keys))
	case KArray:
		return fmt.Sprintf("array(%d 项)", len(n.Items))
	case KText:
		return fmt.Sprintf("text(%d 字符)", len([]rune(n.Text)))
	case KBytes:
		return fmt.Sprintf("bytes(%d)", len(n.Bytes))
	case KFloat:
		if n.IsF16 {
			return fmt.Sprintf("float16 %g", n.Float)
		}
		return fmt.Sprintf("float64 %g", n.Float)
	case KUint:
		return fmt.Sprintf("uint %d", n.Uint)
	case KInt:
		return fmt.Sprintf("negint %d", n.Int)
	case KBool:
		return fmt.Sprintf("bool %v", n.Bool)
	case KNull:
		return "null"
	case KUndefined:
		return "undefined"
	case KTag:
		return fmt.Sprintf("tag(%d)", n.Tag)
	default:
		return fmt.Sprintf("simple(%d)", n.Simple)
	}
}
