// 深挖 pitch.html 缩略图为什么是 300x150 空画布
const fs = require('fs'), os = require('os'), path = require('path'), { spawn } = require('child_process');
const BASE = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA, 'ACETool', 'panel.json'), 'utf8')).url.replace(/\/$/, '') + '/';
const EDGE = 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe';
const PORT = 9600 + Math.floor(Math.random() * 50);
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
    '--user-data-dir=' + path.join(os.tmpdir(), 'pt2-' + PORT), 'about:blank'], { stdio: 'ignore' });
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
  console.log('CanvasMap:', await q('typeof window.CanvasMap'));
  console.log('S.mm:', await q('(window.S && S.mm) ? ("tracks="+S.mm.tracks.length) : "null"'));
  console.log('canvas attrs:', await q('(function(){var e=document.getElementById("mm");return e? e.outerHTML.slice(0,160):"none"})()'));
  console.log('postRun type:', await q('typeof window.postRun'));
  console.log('minimap fetch:', await q(`(async()=>{try{const d=await window.postRun('view.minimap',{});return JSON.stringify({ok:d.ok,tracks:(d.calls&&d.calls[0]&&d.calls[0].json&&d.calls[0].json.tracks||[]).length});}catch(e){return 'ERR '+e.message}})()`));
  console.log('draw fn:', await q('typeof draw'));
  console.log('p1stat:', await q('(document.getElementById("stat")||{}).textContent'));
  child.kill(); process.exit(0);
})();
