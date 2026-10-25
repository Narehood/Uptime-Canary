import { useEffect, useId, useMemo, useRef, useState, type CSSProperties, type FormEvent } from 'react';
import type { StatusPage, StatusPageAnnouncement, StatusPageAnnouncementState, StatusPageInput, StatusPageView } from '@uptime-canary/shared';
import { ApiFailure, describeFailure, publicRssUrl } from '../api';
import { useCanary } from '../context';
import { addressCaveat, badgeTargets } from '../badges';
import { bytesOf, plural, shortRev, span } from '../format';
import { useResource } from '../hooks';
import { RichText } from '../markdown';
import { notify } from '../notices';
import { SELECTION_KEYS, otherFields, selectionFields, writeSelection, type SelectionFields } from '../publicAvailability';
import { href, navigate, publicHref } from '../router';
import {
  REFRESH_CHOICES, accentVariables, buildPresentation, logoUrlProblem, presentationDraft, type PresentationDraft, type PresentationField,
} from '../statusPresentation';
import { BadgeList } from '../ui/badges';
import { Banner, Card, Chip, CopyButton, EmptyState, ErrorText, Modal, PageHead, ReadOnly, Rev, Tag, Time, TypeTag } from '../ui/bits';
import { CheckField, Disclosure, ErrorSummary, Seg, SelectField, TextAreaField, TextField } from '../ui/forms';
import { Glyph, Icon } from '../ui/icons';

export const ANNOUNCEMENT_STATES: { value: StatusPageAnnouncementState; label: string }[] = [
  { value: 'investigating', label: 'Investigating' },
  { value: 'identified', label: 'Identified' },
  { value: 'monitoring', label: 'Monitoring' },
  { value: 'resolved', label: 'Resolved' },
];

const SLUG = /^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$/u;
const ID = /^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,127}$/u;
const randomId = (prefix: string): string => `${prefix}-${Array.from(crypto.getRandomValues(new Uint8Array(5)), byte => byte.toString(36).padStart(2, '0')).join('').slice(0, 8)}`;
const slugify = (text: string): string => text.toLowerCase().normalize('NFKD').replace(/[^a-z0-9]+/gu, '-').replace(/^-+|-+$/gu, '').slice(0, 64).replace(/-+$/u, '');

/** Links to the public page on this dashboard, naming the nodes a visitor's browser should read. */
export function publicLink(slug: string, nodes: string[]): string {
  return `${location.origin}${location.pathname}${publicHref(slug, nodes)}`;
}

function useStatusPages() {
  const { api, connection } = useCanary();
  return useResource<StatusPageView[]>(`${connection.identity}|status-pages`, signal => api.statusPages(signal), 30_000);
}

function PublicLinkBuilder({ slug, enabled }: { slug: string; enabled: boolean }) {
  const { api, nodes, endpoint, selection } = useCanary();
  const peers = nodes.filter(node => node.id !== endpoint && node.url);
  const [chosen, setChosen] = useState<string[]>([api.baseUrl]);
  const all = [{ id: endpoint, url: api.baseUrl }, ...peers.map(node => ({ id: node.id, url: node.url! }))];
  const link = publicLink(slug, chosen);
  if (selection) {
    // Control plane: visitors read the page through this dashboard, from the node this link names.
    const viaDashboard = `${location.origin}${location.pathname}${publicHref(slug, [], selection.id)}`;
    return (
      <div className="linkbuilder">
        <p className="small">Visitors open this dashboard’s address, and its server reads the page from {selection.label} (<span className="mono">{endpoint}</span>) without signing in. Nothing in the link is a credential.</p>
        <code className="link-out">{viaDashboard}</code>
        <div className="row-wrap">
          <CopyButton value={viaDashboard} label="Copy public link" />
          <a className="btn sm" href={publicHref(slug, [], selection.id)}><Icon name="external" />Open</a>
        </div>
        {!enabled ? <p className="small fwarn"><Glyph tone="warn" />Not published: the node answers “not found” until you publish it.</p> : null}
        <p className="muted small">If a sign-in proxy protects this dashboard, visitors must get through it first. The public route never shows private IDs, targets or node diagnostics.</p>
      </div>
    );
  }
  return (
    <div className="linkbuilder">
      <p className="small">Visitors’ browsers read the page straight from a node’s public address, without signing in. Choose which nodes the link offers; the first is tried first and visitors can switch.</p>
      <div className="picklist compact">
        {all.map(node => (
          <label key={node.url}>
            <input type="checkbox" checked={chosen.includes(node.url)} onChange={event => setChosen(event.target.checked ? [...chosen, node.url] : chosen.filter(url => url !== node.url))} />
            <span><b className="mono">{node.id}</b> <span className="mono muted small">{node.url}</span></span>
          </label>
        ))}
      </div>
      {chosen.length ? (
        <>
          <code className="link-out">{link}</code>
          <div className="row-wrap">
            <CopyButton value={link} label="Copy public link" />
            <a className="btn sm" href={publicHref(slug, chosen)}><Icon name="external" />Open</a>
          </div>
        </>
      ) : <p className="muted small">Choose at least one node.</p>}
      {!enabled ? <p className="small fwarn"><Glyph tone="warn" />Not published: every node answers “not found” until you publish it.</p> : null}
      <p className="muted small">Peer addresses are what nodes use to reach each other; visitors need a route to them. The public route allows any origin to read it, sends no cookies and never shows private IDs, targets or node diagnostics.</p>
    </div>
  );
}

