// 证据图 + 运行自检：跨轨道复制音高线页（`pitchx.html`）—— 桩掉 /api/run，不需要 ACE。
// 用法：node devtools/shot-pitchx.js [url]
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
const PORT = 9860 + (process.pid % 60);
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

const MM = {
  meter: '4/4', tempo: 120, durationTick: 7680, ticksPerBar: 1920,
  tracks: [
    { index: 0, name: '轨 0', type: 'sing', clips: [
      { index: 0, name: 'A', begin: 0, end: 3840, noteCount: 3, minPitch: 58, maxPitch: 64,
        notes: [{ pos: 0, dur: 480, pitch: 60 }, { pos: 960, dur: 480, pitch: 62 }, { pos: 2880, dur: 480, pitch: 64 }] },
    ] },
    { index: 1, name: '轨 1', type: 'sing', clips: [
      { index: 0, name: 'B', begin: 1920, end: 5760, noteCount: 2, minPitch: 55, maxPitch: 61,
        notes: [{ pos: 0, dur: 960, pitch: 55 }, { pos: 1920, dur: 960, pitch: 61 }] },
    ] },
  ],
};

(async () => {
  const child = spawn(EDGE, ['--headless=new', '--disable-gpu', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-shotpitchx-' + PORT), 'about:blank'], { stdio: 'ignore' });
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
    var mm = ${JSON.stringify(MM)};
    window.fetch = function (u, opt) {
      var s = String(u);
      if (s.indexOf('/api/run') >= 0) {
        var b = JSON.parse(opt.body); window.__req.push(b.toolId);
        var j = {};
        if (b.toolId === 'view.minimap') j = mm;
        else if (b.toolId === 'project.save') j = { savedPath: 'C:\\\\Temp\\\\歌\\\\歌.acep' };
        else if (b.toolId === 'project.path') j = { '工程文件路径': 'C:\\\\Temp\\\\歌\\\\歌.acep', '未保存改动': '没有（已落盘）' };
        else if (b.toolId === 'pitch.xcopy') j = {
          '轨道': '源轨 0 → 目标轨 1', '片段': '源「A」→ 目标「B」',
          '源': '轨 0 第 1–1 小节（工程 tick 0..1920）= 片段内 0..1920',
          '目标': '轨 1 同一段（片段内 0..1920）',
          '曲线': { '结果': '源 640 个样本：写进已有覆盖 0 处；新建 1 条 entry（640 个样本）' },
          '锚点': { '结果': '源区间里没有锚点' },
          '音符颤音': { '结果': '源 2 个带颤音的音符 → 写到 2 个目标音符' },
          '备份': 'C:\\\\Temp\\\\ACETool\\\\acep-backup\\\\歌-x.acep',
          '自检': ['payload 重解通过（1000 → 1058 字节）'], '提示': '已**原地**改写工程文件。' };
        else j = {};
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
    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 720, height: 640, deviceScaleFactor: 2, mobile: false });
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'pitchx.html' });
    await sleep(2000);
    console.log('页面错误：' + JSON.stringify(await evalJS(ws, `window.__err`)));
    console.log('轨道下拉：' + await evalJS(ws, `document.getElementById('src').options.length + ' 条 · 选中 ' +
      document.getElementById('src').value + ' → ' + document.getElementById('dst').value`));
    console.log('读数：' + await evalJS(ws, `document.getElementById('rangeInfo').textContent`));
    console.log('缩略图：' + await evalJS(ws, `(function(){
      var c = document.getElementById('mm');
      var g = c.getContext('2d');
      var d = g.getImageData(0, 0, c.width, c.height).data;
      var ink = 0;
      for (var i = 3; i < d.length; i += 4) { if (d[i] > 0) ink++; }
      return '画布 ' + c.width + '×' + c.height + ' · 非透明像素 ' + ink;
    })()`));
    await evalJS(ws, `document.getElementById('p1').click()`);
    await sleep(400);
    console.log('① 保存工程：' + await evalJS(ws, `document.getElementById('p1stat').textContent + ' · ' + document.getElementById('p2').disabled`));
    await evalJS(ws, `document.getElementById('p2').click()`);
    await sleep(500);
    console.log('② 结果：' + (await evalJS(ws, `document.getElementById('out').textContent`)).split('\n').slice(0, 5).join(' / '));
    console.log('③ 恢复备份：' + await evalJS(ws, `document.getElementById('ustat').textContent`));
    console.log('请求序列：' + await evalJS(ws, `window.__req.join(',')`));
    const shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
    fs.writeFileSync(path.join(__dirname, 'pitchx-page.png'), Buffer.from(shot.data, 'base64'));
    console.log('落盘：devtools/pitchx-page.png');
  } catch (e) {
    console.error('崩了：' + e.message);
    process.exitCode = 1;
  }
  ws.close();
  child.kill();
})();
