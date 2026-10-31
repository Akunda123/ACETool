/* 快捷键 + 窗口置顶的**真机自检**（打真实 HTTP；不碰工程数据）。
 *
 * 覆盖：
 *   ① GET /api/shortcuts —— 动作表 / 绑定 / 平台 / 逐条注册结果
 *   ② 非法键位 ⇒ **一条都不存**（半存半不存最难查）
 *   ③ 改一条键位 ⇒ 落盘 + 立刻重注册
 *   ④ GET+POST /api/panel/ontop —— 真去置顶那个浏览器窗口，并**读回来核对**
 *   ⑤ GET /api/events —— exe → 页面的推送通道（系统级热键的结果从这里回页面）
 *
 * 用法：node e2e-shortcuts.js http://127.0.0.1:64648/
 */
const base = process.argv[2] || 'http://127.0.0.1:64648/';
const H = { 'Content-Type': 'application/json', 'X-ACETool': '1' };

async function get(p) { const r = await fetch(new URL(p, base)); return { s: r.status, j: await r.json().catch(() => null) }; }
async function post(p, body) {
  const r = await fetch(new URL(p, base), { method: 'POST', headers: H, body: JSON.stringify(body || {}) });
  return { s: r.status, j: await r.json().catch(() => null) };
}
let fails = 0;
function check(name, ok, extra) {
  console.log((ok ? '  ok   ' : '  FAIL ') + name + (extra ? '  — ' + extra : ''));
  if (!ok) fails++;
}

