import { useEffect, useState, type ReactNode } from 'react'
import { ChevronRight, LayoutDashboard, Menu, Monitor, UserRound } from 'lucide-react'
import { motion } from 'motion/react'
import { useT } from '../../i18n'
import { Link, SCROLL_ROOT_ID, useRouter } from '../../router'
import { spring } from '../../ui'
import { IncidentLights } from '../IncidentLights'
import { navStrings } from '../navStrings'
import { NotificationCenter } from '../NotificationCenter'
import { HOME_PATH, owns, PROFILE_PATH, visiblePages, type PageDef } from '../pages'
import { SEVERITIES, SEVERITY_TONE } from '../incidents/types'
import { useSession } from '../session'
import { strings } from '../strings'
import { UserMenu } from '../UserMenu'
import { useLightCounts } from './lightsStore'
import { hasOverview, mobileReady, OVERVIEW_PATH, TAB_SLOTS } from './mobilePages'
import { mobileStrings } from './mobileStrings'
import { Sheet } from './Sheet'
import './mobile.css'

// MobileFrame is the shell of the phone layout: the title of the page with the incident light,
// notifications and the user menu on top, the page in between and the tab bar at the bottom.
export function MobileFrame({ onSignOut, children }: { onSignOut: () => void; children: ReactNode }) {
  const t = useT(mobileStrings)
  const { path } = useRouter()
  const { can } = useSession()
  const [more, setMore] = useState(false)
  const pages = visiblePages(can)
  const tabs = TAB_SLOTS.flatMap((slot) => pages.find((p) => slot.includes(p.id)) ?? [])
  const inTabs = path === OVERVIEW_PATH || tabs.some((p) => owns(p, path))

  useEffect(() => setMore(false), [path])

  return (
    <div className="m-shell">
      <TopBar onSignOut={onSignOut} pages={pages} />
      <div id={SCROLL_ROOT_ID} className="m-main">
        {children}
      </div>
      <nav className="m-tabs" aria-label={t('m.tabs')}>
        {hasOverview(can) && <Tab to={OVERVIEW_PATH} icon={LayoutDashboard} label="m.tab.overview" active={path === OVERVIEW_PATH} />}
        {tabs.map((p) => (
          <Tab key={p.id} to={p.path} icon={p.icon} label={`m.tab.${p.id}`} active={owns(p, path)} badge={p.id === 'incidents'} />
        ))}
        <TabButton icon={Menu} label="m.tab.more" active={more || !inTabs} onClick={() => setMore(true)} />
      </nav>
      <MoreSheet open={more} onClose={() => setMore(false)} pages={pages} tabs={tabs} />
    </div>
  )
}

function useTitle(pages: PageDef[]) {
  const { path } = useRouter()
  const t = useT(navStrings)
  const tm = useT(mobileStrings)
  const ts = useT(strings)
  if (path === OVERVIEW_PATH) return tm('m.ov.title')
  if (path === PROFILE_PATH) return ts('nav.profile')
  const page = pages.find((p) => owns(p, path))
  return page ? t(`page.${page.id}`) : ''
}

function TopBar({ onSignOut, pages }: { onSignOut: () => void; pages: PageDef[] }) {
  const title = useTitle(pages)
  return (
    <header className="m-top">
      <Link to={HOME_PATH} className="m-top-brand" aria-label="Umbrella">
        <img src="/logo.svg" alt="" width={28} height={28} />
      </Link>
      <h1 className="m-top-title">{title}</h1>
      <div className="m-top-end">
        <IncidentLights compact />
        <NotificationCenter />
        <UserMenu onSignOut={onSignOut} />
      </div>
    </header>
  )
}

type Icon = PageDef['icon']

function Badge() {
  const t = useT(mobileStrings)
  const counts = useLightCounts()
  if (!counts) return null
  const top = SEVERITIES.find((s) => (counts[s] ?? 0) > 0)
  const n = SEVERITIES.reduce((sum, s) => sum + (counts[s] ?? 0), 0)
  if (!top) return null
  return (
    <span className={`m-tab-badge tone-${SEVERITY_TONE[top]}`} aria-label={t('m.badge', { n })}>
      {n > 99 ? '99+' : n}
    </span>
  )
}

function TabInner({ icon: Icon, label, active, badge }: { icon: Icon; label: string; active: boolean; badge?: boolean }) {
  const t = useT(mobileStrings)
  return (
    <>
      {active && <motion.span layoutId="m-tab-pill" className="m-tab-pill" transition={spring} />}
      <span className="m-tab-icon">
        <Icon size={21} aria-hidden />
        {badge && <Badge />}
      </span>
      <span className="m-tab-label">{t(label)}</span>
    </>
  )
}

function Tab({ to, active, ...rest }: { to: string; icon: Icon; label: string; active: boolean; badge?: boolean }) {
  return (
    <Link to={to} className={`m-tab${active ? ' active' : ''}`} aria-current={active ? 'page' : undefined}>
      <TabInner active={active} {...rest} />
    </Link>
  )
}

function TabButton({ onClick, active, ...rest }: { icon: Icon; label: string; active: boolean; onClick: () => void }) {
  return (
    <button type="button" className={`m-tab${active ? ' active' : ''}`} onClick={onClick} aria-haspopup="dialog">
      <TabInner active={active} {...rest} />
    </button>
  )
}

// MoreSheet lists the sections that are not in the tab bar: first those made for the phone, then
// those better opened on a computer (they still open here, after a note).
function MoreSheet({ open, onClose, pages, tabs }: { open: boolean; onClose: () => void; pages: PageDef[]; tabs: PageDef[] }) {
  const t = useT(mobileStrings)
  const tn = useT(navStrings)
  const { path } = useRouter()
  const rest = pages.filter((p) => !tabs.includes(p))
  const phone = rest.filter(mobileReady)
  const desk = rest.filter((p) => !mobileReady(p))
  return (
    <Sheet open={open} title={t('m.more.title')} onClose={onClose}>
      {phone.length > 0 && (
        <div className="m-more-grid">
          {phone.map((p) => (
            <Link key={p.id} to={p.path} className={`m-more-tile${owns(p, path) ? ' active' : ''}`}>
              <span className="m-more-icon">
                <p.icon size={20} aria-hidden />
              </span>
              <span>{tn(`page.${p.id}`)}</span>
            </Link>
          ))}
        </div>
      )}
      <Link to={PROFILE_PATH} className={`m-more-row${path === PROFILE_PATH ? ' active' : ''}`}>
        <UserRound size={18} aria-hidden />
        <span>{t('m.more.profile')}</span>
        <ChevronRight size={16} aria-hidden className="m-more-chev" />
      </Link>
      {desk.length > 0 && (
        <section className="m-more-desk">
          <h3>
            <Monitor size={15} aria-hidden />
            {t('m.more.desktop')}
          </h3>
          <p className="muted">{t('m.more.desktop.hint')}</p>
          <div className="m-more-list">
            {desk.map((p) => (
              <Link key={p.id} to={p.path} className={`m-more-row${owns(p, path) ? ' active' : ''}`}>
                <p.icon size={17} aria-hidden />
                <span>{tn(`page.${p.id}`)}</span>
                <ChevronRight size={16} aria-hidden className="m-more-chev" />
              </Link>
            ))}
          </div>
        </section>
      )}
    </Sheet>
  )
}
