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
let saveMode = 'normal', deleteMode = 'normal', assignMode = 'normal', monitorChannels, deliveriesFail = false, deliveriesMalformed = false, rejectOverview = false;
const requests = [];
const now = () => Date.now();
// Go keeps these value-typed zero structs in provider JSON even when unused.
const nativeConfig = config => ({ sender: { email: '' }, smtp: { server: '', port: 0 },
  mailgun: { apiKey: '', domain: '' }, brevo: { apiKey: '' }, mandrill: { apiKey: '' },
  webhook: { url: '' }, templates: {}, ...config });
const makeChannel = (id, name, config, extra = {}) => ({ channel: {
  id, name, revision: `fixture-revision-${revision++}`, updatedAt: now() - 3600_000,
  enabled: true, ...extra, config: nativeConfig(config), // Match native metadata and provider JSON.
}, configConflict: false });
function reset(empty = false, extras = false) {
  attempts = [];
  channelReadsFail = false;
  deliveriesFail = false;
  deliveriesMalformed = false;
  rejectOverview = false;
  monitorChannels = { website: ['ha-office', 'matrix-ops'], api: ['matrix-ops'] };
  channels = empty ? [] : [
    makeChannel('ha-office', 'Office status light', { kind: 'homeassistant', homeassistant: {
      baseURL: 'https://home.example.test', accessToken: 'private-fixture-ha-token', eventType: 'uptime_canary',
    } }, { events: ['down', 'recovery'] }),
    makeChannel('matrix-ops', 'Operations room', { kind: 'matrix', matrix: {
      baseURL: 'https://matrix.example.test', accessToken: 'private-fixture-matrix-token', roomId: '!operations:example.test',
    } }, { isDefault: true }),
  ];
  if (!empty && extras) channels.push(
    makeChannel('smtp-ops', 'Operations email', { kind: 'smtp', sender: { email: 'canary@example.test' }, recipients: [{ email: 'ops-one@example.test' }, { email: 'ops-two@example.test' }], smtp: { server: 'smtp.example.test', port: 587, tlsMode: 'starttls', username: 'fixture-user', password: 'private-fixture-smtp-pass' } }),
    makeChannel('ntfy-ops', 'Operations topic', { kind: 'ntfy', ntfy: { topic: 'private-fixture-topic', username: 'fixture-user', password: 'private-fixture-ntfy-pass' } }),
    makeChannel('pushover-ops', 'Emergency phone', { kind: 'pushover', pushover: { appToken: 'a'.repeat(30), userKey: 'b'.repeat(30), priority: 2, retrySeconds: 60, expireSeconds: 3600 } }),
  );
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
  monitors: [monitor('website', 'Website', monitorChannels.website), monitor('api', 'API health', monitorChannels.api)],
  nodes: [], incidents: [], notifications: [], groups: [], maintenance: [],
});
const vite = await createVite({ root: join(root, 'apps/web'), configFile: join(root, 'apps/web/vite.config.ts'),
  cacheDir: join(output, 'vite-cache'), server: { middlewareMode: true, hmr: false },
  logLevel: 'error',
});
const server = createServer(async (req, res) => {
  const url = new URL(req.url, 'http://127.0.0.1');
  const reply = (value, status = 200) => { res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }); res.end(JSON.stringify(value)); };
  const brokenReply = () => { res.writeHead(200, { 'Content-Type': 'application/json' }); res.end('{'); };
  try {
    if (url.pathname === '/__fixture/reset') {
      protocol = Number(url.searchParams.get('protocol') || 22); role = url.searchParams.get('role') || 'admin';
      testMode = url.searchParams.get('test') || 'reject';
      saveMode = url.searchParams.get('save') || 'normal'; deleteMode = url.searchParams.get('remove') || 'normal'; assignMode = url.searchParams.get('assign') || 'normal';
      reset(url.searchParams.get('empty') === '1', url.searchParams.get('extras') === '1'); requests.length = 0; return reply({ fixture: true });
    }
    if (url.pathname === '/__fixture/report') return reply({ fixture: true, requests });
    if (url.pathname === '/__fixture/overview-rejection') { rejectOverview = url.searchParams.get('on') === '1'; return reply({ fixture: true, rejectOverview }); }
    if (url.pathname === '/__fixture/channels-failure') {
      channelReadsFail = url.searchParams.get('on') === '1'; return reply({ fixture: true, channelReadsFail });
    }
    if (url.pathname === '/__fixture/remove-channel') {
      channels = channels.filter(view => view.channel.id !== url.searchParams.get('id')); return reply({ fixture: true });
    }
    if (url.pathname === '/__fixture/deliveries-failure') {
      deliveriesFail = url.searchParams.get('on') === '1'; deliveriesMalformed = url.searchParams.get('malformed') === '1'; return reply({ fixture: true, deliveriesFail, deliveriesMalformed });
    }
    if (url.pathname.startsWith('/api/')) {
      requests.push({ method: req.method, path: url.pathname });
      if (url.pathname === '/api/overview') return rejectOverview ? reply({ error: 'The private fixture rejected this credential.' }, 401) : reply(overview());
      if (url.pathname === '/api/auth/me') return reply({ id: 'fixture-admin', name: 'Alex Morgan', role, passwordEnabled: true });
      if (url.pathname === '/api/tags') return reply([]);
      if (url.pathname === '/api/notification-channels' && req.method === 'GET') return channelReadsFail
        ? reply({ error: 'Channel refresh unavailable in the private fixture.' }, 503)
        : reply(role === 'viewer' ? channels.map(view => ({ ...view, channel: { ...view.channel, config: { kind: view.channel.config.kind } } })) : channels);
      if (url.pathname.endsWith('/apply') && req.method === 'POST') {
        let bytes = ''; for await (const chunk of req) { bytes += chunk; if (bytes.length > 262144) return reply({ error: 'Too large' }, 413); }
        const body = JSON.parse(bytes), id = url.pathname.split('/').at(-2), changedIds = [];
        for (const item of body.monitors ?? []) if (monitorChannels[item.id] && !monitorChannels[item.id].includes(id)) { monitorChannels[item.id].push(id); changedIds.push(item.id); }
        return assignMode === 'malformed' ? brokenReply() : reply({ changedIds });
      }
      if (url.pathname.startsWith('/api/notification-channels/') && req.method === 'DELETE') {
        const id = url.pathname.split('/').at(-1); channels = channels.filter(view => view.channel.id !== id);
        return deleteMode === 'malformed' ? brokenReply() : (res.writeHead(204), res.end());
      }
      if (url.pathname.endsWith('/test') || url.pathname.endsWith('/preview-test')) {
        if (role === 'viewer') return reply({ error: 'Viewer access refused by fixture.' }, 403);
        if (testMode === 'reject') return reply({ error: 'Visual fixture rejected the test; no notification was sent.' }, 400);
        let bytes = ''; for await (const chunk of req) { bytes += chunk; if (bytes.length > 262144) return reply({ error: 'Too large' }, 413); }
        const body = JSON.parse(bytes), preview = url.pathname.endsWith('/preview-test');
        const channelId = preview ? undefined : url.pathname.split('/').at(-2);
        const config = preview ? body.config : channels.find(view => view.channel.id === channelId)?.channel.config;
        const provider = config.kind;
        const batch = (config.recipients?.length ? config.recipients : [null]).map((_, index) => ({
          id: `fixture-test-${revision++}`, nodeId: 'canary-eu', kind: 'test', provider,
          destinationId: 'f'.repeat(63) + (index + 1), channelId, lastAttemptAt: now(), failures: 0,
          state: testMode === 'mixed' ? index === 0 ? 'accepted' : 'failed' : testMode === 'accepted' ? 'accepted' : 'queued',
          ...(testMode === 'mixed' && index > 0 ? { errorCode: 'recipient_rejected' } : {}),
        }));
        attempts.unshift(...batch);
        // Simulates queueing followed by an unusable acknowledgement, never an external provider send.
        if (testMode === 'malformed') return reply({ fixture: true, acknowledgementUnreadable: true }, 202);
        return reply({ ...(preview ? { preview: true } : {}), attempts: batch }, 202);
      }
      if (url.pathname.startsWith('/api/notification-channels') && ['POST', 'PUT'].includes(req.method)) {
        if (saveMode === 'reject') return reply({ error: 'Visual fixture rejected the save; nothing changed.' }, 400);
        let bytes = ''; for await (const chunk of req) { bytes += chunk; if (bytes.length > 262144) return reply({ error: 'Too large' }, 413); }
        const body = JSON.parse(bytes), id = req.method === 'PUT' ? url.pathname.split('/').at(-1) : `fixture-created-${revision}`;
        const saved = makeChannel(id, body.name, body.config, body);
        saved.channel.revision = `fixture-revision-${revision++}`;
        channels = [...channels.filter(v => v.channel.id !== id), saved];
        if (saveMode === 'malformed') return reply({ fixture: true, acknowledgementUnreadable: true });
        return reply(saved.channel, req.method === 'POST' ? 201 : 200);
      }
      if (url.pathname === '/api/monitors') return reply(overview().monitors);
      if (url.pathname === '/api/notification-deliveries') return role === 'viewer'
        ? reply({ error: 'Viewer access refused by fixture.' }, 403) : deliveriesFail
          ? deliveriesMalformed ? reply({ fixture: true, unreadableDeliveryPayload: true }) : reply({ error: 'The delivery log cannot be read in the private fixture.' }, 503)
          : reply(attempts);
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
