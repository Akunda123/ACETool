'use strict';

/* ACETool 面板前端。
 *
 * 两个刻意的约束：
 *  1. **不写死任何工具** —— 工具清单和表单 schema 都从 /api/tools 拿，
 *     加一个工具不需要动这里一行（见 internal/tools/registry.go 的说明）。
 *  2. **失败按错误报告展示** —— exit code / error.code / error.hint 原样摆出来，
 *     不翻译成"出错了"；命令日志抽屉里每个调用都留了 argv + 两个流。
 */

const API_HEADER = 'X-ACETool';
const $ = (id) => document.getElementById(id);

const state = { catalog: null, current: null, values: {}, log: [], busy: false };

/* ---------------- 通用小工具 ---------------- */

function esc(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, (c) =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

/** 把 argv 拼成能直接粘进终端的形状（带空格的加引号）。 */
function fmtCmd(args) {
  return (args || []).map((a) => (/[\s"]/.test(a) ? JSON.stringify(a) : a)).join(' ');
}

const NOTE_NAMES = ['C', 'C#', 'D', 'D#', 'E', 'F', 'F#', 'G', 'G#', 'A', 'A#', 'B'];

/* 所有输入框统一带这串属性（用户 2026-10-06：网页上不保存历史记录信息）——
 * 否则浏览器会在框上弹「保存的信息 / 上次使用」那种历史下拉，正好盖住面板本身。 */
const NOFILL = 'autocomplete="off" autocapitalize="off" autocorrect="off" spellcheck="false"';

/** MIDI 音高 → 音名（C4 = 60，与 ACE / SV 一致）。 */
function pitchName(p) {
  if (typeof p !== 'number') return '';
  return NOTE_NAMES[((p % 12) + 12) % 12] + (Math.floor(p / 12) - 1);
}

/** 通用表格：cols 是 [{title, cls}]，cell 是对象就序列化。 */
function table(cols, rows) {
  const th = cols.map((c) => '<th' + (c.cls ? ' class="' + c.cls + '"' : '') + '>' + esc(c.title) + '</th>').join('');
  const tb = rows.map((r) => '<tr>' + cols.map((c, i) => {
    let v = r[i];
    if (v == null || v === '') v = '<span class="dim">—</span>';
    else if (typeof v === 'object') v = esc(JSON.stringify(v));
    else v = esc(v);
    return '<td' + (c.cls ? ' class="' + c.cls + '"' : '') + '>' + v + '</td>';
  }).join('') + '</tr>').join('');
  return '<table class="tbl"><thead><tr>' + th + '</tr></thead><tbody>' + tb + '</tbody></table>';
}

function emptyHint(list, msg) {
  return (list && list.length) ? '' : '<div class="hint">' + esc(msg) + '</div>';
}

/* ---------------- 顶栏自检 ---------------- */

async function selfcheck() {
  const ov = $('overall');
  ov.className = 'pill idle';
  ov.textContent = '检测中…';
  try {
    const res = await fetch('/api/selfcheck', { cache: 'no-store' });
    const d = await res.json();
    let cls = 'warn', text = 'ACE 离线';
    if (!d.cliPath) { cls = 'bad'; text = '未检测到 ACE Studio'; }
    else if (d.online && d.ok) { cls = 'ok'; text = '就绪'; }
    else if (d.online) { cls = 'warn'; text = '在线，但有步骤没过'; }
    ov.className = 'pill ' + cls;
    ov.textContent = text;

    const bits = [];
    const v = d.version || {};
    if (v.cli) bits.push('cli ' + v.cli);
    if (v.forAce) bits.push(v.forAce);
    if (v.surface) bits.push(v.surface);
    if (d.project) {
      bits.push('工程：' + (d.project.projectName || '(未命名/临时)'));
      if (typeof d.project.duration === 'number') bits.push((d.project.duration / 480).toFixed(1) + ' 拍');
    }
    let meta = bits.join(' · ');
    if (d.hint) meta += '　⚠ ' + d.hint;
    $('meta').textContent = meta;
  } catch (e) {
    ov.className = 'pill bad';
    ov.textContent = '自检失败';
    $('meta').textContent = String((e && e.message) || e);
  }
}

/* ---------------- 工具清单 / 侧栏 ---------------- */

async function loadTools() {
  try {
    const res = await fetch('/api/tools', { cache: 'no-store' });
    state.catalog = await res.json();
    // 查找面板（find.js）要一份工具清单才知道属性/参数下拉里有哪些项 ——
    // 它读不到我们的 `const state` ⇒ 主动递过去（2026-10-08）
    if (window.FindPanel && window.FindPanel.setCatalog) window.FindPanel.setCatalog(state.catalog);
    renderSidebar();
    // 深链优先：带 `#工具id` 就落到那一页，否则选第一个
    if (!selectByHash()) {
      const first = (state.catalog.tools || [])[0];
      if (first) selectTool(first.id);
    }
  } catch (e) {
    $('sidebar').innerHTML = '<div class="cat">工具清单载入失败</div><div class="cat">' + esc((e && e.message) || e) + '</div>';
  }
}

function renderSidebar() {
  const c = state.catalog;
  if (!c) return;
  const byCat = {};
  (c.tools || []).forEach((t) => { (byCat[t.category] = byCat[t.category] || []).push(t); });
  let html = '';
  (c.categories || Object.keys(byCat)).forEach((cat) => {
    if (!byCat[cat]) return;
    html += '<div class="cat">' + esc(cat) + '</div>';
    byCat[cat].forEach((t) => {
      const active = state.current && state.current.id === t.id ? ' active' : '';
      const kind = t.kind === 'write' ? '写' : '读';
      html += '<button class="tool' + active + '" data-id="' + esc(t.id) + '" type="button">' +
        '<span class="kind">' + kind + '</span>' + esc(t.title) + '</button>';
    });
  });
  if (!c.writable) {
    html += '<div class="cat">写工具还没启用（M2）</div>';
  }
  $('sidebar').innerHTML = html;
  $('sidebar').querySelectorAll('.tool').forEach((b) => {
    b.addEventListener('click', () => selectTool(b.dataset.id));
  });
}

function selectTool(id) {
  const t = (state.catalog.tools || []).find((x) => x.id === id);
  if (!t) return;
  if (cycle.on) cycleStop('换了工具，已停'); // 别让循环在后台继续写
  pollStop();                                // 换工具就停轮询（只有歌词页需要它）
  state.current = t;
  state.values = {};
  (t.params || []).forEach((p) => { state.values[p.name] = p.default != null ? p.default : ''; });
  renderSidebar();
  renderTool();
}

/* ---------------- 工具表单 ---------------- */

function fieldHTML(p) {
  const v = state.values[p.name] != null ? state.values[p.name] : '';
  if (p.type === 'check') {
    const checked = (String(v) === '1' || String(v) === 'true') ? ' checked' : '';
    return '<div class="check"><input type="checkbox" data-name="' + esc(p.name) + '"' + checked + '>' +
      '<label>' + esc(p.label) + '</label></div>';
  }
  let input;
  if (p.type === 'select') {
    input = '<select data-name="' + esc(p.name) + '">' + (p.options || []).map((o) =>
      '<option value="' + esc(o.value) + '"' + (String(o.value) === String(v) ? ' selected' : '') + '>' +
      esc(o.label) + '</option>').join('') + '</select>';
  } else if (p.type === 'textarea') {
    // 多行文本框：贴一整段简谱用。值收集器用 `[data-name]` + `el.value`，textarea 天然满足。
    input = '<textarea data-name="' + esc(p.name) + '" rows="' + (p.rows || 8) + '" ' + NOFILL +
      (p.placeholder ? ' placeholder="' + esc(p.placeholder) + '"' : '') + '>' + esc(v) + '</textarea>';
  } else {
    const type = p.type === 'number' ? 'number' : 'text';
    input = '<input type="' + type + '" data-name="' + esc(p.name) + '" ' + NOFILL + ' value="' + esc(v) + '"' +
      (p.placeholder ? ' placeholder="' + esc(p.placeholder) + '"' : '') + '>';
  }
  return '<div class="field"><label>' + esc(p.label) + '</label>' + input +
    (p.hint ? '<div class="hint">' + esc(p.hint) + '</div>' : '') + '</div>';
}

function renderTool() {
  const t = state.current;
  const params = t.params || [];
  const kindNote = t.kind === 'write'
    ? '⚠️ 写工具 —— 会真的改动 ACE 工程（勾上「干跑」则只出计划）'
    : '只读 · 不会改动你的工程';
  // 专用面板：查找（`find.js`，主面板与独立窗口 `find.html` 共用）/
  // 歌词（都逐条复刻对应 SV 脚本的 SidePanelSection）、
  // 工具面板（AKD工具侧栏.js 的点名项，6 个页面共用）、变色测试台、
  // 谱面编辑器（简谱 + 五线谱，见 notation.js / notation-glyphs.js）。
  // 其余工具走 /api/tools 里的 schema 自动生成表单 —— 加工具不用动这里。
  const custom = t.id === 'find.notes' ? (window.FindPanel ? window.FindPanel.html() : '')
    : (t.id === 'lyric.pages' ? lyricPanelHTML()
      : (NOTEOPS.indexOf(t.id) >= 0 ? noteopsPanelHTML()
        : (t.id === 'color.cycle' ? cyclePanelHTML()
          : (t.id === 'jianpu.editor' && window.NotationPanel ? NotationPanel.html()
            : (t.id === 'view.minimap' && window.MinimapPanel ? MinimapPanel.html() : '')))));
  const showForm = !custom;
  $('main').innerHTML =
    '<h2 class="title">' + esc(t.title) + '</h2>' +
    // 工具描述：**最多两行**，全文挪到悬停提示（用户 2026-10-05：界面上不要加无关的字）
    '<div class="desc clamp" title="' + esc(t.desc) + '">' + esc(t.desc) + '</div>' +
    (showForm && params.length ? '<div class="form">' + params.map(fieldHTML).join('') + '</div>' : '') +
    custom +
    (showForm
      ? '<div><button class="btn" id="run" type="button">执行一次</button> <span class="dim">' + esc(kindNote) + '</span></div>'
      : '') +
    '<div id="result"></div>';
  if (showForm) {
    bindForm();
    const btn = $('run');
    if (btn) btn.addEventListener('click', run);
  }
  if (t.id === 'find.notes' && window.FindPanel) window.FindPanel.bind();
  if (t.id === 'lyric.pages') { lyricLoad(); pollStart(); }
  if (NOTEOPS.indexOf(t.id) >= 0) bindNoteops();
  if (t.id === 'jianpu.editor' && window.NotationPanel) NotationPanel.bind();
  if (t.id === 'view.minimap' && window.MinimapPanel) MinimapPanel.bind();

  if (t.id === 'color.cycle') {
    const toggle = $('cycletoggle');
    if (toggle) {
      toggle.addEventListener('click', () => { if (cycle.on) { cycleStop('已暂停'); } else { cycleStart(); } });
      toggle.textContent = cycle.on ? '⏸ 暂停' : '▶ 开始变色';
      toggle.classList.toggle('danger', cycle.on);
    }
    const sw = $('swatch');
    if (sw && cycle.color) sw.style.background = cycle.color;
    const st = $('cyclestate');
    if (st && cycle.steps) st.textContent = '第 ' + cycle.steps + ' 步 · ' + (cycle.color || '—');
  }
}

function bindForm() {
  $('main').querySelectorAll('[data-name]').forEach((el) => {
    const name = el.dataset.name;
    const read = () => {
      state.values[name] = el.type === 'checkbox' ? (el.checked ? '1' : '0') : el.value;
    };
    read();
    el.addEventListener('change', read);
    el.addEventListener('input', read);
  });
}

/* ---------------- 执行 ---------------- */

/** 统一入口：POST /api/run。HTTP 非 2xx ⇒ 抛 Error，其余原样返回。 */
async function postRun(toolId, values) {
  const res = await fetch('/api/run', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', [API_HEADER]: '1' },
    body: JSON.stringify({ toolId, values }),
  });
  let data = null;
  try { data = await res.json(); } catch (e) { /* 保持 null，用 HTTP 状态报错 */ }
  if (!res.ok) throw new Error((data && data.error) || ('HTTP ' + res.status));
  return data;
}

async function run() {
  if (state.busy || !state.current) return;
  state.busy = true;
  const btn = $('run');
  if (btn) btn.disabled = true;
  $('result').innerHTML = '<div class="hint ok">执行中…（命令串行跑，单条 30 s 超时）</div>';
  try {
    const data = await postRun(state.current.id, state.values);
    renderRun(data);
    (data.calls || []).forEach(pushLog);
  } catch (e) {
    $('result').innerHTML = '<div class="hint bad">' + esc((e && e.message) || e) + '</div>';
  } finally {
    state.busy = false;
    const b = $('run');
    if (b) b.disabled = false;
  }
}

function renderRun(data) {
  $('result').innerHTML = renderRunHTML(data);
}

/** 回包 → HTML。抽出来是为了让查找面板能把它渲染到自己的位置（不写死 #result）。 */
function renderRunHTML(data, opts) {
  const compact = !!(opts && opts.compact);
  const calls = data.calls || [];
  const plan = data.plan || [];
  let html = '';

  // ★ 干跑计划必须渲染出来 —— 踩过：CLI 写工具干跑时回包只有 plan、calls 是空的，
  //   原来只渲染 calls ⇒ 界面显示"0 条命令全部成功"，用户根本看不到将要执行什么。
  if (plan.length) {
    html += '<div class="hint warn">干跑：<b>一条都没执行</b>。下面是将会执行的 ' + plan.length + ' 条命令。</div>';
    html += '<div class="card"><div class="card-head"><span class="pill warn">计划</span>' +
      '<span class="name">将执行的命令</span></div><div class="card-body"><pre class="code">' +
      plan.map((c) => '# ' + (c.label || '') + '\n' + fmtCmd(c.args)).join('\n\n') + '</pre></div></div>';
  }

  if (data.hint) html += '<div class="hint bad">' + esc(data.hint) + '</div>';
  else if (data.ok && !plan.length && !compact) html += '<div class="hint ok">' + calls.length + ' 条命令全部成功。</div>';
  calls.forEach((c) => { html += callCardHTML(c); });
  return html;
}

function callCardHTML(c) {
  const ok = c.exitCode === 0;
  let head = '<div class="card-head">' +
    '<span class="pill ' + (ok ? 'ok' : 'bad') + '">' + (ok ? 'exit 0' : 'exit ' + c.exitCode) + '</span>' +
    '<span class="name">' + esc(c.label) + '</span>' +
    '<span class="time">' + (c.millis || 0) + ' ms</span></div>';

  let body = '';
  if (c.spawnError) body += '<div class="hint bad">' + esc(c.spawnError) + '</div>';
  if (c.timedOut) body += '<div class="hint bad">调用超时后被终止（作业本身没被取消）。</div>';
  if (c.error) {
    body += '<div class="hint bad"><b>error[' + esc(c.error.code) + ']</b> ' + esc(c.error.message || '') +
      (c.error.hint ? '<br>ACE 的提示：' + esc(c.error.hint) : '') + '</div>';
  }
  body += renderPayload(c.json, c.stdout);

  const dump = '$ ' + fmtCmd(c.args) +
    (c.stdout ? '\n\n[stdout]\n' + c.stdout : '') +
    (c.stderr ? '\n\n[stderr]\n' + c.stderr : '');
  body += '<details class="raw"><summary>原始输出（argv / stdout / stderr）</summary>' +
    '<pre class="code">' + esc(dump) + '</pre></details>';

  return '<div class="card">' + head + '<div class="card-body">' + body + '</div></div>';
}

/* ---------------- 结果渲染：认得出的形状就给表格 ---------------- */

function shapeOf(v) {
  if (!v || typeof v !== 'object' || Array.isArray(v)) return 'raw';
  if (Array.isArray(v.tracks)) return 'tracks';
  if (Array.isArray(v.clips)) return 'clips';
  if (Array.isArray(v.notes)) return 'notes';
  if (Array.isArray(v.sentences)) return 'sentences';
  if (Array.isArray(v.signatures)) return 'signatures';
  if (Array.isArray(v.points)) return 'points';
  if (Array.isArray(v.entries)) return 'entries';
  if (Array.isArray(v.params)) return 'params';
  return 'raw';
}

function renderPayload(json, stdout) {
  if (json == null) {
    const text = (stdout || '').trim();
    return text ? '<pre class="code">' + esc(text) + '</pre>' : '<div class="dim">（无输出）</div>';
  }

  let body = '';
  switch (shapeOf(json)) {
    case 'tracks':
      body = emptyHint(json.tracks, '工程里还没有轨道。先在 ACE 里建一条内容轨道，或确认 --include-empty 勾上了。') + tableTracks(json.tracks);
      break;
    case 'clips':
      body = emptyHint(json.clips, '这条轨道上没有片段。换个 trackIndex 试试，或先在 ACE 里建一个片段。') + tableClips(json.clips);
      break;
    case 'notes':
      body = emptyHint(json.notes, '这个片段里没有音符（或片段类型没有音符：Audio / Chord 片段没有）。') + tableNotes(json.notes) + fingerprintLine(json.fingerprint);
      break;
    case 'sentences':
      body = emptyHint(json.sentences, '没有句子级歌词（Audio / Chord 片段没有）。') + tableSentences(json.sentences);
      break;
    case 'signatures':
      body = tableSignatures(json.signatures);
      break;
    case 'points':
      body = tablePoints(json.points) + fingerprintLine(json.fingerprint);
      break;
    case 'entries':
      body = tableEntries(json.entries);
      break;
    case 'params':
      body = tableParams(json.params);
      break;
    default:
      body = '';
  }

  const open = body ? '' : ' open';
  return body + '<details class="raw"' + open + '><summary>结构化原始数据</summary>' +
    '<pre class="code">' + esc(JSON.stringify(json, null, 2)) + '</pre></details>';
}

/** 指纹单列一行：之后任何写都要把它当 --if-match 带回来。 */
function fingerprintLine(fp) {
  if (!fp) return '';
  return '<div class="dim" style="margin-top:8px;font-size:12px">fingerprint（写时当 <span class="mono">--if-match</span> 带回来）：' +
    '<span class="mono">' + esc(fp) + '</span></div>';
}

function tableTracks(list) {
  return table(
    [{ title: '区带' }, { title: '索引', cls: 'num' }, { title: '类型' }, { title: '名称' },
     { title: '音源' }, { title: '片段', cls: 'num' }, { title: 'UUID', cls: 'mono' }],
    list.map((t) => [t.region, t.trackIndex, t.trackType, t.trackName, t.soundSourceName, t.clipCount, t.trackUuid]));
}

// ⚠️ 单位口径（用户 2026-10-14）：**对外一律「拍」**（1 拍 = 一个四分音符，ACE PPQ = 480 tick/拍）
// ⇒ 这些表头不再写 tick，数值一律除成拍（内部字段名还是 tick，那属于内部）。
function beats(tick) {
  return (typeof tick === 'number') ? Math.round((tick / 480) * 1000) / 1000 : tick;
}

function tableClips(list) {
  return table(
    [{ title: '类型' }, { title: '名称' }, { title: '起（拍）', cls: 'num' }, { title: '止（拍）', cls: 'num' },
     { title: '时值(拍)', cls: 'num' }, { title: '音符', cls: 'num' }, { title: '启用' },
     { title: '颜色', cls: 'mono' }, { title: 'UUID', cls: 'mono' }],
    list.map((c) => [c.clipType, c.clipName, beats(c.clipBegin), beats(c.clipEnd),
      (typeof c.clipEnd === 'number' && typeof c.clipBegin === 'number') ? ((c.clipEnd - c.clipBegin) / 480).toFixed(3) : '',
      c.noteCount, c.enabled === false ? '✗' : '✓', c.clipColor, c.clipUuid]));
}

function tableNotes(list) {
  return table(
    [{ title: '#' }, { title: '起（拍）', cls: 'num' }, { title: '时值（拍）', cls: 'num' },
     { title: '音高', cls: 'num' }, { title: '音名', cls: 'mono' }, { title: '歌词' },
     { title: '音素', cls: 'mono' }, { title: '语言' }, { title: 'UUID', cls: 'mono' }],
    list.map((n, i) => [i, beats(n.pos), beats(n.dur), n.pitch, pitchName(n.pitch), n.lyric,
      n.syllable || n.phonemes, n.language, n.noteUuid]));
}

function tableSentences(list) {
  return table(
    [{ title: '#' }, { title: '歌词' }, { title: '音符数', cls: 'num' }],
    list.map((s) => [s.index, s.lyric, Array.isArray(s.noteUuids) ? s.noteUuids.length : '']));
}

function tableSignatures(list) {
  return table(
    [{ title: 'barPos', cls: 'num' }, { title: '拍号' }, { title: '第几拍', cls: 'num' }, { title: '秒', cls: 'num' }],
    list.map((s) => [s.barPos, (s.numerator != null && s.denominator != null) ? (s.numerator + '/' + s.denominator) : '', beats(s.tick), s.sec]));
}

function tablePoints(list) {
  return table(
    [{ title: '位置（拍）', cls: 'num' }, { title: '值', cls: 'num' }, { title: 'bend', cls: 'num' }],
    list.map((p) => [beats(p.pos != null ? p.pos : p.tick), p.value, p.bend]));
}

function tableEntries(list) {
  return table(
    [{ title: 'index', cls: 'num' }, { title: '操作' }, { title: '发起方' }, { title: '已应用' }],
    list.map((e) => [e.index, e.name, e.actor, e.applied ? '✓' : '✗']));
}

function tableParams(list) {
  return table(
    [{ title: '参数' }, { title: '可用' }, { title: '层（layername:shape/access）', cls: 'wrap' }],
    list.map((p) => [p.param, p.available ? '✓' : '✗',
      (p.layers || []).map((l) => l.layer + ':' + l.shape + '/' + l.access).join('  ') +
      (p.available ? '' : '  (' + (p.unavailableReason || '不可用') + ')')]));
}

/* ---------------- 命令日志抽屉 ---------------- */

function pushLog(c) {
  state.log.unshift(c);
  if (state.log.length > 200) state.log.pop();
  renderLog();
}

function renderLog() {
  $('logcount').textContent = String(state.log.length);
  $('logbody').innerHTML = state.log.map((c) => {
    const ok = c.exitCode === 0;
    const detail =
      (c.error ? '<pre class="code">error[' + esc(c.error.code) + '] ' + esc(c.error.message || '') +
        (c.error.hint ? '\nhint: ' + esc(c.error.hint) : '') + '</pre>' : '') +
      '<pre class="code">' + esc((c.stdout || '(stdout 空)') + (c.stderr ? '\n\n[stderr]\n' + c.stderr : '')) + '</pre>';
    return '<div class="logitem">' +
      '<div class="row1">' +
      '<span class="code ' + (ok ? 'ok' : 'bad') + '">exit ' + c.exitCode + '</span>' +
      '<span class="cmd">' + esc(fmtCmd(c.args)) + '</span>' +
      '<span class="ms">' + (c.millis || 0) + ' ms</span>' +
      '</div><div class="logdetail" hidden>' + detail + '</div></div>';
  }).join('');
  $('logbody').querySelectorAll('.logitem').forEach((el) => {
    el.querySelector('.row1').addEventListener('click', () => {
      const d = el.querySelector('.logdetail');
      d.hidden = !d.hidden;
    });
  });
}

/* ---------------- 变色测试台（color.cycle 专用 UI）
 *
 * 照参考脚本 scratch/ace-color-cycle.ps1 的 -Loop：
 *   · 一步 = 一次「读当前色 → 写下一格 → 回读核对」，服务端一次 /api/run 全办完
 *   · 节奏按**实测**排：set+回读 140–180 ms ⇒ 默认 200 ms；这一步用得比间隔久就让出，
 *     **绝不并发叠加**（自调度 setTimeout，不是 setInterval）
 *   · 每步在 ACE 里是一条撤销记录（没有事务 API）⇒ 硬上限 400 步，停下时报撤销代价
 */
const cycle = {
  on: false, timer: null, steps: 0, warns: 0, checked: false,
  intervalMs: 200, maxSteps: 400, lastMs: 0, color: '', running: false,
  form: null, trackIndex: '',   // 起步快照 + 第 1 步侦察到的目标轨道
};

function cyclePanelHTML() {
  return '<div class="card" id="cyclecard">' +
    '<div class="card-head">' +
    '<span class="swatch" id="swatch"></span>' +
    '<span class="name" id="cyclestate">未开始</span>' +
    '<span class="time" id="cyclestat">—</span>' +
    '</div><div class="card-body">' +
    '<div class="row">' +
    '<button class="btn" id="cycletoggle" type="button">▶ 开始变色</button>' +
    '<span class="dim">间隔 <input type="number" id="cyclems" value="200" min="120" step="10" autocomplete="off"> ms</span>' +
    '</div>' +
    '<div class="hint" style="margin-top:12px">⚠️ 每走一步在 ACE 里是<b>一条撤销记录</b>（ACE 没有事务 API）' +
    '——停下后 Ctrl+Z 要按那么多次。硬上限 400 步。' +
    '<b>只有第 1 步要侦察</b>（读 caret / 读当前色），之后每步复用目标与上一步写出去的色 ⇒ ' +
    '<b>一步一条 track set</b>；回读默认关。' +
    '某步超出间隔时会让出，不并发叠加。</div>' +
    '</div></div>';
}

function cycleStart() {
  cycle.on = true;
  cycle.steps = 0;
  cycle.warns = 0;
  cycle.checked = false;
  cycle.lastMs = 0;
  cycle.trackIndex = '';
  cycle.form = Object.assign({}, state.values); // 起步快照：跑起来后改表单不影响本次循环
  const el = $('cyclems');
  const ms = el ? parseInt(el.value, 10) : NaN;
  cycle.intervalMs = (isNaN(ms) || ms < 120) ? 200 : ms;
  const btn = $('cycletoggle');
  if (btn) { btn.textContent = '⏸ 暂停'; btn.classList.add('danger'); }
  const st = $('cyclestate');
  if (st) st.textContent = '正在变色…';
  cycleStep();
}

function cycleStop(reason) {
  cycle.on = false;
  if (cycle.timer) { clearTimeout(cycle.timer); cycle.timer = null; }
  const btn = $('cycletoggle');
  if (btn) { btn.textContent = '▶ 开始变色'; btn.classList.remove('danger'); }
  const st = $('cyclestate');
  if (st) st.textContent = reason || '已暂停';
  cycleSummary(reason);
}

async function cycleStep() {
  if (!cycle.on || cycle.running) return;
  if (cycle.steps >= cycle.maxSteps) { cycleStop('到 ' + cycle.maxSteps + ' 步硬上限，自动停'); return; }
  cycle.running = true;
  const t0 = now();
  try {
    const vals = Object.assign({}, cycle.form || state.values);
    // 第 2 步起复用第 1 步侦察到的目标轨道 + 上一步写出去的色
    // ⇒ 本步一次读都不发（trackIndex 不再问 caret、color 不再 track get），只有一条 track set。
    if (cycle.steps > 0) {
      if (cycle.trackIndex !== '') vals.trackIndex = cycle.trackIndex;
      if (cycle.color) vals.fromHex = cycle.color;
    }
    const data = await postRun('color.cycle', vals);
    (data.calls || []).forEach(pushLog);
    cycle.steps++;
    const back = readBackColor(data);
    const want = wantedColor(data);
    const idx = usedTrackIndex(data);
    if (idx !== '') cycle.trackIndex = idx;
    // 回读默认关 ⇒ 就拿我们写出去的那个色来显示（否则关了回读色块就没数据了）
    if (back) { cycle.color = back; cycle.checked = true; } else if (want) { cycle.color = want; }
    if (want && back && want.toLowerCase() !== back.toLowerCase()) {
      cycle.warns++;
      pushLog({
        label: '回读核对不符', args: ['（核对）'], exitCode: 1, millis: 0,
        stdout: 'asked ' + want + ', read back ' + back,
      });
    }
    renderCycleLive(cycle.color, (data.calls || []).length);
  } catch (e) {
    cycle.running = false;
    cycleStop('失败：' + ((e && e.message) || e));
    return;
  }
  cycle.running = false;
  const work = Math.round(now() - t0);
  cycle.lastMs = work;
  const wait = Math.max(0, cycle.intervalMs - work);
  if (cycle.on) cycle.timer = setTimeout(cycleStep, wait);
}

function now() { return (window.performance && window.performance.now) ? window.performance.now() : Date.now(); }

/** 想要的色：从 `track set … --color <hex>` 的 argv 里取（别去解析给人看的 label）。 */
function wantedColor(data) {
  for (const c of (data.calls || [])) {
    const a = c.args || [];
    const i = a.indexOf('--color');
    if (i >= 0 && a[i + 1]) return a[i + 1];
  }
  return '';
}

/** 回读到的色：从 `track get` 的 JSON `.color` 取。 */
function readBackColor(data) {
  for (const c of (data.calls || [])) {
    if (c.json && typeof c.json.color === 'string') return c.json.color;
  }
  return '';
}

/** 本步实际用的轨道索引：从 argv 取，别解析给人看的 label（干跑时从 plan 取）。 */
function usedTrackIndex(data) {
  for (const pool of [data.calls || [], data.plan || []]) {
    for (const c of pool) {
      const a = c.args || [];
      const i = a.indexOf('--track-index');
      if (i >= 0 && a[i + 1]) return a[i + 1];
    }
  }
  return '';
}

function renderCycleLive(col, cmdCount) {
  const sw = $('swatch');
  if (sw && col) sw.style.background = col;
  const st = $('cyclestate');
  if (st) st.textContent = '第 ' + cycle.steps + ' 步 · ' + (col || '—');
  const stat = $('cyclestat');
  if (stat) {
    stat.textContent = cycle.lastMs + ' ms/步' +
      (cmdCount ? ' · ' + cmdCount + ' 条命令' : '') +
      (cycle.warns ? ' · ' + cycle.warns + ' 次回读不符' : '');
  }
}

function cycleSummary(reason) {
  const box = $('result');
  if (!box || !cycle.steps) return;
  const tail = cycle.warns
    ? '；' + cycle.warns + ' 步回读核对不符（看命令日志）'
    : (cycle.checked
      ? '；每步回读都核对通过'
      : '；未做回读核对（要核对就在上面勾「写后回读核对」，代价是每步多一次往返）');
  const text = (reason ? reason + ' · ' : '') +
    '共走 ' + cycle.steps + ' 步；撤销栈 +' + cycle.steps + '（在 ACE 里 Ctrl+Z，一次一步）' + tail;
  box.innerHTML = '<div class="hint ' + (cycle.warns ? 'bad' : 'ok') + '">' + esc(text) + '</div>' + box.innerHTML;
}

/* ---------------- 查找面板（find.notes 专用 UI） ----------------
 *
 * 面板本体已经搬进 **`find.js`**（`window.FindPanel`）—— 用户 2026-10-08：
 * 「我要的是进入 home.html 的正式功能，新的 web 页」⇒ 有了独立的 `find.html` 子窗口。
 * 独立窗口**不加载 app.js**（那是整个主面板），所以面板必须住在两边都能用的模块里。
 *
 * 这里只留一句委托；主面板（index.html）产出的元素 id 与以前**完全一样**
 * （热键 `HOST_PRIMARY['find.notes'] = 'fprefresh'` 照旧能用）。
 * 宿主页要给的只有三样：`postRun`（app.js 有）、`MinimapPanel`（minimap.js）、工具清单
 * （app.js 走 `state.catalog`，独立窗口走 `FindPanel.ready()`）。
 */

/* ---------------- 歌词面板（lyric.pages 专用 UI）
 *
 * **和 SV 脚本 `[SV2]歌词侧栏.js` 同 UI**：行序、元素类型、按钮排布逐条照 `getSidePanelSectionState()`。
 *
 *   原脚本的行                                        → 这里
 *   ① Container[TextArea h60 readOnly]                 → 只读 <textarea>（信息行）
 *   ② Container[CheckBox "Separate into Characters"]   → 同名复选框（**紧跟信息框**，中间不夹任何控件）
 *   ③ Container[TextArea h20] × pageRowCount           → 一行一个 <textarea data-sentence=…>（**行内没有按钮**）
 *   ④ Container[Button 上一页 width 1.0]               → 整行按钮（竖排）
 *   ⑤ Container[Button 下一页 width 1.0]               → 整行按钮（竖排）
 *   ⑥ Container[ComboBox choices=本页各句]             → <select>（本页各句）
 *
 * 三处**有意的偏离**（都在注释里写明，不藏着）：
 *   a) 原脚本把行文本框声明成 `readOnly: true`，却又给它们挂了 `setValueChangeCallback`
 *      —— 自相矛盾（真 readOnly 的话回调永远不会触发）。**以它的行为为准**：文本框可编辑，
 *      **失焦/回车即写**（就是 setValueChangeCallback 的触发时机）。这里照这个来。
 *   b) 复选框标签：原脚本写的是 `SV.T("Separate into Characters")`，而它**没有 getTranslations()**
 *      ⇒ 在 SV 里显示的就是这串英文原文。我们按本仓「只写中文」的口径用「分割成字符」。
 *   c) 原脚本**没有刷新按钮**（它靠 registerSelectionCallback 被动重读；ACE 没有这种推送）。
 *      所以「刷新」放在**面板外面**，不进被复刻的那 6 行。
 */
const lyric = { page: 0, splitChars: false, data: null, busy: false, writing: 0 };

function lyricPanelHTML() {
  return '<div class="lyricpanel" id="lyricpanel"><div class="dim">载入中…</div></div>' +
    // 这几个控件都在面板**外面** —— 被复刻的那 6 行里不该多原脚本没有的东西
    '<div class="lp-outside">' +
    '<button class="btn ghost" id="lprefresh" type="button">刷新</button>' +
    '<label class="check lp-auto"><input type="checkbox" id="lpauto"' + (poll.on ? ' checked' : '') +
    '><span>宿主一改就自动刷新</span></label>' +
    '<span class="dim">间隔 <input type="number" id="lpautoms" value="' + poll.ms + '" min="1000" step="500" autocomplete="off"> ms</span>' +
    '<span class="dim" id="lpautostat">—</span>' +
    '</div>' +
    '<div class="hint" id="lyricflash" hidden></div>' +
    '<div id="lyricresult"></div>';
}

/* 自动刷新（轮询）。
 *
 * ⚠️ ACE **没有推送** —— SV 那脚本靠 `registerSelectionCallback` 被动重读，
 * ACE 没有这种回调。所以要「宿主一改就自动刷新」只能**轮询**一个便宜的变更令牌
 * （`inspect.revision`：editor status + clip lyrics 的 fingerprint + 选中音符，3 次读 ≈ 210 ms）。
 *
 * 四道守卫（缺一个都会出事）：
 *   1. **正在编辑就不动**：任何一个歌词文本框有焦点、或内容跟读到的基线不一致 ⇒ 跳过这一轮。
 *      否则你打字打到一半，刷新会把输入覆盖掉。
 *   2. **正在写就不动**：写请求在飞的时候跳过（写完本来就会自己重读）。
 *   3. **标签页看不见就不动**：`document.hidden` ⇒ 跳过（切到 ACE 去干活时最需要省）。
 *   4. **只在这个页面轮询**：换工具就停（`selectTool` 里调 `pollStop`）。
 *
 * 轮询**必须安静**：失败不清面板、不弹红条，只在状态行里记一笔（ACE 忙的时候读会失败，
 * 那是正常的，不该拿红条糊用户一脸）。
 */
const poll = { on: true, ms: 2500, timer: null, token: '', running: false, ok: 0, err: 0, last: '' };

function pollStop() {
  poll.on = false;
  if (poll.timer) { clearTimeout(poll.timer); poll.timer = null; }
}

function pollStart() {
  if (poll.timer) { clearTimeout(poll.timer); poll.timer = null; }
  if (!poll.on) return;
  poll.timer = setTimeout(pollTick, poll.ms);
}

function pollSchedule() {
  if (!poll.on) return;
  poll.timer = setTimeout(pollTick, poll.ms);
}

/** 现在**不该**打扰用户的情况。 */
function pollBlocked() {
  if (document.hidden) return '标签页不可见';
  if (lyric.busy || lyric.writing > 0) return '正在读写';
  const box = $('lyricpanel');
  if (!box) return null;
  // 有焦点在文本框上，或某个框被改过还没提交 ⇒ 别动
  const active = document.activeElement;
  if (active && active.classList && active.classList.contains('lp-text')) return '正在编辑';
  const dirty = [...box.querySelectorAll('.lp-text')].some((el) => el.value !== el.dataset.orig);
  if (dirty) return '有未提交的编辑';
  return null;
}

async function pollTick() {
  poll.timer = null;
  if (!poll.on) return;
  const blocked = pollBlocked();
  if (blocked) {
    pollStat('暂停（' + blocked + '）');
    pollSchedule();
    return;
  }
  poll.running = true;
  try {
    const data = await postRun('inspect.revision', {});
    // ⚠️ 等回来时可能已经换工具了（pollStop 把 on 置 false）——
    //    这时**不能**再去 lyricLoad()，那块面板已经不在 DOM 里了。
    if (!poll.on) return;
    if (!data.ok) {
      poll.err++;
      pollStat('读不到（' + (data.hint || '').slice(0, 40) + '）');
    } else {
      const rev = data.calls[0].json;
      const tok = rev.token || '';
      if (poll.token && tok !== poll.token) {
        poll.ok++;
        poll.last = nowClock();
        pollStat('宿主变了 → 已重读');
        poll.token = tok;
        await lyricLoad();
      } else {
        if (!poll.token) poll.token = tok;
        pollStat('已跟上宿主的改动');
      }
    }
  } catch (e) {
    if (!poll.on) return;
    poll.err++;
    pollStat('读不到（' + ((e && e.message) || e).toString().slice(0, 40) + '）');
  } finally {
    poll.running = false;
    pollSchedule();
  }
}

function pollStat(msg) {
  const el = $('lpautostat');
  if (!el) return;
  const bits = [msg];
  if (poll.ok) bits.push('已刷新 ' + poll.ok + ' 次');
  if (poll.err) bits.push('读失败 ' + poll.err + ' 次');
  if (poll.last) bits.push('上次 ' + poll.last);
  el.textContent = bits.join(' · ');
}

function nowClock() {
  const d = new Date();
  const p = (n) => String(n).padStart(2, '0');
  return p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds());
}

function lyricRender() {
  const box = $('lyricpanel');
  if (!box) return;
  const d = lyric.data;
  if (!d) { box.innerHTML = '<div class="dim">还没读到句子。</div>'; return; }

  const rows = d.rows || [];
  // ① 信息行（只读 TextArea）
  let html = '<textarea class="lp-info" id="lpinfo" readonly ' + NOFILL + '>' + esc(d.info || '') + '</textarea>';
  // ② 按字符隔开（紧跟信息框）—— 用户 2026-10-06 定名，原来叫「分割成字符」
  html += '<label class="check lp-split"><input type="checkbox" id="lpsplit"' +
    (lyric.splitChars ? ' checked' : '') + '><span>按字符隔开</span></label>';
  // ③ 一行一句的文本框（行内没有按钮、没有元信息行 —— 与原脚本一致）
  if (!rows.length) {
    html += '<div class="hint">' + esc(d.note || '这一页没有句子。') + '</div>';
  } else {
    rows.forEach((r) => {
      html += '<textarea class="lp-text" id="lprow' + r.index + '" data-sentence="' + r.index +
        '" data-orig="' + esc(r.text) + '" rows="1" ' + NOFILL +
        ' title="第 ' + (r.index + 1) + ' 句 · ' + r.noteCount + ' 个音符' +
        (r.hasSelectedNote ? ' · 含选中的音符' : '') + '">' + esc(r.text) + '</textarea>';
    });
  }
  // ④⑤ 上一页 / 下一页 —— 各自整行（原脚本是两个独立的 Container）
  html += '<button class="btn lp-wide" id="lpprev" type="button"' + (d.canPrev ? '' : ' disabled') + '>上一页</button>';
  html += '<button class="btn lp-wide" id="lpnext" type="button"' + (d.canNext ? '' : ' disabled') + '>下一页</button>';
  // ⑥ 本页各句的下拉
  html += '<select id="lpgoto" class="lp-goto">' +
    (rows.length ? '<option value="">跳到某一句…</option>' : '<option value="">-</option>') +
    rows.map((r) => '<option value="' + r.index + '">第 ' + (r.index + 1) + ' 句：' +
      esc(String(r.text).slice(0, 20)) + '</option>').join('') +
    '</select>';
  box.innerHTML = html;
  bindLyric();
}

function bindLyric() {
  const split = $('lpsplit');
  if (split) split.addEventListener('change', () => { lyric.splitChars = split.checked; lyricLoad(); });
  const prev = $('lpprev');
  if (prev) prev.addEventListener('click', () => { lyric.page--; lyricLoad(); });
  const next = $('lpnext');
  if (next) next.addEventListener('click', () => { lyric.page++; lyricLoad(); });
  const ref = $('lprefresh');
  if (ref) ref.addEventListener('click', () => lyricLoad());
  const auto = $('lpauto');
  if (auto) {
    auto.addEventListener('change', () => {
      poll.on = auto.checked;
      if (poll.on) { pollStat('已开启'); pollStart(); } else { pollStop(); pollStat('已暂停（手动）'); }
    });
  }
  const ms = $('lpautoms');
  if (ms) {
    ms.addEventListener('change', () => {
      const v = parseInt(ms.value, 10);
      poll.ms = (isNaN(v) || v < 1000) ? 1000 : v;   // 下限 1 s：一次轮询本身要约 210 ms
      ms.value = String(poll.ms);
      if (poll.on) pollStart();
    });
  }
  const go = $('lpgoto');
  if (go) {
    go.addEventListener('change', () => {
      if (go.value === '') return;
      lyricGoto(parseInt(go.value, 10));
      go.value = '';
    });
  }
  // ③ 文本框：**失焦即写**（原脚本 setValueChangeCallback 的时机）。
  //    ⚠️ 加一道「真的改了才写」的闸 —— 否则只是用 Tab 过一遍焦点就会把每句重写一遍，
  //    歌词没变却给 ACE 的撤销栈刷一堆记录。
  $('lyricpanel').querySelectorAll('.lp-text').forEach((el) => {
    el.addEventListener('change', () => {
      if (el.value === el.dataset.orig) return;
      lyricWrite(parseInt(el.dataset.sentence, 10), el.value, el);
    });
    // 单行文本框里回车应当是"提交"，不是换行
    el.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); el.blur(); }
    });
  });
}

