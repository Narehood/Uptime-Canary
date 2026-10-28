import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import test from 'node:test';
import type { ApiError, Group, GroupInput, Monitor, MonitorInput, Overview } from '../../../packages/shared/src/index.js';
import { CHECK_INTERVAL_MS, Cluster, QUIET_MS, type Alert, type ApiResponse, type HttpTarget } from '../fixtures/cluster.js';

function input(cluster: Cluster, target: HttpTarget, name: string, groupId?: string): MonitorInput {
  return {
    name, type: 'http', target: target.url, internalOnly: true, intervalMs: CHECK_INTERVAL_MS, timeoutMs: 500,
    nodeIds: cluster.nodes.map((node) => node.id), enabled: true, expectedStatusCodes: [200],
    webhookUrl: cluster.webhookUrl, repeatAfterMs: 0, ...(groupId ? { groupId } : {}),
  };
}

function apiError(response: ApiResponse<unknown>, status: number): void {
  assert.equal(response.status, status);
  assert.equal(typeof (response.body as ApiError)?.error, 'string');
}

function groupIn(overview: Overview, id: string) {
  // Optional in the general DTO, required here because a user created groups.
  assert.ok(Array.isArray(overview.groups), 'created groups must appear in overview');
  const view = overview.groups.find((item) => item.group.id === id);
  assert.ok(view, 'the configured group must be visible by its generated ID');
  return view;
}

async function replicatedGroup(cluster: Cluster, group: Group): Promise<void> {
  await cluster.wait('group configuration and revision replicate through real peer transport', async () => {
    const lists = await Promise.all(cluster.nodes.map((node) => cluster.groups(node)));
    const overviews = await cluster.overviews();
    return lists.every((list, index) => {
      const views = [list.find((item) => item.group.id === group.id), groupIn(overviews[index]!, group.id)];
      return views.every((view) => view && !view.configConflict && view.group.revision === group.revision &&
        view.group.name === group.name && view.group.graceMs === group.graceMs &&
        view.group.outageMonitorId === group.outageMonitorId &&
        JSON.stringify([...view.group.nodeIds].sort()) === JSON.stringify([...group.nodeIds].sort()));
    });
  });
}

async function observed(cluster: Cluster, monitors: Monitor[], state: 'up' | 'down', since: number): Promise<void> {
  await cluster.wait(`both real processes have fresh ${state} evidence for ${monitors.map((monitor) => monitor.name).join(', ')}`, async () =>
    (await cluster.overviews()).every((overview) => monitors.every((monitor) => {
      const view = overview.monitors.find((item) => item.monitor.id === monitor.id);
      return view?.state === state && !view.configConflict && view.monitor.revision === monitor.revision &&
        view.monitor.groupId === monitor.groupId && view.unknownNodes === 0 && monitor.nodeIds.every((id) =>
          view.observations.some((observation) => observation.nodeId === id && observation.state === state && observation.checkedAt >= since));
    })));
}

async function deliveries(cluster: Cluster, since: number, expected: { monitor: Monitor; kind: Alert['kind'] }[]): Promise<Alert[]> {
  await cluster.wait('the expected ordinary webhook notifications arrive', () => expected.every(({ monitor, kind }) =>
    cluster.alerts(since).some((alert) => alert.monitorId === monitor.id && alert.kind === kind)));
  const alerts = cluster.alerts(since);
  assert.equal(alerts.length, expected.length, 'connected processes must send exactly the expected notifications');
  for (const { monitor, kind } of expected) {
    assert.equal(alerts.filter((alert) => alert.monitorId === monitor.id && alert.kind === kind).length, 1);
  }
  for (const alert of alerts) {
    assert.ok(cluster.nodes.some((node) => node.id === alert.nodeId), 'a real configured process must send each webhook');
  }
  await cluster.wait('both processes expose successful notification receipts', async () =>
    (await cluster.overviews()).every((overview) => alerts.every((alert) => overview.notifications.some((receipt) =>
      Boolean(receipt.id) && receipt.monitorId === alert.monitorId && receipt.incidentId === alert.incidentId &&
      receipt.kind === alert.kind && receipt.nodeId === alert.nodeId))));
  return alerts;
}

