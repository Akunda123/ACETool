// 量版式：**这个页面到底有多宽，谁把窗口撑爆了**。顺带能给一张**尺寸精确**的截图。
//
// 起因：RGB 窗口的撤销警示条被右边缘切掉（无头截图一眼看到），顶栏的「未连接」也不见了。
// 目测只能得出"好像超了"，所以这里连 CDP 直接问浏览器要数字。
//
// ⭐⭐ 这一量还揪出了**截图管线自己的假象**：`msedge --headless=new --window-size=460,620 --screenshot`
//   在**这台机器上量出来 innerWidth=504**（光栅 460 宽 ⇒ 右边 44 px 从来没进过 PNG），
//   而当时页面**根本没有横向溢出**。也就是说："看起来被切了"是**截图裁的**，不是版式坏了 ——
//   差点按假象去改 CSS。教训：**截图先确认光栅尺寸 = 视口尺寸**，否则它在骗你。
//   ⇒ 现在改成用 CDP `Emulation.setDeviceMetricsOverride` **精确指定视口**、
//     用 `Page.captureScreenshot` **精确出图**（两条走的是同一个宽度，不再各说各话）。
//
// 用法：node devtools/measure-layout.js [页面路径] [宽] [高] [截图输出.png] [沉降毫秒]
//   node devtools/measure-layout.js rgb.html 460 620
//   node devtools/measure-layout.js rgb.html 460 620 devtools/rgb-warn.png
//
// ⚠️ **沉降时间不能省**：这些页面上屏后还要打 `/api/run`（每条都是 CLI 进程往返）才填内容，
// 一上来就截会拍到「读取中…」的空壳（真踩过：截图 5 KB、列表全空，看着像功能坏了）。
// 所以这里先等 `document.readyState === 'complete'`，再等 `settle` 毫秒（默认 1500）。
//
// 依赖：Edge（`msedge --headless=new --remote-debugging-port`）+ Node 自带的全局 WebSocket（≥22）。
const { spawn } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const EDGE = [
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
].find((p) => fs.existsSync(p));
if (!EDGE) { console.error('找不到 msedge.exe'); process.exit(1); }

function panelUrl() {
  const p = path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json');
  return JSON.parse(fs.readFileSync(p, 'utf8')).url;
}

const page = process.argv[2] || '';
const W = Number(process.argv[3] || 460);
const H = Number(process.argv[4] || 620);
const SHOT = process.argv[5] || '';
const SETTLE = Number(process.argv[6] || 1500);
const PORT = 9333 + (process.pid % 500);
const URL_ = panelUrl() + page;

const EXPR = `JSON.stringify((function(){
  var vw = window.innerWidth, vh = window.innerHeight;
  var over = [];
  document.querySelectorAll('*').forEach(function(e){
    var r = e.getBoundingClientRect();
    if (r.right > vw + 0.5 && r.width > 0) {
      over.push({ sel: e.tagName.toLowerCase() + (e.id ? '#' + e.id : '') + (typeof e.className === 'string' && e.className ? '.' + e.className.trim().split(/\\s+/).join('.') : ''),
                  right: Math.round(r.right), width: Math.round(r.width),
                  text: (e.textContent || '').trim().slice(0, 24) });
    }
  });
  return { vw: vw, vh: vh,
           bodyScrollW: document.body.scrollWidth, docScrollW: document.documentElement.scrollWidth,
           bodyClientW: document.body.clientWidth,
           over: over.slice(0, 25) };
})())`;

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// 极简 CDP：发一条、等它那条回包
function cdp(ws, id, method, params) {
  return new Promise((res, rej) => {
    const onMsg = (ev) => {
      const m = JSON.parse(ev.data);
      if (m.id === id) { ws.removeEventListener('message', onMsg); m.error ? rej(new Error(method + '：' + JSON.stringify(m.error))) : res(m.result); }
    };
    ws.addEventListener('message', onMsg);
    ws.send(JSON.stringify({ id: id, method: method, params: params || {} }));
    setTimeout(() => { ws.removeEventListener('message', onMsg); rej(new Error(method + ' 超时')); }, 15000);
  });
}

(async () => {
  const child = spawn(EDGE, [
    '--headless=new', '--disable-gpu', '--hide-scrollbars',
    '--remote-debugging-port=' + PORT,
    '--window-size=' + W + ',' + H,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-measure-' + PORT),
    URL_,
  ], { stdio: 'ignore' });

  let target = null;
  for (let i = 0; i < 40 && !target; i++) {
    await sleep(250);
    try {
      const list = await (await fetch('http://127.0.0.1:' + PORT + '/json/list')).json();
      target = list.find((t) => t.type === 'page' && t.url.indexOf('devtools') < 0);
    } catch (e) { /* 还没起来 */ }
  }
  if (!target) { child.kill(); console.error('连不上 Edge 的调试口'); process.exit(1); }

  const ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((res, rej) => { ws.onopen = res; ws.onerror = () => rej(new Error('WS 连不上')); });

  try {
    // ⭐ 精确锁定视口（并显式把缩放钉成 1，免得又出现"光栅 ≠ 视口"）
    await cdp(ws, 1, 'Emulation.setDeviceMetricsOverride',
      { width: W, height: H, deviceScaleFactor: 1, mobile: false });
    // 等页面**真的加载完**（readyState），再给一段沉降时间让页内那些 `/api/run` 落下来
    for (let i = 0; i < 60; i++) {
      const r0 = await cdp(ws, 10 + i, 'Runtime.evaluate',
        { expression: 'document.readyState', returnByValue: true });
      if (r0.result.value === 'complete') break;
      await sleep(100);
    }
    await sleep(SETTLE);
    const r = await cdp(ws, 2, 'Runtime.evaluate', { expression: EXPR, returnByValue: true });
    const result = JSON.parse(r.result.value);

    console.log('页面：' + URL_);
    console.log('视口 ' + result.vw + '×' + result.vh + ' · body.clientWidth=' + result.bodyClientW
      + ' · body.scrollWidth=' + result.bodyScrollW + ' · document.scrollWidth=' + result.docScrollW);
    if (result.bodyScrollW > result.vw) {
      console.log('⚠️ **横向溢出 ' + (result.bodyScrollW - result.vw) + ' px**（页面比窗口宽，右边会被切掉）');
      console.log('顶到右边缘的元素：');
      for (const o of result.over) console.log('  right=' + String(o.right).padStart(4) + ' w=' + String(o.width).padStart(4) + '  ' + o.sel + '   「' + o.text + '」');
    } else {
      console.log('没有横向溢出 ✓');
    }

    if (SHOT) {
      const shot = await cdp(ws, 3, 'Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
      fs.writeFileSync(SHOT, Buffer.from(shot.data, 'base64'));
      const size = fs.statSync(SHOT).size;
      console.log('截图 ' + SHOT + '（' + W + '×' + H + '，' + size + ' B）—— ⚠️ 出图与量尺寸**走同一个视口**，不会再出现"图比视口窄"');
    }
  } catch (e) {
    console.error(e.message);
  }

  ws.close();
  child.kill();
  await sleep(300);
  process.exit(0);
})();

