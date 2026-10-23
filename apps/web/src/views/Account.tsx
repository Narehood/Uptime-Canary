import { useEffect, useMemo, useState, type FormEvent } from 'react';
import type { AccountView as Account, AuthenticatorEnrollment } from '@uptime-canary/shared';
import { ApiFailure, codeProblem, describeFailure, fetchAuthStatus, passwordProblem } from '../api';
import { useCanary } from '../context';
import { elapsed, shortClock } from '../format';
import { useNow, useResource, useSummaryFocus } from '../hooks';
import { ROLES } from '../model';
import { notify } from '../notices';
import { session, signOut } from '../session';
import { Banner, Card, CopyButton, EmptyState, Modal, PageHead, Tag, Time } from '../ui/bits';
import { CodeField, ErrorSummary, PasswordField } from '../ui/forms';
import { Glyph, Icon } from '../ui/icons';
import { encodeQr } from '../ui/qr';

/** Base32 key in groups of four, the way authenticator apps show it. */
const grouped = (secret: string): string => secret.replace(/(.{4})/gu, '$1 ').trim();

export function AccountView() {
  const { api, endpoint, account, overview } = useCanary();
  const status = useResource(`${api.baseUrl}|auth-status`, signal => fetchAuthStatus(api.baseUrl, signal));
  const name = account?.name ?? overview.user.name;
  const role = account?.role ?? overview.user.role;
  const supportsPasswords = status.data ? status.data.passwordLogin : true;

  return (
    <div className="view">
      <PageHead
        title="Your account"
        sub={<><b>{name}</b> · {role} on <span className="mono">{endpoint}</span>. Sign-in settings belong to this node only; other nodes keep their own accounts.</>}
      />
      <div className="g2">
        <div className="vstack">
          {!account ? (
            <Card title="Sign-in settings">
              <EmptyState title={`${endpoint} doesn’t offer account settings`}>This node predates password sign-in. You’re connected with an account token; an admin can upgrade the node to add passwords and two-factor authentication.</EmptyState>
            </Card>
          ) : !supportsPasswords ? (
            <Card title="Sign-in settings"><EmptyState title="Token sign-in only">{endpoint} doesn’t offer password sign-in.</EmptyState></Card>
          ) : (
            <>
              <PasswordCard account={account} />
              <TwoFactorCard account={account} mfaSupported={status.data?.mfaSupported ?? true} />
            </>
          )}
        </div>
        <div className="vstack">
          <SignedInCard account={account} />
          <Card title="Your role" hint={`On ${endpoint}`}>
            <dl className="roles">
              {ROLES.map(entry => <div key={entry.role} className={entry.role === role ? 'me-role' : undefined}><dt>{entry.label}{entry.role === role ? <span className="vh"> (yours)</span> : null}</dt><dd>{entry.can}</dd></div>)}
            </dl>
            <p className="foot-note">Only an admin can change your role. Admins can also reset your password and two-factor authentication if you lose access.</p>
          </Card>
        </div>
      </div>
    </div>
  );
}

function SignedInCard({ account }: { account: Account | null }) {
  const { api, endpoint, overview, connection } = useCanary();
  const now = useNow(30_000);
  const [confirmAll, setConfirmAll] = useState(false);
  const [busy, setBusy] = useState(false);
  const nodeNow = now + (overview.now - connection.fetchedAt);
  return (
    <Card title="How you’re signed in">
      <dl className="kv">
        <dt>Method</dt>
        <dd>{api.credential === 'session' ? <><Icon name="lock" className="inline" />Password session</> : <><Icon name="key" className="inline" />Account token</>}</dd>
        {api.expiresAt ? (
          <><dt>Session ends</dt><dd><Time at={api.expiresAt} now={overview.now} /> <span className="muted">· in {elapsed(Math.max(0, api.expiresAt - nodeNow))}</span></dd></>
        ) : (
          <><dt>Valid</dt><dd>Until an admin replaces the token or removes the account</dd></>
        )}
        <dt>Kept</dt><dd>In this tab’s memory only. Reloading or closing the tab asks you to sign in again.</dd>
        {account ? <><dt>Account ID</dt><dd><span className="mono">{account.id}</span></dd></> : null}
      </dl>
      <div className="row-wrap card-acts">
        <button type="button" className="btn" disabled={busy} onClick={() => { setBusy(true); void signOut().finally(() => setBusy(false)); }}>
          <Icon name="logout" />{busy ? 'Signing out…' : api.credential === 'session' ? 'Sign out' : 'Disconnect'}
        </button>
        {account?.passwordEnabled ? <button type="button" className="btn" onClick={() => setConfirmAll(true)}><Icon name="close" />Sign out everywhere</button> : null}
      </div>
      <p className="foot-note">
        {api.credential === 'session'
          ? `Signing out ends this session on ${endpoint} before the tab forgets it.`
          : 'Disconnecting only removes the token from this tab; the token itself keeps working.'}
      </p>
      {confirmAll && account ? <SignOutEverywhere account={account} onClose={() => setConfirmAll(false)} /> : null}
    </Card>
  );
}

