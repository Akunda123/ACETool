/* 「查找」面板（`find.notes` 专用 UI）—— **独立模块**，主面板与独立窗口共用。
 *
 * 为什么单独成文件（用户 2026-10-08）：
 *   「我要的是进入 home.html 的**正式功能**，新的 web 页」——
 *   于是有了 `find.html`（home.html 上的按钮开的子窗口）。独立窗口**不加载 app.js**
 *   （那是整个主面板：侧栏 + 状态 + 表单），所以面板本体必须搬到一个两边都能用的模块里，
 *   就像 `notation.js`（谱面编辑器）与 `minimap.js`（缩略图）那样。
 *
 * 两边怎么共用（宿主页只需给三样东西）：
 *   · `window.postRun(toolId, values)` —— 必须（主面板 app.js 有；`find.html` 自己给一个替身）
 *   · `window.MinimapPanel`            —— 结果预览要用（`minimap.js`，两边都引了）
 *   · 工具清单                         —— 主面板从 `state.catalog` 拿；独立窗口自己 `fetch('/api/tools')`
 *     （`ready()` 负责；所以独立窗口要 `await FindPanel.mount(el)`，别自己先 `html()`）
 *   `window.pushLog` / `window.renderRunHTML` 有就用（主面板的命令日志与结果卡片），
 *   没有就**什么都不画** —— 独立窗口不必为了这个把 app.js 拖进来，也不另造一套结果 UI。
 *
 * 面板本身**逐条复刻** SV 脚本 `[SV2]查找.js` 的 SidePanelSection：行顺序、行类型、默认值、
 * 联动关系、「位置:」的格式、按钮可用性，都照原脚本。只写 zh-cn 那套字面，
 * **不建翻译表**（原脚本带 ja-jp / zh-cn / zh-tw 三套 —— 用户 2026-10-04 裁定只留中文）。
 *
 * 与原脚本的对照（原脚本 → 这里）：
 *   TextArea(height 100)            → <pre id=fpinfo>（检索结果；**动态、不可编辑**，不是输入控件）
 *   Button 全选目标音符(全宽)        → 同；范围≠当前片段时**禁用**（原脚本 setEnabled(isGroup)）
 *   Button 上一项 | 下一项           → 同（**中间多了序号框**，用户 2026-10-08）
 *   Button 刷新 | 说明               → 同
 *   TextArea(height 20)             → <input id=fpcontent>
 *   ComboBox 大于/小于/等于/包含     → 同，默认「等于」（原脚本 condition.setValue(2)）
 *   ComboBox 当前片段/轨道/工程    → 同，默认「当前片段」
 *   CheckBox 音符属性 | 参数         → 同，互斥，默认勾「音符属性」
 *   ComboBox 22 属性 | 9 参数        → 同（参数我们给 13 条：9 条原样 + 4 条 ACE 独有）
 *   参数层（**原脚本没有**）          → 我们加的：ACE 的参数分多层，得先说是哪一层
 *   结果预览（**原脚本没有**）        → 我们加的：工程缩略图 + 命中位置高亮（用户 2026-10-08）
 */
