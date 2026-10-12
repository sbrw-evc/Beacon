import { useState } from 'react'
import { api } from '../../../api'
import { ConnectionCard, ErrorFlash } from '../../../connections/ConnectionCard'
import { useAction } from '../../../connections/useRequest'
import { useLocale, useT } from '../../../i18n'
import { formatDate } from '../../../ui'
import { useSession } from '../../session'
import { formatBytes } from './format'
import { strings } from './strings'
import type { PostgresOverview, PostgresTest } from './types'
import { Flash } from '../../../notify'

export function CurrentConnection({ overview, sizeBytes }: { overview: PostgresOverview; sizeBytes?: number }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone, can } = useSession()
  const test = useAction()
  const [result, setResult] = useState<PostgresTest | null>(null)
  const c = overview.connection
  const problem = overview.error || overview.persist.error
  const runTest = () =>
    void test.run(async () => {
      setResult(null)
      setResult(await api<PostgresTest>('POST', '/api/settings/postgres/test'))
    })
  return (
    <ConnectionCard
      title={t('pgs.current')}
      text={t('pgs.current.text')}
      ok={overview.ok && !overview.persist.error}
      problem={problem}
      onTest={can('settings.postgres:test') ? runTest : undefined}
      testing={test.busy}
      rows={[
        [t('pgs.host'), c.host],
        [t('pgs.port'), c.port],
        [t('pgs.database'), c.database],
        [t('pgs.user'), c.user],
        [t('pgs.sslmode'), c.sslmode],
        [t('pgs.version'), overview.info.version],
        [t('pgs.size'), sizeBytes === undefined ? '' : formatBytes(sizeBytes, locale, t)],
        [t('pgs.savedAt'), formatDate(overview.info.saved_at, locale, timezone)],
        [t('pgs.pending'), t(overview.persist.pending ? 'yes' : 'no')],
      ]}
    >
      <ErrorFlash error={test.error} strings={strings} />
      {result &&
        (result.ok ? (
          <Flash kind="ok" title={t('pgs.test.ok')} trigger={result}>
            {t('pgs.test.okText', { version: result.version ?? '?', latency: result.latency_ms })}
          </Flash>
        ) : (
          <Flash kind="error" title={t('pgs.test.fail')} trigger={result}>
            {result.error}
          </Flash>
        ))}
    </ConnectionCard>
  )
}
