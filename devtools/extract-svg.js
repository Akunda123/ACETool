// 把 bravura_glyphs.js 里我们要用的字形，转成**现成的 SVG path**（y 轴翻好、坐标换成「360 单位 = 1 个间距」）。
//
// 为什么要这一步：
//   * 五线谱的谱号/音符头/变音号/时值数字都是**字形轮廓**，自己画不像；Bravura（OFL）是现成的。
//   * VexFlow 5 起没有 UMD 包了，`build/cjs/*` 没法用 `<script>` 直接引 —— 而本项目**没有构建步骤**，
//     所以只抽这十几个字形的路径（十几 KB），而不是把 1.1 MB 的库整套嵌进来。
//   * SmuFL 路径的 `b` 是**绝对**三次贝塞尔（实测：按绝对解析出来的包围盒与声明值**等比 1.44**，
//     而 250×1.44 = 360 ⇒ 声明值按 250/间距、路径按 360/间距，这里统一到 360）。
const fs = require('fs');
const src = fs.readFileSync(process.argv[2], 'utf8');
const body = (src.match(/glyphs:\s*\{([\s\S]*)\}\s*;?\s*$/) || [, src])[1];

const WANT = ['gClef', 'accidentalSharp', 'accidentalFlat', 'accidentalNatural', 'augmentationDot',
  'noteheadBlack', 'noteheadHalf', 'noteheadWhole', 'noteheadDoubleWhole',
  'restWhole', 'restHalf', 'restQuarter', 'rest8th', 'rest16th', 'rest32nd',
  'flag8thUp', 'flag8thDown', 'flag16thUp', 'flag16thDown', 'flag32ndUp', 'flag32ndDown'];
for (let i = 0; i <= 9; i++) WANT.push('timeSig' + i);

const re = /(\w+):\s*\{([^{}]*)\}/g;
const raw = {};
let mm;
while ((mm = re.exec(body))) {
  if (WANT.indexOf(mm[1]) < 0) continue;
  const blk = mm[2];
  const o = (/o:\s*'([^']*)'/.exec(blk) || [])[1];
  if (o) raw[mm[1]] = o;
}

/** 绝对解析 SmuFL 路径 → {d: SVG path, bb: [x0,y0,x1,y1]}（y 已翻、单位=路径单位） */
function toSVG(path) {
  const t = path.match(/[mlbz]|-?\d+(?:\.\d+)?/g).map((s) => (isNaN(+s) ? s : +s));
  let i = 0, cx = 0, cy = 0, d = '', xs = [], ys = [];
  const num = (v) => (Math.round(v * 100) / 100).toString();
  const P = (x, y) => { xs.push(x); ys.push(y); return num(x) + ' ' + num(-y); };
  while (i < t.length) {
    const c = t[i++];
    if (c === 'm' || c === 'l') {
      const x = t[i++], y = t[i++];
      d += (c === 'm' ? 'M' : 'L') + P(x, y) + ' ';
      cx = x; cy = y;
    } else if (c === 'b') {
      // ⚠️ 源数据（VexFlow bravura_glyphs.js）里 `b` 后面的三对点是 **「端点 c1 c2」**，
      //    不是 SVG 的「c1 c2 端点」—— 照 SVG 顺序原样搬，符头/谱号会被画成自交的"蝴蝶结"
      //    （2026-10-08 实测：改前 noteheadBlack 的接缝总转角 308°、改成 0°；正圆点 augmentationDot
      //     到圆心的相对标准差 0.169 → 0.001）。判据与修法见 devtools/probe-glyph-curve.js。
      const a = [t[i++], t[i++], t[i++], t[i++], t[i++], t[i++]];
      const ep = P(a[0], a[1]), p1 = P(a[2], a[3]), p2 = P(a[4], a[5]);
      d += 'C' + p1 + ' ' + p2 + ' ' + ep + ' ';
      cx = a[0]; cy = a[1];
    } else if (c === 'z') { d += 'Z '; }
  }
  const r = (v) => Math.round(v * 10) / 10;
  return { d: d.trim(), bb: [r(Math.min(...xs)), r(-Math.max(...ys)), r(Math.max(...xs)), r(-Math.min(...ys))] };
}

const out = {};
for (const n of Object.keys(raw)) out[n] = toSVG(raw[n]);

const lines = [
  '/* 由 vendor/extract-svg.js 生成 —— 请勿手改。',
  ' * 字形来源：Bravura（SMuFL），经 VexFlow 4.2.3 的 bravura_glyphs.js 转出。',
  ' * Bravura 采用 SIL Open Font License 1.1（可自由使用/再分发）。',
  ' * 坐标：y 轴已翻成 SVG 方向；**360 单位 = 1 个五线谱间距**（渲染时按 space/360 缩放）。',
  ' * bb = [x0,y0,x1,y1]（同一坐标系）。 */',
  'const GLYPHS = ' + JSON.stringify(out) + ';',
  '',
];
fs.writeFileSync(process.argv[3], lines.join('\n'));
console.log('glyphs', Object.keys(out).length, 'bytes', fs.statSync(process.argv[3]).size);
console.log('gClef bb', JSON.stringify(out.gClef.bb), 'noteheadBlack bb', JSON.stringify(out.noteheadBlack.bb));
