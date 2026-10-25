import type { ReactNode } from 'react';

/**
 * A small, bounded Markdown subset for text an operator opts into on a public status page (protocol 19): the page
 * description, its footer and announcement messages. It builds React elements only, so the browser receives text nodes
 * and a fixed set of tags; nothing is ever parsed as HTML or handed to the DOM as markup. Supported:
 * paragraphs with their line breaks, **strong** and *emphasis* (also with underscores, not inside words), `inline code`,
 * fenced code blocks, flat "-", "*" or "+" lists and numbered lists, backslash escapes, and links to absolute http:// or
 * https:// addresses, written [text](https://…) or <https://…>. Links open in a new tab with rel="noopener noreferrer
 * nofollow" and no referrer. Everything else stays literal text: raw HTML, scripts, images (![…](…)), headings, tables,
 * and links to any other scheme, a relative address or one with user:password@. Nothing is fetched or loaded.
 */

/** Text past this many characters is shown as plain text; the node's own limits are far below it. */
export const MARKDOWN_MAX_CHARS = 16_384;
/** At most this many formatted elements per text; past it the rest is plain text. */
const MAX_ELEMENTS = 2000;
/** How deeply emphasis may nest inside emphasis and link text. */
const MAX_DEPTH = 4;
const LINK_MAX = 2048;
const LABEL_MAX = 1000;

