# Beacon

*Formerly **Umbrella Monitoring**. Beacon, part of the Umbrella family, turns alerts from monitoring systems into
incidents, paging and response.*

The other members of the family: [Keystone](https://onedev.evcsbrw.ru/Monitoring/Keystone) (sign-in and access; formerly SPA),
[Ledger](https://onedev.evcsbrw.ru/Monitoring/Ledger) (inventory — DCIM/IPAM — and spreadsheet databases; formerly Inventory DB),
[Current](https://onedev.evcsbrw.ru/Monitoring/Current) (network traffic and network devices; formerly Umbrella Flow) and
Network Control.

Beacon is an umbrella monitoring web application: it receives events from monitoring systems and clouds through a low-code connector builder, acknowledges receipt to the source, normalizes events and collapses duplicates into incidents by CI and signal, keeps a catalog of CIs and business services with a CMDB map, raises alerts by RED and USE rules and hands incidents over to PagerDuty. If PagerDuty has not accepted an incident, the responsible people are notified through fallback channels (e-mail and Telegram). From an incident, a link opens a context dashboard in Grafana with the incident's variables. Access to pages and functions is configured with roles; people sign in at Keystone, sign-in and access for the Umbrella family, which gives them these roles ([app/docs/identity-federation.md](identity-federation.md)).

The application code (Go and React) is in [app](https://onedev.evcsbrw.ru/Monitoring/Beacon/~files/main/app/README.md): it describes what already works and how to run it, and has [screenshots](https://onedev.evcsbrw.ru/Monitoring/Beacon/~files/main/app/README.md#screenshots).

![Incidents](https://onedev.evcsbrw.ru/Monitoring/Beacon/~raw?revision=main&file=app/docs/screens/incidents-light-ru.png)

The screenshot was taken in one of the earlier versions of the interface.

## Test environment

[deploy](https://onedev.evcsbrw.ru/Monitoring/Beacon/~files/main/deploy) holds separate Docker Compose sets that join the shared `umbrella` network. Each one is started from its own folder with `docker compose up -d` after copying `.env.example` to `.env`:

| Folder | What it brings up |
| --- | --- |
| [deploy/beacon](https://onedev.evcsbrw.ru/Monitoring/Beacon/~files/main/deploy/beacon) | the application itself: the image is built from `app`, data in the `/data` volume, port `8080` |
| [deploy/postgres](https://onedev.evcsbrw.ru/Monitoring/Beacon/~files/main/deploy/postgres) | PostgreSQL 17 with `pg_stat_statements` (database `umbrella`) and pgAdmin |
| [deploy/openbao](https://onedev.evcsbrw.ru/Monitoring/Beacon/~files/main/deploy/openbao) | OpenBao 2.7 with file storage; an init container unseals it, enables KV v2 `umbrella/` and AppRole `umbrella` and puts `role_id` and `secret_id` into `secrets/umbrella` |
| [deploy/ad](https://onedev.evcsbrw.ru/Monitoring/Beacon/~files/main/deploy/ad) | a Samba AD domain controller (LDAP 389, LDAPS 636) with test users, the `svc-umbrella` service account and the `Umbrella Admins` and `Umbrella Users` groups, plus LDAP Account Manager |
| [deploy/zabbix](https://onedev.evcsbrw.ru/Monitoring/Beacon/~files/main/deploy/zabbix/README.md) | Zabbix 7.0 LTS (server, web interface, PostgreSQL, agent 2) and the Beacon media type |
| [deploy/netbox](https://onedev.evcsbrw.ru/Monitoring/Beacon/~files/main/deploy/netbox/README.md) | NetBox 4.7 with the background worker, PostgreSQL and Valkey; an administrator and an API token are created on the first start |
| [deploy/grafana](https://onedev.evcsbrw.ru/Monitoring/Beacon/~files/main/deploy/grafana/README.md) | Grafana 13 with the "Beacon" contact point and the incident context dashboard |

How to connect Zabbix, NetBox and Grafana to Beacon is described in the README files in their folders. There is no script in the repository that installs the whole test environment with a single command.

## Architecture documentation

Architecture documents (MVP and target architecture) and their diagrams are kept separately from this repository; the architecture of the Ledger integration is in [integrations/ledger.md](https://onedev.evcsbrw.ru/Monitoring/Beacon/~files/main/integrations/ledger.md). This repository holds the application documentation ([app/README.md](https://onedev.evcsbrw.ru/Monitoring/Beacon/~files/main/app/README.md) and [app/docs](https://onedev.evcsbrw.ru/Monitoring/Beacon/~files/main/app/docs)) and an analysis of n8n capabilities for the connector builder ([n8n.md](https://onedev.evcsbrw.ru/Monitoring/Beacon/~files/main/n8n.md)).

## Compatibility after the rename

Beacon was called Umbrella Monitoring (and "Umbrella" in its own interface). The product, the Go module
(`github.com/sbrw-evc/beacon/app/backend`), the binary (`cmd/beacon`, `/beacon` in the image), the Docker image
(`beacon`), the compose project and service (`deploy/beacon`, `beacon`) and the web package (`beacon-web`) carry the new
name, and the other apps are called Keystone, Ledger and Current. These identifiers keep the old spelling on purpose,
because existing installations and the other apps depend on them:

| kept | why |
| --- | --- |
| environment variables `UMBRELLA_*` (`UMBRELLA_DATA_DIR`, `UMBRELLA_TRUSTED_PROXIES`, `UMBRELLA_IMAGE`, `UMBRELLA_TEST_POSTGRES`, …), `FLOW_CLICKHOUSE_*` of `deploy/grafana` | existing deployments |
| the Keystone protocol: `SPA_CONNECTION_KEY`, `SPA_CONNECTION_KEY_FILE`, `SPA_PUBLIC_URL`, the key prefix `spa1.`, `/api/auth/spa/start` and `/api/auth/spa/callback`, Keystone's `/api/spa/v1/*`, the OpenBao key `spa` → `connection_key`, `source = spa` of people from Keystone | the wire format between Keystone and its applications (Keystone's `docs/protocol.md`) |
| the configuration file `umbrella.json`, the OpenBao mount `umbrella`, its paths (`umbrella/spa`, `umbrella/inventory-db`, `umbrella/system/…`), the policy `umbrella-policy.hcl` and the AppRole `umbrella` | existing configuration and secrets |
| the PostgreSQL database and user `umbrella` of `deploy/postgres`, the table `umbrella_state` and the other tables, the LISTEN channel `umbrella_incidents_changed` | existing data |
| the compose volume `umbrella_umbrella-data`, the shared network `umbrella` and the host name `umbrella` (an alias of the `beacon` service) | existing data; the other stacks of `deploy/` reach Beacon at `http://umbrella:8080` or `http://beacon:8080` |
| the session cookie `umbrella_session`, the `localStorage` keys `umbrella.*` | sessions and saved preferences |
| HTTP headers `X-Umbrella-Token`, `X-Umbrella-Signature`, `X-Umbrella-Timestamp` and the other `X-Umbrella-*` | connectors, monitoring systems and webhooks already configured |
| file formats `umbrella.connector`, `umbrella-theme/v1`, `umbrella-locale/v1` | exported connectors, themes and locales |
| the API paths `/api/settings/spa`, `/api/inventory-db`, `/api/flow`, `/api/integrations/flow/scope`; the JSON keys `inventory_db`, `flow`, `flow_url`; the page and permission IDs `settings.spa`, `inventorydb`, `integrations.flow:read`; the connector template `network-flow` | API clients, Current, Ledger and roles stored with their permissions |
| the source references `inventory-db:…` of CIs from Ledger | stored CIs and the contract with Ledger and Current |
| Grafana UIDs `umbrella-incident`, `flow-ci-traffic` and the datasource `flow-clickhouse` | dashboard links stored in the alerting settings |

The pages `/settings/spa` and `/inventory-db` of the web interface moved to `/settings/keystone` and `/ledger`; the old
addresses redirect. The GitHub repository is to be renamed to `sbrw-evc/beacon`; GitHub redirects the old address.
