# 复现「关闭 web 面板后侧栏会消失」：起面板 → 起侧栏 → 开一个 web 面板窗口 → 关掉它 → 立刻看 exe 还在不在。
#
# 判据分三层（缺一层就会把结论说错）：
#   ① exe 进程还在吗（`Get-Process acetool`）
#   ② 面板 API 还应答吗（`/api/ping`）—— 进程在但服务死了是另一回事
#   ③ 侧栏窗口还在吗（`/api/rail` 的 running/visible + 窗口枚举里有没有 `ACETool 细条`）
$ErrorActionPreference = 'Continue'
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $root

function Show-State([string]$tag) {
  $p = Get-Process -Name acetool -ErrorAction SilentlyContinue
  $json = Get-Content (Join-Path $env:APPDATA 'ACETool\panel.json') -Raw | ConvertFrom-Json
  $alive = [bool]$p
  $api = 'no'
  try { $null = Invoke-RestMethod -Uri ($json.url.TrimEnd('/') + '/api/ping') -TimeoutSec 3; $api = 'yes' } catch { $api = 'no' }
  $rail = 'n/a'
  if ($api -eq 'yes') {
    try {
      $st = Invoke-RestMethod -Uri ($json.url.TrimEnd('/') + '/api/rail') -TimeoutSec 3
      $rail = "running=$($st.running) visible=$($st.visible) level=$($st.level)"
    } catch { $rail = '问不到' }
  }
  $rw = (Get-Process -Name msedge -ErrorAction SilentlyContinue | Where-Object { $_.MainWindowTitle -like 'ACETool 细条*' }).Count
  Write-Host ("[{0}] exe pid={1} 活着={2} · /api/ping={3} · 侧栏 {4} · 细条窗口数={5}" -f $tag, $json.pid, $alive, $api, $rail, $rw)
  return [pscustomobject]@{ Alive = $alive; API = $api; Rail = $rail }
}

Write-Host '== ① 起面板（-no-rail，跟平时一样）'
& powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $root 'devtools\start-panel.ps1') -NoBrowser | Select-Object -Last 2
Start-Sleep -Seconds 2
$j = Get-Content (Join-Path $env:APPDATA 'ACETool\panel.json') -Raw | ConvertFrom-Json
$base = $j.url.TrimEnd('/')
$H = @{ 'X-ACETool' = '1' }
$before = Show-State '刚起来'

Write-Host '== ② 起侧栏'
Invoke-RestMethod -Uri "$base/api/rail" -Method Post -Headers $H -ContentType 'application/json' -Body '{"enable":true}' -TimeoutSec 20 | ConvertTo-Json -Compress
Start-Sleep -Seconds 2
Show-State '侧栏已起' | Out-Null

Write-Host '== ③ 开一个 web 面板窗口（app 模式，等于用户那个「web 面板」）'
$d = Invoke-RestMethod -Uri "$base/api/app-window" -Method Post -Headers $H -ContentType 'application/json' -Body '{"page":"home.html"}' -TimeoutSec 20
"  开窗回包：" + ($d | ConvertTo-Json -Compress)
Start-Sleep -Seconds 3
$wins = Get-Process -Name msedge -ErrorAction SilentlyContinue | Where-Object { $_.MainWindowTitle -like '*ACETool*' } | Select-Object Id, MainWindowTitle
$wins | Format-Table -AutoSize
Show-State 'web 面板已开' | Out-Null

Write-Host '== ④ 关掉那个 web 面板窗口（就是用户关面板那一下）'
# ⚠️ 不能用 `Get-Process msedge | MainWindowTitle`：Edge 是多进程，app 窗口的标题不一定挂在那个进程上
#    （第一次跑就是因为这个"没找到窗口"而没验成）⇒ 用自己的 `devtools/winclose`（按标题找真窗口 + 发 WM_CLOSE）。
& go run (Join-Path $root 'devtools\winclose') -title 'ACETool' -exact
if ($LASTEXITCODE -ne 0) { Write-Host "  winclose 退出码 $LASTEXITCODE ⇒ 这一步没做成，结论不能说"; exit 9 }
Start-Sleep -Seconds 3
$after = Show-State '关掉 web 面板 3 秒后'

Write-Host ''
if ($before.Alive -and -not $after.Alive) {
  Write-Host '⛔ 复现了：关掉 web 面板窗口 ⇒ **exe 也没了**（不是侧栏单独消失，是整个 exe 死了）'
} elseif ($before.Alive -and $after.Alive -and $after.Rail -ne $before.Rail) {
  Write-Host '⚠️ exe 还活着，但侧栏状态变了（那就是"侧栏自己消失"这条路）'
} elseif ($before.Alive -and $after.Alive) {
  Write-Host '✅ 关掉 web 面板**不影响** exe 与侧栏 —— 这次没复现'
}
