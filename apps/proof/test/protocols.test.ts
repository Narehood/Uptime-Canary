import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:https';
import type { Duplex } from 'node:stream';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import type { MonitorInput } from '../../../packages/shared/src/index.js';
import { Cluster, QUIET_MS } from '../fixtures/cluster.js';

test('standalone runtime performs trusted local HTTPS and real loopback ICMP checks without peers or a UI', {
  timeout: 90_000,
}, async (t) => {
  // This public fixture key is only for an ephemeral loopback HTTPS server.
  // The certificate covers localhost/127.0.0.1 and is valid from 2020 to 2099.
  const certificatePath = fileURLToPath(new URL('../fixtures/localhost-cert.pem', import.meta.url));
  // The Go process must load this PEM trust bundle, including on Windows.
  // No certificate-validation bypass or machine-wide trust-store change is used.
  const cluster = new Cluster({ SSL_CERT_FILE: certificatePath });
  t.after(async () => cluster.cleanup());
  let status = 200;
  let secureRequests = 0;
  const sockets = new Set<Duplex>();
  const server = createServer({
    cert: await readFile(certificatePath),
    key: await readFile(new URL('../fixtures/localhost-key.pem', import.meta.url)),
  }, (req, res) => {
    if (req.url !== '/service') { res.writeHead(404).end(); return; }
    secureRequests++;
    res.writeHead(status, { 'Content-Type': 'text/plain' }).end(String(status));
  });
  server.on('connection', (socket) => {
    sockets.add(socket);
    socket.on('close', () => sockets.delete(socket));
  });
  t.after(async () => {
    const closed = new Promise<void>((resolveClose) => server.close(() => resolveClose()));
    for (const socket of sockets) socket.destroy();
    server.closeAllConnections();
    await closed;
  });

  try {
    await new Promise<void>((resolveListen, reject) => {
      server.once('error', reject);
      server.listen(0, '127.0.0.1', () => { server.off('error', reject); resolveListen(); });
    });
    const address = server.address();
    assert.ok(address && typeof address !== 'string');
    await cluster.initialize(['proof-solo']);
    await cluster.startAll();
    const node = cluster.nodes[0]!;
    const input: MonitorInput = {
      name: 'Standalone HTTPS proof', type: 'http', target: `https://127.0.0.1:${address.port}/service`,
      internalOnly: true, intervalMs: 1_000, timeoutMs: 750, nodeIds: [node.id], enabled: true,
      webhookUrl: cluster.webhookUrl, repeatAfterMs: 0,
    };
    const httpsMonitor = await cluster.createMonitor(input);
    await cluster.wait('standalone HTTPS success using a trusted loopback certificate', async () =>
      (await cluster.history(node, httpsMonitor.id)).some((observation) => observation.state === 'up'));
    assert.ok(secureRequests > 0, 'the actual TLS fixture must receive checks');

    const pingInput: MonitorInput = {
      name: 'Real ICMP loopback proof', type: 'ping', target: '127.0.0.1',
      internalOnly: true, intervalMs: 1_000, timeoutMs: 1_000, nodeIds: [node.id], enabled: true, repeatAfterMs: 0,
    };
    const pingMonitor = await cluster.createMonitor(pingInput);
    await cluster.wait('real system ping to 127.0.0.1 succeeds (ping must be installed and ICMP permitted)', async () =>
      (await cluster.history(node, pingMonitor.id)).some((observation) => observation.state === 'up'));
    for (const target of ['--help', '-n 2 127.0.0.1', '127.0.0.1; whoami', 'https://127.0.0.1']) {
      const rejected = await cluster.api(node, '/api/monitors', { method: 'POST', body: { ...pingInput, target } });
      assert.equal(rejected.status, 400, 'ping targets must reject command switches, shell fragments, and URL schemes');
    }

    const outageStart = cluster.events.length;
    status = 503;
    await cluster.wait('standalone HTTPS outage sends a real webhook', () => cluster.alerts(outageStart).some((alert) =>
      alert.monitorId === httpsMonitor.id && alert.kind === 'down' && alert.nodeId === node.id));
    assert.equal(cluster.events.length, outageStart + 1);
    const down = cluster.alerts(outageStart)[0]!;
    await cluster.quiet('standalone HTTPS outage settles', outageStart + 1);

    const recoveryStart = cluster.events.length;
    status = 200;
    await cluster.wait('standalone HTTPS recovery sends a real webhook', () => cluster.alerts(recoveryStart).some((alert) =>
      alert.monitorId === httpsMonitor.id && alert.kind === 'recovery' && alert.nodeId === node.id));
    assert.equal(cluster.events.length, recoveryStart + 1);
    const recovery = cluster.alerts(recoveryStart)[0]!;
    assert.equal(recovery.incidentId, down.incidentId);
    assert.notEqual(recovery.event.idempotencyKey, down.event.idempotencyKey);
    await cluster.quiet('standalone HTTPS recovery settles', recoveryStart + 1);

    const overview = await cluster.overview(node);
    assert.equal(overview.nodes.length, 1, 'the process is standalone');
    assert.equal(overview.nodes[0]!.state, 'local');
    for (const monitor of [httpsMonitor, pingMonitor]) {
      const history = await cluster.history(node, monitor.id);
      assert.ok(history.length > 0);
      assert.ok(history.every((observation) => observation.nodeId === node.id && observation.monitorId === monitor.id));
      assert.ok(history.every((observation) => Number.isFinite(observation.latencyMs) && observation.latencyMs >= 0));
    }
    t.diagnostic(`Actual loopback HTTPS requests: ${secureRequests}; ICMP observations came from a separate runtime process. Quiescence windows: ${QUIET_MS}ms.`);
  } catch (error) {
    throw new Error(cluster.redact(`${error instanceof Error ? error.stack ?? error.message : String(error)}\n${cluster.diagnostics()}`));
  }
});
