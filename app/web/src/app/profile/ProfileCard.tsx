import type { FormEvent, ReactNode } from 'react'
import type { Action } from './useAction'
import { Flash } from '../../notify'
import { Banner } from '../../ui'

export function ProfileCard({
  title,
  badge,
  action,
  footer,
  onSubmit,
  wide,
  inline,
  children,
}: {
  title: string
  badge?: ReactNode
  action?: Action
  footer?: ReactNode
  onSubmit?: () => void
  wide?: boolean
  // inline: the error stays in the card instead of popping up, for a card whose content could not
  // be loaded.
  inline?: boolean
  children: ReactNode
}) {
  const body = (
    <>
      <header>
        <h2>{title}</h2>
        {badge}
      </header>
      {children}
      {action?.notice && <Flash kind="ok" title={action.notice} />}
      {action?.error &&
        (inline ? (
          <Banner kind="error" title={action.error.message}>
            {action.error.detail}
          </Banner>
        ) : (
          <Flash kind="error" title={action.error.message}>
            {action.error.detail}
          </Flash>
        ))}
      {footer && <div className="card-actions">{footer}</div>}
    </>
  )
  const className = `card status-card${wide ? ' span-all' : ''}`
  if (!onSubmit) {
    return (
      <section className={className} aria-label={title}>
        {body}
      </section>
    )
  }
  const submit = (e: FormEvent) => {
    e.preventDefault()
    onSubmit()
  }
  return (
    <form className={className} aria-label={title} onSubmit={submit} noValidate>
      {body}
    </form>
  )
}
