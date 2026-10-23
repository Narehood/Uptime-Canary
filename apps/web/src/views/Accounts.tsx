import { useId, useState, type FormEvent } from 'react';
import type { AccountView, Role } from '@uptime-canary/shared';
import { ApiFailure, checkToken, describeFailure, passwordProblem } from '../api';
import { useCanary } from '../context';
import { shortId } from '../format';
import { ROLES } from '../model';
import { useResource, useSummaryFocus } from '../hooks';
import { notify } from '../notices';
import { session } from '../session';
import { Banner, Card, CopyButton, EmptyState, ErrorText, Modal, PageHead, Tag } from '../ui/bits';
import { ErrorSummary, PasswordField, TextField } from '../ui/forms';
import { Glyph, Icon } from '../ui/icons';

/** 32 random bytes as hex: 64 printable characters, well inside the node's 24–256 rule. */
function generateToken(): string {
  for (;;) {
    const bytes = crypto.getRandomValues(new Uint8Array(32));
    const token = Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('');
    if (new Set(token).size >= 8) return token;
  }
}

function tokenProblem(token: string): string | undefined {
  const basic = checkToken(token);
  if (basic) return basic === 'Enter the account token for this node.' ? 'Enter a token, or switch back to generating one.' : basic;
  if (token.length < 24 || token.length > 256) return 'Use 24 to 256 characters.';
  if (new Set(token).size < 8) return 'Use a more varied token (at least 8 different characters).';
  return undefined;
}

function nameProblem(name: string): string | undefined {
  if (!name.trim()) return 'Enter a name.';
  if (name.length > 128 || /[\x00-\x1f\x7f]/u.test(name)) return 'Use at most 128 characters, without control characters.';
  return undefined;
}

interface Issued { name: string; role: Role; token: string; rotated: boolean }

