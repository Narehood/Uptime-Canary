import { useState } from 'react';
import type { Monitor } from '@uptime-canary/shared';
import { CHECK_TYPES, typeLabel } from '../checks';
import { useCanary } from '../context';
import { maskedUrl, plural, span } from '../format';
import { matchesFilter, matchesSearch, type StateFilter } from '../model';
import { href, navigate } from '../router';
import { Card, Dots, EmptyState, FilterChips, PageHead, ReadOnly, Rev, StateChip, Tag, TypeTag } from '../ui/bits';
import { Glyph, Icon } from '../ui/icons';
import { MonitorTags } from '../ui/tags';
import { MaintenanceTag } from './Maintenance';

function Destinations({ m }: { m: Monitor }) {
  const { role, channels } = useCanary();
  if (role === 'viewer' && !m.notificationIds?.length) return <span className="muted">Hidden from viewers</span>;
  const names = (m.notificationIds ?? []).map(id => channels.map.get(id)?.channel.name ?? 'unknown channel');
  const parts: string[] = [];
  if (names.length) parts.push(names.length <= 2 ? names.join(', ') : `${names.slice(0, 2).join(', ')} +${names.length - 2}`);
  if (m.webhookUrl && role !== 'viewer') parts.push(`webhook ${maskedUrl(m.webhookUrl)}`);
  return parts.length ? <span className="small">{parts.join(' · ')}</span> : <span className="muted">Records only</span>;
}

export function MonitorsView() {
  const { assessed, editable, search, groupMap, tags } = useCanary();
  const [filter, setFilter] = useState<StateFilter>('all');
  const [type, setType] = useState('');
  const [tagId, setTagId] = useState('');
  const searched = assessed.filter(({ view }) => matchesSearch(view, search, groupMap.get(view.monitor.groupId ?? '')?.group.name, view.monitor.tags?.map(tag => `${tags.map.get(tag.tagId)?.tag.name ?? ''} ${tag.value ?? ''}`).join(' '))
    && (!type || view.monitor.type === type) && (!tagId || view.monitor.tags?.some(tag => tag.tagId === tagId)));
  const rows = searched.filter(({ a }) => matchesFilter(a, filter));
  const count = (f: StateFilter): number => searched.filter(({ a }) => matchesFilter(a, f)).length;
  const types = CHECK_TYPES.filter(entry => assessed.some(({ view }) => view.monitor.type === entry.type));
  const tagList = (tags.list ?? []).filter(view => !view.tag.deleted).sort((a, b) => a.tag.name.localeCompare(b.tag.name));
  return (
    <div className="view">
      <PageHead
        title="Monitors"
        sub={`${plural(assessed.length, 'monitor')} · 24 check types · placement per monitor`}
        actions={<>
          {!tags.unsupported ? <a className="btn" href={href('tags')}><Icon name="config" />Tags</a> : null}
          {editable ? <a className="btn primary" href={href('monitors', 'new')}><Icon name="plus" />New monitor</a> : <ReadOnly>Viewers can’t edit monitors</ReadOnly>}
        </>}
      />
      <Card>
        <div className="filterbar">
          <FilterChips<StateFilter> label="Filter monitors by state" value={filter} onChange={setFilter} options={[
            { value: 'all', label: 'All', count: count('all') },
            { value: 'failing', label: 'Failing', count: count('failing') },
            { value: 'unknown', label: 'Unknown or pending', count: count('unknown') },
            { value: 'up', label: 'Up', count: count('up') },
            { value: 'paused', label: 'Paused', count: count('paused') },
          ]} />
          <span className="grow" />
          {types.length > 1 ? (
            <label className="inline-sel">Type
              <select value={type} onChange={event => setType(event.target.value)}>
                <option value="">All types</option>
                {types.map(entry => <option key={entry.type} value={entry.type}>{entry.label}</option>)}
              </select>
            </label>
          ) : null}
          {tagList.length ? (
            <label className="inline-sel">Tag
              <select value={tagId} onChange={event => setTagId(event.target.value)}>
                <option value="">All tags</option>
                {tagList.map(view => <option key={view.tag.id} value={view.tag.id}>{view.tag.name}</option>)}
              </select>
            </label>
          ) : null}
        </div>
        {rows.length ? (
          <div className="tbl-wrap">
            <table className="tbl stack">
              <caption className="vh">Monitors</caption>
              <thead>
                <tr><th scope="col">Monitor</th><th scope="col">State</th><th scope="col">Locations</th><th scope="col">Schedule</th><th scope="col">Group</th><th scope="col">Revision</th><th scope="col">Notifies</th></tr>
              </thead>
              <tbody>
                {rows.map(({ view, a }) => {
                  const m = view.monitor;
                  const group = m.groupId ? groupMap.get(m.groupId) : undefined;
                  return (
                    <tr key={m.id} className="rowlink" onClick={event => { if (!(event.target as HTMLElement).closest('a, button')) navigate(href('monitors', m.id)); }}>
                      <td className="lead" data-label="Monitor">
                        <a href={href('monitors', m.id)}>{m.name}</a><TypeTag type={m.type} />
                        <div className="mono muted tgt" title={typeLabel(m.type)}>{m.target}</div>
                        <MonitorTags tags={m.tags} catalog={tags.map} limit={4} />
                      </td>
                      <td data-label="State">
                        <StateChip a={a} />
                        {view.suppressedByGroupId || view.maintenanceIds?.length ? (
                          <div className="hold-line">
                            {view.suppressedByGroupId ? <Tag icon="groups" tone="hold">alerts held</Tag> : null}
                            <MaintenanceTag ids={view.maintenanceIds} />
                          </div>
                        ) : null}
                      </td>
                      <td data-label="Locations"><Dots view={view} a={a} /> <span className="muted">{plural(m.nodeIds.length, 'node')}</span></td>
                      <td data-label="Schedule" className="tn">every {span(m.intervalMs)}<span className="muted"> · timeout {span(m.timeoutMs)}{m.retries ? ` · ${m.retries} ${m.retries === 1 ? 'retry' : 'retries'}` : ''}</span></td>
                      <td data-label="Group" className={m.groupId ? undefined : 'nil'}>{m.groupId ? (group ? group.group.name : <span className="muted">missing group</span>) : <span className="muted">—</span>}</td>
                      <td data-label="Revision">{view.configConflict ? <a href={href('config')} className="nowrap"><Glyph tone="serious" /> conflict</a> : <Rev revision={m.revision} />}</td>
                      <td data-label="Notifies">
                        <Destinations m={m} />
                        {m.repeatAfterMs ? <span className="muted"> · repeats every {span(m.repeatAfterMs)}</span> : null}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : (
          <EmptyState
            title={assessed.length ? 'Nothing matches' : 'No monitors yet'}
            action={!assessed.length && editable ? <a className="btn primary" href={href('monitors', 'new')}><Icon name="plus" />New monitor</a> : undefined}
          >
            {assessed.length ? 'Try another filter or search.' : 'Add a check, choose which nodes run it, and pick where alerts go.'}
          </EmptyState>
        )}
      </Card>
    </div>
  );
}
