import { useState } from 'react'
import { Copy, Wand2 } from 'lucide-react'
import { PolicyChecklist } from '../../PolicyChecklist'
import { checkPassword } from '../../policy'
import { useT } from '../../i18n'
import { Button, Field, Password, Switch } from '../../ui'
import { useSession } from '../session'
import { generatePassword } from './generate'
import { strings } from './strings'

export type NewPassword = { password: string; must_change_password: boolean }

export const freshPassword: NewPassword = { password: '', must_change_password: true }

export function usePasswordValid(value: NewPassword, username: string) {
  const { policy } = useSession()
  return checkPassword(value.password, username, policy).length === 0
}

export function NewPasswordFields({ value, onChange, username }: { value: NewPassword; onChange: (v: NewPassword) => void; username: string }) {
  const t = useT(strings)
  const { policy } = useSession()
  const [copied, setCopied] = useState(false)

  const generate = () => {
    onChange({ ...value, password: generatePassword(policy, username) })
    setCopied(false)
  }

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value.password)
      setCopied(true)
    } catch {
      setCopied(false)
    }
  }

  return (
    <div className="stack">
      <Field label={t('usr.field.password')}>
        {(id) => (
          <div className="usr-password">
            <Password
              id={id}
              value={value.password}
              autoComplete="new-password"
              onChange={(e) => {
                onChange({ ...value, password: e.target.value })
                setCopied(false)
              }}
            />
            <Button type="button" onClick={generate}>
              <Wand2 size={16} aria-hidden />
              {t('usr.generate')}
            </Button>
            {value.password && window.isSecureContext && (
              <Button type="button" variant="ghost" onClick={copy}>
                <Copy size={16} aria-hidden />
                {copied ? t('usr.copied') : t('usr.copy')}
              </Button>
            )}
          </div>
        )}
      </Field>
      <PolicyChecklist policy={policy} password={value.password} username={username} t={t} />
      <Switch
        checked={value.must_change_password}
        onChange={(v) => onChange({ ...value, must_change_password: v })}
        label={t('usr.mustChange')}
        hint={t('usr.mustChange.hint')}
      />
    </div>
  )
}
