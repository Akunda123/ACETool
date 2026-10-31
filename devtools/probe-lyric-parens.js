// 「忽略连音会不会改变括号状态」—— 同一份数据，两种开关各读一次，逐行比。
const fs = require('fs');
const path = require('path');
const os = require('os');
const H = { 'Content-Type': 'application/json', 'X-ACETool': '1' };
const B = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json'), 'utf8')).url.replace(/\/$/, '');

(async () => {
  const out = {};
  for (const v of [{ split: 'auto', ig: '1' }, { split: 'auto', ig: '0' }, { split: 'on', ig: '1' }, { split: 'on', ig: '0' }]) {
    const d = await (await fetch(B + '/api/run', {
      method: 'POST', headers: H,
      body: JSON.stringify({ toolId: 'lyric.pages', values: { perPage: '0', splitChars: v.split, ignoreTenuto: v.ig } }),
    })).json();
    const j = d.calls && d.calls[0] && d.calls[0].json;
    if (!j) { console.log('读失败：' + (d.hint || '')); continue; }
    out[v.split + '/' + v.ig] = j;
    console.log('splitChars=' + v.split + ' ignoreTenuto=' + v.ig
      + ' → 模式 ' + j.mode + (j.splitCharsAuto ? '(自动)' : '') + ' · 写回走 ' + j.writeMode + ' · ' + j.rows.length + ' 行');
  }
  const a = out['auto/1'], b = out['auto/0'];
  if (a && b) {
    console.log('\n=== 忽略连音 开 vs 关（同一份数据）===')
    let diff = 0;
    for (let i = 0; i < Math.min(a.rows.length, b.rows.length); i++) {
      if (a.rows[i].text !== b.rows[i].text) {
        diff++;
        if (diff <= 12) {
          console.log('  行 ' + a.rows[i].index + '：开=' + JSON.stringify(a.rows[i].text) + '  关=' + JSON.stringify(b.rows[i].text));
        }
      }
    }
    console.log('  共 ' + diff + ' / ' + a.rows.length + ' 行文字不同');
    const parens = (j) => j.rows.filter((r) => /[()]/.test(r.text)).length;
    console.log('  含括号的行：开 ' + parens(a) + ' 行 · 关 ' + parens(b) + ' 行');
  }
  const c = out['on/1'], d2 = out['on/0'];
  if (c && d2) {
    const parens = (j) => j.rows.filter((r) => /[()]/.test(r.text)).length;
    console.log('\n=== 强制按字符 ===');
    console.log('  含括号的行：忽略连音开 ' + parens(c) + ' 行 · 关 ' + parens(d2) + ' 行');
    console.log('  样例：开=' + JSON.stringify(c.rows[0].text) + '  关=' + JSON.stringify(d2.rows[0].text));
  }
  // ⭐ 用户 2026-10-06：「**不按字符隔开时不显示括号，空格就是分隔符**」
  console.log('\n=== 不按字符隔开（splitChars=off）===')
  for (const ig of ['1', '0']) {
    const d = await (await fetch(B + '/api/run', {
      method: 'POST', headers: H,
      body: JSON.stringify({ toolId: 'lyric.pages', values: { perPage: '0', splitChars: 'off', ignoreTenuto: ig } }),
    })).json();
    const j = d.calls && d.calls[0] && d.calls[0].json;
    if (!j) { console.log('  读失败：' + (d.hint || '')); continue; }
    const pn = j.rows.filter((r) => /[()]/.test(r.text)).length;
    console.log('  忽略连音=' + ig + ' · 模式 ' + j.mode + ' · 含括号的行 ' + pn + ' 行'
      + ' · 样例 ' + JSON.stringify(j.rows[0].text) + ' / ' + JSON.stringify(j.rows[4] ? j.rows[4].text : ''));
  }
})().catch((e) => { console.error('崩了：' + e.stack); process.exitCode = 1; });
