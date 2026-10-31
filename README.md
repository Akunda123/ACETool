# ACETool

**ACE Studio 工具箱** —— 独立、零依赖、Windows 单文件（目标 < 10 MB），通过 ACE 自带的
`acestudio-cli.exe` 读/改 ACE Studio 工程（轨道 / 片段 / 音符 / 参数 / 导出）。

> **资料仓是隔壁的 `..\acetool\`**（HANDOVER.md + 1.8 MB 参考资料 + 参考脚本）。
> 本仓是**代码仓**；设计的单一事实源仍然是 `..\acetool\HANDOVER.md` —— 有冲突以它为准并回写它。

---

## 现在的状态（2026-10-04）

| 项 | 状态 |
|---|---|
| M0 启动自检（找 CLI → `--version` → 在线灯 → 本地只读面板） | ✅ **已跑通（真机）** |
| `go vet ./...` | ✅ 干净（Go 1.27.0 windows/amd64） |
| `go test ./...` | ✅ **116 条**离线用例全过（acetool 2 · acecli 10 · acep 19 · cbor 18 · tools 67），不依赖 ACE |
| 前端面板自检 | ✅ `node tools/checkpanel.js` **71 条**全过（最小 DOM 桩，不需要浏览器） |
| `go build`（`CGO_ENABLED=0` + `-trimpath -ldflags "-s -w"`） | ✅ `acetool.exe` **8.11 MB** |
| 真机自检 | ✅ `/api/selfcheck` 三步全绿：CLI 从 **PATH** 找到 · `cli 0.17.0 · built for ACE Studio 2.1.8 · Surface 17.1` · `project info` 读到工程（`duration 57600, isNewProject/isTempProject = true, projectName ""` —— 当时 ACE 里开着的是**空白临时工程**） |
| **M1 只读巡检面板** | ✅ **已跑通（真机）** —— 工具壳 + 9 个只读工具，对着「洛天依」Sing 轨真读到了：片段「啦」0–3840 tick / 1 音符（pitch 57 · dur 1920 · headConsonants 0.0697 · 带 `clip-note-content` fingerprint）· 120 BPM · 4/4 |
| **M2 查找面板** | ✅ **已跑通（真机 · 2026-10-04）**：逐条复刻 `[SV2]查找.js` 的面板 + 18 属性 / 6 参数 / 6 参数层；`find.goto`、`find.select` 真写入并回读核对过（18 个命中全选中、`notesNotFound: 0`） |
| **M2 歌词面板** | ✅ **已跑通（真机 · 2026-10-04）**：逐条复刻 `[SV2]歌词侧栏.js`；分页 / 分割成字符 / 跳句都在；两种写法都真写过并回读核对过，**测完已把歌词还原** |
| 写入路径 | ✅ **已开**（2026-10-04）：`color.cycle`（轨道颜色循环）是第一个写工具，也是写路径的冒烟测试。**干跑闸门在 runner 里**（勾了「干跑」⇒ 一条都不执行，只回计划），写后回读可选，真机验收过「读 + 写 + 撤销」 |
| 文件路线 | ✅ **第一刀（只读）**：`acep.inspect` 判**三种壳**：① ACEP2 校验长度 + 解 CBOR payload；② 历史壳一（有 `compressMethod`）**可读**，内层是 JSON 文本；③ 历史壳二（没有 `compressMethod`）加密，**只认出不解密**。全部带往返自检。zstd 依赖实测 **+0.46 MB**；构建需 `GOPROXY` 镜像且 Go ≥1.25 |

⚠️ **8.11 MB 超出了 HANDOVER.md §1 估的 4–6 MB**（Go 1.27 的 `net/http` + zstd 编解码器），但仍在 < 10 MB 目标内。
要压体积可以试 `-tags netgo` / 换更轻的 HTTP 层；先记下，别为此改架构。

**Go 工具链**（开发环境而已 —— 产物是静态单文件，用户那边仍然零依赖）：本机 2026-10-04 用
`winget install --id GoLang.Go -e --source winget` 装好 **go1.27.0**，在 `C:\Program Files\Go`。
⚠️ 装完后**机器级 PATH 才加进 `Go\bin`，已经开着的终端/会话看不到**（我们这次是用全路径
`C:\Program Files\Go\bin\go.exe` 跑的）—— **新开一个终端**再直接用 `go` / `.\build.ps1`。

```powershell
.\build.ps1        # = go vet + go test + 静态单文件构建
# 或直接跑：
go run .           # 起面板并自动开浏览器
go run . --no-browser --cli "C:\Program Files\ACE Studio\acestudio-cli.exe"
```

源码改动后先 `go fmt ./...`（本仓首次提交前跑过一次，规整了 3 个文件的注释对齐）。

---

## 目录结构

```
main.go                      装配层：找 CLI → 起面板 → 交给 api.go
api.go                       路由：/api/selfcheck · /api/tools · /api/run（本机跨站防护 + 错误码→可执行的下一步）
internal/acecli/             唯一调用层（纪律都在这里）
  run.go                     exec 直传 argv、分别捕获 stdout/stderr、剥 BOM、串行队列、逐条超时、退出码语义、错误信封
  locate.go                  找 CLI：配置 → ACETOOL_CLI → PATH → 注册表(reg.exe) → 常见位置 → 限深扫 %LOCALAPPDATA%
  probe.go                   --version 三元组解析 + M0 自检（永不返回 error：四种例外都是面板上的一行）
  run_test.go                离线可测：测试二进制自带 stub CLI（纪律 §10-4）
internal/tools/              ★ 工具注册表 —— SV 脚本移植就落在这里
  registry.go                Tool / Param / Call 类型 + 分类排序；表单 schema 直接序列化给前端
  inspect.go                 只读巡检 9 个工具
  color.go                   轨道颜色循环（第一个写工具 = 写路径冒烟测试）
  find.go                    查找：find.notes（读）+ find.goto / find.select（写）—— 复刻 [SV2]查找.js
  find_match.go              22 个音符属性的匹配（6 条能实现 / 16 条明确拒绝并说明 ACE 缺什么）
  lyric.go                   歌词：lyric.pages（读）+ lyric.setSentence / lyric.goto（写）—— 复刻 [SV2]歌词侧栏.js
  noteops.go                 音符/歌词编辑：自动拼接 · 按播放头选中 · 复制/粘贴歌词 · 加减号/删除合并移位（移植 AKD工具侧栏.js）
  tracks.go                  按播放头分割轨道（Plan+Apply：要边跑边读，Build 做不到）
  tones.go                   汉字 → 语调（拼音/声调/调值/走向）；来源可以是文本，也可以是工程里的音符
  pinyin.go                  拼音表装载 + 声调号换算（数字 ⇄ 调号）+ 歌词常用读法小表
  pinyin_table.go            ⚠️ **生成物**（tools/genpinyin.js 从 pinyin-pro 生成），不要手改
  acep.go                    .acep 三件套：inspect（判壳）/ dump（解析）/ patch（回填）
internal/config/config.go    %APPDATA%\ACETool\config.json（CLI 路径 / 超时 / 干跑默认开）
internal/web/                只绑 127.0.0.1 随机端口的本地面板（go:embed，不背 Chromium）
  assets/index.html          工具壳骨架
  assets/style.css           深色主题
  assets/app.js              侧栏 / 表单 / 结果表格 / 命令日志 + 三个专用面板（查找、歌词、变色测试台）
