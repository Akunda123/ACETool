# 原样口播稿：完全用你 PR 里的原文，只把念不出来的符号换成汉字
import io
import re

rows = []
for line in io.open('docs/pr_subtitles.txt', encoding='utf-8'):
    m = re.match(r'(\d+)\s+([\d.]+) -->\s+([\d.]+)\s+\([\d.]+s\)\s+(.*)', line.strip())
    if m:
        rows.append((int(m.group(1)), float(m.group(2)), float(m.group(3)), m.group(4).strip()))

with io.open('docs/pr_voice_lines_verbatim.tsv', 'w', encoding='utf-8') as f:
    f.write('序号\t开始\t结束\t时长\t口播文本\n')
    for i, a, b, t in rows:
        s = t
        # 只做“读不出来”的替换，其余一个字不改
        s = s.replace('‘-’', '减号').replace('“-”', '减号').replace("'-'", '减号')
        if '-' in s:
            s = s.replace('-', '减号')
        f.write('%d\t%.2f\t%.2f\t%.2f\t%s\n' % (i, a, b, b - a, s))

print('rows', len(rows))
print('示例 08:', rows[7][3])
