// 把当前 Sing 片段的**逐音符歌词**与 ACE 的**句子**并排打出来（取证用）。
//
// 为什么需要它：面板要做的「忽略连音」得知道 ACE 到底把连音存成什么 ——
// 是 `-`？还是 `hello#1` / `hello#2`？还是别的？猜不得（猜错就是"显示对了、写回错了"）。
// 这里直接问 CLI 要原始数据，按句把每个音符的 lyric 摆出来。
//
// 用法：node devtools/dump-lyrics.js [轨道] [片段]
const { execFileSync } = require('child_process');

const CLI = 'C:\\Program Files\\ACE Studio\\acestudio-cli.exe';
const T = process.argv[2] === undefined ? null : Number(process.argv[2]);
const C = process.argv[3] === undefined ? null : Number(process.argv[3]);

function json(args) {
  const out = execFileSync(CLI, args.concat(['--json']), { maxBuffer: 128 * 1024 * 1024 }).toString('utf8');
  return JSON.parse(out.charCodeAt(0) === 0xfeff ? out.slice(1) : out);
}

// 没给轨道/片段就跟着编辑器走（和面板同一个口径）
let track = T, clip = C;
if (track === null || clip === null) {
  const es = json(['editor', 'status']);
  if (!es.isAvailable) { console.error('编辑器里没打开片段（把标记线移进片段）'); process.exit(1); }
  track = es.trackIndex; clip = es.clipIndex;
  console.log('跟着编辑器走：轨道 ' + track + ' 片段 ' + clip + '（' + es.clipName + ' · ' + es.editorType + '）');
}

const sent = json(['clip', 'lyrics', '--track-index', String(track), '--clip-index', String(clip)]);
const content = json(['clip', 'note-content', '--track-index', String(track), '--clip-index', String(clip)]);
const byUuid = new Map();
for (const n of content.notes) byUuid.set(n.noteUuid, n);

console.log('句子 ' + sent.sentenceCount + ' 句 · 音符 ' + content.notes.length + ' 个\n');
console.log('句子 |  tick 起-止      | ACE 报的整句 lyric            | 逐音符 lyric（按 ACE 给的 uuid 顺序）');
console.log('-----+------------------+-------------------------------+----------------------------------');
for (const s of sent.sentences) {
  const per = s.noteUuids.map((u) => {
    const n = byUuid.get(u);
    return n ? JSON.stringify(n.lyric) : '(读不到)';
  });
  console.log(String(s.index).padStart(4) + ' | ' + String(s.sentenceBegin).padStart(7) + '-' + String(s.sentenceEnd).padStart(7)
    + ' | ' + JSON.stringify(s.lyric).padEnd(30) + '| ' + per.join(' '));
}

// 逐音符里出现过的"特殊"写法统计（连音/多音节词的形态）
const marks = new Map();
for (const n of content.notes) {
  const l = n.lyric || '';
  let kind = null;
  if (l === '-') kind = '- （连音/延音）';
  else if (/#\d+$/.test(l)) kind = '#n 结尾（多音符词的一段）';
  if (kind) marks.set(kind, (marks.get(kind) || 0) + 1);
}
console.log('\n特殊写法统计：');
if (!marks.size) console.log('  （没有 `-`，也没有 `#n`）');
for (const [k, v] of marks) console.log('  ' + String(v).padStart(4) + '  ' + k);

// 一个音符的所有字段（看还有没有别的线索：syllable / grapheme / language）
console.log('\n第一个音符的全部字段：');
console.log(JSON.stringify(content.notes[0], null, 1));
