// 一次性手术（**已于 2026-10-08 跑过，别再无脑跑**）：把 app.js 里的「查找面板」整段删掉
// （已搬进 `find.js`），换上一句委托。再跑一次会因为找不到 START 标记而报错退出 —— 这是**有意**的。
//
// 为什么用脚本而不是手改：这段有 420 行，靠人誊抄必然出错。用 Node 按**标记行**切，
// 切完自己验一遍（标记只剩一处、文件长度对得上、`node --check` 过）。
// ⚠️ 不用 PowerShell 读改写 —— 那会把 UTF-8 读成 ANSI，整份文件变乱码（本项目踩过）。
//
// 用法：node devtools/move-find-block.js
const fs = require('fs');
const path = require('path');

const file = path.join(__dirname, '..', 'internal', 'web', 'assets', 'app.js');
const src = fs.readFileSync(file, 'utf8');

const START = '/* ---------------- 查找面板（find.notes 专用 UI）';
const NEXT = '/* ---------------- 歌词面板（lyric.pages 专用 UI）';

const i = src.indexOf(START);
const j = src.indexOf(NEXT);
if (i < 0 || j < 0 || j < i) throw new Error('找不到标记（START=' + i + ' NEXT=' + j + '）');
if (src.indexOf(START, i + 1) >= 0) throw new Error('START 标记不止一处 —— 先看一眼再动');

const REPLACEMENT = `/* ---------------- 查找面板（find.notes 专用 UI） ----------------
 *
 * 面板本体已经搬进 **\`find.js\`**（\`window.FindPanel\`）—— 用户 2026-10-08：
 * 「我要的是进入 home.html 的正式功能，新的 web 页」⇒ 有了独立的 \`find.html\` 子窗口。
 * 独立窗口**不加载 app.js**（那是整个主面板），所以面板必须住在两边都能用的模块里。
 *
 * 这里只留一句委托；主面板（index.html）产出的元素 id 与以前**完全一样**
 * （热键 \`HOST_PRIMARY['find.notes'] = 'fprefresh'\` 照旧能用）。
 * 宿主页要给的只有三样：\`postRun\`（app.js 有）、\`MinimapPanel\`（minimap.js）、工具清单
 * （app.js 走 \`state.catalog\`，独立窗口走 \`FindPanel.ready()\`）。
 */

`;
const out = src.slice(0, i) + REPLACEMENT + src.slice(j);
fs.writeFileSync(file, out, 'utf8');

const removed = src.slice(i, j).split('\n').length;
console.log('删掉 ' + removed + ' 行，换成 ' + REPLACEMENT.split('\n').length + ' 行委托');
console.log('文件 ' + src.length + ' B → ' + out.length + ' B');
console.log('还剩 START 标记 ' + out.split(START).length + ' 处（应为 0）、NEXT 标记 ' + (out.split(NEXT).length - 1) + ' 处（应为 1）');
