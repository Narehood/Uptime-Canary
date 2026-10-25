import assert from 'node:assert/strict';
import { finishInterception } from './cdp-interception.mjs';

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
