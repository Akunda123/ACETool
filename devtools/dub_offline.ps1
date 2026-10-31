# 离线定时任务的实际动作：把「等第一批 → 补 38..46 → 出汇总」做成幂等脚本。
# 反复被计划任务调用也没关系：有活的 python 合成进程就直接退出（不抢 GPU）。
$ErrorActionPreference = 'Continue'

$Root = 'C:\Users\wujichao\Documents\acetool-app'
$Lines = Join-Path $Root 'docs\voice\lines'
$Report = Join-Path $Root 'docs\voice\lines_report.json'
$Log = Join-Path $Root 'docs\voice\dub_offline.log'
$Target = 'F:\新建文件夹 (11)\Index-TTS-V26\Index-TTS-V26\dist\index-tts-windows-cu128-deepspeed'
$Py = 'I:\runtime\python.exe'
$SysPy = 'C:\Users\wujichao\AppData\Local\Programs\Python\Python310\python.exe'

function L($m) { "{0} {1}" -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $m | Out-File -Append -Encoding UTF8 $Log }

# 单实例：拿一个全局互斥量
$mutex = New-Object System.Threading.Mutex($false, 'Global\ACEToolIndexTTSDub')
if (-not $mutex.WaitOne(0)) { L 'another instance running, exit'; exit 0 }

try {
    # 1) 盘符映射（重启后会没）
    if (-not (Test-Path $Py)) {
        cmd.exe /c subst I: $Target | Out-Null
        L ('subst I: -> ' + (Test-Path $Py))
    }

    # 2) 已有合成进程在跑 ⇒ 让路
    $busy = Get-CimInstance Win32_Process -Filter "Name='python.exe'" -ErrorAction SilentlyContinue |
        Where-Object { $_.CommandLine -match 'synth_lines\.py' }
    if ($busy) { L ('busy: pid ' + ($busy.ProcessId -join ',')); exit 0 }

    $have = (Get-ChildItem $Lines -Filter *.wav -ErrorAction SilentlyContinue | Measure-Object).Count
    L "lines=$have"
    if ($have -ge 46) { L 'all done'; }
    else {
        $env:HF_HOME = 'I:\data\hf_cache'
        $env:HF_HUB_CACHE = $env:HF_HOME
        $env:NUMBA_CACHE_DIR = 'I:\data\.numba_cache'
        $env:PYTHONIOENCODING = 'utf-8'
        Set-Location 'I:\app'
        & $Py 'I:\app\synth_lines.py' 1 46 *>> (Join-Path $Root 'docs\voice\dub_offline_synth.log')
        L ('synth exit=' + $LASTEXITCODE + ' lines=' + ((Get-ChildItem $Lines -Filter *.wav | Measure-Object).Count))
    }

    # 3) 汇总（不占 GPU）
    if (Test-Path $Report) {
        & $SysPy (Join-Path $Root 'devtools\pr_lines_summary.py') *>> $Log
        L 'summary done'
    }
} catch {
    L ('ERROR ' + $_.Exception.Message)
} finally {
    $mutex.ReleaseMutex() | Out-Null
    $mutex.Dispose()
}
