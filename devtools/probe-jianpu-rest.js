/* 真机探针：**空拍 = 休止**（用户 2026-10-07 的质问：「空拍不是休止吗」）
 *
 * 全是只读 + 干跑：
 *   ① `jianpu.editor`（读当前片段成元素文档）⇒ 看空拍有没有被补成休止（`kind:"rest"`）、
 *      诊断里有没有那句「空拍已按间隙还原」；
 *   ② 把那份元素**原样喂回** `jianpu.toNotes`（干跑）⇒ 看休止有没有被跳过、**音符位置有没有变**
 *      （这是关键：休止占位置 ⇒ 位置必须与 ACE 里一致）；
 *   ③ `0 -`（休止 + 延音线）⇒ 只占位置、**不该**把上一个音续回来。
 *
 * 用法：node devtools/probe-jianpu-rest.js [url]
 */
const base = process.argv[2] || require('./panel-url.js');
const H = { 'Content-Type': 'application/json', 'X-ACETool': '1' };

async function run(toolId, values) {
  const r = await fetch(new URL('/api/run', base), {
    method: 'POST', headers: H, body: JSON.stringify({ toolId, values: values || {} }),
  });
  const j = await r.json();
  return { ok: j.ok, hint: j.hint, json: (j.calls && j.calls[0] && j.calls[0].json) || null };
}

