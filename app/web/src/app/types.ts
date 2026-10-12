export type ProfileFields = {
  last_name: string
  first_name: string
  middle_name: string
  title: string
  department: string
  manager: string
  email: string
}

export type ScopeMode = 'all' | 'teams' | 'services'

export type User = ProfileFields & {
  id: string
  username: string
  name: string
  source: 'local' | 'ldap' | 'entra' | 'netbox'
  role: string
  role_name?: string
  permissions?: string[]
  /** Active administrators; sent only to a user without any permission. */
  admins?: { name: string; email?: string }[]
  team_ids?: string[]
  /** What the user sees: all services, the services of their teams, or the chosen services. Ignored for administrators. */
  scope_mode?: ScopeMode
  /** The chosen services of the services mode. */
  service_ids?: string[]
  must_change_password?: boolean
  timezone: string
  telegram?: string
  /** The interface language the server keeps (voice calls speak it). */
  locale?: string
  last_login_at?: string
  csrf?: string
  gravatar?: string
  has_avatar?: boolean
  avatar_version?: string
  avatar_source?: 'upload' | 'ldap'
  password_expired?: boolean
  password_expiry_warning?: boolean
  password_expires_at?: string
}

export const PROFILE_KEYS: (keyof ProfileFields)[] = ['last_name', 'first_name', 'middle_name', 'title', 'department', 'manager', 'email']

export function profileOf(u: ProfileFields): ProfileFields {
  return Object.fromEntries(PROFILE_KEYS.map((k) => [k, u[k] ?? ''])) as ProfileFields
}

export function profileChanged(a: ProfileFields, b: ProfileFields) {
  return PROFILE_KEYS.some((k) => a[k] !== b[k])
}

export const SYSTEM_ROLES = ['admin', 'user']

export function roleLabel(t: (k: string) => string, id: string, name?: string) {
  return SYSTEM_ROLES.includes(id) ? t(`role.${id}`) : name || id
}

export function fullName(u: User) {
  return [u.last_name, u.first_name, u.middle_name].filter(Boolean).join(' ') || u.name
}
