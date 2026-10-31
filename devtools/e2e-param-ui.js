// 「参数映射」窗口（param.html）的页面接线：范围三档、小节框、拖动、筛选折叠、v1/v2 的层勾选、预览/执行发什么。
//
// 为什么这么测：这一页的**判据**是"发出去的参数对不对"（范围换算、层、筛选），
// 真写工程太脏 ⇒ 照 e2e-quicktip 的做法**把 /api/run 拦下来**（param.view 回一份假快照、
// param.sync 回一份假报告），只看页面**发出去什么**。真机上"读得对不对"由 param.view 的真跑覆盖。
//
// 用法：node devtools/e2e-param-ui.js
const { spawn } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const EDGE = [
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
].find((p) => fs.existsSync(p));
const BASE = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json'), 'utf8')).url;
const PORT = 9900 + (process.pid % 90);
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

// 假快照：片段 0..3840 tick（两小节 4/4）、4 个音符、引擎按 mode 变
function viewJSON(mode) {
  const v1 = mode !== 'v2';
  const layers = v1
    ? [{ layer: 'baseline', access: 'read-only', shape: 'dense' },
       { layer: 'direct', access: 'read-write', shape: 'dense' },
       { layer: 'envelope', access: 'read-write', shape: 'dense' }]
    : [{ layer: 'baseline', access: 'read-only', shape: 'dense' },
       { layer: 'user', access: 'read-write', shape: 'dense' }];
  const mk = (p, dn, min, max) => ({
    param: p, displayName: dn, available: true, scale: v1 ? 'envelope' : 'model',
    valueMin: min, valueMax: max,
    layers: layers, writableLayers: layers.filter((l) => l.access === 'read-write').map((l) => l.layer),
  });
  return {
    clipName: '测试片段', clipUuid: '{c1}', trackIndex: 0, clipIndex: 0, clipBegin: 0, clipEnd: 3840, clipTicks: 3840,
    meter: '4/4', ticksPerBar: 1920, tempo: 120, clipBars: 2,
    notes: [{ pos: 0, dur: 960, pitch: 60 }, { pos: 960, dur: 960, pitch: 64 },
            { pos: 1920, dur: 960, pitch: 67 }, { pos: 2880, dur: 960, pitch: 62 }],
    minPitch: 60, maxPitch: 67,
    hasSelection: true, selBegin: 960, selEnd: 2880, selNoteCount: 2,
    engineGeneration: v1 ? 'verse24' : 'singing-mamba', vocalControlRoute: 'dynamic',
    params: [mk('tension', 'Tension', v1 ? 0.3 : 0, v1 ? 2.5 : 1),
             mk('energy', 'Energy', v1 ? 0 : 0, v1 ? 2 : 1),
             { param: 'pitch', displayName: 'Pitch', available: false, unavailableReason: 'not on this surface' }],
  };
}