async function suppressed(cluster: Cluster, group: Group, parent: Monitor, children: Monitor[]): Promise<void> {
  await cluster.wait('group, dependent monitors and incidents expose actual suppression and its reason', async () =>
    (await cluster.overviews()).every((overview) => {
      const groupView = groupIn(overview, group.id);
      const parentView = overview.monitors.find((item) => item.monitor.id === parent.id);
      return groupView.state === 'down' && groupView.suppressionActive && groupView.reason.trim().length > 0 &&
        groupView.reachableNodes === group.nodeIds.length && groupView.totalNodes === group.nodeIds.length &&
        parentView?.state === 'down' && !parentView.suppressedByGroupId && children.every((child) => {
          const view = overview.monitors.find((item) => item.monitor.id === child.id);
          const incident = overview.incidents.find((item) => item.monitorId === child.id && item.resolvedAt === null);
          return view?.state === 'down' && view.suppressedByGroupId === group.id && Boolean(view.suppressionReason?.trim()) &&
            incident?.suppressedByGroupId === group.id && Boolean(incident.suppressionReason?.trim()) &&
            incident.notifiedAt === null && incident.recoveryNotifiedAt === null;
        });
    }));
}

test('user-defined groups: real shared outage suppression, independent alerts, recovery release and versioned configuration', {
  timeout: 240_000,
}, async (t) => {
  const cluster = new Cluster();
  t.after(async () => cluster.cleanup());
  let phase = 'initialize two compiled Go processes and independent real HTTP targets';
  const stage = (description: string) => { phase = description; t.diagnostic(description); };
  try {
    // Neither node identity nor group name corresponds to a built-in site/provider topology.
    const tag = randomUUID();
    await cluster.initialize([`relay-${randomUUID()}`, `relay-${randomUUID()}`]);
    await cluster.startAll();
    const node = cluster.nodes[0]!;
    const [rootTarget, healedTarget, remainingTarget, independentTarget] = await Promise.all([
      cluster.createTarget(), cluster.createTarget(), cluster.createTarget(), cluster.createTarget(),
    ]);
    const createdAt = Date.now();
    const rootInput = input(cluster, rootTarget!, `User-selected dependency ${tag}`);
    let root = await cluster.createMonitor(rootInput);
    const groupInput: GroupInput = {
      name: `Workshop / ${tag} / réseau`, nodeIds: cluster.nodes.map((peer) => peer.id),
      outageMonitorId: root.id, graceMs: 6_000,
    };
    let group = await cluster.createGroup(groupInput);
    const otherGroup = await cluster.createGroup({ name: `Unrelated domain ${randomUUID()}`, nodeIds: group.nodeIds, graceMs: 0 });
    assert.notEqual(otherGroup.id, group.id);
    await replicatedGroup(cluster, group);
    await replicatedGroup(cluster, otherGroup);

    // Exercise the important self-dependency exception: the ordinary parent
    // monitor belongs to the group whose other alerts it suppresses.
    const rootUpdate = await cluster.api<Monitor>(node, `/api/monitors/${root.id}`, {
      method: 'PUT', body: { ...rootInput, groupId: group.id, revision: root.revision },
    });
    assert.equal(rootUpdate.status, 200, cluster.redact(rootUpdate.text));
    assert.equal(rootUpdate.body.id, root.id);
    assert.notEqual(rootUpdate.body.revision, root.revision);
    root = rootUpdate.body;
    const healed = await cluster.createMonitor(input(cluster, healedTarget!, `Short failure ${tag}`, group.id));
    const remaining = await cluster.createMonitor(input(cluster, remainingTarget!, `Persistent failure ${tag}`, group.id));
    const independent = await cluster.createMonitor(input(cluster, independentTarget!, `Independent check ${tag}`, otherGroup.id));
    const monitors = [root, healed, remaining, independent];
    await observed(cluster, monitors, 'up', createdAt);

    stage('group APIs require management credentials and enforce viewer permissions');
    apiError(await cluster.api(node, '/api/groups', { token: null }), 401);
    const wrongCredential = await cluster.api(node, '/api/groups', { token: cluster.clusterToken });
    assert.ok([401, 403].includes(wrongCredential.status));
    assert.equal(typeof (wrongCredential.body as ApiError)?.error, 'string');
    const viewer = cluster.secret();
    const account = await cluster.api(node, '/api/accounts', {
      method: 'POST', body: { name: `Group reader ${tag}`, role: 'viewer', token: viewer },
    });
    assert.equal(account.status, 201);
    assert.ok(!account.text.includes(viewer), 'account creation must not echo private credentials');
    assert.equal((await cluster.api(node, '/api/groups', { token: viewer })).status, 200);
    assert.equal((await cluster.api(node, `/api/groups/${group.id}/conflicts`, { token: viewer })).status, 200);
    for (const mutation of [
      { path: '/api/groups', method: 'POST', body: groupInput },
      { path: `/api/groups/${group.id}`, method: 'PUT', body: { ...groupInput, revision: group.revision } },
      { path: `/api/groups/${group.id}`, method: 'DELETE', body: { revision: group.revision } },
      { path: `/api/groups/${group.id}/resolve`, method: 'POST', body: { ...groupInput, revisions: [group.revision] } },
    ]) {
      apiError(await cluster.api(node, mutation.path, { token: viewer, method: mutation.method, body: mutation.body }), 403);
    }
    await cluster.quiet('healthy grouped monitors must not notify', 0);

    stage('one ordinary root outage alert suppresses both dependent alerts beyond the configured grace');
    const outageStart = cluster.events.length;
    const failedAt = Date.now();
    healedTarget!.state = 'down';
    remainingTarget!.state = 'down';
    // Establish child evidence first, then fail the parent. The configurable
    // grace must hold notifications while its independent evidence catches up.
    await observed(cluster, [healed, remaining], 'down', failedAt);
    assert.equal(cluster.events.length, outageStart, 'dependent alerts must wait for the configured grace');
    const rootFailedAt = Date.now();
    rootTarget!.state = 'down';
    await observed(cluster, [root], 'down', rootFailedAt);
    await suppressed(cluster, group, root, [healed, remaining]);
    const [rootDown] = await deliveries(cluster, outageStart, [{ monitor: root, kind: 'down' }]);
    const suppressionWindow = Math.max(QUIET_MS, group.graceMs + CHECK_INTERVAL_MS * 2);
    await cluster.quiet('only the root sends while the shared failure remains fresh', outageStart + 1, suppressionWindow);
    for (const peer of cluster.nodes) {
      for (const child of [healed, remaining]) {
        const history = await cluster.history(peer, child.id);
        assert.ok(history.some((sample) => sample.state === 'down' && sample.checkedAt >= failedAt),
          'notification suppression must preserve the actual down measurements');
        assert.ok(history.every((sample) => sample.monitorId === child.id && sample.nodeId === peer.id),
          'suppressed measurements remain local history, not replicated samples');
      }
    }
    const beforeHealing = await cluster.overview(node);
    const healedIncident = beforeHealing.incidents.find((incident) => incident.monitorId === healed.id && incident.resolvedAt === null)!;
    const remainingIncident = beforeHealing.incidents.find((incident) => incident.monitorId === remaining.id && incident.resolvedAt === null)!;
    assert.ok(healedIncident?.id && remainingIncident?.id);

    stage('an unrelated user group still sends while the shared dependency remains down');
    const independentStart = cluster.events.length;
    const independentFailedAt = Date.now();
    independentTarget!.state = 'down';
    await observed(cluster, [independent], 'down', independentFailedAt);
    const [independentDown] = await deliveries(cluster, independentStart, [{ monitor: independent, kind: 'down' }]);
    await suppressed(cluster, group, root, [healed, remaining]);
    for (const overview of await cluster.overviews()) {
      assert.ok(!overview.monitors.find((view) => view.monitor.id === independent.id)!.suppressedByGroupId);
      assert.equal(groupIn(overview, otherGroup.id).suppressionActive, false);
    }
    await cluster.quiet('separate-group delivery does not release suppressed children', independentStart + 1);

    stage('a dependent recovers entirely under suppression without sending stale outage or recovery');
    const beforeChildHealing = cluster.events.length;
    const childHealedAt = Date.now();
    healedTarget!.state = 'up';
    await observed(cluster, [healed], 'up', childHealedAt);
    await cluster.wait('the suppressed child incident resolves locally without notification timestamps', async () =>
      (await cluster.overviews()).every((overview) => overview.incidents.some((incident) =>
        incident.id === healedIncident.id && incident.resolvedAt !== null &&
        incident.notifiedAt === null && incident.recoveryNotifiedAt === null)));
    await suppressed(cluster, group, root, [remaining]);
    await cluster.quiet('healing during suppression must not replay an outage/recovery pair', beforeChildHealing);

    stage('dependency recovery sends one recovery and releases only the still-down child');
    const releaseStart = cluster.events.length;
    const rootHealedAt = Date.now();
    rootTarget!.state = 'up';
    await observed(cluster, [root], 'up', rootHealedAt);
    const released = await deliveries(cluster, releaseStart, [
      { monitor: root, kind: 'recovery' }, { monitor: remaining, kind: 'down' },
    ]);
    assert.equal(released.find((alert) => alert.monitorId === root.id)!.incidentId, rootDown!.incidentId);
    const childDown = released.find((alert) => alert.monitorId === remaining.id)!;
    assert.equal(childDown.incidentId, remainingIncident.id, 'release uses the real incident opened during suppression');
    assert.ok(childDown.event.at >= rootHealedAt, 'the child must not notify before parent recovery');
    await cluster.wait('public suppression clears after dependency recovery', async () =>
      (await cluster.overviews()).every((overview) => !groupIn(overview, group.id).suppressionActive &&
        [root, healed, remaining].every((monitor) => !overview.monitors.find((view) => view.monitor.id === monitor.id)!.suppressedByGroupId)));
    await cluster.quiet('released child and parent recovery deduplicate across both processes', releaseStart + 2, suppressionWindow);
    assert.ok(!cluster.alerts().some((alert) => alert.monitorId === healed.id), 'the fully suppressed incident must never replay');

    stage('released and independent incidents recover normally with their original identities');
    const recoveryStart = cluster.events.length;
    const recoveredAt = Date.now();
    remainingTarget!.state = 'up';
    independentTarget!.state = 'up';
    await observed(cluster, monitors, 'up', recoveredAt);
    const recoveries = await deliveries(cluster, recoveryStart, [
      { monitor: remaining, kind: 'recovery' }, { monitor: independent, kind: 'recovery' },
    ]);
    assert.equal(recoveries.find((alert) => alert.monitorId === remaining.id)!.incidentId, childDown.incidentId);
    assert.equal(recoveries.find((alert) => alert.monitorId === independent.id)!.incidentId, independentDown!.incidentId);
    await cluster.quiet('normal recoveries settle and no healed child incident reappears', recoveryStart + 2);
    assert.ok(!cluster.alerts().some((alert) => alert.monitorId === healed.id));

    stage('concurrent group edits become explicit conflicts, then resolve with all observed heads');
    cluster.partition(cluster.nodes.map((peer) => [peer.id]));
    await cluster.wait('both directed peer transports are partitioned', async () =>
      (await cluster.overviews()).every((overview) => overview.nodes.every((peer) =>
        peer.id === overview.node.id ? peer.state === 'local' : peer.state === 'unreachable')));
    const branches: Group[] = [];
    for (const [index, peer] of cluster.nodes.entries()) {
      const edit = await cluster.api<Group>(peer, `/api/groups/${group.id}`, {
        method: 'PUT', body: { ...groupInput, name: `${groupInput.name} / choice ${index}`, revision: group.revision },
      });
      assert.equal(edit.status, 200, cluster.redact(edit.text));
      assert.equal(edit.body.id, group.id);
      assert.notEqual(edit.body.revision, group.revision);
      branches.push(edit.body);
    }
    assert.notEqual(branches[0]!.revision, branches[1]!.revision);
    cluster.reconnect();
    const heads = branches.map((branch) => branch.revision).sort();
    await cluster.wait('both public APIs retain the concurrent group revisions', async () => {
      for (const peer of cluster.nodes) {
        const view = (await cluster.groups(peer)).find((item) => item.group.id === group.id);
        if (!view?.configConflict || !groupIn(await cluster.overview(peer), group.id).configConflict) return false;
        const conflicts = await cluster.api<Group[]>(peer, `/api/groups/${group.id}/conflicts`);
        if (conflicts.status !== 200 || !Array.isArray(conflicts.body) ||
          JSON.stringify(conflicts.body.map((candidate) => candidate.revision).sort()) !== JSON.stringify(heads)) return false;
      }
      return true;
    });
    apiError(await cluster.api(node, `/api/groups/${group.id}`, {
      method: 'PUT', body: { ...groupInput, revision: branches[0]!.revision },
    }), 409);
    apiError(await cluster.api(node, `/api/groups/${group.id}/resolve`, {
      method: 'POST', body: { ...groupInput, revisions: [branches[0]!.revision] },
    }), 409);
    const resolved = await cluster.api<Group>(node, `/api/groups/${group.id}/resolve`, {
      method: 'POST', body: { ...groupInput, name: `${groupInput.name} / selected`, revisions: heads },
    });
    assert.equal(resolved.status, 200, cluster.redact(resolved.text));
    assert.equal(resolved.body.id, group.id);
    assert.ok(!heads.includes(resolved.body.revision));
    group = resolved.body;
    await replicatedGroup(cluster, group);

    stage('group tombstones replicate without deleting monitors and a missing policy fails open');
    apiError(await cluster.api(node, `/api/groups/${group.id}`, {
      method: 'DELETE', body: { revision: branches[0]!.revision },
    }), 409);
    const removed = await cluster.api(node, `/api/groups/${group.id}`, { method: 'DELETE', body: { revision: group.revision } });
    assert.equal(removed.status, 204);
    await cluster.wait('deleted group disappears on both peers while dependent monitors remain', async () => {
      for (const peer of cluster.nodes) {
        if ((await cluster.groups(peer)).some((view) => view.group.id === group.id)) return false;
        const overview = await cluster.overview(peer);
        if (overview.groups?.some((view) => view.group.id === group.id)) return false;
        if (![root, healed, remaining].every((monitor) => overview.monitors.some((view) =>
          view.monitor.id === monitor.id && view.monitor.groupId === group.id && view.state === 'up'))) return false;
      }
      return true;
    });
    const failOpenStart = cluster.events.length;
    const failOpenAt = Date.now();
    healedTarget!.state = 'down';
    await observed(cluster, [healed], 'down', failOpenAt);
    const [failOpenDown] = await deliveries(cluster, failOpenStart, [{ monitor: healed, kind: 'down' }]);
    assert.notEqual(failOpenDown!.incidentId, healedIncident.id, 'a later real outage gets its own incident');
    await cluster.quiet('missing group policy cannot silently inhibit a new outage', failOpenStart + 1, suppressionWindow);
    const failOpenRecoveryStart = cluster.events.length;
    const failOpenRecoveredAt = Date.now();
    healedTarget!.state = 'up';
    await observed(cluster, [healed], 'up', failOpenRecoveredAt);
    const [failOpenRecovery] = await deliveries(cluster, failOpenRecoveryStart, [{ monitor: healed, kind: 'recovery' }]);
    assert.equal(failOpenRecovery!.incidentId, failOpenDown!.incidentId);
    await cluster.quiet('missing-policy recovery settles without a stale replay', failOpenRecoveryStart + 1);

    for (const target of [rootTarget!, healedTarget!, remainingTarget!, independentTarget!]) {
      assert.ok(target.requests.some((request) => request.state === 'up'));
      assert.ok(target.requests.some((request) => request.state === 'down'), 'every independent listener must receive real failing checks');
    }
    for (const peer of cluster.nodes) {
      for (const monitor of monitors) {
        const history = await cluster.history(peer, monitor.id);
        assert.ok(history.length > 1 && history.length <= 100);
        assert.ok(history.every((sample) => sample.nodeId === peer.id && sample.monitorId === monitor.id));
        assert.ok(history.some((sample) => sample.state === 'down'));
        assert.ok(history.some((sample) => sample.state === 'up'));
        for (let index = 1; index < history.length; index++) {
          assert.ok(history[index - 1]!.checkedAt >= history[index]!.checkedAt);
        }
      }
    }
    assert.equal(new Set(cluster.events.map((event) => event.idempotencyKey)).size, cluster.events.length,
      'distinct successful transitions have distinct idempotency keys');
    t.diagnostic(`Two compiled processes, four independently controlled HTTP listeners, ${cluster.events.length} real webhooks; grace ${group.graceMs}ms, quiescence ${suppressionWindow}ms.`);
  } catch (error) {
    throw new Error(cluster.redact(`Proof phase: ${phase}\n${error instanceof Error ? error.stack ?? error.message : String(error)}\n${cluster.diagnostics()}`));
  }
});
