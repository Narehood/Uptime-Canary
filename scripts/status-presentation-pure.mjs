// Pure protocol-19 checks for status-page presentation: apps/web/src/statusPresentation.ts (a mirror of the node's
// validateStatusPresentation in canary/status_presentation.go, as read, and a strict reader of what a node publishes),
// the bounded Markdown subset in markdown.tsx (React elements only), the RSS feed address in api.ts, the theme default a
// public page may set in theme.tsx, and the status-page editor's model (views/StatusPages.tsx statusPageModel). Imports
// the source in place through one tsx namespace: no copy, scratch, network, browser, image or feed request, or output
// files; storage and fetch are stand-ins that record any use, and none is expected. Root's contract harness checks the
// node's feed and a real public page. Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/status-presentation-pure.mjs   (exit 1 on any failure)
import { readFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

process.env.TSX_DISABLE_CACHE = '1';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const touched = [];
const recorder = name => (...args) => { touched.push([name, ...args.map(String)]); };
for (const name of ['localStorage', 'sessionStorage']) {
  Object.defineProperty(globalThis, name, { value: { getItem: () => null, setItem: recorder(`${name}.setItem`), removeItem: recorder(`${name}.removeItem`), clear: recorder(`${name}.clear`) }, configurable: true, writable: true });
}
const requests = [];
globalThis.fetch = async (url, init = {}) => { requests.push({ url: String(url), init }); throw new Error('no network in this fixture'); };
globalThis.document ??= { addEventListener() {} };
const reactModule = await import('react');
const React = reactModule.default ?? reactModule;
globalThis.React ??= React;
const { register } = await import('tsx/esm/api');
const { renderToStaticMarkup } = await import('react-dom/server');
const scope = register({ namespace: 'status-presentation-pure', tsconfig: resolve(root, 'apps/web/tsconfig.json') });
const load = file => scope.import(pathToFileURL(resolve(root, 'apps/web/src', file)).href, import.meta.url);
const p = await load('statusPresentation.ts');
const md = await load('markdown.tsx');
const api = await load('api.ts');
const theme = await load('theme.tsx');
const { statusPageModel } = await load('views/StatusPages.tsx');

let failed = 0;
const check = async (name, fn) => { try { await fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };
const ch = code => String.fromCodePoint(code);
const LONE = String.fromCharCode(0xd800);
const html = node => renderToStaticMarkup(React.createElement(React.Fragment, null, node));
const render = text => html(md.markdownNodes(text));
const source = file => readFileSync(resolve(root, 'apps/web/src', file), 'utf8').replace(/\r\n/gu, '\n');
const goSource = file => readFileSync(resolve(root, 'apps/node-go/internal', file), 'utf8').replace(/\r\n/gu, '\n');

// The page root's Go test admits (status_presentation_test.go TestStatusPresentationStrictInputs).
const APPEARANCE = { theme: 'dark', accentColor: '#aAbB09', logoUrl: 'https://images.example.invalid/logo.png', footerText: 'Public footer', refreshIntervalMs: 10000, markdown: true };
const PAGE = {
  id: 'p1', revision: 'r1', updatedAt: 5, slug: 'public', title: 'Review public', description: 'Public description', enabled: true,
  groups: [{ id: 'web', name: 'Web', monitors: [{ monitorId: 'm1', name: 'API' }] }],
  announcements: [{ id: 'n1', title: 'Maintenance', message: 'Tonight', state: 'investigating', createdAt: 1, updatedAt: 2, pinned: false }],
  appearance: APPEARANCE, publicUrl: 'https://status.example.invalid/#/status/public', rssEnabled: true, futureTop: { kept: [1] },
};

await check('node rules as read: the presentation keys, theme list, refresh range, accent, logo, public address and the feed’s public address requirement', () => {
  const go = goSource('canary/status_presentation.go');
  assert.match(go, /statusObject\(fields\["appearance"\], "theme", "accentColor", "logoUrl", "footerText", "refreshIntervalMs", "markdown"\)/u);
  assert.match(go, /slices\.Contains\(\[\]string\{"", "system", "light", "dark"\}, appearance\.Theme\)/u);
  assert.match(go, /appearance\.RefreshIntervalMs != 0 && \(appearance\.RefreshIntervalMs < 10000 \|\| appearance\.RefreshIntervalMs > 300000\)/u);
  assert.match(go, /statusText\(appearance\.FooterText, 2048, true, true\)/u);
  assert.match(go, /value\.Scheme != "https" \|\| value\.Hostname\(\) == "" \|\| value\.User != nil \|\| value\.Opaque != "" \|\| value\.RawQuery != "" \|\| value\.ForceQuery \|\| value\.Fragment != ""/u);
  assert.match(go, /parsed\.Scheme != "https" && parsed\.Scheme != "http" \|\| strings\.ContainsAny\(in\.PublicURL, "\\\\\\r\\n\\t"\)/u);
  assert.match(go, /if in\.RSSEnabled && in\.PublicURL == "" \{/u);
  assert.match(go, /r\.URL\.RawQuery != "" \|\| r\.URL\.ForceQuery/u, 'the node’s feed route refuses any query');
  assert.match(go, /if page == nil \|\| !page\.RSSEnabled \|\| page\.PublicURL == "" \{/u, 'an unpublished page or a feed that is off answers not found');
  assert.match(goSource('canary/dashboard_http.go'), /query\.Del\("registration"\)/u, 'the dashboard’s public proxy strips its selector before asking the node');
  assert.deepEqual([...p.THEMES], ['', 'system', 'light', 'dark']);
  assert.equal(p.REFRESH_MIN_MS, 10_000); assert.equal(p.REFRESH_MAX_MS, 300_000); assert.equal(p.REFRESH_DEFAULT_MS, 30_000);
});

await check('public page address: complete http(s) links with a host, a query or hash route allowed; never credentials, a backslash or other schemes; not stricter than Go', () => {
  for (const url of ['', PAGE.publicUrl, 'http://localhost:5173/#/status/x?node=http%3A%2F%2F127.0.0.1%3A3100', 'https://dash.example/#/status/x?registration=abc', 'HTTPS://Status.Example/',
    'https://h/a b', 'https://h:99999/', 'https://[::1]:3100/', `https://${'a'.repeat(2030)}/`, 'https://status.example.com?']) {
    assert.equal(p.publicUrlProblem(url), undefined, url.slice(0, 60));
  }
  for (const url of ['javascript:alert(1)', 'https://user:pw@h/', 'https://h\\x', 'ftp://h/', 'https:///x', 'mailto:ops@example.com', 'https://a:1:2/', ' https://h/', 'https://h/\tx', `https://h/${LONE}`,
    `https://${'a'.repeat(2040)}/`, 'https://h/%zz', 'https://h/#%zz', '//h/x', 'https://exa mple.com/']) {
    assert.ok(p.publicUrlProblem(url), url.slice(0, 60));
  }
});

await check('logo address: https only, with a host, no credentials, ?query or #fragment text (a bare # passes, as in Go), at most 2,048 bytes', () => {
  for (const url of ['', APPEARANCE.logoUrl, 'HTTPS://h/logo.svg', 'https://h/logo.png#', 'https://h:8443/a%20b.png', 'https://[2001:db8::1]/l.png']) assert.equal(p.logoUrlProblem(url), undefined, url);
  for (const url of ['data:image/svg+xml,script', 'https://user:secret@images.invalid/logo', `${APPEARANCE.logoUrl}?secret=value`, `${APPEARANCE.logoUrl}?`, `${APPEARANCE.logoUrl}#frag`, 'http://h/logo.png',
    'javascript:alert(1)', '//h/logo.png', 'https://h\\logo', 'blob:https://h/1', `https://h/${'l'.repeat(2048)}`]) {
    assert.ok(p.logoUrlProblem(url), url.slice(0, 60));
  }
  assert.ok(p.logoUrlProblem('http://h/logo.png').includes('https://'));
});

await check('accent, footer, refresh and theme: exactly the node’s ranges (root’s Go refusals included)', () => {
  for (const color of ['', '#aAbB09', '#000000', '#FFFFFF']) assert.equal(p.accentProblem(color), undefined, color);
  for (const color of ['red;url(secret)', '#abc', '#abcdeg', 'aabbcc', '#aabbcc ', '#aabbccdd']) assert.ok(p.accentProblem(color), color);
  for (const text of ['', 'Public footer', 'line\nbreak', 'a'.repeat(2048), ch(0xe9).repeat(1024), `C1 ${ch(0x85)}`]) assert.equal(p.footerProblem(text), undefined, text.slice(0, 20));
  for (const text of ['line\x00break', 'tab\there', 'cr\rhere', 'a'.repeat(2049), `del${ch(0x7f)}`, `a${LONE}`]) assert.ok(p.footerProblem(text), JSON.stringify(text.slice(0, 12)));
  for (const ms of [0, 10_000, 12_345, 300_000]) assert.equal(p.refreshProblem(ms), undefined, String(ms));
  for (const ms of [9999, 300_001, 12_345.5, -1, Number.NaN]) assert.ok(p.refreshProblem(ms), String(ms));
  const errors = {};
  p.buildPresentation(undefined, { ...p.presentationDraft(undefined), theme: 'auto' }, errors);
  assert.deepEqual(Object.keys(errors), ['theme']);
});

await check('builder: nothing set sends nothing; every setting in Go’s order; a stored appearance (keys from a newer node included) saves byte for byte; RSS needs the public address', () => {
  let errors = {};
  assert.deepEqual(p.buildPresentation(undefined, p.presentationDraft(undefined), errors), {});
  assert.deepEqual(errors, {});
  errors = {};
  const typed = { theme: 'dark', accent: ' #aAbB09 ', logo: ` ${APPEARANCE.logoUrl} `, footer: 'Public footer', refreshMs: 10000, markdown: true, publicUrl: ` ${PAGE.publicUrl}`, rss: true };
  const built = p.buildPresentation(undefined, typed, errors);
  assert.deepEqual(errors, {});
  assert.equal(JSON.stringify(built), JSON.stringify({ appearance: APPEARANCE, publicUrl: PAGE.publicUrl, rssEnabled: true }), 'typed links and colors are trimmed; the footer is sent as typed');
  const future = { ...PAGE, appearance: { markdown: true, futureKey: { x: 1 }, theme: 'light' } };
  errors = {};
  const kept = p.buildPresentation(future, p.presentationDraft(future), errors);
  assert.deepEqual(errors, {});
  assert.equal(JSON.stringify(kept.appearance), JSON.stringify(future.appearance), 'key order and unknown keys stay');
  const empty = { ...PAGE, appearance: {}, publicUrl: undefined, rssEnabled: undefined };
  assert.equal(JSON.stringify(p.buildPresentation(empty, p.presentationDraft(empty), {})), '{"appearance":{}}', 'a stored empty appearance stays');
  errors = {};
  const cleared = p.buildPresentation(PAGE, { ...p.presentationDraft(PAGE), theme: '', accent: '', logo: '', footer: '', refreshMs: 0, markdown: false, publicUrl: '', rss: false }, errors);
  assert.deepEqual(cleared, { appearance: {} });
  errors = {};
  const orphan = p.buildPresentation(undefined, { ...p.presentationDraft(undefined), rss: true }, errors);
  assert.deepEqual(Object.keys(errors), ['rss']);
  assert.equal(orphan.rssEnabled, true, 'refused, and sent as the node would refuse it');
  errors = {};
  p.buildPresentation(undefined, { ...p.presentationDraft(undefined), logo: 'http://h/x.png', accent: 'red', publicUrl: 'javascript:alert(1)', footer: 'a\tb', refreshMs: 5 }, errors);
  assert.deepEqual(Object.keys(errors).sort(), ['accent', 'footer', 'logo', 'publicUrl', 'refresh']);
});

await check('editor model: a page with every presentation setting saves them unchanged with its other fields; a page without them saves exactly as before', () => {
  const draft = statusPageModel.pageDraft(PAGE);
  const built = statusPageModel.buildPage(draft, 10, PAGE);
  assert.ok(built.input, JSON.stringify(built.errors));
  assert.equal(JSON.stringify(built.input.appearance), JSON.stringify(PAGE.appearance));
  assert.equal(built.input.publicUrl, PAGE.publicUrl);
  assert.equal(built.input.rssEnabled, true);
  assert.deepEqual(built.input.futureTop, { kept: [1] });
  for (const key of ['id', 'revision', 'updatedAt']) assert.equal(key in built.input, false, key);
  const plain = { ...PAGE };
  delete plain.appearance; delete plain.publicUrl; delete plain.rssEnabled;
  const before = statusPageModel.buildPage(statusPageModel.pageDraft(plain), 10, plain);
  assert.ok(before.input);
  for (const key of ['appearance', 'publicUrl', 'rssEnabled']) assert.equal(key in before.input, false, key);
  const noAddress = statusPageModel.pageDraft(plain);
  noAddress.look = { ...noAddress.look, rss: true };
  const refused = statusPageModel.buildPage(noAddress, 10, plain);
  assert.equal(refused.input, undefined);
  assert.ok(refused.errors.rss?.includes('public address'));
});

await check('reader: each published value is used only when it passes the node’s own rule; a bad one drops that setting, never the page', () => {
  assert.deepEqual(p.readPresentation({ appearance: APPEARANCE, publicUrl: PAGE.publicUrl, rssEnabled: true }),
    { theme: 'dark', accent: '#aAbB09', logo: APPEARANCE.logoUrl, footer: 'Public footer', refreshMs: 10000, markdown: true, publicUrl: PAGE.publicUrl, rss: true });
  const hostile = p.readPresentation({
    appearance: { theme: 'auto', accentColor: 'red;}body{display:none', logoUrl: 'javascript:alert(1)', footerText: 42, refreshIntervalMs: 5, markdown: 'yes' },
    publicUrl: 'javascript:alert(1)', rssEnabled: 'true',
  });
  assert.deepEqual(hostile, { theme: '', accent: '', logo: '', footer: '', refreshMs: 30000, markdown: false, publicUrl: '', rss: false });
  for (const logo of ['http://h/x.png', 'https://h/x.png?track=1', 'data:image/png;base64,AAAA', '//h/x.png']) assert.equal(p.readPresentation({ appearance: { logoUrl: logo } }).logo, '', logo);
  assert.deepEqual(p.readPresentation(null), { theme: '', accent: '', logo: '', footer: '', refreshMs: 30000, markdown: false, publicUrl: '', rss: false });
  assert.equal(p.readPresentation({ appearance: { refreshIntervalMs: 300_001 } }).refreshMs, 30000);
  assert.equal(p.readPresentation({ appearance: { footerText: 'x'.repeat(9000) } }).footer.length, 8192);
});

await check('accent: CSS custom properties from a checked #rrggbb only; text and button fills only where they keep 4.5:1', () => {
  assert.deepEqual(p.accentVariables('red;}body{x', 'light'), {});
  assert.deepEqual(p.accentVariables('', 'dark'), {});
  assert.ok(Math.abs(p.contrast('#ffffff', '#000000') - 21) < 1e-9);
  const black = p.accentVariables('#000000', 'light');
  assert.deepEqual(Object.keys(black).sort(), ['--accent', '--accent-btn', '--accent-text', '--accent-wash', '--page-accent']);
  assert.equal(black['--accent-wash'], 'rgba(0, 0, 0, .10)');
  const yellowLight = p.accentVariables('#FFFF00', 'light');
  assert.equal(yellowLight['--accent'], '#ffff00');
  assert.equal('--accent-text' in yellowLight || '--accent-btn' in yellowLight, false, 'pale on light: marks only');
  const yellowDark = p.accentVariables('#ffff00', 'dark');
  assert.equal(yellowDark['--accent-text'], '#ffff00');
  assert.equal('--accent-btn' in yellowDark, false, 'white text on yellow would be unreadable');
  for (const value of Object.values(p.accentVariables('#2a78d6', 'dark'))) assert.match(value, /^(?:#[0-9a-f]{6}|rgba\(\d{1,3}, \d{1,3}, \d{1,3}, \.\d{2}\))$/u);
});

await check('Markdown: emphasis, code, lists, line breaks and safe links become fixed elements; HTML, scripts, images and unsafe links stay text', () => {
  assert.equal(render('a\nb'), '<p>a<br/>b</p>');
  assert.equal(render('**bold** and *em* and _em_ and __strong__'), '<p><strong>bold</strong> and <em>em</em> and <em>em</em> and <strong>strong</strong></p>');
  assert.equal(render('snake_case_name stays'), '<p>snake_case_name stays</p>');
  assert.equal(render('\\*not em\\*'), '<p>*not em*</p>');
  assert.equal(render('`code <b>`'), '<p><code>code &lt;b&gt;</code></p>');
  assert.equal(render('```\n<script>x</script>\n```'), '<pre><code>&lt;script&gt;x&lt;/script&gt;</code></pre>');
  assert.equal(render('- one\n- two\n\n1. a\n2. b'), '<ul><li>one</li><li>two</li></ul><ol><li>a</li><li>b</li></ol>');
  assert.equal(render('3. three'), '<ol start="3"><li>three</li></ol>');
  const linked = render('[docs](https://docs.example.com/a?b=1#c) and <https://status.example.com/>');
  assert.ok(linked.includes('href="https://docs.example.com/a?b=1#c"') && linked.includes('>docs</a>'), linked);
  assert.ok(linked.includes('href="https://status.example.com/"') && linked.includes('>https://status.example.com/</a>'), linked);
  assert.equal((linked.match(/rel="noopener noreferrer nofollow"/gu) ?? []).length, 2);
  assert.equal((linked.match(/target="_blank"/gu) ?? []).length, 2);
  assert.equal((linked.match(/referrerpolicy="no-referrer"/giu) ?? []).length, 2);
  for (const unsafe of ['<script>alert(1)</script>', '<img src=x onerror=alert(1)>', '![logo](https://h/x.png)', '[click](javascript:alert(1))', '[x](data:text/html,hi)', '[x](https://user:pw@h/)',
    '[x](/relative)', '[x](HTTPS://h/ "title")', '<javascript:alert(1)>', '[x](vbscript:msgbox)', '<a href="https://h/">raw</a>']) {
    const out = render(unsafe);
    assert.equal(/<(?:script|img|a|iframe|object|svg|style)\b/iu.test(out), false, `${unsafe} -> ${out}`);
  }
  assert.ok(render('<img src=x onerror=alert(1)>').includes('&lt;img src=x onerror=alert(1)&gt;'));
  assert.ok(render('![logo](https://h/x.png)').includes('![logo](https://h/x.png)'), 'an image stays its own text');
  for (const href of ['https://h/', 'http://h:8080/x?y#z']) assert.ok(md.safeHref(href));
  for (const href of ['javascript:alert(1)', 'https://u:p@h/', '/x', 'https://h/a b', '', `https://h/${'x'.repeat(2048)}`, 'ftp://h/', 'https://h/"x']) assert.equal(md.safeHref(href), null, href.slice(0, 40));
  assert.equal(html(React.createElement(md.RichText, { text: '**x** <b>', markdown: false, className: 'pub-desc' })), '<p class="pub-desc">**x** &lt;b&gt;</p>', 'plain text by default');
  assert.equal(html(React.createElement(md.RichText, { text: '**x**', markdown: true, className: 'pub-desc' })), '<div class="md pub-desc"><p><strong>x</strong></p></div>');
  for (const file of ['markdown.tsx', 'views/PublicStatus.tsx', 'views/StatusPages.tsx', 'statusPresentation.ts']) {
    assert.equal(/dangerouslySetInnerHTML|innerHTML|insertAdjacentHTML|document\.write/u.test(source(file)), false, file);
  }
});

await check('Markdown bounds: element budget, length cap and pathological input finish quickly with bounded output', () => {
  const started = Date.now();
  const many = render('**x** '.repeat(3000));
  assert.equal((many.match(/<strong>/gu) ?? []).length, 2000, 'at most 2,000 formatted elements');
  const long = render('a'.repeat(md.MARKDOWN_MAX_CHARS + 10));
  assert.ok(long.endsWith('<p>aaaaaaaaaa</p>'), 'text past the cap stays plain');
  for (const text of ['*a '.repeat(3000), '*'.repeat(8000), '['.repeat(4000) + ']'.repeat(10), '`'.repeat(5000), `${'*'.repeat(40)}x${'*'.repeat(40)}`, '_a_'.repeat(3000), '<'.repeat(6000)]) {
    assert.equal(typeof render(text), 'string');
  }
  assert.ok(Date.now() - started < 5000, `took ${Date.now() - started} ms`);
});

await check('RSS feed address: the node’s own path without a query, or the dashboard’s public proxy with only the registration selector; nothing is fetched', () => {
  assert.equal(api.publicRssUrl('https://node.example:3100', 'ops'), 'https://node.example:3100/public/status/ops/rss.xml');
  assert.equal(api.publicRssUrl('https://dash.example', 'ops', 'reg-01'), 'https://dash.example/public/status/ops/rss.xml?registration=reg-01');
  assert.throws(() => api.publicRssUrl('https://node.example', 'Not a slug'), /Invalid status page address/u);
  assert.throws(() => api.publicRssUrl('https://node.example', 'ops', 'a b'));
  assert.throws(() => api.publicRssUrl('https://node.example/api', 'ops'));
  const page = source('views/PublicStatus.tsx');
  assert.match(page, /if \(shown && look\?\.rss && look\.publicUrl\)/u, 'the link appears only for a read page whose node serves the feed');
  assert.match(page, /type="application\/rss\+xml"/u);
});

await check('public page wiring: the logo renders only from a read and re-checked page, without a referrer; theme, accent and refresh come from the checked presentation', () => {
  const page = source('views/PublicStatus.tsx');
  assert.match(page, /presentation: readPresentation\(page\)/u);
  assert.match(page, /\{look\?\.logo \? \(/u);
  assert.match(page, /<img className="pub-logo" src=\{look\.logo\} alt="" referrerPolicy="no-referrer" decoding="async" \/>/u);
  assert.equal((page.match(/<img\b/gu) ?? []).length, 1, 'no other image on the public page');
  assert.match(page, /refreshMs\.current = shownPage\.presentation\.refreshMs/u);
  assert.match(page, /accentVariables\(look\.accent, theme\.mode\)/u);
  assert.match(page, /useEffect\(\(\) => \(\) => setPageTheme\(null\), \[\]\)/u, 'leaving the page clears its theme');
  const editor = source('views/StatusPages.tsx');
  assert.match(editor, /logoShown === look\.logo\.trim\(\) && logoReady/u, 'the editor loads a logo only after it is asked to');
  assert.match(editor, /!logoUrlProblem\(look\.logo\.trim\(\)\)/u);
});

await check('theme default: a page’s light or dark theme replaces only the system setting and is never stored', () => {
  const button = t => html(React.createElement(theme.ThemeButton, { theme: { cycle() {}, ...t } }));
  assert.ok(button({ preference: 'system', mode: 'dark', page: 'dark' }).includes('aria-label="Theme: Page default (dark). Switch to light."'));
  assert.ok(button({ preference: 'system', mode: 'light', page: null }).includes('aria-label="Theme: System (light). Switch to light."'));
  assert.ok(button({ preference: 'dark', mode: 'dark', page: 'light' }).includes('aria-label="Theme: Dark. Switch to the page default."'));
  theme.setPageTheme('dark'); theme.setPageTheme(null);
  assert.match(source('theme.tsx'), /const mode = preference === 'system' \? \(page \?\? \(systemDark \? 'dark' : 'light'\)\) : preference;/u);
});

await check('no storage or network: loading, building and rendering touched nothing', () => {
  assert.deepEqual(touched, []);
  assert.deepEqual(requests, []);
});

console.log(failed ? `${failed} check group(s) failed` : 'all status presentation checks passed');
process.exit(failed ? 1 : 0);