/** Badge addresses for the SAVED page, on one node the user picks. Drafts and conflicts get no links. */
function BadgeShare({ page, pendingChange }: { page: StatusPage; pendingChange?: boolean }) {
  const { api, nodes, endpoint, selection } = useCanary();
  const formId = useId();
  const choices = [{ id: endpoint, url: api.baseUrl }, ...nodes.filter(node => node.id !== endpoint && node.url).map(node => ({ id: node.id, url: node.url! }))];
  const [picked, setNodeUrl] = useState(api.baseUrl);
  const [nonce, setNonce] = useState(0);
  // A node that left the list (or a different endpoint) never keeps serving stale addresses.
  const nodeUrl = choices.some(choice => choice.url === picked) ? picked : api.baseUrl;
  if (!page.enabled) {
    return (
      <section className="badge-share" aria-labelledby={`${formId}-h`}>
        <h3 className="mini-h" id={`${formId}-h`}>Badges</h3>
        <p className="muted small">Publish the page to get badge links. While it’s a draft, every badge address answers “not found”.</p>
      </section>
    );
  }
  const caveat = addressCaveat(nodeUrl);
  return (
    <section className="badge-share" aria-labelledby={`${formId}-h`}>
      <div className="obs-h">
        <h3 className="mini-h" id={`${formId}-h`}>Badges</h3>
        <button type="button" className="icon-btn sm" onClick={() => setNonce(n => n + 1)} aria-label="Reload badge previews" data-tip="Reload previews"><Icon name="refresh" /></button>
      </div>
      <p className="small">Images for a README or another site, from one node’s public address, without signing in. A state badge shows the current state: up, down, pending or unknown, plus a maintenance segment during planned work. Services that publish observed history also get a 24 h observed badge with that node’s availability and coverage percentages.</p>
      <label className="inline-sel" htmlFor={`${formId}-node`}>Served by
        <select id={`${formId}-node`} value={nodeUrl} onChange={event => setNodeUrl(event.target.value)}>
          {choices.map(choice => <option key={choice.url} value={choice.url}>{choice.id} · {choice.url}</option>)}
        </select>
      </label>
      {caveat ? <p className="small fwarn"><Glyph tone="warn" />{caveat}</p> : null}
      {selection
        ? <BadgeList slug={page.slug} title={page.title} groups={savedBadgeGroups(page)} nodeUrl={api.baseUrl} registration={selection.id} nonce={nonce}
            pageLink={`${location.origin}${location.pathname}${publicHref(page.slug, [], selection.id)}`} />
        : <BadgeList slug={page.slug} title={page.title} groups={savedBadgeGroups(page)} nodeUrl={nodeUrl} pageLink={publicLink(page.slug, [nodeUrl])} nonce={nonce} />}
      <p className="muted small">A badge address contains the page address, the section’s public ID and the service’s public name. Changing any of them, or unpublishing, makes existing embeds show a broken image because the node answers “not found”. Renaming a section keeps its ID, so its badges keep working. An image shows one node’s view; it doesn’t switch nodes like the page does.</p>
      {pendingChange ? <p className="small fwarn"><Glyph tone="warn" />Unsaved changes alter or remove some of these addresses. Embeds of the current addresses stop working when you save.</p> : null}
    </section>
  );
}

function DeletePageDialog({ page, onClose, onDone }: { page: StatusPage; onClose: () => void; onDone: () => void }) {
  const { api, endpoint, stillCurrent } = useCanary();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const remove = async (): Promise<void> => {
    setBusy(true); setError(null);
    try {
      await api.deleteStatusPage(page.id, page.revision);
      if (!stillCurrent()) return;
      notify(`Deleted status page “${page.title}” on ${endpoint}. Other nodes stop publishing it when they sync.`);
      onDone();
      onClose();
    } catch (failure) {
      if (!stillCurrent()) return;
      setError(describeFailure(failure)); setBusy(false);
    }
  };
  return (
    <Modal title={`Delete ${page.title}?`} onClose={onClose} busy={busy}>
      <p>Its public address answers “not found” on {endpoint} immediately and on peers after they sync. A partitioned peer can keep publishing until then.</p>
      {error ? <div className="errsum" role="alert"><Glyph tone="down" /><div><b>Not deleted</b><p>{error}</p></div></div> : null}
      <div className="row-wrap modal-acts">
        <button type="button" className="btn danger-solid" onClick={() => { void remove(); }} disabled={busy}><Icon name="trash" />{busy ? 'Deleting…' : 'Delete page'}</button>
        <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
      </div>
    </Modal>
  );
}

export function StatusPagesView() {
  const { endpoint, editable, search, api } = useCanary();
  const pages = useStatusPages();
  const [deleting, setDeleting] = useState<StatusPage | null>(null);
  const [linkFor, setLinkFor] = useState<StatusPage | null>(null);
  const unsupported = pages.error instanceof ApiFailure && pages.error.status === 404;
  const q = search.trim().toLowerCase();
  const list = (pages.data ?? []).filter(view => !view.statusPage.deleted && (!q || `${view.statusPage.title} ${view.statusPage.slug}`.toLowerCase().includes(q)))
    .sort((a, b) => a.statusPage.title.localeCompare(b.statusPage.title));
  return (
    <div className="view">
      <PageHead
        title="Status pages"
        sub={<>Public pages you publish from <span className="mono">{endpoint}</span>’s configuration. Each node serves them as JSON; this dashboard renders them.</>}
        actions={editable ? <a className="btn primary" href={href('status-pages', 'new')}><Icon name="plus" />New status page</a> : <ReadOnly>Viewers can’t publish</ReadOnly>}
      />
      <div className="guar">
        <div><b><Icon name="lock" />Only what you choose</b><p>Pages show the public names you type for monitors you pick. Private names, targets, IDs, errors and node details never appear.</p></div>
        <div><b><Icon name="pulse" />Live state, opt-in history</b><p>Each monitor shows its current state from the answering node: unknown is never shown as up. A service you opt in also shows the last 24 hours as that node observed them. Never an SLA.</p></div>
        <div><b><Icon name="megaphone" />Your announcements</b><p>Announcements are text you write. Incidents are never published automatically.</p></div>
      </div>
      <Card className={pages.loading && pages.data ? 'refetching' : undefined}>
        {unsupported ? <EmptyState title={`${endpoint} doesn’t publish status pages`}>It probably runs an older Uptime-Canary version.</EmptyState>
          : pages.error && !pages.data ? <Banner tone="down" title="Status pages could not be loaded" actions={<button type="button" className="btn sm" onClick={pages.reload}><Icon name="refresh" />Try again</button>}>{describeFailure(pages.error)}</Banner>
          : !pages.data ? <p className="muted">Loading status pages from {endpoint}…</p>
          : list.length ? (
            <ul className="chan-list">
              {list.map(view => {
                const page = view.statusPage;
                const monitors = page.groups.reduce((n, group) => n + group.monitors.length, 0);
                const observed = page.groups.reduce((n, group) => n + group.monitors.filter(sel => sel.showAvailability === true).length, 0);
                return (
                  <li key={page.id}>
                    <span className="prov-ic" aria-hidden="true"><Icon name="globe" /></span>
                    <div className="grow">
                      <div className="t">
                        <b>{page.title}</b>
                        {view.configConflict ? <a href={href('config')} className="nowrap small"><Glyph tone="serious" /> conflict · not published</a>
                          : page.enabled ? <Chip tone="up" label="Published" /> : <Chip tone="paused" label="Draft" />}
                      </div>
                      <div className="m">
                        <span className="mono">/status/{page.slug}</span>
                        <span className="muted"> · {plural(page.groups.length, 'section')}, {plural(monitors, 'monitor')}{observed ? `, ${observed} with 24 h history` : ''} · {plural(page.announcements.length, 'announcement')} · <Rev revision={page.revision} /></span>
                      </div>
                    </div>
                    <div className="acts">
                      <button type="button" className="btn sm" onClick={() => setLinkFor(page)} disabled={view.configConflict} aria-label={`Share ${page.title}`}><Icon name="external" />Share</button>
                      {editable ? <>
                        <a className={`btn sm${view.configConflict ? ' is-disabled' : ''}`} href={view.configConflict ? undefined : href('status-pages', page.id, 'edit')} aria-disabled={view.configConflict || undefined}><Icon name="edit" />Edit</a>
                        <button type="button" className="btn sm danger" onClick={() => setDeleting(page)} disabled={view.configConflict} aria-label={`Delete ${page.title}`}><Icon name="trash" /></button>
                      </> : null}
                    </div>
                  </li>
                );
              })}
            </ul>
          ) : (
            <EmptyState title={pages.data.length ? 'Nothing matches' : 'No status pages yet'} action={!pages.data.length && editable ? <a className="btn primary" href={href('status-pages', 'new')}><Icon name="plus" />New status page</a> : undefined}>
              {pages.data.length ? 'Try another search.' : 'Publish a page for your users with the monitors and wording you choose.'}
            </EmptyState>
          )}
        <p className="foot-note">Pages publish only when saved as published. Duplicate slugs created on separate sides of a partition answer “not found” until one is renamed. Share a published page as a link or as status badges. Custom domains, custom CSS and history beyond the last 24 hours aren’t available.</p>
      </Card>
      {deleting ? <DeletePageDialog page={deleting} onClose={() => setDeleting(null)} onDone={pages.reload} /> : null}
      {linkFor ? <Modal title={`Share: ${linkFor.title}`} onClose={() => setLinkFor(null)} wide><PublicLinkBuilder slug={linkFor.slug} enabled={linkFor.enabled} /><BadgeShare page={linkFor} /><p className="muted small">Based on {api.baseUrl}.</p></Modal> : null}
    </div>
  );
}

