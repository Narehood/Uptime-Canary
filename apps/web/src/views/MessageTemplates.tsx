import { useEffect, useRef, useState, type ReactNode } from 'react';
import {
  ADDED_PLACEHOLDER_NAMES, DISCORD_LIMIT, PLACEHOLDER_MEANING, SAMPLE_EVENTS, TEMPLATE_PROTOCOL, applyPreset, chatFit, combinesTitle, discordFixedText,
  discordMessage, earlierPresetOf, placeholderNames, presetLabel, presetOf, presetsFor, prepareMessage, replacesCustom, sameFields, sampleValues,
  templateFamily, templateTokens,
  type AnyPlaceholderName, type DiscordMessage, type DiscordPart, type MessageValues, type PresetId, type SampleEvent, type TemplateCapability,
  type TemplateFamily, type TemplateFields, type TemplateLevel,
} from '../alertTemplates';
import { isMessaging, TEXT_LIMIT, TITLE_LIMIT } from '../messaging';
import type { Tone } from '../model';
import { ErrorText, Tag } from '../ui/bits';
import { Seg, TextAreaField, TextField } from '../ui/forms';
import { Glyph } from '../ui/icons';

const EVENT_TONE: Record<SampleEvent, Tone> = { down: 'down', repeat: 'warn', recovery: 'up', test: 'pending' };
const EVENT_NAME: Record<SampleEvent, string> = { down: 'Sample down alert', repeat: 'Sample repeat alert', recovery: 'Sample recovery', test: 'What a test sends' };
const LEGACY: TemplateCapability = { level: 'legacy' };
const ADDED: ReadonlySet<string> = new Set(ADDED_PLACEHOLDER_NAMES);
const token = (name: string): string => `{{${name}}}`;
const units = (count: number): string => count.toLocaleString('en-US');

/** What the node reports about itself, never a guessed version. */
const reports = (capability: TemplateCapability, nodeName: string): string =>
  capability.protocol === undefined ? `${nodeName} doesn’t report its peer protocol` : `${nodeName} reports peer protocol ${capability.protocol}`;

/**
 * A channel's message templates: presets, the fields and a preview with sample data. A preset changes the fields only when
 * the user picks it; replacing custom text asks first, and an applied preset can be undone until the next edit. The preview
 * is computed locally with the node's own rules (alertTemplates.ts). Nothing here sends, stores or fetches anything: the
 * form's "Test these settings" stays the only way to send. What's offered follows the selected node's peer protocol: the
 * added placeholders, Discord wording and Discord's card layout need protocol 20, and older nodes keep the six-placeholder
 * presets and Discord's fixed message.
 */
