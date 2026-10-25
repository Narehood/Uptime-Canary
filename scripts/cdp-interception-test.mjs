import assert from 'node:assert/strict';
import { finishInterception, interceptionTracker } from './cdp-interception.mjs';

let canceled = 0; const failures = [], calls = [];
const outcome = { canceled() { canceled++; }, failed(error) { failures.push(error.message); } };
const request = { requestId: 'private-loopback-fixture' };
await finishInterception({ send: async (...args) => { calls.push(args); } }, 'Fetch.continueRequest', request, outcome);
assert.deepEqual(calls, [['Fetch.continueRequest', request]]);
assert.equal(canceled, 0); assert.deepEqual(failures, []);
for (const method of ['Fetch.continueRequest','Fetch.failRequest','Fetch.fulfillRequest']) {
  await finishInterception({ send: async () => { throw Error('Invalid InterceptionId.'); } }, method, request, outcome);
}
assert.equal(canceled, 3); assert.deepEqual(failures, []);
await finishInterception({ send: async () => { throw Error('Invalid InterceptionId.'); } }, 'Page.navigate', request, outcome);
await finishInterception({ send: async () => { throw Error('Permission denied'); } }, 'Fetch.continueRequest', request, outcome);
await finishInterception({ send: async () => { throw Error('WebSocket closed'); } }, 'Fetch.failRequest', request, outcome);
assert.deepEqual(failures, ['Invalid InterceptionId.','Permission denied','WebSocket closed']);
assert.equal(canceled, 3);
console.log('PASS CDP continuation preserves request, records canceled interception IDs, and retains all other protocol failures');

// No timer or real browser: hold the command past the final step, start
// shutdown, then deliver its error. Reporting cannot finish ahead of it.
let rejectCommand, stopped = false, lateWork = false;
const lateFailures = [];
const tracker = interceptionTracker(error => lateFailures.push(error.message));
tracker.run(() => finishInterception({ send: () => new Promise((_, reject) => { rejectCommand = reject; }) },
  'Fetch.continueRequest', request, { canceled() { assert.fail('not a stale ID'); }, failed: error => lateFailures.push(error.message) }));
await Promise.resolve();
const settling = tracker.stop().then(() => { stopped = true; });
tracker.run(() => { lateWork = true; });
await Promise.resolve();
assert.equal(stopped, false, 'the report must wait for outstanding CDP commands');
assert.equal(lateWork, false, 'shutdown admits no new interception work');
assert.deepEqual(lateFailures, []);
rejectCommand(Error('CDP timeout: Fetch.continueRequest'));
await settling;
assert.equal(stopped, true);
assert.deepEqual(lateFailures, ['CDP timeout: Fetch.continueRequest'], 'the final report includes the late failure');
// A custom request hook is tracked too; a synchronous callback error also
// remains a review failure. Precise stale IDs still count as cancellations.
let releaseHook, hookDone = false;
const hooks = interceptionTracker(error => lateFailures.push(error.message));
hooks.run(async () => { await new Promise(resolve => { releaseHook = resolve; }); hookDone = true; });
hooks.run(() => { throw Error('hook failed'); });
await Promise.resolve();
const hookStop = hooks.stop();
assert.equal(hookDone, false);
releaseHook(); await hookStop;
assert.equal(hookDone, true);
assert.equal(lateFailures.at(-1), 'hook failed');
console.log('PASS CDP shutdown drains late command and hook outcomes, rejects new work, and reports delayed protocol failures before completion');
