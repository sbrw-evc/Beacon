import { Fragment, useCallback, useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Building2, ChevronDown, LayoutGrid, Settings, Workflow, type LucideIcon } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { api } from '../api'
import { useLocale, useT } from '../i18n'
import { Link, useRouter } from '../router'
import { spring } from '../ui'
import { navStrings } from './navStrings'
import { owns, visiblePages, type PageDef } from './pages'
import type { Catalog } from './roles/permissions'
import { useSession } from './session'

const COMPACT = '(max-width: 860px)'
const openKey = (group: string) => `umbrella.sidebar.${group}`
const collapsedKey = (user: string) => `umbrella.sidebar.collapsed.${user}`
const widthKey = (user: string) => `umbrella.sidebar.width.${user}`
// The width of the expanded sidebar the user can drag between these bounds.
export const SIDEBAR_WIDTH = { min: 232, max: 440, initial: 280 }
const WIDTH_STEP = 16
// The groups, their titles and order come from the access catalog; only the icons are here.
const GROUP_ICONS: Record<string, LucideIcon> = { overview: LayoutGrid, automation: Workflow, org: Building2, settings: Settings }

// NavGroup is a group of the menu with the pages of it the user sees.
type NavGroup = { id: string; title: string; pages: PageDef[] }

let catalogLoad: Promise<Catalog> | null = null

// loadCatalog reads the access catalog once; it is the same for every user.
function loadCatalog() {
  catalogLoad ??= api<Catalog>('GET', '/api/access/catalog').catch((e: unknown) => {
    catalogLoad = null
    throw e
  })
  return catalogLoad
}

// useMenu groups the pages as the access catalog does, in its order. Without the catalog the
// pages are one group.
function useMenu(pages: PageDef[], fallbackTitle: string): NavGroup[] {
  const { locale } = useLocale()
  const [catalog, setCatalog] = useState<Catalog | 'failed' | null>(null)
  useEffect(() => {
    let live = true
    loadCatalog().then(
      (c) => live && setCatalog(c),
      () => live && setCatalog('failed'),
    )
    return () => {
      live = false
    }
  }, [])
  if (catalog === null) return []
  if (catalog === 'failed') return pages.length > 0 ? [{ id: 'all', title: fallbackTitle, pages }] : []
  const byID = new Map(pages.map((p) => [p.id, p]))
  return catalog.groups
    .map((g) => ({
      id: g.id,
      title: g.title[locale],
      pages: catalog.pages.filter((p) => p.group === g.id).flatMap((p) => byID.get(p.id) ?? []),
    }))
    .filter((g) => g.pages.length > 0)
}

function useCompact() {
  return useSyncExternalStore(
    (notify) => {
      const mq = window.matchMedia(COMPACT)
      mq.addEventListener('change', notify)
      return () => mq.removeEventListener('change', notify)
    },
    () => window.matchMedia(COMPACT).matches,
  )
}

// readOpen: a group the user has not opened or closed yet is open when it is the first one.
function readOpen(group: string, first: boolean) {
  try {
    const v = window.localStorage.getItem(openKey(group))
    return v === null ? first : v === '1'
  } catch {
    return first
  }
}

function readCollapsed(user: string) {
  try {
    return window.localStorage.getItem(collapsedKey(user)) === '1'
  } catch {
    return false
  }
}

// useCollapsed keeps the collapsed state of the sidebar per user in this browser.
export function useCollapsed(user: string) {
  const [collapsed, setCollapsed] = useState(() => readCollapsed(user))
  useEffect(() => setCollapsed(readCollapsed(user)), [user])
  const toggle = useCallback(
    () =>
      setCollapsed((v) => {
        try {
          window.localStorage.setItem(collapsedKey(user), v ? '0' : '1')
        } catch {
          // The choice then lasts until the page is reloaded.
        }
        return !v
      }),
    [user],
  )
  return [collapsed, toggle] as const
}

const clampWidth = (w: number) => Math.round(Math.min(SIDEBAR_WIDTH.max, Math.max(SIDEBAR_WIDTH.min, w)))

