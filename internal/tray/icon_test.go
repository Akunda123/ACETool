//go:build windows

package tray

import (
	"bytes"
	"image/png"
	"testing"
)

// 嵌入的 6 档图标必须都在、尺寸对、而且**真的画着东西**（不是一张全透明的空图）。
//
// 为什么值得一条测试：图标是**光栅化产物**（`devtools/make-tray-icon.js` 生成的 PNG），
// 换素材/改取景框时最典型的翻车就是"图偏了/没画上"—— 那种错在托盘上只表现为"看不见"，
// 肉眼很难判断是没加图标还是图是空的。这里直接量像素。
func TestIconPNGs(t *testing.T) {
	for _, size := range iconSizes {
		b, err := iconPNG(size)
		if err != nil {
			t.Fatalf("取不到 %dpx 图标：%v", size, err)
		}
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("%dpx 图标不是合法 PNG：%v", size, err)
		}
		bd := img.Bounds()
		if bd.Dx() != size || bd.Dy() != size {
			t.Fatalf("%dpx 图标的实际尺寸是 %dx%d（预期正方形 %d）", size, bd.Dx(), bd.Dy(), size)
		}
		// 统计：不透明总面积 / 黑色实心 / **偏白的像素**（白描边）
		//
		// ⚠️ 白描边的门槛不能按"纯白 + 全不透明"来定：白边是**亚像素**宽（16px 上只有 0.5px），
		// 边缘像素的 alpha 天然是半透的（~100–180），还会和黑色抗锯齿边混一点灰。
		// 所以判据取"够亮 + 有点不透明"，能证明那圈白边画上了就够（形状/位置由上面两条守着）。
		opaque, dark, halo := 0, 0, 0
		for y := 0; y < bd.Dy(); y++ {
			for x := 0; x < bd.Dx(); x++ {
				r, g, bb, a := img.At(bd.Min.X+x, bd.Min.Y+y).RGBA()
				if a>>8 > 200 {
					opaque++
					if (r>>8) < 60 && (g>>8) < 60 && (bb>>8) < 60 {
						dark++
					}
				}
				if a>>8 > 80 && (r>>8) > 140 && (g>>8) > 140 && (bb>>8) > 140 {
					halo++
				}
			}
		}
		total := bd.Dx() * bd.Dy()
		if dark*100/total < 8 {
			t.Fatalf("%dpx 图标里黑色实心像素只有 %d/%d（<8%%）—— 取景框八成偏了，画到了空白上",
				size, dark, total)
		}
		if opaque*100/total > 95 {
			t.Fatalf("%dpx 图标几乎整张不透明（%d/%d）—— 背景没透明，托盘上会看到一个黑方块",
				size, opaque, total)
		}
		// 用户 2026-10-17：「可以给 a 加一个小的白色描边」⇒ 每档都要量到那圈白边
		t.Logf("%dpx：黑=%d 偏白边=%d 不透明=%d（共 %d）", size, dark, halo, opaque, total)
		if halo < 6 {
			t.Fatalf("%dpx 图标里偏白的像素只有 %d 个 —— 白色描边没画上（脚本里那条 #halo path）", size, halo)
		}
	}
}

// pickSize：DPI 与档位的对应关系要说得准（150% ⇒ 24px，200% ⇒ 32px）。
func TestPickSize(t *testing.T) {
	cases := map[int]int{16: 16, 20: 20, 24: 24, 30: 32, 32: 32, 40: 40, 48: 48, 64: 48, 8: 16}
	for want, expect := range cases {
		if got := pickSize(want); got != expect {
			t.Fatalf("pickSize(%d) = %d，期望 %d", want, got, expect)
		}
	}
}
