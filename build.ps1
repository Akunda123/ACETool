# ACETool 构建脚本 —— 只做一件事：按 HANDOVER.md §1 的配方出单文件 exe
# 配方：CGO_ENABLED=0 ⇒ 零运行时依赖；-trimpath -ldflags "-s -w" ⇒ 剥符号
# ⚠️ 不要用 UPX（明显提高杀软误报率，见 HANDOVER.md §2）

param([string]$Version = '1.0.0')
$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Error "没找到 go。装一个：winget install GoLang.Go（或 https://go.dev/dl/）"
}

$env:CGO_ENABLED = '0'
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'

Write-Host "==> go vet ./..." -ForegroundColor Cyan
go vet ./...

Write-Host "==> go test ./..." -ForegroundColor Cyan
go test ./...

Write-Host "==> go build" -ForegroundColor Cyan
# 用户 2026-10-20：「能否将那个 command 窗口隐藏」⇒ -H=windowsgui（GUI 子系统，双击不弹黑框）。
# 要看日志：用 -console 启动，或看 %APPDATA%\ACETool\acetool.log（main.go 里重定向）。
go build -trimpath -ldflags "-s -w -H=windowsgui -X main.appVersion=$Version" -o acetool.exe .

Get-Item -LiteralPath .\acetool.exe | Select-Object Name, @{n = 'Size(MB)'; e = { [math]::Round($_.Length / 1MB, 2) } }, LastWriteTime
