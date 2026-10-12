import { useEffect, useState, type ReactNode } from 'react'
import { Copy, Eye, ExternalLink, Pencil, Plus, RefreshCw, Trash2, Tv } from 'lucide-react'
import { api } from '../../api'
import { ErrorBanner, ErrorFlash } from '../../connections/ConnectionCard'
import { useAction, useResource } from '../../connections/useRequest'
import { useLocale, useT } from '../../i18n'
import { Banner, Button, Field, formatDate, Modal, Rows, Textarea, zoneLabel } from '../../ui'
import { SummaryCard } from '../profile/SummaryCard'
import { SeverityPill } from '../incidents/IncidentDetail'
import { SEVERITIES, severityText } from '../incidents/types'
import { useSession } from '../session'
import { copyText, publicURL } from './model'
import { plural, strings } from './strings'
import type { List, Preview, PreviewIncident, Wallboard } from './types'
import '../connectors/connectors.css'
import '../cis/cis.css'
import '../incidents/incidents.css'
import '../maintenance/maintenance.css'
import './wallboards.css'
import { WallboardEditor } from './WallboardEditor'
import { notify } from '../../notify'
import { ask } from '../../confirm'
import { SetupGuide } from '../guide/SetupGuide'

type T = (key: string, vars?: Record<string, string | number>) => string

export function WallboardsPage() {
  const t = useT(strings)
  const { can } = useSession()
  const editor = can('wallboards:edit')
  const [epoch, setEpoch] = useState(0)
  const list = useResource<List>('/api/wallboards', epoch)
  const [editing, setEditing] = useState<Wallboard | 'new' | null>(null)
  const [viewing, setViewing] = useState<Wallboard | null>(null)
  const [preview, setPreview] = useState<Wallboard | null>(null)
  const act = useAction()
  const reload = () => setEpoch((e) => e + 1)

  const boards = list.data?.wallboards ?? []
  const clientIP = list.data?.client_ip ?? ''

  const copy = async (w: Wallboard) => {
    const url = publicURL(w)
    const ok = await copyText(url)
    notify(ok ? { kind: 'ok', title: t('wb.copied') } : { kind: 'warn', title: t('wb.copy.failed', { url }) })
  }
  const remove = async (w: Wallboard) =>
    (await ask({ text: t('wb.delete.confirm', { title: w.title }), danger: true })) &&
    void act.run(async () => {
      await api('DELETE', `/api/wallboards/${encodeURIComponent(w.id)}`)
      reload()
    })

  return (
    <div className="stack">
      <SetupGuide page="wallboards" actions={{ create: () => setEditing('new') }} />
      {editor && boards.length > 0 && (
        <div className="row">
          <Button variant="primary" onClick={() => setEditing('new')}>
            <Plus size={16} aria-hidden />
            {t('wb.new')}
          </Button>
        </div>
      )}
      <ErrorBanner error={list.error} strings={strings} />
      <ErrorFlash error={act.error} strings={strings} />
      {list.data && boards.length === 0 && (
        <div className="card wb-empty">
          <Tv size={36} className="wb-empty-icon" aria-hidden />
          <h2>{t('wb.empty.title')}</h2>
          <p className="muted">{t('wb.empty.text')}</p>
          <p className="muted">{t('wb.empty.access')}</p>
          {editor ? (
            <Button variant="primary" onClick={() => setEditing('new')}>
              <Plus size={16} aria-hidden />
              {t('wb.new')}
            </Button>
          ) : (
            <p className="muted">{t('wb.empty.readonly')}</p>
          )}
        </div>
      )}
      {!list.data && !list.error && <p className="muted">{t('loading')}</p>}
      {boards.length > 0 && (
        <div className="wb-grid">
          {boards.map((w) => (
            <BoardCard
              key={w.id}
              w={w}
              editor={editor && w.in_scope !== false}
              busy={act.busy}
              onEdit={() => (editor && w.in_scope !== false ? setEditing(w) : setViewing(w))}
              onPreview={() => setPreview(w)}
              onCopy={() => void copy(w)}
              onDelete={() => remove(w)}
            />
          ))}
        </div>
      )}
      {list.data && <Proxies onSaved={reload} />}
      <WallboardEditor
        value={editing}
        clientIP={clientIP}
        onClose={() => setEditing(null)}
        onSaved={() => {
          setEditing(null)
          reload()
        }}
      />
      <Details w={viewing} onClose={() => setViewing(null)} />
      <PreviewDialog w={preview} onClose={() => setPreview(null)} />
    </div>
  )
}

