/**
 * Peer protocol 19 network options the monitor form edits, as pure rules mirroring the node's `checks.Validate` and its
 * record canonicalization (internal/checks/validate.go, dns.go, dns_extended.go) without being stricter, so a stored
 * monitor always saves again unchanged: SRV, SOA and CAA expectations for DNS checks, WebSocket subprotocol offers, and
 * the monitor description (canary/validation.go: statusText with 8,192 bytes, line breaks allowed).
 */

const utf8 = (text: string): number => new TextEncoder().encode(text).length;

// ---------- DNS: SRV, SOA and CAA

/** Record types whose expectations the node canonicalizes as structured records (canonicalExtendedRecord). */
export const DNS_EXTENDED = ['SRV', 'SOA', 'CAA'] as const;
export type DnsExtended = (typeof DNS_EXTENDED)[number];
export const isExtendedRecord = (record: string): record is DnsExtended => (DNS_EXTENDED as readonly string[]).includes(record);

/** SOA and CAA are asked of one resolver directly, so the node requires an explicit IP:port; SRV may use the system resolver. */
export const dnsNeedsServer = (record: string): boolean => record === 'SOA' || record === 'CAA';

/** The node's limits on expectations: at most 128 of them, 8,192 bytes each and 64 KiB together. */
export const DNS_EXPECTED_MAX = 128;
export const DNS_EXPECTED_BYTES = 8192;
export const DNS_EXPECTED_TOTAL = 64 << 10;

/** What each extended expectation looks like, for hints and placeholders. */
export const DNS_RECORD_SHAPE: Record<DnsExtended, { example: string; fields: string }> = {
  SRV: { example: '10 5 5060 sip.example.com', fields: 'priority weight port target' },
  SOA: { example: 'ns1.example.com hostmaster.example.com 2026102401 7200 3600 1209600 300', fields: 'primary mailbox serial refresh retry expire minimum' },
  CAA: { example: '0 issue "letsencrypt.org"', fields: 'flags tag "value"' },
};

/** Go's strings.Fields: runs of Unicode White_Space separate fields. */
const fields = (text: string): string[] => text.split(/\p{White_Space}+/u).filter(Boolean);
/** Go's strings.TrimSpace: Unicode White_Space at both ends. */
const goTrim = (text: string): string => text.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, '');
/** Go's strconv.ParseUint(text, 10, bits): ASCII digits only (no sign or underscore), at most the bit size. */
function parseUint(text: string, max: number): number | null {
  if (!/^[0-9]+$/u.test(text)) return null;
  const value = Number(text.replace(/^0+(?=\d)/u, ''));
  return Number.isSafeInteger(value) && value <= max ? value : null;
}

/** The node's validDNSName: an optional trailing dot, 1–253 bytes, labels of 1–63 ASCII letters, digits, hyphens and underscores without an edge hyphen. */
export function validDnsName(text: string): boolean {
  const name = text.endsWith('.') ? text.slice(0, -1) : text;
  if (!name || name.length > 253 || /[^\x00-\x7f]/u.test(name)) return false;
  return name.split('.').every(label => label.length >= 1 && label.length <= 63 && !label.startsWith('-') && !label.endsWith('-') && /^[A-Za-z0-9_-]+$/u.test(label));
}
/** A name field of an SRV or SOA record: the root "." or a DNS name, compared lowercase without its trailing dot. */
const recordName = (text: string): string | null => text === '.' ? '.' : validDnsName(text) ? text.replace(/\.$/u, '').toLowerCase() : null;

/**
 * The node's canonical form of an SRV, SOA or CAA expectation, or null where it refuses one: SRV is four fields (three
 * 16-bit numbers and a target), SOA seven (two names and five 32-bit numbers), CAA a flag byte, a 1–15 character ASCII
 * alphanumeric tag (compared lowercase) and the rest as the value, either a JSON string literal or unquoted text, at most
 * 8,192 bytes.
 */
export function canonicalExtended(kind: DnsExtended, input: string): string | null {
  const parts = fields(input);
  if (kind === 'SRV') {
    if (parts.length !== 4) return null;
    const numbers = parts.slice(0, 3).map(part => parseUint(part, 0xffff));
    const target = recordName(parts[3]!);
    return numbers.some(n => n === null) || target === null ? null : [...numbers, target].join(' ');
  }
  if (kind === 'SOA') {
    if (parts.length !== 7) return null;
    const names = parts.slice(0, 2).map(recordName);
    const numbers = parts.slice(2).map(part => parseUint(part, 0xffff_ffff));
    return names.some(n => n === null) || numbers.some(n => n === null) ? null : [...names, ...numbers].join(' ');
  }
  if (parts.length < 3) return null;
  const flags = parseUint(parts[0]!, 0xff);
  const tag = parts[1]!;
  if (flags === null || tag.length < 1 || tag.length > 15 || !/^[A-Za-z0-9]+$/u.test(tag)) return null;
  // The value is what follows the flags and the tag, as Go's TrimPrefix/TrimSpace chain leaves it.
  let value = goTrim(goTrim(goTrim(input).slice(parts[0]!.length)).slice(tag.length));
  if (value.startsWith('"')) {
    try {
      const parsed: unknown = JSON.parse(value);
      if (typeof parsed !== 'string') return null;
      value = parsed;
    } catch { return null; }
  }
  if (utf8(value) > DNS_EXPECTED_BYTES) return null;
  return `${flags} ${tag.toLowerCase()} ${JSON.stringify(value)}`;
}

