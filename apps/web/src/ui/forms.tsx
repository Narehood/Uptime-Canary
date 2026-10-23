import { forwardRef, useId, useState, type ReactNode } from 'react';
import { ago } from '../format';
import { candidateOrder, outOfView, type NodeColumn } from '../model';
import { ErrorText, nodeTone } from './bits';
import { Glyph, Icon } from './icons';

export type Unit = 'ms' | 's' | 'min' | 'h';
export const UNIT_MS: Record<Unit, number> = { ms: 1, s: 1000, min: 60_000, h: 3_600_000 };
export interface DurationDraft { amount: string; unit: Unit }

export function toDraft(ms: number): DurationDraft {
  if (ms === 0) return { amount: '0', unit: 's' };
  for (const unit of ['h', 'min', 's'] as const) if (ms >= UNIT_MS[unit] && ms % UNIT_MS[unit] === 0) return { amount: String(ms / UNIT_MS[unit]), unit };
  return ms >= 1000 ? { amount: String(ms / 1000), unit: 's' } : { amount: String(ms), unit: 'ms' };
}

export function fromDraft(draft: DurationDraft): number | null {
  const text = draft.amount.trim();
  if (!text) return null;
  const value = Number(text);
  if (!Number.isFinite(value) || value < 0) return null;
  const ms = value * UNIT_MS[draft.unit];
  // The API takes whole milliseconds; reject values like 0.5 ms instead of silently rounding them.
  return Math.abs(ms - Math.round(ms)) < 1e-6 ? Math.round(ms) : null;
}

export function DurationField({ label, value, onChange, error, hint, units = ['ms', 's', 'min', 'h'], disabled }: {
  label: string; value: DurationDraft; onChange: (value: DurationDraft) => void; error?: string; hint?: ReactNode; units?: Unit[]; disabled?: boolean;
}) {
  const id = useId();
  const describedBy = [hint ? `${id}-h` : '', error ? `${id}-e` : ''].filter(Boolean).join(' ') || undefined;
  return (
    <div className="fld">
      <label htmlFor={id}>{label}</label>
      <div className="dur">
        <input
          id={id} type="number" inputMode="decimal" min="0" step="any" value={value.amount} disabled={disabled}
          aria-invalid={error ? true : undefined} aria-describedby={describedBy}
          onChange={event => onChange({ ...value, amount: event.target.value })}
        />
        <select aria-label={`${label} unit`} value={value.unit} disabled={disabled} onChange={event => onChange({ ...value, unit: event.target.value as Unit })}>
          {units.map(unit => <option key={unit} value={unit}>{unit}</option>)}
        </select>
      </div>
      {hint ? <small id={`${id}-h`}>{hint}</small> : null}
      <ErrorText id={`${id}-e`}>{error}</ErrorText>
    </div>
  );
}

export function TextField({ label, value, onChange, error, hint, mono, type = 'text', placeholder, autoFocus, disabled, full, inputMode, autoComplete = 'off', maxLength }: {
  label: string; value: string; onChange: (value: string) => void; error?: string; hint?: ReactNode; mono?: boolean; type?: string;
  placeholder?: string; autoFocus?: boolean; disabled?: boolean; full?: boolean; inputMode?: 'text' | 'url' | 'numeric'; autoComplete?: string; maxLength?: number;
}) {
  const id = useId();
  const describedBy = [hint ? `${id}-h` : '', error ? `${id}-e` : ''].filter(Boolean).join(' ') || undefined;
  return (
    <div className={`fld${full ? ' full' : ''}`}>
      <label htmlFor={id}>{label}</label>
      <input
        id={id} type={type} value={value} placeholder={placeholder} autoFocus={autoFocus} disabled={disabled} className={mono ? 'mono' : undefined}
        aria-invalid={error ? true : undefined} aria-describedby={describedBy} inputMode={inputMode} autoComplete={autoComplete}
        spellCheck={false} autoCapitalize="off" maxLength={maxLength}
        onChange={event => onChange(event.target.value)}
      />
      {hint ? <small id={`${id}-h`}>{hint}</small> : null}
      <ErrorText id={`${id}-e`}>{error}</ErrorText>
    </div>
  );
}

