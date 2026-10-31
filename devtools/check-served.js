// 「面板真的在发盘上那份资源吗？」—— 挡住**忘了重新构建**这类假成功。
//
// 起因（本项目真踩过）：`internal/web/assets/*` 是 `go:embed` 进 exe 的。
// 改完 HTML 只重启、不重新 `go build` ⇒ 浏览器拿到的还是**旧的那份**，
// 而你在编辑器里怎么看都是新的 —— 最容易得出"我改了但没生效"的错误结论。
// 这个脚本把盘上每一份资源与正在运行的面板实际发出的字节**逐个比对**（长度 + 内容）。
//
// 用法：node devtools/check-served.js [url]
const fs = require('fs');
const path = require('path');
const os = require('os');

function baseUrl() {
  if (process.argv[2]) return process.argv[2].replace(/\/$/, '');
  const p = path.join(process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'ACETool', 'panel.json');
  return JSON.parse(fs.readFileSync(p, 'utf8')).url.replace(/\/$/, '');
}

const A = path.join(__dirname, '..', 'internal', 'web', 'assets');

// ⭐ 2026-10-17 用户：「停用那个默认的 web 页面，现在起只用主页面」⇒ `/` 与 `/index.html`
// 都 **302 到 `/home.html`**。所以 `index.html` 不再按"逐字节发出"来比（它会跳到主页面），
// 改成单独断言那两条跳转（见下面 `entry()`）。
const NOT_SERVED = ['index.html'];

(async () => {
  const B = baseUrl();
  console.log('面板：' + B);

  // 入口：`/` 与 `/index.html` 必须都 302 到 /home.html（`redirect:'manual'` 才看得到 302 本身）
  const entry = async (p) => {
    const r = await fetch(B + p, { redirect: 'manual' });
    return { status: r.status, loc: r.headers.get('location') || '' };
  };
  for (const p of ['/', '/index.html']) {
    try {
      const e = await entry(p);
      const ok = e.status === 302 && /\/home\.html$/.test(e.loc);
      console.log((ok ? '  ok   ' : '  FAIL ') + '入口 ' + p + ' → ' + e.status + ' ' + e.loc +
        (ok ? '' : '（期望 302 → /home.html）'));
      if (!ok) process.exitCode = 1;
    } catch (ex) {
      console.log('  FAIL 入口 ' + p + ' 取不到：' + ex.message);
      process.exitCode = 1;
    }
  }
  // 主页面本身必须是 200
  const home = await fetch(B + '/home.html');
  console.log((home.status === 200 ? '  ok   ' : '  FAIL ') + '主页面 /home.html → ' + home.status);
  if (home.status !== 200) process.exitCode = 1;

  const files = fs.readdirSync(A).filter((f) => /\.(html|js|css|png)$/.test(f) && NOT_SERVED.indexOf(f) < 0);
  let bad = 0;
  for (const f of files) {
    const diskBuf = fs.readFileSync(path.join(A, f));
    const isBin = /\.png$/.test(f);
    const disk = isBin ? '' : diskBuf.toString('utf8');
    let served = '';
    let servedBuf = null;
    try {
      const r = await fetch(B + '/' + f);
      servedBuf = Buffer.from(await r.arrayBuffer());
      served = isBin ? '' : servedBuf.toString('utf8');
    } catch (e) {
      console.log('  FAIL ' + f + ' 取不到：' + e.message);
      bad++;
      continue;
    }
    const same = isBin ? diskBuf.equals(servedBuf) : disk === served;
    // 只报不一致的（一致的太多，吵）
    if (!same) {
      console.log('  FAIL ' + f + ' 盘上 ' + diskBuf.length + ' B ≠ 面板 ' + servedBuf.length + ' B ⇒ **exe 没重新构建**');
      bad++;
    }
  }
  console.log(bad ? '\n' + bad + ' 份资源与盘上不一致（先 go build 再重启）'
    : '\n' + files.length + ' 份资源与盘上逐字一致（embed 是新的）' +
      '；`index.html` 不再逐字节比 —— 它已停用，`/` 与 `/index.html` 都 302 到主页面');
  if (bad) process.exitCode = 1; // ⚠️ 别写成 `process.exitCode = bad ? 1 : 0`：会把前面入口断言设的 1 抹掉
})().catch((e) => { console.error('崩了：' + e.stack); process.exitCode = 1; });
