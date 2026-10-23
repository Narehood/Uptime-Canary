import { useEffect, useMemo, useRef, useState } from 'react';
import type { NotificationChannelView, TagView } from '@uptime-canary/shared';
import { ApiFailure, describeFailure } from './api';
import { CanaryContext, type Canary, type Catalog, type SwitchRequest } from './context';
import { ago, andList, elapsed, plural, shortClock } from './format';
import { useNow, useResource } from './hooks';
import { assessAll, canEdit, nodeColumns, type Assessment } from './model';
import { dismiss, useNotices } from './notices';
import { href, navigate, sectionOf, useRoute, type Route } from './router';
import { forget, POLL_MS, session, signOut, useSession, type Connection } from './session';
import { ThemeButton, useTheme, type ThemeControl } from './theme';
import { Banner, Card, EmptyState, PageHead } from './ui/bits';
import { CanaryMark, EndpointMark, Glyph, Icon, type IconName } from './ui/icons';
import { AccountView } from './views/Account';
import { AccountsView } from './views/Accounts';
import { ChannelFormView, ChannelsView, DeliveriesView } from './views/Channels';
import { ConfigView } from './views/Config';
import { ConnectScreen, SwitchDialog } from './views/Connect';
import { GroupFormView, GroupsView } from './views/Groups';
import { IncidentsView, NotificationsView } from './views/Incidents';
import { MaintenanceFormView, MaintenanceListView } from './views/Maintenance';
import { MonitorDetailView } from './views/MonitorDetail';
import { MonitorFormView } from './views/MonitorForm';
import { MonitorsView } from './views/Monitors';
import { NodesView } from './views/Nodes';
import { OverviewView } from './views/Overview';
import { PublicStatusView } from './views/PublicStatus';
import { StatusPageFormView, StatusPagesView } from './views/StatusPages';
import { TagsView } from './views/Tags';

export function App() {
  const { connection } = useSession();
  const theme = useTheme();
  const route = useRoute();
  return (
    <div className="uc">
      <a className="skip" href="#main" onClick={event => { event.preventDefault(); document.getElementById('main')?.focus(); }}>Skip to content</a>
      {route.name === 'public-status'
        // Public pages never use the signed-in session: they read the node's public JSON without a credential.
        ? <PublicStatusView key={route.slug} slug={route.slug} nodes={route.nodes} theme={theme} signedIn={Boolean(connection)} />
        : connection ? <Shell key={connection.identity} connection={connection} theme={theme} route={route} /> : <ConnectScreen theme={theme} />}
      <TooltipLayer />
      <Toasts />
    </div>
  );
}

function Toasts() {
  const notices = useNotices();
  return (
    <div className="toasts" role="status" aria-live="polite">
      {notices.map(notice => (
        <div className="toast" key={notice.id}>
          <Glyph tone="up" />
          <span className="toast-t">{notice.text}</span>
          <button type="button" className="icon-btn sm ghost" onClick={() => dismiss(notice.id)} aria-label="Dismiss"><Icon name="close" /></button>
        </div>
      ))}
    </div>
  );
}

/** One tooltip for every `data-tip` element. Text only (never HTML); it repeats data that is also on screen or in a table. */
function TooltipLayer() {
  const [tip, setTip] = useState<{ text: string; x: number; y: number; below: boolean } | null>(null);
  useEffect(() => {
    let current: Element | null = null;
    const selector = '[data-tip], [data-tip-rail]';
    // Rail labels become tooltips only when the rail collapses to icons.
    const textOf = (element: Element): string | null => {
      const rail = element.getAttribute('data-tip-rail');
      if (rail === null) return element.getAttribute('data-tip');
      const label = element.querySelector('.lbl');
      return label && label.getBoundingClientRect().width < 2 ? rail : null;
    };
    const show = (element: Element): void => {
      const text = textOf(element);
      if (!text) { current = null; setTip(null); return; }
      current = element;
      const rect = element.getBoundingClientRect();
      const below = rect.top < 110;
      setTip({ text, x: Math.min(Math.max(rect.left + rect.width / 2, 150), window.innerWidth - 150), y: below ? rect.bottom + 8 : rect.top - 8, below });
    };
    const hide = (): void => { current = null; setTip(null); };
    const over = (event: Event): void => {
      const element = (event.target as Element | null)?.closest?.(selector);
      if (element && element !== current) show(element);
      else if (!element && current) hide();
    };
    const focus = (event: Event): void => {
      const element = (event.target as Element | null)?.closest?.(selector);
      if (element) show(element); else hide();
    };
    const key = (event: KeyboardEvent): void => { if (event.key === 'Escape') hide(); };
    document.addEventListener('pointerover', over);
    document.addEventListener('focusin', focus);
    document.addEventListener('focusout', hide);
    document.addEventListener('keydown', key);
    window.addEventListener('scroll', hide, true);
    return () => {
      document.removeEventListener('pointerover', over);
      document.removeEventListener('focusin', focus);
      document.removeEventListener('focusout', hide);
      document.removeEventListener('keydown', key);
      window.removeEventListener('scroll', hide, true);
    };
  }, []);
  if (!tip) return null;
  return <div className={`tip floating${tip.below ? ' below' : ''}`} role="tooltip" style={{ left: tip.x, top: tip.y }}>{tip.text}</div>;
}

