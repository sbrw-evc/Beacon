# Sign-in through SPA

Umbrella Monitoring does not sign people in itself and is no longer the identity hub of the family. People
sign in at **SPA**, the Single Point of Authorization of the Umbrella family. The protocol between SPA and
the applications (OpenID Connect with PKCE, the `spa1.` connection key, the federation API, the role
catalogue) is described in the SPA repository: **[docs/protocol.md](https://github.com/sbrw-evc/spa/blob/claude/single-point-of-authorization/docs/protocol.md)**.

## What this means for Umbrella

- **Connection.** In SPA, open Applications → Connect, choose Umbrella Monitoring and give it the redirect
  address `https://<umbrella>/api/auth/spa/callback` (and `https://<umbrella>/` after sign-out). Put the
  connection key SPA shows into `SPA_CONNECTION_KEY` (or `SPA_CONNECTION_KEY_FILE`, or the setup wizard,
  which keeps it in OpenBao at `spa/connection_key`) and restart Umbrella. `SPA_PUBLIC_URL` overrides the
  public address used for the redirect when it differs from "Settings → Alerting → Umbrella address".
- **Before you connect, give yourself the role «Administrator» for Umbrella in SPA.** The first
  synchronization binds accounts of earlier versions to SPA people once, by login, and locks every account
  SPA does not list, the old local administrator included.
- **Roles.** Umbrella publishes its roles to SPA as the role catalogue: the built-in `admin` and `user` and
  every custom role, under its ID in lower case (`ROL-7` → `rol-7`). A changed or new role is published at
  once. A person's role comes from the `roles` claim at sign-in and from the synchronization; with several
  roles, administrator wins, then the role with the most permissions. Teams and the incident scope stay in
  Umbrella.
- **People.** Users are synchronized from SPA every 5 minutes (Settings → SPA → "Synchronize now" runs it
  at once). A person SPA disables or takes the role from is locked, signed out, and the API tokens of the
  service accounts they own stop working until SPA gives access back. Name and e-mail come from SPA; the
  photo, time zone, language and Telegram chat stay in Umbrella.
- **Gone from Umbrella:** passwords and their history, the password policy, passkeys, sign-in with
  LDAP / AD, Microsoft Entra ID, Google, Apple and OpenID Connect, group mapping, the OpenID Connect provider
  with connected applications and `/api/identity/v1`, the first administrator step of the setup wizard.
  The secrets they kept in OpenBao (`users/<id>`, password history, `entra`, `identity/signing`, provider
  secrets) are deleted once after the update. LDAP / Active Directory stays for reading computers into the
  CMDB.

## API

| Route | Permission | |
| --- | --- | --- |
| `GET /api/auth/spa` | — | whether Umbrella is connected, the SPA address and its sign-in methods (sign-in page) |
| `GET /api/auth/spa/start?return=<path>[&idp_hint=…][&login_hint=…]` | — | starts the sign-in at SPA |
| `GET /api/auth/spa/callback` | — | SPA's answer; on failure the browser lands on `/login?sso_error=<code>` (`state`, `not_connected`, `spa_unavailable`, `invalid_token`, `no_role`, `account_conflict` or SPA's own error such as `access_denied`) |
| `POST /api/auth/logout` | session | ends the session; answers `{"redirect": "<SPA sign-out>"}` |
| `GET /api/settings/spa` | `settings.spa:view` | connection, last synchronization, counts, the published catalogue |
| `POST /api/settings/spa/sync` | `settings.spa:sync` | synchronizes now |
| `POST /api/setup/spa/test` | setup code | checks a connection key in the setup wizard |
