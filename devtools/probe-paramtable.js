/* 真机探针：**参数表**（用户 2026-10-07「参数展开…v1 就实参和包络两列」）
 *
 * 读两件事（都不碰工程，全是只读 + 干跑）：
 *   ① `xcopy.view`（每条轨）= 目标那一侧花名册推出的 **paramColumns** —— 钉"v1 两列 / v2 一列"
 *      是**现读**出来的，不是我们写死的世代名；
 *   ② `xcopy.within`（干跑，带 `paramSel`）—— 钉表交上去之后**逐格**怎么落（实参 → direct/user、
 *      包络 → envelope），以及列不存在时**如实跳过**的那句话。
 *
 * 用法：node devtools/probe-paramtable.js [url]
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

  // ① 每条轨的花名册 ⇒ 表格该有哪几列
  const mm = await run('view.minimap', { notes: '1' });
  const tracks = (mm.json && mm.json.tracks) || [];
  if (!tracks.length) { console.log('这条工程上没有轨道（读不到缩略图）：' + (mm.hint || '')); return; }
  console.log('== ① 每条轨的 paramColumns（由该轨花名册现推）');
  for (const t of tracks) {
    const v = await run('xcopy.view', { trackIndex: String(t.trackIndex) });
    const j = v.json;
    if (!v.ok || !j) { console.log('  轨 ' + t.trackIndex + '：读不到（' + (v.hint || '') + '）'); continue; }
    const cols = (j.paramColumns || []).map((c) => c.label + '（' + c.key + '→' + c.dstLayer + '）');
    const rows = (j.params || []).filter((p) => p.available && (p.writableLayers || []).length)
      .map((p) => p.param + '[' + (p.writableLayers || []).join('/') + ']');
    console.log('  轨 ' + t.trackIndex + '「' + (t.name || '') + '」· 片段「' + (j.clipName || '') + '」· 引擎 ' +
      (j.engineGeneration || '?') + ' · ' + (j.clipNoteCount || 0) + ' 个音符');
    console.log('    列（' + cols.length + '）：' + (cols.join(' · ') || '（没有可逐 tick 写的层）'));
    console.log('    行（' + rows.length + '）：' + (rows.join(' · ') || '（无）'));
  }

  // ② 干跑：把表交上去，看它逐格怎么落
  console.log('\n== ② xcopy.within 干跑（带 paramSel）');
  const cur = await run('xcopy.view', {});
  const cj = cur.json;
  if (!cur.ok || !cj) { console.log('  读不到当前片段：' + (cur.hint || '')); return; }
  const cols = (cj.paramColumns || []).map((c) => c.key);
  const rows = (cj.params || []).filter((p) => p.available && (p.writableLayers || []).length).map((p) => p.param);
  if (!rows.length || !cols.length) { console.log('  当前片段没有能逐 tick 写的参数 ⇒ 表格会是空的（这本身就是结论）'); return; }
  // 拿第一个参数，把**所有列**都勾上 ⇒ 该落几个层、不该落的要如实说
  const sel = {}; sel[rows[0]] = cols.slice();
  console.log('  当前片段引擎 ' + (cj.engineGeneration || '?') + ' · 列 = ' + cols.join('/') +
    ' · 拿「' + rows[0] + '」把每一列都勾上：' + JSON.stringify(sel));
  const bars = (cj.clipTicks || 0) / (cj.ticksPerBar || 1920);
  const plan = await run('xcopy.within', {
    srcStart: '1', srcEnd: String(Math.min(2, Math.max(1, Math.floor(bars / 4) + 1))),
    dstStart: String(Math.min(3, Math.max(2, Math.floor(bars / 2)))), dryRun: '1', params: '1',
    paramSel: JSON.stringify(sel),
  });
  if (!plan.ok || !plan.json) { console.log('  干跑失败：' + (plan.hint || '')); return; }
  const j = plan.json;
  console.log('  配上 ' + ((j.pairs && j.pairs[0] && j.pairs[0].matched) || 0) + ' 个音符 · 参数要写 ' +
    (j.willWriteParamLayers || 0) + ' 层 / ' + (j.paramTicks || 0) + ' tick');
  for (const c of (j.plannedCalls || []).filter((c) => c.indexOf('vocalparam') >= 0)) console.log('    ' + c);
  for (const s of (j.skipped || []).filter((s) => /参数|层|实参|包络/.test(s))) console.log('    ⚠ ' + s);

  // ③ 表里一个格子都不勾 ⇒ 必须"什么都不写"（不能退回"所有参数全搬"）
  console.log('\n== ③ 表交上来但一格都没勾（paramSel = {}）');
  const empty = await run('xcopy.within', {
    srcStart: '1', srcEnd: '2', dstStart: '3', dryRun: '1', params: '1', paramSel: '{}',
  });
  if (empty.ok && empty.json) {
    const e = empty.json;
    console.log('  参数要写 ' + (e.willWriteParamLayers || 0) + ' 层 · vocalparam 命令 ' +
      (e.plannedCalls || []).filter((c) => c.indexOf('vocalparam') >= 0).length + ' 条');
    for (const s of (e.skipped || []).filter((s) => /参数/.test(s))) console.log('    ' + s);
  } else {
    console.log('  干跑失败：' + (empty.hint || ''));
  }
})();