export function MessageTemplates({ kind, providerLabel, icon, channelName, fields, onChange, saving, error, formId, capability = LEGACY, nodeName = 'This node', embed = false, onEmbed }: {
  kind: string;
  providerLabel: string;
  icon: ReactNode;
  /** Discord's fixed test text names the channel. */
  channelName: string;
  fields: TemplateFields;
  onChange: (change: Partial<TemplateFields>) => void;
  saving: boolean;
  error?: string;
  formId: string;
  /** The selected node's template grammar; an unknown node gets the six placeholders. */
  capability?: TemplateCapability;
  /** The selected node, named in the upgrade note. */
  nodeName?: string;
  /** Discord's card layout (webhook.embed), on peer protocol 20. Only the layout control changes it. */
  embed?: boolean;
  onEmbed?: (embed: boolean) => void;
}) {
  const level = capability.level;
  const family = templateFamily(kind, level);
  const [event, setEvent] = useState<SampleEvent>('down');
  const [confirming, setConfirming] = useState<PresetId | null>(null);
  const [undo, setUndo] = useState<{ before: TemplateFields; after: TemplateFields; id: PresetId } | null>(null);
  const replaceRef = useRef<HTMLButtonElement>(null);
  useEffect(() => { if (confirming) replaceRef.current?.focus(); }, [confirming]);
  const errorText = error ? <ErrorText id={`${formId}-tpl`}>{error}</ErrorText> : null;
  const preview = <TemplatePreview kind={kind} family={family} level={level} embed={embed} providerLabel={providerLabel} icon={icon} fields={fields} channelName={channelName} event={event} onEvent={setEvent} formId={formId} />;

  if (family === 'fixed') {
    const stored = Boolean(fields.subject || fields.text || fields.html);
    return (
      <>
        <p className="full muted small">
          {kind === 'discord'
            ? <>Discord channels send Canary’s fixed message, shown below, so templates aren’t used. Your own wording works with Slack, Telegram, ntfy, Gotify, Pushover and email channels.</>
            : <>Webhook channels receive Canary’s JSON event (monitor, incident, kind, sending node and each node’s observations), so templates aren’t used.</>}
        </p>
        {kind === 'discord' ? <p className="full muted small">Your own Discord wording and the card layout need peer protocol {TEMPLATE_PROTOCOL} or later. {reports(capability, nodeName)}.</p> : null}
        {stored ? (
          <div className="full tpl-fields">
            <p className="fwarn"><Glyph tone="warn" /><span>This channel stores template text that {providerLabel} doesn’t use. It’s kept as it is unless you clear it.</span></p>
            {fields.subject ? <TextField label="Subject (unused)" value={fields.subject} onChange={value => onChange({ subject: value })} disabled={saving} /> : null}
            {fields.text ? <TextAreaField label="Text (unused)" value={fields.text} onChange={value => onChange({ text: value })} rows={3} disabled={saving} /> : null}
            {fields.html ? <TextAreaField mono label="HTML (unused)" value={fields.html} onChange={value => onChange({ html: value })} rows={3} disabled={saving} /> : null}
            <div className="row-wrap"><button type="button" className="btn sm" onClick={() => onChange({ subject: '', text: '', html: '' })} disabled={saving}>Clear unused templates</button></div>
            {errorText}
          </div>
        ) : errorText ? <div className="full">{errorText}</div> : null}
        {kind === 'discord' ? <div className="full">{preview}</div> : null}
      </>
    );
  }

  const messaging = isMessaging(kind);
  const discord = family === 'discord';
  // Chat, push and Discord take a title and plain text; only email has a subject and an HTML part.
  const titled = messaging || discord;
  const presets = presetsFor(level);
  const current = presetOf(fields, family, level);
  const earlier = earlierPresetOf(fields, family, level);
  const apply = (id: PresetId): void => {
    const next = applyPreset(fields, id, family, level);
    setConfirming(null);
    setUndo({ before: fields, after: next, id });
    onChange(next);
  };
  const choose = (id: PresetId): void => { if (replacesCustom(fields, family, level)) setConfirming(id); else apply(id); };
  const undoable = undo && sameFields(undo.after, fields) ? undo : null;
  const ownWords = titled ? 'title and text' : fields.html ? 'subject, text and HTML' : 'subject and text';
  const literal = [...new Set([fields.subject, fields.text, family === 'email' ? fields.html : ''].flatMap(source => templateTokens(source, level).literal))];
  const later = literal.filter(raw => ADDED.has(raw.slice(2, -2)));
  return (
    <>
      <p className="full muted small">Optional literal text with these substitutions only: {placeholderNames(level).map(name => <span key={name} className="mono tok">{token(name)}</span>)}. Nothing executes, and HTML substitutions are escaped. Avoid putting private details in messages.</p>
      {level === 'legacy' ? (
        <p className="full muted small">The status word, the check’s target, outage times, downtime, latency per location and the sending node need peer protocol {TEMPLATE_PROTOCOL} or later, as do Discord wording and cards. {reports(capability, nodeName)}, so the presets use the six above.</p>
      ) : null}
      {discord ? (
        <Seg legend="Discord layout" name={`${formId}-dc-layout`} value={embed ? 'card' : 'plain'} disabled={saving} full
          options={[{ value: 'plain', label: 'Plain message' }, { value: 'card', label: 'Card' }]} onChange={value => onEmbed?.(value === 'card')}
          hint={embed ? 'A Discord card (embed): an edge colored by event, the title, the text and the event’s time.' : 'An ordinary Discord message. A card adds an edge colored by event and the event’s time.'} />
      ) : null}
      <div className="full tpl-presets">
        <div className="tpl-presets-h">
          <h4 id={`${formId}-presets`}>Presets</h4>
          <span className="muted small">{current !== 'custom' ? `Now: ${presetLabel(current)}` : earlier ? `Now: earlier ${presetLabel(earlier)} preset` : 'Now: your own wording'}</span>
        </div>
        <ul className="tpl-cards" aria-labelledby={`${formId}-presets`}>
          {presets.map(preset => (
            <li key={preset.id} className={current === preset.id ? 'on' : undefined}>
              <b>{preset.label}</b>
              <p className="muted small">{discord && preset.discord ? preset.discord : preset.summary}</p>
              {current === preset.id ? <Tag icon="check">In use</Tag>
                : <button type="button" className="btn sm" onClick={() => choose(preset.id)} disabled={saving} aria-label={`Use the ${preset.label} preset`}>Use</button>}
            </li>
          ))}
        </ul>
        {confirming ? (
          <div className="tpl-confirm" role="group" aria-labelledby={`${formId}-replace`}>
            <p id={`${formId}-replace`}><Glyph tone="warn" />Replace your own {ownWords} with the {presetLabel(confirming)} preset?</p>
            <div className="row-wrap">
              <button ref={replaceRef} type="button" className="btn sm primary" onClick={() => apply(confirming)} disabled={saving}>Replace</button>
              <button type="button" className="btn sm" onClick={() => setConfirming(null)}>Keep mine</button>
            </div>
          </div>
        ) : null}
        <p className="tpl-undo small" role="status">
          {undoable ? <>{presetLabel(undoable.id)} preset applied. <button type="button" className="linkbtn" onClick={() => { onChange(undoable.before); setUndo(null); }} disabled={saving}>Undo</button></> : null}
        </p>
      </div>
      <div className="full tpl-work">
        <div className="tpl-fields">
          {messaging ? (
            <p className="muted small">
              {providerLabel} gets plain text: formatting characters appear as typed. Built-in wording that runs long is shortened to fit and ends with “[truncated; see monitor details]”.
              Your own title and text are sent exactly; if a filled-in template is too long, that attempt fails instead of being cut.
            </p>
          ) : null}
          {discord ? (
            <p className="muted small">
              Discord formats Markdown in your own wording. Filled-in values get a backslash before Markdown characters, so names and messages show as typed, and mentions never ping.
              Your own title and text are sent exactly; if a filled-in template is over Discord’s limit, that attempt fails instead of being cut.
            </p>
          ) : null}
          <TextField label={titled ? 'Title' : 'Subject'} value={fields.subject} onChange={value => onChange({ subject: value })} disabled={saving}
            placeholder={discord ? (embed ? '{{status}} {{monitor_name}}' : undefined) : '{{event_kind}} {{monitor_name}}'}
            hint={messaging ? TITLE_LIMIT
              : discord ? (embed ? `One line, at most ${units(DISCORD_LIMIT.title)} UTF-16 units after substitution. Empty uses the status and the monitor’s name.` : `One line. Title and text together: at most ${units(DISCORD_LIMIT.content)} UTF-16 units after substitution.`)
              : 'One line, at most 512 bytes after substitution. Empty uses the built-in wording.'} />
          <TextAreaField optional label="Text" value={fields.text} onChange={value => onChange({ text: value })} rows={6} disabled={saving} placeholder={discord ? undefined : '{{observations_summary}}'}
            hint={isMessaging(kind) ? TEXT_LIMIT[kind].rule
              : discord ? (embed ? `At most ${units(DISCORD_LIMIT.description)} UTF-16 units after substitution. Empty uses Canary’s fixed lines.` : 'Leave the title and text empty for Canary’s fixed message. With only a title, only the title is sent.')
              : 'Empty uses each node’s result, one line per node.'} />
          {family === 'email' ? <TextAreaField mono optional label="HTML" value={fields.html} onChange={value => onChange({ html: value })} rows={4} disabled={saving} hint="Substituted values are HTML-escaped. The preview shows this part as source, never rendered." /> : null}
          {titled && fields.html ? <p className="fwarn"><Glyph tone="warn" />{providerLabel} gets plain text, so saving drops the HTML template.</p> : null}
          {literal.length ? (
            <p className="fwarn"><Glyph tone="warn" /><span>Sent as typed, not replaced: {literal.map(raw => <span key={raw} className="mono tok">{raw}</span>)}. Write placeholders exactly as listed, without spaces.{later.length ? ` ${later.join(', ')} ${later.length === 1 ? 'needs' : 'need'} peer protocol ${TEMPLATE_PROTOCOL} or later. ${reports(capability, nodeName)}.` : ''}</span></p>
          ) : null}
          {errorText}
        </div>
        {preview}
      </div>
    </>
  );
}

