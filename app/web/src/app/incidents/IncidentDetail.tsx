import { Fragment, useCallback, useEffect, useState, type ReactNode } from 'react'
import { CalendarClock, ExternalLink, Link2, Plus } from 'lucide-react'
import { api } from '../../api'
import { ErrorBanner, ErrorFlash } from '../../connections/ConnectionCard'
import { useAction, useResource } from '../../connections/useRequest'
import { useLocale, useT } from '../../i18n'
import { Link } from '../../router'
import { Banner, Button, formatDate, Modal, Rows, Segmented, Textarea } from '../../ui'
import { ResponseTab } from '../response/ResponseTab'
import { MachineTab } from './MachineTab'
import { machineStrings } from './machineStrings'
import { useSession } from '../session'
import { BindCIForm, CreateCIForm, ResolveConfirm } from './CatalogForms'
import { entryText, severityTone } from './format'
import { useLiveReload } from './live'
import { IncidentDetails, SourceDetailsToggle } from './SourceDetails'
import { strings } from './strings'
import { severityText, type Detail, type Incident, type PD, type Person, type Severity } from './types'
import { notify } from '../../notify'

export function SeverityPill({ severity }: { severity: Severity }) {
  const t = useT(strings)
  return (
    <span className={`pill inc-sev inc-sev-${severityTone(severity)}`}>
      <span className="inc-dot" aria-hidden />
      {severityText(t, severity)}
    </span>
  )
}

export function StatusPill({ status }: { status: Incident['status'] }) {
  const t = useT(strings)
  const tone = status === 'open' ? 'error' : status === 'acknowledged' ? 'warn' : 'ok'
  return <span className={`pill pill-${tone}`}>{t(`inc.status.${status}`)}</span>
}

// PDPill: "off" (PagerDuty is turned off) is grey and neutral, not a failure.
export function PDPill({ state }: { state: PD['state'] }) {
  const t = useT(strings)
  const tone = state === 'accepted' || state === 'acked' ? 'ok' : state === 'failed' ? 'error' : state === 'pending' || state === 'standby' ? 'warn' : 'off'
  const label = t(`inc.pd.${state}`)
  return <span className={`pill pill-${tone}`}>{label === `inc.pd.${state}` ? state : label}</span>
}

type Props = { id: string | null; actor: boolean; onClose: () => void; onChanged: () => void; onOpen: (id: string) => void }

type Tab = 'main' | 'machine' | 'timeline' | 'sources' | 'response'

// Mode: the card shows the incident, or one of the forms it leads to.
type Mode = null | 'create' | 'bind' | 'resolve'

// maintenanceURL opens the maintenance editor with the item (or the services) of the incident.
export function maintenanceURL(d: Detail, title: string) {
  const p = new URLSearchParams({ new: '1', title })
  const a = d.alert
  if (a.ci_id) {
    p.set('ci', a.ci_id)
    p.set('ci_name', d.ci?.name ?? a.ci_name)
  } else {
    for (const s of a.route.services) {
      p.append('service', s.id)
      p.append('service_name', s.name)
    }
  }
  return `/maintenance?${p.toString()}`
}

