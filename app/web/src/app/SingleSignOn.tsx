import { useEffect, useState } from 'react'
import { ApiError, api, setCsrf, type Meta } from '../api'
import { errorText, useT } from '../i18n'
import { Brand, Button } from '../ui'
import { Flash } from '../notify'
import { SignIn, SSO_KEY } from './SignIn'
import { strings } from './strings'
import type { User } from './types'

type Request = { id: string; application: string; kind: string; url: string; idp_hint: string; login_hint: string; prompt: string }

// pendingSSO returns the single sign-in request a person left for an external provider.
export function pendingSSO(): string | null {
  try {
    const id = sessionStorage.getItem(SSO_KEY)
    if (id) sessionStorage.removeItem(SSO_KEY)
    return id
  } catch {
    return null
  }
}

// SingleSignOn is /sso: a connected application (Umbrella Flow, Inventory DB, Network Control)
// asked Umbrella who the person is. A signed-in person goes straight back; others sign in first.
export function SingleSignOn({ meta, user, onSignedIn }: { meta: Meta; user: User | null; onSignedIn: (u: User) => void }) {
  const t = useT(strings)
  const params = new URLSearchParams(window.location.search)
  const id = params.get('request') ?? ''
  const [req, setReq] = useState<Request | null>(null)
  const [error, setError] = useState<{ message: string; detail?: string } | null>(() =>
    params.get('error') || !id ? errorText(t, new ApiError(400, params.get('error') ?? 'identity_request_expired')) : null,
  )
  const [reauth, setReauth] = useState(false)

  useEffect(() => {
    if (!id || error) return
    api<Request>('GET', `/api/oauth2/requests/${encodeURIComponent(id)}`)
      .then(setReq)
      .catch((e) => setError(errorText(t, e)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

  useEffect(() => {
    if (!req || !user || reauth) return
    api<{ redirect: string }>('POST', `/api/oauth2/requests/${encodeURIComponent(req.id)}/approve`)
      .then((r) => window.location.replace(r.redirect))
      .catch(async (e) => {
        if (e instanceof ApiError && e.code === 'identity_reauth') {
          await api('POST', '/api/auth/logout').catch(() => undefined)
          setCsrf('')
          setReauth(true)
          return
        }
        setError(errorText(t, e))
      })
  }, [req, user, reauth, t])

  if (error) {
    return (
      <div className="center-page">
        <div className="card signin">
          <Brand />
          <div className="stack" style={{ marginTop: 20 }}>
            <h1>{t('sso.title')}</h1>
            <Flash kind="error" title={error.message}>
              {error.detail ?? t('sso.error.text')}
            </Flash>
            <div className="row">
              {req && (
                <Button variant="primary" onClick={() => window.location.assign(req.url)}>
                  {t('sso.back', { app: req.application })}
                </Button>
              )}
              <Button onClick={() => window.location.assign('/')}>{t('sso.home')}</Button>
            </div>
          </div>
        </div>
      </div>
    )
  }
  if (!req) return <div className="center-page boot">{t('loading')}</div>
  if (!user || reauth) {
    return (
      <SignIn
        meta={meta}
        context={{ id: req.id, application: req.application, hint: req.idp_hint, loginHint: req.login_hint }}
        onSignedIn={(u) => {
          setReauth(false)
          onSignedIn(u)
        }}
      />
    )
  }
  return <div className="center-page boot">{t('sso.continue', { app: req.application })}</div>
}
