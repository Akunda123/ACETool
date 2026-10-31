package cbor

import (
	"math"
	"testing"
)

// EncodeNode 必须**值语义等价**地往返：解 → 编 → 再解，两边逐字段相同。
// （字节形态可以不逐字节相同 —— 半精度会升成 f64、不定长会变定长，见 encode.go 的说明。）
func TestEncodeNodeRoundTrip(t *testing.T) {
	// 一份"什么都有"的 payload：文本键 map + 数组 + 浮点 + 布尔 + 负整数 + 嵌套 + 半精度
	payload := []byte{}
	payload = append(payload, EncodeMapHeader(6)...)
	payload = append(payload, EncodeText("name")...)
	payload = append(payload, EncodeText("轨 0")...)
	payload = append(payload, EncodeText("pitch")...)
	payload = append(payload, EncodeFloat64(60.5)...)
	payload = append(payload, EncodeText("neg")...)
	payload = append(payload, appendHeader(nil, 1, 4)...) // -5
	payload = append(payload, EncodeText("yes")...)
	payload = append(payload, 0xf5)
	payload = append(payload, EncodeText("vals")...)
	payload = append(payload, EncodeArrayHeader(3)...)
	payload = append(payload, EncodeFloat64(0)...)
	payload = append(payload, EncodeFloat64(-1.25)...)
	payload = append(payload, EncodeFloat64(3.5)...)
	payload = append(payload, EncodeText("nested")...)
	payload = append(payload, EncodeMapHeader(1)...)
	payload = append(payload, EncodeText("half")...)
	payload = append(payload, 0xf9, 0x3c, 0x00) // half float 1.0

	root, err := Decode(payload)
	if err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	again := EncodeNode(root)
	root2, err := Decode(again)
	if err != nil {
		t.Fatalf("重编出来的字节解不开：%v（% x）", err, again)
	}
	if len(root2.Keys) != 6 {
		t.Fatalf("顶层键数不对：%d（Shape=%s）", len(root2.Keys), Shape(root2))
	}
	if v := root2.Get("name"); v == nil || v.Text != "轨 0" {
		t.Fatalf("名字没保住：%+v", v)
	}
	if v := root2.Get("pitch"); v == nil || v.Float != 60.5 {
		t.Fatalf("浮点没保住：%+v", v)
	}
	if v := root2.Get("neg"); v == nil || v.Int != -5 {
		t.Fatalf("负整数没保住：%+v", v)
	}
	if v := root2.Get("yes"); v == nil || v.Kind != KBool || !v.Bool {
		t.Fatalf("布尔没保住：%+v", v)
	}
	vals := root2.Get("vals")
	if vals == nil || vals.Kind != KArray || len(vals.Items) != 3 {
		t.Fatalf("数组没保住：%+v", vals)
	}
	for i, want := range []float64{0, -1.25, 3.5} {
		if vals.Items[i].Float != want {
			t.Fatalf("vals[%d] = %v，期望 %v", i, vals.Items[i].Float, want)
		}
	}
	half := root2.Get("nested").Get("half")
	if half == nil || math.Abs(half.Float-1.0) > 1e-9 {
		t.Fatalf("半精度没升对：%+v", half)
	}

	// 幂等：再编一次应当逐字节相同（稳定形态）
	if string(EncodeNode(root2)) != string(again) {
		t.Fatal("重编码不幂等（第二次编出来的字节不一样）")
	}
}

// EncodeMap / 数组便利分支也要能编出能被自己解开的东西（音高线那几条写的形状）。
func TestEncodeMapShapes(t *testing.T) {
	entry := EncodeMap(
		[2]any{"type", "data"},
		[2]any{"offset", 360},
		[2]any{"values", []float64{0, 0.5, -1.5}},
	)
	n, err := Decode(entry)
	if err != nil {
		t.Fatalf("解不开：%v", err)
	}
	if n.Get("type").Text != "data" || n.Get("offset").Uint != 360 {
		t.Fatalf("字段不对：%+v", n)
	}
	if got := len(n.Get("values").Items); got != 3 {
		t.Fatalf("values 长度 %d，期望 3", got)
	}

	anch := EncodeMap(
		[2]any{"type", "anchor"},
		[2]any{"points", []any{100, 61.5, 200, 63.25}},
		[2]any{"pointsVUV", []float64{1, 0}},
	)
	a, err := Decode(anch)
	if err != nil {
		t.Fatalf("anchor 解不开：%v", err)
	}
	pts := a.Get("points")
	if len(pts.Items) != 4 || pts.Items[1].Float != 61.5 {
		t.Fatalf("锚点不对：%+v", pts)
	}
}

// Apply + EncodeNode 的组合：往一个 map 里**加一个键**（这正是写 `notes[k].vibrato` 的做法）。
func TestEncodeNodeGrowsMapAndApplies(t *testing.T) {
	payload := EncodeMapHeader(2)
	payload = append(payload, EncodeText("uuid")...)
	payload = append(payload, EncodeText("{n1}")...)
	payload = append(payload, EncodeText("pitch")...)
	payload = append(payload, EncodeUint(60)...)

	root, err := Decode(payload)
	if err != nil {
		t.Fatalf("解不开：%v", err)
	}
	// 造一个新 map：原有键 + vibrato
	keys := append([]*Node(nil), root.Keys...)
	vals := append([]*Node(nil), root.Values...)
	keys = append(keys, &Node{Kind: KText, Text: "vibrato"})
	vals = append(vals, mustDecode(t, EncodeMap([2]any{"frequency", 6.0}, [2]any{"amplitude", 2.0})))
	bigger := &Node{Kind: KMap, Keys: keys, Values: vals}

	out, err := Apply(payload, []Edit{{Start: root.Start, End: root.End, Bytes: EncodeNode(bigger), What: "加 vibrato"}})
	if err != nil {
		t.Fatalf("Apply 失败：%v", err)
	}
	n2, err := Decode(out)
	if err != nil {
		t.Fatalf("加键之后解不开：%v", err)
	}
	v := n2.Get("vibrato")
	if v == nil || v.Get("frequency").Float != 6 {
		t.Fatalf("vibrato 没写进去：%+v", v)
	}
	if n2.Get("uuid").Text != "{n1}" || n2.Get("pitch").Uint != 60 {
		t.Fatal("原有键丢了")
	}
}

func mustDecode(t *testing.T, b []byte) *Node {
	t.Helper()
	n, err := Decode(b)
	if err != nil {
		t.Fatalf("解不开：%v", err)
	}
	return n
}
