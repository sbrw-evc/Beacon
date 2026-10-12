import { useT } from '../../i18n'
import { strings } from './strings'
import type { CI } from './types'

export function StatusPill({ status }: { status: string }) {
  const t = useT(strings)
  const tone = status === 'active' ? 'ok' : status === 'failed' ? 'error' : status === 'offline' || status === 'decommissioning' ? 'warn' : 'off'
  const label = t(`ci.status.${status}`)
  return <span className={`pill pill-${tone}`}>{label.startsWith('ci.status.') ? status : label}</span>
}

export function SourcePill({ ci }: { ci: CI }) {
  const t = useT(strings)
  const key =
    ci.source === 'netbox' ? 'ci.source.netbox' : ci.source === 'inventory-db' ? 'ci.source.inventory-db' : ci.netbox ? 'ci.source.local.registered' : 'ci.source.local'
  return <span className={`pill ci-source ci-source-${ci.source}`}>{t(key)}</span>
}

// PresenceCell: the systems the item is in (filled) and those it is missing from (struck out).
export function PresenceCell({ ci }: { ci: CI }) {
  const t = useT(strings)
  if (ci.presence.length === 0) return <span className="muted">{t('ci.none')}</span>
  return (
    <div className="ci-presence">
      {ci.presence.map((p) => (
        <span
          key={p.kind + (p.source_id ?? '')}
          className={`pill ${p.state === 'present' ? `pill-${tone(p.host_state)}` : 'ci-absent'}`}
          title={t(p.state === 'present' ? 'ci.presence.in' : 'ci.presence.out', { name: p.name })}
        >
          {p.kind === 'directory' ? 'AD' : p.name}
        </span>
      ))}
    </div>
  )
}

function tone(state?: string) {
  return state === 'down' ? 'error' : state === 'partial' || state === 'disabled' ? 'warn' : 'ok'
}

export function MonitorState({ state }: { state: string }) {
  const t = useT(strings)
  const tone = state === 'up' ? 'ok' : state === 'down' ? 'error' : state === 'partial' ? 'warn' : 'off'
  return <span className={`pill pill-${tone}`}>{t(`ci.mon.state.${state}`)}</span>
}
