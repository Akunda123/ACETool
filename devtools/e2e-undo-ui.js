// 「撤回这一批」的**页面接线**：跑完一个写工具 ⇒ 顶栏那颗键点亮 ⇒ 点下去把标记原样发回 exe。
//
// 为什么这样测：这颗键的值全在"exe 给的标记"上（锚点 + 条数 + 署名）。真写工程来测太脏，
// 所以照 e2e-quicktip 的做法**把 /api/run 拦下来**：只验页面有没有把标记收好、有没有发对参数。
// 真机"写了能不能退回去"由 devtools/e2e-undo-live.js 负责（它净效果为零）。
//
// 用法：node devtools/e2e-undo-ui.js
const { spawn } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const EDGE = [
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
].find((p) => fs.existsSync(p));
const BASE = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json'), 'utf8')).url;
const PORT = 9800 + (process.pid % 100);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

let bad = 0;
const ok = (name, cond, extra) => {
  console.log((cond ? '  ok   ' : '  FAIL ') + name + (extra ? '  — ' + extra : ''));
  if (!cond) bad++;
};
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
  if (r.exceptionDetails) throw new Error('页面里抛错：' + (r.exceptionDetails.exception && r.exceptionDetails.exception.description || r.exceptionDetails.text));
  return r.result.value;
}

