import type { MonitorTag, TagView } from '@uptime-canary/shared';
import { Icon } from './icons';

/** Tag colors come from operators (and, on public pages, from the network): only strict #rrggbb reaches a style. */
export const safeColor = (color: string | undefined): string => color && /^#[0-9a-f]{6}$/iu.test(color) ? color : 'var(--neutral)';

/** Color is decoration only: the swatch sits beside the name, and text keeps the normal text color. */
export function TagPill({ name, color, value, isPublic, missing, conflict }: { name: string; color?: string; value?: string; isPublic?: boolean; missing?: boolean; conflict?: boolean }) {
  return (
    <span className={`tagpill${missing ? ' missing' : ''}`}>
      <i className="sw-tag" style={{ background: safeColor(color) }} aria-hidden="true" />
      <span className="tn">{name}</span>
      {value ? <span className="tv">{value}</span> : null}
      {isPublic ? <Icon name="globe" className="tp" /> : null}
      {isPublic ? <span className="vh"> (public)</span> : null}
      {conflict ? <span className="vh"> (tag has conflicting versions)</span> : null}
    </span>
  );
}

/** A monitor's assigned tags, resolved against the catalog. Unknown IDs stay visible rather than vanishing. */
export function MonitorTags({ tags, catalog, limit }: { tags: MonitorTag[] | undefined; catalog: Map<string, TagView>; limit?: number }) {
  if (!tags?.length) return null;
  const shown = limit ? tags.slice(0, limit) : tags;
  return (
    <span className="tagrow">
      {shown.map(assignment => {
        const view = catalog.get(assignment.tagId);
        return view
          ? <TagPill key={assignment.tagId} name={view.tag.name} color={view.tag.color} value={assignment.value} isPublic={assignment.public} conflict={view.configConflict} />
          : <TagPill key={assignment.tagId} name={`tag ${assignment.tagId.slice(0, 8)}`} value={assignment.value} missing />;
      })}
      {limit && tags.length > limit ? <span className="muted small">+{tags.length - limit}</span> : null}
    </span>
  );
}
