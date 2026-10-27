import { Fragment, useId } from 'react';
import type { FleetTargetReferences, MonitorTag } from '@uptime-canary/shared';
import type { FleetReferenceFieldsProps, FleetReferenceOption, FleetReferenceProfile } from '../fleetReferences';
import type { FleetReferenceSync } from '../fleetViews';
import { plural } from '../format';
import { Glyph, Icon, type IconName } from '../ui/icons';

/**
 * Each selected node's own notification channels, outage dependency group and tags for a placed monitor. Every ID
 * belongs to the node that lists it: nothing here matches or copies a resource to another node by name, reads anything
 * or picks defaults. A node without its own entry uses this form's settings. Choosing references copies those exactly,
 * unknown IDs, tag values and public flags included, so later edits to the form don't reach that node. IDs a node
 * doesn't list stay selected and visible until they're removed here. Profiles carry names, IDs and flags only.
 *
 * With `sync`, the dashboard copies the form's channels and tags from the source node to the others, so a node only
 * needs its own entry for something different, and each field is chosen on its own: an entry holds just the fields
 * set here. Channel and tag IDs from the form aren't expected on other nodes then. An outage dependency group is never
 * copied, so a node that doesn't list the form's group is pointed out.
 */

const MAX_CHANNELS = 16;
const MAX_TAGS = 32;

type Status = FleetReferenceProfile['status'];
type Base = FleetReferenceFieldsProps['base'];

const byName = (a: FleetReferenceOption, b: FleetReferenceOption): number => a.name.localeCompare(b.name) || a.id.localeCompare(b.id);
const sorted = (options: readonly FleetReferenceOption[]): FleetReferenceOption[] => [...options].sort(byName);
const find = (options: readonly FleetReferenceOption[], id: string): FleetReferenceOption | undefined => options.find(option => option.id === id);
const nameOf = (option: FleetReferenceOption): string => option.name || option.id;
/** New tag objects with every stored key kept as it is. */
const copyTags = (tags: readonly MonitorTag[] | undefined): MonitorTag[] => (tags ?? []).map(tag => ({ ...tag }));

/** Why a selected ID isn't among this node's listed resources, as far as the browser can tell. */
const absentText = (status: Status): string => status === 'ready' ? 'not on this node' : status === 'loading' ? 'not read yet' : 'node not read';
const noneText = (status: Status, noun: string): string =>
  status === 'ready' ? `This node has no ${noun}.` : status === 'loading' ? `Reading this node’s ${noun}…` : `This node’s ${noun} couldn’t be read.`;

/** Reading a node is transport, so these use accent, dashes and hatching like the fleet snapshots, never health colours. */
const STATUS: Record<Status, { icon: IconName; label: string }> = {
  loading: { icon: 'refresh', label: 'Reading…' },
  ready: { icon: 'eye', label: 'Read' },
  unavailable: { icon: 'plug', label: 'Not read' },
};

/** An ID as this node lists it: its name when the node has that exact ID, otherwise the ID itself. */
function RefName({ options, id, status }: { options: readonly FleetReferenceOption[]; id: string; status: Status }) {
  const option = find(options, id);
  return option ? <>{nameOf(option)}</> : <><span className="mono">{id}</span> <span className="fl-ref-gone">({absentText(status)})</span></>;
}

/** Selected IDs this node doesn't list. They stay until removed here, never dropped or replaced automatically. */
function AbsentList({ noun, ids, status, onRemove }: { noun: string; ids: readonly string[]; status: Status; onRemove: (id: string) => void }) {
  return (
    <ul className="fl-ref-miss" aria-label={`Selected ${noun} IDs ${status === 'ready' ? 'this node doesn’t list' : 'not checked yet'}`}>
      {ids.map(id => (
        <li key={id}>
          <Glyph tone="unknown" />
          <span className="mono">{id}</span>
          <span className="muted">{absentText(status)}</span>
          <button type="button" className="linkbtn" onClick={() => onRemove(id)}><Icon name="close" />Remove<span className="vh"> {noun} {id}</span></button>
        </li>
      ))}
    </ul>
  );
}

