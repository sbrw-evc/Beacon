# Grafana

Grafana 13 in the shared `umbrella` network with a ready "Umbrella" contact point and the incident context dashboard.

```sh
cp .env.example .env   # set GRAFANA_ADMIN_PASSWORD and UMBRELLA_INGEST_TOKEN
docker compose up -d
```

Interface: http://localhost:3000, sign in as `admin` with the password from `.env`. If Grafana is opened at a different address, set it in `GRAFANA_ROOT_URL`: the links in notifications are built from it.

## Grafana alerts → Umbrella

The easiest way: in Umbrella, **Connectors → Connect a source → Grafana** — the token, the connector and the publication are made at once, and the address and header for the contact point are shown. Manually:

1. In Umbrella: **Automation → Credentials** — create a Bearer token. **Connectors** — create a connector from the "Grafana Alerting" template, choose the token in the webhook node and publish it.
2. In the Grafana `.env`: `UMBRELLA_INGEST_URL` — the connector's intake address (`http://umbrella:8080/api/ingest/<slug>` if Umbrella is run from `deploy/umbrella` on this host), `UMBRELLA_INGEST_TOKEN` — the token. Then `docker compose up -d`.
3. The "Umbrella" contact point is created from [`provisioning/alerting/umbrella.yaml`](provisioning/alerting/umbrella.yaml) with the header `Authorization: Bearer <token>`. Select it in **Alerting → Notification policies** (the default policy or a nested one). You can check delivery with the **Test** button in **Alerting → Contact points**.

The preset takes the severity from the `severity` label (or `priority`), the CI from `instance`, `host` or `service`, and the value from `valueString`. Set these labels in your alert rules.

## Incident context

The **Umbrella → Umbrella incident** dashboard accepts the variables that Umbrella fills into the link from an incident: `incident`, `ci`, `ci_id`, `service`, `team`, `signal`. Copy it, add your own panels filtered by these variables and enter the copy's address in Umbrella: **Settings → Alerting → Grafana** (for example, `http://localhost:3000/d/umbrella-incident/umbrella-incident`).

## Umbrella Flow: CI traffic

[`flow-ci-traffic.json`](flow-ci-traffic.json) is the **Umbrella Flow → CI traffic** dashboard (uid `flow-ci-traffic`):
the NetFlow / IPFIX / sFlow traffic of one configuration item, read straight from the ClickHouse tables of
[Umbrella Flow](../../app/docs/flow.md). The same file is kept in the Flow repository (`deploy/grafana`); the contract
is `docs/integrations.md` §3.5 there.

- **Datasource** — `grafana-clickhouse-datasource` with uid `flow-clickhouse`, provisioned from
  [`provisioning/datasources/flow-clickhouse.yaml`](provisioning/datasources/flow-clickhouse.yaml). The plugin is
  installed on start through `GF_PLUGINS_PREINSTALL` (`GRAFANA_PLUGINS`; without internet access install it into the
  image yourself and set `GRAFANA_PLUGINS=`).
- **Variables** — `ci` (name, shown in titles), `ci_id` (Umbrella CI ID), `ip` (an address of the item), `ref` (its
  Inventory DB reference, e.g. `inventory-db:dcim.device:100`), `rollup` (the aggregated table of the long-range panel:
  `flows_1m`, `flows_5m`, `flows_1h`). Umbrella's incident link fills `ci`, `ci_id`, `ip` and `ref` (the first address
  and the Inventory DB reference of the CI, or the `ip` and `source_ref` labels of the events).
- **Panels** — totals in / out, peers and flows; traffic in / out in bit/s (raw `flows`, matched by
  `DstCI` / `SrcCI` = `ref` or `DstAddr` / `SrcAddr` = `ip`); the long-range traffic by reference from the chosen
  aggregate (the aggregates keep no addresses or ports); top talkers (peer address, host, Inventory DB reference); top
  ports and applications (protocol, the lower port, the application Flow detected); top interfaces (exporter, input
  and output interface). `Bytes` in Flow are already multiplied by the sampling rate. The raw table is kept 15 days
  by default, `flows_1m` 30 days, `flows_5m` 90 days, `flows_1h` two years.

Environment (`.env`):

| Variable | Meaning | Default |
| --- | --- | --- |
| `FLOW_CLICKHOUSE_HOST` | host of the Flow ClickHouse HTTP interface (port 8123) as Grafana reaches it | `flow-clickhouse` |
| `FLOW_CLICKHOUSE_USER` | ClickHouse user; a read-only user is enough | `flow` |
| `FLOW_CLICKHOUSE_PASSWORD` | its password (`CLICKHOUSE_PASSWORD` of the Flow deployment) | — |
| `FLOW_CLICKHOUSE_DATABASE` | Flow database | `flow` |
| `GRAFANA_PLUGINS` | plugins installed on start | `grafana-clickhouse-datasource` |

The port (8123), the protocol (HTTP) and TLS (off) are set in the datasource file; change them there for HTTPS or the
native protocol. The Flow deployment publishes ClickHouse HTTP only on `127.0.0.1` of its host. Either put the Flow
ClickHouse into the shared `umbrella` network under the name `flow-clickhouse` (an override next to the Flow
`docker-compose.yml`):

```yaml
services:
  clickhouse:
    networks:
      default:
      umbrella:
        aliases: [flow-clickhouse]
networks:
  umbrella:
    name: umbrella
    external: true
```

or publish it on a reachable address (`CLICKHOUSE_BIND` in the Flow `.env`) and set `FLOW_CLICKHOUSE_HOST` to that
host. Then open the dashboard from an incident: in Umbrella **Settings → Alerting → Grafana**, enter its address,
for example `http://localhost:3000/d/flow-ci-traffic/ci-traffic`.
