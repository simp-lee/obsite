import {test, expect} from '@playwright/test';
import {spawn, execFileSync} from 'node:child_process';
import {createServer} from 'node:http';
import {promises as fs} from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const repoRoot = path.resolve(import.meta.dirname, '..', '..');
const fixtureRoot = path.join(repoRoot, 'test', 'testdata', 'e2e', 'runtime-vault');
let tempRoot;
let binaryPath;
let alphaVault;
let betaVault;
let staticServer;
let origin;
const requests = [];

async function listen(server, port = 0) {
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(port, '127.0.0.1', resolve);
  });
  return server.address().port;
}

function contentType(filePath) {
  switch (path.extname(filePath)) {
    case '.html': return 'text/html; charset=utf-8';
    case '.js': return 'text/javascript; charset=utf-8';
    case '.css': return 'text/css; charset=utf-8';
    case '.json': return 'application/json; charset=utf-8';
    case '.png': return 'image/png';
    default: return 'application/octet-stream';
  }
}

async function serveOutput(req, res) {
  const url = new URL(req.url, 'http://127.0.0.1');
  requests.push({path: url.pathname, cacheControl: req.headers['cache-control'] || ''});
  const match = /^\/(alpha|beta)(\/.*)?$/.exec(url.pathname);
  if (!match) {
    res.writeHead(404).end('not found');
    return;
  }
  const outputRoot = path.join(match[1] === 'alpha' ? alphaVault : betaVault, 'public');
  let relative = (match[2] || '/').replace(/^\/+/, '');
  if (!relative || relative.endsWith('/')) relative += 'index.html';
  const filePath = path.resolve(outputRoot, relative);
  if (filePath !== outputRoot && !filePath.startsWith(outputRoot + path.sep)) {
    res.writeHead(403).end('forbidden');
    return;
  }
  try {
    const data = await fs.readFile(filePath);
    res.writeHead(200, {'content-type': contentType(filePath)}).end(data);
  } catch {
    res.writeHead(404).end('not found');
  }
}

async function copyAndBuild(name, basePath) {
  const vault = path.join(tempRoot, name);
  await fs.cp(fixtureRoot, vault, {recursive: true});
  const configPath = path.join(vault, 'obsite.yaml');
  const config = (await fs.readFile(configPath, 'utf8')).replace('http://127.0.0.1/alpha/', `${origin}${basePath}`);
  await fs.writeFile(configPath, config);
  execFileSync(binaryPath, ['build', '--vault', vault], {cwd: repoRoot, stdio: 'inherit'});
  return vault;
}

async function offlineContext(browser, options = {}, allowedOrigin = origin) {
  const context = await browser.newContext(options);
  const blocked = [];
  await context.route('**/*', async route => {
    const target = new URL(route.request().url());
    if (target.origin === allowedOrigin) {
      await route.continue();
    } else {
      blocked.push(target.href);
      await route.abort('blockedbyclient');
    }
  });
  return {context, blocked};
}

async function waitForHTTP(url, timeout = 20_000) {
  const deadline = Date.now() + timeout;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url);
      if (response.ok) return;
      lastError = new Error(`HTTP ${response.status}`);
    } catch (error) {
      lastError = error;
    }
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw lastError || new Error(`timed out waiting for ${url}`);
}

async function glob(pattern) {
  const matches = [];
  for await (const match of fs.glob(pattern)) matches.push(match);
  return matches;
}

async function freePort() {
  const server = createServer();
  const port = await listen(server);
  await new Promise(resolve => server.close(resolve));
  return port;
}

test.beforeAll(async () => {
  tempRoot = await fs.mkdtemp(path.join(os.tmpdir(), 'obsite-e2e-'));
  binaryPath = path.join(tempRoot, process.platform === 'win32' ? 'obsite.exe' : 'obsite');
  execFileSync('go', ['build', '-o', binaryPath, './cmd/obsite'], {cwd: repoRoot, stdio: 'inherit'});
  staticServer = createServer((req, res) => void serveOutput(req, res));
  const port = await listen(staticServer);
  origin = `http://127.0.0.1:${port}`;
  alphaVault = await copyAndBuild('alpha-vault', '/alpha/');
  betaVault = await copyAndBuild('beta-vault', '/beta/');
});

test.afterAll(async () => {
  if (staticServer) await new Promise(resolve => staticServer.close(resolve));
  if (tempRoot) await fs.rm(tempRoot, {recursive: true, force: true});
});

