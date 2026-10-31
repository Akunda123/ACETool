// 证据图 + 运行自检：语调教页（`tone.html`）—— 桩掉 /api/run，不需要 ACE。
// 用法：node devtools/shot-tone.js [url]
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
const PORT = 9950 + (process.pid % 40);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

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
  if (r.exceptionDetails) throw new Error('页面抛错：' + (r.exceptionDetails.exception && r.exceptionDetails.exception.description));
  return r.result.value;
}

// 4 个音符：三个汉字 + 一个延音 `-`（`-` 不在 lyric.tones 的结果里 ⇒ 应当默认「不动」）
const NOTES = [
  { '音符': '{a1}', '歌词': '啦', '音高': 62, '片段内位置（tick）': 240, '时长（tick）': 660 },
  { '音符': '{b2}', '歌词': '明', '音高': 64, '片段内位置（tick）': 900, '时长（tick）': 1020 },
  { '音符': '{c3}', '歌词': '天', '音高': 65, '片段内位置（tick）': 1920, '时长（tick）': 840 },
  { '音符': '{d4}', '歌词': '-', '音高': 66, '片段内位置（tick）': 2760, '时长（tick）': 1020 },
];
const CHARS = [
  { char: '啦', pinyin: 'lā', tone: 1, toneName: '阴平', contour: '—', noteUuid: '{a1}', pitch: 62, pos: 240 },
  { char: '明', pinyin: 'míng', tone: 2, toneName: '阳平', contour: '↗', noteUuid: '{b2}', pitch: 64, pos: 900 },
  { char: '天', pinyin: 'tiān', tone: 1, toneName: '阴平', contour: '—', noteUuid: '{c3}', pitch: 65, pos: 1920 },
];

