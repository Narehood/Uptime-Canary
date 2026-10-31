import { useEffect, useId, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import { describeFailure } from '../api';
import { ago, elapsed, longStamp, plural, utc } from '../format';
import { controlPlaneMode } from '../gateway';
import { useNow } from '../hooks';
import { session, type Connection } from '../session';
import { Banner, Modal, Spinner, Time } from '../ui/bits';
import { Glyph, Icon } from '../ui/icons';
import {
  NO_UPDATES, UPDATE_LINKS, checkProblem, compareHref, dismissNotice, noticeDismissed, rowStatus, shortCommit, updateFeed, updateFeedKey,
  updateNotice, updateSummary, type RowKind, type UpdateReport, type UpdateRow, type UpdateSnapshot,
} from '../updates';

/** The dashboard's stored update status as the shell shows it. */
export interface Updates {
  /** This sign-in's feed; a dismissed notice stays dismissed only for it. */
  key: string;
  snapshot: UpdateSnapshot;
  /** An administrator's "Check now": one request, never repeated on its own. */
  check: () => void;
}

const quiet = (): (() => void) => () => {};
const nothing = (): UpdateSnapshot => NO_UPDATES;

/**
 * Control plane only: the update status this dashboard's server stored, read from the dashboard itself at most once a
 * minute and shared across the shell's renders, remounts and node selections. Direct and gateway pages, and screens
 * without a sign-in (which never render the shell), ask nothing. Reads wait while the sign-in is rejected or the page
 * is hidden, and stop when the shell goes away.
 */
export function useUpdates(connection: Connection): Updates | null {
  const key = controlPlaneMode() ? updateFeedKey(connection) : null;
  const feed = useMemo(() => key === null ? null : updateFeed(key), [key]);
  const api = useRef(connection.api);
  api.current = connection.api;
  useEffect(() => {
    if (!feed) return;
    // The stylesheet comes with the feature, so anything that renders the shell outside a browser never loads CSS.
    void import('../updates.css').catch(() => undefined);
    const release = feed.watch({
      read: signal => api.current.updates(signal),
      check: signal => api.current.checkUpdates(signal),
      paused: () => session.getSnapshot().connection?.link === 'rejected' || document.visibilityState === 'hidden',
    });
    const visible = (): void => { if (document.visibilityState !== 'hidden') feed.wake(); };
    document.addEventListener('visibilitychange', visible);
    return () => { document.removeEventListener('visibilitychange', visible); release(); };
  }, [feed]);
  const snapshot = useSyncExternalStore(feed ? feed.listen : quiet, feed ? feed.current : nothing, feed ? feed.current : nothing);
  return useMemo(() => feed && key !== null ? { key, snapshot, check: () => { void feed.checkNow(); } } : null, [feed, key, snapshot]);
}

function PackageIcon() {
  return (
    <svg className="ic" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <g fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
        <path d="M12 3.3 19.8 7.4v9.2L12 20.7l-7.8-4.1V7.4z" />
        <path d="m4.2 7.4 7.8 4.1 7.8-4.1M12 11.5v9.2M8.1 5.35l7.8 4.1" />
      </g>
    </svg>
  );
}

function ExternalLink({ href, className, children }: { href: string; className?: string; children: ReactNode }) {
  return (
    <a className={className} href={href} target="_blank" rel="noopener noreferrer" referrerPolicy="no-referrer">
      {children}<Icon name="external" /><span className="vh"> (opens in a new tab)</span>
    </a>
  );
}

/** The shell's way into the details, on every control-plane page; it carries a count while something can be updated. */
export function UpdatesButton({ updates, onOpen }: { updates: Updates; onOpen: () => void }) {
  if (updates.snapshot.unsupported) return null;
  const count = updateNotice(updates.snapshot.report)?.count ?? 0;
  const label = count ? `Software updates: ${plural(count, 'update')} available` : 'Software updates';
  return (
    <button type="button" className={`icon-btn upd-btn${count ? ' has' : ''}`} onClick={onOpen} aria-haspopup="dialog" aria-label={label} data-tip={label}>
      <PackageIcon />
      {count ? <span className="upd-n" aria-hidden="true">{count}</span> : null}
    </button>
  );
}

/**
 * Discreet, and only while something can be updated; nothing shows when everything is current, unknown or off. "Not now"
 * hides it for this sign-in until another update, a newer image or a newer build of the same source is published.
 */
export function UpdateBanner({ updates, onOpen }: { updates: Updates; onOpen: () => void }) {
  const [, setDismissed] = useState<string | null>(null);
  const notice = updateNotice(updates.snapshot.report);
  if (!notice || noticeDismissed(updates.key, notice)) return null;
  return (
    <div className="upd-banner">
      <Banner tone="info" role="note" title={notice.title} actions={<>
        <button type="button" className="btn sm" onClick={onOpen} aria-haspopup="dialog"><PackageIcon />Details</button>
        <button type="button" className="btn sm" onClick={() => { dismissNotice(updates.key, notice); setDismissed(notice.signature); }}>Not now</button>
      </>}>
        {notice.text}
      </Banner>
    </div>
  );
}

function StatusChip({ kind, label }: { kind: RowKind; label: string }) {
  const mark = kind === 'available' ? <span className="upd-ic"><Icon name="arrowUp" /></span>
    : kind === 'ahead' ? <span className="upd-ic"><Icon name="branch" /></span>
    : <Glyph tone={kind === 'current' ? 'up' : kind === 'unavailable' ? 'warn' : kind === 'off' ? 'none' : 'unknown'} />;
  return <span className={`chip upd-chip k-${kind}`}>{mark}<b>{label}</b></span>;
}

/** A commit that already read as hexadecimal: its first digits, with the whole commit as a title. */
function Commit({ value, missing }: { value?: string; missing: string }) {
  return value ? <span className="mono" title={value}>{shortCommit(value)}</span> : <span className="muted">{missing}</span>;
}

/** When the official publisher built an image: the UTC instant, then the viewer's local time, each labelled. */
function BuildTime({ at, missing }: { at?: number; missing: string }) {
  if (at === undefined) return <span className="muted">{missing}</span>;
  return <><time className="mono" dateTime={new Date(at).toISOString()}>{utc(at)}</time> <span className="muted upd-local">{longStamp(at)} your time</span></>;
}

function UpdateRowItem({ row, report }: { row: UpdateRow; report: UpdateReport }) {
  const status = rowStatus(row, report);
  const changes = compareHref(row);
  return (
    <li className={`upd-row k-${status.kind}`}>
      <div className="upd-row-h">
        <span className="upd-name">
          <b>{row.label}</b>
          <span className="muted">{row.component === 'dashboard' ? 'Dashboard' : 'Node'}{row.image ? <> · <span className="mono">{row.image}</span></> : null}</span>
        </span>
        <StatusChip kind={status.kind} label={status.label} />
      </div>
      <dl className="upd-commits">
        <div><dt>Running version</dt><dd>{row.currentVersion || 'Not reported'}</dd></div>
        <div><dt>Latest version</dt><dd>{row.latestVersion || (report.enabled ? 'Not read' : 'Not checked')}</dd></div>
        <div><dt>Running source</dt><dd><Commit value={row.currentCommit} missing="Not reported" /></dd></div>
        <div><dt>Latest official</dt><dd><Commit value={row.latestCommit} missing={report.enabled ? 'Not read' : 'Not checked'} /></dd></div>
        <div><dt>Running image built</dt><dd><BuildTime at={row.currentBuiltAt} missing="Not reported" /></dd></div>
        <div><dt>Latest image built</dt><dd><BuildTime at={row.latestBuiltAt} missing={row.latestCommit ? 'Not reported' : report.enabled ? 'Not read' : 'Not checked'} /></dd></div>
      </dl>
      <p className="upd-why">{status.text}</p>
      {changes ? <ExternalLink className="upd-link" href={changes}>What changed</ExternalLink> : null}
    </li>
  );
}

/**
 * Every role can read what's running and what was published; only administrators see "Check now". Check times are the
 * dashboard server's, so they're compared with its clock (`skew`: the dashboard's clock minus the browser's).
 */
export function UpdatesDialog({ updates, admin, skew, onClose }: { updates: Updates; admin: boolean; skew: number; onClose: () => void }) {
  const browserNow = useNow(15_000);
  const now = browserNow + skew;
  const rowsId = useId();
  const howId = useId();
  const { snapshot } = updates;
  const { report, failure, readAt, sending, checkFailure } = snapshot;
  const summary = updateSummary(snapshot);
  const checking = report?.state === 'checking';
  const progress = sending ? 'Asking the dashboard’s server to check…'
    : checkFailure ? checkProblem(checkFailure)
    : checking ? 'Checking. This page asks for the result in about a minute.'
    : !admin && report?.enabled ? 'An administrator can ask for a check sooner.'
    : '';
  return (
    <Modal title="Software updates" onClose={onClose} wide>
      <p className="upd-sum"><b>{summary.title}</b>{summary.text ? <span>{summary.text}</span> : null}</p>
      {!report && !failure && !snapshot.unsupported ? <Spinner label="Reading the stored status…" /> : null}
      {report && failure ? (
        <p className="upd-stale"><Glyph tone="warn" /><span>Couldn’t refresh this status. {describeFailure(failure)} What’s shown was read {readAt ? ago(readAt, browserNow) : 'earlier'}.</span></p>
      ) : null}
      {report ? <>
        <dl className="kv upd-when">
          <dt>Last check</dt>
          <dd>{report.checkedAt ? <><Time at={report.checkedAt} now={now} /> · {ago(report.checkedAt, now)}</> : 'None yet'}</dd>
          <dt>Next check</dt>
          <dd>{!report.enabled ? 'None: update checks are off'
            : report.nextCheckAt ? <><Time at={report.nextCheckAt} now={now} /> · {report.nextCheckAt > now ? `in ${elapsed(report.nextCheckAt - now)}` : 'due now'}</>
            : 'Not scheduled yet'}</dd>
          <dt>How often</dt>
          <dd>{report.intervalHours ? `Every ${plural(report.intervalHours, 'hour')}` : 'On its own schedule'}, by this dashboard’s server. Browsers only read the result it stored.</dd>
        </dl>
        <section className="upd-sec" aria-labelledby={rowsId}>
          <h3 id={rowsId}>Components</h3>
          {report.rows.length
            ? <ul className="upd-rows">{report.rows.map(row => <UpdateRowItem key={row.key} row={row} report={report} />)}</ul>
            : <p className="muted">No components listed.</p>}
        </section>
      </> : null}
      <section className="upd-sec upd-how" aria-labelledby={howId}>
        <h3 id={howId}>How to update</h3>
        <ul>
          <li>Nothing here pulls, installs or restarts anything, and pinned tags or digests are never changed.</li>
          <li>Back up first. Then pull the new image and recreate each container with its existing volumes: a node’s identity and database, the dashboard’s data and identity.</li>
          <li>Update every node and the dashboard together to the same source. With a pinned tag or digest, choose the new one yourself.</li>
        </ul>
        <p className="upd-limit">
          <b>What this check compares.</b> It compares the source commit each image was built from. When the commits match, it
          compares the build time the official publisher stamped into each image, so a newer build of the same source shows as an
          update; without that time on both images (older images and custom builds don’t have it) the result is unknown. It can’t
          see what changed inside a rebuilt image, such as its base image, and the exact image digest each component runs isn’t
          known to this check.
        </p>
        <p className="upd-links">
          <ExternalLink href={UPDATE_LINKS.upgrades}>Coordinated upgrades</ExternalLink>
          <ExternalLink href={UPDATE_LINKS.packages}>Official packages</ExternalLink>
          <ExternalLink href={UPDATE_LINKS.workflow}>Publishing workflow</ExternalLink>
        </p>
      </section>
      <div className="modal-foot">
        <p className="upd-act" role="status">{progress}</p>
        <div className="upd-acts">
          {admin && report?.enabled ? (
            <button type="button" className="btn primary" disabled={sending || checking} onClick={updates.check}>
              <Icon name="refresh" />{sending ? 'Asking…' : checking ? 'Checking…' : 'Check now'}
            </button>
          ) : null}
          <button type="button" className="btn" onClick={onClose}>Close</button>
        </div>
      </div>
    </Modal>
  );
}
