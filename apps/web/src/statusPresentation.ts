import type { StatusPage, StatusPageAppearance } from '@uptime-canary/shared';
import { goParseUrl } from './browser';

/**
 * Status-page presentation (peer protocol 19): the operator's theme, accent color, logo, footer, refresh interval and
 * Markdown choice, the page's public address and its opt-in RSS feed. The rules mirror the node's
 * validateStatusPresentation (canary/status_presentation.go) without being stricter, so a stored page always saves again
 * unchanged; the reader below re-checks what a node publishes before anything is rendered or loaded. These are public,
 * operator-written values: never CSS, HTML or script, only typed settings.
 */

export const THEMES = ['', 'system', 'light', 'dark'] as const;
export type PageTheme = (typeof THEMES)[number];
export const REFRESH_DEFAULT_MS = 30_000;
export const REFRESH_MIN_MS = 10_000;
export const REFRESH_MAX_MS = 300_000;
/** Refresh choices the editor offers; a stored interval outside them is kept as stored. Empty is the 30 s default. */
export const REFRESH_CHOICES = [10_000, 15_000, 60_000, 120_000, 300_000] as const;
export const LINK_MAX = 2048;
export const FOOTER_MAX = 2048;
const ACCENT = /^#[0-9a-fA-F]{6}$/u;

type Obj = Record<string, unknown>;
const isObj = (value: unknown): value is Obj => typeof value === 'object' && value !== null && !Array.isArray(value);
const utf8 = (text: string): number => new TextEncoder().encode(text).length;
/** Go's strings.TrimSpace(text) != "": something besides Unicode White_Space. */
const nonBlank = (text: string): boolean => /[^\p{White_Space}]/u.test(text);
/** The node's statusText control rule: no byte below 32 (a line break only where allowed) and no DEL. */
const controls = (text: string, multiline: boolean): boolean => (multiline ? /[\x00-\x09\x0b-\x1f\x7f]/u : /[\x00-\x1f\x7f]/u).test(text);

// ---------- the node's rules

/**
 * The page's public address, or why the node refuses it: optional; otherwise valid text of at most 2,048 bytes, a
 * complete http:// or https:// URL with a host, no user:password@, no backslash, tab or line break. A query (a
 * dashboard's registration selector) and a #fragment (a hash route) are allowed. The node never fetches it.
 */
export function publicUrlProblem(url: string): string | undefined {
  if (!url) return undefined;
  if (/\p{Cs}/u.test(url)) return 'The address holds a broken character (an unpaired surrogate). Type it again instead of pasting it.';
  if (utf8(url) > LINK_MAX) return 'Use at most 2,048 bytes. Characters outside ASCII take 2 to 4 bytes each.';
  if (controls(url, false) || url.includes('\\')) return 'Remove backslashes, tabs, line breaks and other control characters.';
  if (!nonBlank(url)) return 'Enter the address visitors open, or leave it empty.';
  const parsed = goParseUrl(url);
  if (!parsed || (parsed.scheme !== 'http' && parsed.scheme !== 'https')) return 'Enter the complete address visitors open, starting with https:// or http://, like https://status.example.com/.';
  if (parsed.opaque || !parsed.hostname) return 'Write a complete address with // and a host, like https://status.example.com/.';
  if (parsed.user) return 'Remove user:password@ from the address: it’s public.';
  return undefined;
}

/**
 * The logo's address, or why the node refuses it: optional; otherwise an https:// URL with a host, without
 * user:password@, query or #fragment text, a backslash or control characters, of at most 2,048 bytes.
 */
export function logoUrlProblem(url: string): string | undefined {
  if (!url) return undefined;
  if (/\p{Cs}/u.test(url)) return 'The address holds a broken character (an unpaired surrogate). Type it again instead of pasting it.';
  if (utf8(url) > LINK_MAX) return 'Use at most 2,048 bytes. Characters outside ASCII take 2 to 4 bytes each.';
  if (controls(url, false) || url.includes('\\')) return 'Remove backslashes, tabs, line breaks and other control characters.';
  if (!nonBlank(url)) return 'Enter the image’s https:// address, or leave it empty.';
  const parsed = goParseUrl(url);
  if (!parsed || parsed.scheme !== 'https') return 'Use an https:// image address, like https://www.example.com/logo.png. Plain http://, data: and other schemes aren’t allowed.';
  if (parsed.opaque || !parsed.hostname) return 'Write a complete address with // and a host, like https://www.example.com/logo.png.';
  if (parsed.user) return 'Remove user:password@ from the address: visitors’ browsers load it.';
  if (parsed.rawQuery || parsed.forceQuery || parsed.fragment) return 'Remove the ?query and #fragment: a logo address can’t carry either.';
  return undefined;
}

