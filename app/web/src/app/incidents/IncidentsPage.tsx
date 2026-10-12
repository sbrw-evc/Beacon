import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ListChecks, Search, SlidersHorizontal } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { api } from '../../api'
import { ErrorBanner, ErrorFlash } from '../../connections/ConnectionCard'
import { useAction, useResource } from '../../connections/useRequest'
import { useLiveReload } from './live'
import { useT } from '../../i18n'
import { Banner, Button, Input, Select } from '../../ui'
import { useSession } from '../session'
import { BulkConfirm } from './CatalogForms'
import { OnboardingChecklist } from '../onboarding/OnboardingChecklist'
import { IncidentDetail, PDPill, SeverityPill, StatusPill } from './IncidentDetail'
import { ago } from './format'
import { strings } from './strings'
import { filtersFromURL, INCIDENTS_PATH, METHODS, NO_FILTERS, queryOf, SEVERITIES, SEVERITY_TONE, severityText, STATUSES, urlOf, type Filters, type Flag, type Incident, type Page, type Ref } from './types'
import '../services/services.css'
import '../connectors/connectors.css'
import '../cis/cis.css'
import './incidents.css'
import { useRouter } from '../../router'
import { IncidentCard } from './IncidentCard'
import { mobileStrings } from '../mobile/mobileStrings'
import { useMobile } from '../mobile/useMobile'
import { notify } from '../../notify'

// The stream brings changes at once; the list is also reloaded now and then, in case the stream
// is down or a change does not touch an incident (a maintenance window that starts).
const SAFETY_MS = 30_000
const OFFLINE_MS = 10_000
const CLOCK_MS = 10_000

function useDebounced<T>(value: T, ms: number) {
  const [v, setV] = useState(value)
  useEffect(() => {
    const id = window.setTimeout(() => setV(value), ms)
    return () => window.clearTimeout(id)
  }, [value, ms])
  return v
}

type TeamRef = { id: string; name: string }

