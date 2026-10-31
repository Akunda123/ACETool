// 「查找」页（`find.notes`）的新版式自检（用户 2026-10-08 的口径）：
//   ① 最上方 `fp-info` 显示检索结果、**框内不可编辑**
//   ② 下面有**结果预览**：`view.minimap` 那张图 + **命中位置高亮**（当前项更亮）
//   ③ 「全选目标音符」**只在音符模式**出现（参数模式不出现）
//   ④ 上一项 /【序号数字框】/ 下一项 —— 框里输入 index 直接跳
//   ⑤ `fpattr` 只在音符模式；`fpparam` + `fplayer`（含参数层说明）只在参数模式
//   ⑥ `fpcontent` 默认空着
//
// ⭐ 用户 2026-10-08：「我要的是进入 home.html 的**正式功能**，新的 web 页」⇒ 跑的是**独立窗口**
//    `find.html`（home.html 那颗按钮开的那个），最后再回主面板确认同一份模块两边都能挂。
//
// 真读 ACE 太脏/太慢 ⇒ 拦 `/api/run`：find.notes 回 3 条命中、view.minimap 回一张假工程、
// find.goto 回回读值。`/api/tools` 故意**不拦**（跑真清单，顺带验 schema 形状）。
// 用法：node devtools/e2e-find-ui.js [url]
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
const PORT = 9940 + (process.pid % 50);
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
    setTimeout(() => { ws.removeEventListener('message', onMsg); rej(new Error(method + ' 超时')); }, 25000);
  });
}
let seq = 100;
async function evalJS(ws, expr) {
  const r = await cdp(ws, ++seq, 'Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true });
  if (r.exceptionDetails) throw new Error('页面里抛错：' + ((r.exceptionDetails.exception && r.exceptionDetails.exception.description) || r.exceptionDetails.text));
  return r.result.value;
}

// 3 条命中（都是「当前片段」范围内的音符，位置各不相同）
function hitsJSON() {
  return {
    target: 'note', query: '音高 = 60', scope: 'clip', clipCount: 1, noteCount: 5, hitCount: 3,
    millis: 12, playheadTick: 0, startIndex: 0,
    hits: [
      { track: 0, clip: 0, trackName: '轨 0', clipName: '片段 A', noteIndex: 0, tick: 0, absTick: 0, posSv: '1.00', pitch: 60, pitchName: 'C4', value: '60', noteUuid: '{n1}' },
      { track: 0, clip: 0, trackName: '轨 0', clipName: '片段 A', noteIndex: 1, tick: 960, absTick: 960, posSv: '1.50', pitch: 60, pitchName: 'C4', value: '60', noteUuid: '{n2}' },
      { track: 0, clip: 0, trackName: '轨 0', clipName: '片段 A', noteIndex: 2, tick: 2880, absTick: 2880, posSv: '2.50', pitch: 60, pitchName: 'C4', value: '60', noteUuid: '{n3}' },
    ],
    note: '只读：没动工程。',
  };
}
// 一张假工程：轨 0 一个片段（tick 0–3840，含 3 个音符）+ 轨 1 一个片段
function mapJSON() {
  return {
    trackCount: 2, clipCount: 2, noteCount: 5, firstTick: 0, lastTick: 5760, meter: '4/4', tempo: 120,
    playheadTick: 0, noteBudget: 0, clipsWithoutNotes: 0,
    tracks: [
      { index: 0, name: '轨 0', type: 'sing', clips: [
        { index: 0, uuid: '{c1}', name: '片段 A', type: 'sing', begin: 0, end: 3840, noteCount: 3, color: '#859C3F',
          minPitch: 58, maxPitch: 64, notes: [{ pos: 0, dur: 480, pitch: 60 }, { pos: 960, dur: 480, pitch: 62 }, { pos: 2880, dur: 480, pitch: 64 }] },
      ] },
      { index: 1, name: '轨 1', type: 'instrument', clips: [
        { index: 0, uuid: '{c2}', name: '片段 B', type: 'instrument', begin: 1920, end: 5760, noteCount: 2, color: '#3F7C9C', minPitch: 48, maxPitch: 55 },
      ] },
    ],
    note: '缩略图读好了（假数据）。',
  };
}
function selfcheck() {
  return { online: true, version: { forAce: 'cli 0.17.0 · ACE Studio 2.1.8' }, project: { projectName: '测试工程' } };
}

(async () => {
  const child = spawn(EDGE, [
    '--headless=new', '--disable-gpu', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-find-' + PORT), 'about:blank',
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
        var j = null;
        if (body.toolId === 'find.notes') j = (${hitsJSON.toString()})();
        else if (body.toolId === 'view.minimap') j = (${mapJSON.toString()})();
        else if (body.toolId === 'find.goto') j = { tick: parseInt((body.values||{}).absTick || '0', 10), noteUuid: (body.values||{}).noteUuid || '' };
        else j = {};
        return Promise.resolve(new Response(JSON.stringify({ok:true,calls:[{json:j, exitCode:0, stdout:''}]}), {status:200, headers:{'Content-Type':'application/json'}}));
      }
      if (s.indexOf('/api/selfcheck') >= 0) {
        return Promise.resolve(new Response(JSON.stringify((${selfcheck.toString()})()), {status:200, headers:{'Content-Type':'application/json'}}));
      }
      return orig.apply(window, arguments);
    };
  })()`;
  try { new Function(stub.replace(/^\(function\(\)\{/, '').replace(/\}\)\(\)$/, '')); }
  catch (e) { child.kill(); console.error('桩自己就是语法错：' + e.message); process.exit(1); }

  try {
    await cdp(ws, ++seq, 'Page.enable', {});
    await cdp(ws, ++seq, 'Page.addScriptToEvaluateOnNewDocument', { source: stub });
    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 1200, height: 1000, deviceScaleFactor: 1, mobile: false });
    // ⭐ 跑的是 home.html 那颗按钮开的**独立窗口**
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'find.html' });
    await sleep(2200);
    for (let i = 0; i < 30 && !(await evalJS(ws, `!!document.getElementById('fpinfo')`)); i++) await sleep(200);

    // ⓪ 独立窗口：自己带 postRun（**不加载 app.js**）、顶栏有自检状态
    const shell = JSON.parse(await evalJS(ws, `(function(){
      var srcs = Array.prototype.slice.call(document.querySelectorAll('script[src]')).map(function(s){return s.getAttribute('src')}).join(',');
      return JSON.stringify({ srcs: srcs, appjs: /app\\.js/.test(srcs), postRun: typeof window.postRun,
        findPanel: typeof (window.FindPanel && window.FindPanel.mount), stat: document.getElementById('fstat').textContent });
    })()`));
    ok('⓪ 独立窗口 `find.html`：引了 minimap.js + find.js，**没引 app.js**，自带 postRun',
      /minimap\.js/.test(shell.srcs) && /find\.js/.test(shell.srcs) && shell.appjs === false &&
      shell.postRun === 'function' && shell.findPanel === 'function', JSON.stringify(shell));
    ok('⓪ 顶栏自检显示版本（这个窗口自己会自检）',
      /cli 0\.17\.0/.test(shell.stat), shell.stat);
    ok('⓪ 下拉是按 `/api/tools`（真清单）填出来的：属性下拉是**平铺**的（没有 optgroup、没有 ★）',
      (await evalJS(ws, `(function(){
        var s = document.getElementById('fpattr');
        if (!s || s.options.length <= 3) return false;
        if (s.querySelectorAll('optgroup').length !== 0) return false;
        for (var i = 0; i < s.options.length; i++) {
          if (s.options[i].textContent.indexOf('★') >= 0) return false;
        }
        return true;
      })()`)) === true);

    // ① 最上方是 fp-info：**动态、不可编辑**（是 `<pre>`，不是输入控件）+ 面板上没有多余的字
    const head = JSON.parse(await evalJS(ws, `(function(){
      var p = document.querySelector('.findpanel');
      var first = p && p.children[0];
      var t = document.getElementById('fpinfo');
      return JSON.stringify({
        firstId: first ? first.id : '', tag: t ? t.tagName : '',
        isInput: t ? (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable) : null,
        valueType: t ? typeof t.value : null,   // pre 没有 value（不是输入控件）
        before: t ? t.textContent : '',
      });
    })()`));
    ok('① 最上方就是 `fp-info`：**动态不可编辑**的框（`<pre>`，不是 input/textarea）',
      head.firstId === 'fpinfo' && head.tag === 'PRE' && head.isInput === false && head.valueType === 'undefined',
      JSON.stringify(head));
    const jpNames = JSON.parse(await evalJS(ws, `(function(){
      var t = document.querySelector('.findpanel').textContent;
      return JSON.stringify({
        preview: t.indexOf('结果预览') >= 0, mm: t.indexOf('view.minimap') >= 0,
        btns: !!document.getElementById('fpmapnotes') || !!document.getElementById('fpmapreload'),
      });
    })()`));
    ok('① 面板上没有「结果预览 / view.minimap / 显示音符 / 重读」这些多余的字与控件',
      jpNames.preview === false && jpNames.mm === false && jpNames.btns === false, JSON.stringify(jpNames));

    // ② 预览：view.minimap 读一次 + 高亮标记（3 条命中、当前项一条 cur）
    await sleep(400);
    const map = JSON.parse(await evalJS(ws, `(function(){
      var el = document.getElementById('fpmap');
      return JSON.stringify({
        svg: el ? !!el.querySelector('svg') : false,
        clips: el ? el.querySelectorAll('.mmclip').length : -1,
        marks: el ? el.querySelectorAll('.fpmk').length : -1,
        cur: el ? el.querySelectorAll('.fpmk.cur').length : -1,
        outline: el ? el.querySelectorAll('.mmclip rect[fill="none"]').length : -1,
        solid: el ? el.querySelectorAll('.mmclip rect[fill="#1a1a1a"]').length : -1,
        mapCalls: window.__req.filter(function(x){return x.toolId==='view.minimap'}).length,
        notes: window.__req.filter(function(x){return x.toolId==='view.minimap'}).map(function(x){return x.values.notes}).join(','),
      });
    })()`));
    ok('② 预览用 `view.minimap` 画出来了（2 个片段）', map.svg === true && map.clips === 2, JSON.stringify(map));
    ok('② 缩略图只读一次、**带音符**，音符画成**轮廓**（描边不填充）',
      map.mapCalls === 1 && map.notes === '24' && map.outline > 0 && map.solid === 0, JSON.stringify(map));
    // ⚠️ 回归：预览里那片"点击区"必须透明 —— `MinimapPanel.draw()` 忘了 ensureStyle() 的话，
    //    它会按 SVG 默认的黑填满整个片段（2026-10-08 实测：整条片段是黑的）
    ok('② 片段不是被那片点击区涂黑的（`.hit` 是 transparent + 片段底色是它自己的颜色）',
      (await evalJS(ws, `(function(){
        var body = document.querySelector('#fpmap .mmclip rect.body');
        var hit = document.querySelector('#fpmap .mmclip rect.hit');
        return getComputedStyle(hit).fill + '|' + getComputedStyle(body).fill;
      })()`)) === 'rgba(0, 0, 0, 0)|rgb(133, 156, 63)');

    // 查一次（音符模式）⇒ 3 条命中 + 序号框 = 1
    await evalJS(ws, `document.getElementById('fprefresh').click()`);
    await sleep(500);
    const after = JSON.parse(await evalJS(ws, `(function(){
      var el = document.getElementById('fpmap');
      return JSON.stringify({
        marks: el.querySelectorAll('.fpmk').length,
        cur: el.querySelectorAll('.fpmk.cur').length,
        curTick: (el.querySelector('.fpmk.cur')||{getAttribute:function(){return ''}}).getAttribute('data-tick'),
        mapCalls: window.__req.filter(function(x){return x.toolId==='view.minimap'}).length,
        idx: document.getElementById('fpindex').value,
        info: document.getElementById('fpinfo').textContent,
      });
    })()`));
    ok('② 命中位置高亮出来了（3 条标记、1 条是当前项）',
      after.marks === 3 && after.cur === 1 && after.curTick === '0', JSON.stringify(after));
    ok('② 重画高亮**不再读**工程缩略图（还是只读过 1 次）', after.mapCalls === 1, String(after.mapCalls));
    // ⭐ 用户 2026-10-08：「我不是说输入框默认没东西吗」⇒ 序号框**不写值**
    //    （当前第几项看结果框里的「序号: N/M」；这个框只用来"你输入 index"）
    ok('④ 序号框里**没东西**（不预填当前项）', after.idx === '', JSON.stringify(after.idx));
    ok('① 结果框里是检索结果（序号/位置）', /序号: 1\/3/.test(after.info) && /位置: 1\.00/.test(after.info), JSON.stringify(after.info));

    // ③⑤ 音符模式：全选 + fpattr 在；fpparam/fplayer 不在
    const noteMode = JSON.parse(await evalJS(ws, `(function(){
      var v = function(id){ var e = document.getElementById(id); if (!e) return 'missing';
        return e.offsetParent === null ? 'hidden' : 'shown'; };
      return JSON.stringify({ sel: v('fpselall'), attr: v('fpattrslot'), param: v('fpparamslot'), layer: v('fplayerslot') });
    })()`));
    ok('③⑤ 音符模式：显示「全选目标音符」+ `fpattr`，不显示 `fpparam`/`fplayer`',
      noteMode.sel === 'shown' && noteMode.attr === 'shown' && noteMode.param === 'hidden' && noteMode.layer === 'hidden',
      JSON.stringify(noteMode));

    // 切参数模式：反过来
    await evalJS(ws, `(function(){ var c=document.getElementById('fpisparam'); c.checked = true; c.dispatchEvent(new Event('change')); return true; })()`);
    await sleep(500);
    const paramMode = JSON.parse(await evalJS(ws, `(function(){
      var v = function(id){ var e = document.getElementById(id); if (!e) return 'missing';
        return e.offsetParent === null ? 'hidden' : 'shown'; };
      return JSON.stringify({ sel: v('fpselall'), attr: v('fpattrslot'), param: v('fpparamslot'), layer: v('fplayerslot'), target: document.getElementById('fpisparam').checked });
    })()`));
    ok('③⑤ 参数模式：显示 `fpparam`/`fplayer`，「全选目标音符」与 `fpattr` 都不出现',
      paramMode.sel === 'hidden' && paramMode.attr === 'hidden' && paramMode.param === 'shown' && paramMode.layer === 'shown',
      JSON.stringify(paramMode));

    // ④ 序号框输入 3 ⇒ 跳到第 3 条（absTick 2880 = **第 6 拍**）+ 高亮跟着走
    //    ⚠️ 先把模式切回**音符属性**：只有「范围=当前片段 且 目标=音符属性」才会**选中**那个音符
    //    （原脚本 resultInfo() 的口径）⇒ 参数模式下 `find.goto` 不带 noteUuid 是对的。
    //    ⚠️ 单位口径（用户 2026-10-14）：对外一律「拍」⇒ 页面发的是 `absBeat`（2880/480 = 6）。
    await evalJS(ws, `(function(){ var c=document.getElementById('fpisnote'); c.checked = true; c.dispatchEvent(new Event('change')); return true; })()`);
    await sleep(500);
    await evalJS(ws, `(function(){ window.__req.length=0;
      var i = document.getElementById('fpindex'); i.value = '3'; i.dispatchEvent(new Event('change')); return true; })()`);
    await sleep(500);
    const jumped = JSON.parse(await evalJS(ws, `(function(){
      var gs = window.__req.filter(function(x){return x.toolId==='find.goto'});
      var el = document.getElementById('fpmap');
      return JSON.stringify({
        gotos: gs.map(function(x){ return x.values.absBeat + '|' + (x.values.noteUuid||''); }).join(','),
        curTick: (el.querySelector('.fpmk.cur')||{getAttribute:function(){return ''}}).getAttribute('data-tick'),
        info: document.getElementById('fpinfo').textContent,
        idx: document.getElementById('fpindex').value,
      });
    })()`));
    ok('④ 序号框输入 3 ⇒ 发 `find.goto` 到第 3 条的**第 6 拍**（2880 tick）并带上它的 noteUuid',
      jumped.gotos === '6|{n3}', jumped.gotos);
    ok('④ 高亮与结果文本都跟着跳到第 3 项', jumped.curTick === '2880' && /序号: 3\/3/.test(jumped.info),
      JSON.stringify([jumped.curTick, jumped.info.split('\n')[0]]));
    ok('④ 跳完序号框清回空的（框里不留东西）', jumped.idx === '', JSON.stringify(jumped.idx));

    // ④b 越界序号：如实拒绝（不静默跳）
    await evalJS(ws, `(function(){ var i = document.getElementById('fpindex'); i.value = '9'; i.dispatchEvent(new Event('change')); return true; })()`);
    await sleep(300);
    const badIdx = JSON.parse(await evalJS(ws, `(function(){
      return JSON.stringify({ msg: document.getElementById('fpflash').textContent, idx: document.getElementById('fpindex').value });
    })()`));
    ok('④ 序号越界时如实说一声（不静默跳到别处）且框清空', /1–3/.test(badIdx.msg) && badIdx.idx === '', JSON.stringify(badIdx));

    // ④c 倒着数的口径：范围换「当前轨道」⇒ 当前第 1 条显示 3（看结果框，不看序号框）
    await evalJS(ws, `(function(){ window.__req.length=0;
      var s = document.getElementById('fpscope'); s.value='track'; s.dispatchEvent(new Event('change')); return true; })()`);
    await sleep(500);
    const revIdx = JSON.parse(await evalJS(ws, `(function(){
      return JSON.stringify({ info: document.getElementById('fpinfo').textContent, idx: document.getElementById('fpindex').value });
    })()`));
    ok('④ 范围=当前轨道时序号**倒着数**（复刻原脚本：当前第 1 条显示 3），序号框仍是空的',
      /序号: 3\/3/.test(revIdx.info) && revIdx.idx === '', JSON.stringify(revIdx));

    // ⑥ fpcontent 默认空着：**没有值、也没有 placeholder**（用户 2026-10-08：「输入框默认没东西」）
    ok('⑥ `fpcontent`（查找内容）默认是空的：没值、没 placeholder',
      (await evalJS(ws, `(function(){
        var el = document.getElementById('fpcontent');
        return el.value === '' && !el.getAttribute('placeholder');
      })()`)) === true);

    // ⑦ 独立窗口没有 app.js ⇒ 结果框**什么都不画**（不自己造一套结果清单出来）
    ok('⑦ 独立窗口不另造结果清单（`.fp-rep` 不存在、`#fpresult` 是空的）；结果在 `fpinfo` 里',
      /序号: \d\/3/.test(await evalJS(ws, `document.getElementById('fpinfo').textContent`)) &&
      (await evalJS(ws, `!document.querySelector('.fp-rep') && document.getElementById('fpresult').innerHTML === ''`)) === true);

    // 截图（给人看的证据）
    const shotPath = path.join(__dirname, 'find-panel.png');
    const shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
    fs.writeFileSync(shotPath, Buffer.from(shot.data, 'base64'));
    console.log('  落盘：devtools/find-panel.png');

    // ⑧ 同一份模块在主面板里也能挂（`index.html#find.notes` 走 app.js 那条路）
    //    先顺路拍一张 home.html：那颗「查找」按钮是这次加的**正式入口**
    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 760, height: 620, deviceScaleFactor: 2, mobile: false });
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'home.html' });
    await sleep(1600);
    const homeBtn = JSON.parse(await evalJS(ws, `(function(){
      var bs = Array.prototype.slice.call(document.querySelectorAll('#hgrid button.live'));
      var b = bs.filter(function(x){ return x.textContent === '查找'; })[0];
      return JSON.stringify({ n: bs.length, has: !!b, tip: b ? (b.getAttribute('data-tip') || b.title || '') : '' });
    })()`));
    ok('⑧ home.html 网格上有「查找」按钮（打开 find.html 的正式入口）',
      homeBtn.has === true && homeBtn.n > 10, JSON.stringify(homeBtn));
    const homeShot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(__dirname, 'home-find-button.png'), Buffer.from(homeShot.data, 'base64'));
    console.log('  落盘：devtools/home-find-button.png');

    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 1200, height: 1000, deviceScaleFactor: 1, mobile: false });
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'index.html#find.notes' });
    await sleep(1800);
    for (let i = 0; i < 30 && !(await evalJS(ws, `!!document.getElementById('fpinfo')`)); i++) await sleep(200);
    const inPanel = JSON.parse(await evalJS(ws, `(function(){
      return JSON.stringify({
        info: !!document.getElementById('fpinfo'),
        selall: !!document.getElementById('fpselall'),
        idx: !!document.getElementById('fpindex'),
        map: !!document.getElementById('fpmap'),
        appjs: /app\\.js/.test(Array.prototype.slice.call(document.querySelectorAll('script[src]')).map(function(s){return s.getAttribute('src')}).join(',')),
      });
    })()`));
    ok('⑧ 主面板（index.html#find.notes）里也挂的同一份面板（找得到 fp-info / 全选 / 序号框 / 预览）',
      inPanel.info && inPanel.selall && inPanel.idx && inPanel.map && inPanel.appjs, JSON.stringify(inPanel));

    // ⑨ 缩略图面板本身也不能被这次重构弄坏（`render()` 现在只是 `svgHTML()` 的调用方）
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'index.html#view.minimap' });
    await sleep(1600);
    for (let i = 0; i < 25 && !(await evalJS(ws, `!!document.getElementById('mmscroll')`)); i++) await sleep(200);
    await evalJS(ws, `document.getElementById('mmrefresh').click()`);
    await sleep(600);
    const mm = JSON.parse(await evalJS(ws, `(function(){
      var el = document.getElementById('mmscroll');
      return JSON.stringify({
        svg: !!el.querySelector('svg'),
        clips: el.querySelectorAll('.mmclip').length,
        marks: el.querySelectorAll('.fpmk').length,
        stat: (document.getElementById('mmstat')||{}).textContent || '',
      });
    })()`));
    ok('⑨ 缩略图面板照旧（2 个片段、**没有**查找那些高亮标记）',
      mm.svg === true && mm.clips === 2 && mm.marks === 0 && /2 轨/.test(mm.stat), JSON.stringify(mm));
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
