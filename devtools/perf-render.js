/* 量一下**我们最复杂的那个视图**的渲染成本（谱面编辑器：简谱 + 五线谱两排 SVG）。
 *
 * 为什么值得测：讨论"要不要全迁 Vue"时我断言过"换框架省下的重渲染在我们这个规模是亚毫秒级" ——
 * 那是**推断**。这里把它变成**数字**（本项目纪律：能测的就别断言）。
 *
 * ⚠️ 测得到什么、测不到什么，先说清：
 *   · 测到 = **生成 SVG 字符串**的纯计算成本（这是我们自己的那部分活儿；重渲染的开销全在这儿）。
 *   · 测不到 = 浏览器把字符串 parse 成 DOM 的成本、样式/布局/绘制的成本。
 *     所以这是**下界**，不是全貌；但"要不要为它换框架"这个量级判断够用了。
 *
 * 用法：node perf-render.js [元素个数...]
 */
const fs = require('fs');
const path = require('path');
const vm = require('vm');

const dir = path.join(__dirname, '..', 'internal', 'web', 'assets');
const src = [
  fs.readFileSync(path.join(dir, 'notation-glyphs.js'), 'utf8'),
  fs.readFileSync(path.join(dir, 'notation.js'), 'utf8'),
].join('\n;\n');

const sandbox = {
  window: {},
  document: {
    getElementById: () => null, createElement: () => ({ style: {}, appendChild() {} }),
    head: { appendChild() {} }, addEventListener() {}, querySelectorAll: () => [],
  },
  console,
};
sandbox.window.document = sandbox.document;
vm.createContext(sandbox);
vm.runInContext(src, sandbox);
const T = sandbox.window.NotationPanel._test;

function makeEls(n) {
  const els = [];
  for (let i = 0; i < n; i++) {
    if (i % 7 === 6) { els.push(T.norm({ kind: 'sustain', duration: 4 })); continue; }
    if (i % 11 === 10) { els.push(T.norm({ kind: 'rest', duration: 4 })); continue; }
    els.push(T.norm({
      kind: 'note', pitch: 1 + (i % 7), octave: (i % 3) - 1,
      duration: [1, 2, 4, 8, 16][i % 5], dots: i % 4 === 3 ? 1 : 0,
      accidental: i % 9 === 8 ? 'sharp' : '', lyric: 'la',
    }));
  }
  return els;
}

function timeIt(label, fn, iters) {
  fn(); // 预热（JIT）
  const t0 = process.hrtime.bigint();
  for (let i = 0; i < iters; i++) fn();
  const ms = Number(process.hrtime.bigint() - t0) / 1e6 / iters;
  console.log('  ' + label.padEnd(42) + ms.toFixed(3) + ' ms/次');
  return ms;
}

const sizes = process.argv.slice(2).map(Number).filter((n) => n > 0);
const list = sizes.length ? sizes : [50, 200, 800];
console.log('谱面编辑器渲染成本（Node 侧纯计算，浏览器 parse/布局/绘制**未计**）\n');
for (const n of list) {
  const els = makeEls(n);
  const lay = T.layout(els);
  const geom = T.staffGeom(lay.cols, 0);
  const opt = { key: 0, meter: { n: 4, d: 4 }, sel: 3, dy: geom.dy, lyricY: geom.lyricY };
  console.log('· ' + n + ' 个元素（简谱 + 五线谱两排）');
  const a = timeIt('layout()', () => T.layout(els), 200);
  const b = timeIt('renderJianpu()', () => T.renderJianpu(els, lay.cols, opt), 100);
  const c = timeIt('renderStaff()', () => T.renderStaff(els, lay.cols, opt), 100);
  console.log('  ⇒ 一帧全量重建 ≈ ' + (a + b + c).toFixed(2) + ' ms' +
    '（60fps 的预算是 16.7 ms）\n');
}
