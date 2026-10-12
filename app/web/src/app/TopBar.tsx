import { PanelLeftClose, PanelLeftOpen } from 'lucide-react'
import { useT } from '../i18n'
import { Link } from '../router'
import { Brand } from '../ui'
import { navStrings } from './navStrings'
import { HOME_PATH } from './pages'
import { IncidentLights } from './IncidentLights'
import { NotificationCenter } from './NotificationCenter'
import { UserMenu } from './UserMenu'

export function TopBar({ onSignOut, sidebar }: { onSignOut: () => void; sidebar?: { collapsed: boolean; toggle: () => void } }) {
  const t = useT(navStrings)
  const label = t(sidebar?.collapsed ? 'nav.expand' : 'nav.collapse')
  return (
    <header className="topbar">
      <div className="topbar-start">
        {sidebar && (
          <button type="button" className="icon-btn side-toggle" aria-label={label} title={label} aria-expanded={!sidebar.collapsed} aria-controls="app-sidebar" onClick={sidebar.toggle}>
            {sidebar.collapsed ? <PanelLeftOpen size={18} /> : <PanelLeftClose size={18} />}
          </button>
        )}
        <Link to={HOME_PATH} className="brand-link">
          <Brand />
        </Link>
      </div>
      <div className="topbar-end">
        <IncidentLights />
        <NotificationCenter />
        <UserMenu onSignOut={onSignOut} />
      </div>
    </header>
  )
}
