import { useEffect, useId, useMemo, useRef, useState, type FormEvent, type ReactNode } from 'react';
import type { Maintenance, MaintenanceInput, MaintenanceSchedule, MaintenanceView } from '@uptime-canary/shared';
import { ApiFailure, describeFailure } from '../api';
import { useCanary } from '../context';
import { ago, andList, elapsed, longStamp, plural, shortRev, utcStamp } from '../format';
import { notify } from '../notices';
import { href, navigate } from '../router';
import { Banner, Card, Chip, EmptyState, ErrorText, Modal, PageHead, ReadOnly, Rev, Tag, TypeTag } from '../ui/bits';
import { AmountField, CheckField, ErrorSummary, Seg, TextField } from '../ui/forms';
import { Glyph, Icon } from '../ui/icons';
import { groupPath, type Tone } from '../model';

type Strategy = MaintenanceSchedule['strategy'];
const STRATEGIES: { value: Strategy; label: string; hint: string }[] = [
  { value: 'once', label: 'Once', hint: 'One window starting on the start date.' },
  { value: 'interval', label: 'Every N days', hint: 'Every N local calendar days from the start date. DST doesn’t move the wall-clock start.' },
  { value: 'daily', label: 'Daily', hint: 'Every date between the start and end dates.' },
  { value: 'weekly', label: 'Weekly', hint: 'On the chosen weekdays.' },
  { value: 'monthly', label: 'Monthly', hint: 'On chosen days of the month. Missing days (like the 31st in April) are skipped.' },
  { value: 'cron', label: 'Cron', hint: 'Five-field cron in the schedule’s time zone.' },
];
const WEEKDAYS = [
  { n: 1, short: 'Mon', long: 'Monday' }, { n: 2, short: 'Tue', long: 'Tuesday' }, { n: 3, short: 'Wed', long: 'Wednesday' }, { n: 4, short: 'Thu', long: 'Thursday' },
  { n: 5, short: 'Fri', long: 'Friday' }, { n: 6, short: 'Sat', long: 'Saturday' }, { n: 7, short: 'Sun', long: 'Sunday' },
];
const ordinal = (n: number): string => `${n}${n % 10 === 1 && n % 100 !== 11 ? 'st' : n % 10 === 2 && n % 100 !== 12 ? 'nd' : n % 10 === 3 && n % 100 !== 13 ? 'rd' : 'th'}`;
/** Exact configured duration, e.g. "90 min", "2 h 30 min", "2 d 12 h". Never rounded. */
const minutesText = (minutes: number): string => {
  const d = Math.floor(minutes / 1440), h = Math.floor((minutes % 1440) / 60), m = minutes % 60;
  return [d ? `${d} d` : '', h ? `${h} h` : '', m ? `${m} min` : ''].filter(Boolean).join(' ') || '0 min';
};

/** A plain sentence for a schedule. Times are wall-clock in the schedule's own zone, which is always named. */
export function scheduleText(s: MaintenanceSchedule): string {
  const at = s.startTime ? ` at ${s.startTime}` : '';
  const zone = ` ${s.timezone}`;
  const lasting = ` for ${minutesText(s.durationMinutes)}`;
  const range = s.strategy === 'once' ? ` on ${s.startDate}` : `, ${s.startDate} to ${s.endDate}`;
  switch (s.strategy) {
    case 'once': return `Once${range}${at}${zone}${lasting}`;
    case 'interval': return `Every ${plural(s.intervalDays ?? 1, 'day')}${at}${zone}${lasting}${range}`;
    case 'daily': return `Daily${at}${zone}${lasting}${range}`;
    case 'weekly': return `Every ${andList((s.weekdays ?? []).map(n => WEEKDAYS[n - 1]?.long ?? String(n)))}${at}${zone}${lasting}${range}`;
    case 'monthly': {
      const days = [...(s.daysOfMonth ?? []).map(ordinal), ...(s.lastDayOfMonth ? ['last day'] : [])];
      return `Monthly on the ${andList(days)}${at}${zone}${lasting}${range}`;
    }
    case 'cron': return `Cron “${s.cron ?? ''}”${zone}${lasting}${range}`;
  }
}