async function lyricLoad() {
  const box = $('lyricpanel');
  // ⚠️ 面板可能已经不在了（换了工具、或轮询回来时页面已经换了）——
  //    不挡这一下，下面那条 `.innerHTML` 会打在 null 上直接抛错。
  if (!box) return;
  if (lyric.busy) return;
  lyric.busy = true;
  try {
    const data = await postRun('lyric.pages', {
      page: String(Math.max(0, lyric.page)),
      splitChars: lyric.splitChars ? '1' : '0',
      scope: 'auto',   // 原脚本的语义：有选中就只算选中的 —— 所以面板上没有范围控件
    });
    (data.calls || []).forEach(pushLog);
    if (!$('lyricpanel')) return;   // 等回来时页面已经切走了
    if (!data.ok) {
      lyric.data = null;
      $('lyricpanel').innerHTML = '<div class="hint bad">' + esc(data.hint || '读不到歌词') + '</div>';
      return;
    }
    lyric.data = data.calls[0].json;
    lyric.page = lyric.data.page;
    lyricRender();
  } catch (e) {
    const b = $('lyricpanel');
    if (b) b.innerHTML = '<div class="hint bad">' + esc((e && e.message) || e) + '</div>';
  } finally {
    lyric.busy = false;
  }
}

/** 写某一句（照原脚本：改了就走，默认「按原脚本规则」逐音符、循环铺满不空音符）。 */
async function lyricWrite(sentence, text, el) {
  lyric.writing++;   // 轮询看到这个就跳过（写的时候别来插队）
  try {
    const data = await postRun('lyric.setSentence', {
      sentence: String(sentence),
      text: text,
      mode: 'perNote',
      splitChars: lyric.splitChars ? '1' : '0',
      dryRun: '0',
    });
    (data.calls || []).forEach(pushLog);
    const box = $('lyricresult');
    if (box) box.innerHTML = renderRunHTML(data, { compact: true });
    if (!data.ok) {
      lyricFlash('第 ' + (sentence + 1) + ' 句写失败：' + (data.hint || ''), false);
      if (el) el.value = el.dataset.orig;   // 写失败就把框里的内容退回读到的值
      return;
    }
    lyricFlash('第 ' + (sentence + 1) + ' 句已写入（' + text + '）', true);
    if (el) el.dataset.orig = el.value;     // 更新基线，免得再触发一次
    await lyricLoad();                      // 重读，让显示与工程一致
    poll.token = '';                        // 我们自己改了 ⇒ 令牌作废，下一轮重新取基准
  } finally {
    lyric.writing--;
  }
}

