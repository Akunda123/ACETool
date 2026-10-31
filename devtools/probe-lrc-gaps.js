// 量「音符之间的缺口」分布 —— 解释为什么我们切出的行数比 ACE 自带的多一倍。
//
// 原脚本的切行判据是**严格不等**：`Notes[i].getEnd() != Notes[i+1].getOnset()` ⇒ 有 1 tick 的缝也换行。
// ACE 自己的句模型说的是 "auto-split based on timing gaps" —— 听上去有阈值。
// 这个脚本把缺口分布打出来，并算「阈值取 N tick 时会切出几行」，让这件事有数可依。
//
// 用法：node devtools/probe-lrc-gaps.js
const { execFileSync } = require('child_process');

const CLI = 'C:\\Program Files\\ACE Studio\\acestudio-cli.exe';
function cli(args) {
  const out = execFileSync(CLI, args.concat(['--json']), { maxBuffer: 128 * 1024 * 1024 }).toString('utf8');
  return JSON.parse(out.charCodeAt(0) === 0xfeff ? out.slice(1) : out);
}

const es = cli(['editor', 'status']);
const notes = cli(['clip', 'note-content', '--track-index', String(es.trackIndex), '--clip-index', String(es.clipIndex)]).notes;
console.log('片段：' + es.clipName + ' · 音符 ' + notes.length);

// 相邻音符之间的缝（>0 才是缝）
const gaps = [];
for (let i = 0; i < notes.length - 1; i++) {
  const g = notes[i + 1].pos - notes[i].endPos;
  if (g > 0) gaps.push({ i, g, lyric: notes[i].lyric + ' → ' + notes[i + 1].lyric });
}
console.log('有缝的相邻对：' + gaps.length + ' / ' + (notes.length - 1));

const dist = new Map();
for (const { g } of gaps) {
  const bucket = g <= 2 ? String(g) : (g <= 10 ? '3–10' : (g <= 60 ? '11–60' : (g <= 240 ? '61–240' : '>240')));
  dist.set(bucket, (dist.get(bucket) || 0) + 1);
}
console.log('缝的大小分布（tick）：');
for (const k of ['1', '2', '3–10', '11–60', '61–240', '>240']) {
  if (dist.has(k)) console.log('  ' + k.padStart(7) + ' : ' + dist.get(k) + ' 处');
}

// 阈值 N ⇒ 只把 > N 的缝当换行
console.log('\n阈值 → 切出几行（原脚本 = 阈值 0）：');
for (const th of [0, 1, 2, 5, 10, 20, 30, 60, 120, 240]) {
  let lines = 1;
  for (const { g } of gaps) if (g > th) lines++;
  console.log('  阈值 ' + String(th).padStart(3) + ' tick → ' + String(lines).padStart(4) + ' 行');
}

// 前几处小缝长什么样
console.log('\n最小的 8 处缝：');
for (const x of gaps.slice().sort((a, b) => a.g - b.g).slice(0, 8)) {
  console.log('  ' + String(x.g).padStart(4) + ' tick  @' + notes[x.i].pos + '   ' + x.lyric);
}
