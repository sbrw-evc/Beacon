# Monitoring systems: Zabbix and Prometheus

The **Automation → Monitoring systems** page is one place for a single Zabbix or Prometheus-compatible server (Prometheus, VictoriaMetrics, Thanos, Mimir). Each system is a card with tabs:

- **Alert intake** — the linked connector, the intake address, the time of the last event and the "Send a test event" button. If there is no intake yet, the **Connect the alert intake** button does everything at once (see [connectors.md](connectors.md#quick-source-connection)): a token, a connector from the Zabbix or Alertmanager template, publishing, a ready media type or receiver. An existing connector can be chosen instead;
- **Hosts** — how many hosts were read and matched with CIs, and when, plus a link to the list of hosts of this system;
- **RED/USE metrics** (Prometheus only) — the system itself serves as a metric source for RED/USE rules: it can be chosen as the source in a rule, and a separate "Metric source" with the same address and credentials is not needed.

The address and credentials of a system are set once (the **Settings** button). The system's credentials are **outgoing**: Umbrella uses them to read hosts and metrics. The intake token is **incoming**: Zabbix or Alertmanager uses it to send alerts to Umbrella. These are two different secrets working in opposite directions.

A system reads lists of hosts and matches them with CIs, including those loaded from NetBox. This shows which CIs are monitored and which monitoring hosts are not described in the CMDB.

How the three pages relate: alert intake — **Connectors**; host lists and matching with CIs — **Monitoring systems**; metrics for rules — Prometheus systems or **Metric sources** on the RED/USE rules page.

## Connecting

The **Systems → Add a system** tab:

| Field | What to enter |
| --- | --- |
| System | Zabbix or Prometheus |
| Address | Zabbix — the web interface address (`https://zabbix.example.com`, the API is taken from `api_jsonrpc.php`); Prometheus — the HTTP API address |
| Credential | from **Automation → Credentials**, secrets are stored in OpenBao. Zabbix: an API token (bearer) or a name and password (basic, the session is closed after reading). Prometheus: bearer, basic or header, if the server requires them |
| Query, Host label | Prometheus only: `up` and `instance` by default; the port and URL scheme are dropped, all targets of one host (node, postgres, blackbox) are one host |
| Read every | interval in minutes, 0 — only with the button |

Zabbix 6.4 and newer receives the token in the `Authorization` header, older versions — in the `auth` field. The version is detected automatically via `apiinfo.version`. Tested on Zabbix 7.0 and 8.0 from [`deploy/zabbix`](../../deploy/zabbix).

## Matching

A host is matched with a CI by the same keys by which events find CIs (`alert.CIKeys` and `alert.EventKeys`): the technical and visible host name, its DNS names and IP addresses against the CI name, short name, IP addresses and the computer's DNS name in AD. A name shared by several CIs matches nothing: the host goes to "Several candidates".

On the **Hosts** tab, an unmatched host can be:

- **Linked** to a CI by hand — a manual link takes precedence over the automatic one;
- marked **Not a configuration item** — for test hosts and Zabbix templates;
- turned into a CI with **Create CI**, using the host's name and addresses, optionally registering it in NetBox right away.

## Links and incidents

Manual links also apply to alerts. When an event names a host (the `ci` field or the `ci` label), the incident engine first looks for it among hosts with a manual link — by technical and visible name, short names, DNS names, IP addresses and Prometheus target addresses (`host:port`) — and only then matches by CI name, IP and DNS:

- the host is **linked to a CI** — the incident gets that CI and its route (business services, team, people), even if the host name does not match the CI name;
- the host is marked **Not a configuration item** — the incident is opened but suppressed: it is not sent to PagerDuty or via the fallback notification, it is marked "Host excluded" in the list, and the timeline shows the reason;
- a name on which two manual links disagree decides nothing: automatic matching applies.

After a link changes (link, mark, return to automatic matching, create a CI from a host), open incidents are re-matched immediately: an incident moves to the new CI and route, loses its CI, or becomes suppressed; when the mark is removed, the incident is sent as usual. The same happens after any change of the catalog. For this, the incident remembers the name from the first event (`event_ci`). An incident is not moved to a CI that already has an open incident for the same signal.

## One CI for all systems

One machine — one CI, no matter how many systems see it. Hosts from different systems with a shared name or address end up in one CI. If there is no CI yet, an unmatched host shows "Same machine in: …": **Create CI** creates one CI and links the hosts of this machine in all systems to it.

## Bulk CI creation

On the **Hosts** tab, hosts without a CI have checkboxes (the checkbox in the header selects all shown). **Create items…** creates CIs for all selected hosts at once (up to 500), with the chosen type and, optionally, registration in NetBox. The "one machine — one CI" rule applies here too: Zabbix hosts are processed first, so the CI gets the technical name from Zabbix, and the same machine from Prometheus is linked to it ("Linked to an item made in this batch"). A host that already has a CI by that moment is not changed. The result is shown for each host; if the same error (for example, NetBox is unavailable) repeats three times in a row, the remaining hosts are not processed and are marked "Skipped".

## Bulk binding of CIs to business services

In **Configuration items** the rows have checkboxes (the `services:edit` permission is required). The selected CIs can be bound with **Bind to services…** or unbound with **Unbind from services…** — to or from several services at once. In a service card you can tick several found CIs and press **Bind selected**, or tick bound ones and press **Unbind selected**. For services linked with NetBox, the service tag is set or removed on the CIs from NetBox; if NetBox refuses for some CI, its binding is not changed while the rest are carried out. The result is shown for each "CI — service" pair.

## Where the CI is present

In the CI card, the **Present in** row lists NetBox (if it is connected or the CI is linked with it), Active Directory (if the CI was checked) and every enabled monitoring system, each marked "Yes" or "No". For monitoring systems, the host state and a link to the host are shown. In **Configuration items** the same is shown in the "Present in" column: systems where the CI is present are highlighted in colour, those where it is absent are struck through. The **Not monitored** tile counts devices and VMs (except planned ones and those being decommissioned) that no enabled system sees.

## Permissions

The `monitoring` page: view, connection check, edit, host loading and linking hosts to CIs. Creating a CI from a host also requires `cis:edit`. Connecting the alert intake also requires `connectors:edit`, `connectors:publish` and `credentials:edit`; the test event — `connectors:edit` or `monitoring:edit`.

A Prometheus system that RED/USE rules query cannot be deleted or turned into Zabbix until the rules are moved (`source_in_use`).

## Metric sources

Metric sources on the RED/USE rules page keep working. A source whose address matches a Prometheus system can be merged with **Merge into the system**: the rules move to the system together with their state, and the source is deleted. A source without such a system can be turned into one with **Make a monitoring system** — Umbrella creates a Prometheus system with its address and credentials (`POST /api/metric-sources/{id}/merge`, permissions `rules:edit` and `monitoring:edit`).

## API

| Method and path | Purpose |
| --- | --- |
| `GET /api/monitoring` | systems with the intake connector (`connector`, last event) and the number of rules (`rules`) |
| `POST`, `PUT`, `DELETE /api/monitoring/sources[/{id}]` | system (`connector_id` — the intake connector) |
| `PUT /api/monitoring/sources/{id}/connector` | link the intake connector (`{connector_id}`, empty — unlink) |
| `POST /api/monitoring/link` | manual host link; open incidents are re-matched |