export function IncidentDetail({ id, actor, onClose, onChanged, onOpen }: Props) {
  const t = useT(strings)
  const tm = useT(machineStrings)
  const { can } = useSession()
  const [epoch, setEpoch] = useState(0)
  const [tab, setTab] = useState<Tab>('main')
  const [mode, setMode] = useState<Mode>(null)
  const detail = useResource<Detail>(id ? `/api/incidents/${encodeURIComponent(id)}` : '', epoch)
  const act = useAction()
  useEffect(() => {
    setTab('main')
    setMode(null)
    act.clear()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])
  const live = useLiveReload(
    useCallback(() => {
      if (id) setEpoch((e) => e + 1)
    }, [id]),
  )
  useEffect(() => {
    if (!id) return
    const timer = window.setInterval(() => setEpoch((e) => e + 1), live ? 30_000 : 10_000)
    return () => window.clearInterval(timer)
  }, [id, live])
  const d = detail.data && detail.data.alert.id === id ? detail.data : null
  const a = d?.alert
  const refresh = () => {
    setEpoch((e) => e + 1)
    onChanged()
  }
  const run = (action: string, body?: unknown) =>
    act.run(async () => {
      await api('POST', `/api/incidents/${encodeURIComponent(id!)}/${action}`, body)
      refresh()
    })
  const resolve = (comment: string) =>
    act.run(async () => {
      if (comment) await api('POST', `/api/incidents/${encodeURIComponent(id!)}/comment`, { text: comment })
      await api('POST', `/api/incidents/${encodeURIComponent(id!)}/resolve`)
      setMode(null)
      refresh()
    })
  const catalogDone = ({ bound }: { bound: string[] }) => {
    setMode(null)
    notify({ kind: 'ok', title: t('inc.ci.done', { n: bound.length }) })
    refresh()
  }

  const footer =
    mode !== null ? (
      <Button onClick={onClose}>{t('inc.close')}</Button>
    ) : (
      <>
        {d?.grafana_url && (
          <a className="btn btn-ghost" href={d.grafana_url} target="_blank" rel="noopener noreferrer">
            {t('inc.grafana')}
            <ExternalLink size={14} aria-hidden />
          </a>
        )}
        {d?.flow_url && (
          <a className="btn btn-ghost" href={d.flow_url} target="_blank" rel="noopener noreferrer" title={t('inc.flow.hint')}>
            {t('inc.flow')}
            <ExternalLink size={14} aria-hidden />
          </a>
        )}
        {a?.pd.incident_url && (
          <a className="btn btn-ghost" href={a.pd.incident_url} target="_blank" rel="noopener noreferrer">
            {t('inc.pd.open')}
            <ExternalLink size={14} aria-hidden />
          </a>
        )}
        {actor && a && a.status !== 'resolved' && (a.pd.state === 'standby' || (a.pd.state === 'skipped' && a.pd.error_code !== 'test')) && (
          <Button busy={act.busy} onClick={() => void run('pagerduty')}>
            {t('inc.pd.send')}
          </Button>
        )}
        {actor && a?.status === 'open' && (
          <Button busy={act.busy} onClick={() => void run('ack')}>
            {t('inc.ack')}
          </Button>
        )}
        {actor && a && a.status !== 'resolved' && (
          <Button variant="primary" busy={act.busy} onClick={() => setMode('resolve')}>
            {t('inc.resolve')}
          </Button>
        )}
        <Button onClick={onClose}>{t('inc.close')}</Button>
      </>
    )
  return (
    <Modal open={id !== null} title={a ? `${a.id} · ${a.title}` : (id ?? '')} onClose={onClose} footer={footer}>
      {!d && detail.error ? <ErrorBanner error={detail.error} strings={strings} /> : null}
      {!d && !detail.error && <p className="muted">{t('loading')}</p>}
      {d && a && mode === 'create' && <CreateCIForm a={a} onCancel={() => setMode(null)} onDone={catalogDone} />}
      {d && a && mode === 'bind' && <BindCIForm a={a} onCancel={() => setMode(null)} onDone={catalogDone} />}
      {d && a && mode === 'resolve' && <ResolveConfirm a={a} busy={act.busy} onCancel={() => setMode(null)} onResolve={(c) => void resolve(c)} />}
      {d && a && mode === null && (
        <div className="inc-detail">
          <div className="row">
            <SeverityPill severity={a.severity} />
            <StatusPill status={a.status} />
            <PDPill state={a.pd.state} />
          </div>
          <Segmented
            label={t('inc.tab.main')}
            value={tab}
            onChange={setTab}
            options={[
              { value: 'main', label: t('inc.tab.main') },
              { value: 'machine', label: tm('inc.tab.machine') },
              { value: 'timeline', label: `${t('inc.tab.timeline')} (${d.timeline.length})` },
              { value: 'sources', label: `${t('inc.tab.sources')} (${Object.keys(a.sources).length})` },
              ...(can('response:view') ? [{ value: 'response' as Tab, label: t('inc.tab.response') }] : []),
            ]}
          />
          {tab === 'main' && (
            <>
              <Catalog d={d} onMode={setMode} />
              <IncidentDetails d={d} />
              <Main d={d} onOpen={onOpen} />
              {actor && <CommentBox busy={act.busy} onComment={(text) => run('comment', { text })} />}
            </>
          )}
          {tab === 'machine' && <MachineTab d={d} onOpen={onOpen} />}
          {tab === 'timeline' && <Timeline d={d} actor={actor} busy={act.busy} onComment={(text) => run('comment', { text })} />}
          {tab === 'sources' && <Sources d={d} />}
          {tab === 'response' && <ResponseTab id={a.id} status={a.status} actor={actor} epoch={epoch} />}
        </div>
      )}
      <ErrorFlash error={act.error} strings={strings} />
    </Modal>
  )
}

