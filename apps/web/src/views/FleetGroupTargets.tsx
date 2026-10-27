import { useId } from 'react';
import type { RegisteredNode } from '@uptime-canary/shared';
import { registryOrder } from '../dashboard';
import type { FleetNodeGroup } from '../fleetViews';
import { andList, plural } from '../format';
import { href } from '../router';
import { ErrorText } from '../ui/bits';
import { Glyph, Icon } from '../ui/icons';

/**
 * The node groups one monitor follows. Values are dashboard group IDs. A group ID the dashboard no longer lists stays
 * chosen, and visible, until it's removed here. With `active` off the monitor runs on individually chosen nodes:
 * nothing is shown as chosen, and choosing a group asks the form to run on groups again.
 */
export function FleetGroupTargets({ groups, nodes, selected, active, onChange, onChooseNodes, disabled, error, outside }: {
  groups: readonly FleetNodeGroup[]; nodes: readonly RegisteredNode[]; selected: readonly string[]; active: boolean;
  onChange: (groupIds: string[]) => void; onChooseNodes: () => void; disabled?: boolean; error?: string;
  /** Registrations the monitor runs on now that the chosen groups don't include. */
  outside: readonly string[];
}) {
  const id = useId();
  const byId = new Map(nodes.map(node => [node.id, node]));
  const sorted = [...groups].sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id));
  const known = new Set(groups.map(group => group.id));
  const chosen = active ? selected.filter(groupId => known.has(groupId)) : [];
  const missing = active ? selected.filter(groupId => !known.has(groupId)) : [];
  const runsOn = registryOrder([...new Set(chosen.flatMap(groupId => groups.find(group => group.id === groupId)?.registrationIds ?? []))].flatMap(registrationId => byId.get(registrationId) ?? []));
  const labels = (registrationIds: readonly string[]): string => andList(registrationIds.map(registrationId => byId.get(registrationId)?.label ?? 'a node no longer registered'));

  const emit = (next: string[]): void => { if (!disabled) onChange([...new Set(next)]); };
  const toggle = (groupId: string, on: boolean): void => {
    if (!active) { if (on) emit([groupId]); return; }
    emit(on ? [...selected, groupId] : selected.filter(other => other !== groupId));
  };

  const describedBy = [`${id}-h`, error ? `${id}-e` : ''].filter(Boolean).join(' ');
  return (
    <fieldset className="fld full fl-gsel" disabled={disabled} aria-describedby={describedBy} aria-invalid={error ? true : undefined}>
      <legend>Node groups <span className="muted">({active ? chosen.length ? `${chosen.length} chosen` : 'none chosen' : 'not used'})</span></legend>
      {!active ? (
        <p className="fl-gsel-off"><Icon name="info" /><span>This monitor runs on nodes chosen one by one, under Advanced settings. Choose a group to run it on that group’s nodes instead.</span></p>
      ) : null}

      {sorted.length ? (
        <div className="fl-gsel-l">
          {sorted.map(group => {
            const members = registryOrder([...new Set(group.registrationIds)].flatMap(registrationId => byId.get(registrationId) ?? []));
            const gone = new Set(group.registrationIds.filter(registrationId => !byId.has(registrationId))).size;
            const on = active && selected.includes(group.id);
            return (
              <label key={group.id} className={gone ? 'warn' : undefined}>
                <input type="checkbox" value={group.id} checked={on} onChange={event => toggle(group.id, event.target.checked)} />
                <span className="fl-gsel-b">
                  <b>{group.name}</b>
                  <span className="nm-sub">{members.length ? `${plural(members.length, 'node')}: ${members.map(node => node.label).join(', ')}` : 'No registered nodes'}</span>
                  {gone ? <span className="nm-sub fl-gsel-w"><Glyph tone="warn" />{gone === 1 ? 'One node is' : `${gone} nodes are`} no longer registered. Edit the group before saving.</span> : null}
                </span>
              </label>
            );
          })}
        </div>
      ) : (
        <div className="fl-gsel-none">
          <p>No node groups yet. A group keeps the same monitors on all of its nodes.</p>
          <div className="row-wrap">
            <a className="btn sm" href={href('node-groups')}><Icon name="plus" />Create a node group</a>
            <button type="button" className="btn sm" onClick={onChooseNodes}><Icon name="nodes" />Choose nodes one by one</button>
          </div>
        </div>
      )}

      {missing.length ? (
        <div className="fl-missing" role="group" aria-labelledby={`${id}-m`}>
          <p id={`${id}-m`}>
            <Glyph tone="warn" />
            <span>{missing.length === 1 ? 'A node group this monitor follows no longer exists.' : `${missing.length} node groups this monitor follows no longer exist.`} Remove {missing.length === 1 ? 'it' : 'them'} to save.</span>
          </p>
          <ul>
            {missing.map(groupId => (
              <li key={groupId}>
                <span className="mono">{groupId}</span>
                <button type="button" className="linkbtn" onClick={() => emit(selected.filter(other => other !== groupId))}><Icon name="close" />Remove<span className="vh"> node group {groupId}</span></button>
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      {active && outside.length ? (
        <p className="fwarn fl-gsel-out">
          <Glyph tone="warn" />
          <span>It also runs on {labels(outside)} now, outside these groups. Those assignments are kept when you save. To change them, choose nodes one by one under Advanced settings.</span>
        </p>
      ) : null}
      <small id={`${id}-h`}>
        {active && runsOn.length ? <>Runs on {plural(runsOn.length, 'node')}: {runsOn.map(node => node.label).join(', ')}. Nodes added to these groups later get it too. </> : null}
        {sorted.length ? <a href={href('node-groups')}>Manage node groups</a> : null}
      </small>
      <ErrorText id={`${id}-e`}>{error}</ErrorText>
    </fieldset>
  );
}
