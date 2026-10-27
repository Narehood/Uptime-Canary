import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import type { AccountView, AuthenticationStatus, Overview } from '@uptime-canary/shared';
import {
  ApiFailure, CanaryApi, checkToken, codeProblem, createDashboardOwner, describeFailure, fetchAuthStatus, fetchDashboardStatus, isAbort,
  normalizeApiUrl, passwordProblem, passwordSignIn,
} from '../api';
import { useCanary } from '../context';
import { ago } from '../format';
import { controlPlaneMode, gatewayMode, pageOrigin } from '../gateway';
import { useNow, useSummaryFocus } from '../hooks';
import { notify } from '../notices';
import { href, navigate } from '../router';
import { session, signOut, useSession, type Farewell } from '../session';
import { Banner, ErrorText, Modal, Spinner, Tag, nodeTone } from '../ui/bits';
import { ErrorSummary, PasswordField, SecretField, TextField } from '../ui/forms';
import { CanaryMark, Glyph, Icon } from '../ui/icons';
import type { ThemeControl } from '../theme';
import { ThemeButton } from '../theme';

export const DEFAULT_ENDPOINT = 'http://127.0.0.1:3100';
const LAST_ENDPOINT_KEY = 'uptime-canary.endpoint';

/**
 * The address is remembered between visits; verified sign-ins survive reloads in tab session storage. Passwords and codes are never saved.
 * In gateway mode the address is always this page's origin: a remembered one (perhaps a private node's) is ignored.
 */
export function lastEndpoint(): string {
  if (gatewayMode()) return pageOrigin() ?? '';
  try { return localStorage.getItem(LAST_ENDPOINT_KEY) || DEFAULT_ENDPOINT; } catch { return DEFAULT_ENDPOINT; }
}
function rememberEndpoint(url: string): void {
  if (gatewayMode()) return;
  try { localStorage.setItem(LAST_ENDPOINT_KEY, url); } catch { /* storage unavailable: nothing to remember */ }
}

type Method = 'password' | 'token';
type Probe = { url: string; status: AuthenticationStatus } | { url: string; status: null };

function failureHints(error: unknown, url: string, method: Method): string[] {
  if (!(error instanceof ApiFailure)) return [];
  const origin = window.location.origin;
  if (controlPlaneMode()) {
    // The dashboard owns these accounts; node accounts and a sign-in proxy's session don't sign in here.
    if (error.status === 401) {
      return method === 'password'
        ? ['Use an account on this dashboard. Accounts on monitoring nodes don’t sign in here.']
        : ['Use an account token this dashboard issued. Tokens for accounts on monitoring nodes don’t work here.'];
    }
    if (error.status === 403) return [`This dashboard’s server may not list ${origin} as its address. Check its CANARY_DASHBOARD_ORIGINS setting.`];
    if (error.status === 409) return ['This dashboard has no owner yet. Reload the page to create it.'];
    if (error.status === 429) return ['This dashboard is limiting sign-in attempts. Wait a minute before trying again.'];
    return [];
  }
  if (error.status === 0) {
    // The page's own origin needs no cross-origin permission; the failure itself already names a sign-in proxy.
    if (url === origin) return [];
    const hints = [`If the address is right, make sure the node’s CANARY_CORS_ORIGINS includes ${origin}.`];
    if (window.location.protocol === 'https:' && url.startsWith('http:')) hints.unshift('Browsers block plain HTTP requests from an HTTPS page. Use an HTTPS address for the node.');
    return hints;
  }
  if (error.status === 401) {
    const where = gatewayMode() ? 'on the node behind this dashboard' : 'on this node';
    return method === 'password'
      ? [`Accounts and passwords are local to each node. Use an account that exists ${where}.`]
      : [`Accounts are local to each node. Use a token for an account that exists ${where}.`];
  }
  if (error.status === 429) return ['Each address gets 10 sign-in attempts per minute. Wait a minute before trying again.'];
  if (error.status === 403) return [`The node may not allow ${origin} as a dashboard origin.`];
  return [];
}

interface SignInFormProps {
  initialUrl: string;
  initialName?: string;
  /** Present when switching from a token connection: allows reusing that token for another address. */
  current?: { api: CanaryApi; endpoint: string };
  submitLabel: string;
  /** `setup`: the account signed in with a token and has no password yet, on a node that offers passwords. */
  onConnected: (overview: Overview, setup: boolean) => void;
  onBusy?: (busy: boolean) => void;
  autoFocus?: boolean;
}

