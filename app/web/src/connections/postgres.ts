import { isLoopbackHost } from './loopback'

export type PostgresDraft = { host: string; port: string; database: string; user: string; password: string; sslmode: string }

export type PostgresProbe = { version: string; database: string; user: string; can_create: boolean; has_state: boolean; saved_at?: string }

export type PostgresReport = { ok: boolean; error?: string; probe: PostgresProbe }

export const SSL_MODES = ['disable', 'prefer', 'require', 'verify-ca', 'verify-full']

export function postgresDraft(): PostgresDraft {
  return { host: '', port: '5432', database: 'umbrella', user: 'umbrella', password: '', sslmode: 'prefer' }
}

export function postgresBody(p: PostgresDraft) {
  return {
    host: p.host.trim(),
    port: Number(p.port) || 0,
    database: p.database.trim(),
    user: p.user.trim(),
    password: p.password,
    sslmode: p.sslmode,
  }
}

export function postgresCheckKey(p: PostgresDraft) {
  return JSON.stringify(postgresBody(p))
}

export function postgresComplete(p: PostgresDraft) {
  return p.host.trim() !== '' && p.database.trim() !== '' && p.user.trim() !== ''
}

export function postgresUsable(probe: PostgresProbe) {
  return probe.can_create || probe.has_state
}

export function postgresFailHint(draft: PostgresDraft, container?: boolean): string | undefined {
  return container && isLoopbackHost(draft.host) ? 'pg.loopback' : undefined
}
