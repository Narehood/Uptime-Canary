import { useId, useMemo, useState, type FormEvent } from 'react';
import type { Tag, TagInput } from '@uptime-canary/shared';
import { ApiFailure, describeFailure } from '../api';
import { useCanary } from '../context';
import { plural, shortRev } from '../format';
import { notify } from '../notices';
import { href } from '../router';
import { Banner, Card, EmptyState, ErrorText, Modal, PageHead, ReadOnly, Rev } from '../ui/bits';
import { Glyph, Icon } from '../ui/icons';
import { TagPill, safeColor } from '../ui/tags';
import { storedExtras } from '../writable';

/** A few distinguishable starting colors. Any #rrggbb works; color is never the only label. */
const PALETTE = ['#2a78d6', '#0ca30c', '#d03b3b', '#fab219', '#8a5cd1', '#1f9e9e', '#ec835a', '#6f6d68'];
const HEX = /^#[0-9a-fA-F]{6}$/u;
const TAG_KEYS = ['name', 'color'];

/** The saved tag: name and color from the form, and every other key of the stored tag (a newer node's) as it was. */
function tagInput(name: string, color: string, stored: Tag | undefined): TagInput {
  return { name: name.trim(), color: color.toLowerCase(), ...storedExtras(stored, TAG_KEYS) };
}

/** The save model, for scripts/editor-preservation-pure.mjs. */
export const tagFormModel = { tagInput };

function TagEditorDialog({ tag, onClose }: { tag?: Tag; onClose: () => void }) {
  const { api, endpoint, tags, stillCurrent } = useCanary();
  const [name, setName] = useState(tag?.name ?? '');
  const [color, setColor] = useState(tag?.color ?? PALETTE[(tags.list?.length ?? 0) % PALETTE.length]!);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [tried, setTried] = useState(false);
  const id = useId();
  const nameProblem = !name.trim() || new TextEncoder().encode(name).length > 128 || /[\x00-\x1f\x7f]/u.test(name) ? 'Enter a name of at most 128 bytes on one line.' : undefined;
  const colorProblem = HEX.test(color) ? undefined : 'Use a color like #2a78d6.';
  const submit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    setTried(true);
    if (nameProblem || colorProblem || busy) return;
    setBusy(true); setError(null);
    try {
      const input = tagInput(name, color, tag);
      const saved = tag ? await api.updateTag(tag.id, { ...input, revision: tag.revision }) : await api.createTag(input);
      if (!stillCurrent()) return;
      notify(`${tag ? 'Saved' : 'Created'} tag “${saved.name}” on ${endpoint} (revision ${shortRev(saved.revision)}).`);
      tags.reload();
      onClose();
    } catch (failure) {
      if (!stillCurrent()) return;
      setError(failure instanceof ApiFailure && failure.status === 409 ? 'Someone changed this tag first, or nodes hold conflicting versions. Close and reopen it.' : describeFailure(failure, { preferDetail: true }));
      setBusy(false);
    }
  };
  return (
    <Modal title={tag ? `Edit tag ${tag.name}` : 'New tag'} onClose={onClose} busy={busy}>
      <form className="form one" onSubmit={event => { void submit(event); }} noValidate>
        {error ? <div className="errsum" role="alert"><Glyph tone="down" /><div><b>Not saved</b><p>{error}</p></div></div> : null}
        <div className="fld">
          <label htmlFor={`${id}-name`}>Name</label>
          <input id={`${id}-name`} type="text" value={name} maxLength={128} autoFocus onChange={event => setName(event.target.value)} aria-invalid={tried && nameProblem ? true : undefined} />
          <small>Your own label, such as an environment, team or customer.</small>
          <ErrorText id={`${id}-name-e`}>{tried ? nameProblem : undefined}</ErrorText>
        </div>
        <fieldset className="fld">
          <legend>Color</legend>
          <div className="swatches">
            {PALETTE.map(option => (
              <label key={option} className="swatch">
                <input type="radio" name={`${id}-color`} value={option} checked={color.toLowerCase() === option} onChange={() => setColor(option)} />
                <span style={{ background: option }} aria-label={option} />
              </label>
            ))}
            <label className="swatch custom">
              <span className="vh">Custom color</span>
              <input type="color" value={HEX.test(color) ? color : '#2a78d6'} onChange={event => setColor(event.target.value)} />
            </label>
            <input type="text" className="mono hexin" aria-label="Color hex value" value={color} maxLength={7} onChange={event => setColor(event.target.value)} aria-invalid={tried && colorProblem ? true : undefined} />
          </div>
          <small>Decoration only: the name always appears beside it.</small>
          <ErrorText id={`${id}-color-e`}>{tried ? colorProblem : undefined}</ErrorText>
        </fieldset>
        <p className="small">Preview: <TagPill name={name || 'tag'} color={safeColor(color)} value="value" /></p>
        <div className="row-wrap modal-acts">
          <button type="submit" className="btn primary" disabled={busy}><Icon name={tag ? 'check' : 'plus'} />{busy ? `Saving to ${endpoint}…` : tag ? 'Save tag' : 'Create tag'}</button>
          <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
        </div>
      </form>
    </Modal>
  );
}