async function lyricGoto(sentence) {
  const data = await postRun('lyric.goto', { sentence: String(sentence), dryRun: '0' });
  (data.calls || []).forEach(pushLog);
  if (!data.ok) { lyricFlash('跳句失败：' + (data.hint || ''), false); return; }
  lyricFlash('已跳到第 ' + (sentence + 1) + ' 句（标记线 + 播放头）', true);
}

function lyricFlash(msg, ok) {
  const el = $('lyricflash');
  if (!el) return;
  el.className = 'hint ' + (ok ? 'ok' : 'bad');
  el.textContent = msg;
  el.hidden = false;
}

/* ---------------- 工具面板（note.join / select.playhead / lyric.* 专用 UI）
 *
 * 移植 SV 脚本 `AKD工具侧栏.js` 里**用户点名的那几项**，版式照它的
 * `getSidePanelSectionState()`：**两列一行的按钮网格** + 末尾的
 * 「选择下方工具查看说明：」Label + ComboBox + TextArea（说明）。
 *
 * ⚠️ 两处与用户无关的差别（都写在这里，不藏着）：
 *   1. 原脚本的说明 ComboBox 是 `readOnly` 的（只能选、不能输），这里也是。
 *   2. 原脚本**点一下就直接写**。这里照搬（按钮即执行），但多给一个
 *      **「干跑」复选框**（默认不勾）—— 勾上就只出计划不写。批量操作（自动拼接可能改几百个音符）
 *      想先看一眼时很有用。
 *
 * 这一组工具有 6 个页面，面板在**每个页面上都显示**（像原脚本那个常驻侧栏一样），
 * 当前页面对应的按钮高亮。下面再跟该工具自己的通用表单。
 */