/** Why the node would refuse one extended expectation, in words, or undefined when it accepts it. */
export function extendedRecordProblem(kind: DnsExtended, value: string): string | undefined {
  if (canonicalExtended(kind, value) !== null) return undefined;
  const shape = DNS_RECORD_SHAPE[kind];
  const parts = fields(value);
  if (kind === 'SRV') {
    if (parts.length !== 4) return `“${value}” isn’t an SRV record. Write the four fields ${shape.fields}, like ${shape.example}.`;
    if (parts.slice(0, 3).some(part => parseUint(part, 0xffff) === null)) return `In “${value}”, priority, weight and port are whole numbers from 0 to 65,535.`;
    return `In “${value}”, the target is a host name like sip.example.com, or “.” for no service.`;
  }
  if (kind === 'SOA') {
    if (parts.length !== 7) return `“${value}” isn’t an SOA record. Write the seven fields ${shape.fields}, like ${shape.example}.`;
    if (parts.slice(0, 2).some(part => recordName(part) === null)) return `In “${value}”, the primary server and mailbox are host names, like ns1.example.com hostmaster.example.com.`;
    return `In “${value}”, serial, refresh, retry, expire and minimum are whole numbers from 0 to 4,294,967,295.`;
  }
  if (parts.length < 3) return `“${value}” isn’t a CAA record. Write ${shape.fields}, like ${shape.example}.`;
  if (parseUint(parts[0]!, 0xff) === null) return `In “${value}”, the flags are a whole number from 0 to 255, usually 0.`;
  if (parts[1]!.length > 15 || !/^[A-Za-z0-9]+$/u.test(parts[1]!)) return `In “${value}”, the tag is 1 to 15 ASCII letters or digits, like issue, issuewild or iodef.`;
  return `In “${value}”, write the value as a quoted JSON string like "letsencrypt.org", or unquoted text, of at most 8,192 bytes.`;
}

// ---------- WebSocket subprotocols

export const WS_PROTOCOLS_MAX = 16;
export const WS_PROTOCOL_BYTES = 128;
/** An HTTP token, as the node's validToken: ASCII letters, digits and !#$%&'*+-.^_`|~. */
const HTTP_TOKEN = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/u;

/** The offers as typed, split on commas and white space, in order. */
export const parseSubprotocols = (text: string): string[] => text.split(/[\s,]+/u).filter(Boolean);
/** The stored offers back into the field's text. */
export const subprotocolsText = (list: readonly string[] | undefined): string => (list ?? []).join(', ');

/**
 * Why the node would refuse these offers, or undefined: at most 16 distinct HTTP tokens (compared case-sensitively) of at
 * most 128 bytes each; requiring a negotiated subprotocol needs at least one offer.
 */
export function subprotocolProblem(list: readonly string[], required: boolean): string | undefined {
  if (required && !list.length) return 'List the subprotocols to offer, or turn off “Require one of them”.';
  if (list.length > WS_PROTOCOLS_MAX) return `Offer at most ${WS_PROTOCOLS_MAX} subprotocols.`;
  const seen = new Set<string>();
  for (const protocol of list) {
    if (!HTTP_TOKEN.test(protocol)) return `“${protocol}” isn’t a valid subprotocol name: use letters, digits and ! # $ % & ' * + - . ^ _ \` | ~, without spaces, quotes or commas.`;
    if (protocol.length > WS_PROTOCOL_BYTES) return `“${protocol.slice(0, 24)}…” is longer than ${WS_PROTOCOL_BYTES} characters.`;
    if (seen.has(protocol)) return `“${protocol}” is listed twice.`;
    seen.add(protocol);
  }
  return undefined;
}

// ---------- monitor description

export const DESCRIPTION_MAX = 8192;

/**
 * Why the node would refuse a monitor description, or undefined: valid text of at most 8,192 UTF-8 bytes with line
 * breaks but no other control characters (no tab, carriage return or DEL). An unpaired surrogate is refused here because
 * the node would store a replacement character instead of what was typed.
 */
export function descriptionProblem(text: string): string | undefined {
  if (/\p{Cs}/u.test(text)) return 'The description holds a broken character (an unpaired surrogate). Type it again instead of pasting it.';
  if (/[\x00-\x09\x0b-\x1f\x7f]/u.test(text)) return 'Remove tabs and other control characters. Line breaks are fine.';
  if (utf8(text) > DESCRIPTION_MAX) return `Use at most 8,192 bytes (${utf8(text).toLocaleString('en-US')} now). Characters outside ASCII take 2 to 4 bytes each.`;
  return undefined;
}
