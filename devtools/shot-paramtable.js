// 证据图：**参数表**（用户 2026-10-07「参数展开…v1 就实参和包络两列」）—— 对着**真面板、真花名册**截。
//
// 为什么用「跨轨复制」窗口：它能让目标轨选到**轨 1（v1 `verse24`）**，于是表里就是真机的两列
// （实参→direct、包络→envelope），还能看到 `formant` 只有包络 ⇒ 实参那格是「—」。
// 全程**只读 + 干跑**：只调 `view.minimap` / `xcopy.view`（都不是写工具），不按预览不动工程。
//
// 用法：node devtools/shot-paramtable.js [url]
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
const PORT = 9700 + (process.pid % 90);
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
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-shot-' + PORT), 'about:blank',
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
  await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 620, height: 820, deviceScaleFactor: 2, mobile: false });

  try {
    // ① 跨轨窗口：目标轨切到**轨 1（真机 v1）**，勾上参数曲线 ⇒ 表里就是真机的两列
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'xcopy.html' });
    await sleep(1800);
    for (let i = 0; i < 20 && !(await evalJS(ws, `!!S.mm && !!S.cur`)); i++) await sleep(200);
    await evalJS(ws, `(function(){ $('dst').value='1'; $('dst').dispatchEvent(new Event('change')); $('params').checked=true; return true; })()`);
    for (let i = 0; i < 20 && !(await evalJS(ws, `!!S.roster && (S.roster.paramColumns||[]).length > 0`)); i++) await sleep(200);
    await evalJS(ws, `renderParamTable()`);
    await sleep(300);
    const info = await evalJS(ws, `JSON.stringify({eng:(S.roster||{}).engineGeneration,
      cols:((S.roster||{}).paramColumns||[]).map(function(c){return c.label+'->'+c.dstLayer}),
      cells:Array.from($('ptab').querySelectorAll('input[data-p]')).map(function(i){return i.getAttribute('data-p')+'.'+i.getAttribute('data-c')+'='+i.checked}),
      na:Array.from($('ptab').querySelectorAll('td.na')).length,
      hint:$('ptHint').textContent})`);
    console.log('真机参数表：' + info);
    let shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(__dirname, 'param-table-v1.png'), Buffer.from(shot.data, 'base64'));
    console.log('落盘：devtools/param-table-v1.png');

    // ② 轨内窗口（当前片段 = 真机轨 0 = v2）⇒ 一列（实参 → user）
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'within.html' });
    await sleep(1800);
    for (let i = 0; i < 20 && !(await evalJS(ws, `!!S.view`)); i++) await sleep(200);
    await evalJS(ws, `(function(){ $('params').checked=true; renderParamTable(); return true; })()`);
    await sleep(300);
    console.log('真机参数表（当前片段）：' + (await evalJS(ws, `JSON.stringify({eng:(S.view||{}).engineGeneration,
      cols:((S.view||{}).paramColumns||[]).map(function(c){return c.label+'->'+c.dstLayer}),
      rows:Array.from($('ptab').querySelectorAll('td.pname')).map(function(t){return t.textContent})})`)));
    shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(__dirname, 'param-table-v2.png'), Buffer.from(shot.data, 'base64'));
    console.log('落盘：devtools/param-table-v2.png');
  } catch (e) {
    console.error('崩了：' + e.message);
    process.exitCode = 1;
  }
  ws.close();
  child.kill();
})();