export function NodePicker({ nodes, selected, onChange, endpoint, error, legend, hint, disabled, now }: {
  nodes: NodeColumn[]; selected: string[]; onChange: (ids: string[]) => void; endpoint: string; error?: string; legend: string; hint?: ReactNode; disabled?: boolean; now: number;
}) {
  const id = useId();
  const toggle = (nodeId: string, on: boolean): void => onChange(on ? candidateOrder([...selected, nodeId]) : selected.filter(x => x !== nodeId));
  return (
    <fieldset className="fld full nodepick-f" aria-describedby={[hint ? `${id}-h` : '', error ? `${id}-e` : ''].filter(Boolean).join(' ') || undefined} aria-invalid={error ? true : undefined} disabled={disabled}>
      <legend>{legend} <span className="muted">({selected.length} of {nodes.length})</span></legend>
      <div className="nodepick">
        {nodes.map(node => (
          <label key={node.id} className={outOfView(node) ? 'out' : undefined}>
            <input type="checkbox" checked={selected.includes(node.id)} onChange={event => toggle(node.id, event.target.checked)} />
            <Glyph tone={nodeTone(node)} />
            <span>
              <b>{node.id}</b>
              <span className="nm-sub">
                {node.id === endpoint ? 'endpoint' : node.state === 'connected' ? 'connected' : node.state === 'unlisted' ? 'not a configured peer' : node.lastSeenAt ? `out of view · seen ${ago(node.lastSeenAt, now)}` : 'out of view'}
              </span>
            </span>
          </label>
        ))}
      </div>
      <div className="row-wrap pick-acts">
        <button type="button" className="linkbtn" onClick={() => onChange(candidateOrder(nodes.filter(n => n.state !== 'unlisted').map(n => n.id)))}>Select all</button>
        <span aria-hidden="true" className="muted">·</span>
        <button type="button" className="linkbtn" onClick={() => onChange([endpoint])}>Only {endpoint}</button>
      </div>
      {hint ? <small id={`${id}-h`}>{hint}</small> : null}
      <ErrorText id={`${id}-e`}>{error}</ErrorText>
    </fieldset>
  );
}

/** Nodes try to notify in ID order; the first that gets through records a receipt and the rest stand down. */
export function CandidateOrder({ ids, nodes, sender, label = 'Notification order' }: { ids: string[]; nodes: Map<string, NodeColumn>; sender?: string; label?: string }) {
  const order = candidateOrder(ids);
  if (!order.length) return <span className="muted">Select at least one node</span>;
  return (
    <ol className="order" aria-label={label}>
      {order.map((id, index) => (
        <li key={id} className={`o${outOfView(nodes.get(id)) ? ' out' : ''}${sender === id ? ' sent' : ''}`}>
          <i>{index + 1}</i>{id}
          {sender === id ? <span className="vh"> (sent the latest down alert)</span> : null}
          {outOfView(nodes.get(id)) ? <span className="vh"> (out of view)</span> : null}
        </li>
      ))}
    </ol>
  );
}

export const ErrorSummary = forwardRef<HTMLDivElement, { title: string; errors: string[]; children?: ReactNode }>(function ErrorSummary({ title, errors, children }, ref) {
  if (!errors.length && !children) return null;
  return (
    <div className="errsum" ref={ref} tabIndex={-1} role="alert">
      <Glyph tone="down" />
      <div>
        <b>{title}</b>
        {errors.length ? <ul>{errors.map(error => <li key={error}>{error}</li>)}</ul> : null}
        {children}
      </div>
    </div>
  );
});

