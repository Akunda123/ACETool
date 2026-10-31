package acep

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"acetool/internal/cbor"
)

// 本文件是**回填**（重建容器）那一半。纪律（技能 acep §4 / §8）：
//   · 永远**只写副本**，原文件一个字节都不动；
//   · 写完必须**两层自检**：文件级（长度/头原样）+ 端到端（重新解析、逐字节回读）；
//   · ACE 认不认是**第三层**，本包做不到 —— 只能让它自己读回来（CLI `project open` 或手工打开）。

// Parse 从内存解析（自检用 —— 重建出来的字节要能按同一条路径再解一遍）。
func Parse(buf []byte) (*Container, error) { return parse(buf) }

// RawBytes 是**打开时的原文件字节**。落盘时拿它当 autosave 快照（改动前的状态）。
func (c *Container) RawBytes() []byte { return c.raw }

// Repack 用新的 payload 重建**整个文件字节**。
//
//	① ACEP2（技能 §1.1）：**只改 3 处** —— off8 头长（不变）、off16 压缩长、off24 原始长；
//	   其余头字节（尤其那 1248 B 高熵区）**原样复制**。
//	② 历史壳一（技能 §1.0）：重 zstd → 重 base64 → 重建包装 JSON，**只换 content**。
func (c *Container) Repack(newPayload []byte) ([]byte, error) {
	frame, err := Encode(newPayload)
	if err != nil {
		return nil, err
	}
	switch c.Info.Kind {
	case KindACEP2:
		if len(c.header) < 32 {
			return nil, fmt.Errorf("手里没有 ACEP2 的头，没法重建")
		}
		out := make([]byte, 0, len(c.header)+len(frame))
		out = append(out, c.header...) // ← 连同 1248 B 高熵区一起原样复制
		binary.LittleEndian.PutUint64(out[16:24], uint64(len(frame)))
		binary.LittleEndian.PutUint64(out[24:32], uint64(len(newPayload)))
		out = append(out, frame...)
		return out, nil

	case KindLegacyReadable:
		if c.envelope == nil {
			return nil, fmt.Errorf("手里没有历史壳一的包装，没法重建")
		}
		env := make(map[string]json.RawMessage, len(c.envelope))
		for k, v := range c.envelope {
			env[k] = v
		}
		b64, err := json.Marshal(base64.StdEncoding.EncodeToString(frame))
		if err != nil {
			return nil, err
		}
		env["content"] = b64
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false) // 别把 debugInfo 里的 < > & 转成 \u003c
		if err := enc.Encode(env); err != nil {
			return nil, err
		}
		return bytes.TrimRight(buf.Bytes(), "\n"), nil

	default:
		return nil, fmt.Errorf("壳型 %s 不支持重建（加密壳不碰）", c.Info.Kind)
	}
}

// Check 是一条自检结论。
type Check struct {
	Name string `json:"自检项"`
	OK   bool   `json:"通过"`
	Note string `json:"说明,omitempty"`
}

// PatchResult 是一次「改一处 → 重建 → 自检」的完整留痕。
// **字节仍在内存里**，落盘是调用方的事（永远写副本）。
type PatchResult struct {
	Shell       string   `json:"壳型"`
	Edits       []string `json:"改动"`
	OldPayload  int      `json:"原payload大小"`
	NewPayload  int      `json:"新payload大小"`
	OldFileSize int      `json:"原文件大小"`
	NewFileSize int      `json:"新文件大小"`
	Checks      []Check  `json:"自检"`
	AllOK       bool     `json:"自检全过"`

	Bytes []byte `json:"-"` // 新文件字节（只给落盘用，不进 JSON）
}

