// 验证 pitch.html 缩略图上的拖动：模拟指针事件，看 S.s1/S.s2/S.d1 是否变化
const fs = require('fs'), os = require('os'), path = require('path'), { spawn } = require('child_process');
const BASE = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA, 'ACETool', 'panel.json'), 'utf8')).url.replace(/\/$/, '') + '/';
const EDGE = 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe';
const PORT = 9700 + Math.floor(Math.random() * 50);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
function cdp(ws, id, method, params) {
  return new Promise((res, rej) => {
    const on = (ev) => { const m = JSON.parse(ev.data); if (m.id === id) { ws.removeEventListener('message', on); m.error ? rej(new Error(JSON.stringify(m.error))) : res(m.result); } };
    ws.addEventListener('message', on);
    ws.send(JSON.stringify({ id, method, params: params || {} }));
    setTimeout(() => rej(new Error(method + ' timeout')), 25000);
  });
}
(async () => {
  const child = spawn(EDGE, ['--headless=new', '--disable-gpu', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'pd-' + PORT), 'about:blank'], { stdio: 'ignore' });
  let t = null;
  for (let i = 0; i < 40 && !t; i++) { await sleep(250); try { t = (await (await fetch('http://127.0.0.1:' + PORT + '/json/list')).json()).find((x) => x.type === 'page'); } catch (e) { } }
  const ws = new WebSocket(t.webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let seq = 1;
  await cdp(ws, seq++, 'Runtime.enable', {});
  await cdp(ws, seq++, 'Page.enable', {});
  const q = async (e) => { const r = await cdp(ws, seq++, 'Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }); return r.result && r.result.value; };
  await cdp(ws, seq++, 'Page.navigate', { url: BASE + 'pitch.html' });
  await sleep(8000);
  console.log('state before:', await q('JSON.stringify({s1:S.s1,s2:S.s2,d1:S.d1,view:!!S.view,boxes:S.boxes})'));
  const rect = await q('(function(){var r=document.getElementById("mm").getBoundingClientRect();return JSON.stringify({l:r.left,t:r.top,w:r.width,h:r.height})})()');
  const r = JSON.parse(rect);
  console.log('canvas rect:', rect);
  const y = r.t + r.h / 2;
  // 在画布上横向拖拽（源区间）
  await cdp(ws, seq++, 'Input.dispatchMouseEvent', { type: 'mousePressed', x: r.l + r.w * 0.25, y, button: 'left', clickCount: 1, pointerType: 'mouse' });
  await cdp(ws, seq++, 'Input.dispatchMouseEvent', { type: 'mouseMoved', x: r.l + r.w * 0.55, y, button: 'left', pointerType: 'mouse' });
  await cdp(ws, seq++, 'Input.dispatchMouseEvent', { type: 'mouseReleased', x: r.l + r.w * 0.55, y, button: 'left', clickCount: 1, pointerType: 'mouse' });
  await sleep(500);
  console.log('state after src drag:', await q('JSON.stringify({s1:S.s1,s2:S.s2,d1:S.d1})'));
  const bx = JSON.parse(await q('JSON.stringify(S.boxes.dst)'));
  const cx = r.l + (bx[0] + bx[1]) / 2;
  await cdp(ws, seq++, 'Input.dispatchMouseEvent', { type: 'mousePressed', x: cx, y, button: 'left', clickCount: 1, pointerType: 'mouse' });
  await cdp(ws, seq++, 'Input.dispatchMouseEvent', { type: 'mouseMoved', x: cx + 120, y, button: 'left', pointerType: 'mouse' });
  await cdp(ws, seq++, 'Input.dispatchMouseEvent', { type: 'mouseReleased', x: cx + 120, y, button: 'left', clickCount: 1, pointerType: 'mouse' });
  await sleep(400);
  console.log('state after dst drag:', await q('JSON.stringify({s1:S.s1,s2:S.s2,d1:S.d1})'));
  child.kill(); process.exit(0);
})();