function DeleteTagDialog({ tag, users, onClose }: { tag: Tag; users: number; onClose: () => void }) {
  const { api, endpoint, tags, stillCurrent } = useCanary();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const remove = async (): Promise<void> => {
    setBusy(true); setError(null);
    try {
      await api.deleteTag(tag.id, tag.revision);
      if (!stillCurrent()) return;
      notify(`Deleted tag “${tag.name}” on ${endpoint}.`);
      tags.reload();
      onClose();
    } catch (failure) {
      if (!stillCurrent()) return;
      setError(describeFailure(failure)); setBusy(false);
    }
  };
  return (
    <Modal title={`Delete tag ${tag.name}?`} onClose={onClose} busy={busy}>
      <p>{users ? `${plural(users, 'monitor')} keep${users === 1 ? 's' : ''} the assignment, shown as an unknown tag, until edited.` : 'No monitor uses it.'} Checks, incidents and alerts are unaffected.</p>
      {error ? <div className="errsum" role="alert"><Glyph tone="down" /><div><b>Not deleted</b><p>{error}</p></div></div> : null}
      <div className="row-wrap modal-acts">
        <button type="button" className="btn danger-solid" onClick={() => { void remove(); }} disabled={busy}><Icon name="trash" />{busy ? 'Deleting…' : 'Delete tag'}</button>
        <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
      </div>
    </Modal>
  );
}

export function TagsView() {
  const { tags, editable, endpoint, overview, search } = useCanary();
  const [editing, setEditing] = useState<Tag | 'new' | null>(null);
  const [deleting, setDeleting] = useState<Tag | null>(null);
  const usage = useMemo(() => {
    const counts = new Map<string, { monitors: number; isPublic: number }>();
    for (const view of overview.monitors) for (const tag of view.monitor.tags ?? []) {
      const entry = counts.get(tag.tagId) ?? { monitors: 0, isPublic: 0 };
      entry.monitors++;
      if (tag.public) entry.isPublic++;
      counts.set(tag.tagId, entry);
    }
    return counts;
  }, [overview.monitors]);
  const q = search.trim().toLowerCase();
  const list = (tags.list ?? []).filter(view => !view.tag.deleted && (!q || view.tag.name.toLowerCase().includes(q))).sort((a, b) => a.tag.name.localeCompare(b.tag.name));
  return (
    <div className="view">
      <PageHead
        crumbs={<a href={href('monitors')}>Monitors</a>}
        title="Tags"
        sub={<>Reusable labels known to <span className="mono">{endpoint}</span>. Assign them, with an optional value, in each monitor’s settings.</>}
        actions={editable && !tags.unsupported ? <button type="button" className="btn primary" onClick={() => setEditing('new')}><Icon name="plus" />New tag</button> : !editable ? <ReadOnly>Viewers can’t edit tags</ReadOnly> : null}
      />
      <Card>
        {tags.unsupported ? <EmptyState title={`${endpoint} doesn’t offer tags`}>It probably runs an older Uptime-Canary version.</EmptyState>
          : tags.error && !tags.list ? <Banner tone="down" title="Tags could not be loaded" actions={<button type="button" className="btn sm" onClick={tags.reload}><Icon name="refresh" />Try again</button>}>{describeFailure(tags.error)}</Banner>
          : !tags.list ? <p className="muted">Loading tags from {endpoint}…</p>
          : list.length ? (
            <div className="tbl-wrap">
              <table className="tbl stack">
                <caption className="vh">Tags</caption>
                <thead><tr><th scope="col">Tag</th><th scope="col">Used by</th><th scope="col">Revision</th>{editable ? <th scope="col"><span className="vh">Actions</span></th> : null}</tr></thead>
                <tbody>
                  {list.map(view => {
                    const use = usage.get(view.tag.id);
                    return (
                      <tr key={view.tag.id}>
                        <td className="lead" data-label="Tag"><TagPill name={view.tag.name} color={view.tag.color} /> <span className="mono muted small">{view.tag.color}</span></td>
                        <td data-label="Used by">{use ? <a href={href('monitors')}>{plural(use.monitors, 'monitor')}</a> : <span className="muted">none</span>}{use?.isPublic ? <span className="muted"> · {use.isPublic} public</span> : null}</td>
                        <td data-label="Revision">{view.configConflict ? <a href={href('config')} className="nowrap"><Glyph tone="serious" /> conflict</a> : <Rev revision={view.tag.revision} />}</td>
                        {editable ? (
                          <td data-label="Actions" className="acts-cell">
                            <button type="button" className="btn sm" onClick={() => setEditing(view.tag)} disabled={view.configConflict}><Icon name="edit" />Edit</button>
                            <button type="button" className="btn sm danger" onClick={() => setDeleting(view.tag)} disabled={view.configConflict} aria-label={`Delete ${view.tag.name}`}><Icon name="trash" /></button>
                          </td>
                        ) : null}
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          ) : <EmptyState title={tags.list.length ? 'Nothing matches' : 'No tags yet'} action={!tags.list.length && editable ? <button type="button" className="btn primary" onClick={() => setEditing('new')}><Icon name="plus" />New tag</button> : undefined}>
              {tags.list.length ? 'Try another search.' : 'Tags label monitors, such as env: production or team: payments, and filter the monitor list.'}
            </EmptyState>}
        <p className="foot-note">Tag edits never change what a monitor checks, so they keep open incidents. A status page shows a monitor’s tags only if the page turns tags on for that monitor and the assignment is marked public.</p>
      </Card>
      {editing ? <TagEditorDialog tag={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} /> : null}
      {deleting ? <DeleteTagDialog tag={deleting} users={usage.get(deleting.id)?.monitors ?? 0} onClose={() => setDeleting(null)} /> : null}
    </div>
  );
}
