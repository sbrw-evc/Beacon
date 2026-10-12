import { useEffect, useState, type ReactNode } from 'react'
import { ArrowDown, ArrowUp, Plus, RefreshCw, Send, Trash2 } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { api } from '../../../api'
import { useResource } from '../../../connections/useRequest'
import { useLocale, useT } from '../../../i18n'
import { Banner, Button, Field, formatDate, Input, Password, Rows, Select, Switch } from '../../../ui'
import { ProfileCard } from '../../profile/ProfileCard'
import { SummaryCard } from '../../profile/SummaryCard'
import { useAction } from '../../profile/useAction'
import { useSession } from '../../session'
import { PDQueues } from './PDQueues'
import { strings } from './strings'
import { severityText } from '../../incidents/types'
import { PD_MODES, SEVERITIES, type PagerDutyView, type PDService, type PDSync, type Refs } from './types'

type RouteDraft = { key: string; id: string; name: string; team_id: string; service_id: string; routing_key: string; pd_service_id: string; has_key: boolean }

type Draft = {
  enabled: boolean
  region: 'us' | 'eu'
  min_severity: string
  events_url: string
  api_url: string
  routing_key: string
  pd_service_id: string
  api_token: string
  webhook_secret: string
  routes: RouteDraft[]
  mode: string
  modes: Record<string, string>
  // Minutes as typed; empty is the default.
  backup_min: string
  sync: PDSync
}

// The choices of how often incident states are read back, in seconds (0: default, -1: off).
const SYNC_INTERVALS = [15, 30, 0, 300, -1]

let seq = 0

export const reveal = { duration: 0.28, ease: [0.22, 1, 0.36, 1] as const }

function draftOf(v: PagerDutyView): Draft {
  return {
    enabled: v.enabled,
    region: v.region,
    min_severity: v.min_severity ?? '',
    events_url: v.events_url ?? '',
    api_url: v.api_url ?? '',
    routing_key: '',
    pd_service_id: v.service_id ?? '',
    api_token: '',
    webhook_secret: '',
    routes: v.routes.map((r) => ({
      key: r.id,
      id: r.id,
      name: r.name,
      team_id: r.team_id ?? '',
      service_id: r.service_id ?? '',
      routing_key: '',
      pd_service_id: r.pd_service_id ?? '',
      has_key: r.has_key,
    })),
    mode: v.mode || 'primary',
    modes: { ...(v.modes ?? {}) },
    backup_min: v.backup_after_seconds ? String(v.backup_after_seconds / 60) : '',
    sync: {
      interval_seconds: v.sync?.interval_seconds ?? 0,
      from_email: v.sync?.from_email ?? '',
      notes: !!v.sync?.notes,
      priority: !!v.sync?.priority,
      on_call: !!v.sync?.on_call,
      queues: !!v.sync?.queues,
    },
  }
}

function bodyOf(d: Draft) {
  return {
    enabled: d.enabled,
    region: d.region,
    min_severity: d.min_severity,
    events_url: d.events_url,
    api_url: d.api_url,
    routing_key: d.routing_key,
    pd_service_id: d.pd_service_id,
    api_token: d.api_token,
    webhook_secret: d.webhook_secret,
    routes: d.routes.map((r) => ({ id: r.id, name: r.name, team_id: r.team_id, service_id: r.service_id, routing_key: r.routing_key, pd_service_id: r.pd_service_id })),
    mode: d.mode,
    modes: Object.fromEntries(Object.entries(d.modes).filter(([, m]) => m)),
    backup_after_seconds: d.backup_min.trim() === '' ? 0 : Math.round(Number(d.backup_min) * 60),
    sync: d.sync,
  }
}