const NOTEOPS = ['note.join', 'select.playhead', 'lyric.copy', 'lyric.paste', 'lyric.shift', 'lyric.remove', 'track.split'];

// 说明文本：前 8 条里带原脚本 HelpText 原文的口径；「选中之前」与「分割轨道」按本仓的落地写。
const noteopsHelp = [
  ['自动拼接音符', '选中2个以上音符 → 消除所有间隙，按起始时间排序后首尾相连。无选中时默认处理当前组全部音符。'],
  ['选中之后的所有音符', '选中播放头时间位置之后的所有音符（结束 > 播放头，含正在唱的那个）。'],
  ['选中之前的所有音符', '选中播放头之前的所有音符（起点 < 播放头）。⚠️ 原脚本没有这一项，是它的对称补全。'],
  ['复制歌词', '拼接选中音符歌词（空格分隔），复制到系统剪贴板。无选中默认全部。'],
  ['粘贴所选音符歌词', '剪贴板/文本框 → 自动判断字/词分割 → 顺序填入选中音符。⚠️ 词用完就停，不循环复用。'],
  ['添加 + 并后移', '播放头所在音符 → 歌词改为 +（连音标记）→ 后续歌词右移一位。'],
  ['添加 - 并后移', '播放头所在音符 → 歌词改为 -（延音标记）→ 后续歌词右移一位。'],
  ['删除歌词并前移', '播放头所在音符 → 删除歌词 → 后续歌词左移一位（最后一个音符的歌词留在原地）。'],
  ['合并歌词并前移', '播放头所在音符 → 拼接下一音符歌词 → 后续歌词左移一位。'],
  ['按播放头分割轨道', '当前轨道在播放头处切成两条：先复制一条轨，再让原轨只留播放头之前、复制轨只留之后；' +
    '跨在播放头上的片段用 clip split 原生切开。⚠️ 中途失败会回滚（删掉复制出来的那条轨）。'],
];

