/* 无浏览器渲染自检：在 Node 里跑 `notation.js` 的纯渲染函数，产出一份 HTML。
 *
 * 为什么值得写：五线谱是**几何**——音符头该落在哪条线/哪条加线上、符干朝上还是朝下、
 * 变音号该画升号还是还原号，这些用眼睛看一眼图比读代码快得多。这里把 SVG 生成出来，
 * 再交给 Edge 无头截屏（见 render-check.md 里那条命令）人眼过一遍。
 *
 * 用法：node render-check.js  → 产出 notation-check.html
 */
const fs = require('fs');
const path = require('path');
const vm = require('vm');

// 脚本住在 devtools/，而被测的 JS 在 internal/web/assets/（**不能**把自检脚本放进 assets ——
// 那里是 `go:embed all:assets`，放进去会连测试产物一起打进 exe）。
const dir = path.join(__dirname, '..', 'internal', 'web', 'assets');
const src = [
  fs.readFileSync(path.join(dir, 'notation-glyphs.js'), 'utf8'),
  fs.readFileSync(path.join(dir, 'notation.js'), 'utf8'),
].join('\n;\n');

// 最小 DOM 桩：notation.js 顶层只用到 window / document.getElementById / document.head
const sandbox = {
  window: {},
  document: {
    getElementById: () => null,
    createElement: () => ({ style: {}, appendChild() {} }),
    head: { appendChild() {} },
    addEventListener() {},
    querySelectorAll: () => [],
  },
  console,
};
sandbox.window.document = sandbox.document;
vm.createContext(sandbox);
vm.runInContext(src, sandbox);

const T = sandbox.window.NotationPanel._test;

// 几组刻意选来暴露问题的用例
const CASES = [
  { name: 'C 大调 · 音阶上下行 + 加线', key: 0, meter: { n: 4, d: 4 }, els: [
    { kind: 'note', pitch: 1, octave: -1, duration: 4, lyric: '低' },
    { kind: 'note', pitch: 3, octave: 0, duration: 4, lyric: 'mi' },
    { kind: 'note', pitch: 5, octave: 0, duration: 4, lyric: 'sol' },
    { kind: 'note', pitch: 7, octave: 0, duration: 4, lyric: 'si' },
    { kind: 'note', pitch: 1, octave: 1, duration: 2, lyric: '高' },
    { kind: 'sustain', duration: 4 },
    { kind: 'note', pitch: 6, octave: 1, duration: 4, lyric: 'la' },
    { kind: 'rest', duration: 4 },
  ] },
  { name: 'F 大调（1 个降号）· #4 应画还原号', key: 5, meter: { n: 4, d: 4 }, els: [
    { kind: 'note', pitch: 4, octave: 0, duration: 4, lyric: '降' },   // Bb：调号已给 ⇒ 不画记号
    { kind: 'note', pitch: 4, octave: 0, duration: 4, accidental: 'sharp', lyric: '还原' }, // B♮ ⇒ 画还原号
    { kind: 'note', pitch: 1, octave: 0, duration: 8, lyric: '八' },
    { kind: 'note', pitch: 1, octave: 0, duration: 16, lyric: '十六' },
    { kind: 'note', pitch: 1, octave: 0, duration: 32, lyric: '卅二' },
    { kind: 'note', pitch: 2, octave: 0, duration: 4, dots: 1, lyric: '附点' },
  ] },
  { name: 'D 大调（2 个升号）· 高音加线', key: 2, meter: { n: 3, d: 4 }, els: [
    { kind: 'note', pitch: 7, octave: 1, duration: 2, lyric: '高' },
    { kind: 'sustain', duration: 4 },
    { kind: 'note', pitch: 2, octave: -1, duration: 4, lyric: '低' },
    { kind: 'note', pitch: 5, octave: 2, duration: 4, lyric: '很高' },
  ] },
];

