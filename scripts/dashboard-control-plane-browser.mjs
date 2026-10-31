// Real Go dashboard account server behind a private cookie-gated proxy, with its dedicated node-connection listener behind
// an owned loopback HTTPS front that only a private production node dials.
// Reuses the bounded harness's one executable, web build and browser profile.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { once } from 'node:events';
import { existsSync, readFileSync } from 'node:fs';
import { createServer, request as upstreamRequest } from 'node:http';
import { createServer as createHttpsServer } from 'node:https';
import { dirname, join, resolve } from 'node:path';
import { pinnedFixtureFetch } from './tls-fixture.mjs';
import { reviewFleet } from './fleet-browser.mjs';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
// The public loopback test certificate (it names 127.0.0.1) and its key, read in place from the source tree; nothing is
// copied. The node that connects to the dashboard trusts this certificate and nothing else.
const TRUST = join(root, 'apps', 'proof', 'fixtures', 'localhost-cert.pem');
const TRUST_KEY = join(root, 'apps', 'proof', 'fixtures', 'localhost-key.pem');
// The only requests the dashboard's node-connection listener serves to nodes (dashboard_connection_http.go), all POST.
const CONNECTION_ROUTES = ['/connect/v1/enroll', '/connect/v1/open', '/connect/v1/poll', '/connect/v1/result'];
// cmd/canary/main.go prints this once the node has started with CANARY_DASHBOARD_URL.
const FINGERPRINT_LINE = /^Node connection fingerprint \(compare before confirming\): ([0-9a-f]{64})\r?$/mu;

export async function createDashboardControlPlaneFixture({ runDir, port, env, owned, servers, until, secrets }) {
  const backend = `http://127.0.0.1:${await port()}`;
  const origin = `http://127.0.0.1:${await port()}`;
  const identity = join(runDir, 'dashboard-identity');
  const password = 'Private dashboard browser fixture password';
  const proxyCookie = 'canary_dashboard_fixture_proxy';
  let session = '', denied = 0, authenticated = 0;
  let updatePreview = null;
  let onboardingPreview = null;
  const paths = [];
  const watchers = new Set();
  // Test-only: one dashboard answer held back after the dashboard's server has answered it, so a step can look at the
  // page while its request is still pending (hold() below). Only the next request with that method and path is held.
  // Its answer stays in memory, at most 64 KiB, until release(), a 20 s limit, or the page giving up on the request,
  // which drops it.
  let holding = null;
  const proxy = createServer((req, res) => {
    let entry = null;
    if (req.url === '/fixture/sign-in') {
      session = randomBytes(24).toString('hex');
      res.writeHead(302, { 'Set-Cookie': `${proxyCookie}=${session}; Path=/; HttpOnly; SameSite=Lax`, Location: '/', 'Cache-Control': 'no-store' }).end();
      return;
    }
    if (req.url === '/fixture/sign-in-required') {
      res.writeHead(200, { 'Content-Type': 'text/html', 'Cache-Control': 'no-store' }).end('<h1>Private proxy sign-in required</h1>');
      return;
    }
    const valid = session && (req.headers.cookie ?? '').split(';').some(value => value.trim() === `${proxyCookie}=${session}`);
    if (!valid) { denied++; res.writeHead(302, { Location: '/fixture/sign-in-required', 'Cache-Control': 'no-store' }).end(); return; }
    if (req.url.startsWith('/api/')) {
      if (req.headers.authorization) authenticated++;
      assert.ok(paths.length < 1000, 'bounded dashboard proxy request evidence');
      entry = { path: req.url.split('?')[0], method: req.method, authorization: Boolean(req.headers.authorization), registration: req.headers['x-canary-registered-node'] ?? null };
      paths.push(entry);
      for (const watch of watchers) watch(entry);
    }
    const held = entry && holding && !holding.taken && holding.method === req.method && holding.path === entry.path ? holding : null;
    if (held) held.taken = true;
    const headers = { ...req.headers, host: new URL(backend).host };
    delete headers.cookie;
    const upstream = upstreamRequest(new URL(req.url, backend), { method: req.method, headers, timeout: 14000 }, response => {
      if (entry) entry.status = response.statusCode;
      const returned = { ...response.headers }; delete returned['set-cookie'];
      // UI-only sample metadata, after the real server authenticated this GET.
      // All POSTs still reach the disabled real service, so this fixture never
      // queries GitHub/GHCR or grants authority through a sample answer.
      if (updatePreview && entry?.path === '/api/dashboard/updates' && req.method === 'GET' && response.statusCode === 200) {
        response.resume();
        delete returned['content-length']; delete returned['content-encoding'];
        res.writeHead(200, { ...returned, 'content-type': 'application/json' }).end(JSON.stringify(updatePreview));
        return;
      }
      // Only the authenticated capability read is overridden; invitations and
      // all other writes still reach the real dashboard's authorization checks.
      if (onboardingPreview && entry?.path === '/api/dashboard/onboarding' && req.method === 'GET' && response.statusCode === 200) {
        response.resume();
        delete returned['content-length']; delete returned['content-encoding'];
        res.writeHead(200, { ...returned, 'content-type': 'application/json' }).end(JSON.stringify(onboardingPreview));
        return;
      }
      if (held) {
        const chunks = []; let size = 0;
        response.on('data', chunk => { size += chunk.length; if (size <= 65536) chunks.push(chunk); });
        response.on('end', () => {
          held.answered = { status: response.statusCode, bytes: size };
          void held.released.then(() => {
            if (res.destroyed || res.writableEnded) { held.dropped = true; return; }
            if (size > 65536) { res.writeHead(502, { 'Cache-Control': 'no-store' }).end(); return; }
            res.writeHead(response.statusCode, returned).end(Buffer.concat(chunks));
            held.delivered = true;
          });
        });
        return;
      }
      res.writeHead(response.statusCode, returned); response.pipe(res);
    });
    upstream.on('timeout', () => upstream.destroy());
    upstream.on('error', () => { if (!res.headersSent) res.writeHead(502); res.end(); });
    res.on('close', () => { if (!res.writableEnded) upstream.destroy(); });
    req.pipe(upstream);
  });
  proxy.listen(Number(new URL(origin).port), '127.0.0.1'); await once(proxy, 'listening'); servers.push(proxy);
  // The dashboard's dedicated node-connection listener, which RunDashboard opens when CANARY_DASHBOARD_CONNECTION_URL is
  // set: plain HTTP on its own fresh loopback port, never the default 8081, which a user's app or another fixture may
  // hold. Nodes reach it only through this owned HTTPS front, which serves the loopback test certificate at the
  // dashboard's node-connection address. The front forwards only the four fixed connection POSTs, with their full path
  // and the node's own Authorization and X-Canary-Registration headers, never a cookie, and adds no Origin. It keeps only
  // bounded counts in memory and adds the bearer credentials it carries to the redaction list. The browser never gets
  // either address, and the harness's request filter allows neither.
  const connectionBackend = `http://127.0.0.1:${await port()}`;
  const connectionUrl = `https://127.0.0.1:${await port()}`;
  const transport = { forwarded: 0, refused: [], overflow: 0, cookies: 0, origins: 0, unauthenticated: 0, unregistered: 0, tlsErrors: 0, routes: Object.fromEntries(CONNECTION_ROUTES.map(path => [path, 0])) };
  const front = createHttpsServer({ cert: readFileSync(TRUST), key: readFileSync(TRUST_KEY) }, (req, res) => {
    if (req.headers.cookie !== undefined) transport.cookies++;
    if (req.headers.origin !== undefined) transport.origins++;
    if (req.method !== 'POST' || !CONNECTION_ROUTES.includes(req.url)) {
      if (transport.refused.length < 32) transport.refused.push(`${req.method} ${String(req.url).slice(0, 96)}`);
      req.resume(); res.writeHead(404, { 'Cache-Control': 'no-store' }).end(); return;
    }
    if (transport.forwarded >= 4000) { transport.overflow++; req.resume(); res.writeHead(503, { 'Cache-Control': 'no-store' }).end(); return; }
    transport.forwarded++; transport.routes[req.url]++;
    const bearer = /^Bearer (\S+)$/u.exec(req.headers.authorization ?? '')?.[1];
    if (bearer) secrets.add(bearer); else transport.unauthenticated++;
    if (req.url !== '/connect/v1/enroll' && !req.headers['x-canary-registration']) transport.unregistered++;
    const headers = { ...req.headers, host: new URL(connectionBackend).host };
    delete headers.cookie;
    // The node's long poll is answered within 15 s (nodeconnection.PollWait); its client gives up after 22 s.
    const upstream = upstreamRequest(new URL(req.url, connectionBackend), { method: 'POST', headers, timeout: 25000 }, response => {
      res.writeHead(response.statusCode, response.headers); response.pipe(res);
    });
    upstream.on('timeout', () => upstream.destroy());
    upstream.on('error', () => { if (!res.headersSent) res.writeHead(502); res.end(); });
    res.on('close', () => { if (!res.writableEnded) upstream.destroy(); });
    req.pipe(upstream);
  });
  // Idle connections close from the node's side first (its transport's IdleConnTimeout is 30 s): Go doesn't retry a POST
  // whose reused connection the server closed meanwhile, so a shorter server keep-alive would make the session flap.
  front.keepAliveTimeout = 35000;
  front.on('tlsClientError', () => { transport.tlsErrors++; });
  front.listen(Number(new URL(connectionUrl).port), '127.0.0.1'); await once(front, 'listening'); servers.push(front);
  const child = spawn(join(runDir, process.platform === 'win32' ? 'review-node.exe' : 'review-node'), ['dashboard'], {
    cwd: runDir, env: { ...env, CANARY_DASHBOARD_DATA: join(runDir, 'dashboard-data'), CANARY_DASHBOARD_IDENTITY: identity,
      CANARY_DASHBOARD_ASSETS: join(runDir, 'dist'), CANARY_DASHBOARD_HOST: '127.0.0.1',
      CANARY_DASHBOARD_PORT: new URL(backend).port, CANARY_DASHBOARD_ORIGINS: origin, CANARY_UPDATE_CHECKS: 'false',
      CANARY_DASHBOARD_CONNECTION_URL: connectionUrl, CANARY_DASHBOARD_CONNECTION_HOST: '127.0.0.1', CANARY_DASHBOARD_CONNECTION_PORT: new URL(connectionBackend).port },
    windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'],
  });
  owned.push(child);
  let log = ''; for (const stream of [child.stdout, child.stderr]) stream.on('data', value => { log = (log + value).slice(-16000); });
  await until(async () => {
    if (child.exitCode !== null) throw Error('Private dashboard fixture exited during startup');
    try {
      return (await fetch(backend + '/healthz')).ok && (await fetch(connectionBackend + '/healthz')).ok && existsSync(join(identity, 'setup.code'));
    } catch { return false; }
  }, 'private dashboard account service and its node-connection listener', 30000);
  const code = readFileSync(join(identity, 'setup.code'), 'utf8').trim();
  secrets.add(code); secrets.add(password);
  // A second loopback route to an existing private node, for address moves. It forwards every request and answer
  // unchanged (the node's identity header included). In memory only, it remembers which bearer credentials the
  // dashboard's server sent through it, so the review can tell which role credentials were in use and prove that a
  // rotation revoked them; each one is added to the redaction list. `lose(true)` still forwards role-credential
  // changes to the node, but drops the node's answer, as a lost response would. `refuse(tokens)` forwards a role-credential
  // change that carries one of those bearer credentials with a credential the node never issued in its place, so the node
  // itself answers 401 (with its identity header), as it does once that credential is revoked there. Only those changes
  // are affected: the dashboard's server's read-only checks with the same credential still pass, so the refusal comes
  // after its checks, as a revocation racing them would. `patches()` lists, in memory, the role credentials each change
  // asked for, so the review can tell whether a later attempt finished the same saved update.
  async function route(target) {
    const routeOrigin = `http://127.0.0.1:${await port()}`;
    const seen = [];
    const patches = [];
    const unknown = `Bearer ${randomBytes(32).toString('hex')}`;
    let losing = false, lost = 0, changes = 0, refusing = null, refused = 0;
    const server = createServer((req, res) => {
      const chunks = []; let size = 0;
      req.on('data', chunk => { size += chunk.length; if (size <= 65536) chunks.push(chunk); });
      req.on('end', () => {
        const body = Buffer.concat(chunks);
        const bearer = /^Bearer (\S+)$/u.exec(req.headers.authorization ?? '')?.[1];
        if (bearer) { secrets.add(bearer); if (seen.length < 512) seen.push({ token: bearer, method: req.method, path: req.url.split('?')[0] }); }
        const change = req.method === 'PATCH' && req.url.startsWith('/api/dashboard-enrollments/');
        const headers = { ...req.headers, host: new URL(target).host };
        if (change) {
          changes++;
          try {
            const asked = JSON.parse(body.toString('utf8'));
            for (const value of Object.values(asked)) if (typeof value === 'string' && value.length >= 24) secrets.add(value);
            if (patches.length < 256) patches.push({ token: bearer, admin: asked.admin, editor: asked.editor, viewer: asked.viewer });
          } catch {}
          if (bearer && refusing?.has(bearer)) { refused++; headers.authorization = unknown; }
        }
        const upstream = upstreamRequest(new URL(req.url, target), { method: req.method, headers, timeout: 14000 }, response => {
          if (change && losing) { lost++; response.resume(); response.on('end', () => res.destroy()); return; }
          res.writeHead(response.statusCode, response.headers); response.pipe(res);
        });
        upstream.on('timeout', () => upstream.destroy());
        upstream.on('error', () => { if (!res.headersSent) res.writeHead(502); res.end(); });
        upstream.end(body);
      });
    });
    server.listen(Number(new URL(routeOrigin).port), '127.0.0.1'); await once(server, 'listening'); servers.push(server);
    return {
      origin: routeOrigin, lose(value) { losing = value; }, refuse(tokens) { refusing = tokens ? new Set(tokens) : null; },
      stats: () => ({ lost, changes, refused }), seen: () => seen.map(row => ({ ...row })), patches: () => patches.map(row => ({ ...row })),
    };
  }
  // A fresh private node for the Add node wizard review. It starts with the settings the wizard made in the page (read
  // back from its Copy action, never from a download), on loopback, with its own database and identity directory in this
  // run directory. Its tokens join the redaction list; the harness stops it with every other owned process.
  // `connect: true` is for a node that reaches the dashboard. Its connector checks the HTTPS front's certificate with the
  // normal verifier, no proxy and nothing skipped, against only the loopback test certificate: SSL_CERT_FILE replaces the
  // system roots (on Windows and macOS too, from Go 1.27 with go.mod's `go 1.27.3`) and SSL_CERT_DIR is dropped. Its output
  // stays in memory, bounded: the first 16 KiB for the fingerprint line it prints at start, and the last 16 KiB.
  async function startNode(id, settings, nodePort, { connect = false } = {}) {
    for (const value of Object.values(settings)) if (value.length >= 24) secrets.add(value);
    const childEnv = { ...env, ...settings, CANARY_HOST: '127.0.0.1', CANARY_PORT: String(nodePort), CANARY_DB: join(runDir, `${id}.db`), CANARY_IDENTITY_DIRECTORY: join(runDir, `${id}-identity`) };
    if (connect) { childEnv.SSL_CERT_FILE = TRUST; delete childEnv.SSL_CERT_DIR; }
    const node = spawn(join(runDir, process.platform === 'win32' ? 'review-node.exe' : 'review-node'), ['serve'], {
      cwd: runDir, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'], env: childEnv,
    });
    owned.push(node);
    let head = '', tail = '';
    for (const stream of [node.stdout, node.stderr]) {
      stream.on('data', value => { const chunk = String(value); if (head.length < 16384) head = (head + chunk).slice(0, 16384); tail = (tail + chunk).slice(-16384); });
    }
    const nativeTLS = settings.CANARY_TLS_MODE === 'auto';
    const url = `${nativeTLS ? 'https' : 'http'}://127.0.0.1:${nodePort}`;
    const certificatePin = () => /Node TLS certificate fingerprint: ([0-9a-f]{64})/u.exec(head)?.[1] ?? null;
    const read = (path, options) => nativeTLS ? pinnedFixtureFetch(url + path, certificatePin(), options) : fetch(url + path, options);
    await until(async () => {
      if (node.exitCode !== null) throw Error(`Wizard node ${id} exited during startup: ${tail.slice(-600)}`);
      try { return (await read('/api/auth/status')).ok; } catch { return false; }
    }, `${id} ready`, 30000);
    return {
      url, request: read, tlsFingerprint: certificatePin,
      async stop() {
        node.kill('SIGTERM');
        await until(() => node.exitCode !== null || node.signalCode !== null, `${id} stopped`, 15000);
      },
      /** The connection fingerprint the node printed, or null before it did. */
      fingerprint: () => FINGERPRINT_LINE.exec(head)?.[1] ?? null,
      log: () => tail,
      running: () => node.exitCode === null && node.signalCode === null,
    };
  }
  return {
    origin, backend, child, code, password, proxyCookie, route, startNode, freePort: port,
    previewUpdates(value) { assert.ok(Buffer.byteLength(JSON.stringify(value)) < 32768); updatePreview = value; },
    previewOnboarding(value) { assert.ok(Buffer.byteLength(JSON.stringify(value)) < 32768); onboardingPreview = value; },
    // The node-connection address the dashboard reports (the HTTPS front), its private listener behind it, and what the
    // front carried.
    connection: {
      url: connectionUrl, privateOrigin: connectionBackend,
      stats: () => ({ ...transport, refused: [...transport.refused], routes: { ...transport.routes } }),
    },
    expire() { session = ''; },
    // Holds the dashboard's answer to the next `method` `path` request (see `holding`). Steps release it in `finally`.
    hold(method, path) {
      assert.equal(holding, null, 'one held answer at a time');
      let open;
      const held = { method, path, taken: false, answered: null, dropped: false, delivered: false, released: new Promise(resolve => { open = resolve; }) };
      const release = () => { clearTimeout(limit); if (holding === held) holding = null; open(); };
      const limit = setTimeout(release, 20000);
      limit.unref?.();
      holding = held;
      return { answered: () => held.answered, state: () => ({ taken: held.taken, answered: held.answered, dropped: held.dropped, delivered: held.delivered }), release };
    },
    stats: () => ({ denied, authenticated, paths: [...paths] }),
    // Called with each dashboard API request as it reaches the proxy, before the dashboard's server sees it.
    watch(fn) { watchers.add(fn); return () => watchers.delete(fn); },
    // Fixture-only direct administration supports controls after UI sign-in;
    // it never reads a user's deployment database or credential.
    async api(path, method = 'GET', body, token, registration) {
      const headers = {};
      if (token) headers.Authorization = `Bearer ${token}`;
      if (registration) headers['X-Canary-Registered-Node'] = registration;
      if (body !== undefined) headers['Content-Type'] = 'application/json';
      const response = await fetch(backend + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), redirect: 'error' });
      const text = await response.text(); let value; try { value = JSON.parse(text); } catch {}
      return { status: response.status, value };
    },
  };
}

