# 用最新的 PR 字幕时间码 + 配音实测时长，重算对照表
import io
import json
import re

REPORT = r'C:\Users\wujichao\Documents\acetool-app\docs\voice\lines_report.json'
SUBS = r'C:\Users\wujichao\Documents\acetool-app\docs\pr_subtitles.txt'
OUT_MD = r'C:\Users\wujichao\Documents\acetool-app\docs\pr_voice_report.md'
OUT_TSV = r'C:\Users\wujichao\Documents\acetool-app\docs\pr_voice_report.tsv'

time = {}
for line in io.open(SUBS, encoding='utf-8'):
    m = re.match(r'(\d+)\s+([\d.]+) -->\s+([\d.]+)\s+\(([\d.]+)s\)\s+(.*)', line.strip())
    if m:
        time[int(m.group(1))] = (float(m.group(2)), float(m.group(3)), m.group(5).strip())

rep = {r['idx']: r for r in json.load(io.open(REPORT, encoding='utf-8'))}

with io.open(OUT_TSV, 'w', encoding='utf-8') as t, io.open(OUT_MD, 'w', encoding='utf-8') as m:
    t.write('序号\t字幕开始\t字幕结束\t槽位\t配音\t差值\n')
    m.write('# 配音 vs 字幕时间码（按 PR 最新工程）\n\n')
    m.write('> 时间码取自 `acetool.prproj` 最近一次保存（`docs/pr_subtitles.txt`）；配音时长取自 `lines_report.json`。\n\n')
    m.write('| # | 字幕时间 | 槽位 | 配音 | 差值 |\n|---|---|---|---|---|\n')
    total = 0.0
    for k in sorted(rep):
        a, b, _ = time[k]
        audio = rep[k]['audio']
        slot = b - a
        total += audio
        t.write('%d\t%.2f\t%.2f\t%.2f\t%.2f\t%+.2f\n' % (k, a, b, slot, audio, audio - slot))
        m.write('| %d | %.2f–%.2f | %.2fs | **%.2fs** | %+.2fs |\n' % (k, a, b, slot, audio, audio - slot))
    m.write('\n共 %d 条，配音总长 %.2fs（%.2f 分钟）。\n' % (len(rep), total, total / 60))
print('ok', len(rep))
