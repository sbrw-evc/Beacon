import { useState } from 'react'
import { Play, PlayCircle, Radio, RefreshCw, RotateCcw, Save, StepForward, Trash2 } from 'lucide-react'
import { api } from '../../api'
import { ErrorBanner, ErrorFlash } from '../../connections/ConnectionCard'
import { useAction, useResource } from '../../connections/useRequest'
import { useLocale, useT } from '../../i18n'
import { Banner, Button, Field, formatDate, Input, Modal, Select, Textarea } from '../../ui'
import { FieldList, LongText } from '../../LongText'
import { useSession } from '../session'
import { json } from './graph'
import { strings } from './strings'
import type { Capture, Connector, EventPreview, IngestRequest, Issue, Sample, Stats, StoredEvent, StoredFailure, TestAll, TestRun } from './types'
import { Flash } from '../../notify'

export type Tab = 'issues' | 'test' | 'samples' | 'requests' | 'failures' | 'events' | 'stats'

function SevPill({ sev }: { sev: string }) {
  const kind = sev === 'critical' || sev === 'error' ? 'pill-error' : sev === 'warning' ? 'pill-warn' : 'pill-off'
  return <span className={`pill ${kind}`}>{sev || '—'}</span>
}

export function EventsTable({ events }: { events: (EventPreview | StoredEvent)[] }) {
  const t = useT(strings)
  if (events.length === 0) return <p className="muted">{t('cn.events.none')}</p>
  return (
    <div className="cn-table-wrap">
      <table className="cn-table compact">
        <thead>
          <tr>
            <th>{t('cn.ev.severity')}</th>
            <th>{t('cn.ev.status')}</th>
            <th>{t('cn.ev.title')}</th>
            <th>{t('cn.ev.ci')}</th>
            <th>{t('cn.ev.signal')}</th>
            <th>{t('cn.ev.labels')}</th>
            <th>{t('cn.ev.key')}</th>
          </tr>
        </thead>
        <tbody>
          {events.map((e, i) => (
            <tr key={'id' in e ? e.id : i}>
              <td>
                <SevPill sev={e.severity} />
              </td>
              <td>{e.status}</td>
              <td>
                {e.title}
                {e.value && <span className="muted"> · {e.value}</span>}
                {(e.description || (e.fields?.length ?? 0) > 0) && (
                  <details className="cn-ev-details">
                    <summary>{t('cn.ev.details')}</summary>
                    {(e.fields?.length ?? 0) > 0 && <FieldList fields={e.fields ?? []} />}
                    {e.description && <LongText text={e.description} />}
                  </details>
                )}
              </td>
              <td className="cn-mono">{e.ci}</td>
              <td className="cn-mono">{e.signal}</td>
              <td className="cn-mono cn-labels">
                {Object.entries(e.labels ?? {})
                  .map(([k, v]) => `${k}=${v}`)
                  .join(', ')}
              </td>
              <td className="cn-mono muted" title={e.key}>
                {e.key.slice(0, 8)}
                {'seen' in e && e.seen > 1 && ` ×${e.seen}`}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function IssuesPanel({ issues, onSelect }: { issues: Issue[]; onSelect: (node: string) => void }) {
  const t = useT(strings)
  if (issues.length === 0) return <p className="muted">{t('cn.issues.none')}</p>
  return (
    <ul className="cn-issues">
      {issues.map((i, k) => (
        <li key={k} className={i.level}>
          <span className={`pill ${i.level === 'error' ? 'pill-error' : 'pill-warn'}`}>{t(`cn.level.${i.level}`)}</span>
          {i.node_id ? (
            <button type="button" className="cn-link" onClick={() => onSelect(i.node_id!)}>
              {i.node_id}
              {i.param ? `.${i.param}` : ''}
            </button>
          ) : null}
          <span>{i.message}</span>
        </li>
      ))}
    </ul>
  )
}

export function TestPanel({
  samples,
  sample,
  setSample,
  selected,
  run,
  all,
  busy,
  error,
  onRun,
  onRunAll,
}: {
  samples: Sample[]
  sample: string
  setSample: (id: string) => void
  selected: string | null
  run: TestRun | null
  all: TestAll | null
  busy: boolean
  error: unknown
  onRun: (stopAt?: string) => void
  onRunAll: () => void
}) {
  const t = useT(strings)
  const failures = run?.result?.failures ?? []
  return (
    <div className="stack">
      <div className="row cn-test-bar">
        <Select value={sample} onChange={(e) => setSample(e.target.value)} aria-label={t('cn.test.sample')}>
          {samples.length === 0 && <option value="">{t('cn.test.noSamples')}</option>}
          {samples.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </Select>
        <Button variant="primary" busy={busy} disabled={!sample} onClick={() => onRun()}>
          <Play size={14} />
          {t('cn.test.run')}
        </Button>
        <Button busy={busy} disabled={!sample || !selected} onClick={() => selected && onRun(selected)} title={t('cn.test.toNode.hint')}>
          <StepForward size={14} />
          {t('cn.test.toNode')}
        </Button>
        <Button busy={busy} disabled={samples.length === 0} onClick={onRunAll} title={t('cn.test.all.hint')}>
          <PlayCircle size={14} />
          {t('cn.test.all')}
        </Button>
      </div>
      <ErrorFlash error={error} strings={strings} />
      {run && (
        <>
          {run.issues.some((i) => i.level === 'error') && !run.result && <Banner kind="error" title={t('cn.test.cannotRun')} />}
          {run.result && (
            <p className="muted">
              {t('cn.test.summary', { events: run.events.length, failures: failures.length, filtered: run.result.filtered, skipped: run.result.skipped })}
            </p>
          )}
          {failures.map((f, i) => (
            <Banner key={i} kind="error" title={`${f.node}: ${f.error}`}>
              {t('cn.data.item', { n: f.lineage.item })}
            </Banner>
          ))}
          {run.result && <EventsTable events={run.events} />}
        </>
      )}
      {all && (
        <div className="cn-table-wrap">
          <table className="cn-table compact">
            <thead>
              <tr>
                <th>{t('cn.test.sample')}</th>
                <th className="num">{t('cn.col.events')}</th>
                <th className="num">{t('cn.test.failures')}</th>
                <th className="num">{t('cn.test.filtered')}</th>
                <th>{t('cn.test.result')}</th>
              </tr>
            </thead>
            <tbody>
              {all.samples.map((s) => (
                <tr key={s.sample_id}>
                  <td>{s.name}</td>
                  <td className="num">{s.events.length}</td>
                  <td className="num">{s.failures.length}</td>
                  <td className="num">{s.filtered + s.skipped}</td>
                  <td className={s.error || s.failures.length ? 'cn-bad' : 'cn-good'}>{s.error ?? (s.failures.length ? s.failures[0].error : t('cn.test.ok'))}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function CaptureBox({ conn, capture, editable, onChanged }: { conn: Connector; capture?: Capture; editable: boolean; onChanged: () => void }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const action = useAction()
  const [count, setCount] = useState('5')
  const [minutes, setMinutes] = useState('15')
  if (capture) {
    return (
      <Banner kind="info" title={t('cn.capture.on', { n: capture.remaining, until: formatDate(capture.until, locale, timezone) })}>
        <p className="cn-mono">{`${window.location.origin}${conn.ingest_path}`}</p>
        {editable && (
          <Button busy={action.busy} onClick={async () => (await action.run(() => api('DELETE', `/api/connectors/${conn.id}/capture`)), onChanged())}>
            {t('cn.capture.stop')}
          </Button>
        )}
      </Banner>
    )
  }
  if (!editable) return null
  return (
    <div className="row cn-capture">
      <span className="muted">{t('cn.capture.hint')}</span>
      <Input type="number" min={1} max={20} value={count} onChange={(e) => setCount(e.target.value)} aria-label={t('cn.capture.count')} className="cn-num" />
      <span className="muted">{t('cn.capture.within')}</span>
      <Input type="number" min={1} max={60} value={minutes} onChange={(e) => setMinutes(e.target.value)} aria-label={t('cn.capture.minutes')} className="cn-num" />
      <span className="muted">{t('cn.capture.unit')}</span>
      <Button
        busy={action.busy}
        onClick={async () => {
          const ok = await action.run(() => api('POST', `/api/connectors/${conn.id}/capture`, { count: Number(count), minutes: Number(minutes) }))
          if (ok !== undefined) onChanged()
        }}
      >
        <Radio size={14} />
        {t('cn.capture.start')}
      </Button>
      <ErrorFlash error={action.error} strings={strings} />
    </div>
  )
}

export function SamplesPanel({
  conn,
  samples,
  editable,
  onChanged,
}: {
  conn: Connector
  samples: Sample[]
  editable: boolean
  onChanged: () => void
}) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const [adding, setAdding] = useState(false)
  const [viewing, setViewing] = useState<Sample | null>(null)
  const action = useAction()
  const open = async (s: Sample) => {
    const full = await action.run(() => api<Sample>('GET', `/api/connectors/${conn.id}/samples/${s.id}`))
    if (full) setViewing(full)
  }
  return (
    <div className="stack">
      <CaptureBox conn={conn} capture={conn.capture} editable={editable} onChanged={onChanged} />
      <div className="row">
        {editable && (
          <Button onClick={() => setAdding(true)}>
            <Save size={14} />
            {t('cn.samples.add')}
          </Button>
        )}
        <Button variant="ghost" onClick={onChanged}>
          <RefreshCw size={14} />
          {t('cn.refresh')}
        </Button>
      </div>
      <ErrorFlash error={action.error} strings={strings} />
      {samples.length === 0 ? (
        <p className="muted">{t('cn.samples.none')}</p>
      ) : (
        <div className="cn-table-wrap">
          <table className="cn-table compact">
            <thead>
              <tr>
                <th>{t('cn.col.name')}</th>
                <th>{t('cn.samples.source')}</th>
                <th>{t('cn.samples.format')}</th>
                <th className="num">{t('cn.samples.size')}</th>
                <th>{t('cn.samples.created')}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {samples.map((s) => (
                <tr key={s.id}>
                  <td>
                    <button type="button" className="cn-link" onClick={() => void open(s)}>
                      {s.name}
                    </button>
                  </td>
                  <td>{t(`cn.source.${s.source}`)}</td>
                  <td>{s.format}</td>
                  <td className="num">{s.size}</td>
                  <td className="muted">
                    {formatDate(s.created_at, locale, timezone)} · {s.created_by}
                  </td>
                  <td>
                    {editable && (
                      <button
                        type="button"
                        className="icon-btn"
                        aria-label={t('cn.samples.delete')}
                        onClick={async () => {
                          await action.run(() => api('DELETE', `/api/connectors/${conn.id}/samples/${s.id}`))
                          onChanged()
                        }}
                      >
                        <Trash2 size={14} />
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <AddSampleDialog open={adding} connector={conn.id} onClose={() => setAdding(false)} onAdded={onChanged} />
      <Modal open={viewing !== null} title={viewing?.name ?? ''} onClose={() => setViewing(null)}>
        {viewing && <BodyView body={viewing.body ?? ''} headers={viewing.headers} query={viewing.query} />}
      </Modal>
    </div>
  )
}

function BodyView({ body, headers, query }: { body: string; headers?: Record<string, string>; query?: Record<string, string> }) {
  const t = useT(strings)
  let shown = body
  try {
    shown = json(JSON.parse(body))
  } catch {
    shown = body
  }
  return (
    <>
      {headers && Object.keys(headers).length > 0 && (
        <>
          <h3 className="section-title">{t('cn.headers')}</h3>
          <pre className="cn-json">{Object.entries(headers).map(([k, v]) => `${k}: ${v}`).join('\n')}</pre>
        </>
      )}
      {query && Object.keys(query).length > 0 && (
        <>
          <h3 className="section-title">{t('cn.query')}</h3>
          <pre className="cn-json">{json(query)}</pre>
        </>
      )}
      <h3 className="section-title">{t('cn.body')}</h3>
      <pre className="cn-json">{shown}</pre>
    </>
  )
}

function AddSampleDialog({ open, connector, onClose, onAdded }: { open: boolean; connector: string; onClose: () => void; onAdded: () => void }) {
  const t = useT(strings)
  const [name, setName] = useState('')
  const [body, setBody] = useState('')
  const action = useAction()
  const submit = async () => {
    const s = await action.run(() => api<Sample>('POST', `/api/connectors/${connector}/samples`, { name, body }))
    if (!s) return
    setName('')
    setBody('')
    onAdded()
    onClose()
  }
  return (
    <Modal
      open={open}
      title={t('cn.samples.add')}
      onClose={onClose}
      footer={
        <Button variant="primary" busy={action.busy} disabled={!name.trim() || !body.trim()} onClick={submit}>
          {t('cn.save')}
        </Button>
      }
    >
      <Field label={t('cn.col.name')}>{(id) => <Input id={id} value={name} maxLength={100} onChange={(e) => setName(e.target.value)} />}</Field>
      <Field label={t('cn.body')} hint={t('cn.samples.body.hint')}>
        {(id) => <Textarea id={id} rows={12} className="cn-mono" value={body} onChange={(e) => setBody(e.target.value)} />}
      </Field>
      <ErrorFlash error={action.error} strings={strings} />
    </Modal>
  )
}

export function RequestsPanel({ conn, editable, onSampled }: { conn: Connector; editable: boolean; onSampled: () => void }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const [epoch, setEpoch] = useState(0)
  const [status, setStatus] = useState('')
  const [viewing, setViewing] = useState<IngestRequest | null>(null)
  const list = useResource<IngestRequest[]>(`/api/connectors/${conn.id}/requests${status ? `?status=${status}` : ''}`, epoch)
  const action = useAction()
  const open = async (r: IngestRequest) => {
    const full = await action.run(() => api<IngestRequest>('GET', `/api/connectors/${conn.id}/requests/${r.id}`))
    if (full) setViewing(full)
  }
  const sample = async (r: IngestRequest) => {
    const s = await action.run(() => api<Sample>('POST', `/api/connectors/${conn.id}/samples`, { from_request: String(r.id) }))
    if (s) {
      onSampled()
      setViewing(null)
    }
  }
  return (
    <div className="stack">
      <div className="row">
        <Select value={status} onChange={(e) => setStatus(e.target.value)} aria-label={t('cn.req.status')}>
          <option value="">{t('cn.req.any')}</option>
          {['pending', 'done', 'failed'].map((s) => (
            <option key={s} value={s}>
              {t(`cn.req.${s}`)}
            </option>
          ))}
        </Select>
        <Button variant="ghost" onClick={() => setEpoch((e) => e + 1)}>
          <RefreshCw size={14} />
          {t('cn.refresh')}
        </Button>
      </div>
      <ErrorBanner error={list.error} strings={strings} />
      <ErrorFlash error={action.error} strings={strings} />
      {list.data && list.data.length === 0 && <p className="muted">{t('cn.req.none')}</p>}
      {list.data && list.data.length > 0 && (
        <div className="cn-table-wrap">
          <table className="cn-table compact">
            <thead>
              <tr>
                <th>ID</th>
                <th>{t('cn.req.received')}</th>
                <th>{t('cn.req.status')}</th>
                <th>{t('cn.col.version')}</th>
                <th className="num">{t('cn.col.events')}</th>
                <th>{t('cn.req.from')}</th>
                <th>{t('cn.req.error')}</th>
              </tr>
            </thead>
            <tbody>
              {list.data.map((r) => (
                <tr key={r.id}>
                  <td>
                    <button type="button" className="cn-link" onClick={() => void open(r)}>
                      {r.id}
                    </button>
                  </td>
                  <td className="muted">{formatDate(r.received_at, locale, timezone)}</td>
                  <td>{t(`cn.req.${r.status}`)}</td>
                  <td>v{r.version}</td>
                  <td className="num">{r.events}</td>
                  <td className="cn-mono">{r.remote_ip}</td>
                  <td className="cn-bad">{r.error}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Modal
        open={viewing !== null}
        title={t('cn.req.title', { id: viewing?.id ?? '' })}
        onClose={() => setViewing(null)}
        footer={
          editable &&
          viewing && (
            <Button busy={action.busy} onClick={() => void sample(viewing)}>
              <Save size={14} />
              {t('cn.req.toSample')}
            </Button>
          )
        }
      >
        {viewing && <BodyView body={viewing.body ?? ''} headers={viewing.headers} query={viewing.query} />}
      </Modal>
    </div>
  )
}

export function FailuresPanel({ conn, canReprocess }: { conn: Connector; canReprocess: boolean }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const [epoch, setEpoch] = useState(0)
  const [resolved, setResolved] = useState(false)
  const [chosen, setChosen] = useState<Set<number>>(new Set())
  const [result, setResult] = useState<{ requeued: number; missing: number } | null>(null)
  const list = useResource<StoredFailure[]>(`/api/connectors/${conn.id}/failures${resolved ? '?resolved=true' : ''}`, epoch)
  const action = useAction()
  const reprocess = async (ids: number[]) => {
    const r = await action.run(() => api<{ requeued: number; missing: number }>('POST', `/api/connectors/${conn.id}/failures/reprocess`, { ids }))
    if (r) {
      setResult(r)
      setChosen(new Set())
      setEpoch((e) => e + 1)
    }
  }
  const toggle = (id: number) =>
    setChosen((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  return (
    <div className="stack">
      <div className="row">
        <Select value={resolved ? 'resolved' : 'open'} onChange={(e) => setResolved(e.target.value === 'resolved')} aria-label={t('cn.fail.show')}>
          <option value="open">{t('cn.fail.open')}</option>
          <option value="resolved">{t('cn.fail.resolved')}</option>
        </Select>
        <Button variant="ghost" onClick={() => setEpoch((e) => e + 1)}>
          <RefreshCw size={14} />
          {t('cn.refresh')}
        </Button>
        {canReprocess && !resolved && (
          <>
            <Button busy={action.busy} disabled={chosen.size === 0 || conn.published === 0} onClick={() => void reprocess([...chosen])}>
              <RotateCcw size={14} />
              {t('cn.fail.reprocessChosen', { n: chosen.size })}
            </Button>
            <Button busy={action.busy} disabled={!list.data?.length || conn.published === 0} onClick={() => void reprocess([])}>
              <RotateCcw size={14} />
              {t('cn.fail.reprocessAll')}
            </Button>
          </>
        )}
      </div>
      {canReprocess && conn.published > 0 && !resolved && <p className="muted">{t('cn.fail.hint', { n: conn.published })}</p>}
      {result && <Flash kind="ok" title={t('cn.fail.requeued', { n: result.requeued, missing: result.missing })} trigger={result} />}
      <ErrorBanner error={list.error} strings={strings} />
      <ErrorFlash error={action.error} strings={strings} />
      {list.data && list.data.length === 0 && <p className="muted">{t('cn.fail.none')}</p>}
      {list.data?.map((f) => (
        <div key={f.id} className="card cn-failure">
          <div className="row">
            {canReprocess && !resolved && <input type="checkbox" checked={chosen.has(f.id)} onChange={() => toggle(f.id)} aria-label={t('cn.fail.choose')} />}
            <strong>{f.node}</strong>
            <span className="cn-bad">{f.error}</span>
          </div>
          <div className="muted">
            {t('cn.fail.meta', { request: f.request_id, item: f.item, version: f.version, retries: f.retries })} · {formatDate(f.created_at, locale, timezone)}
          </div>
          {(f.data || f.raw) && (
            <details>
              <summary>{t('cn.fail.record')}</summary>
              <pre className="cn-json">{f.data ? json(f.data) : f.raw}</pre>
            </details>
          )}
        </div>
      ))}
    </div>
  )
}

export function EventsPanel({ conn }: { conn: Connector }) {
  const t = useT(strings)
  const [epoch, setEpoch] = useState(0)
  const list = useResource<StoredEvent[]>(`/api/connectors/${conn.id}/events`, epoch)
  return (
    <div className="stack">
      <div className="row">
        <Button variant="ghost" onClick={() => setEpoch((e) => e + 1)}>
          <RefreshCw size={14} />
          {t('cn.refresh')}
        </Button>
        <span className="muted">{t('cn.events.hint')}</span>
      </div>
      <ErrorBanner error={list.error} strings={strings} />
      {list.data && <EventsTable events={list.data} />}
    </div>
  )
}

const SERIES = ['received', 'events', 'failed', 'rejected', 'filtered'] as const

export function StatsPanel({ conn }: { conn: Connector }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const [hours, setHours] = useState('24')
  const [epoch, setEpoch] = useState(0)
  const stats = useResource<Stats>(`/api/connectors/${conn.id}/stats?hours=${hours}`, epoch)
  const buckets = stats.data?.buckets ?? []
  const peak = Math.max(1, ...buckets.map((b) => b.received + b.rejected))
  const totals = Object.fromEntries(SERIES.map((s) => [s, buckets.reduce((a, b) => a + b[s], 0)]))
  const latency = buckets.length ? Math.max(...buckets.map((b) => b.latency_max_ms)) : 0
  return (
    <div className="stack">
      <div className="row">
        <Select value={hours} onChange={(e) => setHours(e.target.value)} aria-label={t('cn.stats.period')}>
          {['1', '6', '24', '168'].map((h) => (
            <option key={h} value={h}>
              {t(`cn.stats.h${h}`)}
            </option>
          ))}
        </Select>
        <Button variant="ghost" onClick={() => setEpoch((e) => e + 1)}>
          <RefreshCw size={14} />
          {t('cn.refresh')}
        </Button>
      </div>
      <ErrorBanner error={stats.error} strings={strings} />
      <div className="cn-totals">
        {SERIES.map((s) => (
          <div key={s} className={`cn-total s-${s}`}>
            <span className="muted">{t(`cn.stats.${s}`)}</span>
            <strong>{totals[s]}</strong>
          </div>
        ))}
        <div className="cn-total">
          <span className="muted">{t('cn.stats.latency')}</span>
          <strong>{latency} ms</strong>
        </div>
      </div>
      {buckets.length === 0 ? (
        <p className="muted">{t('cn.stats.none')}</p>
      ) : (
        <div className="cn-bars" role="img" aria-label={t('cn.stats.chart')}>
          {buckets.map((b) => (
            <div
              key={b.at}
              className="cn-bar"
              title={`${formatDate(b.at, locale, timezone)}\n${SERIES.map((s) => `${t(`cn.stats.${s}`)}: ${b[s]}`).join('\n')}`}
            >
              <span className="ok" style={{ height: `${((b.received - b.failed) / peak) * 100}%` }} />
              <span className="bad" style={{ height: `${((b.failed + b.rejected) / peak) * 100}%` }} />
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
