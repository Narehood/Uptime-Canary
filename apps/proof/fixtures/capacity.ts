import assert from 'node:assert/strict';
import type { NotificationView } from '../../../packages/shared/src/index.js';
import type { DeliveryAttempt } from './notifications.js';

export interface CapacityRequest {
  route: string; monitor: string; source: string; sender: string; kind: string; key: string; accepted: boolean;
}
export interface DeliverySnapshot {
  nodeId: string; attempts: DeliveryAttempt[]; notifications: NotificationView[];
}
interface Obligation {
  nodeId: string; id: string; monitorId: string; sourceIncidentId: string; channelId: string; kind: string; destinationId?: string;
}

/** Keep every observed local obligation, including requests racing a restart. */
export class CapacityObligations {
  private readonly retained = new Map<string, Obligation>();
  constructor(private readonly channels: ReadonlyMap<string, string>) {}

  private retain(value: Obligation): void {
    const key = `${value.nodeId}/${value.id}`;
    const prior = this.retained.get(key);
    if (prior) {
      assert.equal(value.monitorId, prior.monitorId, 'delivery identity retains its monitor');
      assert.equal(value.channelId, prior.channelId, 'delivery identity retains its channel');
      assert.equal(value.kind, prior.kind, 'delivery identity retains its event');
      assert.equal(value.sourceIncidentId, prior.sourceIncidentId, 'delivery identity retains its original source');
      if (prior.destinationId && value.destinationId) {
        assert.equal(value.destinationId, prior.destinationId, 'delivery identity retains its destination');
      }
    }
    this.retained.set(key, { ...prior, ...value, destinationId: value.destinationId ?? prior?.destinationId });
  }

  remember(nodeId: string, attempts: DeliveryAttempt[]): void {
    // The public endpoint has no cursor. A full page cannot prove completeness.
    assert.ok(attempts.length < 1000, 'capacity delivery page must be untruncated');
    for (const attempt of attempts) {
      if (!attempt.channelId || ![...this.channels.values()].includes(attempt.channelId) ||
          !['down', 'recovery'].includes(attempt.kind)) continue;
      assert.ok(attempt.monitorId && attempt.destinationId && attempt.sourceIncidentId,
        'delivery view exposes original source and destination provenance');
      this.retain({ nodeId, id: attempt.id, monitorId: attempt.monitorId, channelId: attempt.channelId,
        sourceIncidentId: attempt.sourceIncidentId, destinationId: attempt.destinationId, kind: attempt.kind });
    }
  }

  inspect(snapshots: DeliverySnapshot[], requests: CapacityRequest[], route?: string, kind?: string) {
    assert.equal(snapshots.length, 2, 'both observing nodes supply delivery evidence');
    assert.equal(new Set(snapshots.map(value => value.nodeId)).size, 2, 'independent node evidence');
    for (const snapshot of snapshots) this.remember(snapshot.nodeId, snapshot.attempts);
    for (const request of requests) {
      const channelId = this.channels.get(request.route);
      if (channelId && ['down', 'recovery'].includes(request.kind)) {
        this.retain({ nodeId: request.sender, id: request.key, monitorId: request.monitor,
          sourceIncidentId: request.source, channelId, kind: request.kind });
      }
    }
    const channelId = route === undefined ? undefined : this.channels.get(route);
    assert.ok(route === undefined || channelId, 'known capacity destination');
    const accepted = new Set(requests.filter(request => request.accepted).map(request =>
      `${this.channels.get(request.route)}/${request.monitor}/${request.kind}/${request.key}`));
    const pending: { nodeId: string; id: string; state: string }[] = [];
    let exact = 0, aliased = 0;
    for (const obligation of this.retained.values()) {
      if (channelId && obligation.channelId !== channelId || kind && obligation.kind !== kind) continue;
      const snapshot = snapshots.find(value => value.nodeId === obligation.nodeId);
      const current = snapshot?.attempts.find(value => value.id === obligation.id);
      if (!snapshot || !current) {
        pending.push({ nodeId: obligation.nodeId, id: obligation.id, state: 'missing' });
        continue;
      }
      // This witness is selected with source/episode reconciliation under one
      // node lock. Canonical display IDs cannot establish source retirement.
      const witness = current.satisfiedByReceiptId;
      const fulfilled = current.state === 'accepted' && witness && snapshots.every(peer =>
        peer.notifications.some(receipt => receipt.id === witness && receipt.monitorId === obligation.monitorId &&
          receipt.channelId === obligation.channelId && receipt.destinationId === obligation.destinationId &&
          receipt.kind === obligation.kind && accepted.has(
            `${receipt.channelId}/${receipt.monitorId}/${receipt.kind}/${receipt.id}`)));
      if (!fulfilled) pending.push({ nodeId: obligation.nodeId, id: obligation.id, state: current.state });
      else if (witness === obligation.id) exact++;
      else aliased++;
    }
    return { exact, aliased, pending };
  }
}
