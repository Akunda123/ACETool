//go:build windows

package win

import (
	"os"
	"strings"
	"testing"
)

// 剪贴板往返。⚠️ 这是**动用户真剪贴板**的测试 ⇒ 三条自律：
//
//	① 只在本机剪贴板里是**文本或空的**时候跑（里面有图片/文件就跳过 —— 我们不能把它冲掉）
//	② 跑完把原来的文本**放回去**（原来的内容不是文本时那条路已经跳过了）
//	③ 用一段带中文/空格/两端空白的字符串，验"一字不差"
func TestClipboardRoundTrip(t *testing.T) {
	orig, err := ClipboardText()
	if err != nil {
		t.Skipf("读不到剪贴板（多半是它正被别的程序占着）：%v", err)
	}
	if strings.TrimSpace(orig) == "" {
		// 空剪贴板也可能其实装着图片/文件（那时 ClipBoardText 返回空串）⇒ 保守跳过。
		// 要**明知故犯**地跑（我们自己验收这条往返用；用户的剪贴板此刻是空的）就设这个环境变量。
		if os.Getenv("ACETOOL_CLIPBOARD_PROBE") == "" {
			t.Skip("剪贴板里没有文本（也可能装着图片/文件）—— 不拿它冒险，跳过（要强跑：ACETOOL_CLIPBOARD_PROBE=1）")
		}
		t.Log("剪贴板里没有文本，但设了 ACETOOL_CLIPBOARD_PROBE ⇒ 强跑这一次（跑完放回空文本）")
	}

	const want = "ACETool 剪贴板往返 · test 123 · 两端空白 "
	if err := SetClipboardText(want); err != nil {
		t.Fatalf("SetClipboardText：%v", err)
	}
	got, err := ClipboardText()
	if err != nil {
		t.Fatalf("ClipboardText：%v", err)
	}
	if got != want {
		t.Errorf("往返不一致：写进去 %q，读回来 %q", want, got)
	}
	// 放回去（失败也**不能**让测试通过 —— 用户的剪贴板被我们改了，必须说出来）
	if err := SetClipboardText(orig); err != nil {
		t.Errorf("原内容没能放回去：%v（原内容前 40 字符：%.40q）", err, orig)
	} else if back, _ := ClipboardText(); back != orig {
		t.Errorf("放回去的内容跟原来不一致：原来 %.40q，现在 %.40q", orig, back)
	}
}
