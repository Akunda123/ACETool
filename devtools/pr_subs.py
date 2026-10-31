# 从 prproj 里提取字幕：时间码 + 文本（FormattedTextData 是 base64 序列化文本）
import gzip, re, base64, xml.etree.ElementTree as ET

P = r'C:/Users/<user>\Documents\Adobe\Premiere Pro\22.0\acetool.prproj'
TICKS = 254016000000  # 每秒

xml = gzip.decompress(open(P, 'rb').read()).decode('utf-8', 'ignore')
root = ET.fromstring(xml)

by_id = {}
for el in root.iter():
    oid = el.get('ObjectID')
    if oid:
        by_id[oid] = el

def find_text(el):
    """从 block 里拿 FormattedTextData 的 base64 并抽文本"""
    for ft in el.iter('FormattedTextData'):
        b64 = (ft.text or '').strip()
        if not b64:
            continue
        raw = base64.b64decode(b64)
        s = raw.decode('utf-8', 'ignore')
        # 抓连续的中英文/标点串（去掉控制字符）
        runs = re.findall(r'[\u4e00-\u9fff\u3000-\u303fA-Za-z0-9，。！？、；：“”‘’（）《》…—\-\s]{4,}', s)
        runs = [r.strip() for r in runs if len(r.strip()) >= 2]
        if runs:
            # 取最长的一段当正文（其余是样式名/字体名）
            return max(runs, key=len)
    return ''

subs = []
for it in root.iter('CaptionDataClipTrackItem'):
    start = end = None
    for tag in ('Start', 'End'):
        e = it.find('.//TrackItem/' + tag)
        if e is not None:
            v = int(e.text)
            if tag == 'Start':
                start = v
            else:
                end = v
    text = ''
    for bv in it.iter('BlockVectorItem'):
        ref = bv.get('ObjectRef')
        if ref and ref in by_id:
            t = find_text(by_id[ref])
            if len(t) > len(text):
                text = t
    subs.append((start, end, text))

subs = [s for s in subs if s[0] is not None and s[2]]
subs.sort(key=lambda x: x[0])
print('字幕条数:', len(subs))
import io
out = io.open(r"docs/pr_subtitles.txt", "w", encoding="utf-8")
for i, (a, b, t) in enumerate(subs, 1):
    out.write("%02d  %7.2f --> %7.2f  (%.2fs)  %s\n" % (i, a / TICKS, b / TICKS, (b - a) / TICKS, t))
out.close()

def srt_time(v):
    h = int(v // 3600); m = int(v % 3600 // 60); sec = v % 60
    return "%02d:%02d:%06.3f" % (h, m, sec)

with io.open(r"docs/pr_subtitles.srt", "w", encoding="utf-8") as f:
    for i, (a, b, t) in enumerate(subs, 1):
        f.write("%d\n%s --> %s\n%s\n\n" % (i, srt_time(a / TICKS).replace(".", ","), srt_time(b / TICKS).replace(".", ","), t))
print("written", len(subs))
