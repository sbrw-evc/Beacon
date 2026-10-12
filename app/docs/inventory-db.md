# Integration with Inventory DB (optional)

Inventory DB is a data centre inventory (sites, racks, devices, addresses in the style of NetBox). The integration
is off by default: until it is turned on on the "Inventory DB" page, Umbrella does not contact
Inventory DB and changes nothing in the CI catalog.

## What it does

```mermaid
sequenceDiagram
  participant U as Umbrella
  participant I as Inventory DB
  U->>I: GET /api/v1/integrations/<id>/umbrella/ci (xc-token)
  I-->>U: devices, racks, sites
  U->>U: create a CI with the "Inventory DB" source or link an existing CI by name
  U->>I: POST /api/v1/integrations/<id>/umbrella/alerts (HMAC signature)
  I-->>U: 202; the device card shows firing alerts with a link to the incident
```

- **Devices → CIs.** On a schedule (by default every 15 minutes) and with the "Read devices now" button.
  An existing CI with the same name (case-insensitive) is linked to the device rather than duplicated.
  Attributes: site, role, type (manufacturer and model), tenant, platform, rack, serial number.
  If a device disappears from Inventory DB, a load with removal requires confirmation; an empty response
  deletes and unlinks nothing.
- **Alert states → Inventory DB.** Every 30 seconds Umbrella sends changes of active
  incidents on linked CIs (opened, acknowledged, resolved) with a link to the incident and Grafana. Severity P4
  ("Low") is sent as `info`. Unsent changes are retried on the next cycle.
- **Device reference in events.** An event with the `source_ref` label (`inventory-db:dcim.device:100`)
  is bound to the CI with that reference before matching by name; this is how Umbrella Flow finds the CI ([flow.md](flow.md)).

## Setup

1. In Inventory DB, create an Umbrella integration (`POST /api/v1/integrations`), save its id
   (`int_…`) and the signing secret — it is shown once.
2. In Inventory DB, create a read-only user and issue an API token for it.
3. In Umbrella, open "Automation → Inventory DB", turn on "Connect Inventory DB", enter the
   address, the integration id, the token and the secret. Umbrella checks the connection before saving. The token and secret
   are stored in OpenBao at the path `umbrella/inventory-db`.
4. For links to incidents, set "Settings → Alerting → Umbrella address".

## Permissions

| Permission | What it grants |
|---|---|
| `inventorydb:view` | see the page and the load status |
| `inventorydb:test` | check the connection |
| `inventorydb:edit` | change the settings |
| `inventorydb:sync` | run a load, including one with removal |

## API

| Method | Path | Permission |
|---|---|---|
| GET | `/api/inventory-db` | `inventorydb:view` |
| PUT | `/api/inventory-db` | `inventorydb:edit` |
| POST | `/api/inventory-db/test` | `inventorydb:test` |
| POST | `/api/inventory-db/sync[?confirm=removal]` | `inventorydb:sync` |
