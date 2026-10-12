import { useEffect, useMemo, useState } from 'react'
import { Plus, Search, X } from 'lucide-react'
import { api } from '../../api'
import { ErrorBanner, ErrorFlash } from '../../connections/ConnectionCard'
import { useAction, useResource } from '../../connections/useRequest'
import { useLocale, useT } from '../../i18n'
import { Button, Field, formatDate, Input, Modal, Textarea } from '../../ui'
import { useSession } from '../session'
import { strings } from './strings'
import '../connectors/connectors.css'
import '../cis/cis.css'
import './maintenance.css'
import { ask } from '../../confirm'
import { SetupGuide } from '../guide/SetupGuide'

type Ref = { id: string; name: string; missing?: boolean }
type State = 'active' | 'planned' | 'finished'
type Window = {
  id: string
  /** The viewer may change the window: its targets are within their visibility scope. */
  in_scope?: boolean
  title: string
  comment: string
  ci_ids: string[]
  service_ids: string[]
  start: string
  end: string
  created_by: string
  updated_by: string
  state: State
  cis: Ref[]
  services: Ref[]
}

const pad = (n: number) => String(n).padStart(2, '0')
// datetime-local works in the browser's time zone.
const toLocal = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
const fromLocal = (v: string) => (v ? new Date(v).toISOString() : '')

const TONE: Record<State, string> = { active: 'warn', planned: 'planned', finished: 'off' }

// Prefill is a new window started from elsewhere (the incident card):
// /maintenance?new=1&ci=<id>&ci_name=<name>&service=<id>&service_name=<name>&title=<title>&hours=<n>
type Prefill = { prefill: true; title: string; cis: Ref[]; services: Ref[]; hours: number }
type Editing = Window | 'new' | Prefill | null

function refsOf(p: URLSearchParams, kind: 'ci' | 'service'): Ref[] {
  const names = p.getAll(`${kind}_name`)
  return p.getAll(kind).map((id, i) => ({ id, name: names[i] || id }))
}

// fromAddress reads the window to start and the window to show from the address once, then
// drops the parameters so a reload does not repeat them.
function fromAddress(): { prefill: Prefill | null; show: string } {
  const p = new URLSearchParams(window.location.search)
  const show = p.get('id') ?? ''
  let prefill: Prefill | null = null
  if (p.get('new') === '1') {
    const hours = Number(p.get('hours') ?? '1')
    prefill = { prefill: true, title: p.get('title') ?? '', cis: refsOf(p, 'ci'), services: refsOf(p, 'service'), hours: hours > 0 && hours <= 24 * 30 ? hours : 1 }
  }
  if (p.toString()) window.history.replaceState(null, '', window.location.pathname)
  return { prefill, show }
}

