import { useCallback, useEffect, useState } from 'react'
import { ExternalLink, Plus, RefreshCw, Search, Settings, PlugZap } from 'lucide-react'
import { api } from '../../api'
import { ErrorBanner, ErrorFlash } from '../../connections/ConnectionCard'
import { mergeDicts } from '../../connections/connectionStrings'
import { useAction, useResource } from '../../connections/useRequest'
import { useLocale, useT } from '../../i18n'
import { Banner, Button, Field, formatDate, Input, Modal, Segmented, Select, Stepper, Switch } from '../../ui'
import { strings as ciStrings } from '../cis/strings'
import type { CI, CIList } from '../cis/types'
import { CreateHostsDialog, type HostKey } from '../bulk/BulkDialogs'
import { strings as bulkStrings } from '../bulk/strings'
import { useSession } from '../session'
import { CopyField, QuickConnectDialog, SourcesExplainer, TestEvent, useCanQuickConnect } from '../connectors/QuickConnect'
import { sourcesStrings } from '../connectors/sourcesStrings'
import { strings as connectorStrings } from '../connectors/strings'
import { Link } from '../../router'
import { strings } from './strings'
import { ContextTab } from './ContextTab'
import { contextStrings } from './contextStrings'
import '../services/services.css'
import '../connectors/connectors.css'
import '../cis/cis.css'
import '../rules/rules.css'
import './monitoring.css'
import '../bulk/bulk.css'
import { Flash, notify } from '../../notify'
import { ask } from '../../confirm'
import { SetupGuide } from '../guide/SetupGuide'

type Kind = 'zabbix' | 'prometheus' | 'grafana' | 'graylog'
type Poll = { at: string; ok: boolean; error?: string; firing: number; sent: number }
type Sync = { started_at: string; finished_at: string; ok: boolean; error?: string; actor: string; hosts: number; version?: string }
type Source = {
  id: string
  name: string
  kind: Kind
  url: string
  credential_id?: string
  credential_name?: string
  skip_verify: boolean
  enabled: boolean
  sync_minutes: number
  query?: string
  host_label?: string
  poll_alerts?: boolean
  poll_seconds?: number
  quiet_minutes?: number
  poll?: Poll
  sync: Sync
  hosts: number
  matched: number
  unmatched: number
  running: boolean
  next_sync_at?: string
  connector_id?: string
  connector?: { id: string; name: string; slug: string; status: string; ingest_path: string; last_received?: string; received: number }
  rules: number
}
type View = {
  sources: Source[]
  defaults: { query: string; host_label: string; grafana_host_labels?: string[]; poll_seconds?: number; graylog_host_field?: string; graylog_quiet_minutes?: number }
}
type Ref = { id: string; name: string }
type Host = {
  key: string
  host: string
  name: string
  // Lists may come as null from an older server (hosts read back from its snapshot).
  ips: string[] | null
  dns: string[] | null
  groups: string[] | null
  endpoints?: string[] | null
  state: string
  url?: string
  source_id: string
  source_name: string
  kind: Kind
  ci?: Ref
  match: string
  candidates: Ref[] | null
  also_in: { source_id: string; source_name: string; key: string; host: string }[] | null
}
type Summary = { total: number; matched: number; unmatched: number; ambiguous: number; excluded: number }
type HostList = { items: Host[]; summary: Summary; limited: boolean }
type Credential = { id: string; name: string; type: string }
type Report = { ok: boolean; error?: string; version?: string; hosts: number; sample: Host[] | null }

const MATCHES = ['matched', 'unmatched', 'ambiguous', 'excluded'] as const
const ran = (at?: string) => !!at && !at.startsWith('0001-')
const createStrings = mergeDicts(ciStrings, strings)
const hostStrings = mergeDicts(strings, bulkStrings)
const systemStrings = mergeDicts(connectorStrings, strings, sourcesStrings)
const hostID = (h: { source_id: string; key: string }) => h.source_id + '/' + h.key
// creatable: a host an item can be made of.
const creatable = (h: Host) => !h.ci && h.match !== 'excluded'

function useDebounced<T>(value: T, ms: number) {
  const [v, setV] = useState(value)
  useEffect(() => {
    const id = window.setTimeout(() => setV(value), ms)
    return () => window.clearTimeout(id)
  }, [value, ms])
  return v
}

const query = () => new URLSearchParams(window.location.search)

