import { useState } from 'react'
import { Copy, KeyRound } from 'lucide-react'
import { api } from '../../api'
import { ErrorFlash } from '../../connections/ConnectionCard'
import { useAction } from '../../connections/useRequest'
import { useT } from '../../i18n'
import { Banner, Button } from '../../ui'
import { useSession } from '../session'
import { strings } from './strings'
import { tokenStrings } from './tokenStrings'
import type { CredentialChoice } from './types'

const dict = { en: { ...strings.en, ...tokenStrings.en }, ru: { ...strings.ru, ...tokenStrings.ru } }

export const BEARER = 'bearer'

type Made = { credential: CredentialChoice; token: string }

// useCanCreateToken: the slot takes a Bearer credential and the user may create credentials.
export function useCanCreateToken(types: string[]) {
  const { can } = useSession()
  return can('credentials:edit') && can('connectors:edit') && (types.length === 0 || types.includes(BEARER))
}

// CreateToken makes a Bearer credential with a generated token for a connector slot and shows the
// token once, so the source can be set up without leaving the dialog.
export function CreateToken({ name, onCreated }: { name: string; onCreated: (c: CredentialChoice) => void }) {
  const t = useT(dict)
  const action = useAction()
  const [made, setMade] = useState<Made | null>(null)
  const [copied, setCopied] = useState(false)

  const create = async () => {
    const label = t('tk.name', { name: name.trim() || t('tk.default') })
    const out = await action.run(() => api<Made>('POST', '/api/connectors/credentials/token', { name: label }))
    if (!out) return
    setMade(out)
    setCopied(false)
    onCreated(out.credential)
  }
  const copy = async () => {
    if (!made) return
    try {
      await navigator.clipboard.writeText(made.token)
      setCopied(true)
    } catch {
      setCopied(false)
    }
  }

  return (
    <div className="stack">
      {!made && (
        <div>
          <Button busy={action.busy} onClick={() => void create()}>
            <KeyRound size={15} aria-hidden />
            {t('tk.create')}
          </Button>
        </div>
      )}
      {made && (
        <Banner kind="ok" title={t('tk.made', { name: made.credential.name })}>
          <p>{t('tk.once')}</p>
          <div className="row" style={{ alignItems: 'center', flexWrap: 'wrap' }}>
            <code className="cn-mono" style={{ wordBreak: 'break-all', userSelect: 'all' }}>
              {made.token}
            </code>
            <Button variant="ghost" onClick={() => void copy()}>
              <Copy size={15} aria-hidden />
              {copied ? t('tk.copied') : t('tk.copy')}
            </Button>
          </div>
        </Banner>
      )}
      <ErrorFlash error={action.error} strings={strings} />
    </div>
  )
}