function status(view: MaintenanceView): { tone: Tone; label: string } {
  if (view.configConflict) return { tone: 'serious', label: 'Conflict · not applied' };
  if (!view.maintenance.enabled) return { tone: 'paused', label: 'Disabled' };
  if (view.active) return { tone: 'warn', label: 'Active · holding alerts' };
  if (view.nextStart) return { tone: 'pending', label: 'Scheduled' };
  return { tone: 'none', label: 'No more windows' };
}

/** An instant shown both ways: explicit UTC (the schedule engine's output) and the viewer's local time. */
function Instant({ iso, now }: { iso: string; now: number }) {
  const ms = Date.parse(iso);
  if (!Number.isFinite(ms)) return <span className="mono">{iso}</span>;
  return (
    <span className="instant">
      <time dateTime={iso} className="mono">{utcStamp(ms)}</time>
      <span className="muted"> · {longStamp(ms)} your time · {ms > now ? `in ${elapsed(ms - now)}` : ago(ms, now)}</span>
    </span>
  );
}

function Selection({ m }: { m: Maintenance }) {
  const { monitorMap, groupMap } = useCanary();
  if (m.allMonitors) return <b>All monitors</b>;
  const monitors = (m.monitorIds ?? []).map(id => monitorMap.get(id)?.monitor.name ?? `missing monitor ${id.slice(0, 8)}`);
  const groups = (m.groupIds ?? []).map(id => groupPath(id, groupMap));
  return (
    <span>
      {monitors.length ? <>{plural(monitors.length, 'monitor')}: {monitors.slice(0, 6).join(', ')}{monitors.length > 6 ? ` +${monitors.length - 6}` : ''}</> : null}
      {monitors.length && groups.length ? <br /> : null}
      {groups.length ? <>{plural(groups.length, 'group')}: {groups.join(', ')} <span className="muted">(their current members)</span></> : null}
    </span>
  );
}

function MaintenanceCard({ view }: { view: MaintenanceView }) {
  const { overview, editable, assessed } = useCanary();
  const [deleting, setDeleting] = useState(false);
  const m = view.maintenance;
  const st = status(view);
  const held = assessed.filter(({ view: monitor }) => monitor.maintenanceIds?.includes(m.id));
  return (
    <article className={`gcard mcard${view.active && m.enabled && !view.configConflict ? ' on' : ''}`} aria-labelledby={`mt-${m.id}`}>
      <div className="gcard-h">
        <div>
          <h2 id={`mt-${m.id}`}>{m.name}</h2>
          <div className="row-wrap"><Chip tone={st.tone} label={st.label} /></div>
        </div>
        {editable ? (
          <div className="acts">
            <a className={`btn sm${view.configConflict ? ' is-disabled' : ''}`} href={view.configConflict ? undefined : href('maintenance', m.id, 'edit')} aria-disabled={view.configConflict || undefined}><Icon name="edit" />Edit</a>
            <button type="button" className="btn sm danger" onClick={() => setDeleting(true)} disabled={view.configConflict} aria-label={`Delete ${m.name}`}><Icon name="trash" /></button>
          </div>
        ) : null}
      </div>
      {view.configConflict ? <Banner tone="warn" title="Configuration conflict" actions={<a className="btn sm" href={href('config')}><Icon name="merge" />Resolve</a>}>Conflicting schedules don’t hold alerts until someone resolves them.</Banner> : null}
      <p className="g-reason">{scheduleText(m.schedule)}</p>
      <dl className="kv">
        {view.active && view.start && view.end ? <><dt>Current window</dt><dd><Instant iso={view.start} now={overview.now} /><br />until <Instant iso={view.end} now={overview.now} /></dd></> : null}
        <dt>Next window</dt><dd>{view.nextStart ? <Instant iso={view.nextStart} now={overview.now} /> : <span className="muted">None after this</span>}</dd>
        <dt>Applies to</dt><dd><Selection m={m} /></dd>
        {view.reason ? <><dt>Node says</dt><dd>{view.reason}</dd></> : null}
        {held.length ? <><dt>Holding now</dt><dd>{held.map(({ view: monitor }) => <a key={monitor.monitor.id} className="nowrap" href={href('monitors', monitor.monitor.id)}>{monitor.monitor.name}</a>).reduce<ReactNode[]>((all, link, index) => index ? [...all, ', ', link] : [link], [])}</dd></> : null}
        <dt>Revision</dt><dd>{view.configConflict ? 'conflicting versions' : <Rev revision={m.revision} />}</dd>
      </dl>
      {deleting ? <DeleteMaintenanceDialog m={m} onClose={() => setDeleting(false)} /> : null}
    </article>
  );
}

