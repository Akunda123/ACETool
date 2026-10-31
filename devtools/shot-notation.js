// 证据图：谱面编辑器的**生成块**（用户 2026-10-07：「直接把 nt 块拿过来，下面是生成」）—— 对着真面板截。
// 只读：点的是「从 ACE 读取」（读当前片段）+「生成（干跑）」（dryRun=1，不写工程）。
// 用法：node devtools/shot-notation.js [url]
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
const PORT = 9500 + (process.pid % 90);
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
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-shotn-' + PORT), 'about:blank',
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
  await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 760, height: 1000, deviceScaleFactor: 2, mobile: false });
  try {
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + '#jianpu.editor' });
    await sleep(2200);
    for (let i = 0; i < 20 && !(await evalJS(ws, `!!document.getElementById('nt-read')`)); i++) await sleep(200);
    await evalJS(ws, `document.getElementById('nt-read').click()`);
    await sleep(1200);
    // ⚠️ 干跑按钮已按用户要求删除（2026-10-08）⇒ 走暴露出来的同一个函数（`writeACE(true)`）
    await evalJS(ws, `window.NotationPanel._test.writeACE(true)`);
    await sleep(1500);
    console.log('元素数：' + (await evalJS(ws, `document.getElementById('nt-count').textContent`)));
    console.log('生成报告：' + (await evalJS(ws, `document.getElementById('nt-out').textContent`)).split('\n').slice(0, 3).join(' / '));
    // 把「生成」那一块滚进视野（它在两排谱**下面**，默认视口看不到）
    await evalJS(ws, `(function(){ var el=document.querySelector('.nt-frame'); if(el) el.scrollIntoView({block:'end'}); return true; })()`);
    await sleep(400);
    await evalJS(ws, `(function(){ var el=document.querySelector('.nt-actions'); if(el) el.scrollIntoView({block:'center'}); return true; })()`);
    await sleep(400);
    const shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(__dirname, 'notation-gen.png'), Buffer.from(shot.data, 'base64'));
    console.log('落盘：devtools/notation-gen.png');
  } catch (e) {
    console.error('崩了：' + e.message);
    process.exitCode = 1;
  }
  ws.close();
  child.kill();
})();
