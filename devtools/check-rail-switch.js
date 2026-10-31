// 「web ⇄ 原生侧栏」切换按钮的自检（用户 2026-10-16：「做一个 web 和侧栏的切换按钮就行了」）。
//
// 它验四件事：
//   ① `GET /api/rail` 能报状态（按钮就是读它决定显示"侧"还是"web"）；
//   ② `POST {enable:true}` 真能把原生细条**运行时**起起来；
//   ③ 起来之后 **exe 还活着**（⚠️ 这正是 HANDOVER §72 那条未解决问题要盯的：建了细条它会自己退出）；
//   ④ `POST {enable:false}` 能停掉，且 exe 继续活着。
//
// ⚠️ 跑之前最好让 **ACE 开着**：实测建细条 + ACE 不可用时 exe 会退出（那是 §72 的问题，
//    不是这个自检的问题）。ACE 没开时脚本会**如实标出来**，别把结果当成按钮坏了。
//
// 用法：node devtools/check-rail-switch.js [url]
const fs = require('fs');
const os = require('os');
const path = require('path');

const PANEL = path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json');
const BASE = (process.argv[2] || JSON.parse(fs.readFileSync(PANEL, 'utf8')).url).replace(/\/$/, '');
const H = { 'Content-Type': 'application/json', 'X-ACETool': '1' };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

let bad = 0;
const ok = (name, cond, extra) => {
  console.log((cond ? '  ok   ' : '  FAIL ') + name + (extra ? '  — ' + extra : ''));
  if (!cond) bad++;
};

const getRail = async () => (await fetch(BASE + '/api/rail', { cache: 'no-store' })).json();
const setRail = async (enable) => {
  const r = await fetch(BASE + '/api/rail', { method: 'POST', headers: H, body: JSON.stringify({ enable }) });
  return await r.json();
};
const alive = async () => {
  try {
    const r = await fetch(BASE + '/api/ping', { cache: 'no-store' });
    return r.ok;
  } catch (e) {
    return false;
  }
};

(async () => {
  console.log('面板：' + BASE);
  const before = await getRail();
  ok('① 能读侧条状态（按钮靠它显示"侧"/"web"）', typeof before.running === 'boolean', JSON.stringify(before).slice(0, 120));

  if (before.running) {
    console.log('  （侧条已经在跑 ⇒ 先停掉，从干净状态开始）');
    await setRail(false);
    await sleep(400);
  }

  const on = await setRail(true);
  ok('② POST {enable:true} 报成功', on.ok === true, JSON.stringify(on).slice(0, 160));
  if (on.warn) console.log('  它自己提示的风险：' + on.warn);
  await sleep(2500); // 给它一点时间（§72 那条问题就是在这个窗口里露出来的）

  ok('③ 起完之后 **exe 还活着**（§72 那条未解决问题没被踩到）', await alive());
  if (!(await alive())) {
    console.log('  ⛔ exe 已经没了 ⇒ 这就是 §72 那条问题（建了细条它会自己退出），不是切换按钮的错。');
    console.log('     把面板重新起一下：powershell -File devtools/start-panel.ps1 -NoBrowser');
    console.log('\n' + (bad + 1) + ' 项失败（其中最后一项是已知问题）');
    process.exitCode = 1;
    return;
  }
  const st = await getRail();
  ok('③ 状态里 running=true 且贴到了 ACE 窗口', st.running === true && !!st.targetTitle,
    'target=' + (st.targetTitle || '(没贴到)') + ' level=' + st.level + ' 宽=' + ((st.rect || {}).Right - (st.rect || {}).Left) + 'px');

  const off = await setRail(false);
  await sleep(600);
  ok('④ POST {enable:false} 停掉，且 exe 继续活着', off.ok === true && (await alive()));
  const after = await getRail();
  ok('④ 停掉之后状态 running=false', after.running === false, JSON.stringify(after).slice(0, 120));

  // 收尾：**把原来的状态放回去**（原来开着就再开起来）——不然跑一次自检就把用户的侧栏关了。
  if (before.running) {
    const back = await setRail(true);
    console.log('  （原本是开着的 ⇒ 收尾再开起来：' + (back.ok ? 'ok' : '失败 ' + JSON.stringify(back)) + '）');
  }

  console.log('\n' + (bad ? bad + ' 项失败' : '全部通过'));
  process.exitCode = bad ? 1 : 0;
})().catch((e) => { console.error('自检脚本自己炸了：' + e.stack); process.exitCode = 2; });
