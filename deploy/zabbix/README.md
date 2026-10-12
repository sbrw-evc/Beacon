# Zabbix

Zabbix 7.0 LTS (server, web interface on nginx, PostgreSQL 17 and agent 2, which monitors Zabbix itself) in the shared `umbrella` network.

```sh
cp .env.example .env   # set ZABBIX_DB_PASSWORD
docker compose up -d
```

For Zabbix 8.0, uncomment the lines with the 8.0 images in `.env`.

Interface: http://localhost:8081, sign in as `Admin` / `zabbix` (change the password at first sign-in). External agents connect to port 10051; to make it reachable from other machines too, set `ZABBIX_BIND=0.0.0.0`.

## Connecting to Umbrella

The easiest way: in Umbrella, **Connectors → Connect a source → Zabbix** — the token, the connector and the publication are made at once, and the media type is given with the address and token already filled in (steps 1 and 2 then come down to importing the file). Manually:

1. In Umbrella: **Automation → Credentials** — create a Bearer token. **Connectors** — create a connector from the "Zabbix (webhook media type)" template, choose this token in the webhook node and publish it. The intake address is `/api/ingest/<slug>`, `zabbix` by default.
2. In Zabbix: **Alerts → Media types → Import**, file [`umbrella-mediatype.yaml`](umbrella-mediatype.yaml). In the "Umbrella" media type, set the parameters:
   - `url` — the intake address. If Umbrella is run from `deploy/umbrella` on the same host, `http://umbrella:8080/api/ingest/zabbix` will do;
   - `token` — the token from step 1.
3. **Users → Users → Admin → Media**: add the "Umbrella" media type; in the "Send to" field enter any value, for example `umbrella`.
4. **Alerts → Actions → Trigger actions**: create an action with a "Send message" operation to the Admin user via "Umbrella" and the same recovery operation.

The media type sends the fields the template expects: `event_id`, `host`, `host_group`, `trigger`, `item_key`, `severity` (0–5), `status` (1 — problem, 0 — recovery) and `value`. A recovery comes with the same `event_id` as the problem, so it closes the same alert. Zabbix shows an Umbrella response outside the 2xx range as a sending error in "Reports → Action log".