// Catalog leads from the incident to the catalog: an incident waiting for its item can create it
// or name an existing one; any incident with an item or services can get a maintenance window.
function Catalog({ d, onMode }: { d: Detail; onMode: (m: Mode) => void }) {
  const t = useT(strings)
  const { can } = useSession()
  const a = d.alert
  const unknown = !a.ci_id && a.ci_name.trim() !== '' && a.status !== 'resolved'
  const cis = can('cis:edit')
  const mw = can('maintenance:edit') && (a.ci_id || a.route.services.length > 0)
  return (
    <>
      {unknown && (
        <Banner kind="warn" title={t('inc.ci.unknown', { name: a.ci_name })}>
          {cis ? t('inc.ci.unknown.text') : t('inc.ci.unknown.ask')}
        </Banner>
      )}
      {((unknown && cis) || mw) && (
        <div className="row inc-actions">
          {unknown && cis && (
            <>
              <Button onClick={() => onMode('create')}>
                <Plus size={16} aria-hidden />
                {t('inc.ci.create')}
              </Button>
              <Button onClick={() => onMode('bind')}>
                <Link2 size={16} aria-hidden />
                {t('inc.ci.bind.open')}
              </Button>
            </>
          )}
          {mw && (
            <Link className="btn btn-secondary" to={maintenanceURL(d, t('inc.mw.title', { name: d.ci?.name ?? a.ci_name }))}>
              <CalendarClock size={16} aria-hidden />
              {a.ci_id ? t('inc.mw.ci') : t('inc.mw.services')}
            </Link>
          )}
        </div>
      )}
    </>
  )
}

function People({ list }: { list: Person[] }) {
  const t = useT(strings)
  if (list.length === 0) return <span className="muted">{t('inc.none')}</span>
  return (
    <ul className="ci-owners">
      {list.map((p) => (
        <li key={p.user_id + (p.role ?? '')}>
          {p.name}
          {p.role && <span className="muted"> · {p.role === 'lead' || p.role === 'member' ? t(`inc.role.${p.role}`) : p.role}</span>}
          {p.email && (
            <>
              {' · '}
              <a href={`mailto:${p.email}`}>{p.email}</a>
            </>
          )}
          {p.telegram && <span className="muted"> · Telegram</span>}
        </li>
      ))}
    </ul>
  )
}

function Main({ d, onOpen }: { d: Detail; onOpen: (id: string) => void }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone, can } = useSession()
  const a = d.alert
  const at = (v?: string) => formatDate(v, locale, timezone)
  const ci = d.ci
  const services = d.services ?? []
  const rows: [string, ReactNode][] = [
    [
      t('inc.field.ci'),
      <span key="ci">
        {ci && can('cis:view') ? <Link to={`/cis?id=${encodeURIComponent(ci.id)}`}>{ci.name}</Link> : a.ci_name}
        {ci && ci.name !== a.ci_name && <span className="muted"> ({a.ci_name})</span>}
        {!a.ci_id && <span className="inc-warn"> ({t('inc.noci')})</span>}
        {ci?.netbox_url && (
          <>
            {' · '}
            <a href={ci.netbox_url} target="_blank" rel="noopener noreferrer">
              {t('inc.netbox')}
              <ExternalLink size={13} aria-hidden />
            </a>
          </>
        )}
        {ci?.inventory_url && (
          <>
            {' · '}
            <a href={ci.inventory_url} target="_blank" rel="noopener noreferrer">
              Inventory DB
              <ExternalLink size={13} aria-hidden />
            </a>
          </>
        )}
      </span>,
    ],
    [t('inc.field.signal'), a.signal],
    [t('inc.field.method'), t(`inc.method.${a.method}`)],
    [
      t('inc.field.services'),
      <span key="s">
        {a.route.services.map((s, i) => (
          <Fragment key={s.id}>
            {i > 0 && ', '}
            {can('services:view') ? <Link to={`/services?id=${encodeURIComponent(s.id)}`}>{s.name}</Link> : s.name}
          </Fragment>
        ))}
      </span>,
    ],
  ]
  const links = services.filter((s) => s.links.length > 0)
  if (links.length)
    rows.push([
      t('inc.field.links'),
      <ul key="l" className="inc-links">
        {links.map((s) => (
          <li key={s.id}>
            {links.length > 1 && <span className="muted">{s.name}: </span>}
            {s.links.map((l, i) => (
              <Fragment key={l.url + i}>
                {i > 0 && ' · '}
                <a href={l.url} target="_blank" rel="noopener noreferrer">
                  {l.title || l.url}
                  <ExternalLink size={13} aria-hidden />
                </a>
              </Fragment>
            ))}
          </li>
        ))}
      </ul>,
    ])
  rows.push(
    [t('inc.field.team'), a.route.team?.name ?? ''],
    [t('inc.field.route'), t(`inc.via.${a.route.via}`)],
    [t('inc.field.people'), <People key="p" list={a.route.people} />],
    [t('inc.field.owners'), <People key="o" list={a.route.owners} />],
    [t('inc.field.opened'), at(a.opened_at)],
    [t('inc.field.first'), at(a.first_seen)],
    [t('inc.field.last'), `${at(a.last_seen)} · ${a.count}`],
  )
  if (a.acked_at) rows.push([t('inc.field.acked'), `${at(a.acked_at)} · ${a.acked_by ?? ''}`])
  if (a.resolved_at) rows.push([t('inc.field.resolved'), `${at(a.resolved_at)}${a.resolved_by ? ` · ${a.resolved_by}` : ''}`])
  rows.push([t('inc.field.pd'), <PDPill key="pd" state={a.pd.state} />])
  if (a.pd.route) rows.push([t('inc.field.pdroute'), a.pd.route])
  if (a.pd.queue) rows.push([t('inc.field.pdqueue'), a.pd.queue_name || a.pd.queue])
  if (a.pd.error) rows.push([t('inc.field.pderror'), <span key="e" className="inc-warn">{a.pd.error}</span>])
  if (a.fallback) rows.push([t('inc.field.fallback'), at(a.fallback_at)])
  if (a.excluded) rows.push([t('inc.field.excluded'), t('inc.excluded.text')])
  else if (a.suppressed) {
    const m = d.maintenance
    rows.push([
      t('inc.field.maintenance'),
      m ? (
        <span key="m">
          {can('maintenance:view') ? <Link to={`/maintenance?id=${encodeURIComponent(m.id)}`}>{m.title}</Link> : m.title}
          <span className="muted">
            {' · '}
            {at(m.start)} → {at(m.end)}
          </span>
        </span>
      ) : (
        <span key="m" className="muted">
          {t('inc.mw.gone')}
        </span>
      ),
    ])
  }
  if (a.related_id)
    rows.push([
      t('inc.field.related'),
      <button key="r" type="button" className="cn-link" onClick={() => onOpen(a.related_id!)}>
        {a.related_id}
      </button>,
    ])
  const labels = Object.entries(a.labels ?? {})
  if (labels.length) rows.push([t('inc.field.labels'), <span key="l" className="cn-mono">{labels.map(([k, v]) => `${k}=${v}`).join(', ')}</span>])
  rows.push([t('inc.field.key'), <code key="k">{a.pd.key}</code>])
  return <Rows rows={rows} />
}

