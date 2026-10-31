"""用用户自己的预设（播音.wav）跑一次 IndexTTS2 合成，产出一段演示旁白。

在 IndexTTS 的 app 目录下运行（cwd 必须是 app），用它的 runtime/python.exe。
"""
import os
import sys
import time
import traceback

APP = os.path.dirname(os.path.abspath(__file__))
os.chdir(APP)
sys.path.insert(0, APP)
# wetext 的 kaldifst 打不开中文路径 ⇒ 用 ASCII 路径下的副本（11MB，复制自 site-packages）
sys.path.insert(0, r"C:\Users\wujichao\AppData\Local\Temp\ixenv")

DIST = os.path.dirname(APP)
MODEL_DIR = os.path.join(DIST, "data", "checkpoints")

os.environ.setdefault("HF_HOME", os.path.join(DIST, "data", "hf_cache"))
os.environ.setdefault("HF_HUB_CACHE", os.path.join(DIST, "data", "hf_cache"))
os.environ.setdefault("HUGGINGFACE_HUB_CACHE", os.path.join(DIST, "data", "hf_cache"))
os.environ.setdefault("NUMBA_CACHE_DIR", os.path.join(DIST, "data", ".numba_cache"))

PROMPT = os.path.join(APP, "prompts", "library", "播音.wav")
OUT_DIR = r"C:\Users\wujichao\Documents\acetool-app\docs\voice"
TEXTS = {
    "preset_boyin_short.wav": "这是一个贴在 ACE Studio 边上的小工具。",
    "preset_boyin_demo.wav": (
        "你看啊，这个工具，其实就贴在 ACE 边上。双击就能跑，不弹黑框，也不抢焦点。"
        "改歌词、搬参数、复制音高线，都变成一次点击。"
    ),
}


def main():
    from indextts.infer_v2 import IndexTTS2

    t0 = time.time()
    print("加载模型：", MODEL_DIR, flush=True)
    tts = IndexTTS2(
        model_dir=MODEL_DIR,
        cfg_path=os.path.join(MODEL_DIR, "config.yaml"),
        use_fp16=True,
        use_deepspeed=False,
        use_cuda_kernel=False,
    )
    print("模型就绪 %.1fs" % (time.time() - t0), flush=True)

    for name, text in TEXTS.items():
        out = os.path.join(OUT_DIR, name)
        t1 = time.time()
        try:
            tts.infer(spk_audio_prompt=PROMPT, text=text, output_path=out, verbose=False)
            print("OK %s  %.1fs  %.0f KB" % (out, time.time() - t1, os.path.getsize(out) / 1024), flush=True)
        except Exception:
            print("FAIL %s" % name, flush=True)
            traceback.print_exc()


if __name__ == "__main__":
    main()
