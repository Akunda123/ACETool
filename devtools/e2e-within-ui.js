// 「轨内复制」窗口（within.html）的页面接线：小节框、两个色框、图上拖动、本地"能配上几个"、
// 三个属性勾选、预览/执行的取值。
//
// 为什么这么测：这一页的判据是"发出去的三个小节号 + 三个开关对不对"，以及"本地预判的配对数跟
// Go 侧同一判据"。真改工程太脏 ⇒ 照 e2e-param-ui 的做法把 `/api/run` 拦下来（xcopy.view 回假快照、
// xcopy.within 回假报告）。真机"配对与写入"由 xcopywithin 的 Go 测试 + 真机干跑覆盖。
//
// 用法：node devtools/e2e-within-ui.js
const { spawn } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const EDGE = [
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
].find((p) => fs.existsSync(p));
const BASE = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json'), 'utf8')).url;
const PORT = 9950 + (process.pid % 40);
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

// 假快照：4/4、一小节 1920 tick；
// 片段 1 = 工程 0..15360（8 小节），音符是**一模一样的两段**（各 16 个、每 240 tick 一个）：
// 第 3 小节起 与 第 5 小节起 ⇒ 源放第 3 小节、目标放第 5 小节时"相对位置"能对上。
// ⭐ 片段 2 = 工程 15360..30720（**第 9 小节起**，8 个音符）—— 用来钉"作用域是整条轨"：
// 旧版只看编辑器里那一个片段，范围挪到第 9 小节就会算出 0 个音符（静默搬不到）。
function viewJSON() {
  const notes = [];
  for (let i = 0; i < 16; i++) notes.push({ pos: 3840 + i * 240, dur: 240, pitch: 60 + (i % 5) });
  for (let i = 0; i < 16; i++) notes.push({ pos: 7680 + i * 240, dur: 240, pitch: 60 + (i % 5) });
  const notes2 = [];
  // ⚠️ 从**片段内 3840** 起（绝对 19200 = 第 11 小节）：这样"目标挪到第 9 小节"仍然是**空的**
  //（那条断言要的是"配不上 ⇒ 什么都不会动"），而"目标挪到第 10 小节"就落在第二个片段里。
  for (let i = 0; i < 8; i++) notes2.push({ pos: 3840 + i * 240, dur: 240, pitch: 62 + (i % 4) });
  return {
    trackIndex: 0, trackName: '测试轨',
    clipUuid: '{c1}', clipName: '测试片段', clipIndex: 0, clipType: 'sing',
    clipBegin: 0, clipEnd: 15360, clipTicks: 15360, clipStartBar: 1,
    meter: '4/4', ticksPerBar: 1920, tempo: 120, durationTick: 30720, barCount: 16,
    clips: [
      { uuid: '{c1}', index: 0, name: '测试片段', type: 'sing', begin: 0, end: 15360,
        noteCount: notes.length, notes: notes, minPitch: 60, maxPitch: 64 },
      { uuid: '{c2}', index: 1, name: '第二片段', type: 'sing', begin: 15360, end: 30720,
        noteCount: notes2.length, notes: notes2, minPitch: 62, maxPitch: 65 },
    ],
    clipNoteCount: notes.length,
    // 参数花名册：给一份 **v1 样子**的（包络/真值都可写）⇒ 参数表应当是**实参 + 包络两列**
    engineGeneration: 'verse24',
    params: [
      { param: 'tension', displayName: 'Tension', available: true, scale: 'envelope', valueMin: 0.3, valueMax: 2.5,
        layers: [{ layer: 'baseline', access: 'read-only', shape: 'dense' },
                 { layer: 'direct', access: 'read-write', shape: 'dense' },
                 { layer: 'envelope', access: 'read-write', shape: 'dense' }],
        writableLayers: ['envelope', 'direct'] },
      { param: 'dynamic', displayName: 'Dynamic', available: true, scale: 'control', valueMin: -1, valueMax: 1,
        layers: [{ layer: 'user', access: 'read-write', shape: 'points' }], writableLayers: [] },
    ],
    // ⭐ 列由 **Go 侧** `paramColumnsOf` 按这份花名册推（页面不自己解释列的含义）——桩要照它给
    paramColumns: [
      { key: 'effective', label: '实参', dstLayer: 'direct', hint: '实参是**合并后的绝对值**曲线 ⇒ 落进目标的 `direct` 层' },
      { key: 'envelope', label: '包络', dstLayer: 'envelope', hint: '包络是**乘数层**（围绕 1.0）⇒ 落进目标的 `envelope` 层' },
    ],
  };
}

