// Root runs through python scripts/bounded_validation.py -- node scripts/capacity-proof-pure.mjs.
// Pure proof controls: no runtime, network, databases or generated files.
import assert from 'node:assert/strict';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { register } from 'tsx/esm/api';
process.env.TSX_DISABLE_CACHE = '1';
const scope = register({ namespace: 'capacity-proof', tsconfig: resolve('apps/proof/tsconfig.json') });
const { CapacityObligations } = await scope.import(pathToFileURL(resolve('apps/proof/fixtures/capacity.ts')).href, import.meta.url);
const channels = new Map([['/fast', 'fast'], ['/backlog', 'backlog']]);
const attempt = (id, incidentId = id, state = 'retrying') => ({ id, incidentId, state, nodeId: 'a', monitorId: 'monitor',
  sourceIncidentId: id, satisfiedByReceiptId: state === 'accepted' ? id : undefined,
  destinationId: 'destination', channelId: 'backlog', kind: 'down', provider: 'webhook', failures: 5, nextAttemptAt: Date.now() + 60_000 });
const receipt = (id, incidentId = id) => ({ id, incidentId, monitorId: 'monitor', destinationId: 'destination',
  channelId: 'backlog', kind: 'down', nodeId: 'a', sentAt: 1 });
const request = (key, accepted = true) => ({ key, route: '/backlog', monitor: 'monitor', source: key, sender: 'a', kind: 'down', accepted });
const snapshot = (attempts, notifications, nodeId = 'a') => ({ nodeId, attempts, notifications });
const peers = (attempts, notifications, peerReceipts = notifications) =>
  [snapshot(attempts, notifications), snapshot([], peerReceipts, 'b')];
let passed = 0;
function check(name, run) { run(); passed++; console.log('PASS ' + name); }

check('one accepted source cannot hide a retained independent retry', () => {
  const requests = [request('one'), request('two', false)];
  assert.equal(new Set(requests.filter(value => value.accepted).map(value => value.monitor)).size, 1,
    'the previous monitor-count criterion passes this counterexample');
  const tracker = new CapacityObligations(channels);
  const values = [attempt('one', 'one', 'accepted'), attempt('two')];
  assert.deepEqual(tracker.inspect(peers(values, [receipt('one')]), requests).pending.map(value => value.id), ['two']);
  values[1] = attempt('two', 'two', 'accepted');
  assert.equal(tracker.inspect(peers(values, [receipt('one'), receipt('two')]),
    [...requests, request('two')]).pending.length, 0);
});
check('alias coverage requires owning-node acceptance and a matching canonical receipt', () => {
  const tracker = new CapacityObligations(channels);
  const values = [attempt('one', 'canonical', 'accepted'), attempt('two', 'canonical')];
  assert.equal(tracker.inspect(peers(values, [receipt('one', 'canonical')]), [request('one')]).pending.length, 1);
  values[1] = { ...attempt('two', 'canonical', 'accepted'), satisfiedByReceiptId: 'one' };
  const result = tracker.inspect(peers(values, [receipt('one', 'canonical')]), [request('one')]);
  assert.equal(result.pending.length, 0); assert.equal(result.exact, 1); assert.equal(result.aliased, 1);
});
check('unexplained cancellation and unavailable destinations do not settle an obligation', () => {
  for (const state of ['cancelled', 'held', 'failed', 'unknown', 'unavailable']) {
    assert.equal(new CapacityObligations(channels).inspect(peers([attempt('one', 'one', state)], []), []).pending.length, 1);
  }
});
check('a receipt without provider acceptance does not establish delivery', () => {
  assert.equal(new CapacityObligations(channels).inspect(peers([attempt('one', 'one', 'accepted')], [receipt('one')]),
    [request('one', false)]).pending.length, 1);
});
check('receipt scope must match monitor, channel, destination and event', () => {
  for (const patch of [{ monitorId: 'other' }, { channelId: 'fast' }, { destinationId: 'other' }, { kind: 'recovery' }]) {
    assert.equal(new CapacityObligations(channels).inspect(peers([attempt('one', 'one', 'accepted')],
      [{ ...receipt('one'), ...patch }]), [request('one')]).pending.length, 1);
  }
});
check('a remembered obligation cannot disappear from the local attempt view', () => {
  const tracker = new CapacityObligations(channels); tracker.remember('a', [attempt('one')]);
  assert.equal(tracker.inspect(peers([], [receipt('one')]), [request('one')]).pending[0].state, 'missing');
});
check('requests racing a snapshot still create retained obligations', () => {
  const result = new CapacityObligations(channels).inspect(peers([], []), [request('one', false)]);
  assert.equal(result.pending[0].id, 'one'); assert.equal(result.pending[0].state, 'missing');
});
check('each owning node must learn coverage and newly discovered obligations remain pending', () => {
  const tracker = new CapacityObligations(channels);
  const values = [snapshot([attempt('one', 'one', 'accepted')], [receipt('one')]), snapshot([attempt('one', 'one', 'accepted')], [], 'b')];
  assert.ok(tracker.inspect(values, [request('one')]).pending.some(item => item.nodeId === 'b'));
  values[1].notifications.push(receipt('one'));
  assert.equal(tracker.inspect(values, [request('one')]).pending.length, 0);
  values[1].attempts.push(attempt('two'));
  assert.equal(tracker.inspect(values, [request('one')]).pending[0].id, 'two');
});
check('a full attempt page cannot silently truncate the proof', () => {
  assert.throws(() => new CapacityObligations(channels).remember('a', Array(1000).fill(attempt('one'))), /untruncated/);
});
check('retained identity metadata cannot change', () => {
  const tracker = new CapacityObligations(channels); tracker.remember('a', [attempt('one')]);
  assert.throws(() => tracker.remember('a', [{ ...attempt('one'), destinationId: 'other' }]), /destination/);
});
check('display acceptance alone and missing provenance cannot settle a source', () => {
  const value = attempt('one', 'one', 'accepted'); delete value.satisfiedByReceiptId;
  assert.equal(new CapacityObligations(channels).inspect(peers([value], [receipt('one')]), [request('one')]).pending.length, 1);
  delete value.sourceIncidentId;
  assert.throws(() => new CapacityObligations(channels).inspect(peers([value], [receipt('one')]), [request('one')]), /provenance/);
});
check('alias splitting revokes prior sibling satisfaction', () => {
  const tracker = new CapacityObligations(channels);
  const value = { ...attempt('two', 'two', 'accepted'), satisfiedByReceiptId: 'one' };
  assert.equal(tracker.inspect(peers([attempt('one', 'two', 'accepted'), value], [receipt('one', 'two')]), [request('one')]).pending.length, 0);
  delete value.satisfiedByReceiptId; value.state = 'retrying';
  assert.ok(tracker.inspect(peers([attempt('one', 'one', 'accepted'), value], [receipt('one')]), [request('one')]).pending.some(item => item.id === 'two'));
});
console.log(`ALL PASS (${passed} groups)`);
