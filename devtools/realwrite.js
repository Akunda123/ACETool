/* **真实写入**验证：把一小段（含中文歌词 + 1 个 tenuto 连音）真的写进 ACE，**新建一个片段**。
 *
 * 为什么必须做一次真写：干跑只证明"我们算得对"，证明不了 ACE **收不收这个载荷** ——
 * 尤其是 tenuto（歌词字段里的字面 `-`）和中文歌词的编码。这条一直是未验证项。
 *
 * 为什么用 `target=newClip`：它在当前轨**另起一个片段**，不动任何既有音符 —— 非破坏性；
 * 事后可以用 `history undo` 或删掉这个片段还原（脚本会打印该怎么做）。
 *
 * 用法：node realwrite.js http://127.0.0.1:51703/ [--yes]
 */
const base = process.argv[2] || 'http://127.0.0.1:51703/';
const go = process.argv.indexOf('--yes') >= 0;
const H = { 'Content-Type': 'application/json', 'X-ACETool': '1' };

const ELEMS = [
  { kind: 'note', pitch: 1, duration: 4, lyric: '月' },
  { kind: 'note', pitch: 2, duration: 4, lyric: '落' },
  { kind: 'note', pitch: 3, duration: 4, lyric: '乌' },
  { kind: 'sustain', duration: 4 },              // ⇒ ACE 里歌词为 `-` 的 tenuto 音符
  { kind: 'note', pitch: 5, duration: 2, lyric: '霜' },
];

(async () => {
  // 先干跑一次，把要写的东西原样打出来
  const dry = await (await fetch(new URL('/api/run', base), {
    method: 'POST', headers: H,
    body: JSON.stringify({ toolId: 'jianpu.toNotes', values: {
      elements: JSON.stringify(ELEMS), key: '0', defaultLyric: 'la',
      target: 'newClip', startAt: 'clipStart', dryRun: '1' } }),
  })).json();
  const d = dry.calls && dry.calls[0] && dry.calls[0].json;
  console.log('干跑：');
  console.log('  轨 #' + (d && d.trackIndex) + '，当前片段 ' + (d && d.clip) + ' ' + (d && d.clipUuid));
  console.log('  要写 ' + (d && d.notesToWrite) + ' 个音（tenuto ' + (d && d.sustainNotes) + ' 个）');
  console.log('  预览 ' + JSON.stringify(d && d.preview));
  if (!go) { console.log('\n（没带 --yes，只干跑，不真写）'); return; }

  console.log('\n真写（在当前轨新建片段）…');
  const r = await (await fetch(new URL('/api/run', base), {
    method: 'POST', headers: H,
    body: JSON.stringify({ toolId: 'jianpu.toNotes', values: {
      elements: JSON.stringify(ELEMS), key: '0', defaultLyric: 'la',
      target: 'newClip', startAt: 'clipStart', dryRun: '0' } }),
  })).json();
  console.log('  ok=' + r.ok + '  hint=' + (r.hint || '（无）'));
  const res = r.calls && r.calls[0] && r.calls[0].json;
  console.log('  回包：' + JSON.stringify({
    trackIndex: res && res.trackIndex, clipUuid: res && res.clipUuid, write: res && res.notesToWrite,
    sustains: res && res.sustainNotes, skipped: res && res.skipped, done: res && res.done, note: res && res.note,
  }, null, 2));
  if (!r.ok) { console.log('  （写失败，详情见上）'); process.exit(1); }
  console.log('\n⚠️ 收尾：这段是新建的片段，不是你的原内容。要还原请用 `history undo`（栈顶那一条），');
  console.log('   或者直接删掉那个新片段。');
})().catch((e) => { console.error('炸了：', e); process.exit(2); });
