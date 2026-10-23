// Root-owned manual browser fixture. Run only through bounded_validation.py.
// Existing sources are built in place; all runtime files live in owned scratch.
import { createServer } from 'node:http';
import { createServer as tcpServer } from 'node:net';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { randomBytes, createHash } from 'node:crypto';
import { readFileSync, writeFileSync, existsSync } from 'node:fs';
import { resolve, join, extname, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const work = resolve(root, '.canary/work/current/outputs');
const web = join(work, 'web');
const binary = join(work, 'messaging-proof.exe');
if (!existsSync(binary) || !existsSync(join(web, 'index.html'))) throw Error('Build the owned fixture artifacts first');
const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !/^CANARY_/i.test(key)));
const token = randomBytes(32).toString('hex'), stopKey = randomBytes(32).toString('hex');
const calls = [];
let child, endpoint, stopping = false;
const headers = { 'Cache-Control': 'no-store', 'X-Content-Type-Options': 'nosniff' };
const types = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png', '.ico': 'image/x-icon' };
const staticServer = createServer((req, res) => {
  if (req.url === '/__stop' && req.method === 'POST' && req.headers.authorization === `Bearer ${stopKey}`) {
    res.writeHead(200, headers).end('stopping'); void stop(); return;
  }
  const path = resolve(web, '.' + decodeURIComponent(new URL(req.url, 'http://localhost').pathname));
  if (path !== web && !path.startsWith(web + sep)) { res.writeHead(404).end(); return; }
  const target = extname(path) ? path : join(web, 'index.html');
  try {
    let body = readFileSync(target);
    if (extname(target) === '.html') {
      // Set the private endpoint before React mounts, so its initial auth probe
      // cannot contact the dashboard's usual default node on port 3100.
      body = body.toString().replace('<head>', `<head><script>localStorage.setItem('uptime-canary.endpoint',${JSON.stringify(endpoint)})</script>`);
    }
    res.writeHead(200, { ...headers, 'Content-Type': types[extname(target)] || 'application/octet-stream' }); res.end(body);
  }
  catch { res.writeHead(404).end(); }
});
const sink = createServer((req, res) => {
  let bytes = 0;
  req.on('data', value => { bytes += value.length; if (bytes > 65536) req.destroy(); });
  req.on('end', () => {
    calls.push({ path: req.url?.split('?')[0].replace(/bot[^/]+/g, 'bot[redacted]'), bytes });
    let reply = '', code = 200;
    if (req.url?.startsWith('/services/')) reply = 'ok';
    else if (req.url?.startsWith('/telegram/')) reply = '{"ok":true,"result":{"message_id":1,"chat":{"id":12345}}}';
    else if (req.url?.startsWith('/ntfy/')) reply = '{"id":"Abcdef123456","event":"message","topic":"canary"}';
    else if (req.url?.startsWith('/gotify/')) reply = '{"id":1,"appid":2,"message":"fixture","date":"2026-10-22T00:00:00Z"}';
    else if (req.url?.startsWith('/pushover/')) reply = '{"status":1,"request":"647d2300-702c-4b38-8b2f-d56326ae460b"}';
    else code = 404;
    res.writeHead(code, { 'Content-Type': 'application/json' }).end(reply);
    writeFileSync(join(work, 'messaging-calls.json'), JSON.stringify(calls));
  });
});
async function port() { const s = tcpServer(); s.listen(0, '127.0.0.1'); await once(s, 'listening'); const p = s.address().port; await new Promise(done => s.close(done)); return p; }
async function stop() {
  if (stopping) return; stopping = true;
  clearTimeout(timer);
  if (child && child.exitCode === null) {
    const exited = once(child, 'exit'); child.kill(); await exited;
  }
  sink.closeAllConnections(); staticServer.closeAllConnections();
  await Promise.all([new Promise(done => sink.close(done)), new Promise(done => staticServer.close(done))]);
  writeFileSync(join(work, 'fixture-cleanup.json'), JSON.stringify({ stopped: true, fixtureCalls: calls.length }));
}
const timer = setTimeout(() => { console.error('Fixture expired'); void stop(); }, 480000);
process.on('SIGINT', () => void stop()); process.on('SIGTERM', () => void stop());
try {
  staticServer.listen(0, '127.0.0.1'); sink.listen(0, '127.0.0.1');
  await Promise.all([once(staticServer, 'listening'), once(sink, 'listening')]);
  const origin = `http://127.0.0.1:${staticServer.address().port}`;
  endpoint = `http://127.0.0.1:${await port()}`;
  child = spawn(binary, ['serve'], { cwd: work, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'], env: { ...env,
    CANARY_NODE_ID: 'bounded-messaging-proof', CANARY_HOST: '127.0.0.1', CANARY_PORT: new URL(endpoint).port,
    CANARY_DB: join(work, 'messaging.db'), CANARY_IDENTITY_DIRECTORY: join(work, 'identities'),
    CANARY_ADMIN_TOKEN: token, CANARY_CLUSTER_TOKEN: randomBytes(32).toString('hex'), CANARY_PEERS: '[]', CANARY_CORS_ORIGINS: origin,
  } });
  let log = ''; for (const stream of [child.stdout, child.stderr]) stream.on('data', value => { log = (log + value.toString()).slice(-4096); });
  let ready = false;
  for (let attempt = 0; attempt < 100; attempt++) {
    if (child.exitCode !== null) throw Error(`Private node exited: ${log.replaceAll(token, '[redacted]')}`);
    try { ready = (await fetch(endpoint + '/api/auth/status')).ok; if (ready) break; } catch {}
    await new Promise(done => setTimeout(done, 100));
  }
  if (!ready) throw Error('Private node did not become ready');
  writeFileSync(join(work, 'messaging-state.private.json'), JSON.stringify({ origin, endpoint, token, stopKey, sink: `http://127.0.0.1:${sink.address().port}`, binarySha256: createHash('sha256').update(readFileSync(binary)).digest('hex') }));
  console.log('READY bounded messaging browser fixture');
  if (process.argv.includes('--browser')) {
    const { review } = await import('./bounded-channel-browser.mjs');
    const result = await review({ origin, endpoint, token, sink: `http://127.0.0.1:${sink.address().port}`, work });
    console.log('PROOF_RESULT ' + JSON.stringify(result));
    if (!result.passed) process.exitCode = 1;
    await stop();
  }
} catch (error) { console.error(error.message); process.exitCode = 1; await stop(); }
