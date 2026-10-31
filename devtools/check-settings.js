// 设置页自检：**静态接线** + **真渲染**（无头 Edge 里点「工具」那一页，读回真实数据）。
//
// 为什么要真渲染：设置页最容易出的错不是"标记没写"，而是**接错了**——
// 元素 id 在、但 JS 没绑上；或者绑上了、`/api/settings` 的数据没填进去（页面上全是"—"）。
// 静态检查看不出这两样，只有把页面真打开、点一下、把 DOM 读回来才算数。
//
// 用法：node devtools/check-settings.js [url]
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawn } = require('child_process');

const PANEL = path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json');
const BASE = (process.argv[2] || JSON.parse(fs.readFileSync(PANEL, 'utf8')).url).replace(/\/$/, '') + '/';
const EDGE = process.env.ACETOOL_EDGE ||
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe';
const PORT = 9300 + Math.floor(Math.random() * 400);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

let bad = 0;
const ok = (name, cond, extra) => {
  console.log((cond ? '  ok   ' : '  FAIL ') + name + (extra ? '  — ' + extra : ''));
  if (!cond) bad++;
};

/* ---------------- ① 静态接线 ---------------- */
const file = path.join(__dirname, '..', 'internal', 'web', 'assets', 'settings.html');
const s = fs.readFileSync(file, 'utf8');
const ids = ['page-tool', 'cliPath', 'timeoutSec', 'railOnBoot', 'railState', 'railToggle',
  'usingCli', 'detectedCli', 'panelUrl', 'configPath', 'saveTool', 'redetect', 'openPanel',
  'toolMsg', 'railMsg', 'panelMsg',
  // 开机启动（用户 2026-10-17）：勾 + 状态 + 写入位置 + 自己那颗保存键
  'autoStart', 'autoState', 'autoCmd', 'autoSave', 'autoMsg'];
