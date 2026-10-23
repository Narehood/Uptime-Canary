import assert from 'node:assert/strict';
import { stat } from 'node:fs/promises';
import test from 'node:test';
import type { AccountView, ApiError, Monitor, MonitorInput, Overview } from '../../../packages/shared/src/index.js';
import { Cluster, QUIET_MS, type Alert, type ApiResponse, type NodeProcess } from '../fixtures/cluster.js';

function monitorInput(cluster: Cluster): MonitorInput {
  return {
    name: 'Independent HTTP HA proof', type: 'http', target: cluster.targetUrl,
    intervalMs: 1_000, timeoutMs: 500, nodeIds: cluster.nodes.map((node) => node.id),
    enabled: true, expectedStatusCodes: [200], webhookUrl: cluster.webhookUrl, repeatAfterMs: 0,
  };
}

function denied(response: ApiResponse<unknown>, roleForbidden = false): void {
  if (roleForbidden) assert.equal(response.status, 403, 'viewer mutations must be forbidden');
  else assert.ok([401, 403].includes(response.status), `credentials must be rejected; got ${response.status}`);
  assert.equal(typeof (response.body as ApiError)?.error, 'string', 'errors must contain a JSON error message');
}

async function topology(cluster: Cluster, active = cluster.nodes, groups = [active.map((node) => node.id)]): Promise<void> {
  await cluster.wait('public overviews reflect the expected peer topology', async () => {
    const overviews = await cluster.overviews(active);
    return overviews.every((overview, index) => cluster.nodes.every((peer) => {
      const observer = active[index]!;
      const state = overview.nodes.find((node) => node.id === peer.id)?.state;
      if (observer.id === peer.id) return state === 'local';
      const connected = groups.some((group) => group.includes(observer.id) && group.includes(peer.id));
      return state === (connected ? 'connected' : 'unreachable');
    }));
  });
}

async function localState(cluster: Cluster, monitor: Monitor, state: 'up' | 'down', since: number, active = cluster.nodes): Promise<void> {
  await cluster.wait(`${active.map((node) => node.id).join(', ')} independently observe ${state}`, async () => {
    const histories = await Promise.all(active.map((node) => cluster.history(node, monitor.id)));
    return histories.every((history, index) => history.some((observation) =>
      observation.nodeId === active[index]!.id && observation.state === state && observation.checkedAt >= since));
  });
}

async function allUp(cluster: Cluster, monitor: Monitor, since: number): Promise<void> {
  await cluster.wait('all nodes have fresh up observations from every assigned node', async () => {
    const overviews = await cluster.overviews();
    return overviews.every((overview) => {
      const view = overview.monitors.find((item) => item.monitor.id === monitor.id);
      return view?.monitor.revision === monitor.revision && !view.configConflict && view.state === 'up' &&
        view.unknownNodes === 0 && monitor.nodeIds.every((id) =>
          view.observations.some((observation) => observation.nodeId === id && observation.state === 'up' && observation.checkedAt >= since));
    });
  });
}

function hasReceipt(overview: Overview, alert: Alert): boolean {
  const receipt = overview.notifications.find((notification) =>
    notification.monitorId === alert.monitorId && notification.kind === alert.kind && notification.nodeId === alert.nodeId &&
    (alert.receiptId ? notification.id === alert.receiptId :
      notification.incidentId === alert.incidentId || notification.id === alert.event.idempotencyKey));
  if (!receipt?.id) return false;
  alert.receiptId = receipt.id;
  return true;
}

async function receipts(cluster: Cluster, alerts: Alert[], nodes = cluster.nodes): Promise<void> {
  await cluster.wait('successful webhook receipts are visible through every connected management API', async () =>
    (await cluster.overviews(nodes)).every((overview) => alerts.every((alert) => hasReceipt(overview, alert))));
}

