// 真机演示：**按效果判噪音**（用户 2026-10-15：「只要不是实际落在工程的改动，都是噪音」）
//
// 关键设计：把 `skipNames` **清空** ⇒ 那条「更改选择」不在任何名单里，只能靠效果判：
//   `project save` 把 dirty 归零 → 撤一条 → 看 `dirty`：还是 false ⇒ 没落在工程里 = 噪音（穿过去）。
// 全程自反：内存里 +1 半音，演示完由撤回本身还原（不落盘）。
const { execFileSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const CLI = 'C:\\Program Files\\ACE Studio\\acestudio-cli.exe';
const NOTE = '{e2590def-64e6-4a8a-8683-1083b7e432de}'; // 「沉」原 pitch 64
const PANEL = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA, 'ACETool', 'panel.json'), 'utf8')).url.replace(/\/$/, '');

function cli(args) { return JSON.parse(execFileSync(CLI, [...args, '--json'], { encoding: 'utf8', maxBuffer: 1 << 26 })); }
function stack(tag) {
  const j = cli(['history', 'list']);
  console.log(String(tag).padEnd(30) + ' index=' + j.index + '/' + j.count + ' 顶=' + ((j.entries[0] || {}).name || '-') +
    ' actor=' + JSON.stringify((j.entries[0] || {}).actor || ''));
  return j.index;
}
function pitch() {
  const j = cli(['clip', 'note-content', '--track-index', '0', '--clip-index', '0']);
  const n = (j.notes || []).find((x) => x.noteUuid === NOTE);
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
  console.log('=== 撤销栈「按效果判噪音」真机演示 ===\n');
  console.log('起点：音高 ' + pitch() + ' · dirty=' + cli(['project', 'dirty']).dirty);
  const anchor = stack('① 基线（anchor）');
  console.log();

  cli(['note', 'move', '--note-uuid', NOTE, '--pitch-by', '1']);
  stack('② 我们真写（+1 半音）');
  console.log('   音高 → ' + pitch());

  cli(['selection', 'set', '--scope', 'arrangement',
    '--horizontal-selection', JSON.stringify({ begin: 30720, end: 31440 }),
    '--vertical-selection', JSON.stringify({ beginRegion: 'arrangement', beginIndex: 0, endRegion: 'arrangement', endIndex: 1 })]);
  stack('③ 压一条噪音（编排区选区）');
  console.log();

  console.log('④ 撤回（anchor=' + anchor + ', count=1, **skipNames 清空** ⇒ 只能按效果判）');
  const d = await run('history.undoBatch', {
    anchor: String(anchor), count: '1', actor: '远程控制', skipNames: '', checkEffect: '1',
    // 面板真实流程会带着「这一批的名字」（= 我们写出来的那些）⇒ 顶上那条「更改选择」就**不能**被证明是我们的
    names: JSON.stringify(['移动音符']),
  });
  const rep = d.json;
  console.log('   ok=' + d.ok + (d.hint ? ' hint=' + d.hint : ''));
  console.log('   探针：' + JSON.stringify(rep.probed || []));
  console.log('   内容=' + JSON.stringify(rep.names || []) + ' · 穿过=' + JSON.stringify(rep.skipped || []));
  console.log('   undone=' + JSON.stringify(rep.undone || []) + ' · probeSaves=' + rep.probeSaves);
  console.log('   cursorAfter=' + rep.cursorAfter + '（锚点 ' + anchor + '）· 音高 → ' + pitch());
  console.log('   提示：' + rep.note);
  console.log();
  stack('⑤ 之后');
  console.log('   最终音高：' + pitch() + '（应当回到 64）· dirty=' + cli(['project', 'dirty']).dirty);
})();