export function MaintenancePage() {
  const t = useT(strings)
  const { can } = useSession()
  const editor = can('maintenance:edit')
  const [epoch, setEpoch] = useState(0)
  const list = useResource<Window[]>('/api/maintenance', epoch)
  const [linked] = useState(fromAddress)
  const [editing, setEditing] = useState<Editing>(() => (editor ? linked.prefill : null))
  const [filter, setFilter] = useState<State | ''>('')
  const act = useAction()
  const reload = () => setEpoch((e) => e + 1)
  useEffect(() => {
    const id = window.setInterval(() => document.visibilityState === 'visible' && setEpoch((e) => e + 1), 30_000)
    return () => window.clearInterval(id)
  }, [])

  useEffect(() => {
    if (linked.show && list.data) document.getElementById(`mw-${linked.show}`)?.scrollIntoView({ block: 'center' })
  }, [linked.show, list.data])

  const all = list.data ?? []
  const count = (s: State) => all.filter((w) => w.state === s).length
  const shown = filter ? all.filter((w) => w.state === filter) : all

  const run = (fn: () => Promise<unknown>) =>
    act.run(async () => {
      await fn()
      reload()
    })

  return (
    <div className="stack">
      <SetupGuide page="maintenance" actions={{ create: () => setEditing('new') }} />
      <div className="ci-tiles mw-tiles">
        {(['active', 'planned', 'finished'] as State[]).map((s) => (
          <button key={s} type="button" className={`card ci-tile ${filter === s ? 'active' : ''}`} aria-pressed={filter === s} onClick={() => setFilter(filter === s ? '' : s)}>
            <span className="ci-tile-value">{count(s)}</span>
            <span className="muted">{t(`mw.tile.${s}`)}</span>
          </button>
        ))}
      </div>
      {editor && (
        <div className="row">
          <Button variant="primary" onClick={() => setEditing('new')}>
            <Plus size={16} aria-hidden />
            {t('mw.new')}
          </Button>
        </div>
      )}
      <ErrorBanner error={list.error} strings={strings} />
      <ErrorFlash error={act.error} strings={strings} />
      {list.data && shown.length === 0 && <p className="muted card mw-empty">{t('mw.empty')}</p>}
      {shown.length > 0 && (
        <div className="card cn-table-card">
          <table className="cn-table mw-table">
            <thead>
              <tr>
                <th>{t('mw.col.state')}</th>
                <th>{t('mw.col.title')}</th>
                <th>{t('mw.col.targets')}</th>
                <th>{t('mw.col.period')}</th>
                <th>{t('mw.col.author')}</th>
                {editor && <th />}
              </tr>
            </thead>
            <tbody>
              {shown.map((w) => (
                <Row key={w.id} w={w} shown={w.id === linked.show} editor={editor && w.in_scope !== false} busy={act.busy} onEdit={() => setEditing(w)} run={run} />
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Editor
        value={editing}
        onClose={() => setEditing(null)}
        onSaved={() => {
          setEditing(null)
          reload()
        }}
      />
    </div>
  )
}

function Targets({ w }: { w: Window }) {
  const t = useT(strings)
  const names = [...w.services, ...w.cis]
  const head = names.slice(0, 3).map((r) => r.name).join(', ')
  return (
    <span title={names.map((r) => r.name).join(', ')}>
      {head}
      {names.length > 3 && <span className="muted"> +{names.length - 3}</span>}
      <div className="muted mw-sub">
        {w.services.length > 0 && t('mw.services', { n: w.services.length })}
        {w.services.length > 0 && w.cis.length > 0 && ' · '}
        {w.cis.length > 0 && t('mw.cis', { n: w.cis.length })}
      </div>
    </span>
  )
}

function Row({
  w,
  shown,
  editor,
  busy,
  onEdit,
  run,
}: {
  w: Window
  shown: boolean
  editor: boolean
  busy: boolean
  onEdit: () => void
  run: (fn: () => Promise<unknown>) => void
}) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const at = (v: string) => formatDate(v, locale, timezone)
  return (
    <tr id={`mw-${w.id}`} className={[w.state === 'finished' ? 'mw-finished' : '', shown ? 'mw-shown' : ''].join(' ').trim() || undefined}>
      <td>
        <span className={`pill pill-${TONE[w.state]}`}>{t(`mw.state.${w.state}`)}</span>
      </td>
      <td className="mw-title">
        {editor ? (
          <button type="button" className="cn-link cn-name" onClick={onEdit}>
            {w.title}
          </button>
        ) : (
          <span className="cn-name">{w.title}</span>
        )}
        {w.comment && <div className="muted mw-sub">{w.comment}</div>}
      </td>
      <td className="mw-targets">
        <Targets w={w} />
      </td>
      <td className="mw-period">
        {at(w.start)}
        <div className="muted">→ {at(w.end)}</div>
      </td>
      <td>{w.created_by}</td>
      {editor && (
        <td className="mw-actions">
          {w.state === 'active' && (
            <Button variant="ghost" busy={busy} onClick={() => run(() => api('POST', `/api/maintenance/${w.id}/finish`))}>
              {t('mw.finish')}
            </Button>
          )}
          <Button
            variant="ghost"
            busy={busy}
            onClick={async () => (await ask({ text: t('mw.delete.confirm', { title: w.title }), danger: true })) && run(() => api('DELETE', `/api/maintenance/${w.id}`))}
          >
            {t('mw.delete')}
          </Button>
        </td>
      )}
    </tr>
  )
}

type Draft = { title: string; comment: string; start: string; end: string; services: Ref[]; cis: Ref[] }

const HOURS = [1, 2, 4, 8, 24]

function draftOf(v: Editing): Draft {
  if (v && v !== 'new' && 'prefill' in v) {
    const start = new Date()
    start.setSeconds(0, 0)
    return { title: v.title, comment: '', start: toLocal(start), end: toLocal(new Date(start.getTime() + v.hours * 3600_000)), services: v.services, cis: v.cis }
  }
  if (v && v !== 'new') return { title: v.title, comment: v.comment, start: toLocal(new Date(v.start)), end: toLocal(new Date(v.end)), services: v.services, cis: v.cis }
  const start = new Date()
  start.setSeconds(0, 0)
  return { title: '', comment: '', start: toLocal(start), end: toLocal(new Date(start.getTime() + 2 * 3600_000)), services: [], cis: [] }
}

function Editor({ value, onClose, onSaved }: { value: Editing; onClose: () => void; onSaved: () => void }) {
  const t = useT(strings)
  const [d, setD] = useState<Draft>(() => draftOf(value))
  const [q, setQ] = useState('')
  const save = useAction()
  useEffect(() => {
    setD(draftOf(value))
    setQ('')
    save.clear()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value])
  const found = useResource<{ cis: Ref[]; services: Ref[] }>(value && q.trim() ? `/api/maintenance/targets?q=${encodeURIComponent(q.trim())}` : '', 0)
  const zone = useMemo(() => Intl.DateTimeFormat().resolvedOptions().timeZone, [])
  const editing = value && value !== 'new' && !('prefill' in value) ? value : null
  const hours = d.start && d.end ? Math.round((new Date(d.end).getTime() - new Date(d.start).getTime()) / 3600_000) : 0

  const submit = () =>
    save.run(async () => {
      const body = { title: d.title, comment: d.comment, start: fromLocal(d.start), end: fromLocal(d.end), service_ids: d.services.map((s) => s.id), ci_ids: d.cis.map((c) => c.id) }
      await api(editing ? 'PUT' : 'POST', editing ? `/api/maintenance/${editing.id}` : '/api/maintenance', body)
      onSaved()
    })
  const add = (kind: 'services' | 'cis', r: Ref) => !d[kind].some((x) => x.id === r.id) && setD({ ...d, [kind]: [...d[kind], r] })
  const drop = (kind: 'services' | 'cis', id: string) => setD({ ...d, [kind]: d[kind].filter((x) => x.id !== id) })
  const setLength = (h: number) => d.start && setD({ ...d, end: toLocal(new Date(new Date(d.start).getTime() + h * 3600_000)) })

  const results = found.data && q.trim() ? found.data : null
  return (
    <Modal
      open={value !== null}
      title={editing ? t('mw.dialog.edit') : t('mw.dialog.new')}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>{t('mw.cancel')}</Button>
          <Button variant="primary" busy={save.busy} onClick={() => void submit()}>
            {t('mw.save')}
          </Button>
        </>
      }
    >
      <div className="stack mw-editor">
        <Field label={t('mw.title')}>{(id) => <Input id={id} value={d.title} placeholder={t('mw.title.ph')} onChange={(e) => setD({ ...d, title: e.target.value })} />}</Field>
        <div className="mw-period-row">
          <Field label={t('mw.start')}>{(id) => <Input id={id} type="datetime-local" value={d.start} onChange={(e) => setD({ ...d, start: e.target.value })} />}</Field>
          <Field label={t('mw.end')}>{(id) => <Input id={id} type="datetime-local" value={d.end} onChange={(e) => setD({ ...d, end: e.target.value })} />}</Field>
        </div>
        <div className="mw-quick">
          <span className="hint">{t('mw.tz', { zone })}</span>
          <span className="mw-chips">
            <button type="button" className="chip mw-chip" onClick={() => setD({ ...d, start: toLocal(new Date()), end: toLocal(new Date(Date.now() + Math.max(hours, 1) * 3600_000)) })}>
              {t('mw.now')}
            </button>
            {HOURS.map((h) => (
              <button key={h} type="button" className={`chip mw-chip${hours === h ? ' on' : ''}`} onClick={() => setLength(h)}>
                {t('mw.hours', { n: h })}
              </button>
            ))}
          </span>
        </div>
        <Field label={t('mw.targets')} hint={t('mw.targets.hint')}>
          {(id) => (
            <div className="mw-search">
              <Search size={16} className="mw-search-icon" aria-hidden />
              <Input id={id} value={q} placeholder={t('mw.search')} onChange={(e) => setQ(e.target.value)} />
            </div>
          )}
        </Field>
        {results && (
          <div className="mw-results">
            {results.services.length + results.cis.length === 0 && <p className="muted">{t('mw.nothing')}</p>}
            {results.services.length > 0 && <div className="mw-results-head">{t('mw.found.services')}</div>}
            {results.services.map((r) => (
              <button key={r.id} type="button" className="mw-result" disabled={d.services.some((x) => x.id === r.id)} onClick={() => add('services', r)}>
                <Plus size={14} aria-hidden /> {r.name}
              </button>
            ))}
            {results.cis.length > 0 && <div className="mw-results-head">{t('mw.found.cis')}</div>}
            {results.cis.map((r) => (
              <button key={r.id} type="button" className="mw-result" disabled={d.cis.some((x) => x.id === r.id)} onClick={() => add('cis', r)}>
                <Plus size={14} aria-hidden /> {r.name}
              </button>
            ))}
          </div>
        )}
        {d.services.length + d.cis.length > 0 && (
          <div className="mw-chosen">
            {(['services', 'cis'] as const).map((kind) =>
              d[kind].map((r) => (
                <span key={kind + r.id} className={`mw-tag mw-tag-${kind}`}>
                  {r.name}
                  {r.missing && <span className="muted"> ({t('mw.missing')})</span>}
                  <button type="button" aria-label={t('mw.remove', { name: r.name })} onClick={() => drop(kind, r.id)}>
                    <X size={13} />
                  </button>
                </span>
              )),
            )}
          </div>
        )}
        <Field label={t('mw.comment')}>{(id) => <Textarea id={id} rows={2} value={d.comment} onChange={(e) => setD({ ...d, comment: e.target.value })} />}</Field>
        <ErrorFlash error={save.error} strings={strings} />
      </div>
    </Modal>
  )
}
