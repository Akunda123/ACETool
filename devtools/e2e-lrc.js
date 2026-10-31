// 「LRC 的空格到底怎么处理」—— 拿 ACE **自带**的 `export lrc` 与我们的 `lyric.lrc` 逐行对账。
//
// 起因（用户 2026-10-05）：「自带的 lrc 对空格是怎么处理的」。
// 这个问题的答案决定我们该照谁：ACE 原生导出是**它自己的**合并规则，我们移植的脚本是另一套。
// 所以这里不猜，两边都跑出来，按「时间戳 + 文字」对齐比。
//
// 用法：node devtools/e2e-lrc.js
const { execFileSync } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const CLI = 'C:\\Program Files\\ACE Studio\\acestudio-cli.exe';
const H = { 'Content-Type': 'application/json', 'X-ACETool': '1' };
const BASE = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json'), 'utf8')).url.replace(/\/$/, '');

function cli(args) {
  const out = execFileSync(CLI, args.concat(['--json']), { maxBuffer: 128 * 1024 * 1024 }).toString('utf8');
  return JSON.parse(out.charCodeAt(0) === 0xfeff ? out.slice(1) : out);
}

const CJK = /[\u3400-\u9fff\u3040-\u30ff\uac00-\ud7af]/;
const LATIN = /[A-Za-z]/;

// 拆一行 LRC：开头的所有时间戳 + 剩下的文字
//
// ⚠️ **必须先剥掉行尾的 `\r`**：ACE 写的是 CRLF，而 JS 正则里的 `.` **不匹配 `\r`**，
// `(.*)$` 于是整条匹配失败（`$` 只认字符串末尾）⇒ 所有行都被当成"没有时间戳"跳过，
// 统计出来就是"0 行有空格、0 行空文字"那种自相矛盾的数（第一版就是这么被骗的）。
function splitLine(line) {
  const clean = line.replace(/\r$/, '');
  const m = clean.match(/^((?:\[[^\]]*\])+)([\s\S]*)$/);
  if (!m) return { times: [], text: clean };
  const times = [...m[1].matchAll(/\[([^\]]*)\]/g)].map((x) => x[1]);
  return { times, text: m[2] };
}

function analyze(name, raw, debug) {
  const lines = raw.split('\n').filter((l) => l !== '');
  if (debug && lines.length) {
    // ⚠️ 之前这段统计报出「0 行有空格、0 行空文字」，而文件明明两者都有 —— 先把真相打出来
    console.log('  [debug] 第 1 行原始 = ' + JSON.stringify(lines[0]));
    console.log('  [debug] 拆出来 = ' + JSON.stringify(splitLine(lines[0])));
  }
  let empty = 0, withSpace = 0, leading = 0, trailing = 0, dbl = 0, mixed = 0;
  const spaceSamples = [];
  for (const l of lines) {
    const { times, text } = splitLine(l);
    if (times.length === 0) continue;
    if (text === '') { empty++; continue; }
    const lead = text.match(/^ +/);
    const trail = text.match(/ +$/);
    if (lead) leading++;
    if (trail) trailing++;
    if (/ {2,}/.test(text)) { dbl++; }
    const hasSp = / /.test(text);
    if (hasSp) {
      withSpace++;
      if (spaceSamples.length < 6) spaceSamples.push(text);
    }
    if (CJK.test(text) && LATIN.test(text)) mixed++;
  }
  console.log('【' + name + '】共 ' + lines.length + ' 行（其中空文字 ' + empty + ' 行）');
  console.log('  含空格的行 ' + withSpace + ' · 行首有空格 ' + leading + ' · 行尾有空格 ' + trailing
    + ' · 出现连续两个空格 ' + dbl + ' · 中英混排 ' + mixed);
  if (spaceSamples.length) console.log('  含空格的行样例：' + spaceSamples.map((s) => JSON.stringify(s)).join(' · '));
  return { lines, empty };
}

