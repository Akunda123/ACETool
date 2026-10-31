// 真机探针：ACE 的共享撤销栈里，**选中/移标记线/重开工程**各算不算一次可撤回操作？
// 用法：node devtools/probe-undo-selection.js
const { execFileSync } = require('child_process');

const CLI = 'C:\\Program Files\\ACE Studio\\acestudio-cli.exe';
const PROJ = 'C:/Users/<user>/Documents/<project-dir>/未命名1.acep';
const NOTE = '{e2590def-64e6-4a8a-8683-1083b7e432de}'; // 「沉」

function cli(args) {
  const out = execFileSync(CLI, [...args, '--json'], { encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 });
  return JSON.parse(out);
}

function stack(tag) {
  const j = cli(['history', 'list']);
  const names = (j.entries || []).slice(0, 3).map((e) => e.name).join(' / ');
  console.log(String(tag).padEnd(34) + ' count=' + String(j.count).padEnd(4) + ' 栈顶3条：' + names);
  return j.count;
}

let before = 0;
function step(tag, fn) {
  const b = stack('  基线（做之前）');
  fn();
  const a = stack(tag + ' ⇒ 之后');
  console.log('    Δ = ' + (a - b) + '\n');
  return a - b;
}

console.log('=== ACE 撤销栈探针（' + PROJ + '）===\n');
stack('起点');

step('selection set（清空选中 []）', () => cli(['selection', 'set', '--scope', 'editor', '--mode', 'replace', '--notes-to-select', '[]']));
step('selection set（选 1 个音符）', () => cli(['selection', 'set', '--scope', 'editor', '--mode', 'replace', '--notes-to-select', JSON.stringify([{ uuid: NOTE }])]));
step('caret set（移标记线）', () => cli(['caret', 'set', '--tick', '30960t']));
step('caret set（再移一次）', () => cli(['caret', 'set', '--tick', '31200t']));
step('project save', () => cli(['project', 'save']));
step('project open --discard-changes', () => cli(['project', 'open', PROJ, '--discard-changes']));
console.log('最终：');
cli(['history', 'list']).entries.slice(0, 6).forEach((e) => console.log('  [' + e.index + '] ' + e.name + '  actor=' + JSON.stringify(e.actor) + ' applied=' + e.applied));
