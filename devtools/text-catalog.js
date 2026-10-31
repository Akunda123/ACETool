// 把**面板上会显示的所有文本**从源码里抽出来，生成一份可以逐条改的 Markdown。
//
// 用户 2026-10-17：「把所有文本写成一个md我改一下」+「直接读字符串，不要截图」
// ⇒ 这里**只读源码**（HTML/JS/Go 的字符串），不起浏览器、不截图：
//   · 先把 `<script>` / `<style>` / HTML 注释剥掉，再取**看得见的文字**（标签内文本、标题、
//     按钮字、label、option、placeholder、title 提示）；
//   · JS 里带中文的 `toast('…')` / `textContent = '…'` 之类也收（这些是运行时的提示语）；
//   · 细条那 21 项、快捷键那 20 条、托盘菜单从 Go 里读（它们是 Go 的字符串，不在 HTML 里）。
//
// 用法：
//   node devtools/text-catalog.js                      # 写到 ..\acetool\面板文案.md
//   node devtools/text-catalog.js <输出路径>
// 改完那份 md 之后告诉我要改哪几条，我按「位置」那一列回到源码改原文。
const fs = require('fs');
const path = require('path');

const ROOT = path.join(__dirname, '..');
const ASSETS = path.join(ROOT, 'internal', 'web', 'assets');
const OUT = process.argv[2] || path.join(ROOT, '..', 'acetool', '面板文案.md');

// 面板自己开的那些页（`index.html` 是老壳页，已停用，不收）
const PAGES = [
  ['home.html', '主页面'],
  ['settings.html', '设置页'],
  ['find.html', '查找'],
  ['lyric.html', '填词'],
  ['lrc.html', 'LRC 导出'],
  ['notation.html', '谱面编辑器'],
  ['param.html', '参数映射'],
  ['pitch.html', '轨道内复制音高线'],
  ['pitchx.html', '跨轨道复制音高线'],
  ['rgb.html', 'RGB 变色'],
  ['tone.html', '语调教'],
  ['vib.html', '复制颤音模板'],
  ['within.html', '轨内复制'],
  ['xcopy.html', '跨轨复制'],
  ['rail.html', '侧栏（窄栏页）'],
];

const CJK = /[\u4e00-\u9fa5]/;
const seen = new Set();
let n = 0;

// 行号：按字符偏移数换行（够用，这些文件都不大）
function lineOf(text, idx) {
  let line = 1;
  for (let i = 0; i < idx && i < text.length; i++) if (text[i] === '\n') line++;
  return line;
}

const rows = [];
let curGroup = '';
function add(where, line, text, note) {
  const t = String(text).replace(/\s+/g, ' ').trim();
  if (!t) return;
  if (!CJK.test(t)) return;          // 只收中文文本（英文/数字属于代码，不是"文案"）
  const key = where + '|' + t;
  if (seen.has(key)) return;
  seen.add(key);
  rows.push({ n: ++n, group: curGroup, where: where + ':' + line, text: t, note: note || '' });
}

// ---------- HTML：看得见的文字 ----------
function stripNoise(html) {
  return html
    .replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))       // 注释：保留换行，行号别乱
    .replace(/<script[\s\S]*?<\/script>/gi, (m) => m.replace(/[^\n]/g, ' '))
    .replace(/<style[\s\S]*?<\/style>/gi, (m) => m.replace(/[^\n]/g, ' '));
}

