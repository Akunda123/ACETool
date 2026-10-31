# 选“连续说话最长”的段（不是平均有声比例），切 8–12 秒，出 3 个候选
import av
import numpy as np
import soundfile as sf

SRC = r'C:\Users\wujichao\Documents\acetool-app\docs\voice\_adjutant.aac'
OUTDIR = r'C:\Users\wujichao\Documents\acetool-app\docs\voice'
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

# 连续有声 run（允许 2 帧静音间隙）
runs = []
i = 0
while i < n:
    if not voice[i]:
        i += 1
        continue
    j = i
    gap = 0
    while j < n and (voice[j] or gap < 2):
        gap = 0 if voice[j] else gap + 1
        j += 1
    runs.append((j - i, i, j))
    i = j
runs.sort(reverse=True)
print('top runs:', [(r[0] * 0.1, r[1] * 0.1, r[2] * 0.1) for r in runs[:5]])

for k, (length, a, b) in enumerate(runs[:3]):
    seg = x[a * win:b * win].copy()
    nz = np.where(np.abs(seg) > 0.005)[0]
    if len(nz):
        seg = seg[max(0, nz[0] - int(0.05 * SR)): nz[-1] + int(0.05 * SR)]
    seg = seg[:int(12.0 * SR)]
    peak = max(np.abs(seg).max(), 1e-6)
    seg = seg * (10 ** (-6 / 20) / peak)
    f = int(0.02 * SR)
    seg[:f] *= np.linspace(0, 1, f, dtype=np.float32)
    seg[-f:] *= np.linspace(1, 0, f, dtype=np.float32)
    out = '%s\\ref_adjutant_%s.wav' % (OUTDIR, 'ABC'[k])
    sf.write(out, seg.astype(np.float32), SR, subtype='PCM_16')
    print('%s  %.1f-%.1fs  %.2fs' % (out, a * 0.1, b * 0.1, len(seg) / SR))
