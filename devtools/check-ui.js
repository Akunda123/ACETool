/* 前端资源自检：语法 + **两份颜色表是否逐字一致** + 关键接线在不在。
 *
 * 为什么单独写成文件：这次用 `node -e "…"` 夹多行 JS 被 PowerShell 的引号规则咬了一口
 * （`\"` 在双引号字符串里不是转义）—— 教训与 recolor-panel.js 头注释里那条一样：
 * **多行 JS/JSON 不要经 shell 传**，写成文件再跑。
 *
 * 用法：node check-ui.js
 */
const fs = require('fs');
const path = require('path');

const A = path.join(__dirname, '..', 'internal', 'web', 'assets');
const read = (f) => fs.readFileSync(path.join(A, f), 'utf8');
/** 读仓库里**别处**的文件（比如 Go 工具定义）—— `read()` 只在 assets 目录里找。 */
const readRepo = (...rel) => fs.readFileSync(path.join(__dirname, '..', ...rel), 'utf8');
let bad = 0;
const ok = (name, cond, extra) => {
  console.log((cond ? '  ok   ' : '  FAIL ') + name + (extra ? '  — ' + extra : ''));
  if (!cond) bad++;
};

// ①.0 外部 .js 也过语法检查（2026-10-23：minimap.js 里混进了一个字面 "\\n"，
// find.html 加载时直接 SyntaxError ⇒ 缩略图整块没加载，页面上只剩一句报错）。
{
  const { execFileSync } = require('child_process');
  const jsFiles = ['minimap.js', 'find.js', 'app.js', 'shortcuts.js', 'notation.js'];
  const badJs = jsFiles.filter((f) => {
    try { execFileSync('node', ['--check', path.join(__dirname, '..', 'internal', 'web', 'assets', f)], { stdio: 'pipe' }); return false; }
    catch (e) { return true; }
  });
  ok('外部 .js 语法（node --check）', badJs.length === 0, badJs.length ? '不过：' + badJs.join(',') : jsFiles.length + ' 个');
}

// ① 内联 JS 语法
for (const f of ['home.html', 'rgb.html', 'settings.html', 'lyric.html', 'lrc.html', 'param.html', 'within.html', 'xcopy.html', 'find.html', 'pitch.html', 'notation.html', 'pitchx.html', 'vib.html', 'tone.html']) {
  const t = read(f);
  const blocks = t.match(/<script>([\s\S]*?)<\/script>/g) || [];
  try {
    blocks.forEach((b) => new Function(b.replace(/<\/?script>/g, '')));
    ok(f + ' 内联 JS 语法', true, blocks.length + ' 段');
  } catch (e) {
    ok(f + ' 内联 JS 语法', false, e.message);
  }
}

