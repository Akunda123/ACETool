"""逐条配音：读 docs/pr_voice_lines.tsv 的口播文本，用用户的 播音 预设合成一条一段。

在 IndexTTS 的 app 目录下用 C:\\ix\\runtime\\python.exe 运行（C:\\ix 是 ASCII 软链，绕开句柄编码问题）。
用法：python synth_lines.py [起始序号] [结束序号]
"""
import io
import json
import os
import sys
import time
import traceback

APP = os.path.dirname(os.path.abspath(__file__))
os.chdir(APP)
sys.path.insert(0, APP)
sys.path.insert(0, r'C:\Users\wujichao\AppData\Local\Temp\ixenv')  # wetext 的 ASCII 副本

DIST = os.path.dirname(APP)
MODEL_DIR = os.environ.get('IX_MODEL_DIR') or os.path.join(DIST, 'data', 'checkpoints')
os.environ.setdefault('HF_HOME', os.path.join(DIST, 'data', 'hf_cache'))
os.environ.setdefault('HF_HUB_CACHE', os.environ['HF_HOME'])
os.environ.setdefault('NUMBA_CACHE_DIR', os.path.join(DIST, 'data', '.numba_cache'))

PROMPT = os.environ.get('IX_PROMPT') or os.path.join(APP, 'prompt_boyin_12s.wav')
EMO_MODE = os.environ.get('IX_EMO_MODE', '0')          # 0 跟音色参考 / 1 情感音频 / 2 情感向量 / 3 情感文本
EMO_TEXT = os.environ.get('IX_EMO_TEXT', '')           # mode 3 用
EMO_VEC = os.environ.get('IX_EMO_VEC', '')             # mode 2 用：8 个逗号分隔的数
EMO_AUDIO = os.environ.get('IX_EMO_AUDIO', '')         # mode 1 用
EMO_ALPHA = float(os.environ.get('IX_EMO_ALPHA', '0.65'))
TSV = os.environ.get('IX_TSV') or r'C:\Users\wujichao\Documents\acetool-app\docs\pr_voice_lines.tsv'
OUT_DIR = os.environ.get('IX_OUT') or r'C:\Users\wujichao\Documents\acetool-app\docs\voice\lines'
REPORT = r'C:\Users\wujichao\Documents\acetool-app\docs\voice\lines_report.json'

ONLY = {int(x) for x in os.environ.get('IX_ONLY', '').split(',') if x.strip()}
a = int(sys.argv[1]) if len(sys.argv) > 1 else 1
b = int(sys.argv[2]) if len(sys.argv) > 2 else 999

rows = []
for line in io.open(TSV, encoding='utf-8').read().splitlines()[1:]:
    parts = line.split('\t')
    if len(parts) >= 5:
        rows.append((int(parts[0]), float(parts[1]), float(parts[2]), parts[4]))

os.makedirs(OUT_DIR, exist_ok=True)
report = []
if os.path.exists(REPORT):
    try:
        report = json.load(io.open(REPORT, encoding='utf-8'))
    except Exception:
        report = []
done = set() if os.environ.get('IX_FORCE') == '1' else {r['idx'] for r in report}

from indextts.infer_v2 import IndexTTS2

t0 = time.time()
print('加载模型…', flush=True)
tts = IndexTTS2(model_dir=MODEL_DIR, cfg_path=os.path.join(MODEL_DIR, 'config.yaml'),
                use_fp16=True, use_deepspeed=False, use_cuda_kernel=False)
print('模型就绪 %.1fs' % (time.time() - t0), flush=True)

for idx, start, end, text in rows:
    if idx < a or idx > b or idx in done:
        continue
    if ONLY and idx not in ONLY:
        continue
    out = os.path.join(OUT_DIR, '%02d.wav' % idx)
    t1 = time.time()
    try:
        emo_kw = {}
        if EMO_MODE == '1' and EMO_AUDIO:
            emo_kw = {'emo_audio_prompt': EMO_AUDIO, 'emo_alpha': EMO_ALPHA}
        elif EMO_MODE == '2' and EMO_VEC:
            vec = [float(x) for x in EMO_VEC.split(',')]
            emo_kw = {'emo_vector': tts.normalize_emo_vec(vec, apply_bias=True), 'emo_alpha': EMO_ALPHA}
        elif EMO_MODE == '3' and EMO_TEXT:
            emo_kw = {'use_emo_text': True, 'emo_text': EMO_TEXT, 'emo_alpha': EMO_ALPHA}
        tts.infer(spk_audio_prompt=PROMPT, text=text, output_path=out, verbose=False, **emo_kw)
        import soundfile as sf
        info = sf.info(out)
        rec = {'idx': idx, 'start': start, 'end': end, 'slot': round(end - start, 2),
               'audio': round(info.duration, 2), 'file': out, 'text': text}
        report.append(rec)
        json.dump(report, io.open(REPORT, 'w', encoding='utf-8'), ensure_ascii=False, indent=1)
        print('%02d  %.1fs 音频 / 槽 %.1fs  (%.0fs)' % (idx, info.duration, end - start, time.time() - t1), flush=True)
    except Exception:
        print('FAIL %02d' % idx, flush=True)
        traceback.print_exc()
print('done', len(report))