function SignOutEverywhere({ account, onClose }: { account: Account; onClose: () => void }) {
  const { api, endpoint, connection } = useCanary();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const run = async (): Promise<void> => {
    if (!session.isCurrent(connection)) return;
    setBusy(true); setError(null);
    try {
      await api.logoutAll();
      if (!session.isCurrent(connection)) return;
      if (api.credential === 'session') {
        session.end({ url: api.baseUrl, name: account.name, message: `Signed out of every password session for ${account.name} on ${endpoint}.`, tone: 'info' }, connection);
      } else {
        notify(`${endpoint} ended every password session for ${account.name}. This tab stays connected with the account token.`);
        onClose();
      }
    } catch (failure) {
      if (!session.isCurrent(connection)) return;
      setError(describeFailure(failure, { preferDetail: true }));
      setBusy(false);
    }
  };
  return (
    <Modal title="Sign out everywhere?" onClose={onClose} busy={busy}>
      <p>{endpoint} ends every password session for <b>{account.name}</b>, on every browser and device{api.credential === 'session' ? ', including this one' : ''}.</p>
      <p className="muted small">Account tokens keep working. Other nodes keep their own sessions.</p>
      {error ? <div className="errsum" role="alert"><Glyph tone="down" /><div><b>Nothing changed</b><p>{error}</p></div></div> : null}
      <div className="row-wrap modal-acts">
        <button type="button" className="btn danger-solid" onClick={() => { void run(); }} disabled={busy}><Icon name="logout" />{busy ? 'Signing out…' : 'Sign out everywhere'}</button>
        <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
      </div>
    </Modal>
  );
}

