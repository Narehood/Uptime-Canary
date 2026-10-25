import type { ReactNode } from 'react';
import type { Monitor } from '@uptime-canary/shared';

/**
 * Tailscale peer checks (peer protocol 19). Each assigned node runs the Tailscale client installed on its own machine,
 * at a fixed path, for one `tailscale ping` to the target: Tailscale's own peer ping over the tailnet, not ICMP. The
 * target is the whole configuration, with no options object, path, command, socket or credential. The node validates
 * the target; these rules mirror its `tailscalemonitor.Validate` without being stricter, so a stored monitor can always
 * be saved again unchanged. Pure apart from the facts' JSX.
 */

/** The node's limit for the whole target, in bytes. Every character a name allows is ASCII. */
export const TAILSCALE_NAME_MAX = 253;
/** The only client paths a node runs. None can be configured. */
export const TAILSCALE_LINUX_PATHS = ['/usr/bin/tailscale', '/usr/local/bin/tailscale'] as const;
export const TAILSCALE_WINDOWS_PATH = 'Program Files\\Tailscale\\tailscale.exe';
/** The node's fixed result messages. Names, addresses, the path and the client's own output never leave the node. */
export const TAILSCALE_SUMMARY = { up: 'Tailscale peer replied', down: 'Tailscale peer did not reply', pending: 'Tailscale peer status unavailable' } as const;

const LABEL = /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/u;
const utf8Bytes = (text: string): number => new TextEncoder().encode(text).length;
const digit = (code: number): boolean => code >= 48 && code <= 57;

/** Go's net/netip IPv4 rule: exactly four decimal fields of 0–255, none empty and none with a leading zero. Shared with browser.tsx. */
export function parseIPv4(text: string): number[] | null {
  const fields: number[] = [];
  let value = 0, digits = 0;
  for (let i = 0; i < text.length; i++) {
    const code = text.charCodeAt(i);
    if (digit(code)) {
      if (digits === 1 && value === 0) return null;
      value = value * 10 + code - 48;
      digits++;
      if (value > 255) return null;
    } else if (code === 46) {
      if (i === 0 || i === text.length - 1 || text.charCodeAt(i - 1) === 46 || fields.length === 3) return null;
      fields.push(value);
      value = 0;
      digits = 0;
    } else return null;
  }
  if (fields.length < 3) return null;
  fields.push(value);
  return fields;
}

const hexValue = (code: number): number => digit(code) ? code - 48 : code >= 97 && code <= 102 ? code - 87 : code >= 65 && code <= 70 ? code - 55 : -1;

/**
 * Go's net/netip IPv6 rule, for text without a zone: groups of one to four hex digits, at most one `::` that stands for
 * at least one zero group, and an optional dotted IPv4 tail in the last 32 bits. Shared with browser.tsx.
 */
export function parseIPv6(input: string): number[] | null {
  const ip = new Array<number>(16).fill(0);
  let text = input;
  let ellipsis = -1;
  if (text.startsWith('::')) {
    ellipsis = 0;
    text = text.slice(2);
    if (!text) return ip;
  }
  let i = 0;
  while (i < 16) {
    let off = 0, acc = 0;
    for (; off < text.length; off++) {
      const value = hexValue(text.charCodeAt(off));
      if (value < 0) break;
      acc = acc * 16 + value;
      if (off > 3 || acc > 0xffff) return null;
    }
    if (off === 0) return null;
    if (off < text.length && text[off] === '.') {
      if (ellipsis < 0 && i !== 12) return null;
      if (i + 4 > 16) return null;
      const tail = parseIPv4(text);
      if (!tail) return null;
      ip.splice(i, 4, ...tail);
      text = '';
      i += 4;
      break;
    }
    ip[i] = acc >> 8;
    ip[i + 1] = acc & 0xff;
    i += 2;
    text = text.slice(off);
    if (!text) break;
    if (text[0] !== ':' || text.length === 1) return null;
    text = text.slice(1);
    if (text[0] === ':') {
      if (ellipsis >= 0) return null;
      ellipsis = i;
      text = text.slice(1);
      if (!text) break;
    }
  }
  if (text) return null;
  if (i < 16) {
    if (ellipsis < 0) return null;
    const n = 16 - i;
    for (let j = i - 1; j >= ellipsis; j--) ip[j + n] = ip[j]!;
    ip.fill(0, ellipsis, ellipsis + n);
  } else if (ellipsis >= 0) return null;
  return ip;
}

interface Address { v6: boolean; bytes: number[] }

/**
 * Go's `netip.ParseAddr`: the first dot, colon or percent sign picks the parser. Any zone is refused here, as the node
 * refuses a zoned address after parsing it, and a percent sign is never part of a name either.
 */
function parseAddress(text: string): Address | null {
  for (const char of text) {
    if (char === '%') return null;
    if (char === '.') { const bytes = parseIPv4(text); return bytes ? { v6: false, bytes } : null; }
    if (char === ':') { if (text.includes('%')) return null; const bytes = parseIPv6(text); return bytes ? { v6: true, bytes } : null; }
  }
  return null;
}

/** `netip.Addr.IsUnspecified`: 0.0.0.0 or ::, but not ::ffff:0.0.0.0. */
const unspecified = (a: Address): boolean => a.bytes.every(byte => byte === 0);
/** `netip.Addr.IsMulticast`: 224.0.0.0/4, ff00::/8, and an IPv4-mapped IPv6 address in 224.0.0.0/4. */
function multicast(a: Address): boolean {
  if (!a.v6) return (a.bytes[0]! & 0xf0) === 0xe0;
  const mapped = a.bytes.slice(0, 10).every(byte => byte === 0) && a.bytes[10] === 0xff && a.bytes[11] === 0xff;
  return mapped ? (a.bytes[12]! & 0xf0) === 0xe0 : a.bytes[0] === 0xff;
}