/** Password input with show/hide. `autoComplete` tells password managers whether it's the current or a new password. */
export function PasswordField({ label, value, onChange, error, hint, autoComplete, disabled, autoFocus, full, name }: {
  label: string; value: string; onChange: (value: string) => void; error?: string; hint?: ReactNode;
  autoComplete: 'current-password' | 'new-password'; disabled?: boolean; autoFocus?: boolean; full?: boolean; name?: string;
}) {
  const id = useId();
  const [shown, setShown] = useState(false);
  const describedBy = [hint ? `${id}-h` : '', error ? `${id}-e` : ''].filter(Boolean).join(' ') || undefined;
  return (
    <div className={`fld${full ? ' full' : ''}`}>
      <label htmlFor={id}>{label}</label>
      <div className="secret">
        <input
          id={id} name={name} type={shown ? 'text' : 'password'} value={value} autoComplete={autoComplete} spellCheck={false} autoCapitalize="off"
          disabled={disabled} autoFocus={autoFocus} maxLength={512} aria-invalid={error ? true : undefined} aria-describedby={describedBy}
          onChange={event => onChange(event.target.value)}
        />
        <button type="button" className="icon-btn sm" onClick={() => setShown(v => !v)} aria-pressed={shown} aria-label={shown ? `Hide ${label.toLowerCase()}` : `Show ${label.toLowerCase()}`} disabled={disabled}>
          <Icon name={shown ? 'eyeOff' : 'eye'} />
        </button>
      </div>
      {hint ? <small id={`${id}-h`}>{hint}</small> : null}
      <ErrorText id={`${id}-e`}>{error}</ErrorText>
    </div>
  );
}

/** Six-digit authenticator code: numeric keyboard, one-time-code autofill, monospace. */
export function CodeField({ label = 'Authenticator code', value, onChange, error, hint, disabled, autoFocus, full }: {
  label?: string; value: string; onChange: (value: string) => void; error?: string; hint?: ReactNode; disabled?: boolean; autoFocus?: boolean; full?: boolean;
}) {
  const id = useId();
  const describedBy = [hint ? `${id}-h` : '', error ? `${id}-e` : ''].filter(Boolean).join(' ') || undefined;
  return (
    <div className={`fld${full ? ' full' : ''}`}>
      <label htmlFor={id}>{label}</label>
      <input
        id={id} type="text" className="mono code-in" value={value} inputMode="numeric" autoComplete="one-time-code" pattern="[0-9 ]*" maxLength={7}
        disabled={disabled} autoFocus={autoFocus} aria-invalid={error ? true : undefined} aria-describedby={describedBy}
        onChange={event => onChange(event.target.value)}
      />
      {hint ? <small id={`${id}-h`}>{hint}</small> : null}
      <ErrorText id={`${id}-e`}>{error}</ErrorText>
    </div>
  );
}

/** A full-width section that opens on demand. The summary says what's configured while it's closed. */
export function Disclosure({ title, summary, open, onToggle, children }: { title: string; summary?: ReactNode; open: boolean; onToggle: () => void; children: ReactNode }) {
  const id = useId();
  return (
    <div className={`disc full${open ? ' open' : ''}`}>
      <button type="button" className="disc-h" aria-expanded={open} aria-controls={`${id}-b`} onClick={onToggle}>
        <Icon name="chevRight" className="disc-chev" />
        <span className="disc-t">{title}</span>
        {summary ? <span className="disc-s">{summary}</span> : null}
      </button>
      {open ? <div className="disc-b form" id={`${id}-b`}>{children}</div> : null}
    </div>
  );
}

export function TextAreaField({ label, value, onChange, error, hint, mono, rows = 4, placeholder, full, disabled, optional }: {
  label: string; value: string; onChange: (value: string) => void; error?: string; hint?: ReactNode; mono?: boolean; rows?: number;
  placeholder?: string; full?: boolean; disabled?: boolean; optional?: boolean;
}) {
  const id = useId();
  const describedBy = [hint ? `${id}-h` : '', error ? `${id}-e` : ''].filter(Boolean).join(' ') || undefined;
  return (
    <div className={`fld${full ? ' full' : ''}`}>
      <label htmlFor={id}>{label}{optional ? <span className="muted"> (optional)</span> : null}</label>
      <textarea
        id={id} value={value} rows={rows} placeholder={placeholder} disabled={disabled} className={mono ? 'mono' : undefined}
        spellCheck={false} autoCapitalize="off" autoComplete="off" aria-invalid={error ? true : undefined} aria-describedby={describedBy}
        onChange={event => onChange(event.target.value)}
      />
      {hint ? <small id={`${id}-h`}>{hint}</small> : null}
      <ErrorText id={`${id}-e`}>{error}</ErrorText>
    </div>
  );
}

