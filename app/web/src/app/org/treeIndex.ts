export type TreeItem = { id: string; parent_id: string; name: string }

export class TreeIndex<T extends TreeItem> {
  readonly byId: Map<string, T>
  private readonly kids = new Map<string, T[]>()

  constructor(items: T[]) {
    this.byId = new Map(items.map((i) => [i.id, i]))
    for (const i of [...items].sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }))) {
      const parent = this.byId.has(i.parent_id) ? i.parent_id : ''
      this.kids.set(parent, [...(this.kids.get(parent) ?? []), i])
    }
  }

  roots(): T[] {
    return this.kids.get('') ?? []
  }

  children(id: string): T[] {
    return this.kids.get(id) ?? []
  }

  ancestors(id: string): T[] {
    const out: T[] = []
    const seen = new Set<string>()
    let cur = this.byId.get(id)?.parent_id ?? ''
    while (cur && !seen.has(cur)) {
      seen.add(cur)
      const node = this.byId.get(cur)
      if (!node) break
      out.unshift(node)
      cur = node.parent_id
    }
    return out
  }

  descendants(id: string): Set<string> {
    const out = new Set<string>()
    const walk = (x: string) =>
      this.children(x).forEach((c) => {
        if (out.has(c.id)) return
        out.add(c.id)
        walk(c.id)
      })
    walk(id)
    return out
  }

  path(id: string, sep = ' / '): string {
    const node = this.byId.get(id)
    return node ? [...this.ancestors(id), node].map((n) => n.name).join(sep) : ''
  }

  flat(): { item: T; depth: number }[] {
    const out: { item: T; depth: number }[] = []
    const walk = (list: T[], depth: number) =>
      list.forEach((item) => {
        out.push({ item, depth })
        walk(this.children(item.id), depth + 1)
      })
    walk(this.roots(), 0)
    return out
  }

  visible(match: (item: T) => boolean): Set<string> {
    const out = new Set<string>()
    for (const item of this.byId.values()) {
      if (!match(item)) continue
      out.add(item.id)
      this.ancestors(item.id).forEach((a) => out.add(a.id))
    }
    return out
  }
}
