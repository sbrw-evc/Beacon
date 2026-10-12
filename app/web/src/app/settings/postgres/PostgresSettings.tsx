import { useState } from 'react'
import { ErrorBanner } from '../../../connections/ConnectionCard'
import { useResource } from '../../../connections/useRequest'
import { useT } from '../../../i18n'
import { CurrentConnection } from './CurrentConnection'
import { Migration } from './Migration'
import { Statistics } from './Statistics'
import { useSession } from '../../session'
import { strings } from './strings'
import type { PostgresOverview, PostgresStats } from './types'
import './postgres.css'

export function PostgresSettings() {
  const t = useT(strings)
  const { can } = useSession()
  const [epoch, setEpoch] = useState(0)
  const overview = useResource<PostgresOverview>('/api/settings/postgres', epoch)
  const stats = useResource<PostgresStats>('/api/settings/postgres/stats', epoch)
  if (!overview.data) {
    return overview.error ? <ErrorBanner error={overview.error} strings={strings} /> : <p className="muted">{t('loading')}</p>
  }
  return (
    <>
      <CurrentConnection overview={overview.data} sizeBytes={stats.data?.database.size_bytes} />
      <Statistics stats={stats.data} error={stats.error} busy={stats.busy} reload={stats.reload} />
      {can('settings.postgres:migrate') && <Migration current={overview.data.where} onMigrated={() => setEpoch((e) => e + 1)} />}
    </>
  )
}
