import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { RefreshCw } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { api } from '../../api'
import { CheckResult, type Check } from '../../connections/CheckResult'
import { useLocale, useT } from '../../i18n'
import { Banner, Button, Field, formatDate, Input, Password, Select, Stepper, Switch } from '../../ui'
import { ProfileCard } from '../profile/ProfileCard'
import { SummaryCard } from '../profile/SummaryCard'
import { useAction } from '../profile/useAction'
import { useSession } from '../session'
import { strings } from './strings'
import './netbox.css'
import { Flash } from '../../notify'
import { SetupGuide } from '../guide/SetupGuide'

type NetBoxConfig = {
  enabled: boolean
  url: string
  skip_verify: boolean
  sync_minutes: number
  import_devices: boolean
  import_vms: boolean
  import_services: boolean
  sync_contacts: boolean
  create_users: boolean
  sync_directory: boolean
  site_id: number
  device_role_id: number
  device_type_id: number
  cluster_id: number
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
    contacts: number
    users_created: number
    users_updated: number
    users_linked: number
    users_skipped?: number
    directory_checked: boolean
    directory_matched: number
    directory_missing: number
    directory_error?: string
    held?: number
    held_reason?: 'empty' | 'share'
  }
}

type NetBoxView = {
  config: NetBoxConfig
  token_set: boolean
  sync: SyncState
  running: boolean
  next_sync_at?: string
  directory_available: boolean
  items: number
  users: number
}

type Probe = { version: string; devices: number; vms: number; services: number; contacts: number }
type Report = { ok: boolean; error?: string; probe: Probe }
type Option = { id: number; name: string }
type Choices = { sites: Option[]; device_roles: Option[]; device_types: Option[]; clusters: Option[] }

const NEVER = '0001-01-01T00:00:00Z'
const ran = (at?: string) => !!at && !at.startsWith('0001-')

function sameUrl(a: string, b: string) {
  const norm = (u: string) => u.trim().replace(/\/+$/, '').replace(/\/api$/, '').toLowerCase()
  return norm(a) === norm(b)
}

function checkKey(c: NetBoxConfig, token: string) {
  return JSON.stringify({ url: c.url.trim(), skip: c.skip_verify, token })
}

