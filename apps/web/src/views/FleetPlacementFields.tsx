import { useId } from 'react';
import { nodeStateInfo, registryOrder, shownNodeId } from '../dashboard';
import type { FleetNodeGroup, FleetPlacementFieldsProps } from '../fleetViews';
import { plural } from '../format';
import { ErrorText } from '../ui/bits';
import { Glyph, Icon } from '../ui/icons';

/**
 * Where a monitor runs, chosen from every registration on this dashboard. Values are registration IDs, never node IDs.
 * A node's registry state is shown, never used to leave it out. Selected IDs that are no longer registered stay until
 * removed here. Node groups only tick boxes: nothing is read, saved or peered by this field.
 */
export function FleetPlacementFields({ nodes, groups, selectedRegistrationIds, onChange, disabled, error, legend = 'Run on' }: FleetPlacementFieldsProps) {
  const id = useId();
  const ordered = registryOrder([...nodes]);
  const known = new Set(ordered.map(node => node.id));
  const selected = new Set(selectedRegistrationIds);
  const missing = [...selected].filter(registrationId => !known.has(registrationId));
  const chosen = ordered.filter(node => selected.has(node.id)).length;
  const shortcuts = [...groups].sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id));

  const emit = (next: Iterable<string>): void => { if (!disabled) onChange([...new Set(next)]); };
  const toggle = (registrationId: string, on: boolean): void =>
    emit(on ? [...selectedRegistrationIds, registrationId] : selectedRegistrationIds.filter(other => other !== registrationId));
  const members = (group: FleetNodeGroup) => ordered.filter(node => group.registrationIds.includes(node.id));
  const include = (group: FleetNodeGroup): void => emit([...selectedRegistrationIds, ...members(group).map(node => node.id)]);
  // Select all keeps IDs that are no longer registered: only their own Remove or Clear drops them.
  const selectAll = (): void => emit([...selectedRegistrationIds, ...ordered.map(node => node.id)]);
  const clear = (): void => emit([]);
  const remove = (registrationId: string): void => emit(selectedRegistrationIds.filter(other => other !== registrationId));

  const describedBy = [`${id}-h`, error ? `${id}-e` : ''].filter(Boolean).join(' ');
  return (
    <fieldset className="fld full fl-place" disabled={disabled} aria-describedby={describedBy} aria-invalid={error ? true : undefined}>
      <legend>{legend} <span className="muted">({chosen} of {plural(ordered.length, 'node')}{missing.length ? ` · ${missing.length} no longer registered` : ''})</span></legend>

      {shortcuts.length ? (
        <div className="fl-gpick">
          <span className="fl-gpick-k" id={`${id}-g`}>Add a node group</span>
          <div className="fl-gpick-l" role="group" aria-labelledby={`${id}-g`}>
            {shortcuts.map(group => {
              const inGroup = members(group);
              const gone = new Set(group.registrationIds.filter(registrationId => !known.has(registrationId))).size;
              const all = inGroup.length > 0 && inGroup.every(node => selected.has(node.id));
              return (
                <button key={group.id} type="button" className={`fchip fl-gchip${all ? ' on' : ''}`} onClick={() => include(group)} disabled={!inGroup.length}>
                  <Icon name={all ? 'check' : 'plus'} />{group.name}<span className="n" aria-hidden="true">{inGroup.length}</span>
                  <span className="vh">: {all ? `all ${plural(inGroup.length, 'node')} selected` : inGroup.length ? `adds ${plural(inGroup.length, 'node')}` : 'no registered nodes'}{gone ? `, ${gone} no longer registered` : ''}</span>
                </button>
              );
            })}
          </div>
        </div>
      ) : null}

      {ordered.length ? (
        <div className="nodepick fl-pick">
          {ordered.map(node => {
            const info = nodeStateInfo(node);
            const nodeId = shownNodeId(node);
            const named = node.nodeName && node.nodeName !== node.label && node.nodeName !== node.nodeId ? node.nodeName : null;
            return (
              <label key={node.id} className={info.out ? 'out' : undefined}>
                <input type="checkbox" value={node.id} checked={selected.has(node.id)} onChange={event => toggle(node.id, event.target.checked)} />
                <span className="fl-pn">
                  <b className="fl-pn-l">{node.label}</b>
                  <span className="mono nm-sub">{nodeId ?? 'node ID not read yet'}{named ? ` · ${named}` : ''}</span>
                  <span className="nm-sub">{info.label}{node.local ? ' · this deployment' : ''}</span>
                </span>
              </label>
            );
          })}
        </div>
      ) : <p className="fl-none">No nodes are registered with this dashboard yet.</p>}

      {missing.length ? (
        <div className="fl-missing" role="group" aria-labelledby={`${id}-m`}>
          <p id={`${id}-m`}>
            <Glyph tone="warn" />
            <span>{missing.length === 1 ? 'A selected registration is' : `${missing.length} selected registrations are`} no longer on this dashboard. {missing.length === 1 ? 'It stays' : 'They stay'} selected until you remove {missing.length === 1 ? 'it' : 'them'}.</span>
          </p>
          <ul>
            {missing.map(registrationId => (
              <li key={registrationId}>
                <span className="mono">{registrationId}</span>
                <button type="button" className="linkbtn" onClick={() => remove(registrationId)}><Icon name="close" />Remove<span className="vh"> registration {registrationId}</span></button>
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      <div className="row-wrap pick-acts">
        <button type="button" className="linkbtn" onClick={selectAll} disabled={!ordered.length || chosen === ordered.length}>Select all</button>
        <span aria-hidden="true" className="muted">·</span>
        <button type="button" className="linkbtn" onClick={clear} disabled={!selectedRegistrationIds.length}>Clear</button>
      </div>

      <p className="fl-peer-note" id={`${id}-h`}>
        <Icon name="info" />
        <span>
          A monitor placed on these nodes runs as a separate copy on each. Nodes registered here separately aren’t HA peers: each checks and alerts on its own.
          Coordinated alerts need HA peering configured between the nodes.{shortcuts.length ? ' Adding a node group only ticks its nodes; it saves nothing by itself.' : ''}
        </span>
      </p>
      <ErrorText id={`${id}-e`}>{error}</ErrorText>
    </fieldset>
  );
}
