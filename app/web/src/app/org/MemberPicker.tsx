import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Check, Search } from 'lucide-react'
import { useT } from '../../i18n'
import { Button, Input, Modal } from '../../ui'
import type { Action } from '../profile/useAction'
import { MemberLine } from './MemberList'
import { orgStrings } from './strings'
import type { Member } from './types'
import './org.css'
import { Flash } from '../../notify'

export function matchesUser(u: { name: string; username: string }, query: string) {
  const q = query.trim().toLowerCase()
  return !q || u.name.toLowerCase().includes(q) || u.username.toLowerCase().includes(q)
}

export function MemberPicker({
  open,
  title,
  users,
  exclude,
  note,
  action,
  confirmLabel,
  onClose,
  onConfirm,
}: {
  open: boolean
  title: string
  users: Member[]
  exclude: string[]
  note?: (u: Member) => ReactNode
  action: Action
  confirmLabel: string
  onClose: () => void
  onConfirm: (ids: string[]) => void
}) {
  const t = useT(orgStrings)
  const [query, setQuery] = useState('')
  const [picked, setPicked] = useState<Set<string>>(() => new Set())
  useEffect(() => {
    if (!open) return
    setQuery('')
    setPicked(new Set())
  }, [open])
  const candidates = useMemo(() => {
    const skip = new Set(exclude)
    return users.filter((u) => !skip.has(u.id))
  }, [users, exclude])
  const shown = candidates.filter((u) => matchesUser(u, query))
  const flip = (id: string) =>
    setPicked((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  return (
    <Modal
      open={open}
      title={title}
      onClose={onClose}
      footer={
        <>
          <span className="hint org-picker-count">{t('org.picked', { n: picked.size })}</span>
          <Button variant="ghost" onClick={onClose}>
            {t('org.cancel')}
          </Button>
          <Button variant="primary" busy={action.busy} disabled={picked.size === 0} onClick={() => onConfirm([...picked])}>
            {confirmLabel}
          </Button>
        </>
      }
    >
      <div className="org-search">
        <Search size={16} aria-hidden />
        <Input value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t('org.search.users')} aria-label={t('org.search.users')} />
      </div>
      {candidates.length === 0 ? (
        <p className="muted">{t('org.picker.none')}</p>
      ) : shown.length === 0 ? (
        <p className="muted">{t('org.nothing')}</p>
      ) : (
        <ul className="org-picker" role="listbox" aria-multiselectable>
          {shown.map((u) => {
            const on = picked.has(u.id)
            return (
              <li key={u.id} role="option" aria-selected={on}>
                <button type="button" className={`org-pick${on ? ' on' : ''}`} onClick={() => flip(u.id)}>
                  <span className="org-check" aria-hidden>
                    {on && <Check size={13} />}
                  </span>
                  <MemberLine member={u} note={note?.(u)} />
                </button>
              </li>
            )
          })}
        </ul>
      )}
      {action.error && (
        <Flash kind="error" title={action.error.message}>
          {action.error.detail}
        </Flash>
      )}
    </Modal>
  )
}
