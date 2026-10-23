// Root-owned acceptance extension. Runs only inside the bounded dashboard fixture.
// Real API writes stay on private loopback nodes; injected responses are explicit safety fixtures.
import assert from 'node:assert/strict';

export async function reviewPublishedAvailability(h) {
  const { api, A, B, page, nav, heading, text, click, choose, fill, until, step, sleep, report } = h;
  let monitor, saved, measured;
  const slug = `observed-${Date.now().toString(36)}`;
  const title = 'Observed acceptance page';
  const path = `/public/status/${slug}`;
  const publicHash = node => `#/status/${slug}?node=${encodeURIComponent(node.url)}`;
  const monitorInput = { name: 'Private observed acceptance monitor', type: 'manual', target: 'manual', intervalMs: 1000,
    timeoutMs: 750, enabled: true, nodeIds: [A.id], manual: { state: 'up', message: 'Private observed acceptance diagnostics' } };
  const input = { slug, title, description: 'Explicit local observed history', enabled: true,
    groups: [{ id: 'services', name: 'Public services', monitors: [] }], announcements: [] };
  const badge = `${A.url}/public/badge/${slug}.svg?${new URLSearchParams({ group: 'services', monitor: 'Published observed service', metric: 'uptime' })}`;
  const refresh = async () => {
    await page.eval(`document.querySelector('details.pub-source').open = true`);
    await click('Refresh');
  };
  const currentPage = async () => {
    const r = await api(A, `/api/status-pages/${saved.id}`);
    assert.equal(r.status, 200, r.text);
    return r.value.statusPage ?? r.value;
  };
  const writePage = async change => {
    const current = await currentPage();
    const r = await api(A, `/api/status-pages/${saved.id}`, 'PUT', { ...current, ...change, revision: current.revision });
    assert.equal(r.status, 200, r.text); saved = r.value;
    return saved;
  };
  const fixtureResponse = async (value, action) => {
    let intercepted = 0;
    h.setHook(p => {
      if (p.request.url !== A.url + path) return false;
      intercepted++;
      void page.send('Fetch.fulfillRequest', { requestId: p.requestId, responseCode: 200,
        responseHeaders: [{ name: 'Content-Type', value: 'application/json' }, { name: 'Access-Control-Allow-Origin', value: '*' }],
        body: Buffer.from(JSON.stringify(value)).toString('base64') });
      return true;
    });
    try { await refresh(); await until(() => intercepted > 0, 'explicit public-response fixture'); await action(); }
    finally { h.setHook(undefined); }
  };

  await step('Public observed: existing selection is off by default and UI opt-in persists', async () => {
    const made = await api(A, '/api/monitors', 'POST', monitorInput);
    assert.equal(made.status, 201, made.text); monitor = made.value;
    input.groups[0].monitors = [{ monitorId: monitor.id, name: 'Published observed service', showCertificate: false }];
    const created = await api(A, '/api/status-pages', 'POST', input);
    assert.equal(created.status, 201, created.text); saved = created.value;
    const before = await api(A, path, 'GET', undefined, null);
    assert.equal(before.value.groups[0].monitors[0].availability, undefined);
    await nav(`#/status-pages/${saved.id}/edit`); await heading(`Edit ${title}`);
    assert.equal(await page.eval(`(() => { const label=[...document.querySelectorAll('label')].find(e=>e.innerText.includes('24 h history')); return label?.querySelector('input')?.checked; })()`), false);
    await choose('24 h history'); await text('including when outages happened'); await click('Save page'); await heading('Status pages');
    saved = await currentPage();
    assert.equal(saved.groups[0].monitors[0].showAvailability, true);
    return { defaultOff: true, explicitSavedOptIn: true };
  });
  await step('Public observed: metadata edit preserves opt-in and selection', async () => {
    const before = structuredClone(saved.groups);
    await nav(`#/status-pages/${saved.id}/edit`); await heading(`Edit ${title}`);
    await fill('Description', 'Renamed description without changing the publication policy');
    await click('Save page'); await heading('Status pages'); saved = await currentPage();
    assert.deepEqual(saved.groups, before);
  });
  await step('Public observed: live history has separate honest figures, hourly table and no credentials', async () => {
    await until(async () => {
      const r = await api(A, path, 'GET', undefined, null);
      const value = r.value.groups[0].monitors[0].availability;
      if (value?.status !== 'available' || !value.summary.knownMs) return false;
      measured = r.value; return true;
    }, 'real rounded-frame observed coverage', 45000);
    const summary = measured.groups[0].monitors[0].availability.summary;
    assert.ok(summary.coveragePercent > 0 && summary.coveragePercent < 1);
    const start = report.requests.length;
    await nav(publicHash(A)); await heading(title); await text('Last 24 h'); await text('coverage');
    await until(() => page.eval(`!!document.querySelector('.pub-obs [role="img"]')`), 'hourly history strip');
    await page.eval(`document.querySelector('.pub-obs-more').open=true`);
    assert.equal(await page.eval(`document.querySelectorAll('.pub-obs table tbody tr').length`), measured.groups[0].monitors[0].availability.buckets.length);
    assert.match(await page.eval(`document.querySelector('.pub-obs').innerText`), /100\.00%/);
    const requests = report.requests.slice(start).filter(r => r.url.includes('/public/'));
    assert.ok(requests.some(r => r.url.includes('/public/status/')));
    assert.ok(requests.every(r => !r.authorization && !r.cookie));
    for (const secret of [monitor.id, monitor.name, monitorInput.manual.message]) assert.ok(!JSON.stringify(measured).includes(secret));
    return { availability: summary.availabilityPercent, coverage: summary.coveragePercent, buckets: measured.groups[0].monitors[0].availability.buckets.length };
  });
  await step('Public observed: uptime badge is a public scoped URL and an actual SVG', async () => {
    await page.eval(`(() => { const d=[...document.querySelectorAll('details')].find(e=>e.querySelector('summary')?.textContent.includes('Embed a status badge')); d.open=true; })()`);
    await until(() => page.eval(`!!document.querySelector('.pub-embed .badge-more')`), 'badge panel');
    await page.eval(`document.querySelector('.pub-embed .badge-more').open=true`);
    await until(() => page.eval(`!!document.querySelector('.pub-embed img[src*="metric=uptime"]')`), 'observed badge image');
    assert.equal(await page.eval(`document.querySelector('.pub-embed img[src*="metric=uptime"]').getAttribute('src')`), badge);
    const response = await fetch(badge, { signal: AbortSignal.timeout(3000) });
    assert.equal(response.status, 200); const svg = await response.text();
    assert.match(svg, /coverage/); assert.match(svg, /100\.00% up/);
    assert.ok(report.requests.filter(r => r.url.includes('metric=uptime')).every(r => !r.authorization && !r.cookie));
  });
  await step('Public observed: historical up time never overrides a real current DOWN', async () => {
    const changed = await api(A, `/api/monitors/${monitor.id}`, 'PUT', { ...monitorInput,
      manual: { state: 'down', message: 'Private new outage' }, revision: monitor.revision });
    assert.equal(changed.status, 200, changed.text); monitor = changed.value;
    await until(async () => {
      const r = await api(A, path, 'GET', undefined, null);
      return r.value.state === 'down' && (r.value.groups[0].monitors[0].availability?.summary?.upMs ?? 0) > 0;
    }, 'new down alongside retained historical up');
    await refresh(); await until(() => page.eval(`!!document.querySelector('.pub-st.s-down')`), 'distinct current down state');
    assert.ok(await page.eval(`!!document.querySelector('.pub-obs')`));
  });
  await step('Public observed: unavailable never draws a cached healthy history', async () => {
    const unavailable = structuredClone(measured);
    unavailable.groups[0].monitors[0].availability = { status: 'unavailable', scope: 'serving-node', from: 0, to: 0 };
    await fixtureResponse(unavailable, async () => {
      await text('not available from this node right now');
      assert.equal(await page.eval(`document.querySelectorAll('.pub-obs-more').length`), 0);
      assert.ok(!(await page.eval(`document.querySelector('.pub-obs').innerText`)).includes('100.00%'));
    });
  });
  await step('Public observed: malformed totals fail closed instead of implying full coverage', async () => {
    const invalid = structuredClone(measured);
    invalid.groups[0].monitors[0].availability.summary.knownMs = 86_400_001;
    await fixtureResponse(invalid, async () => {
      await text('unexpected form');
      assert.equal(await page.eval(`document.querySelectorAll('.pub-obs-more').length`), 0);
      assert.ok(!(await page.eval(`document.querySelector('.pub-obs').innerText`)).includes('100.00%'));
    });
  });
  await step('Public observed: zero known time is unknown availability and zero coverage', async () => {
    const empty = structuredClone(measured), metric = empty.groups[0].monitors[0].availability;
    for (const totals of [metric.summary, ...metric.buckets]) Object.assign(totals, {
      upMs: 0, downMs: 0, pendingMs: 0, unknownMs: totals.endMs - totals.startMs,
      maintenanceMs: 0, knownMs: 0, availabilityPercent: null, coveragePercent: 0,
    });
    await fixtureResponse(empty, async () => {
      await text('No up or down time was observed');
      const copy = await page.eval(`document.querySelector('.pub-obs').innerText`);
      assert.match(copy, /unknown/); assert.match(copy, /0\.00%/); assert.ok(!copy.includes('100.00%'));
    });
    await refresh(); await text('Hourly figures');
  });
  await step('Public observed: history and expanded hourly table have no horizontal page overflow', async () => {
    await page.eval(`document.querySelector('.pub-obs-more').open=true`);
    for (const width of [390, 1440]) {
      await page.send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false });
      assert.equal(await page.eval(`document.documentElement.scrollWidth <= innerWidth`), true, `overflow at ${width}`);
    }
    assert.ok(!report.blocked.length);
    return { widths: [390, 1440], externalCalls: 0 };
  });
  await step('Public observed: switching node rejects a late old-node history response', async () => {
    const other = await api(B, '/api/status-pages', 'POST', { ...input, title: 'Different node without history', groups: [] });
    assert.equal(other.status, 201, other.text);
    await nav(`#/status/${slug}?node=${encodeURIComponent(A.url)}&node=${encodeURIComponent(B.url)}`); await heading(title);
    let held;
    h.setHook(p => { if (p.request.url === A.url + path) { held = p; return true; } return false; });
    try {
      await refresh(); await until(() => held, 'held actual A public request');
      await choose(B.url); await heading('Different node without history');
      try { await page.send('Fetch.continueRequest', { requestId: held.requestId }); } catch (error) {
        assert.match(error.message, /Invalid|not found|intercept/i, 'only cancellation of the obsolete request is expected');
      }
      held = undefined; await sleep(250);
      await heading('Different node without history');
      assert.equal(await page.eval(`document.querySelectorAll('.pub-obs').length`), 0);
    } finally {
      h.setHook(undefined);
      if (held) { try { await page.send('Fetch.continueRequest', { requestId: held.requestId }); } catch {} }
    }
  });
  await step('Public observed: switching slug on the same node clears prior service history', async () => {
    const second = await api(A, '/api/status-pages', 'POST', { ...input, slug: slug + '-other', title: 'Different page without history', groups: [] });
    assert.equal(second.status, 201, second.text);
    await nav(publicHash(A)); await heading(title); await text('Last 24 h');
    await nav(`#/status/${slug}-other?node=${encodeURIComponent(A.url)}`); await heading('Different page without history');
    assert.equal(await page.eval(`document.querySelectorAll('.pub-obs').length`), 0);
  });
  await step('Public observed: unpublishing returns 404 and clears old page, history and embed links', async () => {
    await nav(publicHash(A)); await heading(title); await text('Last 24 h');
    await writePage({ enabled: false }); await refresh(); await heading('Status page not found');
    assert.equal(await page.eval(`document.querySelectorAll('.pub-obs,.pub-list,.pub-embed').length`), 0);
    await writePage({ enabled: true });
  });
  await step('Public observed: editor opt-out withdraws history and the uptime badge', async () => {
    await nav(`#/status-pages/${saved.id}/edit`); await heading(`Edit ${title}`);
    await choose('24 h history'); await click('Save page'); await heading('Status pages'); saved = await currentPage();
    assert.notEqual(saved.groups[0].monitors[0].showAvailability, true);
    const response = await fetch(badge, { signal: AbortSignal.timeout(3000) }); assert.equal(response.status, 404);
    await nav(publicHash(A)); await heading(title);
    assert.equal(await page.eval(`document.querySelectorAll('.pub-obs').length`), 0);
  });
  await nav('#/overview'); await heading('Overview');
}
