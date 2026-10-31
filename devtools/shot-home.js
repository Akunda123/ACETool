// 证据图：home.html 的按钮网格（哪颗按钮"改工程文件"一眼看得出）。
// 用法：node devtools/shot-home.js [url]
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
const PORT = 9780 + (process.pid % 40);
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
  if (r.exceptionDetails) throw new Error('页面抛错：' + (r.exceptionDetails.exception && r.exceptionDetails.exception.description));
  return r.result.value;
}

(async () => {
  const child = spawn(EDGE, ['--headless=new', '--disable-gpu', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-shothome-' + PORT), 'about:blank'], { stdio: 'ignore' });
  let target = null;
  for (let i = 0; i < 40 && !target; i++) {
    await sleep(250);
    try {
      const l = await (await fetch('http://127.0.0.1:' + PORT + '/json/list')).json();
      target = l.find((t) => t.type === 'page' && t.url.indexOf('devtools') < 0);
    } catch (e) { /* 还没起来 */ }
  }
  if (!target) { child.kill(); console.error('连不上 Edge'); process.exit(1); }
  const ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((res, rej) => { ws.onopen = res; ws.onerror = () => rej(new Error('WS 连不上')); });
  try {
    await cdp(ws, ++seq, 'Page.enable', {});
    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 760, height: 600, deviceScaleFactor: 2, mobile: false });
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'home.html' });
    await sleep(1800);
    console.log(await evalJS(ws, `(function(){
      var bs = Array.prototype.slice.call(document.querySelectorAll('#hgrid button.live'));
      var m = bs.filter(function(x){ return x.textContent === '轨道内复制音高线'; })[0];
      return '轨道内复制音高线 class=' + (m ? m.className : '(没找到)') + ' · 普通按钮 class=' + (bs[0] ? bs[0].className : '');
    })()`));
    // ⭐ 用户 2026-10-15：三类按钮要能一眼分开（绿=立即执行 / 蓝=开面板 / 琥珀=改工程文件）
    console.log(await evalJS(ws, `(function(){
      var out = { instant: [], panel: [], file: [], menu: [] };
      Array.prototype.slice.call(document.querySelectorAll('#hgrid button.live')).forEach(function (b) {
        var c = b.className;
        if (c.indexOf(' file') >= 0) out.file.push(b.textContent);
        else if (c.indexOf(' instant') >= 0) out.instant.push(b.textContent);
        else if (/展开菜单/.test(b.title || '')) out.menu.push(b.textContent);
        else out.panel.push(b.textContent);
      });
      return '绿(立即执行)=' + out.instant.join('/') + '\\n蓝(开面板)=' + out.panel.join('/') +
        '\\n琥珀(改工程文件)=' + out.file.join('/') + '\\n菜单=' + out.menu.join('/');
    })()`));
    const shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(__dirname, 'home-buttons.png'), Buffer.from(shot.data, 'base64'));
    console.log('落盘：devtools/home-buttons.png');

    // ⭐ 顺手证一遍「点得到」：用**真鼠标事件**点它（`el.click()` 不是可信手势，window.open 会被拦）
    const before = (await (await fetch('http://127.0.0.1:' + PORT + '/json/list')).json()).length;
    const box = JSON.parse(await evalJS(ws, `(function(){
      var bs = Array.prototype.slice.call(document.querySelectorAll('#hgrid button.live'));
      var m = bs.filter(function(x){ return x.textContent === '轨道内复制音高线'; })[0];
      if (!m) return 'null';
      var r = m.getBoundingClientRect();
      return JSON.stringify({ x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2) });
    })()`));
    if (box) {
      await cdp(ws, ++seq, 'Input.dispatchMouseEvent', { type: 'mousePressed', x: box.x, y: box.y, button: 'left', clickCount: 1 });
      await cdp(ws, ++seq, 'Input.dispatchMouseEvent', { type: 'mouseReleased', x: box.x, y: box.y, button: 'left', clickCount: 1 });
    }
    await sleep(1500);
    const list = await (await fetch('http://127.0.0.1:' + PORT + '/json/list')).json();
    const opened = list.map((t) => t.url).filter((u) => /pitch\.html/.test(u));
    console.log('用真鼠标点「轨道内复制音高线」之后：目标数 ' + before + ' → ' + list.length +
      ' · 开出的 pitch.html：' + (opened.length ? opened.join(',') : '(没有)'));
    // 说明：无头浏览器里 window.open 有时仍被拦 ⇒ 再看一眼落点对不对（点了之后有没有 toast 之类的副作用）
    console.log('  （无头环境 window.open 可能被拦；上面若为"没有"，以真浏览器为准）');
  } catch (e) {
    console.error('崩了：' + e.message);
    process.exitCode = 1;
  }
  ws.close();
  child.kill();
})();
