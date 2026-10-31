/* 端到端自检：打**真实 HTTP** 走一遍面板的接口。
 *
 * 起点是一次真实的教训：ACETool 早期所有"看起来能跑"的结论都是靠 `Invoke-RestMethod` 得出的，
 * 而它会把请求体里的中文变成 `???????`（tool UTF-8 本身是干净的）——**验证工具本身会撒谎**。
 * 所以这里用 Node 的原生 fetch，并且把「回包里到底有什么」原样打出来，不做美化。
 *
 * 用法：node e2e.js http://127.0.0.1:51646/
 * 覆盖：① 面板页面与前端文件在不在 ② 元素文档 → MusicXML（**不读 ACE**，纯进程内）
 *       ③ 元素文档 → ACE 干跑/真写（需要 ACE 在线） ④ jianpu.editor 读 ACE
 */
const base = process.argv[2] || 'http://127.0.0.1:51646/';
const H = { 'Content-Type': 'application/json', 'X-ACETool': '1' };

async function get(p) {
  const r = await fetch(new URL(p, base));
  const t = await r.text();
  return { status: r.status, len: t.length, text: t };
}
async function run(toolId, values) {
  const r = await fetch(new URL('/api/run', base), { method: 'POST', headers: H, body: JSON.stringify({ toolId, values }) });
  const j = await r.json().catch(() => null);
  return { status: r.status, body: j };
}
function head(s, n) { s = String(s == null ? '' : s); return s.length > (n || 400) ? s.slice(0, n || 400) + '…' : s; }

let fails = 0;
function check(name, cond, extra) {
  console.log((cond ? '  ok   ' : '  FAIL ') + name + (extra ? '  — ' + extra : ''));
  if (!cond) fails++;
}

const ELEMS = [
  { kind: 'note', pitch: 1, duration: 4, lyric: '月' },
  { kind: 'note', pitch: 2, duration: 4, lyric: '落' },
  { kind: 'note', pitch: 3, duration: 8, dots: 1, lyric: '乌' },
  { kind: 'sustain', duration: 4 },
  { kind: 'note', pitch: 5, duration: 2, lyric: '霜' },
  { kind: 'sustain', duration: 4 },
  { kind: 'rest', duration: 4 },
  { kind: 'note', pitch: 4, duration: 4, accidental: 'sharp', octave: 1, lyric: '天' },
];