(async () => {
  const child = spawn(EDGE, [
    '--headless=new', '--disable-gpu', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-within-' + PORT),
    // ⚠️ 从 `about:blank` 起步：直接开目标 URL 再 `Page.navigate` 到**同一个 URL**，
    // Chromium 有时当"没导航"处理 ⇒ 注入的桩根本不执行。
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

  // 桩要在**页面脚本之前**装（boot() 一上来就真发 xcopy.view，晚装会被在途的真回包盖掉）
  const stub = `(function(){
    try { localStorage.removeItem('acetool.within'); } catch (e) {}   // 默认范围每次从零推
    window.__req = [];
    var orig = window.fetch;
    window.fetch = function(u, o){
      if (String(u).indexOf('/api/run') >= 0) {
        var body = JSON.parse(o.body);
        window.__req.push(body);
        if (body.toolId === 'xcopy.view') {
          // ⚠️ 注入的是**源码文本**：「function f(){…}()」会被当成「声明 + 一个空表达式」直接语法错误
          //（整段桩静默不执行，表现为 window.__req undefined）⇒ 必须用括号包成**函数表达式**再调用。
          return Promise.resolve(new Response(JSON.stringify({ok:true,calls:[{json: (${viewJSON.toString()})()}]}), {status:200, headers:{'Content-Type':'application/json'}}));
        }
        var v = body.values;
        return Promise.resolve(new Response(JSON.stringify({ok:true,calls:[{json:{mode:'within',srcTrack:0,dstTrack:0,pairCount:1,matched:0,srcOnly:0,dstOnly:0,
          pairs:[{pair:1,srcClip:'第 '+v.srcStart+'–'+v.srcEnd+' 小节（片段内 tick 3840..7680）',dstClip:'第 '+v.dstStart+' 小节起（片段内 tick 9600..13440）',
            srcNotes:16,dstNotes:0,matched:0,srcOnly:16,dstOnly:0,srcEndTick:7680,srcAbsTick:3840,srcEndAbsTick:7680,dstAbsTick:9600,dstEndAbsTick:13440,clipBeginAbsTick:0}],
          skipped:['测试跳过项'],plannedCalls:['note set-lyric …','note move --pitch …'],note:'干跑：一个字节都没写。'}}]}),
          {status:200, headers:{'Content-Type':'application/json'}}));
      }
      return orig.apply(this, arguments);
    };
  })();`;
  // ⚠️ 先自己验一遍桩的**语法**：注入的是一段源码文本，语法错会**静默**不执行
  //（表现：window.__req undefined、页面照旧用真数据）——这坑踩过一次，这里直接把它拦住。
  try { new Function(stub); } catch (e) {
    child.kill(); console.error('桩的语法就错了，先修它：' + e.message); process.exit(1);
  }
  await cdp(ws, ++seq, 'Page.enable', {});
  await cdp(ws, ++seq, 'Page.addScriptToEvaluateOnNewDocument', { source: stub });
  await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'within.html' });

  try {
    let ready = false;
    for (let i = 0; i < 60; i++) {
      try {
        if (await evalJS(ws, `typeof $ === 'function' && !!window.S && !!S.view && S.view.clipTicks === 15360`)) { ready = true; break; }
      } catch (e) { /* 导航中 */ }
      await sleep(200);
    }
    ok('页面用**假快照**起来了（4/4 · 一小节 1920）', ready, ready ? '' : '没等到 S.view.clipTicks=15360');

    ok('三个小节框都在（源开始/源结束/目标开始）',
      (await evalJS(ws, `['s1','s2','d1'].every(function(i){return !!$(i)})`)) === true);
    ok('默认范围落在**第一个音符**那一带（源 3 起，不是空段）',
      (await evalJS(ws, `String(S.s1)`)) === '3' && (await evalJS(ws, `String(S.s2)`)) === '5',
      await evalJS(ws, `S.s1 + '-' + S.s2 + ' → ' + S.d1`));

    // ① 本地"能配上几个"：源放第 3–4 小节、目标放第 5 小节（那段音符的**相对位置一样**）⇒ 应当配上
    await evalJS(ws, `(function(){ S.s1=3; S.s2=4; S.d1=5; S.exact=null; render(); return true; })()`);
    await sleep(150);
    const lc = JSON.parse(await evalJS(ws, `JSON.stringify(liveCounts())`));
    ok('本地预判：源 3–4 小节对上目标 5 起 ⇒ 按位置能配上 9 个',
      lc.matched === 9 && lc.src === 9 && lc.dst === 9, JSON.stringify(lc));
    await evalJS(ws, `(function(){ S.d1=9; S.exact=null; render(); return true; })()`);
    await sleep(150);
    const lc2 = JSON.parse(await evalJS(ws, `JSON.stringify(liveCounts())`));
    ok('目标挪到空白处 ⇒ 一个都配不上、并在界面上说清"什么都不会动"',
      lc2.matched === 0 && /什么都不会动/.test(await evalJS(ws, `$('rangeInfo').textContent`)),
      JSON.stringify(lc2));

    // ⭐ 用户 2026-10-07：「作用于轨内所有 clip，遍历绝对时间」——
    // 范围落到**第 2 个片段**里也要算得出来（旧版只看当前片段 ⇒ 这里会傻乎乎算 0 个）
    const lcClip2 = JSON.parse(await evalJS(ws, `(function(){ S.d1=11; S.exact=null; render();
      return JSON.stringify(liveCounts()); })()`));
    ok('⭐ 范围落在**第 2 个片段**里也算得出来（作用域=整条轨，不再只看当前片段）',
      lcClip2 && lcClip2.dst === 8, JSON.stringify(lcClip2));
    ok('⭐ 页面写清作用域是"整条轨 / 几个片段"（不再说"当前片段"）',
      /作用域：整条轨/.test(await evalJS(ws, `$('rangeInfo').textContent`)) &&
      /2 个片段/.test(await evalJS(ws, `$('rangeInfo').textContent`)),
      (await evalJS(ws, `$('rangeInfo').textContent`)).split('\n')[0]);
    await evalJS(ws, `(function(){ S.d1=6; S.exact=null; render(); return true; })()`);

    // ② 两个色框：源与目标都在画布上（各自的 x 区间不重叠且顺序正确）
    await evalJS(ws, `(function(){ S.s1=3; S.s2=4; S.d1=5; S.exact=null; render(); return true; })()`);
    await sleep(150);
    const box = JSON.parse(await evalJS(ws, `JSON.stringify(S.boxes)`));
    ok('源框在目标框左边（两个色框各画一段）',
      box.src[0] < box.src[1] && box.dst[0] < box.dst[1] && box.src[1] <= box.dst[0] + 1,
      JSON.stringify(box));

    // ③ 图上拖动：空白处拖 = 拉源范围（按下一整段）
    const rect = JSON.parse(await evalJS(ws, `JSON.stringify($('mm').getBoundingClientRect())`));
    // ⚠️ 画布横轴是 `max(durationTick, clipEnd, 1920)` ⇒ 从页面里现取，别写死（改了桩的总长就会错位）
    const mmTotal = await evalJS(ws, `Math.max((S.view&&S.view.durationTick)||0, (S.view&&S.view.clipEnd)||0, 1920)`);
    const xForBar = (b) => rect.left + 8 + ((b - 1) * 1920 / mmTotal) * (rect.width - 16);
    await cdp(ws, ++seq, 'Input.dispatchMouseEvent', { type: 'mousePressed', x: xForBar(2), y: rect.top + 90, button: 'left', buttons: 1, clickCount: 1 });
    await cdp(ws, ++seq, 'Input.dispatchMouseEvent', { type: 'mouseMoved', x: xForBar(4), y: rect.top + 90, button: 'left', buttons: 1 });
    await cdp(ws, ++seq, 'Input.dispatchMouseEvent', { type: 'mouseReleased', x: xForBar(4), y: rect.top + 90, button: 'left', buttons: 0, clickCount: 1 });
    await sleep(250);
    ok('在图上拖 ⇒ 源范围变成按下那一小节到松开那一小节（2–4）',
      (await evalJS(ws, `String(S.s1)`)) === '2' && (await evalJS(ws, `String(S.s2)`)) === '4',
      await evalJS(ws, `S.s1 + '-' + S.s2`));

    // ④ 三个开关 + 预览/执行的取值
    await evalJS(ws, `(function(){ S.s1=3; S.s2=5; S.d1=6; render();
      $('phon').checked=false; window.__req.length=0; $('prev').click(); return true; })()`);
    await sleep(500);
    let reqs = JSON.parse(await evalJS(ws, `JSON.stringify(window.__req.filter(function(x){return x.toolId==='xcopy.within'}))`));
    ok('点「预览」发 xcopy.within', reqs.length === 1, reqs.length + ' 条');
    let v = (reqs[0] || {}).values || {};
    ok('三个小节号按原样发出', v.srcStart === '3' && v.srcEnd === '5' && v.dstStart === '6', JSON.stringify([v.srcStart, v.srcEnd, v.dstStart]));
    ok('预览 = 干跑（dryRun=1）', v.dryRun === '1', String(v.dryRun));
    ok('三个属性勾选原样带上（音素取消掉了）',
      v.lyrics === '1' && v.pitch === '1' && v.phonemes === '0', JSON.stringify([v.lyrics, v.pitch, v.phonemes]));
    const rep = await evalJS(ws, `$('rep').textContent`);
    ok('报告里报"配上几个"与落单数', /配上 0 个/.test(rep) && /找不到对应位置/.test(rep), rep.split('\n')[1] || rep);
    ok('预览回包的**精确 tick** 被用来画框了（不再用等拍号估算）',
      (await evalJS(ws, `JSON.stringify(S.exact)`)) === '{"srcAbs":3840,"srcEndAbs":7680,"dstAbs":9600,"dstEndAbs":13440}',
      await evalJS(ws, `JSON.stringify(S.exact)`));

    await evalJS(ws, `(function(){ window.__req.length=0; $('run').click(); return true; })()`);
    await sleep(500);
    reqs = JSON.parse(await evalJS(ws, `JSON.stringify(window.__req.filter(function(x){return x.toolId==='xcopy.within'}))`));
    ok('点「执行」发的是真写（dryRun=0）', reqs.length === 1 && reqs[0].values.dryRun === '0', reqs.length ? String(reqs[0].values.dryRun) : '');

    // ④b ⭐ 2026-10-07「除 uuid 外全都要」补齐的选项：时值 / 辅音时长 / 参数曲线（+参数表）/ dynamic
    ok('新增四个开关都在（时值 / 辅音时长 / 参数曲线 / dynamic 锚点）',
      (await evalJS(ws, `['dur','cons','params','dyn'].every(function(i){return !!$(i)})`)) === true);
    ok('参数曲线没勾时**整块参数表隐藏**（省得看着能选）',
      (await evalJS(ws, `$('ptWrap').hidden`)) === true);
    await evalJS(ws, `(function(){ $('params').checked=true; renderParamTable(); return true; })()`);
    await sleep(150);
    // ⭐ 用户 2026-10-07：「参数展开，列出所有参数，列个表格，勾选，如果是 v1 就实参和包络列两列」
    ok('⭐ 勾了参数曲线 ⇒ 展开参数表，**v1 两列**：实参 + 包络（表头写着落点）',
      (await evalJS(ws, `Array.from($('ptab').querySelectorAll('th')).map(function(h){return h.textContent}).join('|')`))
        === '参数|实参→ direct|包络→ envelope',
      await evalJS(ws, `Array.from($('ptab').querySelectorAll('th')).map(function(h){return h.textContent}).join('|')`));
    ok('⭐ 行 = 花名册里**能逐 tick 写**的参数（tension 有；dynamic 是锚点形状 ⇒ 不进行里）',
      (await evalJS(ws, `Array.from($('ptab').querySelectorAll('td.pname')).map(function(t){return t.textContent}).join(',')`)) === 'tension',
      await evalJS(ws, `$('ptab').textContent`));
    ok('⭐ 实参列默认勾、包络列默认不勾',
      (await evalJS(ws, `$('ptab').querySelector('input[data-p=tension][data-c=effective]').checked`)) === true &&
      (await evalJS(ws, `$('ptab').querySelector('input[data-p=tension][data-c=envelope]').checked`)) === false);
    ok('⭐ 勾选状态就是发给工具的 `paramSel`（默认 {tension:[effective]}）',
      (await evalJS(ws, `JSON.stringify(pickedParams())`)) === '{"tension":["effective"]}',
      await evalJS(ws, `JSON.stringify(pickedParams())`));
    // 两列都勾上 ⇒ 两条曲线都搬（同一参数的两格各是一条曲线）
    await evalJS(ws, `(function(){ $('ptab').querySelector('input[data-p=tension][data-c=envelope]').checked=true;
      $('ptab').querySelector('input[data-p=tension][data-c=envelope]').dispatchEvent(new Event('change')); return true; })()`);
    ok('⭐ 两列都勾 ⇒ `paramSel` 带上两条曲线（实参+包络），提示写明"每格 = 一条曲线、1 格 = 1 条撤销"',
      (await evalJS(ws, `JSON.stringify(pickedParams())`)) === '{"tension":["effective","envelope"]}' &&
      /每格 = 一条曲线/.test(await evalJS(ws, `$('ptHint').textContent`)),
      await evalJS(ws, `JSON.stringify(pickedParams()) + ' / ' + $('ptHint').textContent`));
    // 一格都不勾 ⇒ 页面上就要说清"这一项不会写任何东西"（Go 侧也会如实报）
    await evalJS(ws, `(function(){ $('ptab').querySelectorAll('input[data-p]').forEach(function(i){ i.checked=false; });
      paramHint(); return true; })()`);
    ok('⭐ 一格都不勾 ⇒ 页面就说清"这一项不会写任何东西"',
      /一个格子都没勾/.test(await evalJS(ws, `$('ptHint').textContent`)), await evalJS(ws, `$('ptHint').textContent`));
    // 复原成默认（实参一列）
    await evalJS(ws, `(function(){ $('ptab').querySelector('input[data-p=tension][data-c=effective]').checked=true; return true; })()`);

    await evalJS(ws, `(function(){ S.s1=3; S.s2=5; S.d1=6; render();
      $('dur').checked=true; $('cons').checked=true; $('dyn').checked=true;
      window.__req.length=0; $('prev').click(); return true; })()`);
    await sleep(500);
    const reqs2 = JSON.parse(await evalJS(ws, `JSON.stringify(window.__req.filter(function(x){return x.toolId==='xcopy.within'}))`));
    const v2 = (reqs2[0] || {}).values || {};
    ok('新增三项原样发出（时值/辅音/dynamic）',
      v2.dur === '1' && v2.consonants === '1' && v2.dynamic === '1', JSON.stringify([v2.dur, v2.consonants, v2.dynamic]));
    ok('参数曲线带上 params=1 与**表格勾选**（paramSel=[{"tension":["effective"]}]）',
      v2.params === '1' && v2.paramSel === '{"tension":["effective"]}',
      JSON.stringify([v2.params, v2.paramSel, v2.paramLayers]));

    // ⑤ 本地挡住的两种错：属性全不勾 / 源结束 ≤ 源开始
    await evalJS(ws, `(function(){ ['lyrics','pitch','phon','dur','cons','params','dyn'].forEach(function(i){ $(i).checked=false; });
      window.__req.length=0; $('prev').click(); return true; })()`);
    await sleep(300);
    ok('所有属性都不勾 ⇒ 本地就挡下（不发请求）',
      (await evalJS(ws, `window.__req.filter(function(x){return x.toolId==='xcopy.within'}).length`)) === 0 &&
      /一个属性都没勾/.test(await evalJS(ws, `$('rep').textContent`)), await evalJS(ws, `$('rep').textContent`));
    await evalJS(ws, `(function(){ $('lyrics').checked=true; S.s1=5; S.s2=5; window.__req.length=0; $('prev').click(); return true; })()`);
    await sleep(300);
    ok('源结束 ≤ 源开始 ⇒ 本地挡下并说清',
      (await evalJS(ws, `window.__req.filter(function(x){return x.toolId==='xcopy.within'}).length`)) === 0 &&
      /要大于源开始小节/.test(await evalJS(ws, `$('rep').textContent`)), await evalJS(ws, `$('rep').textContent`));
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
