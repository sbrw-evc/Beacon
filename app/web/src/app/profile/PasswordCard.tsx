import { useT } from '../../i18n'
import { Button } from '../../ui'
import { strings } from '../strings'
import { PasswordExpiryNotice } from './PasswordExpiryNotice'
import { PasswordFields } from './PasswordFields'
import { ProfileCard } from './ProfileCard'
import { useAction } from './useAction'
import { usePasswordChange } from './usePasswordChange'

export function PasswordCard() {
  const t = useT(strings)
  const action = useAction()
  const change = usePasswordChange()

  const save = () =>
    action.run(async () => {
      await change.submit()
      return t('prefs.password.changed')
    })

  return (
    <ProfileCard
      title={t('profile.password')}
      action={action}
      onSubmit={save}
      footer={
        <Button type="submit" variant="primary" busy={action.busy} disabled={!change.valid}>
          {t('prefs.password.change')}
        </Button>
      }
    >
      <PasswordExpiryNotice />
      <PasswordFields change={change} />
    </ProfileCard>
  )
}
