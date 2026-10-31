# 逐条对比：原文 / 我改过的口播 / 偏差 / 当前音频
import io
import re

def norm(s):
    return re.sub(r'[，。！？、；：“”‘’（）《》…—\-/\s]', '', s)

def lev(a, b):
    if a == b:
        return 0
    n = len(b)
    prev = list(range(n + 1))
    for i in range(1, len(a) + 1):
        cur = [i] + [0] * n
        for j in range(1, n + 1):
            cur[j] = min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + (a[i - 1] != b[j - 1]))
        prev = cur
    return prev[n]

orig, said = {}, {}
for line in io.open('docs/pr_subtitles.txt', encoding='utf-8'):
    m = re.match(r'(\d+)\s+[\d.]+ -->\s+[\d.]+\s+\([\d.]+s\)\s+(.*)', line.strip())
    if m:
        orig[int(m.group(1))] = m.group(2).strip()
for line in io.open('docs/pr_voice_lines.tsv', encoding='utf-8').read().splitlines()[1:]:
    p = line.split('\t')
    if len(p) >= 5:
        said[int(p[0])] = p[4]

big = set(int(x) for x in io.open('docs/voice/rerender_ids.txt').read().split(','))
with io.open('docs/voice/compare_list.md', 'w', encoding='utf-8') as f:
    f.write('| # | 原文（你的字幕） | 我之前的口播 | 偏差 | 处理 |\n|---|---|---|---|---|\n')
    for k in sorted(orig):
        a, b = norm(orig[k]), norm(said.get(k, ''))
        ratio = lev(a, b) / max(len(a), 1)
        act = '**重渲染（原文）**' if k in big else '保留'
        f.write('| %d | %s | %s | %.0f%% | %s |\n' % (k, orig[k], said.get(k, ''), ratio * 100, act))
print('written')