build.ps1                    go vet + go test + 构建（CGO_ENABLED=0，不用 UPX）
tools/checkpanel.js          面板自检（Node + 最小 DOM 桩，不需要浏览器）—— 查找 + 歌词两个面板
tools/genpinyin.js           **生成** internal/tools/pinyin_table.go（汉字读音表，从 pinyin-pro）
```

构建配方（HANDOVER.md §1）：

```
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o acetool.exe .
```

---

## 工具（28 个，侧栏分两栏：**移植脚本 14** · **自建工具 14**）

**数据驱动**：工具清单和表单 schema 都由 `GET /api/tools` 现给；**通用表单**由前端按 schema 生成，
加一个工具 = 在 `internal/tools/` 里加一条 + 写个 `Build` 函数，前端一行都不用改。

⚠️ **例外**：两个工具带**专用面板**（`find.notes` 的查找面板、`color.cycle` 的变色测试台）——
它们的界面是照原脚本/参考脚本**逐条复刻**的，布局有含义，不是 schema 能描述的。
专用面板**仍然从 `/api/tools` 取下拉选项**（`findOpts()`），不在前端重抄一份列表。

📌 **SV 脚本移植就落在这里** —— 一个 SV 脚本 = 一个 `Tool`：
`showCustomDialog` 的 `widgets` → `Tool.Params`；`main()` 里那串 API 调用 → `Tool.Build` 返回的 `[]Call`；
移植来源写进 `Tool.Source`，便于逐条对照原脚本。

| 工具 | 实际跑的命令 |
|---|---|
| 工程总览 | `project info` + `project dirty` + `editor status` |
| 轨道清单 | `track list [--include-empty]` |
| 片段清单 | `clip list --track-index N` |
| 片段内音符 + 歌词 | `clip note-content` + `clip lyrics`（回包带 **fingerprint**，面板单列一行提醒写时要带回来） |
| 速度与拍号 | `tempo get` + `timesig get` |
| 走带 / 标记线 / 选区 | `transport state` + `caret get` + `selection get --scope arrangement` |
| 人声参数分层 | `vocalparam layers --clip-uuid …` |
| 轨道音源 / 声库 | `sound-source get` + `voice get` |
| 撤销栈 | `history list [--limit N]` |
| **轨道颜色循环（写）** | `caret get`（留空跟随 caret 时）→ `track get` 读当前色 → `track set --color <下一格>` →（可选）`track get` **回读核对** |
| **读 .acep 工程文件（只读）** | **不经过 CLI**：判壳（① `ACEP2` / ② 历史壳一 / ③ 历史壳二加密）→ 校验长度字段 → 解 zstd payload → 往返自检 |
| **解析 .acep 内容（只读）** | **不经过 CLI**：① 走自写的 **CBOR 解码器（带字节偏移）**，② 走 JSON；报顶层结构（键 / 各形状计数 / **真轨道按 `type` 分档**）或取某个路径的值 |
| **回填 .acep（写 · 默认干跑）** | **不经过 CLI**：按路径改一处文本 → 重压 → 重建壳 → **两层共 6 条自检** → 只写副本（工程包布局） |
| **查找（只读）** | `editor status` / `caret get` + `clip list` / `track list` → 逐片段 `clip note-content` → **本地过滤**；参数搜索另加 `vocalparam layers`（现读花名册）+ `vocalparam read`；音素属性另加 `phoneme list --clip … --range-begin <t>t`；`timesig list` 算小节.拍 |
| **跳到某一项（写 · 干跑默认）** | `caret set --tick <t>t` →（给了 uuid 才）`selection set --scope editor --mode replace --notes-to-select [{"uuid":…}]` → `caret get --scope arrangement` 回读 |
| **查找并全选（写 · 干跑默认）** | 同「查找」→ `selection set --scope editor --mode replace --notes-to-select [{"uuid":…}]` → `selection get --scope editor` 回读 |
| **变更令牌（只读 · 给自动刷新用）** | `editor status` + `clip lyrics`（取 `fingerprint`）+ `selection get --scope editor`（选中音符哈希）→ 合成一个 `token`，约 225 ms |
| **歌词侧栏（只读）** | `editor status` → `clip lyrics`（ACE 的句：index / lyric / noteUuids / 起止 tick）+ `clip note-content`（逐音符歌词，字符模式要用）+ `selection get --scope editor`（可选）→ **本地分页** |
| **歌词：改一句（写 · 干跑默认）** | 按原脚本规则：`note set-lyric --note-uuid … --lyrics […]` → `clip lyrics` 回读；或 ACE 原生：`lyric fill --clip … --sentence N --text …`（它**自带 `--dry-run`**） |
| **歌词：跳到某一句（写 · 干跑默认）** | `caret set --tick <句起点−lead>t --scope arrangement` → `transport seek --time <同一 tick>t` → `caret get` 回读 |
| **汉字 → 语调（只读 · 纯本地）** | **不经过 CLI**（本地读音表）+ 可选一次 `phoneme g2p --language Chinese --grapheme …`（整串一次拿全）；来源选工程时另加 `editor status` + `clip note-content` + `timesig list` |

⚠️ **只用 `caret`，别用 `marker`** —— 实测这个版本**没有 marker 域**：`marker get` 回 **exit 2**
`unrecognized subcommand 'marker'`（CLI 自己提示 "a similar subcommand exists: 'caret'"），
而 `help marker-line` 那篇 topic 通篇在讲 `marker get/set/seek`/`marker get-focus` ⇒ **文档漂移**，别照抄。

**失败怎么办**：`api.go` 的 `codeHints` 把 `error.code` 翻成**可执行的下一步**
（`NOTE_OVERLAP` → 先把挡路音符移走；`STALE_WRITE` → 重读拿新指纹；`NO_PROJECT_OPEN` → 先开工程 …），
同时把 ACE 自己的 `error.hint` 原样附上。**不翻译成「出错了」**。

**跨站防护**：面板只绑 `127.0.0.1`，但本机其它网页照样能对 localhost 发请求，
所以 `/api/run` 要求自定义头 `X-ACETool` —— 简单表单 / `<img>` 带不上它，浏览器预检我们也不答 CORS。

**接口边界的两条宽容（都踩过，都往"安全方向"收）**：

1. **`values` 宽进**：工具签名只收 `map[string]string`（表单值的自然形状），
   但把 `absTick` 当**数字**发（`{"absTick": 29520}`）会让整个请求被拒，且错误信息里连工具名都没有。
   现在字符串/数字/布尔/null 都归一成字符串（数字**不用科学计数法**，免得 tick 变 `2.952e+04`）；
   只有嵌套对象/数组仍然报错，但会**点名是哪个 key**。
2. **`isTruthy` 的失败方向是危险的** —— 它就是干跑闸门（`values["dryRun"]`）。
   认不出来 ⇒ 当成"没勾" ⇒ **真的去写**。所以肯定式收宽（含 `是/有/开/真`，与 `boolWant` 口径一致），
   否定式也收，**其余一律当假**（宁可多干跑，不可误写）。真机验过
   `true` / `"true"` / `"是"` / `"1"` / `1` 五种写法**全都只出计划、一条都不执行**。

---

## 写路径（已开）

第一个写工具是**轨道颜色循环**（`color.cycle`），它同时就是写路径的冒烟测试，
照参考脚本 `..\acetool\scratch\ace-color-cycle.ps1`（`-Mode next` / `-Loop`）那一套做的。

**三道闸门**

1. **干跑闸门在 runner 里**，不是每个工具自己实现：写工具勾了「干跑」⇒ `/api/run` **一条都不执行**，
   只把算好的调用当 `plan` 回给界面。注意**读探针照做** —— 计划本身就需要当前颜色
   （参考脚本的 dry run 同样只读不写）。
2. **「读 → 决定 → 写」是一等形状**：`Tool.Build` 的签名是
   `func(ctx, rd ReadFunc, vals) ([]Call, error)`，`rd` 是只读探针（自动带 `--json`）。
   这是 SV 脚本的主导形状，所以框架层直接给了读口子；
   **写口子只有返回的 `[]Call` 一条**，必然经过干跑闸门。
3. **写后回读核对是可选项，且默认关**（照参考脚本的 `-NoReadBack`）：`track set` 成功即认为落地，
   省掉一次往返。勾上才追加一条 `track get`，并把实际落地的颜色读回来；
   对不上就记一条 WARN 进命令日志（照参考脚本的 `WARN asked X, read back Y` 分支）。
   ⚠️ 关掉回读时，面板上的色块与状态显示的是**我们写出去的那个色**（不是读回来的）——
   代码里就是 `back || want`，别指望它证明落地。

**面板上的那颗按钮**：选「轨道颜色循环（写）」→ `▶ 开始变色` / `⏸ 暂停`（再按一下停）。
循环是**客户端自调度**的，不是 `setInterval`：一步 = 一次 `/api/run`，干完看耗时，
再 `setTimeout(max(0, 间隔 - 耗时))` ⇒ **绝不并发叠加**。硬上限 400 步（照参考脚本的 `-MaxWrites`），
停下时报撤销代价。旁边有实时色块 + `xxx ms/步` + 回读不符计数。

**实测节奏**（2026-10-04 · 本机 · 含 HTTP 往返）：

| 每步的调用 | 命令数/步 | min | avg | max |
|---|---|---|---|---|
| **第 2 步起（现在默认）** | **1 条** | 68 | **73 ms** | 78 ms |
| 关回读、但每步仍读当前色 | 2 条 | 140 | 149 ms | 162 ms |
| 最初：+ 写后回读 | 3 条 | 218 | 229 ms | 245 ms |

**第 1 步是「冷启动」**（283 ms）：`caret get`（目标轨道）+ `track get`（当前色）+ `track set`。
**之后每步只有一条 `track set`** —— 循环把第 1 步侦察到的轨道索引和**上一步写出去的色**传回来，
于是既不用问 caret、也不用 `track get`。三条优化合起来把单步从 229 ms 压到 **73 ms（约 3.1×）**。

⇒ 默认间隔 200 ms 现在**绰绰有余**（单步只占 73 ms），要更快可以把面板上那个间隔往下调
（输入框下限 120 ms，单步 73 ms 还留有余量）。**间隔是下限、不是保证** —— 看面板上那行 `ms/步`。

⚠️ **代价（已写进工具说明）**：`fromHex` 是**信任传进来的值**，不再核对真实颜色。
外部（你在 ACE 里手改颜色）若同时改了色，循环会从你给的那个值继续往下走 —— 会错位但不会报错。
要真值就把「写后回读核对」勾上（那样每步回到 2 条命令）。

⚠️ **撤销代价**：ACE 没有事务 API（`help interaction-model`），**每写一步 = 一条撤销记录**。
真机验证：撤销栈里出现 N 条 `更改轨道颜色`、`actor='远程控制'`。停下后要 Ctrl+Z 那么多次才全清。
这条在工具说明里和按钮旁边都写了。

---

## 文件路线（`.acep`）

技能 `acep` §0 的纪律：**CLI 能做的永远走 CLI** —— CLI 有撤销栈、指纹、原子拒写，**文件改三样都没有**。
文件路线只用在「CLI 没开放的能力」上，当前已知唯一一个是**人声音高曲线**（`vocalparam` 里 `pitch` 恒 `available:false`）。

第一个文件工具是 `acep.inspect`（**只读**：判壳 → 校验长度 → 解 payload → 往返自检）。

### 三种壳（技能 acep **v1.2.3** §1.0）

| 壳 | 判据 | 结构 | 我们的口径 |
|---|---|---|---|
| ① **当前格式** | 首字节 `ACEP2` | 1280 B 头 + 裸 zstd 帧；payload 是 **CBOR**（顶层 map） | **完整支持** |
| ② **历史壳一** | 有 `compressMethod` | 包装 JSON：`content` = base64 的 zstd 帧，解出 **JSON 文本** | **可读**；改法走 `set-json`（整树改 → 重 zstd → 重 base64），**不是** ① 那套原位覆盖 |
| ③ **历史壳二** | **没有** `compressMethod` | 加密（zstd/gzip/deflate/brotli 全解不开，熵 7.978 bit/B） | **只认出，不尝试解密** |

⚠️ **②③的判据是"有没有 `compressMethod`"，不是版本号** —— 技能明确：`version:2` 只是③唯一样本的特征。

**真机分布**（2026-10-04 扫本机 40+ 个 `.acep`）：ACE 2.1.8 今天存的**全是 ①**；
2023-08～2026-02 的（`ace.acep` / `senpa.acep` / 腾讯收到的 / FL Studio 目录下的）**全是 ②**（跑得通、往返逐字节一致）；
`%LOCALAPPDATA%\Timedomain\ACE Studio\project\<hash>\` 下的临时工程缓存是 **③**。
⚠️ **`.acep` 不是 ACE 独占的扩展名** —— `Image-Line\FL Studio\...` 下也有一堆，实测是 ②（逐字节核过，不是误判）。

### zstd 依赖的代价（全部实测）

| 项 | 实测 |
|---|---|
| 只用**解码器** | `acetool.exe` 7.12 → **7.36 MB**（+0.24 MB） |
| **编解码都要**（写路线必需） | → **7.58 MB**（+0.46 MB） |
| 依赖本身 | `github.com/klauspost/compress v1.20.1` —— **纯 Go、无 CGO、无外部依赖** ⇒ 产物仍是零运行时依赖的单文件 |
| 拉依赖 | ⚠️ `proxy.golang.org` 在本机**超时**，必须 `GOPROXY=https://goproxy.cn,direct` |
| go.mod | 被提到 **`go 1.25`**（该库要求）⇒ 构建机要 Go ≥1.25 |

