import { isLoopbackUrl } from './loopback'

export type OpenBaoAuth = 'approle' | 'token'

export type OpenBaoDraft = {
  addr: string
  mount: string
  namespace: string
  auth: OpenBaoAuth
  token: string
  role_id: string
  secret_id: string
  approle_path: string
  ca_cert: string
  skip_verify: boolean
}

export type OpenBaoStatus = {
  configured?: boolean
  addr?: string
  mount?: string
  namespace?: string
  auth?: string
  reachable?: boolean
  version?: string
  sealed?: boolean
  token_ok?: boolean
  mount_ok?: boolean
  token_expires?: string
  renewable?: boolean
  policies?: string[]
  error?: string
  last_error?: string
  last_error_at?: string
}

export type OpenBaoReport = { ok: boolean; error?: string; write_ok: boolean; write_error?: string; status: OpenBaoStatus }

export const DEFAULT_MOUNT = 'umbrella'

export function openBaoDraft(): OpenBaoDraft {
  return {
    addr: '',
    mount: DEFAULT_MOUNT,
    namespace: '',
    auth: 'approle',
    token: '',
    role_id: '',
    secret_id: '',
    approle_path: 'approle',
    ca_cert: '',
    skip_verify: false,
  }
}

export function openBaoBody(o: OpenBaoDraft) {
  const base = {
    addr: o.addr.trim(),
    mount: o.mount.trim(),
    namespace: o.namespace.trim(),
    auth: o.auth,
    ca_cert: o.ca_cert.trim(),
    skip_verify: o.skip_verify,
  }
  return o.auth === 'token' ? { ...base, token: o.token } : { ...base, role_id: o.role_id, secret_id: o.secret_id, approle_path: o.approle_path.trim() }
}

export function openBaoCheckKey(o: OpenBaoDraft) {
  return JSON.stringify(openBaoBody(o))
}

export function openBaoMount(o: OpenBaoDraft) {
  return o.mount.trim() || DEFAULT_MOUNT
}

export function openBaoFailHint(error: string, draft: OpenBaoDraft, container?: boolean): string | undefined {
  if (container && isLoopbackUrl(draft.addr)) return 'ob.loopback'
  if (/permission denied|403/i.test(error)) return 'ob.policy.hint'
  return undefined
}
