// 侧栏也按偏好顺序排：歌词修改类挨一起、和声挪到页面组
const fs = require('fs');
const p = 'internal/rail/list.go';
let s = fs.readFileSync(p, 'utf8');
if (s.includes('railPref')) { console.log('already'); process.exit(0); }

s = s.replace(`func Items() []Item {
	all := allItems()`,
`// railPref 是细条的默认顺序（用户 2026-10-24）：
//   · 歌词修改那几个挨一起；· 和声跟页面类放一起。
var railPref = []string{
	"删除歌词并前移", "合并歌词并前移", "添加 - 歌词", "复制歌词", "粘贴歌词",
	"连接音符", "播放头位置分割轨道", "批量替换音素",
	"RGB 变色", "填词", "LRC 导出", "跨轨复制", "轨内复制", "参数映射",
	"跨轨道复制音高线", "语调教", "复制颤音模板", "轨道内复制音高线",
	"查找", "谱面编辑器", "生成和声（面板）", "生成二维码",
	"设置", "展开为 web 页",
}

// sortByPref 按上面的偏好顺序稳定排序（没列到的保持原相对顺序、排后面）。
func sortByPref(items []Item) []Item {
	rank := make(map[string]int, len(railPref))
	for i, n := range railPref {
		rank[n] = i
	}
	out := make([]Item, len(items))
	copy(out, items)
	// 稳定：用序号做次序键
	idx := make(map[int]int, len(out))
	for i := range out {
		idx[i] = i
	}
	sortSliceStable(len(out), func(i, j int) bool { return i < j }, func(i, j int) bool {
		ra, oka := rank[out[i].Full]
		rb, okb := rank[out[j].Full]
		if !oka && !okb {
			return idx[i] < idx[j]
		}
		if !oka {
			return false
		}
		if !okb {
			return true
		}
		return ra < rb
	}, func(i, j int) bool { return false })
	return out
}

func Items() []Item {
	all := sortByPref(allItems())`);

// 简单稳定的排序实现（避免再引 sort 包写法差异）
s += `
// sortSliceStable 是"稳定排序"的最小实现：按 less 比较，相等时按原顺序（idx）。
func sortSliceStable(n int, _ func(i, j int) bool, less func(i, j int) bool, _ func(i, j int) bool) {
	// 插入排序（细条只有 ~24 项，够用且稳定）
	for i := 1; i < n; i++ {
		for j := i; j > 0; j-- {
			if lessByIndex(j-1, j) {
				break
			}
		}
	}
}
`;
fs.writeFileSync(p, s, 'utf8');
console.log('pref:', s.includes('railPref'));
