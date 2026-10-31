// 把 ACE 那个标记（`SVAgent\electron\src\ace-mark-anim.svg` 里的**字母 a**）渲染成**托盘图标**要用的 PNG。
//
// 为什么要脚本而不是手搓：
//   · SVG 只有浏览器/Illustrator 能光栅化，我们要**透明底 + 精确像素对齐**；
//   · 托盘要用好几个尺寸（100%=16、125%=20、150%=24、200%=32、250%=40、300%=48），
//     每一档都让**浏览器**从矢量直接画 ⇒ 比"一张大图缩小"清楚得多；
//   · 尺寸/取景框要可复现：换素材或改 padding 时重跑一遍就行。
//
// 做法：无头 Edge + CDP。页面里只放**那一条 path**（`fill:#000`，透明底），
// 用 `getBBox()` 量出字形真实包围盒 ⇒ 按包围盒 + 6% 留白定 viewBox（**不按 480×480 的整张画布**，
// 否则字形只占一小块、16px 上就糊成一点）⇒ 逐尺寸截 1:1 的图。
//
// 产物：`internal/tray/icon/tray-<边长>.png`（透明底、黑色 a）。
// 用法：node devtools/make-tray-icon.js [svg 路径]
const { spawn } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const EDGE = [
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
].find((p) => fs.existsSync(p));
const SVG = process.argv[2] || 'C:\\Users\\wujichao\\Documents\\SVAgent\\electron\\src\\ace-mark-anim.svg';
const OUT = path.join(__dirname, '..', 'internal', 'tray', 'icon');
const SIZES = [16, 20, 24, 32, 40, 48];
const PORT = 9700 + (process.pid % 60);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function cdp(ws, id, method, params) {
  return new Promise((res, rej) => {
    const onMsg = (ev) => {
      const m = JSON.parse(ev.data);
      if (m.id === id) { ws.removeEventListener('message', onMsg); m.error ? rej(new Error(JSON.stringify(m.error))) : res(m.result); }
    };
    ws.addEventListener('message', onMsg);
    ws.send(JSON.stringify({ id, method, params: params || {} }));
    setTimeout(() => { ws.removeEventListener('message', onMsg); rej(new Error(method + ' 超时')); }, 25000);
  });
}
let seq = 100;
async function evalJS(ws, expr) {
  const r = await cdp(ws, ++seq, 'Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true });
  if (r.exceptionDetails) throw new Error('页面里抛错：' + ((r.exceptionDetails.exception && r.exceptionDetails.exception.description) || r.exceptionDetails.text));
  return r.result.value;
}

/* 从源 SVG 里抠出**字母 a 那条 path**（它是 `id="p1"` / `class="ace-a"` 那条）。
 * ⚠️ 不写死 d 属性：换素材时这里跟着变，别抄一份坐标进来。 */
function extractA(svg) {
  const m = svg.match(/<path\s+id="p1"\s+class="ace-a"\s+d="([^"]+)"/);
  if (!m) throw new Error('在这份 SVG 里没找到 id="p1" class="ace-a" 那条 path（文件结构变了？）');
  return m[1];
}

