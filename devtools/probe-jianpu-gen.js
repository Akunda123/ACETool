/* 真机探针：**谱 → 音符的生成**（用户 2026-10-07：
 * 「jianpu.editor，直接把 nt 块拿过来，下面是生成，生成音符的位置是当前 clip 从播放头所在小节开始生成音符」）
 *
 * 全是**只读 + 干跑**：`jianpu.editor`（读）+ `jianpu.toNotes`（dryRun=1）。
 * 钉三件事：
 *   ① 生成的那份输入**就是编辑器那份元素**（`jianpu.editor` 的 `elements` 原样喂回 `jianpu.toNotes`）；
 *   ② 起点默认落在**播放头所在小节的起点**（不是播放头的精确 tick）：报告里报播放头 tick、
 *      第几小节、对齐到哪条小节线；
 *   ③ `playheadExact` 那一档仍然给播放头的精确 tick（旧行为还在，只是默认换了）。
 *
 * 用法：node devtools/probe-jianpu-gen.js [url]
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

async function gen(elements, startAt) {
  const rep = await run('jianpu.toNotes', {
    elements: JSON.stringify(elements), key: '0', defaultLyric: 'la',
    target: 'current', startAt: startAt || 'playhead', dryRun: '1',
  });
  if (!rep.ok || !rep.json) return { err: rep.hint || '失败' };
  const j = rep.json;
  return {
    src: j.source, parsed: j.parsedElements, write: j.notesToWrite,
    sustain: j.sustainNotes, skipped: j.skipped,
    playhead: j.playheadTick, bar: j.startBar, barTick: j.startBarTick, start: j.startTick,
    first: (j.preview || [])[0],
    note: (j.note || '').replace(/\s+/g, ' ').slice(0, 240),
  };
}

(async () => {
  console.log('面板：' + base + '\n');

  // ① 直接拿 nt 块那份元素：先把当前片段读成元素文档（这就是编辑器里那份）
  const read = await run('jianpu.editor', { scope: 'clip', key: 'auto', meter: '4/4' });
  if (!read.ok || !read.json || !read.json.elements) {
    console.log('读不到当前片段的元素文档：' + (read.hint || '（片段里没有音符？）'));
    return;
  }
  const els = read.json.elements;
  console.log('== ① 编辑器那份元素（前 6 个）：' + JSON.stringify(els.slice(0, 6)));
  console.log('   共 ' + els.length + ' 个 · 调式 ' + read.json.keyName + ' · 拍号 ' + read.json.meter);

  // ② 直接把它喂回去生成（干跑）：起点应当落在播放头所在小节的起点
  console.log('\n== ② 生成（默认档 = 播放头所在小节的起点）');
  console.log(JSON.stringify(await gen(els), null, 1));

  // ③ 对照：精确档
  console.log('\n== ③ 生成（playheadExact = 播放头的精确 tick）');
  console.log(JSON.stringify(await gen(els, 'playheadExact'), null, 1));

  // ④ 片段开头那档
  console.log('\n== ④ 生成（clipStart = 片段开头）');
  console.log(JSON.stringify(await gen(els, 'clipStart'), null, 1));
})();
