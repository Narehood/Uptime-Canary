// Private cookie-gated reverse proxy for the existing bounded dashboard harness.
// No NetBird/Tailscale service, user credential, or external endpoint is contacted.
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { once } from 'node:events';
import { createServer, request } from 'node:http';

const COOKIE = 'canary_fixture_proxy_session';

export async function createProxyAuthFixture({ origin, node, peer, servers }) {
  for (const url of [origin, node.url, peer.url]) assert.equal(new URL(url).hostname, '127.0.0.1');
  let enabled = false, gateway = false, activeSession = '', gateRequests = 0, denied = 0, loginPages = 0;
  let authenticated = 0, publicStatus = 0;
  const crossRequests = [];
  const cookiePresent = req => (req.headers.cookie || '').split(';').some(part => part.trim() === `${COOKIE}=${activeSession}`) && activeSession !== '';
  function forward(req, res, endpoint) {
    const target = new URL(req.url, endpoint);
    const headers = { ...req.headers, host: target.host };
    delete headers.cookie;
    const upstream = request(target, { method: req.method, headers, timeout: 10000 }, response => {
      res.writeHead(response.statusCode, response.headers);
      response.pipe(res);
    });
    upstream.on('timeout', () => upstream.destroy());
    upstream.on('error', () => { if (!res.headersSent) res.writeHead(502); res.end(); });
    res.on('close', () => { if (!res.writableEnded) upstream.destroy(); });
    req.pipe(upstream);
  }
  // Same cookie host, different origin/port: cookies would be eligible if the
  // dashboard accidentally changed its request policy to credentials: include.
  const cross = createServer((req, res) => {
    if (!req.url.startsWith('/api/')) { res.writeHead(404).end(); return; }
    assert.ok(crossRequests.length < 1000, 'bounded cross-origin request evidence');
    crossRequests.push({ path: req.url.split('?')[0], cookie: Boolean(req.headers.cookie), bearer: req.headers.authorization === `Bearer ${peer.token}` });
    forward(req, res, peer.url);
  });
  cross.listen(0, '127.0.0.1'); await once(cross, 'listening'); servers.push(cross);
  const crossOrigin = `http://127.0.0.1:${cross.address().port}`;
  return {
    crossOrigin, cookieName: COOKIE,
    enable() { enabled = true; },
    enableGateway() { gateway = true; },
    expire() { activeSession = ''; },
    disable() { enabled = false; activeSession = ''; },
    stats: () => ({ gateRequests, denied, loginPages, authenticated, publicStatus, crossRequests: [...crossRequests] }),
    handle(req, res) {
      if (!enabled) return false;
      if (req.url === '/fixture/proxy-login') {
        activeSession = randomBytes(24).toString('hex');
        res.writeHead(302, { 'Set-Cookie': `${COOKIE}=${activeSession}; Path=/; HttpOnly; SameSite=Lax`, Location: '/', 'Cache-Control': 'no-store' }).end();
        return true;
      }
      if (req.url === '/fixture/proxy-login-required') {
        loginPages++;
        res.writeHead(200, { 'Content-Type': 'text/html', 'Cache-Control': 'no-store' }).end('<h1>Private fixture proxy sign-in required</h1>');
        return true;
      }
      if (!cookiePresent(req)) {
        denied++;
        res.writeHead(302, { Location: '/fixture/proxy-login-required', 'Cache-Control': 'no-store' }).end();
        return true;
      }
      if (gateway && req.url === '/canary-config.js') {
        res.writeHead(200, { 'Content-Type': 'text/javascript', 'Cache-Control': 'no-store' }).end('globalThis.__CANARY_DASHBOARD_GATEWAY__ = true;');
        return true;
      }
      if (!req.url.startsWith('/api/') && !req.url.startsWith('/public/')) return false;
      gateRequests++;
      if (req.headers.authorization === `Bearer ${node.token}`) authenticated++;
      if (req.url === '/api/auth/status' && !req.headers.authorization) publicStatus++;
      forward(req, res, node.url);
      return true;
    },
  };
}

