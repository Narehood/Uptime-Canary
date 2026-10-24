// Independent contract review. All disposable files belong to the storage guard.
// Run: python scripts/bounded_validation.py --timeout 600 -- node scripts/dashboard-contract-review.mjs --await-browser
// Development: --fixture holds an owned fixture; --run <runDir> reviews that fixture; STOP ends it.
// Uses the documented Chrome/CDP fallback after attempting T3 preview. No .env or user database access.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import { once } from 'node:events';
import { createServer } from 'node:http';
import { createServer as netServer } from 'node:net';
import { existsSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, extname, join, relative, resolve, sep } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

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
let runDir, state, report, page, ws, requestHook;
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
  await command(go, ['build', '-trimpath', '-o', binary, './apps/node-go/cmd/canary'], source, { ...cleanEnv, CGO_ENABLED: '0', GOTOOLCHAIN: 'local' });
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
  const contentTypes = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png', '.woff2': 'font/woff2' };
  const preview = createServer((req, res) => {
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
  const artifacts = files(dist).map(f => ({ path: relative(dist, f).replaceAll('\\', '/'), bytes: readFileSync(f).length, sha256: sha(readFileSync(f)) }));
  state = { kind: 'dashboard-contract-review', runDir, source, origin, sink: `http://127.0.0.1:${sink.address().port}`, nodes, sourceHash, binaryHash: sha(readFileSync(binary)), artifacts, startedAt: new Date().toISOString(), pid: process.pid };
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
  await page.send('Fetch.enable', { patterns: [{ urlPattern: '*', requestStage: 'Request' }] });
  page.on('Fetch.requestPaused', p => {
    if (requestHook?.(p)) return;
    let ok = false; try { const u = new URL(p.request.url); ok = allowed.has(u.origin) || u.protocol === 'data:'; } catch {}
    if (ok) void page.send('Fetch.continueRequest', { requestId: p.requestId });
    else { report.blocked.push(p.request.url); void page.send('Fetch.failRequest', { requestId: p.requestId, errorReason: 'BlockedByClient' }); }
  });
  page.on('Runtime.exceptionThrown', p => { report.exceptions.push(redact(p.exceptionDetails?.exception?.description || p.exceptionDetails?.text)); });
  page.on('Network.requestWillBeSent', p => {
    const h = p.request.headers; report.requests.push({ url: p.request.url, method: p.request.method, authorization: Object.keys(h).some(k => k.toLowerCase() === 'authorization'), cookie: Object.keys(h).some(k => k.toLowerCase() === 'cookie') });
  });
  await page.send('Page.addScriptToEvaluateOnNewDocument', { source: `if(location.origin === ${J(state.origin)} && !localStorage.getItem('uptime-canary.endpoint')) localStorage.setItem('uptime-canary.endpoint', ${J(state.nodes[0].url)});` });
  await page.send('Page.navigate', { url: state.origin }); await until(() => page.eval('!!document.querySelector("h1")'), 'first paint'); await ready();
}
async function signIn(node, switching = false) {
  if (switching) { await page.eval(`document.querySelector('.topbar .tb-btn').click()`); await sleep(100); }
  const scope = switching ? 'dialog[open]' : undefined;
  await fill('Node API address', node.url, scope); await choose('Account token', scope); await fill('Account token', node.token, scope); await click(switching ? 'Sign in and switch' : 'Sign in', scope);
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
  return { ...monitor, ...channel, ...config, ...maintenance, ...specs, ...checks };
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
  await step('Catalogue matches all 28 implemented check types, including Globalping, system-service and Steam', async () => {
    assert.equal(f.CHECK_TYPES.length, 28); assert.equal(new Set(f.CHECK_TYPES.map(v => v.type)).size, 28);
    assert.ok(f.CHECK_TYPES.some(v => v.type === 'globalping'));
    assert.ok(f.CHECK_TYPES.some(v => v.type === 'system-service'));
    assert.ok(f.CHECK_TYPES.some(v => v.type === 'steam'));
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
  };
  for (const [type, options] of Object.entries(networkOptions)) await step(`API + form: ${type} preserves all non-service options and nullable omission`, async () => {
    if (options.push) secrets.add(options.push.token);
    if (options.steam) secrets.add(options.steam.apiKey);
    const made = await api(A, '/api/monitors', 'POST', base(type, options)); assert.equal(made.status, 201, made.text); const m = made.value;
    const d = f.draftFrom(m, [A.id]), initial = structuredClone(d); d.name += ' renamed'; const built = f.build(d, m, initial); assert.ok(built.input, JSON.stringify({ ...built.errors, ...built.serviceErrors }));
    const saved = await api(A, `/api/monitors/${m.id}`, 'PUT', { ...built.input, revision: m.revision }); assert.equal(saved.status, 200, saved.text);
    for (const key of ['http','tls','dns','websocket','smtp','ping','manual','push','steam','expectedStatusCodes','retries','retryIntervalMs','upsideDown','webhookUrl']) assert.deepEqual(saved.value[key], m[key], key);
    assert.equal(saved.value.target, m.target);
    if (type === 'push') { assert.equal(saved.value.pushStartedAt, m.pushStartedAt); assert.equal(saved.value.checkGeneration || saved.value.revision, m.checkGeneration || m.revision); }
    if (type === 'steam') assert.equal(saved.value.checkGeneration || saved.value.revision, m.checkGeneration || m.revision);
    networkMonitors[type] = saved.value; return { id: m.id, allOptionsPreserved: true };
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
  await browserReview({ f, A, B, base, monitors, channels, emptyKafka, emptyRabbit, networkMonitors, maintenanceWindows });
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
    for (const key of ['http','tls','dns','websocket','smtp','ping','manual','push','steam','expectedStatusCodes','retries','retryIntervalMs','upsideDown','webhookUrl']) assert.deepEqual(saved[key], m[key], key);
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
  await step('Viewer: API and UI hide monitor/channel/push credentials and disable editing', async () => {
    const token = secret(); secrets.add(token); const made = await api(A, '/api/accounts', 'POST', { name: `review-viewer-${Date.now()}`, role: 'viewer', token }); assert.equal(made.status, 201, made.text);
    const raw = await api(A, '/api/overview', 'GET', undefined, token); for (const value of ['review-password', 'review-hook-key', networkMonitors.push.push.token, networkMonitors.steam.steam.apiKey, 'private-body', 'private-header']) assert.ok(!raw.text.includes(value));
    const c = await api(A, '/api/notification-channels', 'GET', undefined, token);
    for (const value of ['review-hook-key', 'review-smtp-password', 'review-slack-secret', 'review-telegram-token', '-1001234567890', 'review-topic', 'tk_review-ntfy-token', 'review-gotify-token', 'a'.repeat(30), 'u'.repeat(30)]) assert.ok(!c.text.includes(value), 'viewer received private channel configuration');
    await signIn({ ...A, token }, true); await nav(`#/monitors/${monitors.redis.id}`); await heading(monitors.redis.name); await text('Hidden from viewers');
    await nav(`#/monitors/${networkMonitors.steam.id}`); await heading(networkMonitors.steam.name); await text('Hidden from viewers');
    assert.ok(!(await page.eval('document.body.innerText')).includes(networkMonitors.steam.steam.apiKey));
    await nav('#/channels/new'); await text('Viewers can’t change channels');
    await signIn(A, true); return { redactedAtServer: true, formsReadOnly: true };
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