⇒ **加得起**（+0.46 MB，仍在 <10 MB 内）。代价全在**构建侧**（要能出网、要配镜像、Go ≥1.25），产物侧零代价。

### 踩到并记下的两个坑

1. **CBOR 头别看一个字节**：ACEP2 探针工程的顶层是 `b8 1a` —— `0xb8` 表示"map，长度写在紧跟的 1 字节里"
   （26 键）；`0xba` 是"长度跟在后面 4 字节"，**不是 map(26)**。我第一版就按 `0xba` 写死了，报成"不认识的编码"。
   另外 JSON 的 `{`（0x7b）与 CBOR 的 text(27 字节) 首字节**完全相同** ⇒ 要靠"头 96 字节是否可打印"区分。
2. **PowerShell 5.1 发 JSON body 会把中文变成 `?`** ⇒ 用命令行探面板时要**显式发 UTF-8 字节**
   （`-Body ([Text.Encoding]::UTF8.GetBytes($json))`），否则会把"客户端编码问题"误判成"工具读不了中文路径"。
   浏览器 `fetch` + `JSON.stringify` 没这个问题（显式 UTF-8）。

---

## 解析与回填（`.acep`）：能做什么、边界在哪

三个工具一条链：**`acep.inspect`（判壳）→ `acep.dump`（解析）→ `acep.patch`（回填 · 默认干跑）**。

### 解析：自写的 CBOR 解码器（带字节偏移）

**为什么不引通用库**：ACEP2 的写法是**原位覆盖**（技能 acep §2.3 / §4）—— 文本项只改自己的长度前缀、
float64 数组只要元素个数不变就逐值覆盖。那需要**每个节点的字节范围**，通用库一般只给值不给偏移。

技能 §2.3 记的两个坑都有**专门用例**卡死：

1. **half float（`0xf9`）必须消费 2 字节** —— 漏了后面整段错位；
2. **float64（`0xfb`）只能消费 8 字节** —— 多读一次就整段错位（用"每个值偏移严格 +9"卡）。

另外整数键也认（`phonemeTimings` 的键就是 `0`/`1`，别假定键都是文本），不定长容器 / tag / 负数 / null 都能解。
**解码必须恰好消费完所有字节** —— 这条本身就是技能 §4 第 ⑥ 步的自检之一。

### 回填：两条完全不同的路（按壳分）

| 壳 | 改法 | 依据 |
|---|---|---|
| ① `ACEP2` | **CBOR 原位覆盖**：文本换长换短只改它自己的长度前缀，外层容器一个字节都不动 | 技能 §2.3（map/array 长度是**元素个数**，不是字节数） |
| ② 历史壳一 | **set-json**：整树解析 → 改路径 → 重 stringify → 重 zstd → 重 base64 → **只换 content** | 技能 §1.0 |
| ③ 历史壳二 | **不碰**（加密） | 技能 §1.0 |

⚠️ 历史壳一走 set-json 会**重排键序、归一空白**（内层本就是 JSON 文本，ACE 读的是值不是格式）——
这条在工具的说明里写明了，不藏着。

### 两层自检（`acep.patch` 每次都给这 6 条）

1. 新 payload **整段可解**（恰好消费完）
2. **目标路径回读** == 想要的值
3. **重建容器**成功（报大小变化）
4. 重建出来的文件**按同一条路径再解一遍**，payload 逐字节一致
5. **头部除 off16/off24 之外与源文件逐字节相同**（含那条 1248 B 高熵区）← 技能 §1.1 的强断言，自动验
6. 文件大小 == 头长 + 压缩长

**自检有任何一条没过 ⇒ 拒绝落盘**（不是警告，是拒绝）。

### 落盘：只写副本，而且成"包"

```
<输出目录>\<名字>\<名字>.acep                        ← 改过的（源文件一个字节都没动）
<输出目录>\<名字>\autosave\<名字>_save_<时间戳>.acep   ← **改动前**原文件的快照
<输出目录>\<名字>\Samples\                            ← 空目录
```
⚠️ 必须成"包"：技能 §5.2 实测 —— 只放一个 `.acep` 的文件夹会被 ACE 当成"无名临时工程"
（`projectName:""`、`projectPath:""`）。目标已存在时**拒绝覆盖**。

### 实测（2026-10-04 · `acep-probe\A\A.acep`，真工程）

