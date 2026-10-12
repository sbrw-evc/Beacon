import { useCallback, useEffect, useState } from 'react'
import { Plus, Users } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { api } from '../../api'
import { useT } from '../../i18n'
import { Banner, Button } from '../../ui'
import { useSession } from '../session'
import { roleLabel } from '../types'
import { loadRefs, type UserRef } from '../org/types'
import { useAction } from '../profile/useAction'
import { changes, draftOf, type Catalog, type Draft, type Role } from './permissions'
import { CreateRoleDialog, DeleteRoleDialog } from './RoleDialogs'
import { RoleDetail } from './RoleDetail'
import { strings } from './strings'
import '../org/org.css'
import './roles.css'

export function RolesPage() {
  const t = useT(strings)
  const { can, user, refresh } = useSession()
  const editable = can('roles:edit')
  const loader = useAction(strings)
  const [catalog, setCatalog] = useState<Catalog | null>(null)
  const [roles, setRoles] = useState<Role[] | null>(null)
  const [users, setUsers] = useState<UserRef[]>([])
  const [selected, setSelected] = useState('')
  const [drafts, setDrafts] = useState<Record<string, Draft>>({})
  const [creating, setCreating] = useState<{ source: string } | null>(null)
  const [deleting, setDeleting] = useState<Role | null>(null)

  const reload = useCallback(async () => {
    const [c, r, refs] = await Promise.all([api<Catalog>('GET', '/api/access/catalog'), api<Role[]>('GET', '/api/roles'), loadRefs()])
    setCatalog(c)
    setRoles(r)
    setUsers(refs.users)
    setSelected((cur) => (r.some((x) => x.id === cur) ? cur : (r[0]?.id ?? '')))
  }, [])

  const { run: load } = loader
  useEffect(() => {
    void load(reload)
  }, [load, reload])

  if (!catalog || !roles) {
    return loader.error ? (
      <Banner kind="error" title={loader.error.message}>
        {loader.error.detail}
      </Banner>
    ) : null
  }

  const role = roles.find((r) => r.id === selected)
  const setDraft = (id: string, d: Draft | null) =>
    setDrafts((prev) => {
      const next = { ...prev }
      if (d) next[id] = d
      else delete next[id]
      return next
    })
  const dirty = (r: Role) => drafts[r.id] !== undefined && changes(catalog, r, drafts[r.id]).dirty

  const afterChange = async (touchesMe: boolean) => {
    await reload()
    if (touchesMe) await refresh()
  }

  return (
    <div className="org-split">
      <aside className="card org-side" aria-label={t('roles.list')}>
        <div className="org-side-head">
          <span className="section-title">{t('roles.list')}</span>
          {editable && (
            <Button variant="ghost" onClick={() => setCreating({ source: '' })}>
              <Plus size={16} aria-hidden />
              {t('roles.new')}
            </Button>
          )}
        </div>
        <ul className="roles-list">
          {roles.map((r) => (
            <li key={r.id}>
              <button
                type="button"
                className={`roles-item${r.id === selected ? ' active' : ''}`}
                onClick={() => setSelected(r.id)}
                aria-current={r.id === selected}
              >
                {r.id === selected && (
                  <motion.span layoutId="roles-active" className="roles-item-bg" transition={{ type: 'spring', stiffness: 420, damping: 34 }} />
                )}
                <span className="roles-item-name">
                  {roleLabel(t, r.id, r.name)}
                  {dirty(r) && <span className="roles-dot" title={t('roles.unsaved')} />}
                </span>
                <span className="roles-item-meta">
                  {r.new_users && (
                    <span className="pill pill-ok" title={t('roles.newUsers.on')}>
                      {t('roles.newUsers.short')}
                    </span>
                  )}
                  {r.system && <span className="pill pill-off">{t('roles.system')}</span>}
                  <span className="org-count" title={t('roles.members.count')}>
                    <Users size={13} aria-hidden />
                    {r.member_count}
                  </span>
                </span>
              </button>
            </li>
          ))}
        </ul>
      </aside>
      <section className="card org-detail" aria-live="polite">
        <AnimatePresence mode="wait" initial={false}>
          {role && (
            <motion.div
              key={role.id}
              initial={{ opacity: 0, x: 12 }}
              animate={{ opacity: 1, x: 0 }}
              exit={{ opacity: 0, x: -8 }}
              transition={{ duration: 0.18, ease: [0.22, 1, 0.36, 1] }}
            >
              <RoleDetail
                role={role}
                roles={roles}
                catalog={catalog}
                users={users}
                draft={drafts[role.id] ?? draftOf(role)}
                editable={editable}
                onDraft={(d) => setDraft(role.id, d)}
                onSaved={async (saved) => {
                  setDraft(saved.id, null)
                  await afterChange(saved.id === user.role)
                }}
                onMoved={(target, ids) => afterChange(target === user.role || ids.includes(user.id))}
                onDuplicate={() => setCreating({ source: role.id })}
                onDelete={() => setDeleting(role)}
                onNewUsers={reload}
              />
            </motion.div>
          )}
        </AnimatePresence>
      </section>
      <CreateRoleDialog
        open={creating !== null}
        roles={roles}
        source={creating?.source ?? ''}
        onClose={() => setCreating(null)}
        onCreated={async (r) => {
          setCreating(null)
          await reload()
          setSelected(r.id)
        }}
      />
      <DeleteRoleDialog
        role={deleting}
        roles={roles}
        onClose={() => setDeleting(null)}
        onDeleted={async (id) => {
          setDeleting(null)
          setDraft(id, null)
          await afterChange(id === user.role)
        }}
      />
    </div>
  )
}
