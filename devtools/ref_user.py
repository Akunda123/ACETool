# 用户的 FL Studio 录音 → IndexTTS 参考：24k 单声道、去静音、-6 dBFS
import numpy as np
import soundfile as sf

SRC = r'C:/Users/<user>\Documents\Image-Line\FL Studio\Projects\123123132.wav'
OUT = r'C:/Users/<user>\Documents\acetool-app\docs\voice\ref_user_24k.wav'
SR = 24000

x, sr = sf.read(SRC, dtype='float32')
if x.ndim > 1:
    x = x.mean(axis=1)
if sr != SR:
    idx = np.linspace(0, len(x) - 1, int(len(x) * SR / sr))
    x = np.interp(idx, np.arange(len(x)), x).astype(np.float32)

nz = np.where(np.abs(x) > 0.005)[0]
if len(nz):
    x = x[max(0, nz[0] - int(0.05 * SR)): nz[-1] + int(0.05 * SR)]

peak = max(np.abs(x).max(), 1e-6)
x = x * (10 ** (-6 / 20) / peak)
f = int(0.02 * SR)
x[:f] *= np.linspace(0, 1, f, dtype=np.float32)
x[-f:] *= np.linspace(1, 0, f, dtype=np.float32)
sf.write(OUT, x, SR, subtype='PCM_16')
print('saved %s  %.2fs  %d Hz  peak -6dBFS' % (OUT, len(x) / SR, SR))