// summary is the short filter line of a board, such as "3 CIs · 2 services · Critical, Error".
function summary(t: T, locale: string, w: Wallboard) {
  const parts: string[] = []
  const count = (kind: 'cis' | 'services' | 'teams', n: number) => n > 0 && parts.push(t(`wb.n.${kind}.${plural(locale, n)}`, { n }))
  count('cis', w.ci_ids?.length ?? 0)
  count('services', w.service_ids?.length ?? 0)
  count('teams', w.team_ids?.length ?? 0)
  if (parts.length === 0) parts.push(t('wb.scope.all'))
  if (w.severities?.length) parts.push(w.severities.map((s) => severityText(t, s)).join(', '))
  if (w.methods?.length) parts.push(w.methods.map((m) => t(`wb.method.${m}`)).join(', '))
  if (!w.show_acknowledged) parts.push(t('wb.ack.hidden'))
  if (w.show_suppressed) parts.push(t('wb.suppressed.shown'))
  if (w.resolved_minutes > 0) parts.push(t('wb.resolved.shown', { n: w.resolved_minutes }))
  return parts.join(' · ')
}

function Networks({ list }: { list: string[] }) {
  return (
    <span className="wb-nets">
      {list.map((n) => (
        <span key={n} className="wb-net">
          {n}
        </span>
      ))}
    </span>
  )
}

function BoardCard({
  w,
  editor,
  busy,
  onEdit,
  onPreview,
  onCopy,
  onDelete,
}: {
  w: Wallboard
  editor: boolean
  busy: boolean
  onEdit: () => void
  onPreview: () => void
  onCopy: () => void
  onDelete: () => void
}) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const path = w.path || `/tv/${w.slug}`
  const names = [...(w.services ?? []), ...(w.cis ?? []), ...(w.teams ?? [])].map((r) => r.name)
  return (
    <article className={`card wb-card${w.enabled ? '' : ' wb-card-off'}`}>
      <header className="wb-card-head">
        <button type="button" className="cn-link cn-name wb-card-title" onClick={onEdit}>
          {w.title}
        </button>
        <span className={`pill pill-${w.enabled ? 'ok' : 'off'}`}>{t(w.enabled ? 'wb.on' : 'wb.off')}</span>
      </header>
      <a className="wb-path" href={path} target="_blank" rel="noopener">
        {path}
        <ExternalLink size={13} aria-hidden />
      </a>
      {w.description && <p className="muted wb-desc">{w.description}</p>}
      <div className="wb-summary" title={names.join(', ') || undefined}>
        {summary(t, locale, w)}
      </div>
      <div className="wb-meta">
        <span className="hint muted">{t('wb.networks')}</span>
        <Networks list={w.allowed_networks ?? []} />
      </div>
      <div className="hint muted wb-foot-line">
        <span className="wb-inline-icon">
          <RefreshCw size={12} aria-hidden />
          {t('wb.refresh.every', { n: w.refresh_seconds })}
        </span>
        <span>·</span>
        <span>{t(`wb.theme.${w.theme || 'dark'}`)}</span>
        <span>·</span>
        <span>
          {w.updated_by
            ? t('wb.updated', { who: w.updated_by, at: formatDate(w.updated_at, locale, timezone) })
            : t('wb.updated.at', { at: formatDate(w.updated_at, locale, timezone) })}
        </span>
      </div>
      <div className="wb-actions">
        <a className="btn btn-secondary" href={path} target="_blank" rel="noopener">
          <Tv size={15} aria-hidden />
          {t('wb.open')}
        </a>
        <Button variant="ghost" onClick={onCopy} title={t('wb.copy')} aria-label={t('wb.copy')}>
          <Copy size={15} aria-hidden />
        </Button>
        <Button variant="ghost" onClick={onPreview}>
          <Eye size={15} aria-hidden />
          {t('wb.preview')}
        </Button>
        <span className="wb-actions-spacer" />
        <Button variant="ghost" onClick={onEdit}>
          <Pencil size={15} aria-hidden />
          {t(editor ? 'wb.edit' : 'wb.view')}
        </Button>
        {editor && (
          <Button variant="ghost" busy={busy} onClick={onDelete} title={t('wb.delete')} aria-label={t('wb.delete')}>
            <Trash2 size={15} aria-hidden />
          </Button>
        )}
      </div>
    </article>
  )
}

type ProxyView = { trusted_proxies: string[]; env: string[]; client_ip: string }