const out = [];
out.push('<!doctype html><meta charset="utf-8"><title>谱面渲染自检</title>');
out.push('<style>body{background:#fff;color:#111;font:14px sans-serif;margin:20px}'
  + 'h2{font-size:15px}svg{background:#fff;border:1px solid #ddd;margin-bottom:6px}'
  + '.nt-num{font:600 22px/1 "Times New Roman",serif;fill:#111}'
  + '.nt-dash{font:700 24px/1 "Times New Roman",serif;fill:#111}'
  + '.nt-hold{font:400 20px/1 "Times New Roman",serif;fill:#b9b9b9}'
  + '.nt-plus{font:400 20px/1 sans-serif;fill:#5b91ff}'
  + '.nt-acc{font:400 15px/1 serif;fill:#111}'
  + '.nt-leg{font:400 13px/1 sans-serif;fill:#333}'
  + '.nt-dot{fill:#111}.nt-under{stroke:#111;stroke-width:1.6}'
  + '.nt-bar{stroke:#8a8a8a;stroke-width:1;stroke-dasharray:3 3}'
  + '.nt-line{stroke:#111;stroke-width:1}.nt-barline{stroke:#111;stroke-width:1.2}'
  + '.nt-ledger{stroke:#111;stroke-width:1.4}.nt-stem{stroke:#111;stroke-width:1.5}'
  + '.nt-tie{fill:none;stroke:#111;stroke-width:1.4}'
  + '.nt-head path,.nt-rest path,.nt-clef path,.nt-keysig path,.nt-accglyph path,.nt-flag path,.nt-timesig path{fill:#111}'
  + '.nt-augdot{fill:#111}.nt-lyric{font:400 13px/1 sans-serif;fill:#333}'
  + '.nt-hitrect{fill:transparent}</style>');

let fails = 0;
for (const c of CASES) {
  const els = c.els.map(T.norm);
  const lay = T.layout(els);
  const w = 104 + lay.total + 60;
  const geom = T.staffGeom(lay.cols, c.key);
  const opt = { key: c.key, meter: c.meter, sel: 1 };
  out.push('<h2>' + c.name + '（调式下标 ' + c.key + '，拍号 ' + c.meter.n + '/' + c.meter.d
    + '，五线谱垂直让位 ' + geom.dy.toFixed(1) + 'px）</h2>');
  out.push('<svg xmlns="http://www.w3.org/2000/svg" width="' + w + '" height="214" viewBox="0 0 ' + w + ' 214">'
    + '<rect width="100%" height="100%" fill="#fff"/>' + T.renderJianpu(els, lay.cols, opt) + '</svg>');
  out.push('<svg xmlns="http://www.w3.org/2000/svg" width="' + w + '" height="' + geom.height + '" viewBox="0 0 ' + w + ' ' + geom.height + '">'
    + '<rect width="100%" height="100%" fill="#fff"/>'
    + T.renderStaff(els, lay.cols, { key: c.key, meter: c.meter, sel: 1, dy: geom.dy, lyricY: geom.lyricY }) + '</svg>');
  // 顺带把几何断言打出来（肉眼之外再有一层）
  const midis = els.map((e) => T.midiOf(e, c.key));
  const dias = els.map((e) => (e.kind === 'note' ? T.diatonicOf(e, c.key) : '-'));
  const accs = els.map((e) => (e.kind === 'note' ? (T.accidentalToDraw(e, c.key, T.keySig(c.key)) || '—') : '-'));
  console.log('· ' + c.name);
  console.log('   midi  ' + midis.join(' '));
  console.log('   字母步 ' + dias.join(' '));
  console.log('   变音号 ' + accs.join(' '));
}

