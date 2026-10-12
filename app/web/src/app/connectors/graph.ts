import type { Graph, GraphNode, Issue, NodeType } from './types'

export function uniqueId(base: string, taken: Iterable<string>) {
  const used = new Set(taken)
  const clean = base.replace(/[^A-Za-z0-9_-]+/g, '_').slice(0, 48) || 'node'
  if (!used.has(clean)) return clean
  for (let i = 2; ; i++) {
    const id = `${clean}_${i}`
    if (!used.has(id)) return id
  }
}

export function newNode(t: NodeType, g: Graph, position: { x: number; y: number }): GraphNode {
  const base = t.type.split('.').pop() ?? 'node'
  const params: Record<string, unknown> = {}
  for (const p of t.params) {
    if (p.default !== undefined) params[p.key] = p.default
  }
  return { id: uniqueId(base, g.nodes.map((n) => n.id)), type: t.type, type_version: t.version, params, position }
}

export function slugify(s: string) {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 48)
}

// signature of what a test run depends on for one node.
function signature(n: GraphNode) {
  return JSON.stringify([n.type, n.type_version, n.params, !!n.disabled, n.on_error ?? ''])
}

export function signatures(g: Graph) {
  const out: Record<string, string> = {}
  for (const n of g.nodes) out[n.id] = signature(n)
  const edges = g.edges.map((e) => `${e.source}:${e.source_output}>${e.target}`).sort()
  out['#edges'] = edges.join('|')
  return out
}

// stale lists the nodes whose test-run result no longer matches the graph: nodes changed since
// the run and everything downstream of them.
export function staleNodes(g: Graph, at: Record<string, string> | null): Set<string> {
  const out = new Set<string>()
  if (!at) return out
  const now = signatures(g)
  const edgesChanged = now['#edges'] !== at['#edges']
  const next = new Map<string, string[]>()
  for (const e of g.edges) next.set(e.source, [...(next.get(e.source) ?? []), e.target])
  const queue = g.nodes.filter((n) => at[n.id] === undefined || at[n.id] !== now[n.id]).map((n) => n.id)
  if (edgesChanged) {
    const old = new Set(at['#edges'].split('|'))
    for (const e of g.edges) if (!old.has(`${e.source}:${e.source_output}>${e.target}`)) queue.push(e.target)
  }
  while (queue.length) {
    const id = queue.pop()!
    if (out.has(id)) continue
    out.add(id)
    queue.push(...(next.get(id) ?? []))
  }
  return out
}

export function issuesByNode(issues: Issue[]) {
  const out = new Map<string, Issue[]>()
  for (const i of issues) {
    if (!i.node_id) continue
    out.set(i.node_id, [...(out.get(i.node_id) ?? []), i])
  }
  return out
}

export function json(v: unknown) {
  return JSON.stringify(v, null, 2)
}

export function downloadJSON(name: string, data: unknown) {
  const url = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' }))
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
