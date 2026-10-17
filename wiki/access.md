# Access: roles, teams and visibility scopes

## Roles

Every user has exactly one role. A role is a set of `<page>:<function>` permissions from the catalog (`GET /api/access/catalog`).

| Role | What it grants | For whom |
| --- | --- | --- |
| Administrator (system) | everything, including pages that appear later | administrators |
| User (system) | nothing, only the user's own profile | — |
| Viewer | viewing incidents, the CMDB map, CIs, services, maintenance windows and TV wallboards | everyone by default |
| On-call engineer | Viewer + acknowledging, resolving and commenting on incidents, maintenance windows | NOC shift |
| Service owner | incidents (view and acknowledge), the CMDB map, CIs, services, viewing maintenance windows; give it together with the "Services of my teams" scope | development teams |
| Integration administrator | Viewer + connectors, credentials, RED/USE rules, monitoring systems, NetBox, editing CIs and services, system status | monitoring engineers |
| NOC lead | On-call engineer + teams, users (view, edit, block; only administrators change administrators), TV wallboards, viewing and testing alerting, system status | shift lead |

The five built-in roles are created **once** — at installation or on the first start of the version in which they appeared — in the installation language. They are ordinary roles: they can be renamed, changed and deleted, and a deleted role does not come back. If a role with the same name already existed, the built-in one is not created.

### Where roles are given

People get their role in **Keystone** ([identity-federation.md](identity-federation.md)). Beacon publishes every role to Keystone as its role catalogue, under the role ID in lower case (`admin`, `user`, `rol-7`), and takes the role from the `roles` claim at sign-in and from the synchronization every 5 minutes. With several roles, administrator wins, then the role with the most permissions. A role that is still given to people cannot be deleted (`role_in_use`): take it away in Keystone first. Roles → role → "Give to new users" (`PUT /api/roles/{id}/new-users`) is the role of NetBox contact accounts; the administrator role cannot be chosen.

### User without permissions

If a role opens no pages at all, after signing in the person lands on the profile and sees the "No access yet" banner with a list of active administrators and their e-mail addresses; another role is given in Keystone.

## Teams

A person can be a member of **several** teams (Teams → members or Users → "Teams"; `team_ids` in the API). Adding someone to a team does not remove them from others.

A team can have a **mailbox** and a **Telegram chat**. Then the fallback notification for its incidents goes to the team's mailbox and chat and to its lead, not to every member.

## Visibility scope

Every user has one of the modes (`scope_mode`):

| Mode | What the user sees and can work with |
| --- | --- |
| All services (`all`, default) | everything |
| Services of my teams (`teams`) | services that the user's teams **own or support**; changes together with team membership |
| Chosen services (`services`) | only the chosen services (`service_ids`) |

An administrator is never restricted. The scope applies to:

- incidents: the list, counters, the card, actions, the link to Grafana, events on the CMDB map;
- **maintenance windows**: only a window whose targets are all in scope can be created, changed, ended and deleted. A CI counts as one's own if **all** of its active services are in scope (otherwise the window would silence someone else's service);
- **TV wallboards**: a wallboard's targets can only be CIs, services and teams from the scope (a team — if all the services it owns or supports are in scope); a user with a restricted scope cannot create a wallboard without targets ("All incidents"). Such a user sees other people's windows and wallboards but cannot change them (`in_scope: false`).

Accounts from earlier versions that had a list of services got the "Chosen services" mode.

The built-in "Service owner" role is designed for the "Services of my teams" mode: give the role in Keystone, then the team and the mode in Beacon.

## Service accounts and API tokens

Other systems (Current, scripts) call the API with an **API token** of a **service account** instead of a session.
This is Beacon's side of the cross-application contract (Current repository, `docs/integrations.md` §1).

**Settings → Service accounts** (permissions `serviceaccounts:view` and `serviceaccounts:edit`):

- A service account has a name (Latin letters, digits and `. _ @ -`; the audit log shows it as `service:<name>`), a
  description, a **role**, **teams** and a **visibility scope** exactly like a user (all services, services of its
  teams, chosen services). It never signs in, gets no notifications and is not touched by Keystone or NetBox
  synchronization. Only administrators give or change the administrator role. A disabled account's tokens are refused.
- Every account has an **owner**, a person (by default whoever creates it). While Keystone has the owner disabled, the
  account's tokens are refused with 401 and the list marks the account "owner disabled"; they work again when Keystone
  gives the person access back, or after another owner is chosen. Accounts made before owners existed got the person
  who created them; an account whose creator is unknown has no owner and keeps working.
- An account has up to 20 active **tokens**: a name and an optional expiry (30 / 90 days, a year or never in the
  interface; up to ten years in the API). The token `umb_<32 random bytes, base64url>` is shown **once**; Beacon
  stores only its HMAC-SHA256 with a key kept in OpenBao (`<mount>/system/api-tokens#hmac_key`, created with the first
  token; moved with the other secrets by an OpenBao migration). The list shows the start and end of a token, when it
  was issued and by whom, its expiry, when and from which address it was last used. A token can be **revoked**; it stays
  listed as revoked. Deleting an account deletes its tokens.
- Audit log: `serviceaccount.create/update/delete`, `serviceaccount.token.create`, `serviceaccount.token.revoke` and
  `serviceaccount.token.new_address` (the first use of a token from an address).

Using a token: `Authorization: Bearer umb_…`. A request with this header is judged by the token only (a session cookie
in the same request is ignored); it needs **no** `X-CSRF-Token`, and every route checks the same `<page>:<function>`
permissions and visibility scope as for a person with that role. Session routes (`/api/auth/*`: sign-out, avatar,
preferences) refuse tokens with 403, except `GET /api/auth/me`. An unknown, revoked or expired token is 401
`unauthenticated`; when OpenBao cannot give the key, 503 `secrets_unavailable`. Sessions of people are unchanged.

```bash
curl -H "Authorization: Bearer $UMB_TOKEN" "https://umbrella.example.com/api/integrations/flow/scope?username=ivanov"
```

| Method | Path | Permission | Purpose |
| --- | --- | --- | --- |
| GET | `/api/service-accounts` | `serviceaccounts:view` | accounts with `role_name` and `tokens` (never the hash) |
| GET | `/api/service-accounts/{id}` | `serviceaccounts:view` | one account |
| POST | `/api/service-accounts` | `serviceaccounts:edit` | `{name, description, role_id, team_ids, scope_mode, service_ids, disabled}` → 201 |
| PUT | `/api/service-accounts/{id}` | `serviceaccounts:edit` | the same body, replaces the account |
| DELETE | `/api/service-accounts/{id}` | `serviceaccounts:edit` | deletes the account and its tokens |
| POST | `/api/service-accounts/{id}/tokens` | `serviceaccounts:edit` | `{name, expires_at?}` → 201 `{token, secret}`; `secret` is the plaintext, only here |
| POST | `/api/service-accounts/{id}/tokens/{token}/revoke` | `serviceaccounts:edit` | revokes the token |

Errors: `invalid_username`, `account_name_taken`, `unknown_role`, `unknown_team`, `invalid_scope`,
`scope_services_required`, `admin_only`, `invalid_token_name`, `invalid_expiry`, `too_many_tokens`, `token_revoked`.

A role for Current needs `integrations.flow:read` (visibility scopes, [current.md](current.md#current--beacon-visibility-scopes))
and `cmdb:propose` (dependency proposals, [current.md](current.md#current--beacon-dependency-proposals)). These two endpoints are not
limited by the scope of the calling account: the scope endpoint answers for the user asked about.
