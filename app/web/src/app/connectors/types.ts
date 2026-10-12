import type { Locale } from '../../api'

export type Text = Record<Locale, string>

export type ParamKind = 'string' | 'text' | 'number' | 'bool' | 'select' | 'template' | 'cel' | 'path' | 'credential' | 'list' | 'table'

export type ParamSpec = {
  key: string
  kind: ParamKind
  title: Text
  help?: Text
  required?: boolean
  default?: unknown
  placeholder?: string
  options?: { value: string; title: Text }[]
  credential_types?: string[]
  columns?: ParamSpec[]
  min?: number
  max?: number
}

export type Category = 'trigger' | 'parse' | 'transform' | 'route' | 'output' | 'config'

export type NodeType = {
  type: string
  version: number
  category: Category
  title: Text
  description: Text
  params: ParamSpec[]
  inputs: number
  outputs: string[]
  dynamic_outputs?: boolean
  can_fail?: boolean
  singleton?: boolean
}

export type NodeTypes = { types: NodeType[]; severities: string[] }

export type OnError = '' | 'fail_record' | 'skip' | 'route_error'

export type GraphNode = {
  id: string
  type: string
  type_version: number
  name?: string
  params: Record<string, unknown>
  position: { x: number; y: number }
  disabled?: boolean
  on_error?: OnError
}

export type GraphEdge = { id: string; source: string; source_output: string; target: string; target_input?: string }

export type Graph = { nodes: GraphNode[]; edges: GraphEdge[] }

export type Issue = { level: 'error' | 'warning'; node_id?: string; edge_id?: string; param?: string; code: string; message: string }

export type Lock = { user_id: string; username: string; name: string; until: string; mine: boolean }

export type Capture = { remaining: number; until: string; by: string }

export type Summary = {
  received: number
  rejected: number
  failed: number
  events: number
  open_failures: number
  last_received?: string
}

export type Status = 'draft' | 'published' | 'changed' | 'publish_failed'

export type ConnectorSummary = {
  id: string
  slug: string
  name: string
  description: string
  tags: string[]
  preset?: string
  status: Status
  published: number
  published_at?: string
  published_by?: string
  created_at: string
  updated_at: string
  ingest_path: string
  lock?: Lock
  capture?: Capture
  samples: number
  stats?: Summary
}

export type Version = {
  number: number
  name: string
  comment: string
  created_at: string
  created_by: string
  current: boolean
  graph?: Graph
}

export type Connector = ConnectorSummary & {
  draft: { graph: Graph; pins: Pins | null; revision: number; updated_at: string; updated_by: string }
  issues: Issue[]
  versions: Version[]
  publish_error?: string
}

export type ConnectorList = { connectors: ConnectorSummary[]; ingest: boolean }

export type DraftSaved = { revision: number; status: Status; issues: Issue[] }

export type Preset = {
  id: string
  title: Text
  description: Text
  name: string
  tags: string[]
  credentials: Slot[]
  samples: number
  // quick is set for the presets quick connect offers: its order, the default connector name,
  // the monitoring system kinds it is the alert intake for and how many steps the source needs.
  quick?: { order: number; name: Text; monitoring_kinds: string[]; steps: number }
}

export type Slot = { slot: string; node: string; param: string; types: string[]; name?: string }

export type CredentialChoice = { id: string; name: string; type: string }

export type ImportCheck = {
  name: string
  description: string
  tags: string[]
  nodes: number
  samples: number
  credentials: Slot[]
  choices: CredentialChoice[]
}

export type Sample = {
  id: string
  name: string
  source: 'capture' | 'manual' | 'request' | 'preset' | string
  size: number
  format: string
  method?: string
  remote_ip?: string
  created_at: string
  created_by: string
  body?: string
  headers?: Record<string, string>
  query?: Record<string, string>
}

export type DataRecord = { data: Record<string, unknown>; raw?: string; lineage: { request?: string; item: number } }

export type Pins = Record<string, Record<string, DataRecord[]>>

export type Failure = { node: string; error: string; lineage: { request?: string; item: number }; data?: Record<string, unknown>; raw?: string }

