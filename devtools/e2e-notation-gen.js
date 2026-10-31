// 「谱面编辑器」页的**生成**那一块（用户 2026-10-07：
// 「jianpu.editor，直接把 nt 块拿过来，下面是生成，生成音符的位置是当前 clip 从播放头所在小节开始生成音符」）。
//
// 判据是四件事（真改工程太脏 ⇒ 拦 `/api/run`）：
//   ① 生成块在 **nt 块（两排谱）下面**（DOM 顺序）；
//   ② 生成的输入**直接就是编辑器里那份元素**（"直接把 nt 块拿过来"，不让用户重填）；
//   ③ 起点默认发 `startAt=playhead`（= 播放头所在小节的起点）、目标默认 `target=current`（当前片段）；
//   ④ 干跑回包里报了"第几小节 / 工程 tick"就显示出来（起点是往下取整到小节线的，要说清）。
//
// 用法：node devtools/e2e-notation-gen.js [url]
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
const PORT = 9910 + (process.pid % 80);
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

// 假「从 ACE 读取」：4 个元素（含一个 `-` 连音与一个休止），调式 C、4/4
function readJSON() {
  return {
    notation: 'elements', scope: 'clip', clip: '测试片段', clipType: 'sing', trackIndex: 0, notes: 4,
    keyFrom: 'manual', key: 0, keyName: 'C', autoDetectedKey: 0, autoDetectedKeyName: 'C',
    meter: '4/4',
    elements: [
      { kind: 'note', pitch: 1, duration: 4, lyric: '月' },
      { kind: 'note', pitch: 3, duration: 8, lyric: '落' },
      { kind: 'sustain', duration: 4 },
      { kind: 'note', pitch: 5, duration: 4, rest: true, pitch: 0 },
    ],
    note: '只读：没动工程。',
  };
}

// 假「生成」回包：起点在第 9 小节（对齐小节线 15360）、写 4 个音符
function writeJSON() {
  return {
    startAt: 'playhead', target: 'current', source: 'elements', clip: '测试片段', trackIndex: 0,
    clipBegin: 0, startTick: 15360, playheadTick: 15420, startBar: 9, startBarTick: 15360,
    parsedElements: 4, notesToWrite: 4, sustainNotes: 1, skipped: 1, minPitch: 60, maxPitch: 64,
    totalTicks: 1920, preview: ['pos 15360 dur 480 pitch 60 lyric la', 'pos 15840 dur 240 pitch 64 lyric -'],
    plannedCalls: ['note add --clip-uuid {c1} --notes @<临时文件>   （4 个音）'],
    note: '干跑：一个字节都没写。 起点 = **播放头所在小节的起点**：播放头 15420 落在第 9 小节 ⇒ 对齐到小节线 15360（往下取整）。',
  };
}

