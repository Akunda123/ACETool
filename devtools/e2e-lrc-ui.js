// 在**真的 lrc.html 页面里**跑一遍「分割 / 合并」—— 不靠肉眼、也不靠读代码。
//
// 为什么要这样测：分割/合并是纯前端逻辑（服务端只给逐字符时间 `charSec`），
// 而它的对错很具体：新行的时间戳必须**正好**等于断开处那个字符对应的音符起始时间。
// 用 CDP 连进页面，直接改光标、点按钮、读 `S.rows`。
//
// 用法：node devtools/e2e-lrc-ui.js
const { spawn } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const EDGE = [
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
].find((p) => fs.existsSync(p));
const BASE = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json'), 'utf8')).url;
const PORT = 9800 + (process.pid % 100);
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
  if (r.exceptionDetails) throw new Error('页面里抛错：' + JSON.stringify(r.exceptionDetails.exception && r.exceptionDetails.exception.description || r.exceptionDetails.text));
  return r.result.value;
}

(async () => {
  const child = spawn(EDGE, [
    '--headless=new', '--disable-gpu', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-lrcui-' + PORT),
    BASE + 'lrc.html',
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
    // 等页面把 lyric.lrc 拉回来（它要跑几条 CLI）
    // ⚠️ 轮询要**容错**：页面还没解析到 `<script>` 时 `S` 根本不存在，直接求值会抛
    // `ReferenceError: S is not defined`（第一版就栽在这，看着像"页面坏了"）。
    const rowsNow = async () => {
      try { return await evalJS(ws, 'typeof S === "undefined" ? 0 : S.rows.length'); } catch (e) { return 0; }
    };
    for (let i = 0; i < 60; i++) {
      if ((await rowsNow()) > 2) break;
      await sleep(300);
    }
    const n0 = await rowsNow();
    ok('页面读到了行', n0 > 2, n0 + ' 行');
    if (!(n0 > 2)) throw new Error('页面没读出内容：' + await evalJS(ws, '$("stat").textContent'));

    // 选项行的数字框必须是**紧凑**的（第一版没写 flex:0 0 auto，被撑到 230 px，一行只放得下两个框）
    const wOff = await evalJS(ws, `document.getElementById('offMin').getBoundingClientRect().width`);
    ok('偏移数字框是紧凑的（没被 flex 撑开）', wOff <= 90, 'offMin=' + Math.round(wOff) + 'px');
    const optRows = await evalJS(ws, `(function(){var s={};[].forEach.call(document.querySelectorAll('.optrow'),function(e){s[e.offsetTop]=1});return Object.keys(s).length;})()`);
    ok('选项是**显式两行**（不靠自由换行，免得一加控件就变三行）', optRows === 2, optRows + ' 行');

    // ⭐ 短间隙阈值：滑块档位**与主页「连接音符」同一张表**（只有第 0 档名字不同：不忽略 / 不设限）
    const tierNames = await evalJS(ws, 'TIERS.map(function(x){return x.name}).join("|")');
    const tierTicks = await evalJS(ws, 'TIERS.map(function(x){return x.t}).join(",")');
    ok('档位表 = 9 档、数值与连接音符一致',
      tierTicks === '0,1920,960,480,240,120,60,30,15', tierTicks);
    ok('第 0 档在这边叫「不忽略」（那边叫「不设限」）', /^不忽略\|/.test(tierNames), tierNames);
    const shown = await evalJS(ws, `document.getElementById('gapName').textContent`);
    ok('滑块旁边显示当前档位名', shown === '不忽略', shown);
    // 拖到「十六分音符」（120 tick，实测正好对上 ACE 自带）⇒ 重新切行
    await evalJS(ws, `(function(){var g=$('gap');g.value='5';g.dispatchEvent(new Event('input'));g.dispatchEvent(new Event('change'));return true;})()`);
    for (let i = 0; i < 40; i++) {
      if ((await evalJS(ws, `document.getElementById('gapName').textContent`)) === '十六分音符'
        && (await evalJS(ws, 'S.rows.length')) < n0) break;
      await sleep(300);
    }
    ok('选「十六分音符」后重新切行（行数变少）', (await evalJS(ws, 'S.rows.length')) < n0,
      n0 + ' → ' + (await evalJS(ws, 'S.rows.length')));
    // 回到「不忽略」再继续后面的用例（后面的断言基于 n0 那份行）
    await evalJS(ws, `(function(){var g=$('gap');g.value='0';g.dispatchEvent(new Event('input'));g.dispatchEvent(new Event('change'));return true;})()`);
    for (let i = 0; i < 40; i++) {
      if ((await rowsNow()) === n0) break;
      await sleep(300);
    }
    ok('回到「不忽略」行数复原', (await rowsNow()) === n0, String(await rowsNow()));

    // ⭐ 分割：断点必须落在**音符之间的间隙**上（用户 2026-10-05 口径）。
    // ⚠️ 期望值**按规则推导**，不写死歌词 —— 第一版写死了 `indexOf('jiao')`，
    // 工程一换（歌词全变成 `la la la`）那条用例就崩，看着像功能坏了。
    const rowIdx = await evalJS(ws, 'S.rows.findIndex(function(r){return r.charNote && r.charNote.length === r.text.length && new Set(r.charNote).size > 1})');
    ok('找得到一行有多个词（要验分割）', rowIdx >= 0, 'index=' + rowIdx);
    const b = JSON.parse(await evalJS(ws, `JSON.stringify(S.rows[${rowIdx}])`));
    const caretInside = 1;                        // 光标落在**第一个词的内部**
    const beforeTok = b.charNote[caretInside - 1];
    let wantCut = caretInside;
    while (wantCut < b.text.length && b.charNote[wantCut] <= beforeTok) wantCut++;  // 推到下一个词的开头
    const wantSec = b.charSec[wantCut];           // 新行时间 = 下一个音符的起始时间
    const wantText = b.text.slice(wantCut).replace(/^\s+/, '');
    await evalJS(ws, `(function(){var box=document.querySelectorAll('.ln:not(.sub) input.tx')[${rowIdx}];box.focus();box.setSelectionRange(${caretInside},${caretInside});document.querySelectorAll('.mini.split')[${rowIdx}].click();return true;})()`);
    const n1 = await evalJS(ws, 'S.rows.length');
    ok('分割：行数 +1', n1 === n0 + 1, n0 + ' → ' + n1);
    ok('分割：断在**音符间隙**上（没断在词中间）',
      (await evalJS(ws, `S.rows[${rowIdx}].text`)) === b.text.slice(0, wantCut).replace(/\s+$/, ''),
      JSON.stringify(await evalJS(ws, `S.rows[${rowIdx}].text`)) + ' vs ' + JSON.stringify(b.text.slice(0, wantCut).replace(/\s+$/, '')));
    const r1 = JSON.parse(await evalJS(ws, `JSON.stringify(S.rows[${rowIdx + 1}])`));
    ok('分割：新行文字 = 断点右边那截', r1.text === wantText, JSON.stringify(r1.text) + ' vs ' + JSON.stringify(wantText));
    ok('分割：新行时间戳 = 下一个音符的起始时间（不是中点、不是编的）',
      Math.abs(r1.times[0] - wantSec) < 1e-9, r1.times[0] + ' vs charSec=' + wantSec);
    ok('分割：新行只带一个时间戳', r1.times.length === 1, JSON.stringify(r1.times));
    ok('分割：两行的逐字符数组都与文字等长（后面还能继续分割/合并）',
      (await evalJS(ws, `S.rows[${rowIdx}].charSec.length === S.rows[${rowIdx}].text.length && S.rows[${rowIdx}].charNote.length === S.rows[${rowIdx}].text.length`))
      && (await evalJS(ws, `S.rows[${rowIdx + 1}].charSec.length === S.rows[${rowIdx + 1}].text.length && S.rows[${rowIdx + 1}].charNote.length === S.rows[${rowIdx + 1}].text.length`)));

    // 光标停在**最后一个音符**里 ⇒ 后面没有音符可断，必须**如实拒绝**（不许造一个假断点）
    const nBefore = await evalJS(ws, 'S.rows.length');
    await evalJS(ws, `(function(){var box=document.querySelectorAll('.ln:not(.sub) input.tx')[${rowIdx}];box.focus();var L=box.value.length;box.setSelectionRange(L-1,L-1);document.querySelectorAll('.mini.split')[${rowIdx}].click();return true;})()`);
    ok('分割：光标在最后一个音符里 ⇒ 拒绝且行数不变',
      (await evalJS(ws, 'S.rows.length')) === nBefore,
      await evalJS(ws, 'S.rows.length') + ' vs ' + nBefore);

    // ⭐ 合并：把第 1、2 行合起来，**只留前一个时间戳**
    const t0 = JSON.parse(await evalJS(ws, 'JSON.stringify(S.rows[0].times)'));
    const t1 = JSON.parse(await evalJS(ws, 'JSON.stringify(S.rows[1].times)'));
    const txt0 = await evalJS(ws, 'S.rows[0].text');
    const txt1 = await evalJS(ws, 'S.rows[1].text');
    await evalJS(ws, `document.querySelectorAll('.mini.merge')[0].click()`);
    const n2 = await evalJS(ws, 'S.rows.length');
    ok('合并：行数 -1', n2 === n1 - 1, n1 + ' → ' + n2);
    const m = JSON.parse(await evalJS(ws, 'JSON.stringify(S.rows[0])'));
    ok('合并：只留前一个时间戳', m.times.length === 1 && Math.abs(m.times[0] - t0[0]) < 1e-9,
      JSON.stringify(t0) + '+' + JSON.stringify(t1) + ' → ' + JSON.stringify(m.times));
    const wantMerge = txt0 + ((/[\u3400-\u9fff]$/.test(txt0) && /^[\u3400-\u9fff]/.test(txt1)) ? '' : ' ') + txt1;
    ok('合并：文字接上（两边都是汉字就直粘）', m.text === wantMerge, JSON.stringify(m.text));
    ok('合并：charSec 跟着合并（分割还能用）', (m.charSec || []).length === m.text.length,
      (m.charSec || []).length + ' vs ' + m.text.length);

    // 偏移改一下：时间戳要**就地**变，且不改数据（22.19s + 62.5s = 84.69s ⇒ [01:24.69]）
    await evalJS(ws, `$('offMin').value='1';$('offSec').value='2';$('offMs').value='500';$('offMin').dispatchEvent(new Event('input'));true`);
    const ts = await evalJS(ws, `document.querySelector('.ln .ts').textContent`);
    const raw = await evalJS(ws, 'S.rows[0].times[0]');
    const want = (function (t) {
      var min = Math.floor(t / 60), s = Math.floor(t) % 60, cs = Math.floor(t * 100) % 100;
      return '[01:' + (s < 10 ? '0' : '') + s + '.' + (cs < 10 ? '0' : '') + cs + ']';
    })(raw + 62.5);
    ok('改偏移：显示的时间戳跟着走', ts === want, ts + ' vs ' + want);
    ok('改偏移：原始秒不动（只是打印时加）', Math.abs(raw - t0[0]) < 1e-9, String(raw));

    // 成品文本：复制/保存用的那份，必须是「时间戳 + 文字」逐行
    const lrc = await evalJS(ws, 'renderLRC()');
    const lines = lrc.split('\n').filter((x) => x !== '');
    ok('成品 LRC 行数 = 行表行数', lines.length === n2, lines.length + ' vs ' + n2);
    ok('成品 LRC 每行都以时间戳开头', lines.every((l) => /^(\[\d+:\d\d\.\d\d\])+/.test(l)), JSON.stringify(lines[0]));

    // ---- ① 文本**可以直接改**（用户：「避免歌词错了二次编辑」）----
    await evalJS(ws, `(function(){var b=document.querySelectorAll('.ln:not(.sub) input.tx')[0];b.value='GAI GUO';b.dispatchEvent(new Event('change'));return true;})()`);
    ok('改文本：行表里的文字跟着变', (await evalJS(ws, 'S.rows[0].text')) === 'GAI GUO', await evalJS(ws, 'S.rows[0].text'));
    ok('改文本：该行被标记成「改过」（给个可见的记号）', (await evalJS(ws, '!!S.rows[0].edited')) === true);
    ok('改文本：成品 LRC 用的是**改后**的文字', /GAI GUO$/.test(await evalJS(ws, 'renderLRC().split("\\n")[0]')),
      await evalJS(ws, 'renderLRC().split("\\n")[0]'));
    // 改过的行逐字符时间已失效 ⇒ 分割必须**如实拒绝**（不许拿错位的时间造新行）
    const nEdited = await evalJS(ws, 'S.rows.length');
    await evalJS(ws, `(function(){var b=document.querySelectorAll('.ln:not(.sub) input.tx')[0];b.focus();b.setSelectionRange(2,2);document.querySelectorAll('.mini.split')[0].click();return true;})()`);
    ok('改过的行：分割被拒绝且行数不变', (await evalJS(ws, 'S.rows.length')) === nEdited,
      await evalJS(ws, '$("stat").textContent'));

    // ---- ② 多时间戳的行**可展开**（用户：「折叠歌词有点抽象，设计成可展开」）----
    await evalJS(ws, 'refresh()');
    for (let i = 0; i < 40; i++) { if (!(await evalJS(ws, '!!S.rows[0].edited'))) break; await sleep(300); }
    const mi = await evalJS(ws, 'S.rows.findIndex(function(r){return r.times.length>1})');
    ok('找得到一行是多时间戳的（要验展开）', mi >= 0, 'index=' + mi);
    if (mi >= 0) {
      const wantSub = await evalJS(ws, `S.rows[${mi}].times.length`);
      // ⚠️ 展开按钮**只出现在多时间戳的行上**，所以不能按下标去索引 `.mini.exp` 的 NodeList
      // （那是"这一类按钮"的顺序，不是行号）——要**在该行的 DOM 里**找。
      const clickExp = `document.querySelectorAll('.ln:not(.sub)')[${mi}].querySelector('.mini.exp').click()`;
      await evalJS(ws, clickExp);
      const gotSub = await evalJS(ws, `document.querySelectorAll('.ln.sub').length`);
      ok('展开：多时间戳的行摊成 N 条（每条一个时间戳）', gotSub === wantSub - 1, gotSub + ' vs ' + (wantSub - 1));
      ok('展开：按钮变成「收起」',
        /收起/.test(await evalJS(ws, `document.querySelectorAll('.ln:not(.sub)')[${mi}].querySelector('.mini.exp').textContent`)));
      ok('展开后行数**不变**（只是显示，不动物理行）', (await evalJS(ws, 'S.rows.length')) === n2);
      await evalJS(ws, clickExp);
      ok('收起：子行没了', (await evalJS(ws, 'document.querySelectorAll(".ln.sub").length')) === 0);
    }

    // ---- ③ 确认面板：能看能改最终 LRC ----
    ok('确认面板显示的就是成品 LRC',
      (await evalJS(ws, `$('finalText').value`)) === (await evalJS(ws, 'renderLRC()')));
    // 先把偏移归零（前面那条用例把它设成 62.5s 了）—— 否则下面反解出来的原始秒要减去它
    await evalJS(ws, `(function(){['offMin','offSec','offMs'].forEach(function(id){$(id).value='0';});$('offMin').dispatchEvent(new Event('input'));return true;})()`);
    await evalJS(ws, `(function(){var t=$('finalText');t.value='[00:01.00]yi\\n[00:02.50]er';t.dispatchEvent(new Event('change'));return true;})()`);
    ok('确认面板改文本 ⇒ 从文本重建行表', (await evalJS(ws, 'S.rows.length')) === 2,
      String(await evalJS(ws, 'S.rows.length')));
    ok('重建出来的时间戳是**原始秒**（上面显示时再加偏移）',
      Math.abs((await evalJS(ws, 'S.rows[1].times[0]')) - 2.5) < 1e-9,
      await evalJS(ws, 'S.rows[1].times[0]'));
    // 偏移非 0 时：面板显示带偏移、反解要减回去（不许来回加两次）
    await evalJS(ws, `(function(){$('offSec').value='10';$('offSec').dispatchEvent(new Event('input'));return true;})()`);
    ok('带偏移时：面板显示的是"原始秒 + 偏移"',
      /^\[00:12\.50\]/.test(await evalJS(ws, `$('finalText').value.split('\\n')[1]`)),
      await evalJS(ws, `$('finalText').value.split('\\n')[1]`));
    ok('带偏移时反解：减回偏移，仍是原始秒 2.5',
      Math.abs((await evalJS(ws, 'S.rows[1].times[0]')) - 2.5) < 1e-9,
      await evalJS(ws, 'S.rows[1].times[0]'));
    ok('重建出来的行不能再分割（逐字符时间已清空）',
      (await evalJS(ws, 'S.rows[0].charSec.length')) === 0);

    // ---- ④ ⭐ 用户 2026-10-06（截图）：浏览器别在框上弹历史下拉 ⇒ 三个能打字的框都得关自动填充 ----
    const af = await evalJS(ws, `[$('path'),$('finalText'),document.querySelector('.ln .tx')]
      .map(function(e){return e ? e.getAttribute('autocomplete') : 'MISSING';}).join('/')`);
    ok('路径框/最终文本/行文本框都关了自动填充', af === 'off/off/off', af);
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
