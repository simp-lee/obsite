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
    case '.svg': return 'image/svg+xml';
    case '.woff2': return 'font/woff2';
    default: return 'application/octet-stream';
  }
}

async function serveOutput(req, res) {
  const url = new URL(req.url, 'http://127.0.0.1');
  requests.push({path: url.pathname, cacheControl: req.headers['cache-control'] || ''});
  const match = /^\/(alpha|beta)(\/.*)?$/.exec(decodeURIComponent(url.pathname));
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
    res.writeHead(200, {'content-type': contentType(filePath), 'cache-control': 'no-cache'}).end(data);
  } catch {
    try {
      const fallback = await fs.readFile(path.join(outputRoot, '404.html'));
      res.writeHead(404, {'content-type': 'text/html; charset=utf-8'}).end(fallback);
    } catch {
      res.writeHead(404).end('not found');
    }
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

test('official KaTeX and Mermaid render offline and isolate invalid blocks', async ({browser}) => {
  const {context, blocked} = await offlineContext(browser);
  const page = await context.newPage();
  const errors = [];
  page.on('console', message => { errors.push(`${message.type()}:${message.text()}`); });
  await page.goto(`${origin}/alpha/math-diagrams/`);

  await expect.poll(() => page.locator('.katex').count()).toBeGreaterThanOrEqual(4);
  await expect.poll(() => page.locator('.katex-display').count()).toBeGreaterThanOrEqual(2);
  await expect(page.locator('[data-page-content]')).toContainText('\\frac{1}{2');
  const mathSources = await page.locator('annotation[encoding="application/x-tex"]').allTextContents();
  for (const source of ['\\frac{1}{1 + \\frac{1}{n}}', '\\begin{matrix}', '\\begin{aligned}', '\\sqrt{x_2}']) {
    expect(mathSources.some(value => value.includes(source))).toBeTruthy();
  }
  const validDiagrams = [
    {index: 0, labels: ['Start', 'Stop']},
    {index: 1, labels: ['Alice', 'Bob', 'Hello', 'Hi']},
    {index: 3, labels: ['Ready']},
    {index: 4, labels: ['Animal', 'speak()']}
  ];
  for (const diagram of validDiagrams) {
    const block = page.locator('pre.mermaid').nth(diagram.index);
    await expect(block.locator('svg')).toHaveCount(1);
    await expect(block).not.toContainText(/Syntax error|No diagram type detected/i);
    for (const label of diagram.labels) await expect(block).toContainText(label);
  }
  await expect.poll(() => errors.some(message => message.includes('Mermaid could not render diagram'))).toBeTruthy();
  await expect(page.getByText('After invalid diagram remains visible.')).toBeVisible();
  await expect(page.locator('pre.mermaid').nth(2)).toContainText(/this is not a valid mermaid diagram|Syntax error/i);
  await expect.poll(() => errors.some(message => message.includes('KaTeX could not render formula'))).toBeTruthy();
  await page.goto(`${origin}/alpha/embed-host/`);
  await expect.poll(() => page.locator('.katex').count()).toBeGreaterThanOrEqual(1);
  await expect.poll(() => page.locator('annotation[encoding="application/x-tex"]').filter({hasText: '\\frac{a}{b}'}).count()).toBeGreaterThanOrEqual(1);
  await expect(page.locator('pre.mermaid svg')).toHaveCount(1);
  expect(blocked).toEqual([]);
  await context.close();
});

test('color mode is pre-paint and isolated by base path', async ({browser}) => {
  const {context} = await offlineContext(browser);
  await context.addInitScript(() => {
    localStorage.setItem('obsite.theme.v1:/alpha/', 'dark');
    localStorage.setItem('obsite.theme.v1:/beta/', 'light');
    requestAnimationFrame(() => { window.__obsiteFirstFrameTheme = document.documentElement.getAttribute('data-theme'); });
  });
  const page = await context.newPage();
  await page.goto(`${origin}/alpha/math-diagrams/`);
  await expect.poll(() => page.evaluate(() => window.__obsiteFirstFrameTheme)).toBe('dark');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await page.goto(`${origin}/beta/math-diagrams/`);
  await expect.poll(() => page.evaluate(() => window.__obsiteFirstFrameTheme)).toBe('light');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  await context.close();
});

test('Sidebar state, mobile controls, delegated Popover, and revalidation work', async ({browser}) => {
  requests.length = 0;
  const {context} = await offlineContext(browser);
  const page = await context.newPage();
  await page.goto(`${origin}/alpha/child/`);
  await expect(page.locator('[data-site-body]')).toHaveAttribute('data-sidebar-ready', 'true');
  await expect(page.locator('[data-sidebar-root] a[aria-current="page"]')).toHaveText('Nested Child');
  const nestedLink = page.locator('[data-sidebar-root] a.sidebar-link-dir').filter({hasText: /^nested$/});
  await expect(nestedLink.locator('xpath=../..')).toHaveAttribute('data-expanded', 'true');

  const nestedItem = nestedLink.locator('xpath=../..');
  await nestedItem.locator('button.sidebar-toggle').click();
  await expect(nestedItem).toHaveAttribute('data-expanded', 'false');
  await page.reload();
  await expect(page.locator('[data-sidebar-root] a.sidebar-link-dir').filter({hasText: /^nested$/}).locator('xpath=../..')).toHaveAttribute('data-expanded', 'false');

  const markdownReference = page.locator('[data-page-content] a[data-popover-path="reference"]');
  const authoredHref = await markdownReference.getAttribute('href');
  await markdownReference.focus();
  await expect(page.locator('#obsite-popover-card')).toBeVisible();
  await expect(markdownReference).toHaveAttribute('aria-describedby', /obsite-popover-card/);
  await page.keyboard.press('Escape');
  await expect(page.locator('#obsite-popover-card')).toBeHidden();
  expect(await markdownReference.getAttribute('href')).toBe(authoredHref);

  const sidebarReference = page.locator('[data-sidebar-root] a[data-popover-path="reference"]');
  await sidebarReference.hover();
  await expect(page.locator('#obsite-popover-card')).toBeVisible();
  await expect(page.locator('#obsite-popover-card')).toContainText('Reference');
  await page.locator('#obsite-popover-card').hover();
  await page.waitForTimeout(160);
  await expect(page.locator('#obsite-popover-card')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.locator('#obsite-popover-card')).toBeHidden();
  expect(requests.some(request => request.path === '/alpha/assets/obsite/sidebar.json' && request.cacheControl.includes('no-cache'))).toBeTruthy();
  await context.close();

  const mobile = await offlineContext(browser, {viewport: {width: 390, height: 844}});
  const mobilePage = await mobile.context.newPage();
  await mobilePage.goto(`${origin}/alpha/child/`);
  const launch = mobilePage.locator('[data-sidebar-toggle]');
  await expect(launch).toBeVisible();
  await launch.click();
  await expect(launch).toHaveAttribute('aria-expanded', 'true');
  await expect(mobilePage.locator('[data-sidebar-shell]')).not.toHaveAttribute('aria-hidden', 'true');
  await mobilePage.locator('[data-sidebar-overlay]').click({position: {x: 380, y: 400}});
  await expect(launch).toHaveAttribute('aria-expanded', 'false');
  await launch.click();
  await mobilePage.keyboard.press('Escape');
  await expect(launch).toHaveAttribute('aria-expanded', 'false');
  await expect(mobilePage.locator('[data-sidebar-shell]')).toHaveAttribute('aria-hidden', 'true');
  await mobile.context.close();
});

test('feature-free pages do not request or execute vendor JavaScript', async ({browser}) => {
  const {context, blocked} = await offlineContext(browser);
  const page = await context.newPage();
  const vendorRequests = [];
  page.on('request', request => {
    if (/\/(katex\.min\.js|auto-render\.min\.js|mermaid\.min\.js)$/.test(new URL(request.url()).pathname)) vendorRequests.push(request.url());
  });
  await page.goto(`${origin}/alpha/reference/`);
  await page.waitForTimeout(250);
  expect(vendorRequests).toEqual([]);
  expect(await page.evaluate(() => ({katex: typeof window.katex, auto: typeof window.renderMathInElement, mermaid: typeof window.mermaid}))).toEqual({katex: 'undefined', auto: 'undefined', mermaid: 'undefined'});
  expect(blocked).toEqual([]);
  await context.close();
});

test('runtime and Sidebar failures plus disabled JavaScript preserve static content', async ({browser}) => {
  const runtimeFailure = await offlineContext(browser);
  const runtimePage = await runtimeFailure.context.newPage();
  await runtimePage.route('**/assets/obsite/runtime.*.js', route => route.abort('failed'));
  await runtimePage.goto(`${origin}/alpha/math-diagrams/`);
  await expect(runtimePage.getByRole('heading', {name: 'Math and Diagrams'})).toBeVisible();
  await expect(runtimePage.locator('[data-page-content]')).toContainText('After invalid diagram remains visible.');
  await runtimeFailure.context.close();

  const {context} = await offlineContext(browser);
  const page = await context.newPage();
  const errors = [];
  page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
  await page.route('**/assets/obsite/sidebar.json', route => route.abort('failed'));
  await page.goto(`${origin}/alpha/math-diagrams/`);
  await expect(page.getByRole('heading', {name: 'Math and Diagrams'})).toBeVisible();
  await expect(page.locator('[data-page-content]')).toContainText('After invalid diagram remains visible.');
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute('href', /math-diagrams/);
  await expect.poll(() => errors.some(message => message.includes('Sidebar initialization failed'))).toBeTruthy();
  await context.close();

  const noJS = await offlineContext(browser, {javaScriptEnabled: false});
  const noJSPage = await noJS.context.newPage();
  await noJSPage.goto(`${origin}/alpha/math-diagrams/`);
  await expect(noJSPage.getByRole('heading', {name: 'Math and Diagrams'})).toBeVisible();
  await expect(noJSPage.locator('[data-page-content]')).toContainText('After invalid diagram remains visible.');
  await expect(noJSPage.locator('.breadcrumbs a')).toHaveCount(2);
  await noJS.context.close();
});

test('serve --watch rebuilds and reloads a real browser', async ({browser}) => {
  const watchRoot = path.join(tempRoot, 'watch-vault');
  await fs.mkdir(path.join(watchRoot, 'notes'), {recursive: true});
  const port = await freePort();
  await fs.writeFile(path.join(watchRoot, 'obsite.yaml'), `baseURL: http://127.0.0.1:${port}/\ntitle: Watch Garden\nsidebar:\n  enabled: false\npopover:\n  enabled: false\nrelated:\n  enabled: false\n`);
  const notePath = path.join(watchRoot, 'notes', 'watch.md');
  await fs.writeFile(notePath, '# Watch Note\n\nVersion one.\n');
  const child = spawn(binaryPath, ['serve', '--watch', '--vault', watchRoot, '--port', String(port)], {cwd: repoRoot, stdio: ['ignore', 'pipe', 'pipe']});
  let logs = '';
  child.stdout.on('data', chunk => { logs += chunk; });
  child.stderr.on('data', chunk => { logs += chunk; });
  try {
    await waitForHTTP(`http://127.0.0.1:${port}/watch/`);
    const watchOrigin = `http://127.0.0.1:${port}`;
    const watchOffline = await offlineContext(browser, {}, watchOrigin);
    const watchContext = watchOffline.context;
    const page = await watchContext.newPage();
    const liveReloadResponse = page.waitForResponse(response => response.url().includes('/_livereload?'));
    await page.goto(`http://127.0.0.1:${port}/watch/`);
    await liveReloadResponse;
    await page.waitForTimeout(200);
    await expect(page.locator('[data-page-content]')).toContainText('Version one.');
    await fs.writeFile(notePath, '# Watch Note\n\nVersion two after rebuild.\n');
    await expect.poll(async () => fs.readFile(path.join(watchRoot, 'public', 'watch', 'index.html'), 'utf8'), {timeout: 20_000, message: logs}).toContain('Version two after rebuild.');
    await expect(page.locator('[data-page-content]')).toContainText('Version two after rebuild.', {timeout: 20_000});
    expect(watchOffline.blocked).toEqual([]);
    await watchContext.close();
  } finally {
    if (child.exitCode === null) {
      child.kill('SIGTERM');
      await new Promise(resolve => child.once('exit', resolve));
    }
  }
  expect(logs).not.toContain('build failed');
});
