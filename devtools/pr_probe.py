# 探查 prproj 结构：是否含字幕/文本，以及它们存在哪
import gzip, re, sys, os

p = r'C:/Users/<user>\Documents\Adobe\Premiere Pro\22.0\acetool.prproj'
raw = open(p, 'rb').read()
print('size', len(raw), 'head', raw[:8])

if raw[:2] == b'\x1f\x8b':
    xml = gzip.decompress(raw)
else:
    xml = raw
txt = xml.decode('utf-8', 'ignore')
print('xml bytes', len(txt))

for kw in ['Caption', 'CaptionClip', 'Text', 'Transcript', 'Subtitle', 'Graphic', 'Title']:
    print('%-12s %d' % (kw, txt.count(kw)))

# 找出所有可疑的“字幕/文本”节点名
tags = {}
for m in re.finditer(r'<([A-Za-z0-9_]+)[ >]', txt):
    t = m.group(1)
    if re.search(r'(?i)caption|text|subtitle|title|graphic', t):
        tags[t] = tags.get(t, 0) + 1
print('tags:', sorted(tags.items(), key=lambda x: -x[1])[:20])

# 打印含 Text/Caption 的片段上下文
hits = list(re.finditer(r'(?i)<(Caption[^ >]*|TextGraphic[^ >]*|Graphic[^ >]*)', txt))
print('nodes found', len(hits))
for h in hits[:5]:
    s = max(0, h.start() - 200)
    print('-----', txt[s:h.start() + 800].replace('\n', ' ')[:900])

# 抓所有可能的字符串内容
strings = re.findall(r'<String>([^<]{2,200})</String>', txt)
print('String count', len(strings))
print(strings[:20])
