# 对比情绪：F0 起伏（半音标准差）+ 音高范围 + 时长
import glob
import os

import numpy as np
import soundfile as sf


def f0_track(x, sr):
    fl, hop = int(0.04 * sr), int(0.01 * sr)
    out = []
    for i in range(0, len(x) - fl, hop):
        fr = x[i:i + fl]
        if np.sqrt((fr ** 2).mean()) < 0.008:
            continue
        fr = fr - fr.mean()
        ac = np.correlate(fr, fr, 'full')[len(fr) - 1:]
        lo, hi = int(sr / 400), int(sr / 60)
        if hi >= len(ac):
            continue
        k = lo + int(np.argmax(ac[lo:hi]))
        if k > 0 and ac[k] > 0.3 * ac[0]:
            out.append(sr / k)
    return np.array(out)


def report(path):
    x, sr = sf.read(path, dtype='float32')
    if x.ndim > 1:
        x = x.mean(axis=1)
    f0 = f0_track(x, sr)
    if len(f0) < 10:
        return None
    st = 12 * np.log2(f0 / 55.0)
    return dict(dur=len(x) / sr, median=float(np.median(f0)),
                std_st=float(np.std(st)), range_st=float(np.percentile(st, 95) - np.percentile(st, 5)))

for label, pattern in [
    ('emo 测试（你的录音 + 情绪文本）', r'docs\voice\emo_test\*.wav'),
    ('旧·副官（无情绪）', r'docs\voice\lines\*.wav'),
]:
    rows = []
    for p in sorted(glob.glob(pattern))[:3]:
        r = report(p)
        if r:
            rows.append(r)
    if rows:
        print('%-28s 时长 %.2fs | F0 中位 %.0f Hz | 起伏 %.2f 半音 | 音域 %.2f 半音' % (
            label, np.mean([r['dur'] for r in rows]), np.mean([r['median'] for r in rows]),
            np.mean([r['std_st'] for r in rows]), np.mean([r['range_st'] for r in rows])))
