import { useState, type ReactNode } from 'react';
import { badgeHtml, badgeMarkdown, badgeTargets, badgeUrl, type BadgeSelection } from '../badges';
import { CopyButton } from './bits';
import { Glyph } from './icons';

function BadgeRow({ image, alt, link, label, full, nonce }: { image: string; alt: string; link: string; label: ReactNode; full?: boolean; nonce: number }) {
  const [failed, setFailed] = useState<string | null>(null);
  const attempt = `${image}#${nonce}`;
  return (
    <div className="badge-row">
      <div className="bname">
        {label}
        <div className="badge-prev">
          {failed === attempt
            ? <span className="small muted"><Glyph tone="warn" /> No badge from this address right now: not published there yet, not found, or unreachable from this browser.</span>
            : <img key={attempt} src={image} alt={`Current badge: ${alt}`} referrerPolicy="no-referrer" onError={() => setFailed(attempt)} onLoad={() => setFailed(null)} />}
        </div>
      </div>
      <div className="bacts">
        <CopyButton value={badgeMarkdown(image, alt, link)} label="Markdown" ariaLabel={`Copy Markdown for the ${alt} badge`} />
        {full ? <CopyButton value={badgeHtml(image, alt, link)} label="HTML" ariaLabel={`Copy HTML for the ${alt} badge`} /> : null}
        <CopyButton value={image} label="Image URL" ariaLabel={`Copy the image address of the ${alt} badge`} />
      </div>
      {full ? <code className="link-out">{image}</code> : null}
    </div>
  );
}

/**
 * Badge addresses for one published page on one node: the whole page, then each service whose public section ID and
 * public name pick exactly one entry. Ambiguous names get an explanation instead of a link that would answer 404.
 */
export function BadgeList({ slug, title, groups, nodeUrl, pageLink, nonce }: {
  slug: string; title: string; nodeUrl: string; pageLink: string; nonce: number;
  groups: { id: string; name: string; monitors: { name: string }[] }[];
}) {
  let whole: string;
  try { whole = badgeUrl(nodeUrl, slug); } catch (error) { return <p className="small fwarn"><Glyph tone="warn" />{error instanceof Error ? error.message : 'No badge address.'}</p>; }
  const targets = badgeTargets(groups);
  const seen = new Set<string>();
  const rows: ({ kind: 'ok'; selection: BadgeSelection; section: string } | { kind: 'dup'; name: string; section: string; matches: number })[] = [];
  for (const target of targets) {
    const key = JSON.stringify([target.group, target.monitor]);
    if (seen.has(key) || target.matches === 0) continue;
    seen.add(key);
    rows.push(target.matches === 1 ? { kind: 'ok', selection: { group: target.group, monitor: target.monitor }, section: target.sectionName } : { kind: 'dup', name: target.monitor, section: target.sectionName, matches: target.matches });
  }
  const usable = rows.filter(row => row.kind === 'ok').length;
  return (
    <div className="badges">
      <BadgeRow image={whole} alt={`${title} status`} link={pageLink} full nonce={nonce} label={<><b>Whole page</b> <span className="muted small">overall state of {title}</span></>} />
      {rows.length ? (
        <details className="badge-more">
          <summary>Single services <span className="muted">({usable} of {targets.length})</span></summary>
          {rows.map(row => row.kind === 'ok' ? (
            <BadgeRow key={JSON.stringify(row.selection)} image={badgeUrl(nodeUrl, slug, row.selection)} alt={`${row.selection.monitor} status`} link={pageLink} nonce={nonce}
              label={<><span className="muted">{row.section} ›</span> <b>{row.selection.monitor}</b></>} />
          ) : (
            <div key={`dup-${row.section}-${row.name}`} className="badge-row off">
              <div className="bname"><span>{row.section} ›</span> <b>{row.name}</b></div>
              <p className="small">No badge: {row.matches} services in this section share this public name, so the node can’t tell which one is meant. Give each a distinct public name.</p>
            </div>
          ))}
        </details>
      ) : null}
    </div>
  );
}
