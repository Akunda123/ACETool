// 诊断具体页面：画布为什么空 / 输入为什么不联动
const fs = require('fs'), os = require('os'), path = require('path'), { spawn } = require('child_process');
const BASE = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA, 'ACETool', 'panel.json'), 'utf8')).url.replace(/\/$/, '') + '/';
const EDGE = 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe';
const PORT = 9950 + Math.floor(Math.random() * 40);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const PAGES = (process.argv[2] || 'xcopy.html,harmony.html,param.html,qr.html').split(',');
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
    '--user-data-dir=' + path.join(os.tmpdir(), 'diag-' + PORT), 'about:blank'], { stdio: 'ignore' });
  let t = null;
  for (let i = 0; i < 40 && !t; i++) { await sleep(250); try { t = (await (await fetch('http://127.0.0.1:' + PORT + '/json/list')).json()).find((x) => x.type === 'page'); } catch (e) { } }
  const ws = new WebSocket(t.webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let seq = 1;
  await cdp(ws, seq++, 'Runtime.enable', {});
  await cdp(ws, seq++, 'Page.enable', {});
  const errs = [];
  ws.addEventListener('message', (ev) => { const m = JSON.parse(ev.data); if (m.method === 'Runtime.exceptionThrown') errs.push(String((m.params.exceptionDetails.exception || {}).description || m.params.exceptionDetails.text).slice(0, 160)); });
  const q = async (e) => { const r = await cdp(ws, seq++, 'Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }); return r.result && r.result.value; };
  for (const page of PAGES) {
    await cdp(ws, seq++, 'Page.navigate', { url: BASE + page });
    await sleep(7000);
    const info = await q(`(function(){
      var out={};
      var el=document.getElementById('mm');
      out.canvas = el ? el.width+'x'+el.height+' css'+el.clientWidth+'x'+el.clientHeight : 'none';
      out.state = (typeof S!=='undefined') ? Object.keys(S).join(',') : 'noS';
      out.view = (typeof S!=='undefined' && S.view) ? 'yes' : 'no';
      out.stat = (document.getElementById('stat')||document.getElementById('p1stat')||{}).textContent;
      out.inputs = [].map.call(document.querySelectorAll('input'), function(i){return (i.id||'?')+'='+i.value;}).join(' | ').slice(0,200);
      return JSON.stringify(out);
    })()`);
    console.log(page, info);
    if (errs.length) { console.log('  errors:', errs.slice(0, 2)); errs.length = 0; }
  }
  child.kill(); process.exit(0);
})();