(async () => {
  console.log('面板：' + base + '\n');

  // ① 读当前片段成元素文档
  const read = await run('jianpu.editor', { scope: 'clip', key: 'auto', meter: '4/4' });
  if (!read.ok || !read.json || !read.json.elements) {
    console.log('读不到元素文档：' + (read.hint || '（片段里没有音符？）'));
    return;
  }
  const els = read.json.elements;
  const rests = els.filter((e) => e.kind === 'rest' || e.rest);
  console.log('== ① 读进 ' + els.length + ' 个元素，其中**休止 ' + rests.length + ' 个**（空拍补出来的）');
  console.log('   前 8 个：' + els.slice(0, 8).map((e) => e.kind === 'rest' || e.rest ? '0/' + e.duration : (e.pitch + '/' + e.duration)).join(' '));
  for (const d of (read.json.diagnostics || [])) console.log('   ⚠ ' + d);

  // ② 原样喂回去（干跑）：休止跳过、位置不变
  const back = await run('jianpu.toNotes', {
    elements: JSON.stringify(els), key: String(read.json.key || 0), defaultLyric: 'la',
    target: 'current', startAt: 'clipStart', dryRun: '1',
  });
  if (!back.ok || !back.json) { console.log('\n== ② 写回失败：' + back.hint); return; }
  const bj = back.json;
  console.log('\n== ② 原样写回（干跑，起点 = 片段开头）');
  console.log('   元素 ' + bj.parsedElements + ' ⇒ 音符 ' + bj.notesToWrite +
    '（跳过 ' + bj.skipped + ' 个休止，其中孤儿延音 ' + (bj.orphanSustains || 0) + '）· tenuto ' + (bj.sustainNotes || 0));
  console.log('   前几个音符：' + (bj.preview || []).slice(0, 5).join(' | '));
  console.log('   ' + (bj.note || '').replace(/\s+/g, ' ').slice(0, 200));

  // ③ `0 -`：休止 + 延音线
  const probe = [
    { kind: 'note', pitch: 1, duration: 4, lyric: 'la' },
    { kind: 'rest', duration: 4 },
    { kind: 'sustain', duration: 4 },
    { kind: 'note', pitch: 2, duration: 4, lyric: 'la' },
  ];
  const r3 = await run('jianpu.toNotes', {
    elements: JSON.stringify(probe), key: '0', defaultLyric: 'la',
    target: 'current', startAt: 'clipStart', dryRun: '1',
  });
  console.log('\n== ③ `1 0 - 2`（休止 + 延音线）⇒ 应当只有 2 个音，且第二个落在 1440');
  if (r3.ok && r3.json) {
    console.log('   音符 ' + r3.json.notesToWrite + ' · 跳过 ' + r3.json.skipped +
      ' · 孤儿延音 ' + (r3.json.orphanSustains || 0));
    console.log('   ' + (r3.json.preview || []).join(' | '));
  } else {
    console.log('   失败：' + r3.hint);
  }

  // ④ ⭐ 两笔账分开算（这是回答"空拍处理有没有挪动音符"的关键）：
  //   (a) **休止那几段**：与 ACE 里真实的空隙**逐段比**，必须相等（我的改动不许挪任何东西）；
  //   (b) **时值折算**：ACE 的 `dur` 落不进简谱族（几分音符+附点）时会被折算 —— 那一笔是**旧有的**、
  //       而且诊断里已经如实报过；它才是"累计位置漂一点"的来源。
  const view = await run('xcopy.view', {});
  const aceNotes = (((view.json || {}).clips || [])[0] || {}).notes || [];
  const aceMin = aceNotes.length ? Math.min.apply(null, aceNotes.map((x) => x.pos)) : 0;
  const aceSorted = aceNotes.slice().sort((a, b) => a.pos - b.pos);
  const beatTicks = (e) => Math.round((4 / (e.duration || 4)) * (2 - Math.pow(2, -(e.dots || 0))) * 480);
  let ai = 0, acc = 0;
  let restSegs = 0, restTicks = 0, restMismatch = 0;
  let durMismatch = 0, durDrift = 0, paired = 0;
  // ⚠️ 一段空拍**可能被拆成好几个休止元素**（如 1200 = 960 + 240）⇒ 要按"连续几段休止"合成一次比，
  //    拿单个元素去比整段空隙会假报（第一版就是这么误报 23 段的）。
  let runTicks = 0;   // ⚠️ 别叫 `run` —— 外面有个 `run()` 函数，同名会把它遮住（TDZ 报错）
  const flushRun = () => {
    if (!runTicks) return;
    const prevEnd = ai > 0 ? aceSorted[ai - 1].pos + aceSorted[ai - 1].dur : aceMin;
    const nextPos = ai < aceSorted.length ? aceSorted[ai].pos : prevEnd + runTicks;
    restSegs++;
    restTicks += runTicks;
    if (nextPos - prevEnd !== runTicks) {
      restMismatch++;
      if (restMismatch <= 3) {
        const a = aceSorted[ai - 1] || {}, b = aceSorted[ai] || {};
        console.log('   ⚠ 第 ' + restSegs + ' 段空拍 ' + runTicks + ' tick，但 ACE 里那段空隙是 ' + (nextPos - prevEnd) +
          ' tick　（前一个音 pos ' + a.pos + ' dur ' + a.dur + '；下一个音 pos ' + b.pos + '）');
      }
    }
    runTicks = 0;
  };
  for (const e of els) {
    if (e.kind === 'barline') continue;
    const tk = beatTicks(e);
    const isRest = e.kind === 'rest' || e.rest;
    if (isRest) {
      runTicks += tk;
      acc += tk;
      continue;
    }
    flushRun();
    // 音符 / 连音：与 ACE 的同序音符比时值
    const an = aceSorted[ai];
    if (an) {
      paired++;
      if (an.dur !== tk) {
        durMismatch++;
        durDrift += (tk - an.dur);
      }
    }
    ai++;
    acc += tk;
  }
  flushRun();
  console.log('\n== ④ 两笔账分开算');
  console.log('   (a) 空拍 → 休止：' + restSegs + ' 段、合计 ' + restTicks + ' tick —— ' +
    (restMismatch === 0 ? '**与 ACE 里的空隙逐段相等** ✓（我的改动一个 tick 都没挪）'
      : '**有 ' + restMismatch + ' 段对不上** ✗'));
  console.log('   (b) 时值折算：ACE ' + aceSorted.length + ' 个音里配到 ' + paired + ' 个，' +
    (durMismatch === 0 ? '时值全部原样 ✓' : durMismatch + ' 个音符的 dur 落不进简谱族（累计差 ' + durDrift +
      ' tick）—— 这是**旧有的**折算，诊断里已如实报出'));
  console.log('   推出总长 ' + acc + ' tick');

  // ⑤ 两个出口也要能看到休止：MusicXML 里应当是 `<rest/>`，简谱文本里应当是 `0`
  const xml = await run('jianpu.fromClip', { scope: 'clip', format: 'musicxml', key: 'auto', meter: '4/4' });
  const terse = await run('jianpu.fromClip', { scope: 'clip', format: 'terse', key: 'auto', meter: '4/4' });
  const xmlOut = (xml.json || {}).output || '';
  const terseOut = (terse.json || {}).output || '';
  const nRest = (xmlOut.match(/<rest\s*\/>/g) || []).length;
  const nZero = (terseOut.match(/(^|\s)0(\s|$)/g) || []).length;
  console.log('\n== ⑤ 两个出口');
  console.log('   MusicXML：' + xmlOut.length + ' 字节，`<rest/>` **' + nRest + '** 个' +
    (nRest > 0 ? ' ✓' : ' ✗（空拍没被写成休止）'));
  console.log('   简谱文本：`0` 出现 **' + nZero + '** 次' + (nZero > 0 ? ' ✓' : '（这一份可能本来就没有空拍）'));
})();
