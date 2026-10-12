import { useRef } from 'react'
import { ImagePlus, Trash2 } from 'lucide-react'
import { api, upload } from '../../api'
import { Avatar } from '../../Avatar'
import { useT } from '../../i18n'
import { Button } from '../../ui'
import { useSession } from '../session'
import { strings } from '../strings'
import type { User } from '../types'
import type { Action } from './useAction'

function avatarHint(u: User) {
  if (u.avatar_source === 'ldap') return 'prefs.avatar.ldap'
  return u.has_avatar ? 'prefs.avatar.own' : 'prefs.avatar.gravatar'
}

export function AvatarEditor({ action }: { action: Action }) {
  const t = useT(strings)
  const { user, update } = useSession()
  const file = useRef<HTMLInputElement>(null)

  const pick = (f: File | undefined) => {
    if (!f) return
    void action.run(async () => {
      update(await upload<User>('PUT', '/api/auth/me/avatar', f))
      return t('prefs.avatar.saved')
    })
  }

  const remove = () =>
    action.run(async () => {
      update(await api<User>('DELETE', '/api/auth/me/avatar'))
    })

  return (
    <div className="avatar-edit">
      <Avatar user={user} size={72} />
      <div className="stack" style={{ gap: 8 }}>
        <div className="row">
          <Button type="button" onClick={() => file.current?.click()} busy={action.busy}>
            <ImagePlus size={16} />
            {t('prefs.avatar.upload')}
          </Button>
          {user.avatar_source === 'upload' && (
            <Button type="button" variant="ghost" onClick={remove} disabled={action.busy}>
              <Trash2 size={16} />
              {t('prefs.avatar.remove')}
            </Button>
          )}
        </div>
        <p className="hint">{t(avatarHint(user))}</p>
        <input
          ref={file}
          type="file"
          accept="image/png,image/jpeg,image/gif,image/webp"
          hidden
          onChange={(e) => {
            pick(e.target.files?.[0])
            e.target.value = ''
          }}
        />
      </div>
    </div>
  )
}
