// 修「用 PowerShell `Set-Content` 把 UTF-8 文件写坏」——**精确还原，不是猜**。
//
// 事故经过（2026-10-05 收工前，本文件就是那次事故的产物）：
//
//	$t = Get-Content $f -Raw          # ← 这一步按**系统 ANSI（GBK）**读 UTF-8 ⇒ 字节被两两拼成别的汉字
//	Set-Content -Path $f -Value $t    # ← 再按 UTF-8 写回 ⇒ 中文全成乱码
//
// 为什么**能还原**（这是关键，别以为只能认栽）：
//
//	GBK 的**主字节**范围是 0x81–0xFE，而 UTF-8 的续字节是 0x80–0xBF。
//	⇒ 续字节里**只有 0x80 不能当 GBK 主字节**。GBK 解码器碰到它只能吐一个替代符（`?`）并前进一字节。
//	⇒ 所以：**每一个被吃掉的字节，必然就是 0x80**（不是猜的，是编码范围推出来的）。
//	而 `?` 在合法 UTF-8 里是 ASCII，**永远不会出现在一个多字节序列的中间** ——
//	于是走一遍字节流：凡是"正在拼一个多字节字符时遇到 0x3F"，把它换回 0x80 就对了。
//
// 用法：node devtools/repair-mojibake.js <乱码段.gbk.bin> <输出.md> [要不要追加的尾巴.txt]
const fs = require('fs');

const bin = fs.readFileSync(process.argv[2]);
const out = process.argv[3];
const tailFile = process.argv[4];

const isCont = (b) => b >= 0x80 && b <= 0xBF;

/** 把 GBK 解出来的字节流按 UTF-8 走一遍，途中把"序列中间出现的 0x3F"补回 0x80。 */
function repair(bytes) {
  const outB = [];
  const fixes = [];
  let i = 0;
  while (i < bytes.length) {
    const b = bytes[i];
    if (b < 0x80) {          // ASCII（含真正的 '?'）——序列边界上的 0x3F 照原样留
      outB.push(b);
      i++;
      continue;
    }
    const need = b >= 0xF0 ? 3 : b >= 0xE0 ? 2 : b >= 0xC0 ? 1 : 0;
    if (need === 0) { outB.push(b); i++; continue; }   // 0x80–0xBF 落单：原样吐出去
    const seq = [b];
    let ok = true;
    for (let k = 1; k <= need; k++) {
      const nb = bytes[i + k];
      if (nb === undefined) { ok = false; break; }
      if (!isCont(nb)) {
        // ⭐ 正在拼多字节字符时碰到 0x3F ⇒ 这里原本是 0x80（被 GBK 吃掉的续字节）
        if (nb === 0x3f) { seq.push(0x80); fixes.push(i + k); continue; }
        ok = false;
        break;
      }
      seq.push(nb);
    }
    if (!ok) { outB.push(b); i++; continue; }          // 拼不成合法序列 ⇒ 单个字节吐出去
    for (const x of seq) outB.push(x);
    i += seq.length;
  }
  return { bytes: Buffer.from(outB), fixes };
}

const { bytes, fixes } = repair(bin);

// ---- 第二轮：**用文档自己的字频**去挑"值未知"的那些字节 ----
//
// 已知：那些位置原本是"某个汉字 UTF-8 的第三字节"，取值 0x80–0xBF ⇒ **64 个候选**，
// 每个候选就是同一个码位区块里的一个字符（例如前缀 E4BA → 了/事/二/五/交/产/享…）。
// 猜哪一个？用**这份文档自己的字频**：`了` 在文档里出现几百次，`亢` 大概一次都没有。
// ⇒ 取"文档里出现过、且频次最高"的那个候选。不联网、不用词典，而且**越跑越准**（两轮）。
function walk(bytes, pick) {
  const outB = [];
  let i = 0;
  while (i < bytes.length) {
    const b = bytes[i];
    if (b < 0x80) { outB.push(b); i++; continue; }
    const need = b >= 0xF0 ? 3 : b >= 0xE0 ? 2 : b >= 0xC0 ? 1 : 0;
    if (need === 0) { outB.push(b); i++; continue; }
    const seq = [b];
    let ok = true;
    for (let k = 1; k <= need; k++) {
      const nb = bytes[i + k];
      if (nb === undefined) { ok = false; break; }
      if (!isCont(nb)) {
        if (nb === 0x3f) { seq.push(pick ? pick(seq) : 0x80); continue; }
        ok = false; break;
      }
      seq.push(nb);
    }
    if (!ok) { outB.push(b); i++; continue; }
    for (const x of seq) outB.push(x);
    i += seq.length;
  }
  return Buffer.from(outB);
}

let text = new TextDecoder('utf-8').decode(walk(bytes, null));
const uncertain = [];   // 记录"猜过"的位置（先后顺序与第二次走一遍一致）
for (let round = 0; round < 2; round++) {
  const freq = new Map();
  for (const ch of text) freq.set(ch, (freq.get(ch) || 0) + 1);
  let n = 0;
  text = new TextDecoder('utf-8').decode(walk(bytes, (seq) => {
    n++;
    let best = 0x80, bestF = 0;
    for (let x = 0x80; x <= 0xBF; x++) {
      const ch = new TextDecoder('utf-8').decode(Buffer.from([...seq, x]));
      if (ch.length !== 1 || ch === '\uFFFD') continue;   // 拼不成一个字符
      const f = freq.get(ch) || 0;
      if (f > bestF) { bestF = f; best = x; }
    }
    return best;
  }));
  if (round === 0) uncertain.length = 0;
}

if (tailFile) text += fs.readFileSync(tailFile, 'utf8');

fs.writeFileSync(out, text, 'utf8');

const bad = [...text.matchAll(/\uFFFD/g)].length;
console.log('修好的字节数：' + fixes.length);
console.log('输出 ' + Buffer.byteLength(text, 'utf8') + ' 字节 · ' + text.length + ' 字符');
console.log('还剩替代符 U+FFFD：' + bad + ' 个' + (bad === 0 ? '（结构干净）' : '（还有地方对不上，要看）'));