function noteopsPanelHTML() {
  const cur = state.current ? state.current.id : '';
  const btn = (id, label, cls) =>
    '<button class="btn ' + (cls || 'ghost') + ' np-btn" type="button" data-tool="' + id + '"' +
    (id === cur ? ' data-cur="1"' : '') + '>' + esc(label) + '</button>';
  const two = (a, b) => '<div class="np-two">' + a + b + '</div>';
  return '<div class="nopanel" id="nopanel">' +
    '<button class="btn np-wide np-btn" type="button" data-tool="note.join">自动拼接音符</button>' +
    two(btn('select.playhead', '选中之后的所有音符'), btn('select.playhead', '选中之前的所有音符')) +
    two(btn('lyric.copy', '复制歌词'), btn('lyric.paste', '粘贴所选音符歌词')) +
    two(btn('lyric.shift', '添加 + 并后移'), btn('lyric.shift', '添加 - 并后移')) +
    two(btn('lyric.remove', '合并歌词并前移'), btn('lyric.remove', '删除歌词并前移')) +
    '<button class="btn ghost np-wide np-btn" type="button" data-tool="track.split">按播放头分割轨道</button>' +
    '<div class="np-paste">' +
    '<span class="dim">粘贴用文本</span>' +
    '<input type="text" id="nptext" ' + NOFILL + ' placeholder="留空则从剪贴板读">' +
    '<button class="btn ghost" id="npfromclip" type="button">从剪贴板读</button>' +
    '</div>' +
    '<label class="check np-dry"><input type="checkbox" id="npdry"><span>干跑（只出计划不写）</span></label>' +
    '<div class="np-label">选择下方工具查看说明：</div>' +
    '<select id="nphelp" class="np-goto">' +
    noteopsHelp.map((e, i) => '<option value="' + i + '">' + esc(e[0]) + '</option>').join('') +
    '</select>' +
    '<textarea class="np-helpbox" id="nphelpbox" readonly ' + NOFILL + '>' + esc(noteopsHelp[0][1]) + '</textarea>' +
    '</div>' +
    '<div class="hint" id="npflash" hidden></div>' +
    '<div id="npresult"></div>';
}

