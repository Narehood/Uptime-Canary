import { useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import type { MonitorView } from '@uptime-canary/shared';
import { ago, shortClock, shortRev, stamp, utc } from '../format';
import { observationFor, stateTone, type Assessment, type NodeColumn, type Tone } from '../model';
import { CanaryMark, Glyph, Icon, type IconName } from './icons';
import { typeLabel, typeShort } from '../checks';

export function Chip({ tone, label, detail }: { tone: Tone; label: string; detail?: string }) {
  return (
    <span className={`chip c-${tone}`}>
      <Glyph tone={tone} />
      <b>{label}</b>
      {detail ? <span>{detail}</span> : null}
    </span>
  );
}

export const StateChip = ({ a }: { a: Assessment }) => <Chip tone={a.tone} label={a.label} detail={a.detail} />;

/** Mini per-location strip for lists; the label carries the counts. */
export function Dots({ view, a }: { view: MonitorView; a: Assessment }) {
  const label = `${a.failing.length} down, ${a.passing.length} up, ${a.pending.length ? `${a.pending.length} pending, ` : ''}${a.unknown.length} unknown of ${a.total} ${a.total === 1 ? 'location' : 'locations'}`;
  return (
    <span className="dots" role="img" aria-label={label}>
      {view.monitor.nodeIds.map(id => {
        const state = view.state === 'paused' ? 'paused' : stateTone(observationFor(view, id)?.state ?? 'unknown');
        return <i key={id} className={state === 'up' ? undefined : state} />;
      })}
    </span>
  );
}

export function Time({ at, now, mode = 'stamp' }: { at: number; now?: number; mode?: 'stamp' | 'short' | 'ago' }) {
  const text = mode === 'ago' ? ago(at, now ?? Date.now()) : mode === 'short' ? shortClock(at, now) : stamp(at, now);
  return <time dateTime={new Date(at).toISOString()} title={utc(at)}>{text}</time>;
}

export const Rev = ({ revision }: { revision: string }) => <span className="rev" title={`Revision ${revision}`}>{shortRev(revision)}</span>;

export const TypeTag = ({ type }: { type: string }) => <span className="ttype" title={typeLabel(type)}>{typeShort(type)}</span>;

export function Tag({ icon, children, tone }: { icon?: IconName; children: ReactNode; tone?: 'accent' | 'hold' }) {
  return <span className={`tag${tone ? ` t-${tone}` : ''}`}>{icon ? <Icon name={icon} /> : null}{children}</span>;
}

export const ReadOnly = ({ children = 'Read only' }: { children?: ReactNode }) => <Tag icon="lock">{children}</Tag>;

export function EmptyState({ title, children, action, quiet = true }: { title: string; children?: ReactNode; action?: ReactNode; quiet?: boolean }) {
  return (
    <div className="empty">
      <CanaryMark wire="var(--axis)" quiet={quiet} />
      <b>{title}</b>
      {children ? <span>{children}</span> : null}
      {action}
    </div>
  );
}

export function PageHead({ title, sub, actions, crumbs }: { title: ReactNode; sub?: ReactNode; actions?: ReactNode; crumbs?: ReactNode }) {
  return (
    <div className="vhead">
      <div className="vhead-t">
        {crumbs ? <div className="crumbs">{crumbs}</div> : null}
        <h1 tabIndex={-1}>{title}</h1>
        {sub ? <div className="sub">{sub}</div> : null}
      </div>
      {actions ? <div className="acts">{actions}</div> : null}
    </div>
  );
}

export function Card({ title, hint, actions, children, className, id }: { title?: ReactNode; hint?: ReactNode; actions?: ReactNode; children: ReactNode; className?: string; id?: string }) {
  const generated = useId();
  const headingId = id ?? generated;
  return (
    <section className={`card${className ? ` ${className}` : ''}`} aria-labelledby={title ? headingId : undefined}>
      {title ? (
        <div className="card-h">
          <div>
            <h2 id={headingId}>{title}</h2>
            {hint ? <div className="hint">{hint}</div> : null}
          </div>
          {actions ? <div className="r">{actions}</div> : null}
        </div>
      ) : null}
      {children}
    </section>
  );
}

export function Banner({ tone = 'warn', title, children, actions, role = 'status' }: { tone?: 'warn' | 'down' | 'info' | 'hold'; title: ReactNode; children?: ReactNode; actions?: ReactNode; role?: 'status' | 'alert' | 'note' }) {
  const glyph: Record<string, ReactNode> = {
    warn: <Glyph tone="warn" />, down: <Glyph tone="down" />,
    info: <span className="b-ic"><Icon name="info" /></span>, hold: <span className="b-ic"><Icon name="groups" /></span>,
  };
  return (
    <div className={`banner b-${tone}`} role={role === 'note' ? undefined : role}>
      {glyph[tone]}
      <div>
        <b className="bt">{title}</b>
        {children ? <div className="bb">{children}</div> : null}
        {actions ? <div className="acts">{actions}</div> : null}
      </div>
    </div>
  );
}

/**
 * Copies `value`. Without a clipboard (a plain-HTTP page) or when the browser refuses, it says so instead of doing
 * nothing; the page never promises to clear the clipboard afterwards. `onCopied` runs only after a successful copy.
 */
export function CopyButton({ value, label = 'Copy', className = 'btn sm', ariaLabel, onCopied, disabled = false }: { value: string; label?: string; className?: string; ariaLabel?: string; onCopied?: () => void; disabled?: boolean }) {
  const [copied, setCopied] = useState<null | 'done' | 'failed'>(null);
  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => setCopied(null), copied === 'failed' ? 6000 : 2000);
    return () => clearTimeout(timer);
  }, [copied]);
  const copy = (): void => {
    if (disabled) return;
    const clipboard = typeof navigator === 'undefined' ? undefined : navigator.clipboard;
    if (!clipboard?.writeText) { setCopied('failed'); return; }
    clipboard.writeText(value).then(() => { setCopied('done'); onCopied?.(); }, () => setCopied('failed'));
  };
  const what = ariaLabel?.replace(/^Copy /u, '');
  return (
    <button type="button" className={`${className}${copied === 'failed' ? ' copy-failed' : ''}`} aria-label={ariaLabel ? (copied === 'done' ? `Copied: ${what}` : copied === 'failed' ? `Couldn’t copy: ${what}` : ariaLabel) : undefined} disabled={disabled} onClick={copy}>
      <Icon name={copied === 'done' ? 'check' : copied === 'failed' ? 'info' : 'copy'} />
      <span aria-live="polite">{copied === 'done' ? 'Copied' : copied === 'failed' ? 'Couldn’t copy' : label}</span>
    </button>
  );
}

