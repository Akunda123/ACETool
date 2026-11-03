package phoneme

import (
	"encoding/json"
	"os"
	"testing"
)

// 真机抓的 inventory（2026-11-03 用 ACE 2.1.9 的 `phoneme inventory --language <L> --json` 存的）。
// 用来保证：inventory 里每个符号都能拿到类别/发音方式 —— 拿不到就说明兜底规则有洞。
type inventory struct {
	Consonants      []string          `json:"consonants"`
	Vowels          []string          `json:"vowels"`
	PhoneticsHints  map[string]string `json:"phoneticsHints"`
	DefaultPhonemes []string          `json:"defaultPhonemes"`
}

func loadInventory(t *testing.T, lang string) inventory {
	t.Helper()
	b, err := os.ReadFile("source/inventory-" + lang + ".json")
	if err != nil {
		t.Skipf("没有 %s 的 inventory 夹具：%v", lang, err)
	}
	var inv inventory
	if err := json.Unmarshal(b, &inv); err != nil {
		t.Fatalf("夹具解不开：%v", err)
	}
	return inv
}

func TestInventorySymbolsAllDescribed(t *testing.T) {
	for _, lang := range []string{"zh", "eng", "jp"} {
		inv := loadInventory(t, lang)
		var missing []string
		check := func(sym string) {
			e := Describe(lang, sym, inv.PhoneticsHints[sym])
			if e.Manner == "" {
				missing = append(missing, sym+"("+inv.PhoneticsHints[sym]+")")
			}
		}
		for _, s := range inv.Consonants {
			check(s)
		}
		for _, s := range inv.Vowels {
			check(s)
		}
		if len(missing) > 0 {
			t.Errorf("%s：%d 个符号没推出类别/发音方式：%v", lang, len(missing), missing)
		}
	}
}
