// 一次性核对：**每页是不是 A4 大小**（用户 2026-10-08：「不是应该按照 A4 大小来吗」）。
// 判据：① 屏幕上那张"纸"的**实际 mm 尺寸** = 297×210（横向）/ 210×297（竖向）；
//       ② 每页内容**不超出**纸的内容区（277×190 / 190×277）；
//       ③ 打印时 `@page` 的纸型跟着换、`margin:0`（页边距由纸自己的 padding 给）。
// 用法：node devtools/probe-paper.js [url]
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
const PORT = 9190 + (process.pid % 60);
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
const MM = 96 / 25.4;   // 1mm = 3.7795 CSS px

(async () => {
  const child = spawn(EDGE, [
    '--headless=new', '--disable-gpu', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-paper-' + PORT), 'about:blank',
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
  await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 1400, height: 900, deviceScaleFactor: 1, mobile: false });
  await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'notation.html' });
  await sleep(1600);
  await evalJS(ws, `document.getElementById('nt-demo').click()`);
  await sleep(250);
  for (const orient of ['landscape', 'portrait']) {
    await evalJS(ws, `(function(){ var p=document.getElementById('nt-paper'); p.value='${orient}'; return true; })()`);
    await evalJS(ws, `document.getElementById('nt-print-top').click()`);
    await sleep(400);
    const d = JSON.parse(await evalJS(ws, `(function(){
      var MM = 96 / 25.4;
      var pages = Array.prototype.slice.call(document.querySelectorAll('#nt-printroot .nt-print-page'));
      var first = pages[0];
      var cs = getComputedStyle(first);
      var box = first.getBoundingClientRect();
      var svgs = Array.prototype.slice.call(first.querySelectorAll('svg'));
      var inner = svgs.map(function(s){ var b = s.getBoundingClientRect(); return [Math.round(b.width / MM), Math.round(b.height / MM)]; });
      // 内容（页眉 + 所有行）有没有超出内容区
      var contentMM = [0, 0];
      Array.prototype.slice.call(first.children).forEach(function(ch){
        var b = ch.getBoundingClientRect();
        contentMM[0] = Math.max(contentMM[0], Math.round(b.width / MM));
        contentMM[1] = Math.max(contentMM[1], Math.round(b.height / MM));
      });
      return JSON.stringify({
        pages: pages.length,
        paperMM: [Math.round(box.width / MM), Math.round(box.height / MM)],
        paddingMM: [Math.round(parseFloat(cs.paddingTop) / MM), Math.round(parseFloat(cs.paddingLeft) / MM)],
        innerMM: [Math.round((box.width - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight)) / MM),
                  Math.round((box.height - parseFloat(cs.paddingTop) - parseFloat(cs.paddingBottom)) / MM)],
        svgMM: inner,
        contentMM: contentMM,
        paperStyle: (document.getElementById('nt-paper-style') || {}).textContent || '(无)',
        meta: document.querySelector('.nt-pv-meta').textContent,
      });
    })()`));
    console.log('[' + orient + '] 纸 = ' + d.paperMM[0] + '×' + d.paperMM[1] + 'mm · 边距 ' + d.paddingMM[0] +
      'mm · 内容区 ' + d.innerMM[0] + '×' + d.innerMM[1] + 'mm · 每页 ' + d.pages + ' 张');
    console.log('       谱面 svg = ' + d.svgMM.map(function (x) { return x[0] + '×' + x[1] + 'mm'; }).join(' / ') +
      ' · 页内内容占 ' + d.contentMM[0] + '×' + d.contentMM[1] + 'mm · ' + d.meta);
    console.log('       @page ⇒ ' + d.paperStyle);
  }
  ws.close();
  child.kill();
})();
