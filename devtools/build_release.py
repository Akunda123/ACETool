# 打包最终成品 v1.0.0：对齐后的 SRT + 整轨旁白 + 逐条音频 + 说明
import io
import json
import os
import shutil
import re

import numpy as np
import soundfile as sf

ROOT = r'C:\Users\wujichao\Documents\acetool-app'
VER = 'v1.0.0'
OUT = os.path.join(ROOT, 'docs', 'release', VER)
LINES = os.path.join(ROOT, 'docs', 'voice', 'lines')
REPORT = os.path.join(ROOT, 'docs', 'voice', 'lines_report.json')
SR = 24000

GAP = 0.20        # 条与条之间
PARA_GAP = 0.60   # 每 10 条当一段

os.makedirs(os.path.join(OUT, 'voice'), exist_ok=True)

rep = {r['idx']: r for r in json.load(io.open(REPORT, encoding='utf-8'))}
texts = {}
for line in io.open(os.path.join(ROOT, 'docs', 'pr_subtitles.txt'), encoding='utf-8'):
    m = re.match(r'(\d+)\s+[\d.]+ -->\s+[\d.]+\s+\(([\d.]+)s\)\s+(.*)', line.strip())
    if m:
        texts[int(m.group(1))] = m.group(3).strip()

def srt_time(t):
    h = int(t // 3600); m = int(t % 3600 // 60); s = t % 60
    return '%02d:%02d:%06.3f' % (h, m, s)

def load(i):
    x, sr = sf.read(os.path.join(LINES, '%02d.wav' % i), dtype='float32')
    if sr != SR:
        idx = np.linspace(0, len(x) - 1, int(len(x) * SR / sr))
        x = np.interp(idx, np.arange(len(x)), x).astype(np.float32)
    if x.ndim > 1:
        x = x.mean(axis=1)
    return x

cursor = 0.0
srt = []
audio_parts = []
rows = []
for i in range(1, 47):
    x = load(i)
    dur = len(x) / SR
    start = cursor
    end = start + dur
    srt.append((i, start, end, texts[i]))
    audio_parts.append(x)
    gap = PARA_GAP if i % 10 == 0 else GAP
    audio_parts.append(np.zeros(int(gap * SR), dtype=np.float32))
    rows.append((i, start, end, dur, texts[i]))
    cursor = end + gap

with io.open(os.path.join(OUT, 'acetool_demo_%s.srt' % VER), 'w', encoding='utf-8') as f:
    for i, a, b, t in srt:
        f.write('%d\n%s --> %s\n%s\n\n' % (i, srt_time(a).replace('.', ','), srt_time(b).replace('.', ','), t))

full = np.concatenate(audio_parts)
peak = max(np.abs(full).max(), 1e-6)
full = full * (10 ** (-1 / 20) / peak)   # 整轨留 1 dB 余量
sf.write(os.path.join(OUT, 'voice', 'narration_full_%s.wav' % VER), full, SR, subtype='PCM_16')

for i in range(1, 47):
    shutil.copy2(os.path.join(LINES, '%02d.wav' % i), os.path.join(OUT, 'voice', '%02d.wav' % i))

with io.open(os.path.join(OUT, 'timeline_%s.tsv' % VER), 'w', encoding='utf-8') as t:
    t.write('序号\t开始\t结束\t时长\t文本\n')
    for i, a, b, dur, txt in rows:
        t.write('%d\t%.3f\t%.3f\t%.3f\t%s\n' % (i, a, b, dur, txt))

with io.open(os.path.join(OUT, 'README.md'), 'w', encoding='utf-8') as m:
    m.write('# ACETool 演示视频 · 配音与字幕成品 %s\n\n' % VER)
    m.write('- 条数：46\n- 总长：%.2f 秒（%.2f 分钟）\n' % (cursor, cursor / 60))
    m.write('- 音色：`ref_user_24k.wav`（用户录音）· 情绪 alpha 0.6\n')
    m.write('- 稿子：PR 工程原文逐字（仅 `-` 读作“减号”）\n')
    m.write('- 规则：条间 0.20s，每 10 条分段 0.60s\n\n')
    m.write('## 文件\n\n')
    m.write('| 文件 | 用途 |\n|---|---|\n')
    m.write('| `acetool_demo_%s.srt` | 对齐后的字幕，直接导入 PR |\n' % VER)
    m.write('| `voice/narration_full_%s.wav` | 整轨旁白（与 SRT 时间轴一致） |\n' % VER)
    m.write('| `voice/01…46.wav` | 逐条旁白（要替换某条时用） |\n')
    m.write('| `timeline_%s.tsv` | 时间码表（Excel 可看） |\n\n' % VER)
    m.write('## 导入 PR\n\n')
    m.write('1. 新建一条音频轨，放 `narration_full_%s.wav`（起点 00:00）；\n' % VER)
    m.write('2. 文件 → 导入 → 字幕，选 `acetool_demo_%s.srt`，匹配到该音频轨；\n' % VER)
    m.write('3. 若要单条微调，用 `voice/NN.wav` 替换对应段。\n')

print('built', OUT, 'total %.2fs' % cursor)