export function MonitoringPage() {
  const t = useT(strings)
  const ts = useT(sourcesStrings)
  const tc = useT(contextStrings)
  const { can } = useSession()
  const quick = useCanQuickConnect()
  const [tab, setTab] = useState<'hosts' | 'sources' | 'context'>(() =>
    query().get('tab') === 'context' ? 'context' : query().get('system') || query().get('connect') ? 'sources' : 'hosts',
  )
  const [epoch, setEpoch] = useState(0)
  const view = useResource<View>('/api/monitoring', epoch)
  const [editing, setEditing] = useState<Source | 'new' | null>(null)
  const [connecting, setConnecting] = useState(() => query().get('connect') === '1')
  const [hostSource, setHostSource] = useState('')
  const [hostEpoch, setHostEpoch] = useState(0)
  const reload = useCallback(() => setEpoch((e) => e + 1), [])
  const v = view.data
  useEffect(() => {
    if (v && v.sources.length === 0) setTab((cur) => (cur === 'context' ? cur : 'sources'))
  }, [v])
  const showHosts = (id: string) => {
    setHostSource(id)
    setHostEpoch((n) => n + 1)
    setTab('hosts')
  }
  return (
    <div className="stack">
      <SourcesExplainer />
      <SetupGuide page="monitoring" actions={{ create: () => setEditing('new') }} />
      <div className="row rl-head">
        <Segmented
          label={t('mon.tab.hosts')}
          value={tab}
          onChange={setTab}
          options={[
            { value: 'hosts', label: t('mon.tab.hosts') },
            { value: 'sources', label: `${t('mon.tab.sources')} (${v?.sources.length ?? 0})` },
            { value: 'context', label: tc('mon.tab.context') },
          ]}
        />
        {tab === 'sources' && (
          <div className="row">
            {quick && (
              <Button onClick={() => setConnecting(true)}>
                <PlugZap size={16} aria-hidden />
                {ts('src.connect')}
              </Button>
            )}
            {can('monitoring:edit') && (
              <Button variant="primary" onClick={() => setEditing('new')}>
                <Plus size={16} aria-hidden />
                {t('mon.new')}
              </Button>
            )}
          </div>
        )}
      </div>
      <ErrorBanner error={view.error} strings={strings} />
      {v && tab === 'sources' && <SourcesTable v={v} onOpen={setEditing} onChanged={reload} onHosts={showHosts} />}
      {tab === 'context' && <ContextTab />}
      {v && tab === 'hosts' && <HostsTab key={hostEpoch} initialSource={hostSource} sources={v.sources} epoch={epoch} onChanged={reload} />}
      <QuickConnectDialog open={connecting} onClose={() => setConnecting(false)} onDone={reload} />
      {v && <SourceEditor value={editing} defaults={v.defaults} onClose={() => setEditing(null)} onSaved={() => (setEditing(null), reload())} />}
    </div>
  )
}

function When({ at }: { at?: string }) {
  const { locale } = useLocale()
  const { timezone } = useSession()
  return <>{ran(at) ? formatDate(at, locale, timezone) : '—'}</>
}

function SourcesTable({ v, onOpen, onChanged, onHosts }: { v: View; onOpen: (s: Source) => void; onChanged: () => void; onHosts: (id: string) => void }) {
  const t = useT(strings)
  const syncer = useAction()
  const [busy, setBusy] = useState('')
  const focus = query().get('system') ?? ''
  useEffect(() => {
    if (focus) document.getElementById(`system-${focus}`)?.scrollIntoView({ block: 'start' })
  }, [focus])
  const read = (s: Source) => {
    setBusy(s.id)
    void syncer
      .run(async () => {
        const st = await api<Sync>('POST', `/api/monitoring/sources/${s.id}/sync`)
        if (st.ok) notify({ kind: 'ok', title: t('mon.sync.done', { n: st.hosts, name: s.name }) })
      })
      .finally(() => {
        setBusy('')
        onChanged()
      })
  }
  if (v.sources.length === 0) return <p className="muted card rl-empty">{t('mon.empty.sources')}</p>
  return (
    <>
      <ErrorFlash error={syncer.error} strings={strings} />
      {v.sources.map((s) => (
        <SystemCard key={s.id} s={s} reading={busy === s.id || s.running} onRead={() => read(s)} onOpen={() => onOpen(s)} onChanged={onChanged} onHosts={() => onHosts(s.id)} />
      ))}
    </>
  )
}

type SystemTab = 'alerts' | 'hosts' | 'metrics'

