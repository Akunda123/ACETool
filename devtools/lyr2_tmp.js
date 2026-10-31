// 临时：在 **Sing** 轨上试「把歌词设成空串」与「设成 -」，看 ACE 接受哪个、读回是什么。
const { spawnSync } = require('child_process');
const CLI = 'C:\\Program Files\\ACE Studio\\acestudio-cli.exe';
const j = (args) => {
  const r = spawnSync(CLI, args, { encoding: 'utf8' });
  let o = null;
  try { o = JSON.parse((r.stdout || '').trim()); } catch (e) { }
  return { code: r.status, out: (r.stdout || '').trim(), err: (r.stderr || '').trim(), j: o };
};
const tr = j(['track', 'create', '--type', 'sing', '--name', 'LYR2', '--json']).j;
console.log('track:', tr && tr.trackIndex);
const notes = [
  { pos: 0, dur: 480, pitch: 60, lyric: 'la' },
  { pos: 480, dur: 480, pitch: 62, lyric: 'le' },
];
const c = j(['clip', 'create', '--track-index', String(tr.trackIndex), '--pos', '0t', '--dur', '960t',
  '--type', 'sing', '--name', 'LYR2', '--notes', JSON.stringify(notes), '--json']);
console.log('clip:', c.code, c.err.slice(0, 120));
const nc = j(['clip', 'note-content', '--track-index', String(tr.trackIndex), '--clip-index', '0', '--json']).j;
console.log('before:', JSON.stringify(nc.notes.map((n) => n.lyric)));
const ids = nc.notes.map((n) => n.noteUuid);
const w1 = j(['note', 'set-lyric', '--note-uuid', ids[0], '--lyric', '']);
console.log('set "" →', w1.code, w1.err.slice(0, 160));
const nc2 = j(['clip', 'note-content', '--track-index', String(tr.trackIndex), '--clip-index', '0', '--json']).j;
console.log('after "":', JSON.stringify(nc2.notes.map((n) => n.lyric)));
const w2 = j(['note', 'set-lyric', '--note-uuid', ids[0], '--lyric', '-']);
console.log('set "-" →', w2.code, w2.err.slice(0, 160));
const nc3 = j(['clip', 'note-content', '--track-index', String(tr.trackIndex), '--clip-index', '0', '--json']).j;
console.log('after "-":', JSON.stringify(nc3.notes.map((n) => n.lyric)));