export function accentProblem(color: string): string | undefined {
  return !color || ACCENT.test(color) ? undefined : 'Use a hex color like #2a78d6: # and six digits 0–9 or letters a–f.';
}

export function footerProblem(text: string): string | undefined {
  if (/\p{Cs}/u.test(text)) return 'The footer holds a broken character (an unpaired surrogate). Type it again instead of pasting it.';
  if (controls(text, true)) return 'Remove tabs and other control characters. Line breaks are fine.';
  if (utf8(text) > FOOTER_MAX) return 'Use at most 2,048 bytes. Characters outside ASCII take 2 to 4 bytes each.';
  return undefined;
}

export const refreshProblem = (ms: number): string | undefined =>
  ms === 0 || (Number.isInteger(ms) && ms >= REFRESH_MIN_MS && ms <= REFRESH_MAX_MS) ? undefined : 'Refresh every 10 seconds to 5 minutes, or use the 30-second default.';

// ---------- the editor's draft

export interface PresentationDraft {
  theme: PageTheme | string;
  accent: string;
  logo: string;
  footer: string;
  /** Milliseconds, 0 for the default. */
  refreshMs: number;
  markdown: boolean;
  publicUrl: string;
  rss: boolean;
}
export type PresentationField = 'theme' | 'accent' | 'logo' | 'footer' | 'refresh' | 'publicUrl' | 'rss';

const str = (value: unknown): string => typeof value === 'string' ? value : '';

export function presentationDraft(page: StatusPage | undefined): PresentationDraft {
  const a: StatusPageAppearance = isObj(page?.appearance) ? page!.appearance! : {};
  const refresh = (a as Obj).refreshIntervalMs;
  return {
    theme: str(a.theme), accent: str(a.accentColor), logo: str(a.logoUrl), footer: str(a.footerText),
    refreshMs: typeof refresh === 'number' && Number.isFinite(refresh) ? refresh : 0, markdown: a.markdown === true,
    publicUrl: str(page?.publicUrl), rss: page?.rssEnabled === true,
  };
}

export interface BuiltPresentation { appearance?: StatusPageAppearance; publicUrl?: string; rssEnabled?: true }

/**
 * The presentation fields of a page input, on top of the stored appearance so keys from a newer node survive. Unedited
 * stored values go back byte for byte; typed links and colors are trimmed. Go's omitempty fields are omitted when empty,
 * 0 or false, and an appearance with nothing set is omitted unless the stored page had one. A refused value is still what
 * would be sent, so the node refuses it too.
 */
export function buildPresentation(stored: StatusPage | undefined, d: PresentationDraft, errors: Partial<Record<PresentationField, string>>): BuiltPresentation {
  const before = presentationDraft(stored);
  const keep = (value: string, was: string): string => value === was ? value : value.trim();
  const a: Obj = isObj(stored?.appearance) ? structuredClone(stored!.appearance) as Obj : {};
  const put = (key: string, value: unknown): void => { if (value === '' || value === 0 || value === false || value === undefined) delete a[key]; else a[key] = value; };

  if (!(THEMES as readonly string[]).includes(d.theme)) errors.theme = 'Choose a theme.';
  put('theme', d.theme);
  const accent = keep(d.accent, before.accent);
  const accentError = accentProblem(accent);
  if (accentError) errors.accent = accentError;
  put('accentColor', accent);
  const logo = keep(d.logo, before.logo);
  const logoError = logoUrlProblem(logo);
  if (logoError) errors.logo = logoError;
  put('logoUrl', logo);
  const footerError = footerProblem(d.footer);
  if (footerError) errors.footer = footerError;
  put('footerText', d.footer);
  const refreshError = refreshProblem(d.refreshMs);
  if (refreshError) errors.refresh = refreshError;
  put('refreshIntervalMs', d.refreshMs);
  put('markdown', d.markdown);

  const out: BuiltPresentation = {};
  if (Object.keys(a).length || isObj(stored?.appearance)) out.appearance = a as StatusPageAppearance;
  const publicUrl = keep(d.publicUrl, before.publicUrl);
  const urlError = publicUrlProblem(publicUrl);
  if (urlError) errors.publicUrl = urlError;
  if (publicUrl) out.publicUrl = publicUrl;
  if (d.rss) {
    out.rssEnabled = true;
    if (!publicUrl) errors.rss = 'An RSS feed needs the page’s public address above, for its links.';
  }
  return out;
}