async function singleTransition(cluster: Cluster, monitor: Monitor, kind: 'down' | 'recovery', active = cluster.nodes): Promise<Alert> {
  const start = cluster.events.length;
  const changedAt = Date.now();
  cluster.targetState = kind === 'down' ? 'down' : 'up';
  await localState(cluster, monitor, cluster.targetState, changedAt, active);
  if (kind === 'recovery') await allUp(cluster, monitor, changedAt);
  await cluster.wait(`one real ${kind} webhook`, () => cluster.alerts(start).some((alert) => alert.kind === kind));
  const alerts = cluster.alerts(start);
  assert.equal(alerts.length, 1, 'ordinary connected operation must deduplicate webhook delivery');
  const alert = alerts[0]!;
  assert.equal(alert.kind, kind);
  assert.equal(alert.monitorId, monitor.id);
  assert.ok(active.some((node) => node.id === alert.nodeId), 'an active runtime must have sent the webhook');
  await receipts(cluster, [alert], active);
  await cluster.quiet(`connected ${kind} deduplication over ${QUIET_MS}ms`, start + 1);
  return alert;
}

async function credentials(cluster: Cluster, input: MonitorInput): Promise<{ token: string; node: NodeProcess }> {
  const node = cluster.nodes[0]!;
  denied(await cluster.api(node, '/api/overview', { token: null }));
  denied(await cluster.api(node, '/api/overview', { token: cluster.secret() }));
  denied(await cluster.api(node, '/api/overview', { token: cluster.clusterToken }));
  denied(await cluster.api(node, '/api/monitors', { token: cluster.clusterToken, method: 'POST', body: input }));

  const token = cluster.secret();
  const account = await cluster.api<AccountView>(node, '/api/accounts', {
    method: 'POST', body: { name: 'proof-viewer', role: 'viewer', token },
  });
  assert.equal(account.status, 201);
  assert.equal(account.body.role, 'viewer');
  const accounts = await cluster.api<AccountView[]>(node, '/api/accounts');
  assert.equal(accounts.status, 200);
  for (const response of [account, accounts]) {
    for (const secret of cluster.secrets) assert.ok(!response.text.includes(secret), 'account responses must not expose tokens');
    assert.ok(!/"[^"\s]*(?:token|hash)[^"\s]*"\s*:/i.test(response.text), 'account responses must not expose token hashes');
  }
  const overview = await cluster.api<Overview>(node, '/api/overview', { token });
  assert.equal(overview.status, 200);
  assert.equal(overview.body.user.role, 'viewer');
  denied(await cluster.api(node, '/api/accounts', { token }), true);
  denied(await cluster.api(node, '/api/monitors', { token, method: 'POST', body: input }), true);

  // Discover the private endpoint from successful real peer traffic, without
  // importing a runtime module or fabricating a synchronization snapshot.
  await cluster.wait('real peer synchronization uses the cluster credential', () => Boolean(cluster.successfulPeerExchange()));
  const exchange = cluster.successfulPeerExchange()!;
  const destination = cluster.nodes.find((candidate) => candidate.id === exchange.to)!;
  denied(await cluster.api(destination, exchange.path, {
    token: destination.adminToken, method: exchange.method, headers: exchange.requestHeaders,
    ...(['GET', 'HEAD'].includes(exchange.method) ? {} : { body: {} }),
  }));
  denied(await cluster.api(destination, exchange.path, {
    token: null, method: exchange.method, headers: exchange.requestHeaders,
    ...(['GET', 'HEAD'].includes(exchange.method) ? {} : { body: {} }),
  }));
  return { token, node };
}

