import { useEffect, useRef, useState, type ReactNode } from 'react'
import { RefreshCw } from 'lucide-react'
import { ErrorBanner } from '../../../connections/ConnectionCard'
import { useLocale, useT } from '../../../i18n'
import { Banner, Button, formatDate, Segmented, Switch } from '../../../ui'
import { useSession } from '../../session'
import { DataTable } from './DataTable'
import { formatBytes, formatCount, formatMs, formatPercent } from './format'
import { strings } from './strings'
import type { DatabaseStats, Operation, PostgresStats, Statement } from './types'

const AUTO_REFRESH_MS = 10_000

type T = (key: string, vars?: Record<string, string | number>) => string

function useAutoRefresh(enabled: boolean, refresh: () => void) {
  const latest = useRef(refresh)
  useEffect(() => {
    latest.current = refresh
  }, [refresh])
  useEffect(() => {
    if (!enabled) return
    const id = window.setInterval(() => {
      if (document.visibilityState === 'visible') latest.current()
    }, AUTO_REFRESH_MS)
    return () => window.clearInterval(id)
  }, [enabled])
}

function useTransactionRate(stats: PostgresStats | null) {
  const previous = useRef<PostgresStats | null>(null)
  const [rate, setRate] = useState<number | null>(null)
  useEffect(() => {
    if (!stats) return
    const prev = previous.current
    previous.current = stats
    if (!prev || prev.database.stats_reset !== stats.database.stats_reset) {
      setRate(null)
      return
    }
    const seconds = (Date.parse(stats.collected_at) - Date.parse(prev.collected_at)) / 1000
    const delta = stats.database.xact_commit + stats.database.xact_rollback - prev.database.xact_commit - prev.database.xact_rollback
    if (seconds > 0 && delta >= 0) setRate(delta / seconds)
  }, [stats])
  return rate
}

function Tile({ label, value, note }: { label: string; value: ReactNode; note?: ReactNode }) {
  return (
    <div className="pg-tile">
      <div className="pg-tile-label">{label}</div>
      <div className="pg-tile-value">{value}</div>
      {note && <div className="pg-tile-note">{note}</div>}
    </div>
  )
}

function Transactions({ db, rate, t, locale, tz }: { db: DatabaseStats; rate: number | null; t: T; locale: string; tz: string }) {
  const n = (v: number) => formatCount(v, locale)
  const total = db.xact_commit + db.xact_rollback
  return (
    <div className="stack">
      <h3 className="pg-sub">{t('pgs.tx')}</h3>
      <div className="pg-tiles">
        <Tile label={t('pgs.tx.commits')} value={n(db.xact_commit)} />
        <Tile label={t('pgs.tx.rollbacks')} value={n(db.xact_rollback)} note={formatPercent(db.xact_rollback, total, locale)} />
        <Tile label={t('pgs.tx.rate')} value={rate === null ? '—' : n(rate)} note={rate === null ? t('pgs.tx.rateWait') : undefined} />
        <Tile label={t('pgs.tx.cache')} value={formatPercent(db.blks_hit, db.blks_hit + db.blks_read, locale)} />
        <Tile label={t('pgs.tx.backends')} value={n(db.backends)} />
        <Tile label={t('pgs.tx.deadlocks')} value={n(db.deadlocks)} />
        <Tile label={t('pgs.tx.conflicts')} value={n(db.conflicts)} />
        <Tile label={t('pgs.tx.temp')} value={n(db.temp_files)} note={formatBytes(db.temp_bytes, locale, t)} />
        <Tile label={t('pgs.tx.returned')} value={n(db.tup_returned)} />
        <Tile label={t('pgs.tx.fetched')} value={n(db.tup_fetched)} />
        <Tile label={t('pgs.tx.inserted')} value={n(db.tup_inserted)} />
        <Tile label={t('pgs.tx.updated')} value={n(db.tup_updated)} />
        <Tile label={t('pgs.tx.deleted')} value={n(db.tup_deleted)} />
      </div>
      <p className="hint">{db.stats_reset ? t('pgs.tx.since', { at: formatDate(db.stats_reset, locale, tz) }) : t('pgs.tx.sinceStart')}</p>
    </div>
  )
}

function Tables({ stats, t, locale }: { stats: PostgresStats; t: T; locale: string }) {
  const tables = stats.tables ?? []
  const b = (v: number) => formatBytes(v, locale, t)
  return (
    <div className="stack">
      <h3 className="pg-sub">{t('pgs.tables')}</h3>
      {tables.length === 0 ? (
        <p className="muted">{t('pgs.tables.none')}</p>
      ) : (
        <>
          <DataTable
            caption={t('pgs.tables')}
            columns={[
              { label: t('pgs.tables.name'), wide: true },
              { label: t('pgs.tables.total'), numeric: true },
              { label: t('pgs.tables.data'), numeric: true },
              { label: t('pgs.tables.indexes'), numeric: true },
              { label: t('pgs.tables.rows'), numeric: true },
              { label: t('pgs.tables.dead'), numeric: true },
            ]}
            rows={tables.map((r) => ({
              key: `${r.schema}.${r.name}`,
              cells: [
                <code key="n">{`${r.schema}.${r.name}`}</code>,
                b(r.total_bytes),
                b(r.table_bytes),
                b(r.index_bytes),
                formatCount(r.rows, locale),
                formatCount(r.dead_rows, locale),
              ],
            }))}
          />
          {stats.table_count > tables.length && <p className="hint">{t('pgs.tables.top', { shown: tables.length, total: stats.table_count })}</p>}
        </>
      )}
    </div>
  )
}