(function () {
  'use strict';

  var $ = function (id) { return document.getElementById(id); };
  function esc(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }
  // 与 app.js 那份逐字相同（`const` 不会挂到 window 上，所以这里自己再写一份）
  var NOFILL = 'autocomplete="off" autocapitalize="off" autocorrect="off" spellcheck="false"';

  /** 宿主页给的 postRun（缺了就明确报错，别静默什么都不做）。 */
  function run(toolId, values) {
    if (typeof window.postRun !== 'function') {
      return Promise.reject(new Error('页面没有 postRun（主面板用 app.js 的、独立窗口要自带一个）'));
    }
    return window.postRun(toolId, values);
  }
  /** 命令日志：主面板有 `pushLog`；独立窗口没有就丢掉（那边只显示结果框）。 */
  function log(calls) {
    if (typeof window.pushLog !== 'function') return;
    (calls || []).forEach(function (c) { window.pushLog(c); });
  }

  var FIND_HELP = '【查找脚本 使用说明】\n\n' +
    '1. 输入查找文本\n' +
    '2. 选择匹配条件（大于/小于/等于/包含）\n' +
    '3. 选择查找范围（片段/轨道/工程）\n' +
    '4. 选择目标类型（音符属性/参数）\n' +
    '5. 选择具体属性或参数\n\n' +
    '点击「上一项」「下一项」浏览结果（中间那个框可以直接输入第几项）\n' +
    '点击「全选目标音符」选中当前组内所有匹配项（只在音符属性模式有这一颗）\n\n' +
    '提示: 音高用MIDI号（C4=60），可输入「黑」「白」检索黑键/白键';

  var find = {
    info: '无内容', content: '', cond: '2', scope: 'clip', target: 'note',
    attr: 'pitch', param: 'tension', layer: 'direct',
    hits: [], cur: -1, running: false,
  };
  var findStats = null;

  /** 结果预览那一块的状态（`view.minimap` 的数据；默认就带音符 ⇒ 图上看得见音符轮廓）。 */
  var findMap = { data: null, busy: false, notes: '24' };

  /* ============================ 工具清单（属性/参数下拉的来源） ============================ */

  var CATALOG = null;
  function catalog() { return CATALOG || (window.state && window.state.catalog) || null; }

  /**
   * 宿主页把自己的工具清单递进来 —— 主面板（app.js）走这条：
   * 它的 `state` 是 `const`（**不会**挂到 window 上），所以光看 `window.state` 是拿不到的
   * （2026-10-08 实测：主面板里属性下拉只剩一条「（已移除的属性：pitch）」就是这个原因）。
   * 独立窗口不用这个 —— 它 `mount()` 里 `await ready()` 自己去 `/api/tools` 要。
   */
  function setCatalog(c) { if (c) CATALOG = c; return CATALOG; }

  /** 拿工具清单：主面板由 `setCatalog()` 递进来；独立窗口自己去 `/api/tools` 要一份。 */
  function ready() {
    if (catalog()) return Promise.resolve(catalog());
    return fetch('/api/tools', { cache: 'no-store' })
      .then(function (r) { return r.json(); })
      .then(function (j) { CATALOG = j; return j; })
      .catch(function () { return null; });   // 取不到就让下拉空着（下面的选项列表会是空的，如实）
  }

  function findOpts(name) {
    var c = catalog();
    var t = ((c && c.tools) || []).filter(function (x) { return x.id === 'find.notes'; })[0];
    return (t && (t.params || []).filter(function (p) { return p.name === name; })[0]) || { options: [] };
  }

  /**
   * fpattr：音符属性下拉（**重写**过 —— 原来是直接吃 schema 的扁平列表）。
   *
   * 属性表现在是**两段来源**（原脚本里 ACE 真能做的那些 + ACE 独有的那些），
   * 但**下拉里就是一个平铺列表**：用户 2026-10-14「把原来的和新的标签去掉」/「我们加的标签去掉」
   * /「把参数前的星号去掉」⇒ 我们自己加的三处标记全去掉了：
   *   ① 属性下拉的两个分组标签（「原脚本 · ACE 能做」/「ACE 独有」）② 参数层后面那个
   *   带 `fp-tag` 类的「我们加的」小标 ③ 那 12 条 ACE 独有属性与 4 条参数标签前的 `★ `。
   * API 侧仍靠 `ace.` 前缀区分（那是回包字段，不是界面标签）。
   */
  function findAttrSelectHTML() {
    var all = findOpts('attr').options || [];
    var opt = function (o) {
      return '<option value="' + esc(o.value) + '"' +
        (String(o.value) === String(find.attr) ? ' selected' : '') + '>' + esc(o.label) + '</option>';
    };

    // 当前选中的 key 不在列表里（比如上一版留下的 detune）⇒ 补一条占位，
    // 否则浏览器会静默选中第一项，用户以为条件生效了、其实换了属性。
    var stale = (find.attr && !all.some(function (o) { return String(o.value) === String(find.attr); }))
      ? '<option value="' + esc(find.attr) + '" selected>（已移除的属性：' + esc(find.attr) + '）</option>'
      : '';

    return '<select id="fpattr">' + stale + all.map(opt).join('') + '</select>';
  }

  /* ============================ 面板 HTML ============================ */

  function html() {
    var toOpts = function (name) {
      return (findOpts(name).options || []).map(function (o) { return { v: o.value, t: o.label }; });
    };
    var combo = function (id, list, cur) {
      return '<select id="' + id + '">' + list.map(function (c) {
        return '<option value="' + esc(c.v) + '"' + (String(c.v) === String(cur) ? ' selected' : '') + '>' +
          esc(c.t) + '</option>';
      }).join('') + '</select>';
    };
    var two = function (a, b) { return '<div class="fp-two">' + a + b + '</div>'; };

    // ⭐ 用户 2026-10-08 的版式口径（逐条对上）：
    //   ① 最上方 `fp-info` = 检索结果，**只读**（框内不可编辑）
    //   ② 它下面 = 结果预览（`view.minimap` 那张图，命中位置高亮）
    //   ③ 「全选目标音符」**只在音符模式**出现
    //   ④ 上一项 /【序号输入框】/ 下一项 —— 中间那个数字框用来输入 index 直接跳
    //   ⑤ `fpattr` 只在音符模式；`fpparam` + `fplayer`（含参数层说明）只在参数模式
    //   ⑥ `fpcontent`（查找内容）默认**空着**（用户 2026-10-08 确认：就是默认没内容，不是 `[]`）
    return '<div class="findpanel">' +
      // ① 结果框：**动态、不可编辑** —— 用 <pre>（不是输入控件），内容由 draw() 写 textContent
      '<pre class="fp-info" id="fpinfo"></pre>' +
      // ② 预览：工程缩略图 + 命中高亮。**不加标题、不加控件**（用户 2026-10-08：不要多余的字）
      '<div class="fp-map" id="fpmap"></div>' +
      '<button class="btn fp-wide" id="fpselall" type="button">全选目标音符</button>' +
      '<div class="fp-two">' +
      '<button class="btn ghost" id="fpprev" type="button">上一项</button>' +
      '<input type="number" class="fp-idx" id="fpindex" min="1" step="1" ' + NOFILL + ' title="第几项">' +
      '<button class="btn ghost" id="fpnext" type="button">下一项</button>' +
      '</div>' +
      two('<button class="btn ghost" id="fprefresh" type="button">刷新</button>',
          '<button class="btn ghost" id="fphelp" type="button">说明</button>') +
      '<div class="fp-label">查找内容</div>' +
      '<input type="text" class="fp-text" id="fpcontent" ' + NOFILL + ' value="' + esc(find.content) + '">' +
      '<div class="fp-label">匹配条件</div>' +
      combo('fpcond', [{ v: '0', t: '大于' }, { v: '1', t: '小于' }, { v: '2', t: '等于' }, { v: '3', t: '包含' }], find.cond) +
      '<div class="fp-label">查找范围</div>' +
      combo('fpscope', [{ v: 'clip', t: '当前片段' }, { v: 'track', t: '当前轨道' }, { v: 'project', t: '当前工程' }], find.scope) +
      // 原脚本的 Target 在 zh-cn 里也译作「查找内容」—— 这个重复是原脚本自己的，照抄
      '<div class="fp-label">查找内容</div>' +
      '<div class="fp-checks">' +
      '<label class="check"><input type="checkbox" id="fpisnote"><span>音符属性</span></label>' +
      '<label class="check"><input type="checkbox" id="fpisparam"><span>参数</span></label>' +
      '</div>' +
      '<div class="fp-two">' +
      '<span class="fp-slot" id="fpattrslot">' + findAttrSelectHTML() + '</span>' +
      '<span class="fp-slot" id="fpparamslot">' + combo('fpparam', toOpts('param'), find.param) + '</span>' +
      '</div>' +
      '<div class="fp-slot" id="fplayerslot">' +
      '<div class="fp-label">参数层</div>' +
      combo('fplayer', toOpts('paramLayer'), find.layer) +
      '<div class="fp-note">原脚本没有「参数层」—— ACE 的参数曲线分层（baseline / direct / envelope / effective），' +
      '只有 <b>直接层</b> 相当于 SV 的 <span class="mono">getAllPoints()</span>（你画过的点）。' +
      '「生效曲线」是融合后实际唱出来的。</div>' +
      '</div>' +
      '<div class="fp-stat" id="fpstat"></div>' +
      '</div>' +
      '<div class="hint" id="fpflash" hidden></div>' +
      '<div id="fpresult"></div>';
  }

  /**
   * 按当前模式显示/隐藏那几块（用户 2026-10-08 的口径）：
   * **音符模式** = 全选 + `fpattr`；**参数模式** = `fpparam` + `fplayer`（含参数层说明）。
   */
  function applyMode() {
    var isNote = find.target === 'note';
    var set = function (id, on) { var el = $(id); if (el) el.hidden = !on; };
    set('fpselall', isNote);
    set('fpattrslot', isNote);
    set('fpparamslot', !isNote);
    set('fplayerslot', !isNote);
  }

  /** 结果里的"序号"（复刻原脚本：片段**正着数**、轨道/工程**倒着数**）。 */
  function displayNum(i) { return find.scope === 'clip' ? i + 1 : find.hits.length - i; }

  /** 输入序号 → 内部下标（与 `displayNum` 同一套口径）；越界给 -1。 */
  function indexForDisplay(n) {
    if (!isFinite(n) || n < 1) return -1;
    var i = find.scope === 'clip' ? n - 1 : find.hits.length - n;
    return (i >= 0 && i < find.hits.length) ? i : -1;
  }

  function sync() {
    find.content = $('fpcontent').value;
    find.cond = $('fpcond').value;
    find.scope = $('fpscope').value;
    find.target = $('fpisparam').checked ? 'param' : 'note';
    find.attr = $('fpattr').value;
    find.param = $('fpparam').value;
    find.layer = $('fplayer').value;
  }

  /** 复刻原脚本的互斥回调：checkValue1 → (true,false,type=0)；checkValue2 → (false,true,type=1)。 */
  function syncChecks() {
    var n = $('fpisnote'), p = $('fpisparam');
    if (n) n.checked = find.target === 'note';
    if (p) p.checked = find.target === 'param';
  }

  function values() {
    sync();
    return {
      target: find.target, attr: find.attr, param: find.param, paramLayer: find.layer,
      cond: ['gt', 'lt', 'eq', 'contains'][parseInt(find.cond, 10) || 0] || 'eq',
      value: find.content, scope: find.scope,
    };
  }

  function draw() {
    // 结果框是**动态的**：每次状态变都重写它的文本（`<pre>` 不是输入控件 ⇒ 框里改不了）
    var t = $('fpinfo');
    if (t) t.textContent = find.info;
    var st = $('fpstat');
    if (st) {
      st.textContent = (findStats && findStats.query)
        ? findStats.query + ' · 扫了 ' + findStats.noteCount + ' 音符 / ' + findStats.clipCount + ' 片段 · ' + findStats.millis + ' ms'
        : '';
    }
    applyMode();
    var prev = $('fpprev'), next = $('fpnext'), all = $('fpselall');
    if (prev) prev.disabled = !(find.cur > 0);
    if (next) next.disabled = !(find.cur >= 0 && find.cur < find.hits.length - 1);
    // 原脚本：selectButton.setEnabled(isGroup)（而且它**只在音符模式**出现，见 applyMode）
    if (all) all.disabled = find.scope !== 'clip';
    // ⚠️ 序号框**不写值**（用户 2026-10-08：「我不是说输入框默认没东西吗」）——
    //    它就是个输入口：只在没结果时置灰，当前是第几项看结果框里的「序号: N/M」。
    var idx = $('fpindex');
    if (idx) idx.disabled = !find.hits.length;
    syncChecks();
  }

  async function search() {
    if (find.running) return;
    find.running = true;
    find.info = '查找中…';
    draw();
    try {
      var data = await run('find.notes', values());
      log(data.calls);
      if (!data.ok) {
        // 明确拒绝（ACE 没这个概念 / 参数不可用）：把服务端的话**原样**摆出来。
        // 这里绝不能显示「无搜索结果」—— 那会把"问错了"伪装成"查过没有"。
        find.hits = []; find.cur = -1;
        find.info = '〔未执行〕\n' + (data.hint || '执行失败');
        findStats = null;
        draw(); renderResult(data); mapDraw();
        return;
      }
      var d = data.calls[0].json;
      find.hits = d.hits || [];
      find.cur = find.hits.length ? (d.startIndex | 0) : -1;
      findStats = d;
      if (!find.hits.length) { find.info = '无搜索结果' + (d.note ? '\n\n' + d.note : ''); draw(); }
      else resultInfo(false); // 查完只摆文本，先不动标记线
      renderResult(data);
    } catch (e) {
      find.info = '〔请求失败〕\n' + ((e && e.message) || e);
      findStats = null;
      draw();
    } finally {
      find.running = false;
    }
  }

  /**
   * 复刻原脚本 resultInfo()：清空选区 → 空结果给「无搜索结果」+ 两端禁用 →
   * displayNum（片段正着数 / 轨道·工程倒着数）→ 只有「范围=片段 且 目标=音符属性」才选音符
   * → 上一项/下一项 按 index 两端禁用。
   *
   * 我们多做的：把标记线**真的**移过去（原脚本是 navigation.setTimeLeft 移视图）。
   * 那是写操作，所以只在按「上一项/下一项」（或序号框）时发（go=true）；查完只摆文本。
   */
  function resultInfo(go) {
    var isGroup = find.scope === 'clip';
    var isNote = find.target === 'note';
    if (!find.hits.length) { find.info = '无搜索结果'; draw(); mapDraw(); return; }
    if (find.cur < 0) find.cur = 0;
    if (find.cur >= find.hits.length) find.cur = find.hits.length - 1;
    var r = find.hits[find.cur];
    var n = displayNum(find.cur);

    var text = '序号: ' + n + '/' + find.hits.length + '\n';
    // 原脚本：范围=片段 时 轨道/片段 两行都不给；范围=轨道 时只给 片段
    if (find.scope === 'project') text += '轨道: ' + (r.trackName || ('轨道 ' + r.track)) + '\n';
    if (!isGroup) text += '片段: ' + (r.clipName || ('片段 ' + r.clip)) + '\n';
    if (isNote && r.noteIndex >= 0) text += '音符序号: ' + r.noteIndex + '\n';
    text += '位置: ' + (r.posSv || '');
    if (r.value !== undefined && r.value !== '') text += '\n数值: ' + r.value;
    find.info = text;
    draw();
    mapDraw();              // ⭐ 预览里的高亮跟着走（只重画，不重读工程）
    if (go) jump(r, isGroup && isNote);
  }

  /**
   * 把当前项移过去：标记线 +（该选音符时）选中它。
   *
   * ⚠️ 单位口径（用户 2026-10-14）：**对外一律「拍」**（1 拍 = 一个四分音符，ACE PPQ = 480 tick/拍）
   * ⇒ 发给 `find.goto` 的是**拍**、提示里显示的也是**拍**，`tick` 不出现在任何用户可见文字里。
   */
  function beatsOf(tick) {
    var v = (Number(tick) || 0) / 480;
    return String(Math.round(v * 100) / 100);
  }
  async function jump(r, selectNote) {
    try {
      var data = await run('find.goto', {
        absBeat: beatsOf(r.absTick),
        noteUuid: selectNote ? (r.noteUuid || '') : '',
        dryRun: '0',
      });
      log(data.calls);
      if (!data.ok) flash('跳到该项失败：' + (data.hint || ''), false);
      else flash('标记线 → 第 ' + beatsOf(r.absTick) + ' 拍' + (selectNote && r.noteUuid ? '，并选中了该音符' : '（参数命中只移标记线）'), true);
    } catch (e) {
      flash('跳到该项失败：' + ((e && e.message) || e), false);
    }
  }

  function nav(delta) {
    if (!find.hits.length) return;
    var i = find.cur + delta;
    if (i < 0) i = 0;
    if (i >= find.hits.length) i = find.hits.length - 1;
    find.cur = i;
    resultInfo(true);
  }

  /** 跳到第 n 项（序号框里输入的那个数）—— 口径与「序号: N/M」完全一致。 */
  function gotoIndex(n) {
    var idx = $('fpindex');
    if (!find.hits.length) { flash('还没有结果，先查一次', false); if (idx) idx.value = ''; return; }
    var i = indexForDisplay(n);
    if (i < 0) {
      flash('序号要在 1–' + find.hits.length + ' 之间（当前是「' + displayNum(find.cur) + '」）', false);
      if (idx) idx.value = '';          // 越界就把框清回空的（框里不留东西）
      draw();
      return;
    }
    find.cur = i;
    if (idx) idx.value = '';            // 跳完也清空：这个框永远是"你正在输入"的地方
    resultInfo(true);
  }

  /* ---------------- 结果预览（`view.minimap` + 命中高亮） ----------------
   *
   * 用户 2026-10-08：「可在下方显示预览，预览为 view.minimap，高亮出检索到内容的位置」+
   * 「minimap显示音符轮廓」「不要把「结果预览 view.minimap」这种东西加进来」⇒
   *   · 面板上**没有**标题/标签/开关，就一张图；
   *   · 读一次（带音符，`notes=24`）缓存住，查找/翻页只**重画高亮**，不重读工程；
   *   · 音符画成**轮廓**（`noteStyle:'outline'`，描边不填充），与缩略图面板的实心条区分开。
   */
  function mapDraw() {
    var el = $('fpmap');
    if (!el) return;
    if (!window.MinimapPanel || !window.MinimapPanel.draw) {
      el.innerHTML = '<div class="hint bad" style="margin:0">缩略图模块没加载（minimap.js）</div>';
      return;
    }
    if (!findMap.data) {
      el.textContent = findMap.busy ? '…' : '';   // 加载中就一个省略号（不写说明文字）
      return;
    }
    var marks = find.hits.map(function (h, i) {
      return {
        tick: h.absTick, track: h.track, cur: i === find.cur,
        title: '#' + displayNum(i) + ' · ' + (h.clipName || ('片段 ' + h.clip)) + ' · ' + (h.posSv || ''),
      };
    });
    window.MinimapPanel.draw(el, findMap.data, {
      marks: marks, showNotes: true, noteStyle: 'outline', fit: true, zoom: 1,
    });
  }

  async function mapLoad() {
    if (findMap.busy) return;
    if (!window.MinimapPanel || !window.MinimapPanel.fetch) { mapDraw(); return; }
    findMap.busy = true;
    mapDraw();
    try {
      findMap.data = await window.MinimapPanel.fetch(findMap.notes);
    } catch (e) {
      findMap.data = null;
      var el = $('fpmap');
      if (el) el.innerHTML = '<div class="hint bad" style="margin:0">' + esc((e && e.message) || e) + '</div>';
      return;
    } finally {
      findMap.busy = false;
    }
    mapDraw();
  }

  async function selectAll() {
    if (find.scope !== 'clip') { flash('「全选目标音符」只在「当前片段」范围可用（原脚本也是这么禁用的）', false); return; }
    if (!find.hits.length) { flash('没有命中的音符', false); return; }
    try {
      var v = values();
      v.dryRun = '0';
      var data = await run('find.select', v);
      log(data.calls);
      if (!data.ok) { flash('全选失败：' + (data.hint || ''), false); return; }
      var back = (data.calls || []).map(function (c) { return c.json; }).filter(Boolean).pop();
      var n = back && (back.selectionCount != null ? back.selectionCount : (back.notes || []).length);
      flash('已在编辑器里选中 ' + (n != null ? n : '?') + ' 个音符（已回读核对）', true);
    } catch (e) {
      flash('全选失败：' + ((e && e.message) || e), false);
    }
  }

  function flash(msg, ok) {
    var el = $('fpflash');
    if (!el) return;
    el.className = 'hint ' + (ok ? 'ok' : 'bad');
    el.textContent = msg;
    el.hidden = false;
  }

  /**
   * 结果落进 `#fpresult` —— **只**在主面板里画（那边有 app.js 的 `renderRunHTML`）。
   * 独立窗口没有那份渲染就**什么都不画**（不自己造一种结果清单出来 —— 用户 2026-10-08：
   * 「fp-rep 是我提到要加的东西吗？你加这个干什么」）。
   * 失败/未执行的说明本来就在 `#fpinfo` 里看得见，不缺这一块。
   */
  function renderResult(data) {
    var box = $('fpresult');
    if (!box || typeof window.renderRunHTML !== 'function') return;
    box.innerHTML = window.renderRunHTML(data, { compact: true });
  }

  function bind() {
    // 结果框的内容由 draw() 写（`<pre>`，不给它挂任何 .value —— 那是输入控件的东西）
    $('fpcontent').addEventListener('change', function () { search(); });
    $('fpcond').addEventListener('change', function () { search(); });
    $('fpscope').addEventListener('change', function () { search(); });
    $('fpattr').addEventListener('change', function () { find.target = 'note'; syncChecks(); search(); });
    $('fpparam').addEventListener('change', function () { find.target = 'param'; syncChecks(); search(); });
    $('fplayer').addEventListener('change', function () { search(); });
    $('fprefresh').addEventListener('click', function () { search(); });
    $('fphelp').addEventListener('click', function () { find.info = FIND_HELP; draw(); });
    $('fpisnote').addEventListener('change', function () { find.target = 'note'; syncChecks(); search(); });
    $('fpisparam').addEventListener('change', function () { find.target = 'param'; syncChecks(); search(); });
    $('fpprev').addEventListener('click', function () { nav(-1); });
    $('fpnext').addEventListener('click', function () { nav(1); });
    // ⭐ 序号数字框（用户 2026-10-08：「上一项和下一项中间加一个数字框，用来输入 index」）
    var idx = $('fpindex');
    if (idx) {
      idx.addEventListener('change', function () { gotoIndex(parseInt(idx.value, 10)); });
      idx.addEventListener('keydown', function (ev) {
        if (ev.key === 'Enter') { ev.preventDefault(); gotoIndex(parseInt(idx.value, 10)); }
      });
    }
    $('fpselall').addEventListener('click', function () { selectAll(); });
    // 预览：进页读一次（带音符）；万一那次没读到（ACE 没连上等），下次查找再补一次
    draw();
    mapLoad();
  }

  /** 独立窗口用这个：先把工具清单取回来（属性/参数下拉要有东西），再挂 + 接线。 */
  async function mount(el) {
    await ready();
    el.innerHTML = html();
    bind();
  }

  window.FindPanel = {
    html: html, bind: bind, mount: mount, ready: ready, setCatalog: setCatalog,
    state: find, mapState: findMap,
    _test: {
      displayNum: displayNum, indexForDisplay: indexForDisplay, applyMode: applyMode,
      jump: jump, gotoIndex: gotoIndex, values: values, search: search,
    },
  };
})();
