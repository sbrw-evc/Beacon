import { useState, type ReactNode } from 'react'
import { ExternalLink, RefreshCw } from 'lucide-react'
import { api } from '../../../api'
import { ErrorBanner, ErrorFlash } from '../../../connections/ConnectionCard'
import { useAction, useResource } from '../../../connections/useRequest'
import { useLocale, useT } from '../../../i18n'
import { Banner, Button, formatDate } from '../../../ui'
import { Flash } from '../../../notify'
import { SummaryCard } from '../../profile/SummaryCard'
import { useSession } from '../../session'
import { strings } from './strings'
import type { SpaView } from './types'

// minutes turns a Go duration such as "5m0s" into minutes.
function minutes(d: string) {
  const m = /^(?:(\d+)h)?(?:(\d+)m)?/.exec(d)
  return (Number(m?.[1] ?? 0) * 60 + Number(m?.[2] ?? 0)) || 5
}

export function SpaSettings() {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone, can } = useSession()
  const [epoch, setEpoch] = useState(0)
  const res = useResource<SpaView>('/api/settings/spa', epoch)
  const sync = useAction()
  const [synced, setSynced] = useState<SpaView | null>(null)
  const v = synced ?? res.data
  if (!v) return res.error ? <ErrorBanner error={res.error} strings={strings} /> : <p className="muted">{t('loading')}</p>
  const date = (s?: string) => formatDate(s, locale, timezone)

  const runSync = () =>
    void sync.run(async () => {
      const next = await api<SpaView>('POST', '/api/settings/spa/sync')
      setSynced(next)
      setEpoch((e) => e + 1)
    })

  const pill = (ok: boolean, label: string) => <span className={`pill pill-${ok ? 'ok' : 'error'}`}>{label}</span>
  const syncOK = !!v.last_sync && v.ok

  return (
    <>
      <SummaryCard
        title={t('spa.connection')}
        badge={pill(v.connected, v.connected ? 'Keystone' : t('spa.notConnected'))}
        text={t('spa.connection.text')}
        rows={[
          [t('spa.issuer'), v.issuer],
          [t('spa.app'), v.app_name],
          [t('spa.client'), v.client_id],
          [t('spa.key'), v.key_source ? t(`spa.key.${v.key_source}`) : ''],
          [t('spa.redirect'), <code key="r">{v.redirect_uri}</code>],
          [t('spa.signout'), <code key="s">{v.post_logout_redirect_uri}</code>],
          [t('spa.methods'), v.methods.map((m) => m.title).join(', ')],
        ]}
        footer={
          v.account_url && (
            <a className="btn" href={v.issuer} target="_blank" rel="noreferrer">
              <ExternalLink size={16} aria-hidden />
              Keystone
            </a>
          )
        }
      >
        {v.key_error && <Banner kind="error" title={v.key_error} />}
        {!v.connected && !v.key_error && (
          <Banner kind="warn" title={t('spa.notConnected')}>
            {t('spa.notConnected.text')}
          </Banner>
        )}
      </SummaryCard>

      {v.connected && (
        <SummaryCard
          title={t('spa.sync')}
          badge={v.last_sync ? pill(syncOK, syncOK ? 'OK' : '!') : undefined}
          text={t('spa.sync.text', { interval: t('spa.minutes', { n: minutes(v.interval) }) })}
          rows={[
            [t('spa.sync.last'), v.last_sync ? date(v.last_sync) : t('spa.sync.never')],
            [t('spa.sync.result'), v.last_sync ? t('spa.sync.result.value', { users: v.users, created: v.created, bound: v.bound, updated: v.updated, disabled: v.disabled }) : ''],
            [t('spa.people'), t('spa.people.value', { spa: v.counts.spa, disabled: v.counts.disabled, legacy: v.counts.legacy })],
            [t('spa.conflicts'), v.conflicts.join(', ')],
            [t('spa.noRole'), v.no_role.join(', ')],
          ]}
          footer={
            can('settings.spa:sync') && (
              <Button onClick={runSync} busy={sync.busy}>
                {!sync.busy && <RefreshCw size={16} aria-hidden />}
                {t('spa.sync.now')}
              </Button>
            )
          }
        >
          {v.error && <Banner kind="error" title={v.error} />}
          <ErrorFlash error={sync.error} strings={strings} />
          {synced && !sync.error && v.ok && <Flash kind="ok" title={t('spa.sync.done')} trigger={synced} />}
        </SummaryCard>
      )}

      {v.connected && (
        <SummaryCard
          title={t('spa.catalogue')}
          text={t('spa.catalogue.text')}
          rows={[
            [t('spa.catalogue.at'), date(v.catalog_at)],
            [t('spa.catalogue.count'), v.catalog_roles || ''],
            ...v.roles.map((r) => [<code key={r.id}>{r.id}</code>, r.name] as [ReactNode, ReactNode]),
          ]}
        >
          {v.catalog_error && <Banner kind="error" title={v.catalog_error} />}
        </SummaryCard>
      )}
    </>
  )
}
