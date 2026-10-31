/* 谱面编辑器（简谱 + 五线谱）—— ACETool 面板的专用 UI。
 *
 * 用户 2026-10-05 定的路线：
 *     简谱文本 / 谱面编辑器  ⇄  音符块(Element)  ⇄  MusicXML  ⇄  { ACE | 打谱软件 }
 *
 * ⭐ 两条必须记住的约定：
 *   ① **一个元素格子 = ACE 里一个音符**。`-` 连音格 ⇒ ACE 里**歌词为 `-` 的音符**（tenuto）。
 *      所以编辑器与 ACE **一一对应、完美往返**，不需要「合并长音」这类猜测。
 *   ② 时值 ≥ 2 拍的**单个音符**（ACE 里 `dur` 更长的那一个音符）在简谱上按传统画成
 *      「数字 + 浅色 `-`」—— 浅色是**同一个音符内的延长**，黑色 `-` 才是连音格。两者都清楚标了。
 *
 * 渲染全部手写 SVG（零前端依赖、无构建步骤）：
 *   * 简谱：数字/八度点/下划线/附点 —— 都是直线和文字，画起来直接。
 *   * 五线谱：谱号/音符头/变音号/休止符/时值数字用 **Bravura 字形轮廓**
 *     （`notation-glyphs.js`，SIL OFL；从 VexFlow 4 的字体数据里抽出来的十几个字形）。
 *     VexFlow 5 起没有 UMD 包、`build/cjs/*` 不能 `<script>` 直引，而本项目**不引入构建步骤**，
 *     所以只嵌这十几 KB 的字形，而不是塞 1.1 MB 的库。
 *   * 两排谱**共用同一列布局**（`layout()`），所以上下对齐、点哪个音就选中哪个音。
 */
