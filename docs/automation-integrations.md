# Automation and chat integrations

Peer protocol 22 adds Home Assistant events, Matrix notices and per-channel
event selection. Upgrade all cluster nodes together: older protocols cannot
exchange these channel resources or their delivery receipts.

Create a channel in **Notifications → New channel**, select a provider, save it,
then add it to the monitors that should use it. A default channel is selected
for new monitors only. API requests below use an admin or editor bearer token.
Provider credentials are stored with channel configuration and replicated to
trusted cluster nodes; viewer responses hide the configuration.

## Home Assistant: a site outage turns on a light

The provider fires a named event using Home Assistant's authenticated
[REST events API](https://developers.home-assistant.io/docs/api/rest/).
Enter the instance base URL (for example `https://home.example.net`), a
long-lived access token from the Home Assistant profile, and an event type
(default `uptime_canary`). A reverse-proxy path prefix is supported. Use the
instance base URL, without `/api/events`. Plain HTTP requires explicit opt-in
and sends the token unencrypted; use it only on a trusted local network.

For example, POST this body to `/api/notification-channels`:

```json
{
  "name": "Home Assistant",
  "enabled": true,
  "events": ["down", "recovery"],
  "config": {
    "kind": "homeassistant",
    "homeassistant": {
      "baseURL": "https://home.example.net",
      "accessToken": "REPLACE_WITH_HOME_ASSISTANT_TOKEN",
      "eventType": "uptime_canary"
    }
  }
}
```

The event data contains `kind` (`down`, `repeat`, `recovery` or `test`),
`monitor_id`, `monitor_name`, `incident_id`, `idempotency_key`, `title` and
`message`. The last two fields use the channel's message templates. Event
metadata stays separate from templates, so custom wording cannot change a
trigger's monitor ID or alert kind. Test events have no real monitor/incident.

Add this automation in Home Assistant's YAML editor, replacing the monitor ID
and light entity. Use the same event type as the channel:

```yaml
alias: Canary outage light
triggers:
  - trigger: event
    event_type: uptime_canary
    event_data:
      monitor_id: REPLACE_WITH_MONITOR_ID
      kind: down
    id: outage
  - trigger: event
    event_type: uptime_canary
    event_data:
      monitor_id: REPLACE_WITH_MONITOR_ID
      kind: recovery
    id: recovery
actions:
  - choose:
      - conditions: "{{ trigger.id == 'outage' }}"
        sequence:
          - action: light.turn_on
            target:
              entity_id: light.monitor_warning
    default:
      - action: light.turn_off
        target:
          entity_id: light.monitor_warning
mode: queued
```

`test` is deliberately absent from these triggers. A channel test still fires
a real Home Assistant event and can activate other automations listening for
that kind. Provider acceptance confirms the event fired, not the light's state.
Home Assistant does not deduplicate events: delivery retries and disconnected
HA nodes can produce duplicates. Prefer actions such as `turn_on`/`turn_off`
that are safe to repeat; avoid `toggle`. The `idempotency_key` is stable across
retries if your automation needs its own deduplication.

## Matrix

Use the homeserver base URL, an access token for a bot/user that has already
joined the room, and its internal room ID (`!room:example.org`, not a `#alias`).
Only **unencrypted rooms** are supported. The adapter sends a plain `m.notice`
using the [Matrix room send API](https://spec.matrix.org/latest/client-server-api/#put_matrixclientv3roomsroomidsendeventtypetxnid).
There is no login, room joining, encryption or key storage in Canary.

```json
{
  "name": "Matrix operations",
  "enabled": true,
  "config": {
    "kind": "matrix",
    "matrix": {
      "baseURL": "https://matrix.example.org",
      "accessToken": "REPLACE_WITH_MATRIX_TOKEN",
      "roomId": "!operations:example.org"
    }
  }
}
```

Title and text are joined with a newline. No HTML is sent and mentions are
disabled. The durable attempt ID becomes the Matrix transaction ID, allowing
the homeserver to deduplicate retries for the same access token/device.
Rotating credentials keeps Canary's destination receipts, but the homeserver's
deduplication scope can change. HTTP 200 must contain an `event_id`; a generic
success page is not accepted as a delivered notice. Rate limits honor both
`Retry-After` and Matrix's `retry_after_ms` response.

## Event selection and delivery

Channel input optionally accepts `events`, a nonempty array containing `down`,
`repeat`, and/or `recovery`. Omission keeps all three for existing clients.
Repeats require down alerts, and still require the monitor's repeat interval.
A recovery-only channel is supported; it sends only for an outage where that
destination was configured and observed. Adding a channel after recovery does
not invent a recovery notification. Changing the selected events affects
future eligible sends, including queued work; it does not replay receipts.
Tests remain an explicit real send regardless of the selection.

Maintenance and dependency holds continue to apply. Independent destinations
keep separate receipts, retry schedules and failover. Rotating provider tokens
keeps destination identity; changing a Home Assistant event/instance or Matrix
room/homeserver creates a new route whose receipts do not move.

For long-term measurement storage and dashboards, see
[Prometheus and Grafana](external-observability.md).
