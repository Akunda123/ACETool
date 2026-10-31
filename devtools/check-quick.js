// 自检：**主页面那 20 个键位的"点按钮"链路**（用户 2026-10-17）。
//
// 为什么单独一份：`check-settings` 只能证明设置页**画出了 20 个位置**；
// 「按了键真的等于点那颗按钮」这条得在**主页面**里验 —— 而按真组合键会真跑工具
// （有一半是写操作，绝不能在用户的工程上试）⇒ 这里走页面暴露的 `HomeQuick.run(tool)`，
// 它和"exe 发事件 → 页面点按钮"用的是**同一个函数**。
//
// 判据：
//   ① 主页面按钮上有 `data-tool`，而且 19 个都在、没有重复；
//   ② `HomeQuick.run('<工具 id>')` 返回"已触发…"（说明真的点到了那颗按钮）；
//   ③ 不认识的工具 id **如实报错**，不静默；
//   ④ `quick.open` 这类不是按钮的动作不会被误当成按钮。
//
// ⚠️ 用 `find.notes`（开「查找」子窗口，只读）做实测 —— 它会**真的开一个窗口**，
//    跑完请把它关掉（`devtools/winclose -title 查找`）。别的按钮一半是写操作，不能碰。
//
// 用法：node devtools/check-quick.js [url]
const { spawn } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const EDGE = [
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
].find((p) => fs.existsSync(p));
const PANEL = path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json');
const BASE = (process.argv[2] || JSON.parse(fs.readFileSync(PANEL, 'utf8')).url).replace(/\/$/, '') + '/';
const PORT = 9500 + (process.pid % 60);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

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

let bad = 0;
const ok = (name, cond, extra) => {
  console.log((cond ? '  ok   ' : '  FAIL ') + name + (extra ? '  — ' + extra : ''));
  if (!cond) bad++;
};

(async () => {
  if (!EDGE) { console.error('找不到 Edge'); process.exit(1); }
  console.log('面板：' + BASE + '  调试端口 ' + PORT);
  const child = spawn(EDGE, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-quick-' + PORT), 'about:blank',
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
  await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 900, height: 900, deviceScaleFactor: 2, mobile: false });
  await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'home.html' });
  await sleep(1200);
  for (let i = 0; i < 20 && !(await evalJS(ws, '!!window.HomeQuick')); i++) await sleep(200);

  const tools = JSON.parse(await evalJS(ws, `JSON.stringify([].slice.call(document.querySelectorAll('#hgrid [data-tool]'))
    .map(function(b){ return b.getAttribute('data-tool'); }))`));
  ok('主页面 19 颗按钮都带 data-tool', tools.length === 19, '实际 ' + tools.length + ' 个');
  ok('data-tool 没有重复', new Set(tools).size === tools.length, tools.length + ' 个');
  ok('里面确实有 find.notes（要拿它实测）', tools.indexOf('find.notes') >= 0);

  const run = JSON.parse(await evalJS(ws, `JSON.stringify([
    window.HomeQuick.run('find.notes'),
    window.HomeQuick.run('nope.nope'),
    window.HomeQuick.run('open')
  ])`));
  ok('run("find.notes") 真的点到了那颗按钮', run[0].indexOf('已触发') === 0, run[0]);
  ok('不认识的工具 id **如实报错**（不静默）', run[1].indexOf('不认识') === 0, run[1]);
  ok('`open` 不会被当成按钮', run[2].indexOf('不认识') === 0, run[2]);

  // 说明：find.notes 会真开一个「查找」窗口（只读页），跑完关掉
  await sleep(1200);
  ws.close();
  child.kill();
  console.log('\n' + (bad ? bad + ' 项失败' : '全部通过') + '（「查找」窗口如果开出来了，记得关：winclose -title 查找）');
  process.exitCode = bad ? 1 : 0;
})().catch((e) => { console.error('自检脚本自己炸了：' + e.stack); process.exitCode = 2; });