export function SignInForm({ initialUrl, initialName = '', current, submitLabel, onConnected, onBusy, autoFocus }: SignInFormProps) {
  const id = useId();
  // Gateway mode signs in through this page's own server: its origin is the only address, never typed or passed in.
  const gateway = gatewayMode();
  const [typedUrl, setUrl] = useState(initialUrl);
  const url = gateway ? pageOrigin() ?? '' : typedUrl;
  const [probe, setProbe] = useState<Probe | null>(null);
  const [chosen, setChosen] = useState<Method | null>(null);
  const [name, setName] = useState(initialName);
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [step, setStep] = useState<'credentials' | 'code'>('credentials');
  const [token, setToken] = useState('');
  const [reuse, setReuse] = useState(false);
  const [showSecret, setShowSecret] = useState(false);
  const [pending, setPending] = useState(false);
  const [errors, setErrors] = useState<{ url?: string; name?: string; password?: string; code?: string; token?: string }>({});
  const [failure, setFailure] = useState<{ message: string; hints: string[] } | null>(null);
  const attempt = useRef<AbortController | null>(null);
  const focusRef = useRef<HTMLInputElement>(null);
  const urlRef = useRef<HTMLInputElement>(null);

  useEffect(() => () => attempt.current?.abort(), []);
  useEffect(() => { setUrl(initialUrl); }, [initialUrl]);

  // Ask the node which sign-in methods it offers (a public, credential-free request), shortly after typing stops.
  useEffect(() => {
    let base: string;
    try { base = normalizeApiUrl(url); } catch { setProbe(null); return; }
    const controller = new AbortController();
    const timer = setTimeout(() => {
      fetchAuthStatus(base, controller.signal).then(
        status => setProbe({ url: base, status }),
        error => { if (!isAbort(error)) setProbe({ url: base, status: null }); },
      );
    }, 350);
    return () => { clearTimeout(timer); controller.abort(); };
  }, [url]);

  let base: string | null = null;
  try { base = normalizeApiUrl(url); } catch { base = null; }
  const status = probe && probe.url === base ? probe.status : null;
  const passwordOffered = status ? status.passwordLogin : true;
  // A dashboard's first owner is created on its own setup screen, never with a node's admin token.
  const controlPlane = controlPlaneMode();
  const setupRequired = !controlPlane && Boolean(status?.passwordLogin && status.setupRequired);
  const suggested: Method = !passwordOffered || setupRequired ? 'token' : 'password';
  const method: Method = chosen && (chosen === 'token' || passwordOffered) ? chosen : suggested;
  const reusing = Boolean(current && reuse && method === 'token');

  const pick = (next: Method): void => {
    setChosen(next); setFailure(null); setErrors({}); setStep('credentials'); setCode('');
    setTimeout(() => focusRef.current?.focus(), 0);
  };

  const finish = async (api: CanaryApi, controller: AbortController, base: string): Promise<void> => {
    const overview = await session.connect(api, controller.signal);
    rememberEndpoint(base);
    setPassword(''); setToken(''); setCode(''); setStep('credentials');
    const account = session.getSnapshot().connection?.account;
    onConnected(overview, api.credential === 'token' && account?.passwordEnabled === false && Boolean(status?.passwordLogin));
  };

  const submit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (pending) return;
    setFailure(null);
    let target: string;
    try { target = normalizeApiUrl(url); }
    catch (error) {
      if (gateway) { setFailure({ message: 'This page has no web address to reach the dashboard’s server at.', hints: [] }); return; }
      setErrors({ url: error instanceof Error ? error.message : 'Invalid address' }); urlRef.current?.focus(); return;
    }
    const problems: typeof errors = {};
    if (method === 'password') {
      if (step === 'credentials') {
        if (!name.trim()) problems.name = 'Enter your account name.';
        if (!password) problems.password = 'Enter your password.';
      } else problems.code = codeProblem(code);
    } else if (!reusing) problems.token = checkToken(token) ?? undefined;
    setErrors(problems);
    if (Object.values(problems).some(Boolean)) { focusRef.current?.focus(); return; }

    const controller = new AbortController();
    attempt.current = controller;
    setPending(true);
    onBusy?.(true);
    try {
      if (method === 'token') {
        const api = reusing && current ? current.api.withBaseUrl(target) : new CanaryApi(target, token);
        await finish(api, controller, target);
        return;
      }
      const result = await passwordSignIn(target, { name, password, ...(step === 'code' ? { code } : {}) }, controller.signal);
      if (result.kind === 'mfa') {
        setStep('code');
        setTimeout(() => focusRef.current?.focus(), 0);
        return;
      }
      await finish(result.api, controller, target);
    } catch (error) {
      if (isAbort(error)) return;
      const rejected = error instanceof ApiFailure && error.status === 401;
      if (rejected && method === 'token') setToken('');
      if (rejected && method === 'password') {
        if (step === 'code') {
          setCode('');
          setFailure({ message: 'That code wasn’t accepted.', hints: ['Codes change every 30 seconds and each one works only once. Wait for the next code and try again.', 'Check that your device’s clock is set automatically.'] });
          return;
        }
        setPassword('');
      }
      setFailure({ message: method === 'password' && error instanceof ApiFailure && error.detail ? describeFailure(error, { preferDetail: true }) : describeFailure(error), hints: failureHints(error, target, method) });
    } finally {
      if (attempt.current === controller) attempt.current = null;
      setPending(false);
      onBusy?.(false);
    }
  };

  const describe = (field: keyof typeof errors, hint?: string): string | undefined => [hint, errors[field] ? `${id}-${field}-e` : ''].filter(Boolean).join(' ') || undefined;
  const secretToggle = (label: string) => (
    <button type="button" className="icon-btn sm" onClick={() => setShowSecret(value => !value)} aria-pressed={showSecret} aria-label={showSecret ? `Hide ${label}` : `Show ${label}`}>
      <Icon name={showSecret ? 'eyeOff' : 'eye'} />
    </button>
  );

  return (
    <form className="connect-form" onSubmit={event => { void submit(event); }} noValidate aria-busy={pending}>
      {gateway ? null : (
        <div className="fld">
          <label htmlFor={`${id}-url`}>Node API address</label>
          <input
            ref={urlRef} id={`${id}-url`} name="node" type="url" className="mono" value={url} inputMode="url" autoComplete="url" spellCheck={false}
            aria-invalid={errors.url ? true : undefined} aria-describedby={`${id}-url-h${errors.url ? ` ${id}-url-e` : ''}`}
            onChange={event => { setUrl(event.target.value); setStep('credentials'); }} disabled={pending || step === 'code'}
          />
          <small id={`${id}-url-h`}>Base address only, for example <span className="mono">{DEFAULT_ENDPOINT}</span></small>
          <ErrorText id={`${id}-url-e`}>{errors.url}</ErrorText>
        </div>
      )}

      {step === 'credentials' ? (
        <fieldset className="fld method-f" disabled={pending}>
          <legend className="vh">Sign in with</legend>
          <div className="seg method" role="presentation">
            {passwordOffered ? <label><input type="radio" name={`${id}-method`} checked={method === 'password'} onChange={() => pick('password')} /><span><Icon name="users" />Password</span></label> : null}
            <label><input type="radio" name={`${id}-method`} checked={method === 'token'} onChange={() => pick('token')} /><span><Icon name="key" />Account token</span></label>
          </div>
          {status && !status.passwordLogin ? <small>{controlPlane ? 'This dashboard offers token sign-in only.' : 'This node offers token sign-in only.'}</small> : null}
        </fieldset>
      ) : null}

      {method === 'password' && setupRequired && step === 'credentials' ? (
        <div className="callout" role="note">
          <Icon name="info" />
          <div>
            <b>No passwords are set on this node yet</b>
            <p>Sign in once with your account token (the first admin’s is <span className="mono">CANARY_ADMIN_TOKEN</span> from the node’s environment). You’ll go straight to your account page to choose a password, then sign in with it from then on.</p>
            <button type="button" className="btn sm" onClick={() => pick('token')}><Icon name="key" />Use an account token</button>
          </div>
        </div>
      ) : null}

      {method === 'password' && step === 'credentials' ? (
        <>
          <div className="fld">
            <label htmlFor={`${id}-name`}>Account name</label>
            <input
              ref={focusRef} id={`${id}-name`} name="username" type="text" value={name} autoComplete="username" spellCheck={false} autoCapitalize="off"
              autoFocus={autoFocus && Boolean(!initialName)} disabled={pending} maxLength={128}
              aria-invalid={errors.name ? true : undefined} aria-describedby={describe('name')} onChange={event => setName(event.target.value)}
            />
            <ErrorText id={`${id}-name-e`}>{errors.name}</ErrorText>
          </div>
          <div className="fld">
            <label htmlFor={`${id}-password`}>Password</label>
            <div className="secret">
              <input
                id={`${id}-password`} name="password" type={showSecret ? 'text' : 'password'} value={password} autoComplete="current-password" spellCheck={false}
                autoFocus={autoFocus && Boolean(initialName)} disabled={pending} maxLength={512}
                aria-invalid={errors.password ? true : undefined} aria-describedby={describe('password', `${id}-password-h`)} onChange={event => setPassword(event.target.value)}
              />
              {secretToggle('password')}
            </div>
            <small id={`${id}-password-h`}><Icon name="lock" className="inline" />Your 12-hour session survives reloads in this tab. Sign out to clear it.</small>
            <ErrorText id={`${id}-password-e`}>{errors.password}</ErrorText>
          </div>
        </>
      ) : null}

      {method === 'password' && step === 'code' ? (
        <div className="mfa-step">
          <p className="sec"><Glyph tone="up" /> Password accepted for <b>{name.trim()}</b>. Two-factor authentication is on for this account.</p>
          <div className="fld">
            <label htmlFor={`${id}-code`}>Authenticator code</label>
            <input
              ref={focusRef} id={`${id}-code`} name="one-time-code" type="text" className="mono code-in" value={code} inputMode="numeric" autoComplete="one-time-code"
              pattern="[0-9 ]*" maxLength={7} disabled={pending} autoFocus
              aria-invalid={errors.code ? true : undefined} aria-describedby={describe('code', `${id}-code-h`)} onChange={event => setCode(event.target.value)}
            />
            <small id={`${id}-code-h`}>The 6-digit code your authenticator app shows for Uptime Canary · {name.trim()}.</small>
            <ErrorText id={`${id}-code-e`}>{errors.code}</ErrorText>
          </div>
        </div>
      ) : null}

      {method === 'token' && current ? (
        <label className="check">
          <input type="checkbox" checked={reuse} onChange={event => setReuse(event.target.checked)} disabled={pending} />
          <span>Use the same account token as on <span className="mono">{current.endpoint}</span><small>Works only if that account also exists on the other node with the same token. The token is sent to the address above.</small></span>
        </label>
      ) : null}
      {method === 'token' && !reusing ? (
        <div className="fld">
          <label htmlFor={`${id}-token`}>Account token</label>
          <div className="secret">
            <input
              ref={focusRef} id={`${id}-token`} name="token" type={showSecret ? 'text' : 'password'} className="mono" value={token} autoComplete="off"
              spellCheck={false} autoCapitalize="off" disabled={pending}
              aria-invalid={errors.token ? true : undefined} aria-describedby={describe('token', `${id}-token-h`)} onChange={event => setToken(event.target.value)}
            />
            {secretToggle('token')}
          </div>
          <small id={`${id}-token-h`}><Icon name="lock" className="inline" />For headless and first-time access. Saved for this tab so reloads keep you signed in. Sign out to clear it.</small>
          <ErrorText id={`${id}-token-e`}>{errors.token}</ErrorText>
        </div>
      ) : null}

      {failure ? (
        <div className="errsum" role="alert">
          <Glyph tone="down" />
          <div>
            <b>{failure.message}</b>
            {failure.hints.length ? <ul>{failure.hints.map(hint => <li key={hint}>{hint}</li>)}</ul> : null}
          </div>
        </div>
      ) : null}
      <div className="row-wrap">
        <button type="submit" className="btn primary" disabled={pending}>
          <Icon name={method === 'password' ? 'lock' : 'plug'} />
          {pending ? (method === 'password' ? 'Signing in…' : 'Connecting…') : step === 'code' ? 'Verify code' : submitLabel}
        </button>
        {pending ? <button type="button" className="btn" onClick={() => attempt.current?.abort()}>Cancel</button> : null}
        {step === 'code' && !pending ? <button type="button" className="btn" onClick={() => { setStep('credentials'); setCode(''); setPassword(''); setFailure(null); }}><Icon name="chevLeft" />Back</button> : null}
      </div>
    </form>
  );
}

