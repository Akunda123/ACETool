// 真机演示：撤销栈"穿噪音"（用户 2026-10-15：「如果有选中音符什么的批量撤回直接撤」）
//
// 场景（完全自反，不落盘、不动工程）：
//   ① 基线游标 = anchor
//   ② 我们的一次真写（`note move --pitch-by +1`）⇒ 内容条目，署名「远程控制」
//   ③ 再压一条噪音（编排区拉时间选区 ⇒ 「更改选择」，署名也「远程控制」）
//   ④ `history.undoBatch`（anchor=①、count=1）⇒ 应当**穿过**噪音、把我们的写入退掉
//   ⑤ 读回：音高回到原值、游标回到 anchor
const { execFileSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const CLI = 'C:\\Program Files\\ACE Studio\\acestudio-cli.exe';
const NOTE = '{e2590def-64e6-4a8a-8683-1083b7e432de}'; // 「沉」原来 pitch 64
const PANEL = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA, 'ACETool', 'panel.json'), 'utf8')).url.replace(/\/$/, '');

function cli(args) {
  return JSON.parse(execFileSync(CLI, [...args, '--json'], { encoding: 'utf8', maxBuffer: 1 << 26 }));
}
function stack(tag) {
  const j = cli(['history', 'list']);
  console.log(String(tag).padEnd(26) + ' count=' + j.count + ' 顶=' + ((j.entries[0] || {}).name || '-') +
    ' actor=' + JSON.stringify((j.entries[0] || {}).actor || ''));
  return j.index;
}
function pitchOf(uuid) {
  const j = cli(['clip', 'note-content', '--track-index', '0', '--clip-index', '0']);
  const n = (j.notes || []).find((x) => x.noteUuid === uuid);
  return n ? n.pitch : null;
}
async function run(toolId, values) {
  const r = await fetch(PANEL + '/api/run', {
    method: 'POST', headers: { 'Content-Type': 'application/json', 'X-ACETool': '1' },
    body: JSON.stringify({ toolId, values }),
  });
  const j = await r.json();
  return { ok: j.ok, hint: j.hint, json: ((j.calls || [])[0] || {}).json || {} };
}

(async () => {
  console.log('=== 撤销栈「穿噪音」真机演示 ===\n');
  console.log('写入前「沉」的音高：' + pitchOf(NOTE));
  const anchor = stack('① 基线（anchor）');
  console.log();

  cli(['note', 'move', '--note-uuid', NOTE, '--pitch-by', '1']);
  stack('② 我们真写（+1 半音）');
  console.log('   现在音高：' + pitchOf(NOTE));

  cli(['selection', 'set', '--scope', 'arrangement',
    '--horizontal-selection', JSON.stringify({ begin: 30720, end: 31440 }),
    '--vertical-selection', JSON.stringify({ beginRegion: 'arrangement', beginIndex: 0, endRegion: 'arrangement', endIndex: 1 })]);
  stack('③ 再压一条噪音（编排区选区）');
  console.log();

  console.log('④ 调面板的「撤回刚才的一批」（anchor=' + anchor + ', count=1）');
  const d = await run('history.undoBatch', { anchor: String(anchor), count: '1', actor: '远程控制' });
  const rep = d.json;
  console.log('   ok=' + d.ok + (d.hint ? ' hint=' + d.hint : ''));
  console.log('   willUndo=' + rep.willUndo + ' · 内容=' + JSON.stringify(rep.names || []) +
    ' · 穿过=' + JSON.stringify(rep.skipped || []));
  console.log('   undone=' + JSON.stringify(rep.undone || []));
  console.log('   cursorAfter=' + rep.cursorAfter + '（锚点 ' + anchor + '）');
  console.log('   提示：' + rep.note);
  console.log();
  stack('⑤ 之后');
  console.log('   音高现在：' + pitchOf(NOTE) + '（应当回到原值）');
})();
