import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { RefreshCw } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { api } from '../../api'
import { CheckResult, type Check } from '../../connections/CheckResult'
import { useLocale, useT } from '../../i18n'
import { Banner, Button, Field, formatDate, Input, Password, Stepper, Switch } from '../../ui'
import { Flash } from '../../notify'
import { ProfileCard } from '../profile/ProfileCard'
import { SummaryCard } from '../profile/SummaryCard'
import { useAction } from '../profile/useAction'
import { useSession } from '../session'
import { strings } from './strings'
import '../netbox/netbox.css'

type InventoryConfig = {
  enabled: boolean
  url: string
  integration_id: string
  skip_verify: boolean
  sync_minutes: number
  import_devices: boolean
  push_status: boolean
}

type SyncState = {
  started_at: string
  finished_at: string
  ok: boolean
  error?: string
  actor: string
  stats: {
    objects: number
    created: number
    updated: number
    deleted: number
    unlinked: number
    linked?: number
    held?: number
    held_reason?: 'empty' | 'share'
  }
}

type InventoryView = {
  config: InventoryConfig
  token_set: boolean
  secret_set: boolean
  sync: SyncState
  running: boolean
  items: number
  pushed: number
  next_sync_at?: string
  public_url_set: boolean
}

type Report = { ok: boolean; error?: string; probe: { items: number } }

const ran = (at?: string) => !!at && !at.startsWith('0001-')

const norm = (u: string) => u.trim().replace(/\/+$/, '').replace(/\/api(\/v1)?$/, '').toLowerCase()
const sameTarget = (a: InventoryConfig, b: InventoryConfig) => norm(a.url) === norm(b.url) && a.integration_id.trim() === b.integration_id.trim()

function checkKey(c: InventoryConfig, token: string) {
  return JSON.stringify({ url: c.url.trim(), id: c.integration_id.trim(), skip: c.skip_verify, token })
}