function PasswordCard({ account }: { account: Account }) {
  const { api, endpoint, connection } = useCanary();
  const first = !account.passwordEnabled;
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [again, setAgain] = useState('');
  const [code, setCode] = useState('');
  const [show, setShow] = useState(false);
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [justSet, setJustSet] = useState(false);
  const [summaryRef, failed] = useSummaryFocus();

  const errors: { current?: string; next?: string; again?: string; code?: string } = {};
  if (!first && !current) errors.current = 'Enter your current password.';
  errors.next = passwordProblem(next);
  if (!errors.next && again !== next) errors.again = 'The passwords don’t match.';
  if (!first && account.mfaEnabled) errors.code = codeProblem(code);
  const visible = show ? errors : {};

  const submit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (saving || !session.isCurrent(connection)) return;
    setShow(true); setFailure(null);
    if (Object.values(errors).some(Boolean)) { failed(); return; }
    setSaving(true);
    try {
      const updated = await api.setPassword(next, first ? undefined : current, !first && account.mfaEnabled ? code : undefined);
      if (!session.isCurrent(connection)) return;
      setCurrent(''); setNext(''); setAgain(''); setCode(''); setShow(false);
      if (api.credential === 'session') {
        session.end({ url: api.baseUrl, name: updated.name, message: 'Password changed. Sign in with your new password.', tone: 'info' }, connection);
        return;
      }
      session.setAccount(updated, connection);
      setJustSet(first);
      notify(first
        ? `Password set for ${updated.name} on ${endpoint}. Next time, sign in with your name and password.`
        : `Password changed on ${endpoint}. Its password sessions were signed out; your account token still works.`);
    } catch (error) {
      if (!session.isCurrent(connection)) return;
      if (error instanceof ApiFailure && error.status === 401) setCode('');
      setFailure(describeFailure(error, { preferDetail: true }));
      failed();
    } finally { if (session.isCurrent(connection)) setSaving(false); }
  };

  if (justSet) {
    return (
      <Card title="Password" actions={<Tag icon="check">Set</Tag>}>
        <div className="done">
          <Glyph tone="up" />
          <div>
            <b>Your password is set on {endpoint}</b>
            <p>Sign in with <b>{account.name}</b> and this password from now on. Your account token keeps working for headless tools.</p>
            <div className="row-wrap">
              <button type="button" className="btn primary" onClick={() => session.end({ url: api.baseUrl, name: account.name, message: 'Sign in with your new password.', tone: 'info' }, connection)}>
                <Icon name="lock" />Sign in with the password now
              </button>
              <button type="button" className="btn" onClick={() => setJustSet(false)}>Stay connected</button>
            </div>
            <p className="foot-note">Next, consider turning on two-factor authentication below.</p>
          </div>
        </div>
      </Card>
    );
  }

  return (
    <Card
      title={first ? 'Choose a password' : 'Change password'}
      hint={first ? `Authorized by the account token you signed in with. Applies to ${endpoint} only.` : `Applies to ${endpoint} only.`}
      actions={first ? <Tag>Not set</Tag> : <Tag icon="check">Set</Tag>}
    >
      <form className="form one" onSubmit={event => { void submit(event); }} noValidate aria-busy={saving}>
        <input className="vh" type="text" name="username" autoComplete="username" value={account.name} readOnly tabIndex={-1} aria-hidden="true" />
        <ErrorSummary ref={summaryRef} title={failure ? 'Password not changed' : 'Check these fields'} errors={failure ? [] : Object.values(visible).filter(Boolean) as string[]}>
          {failure ? <p>{failure}</p> : null}
        </ErrorSummary>
        {!first ? <PasswordField label="Current password" value={current} onChange={setCurrent} error={visible.current} autoComplete="current-password" disabled={saving} /> : null}
        <PasswordField label="New password" value={next} onChange={setNext} error={visible.next} autoComplete="new-password" disabled={saving} autoFocus={first}
          hint="12 to 128 characters. A long passphrase is easier to remember and harder to guess." />
        <PasswordField label="Confirm new password" value={again} onChange={setAgain} error={visible.again} autoComplete="new-password" disabled={saving} />
        {!first && account.mfaEnabled ? <CodeField value={code} onChange={setCode} error={visible.code} disabled={saving} hint="Two-factor is on, so changing the password needs a fresh code." /> : null}
        <div className="form-acts">
          <button type="submit" className="btn primary" disabled={saving}><Icon name="key" />{saving ? `Saving to ${endpoint}…` : first ? 'Set password' : 'Change password'}</button>
        </div>
        <p className="foot-note">
          {first
            ? 'Your account token keeps working after you set a password.'
            : api.credential === 'session'
              ? `Changing it signs out every password session for ${account.name}, including this one. You’ll sign in again with the new password.`
              : `Changing it signs out every password session for ${account.name}. This tab stays connected with the account token.`}
        </p>
      </form>
    </Card>
  );
}

function QrImage({ value, label }: { value: string; label: string }) {
  const qr = useMemo(() => encodeQr(value), [value]);
  const quiet = 4;
  const size = qr.size + quiet * 2;
  let path = '';
  for (let y = 0; y < qr.size; y++) for (let x = 0; x < qr.size; x++) if (qr.dark(x, y)) path += `M${x + quiet} ${y + quiet}h1v1h-1z`;
  return (
    <svg className="qr" viewBox={`0 0 ${size} ${size}`} role="img" aria-label={label} shapeRendering="crispEdges">
      <rect width={size} height={size} fill="#fff" />
      <path d={path} fill="#000" />
    </svg>
  );
}

type Enrollment = AuthenticatorEnrollment & { password: string };

