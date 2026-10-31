// 证据 + 自检：**每个面板页面都有一颗 📌，而且长得一模一样**（用户 2026-10-16：
// 「那就所有面板包括 home 和所有打开的都加 📌」；配色一致性是更早那条
// 「有的有上下边框颜色有的没有不协调」的延续）。
//
// 为什么用真浏览器跑：`check-ui.js` 只能证明"文件里有那一行"，证明不了
//  ① 按钮**真的**被挂上去了（挂在哪个容器、字号/边框/圆角是不是一致）；
//  ② 页面里没有 JS 报错（`ontop.js` 在 14 个页面上都得能跑）。
//
// 只读：只看按钮、只点一次（无头窗口不是"可见的顶层窗口" ⇒ 服务端会**如实报找不到**，
// 这正是我们要断言的：**不假装成功**）。
//
// 用法：node devtools/check-ontop.js [url]
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
const PORT = 9600 + (process.pid % 60);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/* 页面 → 期望挂在哪个容器（设置页没有顶栏，而且它的 `.bar` 是卡片里那排按钮
   ⇒ 走右上角固定那颗，挂在 `<body>` 上） */
const PAGES = [
  ['home.html', 'DIV.bar'], ['find.html', 'DIV.bar'], ['lrc.html', 'DIV.bar'],
  ['lyric.html', 'DIV.bar'], ['notation.html', 'DIV.bar'], ['param.html', 'DIV.bar'],
  ['pitch.html', 'DIV.bar'], ['pitchx.html', 'DIV.bar'], ['rgb.html', 'DIV.bar'],
  ['settings.html', 'BODY'], ['tone.html', 'DIV.bar'], ['vib.html', 'DIV.bar'],
  ['within.html', 'DIV.bar'], ['xcopy.html', 'DIV.bar'],
];

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
  console.log('面板：' + BASE + '  调试端口 ' + PORT);
  const child = spawn(EDGE, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-ontop-' + PORT), 'about:blank',
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
  console.log('Edge 已就绪');
  const ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((res, rej) => { ws.onopen = res; ws.onerror = () => rej(new Error('WS 连不上')); });
  await cdp(ws, ++seq, 'Page.enable', {});
  await cdp(ws, ++seq, 'Runtime.enable', {});
  await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 1100, height: 720, deviceScaleFactor: 2, mobile: false });

  const probe = `(function(){
    var b = document.getElementById('btnOntop');
    if (!b) return JSON.stringify({ ok: false });
    var cs = getComputedStyle(b), host = b.parentElement;
    return JSON.stringify({
      ok: true, api: !!window.OntopBtn,
      host: host.tagName + (host.className ? '.' + host.className : ''),
      glyph: (b.textContent || '').trim(), on: b.classList.contains('on'),
      title: b.title.split('\\n')[0],
      /* 一致性的判据：这些**计算后**的值必须 14 个页面全一样。
         ⚠️ 底色/边色/字色要**分开量** —— 它们随 "已置顶"（.on）变蓝，那是**状态**不是样式差异。 */
      sig: [cs.width, cs.height, cs.borderTopWidth, cs.borderRadius,
            cs.fontSize, cs.cursor, cs.display].join('|'),
      sigOn: [cs.backgroundColor, cs.borderTopColor, cs.color].join('|'),
    });
  })()`;

  const sigs = new Set();
  const onSigs = new Set();
  const results = [];
  for (const [page, wantHost] of PAGES) {
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + page });
    await sleep(120);
    for (let i = 0; i < 25; i++) {
      const has = await evalJS(ws, `!!document.getElementById('btnOntop')`);
      if (has) break;
      await sleep(120);
    }
    const d = JSON.parse(await evalJS(ws, probe));
    results.push([page, d, wantHost]);
    if (d.ok) { sigs.add(d.sig); if (d.on) onSigs.add(d.sigOn); }
    console.log('  看过 ' + page + ' → ' + (d.ok ? (d.host + ' / ' + (d.on ? '已置顶' : '未置顶')) : '没有 📌'));
  }

  const missing = results.filter(([, d]) => !d.ok).map(([p]) => p);
  ok('14 个页面**都**有 📌（含 home 与设置页）', missing.length === 0, missing.join(', ') || PAGES.length + ' 个都在');
  ok('📌 真的跑起来了（`window.OntopBtn` 在）', results.every(([, d]) => d.ok && d.api));
  const wrongHost = results.filter(([, d, w]) => d.ok && d.host !== w).map(([p, d, w]) => p + '→' + d.host + '(期望 ' + w + ')');
  ok('挂载位置对（工具页/home 在顶栏，设置页在右上角固定那颗）', wrongHost.length === 0, wrongHost.join(', ') || '都到位');
  ok('字形都是 📌', results.every(([, d]) => d.glyph === '📌'), [...new Set(results.map(([, d]) => d.glyph))].join(' / '));
  ok('14 个页面的📌**长得逐项一致**（宽高/边框/圆角/字号/光标/display 全同）',
    sigs.size === 1, sigs.size === 1 ? '' : '出现了 ' + sigs.size + ' 种样式：' + [...sigs].join('  ||  '));
  ok('已置顶态也是同一套配色（蓝底白字，不分页面）',
    onSigs.size <= 1, [...onSigs].join(' || ') || '（当前没有页面处于已置顶态）');
  ok('悬停提示说的是"把这个窗口浮在 ACE 上面"',
    results.every(([, d]) => /置顶/.test(d.title || '')), results[0][1].title || '');

  // 点一下 —— 分两条：
  //  A) **确定性**的"找不到就如实说"：先把页面标题改成一个**不可能存在**的标题再点
  //     （页面把自己的 `document.title` 上报给服务端 ⇒ 换个独一份的标题就一定找不到）。
  //  B) **真窗口往返**（桌面上正好有同标题的真浏览器窗口时才做得到）：
  //     点开 → 读回 `WS_EX_TOPMOST` → 再点回去（**不留着人家的窗口钉在最上面**）。
  //     桌面上没有那个窗口就**如实跳过**，不算失败（这个自检不该依赖桌面上开着什么）。
  await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'home.html' });
  await sleep(400);
  const uniq = 'ACETool 自检-' + PORT;
  const a = JSON.parse(await evalJS(ws, `(async function(){
    document.title = ${JSON.stringify(uniq)};
    document.getElementById('btnOntop').click();
    await new Promise(function(r){ setTimeout(r, 700); });
    var s = window.OntopBtn.state();
    return JSON.stringify({ on: s.on, err: s.lastErr, title: s.title });
  })()`));
  ok('点一下：那个窗口不存在时**如实说**（不假装成功、按钮不亮）',
    a.on === false && /没找到/.test(a.err || ''), a.err || '(没有错误信息 —— 像是假装成功了)');
  ok('回包里的信息进了悬停提示（凭什么认的 / 认到几个窗口）',
    /窗口：|置顶问不出来/.test(a.title || ''), (a.title || '').split('\n').slice(-1)[0]);

  const b = JSON.parse(await evalJS(ws, `(async function(){
    document.title = 'ACE 工具箱'; // ⚠️ 主页面标题（2026-10-17 用户改的），别用旧的 ACETool
    var H = { 'Content-Type': 'application/json', 'X-ACETool': '1' };
    var out = { steps: [] };
    /* ⚠️ 先把状态**归零**（置顶=false）再点：这条自检要断言"点一下 → 亮了、再点一下 → 灭了"，
       而"亮的起点"是**上一次跑留下的状态**（上一回如果没跑完、或桌面上的窗口本来就被别的
       页面的 📌 顶过），从亮开始 ⇒ 第一次点就是"取消" ⇒ 断言假失败（2026-10-17 真撞上过）。 */
    try {
      var r0 = await fetch('/api/panel/ontop', { method: 'POST', headers: H,
        body: JSON.stringify({ on: false, title: 'ACETool' }) });
      out.reset = await r0.json();
    } catch (e) { out.reset = { error: String(e) }; }
    window.OntopBtn.refresh();
    await new Promise(function(r){ setTimeout(r, 300); });
    for (var i = 0; i < 2; i++) {
      document.getElementById('btnOntop').click();
      await new Promise(function(r){ setTimeout(r, 800); });
      out.steps.push(window.OntopBtn.state());
    }
    /* 收尾：无论断言过没过，都**别把窗口留着钉在最上面** */
    try {
      await fetch('/api/panel/ontop', { method: 'POST', headers: H,
        body: JSON.stringify({ on: false, title: 'ACETool' }) });
    } catch (e) { /* 收尾失败不改变断言结论 */ }
    return JSON.stringify(out);
  })()`));
  const [s1, s2] = b.steps || [];
  // ⚠️ 判"跳过"看的是**这一步的悬停提示**（`title` 里写了"置顶问不出来"），
  //    别看 `lastErr` —— 那是模块级的"最后一条错误"，上一次的失败会留在里面（踩过）。
  if (/没找到|置顶问不出来/.test(s1.title || '')) {
    console.log('  skip 真窗口往返 —— 桌面上现在没有标题为 `ACETool` 的浏览器窗口（如实跳过）');
  } else {
    const rst = b.reset || {};
    ok('真窗口往返：点一下**读回来**确实置顶了', s1.on === true,
      (s1.title || '').split('\n')[1] ||
      ('（归零那一手：onTop=' + rst.onTop + ' matches=' + rst.windowMatches +
       ' picked=' + rst.picked + ' err=' + (rst.error || '无') + '）'));
    ok('再点一下取消（**不把用户的窗口留着钉在最上面**）', s2 && s2.on === false && !s2.lastErr,
      (s2 && (s2.lastErr || (s2.title || '').split('\n')[1])) || '');
  }

  for (const [page, name] of [['home.html', 'ontop-home.png'], ['settings.html', 'ontop-settings.png']]) {
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + page });
    await sleep(700);
    const shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(__dirname, name), Buffer.from(shot.data, 'base64'));
    console.log('  落盘：devtools/' + name);
  }

  child.kill();
  console.log('\n' + (bad ? bad + ' 项失败' : '全部通过'));
  process.exitCode = bad ? 1 : 0;
})().catch((e) => { console.error('自检脚本自己炸了：' + e.stack); process.exitCode = 2; });