function bindNoteops() {
  const box = $('nopanel');
  if (!box) return;
  // 两个同名按钮要靠**文字**区分动作（选中之后/之前、+/-、合并/删除）
  box.querySelectorAll('.np-btn').forEach((b) => {
    b.addEventListener('click', () => noteopsRun(b));
  });
  const help = $('nphelp');
  if (help) {
    help.addEventListener('change', () => {
      const e = noteopsHelp[parseInt(help.value, 10) || 0];
      $('nphelpbox').value = e ? e[1] : '';
    });
  }
  const fromClip = $('npfromclip');
  if (fromClip) fromClip.addEventListener('click', () => noteopsReadClipboard());
  // 进页面时顺手把说明框填上（照原脚本：ComboBox 一动就填）
  $('nphelpbox').value = noteopsHelp[0][1];
}

/** 把按钮的文字翻成一个动作参数（原脚本是一个按钮一件事，这里靠文字分辨）。 */
function noteopsArgs(label, dry) {
  const t = String(label || '');
  if (t.indexOf('自动拼接') >= 0) return ['note.join', {}];
  if (t.indexOf('选中之后') >= 0) return ['select.playhead', { side: 'after' }];
  if (t.indexOf('选中之前') >= 0) return ['select.playhead', { side: 'before' }];
  if (t.indexOf('复制歌词') >= 0) return ['lyric.copy', {}];
  if (t.indexOf('粘贴') >= 0) return ['lyric.paste', { text: ($('nptext') || {}).value || '' }];
  if (t.indexOf('添加 +') >= 0) return ['lyric.shift', { mark: '+' }];
  if (t.indexOf('添加 -') >= 0) return ['lyric.shift', { mark: '-' }];
  if (t.indexOf('合并歌词') >= 0) return ['lyric.remove', { mode: 'merge' }];
  if (t.indexOf('删除歌词') >= 0) return ['lyric.remove', { mode: 'delete' }];
  if (t.indexOf('分割轨道') >= 0) return ['track.split', {}];
  return [null, null];
}

