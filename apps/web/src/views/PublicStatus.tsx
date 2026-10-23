import { useEffect, useId, useMemo, useState, type FormEvent } from 'react';
import type { PublicStatusPage, PublicStatusState, StatusPageAnnouncement } from '@uptime-canary/shared';
import { ApiFailure, fetchPublicStatus, isAbort, normalizeApiUrl } from '../api';
import { addressCaveat } from '../badges';
import { ago, stamp, utc } from '../format';
import { useNow } from '../hooks';
import { href, navigate, publicHref } from '../router';
import { ThemeButton, type ThemeControl } from '../theme';
import { BadgeList } from '../ui/badges';
import { CanaryMark, Glyph, Icon } from '../ui/icons';
import { TagPill } from '../ui/tags';
import { lastEndpoint } from './Connect';
import type { Tone } from '../model';

const REFRESH_MS = 30_000;
const STATES = new Set<PublicStatusState>(['up', 'down', 'pending', 'unknown']);
const NOTE_STATES = new Set(['investigating', 'identified', 'monitoring', 'resolved']);

const str = (value: unknown, limit = 4096): string => typeof value === 'string' ? value.slice(0, limit) : '';
const state = (value: unknown): PublicStatusState => STATES.has(value as PublicStatusState) ? value as PublicStatusState : 'unknown';
const arr = (value: unknown): unknown[] => Array.isArray(value) ? value : [];
const obj = (value: unknown): Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : {};

/** The public JSON comes from the network: keep only expected fields, as text, with safe fallbacks. */
function normalize(value: unknown): PublicStatusPage {
  const page = obj(value);
  return {
    slug: str(page.slug, 64), title: str(page.title, 512) || 'Status', description: str(page.description, 8192),
    state: state(page.state), maintenance: page.maintenance === true,
    generatedAt: typeof page.generatedAt === 'number' && Number.isFinite(page.generatedAt) ? page.generatedAt : 0,
    groups: arr(page.groups).slice(0, 16).map(raw => {
      const group = obj(raw);
      return {
        id: str(group.id, 128), name: str(group.name, 512), state: state(group.state), maintenance: group.maintenance === true,
        monitors: arr(group.monitors).slice(0, 64).map(rawMonitor => {
          const monitor = obj(rawMonitor);
          const certificate = obj(monitor.certificate);
          const tags = arr(monitor.tags).slice(0, 32).map(rawTag => { const tag = obj(rawTag); return { name: str(tag.name, 512), color: str(tag.color, 7), ...(tag.value ? { value: str(tag.value, 512) } : {}) }; }).filter(tag => tag.name);
          return {
            name: str(monitor.name, 512), state: state(monitor.state), maintenance: monitor.maintenance === true,
            ...(certificate.notAfter && certificate.notBefore ? { certificate: { notBefore: str(certificate.notBefore, 64), notAfter: str(certificate.notAfter, 64) } } : {}),
            ...(tags.length ? { tags } : {}),
          };
        }),
      };
    }),
    announcements: arr(page.announcements).slice(0, 50).map((raw): StatusPageAnnouncement => {
      const note = obj(raw);
      return {
        id: str(note.id, 128), title: str(note.title, 512), message: str(note.message, 8192),
        state: (NOTE_STATES.has(str(note.state)) ? note.state : 'investigating') as StatusPageAnnouncement['state'],
        createdAt: typeof note.createdAt === 'number' ? note.createdAt : 0, updatedAt: typeof note.updatedAt === 'number' ? note.updatedAt : 0, pinned: note.pinned === true,
      };
    }),
  };
}

const WORD: Record<PublicStatusState, string> = { up: 'Operational', down: 'Down', pending: 'Pending', unknown: 'Unknown' };
const tone = (s: PublicStatusState): Tone => s === 'up' ? 'up' : s === 'down' ? 'down' : s === 'pending' ? 'pending' : 'unknown';
const NOTE: Record<string, { label: string; tone: Tone }> = {
  investigating: { label: 'Investigating', tone: 'warn' }, identified: { label: 'Identified', tone: 'serious' },
  monitoring: { label: 'Monitoring', tone: 'pending' }, resolved: { label: 'Resolved', tone: 'up' },
};

