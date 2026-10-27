import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:net';
import test from 'node:test';
import { createQueryServer, decode, PROFILE } from './server.mjs';
import catalogue from '../../apps/node-go/internal/gamedigmonitor/catalogue.json' with { type: 'json' };

const token = 'private-game-query-fixture-0123456789';
const base = { id: 'a'.repeat(32), game: 'minecraft', host: '127.0.0.1', port: 25565, givenPortOnly: true, timeoutMs: 3000 };
function vi(n) { const bytes = []; do { let b = n & 127; n >>>= 7; if (n) b |= 128; bytes.push(b); } while (n); return Buffer.from(bytes); }

async function service(t) {
  const server = createQueryServer(token);
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => server.shutdown());
  const origin = `http://127.0.0.1:${server.address().port}`;
  return { origin, async query(input, signal) {
    const response = await fetch(origin + '/v1/query', { method: 'POST', headers: { Authorization: 'Bearer ' + token, 'Content-Type': 'application/json' }, body: JSON.stringify(input), signal });
    return { status: response.status, body: await response.json() };
  } };
}

async function minecraft(t, mode) {
  const sockets = new Set(); let connections = 0, firstRequestAt = null;
  const server = createServer(socket => {
    connections++; sockets.add(socket); socket.once('close', () => sockets.delete(socket)); socket.on('error', () => {});
    let bytes = 0;
    socket.once('data', input => {
      firstRequestAt ??= performance.now();
      bytes += input.length; assert.ok(bytes <= 1024 && input.length > 7, 'bounded Minecraft status handshake required');
      if (mode.value === 'stall') return;
      if (mode.value === 'bad') { socket.end(Buffer.from([8, 0, 6, ...Buffer.from('broken')])); return; }
      const json = Buffer.from(JSON.stringify({ version: { name: 'private-fixture', protocol: 47 }, players: { max: 10, online: 1 }, description: { text: 'private-server-name-never-exposed' } }));
      const body = Buffer.concat([Buffer.from([0]), vi(json.length), json]);
      socket.end(Buffer.concat([vi(body.length), body]));
    });
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(async () => { for (const socket of sockets) socket.destroy(); await new Promise(resolve => server.close(resolve)); });
  return { port: server.address().port, sockets, get connections() { return connections; }, get firstRequestAt() { return firstRequestAt; } };
}

test('exact primitive query admission rejects ambiguity, malformed Unicode and unsupported options', () => {
  assert.deepEqual(decode(Buffer.from(JSON.stringify(base))), base);
  const missingDefault = catalogue.games.find(game => !game.defaultPort);
  assert.ok(missingDefault, 'missing-default negative control required');
  assert.throws(() => decode(Buffer.from(JSON.stringify({ ...base, game: missingDefault.id, port: 0, givenPortOnly: false }))));
  assert.equal(decode(Buffer.from(JSON.stringify({ ...base, game: missingDefault.id, port: 12345 }))).port, 12345);
  for (const suffix of [',"id":"' + 'b'.repeat(32) + '"}', ',"\\u0069d":"' + 'a'.repeat(32) + '"}', ',"Host":"127.0.0.1"}', ',"command":"arbitrary"}', ',"token":"\\ud800"}']) {
    const raw = JSON.stringify(base).slice(0, -1) + suffix;
    assert.throws(() => decode(Buffer.from(raw)));
  }
  for (const value of [{ ...base, id: [base.id] }, { ...base, game: 'protocol-minecraftvanilla' }, { ...base, host: '--help' }, { ...base, port: null }, { ...base, timeoutMs: 90001 }, { ...base, token: null }]) assert.throws(() => decode(Buffer.from(JSON.stringify(value))));
});

test('actual locked GameDig queries private Minecraft and returns only fixed evidence', { timeout: 10000 }, async t => {
  const fixture = await minecraft(t, { value: 'ok' }), api = await service(t);
  const started = performance.now();
  const result = await api.query({ ...base, port: fixture.port });
  t.diagnostic(JSON.stringify({ budgetMs: base.timeoutMs, elapsedMs: Math.round(performance.now() - started),
    firstTCPMs: fixture.firstRequestAt === null ? null : Math.round(fixture.firstRequestAt - started), connections: fixture.connections }));
  assert.equal(result.status, 200); assert.equal(result.body.profile, PROFILE); assert.equal(result.body.id, base.id); assert.equal(result.body.state, 'up');
  assert.ok(Number.isFinite(result.body.latencyMs) && result.body.latencyMs >= 0);
  assert.deepEqual(Object.keys(result.body).sort(), ['id', 'latencyMs', 'profile', 'state']);
  assert.equal(JSON.stringify(result).includes('private-server-name'), false);
  assert.ok(fixture.connections >= 1);
});

test('query service never accepts a browser Origin or an absent credential', async t => {
  const api = await service(t);
  for (const headers of [{}, { Authorization: 'Bearer ' + token, Origin: 'https://untrusted.example' }]) {
    const response = await fetch(api.origin + '/v1/query', { method: 'POST', headers: { 'Content-Type': 'application/json', ...headers }, body: JSON.stringify(base) });
    assert.equal(response.status, 401);
  }
});

test('short-budget Minecraft keeps a successful TCP reply while silent UDP attempts settle', { timeout: 10000 }, async t => {
  const fixture = await minecraft(t, { value: 'ok' }), api = await service(t);
  const result = await api.query({ ...base, port: fixture.port, timeoutMs: 1500 });
  assert.equal(result.status, 200);
  assert.equal(result.body.state, 'up');
  assert.ok(fixture.connections >= 1, 'the real vanilla query must reach the owned TCP server');
  assert.ok(Number.isFinite(result.body.latencyMs) && result.body.latencyMs >= 0);
  assert.deepEqual(Object.keys(result.body).sort(), ['id', 'latencyMs', 'profile', 'state']);
});

test('canceling one of two occupied workers joins it before replacement admission', { timeout: 15000 }, async t => {
  const mode = { value: 'stall' }, fixture = await minecraft(t, mode), api = await service(t);
  const controllers = [new AbortController(), new AbortController()];
  const pending = controllers.map(controller => api.query({ ...base, port: fixture.port, timeoutMs: 8000 }, controller.signal).catch(() => null));
  const deadline = Date.now() + 3000;
  while (fixture.connections < 2) { assert.ok(Date.now() < deadline, 'both workers must actually reach query I/O'); await new Promise(resolve => setTimeout(resolve, 10)); }
  assert.equal((await api.query({ ...base, port: fixture.port })).status, 503);
  controllers[0].abort(); await pending[0];
  const released = Date.now() + 1500;
  while (fixture.sockets.size > 1) { assert.ok(Date.now() < released, 'canceled query socket survived its worker'); await new Promise(resolve => setTimeout(resolve, 10)); }
  mode.value = 'ok';
  const replacement = await api.query({ ...base, port: fixture.port });
  assert.equal(replacement.status, 200); assert.equal(replacement.body.state, 'up');
  controllers[1].abort(); await pending[1];
});
