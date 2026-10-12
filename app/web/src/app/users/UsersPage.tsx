import { useCallback, useEffect, useRef, useState } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { api } from '../../api'
import { useT } from '../../i18n'
import { Banner } from '../../ui'
import { useAction } from '../profile/useAction'
import { useSession } from '../session'
import { CreateUserDialog } from './CreateUserDialog'
import { NO_FILTERS, usersQuery, type Filters, type ManagedUser, type Refs } from './model'
import { strings } from './strings'
import { UserDialog } from './UserDialog'
import { UsersList } from './UsersList'
import { UsersToolbar } from './UsersToolbar'
import './users.css'
import { notify } from '../../notify'
import { SetupGuide } from '../guide/SetupGuide'

export function UsersPage() {
  const t = useT(strings)
  const { user: me, refresh } = useSession()
  const loader = useAction(strings)
  const [refs, setRefs] = useState<Refs | null>(null)
  const [users, setUsers] = useState<ManagedUser[] | null>(null)
  const [total, setTotal] = useState(0)
  const [filters, setFilters] = useState<Filters>(NO_FILTERS)
  const [selected, setSelected] = useState<ManagedUser | null>(null)
  const [creating, setCreating] = useState(0)
  const request = useRef(0)

  const { run } = loader
  const load = useCallback(
    (f: Filters) =>
      run(async () => {
        const n = ++request.current
        const [list, r] = await Promise.all([api<ManagedUser[]>('GET', usersQuery(f)), api<Refs & { users: unknown[] }>('GET', '/api/refs')])
        if (n !== request.current) return
        setUsers(list)
        setTotal(r.users.length)
        setRefs({ roles: r.roles, teams: r.teams, services: r.services ?? [] })
      }),
    [run],
  )

  useEffect(() => {
    void load(filters)
  }, [filters, load])

  const changed = (u: ManagedUser) => {
    setSelected(u)
    void load(filters)
    if (u.id === me.id) void refresh()
  }

  const deleted = (u: ManagedUser) => {
    setSelected(null)
    notify({ kind: 'ok', title: t('usr.deleted', { name: u.display_name }) })
    void load(filters)
  }

  const closeCreate = () => setCreating((k) => -Math.abs(k))

  const created = (u: ManagedUser) => {
    closeCreate()
    notify({ kind: 'ok', title: t('usr.created', { name: u.display_name }) })
    void load(filters)
  }

  if (!users || !refs) {
    return loader.error ? (
      <Banner kind="error" title={loader.error.message}>
        {loader.error.detail}
      </Banner>
    ) : (
      <p className="muted">{t('loading')}</p>
    )
  }

  return (
    <div className="stack usr-page">
      <SetupGuide page="users" actions={{ create: () => setCreating((k) => Math.abs(k) + 1) }} />
      <UsersToolbar refs={refs} filters={filters} onChange={setFilters} onCreate={() => setCreating((k) => Math.abs(k) + 1)} />
      <AnimatePresence initial={false}>
        {loader.error && (
          <motion.div key="notice" initial={{ opacity: 0, height: 0 }} animate={{ opacity: 1, height: 'auto' }} exit={{ opacity: 0, height: 0 }}>
            <Banner kind="error" title={loader.error.message}>
              {loader.error.detail}
            </Banner>
          </motion.div>
        )}
      </AnimatePresence>
      <div className="usr-count muted">{t('usr.count', { n: users.length, total })}</div>
      {users.length === 0 ? <p className="card usr-empty muted">{t('usr.empty')}</p> : <UsersList users={users} refs={refs} onOpen={(u) => setSelected(u)} />}
      <UserDialog user={selected} refs={refs} onClose={() => setSelected(null)} onChanged={changed} onDeleted={deleted} />
      <CreateUserDialog key={Math.abs(creating)} open={creating > 0} refs={refs} onClose={closeCreate} onCreated={created} />
    </div>
  )
}
