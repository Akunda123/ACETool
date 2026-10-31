// ⭐⭐ 「回填」的**真机端到端**证明 —— 用户 2026-10-05 的硬要求：
// **`#1#2` 和 `-` 都要能正常读、隐藏、回填。**
//
// 做法：把面板**当前显示的那串文本原样写回去**（内容不变，所以工程不会有可见改动），
// 然后逐音符读回来对比。要证明的就是：
//
//	忽略连音 开 ⇒ 走 `lyric fill` ⇒ `-` 那个音符 **还是 `-`**、`hello#1`/`hello#2` **一个不差**
//	忽略连音 关 ⇒ 走逐音符 `note set-lyric` ⇒ 原样写回原样读出
//
// 写法用面板同款（`mode=auto` + `ignoreTenuto` + 已生效的 `splitChars`），**不带 dryRun** ⇒ 真写。
// ⚠️ 真写会往 ACE 撤销栈里压记录（Ctrl+Z 可撤）；文本没变，所以工程内容不变。
//
// 用法：node devtools/e2e-lyric-write.js [句子下标,逗号分隔] [off = 关掉忽略连音，走逐音符那条路]
const { execFileSync } = require('child_process');
const fs = require('fs');
const path = require('path');
const os = require('os');

const CLI = 'C:\\Program Files\\ACE Studio\\acestudio-cli.exe';
const H = { 'Content-Type': 'application/json', 'X-ACETool': '1' };
const IGNORE = process.argv[3] === 'off' ? '0' : '1';
const BASE = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json'), 'utf8')).url.replace(/\/$/, '');

function cli(args) {
  const out = execFileSync(CLI, args.concat(['--json']), { maxBuffer: 128 * 1024 * 1024 }).toString('utf8');
  return JSON.parse(out.charCodeAt(0) === 0xfeff ? out.slice(1) : out);
}

(async () => {
  // 当前打开的片段（和面板同一个口径）
  const es = cli(['editor', 'status']);
  const track = es.trackIndex, clip = es.clipIndex;
  console.log('片段：轨道 ' + track + ' 片段 ' + clip + '（' + es.clipName + '）· 忽略连音 ' + IGNORE);

  // ① 面板读出来的东西（显示 + 逐音符）
  const pages = (await (await fetch(BASE + '/api/run', {
    method: 'POST', headers: H,
    body: JSON.stringify({ toolId: 'lyric.pages', values: { perPage: '0', splitChars: 'auto', ignoreTenuto: IGNORE } }),
  })).json()).calls[0].json;
  const resolved = pages.splitChars ? 'on' : 'off';
  console.log('读：' + pages.sentenceCount + ' 句 · 模式 ' + pages.mode + ' · 写回走 ' + pages.writeMode);

  // 默认挑「带 `-` 或 `#n`」的句子
  const picks = process.argv[2] ? process.argv[2].split(',').map(Number)
    : pages.rows.filter((r) => r.noteLyrics.some((l) => l === '-' || /#\d+$/.test(l))).map((r) => r.index);
  if (!picks.length) { console.error('这一份里没有带 - / #n 的句子可验'); process.exit(1); }

  const readNotes = () => {
    const m = new Map();
    for (const n of cli(['clip', 'note-content', '--track-index', String(track), '--clip-index', String(clip)]).notes) {
      m.set(n.noteUuid, n.lyric);
    }
    return m;
  };
  const before = readNotes();

  let bad = 0;
  for (const idx of picks) {
    const row = pages.rows.find((r) => r.index === idx);
    if (!row) { console.log('  跳过第 ' + idx + ' 句（读不到）'); continue; }
    // ② 真写：文本就是**显示的那串**（内容不变）
    const w = await (await fetch(BASE + '/api/run', {
      method: 'POST', headers: H,
      body: JSON.stringify({
        toolId: 'lyric.setSentence',
        values: { sentence: String(idx), text: row.text, mode: 'auto', splitChars: resolved, ignoreTenuto: IGNORE },
      }),
    })).json();
    if (!w.ok) { console.log('  ✗ 第 ' + idx + ' 句写回失败：' + w.hint); bad++; continue; }

    // ③ 读回来对比
    const after = readNotes();
    const got = row.noteUuids.map((u) => after.get(u));
    const want = row.noteUuids.map((u) => before.get(u));
    const same = JSON.stringify(got) === JSON.stringify(want);
    if (!same) bad++;
    console.log('  ' + (same ? '✓' : '✗') + ' 第 ' + String(idx).padStart(3) + ' 句  显示 ' + JSON.stringify(row.text)
      + '   写回用了 ' + (w.calls && w.calls[0] ? w.calls[0].args.slice(0, 2).join(' ') : '?'));
    console.log('      改前 ' + JSON.stringify(want));
    console.log('      改后 ' + JSON.stringify(got));
    // 特别点出 `-` 与 `#n` 是否原样存活
    const wi = want.indexOf('-'), gi = got.indexOf('-');
    if (wi >= 0 || gi >= 0) {
      console.log('      ' + (wi === gi ? '✓' : '✗') + ' 连音 `-` 位置：改前 ' + wi + ' → 改后 ' + gi);
    }
    const wn = want.filter((l) => /#\d+$/.test(l)).sort().join(',');
    const gn = got.filter((l) => /#\d+$/.test(l)).sort().join(',');
    if (wn || gn) {
      console.log('      ' + (wn === gn ? '✓' : '✗') + ' 多音符词：改前 [' + wn + '] → 改后 [' + gn + ']');
    }
  }

  console.log('\n' + (bad ? bad + ' 句没对上' : picks.length + ' 句全部逐音符一致（`-` 与 `#1#2` 都原样存活）'));
  process.exitCode = bad ? 1 : 0;
})().catch((e) => { console.error('崩了：' + e.stack); process.exitCode = 1; });
