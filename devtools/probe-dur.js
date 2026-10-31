/* 真机探针 ③：定位 `clip create` **收下片段但一个音符都不落**的触发条件。
 *
 * 线索：我们那份写下去 0 个音，可 ACE 还是把片段命名成了「月落乌霜」（头几个字的歌词）
 * ⇒ **载荷解析过了**（名字是从歌词推的），但音符没落。唯一与成功探针不同的参数是
 * `--dur` **正好等于**音符总长（2880t）。所以这一轮专测「片段时长 vs 音符末尾」。
 *
 * 用法：node probe-dur.js
 */
const { execFileSync } = require('child_process');
const CLI = 'C:\\Program Files\\ACE Studio\\acestudio-cli.exe';
function cli(args) {
  try { return JSON.parse(execFileSync(CLI, args, { encoding: 'utf8', maxBuffer: 32 << 20 })); }
  catch (e) { return { __error: e.message.split('\n').slice(0, 3).join(' | ') }; }
}

const FIVE = [
  { pos: 0, dur: 480, pitch: 60, lyric: '月' },
  { pos: 480, dur: 480, pitch: 62, lyric: '落' },
  { pos: 960, dur: 480, pitch: 64, lyric: '乌' },
  { pos: 1440, dur: 480, pitch: 64, lyric: '-' },
  { pos: 1920, dur: 960, pitch: 67, lyric: '霜' },
];
const TWO = [
  { pos: 0, dur: 480, pitch: 60, lyric: '月' },
  { pos: 480, dur: 480, pitch: 62, lyric: '落' },
];

const CASES = [
  ['E 5 个音，dur = 2880（**正好等于**音符末尾）', FIVE, 2880],
  ['F 5 个音，dur = 2881（末尾 +1 tick）', FIVE, 2881],
  ['G 5 个音，dur = 3840（一小节余量）', FIVE, 3840],
  ['H 2 个音，dur = 960（正好）', TWO, 960],
  ['I 2 个音，dur = 961（+1）', TWO, 961],
];

let pos = 100000;
for (const [name, notes, dur] of CASES) {
  const r = cli(['--json', 'clip', 'create', '--track-index', '0', '--pos', pos + 't',
    '--dur', dur + 't', '--type', 'sing', '--name', 'dur探针', '--notes', JSON.stringify(notes)]);
  console.log('== ' + name);
  if (r.__error) console.log('   CLI 报错：' + r.__error);
  else console.log('   回包 noteCount = ' + r.noteCount + '，clipEnd = ' + r.clipEnd
    + (r.noteCount === 0 ? '   ⚠️ **片段建了、音符没落**' : '   ✓'));
  pos += 12000;
}