/** Words and line breaks are the node's for these sample values; the frame around them is the dashboard's illustration. */
function TemplatePreview({ kind, family, level, embed, providerLabel, icon, fields, channelName, event, onEvent, formId }: {
  kind: string; family: TemplateFamily; level: TemplateLevel; embed: boolean; providerLabel: string; icon: ReactNode; fields: TemplateFields; channelName: string;
  event: SampleEvent; onEvent: (event: SampleEvent) => void; formId: string;
}) {
  const values = sampleValues(event, level);
  const channel = channelName.trim() || 'Unnamed channel';
  const notes: ReactNode[] = [];
  const sent: { label: string; part: DiscordPart }[] = [];
  let body: ReactNode;
  let card = false;
  if (family === 'fixed') {
    body = <div className="tpl-msg-text">{discordFixedText(event, channel)}</div>;
    notes.push('Canary’s fixed text for these sample values, with times in UTC. Discord shows it as a plain message.');
  } else if (family === 'discord') {
    const message = discordMessage(fields, event, channel, embed);
    card = message.embed;
    body = message.embed ? <DiscordCard message={message} event={event} /> : <div className="tpl-msg-text">{message.content?.shown}</div>;
    const builtIn = message.embed ? Boolean(message.title?.builtIn && message.description?.builtIn) : Boolean(message.content?.builtIn);
    if (!message.embed && builtIn) notes.push('Canary’s fixed text for these sample values, with times in UTC. Discord shows it as a plain message.');
    else if (!message.embed) notes.push('Exact words and line breaks for these sample values. Discord formats Markdown in your own wording, which the preview shows as typed.');
    else notes.push('Exact words for these sample values; the card’s edge color is the node’s. The frame is illustrative: Discord draws cards its own way and shows the time in each reader’s time zone.');
    // Only the user's parts are counted: Canary's own wording is cut to fit.
    if (message.embed) {
      if (message.title && !message.title.builtIn) sent.push({ label: 'Title', part: message.title });
      if (message.description && !message.description.builtIn) sent.push({ label: 'Text', part: message.description });
    } else if (message.content && !message.content.builtIn) sent.push({ label: 'Message', part: message.content });
    if (sent.length) notes.push(`${sent.map(entry => `${entry.label}: ${units(entry.part.units)} of ${units(entry.part.limit)} UTF-16 units`).join(' · ')}.`);
    if (message.tooLong) notes.push(<span className="fwarn"><Glyph tone="down" />With these values this alert would fail: the {message.tooLong === 'content' ? 'message' : message.tooLong === 'title' ? 'card title' : 'card text'} is over Discord’s limit, and your own wording is never cut.</span>);
    if (message.empty) notes.push(<span className="fwarn"><Glyph tone="warn" />{message.embed ? 'The card’s title and text are both empty for this event.' : 'The title and text are both empty for this event, so there’s no message and the attempt fails.'}</span>);
    if (event === 'test') notes.push(builtIn ? 'Test these settings sends this, naming the node that sends it instead of the sample one.' : 'Test these settings fills in exactly these values, with a new attempt ID and the name of the node that sends it.');
  } else {
    const message = prepareMessage(fields, values, family, level);
    if (family === 'chat' && isMessaging(kind)) {
      const fit = chatFit(kind, message);
      body = combinesTitle(kind)
        ? <div className="tpl-msg-text">{`${message.subject}\n${message.text}`}</div>
        : <><div className="tpl-msg-title">{message.subject}</div><div className="tpl-msg-text">{message.text}</div></>;
      notes.push(combinesTitle(kind)
        ? `Exact words and line breaks for these sample values. ${providerLabel} gets them as one plain-text message, the title on the first line, and styles it its own way.`
        : `Exact title and text for these sample values. The frame is illustrative: ${providerLabel} shows them in its own style.`);
      notes.push(`Title: ${fit.titleChars} of 250 characters · ${fit.combined ? 'title and text' : 'text'}: ${fit.used.toLocaleString('en-US')} of ${fit.limit.toLocaleString('en-US')} ${fit.unit}.`);
      if (!fit.titleFits || !fit.textFits) notes.push(<span className="fwarn"><Glyph tone="down" />With these values this alert would fail: it’s over {providerLabel}’s limit, and custom templates are never shortened.</span>);
    } else {
      body = (
        <>
          <div className="tpl-mail-subj"><span className="muted small">Subject</span><b>{message.subject}</b></div>
          {message.text ? <div className="tpl-msg-text">{message.text}</div> : null}
          {message.html ? <div><span className="muted small">HTML part, shown as source</span><pre className="tpl-msg-src">{message.html}</pre></div> : null}
        </>
      );
      notes.push('Exact subject and body for these sample values. The frame is illustrative: mail apps use their own layout.');
      if (message.html && message.text) notes.push(kind === 'brevo' ? 'Brevo gets only the HTML part when there is one.' :'Both parts are sent; mail apps usually show the HTML part.');
    }
    if (message.tooLong) notes.push(<span className="fwarn"><Glyph tone="down" />The {message.tooLong === 'subject' ? 'subject' : message.tooLong === 'text' ? 'text' : 'HTML'} is over the node’s limit after substitution, so this alert would fail.</span>);
    if (event === 'test') notes.push(level === 'enhanced' ? 'Test these settings fills in exactly these values, with a new attempt ID and the name of the node that sends it.' : 'Test these settings fills in exactly these values, with a new attempt ID.');
  }
  if (family === 'fixed' && event === 'test') notes.push('Test these settings sends this, naming the node that sends it instead of the sample one.');
  return (
    <section className="tpl-pv" aria-labelledby={`${formId}-pv`}>
      <div className="tpl-pv-h"><h4 id={`${formId}-pv`}>Preview</h4><Tag>Sample data</Tag></div>
      <Seg legend="Sample event" name={`${formId}-pv-event`} value={event} options={[...SAMPLE_EVENTS]} onChange={onEvent} />
      <figure className={card ? 'tpl-msg tpl-msg-card' : `tpl-msg ev-${event}`}>
        <figcaption className="tpl-msg-h">{icon}<b>{providerLabel}</b><span className="muted">· illustration</span><span className="tpl-ev"><Glyph tone={EVENT_TONE[event]} />{EVENT_NAME[event]}</span></figcaption>
        {body}
      </figure>
      {notes.map((note, index) => <p key={index} className="muted small">{note}</p>)}
      {sent.length ? (
        <details className="tpl-vals">
          <summary>Text as sent</summary>
          <p className="muted small">Filled-in values carry a backslash before each Markdown character; Discord hides it. Discord’s limits count this text.</p>
          {sent.map(entry => <div key={entry.label}><span className="muted small">{entry.label}</span><pre className="tpl-msg-src">{entry.part.sent}</pre></div>)}
        </details>
      ) : null}
      {family === 'fixed' ? null : <SampleValues values={values} names={placeholderNames(level)} />}
    </section>
  );
}

