import type { Member } from '../org/types'

export type Team = {
  id: string
  name: string
  description: string
  parent_id: string
  lead_id: string
  email: string
  telegram: string
  // Webhook URLs of the team's Teams channel and Zoom chat; redacted for people who cannot edit
  // teams. Absent on an older server.
  teams?: string
  zoom?: string
  depth: number
  child_count: number
  member_count: number
  members: Member[]
  lead: Member | null
}

export type TeamDraft = {
  name: string
  description: string
  parent_id: string
  lead_id: string
  email: string
  telegram: string
  teams: string
  zoom: string
}

export const MAX_DEPTH = 8

export function teamDraft(t?: Team, parent = ''): TeamDraft {
  return {
    name: t?.name ?? '',
    description: t?.description ?? '',
    parent_id: t?.parent_id ?? parent,
    lead_id: t?.lead_id ?? '',
    email: t?.email ?? '',
    telegram: t?.telegram ?? '',
    teams: t?.teams ?? '',
    zoom: t?.zoom ?? '',
  }
}

export function teamChanges(t: Team, d: TeamDraft): Partial<TeamDraft> {
  const out: Partial<TeamDraft> = {}
  if (d.name.trim() !== t.name) out.name = d.name
  if (d.description.trim() !== t.description) out.description = d.description
  if (d.parent_id !== t.parent_id) out.parent_id = d.parent_id
  if (d.lead_id !== t.lead_id) out.lead_id = d.lead_id
  if (d.email.trim() !== (t.email ?? '')) out.email = d.email.trim()
  if (d.telegram.trim() !== (t.telegram ?? '')) out.telegram = d.telegram.trim()
  if (d.teams.trim() !== (t.teams ?? '')) out.teams = d.teams.trim()
  if (d.zoom.trim() !== (t.zoom ?? '')) out.zoom = d.zoom.trim()
  return out
}