(function () {
  'use strict';

  /* ============================ 常量与工具 ============================ */

  var SPACE = 11;          // 五线谱一个间距（px）
  var UPM = 360;           // 字形坐标每「一个间距」的单位数（Bravura 实测：声明包围盒按 250/间距、路径按 360/间距）
  var X0 = 104;            // 两排谱共用的起始 x（左边留给谱号/调号/拍号 + 简谱的调式说明）
  var J_BASE = 46;         // 简谱数字中线
  var S_TOP = 112;         // 五线谱最上面一条线
  var S_BOT = S_TOP + 4 * SPACE;
  var LYRIC_Y = 196;
  var SVG_H = 214;
  var MIDDLE = 34;         // 中线（B4）：E4 = 30，每半间距一步

  var LETTER_SEMI = [0, 2, 4, 5, 7, 9, 11];              // C D E F G A B
  var KEY_NAMES = ['C', 'C♯/D♭', 'D', 'E♭', 'E', 'F', 'F♯/G♭', 'G', 'A♭', 'A', 'B♭', 'B'];
  var SHARP_ORDER = [38, 35, 39, 36, 33, 37, 34];        // F C G D A E B 的字母步（E4=30）
  var FLAT_ORDER = [34, 37, 33, 36, 32, 35, 31];         // B E A D G C F

  /**
   * 调式的 `<fifths>`（正=升号个数、负=降号个数）。**必须与 Go 侧 `fifthsOf` 逐档一致**。
   *
   * ⚠️ 只有 pc=1 / pc=6 是两可的（C#≡Db、F#≡Gb），面板与「ACE → MusicXML」那条路一样
   * **总是走升号侧**（用户的记法表全是升号：`1=#C`、`1=#F`…）。
   * ⛔ 但升号侧**只管这两档** —— F 大调（pc=5）就是 1 个降号，
   * 曾经把它也当成"升号侧查不到就 0"，等于把 F 大调写成 C 大调。
   */
  function fifthsOf(pc) {
    var sharpOnly = { 0: 0, 7: 1, 2: 2, 9: 3, 4: 4, 11: 5 };
    var flatOnly = { 5: -1, 10: -2, 3: -3, 8: -4 };
    var twoWaySharp = { 1: 7, 6: 6 };
    if (sharpOnly[pc] != null) return sharpOnly[pc];
    if (flatOnly[pc] != null) return flatOnly[pc];
    if (twoWaySharp[pc] != null) return twoWaySharp[pc];
    return 0;
  }

  /**
   * 由 `<fifths>` 反推主音字母（0=C … 6=B）。五度圈的规律就是这一行：
   * 每加 1 个升号，主音上行五度（字母 +4 mod 7）⇒ 黑键主音（C#/Db）也能落到对的字母上。
   */
  function tonicLetterOf(fifths) { return ((4 * fifths) % 7 + 7) % 7; }

  /** 示例 = 一份 1-7 音阶（每个音一拍，末音高八度）。 */
  var DEMO = [
    { kind: 'note', pitch: 1, octave: 0, duration: 4, lyric: 'do' },
    { kind: 'note', pitch: 2, octave: 0, duration: 4, lyric: 're' },
    { kind: 'note', pitch: 3, octave: 0, duration: 4, lyric: 'mi' },
    { kind: 'note', pitch: 4, octave: 0, duration: 4, lyric: 'fa' },
    { kind: 'note', pitch: 5, octave: 0, duration: 4, lyric: 'sol' },
    { kind: 'note', pitch: 6, octave: 0, duration: 4, lyric: 'la' },
    { kind: 'note', pitch: 7, octave: 0, duration: 4, lyric: 'si' },
    { kind: 'note', pitch: 1, octave: 1, duration: 4, lyric: 'do' },
  ];

  function esc2(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }
  function num(v, d) { var n = parseFloat(v); return isFinite(n) ? n : d; }
  function clamp(v, a, b) { return Math.max(a, Math.min(b, v)); }

  /* ============================ 元素模型 ============================ */

  function norm(e) {
    var o = {
      kind: e && e.kind === 'sustain' ? 'sustain' : (e && e.kind === 'barline' ? 'barline' : (e && e.kind === 'rest' ? 'rest' : 'note')),
      duration: Math.max(1, Math.round(num(e && e.duration, 4))),
      dots: clamp(Math.round(num(e && e.dots, 0)), 0, 2),
      pitch: clamp(Math.round(num(e && e.pitch, 1)), 0, 9),
      octave: clamp(Math.round(num(e && e.octave, 0)), -3, 3),
      accidental: (e && (e.accidental === 'sharp' || e.accidental === 'flat')) ? e.accidental : '',
      lyric: (e && e.lyric) || '',
      code: (e && e.code) || '',
    };
    if (e && (e.rest === true || e.kind === 'rest')) { o.kind = 'rest'; o.pitch = 0; }
    return o;
  }

  /** 元素拍数（四分音符 = 1 拍）。 */
  function beats(e) {
    var b = 4 / (e.duration || 4);
    if (e.dots > 0) b *= (2 - Math.pow(2, -e.dots));
    return b;
  }
  function ticks(e) { return Math.round(beats(e) * 480); }

/** 拍 = tick / 480（⚠️ 单位口径 2026-10-14：对外一律「拍」，tick 只进内部计算）。 */
function beatOf(tick) {
  if (typeof tick !== 'number') return tick;
  return String(Math.round((tick / 480) * 100) / 100);
}
  function isRest(e) { return e.kind === 'rest' || (!e.kind || e.kind === 'note') && !e.pitch && e.kind !== 'sustain'; }
  function isSustain(e) { return e.kind === 'sustain'; }

  /** 度数（1..7）→ 相对主音的半音数。**必须与 Go 侧 jianpu.DegreeSemitone 一致**。 */
  function degSemi(d) { return LETTER_SEMI[((d - 1) % 7 + 7) % 7]; }

  /** 元素的绝对 MIDI（休止/延音返回 -1）。**必须与 Go 侧换算一致**。 */
  function midiOf(e, key) {
    if (isSustain(e) || isRest(e) || !e.pitch) return -1;
    var m = 60 + key + degSemi(e.pitch) + 12 * (e.octave || 0);
    if (e.accidental === 'sharp') m++;
    else if (e.accidental === 'flat') m--;
    return m;
  }

  /** 字母步（E4 = 30，每半间距一步）—— 决定五线谱上的位置。 */
  function diatonicOf(e, key) {
    var tonic = 28 + tonicLetterOf(fifthsOf(key));        // C4 = 28
    return tonic + (e.pitch - 1) + 7 * (e.octave || 0);
  }
  function letterOctave(d) {
    var letter = ((d % 7) + 7) % 7;
    var oct = Math.floor(d / 7);
    return { letter: letter, oct: oct, natural: 12 * (oct + 1) + LETTER_SEMI[letter] };
  }

  /** 调号里有几个升/降（返回 {n, sharp}）。与 Go 的 `fifthsOf` 同源。 */
  function keySig(key) {
    var f = fifthsOf(key);
    return f >= 0 ? { n: f, sharp: true } : { n: -f, sharp: false };
  }

  /* ============================ 列布局（两排谱共用） ============================ */

  function layout(els) {
    var cols = [], x = 0;
    for (var i = 0; i < els.length; i++) {
      var e = els[i];
      var w = wForBeats(beats(e), isSustain(e));
      cols.push({ i: i, e: e, x: x, w: w, cx: x + w / 2 });
      x += w;
    }
    return { cols: cols, total: x };
  }

  /** 一列的宽度（拍数 → px）。`layout()` 与"按小节线拆开"之后的碎片列**共用这一处**。 */
  function wForBeats(b, isSus) {
    var w = clamp(28 + 13 * Math.log2(b * 4), 36, 118);
    return isSus ? clamp(w, 36, 44) : w;
  }

  /**
   * 谱面上**写得出来**的时值块（分母 + 附点），从大到小。
   *
   * ⭐ 用途：用户 2026-10-07「按小节线拆开」—— 一个音跨过小节线时，要把它拆成"这一小节剩下的"
   * 与"下一小节接着的"两截，**两截各自得是谱面上写得出来的时值**（不能写"1.3 拍"）。
   * 贪心用最大的块、剩下继续拆 ⇒ 总和与原来的拍数**精确一致**（1/64 音符 = 0.0625 拍为最小单位）。
   */
  var NOTE_CHUNKS = (function () {
    var out = [];
    [1, 2, 4, 8, 16, 32, 64].forEach(function (d) {
      [0, 1, 2, 3].forEach(function (dots) {
        out.push({ d: d, dots: dots, beats: (4 / d) * (2 - Math.pow(2, -dots)) });
      });
    });
    out.sort(function (a, b) { return b.beats - a.beats; });
    return out;
  })();

  function splitBeats(beats) {
    var out = [], rem = beats;
    for (var guard = 0; guard < 24 && rem > 1e-6; guard++) {
      var picked = null;
      for (var i = 0; i < NOTE_CHUNKS.length; i++) {
        if (NOTE_CHUNKS[i].beats <= rem + 1e-6) { picked = NOTE_CHUNKS[i]; break; }
      }
      if (!picked) {                              // 比 1/64 音符还小 ⇒ 并进上一块（如实记在心里：不静默）
        break;
      }
      out.push(picked);
      rem -= picked.beats;
    }
    return out;
  }

  // ---- 列上的"有效时值"：拆出来的碎片列会带覆盖值，渲染器一律走这三个取值 ----
  function colDur(col) { return col && col.dur ? col.dur : col.e.duration; }
  function colDots(col) { return (col && col.dots != null) ? col.dots : (col.e.dots || 0); }
  function colBeats(col) { return (col && col.beats != null) ? col.beats : beats(col.e); }

  /* ============================ 字形 ============================ */

  /** 字形路径：`translate` 到目标原点 + 按 space/360 缩放（y 已经在生成时翻好）。 */
  function glyph(name, x, y, s) {
    var g = GLYPHS[name];
    if (!g) return '';
    return '<path d="' + g.d + '" transform="translate(' + r2(x) + ' ' + r2(y) + ') scale(' + r2(s) + ')"/>';
  }
  function bbOf(name) { var g = GLYPHS[name]; return g ? g.bb : [0, 0, 0, 0]; }
  function r2(v) { return Math.round(v * 100) / 100; }

  /** 把一个字形放在 (left, 垂直中心 cy)：按包围盒对齐。 */
  function glyphLeftCenter(name, left, cy, space) {
    var bb = bbOf(name), s = space / UPM;
    var y = cy - s * (bb[1] + bb[3]) / 2;
    return glyph(name, left - s * bb[0], y, s);
  }
  /** 把一个字形放在 (水平中心 cx, 垂直中心 cy)。 */
  function glyphCenter(name, cx, cy, space) {
    var bb = bbOf(name), s = space / UPM;
    return glyphLeftCenter(name, cx - s * (bb[2] - bb[0]) / 2, cy, space);
  }
  /** 原点是字形自己的原点（谱号/旗子/休止符按约定对齐时用）。 */
  function glyphOrigin(name, x, y, space) { return glyph(name, x, y, space / UPM); }

  /* ============================ 简谱渲染 ============================ */

  function renderJianpu(els, cols, opt) {
    var key = opt.key, parts = [];
    parts.push('<g class="nt-jp">');
    // 调式 / 拍号 说明（放在左边那条留白里）——**只有第一行画**（导出多行谱时 `opt.label=false`）
    if (opt.label !== false) {
      parts.push('<text class="nt-leg" x="4" y="' + (J_BASE - 12) + '">1 = ' + esc2(KEY_NAMES[key]) + '</text>');
      parts.push('<text class="nt-leg" x="4" y="' + (J_BASE + 12) + '">' + opt.meter.n + '/' + opt.meter.d + '</text>');
    } else if (opt.measureNo) {
      // 后面每一行开头标一下小节号（简谱的习惯，找位置用）
      parts.push('<text class="nt-leg" x="4" y="' + (J_BASE + 4) + '">[' + opt.measureNo + ']</text>');
    }

    // ⚠️ `opt.beatAt` = 本行第一列**之前**已经走了多少拍（导出时按小节切行 ⇒ 小节线要接着上一行算）
    var acc = opt.beatAt || 0, per = 4 * opt.meter.n / opt.meter.d;
    for (var c = 0; c < cols.length; c++) {
      var col = cols[c], e = col.e, x = X0 + col.x, cx = X0 + col.cx;
      if (col.i === opt.sel) {
        parts.push('<rect class="nt-selrect" x="' + r2(x + 1) + '" y="18" width="' + r2(col.w - 2) + '" height="' + (LYRIC_Y - 30) + '" rx="4"/>');
      }
      parts.push('<g data-i="' + c + '" class="nt-hit">');
      parts.push('<rect class="nt-hitrect" x="' + r2(x) + '" y="14" width="' + r2(col.w) + '" height="' + (LYRIC_Y - 22) + '"/>');

      if (isSustain(e) || col.cont) {
        // 连音格（`-`）；**按小节线拆开**的后半截也走这里（简谱里它就是 `-`）
        parts.push('<text class="nt-dash" x="' + r2(cx) + '" y="' + J_BASE + '" text-anchor="middle">-</text>');
      } else if (isRest(e)) {
        parts.push('<text class="nt-num" x="' + r2(cx) + '" y="' + J_BASE + '" text-anchor="middle">0</text>');
      } else {
        if (e.accidental) {
          parts.push('<text class="nt-acc" x="' + r2(cx - 13) + '" y="' + J_BASE + '" text-anchor="middle">' +
            (e.accidental === 'sharp' ? '♯' : '♭') + '</text>');
        }
        parts.push('<text class="nt-num" x="' + r2(cx) + '" y="' + J_BASE + '" text-anchor="middle">' + e.pitch + '</text>');
        // 八度点：正数在上、负数在下，一个点一个八度
        for (var o = 0; o < Math.abs(e.octave || 0); o++) {
          var oy = e.octave > 0 ? J_BASE - 15 - o * 6 : J_BASE + 13 + o * 6;
          parts.push('<circle class="nt-dot" cx="' + r2(cx) + '" cy="' + oy + '" r="1.7"/>');
        }
        // 附点
        if (colDots(col) > 0) {
          for (var d = 0; d < colDots(col); d++) {
            parts.push('<circle class="nt-dot" cx="' + r2(cx + 12 + d * 6) + '" cy="' + (J_BASE - 5) + '" r="1.9"/>');
          }
        }
        // 减时线（下划线）：8 分 1 条、16 分 2 条、32 分 3 条
        var dd0 = colDur(col);
        var nLines = dd0 >= 32 ? 3 : dd0 >= 16 ? 2 : dd0 >= 8 ? 1 : 0;
        for (var L = 0; L < nLines; L++) {
          parts.push('<line class="nt-under" x1="' + r2(cx - 9) + '" y1="' + (J_BASE + 7 + L * 4) + '" x2="' + r2(cx + 9) + '" y2="' + (J_BASE + 7 + L * 4) + '"/>');
        }
        // 音内延长：≥2 拍画浅色 `-`（**同一个 ACE 音符**，不是连音格）
        var extra = Math.round(colBeats(col)) - 1;
        for (var k = 0; k < extra; k++) {
          parts.push('<text class="nt-hold" x="' + r2(cx + 15 + k * 15) + '" y="' + J_BASE + '" text-anchor="middle">-</text>');
        }
      }
      if (e.lyric && !opt.hideLyrics) {
        parts.push('<text class="nt-lyric" x="' + r2(cx) + '" y="' + LYRIC_Y + '" text-anchor="middle">' + esc2(e.lyric) + '</text>');
      }
      parts.push('</g>');

      // 小节线：按拍号累计，落在小节头（浅色虚线）；显式 barline 元素则画实线
      acc += colBeats(col);
      if (c < cols.length - 1 && acc >= per - 1e-6) {
        acc -= per;
        parts.push('<line class="nt-bar" x1="' + r2(X0 + col.x + col.w) + '" y1="' + (J_BASE - 18) + '" x2="' + r2(X0 + col.x + col.w) + '" y2="' + (LYRIC_Y - 8) + '"/>');
      }
    }
    // 末尾追加格（**导出时不画** —— 那是编辑器的交互件）
    var endX = X0 + (cols.length ? cols[cols.length - 1].x + cols[cols.length - 1].w : 0);
    if (opt.append !== false) {
      parts.push(appendZone(endX, J_BASE + 6));
    }
    parts.push('</g>');
    return parts.join('');
  }

  /** 末尾的追加格 `+`。`cy` = 视觉中心（五线谱传中线，简谱传数字基线附近）。 */
  function appendZone(x, cy, h, top) {
    var y0 = (top != null) ? top - 12 : 14;
    var hh = (h != null) ? h + 8 : (LYRIC_Y - 22);
    return '<g data-i="append" class="nt-hit nt-append">' +
      '<rect class="nt-hitrect" x="' + r2(x + 2) + '" y="' + r2(y0) + '" width="34" height="' + r2(hh) + '"/>' +
      '<text class="nt-plus" x="' + r2(x + 19) + '" y="' + r2(cy + 7) + '" text-anchor="middle">+</text></g>';
  }

  /* ============================ 五线谱渲染 ============================ */

  function yOfStaff(d, dy) { return S_BOT + (dy || 0) - (d - 30) * SPACE / 2; }
  function noteY(e, key, dy) { return yOfStaff(diatonicOf(e, key), dy); }
  function headWidth(e) { return (e.duration >= 1 ? 1.18 : 1.18) * SPACE; }

  /**
   * 五线谱的**垂直让位**：音域超出五线 + 一个间距时，整排谱上下平移，别把加线压到歌词上。
   *
   * 为什么要它：真谱子里低音能到 C3（`1` 低一个八度），加线会一路画到画布外面、
   * 还会和歌词重叠 —— 这是第一版实拍截图里一眼看到的毛病（`render-check.html`）。
   * 现在按实际音域算偏移：向下最多让到能容下最低的加线，向上同理。
   */
  function staffGeom(cols, key) {
    var lo = 30, hi = 38, prev = null;
    for (var i = 0; i < cols.length; i++) {
      var e = cols[i].e;
      if (isSustain(e)) continue;
      if (isRest(e)) { prev = null; continue; }
      var d = diatonicOf(e, key);
      if (d < lo) lo = d;
      if (d > hi) hi = d;
      prev = d;
    }
    var dy = 0;
    if (lo < 28) dy += (28 - lo) * SPACE / 2;      // 往下挪，给低音的加线留地方
    if (hi > 40) dy -= (hi - 40) * SPACE / 2;      // 往上挪，给高音的加线留地方
    var down = Math.max(0, dy);
    return { dy: dy, lyricY: LYRIC_Y + down + 6, height: SVG_H + down + 14 };
  }

  /**
   * 休止符字形。⚠️ `dur` 是**时值代号**：1=全、2=二分、4=四分、8=八分、16=十六分、32=三十二分
   * —— 代号**越小音符越长**，所以判据一律是 `<=`（2026-10-08 修：原来写成 `>=`，
   * 结果**每个音符都被画成全音符**（空心的圈）、每个休止都画成全休止）。
   */
  function restName(dur) {
    if (dur <= 1) return 'restWhole';
    if (dur <= 2) return 'restHalf';
    if (dur <= 4) return 'restQuarter';
    if (dur <= 8) return 'rest8th';
    if (dur <= 16) return 'rest16th';
    return 'rest32nd';
  }
  /** 符头字形。判据同 `restName`（代号小 = 音长）。 */
  function headName(dur) {
    if (dur <= 1) return 'noteheadWhole';
    if (dur <= 2) return 'noteheadHalf';
    return 'noteheadBlack';
  }

  function renderStaff(els, cols, opt) {
    var key = opt.key, parts = [], ks = keySig(key);
    var dy = opt.dy || 0, lyricY = opt.lyricY || LYRIC_Y;
    var top = S_TOP + dy, bot = S_BOT + dy;
    function Y(d) { return yOfStaff(d, dy); }
    parts.push('<g class="nt-staff">');
    // 五条线
    var lineEnd = X0 + (cols.length ? cols[cols.length - 1].x + cols[cols.length - 1].w + 40 : 40);
    for (var k = 0; k <= 4; k++) {
      var y = top + k * SPACE;
      parts.push('<line class="nt-line" x1="' + X0 + '" y1="' + y + '" x2="' + lineEnd + '" y2="' + y + '"/>');
    }
    // 谱号（按包围盒对齐：底边落到底线下方 2 个间距）
    var gbb = bbOf('gClef'), gs = SPACE / UPM;
    parts.push('<g class="nt-clef">' + glyph('gClef', 8, bot + 2 * SPACE - gs * gbb[3], gs) + '</g>');
    // 调号
    var order = ks.sharp ? SHARP_ORDER : FLAT_ORDER, sigName = ks.sharp ? 'accidentalSharp' : 'accidentalFlat';
    for (var s = 0; s < ks.n; s++) {
      parts.push('<g class="nt-keysig">' + glyphCenter(sigName, 62 + s * 9.5, Y(order[s]), SPACE) + '</g>');
    }
    // 拍号（分子在上两个间、分母在下两个间）
    var tx = 62 + (ks.n ? ks.n * 9.5 + 6 : 0);
    parts.push('<g class="nt-timesig">' + glyphCenter('timeSig' + opt.meter.n, tx + 10, bot - 3 * SPACE, SPACE) +
      glyphCenter('timeSig' + opt.meter.d, tx + 10, bot - SPACE, SPACE) + '</g>');
    // 每行开头标小节号（导出多行谱时给；放在谱号上方）
    if (opt.measureNo) {
      parts.push('<text class="nt-leg" x="6" y="' + r2(top - 8) + '">[' + opt.measureNo + ']</text>');
    }

    var acc = opt.beatAt || 0, per = 4 * opt.meter.n / opt.meter.d;
    for (var c = 0; c < cols.length; c++) {
      var col = cols[c], e = col.e, x = X0 + col.x, cx = X0 + col.cx;
      if (col.i === opt.sel) {
        parts.push('<rect class="nt-selrect" x="' + r2(x + 1) + '" y="' + r2(top - 14) + '" width="' + r2(col.w - 2) + '" height="' + r2(lyricY - top + 4) + '" rx="4"/>');
      }
      parts.push('<g data-i="' + c + '" class="nt-hit">');
      parts.push('<rect class="nt-hitrect" x="' + r2(x) + '" y="' + r2(top - 12) + '" width="' + r2(col.w) + '" height="' + r2(lyricY - top + 8) + '"/>');

      if (isRest(e)) {
        var rn = restName(colDur(col)), rbb = bbOf(rn), rs = SPACE / UPM, ry;
        if (rn === 'restWhole') ry = bot - 3 * SPACE - rs * rbb[1];
        else if (rn === 'restHalf') ry = bot - 2 * SPACE - rs * rbb[3];
        else ry = bot - 2 * SPACE - rs * (rbb[1] + rbb[3]) / 2;
        parts.push('<g class="nt-rest">' + glyph(rn, cx - rs * (rbb[2] - rbb[0]) / 2, ry, rs) + '</g>');
      } else {
        // 延音格 / **按小节线拆开的后半截**：画在前一个音的音高上，并拉一条延音线接上
        var pitchEl = e;
        if (isSustain(e) || col.cont) {
          pitchEl = col.cont ? e : null;      // 拆出来的后半截：音高就是它自己
          if (!pitchEl) {
            // 往回找"能提供音高"的那一列：真正是音符的（`-` 元素自己没有音高；**拆出来的后半截算**）
            for (var p = c - 1; p >= 0; p--) {
              if (!isSustain(cols[p].e) && !isRest(cols[p].e)) { pitchEl = cols[p].e; break; }
            }
          }
          if (pitchEl && c > 0) {
            var px = X0 + cols[c - 1].x + headWidth(cols[c - 1].e) / 2 + headWidth(cols[c - 1].e) / 2;
            var py = noteY(pitchEl, key, dy), nx = x + 3;
            parts.push('<path class="nt-tie" d="M' + r2(px - 2) + ' ' + r2(py - 3) + ' Q' + r2((px + nx) / 2) + ' ' + r2(py + 8) + ' ' + r2(nx + 3) + ' ' + r2(py - 3) + '"/>');
          } else if (pitchEl && opt.carry) {
            // ⭐ **跨行连音**（真谱写法）：这一行开头是上一行那个音的延续（`-` 或**拆出来的后半截**）⇒
            //    把这个音**重新写一遍**（同音高），并从**左边那条边**拉一条连音线接上。
            var cy2 = Y(diatonicOf(pitchEl, key));
            parts.push('<path class="nt-tie" d="M' + r2(X0 - 30) + ' ' + r2(cy2 - 3) + ' Q' + r2((X0 - 30 + cx) / 2) +
              ' ' + r2(cy2 + 8) + ' ' + r2(cx - headWidth(pitchEl) / 2 - 2) + ' ' + r2(cy2 - 3) + '"/>');
          }
        }
        if (!pitchEl) pitchEl = opt.carry || { pitch: 1, octave: 0, accidental: '' };
        var d0 = diatonicOf(pitchEl, key), ny0 = Y(d0), hw = headWidth(e);
        var dEff = colDur(col);
        // 加线：低于 E4(30) 或高于 F5(38) 时每隔一个音级画一条
        var led = [], t1, t2;
        if (d0 <= 28) { for (t1 = 28; t1 >= d0; t1 -= 2) led.push(t1); }
        if (d0 >= 40) { for (t2 = 40; t2 <= d0; t2 += 2) led.push(t2); }
        for (var li = 0; li < led.length; li++) {
          var ly = Y(led[li]);
          parts.push('<line class="nt-ledger" x1="' + r2(cx - 12) + '" y1="' + r2(ly) + '" x2="' + r2(cx + 12) + '" y2="' + r2(ly) + '"/>');
        }
        // 真变音号：跟调号比对，只有「读谱会读错」时才画（含还原号）
        var accSym = accidentalToDraw(pitchEl, key, ks);
        var headX = cx - hw / 2;
        // 符干 + 旗子
        if (dEff >= 2) {
          var up = d0 < MIDDLE;
          var stemX = up ? headX + hw : headX;
          var stemEnd = up ? ny0 - 3.5 * SPACE : ny0 + 3.5 * SPACE;
          parts.push('<line class="nt-stem" x1="' + r2(stemX) + '" y1="' + r2(ny0) + '" x2="' + r2(stemX) + '" y2="' + r2(stemEnd) + '"/>');
          if (dEff >= 8) {
            var fn = (dEff >= 32 ? 'flag32nd' : dEff >= 16 ? 'flag16th' : 'flag8th') + (up ? 'Up' : 'Down');
            parts.push('<g class="nt-flag">' + glyphOrigin(fn, stemX, stemEnd, SPACE) + '</g>');
          }
        }
        if (accSym) {
          parts.push('<g class="nt-accglyph">' + glyphLeftCenter(accSym, headX - 13 - bbW(accSym), ny0, SPACE) + '</g>');
        }
        parts.push('<g class="nt-head">' + glyphLeftCenter(headName(dEff), headX, ny0, SPACE) + '</g>');
        if (colDots(col) > 0) {
          for (var dd = 0; dd < colDots(col); dd++) {
            parts.push('<circle class="nt-augdot" cx="' + r2(headX + hw + 5 + dd * 7) + '" cy="' + r2(ny0 - SPACE / 2) + '" r="' + r2(SPACE * 0.2) + '"/>');
          }
        }
      }
      if (e.lyric && !opt.hideLyrics) {
        // ⚠️ 拆开的碎片上**不重复写歌词**（一个字只写在该音的第一截上）
        if (!col.cont) {
          parts.push('<text class="nt-lyric" x="' + r2(cx) + '" y="' + r2(lyricY) + '" text-anchor="middle">' + esc2(e.lyric) + '</text>');
        }
      }
      parts.push('</g>');

      acc += colBeats(col);
      if (c < cols.length - 1 && acc >= per - 1e-6) {
        acc -= per;
        var bx = X0 + col.x + col.w;
        parts.push('<line class="nt-barline" x1="' + r2(bx) + '" y1="' + top + '" x2="' + r2(bx) + '" y2="' + bot + '"/>');
      }
    }
    var endX = X0 + (cols.length ? cols[cols.length - 1].x + cols[cols.length - 1].w : 0);
    // ⭐ **跨行连音（甩出去那半）**：本行末尾那个音的后面还是同一个音的 `-` ⇒ 从它符头往右甩一小段
    //    连音线；下一行开头会把这个音重写一遍并接上（真谱就是这么画的）。
    if (opt.leaveTie && cols.length) {
      var lc = cols[cols.length - 1], lp = lc.e;
      if (!isSustain(lp) && !isRest(lp)) {        var lpy = Y(diatonicOf(lp, key));
        var sx = X0 + lc.x + lc.w / 2 + headWidth(lp) / 2;
        parts.push('<path class="nt-tie" d="M' + r2(sx - 2) + ' ' + r2(lpy - 3) + ' Q' + r2((sx + endX + 34) / 2) +
          ' ' + r2(lpy + 8) + ' ' + r2(endX + 32) + ' ' + r2(lpy - 3) + '"/>');
      }
    }
    if (opt.append !== false) {
      parts.push(appendZone(endX, bot - 2 * SPACE, lyricY - top, top));
    }
    // 每行末尾一条结束小节线（导出多行谱时每行都要收口）
    parts.push('<line class="nt-barline" x1="' + r2(endX + 40) + '" y1="' + top + '" x2="' + r2(endX + 40) + '" y2="' + bot + '"/>');
    parts.push('</g>');
    return parts.join('');
  }

  function bbW(name) { var bb = bbOf(name); return bb[2] - bb[0]; }

  /**
   * 该画哪个变音号（'' 表示不画）。
   * 判据：谱面上「字母 + 调号」读出来的音高，与目标音高不一致时必须写一个记号。
   * 所以 F 大调里 `#4` 要画的是**还原号**（调号已经给了降号），而不是升号。
   */
  function accidentalToDraw(e, key, ks) {
    var midi = midiOf(e, key);
    if (midi < 0) return '';
    var lo = letterOctave(diatonicOf(e, key));
    var implied = 0;
    if (ks.n > 0) {
      var order = ks.sharp ? SHARP_ORDER : FLAT_ORDER;
      for (var i = 0; i < ks.n; i++) {
        var lo2 = letterOctave(order[i]);
        if (lo2.letter === lo.letter) implied = ks.sharp ? 1 : -1;
      }
    }
    var alt = midi - lo.natural;
    if (alt === implied) return '';
    if (alt === 0) return 'accidentalNatural';
    if (alt === 1) return 'accidentalSharp';
    if (alt === -1) return 'accidentalFlat';
    return alt > 1 ? 'accidentalSharp' : 'accidentalFlat';
  }

  /* ============================ 面板 HTML ============================ */

  var STYLE_ID = 'nt-style';
  function ensureStyle() {
    if (document.getElementById(STYLE_ID)) return;
    var css = [
      '.nt-frame{border:1px solid var(--line,#2a3140);border-radius:8px;margin:10px 0;background:var(--panel,#161b24)}',
      '.nt-bar,.nt-tools,.nt-actions,.nt-gen,.nt-actions2{display:flex;flex-wrap:wrap;gap:6px;align-items:center;padding:8px;border-bottom:1px solid var(--line,#2a3140)}',
      '.nt-actions{border-bottom:0;border-top:1px solid var(--line,#2a3140);display:block;padding:0}',
      // ⭐ 生成块（谱 → 音符）：单独一段、有标题，视觉上"在 nt 块下面"
      '.nt-gen{background:var(--inset,#111722);border-bottom:0}',
      '.nt-gen b{font-size:13px}',
      '.nt-actions2{border-bottom:0;padding-top:0}',
      '.nt-gen label,.nt-actions2 label{font-size:12px;opacity:.75;margin-right:2px}',
      '.nt-gen select,.nt-actions2 select{background:#0f131a;color:var(--fg,#d6dae0);border:1px solid var(--line,#2a3140);border-radius:5px;padding:3px 6px;font:inherit;font-size:12px}',
      // ⭐ 面板里的下拉**按内容宽**（宿主 `style.css` 给 select 定了 `min-width:180px`，
      //    三个下拉一排就是 540px ⇒「导出」那一行被挤到换行；面板自己把它压回来）
      '.nt-frame select,.nt-pv select{min-width:0;width:auto;max-width:100%}',
      '.nt-bar label,.nt-tools label{font-size:12px;opacity:.75;margin-right:2px}',
      // ⭐「重新优化一下 ui」（2026-10-08）：屏幕上只留**一颗**实心的红按钮（生成块里那颗）。
      //    顶栏那颗是"顺手也能写"的快捷入口 ⇒ 改成描边红，别跟主操作抢眼睛。
      '.nt-bar .btn.danger{background:transparent;color:var(--bad,#e06c75)}',
      '.nt-scroll{overflow:auto;max-height:280px;background:#fff}',
      '.nt-scroll svg{display:block}',
      '.nt-num{font:600 22px/1 "Times New Roman",serif;fill:#111}',
      '.nt-dash{font:700 24px/1 "Times New Roman",serif;fill:#111}',
      '.nt-hold{font:400 20px/1 "Times New Roman",serif;fill:#b9b9b9}',
      '.nt-plus{font:400 20px/1 sans-serif;fill:#5b91ff}',
      '.nt-acc{font:400 15px/1 serif;fill:#111}',
      '.nt-leg{font:400 13px/1 sans-serif;fill:#333}',
      '.nt-dot{fill:#111}',
      '.nt-under{stroke:#111;stroke-width:1.6}',
      '.nt-bar{stroke:#8a8a8a;stroke-width:1;stroke-dasharray:3 3}',
      '.nt-line{stroke:#111;stroke-width:1}',
      '.nt-barline{stroke:#111;stroke-width:1.2}',
      '.nt-ledger{stroke:#111;stroke-width:1.4}',
      '.nt-stem{stroke:#111;stroke-width:1.5}',
      '.nt-tie{fill:none;stroke:#111;stroke-width:1.4}',
      '.nt-head path,.nt-rest path,.nt-clef path,.nt-keysig path,.nt-accglyph path,.nt-flag path,.nt-timesig path{fill:#111}',
      '.nt-augdot{fill:#111}',
      '.nt-lyric{font:400 13px/1 sans-serif;fill:#333}',
      '.nt-hitrect{fill:transparent}',
      '.nt-hit{cursor:pointer}',
      // ⭐ 用户 2026-10-08：「可以把当前的那条蓝线改个色」——
      // 选中/悬停原来是**蓝**的（rgba(91,145,255,…)），而音高线接下来要画在这张谱上、
      // 也是蓝的 ⇒ 把"当前位置"改成**琥珀**（与 minimap 里"当前命中"同一个色），一眼分得开。
      '.nt-hit:hover .nt-hitrect{fill:rgba(217,164,65,.14)}',
      '.nt-selrect{fill:rgba(217,164,65,.18);stroke:rgba(217,164,65,.75)}',
      '.nt-out{max-height:280px;overflow:auto;background:#0f131a;color:#cfe3ff;padding:10px;border-radius:6px;font:12px/1.5 ui-monospace,Consolas,monospace;white-space:pre-wrap}',
      '.nt-badge{font-size:12px;padding:2px 7px;border-radius:10px;background:#243044;color:#9dc0ff}',
      '.nt-warn{color:#ffc46b}',
      // ⭐ 导出乐谱：**页内预览**（屏幕上就是一个浮层，一页一张白纸）+ 打印时只留它
      '#nt-printroot{display:none}',
      '.nt-pv{position:fixed;left:0;right:0;top:0;bottom:0;z-index:90;background:#20242b;overflow:auto;padding:0 0 18px}',
      '.nt-pv-bar{position:sticky;top:0;z-index:2;display:flex;align-items:center;gap:8px;flex-wrap:wrap;' +
      'padding:7px 12px;background:#2f333a;border-bottom:1px solid var(--line,#3f434a)}',
      '.nt-pv-bar b{font-size:13px}',
      '.nt-pv-bar label{font-size:12px;opacity:.75}',
      // ⭐ 页眉三项里"页码"那个复选框：别被上面的 label 规则弄成一行两截
      '.nt-pv-bar label.nt-ck{display:inline-flex;align-items:center;gap:4px;opacity:1;cursor:pointer}',
      '.nt-pv-bar label.nt-ck input{accent-color:var(--accent,#1a73e8)}',
      // 主控件那一排（导出选项）里的「标题」输入框与页码勾选
      '.nt-actions2 label.nt-ck,.nt-actions2 label.nt-ck{display:inline-flex;align-items:center;gap:4px;font-size:12px;cursor:pointer}',
      '.nt-pv-bar select{background:#0f131a;color:var(--fg,#d6dae0);border:1px solid var(--line,#3f434a);' +
      'border-radius:5px;padding:3px 6px;font:inherit;font-size:12px}',
      '.nt-pv-meta{font-size:12px;color:var(--dim,#9498a0)}',
      // ⭐ 一张"纸"就是 **A4 的 mm 尺寸**（宽高与内边距都由 JS 按行内样式给，见 `pageStyle()`）
      //    ⇒ 屏幕上看到的就是印出来的；这里只管外观（白纸 / 阴影 / 居中 / 不撑破）
      '.nt-print-page{background:#fff;color:#000;margin:14px auto;box-shadow:0 2px 10px rgba(0,0,0,.45);' +
      'border-radius:2px;box-sizing:border-box;overflow:hidden}',
      '.nt-print-head{font:11px/1.5 "Microsoft YaHei",sans-serif;color:#333;margin:0 0 3mm 0}',
      '.nt-print-page svg{display:block;height:auto}',
      '@media print{',
      // 屏幕上的一切都不打印 —— 只留 `#nt-printroot`（它是 body 的直接子元素）
      '  body>*{display:none !important}',
      '  html,body{height:auto;overflow:visible;background:#fff}',
      '  #nt-printroot{display:block !important;position:static;background:#fff;padding:0;color:#000}',
      '  .nt-pv-bar{display:none !important}',
      // ⚠️ `@page` 的纸型/方向由 `setPaper()` 动态塞进 `#nt-paper-style`（A4 横向或竖向）；
      //    这里 margin 归零 —— 页边距由**纸自己的 padding** 提供 ⇒ 屏幕所见即打印所得
      // 一页一段：每段里叠**好几行**（行与行的换页由 JS 分页决定，这里只保证段不跨页）
      '  .nt-print-page{margin:0;box-shadow:none;background:#fff;border-radius:0;overflow:visible;' +
      'page-break-after:always;break-after:page;page-break-inside:avoid;break-inside:avoid}',
      '  .nt-print-page:last-child{page-break-after:auto;break-after:auto}',
      '  .nt-print-page svg{width:100%;height:auto;margin:0}',
      // 选中高亮是**屏幕上的编辑态**，不该印进谱里
      '  .nt-selrect{display:none}',
      '}',
    ].join('');
    var st = document.createElement('style');
    st.id = STYLE_ID;
    st.textContent = css;
    document.head.appendChild(st);
  }

  var PITCHES = [['1', 1], ['2', 2], ['3', 3], ['4', 4], ['5', 5], ['6', 6], ['7', 7]];
  var DURS = [['全', 1], ['二分', 2], ['四分', 4], ['八分', 8], ['十六分', 16]];

  function panelHTML() {
    return '' +
      '<div class="nt-frame">' +
      '<div class="nt-bar">' +
      '<button class="btn" id="nt-read" type="button">从 ACE 读取</button>' +
      '<button class="btn ghost" id="nt-demo" type="button" title="填一份 1-7 音阶当样例">示例</button>' +
      '<button class="btn ghost" id="nt-clear" type="button">清空</button>' +
      // 顶栏只放**真写**那一颗（用户 2026-10-08：「生成（干跑）」只在下面生成块里）
      '<button class="btn danger" id="nt-write-top" type="button" title="把上面这份谱写成 ACE 音符（可 Ctrl+Z 退回）">生成并写进 ACE</button>' +
      '<button class="btn ghost" id="nt-print-top" type="button" title="出预览（多行谱、A4 分页）">导出预览</button>' +
      '<span style="width:10px"></span>' +
      '<label>调式</label><select id="nt-key"></select>' +
      '<label>拍号</label><input id="nt-meter" autocomplete="off" spellcheck="false" style="width:60px" value="4/4">' +
      '<span class="nt-badge" id="nt-count">0 个元素</span>' +
      '<span class="dim" id="nt-meta"></span>' +
      '</div>' +
      '<div class="nt-tools">' +
      '<label>音高</label>' + PITCHES.map(function (p) {
        return '<button class="btn ghost" data-nt-pitch="' + p[1] + '" type="button">' + p[0] + '</button>';
      }).join('') +
      '<button class="btn ghost" data-nt-pitch="0" type="button">0 休止</button>' +
      '<button class="btn ghost" data-nt-sustain="1" type="button">- 连音</button>' +
      '<span style="width:10px"></span>' +
      '<label>时值</label>' + DURS.map(function (d) {
        return '<button class="btn ghost" data-nt-dur="' + d[1] + '" type="button">' + d[0] + '</button>';
      }).join('') +
      '<button class="btn ghost" data-nt-dots="1" type="button">附点</button>' +
      '<span style="width:10px"></span>' +
      '<label>变音</label>' +
      '<button class="btn ghost" data-nt-acc="sharp" type="button">♯</button>' +
      '<button class="btn ghost" data-nt-acc="flat" type="button">♭</button>' +
      '<button class="btn ghost" data-nt-acc="" type="button">♮</button>' +
      '<span style="width:10px"></span>' +
      '<button class="btn ghost" data-nt-oct="1" type="button">八度 +</button>' +
      '<button class="btn ghost" data-nt-oct="-1" type="button">八度 −</button>' +
      '<button class="btn ghost" data-nt-move="-1" type="button">← 左移</button>' +
      '<button class="btn ghost" data-nt-move="1" type="button">右移 →</button>' +
      '<button class="btn ghost" data-nt-del="1" type="button">删除</button>' +
      '<label>歌词</label><input id="nt-lyric" autocomplete="off" spellcheck="false" style="width:90px" placeholder="一个字">' +
      '<button class="btn ghost" id="nt-lyric-set" type="button">填给选中</button>' +
      '</div>' +
      '<div class="nt-scroll"><svg id="nt-jp" xmlns="http://www.w3.org/2000/svg"></svg></div>' +
      '<div class="nt-scroll"><svg id="nt-st" xmlns="http://www.w3.org/2000/svg"></svg></div>' +
      // ⭐ 用户 2026-10-08：「删除不必要的文字」——图例压成一行的三条（机制都在 HANDOVER，不往界面上搬）
      '<div class="nt-legend dim" style="padding:2px 8px;font-size:11.5px">' +
      '浅灰 <b>-</b> = 音内延长 · 黑 <b>-</b> = 连音格 · <b>0</b> = 休止（= 空拍）· <b>+</b> = 追加格' +
      '</div>' +
      '<div class="nt-actions">' +
      // ⭐ 用户 2026-10-07：「直接把 nt 块拿过来，下面是生成」——
      // 这一块就是**谱 → 音符的生成**：输入直接用上面那个 nt 块（编辑器里的元素，不再让用户重填），
      // 起点默认 = **播放头所在小节的起点**（播放头在哪一小节就从那条小节线开始生成）。
      '<div class="nt-gen">' +
      '<b>生成</b>' +
      '<label>起点</label>' +
      '<select id="nt-start">' +
      '<option value="playhead">播放头所在小节的起点</option>' +
      '<option value="playheadExact">播放头的精确位置</option>' +
      '<option value="clipStart">片段开头</option>' +
      '</select>' +
      '<label>写到哪</label>' +
      '<select id="nt-target">' +
      '<option value="current">当前片段</option>' +
      '<option value="newClip">在当前轨新建一个片段</option>' +
      '</select>' +
      '<button class="btn danger" id="nt-write" type="button">生成并写进 ACE</button>' +
      '</div>' +
      '<div class="nt-actions2">' +
      '<label>导出</label>' +
      // ⭐ 导出内容：两排 / 只要五线谱 / 只要简谱（用户 2026-10-07 点名要能选）
      '<select id="nt-rows" title="导出哪几排谱">' + rowsOptions('both') + '</select>' +
      // ⭐ 每行几小节（用户 2026-10-07：「挂下拉」）：自动按页宽，或固定档
      '<select id="nt-mpl" title="每行排几小节">' + mplOptions(0) + '</select>' +
      // ⭐ 纸型（用户 2026-10-08：「不是应该按照 A4 大小来吗」）
      '<select id="nt-paper" title="纸型（A4 横向 / 竖向）">' + paperOptions('landscape') + '</select>' +
      // ⭐ 用户 2026-10-15：页眉能隐藏 / 可自定义（标题留空不印；页眉三档；页码单独关）
      '<input id="nt-title" autocomplete="off" spellcheck="false" style="width:110px" placeholder="标题" title="谱面标题（留空就不印）">' +
      '<select id="nt-head" title="页眉：每页印 / 只在第一页印 / 不印（连页码一起）">' + headOptions('all') + '</select>' +
      '<label class="nt-ck" title="页眉在印的前提下，是否带「第 x / y 页」"><input type="checkbox" id="nt-headpage" checked autocomplete="off">页码</label>' +
      '<button class="btn ghost" id="nt-print" type="button" title="先出预览（多行谱、一页好几行）；预览里再点「打印 / 另存为 PDF」">导出预览</button>' +
      '<button class="btn ghost" id="nt-xml" type="button" title="导出 MusicXML（也能喂 ACE：import file 收 MusicXML）">MusicXML</button>' +
      '<button class="btn ghost" id="nt-elems" type="button">元素 JSON</button>' +
      '<button class="btn ghost" id="nt-dl" type="button" hidden>下载 .musicxml</button>' +
      '</div>' +
      '</div>' +
      '</div>' +
      '<div id="nt-out"></div>';
  }

  /* ============================ 状态与渲染 ============================ */

  var S = { els: [], sel: -1, key: 0, meter: { n: 4, d: 4 }, title: '', pen: { pitch: 1, duration: 4, dots: 0, accidental: '' }, xml: '', meta: '' };

  /* ---------- 页眉设置（用户 2026-10-15：「五线谱导出能把上面的字隐藏或可自定义吗」⇒ 要 B+C） ----------
     ① **去掉 `[meta]`**：那句"来自 ACE（当前片段）· 调式…"是**面板上的状态提示**，以前被印到纸上，不该上谱；
     ② **标题可填**（留空就不印这一段，同时喂给 MusicXML 的 `<work-title>`）；
     ③ **页眉三档**：`all` 每页印 / `first` 只在第一页印 / `off` 整行不印（连页码一起）；
     ④ **页码**单独开关（页眉在印的前提下）。
     ⚠️ 分页时预留的页眉高度要跟着这三档走（不印就不该白留 8mm）。设置存 localStorage。 */
  var HEAD_KEY = 'acetool.notation.head';

  function loadHeadCfg() {
    try {
      var j = JSON.parse(localStorage.getItem(HEAD_KEY) || '{}');
      if (j.title) S.title = j.title;
      if (j.head === 'all' || j.head === 'first' || j.head === 'off') S.head = j.head;
      if (typeof j.headPage === 'boolean') S.headPage = j.headPage;
    } catch (e) { /* 坏数据当没存过 */ }
  }
  function saveHeadCfg() {
    try {
      localStorage.setItem(HEAD_KEY, JSON.stringify({
        title: S.title || '', head: S.head || 'all', headPage: S.headPage !== false,
      }));
    } catch (e) { /* 存不了就算了 */ }
  }

  function out(html) { var el = document.getElementById('nt-out'); if (el) el.innerHTML = html; }
  function pre(t) { return '<div class="nt-out">' + esc2(t) + '</div>'; }

  function draw() {
    ensureStyle();
    var jp = document.getElementById('nt-jp'), st = document.getElementById('nt-st');
    if (!jp || !st) return;
    var lay = layout(S.els), w = X0 + lay.total + 60;
    var geom = staffGeom(lay.cols, S.key);
    var opt = { key: S.key, meter: S.meter, sel: S.sel };
    jp.setAttribute('width', w); jp.setAttribute('height', SVG_H);
    jp.setAttribute('viewBox', '0 0 ' + w + ' ' + SVG_H);
    jp.innerHTML = '<rect width="100%" height="100%" fill="#fff"/>' + renderJianpu(S.els, lay.cols, opt);
    opt.dy = geom.dy; opt.lyricY = geom.lyricY;
    st.setAttribute('width', w); st.setAttribute('height', geom.height);
    st.setAttribute('viewBox', '0 0 ' + w + ' ' + geom.height);
    st.innerHTML = '<rect width="100%" height="100%" fill="#fff"/>' + renderStaff(S.els, lay.cols, opt);
    var cnt = document.getElementById('nt-count');
    if (cnt) {
      var sus = 0, rest = 0;
      S.els.forEach(function (e) { if (isSustain(e)) sus++; else if (isRest(e)) rest++; });
      var totalTicks = S.els.reduce(function (a, e) { return a + ticks(e); }, 0);
      cnt.textContent = S.els.length + ' 个元素 · ' + (totalTicks / 480).toFixed(2) + ' 拍';
      var extra = [];
      if (sus) extra.push(sus + ' 个连音格');
      if (rest) extra.push(rest + ' 个休止');
      if (extra.length) cnt.textContent += ' · ' + extra.join(' · ');
    }
    var ly = document.getElementById('nt-lyric');
    if (ly) ly.value = (S.sel >= 0 && S.els[S.sel] && S.els[S.sel].lyric) || '';
  }

  function appEnd() {
    var e = norm({ kind: 'note', pitch: S.pen.pitch, duration: S.pen.duration, dots: S.pen.dots, accidental: S.pen.accidental, octave: 0 });
    if (S.pen.pitch === 0) { e = norm({ kind: 'rest', duration: S.pen.duration, dots: S.pen.dots }); }
    S.els.push(e);
    S.sel = S.els.length - 1;
    draw();
  }

  function sel() { return S.sel >= 0 && S.sel < S.els.length ? S.els[S.sel] : null; }

  /** 工具条动作：有选中就改选中的，没选中就改「画笔」（下一个追加格的默认值）。 */
  function apply(fn) {
    var e = sel();
    if (e) { fn(e, true); draw(); }
    else { fn(S.pen, false); }
  }

  /* ============================ 交互绑定 ============================ */

  function bind() {
    ensureStyle();
    var keySel = document.getElementById('nt-key');
    if (keySel) {
      keySel.innerHTML = KEY_NAMES.map(function (n, i) { return '<option value="' + i + '">' + n + '</option>'; }).join('');
      keySel.value = String(S.key);
      keySel.addEventListener('change', function () { S.key = parseInt(keySel.value, 10) || 0; draw(); });
    }
    var meterIn = document.getElementById('nt-meter');
    if (meterIn) {
      meterIn.value = S.meter.n + '/' + S.meter.d;
      meterIn.addEventListener('change', function () {
        var m = /^\s*(\d+)\s*\/\s*(\d+)\s*$/.exec(meterIn.value);
        if (m) { S.meter = { n: clamp(parseInt(m[1], 10), 1, 16), d: clamp(parseInt(m[2], 10), 1, 16) }; draw(); }
        else { meterIn.value = S.meter.n + '/' + S.meter.d; }
      });
    }
    document.getElementById('nt-read').addEventListener('click', readACE);
    // ⭐ 页眉设置（标题 / 页眉档 / 页码）开机从 localStorage 读回，并把值填进主控件
    loadHeadCfg();
    (function fillHeadCtl() {
      var t = document.getElementById('nt-title'), h = document.getElementById('nt-head'), hp = document.getElementById('nt-headpage');
      if (t) t.value = S.title || '';
      if (h) h.value = S.head || 'all';
      if (hp) hp.checked = S.headPage !== false;
    })();
    document.getElementById('nt-demo').addEventListener('click', function () {
      S.els = DEMO.map(norm); S.sel = 0; S.meta = '示例（不是从 ACE 读的）'; draw(); setMeta();
    });
    document.getElementById('nt-clear').addEventListener('click', function () {
      S.els = []; S.sel = -1; draw();
    });
    ['nt-jp', 'nt-st'].forEach(function (id) {
      var svg = document.getElementById(id);
      svg.addEventListener('click', function (ev) {
        var g = ev.target.closest ? ev.target.closest('[data-i]') : null;
        if (!g) return;
        var di = g.getAttribute('data-i');
        if (di === 'append') { appEnd(); return; }
        S.sel = parseInt(di, 10); draw();
      });
    });
    // 工具条
    document.querySelectorAll('[data-nt-pitch]').forEach(function (b) {
      b.addEventListener('click', function () {
        var p = parseInt(b.getAttribute('data-nt-pitch'), 10);
        apply(function (e, real) {
          if (p === 0) { e.kind = 'rest'; e.pitch = 0; e.rest = true; }
          else { e.kind = 'note'; e.rest = false; e.pitch = p; }
          if (real) S.pen.pitch = p;
        });
      });
    });
    document.querySelectorAll('[data-nt-dur]').forEach(function (b) {
      b.addEventListener('click', function () {
        var d = parseInt(b.getAttribute('data-nt-dur'), 10);
        apply(function (e) { e.duration = d; });
      });
    });
    document.querySelectorAll('[data-nt-dots]').forEach(function (b) {
      b.addEventListener('click', function () { apply(function (e) { e.dots = (e.dots || 0) >= 2 ? 0 : (e.dots || 0) + 1; }); });
    });
    document.querySelectorAll('[data-nt-acc]').forEach(function (b) {
      b.addEventListener('click', function () {
        var a = b.getAttribute('data-nt-acc');
        apply(function (e) { if (e.kind === 'note') e.accidental = (e.accidental === a ? '' : a); });
      });
    });
    document.querySelectorAll('[data-nt-oct]').forEach(function (b) {
      b.addEventListener('click', function () {
        var d = parseInt(b.getAttribute('data-nt-oct'), 10);
        apply(function (e) { if (e.kind === 'note') e.octave = clamp((e.octave || 0) + d, -3, 3); });
      });
    });
    document.querySelectorAll('[data-nt-move]').forEach(function (b) {
      b.addEventListener('click', function () {
        var d = parseInt(b.getAttribute('data-nt-move'), 10), i = S.sel;
        var j = i + d;
        if (i < 0 || j < 0 || j >= S.els.length) return;
        var t = S.els[i]; S.els[i] = S.els[j]; S.els[j] = t; S.sel = j; draw();
      });
    });
    document.querySelectorAll('[data-nt-del]').forEach(function (b) {
      b.addEventListener('click', function () {
        if (S.sel < 0) return;
        S.els.splice(S.sel, 1);
        if (S.sel >= S.els.length) S.sel = S.els.length - 1;
        draw();
      });
    });
    document.querySelectorAll('[data-nt-sustain]').forEach(function (b) {
      b.addEventListener('click', function () {
        var e = sel();
        if (e) { S.els.splice(S.sel + 1, 0, norm({ kind: 'sustain', duration: e.duration, dots: e.dots })); S.sel = S.sel + 1; draw(); }
        else { S.els.push(norm({ kind: 'sustain', duration: S.pen.duration, dots: S.pen.dots })); S.sel = S.els.length - 1; draw(); }
      });
    });
    var lyBtn = document.getElementById('nt-lyric-set');
    var lyIn = document.getElementById('nt-lyric');
    function applyLyric() {
      var e = sel();
      if (e) { e.lyric = lyIn.value.trim(); draw(); }
      else out('<div class="hint">先在谱上点一个音，再填歌词。</div>');
    }
    if (lyBtn) lyBtn.addEventListener('click', applyLyric);
    if (lyIn) lyIn.addEventListener('keydown', function (ev) { if (ev.key === 'Enter') { applyLyric(); ev.preventDefault(); } });
    // 快捷键：←/→ 选、↑/↓ 八度、Del 删、1-7/0/- 设音高
    document.addEventListener('keydown', function (ev) {
      if (ev.target && /INPUT|TEXTAREA|SELECT/.test(ev.target.tagName)) return;
      if (!document.getElementById('nt-jp')) return;
      var k = ev.key;
      if (k === 'ArrowLeft') { if (S.sel > 0) { S.sel--; draw(); ev.preventDefault(); } }
      else if (k === 'ArrowRight') { if (S.sel < S.els.length - 1) { S.sel++; draw(); ev.preventDefault(); } }
      else if (k === 'ArrowUp' || k === 'ArrowDown') {
        apply(function (e) { if (e.kind === 'note') e.octave = clamp((e.octave || 0) + (k === 'ArrowUp' ? 1 : -1), -3, 3); });
        ev.preventDefault();
      } else if (k === 'Delete' || k === 'Backspace') {
        if (S.sel >= 0) { S.els.splice(S.sel, 1); if (S.sel >= S.els.length) S.sel = S.els.length - 1; draw(); ev.preventDefault(); }
      } else if (/^[0-7]$/.test(k)) {
        var p = parseInt(k, 10);
        apply(function (e) { if (p === 0) { e.kind = 'rest'; e.pitch = 0; e.rest = true; } else { e.kind = 'note'; e.rest = false; e.pitch = p; } });
        ev.preventDefault();
      } else if (k === '-') { document.querySelector('[data-nt-sustain]').click(); ev.preventDefault(); }
    });
    // ⭐ 用户 2026-10-08：「下面还有个干跑没删」⇒ 生成块里那颗 `#nt-dry` 也删了，
    //    面板上**只剩一颗**真写按钮（干跑这条路径仍在 `writeACE(true)` 里，给脚本/自检用 ——
    //    见 `window.NotationPanel._test.writeACE`）。
    document.getElementById('nt-write').addEventListener('click', function () { writeACE(false); });
    // 顶栏那一份（同一个动作；按钮在哪儿点都一样）
    var writeTop = document.getElementById('nt-write-top');
    if (writeTop) writeTop.addEventListener('click', function () { writeACE(false); });
    document.getElementById('nt-xml').addEventListener('click', function () { exportAs('musicxml'); });
    // 导出乐谱：**先预览**（顶栏与导出行各一颗，同一个动作）；真正打印在预览浮层里那颗
    document.getElementById('nt-print').addEventListener('click', exportScore);
    var printTop = document.getElementById('nt-print-top');
    if (printTop) printTop.addEventListener('click', exportScore);
    document.getElementById('nt-elems').addEventListener('click', function () {
      out('<div class="dim">编辑器文档（元素 JSON）—— 这就是发给后端的那份：</div>' + pre(JSON.stringify(S.els, null, 2)));
    });
    document.getElementById('nt-dl').addEventListener('click', downloadXML);
    draw();
  }

  function setMeta() {
    var el = document.getElementById('nt-meta');
    if (el) el.textContent = S.meta || '';
  }

  /* ============================ 与后端来回 ============================ */

  function values() {
    return (window.state && state.values) || {};
  }
  /**
   * 元素 JSON：**发上游方言**（休止 = `kind:"note" + rest:true`）。
   *
   * 为什么不用编辑器内部的 `kind:"rest"`：`jianpu.Element` 是上游（open-fanqie）模型的移植，
   * 它的休止就是 `note + rest:true`，解析器也只产出这一种。发方言出去，任何消费者都认；
   * 而 Go 侧另外**也收** `kind:"rest"`（`elementsToJpElems`），所以手写 JSON 的人写哪个都行。
   */
  function elemsJSON() { return elemsJSONOf(S.els); }

  /**
   * 同上，但是**纯函数**（给自检用：能脱离 DOM 产出「面板会发的那份 JSON」，
   * 再拿去打真实 HTTP，验证 Go 侧确实收这个形状 —— 见 `devtools/e2e.js` ⑤）。
   */
  function elemsJSONOf(els) {
    return JSON.stringify(els.map(function (e) {
      var o = { kind: e.kind === 'sustain' ? 'sustain' : 'note', duration: e.duration, dots: e.dots };
      if (o.kind === 'note') {
        if (e.kind === 'rest') { o.rest = true; o.pitch = 0; }
        else {
          o.pitch = e.pitch;
          if (e.octave) o.octave = e.octave;
          if (e.accidental) o.accidental = e.accidental;
        }
      }
      if (e.lyric) o.lyric = e.lyric;
      return o;
    }));
  }

  function busy(msg) { out('<div class="hint ok">' + esc2(msg) + '</div>'); }

  /* ============================ 导出乐谱：多行谱 + 页内预览 + 打印 ============================ */
  //
  // 用户 2026-10-07 三条要求（这一节就是照它们写的）：
  //   ① 「导出的时候不要弹打印界面直接预览可以吗」⇒ 点按钮先**在页面里出预览**（一页一张白纸），
  //      预览里再点「打印 / 另存为 PDF」才真的调 `window.print()`；
  //   ② 「用户选择导出五线谱或简谱」⇒ 「导出内容」下拉：两排 / 只要五线谱 / 只要简谱；
  //   ③ 「让谱子不再每页只有一行」⇒ **按小节切行、把行两端对齐到页宽**（`buildSystems`），
  //      一页里叠**好几行**（`pageBreak` 按行高累计），而不是把一条长线横着切成十几页。

  var PRINT = {
    spaceMM: 1.75,        // 1 个五线谱间距印多大（印刷常见值）⇒ 决定整份谱的比例
    margin: 10,           // 页边距（四周各 10mm）
    paperW: 297,          // A4 横向：297 × 210
    paperH: 210,
    pageW: 277,           // 内容区 = 纸 − 2×边距
    pageH: 190,
    orient: 'landscape',
    sysGap: 26,           // 行与行之间的空（px，布局坐标系里）
    natural: 0.78,        // 一行"自然宽度"目标 = 页宽 × 这个系数（剩下的靠两端对齐摊开）
    headMM: 8,            // 每页顶上那行页眉占的高度（mm）—— 分页时要先让出来
  };

  /**
   * ⭐ 纸型（用户 2026-10-08：「这个每页的乐谱不是应该按照 A4 大小来吗」）。
   *
   * ⇒ 预览里那张"纸"就**按 mm 画成 A4**（297×210 或 210×297、四边 10mm 边距），内容区正好
   *   `pageW × pageH`；打印时 `@page{size:A4 <方向>;margin:0}` + 纸自带 10mm 内边距
   *   ⇒ **屏幕上看到的就是印出来的**（不再靠浏览器默认边距，也不再是"一个 1200px 的白盒子"）。
   */
  function setPaper(orient) {
    PRINT.orient = (orient === 'portrait') ? 'portrait' : 'landscape';
    PRINT.paperW = PRINT.orient === 'portrait' ? 210 : 297;
    PRINT.paperH = PRINT.orient === 'portrait' ? 297 : 210;
    PRINT.pageW = PRINT.paperW - 2 * PRINT.margin;
    PRINT.pageH = PRINT.paperH - 2 * PRINT.margin;
    // `@page` 没法按 class 作用域 ⇒ 动态塞一条，纸型换了就换它
    var st = document.getElementById('nt-paper-style');
    if (!st) {
      st = document.createElement('style');
      st.id = 'nt-paper-style';
      document.head.appendChild(st);
    }
    st.textContent = '@media print{@page{size:A4 ' + PRINT.orient + ';margin:0}}';
  }

  function paperOptions(cur) {
    var opts = [['landscape', 'A4 横向'], ['portrait', 'A4 竖向']];
    return opts.map(function (o) {
      return '<option value="' + o[0] + '"' + (o[0] === cur ? ' selected' : '') + '>' + o[1] + '</option>';
    }).join('');
  }

  /** 一张"纸"的行内样式（宽高就是纸的 mm，内边距就是页边距）—— 屏幕与打印**共用**这一份。 */
  function pageStyle() {
    return 'width:' + PRINT.paperW + 'mm;height:' + PRINT.paperH + 'mm;padding:' + PRINT.margin + 'mm';
  }

  /**
   * 一行的高度（px，布局坐标系）：按"导出哪几排"算，五线谱那排按它自己的让位高度。
   *
   * ⚠️ **两排模式**下简谱那排**不画歌词**（歌词跟五线谱那排走，免得两处重复）⇒ 那一行不需要
   * 留到 `SVG_H`（214px，给歌词留的），只留数字/附点/减时线要的地方 ⇒ 一页能多塞一行
   * （实测：两排模式从"每页 2 行"变成**每页 3 行**）。
   */
  var JP_ROW_TIGHT = J_BASE + 34;   // 不画歌词时简谱那排要的高度

  function jpRowHeight(rows) { return rows === 'both' ? JP_ROW_TIGHT : SVG_H; }

  function systemHeight(rows, staffH) {
    var h = 0;
    if (rows !== 'staff') h += jpRowHeight(rows);      // 简谱那排
    if (rows !== 'jianpu') h += staffH;
    return h + PRINT.sysGap;
  }

  /**
   * 把小节切成"行"（systems）—— **这是"每页不再只有一行"的关键**。
   *
   * 做法：先按现有的列布局（`layout()`）算每个元素多宽，再按**小节**分组，
   * 然后贪心地往一行里塞小节：塞到"自然宽度"接近 `页宽 × natural` 就断行；
   * 最后把这一行的列 **两端对齐**（把多余的空按列宽比例摊进列间距里）⇒ 每行都顶满页宽。
   *
   * ⚠️ 两端对齐只**加空隙**、不缩放字形（真谱的 justification 也是这么做的）——
   * 直接 `scale()` 会把音符头拉扁。
   *
   * `mpl > 0` = **固定每行几小节**（用户 2026-10-07：「挂下拉」）；`0` = 按页宽自动。
   */
  function buildSystems(els, key, meter, rows, mpl) {
    var cols = layout(els).cols;
    var per = 4 * meter.n / meter.d;                 // 一小节几拍
    var pageWpx = PRINT.pageW / (PRINT.spaceMM / SPACE);
    var target = pageWpx * PRINT.natural;

    // ① 按小节分组（一小节 = 若干列）—— ⭐ **跨界的地方按小节线拆开**（用户 2026-10-07）
    //   一个音跨过小节线时，拆成"这一小节剩下的"与"下一小节接着的"两截（各自是写得出来的时值），
    //   后一截记 `cont = true`（它是延续：简谱写 `-`、五线谱要连音线）。
    var measures = [], cur = { cols: [], beats: 0, width: 0 }, beatAcc = 0;
    var pushPiece = function (col, ck, cont, contAfter) {
      var c = {
        i: col.i, e: col.e, x: 0, cx: 0, w: wForBeats(ck.beats, isSustain(col.e)),
        dur: ck.d, dots: ck.dots, beats: ck.beats, cont: cont, contAfter: !!contAfter,
      };
      cur.cols.push(c);
      cur.beats += ck.beats;
      cur.width += c.w;
      beatAcc += ck.beats;
      if (beatAcc >= per - 1e-6) {
        beatAcc -= per;
        cur.beats = per;
        measures.push(cur);
        cur = { cols: [], beats: 0, width: 0 };
      }
    };
    for (var i = 0; i < cols.length; i++) {
      var col = cols[i], left = beats(col.e), firstPiece = true;
      while (left > 1e-6) {
        var room = per - beatAcc;
        var seg = Math.min(left, room);           // 先切到小节线
        var chunks = splitBeats(seg);             // 再切成写得出来的时值
        if (!chunks.length) { chunks = [{ d: col.e.duration || 4, dots: col.e.dots || 0, beats: seg }]; }
        for (var ci = 0; ci < chunks.length; ci++) {
          var cont = !(firstPiece && ci === 0);
          var more = (ci < chunks.length - 1) || (left - seg > 1e-6);
          pushPiece(col, chunks[ci], cont, more);
        }
        left -= seg;
        firstPiece = false;
      }
    }
    if (cur.cols.length) measures.push(cur);     // 末尾不完整的小节也算一行里的内容

    // ② 行数：固定档直接用；自动档让每行的自然宽度接近 target
    var perSystem;
    if (mpl > 0) {
      perSystem = mpl;
    } else {
      var totalW = 0;
      for (var q = 0; q < measures.length; q++) totalW += measures[q].width;
      perSystem = Math.max(1, Math.round(measures.length / Math.max(1, Math.ceil(totalW / target))));
    }

      // ③ 逐行收列 + 两端对齐
    var systems = [];
    for (var a = 0; a < measures.length; a += perSystem) {
      var group = measures.slice(a, a + perSystem);
      var sysCols = [], x = 0, natW = 0, lyr = false;
      for (var g = 0; g < group.length; g++) {
        for (var c = 0; c < group[g].cols.length; c++) {
          var col = group[g].cols[c];
          sysCols.push({ i: col.i, e: col.e, x: x, w: col.w, cx: x + col.w / 2,
            dur: col.dur, dots: col.dots, beats: col.beats, cont: col.cont, contAfter: col.contAfter });
          x += col.w;
          natW += col.w;
          if (col.e.lyric) lyr = true;
        }
      }
      // 两端对齐：把 (页宽 − 左边留白 − 自然宽 − 收口那 40px) **按列宽比例摊进列间距**。
      //   ⚠️ 只加空隙、不缩放字形 —— 直接 `scale()` 会把音符头拉扁。
      //   ⚠️ 分摊按**前一列的宽度**给（时值长的列后面留宽一点，读起来更匀）。
      var rightPad = 40;                              // 末行那条结束小节线要的地方
      var extra = Math.max(0, pageWpx - X0 - natW - rightPad);
      var wSum = 0;
      for (var k = 0; k < sysCols.length; k++) wSum += sysCols[k].w;
      if (extra > 0 && wSum > 0 && sysCols.length > 1) {
        var x2 = 0;
        for (var j = 0; j < sysCols.length; j++) {
          if (j > 0) x2 += extra * (sysCols[j - 1].w / wSum);
          sysCols[j].x = x2;
          sysCols[j].cx = x2 + sysCols[j].w / 2;
          x2 += sysCols[j].w;
        }
      }
      var beatAt = 0;
      if (systems.length) {                            // 本行第一列"之前"已经走了多少拍
        var done = 0;
        for (var p2 = 0; p2 < a; p2++) done += measures[p2].beats;
        beatAt = ((done % per) + per) % per;
      }
      var staffH = staffGeom(sysCols, key).height;
      systems.push({
        cols: sysCols, beatAt: beatAt, hasLyric: lyr,
        measureNo: a + 1,                              // 这一行的第一个小节（1 起）
        measures: group.length, staffH: staffH,
        carry: carryInto(els, sysCols),                // ⭐ 从上一行"带过来"的音（跨行连音）
        leaveTie: leavesTie(els, sysCols),             // ⭐ 这一行末尾要甩出去一个连音线
      });
    }
    return { systems: systems, pageWpx: pageWpx, pageHpx: PRINT.pageH / (PRINT.spaceMM / SPACE) };
  }

  /**
   * ⭐ **跨行连音**（用户 2026-10-07：「按真谱来」）。
   *
   * 什么时候会出现：一个音被 `-`（tenuto 音符）延续，**而 `-` 落在下一行**（小节线正好是换行处，
   * 或者固定"每行 N 小节"把它切开）。真谱的写法是：
   *
   *   五线谱 —— 上一行**末尾**甩出一小段连音线，下一行**开头把那个音重新写一遍**（同音高）并接上，
   *             两头的符头是不同的时值（各按自己那一行的长度记）；
   *   简谱   —— 下一行开头写 `-`（延音号），表示"接着上一行那个音"。
   *
   * ⇒ 这里给出两件东西：
   *   `carryInto(els, cols)` —— 本行第一列如果是 `-`，把**链头那个音**找出来（跨行带过来的音高）；
   *   `leavesTie(els, cols)` —— 本行最后一列的后面还是不是同一个音的 `-`（是 ⇒ 末尾甩连音线）。
   */
  function carryInto(els, cols) {
    if (!cols.length) return null;
    var first = cols[0];
    if (first.cont) return first.e;                     // ⭐ 拆出来的后半截：音高就是它自己
    if (!isSustain(first.e)) return null;
    for (var k = first.i - 1; k >= 0; k--) {            // 往回找链头（跳过 `-`）
      if (isSustain(els[k])) continue;
      if (isRest(els[k])) return null;                  // 休止后面接 `-` 是"空拍拖长"，没有音高可带
      return els[k];
    }
    return null;
  }

  function leavesTie(els, cols) {
    if (!cols.length) return false;
    var last = cols[cols.length - 1];
    if (last.contAfter) return true;                    // ⭐ 拆出来的前半截：尾巴要甩到下一行
    if (isSustain(last.e)) return false;                // 行末就是 `-`：它自己就是延续，另算
    var next = els[last.i + 1];
    return !!next && isSustain(next);
  }

  /** 一行（system）渲成 SVG 片段：`rows` 决定画哪几排；`firstSystem` 决定简谱那排画不画调式说明。 */
  function renderSystem(sys, rows, firstSystem, sel) {
    var parts = [], y = 0;
    if (rows !== 'staff') {
      parts.push('<g transform="translate(0,' + r2(y) + ')">' +
        renderJianpu(S.els, sys.cols, {
          key: S.key, meter: S.meter, sel: sel, beatAt: sys.beatAt, append: false,
          label: firstSystem, measureNo: firstSystem ? 0 : sys.measureNo, hideLyrics: rows === 'both',
        }) + '</g>');
      y += jpRowHeight(rows);
    }
    if (rows !== 'jianpu') {
      var g = staffGeom(sys.cols, S.key);
      parts.push('<g transform="translate(0,' + r2(y) + ')">' +
        renderStaff(S.els, sys.cols, {
          key: S.key, meter: S.meter, sel: -1, dy: g.dy, lyricY: g.lyricY,
          beatAt: sys.beatAt, append: false, measureNo: firstSystem ? 0 : sys.measureNo,
          carry: sys.carry, leaveTie: sys.leaveTie,        // ⭐ 跨行连音（真谱写法）
        }) + '</g>');
      y += g.height;
    }
    return { html: parts.join(''), height: y };
  }

  /** 把行分页：一页叠到装不下为止（`rows` 决定每行多高 ⇒ 一页几行）。
   *  ⚠️ 顶上那行页眉要**按档位**预留：每页印 ⇒ 每页都留；仅首页 ⇒ 只第一页留；不印 ⇒ 不留（别白留 8mm）。 */
  function paginate(built, rows, headMode) {
    var pages = [], cur = [], h = 0;
    var s = PRINT.spaceMM / SPACE;
    var mode = headMode || 'all';
    var rFirst = (mode === 'off') ? 0 : PRINT.headMM / s;
    var rRest = (mode === 'all') ? PRINT.headMM / s : 0;
    for (var i = 0; i < built.systems.length; i++) {
      var sh = systemHeight(rows, built.systems[i].staffH);
      var avail = built.pageHpx - (pages.length === 0 ? rFirst : rRest);
      if (cur.length && h + sh > avail) { pages.push({ systems: cur, h: h }); cur = []; h = 0; }
      cur.push(built.systems[i]);
      h += sh;
    }
    if (cur.length) pages.push({ systems: cur, h: h });
    return pages;
  }

  /** 一页 → 一个 `<svg>`（viewBox 就是页宽×内容高，`width` 用**固定比例**折成 mm）。 */
  function renderPage(page, built, rows, firstSystemOfScore) {
    var s = PRINT.spaceMM / SPACE;
    var y = 0, parts = [];
    for (var i = 0; i < page.systems.length; i++) {
      var sys = page.systems[i];
      var r = renderSystem(sys, rows, firstSystemOfScore && i === 0, -1);
      parts.push('<g transform="translate(0,' + r2(y) + ')">' + r.html + '</g>');
      y += r.height + PRINT.sysGap;
    }
    var h = Math.max(1, y);
    var wmm = (built.pageWpx * s).toFixed(2), hmm = (h * s).toFixed(2);
    return '<svg class="nt-score" style="width:' + wmm + 'mm;height:' + hmm + 'mm" viewBox="0 0 ' +
      r2(built.pageWpx) + ' ' + r2(h) + '" preserveAspectRatio="xMinYMin meet">' +
      '<rect width="100%" height="100%" fill="#fff"/>' + parts.join('') + '</svg>';
  }

  /** 当前"导出哪几排"。 */
  function exportRows() {
    var el = document.getElementById('nt-rows');
    return (el && el.value) || 'both';
  }

  /** 当前"每行几小节"（0 = 按页宽自动）。 */
  function exportMpl() {
    var el = document.getElementById('nt-mpl');
    return parseInt((el && el.value) || '0', 10) || 0;
  }

  function mplOptions(cur) {
    var opts = [[0, '每行：自动'], [1, '每行 1 小节'], [2, '每行 2 小节'], [3, '每行 3 小节'],
      [4, '每行 4 小节'], [6, '每行 6 小节'], [8, '每行 8 小节'], [12, '每行 12 小节']];
    return opts.map(function (o) {
      return '<option value="' + o[0] + '"' + (o[0] === cur ? ' selected' : '') + '>' + o[1] + '</option>';
    }).join('');
  }

  /**
   * ⭐ 导出乐谱（用户 2026-10-07：**不弹打印界面、先预览**）。
   *
   * 把多行谱铺成"一页一张白纸"的预览（`#nt-printroot`，屏幕上就是一个可滚动的浮层），
   * 浮层里有「打印 / 另存为 PDF」（那一刻才 `window.print()`）和「关闭」，也能就地换"导出内容"。
   * ⚠️ 打印时同一份 DOM 直接进纸：`@media print` 把屏幕上其它一切藏掉、只留 `#nt-printroot`。
   */
  /** 当前"纸型"（'landscape' | 'portrait'）。 */
  function exportPaper() {
    var el = document.getElementById('nt-paper');
    return (el && el.value) || 'landscape';
  }

  /** 页眉三档（用户 2026-10-15）：all = 每页印 · first = 只在第一页印 · off = 整行不印。 */
  function exportHead() {
    var el = document.getElementById('nt-head');
    var v = (el && el.value) || S.head || 'all';
    return (v === 'first' || v === 'off') ? v : 'all';
  }

  /** 页眉里要不要带「第 x / y 页」。 */
  function exportHeadPage() {
    var el = document.getElementById('nt-headpage');
    return el ? !!el.checked : (S.headPage !== false);
  }

  /** 谱面标题（可自定义、留空就不印）。 */
  function exportTitle() {
    var el = document.getElementById('nt-title');
    return el ? String(el.value || '').trim() : (S.title || '');
  }

  /** 页眉下拉的选项。 */
  function headOptions(cur) {
    var opts = [['all', '页眉：每页'], ['first', '页眉：仅首页'], ['off', '不印页眉']];
    return opts.map(function (o) {
      return '<option value="' + o[0] + '"' + (o[0] === cur ? ' selected' : '') + '>' + o[1] + '</option>';
    }).join('');
  }

  function exportScore() {
    ensureStyle();
    if (!S.els.length) { out('<div class="hint bad">谱是空的 —— 先「从 ACE 读取」或按「示例」。</div>'); return; }
    var rows = exportRows(), mpl = exportMpl();
    setPaper(exportPaper());                       // ⭐ 纸型：A4 横/竖（预览那张纸与 @page 都按它）
    var built = buildSystems(S.els, S.key, S.meter, rows, mpl);
    var headMode = exportHead();                       // all | first | off
    var showPageNum = exportHeadPage();
    var title = exportTitle();
    S.title = title; S.head = headMode; S.headPage = showPageNum; saveHeadCfg();   // 记住设置
    var pages = paginate(built, rows, headMode);
    // ⚠️ 页眉**不再印 `[meta]`**（用户 2026-10-15）：那句"来自 ACE（当前片段）· 调式…"是面板上的状态提示，
    // 不该上谱；标题可自定义、留空就不印这一段。
    var head = '1 = ' + (KEY_NAMES[S.key] || '?') + '　' + S.meter.n + '/' + S.meter.d +
      (title ? '　' + title : '');
    var box = document.getElementById('nt-printroot');
    if (!box) {
      box = document.createElement('div');
      box.id = 'nt-printroot';
      document.body.appendChild(box);
    }
    var nLines = 0;
    var html = '<div class="nt-pv-bar">' +
      '<b>导出预览</b>' +
      '<span class="nt-pv-meta">' + pages.length + ' 页 · 每页 ' +
      Math.max.apply(null, pages.map(function (p) { return p.systems.length; })) + ' 行 · 共 ' +
      built.systems.length + ' 行 · ' + (rows === 'both' ? '简谱 + 五线谱' : (rows === 'staff' ? '只要五线谱' : '只要简谱')) +
      ' · ' + (mpl > 0 ? '每行 ' + mpl + ' 小节' : '每行自动') +
      ' · ' + (PRINT.orient === 'portrait' ? 'A4 竖向' : 'A4 横向') + '</span>' +
      '<span style="flex:1"></span>' +
      '<label>导出内容</label><select id="nt-rows-pv">' + rowsOptions(rows) + '</select>' +
      '<label>每行</label><select id="nt-mpl-pv">' + mplOptions(mpl) + '</select>' +
      '<label>纸型</label><select id="nt-paper-pv">' + paperOptions(PRINT.orient) + '</select>' +
      // ⭐ 页眉那三项在预览栏里也能就地改（与上面那排主控件双向同步）
      '<input id="nt-title-pv" autocomplete="off" spellcheck="false" style="width:110px" placeholder="标题" title="谱面标题（留空就不印）" value="' + esc2(title) + '">' +
      '<select id="nt-head-pv" title="页眉：每页印 / 只在第一页印 / 不印（连页码一起）">' + headOptions(headMode) + '</select>' +
      '<label class="nt-ck" title="页眉在印的前提下，是否带「第 x / y 页」"><input type="checkbox" id="nt-headpage-pv"' + (showPageNum ? ' checked' : '') + ' autocomplete="off">页码</label>' +
      '<button class="btn" id="nt-pv-print" type="button" title="调浏览器的打印；在打印对话框里把目标选成「另存为 PDF」">打印 / 另存为 PDF</button>' +
      '<button class="btn ghost" id="nt-pv-close" type="button">关闭</button>' +
      '</div>';
    for (var p = 0; p < pages.length; p++) {
      nLines += pages[p].systems.length;
      // 页眉三档：all = 每页 · first = 只在第一页 · off = 整行不印（连页码一起）
      var showHead = (headMode === 'all') || (headMode === 'first' && p === 0);
      var headHTML = showHead
        ? '<div class="nt-print-head">' + esc2(head) +
          (showPageNum ? '　·　第 ' + (p + 1) + ' / ' + pages.length + ' 页' : '') + '</div>'
        : '';
      html += '<div class="nt-print-page" style="' + pageStyle() + '">' +
        headHTML +
        renderPage(pages[p], built, rows, p === 0) +
        '</div>';
    }
    box.innerHTML = html;
    box.className = 'nt-pv';
    box.style.display = 'block';
    document.getElementById('nt-pv-close').addEventListener('click', closePreview);
    document.getElementById('nt-pv-print').addEventListener('click', function () {
      box.className = 'nt-pv nt-printing';
      window.print();
    });
    var sel = document.getElementById('nt-rows-pv');
    if (sel) sel.addEventListener('change', function () {
      var main = document.getElementById('nt-rows');
      if (main) main.value = sel.value;
      exportScore();                                  // 就地换"导出哪几排"⇒ 重排重画
    });
    var mplSel = document.getElementById('nt-mpl-pv');
    if (mplSel) mplSel.addEventListener('change', function () {
      var main = document.getElementById('nt-mpl');
      if (main) main.value = mplSel.value;
      exportScore();                                  // 就地换"每行几小节"⇒ 重排重画
    });
    var paperSel = document.getElementById('nt-paper-pv');
    if (paperSel) paperSel.addEventListener('change', function () {
      var main = document.getElementById('nt-paper');
      if (main) main.value = paperSel.value;
      exportScore();                                  // 就地换纸型 ⇒ 重排重画
    });
    // ⭐ 页眉三项（标题 / 页眉档 / 页码）：预览栏改了就同步到主控件并重排
    var titlePv = document.getElementById('nt-title-pv');
    if (titlePv) titlePv.addEventListener('change', function () {
      var main = document.getElementById('nt-title');
      if (main) main.value = titlePv.value;
      exportScore();
    });
    var headPv = document.getElementById('nt-head-pv');
    if (headPv) headPv.addEventListener('change', function () {
      var main = document.getElementById('nt-head');
      if (main) main.value = headPv.value;
      exportScore();
    });
    var headPagePv = document.getElementById('nt-headpage-pv');
    if (headPagePv) headPagePv.addEventListener('change', function () {
      var main = document.getElementById('nt-headpage');
      if (main) main.checked = headPagePv.checked;
      exportScore();
    });
    // ⭐ 用户 2026-10-08：「删除不必要的文字」—— 预览浮层顶上那份 meta 已经写全了，
    // 这里只留一行落款（页数 / 行数 / 纸型），不再复述一遍怎么打印。
    out('<div class="hint ok">已出预览：' + pages.length + ' 页 · 共 ' + nLines + ' 行 · ' +
      (PRINT.orient === 'portrait' ? 'A4 竖向' : 'A4 横向') + '</div>');
  }

  function rowsOptions(cur) {
    var opts = [['both', '简谱 + 五线谱'], ['staff', '只要五线谱'], ['jianpu', '只要简谱']];
    return opts.map(function (o) {
      return '<option value="' + o[0] + '"' + (o[0] === cur ? ' selected' : '') + '>' + o[1] + '</option>';
    }).join('');
  }

  function closePreview() {
    var box = document.getElementById('nt-printroot');
    if (box) { box.style.display = 'none'; box.innerHTML = ''; }
  }

  async function readACE() {
    var v = values();
    busy('读 ACE 当前片段…');
    try {
      var data = await postRun('jianpu.editor', {
        scope: v.scope || 'auto', key: v.key || 'auto', meter: v.meter || (S.meter.n + '/' + S.meter.d),
      });
      var r = (data.calls[0] || {}).json;
      if (!r) throw new Error('回包里没有结果');
      if (!r.elements || !r.elements.length) throw new Error('这个片段里没有音符（或没选中任何音符）');
      S.els = r.elements.map(norm);
      S.sel = S.els.length ? 0 : -1;
      if (typeof r.key === 'number') { S.key = r.key; var ks = document.getElementById('nt-key'); if (ks) ks.value = String(S.key); }
      if (r.meter) {
        var m = /^(\d+)\/(\d+)$/.exec(r.meter);
        if (m) { S.meter = { n: parseInt(m[1], 10), d: parseInt(m[2], 10) }; var mi = document.getElementById('nt-meter'); if (mi) mi.value = r.meter; }
      }
      S.meta = '来自 ' + (r.scope || 'ACE') + '（' + (r.clip || '当前片段') + '）· 调式 ' + (r.keyName || '?') +
        (r.keyTiedRotations && r.keyTiedRotations.length > 1 ? ' ⚠️ 自动检测并列 ' + r.keyTiedRotations.length + ' 个，建议手动指定' : '');
      setMeta();
      draw();
      out('<div class="hint ok">读进来 ' + S.els.length + ' 个元素。' +
        ((r.diagnostics || []).length
          // ⚠️ 每条**各自转义**、再用 `<br>` 拼起来 —— 整串 `esc2(join('<br>'))` 会把 `<br>` 本身也转义掉，
          //    用户在结果框里看到的就是字面的 `<br>`（旧版的毛病）
          ? '<br><span class="nt-warn">' + (r.diagnostics || []).map(esc2).join('<br>') + '</span>' : '') + '</div>');
    } catch (e) {
      out('<div class="hint bad">' + esc2((e && e.message) || e) + '</div>');
    }
  }

  function writeParams(dry) {
    var startEl = document.getElementById('nt-start');
    var targetEl = document.getElementById('nt-target');
    return {
      // ⭐ 「直接把 nt 块拿过来」：输入就是编辑器里这份元素（不再让用户另填一份）
      elements: elemsJSON(),
      key: String(S.key),
      defaultLyric: 'la',
      target: (targetEl && targetEl.value) || 'current',
      startAt: (startEl && startEl.value) || 'playhead',
      dryRun: dry ? '1' : '0',
    };
  }

  async function writeACE(dry) {
    if (!S.els.length) { out('<div class="hint bad">谱是空的。</div>'); return; }
    if (!dry && !window.confirm('真的把这些音符写进 ACE 当前片段？（会真的改动工程，ACE 里可以 Ctrl+Z 撤销）')) return;
    busy(dry ? '预览（一个字节都不写）…' : '正在写进 ACE…');
    try {
      var data = await postRun('jianpu.toNotes', writeParams(dry));
      var r = (data.calls[0] || {}).json;
      if (!data.ok) throw new Error(data.hint || '写失败');
      var lines = [];
      lines.push((dry ? '预览（没写）' : '已写入') + '：' + (r.notesToWrite || 0) + ' 个音符' +
        (r.sustainNotes ? '（其中 ' + r.sustainNotes + ' 个 tenuto 连音）' : '') +
        (r.skipped ? '，跳过 ' + r.skipped + ' 个（休止/无处安放）' : ''));
      // ⭐ 起点：说清它落在**哪一小节的哪条线**上（用户 2026-10-07 的口径：从播放头所在小节开始）
      // ⚠️ 单位口径（用户 2026-10-14）：对外一律「拍」（tick/480），文字里不出现 tick
      if (r.startBar) {
        lines.push('起点：第 ' + r.startBar + ' 小节（第 ' + beatOf(r.startTick) + ' 拍）' +
          (r.playheadTick && r.playheadTick !== r.startTick ? ' —— 播放头 ' + beatOf(r.playheadTick) + ' 拍往下取整到小节线' : ''));
      } else if (r.startTick !== undefined) {
        lines.push('起点：第 ' + beatOf(r.startTick) + ' 拍' + (r.target === 'current' ? '（片段内第 ' + beatOf(r.startTick - r.clipBegin) + ' 拍）' : ''));
      }
      if (r.preview && r.preview.length) lines.push('前几个：' + r.preview.join(' | '));
      if (r.plannedCalls && r.plannedCalls.length) lines.push(r.plannedCalls.join('\n'));
      if (r.diagnostics && r.diagnostics.length) lines.push('⚠️ ' + r.diagnostics.join('\n⚠️ '));
      if (r.note) lines.push(r.note);
      out('<div class="hint ' + (dry ? 'ok' : 'bad') + '">' + esc2(lines.join('\n')) + '</div>' + pre(JSON.stringify(r, null, 2)));
    } catch (e) {
      out('<div class="hint bad">' + esc2((e && e.message) || e) + '</div>');
    }
  }

  async function exportAs(format) {
    if (!S.els.length) { out('<div class="hint bad">谱是空的。</div>'); return; }
    busy('生成中…');
    try {
      var data = await postRun('jianpu.fromClip', {
        elements: elemsJSON(), format: format, key: String(S.key),
        meter: S.meter.n + '/' + S.meter.d, title: (document.getElementById('nt-title') || {}).value || S.title || '', composer: '',
      });
      var r = (data.calls[0] || {}).json;
      if (!data.ok) throw new Error(data.hint || '生成失败');
      S.xml = r.output || '';
      var dl = document.getElementById('nt-dl');
      if (dl) dl.hidden = (format !== 'musicxml' || !S.xml);
      out('<div class="hint ok">' + esc2((r.note || '') + ((r.diagnostics || []).length ? '\n⚠️ ' + r.diagnostics.join('\n⚠️ ') : '')) +
        '</div><div class="dim">共 ' + esc2(String((r.output || '').length)) + ' 字节' +
        (format === 'musicxml' ? '（可点「下载 .musicxml」，也能喂 ACE：`import file` 收 MusicXML）' : '') + '</div>' +
        pre(r.output || ''));
    } catch (e) {
      out('<div class="hint bad">' + esc2((e && e.message) || e) + '</div>');
    }
  }

  function downloadXML() {
    if (!S.xml) return;
    var blob = new Blob([S.xml], { type: 'application/vnd.recordare.musicxml+xml' });
    var a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = 'acetool-notation.musicxml';
    document.body.appendChild(a); a.click();
    setTimeout(function () { URL.revokeObjectURL(a.href); a.remove(); }, 1000);
  }

  // 调试口：把纯函数暴露出去，好在 Node 里直接跑渲染（`vendor/render-check.js` 就是这么用的）。
  // 这些函数都不碰 DOM，所以可以脱离浏览器验证「五线谱画在哪」这类几何问题。
  window.NotationPanel = {
    html: panelHTML, bind: bind,
    /** 选中格左右移（给面板快捷键 `item.prev/next` 用；= 工具条那两个按钮的语义）。 */
    moveSel: function (d) {
      if (!S.els.length) return;
      if (S.sel < 0) { S.sel = 0; draw(); return; }
      var j = S.sel + d;
      if (j < 0 || j >= S.els.length) return;
      S.sel = j;
      draw();
    },
    _test: {
      norm: norm, beats: beats, ticks: ticks, midiOf: midiOf, diatonicOf: diatonicOf,
      letterOctave: letterOctave, keySig: keySig, layout: layout, accidentalToDraw: accidentalToDraw,
      fifthsOf: fifthsOf, tonicLetterOf: tonicLetterOf,
      renderJianpu: renderJianpu, renderStaff: renderStaff, DEMO: DEMO, staffGeom: staffGeom,
      headName: headName, restName: restName,
      /** 谱 → 音符（干跑/真写）。⚠️ 面板上只有一个真写按钮，干跑没有按钮了 ⇒ 脚本与自检走这个口。 */
      writeACE: writeACE,
      elemsJSONOf: elemsJSONOf,
      // 导出那一套（**导出多行谱**的纯函数）—— 给自检用：能脱离 DOM 验"每行拍数"这类判据
      buildSystems: buildSystems, splitBeats: splitBeats, colBeats: colBeats, colDur: colDur, wForBeats: wForBeats,
      GLYPHS: (typeof GLYPHS !== 'undefined' ? GLYPHS : null),
    },
  };
})();
