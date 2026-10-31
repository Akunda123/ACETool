/* 真机探针 ②：**为什么我们那份 `--notes` 写下去是 0 个音**。
 *
 * 已知：`clip create --notes` 本身可用（2 个中文歌词的音写进去了，noteCount=2）。
 * 我们要定位的是差异项 —— 最可疑的是 tenuto（`"lyric":"-"`）。
 *
 * 用法：node probe-tenuto.js
 */
const { execFileSync } = require('child_process');
const CLI = 'C:\\Program Files\\ACE Studio\\acestudio-cli.exe';
function cli(args) {
  try {
    return JSON.parse(execFileSync(CLI, args, { encoding: 'utf8', maxBuffer: 32 << 20 }));
  } catch (e) {
    return { __error: e.message.split('\n').slice(0, 4).join(' | ') };
  }
}

const CASES = [
  ['A 两个中文歌词', [
    { pos: 0, dur: 480, pitch: 60, lyric: '月' },
    { pos: 480, dur: 480, pitch: 62, lyric: '落' },
  ]],
  ['B 带一个 tenuto（lyric 是字面 -）', [
    { pos: 0, dur: 480, pitch: 60, lyric: '月' },
    { pos: 480, dur: 480, pitch: 62, lyric: '落' },
    { pos: 960, dur: 480, pitch: 62, lyric: '-' },
  ]],
  ['C 我们真写真发的那份（5 个音）', [
    { pos: 0, dur: 480, pitch: 60, lyric: '月' },
    { pos: 480, dur: 480, pitch: 62, lyric: '落' },
    { pos: 960, dur: 480, pitch: 64, lyric: '乌' },
    { pos: 1440, dur: 480, pitch: 64, lyric: '-' },
    { pos: 1920, dur: 960, pitch: 67, lyric: '霜' },
  ]],
  ['D 只有 tenuto，没有别的', [
    { pos: 0, dur: 480, pitch: 60, lyric: '-' },
  ]],
];

let pos = 30000;
for (const [name, notes] of CASES) {
  const r = cli(['--json', 'clip', 'create', '--track-index', '0', '--pos', pos + 't',
    '--dur', '9600t', '--type', 'sing', '--name', '探针-' + name.slice(0, 1), '--notes', JSON.stringify(notes)]);
  console.log('\n== ' + name);
  if (r.__error) { console.log('  CLI 报错：' + r.__error); }
  else {
    console.log('  回包 noteCount = ' + r.noteCount + '，clipUuid ' + r.clipUuid + '，clipEnd ' + r.clipEnd);
    if (r.noteCount === 0) console.log('  ⚠️ **收下了片段、但音符一个都没落**');
  }
  pos += 12000;
}

console.log('\n== clip list（末尾几个）');
const list = cli(['--json', 'clip', 'list', '--track-index', '0']);
const arr = list.clips || list.items || list;
if (Array.isArray(arr)) {
  arr.slice(-5).forEach((c, i) => console.log('  [' + (arr.length - 5 + i) + '] ' + c.clipName + '  '
    + c.clipBegin + '..' + c.clipEnd + '  noteCount=' + c.noteCount));
}
