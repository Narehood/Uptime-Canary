# Local history, sample statistics and metrics

Each headless node retains its own measurements. Changing the dashboard's node
changes the history source. No endpoint below merges raw measurements from
several nodes or infers that missing measurements were healthy.

Local retention can be configured with `CANARY_HISTORY_LIMIT` (1–100,000
samples per monitor, default 1,000) and `CANARY_HISTORY_RETENTION_MS`
(1,000–31,536,000,000 ms, default seven days). Both constraints apply, so an
active monitor can reach its count limit before the age limit. These node-local
settings take effect on restart and are included in the statistics response.
They do not change replicated incident/receipt retention. The Compose service
forwards both settings; measurements removed by retention cannot be recovered
by requesting a wider time window.

## Time windows and statistics

Authenticated accounts, including viewers, can read:

```text
GET /api/monitors/ID/history?from=TIMESTAMP_MS&to=TIMESTAMP_MS&limit=1000
GET /api/monitors/ID/statistics?from=TIMESTAMP_MS&to=TIMESTAMP_MS
```

The start is inclusive and the end exclusive. Parameters are decimal integer
Unix milliseconds; duplicate, malformed and unknown parameters are rejected.
Omitting a bound leaves that side unrestricted. History filters before applying
the limit, returns newest first and preserves its existing array response.
The default limit is 100; the maximum is 1,000.

Statistics describe all retained samples in the requested window, independently
of the history response limit. They include counts of up, down, pending and
unknown samples; first and last measurement times; average latency; and exact
nearest-rank P50/P95 latency. `nodeId` identifies the source. Current retention
duration and per-monitor record limit are returned so clients can explain why
older measurements may be absent.

`latencyCount` gives the number of measured latencies used for averages and
percentiles. Samples with `latencyAvailable:false`, including unavailable ICMP
RTT and manual status observations, are excluded. A compatibility `latencyMs:0`
on such a sample must display as unavailable, not a zero-millisecond response.
Legacy observations without that flag retain their original measured latency.

`sampleAvailability` is up / (up + down). It excludes pending/unknown samples
and is null when neither up nor down was measured. It is a sample ratio, not
a time-weighted SLA: historical rows do not retain every previous scheduling
and staleness policy. An empty window has null availability and latency
statistics. Maintenance measurements remain real measurements in these counts.

The browser API client supports time windows/statistics; dashboard time-range
controls still require implementation. Full time-weighted coverage and durable
long-term rollups remain distinct release work.

## Prometheus

`GET /api/metrics` requires a management bearer token or password session.
An account with the viewer role is sufficient. Cluster credentials are not
management credentials. A static viewer token suits unattended scraping; keep
it in the scraper's credential file. The optional dashboard is not involved.

The response uses the [Prometheus text exposition format](https://github.com/prometheus/docs/blob/main/docs/instrumenting/exposition_formats.md),
with `Content-Type: text/plain; version=0.0.4; charset=utf-8` and no caching.

| Metric | Meaning |
| --- | --- |
| `uptime_canary_monitor_state` | One current state per monitor, including unknown, pending and paused |
| `uptime_canary_observation_state` | One fresh/missing state for each assigned observing node |
| `uptime_canary_observation_latency_milliseconds` | Latest fresh latency; absent for unknown/stale data |
| `uptime_canary_certificate_days_remaining` | Fresh certificate lifetime, with a verified-trust label |
| `uptime_canary_monitor_maintenance_active` | Whether planned work currently holds this monitor's alerts |
| `uptime_canary_peer_reachable` | Recent authenticated exchange with the configured peer |

Labels identify the viewing node, monitor and observing peer. Configuration
URLs, provider messages, tokens and credentials are omitted. Each metric is a
gauge; state samples have value 1 and a `state` label. A service under maintenance
can still have state down. Unknown data has no latency/certificate sample.

Example scraper configuration:

```yaml
scrape_configs:
  - job_name: uptime-canary
    metrics_path: /api/metrics
    authorization:
      type: Bearer
      credentials_file: /run/secrets/canary_viewer_token
    static_configs:
      - targets: [canary-1:3100]
```

Scrape each node separately to preserve its viewing context. This endpoint
exports current state. Retained notification attempts are not scanned while
holding the state lock just to generate scrape counters.
