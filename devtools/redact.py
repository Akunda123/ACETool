# 源码脱敏：把本机路径/联系信息/工程名换成占位符（只改文本，不动逻辑）
import io
import re
import subprocess

# 只处理 git 跟踪的文本文件
files = subprocess.run(['git', '-c', 'core.quotepath=false', 'ls-files'], capture_output=True, text=True, encoding='utf-8').stdout.split('\n')
files = [f for f in files if f.strip()]

BIN = re.compile(r'\.(png|jpg|jpeg|gif|ico|zip|exe|dll|wav|mp3|ttf|otf|woff2?)$', re.I)

REPL = [
    # 本机绝对路径（正反斜杠、大小写都要覆盖）
    (re.compile(r'[Cc]:[\\/]+Users[\\/]+wujichao', re.I), lambda m: 'C:/Users/<user>'),
    # 邮箱 / B 站 UID
    (re.compile(r'1073243952@qq\.com'), lambda m: 'contact@example.com'),
    (re.compile(r'space\.bilibili\.com/12131593'), lambda m: 'space.bilibili.com/<uid>'),
    # 本机工程目录名
    (re.compile(r'lala-resaved'), lambda m: '<project-dir>'),
    # 剩下的用户名出现（路径已被上面吃掉，这里兜底）
    (re.compile(r'wujichao'), lambda m: '<user>'),
]

changed = []
for f in files:
    if BIN.search(f):
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
print('共修改', len(changed), '个文件')