// SystemCard is one monitoring system as one object: its alert intake, its hosts and, for
// Prometheus, its use as a metric source of RED/USE rules.
function SystemCard({
  s,
  reading,
  onRead,
  onOpen,
  onChanged,
  onHosts,
}: {
  s: Source
  reading: boolean
  onRead: () => void
  onOpen: () => void
  onChanged: () => void
  onHosts: () => void
}) {
  const t = useT(systemStrings)
  const { can } = useSession()
  const [tab, setTab] = useState<SystemTab>(s.connector ? 'hosts' : 'alerts')
  const tabs: { value: SystemTab; label: string }[] = [
    { value: 'alerts', label: t('sys.tab.alerts') },
    { value: 'hosts', label: `${t('sys.tab.hosts')} (${s.hosts})` },
  ]
  if (s.kind === 'prometheus') tabs.push({ value: 'metrics', label: `${t('sys.tab.metrics')} (${s.rules})` })
  return (
    <section className="card mon-system" id={`system-${s.id}`}>
      <div className="mon-system-head">
        <div>
          <h2>{s.name}</h2>
          <div className="rl-sub">
            <span className={`pill mon-kind mon-kind-${s.kind}`}>{t(`mon.kind.${s.kind}`)}</span>
            {!s.enabled && <span className="pill pill-off">{t('mon.off')}</span>}
            {s.sync.version && <span className="muted"> {s.sync.version}</span>} <code className="muted">{s.url}</code>
            {s.credential_name && <span className="muted"> · {s.credential_name}</span>}
          </div>
        </div>
        <div className="row">
          {can('monitoring:sync') && (
            <Button busy={reading} onClick={onRead}>
              <RefreshCw size={15} aria-hidden />
              {reading ? t('mon.sync.running') : t('mon.sync.now')}
            </Button>
          )}
          {can('monitoring:edit') && (
            <Button variant="ghost" onClick={onOpen}>
              <Settings size={15} aria-hidden />
              {t('sys.settings')}
            </Button>
          )}
        </div>
      </div>
      <Segmented label={s.name} value={tab} onChange={setTab} options={tabs} />
      <div className="mon-system-body">
        {tab === 'alerts' && <SystemAlerts s={s} onChanged={onChanged} />}
        {tab === 'hosts' && (
          <>
            <p>{t('sys.hosts.summary', { hosts: s.hosts, matched: s.matched, unmatched: s.unmatched })}</p>
            <dl className="mon-kv">
              <dt>{t('mon.col.sync')}</dt>
              <dd>
                {ran(s.sync.started_at) ? <When at={s.sync.finished_at || s.sync.started_at} /> : t('mon.sync.never')}
                {' · '}
                {s.sync_minutes > 0 ? t('mon.sync.every', { n: s.sync_minutes }) : t('mon.sync.manual')}
                {ran(s.sync.started_at) && !s.sync.ok && (
                  <div className="mon-error" title={s.sync.error}>
                    <span className="pill pill-error">{t('mon.sync.failed')}</span> {s.sync.error}
                  </div>
                )}
              </dd>
            </dl>
            <p className="muted">{t('sys.hosts.links')}</p>
            <div className="row">
              <Button onClick={onHosts}>{t('sys.hosts.open')}</Button>
            </div>
          </>
        )}
        {tab === 'metrics' && (
          <>
            <p>{t('sys.metrics.text')}</p>
            <p className="muted">{t('sys.metrics.rules', { n: s.rules })}</p>
            <div className="row">
              <Link to="/rules">{t('sys.metrics.open')}</Link>
            </div>
          </>
        )}
      </div>
    </section>
  )
}

type ConnectorChoice = { id: string; name: string; preset?: string }

function SystemAlerts({ s, onChanged }: { s: Source; onChanged: () => void }) {
  const t = useT(systemStrings)
  const { locale } = useLocale()
  const { can, timezone } = useSession()
  const quick = useCanQuickConnect()
  const editable = can('monitoring:edit')
  const [connecting, setConnecting] = useState(false)
  const list = useResource<{ connectors: ConnectorChoice[] }>(!s.connector && editable && can('connectors:view') ? '/api/connectors' : '', 0)
  const action = useAction()
  const link = (id: string) =>
    void action.run(async () => {
      await api('PUT', `/api/monitoring/sources/${s.id}/connector`, { connector_id: id })
      onChanged()
    })
  const c = s.connector
  if (!c) {
    return (
      <>
        <p>{t(`sys.alerts.none.${s.kind}`)}</p>
        <div className="row">
          {quick && editable && (
            <Button variant="primary" onClick={() => setConnecting(true)}>
              <PlugZap size={15} aria-hidden />
              {t('sys.alerts.connect')}
            </Button>
          )}
          {editable && (list.data?.connectors.length ?? 0) > 0 && (
            <label className="mon-source-filter">
              <span className="muted">{t('sys.alerts.pick')}</span>
              <Select value="" onChange={(e) => e.target.value && link(e.target.value)}>
                <option value="">{t('sys.alerts.pick.none')}</option>
                {list.data?.connectors.map((x) => (
                  <option key={x.id} value={x.id}>
                    {x.name}
                  </option>
                ))}
              </Select>
            </label>
          )}
        </div>
        <p className="muted">{t('sys.alerts.tokens')}</p>
        <ErrorFlash error={action.error} strings={strings} />
        <QuickConnectDialog key={s.id} open={connecting} onClose={() => setConnecting(false)} onDone={onChanged} monitoring={{ id: s.id, name: s.name, kind: s.kind }} />
      </>
    )
  }
  return (
    <>
      <dl className="mon-kv">
        <dt>{t('sys.alerts.connector')}</dt>
        <dd>
          <Link to={`/connectors/${encodeURIComponent(c.id)}`}>{c.name}</Link> <span className="muted">({t(`cn.status.${c.status}`)})</span>
        </dd>
        <dt>{t('sys.alerts.last')}</dt>
        <dd>
          {c.last_received ? formatDate(c.last_received, locale, timezone) : t('sys.alerts.never')}
          {c.received > 0 && <span className="muted"> · {t('sys.alerts.received', { n: c.received })}</span>}
        </dd>
      </dl>
      <CopyField label={t('sys.alerts.url')} value={`${window.location.origin}${c.ingest_path}`} />
      {(s.kind === 'grafana' || s.kind === 'graylog') && <PollBlock s={s} draft={c.status === 'draft'} onChanged={onChanged} />}
      <p className="muted">{t('sys.alerts.tokens')}</p>
      {c.status !== 'draft' && (can('connectors:edit') || editable) && <TestEvent connectorID={c.id} />}
      {editable && (
        <div className="row">
          <Button variant="ghost" busy={action.busy} onClick={() => link('')}>
            {t('sys.alerts.unlink')}
          </Button>
        </div>
      )}
      <ErrorFlash error={action.error} strings={strings} />
    </>
  )
}