function Inherited({ profile, base }: { profile: FleetReferenceProfile; base: Base }) {
  const channels = base.notificationIds ?? [];
  const group = base.groupId ?? '';
  const tags = base.tags ?? [];
  const { status } = profile;
  const missing = status !== 'ready' ? 0
    : channels.filter(id => !find(profile.channels, id)).length + (group && !find(profile.groups, group) ? 1 : 0) + tags.filter(tag => !find(profile.tags, tag.tagId)).length;
  return (
    <>
      <dl className="fl-ref-inh">
        <dt>Channels</dt>
        <dd>{channels.length ? channels.map((id, index) => <Fragment key={`${index}\n${id}`}>{index ? ', ' : ''}<RefName options={profile.channels} id={id} status={status} /></Fragment>) : 'None'}</dd>
        <dt>Outage dependency group</dt>
        <dd>{group ? <RefName options={profile.groups} id={group} status={status} /> : 'None'}</dd>
        <dt>Tags</dt>
        <dd>{tags.length ? tags.map((tag, index) => (
          <Fragment key={`${index}\n${tag.tagId}`}>
            {index ? ', ' : ''}<RefName options={profile.tags} id={tag.tagId} status={status} />{tag.value ? <>: {tag.value}</> : null}{tag.public ? ' (public)' : null}
          </Fragment>
        )) : 'None'}</dd>
      </dl>
      {missing ? (
        <small className="fwarn">
          <Glyph tone="warn" />
          <span>{missing === 1 ? 'One ID from this form isn’t' : `${missing} IDs from this form aren’t`} on this node. To use this node’s own channels, group or tags, choose references on this node.</span>
        </small>
      ) : null}
    </>
  );
}

function ChannelChoices({ profile, selected, inherited, onChange }: { profile: FleetReferenceProfile; selected: readonly string[]; inherited: boolean; onChange: (ids: string[]) => void }) {
  const id = useId();
  const options = sorted(profile.channels);
  const known = new Set(options.map(option => option.id));
  const chosen = new Set(selected);
  const absent = [...chosen].filter(channelId => !known.has(channelId));
  const full = chosen.size >= MAX_CHANNELS;
  const toggle = (channelId: string, on: boolean): void => onChange(on ? [...selected, channelId] : selected.filter(other => other !== channelId));
  return (
    <fieldset className="fld" aria-describedby={`${id}-h`}>
      <legend>Notification channels on this node <span className="muted">({chosen.size} of at most {MAX_CHANNELS})</span></legend>
      {options.length ? (
        <div className="chanpick fl-ref-pick">
          {options.map(option => {
            const on = chosen.has(option.id);
            const notes = [option.isDefault ? 'default for new monitors' : '', option.enabled === false ? 'disabled' : '', option.conflicted ? 'configuration conflict' : ''].filter(Boolean);
            return (
              <label key={option.id} className={option.enabled === false ? 'off' : undefined}>
                <input type="checkbox" checked={on} disabled={!on && full} onChange={event => toggle(option.id, event.target.checked)} />
                <span>
                  <b>{nameOf(option)}</b>
                  {notes.length ? <span className="nm-sub">{option.conflicted ? <Glyph tone="warn" /> : null}{notes.join(' · ')}</span> : null}
                </span>
              </label>
            );
          })}
        </div>
      ) : <p className="fl-none">{noneText(profile.status, 'channels')}</p>}
      {absent.length ? <AbsentList noun="channel" ids={absent} status={profile.status} onRemove={channelId => onChange(selected.filter(other => other !== channelId))} /> : null}
      <small id={`${id}-h`}>
        {inherited ? 'Shown from this form until you change it here. ' : ''}Only the channels checked here are chosen; this node’s default channels aren’t preselected. Channel settings stay on the node.
      </small>
      {chosen.size > MAX_CHANNELS ? <small className="fwarn"><Glyph tone="warn" /><span>More than {MAX_CHANNELS} channels are chosen. Remove {chosen.size - MAX_CHANNELS} before saving.</span></small> : null}
    </fieldset>
  );
}

