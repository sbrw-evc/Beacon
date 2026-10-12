import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type AnchorHTMLAttributes, type MouseEvent, type ReactNode } from 'react'

type NavigateOptions = { replace?: boolean }

// search and visit change with every navigation, also to the same page with other parameters
// (a link to an incident from a notification while the incident list is open).
type Router = { path: string; search: string; visit: number; navigate: (to: string, options?: NavigateOptions) => void }

const RouterContext = createContext<Router>({ path: '/', search: '', visit: 0, navigate: () => {} })

export const SCROLL_ROOT_ID = 'app-main'

function scrollToTop() {
  const root = document.getElementById(SCROLL_ROOT_ID)
  if (root) root.scrollTo({ top: 0 })
  else window.scrollTo({ top: 0 })
}

function currentPath() {
  return document.location.pathname || '/'
}

export function RouterProvider({ children }: { children: ReactNode }) {
  const [path, setPath] = useState(currentPath)
  const [visit, setVisit] = useState<{ search: string; n: number }>(() => ({ search: document.location.search, n: 0 }))
  const moved = useCallback(() => {
    setPath(currentPath())
    setVisit((v) => ({ search: document.location.search, n: v.n + 1 }))
  }, [])

  useEffect(() => {
    const onPop = () => moved()
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [moved])

  // shown is the page on screen. A page may rewrite the address without telling the router;
  // a click then still goes to its target instead of being taken for the current address.
  const shown = useRef(path)
  shown.current = path

  const navigate = useCallback((to: string, { replace = false }: NavigateOptions = {}) => {
    if (to === currentPath() && to === shown.current && !replace) return
    if (replace) window.history.replaceState(null, '', to)
    else window.history.pushState(null, '', to)
    moved()
    scrollToTop()
  }, [moved])

  const value = useMemo(() => ({ path, search: visit.search, visit: visit.n, navigate }), [path, visit, navigate])
  return <RouterContext.Provider value={value}>{children}</RouterContext.Provider>
}

export function useRouter() {
  return useContext(RouterContext)
}

function plainClick(e: MouseEvent<HTMLAnchorElement>) {
  return (
    e.button === 0 &&
    !e.metaKey &&
    !e.ctrlKey &&
    !e.shiftKey &&
    !e.altKey &&
    !e.defaultPrevented &&
    (!e.currentTarget.target || e.currentTarget.target === '_self')
  )
}

export function Link({ to, onClick, ...rest }: Omit<AnchorHTMLAttributes<HTMLAnchorElement>, 'href'> & { to: string }) {
  const { navigate } = useRouter()
  return (
    <a
      {...rest}
      href={to}
      onClick={(e) => {
        onClick?.(e)
        if (!plainClick(e)) return
        e.preventDefault()
        navigate(to)
      }}
    />
  )
}