function scanHTML(file, label) {
  curGroup = label + ' · ' + file;
  const raw = fs.readFileSync(path.join(ASSETS, file), 'utf8');
  const clean = stripNoise(raw);

  // ① 标签里的文字：<h1>…</h1> <h2> <button> <label> <option> <th> <td> <legend> <span class="msg">
  const tagRe = /<(title|h1|h2|h3|button|label|option|th|td|legend|strong|b)\b[^>]*>([\s\S]*?)<\/\1>/gi;
  let m;
  while ((m = tagRe.exec(clean))) {
    const inner = m[2].replace(/<[^>]*>/g, ' ');   // 去掉内嵌标签（如 <span>…</span>）
    add(file + ' <' + m[1].toLowerCase() + '>', lineOf(clean, m.index), inner, label);
  }
  // ② `class="label">文字` / `class="sub">文字` / `class="value">文字`（设置页那种行式布局）
  const clsRe = /class="(label|sub|value|hint|msg)"[^>]*>([^<]{1,120})</gi;
  while ((m = clsRe.exec(clean))) {
    add(file + ' .' + m[1], lineOf(clean, m.index), m[2], label);
  }
  // ③ 提示/占位：title="…" placeholder="…" aria-label="…"
  const attrRe = /(title|placeholder|aria-label)="([^"]{1,200})"/gi;
  while ((m = attrRe.exec(clean))) {
    add(file + ' [' + m[1] + ']', lineOf(clean, m.index), m[2], label);
  }

  // ④ 数据表里的文案：`label: '…'` / `tip: '…'` / `title: '…'`（主页面的 QUICK、各处字段都这样写）
  const labelRe = /\b(?:label|tip|what|note)\s*:\s*'((?:[^'\\]|\\.)*)'/g;
  while ((m = labelRe.exec(raw))) {
    add(file + ' 数据', lineOf(raw, m.index), m[1].replace(/\\'/g, "'"), '按钮/字段的文案');
  }
  // ④ 同一页面里 JS 的提示语（`toast('…')` / `.textContent = '…'` / `.title = '…'`）
  const scriptRe = /<script\b[^>]*>([\s\S]*?)<\/script>/gi;
  const jsChunks = [];
  while ((m = scriptRe.exec(raw))) jsChunks.push({ src: m[1], at: m.index });
  for (const { src, at } of jsChunks) {
    const jsRe = /(?:toast|say|pill|msg|textContent\s*=|\.title\s*=|innerHTML\s*=)\s*\(?\s*'((?:[^'\\]|\\.){1,200})'/g;
    let jm;
    while ((jm = jsRe.exec(src))) {
      const abs = at + jm.index;
      add(file + ' JS', lineOf(raw, abs), jm[1].replace(/\\'/g, "'"), label);
    }

    // ④b 三元里的两个分支：`say(dry ? '预览中…' : '改文件中…')` / `L.push([dry ? 'A' : 'B'])`
    //     上面那条只认 `say('…')` 这种**直接给字面量**的写法 ⇒ 三元的两边都被漏掉了。
    //     用户 2026-10-19：「最后更新一下文本md」——「预览 / 已改」这对文案正是三元写的，必须收进来。
    //     ⚠️ 只对这一类做一次轻量清理（把 `\n` 转义压成空格），**不动**其它行的写法，免得整份 md 大改。
    const triRe = /\?\s*'((?:[^'\\]|\\.){1,200})'\s*:\s*'((?:[^'\\]|\\.){0,200})'/g;
    const tidy = (s) => s.replace(/\\[nr]/g, ' ').replace(/\s+/g, ' ').trim();
    while ((jm = triRe.exec(src))) {
      const abs = at + jm.index;
      const ln = lineOf(raw, abs);
      add(file + ' JS（三元）', ln, tidy(jm[1].replace(/\\'/g, "'")), label);
      add(file + ' JS（三元）', ln, tidy(jm[2].replace(/\\'/g, "'")), label);
    }
  }
}

// ---------- Go：细条 21 项 / 快捷键 20 条 / 托盘菜单 ----------
function scanGo() {
  // 细条：`{Short: "连", Full: "连接音符", ...}`（list.go）
  curGroup = '细条（贴 ACE 的那条汉字侧栏） · internal/rail/list.go';
  const listGo = fs.readFileSync(path.join(ROOT, 'internal', 'rail', 'list.go'), 'utf8');
  const itemRe = /Short:\s*"([^"]+)",\s*Full:\s*"([^"]+)"/g;
  let m;
  while ((m = itemRe.exec(listGo))) {
    add('internal/rail/list.go', lineOf(listGo, m.index), m[2] + '（栏里显示：' + m[1] + '）', '细条');
  }

  // 快捷键：quick.go 的按钮 label/note；hotkeys.go 里"打开主页面"的字
  curGroup = '快捷键里的按钮名 · internal/hotkeys/quick.go';
  const quickGo = fs.readFileSync(path.join(ROOT, 'internal', 'hotkeys', 'quick.go'), 'utf8');
  const btnRe = /\{Tool:\s*"[^"]+",\s*Label:\s*"([^"]+)",\s*Note:\s*"([^"]+)"\}/g;
  while ((m = btnRe.exec(quickGo))) {
    add('internal/hotkeys/quick.go', lineOf(quickGo, m.index), m[1] + '（' + m[2] + '）', '快捷键位置名');
  }
  const openRe = /Label:\s*"打开主页面"/;
  if (openRe.test(quickGo)) add('internal/hotkeys/quick.go', 1, '打开主页面', '快捷键位置名');

  // 托盘菜单（tray_windows.go 里那几个 addItem 的字）
  curGroup = '托盘右键菜单 · internal/tray/tray_windows.go';
  const trayGo = fs.readFileSync(path.join(ROOT, 'internal', 'tray', 'tray_windows.go'), 'utf8');
  const trayRe = /addItem\(hmenu,\s*menu\w+,\s*"([^"]+)"\)/g;
  while ((m = trayRe.exec(trayGo))) {
    add('internal/tray/tray_windows.go', lineOf(trayGo, m.index), m[1], '托盘右键菜单');
  }
}

// ---------- 输出 ----------
for (const [file, label] of PAGES) {
  if (!fs.existsSync(path.join(ASSETS, file))) continue;
  scanHTML(file, label);
}
scanGo();

const md = [];
md.push('# ACETool 面板文案清单');
md.push('');
md.push('> 这份文件是**从源码里抽出来的**（`devtools/text-catalog.js`，不截图、不起浏览器）。');
md.push('> 想改哪条就改「当前文本」列（或直接告诉我要改第几条），我按「位置」列回源码改原文，');
md.push('> 改完再跑一次 `node devtools/text-catalog.js` 重新生成这份清单。');
md.push('> ');
md.push('> ⚠️ 这些是**已经上屏的文字**：改完要 `go build -o acetool.exe .` + 重启才生效。');
md.push('> ');
md.push('> 注：标着 `JS（三元）` 的行是**同一个位置的两个分支**（如「预览中…」/「改文件中…」、');
md.push('> 「已重新加载。」/「重新加载失败：」），要改就两边分别改；其它行都是一条对应一句。');
md.push('');
md.push('共 **' + rows.length + '** 条。按页面分组，`位置` 是「文件:行号」。');
md.push('');
let cur = '';
for (const r of rows) {
  const group = r.group || '';
  if (group !== cur) {
    cur = group;
    md.push('');
    md.push('## ' + group);
    md.push('');
    md.push('| # | 位置 | 当前文本 | 备注 |');
    md.push('|---|---|---|---|');
  }
  md.push('| ' + r.n + ' | `' + r.where + '` | ' + r.text.replace(/\|/g, '\\|') + ' | ' + (r.note || '') + ' |');
}
md.push('');
fs.mkdirSync(path.dirname(OUT), { recursive: true });
fs.writeFileSync(OUT, md.join('\n'), 'utf8');
console.log('共 ' + rows.length + ' 条 → ' + OUT);