// 断言：F 大调 degree 4（无变音）应得 70（Bb），带 sharp 应得 71（B♮）且画还原号
const f4 = T.norm({ kind: 'note', pitch: 4, duration: 4 });
const f4s = T.norm({ kind: 'note', pitch: 4, duration: 4, accidental: 'sharp' });
if (T.midiOf(f4, 5) !== 70) { console.log('FAIL: F 大调 4 应是 70，得到 ' + T.midiOf(f4, 5)); fails++; }
if (T.midiOf(f4s, 5) !== 71) { console.log('FAIL: F 大调 #4 应是 71，得到 ' + T.midiOf(f4s, 5)); fails++; }
if (T.accidentalToDraw(f4, 5, T.keySig(5)) !== '') { console.log('FAIL: F 大调 4 不该画变音号'); fails++; }
if (T.accidentalToDraw(f4s, 5, T.keySig(5)) !== 'accidentalNatural') { console.log('FAIL: F 大调 #4 应画还原号'); fails++; }
// C 大调：加线判断（`1` 低一个八度 = C3 = 字母步 21，要三条加线；`1` 本位 = C4 = 28，一条）
const c4 = T.norm({ kind: 'note', pitch: 1, octave: 0, duration: 4 });
const c3 = T.norm({ kind: 'note', pitch: 1, octave: -1, duration: 4 });
if (T.diatonicOf(c4, 0) !== 28) { console.log('FAIL: C 大调本位 1 应是字母步 28，得到 ' + T.diatonicOf(c4, 0)); fails++; }
if (T.diatonicOf(c3, 0) !== 21) { console.log('FAIL: C 大调低八度 1 应是字母步 21，得到 ' + T.diatonicOf(c3, 0)); fails++; }
if (T.midiOf(c4, 0) !== 60 || T.midiOf(c3, 0) !== 48) { console.log('FAIL: C4/C3 的 MIDI 不对'); fails++; }
// 拍数：附点四分 = 1.5
if (Math.abs(T.beats(T.norm({ duration: 4, dots: 1 })) - 1.5) > 1e-9) { console.log('FAIL: 附点四分应为 1.5 拍'); fails++; }

// ⭐ 调号/主音字母必须与 Go 侧 `fifthsOf` / `tonicLetterOf` **逐一相同** ——
// 不然同一份谱在屏幕上是 C# 音、导出的 MusicXML 是 Db 音，两边对不上账。
const KEY_CASES = [
  [0, 0, 0, 'C'], [7, 1, 4, 'G'], [2, 2, 1, 'D'], [9, 3, 5, 'A'], [4, 4, 2, 'E'],
  [11, 5, 6, 'B'], [6, 6, 3, 'F#'], [1, 7, 0, 'C#'],
  [5, -1, 3, 'F'], [10, -2, 6, 'Bb'], [3, -3, 2, 'Eb'], [8, -4, 5, 'Ab'],
];
for (const [pc, fifths, letter, name] of KEY_CASES) {
  const got = T.fifthsOf(pc);
  const want = fifths;   // 面板与 Go 一样：只有 pc=1/6 走升号侧，F/Bb/Eb/Ab 该是几个降号就是几个
  if (got !== want) { console.log('FAIL: 调式 ' + pc + '（' + name + '）的 fifths 应为 ' + want + '，得到 ' + got); fails++; }
  if (T.tonicLetterOf(got) !== ((4 * got) % 7 + 7) % 7) { console.log('FAIL: 主音字母推导不对'); fails++; }
}
if (T.fifthsOf(1) !== 7) { console.log('FAIL: 1=#C 应当是 7 个升号（与 Go 侧一致）'); fails++; }
if (T.keySig(5).sharp !== false || T.keySig(5).n !== 1) { console.log('FAIL: F 大调应当是 1 个降号'); fails++; }
// `2b` 在 C 大调 = Db3（字母 D、字母步 22），不是 C#3 —— 与 Go 的 `spellMidi` 同一判据
const db3 = T.norm({ kind: 'note', pitch: 2, octave: -1, duration: 4, accidental: 'flat' });
if (T.diatonicOf(db3, 0) !== 22) { console.log('FAIL: C 大调 2b 的字母步应为 22（D3），得到 ' + T.diatonicOf(db3, 0)); fails++; }