// PatchText 把 path 上的**文本**改成新值。
//
//	① ACEP2：走 CBOR **原位覆盖**（只改这个 text 自己的长度前缀，技能 §2.3）。
//	② 历史壳一：走 **set-json**（整树解析 → 改路径 → 重 stringify；技能 §1.0 指定的路）。
func (c *Container) PatchText(path, newText string) (*PatchResult, error) {
	if c.Info.Kind == KindLegacyReadable {
		return c.patchJSONText(path, newText)
	}
	if c.Info.Kind != KindACEP2 {
		return nil, fmt.Errorf("壳型 %s 不支持改写", c.Info.Kind)
	}
	newPayload, edits, err := cbor.SetText(c.Payload, path, newText)
	if err != nil {
		return nil, err
	}
	what := make([]string, 0, len(edits))
	for _, e := range edits {
		what = append(what, e.What)
	}
	return c.finish(newPayload, what, func(root *cbor.Node) Check {
		return checkTextAt(root, path, newText)
	})
}

// PatchFloat64Array 覆盖一条 float64 数组（**元素个数必须不变**）。只对 ACEP2 有意义。
func (c *Container) PatchFloat64Array(path string, vals []float64) (*PatchResult, error) {
	if c.Info.Kind != KindACEP2 {
		return nil, fmt.Errorf("float64 数组的原位覆盖只对 ACEP2 的 CBOR payload 有意义；本文件是 %s", c.Info.Kind)
	}
	newPayload, _, err := cbor.SetFloat64Array(c.Payload, path, vals)
	if err != nil {
		return nil, err
	}
	what := []string{fmt.Sprintf("%s：原位覆盖 %d 个 float64（%s…）", path, len(vals), firstFew(vals))}
	return c.finish(newPayload, what, func(root *cbor.Node) Check {
		n, err := cbor.Get(root, path)
		if err != nil {
			return Check{Name: "目标路径回读", OK: false, Note: err.Error()}
		}
		if n.Kind != cbor.KArray || len(n.Items) != len(vals) {
			return Check{Name: "目标路径回读", OK: false, Note: fmt.Sprintf("回读到 %s，项数对不上", cbor.Shape(n))}
		}
		for i, want := range vals {
			if n.Items[i].Float != want {
				return Check{Name: "目标路径回读", OK: false,
					Note: fmt.Sprintf("[%d] 回读到 %g，期望 %g", i, n.Items[i].Float, want)}
			}
		}
		return Check{Name: "目标路径回读", OK: true, Note: fmt.Sprintf("%s：%d 个值逐位回读一致", path, len(vals))}
	})
}

// finish 是 ACEP2 的公共尾巴：重建 + 两道自检。
func (c *Container) finish(newPayload []byte, what []string, pathCheck func(*cbor.Node) Check) (*PatchResult, error) {
	res := &PatchResult{
		Shell:       string(c.Info.Kind),
		Edits:       what,
		OldPayload:  len(c.Payload),
		NewPayload:  len(newPayload),
		OldFileSize: len(c.raw),
	}
	defer res.finalize() // ★ AllOK 必须重算（见 finalize 的注释）
	add := res.add

	// 自检 1：新 payload 必须能**整段**解出来（恰好消费完）
	root, err := cbor.Decode(newPayload)
	if err != nil {
		add("新 payload 整段可解（恰好消费完）", false, err.Error())
		return res, nil
	}
	add("新 payload 整段可解（恰好消费完）", true, fmt.Sprintf("%d 字节", len(newPayload)))

	// 自检 2：目标路径回读
	add2(res, pathCheck(root))

	// 自检 3：重建整个文件
	out, err := c.Repack(newPayload)
	if err != nil {
		add("重建容器", false, err.Error())
		return res, nil
	}
	res.Bytes = out
	res.NewFileSize = len(out)
	add("重建容器", true, fmt.Sprintf("%d → %d 字节", res.OldFileSize, res.NewFileSize))

	// 自检 4：重建出来的文件按同一条路径**再解一遍**
	c2, err := Parse(out)
	switch {
	case err != nil:
		add("重建后的文件可再解析", false, err.Error())
	case !bytes.Equal(c2.Payload, newPayload):
		add("重建后的文件可再解析", false, "解出的 payload 与新 payload 不一致")
	default:
		add("重建后的文件可再解析", true, fmt.Sprintf("壳型 %s，payload %d 字节逐字节一致", c2.Info.Kind, len(c2.Payload)))
	}

	// 自检 5（ACEP2 专有）：头部**除那 3 个长度字段**之外与源文件逐字节相同
	// —— 技能 §1.1 的强断言，尤其那条 1248 B 高熵区"原样复制"。
	if c.Info.Kind == KindACEP2 {
		same, note := headerSameExceptLengths(c.raw, out, int(c.Info.HeadLen))
		add("头部其余字节与源文件逐字节相同（含 1248 B 高熵区）", same, note)
		if hl := int(c.Info.HeadLen); hl <= len(out) {
			compLen := int(binary.LittleEndian.Uint64(out[16:24]))
			add("文件大小 = 头长 + 压缩长", hl+compLen == len(out), fmt.Sprintf("%d + %d vs %d", hl, compLen, len(out)))
		}
	}
	return res, nil
}