export async function reviewProxyAuth({ fixture, state, A, B, page, step, signIn, until, text, ready, report }) {
  const q = JSON.stringify;
  fixture.enable();
  try {
    await page.send('Page.navigate', { url: state.origin + '/fixture/proxy-login' });
    await until(() => page.eval('location.pathname === "/" && !!document.querySelector("form") && !document.querySelector("nav.rail")'), 'private proxy page');
    await ready();
    await step('Proxy auth: cookie omission is blocked; proxy session alone cannot authenticate to Canary', async () => {
      const result = await page.eval(`(async () => {
        const missing = await fetch('/api/auth/status', { credentials: 'omit', redirect: 'manual', cache: 'no-store' });
        const alone = await fetch('/api/overview', { credentials: 'same-origin', redirect: 'error', cache: 'no-store' });
        const wrong = await fetch('/api/overview', { credentials: 'same-origin', redirect: 'error', cache: 'no-store', headers: { Authorization: 'Bearer private-invalid-token' } });
        return { omitted: missing.type, cookieOnly: alone.status, wrongToken: wrong.status, visibleCookie: document.cookie };
      })()`);
      assert.equal(result.omitted, 'opaqueredirect');
      assert.equal(result.cookieOnly, 401);
      assert.equal(result.wrongToken, 401);
      assert.ok(!result.visibleCookie.includes(fixture.cookieName), 'the proxy session is HttpOnly');
      return { omittedCookieBlocked: true, cookieOnlyStatus: 401, wrongTokenStatus: 401, httpOnly: true };
    });
    await step('Proxy auth: actual dashboard token sign-in sends the same-origin proxy cookie and Canary bearer', async () => {
      const before = fixture.stats();
      await signIn({ ...A, url: state.origin }, false, () => until(() => fixture.stats().publicStatus > before.publicStatus, 'same-origin auth discovery'));
      const after = fixture.stats();
      assert.ok(after.authenticated >= before.authenticated + 2, 'overview and account identity reached the node with its bearer');
      assert.ok(after.publicStatus > before.publicStatus, 'credential-free auth discovery passed the proxy gate');
      assert.equal(after.denied, before.denied, 'the application did not omit the proxy cookie');
      return { sameOriginSignIn: true, explicitCanaryAuthentication: true };
    });
    await step('Proxy auth: a separately hosted node still receives the Canary bearer without ambient cookies', async () => {
      const cookies = await page.send('Network.getCookies', { urls: [fixture.crossOrigin + '/api/auth/status'] });
      assert.ok(cookies.cookies.some(cookie => cookie.name === fixture.cookieName), 'cookie is eligible for the other port on the same host');
      const before = fixture.stats().crossRequests.length;
      await signIn({ ...B, url: fixture.crossOrigin }, true, () => until(() => fixture.stats().crossRequests.slice(before).some(value => value.path === '/api/auth/status'), 'cross-origin auth discovery'));
      const sent = fixture.stats().crossRequests.slice(before);
      assert.ok(sent.some(value => value.path === '/api/auth/status'));
      assert.ok(sent.filter(value => value.bearer).length >= 2);
      assert.ok(sent.every(value => !value.cookie), 'no cookie sent across origins');
      return { successfulCrossOriginSignIn: true, eligibleCookieExcluded: true };
    });
    await step('Proxy auth: expiry marks the connection stale without following the proxy login redirect', async () => {
      await signIn({ ...A, url: state.origin }, true);
      const before = fixture.stats();
      fixture.expire();
      await until(() => fixture.stats().denied > before.denied, 'expired proxy rejects polling', 16000);
      await text('Not answering', 16000);
      assert.equal(fixture.stats().loginPages, before.loginPages, 'API fetch did not follow the login redirect');
      assert.equal(await page.eval('location.origin'), state.origin);
      assert.ok(await page.eval('!!document.querySelector("nav.rail")'), 'retained data stays visibly stale');
      return { staleConnectionShown: true, redirectsNotFollowed: true };
    });
    await step('Proxy auth: full-page proxy reauthentication restores sign-in without persisting Canary credentials', async () => {
      await page.send('Page.navigate', { url: state.origin + '/fixture/proxy-login' });
      await until(() => page.eval('!!document.querySelector("form") && !document.querySelector("nav.rail")'), 'fresh proxy session page');
      await ready();
      await signIn({ ...A, url: state.origin });
      const storage = await page.eval('({local:{...localStorage},session:{...sessionStorage}})');
      assert.ok(Object.keys(storage.local).every(key => ['uptime-canary.endpoint', 'uptime-canary.theme'].includes(key)));
      assert.deepEqual(storage.session, {});
      assert.ok(!JSON.stringify(storage).includes(A.token));
      assert.ok(!JSON.stringify(storage).includes(B.token));
      return { proxyReauthentication: true, canaryCredentialsRemainInMemory: true };
    });
    await step('Dashboard gateway: sign-in uses its server origin and never sends browser requests to private nodes', async () => {
      fixture.enableGateway();
      const start = report.requests.length;
      await page.send('Page.navigate', { url: state.origin + '/fixture/proxy-login' });
      await until(() => page.eval('!!document.querySelector("form") && !document.querySelector("nav.rail")'), 'gateway sign-in page');
      await ready();
      assert.ok(!await page.eval('[...document.querySelectorAll("input")].some(input => [...input.labels || []].some(label => label.textContent.trim() === "Node API address"))'), 'gateway users cannot select a private node address');
      await signIn({ ...A, url: state.origin });
      await page.eval(`location.hash = '#/status/fixture?node=' + encodeURIComponent(${q(B.url)})`);
      await text('Status unavailable');
      const requests = report.requests.slice(start).filter(value => /^\/(api|public)\//u.test(new URL(value.url).pathname));
      assert.ok(requests.some(value => value.url.startsWith(state.origin + '/api/overview')));
      assert.ok(requests.every(value => new URL(value.url).origin === state.origin), 'private node addresses never reached by the browser');
      return { oneBrowserOrigin: true, serverForwardsToPrivateNode: true, foreignPublicAddressRejected: true };
    });
  } finally {
    fixture.disable();
    await page.send('Network.deleteCookies', { name: fixture.cookieName, url: state.origin });
  }
}