// ⭐ 符头/休止的**时值→字形**映射（代号小 = 音长：1=全 2=二分 4=四分 8=八分 16=十六分 32=三十二分）
//   —— 2026-10-08 修：判据原来写成 `>=`，于是**每个音符都画成全音符（空心圈）**、每个休止都成全休止。
//   这条断言把两处映射钉住（源码里以后写反了，这里立刻红）。
const HEAD_MAP = [[1, 'noteheadWhole'], [2, 'noteheadHalf'], [4, 'noteheadBlack'],
  [8, 'noteheadBlack'], [16, 'noteheadBlack'], [32, 'noteheadBlack']];
const REST_MAP = [[1, 'restWhole'], [2, 'restHalf'], [4, 'restQuarter'],
  [8, 'rest8th'], [16, 'rest16th'], [32, 'rest32nd']];
HEAD_MAP.forEach(([dur, want]) => {
  const got = T.headName(dur);
  if (got !== want) { console.log('FAIL: 时值代号 ' + dur + ' 的符头应为 ' + want + '，得到 ' + got); fails++; }
});
REST_MAP.forEach(([dur, want]) => {
  const got = T.restName(dur);
  if (got !== want) { console.log('FAIL: 时值代号 ' + dur + ' 的休止应为 ' + want + '，得到 ' + got); fails++; }
});
// 顺带把"全音符没有符干、四分音符有符干、八分音符有旗子"这条也钉一下（都是同一套代号）
if (T.beats(T.norm({ duration: 1 })) !== 4) { console.log('FAIL: 全音符应是 4 拍'); fails++; }
if (T.beats(T.norm({ duration: 4 })) !== 1) { console.log('FAIL: 四分音符应是 1 拍'); fails++; }

// ⭐ 字形曲线的**控制点顺序**必须是对的 —— 这一条是 2026-10-08 真机截图抓出来的 bug 的回归闸门：
//   `notation-glyphs.js` 里的 `C` 是从 VexFlow bravura_glyphs.js 抽出来的，源里三对点的顺序是
//   **「端点 c1 c2」**，抽的时候按 SVG 的「c1 c2 端点」原样搬 ⇒ 每个符头/谱号都画成自交的"蝴蝶结"
//   （一拍上去像叉子）。判据（客观）：① 相邻段在接点处的进入/离开切向应共线（接缝总转角小）
//   ② augmentationDot 的包围盒是 144×144，轮廓到圆心距离的相对标准差该 ≈ 0。
//   ⚠️ 接缝只在**同一条子路径内**算（外轮廓 + 内孔之间本来就不相接）。
function seamTurnOf(d) {
  const t = d.match(/[MLCZz]|-?\d+(?:\.\d+)?/g).map((s) => (isNaN(+s) ? s : +s));
  const segs = [];
  let i = 0, cur = null, sub = -1;
  while (i < t.length) {
    const c = t[i++];
    if (c === 'M' || c === 'L') { cur = [t[i++], t[i++]]; if (c === 'M') sub++; }
    else if (c === 'C') {
      segs.push({ from: cur, c1: [t[i++], t[i++]], c2: [t[i++], t[i++]], to: [t[i++], t[i++]], sub: sub });
      cur = segs[segs.length - 1].to;
    }
  }
  const subv = (a, b) => [a[0] - b[0], a[1] - b[1]];
  let tot = 0;
  for (let k = 0; k < segs.length; k++) {
    const prev = segs[(k + segs.length - 1) % segs.length], nxt = segs[k];
    if (prev.sub !== nxt.sub) continue;
    const a = subv(prev.to, prev.c2), b = subv(nxt.c1, nxt.from);
    const la = Math.hypot(a[0], a[1]), lb = Math.hypot(b[0], b[1]);
    if (!la || !lb) continue;
    tot += Math.acos(Math.max(-1, Math.min(1, (a[0] * b[0] + a[1] * b[1]) / (la * lb)))) * 180 / Math.PI;
  }
  return Math.round(tot);
}
const G = T.GLYPHS;
if (!G) { console.log('FAIL: 拿不到 GLYPHS（字形表没暴露出来）'); fails++; }
else {
  const wantSmooth = ['noteheadBlack', 'noteheadHalf', 'noteheadWhole', 'noteheadDoubleWhole',
    'restWhole', 'restHalf', 'augmentationDot', 'accidentalNatural'];
  wantSmooth.forEach((k) => {
    const turn = seamTurnOf(G[k].d);
    if (turn > 30) { console.log('FAIL: 字形 ' + k + ' 的曲线接缝总转角 ' + turn + '°（应 ≤30°）—— 控制点顺序被搬错了？'); fails++; }
  });
  // 正圆点：到圆心的相对标准差
  const dot = G.augmentationDot, bb = dot.bb;
  const t2 = dot.d.match(/[MLCZz]|-?\d+(?:\.\d+)?/g).map((s) => (isNaN(+s) ? s : +s));
  const segs2 = []; let j = 0, cur2 = null;
  while (j < t2.length) {
    const c = t2[j++];
    if (c === 'M') { cur2 = [t2[j++], t2[j++]]; }
    else if (c === 'C') { const g = { from: cur2, c1: [t2[j++], t2[j++]], c2: [t2[j++], t2[j++]], to: [t2[j++], t2[j++]] }; segs2.push(g); cur2 = g.to; }
  }
  const cx0 = (bb[0] + bb[2]) / 2, cy0 = (bb[1] + bb[3]) / 2, r0 = (bb[2] - bb[0]) / 2;
  const ds = [];
  segs2.forEach((g) => {
    for (let k = 0; k <= 10; k++) {
      const u = k / 10, v = 1 - u;
      ds.push(Math.hypot(
        v * v * v * g.from[0] + 3 * v * v * u * g.c1[0] + 3 * v * u * u * g.c2[0] + u * u * u * g.to[0] - cx0,
        v * v * v * g.from[1] + 3 * v * v * u * g.c1[1] + 3 * v * u * u * g.c2[1] + u * u * u * g.to[1] - cy0));
    }
  });
  const mean = ds.reduce((a, b) => a + b, 0) / ds.length;
  const sd = Math.sqrt(ds.reduce((a, b) => a + (b - mean) ** 2, 0) / ds.length) / r0;
  if (sd > 0.02) { console.log('FAIL: augmentationDot 不够圆（相对标准差 ' + sd.toFixed(3) + '）—— 控制点顺序错了'); fails++; }
  console.log('\n字形曲线自检：平滑字形接缝 ' + wantSmooth.map((k) => k + '=' + seamTurnOf(G[k].d) + '°').join(' ') +
    ' · 圆点相对标准差 ' + sd.toFixed(3));
}

