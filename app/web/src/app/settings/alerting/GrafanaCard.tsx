import { useEffect, useState } from 'react'
import { api } from '../../../api'
import { useT } from '../../../i18n'
import { Button, Field, Input } from '../../../ui'
import { ProfileCard } from '../../profile/ProfileCard'
import { useAction } from '../../profile/useAction'
import { useSession } from '../../session'
import { strings } from './strings'

type Grafana = { dashboard_url: string; window_minutes: number }

export function GrafanaCard() {
  const t = useT(strings)
  const { can } = useSession()
  const canEdit = can('settings.alerting:edit')
  const [saved, setSaved] = useState<Grafana | null>(null)
  const [d, setD] = useState({ dashboard_url: '', window: '10' })
  const loader = useAction(strings)
  const saver = useAction(strings)
  const apply = (g: Grafana) => {
    setSaved(g)
    setD({ dashboard_url: g.dashboard_url ?? '', window: String(g.window_minutes || 10) })
  }
  const { run: load } = loader
  useEffect(() => {
    void load(async () => apply(await api<Grafana>('GET', '/api/grafana')))
  }, [load])
  if (!saved) {
    return (
      <ProfileCard title={t('gf.title')} action={loader} inline>
        {!loader.error && <p className="muted">{t('loading')}</p>}
      </ProfileCard>
    )
  }
  const dirty = d.dashboard_url.trim() !== (saved.dashboard_url ?? '') || Number(d.window) !== saved.window_minutes
  const save = () =>
    saver.run(async () => {
      apply(await api<Grafana>('PUT', '/api/grafana', { dashboard_url: d.dashboard_url.trim(), window_minutes: Number(d.window) || 0 }))
      return t('saved')
    })
  return (
    <ProfileCard
      title={t('gf.title')}
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
      <p className="muted">{t('gf.text')}</p>
      <fieldset className="plain-fieldset grid-2" disabled={!canEdit}>
        <Field label={t('gf.dashboard')} hint={t('gf.dashboard.hint')}>
          {(id) => <Input id={id} value={d.dashboard_url} placeholder="https://grafana.example.com/d/abc123/incident" onChange={(e) => setD({ ...d, dashboard_url: e.target.value })} />}
        </Field>
        <Field label={t('gf.window')} hint={t('gf.window.hint')}>
          {(id) => <Input id={id} inputMode="numeric" value={d.window} onChange={(e) => setD({ ...d, window: e.target.value.replace(/\D/g, '') })} />}
        </Field>
      </fieldset>
      <p className="hint">{t('gf.vars')}</p>
    </ProfileCard>
  )
}
