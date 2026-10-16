import { useEffect, useState, type ReactNode } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { Trash2 } from 'lucide-react'
import { api } from '../../api'
import { Avatar } from '../../Avatar'
import { useLocale, useT } from '../../i18n'
import { Banner, Button, formatDate, Modal, Rows, SettingRow } from '../../ui'
import { ProfileFieldsGrid } from '../profile/ProfileFieldsGrid'
import { useAction, type Action } from '../profile/useAction'
import { ADMIN } from '../roles/permissions'
import { useSession } from '../session'
import { profileOf } from '../types'
import { AccessFields, ScopeField, type Access, type ScopeMode } from './AccessFields'
import { SourcePill, StatusPill } from './Badges'
import { adminOf, scopeModeOf, type ManagedUser, type Refs } from './model'
import { strings } from './strings'
import { Flash } from '../../notify'

type Mode = 'edit' | 'delete'

const accessOf = (u: ManagedUser): Access => ({ role_id: u.role, team_ids: u.team_ids ?? [] })
const sameIds = (a: string[], b: string[]) => a.length === b.length && [...a].sort().every((x, i) => x === [...b].sort()[i])

export function UserDialog({
  user,
  refs,
  onClose,
  onChanged,
  onDeleted,
}: {
  user: ManagedUser | null
  refs: Refs
  onClose: () => void
  onChanged: (u: ManagedUser) => void
  onDeleted: (u: ManagedUser) => void
}) {
  const t = useT(strings)
  const [mode, setMode] = useState<Mode>('edit')
  const [last, setLast] = useState(user)
  const shown = user ?? last
  const name = shown?.display_name ?? ''
  const titles: Record<Mode, string> = {
    edit: name,
    delete: t('usr.delete.title', { name }),
  }

  useEffect(() => {
    if (user) setLast(user)
  }, [user])

  useEffect(() => {
    setMode('edit')
  }, [user?.id])

  const switchTo = (m: Mode) => setMode(m)

  return (
    <Modal open={!!user} title={titles[mode]} onClose={onClose}>
      {shown && (
        <AnimatePresence mode="wait" initial={false}>
          <motion.div
            key={mode}
            className="stack"
            initial={{ opacity: 0, x: mode === 'edit' ? -12 : 12 }}
            animate={{ opacity: 1, x: 0 }}
            exit={{ opacity: 0, x: mode === 'edit' ? 12 : -12 }}
            transition={{ duration: 0.18, ease: [0.22, 1, 0.36, 1] }}
          >
            {mode === 'edit' && <EditView user={shown} refs={refs} onChanged={onChanged} onMode={switchTo} />}
            {mode === 'delete' && <DeleteView user={shown} onBack={() => switchTo('edit')} onDeleted={onDeleted} />}
          </motion.div>
        </AnimatePresence>
      )}
    </Modal>
  )
}

function Outcome({ action }: { action: Action }) {
  return (
    <>
      {action.notice && <Flash kind="ok" title={action.notice} />}
      {action.error && (
        <Flash kind="error" title={action.error.message}>
          {action.error.detail}
        </Flash>
      )}
    </>
  )
}

function Footer({ children }: { children: ReactNode }) {
  return <div className="modal-foot">{children}</div>
}

function useRights(user: ManagedUser) {
  const { can, user: me } = useSession()
  const self = user.id === me.id
  const manageable = !adminOf(user) || adminOf(me)
  // People with access in SPA are removed in SPA; Umbrella removes only the records of people
  // SPA disabled and the accounts of earlier versions.
  const removable = user.source !== 'spa' || user.disabled
  return {
    self,
    manageable,
    edit: manageable && can('users:edit'),
    remove: manageable && !self && removable && can('users:delete'),
  }
}

function UserSummary({ user }: { user: ManagedUser }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const date = (v?: string) => formatDate(v, locale, timezone)
  return (
    <>
      <div className="usr-summary">
        <Avatar user={user} size={56} />
        <div className="usr-names">
          <strong>{user.display_name}</strong>
          <span className="muted">{user.username}</span>
          <span className="row usr-pills">
            <SourcePill user={user} />
            <StatusPill user={user} />
          </span>
        </div>
      </div>
      <Rows
        rows={[
          [t('usr.field.created'), date(user.created_at)],
          [t('usr.col.lastLogin'), date(user.last_login_at) || t('usr.never')],
          [t('usr.field.groups'), (user.groups ?? []).join(', ')],
        ]}
      />
    </>
  )
}

