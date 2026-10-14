# Umbrella MVP: the application

Backend in Go, web interface in React and TypeScript, secrets in OpenBao, state and streaming data in PostgreSQL. The MVP architecture documents are kept separately from the repository.

Connectors, webhook intake and the visual builder following the plan from [`n8n.md`](../n8n.md): [docs/connectors.md](docs/connectors.md).

Sign-in through Microsoft Entra ID: [docs/entra-id.md](docs/entra-id.md).

Mapping LDAP / AD and Entra ID groups to roles and teams: [docs/group-mapping.md](docs/group-mapping.md).

Umbrella Identity — passkeys (Windows Hello, Touch ID / Face ID, Android, security keys), sign-in with Google, Apple
and OpenID Connect, and single sign-in with user, role, password policy and passkey synchronization for Umbrella Flow,
Inventory DB and Network Control: [docs/identity-federation.md](docs/identity-federation.md).

Zabbix and Prometheus hosts matched to CIs: [docs/monitoring-hosts.md](docs/monitoring-hosts.md).

Fallback notification to Microsoft Teams and Zoom: [docs/teams-zoom.md](docs/teams-zoom.md).

Optional integration with Inventory DB (devices as CIs, alert states sent back): [docs/inventory-db.md](docs/inventory-db.md).

Optional integration with Umbrella Flow (NetFlow / IPFIX / sFlow traffic events as incidents, a "Traffic" link from the incident and the CI, visibility scopes for Flow users, dependency proposals on the CMDB map, the Grafana "CI traffic" dashboard): [docs/flow.md](docs/flow.md).

## What works

Pages are grouped in the menu into the sections Overview, Automation, Organization and Settings; each page is visible only with the `<page>:view` permission.