export function InventoryDBPage() {
  const t = useT(strings)
  const { can } = useSession()
  const [view, setView] = useState<InventoryView | null>(null)
  const [draft, setDraft] = useState<InventoryConfig | null>(null)
  const [token, setToken] = useState('')
  const [secret, setSecret] = useState('')
  const [check, setCheck] = useState<Check<Report>>(null)
  const loader = useAction(strings)
  const saver = useAction(strings)
  const tester = useAction(strings)

  const apply = useCallback((v: InventoryView) => {
    setView(v)
    setDraft(v.config)
    setToken('')
    setSecret('')
  }, [])

  const { run: runLoad } = loader
  const load = useCallback(() => runLoad(async () => apply(await api<InventoryView>('GET', '/api/inventory-db'))), [runLoad, apply])
  useEffect(() => {
    void load()
  }, [load])

  if (!view || !draft) {
    return loader.error ? (
      <Banner kind="error" title={loader.error.message}>
        {loader.error.detail}
      </Banner>
    ) : null
  }

  const canEdit = can('inventorydb:edit')
  const canTest = can('inventorydb:test')
  const saved = view.config
  const same = sameTarget(draft, saved)
  const tokenReady = token.trim() !== '' || (view.token_set && same)
  const secretReady = !draft.push_status || secret.trim() !== '' || (view.secret_set && same)
  const urlReady = /^https?:\/\/[^/\s]+/i.test(draft.url.trim()) && draft.integration_id.trim() !== ''
  const dirty = JSON.stringify(draft) !== JSON.stringify(saved) || token !== '' || secret !== ''
  const set = (patch: Partial<InventoryConfig>) => setDraft({ ...draft, ...patch })

  const test = () =>
    tester.run(async () => {
      const key = checkKey(draft, token)
      const r = await api<Report>('POST', '/api/inventory-db/test', { config: { ...draft, enabled: true }, token })
      setCheck({ key, ok: r.ok, result: r, error: r.error })
    })

  const save = () =>
    saver.run(async () => {
      apply(await api<InventoryView>('PUT', '/api/inventory-db', { config: draft, token, secret }))
      setCheck(null)
      return t('inv.saved')
    })

  const keepOr = (set: boolean, keep: string, hint: string) => (set && same ? keep : set ? t('inv.token.again') : hint)

  return (
    <>
      <SyncCard view={view} onSynced={apply} />
      <ProfileCard
        title={t('inv.settings')}
        action={saver}
        onSubmit={save}
        footer={
          <div className="row nb-actions">
            {draft.enabled && <span className="hint">{t('inv.save.hint')}</span>}
            {draft.enabled && canTest && (
              <Button onClick={test} busy={tester.busy} disabled={!urlReady || !tokenReady}>
                {tester.busy ? t('inv.checking') : t('inv.check')}
              </Button>
            )}
            {canEdit && (
              <Button type="submit" variant="primary" busy={saver.busy} disabled={!dirty || (draft.enabled && (!urlReady || !tokenReady || !secretReady))}>
                {t('inv.save')}
              </Button>
            )}
          </div>
        }
      >
        <fieldset className="plain-fieldset" disabled={!canEdit}>
          <Switch checked={draft.enabled} onChange={(v) => set({ enabled: v })} label={t('inv.enable')} hint={t('inv.enable.hint')} />
        </fieldset>
        <AnimatePresence initial={false} mode="wait">
          {draft.enabled ? (
            <motion.div
              key="on"
              className="reveal-box"
              initial={{ opacity: 0, height: 0 }}
              animate={{ opacity: 1, height: 'auto' }}
              exit={{ opacity: 0, height: 0 }}
              transition={{ duration: 0.28, ease: [0.22, 1, 0.36, 1] }}
            >
              <div className="stack">
                <fieldset className="plain-fieldset stack" disabled={!canEdit && !canTest}>
                  <Field label={t('inv.url')} hint={t('inv.url.hint')}>
                    {(id) => (
                      <Input id={id} value={draft.url} placeholder="https://inventory.example.com" spellCheck={false} autoComplete="off" onChange={(e) => set({ url: e.target.value })} />
                    )}
                  </Field>
                  <Field label={t('inv.integration')} hint={t('inv.integration.hint')}>
                    {(id) => <Input id={id} value={draft.integration_id} placeholder="int_…" spellCheck={false} autoComplete="off" onChange={(e) => set({ integration_id: e.target.value })} />}
                  </Field>
                  <Field label={t('inv.token')} hint={token ? t('inv.token.hint') : keepOr(view.token_set, t('inv.token.keep'), t('inv.token.hint'))}>
                    {(id) => <Password id={id} value={token} autoComplete="new-password" onChange={(e) => setToken(e.target.value)} />}
                  </Field>
                  <Switch checked={draft.skip_verify} onChange={(v) => set({ skip_verify: v })} label={t('inv.skipVerify')} hint={t('inv.skipVerify.hint')} />
                </fieldset>
                <fieldset className="plain-fieldset stack" disabled={!canEdit}>
                  <Switch checked={draft.import_devices} onChange={(v) => set({ import_devices: v })} label={t('inv.import')} hint={t('inv.import.hint')} />
                  {draft.import_devices && (
                    <Field label={t('inv.interval')} hint={t('inv.interval.hint')}>
                      {(id) => (
                        <div className="nb-stepper">
                          <Stepper id={id} value={draft.sync_minutes} min={0} max={10080} suffix={t('inv.minutes')} onChange={(v) => set({ sync_minutes: v })} />
                        </div>
                      )}
                    </Field>
                  )}
                  <Switch checked={draft.push_status} onChange={(v) => set({ push_status: v })} label={t('inv.push')} hint={t('inv.push.hint')} />
                  {draft.push_status && (
                    <Field label={t('inv.secret')} hint={secret ? t('inv.secret.hint') : keepOr(view.secret_set, t('inv.secret.keep'), t('inv.secret.hint'))}>
                      {(id) => <Password id={id} value={secret} autoComplete="new-password" onChange={(e) => setSecret(e.target.value)} />}
                    </Field>
                  )}
                  {draft.push_status && !view.public_url_set && <Banner kind="info" title={t('inv.push.noPublicUrl')} />}
                </fieldset>
                <CheckResult
                  check={check}
                  currentKey={checkKey(draft, token)}
                  failTitle={t('inv.fail')}
                  ok={(r) => (
                    <Flash kind="ok" title={t('inv.ok')} trigger={r}>
                      {t('inv.ok.text', { items: r.probe.items })}
                    </Flash>
                  )}
                />
                {tester.error && (
                  <Flash kind="error" title={tester.error.message}>
                    {tester.error.detail}
                  </Flash>
                )}
              </div>
            </motion.div>
          ) : (
            <motion.div key="off" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} transition={{ duration: 0.2 }}>
              <Banner kind="info" title={t('inv.off')} />
            </motion.div>
          )}
        </AnimatePresence>
      </ProfileCard>
    </>
  )
}

