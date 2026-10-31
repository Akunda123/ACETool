// 全站统一的 canvas 缩略图绘制（用户 2026-10-23：「那都统一成 canvas」）
//   用法：CanvasMap.draw(el, data, opt) → { geom, X(tick), tickAt(px), hit(px,py) }
//   data: { durationTick, lastTick, ticksPerBar, tracks:[{trackIndex,name,clips:[{begin,end,notes,minPitch,maxPitch,uuid,clipIndex}]}] }
//   opt:  { tracks?:[trackIndex…] 只画这些轨（默认全画有片段的）,
//           hi?: trackIndex 高亮, picks?: [ {begin,end,color,stroke} ] 覆盖层色带,
//           row?: 行高（默认 34）, height?: 总高（默认按行数算）,
//           onPick?: function(trackIndex, clip) 点击片段回调 }
//   绘制内容与原先各页自己那份一致：小节尺 + 一条轨一行 + 片段块 + 音符（按 min/maxPitch 摆）
(function () {
  var RULER = 16, PAD = 8, ROW = 34;

  function esc(s) { return String(s == null ? '' : s); }

  // 没数据时别留一块默认的 300x150 白板（看着像坏了）：直接画一句人话。
  function empty(el, msg) {
    var dpr = window.devicePixelRatio || 1;
    var w = Math.max(320, el.clientWidth || 0), h = Math.max(48, el.clientHeight || 0);
    el.width = Math.round(w * dpr); el.height = Math.round(h * dpr);
    var g = el.getContext('2d');
    g.setTransform(dpr, 0, 0, dpr, 0, 0);
    g.fillStyle = '#141a21'; g.fillRect(0, 0, w, h);
    g.fillStyle = '#8a97a6';
    g.font = '12px "Microsoft YaHei",system-ui,sans-serif';
    g.textAlign = 'center'; g.textBaseline = 'middle';
    g.fillText(msg || 'ACE 没开或没有数据 —— 打开 ACE 后点「刷新」', w / 2, h / 2);
    return { X: function () { return 0; }, geom: { scale: 0, t0: 0, gutter: 0 }, empty: true };
  }

  function draw(el, data, opt) {
    if (!el) return null;
    if (!data || !data.tracks) {
      return empty(el, (opt && opt.emptyText) || 'ACE 没开或没有数据 —— 打开 ACE 后点「刷新」');
    }
    opt = opt || {};
    var rows = (data.tracks || []).filter(function (t) {
      if (opt.tracks && opt.tracks.length && opt.tracks.indexOf(t.trackIndex) < 0) return false;
      return (t.clips || []).length > 0;
    });
    if (!rows.length) {
      return empty(el, (opt && opt.emptyText) || '这条轨上没有片段 —— 换一条轨或先打开 ACE');
    }
    var cssW = el.clientWidth || 620;
    var rowH = opt.row || ROW;
    // 用户 2026-10-23：「轨道数应该动态，单轨道不要显示整个面板」⇒ 高度按**行数**算。
    var cssH = RULER + Math.max(1, rows.length) * rowH + 8;
    var DPR = Math.max(1, Math.min(2, window.devicePixelRatio || 1));
    el.width = Math.round(cssW * DPR); el.height = Math.round(cssH * DPR);
    el.style.height = cssH + 'px';
    var g = el.getContext('2d');
    g.setTransform(DPR, 0, 0, DPR, 0, 0);
    g.clearRect(0, 0, cssW, cssH);

    var w = Math.max(1, cssW - PAD * 2);
    var total = Math.max(data.durationTick || 0, data.lastTick || 0, 1920);
    (data.tracks || []).forEach(function (t) {
      (t.clips || []).forEach(function (c) { total = Math.max(total, c.end || 0); });
    });
    // ⚠️ 覆盖层（源/目标框、预览块）可能落在片段之外 ⇒ 必须一起算，否则会被画布右缘裁掉
    (opt.picks || []).forEach(function (it) {
      if (!it) return;
      total = Math.max(total, it.end || 0, it.begin || 0);
    });
    var tpb = data.ticksPerBar || 1920;
    var X = function (t) { return PAD + t / total * w; };
    var tickAt = function (px) { return Math.max(0, Math.min(total, (px - PAD) / w * total)); };

    // 小节尺
    var bars = Math.max(1, Math.ceil(total / tpb));
    var step = bars > 60 ? 8 : (bars > 24 ? 4 : (bars > 12 ? 2 : 1));
    g.font = '9px system-ui,sans-serif';
    for (var b = 0; b <= bars; b++) {
      var x = Math.round(X(b * tpb)) + .5;
      g.strokeStyle = (b % step === 0) ? '#3f434a' : '#33373e';
      g.beginPath(); g.moveTo(x, RULER); g.lineTo(x, cssH); g.stroke();
      if (b % step === 0) { g.fillStyle = '#9498a0'; g.fillText(String(b + 1), x + 2, 11); }
    }
    g.strokeStyle = '#3f434a';
    g.beginPath(); g.moveTo(0, RULER + .5); g.lineTo(cssW, RULER + .5); g.stroke();

    // 一条轨一行
    rows.forEach(function (t, i) {
      var top = RULER + i * rowH + 2, bot = top + rowH - 5;
      var his = Array.isArray(opt.hi) ? opt.hi : [opt.hi];
      var hm = opt.hiMap || {};
      var isHi = his.indexOf(t.trackIndex) >= 0 || hm[t.trackIndex] != null;
      var hiRGB = hm[t.trackIndex] || '26,115,232';   // 默认蓝；源轨绿 / 目标轨琥珀由调用方给
      g.fillStyle = isHi ? 'rgba(' + hiRGB + ',.12)' : 'rgba(148,152,160,.06)';
      g.fillRect(0, top - 2, cssW, rowH);
      g.fillStyle = isHi ? 'rgb(' + hiRGB + ')' : '#9498a0';
      g.font = '10px system-ui,sans-serif';
      g.fillText(String(t.trackIndex) + ' ' + String(t.name || '').slice(0, 10), 2, top + 10);
      (t.clips || []).forEach(function (cl) {
        var x0 = X(cl.begin), x1 = Math.max(x0 + 2, X(cl.end));
        g.fillStyle = isHi ? 'rgba(94,189,122,.14)' : 'rgba(148,152,160,.08)';
        g.fillRect(x0, top + 8, x1 - x0, bot - top - 8);
        g.strokeStyle = isHi ? 'rgba(94,189,122,.7)' : 'rgba(148,152,160,.4)';
        g.strokeRect(Math.round(x0) + .5, Math.round(top + 8) + .5,
          Math.max(1, Math.round(x1 - x0)) - 1, bot - top - 9);
        var lo = cl.minPitch || 48, hi2 = cl.maxPitch || 72, span = Math.max(1, hi2 - lo);
        (cl.notes || []).forEach(function (n) {
          var abs = cl.begin + n.pos;
          var y = bot - (n.pitch - lo) / span * (bot - top - 14) - 4;
          var nx0 = X(abs), nx1 = Math.max(nx0 + 1.2, X(abs + n.dur));
          // 用户 2026-10-23：「单轨道看不见音符」⇒ 音符改**实心**、高度随行高（不再固定 4px）
          var nh = Math.max(3, Math.min(9, Math.round(rowH * 0.11)));
          g.fillStyle = isHi ? 'rgba(170,215,255,.95)' : 'rgba(160,166,175,.85)';
          g.fillRect(nx0, y, Math.max(1.2, nx1 - nx0), nh);
        });
      });
    });

    // 覆盖层色带（范围 / 预览块）
    (opt.picks || []).forEach(function (it) {
      if (!it) return;
      var a = X(it.begin), b2 = X(it.end);
      var top2 = RULER, bot2 = cssH;
      // ⭐ 带 track 的色带只覆盖**那一行**（跨轨复制：源在源轨行、目标在目标轨行）
      if (it.track != null) {
        var ri = -1;
        for (var k = 0; k < rows.length; k++) { if (rows[k].trackIndex === it.track) { ri = k; break; } }
        if (ri >= 0) {
          top2 = RULER + ri * rowH + 2;
          bot2 = top2 + rowH - 5;
        }
      }
      g.fillStyle = it.color || 'rgba(26,115,232,.18)';
      g.fillRect(Math.min(a, b2), top2, Math.max(1, Math.abs(b2 - a)), bot2 - top2);
      g.strokeStyle = it.stroke || '#1a73e8'; g.lineWidth = 1.5;
      g.beginPath();
      g.moveTo(Math.min(a, b2) + .5, top2); g.lineTo(Math.min(a, b2) + .5, bot2);
      g.moveTo(Math.max(a, b2) - .5, top2); g.lineTo(Math.max(a, b2) - .5, bot2);
      g.stroke(); g.lineWidth = 1;
    });

    var geom = { X: X, tickAt: tickAt, tpb: tpb, total: total, rowH: rowH, ruler: RULER, rows: rows };
    if (opt.onPick) {
      el.onclick = function (ev) {
        var r = el.getBoundingClientRect();
        var px = ev.clientX - r.left, py = ev.clientY - r.top;
        var row = Math.floor((py - RULER) / rowH);
        var t = rows[row];
        if (!t) return;
        var tick = tickAt(px);
        var hit = (t.clips || []).filter(function (c) { return tick >= c.begin && tick <= c.end; })[0];
        if (hit) opt.onPick(t.trackIndex, hit);
      };
    }
    return { geom: geom, X: X, tickAt: tickAt, hitRows: rows };
  }

  window.CanvasMap = { draw: draw };
})();
