import { Check, ExternalLink, Lightbulb, X } from 'lucide-react'
import { api } from '../../api'
import { ErrorBanner, ErrorFlash } from '../../connections/ConnectionCard'
import { useAction, useResource } from '../../connections/useRequest'
import { useLocale, useT } from '../../i18n'
import { Button, formatDate } from '../../ui'
import { useSession } from '../session'
import { proposalStrings } from './proposalStrings'

export type ProposalEnd = { source_ref?: string; name?: string; ip?: string; ci_id: string }

export type Proposal = {
  key: string
  source: string
  type: string
  from: ProposalEnd
  to: ProposalEnd
  evidence: {
    protocol?: string
    port?: number
    app?: string
    bytes_7d: number
    flows_7d: number
    days_seen: number
    first_seen?: string
    last_seen?: string
    flow_url?: string
  }
  status: 'pending' | 'accepted' | 'rejected'
  refreshed_at: string
  from_ci: { id: string; name: string } | null
  to_ci: { id: string; name: string } | null
  dependencies: { service_id: string; service_name: string; depends_on_id: string; depends_on_name: string; exists: boolean }[]
  new: boolean
}

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']

export function formatBytes(n: number) {
  let v = n
  let i = 0
  while (v >= 1000 && i < UNITS.length - 1) {
    v /= 1000
    i++
  }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${UNITS[i]}`
}

// ProposalsPanel lists the service dependencies Umbrella Flow proposes from steady traffic between
// configuration items. Accepting creates the dependency (services:edit); rejecting hides the
// proposal until its traffic grows ten times.
export function ProposalsPanel({ epoch, onDecided, onSelect }: { epoch: number; onDecided: () => void; onSelect: (id: string) => void }) {
  const t = useT(proposalStrings)
  const { locale } = useLocale()
  const { can, timezone } = useSession()
  const editable = can('services:edit')
  const list = useResource<Proposal[]>('/api/cmdb/proposals', epoch)
  const action = useAction()

  if (list.error) return <ErrorBanner error={list.error} strings={proposalStrings} />
  if (!list.data || list.data.length === 0) return null

  const decide = async (p: Proposal, verdict: 'accept' | 'reject') => {
    const ok = await action.run(() => api('POST', `/api/cmdb/proposals/${verdict}`, { key: p.key }))
    if (ok) {
      void list.reload()
      onDecided()
    }
  }
  const endName = (ci: Proposal['from_ci'], end: ProposalEnd) => ci?.name ?? end.name ?? end.ip ?? end.source_ref ?? '—'

  return (
    <details className="card map-proposals" open>
      <summary>
        <Lightbulb size={16} aria-hidden />
        {t('pr.title', { n: list.data.length })}
      </summary>
      <p className="muted">{t('pr.hint')}</p>
      <ul className="map-proposal-list">
        {list.data.map((p) => {
          const ev = p.evidence
          const proto = [ev.protocol, ev.port ? String(ev.port) : ''].filter(Boolean).join('/')
          return (
            <li key={p.key} className="map-proposal">
              <div className="map-proposal-main">
                <div className="map-proposal-ends">
                  <button type="button" className="map-proposal-link" onClick={() => p.from_ci && onSelect(p.from_ci.id)}>
                    {endName(p.from_ci, p.from)}
                  </button>
                  <span aria-hidden> → </span>
                  <button type="button" className="map-proposal-link" onClick={() => p.to_ci && onSelect(p.to_ci.id)}>
                    {endName(p.to_ci, p.to)}
                  </button>
                  <span className="muted"> · {[proto, ev.app].filter(Boolean).join(' · ') || t('pr.unknownPort')}</span>
                </div>
                <div className="map-proposal-deps">
                  {p.dependencies.length === 0 ? (
                    <span className="muted">{t('pr.noServices')}</span>
                  ) : (
                    p.dependencies.map((d) => (
                      <span key={d.service_id + d.depends_on_id} className={d.exists ? 'muted' : ''}>
                        {t('pr.dep', { from: d.service_name, to: d.depends_on_name })}
                        {d.exists && ` (${t('pr.exists')})`}
                      </span>
                    ))
                  )}
                </div>
                <small className="muted">
                  {[
                    t('pr.volume', { bytes: formatBytes(ev.bytes_7d), flows: ev.flows_7d.toLocaleString(locale) }),
                    t('pr.days', { n: ev.days_seen }),
                    ev.last_seen ? t('pr.lastSeen', { at: formatDate(ev.last_seen, locale, timezone) }) : '',
                  ]
                    .filter(Boolean)
                    .join(' · ')}
                </small>
              </div>
              <div className="map-proposal-actions">
                {ev.flow_url && (
                  <a className="btn btn-ghost" href={ev.flow_url} target="_blank" rel="noreferrer">
                    <ExternalLink size={14} aria-hidden />
                    {t('pr.flow')}
                  </a>
                )}
                {editable && (
                  <>
                    <Button variant="ghost" busy={action.busy} onClick={() => void decide(p, 'reject')} title={t('pr.reject.hint')}>
                      <X size={14} aria-hidden />
                      {t('pr.reject')}
                    </Button>
                    <Button busy={action.busy} disabled={!p.new} onClick={() => void decide(p, 'accept')} title={p.new ? undefined : t('pr.accept.none')}>
                      <Check size={14} aria-hidden />
                      {t('pr.accept')}
                    </Button>
                  </>
                )}
              </div>
            </li>
          )
        })}
      </ul>
      <ErrorFlash error={action.error} strings={proposalStrings} />
    </details>
  )
}
