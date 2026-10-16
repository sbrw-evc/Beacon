import type { ComponentType } from 'react'
import { Activity, BellRing, Bot, Boxes, CalendarClock, Briefcase, Cable, Database, Fingerprint, Gauge, KeyRound, LockKeyhole, Megaphone, Network, Radar, Server, Siren, Tv, UserCog, Users, UsersRound, Warehouse, Waypoints, type LucideIcon } from 'lucide-react'
import { CIsPage } from './cis/CIsPage'
import { IncidentsPage } from './incidents/IncidentsPage'
import { CMDBMapPage } from './cmdb/CMDBMapPage'
import { ConnectorsPage } from './connectors/ConnectorsPage'
import { CredentialsPage } from './connectors/CredentialsPage'
import { MaintenancePage } from './maintenance/MaintenancePage'
import { MonitoringPage } from './monitoring/MonitoringPage'
import { NetBoxPage } from './netbox/NetBoxPage'
import { InventoryDBPage } from './inventorydb/InventoryDBPage'
import { ResponsePage } from './response/ResponsePage'
import { RulesPage } from './rules/RulesPage'
import { RolesPage } from './roles/RolesPage'
import { ServicesPage } from './services/ServicesPage'
import { AlertingSettings } from './settings/alerting/AlertingSettings'
import { DirectorySettings } from './settings/directory/DirectorySettings'
import { OpenBaoSettings } from './settings/openbao/OpenBaoSettings'
import { SpaSettings } from './settings/spa/SpaSettings'
import { PostgresSettings } from './settings/postgres/PostgresSettings'
import { ServiceAccountsPage } from './settings/serviceaccounts/ServiceAccountsPage'
import { SystemStatus } from './SystemStatus'
import { TeamsPage } from './teams/TeamsPage'
import { UsersPage } from './users/UsersPage'
import { WallboardsPage } from './wallboards/WallboardsPage'

// PageDef is what the web app adds to a page of the access catalog (GET /api/access/catalog):
// its address, icon and component. The catalog has the group of the page and its place in the menu.
export type PageDef = {
  id: string
  path: string
  icon: LucideIcon
  Component: ComponentType
  subtitle?: string
  // nested: the page also owns the paths below its own, such as /connectors/{id}.
  nested?: boolean
}

export const HOME_PATH = '/'
export const PROFILE_PATH = '/profile'
export const SETTINGS_PATH = '/settings'
// Old addresses of pages that moved, so bookmarks keep working.
const MOVED: Record<string, string> = { '/status': '/settings/status', '/settings/identity': '/settings/spa', '/settings/password-policy': '/settings/spa' }

export const PAGES: PageDef[] = [
  { id: 'incidents', path: '/incidents', icon: BellRing, Component: IncidentsPage, subtitle: 'page.incidents.subtitle' },
  { id: 'cmdb', path: '/cmdb', icon: Waypoints, Component: CMDBMapPage, subtitle: 'page.cmdb.subtitle' },
  { id: 'cis', path: '/cis', icon: Boxes, Component: CIsPage, subtitle: 'page.cis.subtitle' },
  { id: 'services', path: '/services', icon: Briefcase, Component: ServicesPage, subtitle: 'page.services.subtitle' },
  { id: 'maintenance', path: '/maintenance', icon: CalendarClock, Component: MaintenancePage, subtitle: 'page.maintenance.subtitle' },
  { id: 'wallboards', path: '/wallboards', icon: Tv, Component: WallboardsPage, subtitle: 'page.wallboards.subtitle' },
  { id: 'connectors', path: '/connectors', icon: Cable, Component: ConnectorsPage, nested: true },
  { id: 'response', path: '/response', icon: Megaphone, Component: ResponsePage, subtitle: 'page.response.subtitle' },
  { id: 'rules', path: '/rules', icon: Gauge, Component: RulesPage, subtitle: 'page.rules.subtitle' },
  { id: 'netbox', path: '/netbox', icon: Server, Component: NetBoxPage, subtitle: 'page.netbox.subtitle' },
  { id: 'inventorydb', path: '/inventory-db', icon: Warehouse, Component: InventoryDBPage, subtitle: 'page.inventorydb.subtitle' },
  { id: 'monitoring', path: '/monitoring', icon: Radar, Component: MonitoringPage, subtitle: 'page.monitoring.subtitle' },
  { id: 'credentials', path: '/credentials', icon: LockKeyhole, Component: CredentialsPage, subtitle: 'page.credentials.subtitle' },
  { id: 'users', path: '/users', icon: Users, Component: UsersPage, subtitle: 'page.users.subtitle' },
  { id: 'teams', path: '/teams', icon: UsersRound, Component: TeamsPage, subtitle: 'page.teams.subtitle' },
  { id: 'roles', path: '/roles', icon: UserCog, Component: RolesPage, subtitle: 'page.roles.subtitle' },
  { id: 'settings.alerting', path: '/settings/alerting', icon: Siren, Component: AlertingSettings, subtitle: 'page.settings.alerting.subtitle' },
  { id: 'status', path: '/settings/status', icon: Activity, Component: SystemStatus },
  { id: 'settings.ldap', path: '/settings/ldap', icon: Network, Component: DirectorySettings, subtitle: 'page.settings.subtitle' },
  { id: 'settings.postgres', path: '/settings/postgresql', icon: Database, Component: PostgresSettings, subtitle: 'page.settings.subtitle' },
  { id: 'settings.openbao', path: '/settings/openbao', icon: KeyRound, Component: OpenBaoSettings, subtitle: 'page.settings.subtitle' },
  { id: 'settings.spa', path: '/settings/spa', icon: Fingerprint, Component: SpaSettings, subtitle: 'page.settings.spa.subtitle' },
  { id: 'serviceaccounts', path: '/settings/service-accounts', icon: Bot, Component: ServiceAccountsPage, subtitle: 'page.serviceaccounts.subtitle' },
]

export type Can = (perm: string) => boolean

export const viewPerm = (p: PageDef) => `${p.id}:view`

export function owns(p: PageDef, path: string) {
  return p.path === path || (!!p.nested && path.startsWith(p.path + '/'))
}

export function visiblePages(can: Can) {
  return PAGES.filter((p) => can(viewPerm(p)))
}

export type Resolved = { kind: 'page'; page: PageDef } | { kind: 'profile' } | { kind: 'redirect'; to: string }

export function resolve(path: string, can: Can): Resolved {
  if (path === PROFILE_PATH) return { kind: 'profile' }
  if (MOVED[path]) return { kind: 'redirect', to: MOVED[path] }
  const visible = visiblePages(can)
  const page = visible.find((p) => owns(p, path))
  if (page) return { kind: 'page', page }
  if (path === SETTINGS_PATH || path.startsWith(SETTINGS_PATH + '/')) {
    const first = visible.find((p) => p.path.startsWith(SETTINGS_PATH + '/'))
    if (first) return { kind: 'redirect', to: first.path }
  }
  return { kind: 'redirect', to: visible[0]?.path ?? PROFILE_PATH }
}
