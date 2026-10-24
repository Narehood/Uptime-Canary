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
  const proxy = createServer((req, res) => {
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
      paths.push({ path: req.url.split('?')[0], authorization: Boolean(req.headers.authorization), registration: req.headers['x-canary-registered-node'] ?? null });
    }
    const headers = { ...req.headers, host: new URL(backend).host };
    delete headers.cookie;
    const upstream = upstreamRequest(new URL(req.url, backend), { method: req.method, headers, timeout: 14000 }, response => {
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
  return {
    origin, backend, child, code, password, proxyCookie,
    expire() { session = ''; },
    stats: () => ({ denied, authenticated, paths: [...paths] }),
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
