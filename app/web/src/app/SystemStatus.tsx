import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { Pencil, RefreshCw } from 'lucide-react'
import { api, ApiError, type Locale, type Theme } from '../api'
import { defaultPolicy, ruleText, rules, type PasswordPolicy } from '../policy'
import { errorText, useLocale, useT } from '../i18n'
import { Banner, Button, formatDate, Rows, zoneLabel } from '../ui'
import { DefaultsEditor } from './DefaultsEditor'
import { OnboardingChecklist } from './onboarding/OnboardingChecklist'
import { useSession } from './session'
import { statusStrings } from './statusStrings'
import { strings } from './strings'

type LogEntry = { at: string; level: string; message: string; attrs?: Record<string, string> }

type Status = {
  checked_at: string
  version: string
  build: { version: string; commit?: string; built_at?: string; modified?: boolean; go_version: string; platform: string }
  runtime: {
    started_at: string
    uptime_seconds: number
    hostname?: string
    pid: number
    cpus: number
    gomaxprocs: number
    goroutines: number
    memory: { alloc_bytes: number; heap_inuse_bytes: number; sys_bytes: number; num_gc: number; last_gc?: string; gc_pause_total_ms: number }
  }
  openbao: {
    configured: boolean
    addr?: string
    mount?: string
    auth?: string
    namespace?: string
    reachable: boolean
    sealed: boolean
    version?: string
    cluster_name?: string
    token_ok: boolean
    mount_ok: boolean
    renewable: boolean
    token_expires?: string
    policies?: string[]
    error?: string
    last_error?: string
    last_error_at?: string
    latency_ms: number
  }
  postgres: {
    where: string
    ok: boolean
    latency_ms: number
    info: { version?: string; saved_at?: string }
    health?: {
      database: string
      user: string
      size_bytes: number
      started_at?: string
      connections: number
      max_connections: number
      ssl: boolean
      pool: { total: number; idle: number; acquired: number; max: number; acquires: number; empty_waits: number; acquire_avg_ms: number }
    }
    persist: { pending: boolean; error?: string }
    error?: string
  }
  ldap: { enabled: boolean; ok: boolean; kind?: string; url?: string; tls?: string; base_dn?: string; admin_group?: string; latency_ms: number; error?: string }
  entra: {
    enabled: boolean
    cloud?: string
    tenant_id?: string
    client_id?: string
    redirect_url?: string
    admin_group_id?: string
    user_group_id?: string
    issuer?: string
    credentials: boolean
    ok: boolean
    latency_ms: number
    error?: string
  }
  intake: {
    ready: boolean
    connectors: number
    published: number
    invalid: number
    stats?: {
      pending: number
      oldest_pending?: string
      open_failures: number
      received_24h: number
      rejected_24h: number
      failed_24h: number
      events_24h: number
      duplicates_24h: number
      latency_avg_ms: number
      latency_max_ms: number
      last_received?: string
    }
    error?: string
  }
  settings: { default_theme: Theme; default_locale: Locale; default_timezone: string; password_policy?: PasswordPolicy; setup_at?: string; setup_by?: string }
  users: Record<string, number>
  inventory: {
    users: Record<string, number>
    disabled_users: number
    sessions: number
    signed_in_users: number
    roles: number
    teams: number
    services: number
    credentials: number
    audit_entries: number
  }
  logs: { counts: Record<string, number>; recent: LogEntry[] }
}

type State = 'ok' | 'warn' | 'error' | 'off'
type T = (k: string, vars?: Record<string, string | number>) => string

function Pill({ state, t, label }: { state: State; t: T; label?: string }) {
  return <span className={`pill pill-${state}`}>{label ?? t(`state.${state}`)}</span>
}

function Card({ title, state, label, t, wide, children }: { title: string; state?: State; label?: string; t: T; wide?: boolean; children: ReactNode }) {
  return (
    <section className={`card status-card ${wide ? 'span-all' : ''}`} aria-label={title}>
      <header>
        <h2>{title}</h2>
        {state && <Pill state={state} t={t} label={label} />}
      </header>
      {children}
    </section>
  )
}

