# 通用区域截图（放大 N 倍），用来**量**某个窗口的界面尺寸、做证据图。
#
# 用法：powershell -NoProfile -ExecutionPolicy Bypass -File devtools/shot-region.ps1 -X 1400 -Y 285 -W 270 -H 120 -Zoom 4 -Out devtools\ace-topright.png
param(
  [Parameter(Mandatory = $true)][int]$X,
  [Parameter(Mandatory = $true)][int]$Y,
  [Parameter(Mandatory = $true)][int]$W,
  [Parameter(Mandatory = $true)][int]$H,
  [int]$Zoom = 1,
  [string]$Out = 'devtools\region.png'
)

Add-Type -AssemblyName System.Drawing

$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$src = New-Object System.Drawing.Bitmap -ArgumentList $W, $H
$g1 = [System.Drawing.Graphics]::FromImage($src)
$g1.CopyFromScreen($X, $Y, 0, 0, (New-Object System.Drawing.Size -ArgumentList $W, $H))
$g1.Dispose()

if ($Zoom -gt 1) {
  $dst = New-Object System.Drawing.Bitmap -ArgumentList ($W * $Zoom), ($H * $Zoom)
  $g2 = [System.Drawing.Graphics]::FromImage($dst)
  $g2.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::NearestNeighbor
  $g2.DrawImage($src, 0, 0, ($W * $Zoom), ($H * $Zoom))
  $g2.Dispose()
  $src.Dispose()
  $src = $dst
}

$path = Join-Path $root $Out
$src.Save($path, [System.Drawing.Imaging.ImageFormat]::Png)
$src.Dispose()
Write-Host ("({0},{1}) {2}x{3} ×{4} ⇒ {5}" -f $X, $Y, $W, $H, $Zoom, $Out)
