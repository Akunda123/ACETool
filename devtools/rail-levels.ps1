# 侧条**三级**自检 + 证据图（用户 2026-10-16 的口径）：
#   ① 默认特别细  ② 靠近显示 1–2 个汉字  ③ 悬停到字上 ⇒ 在**栏外**（细条左边）弹小窗显示全名
#
# ⚠️ 为什么不去"设置鼠标位置"：这台机器**有人在用**，脚本把光标放过去 150ms 后就被真实鼠标覆盖了
#    （实测：脚本放到 2332，rail 读到的是 2559 —— 那是用户的手）。所以改成：
#     ① 采样若干秒，**按它自己读到的光标位置推算期望级别**，跟它报的级别对账（与谁动鼠标无关）；
#     ② 期间一旦出现第③级，就截图留证据，并核对小窗确实在**栏外**（tipRect.Right ≤ 细条左缘）。
#
# 用法：powershell -NoProfile -ExecutionPolicy Bypass -File devtools/rail-levels.ps1 [-Seconds 12]
param([int]$Seconds = 12, [int]$Zoom = 2)

Add-Type -AssemblyName System.Drawing

$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$url = (Get-Content (Join-Path $env:APPDATA 'ACETool\panel.json') -Raw | ConvertFrom-Json).url
function Rail() { Invoke-RestMethod -Uri ($url + 'api/rail') -TimeoutSec 5 }

$st = Rail
if (-not $st.visible) { Write-Host '细条现在没显示（ACE 没开？）'; exit 1 }
$scale = $st.dpi / 96.0
$nearW = [int](52 * $scale)
$shortW = [int](34 * $scale)

Write-Host ("采样 {0} 秒（把鼠标**移到细条上、再逐个滑过几个字**，能拍到第③级）…" -f $Seconds)
$seen = @{}; $bad = 0; $n = 0; $tipShot = 0; $tipBad = 0
$deadline = (Get-Date).AddSeconds($Seconds)
while ((Get-Date) -lt $deadline) {
  $s = Rail
  $n++
  $r = $s.rect
  $cx = $s.cursorX; $cy = $s.cursorY
  $inY = ($cy -ge $r.Top -and $cy -le $r.Bottom)
  $dist = $r.Right - $cx
  $exp = 0
  if ($inY -and $dist -le $nearW -and $dist -ge (-$nearW)) {
    $exp = 1
    if ($dist -le $shortW -and $dist -ge 0) { $exp = 2 }
  }
  $seen["$($s.level)"] = 1
  if ($exp -ne $s.level) {
    $bad++
    if ($bad -le 5) { Write-Host ("  FAIL 光标({0},{1}) dist={2} ⇒ 期望 level={3}，它报 {4}" -f $cx, $cy, $dist, $exp, $s.level) }
  }
  if ($s.level -eq 2) {
    $tr = $s.tipRect
    $outside = ($tr.Right -le $r.Left) -and (($tr.Right - $tr.Left) -gt 0)
    if (-not $outside) { $tipBad++; Write-Host ("  FAIL 第③级的小窗不在栏外：tip=({0}..{1}) 细条左缘={2}" -f $tr.Left, $tr.Right, $r.Left) }
    if ($tipShot -lt 2 -and $s.tipText) {
      $tipShot++
      $x0 = [Math]::Max(0, [int]$tr.Left - 12); $y0 = [Math]::Max(0, [int]$r.Top)
      $w = [int]($r.Right - $x0); $h = [int]($r.Bottom - $r.Top)
      $src = New-Object System.Drawing.Bitmap -ArgumentList $w, $h
      $g1 = [System.Drawing.Graphics]::FromImage($src)
      $g1.CopyFromScreen($x0, $y0, 0, 0, (New-Object System.Drawing.Size -ArgumentList $w, $h))
      $g1.Dispose()
      $dst = New-Object System.Drawing.Bitmap -ArgumentList ($w * $Zoom), ($h * $Zoom)
      $g2 = [System.Drawing.Graphics]::FromImage($dst)
      $g2.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::NearestNeighbor
      $g2.DrawImage($src, 0, 0, ($w * $Zoom), ($h * $Zoom)); $g2.Dispose(); $src.Dispose()
      $out = Join-Path $root ('devtools\rail-tip-' + $tipShot + '.png')
      $dst.Save($out, [System.Drawing.Imaging.ImageFormat]::Png); $dst.Dispose()
      Write-Host ("  第③级：全名「{0}」弹在栏外 {1}px 宽 ⇒ devtools\rail-tip-{2}.png" -f $s.tipText, ($tr.Right - $tr.Left), $tipShot)
    }
  }
  Start-Sleep -Milliseconds 200
}

Write-Host ''
Write-Host ("采样 {0} 次 · 见过的级别 {1} · 级别对账不一致 {2} · 栏外核对失败 {3} · 拍到第③级 {4} 次" -f `
    $n, (($seen.Keys | Sort-Object) -join '/'), $bad, $tipBad, $tipShot)
Write-Host ("置顶自检：topMost={0}（**必须 false** —— 它不许浮在别的应用上面）· 属主=0x{1:x}" -f $st.topMost, $st.ownerHwnd)
if ($bad -or $tipBad) { exit 1 }
Write-Host '三级逻辑对账通过'
