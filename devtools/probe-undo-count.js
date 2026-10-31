// 量「每个一键按钮会压几个撤销条目」—— 用户问：「其他按钮也需要多次撤回吗」。
//
// 判据：ACE 的每个**写动词**一次调用 = 至多一个撤销条目（各命令 help 原文：
// `note set-lyric` / `note add` / `phoneme reset` 都是 "one call lands exactly one … undo entry"）。
// ⇒ **计划里几条命令，就是要按几次 Ctrl+Z**（"值没变"时可能不压，所以是上界）。
//
// 所以这里对每个按钮跑一次**干跑**（`dryRun=1`，一个字节都不写），数它计划发几条命令。
//
// 用法：node devtools/probe-undo-count.js
const fs = require('fs');
const path = require('path');
const os = require('os');
const H = { 'Content-Type': 'application/json', 'X-ACETool': '1' };
const BASE = JSON.parse(fs.readFileSync(path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json'), 'utf8')).url.replace(/\/$/, '');

const CASES = [
  { label: '连接音符', tool: 'note.join', values: { maxGap: '0' } },
  { label: '播放头位置分割轨道', tool: 'track.split', values: {} },
  { label: '粘贴歌词（整片段）', tool: 'lyric.paste', values: { mark: '-', text: 'la la la la la la la la' } },
  { label: '添加 - 歌词', tool: 'lyric.shift', values: { mark: '-' } },
  { label: '删除并前移', tool: 'lyric.remove', values: { mode: 'merge' } },
  { label: '填词·改一句（ACE 原生填充）', tool: 'lyric.setSentence', values: { sentence: '0', text: 'la la la', mode: 'fill' } },
  { label: '填词·改一句（逐音符）', tool: 'lyric.setSentence', values: { sentence: '0', text: 'la la la', mode: 'perNote', ignoreTenuto: '0' } },
  { label: '批量替换音素', tool: 'phoneme.replace', values: { find: 'l', replace: 'r', scope: 'clip' } },
  // ⭐ 通用「撤回这一批」（`history.undoBatch`）：**一条命令 = 一次 Ctrl+Z** 的规则同样成立，
  //   但它走 ACE 自己的撤销栈 ⇒ 退 N 条就是 N 次 `history undo`（外加每步一次 `history list` 守卫，
  //   所以真机上大概按 **2×N 次 CLI 往返**估时间，单次 ~80ms）。
  { label: '撤回这一批（锚点以上 5 条）', tool: 'history.undoBatch', values: { anchor: '0', count: '5' } },
  { label: 'RGB 变色（一条轨）', tool: 'track.color', values: { trackIndex: '0', color: '#EC4F44' } },
  { label: 'LRC 写成文件', tool: 'lyric.lrcFile', values: { path: 'C:\\Users\\x\\t.lrc', text: '[00:01.00]la\n' } },
];

// Plan/Apply 型的工具（计划与执行分两步）**只回一笔伪调用**（那份报告 JSON 本身），
// ⇒ 命令条数得从**报告字段**里读。第一版把它们都数成 1 条，是错的。
//   phoneme.replace → willSet（会写几个音符 = 几条 `phoneme set`）
//   track.split     → steps（计划要做的步骤数）
//   lyric.lrcFile   → 0（只落一个文件，ACE 侧一个撤销条目都不压）
function realCount(tool, j) {
  if (!j) return null;
  if (tool === 'phoneme.replace') return typeof j.willSet === 'number' ? j.willSet : null;
  if (tool === 'track.split') return Array.isArray(j.steps) ? j.steps.length : null;
  if (tool === 'lyric.lrcFile') return 0;
  if (tool === 'history.undoBatch') return j.willUndo;   // 退几条 = 几次 `history undo`
  return null;
}

function short(args) {
  // 去掉 exe 路径，只留动词与关键参数
  const a = args.slice(1);
  return a.slice(0, 4).join(' ') + (a.length > 4 ? ' …' : '');
}

(async () => {
  console.log('干跑（一个字节都不写）—— 计划几条命令 ⇒ 最多要按几次 Ctrl+Z\n');
  const rows = [];
  for (const c of CASES) {
    const vals = Object.assign({}, c.values, { dryRun: '1' });
    let d;
    try {
      d = await (await fetch(BASE + '/api/run', {
        method: 'POST', headers: H, body: JSON.stringify({ toolId: c.tool, values: vals }),
      })).json();
    } catch (e) { rows.push([c.label, '?', '请求失败']); continue; }
    // ⚠️ 干跑时**计划命令在 `plan` 里**（`panel_api` 的 `resp.Plan = calls`），`calls` 是空的 ——
    // 第一版只看 `calls`，于是每个按钮都数成 0 条。真跑时两者反过来（plan 空、calls 满）。
    const calls = (d.plan && d.plan.length ? d.plan : (d.calls || []));
    const j = (d.calls && d.calls[0] && d.calls[0].json) || (d.plan && d.plan[0] && d.plan[0].json) || null;
    let n = calls.filter((x) => (x.args && x.args.length) || (x.Args && x.Args.length)).length;
    const real = realCount(c.tool, j);
    const first = (calls[0] && (calls[0].args || calls[0].Args)) || [];
    rows.push([c.label, d.ok ? String(real === null ? n : real) : '失败',
      d.ok
        ? (real !== null ? '计划/执行分两步，按报告字段算' : (n ? short(first) : '（没有写命令）'))
        : (d.hint || '').slice(0, 60)]);
  }
  const w = Math.max(...rows.map((r) => [...r[0]].length));
  for (const r of rows) {
    const pad = w - [...r[0]].length;
    console.log('  ' + r[0] + ' '.repeat(pad) + '  →  ' + r[1].padStart(3) + ' 条命令   ' + r[2]);
  }
  console.log('\n注：命令条数是**上界**（"值没变"时 ACE 不压条目）；只读工具与"只写文件"的工具是 0。');
})();
