// 「跨轨复制」窗口（xcopy.html）的页面接线：**所有轨道**的预览、源/目标轨、可选范围、复制内容项。
//
// 为什么这么测：判据是"发出去的轨道号 + 范围 + 开关对不对"，以及"目标轨一换就重读**目标轨**的花名册"
//（跨轨时目标轨的引擎世代可能不同 ⇒ 可写层也不同）。真改工程太脏 ⇒ 拦 `/api/run`：
// view.minimap 回一张**两条轨**的假图、xcopy.view 回花名册、xcopy.notes 回假报告。
//
// 用法：node devtools/e2e-xcopy-ui.js
const { spawn } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const EDGE = [
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
].find((p) => fs.existsSync(p));
const BASE = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json'), 'utf8')).url;
const PORT = 9990 + (process.pid % 9);
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

// 假缩略图：两条轨（0 绿源 / 1 目标），各一个片段，各 3 个音符
function mmJSON() {
  const mk = (u, name, colorShift) => ({
    uuid: u, index: 0, name: name, type: 'sing', begin: 0, end: 15360, noteCount: 3,
    notes: [0, 1, 2].map((i) => ({ pos: 3840 + i * 480, dur: 480, pitch: 60 + i + colorShift })),
    minPitch: 60, maxPitch: 64,
  });
  return {
    scope: 'arrangement', durationTick: 19200, meter: '4/4', tempo: 120, firstTick: 0, lastTick: 15360,
    trackCount: 2, clipCount: 2, noteCount: 6,
    tracks: [
      { trackIndex: 0, name: '源轨', type: 'Sing', soundSource: '洛天依', clips: [mk('{c0}', '源片段', 0)] },
      { trackIndex: 1, name: '目标轨', type: 'Sing', soundSource: '起复', clips: [mk('{c1}', '目标片段', 1)] },
    ],
  };
}
// 假花名册：按 trackIndex 给**不同的引擎世代**（目标轨 v1 ⇒ 有 envelope/direct）
function rosterJSON(trackIndex) {
  const v1 = String(trackIndex) === '1';
  const layers = v1
    ? [{ layer: 'baseline', access: 'read-only', shape: 'dense' },
       { layer: 'direct', access: 'read-write', shape: 'dense' },
       { layer: 'envelope', access: 'read-write', shape: 'dense' }]
    : [{ layer: 'baseline', access: 'read-only', shape: 'dense' },
       { layer: 'user', access: 'read-write', shape: 'dense' }];
  // ⭐ 参数表的列由 Go 的 `paramColumnsOf` 按这份花名册推（页面不自己解释列的含义）
  const columns = v1
    ? [{ key: 'effective', label: '实参', dstLayer: 'direct', hint: '实参是绝对值 ⇒ 落进 direct' },
       { key: 'envelope', label: '包络', dstLayer: 'envelope', hint: '包络是乘数层 ⇒ 落进 envelope' }]
    : [{ key: 'effective', label: '实参', dstLayer: 'user', hint: '实参是绝对值 ⇒ 落进 user' }];
  return {
    trackIndex: Number(trackIndex), trackName: v1 ? '目标轨' : '源轨',
    clipUuid: '{c' + trackIndex + '}', clipName: '片段', clipIndex: 0, clipType: 'sing',
    clipBegin: 0, clipEnd: 15360, clipTicks: 15360, clipNoteCount: 3,
    meter: '4/4', ticksPerBar: 1920, tempo: 120, durationTick: 19200, barCount: 10,
    clips: [], engineGeneration: v1 ? 'verse24' : 'singing-mamba',
    params: [{ param: 'tension', displayName: 'Tension', available: true, scale: v1 ? 'envelope' : 'model',
      valueMin: v1 ? 0.3 : 0, valueMax: v1 ? 2.5 : 1, layers: layers,
      writableLayers: layers.filter((l) => l.access === 'read-write').map((l) => l.layer) }],
    paramColumns: columns,
  };
}