export interface HeaderRow { id: number; name: string; value: string }
let headerRowId = 0;
export const headerRows = (headers: Record<string, string> | undefined): HeaderRow[] =>
  Object.entries(headers ?? {}).map(([name, value]) => ({ id: ++headerRowId, name, value }));

/** Name/value rows. Values often carry API keys, so they're masked until shown. */
export function HeaderEditor({ legend, rows, onChange, error, hint, disabled, noun = 'header', placeholder = 'Header-Name' }: {
  legend: string; rows: HeaderRow[]; onChange: (rows: HeaderRow[]) => void; error?: string; hint?: ReactNode; disabled?: boolean; noun?: string; placeholder?: string;
}) {
  const id = useId();
  const [shown, setShown] = useState(false);
  const update = (rowId: number, change: Partial<HeaderRow>): void => onChange(rows.map(row => row.id === rowId ? { ...row, ...change } : row));
  return (
    <fieldset className="fld full hdr-f" disabled={disabled} aria-describedby={[hint ? `${id}-h` : '', error ? `${id}-e` : ''].filter(Boolean).join(' ') || undefined} aria-invalid={error ? true : undefined}>
      <legend>{legend} <span className="muted">({rows.length || 'none'})</span></legend>
      {rows.length ? (
        <div className="hdr-rows">
          {rows.map((row, index) => (
            <div className="hdr-row" key={row.id}>
              <input type="text" className="mono" value={row.name} placeholder={placeholder} aria-label={`${noun} ${index + 1} name`} spellCheck={false} autoComplete="off" onChange={event => update(row.id, { name: event.target.value })} />
              <input type={shown ? 'text' : 'password'} className="mono" value={row.value} placeholder="value" aria-label={`${noun} ${index + 1} value`} spellCheck={false} autoComplete="off" onChange={event => update(row.id, { value: event.target.value })} />
              <button type="button" className="icon-btn sm" onClick={() => onChange(rows.filter(other => other.id !== row.id))} aria-label={`Remove ${noun} ${row.name || index + 1}`}><Icon name="close" /></button>
            </div>
          ))}
        </div>
      ) : null}
      <div className="row-wrap pick-acts">
        <button type="button" className="linkbtn" onClick={() => onChange([...rows, { id: ++headerRowId, name: '', value: '' }])}><Icon name="plus" />Add {noun}</button>
        {rows.length ? <><span aria-hidden="true" className="muted">·</span><button type="button" className="linkbtn" aria-pressed={shown} onClick={() => setShown(v => !v)}>{shown ? 'Hide values' : 'Show values'}</button></> : null}
      </div>
      {hint ? <small id={`${id}-h`}>{hint}</small> : null}
      <ErrorText id={`${id}-e`}>{error}</ErrorText>
    </fieldset>
  );
}

/**
 * PEM input with progressive disclosure: a stored certificate or key shows as a one-line summary. "Replace" keeps
 * the stored value until new text is pasted, so opening the editor never blanks it; only "Remove" clears it.
 * Private keys are never displayed again; certificates can be viewed.
 */
