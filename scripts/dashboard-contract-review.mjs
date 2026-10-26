// Independent contract review. All disposable files belong to the storage guard.
// Run: python scripts/bounded_validation.py --timeout 600 -- node scripts/dashboard-contract-review.mjs --await-browser
// Development: --fixture holds an owned fixture; --run <runDir> reviews that fixture; STOP ends it.
// Automated regression suite uses the documented private Chrome/CDP fixture. No .env or user database access.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import { once } from 'node:events';
import { createServer } from 'node:http';
import { createServer as netServer } from 'node:net';
import { existsSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, extname, join, relative, resolve, sep } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createProxyAuthFixture, reviewProxyAuth } from './proxy-auth-browser.mjs';
import { createDashboardControlPlaneFixture, reviewDashboardControlPlane } from './dashboard-control-plane-browser.mjs';
import { finishInterception, interceptionTracker } from './cdp-interception.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
process.env.TSX_DISABLE_CACHE = '1';
const cleanEnv = Object.fromEntries(Object.entries(process.env).filter(([k]) => !/^CANARY_/iu.test(k)));
const owned = [], servers = [];
const sleep = ms => new Promise(r => setTimeout(r, ms));
const sha = value => createHash('sha256').update(value).digest('hex');
const secret = () => randomBytes(32).toString('hex');
const J = JSON.stringify;
const json = path => JSON.parse(readFileSync(path, 'utf8'));
const write = (path, value) => writeFileSync(path, JSON.stringify(value, null, 2));
const serial = value => JSON.parse(JSON.stringify(value));
const args = process.argv.slice(2);
let runDir, state, report, page, ws, requestHook, proxyAuthFixture, dashboardControlPlaneFixture, stopInterceptions;
const secrets = new Set();
const redact = value => { let out = String(value); for (const s of secrets) if (s) out = out.replaceAll(s, '[fixture-secret]'); return out; };
const save = () => writeFileSync(join(runDir, 'results.json'), redact(JSON.stringify(report, null, 2)));
async function port() { const s = netServer(); s.listen(0, '127.0.0.1'); await once(s, 'listening'); const p = s.address().port; await new Promise(r => s.close(r)); return p; }
async function until(fn, what, timeout = 12000) {
  const start = Date.now(); let last;
  do { try { const result = await fn(); if (result) return result; } catch (e) { last = e; } await sleep(100); } while (Date.now() - start < timeout);
  throw Error(`Timeout: ${what}${last ? ` (${last.message})` : ''}`);
}
async function command(exe, argv, cwd, env = cleanEnv) {
  const p = spawn(exe, argv, { cwd, env, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
  let log = ''; for (const stream of [p.stdout, p.stderr]) stream.on('data', b => { log += b; });
  const code = await new Promise((ok, fail) => { p.on('error', fail); p.on('exit', ok); });
  if (code !== 0) throw Error(`${exe} ${argv.join(' ')} exited ${code}: ${log.slice(-3000)}`);
  return log;
}
function cleanup() { for (const p of owned) { try { p.kill(); } catch {} } for (const s of servers) s.close(); }
process.on('exit', cleanup);
process.on('SIGINT', () => process.exit(130));
process.on('SIGTERM', () => process.exit(143));
function files(path) {
  if (!existsSync(path)) return [];
  const out = [];
  for (const d of readdirSync(path, { withFileTypes: true })) {
    if (['node_modules', 'dist', '.git'].includes(d.name)) continue;
    const p = join(path, d.name); if (d.isDirectory()) out.push(...files(p)); else if (d.isFile()) out.push(p);
  }
  return out;
}
async function fixture() {
  const base = join(root, '.canary', 'work', 'current', 'outputs');
  assert.ok(existsSync(join(root, '.canary', 'bounded-validation.lock')), 'Run through bounded_validation.py');
  assert.equal(resolve(process.env.GOCACHE || ''), join(root, '.canary', 'cache', 'go-build'));
  assert.ok(existsSync(base), 'The guard must own disposable outputs');
  runDir = mkdtempSync(join(base, 'run-'));
  const source = root;
  const sourceFiles = [...files(join(root, 'apps', 'node-go')), ...files(join(root, 'apps', 'web')), ...files(join(root, 'packages', 'shared')),
    ...['go.mod', 'go.sum', 'package.json', 'package-lock.json', 'tsconfig.base.json', 'Dockerfile.web', 'deploy/web.conf'].map(p => join(root, p)).filter(existsSync)];
  const manifest = [];
  for (const f of sourceFiles.sort()) {
    const name = relative(root, f).replaceAll('\\', '/'), bytes = readFileSync(f);
    manifest.push({ path: name, bytes: bytes.length, sha256: sha(bytes) });
  }
  const sourceHash = sha(JSON.stringify(manifest)); write(join(runDir, 'source-manifest.json'), { sourceHash, files: manifest });
  const go = process.env.CANARY_GO || join(root, '.canary', 'tools', 'go', 'bin', process.platform === 'win32' ? 'go.exe' : 'go');
  const binary = join(runDir, process.platform === 'win32' ? 'review-node.exe' : 'review-node');
  console.log(`BUILD ${runDir}`);
  // Omit compiler debug data and linker symbols to fit the same 512 MiB
  // disposable-storage budget used by native package validation.
  await command(go, ['build', '-p', '1', '-trimpath', '-gcflags=all=-dwarf=false', '-ldflags=-s -w', '-o', binary, './apps/node-go/cmd/canary'], source, { ...cleanEnv, CGO_ENABLED: '0', GOTOOLCHAIN: 'local' });
  const webRoot = join(source, 'apps', 'web'), dist = join(runDir, 'dist'), vite = join(root, 'node_modules', 'vite', 'bin', 'vite.js');
  const tsc = await command(process.execPath, [join(root, 'node_modules', 'typescript', 'bin', 'tsc'), '--noEmit', '-p', webRoot], source);
  const webBuild = await command(process.execPath, [vite, 'build', '--outDir', dist, '--emptyOutDir'], webRoot);
  writeFileSync(join(runDir, 'build.log'), tsc + webBuild);
  const origin = `http://127.0.0.1:${await port()}`, hooks = [];
  const sink = createServer((req, res) => {
    let body = ''; req.on('data', b => { if (body.length < 262144) body += b; }); req.on('end', () => {
      if (req.url?.startsWith('/hook')) { hooks.push({ path: req.url, bytes: body.length, hasAuth: Boolean(req.headers.authorization) }); write(join(runDir, 'sink.json'), hooks); res.writeHead(204); res.end(); }
      else { res.writeHead(req.url === '/fail' ? 503 : 200, { 'Content-Type': 'application/json' }); res.end('{"ok":true}'); }
    });
  });
  sink.listen(0, '127.0.0.1'); await once(sink, 'listening'); servers.push(sink);
  const nodes = [];
  for (const id of ['review-a', 'review-b']) {
    const node = { id, port: await port(), token: secret() }; node.url = `http://127.0.0.1:${node.port}`; nodes.push(node);
    const p = spawn(binary, ['serve'], { cwd: runDir, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'], env: { ...cleanEnv,
      CANARY_NODE_ID: id, CANARY_NODE_NAME: id, CANARY_HOST: '127.0.0.1', CANARY_PORT: String(node.port), CANARY_DB: join(runDir, `${id}.db`),
      CANARY_IDENTITY_DIRECTORY: join(runDir, `${id}-identity`),
      CANARY_ADMIN_TOKEN: node.token, CANARY_CLUSTER_TOKEN: secret(), CANARY_PEERS: '[]', CANARY_CORS_ORIGINS: origin } });
    owned.push(p); let log = ''; for (const s of [p.stdout, p.stderr]) s.on('data', b => { log = (log + b).slice(-20000); });
    p.on('exit', code => writeFileSync(join(runDir, `${id}.exit.log`), `exit ${code}\n${log}`));
    await until(async () => (await fetch(node.url + '/api/auth/status')).ok, `${id} ready`, 30000);
  }
  if (args.includes('--proxy-auth')) proxyAuthFixture = await createProxyAuthFixture({ origin, node: nodes[0], peer: nodes[1], servers });
  const contentTypes = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png', '.woff2': 'font/woff2' };
  const preview = createServer((req, res) => {
    if (proxyAuthFixture?.handle(req, res)) return;
    try {
      let path = resolve(dist, '.' + decodeURIComponent(new URL(req.url, origin).pathname));
      if (path !== dist && !path.startsWith(dist + sep)) throw Error('Outside private dist');
      if (!extname(path)) path = join(dist, 'index.html');
      let body = readFileSync(path);
      if (extname(path) === '.html') {
        // Native preview and CDP both start on this fixture, never the usual :3100.
        body = Buffer.from(body.toString().replace('<head>', `<head><script>localStorage.setItem('uptime-canary.endpoint',${J(nodes[0].url)})</script>`));
      }
      res.writeHead(200, { 'Content-Type': contentTypes[extname(path)] || 'application/octet-stream', 'Cache-Control': 'no-store', 'X-Content-Type-Options': 'nosniff' }).end(body);
    } catch { res.writeHead(404).end(); }
  });
  preview.listen(Number(new URL(origin).port), '127.0.0.1'); await once(preview, 'listening'); servers.push(preview);
  await until(async () => (await fetch(origin)).ok, 'private dashboard');
  if (args.includes('--dashboard-control-plane')) dashboardControlPlaneFixture = await createDashboardControlPlaneFixture({ runDir, port, env: cleanEnv, owned, servers, until, secrets });
  const artifacts = files(dist).map(f => ({ path: relative(dist, f).replaceAll('\\', '/'), bytes: readFileSync(f).length, sha256: sha(readFileSync(f)) }));
  state = { kind: 'dashboard-contract-review', runDir, source, origin, sink: `http://127.0.0.1:${sink.address().port}`, nodes, sourceHash, binaryHash: sha(readFileSync(binary)), artifacts, startedAt: new Date().toISOString(), pid: process.pid, proxyAuthCrossOrigin: proxyAuthFixture?.crossOrigin, dashboardControlPlaneOrigin: dashboardControlPlaneFixture?.origin };
  write(join(runDir, 'state.private.json'), state); console.log(`READY ${runDir}`);
}
async function api(node, path, method = 'GET', body, token = node.token) {
  assert.equal(new URL(node.url).hostname, '127.0.0.1');
  let r;
  try { r = await fetch(node.url + path, { method, headers: { Connection: 'close', ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) }, body: body === undefined ? undefined : JSON.stringify(body), redirect: 'error', signal: AbortSignal.timeout(15000) }); }
  catch (e) { throw Error(`${method} ${path}: ${e.message}; cause=${e.cause?.code || e.cause?.message || 'unknown'}`); }
  const text = await r.text(); let value; try { value = JSON.parse(text); } catch {}
  return { status: r.status, value, text };
}
class Cdp {
  seq = 0; pending = new Map(); listeners = new Map();
  constructor(socket) { this.socket = socket; socket.addEventListener('message', e => { const m = JSON.parse(e.data); if (m.id) { const p = this.pending.get(m.id); if (p) { this.pending.delete(m.id); m.error ? p.reject(Error(m.error.message)) : p.resolve(m.result); } } else for (const fn of this.listeners.get(m.method) ?? []) fn(m.params); }); }
  on(name, fn) { this.listeners.set(name, [...this.listeners.get(name) ?? [], fn]); }
  off(name, fn) { this.listeners.set(name, (this.listeners.get(name) ?? []).filter(listener => listener !== fn)); }
  send(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.seq, timer = setTimeout(() => { this.pending.delete(id); reject(Error(`CDP timeout: ${method}`)); }, 15000); this.pending.set(id, { resolve: v => { clearTimeout(timer); resolve(v); }, reject: e => { clearTimeout(timer); reject(e); } }); this.socket.send(JSON.stringify({ id, method, params })); }); }
  async eval(expression) { const r = await this.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true }); if (r.exceptionDetails) throw Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text); return r.result?.value; }
}
const DOM = `
window.__norm = s => (s || '').replace(/\\s+/g,' ').trim();
window.__vis = e => !!e && e.getClientRects().length > 0;
window.__field = (label, scope) => { const root = scope ? document.querySelector(scope) : document; const match = s => __norm(s).replace(/\\s*\\(optional\\)$/,'') === label.replace(/\\s*\\(optional\\)$/,''); const all = [...root.querySelectorAll('input,textarea,select')].filter(e => __vis(e) && !['checkbox','radio'].includes(e.type) && (match(e.getAttribute('aria-label')) || [...e.labels || []].some(l => match(l.innerText)))); if(all.length !== 1) throw Error('field '+label+' matched '+all.length); return all[0]; };
window.__set = (e,v) => { const p = e.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : e.tagName === 'SELECT' ? HTMLSelectElement.prototype : HTMLInputElement.prototype; Object.getOwnPropertyDescriptor(p,'value').set.call(e,v); e.dispatchEvent(new Event(e.tagName === 'SELECT' ? 'change' : 'input',{bubbles:true})); };
window.__button = (label,scope) => { const root = scope ? document.querySelector(scope) : document; const all = [...root.querySelectorAll('button,a')].filter(e => __vis(e) && [__norm(e.innerText),__norm(e.getAttribute('aria-label'))].includes(label)); if(all.length !== 1) throw Error('button '+label+' matched '+all.length); return all[0]; }; true;`;
async function ready() { await page.eval(DOM); }
async function nav(hash) { await page.eval(`location.hash=${J(hash)}`); await sleep(180); await ready(); }
async function fill(label, value, scope) { await ready(); await page.eval(`__set(__field(${J(label)},${J(scope)}),${J(value)})`); await sleep(45); }
async function click(label, scope) { await ready(); await page.eval(`(() => { const e=__button(${J(label)},${J(scope)}); if(e.disabled) throw Error('disabled'); e.click(); })()`); await sleep(120); }
async function choose(label, scope) { await page.eval(`(() => { const r=${scope ? `document.querySelector(${J(scope)})` : 'document'}; const e=[...r.querySelectorAll('label')].find(e => __vis(e) && __norm(e.innerText).startsWith(${J(label)}) && e.querySelector('input[type=radio],input[type=checkbox]')); if(!e) throw Error('choice missing'); e.querySelector('input').click(); })()`); await sleep(50); }
async function text(s, timeout) { return until(() => page.eval(`document.body.innerText.includes(${J(s)})`), `text ${s}`, timeout); }
async function heading(s) { return until(() => page.eval(`document.querySelector('main h1')?.textContent.includes(${J(s)})`), `heading ${s}`); }
async function disclose(s) { await page.eval(`(() => { const e=[...document.querySelectorAll('.disc-h')].find(e => __norm(e.querySelector('.disc-t')?.textContent) === ${J(s)}); if(!e) throw Error('disclosure missing'); if(e.getAttribute('aria-expanded') !== 'true') e.click(); })()`); await sleep(70); }
async function step(name, fn) {
  const started = Date.now(); try { const detail = await fn(); report.checks.push({ name, passed: true, ms: Date.now() - started, detail }); console.log(`PASS ${name}`); }
  catch(e) { report.checks.push({ name, passed: false, ms: Date.now() - started, error: redact(e.message).slice(0, 1800) }); console.log(`FAIL ${name}: ${redact(e.message).slice(0, 600)}`); }
  save();
}
async function launchBrowser() {
  if (args.includes('--await-browser')) {
    console.log(`BROWSER_READY ${state.origin} ${runDir}`);
    await until(() => existsSync(join(runDir, 'ALLOW_CDP')), 'native preview or documented CDP fallback', 180000);
  }
  const debugPort = await port(), chrome = process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe';
  const profile = mkdtempSync(join(runDir, 'chrome-profile-'));
  const p = spawn(chrome, ['--headless=new', `--remote-debugging-port=${debugPort}`, `--user-data-dir=${profile}`, '--no-first-run', '--no-default-browser-check', '--disable-background-networking', '--disable-component-update', '--disable-sync', '--disable-extensions', '--no-proxy-server', '--password-store=basic', 'about:blank'], { windowsHide: true, stdio: 'ignore' }); owned.push(p);
  const targets = await until(async () => { try { return await (await fetch(`http://127.0.0.1:${debugPort}/json`)).json(); } catch { return false; } }, 'Chrome', 30000);
  ws = new WebSocket(targets.find(t => t.type === 'page').webSocketDebuggerUrl); await once(ws, 'open'); page = new Cdp(ws);
  for (const domain of ['Page', 'Runtime', 'Network']) await page.send(`${domain}.enable`);
  // UI copy and percent fixtures intentionally use en-US; pure checks also cover comma formatting.
  await page.send('Emulation.setLocaleOverride', { locale: 'en-US' });
  const allowed = new Set([state.origin, state.sink, ...state.nodes.map(n => n.url)]);
  if (state.proxyAuthCrossOrigin) allowed.add(state.proxyAuthCrossOrigin);
  if (state.dashboardControlPlaneOrigin) allowed.add(state.dashboardControlPlaneOrigin);
  await page.send('Fetch.enable', { patterns: [{ urlPattern: '*', requestStage: 'Request' }] });
  const failed = error => { report.exceptions.push(redact(`CDP interception failed: ${error.message}`)); };
  const interceptions = interceptionTracker(failed);
  const paused = p => interceptions.run(async () => {
    if (await requestHook?.(p)) return;
    let ok = false; try { const u = new URL(p.request.url); ok = allowed.has(u.origin) || u.protocol === 'data:'; } catch {}
    const outcome = {
      canceled() { report.canceledInterceptions = (report.canceledInterceptions ?? 0) + 1; },
      failed,
    };
    if (ok) await finishInterception(page, 'Fetch.continueRequest', { requestId: p.requestId }, outcome);
    else { report.blocked.push(p.request.url); await finishInterception(page, 'Fetch.failRequest', { requestId: p.requestId, errorReason: 'BlockedByClient' }, outcome); }
  });
  page.on('Fetch.requestPaused', paused);
  let stopping;
  stopInterceptions = () => stopping ??= (async () => {
    const settled = interceptions.stop(); // Synchronously stop admitting callbacks.
    page.off('Fetch.requestPaused', paused);
    try {
      // Keep all traffic blocked after removing the interception allowlist,
      // including background refreshes while old commands are settling.
      await page.send('Network.setBlockedURLs', { urls: ['*'] });
      await page.send('Fetch.disable');
    } catch (error) { failed(error); }
    await settled;
  })();
  page.on('Runtime.exceptionThrown', p => { report.exceptions.push(redact(p.exceptionDetails?.exception?.description || p.exceptionDetails?.text)); });
  page.on('Network.requestWillBeSent', p => {
    const h = p.request.headers; report.requests.push({ url: p.request.url, method: p.request.method, authorization: Object.keys(h).some(k => k.toLowerCase() === 'authorization'), cookie: Object.keys(h).some(k => k.toLowerCase() === 'cookie') });
  });
  await page.send('Page.addScriptToEvaluateOnNewDocument', { source: `if(location.origin === ${J(state.origin)} && !localStorage.getItem('uptime-canary.endpoint')) localStorage.setItem('uptime-canary.endpoint', ${J(state.nodes[0].url)});` });
  await page.send('Page.navigate', { url: state.origin }); await until(() => page.eval('!!document.querySelector("h1")'), 'first paint'); await ready();
}
async function signIn(node, switching = false, beforeSubmit) {
  if (switching) { await page.eval(`document.querySelector('.topbar .tb-btn').click()`); await sleep(100); }
  const scope = switching ? 'dialog[open]' : undefined;
  const addressEditable = await page.eval(`[...document.querySelectorAll(${J(scope ? scope + ' input' : 'input')})].some(input => [...input.labels || []].some(label => label.textContent.trim() === 'Node API address'))`);
  if (addressEditable) await fill('Node API address', node.url, scope);
  else assert.equal(node.url, state.origin, 'gateway sign-in must use the dashboard origin');
  await choose('Account token', scope); await fill('Account token', node.token, scope);
  await beforeSubmit?.();
  await click(switching ? 'Sign in and switch' : 'Sign in', scope);
  await until(() => page.eval(`document.body.innerText.includes(${J(node.id)}) && !document.querySelector('dialog[open]') && !!document.querySelector('nav.rail')`), 'connected');
  const dismiss = await page.eval(`!![...document.querySelectorAll('button')].find(e => e.textContent === 'Not now')`); if (dismiss) await click('Not now');
}
async function functionsUnderReview() {
  // Four small test modules need extra exports. Build real source in place;
  // never clone the source tree. Relative imports still resolve to originals.
  globalThis.document ??= { addEventListener() {} };
  globalThis.React ??= await import('react');
  const { tsImport } = await import('tsx/esm/api');
  const load = async (name, exports) => {
    const src = join(state.source, 'apps', 'web', 'src', name), dest = join(runDir, name.replaceAll('/', '-').replace(/\.tsx?$/, '.review.tsx'));
    const code = readFileSync(src, 'utf8').replace(/(from\s+)(['"])(\.[^'"]+)\2/gu,
      (_, prefix, quote, specifier) => prefix + quote + pathToFileURL(resolve(dirname(src), specifier)).href + quote);
    writeFileSync(dest, code + `\nexport { ${exports} };\n`);
    return tsImport(pathToFileURL(dest).href, import.meta.url);
  };
  const monitor = await load('views/MonitorForm.tsx', 'draftFrom, build, masked');
  const channel = await load('views/Channels.tsx', 'channelDraft, buildChannel');
  const config = await load('views/Config.tsx', 'monitorRows');
  const maintenance = await load('views/Maintenance.tsx', 'mDraft, buildMaintenance');
  const specs = await tsImport(pathToFileURL(join(state.source, 'apps/web/src/serviceSpecs.tsx')).href, import.meta.url);
  const checks = await tsImport(pathToFileURL(join(state.source, 'apps/web/src/checks.tsx')).href, import.meta.url);
  // Protocol 18: the PM2 builder and rules are already exported, so the form/node admission mirror needs no appended export.
  const pm2 = await tsImport(pathToFileURL(join(state.source, 'apps/web/src/pm2.tsx')).href, import.meta.url);
  // Protocol 19: the Tailscale target rule is exported too, for the same form/node admission mirror.
  const tailscale = await tsImport(pathToFileURL(join(state.source, 'apps/web/src/tailscale.tsx')).href, import.meta.url);
  // Protocol 19 real browser: the endpoint, token and wait rules and the builder are exported for the same admission mirror.
  const browser = await tsImport(pathToFileURL(join(state.source, 'apps/web/src/browser.tsx')).href, import.meta.url);
  // Protocol 19 GameDig: the game, address, port, token and service rules and the builder are exported for the same mirror.
  const gamedig = await tsImport(pathToFileURL(join(state.source, 'apps/web/src/gamedig.tsx')).href, import.meta.url);
  // Protocol 19 status-page presentation: the appearance, public address and feed rules, for the same form/node mirror.
  const presentation = await tsImport(pathToFileURL(join(state.source, 'apps/web/src/statusPresentation.ts')).href, import.meta.url);
  return { ...monitor, ...channel, ...config, ...maintenance, ...specs, ...checks, ...pm2, ...tailscale, ...browser, ...gamedig, ...presentation };
}

async function review() {
  for (const n of state.nodes) secrets.add(n.token);
  report = { startedAt: new Date().toISOString(), runtime:process.version, harnessHash:sha(readFileSync(fileURLToPath(import.meta.url))), runDir, sourceHash: state.sourceHash, binaryHash: state.binaryHash, artifacts: state.artifacts, checks: [], exceptions: [], blocked: [], requests: [] }; save();
  const f = await functionsUnderReview(), [A, B] = state.nodes;
  const base = (type, options = {}) => ({ name: `Review ${type}`, type, target: type === 'manual' ? 'manual' : '127.0.0.1', intervalMs: 60000, timeoutMs: 1000, nodeIds: [A.id], enabled: false, ...options });
  const configs = {
    docker: { endpoint: state.sink, container: 'app', apiVersion: 'v1.51' },
    redis: { address: '127.0.0.1:6379', username: 'review-user', password: 'review-password', database: 2, tlsMode: 'plain' },
    postgres: { dsn: 'postgres://review:password@127.0.0.1:5432/app', query: 'SELECT 42', condition: { operator: 'eq', expected: 42 }, tlsMode: 'plain' },
    mysql: { dsn: 'review:password@tcp(127.0.0.1:3306)/app', query: 'SELECT 42', condition: { operator: 'eq', expected: 42 }, tlsMode: 'plain' },
    sqlserver: { dsn: 'sqlserver://review:password@127.0.0.1:1433/app', query: 'SELECT 42', condition: { operator: 'eq', expected: 42 }, tlsMode: 'plain' },
    oracledb: { dsn: 'oracle://review:password@127.0.0.1:1521/APP', query: 'SELECT 42 FROM DUAL', tlsMode: 'plain' },
    mongodb: { dsn: 'mongodb://review:password@127.0.0.1:27017/app', tlsMode: 'plain', collection: 'health', filter: { ok: true }, scalarPath: ['state'], condition: { operator: 'eq', expected: null } },
    mqtt: { address: '127.0.0.1:1883', topic: 'health/+', username: 'review', password: 'review-password', tlsMode: 'plain', condition: { operator: 'eq', expected: 'ok' } },
    sftp: { address: '127.0.0.1:22', username: 'review', password: 'review-password', unsafeIgnoreHostKey: true, path: '/health' },
    ntp: { address: '127.0.0.1:123', stratumThreshold: 5, offsetThresholdMs: 750, rootDispersionThresholdMs: 500, latencyThresholdMs: 1000 },
    grpc: { address: '127.0.0.1:9999', mode: 'health', healthService: 'app', tlsMode: 'plain', metadata: { authorization: 'Bearer review-private' } },
    snmp: { address: '127.0.0.1:161', version: '3', oid: '1.3.6.1.2.1.1.1.0', username: 'review', securityLevel: 'authPriv', authProtocol: 'sha256', authPassphrase: 'review-auth', privacyProtocol: 'aes', privacyPassphrase: 'review-privacy', contextName: 'review' },
    radius: { address: '127.0.0.1:1812', username: 'review', password: 'review-password', secret: 'review-shared-secret', calledStationId: 'called', callingStationId: 'calling', unsafeAllowMissingMessageAuthenticator: true },
    'sip-options': { address: '127.0.0.1:5060', transport: 'udp', requestUri: 'sip:127.0.0.1:5060', expectedStatus: [200, 202] },
    rabbitmq: { mode: 'amqp', address: '127.0.0.1:5672', username: 'review', password: 'review-password', virtualHost: '/', tlsMode: 'plain' },
    'kafka-producer': { mode: 'produce', brokers: ['127.0.0.1:9092'], topic: 'review', message: 'review-payload', allowAutoTopicCreation: true, sasl: { mechanism: 'plain', username: 'review', password: 'review-password' }, tlsMode: 'plain' },
  };
  const monitors = {};
  for (const [type, options] of Object.entries(configs)) await step(`API + pure form: ${type} exact service round-trip and one-field edit`, async () => {
    const key = f.SERVICE_PROTOCOL[type], created = await api(A, '/api/monitors', 'POST', base(type, { service: { [key]: options } }));
    assert.equal(created.status, 201, created.text); const m = created.value; monitors[type] = m;
    const d = f.draftFrom(m, [A.id]), initial = structuredClone(d); d.name += ' renamed'; const built = f.build(d, m, initial);
    assert.ok(built.input, JSON.stringify({ ...built.errors, ...built.serviceErrors })); assert.deepEqual(serial(built.input.service), serial(m.service));
    const saved = await api(A, `/api/monitors/${m.id}`, 'PUT', { ...built.input, revision: m.revision }); assert.equal(saved.status, 200, saved.text);
    const changed = structuredClone(d), field = f.SERVICE_FIELDS[key].find(x => x.key === 'address') || f.SERVICE_FIELDS[key].find(x => x.key === 'query') || f.SERVICE_FIELDS[key].find(x => x.key === 'container') || f.SERVICE_FIELDS[key].find(x => x.key === 'topic');
    if (field) { changed.service[type][field.key] = field.key === 'address' ? '127.0.0.1:9998' : field.key === 'query' ? (type === 'oracledb' ? 'SELECT 43 FROM DUAL' : 'SELECT 43') : 'review-changed'; const edit = f.build(changed, m, initial); assert.ok(edit.input, JSON.stringify(edit.errors)); const expected = structuredClone(m.service); expected[key][field.key] = changed.service[type][field.key]; assert.deepEqual(serial(edit.input.service), serial(expected)); }
    monitors[type] = saved.value;
    return { id: m.id, protocol: key, fieldCount: f.SERVICE_FIELDS[key].length, optionsPreserved: true };
  });
  // Protocol 16 accepted 27 types; protocol 17 explicitly adds the Steam directory monitor.
  // Protocol 17 accepted 28 types; protocol 18 explicitly adds the PM2 process monitor.
  // Protocol 18 accepted 29 types; protocol 19 explicitly adds the Tailscale peer ping (validation.go: typed target).
  // Protocol 19 also explicitly adds the real browser (validation.go: an HTTP(S) typed target with `browser`), so 30 -> 31.
  // Protocol 19 also explicitly adds GameDig (validation.go: a `gamedig` options object for an optional query service), so 31 -> 32.
  await step('Catalogue matches all 32 implemented check types, including Globalping, system-service, Steam, PM2, Tailscale, real browser and GameDig', async () => {
    assert.equal(f.CHECK_TYPES.length, 32); assert.equal(new Set(f.CHECK_TYPES.map(v => v.type)).size, 32);
    assert.ok(f.CHECK_TYPES.some(v => v.type === 'gamedig'));
    assert.ok(f.CHECK_TYPES.some(v => v.type === 'real-browser'));
    assert.ok(f.CHECK_TYPES.some(v => v.type === 'globalping'));
    assert.ok(f.CHECK_TYPES.some(v => v.type === 'system-service'));
    assert.ok(f.CHECK_TYPES.some(v => v.type === 'steam'));
    assert.ok(f.CHECK_TYPES.some(v => v.type === 'pm2'));
    assert.ok(f.CHECK_TYPES.some(v => v.type === 'tailscale-ping'));
    assert.equal(Object.keys(f.SERVICE_PROTOCOL).length, 16);
    assert.equal(f.PROVIDERS.length, 11);
    return { types: f.CHECK_TYPES.map(v => v.type), serviceProtocols: Object.keys(f.SERVICE_PROTOCOL).length };
  });
  await step('All 16 structured service forms cover the Go DTO fields', async () => {
    const goText = [...files(join(state.source, 'apps/node-go/internal/services')), join(state.source, 'apps/node-go/internal/canary/types.go')].filter(p => p.endsWith('.go') && !p.endsWith('_test.go')).map(p => readFileSync(p, 'utf8')).join('\n');
    const names = { docker:'DockerConfig', redis:'RedisConfig', postgres:'SQLConfig', mysql:'SQLConfig', sqlserver:'SQLConfig', oracle:'SQLConfig', mongodb:'MongoDBConfig', mqtt:'MQTTConfig', sftp:'SFTPConfig', ntp:'NTPMonitorOptions', grpc:'GRPCConfig', snmp:'SNMPConfig', radius:'RADIUSConfig', sip:'SIPConfig', rabbitmq:'RabbitMQConfig', kafka:'KafkaConfig' };
    const keys = name => [...goText.match(new RegExp(`type ${name} struct \\{([\\s\\S]*?)\\n\\}`))[1].matchAll(/json:"([^",]+)(?:,[^"]*)?"/g)].map(m => m[1]);
    for (const [protocol, name] of Object.entries(names)) {
      const dto = keys(name), form = f.SERVICE_FIELDS[protocol].map(v => v.key), modeled = new Set(form.map(v => v.split('.')[0]));
      assert.deepEqual([...new Set(dto)].sort(), [...modeled].sort(), `${protocol} DTO versus form`);
      if (dto.includes('tls')) for (const k of keys('TLSConfig').filter(k => !(protocol === 'oracle' && k === 'ignoreVerification'))) assert.ok(form.includes('tls.' + k), `${protocol} tls.${k}`);
      if (dto.includes('sasl')) for (const k of keys('KafkaSASLConfig')) assert.ok(form.includes('sasl.' + k));
    }
    return { protocols: Object.keys(names).length, nestedTLSAndSASLCovered: true, oracleVerificationOverrideIntentionallyOmitted: true };
  });
  await step('Pause/manual input omits nulls, retains nested unknown options and credentials', async () => {
    const m = { ...base('http', { target: state.sink + '/ok', http: { method: 'POST', body: 'private', basicAuth: { username: 'review', password: 'private' }, future: { value: null } }, tls: null, notificationIds: null }), id: 'runtime', revision: 'rev', updatedAt: 123, checkGeneration: 'gen' };
    const input = f.inputOf(m); assert.equal(input.tls, undefined); assert.equal(input.notificationIds, undefined); assert.deepEqual(input.http, m.http); assert.equal(input.id, undefined); input.http.future.value = 42; assert.equal(m.http.future.value, null);
    return { topLevelNullOmitted: true, nestedNullKept: true, independentClone: true };
  });
  for (const [type, m] of Object.entries(monitors)) await step(`Conflict check-row projection preserves ${type} credentials/options`, async () => {
    const ctx = { groupMap: new Map(), role: 'admin', channels: { map: new Map() }, tags: { map: new Map() } };
    const projected = Object.assign({}, ...f.monitorRows(ctx, false).map(row => row.project(m)));
    assert.deepEqual(serial(projected.service), serial(m.service));
    const saved = await api(A, `/api/monitors/${m.id}`, 'PUT', { ...projected, revision: m.revision }); assert.equal(saved.status, 200, saved.text);
    assert.deepEqual(saved.value.service, m.service); monitors[type] = saved.value;
    return { serverValidatedProjection: true, actualMultiHeadConflict: false };
  });
  const networkMonitors = {};
  const networkOptions = {
    http: { target: state.sink + '/ok', http: { method: 'POST', body: 'private-body', headers: { 'X-Review': 'private-header' }, basicAuth: { username: 'review', password: 'review-password' }, maxRedirects: 0, keyword: { value: 'ok', negate: true }, json: { path: '$.ok', expected: 'true' } }, expectedStatusCodes: [200, 204], retries: 3, retryIntervalMs: 5000, upsideDown: true, webhookUrl: state.sink + '/hook-legacy' },
    ping: { target: '127.0.0.1', ping: { count: 3, size: 0, family: 'ipv4' } },
    tcp: { target: '127.0.0.1:9999' },
    tls: { target: '127.0.0.1:9999', tls: { serverName: 'localhost', ignoreVerification: true, minValidityDays: 2.5 } },
    dns: { target: 'localhost', dns: { recordType: 'A', server: '127.0.0.1:9999', expected: ['127.0.0.1'] } },
    websocket: { target: 'wss://127.0.0.1:9999/socket', websocket: { headers: { 'X-Review': 'private-header' }, bearerToken: 'review-ws-token', proxyUrl: 'http://127.0.0.1:9999' }, tls: { ignoreVerification: true } },
    smtp: { target: '127.0.0.1:9999', smtp: { tlsMode: 'starttls' }, tls: { serverName: 'localhost', ignoreVerification: true } },
    manual: { manual: { state: 'up', message: 'Review manual result' } },
    push: { push: { token: secret(), allowGet: true } },
    'system-service': { target: 'getty@tty1.service' },
    // Go accepts this clean method-named base prefix. Its original form-only
    // refusal blocked metadata saves; keep the real API/form regression here.
    steam: { target: '127.0.0.9:27015', steam: { apiKey: 'review-steam-private-key', apiUrl: state.sink + '/internal/IGameServersService/GetServerList/v1/', allowInsecureApi: true, apiFailureIsDown: true, skipLatency: true, packetSize: 0 } },
    // Protocol 18: a numeric-looking name stays a literal PM2 name, never an id or PID; odd durations and retries must
    // survive the form's duration fields. Paused, so no node contacts a PM2 daemon.
    pm2: { target: '0042', intervalMs: 61500, timeoutMs: 1234, retries: 2, retryIntervalMs: 2500, pm2: { transport: 'unix', home: '/var/lib/canary-review/.pm2', namespace: 'review ns/pm2' } },
    // Protocol 19: the target is the whole configuration, with no options object. Paused, so no node runs a Tailscale client.
    'tailscale-ping': { target: 'canary-review-peer.example-tailnet.ts.net', timeoutMs: 4321, retries: 1, retryIntervalMs: 5000, upsideDown: true },
    // Protocol 19: a private endpoint with a credential in its query, a bearer token, a wait below the timeout and
    // screenshots on. Paused, so no node connects to a browser service; the discard port 9 is never dialled.
    'real-browser': { target: state.sink + '/browser-page', timeoutMs: 4000, retries: 1, browser: { endpoint: 'ws://127.0.0.1:9/devtools/browser/canary-review?token=review-browser-query-secret', bearerToken: 'review-browser-bearer-secret', screenshot: true, delayMs: 250 } },
    // Protocol 19 GameDig: every option set, a private game token and service token, and an https service address that
    // is never resolved. Paused, so no node contacts a query service or a game server.
    gamedig: { target: 'play.canary-review.test', timeoutMs: 4000, retries: 1, gamedig: { game: 'minecraft', port: 25565, givenPortOnly: true, token: 'review-game-token-secret', endpoint: 'https://game-query.canary-review.test:3121', bearerToken: 'review-game-query-bearer-0123456789' } },
  };
  for (const [type, options] of Object.entries(networkOptions)) await step(`API + form: ${type} preserves all non-service options and nullable omission`, async () => {
    if (options.push) secrets.add(options.push.token);
    if (options.steam) secrets.add(options.steam.apiKey);
    if (options.browser) { secrets.add(options.browser.bearerToken); secrets.add('review-browser-query-secret'); }
    if (options.gamedig) { secrets.add(options.gamedig.token); secrets.add(options.gamedig.bearerToken); }
    const made = await api(A, '/api/monitors', 'POST', base(type, options)); assert.equal(made.status, 201, made.text); const m = made.value;
    const d = f.draftFrom(m, [A.id]), initial = structuredClone(d); d.name += ' renamed'; const built = f.build(d, m, initial); assert.ok(built.input, JSON.stringify({ ...built.errors, ...built.serviceErrors }));
    const saved = await api(A, `/api/monitors/${m.id}`, 'PUT', { ...built.input, revision: m.revision }); assert.equal(saved.status, 200, saved.text);
    for (const key of ['http','tls','dns','websocket','smtp','ping','manual','push','steam','pm2','browser','gamedig','expectedStatusCodes','retries','retryIntervalMs','upsideDown','webhookUrl']) assert.deepEqual(saved.value[key], m[key], key);
    assert.equal(saved.value.target, m.target);
    if (type === 'push') { assert.equal(saved.value.pushStartedAt, m.pushStartedAt); assert.equal(saved.value.checkGeneration || saved.value.revision, m.checkGeneration || m.revision); }
    if (type === 'steam') assert.equal(saved.value.checkGeneration || saved.value.revision, m.checkGeneration || m.revision);
    if (type === 'real-browser') {
      assert.equal(saved.value.checkGeneration || saved.value.revision, m.checkGeneration || m.revision);
      assert.equal(JSON.stringify(saved.value.browser), JSON.stringify(m.browser), 'browser options byte for byte');
      assert.deepEqual(m.browser, options.browser, 'the node stored the options as sent');
    }
    if (type === 'pm2') {
      assert.equal(saved.value.checkGeneration || saved.value.revision, m.checkGeneration || m.revision);
      assert.equal(JSON.stringify(saved.value.pm2), JSON.stringify(m.pm2), 'PM2 options byte for byte');
      for (const key of ['intervalMs', 'timeoutMs']) assert.equal(saved.value[key], m[key], key);
    }
    if (type === 'gamedig') {
      assert.equal(saved.value.checkGeneration || saved.value.revision, m.checkGeneration || m.revision);
      assert.equal(JSON.stringify(saved.value.gamedig), JSON.stringify(m.gamedig), 'GameDig options byte for byte');
      assert.deepEqual(m.gamedig, options.gamedig, 'the node stored the options as sent');
      assert.equal(saved.value.target, options.target);
    }
    networkMonitors[type] = saved.value; return { id: m.id, allOptionsPreserved: true };
  });
  // r5 (protocol 19): DNS SRV/SOA/CAA, WebSocket subprotocols and OAuth2, TLS on TCP and the monitor description. Each
  // case is a stored-shape monitor: the form must accept exactly what the node accepts, and sends what its builder
  // produced. The existing paused DNS, WebSocket and TCP monitors take every case by PUT, so no monitor is created and no
  // node queries a resolver or opens a connection. The last accepted case of each stays for the browser and viewer steps.
  await step('API + form (r5): DNS SRV/SOA/CAA, WebSocket subprotocols and OAuth2, TCP TLS and descriptions are accepted and refused exactly as on the node', async () => {
    const wsSecret = 'review-ws-oauth-secret'; secrets.add(wsSecret);
    const ws = networkMonitors.websocket.websocket, oauth2 = { tokenUrl: state.sink + '/token', clientId: 'review-ws-client', clientSecret: wsSecret, allowInsecureHttp: true };
    const results = [];
    const run = async (type, patch, label) => {
      const current = networkMonitors[type], candidate = { ...current, ...patch };
      for (const [key, value] of Object.entries(patch)) if (value === undefined) delete candidate[key];
      const d = f.draftFrom(candidate, [A.id]), built = f.build(d, candidate, undefined), formAccepts = Boolean(built.input);
      const body = built.input ?? Object.fromEntries(Object.entries(f.inputOf(candidate)));
      const saved = await api(A, `/api/monitors/${current.id}`, 'PUT', { ...body, revision: current.revision });
      assert.ok(saved.status === 200 || saved.status === 400, `${label}: ${saved.status} ${saved.text}`);
      assert.equal(saved.status === 200, formAccepts, `${label}: form ${formAccepts ? 'accepts' : `refuses (${Object.values(built.errors).join(' ')})`}, node answered ${saved.status}`);
      if (saved.status === 200) {
        // The node re-encodes in Go's field order, so values are compared, not key order.
        for (const key of Object.keys(patch)) assert.deepEqual(saved.value[key], body[key], `${label}: ${key} stored as sent`);
        assert.equal(saved.value.enabled, false, 'admission cases stay paused');
        networkMonitors[type] = saved.value;
      }
      results.push({ label, accepted: formAccepts });
      return saved;
    };
    const dns = [
      [{ recordType: 'SRV', expected: ['10 5 5060 sip.example.com'] }, 'SRV, system resolver'], [{ recordType: 'SRV', server: '127.0.0.1:9999', expected: ['010 05 5060 SIP.Example.COM.'] }, 'SRV non-canonical'],
      [{ recordType: 'SRV', expected: ['0 0 65536 host.invalid'] }, 'SRV port out of range'], [{ recordType: 'SOA' }, 'SOA without resolver'],
      [{ recordType: 'SOA', server: '127.0.0.1:9999', expected: ['ns1.example.com hostmaster.example.com 2026102401 7200 3600 1209600 300'] }, 'SOA'],
      [{ recordType: 'SOA', server: '127.0.0.1:9999', expected: ['ns.invalid host.invalid 4294967296 1 1 1 1'] }, 'SOA serial out of range'], [{ recordType: 'CAA' }, 'CAA without resolver'],
      [{ recordType: 'CAA', server: '127.0.0.1:9999', expected: ['0 issue "letsencrypt.org"', '0 ISSUE pki.goog'] }, 'CAA quoted and unquoted'],
      [{ recordType: 'CAA', server: '127.0.0.1:9999', expected: ['0 bad-tag "value"'] }, 'CAA tag'], [{ recordType: 'CAA', server: '127.0.0.1:9999', expected: ['0 issue "unfinished'] }, 'CAA unfinished value'],
      [{ recordType: 'A', server: '127.0.0.1:9999', expected: ['127.0.0.1'] }, 'A restored'],
    ];
    for (const [options, label] of dns) await run('dns', { dns: options }, `dns ${label}`);
    const websocket = [
      [{ ...ws, subprotocols: ['chat', 'v2'], requireSubprotocol: true }, 'offers, one required'], [{ ...ws, subprotocols: ['chat', 'chat'] }, 'duplicate offer'], [{ ...ws, requireSubprotocol: true }, 'required without offers'],
      [{ ...ws, subprotocols: Array.from({ length: 17 }, (_, i) => `p${i}`) }, '17 offers'], [{ ...ws, subprotocols: ['a'.repeat(129)] }, 'offer over 128 bytes'],
      [{ headers: ws.headers, proxyUrl: ws.proxyUrl, oauth2: { ...oauth2, allowInsecureHttp: false } }, 'OAuth2 with an http:// token endpoint and no plain-HTTP allowance'],
      [{ headers: { ...ws.headers, Authorization: 'Bearer review-other' }, oauth2 }, 'OAuth2 with an Authorization header'],
      [{ headers: ws.headers, proxyUrl: ws.proxyUrl, oauth2, subprotocols: ['chat', 'v2'], requireSubprotocol: true }, 'OAuth2 and offers'],
    ];
    for (const [options, label] of websocket) await run('websocket', { websocket: options }, `websocket ${label}`);
    const before = networkMonitors.tcp;
    for (const [patch, label] of [[{ tls: {} }, 'TLS with defaults'], [{ tls: { serverName: 'localhost', minValidityDays: 3 } }, 'TLS with options'], [{ tls: {} }, 'TLS with defaults again']]) await run('tcp', patch, `tcp ${label}`);
    assert.notEqual(networkMonitors.tcp.checkGeneration || networkMonitors.tcp.revision, before.checkGeneration || before.revision, 'turning TLS on is a new check');
    const description = 'Review description: private notes\nsecond line';
    for (const [text, label] of [['a'.repeat(8192), '8,192 bytes'], ['a'.repeat(8193), '8,193 bytes'], ['tab\there', 'tab'], ['cr\rhere', 'carriage return'], [description, 'two lines']]) {
      const previous = networkMonitors.tcp;
      const saved = await run('tcp', { description: text }, `description ${label}`);
      if (saved.status === 200) assert.equal(saved.value.checkGeneration || saved.value.revision, previous.checkGeneration || previous.revision, `${label}: a description keeps the check generation`);
    }
    assert.equal(networkMonitors.tcp.description, description); assert.deepEqual(networkMonitors.tcp.tls, {});
    return { cases: results.length, accepted: results.filter(v => v.accepted).length, refused: results.filter(v => !v.accepted).length, monitorsCreated: 0 };
  });
  const maintenanceWindows = {};
  for (const strategy of ['once','interval','daily','weekly','monthly','cron']) await step(`Maintenance ${strategy}: API + form exact schedule preservation`, async () => {
    const schedule = { strategy, timezone:'America/New_York', startDate:'2026-10-01', endDate:'2026-11-30', durationMinutes:75, ...(strategy === 'cron' ? { cron:'0 2 * * *' } : { startTime:'02:15' }), ...(strategy === 'interval' ? { intervalDays:3 } : {}), ...(strategy === 'weekly' ? { weekdays:[1,7] } : {}), ...(strategy === 'monthly' ? { daysOfMonth:[1,15], lastDayOfMonth:true } : {}) };
    const made = await api(A, '/api/maintenance', 'POST', { name:`Review ${strategy}`, enabled:false, allMonitors:true, schedule }); assert.equal(made.status, 201, made.text); const m = made.value;
    const d = f.mDraft(m); d.name += ' renamed'; const built = f.buildMaintenance(d); assert.ok(built.input, JSON.stringify(built.errors)); assert.deepEqual(built.input.schedule, m.schedule);
    const saved = await api(A, `/api/maintenance/${m.id}`, 'PUT', { ...built.input, revision:m.revision }); assert.equal(saved.status, 200, saved.text); assert.deepEqual(saved.value.schedule, m.schedule); maintenanceWindows[strategy] = saved.value;
    return { id:m.id, schedulePreserved:true, nodeCompilesDST:true };
  });
  const channelConfigs = {
    webhook: { kind: 'webhook', webhook: { url: state.sink + '/hook-contract', bearerToken: 'review-hook-key', allowInsecureHTTP: true } },
    discord: { kind: 'discord', webhook: { url: 'https://discord.com/api/webhooks/123456789/review-fixture-never-send' } },
    smtp: { kind: 'smtp', smtp: { server: '127.0.0.1', port: 2525, tlsMode: 'starttls', username: 'review', password: 'review-smtp-password' }, sender: { email: 'review@example.test', name: 'Review' }, recipients: [{ email: 'recipient@example.test', name: 'Recipient' }] },
    mailgun: { kind: 'mailgun', mailgun: { apiKey: 'review-mailgun-key', domain: 'example.test', region: 'EU', baseURL: state.sink, allowInsecureHTTP: true }, sender: { email: 'review@example.test' }, recipients: [{ email: 'recipient@example.test' }] },
    // Go accepts an absent region as US, and the raw value is part of each recipient's destination ID: a form rename
    // must not write "US" over it (2026-10-24 review M1).
    'mailgun-default-region': { kind: 'mailgun', mailgun: { apiKey: 'review-mailgun-key', domain: 'example.test', baseURL: state.sink, allowInsecureHTTP: true }, sender: { email: 'review@example.test' }, recipients: [{ email: 'recipient@example.test' }] },
    brevo: { kind: 'brevo', brevo: { apiKey: 'review-brevo-key', baseURL: state.sink, allowInsecureHTTP: true }, sender: { email: 'review@example.test' }, recipients: [{ email: 'recipient@example.test' }] },
    mandrill: { kind: 'mandrill', mandrill: { apiKey: 'review-mandrill-key', baseURL: state.sink, allowInsecureHTTP: true }, sender: { email: 'review@example.test' }, recipients: [{ email: 'recipient@example.test' }] },
    slack: { kind: 'slack', slack: { webhookURL: state.sink + '/services/T0REVIEW/B0REVIEW/review-slack-secret', allowInsecureHTTP: true } },
    telegram: { kind: 'telegram', telegram: { botToken: '123456:review-telegram-token', chatId: '-1001234567890', messageThreadId: 7, silent: true, protectContent: true, disableLinkPreview: true, baseURL: state.sink + '/tg', allowInsecureHTTP: true } },
    ntfy: { kind: 'ntfy', ntfy: { topic: 'review-topic', token: 'tk_review-ntfy-token', priority: 4, tags: ['warning', 'skull'], baseURL: state.sink + '/ntfy', allowInsecureHTTP: true } },
    gotify: { kind: 'gotify', gotify: { appToken: 'review-gotify-token', priority: 5, baseURL: state.sink + '/gotify', allowInsecureHTTP: true } },
    pushover: { kind: 'pushover', pushover: { appToken: 'a'.repeat(30), userKey: 'u'.repeat(30), device: 'phone_1', priority: 2, sound: 'siren', retrySeconds: 60, expireSeconds: 3600, baseURL: state.sink + '/po', allowInsecureHTTP: true } },
  }, channels = {};
  for (const [kind, config] of Object.entries(channelConfigs)) await step(`Channel ${kind} API + form rename preserves credentials and provider options`, async () => {
    const made = await api(A, '/api/notification-channels', 'POST', { name: `Review ${kind}`, enabled: false, config }); assert.equal(made.status, 201, made.text); const m = made.value;
    const d = f.channelDraft(m); d.name += ' renamed'; const built = f.buildChannel(d, m); assert.ok(built.input, JSON.stringify(built.errors));
    for (const key of ['webhook', 'smtp', 'mailgun', 'brevo', 'mandrill', 'slack', 'telegram', 'ntfy', 'gotify', 'pushover', 'sender', 'recipients', 'templates']) if (config[key]) assert.deepEqual(serial(built.input.config[key]), serial(m.config[key]));
    const saved = await api(A, `/api/notification-channels/${m.id}`, 'PUT', { ...built.input, revision: m.revision }); assert.equal(saved.status, 200, saved.text); assert.deepEqual(saved.value.config, m.config); channels[kind] = saved.value;
    return { id: m.id, savedWithoutSending: !existsSync(join(runDir, 'sink.json')) };
  });
  let emptyKafka, emptyRabbit;
  await step('Regression: accepted empty Kafka produce payload remains editable', async () => {
    const options = { ...configs['kafka-producer'], message: '' }; const made = await api(A, '/api/monitors', 'POST', base('kafka-producer', { name: 'Review empty Kafka', service: { kafka: options } }));
    assert.equal(made.status, 201, made.text); emptyKafka = made.value; const d = f.draftFrom(emptyKafka, [A.id]), initial = structuredClone(d); d.name += ' renamed'; const built = f.build(d, emptyKafka, initial);
    assert.ok(built.input, `Go accepted message=""; untouched form blocks rename: ${JSON.stringify(built.serviceErrors)}`);
  });
  await step('Regression: accepted empty RabbitMQ password remains editable', async () => {
    const options = { ...configs.rabbitmq, password:'' }, made = await api(A, '/api/monitors', 'POST', base('rabbitmq', { name:'Review empty RabbitMQ password', service:{ rabbitmq:options } }));
    assert.equal(made.status, 201, made.text); emptyRabbit = made.value; const d = f.draftFrom(emptyRabbit, [A.id]), initial = structuredClone(d); d.name += ' renamed'; const built = f.build(d, emptyRabbit, initial);
    assert.ok(built.input, `Go accepted password=""; untouched form blocks rename: ${JSON.stringify(built.serviceErrors)}`);
  });
  await step('HTTP validation: Go also rejects oversized targets and authenticated proxy URLs', async () => {
    const long = await api(A, '/api/monitors', 'POST', base('http', { target: state.sink + '/ok?q=' + 'x'.repeat(2100) })); assert.equal(long.status, 400);
    const proxy = await api(A, '/api/monitors', 'POST', base('http', { target: state.sink + '/ok', http: { proxyUrl: `http://review:password@127.0.0.1:9999` } })); assert.equal(proxy.status, 400);
    return { oversizedTarget: long.status, authenticatedProxy: proxy.status };
  });
  await step('Unknown service options survive a modeled field edit (forward compatibility)', async () => {
    const m = structuredClone(monitors.redis); m.service.futureEnvelope = { enabled: true }; m.service.redis.futureOption = { nullable: null };
    const d = f.draftFrom(m, [A.id]), initial = structuredClone(d); d.service.redis.address = '127.0.0.1:6380'; const built = f.build(d, m, initial);
    assert.ok(built.input); assert.deepEqual(built.input.service.redis.futureOption, m.service.redis.futureOption); assert.deepEqual(built.input.service.futureEnvelope, m.service.futureEnvelope);
  });
  // A form stricter than the node blocks renames of stored monitors; a looser one surfaces only as a failed save.
  // JSON.stringify sends an unpaired surrogate as a lone escape. Go's decoder alone would turn it into U+FFFD, but the
  // node's PM2 admission (validatePM2MonitorJSON, through validStringEscapes) refuses it with 400, and so does the form.
  await step('PM2 admission: the form and the node accept exactly the same names, homes and namespaces', async () => {
    const c = String.fromCodePoint, home = '/var/lib/canary-review/.pm2', results = [], lone = `api${c(0xd800)}b`;
    const cases = [
      ['target', '0042'], ['target', 'api worker/1 $~*'], ['target', `${c(0xfeff)}api`], ['target', 'a'.repeat(256)], ['target', c(0xe9).repeat(129)],
      ['target', ' api'], ['target', `api${c(0xa0)}`], ['target', 'api\tworker'], ['target', `api${c(0x85)}`],
      ['home', '/'], ['home', '/srv/pm2 home'], ['home', `/${'a'.repeat(97)}`], ['home', `/${'a'.repeat(98)}`], ['home', '~/.pm2'], ['home', '/home/~x/.pm2'],
      ['home', '/home/$USER/.pm2'], ['home', '/home/x\\y'], ['home', '/home/deploy/.pm2/'], ['home', '/home/../x'], ['home', 'relative/.pm2'], ['home', ' /x'],
      ['namespace', 'Prod ns/1'], ['namespace', ' prod'], ['namespace', 'n'.repeat(257)],
      // 2026-10-24 r3 review boundaries: UTF-8 bytes on both sides of the socket limit (1 + 96 + 9 = 106, 1 + 98 + 9 = 108),
      // a doubled leading slash path.Clean removes, a literal "..." segment it keeps, Unicode White_Space line and
      // ideographic spaces at the end, a 256-byte namespace, a hidden home the pipe never sends and an unknown connection.
      ['home', `/${c(0xe9).repeat(48)}`], ['home', `/${c(0xe9).repeat(49)}`], ['home', '//home'], ['home', '/x/...'],
      ['target', `api${c(0x2028)}`], ['namespace', 'n'.repeat(256)], ['namespace', `prod${c(0x3000)}`],
      ['transport', 'windows-pipe', { home: '~/hidden' }], ['transport', 'tcp-future'],
      // 2026-10-24 r6 review: a name with an unpaired surrogate, which both sides refuse.
      ['target', lone],
    ];
    for (const [field, value, extra = {}] of cases) {
      const errors = {}, built = f.buildPm2(undefined, { ...f.pm2DraftFrom(undefined, 'api'), home, ...extra, [field]: value }, errors);
      const made = await api(A, '/api/monitors', 'POST', base('pm2', { name: `Review PM2 admission ${results.length + 1}`, target: built.target, pm2: built.value }));
      const formAccepts = !Object.keys(errors).length, nodeAccepts = made.status === 201;
      assert.ok(nodeAccepts || made.status === 400, made.text);
      assert.equal(nodeAccepts, formAccepts, `${field} ${JSON.stringify(value)}: form ${formAccepts ? 'accepts' : `refuses (${Object.values(errors).join(' ')})`}, node answered ${made.status}`);
      if (value === lone) assert.equal(nodeAccepts, false, 'the node refuses a name with an unpaired surrogate');
      results.push({ field, accepted: nodeAccepts });
    }
    return { cases: results.length, accepted: results.filter(v => v.accepted).length, refused: results.filter(v => !v.accepted).length };
  });
  // Protocol 19: tailscale.tsx mirrors tailscalemonitor.Validate (Go net/netip for literal addresses, ASCII labels for
  // names). Both sides get the same raw bytes; every case is paused, so no node runs a Tailscale client.
  await step('Tailscale admission: the form and the node accept exactly the same peer names and addresses', async () => {
    const c = String.fromCodePoint, results = [];
    const n253 = `${'a'.repeat(63)}.${'b'.repeat(63)}.${'c'.repeat(63)}.${'d'.repeat(61)}`, dotted253 = `${'a'.repeat(63)}.${'b'.repeat(63)}.${'c'.repeat(63)}.${'d'.repeat(60)}.`;
    const cases = [
      'peer', 'Build-Server', 'build-server.example-tailnet.ts.net', 'build-server.example-tailnet.ts.net.', '0', 'xn--caf-dma', 'a'.repeat(63), 'a'.repeat(64), n253, `${n253}d`, dotted253, `${dotted253.slice(0, -1)}d.`,
      '', '.', '..', '.peer', 'peer..net', '-peer', 'peer-', 'pe_er', 'peer name', ' peer', 'peer\n', `pe${c(0)}er`, `peer${c(0x2028)}`, `caf${c(0xe9)}`, `pe${String.fromCharCode(0xd800)}er`,
      '100.101.102.103', '127.0.0.1', '255.255.255.255', '240.0.0.1', '0.0.0.0', '224.0.0.1', '239.255.255.255',
      '01.2.3.4', '1.2.3.256', '1.2.3', '1.2.3.4.5', '1.2.3.4.', '0.0.0.0.', '224.0.0.1.',
      'fd7a:115c:a1e0::1', 'FD7A:115C:A1E0::1', '::1', '1:2:3:4:5:6:7::', '1:2:3:4:5:6:1.2.3.4', '::ffff:100.64.0.1', '::ffff:0.0.0.0',
      '::', '::0.0.0.0', 'ff02::1', '::ffff:224.0.0.1', '1::2::3', '12345::1', '1:2:3:4:5:6:7:8::', '::01.2.3.4', '1:2:3:4:5:6:7:1.2.3.4',
      'fe80::1%eth0', '[::1]', 'peer:41641', '100.101.102.103:41641', 'https://peer', 'peer/x', 'user@peer', 'peer;id', '$(id)', '--help',
    ];
    for (const target of cases) {
      const formAccepts = f.tailscaleTargetAccepted(target);
      assert.equal(f.tailscaleTargetProblem(target) === undefined, formAccepts, `${JSON.stringify(target)}: rule and message agree`);
      const made = await api(A, '/api/monitors', 'POST', base('tailscale-ping', { name: `Review Tailscale admission ${results.length + 1}`, target }));
      assert.ok(made.status === 201 || made.status === 400, made.text);
      assert.equal(made.status === 201, formAccepts, `${JSON.stringify(target)}: form ${formAccepts ? 'accepts' : `refuses (${f.tailscaleTargetProblem(target)})`}, node answered ${made.status}`);
      results.push({ accepted: formAccepts });
    }
    // A typed target's edges are trimmed by the form before it is sent; the node stores the trimmed bytes.
    const trimmed = f.build({ ...f.draftFrom(undefined, [A.id]), name: 'Review Tailscale trimmed', type: 'tailscale-ping', enabled: false, targets: { 'tailscale-ping': '  canary-review-peer  ' } }, undefined, undefined);
    assert.ok(trimmed.input, JSON.stringify(trimmed.errors));
    const made = await api(A, '/api/monitors', 'POST', trimmed.input); assert.equal(made.status, 201, made.text);
    assert.equal(made.value.target, 'canary-review-peer'); assert.equal(made.value.enabled, false);
    return { cases: results.length, accepted: results.filter(v => v.accepted).length, refused: results.filter(v => !v.accepted).length };
  });
  // Protocol 19: browser.tsx mirrors browsermonitor.Validate over Go 1.27's url.Parse (strict colons for http and https)
  // and the node's strict JSON reading. Each case sends the raw value the rule judged; every monitor is paused, so no
  // node connects to a browser service and the discard port 9 is never dialled.
  await step('Real-browser admission: the form and the node accept exactly the same endpoints, tokens and waits', async () => {
    const c = String.fromCodePoint, lone = String.fromCharCode(0xd800), ok = 'ws://127.0.0.1:9/devtools/browser/review', results = [];
    const endpoints = [
      ok, 'wss://chromium.review.test/devtools/browser/x?token=abc', 'WS://Chromium.Review.test:9222/x', 'http://127.0.0.1:9', 'http://127.0.0.1:9/', 'https://chromium.review.test',
      'http://127.0.0.1:9/?', 'ws://127.0.0.1:9/x#', 'ws://[::1]:9/x', 'ws://[fe80::1%25eth0]:9/x', 'ws://[::ffff:127.0.0.1]:9/', 'ws://127.0.0.1:/x', 'ws://127.0.0.1:99999/x',
      'ws://a:1:2/x', 'ws://h/a b', `ws://b${c(0xfc)}cher.review.test/x`, 'ws://h/%41', 'ws://h?x/y', 'ws://a]b/', `ws://${'a'.repeat(2042)}/`,
      '', ' ws://127.0.0.1:9/', 'ftp://127.0.0.1/', 'ws:127.0.0.1:9', 'ws:/127.0.0.1/', 'ws:///x', 'ws://:9/x', 'ws://user:pw@127.0.0.1:9/x', 'ws://@127.0.0.1:9/x',
      'ws://127.0.0.1:9/x#frag', 'http://127.0.0.1:9/json/version', 'http://127.0.0.1:9/?x=1', 'http://127.0.0.1:9/??', 'http://a:1:2/', 'ws://127.0.0.1:x/', 'ws://[127.0.0.1]:9/',
      'ws://[::1/', 'ws://a[b]/', 'ws://[fe80::1%25]:9/', 'ws://[fe80::1%eth0]:9/', 'ws://a b/', 'ws://a%41/', 'ws://h/%zz', 'ws://h/\n', `ws://h/${lone}`, `ws://${'a'.repeat(2043)}/`,
      'ws://h\\x/', 'ws://h^/',
    ];
    const tokens = ['', 'review-token', '!"#$%&\'()*+,-./:;<=>?@[\\]^_`{|}~', 'a'.repeat(4096), 'a'.repeat(4097), 'a b', ' a', 'a\t', 'a\n', `${c(0xe9)}`, `a${c(0)}`, 'a\x7f', 'Bearer abc', `a${lone}`];
    const waits = [[0, 1000], [1, 1000], [999, 1000], [1000, 1000], [30000, 30001], [30000, 30000], [30001, 60000], [-1, 1000], [1.5, 1000]];
    const cases = [
      ...endpoints.map(endpoint => ({ label: `endpoint ${JSON.stringify(endpoint.length > 80 ? `${endpoint.slice(0, 40)}… (${endpoint.length} characters)` : endpoint)}`, form: f.browserEndpointProblem(endpoint), browser: { endpoint } })),
      ...tokens.map((token, index) => ({ label: `token case ${index + 1} (${token.length} characters)`, form: f.browserBearerProblem(token), browser: { endpoint: ok, bearerToken: token } })),
      ...waits.map(([delayMs, timeoutMs]) => ({ label: `wait ${delayMs} ms with a ${timeoutMs} ms timeout`, form: f.browserDelayProblem(String(delayMs), timeoutMs), browser: { endpoint: ok, delayMs }, timeoutMs })),
    ];
    for (const item of cases) {
      const made = await api(A, '/api/monitors', 'POST', base('real-browser', { name: `Review browser admission ${results.length + 1}`, target: state.sink + '/browser-page', timeoutMs: item.timeoutMs ?? 1000, browser: item.browser }));
      assert.ok(made.status === 201 || made.status === 400, made.text);
      const formAccepts = item.form === undefined;
      assert.equal(made.status === 201, formAccepts, `${item.label}: form ${formAccepts ? 'accepts' : `refuses (${item.form})`}, node answered ${made.status}`);
      if (made.status === 201) assert.equal(made.value.enabled, false, 'admission cases stay paused');
      results.push({ accepted: formAccepts });
    }
    // Typed values are trimmed before they are sent, and a zero wait is omitted like Go's omitempty encoding.
    const typed = f.buildBrowser(undefined, { endpoint: `  ${ok}  `, bearer: ' review-typed-token ', screenshot: false, delay: ' 0 ' }, 1000, {});
    assert.deepEqual(typed.value, { endpoint: ok, bearerToken: 'review-typed-token' });
    return { cases: results.length, accepted: results.filter(v => v.accepted).length, refused: results.filter(v => !v.accepted).length };
  });
  // Protocol 19 GameDig: gamedig.tsx mirrors gamedigmonitor.Validate (the Tailscale peer rule for the target, Go 1.27's
  // url.Parse for the query service origin) and the node's strict JSON reading. Each case sends exactly what the form's
  // builder produced. One paused monitor takes every case by PUT, which runs the same parseMonitor admission as a create,
  // so the run stays far below the node's 200-monitor cap. No node contacts a query service or a game server.
  await step('GameDig admission: the form and the node accept exactly the same games, addresses, ports, tokens and service settings', async () => {
    const c = String.fromCodePoint, lone = String.fromCharCode(0xd800), bearer = 'review-game-admission-bearer-0123', results = [];
    secrets.add(bearer);
    const draft = { game: 'minecraft', target: 'play.canary-review.test', port: '', givenPortOnly: false, token: '', endpoint: 'https://game-query.canary-review.test:3121', bearer, insecure: false };
    const first = f.buildGameDig(undefined, draft, {});
    const made = await api(A, '/api/monitors', 'POST', base('gamedig', { name: 'Review GameDig admission', target: first.target, gamedig: first.value }));
    assert.equal(made.status, 201, made.text);
    let current = made.value;
    const cases = [
      ...['minecraft', 'terrariatshock', 'mbe', 'Minecraft', 'not-a-game', ''].map(game => ({ game })),
      ...['203.0.113.10', '2001:db8::10', 'Play-01.canary-review.test.', '01.2.3.4', ' play.canary-review.test ', 'play:25565', '[::1]', 'https://play.canary-review.test', 'fe80::1%eth0',
        '0.0.0.0', '224.0.0.1', 'pl_ay', `caf${c(0xe9)}`, 'a'.repeat(64)].map(target => ({ target })),
      ...['0', '1', '65535', '65536', '-1', '1.5', 'abc', '0x10'].map(port => ({ port })),
      { port: '25565', givenPortOnly: true }, { port: '', givenPortOnly: true }, { port: '0', givenPortOnly: true },
      // r5: a catalogue game without a default port (alienarena) needs an explicit port, with or without "only this port".
      { game: 'alienarena' }, { game: 'alienarena', port: '0' }, { game: 'alienarena', port: '27901' }, { game: 'alienarena', port: '27901', givenPortOnly: true },
      ...['x', ' spaced ', 'a'.repeat(1024), 'a'.repeat(1025), c(0xe9).repeat(512), c(0xe9).repeat(513), `a${c(0x2028)}b`, '<&>', 'a\nb', 'a\tb', `a${c(0x85)}b`, `a${lone}b`].map(token => ({ token })),
      ...['https://game-query.canary-review.test', 'HTTPS://Game-Query.Canary-Review.Test:3121/', 'https://[::1]:3121', 'https://h?', 'https://h#', 'https://h:99999', `https://${'a'.repeat(2040)}`,
        'https://h/v1/query', 'https://h?x=1', 'https://user:pw@h', 'https://h#frag', 'ws://h', 'ftp://h', 'https://a:1:2', 'https://[127.0.0.1]', 'https://a b', `https://${'a'.repeat(2041)}`, `https://h/${lone}`,
      ].map(endpoint => ({ endpoint })),
      { endpoint: 'http://127.0.0.1:9' }, { endpoint: 'http://127.0.0.1:9', insecure: true }, { endpoint: 'HTTP://127.0.0.1:9/', insecure: true }, { insecure: true },
      ...['a'.repeat(23), 'a'.repeat(24), 'a'.repeat(256), 'a'.repeat(257), '!"#$%&\'()*+,-./:;<=>?@[\\]^_`{|}~', ` ${'b'.repeat(30)} `, `${'a'.repeat(24)} b`, `${'a'.repeat(24)}${c(0xe9)}`].map(value => ({ bearer: value })),
    ];
    for (const change of cases) {
      const errors = {}, built = f.buildGameDig(undefined, { ...draft, ...change }, errors), formAccepts = !Object.keys(errors).length;
      const label = JSON.stringify(Object.fromEntries(Object.entries(change).map(([key, value]) => [key, typeof value === 'string' && value.length > 60 ? `${value.slice(0, 30)}… (${value.length} characters)` : value])));
      const saved = await api(A, `/api/monitors/${current.id}`, 'PUT', { ...base('gamedig', { name: 'Review GameDig admission', target: built.target, gamedig: built.value }), revision: current.revision });
      assert.ok(saved.status === 200 || saved.status === 400, `${label}: ${saved.status} ${saved.text}`);
      assert.equal(saved.status === 200, formAccepts, `${label}: form ${formAccepts ? 'accepts' : `refuses (${Object.values(errors).join(' ')})`}, node answered ${saved.status}`);
      if (saved.status === 200) {
        assert.equal(JSON.stringify(saved.value.gamedig), JSON.stringify(built.value), `${label}: stored as sent, in Go’s field order`);
        assert.equal(saved.value.target, built.target); assert.equal(saved.value.enabled, false, 'admission cases stay paused');
        current = saved.value;
      }
      results.push({ accepted: formAccepts });
    }
    return { cases: results.length, accepted: results.filter(v => v.accepted).length, refused: results.filter(v => !v.accepted).length, monitorsCreated: 1 };
  });
  // Monitor clone (frontend only): the form's create body from cloneInput, through the real node. Sources are paused, so
  // their copies are too; a push copy carries a new token, and the source must read back unchanged.
  await step('Clone API + form: copies keep every setting, get a new ID (and push token) and leave the source unchanged', async () => {
    const oauthSecret = 'review-oauth-clone-secret'; secrets.add(oauthSecret);
    const oauth = await api(A, '/api/monitors', 'POST', base('http', { name: 'Review OAuth2 clone source', target: state.sink + '/ok', http: { oauth2: { tokenUrl: state.sink + '/token', clientId: 'review-client', clientSecret: oauthSecret, scope: 'read', allowInsecureHttp: true } } }));
    assert.equal(oauth.status, 201, oauth.text);
    const sources = { http: networkMonitors.http, oauth: oauth.value, redis: monitors.redis, steam: networkMonitors.steam, pm2: networkMonitors.pm2, 'tailscale-ping': networkMonitors['tailscale-ping'], push: networkMonitors.push, 'real-browser': networkMonitors['real-browser'], gamedig: networkMonitors.gamedig };
    const kept = ['type', 'target', 'intervalMs', 'timeoutMs', 'nodeIds', 'enabled', 'http', 'tls', 'dns', 'websocket', 'smtp', 'service', 'ping', 'manual', 'globalping', 'steam', 'pm2', 'browser', 'gamedig', 'expectedStatusCodes', 'retries', 'retryIntervalMs', 'upsideDown', 'webhookUrl', 'notificationIds', 'repeatAfterMs', 'groupId', 'tags'];
    const current = async id => (await api(A, '/api/monitors')).value.find(v => v.monitor.id === id).monitor;
    const ids = [];
    for (const [kind, m] of Object.entries(sources)) {
      const before = await current(m.id), copy = f.cloneInput(before, f.newPushToken);
      if (copy.push) secrets.add(copy.push.token);
      const d = f.draftFrom(copy, [A.id]), built = f.build(d, copy, structuredClone(d));
      assert.ok(built.input, `${kind}: ${JSON.stringify({ ...built.errors, ...built.serviceErrors })}`);
      assert.equal('revision' in built.input || 'id' in built.input, false, `${kind}: a create body`);
      const made = await api(A, '/api/monitors', 'POST', built.input); assert.equal(made.status, 201, made.text);
      const saved = made.value;
      assert.notEqual(saved.id, before.id); assert.equal(saved.name, `${before.name} (copy)`); assert.equal(saved.enabled, false);
      for (const key of kept) assert.deepEqual(saved[key], before[key], `${kind} ${key}`);
      if (kind === 'push') {
        assert.match(saved.push.token, /^[0-9a-f]{64}$/u); assert.notEqual(saved.push.token, before.push.token); assert.equal(saved.push.allowGet, before.push.allowGet);
      } else assert.deepEqual(saved.push, before.push);
      assert.deepEqual(await current(m.id), before, `${kind}: the source is unchanged`);
      ids.push(saved.id);
    }
    return { copies: ids.length, newIds: new Set(ids).size === ids.length, pushTokenNew: true, sourcesUnchanged: true };
  });
  await browserReview({ f, A, B, base, monitors, channels, emptyKafka, emptyRabbit, networkMonitors, maintenanceWindows });
  if (proxyAuthFixture) await reviewProxyAuth({ fixture: proxyAuthFixture, state, A, B, page, step, signIn, until, text, ready, report });
  if (dashboardControlPlaneFixture) await reviewDashboardControlPlane({ fixture: dashboardControlPlaneFixture, A, B, page, step, until, fill, click, text, ready, report, secrets });
  await stopInterceptions?.();
  const manifest = json(join(runDir, 'source-manifest.json'));
  report.sourceChangedAfterSnapshot = manifest.files.filter(v => !existsSync(join(root, v.path)) || sha(readFileSync(join(root, v.path))) !== v.sha256).map(v => v.path);
  report.finishedAt = new Date().toISOString(); report.summary = { passed: report.checks.filter(v => v.passed).length, failed: report.checks.filter(v => !v.passed).length, exceptions: report.exceptions.length, blocked: report.blocked.length }; save();
  console.log(JSON.stringify(report.summary));
  ws?.close(); return report.summary.failed || report.summary.exceptions || report.summary.blocked || report.sourceChangedAfterSnapshot.length ? 1 : 0;
}
async function browserReview({ f, A, B, base, monitors, channels, emptyKafka, emptyRabbit, networkMonitors, maintenanceWindows }) {
  await launchBrowser(); await step('Browser: sign in to private A', () => signIn(A));
  await step('Browser: system service names are literal, locally scoped and preserved by rename', async () => {
    const m = networkMonitors['system-service'];
    await nav(`#/monitors/${m.id}/edit`); await heading('Edit monitor');
    await text('Each selected node checks its own operating system, never another host.');
    assert.equal(await page.eval(`__field('Internal service name').value`), m.target);
    const before = report.requests.filter(r => r.method === 'PUT' && r.url === A.url + '/api/monitors/' + m.id).length;
    await fill('Internal service name', 'ssh*'); await click('Save changes'); await text('Patterns');
    assert.equal(report.requests.filter(r => r.method === 'PUT' && r.url === A.url + '/api/monitors/' + m.id).length, before);
    await fill('Internal service name', m.target); await fill('Name', 'Review system service renamed');
    await click('Save changes'); await heading('Review system service renamed');
    const rows = (await api(A, '/api/monitors')).value;
    const saved = rows.find(v => v.monitor.id === m.id).monitor;
    assert.equal(saved.target, m.target); assert.equal(saved.checkGeneration, m.checkGeneration);
    for (const key of ['http','tls','dns','websocket','smtp','service','ping','manual','push','globalping']) assert.equal(saved[key], undefined, key);
    await text('Not measured; shown as a dash'); await text('ordinary container');
    networkMonitors['system-service'] = saved;
    return { target: saved.target, generationPreserved: true, noManagerQuery: saved.enabled === false };
  });
  await step('System service conflict projection retains the exact target and ordinary settings', async () => {
    const m = networkMonitors['system-service'];
    const ctx = { groupMap: new Map(), role: 'admin', channels: { map: new Map() }, tags: { map: new Map() } };
    const projected = Object.assign({}, ...f.monitorRows(ctx, false).map(row => row.project(m)));
    assert.equal(projected.type, 'system-service'); assert.equal(projected.target, m.target);
    const saved = await api(A, `/api/monitors/${m.id}`, 'PUT', { ...projected, revision: m.revision });
    assert.equal(saved.status, 200, saved.text); assert.equal(saved.value.target, m.target);
    assert.equal(saved.value.checkGeneration, m.checkGeneration);
    return { serverValidatedProjection: true, actualMultiHeadConflict: false };
  });
  await step('Browser: Steam fields preserve options and reject an invalid game port before saving', async () => {
    const m = networkMonitors.steam;
    await nav(`#/monitors/${m.id}/edit`); await heading('Edit monitor');
    await text('Each check tells the Steam API which server you watch');
    assert.equal(await page.eval(`__field('Server address').value`), m.target);
    assert.equal(await page.eval(`__field('Steam Web API key').type`), 'password');
    const before = report.requests.filter(r => r.method === 'PUT' && r.url === A.url + '/api/monitors/' + m.id).length;
    await fill('Server address', '127.0.0.9:0'); await click('Save changes'); await text('whole number from 1 to 65535');
    assert.equal(report.requests.filter(r => r.method === 'PUT' && r.url === A.url + '/api/monitors/' + m.id).length, before);
    await fill('Server address', m.target); await fill('Name', 'Review Steam fields renamed'); await click('Save changes');
    await heading('Review Steam fields renamed');
    const saved = (await api(A, '/api/monitors')).value.find(v => v.monitor.id === m.id).monitor;
    assert.equal(saved.target, m.target); assert.deepEqual(saved.steam, m.steam);
    assert.equal(saved.checkGeneration || saved.revision, m.checkGeneration || m.revision);
    networkMonitors.steam = saved;
    return { targetPreserved: true, optionsPreserved: true, generationPreserved: true, noDirectoryRequests: saved.enabled === false };
  });
  await step('Browser: Tailscale peer target loads, refuses a port before saving and survives a rename', async () => {
    const m = networkMonitors['tailscale-ping'];
    await nav(`#/monitors/${m.id}/edit`); await heading('Edit monitor');
    await text('Each selected node asks its own Tailscale client, never another machine’s.');
    await text('The ordinary Alpine node image has neither the client nor the daemon');
    assert.equal(await page.eval(`__field('Peer name or Tailscale address').value`), m.target);
    const puts = () => report.requests.filter(r => r.method === 'PUT' && r.url === A.url + '/api/monitors/' + m.id).length, before = puts();
    await fill('Peer name or Tailscale address', 'canary-review-peer:41641'); await click('Save changes'); await text('Remove the port');
    assert.equal(puts(), before);
    await fill('Peer name or Tailscale address', m.target); await fill('Name', 'Review Tailscale renamed'); await click('Save changes');
    await heading('Review Tailscale renamed');
    const saved = (await api(A, '/api/monitors')).value.find(v => v.monitor.id === m.id).monitor;
    assert.equal(saved.target, m.target); assert.equal(saved.checkGeneration || saved.revision, m.checkGeneration || m.revision);
    for (const key of ['http', 'tls', 'dns', 'websocket', 'smtp', 'service', 'ping', 'manual', 'push', 'globalping', 'steam', 'pm2']) assert.equal(saved[key], undefined, key);
    for (const key of ['timeoutMs', 'retries', 'retryIntervalMs', 'upsideDown']) assert.equal(saved[key], m[key], key);
    await text('Tailscale peer replied'); await text('Pending is never inverted and never recovers an outage.');
    networkMonitors['tailscale-ping'] = saved;
    return { targetPreserved: true, generationPreserved: true, noClientRun: saved.enabled === false };
  });
  await step('Tailscale conflict projection retains the exact target and ordinary settings', async () => {
    const m = networkMonitors['tailscale-ping'];
    const ctx = { groupMap: new Map(), role: 'admin', channels: { map: new Map() }, tags: { map: new Map() } };
    const projected = Object.assign({}, ...f.monitorRows(ctx, false).map(row => row.project(m)));
    assert.equal(projected.type, 'tailscale-ping'); assert.equal(projected.target, m.target);
    const saved = await api(A, `/api/monitors/${m.id}`, 'PUT', { ...projected, revision: m.revision });
    assert.equal(saved.status, 200, saved.text); assert.equal(saved.value.target, m.target);
    assert.equal(saved.value.checkGeneration, m.checkGeneration);
    networkMonitors['tailscale-ping'] = saved.value;
    return { serverValidatedProjection: true, actualMultiHeadConflict: false };
  });
  await step('Browser: Steam key remains hidden in a revealed JSON preview and keep-current restores the original', async () => {
    const m = networkMonitors.steam;
    await nav(`#/monitors/${m.id}/edit`); await heading('Edit monitor');
    const replacement = 'review-steam-replacement-key'; secrets.add(replacement);
    await fill('Steam Web API key', replacement); await text('Saving a different key starts a new check generation');
    await click('Keep the current key'); assert.equal(await page.eval(`__field('Steam Web API key').value`), m.steam.apiKey);
    await disclose('JSON (advanced)'); await text('The Steam API key always stays masked here'); await click('Show credentials');
    const preview = await page.eval(`document.querySelector('[aria-label="Monitor JSON preview"]').textContent`);
    assert.ok(!preview.includes(m.steam.apiKey) && !preview.includes(replacement));
    const parsed = JSON.parse(preview); assert.equal(parsed.steam.packetSize, 0); assert.equal(parsed.steam.apiUrl, m.steam.apiUrl);
    assert.ok(!JSON.stringify(f.masked(m)).includes(m.steam.apiKey));
    return { revealedPreviewKeyHidden: true, optionalZeroKept: true, keyRestored: true };
  });
  await step('Steam conflict projection retains private options in both display modes', async () => {
    const m = networkMonitors.steam;
    const ctx = { groupMap: new Map(), role: 'admin', channels: { map: new Map() }, tags: { map: new Map() } };
    let projected;
    for (const reveal of [false, true]) {
      projected = Object.assign({}, ...f.monitorRows(ctx, reveal).map(row => row.project(m)));
      assert.deepEqual(projected.steam, m.steam); assert.equal(projected.target, m.target);
    }
    const saved = await api(A, `/api/monitors/${m.id}`, 'PUT', { ...projected, revision: m.revision });
    assert.equal(saved.status, 200, saved.text); assert.deepEqual(saved.value.steam, m.steam);
    assert.equal(saved.value.checkGeneration || saved.value.revision, m.checkGeneration || m.revision);
    networkMonitors.steam = saved.value;
    return { serverValidatedProjection: true, bothDisplayModes: true, actualMultiHeadConflict: false };
  });
  await step('Browser: PM2 fields keep the literal name and options, refuse a ~ home before saving, and hide the home for the Windows pipe', async () => {
    const m = networkMonitors.pm2;
    await nav(`#/monitors/${m.id}/edit`); await heading('Edit monitor');
    await text('Each selected node asks the PM2 daemon on its own machine');
    assert.equal(await page.eval(`__field('Process name').value`), m.target);
    assert.equal(await page.eval(`__field('PM2 home').value`), m.pm2.home);
    assert.equal(await page.eval(`__field('Namespace').value`), m.pm2.namespace);
    const puts = () => report.requests.filter(r => r.method === 'PUT' && r.url === A.url + '/api/monitors/' + m.id).length, before = puts();
    await fill('PM2 home', '~/.pm2'); await click('Save changes'); await text('Write the full path without ~ or $VARIABLES');
    assert.equal(puts(), before);
    const homeShown = () => page.eval(`[...document.querySelectorAll('input')].some(e => __vis(e) && [...e.labels || []].some(l => __norm(l.innerText) === 'PM2 home'))`);
    await choose('Windows named pipe'); await text('PM2 on Windows uses one fixed local pipe'); assert.equal(await homeShown(), false);
    await choose('Unix socket'); assert.equal(await page.eval(`__field('PM2 home').value`), '~/.pm2', 'the hidden home is kept in the draft');
    await fill('PM2 home', m.pm2.home); await fill('Name', 'Review PM2 fields renamed'); await click('Save changes');
    await heading('Review PM2 fields renamed'); await text('Not measured; shown as a dash');
    const saved = (await api(A, '/api/monitors')).value.find(v => v.monitor.id === m.id).monitor;
    assert.equal(saved.target, m.target); assert.equal(JSON.stringify(saved.pm2), JSON.stringify(m.pm2));
    for (const key of ['intervalMs', 'timeoutMs', 'retries', 'retryIntervalMs']) assert.equal(saved[key], m[key], key);
    assert.equal(saved.checkGeneration || saved.revision, m.checkGeneration || m.revision);
    networkMonitors.pm2 = saved;
    return { literalName: saved.target, optionsPreserved: true, generationPreserved: true, refusedBeforeSave: true, windowsHidesHome: true, noDaemonQuery: saved.enabled === false };
  });
  await step('Browser: the PM2 home is masked in the JSON preview until shown, and a masked copy is refused on import', async () => {
    const m = networkMonitors.pm2, MASK = String.fromCodePoint(0x2022).repeat(3);
    assert.ok(!JSON.stringify(f.masked(m)).includes(m.pm2.home));
    await nav(`#/monitors/${m.id}/edit`); await heading('Edit monitor');
    await disclose('JSON (advanced)'); await text('The PM2 home path is masked too');
    const preview = async () => JSON.parse(await page.eval(`document.querySelector('[aria-label="Monitor JSON preview"]').textContent`));
    const masked = await preview();
    assert.equal(masked.pm2.home, MASK); assert.equal(masked.pm2.namespace, m.pm2.namespace); assert.equal(masked.target, m.target);
    await fill('Fill the form from JSON', JSON.stringify({ ...masked, name: 'Review PM2 masked import' })); await click('Fill the form'); await text('pm2.home is a masked placeholder');
    assert.equal(await page.eval(`__field('Name').value`), m.name, 'nothing was filled');
    await click('Show credentials');
    const revealed = await preview(); assert.deepEqual(revealed.pm2, m.pm2);
    await fill('Fill the form from JSON', JSON.stringify({ ...revealed, name: 'Review PM2 imported' })); await click('Fill the form');
    assert.equal(await page.eval(`__field('Name').value`), 'Review PM2 imported'); assert.equal(await page.eval(`__field('PM2 home').value`), m.pm2.home);
    await click('Save changes'); await heading('Review PM2 imported');
    const saved = (await api(A, '/api/monitors')).value.find(v => v.monitor.id === m.id).monitor;
    assert.equal(saved.target, m.target); assert.equal(JSON.stringify(saved.pm2), JSON.stringify(m.pm2));
    assert.equal(saved.checkGeneration || saved.revision, m.checkGeneration || m.revision);
    networkMonitors.pm2 = saved;
    return { maskedPreviewHome: true, maskedImportRefused: true, revealedImportExact: true };
  });
  await step('PM2 conflict projection retains the exact name and options in both display modes', async () => {
    const m = networkMonitors.pm2;
    const ctx = { groupMap: new Map(), role: 'admin', channels: { map: new Map() }, tags: { map: new Map() } };
    let projected;
    for (const reveal of [false, true]) {
      projected = Object.assign({}, ...f.monitorRows(ctx, reveal).map(row => row.project(m)));
      assert.deepEqual(projected.pm2, m.pm2); assert.equal(projected.target, m.target);
    }
    const saved = await api(A, `/api/monitors/${m.id}`, 'PUT', { ...projected, revision: m.revision });
    assert.equal(saved.status, 200, saved.text); assert.equal(JSON.stringify(saved.value.pm2), JSON.stringify(m.pm2));
    assert.equal(saved.value.checkGeneration || saved.value.revision, m.checkGeneration || m.revision);
    networkMonitors.pm2 = saved.value;
    return { serverValidatedProjection: true, bothDisplayModes: true, actualMultiHeadConflict: false };
  });
  // Protocol 19 real browser. The monitor stays paused throughout, so no node connects to a browser service.
  await step('Browser: real-browser fields keep the endpoint and token hidden, refuse a path on an HTTP discovery address before saving, and survive a rename', async () => {
    const m = networkMonitors['real-browser'];
    await nav(`#/monitors/${m.id}/edit`); await heading('Edit monitor');
    await text('Canary doesn’t include or start a browser.'); await text('never someone’s everyday browser or profile');
    assert.equal(await page.eval(`__field('Page URL').value`), m.target);
    for (const label of ['Browser endpoint', 'Bearer token']) assert.equal(await page.eval(`__field(${J(label)}).type`), 'password', label);
    assert.equal(await page.eval(`__field('Browser endpoint').value`), m.browser.endpoint);
    assert.equal(await page.eval(`__field('Bearer token').value`), m.browser.bearerToken);
    assert.equal(await page.eval(`__field('Wait after load').value`), String(m.browser.delayMs));
    const shown = await page.eval('document.body.innerText');
    for (const value of [m.browser.endpoint, 'review-browser-query-secret', m.browser.bearerToken]) assert.ok(!shown.includes(value), 'a private value is page text');
    const puts = () => report.requests.filter(r => r.method === 'PUT' && r.url === A.url + '/api/monitors/' + m.id).length, before = puts();
    await fill('Browser endpoint', 'http://127.0.0.1:9/json/version'); await click('Save changes'); await text('remove the path and the query');
    assert.equal(puts(), before, 'refused before any request');
    await fill('Browser endpoint', m.browser.endpoint); await fill('Name', 'Review real browser renamed'); await click('Save changes');
    await heading('Review real browser renamed');
    const saved = (await api(A, '/api/monitors')).value.find(v => v.monitor.id === m.id).monitor;
    assert.equal(saved.target, m.target); assert.equal(JSON.stringify(saved.browser), JSON.stringify(m.browser));
    assert.equal(saved.checkGeneration || saved.revision, m.checkGeneration || m.revision);
    await text('Browser page loaded'); await text('set · hidden'); await text('Pending is never inverted and never recovers an outage.');
    assert.ok(!(await page.eval('document.body.innerText')).includes('review-browser-query-secret'), 'facts hide the endpoint until revealed');
    networkMonitors['real-browser'] = saved;
    return { optionsPreserved: true, generationPreserved: true, refusedBeforeSave: true, noBrowserContact: saved.enabled === false };
  });
  await step('Browser: the browser endpoint and token are masked in the JSON preview until shown, and a masked copy is refused on import', async () => {
    const m = networkMonitors['real-browser'], MASK = String.fromCodePoint(0x2022).repeat(3);
    assert.ok(!JSON.stringify(f.masked(m)).includes('review-browser-query-secret'));
    await nav(`#/monitors/${m.id}/edit`); await heading('Edit monitor');
    await disclose('JSON (advanced)'); await text('The whole browser endpoint and its bearer token are masked too');
    const preview = async () => JSON.parse(await page.eval(`document.querySelector('[aria-label="Monitor JSON preview"]').textContent`));
    const masked = await preview();
    assert.equal(masked.browser.endpoint, MASK); assert.equal(masked.browser.bearerToken, MASK);
    assert.equal(masked.browser.delayMs, m.browser.delayMs); assert.equal(masked.browser.screenshot, true); assert.equal(masked.target, m.target);
    await fill('Fill the form from JSON', JSON.stringify({ ...masked, name: 'Review real browser masked import' })); await click('Fill the form'); await text('browser.endpoint is a masked placeholder');
    assert.equal(await page.eval(`__field('Name').value`), m.name, 'nothing was filled');
    await click('Show credentials');
    const revealed = await preview(); assert.deepEqual(revealed.browser, m.browser);
    await fill('Fill the form from JSON', JSON.stringify({ ...revealed, name: 'Review real browser imported' })); await click('Fill the form');
    assert.equal(await page.eval(`__field('Name').value`), 'Review real browser imported'); assert.equal(await page.eval(`__field('Browser endpoint').value`), m.browser.endpoint);
    await click('Save changes'); await heading('Review real browser imported');
    const saved = (await api(A, '/api/monitors')).value.find(v => v.monitor.id === m.id).monitor;
    assert.equal(saved.target, m.target); assert.equal(JSON.stringify(saved.browser), JSON.stringify(m.browser));
    assert.equal(saved.checkGeneration || saved.revision, m.checkGeneration || m.revision);
    networkMonitors['real-browser'] = saved;
    return { maskedPreview: true, maskedImportRefused: true, revealedImportExact: true };
  });
  await step('Real-browser conflict projection retains the private options in both display modes', async () => {
    const m = networkMonitors['real-browser'];
    const ctx = { groupMap: new Map(), role: 'admin', channels: { map: new Map() }, tags: { map: new Map() } };
    let projected;
    for (const reveal of [false, true]) {
      projected = Object.assign({}, ...f.monitorRows(ctx, reveal).map(row => row.project(m)));
      assert.equal(JSON.stringify(projected.browser), JSON.stringify(m.browser)); assert.equal(projected.target, m.target);
    }
    const saved = await api(A, `/api/monitors/${m.id}`, 'PUT', { ...projected, revision: m.revision });
    assert.equal(saved.status, 200, saved.text); assert.equal(JSON.stringify(saved.value.browser), JSON.stringify(m.browser));
    assert.equal(saved.value.checkGeneration || saved.value.revision, m.checkGeneration || m.revision);
    networkMonitors['real-browser'] = saved.value;
    return { serverValidatedProjection: true, bothDisplayModes: true, actualMultiHeadConflict: false };
  });
  // Protocol 19 GameDig. The monitor stays paused throughout, so no node contacts a query service or a game server.
  await step('Browser: GameDig fields keep the service address and both tokens hidden, refuse a path before saving, name a port edit as a new check, and survive a rename', async () => {
    const m = networkMonitors.gamedig;
    await nav(`#/monitors/${m.id}/edit`); await heading('Edit monitor');
    await text('Canary doesn’t include GameDig.'); await text('Canary hasn’t tested every game in this list.');
    assert.equal(await page.eval(`__field('Game').value`), m.gamedig.game);
    assert.equal(await page.eval(`__field('Game server address').value`), m.target);
    assert.equal(await page.eval(`__field('Game port').value`), String(m.gamedig.port));
    for (const label of ['Game token', 'Query service address', 'Query service token']) assert.equal(await page.eval(`__field(${J(label)}).type`), 'password', label);
    assert.equal(await page.eval(`__field('Query service address').value`), m.gamedig.endpoint);
    assert.equal(await page.eval(`__field('Query service token').value`), m.gamedig.bearerToken);
    const shown = await page.eval('document.body.innerText');
    for (const value of [m.gamedig.endpoint, m.gamedig.token, m.gamedig.bearerToken]) assert.ok(!shown.includes(value), 'a private value is page text');
    await fill('Game port', String(m.gamedig.port + 1)); await text('Saving starts a new check generation because you changed the game, port, game token or query service');
    await fill('Game port', String(m.gamedig.port));
    const puts = () => report.requests.filter(r => r.method === 'PUT' && r.url === A.url + '/api/monitors/' + m.id).length, before = puts();
    await fill('Query service address', `${m.gamedig.endpoint}/v1/query`); await click('Save changes'); await text('remove the path and the query');
    assert.equal(puts(), before, 'refused before any request');
    await fill('Query service address', m.gamedig.endpoint); await fill('Name', 'Review GameDig renamed'); await click('Save changes');
    await heading('Review GameDig renamed');
    const saved = (await api(A, '/api/monitors')).value.find(v => v.monitor.id === m.id).monitor;
    assert.equal(saved.target, m.target); assert.equal(JSON.stringify(saved.gamedig), JSON.stringify(m.gamedig));
    assert.equal(saved.checkGeneration || saved.revision, m.checkGeneration || m.revision);
    await text('Game server replied'); await text('set · hidden'); await text('Pending is never inverted and never recovers an outage.');
    const facts = await page.eval('document.body.innerText');
    for (const value of [m.gamedig.endpoint, m.gamedig.token, m.gamedig.bearerToken]) assert.ok(!facts.includes(value), 'facts hide the service address and tokens until revealed');
    networkMonitors.gamedig = saved;
    return { optionsPreserved: true, generationPreserved: true, refusedBeforeSave: true, portEditNamedNewCheck: true, noServiceContact: saved.enabled === false };
  });
  await step('Browser: the GameDig service address and both tokens are masked in the JSON preview until shown, and a masked copy is refused on import', async () => {
    const m = networkMonitors.gamedig, MASK = String.fromCodePoint(0x2022).repeat(3);
    const offline = JSON.stringify(f.masked(m));
    for (const value of [m.gamedig.endpoint, m.gamedig.token, m.gamedig.bearerToken]) assert.ok(!offline.includes(value));
    await nav(`#/monitors/${m.id}/edit`); await heading('Edit monitor');
    await disclose('JSON (advanced)'); await text('The whole game query service address, its token and the game token are masked too');
    const preview = async () => JSON.parse(await page.eval(`document.querySelector('[aria-label="Monitor JSON preview"]').textContent`));
    const masked = await preview();
    assert.equal(masked.gamedig.endpoint, MASK); assert.equal(masked.gamedig.bearerToken, MASK); assert.equal(masked.gamedig.token, MASK);
    for (const key of ['game', 'port', 'givenPortOnly']) assert.equal(masked.gamedig[key], m.gamedig[key], key);
    assert.equal(masked.target, m.target);
    await fill('Fill the form from JSON', JSON.stringify({ ...masked, name: 'Review GameDig masked import' })); await click('Fill the form'); await text('gamedig.token is a masked placeholder');
    assert.equal(await page.eval(`__field('Name').value`), m.name, 'nothing was filled');
    await click('Show credentials');
    const revealed = await preview(); assert.deepEqual(revealed.gamedig, m.gamedig);
    await fill('Fill the form from JSON', JSON.stringify({ ...revealed, name: 'Review GameDig imported' })); await click('Fill the form');
    assert.equal(await page.eval(`__field('Name').value`), 'Review GameDig imported'); assert.equal(await page.eval(`__field('Query service address').value`), m.gamedig.endpoint);
    await click('Save changes'); await heading('Review GameDig imported');
    const saved = (await api(A, '/api/monitors')).value.find(v => v.monitor.id === m.id).monitor;
    assert.equal(saved.target, m.target); assert.equal(JSON.stringify(saved.gamedig), JSON.stringify(m.gamedig));
    assert.equal(saved.checkGeneration || saved.revision, m.checkGeneration || m.revision);
    networkMonitors.gamedig = saved;
    return { maskedPreview: true, maskedImportRefused: true, revealedImportExact: true };
  });
  await step('GameDig conflict projection retains the private options in both display modes', async () => {
    const m = networkMonitors.gamedig;
    const ctx = { groupMap: new Map(), role: 'admin', channels: { map: new Map() }, tags: { map: new Map() } };
    let projected;
    for (const reveal of [false, true]) {
      projected = Object.assign({}, ...f.monitorRows(ctx, reveal).map(row => row.project(m)));
      assert.equal(JSON.stringify(projected.gamedig), JSON.stringify(m.gamedig)); assert.equal(projected.target, m.target);
    }
    const saved = await api(A, `/api/monitors/${m.id}`, 'PUT', { ...projected, revision: m.revision });
    assert.equal(saved.status, 200, saved.text); assert.equal(JSON.stringify(saved.value.gamedig), JSON.stringify(m.gamedig));
    assert.equal(saved.value.checkGeneration || saved.value.revision, m.checkGeneration || m.revision);
    networkMonitors.gamedig = saved.value;
    return { serverValidatedProjection: true, bothDisplayModes: true, actualMultiHeadConflict: false };
  });
  await step('Screenshot: a paused monitor has none, the route takes no query, viewers are refused, and only editors’ pages ask', async () => {
    const m = networkMonitors['real-browser'];
    const none = await api(A, `/api/monitors/${m.id}/screenshot`); assert.equal(none.status, 404, none.text);
    const query = await api(A, `/api/monitors/${m.id}/screenshot?full=1`); assert.equal(query.status, 400, query.text);
    const other = await api(A, `/api/monitors/${networkMonitors.http.id}/screenshot`); assert.equal(other.status, 404, other.text);
    const token = secret(); secrets.add(token);
    const made = await api(A, '/api/accounts', 'POST', { name: `review-shot-viewer-${Date.now()}`, role: 'viewer', token }); assert.equal(made.status, 201, made.text);
    const refused = await api(A, `/api/monitors/${m.id}/screenshot`, 'GET', undefined, token); assert.equal(refused.status, 403, refused.text);
    // Every request to the route, CORS preflights included if the browser reports them; the page's own requests are GETs.
    const shots = () => report.requests.filter(r => r.url.startsWith(`${A.url}/api/monitors/${m.id}/screenshot`));
    const start = shots().length;
    await nav(`#/monitors/${m.id}`); await heading(m.name); await text('No screenshot for the current check'); await text('never replicated to other nodes');
    const asked = shots().slice(start).filter(r => r.method !== 'OPTIONS');
    assert.ok(asked.length >= 1, 'the editor page asks for the screenshot');
    assert.ok(asked.every(r => r.method === 'GET' && r.url === `${A.url}/api/monitors/${m.id}/screenshot` && r.authorization), 'plain GETs with the bearer, no query');
    assert.equal(await page.eval('document.querySelectorAll("img.shot-img").length'), 0);
    try {
      await signIn({ ...A, token }, true);
      const viewerStart = shots().length;
      await nav(`#/monitors/${m.id}`); await heading(m.name); await text('Hidden from viewers'); await sleep(400);
      assert.equal(shots().length, viewerStart, 'a viewer’s page never asks for a screenshot');
      const page2 = await page.eval('document.body.innerText');
      assert.ok(!page2.includes('No screenshot for the current check'), 'viewers get no screenshot card');
      for (const value of [m.browser.endpoint, 'review-browser-query-secret', m.browser.bearerToken]) assert.ok(!page2.includes(value), 'a viewer sees a private value');
    } finally { await signIn(A, true); }
    return { noneIs404: true, queryRefused: true, viewerRefused: true, editorAsksPlainGet: asked.length, viewerAsksNothing: true };
  });
  for (const [type, m] of Object.entries(monitors)) await step(`Browser: rename ${type} preserves stored service bytes`, async () => {
    await nav(`#/monitors/${m.id}/edit`); await heading('Edit monitor'); await fill('Name', m.name + ' browser'); await click('Save changes'); await heading(m.name + ' browser');
    const got = await api(A, '/api/monitors'); const saved = got.value.find(v => v.monitor.id === m.id)?.monitor; assert.ok(saved); assert.deepEqual(saved.service, m.service); return { id: m.id, servicePreserved: true };
  });
  for (const [kind, c] of Object.entries(channels)) await step(`Browser: rename ${kind} channel preserves credentials`, async () => {
    await nav(`#/channels/${c.id}/edit`); await heading('Edit'); await fill('Name', c.name + ' browser'); await click('Save changes'); await heading('Notifications');
    const got = await api(A, `/api/notification-channels/${c.id}`); assert.equal(got.status, 200); assert.deepEqual(got.value.config, c.config); return { id: c.id, configPreserved: true };
  });
  if (emptyKafka) await step('Browser regression: empty Kafka payload can be renamed', async () => {
    await nav(`#/monitors/${emptyKafka.id}/edit`); await heading('Edit monitor'); await fill('Name', 'Empty Kafka renamed'); await click('Save changes'); await sleep(300);
    const text = await page.eval('document.body.innerText'), got = await api(A, '/api/monitors'); const m = got.value.find(v => v.monitor.id === emptyKafka.id).monitor;
    assert.equal(m.name, 'Empty Kafka renamed', `UI stayed on edit; error present=${text.includes('Enter message.')}`);
  });
  if (emptyRabbit) await step('Browser regression: empty RabbitMQ password can be renamed', async () => {
    await nav(`#/monitors/${emptyRabbit.id}/edit`); await heading('Edit monitor'); await fill('Name', 'Empty RabbitMQ renamed'); await click('Save changes'); await sleep(300);
    const text = await page.eval('document.body.innerText'), got = await api(A, '/api/monitors'); const m = got.value.find(v => v.monitor.id === emptyRabbit.id).monitor;
    assert.equal(m.name, 'Empty RabbitMQ renamed', `UI stayed on edit; error present=${text.includes('Enter password.')}`);
  });
  for (const [strategy, m] of Object.entries(maintenanceWindows)) await step(`Browser: ${strategy} maintenance rename preserves schedule`, async () => {
    await nav('#/overview'); await heading('Overview'); await nav(`#/maintenance/${m.id}/edit`); await heading('Edit'); await fill('Name', m.name + ' browser'); await click('Save changes'); await heading('Maintenance');
    const got = await api(A, '/api/maintenance'), saved = got.value.find(v => v.maintenance.id === m.id)?.maintenance; assert.ok(saved); assert.deepEqual(saved.schedule,m.schedule); return { id:m.id, schedulePreserved:true };
  });
  for (const [type, m] of Object.entries(networkMonitors)) await step(`Browser: rename ${type} preserves full stored options`, async () => {
    await nav(`#/monitors/${m.id}/edit`); await heading('Edit monitor'); await fill('Name', m.name + ' browser'); await click('Save changes'); await heading(m.name + ' browser');
    const got = await api(A, '/api/monitors'), saved = got.value.find(v => v.monitor.id === m.id)?.monitor; assert.ok(saved);
    for (const key of ['http','tls','dns','websocket','smtp','ping','manual','push','steam','pm2','browser','gamedig','expectedStatusCodes','retries','retryIntervalMs','upsideDown','webhookUrl']) assert.deepEqual(saved[key], m[key], key);
    if (type === 'push') assert.equal(saved.pushStartedAt, m.pushStartedAt);
    networkMonitors[type] = saved; return { id: m.id, optionsPreserved: true };
  });
  await step('Browser: push rotation and GET logging warnings; keep-current retains anchor', async () => {
    const m = networkMonitors.push; await nav(`#/monitors/${m.id}/edit`); await heading('Edit monitor');
    await text('proxies and logs can record it'); await click('Rotate token'); await text('Saving a new token starts a new check');
    const token = await page.eval(`__field('Push token').value`); secrets.add(token); assert.match(token, /^[0-9a-f]{64}$/); assert.notEqual(token, m.push.token);
    await click('Keep the current token'); assert.equal(await page.eval(`__field('Push token').value`), m.push.token); await click('Save changes'); await heading(m.name);
    const got = await api(A, '/api/monitors'), saved = got.value.find(v => v.monitor.id === m.id).monitor; assert.equal(saved.pushStartedAt, m.pushStartedAt); assert.equal(saved.push.token, m.push.token);
    return { generated64Hex: true, undoPreservedTokenAndAnchor: true };
  });
  await step('Browser: real pause retains HTTP auth, assertions, retry, inversion and destinations', async () => {
    const m = networkMonitors.http; const started = await api(A, `/api/monitors/${m.id}`, 'PUT', { ...f.inputOf(m), enabled: true, revision: m.revision }); assert.equal(started.status, 200, started.text);
    await nav('#/overview'); await heading('Overview'); await sleep(5200); await nav(`#/monitors/${m.id}`); await heading(m.name); await click('Pause');
    const saved = await until(async () => { const r = await api(A, '/api/monitors'); const v = r.value.find(v => v.monitor.id === m.id)?.monitor; return v?.enabled === false && v; }, 'pause acknowledged');
    for (const key of ['http','tls','dns','websocket','smtp','ping','manual','push','expectedStatusCodes','retries','retryIntervalMs','upsideDown','webhookUrl']) assert.deepEqual(saved[key], m[key], key);
    return { optionsPreserved: true };
  });
  await step('Browser: management secrets are dropped on endpoint switch', async () => {
    await nav(`#/monitors/${monitors.redis.id}/edit`); await heading('Edit monitor'); await fill('Name', 'A confidential draft');
    const before = await page.eval(`__field('Password').value`); assert.equal(before, 'review-password');
    await signIn(B, true); await text('Monitor not found'); const values = await page.eval(`[...document.querySelectorAll('input,textarea')].map(e => e.value)`); assert.ok(!values.includes(before)); assert.ok(!values.includes('A confidential draft'));
    await signIn(A, true); return { credentialDraftRemounted: true };
  });
  await step('Browser: late A create completion cannot navigate or notify on B', async () => {
    await nav('#/monitors/new'); await heading('New monitor'); await fill('Name', 'Delayed A write'); await fill('URL', state.sink + '/ok');
    let held;
    requestHook = p => { if (p.responseStatusCode && p.request.url === A.url + '/api/monitors' && p.request.method === 'POST') { held = p; return true; } return false; };
    try {
      await page.send('Fetch.enable', { patterns: [{ urlPattern: '*', requestStage: 'Request' }, { urlPattern: A.url + '/api/monitors', requestStage: 'Response' }] });
      await click('Create monitor'); await until(() => held, 'held real create response'); await signIn(B, true); await heading('New monitor'); await fill('Name', 'B draft stays');
      await page.send('Fetch.continueResponse', { requestId: held.requestId }); held = null; await sleep(500);
      assert.equal(await page.eval(`__field('Name').value`), 'B draft stays'); assert.equal(await page.eval('location.hash'), '#/monitors/new'); assert.ok(!(await page.eval(`document.querySelector('.toasts')?.innerText || ''`)).includes('Delayed A write'));
      const b = await api(B, '/api/monitors'); assert.ok(!b.value.some(v => v.monitor.name === 'Delayed A write'));
      const a = await api(A, '/api/monitors'); assert.ok(a.value.some(v => v.monitor.name === 'Delayed A write')); return { writeOnAOnly: true, staleResultSuppressed: true };
    } finally { requestHook = undefined; if (held) await page.send('Fetch.continueResponse', { requestId: held.requestId }); await page.send('Fetch.enable', { patterns: [{ urlPattern: '*', requestStage: 'Request' }] }); await signIn(A, true); }
  });
  await step('Auth regression: account password drafts clear across endpoint switches', async () => {
    await nav('#/accounts'); await heading('Accounts'); await fill('Name', 'A account draft'); await fill('Initial password', 'Review draft password 2026!'); await fill('Confirm password', 'Review draft password 2026!');
    await signIn(B, true); await heading('Accounts'); assert.equal(await page.eval(`__field('Name').value`), ''); assert.equal(await page.eval(`__field('Initial password').value`), ''); assert.equal(await page.eval(`__field('Confirm password').value`), ''); await signIn(A, true);
    return { priorAuthUI03Prevented:true };
  });
  await step('Auth regression: one-time issued token cannot be relabeled as another node', async () => {
    await nav('#/accounts'); await heading('Accounts'); await fill('Name', `review-issued-${Date.now()}`); await choose('Password'); await choose('API token'); await click('Create account');
    const issued = await until(() => page.eval(`document.querySelector('.issued input')?.value`), 'issued token'); secrets.add(issued);
    assert.equal((await api(A, '/api/overview', 'GET', undefined, issued)).status, 200); assert.equal((await api(B, '/api/overview', 'GET', undefined, issued)).status, 401);
    await signIn(B, true); await heading('Accounts'); assert.equal(await page.eval(`document.querySelector('.issued input')?.value || ''`), ''); await signIn(A, true);
    return { oldNodeOnly:true, priorAuthUI02Prevented:true };
  });
  await step('Auth regression: A MFA enrollment and password cannot survive switching to B', async () => {
    const passwordA = 'Review A private password 2026!', passwordB = 'Review B private password 2026!'; secrets.add(passwordA); secrets.add(passwordB);
    for (const [node, password] of [[A,passwordA],[B,passwordB]]) { const r = await api(node, '/api/auth/password', 'POST', { password }); assert.equal(r.status,200,r.text); }
    await signIn(A,true); await nav('#/account'); await heading('Your account');
    await page.eval(`document.querySelectorAll('.card').forEach(e => { if(e.querySelector('h2')?.innerText === 'Two-factor authentication') e.setAttribute('data-review-mfa','true'); })`);
    await fill('Current password',passwordA,'[data-review-mfa]'); await click('Set up authenticator','[data-review-mfa]');
    const seed = await until(() => page.eval(`document.querySelector('.keybox [aria-label]')?.getAttribute('aria-label')?.slice(10).replaceAll(' ','')`), 'MFA seed'); secrets.add(seed);
    await signIn(B,true); await heading('Your account'); assert.ok(!(await page.eval('document.body.innerText')).includes(seed)); assert.equal(await page.eval(`document.querySelectorAll('.keybox').length`),0);
    assert.ok(!(await page.eval(`[...document.querySelectorAll('input')].map(e=>e.value)`)).includes(passwordA));
    const before = report.requests.length; assert.equal(await page.eval(`[...document.querySelectorAll('button')].some(e=>e.textContent.includes('Turn on two-factor'))`),false);
    assert.ok(!report.requests.slice(before).some(r=>r.url === B.url + '/api/auth/totp/confirm')); await signIn(A,true);
    return { pendingSetupRemoved:true, priorAuthUI01Prevented:true };
  });
  let manualUp, manualDown;
  await step('Browser: unmeasured manual latency stays unavailable; sample ratio is qualified', async () => {
    const made = await api(A, '/api/monitors', 'POST', base('manual', { name: 'Review manual no latency', intervalMs: 250, timeoutMs: 100, enabled: true, manual: { state: 'up', message: 'private manual diagnostics' } })); assert.equal(made.status, 201, made.text); manualUp = made.value;
    await until(async () => (await api(A, '/api/overview')).value.monitors.find(v => v.monitor.id === manualUp.id)?.observations.some(v => v.state === 'up' && v.latencyAvailable === false), 'manual sample');
    await nav('#/overview'); await heading('Overview'); await sleep(5200); await nav(`#/monitors/${manualUp.id}`); await heading(manualUp.name); await text('Samples in range'); await text('not a time-weighted uptime or SLA'); await text('No measured latency in range');
    const latency = await page.eval(`document.querySelector('td[data-label="Latency"]')?.innerText`); assert.equal(latency.trim(), '—');
    return { latencyText: latency, sampleRatioQualified: true };
  });
  await step('Browser: administrative closure is distinct from recovery', async () => {
    const made = await api(A, '/api/monitors', 'POST', base('manual', { name: 'Review administrative close', intervalMs: 250, timeoutMs: 100, enabled: true, manual: { state: 'down', message: 'operator selected down' } })); assert.equal(made.status, 201, made.text); manualDown = made.value;
    await until(async () => (await api(A, '/api/overview')).value.incidents.some(i => i.monitorId === manualDown.id && i.resolvedAt === null), 'manual incident');
    await nav('#/overview'); await heading('Overview'); await sleep(5200); await nav(`#/monitors/${manualDown.id}/edit`); await heading('Edit monitor'); await fill('Timeout', '200'); await text('Saving closes the open incident without a recovery alert.');
    await nav(`#/monitors/${manualDown.id}`); await heading(manualDown.name); await click('Pause'); await text('Closed after config change');
    const closed = (await api(A, '/api/overview')).value.incidents.find(i => i.monitorId === manualDown.id); assert.ok(closed.retiredBy); assert.ok(closed.resolvedAt); return { retiredByPresent: true, recoveryNotInvented: true };
  });
  const slug = `review-${Date.now().toString(36)}`;
  await step('Public API: private names, diagnostics, options, IDs and nonpublic tags are absent', async () => {
    const publicTag = await api(A, '/api/tags', 'POST', { name: 'public-tag', color: '#123456' }), privateTag = await api(A, '/api/tags', 'POST', { name: 'private-tag-secret', color: '#abcdef' }); assert.equal(publicTag.status, 201, publicTag.text); assert.equal(privateTag.status, 201, privateTag.text);
    const tagUpdate = await api(A, `/api/monitors/${manualUp.id}`, 'PUT', { ...f.inputOf(manualUp), tags: [{ tagId: publicTag.value.id, value: 'public-value', public: true }, { tagId: privateTag.value.id, value: 'private-value' }], revision: manualUp.revision }); assert.equal(tagUpdate.status, 200, tagUpdate.text); manualUp = tagUpdate.value;
    const made = await api(A, '/api/status-pages', 'POST', { slug, title: 'Review public A', description: '<img src=x onerror="globalThis.__reviewXss=1">', enabled: true, groups: [{ id: 'public-section', name: 'Public services', monitors: [{ monitorId: manualUp.id, name: 'Published service', showCertificate: true, showTags: true }] }], announcements: [] }); assert.equal(made.status, 201, made.text);
    const pub = await api(A, `/public/status/${slug}`, 'GET', undefined, null); assert.equal(pub.status, 200, pub.text);
    for (const v of [A.id, manualUp.id, manualUp.name, 'private manual diagnostics', 'private-tag-secret', 'private-value', 'review-password', 'review-hook-key']) assert.ok(!pub.text.includes(v), `private value appeared: ${v}`);
    assert.ok(pub.text.includes('public-tag')); assert.ok(pub.text.includes('public-value')); return { publicKeys: Object.keys(pub.value), tagsSelectedExplicitly: true };
  });
  await step('Browser: public fetch omits credentials while signed in; publication text cannot execute', async () => {
    const start = report.requests.length; await nav(`#/status/${slug}?node=${encodeURIComponent(A.url)}`); await heading('Review public A'); await text('Published service');
    const requests = report.requests.slice(start).filter(r => r.url.includes('/public/status/')); assert.ok(requests.length); assert.ok(requests.every(r => !r.authorization && !r.cookie));
    assert.equal(await page.eval('globalThis.__reviewXss || 0'), 0); assert.equal(await page.eval('document.querySelector(".pub img")'), null);
    await text('not historical uptime or an SLA'); return { publicRequests: requests.length, noCredentialOrCookie: true, textOnly: true };
  });
  await step('Browser: unsafe public node URLs trigger no network requests', async () => {
    for (const unsafe of ['javascript:alert(1)', `http://user:password@127.0.0.1:9`, `${A.url}/?token=review-secret`, `${A.url}/api`]) {
      const start = report.requests.length; await nav(`#/status/${slug}?node=${encodeURIComponent(unsafe)}`); await text('Status unavailable');
      assert.equal(report.requests.slice(start).filter(r => r.url.includes('/public/status/')).length, 0);
    }
    await nav('#/overview'); await heading('Overview'); return { unsafeUrlsRejected: 4 };
  });
  const monitorList = async () => (await api(A, '/api/monitors')).value.map(v => v.monitor);
  const creates = () => report.requests.filter(r => r.method === 'POST' && r.url === A.url + '/api/monitors').length;
  await step('Browser: Clone opens a filled new-monitor form, creates nothing until Create, then a copy with its own ID', async () => {
    const m = (await monitorList()).find(v => v.id === networkMonitors.http.id);
    await nav(`#/monitors/${m.id}`); await heading(m.name);
    const before = creates(), known = new Set((await monitorList()).map(v => v.id));
    await click('Clone'); await heading('Copy monitor'); await text('Private connection settings are copied too.');
    assert.equal(await page.eval('location.hash'), `#/monitors/${encodeURIComponent(m.id)}/clone`, 'only the source ID is in the address');
    assert.equal(await page.eval(`__field('Name').value`), `${m.name} (copy)`);
    assert.equal(await page.eval(`__field('URL').value`), m.target);
    await sleep(300); assert.equal(creates(), before, 'nothing is created before Create monitor');
    await click('Create monitor'); await heading(`${m.name} (copy)`);
    assert.equal(creates(), before + 1);
    const list = await monitorList(), copy = list.find(v => !known.has(v.id));
    assert.ok(copy, 'one new monitor'); assert.equal(copy.name, `${m.name} (copy)`);
    for (const key of ['type', 'target', 'intervalMs', 'timeoutMs', 'nodeIds', 'enabled', 'http', 'tls', 'expectedStatusCodes', 'retries', 'retryIntervalMs', 'upsideDown', 'webhookUrl', 'notificationIds', 'tags']) assert.deepEqual(copy[key], m[key], key);
    assert.deepEqual(list.find(v => v.id === m.id), m, 'the source is unchanged');
    return { newId: copy.id !== m.id, settingsCopied: true, sourceUnchanged: true, noCreateBeforeSave: true };
  });
  await step('Browser: a push copy gets a new hidden token in the form and on the node; the source keeps its own', async () => {
    const m = (await monitorList()).find(v => v.id === networkMonitors.push.id);
    const known = new Set((await monitorList()).map(v => v.id));
    await nav(`#/monitors/${m.id}/clone`); await heading('Copy monitor'); await text('This copy has its own new push token');
    const token = await page.eval(`__field('Push token').value`); secrets.add(token);
    assert.match(token, /^[0-9a-f]{64}$/u); assert.notEqual(token, m.push.token);
    assert.equal(await page.eval(`__field('Push token').type`), 'password');
    assert.ok(!(await page.eval(`document.body.innerText + [...document.querySelectorAll('input,textarea')].map(e => e.value).join(' ')`)).includes(m.push.token), 'the source token is nowhere in the page');
    await click('Create monitor'); await heading(`${m.name} (copy)`);
    const list = await monitorList(), copy = list.find(v => !known.has(v.id));
    assert.ok(copy); assert.equal(copy.push.token, token); assert.equal(copy.push.allowGet, m.push.allowGet);
    assert.deepEqual(list.find(v => v.id === m.id), m, 'the source keeps its token and generation');
    return { newTokenStored: true, sourceUnchanged: true };
  });
  await step('Browser: a real-browser copy keeps the endpoint and token hidden in its form and creates a paused copy with the same options', async () => {
    const m = (await monitorList()).find(v => v.id === networkMonitors['real-browser'].id);
    const known = new Set((await monitorList()).map(v => v.id));
    await nav(`#/monitors/${m.id}/clone`); await heading('Copy monitor'); await text('browser service addresses come along');
    assert.equal(await page.eval(`__field('Browser endpoint').type`), 'password');
    assert.equal(await page.eval(`__field('Browser endpoint').value`), m.browser.endpoint);
    assert.ok(!(await page.eval('document.body.innerText')).includes('review-browser-query-secret'), 'the copied endpoint is never page text');
    await click('Create monitor'); await heading(`${m.name} (copy)`);
    const list = await monitorList(), copy = list.find(v => !known.has(v.id));
    assert.ok(copy); assert.equal(copy.enabled, false); assert.equal(copy.target, m.target);
    assert.equal(JSON.stringify(copy.browser), JSON.stringify(m.browser));
    assert.deepEqual(list.find(v => v.id === m.id), m, 'the source is unchanged');
    return { optionsCopied: true, paused: true, sourceUnchanged: true };
  });
  await step('Browser: a GameDig copy keeps the service address and both tokens hidden in its form and creates a paused copy with the same options', async () => {
    const m = (await monitorList()).find(v => v.id === networkMonitors.gamedig.id);
    const known = new Set((await monitorList()).map(v => v.id));
    await nav(`#/monitors/${m.id}/clone`); await heading('Copy monitor'); await text('game query service addresses');
    for (const label of ['Game token', 'Query service address', 'Query service token']) assert.equal(await page.eval(`__field(${J(label)}).type`), 'password', label);
    assert.equal(await page.eval(`__field('Query service address').value`), m.gamedig.endpoint);
    assert.equal(await page.eval(`__field('Game').value`), m.gamedig.game);
    const shown = await page.eval('document.body.innerText');
    for (const value of [m.gamedig.endpoint, m.gamedig.token, m.gamedig.bearerToken]) assert.ok(!shown.includes(value), 'a copied private value is never page text');
    await click('Create monitor'); await heading(`${m.name} (copy)`);
    const list = await monitorList(), copy = list.find(v => !known.has(v.id));
    assert.ok(copy); assert.equal(copy.enabled, false); assert.equal(copy.target, m.target);
    assert.equal(JSON.stringify(copy.gamedig), JSON.stringify(m.gamedig));
    assert.deepEqual(list.find(v => v.id === m.id), m, 'the source is unchanged');
    return { optionsCopied: true, paused: true, sourceUnchanged: true };
  });
  // r5 (protocol 19) browser checks on the paused monitors the API + form (r5) step left behind.
  await step('Browser (r5): SOA needs a resolver before saving; WebSocket offers and OAuth2 and TCP TLS show in their forms; a description is plain text', async () => {
    const dns = networkMonitors.dns, ws = networkMonitors.websocket, tcp = networkMonitors.tcp;
    await nav(`#/monitors/${dns.id}/edit`); await heading('Edit monitor');
    await fill('Record type', 'SOA'); await fill('Resolver', ''); await click('Save changes'); await text('SOA checks ask one resolver directly');
    assert.equal((await monitorList()).find(v => v.id === dns.id).revision, dns.revision, 'nothing was saved');
    await nav(`#/monitors/${ws.id}/edit`); await heading('Edit monitor');
    assert.equal(await page.eval(`__field('Subprotocols').value`), 'chat, v2');
    await disclose('Headers, authentication and proxy'); await text('OAuth2 client credentials');
    assert.equal(await page.eval(`__field('Client secret').type`), 'password');
    assert.ok(!(await page.eval('document.body.innerText')).includes('review-ws-oauth-secret'), 'the client secret is never page text');
    await fill('Name', `${ws.name} r5`); await click('Save changes'); await heading(`${ws.name} r5`);
    const savedWs = (await monitorList()).find(v => v.id === ws.id);
    assert.deepEqual(savedWs.websocket, ws.websocket, 'a rename keeps the grant and the offers'); networkMonitors.websocket = savedWs;
    await text('the server must choose one of them');
    await nav(`#/monitors/${tcp.id}`); await heading(tcp.name); await text('TLS 1.2+ handshake');
    assert.equal(await page.eval(`document.querySelector('.mon-desc p')?.textContent`), tcp.description, 'the description, as written, with its line break');
    await nav(`#/monitors/${tcp.id}/edit`); await heading('Edit monitor');
    assert.equal(await page.eval(`[...document.querySelectorAll('label.check')].find(l => l.innerText.startsWith('Use TLS after connecting'))?.querySelector('input').checked`), true);
    assert.equal(await page.eval(`__field('Description').value`), tcp.description);
    return { soaNeedsResolver: true, wsOptionsKept: true, tcpTlsShown: true, descriptionPlain: true };
  });
  await step('Default channels (r5): an omitted list gets the enabled default; the new-monitor form preselects it and a clear opts out; Add to monitors adds it once at the shown revisions', async () => {
    const made = await api(A, '/api/notification-channels', 'POST', { name: 'Review default channel', enabled: true, isDefault: true, config: { kind: 'webhook', webhook: { url: state.sink + '/hook-default', allowInsecureHTTP: true } } });
    assert.equal(made.status, 201, made.text); const channel = made.value; assert.equal(channel.isDefault, true);
    let detail;
    try {
      const implicit = await api(A, '/api/monitors', 'POST', base('manual', { name: 'Review default implicit' }));
      assert.equal(implicit.status, 201, implicit.text); assert.deepEqual(implicit.value.notificationIds, [channel.id], 'the node picked its enabled default');
      await nav('#/monitors/new'); await heading('New monitor');
      const box = `[...document.querySelectorAll('.chanpick label')].find(l => l.innerText.includes('Review default channel'))?.querySelector('input')`;
      // The channel list refreshes every 20 s, so the new channel can take that long to appear.
      await until(() => page.eval(`${box}?.checked === true`), 'the default channel is preselected', 30000);
      await text('Preselected: this node’s enabled default channels');
      await fill('Name', 'Review default cleared'); await choose('Manual'); await choose('Active'); await choose('Review default channel');
      assert.equal(await page.eval(`${box}.checked`), false);
      await click('Create monitor'); await heading('Review default cleared');
      const cleared = (await monitorList()).find(v => v.name === 'Review default cleared');
      assert.ok(cleared); assert.equal(cleared.enabled, false); assert.equal(cleared.notificationIds, undefined, 'an explicit clear is respected: no default added');
      const target = (await monitorList()).find(v => v.id === networkMonitors.ping.id);
      await nav('#/notifications'); await heading('Notifications'); await click(`Add ${channel.name} to monitors`);
      await text(`Add ${channel.name} to monitors`); await sleep(150);
      assert.equal(await page.eval(`document.querySelectorAll('dialog[open] .apply-list input:checked:not(:disabled)').length`), 0, 'nothing is preselected');
      await fill('Find monitors', target.name, 'dialog[open]'); await choose(target.name, 'dialog[open]');
      await click('Add to 1 monitor', 'dialog[open]'); await text(`Added “${channel.name}” to 1 monitor`);
      const after = (await monitorList()).find(v => v.id === target.id);
      assert.deepEqual(after.notificationIds, [...(target.notificationIds ?? []), channel.id].sort());
      assert.equal(after.checkGeneration || after.revision, target.checkGeneration || target.revision, 'adding a destination keeps the check generation');
      const again = await api(A, `/api/notification-channels/${channel.id}/apply`, 'POST', { revision: channel.revision, monitors: [{ id: after.id, revision: after.revision }] });
      assert.equal(again.status, 200, again.text); assert.deepEqual(again.value.changedIds, [], 'already there: zero changes');
      const stale = await api(A, `/api/notification-channels/${channel.id}/apply`, 'POST', { revision: channel.revision, monitors: [{ id: cleared.id, revision: cleared.revision }, { id: after.id, revision: target.revision }] });
      assert.equal(stale.status, 409, stale.text);
      assert.equal((await monitorList()).find(v => v.id === cleared.id).notificationIds, undefined, 'a stale member refuses the whole batch');
      networkMonitors.ping = after;
      detail = { implicitDefault: true, explicitClear: true, appliedOnce: true, zeroChanges: true, staleBatchRefused: true };
    } finally {
      // Default off and disabled again, so later creates and monitors are unaffected; the unsaved test below still works.
      const current = (await api(A, `/api/notification-channels/${channel.id}`)).value;
      const off = await api(A, `/api/notification-channels/${channel.id}`, 'PUT', { name: current.name, enabled: false, config: current.config, revision: current.revision });
      assert.equal(off.status, 200, off.text); assert.equal(off.value.isDefault, undefined);
    }
    return detail;
  });
  await step('Unsaved channel test (r5): Test these settings sends one loopback message with the form’s unsaved, disabled settings and saves nothing', async () => {
    const channel = (await api(A, '/api/notification-channels')).value.find(v => v.channel.name === 'Review default channel').channel;
    assert.equal(channel.enabled, false, 'the step above left the channel disabled');
    // That step disabled the channel through the API after the dashboard last loaded its channel list (Add to monitors
    // reloads it), and the list refreshes every 20 s. The edit form starts from the list, so it could open the enabled
    // revision: the 2026-10-25 run then waited for the disabled sentence in vain. Open it once the list shows the stored
    // revision, disabled, and check that the form shows exactly that before testing.
    await nav('#/notifications'); await heading('Notifications');
    const row = `[...document.querySelectorAll('.chan-list li')].find(li => li.querySelector('.t b')?.textContent === ${J(channel.name)})`;
    await until(() => page.eval(`(r => Boolean(r) && r.querySelector('.rev')?.title === ${J(`Revision ${channel.revision}`)} && [...r.querySelectorAll('.tag')].some(t => t.textContent === 'disabled'))(${row})`), 'the channel list shows the stored, disabled revision', 30000);
    await nav(`#/channels/${channel.id}/edit`); await heading('Edit');
    assert.equal(await page.eval(`document.querySelector('main .vhead .sub .rev')?.title`), `Revision ${channel.revision}`, 'the form edits the stored revision');
    const enabledBox = `(() => { const all = [...document.querySelectorAll('main label.check')].filter(l => l.querySelector('span')?.firstChild?.textContent === 'Enabled'); if (all.length !== 1) throw Error('Enabled matched ' + all.length); return all[0].querySelector('input'); })()`;
    assert.equal(await page.eval(`${enabledBox}.checked`), false, 'the form starts from the stored channel: disabled');
    assert.ok(!(await page.eval('document.body.innerText')).includes('while you were editing'), 'no newer revision is waiting');
    const start = report.requests.length;
    await fill('Webhook URL', state.sink + '/hook-preview-unsaved'); await click('Test these settings');
    await text('Test these settings?'); await text('The channel stays disabled.'); await click('Send test', 'dialog[open]');
    await text('Nothing was saved.');
    await until(() => existsSync(join(runDir, 'sink.json')) && json(join(runDir, 'sink.json')).some(hit => hit.path === '/hook-preview-unsaved'), 'the unsaved test reached the loopback sink');
    const after = (await api(A, `/api/notification-channels/${channel.id}`)).value;
    assert.equal(after.revision, channel.revision); assert.equal(after.config.webhook.url, state.sink + '/hook-default'); assert.equal(after.enabled, false);
    const posts = report.requests.slice(start).filter(r => r.method === 'POST' && r.url.includes('/api/notification-channels'));
    assert.deepEqual(posts.map(r => new URL(r.url).pathname), ['/api/notification-channels/preview-test'], 'one preview request, no save');
    const storage = await page.eval(`JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage })`);
    assert.ok(!storage.includes('hook-preview-unsaved'), 'the unsaved settings never reach browser storage');
    await nav('#/notifications'); await heading('Notifications');
    return { previewSent: true, savedNothing: true };
  });
  await step('Alert templates: presets, Undo and explicit replacement stay local; Discord cards show each sample event without sending', async () => {
    await nav('#/channels/new'); await heading('New channel');
    await page.eval('document.querySelector("input[type=radio][value=discord]").click()');
    await disclose('Message templates'); await text('Sample data');
    const start = report.requests.length;
    const fields = () => page.eval('[__field("Title").value,__field("Text").value]');
    const preset = name => page.eval(`document.querySelector('button[aria-label="Use the ${name} preset"]').click()`);
    const initial = await fields();
    await preset('Compact'); await text('Compact preset applied');
    const compact = await fields(); assert.ok(compact[0].includes('{{status}}'));
    await click('Undo'); assert.deepEqual(await fields(), initial);
    await fill('Title', 'My custom {{monitor_name}}'); await fill('Text', 'Keep this text');
    await preset('Detailed'); await text('Replace your own');
    assert.equal(await page.eval('document.activeElement?.textContent'), 'Replace');
    await click('Keep mine'); assert.deepEqual(await fields(), ['My custom {{monitor_name}}', 'Keep this text']);
    await preset('Detailed'); await click('Replace');
    const detailed = await fields(); assert.ok(detailed[1].includes('{{downtime}}'));
    await choose('Card'); await until(() => page.eval('!!document.querySelector(".tpl-dc-card")'), 'Discord sample card');
    for (const [label, event] of [['Down','down'], ['Still down','repeat'], ['Recovery','recovery'], ['Test','test']]) {
      await choose(label, '.tpl-pv');
      assert.ok(await page.eval(`!!document.querySelector('.tpl-dc-card.ev-${event}')`), label);
    }
    for (const mode of ['light', 'dark']) {
      await page.eval(`document.documentElement.dataset.mode=${J(mode)}`);
      for (const width of [390,728,1280]) {
        await page.send('Emulation.setDeviceMetricsOverride', { width, height:900, deviceScaleFactor:1, mobile:false });
        await page.eval('new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)))');
        assert.ok(await page.eval('document.documentElement.scrollWidth<=innerWidth'), `${mode} ${width}: template overflow`);
      }
    }
    await page.send('Emulation.clearDeviceMetricsOverride');
    const changed = report.requests.slice(start).filter(r => r.method !== 'GET');
    assert.deepEqual(changed, [], 'preview changed server state or sent a test');
    assert.ok(report.requests.slice(start).every(r => [state.origin,A.url,B.url,state.sink].includes(new URL(r.url).origin)), 'preview contacted an external provider');
    await nav('#/notifications'); await heading('Notifications');
    return { presets:true, undo:true, replacementExplicit:true, localPreview:true, events:4, layouts:6 };
  });
  await step('Status page presentation (r5): the form and node agree on appearance, address and feed; Markdown and the footer stay inert; the feed has announcements only', async () => {
    const pageOf = async () => (await api(A, '/api/status-pages')).value.find(v => v.statusPage.slug === slug).statusPage;
    let current = await pageOf();
    const publicUrl = `${state.origin}/#/status/${slug}?node=${encodeURIComponent(A.url)}`;
    const nul = String.fromCharCode(0);
    const cases = [
      [{ appearance: { theme: 'auto' } }, 'theme'], [{ appearance: { accentColor: 'red;}body{display:none' } }, 'accent'], [{ appearance: { logoUrl: 'http://127.0.0.1:9/logo.png' } }, 'http logo'],
      [{ appearance: { logoUrl: 'https://images.canary-review.test/logo.png?track=1' } }, 'logo query'], [{ appearance: { refreshIntervalMs: 9999 } }, 'refresh'], [{ appearance: { footerText: `line${nul}break` } }, 'footer control'],
      [{ rssEnabled: true }, 'feed without an address'], [{ publicUrl: 'javascript:alert(1)' }, 'unsafe address'], [{ publicUrl: 'https://user:pw@status.canary-review.test/' }, 'address with credentials'],
      [{ appearance: { theme: 'dark', accentColor: '#aAbB09', refreshIntervalMs: 10000, markdown: true, footerText: 'Footer with [docs](https://docs.canary-review.test/) and [bad](javascript:alert(1)) and <script>globalThis.__reviewXss=2</script>' }, publicUrl, rssEnabled: true }, 'presentation'],
    ];
    for (const [patch, label] of cases) {
      const errors = {}, candidate = { ...current, ...patch };
      f.buildPresentation(undefined, f.presentationDraft(candidate), errors);
      const formAccepts = !Object.keys(errors).length;
      const { id, revision, updatedAt, deleted, ...input } = candidate;
      const saved = await api(A, `/api/status-pages/${current.id}`, 'PUT', { ...input, revision: current.revision });
      assert.ok(saved.status === 200 || saved.status === 400, `${label}: ${saved.status} ${saved.text}`);
      assert.equal(saved.status === 200, formAccepts, `${label}: form ${formAccepts ? 'accepts' : `refuses (${Object.values(errors).join(' ')})`}, node answered ${saved.status}`);
      if (saved.status === 200) current = saved.value;
    }
    const now = Date.now();
    const noted = await api(A, `/api/status-pages/${current.id}`, 'PUT', { ...(({ id, revision, updatedAt, deleted, ...rest }) => rest)(current), revision: current.revision,
      announcements: [{ id: 'review-note', title: 'Review announcement', message: '**Planned** work <img src=x onerror="globalThis.__reviewXss=3">', state: 'monitoring', pinned: true, createdAt: now - 1000, updatedAt: now }] });
    assert.equal(noted.status, 200, noted.text); current = noted.value;
    const feed = await fetch(`${A.url}/public/status/${slug}/rss.xml`, { redirect: 'error', signal: AbortSignal.timeout(15000) });
    const xml = await feed.text();
    assert.equal(feed.status, 200, xml); assert.equal(feed.headers.get('content-type'), 'application/rss+xml; charset=utf-8');
    // The node HTML-escapes the message and XML-encodes the result, so the raw feed holds &amp;lt;img, never a tag.
    assert.ok(xml.includes(`canary-status:${slug}:review-note`) && /&(?:amp;)?lt;img/u.test(xml) && !xml.includes('<img'), 'announcements only, markup escaped');
    for (const value of [manualUp.id, manualUp.name, 'private manual diagnostics', A.token, current.id, current.revision]) assert.ok(!xml.includes(value), 'the feed carries no private value');
    assert.equal((await fetch(`${A.url}/public/status/${slug}/rss.xml?x=1`, { redirect: 'error' })).status, 404, 'the feed takes no query');
    const start = report.requests.length;
    await nav(`#/status/${slug}?node=${encodeURIComponent(A.url)}`); await heading('Review public A'); await text('Review announcement');
    assert.equal(await page.eval('globalThis.__reviewXss || 0'), 0); assert.equal(await page.eval('document.querySelector(".pub img")'), null);
    assert.equal(await page.eval(`document.querySelector('.pub').style.getPropertyValue('--page-accent')`), '#aabb09');
    if (await page.eval(`localStorage.getItem('uptime-canary.theme')`) === null) assert.equal(await page.eval('document.documentElement.dataset.mode'), 'dark', 'the page’s theme replaces the system setting');
    const links = await page.eval(`[...document.querySelectorAll('.pub-foot a')].map(a => ({ href: a.getAttribute('href'), rel: a.rel, target: a.target, type: a.type }))`);
    assert.deepEqual(links.filter(l => l.href.startsWith('https://docs.canary-review.test')), [{ href: 'https://docs.canary-review.test/', rel: 'noopener noreferrer nofollow', target: '_blank', type: '' }]);
    assert.equal(links.some(l => /^javascript:/iu.test(l.href)), false);
    assert.ok(links.some(l => l.href === `${A.url}/public/status/${slug}/rss.xml` && l.type === 'application/rss+xml'), 'the feed link');
    const foot = await page.eval(`document.querySelector('.pub-foot').innerText`);
    assert.ok(foot.includes('<script>globalThis.__reviewXss=2</script>') && foot.includes('[bad](javascript:alert(1))'), 'unsafe text stays text');
    assert.equal(await page.eval(`document.querySelector('.pub-note strong')?.textContent`), 'Planned');
    await text('refreshes every 10 s');
    assert.equal(report.requests.slice(start).filter(r => r.url.includes('rss.xml') || r.url.includes('canary-review.test')).length, 0, 'the page fetches neither the feed nor linked hosts');
    await nav(`#/status-pages/${current.id}/edit`); await heading('Edit');
    assert.equal(await page.eval(`__field('Accent color').value`), '#aAbB09'); assert.equal(await page.eval(`__field('Public page address').value`), publicUrl);
    await text(`${A.url}/public/status/${slug}/rss.xml`);
    await fill('Logo image URL', 'http://127.0.0.1:9/logo.png'); await click('Save page'); await text('Use an https:// image address');
    assert.equal((await pageOf()).revision, current.revision, 'nothing was saved');
    assert.equal(report.requests.slice(start).filter(r => r.url.includes('127.0.0.1:9/')).length, 0, 'no logo was requested');
    await nav('#/status-pages'); await heading('Status pages');
    return { cases: cases.length, feedChecked: true, inert: true };
  });
  await step('Viewer: API and UI hide monitor/channel/push credentials and disable editing', async () => {
    const token = secret(); secrets.add(token); const made = await api(A, '/api/accounts', 'POST', { name: `review-viewer-${Date.now()}`, role: 'viewer', token }); assert.equal(made.status, 201, made.text);
    const raw = await api(A, '/api/overview', 'GET', undefined, token); for (const value of ['review-password', 'review-hook-key', networkMonitors.push.push.token, networkMonitors.steam.steam.apiKey, 'private-body', 'private-header']) assert.ok(!raw.text.includes(value));
    // PM2 options are omitted for viewers; the admission step above stored other monitors with the same home.
    for (const value of [networkMonitors.pm2.pm2.home, networkMonitors.pm2.pm2.namespace, 'canary-review/.pm2']) assert.ok(!raw.text.includes(value), 'viewer received PM2 options');
    // Protocol 19: viewers receive no browser options at all, so neither the endpoint (with its query credential) nor the token.
    for (const value of ['review-browser-query-secret', 'review-browser-bearer-secret', '127.0.0.1:9/devtools']) assert.ok(!raw.text.includes(value), 'viewer received browser options');
    assert.equal(raw.value.monitors.find(v => v.monitor.id === networkMonitors['real-browser'].id)?.monitor.browser, undefined, 'no browser object for viewers');
    // Protocol 19 GameDig: viewers receive no gamedig object, so neither the service address nor either token.
    for (const value of [networkMonitors.gamedig.gamedig.token, networkMonitors.gamedig.gamedig.bearerToken, 'review-game-admission-bearer-0123', 'game-query.canary-review.test']) assert.ok(!raw.text.includes(value), 'viewer received GameDig options');
    assert.equal(raw.value.monitors.find(v => v.monitor.id === networkMonitors.gamedig.id)?.monitor.gamedig, undefined, 'no gamedig object for viewers');
    // r5: a WebSocket check's OAuth2 grant is a credential like its bearer token; the description is viewer-readable metadata.
    for (const value of ['review-ws-oauth-secret', 'review-ws-client']) assert.ok(!raw.text.includes(value), 'viewer received WebSocket OAuth2 credentials');
    assert.equal(raw.value.monitors.find(v => v.monitor.id === networkMonitors.tcp.id)?.monitor.description, networkMonitors.tcp.description, 'viewers read the description');
    const c = await api(A, '/api/notification-channels', 'GET', undefined, token);
    for (const value of ['review-hook-key', 'review-smtp-password', 'review-slack-secret', 'review-telegram-token', '-1001234567890', 'review-topic', 'tk_review-ntfy-token', 'review-gotify-token', 'a'.repeat(30), 'u'.repeat(30)]) assert.ok(!c.text.includes(value), 'viewer received private channel configuration');
    await signIn({ ...A, token }, true); await nav(`#/monitors/${monitors.redis.id}`); await heading(monitors.redis.name); await text('Hidden from viewers');
    await nav(`#/monitors/${networkMonitors.steam.id}`); await heading(networkMonitors.steam.name); await text('Hidden from viewers');
    assert.ok(!(await page.eval('document.body.innerText')).includes(networkMonitors.steam.steam.apiKey));
    await nav(`#/monitors/${networkMonitors.pm2.id}`); await heading(networkMonitors.pm2.name); await text('Hidden from viewers'); await text(networkMonitors.pm2.target);
    assert.ok(!(await page.eval('document.body.innerText')).includes(networkMonitors.pm2.pm2.home));
    await nav(`#/monitors/${networkMonitors.gamedig.id}`); await heading(networkMonitors.gamedig.name); await text('Hidden from viewers'); await text(networkMonitors.gamedig.target);
    const gameText = await page.eval('document.body.innerText');
    for (const value of [networkMonitors.gamedig.gamedig.token, networkMonitors.gamedig.gamedig.bearerToken, networkMonitors.gamedig.gamedig.endpoint]) assert.ok(!gameText.includes(value), 'viewer page shows a GameDig private value');
    await nav('#/channels/new'); await text('Viewers can’t change channels');
    await signIn(A, true); return { redactedAtServer: true, formsReadOnly: true };
  });
  await step('Viewer: no Clone action, and the clone address shows no form and no copied value', async () => {
    const token = secret(); secrets.add(token); const made = await api(A, '/api/accounts', 'POST', { name: `review-clone-viewer-${Date.now()}`, role: 'viewer', token }); assert.equal(made.status, 201, made.text);
    const m = (await api(A, '/api/monitors')).value.find(v => v.monitor.id === networkMonitors.http.id).monitor;
    try {
      await signIn({ ...A, token }, true); await nav(`#/monitors/${m.id}`); await heading(m.name);
      assert.equal(await page.eval(`[...document.querySelectorAll('a,button')].some(e => __vis(e) && __norm(e.innerText) === 'Clone')`), false, 'viewers see no Clone action');
      await nav(`#/monitors/${m.id}/clone`); await text('Viewers can’t change monitors');
      assert.equal(await page.eval(`document.querySelectorAll('main input, main textarea, main select').length`), 0, 'no form');
      const shown = await page.eval(`document.body.innerText + [...document.querySelectorAll('input,textarea')].map(e => e.value).join(' ')`);
      for (const value of ['review-password', 'private-body', 'private-header']) assert.ok(!shown.includes(value), 'viewer page shows a private value');
    } finally { await signIn(A, true); }
    return { cloneHidden: true, cloneRouteRefused: true };
  });
  if (args.includes('--public-availability')) {
    const { reviewPublishedAvailability } = await import('./public-availability-browser.mjs');
    await reviewPublishedAvailability({ api, A, B, state, page, nav, heading, text, click, choose, fill,
      until, step, sleep, report, setHook: value => { requestHook = value; } });
  }
  await step('Browser: storage contains only endpoint/theme; reload forgets credential', async () => {
    const storage = await page.eval(`({local:{...localStorage},session:{...sessionStorage},cookie:document.cookie})`); assert.ok(Object.keys(storage.local).every(k => ['uptime-canary.endpoint','uptime-canary.theme'].includes(k))); assert.deepEqual(storage.session, {}); assert.equal(storage.cookie, '');
    for (const s of secrets) assert.ok(!JSON.stringify(storage).includes(s));
    await page.send('Page.reload'); await until(() => page.eval(`!document.querySelector('nav.rail') && document.body.innerText.includes('Sign in')`), 'reload signs out'); await ready(); await signIn(A); return { keys: Object.keys(storage.local), noSessionOrCookies: true };
  });
}

let exitCode = 1;
try {
  if (args[0] === '--run') { runDir = resolve(args[1]); state = json(join(runDir, 'state.private.json')); assert.equal(state.kind, 'dashboard-contract-review'); assert.equal(resolve(state.runDir), runDir); }
  else await fixture();
  if (args.includes('--fixture')) { while (!existsSync(join(runDir, 'STOP'))) await sleep(250); exitCode = 0; }
  else exitCode = await review();
} catch (error) { console.error(redact(error.stack)); }
finally {
  await stopInterceptions?.();
  if (state && args[0] !== '--run') { write(join(runDir, 'state.private.json'), { ...state, nodes: state.nodes.map(n => ({ ...n, token: '[removed]' })) }); }
  if (report) {
    const { requests, ...compact } = report;
    const raw = redact(JSON.stringify(compact, null, 2));
    if (Buffer.byteLength(raw) > 128 * 1024) throw Error('Review summary exceeds 128 KiB');
    writeFileSync(join(root, '.canary', 'dashboard-contract-current.json'), raw);
  }
  for (const child of owned.slice().reverse()) {
    if (child.exitCode !== null || child.signalCode !== null) continue;
    if (process.platform === 'win32') {
      try { await command('taskkill.exe', ['/PID', String(child.pid), '/T', '/F'], root); } catch {}
    } else {
      child.kill('SIGTERM');
      await Promise.race([once(child, 'exit'), sleep(1500)]);
      if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL');
    }
  }
  cleanup();
}
process.exit(exitCode);
