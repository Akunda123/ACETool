// 探针：**hgrid**（`home.html` 那个快捷按钮网格）里到底渲染了什么。
// 用户 2026-10-07：「重新检查 hgrid」—— 这个网格里有一批历史遗留的占位按钮（「内容1…N」）。
// 用法：node devtools/probe-hgrid.js [url]
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
const PORT = 9350 + (process.pid % 90);
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
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-hgrid-' + PORT), 'about:blank',
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
  try {
    await cdp(ws, ++seq, 'Page.enable', {});
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'home.html' });
    await sleep(1500);
    const dump = await evalJS(ws, `(function(){
      var g = document.getElementById('hgrid');
      var btns = Array.prototype.slice.call(g.querySelectorAll('button'));
      return JSON.stringify({
        total: btns.length,
        labels: btns.map(function(b){ return b.textContent; }),
        live: btns.filter(function(b){ return b.className.indexOf('live') >= 0; }).length,
        placeholder: btns.filter(function(b){ return b.hasAttribute('data-i'); }).length,
        cols: getComputedStyle(g).gridTemplateColumns,
        scrollW: g.scrollWidth, clientW: g.clientWidth,
      });
    })()`);
    const d = JSON.parse(dump);
    console.log('按钮总数：' + d.total + '（真按钮 ' + d.live + ' + 占位「内容N」' + d.placeholder + '）');
    console.log('列：' + d.cols);
    console.log('横向溢出：' + (d.scrollW > d.clientW ? ('是（' + d.scrollW + ' > ' + d.clientW + '）') : '否'));
    console.log('标签：' + d.labels.join(' | '));
  } catch (e) {
    console.error('崩了：' + e.message);
    process.exitCode = 1;
  }
  ws.close();
  child.kill();
})();