function TwoFactorCard({ account, mfaSupported }: { account: Account; mfaSupported: boolean }) {
  const { api, endpoint, overview, connection } = useCanary();
  const now = useNow(1000);
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [enrollment, setEnrollment] = useState<Enrollment | null>(null);
  const [disabling, setDisabling] = useState(false);
  const [show, setShow] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [summaryRef, failed] = useSummaryFocus();
  // The seed and the password typed for it live only in this component, and only until it finishes or unmounts.
  useEffect(() => () => { setEnrollment(null); setPassword(''); }, []);

  const reset = (): void => { setEnrollment(null); setPassword(''); setCode(''); setShow(false); setFailure(null); setDisabling(false); };
  const nodeNow = now + (overview.now - connection.fetchedAt);
  const remaining = enrollment ? enrollment.expiresAt - nodeNow : 0;

  if (!mfaSupported) {
    return <Card title="Two-factor authentication"><EmptyState title="Not offered by this node">{endpoint} doesn’t support authenticator codes.</EmptyState></Card>;
  }
  if (!account.passwordEnabled) {
    return (
      <Card title="Two-factor authentication" actions={<Tag>Off</Tag>}>
        <p className="prose">Two-factor authentication adds a 6-digit code from an authenticator app to password sign-in. Set a password first to turn it on.</p>
      </Card>
    );
  }

  const start = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (busy || !session.isCurrent(connection)) return;
    setShow(true); setFailure(null);
    if (!password) { failed(); return; }
    setBusy(true);
    try {
      const started = await api.startAuthenticator(password);
      if (!session.isCurrent(connection)) return;
      setEnrollment({ ...started, password });
      setShow(false);
    } catch (error) {
      if (!session.isCurrent(connection)) return;
      setFailure(describeFailure(error, { preferDetail: true }));
      failed();
    } finally { if (session.isCurrent(connection)) setBusy(false); }
  };

  const confirm = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (busy || !enrollment || !session.isCurrent(connection)) return;
    setShow(true); setFailure(null);
    if (codeProblem(code)) { failed(); return; }
    setBusy(true);
    try {
      const updated = await api.confirmAuthenticator(enrollment.password, code);
      if (!session.isCurrent(connection)) return;
      reset();
      if (api.credential === 'session') {
        session.end({ url: api.baseUrl, name: updated.name, message: 'Two-factor authentication is on. Sign in with your password and a fresh code from your app.', tone: 'info' }, connection);
        return;
      }
      session.setAccount(updated, connection);
      notify(`Two-factor authentication is on for ${updated.name} on ${endpoint}. Password sign-ins now need a code; your account token still works.`);
    } catch (error) {
      if (!session.isCurrent(connection)) return;
      if (error instanceof ApiFailure && error.status === 409) {
        reset();
        setFailure(`${describeFailure(error, { preferDetail: true })} Enter your password to get a new key.`);
      } else {
        setCode('');
        setFailure(describeFailure(error, { preferDetail: true }));
      }
      failed();
    } finally { if (session.isCurrent(connection)) setBusy(false); }
  };

  const disable = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (busy || !session.isCurrent(connection)) return;
    setShow(true); setFailure(null);
    if (!password || codeProblem(code)) { failed(); return; }
    setBusy(true);
    try {
      const updated = await api.disableAuthenticator(password, code);
      if (!session.isCurrent(connection)) return;
      reset();
      if (api.credential === 'session') {
        session.end({ url: api.baseUrl, name: updated.name, message: 'Two-factor authentication is off. Sign in with your password.', tone: 'warn' }, connection);
        return;
      }
      session.setAccount(updated, connection);
      notify(`Two-factor authentication is off for ${updated.name} on ${endpoint}.`);
    } catch (error) {
      if (!session.isCurrent(connection)) return;
      setCode('');
      setFailure(describeFailure(error, { preferDetail: true }));
      failed();
    } finally { if (session.isCurrent(connection)) setBusy(false); }
  };

  const passwordError = show && !password ? 'Enter your current password.' : undefined;
  const codeError = show ? codeProblem(code) : undefined;
  const summary = (title: string) => (
    <ErrorSummary ref={summaryRef} title={title} errors={failure ? [] : [passwordError, codeError].filter(Boolean) as string[]}>
      {failure ? <p>{failure}</p> : null}
    </ErrorSummary>
  );
  const hiddenUser = <input className="vh" type="text" name="username" autoComplete="username" value={account.name} readOnly tabIndex={-1} aria-hidden="true" />;

  if (account.mfaEnabled) {
    return (
      <Card title="Two-factor authentication" hint="Password sign-in asks for a code from your authenticator app" actions={<span className="chip c-up"><Glyph tone="up" /><b>On</b></span>}>
        {!disabling ? (
          <>
            <p className="prose">Codes are 6 digits, change every 30 seconds, and each works once. If you lose the device, an admin on {endpoint} can reset your two-factor authentication.</p>
            <div className="row-wrap card-acts"><button type="button" className="btn" onClick={() => { setDisabling(true); setFailure(null); }}><Icon name="close" />Turn off…</button></div>
          </>
        ) : (
          <form className="form one" onSubmit={event => { void disable(event); }} noValidate aria-busy={busy}>
            {hiddenUser}
            {summary('Two-factor authentication is still on')}
            <PasswordField label="Current password" value={password} onChange={setPassword} error={passwordError} autoComplete="current-password" disabled={busy} autoFocus />
            <CodeField value={code} onChange={setCode} error={codeError} disabled={busy} />
            <div className="form-acts">
              <button type="submit" className="btn danger-solid" disabled={busy}><Icon name="close" />{busy ? 'Turning off…' : 'Turn off two-factor'}</button>
              <button type="button" className="btn" onClick={reset} disabled={busy}>Cancel</button>
            </div>
            <p className="foot-note">Turning it off signs out every password session for {account.name}{api.credential === 'session' ? ', including this one' : ''}.</p>
          </form>
        )}
      </Card>
    );
  }

  if (!enrollment) {
    return (
      <Card title="Two-factor authentication" hint="Adds a code from an authenticator app to password sign-in" actions={<Tag>Off</Tag>}>
        <form className="form one" onSubmit={event => { void start(event); }} noValidate aria-busy={busy}>
          {hiddenUser}
          {summary('Couldn’t start setup')}
          <p className="prose">Works with any app that supports time-based codes (TOTP), such as a password manager or an authenticator app. Confirm your password to get a setup key.</p>
          <PasswordField label="Current password" value={password} onChange={setPassword} error={passwordError} autoComplete="current-password" disabled={busy} />
          <div className="form-acts"><button type="submit" className="btn primary" disabled={busy}><Icon name="key" />{busy ? 'Starting…' : 'Set up authenticator'}</button></div>
        </form>
      </Card>
    );
  }

  return (
    <Card title="Set up your authenticator" hint={remaining > 0 ? `This setup key expires at ${shortClock(enrollment.expiresAt, overview.now)} (${elapsed(remaining)} left)` : 'This setup key has expired'} actions={<Tag>Step 2 of 2</Tag>}>
      <div className="enroll">
        <div className="enroll-qr">
          <QrImage value={enrollment.uri} label={`QR code for adding ${account.name} on ${endpoint} to an authenticator app`} />
          <small>Generated in this browser. The key never leaves {endpoint} and this tab.</small>
        </div>
        <div className="enroll-steps">
          <ol>
            <li><b>Scan the code</b> with your authenticator app, or enter the key by hand:
              <div className="keybox"><span className="mono" aria-label={`Setup key ${enrollment.secret.split('').join(' ')}`}>{grouped(enrollment.secret)}</span><CopyButton value={enrollment.secret} label="Copy key" /></div>
              <span className="muted small">Time-based · SHA-1 · 6 digits · 30 seconds. <CopyButton value={enrollment.uri} label="Copy setup link" className="linkbtn" /></span>
            </li>
            <li><b>Enter the code</b> your app shows to finish.</li>
          </ol>
          <form className="form one" onSubmit={event => { void confirm(event); }} noValidate aria-busy={busy}>
            {summary('Two-factor authentication isn’t on yet')}
            <CodeField value={code} onChange={setCode} error={codeError} disabled={busy || remaining <= 0} autoFocus />
            <div className="form-acts">
              <button type="submit" className="btn primary" disabled={busy || remaining <= 0}><Icon name="check" />{busy ? 'Checking…' : 'Turn on two-factor'}</button>
              <button type="button" className="btn" onClick={reset} disabled={busy}>Cancel</button>
            </div>
            <p className="foot-note">
              {api.credential === 'session'
                ? `Turning it on signs out every password session for ${account.name}, including this one. Sign in again with your password and a new code.`
                : `Turning it on signs out every password session for ${account.name}. This tab stays connected with the account token.`}
            </p>
          </form>
        </div>
      </div>
      {remaining <= 0 ? <Banner tone="warn" title="Setup expired">Cancel and start again to get a new key. The expired one can’t be confirmed.</Banner> : null}
    </Card>
  );
}
