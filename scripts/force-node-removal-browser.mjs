// Real React controls and real Go cleanup, using only the harness's private
// dashboard, disabled monitors and loopback nodes. No provider sends.
import assert from 'node:assert/strict';

export async function reviewForceNodeRemoval({ fixture, A, B, page, step, until, fill, click, ready, owner, registrations }) {
  const q = JSON.stringify;
  const node = registrations.find(row => row.nodeId === A.id);
  const survivor = registrations.find(row => row.nodeId === B.id);
  const api = (path, method = 'GET', body, registration) => fixture.api(path, method, body, owner, registration);
  const shared = 'cd190001fe274968a5fe000000000001';
  const solo = 'cd190001fe274968a5fe000000000002';
  const stalled = 'cd190001fe274968a5fe000000000003';
  let physical;
  const longName = 'R'.repeat(128), renamed = 'Retire private runner';
  const deletes = () => fixture.stats().paths.filter(row => row.method === 'DELETE' && row.path === `/api/dashboard/nodes/${node.id}`).length;

  await step('Node removal: real shared and last-runner assignments are ready before confirmation', async () => {
    assert.ok(node && survivor);
    assert.equal((await api(`/api/dashboard/nodes/${node.id}`, 'PATCH', { label: longName })).status, 200);
    for (const [id, runners] of [[shared, [node.id, survivor.id]], [solo, [node.id]]]) {
      const created = await api('/api/dashboard/fleet/monitors', 'POST', {
        requestId: id, registrationIds: runners, monitor: {
          name: id === shared ? 'Shared removal fixture' : 'Last-runner removal fixture', type: 'manual', target: 'manual',
          enabled: false, intervalMs: 60000, timeoutMs: 1000, nodeIds: [A.id], notificationIds: [],
        },
      });
      assert.equal(created.status, 202);
    }
    // Real server preflight rejects a missing node-local channel before sending
    // any placement command. Previously node removal waited on this forever.
    const rejected = await api('/api/dashboard/fleet/monitors', 'POST', {
      requestId: stalled, registrationIds: [node.id], monitor: {
        name: 'Never-started blocked monitor', type: 'manual', target: 'manual', enabled: false,
        intervalMs: 60000, timeoutMs: 1000, nodeIds: [A.id], notificationIds: ['missing-removal-fixture-channel'],
      },
    });
    assert.equal(rejected.status, 202);
    const rows = await until(async () => {
      const result = await api('/api/dashboard/fleet/monitors');
      return result.value?.filter(row => [shared, solo].includes(row.id)).length === 2 &&
        result.value.filter(row => [shared, solo].includes(row.id)).every(row => row.state === 'applied') &&
        result.value.some(row => row.id === stalled && row.state === 'blocked' && row.placements.length === 0) && result.value;
    }, 'disabled removal fixtures applied', 45000);
    physical = rows.find(row => row.id === shared).placements.find(p => p.registrationIds.includes(survivor.id)).monitorId;
    return { managedAssignments: 2, unstartedBlockedOperation: 1, sharedMonitorIDRetained: true, externalSends: 0 };
  });

  await step('Node removal: exact name, real focus and narrow long-name layout gate a destructive request', async () => {
    await page.send('Emulation.setDeviceMetricsOverride', { width: 320, height: 844, deviceScaleFactor: 1, mobile: false });
    await page.eval('location.hash="#/nodes"');
    await until(() => page.eval(`document.getElementById(${q('r-' + node.id)})?.textContent===${q(longName)}`), 'updated node label in list', 30000);
    await ready();
    await page.eval(`(() => { const card=document.getElementById(${q('r-' + node.id)}).closest('article');const button=[...card.querySelectorAll('button')].find(e=>e.textContent.trim()==='Remove');if(!button)throw Error('Remove missing');button.click(); })()`);
    await until(() => page.eval('!!document.querySelector("dialog[open] input")'), 'node confirmation dialog');
    assert.ok(await page.eval('document.activeElement===document.querySelector("dialog[open] input")'), 'name field owns entry focus');
    assert.ok(await page.eval('document.querySelector("dialog[open] button[type=submit]").disabled'));
    const before = deletes();
    await fill(`Type “${longName}” to confirm`, 'Wrong name', 'dialog[open]');
    await page.eval('document.querySelector("dialog[open] form").requestSubmit()');
    assert.equal(deletes(), before, 'a wrong name sends nothing');
    const widths = await page.eval('({page:document.documentElement.clientWidth,pageScroll:document.documentElement.scrollWidth,dialog:document.querySelector("dialog[open]").clientWidth,dialogScroll:document.querySelector("dialog[open]").scrollWidth})');
    assert.ok(widths.pageScroll <= widths.page + 1 && widths.dialogScroll <= widths.dialog + 1, 'long name must not create horizontal overflow');
    return { initialFocus: 'name', invalidSubmissions: 0, viewport: 320, widths };
  });

  await step('Node removal: a polled rename invalidates old text; accepted cleanup does not wait for the browser', async () => {
    assert.equal((await api(`/api/dashboard/nodes/${node.id}`, 'PATCH', { label: renamed })).status, 200);
    await until(() => page.eval(`document.querySelector('dialog[open]')?.innerText.includes(${q('Type “' + renamed + '” to confirm')})`), 'polled rename updates confirmation', 30000);
    assert.ok(await page.eval('document.querySelector("dialog[open] button[type=submit]").disabled'));
    await fill(`Type “${renamed}” to confirm`, renamed, 'dialog[open]');
    const before = deletes(), held = fixture.hold('DELETE', `/api/dashboard/nodes/${node.id}`);
    try {
      await click('Remove node', 'dialog[open]');
      await until(() => held.answered() !== null, 'server accepted durable cleanup');
      assert.equal(held.answered().status, 202);
      assert.ok(await page.eval('document.querySelector("dialog[open] button[type=submit]").disabled'));
      await page.eval('document.querySelector("dialog[open] form").requestSubmit()');
      assert.equal(deletes(), before + 1, 'busy dialog does not submit twice');
    } finally { held.release(); }
    await until(() => page.eval('!document.querySelector("dialog[open]")'), 'accepted dialog closes');
    return { renameInvalidatesConfirmation: true, acceptedStatus: 202, destructiveRequests: 1 };
  });

  await step('Node removal: cleanup retains the shared physical monitor and retires the last-runner monitor', async () => {
    await until(async () => !(await api('/api/dashboard/nodes')).value.some(row => row.id === node.id), 'background cleanup and revocation finish', 90000);
    await until(() => page.eval(`!document.getElementById(${q('r-' + node.id)})`), 'completed removal disappears from the Nodes page', 30000);
    const rows = (await api('/api/dashboard/fleet/monitors')).value;
    const kept = rows.find(row => row.id === shared), retired = rows.find(row => row.id === solo);
    assert.equal(kept.state, 'applied');
    assert.deepEqual(kept.registrationIds, [survivor.id]);
    assert.equal(retired.state, 'removed');
    assert.equal(rows.find(row => row.id === stalled).state, 'removed', 'blocked never-started operation cannot deadlock node removal');
    const monitors = await api('/api/monitors', 'GET', undefined, survivor.id);
    assert.equal(monitors.status, 200);
    assert.ok(monitors.value.some(row => row.monitor.id === physical && row.monitor.nodeIds.includes(B.id)));
    assert.ok(kept.placements.every(p => p.state === 'removed' || p.registrationId !== node.id));
    const accounts = await (await fetch(A.url + '/api/accounts', { headers: { Authorization: `Bearer ${A.token}` } })).json();
    assert.equal(accounts.filter(account => account.id?.startsWith('dashboard-')).length, 0, 'revocation removed this dashboard delegation');
    await page.send('Emulation.clearDeviceMetricsOverride');
    return { sharedMonitorKept: true, lastRunnerRetired: true, oldManagementAnchorGone: true, revocationConfirmed: true, blockedOperationCleared: true };
  });

  await step('Node removal: Remove now clears an already-pending offline node and retains its shared monitor', async () => {
    const id = 'review-offline-removal';
    const remote = await fixture.startNode(id, { CANARY_NODE_ID: id, CANARY_NODE_NAME: 'Offline removal fixture', CANARY_ADMIN_TOKEN: A.token, CANARY_CLUSTER_TOKEN: 'private-offline-removal-cluster-token-2026' }, await fixture.freePort());
    try {
      const verified = await api('/api/dashboard/verify', 'POST', { label: 'Offline removal fixture', url: remote.url, token: A.token });
      assert.equal(verified.status, 200);
      const enrolled = await api('/api/dashboard/nodes', 'POST', { ticket: verified.value.ticket });
      assert.equal(enrolled.status, 201);
      const entry = enrolled.value;
      const sharedId = 'cd190001fe274968a5fe000000000004', soloId = 'cd190001fe274968a5fe000000000005';
      for (const [requestId, runners] of [[sharedId, [entry.id, survivor.id]], [soloId, [entry.id]]]) {
        assert.equal((await api('/api/dashboard/fleet/monitors', 'POST', { requestId, registrationIds: runners, monitor: { name: requestId === sharedId ? 'Offline shared fixture' : 'Offline solo fixture', type: 'manual', target: 'manual', enabled: false, intervalMs: 60000, timeoutMs: 1000, nodeIds: [id], notificationIds: [] } })).status, 202);
      }
      await until(async () => (await api('/api/dashboard/fleet/monitors')).value.filter(row => [sharedId, soloId].includes(row.id)).every(row => row.state === 'applied'), 'offline fixtures applied', 60000);
      const original = (await api('/api/dashboard/fleet/monitors')).value.find(row => row.id === sharedId).placements.find(p => p.registrationId === survivor.id).monitorId;
      await remote.stop();
      assert.equal((await api(`/api/dashboard/nodes/${entry.id}`, 'DELETE', { force: true, confirmation: entry.label })).status, 202);
      await until(async () => (await api('/api/dashboard/nodes')).value.some(row => row.id === entry.id && row.state === 'removing'), 'existing removal is pending');
      await page.eval('location.hash="#/nodes"'); await ready();
      // This fixture enrolled through its direct API, outside the browser's
      // mutation callbacks. Refresh the catalogue through a real visible control.
      await click('Check now', `article[aria-labelledby=${q('r-' + survivor.id)}]`);
      await until(() => page.eval(`!!document.getElementById(${q('r-' + entry.id)})`), 'offline card loaded');
      const card = `article[aria-labelledby=${q('r-' + entry.id)}]`;
      await click('Remove now', card);
      await until(() => page.eval('!!document.querySelector("dialog[open] input")'), 'pending-node confirmation opens');
      assert.match(await page.eval('document.querySelector("dialog[open]").innerText'), /unreachable node may keep running its own checks/iu);
      await fill(`Type “${entry.label}” to confirm`, 'wrong name', 'dialog[open]');
      assert.ok(await page.eval('document.querySelector("dialog[open] button[type=submit]").disabled'));
      await fill(`Type “${entry.label}” to confirm`, entry.label, 'dialog[open]');
      await click('Remove now', 'dialog[open]');
      await until(() => page.eval('!document.querySelector("dialog[open]")'), 'local removal accepted');
      await until(async () => !(await api('/api/dashboard/nodes')).value.some(row => row.id === entry.id), 'offline registry entry removed locally', 45000);
      await until(() => page.eval(`!document.getElementById(${q('r-' + entry.id)})`), 'offline card disappears', 30000);
      await until(async () => (await api('/api/dashboard/fleet/monitors')).value.find(row => row.id === sharedId)?.state === 'applied', 'offline shared monitor retained', 45000);
      const rows = (await api('/api/dashboard/fleet/monitors')).value;
      assert.equal(rows.find(row => row.id === soloId).state, 'removed');
      assert.deepEqual(rows.find(row => row.id === sharedId).registrationIds, [survivor.id]);
      assert.ok(rows.find(row => row.id === sharedId).placements.some(p => p.monitorId === original));
      return { alreadyPending: true, stoppedNode: true, exactNameRequired: true, localEntryRemoved: true, survivorPhysicalMonitorKept: true, lastRunnerRetiredLocally: true };
    } finally { if (remote.running()) await remote.stop(); }
  });
}
