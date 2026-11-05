// Root only: python scripts/bounded_validation.py -- node scripts/delivery-view-proof.mjs [--benchmark]
import assert from 'node:assert/strict';
import { performance } from 'node:perf_hooks';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

process.env.TSX_DISABLE_CACHE = '1';
const { register } = await import('tsx/esm/api');
const scope = register({ namespace: 'delivery-proof', tsconfig: resolve('apps/web/tsconfig.json') });
const { deliveryView, deliveryDestinationTokens } = await scope.import(pathToFileURL(resolve('apps/web/src/deliveryView.ts')).href, import.meta.url);

// Unchanged derivation from Channels.tsx at d071828 (PR #34's merged tree).
// Includes the five chip counts, not React rendering or network work.
const PROBLEM = new Set(['failed', 'unknown', 'unavailable', 'retrying']);
function previous(attempts, channelId, search, filter, monitors, channels) {
  const q = search.trim().toLowerCase();
  const all = attempts.filter(attempt => (!channelId || attempt.channelId === channelId)
    && (!q || `${monitors.get(attempt.monitorId ?? '')?.monitor.name ?? ''} ${channels.get(attempt.channelId ?? '')?.channel.name ?? ''} ${attempt.provider} ${attempt.nodeId} ${attempt.errorCode ?? ''} ${attempt.incidentId ?? ''}`.toLowerCase().includes(q)));
  const keep = (attempt, f) => f === 'all' || (f === 'problems' ? PROBLEM.has(attempt.state) : f === 'accepted' ? attempt.state === 'accepted' : f === 'held' ? attempt.state === 'held' : attempt.kind === 'test');
  return { rows: all.filter(a => keep(a, filter)), counts: {
    all: all.length, problems: all.filter(a => keep(a, 'problems')).length,
    accepted: all.filter(a => keep(a, 'accepted')).length, held: all.filter(a => keep(a, 'held')).length,
    tests: all.filter(a => keep(a, 'tests')).length,
  } };
}
const states = ['queued', 'sending', 'retrying', 'accepted', 'held', 'failed', 'cancelled', 'unknown', 'unavailable'];
const kinds = ['down', 'repeat', 'recovery', 'test'];
const monitors = new Map(Array.from({ length: 100 }, (_, i) => [`m${i}`, { monitor: { name: `Service ${i}` } }]));
const channels = new Map(Array.from({ length: 20 }, (_, i) => [`c${i}`, { channel: { name: `Operations ${i}` } }]));
const attempts = Array.from({ length: 1000 }, (_, i) => ({ id: `a${i}`, nodeId: `node-${i % 3}`,
  monitorId: i % 13 ? `m${i % 100}` : undefined, channelId: i % 17 ? `c${i % 20}` : undefined,
  provider: i % 2 ? 'smtp' : 'matrix', state: states[i % states.length], kind: kinds[i % kinds.length],
  failures: i % 4, errorCode: i % 11 ? undefined : 'provider_rejected', incidentId: `incident-${i}`,
}));
const filters = ['all', 'problems', 'accepted', 'held', 'tests'];
let comparisons = 0;
for (const data of [[], attempts.slice(0, 1), attempts.slice(0, 200), attempts]) {
  for (const channel of ['', 'c3', 'missing']) for (const search of ['', '  SERVICE 2 ', 'operations 3', 'SMTP', 'node-1', 'provider_rejected', 'incident-117', 'missing']) {
    for (const filter of filters) {
      const args = [data, channel, search, filter, monitors, channels];
      const expected = previous(...args), actual = deliveryView(...args);
      assert.deepEqual(actual, expected);
      actual.rows.forEach((row, i) => assert.equal(row, expected.rows[i], 'keep original row identity and order'));
      comparisons++;
    }
  }
}
assert.equal(attempts.length, 1000);
console.log(`PASS ${comparisons} before/after equivalence cases: empty/200/1000 rows, all states, overlapping test counts, search and channel scopes`);
const routes = ids => ids.map((id, i) => ({ ...attempts[i], channelId: 'email', destinationId: id }));
assert.equal(deliveryDestinationTokens(routes(['one', 'one'])).size, 0, 'repeated attempts for one route need no token');
assert.equal(deliveryDestinationTokens([{ ...attempts[0], channelId: 'one', destinationId: 'aaa' }, { ...attempts[1], channelId: 'two', destinationId: 'bbb' }]).size, 0, 'different channels are already identifiable');
const labelled = deliveryDestinationTokens(routes(['prefix-aaaaaa', 'prefix-bbbbbb', 'prefix-aaaaaa']));
assert.equal(labelled.get('prefix-aaaaaa'), '…aaaaaa');
assert.equal(labelled.get('prefix-bbbbbb'), '…bbbbbb');
const collision = deliveryDestinationTokens(routes(['first-collision', 'second-collision']));
assert.equal(collision.get('first-collision'), 'first-collision');
assert.equal(collision.get('second-collision'), 'second-collision', 'colliding short tokens fall back to full public IDs');
console.log('PASS destination tokens: distinct routes, repeated routes, independent channels and suffix collisions');
const byId = { ...attempts[0], id: 'public-attempt-QX49', destinationId: 'public-destination-ZY83' };
for (const query of [' PUBLIC-ATTEMPT-qx49 ', 'PUBLIC-destination-zy83', '…n-ZY83']) {
  assert.deepEqual(deliveryView([byId], '', query, 'all', monitors, channels).rows, [byId]);
}
assert.equal(deliveryView([byId], '', 'public-attempt-qx49', 'accepted', monitors, channels).rows.length, 0, 'identifier search retains the outcome filter');
assert.equal(deliveryView([byId], 'another-channel', 'public-attempt-qx49', 'all', monitors, channels).rows.length, 0, 'identifier search retains channel scope');
console.log('PASS public attempt/destination IDs and displayed tokens are searchable without bypassing scope');

