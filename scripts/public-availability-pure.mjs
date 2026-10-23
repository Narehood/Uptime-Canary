// Pure protocol-15 checks for published observed availability and uptime badge addresses:
// apps/web/src/publicAvailability.ts and apps/web/src/badges.ts. Imports the source in place: no copy, no scratch,
// no network, no output files. Root runs it: node scripts/public-availability-pure.mjs (exit 1 on any failure).
// Payloads are generated with the node's own accounting (availability.summarizeRange) and pass through JSON.
import { readFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const { tsImport } = await import('tsx/esm/api');
const load = file => tsImport(pathToFileURL(resolve(root, 'apps/web/src', file)).href, import.meta.url);
const pa = await load('publicAvailability.ts');
const b = await load('badges.ts');
let failed = 0;
const check = (name, fn) => { try { fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };

const MIN = 60_000, HOUR = 3_600_000, DAY = 86_400_000;
const json = value => JSON.parse(JSON.stringify(value));
/** The node's summarizeRange for sorted, disjoint spans: gaps are unknown, maintenance overlaps and removes nothing. */
function totals(start, end, spans) {
  const t = { startMs: start, endMs: end, upMs: 0, downMs: 0, pendingMs: 0, unknownMs: end - start, maintenanceMs: 0, knownMs: 0, availabilityPercent: null, coveragePercent: 0 };
  for (const span of spans) {
    const a = Math.max(start, span.s), z = Math.min(end, span.e);
    if (z <= a) continue;
    const d = z - a;
    if (span.state !== 'unknown') { t[`${span.state}Ms`] += d; t.unknownMs -= d; }
    if (span.m) t.maintenanceMs += d;
  }
  t.knownMs = t.upMs + t.downMs;
  t.coveragePercent = 100 * (t.knownMs / (end - start));
  if (t.knownMs > 0) t.availabilityPercent = 100 * (t.upMs / t.knownMs);
  return t;
}
/** An `available` field exactly as fillPublicAvailability builds it for a window ending at `to`. */
function metric(to, spans) {
  const from = to - DAY, buckets = [];
  for (let c = from; c < to;) { const e = Math.min(to, (Math.floor(c / HOUR) + 1) * HOUR); buckets.push(totals(c, e, spans)); c = e; }
  return json({ status: 'available', scope: 'serving-node', from, to, summary: totals(from, to, spans), buckets });
}

const TO = Date.UTC(2026, 9, 23, 12, 34, 30); // a 30-second frame, not on the hour: 25 buckets
const GEN = TO + 12_345;
const spans = [
  { s: TO - DAY - HOUR, e: TO - 5 * HOUR, state: 'up' },
  { s: TO - 5 * HOUR, e: TO - 5 * HOUR + 7 * MIN, state: 'down', m: true },
  { s: TO - 5 * HOUR + 7 * MIN, e: TO - 5 * HOUR + 9 * MIN, state: 'pending' },
  // 51 minutes without a fresh result (unknown), then up until one minute before the frame (unknown again)
  { s: TO - 4 * HOUR, e: TO - MIN, state: 'up' },
];
const good = metric(TO, spans);
const empty = metric(TO, []);
const read = value => pa.readPublicAvailability(value, GEN);
const mutate = change => { const v = structuredClone(good); change(v); return v; };

check('opt-out: an absent or null field draws nothing', () => {
  assert.equal(pa.readPublicAvailability(undefined, GEN), undefined);
  assert.equal(pa.readPublicAvailability(null, GEN), undefined);
});
check('available: 25 hourly UTC buckets with partial edges; totals kept exactly', () => {
  const r = read(good);
  assert.equal(r.kind, 'available');
  assert.equal(r.buckets.length, 25);
  assert.equal(r.buckets[0].startMs, TO - DAY);
  assert.equal(r.buckets[0].endMs % HOUR, 0);
  assert.equal(r.buckets[24].endMs, TO);
  assert.deepEqual(r.summary, good.summary);
  assert.equal(r.summary.downMs, 7 * MIN);
  assert.equal(r.summary.maintenanceMs, 7 * MIN);
  assert.equal(r.summary.unknownMs, 52 * MIN);
});
check('window starting on the hour has exactly 24 buckets', () => {
  const to = Date.UTC(2026, 9, 23, 12, 0, 0);
  const r = pa.readPublicAvailability(metric(to, [{ s: to - DAY, e: to, state: 'up' }]), to + 1);
  assert.equal(r.kind, 'available');
  assert.equal(r.buckets.length, 24);
  assert.equal(pa.observedFigures(r.summary).availability, 10000);
  assert.equal(pa.observedFigures(r.summary).coverage, 10000);
});
check('figures truncate like the badge: one failed millisecond never reads 100.00%', () => {
  assert.equal(pa.basisPoints(DAY - 1, DAY), 9999);
  assert.equal(pa.basisPoints(DAY, DAY), 10000);
  assert.equal(pa.basisPoints(1, DAY), 0);
  const to = Date.UTC(2026, 9, 23, 12, 0, 0);
  const r = pa.readPublicAvailability(metric(to, [{ s: to - DAY, e: to - 1, state: 'up' }, { s: to - 1, e: to, state: 'down' }]), to);
  const f = pa.observedFigures(r.summary);
  assert.equal(f.availability, 9999);
  assert.match(pa.percentText(f.availability), /^99[.,]99%$/u);
  assert.equal(f.coverage, 10000);
  const f2 = pa.observedFigures(read(good).summary);
  assert.equal(f2.availability, Math.floor(good.summary.upMs * 10000 / good.summary.knownMs));
  assert.equal(f2.coverage, Math.floor(good.summary.knownMs * 10000 / DAY));
});
check('empty history is available with zero coverage and unknown (not 0 % or 100 %) availability', () => {
  const r = read(empty);
  assert.equal(r.kind, 'available');
  assert.equal(r.summary.availabilityPercent, null);
  assert.deepEqual(pa.observedFigures(r.summary), { availability: null, coverage: 0, windowMs: DAY });
  assert.equal(pa.availabilityText(r.summary), 'unknown');
  assert.match(pa.stripWords(r.buckets), /no down time observed, unknown time in 25/u);
});
check('unavailable: fixed zero range and no totals; anything more is unreadable', () => {
  assert.deepEqual(read({ status: 'unavailable', scope: 'serving-node', from: 0, to: 0 }), { kind: 'unavailable' });
  assert.deepEqual(read({ status: 'unavailable', scope: 'serving-node' }), { kind: 'unavailable' });
  assert.deepEqual(read({ status: 'unavailable', scope: 'serving-node', from: 0, to: 0, summary: good.summary }), { kind: 'unreadable' });
  assert.deepEqual(read({ status: 'unavailable', scope: 'serving-node', from: good.from, to: good.to }), { kind: 'unreadable' });
});
check('generatedAt frame: the window ends on the 30 s frame containing the projection time', () => {
  assert.equal(pa.readPublicAvailability(good, TO).kind, 'available');
  assert.equal(pa.readPublicAvailability(good, TO + 29_999).kind, 'available');
  assert.equal(pa.readPublicAvailability(good, TO + 30_000).kind, 'unreadable');
  assert.equal(pa.readPublicAvailability(good, TO - 1).kind, 'unreadable');
});
// Accepted stricter contract (root review, 2026-10-23): the first frozen fixture expected
// `readPublicAvailability(good, 0).kind === 'available'` (a malformed generatedAt skipped the frame rule). An available
// metric now needs a valid positive projection time; without one it is unreadable, never quantified history.
check('generatedAt missing or invalid: an available metric is unreadable; unavailable stays unavailable', () => {
  for (const at of [0, -1, -TO, TO + 0.5, Number.NaN, Number.POSITIVE_INFINITY, Number.MAX_SAFE_INTEGER + 1, undefined, null, String(GEN)]) {
    assert.deepEqual(pa.readPublicAvailability(good, at), { kind: 'unreadable' }, String(at));
    assert.deepEqual(pa.readPublicAvailability(empty, at), { kind: 'unreadable' }, `empty ${String(at)}`);
    assert.deepEqual(pa.readPublicAvailability({ status: 'unavailable', scope: 'serving-node', from: 0, to: 0 }, at), { kind: 'unavailable' }, `unavailable ${String(at)}`);
  }
  assert.equal(pa.readPublicAvailability(good, GEN).kind, 'available');
});
const malformed = {
  'unknown scope': v => { v.scope = 'cluster'; },
  'missing scope': v => { delete v.scope; },
  'unknown status': v => { v.status = 'partial'; },
  'array instead of object': () => {},
  'window not 24 h': v => { v.from += HOUR; v.summary.startMs += HOUR; },
  'end off the 30 s frame': v => { v.from += 1_000; v.to += 1_000; v.summary.startMs += 1_000; v.summary.endMs += 1_000; },
  'string range': v => { v.from = String(v.from); },
  'missing summary': v => { delete v.summary; },
  'missing buckets': v => { delete v.buckets; },
  'one bucket dropped': v => { v.buckets.pop(); },
  'extra bucket': v => { v.buckets.push(v.buckets[24]); },
  'bucket boundary shifted': v => { v.buckets[0].endMs += 1; v.buckets[1].startMs += 1; },
  'bucket identity broken (up + 1)': v => { v.buckets[3].upMs += 1; },
  'healthier bucket with consistent identity': v => { const t = v.buckets[19]; t.upMs += t.downMs; t.downMs = 0; },
  'summary made fully up': v => { const s = v.summary; s.upMs += s.downMs + s.pendingMs + s.unknownMs; s.downMs = s.pendingMs = s.unknownMs = 0; s.knownMs = s.upMs; s.availabilityPercent = 100; s.coveragePercent = 100; },
  'coverage percent drift': v => { v.summary.coveragePercent += 0.001; },
  'availability percent rounded up': v => { v.summary.availabilityPercent = 100; },
  'availability percent missing with known time': v => { delete v.summary.availabilityPercent; },
  'zero percent where nothing is known': v => { v.buckets[0].availabilityPercent = 0; },
  'negative duration': v => { v.buckets[2].pendingMs = -1; v.buckets[2].unknownMs += 1; },
  'fractional duration': v => { v.buckets[2].unknownMs += 0.5; v.buckets[2].upMs -= 0.5; },
  'numeric string duration': v => { v.buckets[2].upMs = String(v.buckets[2].upMs); },
  'maintenance beyond the bucket': v => { v.buckets[4].maintenanceMs = HOUR + 1; },
  'knownMs not up + down': v => { v.summary.knownMs += 1; },
};
check('malformed available payloads are unreadable as a whole, never partly drawn or healthy', () => {
  for (const [name, change] of Object.entries(malformed)) {
    const value = name === 'array instead of object' ? [good] : name === 'zero percent where nothing is known' ? structuredClone(empty) : mutate(change);
    if (name === 'zero percent where nothing is known') change(value);
    assert.deepEqual(read(value), { kind: 'unreadable' }, name);
  }
  assert.equal(read(empty).kind, 'available');
});
check('words: buckets and strip summaries name down, unknown and maintenance time', () => {
  const r = read(good);
  const hit = r.buckets.find(bucket => bucket.downMs > 0);
  assert.match(pa.observedWords(hit), /down 7 min/u);
  assert.match(pa.observedWords(hit), /maintenance 7 min, still counted/u);
  assert.match(pa.stripWords(r.buckets), /^Hourly observed time, oldest first: down time in 1 of 25 hourly periods, unknown time in \d+, maintenance in 1\.$/u);
});
check('editor selection: no opt-in writes the pre-protocol-15 bytes; flags only when on; unknown fields kept', () => {
  const old = { monitorId: 'm-2', name: 'Web', showCertificate: true, showTags: true };
  assert.equal(JSON.stringify(pa.writeSelection(pa.selectionFields(old), pa.otherFields(old, pa.SELECTION_KEYS))), JSON.stringify(old));
  const plain = { monitorId: 'm-1', name: '  API  ', showCertificate: false, futureField: { keep: [1] } };
  const other = pa.otherFields(plain, pa.SELECTION_KEYS);
  assert.deepEqual(other, { futureField: { keep: [1] } });
  assert.notEqual(other.futureField, plain.futureField);
  const saved = pa.writeSelection(pa.selectionFields(plain), other);
  assert.deepEqual(saved, { futureField: { keep: [1] }, monitorId: 'm-1', name: 'API', showCertificate: false });
  assert.equal('showAvailability' in saved, false);
  const on = pa.writeSelection({ ...pa.selectionFields(old), showAvailability: true }, {});
  assert.equal(JSON.stringify(on), JSON.stringify({ ...old, showAvailability: true }));
  assert.equal(pa.selectionFields(on).showAvailability, true);
  const off = pa.writeSelection({ ...pa.selectionFields(on), showAvailability: false }, pa.otherFields(on, pa.SELECTION_KEYS));
  assert.equal(JSON.stringify(off), JSON.stringify(old));
  assert.equal(pa.selectionFields({ monitorId: 'x', name: 'y', showCertificate: false, showAvailability: 'yes' }).showAvailability, false);
});
check('badge addresses: uptime metric only with one service, exact parameter order, .svg route', () => {
  const node = 'https://canary.example.com';
  assert.equal(b.badgeUrl(node, 'services'), `${node}/public/badge/services.svg`);
  assert.equal(b.badgeUrl(node, 'services', { group: 'web', monitor: 'API' }), `${node}/public/badge/services.svg?group=web&monitor=API`);
  const url = new URL(b.badgeUrl(node, 'services', { group: 'web', monitor: 'API & docs (v2)' }, 'uptime'));
  assert.equal(url.origin + url.pathname, `${node}/public/badge/services.svg`);
  assert.deepEqual([...url.searchParams], [['group', 'web'], ['monitor', 'API & docs (v2)'], ['metric', 'uptime']]);
  assert.throws(() => b.badgeUrl(node, 'services', undefined, 'uptime'));
  assert.throws(() => b.badgeUrl(`${node}/?token=x`, 'services', { group: 'web', monitor: 'API' }, 'uptime'));
});
check('badge targets: observed flag is carried; duplicates stay unusable for both badges', () => {
  const targets = b.badgeTargets([
    { id: 'web', name: 'Web', monitors: [{ name: 'API', observed: true }, { name: 'Site' }, { name: 'Dup', observed: true }, { name: 'Dup' }] },
    { id: 'web', name: 'Web again', monitors: [{ name: 'API' }] },
  ]);
  assert.deepEqual(targets.map(t => [t.group, t.monitor, t.matches, t.observed]), [
    ['web', 'API', 2, true], ['web', 'Site', 1, false], ['web', 'Dup', 2, true], ['web', 'Dup', 2, false], ['web', 'API', 2, false],
  ]);
});
check('edited sources contain no raw line/paragraph separators, NEL or BOM', () => {
  const files = ['publicAvailability.ts', 'badges.ts', 'ui/badges.tsx', 'ui/charts.tsx', 'views/PublicStatus.tsx', 'views/StatusPages.tsx', 'views/Config.tsx', 'styles.css'];
  const bad = [0x2028, 0x2029, 0x85, 0xfeff].map(code => String.fromCodePoint(code));
  for (const file of files) {
    const text = readFileSync(resolve(root, 'apps/web/src', file), 'utf8');
    for (const ch of bad) assert.equal(text.includes(ch), false, `${file} contains U+${ch.codePointAt(0).toString(16).padStart(4, '0')}`);
  }
});

console.log(failed ? `${failed} FAILED` : 'ALL PASSED');
process.exit(failed ? 1 : 0);