test('real three-node HA: connected deduplication, later incidents, sender loss, partition delivery, reconciliation and local history', {
  timeout: 300_000,
}, async (t) => {
  const cluster = new Cluster();
  t.after(async () => cluster.cleanup());
  let phase = 'initialize isolated processes and directed peer proxies';
  const stage = (description: string) => { phase = description; t.diagnostic(description); };
  try {
    await cluster.initialize();
    await cluster.startAll();
    await topology(cluster);

    stage('management and peer credentials are separate; viewer can read but cannot create');
    const input = monitorInput(cluster);
    const viewer = await credentials(cluster, input);
    const createdAt = Date.now();
    const monitor = await cluster.createMonitor(input);
    const historyPath = `/api/monitors/${monitor.id}/history?limit=100`;
    denied(await cluster.api(viewer.node, `/api/monitors/${monitor.id}`, {
      token: viewer.token, method: 'PUT', body: { ...input, name: 'viewer edit must fail', revision: monitor.revision },
    }), true);
    denied(await cluster.api(viewer.node, `/api/monitors/${monitor.id}`, {
      token: viewer.token, method: 'DELETE', body: { revision: monitor.revision },
    }), true);
    assert.equal((await cluster.api(viewer.node, '/api/monitors', { token: viewer.token })).status, 200);
    assert.equal((await cluster.api(viewer.node, historyPath, { token: viewer.token })).status, 200);
    await allUp(cluster, monitor, createdAt);
    await cluster.quiet('healthy startup must not send an outage or recovery');
    for (const node of cluster.nodes) assert.ok((await stat(node.db)).size > 0, `${node.id} must persist to its own SQLite file`);

    stage('three connected nodes deduplicate the first outage and its recovery');
    const firstDown = await singleTransition(cluster, monitor, 'down');
    const firstRecovery = await singleTransition(cluster, monitor, 'recovery');
    assert.equal(firstRecovery.incidentId, firstDown.incidentId);
    assert.notEqual(firstRecovery.event.idempotencyKey, firstDown.event.idempotencyKey);

    stage('a distinct later outage gets a new incident and notification');
    const secondDown = await singleTransition(cluster, monitor, 'down');
    assert.notEqual(secondDown.incidentId, firstDown.incidentId, 'receipts for an old outage must not suppress a new one');
    assert.notEqual(secondDown.event.idempotencyKey, firstDown.event.idempotencyKey);
    const secondRecovery = await singleTransition(cluster, monitor, 'recovery');
    assert.equal(secondRecovery.incidentId, secondDown.incidentId);

    stage('kill an observed sender; a remaining node delivers the next outage without a graceful handoff');
    const killed = cluster.nodes.find((node) => node.id === secondDown.nodeId)!;
    const previousHistory = await cluster.history(killed, monitor.id);
    assert.ok(previousHistory.length > 0);
    const persistedObservation = previousHistory[0]!;
    await cluster.stop(killed);
    const survivors = cluster.nodes.filter((node) => node !== killed);
    await topology(cluster, survivors);
    const failoverDown = await singleTransition(cluster, monitor, 'down', survivors);
    assert.notEqual(failoverDown.nodeId, killed.id);
    assert.notEqual(failoverDown.incidentId, secondDown.incidentId);

    stage('restart the sender from its SQLite file, converge receipts, and recover once');
    const beforeRestart = cluster.events.length;
    await cluster.start(killed);
    await topology(cluster);
    await receipts(cluster, [failoverDown]);
    const restartedHistory = await cluster.history(killed, monitor.id);
    assert.ok(restartedHistory.some((observation) => observation.checkedAt === persistedObservation.checkedAt &&
      observation.nodeId === killed.id && observation.state === persistedObservation.state), 'restart must preserve local history');
    await cluster.quiet('restarting an old sender must not replay a delivered outage', beforeRestart);
    const failoverRecovery = await singleTransition(cluster, monitor, 'recovery');
    assert.equal(failoverRecovery.incidentId, failoverDown.incidentId);

    stage('partition peers into a connected pair and singleton; both groups retain target and webhook access (duplicate alerts are allowed)');
    const groups = [[cluster.nodes[0]!.id, cluster.nodes[1]!.id], [cluster.nodes[2]!.id]];
    const partitionedAt = Date.now();
    cluster.partition(groups);
    await topology(cluster, cluster.nodes, groups);
    await localState(cluster, monitor, 'up', partitionedAt);
    const partitionStart = cluster.events.length;
    const partitionOutageAt = Date.now();
    cluster.targetState = 'down';
    await localState(cluster, monitor, 'down', partitionOutageAt);
    await cluster.wait('at least one actual outage webhook from each disconnected group', () => {
      const alerts = cluster.alerts(partitionStart).filter((alert) => alert.monitorId === monitor.id && alert.kind === 'down');
      return groups.every((group) => alerts.some((alert) => group.includes(alert.nodeId)));
    });
    const partitionAlerts = cluster.alerts(partitionStart);
    assert.ok(partitionAlerts.length >= 2, 'availability during partitions permits duplicate alerts across groups');
    for (const alert of partitionAlerts) {
      assert.equal(alert.kind, 'down');
      assert.equal(alert.monitorId, monitor.id);
      assert.ok(cluster.nodes.some((node) => node.id === alert.nodeId));
    }
    for (const group of groups) {
      await receipts(cluster, partitionAlerts.filter((alert) => group.includes(alert.nodeId)),
        cluster.nodes.filter((node) => group.includes(node.id)));
    }
    await cluster.quiet('each partition settles after sending its outage notification');

    const unionReceipts = new Set((await cluster.overviews()).flatMap((overview) => overview.notifications.map((notification) => notification.id)));
    stage('reconnect; receipt and incident views converge without replaying the delivered outage');
    const beforeReconnect = cluster.events.length;
    cluster.reconnect();
    await topology(cluster);
    await cluster.wait('all receipt IDs and incident identities converge after reconnect', async () => {
      const overviews = await cluster.overviews();
      const incidentViews = overviews.map((overview) => JSON.stringify(overview.incidents
        .filter((incident) => incident.monitorId === monitor.id)
        .map((incident) => ({ id: incident.id, resolved: incident.resolvedAt !== null }))
        .sort((a, b) => a.id.localeCompare(b.id))));
      return overviews.every((overview) => [...unionReceipts].every((id) => overview.notifications.some((notification) => notification.id === id))) &&
        incidentViews.every((view) => view === incidentViews[0]);
    });
    await receipts(cluster, partitionAlerts);
    await cluster.quiet('healing a settled partition must not cause an immediate replay storm', beforeReconnect);

    stage('post-partition recovery settles and all three history endpoints retain only local observations');
    const recoveryStart = cluster.events.length;
    const recoveredAt = Date.now();
    cluster.targetState = 'up';
    await allUp(cluster, monitor, recoveredAt);
    await cluster.wait('post-partition incidents resolve and successful recovery receipts converge', async () => {
      const alerts = cluster.alerts(recoveryStart);
      if (!alerts.some((alert) => alert.kind === 'recovery')) return false;
      return (await cluster.overviews()).every((overview) =>
        alerts.every((alert) => hasReceipt(overview, alert)) && overview.incidents
          .filter((incident) => incident.monitorId === monitor.id)
          .every((incident) => incident.resolvedAt !== null && (incident.notifiedAt === null || incident.recoveryNotifiedAt !== null)));
    });
    const recoveries = cluster.alerts(recoveryStart);
    const partitionIncidentIds = new Set(partitionAlerts.map((alert) => alert.incidentId));
    for (const alert of recoveries) {
      assert.equal(alert.kind, 'recovery');
      assert.equal(alert.monitorId, monitor.id);
      assert.ok(partitionIncidentIds.has(alert.incidentId), 'recovery must refer to an outage that actually notified');
    }
    assert.equal(new Set(recoveries.map((alert) => alert.incidentId)).size, recoveries.length,
      'after healing, each incident recovery is deduplicated');
    await cluster.quiet('post-partition recovery has no repeated delivery');

    for (const node of cluster.nodes) {
      const history = await cluster.history(node, monitor.id);
      assert.ok(history.length > 1 && history.length <= 100, 'history obeys the requested bound');
      assert.ok(history.every((observation) => observation.monitorId === monitor.id && observation.nodeId === node.id),
        `${node.id} history must not contain replicated observations from another node`);
      assert.ok(history.some((observation) => observation.state === 'up'));
      assert.ok(history.some((observation) => observation.state === 'down'));
      for (let index = 1; index < history.length; index++) {
        assert.ok(history[index - 1]!.checkedAt >= history[index]!.checkedAt, 'history must be newest first');
      }
      const view = (await cluster.overview(node)).monitors.find((item) => item.monitor.id === monitor.id)!;
      assert.ok(view.observations.some((observation) => observation.nodeId !== node.id),
        'current peer observations synchronize even though historical samples do not');
    }
    t.diagnostic(`Observed ${cluster.events.length} actual webhook requests; partition duplicates are intentional. Quiescence windows: ${QUIET_MS}ms.`);
  } catch (error) {
    // Do not attach an unredacted cause: node:test prints assertion values and causes.
    throw new Error(cluster.redact(`Proof phase: ${phase}\n${error instanceof Error ? error.stack ?? error.message : String(error)}\n${cluster.diagnostics()}`));
  }
});