// reloadKey changes when the Umbrella address is saved in its own card: the webhook address is
// read again, the unsaved form stays.
export function PagerDutyCard({ onSaved, reloadKey = 0 }: { onSaved?: () => void; reloadKey?: number }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { can, timezone } = useSession()
  const canEdit = can('settings.alerting:edit')
  const canTest = can('settings.alerting:test')
  const [view, setView] = useState<PagerDutyView | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [services, setServices] = useState<PDService[] | null>(null)
  // queuesKey reloads the queues after a synchronization.
  const [queuesKey, setQueuesKey] = useState(0)
  const refs = useResource<Refs>('/api/refs', 0)
  const loader = useAction(strings)
  const saver = useAction(strings)
  const other = useAction(strings)

  const apply = (v: PagerDutyView) => {
    setView(v)
    setDraft(draftOf(v))
  }
  const { run: load } = loader
  useEffect(() => {
    void load(async () => apply(await api<PagerDutyView>('GET', '/api/pagerduty')))
  }, [load])
  useEffect(() => {
    if (!reloadKey) return
    api<PagerDutyView>('GET', '/api/pagerduty').then(setView, () => {})
  }, [reloadKey])
  const hasToken = !!view?.has_api_token
  useEffect(() => {
    if (!hasToken || !canEdit) return
    api<PDService[]>('GET', '/api/pagerduty/services').then(setServices, () => setServices(null))
  }, [hasToken, canEdit])

  if (!view || !draft) {
    return (
      <ProfileCard title={t('pd.title')} action={loader} inline>
        {!loader.error && <p className="muted">{t('loading')}</p>}
      </ProfileCard>
    )
  }
  const at = (v?: string) => formatDate(v, locale, timezone)
  const dirty = JSON.stringify(bodyOf(draft)) !== JSON.stringify(bodyOf(draftOf(view)))
  const set = (p: Partial<Draft>) => setDraft({ ...draft, ...p })
  const moveRoute = (i: number, by: number) => {
    const next = [...draft.routes]
    const [r] = next.splice(i, 1)
    next.splice(i + by, 0, r)
    set({ routes: next })
  }
  const setSync = (p: Partial<PDSync>) => set({ sync: { ...draft.sync, ...p } })
  const usesBackup = draft.mode === 'backup' || Object.values(draft.modes).includes('backup')
  const routeName = (id: string) => (id === '' ? t('pd.oncall.default') : (view.routes.find((r) => r.id === id)?.name ?? id))
  const onCall = Object.entries(view.on_call ?? {}).filter(([, people]) => people.length > 0)
  const setRoute = (i: number, p: Partial<RouteDraft>) => set({ routes: draft.routes.map((r, j) => (j === i ? { ...r, ...p } : r)) })
  const st = view.status

  const save = () =>
    saver.run(async () => {
      apply(await api<PagerDutyView>('PUT', '/api/pagerduty', bodyOf(draft)))
      onSaved?.()
      return t('saved')
    })
  const call = (method: string, path: string, done: string, body?: unknown) =>
    other.run(async () => {
      const r = await api<PagerDutyView | unknown>(method, path, body)
      if (r && typeof r === 'object' && 'routes' in r) apply(r as PagerDutyView)
      return done
    })
  const syncNow = () =>
    other.run(async () => {
      const r = await api<{ applied: number }>('POST', '/api/pagerduty/sync')
      setView(await api<PagerDutyView>('GET', '/api/pagerduty'))
      setQueuesKey((k) => k + 1)
      return t('pd.sync.done', { n: r.applied })
    })

  const servicePicker = (value: string, onChange: (v: string) => void, label: string) =>
    services && services.length > 0 ? (
      <Field label={label}>
        {(id) => (
          <Select id={id} value={value} onChange={(e) => onChange(e.target.value)}>
            <option value="">{t('pd.service.manual')}</option>
            {services.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </Select>
        )}
      </Field>
    ) : null

  const stateKey = !view.enabled ? 'off' : st.breaker_open ? 'breaker' : 'on'
  const subscribed = !!(view.webhook_subscription_id || view.has_webhook_secret)
  const fallback = view.service_name || (view.has_routing_key ? t('pd.set') : '')
  const rows: [string, ReactNode][] = [
    [t('pd.mode'), t(`pd.mode.short.${view.mode || 'primary'}`)],
    [t('pd.sent'), `${st.sent} / ${st.failed}`],
    [t('pd.queue'), String(st.queue)],
    [t('pd.lastSuccess'), at(st.last_success_at)],
    [t('pd.lastWebhook'), at(st.last_webhook_at)],
    [t('pd.default'), fallback || t('pd.notset')],
    [t('pd.routes'), String(view.routes.length)],
    [t('pd.webhook'), t(subscribed ? 'pd.webhook.on' : 'pd.webhook.off')],
  ]
  if (view.has_api_token) rows.push([t('pd.sync.last'), at(st.last_sync_at)])
  if (st.last_sync_error) rows.push([t('pd.sync.error'), <span key="s" className="al-error">{st.last_sync_error}</span>])
  if (st.last_error) rows.push([t('pd.lastError'), <span key="e" className="al-error">{`${at(st.last_error_at)} · ${st.last_error}`}</span>])

  return (
    <>
      <SummaryCard
        title={t('pd.title')}
        badge={<span className={`pill pill-${stateKey === 'off' ? 'off' : stateKey === 'breaker' ? 'error' : 'ok'}`}>{t(`pd.state.${stateKey}`)}</span>}
        text={t('pd.text')}
        rows={rows}
        action={other}
        footer={
          canTest &&
          view.enabled && (
            <>
              <Button busy={other.busy} disabled={dirty} title={dirty ? t('nt.test.saveFirst') : undefined} onClick={() => void call('POST', '/api/pagerduty/test', t('pd.test.ok'))}>
                <Send size={15} aria-hidden />
                {t('pd.test')}
              </Button>
              {view.has_api_token && (
                <Button busy={other.busy} disabled={dirty} onClick={() => void syncNow()}>
                  <RefreshCw size={15} aria-hidden />
                  {t('pd.sync.now')}
                </Button>
              )}
            </>
          )
        }
      />
      <ProfileCard
        title={t('pd.settings')}
        action={saver}
        onSubmit={save}
        footer={
          canEdit && (
            <Button type="submit" variant="primary" busy={saver.busy} disabled={!dirty}>
              {t('save')}
            </Button>
          )
        }
      >
        <fieldset className="plain-fieldset stack" disabled={!canEdit}>
          <Switch checked={draft.enabled} onChange={(enabled) => set({ enabled })} label={t('pd.enable')} hint={t('pd.enable.hint')} />
        </fieldset>
        <AnimatePresence initial={false} mode="wait">
          {draft.enabled ? (
            <motion.div key="on" className="reveal-box" initial={{ opacity: 0, height: 0 }} animate={{ opacity: 1, height: 'auto' }} exit={{ opacity: 0, height: 0 }} transition={reveal}>
              <fieldset className="plain-fieldset stack" disabled={!canEdit}>
                <div className="grid-2">
                  <Field label={t('pd.region')}>
                    {(id) => (
                      <Select id={id} value={draft.region} onChange={(e) => set({ region: e.target.value as Draft['region'] })}>
                        <option value="us">{t('pd.region.us')}</option>
                        <option value="eu">{t('pd.region.eu')}</option>
                      </Select>
                    )}
                  </Field>
                  <Field label={t('pd.min')} hint={t('pd.min.hint')}>
                    {(id) => (
                      <Select id={id} value={draft.min_severity} onChange={(e) => set({ min_severity: e.target.value })}>
                        <option value="">{severityText(t, 'info')}</option>
                        {SEVERITIES.filter((s) => s !== 'info').map((s) => (
                          <option key={s} value={s}>
                            {severityText(t, s)}
                          </option>
                        ))}
                      </Select>
                    )}
                  </Field>
                </div>
                <div className="al-inline">
                  <Field label={t('pd.token')} hint={view.has_api_token && !draft.api_token ? t('keep') : t('pd.token.hint')}>
                    {(id) => <Password id={id} value={draft.api_token} autoComplete="off" onChange={(e) => set({ api_token: e.target.value })} />}
                  </Field>
                  {canTest && (draft.api_token || view.has_api_token) && (
                    <Button busy={other.busy} onClick={() => void call('POST', '/api/pagerduty/check', t('pd.token.ok'), { api_token: draft.api_token })}>
                      {t('pd.token.check')}
                    </Button>
                  )}
                </div>

                <section className="al-section">
                  <h3 className="al-sub">{t('pd.role')}</h3>
                  <p className="hint">{t('pd.role.hint')}</p>
                  <Field label={t('pd.mode')}>
                    {(id) => (
                      <Select id={id} value={draft.mode} onChange={(e) => set({ mode: e.target.value })}>
                        {PD_MODES.map((m) => (
                          <option key={m} value={m}>
                            {t(`pd.mode.${m}`)}
                          </option>
                        ))}
                      </Select>
                    )}
                  </Field>
                  <p className="hint">{t('pd.modes.hint')}</p>
                  <div className="al-modes">
                    {[...SEVERITIES].reverse().map((sev) => (
                      <Field key={sev} label={severityText(t, sev)}>
                        {(id) => (
                          <Select id={id} value={draft.modes[sev] ?? ''} onChange={(e) => set({ modes: { ...draft.modes, [sev]: e.target.value } })}>
                            <option value="">{t('pd.mode.same')}</option>
                            {PD_MODES.map((m) => (
                              <option key={m} value={m}>
                                {t(`pd.mode.short.${m}`)}
                              </option>
                            ))}
                          </Select>
                        )}
                      </Field>
                    ))}
                  </div>
                  {usesBackup && (
                    <Field label={t('pd.backupAfter')} hint={t('pd.backupAfter.hint')}>
                      {(id) => <Input id={id} type="number" min={0} max={1440} placeholder="5" value={draft.backup_min} onChange={(e) => set({ backup_min: e.target.value })} />}
                    </Field>
                  )}
                </section>

                <section className="al-section">
                  <h3 className="al-sub">{t('pd.default')}</h3>
                  <p className="hint">{t('pd.default.hint')}</p>
                  <div className="grid-2">
                    {servicePicker(draft.pd_service_id, (pd_service_id) => set({ pd_service_id, routing_key: '' }), t('pd.service'))}
                    {(!services || !draft.pd_service_id) && (
                      <Field label={t('pd.key')} hint={view.has_routing_key && !draft.routing_key ? t('keep') : undefined}>
                        {(id) => <Password id={id} value={draft.routing_key} autoComplete="off" onChange={(e) => set({ routing_key: e.target.value })} />}
                      </Field>
                    )}
                  </div>
                  {view.service_name && draft.pd_service_id === view.service_id && <p className="hint">{view.service_name}</p>}
                </section>

                <section className="al-section">
                  <h3 className="al-sub">{t('pd.routes')}</h3>
                  <p className="hint">{t('pd.routes.hint')}</p>
                  {draft.routes.map((r, i) => (
                    <div key={r.key} className="al-route">
                      <Field label={t('pd.route.name')}>{(id) => <Input id={id} value={r.name} onChange={(e) => setRoute(i, { name: e.target.value })} />}</Field>
                      <Field label={t('pd.route.service')}>
                        {(id) => (
                          <Select id={id} value={r.service_id} onChange={(e) => setRoute(i, { service_id: e.target.value })}>
                            <option value="">{t('pd.route.any')}</option>
                            {(refs.data?.services ?? []).map((s) => (
                              <option key={s.id} value={s.id}>
                                {s.name}
                              </option>
                            ))}
                          </Select>
                        )}
                      </Field>
                      <Field label={t('pd.route.team')}>
                        {(id) => (
                          <Select id={id} value={r.team_id} onChange={(e) => setRoute(i, { team_id: e.target.value })}>
                            <option value="">{t('pd.route.any')}</option>
                            {(refs.data?.teams ?? []).map((s) => (
                              <option key={s.id} value={s.id}>
                                {s.name}
                              </option>
                            ))}
                          </Select>
                        )}
                      </Field>
                      {servicePicker(r.pd_service_id, (pd_service_id) => setRoute(i, { pd_service_id, routing_key: '' }), t('pd.service'))}
                      {(!services || !r.pd_service_id) && (
                        <Field label={t('pd.key')} hint={r.has_key && !r.routing_key ? t('keep') : undefined}>
                          {(id) => <Password id={id} value={r.routing_key} autoComplete="off" onChange={(e) => setRoute(i, { routing_key: e.target.value })} />}
                        </Field>
                      )}
                      <div className="al-route-tools">
                        <span className="al-route-order" title={t('pd.route.order')}>
                          {i + 1}
                        </span>
                        <button type="button" className="icon-btn" onClick={() => moveRoute(i, -1)} disabled={i === 0} title={t('pd.route.up')} aria-label={t('pd.route.up')}>
                          <ArrowUp size={16} />
                        </button>
                        <button
                          type="button"
                          className="icon-btn"
                          onClick={() => moveRoute(i, 1)}
                          disabled={i === draft.routes.length - 1}
                          title={t('pd.route.down')}
                          aria-label={t('pd.route.down')}
                        >
                          <ArrowDown size={16} />
                        </button>
                        <button type="button" className="icon-btn" aria-label={t('remove')} title={t('remove')} onClick={() => set({ routes: draft.routes.filter((_, j) => j !== i) })}>
                          <Trash2 size={16} />
                        </button>
                      </div>
                    </div>
                  ))}
                  <div className="row">
                    <Button
                      onClick={() =>
                        set({ routes: [...draft.routes, { key: `new-${++seq}`, id: '', name: '', team_id: '', service_id: '', routing_key: '', pd_service_id: '', has_key: false }] })
                      }
                    >
                      <Plus size={16} aria-hidden />
                      {t('pd.route.add')}
                    </Button>
                  </div>
                </section>

                <section className="al-section">
                  <h3 className="al-sub">{t('pd.sync')}</h3>
                  <p className="hint">{t('pd.sync.hint')}</p>
                  <div className="grid-2">
                    <Field label={t('pd.sync.interval')}>
                      {(id) => (
                        <Select id={id} value={String(draft.sync.interval_seconds ?? 0)} onChange={(e) => setSync({ interval_seconds: Number(e.target.value) })}>
                          {SYNC_INTERVALS.map((v) => (
                            <option key={v} value={v}>
                              {t(`pd.sync.interval.${v}`)}
                            </option>
                          ))}
                        </Select>
                      )}
                    </Field>
                    <Field label={t('pd.sync.from')} hint={t('pd.sync.from.hint')}>
                      {(id) => <Input id={id} type="email" value={draft.sync.from_email ?? ''} onChange={(e) => setSync({ from_email: e.target.value })} />}
                    </Field>
                  </div>
                  <Switch checked={draft.sync.notes} onChange={(notes) => setSync({ notes })} label={t('pd.sync.notes')} hint={t('pd.sync.notes.hint')} />
                  <Switch checked={draft.sync.priority} onChange={(priority) => setSync({ priority })} label={t('pd.sync.priority')} hint={t('pd.sync.priority.hint')} />
                  <Switch checked={draft.sync.on_call} onChange={(on_call) => setSync({ on_call })} label={t('pd.sync.oncall')} hint={t('pd.sync.oncall.hint')} />
                  {view.sync?.on_call && (
                    <>
                      <h4 className="al-sub">{t('pd.oncall')}</h4>
                      {onCall.length === 0 ? (
                        <p className="hint">{t('pd.oncall.none')}</p>
                      ) : (
                        <Rows
                          rows={onCall.map(([route, people]) => [
                            routeName(route),
                            people
                              .map((p) => [p.name, p.until && t('pd.oncall.until', { at: at(p.until) }), !p.user_id && t('pd.oncall.notUser')].filter(Boolean).join(', '))
                              .join('; '),
                          ])}
                        />
                      )}
                    </>
                  )}
                </section>

                {view.has_api_token && (
                  <PDQueues view={view} queues={draft.sync.queues} onQueues={(queues) => setSync({ queues })} dirty={dirty} reloadKey={queuesKey + reloadKey} />
                )}

                <section className="al-section">
                  <h3 className="al-sub">{t('pd.webhook')}</h3>
                  <Rows
                    align="end"
                    rows={[
                      [t('pd.webhook.url'), view.webhook_url ? <code key="u">{view.webhook_url}</code> : <span className="muted">{t('pd.webhook.nourl')}</span>],
                      [t('state'), <span key="w" className={`pill pill-${subscribed ? 'ok' : 'off'}`}>{t(subscribed ? 'pd.webhook.on' : 'pd.webhook.off')}</span>],
                    ]}
                  />
                  {view.webhook_url && view.has_api_token && (
                    <div className="row">
                      <Button busy={other.busy} disabled={dirty} onClick={() => void call('POST', '/api/pagerduty/subscription', t('saved'))}>
                        {t('pd.webhook.subscribe')}
                      </Button>
                      {view.webhook_subscription_id && (
                        <Button busy={other.busy} disabled={dirty} onClick={() => void call('DELETE', '/api/pagerduty/subscription', t('saved'))}>
                          {t('pd.webhook.unsubscribe')}
                        </Button>
                      )}
                    </div>
                  )}
                  <Field label={t('pd.webhook.secret')} hint={view.has_webhook_secret && !draft.webhook_secret ? t('keep') : t('pd.webhook.secret.hint')}>
                    {(id) => <Password id={id} value={draft.webhook_secret} autoComplete="off" onChange={(e) => set({ webhook_secret: e.target.value })} />}
                  </Field>
                </section>
              </fieldset>
            </motion.div>
          ) : (
            <motion.div key="off" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} transition={{ duration: 0.2 }}>
              <Banner kind="info" title={t('pd.off')} />
            </motion.div>
          )}
        </AnimatePresence>
      </ProfileCard>
    </>
  )
}
