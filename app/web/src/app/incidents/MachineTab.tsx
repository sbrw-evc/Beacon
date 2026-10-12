import { useEffect, useMemo, useState } from 'react'
import { ExternalLink, RefreshCw, Search } from 'lucide-react'
import { ErrorBanner } from '../../connections/ConnectionCard'
import { mergeDicts } from '../../connections/connectionStrings'
import { useResource } from '../../connections/useRequest'
import { useLocale, useT } from '../../i18n'
import { Link } from '../../router'
import { Banner, Button, Input, Segmented, Stepper, Switch } from '../../ui'
import { useSession } from '../session'
import { formatValue, LineChart, panelTitle, seriesColor, type ChartMark } from './Chart'
import { severityTone } from './format'
import { machineStrings } from './machineStrings'
import { strings } from './strings'
import { severityText, type Detail } from './types'

type Series = { name: string; unit?: string; points: [number, number][] }
type SourceError = { source: string; error: string }
type Panel = {
  id: string
  title: string
  unit: string
  series: Series[]
  errors: SourceError[]
  // alert: the metric the alert fired on; focus: a graph of the same kind.
  alert?: boolean
  query?: string
  source?: string
  op?: string
  threshold?: number
  focus?: boolean
}
type Host = { source_id: string; source_name: string; kind: string; key: string; host: string; name: string; state: string; url?: string }
type Event = {
  connector_id: string
  connector: string
  title: string
  signal: string
  severity: string
  status: string
  value?: string
  first_seen: string
  last_seen: string
  count: number
}
// SourceEvent is a problem a monitoring system (Zabbix) raised on the machine.
type SourceEvent = {
  id: string
  at: string
  title: string
  severity: string
  level: string
  status: string
  resolved_at?: string
  acknowledged?: boolean
  suppressed?: boolean
  tags?: string[]
  url?: string
  source: string
}
type Other = { id: string; title: string; severity: string; status: string; opened_at: string; resolved_at?: string }
type Machine = {
  from: string
  to: string
  opened_at: string
  resolved_at?: string
  window_minutes: number
  span: boolean
  default_window: number
  names: string[]
  hosts: Host[]
  panels: Panel[]
  log_sources: number
  events: Event[]
  incidents: Other[]
  events_error?: string
  source_events: SourceEvent[]
  source_events_errors: SourceError[]
  source_events_truncated?: boolean
  source_event_hosts: number
}
type LogLine = { at: string; level?: string; text: string; source: string; labels?: Record<string, string> }
type Logs = { from: string; to: string; names: string[]; sources: number; lines: LogLine[]; truncated: boolean; errors: SourceError[] }

const PRESETS = [15, 60, 360, 1440] as const
type Choice = '15' | '60' | '360' | '1440' | 'custom'

const allStrings = mergeDicts(strings, machineStrings)

function levelTone(level?: string) {
  const l = (level ?? '').toLowerCase()
  if (/^(crit|fatal|emerg|alert|panic|err)/.test(l)) return 'error'
  if (/^warn/.test(l)) return 'warn'
  if (/^(debug|trace)/.test(l)) return 'off'
  return ''
}