(async () => {
  console.log('== 面板 ' + base);
  const idx = await get('/');
  check('GET / 是 200', idx.status === 200);
  check('页面引了 notation.js', idx.text.indexOf('notation.js') >= 0);
  check('页面引了 notation-glyphs.js', idx.text.indexOf('notation-glyphs.js') >= 0);
  check('页面引了 app.js', idx.text.indexOf('app.js') >= 0);
  const nj = await get('/notation.js');
  check('GET /notation.js 是 200 且非空', nj.status === 200 && nj.len > 20000, nj.len + ' 字节');
  const ng = await get('/notation-glyphs.js');
  check('GET /notation-glyphs.js 是 200 且含 Bravura 字形', ng.status === 200 && ng.text.indexOf('GLYPHS') >= 0, ng.len + ' 字节');
  check('谱号字形在（gClef）', ng.text.indexOf('gClef') >= 0);

  const tools = await get('/api/tools');
  check('工具清单里有 jianpu.editor', tools.text.indexOf('jianpu.editor') >= 0);

  console.log('\n== ① 元素文档 → MusicXML（进程内，不读 ACE）');
  let r = await run('jianpu.fromClip', {
    elements: JSON.stringify(ELEMS), format: 'musicxml', key: '0', meter: '4/4', title: '元素往返自检',
  });
  check('HTTP 200', r.status === 200, r.status);
  let res = r.body && r.body.calls && r.body.calls[0] && r.body.calls[0].json;
  check('回包有结果', !!res, head(r.body && r.body.hint, 200));
  if (res) {
    const x = res.output || '';
    check('产出了 MusicXML', x.indexOf('<score-partwise') >= 0);
    check('divisions = 480（与 NoteBlock.Ticks 同口径）', /<divisions>480<\/divisions>/.test(x));
    check('歌词进了 <lyric>', x.indexOf('<lyric') >= 0);
    check('有 <text>月</text>', x.indexOf('月') >= 0);
    check('休止写成了 <rest', x.indexOf('<rest') >= 0 || x.indexOf('<rest/>') >= 0);
    check('升号音写了 <alter>1</alter>', x.indexOf('<alter>1</alter>') >= 0);
    check('连音折进了前一个音（不该有 `<forward>` 或空拍）', x.indexOf('<forward') < 0);
    console.log('  音符数 = ' + (res.notes || '?') + '，产物 ' + x.length + ' 字节');
    console.log('  --- MusicXML 前 700 字节 ---\n' + head(x, 700));
  }

  console.log('\n== ② 元素文档 → 简洁记法（进程内）');
  r = await run('jianpu.fromClip', { elements: JSON.stringify(ELEMS), format: 'terse', key: '0', meter: '4/4' });
  res = r.body && r.body.calls && r.body.calls[0] && r.body.calls[0].json;
  check('HTTP 200 且有产物', r.status === 200 && !!res && !!res.output, head(r.body && r.body.hint, 200));
  if (res) console.log('  简洁记法：' + JSON.stringify(res.output));
  if (res && res.diagnostics) console.log('  诊断：' + JSON.stringify(res.diagnostics));

  console.log('\n== ③ jianpu.editor 读 ACE（需要 ACE 在线）');
  r = await run('jianpu.editor', { scope: 'clip', key: 'auto', meter: '4/4' });
  res = r.body && r.body.calls && r.body.calls[0] && r.body.calls[0].json;
  if (!r.body || !r.body.ok) {
    console.log('  （ACE 侧没成功，如实报出来）' + head(r.body && r.body.hint, 300));
  } else {
    check('读到了 elements 数组', Array.isArray(res.elements), '元素 ' + (res.elements || []).length + ' 个');
    check('带了调式信息', res.keyName != null, 'key=' + res.key + ' ' + res.keyName);
    check('段落信息在', !!res.scope, res.scope);
    const sus = (res.elements || []).filter((e) => e.kind === 'sustain').length;
    const lyr = (res.elements || []).filter((e) => e.lyric).length;
    console.log('  元素 ' + (res.elements || []).length + ' 个，其中连音格 ' + sus + ' 个、带歌词 ' + lyr + ' 个');
    console.log('  前 3 个：' + JSON.stringify((res.elements || []).slice(0, 3)));
  }

  console.log('\n== ④ 元素文档 → ACE 干跑（需要 ACE 在线）');
  r = await run('jianpu.toNotes', {
    elements: JSON.stringify(ELEMS), key: '0', defaultLyric: 'la', target: 'current', startAt: 'clipStart', dryRun: '1',
  });
  res = r.body && r.body.calls && r.body.calls[0] && r.body.calls[0].json;
  if (!r.body || !r.body.ok) {
    console.log('  （ACE 侧没成功，如实报出来）' + head(r.body && r.body.hint, 300));
  } else {
    check('干跑出了音符计划', (res.notesToWrite || 0) > 0, '要写 ' + res.notesToWrite + ' 个');
    check('连音按 tenuto 展开并计数', (res.sustainNotes || 0) === 2, 'tenuto ' + res.sustainNotes + ' 个');
    check('休止被跳过并计数', (res.skipped || 0) >= 1, '跳过 ' + res.skipped);
    check('歌词带上了编辑器里的字', (res.preview || []).join(' ').indexOf('月') >= 0);
    console.log('  预览：' + JSON.stringify(res.preview));
    console.log('  计划：' + JSON.stringify(res.plannedCalls));
  }

  console.log('\n== ⑤ 面板自己生成的元素 JSON → Go（补最后一段没人验证过的路）');
  let panelJSON = null;
  try { panelJSON = require('fs').readFileSync(require('path').join(__dirname, 'panel-elements.json'), 'utf8'); }
  catch (e) { console.log('  （没有 panel-elements.json，先跑 render-check.js）'); }
  if (panelJSON) {
    console.log('  面板方言：' + panelJSON);
    const els = JSON.parse(panelJSON);
    check('面板 JSON 里有 kind:"note" + rest:true（上游语义）',
      els.some((e) => e.kind === 'note' && e.rest === true));
    check('面板 JSON 里没有编辑器内部的 kind:"rest" 漏出来',
      !els.some((e) => e.kind === 'rest'));
    r = await run('jianpu.toNotes', {
      elements: panelJSON, key: '0', defaultLyric: 'la', target: 'current', startAt: 'clipStart', dryRun: '1',
    });
    res = r.body && r.body.calls && r.body.calls[0] && r.body.calls[0].json;
    if (!r.body || !r.body.ok) {
      check('Go 收下了面板的元素 JSON', false, head(r.body && r.body.hint, 300));
    } else {
      check('Go 收下了面板的元素 JSON', true, res.parsedElements + ' 个元素');
      check('休止被识别并占位（跳过 1 个）', res.skipped === 1, '跳过 ' + res.skipped);
      check('连音格展开成 tenuto', res.sustainNotes === 1, 'tenuto ' + res.sustainNotes);
      const p = res.preview || [];
      console.log('  预览：' + JSON.stringify(p));
      // 位置核算：480 + 240 + 720 + 480（连音）+ 480（休止） = 2400 ⇒ 霜 必须落在 2400
      const last = p[p.length - 1] || '';
      check('休止**占了位置**（霜 在 pos 2400，不是 1920）', last.indexOf('pos 2400') === 0, last);
      check('低八度算对了（霜 = D♭3 = 49）', last.indexOf('pitch 49') > 0, last);
    }
    r = await run('jianpu.fromClip', { elements: panelJSON, format: 'musicxml', key: '0', meter: '4/4' });
    res = r.body && r.body.calls && r.body.calls[0] && r.body.calls[0].json;
    check('同一份 JSON 能出 MusicXML', !!(res && res.output && res.output.indexOf('<score-partwise') >= 0));
    if (res && res.output) {
      check('拼法听元素的（C 大调 2b ⇒ D♭3 的 <alter>-1</alter>）', res.output.indexOf('<alter>-1</alter>') >= 0);
      check('2.5 拍（附点四分 + 连音）拆成带 tie 的两段', res.output.indexOf('<tie type="start"/>') >= 0);
      console.log('  --- 音高/时值那几行 ---');
      res.output.split('\n').filter((l) => /<pitch>|<step>|<alter>|<type>|<dot|<tie/.test(l))
        .forEach((l) => console.log('  ' + l.trim()));
    }

    // 单独的附点：一个附点四分（不折连音）应当写 `<type>quarter</type><dot/>`
    r = await run('jianpu.fromClip', {
      elements: '[{"kind":"note","pitch":1,"duration":4,"dots":1,"lyric":"附"}]',
      format: 'musicxml', key: '0', meter: '4/4',
    });
    res = r.body && r.body.calls && r.body.calls[0] && r.body.calls[0].json;
    check('附点写成 <dot/>', !!(res && res.output && res.output.indexOf('<dot/>') >= 0));

    // 降号调：F 大调的 4（B♭）要拼成 B♭，且调号是 -1 —— 两者不能自相矛盾
    r = await run('jianpu.fromClip', {
      elements: '[{"kind":"note","pitch":4,"duration":4,"lyric":"降"}]',
      format: 'musicxml', key: '5', meter: '4/4',
    });
    res = r.body && r.body.calls && r.body.calls[0] && r.body.calls[0].json;
    if (res && res.output) {
      check('F 大调调号是 <fifths>-1</fifths>（不是 0）', res.output.indexOf('<fifths>-1</fifths>') >= 0);
      check('F 大调的 4 拼成 B♭（step B + alter -1，不是 A#）',
        /<step>B<\/step><alter>-1<\/alter>/.test(res.output));
    }
  }

  console.log('\n' + (fails ? fails + ' 项失败' : '全部通过'));
  // ⚠️ 不强杀进程（见 e2e-shortcuts.js 里那段注释：`process.exit()` 会让退出码变成 1）
  process.exitCode = fails ? 1 : 0;
})().catch((e) => { console.error('自检脚本自己炸了：', e); process.exitCode = 2; });
