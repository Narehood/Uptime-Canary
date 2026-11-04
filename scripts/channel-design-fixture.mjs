// Visual fixture for the real dashboard source. No Go process, database or external provider.
// Root only: python scripts/bounded_validation.py --timeout 900 -- node scripts/channel-design-fixture.mjs
// Stop with .canary/channel-design-stop. All server/cache artifacts live in guarded outputs.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { existsSync, readFileSync, writeFileSync, unlinkSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { createServer as createVite } from 'vite';

const root = process.cwd();
assert.ok(existsSync(join(root, '.canary/bounded-validation.lock')), 'Use bounded_validation.py');
const output = join(root, '.canary/work/current/outputs');
const stop = join(root, '.canary/channel-design-stop');
if (existsSync(stop)) unlinkSync(stop);
let protocol = 22, role = 'admin', channels, revision = 1, testMode = 'reject', attempts = [], channelReadsFail = false;
const requests = [];
const now = () => Date.now();
const makeChannel = (id, name, config, extra = {}) => ({ channel: {
  id, name, config, revision: `fixture-revision-${revision++}`, updatedAt: now() - 3600_000,
  updatedBy: 'canary-eu', enabled: true, ...extra,
}, configConflict: false });
function reset(empty = false) {
  attempts = [];
  channelReadsFail = false;
  channels = empty ? [] : [
    makeChannel('ha-office', 'Office status light', { kind: 'homeassistant', homeassistant: {
      baseURL: 'https://home.example.test', accessToken: 'private-fixture-ha-token', eventType: 'uptime_canary',
    } }, { events: ['down', 'recovery'] }),
    makeChannel('matrix-ops', 'Operations room', { kind: 'matrix', matrix: {
      baseURL: 'https://matrix.example.test', accessToken: 'private-fixture-matrix-token', roomId: '!operations:example.test',
    } }, { isDefault: true }),
  ];
}
reset();
const monitor = (id, name, notificationIds) => ({ monitor: {
  id, name, notificationIds, revision: 'fixture-monitor-r1', updatedAt: now(), type: 'http',
  target: 'https://example.test', intervalMs: 60_000, timeoutMs: 10_000, retries: 1,
  nodeIds: ['canary-eu'], enabled: true,
}, observations: [], state: 'pending', configConflict: false });
const overview = () => ({
  build: { name: 'uptime-canary', version: 'development', peerProtocol: protocol, modified: null },
  node: { id: 'canary-eu', name: 'Europe' }, now: now(), user: { name: 'Alex Morgan', role },
  monitors: [monitor('website', 'Website', ['ha-office', 'matrix-ops']), monitor('api', 'API health', ['matrix-ops'])],
  nodes: [], incidents: [], notifications: [], groups: [], maintenance: [],
});
const vite = await createVite({ root: join(root, 'apps/web'), configFile: join(root, 'apps/web/vite.config.ts'),
  cacheDir: join(output, 'vite-cache'), server: { middlewareMode: true, hmr: false },
  logLevel: 'error',
});
const server = createServer(async (req, res) => {
  const url = new URL(req.url, 'http://127.0.0.1');
  const reply = (value, status = 200) => { res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }); res.end(JSON.stringify(value)); };
  try {
    if (url.pathname === '/__fixture/reset') {
      protocol = Number(url.searchParams.get('protocol') || 22); role = url.searchParams.get('role') || 'admin';
      testMode = url.searchParams.get('test') || 'reject';
      reset(url.searchParams.get('empty') === '1'); requests.length = 0; return reply({ fixture: true });
    }
    if (url.pathname === '/__fixture/report') return reply({ fixture: true, requests });
    if (url.pathname === '/__fixture/channels-failure') {
      channelReadsFail = url.searchParams.get('on') === '1'; return reply({ fixture: true, channelReadsFail });
    }
    if (url.pathname.startsWith('/api/')) {
      requests.push({ method: req.method, path: url.pathname });
      if (url.pathname === '/api/overview') return reply(overview());
      if (url.pathname === '/api/auth/me') return reply({ id: 'fixture-admin', name: 'Alex Morgan', role, passwordEnabled: true });
      if (url.pathname === '/api/tags') return reply([]);
      if (url.pathname === '/api/notification-channels' && req.method === 'GET') return channelReadsFail
        ? reply({ error: 'Channel refresh unavailable in the private fixture.' }, 503)
        : reply(role === 'viewer' ? channels.map(view => ({ ...view, channel: { ...view.channel, config: { kind: view.channel.config.kind } } })) : channels);
      if (url.pathname.endsWith('/test') || url.pathname.endsWith('/preview-test')) {
        if (role === 'viewer') return reply({ error: 'Viewer access refused by fixture.' }, 403);
        if (testMode === 'reject') return reply({ error: 'Visual fixture rejected the test; no notification was sent.' }, 400);
        let bytes = ''; for await (const chunk of req) { bytes += chunk; if (bytes.length > 262144) return reply({ error: 'Too large' }, 413); }
        const body = JSON.parse(bytes), preview = url.pathname.endsWith('/preview-test');
        const channelId = preview ? undefined : url.pathname.split('/').at(-2);
        const provider = preview ? body.config.kind : channels.find(view => view.channel.id === channelId)?.channel.config.kind;
        const attempt = { id: `fixture-test-${revision++}`, nodeId: 'canary-eu', kind: 'test', provider,
          state: testMode === 'accepted' ? 'accepted' : 'queued', failures: 0, channelId, lastAttemptAt: now() };
        attempts.unshift(attempt);
        // Simulates queueing followed by an unusable acknowledgement, never an external provider send.
        if (testMode === 'malformed') return reply({ fixture: true, acknowledgementUnreadable: true }, 202);
        return reply({ ...(preview ? { preview: true } : {}), attempts: [attempt] }, 202);
      }
      if (url.pathname.startsWith('/api/notification-channels') && ['POST', 'PUT'].includes(req.method)) {
        let bytes = ''; for await (const chunk of req) { bytes += chunk; if (bytes.length > 262144) return reply({ error: 'Too large' }, 413); }
        const body = JSON.parse(bytes), id = req.method === 'PUT' ? url.pathname.split('/').at(-1) : `fixture-created-${revision}`;
        const saved = makeChannel(id, body.name, body.config, body);
        saved.channel.revision = `fixture-revision-${revision++}`;
        channels = [...channels.filter(v => v.channel.id !== id), saved];
        return reply(saved.channel, req.method === 'POST' ? 201 : 200);
      }
      if (url.pathname === '/api/monitors') return reply(overview().monitors);
      if (url.pathname === '/api/notification-deliveries') return role === 'viewer'
        ? reply({ error: 'Viewer access refused by fixture.' }, 403) : reply(attempts);
      return reply({ error: 'Not implemented by visual fixture' }, 404);
    }
    if (url.pathname === '/' || url.pathname === '/index.html') {
      let html = readFileSync(join(root, 'apps/web/index.html'), 'utf8');
      const setup = `<script>sessionStorage.setItem('uptime-canary.sign-in',JSON.stringify({baseUrl:location.origin,origin:location.origin,mode:'direct',credential:'token',token:'private-fixture-account',expiresAt:null,issuer:{nodeId:'canary-eu',accountId:'fixture-admin'}}));</script>`;
      html = html.replace('<head>', '<head>' + setup);
      html = await vite.transformIndexHtml(url.pathname, html);
      res.writeHead(200, { 'Content-Type': 'text/html', 'Cache-Control': 'no-store' }); return res.end(html);
    }
    vite.middlewares(req, res);
  } catch (error) { reply({ error: error.message }, 500); }
});
server.listen(0, '127.0.0.1');
await new Promise(resolve => server.once('listening', resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
writeFileSync(join(root, '.canary/channel-design-current.json'), JSON.stringify({ origin, fixture: true, backend: 'deterministic in-memory API; real frontend source; no provider sends' }, null, 2));
console.log(`DESIGN_FIXTURE ${origin}`);
await new Promise(resolve => { const timer = setInterval(() => { if (existsSync(stop)) { clearInterval(timer); resolve(); } }, 250); });
await vite.close(); await new Promise(resolve => server.close(resolve));
