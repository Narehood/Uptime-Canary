import { useId, useRef, useState, type ReactNode } from 'react';
import {
  JSONATA_INPUT, JSONATA_OPERATORS, OPERATORS, SECTION_TITLE, SERVICE_FIELDS, compareKinds, fieldVisible, jsonataCaveat, jsonataKinds,
  type CompareAs, type FieldSection, type JsonataAs, type JsonataDraft, type JsonataOperator, type PairRow, type ProtocolKey, type ServiceField, type Values,
} from '../serviceSpecs';
import { ErrorText } from '../ui/bits';
import { CheckField, Disclosure, HeaderEditor, PemField, SecretField, Seg, SelectField, TextAreaField, TextField } from '../ui/forms';
import { Glyph, Icon } from '../ui/icons';

const COMPARE_LABEL: Record<CompareAs, string> = { text: 'text', number: 'number', true: 'true', false: 'false', null: 'null' };

function ConditionField({ field, values, set, error, disabled }: { field: ServiceField; values: Values; set: (key: string, value: string) => void; error?: string; disabled?: boolean }) {
  const id = useId();
  const operator = String(values[`${field.key}.operator`] ?? '');
  const kinds = compareKinds(operator);
  const chosen = values[`${field.key}.as`] as CompareAs;
  const as = kinds.includes(chosen) ? chosen : kinds[0]!;
  const needsValue = as === 'text' || as === 'number';
  return (
    <fieldset className="fld full cond-f" disabled={disabled} aria-describedby={[field.hint ? `${id}-h` : '', error ? `${id}-e` : ''].filter(Boolean).join(' ') || undefined}>
      <legend>{field.label}</legend>
      <div className="cond">
        <select aria-label={`${field.label}: comparison`} value={operator} onChange={event => set(`${field.key}.operator`, event.target.value)}>
          {OPERATORS.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
        </select>
        {operator && kinds.length > 1 ? (
          <select aria-label={`${field.label}: compare as`} value={as} onChange={event => set(`${field.key}.as`, event.target.value)}>
            {kinds.map(kind => <option key={kind} value={kind}>{kind === 'text' || kind === 'number' ? `a ${COMPARE_LABEL[kind]}` : COMPARE_LABEL[kind]}</option>)}
          </select>
        ) : null}
        {operator && needsValue ? (
          <input type="text" className="mono" aria-label={`${field.label}: expected value`} value={String(values[`${field.key}.expected`] ?? '')} inputMode={as === 'number' ? 'decimal' : 'text'}
            placeholder={as === 'number' ? '0' : 'ok'} aria-invalid={error ? true : undefined} onChange={event => set(`${field.key}.expected`, event.target.value)} />
        ) : null}
      </div>
      {field.hint ? <small id={`${id}-h`}>{field.hint}</small> : null}
      <ErrorText id={`${id}-e`}>{error}</ErrorText>
    </fieldset>
  );
}

function Base64Field({ field, value, onChange, error, disabled }: { field: ServiceField; value: string; onChange: (value: string) => void; error?: string; disabled?: boolean }) {
  const input = useRef<HTMLInputElement>(null);
  const [loaded, setLoaded] = useState<string | null>(null);
  const load = async (file: File): Promise<void> => {
    if (file.size > 65_536) { setLoaded(`${file.name} is larger than 64 KiB, the node’s limit for descriptor sets.`); return; }
    const buffer = new Uint8Array(await file.arrayBuffer());
    let binary = '';
    for (const byte of buffer) binary += String.fromCharCode(byte);
    onChange(btoa(binary));
    setLoaded(`Loaded ${file.name} (${buffer.length.toLocaleString()} bytes).`);
  };
  return (
    <div className="full b64">
      <TextAreaField full mono label={field.label} value={value} onChange={onChange} error={error} rows={3} disabled={disabled} hint={field.hint} />
      <div className="row-wrap">
        <input ref={input} type="file" className="vh" tabIndex={-1} aria-hidden="true" onChange={event => { const file = event.target.files?.[0]; if (file) void load(file); event.target.value = ''; }} />
        <button type="button" className="btn sm" onClick={() => input.current?.click()} disabled={disabled}><Icon name="upload" />Load descriptor file</button>
        {loaded ? <span className="muted small" role="status">{loaded}</span> : null}
      </div>
    </div>
  );
}

function FieldInput({ field, values, set, errors, disabled, stored }: {
  field: ServiceField; values: Values; set: (key: string, value: Values[string]) => void; errors: Record<string, string>; disabled?: boolean; stored: Values;
}) {
  const id = useId();
  const value = values[field.key];
  const text = typeof value === 'string' ? value : '';
  const error = errors[field.key];
  const optional = !field.required;
  switch (field.kind) {
    case 'bool':
      return <CheckField label={field.label} checked={value === true} onChange={on => set(field.key, on)} hint={field.hint} disabled={disabled} danger={field.danger} />;
    case 'select':
      return (field.options?.length ?? 0) <= 3
        ? <Seg legend={field.label} name={`${id}-${field.key}`} value={text} options={field.options ?? []} onChange={next => set(field.key, next)} disabled={disabled} hint={field.hint} full={field.full} />
        : <SelectField label={field.label} value={text} options={field.options ?? []} onChange={next => set(field.key, next)} disabled={disabled} hint={field.hint} error={error} full={field.full} />;
    case 'secret':
      return <SecretField label={field.label} optional={optional} value={text} onChange={next => set(field.key, next)} error={error} hint={field.hint} placeholder={field.placeholder} disabled={disabled} full={field.full} storedSet={Boolean(stored[field.key])} />;
    case 'pem':
      return <PemField label={field.label} kind={field.pem ?? 'certificate'} value={text} onChange={next => set(field.key, next)} error={error} hint={field.hint} disabled={disabled} />;
    case 'textarea': case 'json': case 'lines':
      return <TextAreaField full mono label={field.label} optional={optional} value={text} onChange={next => set(field.key, next)} error={error} rows={field.kind === 'lines' ? 3 : 4} placeholder={field.placeholder} hint={field.hint} disabled={disabled} />;
    case 'base64':
      return <Base64Field field={field} value={text} onChange={next => set(field.key, next)} error={error} disabled={disabled} />;
    case 'pairs':
      return <HeaderEditor legend={field.label} rows={Array.isArray(value) ? value : []} onChange={(rows: PairRow[]) => set(field.key, rows)} error={error} hint={field.hint} disabled={disabled} noun="entry" placeholder="name" />;
    case 'condition':
      return <ConditionField field={field} values={values} set={(key, next) => set(key, next)} error={error} disabled={disabled} />;
    case 'jsonata': {
      const read = (suffix: string): string => String(values[`${field.key}.${suffix}`] ?? '');
      const draft: JsonataDraft = { expression: read('expression'), operator: (read('operator') || '==') as JsonataOperator, as: (read('as') || 'text') as JsonataAs, expected: read('expected') };
      return <JsonataInputs label={field.label} value={draft} input={field.input ?? 'http'} error={error} hint={field.hint} disabled={disabled}
        onChange={next => { for (const [suffix, entry] of Object.entries(next)) set(`${field.key}.${suffix}`, entry); }} />;
    }
    default:
      return (
        <TextField
          mono={field.kind !== 'text' || /address|endpoint|oid|uri|path|topic|container|method|version/iu.test(field.key)} full={field.full}
          label={optional ? `${field.label} (optional)` : field.label} value={text} onChange={next => set(field.key, next)} error={error}
          placeholder={field.placeholder} hint={field.unit ? <>{field.hint} <span className="muted">In {field.unit}.</span></> : field.hint}
          inputMode={field.kind === 'int' || field.kind === 'intlist' ? 'numeric' : 'text'} disabled={disabled}
        />
      );
  }
}

const SECTIONS: FieldSection[] = ['auth', 'query', 'tls', 'advanced'];

function sectionSummary(section: FieldSection, fields: ServiceField[], values: Values): string {
  const set = fields.filter(field => {
    const value = field.kind === 'condition' ? values[`${field.key}.operator`] : field.kind === 'jsonata' ? values[`${field.key}.expression`] : values[field.key];
    return Array.isArray(value) ? value.length > 0 : Boolean(value);
  });
  if (!set.length) return section === 'tls' ? 'System roots · verified' : 'Not set';
  return set.map(field => field.label.replace(/ \(.*\)$/u, '')).slice(0, 3).join(' · ') + (set.length > 3 ? ` +${set.length - 3}` : '');
}

/** Renders one protocol's fields: the essentials inline, the rest in sections that open on demand or on error. */
export function ServiceFields({ protocol, values, onChange, errors, disabled, stored, open, onToggle }: {
  protocol: ProtocolKey; values: Values; onChange: (values: Values) => void; errors: Record<string, string>; disabled?: boolean;
  /** Draft values as first loaded, to flag a cleared stored secret. */
  stored: Values;
  open: Record<string, boolean>; onToggle: (section: string) => void;
}): ReactNode {
  const fields = SERVICE_FIELDS[protocol].filter(field => fieldVisible(field, values));
  // Several keys can change in one event (a JSONata edit); accumulate them so none is lost before re-render.
  let latest = values;
  const set = (key: string, value: Values[string]): void => { latest = { ...latest, [key]: value }; onChange(latest); };
  const main = fields.filter(field => !field.section || field.section === 'main');
  return (
    <>
      {main.map(field => <FieldInput key={field.key} field={field} values={values} set={set} errors={errors} disabled={disabled} stored={stored} />)}
      {SECTIONS.map(section => {
        const inSection = fields.filter(field => field.section === section);
        if (!inSection.length) return null;
        const key = `svc-${section}`;
        const hasError = inSection.some(field => errors[field.key]);
        return (
          <Disclosure key={section} title={SECTION_TITLE[section]} summary={sectionSummary(section, inSection, values)} open={Boolean(open[key]) || hasError} onToggle={() => onToggle(key)}>
            {inSection.map(field => <FieldInput key={field.key} field={field} values={values} set={set} errors={errors} disabled={disabled} stored={stored} />)}
          </Disclosure>
        );
      })}
    </>
  );
}

/** Which service sections hold values, so an existing monitor opens them by default. */
export function openServiceSections(protocol: ProtocolKey, values: Values): Record<string, boolean> {
  const out: Record<string, boolean> = {};
  for (const section of SECTIONS) {
    const filled = SERVICE_FIELDS[protocol].some(field => field.section === section && fieldVisible(field, values) && (() => {
      const value = field.kind === 'condition' ? values[`${field.key}.operator`] : field.kind === 'jsonata' ? values[`${field.key}.expression`] : values[field.key];
      return Array.isArray(value) ? value.length > 0 : Boolean(value);
    })());
    out[`svc-${section}`] = filled;
  }
  return out;
}

const JSONATA_AS_LABEL: Record<JsonataAs, string> = { text: 'text', number: 'number', true: 'true', false: 'false' };

/**
 * Expression, operator and typed expected value. Nothing is compiled or evaluated in the browser: the node runs the
 * expression in a separate, resource-limited worker. The node reports a syntax or evaluation error exactly like a
 * false result, so the copy says so instead of promising a separate diagnostic.
 */
export function JsonataInputs({ label, value, onChange, input, error, hint, disabled }: {
  label: string; value: JsonataDraft; onChange: (value: JsonataDraft) => void; input: keyof typeof JSONATA_INPUT; error?: string; hint?: ReactNode; disabled?: boolean;
}) {
  const id = useId();
  const kinds = jsonataKinds(value.operator);
  const as = kinds.includes(value.as) ? value.as : kinds[0]!;
  const set = (change: Partial<JsonataDraft>): void => onChange({ ...value, ...change });
  const bytes = new TextEncoder().encode(value.expression).length;
  return (
    <fieldset className="fld full jsonata-f" disabled={disabled} aria-describedby={`${id}-h${error ? ` ${id}-e` : ''}`}>
      <legend>{label} <span className="muted">(optional)</span></legend>
      <label className="vh" htmlFor={`${id}-x`}>{label}: expression</label>
      <textarea id={`${id}-x`} className="mono" rows={3} value={value.expression} spellCheck={false} autoComplete="off" placeholder={input === 'http' ? '$count(items[status = "ok"])' : '$.state'}
        aria-invalid={error ? true : undefined} onChange={event => set({ expression: event.target.value })} />
      {value.expression ? (
        <div className="cond">
          {/* Changing the operator never changes the expected value's type. */}
          <select aria-label={`${label}: operator`} value={value.operator} onChange={event => set({ operator: event.target.value as JsonataOperator })}>
            {JSONATA_OPERATORS.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
          </select>
          {kinds.length > 1 ? (
            <select aria-label={`${label}: expected type`} value={as} onChange={event => set({ as: event.target.value as JsonataAs })}>
              {kinds.map(kind => <option key={kind} value={kind}>{kind === 'text' || kind === 'number' ? `a ${JSONATA_AS_LABEL[kind]}` : JSONATA_AS_LABEL[kind]}</option>)}
            </select>
          ) : null}
          {as === 'text' || as === 'number' ? (
            <input type="text" className="mono" aria-label={`${label}: expected value`} value={value.expected} inputMode={as === 'number' ? 'decimal' : 'text'}
              placeholder={as === 'number' ? '0' : 'ok'} aria-invalid={error ? true : undefined} onChange={event => set({ expected: event.target.value })} />
          ) : null}
        </div>
      ) : null}
      <small id={`${id}-h`}>
        {hint ? <>{hint} </> : null}<span className="mono">$</span> is {JSONATA_INPUT[input]}. Empty: no JSONata condition.{value.expression ? ` ${bytes.toLocaleString()} of 4,096 bytes.` : ''}
      </small>
      {value.expression && jsonataCaveat({ ...value, as }) ? <small className="fwarn"><Glyph tone="warn" />{jsonataCaveat({ ...value, as })}</small> : null}
      {value.expression ? (
        <small className="jsonata-note">
          <Icon name="info" className="inline" />
          The node evaluates this in a separate worker with time, memory and size limits; this page never compiles or runs it. A syntax or evaluation error is reported exactly like a false result{input === 'mqtt' ? ', so the check keeps waiting for a matching message until the deadline' : ' (condition not met)'}; only an unavailable worker reports pending.
          {' '}<span className="mono">==</span> and <span className="mono">!=</span> compare as text (2 equals "2"); ordering converts like JSONata <span className="mono">$number</span>; <span className="mono">contains</span> is literal text. Null, arrays and objects never pass.
          {' '}It uses a Go port of JSONata with known differences from the JavaScript version Uptime Kuma uses: no regex lookahead, no <span className="mono">??</span> or <span className="mono">?:</span>, and different Infinity/NaN handling.
        </small>
      ) : null}
      <ErrorText id={`${id}-e`}>{error}</ErrorText>
    </fieldset>
  );
}