export function MaintenanceListView() {
  const { maintenance, editable, endpoint, search } = useCanary();
  if (maintenance === null) {
    return <div className="view"><PageHead title="Maintenance" /><Card><EmptyState title={`${endpoint} doesn’t offer planned maintenance`}>Its overview has no maintenance list, so it probably runs an older Uptime-Canary version.</EmptyState></Card></div>;
  }
  const q = search.trim().toLowerCase();
  const order = (view: MaintenanceView): number => view.configConflict ? 0 : view.active && view.maintenance.enabled ? 1 : view.nextStart && view.maintenance.enabled ? 2 : 3;
  const shown = maintenance.filter(view => !q || view.maintenance.name.toLowerCase().includes(q))
    .sort((a, b) => order(a) - order(b) || (a.nextStart ?? '').localeCompare(b.nextStart ?? '') || a.maintenance.name.localeCompare(b.maintenance.name));
  return (
    <div className="view">
      <PageHead
        title="Maintenance"
        sub={<>Planned windows known to <span className="mono">{endpoint}</span>. Checks, history and incidents continue; new alerts are held.</>}
        actions={editable ? <a className="btn primary" href={href('maintenance', 'new')}><Icon name="plus" />New window</a> : <ReadOnly>Viewers can’t schedule maintenance</ReadOnly>}
      />
      <Card title="How planned maintenance works" hint="It never turns a failing service green">
        <ol className="steps">
          <li><b>Measured state stays honest.</b> A service under maintenance can still be down or unknown; the dashboard shows maintenance beside its result.</li>
          <li><b>Alerts are held.</b> New outage, repeat and recovery notifications wait. Channel tests still send.</li>
          <li><b>After the window.</b> A continuing outage notifies; a destination that heard the outage gets its recovery. A brief outage entirely inside an unchanged window isn’t replayed.</li>
        </ol>
        <p className="foot-note">Each node applies the schedules it knows, with its own clock. During a partition a node that hasn’t learned a new window can still alert. Disabled, deleted or conflicting schedules hold nothing.</p>
      </Card>
      {shown.length ? <div className="gcards">{shown.map(view => <MaintenanceCard key={view.maintenance.id} view={view} />)}</div>
        : <Card><EmptyState title={maintenance.length ? 'Nothing matches' : 'No maintenance scheduled'} action={!maintenance.length && editable ? <a className="btn primary" href={href('maintenance', 'new')}><Icon name="plus" />New window</a> : undefined}>
            {maintenance.length ? 'Try another search.' : 'Schedule a window to hold alerts during planned work.'}
          </EmptyState></Card>}
    </div>
  );
}

function DeleteMaintenanceDialog({ m, onClose }: { m: Maintenance; onClose: () => void }) {
  const { api, endpoint, refresh, stillCurrent } = useCanary();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const remove = async (): Promise<void> => {
    setBusy(true); setError(null);
    try {
      await api.deleteMaintenance(m.id, m.revision);
      if (!stillCurrent()) return;
      notify(`Deleted maintenance “${m.name}” on ${endpoint}. Held alerts are released by the normal rules.`);
      refresh();
      onClose();
    } catch (failure) {
      if (!stillCurrent()) return;
      setError(describeFailure(failure)); setBusy(false);
    }
  };
  return (
    <Modal title={`Delete ${m.name}?`} onClose={onClose} busy={busy}>
      <p>Alerts it holds stop being held. An outage that’s still owed is notified; peers keep the deletion when they sync.</p>
      {error ? <div className="errsum" role="alert"><Glyph tone="down" /><div><b>Not deleted</b><p>{error}</p></div></div> : null}
      <div className="row-wrap modal-acts">
        <button type="button" className="btn danger-solid" onClick={() => { void remove(); }} disabled={busy}><Icon name="trash" />{busy ? 'Deleting…' : 'Delete'}</button>
        <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
      </div>
    </Modal>
  );
}

// ---------- form

