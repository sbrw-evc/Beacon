import type { Method, Severity } from '../incidents/types'

export type Ref = { id: string; name: string; missing?: boolean }

export type Sort = 'newest' | 'oldest'
export type BoardTheme = 'dark' | 'light'
export type BoardLocale = '' | 'ru' | 'en'
export type { Method }

// Wallboard is WallboardView of the admin API: the stored board, names of its targets and its public path.
export type Wallboard = {
  id: string
  /** The viewer may change the wallboard: its targets are within their visibility scope. */
  in_scope?: boolean
  slug: string
  title: string
  description: string
  enabled: boolean
  ci_ids: string[]
  service_ids: string[]
  team_ids: string[]
  severities: string[]
  methods: string[]
  show_acknowledged: boolean
  show_suppressed: boolean
  resolved_minutes: number
  sort: Sort
  refresh_seconds: number
  theme: BoardTheme
  locale: BoardLocale
  timezone: string
  allowed_networks: string[]
  created_by: string
  created_at: string
  updated_by: string
  updated_at: string
  cis: Ref[]
  services: Ref[]
  teams: Ref[]
  path: string
}

export type List = { wallboards: Wallboard[]; client_ip: string }

export type Targets = { cis: Ref[]; services: Ref[]; teams: Ref[] }

export type TargetKind = keyof Targets

// Input is WallboardInput: exactly the editable fields, the server rejects unknown ones.
export type Input = {
  slug: string
  title: string
  description: string
  enabled: boolean
  ci_ids: string[]
  service_ids: string[]
  team_ids: string[]
  severities: string[]
  methods: string[]
  show_acknowledged: boolean
  show_suppressed: boolean
  resolved_minutes: number
  sort: Sort
  refresh_seconds: number
  theme: BoardTheme
  locale: BoardLocale
  timezone: string
  allowed_networks: string[]
}

export type PreviewIncident = {
  id: string
  title: string
  ci_name: string
  ci_kind?: string
  signal: string
  method: string
  severity: Severity
  status: 'open' | 'acknowledged' | 'resolved'
  opened_at: string
  first_seen: string
  last_seen: string
  resolved_at: string | null
  acked_by: string
  count: number
  suppressed: boolean
  fallback: boolean
  services: string[] | null
  team: string
}

export type PreviewCounts = { total: number; acknowledged: number; open: number } & Record<Severity, number>

export type Preview = {
  board: { slug: string; title: string; refresh_seconds: number; theme: BoardTheme; locale: BoardLocale; sort: Sort }
  generated_at: string
  ready: boolean
  counts: PreviewCounts
  incidents: PreviewIncident[] | null
  more: boolean
}
