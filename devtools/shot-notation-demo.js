// 证据图：面板「示例」（1-7 音阶）铺在双联谱上时的样子 —— 专门用来看**界面本身**
// （顶栏 / 工具条 / 图例 / 生成块 / 导出那一行的疏密），不需要 ACE 在线。
// 只读：点「示例」+「导出预览」，不碰工程。用法：node devtools/shot-notation-demo.js [url]
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
const PORT = 9540 + (process.pid % 90);
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
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-shotd-' + PORT), 'about:blank',
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
  await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 820, height: 1020, deviceScaleFactor: 2, mobile: false });
  try {
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'notation.html' });
    await sleep(1800);
    for (let i = 0; i < 20 && !(await evalJS(ws, `!!document.getElementById('nt-demo')`)); i++) await sleep(200);
    await evalJS(ws, `document.getElementById('nt-demo').click()`);
    await sleep(500);
    console.log('元素：' + await evalJS(ws, `document.getElementById('nt-count').textContent`));
    console.log('顶栏按钮：' + await evalJS(ws, `(function(){
      var bar = document.querySelector('.nt-bar');
      return Array.prototype.slice.call(bar.querySelectorAll('button')).map(function(b){return b.textContent}).join(' | ');
    })()`));
    console.log('底部那两行：' + await evalJS(ws, `(function(){
      return Array.prototype.slice.call(document.querySelectorAll('.nt-gen button, .nt-actions2 button')).map(function(b){return b.textContent}).join(' | ');
    })()`));
    console.log('图例：' + await evalJS(ws, `document.querySelector('.nt-legend').textContent`));
    console.log('导出那一行的宽度账：' + await evalJS(ws, `(function(){
      var row = document.querySelector('.nt-actions2');
      var kids = Array.prototype.slice.call(row.children).map(function(el){
        return (el.id || el.tagName.toLowerCase()) + '=' + Math.round(el.getBoundingClientRect().width);
      });
      return '行宽 ' + Math.round(row.getBoundingClientRect().width) + ' · ' + kids.join(' ');
    })()`));
    let shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(__dirname, 'notation-demo.png'), Buffer.from(shot.data, 'base64'));
    console.log('落盘：devtools/notation-demo.png（整页）');

    // 高倍局部：五线谱那一排（放大看符头/连音线/字形对不对，缩略图看不出来）
    const r = JSON.parse(await evalJS(ws, `(function(){
      var b = document.getElementById('nt-st').getBoundingClientRect();
      return JSON.stringify({ x: b.x, y: b.y, width: b.width, height: b.height });
    })()`));
    shot = await cdp(ws, ++seq, 'Page.captureScreenshot', {
      format: 'png', clip: { x: r.x, y: r.y, width: Math.min(r.width, 560), height: r.height, scale: 3 },
    });
    fs.writeFileSync(path.join(__dirname, 'notation-demo-staff.png'), Buffer.from(shot.data, 'base64'));
    console.log('落盘：devtools/notation-demo-staff.png（五线谱放大：' + Math.round(r.width) + '×' + Math.round(r.height) + '）');

    // 再放大一个**符头**（小到 60×40 px 才不会被压缩糊掉）—— 字形曲线的控制点顺序对不对，全在这张上
    const h = JSON.parse(await evalJS(ws, `(function(){
      var hs = document.querySelectorAll('#nt-st .nt-head');
      var b = hs[4].getBoundingClientRect();
      return JSON.stringify({ x: b.x - 8, y: b.y - 8, width: b.width + 16, height: b.height + 16 });
    })()`));
    shot = await cdp(ws, ++seq, 'Page.captureScreenshot', {
      format: 'png', clip: { x: h.x, y: h.y, width: h.width, height: h.height, scale: 8 },
    });
    fs.writeFileSync(path.join(__dirname, 'notation-demo-head.png'), Buffer.from(shot.data, 'base64'));
    console.log('落盘：devtools/notation-demo-head.png（单个符头放大 8×：' + Math.round(h.width) + '×' + Math.round(h.height) + '）');

    // 再来一张：预览里那份 A4 谱
    // ⚠️ 纸是 297mm ≈ 1123 CSS px，**视口比纸窄就会被裁**（第一版截图只看到前 6 个音，
    //    其实是视口 820px 装不下整张纸）⇒ 截图前把视口放宽到能装下整张 A4。
    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 1220, height: 900, deviceScaleFactor: 2, mobile: false });
    await sleep(200);
    await evalJS(ws, `document.getElementById('nt-print-top').click()`);
    await sleep(700);
    console.log('预览顶栏：' + await evalJS(ws, `document.querySelector('.nt-pv-meta').textContent`));
    console.log('结果框：' + await evalJS(ws, `document.getElementById('nt-out').textContent`));
    console.log('预览里那一行：' + await evalJS(ws, `(function(){
      var T = window.NotationPanel._test;
      var b = T.buildSystems(T.DEMO, 0, { n: 4, d: 4 }, 'both', 0);
      var svg = document.querySelector('#nt-printroot .nt-score');
      return 'systems=' + b.systems.length
        + ' · 每行小节 ' + b.systems.map(function(s){ return s.cols.length; }).join('/')
        + ' · pageWpx=' + Math.round(b.pageWpx)
        + ' · 每行最后列的右边缘 ' + b.systems.map(function(s){
            var last = s.cols[s.cols.length - 1];
            return Math.round(last.x + last.w);
          }).join('/')
        + ' · svg viewBox=' + (svg ? svg.getAttribute('viewBox') : '(无)')
        + ' · svg 宽 ' + (svg ? Math.round(svg.getBoundingClientRect().width) : 0) + 'px'
        + ' · 简谱数字个数 ' + document.querySelectorAll('#nt-printroot .nt-num').length
        + ' · 五线谱符头个数 ' + document.querySelectorAll('#nt-printroot .nt-head').length;
    })()`));
    shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(__dirname, 'notation-demo-preview.png'), Buffer.from(shot.data, 'base64'));
    console.log('落盘：devtools/notation-demo-preview.png（A4 预览）');
  } catch (e) {
    console.error('崩了：' + e.message);
    process.exitCode = 1;
  }
  ws.close();
  child.kill();
})();
