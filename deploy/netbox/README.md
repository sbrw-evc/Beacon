# NetBox

NetBox 4.7 (application, background task worker, PostgreSQL 17 and Valkey) in the shared `umbrella` network. On the first start, an administrator and an API token for Umbrella are created.

```sh
cp .env.example .env   # set the passwords, NETBOX_SECRET_KEY, NETBOX_API_TOKEN_PEPPER, NETBOX_API_KEY and NETBOX_API_TOKEN
docker compose up -d
```

The first start with migrations takes a few minutes. Interface: http://localhost:8000, sign in with `NETBOX_ADMIN_USER` and `NETBOX_ADMIN_PASSWORD`.

Keys for `.env`:

```sh
openssl rand -base64 48   # NETBOX_SECRET_KEY, NETBOX_API_TOKEN_PEPPER (at least 50 characters)
openssl rand -hex 6       # NETBOX_API_KEY
openssl rand -hex 20      # NETBOX_API_TOKEN
```

The administrator and the token are created only on the first start. To change the token later, create a new one in **Admin → API Tokens** (version v2) or delete the volumes: `docker compose down -v`. Do not change `NETBOX_API_TOKEN_PEPPER` after the start: with a different value, the existing tokens stop working.

## Connecting to Umbrella

In Umbrella: **Automation → NetBox**:

- address — `http://netbox:8080` if Umbrella is run from `deploy/umbrella` on this host, otherwise `http://<host>:8000`;
- API token — `nbt_<NETBOX_API_KEY>.<NETBOX_API_TOKEN>`, for example `nbt_0123456789ab.0123456789abcdef0123456789abcdef01234567`. Umbrella sends it as `Authorization: Bearer` and stores it in OpenBao.

Turn the integration on and save (the connection is checked before saving), then click **Synchronize now**. The administrator token also gives write access, which is needed to register CIs in NetBox; for read-only access, create a separate user and a token without write permission.