const who = (overview: Overview): string => overview.user.name === overview.user.role ? overview.user.name : `${overview.user.name} (${overview.user.role})`;

function afterSignIn(overview: Overview, setup: boolean, verb: string): void {
  // On the control plane the sign-in is to the dashboard itself, which reports no node.
  const where = controlPlaneMode() ? 'this dashboard' : overview.node.id;
  if (setup) {
    navigate(href('account'));
    notify(`${verb} ${where} as ${who(overview)} with an account token. Choose a password to sign in with from now on.`);
  } else notify(`${verb} ${where} as ${who(overview)}.`);
}

export function ConnectScreen({ theme }: { theme: ThemeControl }) {
  const { farewell } = useSession();
  // Keep the first farewell for this screen even after it's cleared from the store.
  const [note] = useState<Farewell | null>(farewell);
  useEffect(() => { session.clearFarewell(); }, []);
  const gateway = gatewayMode();
  return (
    <div className="connect">
      <div className="connect-top"><ThemeButton theme={theme} /></div>
      <section className="connect-brand" aria-label="About Uptime Canary">
        <CanaryMark className="connect-mark" />
        <p className="wordmark-lg"><b>Uptime</b> Canary</p>
        <p className="tagline">Self-hosted uptime monitoring that keeps working when parts of it fail.</p>
        <ul className="connect-facts">
          {gateway
            ? <li><Icon name="nodes" /><span><b>Many nodes, one address.</b> Your browser talks only to this dashboard. Its server reaches its node privately, and that node shows its peers too, each fact as it knows it.</span></li>
            : <li><Icon name="nodes" /><span><b>Many nodes, one view.</b> The node you sign in to shows its peers too, each fact as that node knows it.</span></li>}
          <li><Icon name="lock" /><span>Your sign-in <b>survives reloads in this tab</b>. Sign out to clear it. Your password and verification codes are never saved.</span></li>
          <li><Icon name="pulse" /><span><b>Optional.</b> Nodes keep monitoring and alerting whether this page is open or not.</span></li>
        </ul>
      </section>
      <main className="card connect-card" id="main">
        <h1>One dashboard for a node and all its peers</h1>
        {gateway ? (
          <>
            <p className="sec">Sign in to this dashboard once with a password or account token to see its node and every node peered with it: their monitors, latest results and incidents. You don’t need to sign in to each peer.</p>
            <p className="muted small">Accounts are local to each node, so use an account on the node behind this dashboard. A sign-in proxy in front of this page doesn’t sign you in here.</p>
          </>
        ) : (
          <>
            <p className="sec">Sign in to a node once with a password or account token to see it and every node peered with it: their monitors, latest results and incidents. You don’t need to sign in to each peer.</p>
            <p className="muted small">Accounts are local to each node, so use an account on the node at the address below.</p>
          </>
        )}
        {note ? <Banner tone={note.tone === 'warn' ? 'warn' : 'info'} title={note.message} role="status" /> : null}
        <SignInForm
          initialUrl={note?.url ?? lastEndpoint()} initialName={note?.name} submitLabel="Sign in" autoFocus
          onConnected={(overview, setup) => afterSignIn(overview, setup, 'Signed in to')}
        />
      </main>
    </div>
  );
}