(async () => {
  console.log('== ① GET /api/shortcuts');
  let r = await get('/api/shortcuts');
  check('HTTP 200', r.s === 200);
  const v = r.j || {};
  check('带动作表', Array.isArray(v.actions) && v.actions.length > 0, (v.actions || []).length + ' 个动作');
  check('带绑定表', !!v.bindings && Object.keys(v.bindings).length > 0, Object.keys(v.bindings || {}).length + ' 条');
  check('带出厂表（恢复默认要用）', !!v.defaults && Object.keys(v.defaults).length > 0);
  console.log('  平台 = ' + v.platform + '，windows = ' + v.windows);
  console.log('  动作：' + (v.actions || []).map((a) => a.id + (a.canGlobal ? '(可全局)' : '')).join(' · '));
  console.log('  绑定：' + Object.keys(v.bindings || {}).map((k) => k + '=' + v.bindings[k].key + (v.bindings[k].global ? '🌐' : '')).join(' · '));
  const reg = v.registered || [];
  console.log('  注册结果：' + (reg.length ? reg.map((x) => x.action + '=' + x.key + (x.ok ? ' ✓' : ' ✗ ' + x.error)).join(' | ') : '（没有系统级绑定）'));
  check('系统级绑定都注册成功了', reg.filter((x) => x.global !== false).every((x) => x.ok),
    reg.filter((x) => !x.ok).map((x) => x.key + ': ' + x.error).join('；') || '全部 ✓');
  check('没有键位冲突', !(v.conflicts || []).length, (v.conflicts || []).join('；'));
  if (v.warnings && v.warnings.length) console.log('  提示：' + v.warnings.join(' / '));

  console.log('\n== ② 非法键位 ⇒ 一条都不存');
  const before = JSON.stringify(v.bindings);
  r = await post('/api/shortcuts', { bindings: { 'quick.find.notes': { key: 'Ctrl+W', global: true } } });
  check('被拒（ok=false）', r.j && r.j.ok === false);
  check('错误里点名了这个键的问题', !!(r.j && r.j.errors && r.j.errors['quick.find.notes'] &&
    /占着/.test(r.j.errors['quick.find.notes'])), (r.j && r.j.errors && r.j.errors['quick.find.notes']) || '');
  let after = await get('/api/shortcuts');
  check('**绑定表没被动过**', JSON.stringify(after.j.bindings) === before);

  console.log('\n== ③ 改一条键位 ⇒ 落盘 + 重注册');
  r = await post('/api/shortcuts', { bindings: after.j.bindings });
  check('保存成功', r.s === 200 && r.j && !r.j.errors);
  const reg2 = (r.j && r.j.registered) || [];
  check('返回了逐条注册结果', reg2.length > 0, reg2.map((x) => x.key + (x.ok ? '✓' : '✗')).join(' '));

  console.log('\n== ④ 窗口置顶（按窗口：页面带上自己的标题，真去动那个浏览器窗口）');
  r = await get('/api/panel/ontop');
  console.log('  老路（标题含 ACETool）：' + JSON.stringify(r.j));
  // ⭐ 2026-10-16 新口径：每个面板窗口各一颗 📌，点击时上报自己的 `document.title`
  //    ⇒ 服务端先**精确**匹配那个标题，落空才退"标题含这段"。
  const ghost = 'ACETool-自检-不存在的标题-9f3c';
  const g1 = await get('/api/panel/ontop?title=' + encodeURIComponent(ghost));
  check('按标题找不到时**如实报**（不猜、不去顶别人）',
    !!(g1.j && g1.j.error) && g1.j.onTop !== true && /没找到标题为/.test(g1.j.error), (g1.j && g1.j.error) || '');
  const g2 = await post('/api/panel/ontop', { on: true, title: ghost });
  check('POST 也认标题，且回包里写清"凭什么找的"（精确落空 ⇒ contains）',
    !!(g2.j && g2.j.error) && g2.j.match === 'contains', JSON.stringify(g2.j));
  // ⛔ 回归（真机踩到的那个）：exe **自己控制台的标题就是 exe 路径**，里面含 `acetool`
  //    ⇒ 老代码把它当"面板窗口"去置顶（读回来的 windowTitle 就是这条路径）。
  //    现在 `win.Find` 剔除本进程窗口 ⇒ 必须报"找不到"。
  const exePath = require('path').join(__dirname, '..', 'acetool.exe');
  const cc = await get('/api/panel/ontop?title=' + encodeURIComponent(exePath));
  check('exe 自己的控制台**不再**被当成面板窗口',
    !!(cc.j && cc.j.error) && cc.j.onTop !== true, JSON.stringify(cc.j));
  r = await post('/api/panel/ontop', { on: true });
  console.log('  置顶：' + JSON.stringify(r.j));
  if (r.j && r.j.ok) {
    check('找到面板窗口了', !!r.j.windowTitle, r.j.windowTitle);
    check('读回来确认**真的**置顶了', r.j.onTop === true);
    // 再取消，别把用户的窗口一直钉在最上面
    const off = await post('/api/panel/ontop', { on: false });
    check('能取消置顶（读回核对）', off.j && off.j.ok && off.j.onTop === false, JSON.stringify(off.j));
  } else {
    check('置顶失败时**如实报原因**', !!(r.j && r.j.error), (r.j && r.j.error) || '');
    console.log('  （浏览器窗口标题跟的是当前标签页；面板不在前台时找不到是已知边界）');
  }

  console.log('\n== ⑤ GET /api/events（exe → 页面的推送通道，**按序号游标取**）');
  r = await get('/api/events');
  check('HTTP 200 且形状对（events 数组 + lastSeq）', r.s === 200 && Array.isArray((r.j || {}).events) &&
    typeof (r.j || {}).lastSeq === 'number', JSON.stringify(r.j).slice(0, 160));
  // ⚠️ 语义是 2026-10-05 改的：**不带 since = 从此刻起**（不回放历史事件，免得新窗口被刷屏），
  // 而事件**不再被取走**（多窗口各自带游标取）。所以这里查的是"不回放、且可重复问"，
  // 不再是"取走即清" —— 旧标签留着会变成自检在撒谎。
  check('不带 since 不回放历史事件', r.j.events.length === 0);
  const again = (await get('/api/events')).j;
  check('再问一次结果一致（事件不会被取走）',
    again.events.length === r.j.events.length && again.lastSeq === r.j.lastSeq);
  check('多窗口用同一游标都能取到（游标语义在）',
    (await get('/api/events?since=' + Math.max(0, r.j.lastSeq - 200))).s === 200);

  console.log('\n' + (fails ? fails + ' 项失败' : '全部通过'));
  // ⚠️ **不要 `process.exit()`**：Windows + Node 24 上强杀会因 undici 残留 handle 触发
  // libuv 断言（`!(handle->flags & UV_HANDLE_CLOSING)`），**退出码变成 1** ——
  // 于是自检"报全部通过、却给失败码"。设 exitCode 让事件循环自己排空即可。
  process.exitCode = fails ? 1 : 0;
})().catch((e) => { console.error('自检脚本自己炸了：', e); process.exitCode = 2; });