// Proxies are the reverse proxies whose X-Forwarded-For is believed: those of
// UMBRELLA_TRUSTED_PROXIES (read-only) and those set here.
function Proxies({ onSaved }: { onSaved: () => void }) {
  const t = useT(strings)
  const { can } = useSession()
  const editor = can('wallboards:proxies')
  const [epoch, setEpoch] = useState(0)
  const view = useResource<ProxyView>('/api/wallboards/proxies', epoch)
  const [text, setText] = useState('')
  const save = useAction()
  useEffect(() => {
    if (view.data) setText(view.data.trusted_proxies.join('\n'))
  }, [view.data])
  const list = (v: string[] | undefined) => (v && v.length ? v.join(', ') : t('wb.proxies.none'))
  const submit = () =>
    save.run(async () => {
      const entries = text
        .split(/[\n,;]+/)
        .map((s) => s.trim())
        .filter(Boolean)
      await api('PUT', '/api/wallboards/proxies', { trusted_proxies: entries })
      notify({ kind: 'ok', title: t('wb.proxies.saved') })
      setEpoch((e) => e + 1)
      onSaved()
    })
  return (
    <SummaryCard
      title={t('wb.proxies.title')}
      text={t('wb.proxies.text')}
      rows={[
        [t('wb.proxies.set'), <span className="wb-mono">{list(view.data?.trusted_proxies)}</span>],
        [t('wb.proxies.env'), <span className="wb-mono">{list(view.data?.env)}</span>],
        [t('wb.proxies.you'), <span className="wb-mono">{view.data?.client_ip || '—'}</span>],
      ]}
    >
      <ErrorBanner error={view.error} strings={strings} />
      {editor && (
        <div className="stack wb-proxies">
          <Field label={t('wb.proxies.field')}>
            {(id) => <Textarea id={id} rows={3} spellCheck={false} className="wb-mono" placeholder="10.0.0.5" value={text} onChange={(e) => setText(e.target.value)} />}
          </Field>
          <ErrorFlash error={save.error} strings={strings} />
          <div className="row">
            <Button busy={save.busy} onClick={() => void submit()}>
              {t('wb.proxies.save')}
            </Button>
          </div>
        </div>
      )}
    </SummaryCard>
  )
}

function Names({ refs }: { refs: { id: string; name: string; missing?: boolean }[] | undefined }) {
  const t = useT(strings)
  if (!refs || refs.length === 0) return null
  return (
    <span className="mw-chosen">
      {refs.map((r) => (
        <span key={r.id} className="mw-tag wb-tag-ro">
          {r.name}
          {r.missing && <span className="muted"> ({t('wb.missing')})</span>}
        </span>
      ))}
    </span>
  )
}

// Details shows every setting of a board to users who may view but not change wallboards.
function Details({ w, onClose }: { w: Wallboard | null; onClose: () => void }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const yes = (v: boolean) => t(v ? 'yes' : 'no')
  const rows: [ReactNode, ReactNode][] = w
    ? [
        [t('wb.title'), w.title],
        [t('wb.slug'), <span className="wb-mono">{w.path || `/tv/${w.slug}`}</span>],
        [t('wb.description'), w.description],
        [t('wb.enabled'), yes(w.enabled)],
        [t('wb.chosen.cis'), <Names refs={w.cis} />],
        [t('wb.chosen.services'), <Names refs={w.services} />],
        [t('wb.chosen.teams'), <Names refs={w.teams} />],
        [t('wb.severities'), w.severities?.length ? w.severities.map((s) => severityText(t, s)).join(', ') : ''],
        [t('wb.methods'), w.methods?.length ? w.methods.map((m) => t(`wb.method.${m}`)).join(', ') : ''],
        [t('wb.show_acknowledged'), yes(w.show_acknowledged)],
        [t('wb.show_suppressed'), yes(w.show_suppressed)],
        [t('wb.resolved'), `${w.resolved_minutes} ${t('wb.minutes')}`],
        [t('wb.sort'), t(`wb.sort.${w.sort || 'newest'}`)],
        [t('wb.refresh'), `${w.refresh_seconds} ${t('wb.seconds')}`],
        [t('wb.theme'), t(`wb.theme.${w.theme || 'dark'}`)],
        [t('wb.locale'), t(`wb.locale.${w.locale || 'auto'}`)],
        [t('wb.timezone'), w.timezone ? zoneLabel(w.timezone) : t('wb.timezone.auto')],
        [t('wb.allowed'), <Networks list={w.allowed_networks ?? []} />],
        [t('wb.updated.label'), `${formatDate(w.updated_at, locale, timezone)}${w.updated_by ? ` · ${w.updated_by}` : ''}`],
      ]
    : []
  return (
    <Modal open={w !== null} title={t('wb.dialog.view')} onClose={onClose} footer={<Button onClick={onClose}>{t('wb.close')}</Button>}>
      <div className="wb-details">
        <Rows rows={rows} />
      </div>
    </Modal>
  )
}

