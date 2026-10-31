// 证据图：**跨行连音**（真谱写法）——现场用工具条画一个**附点全音符**（4+2 = 6 拍），
// 切成「只要五线谱 + 每行 1 小节」：它在小节线处被拆成 4 拍 + 2 拍，而每行正好一小节
// ⇒ 应看到：上一行末尾甩出连音线、下一行开头把那个音重写并接上。
// ⚠️ 不再用「示例」当素材 —— 用户 2026-10-08 把示例换成了 1-7 音阶（没有长音，也就没有连音线）。
// 只读：点工具条 +「导出预览」。用法：node devtools/shot-cross-line-tie.js [url]
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
const PORT = 9460 + (process.pid % 60);
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
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-shottie-' + PORT), 'about:blank',
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
  await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 1000, height: 820, deviceScaleFactor: 2, mobile: false });
  try {
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'notation.html' });
    await sleep(1600);
    // 现场画一个**附点全音符**（6 拍）：清空 → 时值「全」→ 附点 → 画笔音高 1 → 点末尾的「+」追加格
    await evalJS(ws, `(function(){
      document.getElementById('nt-clear').click();
      document.querySelector('[data-nt-dur="1"]').click();
      document.querySelector('[data-nt-dots="1"]').click();
      document.querySelector('[data-nt-pitch="1"]').click();
      document.querySelector('#nt-jp [data-i="append"]').click();
      return true; })()`);
    await sleep(300);
    console.log('元素：' + await evalJS(ws, `document.getElementById('nt-count').textContent`));
    await evalJS(ws, `(function(){ var r=document.getElementById('nt-rows'); r.value='staff';
      var m=document.getElementById('nt-mpl'); m.value='1'; return true; })()`);
    await evalJS(ws, `document.getElementById('nt-print-top').click()`);
    await sleep(600);
    console.log('顶栏：' + await evalJS(ws, `document.querySelector('.nt-pv-meta').textContent`));
    console.log('每行连音线数：' + await evalJS(ws, `(function(){
      var rows = Array.prototype.slice.call(document.querySelectorAll('#nt-printroot .nt-staff'));
      return rows.map(function(r){ return r.querySelectorAll('.nt-tie').length; }).join(',');
    })()`));
    const shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(__dirname, 'cross-line-tie.png'), Buffer.from(shot.data, 'base64'));
    console.log('落盘：devtools/cross-line-tie.png');
  } catch (e) {
    console.error('崩了：' + e.message);
    process.exitCode = 1;
  }
  ws.close();
  child.kill();
})();
