// 证据图 + 运行自检：复制颤音模板页（`vib.html`）—— 桩掉 /api/run，不需要 ACE。
// 用法：node devtools/shot-vib.js [url]
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
const PORT = 9920 + (process.pid % 50);
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

const TPL_NOTE = { '音符': '{u1}', '歌词': '啦', '音高': 62, '片段内位置（tick）': 240, '时长（tick）': 660 };
const DST_NOTES = [
  { '音符': '{u2}', '歌词': '啦', '音高': 64, '片段内位置（tick）': 900, '时长（tick）': 1020 },
  { '音符': '{u3}', '歌词': '啦', '音高': 65, '片段内位置（tick）': 1920, '时长（tick）': 840 },
  { '音符': '{u4}', '歌词': '啦', '音高': 66, '片段内位置（tick）': 2760, '时长（tick）': 1020 },
];
const VIB = '啦（音高 62，片段内 tick 240）：起始 音符内 133 tick · 频率 6.00 Hz · 幅度 2.500 半音 · 相位 0.00';

(async () => {
  const child = spawn(EDGE, ['--headless=new', '--disable-gpu', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-shotvib-' + PORT), 'about:blank'], { stdio: 'ignore' });
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
    var tplNote = ${JSON.stringify(TPL_NOTE)}, dstNotes = ${JSON.stringify(DST_NOTES)}, vib = ${JSON.stringify(VIB)};
    window.fetch = function (u, opt) {
      var s = String(u);
      if (s.indexOf('/api/run') >= 0) {
        var b = JSON.parse(opt.body); window.__req.push(b.toolId);
        var j = {};
        if (b.toolId === 'project.save') j = { savedPath: 'C:\\\\Temp\\\\歌\\\\歌.acep' };
        else if (b.toolId === 'project.path') j = { '工程文件路径': 'C:\\\\Temp\\\\歌\\\\歌.acep', '未保存改动': '没有（已落盘）', '工程名': '歌' };
        else if (b.toolId === 'vibrato.read') {
          var single = (b.values || {}).single === '1';
          j = { '工程文件': 'C:\\\\Temp\\\\歌\\\\歌.acep', '轨道': '轨 0', '片段': '片段1',
            '范围': single ? '选中的 1 个音符（当前片段「片段1」共 4 个）' : '选中的 3 个音符（当前片段「片段1」共 4 个）',
            '选中数': single ? 1 : 3,
            '音符信息': single ? [tplNote] : dstNotes,
            '颤音参数': single ? [vib] : ['（目标不需要颤音参数）'] };
        } else if (b.toolId === 'selection.clear') j = { selectionCount: 0, notesSelected: 0 };
        else if (b.toolId === 'vibrato.apply') j = {
          '片段': '片段「片段1」', '源': '模板：啦（音高 62，片段内 tick 240）', '目标': '目标 3 个音符',
          '音符颤音': { '结果': '把模板颤音写到 **3 个**音符上（跳过 0 个）' },
          '备份': 'C:\\\\Temp\\\\ACETool\\\\acep-backup\\\\歌-20261010-1.acep',
          '自检': ['payload 重解通过（9000 → 9132 字节）', '回读：payload 逐字节一致'] };
        else if (b.toolId === 'project.restore') j = { '用哪份备份': '歌-20261010-1.acep', '备份时间': '2026-10-10 21:00' };
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
    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 700, height: 660, deviceScaleFactor: 2, mobile: false });
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'vib.html' });
    await sleep(1800);
    console.log('页面错误：' + JSON.stringify(await evalJS(ws, `window.__err`)));
    console.log('顶栏：' + await evalJS(ws, `document.getElementById('stat').textContent`));
    console.log('初始禁用：① ' + await evalJS(ws, `document.getElementById('p1').disabled`) +
      ' ② ' + await evalJS(ws, `document.getElementById('p2').disabled`) +
      ' ③ ' + await evalJS(ws, `document.getElementById('p3').disabled`) +
      ' ↺ ' + await evalJS(ws, `document.getElementById('undo').disabled`));

    await evalJS(ws, `document.getElementById('p1').click()`);
    await sleep(500);
    console.log('① 状态：' + await evalJS(ws, `document.getElementById('p1stat').textContent`));
    console.log('① 模板框：' + (await evalJS(ws, `document.getElementById('tplbox').textContent`)).split('\n').join(' | '));
    console.log('① 清除选中：' + await evalJS(ws, `document.getElementById('out').textContent.indexOf('已清除 ACE 的选中') >= 0`));

    await evalJS(ws, `document.getElementById('p2').click()`);
    await sleep(500);
    console.log('② 状态：' + await evalJS(ws, `document.getElementById('p2stat').textContent`) +
      ' · ③ 现在可点：' + await evalJS(ws, `!document.getElementById('p3').disabled`));
    console.log('② 目标框：' + (await evalJS(ws, `document.getElementById('dstbox').textContent`)).split('\n')[0]);

    await evalJS(ws, `document.getElementById('p3').click()`);
    await sleep(600);
    console.log('③ 状态：' + await evalJS(ws, `document.getElementById('p3stat').textContent`) +
      ' · ↺ 现在可点：' + await evalJS(ws, `!document.getElementById('undo').disabled`));
    console.log('③ 输出：' + (await evalJS(ws, `document.getElementById('out').textContent`)).split('\n').join(' / '));
    console.log('请求序列：' + await evalJS(ws, `window.__req.join(' → ')`));
    const shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
    fs.writeFileSync(path.join(__dirname, 'vib-page.png'), Buffer.from(shot.data, 'base64'));
    console.log('落盘：devtools/vib-page.png');
  } catch (e) {
    console.error('崩了：' + e.message);
    process.exitCode = 1;
  }
  ws.close();
  child.kill();
})();
