// 真机：**写一次 → 用「撤回这一批」退回去 → 核对颜色回到原样**。
//
// 为什么要有这条：`history.undoBatch` 的价值全在"真能退回去"上，干跑与假 CLI 只能证明**计划**对。
// 这条走真 ACE，但**净效果为零**：挑的是最无害的一条写路径（`color.cycle` 只动轨道颜色一格，
// 不退也看得见、也好恢复），退完必须回到原色才算通过。
//
// 用法：node devtools/e2e-undo-live.js
const fs = require('fs');
const path = require('path');
const os = require('os');
const { execFileSync } = require('child_process');

const H = { 'Content-Type': 'application/json', 'X-ACETool': '1' };
const BASE = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json'), 'utf8')).url.replace(/\/$/, '');
const TRACK = process.argv[2] || '1';
let CLI = process.env.ACETOOL_CLI || 'C:\\Program Files\\ACE Studio\\acestudio-cli.exe';

let bad = 0;
const ok = (name, cond, extra) => {
  console.log((cond ? '  ok   ' : '  FAIL ') + name + (extra ? '  — ' + extra : ''));
  if (!cond) bad++;
};

async function run(toolId, values) {
  const r = await fetch(BASE + '/api/run', { method: 'POST', headers: H, body: JSON.stringify({ toolId, values }) });
  return r.json();
}
// ⚠️ 颜色**不在** `inspect.tracks` 的回包里 ⇒ 直接问 CLI（只读，不压撤销栈）。
//    cliPath 从任意一次 /api/run 的回包里拿（exe 知道它装在哪）。
async function colorOf(i) {
  if (!CLI) {
    const d = await run('inspect.tracks', {});
    CLI = d.cliPath || CLI;
  }
  const out = execFileSync(CLI, ['track', 'get', '--track-index', String(i), '--json'], { encoding: 'utf8' });
  return (JSON.parse(out).color || '');
}

(async () => {
  const before = await colorOf(TRACK);
  console.log('轨道 ' + TRACK + ' 现在的颜色：' + before + '\n');
  ok('先读到了原色', /^#[0-9a-fA-F]{6}$/.test(before || ''), before);

  // ① 真写一格颜色（这条写路径会压一条「更改轨道颜色」）
  const w = await run('color.cycle', { trackIndex: TRACK, direction: 'next' });
  const moved = await colorOf(TRACK);
  ok('写成功了（颜色换了一格）', w.ok && moved !== before, before + ' → ' + moved);
  ok('回包里带了撤回标记（exe 跑前跑后各读了一次撤销栈）',
    !!(w.undo && w.undo.created >= 1 && w.undo.anchor >= 0), JSON.stringify(w.undo));
  if (!w.undo) { console.log('\n没拿到撤回标记 ⇒ 后面没法测。'); process.exitCode = 1; return; }

  // ② 先干跑一次：只出计划，一个字节都不写
  const dry = await run('history.undoBatch', {
    anchor: String(w.undo.anchor), count: String(w.undo.created), actor: '远程控制', dryRun: '1',
  });
  const dj = (dry.calls && dry.calls[0] && dry.calls[0].json) || null;
  ok('干跑：只报计划（一个字节都没写，颜色还停在改过的那一格）',
    !!dj && dj.dryRun === true && (await colorOf(TRACK)) === moved, JSON.stringify(dj && dj.willUndo));

  // ② 名字守卫（真机版）：拿一份**故意不对**的名字名单 ⇒ 一条都不该退。
  //    ⚠️ 必须插在"真退"**之前**：那时栈顶上确实是我们的条目（锚点还在），
  //    这样测到的才是**名字守卫**，而不是"锚点过期"那条更早的分支。
  const wrong = await run('history.undoBatch', {
    anchor: String(w.undo.anchor), count: String(w.undo.created), names: '["编辑歌词"]',
    actor: '远程控制', dryRun: '0',
  });
  const wj = (wrong.calls && wrong.calls[0] && wrong.calls[0].json) || null;
  ok('名字对不上 ⇒ 一条都不退（不靠锚点硬撞）',
    !!wj && (wj.undone || []).length === 0 && /不在这一批的记录里/.test(wj.note || ''), (wj && wj.note) || '');
  ok('被名字挡下之后颜色**没变**（真的一个字节都没写）', (await colorOf(TRACK)) === moved, await colorOf(TRACK));

  // ③ 真退（**带上名字名单**，跟面板发的一模一样 —— 守卫不能把正当的一批挡住）
  const u = await run('history.undoBatch', {
    anchor: String(w.undo.anchor), count: String(w.undo.created),
    names: JSON.stringify(w.undo.names || []),
    actor: '远程控制', dryRun: '0',
  });
  const uj = (u.calls && u.calls[0] && u.calls[0].json) || null;
  ok('撤回：退掉了条目', !!uj && (uj.undone || []).length >= 1, JSON.stringify(uj && uj.undone));
  ok('撤回：没被挡住、也没中途停手', !!uj && !uj.blocked && !uj.stopped,
    (uj && (uj.blocked || uj.stopped)) || '');
  const after = await colorOf(TRACK);
  ok('★ 颜色回到原样（这才是真的"退回去了"）', after === before, moved + ' → ' + after + '（原色 ' + before + '）');
  ok('游标退回锚点（这一批退干净了）', !!uj && uj.cursorAfter <= w.undo.anchor,
    'cursorAfter=' + (uj && uj.cursorAfter) + ' anchor=' + w.undo.anchor);

  // ④ 同一份标记再点一次 ⇒ 必须**如实说没得退**（而不是把更早的东西也退掉）
  const again = await run('history.undoBatch', {
    anchor: String(w.undo.anchor), count: String(w.undo.created), actor: '远程控制', dryRun: '0',
  });
  const aj = (again.calls && again.calls[0] && again.calls[0].json) || null;
  ok('再点一次：一条都不退，并说明锚点已经过去了',
    !!aj && (aj.undone || []).length === 0 && /已经不在那一批上|没有可退的条目/.test(aj.note || ''),
    (aj && aj.note) || '');
  ok('再点一次之后颜色没变（没退别人的东西）', (await colorOf(TRACK)) === before, await colorOf(TRACK));

  console.log('\n' + (bad ? bad + ' 项失败' : '全部通过（净效果：颜色与跑之前一致）'));  process.exitCode = bad ? 1 : 0;
})().catch((e) => { console.error('炸了：' + e.message); process.exitCode = 2; });
