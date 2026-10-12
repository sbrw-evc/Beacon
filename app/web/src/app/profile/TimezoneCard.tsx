import { useEffect, useState } from 'react'
import { api } from '../../api'
import { useT } from '../../i18n'
import { Button, Field, TimezoneSelect, zoneLabel } from '../../ui'
import { useSession } from '../session'
import { strings } from '../strings'
import type { User } from '../types'
import { ProfileCard } from './ProfileCard'
import { useAction } from './useAction'

export function TimezoneCard() {
  const t = useT(strings)
  const { user, defaultTz, update } = useSession()
  const action = useAction()
  const [tz, setTz] = useState(user.timezone)

  useEffect(() => setTz(user.timezone), [user.timezone])

  const save = () =>
    action.run(async () => {
      update(await api<User>('PUT', '/api/auth/me/preferences', { timezone: tz }))
      return t('prefs.tz.saved')
    })

  return (
    <ProfileCard
      title={t('field.timezone')}
      action={action}
      onSubmit={save}
      footer={
        <Button type="submit" variant="primary" busy={action.busy} disabled={tz === user.timezone}>
          {t('save')}
        </Button>
      }
    >
      <Field label={t('field.timezone')} hint={t('prefs.tz.hint')}>
        {(id) => <TimezoneSelect id={id} value={tz} onChange={setTz} defaultLabel={`${t('prefs.default')}: ${zoneLabel(defaultTz)}`} />}
      </Field>
    </ProfileCard>
  )
}
