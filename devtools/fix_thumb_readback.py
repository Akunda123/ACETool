# 两处改进：① ACE 没开时页面提示友好 ② 写完后用 CLI 回读锚点数，作为「有没有生效」的证据
import io

# ① pitch.html：加载失败时区分「ACE 没开」
p = 'internal/web/assets/pitch.html'
s = io.open(p, encoding='utf-8').read()
if 'ACE 没开' not in s:
    s = s.replace("$('stat').textContent = '读取失败：' + e.message;",
"""$('stat').textContent = '读取失败：' + e.message;
      if (/bridge not reachable|not running|exit 3/i.test(String(e.message))) {
        $('stat').textContent = 'ACE 没开（或桥没起来）—— 先打开 ACE Studio，再点「刷新」';
      }""")
    io.open(p, 'w', encoding='utf-8').write(s)
print('page hint:', 'ACE 没开' in s)

# ② pitchwithin.go：apply 之后回读锚点，写进报告
p = 'internal/tools/pitchwithin.go'
s = io.open(p, encoding='utf-8').read()

# plan 记下 clipUuid 与目标区间
if 'clipUUID string' not in s:
    s = s.replace('\ttrimAnchors bool', '\tclipUUID    string\n\ttrimAnchors bool', 1)
s = s.replace('plan := &pitchPlan{rep: rep, cont: cont, raw: cont.RawBytes(), dstPat: pat}',
              'plan := &pitchPlan{rep: rep, cont: cont, raw: cont.RawBytes(), dstPat: pat, clipUUID: clipUUID}', 1)

# apply 里加回读
if 'readbackPitchAnchors' not in s:
    s = s.replace('func pitchWithinApply(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {',
'''// readbackPitchAnchors 用 CLI 读回目标区间里的锚点（ACE 眼里的样子），
// 让报告能回答「这次到底有没有生效」，而不是只看文件字节。
func readbackPitchAnchors(ctx context.Context, rd ReadFunc, clipUUID string, lo, hi int64) (int, string) {
	if clipUUID == "" {
		return 0, ""
	}
	var out vocalLayerRead
	err := readJSON(ctx, rd, &out, "vocalpitch", "read", "--clip-uuid", clipUUID,
		"--level", "user", "--range-begin", fmt.Sprint(lo), "--range-end", fmt.Sprint(hi), "--json")
	if err != nil {
		return 0, fmt.Sprintf("回读失败（%v）—— 以文件自检为准", err)
	}
	for _, l := range out.Layers {
		if l.Layer == "user" {
			n := 0
			for _, p := range l.Points {
				if len(p) == 2 && p[0] != nil && *p[0] >= float64(lo) && *p[0] < float64(hi) && p[1] != nil {
					n++
				}
			}
			return n, fmt.Sprintf("ACE 回读：目标区间里有 %d 个锚点", n)
		}
	}
	return 0, "ACE 回读：user 层没有数据"
}

func pitchWithinApply(ctx context.Context, rd ReadFunc, vals map[string]string) (any, error) {''', 1)

    s = s.replace('''	plan, err := buildPitchPlan(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	return applyPitchPlan(plan)''',
'''	plan, err := buildPitchPlan(ctx, rd, vals)
	if err != nil {
		return nil, err
	}
	got, err := applyPitchPlan(plan)
	if err != nil {
		return nil, err
	}
	if rep, ok := got.(*pitchCopyReport); ok && rep != nil && !rep.DryRun {
		if n, msg := readbackPitchAnchors(ctx, rd, plan.clipUUID, plan.trimLo, plan.trimHi); msg != "" {
			rep.Hint += "\n" + msg
			if plan.trimAnchors && n == 0 {
				rep.Hint += "（写入后 ACE 看不到锚点 —— 让 ACE 重新打开工程再读）"
			}
		}
	}
	return got, nil''', 1)
    io.open(p, 'w', encoding='utf-8').write(s)
print('readback:', 'readbackPitchAnchors' in s)
