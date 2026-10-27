import type { FleetDeployment, FleetNodeGroup, FleetPlacement, FleetTargetReferences, Monitor, MonitorTag } from '@uptime-canary/shared';
import { ApiFailure } from './api';

const record = (v: unknown): v is Record<string, unknown> => v !== null && typeof v === 'object' && !Array.isArray(v);
const text = (v: unknown, n = 128): v is string => typeof v === 'string' && v.length <= n && !/[\x00-\x1f\x7f]/u.test(v);
const id = (v: unknown): v is string => text(v) && /^[A-Za-z0-9][A-Za-z0-9_.:-]*$/u.test(v);
const fleetId = (v: unknown): v is string => typeof v === 'string' && /^[a-f0-9]{32}$/u.test(v);
const ids = (v: unknown): v is string[] => Array.isArray(v) && v.length <= 64 && v.every(id) && new Set(v).size === v.length;
const integer = (v: unknown): v is number => Number.isSafeInteger(v) && (v as number) >= 0;
const bad = (): never => { throw new ApiFailure(200, 'The dashboard returned unreadable fleet data.'); };

export function readFleetGroup(value: unknown): FleetNodeGroup {
  if (!record(value) || !fleetId(value.id) || !id(value.revision) || !text(value.name) || !ids(value.registrationIds) || !integer(value.updatedAt)) return bad();
  if (value.syncState !== undefined && !['pending','applied','blocked'].includes(String(value.syncState)) || value.syncReason !== undefined && !text(value.syncReason,1024)) return bad();
  return { id: value.id, revision: value.revision, name: value.name, registrationIds: [...value.registrationIds], updatedAt: value.updatedAt,
    ...(value.syncState !== undefined ? { syncState: value.syncState as FleetNodeGroup['syncState'] } : {}), ...(value.syncReason !== undefined ? { syncReason: value.syncReason as string } : {}) };
}
export function readFleetDeployment(value: unknown): FleetDeployment {
  if (!record(value) || !fleetId(value.id) || !id(value.revision) || !integer(value.updatedAt) || !ids(value.registrationIds)
    || !['pending', 'applied', 'blocked', 'unauthorized', 'removed'].includes(String(value.state)) || !Array.isArray(value.placements) || value.placements.length > 64
    || !record(value.monitor) || !text(value.monitor.name) || !text(value.monitor.type) || !ids(value.monitor.nodeIds) || typeof value.monitor.enabled !== 'boolean') return bad();
  if (value.pinnedRegistrationIds !== undefined && !ids(value.pinnedRegistrationIds)
    || value.groupIds !== undefined && (!ids(value.groupIds) || !value.groupIds.every(fleetId))
    || value.syncNotifications !== undefined && typeof value.syncNotifications !== 'boolean'
    || value.notificationSourceRegistrationId !== undefined && !id(value.notificationSourceRegistrationId)) return bad();
  const placements = value.placements.map((p): FleetPlacement => {
    if (!record(p) || !fleetId(p.id) || !id(p.registrationId) || !ids(p.registrationIds) || !ids(p.nodeIds) || !id(p.monitorId)
      || !integer(p.sequence) || !text(p.revision) || !['pending', 'syncing', 'applied', 'removed'].includes(String(p.state))
      || p.retiringRegistrationIds !== undefined && !ids(p.retiringRegistrationIds)) return bad();
    return { id: p.id, registrationId: p.registrationId, registrationIds: [...p.registrationIds], nodeIds: [...p.nodeIds], monitorId: p.monitorId,
      ...(p.retiringRegistrationIds !== undefined ? { retiringRegistrationIds: [...p.retiringRegistrationIds as string[]] } : {}),
      sequence: p.sequence, revision: p.revision, state: p.state as FleetPlacement['state'] };
  });
  if (new Set(placements.map(p => p.id)).size !== placements.length) return bad();
  let source: FleetDeployment['source'];
  if (value.source !== undefined) {
    if (!record(value.source) || !id(value.source.registrationId) || !id(value.source.monitorId) || !id(value.source.revision)) return bad();
    source = { registrationId: value.source.registrationId, monitorId: value.source.monitorId, revision: value.source.revision };
  }
  const references: Record<string,FleetTargetReferences> = {};
  if (value.references !== undefined) {
    if (!record(value.references) || Object.keys(value.references).length > 64) return bad();
    for (const [registration, ref] of Object.entries(value.references)) {
      if (!id(registration) || !record(ref) || Object.keys(ref).some(k => !['notificationIds','groupId','tags'].includes(k))) return bad();
      if (ref.notificationIds !== undefined && (!ids(ref.notificationIds) || ref.notificationIds.length > 16)) return bad();
      if (ref.groupId !== undefined && ref.groupId !== '' && !id(ref.groupId)) return bad();
      if (ref.tags !== undefined && (!Array.isArray(ref.tags) || ref.tags.length > 32 || !ref.tags.every(t => record(t) && id(t.tagId) && (t.value === undefined || text(t.value,4096)) && (t.public === undefined || typeof t.public === 'boolean')))) return bad();
      references[registration] = { ...(ref.notificationIds !== undefined ? {notificationIds: [...ref.notificationIds as string[]]} : {}),
        ...(ref.groupId !== undefined ? {groupId: ref.groupId as string} : {}), ...(ref.tags !== undefined ? {tags: ref.tags as MonitorTag[]} : {}) };
    }
  }
  return { id: value.id, revision: value.revision, updatedAt: value.updatedAt, registrationIds: [...value.registrationIds], placements,
    ...(value.pinnedRegistrationIds !== undefined ? { pinnedRegistrationIds: [...value.pinnedRegistrationIds as string[]] } : {}),
    ...(value.groupIds !== undefined ? { groupIds: [...value.groupIds as string[]] } : {}),
    ...(value.syncNotifications !== undefined ? { syncNotifications: value.syncNotifications as boolean } : {}),
    ...(value.notificationSourceRegistrationId !== undefined ? { notificationSourceRegistrationId: value.notificationSourceRegistrationId as string } : {}),
    ...(value.references !== undefined ? {references} : {}),
    canCancel: value.canCancel === true,
    canRemove: value.canRemove === true,
    state: value.state as FleetDeployment['state'], monitor: { ...value.monitor, nodeIds: [...value.monitor.nodeIds] } as unknown as Monitor,
    ...(source ? { source } : {}), ...(text(value.reason, 1024) ? { reason: value.reason } : {}) };
}