function GroupChoice({ profile, value, inherited, onChange }: { profile: FleetReferenceProfile; value: string; inherited: boolean; onChange: (groupId: string) => void }) {
  const id = useId();
  const options = sorted(profile.groups);
  const absent = value !== '' && !find(options, value);
  return (
    <div className="fld">
      <label htmlFor={`${id}-s`}>Outage dependency group on this node <span className="muted">(optional)</span></label>
      <select id={`${id}-s`} value={value} onChange={event => onChange(event.target.value)} aria-describedby={`${id}-h`}>
        <option value="">None</option>
        {options.map(option => <option key={option.id} value={option.id}>{`${nameOf(option)}${option.conflicted ? ' (configuration conflict)' : ''}`}</option>)}
        {absent ? <option value={value}>{`Unknown group ${value}`}</option> : null}
      </select>
      {absent ? <AbsentList noun="group" ids={[value]} status={profile.status} onRemove={() => onChange('')} /> : null}
      <small id={`${id}-h`}>
        {inherited ? 'Shown from this form until you change it here. ' : ''}One of this node’s own Groups, whose outage monitor can hold back this monitor’s alerts on this node.
        It isn’t a dashboard node group: those only pick nodes for Run on.{profile.status === 'ready' || options.length ? '' : ` ${noneText(profile.status, 'groups')}`}
      </small>
    </div>
  );
}

function TagChoices({ profile, selected, inherited, onChange }: { profile: FleetReferenceProfile; selected: readonly MonitorTag[]; inherited: boolean; onChange: (tags: MonitorTag[]) => void }) {
  const id = useId();
  const options = sorted(profile.tags);
  const chosen = new Set(selected.map(tag => tag.tagId));
  const full = selected.length >= MAX_TAGS;
  // Adding a tag gives only its ID: no value or public flag is invented for it.
  const add = (tagId: string): void => onChange([...copyTags(selected), { tagId }]);
  const drop = (tagId: string): void => onChange(copyTags(selected.filter(tag => tag.tagId !== tagId)));
  const removeAt = (index: number): void => onChange(copyTags(selected.filter((_, other) => other !== index)));
  const edit = (index: number, change: (tag: MonitorTag) => void): void =>
    onChange(selected.map((tag, other) => { const next = { ...tag }; if (other === index) change(next); return next; }));
  const setValue = (index: number, value: string): void => edit(index, tag => { if (value) tag.value = value; else delete tag.value; });
  const setPublic = (index: number, on: boolean): void => edit(index, tag => { if (on) tag.public = true; else delete tag.public; });
  const seen = new Map<string, number>();
  const keyOf = (tagId: string): string => { const n = seen.get(tagId) ?? 0; seen.set(tagId, n + 1); return `${tagId}\n${n}`; };
  return (
    <fieldset className="fld" aria-describedby={`${id}-h`}>
      <legend>Tags on this node <span className="muted">({selected.length} of at most {MAX_TAGS})</span></legend>
      {options.length ? (
        <div className="chanpick fl-ref-pick">
          {options.map(option => {
            const on = chosen.has(option.id);
            return (
              <label key={option.id}>
                <input type="checkbox" checked={on} disabled={!on && full} onChange={event => { if (event.target.checked) add(option.id); else drop(option.id); }} />
                <span>
                  <b>{nameOf(option)}</b>
                  {option.conflicted ? <span className="nm-sub"><Glyph tone="warn" />configuration conflict</span> : null}
                </span>
              </label>
            );
          })}
        </div>
      ) : <p className="fl-none">{noneText(profile.status, 'tags')}</p>}
      {selected.length ? (
        <ul className="fl-tagv" aria-label="Values of the chosen tags">
          {selected.map((tag, index) => {
            const option = find(options, tag.tagId);
            const name = option ? nameOf(option) : `unknown tag ${tag.tagId}`;
            return (
              <li key={keyOf(tag.tagId)} className={option ? undefined : 'gone'}>
                <span className="fl-tagv-n">
                  {option ? <b>{nameOf(option)}</b> : <><Glyph tone="unknown" /><span><b className="mono">{tag.tagId}</b> <span className="muted">{absentText(profile.status)}</span></span></>}
                </span>
                <input type="text" aria-label={`Value for ${name} (optional)`} placeholder="value (optional)" value={tag.value ?? ''} maxLength={128} onChange={event => setValue(index, event.target.value)} />
                <label className="check tag-pub"><input type="checkbox" checked={tag.public === true} onChange={event => setPublic(index, event.target.checked)} /><span>Public<span className="vh"> for {name}</span></span></label>
                <button type="button" className="icon-btn sm" onClick={() => removeAt(index)} aria-label={`Remove tag ${name}`}><Icon name="close" /></button>
              </li>
            );
          })}
        </ul>
      ) : null}
      <small id={`${id}-h`}>
        {inherited ? 'Shown from this form until you change it here. ' : ''}A tag added here starts without a value. “Public” only lets a status page that shows tags for this monitor show it.
      </small>
      {selected.length > MAX_TAGS ? <small className="fwarn"><Glyph tone="warn" /><span>More than {MAX_TAGS} tags are chosen. Remove {selected.length - MAX_TAGS} before saving.</span></small> : null}
    </fieldset>
  );
}