(async () => {
  const child = spawn(EDGE, [
    '--headless=new', '--disable-gpu', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-param-' + PORT),
    BASE + 'param.html',
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

  // ⚠️ 桩必须在**页面脚本之前**装上：`boot()` 一上来就真发一次 `param.view`，
  // 而"装完桩再点刷新"会跟那次**在途的真请求**抢 —— 真回包晚到就把假快照盖掉了（第一版就栽在这）。
  // 用 `Page.addScriptToEvaluateOnNewDocument` 在文档创建时就注入。
  const stub = `(function(){
    window.__req = []; window.__mode = 'v1';
    window.__mkView = function(mode){ return ${viewJSON.toString()}(mode); };
    var orig = window.fetch;
    window.fetch = function(u, o){
      if (String(u).indexOf('/api/run') >= 0) {
        var body = JSON.parse(o.body);
        window.__req.push(body);
        if (body.toolId === 'param.view') {
          return Promise.resolve(new Response(JSON.stringify({ok:true,calls:[{json: window.__mkView(window.__mode)}]}), {status:200, headers:{'Content-Type':'application/json'}}));
        }
        return Promise.resolve(new Response(JSON.stringify({ok:true,calls:[{json:{srcParam:body.values.srcParam,dstParam:body.values.dstParam,
          srcFrom:'effective',layers:String(body.values.layers).split(','),ticks:1920,scaleFactor:Number(body.values.scale),offset:Number(body.values.offset),
          dstValueMin:0,dstValueMax:2,perLayer:[{layer:'direct',ticksChanged:1920,ticksFilteredOut:0,ticksClamped:0,ticksNoSource:0,ticksDrawnBefore:0}],
          sampleAfter:[0.6,0.6,0.6,0.6],rangeDesc:'x'}}]}), {status:200, headers:{'Content-Type':'application/json'}}));
      }
      return orig.apply(this, arguments);
    };
  })();`;
  await cdp(ws, ++seq, 'Page.enable', {});
  await cdp(ws, ++seq, 'Page.addScriptToEvaluateOnNewDocument', { source: stub });
  await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'param.html' });

  try {
    // 等页面自己的脚本跑完（`$` 是它定义的）+ 假快照落地
    let ready = false;
    for (let i = 0; i < 60; i++) {
      try {
        if (await evalJS(ws, `typeof $ === 'function' && !!window.S && !!S.view && S.view.clipTicks === 3840`)) { ready = true; break; }
      } catch (e) { /* 导航中 */ }
      await sleep(200);
    }
    ok('页面用**假快照**起来了（片段 3840 tick / 2 小节）', ready,
      ready ? '' : '没等到 S.view.clipTicks=3840');

    ok('页面读到了快照（片段名/引擎/小节数都显示出来）',
      /verse24/.test(await evalJS(ws, `$('stat').textContent`)) &&
      /测试片段/.test(await evalJS(ws, `$('rangeInfo').textContent`)),
      await evalJS(ws, `$('stat').textContent`));

    // ---- ① 范围三档 ----
    ok('三档都在（全 clip / 所选音符 / 指定）', (await evalJS(ws, `document.querySelectorAll('#segs button').length`)) === 3);
    // ⚠️ 单位口径（用户 2026-10-14「对外一律用拍」）：页面印的是**拍**，不再是 tick ——
    //    这三条断言当时没跟着改，于是从那天起一直红着（2026-10-16 做 📌 时跑全量才看见，已改正）
    ok('默认是「全 clip」，范围 = 整段 8 拍（= 3840 tick）',
      /范围：片段内第 0–8 拍（8 拍 · 第 1–2 小节）/.test(await evalJS(ws, `$('rangeInfo').textContent`)), await evalJS(ws, `$('rangeInfo').textContent`));
    ok('「所选音符」档可用（快照里 hasSelection=true）', (await evalJS(ws, `document.querySelector('#segs button[data-r=sel]').disabled`)) === false);
    await evalJS(ws, `(function(){ document.querySelector('#segs button[data-r=sel]').click(); return true; })()`);
    await sleep(200);
    ok('切「所选音符」⇒ 范围变成选区那一段（2–6 拍 = 960..2880 tick）',
      /范围：片段内第 2–6 拍/.test(await evalJS(ws, `$('rangeInfo').textContent`)), await evalJS(ws, `$('rangeInfo').textContent`));

    // ---- ② 指定 + 小节框 ----
    await evalJS(ws, `(function(){ document.querySelector('#segs button[data-r=bars]').click(); return true; })()`);
    await sleep(200);
    ok('切「指定」⇒ 两个小节框从当前范围反算（1–2）',
      (await evalJS(ws, `$('b1').value`)) === '1' && (await evalJS(ws, `$('b2').value`)) === '2',
      await evalJS(ws, `$('b1').value + '-' + $('b2').value`));
    await evalJS(ws, `(function(){ $('b1').value='2'; $('b1').dispatchEvent(new Event('change')); return true; })()`);
    await sleep(200);
    ok('改开始小节 ⇒ 范围跟着变（第 2 小节 = 片段内 4–8 拍 = 1920..3840 tick）',
      /范围：片段内第 4–8 拍/.test(await evalJS(ws, `$('rangeInfo').textContent`)), await evalJS(ws, `$('rangeInfo').textContent`));
    ok('小节框是**精确到小节**的整数框（step=1, min=1）',
      (await evalJS(ws, `$('b1').getAttribute('step')`)) === '1' && (await evalJS(ws, `$('b1').getAttribute('min')`)) === '1');

    // ---- ③ 拖动缩略图 = 选范围（并自动切到「指定」）----
    await evalJS(ws, `(function(){ document.querySelector('#segs button[data-r=clip]').click(); return true; })()`);
    await sleep(150);
    const rect = JSON.parse(await evalJS(ws, `JSON.stringify($('mm').getBoundingClientRect())`));
    const xForBar = (b) => rect.left + 8 + ((b - 1) * 1920 / 3840) * (rect.width - 16);
    // 从第 1 小节拖到第 2 小节（按下 → 移动 → 松开）
    // ⚠️ 移动那一笔必须带 `buttons: 1`（按住左键的位掩码），否则 Chromium 当**悬停移动**、
    // 页面里的 pointermove 不触发 —— 第一版就是这么让"拖动"没生效的。
    await cdp(ws, ++seq, 'Input.dispatchMouseEvent', { type: 'mousePressed', x: xForBar(1), y: rect.top + 60, button: 'left', buttons: 1, clickCount: 1 });
    await cdp(ws, ++seq, 'Input.dispatchMouseEvent', { type: 'mouseMoved', x: xForBar(2), y: rect.top + 60, button: 'left', buttons: 1 });
    await cdp(ws, ++seq, 'Input.dispatchMouseEvent', { type: 'mouseReleased', x: xForBar(2), y: rect.top + 60, button: 'left', buttons: 0, clickCount: 1 });
    await sleep(250);
    ok('在缩略图上拖 ⇒ 自动切到「指定」档',
      (await evalJS(ws, `document.querySelector('#segs button[data-r=bars]').className`)).indexOf('on') >= 0,
      await evalJS(ws, `document.querySelector('#segs button[data-r=bars]').className`));
    ok('拖出来的范围 = 按下那一小节到松开那一小节（1–2）',
      (await evalJS(ws, `$('b1').value`)) === '1' && (await evalJS(ws, `$('b2').value`)) === '2',
      await evalJS(ws, `$('b1').value + '-' + $('b2').value`));

    // ---- ④ 筛选：不开启就藏起来；条件2 选「无」也不显示 ----
    ok('没勾「开启筛选」时条件 1 是藏着的', (await evalJS(ws, `$('f1').className`)).indexOf('hide') >= 0);
    ok('条件 2 也藏着', (await evalJS(ws, `$('f2').className`)).indexOf('hide') >= 0);
    await evalJS(ws, `(function(){ $('fOn').checked=true; $('fOn').dispatchEvent(new Event('change')); return true; })()`);
    await sleep(150);
    ok('勾上 ⇒ 条件 1 出现', (await evalJS(ws, `$('f1').className`)).indexOf('hide') < 0);
    ok('条件 2 的模式选择出现、但条件 2 本身还藏着（默认「无」）',
      (await evalJS(ws, `$('f2wrap').className`)).indexOf('hide') < 0 &&
      (await evalJS(ws, `$('f2').className`)).indexOf('hide') >= 0);
    await evalJS(ws, `(function(){ $('f2Mode').value='and'; $('f2Mode').dispatchEvent(new Event('change')); return true; })()`);
    await sleep(150);
    ok('条件 2 选「且」⇒ 条件 2 出现', (await evalJS(ws, `$('f2').className`)).indexOf('hide') < 0);
    await evalJS(ws, `(function(){ $('fOn').checked=false; $('fOn').dispatchEvent(new Event('change')); return true; })()`);
    await sleep(150);
    ok('取消勾选 ⇒ 条件 1 / 条件 2 / 模式全藏回去',
      (await evalJS(ws, `$('f1').className`)).indexOf('hide') >= 0 &&
      (await evalJS(ws, `$('f2').className`)).indexOf('hide') >= 0 &&
      (await evalJS(ws, `$('f2wrap').className`)).indexOf('hide') >= 0);

    // ---- ⑤ v1：包络/真值 可多选（**包络在前、默认勾上**）；发出去的 layers 跟着勾选 ----
    ok('v1 给的是「包络 / 真值」两个勾选框（包络在前）',
      (await evalJS(ws, `Array.from(document.querySelectorAll('#layers input[data-l]')).map(function(i){return i.getAttribute('data-l')}).join(',')`)) === 'envelope,direct',
      await evalJS(ws, `$('layers').textContent`));
    ok('默认：**包络勾上、真值不勾**（用户 2026-10-07 口径）',
      (await evalJS(ws, `document.querySelector('#layers input[data-l=envelope]').checked`)) === true &&
      (await evalJS(ws, `document.querySelector('#layers input[data-l=direct]').checked`)) === false,
      await evalJS(ws, `JSON.stringify(Array.from(document.querySelectorAll('#layers input')).map(function(i){return i.getAttribute('data-l')+':'+i.checked}))`));
    // 顺手留一张证据图：把筛选展开着截（这张图就是"筛选不开启时藏起来"的反面）
    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 620, height: 760, deviceScaleFactor: 2, mobile: false });
    await evalJS(ws, `(function(){ $('fOn').checked=true; $('fOn').dispatchEvent(new Event('change'));
      $('f2Mode').value='and'; $('f2Mode').dispatchEvent(new Event('change')); return true; })()`);
    await sleep(250);
    const shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(__dirname, 'param-window.png'), Buffer.from(shot.data, 'base64'));
    await cdp(ws, ++seq, 'Emulation.clearDeviceMetricsOverride', {});
    await evalJS(ws, `(function(){ $('fOn').checked=false; $('fOn').dispatchEvent(new Event('change')); return true; })()`);
    await evalJS(ws, `(function(){
      document.querySelector('#layers input[data-l=direct]').checked = true;
      document.querySelector('#layers input[data-l=envelope]').checked = true;
      $('scale').value='2'; $('offset').value='0.5';
      window.__req.length = 0; $('prev').click(); return true; })()`);
    await sleep(500);
    let reqs = JSON.parse(await evalJS(ws, `JSON.stringify(window.__req.filter(function(x){return x.toolId==='param.sync'}))`));
    ok('点「预览」发 param.sync', reqs.length === 1, reqs.length + ' 条');
    let v = (reqs[0] || {}).values || {};
    ok('预览是干跑（dryRun=1）', v.dryRun === '1', String(v.dryRun));
    ok('两层都勾 ⇒ layers=envelope,direct（按显示顺序，包络在前）', v.layers === 'envelope,direct', String(v.layers));
    ok('倍率/偏移原样带上', v.scale === '2' && v.offset === '0.5', v.scale + '/' + v.offset);
    ok('范围按「指定」发的是两个小节号',
      v.range === 'bars' && /^[12]$/.test(v.beginBar) && /^[12]$/.test(v.endBar), JSON.stringify([v.range, v.beginBar, v.endBar]));
    ok('没开筛选 ⇒ 不带筛选参数（filter=off）', v.filter === 'off' && v.filterValue === undefined, JSON.stringify(v.filter));

    await evalJS(ws, `(function(){ window.__req.length=0; $('run').click(); return true; })()`);
    await sleep(500);
    reqs = JSON.parse(await evalJS(ws, `JSON.stringify(window.__req.filter(function(x){return x.toolId==='param.sync'}))`));
    ok('点「执行」发的是真写（dryRun=0）', reqs.length === 1 && reqs[0].values.dryRun === '0',
      reqs.length ? String(reqs[0].values.dryRun) : '');
    ok('执行完把结果报出来（改了几个 tick）',
      /会改 1920 个/.test(await evalJS(ws, `$('rep').textContent`)), await evalJS(ws, `$('rep').textContent`));

    // ---- ⑥ v2：没有包络/真值 ⇒ **「写进」整行隐藏**（没得选就不显示），但 layers 照样发 user ----
    await evalJS(ws, `(function(){ window.__mode='v2'; document.querySelector('#segs button[data-r=clip]').click(); $('refresh').click(); return true; })()`);
    await sleep(600);
    ok('v2：「写进」那一行整段隐藏（没有勾选框、也没有解释文字）',
      (await evalJS(ws, `$('layerRow').hidden`)) === true &&
      (await evalJS(ws, `document.querySelectorAll('#layers input[data-l]').length`)) === 0,
      await evalJS(ws, `document.getElementById('layers').textContent`));
    ok('v2 引擎名显示出来', /singing-mamba/.test(await evalJS(ws, `$('stat').textContent`)), await evalJS(ws, `$('stat').textContent`));
    // 行隐藏了也要能跑：layers 固定发该参数唯一可写的层
    await evalJS(ws, `(function(){ window.__req.length=0; $('prev').click(); return true; })()`);
    await sleep(500);
    const v2req = JSON.parse(await evalJS(ws, `JSON.stringify(window.__req.filter(function(x){return x.toolId==='param.sync'}))`));
    ok('v2 行隐藏时仍照常发 layers=user（不靠 Go 侧默认兜）',
      v2req.length === 1 && v2req[0].values.layers === 'user',
      v2req.length ? String(v2req[0].values.layers) : '没发请求');
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