(async () => {
  const child = spawn(EDGE, ['--headless=new', '--disable-gpu', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-shottone-' + PORT), 'about:blank'], { stdio: 'ignore' });
  let target = null;
  for (let i = 0; i < 40 && !target; i++) {
    await sleep(250);
    try {
      const l = await (await fetch('http://127.0.0.1:' + PORT + '/json/list')).json();
      target = l.find((t) => t.type === 'page' && t.url.indexOf('devtools') < 0);
    } catch (e) { /* 还没起来 */ }
  }
  if (!target) { child.kill(); console.error('连不上 Edge'); process.exit(1); }
  const ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((res, rej) => { ws.onopen = res; ws.onerror = () => rej(new Error('WS 连不上')); });

  const stub = `(function(){
    window.__err = []; window.__req = [];
    window.addEventListener('error', function (e) { window.__err.push(String(e.message)); });
    var orig = window.fetch;
    var notes = ${JSON.stringify(NOTES)}, chars = ${JSON.stringify(CHARS)};
    window.fetch = function (u, opt) {
      var s = String(u);
      if (s.indexOf('/api/run') >= 0) {
        var b = JSON.parse(opt.body); window.__req.push(b.toolId);
        var j = {};
        if (b.toolId === 'project.save') j = { savedPath: 'C:\\\\Temp\\\\歌\\\\歌.acep' };
        else if (b.toolId === 'project.path') j = { '工程文件路径': 'C:\\\\Temp\\\\歌\\\\歌.acep', '未保存改动': '没有（已落盘）', '工程名': '歌' };
        else if (b.toolId === 'vibrato.read') j = {
          '轨道': '轨 0', '片段': '片段1', '范围': '选中的 4 个音符（当前片段「片段1」共 4 个）',
          '选中数': 4, '音符信息': notes };
        else if (b.toolId === 'lyric.tones') j = {
          source: (b.values || {}).source, count: chars.length, summary: '啦1 明2 天1', chars: chars };
        else if (b.toolId === 'tone.apply') j = {
          '片段': '片段「片段1」', '源': '范围：4 个音符', '目标': '写了 3 个，跳过 1 个',
          '锚点': { '结果': '片段「片段1」：去掉区间内旧锚点 4 个、加 3 条 anchor entry（11 个点）' },
          '曲线': { '结果': '片段「片段1」：删掉曲线样本 2688 个、拆成 2 条 entry' },
          '音符颤音': { '结果': '没动（用户 2026-10-10 口径：颤音保持原样）' },
          '备份': 'C:\\\\Temp\\\\ACETool\\\\acep-backup\\\\歌-20261010-2.acep',
          '自检': ['payload 重解通过（9000 → 9312 字节）', '回读：payload 逐字节一致', '形状自检通过：3 个音符的区间都干净、锚点都在'],
          '逐音符': ['「片段1」1 啦 · 音高 62 · tick 240–900', '「片段1」2 明 · 音高 64 · tick 900–1920', '「片段1」3 天 · 音高 65 · tick 1920–2760'] };
        else if (b.toolId === 'project.restore') j = { '用哪份备份': '歌-20261010-2.acep', '备份时间': '2026-10-10 23:59' };
        else j = {};
        return Promise.resolve(new Response(JSON.stringify({ ok: true, calls: [{ json: j, exitCode: 0, stdout: '' }] }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      if (s.indexOf('/api/selfcheck') >= 0) {
        return Promise.resolve(new Response(JSON.stringify({ online: true, version: { forAce: 'cli 0.17.0 · ACE Studio 2.1.8' } }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      return orig.apply(window, arguments);
    };
  })()`;

  try {
    await cdp(ws, ++seq, 'Page.enable', {});
    await cdp(ws, ++seq, 'Page.addScriptToEvaluateOnNewDocument', { source: stub });
    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 720, height: 700, deviceScaleFactor: 2, mobile: false });
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'tone.html' });
    await sleep(1800);
    console.log('页面错误：' + JSON.stringify(await evalJS(ws, `window.__err`)));
    console.log('范围：' + await evalJS(ws, `document.getElementById('scope').textContent`));
    console.log('卡片：' + await evalJS(ws, `Array.prototype.map.call(document.querySelectorAll('.chip'), function (c) {
      return c.querySelector('.zi').textContent + c.querySelector('.to').textContent;
    }).join(' ')`));
    console.log('提示：' + await evalJS(ws, `document.getElementById('hint').textContent`));

    // 用**真鼠标事件**走一遍（用户 2026-10-10 的 bug：hover 出面板后往选项上挪就没法点了）
    const rectOf = (sel) => evalJS(ws, `(function(){var e=document.querySelector(${JSON.stringify(sel)});
      if(!e) return null; var r=e.getBoundingClientRect(); return { x: r.left + r.width/2, y: r.top + r.height/2 };})()`);
    const chipPt = await rectOf('.chip[data-i="1"]');
    await cdp(ws, ++seq, 'Input.dispatchMouseEvent', { type: 'mouseMoved', x: chipPt.x, y: chipPt.y, buttons: 0 });
    await sleep(200);
    console.log('hover 面板：' + await evalJS(ws, `document.getElementById('pick').style.display + ' · ' +
      Array.prototype.map.call(document.querySelectorAll('#pick button'), function (b) { return b.textContent; }).join('')`));
    const btnPt = await rectOf('#pick button[data-t="3"]');
    // 中间先经过"字与面板之间"那一点（真实手会划过这里）+ 停 120ms（闸门就是这段延迟）
    await cdp(ws, ++seq, 'Input.dispatchMouseEvent',
      { type: 'mouseMoved', x: chipPt.x, y: (chipPt.y + btnPt.y) / 2, buttons: 0 });
    await sleep(120);
    await cdp(ws, ++seq, 'Input.dispatchMouseEvent', { type: 'mouseMoved', x: btnPt.x, y: btnPt.y, buttons: 0 });
    await sleep(120);
    console.log('挪到选项上还在吗：' + await evalJS(ws, `document.getElementById('pick').style.display`));
    const hoverShot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
    fs.writeFileSync(path.join(__dirname, 'tone-hover.png'), Buffer.from(hoverShot.data, 'base64'));
    console.log('落盘：devtools/tone-hover.png（鼠标已经挪到选项上，面板还在）');
    await cdp(ws, ++seq, 'Input.dispatchMouseEvent',
      { type: 'mousePressed', x: btnPt.x, y: btnPt.y, button: 'left', clickCount: 1, buttons: 1 });
    await cdp(ws, ++seq, 'Input.dispatchMouseEvent',
      { type: 'mouseReleased', x: btnPt.x, y: btnPt.y, button: 'left', clickCount: 1, buttons: 0 });
    await sleep(200);
    console.log('改完第 2 张：' + await evalJS(ws, `document.querySelector('.chip[data-i="1"]').className + ' · ' +
      document.querySelector('.chip[data-i="1"]').querySelector('.to').textContent`));

    await evalJS(ws, `document.getElementById('p1').click()`);
    await sleep(400);
    console.log('① 状态：' + await evalJS(ws, `document.getElementById('p1stat').textContent`));
    await evalJS(ws, `document.getElementById('p2').click()`);
    await sleep(600);
    console.log('② 状态：' + await evalJS(ws, `document.getElementById('p2stat').textContent`) +
      ' · ↺ 可点：' + await evalJS(ws, `!document.getElementById('undo').disabled`));
    console.log('③ 输出：' + (await evalJS(ws, `document.getElementById('out').textContent`)).split('\n').join(' / '));
    console.log('请求序列：' + await evalJS(ws, `window.__req.join(' → ')`));
    const shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
    fs.writeFileSync(path.join(__dirname, 'tone-page.png'), Buffer.from(shot.data, 'base64'));
    console.log('落盘：devtools/tone-page.png');
  } catch (e) {
    console.error('崩了：' + e.message);
    process.exitCode = 1;
  }
  ws.close();
  child.kill();
})();