// PollBlock is the state of polling the Grafana Alerting API or the Graylog events API, with a
// button to poll at once.
function PollBlock({ s, draft, onChanged }: { s: Source; draft: boolean; onChanged: () => void }) {
  const t = useT(systemStrings)
  const { can } = useSession()
  const action = useAction()
  const p = s.poll
  const poll = () =>
    void action.run(async () => {
      await api('POST', `/api/monitoring/sources/${s.id}/poll`)
      onChanged()
    })
  return (
    <div className="mon-poll">
      <h3>{t(s.kind === 'graylog' ? 'sys.poll.title.graylog' : 'sys.poll.title')}</h3>
      {!s.poll_alerts && !p ? (
        <p className="muted">{t(s.kind === 'graylog' ? 'sys.poll.off.graylog' : 'sys.poll.off')}</p>
      ) : (
        <dl className="mon-kv">
          <dt>{t('sys.poll.last')}</dt>
          <dd>
            {p ? <When at={p.at} /> : t('sys.poll.never')}
            {s.poll_alerts && <span className="muted"> · {t('sys.poll.every', { n: s.poll_seconds ?? 60 })}</span>}
            {p?.ok && <div className="muted">{t('sys.poll.result', { firing: p.firing, sent: p.sent })}</div>}
            {p && !p.ok && (
              <div className="mon-error" title={p.error}>
                <span className="pill pill-error">{t('sys.poll.failed')}</span> {p.error}
              </div>
            )}
          </dd>
        </dl>
      )}
      {draft && <Banner kind="warn" title={t('sys.poll.unpublished')} />}
      {can('monitoring:sync') && !draft && (
        <div className="row">
          <Button busy={action.busy} onClick={poll}>
            <RefreshCw size={15} aria-hidden />
            {t('sys.poll.now')}
          </Button>
        </div>
      )}
      <ErrorBanner error={action.error} strings={strings} />
    </div>
  )
}

function StatePill({ state }: { state: string }) {
  const t = useT(strings)
  const tone = state === 'up' ? 'ok' : state === 'down' ? 'error' : state === 'partial' ? 'warn' : 'off'
  return <span className={`pill pill-${tone}`}>{t(`mon.state.${state}`)}</span>
}

