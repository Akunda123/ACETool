// 自检：**系统托盘图标**（用户 2026-10-17：「加个状态栏图标」）。
//
// 判据分三层（都必要，缺一层就会漏掉一种翻车）：
//   ① 盘上 6 档 PNG 都在（`internal/tray/icon/tray-<n>.png`）—— 光栅化脚本漏跑/改素材后忘了重跑；
//   ② `/api/tray` 说图标**真加进通知区域了**（`running` + `added`）—— 只看日志"调了 NIM_ADD"不算；
//   ③ 动作可用：`POST {action:"open"}` 要回 ok（**只测 open** —— quit 会把面板关掉，
//      而那正是我们跑自检时最不想要的事；quit 的收尾路径在真机上单独验过，见 HANDOVER §81）。
//
// 用法：node devtools/check-tray.js [url]
const fs = require('fs');
const os = require('os');
const path = require('path');

const PANEL = path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json');
const BASE = (process.argv[2] || JSON.parse(fs.readFileSync(PANEL, 'utf8')).url).replace(/\/$/, '');
const ICON_DIR = path.join(__dirname, '..', 'internal', 'tray', 'icon');
const SIZES = [16, 20, 24, 32, 40, 48];

let bad = 0;
const ok = (name, cond, extra) => {
  console.log((cond ? '  ok   ' : '  FAIL ') + name + (extra ? '  — ' + extra : ''));
  if (!cond) bad++;
};

(async () => {
  // ① 六档 PNG 都在，而且是真的 PNG
  const missing = [];
  const notPng = [];
  for (const n of SIZES) {
    const f = path.join(ICON_DIR, 'tray-' + n + '.png');
    if (!fs.existsSync(f)) { missing.push(n); continue; }
    const b = fs.readFileSync(f);
    if (b.length < 80 || b.slice(1, 4).toString('ascii') !== 'PNG') notPng.push(n);
  }
  ok('图标源都在（' + SIZES.join('/') + 'px 各一份 PNG）',
    missing.length === 0 && notPng.length === 0,
    missing.length ? '缺 ' + missing.join(',') + 'px' : (notPng.length ? '坏 PNG: ' + notPng.join(',') + 'px' : SIZES.length + ' 份'));

  // ② 现场的托盘
  const r = await fetch(BASE + '/api/tray', { cache: 'no-store' });
  const d = await r.json();
  ok('/api/tray 有回包', r.ok, 'http ' + r.status);
  ok('托盘在跑（不是 -no-tray / 起失败）', d.running === true, d.note || '');
  ok('图标**真的**进了通知区域（Shell_NotifyIcon 收下了）', d.added === true, 'added=' + d.added);
  ok('图标尺寸是嵌入的那几档之一', SIZES.indexOf(d.iconSize) >= 0, 'iconSize=' + d.iconSize);
  ok('按 DPI 选了档（150% ⇒ 24px）', !d.dpi || d.iconSize === 24 || d.dpi !== 144,
    'dpi=' + d.dpi + ' → ' + d.iconSize + 'px');
  ok('悬停提示带上了端口', /\d+\.\d+\.\d+\.\d+:\d+/.test(d.tip || ''), d.tip || '(空)');

  // ③ 动作：只测 open（quit 见注释）
  const post = async (body) => {
    const rr = await fetch(BASE + '/api/tray', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-ACETool': '1' },
      body: JSON.stringify(body),
    });
    return await rr.json();
  };
  const o = await post({ action: 'open' });
  ok('动作 open 可用（回 ok）', o.ok === true, o.note || o.error || '');
  const noHdr = await fetch(BASE + '/api/tray', { method: 'POST', body: JSON.stringify({ action: 'open' }) });
  ok('没有 X-ACETool 头的 POST 被拒', noHdr.status === 403, 'http ' + noHdr.status);
  const badAct = await post({ action: 'nope' });
  ok('不认识的 action 被拒', /只认/.test(badAct.error || ''), badAct.error || '');
  const q = await post({ action: 'quit' });
  ok('quit 不带 confirm 被拒（免得一次笔误把面板关了）', /confirm/.test(q.error || ''), q.error || '');

  console.log('\n' + (bad ? bad + ' 项失败' : '全部通过'));
  process.exitCode = bad ? 1 : 0;
})().catch((e) => {
  console.error('自检脚本自己炸了：' + e.stack);
  process.exitCode = 2;
});
