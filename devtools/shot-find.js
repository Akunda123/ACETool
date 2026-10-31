// 证据图：查找页**现在的样子**（`find.html` 独立窗口 + 主面板 index.html 里那一页）。
// 不需要 ACE：桩掉 /api/run（find.notes 回 3 条命中、view.minimap 回一张假工程），
// 让图上有：非空的结果框、带音符轮廓的预览、命中高亮、序号框。
// 用法：node devtools/shot-find.js [url]
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
const PORT = 9620 + (process.pid % 60);
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
function mapJSON() {
  return {
    trackCount: 2, clipCount: 2, noteCount: 5, firstTick: 0, lastTick: 5760, meter: '4/4', tempo: 120,
    playheadTick: 0, noteBudget: 24, clipsWithoutNotes: 0,
    tracks: [
      { index: 0, name: '轨 0', type: 'sing', clips: [
        { index: 0, uuid: '{c1}', name: '片段 A', type: 'sing', begin: 0, end: 3840, noteCount: 3, color: '#859C3F',
          minPitch: 58, maxPitch: 64, notes: [{ pos: 0, dur: 480, pitch: 60 }, { pos: 960, dur: 480, pitch: 62 }, { pos: 2880, dur: 480, pitch: 64 }] },
      ] },
      { index: 1, name: '轨 1', type: 'instrument', clips: [
        { index: 0, uuid: '{c2}', name: '片段 B', type: 'instrument', begin: 1920, end: 5760, noteCount: 2, color: '#3F7C9C', minPitch: 48, maxPitch: 55,
          notes: [{ pos: 0, dur: 960, pitch: 50 }, { pos: 1920, dur: 960, pitch: 55 }] },
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
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-shotfind-' + PORT), 'about:blank',
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
        var j = (body.toolId === 'find.notes') ? (${hitsJSON.toString()})()
          : (body.toolId === 'view.minimap') ? (${mapJSON.toString()})() : { tick: 0 };
        return Promise.resolve(new Response(JSON.stringify({ok:true,calls:[{json:j, exitCode:0, stdout:''}]}), {status:200, headers:{'Content-Type':'application/json'}}));
      }
      if (s.indexOf('/api/selfcheck') >= 0) {
        return Promise.resolve(new Response(JSON.stringify((${selfcheck.toString()})()), {status:200, headers:{'Content-Type':'application/json'}}));
      }
      return orig.apply(window, arguments);
    };
  })()`;

  try {
    await cdp(ws, ++seq, 'Page.enable', {});
    await cdp(ws, ++seq, 'Page.addScriptToEvaluateOnNewDocument', { source: stub });

    // ① 独立窗口
    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 700, height: 900, deviceScaleFactor: 2, mobile: false });
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'find.html' });
    await sleep(2200);
    for (let i = 0; i < 30 && !(await evalJS(ws, `!!document.getElementById('fpinfo')`)); i++) await sleep(200);
    await evalJS(ws, `document.getElementById('fprefresh').click()`);
    await sleep(600);
    console.log('独立窗口 · 结果框：' + JSON.stringify(await evalJS(ws, `document.getElementById('fpinfo').textContent`)));
    console.log('独立窗口 · 预览：' + await evalJS(ws, `(function(){
      var el = document.getElementById('fpmap');
      return '音符轮廓 ' + el.querySelectorAll('.mmclip rect[fill="none"]').length +
        ' · 命中标记 ' + el.querySelectorAll('.fpmk').length +
        ' · 面板上的字：' + document.querySelector('.findpanel').textContent.replace(/\\s+/g, ' ').slice(0, 120);
    })()`));
    console.log('独立窗口 · 面板上的字：' + JSON.stringify(await evalJS(ws, `(function(){
      return document.querySelector('.findpanel').textContent.replace(/\\s+/g, ' ').slice(0, 90);
    })()`)));
    // ⭐ 用户 2026-10-14：属性下拉平铺（没有 optgroup、标签里没有 ★）；参数层那个「我们加的」小标也没了
    console.log('独立窗口 · 属性下拉：' + await evalJS(ws, `(function(){
      var s = document.getElementById('fpattr');
      if (!s) return '没有 fpattr';
      var star = 0;
      for (var i = 0; i < s.options.length; i++) { if (s.options[i].textContent.indexOf('★') >= 0) star++; }
      return s.options.length + ' 项 · optgroup ' + s.querySelectorAll('optgroup').length + ' 个 · 带 ★ 的 ' + star +
        ' 项 · 末尾两项：' + s.options[s.options.length - 2].textContent + ' / ' + s.options[s.options.length - 1].textContent;
    })()`));
    console.log('独立窗口 · 有没有 fp-tag / 我们加的：' + await evalJS(ws, `(function(){
      return 'fp-tag ' + document.querySelectorAll('.fp-tag').length + ' 个 · 面板字里有「我们加的」：' +
        /我们加的/.test(document.querySelector('.findpanel').textContent);
    })()`));
    let shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
    fs.writeFileSync(path.join(__dirname, 'find-window.png'), Buffer.from(shot.data, 'base64'));
    console.log('落盘：devtools/find-window.png');

    // ② 主面板里那一页（同一份模块）
    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 1200, height: 1100, deviceScaleFactor: 1.5, mobile: false });
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'index.html#find.notes' });
    await sleep(2000);
    for (let i = 0; i < 30 && !(await evalJS(ws, `!!document.getElementById('fpinfo')`)); i++) await sleep(200);
    await evalJS(ws, `document.getElementById('fprefresh').click()`);
    await sleep(600);
    console.log('主面板 · 结果区文本：' + JSON.stringify(await evalJS(ws, `(function(){
      var a = document.getElementById('fpresult'), b = document.getElementById('result'), c = document.getElementById('logbody');
      return 'fpresult=' + (a ? a.textContent.replace(/\\s+/g,' ').slice(0,120) : '(无)') +
        ' | result=' + (b ? b.textContent.replace(/\\s+/g,' ').slice(0,80) : '(无)') +
        ' | log=' + (c ? c.textContent.replace(/\\s+/g,' ').slice(0,80) : '(无)');
    })()`)));
    shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
    fs.writeFileSync(path.join(__dirname, 'find-inpanel.png'), Buffer.from(shot.data, 'base64'));
    console.log('落盘：devtools/find-inpanel.png');
  } catch (e) {
    console.error('崩了：' + e.message);
    process.exitCode = 1;
  }
  ws.close();
  child.kill();
})();
