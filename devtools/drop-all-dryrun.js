// 一次性（用户 2026-10-10：「把所有干跑都删掉」）：把**所有**写工具参数表里的
// `checkParam("dryRun", …, …)` 去掉 —— 面板的通用表单里不再出现「干跑」勾选。
//
// ⚠️ 去掉参数**不等于**去掉能力的实现：工具的 `Plan`/`Apply` 两半都还在（`Plan` 就是干跑的实现），
//    只是界面上不再给你那个勾 —— 点「执行一次」就是真写（这些工具大多有 CLI 撤销栈）。
//
// 用法：node devtools/drop-all-dryrun.js
const fs = require('fs');
const path = require('path');

const dir = path.join(__dirname, '..', 'internal', 'tools');
const re = /^\t*checkParam\("dryRun", "[^"]*", (?:true|false)\),\n/gm;
let total = 0;
const touched = [];
for (const f of fs.readdirSync(dir)) {
  if (!f.endsWith('.go') || f.endsWith('_test.go')) continue;
  const p = path.join(dir, f);
  const s = fs.readFileSync(p, 'utf8');
  const n = (s.match(re) || []).length;
  if (!n) continue;
  fs.writeFileSync(p, s.replace(re, ''), 'utf8');
  total += n;
  touched.push(f + '×' + n);
}
console.log('删掉 ' + total + ' 处干跑参数：' + (touched.join('、') || '（没有）'));
