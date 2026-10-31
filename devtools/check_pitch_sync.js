// 验证：改输入框（模拟真实输入）后，缩略图上的选区是否跟着动
const fs = require('fs'), os = require('os'), path = require('path'), { spawn } = require('child_process');
const BASE = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA, 'ACETool', 'panel.json'), 'utf8')).url.replace(/\/$/, '') + '/';
const EDGE = 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe';
const PORT = 9800 + Math.floor(Math.random() * 50);
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
    '--user-data-dir=' + path.join(os.tmpdir(), 'ps-' + PORT), 'about:blank'], { stdio: 'ignore' });
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
  console.log('before:', await q('JSON.stringify({s1:S.s1,s2:S.s2,d1:S.d1,boxes:S.boxes})'));
  // 模拟真实输入：聚焦 s1 输入框，全选改值，触发 input 事件
  await q(`(function(){
    function setv(id,v){ var el=document.getElementById(id); el.focus(); el.value=String(v); el.dispatchEvent(new Event('input',{bubbles:true})); }
    setv('s1',30); setv('s2',60); setv('d1',62);
    return 'ok';
  })()`);
  await sleep(500);
  console.log('after typing:', await q('JSON.stringify({s1:S.s1,s2:S.s2,d1:S.d1,boxes:S.boxes,info:document.getElementById("rangeInfo").textContent.slice(0,60)})'));
  child.kill(); process.exit(0);
})();
