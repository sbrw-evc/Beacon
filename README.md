# Umbrella — umbrella monitoring

Umbrella is an umbrella monitoring web application: it receives events from monitoring systems and clouds through a low-code connector builder, acknowledges receipt to the source, normalizes events and collapses duplicates into incidents by CI and signal, keeps a catalog of CIs and business services with a CMDB map, raises alerts by RED and USE rules and hands incidents over to PagerDuty. If PagerDuty has not accepted an incident, the responsible people are notified through fallback channels (e-mail and Telegram). From an incident, a link opens a context dashboard in Grafana with the incident's variables. Access to pages and functions is configured with roles; people sign in at SPA, the Single Point of Authorization of the Umbrella family, which gives them these roles ([app/docs/identity-federation.md](app/docs/identity-federation.md)).

The application code (Go and React) is in [app](app/README.md): it describes what already works and how to run it, and has [screenshots](app/README.md#screenshots).

![Incidents](app/docs/screens/incidents-light-ru.png)

The screenshot was taken in one of the earlier versions of the interface.

## Test environment

[deploy](deploy) holds separate Docker Compose sets that join the shared `umbrella` network. Each one is started from its own folder with `docker compose up -d` after copying `.env.example` to `.env`:

| Folder | What it brings up |
| --- | --- |
| [deploy/umbrella](deploy/umbrella) | the application itself: the image is built from `app`, data in the `/data` volume, port `8080` |
| [deploy/postgres](deploy/postgres) | PostgreSQL 17 with `pg_stat_statements` (database `umbrella`) and pgAdmin |
| [deploy/openbao](deploy/openbao) | OpenBao 2.7 with file storage; an init container unseals it, enables KV v2 `umbrella/` and AppRole `umbrella` and puts `role_id` and `secret_id` into `secrets/umbrella` |
| [deploy/ad](deploy/ad) | a Samba AD domain controller (LDAP 389, LDAPS 636) with test users, the `svc-umbrella` service account and Umbrella groups, plus LDAP Account Manager |
| [deploy/zabbix](deploy/zabbix/README.md) | Zabbix 7.0 LTS (server, web interface, PostgreSQL, agent 2) and the Umbrella media type |
| [deploy/netbox](deploy/netbox/README.md) | NetBox 4.7 with the background worker, PostgreSQL and Valkey; an administrator and an API token are created on the first start |
| [deploy/grafana](deploy/grafana/README.md) | Grafana 13 with the "Umbrella" contact point and the incident context dashboard |

How to connect Zabbix, NetBox and Grafana to Umbrella is described in the README files in their folders. There is no script in the repository that installs the whole test environment with a single command.

## Architecture documentation

Architecture documents (MVP and target architecture) and their diagrams are kept separately from this repository; the architecture of the Inventory DB integration is in [integrations/inventory-db.md](integrations/inventory-db.md). This repository holds the application documentation ([app/README.md](app/README.md) and [app/docs](app/docs)) and an analysis of n8n capabilities for the connector builder ([n8n.md](n8n.md)).