export type NodeTrace = {
  in: number
  out: Record<string, number>
  input?: DataRecord[]
  output?: Record<string, DataRecord[]>
  errors?: Failure[]
  filtered?: number
  skipped?: number
  us: number
  pinned?: boolean
  disabled?: boolean
}

export type Event = {
  title: string
  ci: string
  signal: string
  method: string
  severity: string
  status: string
  external_id: string
  value: string
  description?: string
  fields?: { name: string; value: string }[]
  labels: Record<string, string>
  key: string
}

export type EventPreview = Event & { item: number; node: string }

export type RunResult = {
  events: unknown[]
  failures: Failure[]
  filtered: number
  skipped: number
  trace?: Record<string, NodeTrace>
  order?: string[]
}

export type TestRun = { issues: Issue[]; result: RunResult | null; events: EventPreview[] }

export type SampleCheck = {
  sample_id: string
  name: string
  events: EventPreview[]
  failures: Failure[]
  filtered: number
  skipped: number
  error?: string
}

export type TestAll = { issues: Issue[]; samples: SampleCheck[] }

export type IngestRequest = {
  id: number
  received_at: string
  version: number
  status: string
  attempts: number
  remote_ip: string
  method: string
  headers: Record<string, string>
  query: Record<string, string>
  size: number
  processed_at?: string
  events: number
  error?: string
  body?: string
  format?: string
}

export type StoredFailure = {
  id: number
  version: number
  node: string
  error: string
  request_id: number
  request_at: string
  item: number
  data?: Record<string, unknown>
  raw?: string
  created_at: string
  retries: number
  resolved_at?: string
}

export type StoredEvent = Event & {
  id: number
  version: number
  request_id: number
  item: number
  first_seen: string
  last_seen: string
  seen: number
}

export type Bucket = {
  at: string
  received: number
  rejected: number
  filtered: number
  skipped: number
  failed: number
  events: number
  duplicates: number
  latency_avg_ms: number
  latency_max_ms: number
}

export type Stats = { step_minutes: number; buckets: Bucket[] }

export type CredentialType = 'bearer' | 'basic' | 'header' | 'hmac'

export type Credential = {
  id: string
  name: string
  type: CredentialType
  description: string
  fields: Record<string, string>
  secrets_set: string[]
  version: number
  used_by: CredentialUse[]
  created_at: string
  created_by: string
  updated_at: string
  updated_by: string
}

// CredentialKind: the plain fields and the secret fields of a credential type, as the server
// describes them at /api/credentials/kinds.
export type CredentialKind = { type: CredentialType; fields: string[]; secrets: string[] }

export const CATEGORIES: Category[] = ['trigger', 'parse', 'transform', 'route', 'output', 'config']

export function typeKey(type: string, version: number) {
  return `${type}@${version}`
}

export function outputsOf(t: NodeType | undefined, n: GraphNode): string[] {
  if (!t) return []
  let out = [...(t.outputs ?? [])]
  if (t.dynamic_outputs) {
    const seen = new Set(['else', 'error'])
    out = []
    const rows = Array.isArray(n.params.rules) ? (n.params.rules as Record<string, unknown>[]) : []
    for (const r of rows) {
      const name = typeof r?.output === 'string' ? r.output : ''
      if (/^[a-z][a-z0-9_]{0,31}$/.test(name) && !seen.has(name)) {
        seen.add(name)
        out.push(name)
      }
    }
    out.push('else')
  }
  if (t.can_fail && n.on_error === 'route_error') out.push('error')
  return out
}

// CredentialUse is a connector, monitoring system or metric source that uses a credential.
export type CredentialUse = { kind: 'connector' | 'monitoring' | 'metric_source' | 'log_source'; id: string; name: string }

export function credentialUseLink(u: CredentialUse) {
  if (u.kind === 'monitoring') return `/monitoring?system=${encodeURIComponent(u.id)}`
  if (u.kind === 'metric_source') return '/rules?tab=sources'
  if (u.kind === 'log_source') return '/monitoring?tab=context'
  return `/connectors/${encodeURIComponent(u.id)}`
}