```
解析：ACEP2 · payload 17686 · CBOR map(26 键)（b8 1a）
      tracks array(100 项) —— 按 type 分档：empty 99 / sing 1      ← 正是技能 §2.5 那条提醒
      tracks[0].patterns[0].notes[0].syllable = "l an"             ← 正是技能 §13.3 那个合法覆盖样本
回填：tracks[0].name  "" → "ACETool 测试轨"
      payload 17686 → 17703 · 文件 5500 → 5530 · **6 条自检全过**
      干跑：源文件哈希不变、输出目录都没建（真的一个字节没写）
      真写：源文件 SHA256 不变 ✓ · 产物逐字段对照只有 name 变了（color/syllable/lyric/pos/version 全同）
```

### 明确**不做**的

- ❌ **加/删音符、加/删采样点** —— 那要**整段重编码** CBOR，是另一条路（技能 §6 也划在 `acep.cjs` 能力之外）
- ❌ 历史壳二的解密
- ❌ 碰源文件 / 原地改用户正开着的工程
- ⚠️ **"ACE 认不认"是第三层，本工具证明不了** —— 文件级自检只证明字节写对了。
  要让 ACE 读回来复核（`project open` 或手工打开产物）。

---

## 查找（从 SV 脚本 `[SV2]查找.js` 移植）

原脚本是 SV2 的 SidePanelSection（656 行）：按「**属性/参数 + 条件 + 值**」在「**当前组 / 当前轨 / 整个工程**」
里查音符或参数，结果按位置排序，可一键全选。移植成**三个工具**（左栏「查找」分类），
外加一个**逐条复刻原脚本面板**的专用 UI：`find.notes`（只读）+ `find.goto` / `find.select`（写 · 干跑默认）。

> 用户 2026-10-04 的裁定：**完全参照原脚本**（面板行顺序、默认值、联动、文本格式都照抄），
> **只写中文、不建翻译表**（原脚本带 ja-jp / zh-cn / zh-tw 三套字面，这里只留 zh-cn）。

### 面板：一行一行对着原脚本抄

| 原脚本（`getSidePanelSectionState`） | 面板里 | 备注 |
|---|---|---|
| `TextArea` height 100 | 只读 `<textarea id=fpinfo>` | 结果文本，格式见下表 |
| Button「全选目标音符」(全宽) | 同 | **范围≠当前片段时禁用**（原脚本 `selectButton.setEnabled(isGroup)`） |
| Button「上一项」「下一项」 | 同 | 两端禁用（原脚本 `lastButton/nextButton.setEnabled`） |
| Button「刷新」「说明」 | 同 | 「说明」= 原脚本 zh-cn 的 `HelpText` 原文 |
| `TextArea` height 20 | `<input id=fpcontent>` | 原脚本是 `setValueChangeCallback` ⇒ 用 `change` 触发（每次查找 300–450 ms，逐键触发会排队） |
| ComboBox 大于/小于/等于/包含 | 同 | 默认「等于」（`condition.setValue(2)`） |
| ComboBox 当前片段/轨道/工程 | 同 | 默认「当前片段」（ACE 的词是**片段** = clip；SV 那边叫音符组） |
| CheckBox 音符属性 \| 参数 | 同 | 互斥，默认勾「音符属性」；点一个就切 `type` 并立刻查 |
| ComboBox 22 属性 \| 9 参数 | 22 属性 \| **13** 参数 | 参数多的 4 条是 **ACE 独有（★）** |
| ——（原脚本没有） | **参数层** | 我们加的：ACE 参数分层，必须说是哪一层（见下） |

**结果文本**（`resultInfo()` 逐行复刻）：
`序号: n/N` → 范围=**工程**才给 `轨道:` → 范围≠当前片段才给 `片段:` → 音符属性才给 `音符序号:` → `位置:` → 有值才给 `数值:`

两处容易抄错、已经用测试钉住的细节：

1. **`位置:` 用的是 `b2p()`，不是"小节.拍.tick"** —— 原脚本是
   `measure.position + denominator*(blick - positionBlick)/(4*numerator*QUARTER) + 1` 再 `toFixed(2)`
   ⇒ 是个**两位小数的"小节.拍内比例"**（4/4 里 `1.25` = 第 1 小节第 2 拍；tick 29040 → `16.13`）。
   我们**两种都留**：`posSv` 给面板显示（忠实），`barBeat`（`16.1.240`）给人精确定位。
   有专门用例卡住"两者不相等"，防止将来有人"顺手统一"。
2. **起始项不是第 1 项** —— 原脚本 search() 结尾按**标记线**算初始下标：
   片段范围 `index = 标记线之前的命中数`（往后找），轨道/工程范围 `index = 标记线之后的命中数`
   （于是 `displayNum = res.length - index` 是**倒着数**的），最后 `if (index == res.length) index--`。
   这条也照抄进后端（`startIndexFor`），不是前端另算一遍。
3. **`序号` 在两个数量级上不一样**：片段范围正着数（`index+1`），轨道/工程倒着数（`N-index`）。

### 属性：**只留 ACE 真能做**的（18 条）

用户 2026-10-04 裁定（两条连着看才完整）：
① 参数「**只留 ace 存在的**」；② 属性同理 ——「**把 ace 没有的 key 去掉**」。

| 类别 | 条数 | 明细 |
|---|---|---|
| 原脚本 22 条里 **ACE 能做**的 | **6** | 持续时间（拍）`dur/480` · 歌词 `lyric` · 音素（实际发音）`phonemes` · 音素（默认发音）`defaultPhonemes` · 音高 `pitch` · 语言 `language` |
| **ACE 独有**（★，原脚本没有） | **12** | 音素覆盖(有/无) · 头辅音长度(秒) · 尾辅音长度(秒) · 时长(tick) · 结束位置(tick) · 片段类型 · 片段启用(有/无) · **有换气(有/无)** · **换气请求长度(秒)** · **换气实际生效(秒)** · **换气被压掉(有/无)** · **可插换气空间(秒)** |
| 原脚本 22 条里 ACE **没有**的 | **16（不在下拉里）** | 音分 1 · 说唱 3 · 颤音调制 1 · 音高过渡 5 · 颤音 6 |

**换气算音符属性**（用户裁定「换气算在音符里吧」）⇒ 换气那 5 项就在**属性下拉**里，不另开工具。

⛔ 但**"不做静默失败"这条纪律不变**：那 16 条的说明**仍然留着**（`unsupportedAttr`）——
直接拿 API 调 `attr=detune` 照样得到**带中文名 + 原因的明确拒绝**
（`「音分」在 ACE 里没有对应概念：…`），不会退化成"0 命中"或"属性认不出"。
`attrLabel` 会在下拉查不到时**退到原脚本 22 条全集**去取中文名，所以话术里不会出现裸英文 key。

两条方向相反的**不变量测试**一起钉住这件事：

- `TestEveryAttrOptionIsAnswered` —— 下拉里**每一条都必须真能用**（一条拒绝的都不许在里面），
  且条数/顺序 = 6 + 12；同时断言那 16 条**不在下拉里**却**仍被明确拒绝**。
- `TestUnsupportedSetIsExactComplement` —— 拒绝表的键**正好**是「原脚本 22 − 能实现的 6 = 16」，
  多一条少一条都算漂移。

**照搬过来的两处巧思**（原脚本的精华，别丢）：
1. **音高输入法**：MIDI 数字 · 音名 `C4`/`C#4`/`Db4` · **音级 `C`（匹配所有八度）** · **黑 / 白**（黑白键）。
2. **语言关键字**：中/普/汉 · 英 · 日 · 粤/广 · 西 · 韩/朝 → ACE 的语言全名（`Chinese`…）。

### 参数：只留 ACE 侧**真实存在**的（6 条）

| 参数 | ACE id | | 参数 | ACE id |
|---|---|---|---|---|
| 张力 | `tension` | | ★ 假声 | `falsetto` |
| 气声（Air） | `air` | | ★ 音色 | `formant` |
| ★ 能量 | `energy` | | ★ 整体动态（ACE 2） | `dynamic` |

原脚本那 9 条里有 **7 条 ACE 根本没有**（音高偏差/颤音包络/响度/发声/性别/音区偏移/口型）
⇒ 已从下拉里摘掉，但 **`paramMap` 里仍然留着**：拿 API 直接调它们照样得到**带原因的明确拒绝**。

⛔ **可用性一律现读 `vocalparam layers`，绝不按名字猜**（技能 `ace-params` §1 的硬纪律）：
参数花名册**随声库的引擎世代变**。本次会话**真机上就换过一次代**（下面 18.7 那条），
所以这里三道门都会**给可执行的下一步**而不是空结果：
参数不在花名册（列出**实际有的**+`engineGeneration`）/ `available:false`（附官方 `unavailableReason` 原文）/
这一层不存在（列出**实际有的层**）。

