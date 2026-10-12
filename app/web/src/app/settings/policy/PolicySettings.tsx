import { useCallback, useEffect, useState } from 'react'
import { api } from '../../../api'
import { useT } from '../../../i18n'
import { PolicyChecklist } from '../../../PolicyChecklist'
import { PolicyEditor } from '../../../PolicyEditor'
import { defaultPolicy, policyError, type PasswordPolicy } from '../../../policy'
import { Banner, Button, Field, Password } from '../../../ui'
import { ProfileCard } from '../../profile/ProfileCard'
import { SummaryCard } from '../../profile/SummaryCard'
import { useAction } from '../../profile/useAction'
import { useSession } from '../../session'
import { strings } from './strings'

type PolicySummary = { policy: PasswordPolicy; local_users: number; expired_users: number; expiring_users: number }

const samePolicy = (a: PasswordPolicy, b: PasswordPolicy) => JSON.stringify(a) === JSON.stringify(b)

export function PolicySettings() {
  const t = useT(strings)
  const { setPolicy, refresh, can } = useSession()
  const canEdit = can('settings.policy:edit')
  const [summary, setSummary] = useState<PolicySummary | null>(null)
  const [draft, setDraft] = useState<PasswordPolicy>(defaultPolicy)
  const [sample, setSample] = useState('')
  const loader = useAction()
  const saver = useAction()

  const apply = useCallback(
    (s: PolicySummary) => {
      setSummary(s)
      setDraft(s.policy)
      setPolicy(s.policy)
    },
    [setPolicy],
  )

  const { run: runLoad } = loader
  useEffect(() => {
    void runLoad(async () => apply(await api<PolicySummary>('GET', '/api/settings/password-policy')))
  }, [runLoad, apply])

  if (!summary) {
    return loader.error ? (
      <Banner kind="error" title={loader.error.message}>
        {loader.error.detail}
      </Banner>
    ) : null
  }

  const dirty = !samePolicy(draft, summary.policy)

  const save = () =>
    saver.run(async () => {
      apply(await api<PolicySummary>('PUT', '/api/settings/password-policy', draft))
      await refresh()
      return t('ps.saved')
    })

  return (
    <>
      <SummaryCard
        title={t('ps.title')}
        text={t('ps.text')}
        rows={[
          [t('ps.accounts'), String(summary.local_users)],
          [t('ps.expired'), String(summary.expired_users)],
          [t('ps.expiring'), String(summary.expiring_users)],
        ]}
      />
      <ProfileCard
        title={t('ps.rules')}
        action={saver}
        onSubmit={save}
        footer={
          canEdit && (
            <div className="row">
              <Button variant="ghost" onClick={() => setDraft(summary.policy)} disabled={!dirty || saver.busy}>
                {t('ps.reset')}
              </Button>
              <Button type="submit" variant="primary" busy={saver.busy} disabled={!dirty || policyError(draft) !== null}>
                {t('ps.save')}
              </Button>
            </div>
          )
        }
      >
        <fieldset className="plain-fieldset" disabled={!canEdit}>
          <PolicyEditor value={draft} onChange={setDraft} />
        </fieldset>
        <Banner kind="info" title={t('ps.applies')} />
      </ProfileCard>
      <ProfileCard title={t('ps.preview')}>
        <p className="muted">{t('ps.preview.text')}</p>
        <Field label={t('ps.sample')}>{(id) => <Password id={id} value={sample} onChange={(e) => setSample(e.target.value)} autoComplete="off" />}</Field>
        <PolicyChecklist policy={draft} password={sample} username="" t={t} />
      </ProfileCard>
    </>
  )
}