function QueryText({ text, truncated }: { text: string; truncated?: boolean }) {
  return (
    <code className="pg-query" title={text}>
      {text}
      {truncated && '…'}
    </code>
  )
}

function Operations({ ops, full, t, locale }: { ops: Operation[]; full: boolean; t: T; locale: string }) {
  return (
    <div className="stack">
      <h3 className="pg-sub">{t('pgs.ops')}</h3>
      {!full && <Banner kind="info" title={t('pgs.ops.limited')} />}
      {ops.length === 0 ? (
        <p className="muted">{t('pgs.ops.none')}</p>
      ) : (
        <DataTable
          caption={t('pgs.ops')}
          columns={[
            { label: t('pgs.ops.pid'), numeric: true },
            { label: t('pgs.ops.who') },
            { label: t('pgs.ops.state') },
            { label: t('pgs.ops.wait') },
            { label: t('pgs.ops.duration'), numeric: true },
            { label: t('pgs.ops.query'), wide: true },
          ]}
          rows={ops.map((o) => ({
            key: o.pid,
            cells: [
              o.pid,
              <span key="w" className="pg-who">
                <span>{o.user || o.backend_type}</span>
                <span className="hint">{[o.application, o.client, o.database].filter(Boolean).join(' · ')}</span>
              </span>,
              o.state,
              o.wait_event ? `${o.wait_event_type}: ${o.wait_event}` : '—',
              formatMs(o.duration_ms, locale, t),
              <QueryText key="q" text={o.query} truncated={o.truncated} />,
            ],
          }))}
        />
      )}
    </div>
  )
}

function Statements({ stats, t, locale }: { stats: PostgresStats['statements']; t: T; locale: string }) {
  const [order, setOrder] = useState<'total' | 'mean'>('total')
  const list: Statement[] = (order === 'total' ? stats.by_total : stats.by_mean) ?? []
  return (
    <div className="stack">
      <div className="pg-sub-row">
        <h3 className="pg-sub">{t('pgs.stmts')}</h3>
        {stats.installed && !stats.error && (
          <Segmented
            label={t('pgs.stmts')}
            value={order}
            onChange={setOrder}
            options={[
              { value: 'total', label: t('pgs.stmts.byTotal') },
              { value: 'mean', label: t('pgs.stmts.byMean') },
            ]}
          />
        )}
      </div>
      {!stats.installed ? (
        <Banner kind="info" title={t('pgs.stmts.missing')}>
          {t('pgs.stmts.missingHint')}
        </Banner>
      ) : stats.error ? (
        <Banner kind="warn" title={t('pgs.stmts.error')}>
          <p>{stats.error}</p>
          <p>{t('pgs.stmts.missingHint')}</p>
        </Banner>
      ) : list.length === 0 ? (
        <p className="muted">{t('pgs.stmts.none')}</p>
      ) : (
        <DataTable
          caption={t('pgs.stmts')}
          columns={[
            { label: t('pgs.ops.query'), wide: true },
            { label: t('pgs.stmts.calls'), numeric: true },
            { label: t('pgs.stmts.total'), numeric: true },
            { label: t('pgs.stmts.mean'), numeric: true },
            { label: t('pgs.stmts.rows'), numeric: true },
          ]}
          rows={list.map((s, i) => ({
            key: s.query_id || i,
            cells: [
              <QueryText key="q" text={s.query} />,
              formatCount(s.calls, locale),
              formatMs(s.total_ms, locale, t),
              formatMs(s.mean_ms, locale, t),
              formatCount(s.rows, locale),
            ],
          }))}
        />
      )}
    </div>
  )
}

export function Statistics({ stats, error, busy, reload }: { stats: PostgresStats | null; error: unknown; busy: boolean; reload: () => void }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const [auto, setAuto] = useState(true)
  useAutoRefresh(auto, reload)
  const rate = useTransactionRate(stats)
  return (
    <section className="card status-card" aria-label={t('pgs.stats')}>
      <header>
        <h2>{t('pgs.stats')}</h2>
        <Button onClick={reload} busy={busy}>
          {!busy && <RefreshCw size={16} />}
          {t('conn.refresh')}
        </Button>
      </header>
      <p className="muted">{t('pgs.stats.text')}</p>
      <div className="pg-sub-row">
        <Switch checked={auto} onChange={setAuto} label={t('pgs.stats.auto')} />
        {stats && <span className="hint">{t('pgs.stats.collected', { at: formatDate(stats.collected_at, locale, timezone) })}</span>}
      </div>
      <ErrorBanner error={error} strings={strings} />
      {stats && (
        <>
          <Transactions db={stats.database} rate={rate} t={t} locale={locale} tz={timezone} />
          <Tables stats={stats} t={t} locale={locale} />
          <Operations ops={stats.operations ?? []} full={stats.full_visibility} t={t} locale={locale} />
          <Statements stats={stats.statements} t={t} locale={locale} />
        </>
      )}
    </section>
  )
}