const NAV: { key: string; label: string; icon: IconName; admin?: boolean }[] = [
  { key: 'overview', label: 'Overview', icon: 'overview' },
  { key: 'monitors', label: 'Monitors', icon: 'pulse' },
  { key: 'groups', label: 'Groups', icon: 'groups' },
  { key: 'incidents', label: 'Incidents', icon: 'incident' },
  { key: 'notifications', label: 'Notifications', icon: 'bell' },
  { key: 'maintenance', label: 'Maintenance', icon: 'calendar' },
  { key: 'status-pages', label: 'Status pages', icon: 'globe' },
  { key: 'nodes', label: 'Nodes', icon: 'nodes' },
  { key: 'config', label: 'Configuration', icon: 'config' },
  { key: 'accounts', label: 'Accounts', icon: 'users', admin: true },
];

/** A list that loads beside the overview and refreshes on its own; a 404 means the node lacks the feature. */
function useCatalog<T>(key: string, load: (signal: AbortSignal) => Promise<T[]>, idOf: (item: T) => string): Catalog<T> {
  const resource = useResource<T[]>(key, load, 20_000);
  const { data, error, loading, reload } = resource;
  const idRef = useRef(idOf);
  return useMemo<Catalog<T>>(() => ({
    list: data ?? null,
    map: new Map((data ?? []).map(item => [idRef.current(item), item])),
    loading,
    error,
    unsupported: !data && error instanceof ApiFailure && error.status === 404,
    reload,
  }), [data, error, loading, reload]);
}

function Freshness({ connection }: { connection: Connection }) {
  const now = useNow(1000);
  const age = ago(connection.fetchedAt, now);
  if (connection.link === 'rejected') return <span className="snap bad"><Glyph tone="down" />{connection.api.credential === 'session' ? 'Signed out' : 'Token rejected'} · data from {age}</span>;
  if (connection.link === 'stale') return <span className="snap bad"><Glyph tone="warn" />Not answering · data from {age}</span>;
  const late = now - connection.fetchedAt > POLL_MS * 3;
  return <span className={`snap${late ? ' bad' : ''}`}>{late ? <Glyph tone="warn" /> : <Icon name="clock" />}Updated {age}</span>;
}

/** In memory only: the password nudge stays dismissed for this connection, even if the shell remounts. */
let nudgeDismissedFor: number | null = null;