(async () => {
  const es = cli(['editor', 'status']);
  const tl = cli(['track', 'list']);
  // ⚠️ 必须导**当前打开的那个片段所在的轨** —— 第一版拿了"第一条 Sing 轨"，
  // 而这份工程现在有两条 Sing 轨（轨 0 是旧的 la/跳一曲，轨 1 才是你在编的拼音那份）⇒ 比的是两份东西。
  const openTrack = tl.tracks.find((t) => t.trackIndex === es.trackIndex);
  if (!openTrack) { console.error('找不到当前轨 ' + es.trackIndex); process.exit(1); }
  const out = path.join(os.tmpdir(), 'acetool-lrc-native.lrc');
  if (fs.existsSync(out)) fs.unlinkSync(out);
  const exp = cli(['export', 'lrc', '--path', out, '--track-uuid', openTrack.trackUuid]);
  const nativeRaw = fs.readFileSync(out, 'utf8').replace(/^\uFEFF/, '');

  const r = await (await fetch(BASE + '/api/run', {
    method: 'POST', headers: H,
    body: JSON.stringify({ toolId: 'lyric.lrc', values: {} }),
  })).json();
  const mine = r.calls && r.calls[0] && r.calls[0].json;
  if (!r.ok || !mine) { console.error('我们的导出失败：' + (r.hint || '')); process.exit(1); }

  console.log('当前片段：轨 ' + es.trackIndex + ' 片段 ' + es.clipIndex + '「' + es.clipName + '」');
  console.log('我们：行数 ' + mine.lineCount + '（原始 ' + mine.rawLines + '，跳过空行 ' + mine.skipped
    + '，合并行 ' + mine.mergedRows + '）· ACE 自带 lineCount=' + exp.lineCount + '\n');

  analyze('ACE 自带 export lrc', nativeRaw, true);
  console.log('');
  analyze('我们（照原脚本）', mine.lrc, false);

  // 按「文字」配对，再比时间：ACE 那份是 `[mm:ss.cc]`，反解成秒
  const toSec = (s) => {
    const m = s.match(/^(\d+):(\d+)\.(\d+)$/);
    return m ? Number(m[1]) * 60 + Number(m[2]) + Number(m[3]) / 100 : NaN;
  };
  const nativeByText = new Map();
  for (const l of nativeRaw.split('\n')) {
    const { times, text } = splitLine(l);
    if (!text) continue;
    const arr = nativeByText.get(text) || [];
    for (const t of times) arr.push(toSec(t));
    nativeByText.set(text, arr);
  }
  let hit = 0;
  const missSameText = [], missNoText = [];
  for (const row of mine.rows) {
    const times = nativeByText.get(row.text);
    for (const t of row.times) {
      if (!times) { missNoText.push(row.text + ' @ ' + t.toFixed(2)); continue; }
      // 容 20 ms：两边一个截断一个可能四舍五入
      if (times.some((x) => Math.abs(x - t) <= 0.021)) hit++;
      else missSameText.push(row.text + '  我们 ' + t.toFixed(2) + 's  vs  ACE ' + times.map((x) => x.toFixed(2)).join('/') + 's');
    }
  }
  const total = mine.rows.reduce((a, x) => a + x.times.length, 0);
  console.log('\n对账（按文字配对、容 20 ms）：我们 ' + total + ' 个「时间戳」里对上 ' + hit + ' 个');
  if (missSameText.length) {
    console.log('  ⚠️ 文字一样但**时间对不上** ' + missSameText.length + ' 个（前 8）：');
    for (const m of missSameText.slice(0, 8)) console.log('    ' + m);
  }
  if (missNoText.length) {
    console.log('  文字在 ACE 那份里根本没出现 ' + missNoText.length + ' 个（前 8）—— ' +
      '多半是**行模型不同**（它按自己的句切、我们按缺口+换气切）：');
    for (const m of missNoText.slice(0, 8)) console.log('    ' + m);
  }

  // 落盘：真写一份到临时目录，读回来核 BOM 与内容（用户要的"能写文件"要端到端验）
  const dest = path.join(os.tmpdir(), 'acetool-lrc-write-test.lrc');
  if (fs.existsSync(dest)) fs.unlinkSync(dest);
  const w = await (await fetch(BASE + '/api/run', {
    method: 'POST', headers: H,
    body: JSON.stringify({ toolId: 'lyric.lrcFile', values: { path: dest, text: mine.lrc } }),
  })).json();
  const wj = w.calls && w.calls[0] && w.calls[0].json;
  if (!w.ok || !wj) {
    console.log('\n✗ 落盘失败：' + (w.hint || ''));
    process.exitCode = 1;
  } else {
    const raw = fs.readFileSync(dest);
    const bom = raw[0] === 0xEF && raw[1] === 0xBB && raw[2] === 0xBF;
    const body = raw.slice(3).toString('utf8');
    const same = body === mine.lrc || body === mine.lrc + '\n';
    console.log('\n落盘：' + wj.path + ' · ' + wj.bytes + ' 字节 · ' + wj.lines + ' 行');
    console.log('  ' + (bom ? '✓' : '✗') + ' UTF-8 带 BOM');
    console.log('  ' + (same ? '✓' : '✗') + ' 内容与面板那份逐字一致');
    if (!bom || !same) process.exitCode = 1;
  }
})().catch((e) => { console.error('崩了：' + e.stack); process.exitCode = 1; });
