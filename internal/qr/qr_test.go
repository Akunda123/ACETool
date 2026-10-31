package qr

import "testing"

// 交接文档 §3 的实测值：这是唯一能证明"移植没跑偏"的锚点。
func TestEncodeOne(t *testing.T) {
	c, err := Encode("1")
	if err != nil {
		t.Fatalf("编码失败：%v", err)
	}
	if c.Version != 1 || c.Size != 21 {
		t.Errorf("版本/尺寸应当是 v1 21×21，实得 v%d %d×%d", c.Version, c.Size, c.Size)
	}
	if got := c.DarkCount(); got != 224 {
		t.Errorf("暗模块应当是 224，实得 %d", got)
	}
	if c.Mask != 4 {
		t.Errorf("掩码应当选 4，实得 %d", c.Mask)
	}
	if got := c.FormatBits(); got != 0x762 {
		t.Errorf("格式信息应当是 0x762，实得 0x%X", got)
	}
}

// 对照 `Documents\qrcode-test.png`：URL → v3 29×29。
func TestEncodeURL(t *testing.T) {
	c, err := Encode("https://example.com")
	if err != nil {
		t.Fatalf("编码失败：%v", err)
	}
	if c.Version != 3 || c.Size != 29 {
		t.Errorf("应当是 v3 29×29，实得 v%d %d×%d", c.Version, c.Size, c.Size)
	}
}

// 定位图案必须在三个角上（角上暗 7×7 的外框）。
func TestFinderPatterns(t *testing.T) {
	c, _ := Encode("1")
	for _, rc := range [][2]int{{0, 0}, {0, c.Size - 7}, {c.Size - 7, 0}} {
		for r := 0; r < 7; r++ {
			for cc := 0; cc < 7; cc++ {
				want := r == 0 || r == 6 || cc == 0 || cc == 6
				if r >= 2 && r <= 4 && cc >= 2 && cc <= 4 {
					want = true
				}
				if got := c.Module[rc[0]+r][rc[1]+cc]; got != want {
					t.Fatalf("定位图案 (%d,%d) 应当 dark=%v，实得 %v", rc[0]+r, rc[1]+cc, want, got)
				}
			}
		}
	}
}

// 长文本要能自动升版本（至少不报错、尺寸自洽）。
func TestVersionGrow(t *testing.T) {
	long := ""
	for i := 0; i < 300; i++ {
		long += "a"
	}
	c, err := Encode(long)
	if err != nil {
		t.Fatalf("300 字节不该失败：%v", err)
	}
	if c.Size != 17+4*c.Version {
		t.Errorf("尺寸与版本不自洽：v%d %d", c.Version, c.Size)
	}
}
