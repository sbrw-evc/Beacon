# Sign-in through Keystone

Beacon does not sign people in itself and is no longer the identity hub of the family. People
sign in at **Keystone**, sign-in and access for the Umbrella family. The protocol between Keystone and
the applications (OpenID Connect with PKCE, the `spa1.` connection key, the federation API, the role
catalogue) is described in the Keystone repository: **[docs/protocol.md](https://onedev.evcsbrw.ru/Monitoring/Keystone/~files/main/docs/protocol.md)**.
Keystone was called SPA before, and the Keystone protocol keeps that historical spelling in its identifiers:
`spa1.`, `SPA_CONNECTION_KEY`, `SPA_PUBLIC_URL`, `/api/auth/spa/start|callback`, `/api/spa/v1/*` and the OpenBao key
`spa/connection_key`.

## What this means for Beacon

- **Connection.** In Keystone, open Applications → Connect, choose Beacon and give it the redirect
  address `https://<beacon>/api/auth/spa/callback` (and `https://<beacon>/` after sign-out). Put the
  connection key Keystone shows into `SPA_CONNECTION_KEY` (or `SPA_CONNECTION_KEY_FILE`, or the setup wizard,
  which keeps it in OpenBao at `spa/connection_key`) and restart Beacon. `SPA_PUBLIC_URL` overrides the
  public address used for the redirect when it differs from "Settings → Alerting → Beacon address".
- **Before you connect, give yourself the role «Administrator» for Beacon in Keystone.** The first
  synchronization binds accounts of earlier versions to Keystone people once, by login, and locks every account
  Keystone does not list, the old local administrator included.
- **Roles.** Beacon publishes its roles to Keystone as the role catalogue: the built-in `admin` and `user` and
  every custom role, under its ID in lower case (`ROL-7` → `rol-7`). A changed or new role is published at
  once. A person's role comes from the `roles` claim at sign-in and from the synchronization; with several
  roles, administrator wins, then the role with the most permissions. Teams and the incident scope stay in
  Beacon.
- **People.** Users are synchronized from Keystone every 5 minutes (Settings → Keystone → "Synchronize now" runs it
  at once). A person Keystone disables or takes the role from is locked, signed out, and the API tokens of the
  service accounts they own stop working until Keystone gives access back. Name and e-mail come from Keystone; the
  photo, time zone, language and Telegram chat stay in Beacon.
- **Gone from Beacon:** passwords and their history, the password policy, passkeys, sign-in with
  LDAP / AD, Microsoft Entra ID, Google, Apple and OpenID Connect, group mapping, the OpenID Connect provider
  with connected applications and `/api/identity/v1`, the first administrator step of the setup wizard.
  The secrets they kept in OpenBao (`users/<id>`, password history, `entra`, `identity/signing`, provider
  secrets) are deleted once after the update. LDAP / Active Directory stays for reading computers into the
  CMDB.

## API

| Route | Permission | |
| --- | --- | --- |
| `GET /api/auth/spa` | — | whether Beacon is connected, the Keystone address and its sign-in methods (sign-in page) |
| `GET /api/auth/spa/start?return=<path>[&idp_hint=…][&login_hint=…]` | — | starts the sign-in at Keystone |
| `GET /api/auth/spa/callback` | — | Keystone's answer; on failure the browser lands on `/login?sso_error=<code>` (`state`, `not_connected`, `spa_unavailable`, `invalid_token`, `no_role`, `account_conflict` or Keystone's own error such as `access_denied`) |
| `POST /api/auth/logout` | session | ends the session; answers `{"redirect": "<Keystone sign-out>"}` |
| `GET /api/settings/spa` | `settings.spa:view` | connection, last synchronization, counts, the published catalogue |
| `POST /api/settings/spa/sync` | `settings.spa:sync` | synchronizes now |
| `POST /api/setup/spa/test` | setup code | checks a connection key in the setup wizard |