export function IncidentsPage() {
  const t = useT(strings)
  const tm = useT(mobileStrings)
  const mobile = useMobile()
  const { can, user } = useSession()
  const actor = can('incidents:ack')
  const [showFilters, setShowFilters] = useState(false)
  const [selecting, setSelecting] = useState(false)
  const [acking, setAcking] = useState<string | null>(null)
  const scoped = user.role !== 'admin' && (user.scope_mode === 'teams' || user.scope_mode === 'services' || (!user.scope_mode && (user.service_ids?.length ?? 0) > 0))
  const [filters, setFilters] = useState<Filters>(() => filtersFromURL(window.location.search))
  const [openID, setOpenID] = useState<string | null>(() => new URLSearchParams(window.location.search).get('id'))
  const [epoch, setEpoch] = useState(0)
  const [now, setNow] = useState(() => Date.now())
  const [checked, setChecked] = useState<Set<string>>(new Set())
  const [confirming, setConfirming] = useState<'ack' | 'resolve' | null>(null)
  const q = useDebounced(filters.q, 250)
  const list = useResource<Page>(`/api/incidents${queryOf({ ...filters, q })}`, epoch)
  const refs = useResource<{ teams: TeamRef[] }>('/api/refs', 0)
  const bulk = useAction()
  const reload = useCallback(() => setEpoch((e) => e + 1), [])

  const live = useLiveReload(reload)
  useEffect(() => {
    const id = window.setInterval(() => {
      if (document.visibilityState === 'visible') reload()
    }, live ? SAFETY_MS : OFFLINE_MS)
    return () => window.clearInterval(id)
  }, [live, reload])
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), CLOCK_MS)
    return () => window.clearInterval(id)
  }, [])
  // A link to the list (a light of the top bar, a notification) while it is open.
  // While the page plays its exit animation the address already belongs to the next page:
  // the list then neither reads it nor writes its filters into it, or the address would
  // point back to /incidents and a click on Incidents in the menu would do nothing.
  const { path, search, visit } = useRouter()
  const here = path === INCIDENTS_PATH
  const arrived = useRef(visit)
  useEffect(() => {
    if (visit === arrived.current || !here) return
    arrived.current = visit
    setFilters(filtersFromURL(search))
    setOpenID(new URLSearchParams(search).get('id'))
  }, [search, visit, here])
  useEffect(() => {
    if (here) window.history.replaceState(null, '', urlOf(filters, openID))
  }, [filters, openID, here])
  useEffect(() => setChecked(new Set()), [filters])
  useEffect(() => {
    if (!selecting) setChecked(new Set())
  }, [selecting])

  const set = (patch: Partial<Filters>) => setFilters((f) => ({ ...f, ...patch }))
  const filtered = JSON.stringify(filters) !== JSON.stringify(NO_FILTERS)
  const services = useMemo(() => {
    const m = new Map<string, Ref>()
    for (const a of list.data?.alerts ?? []) for (const s of a.route.services) m.set(s.id, s)
    if (filters.service && !m.has(filters.service)) m.set(filters.service, { id: filters.service, name: filters.service })
    return [...m.values()].sort((a, b) => a.name.localeCompare(b.name))
  }, [list.data, filters.service])

  if (!list.data) {
    if (list.error) {
      return <ErrorBanner error={list.error} strings={strings} />
    }
    return <p className="muted">{t('loading')}</p>
  }
  const { alerts, counts, more } = list.data
  const pdOn = counts.pd_enabled !== false
  const actionable = alerts.filter((a) => a.status !== 'resolved')
  const allChecked = actionable.length > 0 && actionable.every((a) => checked.has(a.id))

  const runBulk = (action: 'ack' | 'resolve') =>
    void bulk.run(async () => {
      const out = await api<{ done: string[]; failed: Record<string, string> }>('POST', '/api/incidents/bulk', { ids: [...checked], action })
      const failed = Object.keys(out.failed).length
      notify({ kind: failed ? 'warn' : 'ok', title: t('inc.bulk.done', { done: out.done.length, failed }) })
      setChecked(new Set())
      setSelecting(false)
      reload()
    })

  // The quick acknowledgement of a card in the phone layout.
  const ack = (id: string) =>
    void bulk.run(async () => {
      setAcking(id)
      try {
        await api('POST', `/api/incidents/${encodeURIComponent(id)}/ack`)
        notify({ kind: 'ok', title: tm('m.acked.toast', { id }) })
        reload()
      } finally {
        setAcking(null)
      }
    })
  const active = [filters.severity, filters.method, filters.team, filters.service, filters.flag].filter(Boolean).length + (filters.status !== NO_FILTERS.status ? 1 : 0)

  return (
    <div className="ci-page inc-page">
      {scoped && <Banner kind="info" title={t('inc.scoped')} />}
      <Tiles counts={counts} filters={filters} set={set} />
      <div className="card svc-toolbar">
        <div className="svc-toolbar-row">
          <label className="svc-search">
            <Search size={16} aria-hidden />
            <Input type="search" value={filters.q} placeholder={t('inc.search')} aria-label={t('inc.search')} onChange={(e) => set({ q: e.target.value })} />
          </label>
          <span className={`inc-live${live ? ' on' : ''}`} role="status" title={live ? t('inc.live.hint') : t('inc.offline.hint')}>
            <span className="inc-live-dot" aria-hidden />
            {live ? t('inc.live') : t('inc.offline')}
          </span>
        </div>
        {mobile && (
          <div className="m-inc-tools">
            <Button variant={showFilters || active > 0 ? 'secondary' : 'ghost'} className={active > 0 ? 'm-on' : undefined} aria-expanded={showFilters} onClick={() => setShowFilters((v) => !v)}>
              <SlidersHorizontal size={16} aria-hidden />
              {active > 0 ? tm('m.filters.n', { n: active }) : tm('m.filters')}
            </Button>
            {actor && actionable.length > 0 && (
              <Button variant={selecting ? 'secondary' : 'ghost'} aria-pressed={selecting} onClick={() => setSelecting((v) => !v)}>
                <ListChecks size={16} aria-hidden />
                {selecting ? tm('m.select.done') : tm('m.select')}
              </Button>
            )}
            <span className="muted svc-count">{t('inc.count', { shown: alerts.length })}</span>
          </div>
        )}
        {(!mobile || showFilters) && (
        <div className="svc-filters">
          <label>
            <span>{t('inc.filter.status')}</span>
            <Select value={filters.status} onChange={(e) => set({ status: e.target.value })}>
              {STATUSES.map((s) => (
                <option key={s} value={s}>
                  {t(`inc.status.${s}`)}
                </option>
              ))}
            </Select>
          </label>
          <Choose label={t('inc.filter.severity')} value={filters.severity} onChange={(severity) => set({ severity })} options={SEVERITIES.map((s) => [s, severityText(t, s)])} />
          <Choose label={t('inc.filter.method')} value={filters.method} onChange={(method) => set({ method })} options={METHODS.map((m) => [m, t(`inc.method.${m}`)])} />
          <Choose label={t('inc.filter.team')} value={filters.team} onChange={(team) => set({ team })} options={(refs.data?.teams ?? []).map((x) => [x.id, x.name])} />
          <Choose label={t('inc.filter.service')} value={filters.service} onChange={(service) => set({ service })} options={services.map((x) => [x.id, x.name])} />
        </div>
        )}
        {mobile && showFilters && filtered && (
          <Button variant="ghost" onClick={() => setFilters(NO_FILTERS)}>
            {t('inc.filter.reset')}
          </Button>
        )}
        {!mobile && (
        <div className="svc-toolbar-row">
          <div className="row">
            {actor && checked.size > 0 && (
              <>
                <span className="muted">{t('inc.selected', { n: checked.size })}</span>
                <Button busy={bulk.busy} onClick={() => setConfirming('ack')}>
                  {t('inc.ack')}
                </Button>
                <Button busy={bulk.busy} onClick={() => setConfirming('resolve')}>
                  {t('inc.resolve')}
                </Button>
              </>
            )}
          </div>
          <div className="row">
            {filtered && (
              <Button variant="ghost" onClick={() => setFilters(NO_FILTERS)}>
                {t('inc.filter.reset')}
              </Button>
            )}
            <span className="muted svc-count">{t('inc.count', { shown: alerts.length })}</span>
          </div>
        </div>
        )}
      </div>
      {mobile && (
        <AnimatePresence>
          {checked.size > 0 && (
            <motion.div className="m-bulkbar" initial={{ y: 24, opacity: 0 }} animate={{ y: 0, opacity: 1 }} exit={{ y: 24, opacity: 0 }}>
              <span>{t('inc.selected', { n: checked.size })}</span>
              <Button busy={bulk.busy} onClick={() => setConfirming('ack')}>
                {t('inc.ack')}
              </Button>
              <Button variant="primary" busy={bulk.busy} onClick={() => setConfirming('resolve')}>
                {t('inc.resolve')}
              </Button>
            </motion.div>
          )}
        </AnimatePresence>
      )}
      <ErrorFlash error={bulk.error} strings={strings} />
      {list.error ? <ErrorBanner error={list.error} strings={strings} /> : null}
      {more && <Banner kind="info" title={t('inc.more', { n: alerts.length })} />}

      <AnimatePresence mode="wait" initial={false}>
        {alerts.length === 0 ? (
          <motion.div key="empty" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}>
            {filtered ? (
              <div className="card svc-empty">
                <p>{t('inc.empty')}</p>
              </div>
            ) : (
              // Nothing at all: until the installation can deliver an incident, show what is left to do.
              <OnboardingChecklist
                epoch={epoch}
                incidentsNote
                fallback={
                  <div className="card svc-empty">
                    <p>{t('inc.empty.active')}</p>
                  </div>
                }
              />
            )}
          </motion.div>
        ) : mobile ? (
          <motion.div key="cards" className="m-inc-list" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}>
            {alerts.map((a) => (
              <IncidentCard
                key={a.id}
                a={a}
                now={now}
                onOpen={() => setOpenID(a.id)}
                onAck={actor ? () => ack(a.id) : undefined}
                busy={acking === a.id}
                selecting={selecting}
                checked={checked.has(a.id)}
                onCheck={(v) =>
                  setChecked((s) => {
                    const n = new Set(s)
                    if (v) n.add(a.id)
                    else n.delete(a.id)
                    return n
                  })
                }
              />
            ))}
          </motion.div>
        ) : (
          <motion.div key="list" className="card cn-table-wrap" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}>
            <table className="cn-table inc-table">
              <thead>
                <tr>
                  {actor && (
                    <th className="inc-check">
                      <input
                        type="checkbox"
                        aria-label={t('inc.select.all')}
                        checked={allChecked}
                        onChange={() => setChecked(allChecked ? new Set() : new Set(actionable.map((a) => a.id)))}
                      />
                    </th>
                  )}
                  <th>{t('inc.col.severity')}</th>
                  <th>{t('inc.col.incident')}</th>
                  <th>{t('inc.col.owner')}</th>
                  <th>{t('inc.col.status')}</th>
                  {pdOn && <th>{t('inc.col.pd')}</th>}
                  <th>{t('inc.col.seen')}</th>
                  <th className="num">{t('inc.col.count')}</th>
                </tr>
              </thead>
              <tbody>
                {alerts.map((a) => (
                  <Row
                    key={a.id}
                    a={a}
                    now={now}
                    actor={actor}
                    pd={pdOn}
                    checked={checked.has(a.id)}
                    onCheck={(v) =>
                      setChecked((s) => {
                        const n = new Set(s)
                        if (v) n.add(a.id)
                        else n.delete(a.id)
                        return n
                      })
                    }
                    onOpen={() => setOpenID(a.id)}
                  />
                ))}
              </tbody>
            </table>
          </motion.div>
        )}
      </AnimatePresence>

      <BulkConfirm
        action={confirming}
        selected={alerts.filter((a) => checked.has(a.id))}
        busy={bulk.busy}
        onCancel={() => setConfirming(null)}
        onConfirm={() => {
          if (!confirming) return
          setConfirming(null)
          runBulk(confirming)
        }}
      />
      <IncidentDetail id={openID} actor={actor} onClose={() => setOpenID(null)} onChanged={reload} onOpen={setOpenID} />
    </div>
  )
}

