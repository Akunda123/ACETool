// 证据图 + 运行自检：音高线流程页（`pitch.html`）在**没有 ACE** 时也能起来 ——
// 桩掉 `xcopy.view` / `project.*`，看：① 页面不抛错 ② 缩略图画出片段/音符 + 两个框
// ③ 点「保存工程」能拿到路径、② 就能点 ④ 点「执行（干跑）」出计划 ⑤ 「恢复备份」可达。
// 用法：node devtools/shot-pitch.js [url]
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
const PORT = 9820 + (process.pid % 60);
const TMPPROJ = path.join(os.tmpdir(), 'ACETool', 'shot-pitch', '歌', '歌.acep');
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

const VIEW = {
  trackIndex: 0, trackName: '轨 0', clipIndex: 0, clipUuid: '{c1}', clipName: '片段 A',
  clipBegin: 0, clipEnd: 3840, durationTick: 7680, ticksPerBar: 1920, noteCount: 3,
  clips: [
    { uuid: '{c1}', index: 0, name: '片段 A', begin: 0, end: 3840, noteCount: 3, minPitch: 58, maxPitch: 64,
      notes: [{ pos: 0, dur: 480, pitch: 60 }, { pos: 960, dur: 480, pitch: 62 }, { pos: 2880, dur: 480, pitch: 64 }] },
    { uuid: '{c2}', index: 1, name: '片段 B', begin: 3840, end: 7680, noteCount: 2, minPitch: 55, maxPitch: 60,
      notes: [{ pos: 0, dur: 960, pitch: 55 }, { pos: 1920, dur: 960, pitch: 60 }] },
  ],
};

(async () => {
  const child = spawn(EDGE, ['--headless=new', '--disable-gpu', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-shotpitch-' + PORT), 'about:blank'], { stdio: 'ignore' });
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
    window.__req = []; window.__err = [];
    window.addEventListener('error', function (e) { window.__err.push(String(e.message)); });
    var orig = window.fetch;
    var view = ${JSON.stringify(VIEW)};
    window.fetch = function (u, o) {
      var s = String(u);
      if (s.indexOf('/api/run') >= 0) {
        var body = JSON.parse(o.body); window.__req.push(body);
        var j = {};
        if (body.toolId === 'xcopy.view') j = view;
        else if (body.toolId === 'project.save') j = { savedPath: ${JSON.stringify(TMPPROJ)} };
        else if (body.toolId === 'project.path') j = { '工程文件路径': ${JSON.stringify(TMPPROJ)}, '未保存改动': '没有（已落盘）', '说明': '测试桩' };
        else if (body.toolId === 'pitch.within') j = {
          '片段': '轨 0 片段 0「片段 A」', '轨道': '轨 0',
          '源': '第 1–1 小节（工程 tick 0..1920）', '目标': '从第 3 小节起（工程 tick 3840..5760）',
          '曲线': { '结果': '源 320 个样本：写进已有覆盖 0 处；新建 1 条 entry（320 个样本）' },
          '锚点': { '结果': '源区间里没有锚点' },
          '音符颤音': { '结果': '源 1 个带颤音的音符 → 写到 1 个目标音符' },
          '备份': 'C:\\\\Temp\\\\ACETool\\\\acep-backup\\\\歌-20261008-120000.acep',
          '自检': ['payload 重解通过（1000 → 1030 字节）'], '提示': '干跑：一个字节都没写。' };
        else if (body.toolId === 'project.restore') j = { '用哪份备份': 'x.acep', '备份时间': '2026-10-08 12:00:00' };
        else if (body.toolId === 'project.open') j = {};
        return Promise.resolve(new Response(JSON.stringify({ ok: true, calls: [{ json: j, exitCode: 0, stdout: '' }] }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      if (s.indexOf('/api/selfcheck') >= 0) {
        return Promise.resolve(new Response(JSON.stringify({ online: true, version: { forAce: 'cli 0.17.0 · ACE Studio 2.1.8' }, project: { projectName: '测试工程' } }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      return orig.apply(window, arguments);
    };
  })()`;

  try {
    await cdp(ws, ++seq, 'Page.enable', {});
    await cdp(ws, ++seq, 'Page.addScriptToEvaluateOnNewDocument', { source: stub });
    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 760, height: 900, deviceScaleFactor: 2, mobile: false });
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'pitch.html' });
    await sleep(2000);
    console.log('页面错误：' + JSON.stringify(await evalJS(ws, `window.__err`)));
    console.log('范围读数：' + await evalJS(ws, `document.getElementById('rangeInfo').textContent`));
    console.log('缩略图：' + await evalJS(ws, `(function(){
      var c = document.getElementById('mm');
      var g = c.getContext('2d');
      var d = g.getImageData(0, 0, c.width, c.height).data;
      var ink = 0;
      for (var i = 3; i < d.length; i += 4) { if (d[i] > 0) ink++; }
      return '画布 ' + c.width + '×' + c.height + ' · 非透明像素 ' + ink;
    })()`));

    await evalJS(ws, `document.getElementById('p1').click()`);
    await sleep(500);
    console.log('① 保存工程：' + await evalJS(ws, `document.getElementById('p1stat').textContent + ' · ' + document.getElementById('ppath').textContent`));
    console.log('② 能不能点：' + await evalJS(ws, `document.getElementById('p2').disabled ? '还禁用着' : '可点'`));

    await evalJS(ws, `document.getElementById('p2').click()`);
    await sleep(600);
    console.log('② 干跑结果：' + (await evalJS(ws, `document.getElementById('out').textContent`)).split('\n').slice(0, 6).join(' / '));
    console.log('③ 恢复备份：' + await evalJS(ws, `document.getElementById('undo').disabled ? '还禁用着' : '可点（' + document.getElementById('ustat').textContent + '）'`));
    console.log('发出去的请求：' + await evalJS(ws, `window.__req.map(function(r){return r.toolId}).join(',')`));

    const shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
    fs.writeFileSync(path.join(__dirname, 'pitch-page.png'), Buffer.from(shot.data, 'base64'));
    console.log('落盘：devtools/pitch-page.png');
  } catch (e) {
    console.error('崩了：' + e.message);
    process.exitCode = 1;
  }
  ws.close();
  child.kill();
})();
