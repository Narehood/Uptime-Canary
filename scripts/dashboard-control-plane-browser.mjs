// Real Go dashboard account server behind a private cookie-gated proxy.
// Reuses the bounded harness's one executable, web build and browser profile.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { once } from 'node:events';
import { existsSync, readFileSync } from 'node:fs';
import { createServer, request as upstreamRequest } from 'node:http';
import { join } from 'node:path';

export async function createDashboardControlPlaneFixture({ runDir, port, env, owned, servers, until, secrets }) {
  const backend = `http://127.0.0.1:${await port()}`;
  const origin = `http://127.0.0.1:${await port()}`;
  const identity = join(runDir, 'dashboard-identity');
  const password = 'Private dashboard browser fixture password';
  const proxyCookie = 'canary_dashboard_fixture_proxy';
  let session = '', denied = 0, authenticated = 0;
  const paths = [];
  const watchers = new Set();
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
    const headers = { ...req.headers, host: new URL(backend).host };
    delete headers.cookie;
    const upstream = upstreamRequest(new URL(req.url, backend), { method: req.method, headers, timeout: 14000 }, response => {
      if (entry) entry.status = response.statusCode;
      const returned = { ...response.headers }; delete returned['set-cookie'];
      res.writeHead(response.statusCode, returned); response.pipe(res);
    });
    upstream.on('timeout', () => upstream.destroy());
    upstream.on('error', () => { if (!res.headersSent) res.writeHead(502); res.end(); });
    res.on('close', () => { if (!res.writableEnded) upstream.destroy(); });
    req.pipe(upstream);
  });
  proxy.listen(Number(new URL(origin).port), '127.0.0.1'); await once(proxy, 'listening'); servers.push(proxy);
  const child = spawn(join(runDir, process.platform === 'win32' ? 'review-node.exe' : 'review-node'), ['dashboard'], {
    cwd: runDir, env: { ...env, CANARY_DASHBOARD_DATA: join(runDir, 'dashboard-data'), CANARY_DASHBOARD_IDENTITY: identity,
      CANARY_DASHBOARD_ASSETS: join(runDir, 'dist'), CANARY_DASHBOARD_HOST: '127.0.0.1',
      CANARY_DASHBOARD_PORT: new URL(backend).port, CANARY_DASHBOARD_ORIGINS: origin },
    windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'],
  });
  owned.push(child);
  let log = ''; for (const stream of [child.stdout, child.stderr]) stream.on('data', value => { log = (log + value).slice(-16000); });
  await until(async () => {
    if (child.exitCode !== null) throw Error('Private dashboard fixture exited during startup');
    try { return (await fetch(backend + '/healthz')).ok && existsSync(join(identity, 'setup.code')); } catch { return false; }
  }, 'private dashboard account service', 30000);
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
  return {
    origin, backend, child, code, password, proxyCookie, route,
    expire() { session = ''; },
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

export async function reviewDashboardControlPlane({ fixture, A, B, page, step, until, fill, click, text, ready, report, secrets }) {
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
  await step('Control plane: select two nodes without another sign-in; monitoring and account routes remain separate', async () => {
    for (const label of ['First private node', 'Second private node']) {
      await page.eval('location.hash="#/nodes"'); await text(label);
      await until(() => page.eval(`!![...document.querySelectorAll('button')].find(e=>e.getAttribute('aria-label')===${q('Open ' + label)})`), 'registered-node open action');
      await ready(); await page.eval(`[...document.querySelectorAll('button')].find(e=>e.getAttribute('aria-label')===${q('Open ' + label)}).click()`);
      await until(() => page.eval('!!document.querySelector("main h1") && document.querySelector("main h1").textContent.includes("Overview")'), 'selected-node overview');
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
      name: 'Dashboard heartbeat fixture', type: 'push', target: '', intervalMs: 1000, timeoutMs: 750,
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
    await until(() => page.eval('!!document.querySelector("form") && !document.querySelector("nav.rail")'), 'dashboard sign-in after proxy authentication');
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
  const viewing = label => until(() => page.eval(`(document.querySelector('.topbar .tb-btn .t1')?.textContent ?? '').includes(${q('Viewing ' + label)})`), `viewing ${label}`);
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
    await until(() => page.eval(`(() => { const snap=document.querySelector('.topbar .snap'); return (document.querySelector('main h1')?.textContent ?? '').includes('Overview') && !!snap && !snap.classList.contains('bad') && ![...document.querySelectorAll('.banner')].some(b=>/Updating node access|answering \\(/.test(b.innerText)); })()`), 'monitoring answers again', 60000);
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
  await step('Control plane: every browser API request stays at the dashboard origin; no node credential is persisted', async () => {
    const calls = report.requests.slice(start).filter(value => /^\/(api|public)\//u.test(new URL(value.url).pathname));
    assert.ok(calls.length > 10);
    assert.ok(calls.every(value => new URL(value.url).origin === fixture.origin));
    const storage = await page.eval('({local:{...localStorage},session:{...sessionStorage},cookie:document.cookie})');
    assert.deepEqual(storage.session, {});
    assert.ok(Object.keys(storage.local).every(key => ['uptime-canary.endpoint', 'uptime-canary.theme'].includes(key)));
    for (const secret of [A.token, B.token, fixture.code, fixture.password, owner]) assert.ok(!JSON.stringify(storage).includes(secret));
    assert.ok(!storage.cookie.includes(fixture.proxyCookie), 'proxy cookie is HttpOnly');
    assert.ok(fixture.stats().authenticated > 0 && fixture.stats().denied > 0);
    return { browserAPIOrigins: [fixture.origin], privateNodeBrowserRequests: 0, persistedCanaryCredentials: false };
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
