# 发版流程（ACETool）

> 适用：Windows 本机，仓库 `Akunda123/ACETool`。
> ⚠️ 本机常有 **github.com:443 被拦**（DNS 解析到 127.0.0.1）而 `api.github.com` 正常的情况，
> 所以下面给两条推送路子：正常 `git push` 与 **REST API 推送**。

---

## 0. 版本号有两处，必须一致

| 位置 | 作用 |
|---|---|
| `main.go` 里的 `var appVersion = "x.y.z"` | 程序内显示的版本（关于页、更新检测的“当前”）|
| 仓库根 `VERSION` 文件（纯文本一行） | 给**回退源**读的版本（jsDelivr / gh-proxy / raw）|

> GitHub API 源读的是 **release tag**，另外三个源读的是 `VERSION`；两边不一致会导致
> “国外提示有新版本、国内提示已最新”，所以每次都一起改。

---

## 1. 改代码 → 改版本号

```powershell
# 1) 改 main.go 的 appVersion
# 2) 改 VERSION
"1.1.1" | Out-File -Encoding ASCII -NoNewline VERSION
```

## 2. 构建单文件 exe

```powershell
.\build.ps1 -Version 1.1.1
```

- 内部会跑 `go vet ./...` 与 `go test ./...`；
- 产物 `acetool.exe` 被 `.gitignore` 忽略，**不进仓库**；
- 拷一份到 `dist\v1.1.1\acetool_v1.1.1.exe`，并生成校验：

```powershell
New-Item -ItemType Directory -Force dist\v1.1.1 | Out-Null
Copy-Item acetool.exe dist\v1.1.1\acetool_v1.1.1.exe
(Get-FileHash dist\v1.1.1\acetool_v1.1.1.exe -Algorithm SHA256).Hash |
  ForEach-Object { "SHA256 $_" } | Out-File -Encoding ASCII dist\v1.1.1\acetool_v1.1.1.exe.sha256
```

## 3. 提交与推送源码

### 3a. 网络正常（推荐）

```powershell
git add -A
git -c user.name="Akunda123" -c user.email="Akunda123@users.noreply.github.com" `
    commit -m "ACETool 1.1.1：<一句话>"
git push origin main
```

### 3b. `github.com:443` 不通（本机现状）

用 REST API 推（`api.github.com` 可达）：

```powershell
# 全量（首次/大改）：463 个文件，约 10 分钟，有限流重试
node devtools\gh_push.js

# 增量（只推改过的文件）：秒级
node devtools\gh_push_inc.js main.go VERSION internal\web\assets\settings.html
```

> 两者都需要 `gh auth login` 过的 token（脚本会自己 `gh auth token` 取）。
> API 推送会在远端建独立提交，**本地 git 历史与远端不一致**；
> 网络恢复后可用 `git fetch origin && git reset --soft origin/main` 对齐（先备份本地改动）。

## 4. 建 Release（带 exe 附件）

```powershell
gh release create v1.1.1 dist\v1.1.1\acetool_v1.1.1.exe `
  --title "ACETool 1.1.1" `
  --notes "新增：… 修复：… "
```

- tag 必须带 `v` 前缀（更新检测会把 `v` 去掉再比较）；
- `releases/latest` 只认**非 draft、非 prerelease** 的那一条，所以发完要确认它是 Latest；
- 更新检测读的是 `html_url`，点托盘气泡就是打开这个页面。

## 5. 自检（发完必做）

```powershell
# ① 远端版本号
gh api repos/Akunda123/ACETool/contents/VERSION --jq .content

# ② 本机程序看到的结果（需要面板在跑）
#    current 应该是新版本，latest 与 VERSION 一致，newer 应为 false
curl.exe -s -H "X-ACETool: 1" http://127.0.0.1:<端口>/api/update?force=1
```

面板端口见 `%APPDATA%\ACETool\panel.json`。

## 6. 常见坑

| 现象 | 原因 / 处理 |
|---|---|
| 关于页“检查失败：连不上 GitHub” | 三个回退源也失败；看日志里每一条的错误，通常是网络整体不通 |
| 国外提示新版、国内不提示 | `VERSION` 与 release tag 不一致 |
| `git push` 报 `Failed to connect to github.com port 443` | 本机拦截；走 3b 的 API 推送 |
| API 推送 403 `secondary rate limit` | 等几分钟；脚本已按 15s×n 退避重试 |
| 弹窗没出现 | 专注助手/勿扰会折叠通知；另外同一版本只弹一次（`config.json` 的 `notifiedVersion`）|
| 发布后 `latest` 仍是旧版本 | release 被标成 draft/prerelease，或旧 release 还占着 Latest |

## 7. 版本号含义（本项目约定）

- `1.x.0`：加功能；
- `1.x.y`：只修 bug；
- 不自动更新、不静默下载：程序只**提示**，下载由用户点链接完成。
