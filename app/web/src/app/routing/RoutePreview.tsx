import { useEffect, useState, type ReactNode } from 'react'
import { api } from '../../api'
import { useT } from '../../i18n'
import { severityText } from '../incidents/types'
import { Rows } from '../../ui'
import { strings } from './strings'
import './routing.css'

type Ref = { id: string; name: string }
type Person = { user_id: string; name: string; email?: string; telegram?: string; role?: string }

export type RoutePreviewData = {
  services: Ref[]
  service?: Ref
  team?: Ref
  people: Person[]
  owners: Person[]
  // teams and zoom: webhook URLs, redacted by the server.
  channel?: { email?: string; telegram?: string; teams?: string; zoom?: string }
  via: 'service' | 'ci_owners' | 'none'
  pagerduty_route: { id: string; name: string; min_severity: string; no_key?: boolean } | null
  elsewhere?: { ci: Ref; service: Ref; team?: Ref }[]
  // backup: null (or absent from an older server) while backup notification is off.
  backup?: { targets: { channel: string; address: string }[]; delay_seconds: number; min_severity: string } | null
}

// At most this many backup addresses are listed; the rest are counted.
const MAX_TARGETS = 6

/** RoutePreview shows where an incident of a CI or a service would go now (GET /api/{kind}/{id}/route). */
export function RoutePreview({ kind, id, version }: { kind: 'cis' | 'services'; id: string; version?: unknown }) {
  const t = useT(strings)
  const [data, setData] = useState<RoutePreviewData | null>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let live = true
    setFailed(false)
    api<RoutePreviewData>('GET', `/api/${kind}/${encodeURIComponent(id)}/route`)
      .then((d) => live && setData(d))
      .catch(() => live && setFailed(true))
    return () => {
      live = false
    }
  }, [kind, id, version])

  let body: ReactNode
  if (failed) body = <p className="muted">{t('rt.error')}</p>
  else if (!data) body = <p className="muted">{t('rt.loading')}</p>
  else body = <RouteRows data={data} />
  return (
    <section className="rt-preview" aria-label={t('rt.title')}>
      <div className="section-title">{t('rt.title')}</div>
      {body}
      <p className="muted rt-rule">{t('rt.rule')}</p>
    </section>
  )
}

// CHANNEL_NAMES label addresses that do not tell their channel themselves (an e-mail does).
const CHANNEL_NAMES: Record<string, string> = { telegram: 'Telegram', teams: 'Teams', zoom: 'Zoom' }

function targetLabel(channel: string, address: string) {
  return CHANNEL_NAMES[channel] ? `${CHANNEL_NAMES[channel]} ${address}` : address
}

function names(ps: Person[], lead: string) {
  return ps.map((p) => (p.role === 'lead' ? `${p.name} (${lead})` : p.name)).join(', ')
}

function RouteRows({ data }: { data: RoutePreviewData }) {
  const t = useT(strings)
  const others = data.services.filter((s) => s.id !== data.service?.id)
  const channel = (['email', 'telegram', 'teams', 'zoom'] as const)
    .filter((c) => data.channel?.[c])
    .map((c) => targetLabel(c, data.channel?.[c] ?? ''))
    .join(', ')
  let receives: ReactNode
  if (data.via === 'service') {
    receives = (
      <span>
        {data.people.length > 0 && t('rt.people.n', { n: data.people.length, list: names(data.people, t('rt.lead')) })}
        {data.people.length > 0 && channel && ' · '}
        {channel && t('rt.channel', { list: channel })}
      </span>
    )
  } else if (data.via === 'ci_owners') {
    receives = t('rt.people.owners', { n: data.owners.length, list: names(data.owners, t('rt.lead')) })
  } else {
    receives = <span className="rt-warn">{t('rt.people.none')}</span>
  }
  let pd: ReactNode = <span className="muted">{t('rt.pd.off')}</span>
  if (data.pagerduty_route) {
    const r = data.pagerduty_route
    const name = r.id === 'default' ? t(r.no_key ? 'rt.pd.nokey' : 'rt.pd.default') : r.name
    pd = (
      <span>
        {name}
        {r.min_severity && <span className="muted"> · {t('rt.pd.min', { sev: severityText(t, r.min_severity) })}</span>}
      </span>
    )
  }
  let backup: ReactNode = <span className="muted">{t('rt.backup.off')}</span>
  if (data.backup) {
    const b = data.backup
    const shown = b.targets.slice(0, MAX_TARGETS).map((x) => targetLabel(x.channel, x.address))
    const more = b.targets.length - shown.length
    const delay = b.delay_seconds === 0 ? t('rt.backup.now') : b.delay_seconds % 60 === 0 ? t('rt.backup.min', { n: b.delay_seconds / 60 }) : t('rt.backup.sec', { n: b.delay_seconds })
    backup = (
      <span>
        {b.targets.length > 0 ? shown.join(', ') : <span className="rt-warn">{t('rt.backup.nobody')}</span>}
        {more > 0 && <span className="muted"> {t('rt.backup.more', { n: more })}</span>}
        <span className="muted">
          {' '}
          · {delay} · {t('rt.pd.min', { sev: severityText(t, b.min_severity) })}
        </span>
      </span>
    )
  }
  const rows: [ReactNode, ReactNode][] = [
    [
      t('rt.service'),
      data.service ? (
        <span>
          <strong>{data.service.name}</strong>
          {data.services.length > 1 && <span className="muted"> · {t('rt.service.primary')}</span>}
          {others.length > 0 && <span className="muted"> · {t('rt.service.others', { list: others.map((s) => s.name).join(', ') })}</span>}
        </span>
      ) : (
        <span className="rt-warn">{t('rt.service.none')}</span>
      ),
    ],
    [t('rt.team'), data.team ? data.team.name : <span className="muted">{t('rt.team.none')}</span>],
    [t('rt.people'), receives],
    [t('rt.pd'), pd],
    [t('rt.backup'), backup],
  ]
  return (
    <>
      <Rows rows={rows} />
      {data.elsewhere && data.elsewhere.length > 0 && (
        <div className="rt-elsewhere">
          <p className="muted">{t('rt.elsewhere')}</p>
          <ul>
            {data.elsewhere.map((e) => (
              <li key={e.ci.id}>{t('rt.elsewhere.row', { ci: e.ci.name, service: e.service.name, team: e.team?.name ?? t('rt.elsewhere.noteam') })}</li>
            ))}
          </ul>
        </div>
      )}
    </>
  )
}
