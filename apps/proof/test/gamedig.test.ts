import assert from 'node:assert/strict';
import { spawn, type ChildProcess } from 'node:child_process';
import { once } from 'node:events';
import { createServer } from 'node:http';
import { join } from 'node:path';
import test from 'node:test';
import { Cluster, ROOT } from '../fixtures/cluster.js';

// Actual locked GameDig library and its production service entry point, with
// a loopback Terraria status API. No existing game server or manager is used.
test('GameDig service and Go nodes retain private queries, Pending and HA lifecycle', { timeout: 120000 }, async t => {
  const cluster = new Cluster();
  const serviceToken = 'private-game-service-proof-0123456789';
  const gameToken = 'private-game-target-<&>-proof';
  let gameStatus = '200', requests = 0, leakedServiceCredential = false;
  const game = createServer((request, response) => {
    const url = new URL(request.url || '/', 'http://127.0.0.1');
    assert.equal(url.pathname, '/v2/server/status');
    assert.equal(url.searchParams.get('token'), gameToken);
    if (request.headers.authorization) leakedServiceCredential = true;
    requests++;
    response.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify({
      status: gameStatus, players: [], name: 'private-game-server-name', port: 7777, playercount: 0, maxplayers: 10,
    }));
  });
  game.listen(0, '127.0.0.1'); await once(game, 'listening');
  const gameAddress = game.address(); assert.ok(gameAddress && typeof gameAddress !== 'string');
  const reservation = createServer(); reservation.listen(0, '127.0.0.1'); await once(reservation, 'listening');
  const address = reservation.address(); assert.ok(address && typeof address !== 'string');
  await new Promise<void>(resolve => reservation.close(() => resolve()));
  const servicePort = address.port;
  const endpoint = `http://127.0.0.1:${servicePort}`;
  let manager: ChildProcess | undefined;
  async function stopManager(): Promise<void> {
    const child = manager; manager = undefined;
    if (child && child.exitCode === null && child.signalCode === null) {
      const exited = once(child, 'exit'); child.kill('SIGKILL'); await exited;
    }
  }
  async function startManager(): Promise<void> {
    const environment: NodeJS.ProcessEnv = { CANARY_GAME_QUERY_TOKEN: serviceToken, CANARY_GAME_QUERY_HOST: '127.0.0.1', CANARY_GAME_QUERY_PORT: String(servicePort) };
    for (const key of ['SystemRoot', 'WINDIR', 'TMPDIR', 'TMP', 'TEMP']) if (process.env[key]) environment[key] = process.env[key];
    const child = spawn(process.execPath, ['--max-old-space-size=64', '--permission', '--allow-worker',
      '--allow-fs-read=' + join(ROOT, 'services/game-query'),
      '--allow-fs-read=' + join(ROOT, 'apps/node-go/internal/gamedigmonitor'),
      join(ROOT, 'services/game-query/server.mjs')], { cwd: ROOT, env: environment, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
    manager = child;
    let bytes = 0;
    for (const stream of [child.stdout!, child.stderr!]) stream.on('data', (data: Buffer) => { bytes += data.length; if (bytes > 65536) child.kill('SIGKILL'); });
    await cluster.wait('private GameDig service readiness', async () => {
      assert.equal(child.exitCode, null, 'query service exited during startup');
      assert.equal(child.signalCode, null, 'query service terminated during startup');
      try { const response = await fetch(endpoint + '/readyz', { signal: AbortSignal.timeout(250) }); const value: unknown = await response.json(); return response.status === 200 && value !== null && typeof value === 'object' && 'profile' in value && value.profile === 'gamedig/5.3.3'; }
      catch { return false; }
    }, 10000);
  }
  t.after(async () => {
    await cluster.cleanup(); await stopManager(); game.closeAllConnections();
    await new Promise<void>(resolve => game.close(() => resolve()));
  });
  await cluster.initialize(['game-a', 'game-b']); await cluster.startAll(); await startManager();
  const [a, b] = cluster.nodes; assert.ok(a && b);
  const monitor = await cluster.createMonitor({ name: 'Private game proof', type: 'gamedig', target: '127.0.0.1',
    gamedig: { game: 'terrariatshock', port: gameAddress.port, givenPortOnly: true, token: gameToken, endpoint, bearerToken: serviceToken, allowInsecureHTTP: true },
    internalOnly: true, intervalMs: 2000, timeoutMs: 1600, nodeIds: [a.id, b.id], enabled: true, repeatAfterMs: 0, webhookUrl: cluster.webhookUrl });
  async function settle(state: string, since = Date.now() - 10000): Promise<void> {
    await cluster.wait(`fresh game observers ${state}`, async () => (await cluster.overviews()).every(overview => {
      const view = overview.monitors.find(item => item.monitor.id === monitor.id);
      return view?.observations.length === 2 && view.observations.every(sample => sample.state === state && sample.checkedAt >= since &&
        sample.latencyAvailable === (state === 'up') && (state === 'up' || sample.latencyMs === 0));
    }));
  }
  await settle('up'); assert.ok(requests >= 2);
  for (const node of cluster.nodes) {
    const viewerToken = 'private-game-viewer-proof-0123456789';
    assert.equal((await cluster.api(node, '/api/accounts', { method: 'POST', body: { name: 'Game viewer', role: 'viewer', token: viewerToken } })).status, 201);
    const viewer = await cluster.api<{ monitors: { monitor: { gamedig?: unknown } }[] }>(node, '/api/overview', { token: viewerToken });
    assert.equal(viewer.status, 200); assert.equal(viewer.body.monitors[0]?.monitor.gamedig, undefined);
    for (const secret of [serviceToken, gameToken, 'private-game-server-name']) assert.equal(viewer.text.includes(secret), false);
  }
  let since = Date.now(); gameStatus = 'error'; await settle('down', since);
  await cluster.wait('game failure is delivered', () => cluster.alerts().some(alert => alert.monitorId === monitor.id && alert.kind === 'down'));
  const original = (await cluster.overview(a)).incidents.find(item => item.monitorId === monitor.id && item.resolvedAt === null);
  assert.ok(original);
  since = Date.now(); await stopManager(); await settle('pending', since);
  await cluster.stop(b); await cluster.start(b); since = Date.now(); await settle('pending', since);
  for (const overview of await cluster.overviews()) assert.ok(overview.incidents.some(item => item.monitorId === monitor.id && item.openedAt === original.openedAt && item.resolvedAt === null));
  assert.equal(cluster.alerts().some(alert => alert.monitorId === monitor.id && alert.kind === 'recovery'), false);
  gameStatus = '200'; await startManager(); since = Date.now(); await settle('up', since);
  await cluster.wait('the retained game outage resolves and delivers recovery', async () =>
    (await cluster.overviews()).every(overview => overview.incidents.some(item => item.monitorId === monitor.id && item.openedAt === original.openedAt && item.resolvedAt !== null) &&
      !overview.incidents.some(item => item.monitorId === monitor.id && item.resolvedAt === null)) &&
    cluster.alerts().some(alert => alert.monitorId === monitor.id && alert.kind === 'recovery'));
  assert.equal(leakedServiceCredential, false);
});
