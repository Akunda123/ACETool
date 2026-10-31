# 分析 IndexTTS 自带示例人声：时长、性别（中位 F0）、响度
import wave, struct, numpy as np, glob, os

def load(p):
    w = wave.open(p, 'rb')
    sr, ch, sw, n = w.getframerate(), w.getnchannels(), w.getsampwidth(), w.getnframes()
    d = w.readframes(n)
    w.close()
    a = np.frombuffer(d, dtype='<i2').astype(np.float32) / 32768.0
    if ch == 2:
        a = a.reshape(-1, 2).mean(axis=1)
    return sr, a

def f0_median(x, sr):
    # 分帧自相关，取有声帧的中位 F0
    fl = int(0.04 * sr); hop = int(0.02 * sr)
    f0s = []
    for i in range(0, len(x) - fl, hop):
        fr = x[i:i + fl]
        if np.sqrt((fr ** 2).mean()) < 0.01:
            continue
        fr = fr - fr.mean()
        ac = np.correlate(fr, fr, 'full')[len(fr) - 1:]
        lo, hi = int(sr / 400), int(sr / 60)
        if hi >= len(ac):
            continue
        k = lo + int(np.argmax(ac[lo:hi]))
        if k > 0 and ac[k] > 0.3 * ac[0]:
            f0s.append(sr / k)
    return float(np.median(f0s)) if f0s else 0.0

root = r'F:\新建文件夹 (11)\Index-TTS-V26\Index-TTS-V26\dist\index-tts-windows-cu128-deepspeed\app\examples'
for p in sorted(glob.glob(os.path.join(root, 'voice_*.wav'))):
    sr, x = load(p)
    f0 = f0_median(x, sr)
    peak = 20 * np.log10(max(np.abs(x).max(), 1e-6))
    print('%-13s %6.2fs %5d Hz  F0≈%5.1f Hz  peak %5.1f dBFS  %s' % (
        os.path.basename(p), len(x) / sr, sr, f0, peak,
        '男声' if 80 <= f0 < 165 else ('女声' if 165 <= f0 < 300 else '?')))
