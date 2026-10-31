// 一次性修数据：把 `notation-glyphs.js` 里每条 `C`（三次贝塞尔）的三对点**重排**成 SVG 的顺序。
//
// 为什么：字形数据是从 VexFlow 4.2.3 的 bravura_glyphs.js 里抽的，源里 `b` 段三对点的顺序是
//   **「端点 c1 c2」**，而 `extract-svg.js` 当初按 SVG 的「c1 c2 端点」原样搬了出来
//   ⇒ 五线谱上的**符头/谱号/休止符全被画成自交的"蝴蝶结"**（一拍上去像叉子）。
//   判据不是眼睛（见 `probe-glyph-curve.js`，两条客观判据）：
//     ① 接缝平滑：相邻段在接点处的进入/离开切向应共线 ⇒ 按「端点 c1 c2」读，接缝总转角 =
//        noteheadBlack 0°、restWhole 0°、restHalf 0°、augmentationDot 0°（原样读是 308°/360°/360°/323°）。
//     ② 正圆：augmentationDot 的包围盒 144×144 ⇒ 轮廓到圆心距离的相对标准差 = 0.001（原样读 0.169）。
//   `extract-svg.js` 已同步改对（下次重新生成就是对的）；本源文件是一次**修数据**，
//   因为生成它用的那份 vendor 源文件不在仓里。
//
// 用法：node devtools/fix-glyph-curve-order.js [文件路径]
const fs = require('fs');
const path = require('path');

const file = process.argv[2] || path.join(__dirname, '..', 'internal', 'web', 'assets', 'notation-glyphs.js');
const src = fs.readFileSync(file, 'utf8');

// ① 先判一次"要不要修"：按 SVG 顺序读，noteheadBlack / augmentationDot 的接缝应当**已经**是 0°
function seamTurn(d) {
  const t = d.match(/[MLCZz]|-?\d+(?:\.\d+)?/g).map((s) => (isNaN(+s) ? s : +s));
  const segs = [];
  let i = 0, cur = null;
  while (i < t.length) {
    const c = t[i++];
    if (c === 'M' || c === 'L') { cur = [t[i++], t[i++]]; }
    else if (c === 'C') {
      segs.push({ from: cur, c1: [t[i++], t[i++]], c2: [t[i++], t[i++]], to: [t[i++], t[i++]] });
      cur = segs[segs.length - 1].to;
    }
  }
  const sub = (a, b) => [a[0] - b[0], a[1] - b[1]];
  let tot = 0;
  for (let k = 0; k < segs.length; k++) {
    const prev = segs[(k + segs.length - 1) % segs.length], cur2 = segs[k];
    const a = sub(prev.to, prev.c2), b = sub(cur2.c1, cur2.from);
    const la = Math.hypot(a[0], a[1]), lb = Math.hypot(b[0], b[1]);
    if (!la || !lb) continue;
    const cos = Math.max(-1, Math.min(1, (a[0] * b[0] + a[1] * b[1]) / (la * lb)));
    tot += Math.acos(cos) * 180 / Math.PI;
  }
  return Math.round(tot);
}
const before = (0, eval)('(' + src.replace(/^[\s\S]*?const GLYPHS = /, '').replace(/;\s*$/, '') + ')');
if (seamTurn(before.noteheadBlack.d) === 0 && seamTurn(before.augmentationDot.d) === 0) {
  console.log('已经是对的（接缝 0°）—— 不用修。');
  process.exit(0);
}

// ② 重排：`C n1x n1y n2x n2y n3x n3y` → `C n2x n2y n3x n3y n1x n1y`
//    ⚠️ 只动 `C` 后面的六个数，别的字面（含数字写法）一律原样保留。
const NUM = '(-?\\d+(?:\\.\\d+)?)';
const re = new RegExp('C\\s*' + [NUM, NUM, NUM, NUM, NUM, NUM].join('\\s+'), 'g');
let n = 0;
const fixed = src.replace(re, (m, p1, p2, p3, p4, p5, p6) => {
  n++;
  return 'C' + [p3, p4, p5, p6, p1, p2].join(' ');
});
// ③ 头上补一行说明（让下一个人知道这份数据被修过、为什么）
const note = ' * ⚠️ 2026-10-08 修数据：`C` 的三对点已重排成 SVG 的 (c1 c2 端点) ——\n' +
  ' *    源数据（VexFlow bravura_glyphs.js 的 `b`）是 (端点 c1 c2)，原样搬会把符头/谱号画成蝴蝶结。\n' +
  ' *    判据与修法见 devtools/probe-glyph-curve.js / devtools/fix-glyph-curve-order.js。 */';
const out = fixed.replace(/ \* bb = \[x0,y0,x1,y1\]（同一坐标系）。 \*\//, note);
if (out === fixed) throw new Error('没找到要替换的文件头注释，先看一眼文件再改');
fs.writeFileSync(file, out, 'utf8');

const after = (0, eval)('(' + out.replace(/^[\s\S]*?const GLYPHS = /, '').replace(/;\s*$/, '') + ')');
console.log('重排了 ' + n + ' 条 C；接缝总转角（度，应≈0）：');
['noteheadBlack', 'noteheadHalf', 'noteheadWhole', 'restWhole', 'restHalf', 'augmentationDot', 'gClef']
  .forEach((k) => console.log('  ' + k.padEnd(18) + String(seamTurn(after[k].d)).padStart(5) +
    '（改前 ' + String(seamTurn(before[k].d)).padStart(5) + '）'));
console.log('写出 ' + file);