function bytes(n: number, locale: string) {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${n.toLocaleString(locale, { maximumFractionDigits: i === 0 ? 0 : 1 })} ${units[i]}`
}

function duration(sec: number, t: T) {
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const parts: string[] = []
  if (d) parts.push(`${d} ${t('unit.d')}`)
  if (d || h) parts.push(`${h} ${t('unit.h')}`)
  if (!d) parts.push(`${m} ${t('unit.min')}`)
  if (!d && !h) parts.push(`${Math.floor(sec % 60)} ${t('unit.s')}`)
  return parts.join(' ')
}

const ms = (n: number, t: T) => `${n} ${t('unit.ms')}`

export function SystemStatus() {
  const dict = useMemo(() => ({ en: { ...strings.en, ...statusStrings.en }, ru: { ...strings.ru, ...statusStrings.ru } }), [])
  const t = useT(dict)
  const { timezone: tz, expire: onExpired, can, setDefaultTz } = useSession()
  const { locale } = useLocale()
  const [s, setS] = useState<Status | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<{ message: string; detail?: string } | null>(null)
  const [editing, setEditing] = useState(false)
  const [epoch, setEpoch] = useState(0)
  const closeEditor = useCallback(() => setEditing(false), [])

  const load = useCallback(async () => {
    setEpoch((e) => e + 1)
    setBusy(true)
    setError(null)
    try {
      setS(await api<Status>('GET', '/api/system'))
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) onExpired()
      setError(errorText(t, e))
    } finally {
      setBusy(false)
    }
  }, [t, onExpired])

  useEffect(() => {
    void load()
  }, [load])

  const date = (v?: string) => formatDate(v, locale, tz)
  const num = (n: number) => n.toLocaleString(locale)

  const head = (
    <div className="page-head">
      <div>
        <h1>{t('status.title')}</h1>
        <p className="muted">{t('status.subtitle')}</p>
      </div>
      <Button onClick={load} busy={busy}>
        {!busy && <RefreshCw size={16} />}
        {t('status.refresh')}
      </Button>
    </div>
  )
  const failed = error && (
    <Banner kind="error" title={error.message}>
      {error.detail}
    </Banner>
  )
  if (!s) return (
    <>
      {head}
      {failed}
    </>
  )

  const { build: b, runtime: rt, openbao: ob, postgres: pg, ldap: ld, entra: en, intake: ik, inventory: inv, logs } = s
  const obState: State = ob.token_ok && ob.mount_ok ? 'ok' : 'error'
  const pgState: State = !pg.ok || pg.persist.error ? 'error' : pg.persist.pending ? 'warn' : 'ok'
  const ldState: State = !ld.enabled ? 'off' : ld.ok ? 'ok' : 'error'
  const enState: State = !en.enabled ? 'off' : en.ok ? 'ok' : 'error'
  const ikState: State = !ik.ready || ik.error ? 'error' : ik.invalid > 0 || (ik.stats?.open_failures ?? 0) > 0 ? 'warn' : 'ok'
  const errors = logs.counts.error ?? 0
  const warnings = logs.counts.warn ?? 0
  const logState: State = errors > 0 ? 'error' : warnings > 0 ? 'warn' : 'ok'
  const problems = [obState, pgState, ldState, enState, ikState].filter((x) => x === 'error').length
  const total = Object.values(inv.users).reduce((a, n) => a + n, 0)
  const st = ik.stats
  const h = pg.health
  const policy = s.settings.password_policy ?? defaultPolicy

  return (
    <>
      {head}
      {failed}
      <div className="status-summary">
        <Pill state={problems ? 'error' : 'ok'} t={t} label={problems ? t('status.problems', { n: problems }) : t('status.allOk')} />
        <span className="muted">{t('status.checkedAt', { at: date(s.checked_at) })}</span>
      </div>
      <div className="status-onboarding">
        <OnboardingChecklist epoch={epoch} />
      </div>
      <div className="cards">
        <Card title={t('card.app')} t={t}>
          <Rows
            rows={[
              [t('field.appVersion'), b.version],
              [
                t('field.commit'),
                b.commit && (
                  <>
                    <code title={b.commit}>{b.commit.slice(0, 12)}</code>
                    {b.modified && <span className="muted"> {t('field.commit.modified')}</span>}
                  </>
                ),
              ],
              [t('field.builtAt'), date(b.built_at) || b.built_at],
              [t('field.go'), b.go_version],
              [t('field.platform'), b.platform],
              [t('field.startedAt'), date(rt.started_at)],
              [t('field.uptime'), duration(rt.uptime_seconds, t)],
              [t('field.host'), rt.hostname],
              [t('field.pid'), rt.pid],
            ]}
          />
        </Card>
        <Card title={t('card.runtime')} t={t}>
          <Rows
            rows={[
              [t('field.cpus'), `${rt.cpus} (${rt.gomaxprocs})`],
              [t('field.goroutines'), num(rt.goroutines)],
              [t('field.memAlloc'), bytes(rt.memory.heap_inuse_bytes, locale)],
              [t('field.memSys'), bytes(rt.memory.sys_bytes, locale)],
              [t('field.gc'), t('field.gc.value', { n: num(rt.memory.num_gc), ms: num(rt.memory.gc_pause_total_ms) })],
              [t('field.lastGC'), date(rt.memory.last_gc)],
            ]}
          />
        </Card>
        <Card title="OpenBao" state={obState} t={t}>
          <Rows
            rows={[
              [t('field.address'), ob.addr],
              [t('field.version'), ob.version],
              [t('field.cluster'), ob.cluster_name],
              [t('field.namespace'), ob.namespace],
              [t('field.mount'), ob.mount],
              [t('field.auth'), ob.auth === 'approle' ? 'AppRole' : ob.auth ? t('field.auth.token') : ''],
              [t('field.sealed'), ob.reachable ? t(ob.sealed ? 'yes' : 'no') : ''],
              [t('field.policies'), ob.policies?.join(', ')],
              [t('field.tokenExpires'), date(ob.token_expires)],
              [t('field.renewable'), ob.token_ok ? t(ob.renewable ? 'yes' : 'no') : ''],
              [t('field.latency'), ms(ob.latency_ms, t)],
              [t('field.lastError'), ob.last_error && `${ob.last_error} (${date(ob.last_error_at)})`],
            ]}
          />
          {ob.error && <Banner kind="error" title={ob.error} />}
        </Card>
        <Card title="PostgreSQL" state={pgState} t={t}>
          <Rows
            rows={[
              [t('field.database'), pg.where],
              [t('field.version'), pg.info.version],
              [t('field.dbUser'), h?.user],
              [t('field.dbSize'), h && bytes(h.size_bytes, locale)],
              [t('field.pgStarted'), date(h?.started_at)],
              [t('field.connections'), h && t('field.connections.value', { n: h.connections, max: h.max_connections })],
              [t('field.ssl'), h && t(h.ssl ? 'yes' : 'no')],
              [t('field.pool'), h && t('field.pool.value', { ...h.pool })],
              [t('field.poolWait'), h && t('field.poolWait.value', { waits: num(h.pool.empty_waits), acquires: num(h.pool.acquires), ms: h.pool.acquire_avg_ms })],
              [t('field.latency'), pg.where ? ms(pg.latency_ms, t) : ''],
              [t('field.savedAt'), date(pg.info.saved_at)],
              [t('field.persist'), pg.persist.error ? '' : t(pg.persist.pending ? 'field.persist.pending' : 'field.persist.ok')],
            ]}
          />
          {(pg.error || pg.persist.error) && <Banner kind="error" title={pg.error || pg.persist.error} />}
        </Card>
        <Card title="LDAP / AD" state={ldState} t={t}>
          {ld.enabled ? (
            <Rows
              rows={[
                [t('field.directory'), ld.kind === 'ad' ? 'Active Directory' : 'OpenLDAP'],
                [t('field.address'), ld.url],
                [t('field.tls'), ld.tls === 'ldaps' ? 'LDAPS' : ld.tls === 'starttls' ? 'StartTLS' : t('field.tls.none')],
                [t('field.baseDN'), ld.base_dn],
                [t('field.adminGroup'), ld.admin_group],
                [t('field.latency'), ms(ld.latency_ms, t)],
              ]}
            />
          ) : (
            <p className="muted">{t('ldap.off')}</p>
          )}
          {ld.error && <Banner kind="error" title={ld.error} />}
        </Card>
        <Card title={t('card.entra')} state={enState} t={t}>
          {en.enabled ? (
            <Rows
              rows={[
                [t('field.cloud'), en.cloud && t(`cloud.${en.cloud}`)],
                [t('field.tenant'), en.tenant_id],
                [t('field.clientId'), en.client_id],
                [t('field.redirect'), en.redirect_url],
                [t('field.issuer'), en.issuer],
                [t('field.secret'), en.issuer ? t(en.credentials ? 'field.secret.ok' : 'field.secret.bad') : ''],
                [t('field.adminGroup'), en.admin_group_id],
                [t('field.userGroup'), en.user_group_id],
                [t('field.latency'), ms(en.latency_ms, t)],
              ]}
            />
          ) : (
            <p className="muted">{t('entra.off')}</p>
          )}
          {en.error && <Banner kind="error" title={en.error} />}
        </Card>
        <Card title={t('card.intake')} state={ikState} label={ikState === 'ok' ? t('state.ready') : undefined} t={t}>
          <Rows
            rows={[
              [t('field.connectors'), t('field.connectors.value', { total: ik.connectors, published: ik.published, invalid: ik.invalid })],
              [t('field.queue'), st && num(st.pending)],
              [t('field.oldest'), date(st?.oldest_pending)],
              [t('field.openFailures'), st && num(st.open_failures)],
              [
                t('field.received'),
                st && t('field.received.value', { received: num(st.received_24h), rejected: num(st.rejected_24h), failed: num(st.failed_24h), duplicates: num(st.duplicates_24h) }),
              ],
              [t('field.events'), st && num(st.events_24h)],
              [t('field.procLatency'), st && t('field.procLatency.value', { avg: st.latency_avg_ms, max: st.latency_max_ms })],
              [t('field.lastReceived'), date(st?.last_received)],
            ]}
          />
          {!ik.ready && <Banner kind="error" title={t('intake.notReady')} />}
          {ik.error && <Banner kind="error" title={ik.error} />}
        </Card>
        <Card title={t('card.inventory')} t={t}>
          <Rows
            rows={[
              [t('field.users'), t('field.users.value', { total, local: inv.users.local ?? 0, ldap: inv.users.ldap ?? 0, entra: inv.users.entra ?? 0 })],
              [t('field.disabledUsers'), inv.disabled_users],
              [t('field.sessions'), t('field.sessions.value', { sessions: inv.sessions, users: inv.signed_in_users })],
              [t('field.roles'), inv.roles],
              [t('field.teams'), inv.teams],
              [t('field.services'), inv.services],
              [t('field.credentials'), inv.credentials],
              [t('field.audit'), num(inv.audit_entries)],
            ]}
          />
        </Card>
        <Card title={t('settings.title')} t={t}>
          <Rows
            rows={[
              [t('field.theme'), s.settings.default_theme && t(`theme.${s.settings.default_theme}`)],
              [t('field.locale'), s.settings.default_locale && t(`lang.${s.settings.default_locale}`)],
              [t('field.timezone'), zoneLabel(s.settings.default_timezone)],
              [
                t('field.passwordPolicy'),
                rules(policy)
                  .filter((r) => r !== 'username')
                  .map((r) => ruleText(t, r, policy))
                  .join('; '),
              ],
              [t('field.setupAt'), date(s.settings.setup_at)],
              [t('field.setupBy'), s.settings.setup_by],
            ]}
          />
          {can('status:defaults') && (
            <div className="card-actions">
              <Button onClick={() => setEditing(true)}>
                <Pencil size={16} />
                {t('defaults.edit')}
              </Button>
            </div>
          )}
        </Card>
        <Card title={t('card.logs')} state={logState} t={t} wide>
          <p className="muted">
            {logs.recent.length === 0 ? t('logs.none') : t('logs.counts', { errors: num(errors), warnings: num(warnings), n: logs.recent.length })}
          </p>
          {logs.recent.length > 0 && (
            <ul className="status-logs">
              {logs.recent.map((e, i) => (
                <li key={i}>
                  <div className="status-log-head">
                    <Pill state={e.level === 'error' ? 'error' : 'warn'} t={t} label={t(`logs.level.${e.level === 'error' ? 'error' : 'warn'}`)} />
                    <span className="muted">{date(e.at)}</span>
                  </div>
                  <div className="status-log-msg">{e.message}</div>
                  {e.attrs && (
                    <div className="status-log-attrs">
                      {Object.entries(e.attrs).map(([k, v]) => (
                        <code key={k}>
                          {k}={v}
                        </code>
                      ))}
                    </div>
                  )}
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>
      <DefaultsEditor
        open={editing}
        onClose={closeEditor}
        initial={s.settings}
        onSaved={(next) => {
          setS({ ...s, settings: { ...s.settings, ...next } })
          setDefaultTz(next.default_timezone)
        }}
      />
    </>
  )
}
