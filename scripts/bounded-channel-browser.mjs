// Chrome/CDP fallback after two native preview snapshot timeouts. No screenshots,
// source copies or retained profiles. The storage guard owns the fixture tree.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createServer } from 'node:net';
import { once } from 'node:events';
import { join } from 'node:path';

const sleep = ms => new Promise(done => setTimeout(done, ms));
async function until(fn, label, ms = 15000) {
  const end = Date.now() + ms;
  do { const value = await fn(); if (value) return value; await sleep(100); } while (Date.now() < end);
  throw Error('Timeout: ' + label);
}
class Cdp {
  seq = 0; pending = new Map();
  constructor(ws, errors) {
    this.ws = ws;
    ws.addEventListener('message', event => {
      const message = JSON.parse(event.data);
      if (message.id) {
        const pending = this.pending.get(message.id); if (!pending) return;
        this.pending.delete(message.id);
        message.error ? pending.reject(Error(message.error.message)) : pending.resolve(message.result);
      } else if (message.method === 'Runtime.exceptionThrown') errors.push('page exception');
      else if (message.method === 'Runtime.consoleAPICalled' && message.params.type === 'error') errors.push('console error');
    });
  }
  send(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.seq; this.pending.set(id, { resolve, reject }); this.ws.send(JSON.stringify({ id, method, params })); }); }
  async eval(expression) {
    const r = await this.send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
    if (r.exceptionDetails) throw Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
    return r.result.value;
  }
}
export async function review(s) {
  const checks = [], errors = []; let ws, cdp;
  const server = createServer(); server.listen(0, '127.0.0.1'); await once(server, 'listening');
  const port = server.address().port; await new Promise(done => server.close(done));
  const chrome = spawn('C:/Program Files/Google/Chrome/Application/chrome.exe', ['--headless=new', `--remote-debugging-port=${port}`, `--user-data-dir=${join(s.work, 'chrome-profile')}`, '--no-first-run', '--no-default-browser-check', '--disable-background-networking', '--disable-component-update', '--disable-sync', '--disable-extensions', '--no-proxy-server', '--password-store=basic', 'about:blank'], { windowsHide: true, stdio: 'ignore' });
  const exited = once(chrome, 'exit');
  const q = JSON.stringify;
  const api = async (path, method = 'GET', body, token = s.token) => {
    const r = await fetch(s.endpoint + path, { method, headers: { Authorization: `Bearer ${token}`, ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) }, body: body === undefined ? undefined : q(body), signal: AbortSignal.timeout(10000) });
    const raw = await r.text(); assert.ok(r.ok, 'private fixture API returned ' + r.status); return raw ? JSON.parse(raw) : null;
  };
  const pass = name => { checks.push(name); console.log('PASS browser ' + name); };
  try {
    const targets = await until(async () => { try { return await (await fetch(`http://127.0.0.1:${port}/json`)).json(); } catch { return false; } }, 'Chrome');
    ws = new WebSocket(targets.find(t => t.type === 'page').webSocketDebuggerUrl); await once(ws, 'open'); cdp = new Cdp(ws, errors);
    await cdp.send('Page.enable'); await cdp.send('Runtime.enable');
    await cdp.send('Page.navigate', { url: s.origin });
    await until(() => cdp.eval(`Boolean(document.querySelector('input[name="token"]'))`), 'token sign-in');
    const helpers = `window.__field=(label)=>{const l=[...document.querySelectorAll('label')].find(e=>e.htmlFor&&e.textContent.trim()===label);if(!l)throw Error('Missing field '+label);return document.getElementById(l.htmlFor)};window.__set=(e,v)=>{Object.getOwnPropertyDescriptor(e.tagName==='SELECT'?HTMLSelectElement.prototype:HTMLInputElement.prototype,'value').set.call(e,v);e.dispatchEvent(new Event(e.tagName==='SELECT'?'change':'input',{bubbles:true}))};window.__button=(text)=>{const b=[...document.querySelectorAll('button')].find(e=>e.textContent.trim()===text);if(!b)throw Error('Missing button '+text);return b};true`;
    await cdp.eval(helpers);
    await cdp.eval(`__set(document.querySelector('input[name="token"]'),${q(s.token)});document.querySelector('form').requestSubmit();true`);
    await until(() => cdp.eval(`Boolean(document.querySelector('a[href="#/notifications"]'))`), 'signed-in shell'); pass('private sign-in');
    const nav = async hash => { await cdp.eval(`location.hash=${q(hash)}`); await sleep(150); };
    const fill = async (label, value) => { await cdp.eval(`__set(__field(${q(label)}),${q(value)});true`); await sleep(35); };
    const check = async label => { await cdp.eval(`(()=>{const l=[...document.querySelectorAll('label')].find(e=>e.textContent.trim().startsWith(${q(label)})&&e.querySelector('input[type="checkbox"]'));if(!l)throw Error('Missing checkbox');l.querySelector('input').click();return true})()`); await sleep(35); };
    const disclose = async title => { await cdp.eval(`(()=>{const b=[...document.querySelectorAll('.disc-h')].find(e=>e.querySelector('.disc-t')?.textContent.trim()===${q(title)});if(!b)throw Error('Missing disclosure');if(b.getAttribute('aria-expanded')!=='true')b.click();return true})()`); await sleep(50); };
    for (const kind of ['slack', 'telegram', 'ntfy', 'gotify', 'pushover']) {
      await nav('#/channels/new');
      await until(() => cdp.eval(`Boolean(document.querySelector('input[value="${kind}"]'))`), 'provider picker');
      await cdp.eval(`document.querySelector('input[value="${kind}"]').click();true`); await sleep(60);
      await fill('Name', 'Browser ' + kind);
      if (kind === 'slack') { await fill('Slack webhook URL', s.sink + '/services/TTEST/BTEST/fixture_key'); await check('Allow plain HTTP'); }
      if (kind === 'telegram') { await fill('Bot token', '12345:fixture_bot'); await fill('Chat', '12345'); await disclose('API endpoint'); await fill('API base URL (optional)', s.sink + '/telegram'); await check('Allow plain HTTP for this server'); }
      if (kind === 'ntfy') { await fill('Topic', 'canary'); await fill('Server URL (optional)', s.sink + '/ntfy'); await check('Allow plain HTTP for this server'); }
      if (kind === 'gotify') { await fill('Server URL', s.sink + '/gotify'); await fill('Application token', 'fixture_gotify_key'); await check('Allow plain HTTP for this server'); }
      if (kind === 'pushover') { await fill('Application API token', 'a'.repeat(30)); await fill('User or group key', 'b'.repeat(30)); await disclose('API endpoint'); await fill('API base URL (optional)', s.sink + '/pushover'); await check('Allow plain HTTP for this server'); }
      await cdp.eval(`__button('Create channel').click();true`);
      const channel = await until(async () => (await api('/api/notification-channels')).map(v => v.channel).find(v => v.name === 'Browser ' + kind), 'form save ' + kind);
      const expected = kind === 'slack' ? channel.config.slack.webhookURL : channel.config[kind].baseURL;
      assert.equal(new URL(expected).origin, s.sink, 'sending is restricted to the private loopback provider fixture');
      pass(kind + ' created through form');
      await until(() => cdp.eval(`location.hash==='#/notifications'`), 'form navigation');
      await nav(`#/channels/${channel.id}/edit`);
      await until(() => cdp.eval(`Boolean(document.querySelector('button[type="submit"]'))`), 'edit form');
      await fill('Name', 'Renamed ' + kind); await cdp.eval(`__button('Save changes').click();true`);
      const renamed = await until(async () => { const v = await api('/api/notification-channels/' + channel.id); return v.name === 'Renamed ' + kind ? v : false; }, 'rename');
      assert.deepEqual(renamed.config, channel.config); pass(kind + ' rename keeps exact stored options');
      // The UI's existing explicit confirmation is verified below. This API
      // test exercises the rendered form config only after proving loopback.
      await api('/api/notification-channels/' + channel.id + '/test', 'POST', { revision: renamed.revision });
      await until(async () => (await api('/api/notification-deliveries')).some(v => v.channelId === channel.id && v.state === 'accepted'), 'provider acceptance');
      pass(kind + ' saved config reaches local provider'); await sleep(5100);
    }
    await nav('#/notifications'); await sleep(200);
    assert.ok(await cdp.eval(`document.body.innerText.includes('Send test')===false`), 'a test sends only after opening confirmation');
    const confirm = await cdp.eval(`(()=>{const b=[...document.querySelectorAll('button')].find(e=>e.textContent.trim()==='Test');if(!b)throw Error('Test action missing');b.click();return true})()`);
    assert.ok(confirm);
    // Modal is a native HTML dialog: its accessible role is implicit.
    await until(() => cdp.eval(`Boolean(document.querySelector('dialog[open],[role="dialog"]'))`), 'native confirmation dialog');
    assert.ok(await cdp.eval(`document.body.innerText.includes('Send test')&&!document.body.innerText.includes('all 0 recipients')`)); pass('explicit test confirmation has destination wording');
    await cdp.eval(`__button('Send test').click();true`);
    await until(async () => (await api('/api/notification-deliveries')).filter(v => v.state === 'accepted').length === 6, 'confirmed browser test'); pass('UI confirmation sends accepted local test');
    await cdp.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: false });
    assert.ok(await cdp.eval(`document.documentElement.scrollWidth<=innerWidth+1`)); pass('390px layout');
    const storage = await cdp.eval(`Object.fromEntries(Object.entries(localStorage))`);
    assert.deepEqual(storage, { 'uptime-canary.endpoint': s.endpoint }); pass('browser stores endpoint only');
    assert.equal(errors.length, 0); pass('no page or console errors');
    return { passed: true, checks, errors };
  } catch (error) {
    let message = error.message.replaceAll(s.token, '[fixture-secret]');
    return { passed: false, checks, errors, failure: message.slice(0, 1500) };
  } finally {
    if (cdp) await cdp.send('Browser.close').catch(() => {});
    if (ws) ws.close();
    if (chrome.exitCode === null) await Promise.race([exited, sleep(3000)]);
    if (chrome.exitCode === null) { chrome.kill(); await exited; }
  }
}