function Choose({ label, value, options, onChange }: { label: string; value: string; options: [string, string][]; onChange: (v: string) => void }) {
  const t = useT(strings)
  return (
    <label>
      <span>{label}</span>
      <Select value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">{t('inc.filter.any')}</option>
        {options.map(([v, l]) => (
          <option key={v} value={v}>
            {l}
          </option>
        ))}
      </Select>
    </label>
  )
}

function Tiles({ counts, filters, set }: { counts: Page['counts']; filters: Filters; set: (p: Partial<Filters>) => void }) {
  const t = useT(strings)
  const flag = (f: Flag) => set({ flag: filters.flag === f ? '' : f, status: f ? 'active' : filters.status })
  return (
    <div className="ci-tiles inc-tiles">
      <button type="button" className={`card ci-tile ${filters.status === 'active' && !filters.severity && !filters.flag ? 'active' : ''}`} onClick={() => set({ status: 'active', severity: '', flag: '' })}>
        <span className="ci-tile-value">{counts.active}</span>
        <span className="muted">{t('inc.tile.active')}</span>
        {counts.acknowledged > 0 && <span className="muted inc-tile-note">{t('inc.tile.acknowledged', { n: counts.acknowledged })}</span>}
      </button>
      {SEVERITIES.map((s) => (
        <button
          key={s}
          type="button"
          className={`card ci-tile inc-tile-sev inc-sev-${SEVERITY_TONE[s]} ${filters.severity === s ? 'active' : ''}`}
          aria-pressed={filters.severity === s}
          onClick={() => set({ severity: filters.severity === s ? '' : s, status: 'active' })}
        >
          <span className="ci-tile-value">{counts.by_severity[s] ?? 0}</span>
          <span className="muted">{severityText(t, s)}</span>
        </button>
      ))}
      {/* Without PagerDuty nothing is "not taken by PagerDuty": the tile is hidden. */}
      {counts.pd_enabled !== false && (
        <button type="button" className={`card ci-tile ci-tile-flag ${counts.pd_not_taken > 0 ? 'warn' : ''} ${filters.flag === 'pd' ? 'active' : ''}`} onClick={() => flag('pd')}>
          <span className="ci-tile-value">{counts.pd_not_taken}</span>
          <span className="muted">{t('inc.tile.pd')}</span>
        </button>
      )}
      <button type="button" className={`card ci-tile ci-tile-flag ${counts.fallback > 0 ? 'warn' : ''} ${filters.flag === 'fallback' ? 'active' : ''}`} onClick={() => flag('fallback')}>
        <span className="ci-tile-value">{counts.fallback}</span>
        <span className="muted">{t('inc.tile.fallback')}</span>
      </button>
      <button type="button" className={`card ci-tile ${filters.flag === 'suppressed' ? 'active' : ''}`} onClick={() => flag('suppressed')}>
        <span className="ci-tile-value">{counts.suppressed}</span>
        <span className="muted">{t('inc.tile.suppressed')}</span>
        {counts.unbound > 0 && <span className="muted inc-tile-note">{t('inc.tile.unbound', { n: counts.unbound })}</span>}
      </button>
    </div>
  )
}

