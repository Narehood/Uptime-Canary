import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:https';
import type { Duplex } from 'node:stream';
import test from 'node:test';
import { Cluster } from '../fixtures/cluster.js';

test('compiled peers obtain OAuth grants and evaluate real JSONata responses with connected alert deduplication', { timeout: 90_000 }, async t => {
  const cluster = new Cluster();
  t.after(async () => cluster.cleanup());
  const certificate = await readFile(new URL('../fixtures/localhost-cert.pem', import.meta.url), 'utf8');
  const clientSecret = cluster.secret();
  const bearer = cluster.secret();
  let ready = true;
  let grants = 0;
  let checks = 0;
  const sockets = new Set<Duplex>();
  const server = createServer({ cert: certificate, key: await readFile(new URL('../fixtures/localhost-key.pem', import.meta.url)) }, (req, res) => {
    if (req.url === '/token') {
      assert.equal(req.method, 'POST');
      assert.equal(req.headers.authorization, undefined, 'post credentials stay in the grant body');
      let body = '';
      req.setEncoding('utf8');
      req.on('data', chunk => { body += chunk; });
      req.on('end', () => {
        const form = new URLSearchParams(body);
        assert.equal(form.get('grant_type'), 'client_credentials');
        assert.equal(form.get('client_id'), 'proof-client');
        assert.equal(form.get('client_secret'), clientSecret);
        grants++;
        res.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify({ token_type: 'Bearer', access_token: bearer }));
      });
      return;
    }
    if (req.url !== '/service') { res.writeHead(404).end(); return; }
    assert.equal(req.headers.authorization, `Bearer ${bearer}`);
    checks++;
    res.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify({ items: [{ ready }, { ready }] }));
  });
  server.on('connection', socket => { sockets.add(socket); socket.on('close', () => sockets.delete(socket)); });
  t.after(async () => {
    const closed = new Promise<void>(resolve => server.close(() => resolve()));
    for (const socket of sockets) socket.destroy();
    server.closeAllConnections();
    await closed;
  });
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => { server.off('error', reject); resolve(); });
  });
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  const base = `https://127.0.0.1:${address.port}`;
  await cluster.initialize(['query-a', 'query-b']);
  await cluster.startAll();
  const monitor = await cluster.createMonitor({
    name: 'OAuth JSONata process proof', type: 'http', target: `${base}/service`,
    intervalMs: 3_000, timeoutMs: 3_000, nodeIds: cluster.nodes.map(n => n.id), enabled: true,
    webhookUrl: cluster.webhookUrl, repeatAfterMs: 0,
    tls: { caPem: certificate },
    http: {
      oauth2: { tokenUrl: `${base}/token`, clientId: 'proof-client', clientSecret, authMethod: 'client_secret_post', tokenCaPem: certificate },
      jsonata: { expression: '$count(items[ready = true])', operator: '==', expected: 2 },
    },
  });
  const both = async (state: 'up' | 'down') => (await cluster.overviews()).every(o => {
    const view = o.monitors.find(v => v.monitor.id === monitor.id);
    return view?.state === state && view.unknownNodes === 0;
  });
  await cluster.wait('both independent runtimes dispatch successful JSONata workers', () => both('up'));
  assert.ok(checks >= 2 && grants >= checks, 'real grant and service requests must occur');
  ready = false;
  await cluster.wait('query mismatch becomes down despite HTTP 200', () => both('down'));
  await cluster.wait('one connected query outage webhook', () => cluster.alerts().length === 1);
  assert.equal(cluster.alerts()[0]!.kind, 'down');
  await cluster.quiet('query outage receipts deduplicate peers', 1);
  ready = true;
  await cluster.wait('actual query result recovers both peers', () => both('up'));
  await cluster.wait('one connected query recovery webhook', () => cluster.alerts().length === 2);
  assert.equal(cluster.alerts()[1]!.kind, 'recovery');
  assert.equal(cluster.alerts()[1]!.incidentId, cluster.alerts()[0]!.incidentId);
  await cluster.quiet('query recovery settles', 2);
  for (const node of cluster.nodes) {
    const history = await cluster.history(node, monitor.id);
    assert.ok(history.some(o => o.state === 'down') && history.some(o => o.state === 'up'));
    const serialized = JSON.stringify(history);
    assert.ok(!serialized.includes(clientSecret) && !serialized.includes(bearer), 'history cannot expose grant credentials or access tokens');
  }
});
