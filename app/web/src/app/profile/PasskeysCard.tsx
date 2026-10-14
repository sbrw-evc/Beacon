import { useEffect, useState } from 'react'
import { Fingerprint, KeyRound, Pencil, Plus, Smartphone, Trash2 } from 'lucide-react'
import { ApiError, api } from '../../api'
import { ask } from '../../confirm'
import { useLocale, useT } from '../../i18n'
import { Button, formatDate, Input } from '../../ui'
import { createPasskey, PasskeyError, passkeysSupported, platformAuthenticator } from '../../webauthn'
import { useSession } from '../session'
import { strings } from '../strings'
import { ProfileCard } from './ProfileCard'
import { useAction } from './useAction'
import '../settings/identity/identity.css'

type Passkey = { id: string; name: string; transports: string[]; backup_eligible: boolean; created_at: string; last_used_at?: string }

const iconOf = (k: Passkey) => (k.transports.some((t) => ['usb', 'nfc', 'ble'].includes(t)) ? KeyRound : k.transports.includes('hybrid') ? Smartphone : Fingerprint)

// PasskeysCard: the passkeys of the signed-in person. A passkey signs in with Windows Hello (face,
// fingerprint, iris, PIN), Touch ID / Face ID, an Android phone or a security key, without a
// password, and works in every connected application of the passkey domain.
export function PasskeysCard() {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const [keys, setKeys] = useState<Passkey[] | null>(null)
  const [platform, setPlatform] = useState(false)
  const [renaming, setRenaming] = useState<{ id: string; name: string } | null>(null)
  const action = useAction()
  const load = () => api<Passkey[]>('GET', '/api/auth/me/passkeys').then(setKeys)
  useEffect(() => {
    void action.run(load)
    void platformAuthenticator().then(setPlatform)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  const add = () =>
    action.run(async () => {
      const started = await api<{ ceremony: string; options: Record<string, unknown> }>('POST', '/api/auth/me/passkeys/begin')
      let credential
      try {
        credential = await createPasskey(started.options)
      } catch (e) {
        if (e instanceof PasskeyError) throw new ApiError(400, e.code)
        throw e
      }
      await api('POST', '/api/auth/me/passkeys/finish', { ceremony: started.ceremony, credential, name: '' })
      await load()
      return t('pk.added')
    })
  const rename = () =>
    action.run(async () => {
      if (!renaming) return
      await api('PATCH', `/api/auth/me/passkeys/${renaming.id}`, { name: renaming.name })
      setRenaming(null)
      await load()
    })
  const remove = (k: Passkey) =>
    action.run(async () => {
      if (!(await ask({ text: t('pk.remove.ask', { name: k.name }), danger: true }))) return
      await api('DELETE', `/api/auth/me/passkeys/${k.id}`)
      await load()
    })
  return (
    <ProfileCard
      title={t('pk.title')}
      badge={<Fingerprint size={18} className="id-badge" aria-hidden />}
      action={action}
      footer={
        <Button variant="primary" busy={action.busy} disabled={!passkeysSupported()} onClick={() => void add()}>
          <Plus size={16} />
          {platform ? t('pk.add.platform') : t('pk.add')}
        </Button>
      }
    >
      <p className="muted">{t('pk.text')}</p>
      {keys && keys.length === 0 && <p className="hint">{t('pk.empty')}</p>}
      {keys && keys.length > 0 && (
        <ul className="pk-list">
          {keys.map((k) => {
            const Icon = iconOf(k)
            return (
              <li key={k.id}>
                <Icon size={18} aria-hidden />
                {renaming?.id === k.id ? (
                  <Input
                    autoFocus
                    value={renaming.name}
                    aria-label={t('pk.rename')}
                    onChange={(e) => setRenaming({ ...renaming, name: e.target.value })}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') {
                        e.preventDefault()
                        void rename()
                      }
                      if (e.key === 'Escape') setRenaming(null)
                    }}
                    onBlur={() => void rename()}
                  />
                ) : (
                  <div className="pk-name">
                    <strong>{k.name}</strong>
                    <span className="hint">
                      {t('pk.created', { at: formatDate(k.created_at, locale, timezone) })}
                      {' · '}
                      {k.last_used_at ? t('pk.used', { at: formatDate(k.last_used_at, locale, timezone) }) : t('pk.unused')}
                      {k.backup_eligible && ` · ${t('pk.synced')}`}
                    </span>
                  </div>
                )}
                <button type="button" className="icon-btn" aria-label={t('pk.rename')} onClick={() => setRenaming({ id: k.id, name: k.name })}>
                  <Pencil size={15} />
                </button>
                <button type="button" className="icon-btn" aria-label={t('pk.remove')} onClick={() => void remove(k)}>
                  <Trash2 size={15} />
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </ProfileCard>
  )
}
