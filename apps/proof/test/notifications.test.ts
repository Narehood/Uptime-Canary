import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import test from 'node:test';
import { setTimeout as delay } from 'node:timers/promises';
import { Cluster, QUIET_MS } from '../fixtures/cluster.js';
import {
  ProviderSink, SMTPSink, assertSafe, attempts, channelsReplicated, createChannel, emailInput,
  monitorInput, observed, stable, webhookInput,
  type Channel, type ChannelInput, type ChannelView, type DeliveryAttempt, type ProviderRequest,
} from '../fixtures/notifications.js';

const hash = (value: string) => `sha256:${createHash('sha256').update(value).digest('hex')}`;
const providerID = 'opaque-local-provider-id@example.test';
const privateAddress = 'canary@example.test';

test('notification providers: real local mail acceptance, independent recipient retries and persisted Retry-After identities', {
  timeout: 100_000,
}, async t => {
  const cluster = new Cluster();
  const smtp = new SMTPSink();
  const key = cluster.secret();
  const counts = new Map<string, number>();
  const sink: ProviderSink = new ProviderSink(request => {
    assert.equal(request.method, 'POST');
    if (request.path.startsWith('/mailgun-')) {
      assert.ok(['/mailgun-us/v3/proof.example.test/messages', '/mailgun-eu/v3/proof.example.test/messages'].includes(request.path));
      assert.equal(request.headers.authorization, `Basic ${Buffer.from(`api:${key}`).toString('base64')}`);
      assert.deepEqual(request.fields.to, ['<alpha@example.test>']);
      assert.match(request.fields.from![0]!, /Local Canary.*<canary@example.test>/);
      assert.match(request.fields['v:canary-idempotency-key']![0]!, /^[a-f0-9]{64}$/);
      assert.match(request.fields.text![0]!, /Mail integration/);
      return { status: 200, body: { id: providerID } };
    }
    let recipient: string;
    let subject: string;
    let identity: string;
    if (request.path === '/brevo/v3/smtp/email') {
      assert.equal(request.headers['api-key'], key);
      assert.equal(request.body.to.length, 1, 'runtime must split email recipients into independently retried sends');
      assert.equal(request.body.sender.email, privateAddress);
      recipient = request.body.to[0].email;
      subject = request.body.subject;
      identity = request.body.headers['X-Canary-Idempotency-Key'];
    } else {
      assert.equal(request.path, '/mandrill/api/1.0/messages/send');
      assert.equal(request.body.key, key);
      assert.equal(request.body.async, false);
      assert.equal(request.body.message.to.length, 1);
      recipient = request.body.message.to[0].email;
      subject = request.body.message.subject;
      identity = request.body.message.metadata.canary_idempotency_key;
    }
    assert.match(identity, /^[a-f0-9]{64}$/);
    const counter = `${request.path}/${recipient}/${subject}`;
    const count = (counts.get(counter) ?? 0) + 1;
    counts.set(counter, count);
    if (subject.startsWith('down') && recipient === 'beta@example.test' && count === 1) {
      if (request.path.startsWith('/brevo')) return {
        status: 429, headers: { 'Retry-After': '8' },
        body: { message: `private provider body ${key} ${recipient} ${sink.url}` },
      };
      // A rejection applies only to this destination. Accepted alpha must never be resent.
      return { status: 200, body: [{ email: recipient, status: 'rejected', reject_reason: key }] };
    }
    return request.path.startsWith('/brevo')
      ? { status: 201, body: { messageId: providerID } }
      : { status: 200, body: [{ email: recipient, status: 'sent', _id: providerID }] };
  });
  t.after(async () => { await cluster.cleanup(); await sink.close(); await smtp.close(); });
  await cluster.initialize(['mail-process']);
  await sink.start();
  await smtp.start();
  await cluster.startAll();
  const node = cluster.nodes[0]!;
  const viewer = cluster.secret();
  assert.equal((await cluster.api(node, '/api/accounts', {
    method: 'POST', body: { name: 'Delivery outcome reader', role: 'viewer', token: viewer },
  })).status, 201);
  const recipients = ['alpha@example.test', 'beta@example.test'];
  const inputs = [
    emailInput('mailgun', 'Mailgun US', { apiKey: key, domain: 'proof.example.test', region: 'US', baseURL: `${sink.url}/mailgun-us`, allowInsecureHTTP: true }),
    emailInput('mailgun', 'Mailgun EU', { apiKey: key, domain: 'proof.example.test', region: 'EU', baseURL: `${sink.url}/mailgun-eu`, allowInsecureHTTP: true }),
    emailInput('brevo', 'Brevo', { apiKey: key, baseURL: `${sink.url}/brevo`, allowInsecureHTTP: true }, recipients),
    emailInput('mandrill', 'Mandrill', { apiKey: key, baseURL: `${sink.url}/mandrill`, allowInsecureHTTP: true }, recipients),
    emailInput('smtp', 'SMTP', { server: '127.0.0.1', port: smtp.port, tlsMode: 'plain' }),
  ];
  const channels: Channel[] = [];
  for (const input of inputs) channels.push(await createChannel(cluster, input));
  const monitor = await cluster.createMonitor({ ...monitorInput(cluster, 'Mail integration', channels.map(c => c.id)), webhookUrl: cluster.webhookUrl });
  await observed(cluster, monitor.id, 'up');
  cluster.targetState = 'down';
  let pending: DeliveryAttempt | undefined;
  await cluster.wait('Brevo rate-limit outcome is persisted while every other destination succeeds', async () => {
    const values = await attempts(cluster, node, `monitorId=${monitor.id}`);
    pending = values.find(a => a.provider === 'brevo' && a.state === 'retrying');
    return Boolean(pending && values.length === 8 && values.filter(a => a.state === 'accepted').length === 7);
  });
  assert.ok(pending);
  assert.equal(pending.failures, 1);
  assert.equal(pending.httpStatus, 429);
  assert.equal(pending.errorCode, 'rate_limited');
  assert.ok(pending.nextAttemptAt! >= pending.lastAttemptAt! + 8_000, 'Retry-After is honored, not just exponential backoff');
  const failedOutcome = await cluster.api<DeliveryAttempt[]>(node, `/api/notification-deliveries?monitorId=${monitor.id}`, { token: viewer });
  assert.equal(failedOutcome.status, 200);
  assert.ok(failedOutcome.body.some(a => a.state === 'retrying' && a.httpStatus === 429 && a.errorCode === 'rate_limited'));
  assertSafe(failedOutcome.text, [key, privateAddress, ...recipients, sink.url, providerID, 'private provider body']);
  const brevoBeta = () => sink.requests.filter(r => r.path.startsWith('/brevo') && r.body.to[0].email === recipients[1]);
  const firstRequest = brevoBeta()[0]!;
  assert.equal(pending.id, firstRequest.body.headers['X-Canary-Idempotency-Key']);
  assert.equal(cluster.events.length, 1);
  assert.equal(smtp.messages.length, 1);
  assert.equal(smtp.messages[0]!.from, '<canary@example.test>');
  assert.deepEqual(smtp.messages[0]!.recipients, ['<alpha@example.test>']);
  assert.match(smtp.messages[0]!.data, /Subject: down \/ Mail integration/);
  assert.match(smtp.messages[0]!.data, /Content-Transfer-Encoding: quoted-printable/);
  const smtpAttempt = (await attempts(cluster, node)).find(a => a.provider === 'smtp')!;
  assert.ok(smtp.messages[0]!.data.includes(`X-Canary-Idempotency-Key: ${hash(smtpAttempt.id)}`));

  // Kill a real Go process after durable failure status, then reopen its exact private SQLite DB.
  await cluster.stop(node);
  await cluster.start(node);
  const persisted = (await attempts(cluster, node)).find(a => a.id === pending!.id)!;
  assert.equal(persisted.state, 'retrying');
  assert.equal(persisted.nextAttemptAt, pending.nextAttemptAt);
  assert.equal(persisted.failures, 1);
  await stable(cluster, 'restart cannot send before persisted retry deadline', () => {
    assert.equal(brevoBeta().length, 1);
    assert.equal(cluster.events.length, 1);
    assert.equal(smtp.messages.length, 1);
  }, Math.max(1, pending.nextAttemptAt! - Date.now() - 200));
  await cluster.wait('only the pending recipient is accepted after the retry deadline', async () =>
    (await attempts(cluster, node, `monitorId=${monitor.id}`)).filter(a => a.state === 'accepted').length === 8);
  const retries = brevoBeta();
  assert.equal(retries.length, 2);
  assert.ok(retries[1]!.at >= pending.nextAttemptAt!);
  assert.equal(retries[1]!.body.headers['X-Canary-Idempotency-Key'], pending.id);
  for (const provider of ['brevo', 'mandrill']) {
    const requests = sink.requests.filter(r => r.path.startsWith(`/${provider}`));
    const emailOf = (r: ProviderRequest) => provider === 'brevo' ? r.body.to[0].email : r.body.message.to[0].email;
    const keyOf = (r: ProviderRequest) => provider === 'brevo' ? r.body.headers['X-Canary-Idempotency-Key'] : r.body.message.metadata.canary_idempotency_key;
    assert.equal(requests.filter(r => emailOf(r) === recipients[0]).length, 1);
    const beta = requests.filter(r => emailOf(r) === recipients[1]);
    assert.equal(beta.length, 2);
    assert.equal(keyOf(beta[0]!), keyOf(beta[1]!));
  }
  const result = await cluster.api<DeliveryAttempt[]>(node, `/api/notification-deliveries?monitorId=${monitor.id}`);
  assertSafe(result.text, [key, privateAddress, ...recipients, sink.url, providerID]);
  for (const attempt of result.body.filter(a => ['mailgun', 'brevo', 'mandrill'].includes(a.provider))) {
    assert.equal(attempt.providerId, hash(providerID));
    assert.equal(attempt.nextAttemptAt, undefined);
  }
  await stable(cluster, 'accepted destinations remain quiet after process restart', () => {
    assert.equal(sink.requests.length, 8);
    assert.equal(smtp.messages.length, 1);
    assert.equal(cluster.events.length, 1);
    sink.assertHealthy();
  });
  cluster.targetState = 'up';
  await cluster.wait('all destinations independently accept recovery', async () =>
    (await attempts(cluster, node, `monitorId=${monitor.id}`)).filter(a => a.kind === 'recovery' && a.state === 'accepted').length === 8);
  assert.equal(sink.requests.length, 14);
  assert.equal(smtp.messages.length, 2);
  assert.equal(cluster.events.length, 2);
  sink.assertHealthy();
  t.diagnostic('Local Mailgun US/EU path prefixes, Brevo, Mandrill recipient rejection/retry, and SMTP final DATA acknowledgement; no external mail send.');
});

