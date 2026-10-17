# Connectors: webhook intake and the visual builder

An implementation of ideas from [`n8n.md`](https://onedev.evcsbrw.ru/Monitoring/Beacon/~files/main/n8n.md): stage 0, stage 1, most of stages 2 and 4, version rollback from stage 5. No n8n code, node schemas, texts or icons were used — only ideas.

## How it works

A connector is a graph of nodes (JSON with a `type_version` on every node and stable identifiers). On publishing, the graph is compiled into a `Step` pipeline; the executor walks the nodes in topological order (the order on the canvas does not affect behaviour), and every record carries the "request → item" lineage.

```
POST /api/ingest/{slug}
  → network (CIDR) → rate limit → body size → authentication → accept_if
  → write to ingest_requests (PostgreSQL) → 202
  → workers (FOR UPDATE SKIP LOCKED) → pipeline of the version the request arrived at
  → connector_events (upsert by idempotency key), ingest_failures, connector_stats
```

Definitions (connectors, drafts, versions, samples, credentials) are kept in the state snapshot, like the other Beacon objects, and move with it when migrating to another PostgreSQL database. High-frequency data lives in separate PostgreSQL tables: `ingest_requests` (daily partitions), `ingest_idempotency`, `connector_events`, `ingest_failures`, `connector_stats`; they are not copied when migrating to another database. The tables are created at startup; until they are ready, the intake address answers 503 (`ingest_unavailable`).

Retention: the request archive — 7 days, failures and statistics — 30 days, idempotency keys (`Idempotency-Key`) — 24 hours (the periods are set by the `UMBRELLA_INGEST_KEEP_*` variables, see [README](https://onedev.evcsbrw.ru/Monitoring/Beacon/~files/main/app/README.md)). Cleanup and partition creation run once an hour under an advisory lock.

## Nodes (version 1)

| Type | Purpose |
| --- | --- |
| `trigger.webhook` | credentials or anonymous access, HMAC header and prefix, allowed networks, body limit (KB) and rate limit (requests/s), `accept_if` condition in CEL |
| `parse.json` | body parsing, choosing the array of items; the document root is available to every item |
| `filter` | CEL condition; dropped records are counted |
| `route.switch` | "output — condition" rules, first match or all, `else` output |
| `map.severity` | mapping table to priorities P1 `critical` … P4 `low`, P5 `info`, default value |
| `enrich.labels` | fixed and computed labels |
| `map.event` | title, CI, signal, method, severity, status, external ID, value, deduplication key; severity synonyms (including `p1`…`p5`, `sev1`…`sev5`) and status synonyms are normalized |
| `out.event` | event output |
| `ack.response` | a custom response to the source: 2xx code, type and body |

For nodes that can reject a record, an error policy is set: `fail_record` (to the failure list), `skip`, `route_error` (the `error` output). A node can be disabled — records pass through unchanged.

Expressions: `${path}` templates with fallbacks `${a|$b|"text"}` and filters (`lower`, `upper`, `trim`, `json`, `default:"x"`, `date:"layout"`), `$${` is the escape. Conditions are CEL (`event`, `request`, `connector`) with a cost limit, string and encoder extensions, `in_cidr`, `sha256`. Expression preview runs on the server with the same engine.

## Credentials

Types: Bearer token (`Authorization: Bearer` or `X-Umbrella-Token`), Basic, header with a secret, HMAC-SHA256. Secrets are written only to OpenBao (`credentials/<id>`), the API never returns them, and headers with secrets do not get into the request archive or samples. The "Credentials" page, permissions `credentials:view` and `credentials:edit`.

The catalog holds both **incoming** secrets (connectors use them to check requests from sources) and **outgoing** ones (monitoring systems and metric sources use them to sign in to Zabbix and Prometheus). The "Used by" column lists all consumers: connectors (the draft or any version), monitoring systems and metric sources — with links. Credentials in use cannot be deleted: the `409 credential_in_use` response contains `used_by` — a list of `{kind, id, name}`, where `kind` is `connector`, `monitoring` or `metric_source`.

## Quick source connection

The **Connect a source** button on the "Connectors" page (and on "Monitoring systems", and at `/monitoring?connect=1`) does in one step what used to require three pages:

1. creates a Bearer token (credentials "Intake token: …");
2. creates a connector from the Zabbix, Alertmanager, Grafana, Graylog, Current or generic webhook template with this token (the address is derived from the name; if it is taken, a number is appended);
3. publishes it.

The response shows the intake address (the public Beacon address from the alerting settings, or the address the interface was opened at) and the token — **once**; after that it is stored only in OpenBao. For Zabbix you get the [`beacon-mediatype.yaml`](https://onedev.evcsbrw.ru/Monitoring/Beacon/~files/main/deploy/zabbix/beacon-mediatype.yaml) media type with the address and token already filled in, for Alertmanager — a ready `alertmanager.yml` receiver, for Grafana — contact point parameters, for Current — the address and token for its settings ([current.md](current.md)), for webhook — a `curl` example. From the "Alert intake" tab of a monitoring system, the connector immediately becomes that system's intake.

`POST /api/connectors/quick` (permissions `connectors:edit`, `connectors:publish`, `credentials:edit`; with `monitoring_id` — also `monitoring:edit`): body `{preset: "zabbix"|"alertmanager"|"grafana"|"graylog"|"network-flow"|"webhook", name?, monitoring_id?}`, response `{connector, credential_id, ingest_url, token, instructions: {preset, ingest_url, auth_header, snippet?, snippet_kind?}, mediatype_yaml?, monitoring_id?}`. If a step fails, whatever was created before it is deleted.

## Test event

The **Send a test event** button (in the editor of a published connector, after a quick connection and on the "Alert intake" tab of a system) puts a request in the connector template's format into the intake queue (for a custom format — the first sample), and the workers run it through the published version exactly like a real delivery. The resulting event gets the title "Тестовое событие Beacon" ("Beacon test event"; the title is fixed in the code), the labels `umbrella_test=true` and `umbrella_test_id`, its own signal (so it does not merge with real incidents) and the given CI name (`umbrella-test` by default). If the connector produced no event, one is created anyway — the test still shows the route.

A test incident is not sent to PagerDuty (state "skipped"), fallback notification channels skip it by the `umbrella_test` label, and after 5 minutes it resolves itself (reason "the test event is over"). The interface shows the incident number, the CI and the route: business services, the team and people.

`POST /api/connectors/{id}/test-event` (permissions `connectors:edit` or `monitoring:edit`): body `{ci?}`, response `{incident_id, request_id, status, error?}` — waits up to 15 seconds for the incident; `status: failed` means the connector could not process the request. An external request cannot pass itself off as a test: the service headers `X-Umbrella-Test*` are dropped at the intake address.

## Editor

The "Connectors" page (`/connectors`), the editor — `/connectors/{id}`:

- @xyflow canvas: a palette of implemented nodes, drag and drop, links via named outputs, deletion, undo and redo (Ctrl+Z / Ctrl+Shift+Z);
- inspector: forms built from parameter descriptions (strings, templates, CEL, paths, numbers, flags, lists, tables, credential selection), node problems, expression preview on the node input;
- draft autosave with a revision number (a conflict between two windows is detected) and an edit lock (60 s lease, extended while the editor is open);
- publishing with compilation (errors go to the "Problems" tab), stopping, versions with restore into the draft, export and import with credential slots;
- samples: capturing real requests to the connector address (up to 20 requests, up to 60 minutes, works even before publishing), manual input, saving from the request archive, template samples;
- test: running a sample, running up to a chosen node, running all samples (regression before publishing); the input and output of every node (JSON or table), counters on nodes and links, pinning a node output, marking stale results after edits; a test writes nothing;
- the "Requests", "Failures" (reprocessing selected or all of them on the current version), "Events", "Statistics" tabs.

Templates: Prometheus Alertmanager, Grafana Alerting, Graylog, Current (`network-flow`, NetFlow / IPFIX / sFlow traffic events — [current.md](current.md)), Zabbix (webhook), generic webhook — with built-in samples.

A template is a `backend/internal/presets/*.json` file in the connector export format. The optional `quick` object makes it available to quick connection: its order in the dialog (`order`), the default connector name (`name`, en/ru), the kinds of monitoring systems (`monitoring_kinds`), the number of source setup steps (`steps`, their text is in the interface strings `src.steps.<id>.<n>`), a setup snippet (`snippet`) and a file to import (`attachment`) — Go `text/template` templates with `{{.URL}}` and `{{.Token}}` (functions `quote`, `yaml`, `json`) — and the test event body (`test_body`, with `{{.Nonce}}`, `{{.CI}}`, `{{.Title}}`). A template with `starter` defines the graph of a new connector created without a template.

Permissions of the `connectors` page: `view`, `edit` (drafts, samples, tests), `publish` (publishing, stopping, reprocessing), `payload` (request bodies, samples, failed records).

## API

| Method and path | Purpose |
| --- | --- |
| `POST`, `PUT /api/ingest/{slug}` | intake |
| `GET`, `POST /api/connectors` | list (with 24 h counters), creation (from a template) |
| `POST /api/connectors/quick` | quick connection: token, connector from a template, publishing |
| `POST /api/connectors/{id}/test-event` | test event through the published version |
| `GET /api/connectors/node-types`, `/presets`, `/credentials` | node registry, templates, credential selection |
| `GET /api/credentials/kinds` | credential types with their fields and secrets |
| `POST /api/connectors/preview` | preview of a template, CEL or path |
| `POST /api/connectors/import/check`, `/import` | export parsing and import |
| `GET`, `PUT`, `DELETE /api/connectors/{id}` | connector |
| `PUT /api/connectors/{id}/draft` | saving the draft (`revision`) |
| `POST`, `DELETE /api/connectors/{id}/lock` | edit lock |
| `POST /api/connectors/{id}/publish`, `/unpublish` | publishing, stopping |
| `GET /api/connectors/{id}/versions/{n}`, `POST …/restore` | version, restore |
| `GET /api/connectors/{id}/export` | export (`?version=`, `?samples=true`) |
| `…/samples`, `…/capture` | samples, capture |
| `POST …/test-run`, `…/test-all` | test runs |
| `GET …/requests`, `…/failures`, `…/events`, `…/stats`; `POST …/failures/reprocess` | archive, failures, events, statistics |
| `/api/credentials` | credentials |

## What is not done yet from the plan

- Stage 2: sampled traces of production requests and the event → trace link; dragging a field into a parameter and path autocompletion; dry-run of CI binding.
- Stage 3 entirely: `trigger.poll`, a durable scheduler, cursors, retries and a circuit breaker for external calls, connector self-monitoring, Prometheus/OpenTelemetry metrics.
- Stage 4: `enrich.lookup`, lookup tables, `vars.*` variables, fragments.
- Stage 5: scopes and owning teams, version diff, publication approval, tag filters, declarative source descriptions.
- Stage 6 — on request.
- Several Beacon instances: the workers are already safe (`SKIP LOCKED`), but the rate limit is counted in each process separately.
