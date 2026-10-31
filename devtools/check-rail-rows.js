// 验"鼠标指到某一行 ⇒ 细条认为你指的是第几项"这条映射（19 行逐个对）。
//
// 为什么要单独验它：用户 2026-10-17 报「所有按钮都是打开 webmain」——
// `itemAt()` 是"点击落到哪一项"的**唯一**判据（悬停提示、点击、命中行都用它），
// 它一旦跟**画出来的行**对不上，用户指哪都白搭（点 A 得到 B）。
//
// 判据：往第 i 行的中线注入假光标，读回 `hover` 与 `tipText`，跟 `/api/rail/items` 的第 i 项对。
//
// 用法：node devtools/check-rail-rows.js [url]
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

const rail = async () => (await fetch(BASE + '/api/rail', { cache: 'no-store' })).json();
const cursor = async (body) =>
  (await fetch(BASE + '/api/rail/cursor', { method: 'POST', headers: H, body: JSON.stringify(body) })).json();

(async () => {
  const st0 = await rail();
  if (!st0.running || !st0.visible) {
    console.log('细条没在跑 / 没显示 —— 先让它出来：' + JSON.stringify(st0).slice(0, 200));
    console.log('  多数是 ACE 没开或被最小化（那时细条会正确地藏起来）：go run ./devtools/acefocus');
    process.exit(1);
  }
  const items = (await (await fetch(BASE + '/api/rail/items', { cache: 'no-store' })).json()).items || [];
  const scale = (st0.dpi || 96) / 96;
  const pad = Math.round(6 * scale);
  const itemH = st0.itemH;
  const rect = st0.rect;
  console.log(`面板：${BASE}`);
  console.log(`细条 0x${(st0.hwnd || 0).toString(16)} dpi=${st0.dpi} itemH=${itemH} 高=${rect.Bottom - rect.Top} 清单 ${items.length} 项`);
  console.log(`按公式算：pad=${pad}，19 行需要 ${pad * 2 + itemH * items.length}px\n`);

  let wrong = 0;
  for (let i = 0; i < items.length; i++) {
    const y = rect.Top + pad + i * itemH + Math.floor(itemH / 2);
    await cursor({ x: rect.Right - Math.round(15 * scale), y });
    await sleep(180);
    const s = await rail();
    const want = items[i].Full;
    const got = s.tipText || '';
    // ⚠️ 带参数的项，全名后面会跟一段**后缀**（见 Go 的 `itemTip`）：
    //    「连接音符 · 不设限（右键改）」/「批量替换音素（左键填参数）」——
    //    所以判据是"**以全名开头**"，不是逐字相等（2026-10-17 加参数条时踩的）。
    const hit = s.hover === i && got.startsWith(want);
    if (!hit) {
      wrong++;
      console.log(`  FAIL 第 ${i} 行（窗口内 y=${y - rect.Top}）：hover=${s.hover} tip=「${got}」，期望以 ${i}「${want}」开头`);
    }
  }
  await cursor({ clear: true });
  ok(`19 行逐行对得上（注入第 i 行 ⇒ hover=i 且提示文案以第 i 项全名开头）`, wrong === 0, wrong + ' 行对不上');
  console.log('\n' + (bad ? bad + ' 项失败' : '全部通过'));
  process.exitCode = bad ? 1 : 0;
})().catch((e) => { console.error('自检脚本自己炸了：' + e.stack); process.exitCode = 2; });
