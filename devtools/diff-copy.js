// 对比「用户在 md 里改过的文案」与「源码当前状态」，列出差异（只报告，不改任何文件）。
//
// 用法：node devtools/diff-copy.js <用户的 md> <基线 md（从当前源码生成）>
// 说明：两份都是 `text-catalog.js` 的产物 ⇒ 有 `位置` 列（`文件 种类:行号`）可以对上。
//       以「完整位置（含行号）」为键 —— 源码在两次生成之间没动过，行号是稳的；比「当前文本」。
const fs = require('fs');

function parse(file) {
  const out = new Map();
  const dup = new Set();
  for (const line of fs.readFileSync(file, 'utf8').split(/\r?\n/)) {
    const m = line.match(/^\|\s*\d+\s*\|\s*`([^`]+)`\s*\|\s*(.*?)\s*\|\s*([^|]*)\|\s*$/);
    if (!m) continue;
    // ⚠️ 键必须**带行号**：不带的话同一页里几十条同种位置会撞在一起（试过，只剩 94 条）
    const pos = m[1];
    const text = m[2].replace(/\\\|/g, '|').trim();
    if (out.has(pos)) { dup.add(pos); }
    out.set(pos, { text, note: m[3].trim(), raw: line });
  }
  return { map: out, dup };
}

const [mine, base] = process.argv.slice(2);
const A = parse(mine);   // 用户的（可能有改）
const B = parse(base);   // 源码现状

const changed = [];
const onlyA = [];
const onlyB = [];
for (const [k, v] of A.map) {
  if (!B.map.has(k)) { onlyA.push([k, v.text]); continue; }
  if (B.map.get(k).text !== v.text) changed.push([k, B.map.get(k).text, v.text]);
}
for (const [k, v] of B.map) if (!A.map.has(k)) onlyB.push([k, v.text]);

console.log('用户 md：' + A.map.size + ' 条；源码基线：' + B.map.size + ' 条');
console.log('改过的：' + changed.length);
for (const [k, o, n] of changed) console.log('  · ' + k + '\n      - ' + o + '\n      + ' + n);
if (onlyA.length) {
  console.log('\n只在用户 md 里（源码没有对应位置，可能是新增行/我这边改过行号）：' + onlyA.length);
  for (const [k, t] of onlyA) console.log('  + ' + k + ' → ' + t);
}
if (onlyB.length) {
  console.log('\n只在源码基线里（用户 md 少了，可能是删掉的条目）：' + onlyB.length);
  for (const [k, t] of onlyB) console.log('  - ' + k + ' → ' + t);
}
if (A.dup.size) console.log('\n⚠️ 用户 md 里有重复位置（会影响定位）：' + [...A.dup].join(', '));