// ---------- what a node publishes, read strictly

export interface ShownPresentation {
  /** '' when the operator set none, or the node sent one this dashboard doesn't know. */
  theme: 'light' | 'dark' | 'system' | '';
  accent: string;
  /** Only an address that passes the node's own logo rule; anything else is never loaded. */
  logo: string;
  footer: string;
  refreshMs: number;
  markdown: boolean;
  publicUrl: string;
  rss: boolean;
}

/**
 * The presentation of a published page from the network: each value is kept only when it passes the node's own rule,
 * so a bad value from a node drops that one setting (an unknown theme or a refused logo or color is ignored, a refresh
 * interval out of range is the default), never the page. Text is capped like the page's other strings.
 */
export function readPresentation(page: unknown): ShownPresentation {
  const p: Obj = isObj(page) ? page : {};
  const a: Obj = isObj(p.appearance) ? p.appearance : {};
  const rawTheme = a.theme, rawAccent = a.accentColor, rawLogo = a.logoUrl, rawFooter = a.footerText, refresh = a.refreshIntervalMs, rawUrl = p.publicUrl;
  const theme: ShownPresentation['theme'] = rawTheme === 'light' || rawTheme === 'dark' || rawTheme === 'system' ? rawTheme : '';
  const accent = typeof rawAccent === 'string' && ACCENT.test(rawAccent) ? rawAccent : '';
  const logo = typeof rawLogo === 'string' && rawLogo && !logoUrlProblem(rawLogo) ? rawLogo : '';
  const footer = typeof rawFooter === 'string' ? rawFooter.slice(0, 8192) : '';
  const refreshMs = typeof refresh === 'number' && Number.isInteger(refresh) && refresh >= REFRESH_MIN_MS && refresh <= REFRESH_MAX_MS ? refresh : REFRESH_DEFAULT_MS;
  const publicUrl = typeof rawUrl === 'string' && rawUrl && !publicUrlProblem(rawUrl) ? rawUrl : '';
  return { theme, accent, logo, footer, refreshMs, markdown: a.markdown === true, publicUrl, rss: p.rssEnabled === true };
}

// ---------- accent color as CSS custom properties

const channel = (hex: string, at: number): number => parseInt(hex.slice(at, at + 2), 16);
function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5].map(at => { const c = channel(hex, at) / 255; return c <= 0.040_45 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4; });
  return 0.2126 * r! + 0.7152 * g! + 0.0722 * b!;
}
/** WCAG 2 contrast ratio between two #rrggbb colors. */
export function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi! + 0.05) / (lo! + 0.05);
}
/** The page surfaces the accent is read against, from styles.css. */
export const SURFACE = { light: '#fcfcfb', dark: '#1a1a19' } as const;

/**
 * The accent as the page's own custom properties, set through React's style object from a checked #rrggbb value only.
 * It always colors marks and outlines; it colors link text only where it keeps 4.5:1 against the page surface, and
 * button fills only where white text keeps 4.5:1 on it, so a pale or dark accent never makes text unreadable.
 */
export function accentVariables(accent: string, mode: 'light' | 'dark'): Record<string, string> {
  if (!ACCENT.test(accent)) return {};
  const hex = accent.toLowerCase();
  const rgb = [1, 3, 5].map(at => channel(hex, at)).join(', ');
  const out: Record<string, string> = { '--accent': hex, '--accent-wash': `rgba(${rgb}, ${mode === 'dark' ? '.14' : '.10'})`, '--page-accent': hex };
  if (contrast(hex, SURFACE[mode]) >= 4.5) out['--accent-text'] = hex;
  if (contrast(hex, '#ffffff') >= 4.5) out['--accent-btn'] = hex;
  return out;
}
