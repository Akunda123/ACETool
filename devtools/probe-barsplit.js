// 一次性核对：**跨小节线拆开**（用户 2026-10-07「按小节线拆开」）。
//
// 现场画一个**附点全音符**（4+2 = 6 拍）：每行 1 小节 ⇒ 它跨过小节线 ⇒ 应当被拆成
// "这一小节满的 4 拍（全音符）+ 下一小节接的 2 拍（二分，带连音线）"。
// 逐行打出来看：每行的**时值总数必须是 4 拍**（真谱里一小节就是一小节）。
// ⚠️ 不再用「示例」当素材 —— 用户 2026-10-08 把示例换成了 1-7 音阶（没有长音）。
// 用法：node devtools/probe-barsplit.js [url]
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
const PORT = 9130 + (process.pid % 60);
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

(async () => {
  const child = spawn(EDGE, [
    '--headless=new', '--disable-gpu', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-bsp-' + PORT), 'about:blank',
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
  await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'notation.html' });
  await sleep(1600);
  await evalJS(ws, `(function(){
    document.getElementById('nt-clear').click();
    document.querySelector('[data-nt-dur="1"]').click();
    document.querySelector('[data-nt-dots="1"]').click();
    document.querySelector('[data-nt-pitch="1"]').click();
    document.querySelector('#nt-jp [data-i="append"]').click();
    return true; })()`);
  await sleep(300);
  console.log('元素：' + await evalJS(ws, `document.getElementById('nt-count').textContent`));
  await evalJS(ws, `(function(){ var r=document.getElementById('nt-rows'); r.value='both';
    var m=document.getElementById('nt-mpl'); m.value='1'; return true; })()`);
  await evalJS(ws, `document.getElementById('nt-print-top').click()`);
  await sleep(700);
  const dump = await evalJS(ws, `(function(){
    var out = [];
    var rows = Array.prototype.slice.call(document.querySelectorAll('#nt-printroot .nt-staff'));
    rows.forEach(function(r, i){
      var heads = 0, whole = 0, half = 0, quarter = 0, rest = 0;
      r.querySelectorAll('.nt-head path').forEach(function(p){ heads++; });
      out.push({ row: i + 1, heads: heads, rests: r.querySelectorAll('.nt-rest').length,
        ties: r.querySelectorAll('.nt-tie').length, flags: r.querySelectorAll('.nt-flag').length });
    });
    // 简谱那排：数每行的数字 / 延音号 / 音内延长 个数（拆开的后半截在简谱里应当是延音号）
    // ⚠️ 这段是模板串：注释里**不能出现反引号**（连注释也不行）
    var jp = [];
    Array.prototype.slice.call(document.querySelectorAll('#nt-printroot .nt-jp')).forEach(function(g, i){
      jp.push({ row: i + 1, nums: g.querySelectorAll('.nt-num').length,
        dashes: g.querySelectorAll('.nt-dash').length, holds: g.querySelectorAll('.nt-hold').length });
    });
    return JSON.stringify({ staff: out, jianpu: jp, meta: document.querySelector('.nt-pv-meta').textContent });
  })()`);
  const d = JSON.parse(dump);
  console.log('顶栏：' + d.meta);
  console.log('五线谱每行：');
  d.staff.forEach(function (r) { console.log('  第 ' + r.row + ' 行：符头 ' + r.heads + ' · 休止 ' + r.rests + ' · 连音线 ' + r.ties + ' · 旗子 ' + r.flags); });
  console.log('简谱每行：');
  d.jianpu.forEach(function (r) { console.log('  第 ' + r.row + ' 行：数字 ' + r.nums + ' · 延音 `-` ' + r.dashes + ' · 音内延长 ' + r.holds); });
  ws.close();
  child.kill();
})();
