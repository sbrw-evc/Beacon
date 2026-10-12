import type { Locale } from '../../api'
import type { Member } from '../org/types'

export type Text = Record<Locale, string>
export type CatalogFeature = { id: string; title: Text }
export type CatalogPage = { id: string; group: string; title: Text; features: CatalogFeature[] }
export type CatalogGroup = { id: string; title: Text }
export type Catalog = { groups: CatalogGroup[]; pages: CatalogPage[] }

export type Role = {
  id: string
  name: string
  description: string
  permissions: string[]
  system: boolean
  all_permissions: boolean
  /** Accounts created by a directory or NetBox without a mapped role get this role. */
  new_users: boolean
  member_count: number
  members: Member[]
}

export type Draft = { name: string; description: string; permissions: string[] }

export const VIEW = 'view'
export const ADMIN = 'admin'

export const perm = (page: string, feature: string) => `${page}:${feature}`

export class Permissions {
  constructor(
    private readonly catalog: Catalog,
    private readonly set: ReadonlySet<string>,
  ) {}

  static of(catalog: Catalog, list: string[]) {
    return new Permissions(catalog, new Set(list))
  }

  has(page: string, feature: string) {
    return this.set.has(perm(page, feature))
  }

  all(page: CatalogPage) {
    return page.features.every((f) => this.has(page.id, f.id))
  }

  count(page: CatalogPage) {
    return page.features.filter((f) => this.has(page.id, f.id)).length
  }

  toggle(page: CatalogPage, feature: string, on: boolean) {
    const next = new Set(this.set)
    if (!on && feature === VIEW) page.features.forEach((f) => next.delete(perm(page.id, f.id)))
    else if (!on) next.delete(perm(page.id, feature))
    else {
      next.add(perm(page.id, feature))
      next.add(perm(page.id, VIEW))
    }
    return new Permissions(this.catalog, next)
  }

  setAll(page: CatalogPage, on: boolean) {
    const next = new Set(this.set)
    page.features.forEach((f) => (on ? next.add(perm(page.id, f.id)) : next.delete(perm(page.id, f.id))))
    return new Permissions(this.catalog, next)
  }

  list() {
    return this.catalog.pages.flatMap((p) => p.features.map((f) => perm(p.id, f.id))).filter((x) => this.set.has(x))
  }

  diff(base: Permissions) {
    const now = this.list()
    const was = base.list()
    return { added: now.filter((x) => !was.includes(x)), removed: was.filter((x) => !now.includes(x)) }
  }

  describe(id: string, locale: Locale) {
    const [pageId, featureId] = id.split(':')
    const page = this.catalog.pages.find((p) => p.id === pageId)
    const feature = page?.features.find((f) => f.id === featureId)
    return page && feature ? `${page.title[locale]}: ${feature.title[locale]}` : id
  }
}

export function draftOf(r: Role): Draft {
  return { name: r.name, description: r.description, permissions: r.permissions }
}

export function changes(catalog: Catalog, role: Role, draft: Draft) {
  const perms = Permissions.of(catalog, draft.permissions).diff(Permissions.of(catalog, role.permissions))
  const name = draft.name.trim() !== role.name
  const description = draft.description.trim() !== role.description
  return { ...perms, name, description, dirty: name || description || perms.added.length + perms.removed.length > 0 }
}
