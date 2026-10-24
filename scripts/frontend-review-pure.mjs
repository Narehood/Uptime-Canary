// Pure regressions for the 2026-10-24 frontend review fixes: Mailgun region round trip (M1), /api/auth/me failures
// during connect (L2), masked JSON import refusal (L4), unknown service fields in the masked preview (L5), and
// sign-out of a password session whose connect fails or is cancelled (L6).
// Imports the source in place through one tsx namespace, so session.ts and this script share one ApiFailure class.
// No copy, no scratch, no network, no output files. Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/frontend-review-pure.mjs   (exit 1 on any failure)
// The fake APIs below stand in for CanaryApi; the node's own contract stays with the contract harness.
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

process.env.TSX_DISABLE_CACHE = '1';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
globalThis.document ??= { addEventListener() {} };
const reactModule = await import('react');
globalThis.React ??= reactModule.default ?? reactModule;
const { register } = await import('tsx/esm/api');
const scope = register({ namespace: 'frontend-review-pure', tsconfig: resolve(root, 'apps/web/tsconfig.json') });
const load = file => scope.import(pathToFileURL(resolve(root, 'apps/web/src', file)).href, import.meta.url);
const api = await load('api.ts');
const sess = await load('session.ts');
const fmt = await load('format.ts');
const { channelFormModel: cf } = await load('views/Channels.tsx');
const { previewModel: pm } = await load('views/MonitorForm.tsx');
let failed = 0;
const check = async (name, fn) => { try { await fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };

const MASK = String.fromCodePoint(0x2022).repeat(3);

// ---------- M1: Mailgun region

const mailgunChannel = mailgun => ({
  id: 'review-mailgun', name: 'Review Mailgun', enabled: true, revision: 'rev-1', updatedAt: 1,
  config: { kind: 'mailgun', mailgun, sender: { email: 'alerts@example.test' }, recipients: [{ email: 'oncall@example.test' }] },
});
const edited = (channel, change = {}) => {
  const draft = { ...cf.channelDraft(channel), ...change };
  draft.name += ' renamed';
  const built = cf.buildChannel(draft, channel);
  assert.ok(built.input, JSON.stringify(built.errors));
  return built.input.config;
};

await check('M1 Mailgun: a stored channel without a region survives a rename exactly, with no "US" written', () => {
  const stored = mailgunChannel({ apiKey: 'review-mailgun-key', domain: 'mg.example.test' });
  const config = edited(stored);
  assert.deepEqual(config, stored.config);
  assert.equal(Object.hasOwn(config.mailgun, 'region'), false, 'the raw region is part of each recipient destination ID');
});

await check('M1 Mailgun: stored US and EU regions round-trip; choosing a region writes it', () => {
  for (const region of ['US', 'EU']) {
    const stored = mailgunChannel({ apiKey: 'review-mailgun-key', domain: 'mg.example.test', region });
    assert.deepEqual(edited(stored), stored.config, region);
  }
  assert.equal(edited(mailgunChannel({ apiKey: 'k', domain: 'mg.example.test' }), { mailgunRegion: 'EU' }).mailgun.region, 'EU');
  assert.equal(edited(mailgunChannel({ apiKey: 'k', domain: 'mg.example.test', region: 'EU' }), { mailgunRegion: 'US' }).mailgun.region, 'US');
});

await check('M1 Mailgun: a new channel leaves the default US region out', () => {
  const draft = { ...cf.channelDraft(undefined), name: 'New Mailgun', kind: 'mailgun', mailgunKey: 'review-mailgun-key', mailgunDomain: 'mg.example.test', senderEmail: 'alerts@example.test' };
  draft.recipients = [{ key: 1, email: 'oncall@example.test', name: '' }];
  const built = cf.buildChannel(draft, undefined);
  assert.ok(built.input, JSON.stringify(built.errors));
  assert.deepEqual(built.input.config.mailgun, { apiKey: 'review-mailgun-key', domain: 'mg.example.test' });
});

// ---------- L2 and L6: Session.connect

const OVERVIEW = { node: { id: 'review-a' }, user: { name: 'review', role: 'admin' }, monitors: [] };
const ACCOUNT = { id: 'review-account', name: 'review', role: 'admin', passwordEnabled: true, mfaEnabled: false };
const fakeApi = ({ credential = 'token', overview = async () => OVERVIEW, me = async () => ACCOUNT } = {}) => {
  const fake = { credential, logouts: 0, overview, me, logout: async () => { fake.logouts++; }, sameCredential: other => other === fake };
  return fake;
};
const failing = status => async () => { throw new api.ApiFailure(status, `Review failure ${status}.`); };
const aborted = () => new DOMException('The operation was aborted.', 'AbortError');
const close = session => { const connection = session.getSnapshot().connection; if (connection) session.end({ url: 'http://127.0.0.1:1', message: 'review', tone: 'info' }, connection); };

await check('L2 connect: an /api/auth/me failure other than 404 fails the connect and keeps the current connection', async () => {
  for (const status of [503, 409, 500]) {
    const session = new sess.Session();
    await assert.rejects(session.connect(fakeApi({ me: failing(status) })), e => e instanceof api.ApiFailure && e.status === status);
    assert.equal(session.getSnapshot().connection, null, `${status}: nothing connected`);
    const working = fakeApi();
    await session.connect(working);
    await assert.rejects(session.connect(fakeApi({ me: failing(status) })), e => e instanceof api.ApiFailure && e.status === status);
    assert.equal(session.getSnapshot().connection.api, working, `${status}: the working connection stays`);
    close(session);
  }
});

await check('L2 connect: 404 (a node older than accounts) connects with account null; an answer keeps the account', async () => {
  const session = new sess.Session();
  await session.connect(fakeApi({ me: failing(404) }));
  assert.equal(session.getSnapshot().connection.account, null);
  close(session);
  await session.connect(fakeApi());
  assert.deepEqual(session.getSnapshot().connection.account, ACCOUNT);
  close(session);
});

await check('L6 connect: a new password session is signed out when its connect fails, is cancelled, or answers after Cancel', async () => {
  const session = new sess.Session();
  const broken = fakeApi({ credential: 'session', overview: failing(500) });
  await assert.rejects(session.connect(broken), e => e instanceof api.ApiFailure && e.status === 500);
  assert.equal(broken.logouts, 1, 'overview failure');

  const noAccount = fakeApi({ credential: 'session', me: failing(503) });
  await assert.rejects(session.connect(noAccount), e => e instanceof api.ApiFailure && e.status === 503);
  assert.equal(noAccount.logouts, 1, '/api/auth/me failure');

  const controller = new AbortController();
  const waiting = fakeApi({ credential: 'session', overview: signal => new Promise((_, reject) => signal.addEventListener('abort', () => reject(aborted()), { once: true })) });
  const attempt = session.connect(waiting, controller.signal);
  controller.abort();
  await assert.rejects(attempt, e => e.name === 'AbortError');
  assert.equal(waiting.logouts, 1, 'cancelled while waiting');

  const late = fakeApi({ credential: 'session' });
  const cancelled = new AbortController();
  cancelled.abort();
  await assert.rejects(session.connect(late, cancelled.signal), e => e.name === 'AbortError');
  assert.equal(late.logouts, 1, 'answered after Cancel');
  assert.equal(session.getSnapshot().connection, null);
});

await check('L6 connect: tokens are never revoked, a connected session is kept, and a replaced session is still ended', async () => {
  const session = new sess.Session();
  const token = fakeApi({ overview: failing(500) });
  await assert.rejects(session.connect(token));
  assert.equal(token.logouts, 0, 'account tokens stay valid on the node');

  const live = fakeApi({ credential: 'session' });
  await session.connect(live);
  assert.equal(live.logouts, 0);
  const broken = fakeApi({ credential: 'session', overview: failing(500) });
  await assert.rejects(session.connect(broken));
  assert.equal(broken.logouts, 1);
  assert.equal(live.logouts, 0, 'a failed switch leaves the live session alone');
  assert.equal(session.getSnapshot().connection.api, live);

  const next = fakeApi();
  await session.connect(next);
  assert.equal(live.logouts, 1, 'the replaced session is signed out on its node');
  assert.equal(next.logouts, 0);
  close(session);
});

// ---------- L4 and L5: masked preview

const PREVIEW_SECRETS = [
  ['http.headers.X-Key', { type: 'http', http: { headers: { 'X-Key': 'header-secret' } } }],
  ['http.basicAuth.password', { type: 'http', http: { basicAuth: { username: 'review', password: 'basic-secret' } } }],
  ['http.bearerToken', { type: 'http', http: { bearerToken: 'bearer-secret' } }],
  ['http.body', { type: 'http', http: { method: 'POST', body: 'body-secret' } }],
  ['http.oauth2.clientSecret', { type: 'http', http: { oauth2: { tokenUrl: 'https://auth.example.test/token', clientId: 'review', clientSecret: 'client-secret' } } }],
  ['websocket.bearerToken', { type: 'websocket', websocket: { bearerToken: 'ws-secret' } }],
  ['tls.clientKeyPem', { type: 'tls', tls: { clientCertPem: 'review-certificate', clientKeyPem: 'review-key' } }],
  ['webhookUrl', { type: 'http', webhookUrl: 'https://hooks.example.test/private-path' }],
  ['push.token', { type: 'push', push: { token: 'a'.repeat(64) } }],
  ['globalping.token', { type: 'globalping', globalping: { token: 'gp-secret' } }],
  ['steam.apiKey', { type: 'steam', steam: { apiKey: 'ABCDEF0123456789ABCDEF0123456789' } }],
  ['service.redis.password', { type: 'redis', service: { redis: { address: 'cache.internal:6379', password: 'redis-secret' } } }],
];

await check('L4 import: every placeholder the masked preview copies is found; revealed values and ordinary JSON pass', () => {
  for (const [path, input] of PREVIEW_SECRETS) {
    const base = { name: 'Review', target: 'review.example.test', ...input };
    assert.equal(fmt.maskedPath(base), undefined, `revealed ${path}`);
    assert.equal(fmt.maskedPath(JSON.parse(JSON.stringify(pm.masked(base)))), path);
  }
  assert.equal(fmt.MASK, MASK);
  // Was `x${MASK}`, which F2 below makes ordinary text; the array path is what this line checks.
  assert.equal(fmt.maskedPath({ a: [{ b: 'ok' }, { b: `https://x.example.test/${MASK}` }] }), 'a[1].b');
  assert.equal(fmt.maskedPath({ name: 'API', type: 'http', target: 'https://app.example.test/', expectedStatusCodes: [200], enabled: true, http: null }), undefined);
});

await check('F2 import: text that merely contains the mask is not a placeholder; every preview placeholder still is', () => {
  for (const text of [`Checkout ${MASK}`, `${MASK} beta`, `x${MASK}`, `a/${MASK}/b`, MASK.repeat(2) + 'x'])
    assert.equal(fmt.maskedPath({ name: text, http: { keyword: text } }), undefined, text);
  assert.equal(fmt.maskedPath(MASK), '(the whole value)');
  assert.equal(fmt.maskedPath({ name: `Checkout ${MASK}`, http: { bearerToken: MASK } }), 'http.bearerToken');
  for (const [path, input] of PREVIEW_SECRETS) {
    const preview = JSON.parse(JSON.stringify(pm.masked({ name: `Checkout ${MASK}`, target: 'review.example.test', ...input })));
    assert.equal(fmt.maskedPath(preview), path, `named ${path}`);
  }
  const plain = pm.masked({ name: `Checkout ${MASK}`, type: 'http', target: 'https://app.example.test/', http: { method: 'GET' } });
  assert.equal(fmt.maskedPath(JSON.parse(JSON.stringify(plain))), undefined, 'a preview with no credentials fills');
});

await check('L5 preview: unknown service siblings are shown as sent; every protocol object keeps its secrets hidden', () => {
  const input = {
    name: 'Review', type: 'redis', target: 'Redis',
    service: {
      redis: { address: 'cache.internal:6379', password: 'redis-secret', futureOption: { nullable: null } },
      futureEnvelope: { enabled: true },
      postgres: { dsn: 'postgres://review:secret@db.internal/app' },
    },
  };
  const preview = pm.masked(input);
  assert.deepEqual(Object.keys(preview.service).sort(), Object.keys(input.service).sort());
  assert.deepEqual(preview.service.futureEnvelope, { enabled: true });
  assert.deepEqual(preview.service.redis, { address: 'cache.internal:6379', password: MASK, futureOption: { nullable: null } });
  assert.equal(preview.service.postgres.dsn, MASK);
  assert.equal(input.service.redis.password, 'redis-secret', 'the input itself is untouched');
});

await check('F1 preview: credential-named text in unknown service fields is hidden; other unknown and known values stay as sent', () => {
  const input = {
    name: 'Review', type: 'redis', target: 'Redis',
    service: {
      redis: {
        address: 'cache.internal:6379', password: 'redis-secret', sentinelPassword: 'nested-secret',
        tls: { serverName: 'cache.internal', clientKeyPem: 'tls-key', futureKeyId: 'tls-nested-secret' },
        futureOption: { nullable: null, apiToken: 'option-secret', label: 'plain' },
      },
      futureEnvelope: { enabled: true, token: 'future-secret', region: 'eu', retries: 3, headers: { 'X-Key': 'header-secret' }, items: [{ secretRef: 'list-secret', label: 'plain' }] },
      futureSecret: 'sibling-secret',
      sftp: { address: 'files.internal:22', username: 'review', hostKeySha256: 'SHA256:review-fingerprint', unsafeIgnoreHostKey: false, privateKeyPem: 'sftp-key' },
    },
  };
  const preview = pm.masked(input);
  const text = JSON.stringify(preview);
  for (const secret of ['redis-secret', 'nested-secret', 'tls-key', 'tls-nested-secret', 'option-secret', 'future-secret', 'header-secret', 'list-secret', 'sibling-secret', 'sftp-key'])
    assert.ok(!text.includes(secret), secret);
  assert.deepEqual(preview.service.futureEnvelope, { enabled: true, token: MASK, region: 'eu', retries: 3, headers: { 'X-Key': MASK }, items: [{ secretRef: MASK, label: 'plain' }] });
  assert.equal(preview.service.futureSecret, MASK);
  assert.deepEqual(preview.service.redis, {
    address: 'cache.internal:6379', password: MASK, sentinelPassword: MASK,
    tls: { serverName: 'cache.internal', clientKeyPem: MASK, futureKeyId: MASK },
    futureOption: { nullable: null, apiToken: MASK, label: 'plain' },
  });
  // Known fields keep their own rules: a host key fingerprint is not a credential, though its name has "key".
  assert.deepEqual(preview.service.sftp, { address: 'files.internal:22', username: 'review', hostKeySha256: 'SHA256:review-fingerprint', unsafeIgnoreHostKey: false, privateKeyPem: MASK });
  assert.equal(fmt.maskedPath(JSON.parse(text)), 'service.redis.password', 'a copy of this preview is still refused');
  assert.equal(input.service.futureEnvelope.token, 'future-secret', 'the input itself is untouched');
});

await check('F1 secretNamed: the Config pattern moved to format.ts unchanged', () => {
  for (const key of ['password', 'clientSecret', 'apiToken', 'apiKey', 'dsn', 'privateKeyPassphrase', 'community', 'body', 'headers', 'metadata', 'basicAuth', 'proxyUrl'])
    assert.equal(fmt.secretNamed(key), true, key);
  for (const key of ['apiVersion', 'keyword', 'enabled', 'region', 'address', 'username']) assert.equal(fmt.secretNamed(key), false, key);
});

await scope.unregister();
console.log(failed ? `${failed} FAILED` : 'ALL PASS');
process.exitCode = failed ? 1 : 0;
