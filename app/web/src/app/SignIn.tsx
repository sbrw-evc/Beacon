import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { Fingerprint } from 'lucide-react'
import { ApiError, api, setCsrf, type Meta } from '../api'
import { errorText, useT } from '../i18n'
import { Brand, Button, Field, Input, Password, Preferences } from '../ui'
import { getPasskey, PasskeyError, passkeysSupported } from '../webauthn'
import { strings } from './strings'
import type { User } from './types'
import { Flash } from '../notify'

// SSO_KEY keeps a single sign-in request of a connected application while the person signs in
// with an external provider (Microsoft, Google, Apple): the provider sends them back to "/".
export const SSO_KEY = 'umbrella.sso'

export type SignInContext = { id: string; application: string; hint: string; loginHint: string }

export function SignIn({ meta, onSignedIn, context }: { meta: Meta; onSignedIn: (u: User) => void; context?: SignInContext }) {
  const t = useT(strings)
  const [username, setUsername] = useState(context?.loginHint ?? '')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState<'' | 'password' | 'passkey' | string>('')
  const [error, setError] = useState<{ message: string; detail?: string } | null>(() => redirectError(t))
  useEffect(dropRedirectError, [])

  const passkeys = !!meta.passkeys_enabled && passkeysSupported()
  const providers: { id: string; title: string; start: string }[] = [
    ...(meta.entra_enabled ? [{ id: 'entra', title: 'Microsoft', start: '/api/auth/entra/start' }] : []),
    ...(meta.providers ?? []).map((p) => ({ id: p.id, title: p.title, start: `/api/auth/sso/${p.id}/start` })),
  ]

  const done = (u: User) => {
    setCsrf(u.csrf ?? '')
    onSignedIn(u)
  }

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy('password')
    setError(null)
    try {
      done(await api<User>('POST', '/api/auth/login', { username, password }))
    } catch (err) {
      setError(errorText(t, err))
      setPassword('')
    } finally {
      setBusy('')
    }
  }

  const passkey = async () => {
    setBusy('passkey')
    setError(null)
    try {
      const started = await api<{ ceremony: string; options: Record<string, unknown> }>('POST', '/api/auth/passkey/begin')
      const credential = await getPasskey(started.options)
      done(await api<User>('POST', '/api/auth/passkey/finish', { ceremony: started.ceremony, credential }))
    } catch (err) {
      setError(errorText(t, err instanceof PasskeyError ? new ApiError(400, err.code) : err))
    } finally {
      setBusy('')
    }
  }

  const leave = (p: { id: string; start: string }) => {
    setBusy(p.id)
    if (context) {
      try {
        sessionStorage.setItem(SSO_KEY, context.id)
      } catch {
        // without storage the person comes back to Umbrella and opens the application again
      }
    }
    window.location.assign(p.start)
  }

  // A connected application may ask for a method at once (idp_hint).
  const hinted = useRef(false)
  useEffect(() => {
    if (!context || hinted.current) return
    hinted.current = true
    const p = providers.find((x) => x.id === context.hint)
    if (p) leave(p)
    else if (context.hint === 'passkey' && passkeys) void passkey()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [context])

  return (
    <div className="center-page">
      <div className="card signin">
        <div className="row" style={{ justifyContent: 'space-between' }}>
          <Brand />
          <Preferences />
        </div>
        <form onSubmit={submit} noValidate>
          <div>
            <h1>{t('signin.title')}</h1>
            {context && <p className="muted signin-for">{t('signin.for', { app: context.application })}</p>}
          </div>
          {error && <Flash kind="error" title={error.message}>{error.detail}</Flash>}
          {passkeys && (
            <>
              <Button type="button" variant="primary" className="signin-passkey" busy={busy === 'passkey'} disabled={!!busy && busy !== 'passkey'} onClick={passkey}>
                <Fingerprint size={18} aria-hidden />
                {t('signin.passkey')}
              </Button>
              <p className="hint signin-passkey-hint">{t('signin.passkey.hint')}</p>
              <div className="signin-or">{t('signin.or')}</div>
            </>
          )}
          <Field label={t('signin.username')}>
            {(id) => <Input id={id} value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username webauthn" autoFocus={!passkeys} required />}
          </Field>
          <Field label={t('signin.password')}>
            {(id) => <Password id={id} value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required />}
          </Field>
          <Button variant={passkeys ? 'secondary' : 'primary'} type="submit" busy={busy === 'password'} disabled={!username || !password || (!!busy && busy !== 'password')}>
            {t('signin.submit')}
          </Button>
          {meta.ldap_enabled && <p className="hint">{t('signin.ldap')}</p>}
          {providers.length > 0 && (
            <>
              <div className="signin-or">{t('signin.or')}</div>
              <div className="signin-providers">
                {providers.map((p) => (
                  <Button key={p.id} type="button" busy={busy === p.id} disabled={!!busy && busy !== p.id} onClick={() => leave(p)}>
                    <ProviderMark id={p.id} />
                    {t('signin.with', { name: p.title })}
                  </Button>
                ))}
              </div>
            </>
          )}
        </form>
      </div>
      <p className="hint">{meta.version}</p>
    </div>
  )
}

// redirectError reads the error an external sign-in left in the address.
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

export function ProviderMark({ id }: { id: string }): ReactNode {
  switch (id) {
    case 'entra':
      return (
        <svg width="16" height="16" viewBox="0 0 21 21" aria-hidden>
          <rect x="1" y="1" width="9" height="9" fill="#f25022" />
          <rect x="11" y="1" width="9" height="9" fill="#7fba00" />
          <rect x="1" y="11" width="9" height="9" fill="#00a4ef" />
          <rect x="11" y="11" width="9" height="9" fill="#ffb900" />
        </svg>
      )
    case 'google':
      return (
        <svg width="16" height="16" viewBox="0 0 48 48" aria-hidden>
          <path fill="#FFC107" d="M43.6 20.5H42V20H24v8h11.3C33.7 32.7 29.2 36 24 36c-6.6 0-12-5.4-12-12s5.4-12 12-12c3.1 0 5.8 1.2 7.9 3.1l5.7-5.7C34 6.1 29.3 4 24 4 12.9 4 4 12.9 4 24s8.9 20 20 20 20-8.9 20-20c0-1.3-.1-2.4-.4-3.5z" />
          <path fill="#FF3D00" d="M6.3 14.7l6.6 4.8C14.7 15.1 19 12 24 12c3.1 0 5.8 1.2 7.9 3.1l5.7-5.7C34 6.1 29.3 4 24 4 16.3 4 9.7 8.3 6.3 14.7z" />
          <path fill="#4CAF50" d="M24 44c5.2 0 9.9-2 13.4-5.2l-6.2-5.2C29.2 35.1 26.7 36 24 36c-5.2 0-9.6-3.3-11.3-8l-6.5 5C9.5 39.6 16.2 44 24 44z" />
          <path fill="#1976D2" d="M43.6 20.5H42V20H24v8h11.3c-.8 2.2-2.2 4.2-4.1 5.6l6.2 5.2C37 39.2 44 34 44 24c0-1.3-.1-2.4-.4-3.5z" />
        </svg>
      )
    case 'apple':
      return (
        <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden fill="currentColor">
          <path d="M16.37 12.62c-.02-2.3 1.88-3.4 1.96-3.46-1.07-1.56-2.73-1.78-3.32-1.8-1.41-.14-2.76.83-3.48.83-.72 0-1.82-.81-3-.79-1.54.02-2.96.9-3.76 2.28-1.6 2.78-.41 6.9 1.15 9.16.76 1.1 1.67 2.34 2.86 2.3 1.15-.05 1.58-.74 2.97-.74 1.38 0 1.77.74 2.98.72 1.23-.02 2.01-1.12 2.76-2.23.87-1.28 1.23-2.52 1.25-2.58-.03-.01-2.4-.92-2.42-3.66zM14.1 5.86c.63-.77 1.06-1.83.94-2.89-.91.04-2.01.61-2.66 1.37-.58.67-1.1 1.76-.96 2.79 1.01.08 2.05-.51 2.68-1.27z" />
        </svg>
      )
  }
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden fill="none" stroke="currentColor" strokeWidth="2">
      <circle cx="12" cy="12" r="9" />
      <path d="M8 12h8M12 8v8" />
    </svg>
  )
}