**参数层默认是 `auto`**（⚠️ 不是某个具体层名）：
`auto` 会认这一代引擎的「你画过的点」层 —— **v1（`verse24`）是 `direct`、v2（`singing-mamba`）是 `user`**，
两者互相都没有对方那个；解出来还会在报告里**说明用了哪一层**（`layerUsed` / `layerNote`）。
解不出来就照「没有可用层」拒绝，**绝不静默换一个错的层**。
显式指定具名层时**严格按用户说的来**（v2 上要 `direct` 就如实拒绝，不偷偷换成 `user`）。

**参数命中的是"段"不是一个点**：ACE 的参数是**每 tick 一个值**的 dense 数组，
所以连续命中的 tick 会被压成一段（报告里给 `runLenTicks` + 该段的值域 `min~max`）。

### 三级范围怎么落到 ACE

| 原脚本 | ACE 对应物 |
|---|---|
| 当前组 | `editor status` → 当前打开的片段（**编辑器跟着标记线走**，所以这就是"标记线所在的片段"） |
| 当前轨 | `caret get --scope arrangement` → `clip list --track-index N` |
| 整个工程 | `track list` → 逐轨 `clip list`（只扫 Sing / Instrument / GenericMidi，跳过 Audio/Chord/空槽） |

### 速度：这里和 SV 有本质差别

SV 在内存里遍历；ACE **每片段一次 `clip note-content`**，而且**串行**（只有一个 host）
⇒ 每片段约 **75 ms**。实测：当前片段（26 音符）**234 ms** · 当前轨（2 片段 27 音符）**497 ms** ·
整个工程（4 片段 36 音符）**769 ms**。带音素（额外一次 `phoneme list`）约 **+150 ms**。
所以有片段数/命中数上限，报告里**如实给片段数、音符数、耗时**，截断会明说。

### 踩到并修掉的四处

1. **`--notes-to-select` 要的是 `[{"uuid":"{…}"}]`，不是 uuid 字符串数组** —— 传字符串回
   `INVALID_ARG: field 'notesToSelect': expected object for NoteRef`（原子失败，什么都没改）。
   权威在 `help selection set`。
2. **面板原来不渲染 `plan`** —— CLI 写工具干跑时回包**只有 `plan`、`calls` 是空的**，
   而界面只渲染 `calls` ⇒ 显示"0 条命令全部成功"，**看不到将要执行什么**。现在干跑会画一张"将执行的命令"卡。
3. **`phoneme list` 拒绝"整片段"这种无参形式** —— 回 `INVALID_ARG`，官方 hint：
   *"There is no clip-wide form: a whole clip is hundreds of rows, and `clip note-content` is the read that enumerates one."*
   合法的最小形式是 `--clip <UUID>` 配 **`--range-begin` 或 `--range-end`**（另一个默认到片段端点），
   且 `--range-scope` **默认 `project`**（全局时间轴）⇒ 给的是**绝对 tick**。实测一次拿全 25 个音符。
   ⚠️ 另一个坑：`--range-begin` 是 `TIME_VALUE`，**必须带单位**（`29040t`），裸数字会被 `TIME_UNIT_REQUIRED` 拒。
4. **语言属性两边都要过关键字表** —— 这个是**真机才暴露的 bug**：ACE 报完整英文名（`English`），
   用户填中文单字（`英`），我第一版**只映了一边** ⇒ `语言=英` 永远 **0 命中**（正是本工具最危险的失败方式）。
   修完真机复核：`英` → 18 条、`中` → 7 条，18+7 = 25 = 该片段全部音符。补了专门的用例。

### 另外两条真机更正（之前记错了）

- **`editor status` 其实给 `clipUuid`**（还有 `trackUuid` / `region` / `defaultLanguage` / `supportedLanguages`），
  但**不给 `clipBegin` / `clipEnd`**。绝对位置（排序、跳转）和音素的 `--range-begin` 都要 `clipBegin`
  ⇒ 仍然要补一次 `clip list`，理由和之前记的不一样了。
- **`clip note-content` 里没有 `articulation` 键**（实测全文搜过）⇒ 之前"ACE 独有属性：技法"那条**是错的**，
  已从列表里去掉。ACE 的技法要在别的地方读（Instrument 片段的技法接口还没摸）。

### 面板自检（不需要浏览器）

`tools/checkpanel.js` 把 `assets/app.js` 放进一个**最小 DOM 桩**里跑，然后：

1. 调 `findPanelHTML()` 收集生成的 id，再调 `bindFind()` 收集它去取的 id —— **两边对账**
   （面板是手写 HTML 字符串，id 拼错在浏览器里只表现为"按钮没反应"，这条专门抓它）
2. 核对**复刻原脚本的默认值**（`无内容` / 等于 / 当前片段 / 音符属性）
3. 逐种范围×目标组合跑 `resultInfo()`，核对**文本格式**（正着数/倒着数、哪几行该有哪几行不该有）
4. 核对**按钮可用性**（两端禁用、范围≠当前片段时"全选"禁用）

⚠️ 顶层 `const`/`let` 落在 realm 的**全局词法环境**里、不是 `globalThis` 的属性 ⇒
探针脚本不能写 `ctx.find`，得在**同一个 realm** 里再 `runInContext` 一段脚本来读它们（已记在脚本注释里）。

**真机验收**（`find.select` / `find.goto` 真写入，不只干跑）：

```
全选 18 个命中（范围=当前片段 · 语言=英）
  → selection set --scope editor --mode replace --notes-to-select […18 个 uuid…]
  → {"notesSelected":18,"notesNotFound":0,"selectionCount":18,"success":true}
  → 回读 selection get --scope editor：选中 18 个 ✓
跳到第 2 个音符（tick 29520）
  → caret set --tick 29520t → selection set（1 个）→ 回读 {"tick":29520,"trackIndex":4,…} ✓
```

---

## 歌词（从 SV 脚本 `[SV2]歌词侧栏.js` 移植）

原脚本是 SV2 的 SidePanelSection（299 行）：把当前片段按**句**切开，一页 12 句显示成
12 个可编辑文本框；改哪个框就把那句话重新分配到该句的音符上；另有 上一页/下一页、
「分割成字符」开关、以及一个「跳到某句」的下拉（移视图 + seek 播放头）。

移植成**三个工具** + 一个专用面板：`lyric.pages`（读）+ `lyric.setSentence` / `lyric.goto`（写 · 干跑默认）。

### ⛔ 移植时改了一件根上的事：**句的划分以 ACE 为准**

原脚本**自己算分句**（看相邻音符有没有缺口、有没有 `br`）。但 ACE **有自己的句模型**：
`clip lyrics` 直接报 `sentences[]`（`index` / `lyric` / `noteUuids` / 起止 tick），
而且**写回原语就是按它的下标寻址的** —— `lyric fill --sentence <N>`。

⇒ 要是沿用原脚本那套启发式分句，`--sentence N` 会指到**别的句子**上去。
真实例：4 个音符、1440→1620 有个 180 tick 的小缺口 —— 原脚本的启发式会切成 **2 句**，
ACE 认为 **1 句**。以 ACE 为准才对得上它的面板与写回。

### 面板：**和 JS 同 UI**（逐行照 `getSidePanelSectionState()`）

| 原脚本的行 | 面板里 |
|---|---|
| ① `Container[TextArea h60 readOnly]` | 只读 `<textarea>`，内容 `共N页 M句歌词 / 当前为第P页 / 第A到B句` |
| ② `Container[CheckBox "Separate into Characters"]` | 同名复选框，**紧跟信息框**（中间不夹任何控件） |
| ③ `Container[TextArea h20]` × pageRowCount | 一行一个 `<textarea data-sentence=…>`，**行内没有按钮、没有元信息行** |
| ④ `Container[Button 上一页 width 1.0]` | **整行**按钮 |
| ⑤ `Container[Button 下一页 width 1.0]` | **整行**按钮（④⑤ 各自一个 Container ⇒ **竖排**，不是并排） |
| ⑥ `Container[ComboBox choices=本页各句]` | `<select>`，本页各句，选中即跳 |

行序由面板自检卡住（`checkpanel.js` 一条断言按顺序找这 8 个 id 的位置）。

**四处有意的偏离**（都写在代码注释里，不藏着）：