// ② 颜色表：rgb.html 里那份必须与 internal/tools/color.go 的 palette 逐字一致
//   ⚠️ Go 侧是**双引号**、JS 侧是单引号；而且 color.go 里还有第 29 个 hex 字面量
//      （`color.cycle` 参数里的举例 `#F6D203`）⇒ 必须**只取 palette 数组体**再比，
//      否则永远差一个（第一版就报了假阳性）。
const pull = (s) => [...s.matchAll(/["'](#[0-9A-Fa-f]{6})["']/g)].map((m) => m[1].toUpperCase());
const goSrc = fs.readFileSync(path.join(__dirname, '..', 'internal', 'tools', 'color.go'), 'utf8');
const body = (goSrc.match(/var palette = \[\]string\{([\s\S]*?)\n\}/) || [, ''])[1];
const js = pull(read('rgb.html'));
const go = pull(body);
ok('Go 调色板数组体抓到 28 色', go.length === 28, String(go.length));
ok('rgb.html 的颜色数与 Go 调色板一致', js.length === go.length, js.length + ' vs ' + go.length);
ok('两份颜色表逐字一致（顺序也一样）', js.join() === go.join(),
  js.join() === go.join() ? '' : ('第一处不同：js=' + js.find((c, i) => c !== go[i]) + ' go=' + go.find((c, i) => c !== js[i])));

// ②b 细条的 GDI 颜色必须**逐字等于 web 面板的色板**（用户 2026-10-17：「颜色按照web统一」）。
//   单一事实源 = `internal/web/assets/style.css` 的 `:root`；Go 侧是 **0x00BBGGRR** ⇒ 自己转回 RGB 再比。
//   ⚠️ 以前细条自己一套（#23262b / #2f3540），跟面板不在一个色系 ⇒ 这条断言就是防它再漂。
const cssRootSrc = (fs.readFileSync(path.join(__dirname, '..', 'internal', 'web', 'assets', 'style.css'), 'utf8')
  .match(/:root\s*\{([\s\S]*?)\}/) || [, ''])[1];
const cssVar = {};
for (const m of cssRootSrc.matchAll(/--([a-z0-9]+)\s*:\s*#([0-9a-fA-F]{6})/g)) cssVar[m[1]] = m[2].toLowerCase();
const railSrc = fs.readFileSync(path.join(__dirname, '..', 'internal', 'rail', 'rail_windows.go'), 'utf8');
const railColor = (name) => {
  const m = railSrc.match(new RegExp('\\b' + name + '\\s*=\\s*uintptr\\(0x([0-9a-fA-F]{6})\\)'));
  if (!m) return null;
  const bgr = m[1]; // 0x00BBGGRR
  return (bgr.slice(4, 6) + bgr.slice(2, 4) + bgr.slice(0, 2)).toLowerCase();
};
const colorPairs = [
  ['colorBG', 'bg'], ['colorTipBG', 'panel'], ['colorBorder', 'line'],
  ['colorText', 'fg'], ['colorDim', 'dim'], ['colorRun', 'ok'],
  ['colorParams', 'warn'], ['colorPage', 'accent'], ['colorFile', 'bad'], ['colorFieldBG', 'panel2'],
];
for (const [goName, cssName] of colorPairs) {
  const got = railColor(goName), want = cssVar[cssName];
  ok(`细条 ${goName} = web --${cssName}（#${want}）`, got === want, `rail=#${got} web=#${want}`);
}
// ②c 「改工程文件」那一组**三处同色**（用户 2026-10-17：「把需要改工程的放到一起，用同色，web端同步」）：
//     · 原生细条：`colorFile`（上面那条已经核了 = `--bad`）
//     · 主面板 home：`.fbtn.file` 的左竖条
//     · 窄栏页 rail.html：`.it.file::before`
//   三处必须都用 `--bad`；home 那边**不许**再用 `--warn`（琥珀是"要参数"，会撞车 —— 正是这次要分清的）。
{
  const homeFile = (read('home.html').match(/\.fbtn\.file\{([^}]*)\}/) || [, ''])[1];
  ok('home 的 `.fbtn.file` 用 --bad（红）', /var\(--bad,#e06c75\)/.test(homeFile), homeFile.trim().slice(0, 80));
  ok('home 的 `.fbtn.file` **不再**用 --warn（琥珀留给"要参数"）', !/--warn/.test(homeFile), homeFile.trim().slice(0, 80));
  ok('rail.html 有 `.it.file::before` 且用 --bad',
    /\.it\.file::before\s*\{[^}]*var\(--bad\)/.test(read('rail.html')),
    (read('rail.html').match(/\.it\.file::before[^}]*\}/) || [''])[0].trim());
  ok('rail.html 的 :root 里有 --bad', /--bad\s*:\s*#e06c75/.test(read('rail.html')));
}
// 悬停：web 是 `.tool:hover{background:#ffffff0d}`（5% 白叠在 --panel 上）⇒ Go 侧存的是**算好的实色**，
// 这里按同一个公式重算一遍比（四舍五入到整数）。
{
  const base = cssVar['panel'];
  const mix = (i) => Math.round(parseInt(base.slice(i, i + 2), 16) * 0.95 + 255 * 0.05);
  const want = ('0' + mix(0).toString(16)).slice(-2) + ('0' + mix(2).toString(16)).slice(-2) + ('0' + mix(4).toString(16)).slice(-2);
  const got = railColor('colorHover');
  ok('细条 colorHover = web --panel + 5% 白（.tool:hover 同一效果）', got === want, `rail=#${got} 期望=#${want}`);
}

// ③ 接线
const home = read('home.html');
ok('home.html 有 rgbWindow（开子窗口）', /rgbWindow: async function/.test(home));
ok('RGB 按钮指向 rgbWindow', /label: 'RGB 变色'[\s\S]{0,80}custom: 'rgbWindow'/.test(home));
const rgb = read('rgb.html');
// 只查**实际发出的调用**（注释里提到 color.cycle 是允许的，第一版把注释也算成违规了）
const calls = [...rgb.matchAll(/toolId: '([^']+)'/g)].map((m) => m[1]);
ok('rgb.html 实际调用 = track.color + view.minimap',
  calls.indexOf('track.color') >= 0 && calls.indexOf('view.minimap') >= 0 && calls.indexOf('color.cycle') < 0,
  calls.join(', '));
ok('未勾选的轨半透明（.trk.off）', /\.trk\.off\{opacity:/.test(rgb));

// ④ 「叫停」这条路：世代门（`/api/gate` + 每条请求带 `_gen`）
//
// 背景（用户 2026-10-05 的原话："我觉得还是一开始停不下来那版好看，就不能接受一个参数，接受到就不执行吗"）：
// 光在前端 `clearInterval` 挡不住**已经发出去的**请求 ⇒ 停止后仍会把这一轮剩下的轨走完。
// 做法是 exe 侧一个**世代号**：请求带上 `_gen`，到 `executeTool` 入口若已过期就直接不执行（连 CLI 都不 spawn）。
// 这四条断言把这条链路钉住 —— 任何一条掉了，"停止"就会悄悄退回"还要走一轮"。
ok('rgb.html 有世代门调用（POST /api/gate）', /fetch\('\/api\/gate'/.test(rgb));
ok('每条 track.color 都带 _gen', /_gen: String\(g\)/.test(rgb));
ok('过期回包被忽略（不报错、不计数）', /已叫停，未执行/.test(rgb));
ok('停止时推进世代（stop 里有 gate）', /function stop\(why\)[\s\S]{0,400}gate\(S\.gen\)/.test(rgb));
ok('轮转用叠着走的 setInterval（用户说好看的那版）', /S\.handle = setInterval\(/.test(rgb));
const api = fs.readFileSync(path.join(__dirname, '..', 'panel_api.go'), 'utf8');
ok('Go 侧入口有世代门', /已叫停，未执行/.test(api));
// ⚠️ 这条最阴：`_gen` 若留在 values 里，会被 `recordRun` 存进"最后一次"，
// 而"最后一次跑过的参数"会被**原样存下来**（面板自己的记录、以及以后任何重放类动作）⇒ 带着旧世代号重放会被世代门静默跳过（"按了没反应"）。
ok('Go 侧把 _gen 从 values 里删掉（别污染热键重放）', /delete\(values, "_gen"\)/.test(api));
// ⑤ 诚实报数：变色会灌 ACE 的撤销栈（§37.4 实测 13 条/秒），ACE 没有抑制开关 ⇒ 只能如实显示条数
ok('RGB 窗口如实显示「撤销栈 +N」', /id="undoN"/.test(rgb) && /S\.undo\+\+/.test(rgb));
// ★ 用户 2026-10-05：「在 rgb 的 html 醒目位置做个撤销提醒」⇒ 常驻警示条（不是按钮排里的小灰字）
ok('有常驻警示条（.warnbar，不随内容滚走）', /class="warnbar"/.test(rgb) && /\.warnbar\{/.test(rgb));
ok('警示条在 .wrap 之外（滚不走）', rgb.indexOf('class="warnbar"') < rgb.indexOf('class="wrap"'));
ok('警示条说清了后果（Ctrl+Z 回不去）', /Ctrl\+Z 回不到你自己的操作/.test(rgb));
ok('警示条说清了"关不掉"', /本工具关不掉/.test(rgb));

// ⑥ 填词窗口 `lyric.html` —— 用户 2026-10-05 的口径：
//   最上方**动态**显示行数/字数 · 两个选项（分割成字符按语种判断 / 忽略连音默认勾上）·
//   一句话一行 · 每行右边**试听 + 跳转** · 最上方**刷新键** · 改完一行立刻写回
const lyr = read('lyric.html');
ok('home.html 有 lyricWindow（开子窗口）', /lyricWindow: async function/.test(home));
ok('「填词」按钮指向 lyricWindow', /label: '填词'[\s\S]{0,120}custom: 'lyricWindow'/.test(home));
const lcalls = [...lyr.matchAll(/api\('([a-zA-Z.]+)'/g)].map((m) => m[1]);
ok('lyric.html 调的工具齐（读/写/试听/定位/状态/停）',
  ['lyric.pages', 'lyric.setSentence', 'lyric.audition', 'lyric.goto', 'transport.state', 'transport.stop']
    .every((t) => lcalls.indexOf(t) >= 0),
  lcalls.join(', '));
ok('最上方有刷新键', /id="refresh"[\s\S]{0,60}刷新/.test(lyr));
ok('行数/字数在列表**之外**（不随内容滚走）',
  lyr.indexOf('id="nLines"') < lyr.indexOf('class="list"') && /id="nChars"/.test(lyr));
ok('字数跟着编辑框实时重算', /addEventListener\('input', recount\)/.test(lyr));
ok('忽略连音**默认勾上**', /ignore: true/.test(lyr) && /\$\('ignore'\)\.checked = S\.ignore/.test(lyr));
ok('「按字符隔开」默认走语种判断（发 auto）', /splitChars: S\.split === 'auto' \? 'auto'/.test(lyr));
// ⭐ 用户 2026-10-06：这个选项的名字是「按字符隔开」（原来叫「分割成字符」）
ok('选项名是「按字符隔开」', /按字符隔开/.test(lyr) && !/分割成字符/.test(lyr));
// 主面板（app.js）里那个填词表单是**同一个选项**，名字要一致（注释里的旧名不算）
ok('主面板填词表单也改叫「按字符隔开」', /<span>按字符隔开<\/span>/.test(read('app.js')));
ok('每行有试听 + 跳转', /class="mini play"/.test(lyr) && /class="mini go"/.test(lyr));
ok('改完一行立刻写回（change → lyric.setSentence）',
  /addEventListener\('change', function \(\) \{ commit\(/.test(lyr) && /'lyric\.setSentence'/.test(lyr));
ok('试听到点暂停（数 positionTick 到句尾+提前量才 stop）',
  /positionTick >= until/.test(lyr) && /api\('transport\.stop'/.test(lyr));
// Go 侧：读/隐藏/回填 三条的实现在不在
const lyrGo = fs.readFileSync(path.join(__dirname, '..', 'internal', 'tools', 'lyric.go'), 'utf8');
ok('Go 侧「能读」原始形态（`-` / `#n` 都拿得到）', /func readNoteLyrics/.test(lyrGo) && /json:"lyric"/.test(lyrGo));
ok('Go 侧「能藏」：丢 `-` + 收 `#n`', /func collapseTenuto/.test(lyrGo) && /func stripSyllable/.test(lyrGo));
ok('Go 侧「能回填」：按模式选自检', /func roundTripsCollapsed/.test(lyrGo) && /func roundTrips\(/.test(lyrGo));
ok('Go 侧写回跟着忽略连音走（fill / perNote）', /mode = "fill"/.test(lyrGo) && /mode = "perNote"/.test(lyrGo));

// ⑦ LRC 导出窗口 `lrc.html` —— 用户 2026-10-05：行表 + **分割/合并**（分割自动取那个字的音符起始时间）
//   + 偏移（原脚本三个框）+ **忽略短间隙** + **CJK 之间不加空格**（做选项）+ 复制/落盘
const lrcHtml = read('lrc.html');
ok('home.html 有 lrcWindow（开子窗口）', /lrcWindow: async function/.test(home));
ok('「LRC 导出」按钮指向 lrcWindow', /label: 'LRC 导出'[\s\S]{0,140}custom: 'lrcWindow'/.test(home));
const lrcCalls = [...lrcHtml.matchAll(/api\('([a-zA-Z.]+)'/g)].map((m) => m[1]);
ok('lrc.html 调的工具 = lyric.lrc + lyric.lrcFile',
  lrcCalls.indexOf('lyric.lrc') >= 0 && lrcCalls.indexOf('lyric.lrcFile') >= 0, lrcCalls.join(', '));
ok('最上方有刷新键', /id="refresh"[\s\S]{0,40}刷新/.test(lrcHtml));
ok('有复制 + 写成文件', /复制 LRC/.test(lrcHtml) && /写成 \.lrc 文件/.test(lrcHtml));
ok('每行有分割 + 合并↓', /class="mini split"/.test(lrcHtml) && /class="mini merge"/.test(lrcHtml));
// ⭐ 分割必须落在**音符间隙**上（用户 2026-10-05）：
// 靠 `charNote`（逐字符属于第几个词）往后推到下一个词的开头，而不是在光标字符处硬切。
ok('分割落在音符间隙上（用 charNote 推到下一个词）',
  /cn\[cut\] <= before/.test(lrcHtml) && /charNote: cn\.slice\(idx\)/.test(lrcHtml));
ok('分割：光标在最后一个音符里会**拒绝**（不造假断点）',
  /它后面没有音符可以断开了/.test(lrcHtml));
ok('合并：词号给后一行加偏移（不然接缝处会被当成同一个词）',
  /x \+ maxA \+ 1/.test(lrcHtml));
// ⭐ 用户 2026-10-05 追加的三件：
//  ① 文本**可以直接改**（避免歌词错了回 ACE 改一遍再导出）+ 改过的行不能再分割
ok('行文本可直接编辑（主行不 readonly；只有展开出来的子行是只读）',
  /<input class="tx" type="text" autocomplete="off"[^>]*value=/.test(lrcHtml) &&
  !/<input class="tx" type="text" autocomplete="off"[^>]*readonly[^>]*value=/.test(lrcHtml) &&
  /<input class="tx" type="text" readonly autocomplete="off" value=/.test(lrcHtml));
ok('改过的行**拒绝分割**（逐字符时间已错位）',
  /if \(r\.edited\)/.test(lrcHtml) && /逐字符时间已经对不上/.test(lrcHtml));
//  ② 多时间戳的行**可展开**（用户：「折叠歌词有点抽象，设计成可展开」）
ok('多时间戳的行有展开开关', /class="mini exp/.test(lrcHtml) && /铺成|摊成|展开成 /.test(lrcHtml));
ok('展开出来的子行只读、不带按钮', /class="ln sub"/.test(lrcHtml));
//  ③ 确认面板：最终 LRC 原文，能直接改（改了从文本重建行表）
ok('有确认面板（最终 LRC 可看可改）',
  /id="confirmBox"/.test(lrcHtml) && /id="finalText"/.test(lrcHtml) && /function parseFinalIntoRows/.test(lrcHtml));
ok('分割/合并都用逐字符数组（charSec）', /cs\.slice\(idx\)/.test(lrcHtml) && /charSec = charSec\.concat/.test(lrcHtml));
ok('合并只留一个时间戳', /times: \[a\.times\[0\]\]/.test(lrcHtml));
ok('两个选项都在（忽略短间隙 / CJK 不空格）',
  /id="gap"/.test(lrcHtml) && /id="cjkTight"/.test(lrcHtml));
// ⭐ 「短间隙阈值选择和连接音符一致」（用户 2026-10-05）：档位表必须**逐字相同**（只有第 0 档名字不同）
const tiersOf = (src) => {
  const body = (src.match(/var TIERS = \[([\s\S]*?)\];/) || [, ''])[1];
  return [...body.matchAll(/\{\s*t:\s*(\d+),\s*name:\s*'([^']+)'\s*\}/g)].map((m) => m[1] + ':' + m[2]);
};
const homeTiers = tiersOf(home), lrcTiers = tiersOf(lrcHtml);
ok('两边档位表都是 9 档', homeTiers.length === 9 && lrcTiers.length === 9,
  'home=' + homeTiers.length + ' lrc=' + lrcTiers.length);
ok('档位表逐档一致（只允许第 0 档名字不同）',
  homeTiers.length === lrcTiers.length &&
  homeTiers.every((x, i) => i === 0 ? x.split(':')[0] === lrcTiers[0].split(':')[0] : x === lrcTiers[i]),
  'home=[' + homeTiers.join(' ') + '] lrc=[' + lrcTiers.join(' ') + ']');
ok('两边的档位选择器都是**滑块**（和连接音符一致）',
  /id="gap" min="0" max="8" step="1" type="range"|type="range" id="gap"/.test(lrcHtml) && /type="range"/.test(home));
ok('滑块旁边显示档位名', /id="gapName"/.test(lrcHtml));
ok('偏移三个框（分/秒/毫秒）', /id="offMin"/.test(lrcHtml) && /id="offSec"/.test(lrcHtml) && /id="offMs"/.test(lrcHtml));

// ⭐ 时间戳格式：**前端与 Go 必须是同一条规则**（截断、负数、分钟不封顶）。
// 这里把页面里那个 `fmt` 抽出来在 Node 里跑**与 Go 测试同一组向量** —— 两边算得不一样的话，
// 用户看到的行与复制/保存出去的那份就是两份东西。
// ⚠️ `pad2` 是**一行**写的，所以不能假设它有多行函数体（第一版正则就栽在这）。
const fmtSrc = lrcHtml.match(/function pad2\(n\)[^\n]*\n    function fmt\(sec, off\)\s*\{[\s\S]*?\n    \}/);
if (!fmtSrc) {
  ok('能抽出页面的 fmt 做对表', false);
} else {
  const jsFmt = new Function(fmtSrc[0] + '; return fmt;')();
  const VEC = [
    [12.3287, 0, '[00:12.32]'], [0, 0, '[00:00.00]'], [59.999, 0, '[00:59.99]'],
    [60.5, 0, '[01:00.50]'], [65.0, 1.5, '[01:06.50]'], [-0.5, 0, '[-00:00.50]'],
    [12.32, -0.5, '[00:11.82]'], [3661.25, 0, '[61:01.25]'],
  ];
  const bad = VEC.filter(([s, o, want]) => jsFmt(s, o) !== want);
  ok('页面 fmt 与 Go 的 formatLRCTime 逐字一致（8 组向量）', bad.length === 0,
    bad.length ? bad.map(([s, o, w]) => s + '+' + o + '→' + jsFmt(s, o) + '≠' + w).join(' · ') : '');
}
// Go 侧：三条规则的实现都在
const lrcGo = fs.readFileSync(path.join(__dirname, '..', 'internal', 'tools', 'lrc.go'), 'utf8');
ok('Go 侧「忽略短间隙」在（gapTicks）', /gapTicks/.test(lrcGo) && /gap > int64\(gapTicks\)/.test(lrcGo));
ok('Go 侧空格规则按字符判（不按语种字段）',
  /func isCJKToken/.test(lyrGo) && /tight := cjkTight && isCJKToken/.test(lrcGo));
ok('Go 侧 `#n` 收起 / `-` 丢掉都在（同一个 joinLyricsTimed）',
  /stripSyllable/.test(lrcGo) && /n\.Lyric == "-"/.test(lrcGo));

// ⑧ 主页「批量替换音素」按钮（`phoneme.replace`）—— 悬停注释里填查找/替换 + 一个**默认勾上**的干跑
ok('主页有「批量替换音素」按钮并指向 phoneme.replace',
  /label: '批量替换音素', tool: 'phoneme\.replace'/.test(home));
ok('注释里要填查找/替换两个词', /name: 'find', label: '查找'/.test(home) && /name: 'replace', label: '替换'/.test(home));
// ⭐ 用户 2026-10-06：「**只给两个框，不提供其他输入**」⇒ 没有干跑勾选、没有范围选择
ok('只有两个框，不给别的输入（没有干跑勾选）',
  !/dryRunField/.test(home) && !/hf_dryRun/.test(home));
ok('查找框输入完**报数**（范围里多少个音符含它）',
  /function probeCount\(q\)/.test(home) && /含「' \+ v\.find \+ '」' \+ j\.hit/.test(home));
ok('替换后报**成功数**（用 done 的条数，不是计划的 willSet）',
  /j\.done\.length/.test(home) && /替换 ' \+ okN \+ ' 个/.test(home));
ok('报数与真写用**同一套判据**（预览走的就是 dryRun=1 的 Plan）',
  /scope: 'auto', dryRun: '1'/.test(home));
ok('注释里的控件能取到值（fieldValues）',
  /function fieldValues\(q\)/.test(home) && /var fv = fieldValues\(q\)/.test(home));
ok('查找为空时先在本地挡一句（不必白跑一趟）', /先在悬停说明里填「查找」/.test(home));
ok('能报细账（扫/命中/会写/会被冻结）', /willFreeze/.test(home) && /会被冻结成 override/.test(home));

// ⑩ ⭐ 「回退这一批」（用户 2026-10-06 选的方案）：ACE 的 `phoneme set` 一次只收一个音符
//   ⇒ 替换 N 个就是 N 个撤销条目、一次 Ctrl+Z 退不了 ⇒ 菜单里给一颗**我们自己的**一次点击回退。
// ⑩ ⚠️ 「回退这一批」（音素专用那版）**已经拆掉**（用户 2026-10-07：「那这样音素里的撤回就不需要了」）——
//   通用那颗「撤回这一批」走 ACE 自己的撤销栈，连音素**时序**都能还原，比"只还原符号"的那版更彻底。
//   这条断言是**反向**的：别再让它长回来（两套撤回并存只会让人分不清哪颗是权威）。
ok('菜单里不再有音素专用的「回退这一批」',
  !/id="hf_undo"/.test(home) && !/function runRewind\(/.test(home) && !/phoneme\.rewind/.test(home));
ok('Go 侧也没有 phoneme.rewind 了（拆干净，不留半截工具）',
  !/phoneme\.rewind/.test(fs.readFileSync(path.join(__dirname, '..', 'internal', 'tools', 'phonops.go'), 'utf8')));
ok('菜单钉住时跑完**不收**（报数与回退都在菜单里）',
  /if \(el && !MENU\.open\) el\.hidden = true;/.test(home));
ok('摆位抽成 placeTip，并在 resize 时重摆（窄窗口不露外面）',
  /function placeTip\(btn, el\)/.test(home) && /addEventListener\('resize'/.test(home) && /placeTip\(MENU\.btn, el\)/.test(home));

// ⑪ ⭐ 「撤回刚才的一批」（用户 2026-10-07）：**一键按钮压进 ACE 撤销栈的整批**，一次退回去。
//   机制不写在页面上（都在 internal/tools/history.go 与 panel_api.go），这里只钉住接线：
//   跑完记标记 → 点亮按钮 → 点下去带 anchor/count/actor 去 history.undoBatch（真写）。
ok('顶栏有「撤回这一批」，且默认是灰的',
  /id="btnUndo"/.test(home) && /id="btnUndo" type="button" disabled/.test(home) && /class="undobtn"/.test(home));
ok('所有 /api/run 都走 runCall（撤回标记才收得全）',
  /async function runCall\(toolId, values\)/.test(home) &&
  /if \(d && d\.undo\) setUndoMark\(d\.undo\)/.test(home) &&
  (home.match(/fetch\('\/api\/run'/g) || []).length === 1);   // 只剩 runCall 里那一处
ok('写工具跑完就点亮（按 created 条数）',
  /UNDO = \(mark && mark\.created > 0\) \? mark : null/.test(home) && /撤回这一批 · ' \+ UNDO\.created/.test(home));
ok('点撤回发 history.undoBatch（带锚点/条数/署名），且真写',
  /runCall\('history\.undoBatch'/.test(home) && /anchor: String\(mark\.anchor\)/.test(home) &&
  /count: String\(mark\.created\)/.test(home) && /actor: '远程控制'/.test(home) && /dryRun: '0'/.test(home));
ok('把名字名单也带上（顶上不是这一批时一条都不退）',
  /names: JSON\.stringify\(mark\.names \|\| \[\]\)/.test(home));
ok('被挡住 / 停手都要如实报（不假装退干净了）',
  /j\.blocked/.test(home) && /一条都没退/.test(home) && /j\.stopped/.test(home) && /退了 ' \+ n \+ ' 条就停手/.test(home));

// ⑨ ⭐ 用户 2026-10-06（截图）：Edge 在悬停菜单的「查找」框上弹「保存的信息 / 上次使用」，
//   原话「网页上不保存历史记录信息」⇒ 面板里**凡是能打字的框**都得关掉自动填充/历史下拉。
//   做法：autocomplete="off"（+ autocapitalize/autocorrect off）。这条断言扫**全部前端资产**，
//   漏一个框就会在真机上被历史下拉盖住 —— 所以按「文件 + 框」逐个列出来报。
const ALL_ASSETS = ['index.html', 'home.html', 'settings.html', 'rgb.html', 'lyric.html', 'lrc.html', 'param.html',
  'within.html', 'xcopy.html', 'app.js', 'shortcuts.js', 'notation.js', 'minimap.js'];
const TYPABLE = /<(input|textarea)\b[^>]*?>/g;
// 注释里写的 `<input id=fpcontent>` 那种「文档示意」不算控件 ⇒ 先剥注释再扫
const stripComments = (src, isJs) => (isJs
  ? src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^[ \t]*\/\/.*$/gm, '')
  : src.replace(/<!--[\s\S]*?-->/g, ''));
const nofillMissing = [];
ALL_ASSETS.forEach((f) => {
  const src = stripComments(read(f), /\.js$/.test(f));
  [...src.matchAll(TYPABLE)].forEach((m) => {
    const tag = m[0];
    const isInput = tag.indexOf('<input') === 0;
    const type = (tag.match(/type="([^"]*)"/) || [, isInput ? 'text' : 'textarea'])[1];
    // 只挑**能打字**的：checkbox/range/radio/button/submit/file/color 不受历史下拉影响
    if (['checkbox', 'range', 'radio', 'button', 'submit', 'file', 'color', 'hidden'].indexOf(type) >= 0) return;
    // app.js 用 `' + NOFILL + '` 拼进去（常量值就是 autocomplete="off" …），也算过关
    if (/autocomplete="off"/.test(tag) || /NOFILL/.test(tag)) return;
    nofillMissing.push(f + ':' + type);
  });
});
ok('所有能打字的框都关了自动填充（autocomplete="off"）', nofillMissing.length === 0,
  nofillMissing.join(' · '));
ok('关填充这事是**集中**做的（app.js 有 NOFILL 常量，不再逐个手写）',
  /const NOFILL = 'autocomplete="off"/.test(read('app.js')));
ok('主页注释里的查找/替换框带 autocomplete="off"',
  /id="hf_' \+ f\.name \+ '" autocomplete="off"/.test(home));

// ⑫ ⭐ 「参数映射」窗口 `param.html`（用户 2026-10-07：「下一个做参数跟随变化 param.sync，是一个新web」）
//    页面只管"把范围/层/筛选翻译成参数"，判据在 Go 侧（paramsync_test.go）。这里钉住**接线与口径**。
const param = read('param.html');
const pcalls = [...param.matchAll(/call\('([a-zA-Z.]+)'/g)].map((m) => m[1]);
ok('param.html 只调 param.view（读快照）与 param.sync（写）',
  pcalls.length === 2 && pcalls.indexOf('param.view') >= 0 && pcalls.indexOf('param.sync') >= 0, pcalls.join(', '));
ok('范围三档 = 全 clip / 所选音符 / 指定',
  /data-r="clip"/.test(param) && /data-r="sel"/.test(param) && /data-r="bars"/.test(param));
ok('两个小节框（精确到小节：整数、min=1）',
  /<input type="number" id="b1" min="1" step="1"/.test(param) &&
  /<input type="number" id="b2" min="1" step="1"/.test(param) && /第[\s\S]{0,80}小节/.test(param));
ok('缩略图统一用 canvasmap.js（音符按音高摆 + 范围遮罩）',
  /src="canvasmap.js"/.test(param) && /v\.notes/.test(param) &&
  /CanvasMap\.draw\(/.test(param) && /picks:/.test(param));