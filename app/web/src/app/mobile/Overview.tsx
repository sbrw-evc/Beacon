import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { BellRing, CalendarClock, ChevronRight, CircleCheck, Waypoints } from 'lucide-react'
import { api } from '../../api'
import { ErrorBanner } from '../../connections/ConnectionCard'
import { useResource } from '../../connections/useRequest'
import { useLocale, useT } from '../../i18n'
import { notify } from '../../notify'
import { Link, useRouter } from '../../router'
import { formatDate } from '../../ui'
import { problem, rank, type CMDBMap } from '../cmdb/types'
import { IncidentCard } from '../incidents/IncidentCard'
import { useLiveReload } from '../incidents/live'
import { strings as incStrings } from '../incidents/strings'
import { SEVERITIES, SEVERITY_PRIORITY, SEVERITY_TONE, severityText, type Incident, type Page } from '../incidents/types'
import { useSession } from '../session'
import { mobileStrings } from './mobileStrings'

const REFRESH_MS = 30_000
const OFFLINE_MS = 10_000
const ATTENTION = 5
const WINDOWS = 3

type Ref = { id: string; name: string }
type Window = { id: string; title: string; start: string; end: string; state: 'active' | 'planned' | 'finished'; cis: Ref[]; services: Ref[] }

// Overview is the home page of the phone layout: what is on fire now, what it hits and what is
// planned, each block leading to its page.
export function Overview() {
  const { can } = useSession()
  const [epoch, setEpoch] = useState(0)
  const reload = useCallback(() => setEpoch((e) => e + 1), [])
  const live = useLiveReload(reload)
  useEffect(() => {
    const id = window.setInterval(() => {
      if (document.visibilityState === 'visible') reload()
    }, live ? REFRESH_MS : OFFLINE_MS)
    return () => window.clearInterval(id)
  }, [live, reload])

  return (
    <div className="m-ov">
      {can('incidents:view') && <IncidentBlocks epoch={epoch} onChanged={reload} />}
      {can('cmdb:view') && <ServiceBlock epoch={epoch} />}
      {can('maintenance:view') && <MaintenanceBlock epoch={epoch} />}
    </div>
  )
}

function Section({ icon: Icon, title, to, more, children }: { icon: typeof BellRing; title: string; to?: string; more?: string; children: ReactNode }) {
  return (
    <section className="m-ov-section">
      <header>
        <h2>
          <Icon size={16} aria-hidden />
          {title}
        </h2>
        {to && more && (
          <Link to={to} className="m-ov-more">
            {more}
            <ChevronRight size={15} aria-hidden />
          </Link>
        )}
      </header>
      {children}
    </section>
  )
}

function IncidentBlocks({ epoch, onChanged }: { epoch: number; onChanged: () => void }) {
  const t = useT(mobileStrings)
  const ti = useT(incStrings)
  const { navigate } = useRouter()
  const { can, timezone } = useSession()
  const { locale } = useLocale()
  const list = useResource<Page>('/api/incidents?status=active&limit=100', epoch)
  const [acking, setAcking] = useState<string | null>(null)
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => setNow(Date.now()), [list.data])

  if (!list.data) return list.error ? <ErrorBanner error={list.error} strings={incStrings} /> : <p className="muted">{t('m.ov.loading')}</p>
  const { counts, alerts } = list.data
  const top = SEVERITIES.find((s) => (counts.by_severity[s] ?? 0) > 0)
  const order = (a: Incident) => SEVERITIES.indexOf(a.severity)
  const attention = alerts
    .filter((a) => a.status === 'open' && !a.suppressed)
    .sort((a, b) => order(a) - order(b) || b.last_seen.localeCompare(a.last_seen))
    .slice(0, ATTENTION)
  const ack = async (id: string) => {
    setAcking(id)
    try {
      await api('POST', `/api/incidents/${encodeURIComponent(id)}/ack`)
      notify({ kind: 'ok', title: t('m.acked.toast', { id }) })
      onChanged()
    } catch (e) {
      notify({ kind: 'error', title: e instanceof Error ? e.message : String(e) })
    } finally {
      setAcking(null)
    }
  }
  const flags = [
    counts.pd_enabled !== false && counts.pd_not_taken > 0 && (['pd', counts.pd_not_taken] as const),
    counts.fallback > 0 && (['fallback', counts.fallback] as const),
    counts.suppressed > 0 && (['suppressed', counts.suppressed] as const),
  ].filter((x): x is readonly ['pd' | 'fallback' | 'suppressed', number] => !!x)

  return (
    <>
      <Link to={top ? `/incidents?severity=${top}` : '/incidents'} className={`m-ov-hero tone-${top ? SEVERITY_TONE[top] : 'ok'}`}>
        <span className="m-ov-hero-icon">{top ? <BellRing size={22} aria-hidden /> : <CircleCheck size={22} aria-hidden />}</span>
        <span className="m-ov-hero-text">
          <strong>{counts.active === 0 ? t('m.ov.calm') : counts.active === 1 ? t('m.ov.active.one') : t('m.ov.active', { n: counts.active })}</strong>
          <span>{counts.active === 0 ? t('m.ov.calm.text') : t('m.ov.active.text', { open: counts.open, acked: counts.acknowledged })}</span>
          {top && <span className="m-ov-hero-top">{t('m.ov.top', { priority: severityText(ti, top) })}</span>}
        </span>
        <ChevronRight size={18} aria-hidden className="m-ov-hero-chev" />
      </Link>

      <div className="m-ov-prio" role="group" aria-label={t('m.ov.priorities')}>
        {SEVERITIES.map((s) => {
          const n = counts.by_severity[s] ?? 0
          return (
            <button key={s} type="button" className={`m-ov-prio-item tone-${SEVERITY_TONE[s]}${n > 0 ? ' on' : ''}`} aria-label={`${severityText(ti, s)}: ${n}`} onClick={() => navigate(`/incidents?severity=${s}`)}>
              <span className="m-ov-prio-n">{n}</span>
              <span className="m-ov-prio-p">{SEVERITY_PRIORITY[s]}</span>
            </button>
          )
        })}
      </div>

      {flags.length > 0 && (
        <div className="m-ov-flags" aria-label={t('m.ov.flags')}>
          {flags.map(([f, n]) => (
            <Link key={f} to={`/incidents?flag=${f}`} className={`chip m-ov-flag${f === 'suppressed' ? '' : ' warn'}`}>
              {t(`m.ov.flag.${f}`)}
              <strong>{n}</strong>
            </Link>
          ))}
        </div>
      )}

      <Section icon={BellRing} title={t('m.ov.attention')} to="/incidents" more={t('m.ov.all')}>
        {attention.length === 0 ? (
          <p className="muted m-ov-empty">{counts.active === 0 ? t('m.ov.calm.text') : t('m.ov.attention.none')}</p>
        ) : (
          <div className="m-inc-list">
            {attention.map((a) => (
              <IncidentCard
                key={a.id}
                a={a}
                now={now}
                onOpen={() => navigate(`/incidents?id=${encodeURIComponent(a.id)}`)}
                onAck={can('incidents:ack') ? () => void ack(a.id) : undefined}
                busy={acking === a.id}
              />
            ))}
          </div>
        )}
        <p className="muted m-ov-updated">{t('m.ov.updated', { time: formatDate(new Date(now).toISOString(), locale, timezone) })}</p>
      </Section>
    </>
  )
}

