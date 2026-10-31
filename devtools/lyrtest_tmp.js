// 临时：验证 ACE 是否接受"把某个音符的歌词设成空串"（删词的真语义）。
const { spawnSync } = require('child_process');
const CLI = 'C:\\Program Files\\ACE Studio\\acestudio-cli.exe';
function cli(args) {
  const r = spawnSync(CLI, args, { encoding: 'utf8' });
  return { code: r.status, out: (r.stdout || '').trim(), err: (r.stderr || '').trim() };
}
console.log('track create:', JSON.stringify(cli(['track', 'create', '--type', 'instrument', '--name', 'LYR-TEST', '--json']).out.slice(0, 120)));
const t = JSON.parse(cli(['track', 'list', '--json']).out);
const tr = (t.tracks || []).filter((x) => x.trackName === 'LYR-TEST' && x.region === 'arrangement').pop();
console.log('track:', tr && tr.trackIndex);
const notes = [
  { pos: 0, dur: 480, pitch: 60, lyric: 'la' },
  { pos: 480, dur: 480, pitch: 62, lyric: 'le' },
];
const c = cli(['clip', 'create', '--track-index', String(tr.trackIndex), '--pos', '0t', '--dur', '960t',
  '--type', 'instrument', '--name', 'LYR', '--notes', JSON.stringify(notes), '--json']);
console.log('clip create:', c.code, c.out.slice(0, 100), c.err.slice(0, 100));
const nc = JSON.parse(cli(['clip', 'note-content', '--track-index', String(tr.trackIndex), '--clip-index', '0', '--json']).out);
const ids = nc.notes.map((n) => n.noteUuid);
console.log('notes:', nc.notes.map((n) => n.lyric).join(','));
const w = cli(['note', 'set-lyric', '--note', ids[0], '--lyrics', JSON.stringify([''])]);
console.log('set empty:', w.code, w.err.slice(0, 200));
const nc2 = JSON.parse(cli(['clip', 'note-content', '--track-index', String(tr.trackIndex), '--clip-index', '0', '--json']).out);
console.log('after:', JSON.stringify(nc2.notes.map((n) => n.lyric)));
console.log('cleanup clip:', cli(['clip', 'delete', '--clip-uuid', nc.clipUuid || (nc.notes && '')]).code);
