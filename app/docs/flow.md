# Integration with Umbrella Flow

Umbrella Flow is a companion application for traffic analysis over NetFlow, IPFIX and sFlow (in the spirit of Akvorado and
ElastiFlow). The integration is two-way and optional:

- **Flow → Umbrella.** The Flow detector sends traffic events (link utilization, DDoS, anomalies,
  silent exporters) to a connector built from the `network-flow` template; Umbrella turns them into incidents and handles
  them as usual: collapsing, maintenance windows, route, PagerDuty, fallback notification.
- **Umbrella → Flow.** Incident and CI cards get a "Traffic" link to the Flow console, filtered by the
  CI and with a time window around the incident.

```mermaid
sequenceDiagram
  participant F as Umbrella Flow
  participant U as Umbrella
  participant P as On-call engineer
  F->>U: POST /api/ingest/<address> (Bearer token), firing / resolved events
  U->>U: CI by source_ref, name or IP; incident, route, PagerDuty
  P->>U: incident card → "Traffic"
  U-->>P: 302 to <Flow address>/go/incident?ci=…&ip=…&ref=…&from=…&to=…
```

## Flow → Umbrella: events

### Connecting

1. In Umbrella: "Connectors" (or "Monitoring systems") → **Connect a source** → "Umbrella Flow" →
   "Connect". Umbrella creates a Bearer token and a connector from the `network-flow` template, and publishes it.
2. Copy the intake address and the token (the token is shown once) into the Flow settings (the "Umbrella" section);
   Flow stores the token in OpenBao.
3. The "Send a test event" button checks the path to an incident and shows the route.

The same via the API: `POST /api/connectors/quick` with the body `{"preset": "network-flow"}` (permissions `connectors:edit`,
`connectors:publish`, `credentials:edit`), see [connectors.md](connectors.md#quick-source-connection).

### Event format

A single object or an array of objects:

```json
{
  "id": "flow-7f3a1c",
  "status": "firing",
  "signal": "use.link.utilization",
  "method": "USE",
  "severity": "warning",
  "title": "Link utilization xe-0/0/1 on edge-01 — 93.5 %",
  "ci": "edge-01",
  "source_ref": "inventory-db:dcim.device:100",
  "ip": "10.0.0.1",
  "value": 93.5,
  "labels": {"interface": "xe-0/0/1", "direction": "in"},
  "links": {"flow": "https://flow.example.com/visualize?exporter=10.0.0.1&if=xe-0/0/1"},
  "starts_at": "2026-10-12T09:00:00Z",
  "ends_at": ""
}
```

| Flow field | Umbrella event field | Note |
| --- | --- | --- |
| `id` | external ID, deduplication key | resolved with the same `id` closes the firing event |
| `status` | status | `firing` or `resolved`; empty — firing |
| `signal` | signal | `use.link.utilization`, `use.link.commit`, `red.ddos.volumetric`, `red.ddos.syn`, `red.traffic.anomaly`, `red.traffic.drop`, `use.exporter.silent`, `sec.threat.contact` and others |
| `method` | RED / USE method | without the field — by the signal prefix `red.` / `use.`; otherwise "other" |
| `severity` | severity | `critical` (P1), `error` (P2), `warning` (P3), `low` (P4), `info` (P5) and synonyms; unknown — P3 |
| `title` | title | empty — the signal |
| `ci`, then `ip`, then `source_ref` | CI name in the event | see "How the CI is found" |
| `value` | value | |
| `labels` | labels | kept as is |
| `source_ref`, `ip`, `links.flow` | labels `source_ref`, `ip`, `flow_url` | plus the label `source=network-flow` |
| `links.flow`, `ip`, `source_ref`, `starts_at`, `ends_at` | incident fields | "Трафик в Umbrella Flow" (Traffic in Umbrella Flow), "IP", "Inventory DB", "Начало" (Start), "Окончание" (End) — the field names are fixed in the template in Russian; empty ones are not shown |

### How the CI is found

1. The `source_ref` label (from the `source_ref` field) matches the Inventory DB reference of exactly one CI — that CI
   is taken. CIs loaded from Inventory DB have the reference ([inventory-db.md](inventory-db.md)).
2. Otherwise — the usual name lookup by the `ci` field (ID, name, short name, IP, DNS name, aliases, manual
   host links).
3. The name found no CI (or found several) — the `ip` label, if it is the IP address of exactly one CI.

Rules 1 and 3 work for any connector that sets the `source_ref` and `ip` labels. An incident without
a CI waits for one, like the others: once the CI appears (an Inventory DB load, creation from the card), the incident is
bound to it.

## Umbrella → Flow: the "Traffic" link

"Settings → Alerting" → the **Umbrella Flow address** card (next to Grafana): the public address of Flow,
for example `https://flow.example.com`. As long as the address is not set, there are no links anywhere.

With the address:

- incident card — the "Traffic" button to `/go/incidents/{id}/flow`;
- CI card — the "Traffic" row to `/go/cis/{id}/flow`;
- PagerDuty (the "Traffic in Umbrella Flow" link) and the fallback notification (e-mail, Telegram, Teams, Zoom —
  the "Traffic" link) — via the public Umbrella address, like the Grafana link.

Umbrella answers with a redirect to the Flow console:

```
<Flow address>/go/incident?ci=<CI name>&ip=<IP>&ref=<source_ref>&from=<RFC 3339>&to=<RFC 3339>
```

| Parameter | Incident | CI |
| --- | --- | --- |
| `ci` | CI name from the catalog; without a CI — the name from the event | CI name |
| `ip` | the first address of the CI; without it — the `ip` label of the event | the first address of the CI |
| `ref` | the Inventory DB reference of the CI; without it — the `source_ref` label of the event | the Inventory DB reference of the CI |
| `from` | 30 minutes before the incident was opened | an hour ago |
| `to` | now; for a resolved one — 10 minutes after resolution (no later than the current moment) | now |

Empty `ip` and `ref` are not passed. Access works like the Grafana link: a signed-in user with the
`incidents:view` permission and the incident within their visibility scope (for a CI — the `cis:view` permission) goes to Flow,
others go to the incident (`/incidents?id=…`) or the CI (`/cis?id=…`) in Umbrella. A click from PagerDuty, e-mail
or a messenger arrives without the session cookie (SameSite=Strict): Umbrella returns a short page
that repeats the request from its own address.

## API

| Method | Path | Permission | Purpose |
| --- | --- | --- | --- |
| GET | `/api/flow` | `settings.alerting:view` | `{url, updated_at, updated_by}` |
| PUT | `/api/flow` | `settings.alerting:edit` | `{url}`: http or https without parameters, a trailing `/` is dropped; empty — turn the links off; error `url_invalid` |
| GET | `/go/incidents/{id}/flow` | sign-in + `incidents:view` | redirect to the incident's traffic |
| GET | `/go/cis/{id}/flow` | sign-in + `cis:view` | redirect to the CI's traffic for the last hour |
| POST | `/api/connectors/quick` `{"preset":"network-flow"}` | `connectors:edit`, `connectors:publish`, `credentials:edit` | quick connection of Flow |

The responses of `GET /api/incidents/{id}` and of CIs (`GET /api/cis`, `GET /api/cis/{id}`) contain `flow_url` (the
`/go/…/flow` path) when the Flow address is set. A change of the address is written to the audit log (`settings.flow`).
