import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import type { AuthenticationStatus, Overview } from '@uptime-canary/shared';
import {
  ApiFailure, CanaryApi, checkToken, codeProblem, describeFailure, fetchAuthStatus, isAbort, normalizeApiUrl, passwordSignIn,
} from '../api';
import { useCanary } from '../context';
import { ago } from '../format';
import { useNow } from '../hooks';
import { notify } from '../notices';
import { href, navigate } from '../router';
import { session, signOut, useSession, type Farewell } from '../session';
import { Banner, ErrorText, Modal, nodeTone } from '../ui/bits';
import { CanaryMark, Glyph, Icon } from '../ui/icons';
import type { ThemeControl } from '../theme';
import { ThemeButton } from '../theme';

export const DEFAULT_ENDPOINT = 'http://127.0.0.1:3100';
const LAST_ENDPOINT_KEY = 'uptime-canary.endpoint';

/** Only the address is remembered between visits; names, passwords, codes and tokens never touch browser storage. */
export function lastEndpoint(): string {
  try { return localStorage.getItem(LAST_ENDPOINT_KEY) || DEFAULT_ENDPOINT; } catch { return DEFAULT_ENDPOINT; }
}
function rememberEndpoint(url: string): void {
  try { localStorage.setItem(LAST_ENDPOINT_KEY, url); } catch { /* storage unavailable: nothing to remember */ }
}

type Method = 'password' | 'token';
type Probe = { url: string; status: AuthenticationStatus } | { url: string; status: null };

function failureHints(error: unknown, url: string, method: Method): string[] {
  if (!(error instanceof ApiFailure)) return [];
  const origin = window.location.origin;
  if (error.status === 0) {
    const hints = [`If the address is right, make sure the node’s CANARY_CORS_ORIGINS includes ${origin}.`];
    if (window.location.protocol === 'https:' && url.startsWith('http:')) hints.unshift('Browsers block plain HTTP requests from an HTTPS page. Use an HTTPS address for the node.');
    return hints;
  }
  if (error.status === 401) {
    return method === 'password'
      ? ['Accounts and passwords are local to each node. Use an account that exists on this node.']
      : ['Accounts are local to each node. Use a token for an account that exists on this node.'];
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
  const [url, setUrl] = useState(initialUrl);
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
  const setupRequired = Boolean(status?.passwordLogin && status.setupRequired);
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
    catch (error) { setErrors({ url: error instanceof Error ? error.message : 'Invalid address' }); urlRef.current?.focus(); return; }
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

      {step === 'credentials' ? (
        <fieldset className="fld method-f" disabled={pending}>
          <legend className="vh">Sign in with</legend>
          <div className="seg method" role="presentation">
            {passwordOffered ? <label><input type="radio" name={`${id}-method`} checked={method === 'password'} onChange={() => pick('password')} /><span><Icon name="users" />Password</span></label> : null}
            <label><input type="radio" name={`${id}-method`} checked={method === 'token'} onChange={() => pick('token')} /><span><Icon name="key" />Account token</span></label>
          </div>
          {status && !status.passwordLogin ? <small>This node offers token sign-in only.</small> : null}
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
            <small id={`${id}-password-h`}><Icon name="lock" className="inline" />The 12-hour session stays only in this tab’s memory. Reloading asks you to sign in again.</small>
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
          <small id={`${id}-token-h`}><Icon name="lock" className="inline" />For headless and first-time access. Kept only in this tab’s memory; reloading asks again.</small>
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
  if (setup) {
    navigate(href('account'));
    notify(`${verb} ${overview.node.id} as ${who(overview)} with an account token. Choose a password to sign in with from now on.`);
  } else notify(`${verb} ${overview.node.id} as ${who(overview)}.`);
}

export function ConnectScreen({ theme }: { theme: ThemeControl }) {
  const { farewell } = useSession();
  // Keep the first farewell for this screen even after it's cleared from the store.
  const [note] = useState<Farewell | null>(farewell);
  useEffect(() => { session.clearFarewell(); }, []);
  return (
    <div className="connect">
      <div className="connect-top"><ThemeButton theme={theme} /></div>
      <section className="connect-brand" aria-label="About Uptime Canary">
        <CanaryMark className="connect-mark" />
        <p className="wordmark-lg"><b>Uptime</b> Canary</p>
        <p className="tagline">Self-hosted uptime monitoring that keeps working when parts of it fail.</p>
        <ul className="connect-facts">
          <li><Icon name="nodes" /><span>Talks to <b>one node at a time</b>. Every fact is shown as that node knows it.</span></li>
          <li><Icon name="lock" /><span>Your sign-in stays <b>in this tab’s memory</b>. Nothing is saved in the browser, so a reload asks again.</span></li>
          <li><Icon name="pulse" /><span><b>Optional.</b> Nodes keep monitoring and alerting whether this page is open or not.</span></li>
        </ul>
      </section>
      <main className="card connect-card" id="main">
        <h1>Sign in to a node</h1>
        <p className="sec">Use your account name and password on any Uptime-Canary node, or an account token. Accounts are local to each node.</p>
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