function NodeReferences({ profile, base, own, onEnable, onDisable, onWrite }: {
  profile: FleetReferenceProfile; base: Base; own: FleetTargetReferences | undefined;
  onEnable: () => void; onDisable: () => void; onWrite: (patch: FleetTargetReferences) => void;
}) {
  const id = useId();
  const { status, label } = profile;
  const shown = STATUS[status];
  const detail = status === 'ready' ? `${plural(profile.channels.length, 'channel')} · ${plural(profile.groups.length, 'group')} · ${plural(profile.tags.length, 'tag')} on this node`
    : status === 'loading' ? 'Reading this node’s channels, groups and tags…'
    : 'This node’s channels, groups and tags couldn’t be read. Choices here stay as they are.';
  return (
    <div className={`fl-ref s-${status}${own ? ' on' : ''}`} role="group" aria-labelledby={`${id}-n`}>
      <div className="fl-ref-h">
        <span className="fl-ref-id" id={`${id}-n`}><b>{label}</b> <span className="mono">{profile.nodeId || 'node ID not read yet'}</span></span>
        <span className="fl-ref-st"><Icon name={shown.icon} />{shown.label}</span>
      </div>
      <p className="fl-ref-t">{detail}</p>
      <label className="check fl-ref-on">
        <input type="checkbox" checked={own !== undefined} aria-controls={own ? `${id}-b` : undefined} onChange={event => { if (event.target.checked) onEnable(); else onDisable(); }} />
        <span>
          Choose references on this node<span className="vh"> ({label})</span>
          <small>{own
            ? 'Set for this node only: later changes to this form’s channels, group and tags don’t reach it. Turning this off discards these choices.'
            : 'Off: this node uses this form’s channels, group and tags, including later changes.'}</small>
        </span>
      </label>
      {own ? (
        <div className="fl-ref-b" id={`${id}-b`}>
          <ChannelChoices profile={profile} selected={own.notificationIds ?? base.notificationIds ?? []} inherited={own.notificationIds === undefined} onChange={notificationIds => onWrite({ notificationIds })} />
          <GroupChoice profile={profile} value={own.groupId ?? base.groupId ?? ''} inherited={own.groupId === undefined} onChange={groupId => onWrite({ groupId })} />
          <TagChoices profile={profile} selected={own.tags ?? base.tags ?? []} inherited={own.tags === undefined} onChange={tags => onWrite({ tags })} />
        </div>
      ) : <Inherited profile={profile} base={base} />}
    </div>
  );
}

type SyncField = keyof FleetTargetReferences;

/** Names as the source node gives them; IDs it doesn't name are only counted, since they're copied by ID anyway. */
function namedList(ids: readonly string[], named: Readonly<Record<string, string>>, noun: string): string {
  const known = ids.flatMap(id => named[id] ?? []);
  const rest = ids.length - known.length;
  return [...known, rest ? plural(rest, known.length ? `other ${noun}` : noun) : ''].filter(Boolean).join(', ') || 'None';
}