function HostsTab({ sources, epoch, onChanged, initialSource = '' }: { sources: Source[]; epoch: number; onChanged: () => void; initialSource?: string }) {
  const t = useT(hostStrings)
  const { can } = useSession()
  const [match, setMatch] = useState('')
  const [source, setSource] = useState(initialSource)
  const [q, setQ] = useState('')
  const dq = useDebounced(q, 250)
  const [own, setOwn] = useState(0)
  const params = new URLSearchParams()
  if (match) params.set('match', match)
  if (source) params.set('source', source)
  if (dq.trim()) params.set('q', dq.trim())
  const list = useResource<HostList>(`/api/monitoring/hosts?${params}`, epoch + own)
  const [linking, setLinking] = useState<Host | null>(null)
  const [creating, setCreating] = useState<Host | null>(null)
  const [checked, setChecked] = useState<Map<string, HostKey>>(new Map())
  const [bulkHosts, setBulkHosts] = useState<HostKey[] | null>(null)
  const changed = () => {
    setOwn((n) => n + 1)
    onChanged()
  }
  const canLink = can('monitoring:link')
  const canCreate = canLink && can('cis:edit')
  if (!list.data) return list.error ? <ErrorBanner error={list.error} strings={strings} /> : <p className="muted">{t('loading')}</p>
  const { items, summary } = list.data
  const filtered = match !== '' || source !== '' || q.trim() !== ''
  const eligible = canCreate ? items.filter(creatable) : []
  const allChecked = eligible.length > 0 && eligible.every((h) => checked.has(hostID(h)))
  const toggleAll = () =>
    setChecked((c) => {
      const n = new Map(c)
      for (const h of eligible) {
        if (allChecked) n.delete(hostID(h))
        else n.set(hostID(h), { source_id: h.source_id, key: h.key })
      }
      return n
    })
  const toggle = (h: Host) =>
    setChecked((c) => {
      const n = new Map(c)
      if (n.has(hostID(h))) n.delete(hostID(h))
      else n.set(hostID(h), { source_id: h.source_id, key: h.key })
      return n
    })
  return (
    <div className="ci-page">
      <div className="ci-tiles">
        <button type="button" className={`card ci-tile ci-tile-flag ${match === '' ? 'active' : ''}`} aria-pressed={match === ''} onClick={() => setMatch('')}>
          <span className="ci-tile-value">{summary.total}</span>
          <span className="muted">{t('mon.tile.total')}</span>
        </button>
        {MATCHES.map((m) => (
          <button
            key={m}
            type="button"
            className={`card ci-tile ci-tile-flag ${match === m ? 'active' : ''} ${m !== 'matched' && m !== 'excluded' && summary[m] > 0 ? 'warn' : ''}`}
            aria-pressed={match === m}
            onClick={() => setMatch(match === m ? '' : m)}
          >
            <span className="ci-tile-value">{summary[m]}</span>
            <span className="muted">{t(`mon.tile.${m}`)}</span>
          </button>
        ))}
      </div>
      <div className="card svc-toolbar">
        <div className="svc-toolbar-row">
          <label className="svc-search">
            <Search size={16} aria-hidden />
            <Input type="search" value={q} placeholder={t('mon.search')} aria-label={t('mon.search')} onChange={(e) => setQ(e.target.value)} />
          </label>
          {sources.length > 1 && (
            <label className="mon-source-filter">
              <span className="muted">{t('mon.filter.source')}</span>
              <Select value={source} onChange={(e) => setSource(e.target.value)}>
                <option value="">{t('mon.filter.any')}</option>
                {sources.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </Select>
            </label>
          )}
          <span className="muted svc-count">{t('mon.count', { shown: items.length, total: summary.total })}</span>
        </div>
        {canCreate && checked.size > 0 && (
          <div className="bulk-bar">
            <span className="muted">{t('bulk.selected', { n: checked.size })}</span>
            <Button variant="primary" onClick={() => setBulkHosts([...checked.values()])}>
              <Plus size={16} />
              {t('bulk.hosts.create')}
            </Button>
            <Button variant="ghost" onClick={() => setChecked(new Map())}>
              {t('bulk.clear')}
            </Button>
          </div>
        )}
      </div>
      {list.data.limited && <Banner kind="info" title={t('mon.limited', { n: items.length })} />}
      {items.length === 0 ? (
        <div className="card svc-empty">
          <p>{t(filtered || summary.total > 0 ? 'mon.empty.filtered' : 'mon.empty.hosts')}</p>
        </div>
      ) : (
        <div className="card cn-table-wrap">
          <table className="cn-table mon-table">
            <thead>
              <tr>
                {eligible.length > 0 && (
                  <th className="bulk-check">
                    <input type="checkbox" aria-label={t('bulk.selectAll')} checked={allChecked} onChange={toggleAll} />
                  </th>
                )}
                <th>{t('mon.h.host')}</th>
                <th>{t('mon.h.system')}</th>
                <th>{t('mon.h.addresses')}</th>
                <th>{t('mon.h.groups')}</th>
                <th>{t('mon.h.state')}</th>
                <th>{t('mon.h.ci')}</th>
              </tr>
            </thead>
            <tbody>
              {items.map((h) => (
                <tr key={hostID(h)}>
                  {eligible.length > 0 && (
                    <td className="bulk-check">
                      {creatable(h) && (
                        <input type="checkbox" aria-label={t('bulk.selectOne', { name: h.name || h.host })} checked={checked.has(hostID(h))} onChange={() => toggle(h)} />
                      )}
                    </td>
                  )}
                  <td>
                    <div className="cn-name">
                      {h.url ? (
                        <a href={h.url} target="_blank" rel="noopener noreferrer" title={t('mon.open')}>
                          {h.name || h.host}
                          <ExternalLink size={12} aria-hidden />
                        </a>
                      ) : (
                        h.name || h.host
                      )}
                    </div>
                    {h.name && h.host && h.name !== h.host && <div className="muted cn-mono">{h.host}</div>}
                  </td>
                  <td>{h.source_name}</td>
                  <td className="cn-mono">{[...(h.ips ?? []), ...(h.dns ?? [])].join(', ') || <span className="muted">—</span>}</td>
                  <td>{h.groups?.length ? h.groups.join(', ') : <span className="muted">—</span>}</td>
                  <td>
                    <StatePill state={h.state} />
                  </td>
                  <td className="mon-ci">
                    <HostCI h={h} />
                    {(canLink || canCreate) && (
                      <div className="row mon-actions">
                        {canLink && (
                          <Button variant="ghost" onClick={() => setLinking(h)}>
                            {t(h.ci || h.match === 'excluded' ? 'mon.act.change' : 'mon.act.link')}
                          </Button>
                        )}
                        {canCreate && !h.ci && (
                          <Button variant="ghost" onClick={() => setCreating(h)}>
                            {t('mon.act.create')}
                          </Button>
                        )}
                      </div>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <LinkDialog host={linking} onClose={() => setLinking(null)} onSaved={() => (setLinking(null), changed())} />
      <CreateDialog host={creating} onClose={() => setCreating(null)} onSaved={() => (setCreating(null), changed())} />
      <CreateHostsDialog
        hosts={bulkHosts}
        onClose={() => setBulkHosts(null)}
        onDone={() => {
          setChecked(new Map())
          changed()
        }}
      />
    </div>
  )
}

function HostCI({ h }: { h: Host }) {
  const t = useT(strings)
  if (h.ci)
    return (
      <div>
        <span className="cn-name">{h.ci.name}</span> <span className="muted">· {t(`mon.match.${h.match}`)}</span>
      </div>
    )
  if (h.match === 'excluded') return <span className="pill pill-off">{t('mon.match.excluded')}</span>
  if (h.match === 'ambiguous')
    return (
      <div>
        <span className="pill pill-warn">{t('mon.match.ambiguous')}</span>
        <div className="muted rl-sub">{(h.candidates ?? []).map((c) => c.name).join(', ')}</div>
      </div>
    )
  return (
    <div>
      <span className="pill pill-warn">{t('mon.match.none')}</span>
      {(h.also_in?.length ?? 0) > 0 && <div className="muted rl-sub">{t('mon.also', { list: (h.also_in ?? []).map((o) => `${o.source_name}: ${o.host}`).join(', ') })}</div>}
    </div>
  )
}

function LinkDialog({ host, onClose, onSaved }: { host: Host | null; onClose: () => void; onSaved: () => void }) {
  const t = useT(strings)
  const [q, setQ] = useState('')
  const [pick, setPick] = useState('')
  const saver = useAction()
  const dq = useDebounced(q, 250)
  const found = useResource<CIList>(host && dq.trim() ? `/api/cis?q=${encodeURIComponent(dq.trim())}` : '', 0)
  useEffect(() => {
    setQ(host ? host.host || host.name : '')
    setPick(host?.ci?.id ?? '')
    saver.clear()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [host])
  const send = (mode: 'ci' | 'none' | 'auto') =>
    host &&
    void saver.run(async () => {
      await api('POST', '/api/monitoring/link', { source_id: host.source_id, key: host.key, mode, ci_id: mode === 'ci' ? pick : '' })
      onSaved()
    })
  const results: Ref[] = (found.data?.items ?? []).slice(0, 20).map((c: CI) => ({ id: c.id, name: c.name }))
  const option = (r: Ref, extra?: string) => (
    <label key={r.id} className={`mon-option ${pick === r.id ? 'active' : ''}`}>
      <input type="radio" name="mon-ci" checked={pick === r.id} onChange={() => setPick(r.id)} />
      <span className="cn-name">{r.name}</span>
      <span className="muted cn-mono">{r.id}</span>
      {extra && <span className="muted">{extra}</span>}
    </label>
  )
  return (
    <Modal
      open={host !== null}
      title={host ? t('mon.link.title', { host: host.name || host.host }) : ''}
      onClose={onClose}
      footer={
        <>
          {host && (host.match === 'manual' || host.match === 'excluded') && (
            <Button variant="ghost" busy={saver.busy} onClick={() => send('auto')}>
              {t('mon.link.auto')}
            </Button>
          )}
          {host && host.match !== 'excluded' && (
            <Button busy={saver.busy} onClick={() => send('none')}>
              {t('mon.link.none')}
            </Button>
          )}
          <Button onClick={onClose}>{t('mon.cancel')}</Button>
          <Button variant="primary" busy={saver.busy} disabled={!pick || pick === host?.ci?.id} onClick={() => send('ci')}>
            {t('mon.link.save')}
          </Button>
        </>
      }
    >
      {host && (
        <div className="stack">
          <p className="muted">{t('mon.link.text')}</p>
          <p className="hint">{t('mon.link.none.hint')}</p>
          {(host.candidates?.length ?? 0) > 0 && (
            <div className="mon-options">
              <span className="nb-group-title">{t('mon.link.candidates')}</span>
              {(host.candidates ?? []).map((c) => option(c))}
            </div>
          )}
          <label className="svc-search mon-link-search">
            <Search size={16} aria-hidden />
            <Input type="search" value={q} placeholder={t('mon.link.search')} aria-label={t('mon.link.search')} onChange={(e) => setQ(e.target.value)} />
          </label>
          {dq.trim() !== '' && (
            <div className="mon-options">
              <span className="nb-group-title">{t('mon.link.results')}</span>
              {found.data && results.length === 0 && <p className="muted">{t('mon.link.nothing')}</p>}
              {results.map((r) => option(r))}
            </div>
          )}
          <ErrorBanner error={found.error} strings={strings} />
          <ErrorFlash error={saver.error} strings={strings} />
        </div>
      )}
    </Modal>
  )
}

function CreateDialog({ host, onClose, onSaved }: { host: Host | null; onClose: () => void; onSaved: () => void }) {
  const t = useT(createStrings)
  const [kind, setKind] = useState('device')
  const [register, setRegister] = useState(false)
  const saver = useAction()
  useEffect(() => {
    setKind('device')
    setRegister(false)
    saver.clear()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [host])
  const registrable = kind === 'device' || kind === 'vm'
  const submit = () =>
    host &&
    void saver.run(async () => {
      await api('POST', '/api/monitoring/ci', { source_id: host.source_id, key: host.key, kind, register: register && registrable })
      onSaved()
    })
  return (
    <Modal
      open={host !== null}
      title={host ? t('mon.create.title', { host: host.name || host.host }) : ''}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>{t('mon.cancel')}</Button>
          <Button variant="primary" busy={saver.busy} onClick={submit}>
            {t('mon.create.save')}
          </Button>
        </>
      }
    >
      {host && (
        <div className="stack">
          <p className="muted">{t('mon.create.text', { name: host.host || host.name })}</p>
          {(host.also_in?.length ?? 0) > 0 && <p className="hint">{t('mon.create.also', { list: (host.also_in ?? []).map((o) => `${o.source_name}: ${o.host}`).join(', ') })}</p>}
          <Field label={t('mon.create.kind')}>
            {(id) => (
              <Select id={id} value={kind} onChange={(e) => setKind(e.target.value)}>
                {['device', 'vm', 'service', 'other'].map((k) => (
                  <option key={k} value={k}>
                    {t(`mon.kind.${k}`)}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          {registrable && <Switch checked={register} onChange={setRegister} label={t('mon.create.register')} />}
          <ErrorFlash error={saver.error} strings={createStrings} />
        </div>
      )}
    </Modal>
  )
}

type Draft = Omit<Source, 'id' | 'sync' | 'hosts' | 'matched' | 'unmatched' | 'running' | 'next_sync_at' | 'credential_name' | 'connector' | 'rules' | 'poll'> & {
  credential_id: string
  connector_id: string
  query: string
  host_label: string
  poll_alerts: boolean
  poll_seconds: number
  quiet_minutes: number
}

const placeholders: Record<Kind, string> = {
  zabbix: 'https://zabbix.example.com',
  prometheus: 'http://prometheus:9090',
  grafana: 'https://grafana.example.com',
  graylog: 'https://graylog.example.com',
}

function blank(kind: Kind = 'zabbix'): Draft {
  return {
    name: '',
    kind,
    url: '',
    credential_id: '',
    connector_id: '',
    skip_verify: false,
    enabled: true,
    sync_minutes: 60,
    query: '',
    host_label: '',
    poll_alerts: false,
    poll_seconds: 60,
    quiet_minutes: 15,
  }
}

function SourceEditor({
  value,
  defaults,
  onClose,
  onSaved,
}: {
  value: Source | 'new' | null
  defaults: View['defaults']
  onClose: () => void
  onSaved: () => void
}) {
  const t = useT(strings)
  const { can } = useSession()
  const editing = value && value !== 'new' ? value : null
  const [d, setD] = useState<Draft>(blank())
  const [report, setReport] = useState<Report | null>(null)
  const creds = useResource<Credential[]>(value && can('credentials:view') ? '/api/credentials' : '', 0)
  const save = useAction()
  const test = useAction()
  useEffect(() => {
    setD(
      editing
        ? {
            name: editing.name,
            kind: editing.kind,
            url: editing.url,
            credential_id: editing.credential_id ?? '',
            connector_id: editing.connector_id ?? '',
            skip_verify: editing.skip_verify,
            enabled: editing.enabled,
            sync_minutes: editing.sync_minutes,
            query: editing.query ?? '',
            host_label: editing.host_label ?? '',
            poll_alerts: editing.poll_alerts ?? false,
            poll_seconds: editing.poll_seconds || 60,
            quiet_minutes: editing.quiet_minutes || 15,
          }
        : blank(),
    )
    setReport(null)
    save.clear()
    test.clear()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value])
  const set = (patch: Partial<Draft>) => {
    setD({ ...d, ...patch })
    setReport(null)
  }
  const allowed = d.kind === 'zabbix' ? ['bearer', 'basic'] : ['bearer', 'basic', 'header']
  const usable = (creds.data ?? []).filter((c) => allowed.includes(c.type))
  const submit = () =>
    save.run(async () => {
      const saved = await api<Source>(editing ? 'PUT' : 'POST', editing ? `/api/monitoring/sources/${editing.id}` : '/api/monitoring/sources', d)
      // A new or changed system shows its hosts right away.
      if (saved.enabled && can('monitoring:sync')) await api('POST', `/api/monitoring/sources/${saved.id}/sync`).catch(() => undefined)
      onSaved()
    })
  const check = () =>
    test.run(async () => {
      setReport(await api<Report>('POST', '/api/monitoring/test', d))
    })
  const remove = async () =>
    editing &&
    (await ask({ text: t('mon.delete.confirm', { name: editing.name }), danger: true })) &&
    save.run(async () => {
      await api('DELETE', `/api/monitoring/sources/${editing.id}`)
      onSaved()
    })
  return (
    <Modal
      open={value !== null}
      title={editing ? t('mon.dialog.edit') : t('mon.dialog.new')}
      onClose={onClose}
      footer={
        <>
          {editing && (
            <Button variant="ghost" busy={save.busy} onClick={() => void remove()}>
              {t('mon.delete')}
            </Button>
          )}
          {can('monitoring:test') && (
            <Button busy={test.busy} onClick={() => void check()}>
              {t('mon.test')}
            </Button>
          )}
          <Button onClick={onClose}>{t('mon.cancel')}</Button>
          <Button variant="primary" busy={save.busy} onClick={() => void submit()}>
            {t('mon.save')}
          </Button>
        </>
      }
    >
      <div className="stack">
        <Segmented
          label={t('mon.kind')}
          value={d.kind}
          onChange={(kind) => set({ kind, credential_id: '', name: d.name || '', query: '', host_label: '' })}
          options={[
            { value: 'zabbix', label: t('mon.kind.zabbix') },
            { value: 'prometheus', label: t('mon.kind.prometheus') },
            { value: 'grafana', label: t('mon.kind.grafana') },
            { value: 'graylog', label: t('mon.kind.graylog') },
          ]}
        />
        <Field label={t('mon.name')}>
          {(id) => <Input id={id} value={d.name} placeholder={t(`mon.kind.${d.kind}`)} onChange={(e) => set({ name: e.target.value })} />}
        </Field>
        <Field label={t('mon.url')} hint={t(`mon.url.${d.kind}.hint`)}>
          {(id) => (
            <Input
              id={id}
              value={d.url}
              spellCheck={false}
              autoComplete="off"
              placeholder={placeholders[d.kind]}
              onChange={(e) => set({ url: e.target.value })}
            />
          )}
        </Field>
        <Field label={t('mon.cred')} hint={usable.length === 0 && creds.data ? t('mon.cred.missing') : t(`mon.cred.${d.kind}.hint`)}>
          {(id) => (
            <Select id={id} value={d.credential_id} onChange={(e) => set({ credential_id: e.target.value })}>
              <option value="">{t('mon.cred.none')}</option>
              {editing?.credential_id && !usable.some((c) => c.id === editing.credential_id) && (
                <option value={editing.credential_id}>{editing.credential_name ?? editing.credential_id}</option>
              )}
              {usable.map((c) => (
                <option key={c.id} value={c.id}>
                  {`${c.name} (${c.type})`}
                </option>
              ))}
            </Select>
          )}
        </Field>
        {d.kind === 'prometheus' && (
          <div className="rl-grid">
            <Field label={t('mon.query')} hint={t('mon.query.hint')}>
              {(id) => <Input id={id} value={d.query} spellCheck={false} placeholder={defaults.query} onChange={(e) => set({ query: e.target.value })} />}
            </Field>
            <Field label={t('mon.label')} hint={t('mon.label.hint')}>
              {(id) => <Input id={id} value={d.host_label} spellCheck={false} placeholder={defaults.host_label} onChange={(e) => set({ host_label: e.target.value })} />}
            </Field>
          </div>
        )}
        {d.kind === 'grafana' && (
          <>
            <Field label={t('mon.label')} hint={t('mon.glabel.hint', { labels: (defaults.grafana_host_labels ?? ['instance']).join(', ') })}>
              {(id) => (
                <Input id={id} value={d.host_label} spellCheck={false} placeholder={(defaults.grafana_host_labels ?? ['instance'])[0]} onChange={(e) => set({ host_label: e.target.value })} />
              )}
            </Field>
            <Switch checked={d.poll_alerts} onChange={(poll_alerts) => set({ poll_alerts })} label={t('mon.poll')} hint={t('mon.poll.hint')} />
            {d.poll_alerts && (
              <Field label={t('mon.poll.every')}>
                {(id) => (
                  <div className="nb-stepper">
                    <Stepper id={id} value={d.poll_seconds} min={15} max={3600} suffix={t('mon.seconds')} onChange={(poll_seconds) => set({ poll_seconds })} />
                  </div>
                )}
              </Field>
            )}
          </>
        )}
        {d.kind === 'graylog' && (
          <>
            <div className="rl-grid">
              <Field label={t('mon.gquery')} hint={t('mon.gquery.hint')}>
                {(id) => <Input id={id} className="cn-mono" value={d.query} spellCheck={false} placeholder="*" onChange={(e) => set({ query: e.target.value })} />}
              </Field>
              <Field label={t('mon.gfield')} hint={t('mon.gfield.hint', { field: defaults.graylog_host_field ?? 'source' })}>
                {(id) => (
                  <Input id={id} value={d.host_label} spellCheck={false} placeholder={defaults.graylog_host_field ?? 'source'} onChange={(e) => set({ host_label: e.target.value })} />
                )}
              </Field>
            </div>
            <Switch checked={d.poll_alerts} onChange={(poll_alerts) => set({ poll_alerts })} label={t('mon.poll.graylog')} hint={t('mon.poll.graylog.hint')} />
            {d.poll_alerts && (
              <div className="rl-grid">
                <Field label={t('mon.poll.every')}>
                  {(id) => (
                    <div className="nb-stepper">
                      <Stepper id={id} value={d.poll_seconds} min={15} max={3600} suffix={t('mon.seconds')} onChange={(poll_seconds) => set({ poll_seconds })} />
                    </div>
                  )}
                </Field>
                <Field label={t('mon.quiet')} hint={t('mon.quiet.hint')}>
                  {(id) => (
                    <div className="nb-stepper">
                      <Stepper id={id} value={d.quiet_minutes} min={1} max={1440} suffix={t('mon.minutes')} onChange={(quiet_minutes) => set({ quiet_minutes })} />
                    </div>
                  )}
                </Field>
              </div>
            )}
          </>
        )}
        {d.url.trim().startsWith('https') && <Switch checked={d.skip_verify} onChange={(skip_verify) => set({ skip_verify })} label={t('mon.skip')} />}
        <Switch checked={d.enabled} onChange={(enabled) => set({ enabled })} label={t('mon.enabled')} hint={t('mon.enabled.hint')} />
        <Field label={t('mon.interval')} hint={t('mon.interval.hint')}>
          {(id) => (
            <div className="nb-stepper">
              <Stepper id={id} value={d.sync_minutes} min={0} max={10080} suffix={t('mon.minutes')} onChange={(sync_minutes) => set({ sync_minutes })} />
            </div>
          )}
        </Field>
        {report &&
          (report.ok ? (
            <Flash kind="ok" title={t('mon.test.ok', { hosts: report.hosts })} trigger={report}>
              {report.version && <div>{t('mon.test.version', { version: report.version })}</div>}
              {(report.sample?.length ?? 0) > 0 && <div className="cn-mono">{(report.sample ?? []).map((h) => h.host || h.name).join(', ')}</div>}
            </Flash>
          ) : (
            <Flash kind="error" title={t('mon.test.fail')} trigger={report}>
              {report.error}
            </Flash>
          ))}
        <ErrorFlash error={test.error ?? save.error} strings={strings} />
      </div>
    </Modal>
  )
}
