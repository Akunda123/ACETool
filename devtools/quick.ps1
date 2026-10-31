# 改完前端资源后**最短的一条路**：重建 + 重启 + 确认面板发的就是盘上那份。
#
# 什么时候用它（用户 2026-10-08：「就改一个 default string 为什么这么长时间」）：
#   · 一行文案 / 一个默认值 / 一个 class 之类的改动 —— 就这一条命令，然后在浏览器里看一眼。
#   · 别再顺手跑整套 e2e 与截图：那是 `all-checks.ps1` 的活，等一批功能做完再跑一次。
#
# 用法：powershell -NoProfile -ExecutionPolicy Bypass -File devtools/quick.ps1 [-Ui]
#   -Ui  顺便跑 check-ui（纯静态、一秒，改动碰了面板结构时带上）
param([switch]$Ui)
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Push-Location $root
try {
  # start-panel.ps1 自己会先把旧实例收拾掉
  & go build -o acetool.exe .
  if ($LASTEXITCODE -ne 0) { throw 'go build 失败（上面有编译错误）' }
  & powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $root 'devtools\start-panel.ps1') -NoBrowser
  & node (Join-Path $root 'devtools\check-served.js')
  if ($Ui) { & node (Join-Path $root 'devtools\check-ui.js') }
} finally {
  Pop-Location
}
