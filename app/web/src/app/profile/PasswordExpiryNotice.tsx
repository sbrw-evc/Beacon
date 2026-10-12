import { useLocale, useT } from '../../i18n'
import { Link } from '../../router'
import { Banner, formatDate } from '../../ui'
import { PROFILE_PATH } from '../pages'
import { useSession } from '../session'
import { strings } from '../strings'
import './password.css'

const DAY = 24 * 60 * 60 * 1000

export function PasswordExpiryNotice({ link }: { link?: boolean }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { user, timezone } = useSession()
  if (!user.password_expiry_warning || user.password_expired || !user.password_expires_at) return null
  const days = Math.max(Math.ceil((new Date(user.password_expires_at).getTime() - Date.now()) / DAY), 0)
  return (
    <div className="expiry-notice">
      <Banner kind="warn" title={t('expiry.title', { n: days, date: formatDate(user.password_expires_at, locale, timezone) })}>
        {link ? <Link to={PROFILE_PATH}>{t('expiry.change')}</Link> : t('expiry.text')}
      </Banner>
    </div>
  )
}
