import { useEffect, useState } from 'react'
import { api } from '../../../api'
import { useT } from '../../../i18n'
import { Button, Field, Input } from '../../../ui'
import { ProfileCard } from '../../profile/ProfileCard'
import { useAction } from '../../profile/useAction'
import { useSession } from '../../session'
import { strings } from './strings'

type Flow = { url: string }

// FlowCard is the address of Current: with it the incident and CI cards link to the
// traffic around the incident (/go/incidents/{id}/flow, /go/cis/{id}/flow).
export function FlowCard() {
  const t = useT(strings)
  const { can } = useSession()
  const canEdit = can('settings.alerting:edit')
  const [saved, setSaved] = useState<Flow | null>(null)
  const [url, setURL] = useState('')
  const loader = useAction(strings)
  const saver = useAction(strings)
  const apply = (f: Flow) => {
    setSaved(f)
    setURL(f.url ?? '')
  }
  const { run: load } = loader
  useEffect(() => {
    void load(async () => apply(await api<Flow>('GET', '/api/flow')))
  }, [load])
  if (!saved) {
    return (
      <ProfileCard title={t('fl.title')} action={loader} inline>
        {!loader.error && <p className="muted">{t('loading')}</p>}
      </ProfileCard>
    )
  }
  const dirty = url.trim().replace(/\/+$/, '') !== (saved.url ?? '')
  const save = () =>
    saver.run(async () => {
      apply(await api<Flow>('PUT', '/api/flow', { url: url.trim() }))
      return t('saved')
    })
  return (
    <ProfileCard
      title={t('fl.title')}
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
      <p className="muted">{t('fl.text')}</p>
      <fieldset className="plain-fieldset" disabled={!canEdit}>
        <Field label={t('fl.url')} hint={t('fl.url.hint')}>
          {(id) => <Input id={id} value={url} placeholder="https://flow.example.com" onChange={(e) => setURL(e.target.value)} />}
        </Field>
      </fieldset>
      <p className="hint">{t('fl.params')}</p>
    </ProfileCard>
  )
}