| Page | What it does | Function |
| --- | --- | --- |
| Setup wizard | opens on the first start: setup code, default language, theme and time zone, connection to OpenBao (token or AppRole) and PostgreSQL with a check (a database with an already saved Umbrella state can be connected), LDAP / AD if wanted, the Umbrella address (prefilled from the browser address), the first administrator with a password policy (collapsed by default), a final check. The setup code is written to the server log and to the `setup-token` file; after completion the file is deleted and the wizard no longer opens | — |
| Incidents | while the installation is not yet ready to deliver incidents — the "First steps" checklist on the empty page (see "Quick start"); incidents collapsed by CI and signal; tiles: active by severity, not accepted by PagerDuty (only when PagerDuty is on; without it the PagerDuty tile and column are hidden and the state is shown with a grey "PagerDuty not used" label), fallback notification, in a maintenance window, without a CI; search by ID, title, CI, signal, service and team; filters by status, severity, RED/USE method, team and service (kept in the page address); bulk acknowledge and resolve (with confirmation and a warning if the sources are still firing); refresh every 10 seconds; a card with the tabs Overview, Timeline and Sources: CIs and business services are links to their cards, "Open in NetBox", the "Service links" block (runbooks, dashboards), the route (service → team → people or the people responsible for the CI), the PagerDuty state with a link to the incident, fallback notification, the name and period of the maintenance window with a link to it, the related incident, a comment right on the Overview tab, links to the context in Grafana and to the traffic in Umbrella Flow ("Traffic", [docs/flow.md](docs/flow.md)); the "Maintenance window for this item…" button opens the window editor with the CI and a duration of 1 h (`/maintenance?new=1&ci=…`, permission `maintenance:edit`); an incident whose CI is not in the catalog has "Create item" (name, IP from the event and labels, type, optional binding to a service — `services:edit`) and "Bind to an existing configuration item" (the name from the event becomes an additional name of the CI), permission `cis:edit`: the incident and other incidents with this name get the CI and the route at once; "Resolve" asks for confirmation, offers a comment and warns that firing sources will reopen the incident within 10 minutes | F-05, F-13 |
| CMDB map | business services, their dependencies and CIs with the state of each element and the reasons; firing events for the last 24 hours per CI; "Proposed dependencies" from Umbrella Flow traffic with Accept / Reject ([docs/flow.md](docs/flow.md#flow--umbrella-dependency-proposals)) | F-04, F-07 |
| Configuration items | all CIs (devices, VMs, services, others) with search and filters; creating, changing and deleting CIs, including in NetBox; registering CIs in NetBox; responsible people, addresses, NetBox attributes; "Other names in events" (aliases, also for CIs from NetBox: events with these names are bound to the CI); the card opens via the link `/cis?id=<CI>`; "Present in" (NetBox, AD, monitoring systems); bulk binding of CIs to business services; "Traffic" — the last hour of the CI in Umbrella Flow, if its address is set | F-04, F-07 |
| Business services | services with the owning team and supporting teams, criticality, status, tags, links and dependencies; binding CIs (one by one and in bulk); linking a service to a NetBox tag; the card opens via the link `/services?id=<service>` | F-04, F-07 |
| Maintenance windows | creating, changing, finishing early and deleting windows on CIs and business services; incidents of the window's targets are kept but are not sent to PagerDuty or fallback notification; `/maintenance?new=1&ci=<CI>&service=<service>&title=…&hours=…` opens a new window with the targets filled in, `/maintenance?id=<window>` shows a window | F-10 |
| TV wallboards | pages for the big screens of the on-call shift: `/tv/<address>` opens without signing in, but only from allowed subnets or addresses (CIDR or a single IP, IPv4 and IPv6); each wallboard has its own filters: a set of CIs, business services and teams (union), severity, RED/USE method, showing acknowledged incidents, those suppressed by windows and recently resolved ones; incidents are ordered by severity (open before acknowledged), then by the time they started (newest or oldest first); theme, language, clock time zone, refresh period; when space runs out, pages flip by themselves; the default address is random (160 bits), the "New address" button issues a new one and disables the old link; trusted proxies are set on the same page (in addition to `UMBRELLA_TRUSTED_PROXIES`); permissions `wallboards:view`, `wallboards:edit` and `wallboards:proxies`; preview in the interface | — |
| Connectors | list of connectors with counters; builder: node palette, canvas, inspector, a draft with autosave and locking, publication and versions, export and import, samples and request capture, test runs; the tabs Requests, Errors (with reprocessing), Events, Statistics. Details — [docs/connectors.md](docs/connectors.md) | F-01, F-03, F-08 |
| Credentials | created here or with the "Create token" button in the connector dialog; Bearer token, Basic, a header with a secret, HMAC-SHA256; secrets are written to OpenBao at once and are never shown again; used by connectors, monitoring systems and metric sources | F-14 |
| RED/USE rules | creating, changing, deleting, enabling and disabling rules; metric source (a Prometheus-compatible HTTP API with credentials), PromQL query, condition and threshold, `for` duration, interval, severity, CI label, title template; checking against live data; templates for node_exporter, cAdvisor and HTTP services; current series and firings | F-06 |
| NetBox | connection (token in OpenBao) and check; loading devices, VMs and services as CIs and contacts as the people responsible for CIs on a schedule and with a button; accounts for contacts without an account — only with the "Create accounts for contacts" switch (off for new connections); default values for registering CIs in NetBox; reconciliation with the domain controller's computers | F-04 |
| Monitoring systems | connecting Zabbix and Prometheus-compatible servers, loading host lists and matching them to CIs, manual binding, creating CIs from hosts (one by one and in batches). Details — [docs/monitoring-hosts.md](docs/monitoring-hosts.md) | F-04 |
| Users | local accounts and directory accounts (LDAP / AD, Entra ID); creating local ones, changing the role, teams and scope, locking, changing the password, deleting | F-15 |
| Teams | team hierarchy: name, description, parent team, lead, members; a person can belong to several teams | F-15 |
| Roles | the built-in roles "Administrator" (all permissions) and "User" (no permissions); five ready-made roles — "Viewer", "On-call engineer", "Service owner", "Integration administrator", "NOC lead" (created once, they can be changed and deleted); custom roles from the permission catalog; a permission matrix by page, role members; "Role for new users" (by default "Viewer") for accounts from LDAP / AD, Entra ID and NetBox. Each user has exactly one role. Details — [docs/access.md](docs/access.md) | F-15 |
| Settings → Alerting | PagerDuty: enabling, US/EU region, an Events API v2 key or choosing a PagerDuty service through the REST API, API token, severity threshold, routes by team and business service, a Webhooks v3 subscription with one button (the signing secret in OpenBao), token check, a test event; fallback notification by e-mail (SMTP), Telegram, Microsoft Teams and Zoom (incoming webhooks, [docs/teams-zoom.md](docs/teams-zoom.md)) with additional addresses, a delay ("Automatically" — 2 minutes with PagerDuty on and at once without it; "At once" or 1–60 minutes) and a lowest severity (by default "P2 · High"), a sending check; on a new installation the PagerDuty severity threshold is "P2 · High"; the "Umbrella address" card (the public address for links and PagerDuty webhooks) at the top of the page; the Grafana dashboard and the context window; the Umbrella Flow address for the "Traffic" links ([docs/flow.md](docs/flow.md)). Users set their own Telegram chat in their profile | F-09, F-11, F-12 |
| Settings → System status | the "First steps" checklist while it is not completed; version and build, process and memory, OpenBao, PostgreSQL, LDAP / AD, Entra ID, data intake by connectors, number of objects, recent warnings and errors from the log; changing the default theme, language and time zone (`status:defaults`) | F-11 |
| Settings → Sign-in and Umbrella Identity | the setup guide; passkeys (on/off, passkey domain); Google Workspace, Sign in with Apple and any OpenID Connect provider (allowed e-mail domains, existing accounts only, groups claim); connected applications (a three-step wizard: application → access by everybody, roles or directory groups → one-time connection key), last synchronization, new key, disconnection. Users register passkeys in their profile. Umbrella is the OpenID Connect provider of the family (`/.well-known/openid-configuration`, PKCE required) and serves the federation API (`/api/identity/v1/config`, `/users` with ETag) | F-15 |
| Settings → LDAP / AD / Entra ID | sign-in through LDAP / AD (with an administrators group) and through Microsoft Entra ID, connection check, the "Groups → roles and teams" table with scheduled synchronization | F-15 |
| Settings → PostgreSQL | the current connection and its check, database statistics and `pg_stat_statements`, moving a state snapshot to another PostgreSQL database with switching to it on the fly (incident and intake tables are not copied) | — |
| Settings → OpenBao | the current connection and its check, moving secrets to another OpenBao installation | F-14 |
| Settings → Password policy | password requirements for local accounts and their expiry | F-15 |
| Settings → Service accounts | machine identities (Umbrella Flow, scripts) with a role, teams and a visibility scope like users; API tokens `umb_…` with a name and an optional expiry, shown once, stored only as a keyed hash (HMAC key in OpenBao), last use time and address, revocation; creation, use from a new address and revocation are audited. Details — [docs/access.md](docs/access.md#service-accounts-and-api-tokens) | F-15 |
| Profile | personal data, avatar, password change, Telegram chat for fallback notification, time zone | — |

Processing in the backend:

- **Secrets.** The only store is OpenBao (KV v2, default mount `umbrella`). Everything entered in the interface or API (credential secrets, NetBox tokens, PagerDuty keys and tokens, the SMTP password, the Telegram token, the Zoom verification token, the Entra ID secret, the PostgreSQL password) is written to OpenBao at once; the application keeps only an `openbao://…` reference. The password hashes of local users are kept there too (PBKDF2-SHA256, path `users/<id>`). Values are read when used and cached for 30 s, so rotation in OpenBao needs no changes in Umbrella. Sign-in by AppRole or with a token, token renewal. When credentials are deleted, their secret is deleted from OpenBao.
- **State storage.** PostgreSQL is required. The configuration (CIs, services, teams, users, roles, connectors, credentials, rules, windows, TV wallboards, settings, audit log) is stored as a snapshot in the `umbrella_state` table (current and previous versions, written in one transaction) and saved every 2 seconds and on shutdown. If the current snapshot cannot be read, the previous one is used; if neither can be read, the start stops with an error and the snapshot is not overwritten. Incidents and their timeline are in the `alerts`, `alert_timeline`, `alert_keys` tables; resolved incidents are deleted after 90 days; connector requests and events are in their own tables (see [docs/connectors.md](docs/connectors.md)). Only `umbrella.json` (the connection to OpenBao and PostgreSQL; the PostgreSQL password as a reference to OpenBao), `setup-token` until the wizard is completed, and `sessions.gob` — the sessions, so users stay signed in after a restart — remain in `UMBRELLA_DATA_DIR`. On start, Umbrella waits for OpenBao and PostgreSQL up to `UMBRELLA_STARTUP_WAIT`.
- **Intake.** `POST` or `PUT /api/ingest/{slug}` checks the network, rate, size and credentials of the published connector, writes the request to PostgreSQL and answers 202 (or with the response of its `ack.response` node); processing starts right after that in background workers. A Bearer token is accepted in `Authorization: Bearer` or `X-Umbrella-Token`; Basic, a header with a secret and HMAC-SHA256 are also available.
- **Priorities.** The severity of an incident is one of five priorities, a single table for the engine, TV wallboards, the CMDB map, rules, PagerDuty and fallback notification thresholds, the interface and messages; the interface shows the priority with a word ("P1 · Critical"):

  | Priority | `severity` value in the API | Name |
  |---|---|---|
  | P1 | `critical` | Critical / Критический |
  | P2 | `error` | High / Высокий |
  | P3 | `warning` | Medium / Средний |
  | P4 | `low` | Low / Низкий |
  | P5 | `info` | Informational / Информационный |

  The API, rules, settings and events store the former names `critical`, `error`, `warning`, `info` and the new `low`, so existing data does not change. Severity counters (TV wallboards, CMDB) got a `low` field; TV wallboard data contains the `severities` priority table (name, priority, rank, color, names in two languages). Synonyms in events: `p1`…`p5` and `sev1`…`sev5` in any case; `low` — P4; `high` — P2; `average`, `medium`, `minor` — P3; `information`, `notice`, `not classified`, `debug` — P5. Presets: Zabbix 5 Disaster → P1, 4 High → P2, 3 Average → P3, 2 Warning → P4 (formerly P3), 1 Information and 0 Not classified → P5; Alertmanager `severity=low` → P4. PagerDuty Events API v2 has no low level, so P4 goes to PagerDuty as `info`.
- **Collapsing and lifecycle.** Key = CI + signal (without a signal — the title); severity = the maximum over the sources; resolve when all sources have returned to normal; reopening within a 10-minute window; RED and USE of the same service are linked as a "probable cause". An incident for a CI that is not in the catalog waits for it and is bound when it appears: at once when the CI or an alias is created from the incident card (`POST /api/incidents/{id}/create-ci`, `POST /api/incidents/{id}/bind-ci`), otherwise within 15 seconds after the catalog changes (including NetBox synchronization). The CI is found by the Inventory DB reference from the `source_ref` label (if it points to exactly one CI), then by ID, name, short name, IP, DNS name from AD and aliases, and if the name found no CI — by the IP from the `ip` label (`PUT /api/cis/{id}/aliases`; an alias that is already the name of another CI is rejected with `alias_taken`). If the CI already has an active incident for the same signal, the waiting incident joins it and is resolved. Route: CI → business service → owning team → people; without a team — the people responsible for the CI (see "Who gets an incident").
- **Rule Engine.** RED and USE rules evaluate PromQL against a metric source at the rule's interval (from 10 s to 1 h). A series above the threshold for longer than `for` gives a firing event on the CI from the label; a return to normal or a disappearing series gives resolved. The state of the series is saved, so a restart neither loses nor duplicates firings.
- **PagerDuty Gateway.** Events API v2 (US or EU), `dedup_key = umb-<id>`, routing by team and business service, the people responsible for the CI in `custom_details`, a severity threshold. A disabled PagerDuty is not an error: incidents get the `off` state ("PagerDuty not used"), are not counted as "not accepted" and write nothing to the timeline; after PagerDuty is enabled, open incidents are sent to it. Incidents with the `umbrella_test=true` label (test events) are not sent to PagerDuty. Delivery errors are written to the timeline as codes (`no_key`, `key_unavailable`, `queue_full`, `breaker`, `unreachable`, `unavailable`, `rejected`, skipped — `below_threshold`, `test`), which the interface translates. Errors 429 and 5xx are retried, other errors are not; the circuit breaker opens for 60 seconds after 5 errors in a row. Undelivered trigger, acknowledge and resolve are retried once a minute, including after a restart. Webhooks v3 on `/api/pagerduty/webhook` are verified by signature; the incident is found by `incident_key`, the incident ID or the incident's alerts; the events triggered, acknowledged, resolved, reassigned, escalated, annotated, priority_updated go into the timeline.
- **Fallback notification.** An open incident of at least the lowest severity (error — P2 by default) that nobody has taken gets the "Backup" mark: with PagerDuty on — if PagerDuty has not accepted the incident within the delay (2 minutes by default) or skipped it because of its threshold; without PagerDuty, fallback is the main channel and by default goes out at once. The delay and the lowest severity are set in the "Backup notification" card. Acknowledged and resolved incidents, incidents in a maintenance window and test events (`umbrella_test=true`) are not notified; a notification still waiting to be sent is cancelled on acknowledgement (the timeline says "Backup notification cancelled"). The people on the route and the additional addresses get an e-mail and a Telegram message; team channels and additional webhooks get a message in Microsoft Teams (a card with buttons) and Zoom, with the links "Acknowledge", "Open" and Grafana; the acknowledgement link (`/ack/<token>`) is signed with HMAC and is valid for 24 hours. When the incident is acknowledged (in Umbrella, via the link or in PagerDuty) or resolved, all addresses that received the fallback notification get a short notice: who took the incident and when, or that it is resolved, with a link to it. Up to 3 attempts with a pause; the result of each delivery goes into the timeline; unsent notifications and notices are sent again after a restart.
- **Authentication and authorization.** Sessions in the HttpOnly cookie `umbrella_session` (12 hours of inactivity, no longer than 7 days); modifying requests need the `X-CSRF-Token` header with the `csrf` value from the `/api/auth/login` or `/api/auth/me` response. Local passwords, passkeys (WebAuthn with user verification), LDAP / AD, Entra ID, Google, Apple and any OpenID Connect provider; the password policy (length, character classes, alphabets, blocking of common passwords, history of the last N passwords, expiry with a warning, account lockout after N failed sign-ins for M minutes); the sign-in method is recorded in the session and given to connected applications (`amr`). Every API route checks the `<page>:<function>` permission. Machines use **API tokens** of service accounts instead of a session: `Authorization: Bearer umb_…`, no CSRF header, the same permission and scope checks with the role of the account; only a keyed hash (HMAC-SHA256, key in OpenBao) is stored ([docs/access.md](docs/access.md#service-accounts-and-api-tokens)). Session routes of a person (`/api/auth/*` except `GET /api/auth/me`) do not accept tokens.
- **Permissions.** The permission catalog (`GET /api/access/catalog`): `incidents` (view, ack), `cmdb` (view), `cis`, `services`, `maintenance`, `rules`, `credentials`, `teams`, `roles` (view, edit), `wallboards` (view, edit, proxies), `connectors` (view, edit, publish, payload), `netbox` (view, test, edit, sync), `monitoring` (view, test, edit, sync, link), `users` (view, create, edit, lock, password, delete), `settings.alerting` (view, test, edit), `status` (view, defaults), `settings.ldap` (view, test, edit), `settings.postgres` and `settings.openbao` (view, test, migrate), `settings.policy` (view, edit). The `GET /api/refs` reference returns the users, roles, teams and services sections only to those who have the permission of the page that reads them; other sections come back empty.
- **Scope.** The user's mode: "All services", "Services of my teams" (owned or supported) or "Chosen services". A restricted user sees only the incidents of their services (list, counters, card, actions, Grafana, events on the CMDB map), creates maintenance windows and TV wallboards only on their own targets and cannot create an "all incidents" wallboard. An administrator always sees everything. Details — [docs/access.md](docs/access.md).
- **Who gets an incident.** A single chain of rules, which is also shown by the "Where an incident goes" block in the CI and service cards (`GET /api/cis/{id}/route`, `GET /api/services/{id}/route`):
  1. The **CI** of the event (the `ci` label or the `ci` field; manual host bindings, name, short name, IP, DNS name).
  2. The **services** of this CI, except retired ones, in order: criticality, active before planned, name. The **main service** is the first one that has an owning team.
  3. The **owning team** of the main service. Supporting teams do not change the route: they see the service's incidents in the "Services of my teams" scope and on their team's TV wallboards.
  4. **People**: the team lead and members (a person can be in several teams). If the team has its own channel (mailbox, Telegram chat, Teams or Zoom webhook) — the channel and the lead. No people — the parent team.
  5. No service with a team or no people — the **people responsible for the CI** (NetBox contacts or set manually).
  6. **PagerDuty**: the first route row from the top whose team and service match the team and the main service (empty means any), otherwise the default integration. Fallback notification — the people from items 4–5 plus "Always also notify".
- **Grafana.** `/go/incidents/{id}/grafana` opens the dashboard from "Settings → Alerting" with a window around the incident (±10 minutes by default, up to 24 h) and the variables `var-incident`, `var-ci`, `var-ci_id`, `var-signal`, `var-service`, `var-team`; the dashboard's own parameters are kept. The link works for a signed-in user with permission for the incident and sends everyone else to the incident in Umbrella. Without this setting the link is not shown in the interface, in PagerDuty or in e-mails.
- **Umbrella Flow.** `/go/incidents/{id}/flow` and `/go/cis/{id}/flow` redirect to `<Flow address>/go/incident?ci=…&ip=…&ref=…&from=…&to=…` (RFC 3339): for an incident — from 30 minutes before it opened to the current moment or to 10 minutes after it was resolved, for a CI — the last hour. Permissions and behaviour without a cookie are the same as for the Grafana link; the address is `GET/PUT /api/flow` (`settings.alerting:view` / `settings.alerting:edit`). Without an address there are no links. Details — [docs/flow.md](docs/flow.md).

## What is not there yet

- A relational configuration schema in PostgreSQL (it is now stored as a snapshot in a single table) and NATS JetStream instead of internal calls.
- Context Builder (generating a separate Grafana dashboard for an incident).
- Prometheus metrics (`/metrics`), viewing the audit log in the interface, notifications in Microsoft Teams and Zoom.
- Pull connectors (polling a source on a schedule) and other items of the plan from [docs/connectors.md](docs/connectors.md#what-is-not-done-yet-from-the-plan).

These functions are not in the interface either.

## Running

You need Go 1.26+, Node.js 22 (as in the image), OpenBao and PostgreSQL. For development, OpenBao is started in dev mode, and PostgreSQL, for example, from [deploy/postgres](../deploy/postgres):

```bash
cd app
make openbao-dev                 # OpenBao container, KV v2 umbrella/, token dev-root
make build
make run                         # http://localhost:8080, data in backend/data
```

The OpenBao address (`http://127.0.0.1:8200`, token `dev-root`, mount `umbrella`) and the PostgreSQL connection are entered in the setup wizard — `make openbao-dev` reminds you of these values at the end.

Development mode with hot reload: `make dev-api` and `make dev-web` (interface on :5173). Checks: `make test` (`go vet`, `go test`, interface type check; tests with PostgreSQL run if `UMBRELLA_TEST_POSTGRES` is set). Image: `make image` (version from `git describe`; the commit and build date are shown on the "System status" page). Running the image with a data volume: [deploy/umbrella](../deploy/umbrella).

On the first start, the setup wizard opens. The server writes the setup code (`setup_code`) to the log and puts it into `UMBRELLA_DATA_DIR/setup-token` (or takes it from `UMBRELLA_SETUP_TOKEN`). The wizard saves `umbrella.json`; after that, Umbrella connects to OpenBao and PostgreSQL from this file on every start and does not start without them. If the database holds no Umbrella state, the start stops with an error: restore the database or delete `umbrella.json` to go through the wizard again.

### Quick start: from installation to the first incident reaching the on-call engineer

1. **Wizard** (7 screens). The Umbrella address is filled in from the address the wizard is opened at, and is saved (correct it if users open Umbrella under a different name). The password policy is collapsed in the "Administrator" step and by default requires no decisions.
2. **Sign-in.** The empty "Incidents" page shows the "First steps" checklist with progress (it is also a card on "System status"). Each item leads to the page where it is done; items the user has no permissions for are marked "Ask an administrator". The checklist disappears when all required items are done.

| Item | Done when | Where it leads |
| --- | --- | --- |
| Event source | at least one connector is published | `/monitoring?connect=1` (the "Connect a source" dialog; without access to "Monitoring systems" — `/connectors?connect=1`) |
| First event | at least one event has been received (by a connector or a rule) | the connector (if there is only one) or the list of connectors |
| Team | there is a team with at least one active member | "Teams" |
| CI in a service (optional) | a CI is bound to a service with an owning team | "Business services" |
| Delivery channel | PagerDuty is on and has a key, **or** e-mail / Telegram / Teams / Zoom are set up and there is a recipient (a team member with an address or chat, the team's own channel, "Always also notify") | "Settings → Alerting" |
| Umbrella address | a public address is set | "Settings → Alerting", the "Umbrella address" card |

3. **Source:** "Connect" → type (Zabbix, Alertmanager, Grafana, Graylog, Umbrella Flow, webhook) → "Create"; the token, the connector and the publication are made at once. In Zabbix — import the ready media type with the address and token filled in, media for the user, a trigger action. When a connector is created manually, the token is made with the "Create token" button right in the credentials slot and is shown once with a "Copy" button.
4. **Team:** "New team" → name → "Add members".
5. **Delivery:** turn on PagerDuty and paste the default integration key (or e-mail + the on-call mailbox in "Always also notify"). An incident for a CI that is not yet in the catalog goes along the default route.

Checklist API: `GET /api/onboarding` (any signed-in user) → `{done, steps: [{id, done, optional, path, can_fix}]}`, id: `source`, `event`, `team`, `catalog` (optional), `delivery`, `public_url`. Umbrella address: `GET/PUT /api/settings/public-url` (`settings.alerting:view` / `settings.alerting:edit`); the PagerDuty form no longer sends or overwrites it. A token from the connector dialog: `POST /api/connectors/credentials/token` `{name}` (`connectors:edit` + `credentials:edit`) → `{credential, token}`.

**What it costs** (the minimal path to an incident reaching a person, without NetBox and host monitoring):

| | Before | Now |
| --- | --- | --- |
| Wizard | 8 screens, 4 decisions, no public address | 7 screens, 3 decisions, the address is prefilled and saved |
| After sign-in | 9–11 screens on 6–7 pages, ~40 actions (24 without CMDB), no hints | 4 screens on 4 pages (checklist, "Connect a source", "Teams", "Alerting"), 15 actions, each step is one link from the checklist |
| Zabbix setup | 4 steps, the address and token are copied by hand | 3 steps, YAML import with the address and token filled in |
| Decisions | 10–12 | 6 (3 in the wizard; source type; PagerDuty or e-mail; team) |
| **Total** | **17–19 screens, ~40 actions + 4 steps in Zabbix** | **11 screens, 15 actions + 3 steps in Zabbix** |

### Environment variables

| Variable | Purpose | Default |
| --- | --- | --- |
| `UMBRELLA_ADDR` | HTTP address (flag `-addr`) | `:8080` |
| `UMBRELLA_WEB_DIR` | directory of the built interface (flag `-web`); if it does not exist, only the API is served | `../web/dist` (`/web` in the image) |
| `UMBRELLA_DATA_DIR` | directory of `umbrella.json`, `setup-token` and `sessions.gob` (flag `-data`); an empty value is not allowed | `data` (`/data` in the image) |
| `UMBRELLA_STARTUP_WAIT` | how long to wait for OpenBao and PostgreSQL on start | `2m` |
| `UMBRELLA_SETUP_TOKEN` | setup code for the wizard instead of a random one | — |
| `UMBRELLA_SECURE_COOKIES` | cookies with the Secure flag (behind a TLS proxy) | `false` |
| `UMBRELLA_TRUSTED_PROXIES` | reverse proxies (CIDR or IP, comma-separated) whose `X-Forwarded-For` / `X-Real-IP` are trusted when determining the client address (allowed networks of connectors and TV wallboards, sign-in attempt limits, audit log, request archive); extended by the "Trusted proxies" list on the TV wallboards page; empty — the address is taken only from the connection | — |
| `UMBRELLA_INGEST_WORKERS` | how many workers process the connector intake queue | `2` |
| `UMBRELLA_INGEST_BATCH_SIZE` | how many requests a worker takes from the queue in one transaction | `50` |
| `UMBRELLA_INGEST_MAX_ATTEMPTS` | how many times to try a request that could not be saved for a temporary reason | `3` |
| `UMBRELLA_INGEST_PROCESS_TIMEOUT` | time limit for processing one request by a connector | `30s` |
| `UMBRELLA_INGEST_TEST_WAIT` | how long a test event waits for an incident to appear before answering | `15s` |
| `UMBRELLA_INGEST_KEEP_REQUESTS` | retention of the connector request archive (partitions are deleted by whole days) | `168h` |
| `UMBRELLA_INGEST_KEEP_FAILURES` | retention of processing errors | `720h` |
| `UMBRELLA_INGEST_KEEP_STATS` | retention of connector statistics | `720h` |
| `UMBRELLA_INGEST_KEEP_IDEMPOTENCY` | retention of `Idempotency-Key` keys | `24h` |

The connections to OpenBao and PostgreSQL are set in the wizard and on the "Settings → OpenBao" and "Settings → PostgreSQL" pages, while PagerDuty, e-mail, Telegram, Grafana, Umbrella Flow, NetBox, monitoring systems, LDAP / AD and Entra ID are set in the interface or through the API, not with environment variables.

Flags: `-version` prints the version, `-healthcheck` checks `/healthz` of a running server (the image's HEALTHCHECK).

### Example

The API works with a session (sign-in saves a cookie, and modifying requests carry the `X-CSRF-Token` header) or with an API token of a service account (`Authorization: Bearer umb_…`, no CSRF; see [docs/access.md](docs/access.md#service-accounts-and-api-tokens)). The user needs the permissions `credentials:edit`, `connectors:edit` and `connectors:publish` (an administrator has all of them).

```bash
J=cookies.txt
CSRF=$(curl -s -c $J localhost:8080/api/auth/login -d '{"username":"admin","password":"…"}' | jq -r .csrf)
api() { curl -s -b $J -H "X-CSRF-Token: $CSRF" "$@"; }

# credentials for intake: the secret goes to OpenBao
CRED=$(api localhost:8080/api/credentials -d '{"name":"My source","type":"bearer","secrets":{"token":"s3cr3t"}}' | jq -r .id)
# a connector from the "Generic webhook" template with these credentials, then publication
CON=$(api localhost:8080/api/connectors -d '{"name":"My source","slug":"my","preset":"webhook","credentials":{"webhook.credential":"'$CRED'"}}' | jq -r .id)
api -X POST localhost:8080/api/connectors/$CON/publish -d '{}'

# intake: no session, with the token from the credentials
curl -X POST localhost:8080/api/ingest/my -H "Authorization: Bearer s3cr3t" \
  -d '{"id":"x1","host":"db-01","signal":"use.disk","severity":"critical","status":"firing","title":"Disk full"}'
```

## Structure

```
app/
  Dockerfile, Makefile
  backend/
    cmd/umbrella/          entry point: setup wizard or application, connection to OpenBao and PostgreSQL, switching connections on the fly
    internal/access/       catalog of pages and permissions for roles
    internal/alert/        Alert Engine: collapsing, lifecycle, route, windows, RED/USE, fallback, PagerDuty retries; incident tables
    internal/app/          HTTP API and application services: sign-in, users, roles, teams, CIs, services, CMDB, connectors, intake, rules, NetBox, monitoring, alerting, TV wallboards (tvweb), status
    internal/auth/         password hashing, persistent sessions
    internal/avatar/       avatar processing
    internal/config/       umbrella.json: connections to OpenBao and PostgreSQL, setup code
    internal/credentials/  user password hashes in OpenBao
    internal/directory/    LDAP / AD: sign-in, groups (including nested ones), domain computers
    internal/entra/        Microsoft Entra ID: OIDC sign-in, groups through Microsoft Graph
    internal/oidcx/        Google, Apple and OpenID Connect sign-in; JWT signing and verification (RS256, ES256)
    internal/passkey/      passkey (WebAuthn) ceremonies; passkeytest is a software authenticator for tests
    internal/flow/         builder nodes, graph compilation, expressions and CEL, execution
    internal/httpx/        common HTTP functions: JSON, errors, security headers, interface static files
    internal/ingest/       intake queue in PostgreSQL, request archive, events, errors, statistics, cleanup
    internal/logbuf/       buffer of recent log warnings and errors for the status page
    internal/model/        model: user, role, team, CI, service, connector, rule, window, TV wallboard, settings
    internal/monitoring/   Zabbix and Prometheus clients for host lists
    internal/netbox/       NetBox client: loading objects and contacts, registering and changing objects
    internal/notify/       fallback notification: e-mail, Telegram, Teams, Zoom, signed acknowledgement links
    internal/pagerduty/    PagerDuty Gateway: Events API v2, REST API, Webhooks v3
    internal/presets/      connector templates: Alertmanager, Grafana, Graylog, Umbrella Flow, Zabbix, generic webhook
    internal/rules/        RED/USE Rule Engine, PromQL queries, rule templates
    internal/secrets/      OpenBao client: KV v2, AppRole and token, renewal, cache, moving secrets
    internal/setup/        first-setup wizard and its checks
    internal/store/        in-memory state, snapshot in PostgreSQL, database statistics
    internal/textx/        truncating strings by characters and bytes
  web/src/
    app/                   the application after sign-in: menu, top bar, profile, pages by folder (incidents, cmdb, cis, services, maintenance, wallboards, connectors, rules, netbox, monitoring, users, teams, roles, settings, bulk, org); page registry — pages.tsx
    connections/           forms and checks for OpenBao, PostgreSQL and LDAP connections (wizard and settings)
    setup/                 setup wizard
    *.tsx, *.ts            API client, router, themes, localization (i18n.tsx and strings.ts next to the pages), animations, common interface elements, password policy editor
```

## Interface

The layout is designed for monitoring engineers and on-call staff:
- a navigation panel on the left (collapsed with a button in the top bar) with the groups Overview, Automation, Organization and Settings;
- the top bar: menu button, logo, user menu (profile, language, theme, sign-out);
- above tables — counter tiles; severity and status labels are soft, with a colored dot;
- object cards open as a side panel with tabs; the builder is laid out as a node palette, a canvas and an inspector.

### Themes and languages

Light and dark themes and two languages — Russian and English — are built in. The default theme, language and time zone are set in the wizard and on the "System status" page; users make their own choice in the user menu, and it is stored in the browser (localStorage). There is no upload of custom themes and languages. Data from the server (names of CIs, incidents, rules, timeline entries) is shown as is.

### Animation

A theme change spreads as a circle from the click point (View Transitions API; in browsers without it, colors change with a smooth transition). The appearance of pages and wizard steps, menus, panels and dialogs is animated. If reduced motion is turned on in the OS, animation is disabled.

## Screenshots

Taken in one of the earlier versions of the interface: they show pages that no longer exist ("Heatmap", "Operations center", "Events", Teams and Zoom "Notifications", uploading custom themes and languages), and the other pages look different. All files are in [docs/screens](docs/screens). The example theme and language files in [docs/examples](docs/examples) belong to the same earlier version.

| Light theme, Russian | Dark theme, English |
|---|---|
| Incidents ![](docs/screens/incidents-light-ru.png) | Incidents ![](docs/screens/incidents-dark-en.png) |
| Incident card ![](docs/screens/incident-drawer-light-ru.png) | Incident drawer ![](docs/screens/incident-drawer-dark-en.png) |
| Operations center ![](docs/screens/ops-light-ru.png) | Operations center ![](docs/screens/ops-dark-en.png) |
| CMDB map ![](docs/screens/cmdb-light-ru.png) | CMDB map ![](docs/screens/cmdb-dark-en.png) |
| Connector builder ![](docs/screens/editor-light-ru.png) | Connector builder ![](docs/screens/editor-dark-en.png) |
| Heatmap ![](docs/screens/heatmap-light-ru.png) | Heatmap: current state ![](docs/screens/heatmap-state-dark-en.png) |
| Settings: themes ![](docs/screens/settings-light-ru.png) | Settings: languages ![](docs/screens/settings-lang-dark-en.png) |
| Uploaded "Forest" theme ![](docs/screens/custom-theme-forest.png) | Uploaded Deutsch language (partial translation) ![](docs/screens/custom-locale-de.png) |

| Sign-in ![](docs/screens/signin-light-ru.png) | Sign-in ![](docs/screens/signin-dark-en.png) |
| Users ![](docs/screens/users-light-ru.png) | A service owner sees only their own service ![](docs/screens/owner-incidents-dark-en.png) |
| User: roles and business services ![](docs/screens/user-edit.png) | Account menu ![](docs/screens/owner-account-en.png) |
| Roles and permissions ![](docs/screens/roles-light-ru.png) | Role permissions ![](docs/screens/role-edit.png) |
| Teams and Zoom notifications ![](docs/screens/notifications-light-ru.png) | Zoom channel ![](docs/screens/channel-edit-zoom.png) |
| Delivery log ![](docs/screens/deliveries.png) | Theme change: circular reveal ![](docs/screens/theme-reveal-mid.png) |

More: [animation settings](docs/screens/settings-motion.png), [connectors](docs/screens/connectors-light-ru.png), [events](docs/screens/events-light-ru.png), [maintenance windows](docs/screens/maintenance-light-ru.png).
