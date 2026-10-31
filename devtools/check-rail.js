// exe 侧条（贴 ACE 的汉字细条）的自检：**三级** + 层级只在 ACE 前台时置顶 + 清单/行映射。
//
// 用户 2026-10-16 的口径：
//   ① 默认**特别细**  ② **靠近**了显示 1–2 个汉字  ③ 悬停到**字上** ⇒ 在**栏外**弹小窗显示全名
//   + 「它会显示在其他应用上」⇒ 细条**不许**浮在别的应用上面。
//
// ⚠️ 层级那条经过三轮真机（`internal/rail/rail_windows.go` 的 follow 里有完整的三层教训）：
//   常驻置顶 ❌（浮在别的应用上）→ 认 ACE 当属主 ❌（属主被一起提起来 ⇒ 钢琴窗整窗闪）→
//   不碰 Z 序 ❌（被钢琴窗盖住）⇒ 现行 = **条件置顶**：ACE 在前台 ⇒ 置顶；不在前台 ⇒ 立刻退回。
//   所以这里要核的是：`topMost` 与 `aceForeground` **一致**（而不是"永远 false"）。
//
// ⚠️ 用什么驱动：`POST /api/rail/cursor` 注入假光标（**只给自检用**）。
// 为什么不直接挪真鼠标：这台机器有人用 —— 实测脚本把光标放过去，150ms 后就被真实的手覆盖了，
// 于是第②③级永远验不到。注入之后三级是确定性的，还能顺便截图留证据。
//
// 用法：node devtools/check-rail.js [url]
//      行映射那份判据（注入第 i 行 ⇒ 命中第 i 项）在 `check-rail-rows.js`。
const fs = require('fs');
const os = require('os');
const path = require('path');

const PANEL = path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json');
const BASE = (process.argv[2] || JSON.parse(fs.readFileSync(PANEL, 'utf8')).url).replace(/\/$/, '');
const H = { 'Content-Type': 'application/json', 'X-ACETool': '1' };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

let bad = 0;
const ok = (name, cond, extra) => {
  console.log((cond ? '  ok   ' : '  FAIL ') + name + (extra ? '  — ' + extra : ''));
  if (!cond) bad++;
};

const rail = async () => (await fetch(BASE + '/api/rail', { cache: 'no-store' })).json();
const setCursor = async (body) =>
  (await fetch(BASE + '/api/rail/cursor', { method: 'POST', headers: H, body: JSON.stringify(body) })).json();

