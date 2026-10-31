# 把 46 条配音按顺序拼成一条试听带（间隙 0.3s），方便一次听完
import glob
import os

import numpy as np
import soundfile as sf

LINES = r'C:/Users/<user>\Documents\acetool-app\docs\voice\lines'
OUT = r'C:/Users/<user>\Documents\acetool-app\docs\voice\adjutant_all_preview.wav'
SR = 24000

pieces = []
for i in range(1, 47):
    p = os.path.join(LINES, '%02d.wav' % i)
    if not os.path.exists(p):
        print('missing', p)
        continue
    x, sr = sf.read(p, dtype='float32')
    if sr != SR:
        idx = np.linspace(0, len(x) - 1, int(len(x) * SR / sr))
        x = np.interp(idx, np.arange(len(x)), x).astype(np.float32)
    if x.ndim > 1:
        x = x.mean(axis=1)
    peak = max(np.abs(x).max(), 1e-6)
    x = x * (10 ** (-6 / 20) / peak)
    pieces.append(x)
    pieces.append(np.zeros(int(0.3 * SR), dtype=np.float32))

out = np.concatenate(pieces)
sf.write(OUT, out.astype(np.float32), SR, subtype='PCM_16')
print('saved %s  %.1fs  %d Hz' % (OUT, len(out) / SR, SR))