/** An absolute http:// or https:// address without credentials, as a normalized href, or null for anything else. */
export function safeHref(raw: string): string | null {
  if (!raw || raw.length > LINK_MAX || /[\s\x00-\x1f\x7f<>"'`\\]/u.test(raw) || !/^https?:\/\//iu.test(raw)) return null;
  let url: URL;
  try { url = new URL(raw); } catch { return null; }
  if ((url.protocol !== 'http:' && url.protocol !== 'https:') || url.username || url.password || !url.hostname) return null;
  return url.href;
}

interface Budget { left: number; key: number }

type Block = { kind: 'p'; lines: string[] } | { kind: 'ul' | 'ol'; items: string[]; start: number } | { kind: 'code'; text: string };

const FENCE = /^ {0,3}(`{3,}|~{3,})/u;
const BULLET = /^ {0,3}[-*+][ \t]+(\S.*)$/u;
const NUMBERED = /^ {0,3}(\d{1,9})[.)][ \t]+(\S.*)$/u;

function blocks(text: string): Block[] {
  const lines = text.replace(/\r\n?/gu, '\n').split('\n');
  const out: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i]!;
    const fence = FENCE.exec(line);
    if (fence) {
      const mark = fence[1]!;
      const body: string[] = [];
      i++;
      // A fence closes on a line of at least as many of the same character, and nothing else; else at the end.
      while (i < lines.length && !(lines[i]!.trim().length >= mark.length && lines[i]!.trim() === mark[0]!.repeat(lines[i]!.trim().length))) body.push(lines[i++]!);
      i++;
      out.push({ kind: 'code', text: body.join('\n') });
      continue;
    }
    if (!line.trim()) { i++; continue; }
    const numbered = NUMBERED.exec(line);
    if (numbered || BULLET.test(line)) {
      const pattern = numbered ? NUMBERED : BULLET;
      const items: string[] = [];
      while (i < lines.length) {
        const item = pattern.exec(lines[i]!);
        if (!item) break;
        items.push((numbered ? item[2] : item[1])!);
        i++;
      }
      out.push({ kind: numbered ? 'ol' : 'ul', items, start: numbered ? Number(numbered[1]) : 1 });
      continue;
    }
    const lines2: string[] = [];
    while (i < lines.length && lines[i]!.trim() && !FENCE.test(lines[i]!) && !BULLET.test(lines[i]!) && !NUMBERED.test(lines[i]!)) lines2.push(lines[i++]!);
    out.push({ kind: 'p', lines: lines2 });
  }
  return out;
}

const SPACE = /\s/u;
const WORD = /[\p{L}\p{N}]/u;
const PUNCTUATION = /^[!-/:-@[-`{-~]$/u;

/** Where the closing delimiter of emphasis opened at `from` is, or -1. Closers follow a non-space and aren't escaped. */
function closer(text: string, from: number, delim: string): number {
  let at = text.indexOf(delim, from);
  while (at >= 0) {
    const before = text[at - 1] ?? '';
    const after = text[at + delim.length] ?? '';
    const single = delim.length === 1 && (after === delim || before === delim);
    const underscore = delim[0] === '_' && WORD.test(after);
    if (at > from && !SPACE.test(before) && before !== '\\' && !single && !underscore) return at;
    at = text.indexOf(delim, at + 1);
  }
  return -1;
}

/** The end of a code span opened by a run of backticks at `at`: the next run of exactly the same length, or -1. */
function codeEnd(text: string, at: number, run: string): number {
  let close = text.indexOf(run, at + run.length);
  while (close >= 0) {
    if (text[close - 1] !== '`' && text[close + run.length] !== '`') return close;
    close = text.indexOf(run, close + 1);
  }
  return -1;
}

/** [label](address) at `at`: the label, the address and where it ends, or null. Addresses can't contain spaces. */
function linkAt(text: string, at: number): { label: string; url: string; end: number } | null {
  let depth = 0;
  let i = at;
  for (; i < text.length && i - at <= LABEL_MAX; i++) {
    const ch = text[i];
    if (ch === '\\') { i++; continue; }
    if (ch === '[') depth++;
    else if (ch === ']' && --depth === 0) break;
  }
  if (depth !== 0 || text[i] !== ']' || text[i + 1] !== '(') return null;
  const close = text.indexOf(')', i + 2);
  if (close < 0 || close - i > LINK_MAX + 2) return null;
  const url = text.slice(i + 2, close);
  if (!url || SPACE.test(url)) return null;
  return { label: text.slice(at + 1, i), url, end: close + 1 };
}

const link = (href: string, children: ReactNode, key: string): ReactNode =>
  <a key={key} href={href} target="_blank" rel="noopener noreferrer nofollow" referrerPolicy="no-referrer">{children}</a>;

function inline(text: string, depth: number, budget: Budget, links: boolean): ReactNode[] {
  const out: ReactNode[] = [];
  let buffer = '';
  const flush = (): void => { if (buffer) { out.push(buffer); buffer = ''; } };
  let i = 0;
  while (i < text.length) {
    if (budget.left <= 0) { buffer += text.slice(i); break; }
    const ch = text[i]!;
    const next = text[i + 1] ?? '';
    if (ch === '\\' && PUNCTUATION.test(next)) { buffer += next; i += 2; continue; }
    if (ch === '`') {
      const run = /^`+/u.exec(text.slice(i, i + 64))![0];
      const end = codeEnd(text, i, run);
      if (end < 0) { buffer += run; i += run.length; continue; }
      flush(); budget.left--;
      const body = text.slice(i + run.length, end);
      out.push(<code key={`k${budget.key++}`}>{/^ .* $/su.test(body) && body.trim() ? body.slice(1, -1) : body}</code>);
      i = end + run.length;
      continue;
    }
    // Images are never loaded: "![" stays literal, and so does what follows it.
    if (ch === '!' && next === '[') { buffer += '!['; i += 2; continue; }
    if (ch === '[' && links) {
      const found = linkAt(text, i);
      if (found) {
        const href = safeHref(found.url);
        if (href) {
          flush(); budget.left--;
          out.push(link(href, inline(found.label, depth + 1, budget, false), `k${budget.key++}`));
        } else buffer += text.slice(i, found.end);
        i = found.end;
        continue;
      }
    }
    if (ch === '<' && links) {
      const auto = /^<(https?:\/\/[^\s<>]{1,2048})>/iu.exec(text.slice(i, i + LINK_MAX + 16));
      const href = auto ? safeHref(auto[1]!) : null;
      if (auto && href) { flush(); budget.left--; out.push(link(href, auto[1], `k${budget.key++}`)); i += auto[0].length; continue; }
    }
    if ((ch === '*' || ch === '_') && depth < MAX_DEPTH) {
      const delim = next === ch ? ch + ch : ch;
      const after = text[i + delim.length] ?? '';
      const before = text[i - 1] ?? '';
      const opens = after !== '' && !SPACE.test(after) && after !== ch && !(ch === '_' && WORD.test(before));
      const end = opens ? closer(text, i + delim.length, delim) : -1;
      if (end > i + delim.length) {
        flush(); budget.left--;
        const children = inline(text.slice(i + delim.length, end), depth + 1, budget, links);
        out.push(delim.length === 2 ? <strong key={`k${budget.key++}`}>{children}</strong> : <em key={`k${budget.key++}`}>{children}</em>);
        i = end + delim.length;
        continue;
      }
      buffer += delim; i += delim.length;
      continue;
    }
    buffer += ch;
    i++;
  }
  flush();
  return out;
}

/** The Markdown subset as React elements. Text past MARKDOWN_MAX_CHARS is appended as plain text. */
export function markdownNodes(text: string): ReactNode[] {
  const budget: Budget = { left: MAX_ELEMENTS, key: 0 };
  const head = text.slice(0, MARKDOWN_MAX_CHARS);
  const nodes: ReactNode[] = blocks(head).map(block => {
    const key = `b${budget.key++}`;
    if (block.kind === 'code') return <pre key={key}><code>{block.text}</code></pre>;
    if (block.kind === 'p') {
      return <p key={key}>{block.lines.flatMap((line, index) => index ? [<br key={`br${budget.key++}`} />, ...inline(line, 0, budget, true)] : inline(line, 0, budget, true))}</p>;
    }
    const items = block.items.map(item => <li key={`li${budget.key++}`}>{inline(item, 0, budget, true)}</li>);
    return block.kind === 'ol' ? <ol key={key} start={block.start === 1 ? undefined : block.start}>{items}</ol> : <ul key={key}>{items}</ul>;
  });
  if (text.length > head.length) nodes.push(<p key="rest">{text.slice(head.length)}</p>);
  return nodes;
}

/**
 * Operator text on a public page: plain text with its line breaks by default (the `plain` element's class keeps them),
 * or the Markdown subset above when the page opts in.
 */
export function RichText({ text, markdown, className }: { text: string; markdown: boolean; className?: string }) {
  if (!markdown) return <p className={className}>{text}</p>;
  return <div className={`md${className ? ` ${className}` : ''}`}>{markdownNodes(text)}</div>;
}
