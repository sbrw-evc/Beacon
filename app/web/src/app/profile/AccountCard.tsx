import { useLocale, useT } from '../../i18n'
import { formatDate, Rows } from '../../ui'
import { useSession } from '../session'
import { strings } from '../strings'
import { ProfileCard } from './ProfileCard'
import { roleLabel } from '../types'

export function AccountCard() {
  const t = useT(strings)
  const { locale } = useLocale()
  const { user, timezone } = useSession()
  return (
    <ProfileCard title={t('profile.account')}>
      <Rows
        rows={[
          [t('signin.username'), user.username],
          [t('field.source'), t(`source.${user.source}`)],
          [t('field.role'), roleLabel(t, user.role, user.role_name)],
          [t('field.lastLogin'), formatDate(user.last_login_at, locale, timezone)],
        ]}
      />
    </ProfileCard>
  )
}
