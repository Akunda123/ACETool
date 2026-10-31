/* 从 PNG 里**取准一个像素颜色**（用户给色块图让我们照抄时用）。
 *
 * 为什么不靠眼睛估：色号差一点就是另一个色，而且"差不多"没法复核。
 * Node 自带 `zlib` ⇒ 一个 40 行的 PNG 解码够用了（只需 8 位、非隔行）。
 *
 * 用法：node sample-png.js <图片路径> [x] [y]
 */
const fs = require('fs');
const zlib = require('zlib');

const file = process.argv[2];
const px = parseInt(process.argv[3] || '0', 10);
const py = parseInt(process.argv[4] || '0', 10);

const buf = fs.readFileSync(file);
if (buf.slice(0, 8).toString('hex') !== '89504e470d0a1a0a') {
  console.error('这不是 PNG（签名不对）');
  process.exit(1);
}

let off = 8, ihdr = null, idat = [], plte = null;
while (off < buf.length) {
  const len = buf.readUInt32BE(off);
  const type = buf.slice(off + 4, off + 8).toString('ascii');
  const data = buf.slice(off + 8, off + 8 + len);
  if (type === 'IHDR') {
    ihdr = {
      w: data.readUInt32BE(0), h: data.readUInt32BE(4),
      depth: data[8], color: data[9], interlace: data[12],
    };
  } else if (type === 'IDAT') idat.push(data);
  else if (type === 'PLTE') plte = data;
  else if (type === 'IEND') break;
  off += 12 + len;
}
if (!ihdr) { console.error('没有 IHDR'); process.exit(1); }
console.log('尺寸 ' + ihdr.w + '×' + ihdr.h + ' · 位深 ' + ihdr.depth + ' · colorType ' + ihdr.color +
  ' · 隔行 ' + ihdr.interlace);
if (ihdr.depth !== 8 || ihdr.interlace !== 0) { console.error('只支持 8 位非隔行 PNG'); process.exit(1); }

const CH = { 0: 1, 2: 3, 3: 1, 4: 2, 6: 4 }[ihdr.color];
if (!CH) { console.error('不认识的 colorType ' + ihdr.color); process.exit(1); }

const raw = zlib.inflateSync(Buffer.concat(idat));
const stride = ihdr.w * CH;
const out = Buffer.alloc(ihdr.h * stride);
const paeth = (a, b, c) => {
  const p = a + b - c, pa = Math.abs(p - a), pb = Math.abs(p - b), pc = Math.abs(p - c);
  return pa <= pb && pa <= pc ? a : (pb <= pc ? b : c);
};
let p = 0;
for (let y = 0; y < ihdr.h; y++) {
  const f = raw[p++];
  for (let x = 0; x < stride; x++) {
    const cur = raw[p + x];
    const a = x >= CH ? out[y * stride + x - CH] : 0;
    const b = y > 0 ? out[(y - 1) * stride + x] : 0;
    const c = (x >= CH && y > 0) ? out[(y - 1) * stride + x - CH] : 0;
    let v;
    switch (f) {
      case 0: v = cur; break;
      case 1: v = cur + a; break;
      case 2: v = cur + b; break;
      case 3: v = cur + ((a + b) >> 1); break;
      case 4: v = cur + paeth(a, b, c); break;
      default: console.error('未知过滤类型 ' + f); process.exit(1);
    }
    out[y * stride + x] = v & 0xff;
  }
  p += stride;
}

function at(x, y) {
  const i = y * stride + x * CH;
  let r = out[i], g = out[i + 1], b = out[i + 2];
  if (ihdr.color === 3 && plte) { const j = out[i] * 3; r = plte[j]; g = plte[j + 1]; b = plte[j + 2]; }
  if (ihdr.color === 0 || ihdr.color === 4) { g = r; b = r; }
  return '#' + [r, g, b].map((v) => v.toString(16).padStart(2, '0')).join('');
}

// 四个角 + 中心（确认它是纯色块而不是渐变）
const pts = [[0, 0], [ihdr.w - 1, 0], [0, ihdr.h - 1], [ihdr.w - 1, ihdr.h - 1],
  [Math.floor(ihdr.w / 2), Math.floor(ihdr.h / 2)]];
const cols = pts.map(([x, y]) => at(x, y));
pts.forEach(([x, y], i) => console.log('  (' + x + ',' + y + ') = ' + cols[i]));
console.log('是纯色块吗 = ' + (new Set(cols).size === 1 ? '是' : '否（有渐变/多色）'));
console.log('\n取 (' + px + ',' + py + ') = ' + at(px, py));