// MachineTab shows the machine of an incident around its start: graphs of the monitoring
// systems that know it, the events Umbrella received about it and its log lines.
export function MachineTab({ d, onOpen }: { d: Detail; onOpen: (id: string) => void }) {
  const t = useT(allStrings)
  const { locale } = useLocale()
  const { timezone, can } = useSession()
  const id = d.alert.id
  // minutes: null until the person chooses; the server answers with the default window.
  const [minutes, setMinutes] = useState<number | null>(null)
  const [custom, setCustom] = useState(false)
  // draft: the custom window while it is being changed; it is read once the person stops.
  const [draft, setDraft] = useState<number | null>(null)
  useEffect(() => {
    if (draft === null) return
    const timer = window.setTimeout(() => setMinutes(draft), 600)
    return () => window.clearTimeout(timer)
  }, [draft])
  const [span, setSpan] = useState(false)
  const [epoch, setEpoch] = useState(0)
  const [text, setText] = useState('')
  const [search, setSearch] = useState('')
  const params = new URLSearchParams()
  if (minutes !== null) params.set('minutes', String(minutes))
  if (span) params.set('span', 'true')
  const base = `/api/incidents/${encodeURIComponent(id)}/machine`
  const machine = useResource<Machine>(`${base}?${params.toString()}`, epoch)
  const logParams = new URLSearchParams(params)
  if (search) logParams.set('q', search)
  const logs = useResource<Logs>(`${base}/logs?${logParams.toString()}`, epoch)
  const m = machine.data
  const current = minutes ?? m?.window_minutes ?? 60
  const choice: Choice = custom || !PRESETS.includes(current as (typeof PRESETS)[number]) ? 'custom' : (String(current) as Choice)

  const fmt = useMemo(() => {
    const tag = locale === 'ru' ? 'ru-RU' : 'en-GB'
    const opts = (long: boolean): Intl.DateTimeFormatOptions => ({
      ...(timezone ? { timeZone: timezone } : {}),
      hour: '2-digit',
      minute: '2-digit',
      ...(long ? { day: '2-digit', month: '2-digit' } : {}),
    })
    let short: Intl.DateTimeFormat, longF: Intl.DateTimeFormat
    try {
      short = new Intl.DateTimeFormat(tag, opts(false))
      longF = new Intl.DateTimeFormat(tag, opts(true))
    } catch {
      short = new Intl.DateTimeFormat(tag, { hour: '2-digit', minute: '2-digit' })
      longF = new Intl.DateTimeFormat(tag, { hour: '2-digit', minute: '2-digit', day: '2-digit', month: '2-digit' })
    }
    return (ms: number, long?: boolean) => (long ? longF : short).format(new Date(ms))
  }, [locale, timezone])
  const at = (v: string) => fmt(Date.parse(v), true)

  const marks: ChartMark[] = []
  if (m) {
    marks.push({ at: Date.parse(m.opened_at), tone: 'open', label: t('mc.mark.open', { at: at(m.opened_at) }) })
    if (m.resolved_at) marks.push({ at: Date.parse(m.resolved_at), tone: 'resolve', label: t('mc.mark.resolve', { at: at(m.resolved_at) }) })
    for (const e of m.events) marks.push({ at: Date.parse(e.first_seen), tone: 'event', label: `${at(e.first_seen)} · ${e.connector || e.connector_id}: ${e.title}` })
    for (const e of m.source_events ?? []) marks.push({ at: Date.parse(e.at), tone: 'source', label: `${at(e.at)} · ${e.source}: ${e.title}` })
    for (const o of m.incidents)
      if (o.id !== id) marks.push({ at: Date.parse(o.opened_at), tone: 'incident', label: `${at(o.opened_at)} · ${o.id}: ${o.title}` })
  }
  // Without a host only the metric of the alert can be drawn (a rule of Umbrella has its own source).
  const shown = m ? m.panels.filter((p) => p.alert || m.hosts.length > 0) : []
  const from = m ? Date.parse(m.from) : 0
  const to = m ? Date.parse(m.to) : 0

  return (
    <div className="inc-machine stack">
      <div className="mc-controls">
        <Segmented<Choice>
          label={t('mc.window')}
          value={choice}
          onChange={(v) => {
            setDraft(null)
            if (v === 'custom') {
              setCustom(true)
              setMinutes(current)
            } else {
              setCustom(false)
              setMinutes(Number(v))
            }
          }}
          options={[...PRESETS.map((p) => ({ value: String(p) as Choice, label: t(`mc.window.${p}`) })), { value: 'custom' as Choice, label: t('mc.window.custom') }]}
        />
        {choice === 'custom' && (
          <div className="nb-stepper">
            <Stepper value={draft ?? current} min={1} max={10080} suffix={t('mc.window.minutes')} label={t('mc.window')} onChange={setDraft} />
          </div>
        )}
        <Button variant="ghost" busy={machine.busy || logs.busy} onClick={() => setEpoch((e) => e + 1)}>
          <RefreshCw size={15} aria-hidden />
          {t('mc.refresh')}
        </Button>
      </div>
      <Switch checked={span} onChange={setSpan} label={t('mc.span')} hint={t('mc.span.hint')} />
      <ErrorBanner error={machine.error} strings={allStrings} />
      {!m && !machine.error && <p className="muted">{t('loading')}</p>}
      {m && (
        <>
          <p className="muted mc-range">
            {t('mc.range', { from: at(m.from), to: at(m.to) })}
            {m.hosts.length > 0 && (
              <>
                {' · '}
                {t('mc.hosts')}:{' '}
                {m.hosts.map((h, i) => (
                  <span key={h.source_id + h.key}>
                    {i > 0 && ', '}
                    {h.source_name} ({h.name || h.host})
                    {h.url && (
                      <a href={h.url} target="_blank" rel="noopener noreferrer" aria-label={h.source_name}>
                        <ExternalLink size={12} aria-hidden />
                      </a>
                    )}
                  </span>
                ))}
              </>
            )}
          </p>
          {m.hosts.length === 0 && (
            <Banner kind="info" title={t('mc.hosts.none')}>
              {d.alert.ci_id ? t('mc.hosts.none.text') : t('mc.hosts.none.ci', { name: d.alert.ci_name })}
            </Banner>
          )}
          {m.hosts.length > 0 && m.panels.filter((p) => !p.alert).length === 0 && <Banner kind="info" title={t('mc.panels.none')} />}
          {shown.length > 0 && (
            <div className="mc-panels">
              {shown.map((p) => (
                <section key={p.id} className={`mc-panel card ${p.alert ? 'mc-panel-alert' : p.focus ? 'mc-panel-focus' : ''}`}>
                  <header className="mc-panel-head">
                    <span className="mc-panel-title">
                      <b>{p.alert ? p.title : panelTitle(t, p)}</b>
                      {p.alert && <span className="pill pill-error">{t('mc.alert.metric')}</span>}
                      {p.focus && <span className="pill pill-warn">{t('mc.alert.kind')}</span>}
                    </span>
                    <span className="muted">
                      {p.source && `${p.source}${p.unit ? ' · ' : ''}`}
                      {p.unit}
                    </span>
                  </header>
                  {p.query && (
                    <code className="mc-query" title={p.query}>
                      {p.query}
                      {p.threshold !== undefined && p.op && !p.query.trim().endsWith(String(p.threshold)) && ` ${p.op} ${p.threshold}`}
                    </code>
                  )}
                  {p.series.length > 0 ? (
                    <>
                      <LineChart
                        series={p.series}
                        from={from}
                        to={to}
                        unit={p.unit}
                        marks={marks}
                        formatTime={fmt}
                        label={p.alert ? p.title : panelTitle(t, p)}
                        threshold={p.threshold}
                      />
                      <ul className="mc-legend">
                        {p.series.map((s, i) => (
                          <li key={s.name}>
                            <span className="mc-dot" style={{ background: seriesColor(i) }} />
                            {s.name}
                          </li>
                        ))}
                        {p.threshold !== undefined && (
                          <li>
                            <span className="mc-line mc-line-threshold" />
                            {t('mc.threshold', { op: p.op ?? '', value: formatValue(p.threshold, p.unit) })}
                          </li>
                        )}
                      </ul>
                    </>
                  ) : (
                    p.errors.length === 0 && <p className="muted mc-empty">{t('mc.panel.empty')}</p>
                  )}
                  {p.errors.map((e) => (
                    <p key={e.source} className="inc-warn mc-error">
                      {t('mc.panel.error', { source: e.source, error: e.error })}
                    </p>
                  ))}
                </section>
              ))}
            </div>
          )}
          {shown.length > 0 && (
            <ul className="mc-legend mc-marks-legend muted">
              <li>
                <span className="mc-line mc-line-open" />
                {t('mc.legend.open')}
              </li>
              {m.resolved_at && (
                <li>
                  <span className="mc-line mc-line-resolve" />
                  {t('mc.legend.resolve')}
                </li>
              )}
              {m.events.length > 0 && (
                <li>
                  <span className="mc-line mc-line-event" />
                  {t('mc.legend.event')}
                </li>
              )}
              {(m.source_events?.length ?? 0) > 0 && (
                <li>
                  <span className="mc-line mc-line-source" />
                  {t('mc.legend.source')}
                </li>
              )}
            </ul>
          )}

          {m.source_event_hosts > 0 && <SourceEvents m={m} at={at} />}

          <section className="stack mc-section">
            <h3>{t('mc.events')}</h3>
            {m.events_error && <Banner kind="warn" title={t('mc.events.error', { error: m.events_error })} />}
            {m.incidents.length > 1 && (
              <div className="mc-incidents">
                <span className="muted">{t('mc.incidents')}:</span>
                {m.incidents.map((o) =>
                  o.id === id ? (
                    <span key={o.id} className="pill pill-off">
                      {o.id} · {t('mc.incidents.this')}
                    </span>
                  ) : (
                    <button key={o.id} type="button" className="cn-link" title={o.title} onClick={() => onOpen(o.id)}>
                      {o.id} · {severityText(t, o.severity)}
                    </button>
                  ),
                )}
              </div>
            )}
            {m.events.length === 0 && !m.events_error ? (
              <p className="muted">{t('mc.events.none')}</p>
            ) : (
              m.events.length > 0 && (
                <div className="mc-scroll">
                  <table className="cn-table compact">
                    <thead>
                      <tr>
                        <th>{t('mc.events.col.time')}</th>
                        <th>{t('mc.events.col.source')}</th>
                        <th>{t('mc.events.col.event')}</th>
                        <th>{t('mc.events.col.status')}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {m.events.map((e, i) => (
                        <tr key={e.connector_id + e.first_seen + i}>
                          <td className="muted mc-nowrap">
                            {at(e.first_seen)}
                            {e.last_seen !== e.first_seen && ` → ${at(e.last_seen)}`}
                          </td>
                          <td>{e.connector || e.connector_id}</td>
                          <td>
                            <span className={`pill inc-sev inc-sev-${severityTone(e.severity)}`}>
                              <span className="inc-dot" aria-hidden />
                              {severityText(t, e.severity)}
                            </span>{' '}
                            {e.title}
                            {e.value && <span className="muted"> · {e.value}</span>}
                            {e.count > 1 && <span className="muted"> {t('mc.events.count', { n: e.count })}</span>}
                          </td>
                          <td>
                            <span className={`pill pill-${e.status === 'firing' ? 'error' : 'ok'}`}>{t(e.status === 'firing' ? 'mc.firing' : 'mc.resolved')}</span>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )
            )}
          </section>
        </>
      )}

      <section className="stack mc-section">
        <h3>{t('mc.logs')}</h3>
        <form
          className="mc-search"
          onSubmit={(e) => {
            e.preventDefault()
            setSearch(text.trim())
          }}
        >
          <Input value={text} placeholder={t('mc.logs.search')} aria-label={t('mc.logs.search')} onChange={(e) => setText(e.target.value)} />
          <Button busy={logs.busy} onClick={() => setSearch(text.trim())}>
            <Search size={15} aria-hidden />
            {t('mc.logs.find')}
          </Button>
        </form>
        <ErrorBanner error={logs.error} strings={allStrings} />
        {logs.data && <LogList l={logs.data} at={at} canSetUp={can('monitoring:view')} />}
        {!logs.data && !logs.error && <p className="muted">{t('loading')}</p>}
      </section>
    </div>
  )
}

// SourceEvents lists the problems the monitoring systems raised on the machine in the window.
function SourceEvents({ m, at }: { m: Machine; at: (v: string) => string }) {
  const t = useT(allStrings)
  const from = Date.parse(m.from)
  const events = m.source_events ?? []
  const errors = m.source_events_errors ?? []
  return (
    <section className="stack mc-section">
      <h3>{t('mc.src')}</h3>
      {errors.map((e) => (
        <Banner key={e.source} kind="warn" title={t('mc.src.error', { source: e.source, error: e.error })} />
      ))}
      {m.source_events_truncated && <p className="muted">{t('mc.src.truncated', { n: events.length })}</p>}
      {events.length === 0 && errors.length === 0 && <p className="muted">{t('mc.src.none')}</p>}
      {events.length > 0 && (
        <div className="mc-scroll">
          <table className="cn-table compact">
            <thead>
              <tr>
                <th>{t('mc.events.col.time')}</th>
                <th>{t('mc.events.col.source')}</th>
                <th>{t('mc.events.col.event')}</th>
                <th>{t('mc.events.col.status')}</th>
              </tr>
            </thead>
            <tbody>
              {events.map((e) => (
                <tr key={e.source + e.id}>
                  <td className="muted mc-nowrap">
                    {at(e.at)}
                    {Date.parse(e.at) < from && <div className="muted">{t('mc.src.started')}</div>}
                  </td>
                  <td>{e.source}</td>
                  <td>
                    <span className={`pill inc-sev inc-sev-${severityTone(e.severity)}`} title={e.level}>
                      <span className="inc-dot" aria-hidden />
                      {severityText(t, e.severity)}
                    </span>{' '}
                    {e.url ? (
                      <a href={e.url} target="_blank" rel="noopener noreferrer" title={t('mc.src.open')}>
                        {e.title} <ExternalLink size={12} aria-hidden />
                      </a>
                    ) : (
                      e.title
                    )}
                    {(e.tags?.length ?? 0) > 0 && (
                      <span className="mc-src-tags">
                        {e.tags!.map((tag) => (
                          <span key={tag} className="pill pill-off">
                            {tag}
                          </span>
                        ))}
                      </span>
                    )}
                  </td>
                  <td>
                    <span className={`pill pill-${e.status === 'firing' ? 'error' : 'ok'}`}>{t(e.status === 'firing' ? 'mc.firing' : 'mc.resolved')}</span>
                    {e.resolved_at && <span className="muted"> {t('mc.src.until', { at: at(e.resolved_at) })}</span>}
                    {e.acknowledged && (
                      <>
                        {' '}
                        <span className="pill pill-ok">{t('mc.src.ack')}</span>
                      </>
                    )}
                    {e.suppressed && (
                      <>
                        {' '}
                        <span className="pill pill-off">{t('mc.src.suppressed')}</span>
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

function LogList({ l, at, canSetUp }: { l: Logs; at: (v: string) => string; canSetUp: boolean }) {
  const t = useT(allStrings)
  if (l.sources === 0)
    return (
      <p className="muted">
        {t('mc.logs.nosources')}
        {canSetUp && (
          <>
            {' '}
            <Link to="/monitoring?tab=context">{t('mc.logs')} →</Link>
          </>
        )}
      </p>
    )
  if (l.names.length === 0) return <p className="muted">{t('mc.logs.nonames')}</p>
  return (
    <>
      <p className="muted mc-names">{t('mc.logs.names', { names: l.names.join(', ') })}</p>
      {l.errors.map((e) => (
        <Banner key={e.source} kind="warn" title={t('mc.logs.error', { source: e.source, error: e.error })} />
      ))}
      {l.truncated && <p className="muted">{t('mc.logs.truncated', { n: l.lines.length })}</p>}
      {l.lines.length === 0 && l.errors.length === 0 && <p className="muted">{t('mc.logs.none')}</p>}
      {l.lines.length > 0 && (
        <ol className="mc-logs">
          {l.lines.map((line, i) => {
            const tone = levelTone(line.level)
            return (
              <li key={i}>
                <span className="mc-log-at muted">{at(line.at)}</span>
                {line.level ? <span className={`pill pill-${tone || 'off'} mc-log-level`}>{line.level}</span> : <span className="mc-log-level" />}
                <span className="mc-log-src muted" title={Object.entries(line.labels ?? {}).map(([k, v]) => `${k}=${v}`).join(', ')}>
                  {line.source}
                </span>
                <span className="mc-log-text">{line.text}</span>
              </li>
            )
          })}
        </ol>
      )}
    </>
  )
}
