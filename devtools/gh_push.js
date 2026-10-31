// 用 GitHub REST API 推送当前工作区（github.com 的 git 端口被挡，但 api.github.com 可达）。
// 用法：node devtools/gh_push.js
const { execFileSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const TOKEN = process.env.GH_TOKEN || execFileSync('gh', ['auth', 'token'], { encoding: 'utf8' }).trim();
const REPO = 'Akunda123/ACETool';
const BRANCH = 'main';
const API = 'https://api.github.com';

async function api(method, url, body, raw) {
  for (let attempt = 0; attempt < 8; attempt++) {
    const res = await fetch(API + url, {
      method,
      headers: {
        Authorization: 'Bearer ' + TOKEN,
        Accept: 'application/vnd.github+json',
        'User-Agent': 'ACETool-push',
        ...(body ? { 'Content-Type': 'application/json' } : {}),
      },
      body: body ? JSON.stringify(body) : undefined,
    });
    const text = await res.text();
    if ((res.status === 403 || res.status === 429) && /rate limit/i.test(text)) {
      const wait = 15000 * (attempt + 1);
      console.log('  限流，等 ' + Math.round(wait / 1000) + 's');
      await new Promise((r) => setTimeout(r, wait));
      continue;
    }
    let json = null;
    try { json = JSON.parse(text); } catch (e) { }
    if (!res.ok) throw new Error(method + ' ' + url + ' → ' + res.status + ' ' + text.slice(0, 300));
    return raw ? text : json;
  }
  throw new Error('rate limited: ' + method + ' ' + url);
}

(async () => {
  const root = process.cwd();
  const files = execFileSync('git', ['-c', 'core.quotepath=false', 'ls-files'], { encoding: 'utf8' }).split('\n').filter(Boolean);
  console.log('文件数:', files.length);

  // 已有提交就取父提交；空仓库先用 Contents API 造一个初始提交（否则 blobs 会 409）
  let parents = [];
  try {
    const ref = await api('GET', `/repos/${REPO}/git/ref/heads/${BRANCH}`);
    parents = [ref.object.sha];
  } catch (e) {
    console.log('空仓库：先造初始 README 提交');
    const seed = await api('PUT', `/repos/${REPO}/contents/README.md`, {
      message: 'init', content: Buffer.from('# ACETool\n').toString('base64'),
    });
    parents = [seed.commit.sha];
  }

  const tree = [];
  let i = 0;
  // 并发 6 个 blob，746 个文件才不至于太慢
  const queue = files.slice();
  async function worker() {
    while (queue.length) {
      const f = queue.shift();
      const abs = path.join(root, f);
      if (!fs.existsSync(abs)) continue;
      const buf = fs.readFileSync(abs);
      const isText = !buf.includes(0) && !/\.(png|jpg|jpeg|gif|ico|zip|exe|dll|wav|mp3|ttf|otf)$/i.test(f);
      const blob = await api('POST', `/repos/${REPO}/git/blobs`,
        isText ? { content: buf.toString('utf8'), encoding: 'utf-8' } : { content: buf.toString('base64'), encoding: 'base64' });
      tree.push({ path: f.replace(/\\/g, '/'), mode: '100644', type: 'blob', sha: blob.sha });
      if (++i % 50 === 0) console.log('  blobs', i);
      await new Promise((r) => setTimeout(r, 250));
    }
  }
  await worker();
  console.log('blobs 完成:', tree.length);

  const newTree = await api('POST', `/repos/${REPO}/git/trees`, { tree });
  const commit = await api('POST', `/repos/${REPO}/git/commits`, {
    message: 'ACETool 1.1.0：音高线转锚点、配对阈值、缩略图交互修复、更新检测',
    tree: newTree.sha,
    parents,
  });
  if (parents.length) {
    await api('PATCH', `/repos/${REPO}/git/refs/heads/${BRANCH}`, { sha: commit.sha, force: true });
  } else {
    await api('POST', `/repos/${REPO}/git/refs`, { ref: `refs/heads/${BRANCH}`, sha: commit.sha });
  }
  console.log('推送完成 commit=' + commit.sha);
})().catch((e) => { console.error('失败:', e.message); process.exit(1); });
