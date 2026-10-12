import type { ReactNode } from 'react'
import { Rows } from '../../ui'
import { ProfileCard } from './ProfileCard'
import type { Action } from './useAction'

// SummaryCard is the read-only overview card at the top of a settings page:
// a title (with an optional state pill), a short description and a ruled
// list of label/value rows with the values aligned right.
export function SummaryCard({
  title,
  badge,
  text,
  rows,
  action,
  footer,
  children,
}: {
  title: string
  badge?: ReactNode
  text?: ReactNode
  rows: [ReactNode, ReactNode][]
  action?: Action
  footer?: ReactNode
  children?: ReactNode
}) {
  return (
    <ProfileCard title={title} badge={badge} action={action} footer={footer}>
      {text && <p className="muted">{text}</p>}
      <Rows align="end" rows={rows} />
      {children}
    </ProfileCard>
  )
}
