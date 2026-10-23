import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import test from 'node:test';
import { Cluster } from '../fixtures/cluster.js';
import {
  assertSafe, attempts, channelsReplicated, createChannel, monitorInput, observed, stable,
  type Channel, type ChannelInput, type DeliveryAttempt,
} from '../fixtures/notifications.js';

test('messaging channels: real peers preserve native receipts, fail over and retain provider retry deadlines', {
  timeout: 150_000,
}, async t => {
  const cluster = new Cluster();
  const key = cluster.secret();
  const topic = 'proof-' + key.slice(0, 12);
  const events: { provider: string; event: string; retry: boolean; at: number }[] = [];
  const errors: string[] = [];
  let rateLimited = false;
  const sink = createServer((req, res) => {
    void (async () => {
      const chunks: Buffer[] = [];
      let bytes = 0;
      for await (const chunk of req) {
        bytes += chunk.length;
        assert.ok(bytes <= 65_536);
        chunks.push(Buffer.from(chunk));
      }
      assert.equal(req.method, 'POST');
      assert.equal(req.headers['idempotency-key'], undefined, 'unsupported remote idempotency must not be invented');
      const raw = Buffer.concat(chunks).toString('utf8');
      const form = new URLSearchParams(raw);
      const body: Record<string, unknown> = req.headers['content-type']?.startsWith('application/json') ? JSON.parse(raw) : {};
      const path = req.url ?? '';
      const provider = path.startsWith('/services/') ? 'slack' : path.split('/')[1]!;
      const text = provider === 'pushover' ? form.get('message')! : String(body.text ?? body.message);
      const event = /\[event:(down|recovery)\]/u.exec(text)?.[1];
      assert.ok(event, 'each native payload must contain its rendered event kind');
      const retry = text.includes('Retry persistence');
      events.push({ provider, event, retry, at: Date.now() });
      res.setHeader('Content-Type', 'application/json');
      if (provider === 'ntfy' && retry && !rateLimited) {
        rateLimited = true;
        res.writeHead(429, { 'Retry-After': '8' }).end(JSON.stringify({ error: key }));
        return;
      }
      switch (provider) {
        case 'slack': res.end('ok'); break;
        case 'telegram': res.end('{"ok":true,"result":{"message_id":1,"chat":{"id":12345}}}'); break;
        case 'ntfy': res.end(JSON.stringify({ id: 'Abcdef123456', event: 'message', topic })); break;
        case 'gotify': res.end('{"id":1,"appid":2,"message":"fixture","date":"2026-10-22T00:00:00Z"}'); break;
        case 'pushover': res.end('{"status":1,"request":"647d2300-702c-4b38-8b2f-d56326ae460b"}'); break;
        default: throw Error('Unexpected native provider route');
      }
    })().catch(() => {
      errors.push('Local provider rejected the request');
      if (!res.headersSent) res.writeHead(400);
      res.end('{}');
    });
  });
  t.after(async () => {
    await cluster.cleanup();
    sink.closeAllConnections();
    await new Promise<void>(resolve => sink.close(() => resolve()));
  });
  await cluster.initialize(['messaging-a', 'messaging-b']);
  await new Promise<void>(resolve => sink.listen(0, '127.0.0.1', resolve));
  const address = sink.address(); assert.ok(address && typeof address !== 'string');
  const base = `http://127.0.0.1:${address.port}`;
  await cluster.startAll();
  const [a, b] = cluster.nodes; assert.ok(a && b);
  const configs: ChannelInput['config'][] = [
    { kind: 'slack', slack: { webhookURL: `${base}/services/TTEST/BTEST/${key}`, allowInsecureHTTP: true } },
    { kind: 'telegram', telegram: { botToken: '12345:' + key, chatId: '12345', baseURL: base + '/telegram', allowInsecureHTTP: true } },
    { kind: 'ntfy', ntfy: { topic, token: key, baseURL: base + '/ntfy', allowInsecureHTTP: true } },
    { kind: 'gotify', gotify: { appToken: key, baseURL: base + '/gotify', allowInsecureHTTP: true } },
    { kind: 'pushover', pushover: { appToken: key.slice(0, 30), userKey: key.slice(-30), baseURL: base + '/pushover', allowInsecureHTTP: true } },
  ];
  const channels: Channel[] = [];
  for (const config of configs) channels.push(await createChannel(cluster, {
    name: 'Local ' + config.kind, enabled: true,
    config: { ...config, templates: { text: '[event:{{event_kind}}] {{monitor_name}}' } },
  }));
  await channelsReplicated(cluster, channels);
  const monitor = await cluster.createMonitor(monitorInput(cluster, 'Native failover', channels.map(v => v.id)));
  await observed(cluster, monitor.id, 'up');
  const receiptCount = async (id: string, count: number) => (await cluster.overviews()).every(v =>
    v.notifications.filter(r => r.monitorId === id).length === count);
  const count = () => events.filter(v => !v.retry).length;
  const noDuplicates = async (wanted: number) => stable(cluster, 'every native destination stays quiet after receipt convergence', () => {
    assert.deepEqual(errors, []);
    assert.equal(count(), wanted);
  });
  cluster.targetState = 'down';
  await cluster.wait('five native down receipts replicate through real private peer HTTP', () => receiptCount(monitor.id, 5));
  assert.equal(count(), 5);
  const first = (await cluster.overview(b)).notifications.filter(r => r.monitorId === monitor.id);
  assert.equal(new Set(first.map(r => r.destinationId)).size, 5);
  assert.deepEqual(new Set(first.map(r => r.provider)), new Set(configs.map(v => v.kind)));
  await noDuplicates(5);
  cluster.targetState = 'up';
  await cluster.wait('five native recoveries replicate', () => receiptCount(monitor.id, 10));
  assert.equal(count(), 10);
  const beforeFailure = new Set((await cluster.overview(b)).notifications.filter(r => r.monitorId === monitor.id).map(r => r.id));

  await cluster.stop(a);
  cluster.targetState = 'down';
  await cluster.wait('surviving process accepts all five destinations', async () =>
    (await cluster.overview(b)).notifications.filter(r => r.monitorId === monitor.id).length === 15);
  assert.equal(count(), 15);
  const failoverReceipts = (await cluster.overview(b)).notifications.filter(r => r.monitorId === monitor.id && !beforeFailure.has(r.id));
  assert.equal(failoverReceipts.length, 5);
  assert.ok(failoverReceipts.every(r => r.nodeId === b.id && r.kind === 'down'));
  await cluster.start(a);
  await observed(cluster, monitor.id, 'down');
  await noDuplicates(15);
  cluster.targetState = 'up';
  await cluster.wait('restarted sender exchanges exact destination recoveries', () => receiptCount(monitor.id, 20));
  assert.equal(count(), 20);

  // This monitor is assigned only to A, so a different process's independent
  // retry policy cannot obscure whether A retained its own Retry-After deadline.
  const target = await cluster.createTarget();
  const retryMonitor = await cluster.createMonitor({
    ...monitorInput(cluster, 'Retry persistence', channels.map(v => v.id), target), nodeIds: [a.id],
  });
  await observed(cluster, retryMonitor.id, 'up', [a]);
  target.state = 'down';
  const pending = await (async (): Promise<DeliveryAttempt> => {
    let found: DeliveryAttempt | undefined;
    await cluster.wait('native ntfy rate limit and independent acceptances persist', async () => {
      const values = await attempts(cluster, a, `monitorId=${retryMonitor.id}`);
      found = values.find(v => v.provider === 'ntfy' && v.httpStatus === 429 && v.state === 'retrying');
      return Boolean(found && values.filter(v => v.state === 'accepted').length === 4);
    });
    assert.ok(found); return found;
  })();
  assert.ok(pending.nextAttemptAt! >= pending.lastAttemptAt! + 8000);
  await cluster.stop(a); await cluster.start(a);
  const restored = (await attempts(cluster, a, `monitorId=${retryMonitor.id}`)).find(v => v.id === pending.id)!;
  assert.equal(restored.nextAttemptAt, pending.nextAttemptAt);
  assert.equal(restored.failures, pending.failures);
  await stable(cluster, 'native retry cannot run before its durable deadline', () => {
    assert.equal(events.filter(v => v.retry && v.provider === 'ntfy').length, 1);
  }, Math.max(1, pending.nextAttemptAt! - Date.now() - 200));
  await cluster.wait('only the pending native destination retries', async () =>
    (await attempts(cluster, a, `monitorId=${retryMonitor.id}`)).filter(v => v.state === 'accepted').length === 5);
  const retryEvents = events.filter(v => v.retry);
  assert.equal(retryEvents.length, 6);
  assert.ok(retryEvents.filter(v => v.provider === 'ntfy')[1]!.at >= pending.nextAttemptAt!);
  for (const provider of configs.map(v => v.kind).filter(v => v !== 'ntfy')) assert.equal(retryEvents.filter(v => v.provider === provider).length, 1);
  const safe = await cluster.api(a, '/api/notification-deliveries');
  assertSafe(safe.text, [key, topic, base, key.slice(0, 30), key.slice(-30)]);
  assert.deepEqual(errors, []);
  t.diagnostic('Five real native loopback wire formats, connected receipts, SIGKILL/restart failover and persisted native Retry-After; no external providers.');
});
