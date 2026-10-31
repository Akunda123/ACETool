// 世代门（`/api/gate` + `_gen`）真机端到端核对。
//
// 要证明的三件事（用户在 2026-10-05 提的做法：**请求带上参数，收到时若已过期就不执行**）：
//   ① 过期世代号 ⇒ 回 `hint: 已叫停，未执行`，且**不 spawn CLI**（看 calls 里的标签）
//   ② 当前世代号 ⇒ 正常进工具逻辑（这里 ACE 桥不在线，会走到 CLI 并如实报错——关键是要**不**命中门）
//   ③ `_gen` **不会留在 lastRun 里**（否则热键 `run.primary` 重放时会带着旧世代号被静默跳过）
//
// 用法：node devtools/e2e-gate.js [url]
//   url 省略时从 %APPDATA%\ACETool\panel.json 读（面板端口每次重启都变）。

const fs = require('fs');
const path = require('path');
const os = require('os');

const H = { 'Content-Type': 'application/json', 'X-ACETool': '1' };

function baseUrl() {
  if (process.argv[2]) return process.argv[2].replace(/\/$/, '');
  const p = path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json');
  return JSON.parse(fs.readFileSync(p, 'utf8')).url.replace(/\/$/, '');
}

let pass = 0, fail = 0;
function check(ok, name, extra) {
  if (ok) { pass++; console.log('  ok   ' + name + (extra ? '  — ' + extra : '')); }
  else { fail++; console.log('  FAIL ' + name + (extra ? '  — ' + extra : '')); }
}

async function run(values) {
  const r = await fetch(BASE + '/api/run', {
    method: 'POST', headers: H,
    body: JSON.stringify({ toolId: 'track.color', values }),
  });
  return r.json();
}

let BASE = '';

(async () => {
  BASE = baseUrl();
  console.log('面板：' + BASE);

  const ping = await (await fetch(BASE + '/api/ping')).json();
  check(ping.app === 'acetool', '面板在线（/api/ping）', 'pid=' + ping.pid);

  // 世代读回
  const g0 = (await (await fetch(BASE + '/api/gate')).json()).gen;
  check(typeof g0 === 'number', 'GET /api/gate 给出世代号', 'gen=' + g0);

  // 推进世代（模拟用户点了「停止」）
  const g1 = g0 + 1;
  const set = await (await fetch(BASE + '/api/gate', {
    method: 'POST', headers: H, body: JSON.stringify({ gen: g1 }),
  })).json();
  check(set.ok === true && set.gen === g1, 'POST /api/gate 推进世代', g0 + ' → ' + g1);
  const g0b = (await (await fetch(BASE + '/api/gate')).json()).gen;
  check(g0b === g1, '世代号已持久到内存（再读一致）', 'gen=' + g0b);

  // ① 过期请求
  const stale = await run({ color: '#EC4F44', trackIndex: '0', _gen: String(g0) });
  check(stale.hint === '已叫停，未执行', '① 过期世代被跳过', 'hint=' + JSON.stringify(stale.hint));
  check(/世代替换/.test((stale.calls && stale.calls[0] && stale.calls[0].label) || ''),
    '① 回包里说明是世代替换', (stale.calls && stale.calls[0] && stale.calls[0].label) || '(无)');
  check(stale.calls.length === 1, '① 没有为它 spawn 任何 CLI', stale.calls.length + ' 条 call');

  // ② 当前世代
  const fresh = await run({ color: '#EC4F44', trackIndex: '0', _gen: String(g1) });
  check(fresh.hint !== '已叫停，未执行', '② 当前世代正常进工具逻辑', 'hint=' + JSON.stringify(fresh.hint || ''));

  // ③ lastRun 里不许有 _gen
  const ev = await (await fetch(BASE + '/api/events?since=999999')).json();
  const vals = (ev.lastRun && ev.lastRun.values) || {};
  check(!('_gen' in vals), '③ lastRun 里没有 _gen（热键重放安全）',
    'keys=' + JSON.stringify(Object.keys(vals)));

  // 复位：把世代拨回，别让面板停在"叫停"状态
  await fetch(BASE + '/api/gate', { method: 'POST', headers: H, body: JSON.stringify({ gen: g1 + 1 }) });

  console.log('\n' + (fail ? fail + ' 项失败 / ' : '') + pass + ' 项通过');
  process.exitCode = fail ? 1 : 0;
})().catch(e => { console.error('崩了：' + e.stack); process.exitCode = 1; });