if (process.argv.includes('--benchmark')) {
  let checksum = 0;
  const iterations = 1500, pairs = 11;
  const median = values => [...values].sort((a, b) => a - b)[Math.floor(values.length / 2)];
  const reports = [];
  // Rotate every filter on every sample. Search/channel workload is identical for both functions.
  for (const workload of [{ name: 'unfiltered log', channel: '', search: '' }, { name: 'searched log', channel: '', search: 'service' }, { name: 'channel log', channel: 'c3', search: '' }]) {
    const run = fn => {
      const start = performance.now();
      for (let i = 0; i < iterations; i++) {
        const result = fn(attempts, workload.channel, workload.search, filters[i % filters.length], monitors, channels);
        checksum += result.rows.length + result.counts.all;
      }
      return performance.now() - start;
    };
    // The initial baseline can tier up after several samples. Require sustained
    // warm-up, and retain a stability flag instead of claiming steady-state by fiat.
    const warmBefore = [], warmAfter = [];
    const stable = values => {
      const recent = values.slice(-4), preceding = values.slice(-8, -4);
      return values.length >= 12 && Math.max(...recent) / Math.min(...recent) < 1.15
        && Math.abs(median(recent) / median(preceding) - 1) < 0.1;
    };
    for (let i = 0; i < 40; i++) {
      warmBefore.push(run(previous)); warmAfter.push(run(deliveryView));
      if (stable(warmBefore) && stable(warmAfter)) break;
    }
    const before = [], after = [];
    for (let pair = 0; pair < pairs; pair++) {
      if (pair % 2) { after.push(run(deliveryView)); before.push(run(previous)); }
      else { before.push(run(previous)); after.push(run(deliveryView)); }
    }
    const old = median(before), current = median(after);
    reports.push({ workload: workload.name, attempts: attempts.length, iterations, pairs,
      warmupPairs: warmBefore.length, warmupStable: stable(warmBefore) && stable(warmAfter),
      beforeMedianMs: +old.toFixed(3), afterMedianMs: +current.toFixed(3),
      reductionPercent: +(100 * (old - current) / old).toFixed(2), beforeSamplesMs: before, afterSamplesMs: after });
  }
  console.log(JSON.stringify({ runtime: process.version, platform: `${process.platform}/${process.arch}`, measured: 'elapsed time for filtering plus all five counts; excludes destination-token presentation, React, network and memoization gains', reports, checksum }, null, 2));
}
scope.unregister();
