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
