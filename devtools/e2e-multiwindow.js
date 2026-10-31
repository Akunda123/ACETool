/* 多窗口验证：系统级热键的事件，**两个窗口都要收得到**（用户 2026-10-05 选的做法 ①）。
 *
 * 背景：原来是"取走即清"的队列 ⇒ 谁先轮询谁抢走，第二个窗口永远看不到。
 * 现在按**序号游标**增量取、事件留在环形缓冲里 ⇒ 用**同一个 `since`** 问两次，两边都该拿到同一条。
 *
 * 用法：node e2e-multiwindow.js <url> record    # 先记录一次运行（热键的输入）
 *       （中间由外部发一次真实按键，见 PowerShell 那步）
 *       node e2e-multiwindow.js <url> check     # 两个"窗口"各问一次
 */
const base = process.argv[2];
const mode = process.argv[3] || 'record';
const H = { 'Content-Type': 'application/json', 'X-ACETool': '1' };
let fails = 0;
const check = (name, ok, extra) => {
  console.log((ok ? '  ok   ' : '  FAIL ') + name + (extra ? '  — ' + extra : ''));
  if (!ok) fails++;
};

(async () => {
  if (mode === 'record') {
    const r = await fetch(new URL('/api/run', base), {
      method: 'POST', headers: H,
      body: JSON.stringify({
        toolId: 'jianpu.fromClip',
        values: { elements: '[{"kind":"note","pitch":1,"duration":4}]', format: 'terse', key: '0', meter: '4/4' },
      }),
    });
    const j = await r.json();
    console.log('预备运行：ok=' + j.ok + '，工具 =「' + j.title + '」');
    // 第一次**不带 since** ⇒ 服务端只回 lastSeq、不回事件（新窗口不该被历史事件刷屏）
    const d = await (await fetch(new URL('/api/events', base))).json();
    console.log('首次（不带 since）：events=' + d.events.length + '，lastSeq=' + d.lastSeq +
      '，firedCount=' + d.firedCount);
    check('不带 since 时不回放历史事件', d.events.length === 0);
    check('给出了 lastSeq（客户端据此起游标）', typeof d.lastSeq === 'number');
    console.log('\n游标 = ' + d.lastSeq + '（下面两个"窗口"都用它）');
    require('fs').writeFileSync(require('path').join(__dirname, 'mm-cursor.txt'), String(d.lastSeq));
    return;
  }

  const since = require('fs').readFileSync(require('path').join(__dirname, 'mm-cursor.txt'), 'utf8').trim();
  console.log('两个窗口都用 since=' + since + ' 去问：\n');
  const w1 = await (await fetch(new URL('/api/events?since=' + since, base))).json();
  const w2 = await (await fetch(new URL('/api/events?since=' + since, base))).json();
  const show = (n, d) => {
    console.log('窗口' + n + '：events=' + d.events.length + '，lastSeq=' + d.lastSeq + '，missed=' + (d.missed || 0));
    (d.events || []).forEach((e) => console.log('    ' + e.seq + ' [' + e.kind + '] ' + (e.action || '') + ' → ' + e.text));
  };
  show(1, w1);
  show(2, w2);
  check('窗口 1 收到了事件', w1.events.length > 0, w1.events.length + ' 条');
  check('窗口 2 **也**收到了同一条（不是谁先问谁抢走）',
    w2.events.length === w1.events.length && w2.events.length > 0,
    'w1=' + w1.events.length + ' w2=' + w2.events.length);
  check('两次拿到的是**同样的序号**',
    JSON.stringify((w1.events || []).map((e) => e.seq)) === JSON.stringify((w2.events || []).map((e) => e.seq)));
  check('计数仍只增不清（证据通道还在）', (w1.firedCount || 0) > 0, 'firedCount=' + w1.firedCount);
  console.log('\n' + (fails ? fails + ' 项失败' : '全部通过'));
  process.exitCode = fails ? 1 : 0;
})().catch((e) => { console.error('炸了：', e); process.exitCode = 2; });
