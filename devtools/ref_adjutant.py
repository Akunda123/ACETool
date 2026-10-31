# 用 PyAV 解码 AAC（JiJiDown 自带的 ffmpeg 被精简过，没有 wav muxer），再挑 12 秒人声
import av
import numpy as np
import soundfile as sf

SRC = r'C:\Users\wujichao\Documents\acetool-app\docs\voice\_adjutant.aac'
OUT = r'C:\Users\wujichao\Documents\acetool-app\docs\voice\ref_adjutant_24k.wav'
SR = 24000

container = av.open(SRC)
stream = container.streams.audio[0]
resampler = av.AudioResampler(format='s16', layout='mono', rate=SR)
chunks = []
for frame in container.decode(stream):
    for f in resampler.resample(frame):
        chunks.append(f.to_ndarray().reshape(-1))
for f in resampler.resample(None):
    chunks.append(f.to_ndarray().reshape(-1))

x = np.concatenate(chunks).astype(np.float32) / 32768.0
print('decoded %.1fs @ %d Hz' % (len(x) / SR, SR))

win = int(0.1 * SR)
n = len(x) // win
rms = np.array([np.sqrt((x[i * win:(i + 1) * win] ** 2).mean()) for i in range(n)])
db = 20 * np.log10(np.maximum(rms, 1e-8))
thr = max(np.percentile(db, 60), -40)
voice = db > thr

best = (0.0, 0, 0)
for start in range(0, n):
    end = min(n, start + 120)
    if end - start < 80:
        break
    score = voice[start:end].mean()
    if score > best[0]:
        best = (score, start, end)
score, a, b = best
print('best window %.2f-%.2f (voice ratio %.2f)' % (a * 0.1, b * 0.1, score))

seg = x[a * win:b * win]
nz = np.where(np.abs(seg) > 0.005)[0]
if len(nz):
    seg = seg[max(0, nz[0] - int(0.05 * SR)): nz[-1] + int(0.05 * SR)]
seg = seg[:int(12.0 * SR)]
peak = max(np.abs(seg).max(), 1e-6)
seg = seg * (10 ** (-6 / 20) / peak)
f = int(0.02 * SR)
seg[:f] *= np.linspace(0, 1, f, dtype=np.float32)
seg[-f:] *= np.linspace(1, 0, f, dtype=np.float32)
sf.write(OUT, seg.astype(np.float32), SR, subtype='PCM_16')
print('saved %s  %.2fs  %d Hz  peak -6dBFS' % (OUT, len(seg) / SR, SR))
