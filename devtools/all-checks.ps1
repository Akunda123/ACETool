# **交活前的一套**：静态自检 + 渲染自检 + Go + 全部 e2e（URL 自动取 panel.json）。
#
# ⭐ 纪律（用户 2026-10-17）：「每次改完跑**局部检测**，一批做完再跑**总检测**」。
#    所以这里加了 `-Only`（只跑点名的几项）与 `-List`（列出可选名字）：
#      · 改一处 → `-NoE2E -Only check-ui`（纯读源码，**不碰桌面**）
#      · 一批做完 → `-NoE2E`（整轮）
#    哪些改动该跑哪几项，见 HANDOVER §91 那张"改哪儿跑哪儿"表。
# 用法：powershell -NoProfile -ExecutionPolicy Bypass -File devtools/all-checks.ps1 [-NoE2E] [-Only a,b] [-List]
param([switch]$NoE2E, [string[]]$Only = @(), [switch]$List)
# ⚠️ 从命令行传 `-Only a,b` 时 PowerShell 把整串当一个元素（"a,b"）⇒ 这里再按逗号拆一次，
#    否则 `-Only check-ui,check-served` 会一项都匹配不上（表现是"全部通过"但其实什么都没跑）。
$Only = @($Only | ForEach-Object { $_ -split ',' } | ForEach-Object { $_.Trim() } | Where-Object { $_ })
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Push-Location $root
$failed = @()

function Want([string]$name) {
  if ($script:Only.Count -eq 0) { return $true }
  return ($script:Only -contains $name)
}

function Run-Node([string]$name, [string]$file, [string[]]$extra) {
  if (-not (Want $name)) { return }
  $out = & node (Join-Path $root $file) @extra 2>&1
  $code = $LASTEXITCODE
  if ($code -eq 0) {
    Write-Host ('  ok   {0}  — {1}' -f $name, (($out | Select-Object -Last 1) -join ''))
  } else {
    Write-Host ('  FAIL {0}' -f $name)
    ($out | Select-String -Pattern 'FAIL' | Select-Object -First 3) | ForEach-Object { Write-Host ('        ' + $_.Line.Trim()) }
    $script:failed += $name
  }
}

# 细条（原生侧栏）那几份自检要它**正在跑、而且真的贴在 ACE 上**才验得了几何/行映射/参数条 ——
# 不满足就**如实跳过**（不是失败）：
#   · 细条没起（`-no-rail` 起的，或顶栏没点「侧」）；
#   · **ACE 最小化/关掉了** —— 那时细条会（正确地）把自己藏起来，`visible=false`。
#     ⚠️ 这是真机常事（用户在全屏游戏里、ACE 被最小化），以前会被当成 4 项 FAIL 误导一大圈。
function RailReady([string]$url) {
  try {
    $st = Invoke-RestMethod -Uri ($url.TrimEnd('/') + '/api/rail') -TimeoutSec 5
    return [bool]($st.running -and $st.visible)
  } catch { return $false }
}

if ($List) {
  Write-Host '这段能点名的项（配 -Only a,b 用）：'
  @('check-ui', 'check-settings', 'check-quick', 'check-served', 'check-tray', 'render-check',
    'export-preview', 'ontop-btn', 'check-rail', 'check-rail-rows', 'check-rail-switch',
    'panelcheck', 'go test') | ForEach-Object { Write-Host ('  ' + $_) }
  exit 0
}

