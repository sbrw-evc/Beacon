# n8n: a feature analysis for Beacon

This document analyses n8n, the open-source automation platform, and answers three questions: which of its ideas are worth bringing into Beacon's connector builder and event-processing engine, how to implement them in our stack (Go 1.24, React 18 + TypeScript + Vite + @xyflow/react, PostgreSQL, OpenBao, local users and LDAP/AD), and in what order.

Sources were checked on 4 October 2026: the current n8n release is `n8n@2.41.6` (2 October 2026), the documentation is [docs.n8n.io](https://docs.n8n.io) in its new section layout (`build/`, `deploy/`, `administer/`, `connect/`, `integrations/`), and the source code is the `master` branch of the [n8n-io/n8n](https://github.com/n8n-io/n8n) repository. Source paths below are relative to the repository root.

> **Licence.** n8n is distributed under the [Sustainable Use License](https://github.com/n8n-io/n8n/blob/master/LICENSE.md) (a fair-code model); files and directories with `.ee` in their name are under the separate [n8n Enterprise License](https://github.com/n8n-io/n8n/blob/master/LICENSE_EE.md). This is **not** an open-source licence in the OSI sense: use and modification are allowed only for your own internal purposes or non-commercial use, and redistribution only free of charge and non-commercially. Beacon is a product, so **we must not copy n8n code, translate it into Go "one to one", or take node JSON schemas, icons, UI text or documentation**. We take only ideas, concepts and architectural techniques, and implement them from scratch. Links to the source code in this document are there to explain the principle, not as a template to port.

## Contents

1. [Key findings](#1-key-findings)
2. [Concept mapping: the former Beacon builder and n8n](#2-concept-mapping-the-former-beacon-builder-and-n8n)
3. [The key architectural difference: event stream versus automation](#3-the-key-architectural-difference-event-stream-versus-automation)
4. [Feature analysis](#4-feature-analysis)
5. [Applicability summary table](#5-applicability-summary-table)
6. [Prioritised implementation plan](#6-prioritised-implementation-plan)
7. [What not to take from n8n](#7-what-not-to-take-from-n8n)
8. [Open questions](#8-open-questions)
9. [References](#9-references)

---

## 1. Key findings

- **n8n is a general-purpose automation platform (iPaaS)** designed for relatively infrequent workflow runs with a full record of every run. Beacon is a pipeline that processes a large stream of events with strict latency and idempotency requirements. So the n8n model cannot be adopted wholesale: our graph editor compiles into a fast pipeline instead of being interpreted node by node with state saved after every step.
- **The most valuable thing for us** is the in-editor development experience: capturing samples of incoming requests (the webhook's test URL), pinned data, partial execution up to a selected node, viewing each node's input and output, marking stale results (dirty nodes), and re-running on data from history. This is exactly what the former builder lacked, and it is the main thing that separates a convenient low-code tool from a form with JSON.
- **Second in value** is the lifecycle model: a draft with autosave → publishing a specific version → atomically switching triggers only for the parts that changed, version history with rollback, named versions, approval before publishing.
- **Third** is trigger reliability: a "durable" polling scheduler in the database with task claiming, leases, a missed-run policy and a polling cursor saved in the same transaction as the result. In n8n this only appeared in 2.32–2.37 and is still marked as not fully stable for polling; we need it from day one.
- **Expressions**: n8n's `{{ }}` syntax is full JavaScript; it took n8n several years to put it in a sandbox (a V8 isolate is the default only since 2.35). It pays for us to keep our own simple `${field|$fallback|default}` template for substitutions and add a safe condition language without side effects (CEL is recommended, `github.com/google/cel-go`).
- **User code (Code node)** in n8n requires a separate executor process (task runner) in a separate container; otherwise the code can read the database, the encryption key and secrets. For Beacon this is low priority; if it is ever needed, only a sandbox with no network or filesystem access (Starlark or WASM) with CPU and memory limits, off the hot path by default.
- **Secrets**: n8n encrypts them in the database with the instance key; external stores are a paid feature. We have OpenBao as the only place of storage, and the connector configuration holds only `openbao://...` references. Here we are stronger from the start, and this must be kept.
- **Scaling**: n8n's queue mode (Redis + Bull + workers) is a consequence of Node.js being single-threaded. In Go, goroutines and PostgreSQL are enough (`SELECT … FOR UPDATE SKIP LOCKED`, advisory locks for leadership); add a message broker only when load proves it necessary.
- **Do not take**: the catalogue of hundreds of integrations, JavaScript in expressions, saving all executions by default, Wait nodes and long-lived executions in the ingest pipeline, execution order based on node position on the canvas, installing third-party nodes from npm, AI agents.
- **Plan** (section 6): engine and model foundation → MVP of webhook connectors with OpenBao and versions → in-editor debugging → polling and reliability → branching and error handling → access control and lifecycle → optional extensions.

## 2. Concept mapping: the former Beacon builder and n8n

| Former Beacon | n8n equivalent | Comment |
| --- | --- | --- |
| Connector (graph of @xyflow nodes) | Workflow (nodes + connections, JSON) | Essentially the same thing; n8n stores the graph as `nodes[]` + `connections{}` |
| `trigger.webhook` | [Webhook](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.webhook) | n8n has a test URL and a production URL, authentication, an IP allowlist, an "Only Run If" condition, response modes |
| `trigger.poll` | [Schedule Trigger](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.scheduletrigger) + [HTTP Request](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.httprequest); polling triggers with a cursor | In n8n, out-of-the-box polling exists only in nodes for specific services; there is no generic HTTP polling with a cursor |
| `parse.json` with choice of the items array | Implicit: a node's input is an array of items; [Split Out](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.splitout) to unwrap an array | Our explicit array choice is more convenient for monitoring (Alertmanager sends `alerts[]`) |
| `map.severity` (table) | Switch / expression / Code | n8n has no table mapping; ours is better and should be kept |
| `enrich.labels` | [Edit Fields (Set)](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.set) | — |
| `map.event` | Edit Fields (Set) in "only these fields" mode | Our target schema is fixed (title, ci, signal, method, severity, status, external_id, value), which is a plus: it can be validated |
| `out.event` | The last node; writes to the target system | For us, a write into the normalisation/incident pipeline |
| `ack.response` | [Respond to Webhook](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.respondtowebhook) / Webhook response mode | — |
| `${field\|$fallback\|default}` | [Expressions](https://docs.n8n.io/build/work-with-data/transform-data/expressions-for-data-transformation) `{{ $json.field ?? 'default' }}` | n8n is full JS; our variant is safer and faster |
| Draft / published | [Save and publish](https://docs.n8n.io/build/understand-workflows/save-and-publish-workflows): draft autosave, publishing a version | n8n arrived at the same model in 2.x |
| running / stopped | Published / unpublished, publication statuses (partial, failed) | n8n has a useful "partially published" status |
| Event and error counters | [Insights](https://docs.n8n.io/administer/observe-and-log/track-usage-with-insights), executions list | — |
| Parse errors | Failed executions + [Error workflow](https://docs.n8n.io/build/flow-logic/handle-errors-gracefully) | We have a separate store of raw bodies with reprocessing, which is better for monitoring |
| `openbao://...` | Credentials (encrypted with the instance key) + `$secrets` from [external stores](https://docs.n8n.io/administer/manage-credentials/use-external-secret-stores) (Enterprise) | Our approach is stronger |
| Built-in presets | [Templates](https://docs.n8n.io/build/ways-of-building-workflows/use-templates), [JSON import/export](https://docs.n8n.io/build/manage-workflows/export-and-import) | — |

## 3. The key architectural difference: event stream versus automation

How n8n executes (see `packages/core/src/execution-engine/workflow-execute.ts`): every trigger that fires creates an **execution**, a record in the database; the engine walks the graph using a node stack, each node receives an array of items and returns an array of arrays (one per output), and after each node (when "Save execution progress" is enabled) or at the end of the execution the state with all intermediate data is serialised into the database. In queue mode the main process puts only the execution ID into Redis; a worker reads the definition and data from the database, executes and writes the result back ([Enable queue mode](https://docs.n8n.io/deploy/host-n8n/configure-n8n/scaling/enable-queue-mode)). This is convenient for debugging and recovery, but expensive: several database round trips for each incoming event and a large database unless saving is cut down ([Manage execution data](https://docs.n8n.io/deploy/host-n8n/configure-n8n/scaling/manage-execution-data): by default retention is 14 days or 10,000 executions).

For Beacon the stream is thousands of events per second at the peaks of an alert "storm", and what matters is the latency from source to incident. So we propose separating two modes:

| | **Ingest pipeline** | **Automation flow** (later, if needed) |
| --- | --- | --- |
| Purpose | Source → normalised event | Reacting to an incident: enrichment from external systems, escalations, actions |
| Frequency | High, streaming | Low |
| Execution | The graph is **compiled** on publish into a chain of Go functions; no intermediate state is written | Graph interpretation with state saved; waits are allowed |
| State | Stateless, except for the polling cursor and idempotency keys | Execution state in PostgreSQL |
| Tracing | Selective (last N, all errors, on demand) | Full |
| User code | None (only CEL expressions and templates) | Possible in a sandbox |

The editor (@xyflow) and the graph model are shared; what differs is the set of available nodes and the executor. Everything marked "for the pipeline" below must fit into the first column.

## 4. Feature analysis

For each feature: **what it is in n8n**, **why the user needs it**, **applicability to Beacon** (high / medium / low / not needed) and **how to implement it on our side**.

### 4.1. Workflow model: nodes, connections, items

**In n8n.** A workflow is JSON with an array of nodes (`type`, `typeVersion`, `parameters`, `credentials`, position on the canvas) and a connections object "node output → node input" with output/input indexes ([Understand n8n's data structure](https://docs.n8n.io/build/work-with-data/understand-n8ns-data-structure), types in `packages/workflow/src/interfaces.ts`). Data between nodes is an **array of items**, each item is `{ json: {...}, binary?: {...}, pairedItem?: {...} }`. By default a node runs once per item; a node can have several outputs (IF has two, Switch has N). Every node type has a version (`typeVersion`), and old workflows keep working on the old node version ([Versioning](https://docs.n8n.io/connect/create-nodes/build-your-node/reference/versioning)). Item linking between nodes ([Item linking](https://docs.n8n.io/build/work-with-data/reference-data/link-data-items)) makes it possible to tell which input item an output item came from.

**Why the user needs it.** A single predictable model: every node takes and returns a list of records, and everything is visible as a table/JSON in the editor. Batch processing without explicit loops.

**Applicability: high.** The former builder is already close to this model; it is worth formalising.

**How to implement.**
- Storage: a `connectors` table (identity, owner, tags, status) and `connector_versions` (an immutable snapshot of the graph `graph jsonb`, number, author, time, version name/description, published flag). Graph: `nodes[]` with `id` (a stable UUID, not a name: n8n references nodes by name, and renaming breaks expressions), `type`, `type_version`, `params`, `position`; `edges[]` with `source`, `source_output`, `target`, `target_input`.
- Go: a registry of node types. Each type describes itself declaratively: a JSON Schema for parameters, the number and names of inputs/outputs, a category (trigger/transform/output), a version. The `Compile(params) (Step, error)` method runs **once at publish time**: it parses templates, compiles CEL, checks secret references. `Step` is `func(ctx, batch []Record) (outputs [][]Record, err error)`.
- `Record` is `map[string]any` plus service fields: `lineage` (the index of the source item and the ID of the incoming request, the equivalent of `pairedItem`, needed for tracing "which request this event came from"), `raw_ref` (a reference to the raw body).
- Graph validation on save: acyclicity (for the pipeline), exactly one trigger, `out.event` reachable, output types compatible, all required parameters set.
- Node type versions: a `type_version` field in the graph from day one; when a node's semantics change, a new version is added and the old one stays in the registry.
- React: the parameters panel is generated from the node type's JSON Schema (like n8n's declarative node descriptions, but following our schema). Show only implemented node types in the palette.

### 4.2. Binary data

**In n8n.** An item can have a `binary` property with files; storage modes are memory, filesystem, database, S3 ([Work with files and images](https://docs.n8n.io/build/work-with-data/handle-special-data-types/work-with-files-and-images), [Handle binary data](https://docs.n8n.io/deploy/host-n8n/configure-n8n/scaling/handle-binary-data)); the filesystem is not supported in queue mode.

**Why.** Attachments, files, images.

**Applicability: low.** Monitoring events are JSON/text. A possible scenario is graph snapshots in notifications, but that belongs to the notification system, not the ingest pipeline.

**How to implement (if needed).** Do not add `binary` to the record model. Store raw bodies of incoming requests separately (see 4.10) with a size limit. Notification attachments go into a separate (S3-compatible) store with references.

### 4.3. Triggers: webhook

**In n8n.** [Webhook](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.webhook): methods, path, a **test URL** (accepts one request and shows it in the editor) and a **production URL** (registered on publish); Basic / Header / JWT authentication; an IP allowlist; CORS; raw body; an "Only Run If" condition on the request; response modes: immediately, after the last node, via a Respond to Webhook node; a default body limit of 16 MB. In queue mode webhook intake can be moved into separate processes behind a load balancer.

**Why.** Sources "push" events; the user wants to get a URL within a minute, send a test alert and see its structure.

**Applicability: high.**

**How to implement.**
- A URL of the form `/ingest/{connector_slug}` with authentication by one of: a token in a header, Basic, **HMAC signature of the body** (many systems sign webhooks; n8n does not have this out of the box, and we need it), mTLS at the ingress level. Secrets live in OpenBao.
- An allowlist of networks (CIDR), a body size limit, a per-connector rate limit (a 429 response with `Retry-After`).
- **Sample capture mode** (the equivalent of the test URL): in the editor a "Capture as samples" button saves the next N requests to the same URL as samples (body, headers without secrets, time), and they become the input for debugging (see 4.12). It also works for a published connector, without a separate test address, so the source does not have to be reconfigured.
- Response: by default **202 immediately after the request is durably recorded** (written to PostgreSQL), processing is asynchronous. Do not implement a synchronous "after processing" response: the source should not wait for the CMDB and deduplication. The `ack.response` node is only for sources that require a special response body/code.
- An "accept only if…" condition: a CEL expression over the request (the equivalent of "Only Run If").

### 4.4. Triggers: schedule and polling

**In n8n.** [Schedule Trigger](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.scheduletrigger) (intervals and cron, the workflow's time zone). Polling triggers for specific services keep their cursor in the workflow's "static data". Since 2.36 there is the [Durable scheduler](https://docs.n8n.io/deploy/host-n8n/configure-n8n/durable-scheduler) (the `packages/@n8n/scheduler` package): upcoming runs are materialised in advance as rows in the database, instances **claim** them (only one executes), a "reaper" releases claims held by crashed instances, there is a **missed-run policy** (do not run / one catch-up run) with a grace period, a **polling timeout** (45 s by default) with a growing retry interval on persistent failures, dead-lettering after N attempts, and **durable poll cursors**: the cursor is kept in a separate table and saved in the same transaction as the result, so that a repeated poll neither loses nor duplicates data. The documentation states plainly that a poll can run twice (at-least-once). For schedules a unique execution deduplication key was added (`deduplicationKey` in the executions table, the `AddExecutionDeduplicationKey` migration). Scheduler metrics: queue, overdue tasks, start delay (p50/p99), stolen leases.

**Why.** Sources that cannot send webhooks (the Zabbix API, OpenSearch queries, HTTP health endpoints). The user cares that after a Beacon restart polling does not "lose" a window or duplicate events.

**Applicability: high.** This is exactly our `trigger.poll`, and n8n's solutions make a good list of requirements.

**How to implement.**
- Tables `poll_schedules` (connector, version, interval/cron, time zone, missed-run policy, grace period, timeout) and `poll_runs` (scheduled time, status, `claimed_by`, `lease_until`, attempt, error).
- Claiming: `UPDATE poll_runs SET claimed_by=$1, lease_until=now()+$2 WHERE id IN (SELECT id FROM poll_runs WHERE due_at<=now() AND status='pending' ORDER BY due_at FOR UPDATE SKIP LOCKED LIMIT $3) RETURNING …`. Works on any number of Beacon instances without a leader.
- A unique key `(schedule_id, due_at)`: a run is never materialised twice.
- Cursor: a `poll_cursors (connector_id, node_id, cursor jsonb, updated_at)` table; updating the cursor and inserting the accepted events into the normalisation queue happen **in a single PostgreSQL transaction**. A repeated poll is then safe.
- The HTTP request timeout is shorter than the lease; on persistent errors, exponential backoff of the interval and a "connector degraded" flag (see 4.9).
- Prometheus metrics: `due`, start delay, poll duration, errors, stolen leases.
- Protection against a "thundering herd": a random start offset (jitter) within the interval.

### 4.5. Event triggers (queues, streams, internal events)

**In n8n.** Triggers that keep a persistent connection (message queues, IMAP mail, etc.); in multi-main only the leader runs them; [n8n Trigger](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.n8ntrigger) for events of n8n itself; [Error Trigger](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.errortrigger).

**Why.** Sources that publish events to a bus (Kafka and similar), syslog, SNMP traps.

**Applicability: medium.** For some customers this is the main channel (the lab environment already has Kafka). Not needed in the first MVP.

**How to implement.** A `trigger.stream` trigger type with adapters (Kafka consumer group: commit offsets after recording in PostgreSQL; syslog/SNMP: a separate receiver). Subscriptions are distributed between instances by claiming subscription rows with a lease (as in 4.4) or by the bus's own consumer group mechanism. Beacon's internal events (connector error, CI change) do not go through user triggers but through built-in mechanisms (see 4.9).

### 4.6. Expressions

**In n8n.** `{{ … }}` in any parameter is JavaScript ([Expressions](https://docs.n8n.io/build/work-with-data/transform-data/expressions-for-data-transformation), [reference](https://docs.n8n.io/build/work-with-data/transform-data/expression-reference)). Variables: `$json` (the current item), `$input`, `$('Node name').item` / `$node[…]`, `$now`/`$today` (Luxon DateTime), `$execution`, `$workflow`, `$vars`, `$secrets`; type extensions (`.toDateTime()`, `.isEmpty()`, etc.), JMESPath. Implementation: `packages/workflow/src/expression.ts`, `workflow-data-proxy.ts`; sandbox: `packages/@n8n/expression-runtime` (a V8 isolate via isolated-vm). [Expression engine](https://docs.n8n.io/deploy/host-n8n/configure-n8n/basic-configuration/use-environment-variables/expression-engine): the isolated `vm` engine became the default only in 2.35; the editor still uses the non-isolated `legacy` engine by default; limits are 5 s and 128 MiB per evaluation. The editor highlights the expression and shows its result on the current data.

**Why.** Flexible field mapping without code; a result preview right in the field.

**Applicability: high** (the idea and UX), **not needed** (JavaScript as the language).

**How to implement.**
- **Substitutions**: keep our own template language `${path|$fallback|default}`; extend it with paths with indexes and escaping (`${labels["app.kubernetes.io/name"]}`), filter functions (`|lower`, `|trim`, `|default:…`, `|date:"RFC3339"`), and a reference to a named node's output `${node.<id>.field}`, for preceding nodes only. Parsing at compile time, execution without allocations where possible.
- **Conditions and computations**: CEL (`cel-go`): typed, side-effect free, not Turing-complete, with cost estimation and a limit (`cel.CostLimit`), compiled once. Our own functions: time (`now()`, `parse_time`, `duration`), strings (regex with limits), CIDR, hashing. Dates use the standard CEL `timestamp`/`duration` instead of Luxon.
- A single variable environment: `event` (the current record), `request` (webhook headers/parameters), `connector`, `vars` (non-secret constants, see 4.19). **Secrets are not available in expressions**, only in fields explicitly marked as secret (n8n does the same with external secrets).
- React: an expression editor on CodeMirror 6 with path autocompletion from the current data sample and a result preview; the preview is evaluated **on the server** with the same engine (no browser/server discrepancy, no untrusted code running in the browser).

### 4.7. Code node (JavaScript / Python) and sandbox

**In n8n.** [Code](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.code): JavaScript or Python, in "once for all items" and "for each item" modes. It is executed via [task runners](https://docs.n8n.io/deploy/host-n8n/configure-n8n/set-up-task-runners) (`packages/@n8n/task-runner`, `packages/@n8n/task-runner-python`): a broker in n8n, with executors connecting over WebSocket. The documentation warns explicitly that without external mode "anyone who can edit a workflow can potentially read the database, the encryption key, secrets and environment variables"; internal mode is deprecated as of 3.0. [Hardening](https://docs.n8n.io/deploy/host-n8n/configure-n8n/security/harden-task-runners): a separate container, distroless, the nobody user, a read-only filesystem, an AppArmor rule forbidding reads of `/proc/*/environ`.

**Why.** An "escape hatch" when the standard nodes are not enough: parsing an exotic format, complex logic.

**Applicability: low for the pipeline, medium in the longer term.** Code on the hot path is a latency and security risk (Beacon has access to the CMDB and to notification channel tokens). Most cases are covered by CEL and table nodes.

**How to implement (stage 6, on request).**
- Language: **Starlark** (`go.starlark.net`): Python-like, deterministic, with no access to the filesystem/network/time unless granted; a step limit (`thread.SetMaxExecutionSteps`) and cancellation via context. An alternative for JS syntax is QuickJS compiled to WASM, running under `wazero` with a memory limit and interruption; `goja` in the same process does not isolate memory, so only in a separate process.
- Execution in a **separate executor process** (sidecar) with minimal privileges, no secrets and no network access; batches are exchanged over gRPC/Unix socket; a timeout per batch; when exceeded, a record goes to parse errors and the connector does not crash.
- The right to write code is a separate role (scope `connector:code`); changes are audited.

### 4.8. Branching and flow control

**In n8n.** [IF](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.if) and [Switch](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.switch) ([Split with conditionals](https://docs.n8n.io/build/flow-logic/split-with-conditionals)), [Filter](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.filter), [Merge](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.merge) (append, combine by key, by position, an SQL-like mode), [Loop Over Items / Split in Batches](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.splitinbatches), [Wait](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.wait) (pause, waiting for a webhook: the execution is offloaded to the database and resumed later), [Remove Duplicates](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.removeduplicates) (within the input or against the history of previous executions: "value is new", "greater than previous", "date is later"). The [execution order](https://docs.n8n.io/build/flow-logic/understand-execution-order) of branches in v1 depends on **the branch's position on the canvas** (top to bottom, left to right).

**Why.** Different alert types from one source are handled differently; noise is dropped; data is combined.

**Applicability.**

| n8n node | Applicability | Beacon equivalent |
| --- | --- | --- |
| IF / Switch | High | `route.switch`: a list of CEL rules → named outputs + "otherwise" |
| Filter | High | `filter`: a CEL condition; dropped records are counted in a separate "filtered" counter (so it is visible that noise is being cut, not lost) |
| Remove Duplicates (within the input) | Medium | An option of `parse.json`/`map.event`; global deduplication lives in the core (below) |
| Remove Duplicates (against history) | High as an idea | Not a node but **built-in idempotency**: a key `(source, external_id, status, fingerprint)` with a unique index and a window |
| Merge | Low | Instead of merging streams, a lookup node `enrich.lookup` (CMDB, reference data) |
| Loop / Split in Batches | Not needed | The pipeline is already batch-based |
| Wait | Not needed in the pipeline | For escalations, the built-in notification state machine, not a user node |

**How to implement.** Branching compiles into routing records to outputs; several branches can end in different `out.event` nodes (for example, different CI binding rules). Execution order is **topological over the graph**, deterministic and independent of node coordinates.

### 4.9. Error handling

**In n8n.** ([Handle errors gracefully](https://docs.n8n.io/build/flow-logic/handle-errors-gracefully)) At node level: **Retry On Fail** (number of attempts, pause), **On Error**: stop the workflow / continue / continue via a separate "error" output. The [Stop And Error](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.stopanderror) node raises an explicit error. At workflow level, an **error workflow**: a separate workflow with an Error Trigger that receives the execution ID, the error, the last node and a link to the execution. A workflow timeout in the settings.

**Why.** A failure on one alert must not stop the stream; people need to find out about connector errors.

**Applicability: high.**

**How to implement.**
- **Node error policy** (a parameter of every node): `fail_record` (the record goes to parse errors and the rest go on; the default for the pipeline), `skip` (drop and count), `route_error` (to a separate node output, for example into a simplified "raw alert" mapping). Do not stop the whole batch because of one record.
- **Retries**: only for nodes with external calls (polling, HTTP enrichment): number of attempts, exponential pause with jitter, errors split into retryable (5xx, timeout, 429) and non-retryable (4xx, parse error). A circuit breaker per external system.
- **Parse errors / DLQ**: an `ingest_failures` table (connector, version, node, error, reference to the raw body, time, retry count) with a "Reprocess" button that uses the current published version (see 4.10).
- **Instead of an error workflow, built-in meta-monitoring**: an exceeded error threshold, no events for longer than expected (source "silence"), or persistent polling failures produce an **internal Beacon event** with the CI "connector X", which goes the usual path "incident → on-call notification". The user does not have to build an error-handling workflow by hand, and monitoring of the monitoring works out of the box.
- Timeouts: per node and per batch; exceeding one is a record/batch error, not a hung worker.

### 4.10. Credentials and secrets

**In n8n.** Credential types declaratively describe their fields and how requests are authenticated ([Credentials files](https://docs.n8n.io/connect/create-nodes/build-your-node/reference/credentials-files)); OAuth1/OAuth2 with token refresh; values are encrypted with the instance key (`N8N_ENCRYPTION_KEY`, `packages/core/src/encryption`), and recently with a two-level scheme with [data key rotation](https://docs.n8n.io/deploy/host-n8n/configure-n8n/security/rotate-encryption-keys). [Sharing](https://docs.n8n.io/administer/manage-credentials/share-credentials-securely): a credential can be used in a workflow without seeing its value. [External secret stores](https://docs.n8n.io/administer/manage-credentials/use-external-secret-stores) (including Vault) are Enterprise only, referenced as `{{ $secrets.<vault>.<key> }}`, work only in credential fields and only with string values; a store can be global or bound to a project. [Credential overwrites](https://docs.n8n.io/administer/manage-credentials/credential-overwrites): an administrator sets a client secret globally.

**Why.** To connect to a source without revealing the password/token to colleagues and without storing it in the configuration.

**Applicability: high.** Our basic model is already right.

**How to implement.**
- The only store is OpenBao (KV v2). The connector configuration contains only `openbao://<mount>/<path>#<key>` references; Beacon authenticates to OpenBao with its own method (AppRole / Kubernetes auth) under a policy limited to its own prefix.
- **Credential types** as a separate entity with a JSON Schema: `basic`, `bearer`, `header`, `hmac_secret`, `oauth2_client_credentials`, `mtls` (certificate + key), `api_key_query`. A node declares which types it accepts. Secrets are written through the Beacon API (**write-only**: the value cannot be read from the UI; a mask and the KV version are shown).
- OAuth2: client credentials first (machine sources); the access token is cached in memory until it expires; the refresh token (if needed) lives in OpenBao.
- Rotation: KV versioning; the connector picks up the new value without republishing (a cache with TTL, invalidated by version).
- Separation of rights: `credential:use` (select in a node) is separate from `credential:manage` (create/replace).
- Secrets are not available in expressions; values are masked in traces and errors (see 4.11, data redaction).
- A connector export never contains secrets, only a typed placeholder (see 4.16).

### 4.11. Execution history (executions)

**In n8n.** ([Understand executions](https://docs.n8n.io/build/understand-workflows/understand-executions)) A list of executions per workflow and overall, filters by status, each node's data (input/output), retrying a failed execution with the original or the current workflow, [copying execution data into the editor](https://docs.n8n.io/build/understand-workflows/understand-executions/debug-executions) for debugging, [custom execution metadata](https://docs.n8n.io/build/understand-workflows/understand-executions/customize-executions-data) for search. Per-workflow saving settings: successful / failed / manual / per-node progress. [Pruning](https://docs.n8n.io/deploy/host-n8n/configure-n8n/scaling/manage-execution-data): by age (336 h) and count (10,000), soft deletion with a buffer, annotated executions are not deleted (`packages/cli/src/services/pruning`). [Redacting execution data](https://docs.n8n.io/deploy/host-n8n/configure-n8n/security/redact-execution-data) (Enterprise): metadata is visible, the payload is not. [Concurrency control](https://docs.n8n.io/deploy/host-n8n/configure-n8n/scaling/control-concurrency) with a FIFO queue.

**Why.** To answer the question "why did this alert not become an incident / become the wrong one".

**Applicability: high** (the idea), with a different storage model.

**How to implement.**
- Do not save every "execution". Instead:
  - **Raw incoming requests**: an `ingest_requests` table (body up to a limit, headers without secrets, connector, version, time, processing status), partitioned by day, with partitions dropped after the retention period (days, configurable). This is both an intake audit and the source for reprocessing.
  - **Traces**, per connector: all requests with errors; a sample of successful ones (for example 1 in N, or the last K); on demand, "record everything for the next 10 minutes" while debugging. A trace = the input/output of every node for a specific request. Stored in `jsonb` with a size limit and a TTL.
  - **Counters**: aggregates in one-minute buckets (`connector_stats`: accepted, filtered, errors, events created, p50/p95 latency) + Prometheus metrics.
- **Event → request link**: the normalised event stores `ingest_request_id` and `lineage`, so the source request and trace can be opened from the incident card.
- **Reprocessing**: select requests (by filter or from the DLQ) → process them with the current published version; idempotency (4.20) prevents duplicate events.
- **Masking**: fields marked as secret in the schema and authorization headers do not get into traces; in addition, a path-masking rule at connector level; the `trace:read_payload` right is separate from `connector:read` (the equivalent of redaction in n8n).

### 4.12. Pinned data, partial execution, in-editor debugging

**In n8n.** [Pin and mock data](https://docs.n8n.io/build/work-with-data/pin-and-mock-data): a node's output can be "pinned" (and edited); in manual executions the node is not run and the pinned data is used instead; production executions ignore pins. [Partial execution](https://docs.n8n.io/build/understand-workflows/understand-executions/types-of-executions) "Execute step" runs the selected node and its required predecessors (`packages/core/src/execution-engine/partial-execution-utils`). [Dirty nodes](https://docs.n8n.io/build/understand-workflows/understand-executions/understand-dirty-nodes): when parameters, connections or pins change, the node and those after it are marked as stale. Disabling a node. Input/output panels with table / JSON / schema views; dragging a field from the input into a parameter creates an expression.

**Why.** To build the mapping on a real alert without waiting for the source to send something, and to see the result of every step.

**Applicability: high. The top priority for the builder.**

**How to implement.**
- **Samples**: bound to the connector; sources are webhook capture (4.3), pasting JSON by hand, import from `ingest_requests`/DLQ, the built-in preset samples (Alertmanager, Grafana, etc.). Stored together with the draft; optionally included in the export.
- **Test run**: the `POST /connectors/{id}/draft/test-run` API with a sample and, optionally, a target node: the server compiles the draft and executes up to the node in **side-effect-free mode**: `out.event` does not write to the pipeline but returns the final normalised event and the CI binding result (a dry run against the CMDB: which CI would be found); external HTTP nodes run only on an explicit button press.
- **Pinning a node's output**: the node's result is saved in the draft; later runs start from it. Useful for polling (no need to hit the external API on every edit).
- **Stale results**: the frontend keeps a hash of the node's parameters and inputs; when it changes, the node and all its descendants are marked; a re-run clears the mark. Pure client-side logic, no server involved.
- Input/output panels: table, JSON, "schema" (a tree of fields with types); dragging a field into a parameter inserts `${path}`. Highlighting of `map.event` fields that are not filled for the given sample.
- **Checking against the sample set**: "Run all samples" and show a table of results (severity, CI, status), a regression check before publishing. This develops the idea of n8n Evaluations for a deterministic pipeline.

### 4.13. Sub-workflows

**In n8n.** [Execute Sub-workflow](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.executeworkflow) + [Execute Sub-workflow Trigger](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.executeworkflowtrigger) with a schema of input fields; waiting for the result or running "in the background"; a "who can call this workflow" setting; [converting selected nodes into a sub-workflow](https://docs.n8n.io/build/flow-logic/convert-to-sub-workflows).

**Why.** Reusing shared logic (one severity table for every Zabbix, shared CI binding rules).

**Applicability: medium.**

**How to implement.** "Fragments" (shared fragments): a named subgraph with a declared input/output and its own version. A connector references a fragment **at a specific version** (or "latest published", as a deliberate choice). At compile time the fragment is inlined, with no call overhead. Separately, "lookup tables" (mapping tables as data, not as a graph): `map.severity` references a lookup table that can be edited without republishing connectors (with auditing). Lookup tables cover most of the need for reuse more simply than fragments.

### 4.14. Workflow versions and history

**In n8n.** ([Save and publish](https://docs.n8n.io/build/understand-workflows/save-and-publish-workflows), [View change history](https://docs.n8n.io/build/manage-workflows/view-change-history), `packages/cli/src/workflows/workflow-history`) Autosave every 1–5 s creates versions; production executions use only the published version; publishing is asynchronous and **applies only the triggers that changed**, while unchanged ones keep running without interruption; statuses "published, has changes", "partially published" (some triggers failed to activate), "publishing failed". Restoring, cloning, downloading a version, named versions (not deleted by pruning). Edit locking: one editor, everyone else is read-only. [Workflow reviews](https://docs.n8n.io/build/manage-workflows/workflow-reviews) (preview, Enterprise): submitting a version for approval, a visual diff, comments; on approval the version is published on the author's behalf. An important n8n limitation: approval does not cover credentials, variables and settings; those change immediately.

**Why.** To edit a live connector safely, roll back quickly, and understand who changed what.

**Applicability: high.**

**How to implement.**
- The draft is one mutable row per connector (`connector_drafts`) with autosave (debounce 1–2 s) and optimistic locking (`revision`); **edit locking** with a lease and an "N is editing" indicator.
- Publishing creates an immutable `connector_versions` row and compiles it; if compilation fails, publishing is rejected with the offending node identified. Switching the live version is atomic within a transaction; the engine is notified (`LISTEN/NOTIFY`) and swaps the compiled pipeline without stopping intake (requests in flight finish on the old version). The polling schedule is recreated only when it changes.
- Publication statuses: `published`, `published_with_changes`, `publish_failed` (with a reason).
- History: a list of versions, a diff of two versions (nodes added/removed/changed, parameters line by line; visually on the canvas, with highlighting), rollback = publishing an old version as a new one.
- Named versions and a publish comment; delete old unnamed versions after a retention period.
- Approval (four eyes) is stage 5; unlike n8n, the snapshot under approval includes credential references and fragment/lookup table versions.

### 4.15. Tags, folders, favourites

**In n8n.** [Tags](https://docs.n8n.io/build/manage-workflows/tag-workflows), folders, [favourites](https://docs.n8n.io/build/manage-workflows/favorite-items).

**Applicability: medium / low.** With dozens of connectors, filters by source type, status, owning team and tags are enough.

**How to implement.** A `tags` table + a many-to-many link; a filter in the connector list. Do not add folders; projects/teams play their role (4.17).

### 4.16. Templates, import and export

**In n8n.** [Templates](https://docs.n8n.io/build/ways-of-building-workflows/use-templates) from the public library or [your own](https://docs.n8n.io/deploy/host-n8n/configure-n8n/basic-configuration/configuration-examples/configure-a-custom-workflow-templates-library); [JSON export/import](https://docs.n8n.io/build/manage-workflows/export-and-import) (copying nodes via the clipboard), a CLI; the new [`.n8np` packages](https://docs.n8n.io/build/manage-workflows/n8n-packages/export-a-package) with dependencies (sub-workflows, variables, credentials without values) and [pre-import checks](https://docs.n8n.io/build/manage-workflows/n8n-packages/how-import-works) with credential mapping.

**Why.** A quick start with a typical source; moving between environments; sharing configurations.

**Applicability: high** (presets are part of the initial setup wizard).

**How to implement.**
- Export format: JSON with a `format_version` field, the graph, samples (optional), references to lookup tables/fragments, and **placeholders** for credentials (`{ "credential_slot": "zabbix_api", "type": "bearer" }`).
- Import: check the schema and node type versions → a wizard that maps placeholders to existing credentials or creates new ones (the value is entered and written straight to OpenBao) → create a draft (not a publication).
- Presets = built-in templates in the same format, shipped with the binary (`embed.FS`) and versioned together with Beacon; each has a set of samples for checking (4.12).
- Copying/pasting nodes in the editor via the clipboard in the same format.

### 4.17. RBAC and projects

**In n8n.** ([Set permissions and roles](https://docs.n8n.io/administer/manage-users-and-access/set-permissions-and-roles-rbac)) Instance roles (owner, admin, member) and project roles (admin, editor, viewer); [projects](https://docs.n8n.io/administer/manage-users-and-access/set-permissions-and-roles-rbac/organize-work-in-projects) group workflows, credentials and executions; [custom roles](https://docs.n8n.io/administer/manage-users-and-access/set-permissions-and-roles-rbac/create-custom-roles) built from a set of scopes such as `workflow:read`, `workflow:publish`, `credential:share` (`packages/@n8n/permissions`); [LDAP](https://docs.n8n.io/administer/manage-users-and-access/verify-user-identity/connect-ldap), SAML, OIDC, 2FA. Most of it is in the paid editions.

**Why.** The Zabbix team edits its own connectors and cannot see the cloud team's secrets; the on-call engineer only views.

**Applicability: high** (scopes and the link to LDAP/AD groups), **medium** (projects).

**How to implement.**
- Model: **permissions (scopes)** in code (`connector:read`, `connector:edit`, `connector:publish`, `connector:code`, `credential:use`, `credential:manage`, `trace:read_payload`, `sample:manage`, etc.) → **roles** as sets of permissions (built-in + custom) → **assignments** of a role to a user or an LDAP/AD group over a scope (the whole instance or a team).
- A "team" (the equivalent of a project) owns connectors, credentials and lookup tables; it corresponds to an AD group. A team's secrets get a separate prefix in OpenBao and a separate policy.
- Checks are done in a single Go middleware at the API level; the frontend only hides what is unavailable (based on the current user's list of permissions).
- All configuration changes and publications go to the audit log (we already have one).

### 4.18. Environment variables (custom variables)

**In n8n.** [Custom variables](https://docs.n8n.io/build/code-in-n8n/define-custom-variables) `$vars.<name>`: non-secret string constants at instance/project level, different in different environments (a paid feature). Separately, process environment variables configure n8n itself.

**Why.** The same connector on test and production environments with different addresses.

**Applicability: medium.**

**How to implement.** A `variables (scope, name, value)` table; accessible in templates and CEL as `vars.name`; changing a variable does not require republishing, but it is recorded in the audit log and shown in the connector's list of dependencies. Secrets never go into variables. Beacon's own configuration stays in process environment variables and OpenBao, as now.

### 4.19. Queue mode, scaling and reliability

**In n8n.** [Queue mode](https://docs.n8n.io/deploy/host-n8n/configure-n8n/scaling/enable-queue-mode) (`packages/cli/src/scaling`): main receives triggers and creates the execution, Redis (Bull) holds a queue of IDs, workers (10 parallel jobs by default) take the execution from the database, execute it and write the result; separate webhook intake processes behind a load balancer; multi-main with a leader (Enterprise), where the leader runs "exactly-once" tasks (timers, polling, pruning). A webhook response from a worker is passed via Redis (64 MiB limit). Graceful shutdown of 30 s, `/healthz`, `/healthz/readiness`, `/metrics`, OpenTelemetry. [Concurrency control](https://docs.n8n.io/deploy/host-n8n/configure-n8n/scaling/control-concurrency) in regular mode, queue recovery after a restart (`packages/cli/src/concurrency`). The repository has ongoing work on a new execution engine (`packages/cli/src/modules/engine-v2`, with a control plane / data plane split); it is not publicly documented, so it is not analysed here.

**Why.** Throughput, fault tolerance, no losses on restart.

**Applicability: high** (the requirements), **not needed** (the Redis/Bull implementation).

**How to implement.**
- **Event path**: HTTP intake → authentication/limit checks → a write to `ingest_requests` (a single insert, with batched commits every few ms for peaks) → 202. Separate handler goroutines pick up unprocessed rows (`FOR UPDATE SKIP LOCKED`, in batches), run the compiled pipeline, and **in the same transaction** write the normalised events and mark the request as processed. This is at-least-once without an external broker, with recovery after a crash.
- **Scaling**: several Beacon instances behind a load balancer, all equal; singleton tasks (dropping partitions, materialising schedules) run under `pg_try_advisory_lock` or via row claiming, without a dedicated leader.
- **Broker** (NATS JetStream or Kafka): only if measurements show PostgreSQL becoming the bottleneck; make the queue interface in Go abstract from day one.
- **Backpressure**: a rate limit and queue size per connector; on overflow, 429/503 to the source (monitoring sources usually retry) rather than growing memory. Fairness between connectors: a "noisy" source must not delay the others (separate queues/quotas for handlers).
- **Timeouts and cancellation**: `context.Context` per batch and per node; graceful shutdown: stop accepting, finish what has been claimed, release leases.
- **Observability**: `/healthz`, `/readyz`, Prometheus (per connector: accepted, processed, dropped, errors, latency, queue length), OpenTelemetry traces with `ingest_request_id`.
- A load test (k6 or a Go generator) is an acceptance criterion for every stage: target throughput and p99 latency from intake to event.

### 4.20. Idempotency and deduplication

**In n8n.** There is no single mechanism: Remove Duplicates against execution history, polling cursors, a deduplication key for scheduled executions (`deduplicationKey`; a comment in the migration mentions future webhook idempotency keys).

**Applicability: high. This is Beacon's core, not a node.**

**How to implement.** An event's idempotency key is computed in `map.event`: by default `(connector_id, external_id, status)` or a hash of selected fields (`fingerprint`). A unique index in the intake table with a window (time-based partitioning): a repeated delivery of the same alert does not create a new event but updates "last seen". For webhooks, an optional `Idempotency-Key` header. Collapsing events into incidents is the next level, separate from delivery idempotency.

### 4.21. Community nodes and the node SDK

**In n8n.** [Creating nodes](https://docs.n8n.io/connect/create-nodes): two styles, [declarative](https://docs.n8n.io/connect/create-nodes/build-your-node/tutorial-build-a-declarative-style-node) (HTTP requests described in a JSON-like structure, `packages/core/src/execution-engine/routing-node.ts`) and programmatic (`execute()`); the `@n8n/node-cli` CLI, a linter, node versions. [Community nodes](https://docs.n8n.io/integrations/community-nodes): installing npm packages from the UI, [risks](https://docs.n8n.io/integrations/community-nodes/risks) (full access to the machine), verified nodes, a blocklist, the ability to [block nodes](https://docs.n8n.io/deploy/host-n8n/configure-n8n/security/block-specific-nodes).

**Why.** Extension without involving the core team.

**Applicability:** third-party executable code is **not needed**; declarative source descriptions are **medium**.

**How to implement.** The internal Go node interface (4.1) is our "SDK"; new node types are added by the Beacon team. Instead of third-party plugins, **declarative source descriptions**: a JSON/YAML file (polling URL, request parameters, authentication, path to the array, pagination, cursor, default mapping, samples) from which a preset is built. Such descriptions can be loaded without recompiling and contain no executable code. WASM plugins only if real demand appears, and after the sandbox (4.7).

### 4.22. AI nodes (briefly)

**In n8n.** [AI Agent](https://docs.n8n.io/integrations/builtin/cluster-nodes/root-nodes/n8n-nodes-langchain.agent) and cluster nodes built on LangChain (models, memory, tools, vector stores), an MCP client and an MCP server, Guardrails, [evaluations](https://docs.n8n.io/build/integrate-ai/test-and-improve-ai-workflows/understand-why-to-test), an AI assistant in the editor and code generation.

**Applicability: low for the pipeline; medium as an auxiliary editor feature in the future.** An LLM on the hot path contradicts the requirements for latency, determinism and, often, isolation of the customer's network.

**How to implement (optional, stage 6).** "Suggest mapping": given a sample alert, the model proposes how to fill `map.event` and the severity table; the user checks it against the samples (4.12) and accepts it. The model is pluggable and can be deployed locally; disabled by default; sample data is masked before sending.

### 4.23. Source control and environments

**In n8n.** ([Use source control and environments](https://docs.n8n.io/administer/use-source-control-and-environments)) Connecting a Git repository, push/pull of workflows, credential stubs, variables, tags; environments as branches or separate instances; a diff before push/pull; an API for CI. A paid feature.

**Why.** Moving a tested configuration from the test environment to production; GitOps.

**Applicability: medium / low** in the near-term stages.

**How to implement.** Start with export/import (4.16) and the API; then "configuration as code": a directory of YAML/JSON (connectors, lookup tables, fragments, credential placeholders), a `umbrella config apply --dry-run` command with a diff, applied via the API with a service account's rights. Built-in Git integration in the UI comes after that, and only on request.

### 4.24. Event logs, audit and analytics

**In n8n.** [Log streaming](https://docs.n8n.io/administer/observe-and-log/stream-logs-to-external-systems) (audit and execution events to external systems: syslog, webhook, Sentry), [Insights](https://docs.n8n.io/administer/observe-and-log/track-usage-with-insights) (number of executions, failure rate, time), [OpenTelemetry](https://docs.n8n.io/deploy/host-n8n/keep-n8n-running/trace-executions-with-opentelemetry), [security audit](https://docs.n8n.io/deploy/host-n8n/configure-n8n/security/run-security-audits).

**Applicability: high** (connector statistics), **medium** (streaming the audit log).

**How to implement.** A connector page: charts of accepted/filtered/failed/created events, latency, last activity, source "silence" (from `connector_stats`). Audit: the existing log + export to syslog/webhook when configured. Use the frontend's own chart components, without embedding external dashboards into the connector page.

## 5. Applicability summary table

| # | n8n feature | Applicability | What we take (the idea) | Complexity | Stage |
| --- | --- | --- | --- | --- | --- |
| 4.1 | Workflow model, items, node type versions | High | Graph + node type registry with JSON Schema, compilation on publish, record lineage | Medium | 0 |
| 4.2 | Binary data | Low | — | — | — |
| 4.3 | Webhook trigger | High | Sample capture, authentication, CIDR, limits, 202 after recording, HMAC | Medium | 1 |
| 4.4 | Schedule and polling | High | Durable scheduler in PG, leases, misfire, cursor in the same transaction | High | 3 |
| 4.5 | Event triggers | Medium | Subscriptions to a bus/syslog/SNMP with claiming | High | 6 |
| 4.6 | Expressions | High (UX) | Our own template language + CEL, server-side preview | Medium | 0–1 |
| 4.7 | Code node, task runners | Low | Starlark/WASM sandbox in a separate process | High | 6 |
| 4.8 | IF/Switch/Filter/Merge/Loop/Wait | High for Switch/Filter | `route.switch`, `filter`, `enrich.lookup`; no Loop/Wait/Merge | Low | 4 |
| 4.9 | Error handling | High | Node error policy, retries, DLQ, built-in meta-monitoring instead of an error workflow | Medium | 1 + 3 + 4 |
| 4.10 | Credentials | High | Credential types, write-only, `credential:use` ≠ `credential:manage`, OAuth2 CC | Medium | 1 |
| 4.11 | Execution history | High | Raw requests + sampled traces + aggregates, reprocessing, masking | Medium | 2 |
| 4.12 | Pinned data, partial execution | High | Samples, dry run up to a node, pinning, dirty marks, running all samples | Medium | 2 |
| 4.13 | Sub-workflows | Medium | Lookup tables; later, versioned fragments inlined at compile time | Medium | 4 |
| 4.14 | Versions, publishing, approval | High | Draft autosave, atomic publishing, diff, rollback, edit locking | Medium | 1 (basics), 5 (diff, approval) |
| 4.15 | Tags, folders | Low/medium | Tags and filters | Low | 5 |
| 4.16 | Templates, import/export | High | Presets as templates, credential placeholders, mapping wizard | Medium | 1 |
| 4.17 | RBAC, projects | High | Scopes → roles → assignment to a user/AD group for a team | Medium | 5 |
| 4.18 | Variables | Medium | `vars.*` without secrets | Low | 4 |
| 4.19 | Queue mode, scaling | High (requirements) | PG as a queue, SKIP LOCKED, backpressure, no Redis | High | 1 (basics), 3 |
| 4.20 | Deduplication | High | Idempotency in the core | Medium | 1 |
| 4.21 | Community nodes, SDK | Medium (declarative sources) | Declarative source descriptions without code | Medium | 5–6 |
| 4.22 | AI nodes | Low | Mapping suggestions in the editor | Medium | 6 |
| 4.23 | Source control, environments | Medium/low | Configuration as code + dry-run apply | Medium | 6 |
| 4.24 | Log streaming, Insights | High/medium | Connector statistics, audit export | Low | 2–5 |

## 6. Prioritised implementation plan

Estimates are in person-weeks for one experienced full-stack developer, not counting the initial setup wizard (it comes first and sets up the base entities: users, LDAP, the OpenBao connection). Within a stage, items are ordered by priority. A rule for all stages: the UI shows only implemented node types and features, with no placeholders.

### Stage 0. Engine and model foundation (3–4 weeks, medium complexity)

1. Database schema: `connectors`, `connector_drafts`, `connector_versions`, `ingest_requests` (partitioned by day), `ingest_failures`, `connector_stats`.
2. Graph format (JSON) with `type_version` and stable node IDs; a graph validator.
3. Node type registry in Go: parameter JSON Schema, inputs/outputs, `Compile` → `Step`.
4. The `${…}` template language (extended) and CEL integration with a cost limit; an expression preview service.
5. An executor for the compiled pipeline, working in batches with `context` timeouts and lineage.

*Risks:* redesigning the model later is expensive, so build in node type versions and the export format right away; the temptation to "do it like n8n" with JS expressions.

### Stage 1. Webhook connector MVP (4–5 weeks, medium complexity)

1. Nodes: `trigger.webhook`, `parse.json` (array selection), `map.severity` (table), `enrich.labels`, `map.event` (with target schema validation and an idempotency key), `out.event`, `ack.response`.
2. Intake: authentication (token, Basic, HMAC), CIDR, size and rate limits, recording in `ingest_requests` → 202; handlers based on `SKIP LOCKED`; event idempotency.
3. Credentials: types, write-only storage in OpenBao, `openbao://` references, a cache with TTL.
4. A draft with autosave and edit locking; publishing with compilation and atomic version switching; statuses.
5. Presets for Alertmanager, Grafana, Zabbix (webhook mode) and a generic webhook, as templates with built-in samples; import/export.
6. A DLQ for parse errors with manual reprocessing; connector counters.
7. The @xyflow editor: a palette of implemented nodes, forms generated from JSON Schema, graph validation.

*Risks:* PostgreSQL write performance at peaks, so run a load test immediately; the choice of response behaviour towards the source (202 before processing) must be agreed against the sources' requirements.

### Stage 2. In-editor debugging (3–4 weeks, medium complexity)

1. Samples: capture from a webhook, manual entry, from `ingest_requests`/DLQ, from presets.
2. A test run of the draft up to a selected node in side-effect-free mode (including a dry run of CI binding).
3. Input/output panels (table, JSON, schema), dragging a field into a parameter, path autocompletion in expressions.
4. Pinning a node's output, client-side dirty marks.
5. Running all samples before publishing (a results table).
6. Sampled traces of production requests, the event → request → trace link, masking, a right to view the payload.

*Risks:* behaviour of the test run diverging from production execution: use the same compiled code and differ only in the `out.event` "sink"; secrets leaking through samples: mask them at capture time.

### Stage 3. Polling and reliability (4–5 weeks, high complexity)

1. `trigger.poll`: an HTTP request with credentials, pagination, a cursor.
2. Durable scheduler: materialisation, claiming with a lease, the "reaper", a misfire policy, jitter, timeouts, exponential backoff.
3. The cursor in the same transaction as recording the results.
4. Retries and a circuit breaker for external calls.
5. Backpressure and fairness between connectors; graceful shutdown.
6. Meta-monitoring: internal events "connector degraded / source went silent / errors above threshold" → incident → notification.
7. Prometheus and OpenTelemetry metrics; presets for polling the Zabbix API and OpenSearch.

*Risks:* double polling on failures (at-least-once), covered by the cursor-in-transaction and idempotency; time zones and daylight saving changes in cron, covered by tests; an event storm on a "catch-up" run, so by default do not catch up.

### Stage 4. Logic and reuse (2–3 weeks, low/medium complexity)

1. `route.switch`, `filter` (with a filtered-out counter).
2. Node error policy (`fail_record` / `skip` / `route_error`).
3. `enrich.lookup` (CMDB, lookup tables).
4. Lookup tables as editable data; `vars.*` variables.
5. Versioned fragments (if lookup tables turn out not to be enough).

*Risks:* growing graph complexity: limit the number of nodes and the depth, and show warnings about CEL cost.

### Stage 5. Access and lifecycle (3–4 weeks, medium complexity)

1. Scopes → roles → assignments to users and LDAP/AD groups; teams as owners of connectors and secrets, OpenBao prefixes and policies per team.
2. Version history with a diff (structural and on the canvas), rollback, named versions.
3. Publish approval (four eyes), with credential references and lookup table versions included in the snapshot.
4. Tags and filters; audit log export.
5. Declarative source descriptions for presets.

*Risks:* mapping AD groups to roles requires a decision from the customer on the access model; migrating existing connectors when teams are introduced.

### Stage 6. Optional extensions (on request)

| Item | Complexity | Main risk |
| --- | --- | --- |
| Event triggers (message bus, syslog, SNMP trap) | High | Distributing subscriptions, ordering and offset commits |
| A sandboxed user code node (Starlark/WASM, separate process) | High | Security, latency, user support |
| Configuration as code, then Git sync | Medium | Conflicts between UI edits and Git |
| A message broker instead of the PG queue | High | Operational complexity; do it only based on measurements |
| LLM-assisted mapping suggestions | Medium | Network isolation, quality, data masking |
| Automation flows (reactions to incidents) as a separate graph type | High | The product drifting towards an iPaaS |

**Timeline summary:** stages 0–3 (a working builder with webhook and polling, debugging and reliability) take roughly 14–18 person-weeks; with stages 4–5, roughly 20–25.

## 7. What not to take from n8n

| What | Why |
| --- | --- |
| **n8n code, node schemas, texts, icons** | The Sustainable Use License forbids commercial use and redistribution; `.ee` files are under a separate commercial licence. We take only ideas, implement them from scratch and do not open n8n sources as a template while writing our own code |
| The catalogue of hundreds of integrations | Beacon is not an iPaaS; maintaining hundreds of nodes is expensive. Generic webhook/polling/bus + presets and declarative source descriptions are enough |
| JavaScript in `{{ }}` expressions | Risk of arbitrary code execution (it took n8n years and V8 isolation), unpredictable latency, hard to analyse. CEL + templates cover the monitoring use cases |
| A Code node on the hot path and the executor's "internal" mode | By n8n's own documentation this gives access to the database, keys and secrets; unacceptable for a system holding notification channel tokens and a CMDB |
| Saving every execution with all its data by default | With an event stream this bloats the database and writes at every step; we use raw requests + sampled traces + aggregates |
| Queue mode on Redis/Bull, separate webhook processes | A consequence of the Node.js model; in Go goroutines and PostgreSQL are enough, and an extra component is an extra point of failure |
| Branch execution order based on canvas coordinates | Moving a node changes behaviour, which is unacceptable for a deterministic pipeline; topological order only |
| Wait nodes and long-lived executions in the ingest pipeline | State, resumption, timers all kill latency; escalations are the job of the built-in notification system |
| Referencing nodes by name (`$('Node name')`) | Renaming breaks expressions; we have stable IDs |
| Loop / Split in Batches, a generic Merge with many modes | The pipeline is batch-based by nature; merging is replaced by lookup enrichment |
| Error workflow as a user-built workflow | Monitoring of our own connectors must work out of the box: built-in meta-monitoring through the normal incident path |
| Encrypting secrets with an instance key in the application database | OpenBao is our only store; keeping secrets in PostgreSQL, even encrypted, would be a step back |
| Installing third-party nodes from npm via the UI | Supply chain risk and full access of node code to the process; instead, declarative descriptions without code |
| AI agents, LangChain nodes, vector stores, MCP triggers | Not a job for a monitoring pipeline; non-determinism and external dependencies |
| Splitting features across paid editions, telemetry, licence keys | Not relevant to our product model |
| Approval that does not cover credentials and settings | An n8n limitation that is stated explicitly in its documentation; our approval snapshot includes all dependencies |
| Evaluating expressions in the browser for preview | Risk of diverging from the server and of running untrusted code in the browser (n8n still uses a non-isolated engine in the editor by default); preview runs on the server |

## 8. Open questions

1. Target load figures: peak event rate per instance and the acceptable "intake → incident" latency (this determines whether PostgreSQL is enough as a queue).
2. Retention periods for raw requests and traces (information security requirements and disk space).
3. Response to the source: always 202 after recording, or do some sources need a synchronous response with the result?
4. Is user code needed at all, or are CEL and lookup tables enough (this determines whether to plan stage 6.2)?
5. The team/project model and its mapping to AD groups is a decision for the customer.
6. Should connectors from the previous version be migrated automatically (a converter from the old format to the new one), or are presets enough?

## 9. References

Documentation (docs.n8n.io):

- Data model: [data structure](https://docs.n8n.io/build/work-with-data/understand-n8ns-data-structure), [item linking](https://docs.n8n.io/build/work-with-data/reference-data/link-data-items), [binary data](https://docs.n8n.io/build/work-with-data/handle-special-data-types/work-with-files-and-images)
- Expressions and code: [expressions](https://docs.n8n.io/build/work-with-data/transform-data/expressions-for-data-transformation), [expression reference](https://docs.n8n.io/build/work-with-data/transform-data/expression-reference), [expression engine](https://docs.n8n.io/deploy/host-n8n/configure-n8n/basic-configuration/use-environment-variables/expression-engine), [Code node](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.code), [task runners](https://docs.n8n.io/deploy/host-n8n/configure-n8n/set-up-task-runners), [hardening](https://docs.n8n.io/deploy/host-n8n/configure-n8n/security/harden-task-runners)
- Triggers: [Webhook](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.webhook), [Schedule Trigger](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.scheduletrigger), [durable scheduler](https://docs.n8n.io/deploy/host-n8n/configure-n8n/durable-scheduler)
- Logic: [conditionals](https://docs.n8n.io/build/flow-logic/split-with-conditionals), [merge](https://docs.n8n.io/build/flow-logic/merge-data), [loop](https://docs.n8n.io/build/flow-logic/loop), [wait](https://docs.n8n.io/build/flow-logic/wait), [execution order](https://docs.n8n.io/build/flow-logic/understand-execution-order), [sub-workflows](https://docs.n8n.io/build/flow-logic/break-workflows-into-smaller-parts), [error handling](https://docs.n8n.io/build/flow-logic/handle-errors-gracefully), [Remove Duplicates](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.removeduplicates)
- Executions and debugging: [executions](https://docs.n8n.io/build/understand-workflows/understand-executions), [types of executions](https://docs.n8n.io/build/understand-workflows/understand-executions/types-of-executions), [dirty nodes](https://docs.n8n.io/build/understand-workflows/understand-executions/understand-dirty-nodes), [debug executions](https://docs.n8n.io/build/understand-workflows/understand-executions/debug-executions), [pin and mock data](https://docs.n8n.io/build/work-with-data/pin-and-mock-data), [manage execution data](https://docs.n8n.io/deploy/host-n8n/configure-n8n/scaling/manage-execution-data), [redaction](https://docs.n8n.io/deploy/host-n8n/configure-n8n/security/redact-execution-data)
- Lifecycle: [save and publish](https://docs.n8n.io/build/understand-workflows/save-and-publish-workflows), [change history](https://docs.n8n.io/build/manage-workflows/view-change-history), [reviews](https://docs.n8n.io/build/manage-workflows/workflow-reviews), [workflow settings](https://docs.n8n.io/build/manage-workflows/configure-workflow-settings), [tags](https://docs.n8n.io/build/manage-workflows/tag-workflows), [export/import](https://docs.n8n.io/build/manage-workflows/export-and-import), [packages](https://docs.n8n.io/build/manage-workflows/n8n-packages/export-a-package), [templates](https://docs.n8n.io/build/ways-of-building-workflows/use-templates)
- Access and secrets: [RBAC](https://docs.n8n.io/administer/manage-users-and-access/set-permissions-and-roles-rbac), [roles](https://docs.n8n.io/administer/manage-users-and-access/set-permissions-and-roles-rbac/see-available-roles), [projects](https://docs.n8n.io/administer/manage-users-and-access/set-permissions-and-roles-rbac/organize-work-in-projects), [LDAP](https://docs.n8n.io/administer/manage-users-and-access/verify-user-identity/connect-ldap), [share credentials](https://docs.n8n.io/administer/manage-credentials/share-credentials-securely), [external secrets](https://docs.n8n.io/administer/manage-credentials/use-external-secret-stores), [key rotation](https://docs.n8n.io/deploy/host-n8n/configure-n8n/security/rotate-encryption-keys), [variables](https://docs.n8n.io/build/code-in-n8n/define-custom-variables)
- Operations: [queue mode](https://docs.n8n.io/deploy/host-n8n/configure-n8n/scaling/enable-queue-mode), [concurrency](https://docs.n8n.io/deploy/host-n8n/configure-n8n/scaling/control-concurrency), [log streaming](https://docs.n8n.io/administer/observe-and-log/stream-logs-to-external-systems), [insights](https://docs.n8n.io/administer/observe-and-log/track-usage-with-insights), [source control](https://docs.n8n.io/administer/use-source-control-and-environments), [editions](https://docs.n8n.io/deploy/host-n8n/community-edition-features)
- Extension: [create nodes](https://docs.n8n.io/connect/create-nodes), [node building styles](https://docs.n8n.io/connect/create-nodes/plan-your-node/choose-a-node-building-style), [community nodes](https://docs.n8n.io/integrations/community-nodes), [community nodes risks](https://docs.n8n.io/integrations/community-nodes/risks), [AI Agent](https://docs.n8n.io/integrations/builtin/cluster-nodes/root-nodes/n8n-nodes-langchain.agent)

Source code (github.com/n8n-io/n8n, master branch; only for understanding the principles):

- Licences: `LICENSE.md`, `LICENSE_EE.md`
- Engine: `packages/core/src/execution-engine/workflow-execute.ts`, `packages/core/src/execution-engine/partial-execution-utils/`, `packages/core/src/execution-engine/routing-node.ts`, `packages/core/src/execution-engine/triggers-and-pollers.ts`
- Model and expressions: `packages/workflow/src/interfaces.ts`, `packages/workflow/src/expression.ts`, `packages/workflow/src/workflow-data-proxy.ts`, `packages/@n8n/expression-runtime/`
- User code: `packages/@n8n/task-runner/`, `packages/@n8n/task-runner-python/`
- Scaling and scheduler: `packages/cli/src/scaling/`, `packages/cli/src/concurrency/`, `packages/@n8n/scheduler/`, `packages/cli/src/services/pruning/`
- Activation and history: `packages/cli/src/active-workflow-manager.ts`, `packages/cli/src/workflows/workflow-history/`
- Secrets and access: `packages/core/src/encryption/`, `packages/cli/src/modules/external-secrets.ee/`, `packages/@n8n/permissions/`
- Nodes: `packages/nodes-base/nodes/Webhook/`, `Schedule/`, `If/`, `Switch/`, `Merge/`, `SplitInBatches/`, `Wait/`, `Code/`, `ErrorTrigger/`, `ExecuteWorkflow/`, `Transform/RemoveDuplicates/`
- Schedule idempotency: `packages/@n8n/db/src/migrations/common/1778000000000-AddExecutionDeduplicationKey.ts`
