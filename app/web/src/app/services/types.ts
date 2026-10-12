export const CRITICALITIES = ['critical', 'high', 'medium', 'low'] as const
export const STATUSES = ['active', 'planned', 'retired'] as const

export type Criticality = (typeof CRITICALITIES)[number]
export type Status = (typeof STATUSES)[number]

export type Link = { title: string; url: string }

export type TeamLabel = { id: string; name: string; path: string[]; deleted: boolean }

export type ServiceRef = { id: string; name: string }

export type ServiceCI = { id: string; name: string; kind: string; status: string; source: string; netbox: boolean; deleted: boolean }

export type ServiceNetBox = { tag_id: number; slug: string; name: string; url: string; synced_at?: string }

export type Service = {
  id: string
  name: string
  description: string
  owner_team_id: string
  team_ids: string[]
  criticality: Criticality
  status: Status
  tags: string[]
  links: Link[]
  depends_on: string[]
  ci_ids: string[]
  cis: ServiceCI[]
  netbox?: ServiceNetBox
  created_at: string
  updated_at: string
  owner: TeamLabel
  teams: TeamLabel[]
  dependencies: ServiceRef[]
  dependents: ServiceRef[]
}

export type ServiceList = { services: Service[]; tags: string[]; total: number }

export type ServiceInput = {
  name: string
  description: string
  owner_team_id: string
  team_ids: string[]
  criticality: Criticality
  status: Status
  tags: string[]
  links: Link[]
  depends_on: string[]
}

export type TeamRef = { id: string; name: string; parent_id: string }

export type Refs = { teams: TeamRef[] }

export type Filters = { q: string; owner: string; criticality: string; status: string; tag: string }

export const NO_FILTERS: Filters = { q: '', owner: '', criticality: '', status: '', tag: '' }

export function blankInput(): ServiceInput {
  return { name: '', description: '', owner_team_id: '', team_ids: [], criticality: 'medium', status: 'active', tags: [], links: [], depends_on: [] }
}

export function inputOf(s: Service): ServiceInput {
  return {
    name: s.name,
    description: s.description,
    owner_team_id: s.owner_team_id,
    team_ids: [...s.team_ids],
    criticality: s.criticality,
    status: s.status,
    tags: [...s.tags],
    links: s.links.map((l) => ({ ...l })),
    depends_on: [...s.depends_on],
  }
}

export function queryOf(f: Filters) {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(f)) if (v.trim()) p.set(k, v.trim())
  const s = p.toString()
  return s ? `?${s}` : ''
}
