import { useMemo, useState } from 'react'
import { UserPlus } from 'lucide-react'
import { api } from '../../api'
import { useT } from '../../i18n'
import { Button, Select } from '../../ui'
import { roleLabel } from '../types'
import { MemberList } from '../org/MemberList'
import { MemberPicker } from '../org/MemberPicker'
import { pickable, type UserRef } from '../org/types'
import { useAction } from '../profile/useAction'
import { ADMIN, type Role } from './permissions'
import { strings } from './strings'
import { Flash } from '../../notify'

export function RoleMembers({
  role,
  roles,
  users,
  editable,
  onMoved,
}: {
  role: Role
  roles: Role[]
  users: UserRef[]
  editable: boolean
  onMoved: (target: string, ids: string[]) => Promise<void>
}) {
  const t = useT(strings)
  const action = useAction(strings)
  const [picking, setPicking] = useState(false)
  const known = useMemo(() => roles.flatMap((r) => r.members), [roles])
  const candidates = useMemo(() => pickable(users, known), [users, known])
  const names = useMemo(() => new Map(roles.map((r) => [r.id, roleLabel(t, r.id, r.name)])), [roles, t])

  const move = (target: string, ids: string[]) =>
    action.run(async () => {
      await api('PUT', `/api/roles/${encodeURIComponent(target)}/members`, { user_ids: ids })
      await onMoved(target, ids)
      setPicking(false)
    })

  return (
    <div className="stack">
      <div className="org-section-head">
        <p className="muted">{t(role.id === ADMIN ? 'roles.members.admin' : 'roles.members.text')}</p>
        {editable && (
          <Button onClick={() => setPicking(true)}>
            <UserPlus size={16} aria-hidden />
            {t('roles.members.add')}
          </Button>
        )}
      </div>
      {action.error && (
        <Flash kind="error" title={action.error.message}>
          {action.error.detail}
        </Flash>
      )}
      <MemberList
        members={role.members}
        empty={t('roles.members.empty')}
        aside={
          editable
            ? (m) => (
                <Select
                  aria-label={t('roles.members.move', { name: m.name })}
                  value={role.id}
                  disabled={action.busy}
                  onChange={(e) => void move(e.target.value, [m.id])}
                >
                  {roles.map((r) => (
                    <option key={r.id} value={r.id}>
                      {r.id === role.id ? roleLabel(t, r.id, r.name) : t('roles.members.moveTo', { role: roleLabel(t, r.id, r.name) })}
                    </option>
                  ))}
                </Select>
              )
            : undefined
        }
      />
      <MemberPicker
        open={picking}
        title={t('roles.members.addTo', { role: roleLabel(t, role.id, role.name) })}
        users={candidates}
        exclude={role.members.map((m) => m.id)}
        note={(u) => t('roles.members.from', { role: names.get(u.role_id) ?? u.role_id })}
        action={action}
        confirmLabel={t('roles.members.confirm')}
        onClose={() => setPicking(false)}
        onConfirm={(ids) => void move(role.id, ids)}
      />
    </div>
  )
}
