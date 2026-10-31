# 配音链：等第一批（1..37）跑完 → 自动补第二批（38..46）→ 出时长汇总
$ErrorActionPreference = 'Continue'
$Log = 'I:\supervisor.log'
function L($m) { "{0} {1}" -f (Get-Date -Format 'HH:mm:ss'), $m | Out-File -Append -Encoding UTF8 $Log }

L 'supervisor start'

# 0) 确保 I: 映射还在（重启后需要重建）
if (-not (Test-Path 'I:\runtime\python.exe')) {
    cmd /c subst I: "F:\新建文件夹 (11)\Index-TTS-V26\Index-TTS-V26\dist\index-tts-windows-cu128-deepspeed" | Out-Null
    L ('subst recreated: ' + (Test-Path 'I:\runtime\python.exe'))
}

$LINES = 'C:/Users/<user>\Documents\acetool-app\docs\voice\lines'

# 1) 等第一批退出（python 进程消失）
while ($true) {
    $p = Get-Process python -ErrorAction SilentlyContinue
    if (-not $p) { break }
    Start-Sleep -Seconds 30
}
$n = (Get-ChildItem $LINES -Filter *.wav | Measure-Object).Count
L ("batch1 done, lines=$n")

# 2) 补全 1..46（已完成的自会跳过；把 38..46 一起带上）
$env:HF_HOME = 'I:\data\hf_cache'
$env:HF_HUB_CACHE = $env:HF_HOME
$env:NUMBA_CACHE_DIR = 'I:\data\.numba_cache'
$env:PYTHONIOENCODING = 'utf-8'
Set-Location 'I:\app'

foreach ($try in 1..2) {
    L "batch2 attempt $try (1..46)"
    & 'I:\runtime\python.exe' 'I:\app\synth_lines.py' 1 46 *>> 'I:\batch2.log'
    $code = $LASTEXITCODE
    $n2 = (Get-ChildItem $LINES -Filter *.wav | Measure-Object).Count
    L "batch2 attempt $try exit=$code lines=$n2"
    if ($n2 -ge 46) { break }
}

# 3) 时长汇总（用系统 python，不占显卡）
& 'C:/Users/<user>\AppData\Local\Programs\Python\Python310\python.exe' 'C:/Users/<user>\Documents\acetool-app\devtools\pr_lines_summary.py' *>> $Log
L 'summary done'
