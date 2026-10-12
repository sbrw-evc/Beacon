import { Avatar } from '../../Avatar'
import { useT } from '../../i18n'
import { Banner } from '../../ui'
import { ADMIN } from '../roles/permissions'
import { useSession } from '../session'
import { strings } from '../strings'
import { fullName } from '../types'
import { AccountCard } from './AccountCard'
import { PasswordCard } from './PasswordCard'
import { PersonalCard } from './PersonalCard'
import { TelegramCard } from './TelegramCard'
import { TimezoneCard } from './TimezoneCard'

export function ProfilePage() {
  const t = useT(strings)
  const { user } = useSession()
  const noAccess = user.role !== ADMIN && (user.permissions?.length ?? 0) === 0
  const admins = user.admins ?? []
  return (
    <>
      <div className="page-head">
        <div className="profile-head">
          <Avatar user={user} size={88} />
          <div>
            <h1>{fullName(user)}</h1>
            <p className="muted">{[user.title, user.department].filter(Boolean).join(' · ') || t('profile.subtitle')}</p>
          </div>
        </div>
      </div>
      {noAccess && (
        <div className="profile-noaccess">
          <Banner kind="warn" title={t('profile.noAccess.title')}>
            <p>{t('profile.noAccess.text')}</p>
            {admins.length > 0 ? (
              <>
                <p>{t('profile.noAccess.admins')}</p>
                <ul className="profile-admins">
                  {admins.map((a, i) => (
                    <li key={i}>
                      {a.name}
                      {a.email && (
                        <>
                          {' — '}
                          <a href={`mailto:${a.email}`}>{a.email}</a>
                        </>
                      )}
                    </li>
                  ))}
                </ul>
              </>
            ) : (
              <p>{t('profile.noAccess.none')}</p>
            )}
          </Banner>
        </div>
      )}
      <div className="cards">
        <PersonalCard />
        {user.source === 'local' && <PasswordCard />}
        <TimezoneCard />
        <TelegramCard />
        <AccountCard />
      </div>
    </>
  )
}
