# Long-term data with Prometheus and Grafana

Canary exports authenticated current measurements at `GET /api/metrics`.
Prometheus scrapes and retains those measurements; Grafana queries Prometheus.
Neither service is required for monitoring or notification delivery. There is
no Grafana credential in Canary, no direct Grafana push endpoint, and no
automatic installation or change to your running stack.

1. Create a dedicated Canary **viewer** account with a static token. Store
   only its token in a file readable by Prometheus, mounted at
   `/run/secrets/canary_viewer_token`. Do not use the cluster or admin token.
2. Copy [the scraper example](../deploy/observability/prometheus.yml) into
   your Prometheus configuration. Replace the target with your node's HTTPS
   host and port. Scrape the node directly, rather than the dashboard's
   browser/session gateway. Add each cluster node as a separate target.
   Multiple credentials need separate scrape jobs. For a trusted internal
   HTTP deployment, explicitly change `scheme` and the port.
3. Set retention on Prometheus to your storage budget, for example
   `--storage.tsdb.retention.time=180d --storage.tsdb.retention.size=10GB`.
   These are example limits for your external server, not an estimate of
   this workload's disk usage. Observe actual storage before increasing them.
4. Add a Prometheus data source in Grafana and import
   [the dashboard](../deploy/observability/grafana-dashboard.json).
   Alternatively provision [the datasource](../deploy/observability/grafana-datasource.yml)
   under Grafana's `provisioning/datasources`, replacing its URL. Select it in
   the dashboard's data source variable.

To keep data in Grafana Cloud, Mimir, VictoriaMetrics or another compatible
external store, configure the **scraper's** `remote_write` section with that
service's write URL and credentials. For example, when the service uses
basic authentication:

```yaml
remote_write:
  - url: https://REPLACE_WITH_SERVICE_WRITE_ENDPOINT
    basic_auth:
      username: REPLACE_WITH_SERVICE_USER
      password_file: /run/secrets/metrics_remote_write_password
```

Use your service's prescribed authentication and endpoint; the viewer token
only authenticates scraping Canary. No paid account or real provider is
contacted by repository tests. Configuration follows the official
[Prometheus configuration](https://prometheus.io/docs/prometheus/latest/configuration/configuration/)
and [Grafana provisioning](https://grafana.com/docs/grafana/latest/administration/provisioning/)
contracts.

## Reading the data correctly

The exporter keeps `view_node`, `monitor_id` and, for measurements, `probe_node`.
Choose a viewing node rather than summing duplicate cluster views. Scrape
targets may also have Prometheus `job` and `instance` labels. Monitor URLs,
credentials, monitor names and check messages are not metric labels.

State metrics use value **1** for the current `state` label; they are not a
numeric up/down flag. A state transition removes the previous label series
at the next scrape. `unknown`, `pending` and `paused` are distinct from `up`.
Scrape `up` describes exporter reachability, not monitored-site health.
Unknown/stale observations have no latency or certificate-lifetime sample.
Maintenance is separate from health: a monitor can be down during planned work.

The dashboard shows state timelines, fresh latency, certificate days,
maintenance, peer reachability, current monitor counts and exporter scrape
reachability (separate from monitored-site health). It deliberately does not label a count of
scraped samples as SLA uptime. Scrapes can miss short transitions and retain
only data collected after scraping starts. They do not backfill Canary's
SQLite history. For Canary's local observed-time coverage and full metric
definitions, see [observability](observability.md) and
[availability history](availability-history.md).
