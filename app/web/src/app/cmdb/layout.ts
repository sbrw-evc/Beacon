import type { CMDBMap } from './types'

export const SERVICE_W = 250
export const SERVICE_H = 92
export const CI_W = 230
export const CI_H = 70
const COL_GAP = 110
const ROW_GAP = 22

export type Placed = { id: string; kind: 'service' | 'ci'; x: number; y: number }

// layout puts services in columns by dependency depth (services nobody depends on first, the
// services they depend on to the right) and configuration items in a last column. Inside a
// column, nodes are ordered by the mean position of the nodes pointing at them so that edges
// cross less.
export function layout(map: CMDBMap, serviceIds: Set<string>, ciIds: Set<string>): Placed[] {
  const services = map.services.filter((s) => serviceIds.has(s.id))
  const dependents = new Map<string, string[]>()
  for (const s of services) for (const d of s.depends_on) if (serviceIds.has(d)) dependents.set(d, [...(dependents.get(d) ?? []), s.id])

  const level = new Map<string, number>()
  const visiting = new Set<string>()
  const depth = (id: string): number => {
    const known = level.get(id)
    if (known !== undefined) return known
    if (visiting.has(id)) return 0
    visiting.add(id)
    const parents = dependents.get(id) ?? []
    const v = parents.length ? Math.max(...parents.map(depth)) + 1 : 0
    visiting.delete(id)
    level.set(id, v)
    return v
  }
  for (const s of services) depth(s.id)

  const columns: string[][] = []
  for (const s of services) {
    const l = level.get(s.id) ?? 0
    ;(columns[l] ??= []).push(s.id)
  }
  const pos = new Map<string, number>()
  const out: Placed[] = []
  let x = 0
  columns.forEach((col, i) => {
    if (i > 0) {
      const bary = (id: string) => {
        const ps = (dependents.get(id) ?? []).map((p) => pos.get(p)).filter((v): v is number => v !== undefined)
        return ps.length ? ps.reduce((a, b) => a + b, 0) / ps.length : Infinity
      }
      col.sort((a, b) => bary(a) - bary(b))
    }
    col.forEach((id, j) => {
      const y = j * (SERVICE_H + ROW_GAP)
      pos.set(id, y)
      out.push({ id, kind: 'service', x, y })
    })
    x += SERVICE_W + COL_GAP
  })

  const cis = map.cis.filter((c) => ciIds.has(c.id))
  const owners = new Map<string, number[]>()
  for (const s of services) for (const c of s.ci_ids) if (pos.has(s.id)) owners.set(c, [...(owners.get(c) ?? []), pos.get(s.id) as number])
  const bary = (id: string) => {
    const ps = owners.get(id) ?? []
    return ps.length ? ps.reduce((a, b) => a + b, 0) / ps.length : Infinity
  }
  cis.sort((a, b) => bary(a.id) - bary(b.id) || a.name.localeCompare(b.name))
  cis.forEach((c, j) => out.push({ id: c.id, kind: 'ci', x, y: j * (CI_H + ROW_GAP) - (SERVICE_H - CI_H) / 2 }))
  return out
}
