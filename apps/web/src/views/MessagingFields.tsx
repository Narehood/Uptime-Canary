import type { ReactNode } from 'react';
import { href } from '../router';
import { ErrorText } from '../ui/bits';
import { CheckField, Disclosure, SecretField, Seg, SelectField, TextField } from '../ui/forms';
import { Glyph } from '../ui/icons';
import {
  DEFAULT_BASE, GOTIFY_PRIORITIES, NTFY_PRIORITIES, PUSHOVER_PRIORITIES, credentialKey, routeKey,
  type MField, type MessagingDraft, type MessagingKind, type NtfyAuth,
} from '../messaging';

/** What picks each provider's destination, and what is only a credential. Shown under its fields. */
const IDENTITY: Record<MessagingKind, ReactNode> = {
  slack: <>The URL is the credential and picks the channel. A new URL with the same <span className="mono">T…/B…</span> parts counts as a credential rotation and keeps this destination; any other URL, including a relay path, is a new destination.</>,
  telegram: <>The server, chat and forum topic pick the destination; the bot token is only the credential. An @channelname is matched by name, so if the name moves to another chat, alerts follow it.</>,
  ntfy: <>The server and topic pick the destination. Changing the login or token keeps it.</>,
  gotify: <>Gotify tokens don’t say which application they belong to, so this destination is this channel plus the server address. Replace the token only with one from the <b>same application</b>. To alert a different Gotify application, <a href={href('channels', 'new')}>create a new channel</a>; reusing this one would carry its receipts over.</>,
  pushover: <>The user or group key and the device pick the destination. Replacing the application token keeps it.</>,
};