export function PemField({ label, value, onChange, kind, error, hint, disabled, placeholder }: {
  label: string; value: string; onChange: (value: string) => void; kind: 'certificate' | 'key'; error?: string; hint?: ReactNode; disabled?: boolean; placeholder?: string;
}) {
  const id = useId();
  const [stored] = useState(value);
  const [replacing, setReplacing] = useState(false);
  const [text, setText] = useState('');
  const [viewing, setViewing] = useState(false);
  const describedBy = [hint ? `${id}-h` : '', error ? `${id}-e` : ''].filter(Boolean).join(' ') || undefined;
  if (value && !replacing && (value === stored || !text)) {
    const certificates = (value.match(/-----BEGIN [^-]*CERTIFICATE-----/gu) ?? []).length;
    const size = new TextEncoder().encode(value).length;
    return (
      <div className="fld full">
        <span className="flabel" id={`${id}-l`}>{label}</span>
        <div className="pem-sum" aria-labelledby={`${id}-l`} aria-describedby={describedBy}>
          <Icon name={kind === 'key' ? 'key' : 'lock'} />
          <span className="grow">{kind === 'key' ? 'Private key stored' : `${certificates || 'No'} ${certificates === 1 ? 'certificate' : 'certificates'}`} <span className="muted">· {size < 1024 ? `${size} B` : `${(size / 1024).toFixed(1)} KiB`}{value !== stored ? ' · not saved yet' : ''}</span></span>
          {kind === 'certificate' ? <button type="button" className="linkbtn" aria-pressed={viewing} onClick={() => setViewing(v => !v)} disabled={disabled}>{viewing ? 'Hide' : 'View'}</button> : null}
          <button type="button" className="linkbtn" onClick={() => { setText(''); setReplacing(true); }} disabled={disabled}>Replace</button>
          <button type="button" className="linkbtn" onClick={() => { onChange(''); setText(''); setReplacing(true); }} disabled={disabled}>Remove</button>
        </div>
        {viewing && kind === 'certificate' ? <pre className="pem-view">{value}</pre> : null}
        {hint ? <small id={`${id}-h`}>{hint}</small> : null}
        <ErrorText id={`${id}-e`}>{error}</ErrorText>
      </div>
    );
  }
  const keeping = replacing && Boolean(value) && !text;
  return (
    <div className="fld full">
      <label htmlFor={id}>{label} <span className="muted">(optional)</span></label>
      <textarea
        id={id} className="mono pem" rows={4} value={replacing ? text : value} placeholder={placeholder ?? (kind === 'key' ? '-----BEGIN PRIVATE KEY-----' : '-----BEGIN CERTIFICATE-----')}
        spellCheck={false} autoComplete="off" disabled={disabled} aria-invalid={error ? true : undefined} aria-describedby={describedBy}
        onChange={event => {
          if (replacing) { setText(event.target.value); onChange(event.target.value || (value ? stored : '')); }
          else onChange(event.target.value);
        }}
        onBlur={() => { if ((replacing ? text : value).trim()) { setReplacing(false); setText(''); } }}
      />
      {replacing && stored ? (
        <small>
          {keeping ? 'The stored value stays until you paste a replacement. ' : ''}
          <button type="button" className="linkbtn" onClick={() => { onChange(stored); setText(''); setReplacing(false); }} disabled={disabled}>Keep the stored {kind === 'key' ? 'key' : 'certificates'}</button>
        </small>
      ) : null}
      {!value && stored ? <small className="fwarn"><Glyph tone="warn" />Saving removes the stored {kind === 'key' ? 'key' : 'certificates'}.</small> : null}
      {hint ? <small id={`${id}-h`}>{hint}</small> : null}
      <ErrorText id={`${id}-e`}>{error}</ErrorText>
    </div>
  );
}

/** Password-style input for stored credentials. Prefilled values stay unless edited; clearing one is called out. */
export function SecretField({ label, value, onChange, disabled, full, mono = true, error, hint, placeholder, storedSet, optional }: {
  label: string; value: string; onChange: (value: string) => void; disabled?: boolean; full?: boolean; mono?: boolean; error?: string; hint?: ReactNode;
  placeholder?: string; storedSet?: boolean; optional?: boolean;
}) {
  const id = useId();
  const [shown, setShown] = useState(false);
  const describedBy = [hint ? `${id}-h` : '', error ? `${id}-e` : ''].filter(Boolean).join(' ') || undefined;
  return (
    <div className={`fld${full ? ' full' : ''}`}>
      <label htmlFor={id}>{label}{optional ? <span className="muted"> (optional)</span> : null}</label>
      <div className="secret">
        <input id={id} type={shown ? 'text' : 'password'} className={mono ? 'mono' : undefined} value={value} autoComplete="off" spellCheck={false} autoCapitalize="off" disabled={disabled} placeholder={placeholder}
          aria-invalid={error ? true : undefined} aria-describedby={describedBy} onChange={event => onChange(event.target.value)} />
        <button type="button" className="icon-btn sm" onClick={() => setShown(v => !v)} aria-pressed={shown} aria-label={shown ? `Hide ${label.toLowerCase()}` : `Show ${label.toLowerCase()}`} disabled={disabled}>
          <Icon name={shown ? 'eyeOff' : 'eye'} />
        </button>
      </div>
      {storedSet && !value ? <small className="fwarn"><Glyph tone="warn" />Saving removes the stored value.</small> : null}
      {hint ? <small id={`${id}-h`}>{hint}</small> : null}
      <ErrorText id={`${id}-e`}>{error}</ErrorText>
    </div>
  );
}

