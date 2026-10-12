import type { TeamLabel, TeamRef } from './types'

export type TeamOption = { id: string; label: string; depth: number }

export class TeamTree {
  private readonly byId: Map<string, TeamRef>
  private readonly children: Map<string, TeamRef[]>

  constructor(teams: TeamRef[]) {
    this.byId = new Map(teams.map((t) => [t.id, t]))
    this.children = new Map()
    for (const t of teams) {
      const parent = t.parent_id && this.byId.has(t.parent_id) ? t.parent_id : ''
      this.children.set(parent, [...(this.children.get(parent) ?? []), t])
    }
    for (const list of this.children.values()) list.sort((a, b) => a.name.localeCompare(b.name))
  }

  has(id: string) {
    return this.byId.has(id)
  }

  path(id: string): string[] {
    const out: string[] = []
    const seen = new Set<string>()
    for (let t = this.byId.get(id); t && !seen.has(t.id); t = this.byId.get(t.parent_id)) {
      seen.add(t.id)
      out.unshift(t.name)
    }
    return out
  }

  options(): TeamOption[] {
    const out: TeamOption[] = []
    const seen = new Set<string>()
    const walk = (parent: string, depth: number) => {
      for (const t of this.children.get(parent) ?? []) {
        if (seen.has(t.id)) continue
        seen.add(t.id)
        out.push({ id: t.id, label: this.path(t.id).join(' / '), depth })
        walk(t.id, depth + 1)
      }
    }
    walk('', 0)
    for (const t of this.byId.values()) if (!seen.has(t.id)) out.push({ id: t.id, label: this.path(t.id).join(' / '), depth: 0 })
    return out
  }
}

export function teamPath(label: TeamLabel, deleted: string) {
  return label.deleted ? deleted : label.path.join(' / ') || label.name
}
