import { useState } from 'react'
import { api } from '../../../api'
import type { Check } from '../../../connections/CheckResult'
import { ErrorFlash, MigrationActions, MigrationCard } from '../../../connections/ConnectionCard'
import { MigrateConfirm } from '../../../connections/MigrateConfirm'
import { postgresBody, postgresCheckKey, postgresComplete, postgresDraft, postgresUsable, type PostgresReport } from '../../../connections/postgres'
import { PostgresCheckResult, PostgresForm } from '../../../connections/PostgresForm'
import { useAction } from '../../../connections/useRequest'
import { useT } from '../../../i18n'
import { Banner, Switch } from '../../../ui'
import { strings } from './strings'
import type { PostgresMigrated } from './types'
import { Flash } from '../../../notify'

export function Migration({ current, onMigrated }: { current: string; onMigrated: () => void }) {
  const t = useT(strings)
  const [draft, setDraft] = useState(postgresDraft)
  const [check, setCheck] = useState<Check<PostgresReport>>(null)
  const [overwrite, setOverwrite] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const [done, setDone] = useState<string | null>(null)
  const action = useAction()
  const key = postgresCheckKey(draft)
  const probe = check?.ok && check.key === key ? check.result?.probe : undefined
  const ready = probe !== undefined && postgresUsable(probe) && (!probe.has_state || overwrite)
  const where = `${draft.user.trim()}@${draft.host.trim()}:${draft.port}/${draft.database.trim()}`

  const runCheck = () =>
    void action.run(async () => {
      setDone(null)
      const r = await api<PostgresReport>('POST', '/api/settings/postgres/probe', postgresBody(draft))
      setCheck({ key, ok: r.ok, result: r, error: r.error })
      setOverwrite(false)
    })

  const migrate = () =>
    void action.run(async () => {
      const r = await api<PostgresMigrated>('POST', '/api/settings/postgres/migrate', { target: postgresBody(draft), overwrite })
      setConfirming(false)
      setDone(r.where)
      setDraft(postgresDraft())
      setCheck(null)
      onMigrated()
    })

  return (
    <MigrationCard title={t('pgs.move')} text={t('pgs.move.text')}>
      <PostgresForm value={draft} onChange={setDraft} />
      <PostgresCheckResult
        check={check}
        draft={draft}
        state={
          <div className="stack">
            <p>{t('pgs.move.overwriteText')}</p>
            <Switch checked={overwrite} onChange={setOverwrite} label={t('pgs.move.overwrite')} />
          </div>
        }
      />
      <ErrorFlash error={action.error} strings={strings} />
      {done && (
        <Flash kind="ok" title={t('pgs.move.done', { where: done })}>
          {t('pgs.move.doneText')}
        </Flash>
      )}
      <MigrationActions
        onCheck={runCheck}
        canCheck={postgresComplete(draft)}
        onMigrate={() => {
          action.clear()
          setConfirming(true)
        }}
        canMigrate={ready}
        busy={action.busy}
      />
      <MigrateConfirm open={confirming} title={t('pgs.move.confirm')} busy={action.busy} onClose={() => setConfirming(false)} onConfirm={migrate}>
        <ol className="conn-list">
          <li>{t('pgs.move.step1', { where })}</li>
          {draft.password !== '' && <li>{t('pgs.move.step2')}</li>}
          <li>{t('pgs.move.step3')}</li>
          <li>{t('pgs.move.step4', { current })}</li>
        </ol>
        {probe?.has_state && <Banner kind="warn" title={t('pgs.move.replace')} />}
      </MigrateConfirm>
    </MigrationCard>
  )
}
