export type PostgresOverview = {
  connection: { host: string; port: number; database: string; user: string; sslmode: string }
  where: string
  ok: boolean
  info: { version?: string; has_state: boolean; saved_at?: string }
  persist: { enabled: boolean; pending: boolean; error?: string }
  error?: string
}

export type PostgresTest = { ok: boolean; version?: string; latency_ms: number; error?: string }

export type DatabaseStats = {
  name: string
  size_bytes: number
  backends: number
  xact_commit: number
  xact_rollback: number
  blks_read: number
  blks_hit: number
  tup_returned: number
  tup_fetched: number
  tup_inserted: number
  tup_updated: number
  tup_deleted: number
  conflicts: number
  deadlocks: number
  temp_files: number
  temp_bytes: number
  stats_reset?: string
}

export type TableSize = {
  schema: string
  name: string
  total_bytes: number
  table_bytes: number
  index_bytes: number
  rows: number
  dead_rows: number
}

export type Operation = {
  pid: number
  database: string
  user: string
  application: string
  client: string
  backend_type: string
  state: string
  wait_event_type: string
  wait_event: string
  started?: string
  duration_ms: number
  query: string
  truncated: boolean
}

export type Statement = { query_id: string; query: string; calls: number; total_ms: number; mean_ms: number; rows: number }

export type PostgresStats = {
  collected_at: string
  full_visibility: boolean
  database: DatabaseStats
  table_count: number
  tables: TableSize[] | null
  operations: Operation[] | null
  statements: { installed: boolean; error?: string; by_total: Statement[] | null; by_mean: Statement[] | null }
}

export type PostgresMigrated = { where: string }
