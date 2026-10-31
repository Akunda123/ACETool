// 看清 DSH 会话日志里到底存了什么（决定"能不能重放 HANDOVER 的历次改动"）。
const fs = require('fs');
const path = require('path');
const os = require('os');
const zlib = require('zlib');

const root = path.join(os.homedir(), '.dsh', 'sessions');
const files = [];
(function walk(d) {
  for (const e of fs.readdirSync(d, { withFileTypes: true })) {
    const p = path.join(d, e.name);
    if (e.isDirectory()) walk(p);
    else if (e.name.indexOf('session.v3.jsonl') === 0) files.push(p);
  }
})(root);
files.sort((a, b) => fs.statSync(b).mtime - fs.statSync(a).mtime);
console.log('日志 ' + files.length + ' 个（按时间倒序）');

const KEYS = ['HANDOVER', 'acetool', 'file_path', 'old_string', 'new_string', 'tool_use', 'tool_call', 'content'];

function uncompress(f) { return zlib.zstdDecompressSync(fs.readFileSync(f)).toString('utf8'); }

console.log('\n=== 最新的那个 ===');
const t0 = uncompress(files[0]);
console.log(files[0]);
console.log('  ' + t0.length + ' 字符 / ' + t0.split('\n').length + ' 行');
for (const k of KEYS) console.log('  ' + k + ': ' + (t0.split(k).length - 1));
console.log('  前 900 字：\n' + t0.slice(0, 900));

console.log('\n=== 全部日志里的命中数 ===');
let total = {};
for (const f of files) {
  let t;
  try { t = uncompress(f); } catch (e) { continue; }
  const c = {};
  for (const k of KEYS) c[k] = t.split(k).length - 1;
  if (c.HANDOVER || c.acetool) {
    console.log('  ' + path.basename(path.dirname(f)) + '  ' + fs.statSync(f).mtime.toISOString().slice(0, 16)
      + '  HANDOVER=' + c.HANDOVER + ' acetool=' + c.acetool + ' file_path=' + c.file_path + ' old_string=' + c.old_string);
  }
  for (const k of KEYS) total[k] = (total[k] || 0) + c[k];
}
console.log('\n合计：' + JSON.stringify(total));