export function SwitchDialog({ initialUrl, reason, onClose }: { initialUrl?: string; reason?: string; onClose: () => void }) {
  const { api, endpoint, nodes, overview, connection } = useCanary();
  const now = useNow(5000);
  const [url, setUrl] = useState(initialUrl ?? api.baseUrl);
  const [busy, setBusy] = useState(false);
  const peers = nodes.filter(node => node.id !== endpoint && node.url);
  const reusable = api.credential === 'token' && connection.link !== 'rejected';
  if (controlPlaneMode()) return <DashboardSignInDialog reason={reason} onClose={onClose} />;
  if (gatewayMode()) return <GatewaySignInDialog reason={reason} onClose={onClose} />;
  return (
    <Modal title="Switch endpoint" onClose={onClose} busy={busy} wide>
      {reason ? <p className="modal-note">{reason}</p> : null}
      <p className="sec">
        You’re viewing from <b className="mono">{endpoint}</b> at <span className="mono">{api.baseUrl}</span> as {who(overview)}.
        Switching changes only what this dashboard reads. Monitoring and alerts continue on every node.
      </p>
      {peers.length ? (
        <div className="peer-pick">
          <h3>Peers known to {endpoint}</h3>
          <ul>
            {peers.map(node => (
              <li key={node.id}>
                <Glyph tone={nodeTone(node)} label={node.state === 'connected' ? 'connected' : 'out of view'} />
                <span className="grow">
                  <b className="mono">{node.id}</b> <span className="muted">{node.name !== node.id ? node.name : ''}</span>
                  <span className="mono sub">{node.url}</span>
                  <span className="sub">{node.state === 'connected' ? 'In view' : node.lastSeenAt ? `Out of view · seen ${ago(node.lastSeenAt, now)}` : 'Out of view'}</span>
                </span>
                <button type="button" className="btn sm" onClick={() => setUrl(node.url!)} aria-label={`Use ${node.id}’s address`}>Use address</button>
              </li>
            ))}
          </ul>
          <p className="muted small">These are the addresses nodes use to reach each other. Your browser needs a route to them, and the node must allow this dashboard’s origin.</p>
        </div>
      ) : null}
      <SignInForm
        initialUrl={url}
        initialName={connection.account?.name ?? overview.user.name}
        current={reusable ? { api, endpoint } : undefined}
        submitLabel="Sign in and switch"
        onBusy={setBusy}
        onConnected={(next, setup) => { onClose(); afterSignIn(next, setup, 'Now viewing from'); }}
      />
      <div className="modal-foot">
        <span className="muted small">
          {api.credential === 'session'
            ? `Password sign-ins are local to each node: sign in to the other node with an account that exists there. Switching signs this tab out of ${endpoint}.`
            : `Accounts are local to each node. A token for ${endpoint} works elsewhere only if the same account was created there.`}
        </span>
        <button type="button" className="btn" onClick={() => { void signOut(); }} disabled={busy}><Icon name="logout" />{api.credential === 'session' ? 'Sign out' : 'Disconnect'}</button>
      </div>
    </Modal>
  );
}