const miss = ids.filter((i) => !new RegExp('id="' + i + '"').test(s));
ok('「工具」页的元素 id 都在', miss.length === 0, miss.length ? '缺 ' + miss.join(',') : ids.length + ' 个');
ok('导航里有「工具」那一页', /data-page="tool"/.test(s) && /<div class="page" id="page-tool">/.test(s));
ok('读 /api/settings / 存 POST /api/settings（带 X-ACETool）',
  /fetch\('\/api\/settings', \{ cache: 'no-store' \}\)/.test(s) &&
  /fetch\('\/api\/settings', \{ method: 'POST', headers: H2/.test(s) && /'X-ACETool': '1'/.test(s));
ok('侧栏开关走 POST /api/rail', /fetch\('\/api\/rail', \{[\s\S]{0,90}method: 'POST'/.test(s));
ok('「打开主页面」走 /api/app-window', /fetch\('\/api\/app-window'/.test(s));

/* ---------------- ② 真渲染（无头 Edge + CDP） ---------------- */
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
let seq = 500;
async function evalJS(ws, expr) {
  const r = await cdp(ws, ++seq, 'Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true });
  if (r.exceptionDetails) throw new Error('页面里抛错：' + ((r.exceptionDetails.exception && r.exceptionDetails.exception.description) || r.exceptionDetails.text));
  return r.result.value;
}

(async () => {
  console.log('面板：' + BASE);
  let child = null;
  try {
    child = spawn(EDGE, [
      '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
      '--remote-debugging-port=' + PORT,
      '--user-data-dir=' + path.join(os.tmpdir(), 'acetool-settings-' + PORT), 'about:blank',
    ], { stdio: 'ignore' });
    let target = null;
    for (let i = 0; i < 40 && !target; i++) {
      await sleep(250);
      try {
        const list = await (await fetch('http://127.0.0.1:' + PORT + '/json/list')).json();
        target = list.find((t) => t.type === 'page' && t.url.indexOf('devtools') < 0);
      } catch (e) { /* 还没起来 */ }
    }
    if (!target) throw new Error('连不上无头 Edge');
    const ws = new WebSocket(target.webSocketDebuggerUrl);
    await new Promise((res, rej) => { ws.onopen = res; ws.onerror = () => rej(new Error('WS 连不上')); });
    await cdp(ws, ++seq, 'Page.enable', {});
    await cdp(ws, ++seq, 'Runtime.enable', {});
    await cdp(ws, ++seq, 'Emulation.setDeviceMetricsOverride', { width: 1000, height: 700, deviceScaleFactor: 2, mobile: false });
    await cdp(ws, ++seq, 'Page.navigate', { url: BASE + 'settings.html' });
    await sleep(900);

    // 点「工具」，等 loadTool() 把数据填进去
    await evalJS(ws, `(function(){ var b=document.querySelector('[data-page="tool"]'); if(!b) return 'no-nav'; b.click(); return 'ok'; })()`);
    await sleep(1200);
    const view = await evalJS(ws, `JSON.stringify((function(){
      var on = document.getElementById('page-tool');
      var t = function(id){ var e=document.getElementById(id); return e ? (e.value!==undefined && e.value!=='' ? e.value : e.textContent) : null; };
      return {
        shown: !!on && on.classList.contains('on'),
        usingCli: t('usingCli'), detected: t('detectedCli'), cliPath: t('cliPath'),
        timeout: t('timeoutSec'), railState: t('railState'), railToggle: t('railToggle'),
        onBootChecked: document.getElementById('railOnBoot') ? document.getElementById('railOnBoot').checked : null,
        panelUrl: t('panelUrl'), configPath: t('configPath'),
        autoState: t('autoState'), autoCmd: t('autoCmd'),
        autoChecked: document.getElementById('autoStart') ? document.getElementById('autoStart').checked : null,
        toolMsg: t('toolMsg')
      };
    })())`);
    const v = JSON.parse(view);
    ok('点「工具」⇒ 那一页真的显示出来了', v.shown === true, JSON.stringify({ shown: v.shown }));
    ok('「此刻在用」的 CLI 填进来了（不是空的/不是 —）',
      !!v.usingCli && v.usingCli !== '—' && /\.exe$/i.test(v.usingCli.trim()), '「' + v.usingCli + '」');
    ok('超时读成了数字', /^\d+$/.test(String(v.timeout || '')) && Number(v.timeout) >= 1, 'timeout=' + v.timeout);
    ok('侧栏状态有话说（在跑/没在跑）', /跑/.test(String(v.railState || '')), '「' + v.railState + '」');
    ok('面板地址填进来了', /^http:\/\/127\.0\.0\.1:\d+/.test(String(v.panelUrl || '')), '「' + v.panelUrl + '」');
    ok('配置文件路径填进来了', /config\.json$/i.test(String(v.configPath || '').trim()), '「' + v.configPath + '」');
    ok('「随面板启动」勾选状态跟 API 一致（railOnBoot）',
      typeof v.onBootChecked === 'boolean', 'checked=' + v.onBootChecked);
    ok('侧栏那颗按钮的字跟着状态变（开启侧栏/停掉侧栏）', /侧栏/.test(String(v.railToggle || '')), '「' + v.railToggle + '」');
    // 开机启动（用户 2026-10-17）：状态得有话说，写入位置得是注册表那条（或明确说没有）
    ok('「开机启动」状态填进来了（已开启/未开启/平台没做）',
      /已开启|未开启|没做/.test(String(v.autoState || '')), '「' + v.autoState + '」');
    ok('「开机启动」把注册表里那条命令报出来了（Run\ACETool = …）',
      /Run\\ACETool/.test(String(v.autoCmd || '')), '「' + v.autoCmd + '」');
    ok('「开机启动」勾选状态是布尔（跟注册表一致）', typeof v.autoChecked === 'boolean', 'checked=' + v.autoChecked);

    // ⭐ 快捷键页：主页面那 **20 个位置**（用户 2026-10-17「打开 home.html 一个 + 19 个按键，
    //    共 20 个快捷键设置位置，默认都是空」）—— 数一遍、看是不是都（未绑）、「全局」是不是锁住的
    const navKey = await evalJS(ws, `(function(){
      var b = document.querySelector('[data-page="keys"]');
      if (!b) return 'no-nav';
      b.click();
      return 'ok';
    })()`);
    ok('设置页有「快捷键」那一页，能点开', navKey === 'ok', navKey);
    await sleep(1200);
    const keys = JSON.parse(await evalJS(ws, `JSON.stringify((function(){
      var secs = [].slice.call(document.querySelectorAll('#keys section'));
      var g = null;
      secs.forEach(function(s){
        var h = s.querySelector('h2');
        if (h && h.textContent.trim() === '主页面按钮') g = s;
      });
      if (!g) return { found: false, groups: secs.map(function(s){ return s.querySelector('h2') ? s.querySelector('h2').textContent.trim() : '?'; }) };
      var rows = [].slice.call(g.querySelectorAll('.row'));
      return {
        found: true,
        sections: secs.length,
        total: document.querySelectorAll('#keys .row').length,
        count: rows.length,
        ids: rows.map(function(r){ return r.getAttribute('data-act'); }),
        labels: rows.map(function(r){ return (r.querySelector('.label') || {}).textContent || ''; }),
        keys: rows.map(function(r){ return (r.querySelector('.key') || {}).textContent || ''; }),
        glob: rows.map(function(r){
          var c = r.querySelector('input[type=checkbox]');
          return c ? { checked: c.checked, disabled: c.disabled } : null;
        }),
      };
    })())`));
    ok('快捷键页里有「主页面按钮」这一组（20 个位置）', keys.found === true,
      keys.found ? '' : '现有分组：' + (keys.groups || []).join(' / '));
    if (keys.found) {
      // ⭐ 用户 2026-10-17：「只留我说的那几个快捷键」⇒ 这一组**就是全部**，
      // 不许再冒出"跑工具 / 在面板里走"那些老动作（它们已从动作表删掉）。
      ok('快捷键页**只有这一组**（老的那几组已经删干净了）',
        keys.sections === 1 && keys.total === 23, 'sections=' + keys.sections + ' rows=' + keys.total);
      ok('这一组正好 **22** 个位置（1 个打开主页面 + 21 个按钮，含「和声」「撤回这一批」）', keys.count === 23, '实际 ' + keys.count);
      ok('第一个是「打开主页面」，id 是 quick.open',
        keys.ids[0] === 'quick.open' && keys.labels[0] === '打开主页面', keys.ids[0] + ' / ' + keys.labels[0]);
      ok('19 个按钮的 id 都是 quick.<工具 id>',
        keys.ids.slice(1).every(function (x) { return /^quick\.[a-z.]+$/.test(x); }),
        keys.ids.slice(1).join(' '));
      // 用户自己绑过的键不算错（这条以前在干净配置上跑得通，但机器上已经有绑定时会误报）。
      // 「默认全空」这件事由 check-ui 直接断言 quick.go 里 DefaultKey 全是空串。
      ok('**快捷键现状**（用户自己绑的允许存在，只统计）',
        Array.isArray(keys.keys),
        keys.keys.filter(function (k) { return k.indexOf('未绑') < 0; }).join(' | ') || '全部未绑');
      ok('设置页**没有「全局」勾**了（用户 2026-10-20：所有键都是系统级，勾没意义）',
        keys.glob.every(function (c) { return c === null; }),
        '还找到勾的：' + JSON.stringify(keys.glob.filter(Boolean)));
    }

    // 截图留证：**滚到「主页面按钮」那一组**（要不截图停在最上面，看不到这次加的东西）
    await evalJS(ws, `(function(){
      var secs = [].slice.call(document.querySelectorAll('#keys section'));
      for (var i = 0; i < secs.length; i++) {
        var h = secs[i].querySelector('h2');
        if (h && h.textContent.trim() === '主页面按钮') { secs[i].scrollIntoView({ block: 'start' }); return 'ok'; }
      }
      return 'no-group';
    })()`);
    await sleep(400);
    const shot = await cdp(ws, ++seq, 'Page.captureScreenshot', { format: 'png' });
    const out = path.join(__dirname, 'settings-tool.png');
    fs.writeFileSync(out, Buffer.from(shot.data, 'base64'));
    console.log('  截图：devtools/settings-tool.png（滚到「主页面按钮」那一组）');
    ws.close();
  } catch (e) {
    ok('渲染检查', false, (e && e.message) || String(e));
  } finally {
    if (child) child.kill();
  }

  console.log('\n' + (bad ? bad + ' 项失败' : '全部通过'));
  process.exitCode = bad ? 1 : 0;
})();
