import {test, expect} from '@playwright/test';
import {spawn, execFileSync} from 'node:child_process';
import {createServer} from 'node:http';
import {promises as fs} from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const repoRoot = path.resolve(import.meta.dirname, '..', '..');
const passwordHash = '$argon2id$v=19$m=19456,t=2,p=1$o2KEDd/Nrt/G5QtUKhAY3w$9gLt3Rz/TkUAAJl6HSPSjjC1xbg3rAKuEq0AJQzyp5M';
let tempRoot;
let binaryPath;
let vault;
let child;
let origin;

async function freePort() {
  const server = createServer();
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return port;
}

async function waitForHTTP(url) {
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url);
      if (response.status < 500) return;
    } catch {}
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error(`timed out waiting for ${url}`);
}

test.beforeEach(async () => {
  tempRoot = await fs.mkdtemp(path.join(os.tmpdir(), 'obsite-edit-e2e-'));
  binaryPath = path.join(tempRoot, process.platform === 'win32' ? 'obsite.exe' : 'obsite');
  execFileSync('go', ['build', '-o', binaryPath, './cmd/obsite'], {cwd: repoRoot});
  vault = path.join(tempRoot, 'vault');
  await fs.mkdir(vault);
  await fs.writeFile(path.join(vault, 'obsite.yaml'), `title: Edit E2E\nbaseURL: http://127.0.0.1/\nnavigation: []\nedit:\n  username: admin\n  passwordHash: ${passwordHash}\n`);
  await fs.writeFile(path.join(vault, '_index.md'), '---\ntitle: Home\npublish: true\n---\nHome\n');
  await fs.writeFile(path.join(vault, 'article.md'), '---\ntitle: Article\npublish: true\ntype: doc\n---\nOriginal\n');
  const port = await freePort();
  origin = `http://127.0.0.1:${port}`;
  const configPath = path.join(vault, 'obsite.yaml');
  const config = (await fs.readFile(configPath, 'utf8')).replace('http://127.0.0.1/', `${origin}/`);
  await fs.writeFile(configPath, config);
  child = spawn(binaryPath, ['edit', '--vault', vault, '--port', String(port)], {cwd: repoRoot, stdio: 'ignore'});
  await waitForHTTP(`${origin}/_obsite/login`);
});

test.afterEach(async () => {
  if (child && !child.killed) child.kill('SIGTERM');
  if (tempRoot) await fs.rm(tempRoot, {recursive: true, force: true});
  child = undefined;
});

test('offline edit journey keeps serve public and saves complete Markdown', async ({browser}) => {
  const context = await browser.newContext();
  const blocked = [];
  await context.route('**/*', async route => {
    if (new URL(route.request().url()).origin === origin) await route.continue();
    else { blocked.push(route.request().url()); await route.abort('blockedbyclient'); }
  });
  const page = await context.newPage();
  await page.goto(`${origin}/article/`);
  await expect(page.locator('.edit-page-link')).toHaveCount(0);
  expect(await (await context.request.get(`${origin}/_obsite/source?path=article.md`)).status()).toBe(401);

  await page.goto(`${origin}/_obsite/login`);
  await page.locator('input[name="username"]').fill('admin');
  await page.locator('input[name="password"]').fill('secret');
  await page.getByRole('button', {name: 'Log in'}).click();
  await page.goto(`${origin}/_obsite/session`);
  await expect(page.locator('body')).toContainText('"authenticated":true');
  await page.goto(`${origin}/article/?edit=1`);
  await expect(page.locator('.edit-page-link')).toBeVisible();
  await page.goto(`${origin}/_obsite/editor?path=article.md`);
  await expect(page.locator('textarea')).toHaveValue(/Original/);
  await expect(page.getByRole('button', {name: 'Save'})).toBeVisible();
  await page.locator('textarea').fill('---\ntitle: Article\npublish: true\ntype: doc\n---\nChanged\n');
  await page.getByRole('button', {name: 'Save'}).click();
  await expect(page.locator('#status')).toContainText('Saved and rebuilt');
  await page.goto(`${origin}/article/`);
  await expect(page.locator('body')).toContainText('Changed');
  expect(blocked).toEqual([]);
  await context.close();
});