function When({ at }: { at?: string }) {
  const { locale } = useLocale()
  const { timezone } = useSession()
  return <>{ran(at) ? formatDate(at, locale, timezone) : '—'}</>
}

function SyncCard({ view, onSynced }: { view: InventoryView; onSynced: (v: InventoryView) => void }) {
  const t = useT(strings)
  const { can } = useSession()
  const syncer = useAction(strings)
  const { sync } = view
  const s = sync.stats
  const on = view.config.enabled && view.token_set
  const reads = on && view.config.import_devices

  const run = (confirm = false) =>
    syncer.run(async () => {
      const st = await api<SyncState>('POST', confirm ? '/api/inventory-db/sync?confirm=removal' : '/api/inventory-db/sync')
      onSynced(await api<InventoryView>('GET', '/api/inventory-db'))
      return st.ok ? t('inv.sync.done') : undefined
    })

  const rows: [string, ReactNode][] = [[t('inv.state'), <span className={`pill pill-${on ? 'ok' : 'off'}`}>{t(on ? 'inv.state.on' : 'inv.state.off')}</span>]]
  if (view.config.url) rows.push([t('inv.url'), <code key="u">{view.config.url}</code>])
  if (view.config.integration_id) rows.push([t('inv.integration'), <code key="i">{view.config.integration_id}</code>])
  rows.push([t('inv.token'), t(view.token_set ? 'inv.token.set' : 'inv.token.unset')])
  rows.push([t('inv.items'), String(view.items)])
  if (on && view.config.push_status) rows.push([t('inv.pushed'), String(view.pushed)])
  if (reads) rows.push([t('inv.sync.next'), view.config.sync_minutes > 0 ? <When key="n" at={view.next_sync_at} /> : t('inv.sync.manual')])
  if (ran(sync.started_at)) {
    rows.push(
      [
        t('inv.sync.last'),
        <span key="l">
          <When at={sync.finished_at || sync.started_at} /> <span className="muted">{t('inv.sync.by', { actor: sync.actor })}</span>
        </span>,
      ],
      [t('inv.sync.result'), <span className={`pill pill-${sync.ok ? 'ok' : 'error'}`}>{t(sync.ok ? 'inv.sync.ok' : 'inv.sync.failed')}</span>],
    )
    if (sync.ok) {
      rows.push([
        t('inv.sync.objects'),
        t('inv.sync.objects.value', { objects: s.objects, created: s.created, updated: s.updated, linked: s.linked ?? 0, deleted: s.deleted, unlinked: s.unlinked }),
      ])
    }
  }

  return (
    <SummaryCard
      title={t('inv.sync')}
      text={t('inv.state.text')}
      rows={rows}
      action={syncer}
      footer={
        reads &&
        can('inventorydb:sync') && (
          <Button variant="primary" onClick={() => run()} busy={syncer.busy || view.running}>
            <RefreshCw size={15} />
            {syncer.busy || view.running ? t('inv.sync.running') : t('inv.sync.now')}
          </Button>
        )
      }
    >
      {!ran(sync.started_at) && reads && <p className="hint">{t('inv.sync.never')}</p>}
      {ran(sync.started_at) && !sync.ok && sync.error && <Banner kind="error" title={t('inv.sync.failed')}>{sync.error}</Banner>}
      {ran(sync.started_at) && sync.ok && (s.held ?? 0) > 0 && (
        <Banner kind="warn" title={t('inv.sync.held', { count: s.held ?? 0 })}>
          <div className="stack">
            <p>{t(s.held_reason === 'empty' ? 'inv.sync.held.empty' : 'inv.sync.held.share')}</p>
            {reads && can('inventorydb:sync') && (
              <div>
                <Button variant="secondary" onClick={() => run(true)} busy={syncer.busy || view.running}>
                  {t('inv.sync.held.confirm', { count: s.held ?? 0 })}
                </Button>
              </div>
            )}
          </div>
        </Banner>
      )}
    </SummaryCard>
  )
}