/**
 * Gateway mode has no other address to switch to: this dialog signs in again, or as another account, through this
 * dashboard's server. Peers stay visible through that node; the browser never contacts them.
 */
function GatewaySignInDialog({ reason, onClose }: { reason?: string; onClose: () => void }) {
  const { api, endpoint, overview, connection } = useCanary();
  const [busy, setBusy] = useState(false);
  return (
    <Modal title="Dashboard sign-in" onClose={onClose} busy={busy}>
      {reason ? <p className="modal-note">{reason}</p> : null}
      <p className="sec">
        You’re viewing from <b className="mono">{endpoint}</b> through this dashboard’s server as {who(overview)}.
        Signing in again changes only this tab’s sign-in. Monitoring and alerts continue on every node.
      </p>
      <SignInForm
        initialUrl={api.baseUrl}
        initialName={connection.account?.name ?? overview.user.name}
        submitLabel="Sign in"
        onBusy={setBusy}
        onConnected={(next, setup) => { onClose(); afterSignIn(next, setup, 'Signed in to'); }}
      />
      <div className="modal-foot">
        <span className="muted small">
          Accounts are local to each node: use an account on {endpoint}, the node behind this dashboard.
          {api.credential === 'session' ? ` A new sign-in replaces this tab’s session on ${endpoint}.` : ''}
        </span>
        <button type="button" className="btn" onClick={() => { void signOut(); }} disabled={busy}><Icon name="logout" />{api.credential === 'session' ? 'Sign out' : 'Disconnect'}</button>
      </div>
    </Modal>
  );
}

