import type { ReactNode } from 'react'
import { PlugZap } from 'lucide-react'
import { errorText, useT, type Dict } from '../i18n'
import { Flash } from '../notify'
import { Banner, Button } from '../ui'
import { ProfileCard } from '../app/profile/ProfileCard'
import { SummaryCard } from '../app/profile/SummaryCard'
import { connectionStrings } from './connectionStrings'
import './connections.css'

export function StatePill({ ok }: { ok: boolean }) {
  const t = useT(connectionStrings)
  return <span className={`pill pill-${ok ? 'ok' : 'error'}`}>{t(ok ? 'conn.state.ok' : 'conn.state.error')}</span>
}

export function ErrorBanner({ error, strings }: { error: unknown; strings?: Dict }) {
  const t = useT(strings ?? connectionStrings)
  if (!error) return null
  const e = errorText(t, error)
  return (
    <Banner kind="error" title={e.message}>
      {e.detail}
    </Banner>
  )
}

// ErrorFlash reports the error of an action (a save, a check) as a notification; errors that
// stand in for a page or list that could not be loaded stay in place, as ErrorBanner.
export function ErrorFlash({ error, strings }: { error: unknown; strings?: Dict }) {
  const t = useT(strings ?? connectionStrings)
  if (!error) return null
  const e = errorText(t, error)
  return <Flash kind="error" title={e.message}>{e.detail}</Flash>
}

export function ConnectionCard({
  title,
  text,
  ok,
  rows,
  problem,
  onTest,
  testing,
  children,
}: {
  title: string
  text?: string
  ok: boolean
  rows: [ReactNode, ReactNode][]
  problem?: string
  onTest?: () => void
  testing: boolean
  children?: ReactNode
}) {
  const t = useT(connectionStrings)
  return (
    <SummaryCard
      title={title}
      badge={<StatePill ok={ok} />}
      text={text}
      rows={rows}
      footer={
        onTest && (
          <Button onClick={onTest} busy={testing}>
            {!testing && <PlugZap size={16} />}
            {t('conn.test')}
          </Button>
        )
      }
    >
      {problem && <Banner kind="error" title={problem} />}
      {children}
    </SummaryCard>
  )
}

export function MigrationCard({ title, text, children }: { title: string; text: string; children: ReactNode }) {
  return (
    <ProfileCard title={title}>
      <p className="muted">{text}</p>
      {children}
    </ProfileCard>
  )
}

export function MigrationActions({
  onCheck,
  canCheck,
  onMigrate,
  canMigrate,
  busy,
}: {
  onCheck: () => void
  canCheck: boolean
  onMigrate: () => void
  canMigrate: boolean
  busy: boolean
}) {
  const t = useT(connectionStrings)
  return (
    <div className="conn-actions">
      <Button onClick={onCheck} busy={busy} disabled={!canCheck}>
        {t('conn.check')}
      </Button>
      <Button variant="primary" onClick={onMigrate} disabled={!canMigrate || busy}>
        {t('conn.migrate')}
      </Button>
    </div>
  )
}
