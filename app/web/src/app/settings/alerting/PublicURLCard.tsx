import { useEffect, useState } from 'react'
import { api } from '../../../api'
import { useT } from '../../../i18n'
import { Button, Field, Input } from '../../../ui'
import { ProfileCard } from '../../profile/ProfileCard'
import { useAction } from '../../profile/useAction'
import { useSession } from '../../session'
import { strings } from './strings'

type PublicURL = { public_url: string }

// PublicURLCard is the address people and PagerDuty reach Umbrella at. Links in e-mail, Telegram
// and PagerDuty (acknowledge, open the incident, Grafana) and the PagerDuty webhook use it.
export function PublicURLCard({ onSaved }: { onSaved?: () => void }) {
  const t = useT(strings)
  const { can } = useSession()
  const canEdit = can('settings.alerting:edit')
  const [saved, setSaved] = useState<string | null>(null)
  const [draft, setDraft] = useState('')
  const loader = useAction(strings)
  const saver = useAction(strings)
  const apply = (v: PublicURL) => {
    setSaved(v.public_url)
    // Not set yet: suggest the address this page was opened at.
    setDraft(v.public_url || window.location.origin)
  }
  const { run: load } = loader
  useEffect(() => {
    void load(async () => apply(await api<PublicURL>('GET', '/api/settings/public-url')))
  }, [load])

  if (saved === null) {
    return (
      <ProfileCard title={t('pub.title')} action={loader} inline>
        {!loader.error && <p className="muted">{t('loading')}</p>}
      </ProfileCard>
    )
  }
  const value = draft.trim().replace(/\/+$/, '')
  const dirty = value !== saved
  const save = () =>
    saver.run(async () => {
      apply(await api<PublicURL>('PUT', '/api/settings/public-url', { public_url: value }))
      onSaved?.()
      return t('saved')
    })
  return (
    <ProfileCard
      title={t('pub.title')}
      badge={<span className={`pill pill-${saved ? 'ok' : 'warn'}`}>{t(saved ? 'pub.set' : 'pub.unset')}</span>}
      action={saver}
      onSubmit={save}
      footer={
        canEdit && (
          <Button type="submit" variant="primary" busy={saver.busy} disabled={!dirty || !value}>
            {t('save')}
          </Button>
        )
      }
    >
      <p className="muted">{t('pub.text')}</p>
      <fieldset className="plain-fieldset" disabled={!canEdit}>
        <Field label={t('pub.field')} hint={!saved && value === window.location.origin ? t('pub.suggested') : t('pub.hint')}>
          {(id) => <Input id={id} value={draft} placeholder="https://umbrella.example.com" spellCheck={false} onChange={(e) => setDraft(e.target.value)} />}
        </Field>
      </fieldset>
    </ProfileCard>
  )
}
