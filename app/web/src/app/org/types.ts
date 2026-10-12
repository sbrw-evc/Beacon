import { api } from '../../api'
import type { AvatarUser } from '../../Avatar'

export type Member = AvatarUser & {
  source: 'local' | 'ldap' | 'entra'
  disabled: boolean
  role_id: string
  team_ids: string[]
  title?: string
}

export type RoleRef = { id: string; name: string; system: boolean }
export type TeamRef = { id: string; name: string; parent_id: string }
export type UserRef = { id: string; username: string; name: string; source: 'local' | 'ldap' | 'entra'; disabled: boolean; role_id: string; team_ids: string[] }
export type Refs = { roles: RoleRef[]; teams: TeamRef[]; users: UserRef[] }

export function loadRefs() {
  return api<Refs>('GET', '/api/refs')
}

export function pickable(users: UserRef[], known: Member[]): Member[] {
  const byId = new Map(known.map((m) => [m.id, m]))
  return users.map((u) => ({ ...u, ...byId.get(u.id) }))
}

export function byName<T extends { name: string }>(a: T, b: T) {
  return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' })
}
