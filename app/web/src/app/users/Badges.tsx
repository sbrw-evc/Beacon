import { useT } from '../../i18n'
import { statusOf, type ManagedUser } from './model'
import { strings } from './strings'

const tone = { active: 'ok', locked: 'error', expired: 'warn', must_change: 'info' } as const

export function StatusPill({ user }: { user: ManagedUser }) {
  const t = useT(strings)
  const s = statusOf(user)
  return <span className={`pill usr-pill-${tone[s]}`}>{t(`usr.status.${s}`)}</span>
}

export function SourcePill({ user }: { user: ManagedUser }) {
  const t = useT(strings)
  return <span className={`pill usr-source usr-source-${user.source}`}>{t(`usr.source.${user.source}`)}</span>
}
