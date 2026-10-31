// 和声：范围支持「整个轨道（多片段）」
const fs = require('fs');
const p = 'internal/tools/harmony.go';
let s = fs.readFileSync(p, 'utf8');

// ① scope 选项
s = s.replace('{Value: "bars", Label: "指定小节（beginBar..endBar，1 起、含两端）"},',
  '{Value: "bars", Label: "指定小节（beginBar..endBar，1 起、含两端）"},\n\t\t\t\t{Value: "track", Label: "整个轨道（这条轨的所有片段）"},');

// ② move 加 Abs
s = s.replace('type harmonyMove struct {', 'type harmonyMove struct {\n\tAbs int64 `json:"absTick,omitempty"` // 工程绝对位置（整轨范围跨片段时用）');

// ③ 整轨范围：读这条轨所有片段的音符
s = s.replace(`	if scopeMode == "bars" {`,
`	if scopeMode == "track" {
		// ⭐ 用户 2026-10-24：「范围应该允许当前整个轨道（多个片段）」
		clips, cerr := clipListBrief(ctx, rd, sc.TrackIndex)
		if cerr != nil {
			return nil, cerr
		}
		if len(clips) == 0 {
			return nil, fmt.Errorf("这条轨上没有片段")
		}
		var all []xcopyNote
		absOf := map[string]int64{}
		for _, c := range clips {
			nc, nerr := readClipNotes(ctx, rd, sc.TrackIndex, c)
			if nerr != nil {
				return nil, nerr
			}
			for _, n := range nc.Notes {
				absOf[n.NoteUUID] = c.Begin + n.Pos
				all = append(all, n)
			}
		}
		sc.Notes = all
		sc.Which = fmt.Sprintf("整个轨道（%d 个片段、%d 个音符）", len(clips), len(all))
		sc.All = len(all)
		defer func() { /* absOf 在下面循环里用 */ }()
		harmonyAbs = absOf
	}
	if scopeMode == "bars" {`);

// ④ move 里带上 Abs
s = s.replace(`		rep.Moves = append(rep.Moves, harmonyMove{NoteUUID: n.NoteUUID, Pos: n.Pos, Lyric: n.Lyric,
			From: n.Pitch, To: to, Adj: adj, Clamped: clamped})`,
`		rep.Moves = append(rep.Moves, harmonyMove{NoteUUID: n.NoteUUID, Pos: n.Pos, Abs: harmonyAbs[n.NoteUUID],
			Lyric: n.Lyric, From: n.Pitch, To: to, Adj: adj, Clamped: clamped})`);

// ⑤ 新轨映射：按「绝对位置 + 音高」在所有片段里找
s = s.replace(`		nc, err := readClipNotes(ctx, rd, dup.TrackIndex, clips[hit])
		if err != nil {
			return rep, err
		}
		var mapped []harmonyMove
		for _, m := range rep.Moves {
			found := ""
			for _, d := range nc.Notes {
				if d.Pos == m.Pos && d.Pitch == m.From {
					found = d.NoteUUID
					break
				}
			}`,
`		_ = hit
		type key struct {
			abs   int64
			pitch int
		}
		byAbs := map[key]string{}
		for _, c := range clips {
			nc, nerr := readClipNotes(ctx, rd, dup.TrackIndex, c)
			if nerr != nil {
				return rep, nerr
			}
			for _, d := range nc.Notes {
				byAbs[key{c.Begin + d.Pos, d.Pitch}] = d.NoteUUID
			}
		}
		var mapped []harmonyMove
		for _, m := range rep.Moves {
			found := ""
			abs := m.Abs
			if abs == 0 {
				abs = pd.scope.Begin + m.Pos
			}
			if u := byAbs[key{abs, m.From}]; u != "" {
				found = u
			}`);
fs.writeFileSync(p, s, 'utf8');
console.log('ok:', s.includes('整个轨道（这条轨的所有片段）'), s.includes('byAbs'), s.includes('harmonyAbs'));
