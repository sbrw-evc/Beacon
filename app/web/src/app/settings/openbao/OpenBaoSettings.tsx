import { useState } from 'react'
import { ErrorBanner } from '../../../connections/ConnectionCard'
import { useResource } from '../../../connections/useRequest'
import { useT } from '../../../i18n'
import { CurrentConnection } from './CurrentConnection'
import { Migration } from './Migration'
import { useSession } from '../../session'
import { strings } from './strings'
import type { OpenBaoOverview } from './types'

export function OpenBaoSettings() {
  const t = useT(strings)
  const { can } = useSession()
  const [epoch, setEpoch] = useState(0)
  const overview = useResource<OpenBaoOverview>('/api/settings/openbao', epoch)
  if (!overview.data) {
    return overview.error ? <ErrorBanner error={overview.error} strings={strings} /> : <p className="muted">{t('loading')}</p>
  }
  return (
    <>
      <CurrentConnection overview={overview.data} />
      {can('settings.openbao:migrate') && <Migration current={overview.data} onMigrated={() => setEpoch((e) => e + 1)} />}
    </>
  )
}