try {
  $url = (Get-Content (Join-Path $env:APPDATA 'ACETool\panel.json') -Raw | ConvertFrom-Json).url
  Write-Host "面板：$url"

  if ($Only.Count -eq 0) { Write-Host '== 静态' }
  # ⭐ 先把所有 `.ps1` 过一遍**语法解析**（不执行）+ 查 BOM：
  #    `edit` 工具改完 `.ps1` 会**剥掉 BOM**，而 Windows PowerShell 5.1 没有 BOM 时按 GBK 读
  #    ⇒ 中文注释里的引号会把脚本解析炸掉（踩过两次：`Try statement is missing its Catch`、
  #    `Unexpected token '}'`）。这道闸就是为了在"跑脚本才发现"之前拦住它。
  $ps1bad = 0
  foreach ($f in Get-ChildItem (Join-Path $root 'devtools\*.ps1')) {
    $errs = $null
    [void][System.Management.Automation.Language.Parser]::ParseFile($f.FullName, [ref]$null, [ref]$errs)
    $b = [System.IO.File]::ReadAllBytes($f.FullName)
    $hasBom = ($b.Length -ge 3 -and $b[0] -eq 0xEF -and $b[1] -eq 0xBB -and $b[2] -eq 0xBF)
    if ($errs.Count -gt 0) {
      Write-Host ('  FAIL ps1 解析: {0} — {1}' -f $f.Name, $errs[0].Message); $ps1bad++
      $script:failed += ('ps1:' + $f.Name)
    } elseif (-not $hasBom) {
      Write-Host ('  FAIL ps1 缺 BOM（中文注释会被按 GBK 读坏）: {0}' -f $f.Name); $ps1bad++
      $script:failed += ('bom:' + $f.Name)
    }
  }
  if ($ps1bad -eq 0) { Write-Host '  ok   ps1 语法与 BOM' }
  Run-Node 'check-ui' 'devtools\check-ui.js' @()
  Run-Node 'check-settings' 'devtools\check-settings.js' @()
  # ⭐ 主页面那 20 个键位的"点按钮"链路（用户 2026-10-17）：页面暴露的 HomeQuick.run 与
  # "exe 发事件 → 页面点按钮"用同一个函数。⚠️ 它会用 `find.notes` 真开一个只读的「查找」窗口。
  Run-Node 'check-quick' 'devtools\check-quick.js' @()
  Run-Node 'check-served' 'devtools\check-served.js' @()
  # ⭐ 系统托盘图标（用户 2026-10-17）：6 档 PNG 在 + Shell 真的收下了 + 动作接口的护栏
  # ⚠️ 它会调一次 open（把面板窗口提到前台）—— 唯一带副作用的静态项。
  Run-Node 'check-tray' 'devtools\check-tray.js' @()
  Run-Node 'render-check' 'devtools\render-check.js' @()
  # ⭐ 导出页眉那几条（标题可填 / 三档页眉 / 页码 / 不印 meta）：开无头浏览器点开导出预览逐档断言
  Run-Node 'export-preview' 'devtools\shot-export-preview.js' @()
  # ⭐ 📌 置顶那颗按钮（用户 2026-10-16：「所有面板包括 home 和所有打开的都加 📌」）：
  #   开无头浏览器逐页看 —— 14 个页面都得有、挂载位置对、**算出来的样式逐项一致**
  Run-Node 'ontop-btn' 'devtools\check-ontop.js' @()

  # ⭐ 细条 = 贴在 ACE 边上的**原生汉字侧栏**（HANDOVER §69 方案 B 的侧边模式）：
  #   三份判据分开写是有原因的 —— 三级几何（check-rail）· **行映射**（check-rail-rows：注入第 i 行要命中第 i 项，
  #   用户 2026-10-17 报「所有按钮都是打开 webmain」时先验的就是它）· 运行时起停（check-rail-switch）。
  # ⚠️ 要细条正在跑；`check-rail-switch` 会**起停它一次**（收尾会把原来的状态放回去）。
  $wantRail = @(('check-rail', 'check-rail-rows', 'check-rail-switch', 'panelcheck') | Where-Object { Want $_ })
  if ($wantRail.Count -gt 0) { Write-Host '== 细条（原生侧栏；没在跑就跳过）' }
  if ($wantRail.Count -gt 0 -and (RailReady $url)) {
    Run-Node 'check-rail' 'devtools\check-rail.js' @()
    Run-Node 'check-rail-rows' 'devtools\check-rail-rows.js' @()
    Run-Node 'check-rail-switch' 'devtools\check-rail-switch.js' @()
    # 参数条（细条上「连接」/「音素」弹出来那块，§77）：合成点击验『打开 → 滑块/输入框 → 空查找被挡 → 收起』。
    # ⚠️ 它**不会**在你的工程上跑写工具（"查找"空着点「替换」= 被挡住那条路）。
    $pc = if (Want 'panelcheck') { & go run ./devtools/panelcheck 2>&1 } else { @('（没点名 panelcheck ⇒ 跳过）') ; $LASTEXITCODE = 0 }
    if ($LASTEXITCODE -eq 0) {
      Write-Host '  ok   panelcheck  — 全部通过'
    } else {
      Write-Host '  FAIL panelcheck'
      ($pc | Select-String -Pattern 'FAIL' | Select-Object -First 3) | ForEach-Object { Write-Host ('        ' + $_.Line.Trim()) }
      $script:failed += 'panelcheck'
    }
  } elseif ($wantRail.Count -gt 0) {
    Write-Host '  （细条没在跑 / ACE 最小化 ⇒ 跳过这几份（要跑它们：ACE 开着且没最小化，顶栏点「侧」））'
  }

  if (Want 'go test') {
    Write-Host '== Go'
    & gofmt -l . | ForEach-Object { Write-Host ('  FAIL gofmt: ' + $_); $script:failed += 'gofmt' }
    & go vet ./... 2>&1 | ForEach-Object { if ($_ -match '\S') { Write-Host ('  ' + $_); $script:failed += 'go vet' } }
    & go test ./... 2>&1 | Select-Object -Last 4 | ForEach-Object { Write-Host ('  ' + $_) }
    if ($LASTEXITCODE -ne 0) { Write-Host '  FAIL go test'; $script:failed += 'go test' }
  }

  if (-not $NoE2E) {
    Write-Host '== e2e（要面板在线；URL 取自 panel.json）'
    $suites = @('e2e-find-ui.js', 'e2e-notation-print.js', 'e2e-notation-gen.js', 'e2e-notation-window.js',
      'e2e-gate.js', 'e2e-param-ui.js', 'e2e-within-ui.js', 'e2e-xcopy-ui.js', 'e2e-undo-ui.js',
      'e2e-quicktip.js', 'e2e-shortcuts.js', 'e2e-multiwindow.js', 'e2e.js')
    foreach ($s in $suites) { Run-Node $s ('devtools\' + $s) @($url) }
  }

  if ($failed.Count) {
    Write-Host ("`n{0} 项没过：{1}" -f $failed.Count, ($failed -join ', '))
    exit 1
  }
  Write-Host "`n全部通过"
} finally {
  Pop-Location
}


