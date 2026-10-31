# 合集里都是 ~3 秒的短语音 ⇒ 把前几段拼成 ~11 秒参考（同一说话人、留 120ms 间隙）
import av
import numpy as np
import soundfile as sf

SRC = r'C:\Users\wujichao\Documents\acetool-app\docs\voice\_adjutant.aac'
OUT = r'C:\Users\wujichao\Documents\acetool-app\docs\voice\ref_adjutant_11s.wav'
SR = 24000

container = av.open(SRC)
rs = av.AudioResampler(format='s16', layout='mono', rate=SR)
chunks = []
for fr in container.decode(container.streams.audio[0]):
    for f in rs.resample(fr):
        chunks.append(f.to_ndarray().reshape(-1))
for f in rs.resample(None):
    chunks.append(f.to_ndarray().reshape(-1))
x = np.concatenate(chunks).astype(np.float32) / 32768.0

win = int(0.1 * SR)
n = len(x) // win
rms = np.array([np.sqrt((x[i * win:(i + 1) * win] ** 2).mean()) for i in range(n)])
db = 20 * np.log10(np.maximum(rms, 1e-8))
thr = max(np.percentile(db, 55), -42)
voice = db > thr

runs = []
i = 0
while i < n:
    if not voice[i]:
        i += 1
        continue
    j, gap = i, 0
    while j < n and (voice[j] or gap < 2):
        gap = 0 if voice[j] else gap + 1
        j += 1
    runs.append((j - i, i, j))
    i = j
runs.sort(reverse=True)

def take(a, b):
    seg = x[a * win:b * win].copy()
    nz = np.where(np.abs(seg) > 0.005)[0]
    if len(nz):
        seg = seg[max(0, nz[0] - int(0.02 * SR)): nz[-1] + int(0.02 * SR)]
    f = int(0.01 * SR)
    if len(seg) > f:
        seg[:f] *= np.linspace(0, 1, f, dtype=np.float32)
        seg[-f:] *= np.linspace(1, 0, f, dtype=np.float32)
    return seg

pieces = []
total = 0.0
for length, a, b in runs:
    if length < 20:      # 至少 2 秒
        continue
    seg = take(a, b)
    gap = np.zeros(int(0.12 * SR), dtype=np.float32)
    pieces.append(seg)
    pieces.append(gap)
    total += len(seg) / SR + 0.12
    if total >= 11.0:
        break

out = np.concatenate(pieces)[:int(11.5 * SR)]
peak = max(np.abs(out).max(), 1e-6)
out = out * (10 ** (-6 / 20) / peak)
sf.write(OUT, out.astype(np.float32), SR, subtype='PCM_16')
print('saved %s  %.2fs  %d Hz' % (OUT, len(out) / SR, SR))
