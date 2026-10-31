package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"acetool/internal/acep"
	"acetool/internal/cbor"
)

// ---------- 造一份带 uuid 的最小工程（真实工程里 notes[*] 有 14 个键，这里只要用的那几个） ----------

type vibNote struct {
	uuid string
	pos  int
	vib  bool
}

// vibPayload 造 payload：`tracks[0]` 是占位槽（type empty），`tracks[1]` 是真轨；
// pats 里每个元素是一份 note 列表（对应一个片段）。
func vibPayload(t *testing.T, pats ...[]vibNote) []byte {
	t.Helper()
	rawPats := cbor.EncodeArrayHeader(uint64(len(pats)))
	for pi, notes := range pats {
		arr := cbor.EncodeArrayHeader(uint64(len(notes)))
		for _, n := range notes {
			keys := 3
			if n.vib {
				keys = 4
			}
			b := cbor.EncodeMapHeader(uint64(keys))
			b = append(b, cbor.EncodeText("uuid")...)
			b = append(b, cbor.EncodeText(n.uuid)...)
			b = append(b, cbor.EncodeText("pos")...)
			b = append(b, cbor.EncodeUint(uint64(n.pos))...)
			b = append(b, cbor.EncodeText("pitch")...)
			b = append(b, cbor.EncodeUint(60)...)
			if n.vib {
				b = append(b, cbor.EncodeText("vibrato")...)
				b = append(b, cbor.EncodeMap(
					[2]any{"startPos", 133},
					[2]any{"frequency", 6.0},
					[2]any{"amplitude", 2.5},
					[2]any{"phase", 0.0},
					[2]any{"attackRatio", 0.2},
					[2]any{"attackLevel", 0.8},
					[2]any{"releaseRatio", 0.1},
					[2]any{"releaseLevel", 1.0},
				)...)
			}
			arr = append(arr, b...)
		}
		pat := cbor.EncodeMapHeader(2)
		pat = append(pat, cbor.EncodeText("name")...)
		pat = append(pat, cbor.EncodeText("片段"+string(rune('1'+pi)))...)
		pat = append(pat, cbor.EncodeText("notes")...)
		pat = append(pat, arr...)
		rawPats = append(rawPats, pat...)
	}

	empty := cbor.EncodeMapHeader(2)
	empty = append(empty, cbor.EncodeText("type")...)
	empty = append(empty, cbor.EncodeText("empty")...)
	empty = append(empty, cbor.EncodeText("patterns")...)
	empty = append(empty, cbor.EncodeArrayHeader(0)...)

	tr := cbor.EncodeMapHeader(3)
	tr = append(tr, cbor.EncodeText("name")...)
	tr = append(tr, cbor.EncodeText("轨 0")...)
	tr = append(tr, cbor.EncodeText("type")...)
	tr = append(tr, cbor.EncodeText("sing")...)
	tr = append(tr, cbor.EncodeText("patterns")...)
	tr = append(tr, rawPats...)

	tracks := cbor.EncodeArrayHeader(2)
	tracks = append(tracks, empty...)
	tracks = append(tracks, tr...)

	root := cbor.EncodeMapHeader(1)
	root = append(root, cbor.EncodeText("tracks")...)
	root = append(root, tracks...)
	return root
}

func writeTempACEP(t *testing.T, payload []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "测试工程.acep")
	if err := os.WriteFile(p, wrapACEP2(t, payload), 0o644); err != nil {
		t.Fatalf("写临时工程：%v", err)
	}
	return p
}

func readNoteVib(t *testing.T, p, uuid string) *cbor.Node {
	t.Helper()
	cont, err := acep.Open(p)
	if err != nil {
		t.Fatalf("回读打开：%v", err)
	}
	root, err := cbor.Decode(cont.Payload)
	if err != nil {
		t.Fatalf("回读解码：%v", err)
	}
	_, n, err := findNoteByUUID(root, uuid)
	if err != nil {
		t.Fatalf("回读找音符 %s：%v", uuid, err)
	}
	return n.Get("vibrato")
}

// ---------- ① 纯函数 ----------

func TestNormUUIDAndSplitUUIDs(t *testing.T) {
	if normUUID(`"{ABC-1}"`) != "abc-1" {
		t.Fatalf("归一化失败：%q", normUUID(`"{ABC-1}"`))
	}
	got := splitUUIDs("{a},{b} {a};\n{c}")
	if len(got) != 3 {
		t.Fatalf("去重失败：%v", got)
	}
	if len(splitUUIDs("  ")) != 0 {
		t.Fatal("空串应当回空列表")
	}
}