// ---------- editor

/** `other`: stored fields this editor doesn't know, such as ones from a newer node; a save sends them back unchanged. */
interface SelDraft extends SelectionFields { key: number; other: Record<string, unknown> }
interface GroupDraft { key: number; id: string; name: string; monitors: SelDraft[]; other: Record<string, unknown> }
interface NoteDraft { key: number; id: string; title: string; message: string; state: StatusPageAnnouncementState; createdAt: number; updatedAt: number; pinned: boolean; touched: boolean; other: Record<string, unknown> }
/** `look`: the protocol 19 presentation (theme, accent, logo, footer, refresh, Markdown, public address and RSS feed). */
interface PageDraft { slug: string; slugTouched: boolean; title: string; description: string; enabled: boolean; groups: GroupDraft[]; notes: NoteDraft[]; look: PresentationDraft; other: Record<string, unknown> }
let draftKey = 0;

const PAGE_KEYS = ['slug', 'title', 'description', 'enabled', 'groups', 'announcements', 'appearance', 'publicUrl', 'rssEnabled', 'id', 'revision', 'updatedAt', 'deleted'] as const;
const GROUP_KEYS = ['id', 'name', 'monitors'] as const;
const NOTE_KEYS = ['id', 'title', 'message', 'state', 'createdAt', 'updatedAt', 'pinned'] as const;
const newSelection = (): SelDraft => ({ key: ++draftKey, monitorId: '', name: '', showCertificate: false, showTags: false, showAvailability: false, other: {} });

function pageDraft(page: StatusPage | undefined): PageDraft {
  return {
    slug: page?.slug ?? '', slugTouched: Boolean(page), title: page?.title ?? '', description: page?.description ?? '', enabled: page?.enabled ?? false,
    groups: (page?.groups ?? [{ id: randomId('section'), name: 'Services', monitors: [] }]).map(group => ({
      key: ++draftKey, id: group.id, name: group.name, other: otherFields(group, GROUP_KEYS),
      monitors: group.monitors.map(sel => ({ key: ++draftKey, ...selectionFields(sel), other: otherFields(sel, SELECTION_KEYS) })),
    })),
    notes: (page?.announcements ?? []).map(note => ({
      key: ++draftKey, id: note.id, title: note.title, message: note.message, state: note.state, createdAt: note.createdAt, updatedAt: note.updatedAt, pinned: note.pinned,
      touched: false, other: otherFields(note, NOTE_KEYS),
    })),
    look: presentationDraft(page),
    other: page ? otherFields(page, PAGE_KEYS) : {},
  };
}

type PField = 'title' | 'slug' | 'description' | 'groups' | 'notes' | PresentationField;
const PFIELD_LABEL: Record<PField, string> = {
  title: 'Title', slug: 'Address', description: 'Description', groups: 'Sections', notes: 'Announcements',
  theme: 'Theme', accent: 'Accent color', logo: 'Logo image URL', footer: 'Footer', refresh: 'Refresh', publicUrl: 'Public page address', rss: 'RSS feed',
};
const plainLine = (text: string, limit: number): boolean => Boolean(text.trim()) && bytesOf(text) <= limit && !/[\x00-\x1f\x7f]/u.test(text);
const plainBlock = (text: string, limit: number): boolean => bytesOf(text) <= limit && !/[\x00-\x09\x0b-\x1f\x7f]/u.test(text);

