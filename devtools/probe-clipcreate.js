/* 真机探针：**核实 `clip create --notes` 到底有没有把音符写进去**。
 *
 * 起因：ACETool 真写真报成功（undo 里也落了"放置片段"这一条），但 `clip note-content` 读回 0 个音。
 * 两种可能：① ACE 收下了 `--notes` 但没写进音符 ② 我们发出去的 JSON 在参数传递里被改形。
 *
 * ⚠️ 绝不用 PowerShell 传 JSON —— 它会把内嵌双引号吃掉（实测报 "invalid JSON (key must be a string)"）。
 * 这里用 execFile 的**数组参数**，不经 shell，字节原样到 CLI。
 *
 * 用法：node probe-clipcreate.js
 */
const { execFileSync } = require('child_process');
const CLI = 'C:\\Program Files\\ACE Studio\\acestudio-cli.exe';

function cli(args) {
  const out = execFileSync(CLI, args, { encoding: 'utf8', maxBuffer: 32 << 20 });
  try { return JSON.parse(out); } catch (e) { return out; }
}

const NOTES = [
  { pos: 0, dur: 480, pitch: 60, lyric: '月' },
  { pos: 480, dur: 480, pitch: 62, lyric: '落' },
];

console.log('== clip list（写之前）');
let list = cli(['--json', 'clip', 'list', '--track-index', '0']);
const before = (list.clips || list.items || list).map ? (list.clips || list.items || list) : [];
console.log(JSON.stringify(before, null, 2).slice(0, 1200));

// 找一个空位（往后放，避免 CLIP_OVERLAP）
let pos = 20000;
for (const c of before) {
  const end = c.clipEnd != null ? c.clipEnd : c.end;
  if (end != null && end > pos) pos = end + 1920;
}
console.log('\n== 探针位置 ' + pos + 't');

const payload = JSON.stringify(NOTES);
console.log('== 发出去的 --notes（字节原样）\n' + payload);
const created = cli(['--json', 'clip', 'create', '--track-index', '0', '--pos', pos + 't', '--dur', '960t',
  '--type', 'sing', '--name', 'acetool-探针', '--notes', payload]);
console.log('\n== clip create 回包');
console.log(JSON.stringify(created, null, 2).slice(0, 1500));

const uuid = created && created.clipUuid;
if (uuid) {
  console.log('\n== 立刻读回 clip note-content（按 uuid）');
  const back = cli(['--json', 'clip', 'note-content', '--clip-uuid', uuid]);
  console.log(JSON.stringify(back, null, 2).slice(0, 2000));
}
console.log('\n== clip list（写之后）');
list = cli(['--json', 'clip', 'list', '--track-index', '0']);
const after = list.clips || list.items || list;
console.log(JSON.stringify(after, null, 2).slice(0, 1500));
