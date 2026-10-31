// 一次性核对：`notation-glyphs.js` 里 `C`（三次贝塞尔）那三个点对的**真实顺序**是哪一种。
//
// 背景：`extract-svg.js` 把源路径的 `b` 段按 SVG 的 `C c1 c2 端点` 顺序原样写出，但渲染出来的
//   符头/谱号是"蝴蝶结"（自交）—— 说明源里的顺序不是 SVG 那个顺序。
//
// 三种候选（每段三个点对 n1 n2 n3 的分配）：
//   H1 原样：  c1=n1 c2=n2 端点=n3
//   H2 交换：  c1=n2 c2=n1 端点=n3
//   H3 端点在前：c1=n2 c2=n3 端点=n1
// 判据（两条，都不靠眼睛）：
//   ① **接缝平滑**：相邻两段在接点处的进入切向（端点−前一 c2）与离开切向（后一 c1−端点）
//      应当共线；把每条接缝的夹角加起来，谁最小谁对。
//   ② **正圆**：`augmentationDot` 的包围盒是 144×144，轮廓上每点到圆心距离的**相对标准差**
//      谁最小谁对。
// 用法：node devtools/probe-glyph-curve.js
const fs = require('fs');
const path = require('path');
const src = fs.readFileSync(path.join(__dirname, '..', 'internal', 'web', 'assets', 'notation-glyphs.js'), 'utf8');
const GLYPHS = (0, eval)('(' + src.replace(/^[\s\S]*?const GLYPHS = /, '').replace(/;\s*$/, '') + ')');

const H = {
  H1: (n) => ({ c1: n[0], c2: n[1], ep: n[2] }),
  H2: (n) => ({ c1: n[1], c2: n[0], ep: n[2] }),
  H3: (n) => ({ c1: n[1], c2: n[2], ep: n[0] }),
};

function segs(d, hyp) {
  const t = d.match(/[MLCZz]|-?\d+(?:\.\d+)?/g).map((s) => (isNaN(+s) ? s : +s));
  const out = [];
  let i = 0, cur = null, sub = -1;
  while (i < t.length) {
    const c = t[i++];
    if (c === 'M' || c === 'L') {
      cur = [t[i++], t[i++]];
      // ⚠️ 一个字形可能有好几条子路径（外轮廓 + 内孔，如 noteheadHalf/noteheadWhole/gClef）——
      //    子路径之间**不该**算接缝（首尾本来就不相接）。
      if (c === 'M') sub++;
    } else if (c === 'C') {
      const n = [[t[i++], t[i++]], [t[i++], t[i++]], [t[i++], t[i++]]];
      const s = H[hyp](n);
      out.push({ from: cur, c1: s.c1, c2: s.c2, to: s.ep, sub: sub });
      cur = s.ep;
    }
  }
  return out;
}
const sub = (a, b) => [a[0] - b[0], a[1] - b[1]];
const ang = (a, b) => {
  const la = Math.hypot(a[0], a[1]), lb = Math.hypot(b[0], b[1]);
  if (!la || !lb) return 0;
  const c = (a[0] * b[0] + a[1] * b[1]) / (la * lb);
  return Math.acos(Math.max(-1, Math.min(1, c))) * 180 / Math.PI;
};
/** 接缝总转角（度）：越小越平滑（**只在同一条子路径内**算接缝） */
function seamTurn(s) {
  let tot = 0;
  for (let k = 0; k < s.length; k++) {
    const prev = s[(k + s.length - 1) % s.length], cur = s[k];
    if (prev.sub !== cur.sub) continue;
    tot += ang(sub(prev.to, prev.c2), sub(cur.c1, cur.from));
  }
  return Math.round(tot);
}
function sample(s) {
  const pts = [];
  s.forEach((g) => {
    for (let k = 0; k <= 10; k++) {
      const u = k / 10, v = 1 - u;
      pts.push([
        v * v * v * g.from[0] + 3 * v * v * u * g.c1[0] + 3 * v * u * u * g.c2[0] + u * u * u * g.to[0],
        v * v * v * g.from[1] + 3 * v * v * u * g.c1[1] + 3 * v * u * u * g.c2[1] + u * u * u * g.to[1],
      ]);
    }
  });
  return pts;
}
function roundness(pts, bb) {
  const cx = (bb[0] + bb[2]) / 2, cy = (bb[1] + bb[3]) / 2, r = (bb[2] - bb[0]) / 2;
  const ds = pts.map((p) => Math.hypot(p[0] - cx, p[1] - cy));
  const mean = ds.reduce((a, b) => a + b, 0) / ds.length;
  const sd = Math.sqrt(ds.reduce((a, b) => a + (b - mean) ** 2, 0) / ds.length);
  return +(sd / r).toFixed(3);
}

const withCurves = Object.keys(GLYPHS).filter((n) => /C/.test(GLYPHS[n].d));
console.log('带曲线的字形 ' + withCurves.length + ' 个；接缝总转角（度，越小越平滑）：');
console.log('  字形                  H1原样   H2交换   H3端点在前');
withCurves.forEach((n) => {
  const r = ['H1', 'H2', 'H3'].map((h) => String(seamTurn(segs(GLYPHS[n].d, h))).padStart(6));
  console.log('  ' + n.padEnd(22) + r.join('  '));
});
console.log('\naugmentationDot 到圆心的相对标准差（越小越圆）：');
['H1', 'H2', 'H3'].forEach((h) => {
  console.log('  ' + h + ' = ' + roundness(sample(segs(GLYPHS.augmentationDot.d, h)), GLYPHS.augmentationDot.bb));
});
