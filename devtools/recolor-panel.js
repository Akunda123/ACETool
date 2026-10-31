/* 把面板配色换成**钢琴卷帘那种深色风格**（用户 2026-10-05 给的参考图）。
 *
 * 参考图取的色（近黑底 + 极弱中灰网格 + 细白播放头 + 一个蓝色高亮）：
 *   canvas #232323 · 行带 #282828/#2b2b2b · 网格 #333/#3a3a3a · 标尺文字 #8f8f8f
 *   chrome #2a2a2a · 深底(日志/输入) #1f1f1f · 正文 #d6d6d6 · 蓝 #2f7cf6 · 播放头 #e8e8e8
 *
 * ⚠️ 为什么用 Node 而不是 PowerShell：本项目**两次**被 PowerShell 写坏过源文件
 * （`Set-Content -Encoding UTF8` 把 UTF-8 内容写成乱码、还把多行字符串折成一行）。
 * Node 读写 UTF-8 是字节直通，安全。
 *
 * 用法：node recolor-panel.js          # 干跑，只报会改多少处
 *       node recolor-panel.js --write  # 真写
 */
const fs = require('fs');
const path = require('path');

const ASSETS = path.join(__dirname, '..', 'internal', 'web', 'assets');
const WRITE = process.argv.indexOf('--write') >= 0;

// 语义色表（style.css 的 53 处硬编码基本就是这套；顺带把旧值**换成变量引用**会更干净，
// 但这一步只做等值替换 —— 改结构留到下次，改动越小越好验）
// 用户给了个纯色块（实测 #282c33）当**页面背景** ⇒ 整套中性灰按同一色相（+5,+9,+16）挪过去，
// 否则卡片/网格还是暖灰，跟蓝灰底放一起会打架。
const CSS_MAP = {
  '#232323': '#282c33', // --bg（用户指定）
  '#2a2a2a': '#2f333a', // --panel
  '#3a3a3a': '#3f434a', // --line
  '#d6d6d6': '#d6dae0', // --fg
  '#8f8f8f': '#9498a0', // --dim
  '#1f1f1f': '#242830', // 日志/输入那种深底
};

// 缩略图那套是**手写在 SVG 里的**（不走 CSS 变量），要单独换
const MINIMAP_MAP = {
  '#232323': '#282c33', // svg 底 / .mmscroll（与页面背景同一个色）
  '#282828': '#2d3138', // 行带 A
  '#2b2b2b': '#30343b', // 行带 B
  '#2d2d2d': '#32363d', // 轨名栏
  '#3a3a3a': '#3f434a', // 主网格
  '#333333': '#383c43', // 次网格
  '#8f8f8f': '#9498a0', // 标尺文字
  '#d6d6d6': '#d6dae0', // 轨名
};

function apply(file, map) {
  const p = path.join(ASSETS, file);
  let s = fs.readFileSync(p, 'utf8');
  const before = s;
  const report = [];
  for (const [from, to] of Object.entries(map)) {
    const re = new RegExp(from.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'gi');
    const n = (s.match(re) || []).length;
    if (n) { s = s.replace(re, to); report.push(from + ' → ' + to + ' ×' + n); }
  }
  console.log('\n· ' + file + (s === before ? '（没有可换的）' : ''));
  report.forEach((r) => console.log('    ' + r));
  if (WRITE && s !== before) fs.writeFileSync(p, s);
}

apply('style.css', CSS_MAP);
apply('minimap.js', MINIMAP_MAP);

// :root 里那几行是**权威值**，单独核一遍（万一上面漏了它也不会漏）
const rootWant = {
  '--bg': '#282c33', '--panel': '#2f333a', '--line': '#3f434a', '--fg': '#d6dae0',
  '--dim': '#9498a0', '--ok': '#5ebd7a', '--bad': '#e06c75', '--warn': '#d9a441', '--accent': '#1a73e8',
};
let css = fs.readFileSync(path.join(ASSETS, 'style.css'), 'utf8');
let fixed = 0;
for (const [k, v] of Object.entries(rootWant)) {
  const re = new RegExp('(' + k + ':\\s*)(#[0-9a-fA-F]{3,6})');
  const m = css.match(re);
  if (m && m[2].toLowerCase() !== v) {
    console.log('    :root ' + k + ' ' + m[2] + ' → ' + v);
    css = css.replace(re, '$1' + v);
    fixed++;
  }
}
if (WRITE && fixed) fs.writeFileSync(path.join(ASSETS, 'style.css'), css);

console.log('\n' + (WRITE ? '已写入' : '（干跑，没写；加 --write 才真改）'));
if (WRITE) {
  console.log('⚠️ 简谱/五线谱编辑器里的**白纸**没动 —— 那是刻意的（谱面就该是白纸黑字）。');
  console.log('   要连它也做成深色谱面，那是另一个决定，说出来再改。');
}