export function NetBoxPage() {
  const t = useT(strings)
  const { can } = useSession()
  const [view, setView] = useState<NetBoxView | null>(null)
  const [draft, setDraft] = useState<NetBoxConfig | null>(null)
  const [token, setToken] = useState('')
  const [check, setCheck] = useState<Check<Report>>(null)
  const loader = useAction(strings)
  const saver = useAction(strings)
  const tester = useAction(strings)

  const apply = useCallback((v: NetBoxView) => {
    setView(v)
    setDraft(v.config)
    setToken('')
  }, [])

  const { run: runLoad } = loader
  const load = useCallback(() => runLoad(async () => apply(await api<NetBoxView>('GET', '/api/netbox'))), [runLoad, apply])
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

  const canEdit = can('netbox:edit')
  const canTest = can('netbox:test')
  const saved = view.config
  const reusable = view.token_set && sameUrl(draft.url, saved.url)
  const tokenReady = token.trim() !== '' || reusable
  const urlReady = /^https?:\/\/[^/\s]+/i.test(draft.url.trim())
  const dirty = JSON.stringify(draft) !== JSON.stringify(saved) || token !== ''
  const set = (patch: Partial<NetBoxConfig>) => setDraft({ ...draft, ...patch })

  const test = () =>
    tester.run(async () => {
      const key = checkKey(draft, token)
      const r = await api<Report>('POST', '/api/netbox/test', { config: { ...draft, enabled: true }, token })
      setCheck({ key, ok: r.ok, result: r, error: r.error })
    })

  const save = () =>
    saver.run(async () => {
      apply(await api<NetBoxView>('PUT', '/api/netbox', { config: draft, token }))
      setCheck(null)
      return t('nb.saved')
    })

  const tokenHint = token ? t('nb.token.hint') : view.token_set && !reusable ? t('nb.token.again') : reusable ? t('nb.token.keep') : t('nb.token.hint')

  return (
    <>
      <SetupGuide page="netbox" />
      <SyncCard view={view} onSynced={apply} />
      <ProfileCard
        title={t('nb.settings')}
        action={saver}
        onSubmit={save}
        footer={
          <div className="row nb-actions">
            {draft.enabled && <span className="hint">{t('nb.save.hint')}</span>}
            {draft.enabled && canTest && (
              <Button onClick={test} busy={tester.busy} disabled={!urlReady || !tokenReady}>
                {tester.busy ? t('nb.checking') : t('nb.check')}
              </Button>
            )}
            {canEdit && (
              <Button type="submit" variant="primary" busy={saver.busy} disabled={!dirty || (draft.enabled && (!urlReady || !tokenReady))}>
                {t('nb.save')}
              </Button>
            )}
          </div>
        }
      >
        <fieldset className="plain-fieldset" disabled={!canEdit}>
          <Switch checked={draft.enabled} onChange={(v) => set({ enabled: v })} label={t('nb.enable')} hint={t('nb.enable.hint')} />
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
                  <Field label={t('nb.url')} hint={t('nb.url.hint')}>
                    {(id) => (
                      <Input
                        id={id}
                        value={draft.url}
                        placeholder="https://netbox.example.com"
                        spellCheck={false}
                        autoComplete="off"
                        onChange={(e) => set({ url: e.target.value })}
                      />
                    )}
                  </Field>
                  <Field label={t('nb.token')} hint={tokenHint}>
                    {(id) => <Password id={id} value={token} autoComplete="new-password" onChange={(e) => setToken(e.target.value)} />}
                  </Field>
                  <Switch checked={draft.skip_verify} onChange={(v) => set({ skip_verify: v })} label={t('nb.skipVerify')} hint={t('nb.skipVerify.hint')} />
                </fieldset>
                <fieldset className="plain-fieldset stack" disabled={!canEdit}>
                  <Field label={t('nb.interval')} hint={t('nb.interval.hint')}>
                    {(id) => (
                      <div className="nb-stepper">
                        <Stepper id={id} value={draft.sync_minutes} min={0} max={10080} suffix={t('nb.minutes')} onChange={(v) => set({ sync_minutes: v })} />
                      </div>
                    )}
                  </Field>
                  <div className="nb-group">
                    <span className="nb-group-title">{t('nb.import')}</span>
                    <Switch checked={draft.import_devices} onChange={(v) => set({ import_devices: v })} label={t('nb.import.devices')} />
                    <Switch checked={draft.import_vms} onChange={(v) => set({ import_vms: v })} label={t('nb.import.vms')} />
                    <Switch checked={draft.import_services} onChange={(v) => set({ import_services: v })} label={t('nb.import.services')} />
                  </div>
                  <Switch checked={draft.sync_contacts} onChange={(v) => set({ sync_contacts: v })} label={t('nb.contacts')} hint={t('nb.contacts.hint')} />
                  {draft.sync_contacts && (
                    <Switch
                      checked={draft.create_users}
                      onChange={(v) => set({ create_users: v })}
                      label={t('nb.createUsers')}
                      hint={t(draft.create_users ? 'nb.createUsers.on' : 'nb.createUsers.off')}
                    />
                  )}
                  <Switch
                    checked={draft.sync_directory}
                    onChange={(v) => set({ sync_directory: v })}
                    label={t('nb.directory')}
                    hint={view.directory_available ? t('nb.directory.hint') : `${t('nb.directory.hint')} ${t('nb.directory.none')}`}
                  />
                </fieldset>
                <CheckResult
                  check={check}
                  currentKey={checkKey(draft, token)}
                  failTitle={t('nb.fail')}
                  ok={(r) => <Flash kind="ok" title={t('nb.ok')} trigger={r}>{t('nb.ok.text', { ...r.probe, version: r.probe.version || '?' })}</Flash>}
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
              <Banner kind="info" title={t('nb.off')} />
            </motion.div>
          )}
        </AnimatePresence>
      </ProfileCard>
      {saved.enabled && view.token_set && <DefaultsCard view={view} onSaved={apply} />}
    </>
  )
}

function When({ at }: { at?: string }) {
  const { locale } = useLocale()
  const { timezone } = useSession()
  return <>{ran(at) ? formatDate(at, locale, timezone) : '—'}</>
}