/** A Discord card as an illustration: title, text, the node's edge color for the event and the event's time in UTC. */
function DiscordCard({ message, event }: { message: DiscordMessage; event: SampleEvent }) {
  return (
    <div className={`tpl-dc-card ev-${event}`}>
      {message.title?.shown ? <div className="tpl-dc-title">{message.title.shown}</div> : null}
      {message.description?.shown ? <div className="tpl-dc-desc">{message.description.shown}</div> : null}
      {message.timestamp ? <div className="tpl-dc-time">{message.timestamp.replace('T', ' ').replace('Z', ' UTC')}</div> : null}
    </div>
  );
}

function SampleValues({ values, names }: { values: MessageValues; names: readonly AnyPlaceholderName[] }) {
  return (
    <details className="tpl-vals">
      <summary>Sample values</summary>
      <p className="muted small">Made up for this preview. Real alerts fill in the monitor, its incident and the latest result from each of its nodes, so they can be longer.</p>
      <dl>
        {names.map(name => (
          <div key={name}>
            <dt className="mono">{token(name)}</dt>
            <dd><span className="tpl-val">{values[name] || <span className="muted">(empty)</span>}</span><small>{PLACEHOLDER_MEANING[name]}</small></dd>
          </div>
        ))}
      </dl>
    </details>
  );
}