type DurationUnit = 'min' | 'h' | 'd';
interface MDraft {
  name: string; enabled: boolean; scope: 'all' | 'some'; monitorIds: string[]; groupIds: string[];
  strategy: Strategy; timezone: string; startDate: string; endDate: string; startTime: string;
  duration: string; durationUnit: DurationUnit; intervalDays: string; weekdays: number[]; daysOfMonth: number[]; lastDay: boolean; cron: string;
}
const UNIT_MIN: Record<DurationUnit, number> = { min: 1, h: 60, d: 1440 };
const localZone = (): string => { try { return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'; } catch { return 'UTC'; } };
const isoDate = (date: Date): string => `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;

function mDraft(m: Maintenance | undefined): MDraft {
  const s = m?.schedule;
  const today = new Date();
  const year = new Date(today); year.setFullYear(today.getFullYear() + 1); year.setDate(year.getDate() - 1);
  const minutes = s?.durationMinutes ?? 60;
  const unit: DurationUnit = minutes % 1440 === 0 ? 'd' : minutes % 60 === 0 ? 'h' : 'min';
  return {
    name: m?.name ?? '', enabled: m?.enabled ?? true,
    scope: m ? (m.allMonitors ? 'all' : 'some') : 'some', monitorIds: [...(m?.monitorIds ?? [])], groupIds: [...(m?.groupIds ?? [])],
    strategy: s?.strategy ?? 'weekly', timezone: s?.timezone ?? localZone(), startDate: s?.startDate ?? isoDate(today), endDate: s?.endDate ?? isoDate(year),
    startTime: s?.startTime ?? '02:00', duration: String(minutes / UNIT_MIN[unit]), durationUnit: unit, intervalDays: String(s?.intervalDays ?? 7),
    weekdays: [...(s?.weekdays ?? [7])], daysOfMonth: [...(s?.daysOfMonth ?? (s ? [] : [1]))], lastDay: Boolean(s?.lastDayOfMonth), cron: s?.cron ?? '0 2 * * SUN',
  };
}

type MField = 'name' | 'selection' | 'timezone' | 'dates' | 'startTime' | 'duration' | 'intervalDays' | 'weekdays' | 'days' | 'cron';
const MFIELD_LABEL: Record<MField, string> = { name: 'Name', selection: 'Applies to', timezone: 'Time zone', dates: 'Dates', startTime: 'Start time', duration: 'Duration', intervalDays: 'Repeat every', weekdays: 'Weekdays', days: 'Days of the month', cron: 'Cron expression' };
const DATE = /^\d{4}-\d{2}-\d{2}$/u;

function buildMaintenance(d: MDraft): { input?: MaintenanceInput; errors: Partial<Record<MField, string>> } {
  const errors: Partial<Record<MField, string>> = {};
  const name = d.name.trim();
  if (!name || name.length > 128 || /[\x00-\x1f\x7f]/u.test(name)) errors.name = 'Enter a name of at most 128 characters.';
  if (d.scope === 'some' && !d.monitorIds.length && !d.groupIds.length) errors.selection = 'Choose at least one monitor or group, or all monitors.';
  const timezone = d.timezone.trim();
  if (!timezone || timezone === 'Local' || /\s/u.test(timezone)) errors.timezone = 'Enter an IANA time zone such as Europe/Berlin, or UTC.';
  const start = Date.parse(`${d.startDate}T00:00:00Z`);
  const end = Date.parse(`${d.endDate}T00:00:00Z`);
  if (!DATE.test(d.startDate) || !DATE.test(d.endDate) || !Number.isFinite(start) || !Number.isFinite(end)) errors.dates = 'Enter both dates.';
  else if (end < start) errors.dates = 'The end date can’t be before the start date.';
  else if ((end - start) / 86_400_000 + 1 > 3660) errors.dates = 'Use at most 3,660 dates (about ten years).';
  if (d.strategy !== 'cron' && !/^([01]\d|2[0-3]):[0-5]\d$/u.test(d.startTime)) errors.startTime = 'Enter a start time like 02:00.';
  const duration = Number(d.duration) * UNIT_MIN[d.durationUnit];
  if (!Number.isInteger(duration) || duration < 1 || duration > 44_640) errors.duration = 'Use 1 minute to 31 days, in whole minutes.';
  const schedule: MaintenanceSchedule = { strategy: d.strategy, timezone, startDate: d.startDate, endDate: d.strategy === 'once' ? d.endDate : d.endDate, durationMinutes: duration };
  if (d.strategy !== 'cron') schedule.startTime = d.startTime;
  if (d.strategy === 'interval') {
    const n = Number(d.intervalDays);
    if (!Number.isInteger(n) || n < 1 || n > 3660) errors.intervalDays = 'Use 1 to 3,660 days.';
    schedule.intervalDays = n;
  }
  if (d.strategy === 'weekly') {
    if (!d.weekdays.length) errors.weekdays = 'Choose at least one weekday.';
    schedule.weekdays = [...d.weekdays].sort((a, b) => a - b);
  }
  if (d.strategy === 'monthly') {
    if (!d.daysOfMonth.length && !d.lastDay) errors.days = 'Choose at least one day, or the last day of the month.';
    if (d.daysOfMonth.length) schedule.daysOfMonth = [...d.daysOfMonth].sort((a, b) => a - b);
    if (d.lastDay) schedule.lastDayOfMonth = true;
  }
  if (d.strategy === 'cron') {
    const text = d.cron.trim().replace(/\s+/gu, ' ');
    if (text.split(' ').length !== 5) errors.cron = 'Use five fields: minute hour day-of-month month weekday.';
    else if (text.length > 256 || /[^\x20-\x7e]/u.test(text)) errors.cron = 'Use at most 256 printable characters.';
    schedule.cron = text;
  }
  if (Object.keys(errors).length) return { errors };
  const input: MaintenanceInput = { name, enabled: d.enabled, schedule };
  if (d.scope === 'all') input.allMonitors = true;
  else {
    if (d.monitorIds.length) input.monitorIds = [...d.monitorIds];
    if (d.groupIds.length) input.groupIds = [...d.groupIds];
  }
  return { input, errors };
}

function PickList({ legend, items, selected, onChange, disabled }: { legend: string; items: { id: string; label: ReactNode; text: string }[]; selected: string[]; onChange: (ids: string[]) => void; disabled: boolean }) {
  const [filter, setFilter] = useState('');
  const id = useId();
  const q = filter.trim().toLowerCase();
  const shown = items.filter(item => !q || item.text.toLowerCase().includes(q));
  const missing = selected.filter(selectedId => !items.some(item => item.id === selectedId));
  return (
    <fieldset className="fld picklist-f" disabled={disabled}>
      <legend>{legend} <span className="muted">({selected.length} chosen)</span></legend>
      {items.length > 8 ? <input type="search" aria-label={`Filter ${legend.toLowerCase()}`} placeholder="Filter" value={filter} onChange={event => setFilter(event.target.value)} id={id} /> : null}
      <div className="picklist">
        {shown.map(item => (
          <label key={item.id}>
            <input type="checkbox" checked={selected.includes(item.id)} onChange={event => onChange(event.target.checked ? [...selected, item.id] : selected.filter(other => other !== item.id))} />
            <span>{item.label}</span>
          </label>
        ))}
        {missing.map(missingId => (
          <label key={missingId} className="off">
            <input type="checkbox" checked onChange={() => onChange(selected.filter(other => other !== missingId))} />
            <span className="mono">missing {missingId.slice(0, 8)}</span>
          </label>
        ))}
        {!items.length && !missing.length ? <span className="muted small">None yet.</span> : null}
      </div>
    </fieldset>
  );
}

/**
 * The edit form mounts only once the stored item is known, so its draft, initial values and base revision come from
 * that item. A route opened before the overview caught up shows "not found" and then mounts fresh, never a blank draft.
 */
export function MaintenanceFormView({ id }: { id?: string }) {
  const { maintenanceMap } = useCanary();
  const existing = id ? maintenanceMap.get(id) : undefined;
  return <MaintenanceFormBody key={existing ? `edit-${existing.maintenance.id}` : id ? 'missing' : 'new'} id={id} />;
}

function MaintenanceFormBody({ id }: { id?: string }) {
  const { maintenance, maintenanceMap, endpoint, editable, api, refresh, assessed, groups, groupMap, stillCurrent } = useCanary();
  const existing = id ? maintenanceMap.get(id) : undefined;
  const [draft, setDraft] = useState<MDraft>(() => mDraft(existing?.maintenance));
  const [baseRevision, setBaseRevision] = useState(existing?.maintenance.revision);
  const [showErrors, setShowErrors] = useState(false);
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<{ message: string; stale: boolean } | null>(null);
  const [attempts, setAttempts] = useState(0);
  const summaryRef = useRef<HTMLDivElement>(null);
  const formId = useId();
  const zones = useMemo(() => { try { return ['UTC', ...Intl.supportedValuesOf('timeZone')]; } catch { return ['UTC']; } }, []);
  useEffect(() => { if (attempts) summaryRef.current?.focus(); }, [attempts, failure]);

  if (maintenance === null) return <div className="view"><PageHead title="Maintenance" /><Card><EmptyState title={`${endpoint} doesn’t offer planned maintenance`}>It probably runs an older Uptime-Canary version.</EmptyState></Card></div>;
  if (!editable) return <div className="view"><PageHead title={id ? 'Edit maintenance' : 'New maintenance'} /><Card><EmptyState title="Viewers can’t schedule maintenance">Sign in with an editor or admin account.</EmptyState></Card></div>;
  if (id && !existing) return <div className="view"><PageHead title="Edit maintenance" /><Card><EmptyState title="Maintenance not found">{endpoint} doesn’t know this schedule. It may have been deleted.</EmptyState></Card></div>;

  const current = existing?.maintenance;
  const { input, errors } = buildMaintenance(draft);
  const visible = showErrors ? errors : {};
  const set = <K extends keyof MDraft>(key: K, value: MDraft[K]): void => setDraft(d => ({ ...d, [key]: value }));
  const changedElsewhere = Boolean(current && baseRevision && current.revision !== baseRevision);
  const strategy = STRATEGIES.find(entry => entry.value === draft.strategy)!;

  const submit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (saving) return;
    setShowErrors(true); setFailure(null);
    if (!input) { setAttempts(n => n + 1); return; }
    setSaving(true);
    try {
      const saved = current && baseRevision ? await api.updateMaintenance(current.id, { ...input, revision: baseRevision }) : await api.createMaintenance(input);
      if (!stillCurrent()) return;
      notify(`${current ? 'Saved' : 'Scheduled'} “${saved.name}” on ${endpoint} (revision ${shortRev(saved.revision)}). The node computed its windows; see the next window on the list.`);
      refresh();
      navigate(href('maintenance'));
    } catch (error) {
      if (!stillCurrent()) return;
      const stale = error instanceof ApiFailure && error.status === 409;
      const busy = error instanceof ApiFailure && error.status === 503;
      setFailure({ message: stale ? 'Someone saved a newer revision first, or nodes now hold conflicting versions.' : busy ? `${endpoint} is busy compiling another schedule. Try again in a moment.` : describeFailure(error, { preferDetail: true }), stale });
      setAttempts(n => n + 1);
    } finally { setSaving(false); }
  };
  const loadLatest = (): void => { if (current) { setDraft(mDraft(current)); setBaseRevision(current.revision); setFailure(null); setShowErrors(false); } };
  const errorList = Object.entries(visible).map(([field, message]) => `${MFIELD_LABEL[field as MField]}: ${message}`);
  const preview = input ? scheduleText(input.schedule) : null;

  return (
    <div className="view">
      <PageHead
        crumbs={<a href={href('maintenance')}>Maintenance</a>}
        title={current ? `Edit ${current.name}` : 'New maintenance window'}
        sub={current ? <>Editing revision <Rev revision={baseRevision ?? current.revision} />. Saving writes a new revision on <span className="mono">{endpoint}</span>.</> : <>Creates a schedule on <span className="mono">{endpoint}</span>, shared with every peer.</>}
      />
      {existing?.configConflict ? <Banner title="This schedule has a configuration conflict" actions={<a className="btn sm" href={href('config')}><Icon name="merge" />Resolve in Configuration</a>}>Edits are rejected until the conflict is resolved.</Banner>
        : changedElsewhere ? <Banner title={<>This schedule changed on {endpoint} while you were editing</>} actions={<button type="button" className="btn sm" onClick={loadLatest}><Icon name="refresh" />Load latest (discards your edits)</button>}>Saving your version will be rejected because it’s based on an older revision.</Banner>
        : null}
      <Card>
        <form className="form" onSubmit={event => { void submit(event); }} noValidate aria-busy={saving}>
          <ErrorSummary ref={summaryRef} title={failure ? (failure.stale ? 'Not saved: the schedule changed or has a conflict' : 'Not saved') : 'Check these fields'} errors={failure ? [] : errorList}>
            {failure ? <p>{failure.message}{failure.stale && current ? <> <button type="button" className="linkbtn" onClick={loadLatest}>Load the latest version</button> and apply your change again.</> : null}</p> : null}
          </ErrorSummary>
          <h3 className="fsec full">Window</h3>
          <TextField label="Name" value={draft.name} onChange={value => set('name', value)} error={visible.name} hint="Private: never shown on public status pages" maxLength={128} autoFocus={!current} disabled={saving} />
          <CheckField full={false} label="Enabled" checked={draft.enabled} onChange={value => set('enabled', value)} disabled={saving} hint="Disabled schedules hold nothing." />

          <h3 className="fsec full">Applies to</h3>
          <Seg legend="Monitors" name={`${formId}-scope`} value={draft.scope} onChange={value => set('scope', value)} disabled={saving} full
            options={[{ value: 'some', label: 'Chosen monitors and groups' }, { value: 'all', label: 'All monitors' }]}
            hint={draft.scope === 'all' ? 'Every monitor, including ones created later.' : 'Groups apply to their current members, so a monitor joining a group joins its maintenance. Nested groups aren’t included: choose each group whose monitors it covers.'} />
          {draft.scope === 'some' ? (
            <>
              <PickList legend="Monitors" disabled={saving} selected={draft.monitorIds} onChange={ids => set('monitorIds', ids)}
                items={assessed.map(({ view, a }) => ({ id: view.monitor.id, text: `${view.monitor.name} ${view.monitor.target}`, label: <><Glyph tone={a.tone} />{view.monitor.name}<TypeTag type={view.monitor.type} /></> }))} />
              {groups !== null ? (
                <PickList legend="Groups" disabled={saving} selected={draft.groupIds} onChange={ids => set('groupIds', ids)}
                  items={groups.map(view => ({ id: view.group.id, text: groupPath(view.group.id, groupMap), label: <><Icon name="groups" className="inline" />{groupPath(view.group.id, groupMap)}</> }))} />
              ) : null}
              {visible.selection ? <div className="full"><ErrorText id={`${formId}-sel`}>{visible.selection}</ErrorText></div> : null}
            </>
          ) : null}

          <h3 className="fsec full">Schedule</h3>
          <fieldset className="fld full seg-f" disabled={saving}>
            <legend>Repeats</legend>
            <div className="seg">
              {STRATEGIES.map(option => <label key={option.value}><input type="radio" name={`${formId}-strategy`} value={option.value} checked={draft.strategy === option.value} onChange={() => set('strategy', option.value)} /><span>{option.label}</span></label>)}
            </div>
            <small>{strategy.hint}</small>
          </fieldset>
          <div className="fld">
            <label htmlFor={`${formId}-tz`}>Time zone</label>
            <input id={`${formId}-tz`} type="text" list={`${formId}-zones`} className="mono" value={draft.timezone} onChange={event => set('timezone', event.target.value)} disabled={saving} autoComplete="off" spellCheck={false}
              aria-invalid={visible.timezone ? true : undefined} aria-describedby={`${formId}-tz-h${visible.timezone ? ` ${formId}-tz-e` : ''}`} />
            <datalist id={`${formId}-zones`}>{zones.map(zone => <option key={zone} value={zone} />)}</datalist>
            <small id={`${formId}-tz-h`}>Dates and times below are wall-clock in this zone. The node checks the name and handles daylight saving: skipped start times are skipped, repeated ones run once.</small>
            <ErrorText id={`${formId}-tz-e`}>{visible.timezone}</ErrorText>
          </div>
          {draft.strategy !== 'cron' ? (
            <div className="fld">
              <label htmlFor={`${formId}-time`}>Start time</label>
              <input id={`${formId}-time`} type="time" value={draft.startTime} onChange={event => set('startTime', event.target.value)} disabled={saving} aria-invalid={visible.startTime ? true : undefined} />
              <small>In {draft.timezone || 'the chosen zone'}.</small>
              <ErrorText id={`${formId}-time-e`}>{visible.startTime}</ErrorText>
            </div>
          ) : (
            <TextField mono label="Cron expression" value={draft.cron} onChange={value => set('cron', value)} error={visible.cron} disabled={saving} placeholder="0 2 * * SUN"
              hint={<>minute hour day month weekday. Lists, ranges, steps, MON–SUN, JAN–DEC and <span className="mono">L</span> (last day) work. No seconds, <span className="mono">@hourly</span>, <span className="mono">?</span>, <span className="mono">W</span> or <span className="mono">#</span>.</>} />
          )}
          <div className="fld">
            <label htmlFor={`${formId}-from`}>{draft.strategy === 'once' ? 'Date' : 'First date'}</label>
            <input id={`${formId}-from`} type="date" value={draft.startDate} onChange={event => set('startDate', event.target.value)} disabled={saving} aria-invalid={visible.dates ? true : undefined} />
          </div>
          <div className="fld">
            <label htmlFor={`${formId}-to`}>{draft.strategy === 'once' ? 'Allow the window to run until' : 'Last date'}</label>
            <input id={`${formId}-to`} type="date" value={draft.endDate} onChange={event => set('endDate', event.target.value)} disabled={saving} aria-invalid={visible.dates ? true : undefined} />
            <small>{draft.strategy === 'once' ? 'Windows are clipped at midnight after this date; extend it for overnight work.' : 'Inclusive. Windows end no later than midnight after it.'}</small>
            <ErrorText id={`${formId}-dates-e`}>{visible.dates}</ErrorText>
          </div>
          <AmountField label="Duration" amount={draft.duration} unit={draft.durationUnit} units={[{ value: 'min', label: 'minutes' }, { value: 'h', label: 'hours' }, { value: 'd', label: 'days' }]}
            onChange={(amount, unit) => setDraft(d => ({ ...d, duration: amount, durationUnit: unit }))} error={visible.duration} disabled={saving} hint="Elapsed time, even across a DST change. Touching windows merge." />
          {draft.strategy === 'interval' ? (
            <TextField label="Repeat every (days)" value={draft.intervalDays} onChange={value => set('intervalDays', value)} error={visible.intervalDays} inputMode="numeric" disabled={saving} hint="Counted in local calendar days from the first date." />
          ) : null}
          {draft.strategy === 'weekly' ? (
            <fieldset className="fld full" disabled={saving} aria-invalid={visible.weekdays ? true : undefined}>
              <legend>Weekdays</legend>
              <div className="daypick">
                {WEEKDAYS.map(day => (
                  <label key={day.n}><input type="checkbox" checked={draft.weekdays.includes(day.n)} onChange={event => set('weekdays', event.target.checked ? [...draft.weekdays, day.n] : draft.weekdays.filter(n => n !== day.n))} /><span title={day.long}>{day.short}</span></label>
                ))}
              </div>
              <ErrorText id={`${formId}-wd-e`}>{visible.weekdays}</ErrorText>
            </fieldset>
          ) : null}
          {draft.strategy === 'monthly' ? (
            <fieldset className="fld full" disabled={saving} aria-invalid={visible.days ? true : undefined}>
              <legend>Days of the month</legend>
              <div className="daypick month">
                {Array.from({ length: 31 }, (_, index) => index + 1).map(day => (
                  <label key={day}><input type="checkbox" checked={draft.daysOfMonth.includes(day)} onChange={event => set('daysOfMonth', event.target.checked ? [...draft.daysOfMonth, day] : draft.daysOfMonth.filter(n => n !== day))} /><span>{day}</span></label>
                ))}
              </div>
              <CheckField label="The last day of each month" checked={draft.lastDay} onChange={value => set('lastDay', value)} disabled={saving} hint="Combined with the days above. Months without a chosen day are skipped, not moved." />
              <ErrorText id={`${formId}-dom-e`}>{visible.days}</ErrorText>
            </fieldset>
          ) : null}
          <div className="full sched-preview" role="status">
            <Icon name="calendar" />
            <span>{preview ? <><b>{preview}.</b> After saving, the list shows the node’s computed current and next window in UTC and your time.</> : 'Complete the schedule to see it in words.'}</span>
          </div>
          <div className="full form-acts">
            <button type="submit" className="btn primary" disabled={saving || existing?.configConflict}><Icon name={current ? 'check' : 'plus'} />{saving ? `Saving to ${endpoint}…` : current ? 'Save changes' : 'Schedule window'}</button>
            <a className="btn" href={href('maintenance')}>Cancel</a>
            {saving ? <span className="muted small" role="status">Waiting for {endpoint} to validate and acknowledge…</span> : null}
          </div>
        </form>
      </Card>
    </div>
  );
}

/** Small tag for monitors currently inside an applicable window. */
export function MaintenanceTag({ ids }: { ids?: string[] }) {
  const { maintenanceMap } = useCanary();
  if (!ids?.length) return null;
  const names = ids.map(id => maintenanceMap.get(id)?.maintenance.name).filter(Boolean) as string[];
  return <Tag icon="wrench" tone="hold">{names.length === 1 ? `maintenance: ${names[0]}` : 'maintenance'}</Tag>;
}