func TestVibratoSummary(t *testing.T) {
	v, err := cbor.Decode(cbor.EncodeMap([2]any{"startPos", 133}, [2]any{"frequency", 6.0}, [2]any{"amplitude", 2.5}))
	if err != nil {
		t.Fatal(err)
	}
	s := vibratoSummary(v)
	// ⚠️ 单位口径（用户 2026-10-14）：`startPos` 是 tick，印出来必须是**拍**（133/480 = 0.28）
	for _, want := range []string{"第 0.28 拍", "6.00 Hz", "2.500"} {
		if !strings.Contains(s, want) {
			t.Fatalf("摘要少了 %q：%s", want, s)
		}
	}
	if strings.Contains(s, "tick") {
		t.Fatalf("摘要里不该再出现 tick：%s", s)
	}
	if !strings.Contains(vibratoSummary(nil), "没有颤音") {
		t.Fatal("没有颤音时要说出来")
	}
}

// ---------- ② 真写（临时文件 + 备份 + 回读） ----------

func TestVibratoApplyCopiesToTargets(t *testing.T) {
	p := writeTempACEP(t, vibPayload(t,
		[]vibNote{{uuid: "{u1}", pos: 240, vib: true}, {uuid: "{u2}", pos: 900}, {uuid: "{u3}", pos: 1920}},
	))
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	out, err := vibratoApplyApply(context.Background(), nil, map[string]string{
		"path": p, "template": "{u1}", "targets": "{u2},{u3}",
	})
	if err != nil {
		t.Fatalf("执行失败：%v", err)
	}
	rep, ok := out.(*pitchCopyReport)
	if !ok {
		t.Fatalf("回包类型不对：%T", out)
	}
	if !strings.Contains(rep.Vibrato.Summary, "2 个") {
		t.Fatalf("报告没写清写了几个：%s", rep.Vibrato.Summary)
	}

	// 目标音符拿到了模板那份颤音
	for _, u := range []string{"{u2}", "{u3}"} {
		v := readNoteVib(t, p, u)
		if v == nil {
			t.Fatalf("%s 上没有颤音", u)
		}
		if f, ok := nodeFloat(v.Get("frequency")); !ok || f != 6 {
			t.Fatalf("%s 的频率没复制过来：%v", u, v.Get("frequency"))
		}
		if x, ok := nodeFloat(v.Get("amplitude")); !ok || x != 2.5 {
			t.Fatalf("%s 的幅度没复制过来", u)
		}
	}
	// 模板自己还在，且文件真的变了
	if v := readNoteVib(t, p, "{u1}"); v == nil {
		t.Fatal("模板音符的颤音被弄丢了")
	}
	after, _ := os.ReadFile(p)
	if len(after) == len(before) && string(after) == string(before) {
		t.Fatal("文件没变 —— 没写进去")
	}
	// 备份是硬要求
	if b, err := latestBackupFor(p); err != nil || b == "" {
		t.Fatalf("没找到备份：%v", err)
	}
}

func TestVibratoApplyRefusesTemplateWithoutVibrato(t *testing.T) {
	p := writeTempACEP(t, vibPayload(t,
		[]vibNote{{uuid: "{u1}", pos: 240, vib: true}, {uuid: "{u2}", pos: 900}},
	))
	_, err := vibratoApplyApply(context.Background(), nil, map[string]string{
		"path": p, "template": "{u2}", "targets": "{u1}",
	})
	if err == nil || !strings.Contains(err.Error(), "没有颤音") {
		t.Fatalf("模板没颤音时必须拒绝，实际：%v", err)
	}
}

func TestVibratoApplyRefusesCrossPattern(t *testing.T) {
	p := writeTempACEP(t, vibPayload(t,
		[]vibNote{{uuid: "{u1}", pos: 240, vib: true}},
		[]vibNote{{uuid: "{u2}", pos: 900}},
	))
	_, err := vibratoApplyApply(context.Background(), nil, map[string]string{
		"path": p, "template": "{u1}", "targets": "{u2}",
	})
	if err == nil || !strings.Contains(err.Error(), "同一个片段") {
		t.Fatalf("跨片段必须如实拒绝，实际：%v", err)
	}
}

func TestVibratoApplyRefusesUnknownTemplate(t *testing.T) {
	p := writeTempACEP(t, vibPayload(t, []vibNote{{uuid: "{u1}", pos: 240, vib: true}}))
	_, err := vibratoApplyApply(context.Background(), nil, map[string]string{
		"path": p, "template": "{查不到}", "targets": "{u1}",
	})
	if err == nil || !strings.Contains(err.Error(), "找不到") {
		t.Fatalf("模板找不到时必须拒绝，实际：%v", err)
	}
}

// ---------- ③ 注册面（页面对着的接口） ----------

func TestVibratoToolsRegistered(t *testing.T) {
	for _, id := range []string{"vibrato.read", "vibrato.apply", "selection.clear"} {
		tl := Get(id)
		if tl == nil {
			t.Fatalf("%s 没注册", id)
		}
		if id == "vibrato.read" && tl.Kind != KindRead {
			t.Fatalf("vibrato.read 应当是只读")
		}
		if id == "selection.clear" && tl.Kind != KindWrite {
			t.Fatalf("selection.clear 是写")
		}
	}
}
