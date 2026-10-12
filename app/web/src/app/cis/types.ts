export const KINDS = ['device', 'vm', 'service', 'other'] as const
export const STATUSES = ['active', 'planned', 'staged', 'offline', 'failed', 'decommissioning'] as const
export const SOURCES = ['netbox', 'inventory-db', 'local'] as const
export const FLAGS = ['no_owners', 'directory_missing', 'not_monitored'] as const

export type Kind = (typeof KINDS)[number]

export type Owner = { id: string; username: string; name: string; email: string; source: string; role: string; deleted: boolean }

export type NetBoxRef = { kind: 'device' | 'virtual-machine' | 'service'; id: number; url: string }

export type DirectoryInfo = {
  status: 'matched' | 'missing'
  dn?: string
  dns_name?: string
  os?: string
  os_version?: string
  last_logon?: string
  disabled: boolean
  checked_at: string
}

export type Attrs = {
  site?: string
  role?: string
  device_type?: string
  cluster?: string
  tenant?: string
  platform?: string
  parent?: string
  ports?: string
  serial?: string
}

export type CI = {
  id: string
  name: string
  kind: Kind
  status: string
  description: string
  source: 'netbox' | 'inventory-db' | 'local'
  ips: string[]
  aliases?: string[]
  tags: string[]
  attrs: Attrs
  netbox?: NetBoxRef
  inventory_db?: { id: number; url: string; ref: string }
  directory?: DirectoryInfo
  owners: Owner[]
  services: { id: string; name: string }[]
  monitoring: Monitor[]
  presence: Presence[]
  not_monitored: boolean
  editable: boolean
  registrable: boolean
  // The recent traffic of the item in Umbrella Flow through Umbrella (/go/cis/{id}/flow).
  flow_url?: string
  created_at: string
  created_by: string
  updated_at: string
  updated_by: string
  synced_at?: string
}

export type Monitor = {
  source_id: string
  source_name: string
  kind: 'zabbix' | 'prometheus' | 'grafana' | 'graylog'
  key: string
  host: string
  name: string
  state: string
  url?: string
  match: string
}

// Presence: whether a system (NetBox, the domain, a monitoring system) knows the item.
export type Presence = {
  kind: 'netbox' | 'inventory-db' | 'directory' | 'zabbix' | 'prometheus' | 'grafana' | 'graylog'
  source_id?: string
  name: string
  state: 'present' | 'missing'
  detail?: string
  host_state?: string
  url?: string
}

export type Summary = {
  total: number
  netbox: number
  inventory_db?: number
  local: number
  registered: number
  no_owners: number
  directory_missing: number
  not_monitored: number
  monitoring_sources: number
}

export type CIList = { items: CI[]; tags: string[]; summary: Summary }

export type CIInput = {
  name: string
  kind: Kind
  status: string
  description: string
  owner_ids: string[]
  ips: string[]
  tags: string[]
  register: boolean
}

export type UserRef = { id: string; username: string; name: string; source: string; disabled: boolean }

export type Filters = { q: string; kind: string; source: string; status: string; flag: string }

export const NO_FILTERS: Filters = { q: '', kind: '', source: '', status: '', flag: '' }

export function blankInput(): CIInput {
  return { name: '', kind: 'device', status: 'active', description: '', owner_ids: [], ips: [], tags: [], register: false }
}

export function inputOf(ci: CI): CIInput {
  return {
    name: ci.name,
    kind: ci.kind,
    status: ci.status,
    description: ci.description,
    owner_ids: ci.owners.map((o) => o.id),
    ips: [...ci.ips],
    tags: [...ci.tags],
    register: false,
  }
}

export function queryOf(f: Filters) {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(f)) if (v.trim()) p.set(k, v.trim())
  const s = p.toString()
  return s ? `?${s}` : ''
}

export const registrable = (kind: Kind) => kind === 'device' || kind === 'vm'
