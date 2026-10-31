// 增量推送：只推指定文件（用 base_tree 保持其余文件不动）。
// 用法：node devtools/gh_push_inc.js <文件1> <文件2> ...
const { execFileSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const TOKEN = process.env.GH_TOKEN || execFileSync('gh', ['auth', 'token'], { encoding: 'utf8' }).trim();
const REPO = 'Akunda123/ACETool';
const BRANCH = 'main';
const API = 'https://api.github.com';

async function api(method, url, body) {
  for (let attempt = 0; attempt < 6; attempt++) {
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
      const wait = 20000 * (attempt + 1);
      console.log('限流，等 ' + Math.round(wait / 1000) + 's');
      await new Promise((r) => setTimeout(r, wait));
      continue;
    }
    let json = null;
    try { json = JSON.parse(text); } catch (e) { }
    if (!res.ok) throw new Error(method + ' ' + url + ' → ' + res.status + ' ' + text.slice(0, 300));
    return json;
  }
  throw new Error('rate limited');
}

(async () => {
  const files = process.argv.slice(2);
  if (!files.length) throw new Error('要指定文件');
  const ref = await api('GET', `/repos/${REPO}/git/ref/heads/${BRANCH}`);
  const parent = ref.object.sha;
  const parentCommit = await api('GET', `/repos/${REPO}/git/commits/${parent}`);
  const baseTree = parentCommit.tree.sha;

  const tree = [];
  for (const f of files) {
    const buf = fs.readFileSync(path.resolve(f));
    const isText = !buf.includes(0) && !/\.(png|jpg|jpeg|gif|ico|zip|exe|dll|wav|mp3|ttf|otf)$/i.test(f);
    const blob = await api('POST', `/repos/${REPO}/git/blobs`,
      isText ? { content: buf.toString('utf8'), encoding: 'utf-8' } : { content: buf.toString('base64'), encoding: 'base64' });
    tree.push({ path: f.replace(/\\/g, '/'), mode: '100644', type: 'blob', sha: blob.sha });
    console.log('  blob', f);
    await new Promise((r) => setTimeout(r, 300));
  }
  const newTree = await api('POST', `/repos/${REPO}/git/trees`, { base_tree: baseTree, tree });
  const commit = await api('POST', `/repos/${REPO}/git/commits`, {
    message: '更新检测：每次启动强制检查，多源回退（GitHub API / jsDelivr / gh-proxy / raw）',
    tree: newTree.sha, parents: [parent],
  });
  await api('PATCH', `/repos/${REPO}/git/refs/heads/${BRANCH}`, { sha: commit.sha });
  console.log('已推送 commit=' + commit.sha);
})().catch((e) => { console.error('失败:', e.message); process.exit(1); });
