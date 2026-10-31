/* 面板窗口置顶（📌）—— **每个面板窗口各自一个**。
 *
 * 为什么单独一个文件（2026-10-16）：
 *   ① 用户口径：「所有面板包括 home 和所有打开的都加 📌」—— 而各页顶栏结构不一样
 *      （工具页与 home = 顶栏 `div.bar`；设置页 = 顶部 `nav`）；
 *   ② home 上原来那颗 📌 是**假的**（点了只弹一句「「置顶」还没做」）——
 *      14 个页面各写一遍必然写歪，所以只有这一份实现，各页只挂一行 `<script src="ontop.js">`；
 *   ③ 置顶是**按窗口**的（用户 2026-10-16 选的口径：「每个窗口各自」）：
 *      结论 = 点哪一页的 📌 就只顶**那一页那个窗口**。做法是页面把**自己的 `document.title`**
 *      一起上报，服务端按整标题精确定位。
 *
 * ⚠️ 老口径的坑（真机踩到，见 HANDOVER §71）：服务端原来只按「标题含 `ACETool`」找窗口 ⇒
 *   ① 只有 home / 设置 / 老壳页标题里有 `ACETool`，其余页一个都匹配不上；
 *   ② exe **自己控制台**的标题就是 `...\acetool.exe`（含 `acetool`）⇒ 会被当成"面板窗口"去置顶。
 *   现在这两条都由 exe 侧堵住了（`win.Find` 剔除本进程窗口 + 按标题精确定位）。
 *
 * 失败**如实说**：页面上有 toast 就用页面的，没有就自己画一条（不静默）。
 */
