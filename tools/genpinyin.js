'use strict';
/* 生成 internal/tools/pinyin_table.go —— 汉字读音表。
 *
 * 为什么是**生成**的而不是手写：表有 2 万条，手写必错，而且要让"表是从哪来的"可复现。
 * 数据源 = 本机已有的 **pinyin-pro**（同一份 SV 侧技能里已经在用的库，见
 * skills/sv-lyricist 里"拼音/声调来自 pinyin-pro"那句）。
 *
 * 用法（改了数据源或想重新生成时）：
 *   node tools/genpinyin.js "<pinyin-pro 包目录>"
 * 省略参数则用下面 DEFAULT_PP 那个默认路径。
 *
 * 产物形状（刻意压扁，见 pinyin.go 的说明）：
 *   U+4E00..U+9FFF **连续**，一个码位一项，读该码位的下标 = r - 0x4E00；
 *   项内多个读音用 `,` 分（**第一个 = pinyin-pro 给的首选**），无读音写空串。
 *   实测 123 KB —— 表本身很小，值得直接嵌进单文件。
 */
const fs = require('fs');
const path = require('path');

const DEFAULT_PP = 'C:/Users/<user>/Documents/<skills-dir>/dist/server-runtime/node_modules/pinyin-pro';
const ppDir = process.argv[2] || DEFAULT_PP;
const pp = require(ppDir);

const LO = 0x4e00, HI = 0x9fff;
const rows = [];
let blank = 0, poly = 0;
for (let cp = LO; cp <= HI; cp++) {
  const c = String.fromCodePoint(cp);
  // multiple = 全部读音；toneType=num = 声调用数字（轻声是 0）——比带调号好解析，声调号在 Go 侧再拼
  let arr = pp.pinyin(c, { multiple: true, toneType: 'num', type: 'array' }) || [];
  arr = arr.filter((x) => x && x !== c);
  if (!arr.length) { blank++; rows.push(''); continue; }
  if (arr.length > 1) poly++;
  rows.push(arr.join(','));
}
const blob = rows.join('\n');
console.log(`码位 ${LO.toString(16)}..${HI.toString(16)}  共 ${rows.length} 项  无读音 ${blank}  多音字 ${poly}`);
console.log(`读数数据 ${(Buffer.byteLength(blob, 'utf8') / 1024).toFixed(1)} KB`);

// 切成块，避免出现一条 150 KB 的巨行（编辑器/gofmt 都不喜欢）
const CHUNK = 1200;
const parts = [];
for (let i = 0; i < blob.length; i += CHUNK) parts.push(blob.slice(i, i + CHUNK));

const goStr = (s) => JSON.stringify(s);   // 用 JSON 转义：\n、\" 都会被正确转义
const out = `package tools

// 本文件由 tools/genpinyin.js **生成**，不要手改。
//   node tools/genpinyin.js "<pinyin-pro 包目录>"
//
// 数据源：pinyin-pro（${JSON.parse(fs.readFileSync(path.join(ppDir, 'package.json'), 'utf8')).version}）
//   —— 与 SV 侧技能用的是同一个库，拼音/声调口径一致。
//
// 形状：U+4E00..U+9FFF **连续**，一个码位一项，下标 = r - 0x4E00。
//   项内多个读音用 \`,\` 分，**第一个 = pinyin-pro 的首选读音**；没有读音的码位是空串。
//   ⚠️ 「首选」不等于「歌词里的读法」—— 最典型的是 了：pinyin-pro 首选 liao3，
//      而歌词里几乎总是 le（轻声）。这类见 pinyin.go 的 lyricReading 表。

// pinyinLo / pinyinHi 是这张表覆盖的码位范围（含两端）。
const (
	pinyinLo = 0x4E00
	pinyinHi = 0x9FFF
)

// pinyinChunks 拼起来就是整张表（分块只是为了别出现一条巨行）。
var pinyinChunks = [...]string{
${parts.map((p) => '\t' + goStr(p) + ',').join('\n')}
}
`;

const dst = path.join(__dirname, '..', 'internal', 'tools', 'pinyin_table.go');
fs.writeFileSync(dst, out, 'utf8');
console.log(`已写入 ${dst}  (${(fs.statSync(dst).size / 1024).toFixed(0)} KB)`);
