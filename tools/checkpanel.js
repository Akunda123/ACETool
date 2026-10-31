'use strict';
/* 查找面板自检（不依赖浏览器）：把 assets/app.js 放进一个最小 DOM 桩里跑，然后
 *   1. 调 findPanelHTML() 收集它生成的 id
 *   2. 调 bindFind() 收集它去取的 id —— 两边对账（少一个就是运行时会 null 崩）
 *   3. 核对复刻原脚本的默认值、文本格式、按钮可用性
 * 这一层是必要的：面板是手写 HTML 字符串，id 拼错在浏览器里只表现为"按钮没反应"。
 *
 * 用法：node tools/checkpanel.js   （在仓库根目录跑；只读，不改任何东西）
 */
const fs = require('fs');
const path = require('path');
const vm = require('vm');

const root = path.resolve(__dirname, '..');
const src = fs.readFileSync(path.join(root, 'internal', 'web', 'assets', 'app.js'), 'utf8');

const asked = new Set();
const created = new Map();
function mkEl(id) {
  const el = {
    id, value: '', checked: false, disabled: false, hidden: false, textContent: '',
    className: '', innerHTML: '', dataset: {},
    style: {}, classList: { add() {}, remove() {}, toggle() {} },
    addEventListener() {}, querySelectorAll: () => [], querySelector: () => null,
    setAttribute() {}, appendChild() {},
  };
  created.set(id, el);
  return el;
}
const doc = {
  getElementById(id) { asked.add(id); return created.get(id) || mkEl(id); },
  addEventListener() {},
  querySelectorAll: () => [],
  createElement: (t) => mkEl('<' + t + '>'),
};
const ctx = { document: doc, window: {}, console, setTimeout, clearTimeout };
ctx.globalThis = ctx;
vm.createContext(ctx);

// ⚠️ 顶层 const/let 落在 realm 的**全局词法环境**里，不是 globalThis 的属性
// ⇒ 不能写 ctx.state / ctx.find；要在同一个 realm 里再跑一段脚本来读它们。
vm.runInContext(src, ctx, { filename: 'app.js' });

