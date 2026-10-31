/* 面板快捷键 + 窗口置顶 —— 面板自己的两个功能（不是 ACE 工具，所以不进侧栏，放顶栏）。
 *
 * 两层（用户 2026-10-05 定的口径：**两层都做，键位在设置里自己绑**）：
 *
 *   层 1 页面内：这里 `document` 上 keydown 分发。零依赖，但只在浏览器标签前台时有效。
 *   层 2 系统级：exe 调 Windows 的 `RegisterHotKey`，浏览器在后台也响应。
 *               ⚠️ 一个组合键一旦注册成系统级就被 **exe 系统级吞掉**、网页再也收不到它
 *               ⇒ 这里必须**跳过**那些 Global 绑定，否则一次按键跑两遍（最危险的是写工具）。
 *
 * 键位记法与 Go 侧 `internal/hotkeys` 是**同一套契约**（两侧各有测试钉着）：
 *   修饰键固定顺序 `Ctrl+Alt+Shift+Win`，主键用 `e.code` 归一后的名字（见 codeToMain）。
 */
(function () {
  'use strict';

  var HDR = { 'Content-Type': 'application/json', 'X-ACETool': '1' };

  var S = {
    ready: false,
    actions: [],
    bindings: {},   // 动作 id → {key, global}
    registered: [], // 上一次注册结果
    warnings: [],
    conflicts: [],
    windows: false,
    onTop: { onTop: false, want: false, supported: false },
    capturing: null, // 正在采集键位的动作 id
    since: null,     // 事件游标（null = 还没取过 ⇒ 第一次只拿 lastSeq，不刷历史）
    pollTimer: null,
  };

  /* ============================ 键位记法（与 Go 逐字一致） ============================ */

  var NAMED = {
    Enter: 1, Space: 1, Tab: 1, Escape: 1, Backspace: 1, Delete: 1, Insert: 1, Home: 1, End: 1,
    PageUp: 1, PageDown: 1, ArrowUp: 1, ArrowDown: 1, ArrowLeft: 1, ArrowRight: 1,
    BracketLeft: '[', BracketRight: ']', Slash: '/', Backslash: '\\', Semicolon: ';',
    Quote: "'", Comma: ',', Period: '.', Minus: '-', Equal: '=', Backquote: '`',
  };

  /** 把 KeyboardEvent **归一成主键名**。用 `e.code` 而不是 `e.key`：
   *  Windows 上 `Ctrl+Alt+X` 会被当成 AltGr 组合，`e.key` 可能是个怪字符。 */
  function codeToMain(e) {
    var c = e.code || '';
    if (/^Key[A-Z]$/.test(c)) return c.slice(3);
    if (/^Digit[0-9]$/.test(c)) return c.slice(5);
    if (/^Numpad[0-9]$/.test(c)) return c.slice(6);
    if (/^F([1-9]|1[0-9]|2[0-4])$/.test(c)) return c;
    if (NAMED[c] === 1) return c;
    if (typeof NAMED[c] === 'string') return NAMED[c];
    // `?` 是 Shift+/ 的产物：用户按出来的就是这个字符，规范主键名只认 `?`
    if (e.key === '?') return '?';
    if (e.key && e.key.length === 1) return e.key.toUpperCase();
    return c || (e.key || '');
  }

  /** 规范形：修饰键固定 Ctrl+Alt+Shift+Win，末段主键。 */
  function normalize(e) {
    var parts = [];
    if (e.ctrlKey) parts.push('Ctrl');
    if (e.altKey) parts.push('Alt');
    if (e.shiftKey) parts.push('Shift');
    if (e.metaKey) parts.push('Win');
    var m = codeToMain(e);
    if (!m) return '';
    // 纯修饰键单独按下时不是一条完整绑定
    if (/^(Control|Alt|Shift|Meta)(Left|Right)$/.test(e.code || '')) return '';
    parts.push(m);
    return parts.join('+');
  }

  /** 与 Go 的 `reserved` 表对应：浏览器/系统先吃掉的组合**绑了也不会响**，当场说清。 */
  var RESERVED = {
    'Ctrl+W': '关标签页（浏览器）', 'Ctrl+T': '新标签页（浏览器）', 'Ctrl+N': '新窗口（浏览器）',
    'Ctrl+R': '刷新（浏览器）', 'Ctrl+Shift+R': '强制刷新（浏览器）', 'Ctrl+Shift+W': '关窗口（浏览器）',
    'Ctrl+Shift+T': '恢复标签页（浏览器）', 'Ctrl+Tab': '切换标签页（浏览器）', 'Ctrl+Shift+Tab': '切换标签页（浏览器）',
    'Ctrl+L': '定位地址栏（浏览器）', 'Ctrl+D': '收藏本站（浏览器）', 'Ctrl+P': '打印（浏览器）',
    'Ctrl+S': '保存页面（浏览器）', 'Ctrl+O': '打开文件（浏览器）', 'Ctrl+H': '历史（浏览器）',
    'Ctrl+J': '下载（浏览器）', 'Ctrl+U': '查看源码（浏览器）', 'Ctrl+F': '页内查找（浏览器）',
    'Alt+F4': '关窗口（系统）', 'Alt+Tab': '切换窗口（系统）', 'F5': '刷新（浏览器）',
    'F11': '全屏（浏览器）', 'F12': '开发者工具（浏览器）', 'Win+L': '锁屏（系统）', 'Win+D': '显示桌面（系统）',
  };

  /** 本地先校一遍（Go 侧还会再校一次，两边判据一致；这里是为了**立刻**给反馈）。 */
  function validate(key, global) {
    if (!key) return '';
    if (RESERVED[key]) return '`' + key + '` 被' + RESERVED[key] + '占着，绑了也不会响 ⇒ 换一个';
    var hasMod = /(^|\+)(Ctrl|Alt|Shift|Win)(\+|$)/.test(key);
    var main = key.indexOf('+') >= 0 ? key.slice(key.lastIndexOf('+') + 1) : key;
    if (global && !hasMod) return '系统级热键 `' + key + '` 没有修饰键 —— 那会把裸按键从整个系统抢走（打字都受影响）⇒ 加上 Ctrl/Alt';
    if (!hasMod) {
      // 裸键只放行 F1-F24 与 `?` `[` `]`：**打字时我们本来就不抢键**（见 onKeyDown 的 isTyping）
      var bare = /^F([1-9]|1[0-9]|2[0-4])$/.test(main) || main === '?' || main === '[' || main === ']';
      if (!bare) return '`' + key + '` 没有修饰键，会挡住输入框里打字 ⇒ 加上 Ctrl/Alt（F1-F24 与 `?` `[` `]` 可以不修饰）';
    }
    return '';
  }

  /* ============================ 样式（自带，跟 notation.js 一个路子） ============================ */

  var STYLE_ID = 'sc-style';
  function ensureStyle() {
    if (document.getElementById(STYLE_ID)) return;
    var css = [
      '.sccard{border:1px solid var(--line,#2a3140);border-radius:8px;margin:10px 0;background:var(--panel,#161b24)}',
      '.sccard h3{margin:0;padding:10px 12px;font-size:13px;border-bottom:1px solid var(--line,#2a3140);opacity:.85}',
      '.scrow{display:grid;grid-template-columns:1fr 190px 70px 1fr;gap:8px;align-items:center;padding:7px 12px;border-bottom:1px solid rgba(255,255,255,.05)}',
      '.scrow:last-child{border-bottom:0}',
      '.scrow .lbl{font-size:13px}',
      '.scrow .what{font-size:11px;opacity:.6;line-height:1.45}',
      '.sckey{font:600 12px/1 ui-monospace,Consolas,monospace;background:#0f131a;border:1px solid #2b3purple;border:1px solid #2b3648;color:#dbe7ff;border-radius:6px;padding:5px 8px;cursor:pointer;text-align:center;min-height:26px}',
      '.sckey:hover{border-color:#5b91ff}',
      '.sckey.capturing{border-color:#ffc46b;color:#ffc46b}',
      '.sckey.none{color:#67748c}',
      '.sckey.bad{border-color:#ff7a7a;color:#ff8f8f}',
      '.scg{font-size:11px;opacity:.75;display:flex;gap:4px;align-items:center;cursor:pointer;user-select:none}',
      '.scrow.off .lbl,.scrow.off .what{opacity:.35}',
      '.scbar{display:flex;gap:8px;align-items:center;padding:10px 12px;border-top:1px solid var(--line,#2a3140);flex-wrap:wrap}',
      '.scerr{color:#ff8f8f;font-size:12px;padding:0 12px 8px}',
      '.scwarn{color:#ffc46b;font-size:12px;padding:2px 12px}',
      '.scok{color:#7ee0a0;font-size:12px;padding:2px 12px}',
      '.scnote{font-size:12px;opacity:.7;padding:8px 12px;line-height:1.6}',
      '#scmodal{position:fixed;inset:0;background:rgba(6,9,14,.72);z-index:60;display:flex;align-items:flex-start;justify-content:center;padding:40px 16px;overflow:auto}',
      '#scmodal .scbox{background:#12161e;border:1px solid #2a3140;border-radius:10px;max-width:1020px;width:100%;box-shadow:0 18px 50px rgba(0,0,0,.5)}',
      '.scmodalhead{display:flex;justify-content:space-between;align-items:center;padding:12px 14px;border-bottom:1px solid #2a3140}',
      '.scmodalhead b{font-size:14px}',
      '.ontopbtn.on{background:#1d4ed8;border-color:#3b82f6;color:#fff}',
    ].join('');
    var st = document.createElement('style');
    st.id = STYLE_ID;
    st.textContent = css;
    document.head.appendChild(st);
  }

  /* ============================ 设置面板（模态） ============================ */

  function modalHTML() {
    var groups = [], by = {};
    S.actions.forEach(function (a) {
      if (!by[a.group]) { by[a.group] = []; groups.push(a.group); }
      by[a.group].push(a);
    });
    var html = '<div id="scmodal"><div class="scbox">' +
      '<div class="scmodalhead"><b>快捷键设置</b>' +
      '<span><button class="btn ghost" id="screset" type="button">恢复出厂键位</button> ' +
      '<button class="btn ghost" id="scclose" type="button">关闭</button></span></div>';
    html += '<div class="scnote">' +
      '点右边的键位格 → 直接按你要的组合键（<code>Esc</code> 取消、<code>Backspace</code> 清空）。' +
      '勾上「全局」= 交给 exe 做**系统级**注册：**浏览器在后台也响应**（人在 ACE 里就能按），' +
      '代价是这个组合键被 exe 系统级吞掉、网页收不到它（所以别把同一个键既当页面键又当全局键）。' +
      (S.windows ? '' : '<br>⚠️ 本机不是 Windows ⇒ 系统级注册没做，勾了也只会如实报错。') +
      '</div>';
    groups.forEach(function (g) {
      html += '<div class="sccard"><h3>' + esc(g) + '</h3>';
      by[g].forEach(function (a) {
        var b = S.bindings[a.id] || { key: '', global: false };
        var reg = S.registered.filter(function (r) { return r.action === a.id; })[0];
        var cls = 'sckey' + (b.key ? '' : ' none');
        var stat = '';
        if (b.global && reg) stat = reg.ok ? '✅ 已注册' : '❌ ' + esc(reg.error || '注册失败');
        var dflt = a.defaultKey || '（出厂未绑）';
        html += '<div class="scrow' + (b.key ? '' : ' off') + '" data-row="' + esc(a.id) + '">' +
          '<div><div class="lbl">' + esc(a.label) +
          (a.canGlobal ? '' : ' <span class="dim" style="font-size:11px">（仅面板内）</span>') + '</div>' +
          '<div class="what">' + esc(a.what) + '<br>出厂：<code>' + esc(dflt) + '</code></div></div>' +
          '<div class="' + cls + '" data-cap="' + esc(a.id) + '">' + (b.key ? esc(b.key) : '（未绑）') + '</div>' +
          '<label class="scg" title="勾上 = 交给 exe 系统级注册（浏览器在后台也响应）">' +
          '<input type="checkbox" data-glob="' + esc(a.id) + '"' + (b.global ? ' checked' : '') +
          (a.canGlobal ? '' : ' disabled') + '>全局</label>' +
          '<div class="what" style="opacity:.8">' + stat + '</div>' +
          '</div>';
      });
      html += '</div>';
    });
    html += '<div id="scerr"></div>';
    html += '<div class="scbar"><button class="btn" id="scsave" type="button">保存并重新注册</button>' +
      '<span class="dim">保存会**立刻**重注册系统级热键（不用重启面板）</span></div>';
    if (S.warnings && S.warnings.length) {
      html += '<div class="scwarn">' + S.warnings.map(esc).join('<br>') + '</div>';
    }
    if (S.conflicts && S.conflicts.length) {
      html += '<div class="scwarn">⚠️ ' + S.conflicts.map(esc).join('<br>') + '</div>';
    }
    html += '</div></div>';
    return html;
  }

  function openSettings() {
    ensureStyle();
    var host = document.createElement('div');
    host.id = 'schost';
    host.innerHTML = modalHTML();
    document.body.appendChild(host);
    bindModal(host);
    refresh();
  }

  function closeSettings() {
    var h = document.getElementById('schost');
    if (h) h.remove();
    S.capturing = null;
  }

  function bindModal(host) {
    host.querySelector('#scclose').addEventListener('click', closeSettings);
    host.addEventListener('click', function (e) { if (e.target === host.querySelector('#scmodal')) closeSettings(); });
    host.querySelector('#screset').addEventListener('click', function () {
      if (!window.confirm('把所有键位恢复到出厂设置？（只改绑定，不动任何工程数据）')) return;
      var nb = {};
      S.actions.forEach(function (a) {
        var d = S.defaults[a.id];
        if (d) nb[a.id] = { key: d.key, global: !!d.global };
      });
      S.bindings = nb;
      var h = document.getElementById('schost');
      h.innerHTML = modalHTML(); bindModal(h);
    });
    host.querySelectorAll('[data-cap]').forEach(function (el) {
      el.addEventListener('click', function () {
        S.capturing = el.getAttribute('data-cap');
        host.querySelectorAll('.sckey').forEach(function (x) { x.classList.remove('capturing'); });
        el.classList.add('capturing');
        el.textContent = '按下组合键…';
      });
    });
    host.querySelectorAll('[data-glob]').forEach(function (el) {
      el.addEventListener('change', function () {
        var id = el.getAttribute('data-glob');
        var b = S.bindings[id] || { key: '', global: false };
        var err = validate(b.key, el.checked);
        if (err) {
          // 勾了全局但键位不合格 ⇒ **不静默改**，当场说清并撤回勾选
          flashErr({ id: err });
          el.checked = false;
          return;
        }
        b.global = el.checked;
        S.bindings[id] = b;
        rerenderRows();
      });
    });
    host.querySelector('#scsave').addEventListener('click', save);
  }

  function rerenderRows() {
    var h = document.getElementById('schost');
    if (!h) return;
    var box = h.querySelector('.scbox');
    box.innerHTML = modalHTML().replace(/^<div id="scmodal"><div class="scbox">/, '').replace(/<\/div><\/div>$/, '');
    // 重新绑事件（整块替换后旧监听都没了）
    bindModal(h.querySelector('.scbox') ? h : h);
  }

  function flashErr(errors) {
    var el = document.getElementById('scerr');
    if (!el) return;
    el.innerHTML = '<div class="scerr">⚠️ ' + Object.keys(errors).map(function (k) {
      return esc(errors[k]);
    }).join('<br>') + '</div>';
  }

  /* ============================ 与后端来回 ============================ */

  async function load() {
    try {
      var r = await fetch('/api/shortcuts', { cache: 'no-store' });
      var d = await r.json();
      applyData(d);
      S.ready = true;
      return d;
    } catch (e) {
      S.ready = false;
      return null;
    }
  }

  function applyData(d) {
    S.actions = d.actions || [];
    S.bindings = d.bindings || {};
    S.defaults = d.defaults || {};
    S.registered = d.registered || [];
    S.warnings = d.warnings || [];
    S.conflicts = d.conflicts || [];
    S.windows = !!d.windows;
    S.onTop = d.onTop || S.onTop;
    paintOnTop();
    loadOnTop(); // 置顶是**按窗口**的 ⇒ 本窗口的状态得按**自己的标题**去问（见 HANDOVER §71）
  }

  async function refresh() {
    var d = await load();
    if (d && document.getElementById('schost')) {
      var h = document.getElementById('schost');
      h.innerHTML = modalHTML();
      bindModal(h);
    }
  }

  async function save() {
    var payload = { bindings: S.bindings };
    var errs = {};
    Object.keys(S.bindings).forEach(function (id) {
      var b = S.bindings[id];
      var e = validate(b.key, b.global);
      if (e) errs[id] = e;
    });
    if (Object.keys(errs).length) { flashErr(errs); return; }
    try {
      var r = await fetch('/api/shortcuts', { method: 'POST', headers: HDR, body: JSON.stringify(payload) });
      var d = await r.json();
      if (d.errors) { flashErr(d.errors); return; }
      applyData(d);
      var h = document.getElementById('schost');
      if (h) { h.innerHTML = modalHTML(); bindModal(h); }
      var bad = (d.registered || []).filter(function (x) { return x.error; });
      var el = document.getElementById('scerr');
      if (el) {
        el.innerHTML = bad.length
          ? '<div class="scerr">⚠️ 有 ' + bad.length + ' 条**没注册上**：<br>' +
            bad.map(function (x) { return esc(x.key + ' → ' + x.error); }).join('<br>') + '</div>'
          : '<div class="scok">已保存，系统级热键已按新表重新注册。</div>';
      }
    } catch (e) {
      flashErr({ net: '保存失败：' + ((e && e.message) || e) });
    }
  }

  /* ============================ 窗口置顶 ============================ */

  /* 问**本窗口**的置顶状态：带自己的 `document.title`。
     为什么不直接用 `/api/shortcuts` 里那份 `onTop`：那份走的是"标题**含** `ACETool`"的老路，
     老壳页标题里正好有 `ACETool` 所以看着是对的，但多开几个面板窗口时可能认到别人身上
     （2026-10-16 查出来的真机问题：exe 自己的控制台标题含 `acetool`，也被当成面板窗口）。 */
  async function loadOnTop() {
    try {
      var r = await fetch('/api/panel/ontop?title=' + encodeURIComponent(document.title),
        { cache: 'no-store' });
      S.onTop = await r.json();
      paintOnTop();
    } catch (e) { /* 问不到就保留上一次的样子 */ }
  }

  function paintOnTop() {
    var b = document.getElementById('ontop');
    if (!b) return;
    var on = !!(S.onTop && S.onTop.onTop);
    b.classList.toggle('on', on);
    b.textContent = on ? '📌 已置顶' : '📌 置顶';
    b.title = S.onTop && S.onTop.windowTitle
      ? '面板窗口：' + S.onTop.windowTitle
      : '把面板所在的浏览器窗口浮在最上面（在 ACE 里干活也能看见面板）';
  }

  async function toggleOnTop() {
    var b = document.getElementById('ontop');
    if (b) b.disabled = true;
    try {
      var r = await fetch('/api/panel/ontop', { method: 'POST', headers: HDR, body: JSON.stringify({ title: document.title }) });
      var d = await r.json();
      if (!d.ok) {
        // 失败就说清原因（找不到窗口 / 权限不同 / 前台锁），不假装按了
        showToast('bad', '置顶没成功：' + (d.error || '未知原因'));
        S.onTop = d;
      } else {
        S.onTop = d;
        showToast('ok', d.onTop ? '面板窗口已置顶（' + (d.windowTitle || '') + '）' : '已取消置顶');
      }
      paintOnTop();
    } catch (e) {
      showToast('bad', '置顶请求失败：' + ((e && e.message) || e));
    } finally {
      if (b) b.disabled = false;
    }
  }

  /* ============================ 页面内分发（层 1） ============================ */

  /** 全局绑定的组合键要被**跳过**（exe 已经系统级吞掉了它们；这里再处理一次会跑两遍）。 */
  function globalKeys() {
    var out = {};
    Object.keys(S.bindings).forEach(function (id) {
      var b = S.bindings[id];
      if (b && b.global && b.key) out[b.key] = true;
    });
    return out;
  }

  function actionOf(key) {
    var hit = null;
    Object.keys(S.bindings).forEach(function (id) {
      var b = S.bindings[id];
      if (b && b.key === key && !b.global) hit = id;
    });
    return hit;
  }

  function isTyping(el) {
    if (!el) return false;
    if (el.isContentEditable) return true;
    return /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName || '');
  }

  function onKeyDown(e) {
    // 正在采集键位
    if (S.capturing) {
      e.preventDefault();
      e.stopPropagation();
      var id = S.capturing;
      if (e.key === 'Escape') { S.capturing = null; refresh(); return; }
      var b = S.bindings[id] || { key: '', global: false };
      if (e.key === 'Backspace') {
        b.key = '';
        S.bindings[id] = b;
        S.capturing = null;
        refresh();
        return;
      }
      var key = normalize(e);
      if (!key) return; // 只按了修饰键：继续等
      var err = validate(key, b.global);
      if (err) { flashErr({ k: err }); return; }
      b.key = key;
      S.bindings[id] = b;
      S.capturing = null;
      refresh();
      return;
    }

    var key = normalize(e);
    if (!key) return;
    if (globalKeys()[key]) return; // 交给 exe（它系统级吞掉了，网页本来也收不到）

    // 光标在输入框里时，只放行带 Ctrl/Alt 的组合（否则打字会触发工具）
    var typing = isTyping(e.target);
    var hasMod = /(^|\+)(Ctrl|Alt|Win)(\+|$)/.test(key);
    if (typing && !hasMod) return;

    var id = actionOf(key);
    if (!id) {
      // 谱面编辑器自己有一批局部快捷键（←→/↑↓/1-7/Delete）—— 没绑动作时**不要抢**
      return;
    }
    e.preventDefault();
    runAction(id);
  }

  function runAction(id) {
    var H = window.ShortcutHost;
    if (!H) { showToast('bad', '面板还没准备好（ShortcutHost 缺失）'); return; }
    switch (id) {
      case 'run.primary': H.runPrimary(); break;
      case 'run.dry': H.runPrimary(); break;   // 页面里不强制干跑（那是全局键的语义）；页面内等价于"跑一次"
      case 'tool.prev': H.toolPrev(); break;
      case 'tool.next': H.toolNext(); break;
      case 'item.prev': H.itemPrev(); break;
      case 'item.next': H.itemNext(); break;
      case 'dryrun.toggle': H.toggleDry(); break;
      case 'log.clear': H.clearLog(); break;
      case 'settings.shortcuts': openSettings(); break;
      case 'help.shortcuts': showHelp(); break;
      case 'panel.focus': showToast('info', '面板已经在前台了 🙂'); break;
      case 'panel.ontop': toggleOnTop(); break;
      default:
        if (/^group\.(\d+)$/.test(id)) H.gotoGroup(parseInt(id.slice(6), 10));
        break;
    }
  }

  /* ============================ 提示与清单 ============================ */

  function showToast(kind, text) {
    var el = document.createElement('div');
    el.className = 'hint ' + (kind === 'bad' ? 'bad' : 'ok');
    el.style.cssText = 'position:fixed;right:16px;bottom:64px;z-index:70;max-width:460px;box-shadow:0 8px 24px rgba(0,0,0,.4)';
    el.textContent = text;
    document.body.appendChild(el);
    setTimeout(function () { el.remove(); }, kind === 'bad' ? 9000 : 5000);
  }

  function showHelp() {
    ensureStyle();
    var lines = S.actions.map(function (a) {
      var b = S.bindings[a.id] || {};
      var host = document.getElementById('schost');
      if (!b.key) return null;
      return '<div class="scrow"><div class="lbl">' + esc(a.label) + '</div>' +
        '<div class="sckey">' + esc(b.key) + (b.global ? ' 🌐' : '') + '</div><div></div>' +
        '<div class="what">' + esc(a.what) + '</div></div>';
    }).filter(Boolean);
    var host = document.createElement('div');
    host.id = 'schost';
    host.innerHTML = '<div id="scmodal"><div class="scbox">' +
      '<div class="scmodalhead"><b>快捷键清单</b><span>' +
      '<button class="btn ghost" id="schelp-editsc" type="button">去设置</button> ' +
      '<button class="btn ghost" id="schelp-close" type="button">关闭</button></span></div>' +
      '<div class="scnote">🌐 = 系统级（浏览器在后台也响应）。' +
      (Object.keys(S.bindings).length ? '' : '（一条都还没载入 —— 先等一下或看设置页）') + '</div>' +
      lines.join('') + '</div></div>';
    document.body.appendChild(host);
    host.querySelector('#schelp-close').addEventListener('click', closeSettings);
    host.querySelector('#schelp-editsc').addEventListener('click', function () { closeSettings(); openSettings(); });
    host.addEventListener('click', function (e) { if (e.target === host.querySelector('#scmodal')) closeSettings(); });
  }

  /* ============================ 事件轮询（系统级热键的结果） ============================ */

  /**
   * 系统级热键在浏览器后台按下去时，页面完全不知情 ⇒ 得有地方把结果捞回来。
   *
   * ⭐ **按序号游标增量取**（用户 2026-10-05 选的做法：多窗口友好）：
   *   · 第一次**不带 `since`** ⇒ 服务端只回 `lastSeq`、不回事件（新窗口不会把历史事件刷一屏提示）；
   *   · 之后每次带 `since=<上次的 lastSeq>`，取 `Seq > since` 的那些；
   *   · 事件**不被取走**（服务端环形缓冲）⇒ 开两个窗口，两边都收得到同一条
   *     （原来的"取走即清"会是谁先轮询谁抢走）。
   *   · 缓冲淘汰过的话服务端会给 `missed`，如实说出来、别默默当没发生。
   *
   * 用轮询而不是 SSE：本地单用户、事件稀疏，断线不用重连逻辑（而且只在页面可见时轮询）。
   */
  async function pollEvents() {
    if (document.hidden) return;
    try {
      var url = '/api/events' + (S.since == null ? '' : '?since=' + S.since);
      var r = await fetch(url, { cache: 'no-store' });
      var d = await r.json();
      (d.events || []).forEach(function (ev) {
        showToast(ev.kind === 'bad' ? 'bad' : 'ok', (ev.action ? '[' + ev.action + '] ' : '') + ev.text);
        if (window.ShortcutHost && ShortcutHost.pushLog) ShortcutHost.pushLog(ev);
      });
      if (d.missed) showToast('info', '有 ' + d.missed + ' 条热键消息因面板久未打开已被淘汰（缓冲 ' + 200 + ' 条）');
      if (typeof d.lastSeq === 'number') S.since = d.lastSeq;
    } catch (e) { /* 面板关了就关了吧 */ }
  }

  /* ============================ 顶栏按钮与启动 ============================ */

  function installTopbar() {
    var bar = document.querySelector('.topbar .status');
    if (!bar || document.getElementById('ontop')) return;
    var mk = function (id, text, title) {
      var b = document.createElement('button');
      b.className = 'btn ghost';
      b.id = id;
      b.type = 'button';
      b.textContent = text;
      b.title = title;
      return b;
    };
    var onTop = mk('ontop', '📌 置顶', '把面板所在的浏览器窗口浮在最上面');
    var sc = mk('scbtn', '⌨ 快捷键', '快捷键设置（也可以按 F2）');
    onTop.addEventListener('click', toggleOnTop);
    sc.addEventListener('click', openSettings);
    bar.appendChild(onTop);
    bar.appendChild(sc);
    paintOnTop();
  }

  document.addEventListener('keydown', onKeyDown, true); // 捕获阶段：先于各页自己的监听

  document.addEventListener('DOMContentLoaded', function () {
    ensureStyle();
    installTopbar();
    load();
    S.pollTimer = setInterval(pollEvents, 2000);
    pollEvents();
    // 深链：`#shortcuts` 直接开设置页（给用户存书签；也让无头截图能拍到这一页核对版式）
    if ((location.hash || '').replace(/^#/, '') === 'shortcuts') openSettings();
  });

  // 给 app.js / 自检脚本用
  window.ShortcutPanel = {
    open: openSettings, load: load, state: S, normalize: normalize, validate: validate,
    codeToMain: codeToMain, runAction: runAction, toast: showToast,
  };
})();