/**
 * Exactly the node's admission rule for a target: 1–253 bytes; either one literal IPv4 or IPv6 address without brackets,
 * port or zone that isn't unspecified or multicast, or an ASCII DNS or MagicDNS name of dot-separated labels, each 1–63
 * letters, digits and hyphens that neither start nor end with a hyphen, with at most one trailing dot.
 */
export function tailscaleTargetAccepted(target: string): boolean {
  if (!target || utf8Bytes(target) > TAILSCALE_NAME_MAX) return false;
  const address = parseAddress(target);
  if (address) return !unspecified(address) && !multicast(address);
  return (target.endsWith('.') ? target.slice(0, -1) : target).split('.').every(label => LABEL.test(label));
}

/**
 * Why the node would refuse a target, or undefined when it accepts it. Acceptance is `tailscaleTargetAccepted` alone;
 * the message names the most specific problem found.
 */
export function tailscaleTargetProblem(target: string): string | undefined {
  if (tailscaleTargetAccepted(target)) return undefined;
  if (!target) return 'Enter the peer’s MagicDNS name or Tailscale address, like build-server or 100.101.102.103.';
  if (/[\s\x00-\x1f\x7f]/u.test(target)) return 'Enter one name or address, without spaces or line breaks.';
  if (/^[A-Za-z][A-Za-z0-9+.-]*:\/\//u.test(target) || target.includes('/') || target.includes('\\')) return 'Enter only the peer’s name or address, not a URL or a path.';
  if (target.startsWith('-')) return 'Enter a peer’s name or address, not a command-line option.';
  if (target.includes('%')) return 'Remove the %zone and enter the address alone.';
  if (target.includes('[') || target.includes(']')) return 'Remove the brackets and enter the IPv6 address alone, without a port.';
  if (target.includes('@')) return 'Enter the peer alone, without a user name or @.';
  const address = parseAddress(target);
  if (address && unspecified(address)) return '0.0.0.0 and :: aren’t a peer. Enter the peer’s own address.';
  if (address && multicast(address)) return 'A multicast address isn’t a peer. Enter the peer’s own address.';
  if (target.includes(':')) {
    return /^[^:]+:\d+$/u.test(target)
      ? 'Remove the port: Tailscale pings the peer itself, not a port on it.'
      : 'That isn’t a valid IPv6 address. Enter it without brackets, a port or a zone, like fd7a:115c:a1e0::1.';
  }
  if (utf8Bytes(target) > TAILSCALE_NAME_MAX) return `Use at most ${TAILSCALE_NAME_MAX} characters.`;
  if (/[^\x00-\x7f]/u.test(target)) return 'Use only ASCII letters, digits, hyphens and dots. For an international name, enter its xn-- form.';
  const bad = /[^A-Za-z0-9.-]/u.exec(target);
  if (bad) return `“${bad[0]}” isn’t allowed. Use letters, digits, hyphens and dots, like build-server or build-server.example-tailnet.ts.net.`;
  const labels = (target.endsWith('.') ? target.slice(0, -1) : target).split('.');
  if (labels.some(label => !label)) return 'Remove the empty part between two dots, or the extra dot at the end.';
  if (labels.some(label => label.length > 63)) return 'Each dot-separated part of the name is at most 63 characters.';
  if (labels.some(label => label.startsWith('-') || label.endsWith('-'))) return 'A part of the name can’t start or end with a hyphen.';
  return 'Enter a MagicDNS name, or an IPv4 or IPv6 address.';
}

export interface TailscaleFact { label: string; value: ReactNode }

/** The pending causes, shared by the form note, the facts and the detail page. */
export const TAILSCALE_PENDING = 'no Tailscale client at the fixed path, or an operating system other than Linux and Windows; a daemon that isn’t running, isn’t signed in or that Canary’s account may not use; a name or address that isn’t a peer, or is this node itself; client output the node doesn’t recognize; no free check worker; a cancelled check; or no answer within the timeout';

/** What a Tailscale check asks and how its results read. The target is a peer name, not a secret, so viewers see it too. */
export function tailscaleFacts(m: Monitor): TailscaleFact[] {
  return [
    {
      label: 'Asked through',
      value: <>Each assigned node’s own Tailscale client, never another machine’s: one <span className="mono">tailscale ping</span> to <span className="mono wrap">{m.target}</span>, Tailscale’s peer ping over the tailnet rather than ICMP. The client is the one installed at {TAILSCALE_LINUX_PATHS.map((path, index) => <span key={path}>{index ? ' or ' : ''}<span className="mono">{path}</span></span>)} on Linux and <span className="mono">{TAILSCALE_WINDOWS_PATH}</span> on Windows. No settings or credentials; nothing is changed.</>,
    },
    { label: 'Up when', value: 'Exactly one valid pong comes back: direct, through a DERP relay or through a peer relay' },
    { label: 'Down when', value: 'The client confirms its one ping timed out with no reply' },
    { label: 'Pending when', value: `The node gets no definite answer: ${TAILSCALE_PENDING}. Pending is never inverted and never recovers an outage.` },
    { label: 'Latency', value: 'The round trip the client reports for the pong; a dash for every other result' },
    { label: 'Results', value: <>Only “{TAILSCALE_SUMMARY.up}”, “{TAILSCALE_SUMMARY.down}” or “{TAILSCALE_SUMMARY.pending}”. The path, the peer’s name and addresses in the reply and the client’s own messages stay on the node.</> },
  ];
}
