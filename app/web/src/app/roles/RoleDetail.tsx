import { useState } from 'react'
import { Copy, Trash2, UserPlus } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { api } from '../../api'
import { useLocale, useT } from '../../i18n'
import { Banner, Button, Field, Input, Segmented, Textarea } from '../../ui'
import { roleLabel } from '../types'
import type { UserRef } from '../org/types'
import { useAction } from '../profile/useAction'
import { PermissionMatrix } from './PermissionMatrix'
import { ADMIN, changes, Permissions, type Catalog, type Draft, type Role } from './permissions'
import { RoleMembers } from './RoleMembers'
import { strings } from './strings'
import { Flash } from '../../notify'

type Tab = 'permissions' | 'members'

export function RoleDetail({
  role,
  roles,
  catalog,
  users,
  draft,
  editable,
  onDraft,
  onSaved,
  onMoved,
  onDuplicate,
  onDelete,
  onNewUsers,
}: {
  role: Role
  roles: Role[]
  catalog: Catalog
  users: UserRef[]
  draft: Draft
  editable: boolean
  onDraft: (d: Draft | null) => void
  onSaved: (r: Role) => Promise<void>
  onMoved: (target: string, ids: string[]) => Promise<void>
  onDuplicate: () => void
  onDelete: () => void
  onNewUsers: () => Promise<void>
}) {
  const t = useT(strings)
  const { locale } = useLocale()
  const saver = useAction(strings)
  const defaulter = useAction(strings)
  const [tab, setTab] = useState<Tab>('permissions')
  const admin = role.id === ADMIN
  const perms = Permissions.of(catalog, draft.permissions)
  const diff = changes(catalog, role, draft)
  const granted = admin ? role.permissions.length : perms.list().length

  const save = () =>
    saver.run(async () => {
      const body: Record<string, unknown> = {}
      if (diff.name) body.name = draft.name
      if (diff.description) body.description = draft.description
      if (diff.added.length + diff.removed.length > 0) body.permissions = perms.list()
      await onSaved(await api<Role>('PUT', `/api/roles/${encodeURIComponent(role.id)}`, body))
      return t('roles.saved')
    })

  return (
    <div className="stack">
      <div className="org-detail-head">
        <h2>
          {roleLabel(t, role.id, role.name)}
          {role.system && <span className="pill pill-off">{t('roles.system')}</span>}
        </h2>
        {editable && (
          <div className="org-detail-actions">
            <Button variant="ghost" onClick={onDuplicate}>
              <Copy size={16} aria-hidden />
              {t('roles.duplicate')}
            </Button>
            {!role.system && (
              <Button variant="ghost" onClick={onDelete}>
                <Trash2 size={16} aria-hidden />
                {t('roles.delete')}
              </Button>
            )}
          </div>
        )}
      </div>
      {role.new_users ? (
        <Banner kind="info" title={t('roles.newUsers.on')}>
          {t('roles.newUsers.hint')}
        </Banner>
      ) : (
        editable &&
        !admin && (
          <div className="roles-newusers">
            <Button
              variant="ghost"
              busy={defaulter.busy}
              onClick={() =>
                defaulter.run(async () => {
                  await api<Role>('PUT', `/api/roles/${encodeURIComponent(role.id)}/new-users`)
                  await onNewUsers()
                  return t('roles.newUsers.done')
                })
              }
            >
              <UserPlus size={16} aria-hidden />
              {t('roles.newUsers.set')}
            </Button>
            <span className="muted">{t('roles.newUsers.hint')}</span>
          </div>
        )
      )}
      {defaulter.error && (
        <Flash kind="error" title={defaulter.error.message}>
          {defaulter.error.detail}
        </Flash>
      )}
      <div className="grid-2 roles-fields">
        <Field label={t('roles.name')} hint={role.system ? t('roles.name.system') : undefined}>
          {(id) => (
            <Input
              id={id}
              value={role.system ? roleLabel(t, role.id, role.name) : draft.name}
              maxLength={80}
              disabled={!editable || role.system}
              onChange={(e) => onDraft({ ...draft, name: e.target.value })}
            />
          )}
        </Field>
        <Field label={t('roles.description')}>
          {(id) => (
            <Textarea
              id={id}
              value={draft.description}
              rows={2}
              disabled={!editable}
              placeholder={role.system ? t(`roles.system.${role.id}`) : undefined}
              onChange={(e) => onDraft({ ...draft, description: e.target.value })}
            />
          )}
        </Field>
      </div>
      <Segmented<Tab>
        label={t('roles.tabs')}
        value={tab}
        onChange={setTab}
        options={[
          { value: 'permissions', label: t('roles.tab.permissions', { n: granted }) },
          { value: 'members', label: t('roles.tab.members', { n: role.member_count }) },
        ]}
      />
      <AnimatePresence mode="wait" initial={false}>
        <motion.div
          key={tab}
          className="stack"
          initial={{ opacity: 0, y: 6 }}
          animate={{ opacity: 1, y: 0 }}
          exit={{ opacity: 0, y: -4 }}
          transition={{ duration: 0.16 }}
        >
          {tab === 'permissions' ? (
            <>
              {admin && <Banner kind="info" title={t('roles.admin.all')} />}
              {!editable && !admin && <p className="muted">{t('roles.readOnly')}</p>}
              <PermissionMatrix
                catalog={catalog}
                value={admin ? Permissions.of(catalog, role.permissions) : perms}
                readOnly={!editable || admin}
                onChange={(v) => onDraft({ ...draft, permissions: v.list() })}
              />
            </>
          ) : (
            <RoleMembers role={role} roles={roles} users={users} editable={editable} onMoved={onMoved} />
          )}
        </motion.div>
      </AnimatePresence>
      <AnimatePresence initial={false}>
        {(diff.dirty || saver.error || saver.notice) && (
          <motion.div
            className="card roles-savebar"
            initial={{ opacity: 0, y: 16 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: 16 }}
            transition={{ duration: 0.2, ease: [0.22, 1, 0.36, 1] }}
          >
            {diff.dirty && (
              <div className="roles-diff">
                <strong>{t('roles.diff.title')}</strong>
                <ul>
                  {diff.name && <li>{t('roles.diff.name', { from: role.name, to: draft.name.trim() })}</li>}
                  {diff.description && <li>{t('roles.diff.description')}</li>}
                  {diff.added.map((p) => (
                    <li key={p} className="roles-diff-add">
                      + {perms.describe(p, locale)}
                    </li>
                  ))}
                  {diff.removed.map((p) => (
                    <li key={p} className="roles-diff-remove">
                      − {perms.describe(p, locale)}
                    </li>
                  ))}
                </ul>
              </div>
            )}
            {saver.notice && <Flash kind="ok" title={saver.notice} />}
            {saver.error && (
              <Flash kind="error" title={saver.error.message}>
                {saver.error.detail}
              </Flash>
            )}
            {diff.dirty && (
              <div className="roles-savebar-actions">
                <Button variant="ghost" onClick={() => onDraft(null)} disabled={saver.busy}>
                  {t('roles.discard')}
                </Button>
                <Button variant="primary" busy={saver.busy} disabled={!draft.name.trim()} onClick={save}>
                  {t('roles.save')}
                </Button>
              </div>
            )}
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  )
}