// ----- Control plane: the dashboard owns its accounts. No node address, no node account, no node needs to answer.

/** Same limits as the dashboard's check: 128 bytes after trimming, no control characters. */
function ownerNameProblem(name: string): string | undefined {
  const text = name.trim();
  if (!text) return 'Choose an account name.';
  if (new TextEncoder().encode(text).length > 128 || /[\x00-\x1f\x7f]/u.test(text)) return 'Use at most 128 characters, without control characters.';
  return undefined;
}

function setupCodeProblem(code: string): string | undefined {
  const text = code.trim();
  if (!text) return 'Enter the setup code from the dashboard server.';
  if (/[^\x21-\x7e]/u.test(text)) return 'Setup codes contain only printable characters, without spaces.';
  return undefined;
}

const dashboardHost = (): string | null => { const page = pageOrigin(); try { return page ? new URL(page).host : null; } catch { return null; } };

function DashboardBrand() {
  return (
    <section className="connect-brand" aria-label="About this dashboard">
      <CanaryMark className="connect-mark" />
      <p className="wordmark-lg"><b>Uptime</b> Canary</p>
      <p className="tagline">Watch your services from several places. Sign in once, here.</p>
      <ul className="connect-facts">
        <li><Icon name="users" /><span><b>Accounts live on this dashboard,</b> not on a monitoring node. There’s nothing to sign in to on each node.</span></li>
        <li><Icon name="nodes" /><span><b>Nodes come after sign-in.</b> This dashboard’s server reaches them at their private addresses; your browser never does.</span></li>
        <li><Icon name="lock" /><span>Your sign-in <b>survives reloads in this tab</b>. Sign out to clear it. Your password and verification codes are never saved.</span></li>
        <li><Icon name="pulse" /><span><b>Optional.</b> Nodes keep monitoring and alerting whether this page is open or not.</span></li>
      </ul>
    </section>
  );
}

type DashboardScreen =
  | { kind: 'checking' }
  | { kind: 'failed'; message: string }
  | { kind: 'setup' }
  | { kind: 'login'; note?: { message: string; tone: 'info' | 'warn'; name?: string } };

/**
 * The control plane's signed-out screen. The dashboard's own status says whether it still needs its first owner;
 * either way no node is contacted, so a missing or stopped node never blocks setup or sign-in.
 */
