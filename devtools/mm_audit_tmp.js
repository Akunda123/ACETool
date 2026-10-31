// 缩略图巡检：每页 canvas 是否画了、色带/高亮是否出现、有无 JS 异常
const fs = require('fs'), os = require('os'), path = require('path'), { spawn } = require('child_process');
const BASE = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA, 'ACETool', 'panel.json'), 'utf8')).url.replace(/\/$/, '') + '/';
const EDGE = 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe';
const PORT = 9300 + Math.floor(Math.random() * 200);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
function cdp(ws, id, method, params) {
  return new Promise((res, rej) => {
    const on = (ev) => { const m = JSON.parse(ev.data); if (m.id === id) { ws.removeEventListener('message', on); m.error ? rej(new Error(JSON.stringify(m.error))) : res(m.result); } };
    ws.addEventListener('message', on);
    ws.send(JSON.stringify({ id, method, params: params || {} }));
    setTimeout(() => rej(new Error(method + ' 超时')), 30000);
  });
}
(async () => {
  const child = spawn(EDGE, ['--headless=new', '--disable-gpu', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'mmpage-' + PORT), 'about:blank'], { stdio: 'ignore' });
  let t = null;
  for (let i = 0; i < 40 && !t; i++) { await sleep(250); try { t = (await (await fetch('http://127.0.0.1:' + PORT + '/json/list')).json()).find((x) => x.type === 'page'); } catch (e) { } }
  const ws = new WebSocket(t.webSocketDebuggerUrl);
  await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
  let seq = 1;
  await cdp(ws, seq++, 'Runtime.enable', {});
  await cdp(ws, seq++, 'Page.enable', {});
  const errs = [];
  ws.addEventListener('message', (ev) => {
    const m = JSON.parse(ev.data);
    if (m.method === 'Runtime.exceptionThrown') errs.push((m.params.exceptionDetails.exception || {}).description || m.params.exceptionDetails.text);
  });
  const q = async (e) => { const r = await cdp(ws, seq++, 'Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }); return r.result && r.result.value; };

  const probe = `(function(){
    var el = document.querySelector('#mm') || document.querySelector('#fpmap canvas');
    if (!el) return JSON.stringify({canvas:false});
    var d = el.getContext('2d').getImageData(0,0,el.width,el.height).data;
    var blue=0, amber=0, green=0, lit=0, any=0;
    for (var i=0;i<d.length;i+=4){
      var r=d[i],g=d[i+1],b=d[i+2],a=d[i+3];
      if(!a) continue; any++;
      if (b>150 && r<90 && g>70 && g<160) blue++;
      if (r>150 && g>110 && b<110) amber++;
      if (g>150 && r<120 && b<140) green++;
      if (r>120||g>120||b>120) lit++;
    }
    return JSON.stringify({canvas:true,w:el.width,h:el.height,any:any,lit:lit,blue:blue,amber:amber,green:green});
  })()`;

  for (const [page, wait] of [['within.html', 6000], ['pitch.html', 6000], ['pitchx.html', 7000], ['param.html', 6000], ['qr.html', 7000], ['xcopy.html', 11000], ['find.html', 7000], ['harmony.html', 6000]]) {
    errs.length = 0;
    await cdp(ws, seq++, 'Page.navigate', { url: BASE + page });
    await sleep(wait);
    const r = await q(probe);
    const stat = String(await q('(document.getElementById("stat")||document.getElementById("fstat")||{}).textContent||"')).slice(0, 30);
    console.log(page.padEnd(13), r, '| stat=', stat, '| err=', JSON.stringify(errs).slice(0, 80));
  }
  child.kill();
  process.exit(0);
})();
