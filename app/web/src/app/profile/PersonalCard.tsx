import { useEffect, useState } from 'react'
import { api } from '../../api'
import { useT } from '../../i18n'
import { Banner, Button } from '../../ui'
import { useSession } from '../session'
import { strings } from '../strings'
import { profileChanged, profileOf, type ProfileFields, type User } from '../types'
import { AvatarEditor } from './AvatarEditor'
import { ProfileCard } from './ProfileCard'
import { ProfileFieldsGrid } from './ProfileFieldsGrid'
import { useAction } from './useAction'

export function PersonalCard() {
  const t = useT(strings)
  const { user, update } = useSession()
  const action = useAction()
  const local = user.source === 'local'
  const [form, setForm] = useState<ProfileFields>(() => profileOf(user))

  useEffect(() => setForm(profileOf(user)), [user])

  const changed = profileChanged(form, profileOf(user))

  const save = () =>
    action.run(async () => {
      update(await api<User>('PUT', '/api/auth/me/profile', form))
      return t('prefs.saved')
    })

  return (
    <ProfileCard
      title={t('profile.personal')}
      action={action}
      wide
      onSubmit={local ? save : undefined}
      footer={
        local && (
          <Button type="submit" variant="primary" busy={action.busy} disabled={!changed}>
            {t('save')}
          </Button>
        )
      }
    >
      <AvatarEditor action={action} />
      {!local && <Banner kind="info" title={t('prefs.directory')} />}
      <ProfileFieldsGrid value={form} onChange={setForm} editable={local} />
    </ProfileCard>
  )
}