test('notification HA: connected destination deduplication, sender death and delivery-first partitions', {
  timeout: 150_000,
}, async t => {
  const cluster = new Cluster();
  const key = cluster.secret();
  const sink = new ProviderSink(request => {
    assert.equal(request.method, 'POST');
    if (request.path === '/named') {
      assert.equal(request.headers.authorization, `Bearer ${key}`);
      assert.match(String(request.headers['idempotency-key']), /^[a-f0-9]{64}$/);
      return { status: 204 };
    }
    assert.equal(request.path, '/v3/smtp/email');
    assert.equal(request.headers['api-key'], key);
    assert.equal(request.body.to.length, 1);
    assert.match(request.body.headers['X-Canary-Idempotency-Key'], /^[a-f0-9]{64}$/);
    return { status: 201, body: { messageId: providerID } };
  });
  t.after(async () => { await cluster.cleanup(); await sink.close(); });
  await cluster.initialize(['delivery-a', 'delivery-b']);
  await sink.start();
  await cluster.startAll();
  const [a, b] = cluster.nodes;
  const hook = await createChannel(cluster, webhookInput('Named destination', `${sink.url}/named`, key));
  const email = await createChannel(cluster, emailInput('brevo', 'Independent recipients', {
    apiKey: key, baseURL: sink.url, allowInsecureHTTP: true,
  }, ['alpha@example.test', 'beta@example.test']));
  await channelsReplicated(cluster, [hook, email]);
  const monitor = await cluster.createMonitor({ ...monitorInput(cluster, 'HA fanout', [hook.id, email.id]), webhookUrl: cluster.webhookUrl });
  await observed(cluster, monitor.id, 'up');
  assert.notEqual(a!.db, b!.db);
  const count = () => sink.requests.length + cluster.events.length;
  const down = async (expected: number) => {
    cluster.targetState = 'down';
    await cluster.wait('every destination receives outage acceptance', () => count() >= expected);
    assert.equal(count(), expected);
    sink.assertHealthy();
  };
  const recover = async (expected: number) => {
    cluster.targetState = 'up';
    await observed(cluster, monitor.id, 'up');
    await cluster.wait('each destination receives its recovery', () => count() >= expected);
    assert.equal(count(), expected);
  };

  await down(4);
  const incident = cluster.alerts()[0]!.incidentId;
  await cluster.wait('destination-aware receipts replicate to both SQLite processes', async () =>
    (await cluster.overviews()).every(overview => overview.notifications.filter(r => r.monitorId === monitor.id && r.kind === 'down').length === 4));
  const receipts = (await cluster.overview(b!)).notifications.filter(r => r.monitorId === monitor.id);
  assert.equal(new Set(receipts.map(r => r.destinationId ?? '')).size, 4);
  assert.equal(receipts.filter(r => r.channelId === email.id && r.provider === 'brevo').length, 2);
  assert.equal((await attempts(cluster, a!, `incidentId=${incident}`)).filter(v => v.state === 'accepted').length, 4);
  await stable(cluster, 'connected candidates do not resend another destination', () => assert.equal(count(), 4));
  await recover(8);

  // The preferred sender is genuinely dead before the next failure; no manual ownership transfer.
  await cluster.stop(a!);
  await down(12);
  assert.equal(cluster.alerts().at(-1)!.nodeId, b!.id);
  await stable(cluster, 'survivor delivers once per destination', () => assert.equal(count(), 12));
  await cluster.start(a!);
  await observed(cluster, monitor.id, 'down');
  await stable(cluster, 'restarted sender learns accepted destinations instead of replaying them', () => assert.equal(count(), 12));
  await recover(16);

  cluster.partition([[a!.id], [b!.id]]);
  cluster.targetState = 'down';
  await cluster.wait('both disconnected partitions deliver all four destinations', () => count() >= 24);
  assert.equal(count(), 24, 'partitions favor delivery; each disconnected node sends its own evidence');
  const partitionAlerts = cluster.alerts().slice(-2);
  assert.deepEqual(new Set(partitionAlerts.map(alert => alert.nodeId)), new Set([a!.id, b!.id]));
  const before = new Set(partitionAlerts.map(alert => alert.event.idempotencyKey));
  assert.equal(before.size, 2, 'independently discovered partition incidents have independent attempts');
  cluster.reconnect();
  await cluster.wait('all successful partition receipt IDs converge', async () => {
    const views = await cluster.overviews();
    const ids = views.map(view => view.notifications.map(r => r.id).sort());
    return ids[0]!.length === 24 && JSON.stringify(ids[0]) === JSON.stringify(ids[1]);
  });
  await stable(cluster, 'reconciliation preserves receipts and stops extra sends', () => {
    assert.equal(count(), 24);
    sink.assertHealthy();
  });
  t.diagnostic(`Four independent destinations, real SIGKILL/restart and isolated peer relays; quiet windows ${QUIET_MS}ms.`);
});