1. ⚠️ 原脚本把行文本框声明成 `readOnly: true`，**却又给它们挂了 `setValueChangeCallback`**
   —— 自相矛盾（真 readOnly 的话回调永远不会触发）。**以它的行为为准**：文本框可编辑、
   **失焦/回车即写**（就是那个回调的触发时机）。
   我们加了一道「**真的改了才写**」的闸：用 Tab 把焦点过一遍不会重写歌词、也不会刷 ACE 的撤销栈。
2. **复选框标签**：原脚本写的是 `SV.T("Separate into Characters")`，而它**没有翻译表**
   ⇒ 在 SV 里显示的就是这串英文原文。按本仓「只写中文」的口径用了「分割成字符」。
3. **没有范围控件**：原脚本的范围是**自动**的（`Lyrics()` 里"有选中的音符就只算选中的，
   否则算整个片段"）⇒ 后端 `scope` 默认 `auto` 复刻这个语义，面板上就不放原脚本没有的下拉。
   要强制整段可以在通用表单里显式选 `clip`。
4. **「刷新」放在面板外面**：原脚本靠 `registerSelectionCallback` **被动**重读，ACE 没有这种推送，
   所以得手动重读 —— 但它不该出现在被复刻的那 6 行里。

**一处 off-by-one 没照搬**：原脚本的跳句下拉取的是**下一句**（选项 0 起、SV 的 ComboBox 值 1 起
⇒ `group[page*12+idx]` 差一位）。这里按**你选中的那一句**跳。

### 自动刷新：**宿主一改就刷新**（ACE 没有推送，只能轮询）

SV 脚本靠**推送**被动重读（`registerSelectionCallback` / `registerClearCallback`），
**ACE 没有任何推送** ⇒ 要这个效果只能轮询一个便宜的变更令牌。

`inspect.revision`（只读）把三样东西合成一个 `token`：

| 读点 | 变了说明什么 |
|---|---|
| `editor status` | 标记线移到了**另一个片段**（面板该显示别的东西了） |
| `clip lyrics` 的 `fingerprint` | 片段**内容**变了（域实测是 `clip-note-content`，覆盖歌词） |
| `selection get --scope editor` 的**音符 uuid 哈希** | **选中变了** |

⚠️ 第三项用**哈希**而不是 `selectionCount`：**换一个音符选，count 不变**，但
`scope=auto` 下要显示的那一句可能就换了 —— 只比 count 会漏掉这种情况。哈希**与顺序无关**
（换着顺序点同一批音符不算"变了"，否则会一直误报）。有专门用例卡这两条。

**成本**：3 次读、实测 **221~256 ms**。关键是**每次读都约 65 ms，跟载荷大小几乎无关**
（成本主要是 CLI 进程启动）—— 所以 329 个音符的大片段也是这个量级。

**四道守卫**（缺一个都会出事，面板自检逐条卡着）：

1. **正在编辑就不动**：任何歌词文本框有焦点、或内容跟读到的基线不一致 ⇒ 跳过这一轮。
   否则你打字打到一半，刷新会把输入覆盖掉。
2. **正在写就不动**：写请求在飞时跳过（写完本来就会自己重读）。
3. **标签页看不见就不动**：`document.hidden` ⇒ 跳过（切到 ACE 去干活时最该省）。
4. **只在这个页面轮询**：换工具就 `pollStop()`。

另外两条：轮询**必须安静**（失败不清面板、不弹红条，只在状态行记一笔 —— ACE 忙的时候读会失败，
那是正常的）；**轮询回来时页面可能已经切走了**，收尾要再确认一次 `poll.on` 与面板还在，
否则 `.innerHTML` 会打在已经不在 DOM 的节点上（这个坑是写完守卫才想起来的）。

控件在**面板外面**（和「刷新」一排）：`☑ 宿主一改就自动刷新` · `间隔 [2500] ms` · 状态行
（显示"已跟上宿主的改动 / 宿主变了 → 已重读 / 暂停（正在编辑）" + 刷新次数 + 读失败次数）。
间隔下限 1 s（一次轮询本身约 225 ms）。

**真机验收**：

```
连读两次什么都不改      → token 一致 ✅（不会误报变化、不会白重读）
选 brl2  → 选中=1  token 尾 …|1|dcae0671
选 shei  → 选中=1  token 尾 …|1|82a87728   ← **同样 1 个选中、换了音符，token 变了** ✅
再选回 brl2            → token 尾 …|1|dcae0671  ← 与之前一致（可复现、不抖动）✅
清空选中               → 选中=0  token 尾 …|0|-  ✅
静态页面选一个音符     → 歌词页范围自动变 selection（只剩含它的那句）✅
```


### 两种写法的**语义不一样**（默认选了不破坏数据的那种）

| 写法 | 命令 | 词比音符少时 |
|---|---|---|
| **「按原脚本规则」（默认）** | `note set-lyric --note-uuid … --lyrics […]` | 按 `o % 词数` **循环铺满**，一个音符都不会空着 |
| 「ACE 原生填充」 | `lyric fill --sentence N --text …`（一句一条命令，**自带 `--dry-run`**） | ⚠️ **没覆盖到的音符会被清空** |

实测（4 个音符的句子，填 `ha ppy`）：

```
perNote（默认）：ha | ppy | ha | ppy     ← 循环铺满，与原脚本一致
fill（原生）  ：ha | ppy |  |            ← 后两个被清空（ACE 乐句框就是这个行为）
```

⇒ 所以**默认是 `perNote`**：既忠于原脚本，又不会因为少写两个词就把歌词抹掉。
`fill` 留着（它是 ACE 自己的行为、一句一条命令），但标签上直接写了"⚠️ 没覆盖到的音符会被清空"。

### 照搬过来的两处巧思（别丢）

1. **「分割成字符」模式**：每个音符一个字，**长度 > 1 的词套括号**（`(b i y)`），
   写回时括号里整段折叠到**一个**音符上 —— 这样"一个音符唱一整词"才有写法。
   ⚠️ 括号折叠**只在字符模式下成立**：按词切时 `(` 不是独立的 token，`(b i y)` 会被当成 3 个词
   （原脚本就是这样，照实记下来）。
2. **`o % 词数` 的循环分配**（见上）。

### 没有照搬的两处毛病

1. ⛔ **原脚本有个死循环**：字符模式下 `(` 没闭合时，`while (Lyr1[n] != ")")` 会无限拼 `undefined`
   （`undefined != ")"` 永远成立）。这里**明确报错**「括号没闭合」，有专门用例卡住（带 3 秒超时）。
2. **空歌词时原脚本把 `page` 钳到 `-1`**，信息行变成「共0页…第-11到0句」。这里钳到 0，
   并给一句「这一份片段里没有歌词」的人话。

### 一条真正重要的不变量（有用例）

**面板显示的那一行，原样解析回来必须得到同一串逐音符歌词。**
把"显示"与"写回"钉在一起：用户看到 `ha ppy`、原样提交（没改），逐音符结果必须不变。
8 组用例覆盖中英混排、单字母、多字词、延长号 `-`。真机也复核过：把显示文本喂回去，
干跑出来的 `--lyrics` 与现状**逐字一致**。

### 真机验收（2026-10-04）

```
lyric.pages   → 句 0「a b c d e f g h i j k」11 音 · 句 1「m l o p … w#1 w#2 w#3 x」14 音
               信息行「共1页2句歌词 / 当前为第1页 / 第1到2句」
               勾「分割成字符」→「abcdefghijk」/「mlopqrstuv(w#1)(w#2)(w#3)x」 ← 多字词套括号
scope=auto    → 没有选中时 = clip（2 句）；选中 1 个音符后 = selection（只剩含它的那 1 句）✓
               显式 scope=clip 仍然给整段（可覆盖自动行为）✓
perNote 真写  → ha | ppy | ha | ppy   （循环铺满、无空音符）
fill   真写   → ha | ppy |  |         （后两个被清空 —— 所以它不是默认）
还原          → banana#1 | banana#2 | banana#3 | 啦   ✓
lyric.goto    → caret 28320t + transport seek 28320t → 回读 caret tick=28320 ✓
```

**规模**：`exe` 7.86 MB · Go 测试 **89 条**（tools 40）· 面板自检 **60 条**。


---

---

## 汉字 → 语调（`lyric.tones`）

**备用工具**：把一个字变成它的**拼音 / 声调 / 调值 / 音高走向**。来源三选一 ——
直接给文本、**当前片段的歌词**、或**选中的音符**（后两者顺带带上音符位置与音高走向）。

### 为什么是纯本地的

先确认了 ACE 侧**没有语调面**（这一步不能省，否则会去接一个不存在的东西）：