fs.writeFileSync(__dirname + '\\notation-check.html', out.join('\n'));
console.log(fails ? ('\n' + fails + ' 条断言失败') : '\n全部断言通过');
console.log('写出 notation-check.html');

// 顺带把**面板自己会发出去的那份元素 JSON** 落盘，交给 e2e.js 打真实 HTTP ——
// 这一条补的是"面板发的形状"与"Go 收的形状"之间最后一段没人验证过的路
// （编辑器方言 `kind:"rest"` 与上游语义 `note+rest:true` 的坑就是在这儿暴露的）。
const PANEL_ELS = [
  { kind: 'note', pitch: 1, octave: 0, duration: 4, dots: 0, accidental: '', lyric: '月' },
  { kind: 'note', pitch: 3, octave: 0, duration: 8, dots: 0, accidental: '', lyric: '落' },
  { kind: 'note', pitch: 5, octave: 1, duration: 4, dots: 1, accidental: 'sharp', lyric: '乌' },
  { kind: 'sustain', duration: 4, dots: 0 },
  { kind: 'rest', duration: 4, dots: 0 },
  { kind: 'note', pitch: 2, octave: -1, duration: 2, dots: 0, accidental: 'flat', lyric: '霜' },
].map(T.norm);
const json = T.elemsJSONOf(PANEL_ELS);
fs.writeFileSync(__dirname + '\\panel-elements.json', json);
console.log('写出 panel-elements.json（面板方言，' + PANEL_ELS.length + ' 个元素）：\n  ' + json);
