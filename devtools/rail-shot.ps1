# 侧条的证据图：**注入假光标**让第②③级出现 → 立刻截图 → 取消注入。
#
# ⚠️ 为什么不挪真鼠标：这台机器有人用，脚本放过去的光标 150ms 就被真实的手覆盖（实测过）。
# ⚠️ 为什么要在**同一条命令里**做完：分两条命令时中间隔着几秒，ACE 的状态一变（最小化/换窗），
#    细条就把自己藏了 ⇒ 截出来是空的（踩过）。
#
# 用法：powershell -NoProfile -ExecutionPolicy Bypass -File devtools/rail-shot.ps1 [-Level 1|2] [-Zoom 2]
param([int]$Level = 2, [int]$Zoom = 2)

Add-Type -AssemblyName System.Drawing

$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$url = (Get-Content (Join-Path $env:APPDATA 'ACETool\panel.json') -Raw | ConvertFrom-Json).url
$hdr = @{ 'X-ACETool' = '1' }

$st = Invoke-RestMethod -Uri ($url + 'api/rail') -TimeoutSec 5
if (-not $st.visible) { Write-Host '细条没显示（ACE 没开或被最小化）—— 先 go run ./devtools/acefocus'; exit 1 }
$scale = $st.dpi / 96.0
$r = $st.rect
$midY = [int](($r.Top + $r.Bottom) / 2)
# 第②级：离右缘 (34*scale, 52*scale] 之间；第③级：压在"字"那一列上（≤34*scale）
$dx = if ($Level -eq 2) { [int](15 * $scale) } else { [int](44 * $scale) }

$body = '{"x":' + ($r.Right - $dx) + ',"y":' + $midY + '}'
$null = Invoke-RestMethod -Uri ($url + 'api/rail/cursor') -Method Post -Headers $hdr -ContentType 'application/json' -Body $body -TimeoutSec 5
Start-Sleep -Milliseconds 500
$now = Invoke-RestMethod -Uri ($url + 'api/rail') -TimeoutSec 5

# 截：从"栏外小窗的左缘"（没有小窗就从细条左边 240px 起）到细条右缘
$leftMost = if ($now.tipRect.Right -gt 0) { [int]$now.tipRect.Left } else { [int]$now.rect.Left - 240 }
$x0 = [Math]::Max(0, $leftMost - 16)
$y0 = [Math]::Max(0, [int]$now.rect.Top)
$w = [int]($now.rect.Right - $x0)
$h = [int]($now.rect.Bottom - $now.rect.Top)
$src = New-Object System.Drawing.Bitmap -ArgumentList $w, $h
$g1 = [System.Drawing.Graphics]::FromImage($src)
$g1.CopyFromScreen($x0, $y0, 0, 0, (New-Object System.Drawing.Size -ArgumentList $w, $h))
$g1.Dispose()
$dst = New-Object System.Drawing.Bitmap -ArgumentList ($w * $Zoom), ($h * $Zoom)
$g2 = [System.Drawing.Graphics]::FromImage($dst)
$g2.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::NearestNeighbor
$g2.DrawImage($src, 0, 0, ($w * $Zoom), ($h * $Zoom)); $g2.Dispose(); $src.Dispose()
$out = Join-Path $root ('devtools\rail-L' + $Level + '.png')
$dst.Save($out, [System.Drawing.Imaging.ImageFormat]::Png); $dst.Dispose()

# 取消注入（把"靠近判定"还给真实鼠标）
$null = Invoke-RestMethod -Uri ($url + 'api/rail/cursor') -Method Post -Headers $hdr -ContentType 'application/json' -Body '{"clear":true}' -TimeoutSec 5

Write-Host ("level={0} 宽={1}px 高={2}px · tip=「{3}」 @{4},{5} {6}px · 截图 {7}x{8} ⇒ devtools\rail-L{9}.png" -f `
    $now.level, ($now.rect.Right - $now.rect.Left), ($now.rect.Bottom - $now.rect.Top), `
    $now.tipText, $now.tipRect.Left, $now.tipRect.Top, ($now.tipRect.Right - $now.tipRect.Left), $w, $h, $Level)
