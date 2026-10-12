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

### Role for new users

Accounts created by sign-in or by LDAP / AD, Entra ID and NetBox synchronization get the "Role for new users" unless the "Groups → roles and teams" table granted a different one. By default it is "Viewer"; to change it — Roles → role → "Give to new users" (`PUT /api/roles/{id}/new-users`, permission `roles:edit`). The administrator role cannot be chosen. Users that already exist are not changed. If the chosen role is deleted, new users get "User".

The same role is set when the group table stops granting a role to a user (see [group-mapping.md](group-mapping.md)).

### User without permissions

If a role opens no pages at all, after signing in the person lands on the profile and sees the "No access yet" banner with a list of active administrators and their e-mail addresses.

## Teams

A person can be a member of **several** teams (Teams → members or Users → "Teams"; `team_ids` in the API). Adding someone to a team does not remove them from others. The directory group table adds the team of every matching row and takes away only the teams it granted itself.

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

The `all` or `teams` mode can be granted by the "Groups → roles and teams" table (the "Visibility scope" column); when the group stops granting it, the user goes back to "All services". Accounts from earlier versions that had a list of services got the "Chosen services" mode.

The built-in "Service owner" role is designed for the "Services of my teams" mode: a group table row "group → Service owner + team + Services of my teams" grants everything at once.
