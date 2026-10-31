/* 工程缩略图面板 —— **ACE 编排视图的模拟图**（手写 SVG，零依赖）。
 *
 * 用户 2026-10-05 的口径：
 *   · 「绘制一个轨道和 clip 相对位置的缩略图，如果能绘制 note 就绘制，不能就算了」
 *   · 「可以方便选中 clip」+「**是在 web 端选中片段**，用于某些操作范围的选择，
 *      **选中状态不影响 ace 侧**」
 *
 * ⇒ 所以这里**一个字节都不往 ACE 写**：点片段只改本面板的高亮 + `window.PanelSelection`
 *   （存 sessionStorage，换页也在）。ACE 那边的"当前片段/钢琴窗"完全不动。
 *
 * 图上有什么：左侧轨名栏 · 顶部小节标尺 · 一条轨一行 · 片段按 `clipBegin/clipEnd` 摆位
 * （颜色用 ACE 自己那份 `clipColor`）· 片段内**画音符**（音高→行内高度，pos/dur→横向）· 播放头。
 * ⚠️ 音符是**按预算**读回来的（默认 24 个片段）；没读到的片段画成斜纹并标 ⚠，且**计数报出来**。
 */
(function () {
  'use strict';

  var GUTTER = 132;      // 左侧轨名栏宽
  var RULER = 22;        // 顶部小节标尺高
  var ROW = 36;          // 每轨行高
  var PAD = 3;           // 片段与行边距
  var TICK_PER_BAR = 1920; // 4/4 一小节（480 PPQ）——拿不到拍号时的兜底
  var STORE_KEY = 'acetool.panelSelection';

  var S = {
    data: null,
    zoom: 1,
    fit: true,
    showNotes: true,
    sel: null,      // 面板内选中（**不发给 ACE**）
    busy: false,
    err: '',
  };

  /* ============================ 选中状态（面板内） ============================ */

  function loadSel() {
    try { return JSON.parse(sessionStorage.getItem(STORE_KEY) || 'null'); } catch (e) { return null; }
  }
  function saveSel(v) {
    S.sel = v;
    try {
      if (v) sessionStorage.setItem(STORE_KEY, JSON.stringify(v));
      else sessionStorage.removeItem(STORE_KEY);
    } catch (e) { /* 隐身模式之类，忽略 */ }
    window.dispatchEvent(new CustomEvent('acetool-selection', { detail: v }));
  }
  function selKey(c) { return c.trackIndex + '/' + c.clipIndex; }

  /** 给别的页面/工具用的**面板内范围**接口。 */
  window.PanelSelection = {
    get: function () { return S.sel; },
    clear: function () { saveSel(null); paintSel(); },
    /** 一句话描述，给别处显示用。 */
    label: function () {
      var v = S.sel;
      if (!v) return '';
      return '轨 ' + v.trackIndex + '「' + v.trackName + '」· 片段 ' + v.clipIndex + '「' + v.name + '」';
    },
  };

  /* ============================ 工具 ============================ */

  function esc2(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }
  function r2(v) { return Math.round(v * 100) / 100; }
  function clamp(v, a, b) { return Math.max(a, Math.min(b, v)); }
  /** 拍 = tick / 480（⚠️ 单位口径：用户 2026-10-14 —— 对外一律「拍」，tick 不出现在文字里）。 */
  function beatOf(tick) {
    if (typeof tick !== 'number') return tick;
    return String(Math.round((tick / 480) * 100) / 100);
  }
  function barTicks() {
    var m = /^(\d+)\/(\d+)$/.exec((S.data && S.data.meter) || '');
    if (!m) return TICK_PER_BAR;
    return 480 * 4 * (parseInt(m[1], 10) / parseInt(m[2], 10));
  }
  /** ACE 的 tick 单位：**480 PPQ**（每四分音符 480 tick ⇒ 1 拍 = 480 tick；`tick/秒` 随 BPM 漂）。 */
  function tickToSec(t) {
    var bpm = (S.data && S.data.tempo) || 120;
    return t / 480 * (60 / bpm);
  }

  /* ============================ 面板 HTML ============================ */

  var STYLE_ID = 'mm-style';
  function ensureStyle() {
    if (document.getElementById(STYLE_ID)) return;
    var css = [
      '.mmbar{display:flex;flex-wrap:wrap;gap:6px;align-items:center;padding:8px;border-bottom:1px solid var(--line,#2a3140)}',
      '.mmframe{border:1px solid var(--line,#2a3140);border-radius:8px;margin:10px 0;background:var(--panel,#161b24)}',
      '.mmscroll{overflow:auto;max-height:560px;background:#282c33}',
      '.mmscroll svg{display:block}',
      '.mmclip{cursor:pointer}',
      '.mmclip .hit{fill:transparent}',
      '.mmclip:hover .body{stroke:#ffffffcc;stroke-width:1.5}',
      '.mmsel{outline:none}',
      '.mmselbox{fill:none;stroke:#ffd166;stroke-width:2;stroke-dasharray:4 3}',
      '.mminfo{display:flex;gap:10px;align-items:center;padding:8px 10px;font-size:12px;border-top:1px solid var(--line,#2a3140);flex-wrap:wrap}',
      '.mmchip{background:#233046;color:#9dc0ff;border-radius:10px;padding:2px 8px;font-size:12px}',
      '.mmchip.sel{background:#4a3a12;color:#ffd166}',
      '.mmwarn{color:#ffc46b}',
      '.mmdim{opacity:.62}',
      '.mmleg{font-size:12px;padding:6px 10px;line-height:1.6;opacity:.72}',
    ].join('');
    var st = document.createElement('style');
    st.id = STYLE_ID;
    st.textContent = css;
    document.head.appendChild(st);
  }

  function panelHTML() {
    return '' +
      '<div class="mmframe">' +
      '<div class="mmbar">' +
      '<button class="btn" id="mmrefresh" type="button">读取工程</button>' +
      '<button class="btn ghost" id="mmfit" type="button">适配宽度</button>' +
      '<button class="btn ghost" id="mmzin" type="button">放大 +</button>' +
      '<button class="btn ghost" id="mmzout" type="button">缩小 −</button>' +
      '<label class="scg" style="font-size:12px;display:flex;gap:4px;align-items:center">' +
      '<input type="checkbox" id="mmnotes" checked>显示音符</label>' +
      '<span class="mmchip" id="mmstat">还没读</span>' +
      '<button class="btn ghost" id="mmclear" type="button">清空选中</button>' +
      '</div>' +
      '<div class="mmscroll" id="mmscroll"><div class="mmdim" style="padding:16px">点「读取工程」把编排区画出来。</div></div>' +
      '<div class="mminfo"><span class="mmchip sel" id="mmsel">（面板内未选中片段）</span>' +
      '<span class="mmdim">↑ 这个选中**只在面板里**，不影响 ACE 侧（不改当前片段、不动钢琴窗、不写东西）</span></div>' +
      '<div class="mmleg" id="mmleg"></div>' +
      '</div>' +
      '<div id="mmout"></div>';
  }

  /* ============================ 画图 ============================ */

  /**
   * 一份 minimap 数据 → 一段 SVG（**纯函数**；本面板与「查找」页的结果预览共用）。
   *
   * `opt`：
   *   · `box`        量可用宽度用（默认本面板的 `#mmscroll`）
   *   · `showNotes`  片段里画不画音符（默认跟本面板的勾选）
   *   · `sel`        面板内选中片段（默认本面板的 `S.sel`）
   *   · `fit`/`zoom` 缩放（默认跟本面板）
   *   · `marks`      **高亮标记** `[{tick, track, cur, title}]` —— 查找命中用它标位置：
   *                  在该轨那一行画一条竖虚线（当前项更亮 + 顶部小三角）。
   *   · `noteStyle`  `'bar'`（默认，实心小条）或 `'outline'`（**音符轮廓**：只描边不填充，
   *                  查找页的预览用这一种 —— 用户 2026-10-08「minimap显示音符轮廓」）。
   * 返回 `{html, w, h}`。
   */
  function svgHTML(d, opt) {
    opt = opt || {};
    var box = opt.box || document.getElementById('mmscroll');
    var showNotes = opt.showNotes != null ? opt.showNotes : S.showNotes;
    var sel = opt.sel !== undefined ? opt.sel : S.sel;
    var marks = opt.marks || [];
    var outline = opt.noteStyle === 'outline';
    var fit = opt.fit != null ? opt.fit : S.fit;
    var zoom = opt.zoom != null ? opt.zoom : S.zoom;

    var t0 = d.firstTick || 0;
    var t1 = Math.max(d.lastTick || 0, t0 + 480);
    var span = t1 - t0;
    var avail = Math.max(240, ((box && box.clientWidth) || 1200) - GUTTER - 16);
    var scale = fit ? avail / span : (avail / span) * zoom;
    var w = GUTTER + span * scale + 8;
    var h = RULER + d.tracks.length * ROW + 6;
    var bt = barTicks();

    function X(t) { return r2(GUTTER + (t - t0) * scale); }

    var p = [];
    p.push('<svg xmlns="http://www.w3.org/2000/svg" width="' + r2(w) + '" height="' + h + '" viewBox="0 0 ' + r2(w) + ' ' + h + '">');
    p.push('<rect width="100%" height="100%" fill="#282c33"/>');

    // ── 顶部小节标尺：每 bar 一条细线，每 N bar 标号
    var bars = Math.ceil(span / bt);
    var labelEvery = Math.max(1, Math.ceil(bars / Math.max(1, Math.floor(avail / 46))));
    for (var b = 0; b <= bars; b++) {
      var t = t0 + b * bt;
      if (t > t1) break;
      var x = X(t);
      var major = b % labelEvery === 0;
      p.push('<line x1="' + x + '" y1="' + (major ? 0 : RULER - 6) + '" x2="' + x + '" y2="' + h + '" stroke="' +
        (major ? '#3f434a' : '#383c43') + '" stroke-width="1"/>');
      if (major) {
        p.push('<text x="' + r2(x + 3) + '" y="14" fill="#9498a0" font-size="10">' + (b + 1) + '</text>');
      }
    }
    p.push('<line x1="0" y1="' + RULER + '" x2="' + r2(w) + '" y2="' + RULER + '" stroke="#3f434a"/>');

    // ── 每条轨
    d.tracks.forEach(function (tr, ti) {
      var top = RULER + ti * ROW;
      var cy = top + ROW / 2;
      p.push('<rect x="0" y="' + top + '" width="' + r2(w) + '" height="' + ROW + '" fill="' +
        (ti % 2 ? '#2d3138' : '#30343b') + '"/>');
      // 轨名栏（截断，避免长名字压到片段时间轴上）
      var nm = tr.name || ('轨 ' + tr.index);
      p.push('<rect x="0" y="' + top + '" width="' + GUTTER + '" height="' + ROW + '" fill="#32363d"/>');
      p.push('<text x="8" y="' + r2(cy - 1) + '" fill="#d6dae0" font-size="12">' +
        esc2(nm.length > 14 ? nm.slice(0, 13) + '…' : nm) + '</text>');
      p.push('<text x="8" y="' + r2(cy + 12) + '" fill="#9498a0" font-size="10">' +
        esc2('#' + tr.index + ' · ' + (tr.type || '?') + (tr.soundSource ? ' · ' + tr.soundSource : '')) + '</text>');
      p.push('<line x1="' + GUTTER + '" y1="' + top + '" x2="' + GUTTER + '" y2="' + (top + ROW) + '" stroke="#3f434a"/>');

      (tr.clips || []).forEach(function (c) {
        var x = X(c.begin), x2 = X(c.end);
        var cw = Math.max(2, x2 - x);
        var fill = c.color || '#859C3F';
        var key = ti + '/' + c.index;
        var selected = sel && sel.trackIndex === tr.index && sel.clipIndex === c.index;
        p.push('<g class="mmclip" data-ti="' + tr.index + '" data-ci="' + c.index + '" data-key="' + key + '">');
        p.push('<rect class="body" x="' + x + '" y="' + (top + PAD) + '" width="' + r2(cw) + '" height="' + (ROW - 2 * PAD) +
          '" rx="3" fill="' + esc2(fill) + '" fill-opacity="0.92" stroke="#00000055"/>');
        // 没读音符的：斜纹 + ⚠（如实标出来）
        if (c.notesOmitted) {
          p.push('<rect x="' + x + '" y="' + (top + PAD) + '" width="' + r2(cw) + '" height="' + (ROW - 2 * PAD) +
            '" rx="3" fill="url(#mmHatch)" fill-opacity="0.85"/>');
        }
        // 片段名（宽度够才画）
        if (cw > 26) {
          var label = c.name || ('clip ' + c.index);
          var maxCh = Math.max(1, Math.floor((cw - 8) / 6.2));
          if (label.length > maxCh) label = label.slice(0, Math.max(1, maxCh - 1)) + '…';
          p.push('<text x="' + r2(x + 4) + '" y="' + r2(top + 11) + '" fill="#0e1310" font-size="10" font-weight="600">' +
            esc2(label) + '</text>');
        }
        // ── 音符：音高→行内高度，pos/dur→横向（pos 是**片段内** tick ⇒ 加 c.begin 才是全局）
        if (showNotes && c.notes && c.notes.length) {
          var lo = c.minPitch, hi = c.maxPitch;
          if (hi <= lo) { lo -= 2; hi += 2; }               // 单音：给个对称余量，别贴边
          else { var mg = Math.max(1, Math.round((hi - lo) * 0.12)); lo -= mg; hi += mg; }
          var yTop = top + PAD + 12;                         // 顶部留出片段名那一行
          var yBot = top + ROW - PAD;
          if (yBot - yTop >= 4) {
            c.notes.forEach(function (n) {
              var nx = X(c.begin + n.pos);
              var nw = Math.max(1, n.dur * scale);
              var u = (n.pitch - lo) / (hi - lo);
              // 音符画成 3px 高的小条（音高 → 行内高度）；`outline` 只描边、不填充
              var nyy = clamp(yBot - u * (yBot - yTop) - 1.5, yTop, yBot - 3);
              p.push('<rect x="' + r2(nx) + '" y="' + r2(nyy) + '" width="' + r2(Math.min(nw, Math.max(1, cw))) +
                '" height="3" rx="1" ' +
                (outline ? 'fill="none" stroke="#101418" stroke-opacity="0.95" stroke-width="1.2"'
                         : 'fill="#1a1a1a" fill-opacity="0.85"') + '/>');
            });
          }
        }
        if (selected) {
          p.push('<rect class="mmselbox" x="' + (x - 2) + '" y="' + (top + PAD - 2) + '" width="' + r2(cw + 4) +
            '" height="' + (ROW - 2 * PAD + 4) + '" rx="4"/>');
        }
        p.push('<rect class="hit" x="' + x + '" y="' + (top + PAD) + '" width="' + r2(cw) + '" height="' + (ROW - 2 * PAD) + '"/>');
        p.push('</g>');
      });
    });

    // ── 播放头（**只读展示**：这是 ACE 的走带位置，我们不改它）
    if (d.playheadTick >= t0 && d.playheadTick <= t1) {
      var px = X(d.playheadTick);
      p.push('<line x1="' + px + '" y1="' + RULER + '" x2="' + px + '" y2="' + h + '" stroke="#e8e8e8" stroke-width="1.2"/>');
      p.push('<path d="M' + r2(px - 4) + ' ' + RULER + ' L' + r2(px + 4) + ' ' + RULER + ' L' + px + ' ' + (RULER + 7) + ' Z" fill="#e8e8e8"/>');
    }

    // ── 高亮标记（查找命中）：在**该轨那一行**里画一条竖虚线；当前项更亮 + 顶部小三角
    //    ⚠️ `mk.track` 是**轨道号**（不是数组下标）⇒ 先建一张 轨道号 → 行顶 的对照表
    var rowOf = {};
    d.tracks.forEach(function (tr, ti) { rowOf[tr.index] = RULER + ti * ROW; });
    marks.forEach(function (mk) {
      var x = X(mk.tick);
      if (x < GUTTER || x > w) return;
      var ry = (mk.track != null && rowOf[mk.track] != null) ? rowOf[mk.track] : RULER;
      var col = mk.cur ? '#ffd166' : '#f0a04b';
      p.push('<g class="fpmk' + (mk.cur ? ' cur' : '') + '" data-tick="' + mk.tick + '">');
      if (mk.title) p.push('<title>' + esc2(mk.title) + '</title>');
      p.push('<line x1="' + r2(x) + '" y1="' + (ry + 1) + '" x2="' + r2(x) + '" y2="' + r2(ry + ROW - 1) +
        '" stroke="' + col + '" stroke-width="' + (mk.cur ? 2 : 1) + '" stroke-dasharray="3 2" stroke-opacity="' +
        (mk.cur ? 1 : 0.7) + '"/>');
      p.push('<path d="M' + r2(x - 4) + ' ' + ry + ' L' + r2(x + 4) + ' ' + ry + ' L' + r2(x) + ' ' + (ry + 6) +
        ' Z" fill="' + col + '" fill-opacity="' + (mk.cur ? 1 : 0.75) + '"/>');
      p.push('</g>');
    });

    p.push('<defs><pattern id="mmHatch" width="6" height="6" patternTransform="rotate(45)" patternUnits="userSpaceOnUse">' +
      '<rect width="6" height="6" fill="#00000000"/><line x1="0" y1="0" x2="0" y2="6" stroke="#0b0e13" stroke-opacity="0.5" stroke-width="2"/></pattern></defs>');
    p.push('</svg>');
    return {
      html: p.join(''), w: w, h: h,
      geom: { X: X, total: t1 - t0, t0: t0, scale: scale, gutter: GUTTER, ruler: RULER, row: ROW, rows: rowOf },
    };
  }

  function render() {
    var box = document.getElementById('mmscroll');
    if (!box) return;
    var d = S.data;
    if (!d) return;
    if (!d.tracks || !d.tracks.length) {
      box.innerHTML = '<div class="mmwarn" style="padding:16px">工程里一条**有内容的轨**都没有（空轨按你说的跳过了）。' +
        '新建/放点东西再读。</div>';
      paintStat();
      return;
    }
    box.innerHTML = svgHTML(d, { box: box }).html;
    box.querySelectorAll('.mmclip').forEach(function (g) {
      g.addEventListener('click', function () { pick(g.getAttribute('data-ti'), g.getAttribute('data-ci')); });
    });
    paintStat();
    paintSel();
    paintLeg();
  }

  function paintStat() {
    var el = document.getElementById('mmstat');
    if (!el || !S.data) return;
    var d = S.data;
    var txt = d.trackCount + ' 轨 / ' + d.clipCount + ' 片段 / ' + d.noteCount + ' 音符';
    if (d.meter || d.tempo) txt += ' · ' + (d.meter || '') + (d.tempo ? ' ' + d.tempo + ' BPM' : '');
    txt += ' · ' + (tickToSec((d.lastTick || 0) - (d.firstTick || 0))).toFixed(2) + ' s';
    el.textContent = txt;
  }

  function paintLeg() {
    var el = document.getElementById('mmleg');
    if (!el || !S.data) return;
    var d = S.data;
    var lines = [];
    lines.push('时间轴按**片段范围**画（第 ' + beatOf(d.firstTick) + '–' + beatOf(d.lastTick) + ' 拍，' +
      (barTicks() ? Math.ceil((d.lastTick - d.firstTick) / barTicks()) : '?') + ' 小节）；' +
      '红线 = ACE 的播放头（只显示、不改）。');
    if (d.clipsWithoutNotes > 0) {
      lines.push('<span class="mmwarn">⚠️ 有 ' + d.clipsWithoutNotes + ' 个片段的音符没读（预算 ' + d.noteBudget +
        ' 个片段用完了）⇒ 画成斜纹。把「读音符的片段上限」调大可以读全。</span>');
    }
    if (d.diagnostics && d.diagnostics.length) {
      lines.push('<span class="mmwarn">' + d.diagnostics.map(esc2).join('<br>') + '</span>');
    }
    el.innerHTML = lines.join('<br>');
  }

  function paintSel() {
    var el = document.getElementById('mmsel');
    if (!el) return;
    var v = S.sel;
    if (!v) { el.textContent = '（面板内未选中片段）'; return; }
    el.textContent = '面板内已选中：' + window.PanelSelection.label() +
      ' · 第 ' + beatOf(v.begin) + '–' + beatOf(v.end) + ' 拍 · ' + v.noteCount + ' 音符';
  }

  /* ============================ 交互 ============================ */

  function pick(ti, ci) {
    var d = S.data;
    ti = parseInt(ti, 10); ci = parseInt(ci, 10);
    var tr = (d.tracks || []).filter(function (t) { return t.index === ti; })[0];
    if (!tr) return;
    var c = (tr.clips || []).filter(function (x) { return x.index === ci; })[0];
    if (!c) return;
    // ⚠️ **只动面板**：一个字节都不发给 ACE（口径见文件头）
    saveSel({
      trackIndex: ti, trackName: tr.name, clipIndex: ci, clipUuid: c.uuid, name: c.name,
      type: c.type, begin: c.begin, end: c.end, noteCount: c.noteCount, at: Date.now(),
    });
    render();
  }

  /** 读一份工程缩略图数据（**不碰本面板的 DOM**）—— 本面板与「查找」页的预览共用。 */
  async function fetchMap(notes) {
    var data = await postRun('view.minimap', { notes: String(notes == null ? 0 : notes) });
    var r = (data.calls[0] || {}).json;
    if (!data.ok || !r) throw new Error(data.hint || '读取失败');
    return r;
  }

  async function fetchData() {
    if (S.busy) return;
    S.busy = true;
    var box = document.getElementById('mmscroll');
    if (box) box.innerHTML = '<div class="mmdim" style="padding:16px">读取工程中…（每条轨一条命令、每个片段再一条读音符，工程大时会慢）</div>';
    var notesEl = document.getElementById('mmnotes');
    var notes = notesEl && notesEl.checked ? '24' : '0';
    try {
      var r = await fetchMap(notes);
      S.data = r;
      S.fit = true;
      S.zoom = 1;
      render();
      var out = document.getElementById('mmout');
      if (out) out.innerHTML = '<div class="hint ok">' + esc2(r.note || '') + '</div>';
    } catch (e) {
      S.data = null;
      if (box) box.innerHTML = '<div class="hint bad" style="margin:10px">' + esc2((e && e.message) || e) + '</div>';
    } finally {
      S.busy = false;
    }
  }

  function bind() {
    ensureStyle();
    S.sel = loadSel();
    paintSel();
    var el;
    if ((el = document.getElementById('mmrefresh'))) el.addEventListener('click', fetchData);
    if ((el = document.getElementById('mmfit'))) {
      el.addEventListener('click', function () { S.fit = true; S.zoom = 1; render(); });
    }
    if ((el = document.getElementById('mmzin'))) {
      el.addEventListener('click', function () { S.fit = false; S.zoom = clamp(S.zoom * 1.5, 0.5, 40); render(); });
    }
    if ((el = document.getElementById('mmzout'))) {
      el.addEventListener('click', function () { S.fit = false; S.zoom = clamp(S.zoom / 1.5, 0.5, 40); render(); });
    }
    if ((el = document.getElementById('mmnotes'))) {
      el.addEventListener('change', function () { if (S.data) render(); });
    }
    if ((el = document.getElementById('mmclear'))) {
      el.addEventListener('click', function () { saveSel(null); render(); });
    }
    if (S.data) render();
    else fetchData(); // 进页就自动读一次（用户不必先点按钮）
  }

  window.MinimapPanel = {
    html: panelHTML, bind: bind,
    state: S,
    /**
     * 把一份 minimap 数据画进**任意容器**（「查找」页的结果预览就走这里）。
     * `opt.marks` = 高亮标记（查找命中）；`opt.interactive` 才接片段点击。
     */
    draw: function (el, data, opt) {
      if (!el || !data || !data.tracks) return null;
      // ⚠️ 必须先把这套 CSS 塞进 <head>：`.mmclip .hit{fill:transparent}` 在这里面 ——
      //    漏了它，那片透明的"点击区"就按 SVG 默认的**黑**填满整个片段
      //    （2026-10-08 实测：查找页的预览里片段整块是黑的，就是这个原因）。
      ensureStyle();
      opt = opt || {};
      var o = { box: el, sel: null };
      for (var k in opt) if (Object.prototype.hasOwnProperty.call(opt, k)) o[k] = opt[k];
      if (o.sel === undefined) o.sel = null;
      var r = svgHTML(data, o);
      el.innerHTML = r.html;
      if (o.interactive) {
        el.querySelectorAll('.mmclip').forEach(function (g) {
          g.addEventListener('click', function () {
            var ti = g.getAttribute('data-ti'), ci = g.getAttribute('data-ci');
            // ⚠️ `pick()` 画的是**本面板**那份 ⇒ 别处要点击得先切回本面板；这里只在本面板调用时才接。
            pick(ti, ci);
          });
        });
      }
      return { w: r.w, h: r.h, geom: r.geom };
    },
    fetch: fetchMap,
    _test: { barTicks: barTicks, tickToSec: tickToSec, svgHTML: svgHTML },
  };

  /* ---------- 统一入口（用户 2026-10-23：「把 canvas 都统一成 minimap」） ----------
     mount(el, data, opt) → { geom, ov, X(tick) }
     bands(m, items)      → 覆盖层色带（items: {begin,end,color,stroke}，tick 坐标）
     各页不再自己拿 canvas 画，只准备数据 + 传覆盖层。 */
  window.MinimapPanel.mount = function (el, data, opt) {
    if (!el || !data || !data.tracks) return null;
    ensureStyle();
    if (!document.getElementById('mmovStyle')) {
      var st = document.createElement('style');
      st.id = 'mmovStyle';
      st.textContent = '.mmov{position:absolute;inset:0;pointer-events:none}' +
        '.mmov .band{position:absolute;top:0;bottom:0;border-left:1.5px solid;border-right:1.5px solid}';
      document.head.appendChild(st);
    }
    el.style.position = 'relative';
    var r = svgHTML(data, opt || {});
    el.innerHTML = r.html;
    var ov = document.createElement('div');
    ov.className = 'mmov';
    el.appendChild(ov);
    return { geom: r.geom, ov: ov, w: r.w, h: r.h, X: function (t) { return r.geom.X(t); } };
  };
  window.MinimapPanel.bands = function (m, items) {
    if (!m || !m.ov || !m.geom) return;
    m.ov.innerHTML = '';
    (items || []).forEach(function (it) {
      if (!it) return;
      var a = m.geom.X(it.begin), b = m.geom.X(it.end);
      var d = document.createElement('div');
      d.className = 'band';
      d.style.left = Math.min(a, b) + 'px';
      d.style.width = Math.max(1, Math.abs(b - a)) + 'px';
      d.style.background = it.color || 'rgba(26,115,232,.18)';
      d.style.borderColor = it.stroke || '#1a73e8';
      m.ov.appendChild(d);
    });
  };

})();