func (res *PatchResult) add(name string, ok bool, note string) {
	res.Checks = append(res.Checks, Check{Name: name, OK: ok, Note: note})
}

// finalize 按检查项算出 AllOK。
// ⚠️ 必须**重算**而不是增量维护：踩过 —— add() 只会在失败时置 false，从不置回 true，
// 结果 AllOK 永远是 false（自检全过也报不过）。
func (res *PatchResult) finalize() {
	res.AllOK = len(res.Checks) > 0
	for _, c := range res.Checks {
		if !c.OK {
			res.AllOK = false
			return
		}
	}
}

func add2(res *PatchResult, c Check) { res.add(c.Name, c.OK, c.Note) }

func checkTextAt(root *cbor.Node, path, want string) Check {
	n, err := cbor.Get(root, path)
	switch {
	case err != nil:
		return Check{Name: "目标路径回读", OK: false, Note: err.Error()}
	case n.Kind != cbor.KText:
		return Check{Name: "目标路径回读", OK: false, Note: fmt.Sprintf("回读到的不是文本，是 %s", n.Kind)}
	case n.Text != want:
		return Check{Name: "目标路径回读", OK: false, Note: fmt.Sprintf("回读到 %q，期望 %q", n.Text, want)}
	}
	return Check{Name: "目标路径回读", OK: true, Note: fmt.Sprintf("%s = %q", path, n.Text)}
}

// headerSameExceptLengths 比头部 [0,headLen) 里除 off16/off24 之外的字节。
func headerSameExceptLengths(a, b []byte, headLen int) (bool, string) {
	if len(a) < headLen || len(b) < headLen {
		return false, "有一个文件短于头长"
	}
	for i := 0; i < headLen; i++ {
		if (i >= 16 && i < 24) || (i >= 24 && i < 32) {
			continue // 这两个长度字段本来就是允许变的
		}
		if a[i] != b[i] {
			return false, fmt.Sprintf("第 %d 字节不同（源 0x%02x，新 0x%02x）", i, a[i], b[i])
		}
	}
	return true, fmt.Sprintf("头 %d 字节里只有 off16/off24 变了，其余（含 1248 B 高熵区）逐字节相同", headLen)
}

// ---------- 历史壳一的 set-json（技能 §1.0 指定的那条路） ----------