export function DashboardConnectScreen({ theme }: { theme: ThemeControl }) {
  const { farewell } = useSession();
  const [farewellNote] = useState<Farewell | null>(farewell);
  useEffect(() => { session.clearFarewell(); }, []);
  const [screen, setScreen] = useState<DashboardScreen>({ kind: 'checking' });
  const [nonce, setNonce] = useState(0);
  const headingRef = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    const controller = new AbortController();
    setScreen({ kind: 'checking' });
    fetchDashboardStatus(controller.signal).then(status => {
      if (!status.dashboard) {
        setScreen({ kind: 'failed', message: 'This address answered, but not as an Uptime Canary dashboard with its own accounts. Check that the dashboard server, not only a web server, serves this page.' });
      } else setScreen(status.setupRequired ? { kind: 'setup' } : { kind: 'login' });
    }, error => { if (!isAbort(error)) setScreen({ kind: 'failed', message: describeFailure(error) }); });
    return () => controller.abort();
  }, [nonce]);
  // Setup and sign-in focus their first field; a failure has no field, so its heading takes focus.
  useEffect(() => { if (screen.kind === 'failed') headingRef.current?.focus({ preventScroll: true }); }, [screen.kind]);

  const host = dashboardHost();
  const note = screen.kind === 'login' && screen.note ? screen.note : farewellNote ? { message: farewellNote.message, tone: farewellNote.tone, name: farewellNote.name } : null;
  return (
    <div className="connect">
      <div className="connect-top"><ThemeButton theme={theme} /></div>
      <DashboardBrand />
      <main className="card connect-card" id="main">
        {screen.kind === 'checking' ? (
          <><h1>Uptime Canary</h1><Spinner label="Checking this dashboard…" /></>
        ) : screen.kind === 'failed' ? (
          <>
            <h1 ref={headingRef} tabIndex={-1}>This dashboard isn’t ready</h1>
            <div className="errsum" role="alert"><Glyph tone="down" /><div><b>Its sign-in status couldn’t be read</b><p>{screen.message}</p></div></div>
            <p className="muted small">Nodes keep monitoring and alerting either way. A stopped node never causes this: sign-in doesn’t need any node.</p>
            <div className="row-wrap"><button type="button" className="btn primary" onClick={() => setNonce(n => n + 1)}><Icon name="refresh" />Try again</button></div>
          </>
        ) : screen.kind === 'setup' ? (
          <>
            {host ? <span><Tag tone="accent">New dashboard · <span className="mono">{host}</span></Tag></span> : null}
            <h1 ref={headingRef} tabIndex={-1}>Create the owner account</h1>
            <p className="sec">This dashboard has no accounts yet. The first one is its owner and an administrator. After that, only administrators add accounts; there’s no public sign-up.</p>
            <DashboardSetupForm
              onOwnerExists={() => setScreen({ kind: 'login', note: { tone: 'warn', message: 'This dashboard already has an owner, so your account wasn’t created. Sign in with an account an administrator gives you.' } })}
              onCreated={(name, message) => setScreen({ kind: 'login', note: { tone: 'info', message, name } })}
            />
            <p className="muted small">No node needs to be running for this. A node bundled with this dashboard is added automatically; you add others after signing in.</p>
          </>
        ) : (
          <>
            {host ? <span><Tag><span className="mono">{host}</span></Tag></span> : null}
            <h1 ref={headingRef} tabIndex={-1}>Sign in</h1>
            <p className="sec">Sign in to this dashboard to see and manage the nodes it reads. No node address is needed.</p>
            {note ? <Banner tone={note.tone === 'warn' ? 'warn' : 'info'} title={note.message} role="status" /> : null}
            <SignInForm
              initialUrl={pageOrigin() ?? ''} initialName={note?.name} submitLabel="Sign in" autoFocus
              onConnected={(overview, setup) => afterSignIn(overview, setup, 'Signed in to')}
            />
            <p className="muted small">No account? Ask an administrator of this dashboard to add one.</p>
            <p className="muted small">A sign-in proxy in front of this page, such as NetBird, doesn’t sign you in here, and accounts on monitoring nodes don’t either.</p>
          </>
        )}
      </main>
    </div>
  );
}

/**
 * First-owner setup: one atomic request with the private setup code, then an ordinary password sign-in. The password
 * and code exist only in this form's state and are cleared once used; nothing is stored in the browser.
 */