function ago(t: T, v: string, now: number) {
  const s = Math.max(0, Math.round((now - new Date(v).getTime()) / 1000))
  if (s < 60) return t('wb.ago.s')
  if (s < 3600) return t('wb.ago.m', { n: Math.floor(s / 60) })
  if (s < 86400) return t('wb.ago.h', { n: Math.floor(s / 3600) })
  return t('wb.ago.d', { n: Math.floor(s / 86400) })
}

const PREVIEW_ROWS = 20

function PreviewDialog({ w, onClose }: { w: Wallboard | null; onClose: () => void }) {
  const t = useT(strings)
  const [epoch, setEpoch] = useState(0)
  const data = useResource<Preview>(w ? `/api/wallboards/${encodeURIComponent(w.id)}/preview` : '', epoch)
  const p = w && data.data && (data.data.board?.slug ?? w.slug) === w.slug ? data.data : null
  const incidents = p?.incidents ?? []
  const shown = incidents.slice(0, PREVIEW_ROWS)
  const hidden = (p?.counts.total ?? incidents.length) - shown.length
  const now = Date.now()
  return (
    <Modal
      open={w !== null}
      title={t('wb.preview.title', { title: w?.title ?? '' })}
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" busy={data.busy} onClick={() => setEpoch((e) => e + 1)}>
            <RefreshCw size={15} aria-hidden />
          </Button>
          {w && (
            <a className="btn btn-secondary" href={w.path || `/tv/${w.slug}`} target="_blank" rel="noopener">
              <Tv size={15} aria-hidden />
              {t('wb.open')}
            </a>
          )}
          <Button onClick={onClose}>{t('wb.close')}</Button>
        </>
      }
    >
      <div className="stack wb-preview">
        <p className="hint muted">{t('wb.preview.hint')}</p>
        <ErrorBanner error={data.error} strings={strings} />
        {!p && !data.error && <p className="muted">{t('loading')}</p>}
        {p && (
          <>
            <div className="wb-counts">
              {SEVERITIES.map(
                (s) =>
                  p.counts[s] > 0 && (
                    <span key={s} className="wb-count">
                      <SeverityPill severity={s} />
                      <b>{p.counts[s]}</b>
                    </span>
                  ),
              )}
              <span className="hint muted">
                {t('wb.preview.total', { n: p.counts.total })} · {t('wb.preview.open', { n: p.counts.open })} ·{' '}
                {t('wb.preview.acknowledged', { n: p.counts.acknowledged })}
              </span>
            </div>
            {!p.ready && <Banner kind="info" title={t('wb.preview.not_ready')} />}
            {p.ready && incidents.length === 0 && <Banner kind="ok" title={t('wb.preview.empty')} />}
            {shown.length > 0 && (
              <div className="cn-table-wrap">
                <table className="cn-table compact wb-preview-table">
                  <thead>
                    <tr>
                      <th>{t('wb.col.severity')}</th>
                      <th>{t('wb.col.incident')}</th>
                      <th>{t('wb.col.status')}</th>
                      <th>{t('wb.col.opened')}</th>
                      <th className="num">{t('wb.col.count')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {shown.map((i) => (
                      <PreviewRow key={i.id} i={i} t={t} now={now} />
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {hidden > 0 && <p className="hint muted">{t('wb.preview.more', { n: hidden })}</p>}
          </>
        )}
      </div>
    </Modal>
  )
}

function PreviewRow({ i, t, now }: { i: PreviewIncident; t: T; now: number }) {
  const tone = i.status === 'open' ? 'error' : i.status === 'acknowledged' ? 'warn' : 'ok'
  const where = [i.ci_name, i.signal].filter(Boolean).join(' · ')
  const owners = [...(i.services ?? []), i.team].filter(Boolean).join(', ')
  return (
    <tr className={i.status === 'resolved' ? 'mw-finished' : undefined}>
      <td>
        <SeverityPill severity={i.severity} />
      </td>
      <td className="wb-preview-title">
        <div>{i.title}</div>
        <div className="muted mw-sub">
          {where}
          {owners && ` — ${owners}`}
          {i.suppressed && ` · ${t('wb.suppressed')}`}
        </div>
      </td>
      <td>
        <span className={`pill pill-${tone}`}>{t(`wb.status.${i.status}`)}</span>
      </td>
      <td className="wb-nowrap" title={i.opened_at}>
        {ago(t, i.opened_at, now)}
      </td>
      <td className="num">{i.count}</td>
    </tr>
  )
}
