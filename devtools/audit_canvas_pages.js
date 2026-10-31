// 逐个页面测缩略图：① 有数据时画出来了吗 ② 改输入框画布跟着变吗 ③ 在图上拖画布跟着变吗
const fs = require('fs'), os = require('os'), path = require('path'), { spawn } = require('child_process');
const BASE = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA, 'ACETool', 'panel.json'), 'utf8')).url.replace(/\/$/, '') + '/';
const EDGE = 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe';
const PORT = 9900 + Math.floor(Math.random() * 40);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const PAGES = (process.argv[2] || 'within.html,xcopy.html,pitch.html,pitchx.html,param.html,qr.html,harmony.html').split(',');

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
    '--user-data-dir=' + path.join(os.tmpdir(), 'audit-' + PORT), 'about:blank'], { stdio: 'ignore' });
  let t = null;
  for (let i = 0; i < 40 && !t; i++) { await sleep(250); try { t = (await (await fetch('http://127.0.0.1:' + PORT + '/json/list')).json()).find((x) => x.type === 'page'); } catch (e) { } }
  const ws = new WebSocket(t.webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let seq = 1;
  await cdp(ws, seq++, 'Runtime.enable', {});
  await cdp(ws, seq++, 'Page.enable', {});
  const q = async (e) => { const r = await cdp(ws, seq++, 'Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }); return r.result && r.result.value; };

  const sig = `(function(){var el=document.getElementById('mm');if(!el)return 'nocanvas';
    var d=el.getContext('2d').getImageData(0,0,el.width,el.height).data;var s=0,l=0;
    for(var i=0;i<d.length;i+=4){s=(s+d[i]*3+d[i+1]*5+d[i+2]*7)&0xffffffff;if(d[i+3])l++;}
    return el.width+'x'+el.height+':'+s+':'+l;})()`;

  for (const page of PAGES) {
    await cdp(ws, seq++, 'Page.navigate', { url: BASE + page });
    await sleep(7000);
    const before = await q(sig);
    const lit = Number(String(before).split(':')[2] || 0);
    // ② 改第一个数字输入框
    const typed = await q(`(function(){
      var el=null, ins=document.querySelectorAll('input[type=number],input[type=text]');
      for (var i=0;i<ins.length;i++){ if(/^-?\\d+(\\.\\d+)?$/.test(ins[i].value)){ el=ins[i]; break; } }
      if(!el) return 'noinput';
      var v=parseFloat(el.value); el.focus(); el.value=String(v+3);
      el.dispatchEvent(new Event('input',{bubbles:true}));
      el.dispatchEvent(new Event('change',{bubbles:true}));
      return el.id||el.name||'input';
    })()`);
    await sleep(600);
    const afterType = await q(sig);
    // ③ 在画布上拖
    const rect = JSON.parse(await q(`(function(){var r=document.getElementById('mm').getBoundingClientRect();return JSON.stringify({l:r.left,t:r.top,w:r.width,h:r.height})})()`));
    const y = rect.t + rect.h / 2;
    await cdp(ws, seq++, 'Input.dispatchMouseEvent', { type: 'mousePressed', x: rect.l + rect.w * 0.2, y, button: 'left', clickCount: 1, pointerType: 'mouse' });
    await cdp(ws, seq++, 'Input.dispatchMouseEvent', { type: 'mouseMoved', x: rect.l + rect.w * 0.6, y, button: 'left', pointerType: 'mouse' });
    await cdp(ws, seq++, 'Input.dispatchMouseEvent', { type: 'mouseReleased', x: rect.l + rect.w * 0.6, y, button: 'left', clickCount: 1, pointerType: 'mouse' });
    await sleep(500);
    const afterDrag = await q(sig);
    console.log(JSON.stringify({
      page, drawn: lit > 500, lit,
      input: typed, inputSync: typed !== 'noinput' && afterType !== before,
      dragSync: afterDrag !== afterType,
    }));
  }
  child.kill(); process.exit(0);
})();
