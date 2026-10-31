// 证据图：① 主页面（home.html）的按钮网格里多了「谱面编辑器」；② 它开的子窗口 `notation.html`。
// 只读：子窗口里点的是「从 ACE 读取」（读当前片段）。用法：node devtools/shot-notation-window.js [url]
const { spawn } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const EDGE = [
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
].find((p) => fs.existsSync(p));
const PANEL = path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json');
const BASE = process.argv[2] || JSON.parse(fs.readFileSync(PANEL, 'utf8')).url;
const PORT = 9280 + (process.pid % 90);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function cdp(ws, id, method, params) {
  return new Promise((res, rej) => {
    const onMsg = (ev) => {
      const m = JSON.parse(ev.data);
      if (m.id === id) { ws.removeEventListener('message', onMsg); m.error ? rej(new Error(JSON.stringify(m.error))) : res(m.result); }
    };
    ws.addEventListener('message', onMsg);
    ws.send(JSON.stringify({ id, method, params: params || {} }));
    setTimeout(() => { ws.removeEventListener('message', onMsg); rej(new Error(method + ' 超时')); }, 20000);
  });
}
let seq = 100;
async function evalJS(ws, expr) {
  const r = await cdp(ws, ++seq, 'Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true });
  if (r.exceptionDetails) throw new Error('页面里抛错：' + ((r.exceptionDetails.exception && r.exceptionDetails.exception.description) || r.exceptionDetails.text));
  return r.result.value;
}

(async () => {
  const child = spawn(EDGE, [
    '--headless=new', '--disable-gpu', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-shotw2-' + PORT), 'about:blank',
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
  try {
    // ① 主页面网格
    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 620, height: 520, deviceScaleFactor: 2, mobile: false });
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'home.html' });
    await sleep(1400);
    console.log('主页面按钮：' + (await evalJS(ws, `Array.prototype.slice.call(document.querySelectorAll('#hgrid button.live')).map(function(b){return b.textContent}).join(' | ')`)));
    let shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(__dirname, 'home-notation.png'), Buffer.from(shot.data, 'base64'));
    console.log('落盘：devtools/home-notation.png');

    // ② 子窗口（真数据读一遍）
    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 780, height: 880, deviceScaleFactor: 2, mobile: false });
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'notation.html' });
    await sleep(1800);
    for (let i = 0; i < 20 && !(await evalJS(ws, `!!document.getElementById('nt-read')`)); i++) await sleep(200);
    await evalJS(ws, `document.getElementById('nt-read').click()`);
    // ⚠️ 真读要 1 秒上下（走 ACE），**别用固定 sleep** —— 等计数变了再截，
    // 否则截到的是"读 ACE 当前片段…"那一瞬间（第一次就是这么截空的）。
    for (let i = 0; i < 30 && /^0 个元素/.test(await evalJS(ws, `document.getElementById('nt-count').textContent`)); i++) await sleep(200);
    await sleep(300);
    console.log('子窗口元素数：' + (await evalJS(ws, `document.getElementById('nt-count').textContent`)));
    await evalJS(ws, `(function(){ var el=document.querySelector('.nt-actions'); if(el) el.scrollIntoView({block:'center'}); return true; })()`);
    await sleep(300);
    shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(__dirname, 'notation-window.png'), Buffer.from(shot.data, 'base64'));
    console.log('落盘：devtools/notation-window.png');
  } catch (e) {
    console.error('崩了：' + e.message);
    process.exitCode = 1;
  }
  ws.close();
  child.kill();
})();