(async () => {
  const child = spawn(EDGE, [
    '--headless=new', '--disable-gpu', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-undo-' + PORT),
    BASE + 'home.html',
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

  try {
    for (let i = 0; i < 40; i++) {
      if (await evalJS(ws, `document.querySelectorAll('[data-q]').length > 0`)) break;
      await sleep(250);
    }

    ok('刚开始这颗键是灰的（还没有可撤回的一批）',
      (await evalJS(ws, `$('btnUndo').disabled && $('btnUndo').textContent.trim() === '撤回这一批'`)) === true,
      await evalJS(ws, `$('btnUndo').textContent`));

    // 把 /api/run 拦下来：记账 + 回一个**带撤回标记**的假回包
    await evalJS(ws, `(function(){
      window.__req = [];
      window.__origFetch = window.fetch;
      window.fetch = function(u, o){
        if (String(u).indexOf('/api/run') >= 0) {
          var body = JSON.parse(o.body);
          window.__req.push(body);
          if (body.toolId === 'history.undoBatch') {
            return Promise.resolve(new Response(JSON.stringify({ok:true,calls:[{json:{anchor:27,cursor:28,willUndo:14,undone:['调整音符大小','调整音符大小','编辑歌词'],
              cursorAfter:27,note:'这一批退干净了'}}]}), {status:200, headers:{'Content-Type':'application/json'}}));
          }
          // 写工具的回包：exe 会带上 undo（锚点 + 这批几条）——⚠️ 模板串里不能出现反引号！
          return Promise.resolve(new Response(JSON.stringify({ok:true,undo:{anchor:27,created:14,names:['调整音符大小'],tool:body.toolId,label:'连接音符'},
            calls:[{json:{scanned:0,hit:0,willSet:14,done:['a'],note:'连上了'}}]}), {status:200, headers:{'Content-Type':'application/json'}}));
        }
        return window.__origFetch.apply(this, arguments);
      };
      return true; })()`);

    // 点一个**立即执行**的一键按钮（连接音符：无 fields、无 custom ⇒ 直接跑）
    const qi = await evalJS(ws, `QUICK.findIndex(function(q){return q.tool === 'note.join'})`);
    ok('找得到「连接音符」（立即执行的那类按钮）', qi >= 0, 'index=' + qi);
    await evalJS(ws, `document.querySelectorAll('[data-q]')[${qi}].click()`);
    await sleep(700);

    const st = await evalJS(ws, `JSON.stringify({dis:$('btnUndo').disabled, txt:$('btnUndo').textContent, title:$('btnUndo').title})`);
    const s = JSON.parse(st);
    ok('跑完写工具 ⇒ 这颗键亮了，并带上条数', s.dis === false && /撤回这一批 · 14/.test(s.txt), st);
    ok('键的提示里点明了是哪一次操作', /连接音符/.test(s.title) && /14 条/.test(s.title), s.title);

    // 点它 ⇒ 要发出 history.undoBatch，参数就是那份标记
    await evalJS(ws, `(function(){window.__req.length=0;$('btnUndo').click();return true;})()`);
    await sleep(600);
    const arr = JSON.parse(await evalJS(ws, `JSON.stringify(window.__req.filter(function(x){return x.toolId==='history.undoBatch'}))`));
    ok('点撤回 ⇒ 发的是 history.undoBatch', arr.length === 1, arr.length + ' 条');
    const v = (arr[0] && arr[0].values) || {};
    ok('参数就是 exe 给的锚点/条数（原样送回，不自己编）', v.anchor === '27' && v.count === '14', JSON.stringify(v));
    ok('只退我们写的（actor = 远程控制）', v.actor === '远程控制', String(v.actor));
    ok('带上名字名单（顶上不是这一批时 exe 侧会拒退）',
      JSON.parse(v.names || '[]').join() === '调整音符大小', String(v.names));
    ok('真写（dryRun=0，不是干跑）', v.dryRun === '0', String(v.dryRun));
    const toast = await evalJS(ws, `$('htoast').textContent`);
    ok('退完如实报"退了几条、游标回到哪"', /退了 3 条/.test(toast) && /27/.test(toast), toast);
    ok('退干净后这颗键又变灰（不让人连点）',
      (await evalJS(ws, `$('btnUndo').disabled`)) === true, await evalJS(ws, `$('btnUndo').textContent`));

    // 被挡住那种回包：要说清"一条都没退"，而且**留着这颗键**（处理好还能再点）
    await evalJS(ws, `(function(){
      window.fetch = function(u, o){
        if (String(u).indexOf('/api/run') >= 0) {
          var body = JSON.parse(o.body);
          window.__req.push(body);
          if (body.toolId === 'history.undoBatch') {
            return Promise.resolve(new Response(JSON.stringify({ok:true,calls:[{json:{anchor:27,cursor:29,willUndo:0,blocked:'撤销栈顶上那条是「移动音符」，署名 （空 = 你自己在 ACE 里的操作） —— **不是这一批，所以一条都没退**。'}}]}), {status:200, headers:{'Content-Type':'application/json'}}));
          }
          return Promise.resolve(new Response(JSON.stringify({ok:true,undo:{anchor:27,created:2,names:['调整音符大小'],tool:body.toolId,label:'连接音符'},
            calls:[{json:{willSet:2,done:['a'],note:''}}]}), {status:200, headers:{'Content-Type':'application/json'}}));
        }
        return window.__origFetch.apply(this, arguments);
      };
      return true; })()`);
    await evalJS(ws, `(function(){document.querySelectorAll('[data-q]')[${qi}].click();return true;})()`);
    await sleep(600);
    await evalJS(ws, `(function(){window.__req.length=0;$('btnUndo').click();return true;})()`);
    await sleep(600);
    const t2 = await evalJS(ws, `$('htoast').textContent`);
    ok('被挡住时如实说「一条都没退」并点名拦路的那条', /一条都没退/.test(t2) && /移动音符/.test(t2), t2);
    ok('被挡住之后这颗键**留着**（处理好还能再点）',
      (await evalJS(ws, `$('btnUndo').disabled`)) === false, await evalJS(ws, `$('btnUndo').textContent`));
  } catch (e) {
    console.error('崩了：' + e.message);
    bad++;
  }
  ws.close();
  child.kill();
  await sleep(200);
  console.log('\n' + (bad ? bad + ' 项失败' : '全部通过'));
  process.exitCode = bad ? 1 : 0;
})();
