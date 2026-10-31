/* 清场：把**我做真机探针时建出来的那些片段**删掉，并先确认新写的那段真的有 5 个音。
 *
 * 纪律：① 只删**名字/时间戳都能对上是我建的**那些片段，绝不按"位置下标"猜；
 *       ② 每个 uuid 先列出来给人看一遍，再一次性删（`clip delete` 是 all-or-nothing，打错一个 uuid 就整笔不删）；
 *       ③ 用户原本那个片段「明天银去行取钱」**永不入选**（按 uuid 白名单排除）。
 *
 * 用法：node cleanup-probe-clips.js            # 只看
 *       node cleanup-probe-clips.js --delete   # 真删
 */
const { execFileSync } = require('child_process');
const CLI = 'C:\\Program Files\\ACE Studio\\acestudio-cli.exe';
function cli(args) {
  try { return JSON.parse(execFileSync(CLI, args, { encoding: 'utf8', maxBuffer: 32 << 20 })); }
  catch (e) { return { __error: e.message.split('\n').slice(0, 3).join(' | ') }; }
}

const KEEP = '{c5608f9d-6054-44e4-a660-bf4c388d7b8b}';   // 用户原有的片段
const MINE = /^(月落乌霜|acetool-探针|探针-|dur探针)/;

const list = cli(['--json', 'clip', 'list', '--track-index', '0']);
const clips = list.clips || list.items || list;
if (!Array.isArray(clips)) { console.log('clip list 读不出来：' + JSON.stringify(list)); process.exit(1); }

console.log('轨道 0 上的片段：');
clips.forEach((c, i) => console.log('  [' + i + '] ' + c.clipBegin + '..' + c.clipEnd + '  noteCount=' +
  String(c.noteCount).padStart(2) + '  ' + c.clipName + '  ' + c.clipUuid));

const mine = clips.filter((c) => c.clipUuid !== KEEP && MINE.test(c.clipName));
console.log('\n判定为**探针建的** ' + mine.length + ' 个：');
mine.forEach((c) => console.log('  ' + c.clipName + '  ' + c.clipUuid + '  noteCount=' + c.noteCount));

const fresh = clips.filter((c) => c.noteCount === 5 && c.clipName !== '明天银去行取钱');
if (fresh.length) {
  console.log('\n刚真写成功的那段（noteCount=5）：');
  fresh.forEach((c) => console.log('  ' + c.clipName + '  ' + c.clipBegin + '..' + c.clipEnd + '  ' + c.clipUuid));
  const uuid = fresh[fresh.length - 1].clipUuid;
  const back = cli(['--json', 'clip', 'note-content', '--track-index', '0',
    '--clip-index', String(clips.findIndex((c) => c.clipUuid === uuid))]);
  console.log('  读回内容：' + JSON.stringify(back.notes || back, null, 2).slice(0, 900));
}

// 保留工具**真写真成功**的那一段（uuid 已知，是证据），其余探针都删
const KEEP_EVIDENCE = '{ae3f152a-20d7-4246-9aa0-26e0128b2413}';
if (process.argv.indexOf('--delete') < 0) { console.log('\n（没带 --delete，只列不删）'); process.exit(0); }

const toDelete = mine.filter((c) => c.clipUuid !== KEEP_EVIDENCE);
if (!toDelete.length) { console.log('\n没有要删的（探针都清过了）'); process.exit(0); }
const args = ['--json', 'clip', 'delete'];
for (const c of toDelete) args.push('--clip-uuid', c.clipUuid);
console.log('\n删除 ' + toDelete.length + ' 个：' + toDelete.map((c) => c.clipName).join('、'));
const del = cli(args);
console.log(JSON.stringify(del, null, 2).slice(0, 800));
const after = cli(['--json', 'clip', 'list', '--track-index', '0']);
const a = after.clips || after.items || after;
console.log('\n删后轨道 0：');
(a || []).forEach((c, i) => console.log('  [' + i + '] ' + c.clipBegin + '..' + c.clipEnd + '  ' + c.clipName + '  noteCount=' + c.noteCount));
