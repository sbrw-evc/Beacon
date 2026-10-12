// The alert scale, most severe first: the one list of the interface (model.Severities on the server).
// The stored names predate priorities; each level is shown as its priority and word (sev.<name>).
export const SEVERITIES = ['critical', 'error', 'warning', 'low', 'info'] as const
export const STATUSES = ['active', 'open', 'acknowledged', 'resolved', 'all'] as const
// Methods of an alert (model.Methods); rules are RED or USE only.
export const METHODS = ['red', 'use', 'other'] as const
export const RULE_METHODS = ['red', 'use'] as const

export type Severity = (typeof SEVERITIES)[number]
export type Method = (typeof METHODS)[number]
export type RuleMethod = (typeof RULE_METHODS)[number]

// SEVERITY_TONE is the colour each severity is shown in (model.Severity.Tone).
export const SEVERITY_TONE: Record<Severity, string> = { critical: 'critical', error: 'error', warning: 'warn', low: 'low', info: 'info' }

// SEVERITY_PRIORITY is the incident priority of each severity (model.Severity.Priority).
export const SEVERITY_PRIORITY: Record<Severity, string> = { critical: 'P1', error: 'P2', warning: 'P3', low: 'P4', info: 'P5' }

// severityText shows a severity as its priority and word, e.g. "P1 · Critical"; t must know the
// common words (sev.<name>). An unknown name shows as it is.
export function severityText(t: (key: string) => string, s: string) {
  const p = SEVERITY_PRIORITY[s as Severity]
  return p ? `${p} · ${t(`sev.${s}`)}` : s
}

export type Source = {
  connector_id: string
  key: string
  status: 'firing' | 'resolved'
  severity: Severity
  title: string
  value?: string
  // description and fields are what the connector shows of the alert: its full text and named values.
  description?: string
  fields?: { name: string; value: string }[]
  first_seen: string
  last_seen: string
}

export type Ref = { id: string; name: string }

export type Person = { user_id: string; name: string; email?: string; telegram?: string; role?: string }

export type Route = { services: Ref[]; team?: Ref; people: Person[]; owners: Person[]; via: 'service' | 'ci_owners' | 'none'; at: string }

export type PD = {
  state: 'pending' | 'accepted' | 'acked' | 'failed' | 'skipped' | 'off' | 'standby'
  escalated?: boolean
  // The PagerDuty service (queue) the incident is in.
  queue?: string
  queue_name?: string
  key: string
  route?: string
  error?: string
  error_code?: string
  retry?: string
  attempt_at?: string
  incident_id?: string
  incident_url?: string
}

export type Incident = {
  id: string
  title: string
  ci_id?: string
  ci_name: string
  ci_kind?: string
  signal: string
  method: string
  severity: Severity
  status: 'open' | 'acknowledged' | 'resolved'
  sources: Record<string, Source>
  labels: Record<string, string>
  count: number
  first_seen: string
  opened_at: string
  last_seen: string
  resolved_at?: string
  resolved_by?: string
  acked_by?: string
  acked_at?: string
  suppressed: boolean
  maintenance_id?: string
  // excluded: the host is marked «Не является КЕ» on the monitoring systems page.
  excluded?: boolean
  route: Route
  pd: PD
  fallback: boolean
  fallback_at?: string
  fallback_state?: 'pending' | 'sending' | 'sent'
  follow_up?: 'acknowledged' | 'resolved'
  related_id?: string
}

export type Counts = {
  active: number
  open: number
  acknowledged: number
  by_severity: Partial<Record<Severity, number>>
  pd_not_taken: number
  fallback: number
  suppressed: number
  unbound: number
  // PagerDuty is turned on; while it is off its tile and column are hidden.
  pd_enabled: boolean
}

export type Page = { alerts: Incident[]; counts: Counts; more: boolean }

export type Entry = { id: number; at: string; kind: string; code: string; args?: Record<string, string>; author?: string }

export type CardCI = { id: string; name: string; kind: string; imported: boolean; netbox_url?: string; inventory_url?: string; aliases: string[] }

export type CardService = { id: string; name: string; links: { title: string; url: string }[] }

export type CardMaintenance = { id: string; title: string; start: string; end: string }

export type Detail = {
  alert: Incident
  timeline: Entry[]
  grafana_url?: string
  // The traffic of the incident in Umbrella Flow through Umbrella (/go/incidents/{id}/flow).
  flow_url?: string
  connectors: Record<string, string>
  // What the catalog has on the item, the services and the maintenance window of the incident.
  ci?: CardCI
  services?: CardService[]
  maintenance?: CardMaintenance
}

// firing counts the sources of an incident that still fire: resolving it by hand while they do
// opens it again when the next event comes within the reopen window.
export function firing(a: Incident) {
  return Object.values(a.sources).filter((s) => s.status === 'firing').length
}

export type Flag = '' | 'pd' | 'fallback' | 'suppressed'

export type Filters = { q: string; status: string; severity: string; method: string; team: string; service: string; flag: Flag }

export const NO_FILTERS: Filters = { q: '', status: 'active', severity: '', method: '', team: '', service: '', flag: '' }

export function queryOf(f: Filters) {
  const p = new URLSearchParams()
  if (f.status && f.status !== 'all') p.set('status', f.status)
  if (f.severity) p.set('severity', f.severity)
  if (f.method) p.set('method', f.method)
  if (f.team) p.set('team', f.team)
  if (f.service) p.set('service', f.service)
  if (f.q.trim()) p.set('q', f.q.trim())
  if (f.flag === 'pd') p.set('pd', 'failed')
  if (f.flag === 'fallback') p.set('fallback', 'true')
  if (f.flag === 'suppressed') p.set('suppressed', 'true')
  const s = p.toString()
  return s ? `?${s}` : ''
}

// The filters live in the address, so a link (PagerDuty, a message) opens the same view.
export function filtersFromURL(search: string): Filters {
  const p = new URLSearchParams(search)
  const f = { ...NO_FILTERS }
  for (const k of ['q', 'status', 'severity', 'method', 'team', 'service'] as const) {
    const v = p.get(k)
    if (v !== null) f[k] = v
  }
  const flag = p.get('flag')
  if (flag === 'pd' || flag === 'fallback' || flag === 'suppressed') f.flag = flag
  return f
}

export const INCIDENTS_PATH = '/incidents'

export function urlOf(f: Filters, id: string | null) {
  const p = new URLSearchParams()
  for (const k of ['q', 'severity', 'method', 'team', 'service', 'flag'] as const) if (f[k]) p.set(k, f[k])
  if (f.status !== NO_FILTERS.status) p.set('status', f.status)
  if (id) p.set('id', id)
  const s = p.toString()
  return `${INCIDENTS_PATH}${s ? `?${s}` : ''}`
}
