import { useMemo } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { Avatar } from '../../Avatar'
import { useLocale, useT } from '../../i18n'
import { formatDate } from '../../ui'
import { useSession } from '../session'
import { roleLabel } from '../types'
import { SourcePill, StatusPill } from './Badges'
import { scopedOf, scopeModeOf, teamOptions, type ManagedUser, type Refs } from './model'
import { strings } from './strings'

export function UsersList({ users, refs, onOpen }: { users: ManagedUser[]; refs: Refs; onOpen: (u: ManagedUser) => void }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone, user: me } = useSession()
  const paths = useMemo(() => new Map(teamOptions(refs.teams).map((o) => [o.id, o.path])), [refs.teams])

  return (
    <div className="card usr-list">
      <div className="usr-row usr-head" aria-hidden>
        {['usr.col.user', 'usr.col.role', 'usr.col.team', 'usr.col.source', 'usr.col.status', 'usr.col.lastLogin'].map((k) => (
          <span key={k}>{t(k)}</span>
        ))}
      </div>
      <AnimatePresence initial={false}>
        {users.map((u) => (
          <motion.button
            key={u.id}
            type="button"
            className={`usr-row usr-item ${u.disabled ? 'usr-disabled' : ''}`}
            onClick={() => onOpen(u)}
            layout="position"
            initial={{ opacity: 0, y: 6 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, height: 0, paddingTop: 0, paddingBottom: 0 }}
            transition={{ duration: 0.2, ease: [0.22, 1, 0.36, 1] }}
          >
            <span className="usr-who">
              <Avatar user={u} size={34} />
              <span className="usr-names">
                <strong>
                  {u.display_name}
                  {u.id === me.id && <span className="muted"> ({t('usr.you')})</span>}
                </strong>
                <small className="muted">{u.username}</small>
                {scopedOf(u) && (
                  <small className="usr-scope" title={t('usr.field.scope')}>
                    {scopeModeOf(u) === 'teams' ? t('usr.scope.teams.short') : t('usr.scope.short', { names: (u.services ?? []).map((s) => s.name).join(', ') })}
                  </small>
                )}
              </span>
            </span>
            <span data-label={t('usr.col.role')}>{roleLabel(t, u.role, u.role_name)}</span>
            <span data-label={t('usr.col.team')} className={u.team_ids?.length ? '' : 'muted'}>
              {u.team_ids?.length
                ? u.team_ids.map((id) => paths.get(id) ?? u.teams?.find((x) => x.id === id)?.name ?? id).join(', ')
                : t('usr.noTeam')}
            </span>
            <span data-label={t('usr.col.source')}>
              <SourcePill user={u} />
            </span>
            <span data-label={t('usr.col.status')}>
              <StatusPill user={u} />
            </span>
            <span data-label={t('usr.col.lastLogin')} className="muted">
              {formatDate(u.last_login_at, locale, timezone) || t('usr.never')}
            </span>
          </motion.button>
        ))}
      </AnimatePresence>
    </div>
  )
}