/** `stored` is the saved page being edited, whose appearance object (with any keys a newer node added) is built upon. */
function buildPage(d: PageDraft, now: number, stored?: StatusPage): { input?: StatusPageInput; errors: Partial<Record<PField, string>>; rowErrors: Record<number, string> } {
  const errors: Partial<Record<PField, string>> = {};
  const rowErrors: Record<number, string> = {};
  if (!plainLine(d.title, 128)) errors.title = 'Enter a title of at most 128 bytes on one line.';
  if (!SLUG.test(d.slug)) errors.slug = 'Use 1–64 lowercase letters, digits and hyphens, starting and ending with a letter or digit.';
  if (!plainBlock(d.description, 2048)) errors.description = 'Use at most 2,048 bytes. Line breaks are fine.';
  if (d.groups.length > 16) errors.groups = 'Use at most 16 sections.';
  const groupIds = new Set<string>();
  const monitorIds = new Set<string>();
  let total = 0;
  for (const group of d.groups) {
    if (!plainLine(group.name, 128)) rowErrors[group.key] = 'Name this section (at most 128 bytes).';
    if (!ID.test(group.id) || groupIds.has(group.id)) rowErrors[group.key] = 'Each section needs a unique public ID (letters, digits, - _ . :).';
    groupIds.add(group.id);
    for (const sel of group.monitors) {
      total++;
      if (!sel.monitorId) rowErrors[sel.key] = 'Choose a monitor.';
      else if (monitorIds.has(sel.monitorId)) rowErrors[sel.key] = 'This monitor is already on the page.';
      else if (!plainLine(sel.name, 128)) rowErrors[sel.key] = 'Type the public name (at most 128 bytes). Private names are never published automatically.';
      monitorIds.add(sel.monitorId);
    }
  }
  if (total > 64) errors.groups = 'Use at most 64 monitors on one page.';
  if (Object.keys(rowErrors).some(key => d.groups.some(group => group.key === Number(key) || group.monitors.some(sel => sel.key === Number(key))))) errors.groups ??= 'Fix the highlighted sections.';
  if (d.notes.length > 50) errors.notes = 'Use at most 50 announcements.';
  const noteIds = new Set<string>();
  for (const note of d.notes) {
    if (!plainLine(note.title, 128)) rowErrors[note.key] = 'Give the announcement a title (at most 128 bytes).';
    else if (!note.message.trim() || !plainBlock(note.message, 2048)) rowErrors[note.key] = 'Write a message of at most 2,048 bytes.';
    else if (!ID.test(note.id) || noteIds.has(note.id)) rowErrors[note.key] = 'Announcement IDs must be unique.';
    noteIds.add(note.id);
  }
  if (d.notes.some(note => rowErrors[note.key])) errors.notes ??= 'Fix the highlighted announcements.';
  const presentation = buildPresentation(stored, d.look, errors);
  if (Object.keys(errors).length) return { errors, rowErrors };
  // Opt-in flags are written only when on, so a page that publishes no history saves exactly as before protocol 15.
  const input: StatusPageInput = {
    ...d.other, slug: d.slug, title: d.title.trim(), description: d.description, enabled: d.enabled,
    groups: d.groups.map(group => ({
      ...group.other, id: group.id, name: group.name.trim(),
      monitors: group.monitors.map(sel => writeSelection(sel, sel.other)),
    })),
    announcements: d.notes.map((note): StatusPageAnnouncement => ({
      ...note.other, id: note.id, title: note.title.trim(), message: note.message, state: note.state, pinned: note.pinned,
      createdAt: note.createdAt, updatedAt: note.touched ? Math.max(now, note.createdAt) : note.updatedAt,
    })),
    // Written only when set, as Go's omitempty stores them, so a page without them saves exactly as before.
    ...presentation,
  };
  return { input, errors, rowErrors };
}

/** The editor's page model, for scripts/status-presentation-pure.mjs. */
export const statusPageModel = { pageDraft, buildPage };

/** Badge groups for a saved page: public section IDs and names only, and which services publish observed history. */
function savedBadgeGroups(page: StatusPage): { id: string; name: string; monitors: { name: string; observed: boolean }[] }[] {
  return page.groups.map(group => ({ id: group.id, name: group.name, monitors: group.monitors.map(sel => ({ name: sel.name, observed: sel.showAvailability === true })) }));
}

/** Every badge address a page would answer, as comparable keys: the state badge per unique service, and its observed badge. */
function badgeKeys(groups: { id: string; name: string; monitors: { name: string; observed: boolean }[] }[]): Set<string> {
  const keys = new Set<string>();
  for (const target of badgeTargets(groups)) {
    if (target.matches !== 1) continue;
    keys.add(JSON.stringify([target.group, target.monitor]));
    if (target.observed) keys.add(JSON.stringify([target.group, target.monitor, 'uptime']));
  }
  return keys;
}

/** Whether saving the draft would change or remove a badge address of the saved page. */
function badgesChange(saved: StatusPage, draft: PageDraft): boolean {
  if (saved.slug !== draft.slug || saved.enabled !== draft.enabled) return true;
  const after = badgeKeys(draft.groups.map(group => ({ id: group.id, name: group.name, monitors: group.monitors.map(sel => ({ name: sel.name.trim(), observed: sel.showAvailability })) })));
  return [...badgeKeys(savedBadgeGroups(saved))].some(key => !after.has(key));
}

/** The dashboard's current light or dark mode, for previewing an accent against the right surface. */
const currentMode = (): 'light' | 'dark' => typeof document !== 'undefined' && document.documentElement?.dataset?.mode === 'dark' ? 'dark' : 'light';

/**
 * The page's public look: theme default, accent color, logo, footer, visitors' refresh interval and Markdown. Typed
 * settings only, never CSS, HTML or script. The logo is an external image every visitor's browser loads, so the editor
 * loads it only when asked to; the preview shows the text with the accent and the Markdown choice.
 */
