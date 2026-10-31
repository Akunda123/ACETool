// 导出乐谱（用户 2026-10-07 三条要求）：
//   ① 「不要弹打印界面直接预览」——点「导出预览」**不能**调 `window.print()`，而是出页内预览；
//   ② 「用户选择导出五线谱或简谱」——「导出内容」下拉：两排 / 只要五线谱 / 只要简谱；
//   ③ 「让谱子不再每页只有一行」——**一页里叠好几行**（按小节切行 + 两端对齐到页宽）。
// 另外仍然真出一份 PDF（CDP `Page.printToPDF`）核对页数 = 预览页数。
//
// 用法：node devtools/e2e-notation-print.js [url]
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
const PORT = 9780 + (process.pid % 80);
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

// 假「从 ACE 读取」：**160 个元素**（几十小节 ⇒ 必然多行多页），带连音与休止
function readJSON() {
  const els = [];
  for (let i = 0; i < 160; i++) {
    els.push({ kind: 'note', pitch: [1, 2, 3, 5, 6][i % 5], duration: [4, 8, 8, 4][i % 4], lyric: 'la' });
    if (i % 11 === 10) els.push({ kind: 'sustain', duration: 4 });
    if (i % 17 === 16) els.push({ kind: 'note', duration: 4, rest: true, pitch: 0 });
  }
  return {
    notation: 'elements', scope: 'clip', clip: '测试片段', clipType: 'sing', trackIndex: 0, notes: els.length,
    keyFrom: 'manual', key: 0, keyName: 'C', meter: '4/4', elements: els, note: '只读：没动工程。',
  };
}

