// 证据图：**导出预览**（多行谱）——对着真面板、真工程截。
// 只读：点「从 ACE 读取」（读当前片段）→「导出预览」（只在页面里排谱，不打印、不写工程）。
// 三张图：① 两排 ② 只要五线谱 ③ 只要简谱。用法：node devtools/shot-export-preview.js [url]
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
const PORT = 9450 + (process.pid % 60);
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
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-shotp-' + PORT), 'about:blank',
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
  await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 1000, height: 900, deviceScaleFactor: 2, mobile: false });
  try {
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'notation.html' });
    await sleep(1800);
    for (let i = 0; i < 20 && !(await evalJS(ws, `!!document.getElementById('nt-read')`)); i++) await sleep(200);
    await evalJS(ws, `document.getElementById('nt-read').click()`);
    // 真读要 1 秒上下（走 ACE）⇒ 等计数变了
    for (let i = 0; i < 30 && /^0 个元素/.test(await evalJS(ws, `document.getElementById('nt-count').textContent`)); i++) await sleep(200);
    let src = 'ACE 当前片段';
    if (/^0 个元素/.test(await evalJS(ws, `document.getElementById('nt-count').textContent`))) {
      // ACE 没开着（或片段空）⇒ 退到面板「示例」（1-7 音阶）；**如实说明**这不是从 ACE 读的。
      // ⚠️ 别写成"ACE 没开着"就完事：2026-10-17 实测 ACE **在线但当前片段 noteCount=0** 也走这条路
      //   ⇒ 措辞要把两种都盖住，不替用户断定原因。
      // ⚠️ 示例只有 2 小节 ⇒ 一行就排完了，看不出「每页多行」⇒ 再用末尾的「+」追加格铺长一点
      //（画笔默认四分音符；点一下追加格 = 落一个四分音符）。
      src = '示例 + 追加格（没从 ACE 读到音符：ACE 没开着，或当前片段是空的）';
      await evalJS(ws, `document.getElementById('nt-demo').click()`);
      await sleep(300);
      await evalJS(ws, `(function(){
        for (var i = 0; i < 96; i++) {
          var plus = document.querySelector('#nt-jp [data-i="append"]');
          if (!plus) break;
          // ⚠️ 这个是 **SVG 的 g 元素**，SVGElement 上没有 .click()（那是 HTMLElement 的）——
          //    直接 plus.click() 会抛 "plus.click is not a function"（2026-10-17 真撞上）。
          plus.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
        }
        return true; })()`);
      await sleep(600);
    }
    console.log('数据来源：' + src + ' · ' + await evalJS(ws, `document.getElementById('nt-count').textContent`));
    await evalJS(ws, `document.getElementById('nt-print-top').click()`);
    await sleep(600);
    for (const [rows, name] of [['both', 'export-preview-both.png'], ['staff', 'export-preview-staff.png'], ['jianpu', 'export-preview-jianpu.png']]) {
      await evalJS(ws, `(function(){ var s=document.getElementById('nt-rows-pv'); s.value='${rows}';
        s.dispatchEvent(new Event('change')); return true; })()`);
      await sleep(500);
      console.log(rows + ' ⇒ ' + await evalJS(ws, `document.querySelector('.nt-pv-meta').textContent`));
      const shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png' });
      fs.writeFileSync(path.join(__dirname, name), Buffer.from(shot.data, 'base64'));
      console.log('  落盘：devtools/' + name);
    }

    // ⭐ 用户 2026-10-15：导出页眉 —— 标题可自定义 / 页眉三档 / 页码开关 / **不再印 meta**
    await evalJS(ws, `(function(){ var s=document.getElementById('nt-rows-pv'); s.value='both';
      s.dispatchEvent(new Event('change')); return true; })()`);
    await sleep(500);
    const probe = async (title, head, pageNum) => evalJS(ws, `(function(){
      var t=document.getElementById('nt-title-pv'); if (t) { t.value=${JSON.stringify(title)}; t.dispatchEvent(new Event('change')); }
      var h=document.getElementById('nt-head-pv'); if (h) { h.value=${JSON.stringify(head)}; h.dispatchEvent(new Event('change')); }
      var p=document.getElementById('nt-headpage-pv'); if (p) { p.checked=${pageNum}; p.dispatchEvent(new Event('change')); }
      var pages=[].slice.call(document.querySelectorAll('.nt-print-page'));
      var heads=pages.map(function(x){ var d=x.querySelector('.nt-print-head'); return d?d.textContent:'(无)'; });
      var sys=pages.map(function(x){ return x.querySelectorAll('svg.nt-score').length; });
      return JSON.stringify({ pages: pages.length, heads: heads, sysPerPage: sys });
    })()`);
    const shot2 = async (name) => {
      const s = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png' });
      fs.writeFileSync(path.join(__dirname, name), Buffer.from(s.data, 'base64'));
    };
    let r = JSON.parse(await probe('测试标题', 'all', true));
    console.log('页眉=每页/带页码 ⇒ ' + JSON.stringify(r.heads));
    console.log('  断言：每页都有、含标题、含页码、**不含 meta** → ' +
      ((r.heads.every((h) => h.indexOf('测试标题') >= 0 && /第 \d+ \/ \d+ 页/.test(h)) && !/来自|示例/.test(r.heads.join(''))) ? 'PASS' : 'FAIL'));
    await shot2('nt-head-all.png');
    r = JSON.parse(await probe('测试标题', 'all', false));
    console.log('页眉=每页/不带页码 ⇒ ' + JSON.stringify(r.heads[0]));
    console.log('  断言：有页眉但没有「第 x / y 页」 → ' +
      ((r.heads[0].indexOf('测试标题') >= 0 && !/第 \d+ \/ \d+ 页/.test(r.heads[0])) ? 'PASS' : 'FAIL'));
    r = JSON.parse(await probe('测试标题', 'first', true));
    console.log('页眉=仅首页 ⇒ ' + JSON.stringify(r.heads));
    console.log('  断言：第 1 页有、其余无 → ' +
      ((r.heads.length >= 1 && r.heads[0].indexOf('测试标题') >= 0 &&
        r.heads.slice(1).every((h) => h === '(无)')) ? (r.heads.length > 1 ? 'PASS' : 'PASS（只有 1 页）') : 'FAIL'));
    r = JSON.parse(await probe('测试标题', 'off', true));
    console.log('页眉=不印 ⇒ ' + JSON.stringify(r.heads) + ' · 每页谱数 ' + JSON.stringify(r.sysPerPage));
    console.log('  断言：一张纸都没有页眉 → ' + (r.heads.every((h) => h === '(无)') ? 'PASS' : 'FAIL'));
    await shot2('nt-head-off.png');
    r = JSON.parse(await probe('', 'all', true));
    console.log('标题留空 ⇒ ' + JSON.stringify(r.heads[0]));
    console.log('  断言：页眉还在、但没有标题那一段（只剩 1=调 拍号 + 页码） → ' +
      ((/^1 = .+　\d+\/\d+/.test(r.heads[0]) && r.heads[0].indexOf('　　') < 0) ? 'PASS' : 'FAIL'));
  } catch (e) {
    console.error('崩了：' + e.message);
    process.exitCode = 1;
  }
  ws.close();
  child.kill();
})();