- `help --search intonation` → **零匹配**；
- `help --search tone/rap/accent` 命中的都是无关的东西（chord "tone"、grapheme…）；
- `phoneme g2p`（能汉字→音素）**只给音素、不给声调**，而且中文 `contextSupported: false`
  —— 不做上下文消歧。

所以声调只能本地查表。反过来 `phoneme g2p` 补的是**另一半**：**ACE 实际会唱什么音素**。
两个合起来才是完整的"这个字唱成什么"——真机上立刻显出差别：

```
谁  shuí(2)     但 ACE 唱 shei
云  yún(2)      但 ACE 记作 yvn      （ACE 的音素记法用 v 表示 ü）
燕  yàn(4)      但 ACE 记作 yian
```

### 表是**生成**的（`tools/genpinyin.js`）

数据源是本机已有的 **pinyin-pro 3.29.4** —— 也就是 SV 侧技能里已经在用的那个库
（skills 里写着"拼音/声调来自 pinyin-pro"），口径一致。生成物 `pinyin_table.go`：

| 项 | 值 |
|---|---|
| 覆盖 | U+4E00..U+9FFF **连续**（下标 = `r - 0x4E00`） |
| 规模 | 20,992 项，其中 **136 个码位没有读音**（空串）· **3,215 个多音字** |
| 数据量 | **123 KB**（分块存，避免出现 150 KB 的巨行） |
| 产物代价 | `exe` 7.86 → **8.02 MB**（+160 KB） |
| 重新生成 | `node tools/genpinyin.js "<pinyin-pro 包目录>"` |

**懒解析**（`sync.Once`，不是 `init()`）—— 不用这个工具就不该为它付启动成本。

### 声调口径统一成 **1..5**

`1 阴平 · 2 阳平 · 3 上声 · 4 去声 · 5 轻声` —— **与 SV 的 `rapAccent` 一致**，
这样这个工具的输出将来能直接喂给那边。⚠️ pinyin-pro 内部用 **0** 表示轻声，
在换算时消化掉；`toneInfos` 里 5 与 0 都登记（测试抓过一次"声调 5 变成 ?"）。

调值与走向按标准五度标记法：

| 声调 | 调值 | 走向 |
|---|---|---|
| 阴平 | 55 | `—` 高平 |
| 阳平 | 35 | `↗` 上升 |
| 上声 | 214 | `↘↗` 降升 |
| 去声 | 51 | `↘` 下降 |
| 轻声 | — | `·` 轻短 |

### 多音字：**不假装能按词消歧**

⛔ 按词定音（"银行"不是 yín xíng）**需要词表，不做**。取而代之的是三条路，都摆在明面上：

1. `toneMode=library`（默认）：**数据源的首选**读音 —— 可复现、有据可查。
   有意思的是 pinyin-pro 的**单字默认**与 `multiple:true` 的**首选**并不一样：
   `了` 的单字默认是 `liao3`，而 multiple 首选是 **`le0`**（轻声）—— 后者才是歌词里的读法。
   生成的表取的是 multiple 首选，有专门用例把这条钉住（重新生成表时若它变了会响）。
2. `toneMode=lyric`：一张**人工小表**（了→le · 地→de · 得→de · 着→zhe · 的/呢/吗/吧/啊/么…），
   只收**证据清楚的虚字**，界面上会标「歌词常用读法」。有用例卡住两条：
   表不超过 20 条（别写成词典）、且每个读音都能在数据源里找到（**不许凭空来**）。
3. `overrides`：手动指定，优先级最高 —— `行=hang 地=di`（全角 `＝`、`：` 都收）。
   写法不对会**报错**，不默默忽略。

### 真机验收（2026-10-04）

```
燕鸟栖下林            → 燕4 鸟3 栖1 下4 林2（调值 51/214/55/51/35，走向 ↘ ↘↗ — ↘ ↗）
了的地得（library）   → 了5 的5 地4 得2     ← 地/得 是词典首选（dì/dé）
了的地得（lyric）     → 了5 的5 地5 得5     ← 都按歌词里的轻声来
银行行长 overrides=行=hang → 银2 行2 行2 长2（行 → háng）
当前片段（344 字）    → 70 个多音字 · 音符位置 + 音高走向 + ACE 音素都在 · 431 ms
中英混排「燕鸟 fly 下林。」→ 非汉字**如实标出**（f/l/y/。各自一行），不当成没看见
```

### 用例抓出来的 5 个真 bug（都是实现错，不是测试错）

| 症状 | 真因 |
|---|---|
| `hui4` 标成 `hùi` | **`ui` 的调号该标在 i 上**（iu/ui 都标"后面那个"），我一开始给 ui 标了 u |
| 声调 5 显示 `?` | `toneInfos` 只登记了 0（pinyin-pro 口径），而 `toneOf` 对外返回 5 |
| `行＝hang` 的值变乱码 | `IndexAny` 给的是**字节**下标，`+1` 切进了全角 `＝` 的中间 ⇒ 要按字符切 |
| `lv4` → `lv`（声调数字被吃掉） | 一个能标调的元音都没有时不该丢数字 ⇒ 原样返回 |
| `得` 在 lyric 模式下变成 `dé` | `unmarkTone("dé")` 也等于 `de`，于是 `de` 匹到了 `dé` ⇒ **先找完全相等的候选** |

还有两条是**测试自己写错**、被数据纠正的：`U+9FFF` 未必有读音（那一段有 136 个空位）；
`六` 是 **liù**（第四声），我按 liú 写了。

---

## 音符 / 歌词编辑（从 SV 脚本 `AKD工具侧栏.js` 移植）

用户点名要的那几项：**自动拼接** · **选中之后/之前所有音符** · **复制/粘贴歌词** ·
**添加 `+`/`-` 并后移** · **删除/合并歌词并前移**。9 个动作落在 **6 个工具**上
（`+`/`-` 共用 `lyric.shift`；之后/之前共用 `select.playhead`；删除/合并共用 `lyric.remove`）。
面板照原脚本的版式：**两列一行的按钮网格** + 末尾的说明 ComboBox/TextArea（9 条说明原文）。

### 侧栏分两栏

用户要求：「把你写的**工程总览**等栏放一类，**我说的**放一类」。所以侧栏现在两栏：

| 栏 | 是什么 | 工具 |
|---|---|---|
| **移植脚本** | 用户点名的（SV 脚本移植 + 备用的语调工具） | 14 个 |
| **自建工具** | 我先搭的骨架与路线 | 14 个 |

实现上**没有去改每个工具的 `Category`**：工具自己那个细分类（查找/歌词/音符）在代码、
测试、文档里有意义，不该被 UI 分组抹掉 ⇒ 加了一层 `toolGroups` + `groupOf()`，
只在**出给 UI 时换掉 `Category`**（复制一份再改，不动注册表里的原件）。
有用例钉住"两次调用结果一致、且注册表里的细分类一个都没变"。
认不出的分类**不会被吞掉** —— 它自己成一栏（将来加新分类会露出来）。

### 移植时改变的两处形状

1. ⛔ **`note resize` 一次调用只能给一个 `--dur`**（对所有点名音符生效）。
   而"自动拼接"每个音符的新时长都不一样 ⇒ **不能一音符一条命令**（329 个音符要跑 20 秒）。
   改成**按新时长分组**：真机上 329 个音符 → **17 条命令**（其中 262 个音符共享 240 tick，
   压成 3 条），而不是 326 条。
2. **播放头 ≠ 标记线**。原脚本用 `SV.getPlayback().getPlayhead()`（**播放头**），
   ACE 侧对应 `transport state` 的 `positionTick`；而 `find` 那边用的是 `caret`（**标记线**）。
   两者停止时通常重合，但"选中播放头之后的音符"必须用**真播放头**，否则播着的时候会选错。
   ⇒ 两个读点分开命名（`readPlaybackHead` / find 的 `readPlayhead`），注释里互相点名。

### ⛔ 真机验出来的一个静默 bug：分块选音符会把前面的顶掉

`selection set --mode replace` 是**替换**语义。我一开始把"选中之后的所有音符"按 100 个一块
分块发（命令行长度护栏），每块都用 `replace` ⇒ **每一块把上一块顶掉**，
329 个音符的片段最后只选上了 **27** 个，而且**不报错**。

修法：第一块 `replace`（先清干净），后续块 `modify`（往上加）。真机复核：

```
选中 100 个（replace） → selectionCount=100
选中 100 个（modify）  → selectionCount=200
选中 100 个（modify）  → selectionCount=300
选中  27 个（modify）  → selectionCount=327   ✓
```

