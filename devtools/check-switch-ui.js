// 「web ⇄ 原生侧栏」那颗按钮的**真点击**自检（无头 Edge + CDP 去点 home 顶栏的 `#btnRail`）。
//
// 为什么要有它：`check-rail-switch.js` 验的是**接口**（直接 POST `/api/rail`），
// 验不到"页面里那颗按钮到底接上没有、点了有没有反应、按钮字有没有跟着变"。
// 用户 2026-10-16：「把按钮功能实装进去」——那就得**真点一下**再回读状态。
//
// ⚠️ 前置：**ACE 最好开着**（建细条 + ACE 不可用会让 exe 自己退出，见 §72）。脚本跑完会把侧栏停回去。
//
// 用法：node devtools/check-switch-ui.js [url]
const { spawn } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const EDGE = [
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
].find((p) => fs.existsSync(p));
const PANEL = path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json');
const BASE = (process.argv[2] || JSON.parse(fs.readFileSync(PANEL, 'utf8')).url).replace(/\/$/, '');
const PORT = 9700 + (process.pid % 60);
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
const rail = async () => (await fetch(BASE + '/api/rail', { cache: 'no-store' })).json();

(async () => {
  console.log('面板：' + BASE);
  const st0 = await rail();
  console.log('  侧栏现状：running=' + st0.running);

  const child = spawn(EDGE, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-swui-' + PORT), 'about:blank',
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
  await cdp(ws, ++seq, 'Page.enable', {});
  await cdp(ws, ++seq, 'Runtime.enable', {});
  await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 1100, height: 800, deviceScaleFactor: 1, mobile: false });
  await cdp(ws, ++seq, 'Page.navigate', { url: BASE + '/home.html' });
  await sleep(1200);

  const probe = `(function(){
    var b = document.getElementById('btnRail');
    return JSON.stringify({ has: !!b, text: b ? b.textContent : '', title: b ? b.title : '',
      disabled: b ? b.disabled : null, onclick: !!(b && b.onclick !== undefined) });
  })()`;
  const before = JSON.parse(await evalJS(ws, probe));
  ok('① home 顶栏有那颗「侧」按钮', before.has, JSON.stringify(before));
  ok('① 它按侧栏状态显示标签（没跑 ⇒ 「侧」）', before.text === '侧' || before.text === 'web', '标签=' + before.text);

  // 真点一下（页面自己的 click 事件，走它接的那条路）
  await evalJS(ws, `document.getElementById('btnRail').click()`);
  await sleep(1600);
  const st1 = await rail();
  const after = JSON.parse(await evalJS(ws, probe));
  const wantOn = !st0.running;
  ok('② **真点一下**之后侧栏状态翻转了', st1.running === wantOn,
    'running ' + st0.running + ' → ' + st1.running);
  ok('② 按钮标签跟着变了', after.text === (st1.running ? 'web' : '侧'), '标签=' + after.text + ' · ' + after.title);
  if (st1.running) {
    ok('② 细条真贴到了 ACE 窗口', !!st1.targetTitle, 'target=' + (st1.targetTitle || '(没贴到)'));

    // 再点一下切回去（跑完不留侧栏，免得 ACE 一关就把 exe 带走）
    await evalJS(ws, `document.getElementById('btnRail').click()`);
    await sleep(1200);
    const st2 = await rail();
    ok('③ 再点一下切回 web（侧栏停掉）', st2.running === false, 'running=' + st2.running);
    const after2 = JSON.parse(await evalJS(ws, probe));
    ok('③ 标签回到「侧」', after2.text === '侧', '标签=' + after2.text);
  } else {
    console.log('  （原状态是开着的 ⇒ 这一轮验的是"点一下关掉"，上面两条已覆盖）');
  }

  // ④ 开那个没有浏览器工具栏的独立窗口。
  // ⚠️ 2026-10-17 用户「btnAppWin可以删了」⇒ home 顶栏那颗「窗」没了，这一步改成**直接打接口**
  //    （测的还是同一件事：exe 真的按 app 模式开了一个窗口）。
  const opened = JSON.parse(await evalJS(ws, `(async function(){
    var r = await fetch('/api/app-window', { method:'POST', headers:{'Content-Type':'application/json','X-ACETool':'1'},
      body: JSON.stringify({ page: 'home.html' }) });
    var d = await r.json();
    return JSON.stringify({ ok: d.ok, appMode: d.appMode, sized: d.sized, w: d.w, h: d.h, hint: d.hint, error: d.error });
  })()`));
  ok('④ 调 /api/app-window ⇒ exe 真按 app 模式开了一个窗口', opened.ok === true && opened.appMode === true,
    JSON.stringify(opened));
  console.log('  节流等 3 秒让 exe 记住这个窗口（它按"新出现的窗口"认，最多轮询 4 秒）…');
  await sleep(3000);

  // ⑤ 这时点「侧」：应当**顺手把刚开那个 web 窗口关掉**（用户 2026-10-16：「切的时候关掉」）
  await evalJS(ws, `document.getElementById('btnRail').click()`);
  await sleep(1800);
  const toast2 = await evalJS(ws, `document.getElementById('htoast').textContent`);
  const st3 = await rail();
  ok('⑤ 点「侧」后侧栏在跑', st3.running === true, 'running=' + st3.running);
  if (/web 窗口已关/.test(toast2)) {
    ok('⑤ 顺手关掉了那个 web 窗口（toast 明说）', true, toast2);
  } else {
    // 没记住窗口时是**如实**不关（不是假装成功）⇒ 这里只提醒，不当失败
    console.log('  （这次没关 web 窗口 ⇒ 多半是还没认出来；toast=' + (toast2 || '(空)') + '）');
  }

  // ⑥ 收尾：再点一下把侧栏停掉（别留着 —— ACE 一关它会把 exe 带走）
  await evalJS(ws, `document.getElementById('btnRail').click()`);
  await sleep(1200);
  const st4 = await rail();
  ok('⑥ 收尾：再点一下侧栏停掉', st4.running === false, 'running=' + st4.running);

  child.kill();
  console.log('\n' + (bad ? bad + ' 项失败' : '全部通过'));
  process.exitCode = bad ? 1 : 0;
})().catch((e) => { console.error('自检脚本自己炸了：' + e.stack); process.exitCode = 2; });