export async function reviewDashboardControlPlane({ fixture, A, B, page, step, until, fill, click, text, ready, report, secrets, onlyFleet = false }) {
  const q = JSON.stringify;
  const start = report.requests.length;
  const clickFirst = async label => {
    await ready();
    await page.eval(`(() => { const e=[...document.querySelectorAll('button,a')].find(e=>__vis(e)&&__norm(e.innerText)===${q(label)}); if(!e||e.disabled)throw Error('enabled action missing');e.click(); })()`);
  };
  const signIn = async (name, password) => {
    await fill('Account name', name); await fill('Password', password); await click('Sign in');
    await until(() => page.eval('!!document.querySelector("nav.rail")'), 'dashboard account sign-in');
    await ready();
  };
  const verify = async (node, label) => {
    // The central overview waits for the first registry read before directing
    // an empty dashboard to enrollment. Require the same Add node action.
    await until(() => page.eval('[...document.querySelectorAll("button,a")].some(e=>__vis(e)&&__norm(e.innerText)==="Add node")'), 'Add node entry', 15000);
    await clickFirst('Add node');
    await until(() => page.eval('!!document.querySelector("dialog[open]")'), 'add-node dialog');
    await fill('Name on this dashboard', label, 'dialog[open]');
    await fill('Node address', node.url, 'dialog[open]');
    await fill('Enrollment credential', node.token, 'dialog[open]');
    await click('Verify node', 'dialog[open]');
    await text('Verified by the dashboard');
    assert.equal(await page.eval('[...document.querySelectorAll("dialog[open] input[type=password]")].some(e=>e.value.length>0)'), false, 'enrollment credential cleared after verification');
  };
  const add = async (node, label) => {
    await verify(node, label);
    await page.eval(`(() => {const e=[...document.querySelectorAll('dialog[open] button')].find(e=>__norm(e.innerText).startsWith('Add '));if(!e||e.disabled)throw Error('confirm add missing');e.click()})()`);
    await until(() => page.eval('!document.querySelector("dialog[open]")'), 'registration complete');
    await until(() => page.eval(`!![...document.querySelectorAll('button')].find(e=>e.getAttribute('aria-label')===${q('Open ' + label)})`), 'node can be opened', 25000);
  };
  let owner, registrations;
  await step('Control plane: proxy cookie alone has no Canary authority; owner setup works with zero nodes', async () => {
    // A proxy may block the runtime script while still serving HTML. The
    // production server's marker must keep the real app in dashboard mode.
    await page.send('Network.setBlockedURLs', { urls: [fixture.origin + '/canary-config.js'] });
    await page.send('Page.navigate', { url: fixture.origin + '/fixture/sign-in' });
    await text('Create the owner account'); await ready();
    assert.equal(await page.eval('[...document.querySelectorAll("input")].some(e=>__vis(e)&&e.type==="url")'), false);
    const alone = await page.eval('(async()=> (await fetch("/api/overview",{credentials:"same-origin",redirect:"error"})).status)()');
    assert.equal(alone, 401);
    await fill('Account name', 'fixture-owner'); await fill('Password', fixture.password);
    await fill('Confirm password', fixture.password); await fill('Setup code', fixture.code); await click('Create owner account');
    await until(() => page.eval('!!document.querySelector("nav.rail")'), 'owner created and signed in');
    await text('No nodes yet');
    const login = await fixture.api('/api/auth/login', 'POST', { name: 'fixture-owner', password: fixture.password });
    assert.equal(login.status, 200); owner = login.value.token; secrets.add(owner);
    assert.deepEqual((await fixture.api('/api/dashboard/nodes', 'GET', undefined, owner)).value, []);
    return { separateDashboardOwner: true, nodesRequiredForLogin: 0, proxyCookieOnlyStatus: 401, runtimeScriptBlocked: true };
  });
  await step('Control plane: verify/cancel changes no node account and confirm enrolls the real private node', async () => {
    const before = await (await fetch(B.url + '/api/accounts', { headers: { Authorization: `Bearer ${B.token}` } })).json();
    await verify(B, 'Canceled private node'); await click('Cancel', 'dialog[open]');
    const after = await (await fetch(B.url + '/api/accounts', { headers: { Authorization: `Bearer ${B.token}` } })).json();
    assert.deepEqual(after, before);
    assert.deepEqual((await fixture.api('/api/dashboard/nodes', 'GET', undefined, owner)).value, []);
    await add(A, 'First private node'); await add(B, 'Second private node');
    registrations = (await fixture.api('/api/dashboard/nodes', 'GET', undefined, owner)).value;
    assert.equal(registrations.length, 2);
    assert.deepEqual(new Set(registrations.map(row => row.nodeId)), new Set([A.id, B.id]));
    assert.ok(!JSON.stringify(registrations).includes(A.token) && !JSON.stringify(registrations).includes(B.token));
    return { readOnlyVerification: true, cancellationLeavesNoAccounts: true, registeredNodes: 2 };
  });
  if (onlyFleet) {
    await reviewFleet({ fixture, A, B, page, step, until, fill, click, text, ready, owner });
    await page.send('Network.setBlockedURLs', { urls: [] });
    return;
  }
  await step('Control plane: select two nodes without another sign-in; monitoring and account routes remain separate', async () => {
    for (const label of ['First private node', 'Second private node']) {
      await page.eval('location.hash="#/nodes"'); await text(label);
      await until(() => page.eval(`!![...document.querySelectorAll('button')].find(e=>e.getAttribute('aria-label')===${q('Open ' + label)})`), 'registered-node open action');
      await ready(); await page.eval(`[...document.querySelectorAll('button')].find(e=>e.getAttribute('aria-label')===${q('Open ' + label)}).click()`);
      // Accepted fleet behavior: the overview is central even while a node is selected.
      await until(() => page.eval('!!document.querySelector("main h1") && document.querySelector("main h1").textContent.includes("All nodes")'), 'central overview with selected node');
    }
    const requests = fixture.stats().paths;
    for (const row of registrations) assert.ok(requests.some(request => request.path === '/api/overview' && request.registration === row.id));
    assert.ok(!requests.some(request => request.path === '/api/auth/login' && request.registration));
    assert.equal((await fixture.api('/api/auth/me', 'GET', undefined, owner, registrations[1].id)).value.name, 'fixture-owner');
    return { selections: 2, perNodeSignIn: false, accountAuthority: 'dashboard' };
  });
  await step('Control plane: generated POST and GET heartbeats reach the selected node without a dashboard sign-in', async () => {
    const selected = registrations.find(row => row.nodeId === B.id);
    const token = randomBytes(32).toString('hex'); secrets.add(token);
    const made = await fixture.api('/api/monitors', 'POST', {
      name: 'Dashboard heartbeat fixture', type: 'push', target: '', internalOnly: true, intervalMs: 1000, timeoutMs: 750,
      nodeIds: [B.id], enabled: true, push: { token, allowGet: true },
    }, owner, selected.id);
    assert.equal(made.status, 201);
    await page.eval(`location.hash=${q(`#/monitors/${made.value.id}/edit`)}`);
    await text('Edit monitor'); await text('Send heartbeats'); await ready();
    let commands = await page.eval('[...document.querySelectorAll("pre")].find(e=>e.textContent.includes("curl -X POST"))?.textContent');
    assert.ok(commands.includes('<token>') && !commands.includes(token), 'heartbeat secret is masked initially');
    await click('Show push token');
    commands = await page.eval('[...document.querySelectorAll("pre")].find(e=>e.textContent.includes("curl -X POST"))?.textContent');
    const post = commands.match(/curl -X POST "([^"]+)"/u)?.[1];
    const get = commands.match(/curl "([^"]+)"/u)?.[1];
    for (const address of [post, get]) {
      assert.ok(address); assert.equal(new URL(address).origin, fixture.origin);
      assert.equal(new URL(address).searchParams.get('registration'), selected.id);
    }
    const result = await page.eval(`(async()=>{
      const posted=await fetch(${q(post)},{method:'POST',credentials:'same-origin',redirect:'error',headers:{Authorization:${q('Push '+token)},'Content-Type':'application/json'},body:JSON.stringify({status:'up',msg:'private fixture'})});
      await new Promise(resolve=>setTimeout(resolve,150));
      const fetched=await fetch(${q(get)},{credentials:'same-origin',redirect:'error'});
      return {post:posted.status,get:fetched.status};
    })()`);
    assert.deepEqual(result,{post:200,get:200});
    return { selectedNode: B.id, posted: 200, fetched: 200, dashboardBearerSent: false };
  });
  await step('Control plane: viewer shares the registry, cannot enroll, and receives node-redacted data', async () => {
    const password = 'Private dashboard viewer fixture password'; secrets.add(password);
    assert.equal((await fixture.api('/api/accounts', 'POST', { name: 'fleet-viewer', role: 'viewer', password }, owner)).status, 201);
    await click('Sign out'); await until(() => page.eval('!document.querySelector("nav.rail")'), 'owner sign-out');
    await signIn('fleet-viewer', password); await text('First private node'); await text('Second private node');
    assert.equal(await page.eval('[...document.querySelectorAll("button,a")].some(e=>__vis(e)&&__norm(e.innerText)==="Add node")'), false);
    const login = await fixture.api('/api/auth/login', 'POST', { name: 'fleet-viewer', password });
    const viewer = login.value.token; secrets.add(viewer);
    const rows = (await fixture.api('/api/dashboard/nodes', 'GET', undefined, viewer)).value;
    assert.equal(rows.length, 2); assert.ok(rows.every(row => !row.url));
    assert.equal((await fixture.api('/api/dashboard/verify', 'POST', {}, viewer)).status, 403);
    const view = await fixture.api('/api/overview', 'GET', undefined, viewer, registrations[0].id);
    assert.equal(view.status, 200); assert.equal(view.value.user.role, 'viewer');
    for (const row of view.value.monitors) assert.ok(!row.monitor.webhookUrl && !row.monitor.service && !row.monitor.pm2);
    return { sharedReadOnlyList: true, enrollmentDenied: true, nativeNodeRedaction: true };
  });
  await step('Control plane: expired proxy session is reported and full-page reauthentication restores dashboard login', async () => {
    fixture.expire();
    await text('sign-in proxy', 25000);
    assert.ok(!await page.eval('location.pathname.includes("sign-in-required")'), 'API redirects were not followed');
    await page.send('Page.navigate', { url: fixture.origin + '/fixture/sign-in' });
    await until(() => page.eval('!!document.querySelector("nav.rail")'), 'dashboard sign-in restored after proxy authentication');
    // The viewer sign-in now survives navigation. Explicitly switch accounts
    // for the administrator maintenance checks that follow.
    await click('Sign out'); await until(()=>page.eval('!document.querySelector("nav.rail")'),'restored viewer signs out');
    await signIn('fixture-owner', fixture.password); await text('First private node');
    return { expiryVisible: true, redirectFollowedByAPI: false, proxyReauthentication: true };
  });
  // ----- Registry maintenance: rename, address move, credential rotation (dashboard_registry_edit.go). The node is B,
  // reached through a second loopback route after the move, so the review sees which role credentials are in use.
  let second, route, oldRoles;
  const cardText = id => page.eval(`document.getElementById(${q('r-' + id)})?.closest('article')?.innerText ?? ''`);
  const rowOf = async id => (await fixture.api('/api/dashboard/nodes', 'GET', undefined, owner)).value.find(row => row.id === id);
  const dialogOpen = () => until(() => page.eval('!!document.querySelector("dialog[open]")'), 'dialog open');
  const dialogClosed = timeout => until(() => page.eval('!document.querySelector("dialog[open]")'), 'dialog closed', timeout);
  // The global node picker was removed for the accepted fleet-first UI.
  // Selection is shown on node-specific pages and on the selected Nodes card.
  const viewing = async label => {
    const previous = await page.eval('location.hash');
    await page.eval('location.hash="#/nodes"'); await ready();
    await until(() => page.eval(`document.querySelector('article.rnode.ep h3')?.textContent===${q(label)}`), `selected registration ${label}`);
    assert.equal(await page.eval("document.querySelectorAll('.topbar .tb-btn').length"), 0, 'fleet topbar has no global node picker');
    await page.eval(`location.hash=${q(previous)}`); await ready();
  };
  const atNode = async (path, token) => {
    const response = await fetch(B.url + path, { headers: { Authorization: `Bearer ${token}` }, redirect: 'error' });
    const body = await response.text(); let value; try { value = JSON.parse(body); } catch {}
    return { status: response.status, value };
  };
  // The dashboard's role accounts on B behind the bearer credentials the route saw, by role. Others (the node's own
  // administrator, refused or replaced credentials) are left out.
  const delegated = async seen => {
    const roles = {};
    for (const token of new Set(seen.map(row => row.token))) {
      const me = await atNode('/api/auth/me', token);
      if (me.status === 200 && me.value?.id === `dashboard-${second.id}-${me.value.role}`) (roles[me.value.role] ??= new Set()).add(token);
    }
    return roles;
  };
  const nodeFacts = async () => ({
    node: (await atNode('/api/overview', B.token)).value?.node?.id,
    accounts: ((await atNode('/api/accounts', B.token)).value ?? []).map(row => row.id).sort(),
    monitors: ((await atNode('/api/monitors', B.token)).value ?? []).map(row => row.id ?? row.monitor?.id).sort(),
  });
  const retryAt = id => entry => entry.method === 'POST' && entry.path === `/api/dashboard/nodes/${id}/retry`;
  await step('Control plane: an administrator renames a node; its registration, node ID and the current view stay', async () => {
    second = registrations.find(row => row.nodeId === B.id);
    await page.eval('location.hash="#/nodes"'); await text('Second private node');
    await until(() => page.eval(`!![...document.querySelectorAll('button')].find(e=>e.getAttribute('aria-label')===${q('Open Second private node')})`), 'registered-node open action');
    await ready(); await page.eval(`[...document.querySelectorAll('button')].find(e=>e.getAttribute('aria-label')===${q('Open Second private node')}).click()`);
    await viewing('Second private node');
    await page.eval('location.hash="#/nodes"'); await text('Rotate access');
    const begin = fixture.stats().paths.length;
    await click('Edit Second private node'); await dialogOpen();
    await fill('Name on this dashboard', 'Renamed private node', 'dialog[open]');
    await click('Save changes', 'dialog[open]');
    await dialogClosed(15000);
    await text('Renamed private node');
    await viewing('Renamed private node');
    const rows = (await fixture.api('/api/dashboard/nodes', 'GET', undefined, owner)).value;
    const renamed = rows.find(row => row.id === second.id);
    assert.equal(rows.length, 2);
    assert.deepEqual([renamed.label, renamed.nodeId, renamed.url], ['Renamed private node', B.id, B.url]);
    const sent = fixture.stats().paths.slice(begin);
    assert.ok(sent.some(entry => entry.method === 'PATCH' && entry.path === `/api/dashboard/nodes/${second.id}` && entry.registration === null && entry.status === 200), 'one registry PATCH without a node selection');
    assert.ok(!sent.some(entry => entry.path === '/api/auth/login' || entry.path === '/api/auth/logout'), 'no sign-in or sign-out');
    await until(() => fixture.stats().paths.slice(begin).some(entry => entry.path === '/api/overview' && entry.registration === second.id && entry.status === 200), 'the same registration is still read', 15000);
    return { registration: second.id, sameRegistration: true, selectionKept: true, nodeContacted: false };
  });
  await step('Control plane: moving the address re-checks the same node over a second loopback route; a different node is refused', async () => {
    route = await fixture.route(B.url);
    await page.eval('location.hash="#/nodes"'); await text('Renamed private node');
    await click('Edit Renamed private node'); await dialogOpen();
    await fill('Node address', A.url, 'dialog[open]');
    await click('Save changes', 'dialog[open]');
    await text('A different node answers at this address', 15000);
    assert.equal((await rowOf(second.id)).url, B.url, 'another node never replaces the saved address');
    assert.equal(route.seen().length, 0);
    await fill('Node address', route.origin, 'dialog[open]');
    await click('Save changes', 'dialog[open]');
    await dialogClosed(20000);
    await text(route.origin);
    const moved = await rowOf(second.id);
    assert.deepEqual([moved.id, moved.nodeId, moved.label, moved.url], [second.id, B.id, 'Renamed private node', route.origin]);
    oldRoles = await delegated(route.seen());
    assert.deepEqual(Object.keys(oldRoles).sort(), ['admin', 'editor', 'viewer'], 'all three existing role credentials were proved at the new address');
    await until(async () => (await rowOf(second.id)).state === 'answering', 'answering through the new route', 25000);
    await viewing('Renamed private node');
    return { differentNodeRefused: true, sameNodeId: true, rolesProvedAtNewAddress: 3, registrationKept: true };
  });
  await step('Control plane: rotation revokes the old role credentials; a lost answer stays “Updating node access” until Retry; the view recovers without another sign-in', async () => {
    const before = await nodeFacts();
    const begin = fixture.stats().paths.length;
    let from = 0;
    await viewing('Renamed private node');
    route.lose(true);
    const unwatch = fixture.watch(entry => { if (retryAt(second.id)(entry)) route.lose(false); });
    try {
      await page.eval('location.hash="#/nodes"'); await text('Renamed private node');
      await click('Rotate access for Renamed private node'); await dialogOpen();
      assert.deepEqual(await page.eval('[...document.querySelectorAll("dialog[open] input")].map(e=>[e.type,e.value,e.autocomplete])'), [['password', '', 'off']], 'one empty optional credential, password managers off');
      await click('Rotate access', 'dialog[open]');
      await dialogClosed(30000);
      assert.equal((await rowOf(second.id)).state, 'rotating', 'the replacement stays saved while the answer is unknown');
      assert.ok(route.stats().lost >= 1, 'the node answered, and the answer was dropped');
      for (const [role, tokens] of Object.entries(oldRoles)) for (const token of tokens) assert.equal((await atNode('/api/auth/me', token)).status, 401, `old ${role} credential still accepted`);
      await until(async () => (await cardText(second.id)).includes('Updating node access'), 'the card shows the update', 20000);
      await until(() => page.eval(`[...document.querySelectorAll('.banner')].some(b=>b.innerText.includes('Renamed private node: Updating node access'))`), 'the viewed node’s banner explains it', 30000);
      assert.equal(await page.eval('!!document.querySelector("nav.rail") && !document.querySelector("dialog[open]")'), true, 'still signed in; no sign-in prompt');
      from = route.seen().length;
      await clickFirst('Retry access update');
      await until(async () => (await rowOf(second.id)).state !== 'rotating', 'the retried update finished', 30000);
    } finally { unwatch(); route.lose(false); }
    const sent = fixture.stats().paths.slice(begin);
    const retry = sent.find(retryAt(second.id));
    assert.ok(retry, 'Retry was sent from the page');
    await until(() => retry.status !== undefined, 'the retry answered', 15000);
    // 409: the dashboard's own background retry held the operation at that moment and finished it.
    assert.ok([200, 409].includes(retry.status), `retry answered ${retry.status}`);
    const current = await delegated(route.seen().slice(from));
    assert.deepEqual(Object.keys(current).sort(), ['admin', 'editor', 'viewer'], 'all three new role credentials work');
    for (const [role, tokens] of Object.entries(current)) for (const token of tokens) assert.ok(!oldRoles[role].has(token), `${role} credential was replaced`);
    assert.deepEqual(await nodeFacts(), before, 'same node identity, role accounts and monitors');
    await page.eval('location.hash="#/overview"');
    // Same accepted central overview; preserve the original recovery and freshness checks.
    await until(() => page.eval(`(() => { const snap=document.querySelector('.topbar .snap'); return (document.querySelector('main h1')?.textContent ?? '').includes('All nodes') && !!snap && !snap.classList.contains('bad') && ![...document.querySelectorAll('.banner')].some(b=>/Updating node access|answering \\(/.test(b.innerText)); })()`), 'monitoring answers again', 60000);
    await viewing('Renamed private node');
    const after = fixture.stats().paths.slice(begin);
    const retried = after.indexOf(retry);
    assert.ok(after.some((entry, index) => index > retried && entry.path === '/api/overview' && entry.registration === second.id && entry.status === 200), 'the same selection reads the node again');
    assert.ok(!after.some(entry => entry.path === '/api/auth/login' || entry.path === '/api/auth/logout'), 'no sign-in or sign-out');
    oldRoles = current;
    return { lostAnswers: route.stats().lost, oldCredentialsRefused: true, retryStatus: retry.status, rolesReplaced: 3, sameIdentity: true, reSignIn: false };
  });
  await step('Control plane: an optional recovery credential must be an administrator on the same node, and the page forgets it after each attempt', async () => {
    const from = route.seen().length;
    await page.eval('location.hash="#/nodes"'); await text('Renamed private node');
    await click('Rotate access for Renamed private node'); await dialogOpen();
    await fill('Node administrator credential', A.token, 'dialog[open]');
    await click('Rotate access', 'dialog[open]');
    await text('The node refused that administrator credential', 15000);
    assert.equal(await page.eval('[...document.querySelectorAll("dialog[open] input")].every(e=>e.value==="")'), true, 'forgotten after the refused attempt');
    assert.notEqual((await rowOf(second.id)).state, 'rotating', 'a refused recovery credential changed nothing');
    for (const [role, tokens] of Object.entries(oldRoles)) for (const token of tokens) assert.equal((await atNode('/api/auth/me', token)).status, 200, `${role} credential still works after the refusal`);
    await fill('Node administrator credential', B.token, 'dialog[open]');
    await click('Rotate access', 'dialog[open]');
    await dialogClosed(30000);
    await until(async () => (await rowOf(second.id)).state !== 'rotating', 'the update with a recovery credential finished', 30000);
    for (const [role, tokens] of Object.entries(oldRoles)) for (const token of tokens) assert.equal((await atNode('/api/auth/me', token)).status, 401, `${role} credential replaced`);
    const current = await delegated(route.seen().slice(from));
    assert.deepEqual(Object.keys(current).sort(), ['admin', 'editor', 'viewer']);
    const own = await atNode('/api/auth/me', B.token);
    assert.equal(own.status, 200); assert.ok(!String(own.value?.id).startsWith('dashboard-'), 'the node’s own administrator is unchanged');
    oldRoles = current;
    return { otherNodeAdministratorRefused: true, credentialFieldCleared: true, recoveryRotation: true, ownAdministratorUnchanged: true };
  });
  await step('Control plane: during an update viewers see only “Updating node access”, with no address, edit, rotation or retry; the owner’s Retry finishes it', async () => {
    const password = 'Private dashboard rotation viewer password'; secrets.add(password);
    assert.equal((await fixture.api('/api/accounts', 'POST', { name: 'rotation-viewer', role: 'viewer', password }, owner)).status, 201);
    route.lose(true);
    const unwatch = fixture.watch(entry => { if (retryAt(second.id)(entry)) route.lose(false); });
    try {
      const started = await fixture.api(`/api/dashboard/nodes/${second.id}/rotate`, 'POST', {}, owner);
      assert.equal(started.status, 202, JSON.stringify(started.value));
      assert.equal(started.value.state, 'rotating');
      await click('Sign out'); await until(() => page.eval('!document.querySelector("nav.rail")'), 'owner sign-out');
      await signIn('rotation-viewer', password);
      await page.eval('location.hash="#/nodes"');
      await until(async () => (await cardText(second.id)).includes('Updating node access'), 'the viewer sees the update', 20000);
      assert.match(await cardText(second.id), /An administrator can retry it\./u);
      const controls = await page.eval('[...document.querySelectorAll("main button, main a")].filter(e=>__vis(e)).map(e=>__norm(e.innerText)+" | "+(e.getAttribute("aria-label")||""))');
      for (const forbidden of [/^Edit\b/u, /Rotate access/u, /^Retry/u, /^Remove\b/u, /Add node/u, /Recover access/u]) assert.ok(!controls.some(control => forbidden.test(control)), `viewer control ${forbidden}: ${controls.join(', ')}`);
      const shown = await page.eval('document.body.innerText');
      for (const address of [route.origin, B.url, A.url]) assert.ok(!shown.includes(address), 'no node addresses for viewers');
      const login = await fixture.api('/api/auth/login', 'POST', { name: 'rotation-viewer', password });
      const viewer = login.value.token; secrets.add(viewer);
      const listed = (await fixture.api('/api/dashboard/nodes', 'GET', undefined, viewer)).value.find(row => row.id === second.id);
      assert.deepEqual([listed.state, listed.url], ['rotating', undefined]);
      for (const [path, method, body] of [[`/api/dashboard/nodes/${second.id}`, 'PATCH', { label: 'Viewer rename' }], [`/api/dashboard/nodes/${second.id}/rotate`, 'POST', {}], [`/api/dashboard/nodes/${second.id}/retry`, 'POST', undefined], [`/api/dashboard/nodes/${second.id}/retry`, 'POST', { token: B.token }]]) {
        assert.equal((await fixture.api(path, method, body, viewer)).status, 403, `viewer ${method} ${path}`);
      }
      await click('Sign out'); await until(() => page.eval('!document.querySelector("nav.rail")'), 'viewer sign-out');
      await signIn('fixture-owner', fixture.password);
      await page.eval('location.hash="#/nodes"');
      await until(async () => (await cardText(second.id)).includes('Retry access update'), 'the owner sees Retry', 20000);
      await clickFirst('Retry access update');
      await until(async () => (await rowOf(second.id)).state !== 'rotating', 'the owner’s retry finished', 30000);
    } finally { unwatch(); route.lose(false); }
    const row = await rowOf(second.id);
    assert.deepEqual([row.label, row.url, row.nodeId], ['Renamed private node', route.origin, B.id]);
    return { viewerSeesStateOnly: true, viewerChanges: 403, ownerRetry: true };
  });
  await step('Control plane: a late refusal of the credential a saved update uses leaves it “Updating node access”; Recover access finishes that same update with a node administrator credential, without another rotation', async () => {
    // The late refusal is modeled at the second loopback route (route.refuse): the saved update's role-credential change,
    // which carries the dashboard's current administrator credential, reaches the node with a credential it never issued,
    // so the node itself answers 401, while the server's read-only checks before saving still pass. The fixture changes no
    // account on the node: its API refuses edits to dashboard-managed accounts other than deleting them, and a deleted one
    // is a missing delegation that recovery must not revive. Root's Go regression
    // TestDashboardRotationLateAuthorityLossCanRecoverSameJournal replaces the account's hash in a real node store.
    const label = 'Renamed private node';
    const before = await nodeFacts();
    const current = await delegated(route.seen());
    assert.deepEqual(Object.keys(current).sort(), ['admin', 'editor', 'viewer'], 'the credentials the previous update installed');
    const begin = fixture.stats().paths.length;
    const firstChange = route.patches().length;
    const sameAsked = change => ({ admin: change.admin, editor: change.editor, viewer: change.viewer });
    // One attempt from the dialog. A 409 while the dashboard's own background retry holds the update is tried again.
    const finishWith = async (credential, expected) => {
      for (let attempt = 1; ; attempt++) {
        await fill('Node administrator credential', credential, 'dialog[open]');
        await click('Finish update', 'dialog[open]');
        const outcome = await until(() => page.eval(`(() => {
          const d = document.querySelector('dialog[open]'); if (!d) return 'closed';
          const t = d.innerText; if (t.includes('This update is busy or already finished')) return 'busy';
          if (${q(expected ?? '')} && t.includes(${q(expected ?? '')})) return 'expected';
          const shown = d.querySelector('.errsum b'); return shown ? 'failed: ' + shown.innerText : '';
        })()`), 'the recovery answered', 30000);
        if (outcome !== 'busy' || attempt === 3) return outcome;
        assert.equal((await rowOf(second.id)).state, 'rotating', 'a busy answer leaves the update pending');
      }
    };
    let desired, recoveredFrom;
    route.refuse(current.admin);
    try {
      await page.eval('location.hash="#/nodes"'); await text(label);
      await click(`Rotate access for ${label}`); await dialogOpen();
      await click('Rotate access', 'dialog[open]');
      await dialogClosed(30000);
      const pending = await rowOf(second.id);
      assert.equal(pending.state, 'rotating', 'saved before the node was asked, so the update stays pending');
      assert.ok(pending.error, 'the refused attempt left its reason on the entry');
      assert.ok(route.stats().refused >= 1, 'the node refused the saved update’s change');
      const asked = route.patches().slice(firstChange);
      assert.ok(asked.length >= 1);
      desired = sameAsked(asked[0]);
      for (const change of asked) assert.deepEqual(sameAsked(change), desired, 'every attempt asks for the same saved credentials');
      for (const [role, tokens] of Object.entries(current)) {
        assert.ok(!tokens.has(desired[role]), `${role}: the saved update has a new credential`);
        for (const token of tokens) assert.equal((await atNode('/api/auth/me', token)).status, 200, `${role}: the node still accepts the credential it had`);
        assert.equal((await atNode('/api/auth/me', desired[role])).status, 401, `${role}: the saved update hasn’t reached the node`);
      }
      await until(async () => (await cardText(second.id)).includes('Updating node access'), 'the card shows the pending update', 20000);
      const controls = await page.eval(`[...(document.getElementById(${q('r-' + second.id)})?.closest('article')?.querySelectorAll('button,a') ?? [])].map(e=>__norm(e.innerText)+' | '+(e.getAttribute('aria-label')||''))`);
      assert.ok(controls.some(control => control.endsWith(`| Recover access to ${label}`)), `Recover access: ${controls.join(', ')}`);
      assert.ok(controls.some(control => control.startsWith('Retry access update')), 'ordinary Retry stays');
      assert.ok(!controls.some(control => /Rotate access/u.test(control)), 'no second rotation while one is pending');
      await click(`Recover access to ${label}`); await dialogOpen();
      const words = await page.eval('document.querySelector("dialog[open]").innerText');
      assert.match(words, /finishes the access update the dashboard’s server already saved/u);
      assert.match(words, /It doesn’t make another set\./u);
      assert.deepEqual(await page.eval('[...document.querySelectorAll("dialog[open] input")].map(e=>[e.type,e.value,e.autocomplete])'), [['password', '', 'off']], 'one empty optional credential, password managers off');
      assert.equal(await finishWith(A.token, 'The node refused that administrator credential'), 'expected');
      assert.equal(await page.eval('[...document.querySelectorAll("dialog[open] input")].every(e=>e.value==="")'), true, 'forgotten after the refused attempt');
      assert.equal((await rowOf(second.id)).state, 'rotating', 'a refused recovery credential leaves the update pending');
      assert.ok(!route.seen().some(entry => entry.token === A.token && entry.method === 'PATCH'), 'another node’s administrator never carried the update');
      recoveredFrom = route.seen().length;
      assert.equal(await finishWith(B.token, null), 'closed');
      await until(async () => (await rowOf(second.id)).state !== 'rotating', 'the recovered update finished', 30000);
    } finally { route.refuse(null); }
    const sent = fixture.stats().paths.slice(begin);
    assert.equal(sent.filter(entry => entry.method === 'POST' && entry.path === `/api/dashboard/nodes/${second.id}/rotate`).length, 1, 'one rotation; recovering never starts another');
    const retries = sent.filter(retryAt(second.id));
    await until(() => retries.every(entry => entry.status !== undefined), 'every recovery attempt answered', 15000);
    assert.ok(retries.some(entry => entry.status === 502), `the other node’s administrator was refused: ${retries.map(entry => entry.status).join(',')}`);
    assert.ok([200, 202].includes(retries.at(-1).status), `the recovery answered ${retries.at(-1).status}`);
    assert.ok(retries.every(entry => [200, 202, 409, 502].includes(entry.status)), retries.map(entry => entry.status).join(','));
    const carried = route.patches().slice(firstChange).filter(change => change.token === B.token);
    assert.ok(carried.length >= 1, 'the saved update went to the node with the recovery credential');
    for (const change of carried) assert.deepEqual(sameAsked(change), desired, 'the same saved update, not another set');
    const installed = await delegated(route.seen().slice(recoveredFrom));
    assert.deepEqual(Object.keys(installed).sort(), ['admin', 'editor', 'viewer']);
    for (const role of ['admin', 'editor', 'viewer']) assert.deepEqual([...installed[role]], [desired[role]], `${role}: exactly the saved credential works`);
    for (const [role, tokens] of Object.entries(current)) for (const token of tokens) assert.equal((await atNode('/api/auth/me', token)).status, 401, `${role}: the previous credential was replaced`);
    assert.deepEqual(await nodeFacts(), before, 'same node identity, role accounts and monitors; nothing recreated');
    const row = await rowOf(second.id);
    assert.deepEqual([row.id, row.label, row.url, row.nodeId], [second.id, label, route.origin, B.id], 'same registration');
    const own = await atNode('/api/auth/me', B.token);
    assert.equal(own.status, 200); assert.ok(!String(own.value?.id).startsWith('dashboard-'), 'the node’s own administrator is unchanged');
    assert.ok(!sent.some(entry => entry.path === '/api/auth/login' || entry.path === '/api/auth/logout'), 'no sign-in or sign-out');
    oldRoles = installed;
    return { lateRefusal: 'modeled at the loopback route', refusedChanges: route.stats().refused, rotations: 1, otherNodeAdministratorRefused: true, recoveryStatus: retries.at(-1).status, sameSavedUpdate: true, reSignIn: false };
  });
  // ----- Add node wizard (apps/web/src/views/NodeWizard.tsx). Clipboard writes and downloads are captured in the page and
  // never performed: Copy goes to an in-page list and Download's link click is held back, so no private value leaves the
  // page for the system clipboard or a downloads folder.
  const radios = () => page.eval('[...document.querySelectorAll("dialog[open] input[type=radio]")].map(e=>({ text: __norm(e.closest("label").innerText), checked: e.checked, disabled: e.disabled }))');
  const pick = async label => {
    await ready();
    await page.eval(`(() => { const d=document.querySelector('dialog[open]'); const e=[...d.querySelectorAll('label')].find(e=>__vis(e)&&__norm(e.innerText).startsWith(${q(label)})&&e.querySelector('input[type=radio]')); if(!e) throw Error('choice missing'); const input=e.querySelector('input'); if(input.disabled) throw Error('choice disabled'); input.click(); })()`);
  };
  const captureOutputs = () => page.eval(`(() => {
    window.__copies = []; window.__saved = []; window.__downloads = []; window.__revoked = 0;
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: text => { window.__copies.push(text); return Promise.resolve(); } } });
    if (!window.__urlHooks) {
      window.__urlHooks = true;
      const create = URL.createObjectURL.bind(URL), revoke = URL.revokeObjectURL.bind(URL);
      URL.createObjectURL = blob => { const url = create(blob); window.__saved.push({ url, blob }); return url; };
      URL.revokeObjectURL = url => { window.__revoked++; revoke(url); };
      const click = HTMLAnchorElement.prototype.click;
      HTMLAnchorElement.prototype.click = function () { if (this.download) { window.__downloads.push(this.download); return; } return click.call(this); };
    }
    return true;
  })()`);
  const clickLabelled = label => page.eval(`(() => { const e=[...document.querySelectorAll('dialog[open] button')].find(b=>b.getAttribute('aria-label')===${q(label)}); if(!e||e.disabled) throw Error('missing ' + ${q(label)}); e.click(); })()`);
  const copyOf = async name => {
    const before = await page.eval('window.__copies.length');
    await page.eval(`(() => { const e=[...document.querySelectorAll('dialog[open] button')].find(b=>/^(Copy|Copied:|Couldn’t copy:) /u.test(b.getAttribute('aria-label')||'')&&(b.getAttribute('aria-label')||'').endsWith(' '+${q(name)})); if(!e) throw Error('copy missing'); e.click(); })()`);
    return until(() => page.eval(`window.__copies.length > ${before} ? window.__copies[${before}] : null`), `copied ${name}`);
  };
  const envOf = value => Object.fromEntries(value.split('\n').filter(line => line && !line.startsWith('#')).map(line => [line.slice(0, line.indexOf('=')), line.slice(line.indexOf('=') + 1)]));
  // A new node's private settings file is named by resourceName() in apps/web/src/onboarding.ts, read here from the
  // source in place (the contract review turns tsx's cache off), so these steps expect exactly the page's name.
  // Accepted naming change (r4): the r1-r3 steps expected canary-<ID>.env. Go compares node IDs exactly and
  // case-sensitively, and the readable part alone gave distinct IDs one name, so the name now ends in a code from the
  // complete ID: canary-<ID>-<8 hex digits>.env for these lowercase IDs. Each step also finds it on the page's Download.
  let naming = null;
  const settingsFile = async id => {
    naming ??= import('tsx/esm/api').then(({ tsImport }) => tsImport(pathToFileURL(join(root, 'apps', 'web', 'src', 'onboarding.ts')).href, import.meta.url));
    const file = `${(await naming).resourceName(id)}.env`;
    assert.match(file, new RegExp(`^canary-${id}-[0-9a-f]{8}\\.env$`, 'u'), `${id}: the readable ID and its code`);
    return file;
  };
  const downloads = () => page.eval('[...document.querySelectorAll("dialog[open] button")].map(b=>b.getAttribute("aria-label")||"").filter(l=>l.startsWith("Download ")).map(l=>l.slice(9))');
  const connectionFile = async id => (await settingsFile(id)).replace(/\.env$/u, '-connect.env');
  const bootstrapOf = async id => {
    const settings = envOf(await copyOf(await settingsFile(id)));
    for (const key of ['CANARY_ADMIN_TOKEN', 'CANARY_CLUSTER_TOKEN']) { assert.ok(settings[key], key); secrets.add(settings[key]); }
    return settings;
  };
  const footClose = () => page.eval('[...document.querySelectorAll("dialog[open] .modal-foot button")].find(b=>__norm(b.innerText)==="Close").click()');
  await step('Control plane: Add node opens on the running-node form; a new node gets credentials made in this browser, masked until shown, and is verified and added with only its address', async () => {
    const label = 'Wizard direct node', id = 'review-wizard', file = await settingsFile(id);
    const nodePort = await fixture.freePort();
    const address = `http://127.0.0.1:${nodePort}`;
    try { await page.send('Page.setDownloadBehavior', { behavior: 'deny' }); } catch {}
    await page.eval('location.hash="#/nodes"'); await text('Renamed private node');
    const begin = fixture.stats().paths.length;
    await clickFirst('Add node'); await dialogOpen();
    const opened = await radios();
    assert.deepEqual(opened.slice(0, 3).map(row => [row.checked, row.disabled]), [[true, false], [false, false], [true, false]], JSON.stringify(opened));
    assert.ok(opened[0].text.startsWith('A node that’s already running') && opened[2].text.startsWith('This dashboard reaches the node'), JSON.stringify(opened));
    assert.equal(await page.eval('document.querySelectorAll("dialog[open] input[type=password]").length'), 1, 'the running-node form is the default');
    await until(() => fixture.stats().paths.slice(begin).some(entry => entry.method === 'GET' && entry.path === '/api/dashboard/onboarding' && entry.status === 200 && entry.registration === null), 'the wizard asked the dashboard itself', 15000);
    await pick('Deploy a new node');
    await until(() => page.eval('[...document.querySelectorAll("dialog[open] label")].some(l=>__norm(l.innerText)==="Node ID")'), 'the new-node form', 15000);
    await fill('Name on this dashboard', label, 'dialog[open]');
    await fill('Node ID', id, 'dialog[open]');
    await fill('Node URL', address, 'dialog[open]');
    await text(`--env-file ${file}`);
    assert.deepEqual(await downloads(), [file], 'the page offers the private settings under the helper’s name');
    let shown = await page.eval('document.querySelector("dialog[open]").innerText');
    assert.match(shown, /CANARY_ADMIN_TOKEN=•+/u, 'masked preview');
    assert.doesNotMatch(shown, /CANARY_ADMIN_TOKEN=[A-Za-z0-9_-]{43}/u);
    await captureOutputs();
    await page.eval('Object.defineProperty(navigator, "clipboard", { configurable: true, value: undefined })');
    await clickLabelled(`Copy ${file}`);
    await until(() => page.eval('[...document.querySelectorAll("dialog[open] button")].some(b=>__norm(b.innerText)==="Couldn’t copy")'), 'copy failure shown without a clipboard');
    await captureOutputs();
    const copied = await copyOf(file);
    const settings = envOf(copied);
    const admin = settings.CANARY_ADMIN_TOKEN, cluster = settings.CANARY_CLUSTER_TOKEN;
    secrets.add(admin); secrets.add(cluster);
    assert.deepEqual([settings.CANARY_NODE_ID, settings.CANARY_NODE_NAME, settings.CANARY_PEERS], [id, label, '[]']);
    for (const token of [admin, cluster]) assert.match(token, /^[A-Za-z0-9_-]{43}$/u);
    assert.notEqual(admin, cluster);
    assert.ok(!Object.keys(settings).some(key => key.startsWith('CANARY_DASHBOARD_')), 'a node the dashboard reaches gets no connection settings');
    shown = await page.eval('document.querySelector("dialog[open]").innerText');
    assert.ok(!shown.includes(admin) && !shown.includes(cluster), 'copying doesn’t reveal');
    await clickLabelled(`Download ${file}`);
    const saved = await until(() => page.eval('(async () => window.__saved.length ? { name: window.__downloads[0], text: await window.__saved[0].blob.text() } : null)()'), 'download captured');
    assert.deepEqual(saved, { name: file, text: copied });
    await until(() => page.eval('window.__revoked >= 1'), 'the download’s object URL revoked', 5000);
    await clickLabelled(`Show private values in ${file}`);
    assert.ok((await page.eval('document.querySelector("dialog[open]").innerText')).includes(admin), 'Show reveals');
    for (const tab of ['Docker Compose', 'Portainer stack', 'Linux VM']) {
      await click(tab, 'dialog[open]');
      const words = await page.eval('document.querySelector("dialog[open]").innerText');
      assert.ok(!words.includes(admin) && !words.includes(cluster), `${tab}: masked again after changing tabs`);
      if (tab === 'Portainer stack') {
        // User's2026-10-26report: Portainer Basic has separate Name and Value inputs. Each button copies one raw
        // field only; bulk env text remains available separately. All writes stay in the fixture's in-page hook.
        for (const [name, value] of [['CANARY_ADMIN_TOKEN', admin], ['CANARY_CLUSTER_TOKEN', cluster]]) {
          await clickLabelled(`Copy name for ${name}`);
          assert.equal(await page.eval('window.__copies.at(-1)'), name, 'only the variable name');
          await clickLabelled(`Copy value for ${name}`);
          assert.ok((await page.eval('window.__copies.at(-1)')) === value, 'only the raw value, without NAME= or newline');
        }
        const hidden = await page.eval('document.querySelector("dialog[open]").outerHTML');
        assert.ok(!hidden.includes(admin) && !hidden.includes(cluster), 'individual copies leave both private values masked');
        try {
          for (const width of [390, 728]) {
            await page.send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false });
            const layout = await page.eval('(() => {const d=document.querySelector("dialog[open]");return {fits:d.scrollWidth<=d.clientWidth+1,rows:[...d.querySelectorAll(".gvar")].map(e=>e.scrollWidth<=e.clientWidth+1)}})()');
            assert.ok(layout.fits && layout.rows.length === 2 && layout.rows.every(Boolean), `Portainer variable controls fit ${width}px`);
          }
        } finally { await page.send('Emulation.clearDeviceMetricsOverride'); }
        await fill('Publish or listen address on the node host', '192.0.2.123', 'dialog[open]');
        assert.ok((await page.eval('document.querySelector("dialog[open]").innerText')).includes(`192.0.2.123:${nodePort}:3100`), 'an independent local binding');
        assert.equal(await page.eval('__field("Node URL","dialog[open]").value'), address, 'changing a binding never changes the node URL');
        await fill('Publish or listen address on the node host', '0.0.0.0', 'dialog[open]');
      }
    }
    await click('Docker run', 'dialog[open]');
    await text(`--env-file ${file}`);
    assert.deepEqual(await downloads(), [file], 'the same name after changing tabs and back');
    const started = await fixture.startNode(id, settings, nodePort);
    await click('Next: verify the node', 'dialog[open]');
    await text('with the administrator token made for it');
    assert.equal(await page.eval('document.querySelectorAll("dialog[open] input[type=password]").length'), 0, 'nothing to paste');
    assert.deepEqual(await page.eval('[__field("Name on this dashboard","dialog[open]").value, __field("Node address","dialog[open]").value]'), [label, address]);
    await click('Verify node', 'dialog[open]');
    await text('Verified by the dashboard', 20000);
    await page.eval(`(() => {const e=[...document.querySelectorAll('dialog[open] button')].find(e=>__norm(e.innerText)===${q(`Add “${label}”`)});if(!e||e.disabled)throw Error('confirm add missing');e.click()})()`);
    await dialogClosed(30000);
    await until(() => page.eval(`!![...document.querySelectorAll('button')].find(e=>e.getAttribute('aria-label')===${q('Open ' + label)})`), 'the new node can be opened', 25000);
    const row = (await fixture.api('/api/dashboard/nodes', 'GET', undefined, owner)).value.find(entry => entry.nodeId === id);
    assert.deepEqual([row?.label, row?.url, row?.connection ?? 'direct'], [label, address, 'direct']);
    const snapshot = await page.eval('({ html: document.documentElement.outerHTML, local: JSON.stringify({ ...localStorage }), session: JSON.stringify({ ...sessionStorage }) })');
    for (const token of [admin, cluster]) assert.ok(!snapshot.html.includes(token) && !snapshot.local.includes(token) && !snapshot.session.includes(token), 'the page let go of the credentials');
    const sent = fixture.stats().paths.slice(begin);
    assert.ok(sent.some(entry => entry.method === 'POST' && entry.path === '/api/dashboard/verify' && entry.status === 200 && entry.registration === null), 'verified through the dashboard');
    const me = await fetch(started.url + '/api/auth/me', { headers: { Authorization: `Bearer ${admin}` }, redirect: 'error' });
    assert.equal(me.status, 200, 'the node’s first administrator is the token made in the page');
    return { defaultForm: 'running node, dashboard reaches it', generatedInBrowser: 2, maskedPreview: true, copyFailureVisible: true, downloadMatchesCopy: true, objectUrlRevoked: true, addedWithAddressOnly: true, credentialsLeftPage: true };
  });
  await step('Control plane: HTTPS setup authenticates the retained certificate before enrollment and hides trust details from viewers', async () => {
    const id = 'review-wizard-tls', label = 'Wizard HTTPS node', file = await settingsFile(id);
    const nodePort = await fixture.freePort(), address = `https://127.0.0.1:${nodePort}`;
    await page.eval('location.hash="#/nodes"'); await text('Wizard direct node');
    await clickFirst('Add node'); await dialogOpen(); await pick('Deploy a new node');
    await fill('Name on this dashboard', label, 'dialog[open]');
    await fill('Node ID', id, 'dialog[open]');
    await fill('Node URL', address, 'dialog[open]');
    await captureOutputs();
    const settings = envOf(await copyOf(file));
    assert.equal(settings.CANARY_TLS_MODE, 'auto');
    const started = await fixture.startNode(id, settings, nodePort);
    const pin = started.tlsFingerprint(); assert.match(pin, /^[0-9a-f]{64}$/u);
    await click('Next: verify the node', 'dialog[open]');
    const before = fixture.stats().paths.length;
    await click('Verify node', 'dialog[open]');
    await text('Enter the fingerprint from the new node');
    assert.equal(fixture.stats().paths.slice(before).filter(entry => entry.path === '/api/dashboard/verify').length, 0, 'blank trust sent no request');
    await fill('TLS certificate fingerprint', '0'.repeat(64), 'dialog[open]');
    await click('Verify node', 'dialog[open]');
    await until(() => fixture.stats().paths.slice(before).some(entry => entry.path === '/api/dashboard/verify' && entry.status === 502), 'wrong certificate refused', 20000);
    assert.ok(!(await fixture.api('/api/dashboard/nodes', 'GET', undefined, owner)).value.some(row => row.nodeId === id));
    await fill('TLS certificate fingerprint', pin.toUpperCase().match(/../gu).join(':'), 'dialog[open]');
    // A deliberate refusal switches the existing form action to Verify again.
    // The wrong-pin and successful-enrollment assertions remain unchanged.
    await click('Verify again', 'dialog[open]'); await text('Verified by the dashboard', 20000);
    await page.eval(`[...document.querySelectorAll('dialog[open] button')].find(b=>__norm(b.innerText)===${q('Add “' + label + '”')}).click()`);
    await dialogClosed(30000);
    const row = (await fixture.api('/api/dashboard/nodes', 'GET', undefined, owner)).value.find(entry => entry.nodeId === id);
    assert.equal(row.url, address); assert.equal(row.tlsFingerprint, pin);
    assert.equal((await started.request('/api/auth/me', { headers: { Authorization: 'Bearer ' + settings.CANARY_ADMIN_TOKEN } })).status, 200);
    // These checks exercise permission redaction, not another password login.
    // Keep the real ten-per-minute socket-IP sign-in throttle unchanged.
    const viewer = randomBytes(32).toString('hex'); secrets.add(viewer);
    assert.equal((await fixture.api('/api/accounts', 'POST', { name: 'tls-viewer', role: 'viewer', token: viewer }, owner)).status, 201);
    assert.equal((await fixture.api('/api/auth/me', 'GET', undefined, viewer)).status, 200);
    const hidden = (await fixture.api('/api/dashboard/nodes', 'GET', undefined, viewer)).value.find(entry => entry.id === row.id);
    assert.equal(hidden.url, undefined); assert.equal(hidden.tlsFingerprint, undefined);
    return { nativeTLS: true, blankTrustNoRequest: true, wrongPinRefused: true, colonFingerprintAccepted: true, viewerTrustHidden: true };
  });
  await step('Control plane: the node-reaches-dashboard choice follows the dashboard’s own answer; invitations stay administrators’, shown once and masked, and cancelling one changes no node', async () => {
    const begin = fixture.stats().paths.length;
    const preparationId = 'review-outbound', preparationFile = await settingsFile(preparationId);
    let preparationCredentials = null;
    const capabilities = await fixture.api('/api/dashboard/onboarding', 'GET', undefined, owner);
    assert.equal(capabilities.status, 200, JSON.stringify(capabilities.value));
    const password = 'Private dashboard wizard viewer password'; secrets.add(password);
    assert.equal((await fixture.api('/api/accounts', 'POST', { name: 'wizard-viewer', role: 'viewer', password }, owner)).status, 201);
    const viewer = (await fixture.api('/api/auth/login', 'POST', { name: 'wizard-viewer', password })).value.token; secrets.add(viewer);
    const viewerRefused = [];
    for (const [path, method, body] of [['/api/dashboard/onboarding', 'GET'], ['/api/dashboard/invitations', 'GET'], ['/api/dashboard/invitations', 'POST', { label: 'Viewer invitation', nodeId: 'review-viewer-invite' }]]) {
      const reply = await fixture.api(path, method, body, viewer);
      assert.equal(reply.status, 403, `viewer ${method} ${path}`);
      viewerRefused.push(`${method} ${path} ${reply.status}`);
    }
    const nodesBefore = (await fixture.api('/api/dashboard/nodes', 'GET', undefined, owner)).value.length;
    await page.eval('location.hash="#/nodes"'); await text('Wizard direct node');
    fixture.previewOnboarding({ ...capabilities.value, outboundEnabled: false, connectionUrl: undefined, connectionTlsFingerprint: undefined });
    try {
      await clickFirst('Add node'); await dialogOpen();
      await until(async () => !(await radios())[3].text.includes('Checking whether nodes can connect'), 'the outbound choice follows the server’s answer', 15000);
      const outbound = (await radios())[3];
      assert.deepEqual([outbound.disabled, /Not set up here/u.test(outbound.text)], [false, true], JSON.stringify(outbound));
      await pick('Deploy a new node');
      await captureOutputs();
      for (const direction of ['This dashboard reaches the node', 'The node reaches this dashboard']) {
        await pick(direction);
        for (const method of ['Docker Compose', 'Portainer stack', 'Linux VM', 'Docker run']) {
          await click(method, 'dialog[open]');
          const preview = await page.eval('(()=>{const tabs=document.querySelector("dialog[open] [role=tablist]");return {selected:tabs.querySelector("[aria-selected=true]").innerText,words:tabs.parentElement.innerText,actions:[...tabs.parentElement.querySelectorAll("button:not([role=tab])")].map(button=>({label:button.getAttribute("aria-label"),disabled:button.disabled}))}})()');
          assert.equal(preview.selected, method, 'the deployment method can be selected before dashboard setup');
          assert.ok(preview.actions.length >= 3 && preview.actions.every(action => action.disabled), `${direction} ${method}: setup actions stay visible and disabled`);
          for (const prefix of ['Show private values in ', 'Copy ', 'Download ']) assert.ok(preview.actions.some(action => action.label.startsWith(prefix)), `${direction} ${method}: visible ${prefix} action`);
          assert.ok(preview.words.includes('Complete the fields above first.'), 'the warning explains how to enable the controls');
          await page.eval('document.querySelectorAll("dialog[open] .guide button:disabled").forEach(button=>button.click())');
          assert.deepEqual(await page.eval('({copies:window.__copies.length,downloads:window.__saved.length})'), { copies: 0, downloads: 0 }, 'disabled setup actions neither copy nor download');
        }
      }
      await text('Set up this dashboard for incoming node connections');
      await pick('This dashboard reaches the node');
      await fill('Name on this dashboard', 'Wizard outbound node', 'dialog[open]');
      await fill('Node ID', preparationId, 'dialog[open]');
      await fill('Node URL', 'http://127.0.0.1:3100', 'dialog[open]');
      await click('Portainer stack', 'dialog[open]');
      await captureOutputs();
      const directBootstrap = await bootstrapOf(preparationId);
      preparationCredentials = [directBootstrap.CANARY_ADMIN_TOKEN, directBootstrap.CANARY_CLUSTER_TOKEN];
      await pick('The node reaches this dashboard');
      const draftValues = () => page.eval('({name:__field("Name on this dashboard","dialog[open]").value,nodeId:__field("Node ID","dialog[open]").value,method:document.querySelector("dialog[open] [role=tab][aria-selected=true]").innerText})');
      const expectedDraft = { name: 'Wizard outbound node', nodeId: preparationId, method: 'Portainer stack' };
      assert.deepEqual(await draftValues(), expectedDraft, 'switching to outbound keeps the entered identity and deployment method');
      assert.ok(await page.eval('[...document.querySelectorAll("dialog[open] .guide button:not([role=tab])")].every(button=>button.disabled)'), 'outbound exports stay visible but disabled until a real invitation can be included');
      await fill('Name on this dashboard', '', 'dialog[open]');
      assert.ok(await page.eval('[...document.querySelectorAll("dialog[open] .guide button:not([role=tab])")].every(button=>button.disabled)'), 'clearing a required field disables the visible actions again');
      await fill('Name on this dashboard', expectedDraft.name, 'dialog[open]');
      await pick('This dashboard reaches the node');
      assert.deepEqual(await draftValues(), expectedDraft, 'switching back preserves the shared draft');
      assert.equal(await page.eval('__field("Node URL","dialog[open]").value'), 'http://127.0.0.1:3100', 'the direct address is retained');
      await fill('Node ID', 'review-outbound-other', 'dialog[open]');
      const otherPreparation = envOf(await copyOf(await settingsFile('review-outbound-other')));
      for (const token of [otherPreparation.CANARY_ADMIN_TOKEN, otherPreparation.CANARY_CLUSTER_TOKEN]) secrets.add(token);
      assert.notEqual(otherPreparation.CANARY_ADMIN_TOKEN, preparationCredentials[0], 'another node ID receives another administrator token');
      await fill('Node ID', preparationId, 'dialog[open]');
      const restoredPreparation = envOf(await copyOf(preparationFile));
      assert.deepEqual([restoredPreparation.CANARY_ADMIN_TOKEN, restoredPreparation.CANARY_CLUSTER_TOKEN], preparationCredentials, 'returning to the prepared ID restores its original tokens');
      await pick('The node reaches this dashboard');
      assert.ok(!(await page.eval('document.querySelector("dialog[open]").innerText')).includes('Create invitation'), 'no premature invitation form');
      assert.ok(!fixture.stats().paths.slice(begin).some(entry => entry.method === 'POST' && entry.path === '/api/dashboard/invitations' && entry.status !== 403), 'setup guidance made no invitation');
      if (!capabilities.value.outboundEnabled) {
        const refused = await fixture.api('/api/dashboard/invitations', 'POST', { label: 'Wizard outbound node', nodeId: 'review-outbound' }, owner);
        assert.ok(refused.status >= 400 && !(refused.value && typeof refused.value === 'object' && 'token' in refused.value), `an invitation without a connection address: ${refused.status}`);
        await click('Cancel', 'dialog[open]'); await dialogClosed();
        return { outboundEnabled: false, choiceDisabled: false, serverRefusedInvitation: refused.status, viewerRefused };
      }
      fixture.previewOnboarding(null);
      await click('Check dashboard setup', 'dialog[open]');
      await until(() => page.eval('document.querySelector("dialog[open]").innerText.includes("Generate setup")'), 'refresh advances to the complete setup form', 15000);
    } finally {
      fixture.previewOnboarding(null);
    }
    const connectionUrl = capabilities.value.connectionUrl;
    assert.ok(connectionUrl.startsWith('https://') && connectionUrl !== fixture.origin, 'a configured HTTPS node-connection address, not the page’s');
    await pick('Deploy a new node'); await pick('This dashboard reaches the node');
    await click('Linux VM', 'dialog[open]');
    await pick('The node reaches this dashboard');
    await text(connectionUrl);
    await until(() => page.eval('[...document.querySelectorAll("dialog[open] label")].some(l=>__norm(l.innerText)==="Node ID")'), 'the invitation form', 15000);
    await fill('Name on this dashboard', '', 'dialog[open]');
    assert.ok(await page.eval('[...document.querySelectorAll("dialog[open] button")].find(button=>__norm(button.innerText)==="Generate setup").disabled'), 'incomplete first-start details keep generation disabled');
    await pick('A node that’s already running');
    await until(() => page.eval('["Name on this dashboard","Node ID"].every(label=>{const input=__field(label,"dialog[open]");return input.value===""&&input.getAttribute("aria-invalid")!=="true"})'), 'the running-node form keeps its own blank identity without inheriting validation errors');
    const label = 'Wizard outbound node', id = 'review-outbound', file = await connectionFile(id);
    await fill('Name on this dashboard', label, 'dialog[open]'); await fill('Node ID', id, 'dialog[open]');
    await click('Create invitation', 'dialog[open]');
    await text(`Waiting for ${id} to connect`, 15000);
    assert.equal(await page.eval('document.querySelector("dialog[open] [role=tab][aria-selected=true]").innerText'), 'Docker run', 'a running node defaults to its own Docker method despite the new-node VM choice');
    await click('Docker Compose', 'dialog[open]');
    await text('Add to compose.yaml, under the node service’s environment');
    await text('Put this line in its .env file');
    await click('Docker run', 'dialog[open]');
    const listed = (await fixture.api('/api/dashboard/invitations', 'GET', undefined, owner)).value;
    const invitation = listed.find(row => row.nodeId === id);
    assert.ok(invitation && invitation.state === 'waiting' && invitation.connectionUrl === connectionUrl, JSON.stringify(listed));
    assert.ok(!JSON.stringify(listed).includes('"token"'), 'the list never carries a token');
    const status = await fixture.api(`/api/dashboard/invitations/${invitation.id}`, 'GET', undefined, owner);
    assert.ok(status.status === 200 && !('token' in status.value), 'a status never carries a token');
    await footClose();
    await text('Close without saving the invitation?');
    await click('Keep it open', 'dialog[open]');
    assert.deepEqual(await downloads(), [file], 'the page offers the private settings under the helper’s name');
    await captureOutputs();
    const copied = envOf(await copyOf(file));
    const token = copied.CANARY_DASHBOARD_ENROLL_TOKEN;
    secrets.add(token);
    assert.equal(copied.CANARY_DASHBOARD_URL, connectionUrl);
    for (const key of ['CANARY_ADMIN_TOKEN', 'CANARY_CLUSTER_TOKEN', 'CANARY_NODE_ID']) assert.equal(copied[key], undefined, 'invitation exports never replace prepared credentials or identity');
    assert.ok(token && token.length >= 24, 'the one-time token is in the private settings');
    assert.ok(!(await page.eval('document.querySelector("dialog[open]").innerText')).includes(token), 'the token stays masked');
    await footClose();
    await dialogClosed();
    assert.ok(!(await page.eval('document.documentElement.outerHTML')).includes(token), 'the page let go of the token');
    await text('Waiting to connect', 15000);
    await click(`Cancel invitation for ${label}`);
    await dialogOpen();
    await click('Cancel invitation', 'dialog[open]');
    await dialogClosed(15000);
    const cancelled = await until(() => fixture.stats().paths.slice(begin).find(entry => entry.method === 'DELETE' && entry.path === `/api/dashboard/invitations/${invitation.id}` && entry.status !== undefined), 'the cancel answered', 15000);
    assert.equal(cancelled.status, 204);
    const after = (await fixture.api('/api/dashboard/invitations', 'GET', undefined, owner)).value ?? [];
    assert.ok(!after.some(row => row.id === invitation.id && ['waiting', 'connected'].includes(row.state)), 'no longer waiting');
    assert.equal((await fixture.api('/api/dashboard/nodes', 'GET', undefined, owner)).value.length, nodesBefore, 'cancelling never changes the node list');
    const sent = fixture.stats().paths.slice(begin).filter(entry => entry.path.startsWith('/api/dashboard/invitations') || entry.path === '/api/dashboard/onboarding');
    assert.ok(sent.length >= 4 && sent.every(entry => entry.registration === null), 'never with a node selection');
    return { outboundEnabled: true, connectionAddressFromServer: true, tokenShownOnce: true, closeAsksFirst: true, cancelled: 204, viewerRefused, pairing: 'not driven here: it needs a node that connects' };
  });
  // ----- First pairing of a node that reaches this dashboard (fixture.connection). A real private node starts with the
  // settings the wizard made in the page and connects through the owned HTTPS front with real certificate checks. Only
  // that node contacts the node-connection address; the page never does. The node and its entry stay for the steps
  // after this one; the harness stops the node with every other owned process.
  const FINGERPRINT_CHECK = 'This fingerprint matches the one in the node’s log';
  await step('Control plane: a new node that reaches this dashboard pairs over the dedicated HTTPS address; nothing changes until an administrator compares its fingerprint and ticks the check, then it is read with no address, no other sign-in and no browser request beyond the dashboard', async () => {
    const connection = fixture.connection;
    const capabilities = await fixture.api('/api/dashboard/onboarding', 'GET', undefined, owner);
    assert.equal(capabilities.status, 200, JSON.stringify(capabilities.value));
    assert.deepEqual([capabilities.value.outboundEnabled, capabilities.value.connectionUrl], [true, connection.url], 'the fixture configured the dedicated node-connection address');
    const label = 'Paired outbound node', id = 'review-paired';
    let file = await settingsFile(id);
    const nodePort = await fixture.freePort();
    const begin = fixture.stats().paths.length, browserBegin = report.requests.length, blockedBegin = report.blocked.length;
    const listed = async () => (await fixture.api('/api/dashboard/nodes', 'GET', undefined, owner)).value.map(row => row.id).sort();
    const listedBefore = await listed();
    await page.eval('location.hash="#/nodes"');
    await until(() => page.eval('(document.querySelector("main h1")?.textContent ?? "").includes("Nodes")'), 'the node list');
    await clickFirst('Add node'); await dialogOpen();
    await until(async () => !(await radios())[3].text.includes('Checking whether nodes can connect'), 'the outbound choice follows the server’s answer', 15000);
    await pick('Deploy a new node'); await pick('The node reaches this dashboard');
    await text(connection.url);
    await until(() => page.eval('[...document.querySelectorAll("dialog[open] label")].some(l=>__norm(l.innerText)==="Node ID")'), 'the invitation form', 15000);
    await fill('Name on this dashboard', label, 'dialog[open]'); await fill('Node ID', id, 'dialog[open]');
    assert.ok(!capabilities.value.connectionTlsFingerprint, 'the HTTPS reverse-proxy fixture uses ordinary CA trust');
    const addressBegin = fixture.stats().paths.length;
    for (const badAddress of ['192.168.001.010', 'https://unrelated.example.test']) {
      await fill('Dashboard IP or HTTPS address', badAddress, 'dialog[open]');
      await until(() => page.eval('__field("Dashboard IP or HTTPS address","dialog[open]").getAttribute("aria-invalid")==="true" && [...document.querySelectorAll("dialog[open] button")].find(button=>__norm(button.innerText)==="Generate setup").disabled'), 'unsafe outbound address is refused before invitation generation');
    }
    assert.ok(!fixture.stats().paths.slice(addressBegin).some(entry => entry.method === 'POST' && entry.path === '/api/dashboard/invitations'), 'invalid addresses send no invitation request');
    const refusedAddress = await fixture.api('/api/dashboard/invitations', 'POST', { label, nodeId: id, connectionUrl: 'https://unrelated.example.test' }, owner);
    assert.equal(refusedAddress.status, 400, 'the real server also refuses an alternate address without its certificate pin');
    await fill('Dashboard IP or HTTPS address', new URL(connection.url).hostname, 'dialog[open]');
    await click('Generate setup', 'dialog[open]');
    await text(`Waiting for ${id} to connect`, 15000);
    const firstInvitation = (await fixture.api('/api/dashboard/invitations', 'GET', undefined, owner)).value.find(row => row.nodeId === id);
    assert.equal(firstInvitation?.connectionUrl, connection.url, 'a bare dashboard IP preserves the configured connection port');
    await click('Portainer stack', 'dialog[open]');
    await captureOutputs();
    await clickLabelled('Copy value for CANARY_ADMIN_TOKEN');
    secrets.add(await page.eval('window.__copies.at(-1)'));
    await footClose(); await text('Close without saving the invitation?');
    await click('Keep it open', 'dialog[open]');
    let bootstrap;
    for (const method of ['Docker run', 'Docker Compose', 'Portainer stack', 'Linux VM', 'Docker run']) {
      await click(method, 'dialog[open]');
      await captureOutputs();
      const copied = envOf(await copyOf(file));
      const unquote = value => value.startsWith('"') ? JSON.parse(value) : value;
      const credentials = ['CANARY_ADMIN_TOKEN', 'CANARY_CLUSTER_TOKEN', 'CANARY_DASHBOARD_ENROLL_TOKEN'].map(key => unquote(copied[key]));
      for (const token of credentials) secrets.add(token);
      bootstrap ??= copied;
      assert.deepEqual(credentials, [bootstrap.CANARY_ADMIN_TOKEN, bootstrap.CANARY_CLUSTER_TOKEN, bootstrap.CANARY_DASHBOARD_ENROLL_TOKEN], `${method}: one complete export contains stable bootstrap credentials and the real invitation`);
      await clickLabelled(`Show private values in ${file}`);
      assert.ok(credentials.every(token => token && token.length >= 24), `${method}: every private value is real`);
      assert.ok((await page.eval('document.querySelector("dialog[open]").innerText')).includes(credentials[0]), `${method}: Show reveals real settings`);
      await clickLabelled(`Hide private values in ${file}`);
      await clickLabelled(`Download ${file}`);
      const downloaded = await page.eval('window.__saved.at(-1).blob.text()');
      assert.ok(credentials.every(token => downloaded.includes(token)) && !downloaded.includes('••••'), `${method}: Download includes the same complete private settings`);
    }
    const firstNode = await fixture.startNode(id, bootstrap, nodePort, { connect: true });
    await until(async () => (await fixture.api(`/api/dashboard/invitations/${firstInvitation.id}`, 'GET', undefined, owner)).value?.state === 'connected', 'the first deployment connects using only the initial exported settings', 30000);
    const firstFingerprint = firstNode.fingerprint();
    await firstNode.stop();
    await footClose(); await dialogClosed();
    assert.equal((await fixture.api(`/api/dashboard/invitations/${firstInvitation.id}`, 'DELETE', undefined, owner)).status, 204);
    // Reinvite an already-deployed node after closing the dialog. Only its connection settings may change.
    await clickFirst('Add node'); await dialogOpen();
    await until(async () => !(await radios())[3].text.includes('Checking whether nodes can connect'), 'reopened setup read', 15000);
    await pick('Deploy a new node'); await pick('The node reaches this dashboard');
    await click('Linux VM', 'dialog[open]');
    await pick('A node that’s already running');
    assert.ok(await page.eval('["Name on this dashboard","Node ID"].every(label=>__field(label,"dialog[open]").value==="")'), 'an existing node keeps its own blank identity fields');
    await fill('Name on this dashboard', label, 'dialog[open]'); await fill('Node ID', id, 'dialog[open]');
    await click('Create invitation', 'dialog[open]');
    await text(`Waiting for ${id} to connect`, 15000);
    assert.equal(await page.eval('document.querySelector("dialog[open] [role=tab][aria-selected=true]").innerText'), 'Docker run', 'running-node deployment method stays separate from the fresh VM choice');
    file = await connectionFile(id);
    const invitation = (await fixture.api('/api/dashboard/invitations', 'GET', undefined, owner)).value.find(row => row.nodeId === id);
    assert.ok(invitation?.state === 'waiting' && invitation.connectionUrl === connection.url, JSON.stringify(invitation));
    // The node's settings come from the page's Copy action, captured in the page: nothing reaches a clipboard or a file.
    assert.deepEqual(await downloads(), [file], 'the page offers the private settings under the helper’s name');
    await captureOutputs();
    const connectionSettings = envOf(await copyOf(file));
    for (const key of ['CANARY_ADMIN_TOKEN', 'CANARY_CLUSTER_TOKEN', 'CANARY_NODE_ID']) assert.equal(connectionSettings[key], undefined, `${key} must stay with the existing deployment`);
    const settings = { ...bootstrap, ...connectionSettings };
    secrets.add(settings.CANARY_DASHBOARD_ENROLL_TOKEN);
    assert.deepEqual([settings.CANARY_NODE_ID, settings.CANARY_NODE_NAME, settings.CANARY_DASHBOARD_URL], [id, label, connection.url]);
    assert.ok(settings.CANARY_DASHBOARD_ENROLL_TOKEN.startsWith(`${invitation.id}.`), 'the one-time token names this invitation');
    const node = await fixture.startNode(id, settings, nodePort, { connect: true });
    // The node's own API, with the administrator token made in the page (harness only; the browser never calls it).
    const atPaired = async (path, method = 'GET', body) => {
      const response = await fetch(node.url + path, { method, redirect: 'error', body: body === undefined ? undefined : JSON.stringify(body),
        headers: { Authorization: `Bearer ${settings.CANARY_ADMIN_TOKEN}`, ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) } });
      const raw = await response.text(); let value; try { value = JSON.parse(raw); } catch {}
      return { status: response.status, value };
    };
    const diagnose = error => { throw Error(`${error.message}; front ${JSON.stringify(connection.stats())}; node log ${node.log().slice(-600)}`); };
    const printed = await until(() => node.fingerprint(), 'the node printed its connection fingerprint', 15000).catch(diagnose);
    assert.equal(printed, firstFingerprint, 'reinvitation retains the original first-start device identity');
    const pending = await until(async () => {
      if (!node.running()) throw Error('the node stopped');
      const reply = await fixture.api(`/api/dashboard/invitations/${invitation.id}`, 'GET', undefined, owner);
      return reply.value?.state === 'connected' ? reply.value : null;
    }, 'the node connected through the HTTPS front and waits for confirmation', 30000).catch(diagnose);
    assert.deepEqual([pending.node?.id, pending.node?.fingerprint], [id, printed], 'the server reports the node’s ID and the fingerprint the node printed');
    assert.ok(!JSON.stringify(pending).includes(settings.CANARY_DASHBOARD_ENROLL_TOKEN), 'a status never carries the token');
    // Before confirmation: no registry entry, and no dashboard role account on the node.
    assert.deepEqual(await listed(), listedBefore, 'the registry is unchanged while the node waits');
    const accountsBefore = await atPaired('/api/accounts');
    assert.equal(accountsBefore.status, 200, JSON.stringify(accountsBefore.value));
    assert.deepEqual(accountsBefore.value.map(row => String(row.id)).filter(entry => entry.startsWith('dashboard-')), [], 'no dashboard role account on the node yet');
    // Viewers can't list, read, confirm or cancel the connected invitation.
    // Independent role checks must not consume the shared password-login quota.
    // Prove the token first; all original forbidden-route assertions stay 403.
    const viewer = randomBytes(32).toString('hex'); secrets.add(viewer);
    assert.equal((await fixture.api('/api/accounts', 'POST', { name: 'pairing-viewer', role: 'viewer', token: viewer }, owner)).status, 201);
    assert.equal((await fixture.api('/api/auth/me', 'GET', undefined, viewer)).status, 200);
    const viewerRefused = [];
    for (const [path, method] of [['/api/dashboard/invitations', 'GET'], [`/api/dashboard/invitations/${invitation.id}`, 'GET'], [`/api/dashboard/invitations/${invitation.id}/confirm`, 'POST'], [`/api/dashboard/invitations/${invitation.id}`, 'DELETE']]) {
      const reply = await fixture.api(path, method, undefined, viewer);
      assert.equal(reply.status, 403, `viewer ${method} ${path}`);
      viewerRefused.push(`${method} ${path.replace(invitation.id, ':id')} ${reply.status}`);
    }
    assert.equal((await fixture.api(`/api/dashboard/invitations/${invitation.id}`, 'GET', undefined, owner)).value?.state, 'connected', 'still waiting for an administrator');
    assert.deepEqual(await listed(), listedBefore, 'the viewer’s attempts added nothing');
    // The page shows what the node proved; Add waits for the explicit check.
    await until(() => page.eval('!!document.querySelector("dialog[open] .pairing dd.fp")'), 'the confirmation card', 15000);
    const facts = await page.eval('Object.fromEntries([...document.querySelectorAll("dialog[open] .pairing dt")].map(dt=>[__norm(dt.innerText), __norm(dt.nextElementSibling?.innerText)]))');
    assert.equal(facts['Node ID'], `${id} · the ID this invitation expects`, JSON.stringify(facts));
    assert.equal(facts.Fingerprint, printed, 'the page shows the fingerprint the node printed in its log');
    const addLabel = `Add “${label}”`;
    const addDisabled = () => page.eval(`(() => { const b=[...document.querySelectorAll('dialog[open] .pairing button')].find(b=>__norm(b.innerText)===${q(addLabel)}); return b ? b.disabled : null; })()`);
    assert.equal(await addDisabled(), true, 'Add is off until the fingerprint check is ticked');
    assert.ok(!fixture.stats().paths.slice(begin).some(entry => entry.path.endsWith('/confirm')), 'nothing was confirmed from the page yet');
    await ready();
    await page.eval(`(() => { const l=[...document.querySelectorAll('dialog[open] .pairing label')].find(l=>__vis(l)&&__norm(l.innerText).startsWith(${q(FINGERPRINT_CHECK)})); const c=l?.querySelector('input[type=checkbox]'); if(!c) throw Error('fingerprint check missing'); if(c.checked||c.disabled) throw Error('the check must start unticked and usable'); c.click(); })()`);
    await until(async () => (await addDisabled()) === false, 'the tick turns Add on', 5000);
    await click(addLabel, 'dialog[open]');
    const outcome = await until(() => page.eval(`(() => { const t=document.querySelector('dialog[open]')?.innerText ?? ''; return t.includes(${q(`${label} added`)}) ? 'added' : t.includes(${q(`${label} is listed`)}) ? 'listed' : ''; })()`), 'the confirmation answered', 30000);
    const confirmed = await until(() => fixture.stats().paths.slice(begin).find(entry => entry.method === 'POST' && entry.path === `/api/dashboard/invitations/${invitation.id}/confirm` && entry.status !== undefined), 'the confirm request answered', 15000);
    assert.ok([201, 202].includes(confirmed.status), `confirm answered ${confirmed.status}`);
    assert.equal(confirmed.registration, null, 'confirmed on the dashboard itself, without a node selection');
    assert.equal(fixture.stats().paths.slice(begin).filter(entry => entry.path.endsWith('/confirm')).length, 1, 'one confirmation');
    await click('Done', 'dialog[open]'); await dialogClosed(15000);
    // The entry is the invitation's registration, with no address; the dashboard's three role accounts now exist on the node.
    const row = await until(async () => { const found = await rowOf(invitation.id); return found && found.state !== 'enrolling' ? found : null; }, 'the confirmed node finished enrolling', 30000);
    assert.deepEqual([row.id, row.nodeId, row.label, row.connection, row.url], [invitation.id, id, label, 'outbound', undefined], JSON.stringify(row));
    assert.deepEqual(await listed(), [...listedBefore, row.id].sort(), 'exactly one new entry');
    const managed = await until(async () => {
      const ids = ((await atPaired('/api/accounts')).value ?? []).map(entry => String(entry.id)).filter(entry => entry.startsWith('dashboard-')).sort();
      return ids.length === 3 ? ids : null;
    }, 'the dashboard’s role accounts on the node', 30000);
    assert.deepEqual(managed, ['admin', 'editor', 'viewer'].map(role => `dashboard-${row.id}-${role}`));
    // Its card: the outbound tag, no address, and when its connection was last active.
    await page.eval('location.hash="#/nodes"'); await text(label);
    const card = await until(async () => {
      const value = await page.eval(`(() => { const a=document.getElementById(${q('r-' + row.id)})?.closest('article'); if(!a) return null;
        return { tags: [...a.querySelectorAll('.tag')].map(t=>__norm(t.innerText)), facts: Object.fromEntries([...a.querySelectorAll('dt')].map(dt=>[__norm(dt.innerText), __norm(dt.nextElementSibling?.innerText)])) }; })()`);
      return value?.facts['Last connected'] && value.facts['Last connected'] !== 'Not yet' ? value : null;
    }, 'the card shows its connection activity', 25000);
    assert.ok(card.tags.includes('Connects to this dashboard'), JSON.stringify(card));
    assert.ok(!('Address' in card.facts), `no address field: ${JSON.stringify(card)}`);
    // Real monitoring data, made on the node itself, read in the page through the dashboard.
    const heartbeat = 'Paired node heartbeat', pushToken = randomBytes(32).toString('hex'); secrets.add(pushToken);
    const made = await atPaired('/api/monitors', 'POST', { name: heartbeat, type: 'push', target: '', internalOnly: true, intervalMs: 1000, timeoutMs: 750, nodeIds: [id], enabled: true, push: { token: pushToken, allowGet: true } });
    assert.equal(made.status, 201, JSON.stringify(made.value));
    const viewBegin = fixture.stats().paths.length;
    await until(() => page.eval(`!![...document.querySelectorAll('button')].find(e=>e.getAttribute('aria-label')===${q('Open ' + label)})`), 'the paired node can be opened', 25000);
    await ready(); await page.eval(`[...document.querySelectorAll('button')].find(e=>e.getAttribute('aria-label')===${q('Open ' + label)}).click()`);
    await viewing(label);
    await page.eval('location.hash="#/monitors"');
    await text(heartbeat, 30000);
    const reads = fixture.stats().paths.slice(viewBegin);
    assert.ok(reads.some(entry => entry.path === '/api/overview' && entry.registration === row.id && entry.status === 200), 'the page read the paired node through the dashboard');
    const sent = fixture.stats().paths.slice(begin);
    assert.ok(!sent.some(entry => entry.path === '/api/auth/login' || entry.path === '/api/auth/logout'), 'no sign-in or sign-out');
    assert.ok(sent.filter(entry => entry.path.startsWith('/api/dashboard/invitations') || entry.path === '/api/dashboard/onboarding').every(entry => entry.registration === null), 'invitation routes never carry a node selection');
    // The browser asked only the dashboard: never the node, its connection address or the private listener.
    const privateOrigins = new Set([connection.url, connection.privateOrigin, node.url]);
    const originOf = url => { try { return new URL(url).origin; } catch { return ''; } };
    const browserCalls = report.requests.slice(browserBegin);
    assert.ok(!browserCalls.some(value => privateOrigins.has(originOf(value.url))), 'no browser request to the node or its connection');
    assert.ok(!report.blocked.slice(blockedBegin).some(url => privateOrigins.has(originOf(url))), 'none attempted either');
    assert.ok(browserCalls.filter(value => /^\/(api|public)\//u.test(new URL(value.url).pathname)).every(value => new URL(value.url).origin === fixture.origin));
    // Only the production node used the HTTPS front, with its own credentials on the four fixed routes and no cookie.
    const carried = connection.stats();
    assert.deepEqual([carried.cookies, carried.origins, carried.refused, carried.overflow, carried.unauthenticated, carried.unregistered], [0, 0, [], 0, 0, 0], JSON.stringify(carried));
    for (const path of CONNECTION_ROUTES) assert.ok(carried.routes[path] >= 1, `${path} crossed the HTTPS front: ${JSON.stringify(carried.routes)}`);
    assert.ok(node.running(), 'the node keeps running');
    const snapshot = await page.eval('({ html: document.documentElement.outerHTML, local: JSON.stringify({ ...localStorage }), session: JSON.stringify({ ...sessionStorage }) })');
    for (const key of ['CANARY_ADMIN_TOKEN', 'CANARY_CLUSTER_TOKEN', 'CANARY_DASHBOARD_ENROLL_TOKEN']) {
      assert.ok(!snapshot.html.includes(settings[key]) && !snapshot.local.includes(settings[key]) && !snapshot.session.includes(settings[key]), `the page let go of ${key}`);
    }
    await page.eval('location.hash="#/nodes"'); await text(label);
    return {
      registration: row.id, confirmStatus: confirmed.status, outcome, fingerprintMatchesNodeLog: true, explicitCheck: true,
      unchangedBeforeConfirm: true, roleAccountsAfterConfirm: managed.length, outboundTag: true, address: false, viewerRefused,
      monitoringRead: heartbeat, reSignIn: false, browserPrivateRequests: 0, transport: { forwarded: carried.forwarded, routes: carried.routes, tlsErrors: carried.tlsErrors },
    };
  });
  // ----- r5: a pending Create invitation (OutboundNode). The dashboard's server saves an invitation before it answers, and
  // only that answer carries its token. The owned proxy holds the server's 201 (fixture.hold) while the step looks at
  // the dialog: no choices, Close off, Escape refused and Stop the one way out. Escape is a real key press through CDP.
  // This page has no user activation (every click here is a script click), so the browser's close watcher may close
  // the dialog itself despite the prevented cancel event; the Modal must open it again. Once the answer is in, the same
  // Escape asks before closing, which shows the key reaches the dialog. Both invitations are removed again.
  const escapeKey = async () => { for (const type of ['rawKeyDown', 'keyUp']) await page.send('Input.dispatchKeyEvent', { type, key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 }); };
  const settle = ms => new Promise(resolve => setTimeout(resolve, ms));
  const dialogState = () => page.eval(`(() => { const d=document.querySelector('dialog[open]'); if(!d) return null; const x=d.querySelector('.modal-h button[aria-label="Close"]');
    return { radios: d.querySelectorAll('input[type=radio]').length, closeDisabled: x ? x.disabled : null, foot: [...d.querySelectorAll('.modal-foot button')].map(b=>({ text: __norm(b.innerText), disabled: b.disabled })), text: __norm(d.innerText) }; })()`);
  await step('Control plane: while complete setup generation waits for the dashboard’s server, the Add node dialog can’t be closed, escaped or switched; Stop says the invitation may exist; a cancelled invitation says how to invite a deployed node again', async () => {
    const label = 'Held invitation node', stoppedId = 'review-held-stop', id = 'review-held', file = await settingsFile(id);
    const begin = fixture.stats().paths.length;
    const pendingCreate = async where => {
      const state = await dialogState();
      assert.ok(state, `${where}: the dialog is open`);
      assert.deepEqual([state.radios, state.closeDisabled], [0, true], `${where}: no choices and no Close while creating: ${JSON.stringify(state)}`);
      assert.ok(state.foot.some(b => b.text === 'Stop' && !b.disabled) && state.foot.some(b => b.text === 'Creating…' && b.disabled), `${where}: ${JSON.stringify(state.foot)}`);
      assert.ok(!state.text.includes('Invitation ready'), `${where}: still waiting for the answer`);
      return state;
    };
    await page.eval('location.hash="#/nodes"');
    await until(() => page.eval('(document.querySelector("main h1")?.textContent ?? "").includes("Nodes")'), 'the node list');
    await clickFirst('Add node'); await dialogOpen();
    await until(async () => !(await radios())[3].text.includes('Checking whether nodes can connect'), 'the outbound choice follows the server’s answer', 15000);
    await pick('Deploy a new node'); await pick('The node reaches this dashboard');
    await until(() => page.eval('[...document.querySelectorAll("dialog[open] label")].some(l=>__norm(l.innerText)==="Node ID")'), 'the invitation form', 15000);
    await fill('Name on this dashboard', label, 'dialog[open]'); await fill('Node ID', stoppedId, 'dialog[open]');
    await captureOutputs();
    // 1. Stop while the server's answer is held: the page lets go of it and says the invitation may exist.
    let held = fixture.hold('POST', '/api/dashboard/invitations');
    let stopAnswer;
    try {
      await click('Generate setup', 'dialog[open]');
      await until(() => held.answered(), 'the dashboard’s server answered the held create', 15000);
      assert.equal(held.answered().status, 201, 'the server saved the invitation before the page heard back');
      await pendingCreate('held, before Stop');
      await click('Stop', 'dialog[open]');
      await text('The dashboard’s server may have made the invitation anyway.', 10000);
    } finally { held.release(); }
    await settle(200);
    stopAnswer = held.state();
    const afterStop = await dialogState();
    assert.ok(afterStop && afterStop.radios === 4 && afterStop.closeDisabled === false, `after Stop the choices and Close are back: ${JSON.stringify(afterStop)}`);
    assert.ok(!afterStop.text.includes('Invitation ready'), 'no token was shown for the stopped request');
    const stopped = (await fixture.api('/api/dashboard/invitations', 'GET', undefined, owner)).value.find(row => row.nodeId === stoppedId);
    assert.equal(stopped?.state, 'waiting', 'the server kept the invitation whose answer the page let go of');
    assert.equal((await fixture.api(`/api/dashboard/invitations/${stopped.id}`, 'DELETE', undefined, owner)).status, 204, 'fixture cleanup of the stopped invitation');
    // 2. Escape while the answer is held: refused, and the dialog stays open on the same pending form.
    await fill('Node ID', id, 'dialog[open]');
    held = fixture.hold('POST', '/api/dashboard/invitations');
    try {
      await click('Generate setup', 'dialog[open]');
      await until(() => held.answered(), 'the dashboard’s server answered the held create', 15000);
      assert.equal(held.answered().status, 201);
      await pendingCreate('held');
      await page.eval('document.querySelector("dialog[open] .modal-foot button")?.focus(); true');
      await escapeKey(); await settle(400);
      const escaped = await pendingCreate('held, after Escape');
      assert.ok(escaped.text.includes('Name on this dashboard'), 'the same form, not unmounted');
    } finally { held.release(); }
    await text(`Waiting for ${id} to connect`, 15000);
    assert.equal(held.state().delivered, true, 'the held answer reached the page');
    assert.deepEqual(await downloads(), [file], 'its token arrived: the settings are offered under the helper’s name');
    // 3. The same key now reaches the dialog's close request: the token wasn't shown, copied or saved, so it asks.
    await ready();
    await page.eval('[...document.querySelectorAll("dialog[open] .modal-foot button")].find(b=>__norm(b.innerText)==="Close")?.focus(); true');
    await escapeKey();
    await text('Close without saving the invitation?', 10000);
    assert.ok(await page.eval('!!document.querySelector("dialog[open]")'), 'asking keeps the dialog open');
    await click('Keep it open', 'dialog[open]');
    // 4. Cancel it: the notice says how to invite a node that was already deployed with it.
    await click('Cancel invitation', 'dialog[open]');
    await text('Cancel this invitation?');
    await click('Cancel invitation', 'dialog[open]');
    await dialogClosed(15000);
    await text('If the node is already deployed, make the next invitation with “A node that’s already running” and the same node ID, then replace only CANARY_DASHBOARD_ENROLL_TOKEN in its settings.', 10000);
    const creates = fixture.stats().paths.slice(begin).filter(entry => entry.method === 'POST' && entry.path === '/api/dashboard/invitations');
    assert.deepEqual(creates.map(entry => [entry.status, entry.registration]), [[201, null], [201, null]], 'two creates, each saved by the server');
    const left = (await fixture.api('/api/dashboard/invitations', 'GET', undefined, owner)).value ?? [];
    assert.ok(!left.some(row => [id, stoppedId].includes(row.nodeId) && ['waiting', 'connected'].includes(row.state)), 'neither invitation is left waiting');
    return { heldCreates: 2, choicesWhileCreating: 0, closeDisabledWhileCreating: true, escapeKeptPendingDialog: true, stopAnswer, escapeAsksAfterAnswer: true, cancelNoticeNamesRunningNode: true };
  });
  // ----- r5: a new node's tokens belong to one node ID (onboarding.ts secretsForNode). Nothing is deployed or verified:
  // the step reads the settings through Copy, captured in the page, and closes the dialog with Cancel.
  await step('Control plane: a new node’s tokens stay the same when its label or platform changes, are replaced for another node ID without showing revealed values with it, and come back with the first ID', async () => {
    const first = 'review-tokens-a', second = 'review-tokens-b';
    const fileA = await settingsFile(first), fileB = await settingsFile(second);
    const address = `http://127.0.0.1:${await fixture.freePort()}`;
    const begin = fixture.stats().paths.length;
    await page.eval('location.hash="#/nodes"');
    await until(() => page.eval('(document.querySelector("main h1")?.textContent ?? "").includes("Nodes")'), 'the node list');
    await clickFirst('Add node'); await dialogOpen();
    await pick('Deploy a new node');
    await until(() => page.eval('[...document.querySelectorAll("dialog[open] label")].some(l=>__norm(l.innerText)==="Node ID")'), 'the new-node form', 15000);
    await fill('Name on this dashboard', 'Token node', 'dialog[open]');
    await fill('Node ID', first, 'dialog[open]');
    await fill('Node URL', address, 'dialog[open]');
    await text(`--env-file ${fileA}`);
    await captureOutputs();
    const pairOf = settings => [settings.CANARY_ADMIN_TOKEN, settings.CANARY_CLUSTER_TOKEN];
    const a = envOf(await copyOf(fileA));
    for (const value of pairOf(a)) { assert.match(value, /^[A-Za-z0-9_-]{43}$/u); secrets.add(value); }
    assert.equal(a.CANARY_NODE_ID, first);
    // A label change and another platform keep the pair.
    await fill('Name on this dashboard', 'Token node renamed', 'dialog[open]');
    await text('CANARY_NODE_NAME=Token node renamed');
    const renamed = envOf(await copyOf(fileA));
    assert.deepEqual([renamed.CANARY_NODE_NAME, ...pairOf(renamed)], ['Token node renamed', ...pairOf(a)], 'the same pair after a label change');
    await click('Docker Compose', 'dialog[open]');
    assert.deepEqual(pairOf(envOf(await copyOf(fileA))), pairOf(a), 'the same pair on another platform');
    await click('Docker run', 'dialog[open]');
    await text(`--env-file ${fileA}`);
    // Revealed for the first ID. A watcher counts any moment the dialog shows the second ID with revealed token values.
    await clickLabelled(`Show private values in ${fileA}`);
    assert.ok((await page.eval('document.querySelector("dialog[open]").innerText')).includes(a.CANARY_ADMIN_TOKEN), 'Show reveals');
    await page.eval(`(() => { window.__mixed = 0; const d=document.querySelector('dialog[open]');
      const look=()=>{ const t=d.innerText; if(t.includes(${q(`CANARY_NODE_ID=${second}`)}) && /CANARY_(?:ADMIN|CLUSTER)_TOKEN=[A-Za-z0-9_-]{43}/u.test(t)) window.__mixed++; };
      window.__tokenWatch?.disconnect(); window.__tokenWatch=new MutationObserver(look); window.__tokenWatch.observe(d,{ subtree:true, childList:true, characterData:true }); return true; })()`);
    await fill('Node ID', second, 'dialog[open]');
    await text(`--env-file ${fileB}`);
    await text(`Those shown, copied, saved or sent for ${first} stay with that ID.`);
    assert.match(await page.eval('document.querySelector("dialog[open]").innerText'), /CANARY_ADMIN_TOKEN=•+/u, 'the second ID’s settings start hidden');
    const b = envOf(await copyOf(fileB));
    for (const value of pairOf(b)) secrets.add(value);
    assert.equal(b.CANARY_NODE_ID, second);
    assert.ok(pairOf(b).every(value => !pairOf(a).includes(value)) && b.CANARY_ADMIN_TOKEN !== b.CANARY_CLUSTER_TOKEN, 'another node ID gets its own pair');
    assert.deepEqual(await downloads(), [fileB]);
    // Back to the first ID: its own pair, which its copied settings already hold.
    await fill('Node ID', first, 'dialog[open]');
    await text(`--env-file ${fileA}`);
    assert.deepEqual(pairOf(envOf(await copyOf(fileA))), pairOf(a), 'the first ID gets its pair back');
    const mixed = await page.eval('(() => { window.__tokenWatch?.disconnect(); delete window.__tokenWatch; return window.__mixed; })()');
    assert.equal(mixed, 0, 'no moment showed the second ID with revealed tokens');
    await click('Cancel', 'dialog[open]'); await dialogClosed();
    const sent = fixture.stats().paths.slice(begin);
    // AddNodeDialog reads the invitation list through useInvitations() to reserve
    // IDs. That GET is expected; the preview must never verify or mutate one.
    assert.ok(sent.some(entry => entry.method === 'GET' && entry.path === '/api/dashboard/invitations'), 'the wizard read the reserved IDs');
    assert.ok(!sent.some(entry => entry.path === '/api/dashboard/verify' || (entry.path.startsWith('/api/dashboard/invitations') && entry.method !== 'GET')), 'nothing was verified or invited');
    return { samePairAcrossLabelAndPlatform: true, newPairForAnotherId: true, revealedWithOtherId: mixed, firstIdPairBack: true, sentToServer: false };
  });
  await step('Control plane: software updates use cached same-origin metadata, show rebuilds and roles, dismiss per sign-in and never install', async () => {
    const openUpdates = () => page.eval('document.querySelector("button.upd-btn").click()');
    await openUpdates(); await dialogOpen(); await text('Software updates');
    assert.equal(await page.eval('[...document.querySelectorAll("dialog[open] button")].filter(b=>__norm(b.innerText)==="Check now").length'), 0, 'disabled deployment has no manual action');
    await footClose(); await dialogClosed();
    const now = Date.now(), commit = 'a'.repeat(40), begin = fixture.stats().paths.length;
    const adminToken = randomBytes(32).toString('hex'), viewerToken = randomBytes(32).toString('hex');
    for (const token of [adminToken, viewerToken]) secrets.add(token);
    for (const [name, role, token] of [['updates-admin','admin',adminToken],['updates-viewer','viewer',viewerToken]])
      assert.equal((await fixture.api('/api/accounts','POST',{name,role,token},owner)).status,201);
    const enter = async token => {
      // Explicit account switch: refresh itself now restores the sign-in.
      await page.eval("sessionStorage.removeItem('uptime-canary.sign-in')");
      await page.send('Page.reload'); await text('Sign in'); await ready();
      await page.eval('(() => { const choices=[...document.querySelectorAll("label")].filter(l=>l.querySelector("input[type=radio]")&&__norm(l.innerText)==="Account token");if(choices.length!==1)throw Error("token choice missing");choices[0].querySelector("input").click(); })()');
      await fill('Account token',token); await click('Sign in');
      await until(() => page.eval('!!document.querySelector("nav.rail")'), 'token account signed in'); await ready();
    };
    fixture.previewUpdates({ enabled:true, intervalHours:6, state:'ok', checkedAt:now, nextCheckAt:now+21600000, images:[
      { component:'dashboard', label:'Dashboard', image:'ghcr.io/narehood/uptime-canary-web', currentCommit:commit, latestCommit:commit, currentBuiltAt:now-172800000, latestBuiltAt:now-86400000, status:'update-available' },
    ] });
    try {
      await enter(adminToken);
      await until(() => page.eval('!!document.querySelector(".upd-banner")'), 'cached image rebuild notice');
      await click('Details', '.upd-banner'); await dialogOpen(); await text('newer build of the same source');
      assert.equal(await page.eval('[...document.querySelectorAll("dialog[open] a")].filter(a=>a.href.includes("/compare/")).length'), 0, 'a rebuild has no code compare link');
      for (const mode of ['light','dark']) {
        await page.eval(`document.documentElement.dataset.mode=${q(mode)}`);
        for (const width of [390,728,1280]) {
          await page.send('Emulation.setDeviceMetricsOverride', { width, height:900, deviceScaleFactor:1, mobile:false });
          await page.eval('new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)))');
          assert.ok(await page.eval('document.documentElement.scrollWidth<=innerWidth'), `${mode} ${width}: updates overflow`);
        }
      }
      await page.send('Emulation.clearDeviceMetricsOverride');
      await click('Check now', 'dialog[open]');
      await text('Update checks are off in this dashboard');
      assert.equal(fixture.stats().paths.slice(begin).filter(r=>r.method==='POST' && r.path==='/api/dashboard/updates/check').length,1,'one manual request, no automatic retry');
      await footClose(); await dialogClosed();
      // The token-only account password nudge has its own Not now action.
      // Dismiss this update banner, then wait for React to commit its removal.
      await click('Not now', '.upd-banner');
      await until(() => page.eval('!document.querySelector(".upd-banner")'), 'this update notice dismissed');
      assert.equal(await page.eval('!!document.querySelector(".upd-banner")'),false);
      await page.eval('location.hash="#/nodes"'); await ready();
      assert.equal(fixture.stats().paths.slice(begin).filter(r=>r.method==='GET' && r.path==='/api/dashboard/updates').length,1,'navigation reuses the one cached reader');
      await enter(viewerToken);
      await until(() => page.eval('!!document.querySelector(".upd-banner")'), 'new sign-in sees advisory');
      await openUpdates(); await dialogOpen();
      assert.equal(await page.eval('[...document.querySelectorAll("dialog[open] button")].filter(b=>__norm(b.innerText)==="Check now").length'),0,'viewers read, never start checks');
      await footClose(); await dialogClosed();
    } finally {
      fixture.previewUpdates(null);
      await page.send('Emulation.clearDeviceMetricsOverride');
      await enter(adminToken);
    }
    return { cachedReads:true, syntheticMetadata:'UI only; real server authenticates GET and refuses disabled POST', roles:true, explicitManualOnce:true, sameSourceRebuild:true, installs:0, layouts:6 };
  });
  await reviewFleet({ fixture, A, B, page, step, until, fill, click, text, ready, owner, includeOutbound: true });
  await step('Control plane: every browser API request stays at the dashboard origin; no node credential is persisted', async () => {
    const calls = report.requests.slice(start).filter(value => /^\/(api|public)\//u.test(new URL(value.url).pathname));
    assert.ok(calls.length > 10);
    assert.ok(calls.every(value => new URL(value.url).origin === fixture.origin));
    const storage = await page.eval('({local:{...localStorage},session:{...sessionStorage},cookie:document.cookie})');
    assert.deepEqual(Object.keys(storage.session), ['uptime-canary.sign-in']);
    const retained = JSON.parse(storage.session['uptime-canary.sign-in']);
    assert.equal(retained.origin, fixture.origin); assert.equal(retained.mode, 'dashboard');
    assert.ok(Object.keys(storage.local).every(key => ['uptime-canary.endpoint', 'uptime-canary.theme'].includes(key)));
    for (const secret of [A.token, B.token, fixture.code, fixture.password, owner]) if (secret !== retained.token) assert.ok(!JSON.stringify(storage).includes(secret));
    assert.ok(!storage.cookie.includes(fixture.proxyCookie), 'proxy cookie is HttpOnly');
    assert.ok(fixture.stats().authenticated > 0 && fixture.stats().denied > 0);
    return { browserAPIOrigins: [fixture.origin], privateNodeBrowserRequests: 0, persistedCanaryCredentials: 'tab session only' };
  });
  await step('Control plane: the shared node list fits desktop and narrow screens', async () => {
    for (const width of [728, 390]) {
      await page.send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false });
      await page.eval('new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))');
      const size = await page.eval('({width:innerWidth,scroll:document.documentElement.scrollWidth})');
      assert.ok(size.scroll <= size.width, `horizontal overflow at ${width}: ${size.scroll}`);
    }
    await page.send('Emulation.clearDeviceMetricsOverride');
    return { checkedWidths: [728, 390], horizontalOverflow: false };
  });
  await page.send('Network.setBlockedURLs', { urls: [] });
}
