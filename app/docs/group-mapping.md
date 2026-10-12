# Mapping directory groups to roles and teams

Previously only the administrators group was read from the directory, and the role and team of every user were assigned by hand. Now the **Settings → LDAP / AD / Entra ID** page, below the LDAP and Entra ID cards, has the **Groups → roles and teams** table: members of a group get a role, a team, or both.

## Table

| Field | What to enter |
| --- | --- |
| Directory | `LDAP / AD` or `Entra ID` (the column is shown when both are turned on) |
| Group | for LDAP / AD — the group DN (`cn=ops,ou=groups,dc=example,dc=org`), for Entra ID — the group Object ID (GUID) |
| Note | an optional note for people |
| Role | a role from the list of roles, or "do not change" |
| Team | a team from the team hierarchy, or "do not change" |

- Nested groups are taken into account: in AD with a single search using `LDAP_MATCHING_RULE_IN_CHAIN`, in OpenLDAP by walking `member`/`uniqueMember`/`memberUid` up to eight levels deep, in Entra ID via Microsoft Graph `transitiveMemberOf`.
- DNs are compared ignoring case and spaces around separators, so the same group written in different ways is allowed in the table only once.
- For the role, the first matching row from the top wins; the order of rows is changed with the arrows. **Every** matching row adds its team: a person can be a member of several teams. The administrators group from the connection settings takes precedence over the table.
- Rows that refer to a deleted role or team lose that reference; a row with neither a role nor a team is deleted.

A new directory user who was not given a role by the table gets the "Role for new users" (see [access.md](access.md)).

## When it is applied

1. **At every sign-in** of a directory user, together with the profile synchronization.
2. **On a schedule** (by default every 60 minutes, from 5 minutes to 7 days; can be turned off) — so that changes in the directory also reach those who have not signed in for a long time. The "Synchronize now" button runs a pass by hand.

The schedule reads groups without the user's password: for LDAP / AD with the service account, for Entra ID with the application token. That is why Entra ID needs the Microsoft Graph application permission **`GroupMember.Read.All`** with admin consent; sign-in itself only needs the delegated `User.Read`. A user who is no longer in the directory counts as not found in the synchronization results, and their role and team are not changed.

## Manual changes

A value from the table replaces one set by hand. Umbrella remembers exactly what the table granted (`mapped_role`, `mapped_teams` in the account):

- the user no longer matches any rows — what the table granted is taken away: the role becomes the "Role for new users" (Roles → the role marked "new users", "Viewer" by default), and the teams granted by the table are removed;
- if an administrator has changed the role by hand since then, it is kept; teams added by hand remain alongside those granted by the table;
- roles and teams of users the table has never touched are not changed.

The synchronization results (how many were checked, changed, not found) and the number of users whose role and team are currently granted by the table are shown in the same card. Every change of a role or team is written to the log: the `groups.sync` action (a pass) or `user.groups` (sign-in).

## Access

The card uses the permissions of the LDAP / AD page: `settings.ldap:view` for viewing and `settings.ldap:edit` for changing the table and running the synchronization by hand.