(async () => {
  const child = spawn(EDGE, [
    '--headless=new', '--disable-gpu', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-notap-' + PORT), 'about:blank',
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
    window.__printed = 0;
    // ⭐ 无头里 window.print 只记账 —— 而"点导出预览不该走到这里"正是要断言的第一条
    window.print = function () { window.__printed++; };
    var orig = window.fetch;
    window.fetch = function(u, o){
      var s = String(u);
      if (s.indexOf('/api/run') >= 0) {
        var body = JSON.parse(o.body);
        window.__req.push(body);
        return Promise.resolve(new Response(JSON.stringify({ok:true,calls:[{json:(${readJSON.toString()})()}]}), {status:200, headers:{'Content-Type':'application/json'}}));
      }
      if (s.indexOf('/api/selfcheck') >= 0) {
        return Promise.resolve(new Response(JSON.stringify({online:true,version:{forAce:'cli · test'},project:{projectName:''}}), {status:200, headers:{'Content-Type':'application/json'}}));
      }
      return orig.apply(window, arguments);
    };
  })()`;

  try {
    await cdp(ws, ++seq, 'Page.enable', {});
    await cdp(ws, ++seq, 'Page.addScriptToEvaluateOnNewDocument', { source: stub });
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'notation.html' });
    await sleep(1600);
    await evalJS(ws, `document.getElementById('nt-read').click()`);
    for (let i = 0; i < 25 && /^0 个元素/.test(await evalJS(ws, `document.getElementById('nt-count').textContent`)); i++) await sleep(150);
    ok('先读进一份长谱（160+ 个元素）',
      !/^0 /.test(await evalJS(ws, `document.getElementById('nt-count').textContent`)),
      await evalJS(ws, `document.getElementById('nt-count').textContent`));

    ok('「导出内容」下拉在面板上（两排 / 只要五线谱 / 只要简谱）',
      (await evalJS(ws, `(function(){ var s=document.getElementById('nt-rows'); return !!s && s.options.length === 3 && /五线谱/.test(s.options[1].textContent) && /简谱/.test(s.options[2].textContent); })()`)) === true);

    // ① 点「导出预览」⇒ **不弹打印**，只出预览
    await evalJS(ws, `(function(){ window.__req.length=0; document.getElementById('nt-print-top').click(); return true; })()`);
    await sleep(500);
    ok('⭐ 点「导出预览」**不调** `window.print()`（不弹打印界面）',
      (await evalJS(ws, `window.__printed`)) === 0, 'printed=' + (await evalJS(ws, `window.__printed`)));
    const pv = JSON.parse(await evalJS(ws, `(function(){
      var box = document.getElementById('nt-printroot');
      var pages = Array.prototype.slice.call(document.querySelectorAll('#nt-printroot .nt-print-page'));
      return JSON.stringify({
        shown: !!box && getComputedStyle(box).display !== 'none',
        pages: pages.length,
        lines: pages.map(function(p){ return p.querySelectorAll('svg .nt-jp').length; }),   // 每页几行（简谱那排）
        staffRows: pages.map(function(p){ return p.querySelectorAll('svg .nt-staff').length; }),
        bar: !!document.querySelector('#nt-printroot .nt-pv-bar'),
        meta: (document.querySelector('.nt-pv-meta')||{}).textContent || '',
        out: document.getElementById('nt-out').textContent,
      });
    })()`));
    ok('⭐ 预览出来了（浮层可见 + 每页一张白纸 + 顶栏有按钮）',
      pv.shown && pv.pages >= 1 && pv.bar, JSON.stringify({ shown: pv.shown, pages: pv.pages, bar: pv.bar }));
    ok('⭐ **每页不止一行**（用户点名要的）',
      pv.lines.every((n) => n >= 2), '每页行数 = ' + JSON.stringify(pv.lines));
    ok('预览顶栏写清"几页 / 每页几行 / 共几行 / 导出内容"',
      /\d+ 页 · 每页 \d+ 行 · 共 \d+ 行/.test(pv.meta) && /简谱 \+ 五线谱/.test(pv.meta), pv.meta);
    ok('⭐ 结果框只留一行落款（页数 / 行数 / 纸型）—— 用户 2026-10-08「删除不必要的文字」',
      /^已出预览：\d+ 页 · 共 \d+ 行 · A4 (横向|竖向)$/.test((pv.out || '').trim()), (pv.out || '').slice(0, 80));

    // ② 换「导出内容」⇒ 只出那一排（也不弹打印）
    await evalJS(ws, `(function(){ var s=document.getElementById('nt-rows-pv'); s.value='staff';
      s.dispatchEvent(new Event('change')); return true; })()`);
    await sleep(400);
    const staffOnly = JSON.parse(await evalJS(ws, `(function(){
      var pages = Array.prototype.slice.call(document.querySelectorAll('#nt-printroot .nt-print-page'));
      return JSON.stringify({
        jp: pages.map(function(p){ return p.querySelectorAll('svg .nt-jp').length; }),
        st: pages.map(function(p){ return p.querySelectorAll('svg .nt-staff').length; }),
        printed: window.__printed,
        rows: document.getElementById('nt-rows').value,
        meta: (document.querySelector('.nt-pv-meta')||{}).textContent || '',
      });
    })()`));
    ok('⭐ 选「只要五线谱」⇒ 预览里**只有五线谱那排**，面板上的下拉也跟着同步',
      staffOnly.jp.every((n) => n === 0) && staffOnly.st.every((n) => n >= 2) &&
      staffOnly.rows === 'staff' && /只要五线谱/.test(staffOnly.meta),
      JSON.stringify(staffOnly));
    ok('换排重画时**仍然不弹**打印', staffOnly.printed === 0);

    await evalJS(ws, `(function(){ var s=document.getElementById('nt-rows-pv'); s.value='jianpu';
      s.dispatchEvent(new Event('change')); return true; })()`);
    await sleep(400);
    const jpOnly = JSON.parse(await evalJS(ws, `(function(){
      var pages = Array.prototype.slice.call(document.querySelectorAll('#nt-printroot .nt-print-page'));
      return JSON.stringify({
        jp: pages.map(function(p){ return p.querySelectorAll('svg .nt-jp').length; }),
        st: pages.map(function(p){ return p.querySelectorAll('svg .nt-staff').length; }),
      });
    })()`));
    ok('⭐ 选「只要简谱」⇒ 只剩简谱那排',
      jpOnly.jp.every((n) => n >= 2) && jpOnly.st.every((n) => n === 0), JSON.stringify(jpOnly));

    // ⭐ 「每行几小节」下拉（用户 2026-10-07：「挂下拉」）
    ok('「每行几小节」下拉在面板上（自动 + 1/2/3/4/6/8/12 小节）',
      (await evalJS(ws, `(function(){ var s=document.getElementById('nt-mpl'); return !!s && s.options.length === 8 && /自动/.test(s.options[0].textContent) && s.options[1].value === '1' && s.value === '0'; })()`)) === true);
    await evalJS(ws, `(function(){ var s=document.getElementById('nt-mpl-pv'); s.value='2';
      s.dispatchEvent(new Event('change')); return true; })()`);
    await sleep(500);
    const mpl2 = JSON.parse(await evalJS(ws, `(function(){
      var pages = Array.prototype.slice.call(document.querySelectorAll('#nt-printroot .nt-print-page'));
      return JSON.stringify({
        lines: pages.map(function(p){ return p.querySelectorAll('svg .nt-jp').length; }),
        bar: document.querySelector('.nt-pv-meta').textContent,
        main: document.getElementById('nt-mpl').value,
        printed: window.__printed,
      });
    })()`));
    ok('⭐ 换「每行 2 小节」⇒ 行数变多、面板下拉同步、顶栏写明"每行 2 小节"',
      mpl2.bar.indexOf('每行 2 小节') >= 0 && mpl2.main === '2' && mpl2.printed === 0 &&
      mpl2.lines.reduce((a, b) => a + b, 0) > 8, JSON.stringify(mpl2));

    // ⭐ 跨行连音 + **按小节线拆开**（用面板自己的「示例」= 1-7 音阶，8 个四分音符 = 2 小节 4/4）
    //   预期：拆开之后**每一行正好 4 拍**（真谱里一小节就是一小节）；
    //   示例里没有连音格 ⇒ 不该有连音线（跨行甩出/接上的那条由下面读 ACE 的长谱覆盖）。
    await evalJS(ws, `document.getElementById('nt-demo').click()`);
    await sleep(300);
    await evalJS(ws, `(function(){
      var s=document.getElementById('nt-rows'); s.value='staff';
      var m=document.getElementById('nt-mpl'); m.value='1'; return true; })()`);
    await evalJS(ws, `document.getElementById('nt-print-top').click()`);
    await sleep(500);
    const bars = await evalJS(ws, `(function(){
      var T = window.NotationPanel._test;
      var b = T.buildSystems(T.DEMO, 0, { n: 4, d: 4 }, 'staff', 1);
      return JSON.stringify(b.systems.map(function (s) {
        var t = 0;
        s.cols.forEach(function (c) { t += T.colBeats(c); });
        return Math.round(t * 1000) / 1000;
      }));
    })()`);
    ok('⭐ **按小节线拆开**：拆完每一行正好 4 拍（示例 1-7 音阶：2 小节 / 4-4）',
      bars === '[4,4]', bars);
    const ties = JSON.parse(await evalJS(ws, `(function(){
      var rows = Array.prototype.slice.call(document.querySelectorAll('#nt-printroot .nt-staff'));
      return JSON.stringify({ staffRows: rows.length,
        ties: rows.map(function(r){ return r.querySelectorAll('.nt-tie').length; }) });
    })()`));
    ok('⭐ 音阶示例是干净的两行、没有连音线（0,0）',
      ties.staffRows === 2 && ties.ties.join(',') === '0,0', JSON.stringify(ties));

    // 回到"读 ACE（假数据 183 个元素）+ 两排 + 自动"，再真出一份 PDF（长谱才有"每页多行"可验）
    await evalJS(ws, `document.getElementById('nt-read').click()`);
    await sleep(600);
    await evalJS(ws, `(function(){
      var s=document.getElementById('nt-rows'); s.value='both';
      var m=document.getElementById('nt-mpl'); m.value='0';
      document.getElementById('nt-print-top').click(); return true; })()`);
    await sleep(500);
    const pages = await evalJS(ws, `document.querySelectorAll('#nt-printroot .nt-print-page').length`);
    const pdf = await cdp(ws, ++seq, 'Page.printToPDF', {
      landscape: true, printBackground: true, preferCSSPageSize: true,
      marginTop: 0, marginBottom: 0, marginLeft: 0, marginRight: 0,
    });
    const buf = Buffer.from(pdf.data, 'base64');
    fs.writeFileSync(path.join(__dirname, 'notation-print.pdf'), buf);
    const pagesInPdf = (buf.toString('latin1').match(/\/Type\s*\/Page[^s]/g) || []).length;
    ok('⭐ 真出一份 PDF：页数 = 预览页数',
      buf.slice(0, 5).toString() === '%PDF-' && pagesInPdf === pages,
      '%PDF · ' + pagesInPdf + ' 页 / 预览 ' + pages + ' 页 · ' + buf.length + ' B');
    // ⭐ A4：PDF 的 MediaBox 必须是 A4 横向（841.89 × 595.28 pt）—— `@page` 真生效的硬证据
    const mb = ((buf.toString('latin1').match(/\/MediaBox\s*\[([^\]]*)\]/) || [])[1] || '').trim();
    const mbn = mb.split(/\s+/).map(Number);
    ok('⭐ 每页就是 A4：PDF 的 MediaBox = A4 横向（841.9 × 595.3 pt）',
      mbn.length === 4 && Math.abs(mbn[2] - 841.89) < 1.5 && Math.abs(mbn[3] - 595.28) < 1.5, mb);
    console.log('  落盘：devtools/notation-print.pdf（' + buf.length + ' 字节 · ' + pagesInPdf + ' 页 · MediaBox ' + mb + '）');

    // 预览里的「打印」按钮**才**调 window.print
    await evalJS(ws, `document.getElementById('nt-pv-print').click()`);
    await sleep(200);
    ok('预览里点「打印 / 另存为 PDF」才调 `window.print()`',
      (await evalJS(ws, `window.__printed`)) === 1, 'printed=' + (await evalJS(ws, `window.__printed`)));

    // 打印媒体下量一遍：屏幕上的一切都藏了、只剩预览（而且每页仍有多行）
    await cdp(ws, ++seq, 'Emulation.setEmulatedMedia', { media: 'print' });
    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 1122, height: 793, deviceScaleFactor: 2, mobile: false });
    await sleep(400);
    const printState = JSON.parse(await evalJS(ws, `(function(){
      var root = document.getElementById('nt-printroot');
      var rest = Array.prototype.slice.call(document.body.children)
        .filter(function(e){ return e !== root && getComputedStyle(e).display !== 'none'; })
        .map(function(e){ return e.tagName + '#' + (e.id || ''); });
      var pages = Array.prototype.slice.call(document.querySelectorAll('#nt-printroot .nt-print-page'));
      return JSON.stringify({ root: getComputedStyle(root).display, rest: rest, pages: pages.length,
        lines: pages.slice(0,2).map(function(p){ return p.querySelectorAll('svg .nt-jp').length; }),
        bar: getComputedStyle(document.querySelector('.nt-pv-bar')).display });
    })()`));
    ok('⭐ 打印媒体下：其它一切都藏了、预览顶栏也不印、每页仍有多行',
      printState.root === 'block' && printState.rest.length === 0 && printState.bar === 'none' &&
      printState.pages === pages && printState.lines.every((n) => n >= 2), JSON.stringify(printState));
    const shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(__dirname, 'notation-print-page.png'), Buffer.from(shot.data, 'base64'));
    console.log('  落盘：devtools/notation-print-page.png（打印版第一页 = 一页好几行）');

    // 关闭预览
    await cdp(ws, ++seq, 'Emulation.setEmulatedMedia', { media: '' });
    await cdp(ws, ++seq, 'Emulation.clearDeviceMetricsOverride', {});
    await evalJS(ws, `document.getElementById('nt-pv-close').click()`);
    await sleep(150);
    ok('「关闭」把预览收起来（屏幕上不留东西）',
      (await evalJS(ws, `(function(){ var b=document.getElementById('nt-printroot'); return !b || getComputedStyle(b).display === 'none'; })()`)) === true);

    // ⭐ 纸型（用户 2026-10-08「不是应该按照 A4 大小来吗」）：屏幕上那张"纸"就是 A4 的 mm
    const paper = JSON.parse(await evalJS(ws, `(function(){
      var MM = 96 / 25.4;
      document.getElementById('nt-print-top').click();
      var first = document.querySelector('#nt-printroot .nt-print-page');
      var box = first.getBoundingClientRect(), cs = getComputedStyle(first);
      return JSON.stringify({
        paper: [Math.round(box.width / MM), Math.round(box.height / MM)],
        padMM: Math.round(parseFloat(cs.paddingLeft) / MM),
        inner: [Math.round((box.width - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight)) / MM),
                Math.round((box.height - parseFloat(cs.paddingTop) - parseFloat(cs.paddingBottom)) / MM)],
        svgMM: Math.round(first.querySelector('svg').getBoundingClientRect().width / MM),
        atPage: (document.getElementById('nt-paper-style') || {}).textContent || '',
        meta: document.querySelector('.nt-pv-meta').textContent,
      });
    })()`));
    ok('⭐ 预览那张纸 = A4 横向 297×210mm、边距 10mm、内容区 277×190mm（谱面正好 277mm 宽）',
      paper.paper[0] === 297 && paper.paper[1] === 210 && paper.padMM === 10 &&
      paper.inner[0] === 277 && paper.inner[1] === 190 && paper.svgMM === 277,
      JSON.stringify(paper));
    ok('⭐ 打印的纸型跟着走：`@page{size:A4 landscape;margin:0}`（页边距由纸自己的 padding 给）',
      /@page\{size:A4 landscape;margin:0\}/.test(paper.atPage) && /A4 横向/.test(paper.meta), paper.atPage);
    // 换成 A4 竖向 ⇒ 纸与 @page 都换
    const portrait = JSON.parse(await evalJS(ws, `(function(){
      var MM = 96 / 25.4;
      var s = document.getElementById('nt-paper-pv');
      s.value = 'portrait'; s.dispatchEvent(new Event('change'));
      var first = document.querySelector('#nt-printroot .nt-print-page');
      var box = first.getBoundingClientRect();
      return JSON.stringify({ paper: [Math.round(box.width / MM), Math.round(box.height / MM)],
        atPage: (document.getElementById('nt-paper-style') || {}).textContent || '',
        main: document.getElementById('nt-paper').value,
        meta: document.querySelector('.nt-pv-meta').textContent });
    })()`));
    ok('⭐ 换「A4 竖向」⇒ 纸变 210×297、`@page` 跟到 portrait、面板下拉同步',
      portrait.paper[0] === 210 && portrait.paper[1] === 297 &&
      /@page\{size:A4 portrait;margin:0\}/.test(portrait.atPage) && portrait.main === 'portrait' &&
      /A4 竖向/.test(portrait.meta), JSON.stringify(portrait));
  } catch (e) {
    console.error('崩了：' + e.message);
    console.error(String(e.stack || '').split('\n').slice(0, 5).join('\n'));
    bad++;
  }
  ws.close();
  child.kill();
  await sleep(200);
  console.log('\n' + (bad ? bad + ' 项失败' : '全部通过'));
  process.exitCode = bad ? 1 : 0;
})();