function readWidth(user: string) {
  try {
    const v = Number(window.localStorage.getItem(widthKey(user)))
    return v > 0 ? clampWidth(v) : SIDEBAR_WIDTH.initial
  } catch {
    return SIDEBAR_WIDTH.initial
  }
}

function writeWidth(user: string, w: number) {
  try {
    if (w === SIDEBAR_WIDTH.initial) window.localStorage.removeItem(widthKey(user))
    else window.localStorage.setItem(widthKey(user), String(w))
  } catch {
    // The width then lasts until the page is reloaded.
  }
}

// useSidebarWidth keeps the width of the expanded sidebar per user in this browser.
export function useSidebarWidth(user: string) {
  const [width, setWidth] = useState(() => readWidth(user))
  useEffect(() => setWidth(readWidth(user)), [user])
  const change = useCallback(
    (w: number) => {
      const next = clampWidth(w)
      setWidth(next)
      writeWidth(user, next)
    },
    [user],
  )
  return [width, change] as const
}

// SidebarResizer is the edge between the sidebar and the page: dragging it changes the width
// of the sidebar, arrow keys move it by a step, a double click returns the initial width.
export function SidebarResizer({ width, onChange }: { width: number; onChange: (w: number) => void }) {
  const t = useT(navStrings)
  const [dragging, setDragging] = useState(false)
  const start = useRef<{ x: number; width: number } | null>(null)

  useEffect(() => {
    if (!dragging) return
    document.body.classList.add('side-resizing')
    return () => document.body.classList.remove('side-resizing')
  }, [dragging])

  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-controls="app-sidebar"
      aria-label={t('nav.resize')}
      title={t('nav.resize.hint')}
      aria-valuemin={SIDEBAR_WIDTH.min}
      aria-valuemax={SIDEBAR_WIDTH.max}
      aria-valuenow={width}
      tabIndex={0}
      className={`side-resizer ${dragging ? 'dragging' : ''}`}
      onPointerDown={(e) => {
        if (e.button !== 0) return
        e.preventDefault()
        e.currentTarget.setPointerCapture(e.pointerId)
        start.current = { x: e.clientX, width }
        setDragging(true)
      }}
      onPointerMove={(e) => {
        if (start.current) onChange(start.current.width + e.clientX - start.current.x)
      }}
      onPointerUp={() => {
        start.current = null
        setDragging(false)
      }}
      onPointerCancel={() => {
        start.current = null
        setDragging(false)
      }}
      onDoubleClick={() => onChange(SIDEBAR_WIDTH.initial)}
      onKeyDown={(e) => {
        const step = e.shiftKey ? WIDTH_STEP * 4 : WIDTH_STEP
        if (e.key === 'ArrowLeft') onChange(width - step)
        else if (e.key === 'ArrowRight') onChange(width + step)
        else if (e.key === 'Home') onChange(SIDEBAR_WIDTH.min)
        else if (e.key === 'End') onChange(SIDEBAR_WIDTH.max)
        else return
        e.preventDefault()
      }}
    />
  )
}

// Tip is the label shown next to an icon of the collapsed sidebar. It is drawn in a portal
// because the sidebar scrolls and would clip it.
function Tip({ anchor, children }: { anchor: HTMLElement | null; children: ReactNode }) {
  if (!anchor) return null
  const r = anchor.getBoundingClientRect()
  return createPortal(
    <motion.div
      role="tooltip"
      className="side-tip"
      style={{ top: r.top + r.height / 2, left: r.right + 10, y: '-50%' }}
      initial={{ opacity: 0, x: -4 }}
      animate={{ opacity: 1, x: 0 }}
      transition={{ duration: 0.12 }}
    >
      {children}
    </motion.div>,
    document.body,
  )
}

function writeOpen(group: string, v: boolean) {
  try {
    window.localStorage.setItem(openKey(group), v ? '1' : '0')
  } catch {
    return
  }
}