function CommentBox({ busy, onComment }: { busy: boolean; onComment: (text: string) => Promise<unknown> }) {
  const t = useT(strings)
  const [text, setText] = useState('')
  return (
    <div className="inc-comment">
      <Textarea value={text} rows={2} placeholder={t('inc.comment.placeholder')} aria-label={t('inc.comment')} onChange={(e) => setText(e.target.value)} />
      <Button
        busy={busy}
        disabled={!text.trim()}
        onClick={() =>
          void onComment(text).then(() => {
            setText('')
          })
        }
      >
        {t('inc.comment.add')}
      </Button>
    </div>
  )
}

function Timeline({ d, actor, busy, onComment }: { d: Detail; actor: boolean; busy: boolean; onComment: (text: string) => Promise<unknown> }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  return (
    <div className="stack">
      {actor && <CommentBox busy={busy} onComment={onComment} />}
      <ol className="inc-timeline">
        {[...d.timeline].reverse().map((e) => (
          <li key={e.id} className={`inc-tl inc-tl-${e.kind}`}>
            <span className="inc-tl-at muted">{formatDate(e.at, locale, timezone)}</span>
            <span className="inc-tl-text">
              {entryText(t, e, d.connectors)}
              {e.author && <span className="muted"> · {e.author}</span>}
            </span>
          </li>
        ))}
      </ol>
    </div>
  )
}

function Sources({ d }: { d: Detail }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const list = Object.values(d.alert.sources).sort((x, y) => y.last_seen.localeCompare(x.last_seen))
  return (
    <table className="cn-table compact">
      <tbody>
        {list.map((s) => (
          <tr key={s.connector_id + s.key}>
            <td>{d.connectors[s.connector_id] ?? s.connector_id}</td>
            <td>
              {s.title}
              {s.value && <span className="muted"> · {s.value}</span>}
              <SourceDetailsToggle s={s} />
            </td>
            <td>
              <SeverityPill severity={s.severity} />
            </td>
            <td>
              <span className={`pill pill-${s.status === 'firing' ? 'error' : 'ok'}`}>{t(`inc.src.${s.status}`)}</span>
            </td>
            <td className="muted">{formatDate(s.last_seen, locale, timezone)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
