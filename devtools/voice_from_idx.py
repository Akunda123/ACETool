# 从 IndexTTS 自带示例里导出 4 个参考音色：24k 单声道、去静音、峰值 -6dBFS
import soundfile as sf, numpy as np, os, glob

SRC = r'F:\新建文件夹 (11)\Index-TTS-V26\Index-TTS-V26\dist\index-tts-windows-cu128-deepspeed\app\examples'
OUT = 'docs/voice'
PICK = {
    'voice_05': 'idx_voice05_male_24k.wav',   # 8.4s 男声·剑道旁白
    'voice_09': 'idx_voice09_female_24k.wav', # 10.2s 女声·轻快
    'voice_10': 'idx_voice10_male_24k.wav',   # 11.0s 男声·游戏解说
    'voice_11': 'idx_voice11_female_24k.wav', # 7.9s 女声·悲伤
}

def load(p):
    x, sr = sf.read(p, dtype='float32')
    if x.ndim > 1:
        x = x.mean(axis=1)
    return x, sr

def resample(x, sr, t=24000):
    if sr == t:
        return x
    n = int(round(len(x) * t / sr))
    idx = np.linspace(0, len(x) - 1, n)
    return np.interp(idx, np.arange(len(x)), x).astype(np.float32)

def trim(x, sr, thr_db=-45):
    th = 10 ** (thr_db / 20)
    nz = np.where(np.abs(x) > th)[0]
    if len(nz) == 0:
        return x
    a = max(0, nz[0] - int(0.03 * sr))
    b = min(len(x), nz[-1] + int(0.03 * sr))
    return x[a:b]

def save(path, x, sr):
    peak = max(np.abs(x).max(), 1e-6)
    x = x * (10 ** (-6 / 20) / peak)
    f = int(0.02 * sr)
    ramp = np.linspace(0, 1, f, dtype=np.float32)
    x[:f] *= ramp
    x[-f:] *= ramp[::-1]
    sf.write(path, x.astype(np.float32), sr, subtype='PCM_16')
    print('%s  %.2fs  %d Hz  peak -6.0 dBFS' % (os.path.basename(path), len(x) / sr, sr))

for src, dst in PICK.items():
    x, sr = load(os.path.join(SRC, src + '.wav'))
    x = trim(x, sr)
    x = resample(x, sr)
    save(os.path.join(OUT, dst), x, 24000)

print('--- 上一轮生成过的旁白片段（可直接拿来当参考）---')
for p in sorted(glob.glob(r'F:\新建文件夹 (11)\Index-TTS-V26\Index-TTS-V26\dist\index-tts-windows-cu128-deepspeed\app\outputs\dialogue\*\*旁白.wav'))[:8]:
    x, sr = load(p)
    print('%-70s %5.2fs %d Hz' % (p.split('dialogue')[1][:68], len(x) / sr, sr))