test('notification groups: a successful shared route cannot hide failed parent email, and held destinations release once', {
  timeout: 100_000,
}, async t => {
  const cluster = new Cluster();
  const key = cluster.secret();
  const sink = new ProviderSink(request => {
    if (request.path === '/shared') {
      assert.equal(request.headers.authorization, `Bearer ${key}`);
      return { status: 204 };
    }
    assert.equal(request.path, '/v3/smtp/email');
    assert.equal(request.headers['api-key'], key);
    assert.equal(request.body.to.length, 1);
    if (request.body.subject === 'down / Dependency') return { status: 503, headers: { 'Retry-After': '20' }, body: { private: key } };
    return { status: 201, body: { messageId: providerID } };
  });
  t.after(async () => { await cluster.cleanup(); await sink.close(); });
  await cluster.initialize(['group-delivery-a', 'group-delivery-b']);
  await sink.start();
  await cluster.startAll();
  const parentTarget = await cluster.createTarget();
  const childTarget = await cluster.createTarget();
  const shared = await createChannel(cluster, webhookInput('Shared successful route', `${sink.url}/shared`, key));
  const email = await createChannel(cluster, emailInput('brevo', 'Failing parent mail', { apiKey: key, baseURL: sink.url, allowInsecureHTTP: true }));
  await channelsReplicated(cluster, [shared, email]);
  const ids = [shared.id, email.id];
  const parent = await cluster.createMonitor(monitorInput(cluster, 'Dependency', ids, parentTarget));
  const group = await cluster.createGroup({ name: 'Arbitrary local dependency', nodeIds: cluster.nodes.map(n => n.id), outageMonitorId: parent.id, graceMs: 5_000 });
  const child = await cluster.createMonitor({ ...monitorInput(cluster, 'Dependent', ids, childTarget), groupId: group.id });
  await observed(cluster, parent.id, 'up');
  await observed(cluster, child.id, 'up');
  parentTarget.state = 'down';
  childTarget.state = 'down';
  const sharedChild = () => sink.requests.filter(r => r.path === '/shared' && r.body.monitor.id === child.id && r.body.kind === 'down');
  const emailChild = () => sink.requests.filter(r => r.path === '/v3/smtp/email' && r.body.subject === 'down / Dependent');
  await cluster.wait('child email fails open despite a successful parent shared destination', () => emailChild().length >= 1);
  assert.equal(emailChild().length, 1);
  await cluster.wait('held and accepted child destinations remain independently inspectable', async () =>
    (await Promise.all(cluster.nodes.map(n => attempts(cluster, n, `monitorId=${child.id}`)))).every(values =>
      values.some(a => a.channelId === shared.id && a.state === 'held') &&
      values.some(a => a.channelId === email.id && a.state === 'accepted')));
  await stable(cluster, 'successful shared parent route holds only the matching child destination', () => {
    assert.equal(sharedChild().length, 0);
    assert.equal(emailChild().length, 1);
    sink.assertHealthy();
  });
  parentTarget.state = 'up';
  await observed(cluster, parent.id, 'up');
  await cluster.wait('held child route releases once after parent recovery', () => sharedChild().length >= 1);
  assert.equal(sharedChild().length, 1);
  await stable(cluster, 'connected peers stagger released destinations and retain accepted email receipt', () => {
    assert.equal(sharedChild().length, 1);
    assert.equal(emailChild().length, 1);
  });
  childTarget.state = 'up';
  await cluster.wait('each accepted child destination can recover independently', async () =>
    (await cluster.overviews()).every(view => view.notifications.filter(r => r.monitorId === child.id && r.kind === 'recovery').length === 2));
  sink.assertHealthy();
  t.diagnostic('Shared webhook success and parent email failure are independent; the held child route releases without resending accepted child email.');
});

