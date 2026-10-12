export const LEVELS = ['critical', 'warning', 'ok', 'unknown'] as const

export type Level = (typeof LEVELS)[number]

export type Reason = { code: string; level: Level; count?: number; ref?: string; detail?: string }

export type Health = { level: Level; reasons: Reason[] }

export type MapEvent = { incident_id: string; title: string; severity: string; status: string; suppressed?: boolean; last_seen: string }

export type MapCI = {
  id: string
  name: string
  kind: string
  status: string
  source: string
  ips: string[]
  netbox_url?: string
  directory?: 'matched' | 'missing' | 'disabled'
  services: string[]
  events: { critical: number; error: number; warning: number; low?: number; info: number; maintenance?: number; recent: MapEvent[] }
  health: Health
}

export type MapService = {
  id: string
  name: string
  criticality: 'critical' | 'high' | 'medium' | 'low'
  status: 'active' | 'planned' | 'retired'
  owner: string
  ci_ids: string[]
  depends_on: string[]
  netbox_url?: string
  health: Health
}

export type CMDBMap = {
  services: MapService[]
  cis: MapCI[]
  events: { available: boolean; error?: string }
  generated_at: string
}

export const rank: Record<Level, number> = { unknown: 0, ok: 1, warning: 2, critical: 3 }

export const problem = (l: Level) => l === 'warning' || l === 'critical'