async function noteopsRun(btnEl) {
  const [tool, extra] = noteopsArgs(btnEl.textContent, null);
  if (!tool) { noteopsFlash('这个按钮我认不出对应的工具', false); return; }
  if (tool === 'lyric.paste' && !(($('nptext') || {}).value || '').trim()) {
    // 文本空 ⇒ 先试剪贴板（原脚本也是从剪贴板读）
    noteopsFlash('文本框是空的，正在试读剪贴板…', true);
    await noteopsReadClipboard();
    if (!(($('nptext') || {}).value || '').trim()) {
      noteopsFlash('剪贴板也读不到内容 —— 手动粘进上面那个框再点「粘贴所选音符歌词」', false);
      return;
    }
    extra.text = $('nptext').value;
  }
  btnEl.disabled = true;
  try {
    const dry = $('npdry') && $('npdry').checked;
    const data = await postRun(tool, Object.assign({ dryRun: dry ? '1' : '0' }, extra || {}));
    (data.calls || []).forEach(pushLog);
    const box = $('npresult');
    if (box) box.innerHTML = renderRunHTML(data, { compact: true });
    if (!data.ok) {
      noteopsFlash((dry ? '干跑' : '执行') + '失败：' + (data.hint || ''), false);
      return;
    }
    if (tool === 'lyric.copy') {
      const d = data.calls[0].json;
      const ta = $('nptext');
      if (ta) ta.value = d.text || '';
      const ok = await noteopsWriteClipboard(d.text || '');
      noteopsFlash(ok
        ? '已复制 ' + d.count + ' 个歌词到剪贴板（也填进了下面的文本框）'
        : '浏览器不让写剪贴板 —— 文本已填进下面的文本框，手动复制即可', ok);
      return;
    }
    noteopsFlash((dry ? '干跑完成（一条都没写）' : '已执行') + '：' + btnEl.textContent.trim(), true);
  } catch (e) {
    noteopsFlash('请求失败：' + ((e && e.message) || e), false);
  } finally {
    btnEl.disabled = false;
  }
}

