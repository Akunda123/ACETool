// 独立窗口 `notation.html`（用户 2026-10-07：「把这个功能加入到 home.html」——
// 主页面那个「谱面编辑器」按钮开的子窗口）：面板挂得上 · 顶栏只有一颗真写 ·
// 干跑**不点亮**「撤回这一批」、真写**点亮**它（只有真写才压撤销栈）· 不再有「完整面板」按钮。
//
// 真改工程太脏 ⇒ 拦 `/api/run`（jianpu.editor 回 3 个元素、jianpu.toNotes 回假报告，
// 只有**真写**那笔才在回包上带 `undo` 标记）。
// 用法：node devtools/e2e-notation-window.js [url]
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
const PORT = 9860 + (process.pid % 80);
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
  if (r.exceptionDetails) throw new Error('页面里抛错：' + ((r.exceptionDetails.exception && r.exceptionDetails.exception.description) || r.exceptionDetails.text));
  return r.result.value;
}

function readJSON() {
  return {
    notation: 'elements', scope: 'clip', clip: '测试片段', clipType: 'sing', trackIndex: 0, notes: 3,
    keyFrom: 'manual', key: 0, keyName: 'C', meter: '4/4',
    elements: [
      { kind: 'note', pitch: 1, duration: 4, lyric: '上' },
      { kind: 'note', pitch: 3, duration: 4, lyric: '海' },
      { kind: 'note', pitch: 5, duration: 4, lyric: '风' },
    ],
    note: '只读：没动工程。',
  };
}
function writeJSON() {
  return {
    startAt: 'playhead', target: 'current', source: 'elements', clip: '测试片段', trackIndex: 0,
    clipBegin: 0, startTick: 1920, playheadTick: 2400, startBar: 2, startBarTick: 1920,
    parsedElements: 3, notesToWrite: 3, minPitch: 60, maxPitch: 64, totalTicks: 1440,
    plannedCalls: ['note add --clip-uuid {c1} --notes @<临时文件>   （3 个音）'],
    note: '干跑：一个字节都没写。 起点 = **播放头所在小节的起点**：播放头 2400 落在第 2 小节 ⇒ 对齐到小节线 1920（往下取整）。',
  };
}
function selfcheck() {
  return { online: true, version: { forAce: 'cli 0.17.0 · ACE Studio 2.1.8' }, project: { projectName: '' } };
}

