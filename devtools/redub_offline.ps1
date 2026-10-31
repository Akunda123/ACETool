# 离线重配：不联网、不依赖 F: 的稳定性（模型在 C:\ixdata）
#
# 用法示例：
#   powershell -NoProfile -ExecutionPolicy Bypass -File devtools\redub_offline.ps1
#   powershell -NoProfile -ExecutionPolicy Bypass -File devtools\redub_offline.ps1 -Ref "docs\voice\ref_user_24k.wav" -From 1 -To 46 -Emo "语气自然亲切，有情绪起伏"
param(
    [string]$Ref = "docs\voice\ref_user_24k.wav",
    [int]$From = 1,
    [int]$To = 46,
    [string]$Emo = "语气平和自然，像正常讲解，略带一点起伏",
    [double]$EmoAlpha = 0.6,
    [string]$OutDir = "docs\voice\lines",
    [string]$Tsv = "docs\pr_voice_lines_verbatim.tsv",
    [switch]$KeepExisting        # 不加这个 ⇒ 覆盖已生成的
)

$ErrorActionPreference = 'Stop'

# 幂等：已经有合成进程在跑就让路（计划任务每 10 分钟会调一次）
$busy = Get-CimInstance Win32_Process -Filter "Name='python.exe'" -ErrorAction SilentlyContinue |
    Where-Object { $_.CommandLine -match 'synth_lines\.py' }
if ($busy) { Write-Host "已有合成进程（pid $($busy.ProcessId -join ',')），本次跳过"; exit 0 }
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $root

# 找 IndexTTS（F 盘中文目录，运行时用）
$dist = (Get-ChildItem 'F:\' -Force |
        Where-Object { $_.PSIsContainer -and $_.Name -like '*新建文件夹*11*' } |
        Select-Object -First 1).FullName
$dist = Join-Path $dist 'Index-TTS-V26\Index-TTS-V26\dist\index-tts-windows-cu128-deepspeed'
if (-not (Test-Path (Join-Path $dist 'runtime\python.exe'))) { throw "找不到 IndexTTS：$dist" }

# 参考音频拷进 app（ASCII 文件名），避免 C++ 库读中文路径
$refPath = (Resolve-Path $Ref).Path
$prompt = Join-Path $dist 'app\prompt_ref.wav'
Copy-Item $refPath $prompt -Force

$outAbs = (Resolve-Path -LiteralPath (Split-Path -Parent (Resolve-Path $OutDir -ErrorAction SilentlyContinue) -ErrorAction SilentlyContinue) -ErrorAction SilentlyContinue)
Set-Location (Join-Path $dist 'app')

$env:IX_MODEL_DIR = 'C:\ixdata\checkpoints'
$env:IX_PROMPT = $prompt
$env:IX_OUT = (Join-Path $root $OutDir)
$env:IX_TSV = (Join-Path $root $Tsv)
$env:IX_FORCE = if ($KeepExisting) { '0' } else { '1' }
if ($Emo) {
    $env:IX_EMO_MODE = '3'
    $env:IX_EMO_TEXT = $Emo
    $env:IX_EMO_ALPHA = "$EmoAlpha"
} else {
    $env:IX_EMO_MODE = '0'
}
$env:HF_HOME = 'C:\ixdata\hf_cache'
$env:HF_HUB_CACHE = $env:HF_HOME
$env:NUMBA_CACHE_DIR = 'C:\ixdata\.numba_cache'
$env:PYTHONIOENCODING = 'utf-8'

Write-Host "参考: $refPath"
Write-Host "情绪: $(if ($Emo) { $Emo } else { '（跟参考音频）' }) alpha=$EmoAlpha"
Write-Host "范围: $From..$To  输出: $env:IX_OUT"

& (Join-Path $dist 'runtime\python.exe') (Join-Path $dist 'app\synth_lines.py') $From $To
