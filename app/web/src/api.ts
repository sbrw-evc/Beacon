import type { PasswordPolicy } from './policy'

export type Theme = 'light' | 'dark'
export type Locale = 'en' | 'ru'

export type Meta = {
  mode: 'setup' | 'ready'
  version: string
  default_theme: Theme
  default_locale: Locale
  default_timezone: string
  ldap_enabled?: boolean
  entra_enabled?: boolean
  container?: boolean
  password_policy?: PasswordPolicy
}

export class ApiError extends Error {
  status: number
  code: string
  detail?: string

  constructor(status: number, code: string, detail?: string) {
    super(detail ? `${code}: ${detail}` : code)
    this.status = status
    this.code = code
    this.detail = detail
  }
}

let csrf = ''

// changeListeners are told after every successful change (any request but GET), so views that
// follow the state of the installation, such as the setup guides, can read it again.
const changeListeners = new Set<() => void>()

export function onChange(fn: () => void) {
  changeListeners.add(fn)
  return () => {
    changeListeners.delete(fn)
  }
}

function changed(method: string) {
  if (method === 'GET') return
  for (const fn of changeListeners) fn()
}

export function setCsrf(v: string) {
  csrf = v
}

export async function api<T>(method: string, path: string, body?: unknown, headers: Record<string, string> = {}): Promise<T> {
  let res: Response
  try {
    res = await fetch(path, {
      method,
      credentials: 'same-origin',
      headers: {
        'Content-Type': 'application/json',
        ...(csrf && method !== 'GET' ? { 'X-CSRF-Token': csrf } : {}),
        ...headers,
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch (e) {
    throw new ApiError(0, 'network', e instanceof Error ? e.message : String(e))
  }
  if (res.status === 204) {
    changed(method)
    return undefined as T
  }
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new ApiError(res.status, data.error ?? `http_${res.status}`, data.detail)
  changed(method)
  return data as T
}

export async function upload<T>(method: string, path: string, body: Blob): Promise<T> {
  let res: Response
  try {
    res = await fetch(path, {
      method,
      credentials: 'same-origin',
      headers: { 'Content-Type': body.type || 'application/octet-stream', ...(csrf ? { 'X-CSRF-Token': csrf } : {}) },
      body,
    })
  } catch (e) {
    throw new ApiError(0, 'network', e instanceof Error ? e.message : String(e))
  }
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new ApiError(res.status, data.error ?? `http_${res.status}`, data.detail)
  changed(method)
  return data as T
}
