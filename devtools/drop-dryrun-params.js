// 一次性：把「干跑」这条**界面口子**从音高线那一支去掉（用户 2026-10-10：「把所有干跑都删掉」）。
//
// 做法：删掉这几个工具参数表里的 `checkParam("dryRun", …, true)` 那一行 ——
// 于是面板的通用表单里不再出现「干跑」勾选，页面也不再传它（pitch.html 那边的复选框同批删掉）。
// ⚠️ 工具内部的 `Plan` 那一半**留着**（框架仍可只跑计划：`Plan` 是干跑的实现；
//    没有 dryRun 参数时 `/api/run` 会直接走 Apply —— 也就是"执行"）。
//
// 用法：node devtools/drop-dryrun-params.js
const fs = require('fs');
const path = require('path');

const files = ['pitchwithin.go', 'pitchxcopy.go', 'project.go'].map((f) => path.join(__dirname, '..', 'internal', 'tools', f));
const re = /^\t*checkParam\("dryRun", "[^"]*", true\),\n/gm;
let total = 0;
for (const f of files) {
  const src = fs.readFileSync(f, 'utf8');
  const hits = (src.match(re) || []).length;
  if (!hits) { console.log('（没有 dryRun 参数）' + path.basename(f)); continue; }
  fs.writeFileSync(f, src.replace(re, ''), 'utf8');
  total += hits;
  console.log('去掉 ' + hits + ' 处：' + path.basename(f));
}
console.log('共 ' + total + ' 处');
