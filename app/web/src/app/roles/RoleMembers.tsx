import { useT } from '../../i18n'
import { MemberList } from '../org/MemberList'
import type { UserRef } from '../org/types'
import type { Role } from './permissions'
import { strings } from './strings'

// RoleMembers lists who has the role. Roles are given in SPA, the single point of authorization,
// under the ID the catalogue publishes (the role ID in lower case).
export function RoleMembers({ role }: { role: Role; roles?: Role[]; users?: UserRef[]; editable?: boolean; onMoved?: (target: string, ids: string[]) => Promise<void> }) {
  const t = useT(strings)
  return (
    <div className="stack">
      <p className="muted">{t('roles.members.spa', { id: role.id.toLowerCase() })}</p>
      <MemberList members={role.members} empty={t('roles.members.empty')} />
    </div>
  )
}
