/* ACE `midiparam` pitchbend 真机探针 —— 把"文档说的"变成"实测到的"。
 *
 * 要回答的四件事（都是文档没写、只能实测的）：
 *   ① 一条空 lane 读出来长什么样（`empty` / `defaultValue` / `maxValue` / `fingerprint`）
 *   ② **范围写**到底怎么改包络 —— 文档说"宿主会在两端补锚点保住原值（接头是台阶不是斜坡）"，
 *      这句必须看真包络才算数（这是最容易"看着成功其实错"的地方）
 *   ③ 那些护栏是不是真的：单锚点不带 --replace、越界值、重复位置、乱序、陈旧指纹
 *   ④ 半音语义有没有落点（`sound-source` / `instrument list-params` 里有没有 bend range）
 *
 * ⚠️⚠️ 绝不经 shell 传 JSON：PowerShell / cmd 会把内层双引号吃掉（ACE 报
 * `invalid JSON (key must be a string)`）。这里一律 `execFileSync` + **数组参数**，
 * 字节原样到 CLI（同一个教训在 devtools/e2e*.js 里也记了）。
 *
 * 用法：
 *   node probe-ace-pitchbend.js probe     # 建临时轨+clip 并跑完探针（**不删**）
 *   node probe-ace-pitchbend.js cleanup   # 按记录下来的 uuid 删掉临时轨（不猜下标）
 */
const { execFileSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const CLI = process.env.ACECLI || 'C:\\Program Files\\ACE Studio\\acestudio-cli.exe';
const REC = path.join(__dirname, 'ace-probe-temp.json');
const MODE = process.argv[2] || 'probe';

/** 跑一条 CLI；**数组参数、不经 shell**。`--json` 加在末尾（官方文档的写法）。 */
function cli(args, { json = true } = {}) {
  const argv = json ? args.concat(['--json']) : args;
  try {
    const out = execFileSync(CLI, argv, { encoding: 'utf8', maxBuffer: 64 << 20, stdio: ['ignore', 'pipe', 'pipe'] });
    try { return { ok: true, v: JSON.parse(out) }; } catch (e) { return { ok: true, v: out.trim() }; }
  } catch (e) {
    const err = (e.stderr || '').toString().trim() || (e.stdout || '').toString().trim() || e.message;
    let env = null;
    try { env = JSON.parse(err).error; } catch (_) { /* 不是信封 */ }
    return { ok: false, err, env, code: e.status };
  }
}

let n = 0;
function step(title, args, opts) {
  n++;
  const r = cli(args, opts);
  console.log('\n── P' + n + ' ' + title);
  console.log('   $ ace ' + args.join(' '));
  if (r.ok) console.log('   → ' + JSON.stringify(r.v, null, 2).replace(/\n/g, '\n   '));
  else console.log('   ✗ exit ' + r.code + (r.env ? '  error[' + r.env.code + ']: ' + r.env.message + (r.env.hint ? '\n     hint: ' + r.env.hint : '') : '\n   ' + r.err));
  return r;
}
const short = (r) => (r.ok ? JSON.stringify(r.v) : 'ERR ' + (r.env ? r.env.code : r.err)).slice(0, 400);

(async () => {
  if (MODE === 'cleanup') return cleanup();
  if (!fs.existsSync(REC)) fs.writeFileSync(REC, '{}');
  const rec = JSON.parse(fs.readFileSync(REC, 'utf8'));

  console.log('===== 0. 基线（动工程之前）');
  const tracks0 = cli(['track', 'list']);
  const clip0 = cli(['clip', 'list', '--track-index', '0']);
  console.log('   轨：' + short(tracks0));
  console.log('   轨 0 的片段：' + short(clip0));
  console.log('   （用户原有工程只有 1 条 Sing 轨 + 1 个 sing 片段；下面新加的**全是探针**）');

  console.log('\n===== 1. 建临时轨 + 临时 GenericMidi clip');
  const tr = cli(['track', 'create', '--type', 'genericMidi', '--name', 'acetool-探针(临时)']);
  console.log('   track create → ' + short(tr));
  if (!tr.ok) { console.log('   ⛔ 建轨失败，探针中止（工程没被动过）'); process.exit(1); }
  const trackIndex = tr.v.trackIndex;
  const trackUuid = tr.v.trackUuid;

  const NOTES = [
    { pos: 0, dur: 480, pitch: 60, lyric: 'la' },
    { pos: 480, dur: 480, pitch: 62, lyric: 'la' },
  ];
  const cp = cli(['clip', 'create', '--track-index', String(trackIndex), '--pos', '0t', '--dur', '1920t',
    '--type', 'genericMidi', '--name', 'acetool-探针(临时)', '--notes', JSON.stringify(NOTES)]);
  console.log('   clip create → ' + short(cp));
  if (!cp.ok) { console.log('   ⛔ 建 clip 失败；临时轨留在那儿，先 cleanup 再查'); process.exit(1); }
  const clip = cp.v.clipUuid;
  // ⚠️ 记成**数组**：探针跑两次就有两条临时轨，只留最后一条会让前一条永远清不掉
  // （这就是本次踩到的 —— 第一次跑完忘了改，第二次跑把记录覆盖了）。
  const prev = fs.existsSync(REC) ? JSON.parse(fs.readFileSync(REC, 'utf8')) : [];
  const list = Array.isArray(prev) ? prev : (prev && prev.clipUuid ? [prev] : []);
  list.push({ trackIndex, trackUuid, clipUuid: clip, at: new Date().toISOString() });
  fs.writeFileSync(REC, JSON.stringify(list, null, 2));
  console.log('   记下来了（cleanup 只按这份记录删，不猜下标）：' + JSON.stringify({ trackIndex, trackUuid, clipUuid: clip }));

  console.log('\n===== 2. 空 lane 与 discovery');
  step('list-lanes（刚建、只写了音符）', ['midiparam', 'list-lanes', '--clip-uuid', clip]);
  step('read pitchbend（从没写过）', ['midiparam', 'read', '--clip-uuid', clip, '--lane', 'pitchbend']);
  step('read cc1（从没写过）', ['midiparam', 'read', '--clip-uuid', clip, '--lane', 'cc1']);
  step('velocity（音符级）', ['midiparam', 'velocity', '--clip-uuid', clip]);

  console.log('\n===== 3. 写一条完整曲线（--replace lane）');
  const CURVE = [{ pos: 0, value: 8192 }, { pos: 960, value: 12288 }, { pos: 1920, value: 8192 }];
  const w1 = step('write pitchbend --replace lane（3 锚点：中心→上→中心）',
    ['midiparam', 'write', '--clip-uuid', clip, '--lane', 'pitchbend', '--points', JSON.stringify(CURVE), '--replace', 'lane']);
  const r1 = step('读回（核对 count / maxValue / fingerprint）', ['midiparam', 'read', '--clip-uuid', clip, '--lane', 'pitchbend']);

  console.log('\n===== 4. ⭐ 范围写语义（文档说：宿主会在两端补锚点保住原包络）');
  const RANGE = [{ pos: 960, value: 2000 }, { pos: 1440, value: 2000 }];
  step('write 只圈 [960,1440]（值远远接不上原包络）',
    ['midiparam', 'write', '--clip-uuid', clip, '--lane', 'pitchbend', '--points', JSON.stringify(RANGE)]);
  const r2 = step('读回整条 → 看它**到底**补了什么锚点', ['midiparam', 'read', '--clip-uuid', clip, '--lane', 'pitchbend']);
  if (r1.ok && r2.ok) {
    console.log('\n   【对比】写前 ' + JSON.stringify(r1.v.points));
    console.log('   【对比】写后 ' + JSON.stringify(r2.v.points));
    console.log('   ⇒ 多出来的那两个"同 pos 双锚点"就是**宿主补的边**（台阶语义，文档只提了一半）');
  }

  console.log('\n===== 5. 单点 upsert / 删除 / 清空');
  step('set-point pos=480 value=4096（upsert）',
    ['midiparam', 'set-point', '--clip-uuid', clip, '--lane', 'pitchbend', '--pos', '480', '--value', '4096']);
  step('读回', ['midiparam', 'read', '--clip-uuid', clip, '--lane', 'pitchbend']);
  step('remove-point pos=480', ['midiparam', 'remove-point', '--clip-uuid', clip, '--lane', 'pitchbend', '--pos', '480']);
  step('读回', ['midiparam', 'read', '--clip-uuid', clip, '--lane', 'pitchbend']);
  step('clear pitchbend', ['midiparam', 'clear', '--clip-uuid', clip, '--lane', 'pitchbend']);
  step('读回（应当回到 empty:true）', ['midiparam', 'read', '--clip-uuid', clip, '--lane', 'pitchbend']);

  console.log('\n===== 6. ⛔ 护栏 —— 文档说会被拒的，逐个验（"被拒"也是信息）');
  step('单锚点、不带 --replace',
    ['midiparam', 'write', '--clip-uuid', clip, '--lane', 'pitchbend', '--points', JSON.stringify([{ pos: 0, value: 9000 }])]);
  step('越界值 16384（值域 0..16383）',
    ['midiparam', 'write', '--clip-uuid', clip, '--lane', 'pitchbend', '--points', JSON.stringify([{ pos: 0, value: 16384 }]), '--replace', 'lane']);
  step('负值 -1',
    ['midiparam', 'write', '--clip-uuid', clip, '--lane', 'pitchbend', '--points', JSON.stringify([{ pos: 0, value: -1 }]), '--replace', 'lane']);
  step('重复位置',
    ['midiparam', 'write', '--clip-uuid', clip, '--lane', 'pitchbend', '--points', JSON.stringify([{ pos: 0, value: 8000 }, { pos: 0, value: 9000 }])]);
  step('乱序位置',
    ['midiparam', 'write', '--clip-uuid', clip, '--lane', 'pitchbend', '--points', JSON.stringify([{ pos: 960, value: 8000 }, { pos: 0, value: 9000 }])]);
  step('不存在的 lane 名',
    ['midiparam', 'read', '--clip-uuid', clip, '--lane', 'cc200']);
  step('陈旧指纹（先读一个，再用假指纹写）',
    ['midiparam', 'write', '--clip-uuid', clip, '--lane', 'pitchbend', '--points', JSON.stringify(CURVE), '--replace', 'lane', '--if-match', 'deadbeef:midiparam:0']);

  console.log('\n===== 7. 半音语义有落点吗（bend range 在哪）');
  step('sound-source get（这条 genericMidi 轨用什么发声）', ['sound-source', 'get', '--track-index', String(trackIndex)]);
  step('instrument list-params（有没有 Pitch Bend Range 这类参数）', ['instrument', 'list-params', '--track-index', String(trackIndex)]);

  console.log('\n===== 8. 收尾前状态');
  step('list-lanes', ['midiparam', 'list-lanes', '--clip-uuid', clip]);
  console.log('\n⚠️ 临时轨/片段**还在**（轨 ' + trackIndex + '，clip ' + clip + '）。');
  console.log('   看过结果后跑 `node probe-ace-pitchbend.js cleanup` 删掉。');
})().catch((e) => { console.error('探针自己炸了：', e); process.exit(2); });

function cleanup() {
  if (!fs.existsSync(REC)) { console.log('没有 ' + REC + ' —— 没记录就不删（绝不按下标猜）'); process.exit(1); }
  const raw = JSON.parse(fs.readFileSync(REC, 'utf8'));
  const list = Array.isArray(raw) ? raw : [raw];
  console.log('按记录清理 ' + list.length + ' 条：' + JSON.stringify(list));
  const help = cli(['help', 'track', 'delete'], { json: false });
  console.log('\n（track delete 的用法，照着用）\n' + String(help.v).split('\n').slice(0, 22).join('\n'));
  for (const rec of list) {
    if (rec.clipUuid) console.log('clip delete → ' + short(cli(['clip', 'delete', '--clip-uuid', rec.clipUuid])));
  }
  for (const rec of list) {
    if (rec.trackUuid) console.log('track delete --track-uuid → ' + short(cli(['track', 'delete', '--track-uuid', rec.trackUuid])));
  }
  console.log('\n清理后：');
  console.log('   track list → ' + short(cli(['track', 'list'])));
  console.log('   clip list  → ' + short(cli(['clip', 'list', '--track-index', '0'])));
  console.log('\n（如果还有残留，说明有哪条没删掉 —— 记录还在 ' + REC + '，别删这个文件直到确认为止）');
}
