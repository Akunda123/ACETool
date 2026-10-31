// 一次性核对：修好控制点顺序之后，符头是不是**一条简单闭曲线**（不自交、没有多余的孔）。
//
// 判据：把轮廓摊平成多边形，① 数自交（应为 0）② 面积 / 包围盒面积 应接近椭圆的理论值 π/4 ≈ 0.785
//   （旋转椭圆内切于自己的包围盒，比值会略小，但绝不会只有一半；出现"洞"会让面积明显偏小）。
// 用法：node devtools/probe-glyph-shape.js
const fs = require('fs');
const path = require('path');
const src = fs.readFileSync(path.join(__dirname, '..', 'internal', 'web', 'assets', 'notation-glyphs.js'), 'utf8');
const GLYPHS = (0, eval)('(' + src.replace(/^[\s\S]*?const GLYPHS = /, '').replace(/;\s*$/, '') + ')');

function flatten(d, per) {
  const t = d.match(/[MLCZz]|-?\d+(?:\.\d+)?/g).map((s) => (isNaN(+s) ? s : +s));
  const subs = [];
  let i = 0, cur = null, poly = null;
  const step = (p0, p1, p2, p3) => {
    for (let k = 1; k <= per; k++) {
      const u = k / per, v = 1 - u;
      poly.push([
        v * v * v * p0[0] + 3 * v * v * u * p1[0] + 3 * v * u * u * p2[0] + u * u * u * p3[0],
        v * v * v * p0[1] + 3 * v * v * u * p1[1] + 3 * v * u * u * p2[1] + u * u * u * p3[1],
      ]);
    }
  };
  while (i < t.length) {
    const c = t[i++];
    if (c === 'M' || c === 'L') {
      const p = [t[i++], t[i++]];
      if (c === 'M') { poly = [p]; subs.push(poly); } else { poly.push(p); }
      cur = p;
    } else if (c === 'C') {
      const c1 = [t[i++], t[i++]], c2 = [t[i++], t[i++]], ep = [t[i++], t[i++]];
      step(cur, c1, c2, ep); cur = ep;
    }
  }
  return subs;
}
function area(poly) {
  let s = 0;
  for (let k = 0; k < poly.length; k++) {
    const a = poly[k], b = poly[(k + 1) % poly.length];
    s += a[0] * b[1] - b[0] * a[1];
  }
  return s / 2;
}
function segInt(p1, p2, p3, p4) {
  const d = (p2[0] - p1[0]) * (p4[1] - p3[1]) - (p2[1] - p1[1]) * (p4[0] - p3[0]);
  if (Math.abs(d) < 1e-12) return false;
  const ua = ((p3[0] - p1[0]) * (p4[1] - p3[1]) - (p3[1] - p1[1]) * (p4[0] - p3[0])) / d;
  const ub = ((p3[0] - p1[0]) * (p2[1] - p1[1]) - (p3[1] - p1[1]) * (p2[0] - p1[0])) / d;
  return ua > 1e-9 && ua < 1 - 1e-9 && ub > 1e-9 && ub < 1 - 1e-9;
}
function selfCross(poly) {
  const n = poly.length;
  let bad = 0;
  for (let a = 0; a < n; a++) {
    for (let b = a + 2; b < n; b++) {
      if (a === 0 && b === n - 1) continue;
      if (segInt(poly[a], poly[(a + 1) % n], poly[b], poly[(b + 1) % n])) bad++;
    }
  }
  return bad;
}

['noteheadBlack', 'noteheadHalf', 'noteheadWhole', 'restWhole', 'augmentationDot'].forEach((k) => {
  const g = GLYPHS[k], subs = flatten(g.d, 24), bb = g.bb;
  const box = (bb[2] - bb[0]) * (bb[3] - bb[1]);
  const areas = subs.map((p) => Math.abs(area(p)));
  const total = areas.reduce((a, b) => a + b, 0);
  const cross = subs.map(selfCross).reduce((a, b) => a + b, 0);
  console.log(k.padEnd(20) + ' 子路径 ' + subs.length + ' · 自交 ' + cross +
    ' · 面积/包围盒 ' + (total / box).toFixed(3) + '（单条 ' + areas.map((a) => (a / box).toFixed(3)).join(' / ') + '）');
});