(function () {
  'use strict';

  var H = { 'Content-Type': 'application/json', 'X-ACETool': '1' };

  /* 挂载点：**顶栏** `.bar`。
   *   · 工具页与 home 的顶栏是 `<body>` 的直接子元素 ⇒ 用 `body > .bar` 选它；
   *   · 设置页**不能**用它：设置页的 `.bar` 是卡片里那排按钮（「保存并重新注册」），
   *     而且它的 `<nav>` 是**左侧竖排**标签栏（把 📌 挂进去会掉在左栏底下，像个补丁）；
   *   · 老壳页 index.html 不挂这个文件（它那颗 📌 由 shortcuts.js 负责，口径已对齐）。
   * 选不到就退化成窗口右上角**固定按钮**（设置页走的就是这条 —— 它压根没有顶栏）。
   * 用户 2026-10-16 的验收口径：14 个页面**都得有**，而且**长得一模一样**；
   * 一致由 `ontop.js` 自带的那条 `.iconbtn` 规则保证（与 home 的逐字相同）。 */
  var MOUNTS = ['body > .bar'];

  var btn = null;
  var lastErr = '';

  function pageTitle() {
    return (document.title || '').trim() || location.pathname;
  }

  function ensureStyle() {
    if (document.getElementById('ontop-css')) return;
    var s = document.createElement('style');
    s.id = 'ontop-css';
    /* 与 home 的 `.iconbtn` **逐字一致**（用户 2026-10-16：「有的有上下边框颜色有的没有不协调」）
       —— 两份规则值相同，谁后到都一样。`.on` = 已置顶（蓝，与快捷键条那颗同一个色）。 */
    s.textContent =
      '.iconbtn{width:26px;height:26px;display:flex;align-items:center;justify-content:center;' +
      'flex:0 0 auto;border-radius:6px;border:1px solid var(--line,#3f434a);' +
      'background:var(--panel,#2f333a);color:var(--fg,#d6dae0);cursor:pointer;font-size:13px;' +
      'line-height:1;padding:0}' +
      '.iconbtn:hover{border-color:var(--accent,#1a73e8)}' +
      '.iconbtn.on{background:#1d4ed8;border-color:#3b82f6;color:#fff}' +
      '#ontop-pill{position:fixed;left:50%;transform:translateX(-50%);bottom:12px;z-index:99;' +
      'max-width:82%;padding:7px 12px;border-radius:6px;font:12px/1.5 inherit;white-space:pre-wrap;' +
      'word-break:break-all;background:#3a2326;border:1px solid #f08a8a;color:#ffd9d9}';
    document.head.appendChild(s);
  }

  function pill(text) {
    var p = document.getElementById('ontop-pill');
    if (!p) {
      p = document.createElement('div');
      p.id = 'ontop-pill';
      document.body.appendChild(p);
    }
    p.textContent = text;
    p.hidden = false;
    clearTimeout(p._t);
    p._t = setTimeout(function () { p.hidden = true; }, 6000);
  }

  /* 页面自己的提示优先（home / notation / rgb 是 `toast(text, ok)`；shortcuts.js 是 `showToast(kind, text)`）。 */
  function say(ok, text) {
    lastErr = ok ? '' : text; // 成功要**清掉**上一条错误：否则自检/悬停会拿着旧错误当现状
    try {
      if (typeof window.showToast === 'function') { window.showToast(ok ? 'ok' : 'bad', text); return; }
      if (typeof window.toast === 'function') { window.toast(text, !!ok); return; }
    } catch (e) { /* 页面的 toast 炸了不该连累置顶本身，落到下面的兜底 */ }
    if (!ok) pill(text);
  }

  function mount() {
    var existing = document.getElementById('btnOntop');
    if (existing) return existing; // home 已经有那颗（旧的是假的，这里接管成真的）
    for (var i = 0; i < MOUNTS.length; i++) {
      var host = document.querySelector(MOUNTS[i]);
      if (!host) continue;
      var b = document.createElement('button');
      b.className = 'iconbtn';
      b.id = 'btnOntop';
      b.type = 'button';
      b.textContent = '📌'; // 先把字形写上：状态要等一次往返（`refresh`）才有，别让按钮空着闪一下
      b.title = '置顶';
      host.appendChild(b);
      return b;
    }
    /* 没有顶栏的页面（设置页）：固定在窗口右上角。 */
    var f = document.createElement('button');
    f.className = 'iconbtn';
    f.id = 'btnOntop';
    f.type = 'button';
    f.textContent = '📌';
    f.title = '置顶';
    f.style.cssText = 'position:fixed;top:8px;right:8px;z-index:99';
    document.body.appendChild(f);
    return f;
  }

  function paint(d) {
    d = d || {};
    var on = !!d.onTop;
    btn.classList.toggle('on', on);
    btn.textContent = '📌';
    var t = on ? '已置顶（点一下取消）' : '置顶（把这个窗口浮在 ACE 上面）';
    if (d.windowTitle) t += '\n窗口：' + d.windowTitle;
    /* 凭什么认出这个窗口的，如实写在悬停提示里（浏览器会给标题加尾巴，所以多半是 prefix） */
    if (d.match === 'prefix') t += '\n（浏览器给窗口标题加了尾巴 ⇒ 按前缀认的）';
    if (d.match === 'contains') t += '\n（标题被改写过了 ⇒ 按"含这段"认的，未必是你要的那个窗口）';
    if (d.windowMatches > 1) {
      t += '\n⚠️ 同名窗口有 ' + d.windowMatches + ' 个，动的是' +
        (d.picked === 'foreground' ? '**前台那个**' : '先找到的那个');
    }
    if (d.error) t = '置顶问不出来：' + d.error + '\n（点一下再试一次）';
    btn.title = t;
    /* 读不到状态时**不假装**：按钮不亮，标题里写明原因（鼠标悬停就能看到） */
    btn.setAttribute('aria-pressed', on ? 'true' : 'false');
  }

  var busy = false;

  async function call(method, body) {
    if (busy) return null; // 连点两下不该翻两次（第二次的翻转会基于同一个旧状态）
    busy = true;
    btn.disabled = true;
    try {
      var url = '/api/panel/ontop';
      var opt = { cache: 'no-store' };
      if (method === 'POST') {
        opt.method = 'POST';
        opt.headers = H;
        opt.body = JSON.stringify(body || {});
      } else {
        url += '?title=' + encodeURIComponent(pageTitle());
      }
      var r = await fetch(url, opt);
      return await r.json();
    } catch (e) {
      return { error: '请求失败：' + ((e && e.message) || e) };
    } finally {
      busy = false;
      btn.disabled = false;
    }
  }

  async function refresh() {
    var d = await call('GET');
    if (!d) return;
    paint(d);
  }

  async function toggle() {
    var d = await call('POST', { title: pageTitle() });
    if (!d) return;
    paint(d);
    if (d.error) { say(false, '置顶没成功：' + d.error); return; }
    say(true, d.onTop
      ? '已置顶（' + (d.windowTitle || '本窗口') + '）'
      : '已取消置顶');
  }

  function install() {
    ensureStyle();
    btn = mount();
    btn.addEventListener('click', toggle);
    refresh();
    /* 窗口重新获得焦点时对一次账（别人/系统级热键动过这个窗口的话，按钮不会一直骗人） */
    window.addEventListener('focus', refresh);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', install);
  } else {
    install();
  }

  /* 给自检脚本用：能直接问一句"这颗按钮现在什么状态" */
  window.OntopBtn = { refresh: refresh, toggle: toggle, state: function () {
    return { on: btn ? btn.classList.contains('on') : null, title: btn ? btn.title : null,
      lastErr: lastErr, pageTitle: pageTitle() };
  } };
})();