func (c *Container) patchJSONText(path, newText string) (*PatchResult, error) {
	var doc any
	if err := json.Unmarshal(c.Payload, &doc); err != nil {
		return nil, fmt.Errorf("内层不是合法 JSON：%w", err)
	}
	if err := setJSONPath(&doc, path, newText); err != nil {
		return nil, err
	}
	// 重 stringify（技能：整树解析 → 改路径 → 重 stringify）。
	// ⚠️ 这会重排键序、归一空白 —— 内层本就是 JSON 文本，ACE 读的是值不是格式；但要说清。
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	newPayload := bytes.TrimRight(buf.Bytes(), "\n")

	res := &PatchResult{
		Shell:       string(c.Info.Kind),
		Edits:       []string{fmt.Sprintf("set-json %s → %q", path, newText)},
		OldPayload:  len(c.Payload),
		NewPayload:  len(newPayload),
		OldFileSize: len(c.raw),
	}
	defer res.finalize() // ★ AllOK 必须重算（见 finalize 的注释）

	// 自检 1：新内层是合法 JSON，且目标路径回读正确
	var doc2 any
	if err := json.Unmarshal(newPayload, &doc2); err != nil {
		res.add("新内层是合法 JSON", false, err.Error())
	} else {
		res.add("新内层是合法 JSON", true, fmt.Sprintf("%d → %d 字节", len(c.Payload), len(newPayload)))
		if got, err := getJSONPath(doc2, path); err != nil {
			res.add("目标路径回读", false, err.Error())
		} else if got != newText {
			res.add("目标路径回读", false, fmt.Sprintf("回读到 %v，期望 %q", got, newText))
		} else {
			res.add("目标路径回读", true, fmt.Sprintf("%s = %q", path, newText))
		}
	}

	// 自检 2：重建 + 再解析
	out, err := c.Repack(newPayload)
	if err != nil {
		res.add("重建容器", false, err.Error())
		return res, nil
	}
	res.Bytes = out
	res.NewFileSize = len(out)
	res.add("重建容器", true, fmt.Sprintf("%d → %d 字节（重 zstd + 重 base64）", res.OldFileSize, res.NewFileSize))

	c2, err := Parse(out)
	switch {
	case err != nil:
		res.add("重建后的文件可再解析", false, err.Error())
	case !bytes.Equal(c2.Payload, newPayload):
		res.add("重建后的文件可再解析", false, "解出的内层与新内层不一致")
	default:
		res.add("重建后的文件可再解析", true, fmt.Sprintf("壳型 %s，内层逐字节一致", c2.Info.Kind))
	}
	res.add("包装只换了 content", true, "历史壳一：其余键按原样保留，但键序会被重排（形如 ABC 序）")
	return res, nil
}

// setJSONPath 按 `a.b[0].c` 改 JSON 树里的一个文本值。
func setJSONPath(doc *any, path, val string) error {
	segs, err := parsePath(path)
	if err != nil {
		return err
	}
	cur := doc
	for i, s := range segs {
		last := i == len(segs)-1
		switch node := (*cur).(type) {
		case map[string]any:
			if s.key == "" {
				return fmt.Errorf("路径 %q：第 %d 段是 [下标]，但这层是对象", path, i+1)
			}
			if last {
				if _, ok := node[s.key]; !ok {
					return fmt.Errorf("路径 %q：没有键 %q（有的键：%s）", path, s.key, jsonKeys(node, 10))
				}
				node[s.key] = val
				return nil
			}
			next, ok := node[s.key]
			if !ok {
				return fmt.Errorf("路径 %q：没有键 %q（有的键：%s）", path, s.key, jsonKeys(node, 10))
			}
			cur = &next
		case []any:
			if s.key != "" {
				return fmt.Errorf("路径 %q：第 %d 段是键 %q，但这层是数组", path, i+1, s.key)
			}
			if s.idx < 0 || s.idx >= len(node) {
				return fmt.Errorf("路径 %q：下标 %d 越界（长 %d）", path, s.idx, len(node))
			}
			if last {
				node[s.idx] = val
				return nil
			}
			cur = &node[s.idx]
		default:
			return fmt.Errorf("路径 %q：第 %d 段遇到 %T，走不下去", path, i+1, *cur)
		}
	}
	return fmt.Errorf("路径 %q 是空的", path)
}

// getJSONPath 读 JSON 树里的值（自检回读用）。
func getJSONPath(doc any, path string) (any, error) {
	segs, err := parsePath(path)
	if err != nil {
		return nil, err
	}
	cur := doc
	for i, s := range segs {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[s.key]
			if !ok {
				return nil, fmt.Errorf("路径 %q：第 %d 段没有键 %q", path, i+1, s.key)
			}
			cur = v
		case []any:
			if s.idx < 0 || s.idx >= len(node) {
				return nil, fmt.Errorf("路径 %q：下标 %d 越界（长 %d）", path, s.idx, len(node))
			}
			cur = node[s.idx]
		default:
			return nil, fmt.Errorf("路径 %q：第 %d 段遇到 %T，走不下去", path, i+1, cur)
		}
	}
	return cur, nil
}

