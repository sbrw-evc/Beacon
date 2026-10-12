import { useEffect, useState, type FormEvent } from 'react'
import { ApiError, api, setCsrf, type Meta } from '../api'
import { errorText, useT } from '../i18n'
import { Brand, Button, Field, Input, Password, Preferences } from '../ui'
import { strings } from './strings'
import type { User } from './types'
import { Flash } from '../notify'

export function SignIn({ meta, onSignedIn }: { meta: Meta; onSignedIn: (u: User) => void }) {
  const t = useT(strings)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<{ message: string; detail?: string } | null>(() => redirectError(t))
  const [leaving, setLeaving] = useState(false)
  useEffect(dropRedirectError, [])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const u = await api<User>('POST', '/api/auth/login', { username, password })
      setCsrf(u.csrf ?? '')
      onSignedIn(u)
    } catch (err) {
      setError(errorText(t, err))
      setPassword('')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="center-page">
      <div className="card signin">
        <div className="row" style={{ justifyContent: 'space-between' }}>
          <Brand />
          <Preferences />
        </div>
        <form onSubmit={submit} noValidate>
          <h1>{t('signin.title')}</h1>
          {error && <Flash kind="error" title={error.message}>{error.detail}</Flash>}
          <Field label={t('signin.username')}>
            {(id) => <Input id={id} value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" autoFocus required />}
          </Field>
          <Field label={t('signin.password')}>
            {(id) => <Password id={id} value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required />}
          </Field>
          <Button variant="primary" type="submit" busy={busy} disabled={!username || !password}>
            {t('signin.submit')}
          </Button>
          {meta.ldap_enabled && <p className="hint">{t('signin.ldap')}</p>}
          {meta.entra_enabled && (
            <>
              <div className="signin-or">{t('signin.or')}</div>
              <Button
                type="button"
                busy={leaving}
                onClick={() => {
                  setLeaving(true)
                  window.location.assign('/api/auth/entra/start')
                }}
              >
                <MicrosoftMark />
                {t('signin.entra')}
              </Button>
            </>
          )}
        </form>
      </div>
      <p className="hint">{meta.version}</p>
    </div>
  )
}

// redirectError reads the error the Microsoft sign-in left in the address.
function redirectError(t: (k: string) => string) {
  const code = new URLSearchParams(window.location.search).get('signin_error')
  return code ? errorText(t, new ApiError(401, code)) : null
}

function dropRedirectError() {
  const params = new URLSearchParams(window.location.search)
  if (!params.has('signin_error')) return
  params.delete('signin_error')
  const rest = params.toString()
  window.history.replaceState(null, '', window.location.pathname + (rest ? `?${rest}` : '') + window.location.hash)
}

function MicrosoftMark() {
  return (
    <svg width="16" height="16" viewBox="0 0 21 21" aria-hidden>
      <rect x="1" y="1" width="9" height="9" fill="#f25022" />
      <rect x="11" y="1" width="9" height="9" fill="#7fba00" />
      <rect x="1" y="11" width="9" height="9" fill="#00a4ef" />
      <rect x="11" y="11" width="9" height="9" fill="#ffb900" />
    </svg>
  )
}