function Shell({ connection, theme, route }: { connection: Connection; theme: ThemeControl; route: Route }) {
  const [search, setSearch] = useState('');
  const [switching, setSwitching] = useState<SwitchRequest | null>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const { overview, api, account } = connection;
  const endpoint = overview.node.id;
  const role = overview.user.role;
  const [signingOut, setSigningOut] = useState(false);
  const [nudgeDismissed, setNudgeDismissed] = useState(() => nudgeDismissedFor === connection.identity);
  const channels = useCatalog<NotificationChannelView>(`${connection.identity}|${api.baseUrl}|channels`, signal => api.channels(signal), view => view.channel.id);
  const tags = useCatalog<TagView>(`${connection.identity}|${api.baseUrl}|tags`, signal => api.tags(signal), view => view.tag.id);

  const ctx = useMemo<Canary>(() => {
    const nodes = nodeColumns(overview);
    const nodeMap = new Map(nodes.map(node => [node.id, node]));
    const assessed = assessAll(overview.monitors, nodeMap);
    const assessments = new Map<string, Assessment>(assessed.map(({ view, a }) => [view.monitor.id, a]));
    const groups = overview.groups ? overview.groups.filter(view => !view.group.deleted) : null;
    const maintenance = overview.maintenance ? overview.maintenance.filter(view => !view.maintenance.deleted) : null;
    return {
      connection, api, overview, endpoint, role, account, editable: canEdit(role), admin: role === 'admin',
      nodes, nodeMap, assessed,
      monitorMap: new Map(overview.monitors.map(view => [view.monitor.id, view])),
      assessmentOf: id => assessments.get(id),
      groups, groupMap: new Map((groups ?? []).map(view => [view.group.id, view])),
      maintenance, maintenanceMap: new Map((maintenance ?? []).map(view => [view.maintenance.id, view])),
      channels, tags,
      search, setSearch,
      refresh: () => session.refresh(),
      openSwitch: request => setSwitching(request ?? {}),
      stillCurrent: () => session.isCurrent({ identity: connection.identity, api }),
    };
  }, [connection, api, overview, endpoint, role, account, search, channels, tags]);

  // A public status page may have renamed the tab; the dashboard keeps its own title.
  useEffect(() => { document.title = `Uptime Canary · ${endpoint}`; }, [endpoint]);

  // Move focus to the page heading on navigation so screen readers announce the new view.
  const routeKey = JSON.stringify(route);
  useEffect(() => {
    const heading = document.querySelector<HTMLElement>('#main h1');
    heading?.focus({ preventScroll: true });
    window.scrollTo({ top: 0 });
  }, [routeKey]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent): void => {
      const target = event.target as HTMLElement;
      const typing = target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName);
      if ((event.key === '/' && !typing) || (event.key.toLowerCase() === 'k' && (event.metaKey || event.ctrlKey))) {
        if (document.querySelector('dialog[open]')) return;
        event.preventDefault();
        searchRef.current?.focus();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, []);

  const section = sectionOf(route);
  const peers = ctx.nodes.filter(node => node.id !== endpoint && node.state !== 'unlisted');
  const out = ctx.nodes.filter(node => node.state === 'unreachable');
  const openIncidents = overview.incidents.filter(incident => incident.resolvedAt === null).length;
  const conflicts = ctx.assessed.filter(({ a }) => a.kind === 'conflict').length + (ctx.groups ?? []).filter(view => view.configConflict).length
    + (ctx.maintenance ?? []).filter(view => view.configConflict).length + (channels.list ?? []).filter(view => view.configConflict).length
    + (tags.list ?? []).filter(view => view.configConflict).length;
  const activeMaintenance = (ctx.maintenance ?? []).filter(view => view.active && !view.configConflict && view.maintenance.enabled).length;
  const badges: Record<string, { n: number; soft?: boolean; label: string }> = {
    incidents: { n: openIncidents, label: 'open' },
    maintenance: { n: activeMaintenance, soft: true, label: 'active' },
    nodes: { n: out.length, soft: true, label: 'out of view' },
    config: { n: conflicts, soft: true, label: conflicts === 1 ? 'conflict' : 'conflicts' },
  };
  const searchable = ['overview', 'monitors', 'groups', 'incidents', 'notifications', 'maintenance', 'status-pages'].includes(section);

  return (
    <CanaryContext.Provider value={ctx}>
      <div className="shell">
        <nav className="rail" aria-label="Primary">
          <a className="brand" href={href('overview')} aria-label="Uptime Canary, overview">
            <CanaryMark />
            <span className="wordmark"><b>Uptime <span>Canary</span></b><small>dashboard</small></span>
          </a>
          <ul className="nav">
            {NAV.filter(item => !item.admin || ctx.admin).map(item => {
              const badge = badges[item.key];
              return (
                <li key={item.key}>
                  <a href={href(item.key)} aria-current={section === item.key ? 'page' : undefined} data-tip-rail={item.label}>
                    <Icon name={item.icon} />
                    <span className="lbl">{item.label}</span>
                    {badge && badge.n ? <span className={`badge${badge.soft ? ' soft' : ''}`}><span className="vh">, </span>{badge.n}<span className="vh"> {badge.label}</span></span> : null}
                  </a>
                </li>
              );
            })}
          </ul>
          <div className="rail-foot">
            <span className="row"><Glyph tone={out.length ? 'unknown' : 'up'} /><span className="txt">{peers.length ? `${peers.length - out.length} of ${plural(peers.length, 'peer')} in view` : 'Standalone node'}</span></span>
            <span className="txt">Monitoring never depends on this dashboard.</span>
          </div>
        </nav>

        <div className="work">
          <header className="topbar">
            <button type="button" className="tb-btn" onClick={() => setSwitching({})} aria-haspopup="dialog">
              <EndpointMark />
              <span className="tb-txt">
                <span className="t1">Viewing from <span className="mono">{endpoint}</span></span>
                <span className="t2">{peers.length ? `${peers.length - out.length} of ${plural(peers.length, 'peer')} in view` : 'No peers'} · switch</span>
              </span>
              <Icon name="chevDown" />
            </button>
            <label className="search">
              <Icon name="search" />
              <span className="vh">Search monitors, targets, nodes and IDs</span>
              <input
                ref={searchRef} type="search" value={search} placeholder="Search monitors, targets, nodes" autoComplete="off" spellCheck={false}
                onChange={event => { setSearch(event.target.value); if (!searchable) navigate(href('monitors')); }}
                onKeyDown={event => { if (event.key === 'Escape') setSearch(''); }}
              />
              <kbd aria-hidden="true">/</kbd>
            </label>
            <Freshness connection={connection} />
            <ThemeButton theme={theme} />
            <a className="me" href={href('account')} aria-current={section === 'account' ? 'page' : undefined} data-tip={`${overview.user.name} · ${role} on ${endpoint}${api.expiresAt ? ` · session ends ${shortClock(api.expiresAt, overview.now)}` : ' · account token'}`}>
              <span className="av" aria-hidden="true">{overview.user.name.slice(0, 1).toUpperCase()}</span>
              <span className="who"><b>{overview.user.name}</b><span>{role} on {endpoint}</span></span>
              <span className="vh">Your account: {overview.user.name}, {role} on {endpoint}</span>
            </a>
            <button
              type="button" className="icon-btn" disabled={signingOut}
              onClick={() => { setSigningOut(true); void signOut().finally(() => setSigningOut(false)); }}
              aria-label={api.credential === 'session' ? 'Sign out' : 'Disconnect'}
              data-tip={api.credential === 'session' ? `Sign out: ends this session on ${endpoint}` : 'Disconnect: forget the account token in this tab'}
            ><Icon name="logout" /></button>
          </header>

          <LinkBanner connection={connection} onReconnect={() => setSwitching({
            url: api.baseUrl,
            reason: api.credential === 'session'
              ? `Your sign-in on ${endpoint} ended. Sign in again to continue.`
              : `${endpoint} no longer accepts the token this tab was using. Sign in again to continue.`,
          })} />
          <ExpiryBanner connection={connection} onRenew={() => setSwitching({ url: api.baseUrl, reason: `Sign in to ${endpoint} again to start a new 12-hour session.` })} />
          {account && account.passwordEnabled === false && api.credential === 'token' && section !== 'account' && !nudgeDismissed && connection.link === 'live' ? (
            <Banner tone="info" title={<>Set a password for {account.name}</>} actions={<>
              <a className="btn sm primary" href={href('account')}><Icon name="key" />Set a password</a>
              <button type="button" className="btn sm" onClick={() => { nudgeDismissedFor = connection.identity; setNudgeDismissed(true); }}>Not now</button>
            </>}>
              You’re signed in with an account token. With a password (and optionally an authenticator app) you can sign in on {endpoint} without handling the token.
            </Banner>
          ) : null}
          {out.length && connection.link === 'live' ? (
            <Banner
              tone="warn"
              title={<>{endpoint} can’t reach {andList(out.map(node => node.id))}</>}
              actions={<>
                <a className="btn sm" href={href('nodes')}><Icon name="nodes" />Open nodes</a>
                {out[0]?.url ? <button type="button" className="btn sm" onClick={() => setSwitching({ url: out[0]!.url })}><Icon name="plug" />View from {out[0]!.id}</button> : null}
              </>}
            >
              Every node keeps monitoring on its own. Their locations show as <b>unknown</b>, never as up or down. Until they reconnect, each side may send its own alerts, so a duplicate is possible.
            </Banner>
          ) : null}

          <main id="main" tabIndex={-1} className={connection.link !== 'live' ? 'is-stale' : undefined}>
            <View route={route} />
          </main>
        </div>
      </div>
      {switching ? <SwitchDialog initialUrl={switching.url} reason={switching.reason} onClose={() => setSwitching(null)} /> : null}
    </CanaryContext.Provider>
  );
}

