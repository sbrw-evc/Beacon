# Integration with Ledger

Ledger is the inventory of the Umbrella family (sites, racks, devices, addresses, with a NetBox-compatible API).
Inventory is kept only in Ledger; Beacon reads its devices as CIs. The integration is off by default: until
it is turned on on the "Ledger" page, Beacon does not contact Ledger and changes nothing in the CI catalog.

## One inventory source

Beacon reads devices from one inventory source: Ledger, or a real NetBox at sites that run one instead of
Ledger. The NetBox synchronization and the Ledger connection are never on together.

- **Turning one on while the other is on is refused** with `409`: `inventory_db_netbox_active` when Ledger is
  connected while the NetBox synchronization is on, `netbox_ledger_active` when NetBox is turned on while Ledger
  is connected. Turning either off is always allowed. The "Ledger" and "NetBox" pages say which source is active,
  link to its page and keep the "Connect" switch of the other one locked.
- **An installation that had both on** (from before this rule) keeps Ledger: at start the NetBox synchronization is
  turned off with its settings, token and the CIs it imported kept, an audit entry `settings.netbox` says
  "paused: Ledger is the inventory source", and the "NetBox" page and "Settings → System status" show the warning
  "NetBox synchronization paused" with the date, until the NetBox settings are saved again.
- **Beacon writes nothing to NetBox while Ledger is the source.** Before, Beacon wrote to NetBox: registering a CI
  created a device or virtual machine there (the CI editor, the "Register in NetBox" button, "Also register in
  NetBox" when a CI is made from a monitoring host, one by one or in bulk), changing or deleting a registered CI
  changed or deleted the object, and binding a business service to a NetBox tag created the tag and set it on
  objects. Contacts were only read. With Ledger as the source, registration is refused (`409 ci_register_ledger`)
  and hidden; changing or deleting a CI linked to NetBox earlier changes it in Beacon only; NetBox tags of
  services are not touched (NetBox is off).
- **New devices are created in Ledger.** Where Beacon offered to create a device — the CI editor and card, a CI
  from a monitoring host, "Create item" on the incident card — it shows "Create the device in Ledger": Ledger's
  device list with the form for a new device open (`<Ledger>/dcim/devices?_sel=new&q=<name>`; Ledger does not fill
  the form from the address, so the list is searched for the name). On the incident card the local "Create item"
  button is not shown for a device; once the next Ledger synchronization has read the device, the incident is bound
  to it with "Bind to an existing configuration item" (or by itself, when the name matches). A CI of a device made
  by hand is linked to the Ledger device of the same name on the next read, not duplicated.
- `GET /api/inventory-source` (`cis:view`) tells the pages which source is active:
  `{"source": "ledger" | "netbox" | "", "ledger_url", "ledger_new_device_url", "netbox_url", "netbox_paused_at"}`.
  The same object is `source` in `GET /api/netbox` and `GET /api/inventory-db` and `inventory_source` in
  `GET /api/system`.

## What it does

```mermaid
sequenceDiagram
  participant U as Beacon
  participant I as Ledger
  U->>I: GET /api/v1/integrations/<id>/umbrella/ci (xc-token)
  I-->>U: devices, racks, sites
  U->>U: create a CI with the "Ledger" source or link an existing CI by name
  U->>I: POST /api/v1/integrations/<id>/umbrella/alerts (HMAC signature)
  I-->>U: 202; the device card shows firing alerts with a link to the incident
```

- **Devices → CIs.** On a schedule (by default every 15 minutes) and with the "Read devices now" button.
  An existing CI with the same name (case-insensitive) is linked to the device rather than duplicated.
  Attributes: site, role, type (manufacturer and model), tenant, platform, rack, serial number.
  If a device disappears from Ledger, a load with removal requires confirmation; an empty response
  deletes and unlinks nothing.
- **Alert states → Ledger.** Every 30 seconds Beacon sends changes of active
  incidents on linked CIs (opened, acknowledged, resolved) with a link to the incident and Grafana. Severity P4
  ("Low") is sent as `info`. Unsent changes are retried on the next cycle.
- **Device reference in events.** An event with the `source_ref` label (`inventory-db:dcim.device:100`)
  is bound to the CI with that reference before matching by name; this is how Current finds the CI ([current.md](current.md)).

## Setup

1. In Ledger, create a Beacon integration (`POST /api/v1/integrations`), save its id
   (`int_…`) and the signing secret — it is shown once.
2. In Ledger, create a read-only user and issue an API token for it.
3. In Beacon, open "Automation → Ledger", turn on "Connect Ledger", enter the
   address, the integration id, the token and the secret. Beacon checks the connection before saving. The token and secret
   are stored in OpenBao at the path `umbrella/inventory-db`.
4. For links to incidents, set "Settings → Alerting → Beacon address".

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
| GET | `/api/inventory-source` | `cis:view` |
