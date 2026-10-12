import { useState } from 'react'
import { api } from '../../../api'
import { ConnectionCard, ErrorFlash } from '../../../connections/ConnectionCard'
import type { OpenBaoReport } from '../../../connections/openbao'
import { useAction } from '../../../connections/useRequest'
import { useLocale, useT } from '../../../i18n'
import { formatDate } from '../../../ui'
import { useSession } from '../../session'
import { strings } from './strings'
import type { OpenBaoOverview } from './types'
import { Flash } from '../../../notify'

export function CurrentConnection({ overview }: { overview: OpenBaoOverview }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone, can } = useSession()
  const test = useAction()
  const [result, setResult] = useState<OpenBaoReport | null>(null)
  const c = overview.connection
  const s = overview.status
  const tls = c.skip_verify ? t('obs.tls.skip') : c.custom_ca ? t('obs.tls.ca') : t('obs.tls.default')
  const runTest = () =>
    void test.run(async () => {
      setResult(null)
      setResult(await api<OpenBaoReport>('POST', '/api/settings/openbao/test'))
    })
  return (
    <ConnectionCard
      title={t('obs.current')}
      text={t('obs.current.text')}
      ok={s.token_ok === true && s.mount_ok === true}
      problem={s.error || overview.list_error}
      onTest={can('settings.openbao:test') ? runTest : undefined}
      testing={test.busy}
      rows={[
        [t('obs.addr'), c.addr],
        [t('obs.version'), s.version],
        [t('obs.mount'), c.mount],
        [t('obs.namespace'), c.namespace],
        [t('obs.auth'), c.auth === 'approle' ? t('obs.auth.approle', { path: c.approle_path || 'approle' }) : t('obs.auth.token')],
        [t('obs.tls'), c.addr.startsWith('https://') ? tls : ''],
        [t('obs.expires'), s.token_ok ? formatDate(s.token_expires, locale, timezone) || t('obs.never') : ''],
        [t('obs.renewable'), s.token_ok ? t(s.renewable ? 'yes' : 'no') : ''],
        [t('obs.policies'), s.policies?.join(', ')],
        [t('obs.secrets'), overview.list_error ? '' : overview.secrets],
        ...(s.last_error ? ([[t('obs.lastError'), `${s.last_error} (${formatDate(s.last_error_at, locale, timezone)})`]] as [string, string][]) : []),
      ]}
    >
      <ErrorFlash error={test.error} strings={strings} />
      {result &&
        (result.ok ? (
          <Flash kind="ok" title={t('obs.test.ok')} trigger={result}>
            {t('obs.test.okText', { mount: c.mount })}
          </Flash>
        ) : (
          <Flash kind="error" title={t('obs.test.fail')} trigger={result}>
            {result.error}
          </Flash>
        ))}
    </ConnectionCard>
  )
}
