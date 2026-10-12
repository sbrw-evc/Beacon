import type { FormEvent } from 'react'
import { useT } from '../../i18n'
import { Brand, Button, Preferences } from '../../ui'
import { useSession } from '../session'
import { strings } from '../strings'
import { PasswordFields } from './PasswordFields'
import { useAction } from './useAction'
import { usePasswordChange } from './usePasswordChange'
import './password.css'
import { Flash } from '../../notify'

export function ExpiredPassword({ onSignOut }: { onSignOut: () => void }) {
  const t = useT(strings)
  const { user } = useSession()
  const action = useAction()
  const change = usePasswordChange()

  const submit = (e: FormEvent) => {
    e.preventDefault()
    void action.run(change.submit)
  }

  return (
    <div className="center-page">
      <div className="card signin expired-card">
        <div className="row" style={{ justifyContent: 'space-between' }}>
          <Brand />
          <Preferences />
        </div>
        <form onSubmit={submit} noValidate>
          <div>
            <h1>{t('expired.title')}</h1>
            <p className="muted">{t('expired.text', { user: user.username })}</p>
          </div>
          <PasswordFields change={change} />
          {action.error && (
            <Flash kind="error" title={action.error.message}>
              {action.error.detail}
            </Flash>
          )}
          <div className="card-actions">
            <Button variant="ghost" onClick={onSignOut}>
              {t('signout')}
            </Button>
            <Button type="submit" variant="primary" busy={action.busy} disabled={!change.valid}>
              {t('prefs.password.change')}
            </Button>
          </div>
        </form>
      </div>
    </div>
  )
}