function EditView({
  user,
  refs,
  onChanged,
  onMode,
}: {
  user: ManagedUser
  refs: Refs
  onChanged: (u: ManagedUser) => void
  onMode: (m: Mode) => void
}) {
  const t = useT(strings)
  const action = useAction(strings)
  const rights = useRights(user)
  const [access, setAccess] = useState<Access>(() => accessOf(user))
  const [scope, setScope] = useState<string[]>(() => user.service_ids ?? [])
  const [mode, setMode] = useState<ScopeMode>(() => scopeModeOf(user))

  useEffect(() => {
    setAccess(accessOf(user))
    setScope(user.service_ids ?? [])
    setMode(scopeModeOf(user))
  }, [user])

  const saved = accessOf(user)
  const savedScope = user.service_ids ?? []
  const scopeChanged = mode !== scopeModeOf(user) || scope.length !== savedScope.length || scope.some((id) => !savedScope.includes(id))
  const dirty = !sameIds(access.team_ids, saved.team_ids) || scopeChanged

  const save = () =>
    action.run(async () => {
      const body = { team_ids: access.team_ids, scope_mode: mode, service_ids: scope }
      onChanged(await api<ManagedUser>('PUT', `/api/users/${encodeURIComponent(user.id)}`, body))
      return t('usr.saved')
    })

  const roleHint = t('usr.role.spa')

  return (
    <>
      <UserSummary user={user} />
      {!rights.manageable && <Banner kind="info" title={t('usr.adminOnly')} />}
      <section className="stack usr-section">
        <h3>{t('usr.section.profile')}</h3>
        <p className="muted">{t('usr.spa.profile')}</p>
        <ProfileFieldsGrid value={profileOf(user)} onChange={() => undefined} editable={false} />
      </section>
      <section className="stack usr-section">
        <h3>{t('usr.section.access')}</h3>
        <fieldset className="plain-fieldset stack" disabled={!rights.edit}>
          <AccessFields refs={refs} value={access} onChange={setAccess} roleLocked roleHint={roleHint} />
          <ScopeField
            refs={refs}
            mode={mode}
            value={scope}
            known={user.services}
            admin={access.role_id === ADMIN}
            onMode={setMode}
            onChange={setScope}
          />
        </fieldset>
      </section>
      {user.source === 'spa' && !user.disabled && <p className="muted">{t('usr.spa.lock')}</p>}
      {rights.remove && (
        <section className="stack usr-section">
          <h3>{t('usr.section.actions')}</h3>
          <div className="setting-rows">
            <SettingRow label={t('usr.delete')} hint={t('usr.delete.hint')}>
              <Button variant="danger-soft" onClick={() => onMode('delete')}>
                <Trash2 size={16} aria-hidden />
                {t('usr.delete')}
              </Button>
            </SettingRow>
          </div>
        </section>
      )}
      <Outcome action={action} />
      {rights.edit && (
        <Footer>
          <Button variant="primary" onClick={save} busy={action.busy} disabled={!dirty}>
            {t('usr.save')}
          </Button>
        </Footer>
      )}
    </>
  )
}

function DeleteView({ user, onBack, onDeleted }: { user: ManagedUser; onBack: () => void; onDeleted: (u: ManagedUser) => void }) {
  const t = useT(strings)
  const action = useAction(strings)
  const remove = () =>
    action.run(async () => {
      await api('DELETE', `/api/users/${encodeURIComponent(user.id)}`)
      onDeleted(user)
    })
  return (
    <>
      <Banner kind="warn" title={t('usr.delete.text', { login: user.username })}>
        {user.source === 'spa' && t('usr.delete.spa')}
        {user.source === 'netbox' && t(user.netbox_recreates ? 'usr.delete.netbox' : 'usr.delete.netbox.off')}
      </Banner>
      <Outcome action={action} />
      <Footer>
        <Button variant="ghost" onClick={onBack}>
          {t('usr.back')}
        </Button>
        <Button variant="danger" onClick={remove} busy={action.busy}>
          <Trash2 size={16} aria-hidden />
          {t('usr.delete.confirm')}
        </Button>
      </Footer>
    </>
  )
}
