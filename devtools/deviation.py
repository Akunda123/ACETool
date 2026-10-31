# 逐句对照：原文 vs 我改过的口播稿，算偏差（去掉标点后的字符差异）
import io
import re

def norm(s):
    s = re.sub(r'[，。！？、；：“”‘’（）《》…—\-\s]', '', s)
    return s

def lev(a, b):
    if a == b:
        return 0
    m, n = len(a), len(b)
    prev = list(range(n + 1))
    for i in range(1, m + 1):
        cur = [i] + [0] * n
        for j in range(1, n + 1):
            cost = 0 if a[i - 1] == b[j - 1] else 1
            cur[j] = min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + cost)
        prev = cur
    return prev[n]

orig = {}
for line in io.open('docs/pr_subtitles.txt', encoding='utf-8'):
    m = re.match(r'(\d+)\s+([\d.]+) -->\s+([\d.]+)\s+\([\d.]+s\)\s+(.*)', line.strip())
    if m:
        orig[int(m.group(1))] = m.group(4).strip()

said = {}
for line in io.open('docs/pr_voice_lines.tsv', encoding='utf-8').read().splitlines()[1:]:
    p = line.split('\t')
    if len(p) >= 5:
        said[int(p[0])] = p[4]

rows = []
for k in sorted(orig):
    a, b = norm(orig[k]), norm(said.get(k, ''))
    d = lev(a, b)
    ratio = d / max(len(a), 1)
    rows.append((k, ratio, len(a), orig[k], said.get(k, '')))

rows.sort(key=lambda r: -r[1])
for k, ratio, la, o, s in rows:
    flag = '重渲染' if ratio > 0.15 else '保留'
    print('%02d %5.1f%% %-6s 原:%s' % (k, ratio * 100, flag, o))
    if ratio > 0.15:
        print('            改:%s' % s)

print('---')
big = [r[0] for r in rows if r[1] > 0.15]
print('偏差 >15%% 的 %d 条：%s' % (len(big), big))
io.open('docs/voice/rerender_ids.txt', 'w', encoding='utf-8').write(','.join(str(x) for x in big))
