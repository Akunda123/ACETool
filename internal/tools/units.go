package tools

import (
	"math"
	"strconv"
	"strings"

	"acetool/internal/cbor"
)

// 单位口径（用户 2026-10-14 裁定）：**对外一律用「拍」，内部单位不进用户可见输出**。
//
//	1 拍 ≡ 一个四分音符（恒等定义，与拍号无关）
//	ACE 的 PPQ = **每四分音符 480 tick**（标准 PPQ 栅格，不是常量"每秒 tick"）
//	⇒ 拍 = tick / 480；`tick/秒 = 480×BPM/60` **随速度漂**，跨工程别抄（曾记成 1080 tick/s，那属于 135 BPM 的工程）。
//
// ⚠️ 这条只管**印给人看的东西**：内部字段名/结构（`tick` / `absTick` / `startTick`…）不动，
// 免得把协议和测试一起掀了；新写的报告/标签/报错一律走这里的换算。
const aceTicksPerBeat = 480

func ticksToBeats(tick int64) float64 { return float64(tick) / aceTicksPerBeat }

func beatsToTicks(beat float64) int64 { return int64(math.Round(beat * aceTicksPerBeat)) }

// beatLabel 把 tick 印成「拍」（最多两位小数，去掉尾随 0）：480 → `1`、720 → `1.5`、240 → `0.5`。
func beatLabel(tick int64) string {
	return trimFloat(ticksToBeats(tick))
}

// trimFloat 数字转字符串（最多两位小数、去尾随 0）。
func trimFloat(v float64) string {
	s := strconv.FormatFloat(math.Round(v*100)/100, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// clipNameOf 取片段名给人看。
//
// ⚠️ 真工程里**片段可能没有 name（或它是空串）** —— 实测 `lala-resaved/未命名1.acep` 就是空的，
// 而 CLI 的 `clipName` 是它自己拿歌词拼的（"浮沉人海繁 a 太多虚幻的"）⇒ 文件侧读不到那个名字。
// 与其印出「片段「」」，这里回一个不含糊的占位。
func clipNameOf(pat *cbor.Node) string {
	if pat == nil {
		return "（无名片段）"
	}
	if s := strings.TrimSpace(nodeText(pat.Get("name"))); s != "" {
		return s
	}
	return "（无名片段）"
}