(async () => {
  const child = spawn(EDGE, [
    '--headless=new', '--disable-gpu', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-xcopy-' + PORT),
    'about:blank',
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
    try { localStorage.removeItem('acetool.xcopy'); } catch (e) {}
    window.__req = [];
    var orig = window.fetch;
    window.fetch = function(u, o){
      if (String(u).indexOf('/api/run') >= 0) {
        var body = JSON.parse(o.body);
        window.__req.push(body);
        if (body.toolId === 'view.minimap') {
          return Promise.resolve(new Response(JSON.stringify({ok:true,calls:[{json: (${mmJSON.toString()})()}]}), {status:200, headers:{'Content-Type':'application/json'}}));
        }
        if (body.toolId === 'xcopy.view') {
          return Promise.resolve(new Response(JSON.stringify({ok:true,calls:[{json: (${rosterJSON.toString()})(body.values.trackIndex || 0)}]}), {status:200, headers:{'Content-Type':'application/json'}}));
        }
        var v = body.values;
        return Promise.resolve(new Response(JSON.stringify({ok:true,calls:[{json:{mode:'tracks',srcTrack:Number(v.srcTrack),dstTrack:Number(v.dstTrack),
          srcTrackName:'源轨',dstTrackName:'目标轨',pairCount:1,matched:3,srcOnly:0,dstOnly:0,
          range:v.beginBar?('第 '+v.beginBar+'..'+v.endBar+' 小节'):'不限范围（整对片段）',
          willSetPitch:3,willWriteParamLayers:1,paramTicks:1920,skippedDefaultPhonemes:2,
          plannedCalls:['note move --pitch …','vocalparam write …'],note:'干跑：一个字节都没写。'}}]}),
          {status:200, headers:{'Content-Type':'application/json'}}));
      }
      return orig.apply(this, arguments);
    };
  })();`;
  try { new Function(stub); } catch (e) {
    child.kill(); console.error('桩的语法就错了：' + e.message); process.exit(1);
  }
  await cdp(ws, ++seq, 'Page.enable', {});
  await cdp(ws, ++seq, 'Page.addScriptToEvaluateOnNewDocument', { source: stub });
  await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'xcopy.html' });

  try {
    let ready = false;
    for (let i = 0; i < 60; i++) {
      try {
        if (await evalJS(ws, `typeof $ === 'function' && !!window.S && !!S.mm && (S.mm.tracks||[]).length === 2`)) { ready = true; break; }
      } catch (e) { /* 导航中 */ }
      await sleep(200);
    }
    ok('页面用**两条轨**的假缩略图起来了', ready, ready ? '' : '没等到 S.mm.tracks=2');

    ok('档位来自**所有轨道**（两条都列出来）',
      (await evalJS(ws, `Array.from($('src').options).map(function(o){return o.value}).join(',')`)) === '0,1',
      await evalJS(ws, `$('src').textContent`));
    ok('源轨默认 = 当前轨（0），目标轨自动落到**另一条**',
      (await evalJS(ws, `$('src').value`)) === '0' && (await evalJS(ws, `$('dst').value`)) === '1',
      await evalJS(ws, `$('src').value + '→' + $('dst').value`));

    // ① 范围：默认不限（框是灰的、提示写着"不限范围"）
    ok('默认不限范围（两个小节框是灰的）',
      (await evalJS(ws, `$('b1').disabled && $('b2').disabled`)) === true &&
      /不限范围/.test(await evalJS(ws, `$('rangeInfo').textContent`)),
      await evalJS(ws, `$('rangeInfo').textContent`));
    await evalJS(ws, `(function(){ $('useRange').checked=true; $('useRange').dispatchEvent(new Event('change'));
      $('b1').value='3'; $('b1').dispatchEvent(new Event('change'));
      $('b2').value='6'; $('b2').dispatchEvent(new Event('change')); return true; })()`);
    await sleep(200);
    // ⚠️ 单位口径（用户 2026-10-14「对外一律用拍」）：页面印的是**拍**，不再是 tick ——
    //    这条断言当时没跟着改，从那天起一直红着（2026-10-16 跑全量时发现并改正）
    ok('勾上「限定范围」⇒ 框可用、提示给出工程范围（第 8–24 拍 = 3840..11520 tick）',
      (await evalJS(ws, `$('b1').disabled`)) === false &&
      /范围：第 8–24 拍（第 3–6 小节）/.test(await evalJS(ws, `$('rangeInfo').textContent`)),
      await evalJS(ws, `$('rangeInfo').textContent`));

    // ② 图上拖 = 拉范围（并自动勾上限定范围）
    await evalJS(ws, `(function(){ $('useRange').checked=false; $('useRange').dispatchEvent(new Event('change')); return true; })()`);
    const rect = JSON.parse(await evalJS(ws, `JSON.stringify($('mm').getBoundingClientRect())`));
    const xForBar = (b) => rect.left + 8 + ((b - 1) * 1920 / 19200) * (rect.width - 16);
    await cdp(ws, ++seq, 'Input.dispatchMouseEvent', { type: 'mousePressed', x: xForBar(2), y: rect.top + 100, button: 'left', buttons: 1, clickCount: 1 });
    await cdp(ws, ++seq, 'Input.dispatchMouseEvent', { type: 'mouseMoved', x: xForBar(5), y: rect.top + 100, button: 'left', buttons: 1 });
    await cdp(ws, ++seq, 'Input.dispatchMouseEvent', { type: 'mouseReleased', x: xForBar(5), y: rect.top + 100, button: 'left', buttons: 0, clickCount: 1 });
    await sleep(250);
    ok('在图上拖 ⇒ 自动勾上「限定范围」并把小节刷成 2–5',
      (await evalJS(ws, `$('useRange').checked`)) === true &&
      (await evalJS(ws, `$('b1').value`)) === '2' && (await evalJS(ws, `$('b2').value`)) === '5',
      await evalJS(ws, `$('b1').value + '-' + $('b2').value`));

    // ③ 目标轨一换 ⇒ 重读**目标轨**的花名册（0 是 v2 只有 user；1 是 v1 有 envelope/direct）
    await evalJS(ws, `(function(){ $('params').checked=true; $('dst').value='1'; $('dst').dispatchEvent(new Event('change')); return true; })()`);
    await sleep(400);
    ok('目标轨是 v1 ⇒ 参数表**两列**（实参 → direct、包络 → envelope）',
      (await evalJS(ws, `S.roster.engineGeneration`)) === 'verse24' &&
      (await evalJS(ws, `Array.from($('ptab').querySelectorAll('th')).map(function(h){return h.textContent}).join('|')`))
        === '参数（目标轨）|实参→ direct|包络→ envelope',
      await evalJS(ws, `Array.from($('ptab').querySelectorAll('th')).map(function(h){return h.textContent}).join('|')`));
    await evalJS(ws, `(function(){ $('dst').value='0'; $('dst').dispatchEvent(new Event('change')); return true; })()`);
    await sleep(400);
    ok('换成 v2 的轨 ⇒ 参数表**只剩一列**（实参 → user），换轨就重读花名册',
      (await evalJS(ws, `S.roster.engineGeneration`)) === 'singing-mamba' &&
      (await evalJS(ws, `Array.from($('ptab').querySelectorAll('th')).map(function(h){return h.textContent}).join('|')`))
        === '参数（目标轨）|实参→ user',
      await evalJS(ws, `Array.from($('ptab').querySelectorAll('th')).map(function(h){return h.textContent}).join('|')`));

    // ④ 预览/执行发什么
    // ⚠️ 先验**竞态**：换目标轨后**同一拍**里勾参数曲线时，参数表必须是**空**
    //（换轨时旧花名册被同步清掉 ⇒ 表格没有列可画），绝不能按上一条轨的层画一张错的表。
    // 必须**在同一个表达式里**读（跨一次 CDP 往返的话 fetch 早回来了，测不到这个窗口）。
    const raceCells = await evalJS(ws, `(function(){ $('dst').value='1'; $('dst').dispatchEvent(new Event('change'));
      $('params').checked=true; return JSON.stringify(pickedParams()) + ' / ' + $('ptHint').textContent; })()`);
    ok('换轨后同一拍看参数表 ⇒ 空，并说清"还没取到目标轨的花名册"（不按上一条轨的层画表）',
      raceCells.indexOf('{}') === 0 && /还没取到目标轨的花名册/.test(raceCells), '[' + raceCells + ']');
    // 等花名册取回来 ⇒ 应当变成目标轨（v1）的两列，且实参列默认勾上
    for (let i = 0; i < 20 && !(await evalJS(ws, `!!S.roster && S.roster.engineGeneration === 'verse24'`)); i++) await sleep(150);
    ok('花名册取回后表跟上目标轨（两列，实参默认勾）',
      (await evalJS(ws, `JSON.stringify(pickedParams())`)) === '{"tension":["effective"]}' &&
      (await evalJS(ws, `$('ptab').querySelector('input[data-p=tension][data-c=envelope]').checked`)) === false,
      await evalJS(ws, `JSON.stringify(pickedParams())`));

    await evalJS(ws, `(function(){ $('dur').checked=true; $('cons').checked=true; $('dyn').checked=true;
      window.__req.length=0; $('prev').click(); return true; })()`);
    await sleep(600);
    const reqs = JSON.parse(await evalJS(ws, `JSON.stringify(window.__req.filter(function(x){return x.toolId==='xcopy.notes'}))`));
    ok('点「预览」发 xcopy.notes', reqs.length === 1, reqs.length + ' 条');
    const v = (reqs[0] || {}).values || {};
    ok('轨道号 / 范围 / 干跑都带上',
      v.srcTrack === '0' && v.dstTrack === '1' && v.beginBar === '2' && v.endBar === '5' && v.dryRun === '1',
      JSON.stringify([v.srcTrack, v.dstTrack, v.beginBar, v.endBar, v.dryRun]));
    ok('复制内容与「轨内复制」同一套（含时值/辅音/参数表/dynamic）',
      v.lyrics === '1' && v.pitch === '1' && v.phonemes === '1' && v.dur === '1' && v.consonants === '1' &&
      v.params === '1' && v.dynamic === '1' && v.paramSel === '{"tension":["effective"]}' && !v.paramLayers,
      JSON.stringify([v.dur, v.consonants, v.params, v.paramSel, v.dynamic]));
    ok('报告里报"配上几个"+ 只搬手动改过的提示',
      /配上 3 个/.test(await evalJS(ws, `$('rep').textContent`)) &&
      /（只搬手动改过的）/.test(await evalJS(ws, `$('rep').textContent`)),
      (await evalJS(ws, `$('rep').textContent`)).split('\n')[0]);

    await evalJS(ws, `(function(){ window.__req.length=0; $('run').click(); return true; })()`);
    await sleep(600);
    const rq2 = JSON.parse(await evalJS(ws, `JSON.stringify(window.__req.filter(function(x){return x.toolId==='xcopy.notes'}))`));
    ok('点「执行」= 真写（dryRun=0）', rq2.length === 1 && rq2[0].values.dryRun === '0', rq2.length ? String(rq2[0].values.dryRun) : '');

    // ⑤ 本地挡住的两种错
    await evalJS(ws, `(function(){ $('dst').value='0'; $('dst').dispatchEvent(new Event('change'));
      window.__req.length=0; $('prev').click(); return true; })()`);
    await sleep(300);
    ok('源轨=目标轨 ⇒ 本地挡下并指向「轨内复制」',
      (await evalJS(ws, `window.__req.filter(function(x){return x.toolId==='xcopy.notes'}).length`)) === 0 &&
      /轨内复制/.test(await evalJS(ws, `$('rep').textContent`)), await evalJS(ws, `$('rep').textContent`));
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
