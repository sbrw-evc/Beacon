import { useEffect, useState } from 'react'
import { LogIn } from 'lucide-react'
import type { Meta } from '../api'
import { useT } from '../i18n'
import { Brand, Button, Preferences } from '../ui'
import { strings } from './strings'
import { Flash } from '../notify'

// People sign in at Keystone: Beacon only starts the sign-in
// (GET /api/auth/spa/start) and takes the answer at /api/auth/spa/callback, which comes back to
// the page the person opened or to /login?sso_error=… when Keystone or Beacon refused.

const SIGNED_OUT = 'umbrella.signedOut'

// markSignedOut keeps the sign-in page from sending the person straight back to Keystone after they
// signed out on purpose.
export function markSignedOut() {
  try {
    sessionStorage.setItem(SIGNED_OUT, '1')
  } catch {
    // without storage the page offers the button instead of going to Keystone at once anyway
  }
}

function takeSignedOut() {
  try {
    const v = sessionStorage.getItem(SIGNED_OUT) === '1'
    sessionStorage.removeItem(SIGNED_OUT)
    return v
  } catch {
    return false
  }
}

export function startURL(hint = '') {
  const here = window.location.pathname === '/login' ? '/' : window.location.pathname + window.location.search + window.location.hash
  const q = new URLSearchParams({ return: here })
  if (hint) q.set('idp_hint', hint)
  return `/api/auth/spa/start?${q}`
}

export type SignInError = { code: string; description: string }

export function readSignInError(search: string): SignInError | null {
  const q = new URLSearchParams(search)
  const code = q.get('sso_error')
  return code ? { code, description: q.get('sso_error_description') ?? '' } : null
}

const KNOWN = new Set(['state', 'not_connected', 'spa_unavailable', 'invalid_token', 'no_role', 'account_conflict', 'access_denied'])

export function signInErrorText(t: (k: string, v?: Record<string, string | number>) => string, e: SignInError) {
  return KNOWN.has(e.code) ? t(`sso.${e.code}`) : t('sso.other', { code: e.code })
}

export function SignIn({ meta }: { meta: Meta }) {
  const t = useT(strings)
  const [error] = useState(() => readSignInError(window.location.search))
  const [signedOut] = useState(takeSignedOut)
  const [busy, setBusy] = useState('')
  const spa = meta.spa
  const connected = !!spa?.connected
  const auto = connected && !error && !signedOut

  useEffect(() => {
    if (error) window.history.replaceState(null, '', '/login')
  }, [error])

  useEffect(() => {
    if (auto) window.location.replace(startURL())
  }, [auto])

  const go = (hint = '') => {
    setBusy(hint || 'spa')
    window.location.assign(startURL(hint))
  }

  return (
    <div className="center-page">
      <div className="card signin">
        <div className="row" style={{ justifyContent: 'space-between' }}>
          <Brand />
          <Preferences />
        </div>
        <div className="signin-body">
          <h1>{t('signin.title')}</h1>
          {!connected ? (
            <Flash kind="error" title={t('signin.notConnected')}>
              {spa?.error || t('signin.notConnected.text')}
            </Flash>
          ) : auto ? (
            <p className="muted">{t('signin.redirect')}</p>
          ) : (
            <>
              {error && (
                <Flash kind="error" title={t('signin.failed')}>
                  {signInErrorText(t, error)}
                  {error.description && error.code !== 'no_role' ? ` ${error.description}` : ''}
                </Flash>
              )}
              {signedOut && !error && <p className="muted">{t('signin.signedOut')}</p>}
              <p className="muted">{t('signin.text')}</p>
              <Button variant="primary" busy={busy === 'spa'} disabled={!!busy} onClick={() => go()}>
                <LogIn size={18} aria-hidden />
                {t('signin.spa')}
              </Button>
              {(spa?.methods ?? []).length > 1 && (
                <div className="signin-providers">
                  {spa!.methods.map((m) => (
                    <Button key={m.id} busy={busy === m.id} disabled={!!busy} onClick={() => go(m.id)}>
                      {t('signin.with', { name: m.title })}
                    </Button>
                  ))}
                </div>
              )}
            </>
          )}
        </div>
      </div>
      <p className="hint">{meta.version}</p>
    </div>
  )
}
