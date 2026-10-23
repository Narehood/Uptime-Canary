import { normalizeApiUrl } from './api';

/**
 * Public SVG badges (node route `/public/badge/<slug>.svg`). A badge reads the same public projection as the status
 * page: the page's overall state, or one service picked by its public section ID and public name. No credentials, private
 * monitor IDs or names are ever part of the address; the node answers "not found" for anything else.
 */

export const BADGE_PREFIX = '/public/badge/';
const SLUG = /^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$/u;

export interface BadgeSelection { group: string; monitor: string }

/** The image address on one node. Throws for an address that isn't a plain node origin or a slug the node can't serve. */
export function badgeUrl(nodeUrl: string, slug: string, selection?: BadgeSelection): string {
  if (!SLUG.test(slug)) throw new Error('This page address can’t have a badge.');
  const base = normalizeApiUrl(nodeUrl);
  // Exactly these two parameters, in this order: the node rejects any other key, repeat or empty value.
  const query = selection ? `?${new URLSearchParams([['group', selection.group], ['monitor', selection.monitor]])}` : '';
  return `${base}${BADGE_PREFIX}${slug}.svg${query}`;
}

export interface BadgeTarget extends BadgeSelection {
  sectionName: string;
  /** How many services in the page share this section ID and public name; a badge needs exactly one. */
  matches: number;
}

/** Every listed service with its selection; mirrors the node, which counts matches across sections with the same ID. */
export function badgeTargets(groups: { id: string; name: string; monitors: { name: string }[] }[]): BadgeTarget[] {
  const counts = new Map<string, number>();
  const keyOf = (group: string, monitor: string): string => JSON.stringify([group, monitor]);
  for (const group of groups) for (const monitor of group.monitors) counts.set(keyOf(group.id, monitor.name), (counts.get(keyOf(group.id, monitor.name)) ?? 0) + 1);
  return groups.flatMap(group => group.monitors.map(monitor => ({
    group: group.id, monitor: monitor.name, sectionName: group.name, matches: group.id && monitor.name ? counts.get(keyOf(group.id, monitor.name))! : 0,
  })));
}

/** A one-line reason a badge address is a poor choice for embedding elsewhere, or undefined. */
export function addressCaveat(nodeUrl: string): string | undefined {
  let url: URL;
  try { url = new URL(nodeUrl); } catch { return 'This isn’t a valid node address.'; }
  const host = url.hostname.replace(/^\[|\]$/gu, '').toLowerCase();
  if (host === 'localhost' || host.endsWith('.localhost') || /^127\./u.test(host) || host === '::1') {
    return 'This is a loopback address: the image only loads on the node’s own computer. Pick a node address others can reach.';
  }
  if (/^10\./u.test(host) || /^192\.168\./u.test(host) || /^172\.(1[6-9]|2\d|3[01])\./u.test(host) || /^169\.254\./u.test(host) || /^f[cd]/u.test(host) || /^fe80:/u.test(host) || host.endsWith('.local') || !host.includes('.') && !host.includes(':')) {
    return 'This looks like a private network address: the image loads only inside that network, and sites that fetch images for you (such as GitHub) can’t reach it.';
  }
  if (url.protocol === 'http:') return 'Plain HTTP: pages served over HTTPS may block the image, and sites that fetch images for you may refuse it. Prefer an HTTPS address.';
  return undefined;
}

const mdText = (text: string): string => text.replace(/[\\[\]]/gu, match => `\\${match}`);
// encodeURIComponent leaves parentheses alone, and they would end a Markdown link early.
const mdUrl = (url: string): string => url.replace(/[()\s]/gu, match => match === '(' ? '%28' : match === ')' ? '%29' : encodeURIComponent(match));
const attr = (text: string): string => text.replace(/[&"<>]/gu, match => ({ '&': '&amp;', '"': '&quot;', '<': '&lt;', '>': '&gt;' })[match]!);

/** Markdown image linking to the public page, safe for any alt text. */
export const badgeMarkdown = (image: string, alt: string, link: string): string => `[![${mdText(alt)}](${mdUrl(image)})](${mdUrl(link)})`;
export const badgeHtml = (image: string, alt: string, link: string): string => `<a href="${attr(link)}"><img src="${attr(image)}" alt="${attr(alt)}" height="20"></a>`;
