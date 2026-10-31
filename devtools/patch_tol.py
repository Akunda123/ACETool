# 给「按起点配对」加阈值（默认 30 tick），并接到 pitch.within / xcopy.within
import io

# ① xcopy.go：matchByOnset 支持阈值
p = 'internal/tools/xcopy.go'
s = io.open(p, encoding='utf-8').read()
old_sig = 'func matchByOnset(src, dst []xcopyNote, srcBegin, dstBegin int64) (pairs [][2]int, srcOnly, dstOnly int) {'
new_sig = '''func matchByOnset(src, dst []xcopyNote, srcBegin, dstBegin int64) (pairs [][2]int, srcOnly, dstOnly int) {
	return matchByOnsetTol(src, dst, srcBegin, dstBegin, 0)
}

// matchByOnsetTol 同上，但允许起点差 ≤ tol tick 也算同一个音（两段之间常有几 tick 的量化差）。
func matchByOnsetTol(src, dst []xcopyNote, srcBegin, dstBegin, tol int64) (pairs [][2]int, srcOnly, dstOnly int) {'''
assert old_sig in s
s = s.replace(old_sig, new_sig, 1)

old_loop = '''	i, j := 0, 0
	for i < len(si) && j < len(di) {
		on1 := srcBegin + si[i].Pos
		on2 := dstBegin + di[j].Pos
		switch {
		case on1 > on2:
			j++
		case on1 < on2:
			i++
		default:
			pairs = append(pairs, [2]int{srcAt[si[i].NoteUUID], dstAt[di[j].NoteUUID]})
			i++
			j++
		}
	}'''
new_loop = '''	i, j := 0, 0
	for i < len(si) && j < len(di) {
		on1 := srcBegin + si[i].Pos
		on2 := dstBegin + di[j].Pos
		diff := on1 - on2
		if diff < 0 {
			diff = -diff
		}
		switch {
		case diff <= tol:
			pairs = append(pairs, [2]int{srcAt[si[i].NoteUUID], dstAt[di[j].NoteUUID]})
			i++
			j++
		case on1 < on2:
			i++
		default:
			j++
		}
	}'''
assert old_loop in s
s = s.replace(old_loop, new_loop, 1)
io.open(p, 'w', encoding='utf-8').write(s)
print('xcopy.go ok')

# ② xcopywithin.go：读参数并传下去
p = 'internal/tools/xcopywithin.go'
s = io.open(p, encoding='utf-8').read()
s = s.replace('pairs, so, do := matchByOnset(srcNotes, dstNotes, -srcA, -dstA)',
              'pairs, so, do := matchByOnsetTol(srcNotes, dstNotes, -srcA, -dstA, tolTicksOf(vals))', 1)
io.open(p, 'w', encoding='utf-8').write(s)
print('xcopywithin ok:', 'tolTicksOf' in s)

# ③ pitchwithin.go：阈值参数化（默认 30）
p = 'internal/tools/pitchwithin.go'
s = io.open(p, encoding='utf-8').read()
s = s.replace('	const tolTicks = 240 // 半个四分音符', '	tolTicks := tolTicksOf(vals)', 1)
s = s.replace('func planCopyVibrato(src, dst *cbor.Node, sLo, sHi, delta int64) ([]cbor.Edit, string, error) {',
              'func planCopyVibrato(src, dst *cbor.Node, sLo, sHi, delta int64, tolTicks int64) ([]cbor.Edit, string, error) {', 1)
s = s.replace('entries, msg, err := planCopyVibrato(srcPat, dstPat, sLo, sHi, delta)',
              'entries, msg, err := planCopyVibrato(srcPat, dstPat, sLo, sHi, delta, tolTicksOf(vals))', 1)
# 参数化后原来的 const 名字会冲突：把 planCopyVibrato 内部引用改成形参（上面已同名的局部变量 tolTicks 不存在了）
io.open(p, 'w', encoding='utf-8').write(s)
print('pitchwithin ok')