function ServiceBlock({ epoch }: { epoch: number }) {
  const t = useT(mobileStrings)
  // The map is heavier than the list: it is read on every second refresh.
  const map = useResource<CMDBMap>('/api/cmdb', Math.floor(epoch / 2))
  if (!map.data) return map.error ? null : <p className="muted">{t('m.ov.loading')}</p>
  const { services, cis } = map.data
  const bad = services.filter((s) => problem(s.health.level)).sort((a, b) => rank[b.health.level] - rank[a.health.level] || a.name.localeCompare(b.name))
  return (
    <Section icon={Waypoints} title={t('m.ov.services')} to="/cmdb" more={t('m.ov.services.all')}>
      {bad.length === 0 ? (
        <p className="muted m-ov-empty">{t('m.ov.services.none')}</p>
      ) : (
        <div className="m-list card">
          {bad.map((s) => {
            const sick = cis.filter((c) => s.ci_ids.includes(c.id) && problem(c.health.level)).length
            return (
              <Link key={s.id} to={`/cmdb?focus=${encodeURIComponent(s.id)}`} className="m-list-row">
                <span className={`m-health-dot health-${s.health.level}`} aria-hidden />
                <span className="m-list-text">
                  <strong>{s.name}</strong>
                  <small className="muted">{[s.owner, sick > 0 && t('m.ov.services.cis', { n: sick })].filter(Boolean).join(' · ')}</small>
                </span>
                <span className={`m-health health-${s.health.level}`}>{t(`m.health.${s.health.level}`)}</span>
              </Link>
            )
          })}
        </div>
      )}
    </Section>
  )
}

function MaintenanceBlock({ epoch }: { epoch: number }) {
  const t = useT(mobileStrings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const list = useResource<Window[]>('/api/maintenance', Math.floor(epoch / 2))
  if (!list.data) return list.error ? null : <p className="muted">{t('m.ov.loading')}</p>
  const shown = list.data
    .filter((w) => w.state !== 'finished')
    .sort((a, b) => (a.state === b.state ? a.start.localeCompare(b.start) : a.state === 'active' ? -1 : 1))
    .slice(0, WINDOWS)
  const when = (v: string) => formatDate(v, locale, timezone)
  return (
    <Section icon={CalendarClock} title={t('m.ov.maintenance')} to="/maintenance" more={t('m.ov.maintenance.all')}>
      {shown.length === 0 ? (
        <p className="muted m-ov-empty">{t('m.ov.maintenance.none')}</p>
      ) : (
        <div className="m-list card">
          {shown.map((w) => (
            <Link key={w.id} to="/maintenance" className="m-list-row">
              <span className={`m-health-dot ${w.state === 'active' ? 'health-warning' : 'health-unknown'}`} aria-hidden />
              <span className="m-list-text">
                <strong>{w.title}</strong>
                <small className="muted">{[...w.services, ...w.cis].map((x) => x.name).join(', ')}</small>
              </span>
              <small className="m-list-aside">{w.state === 'active' ? t('m.ov.mw.active', { end: when(w.end) }) : t('m.ov.mw.planned', { start: when(w.start) })}</small>
            </Link>
          ))}
        </div>
      )}
    </Section>
  )
}
