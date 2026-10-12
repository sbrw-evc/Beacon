# Sign-in with Microsoft Entra ID (Azure AD)

Umbrella accepts users of the Microsoft Entra ID cloud directory over OpenID Connect: the authorization code flow with PKCE, a confidential client with a secret. The settings are on the **Settings → LDAP / AD / Entra ID** page, below the LDAP parameters. Sign-in via LDAP / AD and via Entra ID can be turned on independently.

## Registering the application in Entra ID

1. Entra portal → **App registrations → New registration**. Supported account types: **Accounts in this organizational directory only** (single tenant).
2. **Redirect URI**: platform **Web**, the address from the "Redirect URI" field on the settings page, for example `https://umbrella.example.com/api/auth/entra/callback`. The address must match the one users open Umbrella with.
3. **Certificates & secrets → New client secret**. Enter the secret value (Value) in Umbrella, not its ID. The secret is stored in OpenBao (path `entra`, key `client_secret` in the Umbrella mount); only a reference remains in Umbrella's state.
4. **API permissions**: the delegated Microsoft Graph permission `User.Read` (present by default). Through it Umbrella reads the profile (job title, department, manager, photo) and group membership, including nested groups.
5. Copy the **Directory (tenant) ID** and **Application (client) ID** from the Overview page.

## Settings in Umbrella

| Field | What to enter |
| --- | --- |
| Microsoft cloud | global, US Government or China (21Vianet) |
| Directory (tenant) ID | the tenant GUID or its domain (`contoso.onmicrosoft.com`); `common` and `organizations` are not accepted |
| Application (client) ID | Application (client) ID |
| Client secret | the secret value; can be left empty when saving again |
| Administrators group | the group Object ID; its members (including via nested groups) get the administrator role at every sign-in |
| Allowed users group | the group Object ID; if set, only its members and administrators can sign in |

"Check connection" finds the tenant by its OpenID configuration and checks the application ID and secret (a client credentials token request). The same check runs before saving.

## How sign-in works

- The sign-in page shows the "Sign in with Microsoft" button. The user goes to the Microsoft sign-in page and returns to `/api/auth/entra/callback`.
- Umbrella checks `state` (and the cookie of the browser that started the sign-in), exchanges the code for tokens with `code_verifier`, verifies the ID token signature with the tenant keys, and checks the issuer, audience, expiry, `nonce` and `tid`.
- The account is created with the "Entra ID" source, the login is `preferred_username` (UPN) in lower case. The account is bound to the immutable `oid`: if someone else later takes over the UPN, they cannot sign in to another person's account.
- The profile and the administrator role are updated at every sign-in. Such accounts have no password, and password sign-in is rejected for them.
- Turning off Entra ID sign-in ends the sessions of Entra ID users. It cannot be turned off if there is no active local administrator.