(async () => {
  const child = spawn(EDGE, [
    '--headless=new', '--disable-gpu', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-notaw-' + PORT), 'about:blank',
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

  const stub = `(function(){
    window.__req = [];
    var orig = window.fetch;
    window.fetch = function(u, o){
      var s = String(u);
      if (s.indexOf('/api/run') >= 0) {
        var body = JSON.parse(o.body);
        window.__req.push(body);
        var vals = body.values || {};
        var isWrite = (body.toolId === 'jianpu.toNotes') && vals.dryRun !== '1';
        var j = (body.toolId === 'jianpu.toNotes') ? (${writeJSON.toString()})() : (${readJSON.toString()})();
        var payload = { ok: true, calls: [{ json: j }] };
        // 只有**真写**那一笔才压撤销栈 ⇒ 回包上才有 undo 标记（干跑没有）
        if (isWrite) payload.undo = { created: 3, anchor: 41, label: 'jianpu.toNotes', names: ['note add'] };
        return Promise.resolve(new Response(JSON.stringify(payload), {status:200, headers:{'Content-Type':'application/json'}}));
      }
      if (s.indexOf('/api/selfcheck') >= 0) {
        return Promise.resolve(new Response(JSON.stringify((${selfcheck.toString()})()), {status:200, headers:{'Content-Type':'application/json'}}));
      }
      return orig.apply(window, arguments);
    };
  })()`;

  try {
    await cdp(ws, ++seq, 'Page.enable', {});
    await cdp(ws, ++seq, 'Page.addScriptToEvaluateOnNewDocument', { source: stub });
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'notation.html' });
    await sleep(1800);

    ok('独立窗口起来了：面板挂上了（两排谱 + 生成块 + 顶栏）',
      (await evalJS(ws, `!!document.getElementById('nt-jp') && !!document.getElementById('nt-st') && !!document.querySelector('.nt-gen')`)) === true);
    ok('顶栏有「生成并写进 ACE」（不滚下去也能点；**两颗干跑都删了**）',
      (await evalJS(ws, `!!document.getElementById('nt-write-top') && !document.getElementById('nt-dry') && !document.getElementById('nt-dry-top')`)) === true);
    ok('「撤回这一批」一开始是灰的（还没有可退的一批）',
      (await evalJS(ws, `document.getElementById('btnUndo').disabled`)) === true &&
      (await evalJS(ws, `document.getElementById('btnUndo').textContent`)) === '撤回这一批');
    ok('顶栏状态栏显示版本（说明这个窗口自己会自检）',
      /cli 0\.17\.0/.test(await evalJS(ws, `document.getElementById('hstat').textContent`)),
      await evalJS(ws, `document.getElementById('hstat').textContent`));
    // ⚠️ 判据看**顶栏里的按钮**，不看 `body.textContent` —— 后者会把 `<script>` 里的注释也算进来
    //    （注释里正留着"为什么删"这句话）。
    ok('⭐ 不再有「完整面板」按钮（用户 2026-10-08：「右上角完整面板删了」）；顶栏只剩「撤回这一批」+ 📌',
      // ⭐ 2026-10-16 起顶栏还多一颗 📌（用户：「所有面板包括 home 和所有打开的都加 📌」）
      (await evalJS(ws, `(function(){
        var bs = Array.prototype.slice.call(document.querySelectorAll('.bar button')).map(function(b){ return b.textContent; });
        return (!document.getElementById('btnIndex')) + '|' + bs.join(',');
      })()`)) === 'true|撤回这一批,📌');

    // 读 + 生成（干跑走 `_test.writeACE(true)` —— 按钮已经删了，函数还在）
    await evalJS(ws, `document.getElementById('nt-read').click()`);
    await sleep(500);
    ok('从 ACE 读进来 3 个元素',
      /^3 个元素/.test(await evalJS(ws, `document.getElementById('nt-count').textContent`)),
      await evalJS(ws, `document.getElementById('nt-count').textContent`));
    await evalJS(ws, `(function(){ window.__req.length=0; window.NotationPanel._test.writeACE(true); return true; })()`);
    await sleep(500);
    const reqs = JSON.parse(await evalJS(ws, `JSON.stringify(window.__req.filter(function(x){return x.toolId==='jianpu.toNotes'}))`));
    const vals = (reqs[0] || {}).values || {};
    ok('干跑（`_test.writeACE(true)`）发的是编辑器那份元素 + 起点=播放头所在小节',
      reqs.length === 1 && vals.dryRun === '1' && vals.startAt === 'playhead' &&
      JSON.parse(vals.elements || '[]').length === 3, JSON.stringify([vals.dryRun, vals.startAt]));
    ok('报告里显示"第 2 小节 / 1920 / 往下取整"',
      /第 2 小节/.test(await evalJS(ws, `document.getElementById('nt-out').textContent`)),
      (await evalJS(ws, `document.getElementById('nt-out').textContent`)).split('\n')[0]);
    ok('⭐ **干跑不点亮**「撤回这一批」（干跑没有落盘、也就没有可退的一批）',
      (await evalJS(ws, `document.getElementById('btnUndo').disabled`)) === true &&
      (await evalJS(ws, `document.getElementById('btnUndo').textContent`)) === '撤回这一批');

    // 真写：点生成块那颗按钮（会 `window.confirm` 挡一下 ⇒ 先换成"永远确认"）⇒ 回包带 `undo` ⇒ 点亮
    await evalJS(ws, `window.confirm = function(){ return true; }; true`);
    await evalJS(ws, `(function(){ window.__req.length=0; document.getElementById('nt-write').click(); return true; })()`);
    await sleep(500);
    const reqs2 = JSON.parse(await evalJS(ws, `JSON.stringify(window.__req.filter(function(x){return x.toolId==='jianpu.toNotes'}))`));
    ok('⭐ 点真写按钮 ⇒ `dryRun=0`（真改工程那条路）',
      reqs2.length === 1 && (reqs2[0].values || {}).dryRun === '0',
      JSON.stringify((reqs2[0] || {}).values ? (reqs2[0].values).dryRun : '(没发)'));
    ok('⭐ 真写回包带 `undo` ⇒「撤回这一批」点亮并带上条数',
      (await evalJS(ws, `document.getElementById('btnUndo').disabled`)) === false &&
      /撤回这一批 · 3/.test(await evalJS(ws, `document.getElementById('btnUndo').textContent`)),
      await evalJS(ws, `document.getElementById('btnUndo').textContent`));
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
