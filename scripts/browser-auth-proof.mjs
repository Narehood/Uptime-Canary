// Local functional browser proof. Never reads .env or connects to a user's node.
// Run: node scripts/browser-auth-proof.mjs (full functional proof)
//      node scripts/browser-auth-proof.mjs --regressions (cross-node probes)
// Optional: CHROME_PATH, CANARY_GO, BROWSER_AUTH_PROOF_ORIGIN (loopback Vite only).
import { spawn } from 'node:child_process';
import { createHash, createHmac, randomBytes } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:net';
import { dirname, join, resolve, relative, isAbsolute } from 'node:path';
import { fileURLToPath } from 'node:url';
import { once } from 'node:events';

export const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const artifactRoot = join(root, '.canary', 'browser-auth-proof');
mkdirSync(artifactRoot, { recursive: true });
export const runDir = mkdtempSync(join(artifactRoot, 'run-'));
export const report = { startedAt: new Date().toISOString(), runtime: process.version, results: [], layouts: [], errors: [], network: [], cleanup: {}, sourceBefore: {}, sourceAfter: {} };
const secrets = new Set();
export const secret = () => { const s = randomBytes(24).toString('hex'); secrets.add(s); return s; };
export const rememberSecret = s => { if (s) secrets.add(s); return s; };
export const redact = s => { let out = String(s); for (const value of secrets) out = out.replaceAll(value, '[fixture-secret]'); return out; };
export const sleep = ms => new Promise(r => setTimeout(r, ms));
export function assert(ok, message) { if (!ok) throw new Error(message); }
export function pass(name, detail = {}) { report.results.push({ name, ...detail }); console.log(`PASS ${name}`); save(); }
export function save() { writeFileSync(join(runDir, 'results.json'), redact(JSON.stringify(report, null, 2))); }
const cleanEnv = Object.fromEntries(Object.entries(process.env).filter(([k]) => !/^CANARY_/i.test(k)));
const owned = [];
let browser;
export const origin = process.env.BROWSER_AUTH_PROOF_ORIGIN || 'http://localhost:5173';
assert(['http://localhost:5173', 'http://127.0.0.1:5173'].includes(origin), 'Proof origin must be the local Vite server.');
export async function port() { const s = createServer(); s.listen(0, '127.0.0.1'); await once(s, 'listening'); const p = s.address().port; await new Promise(r => s.close(r)); return p; }
async function command(exe, args, env = cleanEnv) {
  const p = spawn(exe, args, { cwd: root, env, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
  let output = ''; p.stdout.on('data', b => { output += b; }); p.stderr.on('data', b => { output += b; });
  p.on('error', () => {});
  const [code] = await once(p, 'exit');
  assert(code === 0, `Command failed (${code}): ${output.slice(-3000)}`);
  return output;
}
function sourceHashes() {
  const files = [];
  function walk(dir) { for (const e of readdirSync(join(root, dir), { withFileTypes: true })) { const f = join(dir, e.name); if (e.isDirectory()) walk(f); else files.push(f); } }
  for (const d of ['apps/node-go', 'apps/web/src', 'packages/shared/src']) walk(d);
  files.push('go.mod', 'go.sum', 'apps/web/package.json', 'apps/web/vite.config.ts');
  return Object.fromEntries(files.filter(f => existsSync(join(root, f))).sort().map(f => [f.replaceAll('\\', '/'), createHash('sha256').update(readFileSync(join(root, f))).digest('hex')]));
}
export async function api(node, path, method = 'GET', body, token = node.token) {
  const r = await fetch(node.url + path, { method, headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) }, ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: AbortSignal.timeout(10000), redirect: 'error' });
  const value = await r.json().catch(() => null);
  if (value?.token) rememberSecret(value.token);
  if (value?.secret) rememberSecret(value.secret);
  if (value?.uri) rememberSecret(value.uri);
  return { status: r.status, value };
}
export async function startNode(binary, label) {
  const p = await port(), token = secret(), db = join(runDir, `${label}.db`);
  const node = { url: `http://127.0.0.1:${p}`, token, id: `auth-proof-${label}`, db };
  const child = spawn(binary, ['serve'], { cwd: runDir, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'], env: { ...cleanEnv, CANARY_NODE_ID: node.id, CANARY_NODE_NAME: `Auth proof ${label}`, CANARY_HOST: '127.0.0.1', CANARY_PORT: String(p), CANARY_DB: db, CANARY_ADMIN_TOKEN: token, CANARY_CLUSTER_TOKEN: secret(), CANARY_PEERS: '[]', CANARY_CORS_ORIGINS: 'http://localhost:5173,http://127.0.0.1:5173' } });
  child.stdout.resume(); child.stderr.resume(); owned.push(child); node.child = child;
  await until(async () => { try { return (await api(node, '/api/auth/status')).status === 200; } catch { return false; } }, 'fixture node startup');
  return node;
}
export async function until(fn, description, timeout = 10000) { const start = Date.now(); do { const v = await fn(); if (v) return v; await sleep(100); } while (Date.now() - start < timeout); throw new Error(`Timed out: ${description}`); }

export class Cdp {
  pending = new Map(); seq = 0; listeners = new Map();
  constructor(ws) { this.ws = ws; ws.addEventListener('message', e => { const m = JSON.parse(e.data); if (m.id) { const pair = this.pending.get(m.id); if (pair) { clearTimeout(pair.timer); this.pending.delete(m.id); m.error ? pair.reject(new Error(m.error.message)) : pair.resolve(m.result); } } else { for (const cb of this.listeners.get(m.method) || []) cb(m.params); } }); }
  on(event, callback) { this.listeners.set(event, [...(this.listeners.get(event) || []), callback]); }
  send(method, params = {}) { return new Promise((resolveP, reject) => { const id = ++this.seq, timer = setTimeout(() => { this.pending.delete(id); reject(new Error(`CDP timeout: ${method}`)); }, 15000); this.pending.set(id, { resolve: resolveP, reject, timer }); this.ws.send(JSON.stringify({ id, method, params })); }); }
  async eval(expression) { const r = await this.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true }); if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text); return r.result?.value; }
  async nav(url) { await this.send('Page.navigate', { url }); await until(() => this.eval('!!document.querySelector("h1")'), 'page loaded'); }
  async text(text) { return until(() => this.eval(`document.body.innerText.includes(${JSON.stringify(text)})`), `visible text: ${text}`); }
  async route(hash) { await this.eval(`location.hash=${JSON.stringify(hash)}`); await sleep(180); }
  // Locate from the current DOM by accessible name, then use real Chrome input events.
  async target(kind, name, scope = 'document') {
    const expression = `(() => { const scope=${scope}; const name=${JSON.stringify(name)}; const norm=s=>s?.replace(/\\s+/g,' ').trim(); const visible=e=>!!e.getClientRects().length; let all;
      if (['field','edit','choice'].includes(${JSON.stringify(kind)})) all=[...scope.querySelectorAll('input,select,textarea')].filter(e=>visible(e)&&(${JSON.stringify(kind)}!=='edit'||!['checkbox','radio'].includes(e.type))&&(${JSON.stringify(kind)}!=='choice'||['checkbox','radio'].includes(e.type))&&((e.getAttribute('aria-label')===name)||[...(e.labels||[])].some(l=>norm(l.innerText)===name)));
      else if (${JSON.stringify(kind)}==='button') all=[...scope.querySelectorAll('button')].filter(e=>visible(e)&&(e.getAttribute('aria-label')===name||norm(e.innerText)===name));
      else all=[...scope.querySelectorAll('a')].filter(e=>visible(e)&&(e.getAttribute('aria-label')===name||norm(e.innerText)===name));
      if(all.length!==1) throw new Error('Locator '+${JSON.stringify(kind)}+' '+name+' matched '+all.length); const e=all[0]; if(e.disabled) throw new Error('Disabled '+name); e.scrollIntoView({block:'center'}); const r=e.getBoundingClientRect(); e.setAttribute('data-auth-proof-target','yes'); return {x:r.x+r.width/2,y:r.y+r.height/2}; })()`;
    await this.eval(`document.querySelectorAll('[data-auth-proof-target]').forEach(e=>e.removeAttribute('data-auth-proof-target'))`);
    return this.eval(expression);
  }
  async click(kind, name, scope) { const box = await until(async()=>{const b=await this.target(kind,name,scope);const hit=await this.eval(`(()=>{const e=document.querySelector('[data-auth-proof-target]'),hit=document.elementFromPoint(${b.x},${b.y});return e===hit||e.contains(hit)||(${JSON.stringify(kind)}==='choice'&&e.closest('label')?.contains(hit));})()`);return hit?b:false;},`click unobstructed ${name}`,12000); await this.send('Input.dispatchMouseEvent', { type: 'mousePressed', ...box, button: 'left', clickCount: 1 }); await this.send('Input.dispatchMouseEvent', { type: 'mouseReleased', ...box, button: 'left', clickCount: 1 }); await sleep(120); }
  async field(name, value, scope) { await this.target('edit', name, scope); await this.eval(`document.querySelector('[data-auth-proof-target]').focus();document.querySelector('[data-auth-proof-target]').select?.()`); await this.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Backspace', code: 'Backspace', windowsVirtualKeyCode: 8 }); await this.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Backspace', code: 'Backspace', windowsVirtualKeyCode: 8 }); await this.send('Input.insertText', { text: value }); await sleep(40); }
  async select(name, value, scope) { await this.target('field', name, scope); await this.eval(`(()=>{const e=document.querySelector('[data-auth-proof-target]');e.value=${JSON.stringify(value)};e.dispatchEvent(new Event('change',{bubbles:true}));})()`); await sleep(60); }
  async screenshot(name) { const r = await this.send('Page.captureScreenshot', { format: 'png' }); writeFileSync(join(runDir, `${name}.png`), Buffer.from(r.data, 'base64')); }
  // Hold a real server response so UI actions can finish after a replacement connection is established.
  async holdResponse(node, path, method = 'POST') {
    assert(!this.responseHold, 'Only one delayed response at a time');
    const hold = { url: node.url + path, method, requestId: null };
    this.responseHold = hold;
    await this.send('Fetch.enable', { patterns: [{ urlPattern: '*', requestStage: 'Request' }, { urlPattern: hold.url, requestStage: 'Response' }] });
    return {
      wait: () => until(() => !!hold.requestId, 'real server response held'),
      release: async () => {
        await until(() => !!hold.requestId, 'delayed response ready');
        this.responseHold = null;
        await this.send('Fetch.continueResponse', { requestId: hold.requestId });
        await this.send('Fetch.enable', { patterns: [{ urlPattern: '*', requestStage: 'Request' }] });
        await sleep(300);
      },
    };
  }
}
export const card = title => `[...document.querySelectorAll('section.card')].find(e=>e.querySelector('h2')?.textContent.trim()===${JSON.stringify(title)})`;
export const dialog = 'document.querySelector("dialog[open]")';
export function totp(seed, step = Math.floor(Date.now() / 30000)) { const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'; let bits = ''; for (const c of seed.replaceAll(' ', '').replaceAll('=', '').toUpperCase()) bits += alphabet.indexOf(c).toString(2).padStart(5, '0'); const key = Buffer.from((bits.match(/.{8}/g) || []).map(b => parseInt(b, 2))); const counter = Buffer.alloc(8); counter.writeBigUInt64BE(BigInt(step)); const mac = createHmac('sha1', key).update(counter).digest(); const offset = mac[19] & 15; return String((mac.readUInt32BE(offset) & 0x7fffffff) % 1000000).padStart(6, '0'); }
export async function freshCode(seed, previousStep) { if (Math.floor(Date.now()/30000) <= previousStep) { console.log('WAIT fresh 30-second authenticator window'); await sleep(30000-Date.now()%30000+250); } return { code: totp(seed), step: Math.floor(Date.now()/30000) }; }
export async function initialize() {
  report.sourceBefore = sourceHashes(); save();
  const go = process.env.CANARY_GO || join(root, '.canary', 'tools', 'go', 'bin', process.platform === 'win32' ? 'go.exe' : 'go');
  const binary = join(runDir, process.platform === 'win32' ? 'auth-proof.exe' : 'auth-proof');
  await command(go, ['build', '-trimpath', '-o', binary, './apps/node-go/cmd/canary'], { ...cleanEnv, CGO_ENABLED: '0', GOTOOLCHAIN: 'local' });
  report.goVersion = (await command(go, ['version'])).trim();
  report.binarySha256 = createHash('sha256').update(readFileSync(binary)).digest('hex');
  report.sourceAfterBuild = sourceHashes();
  const a = await startNode(binary, 'a'), b = await startNode(binary, 'b');
  report.fixtures = [a, b].map(n => ({ id: n.id, url: n.url, database: relative(root, n.db).replaceAll('\\', '/') }));
  const chromePath = process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe';
  const debugPort = await port();
  const chrome = spawn(chromePath, ['--headless=new', `--remote-debugging-port=${debugPort}`, `--user-data-dir=${join(runDir, 'chrome-profile')}`, '--no-first-run', '--no-default-browser-check', '--disable-background-networking', '--disable-component-update', '--disable-sync', '--disable-default-apps', '--disable-extensions', '--no-proxy-server', '--password-store=basic', 'about:blank'], { windowsHide: true, stdio: 'ignore' });
  owned.push(chrome); browser = chrome;
  const targets = await until(async () => { try { return await (await fetch(`http://127.0.0.1:${debugPort}/json`)).json(); } catch { return false; } }, 'owned Chrome startup');
  const ws = new WebSocket(targets.find(t => t.type === 'page').webSocketDebuggerUrl); await once(ws, 'open'); const page = new Cdp(ws);
  const allowedOrigins = new Set([origin, a.url, b.url]);
  await page.send('Runtime.enable'); await page.send('Page.enable'); await page.send('Network.enable');
  await page.send('Fetch.enable', { patterns: [{ urlPattern: '*', requestStage: 'Request' }] });
  page.on('Fetch.requestPaused', p => {
    if (p.responseStatusCode !== undefined) {
      const hold = page.responseHold;
      if (hold && p.request.url === hold.url && p.request.method === hold.method && !hold.requestId) hold.requestId = p.requestId;
      else page.send('Fetch.continueResponse', { requestId: p.requestId }).catch(() => {});
      return;
    }
    const u = new URL(p.request.url); const allowed = allowedOrigins.has(u.origin); if (!allowed && u.protocol !== 'data:') { report.network.push({ blocked: true, origin: u.origin, path: u.pathname }); page.send('Fetch.failRequest', { requestId: p.requestId, errorReason: 'BlockedByClient' }).catch(() => {}); } else page.send('Fetch.continueRequest', { requestId: p.requestId }).catch(() => {});
  });
  const responses = [], requests = [];
  page.on('Network.requestWillBeSent', p => { if(p.request.url.startsWith('http')) { const u = new URL(p.request.url); requests.push({ url: p.request.url, method: p.request.method, hasUrlCredential: !!(u.username || u.password || /(?:password|token|secret|code)=/i.test(u.search) || [...secrets].some(s=>p.request.url.includes(s))), headers: p.request.headers }); } });
  page.on('Network.responseReceived', p => { const u = new URL(p.response.url); report.network.push({ origin: u.origin, path: u.pathname, status: p.response.status }); responses.push(p); });
  page.on('Runtime.exceptionThrown', p => { report.errors.push(redact(p.exceptionDetails.exception?.description || p.exceptionDetails.text)); });
  page.responses = responses; page.requests = requests;
  page.lastResponse = async path => { const p = [...responses].reverse().find(p => new URL(p.response.url).pathname === path); assert(p, `No response for ${path}`); const body = await page.send('Network.getResponseBody', { requestId: p.requestId }); const value = JSON.parse(body.body); if(value.token) rememberSecret(value.token); if(value.secret) rememberSecret(value.secret); if(value.uri) rememberSecret(value.uri); return { status: p.response.status, value }; };
  await page.send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false });
  await page.nav(origin);
  pass('isolated latest-source Go build and two fresh loopback node databases');
  return { a, b, page, binary };
}
export async function checks() {
  await command(process.execPath, [join(root, 'node_modules/typescript/bin/tsc'), '--noEmit', '-p', 'apps/web/tsconfig.json']); pass('web typecheck');
  const buildOutput = await command(process.execPath, [join(root, 'node_modules/vite/bin/vite.js'), 'build', 'apps/web', '--outDir', join(runDir, 'web-build'), '--emptyOutDir']);
  report.buildOutput = buildOutput.replaceAll(root, '<workspace>').trim(); pass('web production build into private artifact directory');
}
export async function cleanup(page) {
  if(page) { try { await page.send('Browser.close'); } catch {} page.ws.close(); }
  for(const p of [...owned].reverse()) { if(p.exitCode === null && p.signalCode === null) { p.kill(); await Promise.race([once(p, 'exit').catch(()=>{}), sleep(3000)]); } }
  report.sourceAfter = sourceHashes();
  report.changedSourceFiles = [...new Set([...Object.keys(report.sourceBefore), ...Object.keys(report.sourceAfter)])].filter(f => report.sourceBefore[f] !== report.sourceAfter[f]);
  report.changedProductionSourceFiles=report.changedSourceFiles.filter(f=>!f.endsWith('_test.go'));
  // Only this process's random run directory is eligible for recursive cleanup.
  const rel = relative(artifactRoot, runDir); assert(rel && !rel.startsWith('..') && !isAbsolute(rel), 'Unsafe cleanup path');
  for(const name of readdirSync(runDir)) { if(name === 'chrome-profile' || /\.db(?:-|\.|$)|\.exe$|^auth-proof$/.test(name)) { try { rmSync(join(runDir, name), { recursive: true, force: true }); } catch { report.cleanup.retainedPrivateArtifact = true; } } }
  report.cleanup.ownedProcessesStopped = owned.every(p => p.exitCode !== null || p.signalCode !== null);
  report.finishedAt = new Date().toISOString(); save(); console.log(`RESULT ${relative(root, runDir).replaceAll('\\', '/')}/results.json`);
}

async function main() {
  let page;
  try { const fixtures = await initialize(); page = fixtures.page; const { run } = await import(process.argv.includes('--regressions') ? './browser-auth-proof-regressions.mjs' : './browser-auth-proof-flows.mjs'); await run(fixtures); await checks(); if(report.findings?.length) process.exitCode=1; }
  catch(e) { report.failure = redact(e.stack); save(); console.error(redact(e.stack)); process.exitCode = 1; }
  finally { await cleanup(page); }
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) void main();
