// 在**真页面里**验「批量替换音素」这个悬停注释：两个输入框能不能填、值有没有传对、干跑勾选有没有生效。
//
// 为什么要这样测：这个按钮的参数不在表单里，而在**悬停注释**里（`#htip`）——
// 这套"注释里放控件"的做法是新加的，光看代码看不出"点得进去、取得到值、传对了"。
// 用 CDP 连进页面：真派发 mouseenter 让注释出现 → 往输入框里打字 → 点按钮 →
// **把 fetch 拦下来看请求体**（不真发，免得改到工程）。
//
// 用法：node devtools/e2e-quicktip.js
const { spawn } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const EDGE = [
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
].find((p) => fs.existsSync(p));
const BASE = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json'), 'utf8')).url;
const PORT = 9700 + (process.pid % 100);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

let bad = 0;
function ok(name, cond, extra) {
  console.log((cond ? '  ok   ' : '  FAIL ') + name + (extra ? '  — ' + extra : ''));
  if (!cond) bad++;
}
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
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-tip-' + PORT),
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
    // 把 fetch 拦下来（只看不发），这样测完工程一个字节都没动
    await evalJS(ws, `(function(){
      window.__req = []; window.__block = true;
      if (!window.__origFetch) { window.__origFetch = window.fetch; }
      window.fetch = function(u, o){
        if (String(u).indexOf('/api/run') >= 0) {
          try { window.__req.push(JSON.parse(o.body)); } catch(e) {}
          if (window.__block) { return Promise.resolve(new Response('{"ok":true,"calls":[{"json":{"scanned":9,"hit":3,"willSet":2,"willFreeze":1,"dryRun":true}}]}', {status:200, headers:{'Content-Type':'application/json'}})); }
        }
        return window.__origFetch.apply(this, arguments);
      };
      return true; })()`);

    const qi = await evalJS(ws, `QUICK.findIndex(function(q){return q.tool === 'phoneme.replace'})`);
    ok('找得到「批量替换音素」按钮', qi >= 0, 'index=' + qi);

    // ⭐ 用户 2026-10-06：「**点击按钮相当于展开菜单**」⇒ 点一下只展开，不发请求
    await evalJS(ws, `(function(){window.__req.length=0;document.querySelectorAll('[data-q]')[${qi}].click();return true;})()`);
    await sleep(400);
    ok('点按钮 = **展开菜单**（不直接跑工具）', await evalJS(ws, `!$('htip').hidden && !!document.getElementById('hf_find')`)
      && (await evalJS(ws, `window.__req.length`)) === 0, '发了 ' + (await evalJS(ws, `window.__req.length`)) + ' 条请求');
    ok('菜单里有「替换」按钮（真正动手的是它）', await evalJS(ws, `!!document.getElementById('hf_run')`));
    ok('菜单被钉住了（鼠标移开也不收）', await evalJS(ws, `(function(){hideTip();return !$('htip').hidden;})()`));
    ok('菜单里**只有**两个输入框（没有干跑勾选、没有范围选择）',
      await evalJS(ws, `document.querySelectorAll('#htip input[type=checkbox],#htip select').length === 0`)
      && await evalJS(ws, `document.querySelectorAll('#htip input[type=text]').length === 2`));
    ok('菜单里有报数的那一行', await evalJS(ws, `!!document.getElementById('hfs')`));
    ok('菜单里的控件点得进去（pointer-events 打开）',
      await evalJS(ws, `getComputedStyle($('htip')).pointerEvents !== 'none'`));

    // 往输入框里打字（真派发 input，验证"记住值"与"输入完自动报数"）
    await evalJS(ws, `(function(){var a=document.getElementById('hf_find'),b=document.getElementById('hf_replace');
      a.value='ing';a.dispatchEvent(new Event('input'));
      b.value='in';b.dispatchEvent(new Event('input'));return true;})()`);
    ok('填的值被记住了（localStorage）',
      await evalJS(ws, `localStorage.getItem('acetool.field.phoneme.replace.find') === 'ing' && localStorage.getItem('acetool.field.phoneme.replace.replace') === 'in'`));

    // ⭐ 用户 2026-10-06（截图）：浏览器在这两个框上弹「保存的信息 / 上次使用」历史下拉 ⇒ 关掉自动填充
    const ac = await evalJS(ws, `document.getElementById('hf_find').getAttribute('autocomplete') + '/' +
      document.getElementById('hf_replace').getAttribute('autocomplete')`);
    ok('查找/替换两个框都关了浏览器自动填充（运行时查 DOM）', ac === 'off/off', ac);
    // 证据图：菜单就长这样。⚠️ 视口要在**开菜单之前**钉死 —— 摆位只在弹出那一刻算，
    // 先开菜单再改视口的话，截到的是「按大窗口算出来的坐标」在窄窗口里跑出屏幕（这坑真踩了）。
    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 900, height: 620, deviceScaleFactor: 2, mobile: false });
    await sleep(250);
    await evalJS(ws, `document.getElementById('hf_find').focus()`);
    await sleep(200);
    const shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(__dirname, 'quicktip-autofill.png'), Buffer.from(shot.data, 'base64'));
    // 拉窄窗口 ⇒ resize 要**重新摆位**（钉住的菜单不能露在外面）
    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 460, height: 620, deviceScaleFactor: 1, mobile: false });
    await sleep(300);
    const box = JSON.parse(await evalJS(ws, `(function(){var r=$('htip').getBoundingClientRect();
      return JSON.stringify({hidden:$('htip').hidden, left:Math.round(r.left), right:Math.round(r.right), vw:window.innerWidth});})()`));
    ok('窗口拉窄后菜单被重新夹进窗口（不会露在外面）',
      box.hidden === false && box.left >= 6 && box.right <= box.vw - 5, JSON.stringify(box));
    await cdp(ws, ++seq, 'Emulation.clearDeviceMetricsOverride', {});

    // ⭐ 查找框输入完 ⇒ 自动报数（有 350ms 防抖，要等）
    await sleep(900);
    const parr = JSON.parse(await evalJS(ws, `JSON.stringify(window.__req.filter(function(x){return x.values && x.values.dryRun === '1'}))`));
    ok('查找框输入完**自动发了一次干跑查询**', parr.length >= 1, parr.length + ' 条');
    ok('查询带上了查找词与替换词', parr.length > 0 && parr[0].values.find === 'ing' && parr[0].values.replace === 'in',
      parr.length ? JSON.stringify(parr[0].values) : '');
    const stat = await evalJS(ws, `$('hfs').textContent`);
    ok('报数那一行显示了「范围 N 个 · 含「ing」M」', /范围 9 个 · 含「ing」3/.test(stat), stat);

    // 点菜单里的「替换」（fetch 被拦下，不会真发）
    await evalJS(ws, `(function(){window.__req.length=0;document.getElementById('hf_run').click();return true;})()`);
    await sleep(400);
    const arr = JSON.parse(await evalJS(ws, `JSON.stringify(window.__req)`));
    ok('点「替换」确实发出了 /api/run', arr.length >= 1, arr.length + ' 条');
    const v = (arr[0] && arr[0].values) || {};
    ok('请求里 toolId = phoneme.replace', (arr[0] || {}).toolId === 'phoneme.replace', (arr[0] || {}).toolId);
    ok('查找/替换**填进菜单的值**被带上了', v.find === 'ing' && v.replace === 'in', JSON.stringify(v));
    ok('范围为「选中的音符（没选中就整个片段）」', v.scope === 'auto', JSON.stringify(v.scope));
    ok('不带 dryRun（= 真写）', v.dryRun === undefined, JSON.stringify(v.dryRun));

    // 替换后 ⇒ 报成功数（拦下来的假回包里 done 有 2 条 + changes 带 2 项，回退要靠它）
    await evalJS(ws, `(function(){
      window.__origFetch = window.__origFetch || window.fetch;
      window.fetch = function(u, o){
        if (String(u).indexOf('/api/run') >= 0) {
          var body = {}; try { body = JSON.parse(o.body); } catch (e) {}
          window.__req.push(body);   // ⚠️ 换成桩之后也得继续记账，不然后面查不到请求
          if (body.toolId === 'phoneme.rewind') {
            return Promise.resolve(new Response(JSON.stringify({ok:true,calls:[{json:{total:2,reset:1,restore:1,done:['phoneme reset --note {n1}','phoneme set --note {n2} --phoneme t ian']}}]}), {status:200, headers:{'Content-Type':'application/json'}}));
          }
          return Promise.resolve(new Response(JSON.stringify({ok:true,calls:[{json:{scanned:9,hit:3,willSet:2,willFreeze:1,done:['a','b'],
            changes:[{noteUuid:'{n1}',before:['m','ing'],after:['m','in'],wasOverride:false},
                     {noteUuid:'{n2}',before:['t','ian'],after:['t','in'],wasOverride:true}],
            note:'已写入。'}}]}), {status:200, headers:{'Content-Type':'application/json'}}));
        }
        return window.__origFetch.apply(this, arguments);
      };
      document.getElementById('hf_run').click();
      return true; })()`);
    await sleep(400);
    ok('替换后报**成功数**（2 个）',
      /替换 2 个/.test(await evalJS(ws, `$('hfs').textContent`)), await evalJS(ws, `$('hfs').textContent`));

    // ⭐ 「回退这一批」（音素专用那版）已经拆掉（用户 2026-10-07）——撤回统一走**顶栏那颗通用键**，
    //   它由 `devtools/e2e-undo-ui.js`（页面接线）与 `e2e-undo-live.js`（真机往返）负责。
    ok('菜单里没有音素专用的回退键了（只剩「替换」）',
      (await evalJS(ws, `!document.getElementById('hf_undo')`)) === true &&
      (await evalJS(ws, `document.querySelectorAll('#htip .row.act button').length`)) === 1);

    // 查找留空 ⇒ 本地就挡下来（不发请求）
    await evalJS(ws, `(function(){document.getElementById('hf_find').value='';window.__req.length=0;document.getElementById('hf_run').click();return true;})()`);
    await sleep(300);
    ok('查找留空 ⇒ 不发请求，并提示先填', (await evalJS(ws, `window.__req.length`)) === 0
      && /填「查找」/.test(await evalJS(ws, `$('htoast').textContent`)),
      await evalJS(ws, `$('htoast').textContent`));
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
