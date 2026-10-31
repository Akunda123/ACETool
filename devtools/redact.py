# 源码脱敏：把本机路径/联系信息/工程名换成占位符（只改文本，不动逻辑）
#
# 用法：python devtools/redact.py
#
# ⚠️ 两个例外：
#   ① 关于页（internal/web/assets/settings.html）保留真实联系方式 —— 用户 2026-10-31「关于页不改」；
#   ② 本脚本自己（下面的模式是用占位符拼出来的，避免把自己也脱敏掉）。
import io
import re
import subprocess

# 只处理 git 跟踪的文本文件
files = subprocess.run(['git', '-c', 'core.quotepath=false', 'ls-files'],
                       capture_output=True, text=True, encoding='utf-8').stdout.split('\n')
files = [f for f in files if f.strip()]

SKIP_FILES = {
    'internal/web/assets/settings.html',  # 关于页：保留 B 站主页与邮箱
    'devtools/redact.py',                 # 本脚本
}

BIN = re.compile(r'\.(png|jpg|jpeg|gif|ico|zip|exe|dll|wav|mp3|ttf|otf|woff2?)$', re.I)

# 敏感片段用字符串拼接，避免本脚本被自己匹配到
U = 'wuji' + 'chao'
PROJ = 'lala' + '-resaved'
UID = '1213' + '1593'
MAIL = '1073243952' + '@qq.com'

REPL = [
    # 本机绝对路径（正反斜杠、大小写都覆盖）
    (re.compile(r'[Cc]:[\\/]+Users[\\/]+' + U, re.I), lambda m: 'C:/Users/<user>'),
    # 联系信息（关于页已排除，这里只清别处的残留）
    (re.compile(re.escape(MAIL)), lambda m: 'contact@example.com'),
    (re.compile(r'space\.bilibili\.com/' + UID), lambda m: 'space.bilibili.com/<uid>'),
    # 本机工程目录名 / 技能目录名
    (re.compile(PROJ), lambda m: '<project-dir>'),
    (re.compile('SV' + 'Agent'), lambda m: '<skills-dir>'),
    # 剩下的用户名出现（兜底）
    (re.compile(U), lambda m: '<user>'),
]

changed = []
for f in files:
    if BIN.search(f) or f in SKIP_FILES:
        continue
    try:
        s = io.open(f, encoding='utf-8').read()
    except (UnicodeDecodeError, FileNotFoundError):
        continue
    orig = s
    for pat, rep in REPL:
        s = pat.sub(rep, s)
    if s != orig:
        io.open(f, 'w', encoding='utf-8', newline='').write(s)
        n = sum(len(p.findall(orig)) for p, _ in REPL)
        changed.append((f, n))

for f, n in changed:
    print('%-52s %d 处' % (f, n))
print('共修改', len(changed), '个文件（关于页与脚本自身已跳过）')
