// SpaView is GET /api/settings/spa: the connection to SPA, the last synchronization of people and
// the role catalogue Umbrella publishes there.
export type SpaView = {
  connected: boolean
  key_source?: 'env' | 'file' | 'openbao' | string
  key_error?: string
  redirect_uri: string
  post_logout_redirect_uri: string
  issuer: string
  client_id: string
  app_name: string
  app_type: string
  account_url: string
  methods: { id: string; title: string }[]
  last_sync?: string
  ok: boolean
  error?: string
  users: number
  created: number
  updated: number
  bound: number
  disabled: number
  conflicts: string[]
  no_role: string[]
  catalog_at?: string
  catalog_roles: number
  catalog_error?: string
  counts: { spa: number; disabled: number; legacy: number }
  roles: { id: string; name: string; description?: string }[]
  interval: string
}
