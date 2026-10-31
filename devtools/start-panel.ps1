# 起 ACETool 面板（**脱离调用方的进程组**起）
#
# ⚠️ 先说清楚：这里**不是**在修什么"面板会自己掉"的根因 —— 那件事查过，没查到 agent 侧的原因
#   （一次是用户关机重启；另一次也没有证据，反而有反例：用 Start-Process 起的实例跨了好几条命令都活着）。
#   本脚本只是把"起服务"这一步做得**更不容易被调用方牵连**：
#
#   用 WMI 的 `Win32_Process.Create` 起，进程挂在 WmiPrvSE 下、**不在我们这条命令的进程组里**；
#   而 `Start-Process .\acetool.exe` 起的子进程与那次命令的控制台同生共死（**理论上有这个风险，未验证**）。
#
# 用法：pwsh -File devtools/start-panel.ps1 [-NoBrowser] [-NoRail]
#
# ⚠️ 2026-10-16 的老警告（**已作废，留作历史**）：当时默认传 `-no-rail`，理由是"带侧条 8 秒内静默退出"。
# ✅ 2026-10-17：那件事的**真因找到了**（HANDOVER §76.12）—— 不是原生层异常，是 **Go 的 Windows 回调表被用光**：
#    `syscall.NewCallback` 的回调表**定长（约两千）且永不释放**，而侧条**每 120ms 就 `List()` 枚举一次窗口**
#    ⇒ 约 3 分 40 秒后 Go 抛 `fatal error: too many callback functions` ⇒ **exit 2**；
#    而 detached 进程的 stderr 谁也看不见 ⇒ 所以一直"没有 panic 文本"，把我带偏成"像是原生层崩"。
#    修法：枚举用**一个**缓存好的回调（`internal/win/window_windows.go` 的 `enumWindowsAll`），
#    窗口过程同理（`internal/rail` 的 `wndProcCB`）；侧条里的回调另外都套了 `safe(...)`，
#    panic 会**就地救回**并落一行 `%APPDATA%\ACETool\rail.log`。
# ⇒ 现在**默认就带侧条**（它就是面板的"侧栏"）。不想起它：`-NoRail`（`-Rail` 也还认，老命令不用改）。
param([switch]$NoBrowser, [switch]$NoRail, [switch]$Rail)

$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$exe = Join-Path $root 'acetool.exe'
if (-not (Test-Path $exe)) { throw "找不到 $exe（先 go build）" }

# 先收拾掉旧的（单实例守卫会让新实例自己退出，所以旧的必须先走）
Get-Process acetool -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 400

# ⚠️ 别用 `$args` 当变量名 —— 那是 PowerShell 的自动变量（当前脚本的参数）。
# 侧条**默认就建**（§72 已修，见文件头）；要不起就用 `-NoRail`。
$argLine = @()
if ($NoBrowser) { $argLine += '-no-browser' }
if ($NoRail -and -not $Rail) { $argLine += '-no-rail' }
$argLine = $argLine -join ' '
$r = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{
  CommandLine      = ('"{0}" {1}' -f $exe, $argLine).Trim()
  CurrentDirectory = $root
}
if ($r.ReturnValue -ne 0) { throw "Win32_Process.Create 失败：ReturnValue=$($r.ReturnValue)" }

Start-Sleep -Seconds 3
$url = (Get-Content (Join-Path $env:APPDATA 'ACETool\panel.json') -Raw | ConvertFrom-Json).url
$ping = Invoke-RestMethod -Uri ($url + 'api/ping') -TimeoutSec 5
Write-Host "面板：$url"
Write-Host "pid=$($ping.pid) · $($ping.tools)"
# ⚠️ 这里**不再自己开浏览器窗口**（2026-10-17）：exe 启动时会自己按**app 模式**开一个
# 无地址栏的小窗（`openPanelWindow`，尺寸取 `web.PanelSize("index.html")` = 720×760 逻辑）。
# 原来这里再 `rundll32 url.dll,FileProtocolHandler` 一次 ⇒ 会**多出一个普通浏览器窗口**（带地址栏），
# 正是用户抱怨"地址栏没藏起来"的来源之一。