export function AccountsView() {
  const { admin, api, endpoint, account: me } = useCanary();
  const accounts = useResource<AccountView[]>(`${api.baseUrl}|accounts|${admin}`, signal => admin ? api.accounts(signal) : Promise.resolve([]));
  // A one-time token lives only in this view's state and disappears when it unmounts or on sign-out.
  const [issued, setIssued] = useState<Issued | null>(null);
  const [managing, setManaging] = useState<AccountView | null>(null);

  if (!admin) {
    return <div className="view"><PageHead title="Accounts" /><Card><EmptyState title="Only admins manage accounts">Your account on {endpoint} can’t list or change accounts. Your own sign-in settings are under <a href="#/account">Your account</a>.</EmptyState></Card></div>;
  }
  const list = accounts.data ? [...accounts.data].sort((a, b) => a.name.localeCompare(b.name)) : [];
  const admins = list.filter(entry => entry.role === 'admin').length;
  return (
    <div className="view">
      <PageHead title="Accounts" sub={<>Accounts on <span className="mono">{endpoint}</span>. Each node keeps its own accounts, passwords and sessions; they aren’t replicated.</>} />
      {issued ? (
        <section className="card issued" aria-labelledby="issued-h">
          <div className="card-h"><div><h2 id="issued-h"><Glyph tone="up" /> {issued.rotated ? <>New API token for “{issued.name}”</> : <>Account “{issued.name}” created</>}</h2><div className="hint">This is the only time this token is shown. {endpoint} stores only a hash.</div></div></div>
          <div className="secret wide">
            <input className="mono" readOnly value={issued.token} aria-label={`API token for ${issued.name}`} onFocus={event => event.target.select()} />
            <CopyButton value={issued.token} label="Copy token" className="btn" />
          </div>
          <p className="foot-note">
            {issued.rotated ? 'The previous token stopped working when the node accepted this one. ' : ''}
            Give it to whoever runs the scripts or tools for this {issued.role} account. To use the same token on other nodes, create or update the account there with it.
          </p>
          <button type="button" className="btn primary" onClick={() => setIssued(null)}><Icon name="check" />I’ve stored it</button>
        </section>
      ) : null}
      <div className="g2">
        <Card title="Accounts" hint={`Known to ${endpoint}`} actions={<button type="button" className="icon-btn sm" onClick={accounts.reload} aria-label="Reload accounts" data-tip="Reload accounts"><Icon name="refresh" /></button>}>
          {accounts.error && !accounts.data ? <Banner tone="down" title="Accounts could not be loaded">{describeFailure(accounts.error)}</Banner>
            : !accounts.data ? <p className="muted">Loading accounts…</p>
            : list.length ? (
              <div className="tbl-wrap">
                <table className="tbl stack">
                  <caption className="vh">Accounts on {endpoint}</caption>
                  <thead><tr><th scope="col">Name</th><th scope="col">Role</th><th scope="col">Sign-in</th><th scope="col">ID</th><th scope="col"><span className="vh">Actions</span></th></tr></thead>
                  <tbody>
                    {list.map(entry => (
                      <tr key={entry.id}>
                        <td className="lead" data-label="Name">{entry.name}{me?.id === entry.id ? <> <Tag tone="accent">you</Tag></> : null}</td>
                        <td data-label="Role"><span className="tag">{entry.role}</span></td>
                        <td data-label="Sign-in"><SignInTags account={entry} /></td>
                        <td data-label="ID"><span className="mono muted" title={entry.id}>{shortId(entry.id)}</span></td>
                        <td data-label="Actions" className="acts-cell"><button type="button" className="btn sm" onClick={() => setManaging(entry)} aria-label={`Manage ${entry.name}`}><Icon name="edit" />Manage</button></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : <EmptyState title="No accounts listed" />}
          <dl className="roles">
            {ROLES.map(entry => <div key={entry.role}><dt>{entry.label}</dt><dd>{entry.can}</dd></div>)}
          </dl>
        </Card>
        <NewAccount onCreated={(created, token) => { if (token) setIssued({ name: created.name, role: created.role, token, rotated: false }); accounts.reload(); }} />
      </div>
      {managing ? (
        <ManageAccount
          key={managing.id} account={managing} lastAdmin={managing.role === 'admin' && admins <= 1}
          onClose={() => setManaging(null)}
          onChanged={updated => { setManaging(updated); accounts.reload(); }}
          onIssued={(updated, token) => { setIssued({ name: updated.name, role: updated.role, token, rotated: true }); setManaging(null); accounts.reload(); }}
          onDeleted={() => { setManaging(null); accounts.reload(); }}
        />
      ) : null}
    </div>
  );
}

function SignInTags({ account }: { account: AccountView }) {
  if (account.passwordEnabled === undefined) return <span className="muted">Not reported</span>;
  if (!account.passwordEnabled) return <span className="nowrap"><Icon name="key" className="inline" />API token only</span>;
  return (
    <span className="tags">
      <Tag icon="lock">Password</Tag>
      {account.mfaEnabled ? <Tag icon="check" tone="accent">Two-factor</Tag> : <span className="muted small">no two-factor</span>}
    </span>
  );
}

function TokenChoice({ mode, onMode, token, onToken, error, disabled, name }: {
  mode: 'generate' | 'provide'; onMode: (mode: 'generate' | 'provide') => void; token: string; onToken: (value: string) => void; error?: string; disabled?: boolean; name: string;
}) {
  const id = useId();
  return (
    <>
      <label className="check"><input type="radio" name={name} checked={mode === 'generate'} onChange={() => onMode('generate')} disabled={disabled} /><span>Generate a random token<small>Created in this browser and shown once, after the node accepts it.</small></span></label>
      <label className="check"><input type="radio" name={name} checked={mode === 'provide'} onChange={() => onMode('provide')} disabled={disabled} /><span>Use a token I already have<small>For mirroring the same account on another node.</small></span></label>
      {mode === 'provide' ? (
        <div className="fld">
          <label htmlFor={`${id}-token`} className="vh">Existing token</label>
          <input id={`${id}-token`} type="password" className="mono" value={token} onChange={event => onToken(event.target.value)} autoComplete="off" spellCheck={false} disabled={disabled}
            aria-invalid={error ? true : undefined} aria-describedby={error ? `${id}-token-e` : undefined} />
          <ErrorText id={`${id}-token-e`}>{error}</ErrorText>
        </div>
      ) : null}
    </>
  );
}

function NewAccount({ onCreated }: { onCreated: (account: AccountView, token: string | null) => void }) {
  const { api, endpoint, connection } = useCanary();
  const id = useId();
  const [name, setName] = useState('');
  const [role, setRole] = useState<Role>('viewer');
  const [withPassword, setWithPassword] = useState(true);
  const [password, setPassword] = useState('');
  const [again, setAgain] = useState('');
  const [withToken, setWithToken] = useState(false);
  const [mode, setMode] = useState<'generate' | 'provide'>('generate');
  const [token, setToken] = useState('');
  const [show, setShow] = useState(false);
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [summaryRef, failed] = useSummaryFocus();

  const errors: { name?: string; methods?: string; password?: string; again?: string; token?: string } = {};
  errors.name = nameProblem(name);
  if (!withPassword && !withToken) errors.methods = 'Choose a password, an API token, or both.';
  if (withPassword) {
    errors.password = passwordProblem(password);
    if (!errors.password && again !== password) errors.again = 'The passwords don’t match.';
  }
  if (withToken && mode === 'provide') errors.token = tokenProblem(token);
  const visible = show ? errors : {};

  const submit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (saving || !session.isCurrent(connection)) return;
    setShow(true); setFailure(null);
    if (Object.values(errors).some(Boolean)) { failed(); return; }
    const secret = withToken ? (mode === 'generate' ? generateToken() : token) : undefined;
    setSaving(true);
    try {
      const account = await api.createAccount({ name: name.trim(), role, ...(withPassword ? { password } : {}), ...(secret ? { token: secret } : {}) });
      if (!session.isCurrent(connection)) return;
      notify(`Created ${account.role} account “${account.name}” on ${endpoint}.${withPassword ? ' Share the password over a safe channel; they can change it under Your account.' : ''}`);
      setName(''); setPassword(''); setAgain(''); setToken(''); setShow(false);
      onCreated(account, withToken && mode === 'generate' ? secret! : null);
    } catch (error) {
      if (!session.isCurrent(connection)) return;
      setFailure(`${describeFailure(error, { preferDetail: true })}${error instanceof ApiFailure && error.status !== 0 ? '' : ' If the connection dropped, check the list: the account may exist.'}`);
      failed();
    } finally { if (session.isCurrent(connection)) setSaving(false); }
  };

  return (
    <Card title="Add an account" hint={`Created on ${endpoint} only`}>
      <form className="form one" onSubmit={event => { void submit(event); }} noValidate aria-busy={saving}>
        <ErrorSummary ref={summaryRef} title="Account not created" errors={failure ? [] : Object.values(visible).filter(Boolean) as string[]}>
          {failure ? <p>{failure}</p> : null}
        </ErrorSummary>
        <TextField full label="Name" value={name} onChange={setName} error={visible.name} hint="Used to sign in and shown as the signed-in account. Unique on this node, ignoring case." maxLength={128} disabled={saving} autoComplete="off" />
        <div className="fld full">
          <label htmlFor={`${id}-role`}>Role</label>
          <select id={`${id}-role`} value={role} onChange={event => setRole(event.target.value as Role)} disabled={saving}>
            {ROLES.map(entry => <option key={entry.role} value={entry.role}>{entry.label}</option>)}
          </select>
          <small>{ROLES.find(entry => entry.role === role)!.can}</small>
        </div>
        <fieldset className="fld full" disabled={saving} aria-invalid={visible.methods ? true : undefined}>
          <legend>How they sign in</legend>
          <label className="check"><input type="checkbox" checked={withPassword} onChange={event => setWithPassword(event.target.checked)} /><span>Password<small>For people using the dashboard. They can add two-factor authentication themselves.</small></span></label>
          {withPassword ? (
            <div className="fld sub-fields">
              <PasswordField label="Initial password" value={password} onChange={setPassword} error={visible.password} autoComplete="new-password" disabled={saving} hint="12 to 128 characters. It’s never shown again; they can change it after signing in." />
              <PasswordField label="Confirm password" value={again} onChange={setAgain} error={visible.again} autoComplete="new-password" disabled={saving} />
            </div>
          ) : null}
          <label className="check"><input type="checkbox" checked={withToken} onChange={event => setWithToken(event.target.checked)} /><span>API token<small>For the CLI, scripts and headless access.</small></span></label>
          {withToken ? (
            <div className="fld sub-fields">
              <TokenChoice name={`${id}-mode`} mode={mode} onMode={setMode} token={token} onToken={setToken} error={visible.token} disabled={saving} />
            </div>
          ) : null}
          <ErrorText id={`${id}-methods-e`}>{visible.methods}</ErrorText>
        </fieldset>
        <div className="full form-acts">
          <button type="submit" className="btn primary" disabled={saving}><Icon name="plus" />{saving ? `Creating on ${endpoint}…` : 'Create account'}</button>
        </div>
      </form>
    </Card>
  );
}

interface ManageProps {
  account: AccountView;
  lastAdmin: boolean;
  onClose: () => void;
  onChanged: (account: AccountView) => void;
  onIssued: (account: AccountView, token: string) => void;
  onDeleted: () => void;
}

/** Each section is its own request, so one failure never half-applies another. */
function ManageAccount({ account, lastAdmin, onClose, onChanged, onIssued, onDeleted }: ManageProps) {
  const { api, endpoint, account: me, connection } = useCanary();
  const id = useId();
  const self = me?.id === account.id;
  const [busy, setBusy] = useState<null | 'details' | 'token' | 'password' | 'delete'>(null);
  const [errors, setErrors] = useState<Record<string, string | undefined>>({});
  const [failure, setFailure] = useState<{ section: string; message: string } | null>(null);

  const [name, setName] = useState(account.name);
  const [role, setRole] = useState<Role>(account.role);
  const [mode, setMode] = useState<'generate' | 'provide'>('generate');
  const [token, setToken] = useState('');
  const [password, setPassword] = useState('');
  const [again, setAgain] = useState('');
  const [resetMfa, setResetMfa] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);

  /** After a change to the signed-in account itself, follow what the node did to this tab's credential. */
  const afterSelfChange = (updated: AccountView | null, message: string): boolean => {
    if (!session.isCurrent(connection)) return true;
    if (!self) return false;
    if (updated === null || api.credential === 'session') {
      session.end({ url: api.baseUrl, name: updated?.name ?? account.name, message, tone: updated === null ? 'warn' : 'info' }, connection);
      return true;
    }
    session.setAccount(updated, connection);
    session.refresh();
    return false;
  };

  const run = async <T,>(section: 'details' | 'token' | 'password' | 'delete', action: () => Promise<T>): Promise<T | undefined> => {
    if (busy || !session.isCurrent(connection)) return undefined;
    setBusy(section); setFailure(null);
    try {
      const result = await action();
      return session.isCurrent(connection) ? result : undefined;
    }
    catch (error) {
      if (!session.isCurrent(connection)) return undefined;
      setFailure({ section, message: describeFailure(error, { preferDetail: true }) });
      return undefined;
    } finally { if (session.isCurrent(connection)) setBusy(null); }
  };

  const saveDetails = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    const problem = nameProblem(name);
    setErrors({ name: problem });
    if (problem) return;
    const changes: { name?: string; role?: Role } = {};
    if (name.trim() !== account.name) changes.name = name.trim();
    if (role !== account.role) changes.role = role;
    if (!Object.keys(changes).length) { setFailure({ section: 'details', message: 'Nothing changed.' }); return; }
    const updated = await run('details', () => api.updateAccount(account.id, changes));
    if (!updated || !session.isCurrent(connection)) return;
    notify(`Saved “${updated.name}” (${updated.role}) on ${endpoint}. Its password sessions were signed out.`);
    if (afterSelfChange(updated, 'Your account changed, which signed out your password sessions. Sign in again.')) return;
    onChanged(updated);
  };

  const saveToken = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    const problem = mode === 'provide' ? tokenProblem(token) : undefined;
    setErrors({ token: problem });
    if (problem) return;
    const secret = mode === 'generate' ? generateToken() : token;
    const updated = await run('token', () => api.updateAccount(account.id, { token: secret }));
    if (!updated || !session.isCurrent(connection)) return;
    setToken('');
    if (self && api.credential === 'token') session.adoptToken(secret, connection);
    else if (afterSelfChange(updated, 'Your API token was replaced, which signed out your password sessions. Sign in again.')) return;
    notify(`Replaced the API token for “${updated.name}” on ${endpoint}.${self && api.credential === 'token' ? ' This tab now uses the new token.' : ''}`);
    if (mode === 'generate') onIssued(updated, secret); else onChanged(updated);
  };

  const savePassword = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    const next = { password: passwordProblem(password), again: undefined as string | undefined };
    if (!next.password && again !== password) next.again = 'The passwords don’t match.';
    setErrors(next);
    if (next.password || next.again) return;
    const updated = await run('password', () => api.resetAccountPassword(account.id, password, resetMfa));
    if (!updated || !session.isCurrent(connection)) return;
    setPassword(''); setAgain('');
    notify(`Reset the password for “${updated.name}” on ${endpoint}${resetMfa ? ' and turned off its two-factor authentication' : ''}. Its password sessions were signed out.`);
    setResetMfa(false);
    if (afterSelfChange(updated, 'Your password was reset. Sign in with the new password.')) return;
    onChanged(updated);
  };

  const remove = async (): Promise<void> => {
    const result = await run('delete', async () => { await api.deleteAccount(account.id); return true; });
    if (!result || !session.isCurrent(connection)) return;
    notify(`Deleted account “${account.name}” from ${endpoint}. Its sessions ended; its token no longer works.`);
    if (afterSelfChange(null, `Your account “${account.name}” was deleted from ${endpoint}.`)) return;
    onDeleted();
  };

  const sectionError = (section: string) => failure?.section === section
    ? <div className="errsum" role="alert"><Glyph tone="down" /><div><b>{failure.message === 'Nothing changed.' ? 'Nothing to save' : 'Not saved'}</b><p>{failure.message}</p></div></div>
    : null;
  const disabled = busy !== null;

  return (
    <Modal title={<>Manage {account.name}{self ? <> <Tag tone="accent">you</Tag></> : null}</>} onClose={onClose} busy={disabled} wide>
      <p className="sec">
        {account.role} on <span className="mono">{endpoint}</span> · <span className="mono muted">{account.id}</span> · <SignInTags account={account} />
      </p>
      {self ? (
        <p className="modal-note">
          This is your own account. {api.credential === 'session'
            ? 'Any change here signs out your password sessions, including this tab; you’ll sign in again.'
            : 'Changes sign out its password sessions; this tab stays connected with the account token.'}
        </p>
      ) : null}

      <form className="msec form" onSubmit={event => { void saveDetails(event); }} noValidate>
        <h3 className="full">Name and role</h3>
        {sectionError('details')}
        <TextField label="Name" value={name} onChange={setName} error={errors.name} maxLength={128} disabled={disabled} />
        <div className="fld">
          <label htmlFor={`${id}-role`}>Role</label>
          <select id={`${id}-role`} value={role} onChange={event => setRole(event.target.value as Role)} disabled={disabled}>
            {ROLES.map(entry => <option key={entry.role} value={entry.role}>{entry.label}</option>)}
          </select>
          {lastAdmin && role !== 'admin' ? <small className="fwarn"><Glyph tone="warn" />This is the only admin. {endpoint} keeps at least one, so it will refuse.</small>
            : self && account.role === 'admin' && role !== 'admin' ? <small className="fwarn"><Glyph tone="warn" />You’ll lose access to account management.</small> : null}
        </div>
        <div className="full row-wrap">
          <button type="submit" className="btn primary" disabled={disabled}><Icon name="check" />{busy === 'details' ? 'Saving…' : 'Save name and role'}</button>
        </div>
      </form>

      <form className="msec form one" onSubmit={event => { void saveToken(event); }} noValidate>
        <h3>API token</h3>
        <p className="muted small">{endpoint} doesn’t report whether this account has a token. Setting one replaces any existing token immediately, and tools using the old one stop working. Tokens can be replaced, not removed.</p>
        {sectionError('token')}
        <fieldset className="fld" disabled={disabled}>
          <legend className="vh">New token</legend>
          <TokenChoice name={`${id}-tmode`} mode={mode} onMode={setMode} token={token} onToken={setToken} error={errors.token} disabled={disabled} />
        </fieldset>
        <div className="row-wrap"><button type="submit" className="btn" disabled={disabled}><Icon name="key" />{busy === 'token' ? 'Replacing…' : 'Replace API token'}</button></div>
      </form>

      <form className="msec form" onSubmit={event => { void savePassword(event); }} noValidate>
        <h3 className="full">{account.passwordEnabled ? 'Reset password' : 'Set a password'}</h3>
        <p className="muted small full">{account.passwordEnabled ? 'Use this when they forgot it. ' : 'Lets this account sign in to the dashboard. '}They can change it under Your account after signing in.</p>
        {sectionError('password')}
        <PasswordField label="New password" value={password} onChange={setPassword} error={errors.password} autoComplete="new-password" disabled={disabled} hint="12 to 128 characters." />
        <PasswordField label="Confirm password" value={again} onChange={setAgain} error={errors.again} autoComplete="new-password" disabled={disabled} />
        {account.mfaEnabled ? (
          <label className="check full">
            <input type="checkbox" checked={resetMfa} onChange={event => setResetMfa(event.target.checked)} disabled={disabled} />
            <span>Also turn off two-factor authentication<small>For a lost authenticator. They can set it up again after signing in. Otherwise two-factor stays on.</small></span>
          </label>
        ) : null}
        <div className="full row-wrap"><button type="submit" className="btn" disabled={disabled}><Icon name="lock" />{busy === 'password' ? 'Saving…' : account.passwordEnabled ? 'Reset password' : 'Set password'}</button></div>
      </form>

      <div className="msec danger-zone">
        <h3>Delete account</h3>
        {sectionError('delete')}
        {lastAdmin ? <p className="muted small"><Glyph tone="warn" /> This is the only admin on {endpoint}. Make another account an admin before deleting it.</p> : null}
        {!confirmDelete ? (
          <div className="row-wrap"><button type="button" className="btn danger" onClick={() => setConfirmDelete(true)} disabled={disabled}><Icon name="trash" />Delete {account.name}…</button></div>
        ) : (
          <div className="confirm-row">
            <p>Delete <b>{account.name}</b> from {endpoint}? Its sessions end and its token stops working{self ? ', including yours in this tab' : ''}. Other nodes keep their own accounts.</p>
            <div className="row-wrap">
              <button type="button" className="btn danger-solid" onClick={() => { void remove(); }} disabled={disabled}><Icon name="trash" />{busy === 'delete' ? 'Deleting…' : 'Delete account'}</button>
              <button type="button" className="btn" onClick={() => setConfirmDelete(false)} disabled={disabled}>Cancel</button>
            </div>
          </div>
        )}
      </div>
    </Modal>
  );
}
