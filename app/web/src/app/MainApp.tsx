import { useCallback, useEffect, useMemo, useState, type CSSProperties } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { api, ApiError, setCsrf, type Meta } from '../api'
import { defaultPolicy, type PasswordPolicy } from '../policy'
import { useLocale, useT } from '../i18n'
import { RouterProvider, SCROLL_ROOT_ID, useRouter } from '../router'
import { ExpiredPassword } from './profile/ExpiredPassword'
import { PasswordExpiryNotice } from './profile/PasswordExpiryNotice'
import { ProfilePage } from './profile/ProfilePage'
import { PageBoundary } from './PageBoundary'
import { PageHead } from './PageHead'
import { HOME_PATH, resolve, visiblePages } from './pages'
import { DesktopOnly, forcedPages } from './mobile/DesktopOnly'
import { MobileFrame } from './mobile/MobileFrame'
import { hasOverview, mobileReady, OVERVIEW_PATH } from './mobile/mobilePages'
import { Overview } from './mobile/Overview'
import { useMobile } from './mobile/useMobile'
import { SessionProvider, useSession, type Session } from './session'
import { Sidebar, SidebarResizer, useCollapsed, useSidebarWidth } from './Sidebar'
import { SignIn } from './SignIn'
import { strings } from './strings'
import { TopBar } from './TopBar'
import type { User } from './types'
import { setNotifyOwner, Toaster } from '../notify'
import { ConfirmHost } from '../confirm'

export default function MainApp({ meta }: { meta: Meta }) {
  return (
    <RouterProvider>
      <Shell meta={meta} />
      <Toaster />
      <ConfirmHost />
    </RouterProvider>
  )
}

function Shell({ meta }: { meta: Meta }) {
  const [user, setUser] = useState<User | null>(null)
  const [checked, setChecked] = useState(false)
  const [defaultTz, setDefaultTz] = useState(meta.default_timezone || 'UTC')
  const [policy, setPolicy] = useState<PasswordPolicy>(meta.password_policy ?? defaultPolicy)
  const t = useT(strings)

  useEffect(() => setNotifyOwner(user?.id ?? ''), [user?.id])

  useEffect(() => {
    api<User>('GET', '/api/auth/me')
      .then((u) => {
        setCsrf(u.csrf ?? '')
        setUser(u)
      })
      .catch(() => setUser(null))
      .finally(() => setChecked(true))
  }, [])

  const expire = useCallback(() => {
    setCsrf('')
    setUser(null)
  }, [])

  const refresh = useCallback(async () => {
    try {
      const next = await api<User>('GET', '/api/auth/me')
      setUser((prev) => (prev && JSON.stringify(prev) === JSON.stringify(next) ? prev : next))
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) expire()
    }
  }, [expire])

  const signOut = useCallback(async () => {
    try {
      await api('POST', '/api/auth/logout')
    } catch {
      return
    } finally {
      expire()
    }
  }, [expire])

  // The server keeps the interface language: voice calls speak to the user in it.
  const { locale } = useLocale()
  const userLocale = user && !user.password_expired ? (user.locale ?? '') : null
  useEffect(() => {
    if (userLocale === null || userLocale === locale) return
    api('PUT', '/api/auth/me/preferences', { locale })
      .then(() => setUser((prev) => (prev ? { ...prev, locale } : prev)))
      .catch(() => undefined)
  }, [userLocale, locale])

  const permissions = useMemo(() => new Set(user?.permissions ?? []), [user?.permissions])

  const session = useMemo<Session | null>(
    () =>
      user && {
        user,
        timezone: user.timezone || defaultTz,
        defaultTz,
        policy,
        can: (perm: string) => permissions.has(perm),
        setDefaultTz,
        update: setUser,
        refresh,
        setPolicy,
        expire,
      },
    [user, defaultTz, policy, refresh, expire, permissions],
  )

  if (!checked) return <div className="center-page boot">{t('loading')}</div>
  if (!session) return <SignIn meta={meta} onSignedIn={setUser} />

  const hasSidebar = visiblePages(session.can).length > 0

  if (session.user.password_expired) {
    return (
      <SessionProvider value={session}>
        <ExpiredPassword onSignOut={signOut} />
      </SessionProvider>
    )
  }

  return (
    <SessionProvider value={session}>
      <Frame hasSidebar={hasSidebar} user={session.user.id} onSignOut={signOut} />
    </SessionProvider>
  )
}

function Frame({ hasSidebar, user, onSignOut }: { hasSidebar: boolean; user: string; onSignOut: () => void }) {
  const [collapsed, toggle] = useCollapsed(user)
  const [width, setWidth] = useSidebarWidth(user)
  const mobile = useMobile()
  if (mobile) {
    return (
      <MobileFrame onSignOut={onSignOut}>
        <Pages mobile />
      </MobileFrame>
    )
  }
  return (
    <div className="app-shell">
      <TopBar onSignOut={onSignOut} sidebar={hasSidebar ? { collapsed, toggle } : undefined} />
      <div
        className={`app-body ${hasSidebar ? (collapsed ? 'side-collapsed' : '') : 'no-sidebar'}`}
        style={{ '--side-width': `${width}px` } as CSSProperties}
      >
        {hasSidebar && <Sidebar collapsed={collapsed} />}
        {hasSidebar && !collapsed && <SidebarResizer width={width} onChange={setWidth} />}
        <div id={SCROLL_ROOT_ID} className="app-main">
          <Pages />
        </div>
      </div>
    </div>
  )
}

// The phone layout starts on the overview and keeps the pages made for a computer behind a note.
type Target = ReturnType<typeof resolve> | { kind: 'overview' }

function Pages({ mobile = false }: { mobile?: boolean }) {
  const { path, navigate } = useRouter()
  const { user, refresh, can } = useSession()
  const [forced, setForced] = useState(forcedPages)
  const home = mobile && hasOverview(can)
  const target: Target = home && path === OVERVIEW_PATH ? { kind: 'overview' } : home && path === HOME_PATH ? { kind: 'redirect', to: OVERVIEW_PATH } : resolve(path, can)

  useEffect(() => {
    if (user.source === 'local') void refresh()
  }, [path, user.source, refresh])

  useEffect(() => {
    if (target.kind === 'redirect') navigate(target.to, { replace: true })
  }, [target, navigate])

  if (target.kind === 'redirect') return null
  const key = target.kind === 'page' ? target.page.id : target.kind
  const desk = mobile && target.kind === 'page' && !mobileReady(target.page) && !forced.has(target.page.id)

  return (
    <AnimatePresence mode="wait" initial={false}>
      <motion.main
        key={key}
        className="page"
        initial={{ opacity: 0, y: 10 }}
        animate={{ opacity: 1, y: 0 }}
        exit={{ opacity: 0, y: -6 }}
        transition={{ duration: 0.2, ease: [0.22, 1, 0.36, 1] }}
      >
        {target.kind === 'overview' ? (
          <PageBoundary resetKey={path}>
            <PasswordExpiryNotice link />
            <Overview />
          </PageBoundary>
        ) : target.kind === 'profile' ? (
          <PageBoundary resetKey={path}>
            <ProfilePage />
          </PageBoundary>
        ) : desk ? (
          <DesktopOnly page={target.page} onOpen={() => setForced(forcedPages())} />
        ) : (
          <>
            <PasswordExpiryNotice link />
            {target.page.subtitle && !mobile && <PageHead page={target.page} />}
            <PageBoundary resetKey={path}>
              <target.page.Component />
            </PageBoundary>
          </>
        )}
      </motion.main>
    </AnimatePresence>
  )
}