/** A password session ends at a fixed time. Warn shortly before, measured on the node's clock. */
function ExpiryBanner({ connection, onRenew }: { connection: Connection; onRenew: () => void }) {
  const now = useNow(15_000);
  const expiresAt = connection.api.expiresAt;
  if (!expiresAt || connection.link === 'rejected') return null;
  const nodeNow = now + (connection.overview.now - connection.fetchedAt);
  const left = expiresAt - nodeNow;
  if (left > 15 * 60_000) return null;
  return (
    <Banner tone="warn" title={left > 0 ? <>Your sign-in ends in {elapsed(left)}</> : <>Your sign-in has ended</>} actions={
      <button type="button" className="btn sm primary" onClick={onRenew}><Icon name="lock" />Sign in again</button>
    }>
      Password sessions last 12 hours. Signing in again starts a new one; monitoring isn’t affected either way.
    </Banner>
  );
}

function LinkBanner({ connection, onReconnect }: { connection: Connection; onReconnect: () => void }) {
  const now = useNow(5000);
  const endpoint = connection.overview.node.id;
  if (connection.link === 'live') return null;
  const since = connection.failingSince ? `for ${elapsed(now - connection.failingSince)}` : 'recently';
  if (connection.link === 'rejected') {
    const passwordSession = connection.api.credential === 'session';
    return (
      <Banner tone="down" role="alert" title={passwordSession ? <>Your sign-in on {endpoint} ended</> : <>{endpoint} rejected this tab’s token</>} actions={<>
        <button type="button" className="btn sm primary" onClick={onReconnect}><Icon name="lock" />Sign in again</button>
        <button type="button" className="btn sm" onClick={forget}><Icon name="logout" />Leave</button>
      </>}>
        {passwordSession
          ? 'The 12-hour session expired, someone signed it out, or the account’s password, role, token or two-factor setting changed.'
          : 'The account may have been removed or its token changed.'} Polling stopped. What you see is the last data {endpoint} sent, {ago(connection.fetchedAt, now)}.
      </Banner>
    );
  }
  return (
    <Banner tone="warn" title={<>{endpoint} isn’t answering ({since})</>} actions={<>
      <button type="button" className="btn sm" onClick={() => session.refresh()}><Icon name="refresh" />Try now</button>
      <button type="button" className="btn sm" onClick={onReconnect}><Icon name="plug" />Switch endpoint</button>
    </>}>
      {connection.failure ? `${describeFailure(connection.failure)} ` : ''}Showing the last data it sent, {ago(connection.fetchedAt, now)}; nothing below is current. Monitoring continues on every node regardless. Retrying automatically.
    </Banner>
  );
}

