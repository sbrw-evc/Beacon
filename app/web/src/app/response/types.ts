import type { Severity } from '../incidents/types'

export type Mode = 'off' | 'dry_run' | 'live'
export type Impact = 'extensive' | 'significant' | 'moderate' | 'minor'
export type Criticality = 'critical' | 'high' | 'medium' | 'low'
export type Method = 'email' | 'telegram' | 'teams' | 'zoom' | 'pagerduty' | 'war_room' | 'call_teams' | 'call_zoom' | 'voice_teams' | 'voice_telegram' | 'voice_zoom'
export type Target = 'route' | 'lead' | 'parent_lead' | 'ci_owners' | 'service_owners'

export const SEVERITIES: Severity[] = ['critical', 'error', 'warning', 'low', 'info']
export const IMPACTS: Impact[] = ['extensive', 'significant', 'moderate', 'minor']
export const CRITICALITIES: Criticality[] = ['critical', 'high', 'medium', 'low']
export const METHODS: Method[] = ['email', 'telegram', 'teams', 'zoom', 'pagerduty', 'war_room', 'call_teams', 'call_zoom', 'voice_teams', 'voice_telegram', 'voice_zoom']
export const TARGETS: Target[] = ['route', 'lead', 'parent_lead', 'ci_owners', 'service_owners']
export const PRIORITY: Record<Severity, string> = { critical: 'P1', error: 'P2', warning: 'P3', low: 'P4', info: 'P5' }

export type ImpactPolicy = {
  criticality: Record<Criticality, Impact>
  no_service: Impact
  raise_red: boolean
  raise_dependents: number
  raise_incidents: number
  matrix: Record<Impact, Record<Severity, Severity>>
  never_lower: boolean
}

export type Step = { after_minutes: number; targets: Target[]; user_ids: string[]; team_ids: string[]; methods: Method[] }

export type Policy = {
  priority: Severity
  enabled: boolean
  steps: Step[]
  stop_on_ack: boolean
  war_room: { enabled: boolean; members: Target[]; user_ids: string[]; add_escalated: boolean; post_updates: boolean }
  bridge: '' | 'teams' | 'zoom'
  jira: { task: boolean; postmortem: boolean; postmortem_days: number; comment: boolean }
}

export type Jira = {
  mode: Mode
  base_url: string
  email: string
  project: string
  task_type: string
  postmortem_type: string
  priorities: Partial<Record<Severity, string>>
  labels: string[]
  link_type: string
  done_transition: string
  has_token: boolean
}

export type Graph = { mode: Mode; tenant_id: string; client_id: string; account: string; login_url?: string; graph_url?: string; has_secret: boolean; has_refresh: boolean }
export type Zoom = { mode: Mode; account_id: string; client_id: string; user: string; has_secret: boolean }

export type VoiceVia = 'teams' | 'telegram' | 'zoom'
export const VOICE_VIA: VoiceVia[] = ['teams', 'telegram', 'zoom']
export type TTSProvider = 'piper' | 'rhvoice' | 'openai'

export type Voice = {
  mode: Mode
  tts: { provider: TTSProvider; url: string; voices: Record<string, string>; model: string }
  templates: Record<string, string>
  repeat: number
  ack_digit: boolean
  has_key: boolean
  ffmpeg: boolean
  default_templates: Record<string, string>
  placeholders: string[]
}

export type VoiceCall = {
  id: string
  via: VoiceVia
  person: string
  address?: string
  locale: string
  text: string
  state: 'planned' | 'sent' | 'dialing' | 'talking' | 'done' | 'acked' | 'no_answer' | 'failed'
  level?: number
  by?: string
  voice?: boolean
  error?: string
  dry_run?: boolean
  created: string
}

export type ResponseView = {
  mode: Mode
  active_since?: string
  impact: ImpactPolicy
  policies: Policy[]
  jira: Jira
  graph: Graph
  zoom: Zoom
  voice: Voice
  channels: Record<string, boolean>
  default_impact: ImpactPolicy
  default_policies: Policy[]
  updated_at?: string
  updated_by?: string
}

export type Reason = { code: string; args?: Record<string, string> }

export type ServiceImpact = { id: string; name: string; criticality: Criticality; direct: boolean; via?: string }

export type Assessment = {
  urgency: Severity
  impact: Impact
  priority: Severity
  services: ServiceImpact[]
  top_criticality?: Criticality
  incidents: number
  reasons: Reason[]
  at: string
}

export type Simulation = {
  assessment: Assessment
  policy: Policy | null
  steps: { after_minutes: number; methods: Method[]; people: string[] }[]
  room: string[]
  team?: { id: string; name: string }
}

export type JiraIssue = { key: string; url?: string; at: string; dry_run?: boolean }

export type ResponseState = {
  alert_id: string
  assessment: Assessment
  priority: Severity
  opened_at: string
  steps: { index: number; at: string; reached: string[]; failed?: string[]; people?: string[]; dry_run?: boolean }[]
  room?: { id: string; url?: string; members: string[]; at: string; dry_run?: boolean }
  bridge?: { provider: string; url?: string; at: string; dry_run?: boolean }
  task?: JiraIssue
  postmortem?: JiraIssue
  transitioned?: boolean
  status: string
  failures?: Record<string, { attempts: number; next: string; error: string }>
  skipped?: Record<string, boolean>
  finished: boolean
}
