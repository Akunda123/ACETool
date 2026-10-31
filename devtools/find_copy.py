# 优化「查找」的表述：说清"检索音符 / 参数属性，并标记命中项"
import io

# ① 工具说明（主面板 hover + 页面说明都来自这里）
p = 'internal/tools/find.go'
s = io.open(p, encoding='utf-8').read()
old_start = s.index('		ID: "find.notes", Category: "查找",')
old_end = s.index('		Kind:   KindRead,', old_start)
new = '''		ID: "find.notes", Category: "查找",
		Title: "查找音符与参数属性",
		Desc: "检索**音符属性**与**参数曲线**，把命中的属性直接标出来：" +
			"条件为 大于 / 小于 / 等于 / 包含，范围可选 当前片段 / 轨道 / 工程。" +
			"命中项在缩略图上高亮标出，并逐条列出对应属性值（点一下跳到该处）。" +
			"⚠️ 属性列表照抄原脚本（音符 22 项 + 参数 12 项）：ACE 做不到的项**保留在列表里**并说明缺什么，" +
			"不拿「0 命中」糊弄。⚠️ 只读；参数命中的是**一段（run）**而非一个点（曲线每 tick 一个值）。",
'''
s = s[:old_start] + new + s[old_end:]
io.open(p, 'w', encoding='utf-8').write(s)
print('tool desc:', '检索**音符属性**' in s)

# ② 页面顶部加一句副标题
p = 'internal/web/assets/find.html'
s = io.open(p, encoding='utf-8').read()
s = s.replace('    <h1>查找</h1>',
              '    <h1>查找</h1>\n    <span class="sub">检索音符与参数属性，命中项在缩略图上标记</span>')
if '.sub{' not in s:
    s = s.replace('    .wrap .findpanel{max-width:none;margin:0}',
                  '    .wrap .findpanel{max-width:none;margin:0}\n    .sub{font-size:12px;color:#9aa0aa;margin-left:8px}')
io.open(p, 'w', encoding='utf-8').write(s)
print('page sub:', '命中项在缩略图上标记' in s)
