import { controlPlaneMode, gatewayMode } from './gateway';

const KEY = 'uptime-canary.sign-in';
export interface SignInIssuer { nodeId: string; accountId: string | null }
export interface SavedSignIn { baseUrl: string; token: string; credential: 'token' | 'session'; expiresAt: number | null; issuer: SignInIssuer }
const mode = (): string => controlPlaneMode() ? 'dashboard' : gatewayMode() ? 'gateway' : 'direct';

/** Tab-scoped sign-in, bound to this page's origin and deployment mode. Never a password or MFA code. */
export function rememberSignIn(value: SavedSignIn | null): void {
  try {
    if (value) sessionStorage.setItem(KEY, JSON.stringify({ ...value, origin: location.origin, mode: mode() }));
    else sessionStorage.removeItem(KEY);
  } catch { /* Storage can be disabled; the current in-memory sign-in still works. */ }
}

export function savedSignIn(): SavedSignIn | null {
  try {
    const raw = sessionStorage.getItem(KEY);
    if (!raw) return null;
    const value = raw.length <= 4096 ? JSON.parse(raw) : null;
    if (!value || value.origin !== location.origin || value.mode !== mode()
      || typeof value.baseUrl !== 'string' || value.baseUrl.length > 2048
      || typeof value.token !== 'string' || !/^[\x21-\x7e]{1,256}$/u.test(value.token)
      || !['token', 'session'].includes(value.credential)
      || !value.issuer || typeof value.issuer.nodeId !== 'string' || !value.issuer.nodeId.length || value.issuer.nodeId.length > 256
      || !(value.issuer.accountId === null || typeof value.issuer.accountId === 'string' && value.issuer.accountId.length > 0 && value.issuer.accountId.length <= 256)
      || !(value.expiresAt === null || Number.isFinite(value.expiresAt) && value.expiresAt > 0)
      || gatewayMode() && value.baseUrl !== location.origin) {
      rememberSignIn(null); return null;
    }
    return { baseUrl: value.baseUrl, token: value.token, credential: value.credential, expiresAt: value.expiresAt, issuer: { nodeId: value.issuer.nodeId, accountId: value.issuer.accountId } };
  } catch { rememberSignIn(null); return null; }
}