// ⚠️ 凡是要"查源码里有没有某段代码"的断言，都必须**先去掉注释**再查。
// 这条自己就踩过：加了一句解释原脚本的注释（里面提到了 SV.T( 和 getTranslations），
// 结果把关"不许留翻译表"那条误判成失败 —— 反过来更糟：一句注释也能**满足**一条行为断言
//（比如注释里写了 addEventListener('change' 就让它看起来"实现了失焦即写"）。
const code = src
  .replace(/\/\*[\s\S]*?\*\//g, '')          // 块注释
  .replace(/(^|[^:])\/\/[^\n]*/g, '$1');     // 行注释（[^:] 是为了不啃掉 https://）

// 只靠源码就能判的断言（行为不好在 DOM 桩里驱动，就查代码在不在）：
//  · 文本框**失焦即写**（原脚本 setValueChangeCallback 的时机）
//  · 有「真的改了才写」的闸（否则用 Tab 过一遍焦点就把每句重写一遍 + 刷撤销栈）
//  · 单行文本框里回车当提交（blur 触发 change），不是换行
ctx.__changeWrites = /querySelectorAll\('\.lp-text'\)/.test(code) && /addEventListener\('change'/.test(code);
ctx.__dirtyGuard = /el\.value === el\.dataset\.orig/.test(code);
ctx.__enterCommits = /key === 'Enter'/.test(code) && /el\.blur\(\)/.test(code);
// 自动刷新的四道守卫（缺一个都会出事）+ 生命周期 + 间隔下限
ctx.__guards = {
  hidden: /document\.hidden/.test(code),
  editing: /正在编辑/.test(code),
  writing: /lyric\.writing/.test(code),
  dirty: /有未提交的编辑/.test(code),
};
ctx.__pollStops = /pollStop\(\)/.test(code) && /function selectTool/.test(code);
ctx.__msFloor = /v < 1000/.test(code);
// 轮询回来时页面可能已经切走了 ⇒ 收尾必须再确认一次 poll.on / 面板还在
ctx.__staleGuard = /if \(!poll\.on\) return;/.test(code) && /if \(!\$\('lyricpanel'\)\) return;/.test(code);
// 把 id 记录表交给 probe（vm 里的脚本看不见外层的 Set，得显式递进去）
ctx.__asked = asked;

const probe = vm.runInContext(`(function () {
  state.catalog = { tools: [{
    id: 'find.notes', title: '查找', kind: 'read', params: [
      { name: 'attr', options: [
        { value: 'pitch', label: '音高' },
        { value: 'lyrics', label: '歌词' },
        { value: 'language', label: '语言' },
        { value: 'ace.hasBreath', label: '有换气（有/无）' },
        { value: 'ace.breathSpace', label: '可插换气空间（秒）' },
      ] },
      { name: 'param', options: [{ value: 'tension', label: '张力' }] },
      { name: 'paramLayer', options: [{ value: 'direct', label: '直接层' }] },
    ],
  }] };
  var html = findPanelHTML();
  bindFind();
  var out = {
    html: html,
    info: find.info, cond: find.cond, scope: find.scope, target: find.target,
    help: FIND_HELP,
    attrHTML: findAttrSelectHTML(),
    cases: [],
  };
  // resultInfo 的文本格式：逐种范围/目标组合跑一遍
  function caseOf(label, hits, scope, target, cur) {
    find.hits = hits; find.scope = scope; find.target = target; find.cur = cur;
    findResultInfo(false);
    out.cases.push({ label: label, text: find.info });
  }
  out.setAttr = function (v) { find.attr = v; };
  out.attrHTMLNow = function () { return findAttrSelectHTML(); };
  var two = [
    { absTick: 100, posSv: '1.00', value: '', noteIndex: 3, clipName: 'C1', trackName: 'T1', track: 4, clip: 1 },
    { absTick: 200, posSv: '1.25', value: '', noteIndex: 5, clipName: 'C1', trackName: 'T1', track: 4, clip: 1 }
  ];
  caseOf('clip+note@0', two, 'clip', 'note', 0);
  caseOf('clip+note@1', two, 'clip', 'note', 1);
  caseOf('project+note@1', two, 'project', 'note', 1);
  caseOf('track+note@0', two, 'track', 'note', 0);
  caseOf('clip+param@0', [{ absTick: 100, posSv: '1.00', value: '0.5~2', noteIndex: -1, clipName: 'C1', track: 4, clip: 1 }], 'clip', 'param', 0);
  caseOf('empty', [], 'clip', 'note', 0);
  out.disabledAt = {};
  find.hits = two; find.cur = 0; find.scope = 'clip'; findDraw();
  out.disabledAt.first = { prev: !!document.getElementById('fpprev').disabled, next: !!document.getElementById('fpnext').disabled, all: !!document.getElementById('fpselall').disabled };
  find.cur = 1; findDraw();
  out.disabledAt.last = { prev: !!document.getElementById('fpprev').disabled, next: !!document.getElementById('fpnext').disabled, all: !!document.getElementById('fpselall').disabled };
  find.cur = 0; find.scope = 'track'; findDraw();
  out.disabledAt.trackScopeAll = !!document.getElementById('fpselall').disabled;

  // ---- 歌词面板：造一份假的服务端回包，渲染后检查 id、行数与联动 ----
  out.lyricCheck = function () {
    lyric.data = {
      page: 0, perPage: 12, pageCount: 2, sentenceCount: 3,
      firstSentenceNo: 1, lastSentenceNo: 3, canPrev: false, canNext: true,
      info: '共2页3句歌词\\n当前为第1页\\n第1到3句', mode: 'word', scope: 'clip',
      clipName: 'C1', selectedCount: 0,
      rows: [
        { index: 0, text: 'ha ppy', aceLyric: 'ha ppy', noteCount: 2, beginTick: 0, absBegin: 28800, barBeat: '16.1.0', noteLyrics: ['ha', 'ppy'] },
        { index: 1, text: '啦 啦', aceLyric: '啦 啦', noteCount: 2, beginTick: 960, absBegin: 29760, barBeat: '17.1.0', noteLyrics: ['啦', '啦'] },
        { index: 2, text: 'ah', aceLyric: 'ah', noteCount: 1, beginTick: 1920, absBegin: 30720, barBeat: '18.1.0', noteLyrics: ['ah'] },
      ],
    };
    // ⚠️ before 要在 lyricRender **之前**取 —— lyricRender 内部就会调 bindLyric
    var before = new Set(__asked);
    lyricRender();
    var html = document.getElementById('lyricpanel').innerHTML;
    // ⚠️ matchAll 返回的是**迭代器**，不是数组 —— 用 [...it] 取，别用 [].map.call（那样静默得空）
    // ⚠️ 这一段跑在模板字符串里：正则别写反斜杠-d（会被外层吃掉变成 d），
    //    用 [0-9] 这种不带反斜杠的写法最稳。
    var ids = new Set([...html.matchAll(/id="([^"]+)"/g)].map(function (m) { return m[1]; }));
    // 容器/提示/结果这三个 id 来自 lyricPanelHTML()（外层骨架），不在 lyricRender() 写进容器的这段里
    [...lyricPanelHTML().matchAll(/id="([^"]+)"/g)].forEach(function (m) { ids.add(m[1]); });
    var bound = [...__asked].filter(function (i) { return !before.has(i); });
    var rows = (html.match(/class="lp-text"/g) || []).length;
    var gotoVals = [...html.matchAll(/<option value="([0-9]+)"/g)].map(function (m) { return m[1]; });
    // 「和 js 同 ui」的几个结构判据：行内不许有按钮、两个翻页按钮各自整行、没有范围下拉
    var order = ['lpinfo', 'lpsplit', 'lprow0', 'lprow1', 'lprow2', 'lpprev', 'lpnext', 'lpgoto']
      .map(function (id) { return html.indexOf('id="' + id + '"'); });
    var ordered = order.every(function (pos, i) { return pos >= 0 && (i === 0 || pos > order[i - 1]); });
    return {
      ids: ids, bound: bound, rows: 3, rowCount: rows,
      missing: bound.filter(function (id) { return !ids.has(id); }),
      hasInfo: ids.has('lpinfo'), hasSplit: ids.has('lpsplit'),
      hasPrev: ids.has('lpprev'), hasNext: ids.has('lpnext'),
      // 原脚本：行文本框里**没有按钮**；两个翻页按钮**各自整行**（不是并排的容器）
      writeButtons: (html.match(/lp-write/g) || []).length,
      widePrev: /class="btn lp-wide" id="lpprev"/.test(html),
      wideNext: /class="btn lp-wide" id="lpnext"/.test(html),
      // 原脚本面板上**没有范围控件**（它是自动的：有选中就用选中的）
      hasScopeSelect: ids.has('lpscope'),
      // 行序必须与 getSidePanelSectionState() 一致
      ordered: ordered,
      orderPos: order,
      prevDisabled: /id="lpprev"[^>]*disabled/.test(html),
      nextDisabled: /id="lpnext"[^>]*disabled/.test(html),
      gotoOptions: gotoVals.length,
      gotoJumpsTo: gotoVals.length > 1 ? parseInt(gotoVals[1], 10) : -1,
      // 刷新按钮在面板**外面**（被复刻的那 6 行里不该多一个原脚本没有的控件）
      refreshOutside: lyricPanelHTML().indexOf('lprefresh') > lyricPanelHTML().indexOf('lyricpanel'),
      // 失焦即写 + 「真的改了才写」的闸 + 回车当提交
      changeWrites: __changeWrites, dirtyGuard: __dirtyGuard, enterCommits: __enterCommits,
      // 自动刷新：控件在面板外，且四道守卫与生命周期都得在
      hasAuto: ids.has('lpauto'), hasAutoMs: ids.has('lpautoms'), hasAutoStat: ids.has('lpautostat'),
      autoOutside: lyricPanelHTML().indexOf('lpauto') > lyricPanelHTML().indexOf('lyricpanel'),
      guards: __guards, pollStopsOnSwitch: __pollStops, msFloor: __msFloor, staleGuard: __staleGuard,
    };
  };

  // ---- 工具面板：按钮网格 + 说明框，以及"每个按钮能不能映射到工具" ----
  out.noteopsCheck = function () {
    var before = new Set(__asked);
    state.current = { id: 'note.join' };   // 让"当前页"那个按钮高亮
    var html = noteopsPanelHTML();
    bindNoteops();
    var ids = new Set([...html.matchAll(/id="([^"]+)"/g)].map(function (m) { return m[1]; }));
    var bound = [...__asked].filter(function (i) { return !before.has(i); });
    // 按钮文案（去掉可能包着的 span）
    var buttons = [...html.matchAll(/class="btn [^"]*np-btn" type="button"[^>]*>([^<]+)</g)]
      .map(function (m) { return m[1]; });
    // 每个按钮的文案能不能映射出工具（拼错就会点不动）
    var unmapped = [], used = new Set();
    buttons.forEach(function (label) {
      var r = noteopsArgs(label, null);
      if (!r || !r[0]) unmapped.push(label); else used.add(r[0]);
    });
    return {
      ids: ids, bound: bound, buttons: buttons,
      missing: bound.filter(function (id) { return !ids.has(id); }),
      twoPerRow: (html.match(/class="np-two"/g) || []).length,
      hasHelpSelect: ids.has('nphelp'), hasHelpBox: ids.has('nphelpbox'),
      helpEntries: noteopsHelp.length,
      hasDryCheckbox: ids.has('npdry'),
      hasPasteInput: ids.has('nptext'), hasClipBtn: ids.has('npfromclip'),
      unmapped: unmapped, toolsUsed: [...used].sort(),
    };
  };
  return out;
})()`, ctx, { filename: 'probe.js' });

const ids = new Set([...probe.html.matchAll(/id="([^"]+)"/g)].map((m) => m[1]));
const bound = [...asked];
const missing = bound.filter((id) => !ids.has(id));

let fail = 0;
function check(cond, msg) {
  if (!cond) { console.error('✗ ' + msg); fail++; } else { console.log('✓ ' + msg); }
}
const byLabel = (l) => (probe.cases.find((c) => c.label === l) || {}).text || '';

check(ids.size >= 12, `面板生成了 ${ids.size} 个 id`);
check(missing.length === 0, missing.length ? 'bindFind 取了不存在的 id：' + missing.join(', ') : 'bindFind 取的 id 全都存在（' + bound.length + ' 个）');

check(probe.info === '无内容', '初始 info = 「无内容」');
check(probe.cond === '2', '初始匹配条件 = 「等于」（原脚本 condition.setValue(2)）');
check(probe.scope === 'clip', '初始查找范围 = 「当前片段」');
check(probe.target === 'note', '初始目标 = 「音符属性」（checkValue1 = true）');

const h = probe.html;
check(['全选目标音符', '上一项', '下一项', '刷新', '说明'].every((s) => h.includes(s)), '五个按钮的字面都对');
check(h.includes('音符属性') && h.includes('参数'), '两个复选框都在');
check((h.match(/查找内容/g) || []).length === 2, '「查找内容」出现两次（原脚本 zh-cn 里 Search Text 与 Target 同名）');
check(['大于', '小于', '等于', '包含'].every((s) => h.includes(s)), '四个匹配条件都在');
check(['当前片段', '当前轨道', '当前工程'].every((s) => h.includes(s)), '三个范围都在（ACE 的词是「片段」）');
check(h.includes('参数层') && !h.includes('我们加的') && !/class="fp-tag"/.test(h),
  '参数层在，但**没有「我们加的」那个标签**（用户 2026-10-14：「我们加的标签去掉」）');
check(probe.help.startsWith('【查找脚本 使用说明】'), '说明文本用原脚本 zh-cn 的字面');
check(!/SV\.T\(/.test(code) && !/getTranslations/.test(code), '代码里没有留下翻译表机制（注释里提到不算）');

// fpattr（重写过的那个下拉）：用户 2026-10-14「把原来的和新的标签去掉」⇒ **平铺、没有 optgroup**
const a = probe.attrHTML;
check(/<select id="fpattr">/.test(a), 'fpattr 是个 select');
check((a.match(/<optgroup /g) || []).length === 0, 'fpattr **没有 optgroup**（原来的/新的那两个标签都去掉了）');
check(!a.includes('原脚本 · ACE 能做') && !a.includes('ACE 独有'), '那两个字面标签不再出现');
check(a.includes('音高') && a.includes('歌词'), '原脚本那几条还在');
check(a.includes('有换气（有/无）') && a.includes('可插换气空间（秒）'), 'ACE 独有那几条还在（只是不带 ★ 了）');
check(!a.includes('★'), '标签里没有 ★ 了（用户 2026-10-14：「把参数前的星号去掉」）');
check(!/（已移除的属性/.test(a), '当前选中项在列表里时不该出占位项');
check(a.includes('selected'), '要有 selected 标出当前项');
// 选中一个**已经被移出下拉**的 key（用户裁定去掉的那些）⇒ 补占位，别让浏览器静默选中第一项
probe.setAttr('detune');
const stale = probe.attrHTMLNow();
check(stale.includes('（已移除的属性：detune）'), '选中的 key 不在列表里时补一条占位');
check(/value="detune" selected/.test(stale), '占位项要 selected');
probe.setAttr('pitch');

// ---------- 歌词面板（lyric.pages）：**和 js 同 ui** ----------
// 同样的 id 对账：面板是手写 HTML，id 拼错只表现为"按钮没反应"。
const lp = probe.lyricCheck();
check(lp.ids.size >= 6, `歌词面板生成了 ${lp.ids.size} 个 id`);
check(lp.missing.length === 0, lp.missing.length
  ? '歌词面板 bindLyric 取了不存在的 id：' + lp.missing.join(', ')
  : '歌词面板 bindLyric 取的 id 全都存在（' + lp.bound.length + ' 个）');
check(lp.hasInfo && lp.hasSplit && lp.hasPrev && lp.hasNext,
  '信息框 / 分割成字符 / 上一页 / 下一页都在（照原脚本的行）');
check(lp.rowCount === lp.rows, `一行 = 一句（${lp.rows} 句 → ${lp.rowCount} 个文本框）`);
// 「同 ui」的四条结构判据
check(lp.writeButtons === 0, '行文本框里**没有按钮**（原脚本的行只有 TextArea）');
check(lp.widePrev && lp.wideNext, '上一页 / 下一页各自**整行**（原脚本是两个独立 Container，不是并排）');
check(lp.hasScopeSelect === false, '面板上**没有范围控件**（原脚本是自动的：有选中就用选中的）');
check(lp.ordered === true, '行序与 getSidePanelSectionState() 一致：'
  + '信息 → 分割成字符 → 各行 → 上一页 → 下一页 → 下拉'
  + (lp.ordered ? '' : '（实际位置 ' + JSON.stringify(lp.orderPos) + '）'));
check(lp.refreshOutside === true, '「刷新」在面板外面（不进被复刻的那几行）');
check(lp.prevDisabled === true && lp.nextDisabled === false, '第 1 页：上一页禁用、下一页可用');
check(lp.gotoOptions === lp.rows, '下拉里是本页的句子');
// ⚠️ 原脚本的跳句下拉取的是**下一句**（选项 0 起、ComboBox 值 1 起 ⇒ 差一位）；这里不照搬
check(lp.gotoJumpsTo === 1, '跳句按**你选中的那一句**跳（不照搬原脚本的 off-by-one）');
check(lp.changeWrites === true, '文本框**失焦即写**（原脚本 setValueChangeCallback 的时机）');
check(lp.dirtyGuard === true, '有「真的改了才写」的闸（Tab 过一遍焦点不会重写歌词）');
check(lp.enterCommits === true, '单行文本框里回车 = 提交（blur），不是换行');

// ---------- 自动刷新（宿主一改就刷新；ACE 没有推送，只能轮询）----------
check(lp.hasAuto && lp.hasAutoMs && lp.hasAutoStat,
  '自动刷新控件在（开关 / 间隔 / 状态）');
check(lp.autoOutside === true, '自动刷新控件在面板**外面**（不进被复刻的那几行）');
check(lp.guards.hidden === true, '守卫①：标签页不可见就不轮询');
check(lp.guards.editing === true, '守卫②：正在编辑文本框就不动（否则会覆盖你正在打的字）');
check(lp.guards.dirty === true, '守卫③：有未提交的编辑就不动');
check(lp.guards.writing === true, '守卫④：写请求在飞就不动');
check(lp.pollStopsOnSwitch === true, '换工具就停轮询（只有歌词页需要它）');
check(lp.msFloor === true, '轮询间隔有 1 s 下限（一次轮询本身约 225 ms）');
check(lp.staleGuard === true, '轮询回来时页面已切走 ⇒ 不再动 DOM（否则打在 null 上会抛错）');
// 「变更令牌是只读的」由 Go 侧 TestRevisionToolRegistered 卡住（面板自检看不到注册表）

// ---------- 工具面板（AKD工具侧栏.js 的点名项）----------
const np = probe.noteopsCheck();
check(np.ids.size >= 8, `工具面板生成了 ${np.ids.size} 个 id`);
check(np.missing.length === 0, np.missing.length
  ? '工具面板 bindNoteops 取了不存在的 id：' + np.missing.join(', ')
  : '工具面板 bindNoteops 取的 id 全都存在（' + np.bound.length + ' 个）');
// 用户点名的 9 个动作都要有按钮
const wantBtns = ['自动拼接音符', '选中之后的所有音符', '选中之前的所有音符', '复制歌词',
  '粘贴所选音符歌词', '添加 + 并后移', '添加 - 并后移', '合并歌词并前移', '删除歌词并前移',
  '按播放头分割轨道'];
check(np.buttons.length === wantBtns.length,
  `按钮数 ${np.buttons.length}，期望 ${wantBtns.length}：` + np.buttons.join(' / '));
const btnsMissing = wantBtns.filter((w) => np.buttons.indexOf(w) < 0);
check(btnsMissing.length === 0, btnsMissing.length
  ? '少了按钮：' + btnsMissing.join(' / ') : '九个点名动作的按钮都在');
// 版式照原脚本：两列一行 + 末尾 Label/ComboBox/TextArea
check(np.twoPerRow >= 4, `有两列一行的按钮行（${np.twoPerRow} 组）`);
check(np.hasHelpSelect && np.hasHelpBox, '末尾有说明 ComboBox + TextArea（照原脚本）');
check(np.helpEntries === wantBtns.length,
  `说明条目 ${np.helpEntries} 条，期望与按钮数（${wantBtns.length}）一致`);
// 每个按钮都要能被映射到一个工具（靠文字识别，文案改了却没改映射就会点不动）
check(np.unmapped.length === 0, np.unmapped.length
  ? '这些按钮的文案映射不出工具（点了会没反应）：' + np.unmapped.join(' / ')
  : '每个按钮的文案都能映射到工具');
// 10 个动作落在 7 个工具上（+/- 共用、之后/之前共用、合并/删除共用）
check(np.toolsUsed.length === 7, `10 个动作落在 ${np.toolsUsed.length} 个工具上，期望 7 个：`
  + np.toolsUsed.join(', '));
check(np.hasDryCheckbox === true, '有「干跑」复选框（批量操作想先看一眼时用）');
check(np.hasPasteInput && np.hasClipBtn, '有粘贴文本框 + 「从剪贴板读」（浏览器剪贴板 API）');

check(byLabel('clip+note@0').startsWith('序号: 1/2'), '片段范围 displayNum 正着数（1/2）');
check(!byLabel('clip+note@0').includes('轨道:') && !byLabel('clip+note@0').includes('片段:'),
  '片段范围不给 轨道/片段 两行（原脚本 if (!isGroup) 才给）');
check(byLabel('clip+note@1').startsWith('序号: 2/2'), '片段范围第 2 项 = 2/2');
check(byLabel('project+note@1').startsWith('序号: 1/2'), '轨道/工程范围 displayNum 倒着数（index=1 → 1/2）');
check(byLabel('project+note@1').includes('轨道: T1') && byLabel('project+note@1').includes('片段: C1'),
  '工程范围给 轨道 + 片段');
check(byLabel('track+note@0').includes('片段: C1') && !byLabel('track+note@0').includes('轨道:'),
  '轨道范围只给 片段');
check(byLabel('clip+note@0').includes('音符序号: 3') && byLabel('clip+note@0').includes('位置: 1.00'),
  '音符命中给「音符序号」+「位置」');
check(byLabel('clip+param@0').includes('数值: 0.5~2') && !byLabel('clip+param@0').includes('音符序号'),
  '参数命中给「数值」、不给「音符序号」');
check(byLabel('empty') === '无搜索结果', '空结果 = 「无搜索结果」');

const d = probe.disabledAt;
check(d.first.prev === true && d.first.next === false, '第 1 项：上一项禁用、下一项可用');
check(d.last.prev === false && d.last.next === true, '最后一项：上一项可用、下一项禁用');
check(d.first.all === false, '范围=片段 ⇒ 全选目标音符可用');
check(d.trackScopeAll === true, '范围=轨道 ⇒ 全选目标音符禁用（原脚本 setEnabled(isGroup)）');

console.log(fail ? `\n${fail} 项没过` : '\n三个面板（查找 + 歌词 + 工具）的自检全部通过');
process.exit(fail ? 1 : 0);