function headline(page: PublicStatusPage): { title: string; text: string } {
  const monitors = page.groups.flatMap(group => group.monitors);
  const down = monitors.filter(m => m.state === 'down').length;
  const unknown = monitors.filter(m => m.state === 'unknown').length;
  switch (page.state) {
    case 'up': return { title: 'All systems operational', text: `Every listed service passed its latest checks from every location.` };
    case 'down': return { title: down === monitors.length ? 'Major outage' : `${down} of ${monitors.length} services down`, text: 'At least one location currently sees these services failing.' };
    case 'pending': return { title: 'Checks in progress', text: 'Some services have a result that is neither up nor down yet, such as a retry in progress.' };
    default: return { title: monitors.length ? 'Status partly unknown' : 'No services listed', text: monitors.length ? `${unknown || 'Some'} service${unknown === 1 ? '’s' : 's’'} current state isn’t known from this node. Unknown is never shown as up.` : 'This page has no services yet.' };
  }
}

type Load = { kind: 'loading' } | { kind: 'ok'; page: PublicStatusPage; at: number; from: string } | { kind: 'error'; message: string; notFound: boolean; last?: { page: PublicStatusPage; at: number; from: string } };

export function PublicStatusView({ slug, nodes, theme, signedIn }: { slug: string; nodes: string[]; theme: ThemeControl; signedIn: boolean }) {
  const candidates = useMemo(() => {
    const out: string[] = [];
    for (const node of nodes.length ? nodes : [lastEndpoint()]) { try { const url = normalizeApiUrl(node); if (!out.includes(url)) out.push(url); } catch { /* ignore malformed */ } }
    return out;
  }, [nodes]);
  const [active, setActive] = useState(0);
  const base = candidates[active];
  const [load, setLoad] = useState<Load>({ kind: 'loading' });
  const [nonce, setNonce] = useState(0);
  const now = useNow(5000);
  const formId = useId();
  const [other, setOther] = useState('');
  const [otherError, setOtherError] = useState<string | null>(null);
  const [badgeNonce, setBadgeNonce] = useState(0);
  // Badge previews exist only while a visitor has the panel open: no image requests, and no images in the page otherwise.
  const [embedOpen, setEmbedOpen] = useState(false);

  useEffect(() => { document.title = load.kind === 'ok' ? `${load.page.title} · status` : 'Status'; }, [load]);
  useEffect(() => {
    if (!base) { setLoad({ kind: 'error', message: 'This link doesn’t name a valid node address.', notFound: false }); return; }
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const run = async (): Promise<void> => {
      try {
        const value = await fetchPublicStatus(base, slug, controller.signal);
        if (controller.signal.aborted) return;
        setLoad({ kind: 'ok', page: normalize(value), at: Date.now(), from: base });
      } catch (error) {
        if (controller.signal.aborted || isAbort(error)) return;
        const notFound = error instanceof ApiFailure && error.status === 404;
        const message = notFound ? 'This node doesn’t publish a status page at this address.'
          : error instanceof ApiFailure && error.status === 0 ? 'This node can’t be reached from your browser right now.'
          : error instanceof Error ? error.message : 'The status page couldn’t be loaded.';
        setLoad(previous => ({ kind: 'error', message, notFound, last: previous.kind === 'ok' && previous.from === base ? previous : previous.kind === 'error' && previous.last?.from === base ? previous.last : undefined }));
      }
      if (!controller.signal.aborted) timer = setTimeout(() => { if (!document.hidden) void run(); else timer = setTimeout(() => void run(), REFRESH_MS); }, REFRESH_MS);
    };
    setLoad(previous => previous.kind === 'ok' && previous.from === base ? previous : { kind: 'loading' });
    void run();
    return () => { controller.abort(); if (timer) clearTimeout(timer); };
  }, [base, slug, nonce]);

  const shown = load.kind === 'ok' ? load : load.kind === 'error' ? load.last : undefined;
  const page = shown?.page;
  const head = page ? headline(page) : null;
  const notes = page ? [...page.announcements.filter(note => note.pinned), ...page.announcements.filter(note => !note.pinned)] : [];

  const useOther = (event: FormEvent): void => {
    event.preventDefault();
    try {
      const url = normalizeApiUrl(other);
      setOtherError(null);
      navigate(publicHref(slug, [url, ...candidates.filter(candidate => candidate !== url)]));
      setActive(0);
      setOther('');
    } catch (error) { setOtherError(error instanceof Error ? error.message : 'Enter a node address.'); }
  };

  return (
    <div className="pub">
      <header className="pub-top">
        <span className="pub-brand"><CanaryMark /><span>Status</span></span>
        <span className="grow" />
        {signedIn ? <a className="btn sm" href={href('status-pages')}><Icon name="chevLeft" />Dashboard</a> : null}
        <ThemeButton theme={theme} />
      </header>
      <main id="main" tabIndex={-1} className="pub-main">
        {page && head ? (
          <>
            <div className="pub-head">
              <h1>{page.title}</h1>
              {page.description ? <p className="pub-desc">{page.description}</p> : null}
            </div>
            <section className={`pub-state s-${page.state}`} aria-labelledby={`${formId}-state`}>
              <Glyph tone={tone(page.state)} />
              <div>
                <h2 id={`${formId}-state`}>{head.title}</h2>
                <p>{head.text}</p>
                {page.maintenance ? <p className="pub-maint"><Icon name="wrench" className="inline" />Planned maintenance is in progress for some services. Their state is still shown as measured.</p> : null}
              </div>
            </section>
            {load.kind === 'error' ? <p className="pub-stale" role="status"><Glyph tone="warn" />Couldn’t refresh: {load.message} Showing data from {ago(shown!.at, now)}.</p> : null}
            {notes.length ? (
              <section className="pub-notes" aria-label="Announcements">
                {notes.map(note => {
                  const meta = NOTE[note.state] ?? NOTE.investigating!;
                  return (
                    <article key={note.id || note.title} className={`pub-note n-${note.state}`}>
                      <div className="pub-note-h">
                        <span className={`chip c-${meta.tone}`}><Glyph tone={meta.tone} /><b>{meta.label}</b></span>
                        <h3>{note.title}</h3>
                        {note.pinned ? <Icon name="pin" className="pin" /> : null}
                      </div>
                      <p>{note.message}</p>
                      {note.createdAt ? <p className="muted small">Posted <time dateTime={new Date(note.createdAt).toISOString()} title={utc(note.createdAt)}>{stamp(note.createdAt, now)}</time>{note.updatedAt - note.createdAt >= 60_000 ? <> · updated <time dateTime={new Date(note.updatedAt).toISOString()} title={utc(note.updatedAt)}>{stamp(note.updatedAt, now)}</time></> : null}</p> : null}
                    </article>
                  );
                })}
              </section>
            ) : null}
            {page.groups.map((group, index) => (
              <section key={`${group.id}-${index}`} className="pub-group" aria-labelledby={`${formId}-g${index}`}>
                <div className="pub-group-h">
                  <h2 id={`${formId}-g${index}`}>{group.name}</h2>
                  <span className={`chip c-${tone(group.state)}`}><Glyph tone={tone(group.state)} /><b>{WORD[group.state]}</b></span>
                  {group.maintenance ? <span className="tag t-hold"><Icon name="wrench" />maintenance</span> : null}
                </div>
                {group.monitors.length ? (
                  <ul className="pub-list">
                    {group.monitors.map((monitor, mIndex) => (
                      <li key={`${monitor.name}-${mIndex}`}>
                        <div className="grow">
                          <span className="pub-name">{monitor.name}</span>
                          {monitor.tags?.length ? <span className="tagrow">{monitor.tags.map(tag => <TagPill key={`${tag.name}-${tag.value ?? ''}`} name={tag.name} color={tag.color} value={tag.value} />)}</span> : null}
                          {monitor.certificate ? <span className="muted small pub-cert"><Icon name="lock" className="inline" />Certificate valid until {new Date(monitor.certificate.notAfter).toLocaleDateString(undefined, { dateStyle: 'medium' })}</span> : null}
                        </div>
                        {monitor.maintenance ? <span className="tag t-hold"><Icon name="wrench" />maintenance</span> : null}
                        <span className={`pub-st s-${monitor.state}`}><Glyph tone={tone(monitor.state)} />{WORD[monitor.state]}</span>
                      </li>
                    ))}
                  </ul>
                ) : <p className="muted small">No services in this section.</p>}
              </section>
            ))}
          </>
        ) : load.kind === 'loading' ? (
          <div className="pub-empty"><CanaryMark quiet /><p>Loading the status page…</p></div>
        ) : (
          <div className="pub-empty">
            <CanaryMark quiet wire="var(--axis)" />
            <h1>{load.kind === 'error' && load.notFound ? 'Status page not found' : 'Status unavailable'}</h1>
            <p>{load.kind === 'error' ? load.message : ''}</p>
            {load.kind === 'error' && load.notFound ? <p className="muted small">It may be unpublished, renamed, or not yet known to this node. Another node below may already have it.</p> : null}
          </div>
        )}

        <footer className="pub-foot">
          <details className="pub-source">
            <summary>
              {shown ? <>Live view from <span className="mono">{shown.from}</span>, generated {page?.generatedAt ? <time dateTime={new Date(page.generatedAt).toISOString()} title={utc(page.generatedAt)}>{ago(page.generatedAt, now)}</time> : 'just now'} · refreshes every 30 s</>
                : <>Data source: <span className="mono">{base ?? 'none'}</span></>}
            </summary>
            <div className="pub-source-b">
              <p className="small">Each monitoring node serves this page independently, so two nodes can briefly differ. Choose another node if this one is unreachable.</p>
              {candidates.length > 1 ? (
                <fieldset className="pub-nodes">
                  <legend className="vh">Node</legend>
                  {candidates.map((candidate, index) => (
                    <label key={candidate}><input type="radio" name={`${formId}-node`} checked={index === active} onChange={() => setActive(index)} /><span className="mono">{candidate}</span></label>
                  ))}
                </fieldset>
              ) : null}
              <form className="pub-other" onSubmit={useOther}>
                <label htmlFor={`${formId}-other`}>Another node’s address</label>
                <div className="row-wrap">
                  <input id={`${formId}-other`} type="url" className="mono" placeholder="https://status-node.example.com" value={other} onChange={event => setOther(event.target.value)} aria-invalid={otherError ? true : undefined} />
                  <button type="submit" className="btn sm">Use</button>
                  <button type="button" className="btn sm" onClick={() => setNonce(n => n + 1)}><Icon name="refresh" />Refresh</button>
                </div>
                {otherError ? <small className="ferr"><Glyph tone="down" />{otherError}</small> : null}
              </form>
            </div>
          </details>
          {shown && page ? (
            <details className="pub-source" onToggle={event => { const isOpen = (event.currentTarget as HTMLDetailsElement).open; setEmbedOpen(isOpen); if (isOpen) setBadgeNonce(n => n + 1); }}>
              <summary>Embed a status badge</summary>
              {embedOpen ? <div className="pub-source-b pub-embed">
                <p className="small">A small image for a README or another site, served by <span className="mono">{shown.from}</span> without signing in. It shows the current state only: up, down, pending or unknown, with a maintenance segment during planned work.</p>
                {addressCaveat(shown.from) ? <p className="small fwarn"><Glyph tone="warn" />{addressCaveat(shown.from)}</p> : null}
                <BadgeList slug={slug} title={page.title} groups={page.groups} nodeUrl={shown.from} pageLink={`${location.origin}${location.pathname}${publicHref(slug, [shown.from, ...candidates.filter(candidate => candidate !== shown.from)])}`} nonce={badgeNonce} />
                <p className="muted small">If the page’s address or a service’s public name changes, embedded badges stop loading.</p>
              </div> : null}
            </details>
          ) : null}
          <p className="muted small">States are current results, not historical uptime or an SLA. Maintenance is shown beside a result and never turns a failure green. Powered by Uptime Canary.</p>
        </footer>
      </main>
    </div>
  );
}
