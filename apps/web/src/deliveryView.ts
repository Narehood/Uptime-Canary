import type { DeliveryAttemptView } from '@uptime-canary/shared';

export type AttemptFilter = 'all' | 'problems' | 'accepted' | 'held' | 'tests';
type MonitorNames = ReadonlyMap<string, { monitor: { name: string } }>;
type ChannelNames = ReadonlyMap<string, { channel: { name: string } }>;

/** Distinguish routes within one channel without exposing recipient addresses.
 * Use the whole public ID when short suffixes collide; never merge identities. */
export function deliveryDestinationTokens(attempts: readonly DeliveryAttemptView[]): ReadonlyMap<string, string> {
  const groups = new Map<string, Set<string>>();
  for (const attempt of attempts) {
    if (!attempt.destinationId) continue;
    const key = attempt.channelId ?? attempt.provider;
    let ids = groups.get(key);
    if (!ids) { ids = new Set(); groups.set(key, ids); }
    ids.add(attempt.destinationId);
  }
  const ambiguous = new Set([...groups.values()].flatMap(ids => ids.size > 1 ? [...ids] : []));
  const suffixCounts = new Map<string, number>();
  for (const id of ambiguous) { const suffix = id.slice(-6); suffixCounts.set(suffix, (suffixCounts.get(suffix) ?? 0) + 1); }
  return new Map([...ambiguous].map(id => [id, suffixCounts.get(id.slice(-6)) === 1 && id.length > 6 ? `…${id.slice(-6)}` : id]));
}

/** Counts share the channel/search scope; selecting a chip only changes rows. */
export function deliveryView(attempts: readonly DeliveryAttemptView[], channelId: string, search: string,
  filter: AttemptFilter, monitors: MonitorNames, channels: ChannelNames) {
  const counts = { all: 0, problems: 0, accepted: 0, held: 0, tests: 0 };
  const rows: DeliveryAttemptView[] = [];
  const q = search.trim().toLowerCase();
  const destinationSuffix = q.startsWith('…') && q.length > 1 ? q.slice(1) : '';
  for (const attempt of attempts) {
    if (channelId && attempt.channelId !== channelId) continue;
    if (q && !`${monitors.get(attempt.monitorId ?? '')?.monitor.name ?? ''} ${channels.get(attempt.channelId ?? '')?.channel.name ?? ''} ${attempt.provider} ${attempt.nodeId} ${attempt.errorCode ?? ''} ${attempt.incidentId ?? ''} ${attempt.id} ${attempt.destinationId ?? ''}`.toLowerCase().includes(q)
      && !(destinationSuffix && attempt.destinationId?.toLowerCase().endsWith(destinationSuffix))) continue;
    const problem = attempt.state === 'failed' || attempt.state === 'unknown' || attempt.state === 'unavailable' || attempt.state === 'retrying';
    const accepted = attempt.state === 'accepted', held = attempt.state === 'held', test = attempt.kind === 'test';
    counts.all++;
    if (problem) counts.problems++;
    if (accepted) counts.accepted++;
    if (held) counts.held++;
    if (test) counts.tests++;
    if (filter === 'all' || (filter === 'problems' ? problem : filter === 'accepted' ? accepted : filter === 'held' ? held : test)) rows.push(attempt);
  }
  return { rows, counts };
}
