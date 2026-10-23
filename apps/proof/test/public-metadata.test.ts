import assert from 'node:assert/strict';
import test from 'node:test';
import type { LocalAvailabilityHistory, Monitor, MonitorInput, PublicStatusPage, StatusPage, StatusPageInput, Tag } from '../../../packages/shared/src/index.js';
import { Cluster } from '../fixtures/cluster.js';

test('published status and tags preserve explicit privacy across peers and restart without inventing manual latency', { timeout: 75_000 }, async t => {
  const cluster = new Cluster();
  t.after(() => cluster.cleanup());
  await cluster.initialize(['public-a', 'public-b']);
  await cluster.startAll();
  const [a, b] = cluster.nodes;
  assert.ok(a && b);
  const createTag = async (name: string, color: string): Promise<Tag> => {
    const r = await cluster.api<Tag>(a, '/api/tags', { method: 'POST', body: { name, color } });
    assert.equal(r.status, 201, cluster.redact(r.text));
    return r.body;
  };
  const exposed = await createTag('Environment', '#AABBCC');
  const privateTag = await createTag('Private routing label', '#112233');
  assert.equal(exposed.color, '#aabbcc');
  const input: MonitorInput = {
    name: 'Private internal service name', type: 'manual', target: '',
    intervalMs: 1_000, timeoutMs: 500, nodeIds: [a.id, b.id], enabled: true,
    manual: { state: 'up', message: 'Private operational message' },
    tags: [
      { tagId: exposed.id, value: 'production', public: true },
      { tagId: privateTag.id, value: 'Private rack 42' },
    ],
  };
  const archiveFrom = Date.now();
  let monitor = await cluster.createMonitor(input);
  const generation = monitor.checkGeneration || monitor.revision;
  let pageInput: StatusPageInput = {
    slug: 'service-health', title: 'Service health', description: 'Published explicitly', enabled: true,
    groups: [{ id: 'public-group', name: 'Services', monitors: [{ monitorId: monitor.id, name: 'Public service', showCertificate: false }] }],
    announcements: [],
  };
  const created = await cluster.api<StatusPage>(a, '/api/status-pages', { method: 'POST', body: pageInput });
  assert.equal(created.status, 201, cluster.redact(created.text));
  let page = created.body;
  const publicPath = `/public/status/${pageInput.slug}`;
  const projection = async (node = a) => cluster.api<PublicStatusPage>(node, publicPath, { token: null });
  const badgePath = `/public/badge/${pageInput.slug}.svg?${new URLSearchParams({ group: 'public-group', monitor: 'Public service' })}`;
  const assertPrivate = (text: string): void => {
    for (const value of [monitor.id, monitor.target, 'Private internal service name', 'Private operational message', privateTag.id, 'Private rack 42', 'Private routing label', exposed.id]) {
      // Manual's canonical target is a generic word; private identities/text
      // are the meaningful redaction checks, not that public state vocabulary.
      if (value !== 'manual') assert.ok(!text.includes(value), `Public projection leaked ${value}`);
    }
  };
  await cluster.wait('both nodes publish fresh manual UP without private monitor metadata', async () => {
    for (const node of [a, b]) {
      const r = await projection(node);
      if (r.status !== 200 || r.body.state !== 'up') return false;
      assertPrivate(r.text);
      assert.equal(r.body.groups[0]!.monitors[0]!.tags, undefined, 'assignment opt-in alone cannot publish tags');
    }
    return true;
  });
  const assertBadge = async (node: typeof a, state: string): Promise<void> => {
    const r = await fetch(`${node.url}${badgePath}`, { signal: AbortSignal.timeout(2_000) });
    const body = await r.text();
    assert.equal(r.status, 200);
    assert.match(r.headers.get('content-type') ?? '', /^image\/svg\+xml/);
    assert.equal(r.headers.get('cache-control'), 'no-store');
    assert.equal(r.headers.get('x-content-type-options'), 'nosniff');
    assert.equal(r.headers.get('access-control-allow-origin'), '*');
    assert.ok(r.headers.get('content-security-policy'));
    assert.ok(body.includes(`status: ${state}`));
    assertPrivate(body);
    assert.ok(!body.includes('<script'));
    for (const reference of body.matchAll(/(?:href|src)=["']([^"']*)["']/g)) {
      assert.ok(reference[1]?.startsWith('#'), 'Badge must not load an external reference');
    }
  };
  for (const node of [a, b]) {
    await assertBadge(node, 'up');
    const head = await fetch(`${node.url}${badgePath}`, { method: 'HEAD', signal: AbortSignal.timeout(2_000) });
    assert.equal(head.status, 200);
    assert.equal(await head.text(), '');
  }
  await cluster.wait('both processes persist local observed-time coverage', async () => {
    for (const node of [a, b]) {
      const to = Date.now();
      const r = await cluster.api<LocalAvailabilityHistory>(node,
        `/api/monitors/${monitor.id}/availability?${new URLSearchParams({ from: String(archiveFrom), to: String(to), stepMs: '1000' })}`);
      assert.equal(r.status, 200, cluster.redact(r.text));
      assert.equal(r.body.nodeId, node.id);
      assert.equal(r.body.summary.endMs - r.body.summary.startMs, to - archiveFrom);
      const { upMs, downMs, pendingMs, unknownMs, knownMs } = r.body.summary;
      assert.equal(upMs + downMs + pendingMs + unknownMs, to - archiveFrom);
      assert.equal(knownMs, upMs + downMs);
      assert.equal(r.body.buckets.reduce((sum, bucket) => sum + bucket.knownMs, 0), knownMs);
      if (knownMs === 0) return false;
      assert.equal(r.body.summary.availabilityPercent, 100);
    }
    return true;
  });
  for (const node of [a, b]) {
    const observations = await cluster.history(node, monitor.id);
    assert.ok(observations.length > 0);
    assert.ok(observations.every(o => o.latencyAvailable === false && o.latencyMs === 0), 'manual checks cannot claim measured RTT');
    const stats = await cluster.api<{ latencyCount: number; up: number }>(node, `/api/monitors/${monitor.id}/statistics`);
    assert.equal(stats.status, 200, cluster.redact(stats.text));
    assert.equal(stats.body.latencyCount, 0);
  }
  pageInput = { ...pageInput, groups: [{ ...pageInput.groups[0]!, monitors: [{ ...pageInput.groups[0]!.monitors[0]!, showTags: true }] }] };
  const edited = await cluster.api<StatusPage>(a, `/api/status-pages/${page.id}`, { method: 'PUT', body: { ...pageInput, revision: page.revision } });
  assert.equal(edited.status, 200, cluster.redact(edited.text));
  page = edited.body;
  await cluster.wait('both opt-ins publish only the explicit public tag', async () => {
    for (const node of [a, b]) {
      const r = await projection(node);
      assertPrivate(r.text);
      if (r.body.groups[0]?.monitors[0]?.tags?.length !== 1) return false;
      assert.deepEqual(r.body.groups[0]!.monitors[0]!.tags, [{ name: 'Environment', color: '#aabbcc', value: 'production' }]);
    }
    return true;
  });
  const down = await cluster.api<Monitor>(a, `/api/monitors/${monitor.id}`, { method: 'PUT', body: { ...input, manual: { state: 'down', message: 'Private failure diagnosis' }, revision: monitor.revision } });
  assert.equal(down.status, 200, cluster.redact(down.text));
  monitor = down.body;
  assert.equal(monitor.checkGeneration || monitor.revision, generation, 'manual status changes preserve the check generation');
  await cluster.wait('manual DOWN reaches both published views without the private diagnosis', async () => {
    const results = await Promise.all([projection(a), projection(b)]);
    results.forEach(r => { assertPrivate(r.text); assert.ok(!r.text.includes('Private failure diagnosis')); });
    return results.every(r => r.status === 200 && r.body.state === 'down');
  });
  for (const node of [a, b]) await assertBadge(node, 'down');
  assert.equal((await cluster.api(a, `/api/tags/${exposed.id}`, { method: 'DELETE', body: { revision: exposed.revision } })).status, 204);
  await cluster.wait('deleted public tag disappears on the peer', async () => {
    const r = await projection(b);
    return r.status === 200 && !r.body.groups[0]!.monitors[0]!.tags?.length;
  });
  await cluster.stop(b);
  await cluster.start(b);
  await cluster.wait('publication survives a real process restart with the tag tombstone', async () => {
    const r = await projection(b);
    assertPrivate(r.text);
    return r.status === 200 && r.body.state === 'down' && !r.body.groups[0]!.monitors[0]!.tags?.length;
  });
  await assertBadge(b, 'down');
  const disabled = await cluster.api(a, `/api/status-pages/${page.id}`, { method: 'PUT', body: { ...pageInput, enabled: false, revision: page.revision } });
  assert.equal(disabled.status, 200, cluster.redact(disabled.text));
  await cluster.wait('disabled publication is indistinguishable from a missing slug on both nodes', async () => {
    for (const node of [a, b]) {
      const r = await projection(node);
      if (r.status !== 404) return false;
      const absent = await cluster.api(node, '/public/status/nonexistent', { token: null });
      assert.equal(r.text, absent.text);
      const badge = await fetch(`${node.url}${badgePath}`, { signal: AbortSignal.timeout(2_000) });
      assert.equal(badge.status, 404);
      const absentBadge = await fetch(`${node.url}/public/badge/nonexistent.svg`, { signal: AbortSignal.timeout(2_000) });
      assert.equal(await badge.text(), await absentBadge.text());
    }
    return true;
  });
  assert.equal(cluster.events.length, 0, 'publication and tag edits cannot create webhook side effects');
  t.diagnostic('Two compiled Go processes, private publication names, dual tag opt-in, manual measurement validity, tombstones and restart; no external service calls.');
});