function SyncedReferences({ profile, base, own, sync, onSet, onClear }: {
  profile: FleetReferenceProfile; base: Base; own: FleetTargetReferences | undefined; sync: FleetReferenceSync;
  onSet: (patch: FleetTargetReferences) => void; onClear: (field: SyncField) => void;
}) {
  const id = useId();
  const { status, label } = profile;
  const shown = STATUS[status];
  const source = profile.registrationId === sync.sourceRegistrationId;
  const channels = base.notificationIds ?? [];
  const tags = base.tags ?? [];
  const group = base.groupId ?? '';
  const ownChannels = own?.notificationIds;
  const ownTags = own?.tags;
  const ownGroup = own?.groupId;
  const listed = group ? find(profile.groups, group) : undefined;
  const groupName = sync.groupName ?? (listed ? nameOf(listed) : group);
  const gap = group !== '' && !source && ownGroup === undefined && status === 'ready' && !listed;
  const from = source ? 'Its own' : `Copied from ${sync.sourceLabel}`;
  const tagText = tags.length
    ? tags.map(tag => `${sync.tagNames[tag.tagId] ?? 'a tag'}${tag.value ? `: ${tag.value}` : ''}${tag.public ? ' (public)' : ''}`).join(', ') : 'None';
  const detail = source ? 'The form’s channels and tags are this node’s own, and they’re copied from here.'
    : status === 'ready' ? `${plural(profile.channels.length, 'channel')} · ${plural(profile.groups.length, 'group')} · ${plural(profile.tags.length, 'tag')} on this node`
    : status === 'loading' ? 'Reading this node’s channels, groups and tags…'
    : 'This node’s channels, groups and tags couldn’t be read. Choices here stay as they are.';
  const choose = <span className="vh"> for {label}</span>;
  return (
    <div className={`fl-ref s-${status}${own ? ' on' : ''}${gap ? ' gap' : ''}`} role="group" aria-labelledby={`${id}-n`}>
      <div className="fl-ref-h">
        <span className="fl-ref-id" id={`${id}-n`}><b>{label}</b> <span className="mono">{profile.nodeId || 'node ID not read yet'}</span></span>
        <span className="fl-ref-st"><Icon name={source ? 'copy' : shown.icon} />{source ? 'Source' : shown.label}</span>
      </div>
      <p className="fl-ref-t">{detail}</p>
      <dl className="fl-ref-inh fl-ref-sync">
        <dt>Channels</dt>
        <dd>
          <span>{ownChannels ? 'This node’s own, chosen below' : `${from}: ${namedList(channels, sync.channelNames, 'channel')}`}</span>
          <button type="button" className="linkbtn" onClick={() => ownChannels ? onClear('notificationIds') : onSet({ notificationIds: source ? [...channels] : [] })}>
            {ownChannels ? source ? 'Use the form’s channels' : `Copy from ${sync.sourceLabel} again` : 'Choose for this node'}{choose}
          </button>
        </dd>
        <dt>Tags</dt>
        <dd>
          <span>{ownTags ? 'This node’s own, chosen below' : `${from}: ${tagText}`}</span>
          <button type="button" className="linkbtn" onClick={() => ownTags ? onClear('tags') : onSet({ tags: source ? copyTags(tags) : [] })}>
            {ownTags ? source ? 'Use the form’s tags' : `Copy from ${sync.sourceLabel} again` : 'Choose for this node'}{choose}
          </button>
        </dd>
        <dt>Outage dependency group</dt>
        <dd>
          {ownGroup !== undefined ? <span>Chosen for this node below</span>
            : !group ? <span>None</span>
            : source || listed ? <span>{groupName}</span>
            : gap ? <span className="fl-ref-gap"><Glyph tone="warn" /><span>{groupName} isn’t on this node, so this node refuses the monitor. Choose one of its own groups or None.</span></span>
            : <span>{groupName}, only if this node has the same group</span>}
          <button type="button" className="linkbtn" onClick={() => ownGroup !== undefined ? onClear('groupId') : onSet({ groupId: listed ? group : '' })}>
            {ownGroup !== undefined ? 'Use the form’s group' : 'Choose for this node'}{choose}
          </button>
        </dd>
      </dl>
      {ownChannels || ownTags || ownGroup !== undefined ? (
        <div className="fl-ref-b">
          {ownChannels ? <ChannelChoices profile={profile} selected={ownChannels} inherited={false} onChange={notificationIds => onSet({ notificationIds })} /> : null}
          {ownGroup !== undefined ? <GroupChoice profile={profile} value={ownGroup} inherited={false} onChange={groupId => onSet({ groupId })} /> : null}
          {ownTags ? <TagChoices profile={profile} selected={ownTags} inherited={false} onChange={next => onSet({ tags: next })} /> : null}
        </div>
      ) : null}
    </div>
  );
}