test('notification API safety: revisions, conflict resolution, redaction, RBAC, bounded tests and old-peer rejection', {
  timeout: 80_000,
}, async t => {
  const cluster = new Cluster();
  const key = cluster.secret();
  const sink = new ProviderSink(request => {
    assert.equal(request.headers['api-key'], key);
    assert.equal(request.path, '/v3/smtp/email');
    assert.equal(request.body.to.length, 1);
    assert.match(request.body.subject, /^test \/ Notification test$/);
    return { status: 201, body: { messageId: providerID } };
  });
  t.after(async () => { await cluster.cleanup(); await sink.close(); });
  await cluster.initialize(['channel-api-a', 'channel-api-b']);
  await sink.start();
  await cluster.startAll();
  const [a, b] = cluster.nodes;
  const input = emailInput('brevo', 'Private test channel', { apiKey: key, baseURL: sink.url, allowInsecureHTTP: true }, ['alpha@example.test', 'beta@example.test']);
  let channel = await createChannel(cluster, input);
  await channelsReplicated(cluster, [channel]);
  const path = `/api/notification-channels/${channel.id}`;
  const viewer = cluster.secret();
  const editor = cluster.secret();
  for (const [role, token] of [['viewer', viewer], ['editor', editor]]) {
    assert.equal((await cluster.api(a!, '/api/accounts', { method: 'POST', body: { name: `Notification ${role}`, role, token } })).status, 201);
  }
  for (const endpoint of ['/api/notification-channels', path, `${path}/conflicts`, '/api/notification-deliveries']) {
    assert.equal((await cluster.api(a!, endpoint, { token: null })).status, 401);
    assert.ok([401, 403].includes((await cluster.api(a!, endpoint, { token: cluster.clusterToken })).status));
    const response = await cluster.api(a!, endpoint, { token: viewer });
    assert.equal(response.status, 200);
    assertSafe(response.text, [key, privateAddress, 'alpha@example.test', 'beta@example.test', sink.url, providerID]);
  }
  for (const [endpoint, method, body] of [
    ['/api/notification-channels', 'POST', input], [path, 'PUT', { ...input, revision: channel.revision }],
    [path, 'DELETE', { revision: channel.revision }], [`${path}/test`, 'POST', { revision: channel.revision }],
    [`${path}/resolve`, 'POST', { ...input, revisions: [channel.revision] }],
  ] as const) assert.equal((await cluster.api(a!, endpoint, { method, body, token: viewer })).status, 403);
  for (const query of ['limit=0', 'limit=1001', 'limit=abc']) assert.equal((await cluster.api(a!, `/api/notification-deliveries?${query}`)).status, 400);
  assert.equal((await cluster.api(a!, `${path}/test`, { method: 'POST', body: { revision: 'stale' } })).status, 409);
  assert.equal(sink.requests.length, 0, 'validation and authorization failures perform no outbound sends');

  cluster.partition([[a!.id], [b!.id]]);
  for (const [index, node] of cluster.nodes.entries()) {
    const update = await cluster.api<Channel>(node, path, { method: 'PUT', body: { ...input, name: `Independent edit ${index}`, revision: channel.revision } });
    assert.equal(update.status, 200);
  }
  cluster.reconnect();
  await cluster.wait('concurrent channel edits are visible as conflicts on both nodes', async () =>
    (await Promise.all(cluster.nodes.map(n => cluster.api<ChannelView[]>(n, '/api/notification-channels')))).every(r =>
      r.body.some(v => v.channel.id === channel.id && v.configConflict)));
  const heads = await cluster.api<Channel[]>(a!, `${path}/conflicts`);
  assert.equal(heads.status, 200);
  assert.equal(heads.body.length, 2);
  const redacted = await cluster.api(a!, `${path}/conflicts`, { token: viewer });
  assertSafe(redacted.text, [key, sink.url, privateAddress]);
  assert.equal((await cluster.api(a!, `${path}/test`, { method: 'POST', body: { revision: heads.body[0]!.revision } })).status, 409);
  for (const revisions of [[heads.body[0]!.revision], [heads.body[0]!.revision, heads.body[0]!.revision]]) {
    assert.equal((await cluster.api(a!, `${path}/resolve`, { method: 'POST', body: { ...input, revisions } })).status, 409);
  }
  const resolved = await cluster.api<Channel>(a!, `${path}/resolve`, { method: 'POST', body: { ...input, revisions: heads.body.map(h => h.revision) }, token: editor });
  assert.equal(resolved.status, 200);
  channel = resolved.body;
  await channelsReplicated(cluster, [channel]);

  const queued = await cluster.api<{ attempts: DeliveryAttempt[] }>(a!, `${path}/test`, { method: 'POST', body: { revision: channel.revision }, token: editor });
  assert.equal(queued.status, 202);
  assert.equal(queued.body.attempts.length, 2, 'one bounded test batch contains one attempt per recipient');
  assert.equal(new Set(queued.body.attempts.map(attempt => attempt.destinationId)).size, 2);
  for (const value of queued.body.attempts) {
    assert.equal(value.kind, 'test');
    assert.equal(value.channelId, channel.id);
    assert.equal(value.monitorId, undefined);
    assert.equal(value.incidentId, undefined);
  }
  // The batch limit is per node, not per recipient or channel.
  const other = await createChannel(cluster, { ...input, name: 'Other channel' });
  assert.equal((await cluster.api(a!, `/api/notification-channels/${other.id}/test`, { method: 'POST', body: { revision: other.revision } })).status, 429);
  await cluster.wait('test destinations expose accepted local outcomes', async () =>
    (await attempts(cluster, a!)).filter(value => value.kind === 'test' && value.state === 'accepted').length === 2);
  assert.equal(sink.requests.length, 2);
  const outcomes = await cluster.api<DeliveryAttempt[]>(a!, '/api/notification-deliveries', { token: viewer });
  assert.equal(outcomes.status, 200);
  assertSafe(outcomes.text, [key, privateAddress, 'alpha@example.test', 'beta@example.test', sink.url, providerID]);
  assert.ok(outcomes.body.every(value => value.providerId === hash(providerID) && value.nodeId === a!.id));
  assert.equal((await attempts(cluster, b!)).length, 0, 'test outcomes remain node-local');
  for (const overview of await cluster.overviews()) {
    assert.equal(overview.monitors.length, 0);
    assert.equal(overview.incidents.length, 0);
    assert.equal(overview.notifications.length, 0, 'a channel test must not invent replicated incident receipts');
  }
  assert.equal((await cluster.api(a!, path, { method: 'DELETE', body: { revision: 'stale' } })).status, 409);
  assert.equal((await cluster.api(a!, path, { method: 'DELETE', body: { revision: channel.revision } })).status, 204);
  await cluster.wait('channel tombstone replicates', async () => (await cluster.api(b!, path)).status === 404);
  assert.equal((await cluster.api(a!, `${path}/test`, { method: 'POST', body: { revision: channel.revision } })).status, 409);
  const disabledInput: ChannelInput = { ...input, enabled: false, name: 'Disabled' };
  const disabled = await createChannel(cluster, disabledInput);
  assert.equal((await cluster.api(a!, `/api/notification-channels/${disabled.id}/test`, { method: 'POST', body: { revision: disabled.revision } })).status, 409);
  assert.equal(sink.requests.length, 2);

  const rejected = await cluster.api(a!, '/internal/snapshot', {
    method: 'POST', token: cluster.clusterToken, headers: { 'X-Canary-Node-Id': b!.id },
    body: { protocol: 2, node: { id: b!.id, name: 'Old peer' }, versions: [], observations: [], incidents: [], receipts: [] },
  });
  assert.equal(rejected.status, 400, 'protocol 2 cannot merge expanded channel configuration');
  // Let peer exchanges settle once; no retry loop or invented monitoring state is necessary.
  await delay(200);
  sink.assertHealthy();
  t.diagnostic('Admin/editor management, viewer-safe config and outcomes, conflict resolution and tombstones, bounded per-recipient tests with no fake monitoring state.');
});
