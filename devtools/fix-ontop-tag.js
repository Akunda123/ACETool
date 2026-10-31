// 给面板每个页面补上 `<script src="ontop.js"></script>`（幂等，可反复跑）。
//
// 为什么用脚本而不是手改 14 个文件：纯机械插入（`</body>` 之前），
// 手改一遍就是 14 次搬运。**每个页面都该有这一行**（除了老壳页 index.html ——
// 它那颗 📌 归 shortcuts.js，口径已对齐），由 `check-ui.js` 负责盯着别漏。
//
// 用法：node devtools/fix-ontop-tag.js
const fs = require('fs');
const path = require('path');

const A = path.join(__dirname, '..', 'internal', 'web', 'assets');
const SKIP = new Set(['index.html']); // 老壳页：📌 由 shortcuts.js 提供
const TAG = '  <script src="ontop.js"></script>\n';

let added = 0, already = 0;
for (const f of fs.readdirSync(A).filter((x) => x.endsWith('.html')).sort()) {
  const p = path.join(A, f);
  let s = fs.readFileSync(p, 'utf8');
  if (SKIP.has(f)) { console.log('  跳过 ' + f + '（老壳页）'); continue; }
  if (s.includes('src="ontop.js"')) { already++; console.log('  已有 ' + f); continue; }
  const i = s.lastIndexOf('</body>');
  if (i < 0) { console.log('  ⚠️ ' + f + ' 没有 </body>，跳过'); continue; }
  s = s.slice(0, i) + TAG + s.slice(i);
  fs.writeFileSync(p, s);
  added++;
  console.log('  补上 ' + f);
}
console.log('\n新加 ' + added + ' 个 · 本来就有 ' + already + ' 个');