export function FleetReferenceFields({ profiles, base, overrides, onChange, disabled, onRetry, sync }: FleetReferenceFieldsProps & { sync?: FleetReferenceSync }) {
  const id = useId();
  const listed = new Set(profiles.map(profile => profile.registrationId));
  const ownCount = profiles.filter(profile => Object.hasOwn(overrides, profile.registrationId)).length;
  const hidden = Object.keys(overrides).filter(registrationId => !listed.has(registrationId)).length;
  const reading = profiles.filter(profile => profile.status === 'loading').length;
  const unread = profiles.filter(profile => profile.status === 'unavailable');

  // Every change builds a new map; only the named registration's entry differs, and nothing passed in is modified.
  const enable = (registrationId: string): void => {
    if (disabled) return;
    onChange({ ...overrides, [registrationId]: { notificationIds: [...(base.notificationIds ?? [])], groupId: base.groupId ?? '', tags: copyTags(base.tags) } });
  };
  const disable = (registrationId: string): void => {
    if (disabled) return;
    const next: Record<string, FleetTargetReferences> = { ...overrides };
    delete next[registrationId];
    onChange(next);
  };
  const write = (registrationId: string, patch: FleetTargetReferences): void => {
    if (disabled || !Object.hasOwn(overrides, registrationId)) return;
    onChange({ ...overrides, [registrationId]: { ...overrides[registrationId], ...patch } });
  };
  // Copying: one field at a time. A node's entry holds only the fields set for it, and goes once none are left.
  const set = (registrationId: string, patch: FleetTargetReferences): void => {
    if (disabled) return;
    onChange({ ...overrides, [registrationId]: { ...(Object.hasOwn(overrides, registrationId) ? overrides[registrationId] : {}), ...patch } });
  };
  const clear = (registrationId: string, field: SyncField): void => {
    if (disabled || !Object.hasOwn(overrides, registrationId)) return;
    const entry: FleetTargetReferences = { ...overrides[registrationId] };
    delete entry[field];
    const next: Record<string, FleetTargetReferences> = { ...overrides };
    if (Object.keys(entry).length) next[registrationId] = entry; else delete next[registrationId];
    onChange(next);
  };

  return (
    <fieldset className="fld full fl-refs" disabled={disabled} aria-describedby={`${id}-h`}>
      <legend>
        Channels, group and tags on each node{profiles.length ? <span className="muted"> ({ownCount} of {plural(profiles.length, 'node')} with their own)</span> : null}
      </legend>
      <p className="fl-peer-note" id={`${id}-h`}>
        <Icon name="info" />
        {sync ? (
          <span>
            Every node gets the form’s channels and tags, copied from {sync.sourceLabel}. Choose something else here only for a node that needs it.
            Outage dependency groups aren’t copied: each node can only use one of its own. HA peers that share one monitor configuration need the same choices.
          </span>
        ) : (
          <span>
            Each node uses the notification channels, group and tags set in this form unless you choose references on that node here.
            These IDs belong to one node, so nothing is matched or copied to another node by name. HA peers that share one monitor
            configuration need matching references; independent nodes can each use their own.
          </span>
        )}
      </p>
      <span className="vh" role="status">{reading ? `Reading channels, groups and tags from ${reading} of ${plural(profiles.length, 'node')}.` : ''}</span>
      {unread.length ? (
        <div className="fl-ref-retry">
          <span>
            <Glyph tone="warn" />
            <span>{unread.length === 1 ? `${unread[0].label} couldn’t be read, so its` : `${unread.length} nodes couldn’t be read, so their`} channels, groups and tags aren’t listed. Choices stay as they are.</span>
          </span>
          <button type="button" className="btn sm" onClick={() => { if (!disabled) onRetry(); }}><Icon name="refresh" />Retry<span className="vh"> reading every selected node</span></button>
        </div>
      ) : null}
      {profiles.length ? (
        <div className="fl-ref-list">
          {profiles.map(profile => {
            const own = Object.hasOwn(overrides, profile.registrationId) ? overrides[profile.registrationId] : undefined;
            return sync ? (
              <SyncedReferences key={profile.registrationId} profile={profile} base={base} own={own} sync={sync}
                onSet={patch => set(profile.registrationId, patch)} onClear={field => clear(profile.registrationId, field)} />
            ) : (
              <NodeReferences key={profile.registrationId} profile={profile} base={base} own={own}
                onEnable={() => enable(profile.registrationId)} onDisable={() => disable(profile.registrationId)}
                onWrite={patch => write(profile.registrationId, patch)} />
            );
          })}
        </div>
      ) : <p className="fl-none">{sync ? 'Choose where this monitor runs to see each node here.' : 'Choose nodes under Run on to set channels, a group and tags for each.'}</p>}
      {hidden ? <small>{hidden === 1 ? '1 node not listed here keeps its' : `${hidden} nodes not listed here keep their`} own references as they are.</small> : null}
    </fieldset>
  );
}
