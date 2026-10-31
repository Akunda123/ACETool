// 审计 ACE 的**共享撤销栈**（`history list`）—— 看 RGB 变色在撤销栈里留下了什么。
//
// 起因：`track set --color` **每设一次轨色就压一条撤销记录**（name = "更改轨道颜色"、actor = "远程控制"）。
// RGB 窗口按毫秒级轮转，等于在用户的撤销栈里**灌水** —— 灌满之后 Ctrl+Z 就回不到用户自己的操作了。
// 这个脚本把账算清楚（谁写的、多少条、占了多少），而不是靠猜。
//
// 用法：node devtools/undo-audit.js
const { execFileSync } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const CLI = 'C:\\Program Files\\ACE Studio\\acestudio-cli.exe';

function historyList() {
  // ⚠️ 不禁用 shell 直接 execFileSync 拿 stdout 会撞上 PowerShell 那条 "EPERM 命名管道"边界吗？
  // 不会：这里没有 shell 参与，Node 直接 spawn 可执行文件并读管道 —— 实测可用。
  const out = execFileSync(CLI, ['history', 'list', '--json'], { maxBuffer: 64 * 1024 * 1024 });
  let s = out.toString('utf8');
  if (s.charCodeAt(0) === 0xfeff) s = s.slice(1);
  return JSON.parse(s);
}

const d = historyList();
const byName = new Map();
const byNameActor = new Map();
for (const e of d.entries) {
  byName.set(e.name, (byName.get(e.name) || 0) + 1);
  const k = e.name + ' | ' + (e.actor || '(用户手点)');
  byNameActor.set(k, (byNameActor.get(k) || 0) + 1);
}

console.log('撤销栈总条目：' + d.count + '（canUndo=' + d.canUndo + ' canRedo=' + d.canRedo + '）\n');
console.log('按「操作名 | 作者」：');
for (const [k, v] of [...byNameActor].sort((a, b) => b[1] - a[1])) {
  console.log('  ' + String(v).padStart(5) + '  ' + k);
}

// 栈顶连续段：从最新往回数，连续多少条是同一个作者写的（= 最近这一串"灌水"有多长）
const top = d.entries[0];
let run = 0;
for (const e of d.entries) {
  if (e.name === top.name && (e.actor || '') === (top.actor || '')) run++;
  else break;
}
console.log('\n栈顶连续 ' + run + ' 条都是「' + top.name + '」（作者：' + (top.actor || '(用户手点)') + '）');

fs.writeFileSync(path.join(os.tmpdir(), 'acetool-undo-audit.json'),
  JSON.stringify({ count: d.count, byNameActor: Object.fromEntries(byNameActor), topRun: run }, null, 1));
console.log('明细已落：' + path.join(os.tmpdir(), 'acetool-undo-audit.json'));