/** Segmented single choice built from native radios. */
export function Seg<T extends string>({ legend, name, value, options, onChange, disabled, full, hint }: {
  legend: string; name: string; value: T; options: { value: T; label: string }[]; onChange: (value: T) => void; disabled?: boolean; full?: boolean; hint?: ReactNode;
}) {
  return (
    <fieldset className={`fld seg-f${full ? ' full' : ''}`} disabled={disabled}>
      <legend>{legend}</legend>
      <div className="seg">
        {options.map(option => <label key={option.value}><input type="radio" name={name} value={option.value} checked={value === option.value} onChange={() => onChange(option.value)} /><span>{option.label}</span></label>)}
      </div>
      {hint ? <small>{hint}</small> : null}
    </fieldset>
  );
}

/** Native select with a label, hint and error. */
export function SelectField<T extends string>({ label, value, options, onChange, disabled, full, hint, error }: {
  label: string; value: T; options: { value: T; label: string }[]; onChange: (value: T) => void; disabled?: boolean; full?: boolean; hint?: ReactNode; error?: string;
}) {
  const id = useId();
  const describedBy = [hint ? `${id}-h` : '', error ? `${id}-e` : ''].filter(Boolean).join(' ') || undefined;
  return (
    <div className={`fld${full ? ' full' : ''}`}>
      <label htmlFor={id}>{label}</label>
      <select id={id} value={value} disabled={disabled} aria-invalid={error ? true : undefined} aria-describedby={describedBy} onChange={event => onChange(event.target.value as T)}>
        {options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
      </select>
      {hint ? <small id={`${id}-h`}>{hint}</small> : null}
      <ErrorText id={`${id}-e`}>{error}</ErrorText>
    </div>
  );
}

/** Labelled checkbox with an optional explanation. */
export function CheckField({ label, checked, onChange, hint, disabled, full = true, danger }: {
  label: ReactNode; checked: boolean; onChange: (value: boolean) => void; hint?: ReactNode; disabled?: boolean; full?: boolean; danger?: boolean;
}) {
  return (
    <label className={`check${full ? ' full' : ''}${danger && checked ? ' danger-check' : ''}`}>
      <input type="checkbox" checked={checked} onChange={event => onChange(event.target.checked)} disabled={disabled} />
      <span>{label}{hint ? <small>{hint}</small> : null}</span>
    </label>
  );
}

/** A number with a unit select, for durations measured in minutes, hours or days. */
export function AmountField<U extends string>({ label, amount, unit, units, onChange, error, hint, disabled }: {
  label: string; amount: string; unit: U; units: { value: U; label: string }[]; onChange: (amount: string, unit: U) => void; error?: string; hint?: ReactNode; disabled?: boolean;
}) {
  const id = useId();
  const describedBy = [hint ? `${id}-h` : '', error ? `${id}-e` : ''].filter(Boolean).join(' ') || undefined;
  return (
    <div className="fld">
      <label htmlFor={id}>{label}</label>
      <div className="dur">
        <input id={id} type="number" inputMode="numeric" min="0" step="1" value={amount} disabled={disabled} aria-invalid={error ? true : undefined} aria-describedby={describedBy}
          onChange={event => onChange(event.target.value, unit)} />
        <select aria-label={`${label} unit`} value={unit} disabled={disabled} onChange={event => onChange(amount, event.target.value as U)}>
          {units.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
        </select>
      </div>
      {hint ? <small id={`${id}-h`}>{hint}</small> : null}
      <ErrorText id={`${id}-e`}>{error}</ErrorText>
    </div>
  );
}