function SideLink({ page, active, rail }: { page: PageDef; active: boolean; rail?: boolean }) {
  const t = useT(navStrings)
  const compact = useCompact()
  const [tip, setTip] = useState<HTMLElement | null>(null)
  const label = t(`page.${page.id}`)
  const show = (e: { currentTarget: HTMLElement }) => rail && !compact && setTip(e.currentTarget)
  const hide = () => setTip(null)
  useEffect(() => {
    if (!rail) setTip(null)
  }, [rail])
  return (
    <Link
      to={page.path}
      className={`side-link ${active ? 'active' : ''}`}
      aria-current={active ? 'page' : undefined}
      aria-label={rail ? label : undefined}
      onMouseEnter={show}
      onMouseLeave={hide}
      onFocus={show}
      onBlur={hide}
      onClick={hide}
    >
      {active && <motion.span layoutId="side-pill" className="side-pill" transition={spring} />}
      <page.icon size={17} aria-hidden />
      <span className="side-label" title={rail ? undefined : label}>
        {label}
      </span>
      {rail && <Tip anchor={tip}>{label}</Tip>}
    </Link>
  )
}

function GroupAccordion({ group, first, path }: { group: NavGroup; first: boolean; path: string }) {
  const compact = useCompact()
  const { pages } = group
  const Icon = GROUP_ICONS[group.id] ?? LayoutGrid
  const hasActive = pages.some((p) => owns(p, path))
  const [open, setOpen] = useState(() => hasActive || readOpen(group.id, first))

  useEffect(() => {
    if (hasActive) setOpen(true)
  }, [hasActive])

  const toggle = () =>
    setOpen((v) => {
      writeOpen(group.id, !v)
      return !v
    })
  const shown = open || compact

  return (
    <div className="side-group">
      <button type="button" className={`side-accordion ${hasActive ? 'has-active' : ''}`} aria-expanded={shown} onClick={toggle}>
        <Icon size={17} aria-hidden />
        <span>{group.title}</span>
        <motion.span className="side-chevron" animate={{ rotate: shown ? 180 : 0 }} transition={spring}>
          <ChevronDown size={16} />
        </motion.span>
      </button>
      <AnimatePresence initial={false}>
        {shown && (
          <motion.div
            className="side-children"
            initial={compact ? false : { height: 0, opacity: 0 }}
            animate={{ height: 'auto', opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            transition={{ duration: 0.22, ease: [0.22, 1, 0.36, 1] }}
          >
            <div className="side-children-inner">
              {pages.map((p) => (
                <SideLink key={p.id} page={p} active={owns(p, path)} />
              ))}
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  )
}

export function Sidebar({ collapsed }: { collapsed: boolean }) {
  const t = useT(navStrings)
  const { path } = useRouter()
  const { can } = useSession()
  const nav = useRef<HTMLElement>(null)
  const groups = useMenu(visiblePages(can), t('nav.label'))

  useEffect(() => {
    const el = nav.current
    const active = el?.querySelector<HTMLElement>('.side-link.active')
    if (!el || !active || el.scrollWidth <= el.clientWidth) return
    const box = el.getBoundingClientRect()
    const item = active.getBoundingClientRect()
    el.scrollBy({ left: item.left - box.left - box.width / 2 + item.width / 2, behavior: 'smooth' })
  }, [path, collapsed])

  return (
    <nav ref={nav} id="app-sidebar" className={`sidebar ${collapsed ? 'collapsed' : ''}`} aria-label={t('nav.label')}>
      {collapsed
        ? groups.map((g, i) => (
            <Fragment key={g.id}>
              {i > 0 && <span className="side-sep" aria-hidden />}
              <div className="side-group" role="group" aria-label={g.title}>
                {g.pages.map((p) => (
                  <SideLink key={p.id} page={p} active={owns(p, path)} rail />
                ))}
              </div>
            </Fragment>
          ))
        : groups.map((g, i) => <GroupAccordion key={g.id} group={g} first={i === 0} path={path} />)}
    </nav>
  )
}