function Row({
  a,
  now,
  actor,
  pd,
  checked,
  onCheck,
  onOpen,
}: {
  a: Incident
  now: number
  actor: boolean
  pd: boolean
  checked: boolean
  onCheck: (v: boolean) => void
  onOpen: () => void
}) {
  const t = useT(strings)
  const owner = [a.route.services[0]?.name, a.route.team?.name].filter(Boolean).join(' · ')
  return (
    <tr className={a.status === 'resolved' ? 'inc-resolved' : undefined}>
      {actor && (
        <td className="inc-check">
          {a.status !== 'resolved' && <input type="checkbox" aria-label={t('inc.select.one', { id: a.id })} checked={checked} onChange={(e) => onCheck(e.target.checked)} />}
        </td>
      )}
      <td>
        <SeverityPill severity={a.severity} />
      </td>
      <td className="inc-title">
        <button type="button" className="cn-link cn-name" onClick={onOpen}>
          {a.title}
        </button>
        <div className="muted inc-sub">
          <code>{a.id}</code> · {a.ci_name}
          {!a.ci_id && <span className="inc-warn"> ({t('inc.noci')})</span>}
          {a.signal && a.signal !== a.title && <> · {a.signal}</>}
        </div>
      </td>
      <td>{owner || <span className="muted">{t('inc.noroute')}</span>}</td>
      <td>
        <StatusPill status={a.status} />
        {a.suppressed && <span className="pill pill-off inc-badge">{t(a.excluded ? 'inc.badge.excluded' : 'inc.badge.suppressed')}</span>}
        {a.labels?.umbrella_test === 'true' && <span className="pill pill-off inc-badge">{t('inc.badge.test')}</span>}
        {a.fallback && a.status !== 'resolved' && <span className="pill pill-warn inc-badge">{t('inc.badge.fallback')}</span>}
      </td>
      {pd && (
        <td>
          <PDPill state={a.pd.state} />
        </td>
      )}
      <td title={a.last_seen}>{ago(t, a.last_seen, now)}</td>
      <td className="num">{a.count}</td>
    </tr>
  )
}
