// Real response delays, never fabricated successes or direct product-session mutations.
import { api, assert, card, dialog, pass, rememberSecret, report, secret, sleep, totp, until } from './browser-auth-proof.mjs';

export async function run({ a, b, page: p }, { passwordA, passwordB }) {
  // Earlier viewer/login assertions share the real per-address limit. Start a fresh wall-clock window.
  console.log('WAIT real 60-second fixture login-limit window before delayed-completion cases');
  await sleep(60_000);
  async function connect(node, { token, password, name = 'admin' }) {
    const current = await p.eval('document.querySelector("header.topbar .tb-btn").innerText.replace(/\\s+/g," ").trim()');
    await p.click('button', current);
    await p.field('Node API address', node.url, dialog); await sleep(450);
    await p.click('choice', token ? 'Account token' : 'Password', dialog);
    if (token) await p.field('Account token', token, dialog);
    else { await p.field('Account name', name, dialog); await p.field('Password', password, dialog); }
    await p.click('button', 'Sign in and switch', dialog);
    await until(() => p.eval(`!document.querySelector('dialog[open]')&&document.querySelector('header.topbar')?.innerText.includes(${JSON.stringify(node.id)})`), 'replacement connection established');
  }
  const bConnection = () => connect(b, { password: passwordB });
  async function staysOnB(forbidden = []) {
    assert(await p.eval(`!!document.querySelector('header.topbar')?.innerText.includes(${JSON.stringify(b.id)})`), 'Delayed completion ended replacement connection');
    assert(await p.eval(`!${JSON.stringify(forbidden)}.some(s=>document.body.innerText.includes(s))`), 'Old completion emitted a result on replacement node');
    const sessionB = (await p.lastResponse('/api/auth/login')).value.token;
    assert((await api(b, '/api/auth/me', 'GET', undefined, sessionB)).status === 200, 'Replacement session no longer authenticates');
  }

  // Preserve the existing own-token adoption UX, including the one-time secret panel and live polling.
  await p.route('/accounts'); await p.text('Add an account');
  await p.eval('window.__authProofShell=document.querySelector(".shell");true');
  const oldToken = a.token;
  await p.click('button', 'Manage admin'); await p.click('button', 'Replace API token', dialog); await p.text('New API token for');
  a.token = rememberSecret(await p.eval(`document.querySelector('input[aria-label="API token for admin"]').value`));
  assert(a.token !== oldToken && (await api(a, '/api/auth/me', 'GET', undefined, oldToken)).status === 401 && (await api(a, '/api/auth/me')).status === 200, 'Own-token rotation authentication failed');
  assert(await p.eval('window.__authProofShell===document.querySelector(".shell")&&!!document.querySelector(".issued")'), 'Own-token adoption remounted or lost issued token');
  const polls = p.requests.filter(r => r.method === 'GET' && r.url === a.url + '/api/overview').length;
  await until(() => p.requests.filter(r => r.method === 'GET' && r.url === a.url + '/api/overview').length > polls, 'poll after own-token adoption');
  assert(await p.eval('window.__authProofShell===document.querySelector(".shell")&&!!document.querySelector(".issued")&&!document.body.innerText.includes("Token rejected")'), 'Own-token adoption did not retain live UI');
  pass('own-token rotation adopts working replacement without remounting or losing its one-time panel');
  await p.click('button', 'I’ve stored it');

  // A distinct account makes stale setAccount observable even though both bootstrap admins have the same ID.
  const actor = { ...a, token: secret() }, actorName = 'proof-delayed-actor', actorPassword = secret(), actorNext = secret();
  assert((await api(a, '/api/accounts', 'POST', { name: actorName, role: 'viewer', token: actor.token })).status === 201, 'Delayed actor fixture');
  await connect(a, { token: actor.token }); await p.route('/account'); await p.text('Choose a password');
  let held = await p.holdResponse(a, '/api/auth/password');
  await p.field('New password', actorPassword, card('Choose a password')); await p.field('Confirm new password', actorPassword, card('Choose a password'));
  await p.click('button', 'Set password'); await held.wait();
  assert((await api(actor, '/api/auth/me')).value.passwordEnabled, 'Held password setup did not reach server');
  await bConnection(); await held.release();
  await staysOnB([actorName, 'Your password is set']);
  pass('delayed token-authorized password setup cannot set replacement account or emit old success');

  await connect(a, { password: actorPassword, name: actorName }); await p.text('Change password');
  held = await p.holdResponse(a, '/api/auth/password');
  await p.field('Current password', actorPassword, card('Change password')); await p.field('New password', actorNext, card('Change password')); await p.field('Confirm new password', actorNext, card('Change password'));
  await p.click('button', 'Change password'); await held.wait();
  await bConnection(); await held.release(); await staysOnB(['Password changed. Sign in']);
  pass('delayed password-session password change cannot end replacement connection');

  await connect(a, { password: actorNext, name: actorName });
  held = await p.holdResponse(a, '/api/auth/totp/setup');
  await p.field('Current password', actorNext, card('Two-factor authentication')); await p.click('button', 'Set up authenticator'); await held.wait();
  await bConnection(); await held.release(); await staysOnB([actorName]);
  assert(await p.eval('!document.querySelector(".keybox")&&!document.querySelector("svg.qr")'), 'Delayed seed appeared on replacement connection');
  pass('delayed MFA setup seed stays off replacement connection');

  await connect(a, { password: actorNext, name: actorName });
  await p.field('Current password', actorNext, card('Two-factor authentication')); await p.click('button', 'Set up authenticator'); await p.text('Set up your authenticator');
  const enrollment = (await p.lastResponse('/api/auth/totp/setup')).value;
  held = await p.holdResponse(a, '/api/auth/totp/confirm');
  await p.field('Authenticator code', totp(enrollment.secret)); await p.click('button', 'Turn on two-factor'); await held.wait();
  assert((await api(actor, '/api/auth/me')).value.mfaEnabled, 'Held MFA confirmation did not reach server');
  await bConnection(); await held.release(); await staysOnB(['Two-factor authentication is on. Sign in']);
  pass('delayed MFA confirmation cannot end replacement connection');

  await connect(a, { password: passwordA }); await p.route('/account');
  held = await p.holdResponse(a, '/api/auth/logout-all');
  await p.click('button', 'Sign out everywhere'); await p.click('button', 'Sign out everywhere', dialog); await held.wait();
  await p.route('/accounts'); await bConnection(); await held.release(); await staysOnB(['Signed out of every password session']);
  pass('delayed Sign out everywhere cannot end or notify replacement connection');

  await connect(a, { password: passwordA }); await p.route('/account');
  held = await p.holdResponse(a, '/api/auth/logout');
  await p.click('button', 'Sign out', 'document.querySelector("header.topbar")'); await held.wait();
  await bConnection();
  const replacementNotices = await p.eval('[...document.querySelectorAll(".toast-t")].map(e=>e.textContent)');
  assert(replacementNotices.some(s => s.includes(b.id)), 'Replacement sign-in notice missing');
  await held.release(); await staysOnB([`Signed out. ${a.id} ended this session.`]);
  assert(await p.eval(`${JSON.stringify(replacementNotices)}.every(s=>[...document.querySelectorAll('.toast-t')].some(e=>e.textContent===s))`), 'Delayed sign-out cleared replacement notices');
  pass('delayed sign-out cannot end replacement connection or clear its notices');

  await connect(a, { token: a.token }); await p.route('/accounts'); await p.text('Add an account');
  held = await p.holdResponse(a, '/api/accounts');
  const add = card('Add an account');
  await p.field('Name', 'proof-delayed-created', add);
  await p.click('choice', 'Password For people using the dashboard. They can add two-factor authentication themselves.', add);
  await p.click('choice', 'API token For the CLI, scripts and headless access.', add);
  await p.click('button', 'Create account', add); await held.wait();
  assert((await api(a, '/api/accounts')).value.some(u => u.name === 'proof-delayed-created'), 'Held create did not reach A');
  await bConnection(); await held.release(); await staysOnB(['proof-delayed-created']);
  assert(await p.eval('!document.querySelector(".issued")'), 'Delayed token issuance appeared on B');
  assert(!(await api(b, '/api/accounts')).value.some(u => u.name === 'proof-delayed-created'), 'Delayed create reached B');
  pass('delayed account creation cannot publish its issued token or success on replacement node');

  // A self-edit would end a password connection if its result escaped the guard.
  await connect(a, { password: passwordA }); await p.route('/accounts'); await p.click('button', 'Manage admin');
  const admin = (await api(a, '/api/auth/me')).value;
  held = await p.holdResponse(a, `/api/accounts/${admin.id}`, 'PUT');
  await p.field('Name', 'proof-delayed-admin', dialog); await p.click('button', 'Save name and role', dialog); await held.wait();
  await p.route('/account'); await bConnection(); await held.release(); await staysOnB(['proof-delayed-admin', 'Your account changed']);
  assert((await api(a, '/api/auth/me')).value.name === 'proof-delayed-admin', 'Held edit did not reach A');
  pass('delayed self-admin edit cannot end replacement connection or emit old success');

  // Use B's token here: unguarded adoption would replace it with a token issued only by A.
  await connect(a, { token: a.token }); await p.route('/accounts'); await p.click('button', 'Manage proof-delayed-admin');
  held = await p.holdResponse(a, `/api/accounts/${admin.id}`, 'PUT');
  await p.click('button', 'Replace API token', dialog); await held.wait();
  await p.route('/account'); await connect(b, { token: b.token }); await held.release();
  assert(await p.eval(`document.querySelector('header.topbar')?.innerText.includes(${JSON.stringify(b.id)})&&!document.querySelector('.issued')&&!document.body.innerText.includes('Replaced the API token')`), 'Delayed self-token result escaped');
  const bPolls = p.requests.filter(r => r.method === 'GET' && r.url === b.url + '/api/overview').length;
  await until(() => p.requests.filter(r => r.method === 'GET' && r.url === b.url + '/api/overview').length > bPolls, 'replacement token poll');
  const overview = [...p.requests].reverse().find(r => r.method === 'GET' && r.url === b.url + '/api/overview');
  assert(Object.entries(overview.headers).some(([k,v]) => k.toLowerCase() === 'authorization' && v === `Bearer ${b.token}`), 'Delayed adoption changed replacement API credential');
  pass('delayed own-token rotation cannot adopt old-node token into replacement token connection');

  const stored = await p.eval(`(async()=>({local:{...localStorage},session:{...sessionStorage},cookies:document.cookie,databases:await indexedDB.databases(),caches:await caches.keys()}))()`);
  assert(Object.keys(stored.local).every(k => ['uptime-canary.theme','uptime-canary.endpoint'].includes(k)) && !Object.keys(stored.session).length && !stored.cookies && !stored.databases.length && !stored.caches.length, 'Auth data persisted');
  assert(!p.requests.some(r => r.hasUrlCredential), 'Auth credential entered URL');
  assert(!report.errors.length, 'Browser runtime exception during regression proof');
  assert(!report.network.some(r => !r.blocked && ![new URL(p.requests[0].url).origin, a.url, b.url].includes(r.origin)), 'External request escaped fixture isolation');
  pass('regressions preserve memory-only credentials, URL hygiene, isolated requests and zero browser exceptions');
}