type pathSeg struct {
	key string
	idx int
}

func parsePath(path string) ([]pathSeg, error) {
	rest := strings.TrimPrefix(strings.TrimSpace(path), "$")
	rest = strings.TrimPrefix(rest, ".")
	var out []pathSeg
	for rest != "" {
		if rest[0] == '[' {
			i := strings.IndexByte(rest, ']')
			if i < 0 {
				return nil, fmt.Errorf("路径 %q 的 [] 没闭合", path)
			}
			n, err := strconv.Atoi(strings.TrimSpace(rest[1:i]))
			if err != nil {
				return nil, fmt.Errorf("路径 %q 的下标不是整数：%q", path, rest[1:i])
			}
			out = append(out, pathSeg{idx: n})
			rest = strings.TrimPrefix(rest[i+1:], ".")
			continue
		}
		j := strings.IndexAny(rest, ".[")
		if j < 0 {
			out = append(out, pathSeg{key: rest, idx: -1})
			rest = ""
		} else {
			out = append(out, pathSeg{key: rest[:j], idx: -1})
			rest = strings.TrimPrefix(rest[j:], ".")
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("路径 %q 是空的", path)
	}
	return out, nil
}

func jsonKeys(m map[string]any, max int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
		if len(keys) >= max {
			break
		}
	}
	return strings.Join(keys, ", ")
}

func firstFew(vals []float64) string {
	n := len(vals)
	if n > 3 {
		n = 3
	}
	parts := make([]string, 0, n)
	for _, v := range vals[:n] {
		parts = append(parts, strconv.FormatFloat(v, 'g', -1, 64))
	}
	return strings.Join(parts, ",")
}

// ---------- 落盘：只写副本，并按"工程包"布局（技能 §5.2） ----------

// WrittenFile 是落盘结果的一项。
type WrittenFile struct {
	Path string `json:"路径"`
	Size int    `json:"大小,omitempty"`
	What string `json:"是什么"`
}

// WritePackage 把新文件按"工程包"布局写到 outDir 下：
//
//	<outDir>\<名字>\<名字>.acep                        ← 改过的（全新文件，**原文件没动**）
//	<outDir>\<名字>\autosave\<名字>_save_<时间戳>.acep   ← 改动前原文件的快照
//	<outDir>\<名字>\Samples\                            ← 空目录
//
// ⚠️ 为什么必须成"包"：技能 §5.2 实测 —— 只放一个 `.acep` 的文件夹会被 ACE 当成
// "无名临时工程"（`projectName:""`、`projectPath:""`），后续 `project save` 的去向也不对。
func WritePackage(outDir, name string, original, patched []byte) ([]WrittenFile, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("要一个工程名字（用来命名包目录）")
	}
	pkg := filepath.Join(outDir, name)
	if err := os.MkdirAll(filepath.Join(pkg, "Samples"), 0o755); err != nil {
		return nil, err
	}
	var made []WrittenFile

	main := filepath.Join(pkg, name+".acep")
	if _, err := os.Stat(main); err == nil {
		return nil, fmt.Errorf("目标已存在，不覆盖：%s（换个输出目录或换个名字）", main)
	}
	if err := os.WriteFile(main, patched, 0o644); err != nil {
		return nil, err
	}
	made = append(made, WrittenFile{Path: main, Size: len(patched), What: "改过的工程"})

	// autosave 快照：放**改动前**的原文件（快照该有的语义）
	stamp := time.Now().Format("2006_01_02_15_04_05_000")
	snap := filepath.Join(pkg, "autosave", name+"_save_"+stamp+".acep")
	if err := os.MkdirAll(filepath.Dir(snap), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(snap, original, 0o644); err != nil {
		return nil, err
	}
	made = append(made, WrittenFile{Path: snap, Size: len(original), What: "改动前原文件的快照"})

	made = append(made, WrittenFile{Path: filepath.Join(pkg, "Samples"), What: "采样目录（空）"})
	return made, nil
}
