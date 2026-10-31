# 把**我们自己的**窗口（细条 / 参数条）用 PrintWindow 存成 PNG，并采样几个像素核对颜色。
#
# 为什么不截屏：细条是 ACE 的**子窗口**，用户切到别的程序时它会被盖住（按设计）⇒ 截屏拍到的是别人的画面。
# 而它是**我们自己的 GDI 窗口**（不是 ACE 那种 Qt 窗口 —— 那些 PrintWindow 拍出来是空的），
# 所以可以绕开遮挡直接拍窗口内容，既不动用户的屏幕，也不抢焦点。
#
# 用法：pwsh -File devtools\shot-window.ps1 -Hwnd 0x1234 -Out devtools\rail-win.png
param(
  [Parameter(Mandatory = $true)][string]$Hwnd,
  [string]$Out = 'devtools\window.png',
  [int]$Zoom = 1
)

Add-Type -AssemblyName System.Drawing
Add-Type -Namespace SW -Name P -MemberDefinition @'
[StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
[DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
[DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr h, IntPtr hdc, uint flags);
'@

$h = if ($Hwnd -like '0x*') { [IntPtr][Convert]::ToInt64($Hwnd.Substring(2), 16) } else { [IntPtr][int]$Hwnd }
$r = New-Object SW.P+RECT
if (-not [SW.P]::GetWindowRect($h, [ref]$r)) { throw "GetWindowRect 失败（句柄 0x$('{0:x}' -f $h.ToInt64())）" }
$w = $r.Right - $r.Left; $ht = $r.Bottom - $r.Top
if ($w -le 0 -or $ht -le 0) { throw "窗口尺寸不对：${w}x${ht}" }

$bmp = New-Object System.Drawing.Bitmap -ArgumentList $w, $ht
$g = [System.Drawing.Graphics]::FromImage($bmp)
$hdc = $g.GetHdc()
# PW_RENDERFULLCONTENT = 2（对 GDI 窗口 0 也行，两个都试一下）
$ok = [SW.P]::PrintWindow($h, $hdc, 2)
if (-not $ok) { $ok = [SW.P]::PrintWindow($h, $hdc, 0) }
$g.ReleaseHdc($hdc)
$g.Dispose()
if (-not $ok) { throw "PrintWindow 失败（这个窗口不配合；ACE 的 Qt 窗口就是这样）" }

$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$path = Join-Path $root $Out
$bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png)

function Hex($c) { '#{0:x2}{1:x2}{2:x2}' -f $c.R, $c.G, $c.B }
Write-Host ("PrintWindow 0x{0:x} ⇒ {1}（{2}x{3}）" -f $h.ToInt64(), $Out, $w, $ht)
# 采样：背景（右上角空白）、左边框（x=0）、左起第 3 像素（颜色码）
$midY = [int]($ht / 2)
Write-Host ("  背景   (x={0}, y={1}) = {2}" -f ($w - 4), $midY, (Hex $bmp.GetPixel($w - 4, $midY)))
Write-Host ("  左边框 (x=0, y={0})  = {1}" -f $midY, (Hex $bmp.GetPixel(0, $midY)))
if ($w -gt 4) {
  for ($i = 0; $i -lt [Math]::Min(4, [Math]::Floor($ht / 40)); $i++) {
    $yy = [int](($i + 0.5) * ($ht / [Math]::Max(1, [Math]::Floor($ht / 40))))
    Write-Host ("  行 {0} 颜色码 (x=3, y={1}) = {2}" -f $i, $yy, (Hex $bmp.GetPixel([Math]::Min(3, $w - 1), [Math]::Min($yy, $ht - 1))))
  }
}
$bmp.Dispose()
