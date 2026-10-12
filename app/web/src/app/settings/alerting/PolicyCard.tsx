import { useEffect, useState } from 'react'
import { api } from '../../../api'
import { useT } from '../../../i18n'
import { Button, Field, Input } from '../../../ui'
import { ProfileCard } from '../../profile/ProfileCard'
import { useAction } from '../../profile/useAction'
import { useSession } from '../../session'
import { policyStrings } from './policyStrings'

// AlertPolicy is model.AlertPolicy: 0 or absent keeps the default of the engine.
type AlertPolicy = {
  reopen_window_seconds?: number
  fallback_delay_seconds?: number
  fallback_retry_seconds?: number
  retention_days?: number
  test_lifetime_seconds?: number
}
type View = { policy: AlertPolicy; defaults: Required<AlertPolicy>; limits: Required<AlertPolicy> }
type Key = keyof AlertPolicy

// FIELDS: each value of the policy, the unit the form shows it in (in units of the stored value).
const FIELDS: { key: Key; label: string; unit: number }[] = [
  { key: 'reopen_window_seconds', label: 'window', unit: 60 },
  { key: 'fallback_delay_seconds', label: 'delay', unit: 1 },
  { key: 'fallback_retry_seconds', label: 'retry', unit: 60 },
  { key: 'retention_days', label: 'retention', unit: 1 },
  { key: 'test_lifetime_seconds', label: 'test', unit: 60 },
]

type Draft = Record<Key, string>

function draftOf(p: AlertPolicy): Draft {
  const out = {} as Draft
  for (const f of FIELDS) out[f.key] = p[f.key] ? String(p[f.key]! / f.unit) : ''
  return out
}

function bodyOf(d: Draft): Required<AlertPolicy> {
  const out = {} as Required<AlertPolicy>
  for (const f of FIELDS) out[f.key] = Math.round(Number(d[f.key].replace(',', '.') || 0) * f.unit)
  return out
}

export function PolicyCard() {
  const t = useT(policyStrings)
  const { can } = useSession()
  const canEdit = can('settings.alerting:edit')
  const [saved, setSaved] = useState<View | null>(null)
  const [d, setD] = useState<Draft>(draftOf({}))
  const loader = useAction(policyStrings)
  const saver = useAction(policyStrings)
  const apply = (v: View) => {
    setSaved(v)
    setD(draftOf(v.policy ?? {}))
  }
  const { run: load } = loader
  useEffect(() => {
    void load(async () => apply(await api<View>('GET', '/api/alerting/policy')))
  }, [load])
  if (!saved) {
    return (
      <ProfileCard title={t('ap.title')} action={loader} inline>
        {!loader.error && <p className="muted">{t('loading')}</p>}
      </ProfileCard>
    )
  }
  const before = draftOf(saved.policy ?? {})
  const dirty = FIELDS.some((f) => d[f.key].trim() !== before[f.key])
  const save = () =>
    saver.run(async () => {
      apply(await api<View>('PUT', '/api/alerting/policy', bodyOf(d)))
      return t('saved')
    })
  return (
    <ProfileCard
      title={t('ap.title')}
      action={saver}
      onSubmit={save}
      footer={
        canEdit && (
          <Button type="submit" variant="primary" busy={saver.busy} disabled={!dirty}>
            {t('save')}
          </Button>
        )
      }
    >
      <p className="muted">{t('ap.text')}</p>
      <fieldset className="plain-fieldset grid-2" disabled={!canEdit}>
        {FIELDS.map((f) => (
          <Field key={f.key} label={t(`ap.${f.label}`)} hint={t(`ap.${f.label}.hint`)}>
            {(id) => (
              <Input
                id={id}
                inputMode="decimal"
                value={d[f.key]}
                placeholder={t('ap.default', { v: saved.defaults[f.key] / f.unit })}
                onChange={(e) => setD({ ...d, [f.key]: e.target.value.replace(/[^\d.,]/g, '') })}
              />
            )}
          </Field>
        ))}
      </fieldset>
    </ProfileCard>
  )
}
