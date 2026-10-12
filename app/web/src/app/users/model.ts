import { TreeIndex } from '../org/treeIndex'
import { ADMIN } from '../roles/permissions'
import type { User } from '../types'

export type ScopeService = { id: string; name: string; missing?: boolean }

export type ManagedUser = User & {
  display_name: string
  teams?: ScopeService[]
  services?: ScopeService[]
  disabled: boolean
  created_at: string
  password_changed_at?: string
  /** A NetBox contact account that the next NetBox synchronization creates again. */
  netbox_recreates?: boolean
}

export type RoleRef = { id: string; name: string; system: boolean }
export type TeamRef = { id: string; name: string; parent_id: string }
export type ServiceRef = { id: string; name: string }
export type Refs = { roles: RoleRef[]; teams: TeamRef[]; services: ServiceRef[]; new_user_role?: string }

export type Filters = { q: string; source: string; role: string; team: string; status: string }

export const NO_FILTERS: Filters = { q: '', source: '', role: '', team: '', status: '' }

export type UserStatus = 'locked' | 'must_change' | 'expired' | 'active'

export function statusOf(u: ManagedUser): UserStatus {
  if (u.disabled) return 'locked'
  if (u.must_change_password) return 'must_change'
  if (u.password_expired) return 'expired'
  return 'active'
}

export function usersQuery(f: Filters) {
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(f)) if (v.trim()) params.set(k, v.trim())
  const s = params.toString()
  return s ? `/api/users?${s}` : '/api/users'
}

export type TeamOption = { id: string; name: string; depth: number; path: string }

export function teamOptions(teams: TeamRef[]): TeamOption[] {
  const index = new TreeIndex(teams)
  return index.flat().map(({ item, depth }) => ({ id: item.id, name: item.name, depth, path: index.path(item.id) }))
}

export function adminOf(u: { role: string }) {
  return u.role === ADMIN
}

/** scopeModeOf: the scope mode, also for accounts saved before modes existed. */
export function scopeModeOf(u: { scope_mode?: string; service_ids?: string[] }): 'all' | 'teams' | 'services' {
  if (u.scope_mode === 'teams' || u.scope_mode === 'services' || u.scope_mode === 'all') return u.scope_mode
  return (u.service_ids?.length ?? 0) > 0 ? 'services' : 'all'
}

/** scopedOf: the user sees incidents of some business services only. */
export function scopedOf(u: { role: string; scope_mode?: string; service_ids?: string[] }) {
  return !adminOf(u) && scopeModeOf(u) !== 'all'
}