export function MessagingFields({ kind, draft: d, patch, errors, stored, saving, formId, apiOpen, onApiToggle, previous }: {
  kind: MessagingKind;
  draft: MessagingDraft;
  patch: (change: Partial<MessagingDraft>) => void;
  errors: Partial<Record<MField, string>>;
  stored: (key: keyof MessagingDraft) => boolean;
  saving: boolean;
  formId: string;
  apiOpen: boolean;
  onApiToggle: () => void;
  /** The stored channel's draft when editing the same provider, to explain route and credential changes. */
  previous?: MessagingDraft;
}) {
  const insecure = (checked: boolean, onChange: (value: boolean) => void, label = 'Allow plain HTTP for this server'): ReactNode => (
    <CheckField label={label} checked={checked} onChange={onChange} disabled={saving} danger hint="Only for a private network you trust: credentials and the message travel unencrypted." />
  );
  const endpoint = (value: string, onChange: (value: string) => void, checked: boolean, onCheck: (value: boolean) => void, fallback: string, hint: ReactNode): ReactNode => (
    <Disclosure title="API endpoint" summary={value || `Official (${fallback.replace(/^https:\/\//u, '')})`} open={apiOpen} onToggle={onApiToggle}>
      <TextField full mono label="API base URL (optional)" value={value} onChange={onChange} error={errors.baseUrl} placeholder={fallback} disabled={saving} hint={hint} />
      {insecure(checked, onCheck)}
    </Disclosure>
  );
  const moved = previous && routeKey(kind, d) !== routeKey(kind, previous);
  const rotated = previous && !moved && credentialKey(kind, d) !== credentialKey(kind, previous);
  return (
    <>
      {kind === 'slack' ? (
        <>
          <SecretField full label="Slack webhook URL" value={d.slackUrl} onChange={value => patch({ slackUrl: value })} error={errors.slackUrl} disabled={saving} storedSet={stored('slackUrl')}
            placeholder="https://hooks.slack.com/services/T…/B…/…" hint="Your Slack app → Incoming Webhooks → the URL for one channel. Slack sets the name and icon. Accepted when Slack answers exactly “ok”." />
          {insecure(d.slackInsecure, value => patch({ slackInsecure: value }), 'Allow plain HTTP')}
        </>
      ) : null}

      {kind === 'telegram' ? (
        <>
          <SecretField label="Bot token" value={d.tgToken} onChange={value => patch({ tgToken: value })} error={errors.botToken} disabled={saving} storedSet={stored('tgToken')}
            placeholder="123456789:AA…" hint="From @BotFather." />
          <TextField mono label="Chat" value={d.tgChat} onChange={value => patch({ tgChat: value })} error={errors.chatId} placeholder="-1001234567890 or @channel" disabled={saving}
            hint="A numeric chat ID or a public @channelname. Add the bot first; in a channel it must be an admin." />
          <TextField mono label="Forum topic ID (optional)" value={d.tgThread} onChange={value => patch({ tgThread: value })} error={errors.threadId} inputMode="numeric" placeholder="main chat" disabled={saving}
            hint="For one topic in a forum group." />
          <CheckField full={false} label="Send silently" checked={d.tgSilent} onChange={value => patch({ tgSilent: value })} disabled={saving} hint="A notification without sound." />
          <CheckField full={false} label="Protect content" checked={d.tgProtect} onChange={value => patch({ tgProtect: value })} disabled={saving} hint="Blocks forwarding and saving." />
          <CheckField full={false} label="No link previews" checked={d.tgNoPreview} onChange={value => patch({ tgNoPreview: value })} disabled={saving} />
          {endpoint(d.tgBase, value => patch({ tgBase: value }), d.tgInsecure, value => patch({ tgInsecure: value }), DEFAULT_BASE.telegram,
            <>Only for a self-hosted Bot API server or relay. A path prefix is kept; <span className="mono">/bot…/sendMessage</span> is appended.</>)}
        </>
      ) : null}

      {kind === 'ntfy' ? (
        <>
          <SecretField label="Topic" value={d.ntfyTopic} onChange={value => patch({ ntfyTopic: value })} error={errors.topic} disabled={saving} storedSet={stored('ntfyTopic')}
            placeholder="canary-alerts-7f3k" hint="1–64 letters, digits, - or _. On a public server anyone who knows the topic can subscribe, so choose a hard-to-guess name or a protected topic." />
          <TextField mono label="Server URL (optional)" value={d.ntfyBase} onChange={value => patch({ ntfyBase: value })} error={errors.serverUrl} placeholder={DEFAULT_BASE.ntfy} disabled={saving}
            hint="Your own ntfy server, with any path prefix. Empty uses ntfy.sh." />
          {insecure(d.ntfyInsecure, value => patch({ ntfyInsecure: value }))}
          <Seg<NtfyAuth> legend="Login" name={`${formId}-ntfyauth`} value={d.ntfyAuth} onChange={value => patch({ ntfyAuth: value })} disabled={saving} full
            options={[{ value: 'none', label: 'No login' }, { value: 'token', label: 'Token' }, { value: 'basic', label: 'User and password' }]}
            hint={d.ntfyAuth === 'none' ? 'Publishes without credentials, for open topics.' : 'Sent only to this server, never in the address.'} />
          {d.ntfyAuth === 'token' ? <SecretField full label="Access token" value={d.ntfyToken} onChange={value => patch({ ntfyToken: value })} disabled={saving} storedSet={stored('ntfyToken')} placeholder="tk_…" /> : null}
          {d.ntfyAuth === 'basic' ? (
            <>
              <TextField label="User name" value={d.ntfyUser} onChange={value => patch({ ntfyUser: value })} disabled={saving} />
              <SecretField label="Password" value={d.ntfyPass} onChange={value => patch({ ntfyPass: value })} disabled={saving} storedSet={stored('ntfyPass')} />
            </>
          ) : null}
          {errors.ntfyAuth ? <div className="full"><ErrorText id={`${formId}-ntfyauth-e`}>{errors.ntfyAuth}</ErrorText></div> : null}
          <SelectField label="Priority" value={d.ntfyPriority} onChange={value => patch({ ntfyPriority: value })} options={NTFY_PRIORITIES} disabled={saving} hint="Higher priorities are more intrusive on phones." />
          <TextField label="Tags (optional)" value={d.ntfyTags} onChange={value => patch({ ntfyTags: value })} error={errors.tags} placeholder="warning, rotating_light" disabled={saving}
            hint="Up to 16, separated by commas. Emoji short codes show as emoji." />
        </>
      ) : null}

      {kind === 'gotify' ? (
        <>
          <TextField full mono label="Server URL" value={d.gotifyBase} onChange={value => patch({ gotifyBase: value })} error={errors.serverUrl} placeholder="https://gotify.example.com" disabled={saving}
            hint={<>Your Gotify server, with any path prefix. <span className="mono">/message</span> is appended.</>} />
          {insecure(d.gotifyInsecure, value => patch({ gotifyInsecure: value }))}
          <SecretField label="Application token" value={d.gotifyToken} onChange={value => patch({ gotifyToken: value })} error={errors.appToken} disabled={saving} storedSet={stored('gotifyToken')}
            hint={<>From Apps in Gotify. Sent only in the <span className="mono">X-Gotify-Key</span> header.</>} />
          <SelectField label="Priority" value={d.gotifyPriority} onChange={value => patch({ gotifyPriority: value })} options={GOTIFY_PRIORITIES} disabled={saving}
            hint="In Gotify’s Android app, 0 shows no notification, 1–3 an icon, 4–7 adds sound and 8–10 adds vibration." />
        </>
      ) : null}

      {kind === 'pushover' ? (
        <>
          <SecretField label="Application API token" value={d.poToken} onChange={value => patch({ poToken: value })} error={errors.appToken} disabled={saving} storedSet={stored('poToken')}
            hint="From your application on pushover.net." />
          <SecretField label="User or group key" value={d.poUser} onChange={value => patch({ poUser: value })} error={errors.userKey} disabled={saving} storedSet={stored('poUser')}
            hint="Who receives it. Both are 30 letters and digits." />
          <TextField mono label="Device (optional)" value={d.poDevice} onChange={value => patch({ poDevice: value })} error={errors.device} placeholder="all active devices" disabled={saving}
            hint="One device name." />
          <TextField mono label="Sound (optional)" value={d.poSound} onChange={value => patch({ poSound: value })} error={errors.sound} placeholder="user’s default" disabled={saving}
            hint="Such as siren or persistent, or one of your custom sounds. Pushover checks the name." />
          <SelectField full label="Priority" value={d.poPriority} options={PUSHOVER_PRIORITIES} disabled={saving}
            onChange={value => patch(value === '2' ? { poPriority: value, poRetry: d.poRetry || '60', poExpire: d.poExpire || '3600' } : { poPriority: value })} />
          {d.poPriority === '2' ? (
            <>
              <TextField mono label="Repeat every (seconds)" value={d.poRetry} onChange={value => patch({ poRetry: value })} inputMode="numeric" placeholder="60" disabled={saving} hint="30 to 10,800." />
              <TextField mono label="Keep repeating for (seconds)" value={d.poExpire} onChange={value => patch({ poExpire: value })} inputMode="numeric" placeholder="3600" disabled={saving} hint="At most 10,800 (3 hours), and at least the repeat interval." />
              <p className="full muted small">Pushover itself repeats the alert until someone acknowledges it or the time runs out. Uptime-Canary doesn’t poll the receipt or record who acknowledged it.</p>
              {errors.emergency ? <div className="full"><ErrorText id={`${formId}-emerg-e`}>{errors.emergency}</ErrorText></div> : null}
            </>
          ) : null}
          {endpoint(d.poBase, value => patch({ poBase: value }), d.poInsecure, value => patch({ poInsecure: value }), DEFAULT_BASE.pushover,
            <>Only for a relay or test server. A path prefix is kept; <span className="mono">/1/messages.json</span> is appended.</>)}
          <p className="full muted small">Accepted means Pushover queued the message for this user or group. It isn’t proof a device showed it, and Pushover’s own rules decide which devices get it.</p>
        </>
      ) : null}

      <p className="full muted small id-note"><b>Destination.</b> {IDENTITY[kind]}</p>
      {moved ? <p className="full fwarn small"><Glyph tone="warn" />This is a new destination. Alerts already owed to the old one aren’t moved, and finished outages aren’t replayed to the new one.</p>
        : rotated && kind === 'gotify' ? <p className="full fwarn small"><Glyph tone="warn" />Only the token changes. Use a token from the same Gotify application; for another application, create a new channel.</p>
        : rotated ? <p className="full muted small">Only the credential changes: the destination and its receipts stay the same.</p>
        : null}
    </>
  );
}