function View({ route }: { route: Route }) {
  switch (route.name) {
    case 'overview': return <OverviewView />;
    case 'monitors': return <MonitorsView />;
    case 'monitor-new': return <MonitorFormView />;
    case 'monitor': return <MonitorDetailView key={route.id} id={route.id} />;
    case 'monitor-edit': return <MonitorFormView key={route.id} id={route.id} />;
    case 'tags': return <TagsView />;
    case 'groups': return <GroupsView />;
    case 'group-new': return <GroupFormView />;
    case 'group-edit': return <GroupFormView key={route.id} id={route.id} />;
    case 'incidents': return <IncidentsView />;
    case 'notifications': return <ChannelsView />;
    case 'deliveries': return <DeliveriesView />;
    case 'receipts': return <NotificationsView />;
    case 'channel-new': return <ChannelFormView />;
    case 'channel-edit': return <ChannelFormView key={route.id} id={route.id} />;
    case 'maintenance': return <MaintenanceListView />;
    case 'maintenance-new': return <MaintenanceFormView />;
    case 'maintenance-edit': return <MaintenanceFormView key={route.id} id={route.id} />;
    case 'status-pages': return <StatusPagesView />;
    case 'status-page-new': return <StatusPageFormView />;
    case 'status-page-edit': return <StatusPageFormView key={route.id} id={route.id} />;
    case 'public-status': return null;
    case 'nodes': return <NodesView />;
    case 'config': return <ConfigView />;
    case 'accounts': return <AccountsView />;
    case 'account': return <AccountView />;
    case 'not-found':
      return <div className="view"><PageHead title="Not found" /><Card><EmptyState title="Nothing here" action={<a className="btn" href={href('overview')}>Go to overview</a>}>This dashboard has no page at that address.</EmptyState></Card></div>;
  }
}