test('strict section pages and article flow remain usable without JavaScript', async ({browser}) => {
  const {context, blocked} = await offlineContext(browser, {javaScriptEnabled: false});
  const page = await context.newPage();
  await page.goto(`${origin}/alpha/child/`);
  await expect(page.getByRole('heading', {name: 'Nested Child'})).toBeVisible();
  await expect(page.locator('nav[aria-label="Global navigation"] a')).toHaveCount(2);
  await expect(page.locator('.breadcrumbs')).toContainText('Nested Child');
  await page.goto(`${origin}/alpha/child/child/`);
  await expect(page.locator('article > header h1')).toHaveText('Child Article');
  await expect(page.locator('.reading-flow')).toContainText('1 of 1');
  await expect(page.locator('.source-links')).toHaveCount(0);
  expect(blocked).toEqual([]);
  await context.close();
});

test('strict Markdown runtime stays local and social PNG is independently reachable', async ({browser}) => {
  const {context, blocked} = await offlineContext(browser);
  const page = await context.newPage();
  await page.goto(`${origin}/alpha/math-diagrams/`);
  await expect(page.locator('article > header h1')).toHaveText('Math and Diagrams');
  await expect(page.locator('[data-obsite-main]')).toContainText('After invalid diagram remains visible.');
  await expect(page.locator('script[src*="/assets/obsite/runtime."]')).toHaveCount(1);
  const social = await glob(path.join(alphaVault, 'public', 'assets', 'social', '*', '*.png'));
  expect(social.length).toBeGreaterThanOrEqual(5);
  const response = await page.request.get(`${origin}/alpha/${path.relative(path.join(alphaVault, 'public'), social[0]).replaceAll(path.sep, '/')}`);
  expect(response.status()).toBe(200);
  expect(response.headers()['content-type']).toBe('image/png');
  expect((await response.body()).subarray(0, 8)).toEqual(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]));
  expect(blocked).toEqual([]);
  await context.close();
});

test('base-path builds are isolated and contain no external runtime requests', async ({browser}) => {
  const alpha = await fs.readFile(path.join(alphaVault, 'public', 'index.html'), 'utf8');
  const beta = await fs.readFile(path.join(betaVault, 'public', 'index.html'), 'utf8');
  expect(alpha).toContain(`${origin}/alpha/`);
  expect(alpha).not.toContain(`${origin}/beta/`);
  expect(beta).toContain(`${origin}/beta/`);
  expect(beta).not.toContain(`${origin}/alpha/`);
  const {context, blocked} = await offlineContext(browser);
  const page = await context.newPage();
  await page.goto(`${origin}/beta/`);
  await expect(page.getByRole('heading', {name: 'Runtime Home'})).toBeVisible();
  expect(blocked).toEqual([]);
  await context.close();
});

test('serve --watch rebuilds strict section content', async () => {
  const watchRoot = path.join(tempRoot, 'watch-vault');
  await fs.mkdir(path.join(watchRoot, 'docs'), {recursive: true});
  const port = await freePort();
  await fs.writeFile(path.join(watchRoot, 'obsite.yaml'), `baseURL: http://127.0.0.1:${port}/watch/\ntitle: Watch Garden\nnavigation: []\n`);
  await fs.writeFile(path.join(watchRoot, '_index.md'), '---\ntitle: Watch Home\npublish: true\n---\nHome\n');
  const notePath = path.join(watchRoot, 'watch.md');
  await fs.writeFile(notePath, '---\ntitle: Watch Note\npublish: true\ntype: page\n---\nVersion one.\n');
  const childIndex = path.join(watchRoot, 'docs', '_index.md');
  await fs.writeFile(childIndex, '---\ntitle: Docs\npublish: true\n---\nDocs\n');
  const childNote = path.join(watchRoot, 'docs', 'guide.md');
  await fs.writeFile(childNote, '---\ntitle: Guide\npublish: true\ntype: doc\n---\nGuide one.\n');
  const child = spawn(binaryPath, ['serve', '--watch', '--vault', watchRoot, '--port', String(port)], {cwd: repoRoot, stdio: ['ignore', 'pipe', 'pipe']});
  let logs = '';
  child.stdout.on('data', chunk => { logs += chunk; });
  child.stderr.on('data', chunk => { logs += chunk; });
  try {
    await waitForHTTP(`http://127.0.0.1:${port}/watch/`);
    await fs.writeFile(childNote, '---\ntitle: Guide\npublish: true\ntype: doc\n---\nGuide two after rebuild.\n');
    await expect.poll(async () => fs.readFile(path.join(watchRoot, 'public', 'docs', 'guide', 'index.html'), 'utf8'), {timeout: 20_000, message: logs}).toContain('Guide two after rebuild.');
  } finally {
    if (child.exitCode === null) {
      child.kill('SIGTERM');
      await new Promise(resolve => child.once('exit', resolve));
    }
  }
  expect(logs).not.toContain('build failed');
});