function DashboardSetupForm({ onOwnerExists, onCreated }: { onOwnerExists: () => void; onCreated: (name: string, message: string) => void }) {
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [again, setAgain] = useState('');
  const [code, setCode] = useState('');
  const [show, setShow] = useState(false);
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [summaryRef, failed] = useSummaryFocus();
  const attempt = useRef<AbortController | null>(null);
  useEffect(() => () => attempt.current?.abort(), []);

  const passwordError = passwordProblem(password);
  const errors = {
    name: ownerNameProblem(name),
    password: passwordError,
    again: !passwordError && again !== password ? 'The passwords don’t match.' : undefined,
    code: setupCodeProblem(code),
  };
  const visible: Partial<typeof errors> = show ? errors : {};

  const submit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (pending) return;
    setShow(true); setFailure(null);
    if (Object.values(errors).some(Boolean)) { failed(); return; }
    const controller = new AbortController();
    attempt.current = controller;
    setPending(true);
    let created: AccountView | null = null;
    try {
      created = await createDashboardOwner({ name, password, code }, controller.signal);
      setCode('');
      const result = await passwordSignIn(pageOrigin() ?? '', { name: created.name, password }, controller.signal);
      if (result.kind !== 'signed-in') throw new Error('This account needs a code to sign in.');
      await session.connect(result.api, controller.signal);
      notify(`Owner account “${created.name}” created. You’re signed in to this dashboard.`);
    } catch (error) {
      if (isAbort(error)) return;
      if (created) {
        // The owner exists; only the automatic sign-in didn't work, so the ordinary sign-in takes over.
        onCreated(created.name, `Owner account “${created.name}” created. Sign in with it. (${describeFailure(error)})`);
        return;
      }
      if (error instanceof ApiFailure && error.status === 409) { onOwnerExists(); return; }
      // The passwords are cleared below; the summary says what happened instead of per-field "enter a password".
      setShow(false);
      if (error instanceof ApiFailure && error.status === 401) {
        setCode('');
        setFailure('That setup code wasn’t accepted. Copy it again from the dashboard server’s private setup file or log.');
      } else {
        setFailure(`${describeFailure(error, { preferDetail: true })}${error instanceof ApiFailure && error.status === 0 ? ' If the connection dropped, reload the page: the owner may already exist.' : ''}`);
      }
      failed();
    } finally {
      setPassword(''); setAgain('');
      if (attempt.current === controller) attempt.current = null;
      setPending(false);
    }
  };

  const errorList = failure ? [] : Object.values(visible).filter(Boolean) as string[];
  return (
    <form className="connect-form" onSubmit={event => { void submit(event); }} noValidate aria-busy={pending}>
      <ErrorSummary ref={summaryRef} title={failure ? 'Owner account not created' : 'Check these fields'} errors={errorList}>{failure ? <p>{failure}</p> : null}</ErrorSummary>
      <TextField label="Account name" value={name} onChange={setName} error={visible.name} autoComplete="username" maxLength={128} disabled={pending} autoFocus
        hint="You’ll sign in to this dashboard with it." />
      <PasswordField label="Password" name="password" value={password} onChange={setPassword} error={visible.password} autoComplete="new-password" disabled={pending}
        hint="12 to 128 characters." />
      <PasswordField label="Confirm password" value={again} onChange={setAgain} error={visible.again} autoComplete="new-password" disabled={pending} />
      <SecretField label="Setup code" value={code} onChange={setCode} error={visible.code} disabled={pending}
        hint="The dashboard server prints it in its log at start and keeps it in setup.code in its private identity folder. It shows you run this server, so someone who only opens this page can’t claim it." />
      <div className="row-wrap">
        <button type="submit" className="btn primary" disabled={pending}><Icon name="users" />{pending ? 'Creating account…' : 'Create owner account'}</button>
      </div>
    </form>
  );
}

/**
 * Control plane: sign in again, or as another dashboard account, without leaving the selected node. Selecting a node
 * never needs this; it keeps the same sign-in.
 */
function DashboardSignInDialog({ reason, onClose }: { reason?: string; onClose: () => void }) {
  const { api, overview, account, selection } = useCanary();
  const [busy, setBusy] = useState(false);
  return (
    <Modal title="Dashboard sign-in" onClose={onClose} busy={busy}>
      {reason ? <p className="modal-note">{reason}</p> : null}
      <p className="sec">
        You’re signed in to this dashboard as {account ? `${account.name} (${account.role})` : who(overview)}.
        Signing in again changes only this tab’s sign-in{selection ? `, and you keep viewing ${selection.label}` : ''}. Monitoring and alerts continue on every node.
      </p>
      <SignInForm
        initialUrl={api.baseUrl}
        initialName={account?.name ?? overview.user.name}
        submitLabel="Sign in"
        onBusy={setBusy}
        onConnected={(next, setup) => {
          onClose();
          const fresh = session.getSnapshot().connection;
          if (selection && fresh) session.select(selection, fresh);
          afterSignIn(next, setup, 'Signed in to');
        }}
      />
      <div className="modal-foot">
        <span className="muted small">Use an account on this dashboard.{api.credential === 'session' ? ' A new sign-in replaces this tab’s session.' : ''}</span>
        <button type="button" className="btn" onClick={() => { void signOut(); }} disabled={busy}><Icon name="logout" />{api.credential === 'session' ? 'Sign out' : 'Disconnect'}</button>
      </div>
    </Modal>
  );
}