/** Native modal dialog: focus trap, Escape and focus return come from the platform. */
export function Modal({ title, onClose, children, wide, busy, xwide }: { title: ReactNode; onClose: () => void; children: ReactNode; wide?: boolean; busy?: boolean; xwide?: boolean }) {
  const ref = useRef<HTMLDialogElement>(null);
  const headingId = useId();
  const close = useRef(onClose);
  close.current = onClose;
  const holding = useRef(busy);
  holding.current = busy;
  useLayoutEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    dialog.showModal();
    // The browser may close a modal dialog by itself: a close request (Escape) ignores a prevented cancel event when the
    // page has had no user activation since the last one (HTML close watchers). This page decides when a dialog
    // closes, so one closed that way opens again; unless it's busy, that counts as a close request, which the page may
    // still refuse (a confirmation, a busy step). Removed before this dialog closes itself on unmount.
    const reopen = (): void => {
      if (dialog.open || !dialog.isConnected) return;
      dialog.showModal();
      if (!holding.current) close.current();
    };
    dialog.addEventListener('close', reopen);
    return () => {
      dialog.removeEventListener('close', reopen);
      if (dialog.open) dialog.close();
      // React may detach the dialog before the platform restores focus, so return it to the opener explicitly.
      if (opener?.isConnected) opener.focus({ preventScroll: true });
    };
  }, []);
  return (
    <dialog
      ref={ref}
      className={`modal${wide ? ' wide' : ''}${xwide ? ' xwide' : ''}`}
      aria-labelledby={headingId}
      onCancel={event => { event.preventDefault(); if (!busy) close.current(); }}
    >
      <div className="modal-h">
        <h2 id={headingId}>{title}</h2>
        <button type="button" className="icon-btn sm" onClick={() => { if (!busy) close.current(); }} aria-label="Close" disabled={busy}>
          <Icon name="close" />
        </button>
      </div>
      <div className="modal-b">{children}</div>
    </dialog>
  );
}

export function FilterChips<T extends string>({ label, value, options, onChange }: { label: string; value: T; options: { value: T; label: string; count?: number }[]; onChange: (value: T) => void }) {
  return (
    <div className="filters" role="group" aria-label={label}>
      {options.map(option => (
        <button key={option.value} type="button" className="fchip" aria-pressed={value === option.value} onClick={() => onChange(option.value)}>
          {option.label}
          {option.count !== undefined ? <span className="n">{option.count}</span> : null}
        </button>
      ))}
    </div>
  );
}

export function NodeLabel({ node, id }: { node?: NodeColumn; id: string }) {
  return (
    <span className="nlabel">
      <b className="mono">{id}</b>
      {node && node.name !== id ? <span className="muted"> {node.name}</span> : null}
    </span>
  );
}

/** State of a node as seen from the endpoint. */
export function nodeTone(node: NodeColumn | undefined): Tone {
  if (!node) return 'unknown';
  return node.state === 'local' || node.state === 'connected' ? 'up' : 'unknown';
}

export function nodeStateText(node: NodeColumn | undefined, endpoint: string, now: number): string {
  if (!node) return 'Not configured on this endpoint';
  switch (node.state) {
    case 'local': return 'Endpoint: this dashboard talks to it';
    case 'connected': return node.lastSeenAt ? `Connected · last sync ${ago(node.lastSeenAt, now)}` : 'Connected';
    case 'unreachable': return node.lastSeenAt ? `Out of view from ${endpoint} · last seen ${ago(node.lastSeenAt, now)}` : `Out of view from ${endpoint} · never reached`;
    case 'unlisted': return `Not a configured peer of ${endpoint}`;
  }
}

export function ErrorText({ id, children }: { id: string; children?: ReactNode }) {
  return children ? <small className="ferr" id={id}><Glyph tone="down" />{children}</small> : null;
}

export function Spinner({ label }: { label: string }) {
  return <span className="spin" role="status"><span className="spin-dot" aria-hidden="true" />{label}</span>;
}
