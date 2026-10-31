// 临时（只读）：摸清 v2 的 `dynamic` lane 到底是什么域、什么形状、有没有锚点，以及 v1 那边的 envelope/baseline。
const { execFileSync } = require('child_process');

const CLI = 'C:\\Program Files\\ACE Studio\\acestudio-cli.exe';
function cli(args) {
  try {
    return JSON.parse(execFileSync(CLI, ['--json'].concat(args), { encoding: 'utf8', maxBuffer: 64 << 20 }));
  } catch (e) {
    return { __err: ((e.stdout || '') + (e.stderr || '')).replace(/\s+/g, ' ').slice(0, 300) };
  }
}

const tracks = (cli(['track', 'list']).tracks) || [];
const out = [];
for (const t of tracks) {
  if (t.trackType !== 'Sing') continue;
  const clips = (cli(['clip', 'list', '--track-index', String(t.trackIndex)]).clips) || [];
  for (const [i, c] of clips.entries()) {
    const uuid = c.clipUuid || c.uuid;
    const ro = cli(['vocalparam', 'layers', '--clip-uuid', uuid]);
    if (ro.__err) continue;
    const dyn = (ro.params || []).filter((p) => p.param === 'dynamic')[0];
    out.push({ t, i, c, uuid, gen: ro.engineGeneration, dyn });
  }
}

for (const o of out) {
  console.log('轨 ' + o.t.trackIndex + ' ' + (o.t.trackName || '') + ' · 片段#' + (o.i + 1) +
    ' · 世代 ' + o.gen + ' · 长度 ' + (o.c.clipEnd - o.c.clipBegin));
  if (!o.dyn) { console.log('   （这一代没有 dynamic 参数）'); continue; }
  console.log('   dynamic: available=' + o.dyn.available + ' scale=' + (o.dyn.scale || '?') +
    ' 值域=' + JSON.stringify(o.dyn.valueRange || null));
  (o.dyn.layers || []).forEach((l) => console.log('     层 ' + l.layer + ' · role=' + l.role +
    ' · access=' + l.access + ' · shape=' + l.shape + ' · sparse=' + l.sparse));
  const r = cli(['vocalparam', 'read', '--clip-uuid', o.uuid, '--param', 'dynamic', '--layer', 'user']);
  if (r.__err) { console.log('     读失败：' + r.__err); continue; }
  const top = r.effective || null;
  const l0 = (r.layers || [])[0] || {};
  console.log('     read user: shapeKey=' + (l0.shape || '?') + ' posBegin=' + r.posBegin +
    ' count=' + r.count + ' 顶层键=' + Object.keys(l0).join(','));
  const pts = l0.points || l0.anchors || [];
  console.log('     点列（前 8）：' + JSON.stringify(pts.slice(0, 8)) + ' · 共 ' + pts.length +
    (top ? ' · effective 存在' : ' · 没有 effective'));
}