function SyncCard({ view, onSynced }: { view: NetBoxView; onSynced: (v: NetBoxView) => void }) {
  const t = useT(strings)
  const { can } = useSession()
  const syncer = useAction(strings)
  const { sync } = view
  const s = sync.stats
  const on = view.config.enabled && view.token_set

  const run = (confirm = false) =>
    syncer.run(async () => {
      const st = await api<SyncState>('POST', confirm ? '/api/netbox/sync?confirm=removal' : '/api/netbox/sync')
      onSynced(await api<NetBoxView>('GET', '/api/netbox'))
      return st.ok ? t('nb.sync.done') : undefined
    })

  const rows: [string, ReactNode][] = [
    [t('nb.state'), <span className={`pill pill-${on ? 'ok' : 'off'}`}>{t(on ? 'nb.state.on' : 'nb.state.off')}</span>],
  ]
  if (view.config.url) rows.push([t('nb.url'), <code key="u">{view.config.url}</code>])
  rows.push([t('nb.token'), t(view.token_set ? 'nb.token.set' : 'nb.token.unset')])
  rows.push([t('nb.items'), String(view.items)], [t('nb.users'), String(view.users)])
  if (on) rows.push([t('nb.sync.next'), view.config.sync_minutes > 0 ? <When key="n" at={view.next_sync_at} /> : t('nb.sync.manual')])
  if (ran(sync.started_at)) {
    rows.push(
      [
        t('nb.sync.last'),
        <span key="l">
          <When at={sync.finished_at || sync.started_at || NEVER} /> <span className="muted">{t('nb.sync.by', { actor: sync.actor })}</span>
        </span>,
      ],
      [t('nb.sync.result'), <span className={`pill pill-${sync.ok ? 'ok' : 'error'}`}>{t(sync.ok ? 'nb.sync.ok' : 'nb.sync.failed')}</span>],
    )
    if (sync.ok) {
      rows.push(
        [
          t('nb.sync.objects'),
          t('nb.sync.objects.value', { objects: s.objects, created: s.created, updated: s.updated, deleted: s.deleted, unlinked: s.unlinked }),
        ],
        [t('nb.sync.users'), t('nb.sync.users.value', { contacts: s.contacts, created: s.users_created, updated: s.users_updated, linked: s.users_linked }) +
          (s.users_skipped ? t('nb.sync.users.skipped', { n: s.users_skipped }) : '')],
        [
          t('nb.sync.directory'),
          s.directory_error ? (
            <span key="d" className="nb-error">
              {s.directory_error}
            </span>
          ) : s.directory_checked ? (
            t('nb.sync.directory.value', { matched: s.directory_matched, missing: s.directory_missing })
          ) : (
            t('nb.sync.directory.off')
          ),
        ],
      )
    }
  }

  return (
    <SummaryCard
      title={t('nb.sync')}
      text={t('nb.state.text')}
      rows={rows}
      action={syncer}
      footer={
        on &&
        can('netbox:sync') && (
          <Button variant="primary" onClick={() => run()} busy={syncer.busy || view.running}>
            <RefreshCw size={15} />
            {syncer.busy || view.running ? t('nb.sync.running') : t('nb.sync.now')}
          </Button>
        )
      }
    >
      {!ran(sync.started_at) && on && <p className="hint">{t('nb.sync.never')}</p>}
      {ran(sync.started_at) && !sync.ok && sync.error && <Banner kind="error" title={t('nb.sync.failed')}>{sync.error}</Banner>}
      {ran(sync.started_at) && sync.ok && (s.held ?? 0) > 0 && (
        <Banner kind="warn" title={t('nb.sync.held', { count: s.held ?? 0 })}>
          <div className="stack">
            <p>{t(s.held_reason === 'empty' ? 'nb.sync.held.empty' : 'nb.sync.held.share')}</p>
            {on && can('netbox:sync') && (
              <div>
                <Button variant="secondary" onClick={() => run(true)} busy={syncer.busy || view.running}>
                  {t('nb.sync.held.confirm', { count: s.held ?? 0 })}
                </Button>
              </div>
            )}
          </div>
        </Banner>
      )}
    </SummaryCard>
  )
}

function DefaultsCard({ view, onSaved }: { view: NetBoxView; onSaved: (v: NetBoxView) => void }) {
  const t = useT(strings)
  const { can } = useSession()
  const loader = useAction(strings)
  const saver = useAction(strings)
  const [choices, setChoices] = useState<Choices | null>(null)
  const [ids, setIds] = useState(() => pick(view.config))
  const canEdit = can('netbox:edit')

  const { run: runLoad } = loader
  useEffect(() => {
    void runLoad(async () => setChoices(await api<Choices>('GET', '/api/netbox/choices')))
  }, [runLoad])
  useEffect(() => setIds(pick(view.config)), [view.config])

  const dirty = JSON.stringify(ids) !== JSON.stringify(pick(view.config))
  const save = () =>
    saver.run(async () => {
      onSaved(await api<NetBoxView>('PUT', '/api/netbox', { config: { ...view.config, ...ids }, token: '' }))
      return t('nb.saved')
    })

  const select = (key: keyof typeof ids, label: string, options: Option[] | undefined) => (
    <Field label={label}>
      {(id) => (
        <Select id={id} value={String(ids[key] || '')} disabled={!canEdit || !options} onChange={(e) => setIds({ ...ids, [key]: Number(e.target.value) || 0 })}>
          <option value="">{t('nb.pick')}</option>
          {options?.map((o) => (
            <option key={o.id} value={o.id}>
              {o.name}
            </option>
          ))}
          {ids[key] > 0 && options && !options.some((o) => o.id === ids[key]) && <option value={ids[key]}>#{ids[key]}</option>}
        </Select>
      )}
    </Field>
  )

  return (
    <ProfileCard
      title={t('nb.defaults')}
      action={saver}
      onSubmit={save}
      footer={
        canEdit && (
          <Button type="submit" variant="primary" busy={saver.busy} disabled={!dirty}>
            {t('nb.defaults.save')}
          </Button>
        )
      }
    >
      <p className="muted">{t('nb.defaults.text')}</p>
      {loader.error && (
        <Banner kind="error" title={loader.error.message}>
          {loader.error.detail}
        </Banner>
      )}
      <div className="grid-2">
        {select('site_id', t('nb.site'), choices?.sites)}
        {select('cluster_id', t('nb.cluster'), choices?.clusters)}
        {select('device_role_id', t('nb.deviceRole'), choices?.device_roles)}
        {select('device_type_id', t('nb.deviceType'), choices?.device_types)}
      </div>
    </ProfileCard>
  )
}

function pick(c: NetBoxConfig) {
  return { site_id: c.site_id, device_role_id: c.device_role_id, device_type_id: c.device_type_id, cluster_id: c.cluster_id }
}
