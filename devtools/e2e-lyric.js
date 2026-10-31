// 真机核对「填词」读出来的东西 —— 特别是**显示是不是 ACE 自己合并的那句**。
//
// 判据（`lyric.pages` 每行都带）：
//   · `roundTrip:true`  ⇒ 我拼出来的显示 == ACE 的 `sentence.lyric`
//     ⇒ 而这句又是 `lyric fill` 的逆运算（真机 dry-run 验过）⇒ 显示 → 回填无损
//   · `noteLyrics` 里必须能看见**原始形态**：`-`、`hello#1`、`hello#2`（这是"能读"）
//
// 用法：node devtools/e2e-lyric.js [splitChars=auto|on|off] [ignoreTenuto=0|1]
const fs = require('fs');
const path = require('path');
const os = require('os');

const H = { 'Content-Type': 'application/json', 'X-ACETool': '1' };
const BASE = (process.argv[2] && process.argv[2].startsWith('http'))
  ? process.argv[2].replace(/\/$/, '')
  : JSON.parse(fs.readFileSync(path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json'), 'utf8')).url.replace(/\/$/, '');
const SPLIT = process.argv[3] || 'auto';
const IGNORE = process.argv[4] === undefined ? '1' : process.argv[4];

(async () => {
  const r = await (await fetch(BASE + '/api/run', {
    method: 'POST', headers: H,
    body: JSON.stringify({ toolId: 'lyric.pages', values: { perPage: '0', splitChars: SPLIT, ignoreTenuto: IGNORE } }),
  })).json();
  const j = (r.calls && r.calls[0] && r.calls[0].json);
  if (!r.ok || !j) { console.error('读失败：' + (r.hint || JSON.stringify(r).slice(0, 300))); process.exit(1); }

  console.log('片段：' + j.clipName + ' · 句数 ' + j.sentenceCount + ' · 字数 ' + j.charCount
    + ' · 模式 ' + j.mode + (j.splitCharsAuto ? '（按语种自动）' : '（显式）')
    + ' · 忽略连音 ' + j.ignoreTenuto + ' · 写回走 ' + j.writeMode + ' · 提前 ' + j.leadTicks + ' tick');
  if (j.note) console.log('提示：' + j.note);

  const bad = j.rows.filter((x) => !x.roundTrip);
  console.log('\n行数 ' + j.rows.length + ' · roundTrip 失败 ' + bad.length + ' 行');
  for (const x of bad.slice(0, 10)) {
    console.log('  ✗ 第 ' + x.index + ' 句：显示 ' + JSON.stringify(x.text)
      + ' ≠ ACE ' + JSON.stringify(x.aceLyric) + '   逐音符 ' + JSON.stringify(x.noteLyrics));
  }

  // 「能读」的证据：原始形态必须原封不动地在 noteLyrics 里
  const raw = j.rows.filter((x) => x.noteLyrics.some((l) => l === '-' || /#\d+$/.test(l)));
  console.log('\n带 `-` 或 `#n` 的行 ' + raw.length + ' 行（原始形态能读到的证据）：');
  for (const x of raw.slice(0, 8)) {
    console.log('  句 ' + String(x.index).padStart(3) + '  逐音符 ' + JSON.stringify(x.noteLyrics)
      + '  →  显示 ' + JSON.stringify(x.text));
  }

  console.log('\n前 3 行与末尾 1 行：');
  for (const x of j.rows.slice(0, 3).concat(j.rows.slice(-1))) {
    console.log('  句 ' + String(x.index).padStart(3) + ' tick ' + x.absBeginTick + '-' + x.absEndTick
      + '  音符 ' + x.noteCount + '  ' + JSON.stringify(x.text));
  }
  process.exitCode = bad.length ? 1 : 0;
})().catch((e) => { console.error('崩了：' + e.stack); process.exitCode = 1; });
