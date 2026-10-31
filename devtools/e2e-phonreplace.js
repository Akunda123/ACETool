// 真机验 `phoneme.replace`（**全程干跑，一个字节都不写**）。
//
// 验三件事：
//  ① 报的数对不对（扫了几个音符 / 含查找音素的有几个）—— 和 `clip note-content` 自己数一遍对账
//  ② 替换词不在该音符语言里的音素清单里时，**干跑就能说清**（而不是写下去被 ACE 拒）
//  ③ 报告里"会被冻结成 override"的计数在不在（这是这个工具最要紧的提醒）
//
// 用法：node devtools/e2e-phonreplace.js [find] [replace]
const { execFileSync } = require('child_process');
const fs = require('fs');
const path = require('path');
const os = require('os');

const CLI = 'C:\\Program Files\\ACE Studio\\acestudio-cli.exe';
const H = { 'Content-Type': 'application/json', 'X-ACETool': '1' };
const BASE = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json'), 'utf8')).url.replace(/\/$/, '');
const FIND = process.argv[2] || 'a';
const REPL = process.argv[3] === undefined ? 'A' : process.argv[3];

function cli(args) {
  const out = execFileSync(CLI, args.concat(['--json']), { maxBuffer: 128 * 1024 * 1024 }).toString('utf8');
  return JSON.parse(out.charCodeAt(0) === 0xfeff ? out.slice(1) : out);
}

let bad = 0;
function ok(name, cond, extra) {
  console.log((cond ? '  ok   ' : '  FAIL ') + name + (extra ? '  — ' + extra : ''));
  if (!cond) bad++;
}

async function run(find, replace, scope) {
  const d = await (await fetch(BASE + '/api/run', {
    method: 'POST', headers: H,
    body: JSON.stringify({ toolId: 'phoneme.replace', values: { find: find, replace: replace, scope: scope || 'clip', dryRun: '1' } }),
  })).json();
  return { d, j: d.calls && d.calls[0] && d.calls[0].json };
}

(async () => {
  const es = cli(['editor', 'status']);
  if (!es.isAvailable) { console.error('ACE 编辑器里没打开片段'); process.exit(1); }
  const notes = cli(['clip', 'note-content', '--track-index', String(es.trackIndex), '--clip-index', String(es.clipIndex)]).notes;
  console.log('片段：' + es.clipName + ' · ' + notes.length + ' 个音符 · 查找 ' + JSON.stringify(FIND) + ' → ' + JSON.stringify(REPL));

  const { d, j } = await run(FIND, REPL);
  if (!d.ok || !j) { console.error('干跑失败：' + (d.hint || '')); process.exit(1); }
  console.log('  报告：扫 ' + j.scanned + ' · 命中 ' + j.hit + ' · 会写 ' + j.willSet + ' · 会冻结 ' + j.willFreeze
    + (j.skipped && j.skipped.length ? ' · 跳过 ' + j.skipped.length : ''));
  ok('干跑标记为 DryRun（没写）', j.dryRun === true);
  ok('扫的音符数 = 片段音符数', j.scanned === notes.length, j.scanned + ' vs ' + notes.length);

  // ⭐ 自己数一遍对账：有多少音符的音素里含 FIND（token 级）
  // ⚠️ `phoneme list --note X` 的回包是 `{capabilities, notes:[{phonemes:[...]}]}` ——
  // 第一版按 `rows[0].phonemes` 取，取到 undefined ⇒ 数出 0 个，把"报告 77"误判成错。
  let mine = 0;
  for (const n of notes) {
    const ph = cli(['phoneme', 'list', '--note', n.noteUuid]);
    const row = ((ph.notes && ph.notes[0]) || {});
    const arr = row.phonemes || [];
    if (arr.indexOf(FIND) >= 0) mine++;
  }
  ok('命中数与逐音符读数一致', j.hit === mine, '报告 ' + j.hit + ' vs 自己数 ' + mine);
  ok('会写的数 ≤ 命中数（有些可能因为"替换后一样"被跳过）', j.willSet <= j.hit, j.willSet + ' / ' + j.hit);
  if (j.willFreeze > 0) {
    console.log('  ⚠️ 其中 ' + j.willFreeze + ' 个本来没被改写过 —— 真写会把它们的发音**冻结**成 override');
  }

  // ⭐ 预校验：替换词不在语言清单里 ⇒ 干跑就要说清，而不是写下去被 ACE 拒
  const { d: d2, j: j2 } = await run(FIND, 'zzz');
  if (!d2.ok || !j2) { console.error('第二个干跑失败：' + (d2.hint || '')); process.exit(1); }
  ok('替换词不在音素清单里 ⇒ 干跑就报出来（不写）', j2.willSet === 0 && (j2.skipped || []).length > 0,
    'willSet=' + j2.willSet + ' skipped=' + ((j2.skipped || [])[0] || ''));
  ok('跳过原因点明了"不在音素清单里"', ((j2.skipped || [])[0] || '').indexOf('音素清单') >= 0,
    (j2.skipped || [])[0] || '');

  console.log('\n' + (bad ? bad + ' 项失败' : '全部通过') + '（全程干跑，工程没被改）');
  process.exitCode = bad ? 1 : 0;
})().catch((e) => { console.error('崩了：' + e.stack); process.exitCode = 1; });
