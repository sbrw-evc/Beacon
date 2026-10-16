import { ExternalLink } from 'lucide-react'
import { useT } from '../../i18n'
import { Banner } from '../../ui'
import { useSession } from '../session'
import { strings } from '../strings'
import { profileOf } from '../types'
import { AvatarEditor } from './AvatarEditor'
import { ProfileCard } from './ProfileCard'
import { ProfileFieldsGrid } from './ProfileFieldsGrid'
import { useAction } from './useAction'

// PersonalCard shows the data Keystone gives (name, e-mail); the photo is Beacon's own.
export function PersonalCard() {
  const t = useT(strings)
  const { user } = useSession()
  const action = useAction()
  return (
    <ProfileCard
      title={t('profile.personal')}
      action={action}
      wide
      footer={
        user.account_url && (
          <a className="btn btn-secondary" href={user.account_url} target="_blank" rel="noreferrer">
            <ExternalLink size={16} aria-hidden />
            {t('prefs.spa.open')}
          </a>
        )
      }
    >
      <AvatarEditor action={action} />
      <Banner kind="info" title={t('prefs.directory')} />
      <ProfileFieldsGrid value={profileOf(user)} onChange={() => undefined} editable={false} />
    </ProfileCard>
  )
}
