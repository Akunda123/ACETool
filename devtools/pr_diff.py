# 对比：上次提取（09:26） vs 现在（16:56）的 PR 字幕
import io
import re


def parse(p):
    out = {}
    for line in io.open(p, encoding='utf-8'):
        m = re.match(r'(\d+)\s+([\d.]+) -->\s+([\d.]+)\s+\(([\d.]+)s\)\s+(.*)', line.strip())
        if m:
            out[int(m.group(1))] = (float(m.group(2)), float(m.group(3)), m.group(5).strip())
    return out


a = parse('docs/voice/_prev_subtitles.txt')
b = parse('docs/pr_subtitles.txt')

with io.open('docs/voice/pr_diff.md', 'w', encoding='utf-8') as f:
    f.write('# PR 字幕对比：上次提取 vs 现在\n\n')
    f.write('- 上次：`docs/voice/_prev_subtitles.txt`（09:26 提取，配音就是按它做的）\n')
    f.write('- 现在：`docs/pr_subtitles.txt`（16:56 重新提取，工程 16:55 保存）\n\n')
    f.write('条数：上次 %d，现在 %d\n\n' % (len(a), len(b)))
    diff = [k for k in b if k in a and (a[k][2] != b[k][2] or abs(a[k][1] - b[k][1]) > 0.01)]
    if not diff:
        f.write('**文字与时间码完全一致，没有变化。**\n')
    else:
        f.write('| # | 上次 | 现在 |\n|---|---|---|\n')
        for k in diff:
            f.write('| %d | %s | %s |\n' % (k, a[k][2], b[k][2]))
    print('diff', len(diff))
