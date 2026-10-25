/**
 * How this dashboard reaches its node. `/canary-config.js`, a plain script that runs before the app, sets
 * `__CANARY_DASHBOARD_GATEWAY__`:
 * - `false` (the copy in `apps/web/public`, so dev servers and plain static hosting): direct mode. The browser calls
 *   the management API at the node address typed into the sign-in form and can switch to a peer's address.
 * - `true` (the dashboard container replaces the file): gateway mode. The dashboard's own server forwards `/api/` and
 *   `/public/` to one node it is configured with, so the browser only ever talks to the page's origin and needs no
 *   route to any node.
 * Anything other than exactly `true`, including a missing file, means direct mode, except on a page the dashboard
 * server marked as its control plane (below). Read on every call, so server rendering and fixtures can set the flags
 * (and `location` and `document`) before each check.
 * The control plane implies gateway mode, so a deployment that sets only its flag, or only its page marker, still
 * never contacts another origin.
 */
export function gatewayMode(): boolean {
  return (globalThis as { __CANARY_DASHBOARD_GATEWAY__?: unknown }).__CANARY_DASHBOARD_GATEWAY__ === true || controlPlaneMode();
}

/**
 * The control plane: the dashboard owns its accounts and a shared registry of nodes. Sign-in, accounts and the node
 * list always go to the dashboard itself; monitoring requests name one saved registration in
 * `X-Canary-Registered-Node`, and the dashboard's server picks that node's stored address and the role's credential.
 * The browser never names a node address. On when either of these says so:
 * - the page carries the dashboard server's marker, exactly `<html data-canary-dashboard="control-plane">`. The
 *   server writes it into the page it serves, so a missing or blocked `/canary-config.js` can't turn that page back
 *   into direct mode, and no flag value (missing, `false` or anything else) turns the marker off.
 * - `__CANARY_DASHBOARD_CONTROL_PLANE__ === true`, which the server's runtime script also sets beside the gateway
 *   flag, and which a server rendering or fixture without a page can set.
 * Otherwise off: the development server and static hosting serve no marker, and the source copy of
 * `/canary-config.js` leaves the flag out. Any other attribute value counts as no marker.
 */
export function controlPlaneMode(): boolean {
  return (globalThis as { __CANARY_DASHBOARD_CONTROL_PLANE__?: unknown }).__CANARY_DASHBOARD_CONTROL_PLANE__ === true || serverMarked();
}

/** The dashboard server's page marker, compared exactly. False without a readable document (server rendering, fixtures). */
function serverMarked(): boolean {
  try {
    return globalThis.document?.documentElement?.getAttribute?.('data-canary-dashboard') === 'control-plane';
  } catch {
    return false;
  }
}

/** The page's own `http(s)://host[:port]` origin, or null without one (server rendering, fixtures, `file:` pages). */
export function pageOrigin(): string | null {
  try {
    const origin: unknown = globalThis.location?.origin;
    return typeof origin === 'string' && /^https?:\/\/[^/?#\s]+$/u.test(origin) ? origin : null;
  } catch {
    return null;
  }
}