(async () => {
  const child = spawn(EDGE, [
    '--headless=new', '--disable-gpu', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-nota-' + PORT), 'about:blank',
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
      if (String(u).indexOf('/api/run') >= 0) {
        var body = JSON.parse(o.body);
        window.__req.push(body);
        var j = (body.toolId === 'jianpu.toNotes') ? (${writeJSON.toString()})() : (${readJSON.toString()})();
        return Promise.resolve(new Response(JSON.stringify({ok:true,calls:[{json:j}]}), {status:200, headers:{'Content-Type':'application/json'}}));
      }
      return orig.apply(window, arguments);
    };
  })()`;
  try { new Function(stub.replace(/^\(function\(\)\{/, '').replace(/\}\)\(\)$/, '')); }
  catch (e) { child.kill(); console.error('桩自己就是语法错：' + e.message); process.exit(1); }

  try {
    await cdp(ws, ++seq, 'Page.enable', {});
    await cdp(ws, ++seq, 'Page.addScriptToEvaluateOnNewDocument', { source: stub });
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + '#jianpu.editor' });
    await sleep(2000);
    for (let i = 0; i < 20 && !(await evalJS(ws, `!!document.getElementById('nt-jp')`)); i++) await sleep(200);

    ok('谱面编辑器页起来了（两排谱 + 工具栏都在）',
      (await evalJS(ws, `['nt-jp','nt-st','nt-bar','nt-gen'].every(function(i){return !!document.getElementById(i)||!!document.querySelector('.'+i)})`)) === true);

    // ① 生成块在 nt 块（两排谱）**下面**
    const order = await evalJS(ws, `(function(){
      var all = Array.prototype.slice.call(document.querySelectorAll('.nt-scroll, .nt-gen, #nt-out'));
      return all.map(function(el){ return el.className.indexOf('nt-gen')>=0 ? 'gen' : (el.className.indexOf('nt-scroll')>=0 ? 'scroll' : 'out'); }).join(',');
    })()`);
    ok('⭐ 生成块在**两排谱下面**（DOM 顺序：scroll,scroll,gen,out）',
      order === 'scroll,scroll,gen,out', order);

    // ①b ⭐ 但按钮**顶栏也有一份**（用户 2026-10-07：「在 button 里找不到」——
    //     两排谱各 280px 高，不滚下去就看不到生成块）
    // ⭐ 用户 2026-10-08：「nt-dry-top 删掉」+「下面还有个干跑没删」⇒ 两颗干跑都删了，
    //    顶栏与生成块各留**一颗真写**（同一个动作）
    const topBtns = await evalJS(ws, `(function(){
      var bar = document.querySelector('.nt-bar');
      return bar ? Array.prototype.slice.call(bar.querySelectorAll('button')).map(function(b){return b.id}).join(',') : '';
    })()`);
    ok('⭐ 顶栏只有「生成并写进 ACE」（干跑那颗已删）',
      /nt-write-top/.test(topBtns) && !/nt-dry/.test(topBtns) && topBtns.indexOf('nt-read') === 0, topBtns);
    ok('⭐ 生成块里也只剩真写那一颗（`#nt-dry` 已删；干跑改从 `_test.writeACE` 走）',
      (await evalJS(ws, `(function(){
        var g = document.querySelector('.nt-gen');
        var ids = Array.prototype.slice.call(g.querySelectorAll('button')).map(function(b){return b.id}).join(',');
        return ids + '|' + (typeof window.NotationPanel._test.writeACE === 'function');
      })()`)) === 'nt-write|true');

    // ② 先把 nt 块读出来（走的还是既有的「从 ACE 读取」）
    // ⚠️ 面板内部的 `S` 在 IIFE 闭包里，`Runtime.evaluate` 取不到 ⇒ 一律看 DOM 与发出去的请求。
    await evalJS(ws, `document.getElementById('nt-read').click()`);
    await sleep(500);
    ok('从 ACE 读进来 4 个元素（含连音与休止）',
      /^4 个元素/.test(await evalJS(ws, `document.getElementById('nt-count').textContent`)) &&
      (await evalJS(ws, `document.querySelectorAll('#nt-jp .nt-hit').length`)) >= 4,
      await evalJS(ws, `document.getElementById('nt-count').textContent + ' / hits=' + document.querySelectorAll('#nt-jp .nt-hit').length`));

    // ③ 生成（干跑）：输入必须**就是编辑器里那份元素**。
    // ⚠️ 干跑按钮已经按用户要求删了 ⇒ 改从 `_test.writeACE(true)` 走这条路径（同一个函数、同一个出口）。
    await evalJS(ws, `(function(){ window.__req.length=0; window.NotationPanel._test.writeACE(true); return true; })()`);
    await sleep(500);
    const reqs = JSON.parse(await evalJS(ws, `JSON.stringify(window.__req.filter(function(x){return x.toolId==='jianpu.toNotes'}))`));
    ok('干跑（`_test.writeACE(true)`）发 `jianpu.toNotes`（dryRun=1）',
      reqs.length === 1 && reqs[0].values.dryRun === '1', reqs.length + ' 条');
    const vals = (reqs[0] || {}).values || {};
    ok('⭐ 输入**直接就是 nt 块那份元素**（不是让用户另填一份）',
      (function () {
        try {
          const els = JSON.parse(vals.elements || '[]');
          return els.length === 4 && els[0].pitch === 1 && els[1].duration === 8 && els[2].kind === 'sustain' && els[3].rest === true;
        } catch (e) { return false; }
      })(), String(vals.elements).slice(0, 120));
    ok('⭐ 起点默认 = 播放头所在小节的起点（`startAt=playhead`）、目标默认 = 当前片段',
      vals.startAt === 'playhead' && vals.target === 'current', JSON.stringify([vals.startAt, vals.target]));
    ok('调式跟着编辑器走（`key`）', /^\d+$/.test(String(vals.key)), String(vals.key));

    // ④ 干跑回包里的"第几小节 / 工程 tick"要显示出来
    const rep = await evalJS(ws, `document.getElementById('nt-out').textContent`);
    ok('⭐ 报告说清起点落在哪一小节线（第 9 小节 / 15360），并说明是往下取整',
      /第 9 小节/.test(rep) && /15360/.test(rep) && /往下取整/.test(rep),
      (rep || '').split('\n').filter(function (s) { return /起点/.test(s); })[0] || rep.slice(0, 120));

    // ⑤ 换档：精确位置 / 新建片段 ⇒ 发出去的值跟着变
    //（「起点」那一档的解释已经写进下拉的选项文字里 —— 用户 2026-10-08「删除不必要的文字」，
    // 页面上那颗会跟着变的提示条 `nt-gentip` 已删）
    await evalJS(ws, `(function(){ document.getElementById('nt-start').value='playheadExact';
      document.getElementById('nt-start').dispatchEvent(new Event('change'));
      document.getElementById('nt-target').value='newClip'; return true; })()`);
    ok('起点/目标两档的口径写在选项文字里（页面上不再另挂提示条）',
      /精确位置/.test(await evalJS(ws, `document.querySelector('#nt-start option[value=playheadExact]').textContent`)) &&
      (await evalJS(ws, `!!document.getElementById('nt-gentip')`)) === false,
      await evalJS(ws, `document.querySelector('#nt-start option[value=playheadExact]').textContent`));
    // ⑤b 顺带把**真写那颗按钮**也点一次（干跑按钮删了之后，按钮这条路只剩它 ⇒ 必须验到）
    //     会有 `window.confirm` 挡一下 ⇒ 先在页面里把它换成"永远确认"。
    await evalJS(ws, `window.confirm = function(){ return true; }; true`);
    await evalJS(ws, `(function(){ window.__req.length=0; document.getElementById('nt-write').click(); return true; })()`);
    await sleep(500);
    const reqs2 = JSON.parse(await evalJS(ws, `JSON.stringify(window.__req.filter(function(x){return x.toolId==='jianpu.toNotes'}))`));
    const v2 = (reqs2[0] || {}).values || {};
    ok('⭐ 点生成块那颗真写按钮 ⇒ 两档原样发出去（startAt=playheadExact / target=newClip、dryRun=0）',
      reqs2.length === 1 && v2.dryRun === '0' && v2.startAt === 'playheadExact' && v2.target === 'newClip',
      JSON.stringify([v2.dryRun, v2.startAt, v2.target]));
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