function AppearanceCard({ draft, stored, setLook, errors, disabled, formId }: {
  draft: PageDraft; stored: StatusPage | undefined; setLook: (change: Partial<PresentationDraft>) => void; errors: Partial<Record<PField, string>>; disabled: boolean; formId: string;
}) {
  const look = draft.look;
  const [open, setOpen] = useState(false);
  const [logoShown, setLogoShown] = useState<string | null>(null);
  const storedLook = presentationDraft(stored);
  const hexOk = /^#[0-9a-fA-F]{6}$/u.test(look.accent.trim());
  const refreshOptions = [
    { value: '0', label: 'Every 30 s (default)' },
    ...REFRESH_CHOICES.map(ms => ({ value: String(ms), label: `Every ${span(ms)}` })),
    ...(look.refreshMs && !(REFRESH_CHOICES as readonly number[]).includes(look.refreshMs) ? [{ value: String(look.refreshMs), label: `Every ${span(look.refreshMs)} (as stored)` }] : []),
  ];
  // Only an address the node would accept is ever loaded, and only after the editor asks.
  const logoReady = Boolean(look.logo.trim()) && !logoUrlProblem(look.logo.trim());
  const firstNote = draft.notes[0];
  const accent = hexOk ? look.accent.trim() : '';
  const summary = [look.theme ? `${look.theme} theme` : '', accent ? accent : '', look.logo ? 'logo' : '', look.footer ? 'footer' : '', look.markdown ? 'Markdown' : ''].filter(Boolean).join(' · ') || 'Defaults';
  return (
    <Card title="Appearance" hint="Public. Typed settings only: no custom CSS, HTML or scripts.">
      <div className="form">
        <Seg legend="Theme" name={`${formId}-theme`} value={look.theme} onChange={theme => setLook({ theme })} disabled={disabled} full
          options={[{ value: '', label: 'Visitor’s setting' }, ...(look.theme === 'system' || storedLook.theme === 'system' ? [{ value: 'system', label: 'System' }] : []), { value: 'light', label: 'Light' }, { value: 'dark', label: 'Dark' }]}
          hint="The theme for visitors who haven’t chosen light or dark themselves. They can still switch with the theme button." />
        <div className="fld">
          <label htmlFor={`${formId}-accent`}>Accent color <span className="muted">(optional)</span></label>
          <div className="row-wrap accent-in">
            <input type="color" aria-label="Pick the accent color" value={hexOk ? look.accent.trim().toLowerCase() : '#2a78d6'} onChange={event => setLook({ accent: event.target.value })} disabled={disabled} />
            <input id={`${formId}-accent`} type="text" className="mono" value={look.accent} placeholder="#2a78d6" maxLength={16} spellCheck={false} autoComplete="off" disabled={disabled}
              aria-invalid={errors.accent ? true : undefined} aria-describedby={`${formId}-accent-h${errors.accent ? ` ${formId}-accent-e` : ''}`} onChange={event => setLook({ accent: event.target.value })} />
            {look.accent ? <button type="button" className="linkbtn" onClick={() => setLook({ accent: '' })} disabled={disabled}>Use the default</button> : null}
          </div>
          <small id={`${formId}-accent-h`}>A hex color like #2a78d6, for marks and outlines. Links and buttons use it only where their text stays readable (4.5:1).</small>
          <ErrorText id={`${formId}-accent-e`}>{errors.accent}</ErrorText>
        </div>
        <TextField full mono label="Logo image URL (optional)" value={look.logo} onChange={logo => setLook({ logo })} error={errors.logo} disabled={disabled} placeholder="https://www.example.com/logo.png"
          hint="An https:// image address without a ?query or #fragment, at most 2,048 bytes. Every visitor’s browser loads it from that server when the page opens; the node never fetches it. Use an image you control." />
        {look.logo.trim() ? (
          <div className="fld full">
            {logoShown === look.logo.trim() && logoReady
              ? <span className="logo-preview"><img src={logoShown} alt="Logo preview" referrerPolicy="no-referrer" decoding="async" /></span>
              : <button type="button" className="btn sm" onClick={() => setLogoShown(look.logo.trim())} disabled={disabled || !logoReady}><Icon name="eye" />Load a preview</button>}
            <small>This dashboard loads the image only when you ask, from your browser.</small>
          </div>
        ) : null}
        <TextAreaField full optional label="Footer" value={look.footer} onChange={footer => setLook({ footer })} error={errors.footer} rows={2} disabled={disabled}
          hint="Public text at the bottom of the page, up to 2,048 bytes. Line breaks are fine." />
        <SelectField label="Visitors’ pages refresh" value={String(look.refreshMs)} options={refreshOptions} onChange={value => setLook({ refreshMs: Number(value) })} error={errors.refresh} disabled={disabled}
          hint="How often an open page reads its node again. A hidden tab waits." />
        <CheckField label="Format text with Markdown" checked={look.markdown} onChange={markdown => setLook({ markdown })} disabled={disabled}
          hint={<>For the description, the footer and announcements: <span className="mono">**bold**</span>, <span className="mono">*italic*</span>, <span className="mono">`code`</span>, fenced code, “-” and “1.” lists, and <span className="mono">[links](https://…)</span> to http:// or https:// addresses. HTML, scripts, images and other link schemes stay plain text. The RSS feed always carries the text as written.</>} />
        <Disclosure title="Preview" summary={summary} open={open} onToggle={() => setOpen(value => !value)}>
          <div className="full sp-preview" style={accentVariables(accent, currentMode()) as CSSProperties}>
            <div className="sp-preview-h"><span className="pv-mark" aria-hidden="true" /><b>{draft.title.trim() || 'Status'}</b></div>
            {draft.description ? <RichText text={draft.description} markdown={look.markdown} className="pub-desc" /> : <p className="muted small">No description.</p>}
            {firstNote?.message ? (
              <article className="pub-note">
                <div className="pub-note-h"><h3>{firstNote.title || 'Announcement'}</h3></div>
                <RichText text={firstNote.message} markdown={look.markdown} />
              </article>
            ) : null}
            {look.footer ? <RichText text={look.footer} markdown={look.markdown} className="pub-foot-text" /> : null}
            <p className="muted small">The text with this page’s accent and Markdown choice, in this dashboard’s current theme. Sections, states and the logo are left out.</p>
          </div>
        </Disclosure>
      </div>
    </Card>
  );
}

/**
 * The page's public address and its opt-in RSS feed of announcements. The feed carries only what the page's
 * announcements say, with links to the public address; never incidents, history, monitors or notifications.
 */
function FeedCard({ draft, current, setLook, errors, disabled }: {
  draft: PageDraft; current: StatusPage | undefined; setLook: (change: Partial<PresentationDraft>) => void; errors: Partial<Record<PField, string>>; disabled: boolean;
}) {
  const { api, selection } = useCanary();
  const look = draft.look;
  const suggested = selection
    ? `${location.origin}${location.pathname}${publicHref(draft.slug || 'status', [], selection.id)}`
    : publicLink(draft.slug || 'status', [api.baseUrl]);
  let feed: string | null = null;
  if (current?.enabled && current.rssEnabled && current.publicUrl) {
    try { feed = publicRssUrl(api.baseUrl, current.slug, selection ? selection.id : undefined); } catch { feed = null; }
  }
  return (
    <Card title="Public address and RSS">
      <div className="form">
        <TextField full mono label="Public page address (optional)" value={look.publicUrl} onChange={publicUrl => setLook({ publicUrl })} error={errors.publicUrl} disabled={disabled} placeholder="https://status.example.com/"
          hint="The complete http:// or https:// address visitors open, at most 2,048 bytes; a #/status route and a registration selector are fine. The RSS feed links to it. The node never fetches it." />
        {look.publicUrl.trim() !== suggested ? <div className="full"><button type="button" className="linkbtn small" onClick={() => setLook({ publicUrl: suggested })} disabled={disabled || !draft.slug}>Use this dashboard’s public link</button></div> : null}
        <CheckField label="Publish an RSS feed of announcements" checked={look.rss} onChange={rss => setLook({ rss })} disabled={disabled}
          hint="Off by default. Feed readers get the announcements you write here, newest first, linking to the address above. Never incidents, history, monitors or private names; no email or webhook subscriptions." />
        {errors.rss ? <div className="full"><ErrorText id="sp-rss-e">{errors.rss}</ErrorText></div> : null}
        {feed ? (
          <div className="fld full">
            <span className="flabel">Feed address</span>
            <code className="link-out">{feed}</code>
            <div className="row-wrap"><CopyButton value={feed} label="Copy feed address" /></div>
          </div>
        ) : current && look.rss && !(current.enabled && current.rssEnabled && current.publicUrl) ? <p className="full muted small">Save{current.enabled ? '' : ' and publish'} the page to get its feed address.</p> : null}
      </div>
    </Card>
  );
}

function move<T>(list: T[], index: number, delta: number): T[] {
  const next = [...list];
  const target = index + delta;
  if (target < 0 || target >= next.length) return list;
  [next[index], next[target]] = [next[target]!, next[index]!];
  return next;
}

function MoveButtons({ index, length, onMove, label, disabled }: { index: number; length: number; onMove: (delta: number) => void; label: string; disabled: boolean }) {
  return (
    <span className="movers">
      <button type="button" className="icon-btn sm" onClick={() => onMove(-1)} disabled={disabled || index === 0} aria-label={`Move ${label} up`}><Icon name="arrowUp" /></button>
      <button type="button" className="icon-btn sm" onClick={() => onMove(1)} disabled={disabled || index === length - 1} aria-label={`Move ${label} down`}><Icon name="arrowDown" /></button>
    </span>
  );
}

export function StatusPageFormView({ id }: { id?: string }) {
  const { endpoint, editable, api, monitorMap, assessed, overview, stillCurrent, tags } = useCanary();
  const pages = useStatusPages();
  const existing = id ? pages.data?.find(view => view.statusPage.id === id) : undefined;
  const [draft, setDraft] = useState<PageDraft | null>(() => id ? null : pageDraft(undefined));
  const [base, setBase] = useState<StatusPage | undefined>();
  const [showErrors, setShowErrors] = useState(false);
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<{ message: string; stale: boolean } | null>(null);
  const [attempts, setAttempts] = useState(0);
  const summaryRef = useRef<HTMLDivElement>(null);
  const formId = useId();
  useEffect(() => { if (attempts) summaryRef.current?.focus(); }, [attempts, failure]);
  useEffect(() => {
    if (id && !draft && existing && !existing.configConflict) { setDraft(pageDraft(existing.statusPage)); setBase(existing.statusPage); }
  }, [id, draft, existing]);
  const result = useMemo(() => draft ? buildPage(draft, Date.now(), base) : null, [draft, base]);

  if (!editable) return <div className="view"><PageHead title="Status page" /><Card><EmptyState title="Viewers can’t publish status pages">Sign in with an editor or admin account.</EmptyState></Card></div>;
  if (pages.error instanceof ApiFailure && pages.error.status === 404) return <div className="view"><PageHead title="Status page" /><Card><EmptyState title={`${endpoint} doesn’t publish status pages`}>It probably runs an older Uptime-Canary version.</EmptyState></Card></div>;
  if (id && !existing) return <div className="view"><PageHead crumbs={<a href={href('status-pages')}>Status pages</a>} title="Edit status page" /><Card>{pages.data ? <EmptyState title="Status page not found">{endpoint} doesn’t know this page. It may have been deleted.</EmptyState> : <p className="muted">Loading from {endpoint}…</p>}</Card></div>;
  if (existing?.configConflict) return <div className="view"><PageHead crumbs={<a href={href('status-pages')}>Status pages</a>} title={`Edit ${existing.statusPage.title}`} /><Banner title="This page has a configuration conflict" actions={<a className="btn sm" href={href('config')}><Icon name="merge" />Resolve in Configuration</a>}>It isn’t published while versions disagree. Resolve it first.</Banner></div>;
  if (!draft || !result) return <div className="view"><PageHead title="Edit status page" /><Card><p className="muted">Loading…</p></Card></div>;

  const { input, errors, rowErrors } = result;
  const visible = showErrors ? errors : {};
  const visibleRows = showErrors ? rowErrors : {};
  const update = (change: Partial<PageDraft>): void => setDraft(d => d ? { ...d, ...change } : d);
  const setLook = (change: Partial<PresentationDraft>): void => setDraft(d => d ? { ...d, look: { ...d.look, ...change } } : d);
  const setGroup = (key: number, change: Partial<GroupDraft>): void => update({ groups: draft.groups.map(group => group.key === key ? { ...group, ...change } : group) });
  const setSel = (groupKey: number, key: number, change: Partial<SelDraft>): void => setGroup(groupKey, { monitors: draft.groups.find(group => group.key === groupKey)!.monitors.map(sel => sel.key === key ? { ...sel, ...change } : sel) });
  const setNote = (key: number, change: Partial<NoteDraft>): void => update({ notes: draft.notes.map(note => note.key === key ? { ...note, ...change, touched: true } : note) });
  const chosen = new Set(draft.groups.flatMap(group => group.monitors.map(sel => sel.monitorId)));
  const total = draft.groups.reduce((n, group) => n + group.monitors.length, 0);
  const current = existing?.statusPage;
  const changedElsewhere = Boolean(current && base && current.revision !== base.revision);
  const tagsAvailable = !tags.unsupported;

  const submit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (saving) return;
    setShowErrors(true); setFailure(null);
    if (!input) { setAttempts(n => n + 1); return; }
    setSaving(true);
    try {
      const saved = current && base ? await api.updateStatusPage(current.id, { ...input, revision: base.revision }) : await api.createStatusPage(input);
      if (!stillCurrent()) return;
      notify(`${current ? 'Saved' : 'Created'} status page “${saved.title}” on ${endpoint} (revision ${shortRev(saved.revision)}). ${saved.enabled ? 'It’s published.' : 'It’s a draft: not published yet.'}`);
      pages.reload();
      navigate(href('status-pages'));
    } catch (error) {
      if (!stillCurrent()) return;
      const stale = error instanceof ApiFailure && error.status === 409;
      setFailure({ message: stale ? (current ? 'Someone saved a newer revision first, or nodes now hold conflicting versions.' : 'Another page already uses this address on this node, or nodes hold conflicting versions.') : describeFailure(error, { preferDetail: true }), stale });
      setAttempts(n => n + 1);
    } finally { setSaving(false); }
  };
  const loadLatest = (): void => { if (current) { setDraft(pageDraft(current)); setBase(current); setFailure(null); setShowErrors(false); } };
  const errorList = Object.entries(visible).map(([field, message]) => `${PFIELD_LABEL[field as PField]}: ${message}`);
  const monitorOptions = (selected: string) => [
    { value: '', label: 'Choose a monitor…' },
    ...assessed.filter(({ view }) => view.monitor.id === selected || !chosen.has(view.monitor.id)).map(({ view }) => ({ value: view.monitor.id, label: `${view.monitor.name} (${view.monitor.type})` })),
    ...(selected && !monitorMap.has(selected) ? [{ value: selected, label: `Unknown monitor ${selected.slice(0, 8)}` }] : []),
  ];

  return (
    <div className="view">
      <PageHead
        crumbs={<a href={href('status-pages')}>Status pages</a>}
        title={current ? `Edit ${current.title}` : 'New status page'}
        sub={current ? <>Editing revision <Rev revision={base?.revision ?? current.revision} />. Saving replaces the whole page on <span className="mono">{endpoint}</span>.</> : <>Creates a page on <span className="mono">{endpoint}</span>, shared with every peer. It stays a draft until you publish it.</>}
      />
      {changedElsewhere ? <Banner title={<>This page changed on {endpoint} while you were editing</>} actions={<button type="button" className="btn sm" onClick={loadLatest}><Icon name="refresh" />Load latest (discards your edits)</button>}>Saving your version will be rejected because it’s based on an older revision.</Banner> : null}
      <form className="sp-form" onSubmit={event => { void submit(event); }} noValidate aria-busy={saving}>
        <div className="g2">
          <div className="vstack">
            <Card title="Page">
              <div className="form">
                <ErrorSummary ref={summaryRef} title={failure ? (failure.stale ? 'Not saved: the page changed, conflicts, or its address is taken' : 'Not saved') : 'Check these fields'} errors={failure ? [] : errorList}>
                  {failure ? <p>{failure.message}{failure.stale && current ? <> <button type="button" className="linkbtn" onClick={loadLatest}>Load the latest version</button> and apply your change again.</> : null}</p> : null}
                </ErrorSummary>
                <TextField full label="Title" value={draft.title} error={visible.title} disabled={saving} autoFocus={!current} hint="Public. Shown as the page heading."
                  onChange={value => update({ title: value, ...(draft.slugTouched ? {} : { slug: slugify(value) }) })} />
                <div className="fld full">
                  <label htmlFor={`${formId}-slug`}>Address</label>
                  <div className="prefixed"><span className="mono muted">/status/</span><input id={`${formId}-slug`} type="text" className="mono" value={draft.slug} maxLength={64} disabled={saving} spellCheck={false} autoComplete="off"
                    aria-invalid={visible.slug ? true : undefined} aria-describedby={`${formId}-slug-h${visible.slug ? ` ${formId}-slug-e` : ''}`}
                    onChange={event => update({ slug: event.target.value.toLowerCase(), slugTouched: true })} /></div>
                  <small id={`${formId}-slug-h`}>Lowercase letters, digits and hyphens. Unique among this node’s pages, including drafts.</small>
                  <ErrorText id={`${formId}-slug-e`}>{visible.slug}</ErrorText>
                </div>
                <TextAreaField full optional label="Description" value={draft.description} onChange={value => update({ description: value })} error={visible.description} rows={3} disabled={saving}
                  hint="Public plain text, up to 2,048 bytes. Shown under the title." />
                <CheckField label="Published" checked={draft.enabled} onChange={value => update({ enabled: value })} disabled={saving}
                  hint={draft.enabled ? 'Anyone with the address can read it from every node that has this revision.' : 'A draft: every node answers “not found” for its address.'} />
              </div>
            </Card>

            <AppearanceCard draft={draft} stored={base} setLook={setLook} errors={visible} disabled={saving} formId={formId} />

            <Card title="Sections and monitors" hint={`${plural(draft.groups.length, 'section')} of 16 · ${plural(total, 'monitor')} of 64`}
              actions={<button type="button" className="btn sm" onClick={() => update({ groups: [...draft.groups, { key: ++draftKey, id: randomId('section'), name: '', monitors: [], other: {} }] })} disabled={saving || draft.groups.length >= 16}><Icon name="plus" />Add section</button>}>
              {visible.groups ? <ErrorText id={`${formId}-groups-e`}>{visible.groups}</ErrorText> : null}
              {draft.groups.length ? draft.groups.map((group, gIndex) => (
                <fieldset key={group.key} className="sp-group" disabled={saving}>
                  <legend className="vh">Section {gIndex + 1}</legend>
                  <div className="sp-group-h">
                    <input type="text" aria-label={`Section ${gIndex + 1} name`} placeholder="Section name, like Website or API" value={group.name} maxLength={128} onChange={event => setGroup(group.key, { name: event.target.value })}
                      aria-invalid={visibleRows[group.key] ? true : undefined} />
                    <MoveButtons index={gIndex} length={draft.groups.length} onMove={delta => update({ groups: move(draft.groups, gIndex, delta) })} label={`section ${group.name || gIndex + 1}`} disabled={saving} />
                    <button type="button" className="icon-btn sm" onClick={() => update({ groups: draft.groups.filter(other => other.key !== group.key) })} aria-label={`Remove section ${group.name || gIndex + 1}`}><Icon name="trash" /></button>
                  </div>
                  {visibleRows[group.key] ? <ErrorText id={`${formId}-g${group.key}`}>{visibleRows[group.key]}</ErrorText> : null}
                  {group.monitors.length ? (
                    <ol className="sp-sels">
                      {group.monitors.map((sel, sIndex) => {
                        const view = monitorMap.get(sel.monitorId);
                        const label = sel.name || view?.monitor.name || `monitor ${sIndex + 1}`;
                        return (
                          <li key={sel.key} className={visibleRows[sel.key] ? 'bad' : undefined}>
                            <div className="sp-sel-main">
                              <SelectField label="Private monitor" value={sel.monitorId} options={monitorOptions(sel.monitorId)} onChange={value => setSel(group.key, sel.key, { monitorId: value })} />
                              <div className="fld">
                                <label htmlFor={`${formId}-n${sel.key}`}>Public name</label>
                                <input id={`${formId}-n${sel.key}`} type="text" value={sel.name} maxLength={128} placeholder="What visitors see" onChange={event => setSel(group.key, sel.key, { name: event.target.value })} aria-invalid={visibleRows[sel.key] ? true : undefined} />
                                {view && !sel.name ? <button type="button" className="linkbtn small" onClick={() => setSel(group.key, sel.key, { name: view.monitor.name.slice(0, 128) })}>Publish the private name “{view.monitor.name}”</button> : null}
                              </div>
                            </div>
                            <div className="sp-sel-opts">
                              <CheckField full={false} label="Certificate dates" checked={sel.showCertificate} onChange={value => setSel(group.key, sel.key, { showCertificate: value })} hint="When every node agrees on them" />
                              {tagsAvailable ? <CheckField full={false} label="Public tags" checked={sel.showTags} onChange={value => setSel(group.key, sel.key, { showTags: value })} hint="Only tags marked public on the monitor" /> : null}
                              <CheckField full={false} label="24 h history" checked={sel.showAvailability} onChange={value => setSel(group.key, sel.key, { showAvailability: value })} hint="Observed uptime, off by default" />
                              {view ? <span className="muted small"><TypeTag type={view.monitor.type} /> {view.monitor.enabled ? '' : 'paused · shows unknown'}</span> : sel.monitorId ? <span className="muted small">Not known to {endpoint}: shows unknown</span> : null}
                              <span className="grow" />
                              <MoveButtons index={sIndex} length={group.monitors.length} onMove={delta => setGroup(group.key, { monitors: move(group.monitors, sIndex, delta) })} label={label} disabled={saving} />
                              <button type="button" className="icon-btn sm" onClick={() => setGroup(group.key, { monitors: group.monitors.filter(other => other.key !== sel.key) })} aria-label={`Remove ${label}`}><Icon name="close" /></button>
                            </div>
                            {sel.showAvailability ? (
                              <p className="sp-obs-note small">
                                <Icon name="pulse" />
                                <span>
                                  Visitors see this service’s last 24 hours as the node answering them recorded them: availability, coverage and hourly up and down time, including when outages happened. Each node keeps its own record; nothing is merged across nodes, and it isn’t an SLA. It also gets a 24 h observed badge.
                                  {' '}{!view ? (sel.monitorId ? `Unknown to ${endpoint}, so ${endpoint} shows it as unavailable.` : '')
                                    : !view.monitor.enabled ? 'While the monitor is paused, every node shows it as unavailable.'
                                    : <>Only nodes that run this monitor have it (<span className="mono">{view.monitor.nodeIds.join(', ')}</span>); other nodes show it as unavailable.</>}
                                </span>
                              </p>
                            ) : null}
                            {visibleRows[sel.key] ? <ErrorText id={`${formId}-s${sel.key}`}>{visibleRows[sel.key]}</ErrorText> : null}
                          </li>
                        );
                      })}
                    </ol>
                  ) : <p className="muted small">Empty sections show as unknown.</p>}
                  <button type="button" className="linkbtn" onClick={() => setGroup(group.key, { monitors: [...group.monitors, newSelection()] })} disabled={saving || total >= 64}><Icon name="plus" />Add monitor</button>
                  <details className="sp-adv"><summary>Public section ID</summary>
                    <input type="text" className="mono" aria-label={`Section ${gIndex + 1} public ID`} value={group.id} maxLength={128} onChange={event => setGroup(group.key, { id: event.target.value })} />
                    <small className="muted">Appears in the public JSON. Unrelated to private groups.</small>
                  </details>
                </fieldset>
              )) : <EmptyState title="No sections">A page without monitors shows its state as unknown.</EmptyState>}
            </Card>
          </div>

          <div className="vstack">
            <Card title="Announcements" hint="Public text you write; shown in this order, pinned ones first"
              actions={<button type="button" className="btn sm" disabled={saving || draft.notes.length >= 50} onClick={() => { const now = Date.now(); update({ notes: [{ key: ++draftKey, id: randomId('note'), title: '', message: '', state: 'investigating', createdAt: now, updatedAt: now, pinned: true, touched: true, other: {} }, ...draft.notes] }); }}><Icon name="plus" />Add</button>}>
              {visible.notes ? <ErrorText id={`${formId}-notes-e`}>{visible.notes}</ErrorText> : null}
              {draft.notes.length ? (
                <ol className="sp-notes">
                  {draft.notes.map((note, index) => (
                    <li key={note.key} className={visibleRows[note.key] ? 'bad' : undefined}>
                      <div className="sp-note-h">
                        <input type="text" aria-label={`Announcement ${index + 1} title`} placeholder="Title" value={note.title} maxLength={128} onChange={event => setNote(note.key, { title: event.target.value })} disabled={saving} />
                        <MoveButtons index={index} length={draft.notes.length} onMove={delta => update({ notes: move(draft.notes, index, delta) })} label={`announcement ${note.title || index + 1}`} disabled={saving} />
                        <button type="button" className="icon-btn sm" onClick={() => update({ notes: draft.notes.filter(other => other.key !== note.key) })} aria-label={`Remove announcement ${note.title || index + 1}`} disabled={saving}><Icon name="trash" /></button>
                      </div>
                      <textarea aria-label={`Announcement ${index + 1} message`} rows={3} value={note.message} placeholder="What visitors should know" onChange={event => setNote(note.key, { message: event.target.value })} disabled={saving} />
                      <div className="row-wrap">
                        <label className="inline-sel">State <select value={note.state} onChange={event => setNote(note.key, { state: event.target.value as StatusPageAnnouncementState })} disabled={saving}>{ANNOUNCEMENT_STATES.map(state => <option key={state.value} value={state.value}>{state.label}</option>)}</select></label>
                        <label className="check"><input type="checkbox" checked={note.pinned} onChange={event => setNote(note.key, { pinned: event.target.checked })} disabled={saving} /><span>Pinned</span></label>
                        <span className="muted small">{note.touched && !current?.announcements.some(saved => saved.id === note.id) ? 'new' : <>posted <Time at={note.createdAt} now={overview.now} /></>}{note.updatedAt - note.createdAt >= 60_000 || note.touched ? <> · {note.touched ? (current?.announcements.some(saved => saved.id === note.id) ? 'updated when saved' : 'posted when saved') : <>updated <Time at={note.updatedAt} now={overview.now} /></>}</> : null}</span>
                      </div>
                      {visibleRows[note.key] ? <ErrorText id={`${formId}-a${note.key}`}>{visibleRows[note.key]}</ErrorText> : null}
                    </li>
                  ))}
                </ol>
              ) : <p className="muted small">None. Resolved announcements stay public until you remove them.</p>}
              <p className="foot-note">Times are your browser’s clock when you save; they don’t schedule anything. Announcements never change the measured state.</p>
            </Card>
            <FeedCard draft={draft} current={current} setLook={setLook} errors={visible} disabled={saving} />
            <Card title="Share">
              {current ? <PublicLinkBuilder slug={current.slug} enabled={current.enabled} /> : <p className="muted small">Save the page to get its public link and badges.</p>}
              {current && (draft.slug !== current.slug || draft.enabled !== current.enabled) ? <p className="small fwarn"><Glyph tone="warn" />Unsaved address or publication changes aren’t in this link yet.</p> : null}
              {current ? <BadgeShare page={current} pendingChange={badgesChange(current, draft)} /> : null}
            </Card>
          </div>
        </div>
        <div className="form-acts sticky-acts">
          <button type="submit" className="btn primary" disabled={saving}><Icon name={current ? 'check' : 'plus'} />{saving ? `Saving to ${endpoint}…` : current ? 'Save page' : 'Create page'}</button>
          <a className="btn" href={href('status-pages')}>Cancel</a>
          <Tag icon={draft.enabled ? 'globe' : 'lock'}>{draft.enabled ? 'Will be published' : 'Will stay a draft'}</Tag>
          {saving ? <span className="muted small" role="status">Waiting for {endpoint} to acknowledge…</span> : null}
        </div>
      </form>
    </div>
  );
}
