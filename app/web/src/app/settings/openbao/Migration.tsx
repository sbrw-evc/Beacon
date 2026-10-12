import { useState } from 'react'
import { api } from '../../../api'
import type { Check } from '../../../connections/CheckResult'
import { ErrorFlash, MigrationActions, MigrationCard } from '../../../connections/ConnectionCard'
import { MigrateConfirm } from '../../../connections/MigrateConfirm'
import { openBaoBody, openBaoCheckKey, openBaoDraft, openBaoMount } from '../../../connections/openbao'
import { OpenBaoCheckResult, OpenBaoForm } from '../../../connections/OpenBaoForm'
import { useAction } from '../../../connections/useRequest'
import { useT } from '../../../i18n'
import { Banner, Switch } from '../../../ui'
import { strings } from './strings'
import type { OpenBaoMigrated, OpenBaoOverview, OpenBaoTargetReport } from './types'
import { Flash } from '../../../notify'

export function Migration({ current, onMigrated }: { current: OpenBaoOverview; onMigrated: () => void }) {
  const t = useT(strings)
  const [draft, setDraft] = useState(openBaoDraft)
  const [check, setCheck] = useState<Check<OpenBaoTargetReport>>(null)
  const [overwrite, setOverwrite] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const [done, setDone] = useState<OpenBaoMigrated | null>(null)
  const action = useAction()
  const key = openBaoCheckKey(draft)
  const report = check?.ok && check.key === key ? check.result : undefined
  const ready = report !== undefined && (report.secrets === 0 || overwrite)

  const runCheck = () =>
    void action.run(async () => {
      setDone(null)
      const r = await api<OpenBaoTargetReport>('POST', '/api/settings/openbao/probe', openBaoBody(draft))
      setCheck({ key, ok: r.ok, result: r, error: r.error })
      setOverwrite(false)
    })

  const migrate = () =>
    void action.run(async () => {
      const r = await api<OpenBaoMigrated>('POST', '/api/settings/openbao/migrate', { target: openBaoBody(draft), overwrite })
      setConfirming(false)
      setDone(r)
      setDraft(openBaoDraft())
      setCheck(null)
      onMigrated()
    })

  return (
    <MigrationCard title={t('obs.move')} text={t('obs.move.text')}>
      <OpenBaoForm value={draft} onChange={setDraft} />
      <OpenBaoCheckResult check={check} draft={draft} rows={(r) => [[t('obs.move.targetSecrets'), r.secrets]]}>
        {(r) =>
          r.secrets > 0 && (
            <Banner kind="warn" title={t('obs.move.notEmpty', { count: r.secrets })}>
              <div className="stack">
                <p>{t('obs.move.notEmptyText')}</p>
                <Switch checked={overwrite} onChange={setOverwrite} label={t('obs.move.overwrite')} />
              </div>
            </Banner>
          )
        }
      </OpenBaoCheckResult>
      <ErrorFlash error={action.error} strings={strings} />
      {done && (
        <Flash kind="ok" title={t('obs.move.done', { addr: done.addr, mount: done.mount })}>
          {t('obs.move.doneText', { copied: done.copied, refs: done.refs })}
        </Flash>
      )}
      <MigrationActions
        onCheck={runCheck}
        canCheck={draft.addr.trim() !== ''}
        onMigrate={() => {
          action.clear()
          setConfirming(true)
        }}
        canMigrate={ready}
        busy={action.busy}
      />
      <MigrateConfirm open={confirming} title={t('obs.move.confirm')} busy={action.busy} onClose={() => setConfirming(false)} onConfirm={migrate}>
        <ol className="conn-list">
          <li>
            {t('obs.move.step1', {
              count: current.secrets,
              from: `${current.connection.addr} (${current.connection.mount})`,
              to: `${draft.addr.trim()} (${openBaoMount(draft)})`,
            })}
          </li>
          <li>{t('obs.move.step2')}</li>
          <li>{t('obs.move.step3')}</li>
          <li>{t('obs.move.step4')}</li>
        </ol>
        {report && report.secrets > 0 && <Banner kind="warn" title={t('obs.move.notEmpty', { count: report.secrets })} />}
      </MigrateConfirm>
    </MigrationCard>
  )
}
