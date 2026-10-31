# 汇总配音结果：音频时长 vs 字幕槽位，给出时间轴调整建议（可直接照着改 PR）
import io, json, os, subprocess, sys

REPORT = r'C:/Users/<user>\Documents\acetool-app\docs\voice\lines_report.json'
OUT_MD = r'C:/Users/<user>\Documents\acetool-app\docs\pr_timing_plan.md'
OUT_TSV = r'C:/Users/<user>\Documents\acetool-app\docs\pr_timing_plan.tsv'

rep = json.load(io.open(REPORT, encoding='utf-8'))
rep.sort(key=lambda r: r['idx'])

rows = []
for r in rep:
    slot = r['slot']
    audio = r['audio']
    diff = audio - slot
    rows.append((r['idx'], r['start'], r['end'], slot, audio, diff, r['text']))

with io.open(OUT_TSV, 'w', encoding='utf-8') as t, io.open(OUT_MD, 'w', encoding='utf-8') as m:
    t.write('序号\t原开始\t原结束\t槽位\t配音\t差值\t处理\n')
    m.write('# 配音 vs 字幕时间轴（逐条）\n\n')
    m.write('| # | 原时间 | 槽位 | 配音 | 差值 | 建议 |\n|---|---|---|---|---|---|\n')
    long_n = short_n = ok_n = 0
    for idx, a, b, slot, audio, diff, text in rows:
        if diff > 1.0:
            act = '延长到 %.2fs（+%.2fs）' % (audio, diff); long_n += 1
        elif diff < -1.0:
            act = '压缩到 %.2fs（%.2fs）或后留白' % (audio, diff); short_n += 1
        else:
            act = '基本吻合'; ok_n += 1
        t.write('%d\t%.2f\t%.2f\t%.2f\t%.2f\t%+.2f\t%s\n' % (idx, a, b, slot, audio, diff, act))
        m.write('| %d | %.2f–%.2f | %.2fs | %.2fs | %+.2fs | %s |\n' % (idx, a, b, slot, audio, diff, act))
    m.write('\n合计：%d 条；需要延长 %d 条、需要压缩/留白 %d 条、基本吻合 %d 条。\n' % (len(rows), long_n, short_n, ok_n))
    total_audio = sum(r[4] for r in rows)
    m.write('\n配音总长 %.2fs（%.2f 分钟）。\n' % (total_audio, total_audio / 60))
print('ok', len(rows))
