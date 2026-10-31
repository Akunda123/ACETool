# 把整段 AAC 转成 WAV（不裁剪、不归一，便于自己挑参考）
import av
import numpy as np
import soundfile as sf

SRC = r'C:/Users/<user>\Documents\acetool-app\docs\voice\_adjutant.aac'
OUT = r'C:/Users/<user>\Documents\acetool-app\docs\voice\adjutant_full_24k.wav'
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
sf.write(OUT, x, SR, subtype='PCM_16')
print('saved %s  %.2fs  %d Hz  %.1f MB' % (OUT, len(x) / SR, SR, len(x) * 2 / 1024 / 1024))
