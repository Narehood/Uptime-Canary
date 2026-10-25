// Actual private Chromium plus the production Go executable, not a CDP mock.
import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import { setTimeout as delay } from 'node:timers/promises';
import test from 'node:test';
import { Cluster } from '../apps/proof/fixtures/cluster.ts';

const endpoint = process.env.BROWSER_FIXTURE_ENDPOINT;
assert.ok(endpoint && /^ws:\/\/127\.0\.0\.1:\d+\//u.test(endpoint), 'owned browser fixture endpoint required');

async function contexts() {
  const ws = new WebSocket(endpoint);
  const timer = setTimeout(() => ws.close(), 2000);
  try {
    await once(ws, 'open');
    const response = new Promise((resolve, reject) => {
      ws.addEventListener('message', event => {
        try {
          assert.ok(typeof event.data === 'string' && event.data.length < 8192);
          const value = JSON.parse(event.data);
          if (value.id === 1) { assert.ok(Array.isArray(value.result?.browserContextIds)); resolve(value.result.browserContextIds.length); }
        } catch (error) { reject(error); }
      });
      ws.addEventListener('error', () => reject(new Error('private browser context query failed')));
      ws.addEventListener('close', () => reject(new Error('private browser context query closed')));
    });
    ws.send(JSON.stringify({ id: 1, method: 'Target.getBrowserContexts', params: {} }));
    return await response;
  } finally { clearTimeout(timer); ws.close(); }
}

test('actual Chromium navigates production HA checks, retains Pending outages and keeps captures private', { timeout: 120000 }, async t => {
  const cluster = new Cluster();
  let mode = 'up', rendered = 0, requests = 0, reusedCookies = 0, inContextCookies = 0, leakedAuthorization = 0;
  const target = createServer((request, response) => {
    requests++;
    if (request.headers.authorization) leakedAuthorization++;
    // Favicon/subresource cookies belong to this check's existing context.
    // Isolation is measured on the initial /page request of each new check.
    if (request.url === '/rendered') { rendered++; if (request.headers.cookie) inContextCookies++; response.writeHead(204).end(); return; }
    if (request.url === '/favicon.ico') { response.writeHead(204).end(); return; }
    assert.equal(request.url, '/page');
    if (request.headers.cookie) reusedCookies++;
    if (mode === 'pending') {
      response.writeHead(200, { 'Content-Type': 'text/html' }); response.write('<!doctype html><p>Loading'); return;
    }
    response.writeHead(mode === 'down' ? 503 : 200, { 'Content-Type': 'text/html', 'Set-Cookie': 'canary_fixture=private; SameSite=Strict' });
    response.end('<!doctype html><title>Private Canary fixture</title><p id="result">Private browser page</p><script>document.querySelector("#result").textContent="JavaScript rendered";fetch("/rendered");</script>');
  });
  target.listen(0, '127.0.0.1'); await once(target, 'listening');
  const address = target.address(); assert.ok(address && typeof address !== 'string');
  let cleaned = false;
  t.after(async () => {
    if (!cleaned) await cluster.cleanup();
    target.closeAllConnections(); await new Promise(resolve => target.close(resolve));
  });
  assert.equal(await contexts(), 0, 'fixture must start with no private browser contexts');
  await cluster.initialize(['browser-a', 'browser-b']); await cluster.startAll();
  const [a, b] = cluster.nodes; assert.ok(a && b);
  const monitor = await cluster.createMonitor({ name: 'Private browser proof', type: 'real-browser',
    target: `http://127.0.0.1:${address.port}/page`, browser: { endpoint, bearerToken: 'private-browser-fixture-token', screenshot: true, delayMs: 25 },
    intervalMs: 2000, timeoutMs: 1600, nodeIds: [a.id, b.id], enabled: true, repeatAfterMs: 0,
    webhookUrl: cluster.webhookUrl });
  async function settle(state, since = Date.now() - 10000) {
    await cluster.wait(`fresh browser observers ${state}`, async () => (await cluster.overviews()).every(overview => {
      const view = overview.monitors.find(item => item.monitor.id === monitor.id);
      return view?.observations.length === 2 && view.observations.every(sample => sample.state === state && sample.checkedAt >= since &&
        (state === 'pending' ? sample.latencyAvailable === false && sample.latencyMs === 0 : sample.latencyAvailable === true && sample.latencyMs > 0));
    }));
  }
  await settle('up'); assert.ok(rendered >= 2, 'actual JavaScript must execute in both private pages');
  for (const node of cluster.nodes) {
    const capture = await cluster.api(node, `/api/monitors/${monitor.id}/screenshot`);
    assert.equal(capture.status, 200); assert.equal(capture.body.nodeId, node.id);
    assert.equal(capture.body.mimeType, 'image/png');
    const png = Buffer.from(capture.body.data, 'base64');
    assert.ok(png.length > 32 && png.length <= 192 * 1024); assert.equal(png.subarray(0, 8).toString('hex'), '89504e470d0a1a0a');
    const token = 'viewer-browser-proof-0123456789abcdef';
    assert.equal((await cluster.api(node, '/api/accounts', { method: 'POST', body: { name: 'Browser viewer', role: 'viewer', token } })).status, 201);
    const viewer = await cluster.api(node, '/api/overview', { token });
    assert.equal(viewer.status, 200); assert.equal(viewer.body.monitors.find(item => item.monitor.id === monitor.id)?.monitor.browser, undefined);
    assert.equal(viewer.text.includes('private-browser-fixture-token'), false);
    assert.equal((await cluster.api(node, `/api/monitors/${monitor.id}/screenshot`, { token })).status, 403);
  }
  let since = Date.now(); mode = 'down'; await settle('down', since);
  await cluster.wait('actual browser outage is delivered', () => cluster.alerts().some(alert => alert.monitorId === monitor.id && alert.kind === 'down'));
  const opened = (await cluster.overview(a)).incidents.find(item => item.monitorId === monitor.id && item.resolvedAt === null);
  assert.ok(opened, 'an actual HTTP error must create an incident');
  since = Date.now(); mode = 'pending'; await settle('pending', since);
  const retained = async () => {
    for (const overview of await cluster.overviews()) assert.ok(overview.incidents.some(item => item.monitorId === monitor.id && item.openedAt === opened.openedAt && item.resolvedAt === null), 'Pending must retain the actual outage');
    assert.equal(cluster.alerts().some(alert => alert.monitorId === monitor.id && alert.kind === 'recovery'), false);
  };
  await retained();
  await cluster.stop(b); await cluster.start(b);
  since = Date.now(); await settle('pending', since); await retained();
  since = Date.now(); mode = 'up'; await settle('up', since);
  await cluster.wait('original browser outage remains present and resolves', async () => (await cluster.overviews()).every(overview =>
    overview.incidents.some(item => item.monitorId === monitor.id && item.openedAt === opened.openedAt && item.resolvedAt !== null) &&
    !overview.incidents.some(item => item.monitorId === monitor.id && item.resolvedAt === null)));
  await cluster.wait('browser recovery is delivered', () => cluster.alerts().some(alert => alert.monitorId === monitor.id && alert.kind === 'recovery'));
  assert.equal(reusedCookies, 0, 'each check starts its navigation in a fresh private context');
  assert.equal(inContextCookies, rendered, 'rendered subrequests retain the current context cookie');
  assert.equal(leakedAuthorization, 0, 'browser-service credential must never go to the monitored target');
  assert.ok(requests >= 6 && rendered >= 4);
  await cluster.cleanup(); cleaned = true;
  const deadline = Date.now() + 2500;
  while (await contexts()) { assert.ok(Date.now() < deadline, 'private browser contexts survived stopped nodes'); await delay(25); }
  t.diagnostic(JSON.stringify({ rendered, requests, initialNavigationCookies: reusedCookies, inContextCookies, leakedAuthorization, remainingBrowserContexts: 0 }));
});
