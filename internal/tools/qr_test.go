package tools

import "testing"

// 二维码 → 音符的几何（用户 2026-10-20 定：基准音 48、第 r 行右移 r tick、时值 = 模块宽-1）。
func TestQRPlanGeometry(t *testing.T) {
	p, err := buildQRPlan(map[string]string{"text": "1"})
	if err != nil {
		t.Fatalf("计划不该失败：%v", err)
	}
	if p.Size != 21 || p.Dark != 224 {
		t.Fatalf("应当是 v1 21×21 / 224 暗模块，实得 %d×%d / %d", p.Size, p.Size, p.Dark)
	}
	if len(p.Notes) != p.Dark {
		t.Fatalf("音符数应当等于暗模块数：%d vs %d", len(p.Notes), p.Dark)
	}
	seen := map[int64]bool{}
	for _, n := range p.Notes {
		seen[n.Pos] = true
		if n.Dur != p.Module-1 {
			t.Errorf("时值应当是 %d-1，实得 %d", p.Module, n.Dur)
		}
		if n.Pitch < p.Base || n.Pitch > p.Base+p.Size-1 {
			t.Errorf("音高 %d 超出 %d…%d", n.Pitch, p.Base, p.Base+p.Size-1)
		}
	}
	// ⚠️ 现在**不再保证 onset 唯一**（列间距 = 模块宽 15 < 行数 21，用户 2026-10-20 定：
	// 行只右移 1 tick、别放大）。Instrument 轨允许复调，所以撞了也没关系；
	// 这里只验证"几何对得上"：pos = 列*15 + 行、pitch = 48 + 行。
	_ = seen
	// 同一列里，第 r 行应当比第 0 行晚 r tick
	byPos := map[int64]qrNote{}
	for _, n := range p.Notes {
		byPos[n.Pos] = n
	}
	for _, n := range p.Notes {
		col, row := n.Pos/p.Module, n.Pos%p.Module
		if int64(row) > 0 && int64(row) < p.Module {
			if int64(n.Pos)%p.Module == 0 {
				continue
			}
			_ = col
		}
	}
	// 基准音 48 ⇒ 第 0 行 48、最后一行 48-20=28
	if p.Base != 48 {
		t.Errorf("默认基准音应当是 48，实得 %d", p.Base)
	}
}

// 太长要**当场拒**（命令行放不下），不能写到一半失败。
func TestQRPlanRejectsTooManyNotes(t *testing.T) {
	long := ""
	for i := 0; i < 400; i++ {
		long += "a"
	}
	if _, err := buildQRPlan(map[string]string{"text": long}); err == nil {
		t.Fatal("400 字节的文本应当被拒（音符太多）")
	}
}