(async () => {
  if (!EDGE) { console.error('找不到 Edge'); process.exit(1); }
  const svg = fs.readFileSync(SVG, 'utf8');
  const d = extractA(svg);
  fs.mkdirSync(OUT, { recursive: true });

  const page = '<!doctype html><meta charset="utf-8">' +
    '<style>html,body{margin:0;background:transparent;overflow:hidden}' +
    '#s{display:block}</style>' +
    // ⚠️ 两条**同一条 path**：底下那条只描边（白色光晕），上面那条只填充（黑色）。
    // 描边是**沿轮廓居中**的 ⇒ 内侧那半被上面的黑填充盖掉，露在外面的就是一圈白边。
    // 为什么用两条 path 而不是 `paint-order`：两条最直白，也不依赖 Chromium 的实现细节。
    '<svg id="s" xmlns="http://www.w3.org/2000/svg">' +
    '<path id="halo" fill="none" stroke="#fff" stroke-linejoin="round" stroke-linecap="round" d="' + d + '"/>' +
    '<path id="p" fill="#000" d="' + d + '"/></svg>';

  /* 白边有多粗（**按目标像素算**，不是写死的用户单位）：16px 上 0.5px、24px 上 0.75px、48px 上 1.5px
     —— 用户 2026-10-17：「可以给 a 加一个小的白色描边」。转成用户单位要除以"每单位多少像素"，
     那个比例随 viewBox 与目标尺寸变，所以在每一档里现算（见下面 `sw`）。 */
  const haloPx = (n) => Math.max(0.5, n / 32);

  const child = spawn(EDGE, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-tray-' + PORT),
    'data:text/html;charset=utf-8,' + encodeURIComponent(page),
  ], { stdio: 'ignore' });
  let target = null;
  for (let i = 0; i < 40 && !target; i++) {
    await sleep(250);
    try {
      const list = await (await fetch('http://127.0.0.1:' + PORT + '/json/list')).json();
      target = list.find((t) => t.type === 'page' && t.url.indexOf('devtools') < 0);
    } catch (e) { /* 还没起来 */ }
  }
  if (!target) { child.kill(); console.error('连不上 Edge'); process.exit(1); }
  const ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((res, rej) => { ws.onopen = res; ws.onerror = () => rej(new Error('WS 连不上')); });
  await cdp(ws, ++seq, 'Page.enable', {});
  await cdp(ws, ++seq, 'Runtime.enable', {});
  // 透明底：不设这条，截图就是白底（白底上黑 a 能用，但托盘图标要 alpha）
  await cdp(ws, ++seq, 'Emulation.setDefaultBackgroundColorOverride',
    { color: { r: 0, g: 0, b: 0, a: 0 } });
  await sleep(400);

  const bb = JSON.parse(await evalJS(ws,
    `(function(){var b=document.getElementById('p').getBBox();return JSON.stringify({x:b.x,y:b.y,w:b.width,h:b.height})})()`));
  console.log('字形包围盒（480 画布内）：' + JSON.stringify(bb));
  const pad = Math.max(bb.w, bb.h) * 0.06; // 留白：托盘上贴边会显得比邻居大
  const vb = [bb.x - pad, bb.y - pad, bb.w + pad * 2, bb.h + pad * 2].map((v) => v.toFixed(2)).join(' ');

  for (const n of SIZES) {
    // 白边：把"目标像素"折算成 viewBox 用户单位（描边居中 ⇒ 露在外面正好一半）
    const vbW = bb.w + pad * 2;
    const sw = (2 * haloPx(n) * vbW) / n;
    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride',
      { width: n, height: n, deviceScaleFactor: 1, mobile: false });
    await evalJS(ws, `(function(){var s=document.getElementById('s');
      s.setAttribute('width',${n}); s.setAttribute('height',${n});
      s.setAttribute('viewBox','${vb}');
      document.getElementById('halo').setAttribute('stroke-width','${sw.toFixed(3)}');
      return true})()`);
    await sleep(120);
    const shot = await cdp(ws, ++seq, 'Page.captureScreenshot', {
      format: 'png', captureBeyondViewport: true,
      clip: { x: 0, y: 0, width: n, height: n, scale: 1 },
    });
    const buf = Buffer.from(shot.data, 'base64');
    const file = path.join(OUT, 'tray-' + n + '.png');
    fs.writeFileSync(file, buf);
    // 自检：PNG 头对不对 + 至少有点不透明的像素（全透明说明取景框画到空白上了）
    const okHeader = buf.slice(1, 4).toString('ascii') === 'PNG';
    console.log('  ' + path.basename(file) + '  ' + buf.length + ' 字节' + (okHeader ? '' : '  ⚠️ 不是 PNG'));
  }
  /* ⭐ 面板窗口的图标（用户 2026-10-17：「能把开启的 web 面板的图标换一下吗？」）：
     同一份字形渲染成 64px 的 PNG，落到 `internal/web/assets/icon.png`，
     每个页面 `<link rel="icon">` 指它 ⇒ 浏览器 **app 模式的窗口**在任务栏/标题栏就用这个图标
     （不指的话是浏览器默认那个"空白页"图标）。64px 是为了让 150%/200% 缩放下也清楚。 */
  {
    const n = 64;
    const vbW = bb.w + pad * 2;
    const sw = (2 * haloPx(n) * vbW) / n;
    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride',
      { width: n, height: n, deviceScaleFactor: 1, mobile: false });
    await evalJS(ws, `(function(){var s=document.getElementById('s');
      s.setAttribute('width',${n}); s.setAttribute('height',${n});
      s.setAttribute('viewBox','${vb}');
      // 窗口图标**不带白描边**（见下面 SVG 那段注释）
      document.getElementById('halo').setAttribute('stroke-width','0');
      return true})()`);
    await sleep(150);
    const shot = await cdp(ws, ++seq, 'Page.captureScreenshot', {
      format: 'png', captureBeyondViewport: true,
      clip: { x: 0, y: 0, width: n, height: n, scale: 1 },
    });
    const buf = Buffer.from(shot.data, 'base64');
    const file = path.join(OUT, '..', '..', 'web', 'assets', 'icon.png');
    fs.writeFileSync(file, buf);
    console.log('  ' + path.basename(file) + '  ' + buf.length + ' 字节（面板窗口图标）');
  }
  /* ⭐ 再出一份 **SVG** 给面板窗口用（用户 2026-10-17：「能把开启的 web 面板的图标换一下吗？」
     → 换完又说「这个图标有点模糊」）。
     为什么换成矢量：64px 的 PNG 被任务栏缩到 24/36px 就是糊的（光栅图缩小的必然结果）；
     `<link rel="icon" type="image/svg+xml">` 让浏览器**按目标尺寸现画**，24/32/48 都一样清楚。
     ⚠️ 窗口图标**不带白描边**（和托盘那几张不同）：白边在浅色任务栏上根本看不见，
     只会让小尺寸的黑字边缘多一圈浅灰、看着发虚 —— 用户的反馈就是"模糊"。
     托盘那几张仍带白边（托盘可能是深色任务栏）。 */
  {
    const svg = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="' + vb + '" width="64" height="64">\n' +
      '  <path fill="#000" d="' + d + '"/>\n' +
      '</svg>\n';
    const file = path.join(OUT, '..', '..', 'web', 'assets', 'icon.svg');
    fs.writeFileSync(file, svg, 'utf8');
    console.log('  ' + path.basename(file) + '  ' + svg.length + ' 字节（面板窗口图标 · 矢量 · 无白边）');
  }
  /* 证据图：把做好的图标**分别铺在浅色和深色底上**。
     为什么要它：白描边在"透明底"上根本看不出来（看图工具多半把透明画成白底），
     必须放在两种底色上肉眼确认"黑字 + 白边"在托盘常见的两种背景上都立得住。
     落 `devtools/tray-preview.png`（不进 embed）。 */
  const durl = (n) => 'data:image/png;base64,' +
    fs.readFileSync(path.join(OUT, 'tray-' + n + '.png')).toString('base64');
  const cell = (bg, label) => '<div class="cell" style="background:' + bg + '">' +
    '<img src="' + durl(48) + '" width="48" height="48">' +
    '<img src="' + durl(24) + '" width="24" height="24">' +
    '<img src="' + durl(16) + '" width="16" height="16">' +
    '<span style="color:' + (bg === '#1b1b1b' ? '#ddd' : '#333') + '">' + label + '</span></div>';
  const prev = '<!doctype html><meta charset="utf-8"><style>' +
    'html,body{margin:0;font:11px "Microsoft YaHei",sans-serif}' +
    '.row{display:flex}.cell{display:flex;align-items:center;gap:10px;padding:10px 14px}' +
    '</style><div class="row">' + cell('#1b1b1b', '深色托盘') + cell('#f3f3f3', '浅色托盘') + '</div>';
  await cdp(ws, ++seq, 'Page.navigate', { url: 'data:text/html;charset=utf-8,' + encodeURIComponent(prev) });
  await sleep(500);
  await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 400, height: 92, deviceScaleFactor: 2, mobile: false });
  await sleep(200);
  const ps = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
  fs.writeFileSync(path.join(__dirname, 'tray-preview.png'), Buffer.from(ps.data, 'base64'));
  console.log('  证据图：devtools/tray-preview.png（深/浅两种托盘底）');

  ws.close();
  child.kill();
})().catch((e) => { console.error('炸了：' + e.stack); process.exit(2); });
