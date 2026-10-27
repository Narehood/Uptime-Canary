// Root-owned fleet regression. Reuses the existing bounded loopback servers,
// executable, browser and profile. No external destinations or deployments.
import assert from 'node:assert/strict';

export async function reviewFleet({ fixture, A, B, page, step, until, fill, click, text, ready, owner, includeOutbound = false }) {
  const q = JSON.stringify;
  const name = 'Fleet browser same-name service';
  const groupName = 'Fleet browser sites';
  let a, b, source, unrelated, tagA, tagB, definition;
  const api = (path, method = 'GET', body, registration) => fixture.api(path, method, body, owner, registration);
  const nav = async hash => { await page.eval(`location.hash=${q(hash)}`); await ready(); };
  const openRegistration = async node => {
    await nav('#/nodes');
    const card = `document.querySelector(${q(`article[aria-labelledby="r-${node.id}"]`)})`;
    // A selected registration has Viewing/Its overview instead of an Open
    // button. Check that exact registration; never select an arbitrary node.
    await until(() => page.eval(`document.querySelector('main h1')?.textContent==='Nodes'&&!!${card}&&(${card}.classList.contains('ep')||[...${card}.querySelectorAll('button')].some(b=>b.getAttribute('aria-label')===${q('Open ' + node.label)}&&!b.disabled))`), 'registered node selected or available to open', 25000);
    await page.eval(`(() => {const card=${card};if(!card.classList.contains('ep')){const button=[...card.querySelectorAll('button')].find(b=>b.getAttribute('aria-label')===${q('Open ' + node.label)}&&!b.disabled);if(!button)throw Error('expected registration Open missing');button.click()}})()`);
  };
  const refresh = async () => {
    await nav('#/overview');
    await until(() => page.eval('document.querySelector("main h1")?.textContent==="All nodes"'), 'central fleet heading');
    await until(() => page.eval('[...document.querySelectorAll("main button")].some(b=>__norm(b.innerText)==="Refresh"&&!b.disabled)'), 'fleet finished its current read', 25000);
    await click('Refresh', 'main');
    await until(() => page.eval('[...document.querySelectorAll("main button")].some(b=>__norm(b.innerText)==="Refresh"&&!b.disabled)'), 'new fleet read completed', 25000);
  };
  const rows = label => page.eval(`[...document.querySelectorAll('tbody tr')].filter(r=>r.querySelector('.fl-mon-n')?.textContent===${q(label)}).length`);
  const rowAction = async label => {
    // Graphical R2 deliberately includes the monitor name as hidden accessible
    // text. Match the action within this exact logical row, preserving its name.
    const find = `(() => {const row=[...document.querySelectorAll('tbody tr')].find(r=>r.querySelector('.fl-mon-n')?.textContent===${q(definition.monitor.name)});return row?[...row.querySelectorAll('button')].filter(b=>__norm(b.textContent).startsWith(${q(label)})&&!b.disabled):[]})()`;
    await until(() => page.eval(`${find}.length===1`), `${label} on the managed monitor`, 25000);
    await page.eval(`${find}[0].click()`);
  };
  const layout = async label => {
    const originalMode = await page.eval('document.documentElement.dataset.mode');
    try {
      for (const mode of ['light', 'dark']) for (const width of [1280, 728, 390]) {
        await page.send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false });
        await page.eval(`document.documentElement.dataset.mode=${q(mode)};new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))`);
        assert.ok(await page.eval('document.documentElement.scrollWidth<=innerWidth'), `${label} overflow at ${width} in ${mode}`);
      }
    } finally {
      await page.send('Emulation.clearDeviceMetricsOverride');
      await page.eval(`document.documentElement.dataset.mode=${q(originalMode ?? 'light')}`);
    }
  };
  const inputOf = monitor => Object.fromEntries(Object.entries(monitor).filter(([key]) => !['id','revision','revisions','updatedAt','deleted','checkGeneration','pushStartedAt'].includes(key)));
  const readMonitor = async (registration, id) => {
    const response = await api('/api/monitors', 'GET', undefined, registration);
    assert.equal(response.status, 200);
    return response.value.find(v => v.monitor.id === id)?.monitor;
  };
  const waitApplied = async () => {
    definition = await until(async () => {
      const response = await api('/api/dashboard/fleet/monitors');
      const found = response.value?.find(d => d.id === definition?.id || d.source?.monitorId === source?.id);
      if (found?.state === 'blocked' || found?.state === 'unauthorized') throw Error(`Fleet refused: ${found.reason}`);
      return found?.state === 'applied' ? found : false;
    }, 'saved fleet deployment applied', 30000);
    return definition;
  };
  const choose = async (scope, selected) => page.eval(`(() => {
    const field=document.querySelector(${q(scope)});if(!field)throw Error('placement field missing');
    const chosen=${q(selected)};
    for(const box of field.querySelectorAll('input[type=checkbox]'))if(box.checked!==chosen.includes(box.value))box.click();
  })()`);

  await step('Fleet: central inventory reads independent nodes and preserves unrelated equal-name monitors', async () => {
    const registrations = (await api('/api/dashboard/nodes')).value;
    a = registrations.find(n => n.nodeId === A.id); b = registrations.find(n => n.nodeId === B.id);
    assert.ok(a && b);
    const tag = async registration => {
      const response = await api('/api/tags', 'POST', { name: 'Fleet location tag', color: '#336699' }, registration);
      assert.equal(response.status, 201); return response.value;
    };
    tagA = await tag(a.id); tagB = await tag(b.id);
    assert.notEqual(tagA.id, tagB.id, 'same resource name does not imply the same identity');
    const make = async registration => {
      const response = await api('/api/monitors', 'POST', { name, type: 'manual', target: 'manual', intervalMs: 60000, timeoutMs: 1000,
        nodeIds: [registration.nodeId], enabled: false, notificationIds: [], ...(registration.id === a.id ? { tags: [{ tagId: tagA.id, value: 'source-tag-value' }] } : {}) }, registration.id);
      assert.equal(response.status, 201); return response.value;
    };
    source = await make(a); unrelated = await make(b);
    await refresh(); await until(async () => await rows(name) === 2, 'two unrelated equal-name inventory rows', 25000);
    assert.ok(await page.eval(`document.querySelector('main').textContent.includes(${q(a.label)})&&document.querySelector('main').textContent.includes(${q(b.label)})`));
    return { independentNodes: 2, equalNameRows: 2, browserNodeRequests: 0 };
  });

  await step('Fleet: dashboard node groups list the full registry and change no outage dependency group', async () => {
    await nav('#/groups');
    await until(() => page.eval('document.querySelector("main h1")?.textContent==="Node groups"'), 'dashboard node groups heading');
    await click('New node group', 'main .vhead');
    await until(() => page.eval('!!document.querySelector(".fl-editor")'), 'new node group editor');
    await fill('Name', groupName, '.fl-editor');
    const choices = await page.eval('[...document.querySelectorAll(".fl-editor .fl-place input[type=checkbox]")].map(e=>e.value)');
    assert.ok(choices.includes(a.id) && choices.includes(b.id));
    await choose('.fl-editor .fl-place', [a.id, b.id]); await layout('node group editor'); await click('Create node group', '.fl-editor');
    await until(async () => (await api('/api/dashboard/node-groups')).value.some(g => g.name === groupName && g.registrationIds.length === 2), 'group persisted');
    const groupsA = (await api('/api/groups', 'GET', undefined, a.id)).value;
    assert.ok(!groupsA.some(g => g.group.name === groupName), 'dashboard groups are not outage dependency groups');
    // The previous update-notice proof signs in afresh. Dependency navigation
    // is intentionally absent until a monitoring node has been selected.
    await openRegistration(a);
    await nav('#/groups');
    await until(() => page.eval('document.querySelector("main h1")?.textContent==="Node groups"'), 'groups after node selection');
    await click('Outage dependencies', 'main');
    await until(() => page.eval('document.querySelector("main h1")?.textContent==="Outage dependencies"'), 'selected node dependency view');
    await click('New group', 'main .vhead');
    await until(() => page.eval('document.querySelector("main h1")?.textContent==="New group"'), 'native group form');
    await click('Cancel', 'main');
    await until(() => page.eval('document.querySelector("main h1")?.textContent==="Outage dependencies"'), 'native group Cancel returns to the selected node dependencies');
    return { fullRegistry: true, savedMembers: 2, peeringChanged: false };
  });

  await step('Fleet: an existing monitor uses a node group and explicit destination-local tag references', async () => {
    await openRegistration(a);
    await nav(`#/monitors/${source.id}/edit`); await text('Edit monitor');
    await until(() => page.eval('!!document.querySelector(".fl-place")'), 'full-registry monitor placement');
    await choose('.fl-place', []);
    await page.eval(`(() => {const e=[...document.querySelectorAll('.fl-gpick button')].find(e=>e.textContent.includes(${q(groupName)}));if(!e)throw Error('saved group shortcut missing');e.click()})()`);
    assert.deepEqual(new Set(await page.eval('[...document.querySelectorAll(".fl-place input:checked")].map(e=>e.value)')), new Set([a.id, b.id]));
    await until(() => page.eval(`!![...document.querySelectorAll('.fl-ref')].find(c=>c.querySelector('.fl-ref-id')?.textContent.includes(${q(B.id)})&&c.classList.contains('s-ready'))`), 'destination resource IDs loaded');
    const destination = `[...document.querySelectorAll('.fl-ref')].find(c=>c.querySelector('.fl-ref-id')?.textContent.includes(${q(B.id)}))`;
    await page.eval(`${destination}.querySelector('.fl-ref-on input').click()`);
    assert.ok(await page.eval(`${destination}.textContent.includes(${q(tagA.id)})`), 'unknown source tag remains until explicitly removed');
    await page.eval(`(() => {const c=${destination};c.querySelector('button[aria-label^="Remove tag"]').click()})()`);
    await page.eval(`(() => {const c=${destination};const label=[...c.querySelectorAll('.fl-ref-pick label')].find(l=>l.textContent.includes('Fleet location tag'));if(!label)throw Error('destination tag missing');label.querySelector('input').click()})()`);
    await page.eval(`__set(${destination}.querySelector('input[type=text]'),'destination-tag-value')`);
    await layout('per-node reference controls');
    await click('Save changes', 'main');
    await until(async () => {
      const list = (await api('/api/dashboard/fleet/monitors')).value;
      definition = list.find(d => d.source?.monitorId === source.id || d.placements.some(p => p.registrationId === a.id && p.monitorId === source.id));
      return !!definition;
    }, 'logical placement created');
    await waitApplied();
    assert.equal(definition.placements.length, 2); assert.ok(definition.placements.some(p => p.monitorId === source.id), 'source adopted without duplication');
    for (const [registration, tagId] of [[a.id, tagA.id], [b.id, tagB.id]]) {
      const placement = definition.placements.find(p => p.registrationId === registration);
      const monitor = await readMonitor(registration, placement.monitorId);
      assert.equal(monitor.tags[0].tagId, tagId);
      assert.deepEqual(monitor.nodeIds, [registration === a.id ? A.id : B.id]);
    }
    assert.ok((await api('/api/monitors', 'GET', undefined, b.id)).value.some(v => v.monitor.id === unrelated.id));
    await refresh(); await until(async () => await rows(name) === 2, 'one managed row plus unrelated legacy row', 25000);
    return { logicalMonitors: 1, independentPlacements: 2, sourceIdKept: true, destinationReferencesExplicit: true };
  });

  await step('Fleet: Load latest preserves drafts until requested and reloads the complete logical configuration', async () => {
    await nav(`#/fleet-monitors/${definition.id}/edit`); await text('Edit monitor');
    await fill('Name', 'Unsaved fleet draft', 'main');
    const renamed = 'Fleet globally renamed';
    const response = await api(`/api/dashboard/fleet/monitors/${definition.id}`, 'PUT', { revision: definition.revision, monitor: { ...inputOf(definition.monitor), name: renamed }, registrationIds: definition.registrationIds, references: definition.references });
    assert.equal(response.status, 202); await waitApplied();
    await text('Load latest (discards your edits)', 25000);
    assert.equal(await page.eval('__field("Name","main").value'), 'Unsaved fleet draft');
    await click('Load latest (discards your edits)', 'main');
    assert.equal(await page.eval('__field("Name","main").value'), renamed);
    assert.deepEqual(new Set(await page.eval('[...document.querySelectorAll(".fl-place input:checked")].map(e=>e.value)')), new Set([a.id, b.id]));
    await fill('Name', 'Fleet browser managed final', 'main'); await click('Save changes', 'main'); await waitApplied();
    assert.ok(definition.placements.every(p => p.sequence === 3));
    return { draftRetainedUntilExplicitReload: true, logicalTemplateAndTargetsReloaded: true };
  });

  await step('Fleet: direct node edits require explicit current-revision review before replacement', async () => {
    const p = definition.placements.find(p => p.registrationId === b.id);
    const before = await readMonitor(b.id, p.monitorId);
    const changed = await api(`/api/monitors/${p.monitorId}`, 'PUT', { ...inputOf(before), revision: before.revision, name: 'Changed directly on the private node' }, b.id);
    assert.equal(changed.status, 200);
    await refresh(); await rowAction('Review node edits');
    try { await text('Replace node edits:', 5000); }
    catch (error) {
      const evidence = await page.eval('({banners:[...document.querySelectorAll("main .banner")].map(e=>e.textContent),nodes:[...document.querySelectorAll(".fl-node")].map(e=>({text:e.textContent,state:e.className}))})');
      throw Error(`${error.message}; ${JSON.stringify(evidence)}`);
    }
    await click('Keep node edits', 'dialog[open]');
    assert.equal((await readMonitor(b.id, p.monitorId)).revision, changed.value.revision);
    await rowAction('Review node edits'); await click('Replace reviewed node settings', 'dialog[open]');
    await waitApplied();
    assert.equal((await readMonitor(b.id, p.monitorId)).name, definition.monitor.name);
    assert.ok(definition.placements.every(p => p.sequence === 4));
    return { keepEditsDoesNotWrite: true, explicitReplacement: true, physicalIdsKept: true };
  });

  await step('Fleet: summary fits narrow screens and global removal deletes only owned placements', async () => {
    await refresh(); await until(async () => await rows(definition.monitor.name) === 1, 'single logical monitor row');
    await layout('central fleet summary');
    await rowAction('Remove everywhere'); await click('Confirm removal', 'dialog[open]');
    await until(async () => (await api('/api/dashboard/fleet/monitors')).value.find(d => d.id === definition.id)?.state === 'removed', 'every placement retired', 30000);
    for (const p of definition.placements) assert.equal(await readMonitor(p.registrationId, p.monitorId), undefined);
    assert.ok(await readMonitor(b.id, unrelated.id), 'unrelated monitor remains');
    return { widths: [1280, 728, 390], deletionAcknowledged: true, unrelatedPreserved: true };
  });
  if (includeOutbound) await step('Fleet: a node-initiated connection applies and retires the same durable placement without an inbound URL', async () => {
    const node = (await api('/api/dashboard/nodes')).value.find(n => n.connection === 'outbound' && n.nodeId === 'review-paired');
    assert.ok(node, 'the real paired process from onboarding remains registered');
    assert.equal(node.url, undefined);
    const before = (await api('/api/monitors', 'GET', undefined, node.id)).value.map(v => v.monitor.id).sort();
    const requestId = 'a09fb13768394f10b1c2d3e4f5061728';
    const created = await api('/api/dashboard/fleet/monitors', 'POST', { requestId, registrationIds: [node.id], monitor: {
      name: 'Outbound fleet placement', type: 'manual', target: 'manual', nodeIds: [node.nodeId], enabled: false,
      intervalMs: 60000, timeoutMs: 1000, notificationIds: [] } });
    assert.equal(created.status, 202);
    const deployed = await until(async () => (await api('/api/dashboard/fleet/monitors')).value.find(d => d.id === requestId && d.state === 'applied'), 'outbound fleet commands applied', 30000);
    assert.equal(deployed.placements.length, 1);
    const placement = deployed.placements[0];
    const monitor = await readMonitor(node.id, placement.monitorId);
    assert.deepEqual(monitor.nodeIds, [node.nodeId]);
    assert.equal((await api(`/api/dashboard/fleet/monitors/${requestId}`, 'DELETE', { revision: deployed.revision })).status, 202);
    await until(async () => (await api('/api/dashboard/fleet/monitors')).value.find(d => d.id === requestId)?.state === 'removed', 'outbound deletion confirmed', 30000);
    assert.deepEqual((await api('/api/monitors', 'GET', undefined, node.id)).value.map(v => v.monitor.id).sort(), before);
    return { outbound: true, inboundURL: false, appliedAndRetired: true, unrelatedMonitorsKept: true };
  });
}