⚠️ 顺带一个**语义**观察：「之后」（`结束 > 播放头`）+「之前」（`起点 < 播放头`）
**会重叠一个** —— 正在唱的那个音符两边都算。329 个音符里 327 + 3 − 1 = 329 ✓。

### ⚠️ 移位操作在选区尾部是**有损**的（原脚本就这样）

`添加 + 并后移` 把选中序列整体右移一位 —— **末尾那个音符的歌词被挤出序列、丢掉**；
`删除并前移` 左移一位，**最后一个音符的歌词留在原地**（原脚本的循环只到 `len-1`）。
所以它们**不是彼此的逆操作**。我一开始想用"`+后移` 再 `删除前移` 应当还原"来验收，
真机上没还原 —— 一查是**原脚本算法本身的性质**，不是移植错：

```
选中 3 个：[brl2, 谁, -]，播放头落在**最后一个**上
+ 并后移   → [brl2, 谁, +]     ← 原来的 "-" 被挤出末尾，丢了（后面没有音符可移）
删除并前移 → [brl2, 谁, +]     ← 它是最后一个，左移没东西可拉，自身不动
```

⇒ 用这些操作时：**选区别只选到你要改的地方为止**，尾部一个音符的歌词会被吃掉。
（这条写进了工具说明里。）

### PowerShell 传 JSON 参数的老坑又踩了一次

`--notes-to-select '[{"uuid":"…"}]'` 从 PowerShell 发出去，内层双引号**会被剥掉**
（PowerShell 与 C 运行时两层处理），ACE 收到 `[{uuid:{…}}]` 直接 `invalid JSON`；
双写引号 `""uuid""` 也没绕过去。

⇒ **正解**：用 Node 的 `spawnSync(cli, argsArray)` **数组传参**（不经 shell、不经 PowerShell 的
引号处理）—— 这本来就是本仓的调用纪律（"不经过 shell"）。
ACETool 自己走 `exec.Command(cli, args...)`，所以**工具本身没这个问题**；
只是**手工用 PowerShell 调 CLI 验收时**会踩。

### 真机验收（2026-10-04）

```
复制歌词（无选中）      → 329 个 · 整个片段（照原脚本退化）
自动拼接（干跑）        → 17 条命令（按新时长分组：60/120/180/240…）
选中之后的所有音符      → 100→200→300→327（modify 累加正确）
选中之前的所有音符      → 3 个（与"之后"重叠 1 个 = 播放头那个）
粘贴「燕鸟栖下林」      → 5 个音符 ← 5 个词（一字一音符，标点跳过）
真写 +后移 / 删除前移   → 都成功（⚠️ 尾部有损，见上）
收尾                    → 歌词已逐字还原、编辑器选区已清空
```

**规模**：`exe` 8.11 MB · Go 测试 **116 条**（tools 67）· 面板自检 **71 条**。

---

## 按播放头分割轨道（`track.split`）

原脚本 `splitTrackButton`：把当前轨道**克隆**一份，再按播放头把内容切成两半 ——
克隆留下的那半在克隆上、另一半在原轨上，于是"一条轨"变成"两条轨"。

ACE 侧有更干净的原语（都核过 `help`）：

| 用途 | 命令 |
|---|---|
| 复制一条轨（连片段与 FX 链，落在原轨**旁边** index=原+1，**回包直接给新轨身份**） | `track duplicate --track-index A` |
| 在片段**内部**切一刀（原生，音符/曲线/换气都保留） | `clip split --clip-uuid U --pos <全局 tick>` |
| 按 uuid 删片段（**全有或全无**，一次可点名多个） | `clip delete --clip-uuid U…` |
| 删**编排区**的轨道 | ⚠️ 先 `selection set --tracks [{"trackUuid":…}]` 再 `track delete`（**无参**）—— `--track-uuid` 只认 video/marker 带 |

判据照原脚本（两处边界都是**闭**的，差一个 tick 就是多切一刀/少切一刀，而且不报错）：

| 片段位置 | 归谁 |
|---|---|
| `结束 ≤ 播放头` | 原轨 |
| `开始 ≥ 播放头` | 复制轨 |
| 夹在中间 | `clip split` 切开，各留一半 |

切完之后判据变简单，而且**对没切到的片段同样成立**：原轨删「开始 ≥ 播放头」、
复制轨删「结束 ≤ 播放头」。有用例证明这两个判据**永远相反**（每个片段恰好被一边删掉）。

### ⛔ 这个工具用 `Plan`+`Apply`，而不是 `Build`

`Build` 是"**一次性把所有命令算完**"——它**看不到任何命令的返回值**。
而分割天生要"先复制出一批片段，再改**那些新片段**"（新 uuid 只有跑完才知道），
所以必须**边跑边读**。框架里能干这个的只有 `Apply`（进程内写实现，自己拿 `rd` 一条条跑），
`acep.patch` 当初就是为同样的理由走这条路的。

⇒ 代价：**干跑（`Plan`）只能给到第一步能确定的部分**（片段怎么分、要切哪几个、分几步），
给的是**条数与判据**，不是每条命令的原文。这条写进了工具说明。

⚠️ 有一条例外用**行为**验而不是读代码：给 `Plan` 一个假 CLI，它响应所有读命令、
**一旦 Plan 去调任何写动词就立刻失败** ⇒「干跑只读」是被真跑出来的。

### 回滚：能撤什么、**不能**撤什么

中途失败会把**复制出来的那条轨删掉**（这就是回滚）。但 ⛔ **回滚不是万能的**：

- 步骤顺序刻意把**最容易失败的 `clip split` 排在删除之前** ⇒ 到删除阶段为止只动过复制轨，
  这时回滚是完整的（**原轨没动过**），结果里会这么说。
- 真在**删除阶段**挂了，原轨上已经删掉的片段**回不来**（每次 `clip delete` 全有或全无，
  但已成功的那次是既成事实）⇒ 结果里**如实写明**"原轨已删的片段没有自动还原，Ctrl+Z 可逐步退回"，
  不让用户以为一切复原了。

⚠️ 回滚靠"选中新轨再 `track delete`"，所以会**改到编排区的轨道选择**。

### ⏳ 真机端到端验收**还没做**

写这个功能时 ACE Studio 被关掉了（`project info` 回 **exit 3**、桥握手文件
`%LOCALAPPDATA%\Timedomain\ACE Studio\mcp-bridge.json` 消失）。
单元测试覆盖了：判据与边界、删除谓词互斥、工具注册形态、**干跑只读**（假 CLI 行为验证）。
**真机要验的是**：`track duplicate` → `clip split` → 两次 `clip delete` 串起来对不对、
`track delete`（先选中）这条路在真机上是否按预期工作、回滚是否真能撤干净。
**ACE 一开就能补。**



---

## 纪律（照 `..\acetool\HANDOVER.md`，这里只是提醒）



1. **不经过 shell**：一律 `exec.Command(cli, args...)`。内层 JSON 引号、代码页、`--%` 那些坑全是 shell 带来的。
2. **读动词一律 `--json`**；写动词 stdout 可能是人话 ⇒ **JSON 解析失败 ≠ 命令失败**，只看退出码。
3. **`--json` 的错误信封写在 stderr** ⇒ 两个流分别捕获。
4. **越界一律拒绝、从不 clamp**（与 SV 侧相反）—— 别替它夹值，`error.hint` 当信息读。
5. **串行队列 + 每条 30 s 超时**：只有一个 ACE host，命令别并发。
6. **写前读指纹**，写时 `--if-match`；**干跑默认**，写入要显式开关、**写后回读**。
7. **不缓存能力/花名册**：每次现读 `--version` 与 `layers`（同一个 clip 的 `paramCount` 都变过）。
8. **失败要像错误报告**：exit code / `error.code` / `error.hint` 原样展示。

## 边界（别跑偏）

- ❌ 不碰 Synthesizer V / Instrument X（那是 AKDAgent 的事，走文件通道桥）
- ❌ 不注册 ACE 官方 MCP（63 个工具与 CLI 逐一对应、但参数没 schema；理由见 HANDOVER.md §6）
- ❌ 不做 ACE 的 UI 自动化 / 截图 / 鼠标模拟
- ❌ 不要求用户装任何运行时（选 Go 的全部理由）
- ⚠️ **文件路线（2026-10-04 已开第一刀，只读）**：① ACEP2 完整支持；② 历史壳一**可读**（改法走 `set-json`）；
  ③ 历史壳二加密，**只认出、不尝试解密**。写任何壳之前必须先过**往返自检**（`acep.RoundTrip`），
  并且**永远写副本**；旧工程要处理就**先问用户**。