/** 读系统剪贴板（浏览器 API；可能被拒，拒了就让用户手动粘）。 */
async function noteopsReadClipboard() {
  try {
    if (!navigator.clipboard || !navigator.clipboard.readText) throw new Error('这个浏览器没有剪贴板读接口');
    const t = await navigator.clipboard.readText();
    const el = $('nptext');
    if (el) el.value = t || '';
    noteopsFlash(t ? '已从剪贴板读到 ' + t.length + ' 个字符' : '剪贴板是空的', !!t);
    return t || '';
  } catch (e) {
    noteopsFlash('读剪贴板被拒（' + ((e && e.message) || e) + '）—— 手动粘进文本框即可', false);
    return '';
  }
}

async function noteopsWriteClipboard(text) {
  try {
    if (!navigator.clipboard || !navigator.clipboard.writeText) return false;
    await navigator.clipboard.writeText(text);
    return true;
  } catch (e) {
    return false;
  }
}

function noteopsFlash(msg, ok) {
  const el = $('npflash');
  if (!el) return;
  el.className = 'hint ' + (ok ? 'ok' : 'bad');
  el.textContent = msg;
  el.hidden = false;
}

/* ---------------- 启动 ---------------- */

/**
 * `window.ShortcutHost` —— 快捷键那一层（`shortcuts.js`）操作**页面**的入口。
 *
 * 为什么要有这条缝：快捷键的动作分两种，一种是 exe 自己能干的（跑工具/置顶/唤起窗口，
 * 见 `panel_api.go` 的 `fire`），另一种**必须动页面**（换工具、跳到某栏、点某页的按钮）。
 * 后者只有 app.js 知道 DOM 与状态，所以由它实现、`shortcuts.js` 调。
 *
 * ⚠️ 纪律：**拿不准就说拿不准**。找不到"这一页的主动作"时如实提示，绝不猜一个按钮去点
 * —— 猜错就是"按了快捷键却动了别的东西"。
 */
const HOST_PRIMARY = {
  // 谱面编辑器：面板上只剩**真写**那一颗了（用户 2026-10-08 把两颗干跑按钮都删了），
  // 而快捷键**不该默默改工程** ⇒ 主操作退到只读的「导出预览」（与其余几项"刷新/重读"同一性质）。
  'jianpu.editor': 'nt-print',
  'find.notes': 'fprefresh',   // 查找页：刷新
  'lyric.pages': 'lprefresh',  // 歌词页：刷新
  'color.cycle': 'cycletoggle',
  'view.minimap': 'mmrefresh', // 工程缩略图：重新读取
};

/** NOTEOPS 那一批页面没有"唯一主动作"（每页是一排并列操作）⇒ 不猜，如实说。 */
function hostPrimaryId(t) {
  if (!t) return null;
  if (HOST_PRIMARY[t.id]) return HOST_PRIMARY[t.id];
  if (NOTEOPS.indexOf(t.id) >= 0) return null;
  return 'run';
}

window.ShortcutHost = {
  current: () => state.current,
  runPrimary: () => {
    const t = state.current;
    const id = hostPrimaryId(t);
    if (!id) {
      if (t && NOTEOPS.indexOf(t.id) >= 0) {
        ShortcutPanel.toast('info', '「' + t.title + '」是一排并列操作，没有唯一主动作 ⇒ 请点按钮（这一页不猜）');
      } else {
        ShortcutPanel.toast('bad', '这一页没有可执行的主动作');
      }
      return;
    }
    const btn = $(id);
    if (!btn) { ShortcutPanel.toast('bad', '页面上找不到 `' + id + '`（页面结构变了？）'); return; }
    if (btn.disabled) { ShortcutPanel.toast('info', '这个按钮现在是禁用的（等它可点）'); return; }
    btn.click();
  },
  toolPrev: () => hostStepTool(-1),
  toolNext: () => hostStepTool(1),
  itemPrev: () => hostItem(-1),
  itemNext: () => hostItem(1),
  toggleDry: () => {
    const el = document.querySelector('[data-name="dryRun"]');
    if (!el) {
      ShortcutPanel.toast('info', '这一页没有「干跑」勾选（谱面编辑器只有一颗「生成并写进 ACE」，不再分干跑/真写）');
      return;
    }
    el.checked = !el.checked;
    el.dispatchEvent(new Event('change', { bubbles: true }));
    ShortcutPanel.toast('ok', '干跑 = ' + (el.checked ? '开（只出计划）' : '**关**（会真写）'));
  },
  clearLog: () => { state.log = []; renderLog(); ShortcutPanel.toast('ok', '命令日志已清空'); },
  gotoGroup: (n) => {
    const cat = (state.catalog && state.catalog.categories || [])[n - 1];
    if (!cat) { ShortcutPanel.toast('bad', '没有第 ' + n + ' 栏'); return; }
    const first = (state.catalog.tools || []).filter((t) => t.category === cat)[0];
    if (!first) { ShortcutPanel.toast('bad', '「' + cat + '」栏是空的'); return; }
    selectTool(first.id);
  },
  /** 系统级热键跑完的结果，落进命令日志（页面被切走时看不到 toast，回来还能翻）。 */
  pushLog: (ev) => {
    pushLog({
      label: '系统级热键 · ' + (ev.action || ''), args: [], exitCode: ev.kind === 'bad' ? 1 : 0,
      stdout: ev.text,
    });
  },
};

function hostStepTool(d) {
  const list = (state.catalog && state.catalog.tools) || [];
  if (!list.length) return;
  let i = list.findIndex((t) => state.current && t.id === state.current.id);
  if (i < 0) i = 0;
  const j = (i + d + list.length) % list.length;
  selectTool(list[j].id);
}

function hostItem(d) {
  const t = state.current;
  if (!t) return;
  if (t.id === 'jianpu.editor' && window.NotationPanel && NotationPanel.moveSel) {
    NotationPanel.moveSel(d);
    return;
  }
  const pair = { 'find.notes': ['fpprev', 'fpnext'], 'lyric.pages': ['lpprev', 'lpnext'] }[t.id];
  if (!pair) { ShortcutPanel.toast('info', '这一页没有「上一项 / 下一项」'); return; }
  const btn = $(d < 0 ? pair[0] : pair[1]);
  if (!btn || btn.disabled) { ShortcutPanel.toast('info', '到头了（或者这一页还没载入）'); return; }
  btn.click();
}

/**
 * 深链：`#工具id`（如 `#jianpu.editor`）直接把那个工具选上。
 *
 * 两个用处：① 给用户一条直达链接（"打开谱面编辑器"）；② 无头浏览器截图/自检能直接落到某一页
 * —— 没有它，headless 截出来的永远是"← 左边选一个工具"，等于没法自动看页面。
 * 工具清单是异步来的，所以 `loadTools()` 之后才能用得上，见下面 selectByHash()。
 */
function selectByHash() {
  const id = (location.hash || '').replace(/^#/, '');
  if (!id || !state.catalog) return false;
  const t = (state.catalog.tools || []).filter((x) => x.id === id)[0];
  if (!t) return false;
  selectTool(t.id);
  return true;
}

document.addEventListener('DOMContentLoaded', () => {
  $('recheck').addEventListener('click', selfcheck);
  $('logtoggle').addEventListener('click', () => {
    const b = $('logbody');
    b.hidden = !b.hidden;
  });
  $('logclear').addEventListener('click', () => { state.log = []; renderLog(); });
  window.addEventListener('hashchange', () => selectByHash());
  loadTools();
  selfcheck();
});
