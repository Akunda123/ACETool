/* 真机探针：**轨内复制的作用域 = 整条轨**（用户 2026-10-07：「xcopy.within 作用于轨内所有 clip，遍历绝对时间」）
 *
 * 全是**只读 + 干跑**（`dryRun=1`）：只调 `view.minimap` / `xcopy.view`（读）与 `xcopy.within`（干跑）。
 *
 * 钉四件事：
 *   ① 报告说的是"整条轨 + 几个片段 + 工程绝对 tick"（不再是"当前片段里的片段内 tick"）；
 *   ② 音素/歌词/音高那半照旧能配上（配对走绝对时间上的"相对位置"）；
 *   ③ 参数那一半按**绝对窗口**读、按目标片段写（本机两条轨各只有 1 个片段 ⇒ 落点与旧版一致，可对照）；
 *   ④ `dynamic` 那一路在有它的那一条轨上照样出计划。
 *
 * 用法：node devtools/probe-within-track.js [url]
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

function brief(j) {
  const pv = (j.pairs && j.pairs[0]) || {};
  const calls = (j.plannedCalls || []);
  const paramCalls = calls.filter((c) => c.indexOf('vocalparam') >= 0);
  return {
    mode: j.mode, track: j.srcTrackName, trackClips: j.trackClipCount,
    src: pv.srcClip, dst: pv.dstClip,
    notes: pv.srcNotes + '/' + pv.dstNotes, matched: j.notesMatched,
    srcOnly: j.srcOnlyNotes, dstOnly: j.dstOnlyNotes,
    will: {
      lyric: j.willSetLyrics, pitch: j.willSetPitch, phon: j.willSetPhonemes,
      paramLayers: j.willWriteParamLayers, paramTicks: j.paramTicks, dyn: j.willWriteDynamicAnchors,
    },
    calls: calls.length, paramCalls: paramCalls,
    skips: (j.skipped || []).slice(0, 6),
  };
}

(async () => {
  console.log('面板：' + base + '\n');

  const cur = await run('xcopy.view', {});
  const v = cur.json;
  if (!cur.ok || !v) { console.log('读不到当前片段：' + (cur.hint || '')); return; }
  const rows = (v.params || []).filter((p) => p.available);
  console.log('当前轨「' + v.trackName + '」· 引擎 ' + v.engineGeneration + ' · ' +
    (v.clips || []).length + ' 个片段 · 参数：' +
    rows.map((p) => p.param + (p.writableLayers && p.writableLayers.length ? '[' + p.writableLayers.join('/') + ']' : '[锚点]')).join(' '));

  // ① 属性那半：源 8–10 小节 → 目标 11 小节（干跑）
  console.log('\n== ① 属性（歌词/音高/音素），源 8–10 ⇒ 目标 11');
  const a = await run('xcopy.within', {
    srcStart: '8', srcEnd: '10', dstStart: '11', lyrics: '1', pitch: '1', phonemes: '1', dryRun: '1',
  });
  if (a.ok) console.log(JSON.stringify(brief(a.json), null, 1)); else console.log('失败：' + a.hint);

  // ② 参数那半：同一段范围 + 参数表（拿第一个有 dense 可写层的参数、勾"实参"）
  const first = rows.filter((p) => (p.writableLayers || []).length)[0];
  if (first) {
    const sel = {}; sel[first.param] = ['effective'];
    console.log('\n== ② 参数，源 8–10 ⇒ 目标 11，paramSel=' + JSON.stringify(sel));
    const b = await run('xcopy.within', {
      srcStart: '8', srcEnd: '10', dstStart: '11', params: '1', paramSel: JSON.stringify(sel), dryRun: '1',
    });
    if (b.ok) console.log(JSON.stringify(brief(b.json), null, 1)); else console.log('失败：' + b.hint);
  }

  // ③ dynamic（只有 v2 有）
  if (rows.some((p) => p.param === 'dynamic')) {
    console.log('\n== ③ dynamic 锚点，源 8–10 ⇒ 目标 11');
    const c = await run('xcopy.within', {
      srcStart: '8', srcEnd: '10', dstStart: '11', dynamic: '1', dryRun: '1',
    });
    if (c.ok) console.log(JSON.stringify(brief(c.json), null, 1)); else console.log('失败：' + c.hint);
  } else {
    console.log('\n== ③ dynamic：这一条轨的花名册里没有 dynamic（v1 引擎没有它）⇒ 跳过（工具会如实说）');
  }
})();
