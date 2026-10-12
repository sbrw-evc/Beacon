import type { Input, Ref, Wallboard } from './types'

export const SLUG_RE = /^[a-z0-9][a-z0-9-]{1,62}$/
export const REFRESH_MIN = 5
export const REFRESH_MAX = 600
export const RESOLVED_MAX = 1440
export const MAX_NETWORKS = 200

function ipv4(s: string) {
  const parts = s.split('.')
  return parts.length === 4 && parts.every((p) => /^\d{1,3}$/.test(p) && Number(p) <= 255)
}

function ipv6(s: string) {
  if (!/^[0-9a-f:.]+$/i.test(s) || !s.includes(':')) return false
  const double = s.split('::').length - 1
  if (double > 1) return false
  let groups = s.split(':').filter((g) => g !== '')
  const tail = groups[groups.length - 1]
  if (tail?.includes('.')) {
    if (!ipv4(tail)) return false
    groups = [...groups.slice(0, -1), '0', '0']
  }
  if (!groups.every((g) => /^[0-9a-f]{1,4}$/i.test(g))) return false
  return double ? groups.length < 8 : groups.length === 8
}

// networkOK is a basic client-side check of one allowed network: a single IP or a CIDR.
// The server has the final word (err.network_invalid).
export function networkOK(entry: string) {
  const [addr, prefix, ...rest] = entry.split('/')
  if (rest.length > 0 || !addr) return false
  const v4 = ipv4(addr)
  if (!v4 && !ipv6(addr)) return false
  if (prefix === undefined) return true
  if (!/^\d{1,3}$/.test(prefix)) return false
  return Number(prefix) <= (v4 ? 32 : 128)
}

export function networksOf(text: string) {
  return Array.from(new Set(text.split(/[\n,;]+/).map((s) => s.trim()).filter(Boolean)))
}

// opensToAll tells a network that matches every address, such as 0.0.0.0/0 or ::/0.
export function opensToAll(entry: string) {
  const [addr, prefix] = entry.split('/')
  return prefix !== undefined && Number(prefix) === 0 && !!addr
}

export type Draft = Omit<Input, 'ci_ids' | 'service_ids' | 'team_ids' | 'allowed_networks'> & {
  cis: Ref[]
  services: Ref[]
  teams: Ref[]
  networks: string
  slugTouched: boolean
}

export function draftOf(v: Wallboard | 'new' | null): Draft {
  if (v && v !== 'new')
    return {
      slug: v.slug,
      title: v.title,
      description: v.description,
      enabled: v.enabled,
      cis: v.cis ?? [],
      services: v.services ?? [],
      teams: v.teams ?? [],
      severities: v.severities ?? [],
      methods: v.methods ?? [],
      show_acknowledged: v.show_acknowledged,
      show_suppressed: v.show_suppressed,
      resolved_minutes: v.resolved_minutes,
      sort: v.sort || 'newest',
      refresh_seconds: v.refresh_seconds || 30,
      theme: v.theme || 'dark',
      locale: v.locale ?? '',
      timezone: v.timezone ?? '',
      networks: (v.allowed_networks ?? []).join('\n'),
      slugTouched: true,
    }
  return {
    slug: '',
    title: '',
    description: '',
    enabled: true,
    cis: [],
    services: [],
    teams: [],
    severities: [],
    methods: [],
    show_acknowledged: true,
    show_suppressed: false,
    resolved_minutes: 0,
    sort: 'newest',
    refresh_seconds: 30,
    theme: 'dark',
    locale: '',
    timezone: '',
    networks: '',
    slugTouched: false,
  }
}

export function inputOf(d: Draft): Input {
  return {
    slug: d.slug.trim().toLowerCase(),
    title: d.title.trim(),
    description: d.description.trim(),
    enabled: d.enabled,
    ci_ids: d.cis.map((r) => r.id),
    service_ids: d.services.map((r) => r.id),
    team_ids: d.teams.map((r) => r.id),
    severities: d.severities,
    methods: d.methods,
    show_acknowledged: d.show_acknowledged,
    show_suppressed: d.show_suppressed,
    resolved_minutes: d.resolved_minutes,
    sort: d.sort,
    refresh_seconds: d.refresh_seconds,
    theme: d.theme,
    locale: d.locale,
    timezone: d.timezone,
    allowed_networks: networksOf(d.networks),
  }
}

// publicURL is the absolute address of a board's TV page.
export function publicURL(w: Pick<Wallboard, 'path' | 'slug'>) {
  return new URL(w.path || `/tv/${w.slug}`, window.location.origin).toString()
}

export async function copyText(text: string) {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // fall back below
  }
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.opacity = '0'
  document.body.appendChild(area)
  area.select()
  let ok = false
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }
  area.remove()
  return ok
}