(async () => {
  const st = await rail();
  console.log('面板：' + BASE);
  if (!st.running || !st.visible) {
    console.log('  FAIL 细条没在跑 / 没显示 —— ' + JSON.stringify(st));
    console.log('  常见原因：① ACE 没开或**被最小化**（那时细条会正确地把自己藏起来）');
    console.log('            ② 面板是 `-no-rail` 起的；③ 细条起的时候报错了（看 exe 控制台）');
    console.log('  想让 ACE 回来：go run ./devtools/acefocus');
    process.exit(1);
  }
  const scale = (st.dpi || 96) / 96;
  const r0 = st.rect;
  const midY = Math.round((r0.Top + r0.Bottom) / 2);
  console.log(`  细条 0x${(st.hwnd || 0).toString(16)} 贴「${st.targetTitle}」(0x${(st.targetHwnd || 0).toString(16)}) dpi=${st.dpi} scale=${scale}`);

  // ① 默认：特别细
  // ⚠️ 宽度按**物理像素**算，Go 那边是截断（int32(5*1.5)=7，不是四舍五入的 8）⇒ 允许 ±1px
  const wThin = 5 * scale;
  const wShort = 34 * scale;
  ok('① 默认**特别细**（5 逻辑像素，±1px）', Math.abs((r0.Right - r0.Left) - wThin) <= 1,
    (r0.Right - r0.Left) + 'px（期望 ≈' + wThin + '）');
  ok('① 默认**没有栏外小窗**（只有颜色码）', st.level === 0 && !st.tipText,
    'level=' + st.level + ' tip=' + JSON.stringify(st.tipText || ''));

  // ⭐ 第五轮（2026-10-17）的判据：细条是 ACE 那个窗口的**子窗口**（`SetParent`）。
  //    为什么必须是子窗口：子窗口**永远画在父窗口客户区之上**（不会被 ACE 盖住），
  //    而且**根本不是顶层窗口**（别的应用盖住 ACE 时它跟着被盖住 ⇒ 不可能浮在别的应用上面）。
  //    前四轮（常驻 topmost / 认属主 / 不碰 Z 序 / 条件置顶 / 插到 ACE 之上）都在这两条上翻过车，
  //    完整教训写在 `internal/rail/rail_windows.go` 的 follow() 里。
  ok('⭐ 细条挂在 AC​E 那个窗口里（子窗口）', (st.parentHwnd || 0) !== 0 && st.parentHwnd === st.targetHwnd,
    'parent=0x' + (st.parentHwnd || 0).toString(16) + ' target=0x' + (st.targetHwnd || 0).toString(16));
  ok('⛔ 不是顶层置顶（子窗口更不可能置顶）', st.topMost === false, 'topMost=' + st.topMost);
  // 子窗口不可能被自己的父窗口盖住 —— 这条就是用户报「关闭 web 面板后侧栏会消失」时**真正错**的那条不变量
  ok('⛔ 那个点上最上面的窗口不是 ACE 自己（子窗口不被父窗口盖住）',
    st.hitPid !== st.targetPid || st.hitSelf === true,
    'hitPid=' + st.hitPid + ' targetPid=' + st.targetPid + ' hitSelf=' + st.hitSelf + ' hitClass=' + (st.hitClass || ''));
  if (!st.hitSelf) {
    console.log('    ℹ️ 此刻细条那个点上最上面是 pid=' + st.hitPid + ' class=' + (st.hitClass || '') +
      '（别的应用盖住了 ACE ⇒ 细条跟着被盖住，**按设计如此**；ACE 到前面来它就在）');
  }

  // 清单：19 项、四类颜色语义齐、"改工程文件"那组**连在一起**、每类都带着自己该带的字段。
  // （细条上画什么、点什么，全看这份清单；它错了上面几条几何断言全无意义）
  const list = await (await fetch(BASE + '/api/rail/items', { cache: 'no-store' })).json();
  const its = list.items || [];
  const kinds = new Set(its.map((x) => x.Kind));
  // 19 → 21：2026-10-17 加了「设置」「展开为 web 页」两项（用户要求）。
  ok('清单有 21 项', its.length === 21, '实际 ' + its.length);
  // 四色语义（用户 2026-10-17：「音素也算绿色，把需要改工程的放到一起，用同色」）：
  //   绿 run（侧栏里就地完成）/ 琥珀 params（要参数且塞不下，目前没用到）/ 蓝 page（开窗口）/ 红 file（改工程文件）
  ok('颜色语义齐（run / page / file 都在；params 现在清单里没用到）',
    kinds.has('run') && kinds.has('page') && kinds.has('file') && !kinds.has('params'),
    [...kinds].join(','));
  // 「改工程文件」那一组必须**连续**放（用户：「把需要改工程的放到一起」）
  const fileIdx = its.map((x, i) => (x.Kind === 'file' ? i : -1)).filter((i) => i >= 0);
  ok('「改工程文件」那组是**连续的一段**（4 项：跨复/语调/颤音/单复）',
    fileIdx.length === 4 && fileIdx[fileIdx.length - 1] - fileIdx[0] === 3,
    '行号 ' + fileIdx.join(',') + ' · ' + its.filter((x) => x.Kind === 'file').map((x) => x.Short).join('、'));
  ok('kind=page / file 的每项都带 Page（否则点了只能开根路径 = 主面板）',
    its.filter((x) => x.Kind === 'page' || x.Kind === 'file').every((x) => (x.Page && x.Page.endsWith('.html')) || x.Act),
    its.filter((x) => (x.Kind === 'page' || x.Kind === 'file') && !x.Page).map((x) => x.Full).join('、') || '都对');
  ok('kind=run 的每项都带 Tool', its.filter((x) => x.Kind === 'run').every((x) => !!x.Tool),
    its.filter((x) => x.Kind === 'run' && !x.Tool).map((x) => x.Full).join('、') || '都对');
  const byFull = (s) => its.find((x) => x.Full === s) || {};
  ok('「复制歌词」= 点一下就执行（run + clipCopy：exe 自己写系统剪贴板）',
    byFull('复制歌词').Kind === 'run' && byFull('复制歌词').Act === 'clipCopy',
    JSON.stringify(byFull('复制歌词')));
  ok('「粘贴歌词」= 点一下就执行（run + clipPaste：exe 自己读系统剪贴板）',
    byFull('粘贴歌词').Kind === 'run' && byFull('粘贴歌词').Act === 'clipPaste',
    JSON.stringify(byFull('粘贴歌词')));
  // ⭐ 「连接」/「音素」：用户 2026-10-17 的口径演进 ——
  //    「搬进细条」→「连接做成点按钮就用，参数只是附带」→「音素也算绿色」：
  //      · 连接 = 绿（run）+ 附属档位参数（**左键直接跑**，右键改阈值）
  //      · 音素 = **绿**（run）+ 两个框，但「查找」必填 ⇒ **左键打开参数条**（`Panel.NeedsInput`），不直接跑
  //    这里只核元数据；**交互**用合成点击验，在 `go run ./devtools/panelcheck`。
  const jie = byFull('连接音符'), yin = byFull('批量替换音素');
  ok('「连接音符」= 绿（run）+ 附属档位参数（9 档 · maxGap · 左键直接跑）',
    jie.Kind === 'run' && jie.Panel && jie.Panel.Kind === 'tier' && jie.Panel.Arg === 'maxGap' &&
    (jie.Panel.Tiers || []).length === 9 && !jie.Panel.NeedsInput,
    JSON.stringify({ kind: jie.Kind, panel: jie.Panel ? { kind: jie.Panel.Kind, arg: jie.Panel.Arg, n: (jie.Panel.Tiers || []).length, needsInput: !!jie.Panel.NeedsInput } : null }));
  ok('「批量替换音素」= **绿**（run）+ 两个框 · NeedsInput（左键开参数条，不直接跑）',
    yin.Kind === 'run' && yin.Panel && yin.Panel.Kind === 'fields' && yin.Panel.NeedsInput === true &&
    (yin.Panel.Fields || []).length === 2 && (yin.Values || {}).scope === 'auto',
    JSON.stringify({ kind: yin.Kind, needsInput: yin.Panel ? !!yin.Panel.NeedsInput : null, n: (yin.Panel ? (yin.Panel.Fields || []).length : 0) }));

  // ⭐ 用户 2026-10-16：「只留一个小细条，现在挡关闭按钮了」⇒ 细条必须**让开 ACE 的标题栏**。
  // ACE 是无边框窗口（窗口矩形与客户区顶差 = 0，关闭按钮画在客户区里）⇒ 只能靠 topInset 让位，
  // 所以这里按**数值**核：细条顶边 ≥ 客户区顶边 + topInset（42 逻辑像素）。
  const cli = st.targetClient || {};
  // ⚠️ 细条贴的是**窗口外缘**（代码里 `x = rc.Right - width`），不是客户区外缘：
  // 真机实测 ACE 的钢琴窗**客户区左右各内缩 11px**（边框）⇒ 贴客户区外缘会看着"没贴住"。
  // 所以这里核两条：① 不超出**窗口**右缘；② 至少盖到**客户区**右缘（别浮在客户区外面）。
  const winr = st.targetRect || cli;
  const inset = 42 * scale;
  if (cli.Right) {
    ok('📐 细条不超出目标窗口右缘', st.rect.Right <= winr.Right + 1,
      'rail 右=' + st.rect.Right + ' 窗口右=' + winr.Right);
    ok('📐 细条贴住了客户区右缘（没浮在客户区外面）', st.rect.Right >= cli.Right - 1,
      'rail 右=' + st.rect.Right + ' 客户区右=' + cli.Right);
    ok('📐 细条让开了标题栏（顶边 ≥ 客户区顶 + ' + Math.round(inset) + 'px）',
      st.rect.Top >= cli.Top + inset - 2,
      'rail 顶=' + st.rect.Top + ' 客户区顶=' + cli.Top + ' 让位=' + (st.rect.Top - cli.Top) + 'px');
    ok('📐 细条不超出客户区底部', st.rect.Bottom <= cli.Bottom + 1,
      'rail 底=' + st.rect.Bottom + ' 客户区底=' + cli.Bottom);
  } else {
    ok('📐 报告里有客户区矩形（否则没法核"让开标题栏"）', false, JSON.stringify(cli));
  }

  // ② 靠近：显示两个字  // ⚠️ 距离是**物理像素**：靠近区 = (34*scale, 52*scale]，比"两个字"宽、又还没贴到字上
  const nearW = 52 * scale;
  const dNear = Math.round((wShort + nearW) / 2);
  await setCursor({ x: r0.Right - dNear, y: midY });
  await sleep(450);
  const s1 = await rail();
  ok('② 靠近（离右缘 ' + dNear + 'px）⇒ level=1（显示 1–2 个汉字）', s1.level === 1,
    'level=' + s1.level + ' 宽=' + (s1.rect.Right - s1.rect.Left) + 'px');
  ok('② 宽度 = 两个字（34 逻辑像素，±1px）', Math.abs((s1.rect.Right - s1.rect.Left) - wShort) <= 1,
    (s1.rect.Right - s1.rect.Left) + 'px');
  ok('② 右缘锚点没动（细条是往左长的）', s1.rect.Right === r0.Right, s1.rect.Right + ' vs ' + r0.Right);

  // ③ 压在字上：栏外弹全名
  await setCursor({ x: r0.Right - Math.round(15 * scale), y: midY });
  await sleep(450);
  const s2 = await rail();
  ok('③ 压在字上 ⇒ level=2', s2.level === 2, 'level=' + s2.level);
  ok('③ 细条**不再加宽**（全名不在条里）', Math.abs((s2.rect.Right - s2.rect.Left) - wShort) <= 1,
    (s2.rect.Right - s2.rect.Left) + 'px');
  ok('③ 栏外弹了小窗、写着全名', !!s2.tipText && (s2.tipRect.Right - s2.tipRect.Left) > 0, JSON.stringify(s2.tipText));
  ok('③ 小窗确实在**栏外**（在细条左边）', s2.tipRect.Right <= s2.rect.Left,
    'tip 右边=' + s2.tipRect.Right + ' 细条左边=' + s2.rect.Left);
  ok('③ 小窗与光标那一行**对齐**（同一行上下 20px 内）',
    Math.abs((s2.tipRect.Top + s2.tipRect.Bottom) / 2 - midY) <= Math.max(24, s2.itemH),
    'tip 中线=' + Math.round((s2.tipRect.Top + s2.tipRect.Bottom) / 2) + ' 光标=' + midY);
  ok('③ 注入生效中（状态里标着 fakeCursor）', s2.fakeCursor === true);
  console.log(`  第③级那一条：hover=${s2.hover} 「${s2.tipText}」 ${s2.tipRect.Right - s2.tipRect.Left}px @${s2.tipRect.Left},${s2.tipRect.Top}`);

  // 收尾：取消注入（把"靠近判定"还给用户真实的光标）
  await setCursor({ clear: true });
  await sleep(300);
  const s3 = await rail();
  ok('收尾：取消注入后 fakeCursor=false', !s3.fakeCursor);

  console.log('\n' + (bad ? bad + ' 项失败' : '全部通过'));
  process.exitCode = bad ? 1 : 0;
})().catch((e) => { console.error('自检脚本自己炸了：' + e.stack); process.exitCode = 2; });

