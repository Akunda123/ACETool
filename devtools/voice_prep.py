# 鎶?OneCore 鐨?16k 鍚堟垚璇煶鏁寸悊鎴?IndexTTS 鍙傝€冿細鍘婚潤闊?/ 閲嶉噰鏍?24k / 宄板€?-6dBFS / 闄愰暱
import wave, struct, math, sys

def read(path):
    w = wave.open(path, 'rb')
    sr, ch, sw, n = w.getframerate(), w.getnchannels(), w.getsampwidth(), w.getnframes()
    data = w.readframes(n)
    w.close()
    assert sw == 2 and ch == 1, (sw, ch)
    return sr, list(struct.unpack('<%dh' % (len(data) // 2), data))

def resample(vals, sr, target=24000):
    if sr == target:
        return vals, sr
    n_out = int(round(len(vals) * target / sr))
    out = []
    for i in range(n_out):
        x = i * sr / target
        i0 = int(x); frac = x - i0
        a = vals[min(i0, len(vals) - 1)]
        b = vals[min(i0 + 1, len(vals) - 1)]
        out.append(int(a + (b - a) * frac))
    return out, target

def process(src, dst, max_sec=15.0, target_db=-6.0):
    sr, vals = read(src)
    th = int(32768 * 10 ** (-45 / 20))
    i = 0
    while i < len(vals) and abs(vals[i]) < th:
        i += 1
    j = len(vals) - 1
    while j > i and abs(vals[j]) < th:
        j -= 1
    i = max(0, i - sr // 20); j = min(len(vals) - 1, j + sr // 20)
    vals = vals[i:j + 1]
    if len(vals) > max_sec * sr:
        vals = vals[:int(max_sec * sr)]
    vals, sr = resample(vals, sr)
    peak = max(1, max(abs(v) for v in vals))
    g = 10 ** (target_db / 20) * 32768 / peak
    vals = [max(-32768, min(32767, int(v * g))) for v in vals]
    f = int(sr * 0.02)
    for k in range(min(f, len(vals))):
        vals[k] = int(vals[k] * k / f)
        vals[-1 - k] = int(vals[-1 - k] * k / f)
    o = wave.open(dst, 'wb')
    o.setnchannels(1); o.setsampwidth(2); o.setframerate(sr)
    o.writeframes(struct.pack('<%dh' % len(vals), *vals))
    o.close()
    print('%s  %.2fs  %d Hz  peak %.1f dBFS' % (dst, len(vals) / sr, sr,
          20 * math.log10(max(abs(v) for v in vals) / 32768)))

for name in ['kangkang', 'yaoyao']:
    process('docs/voice/natural_%s.wav' % name, 'docs/voice/ref_%s_24k.wav' % name)

