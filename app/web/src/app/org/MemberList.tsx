import type { ReactNode } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { Avatar } from '../../Avatar'
import { useT } from '../../i18n'
import { orgStrings } from './strings'
import type { Member } from './types'
import './org.css'

export function MemberLine({ member, note }: { member: Member; note?: ReactNode }) {
  const t = useT(orgStrings)
  return (
    <span className="org-member">
      <Avatar user={member} size={32} />
      <span className="org-member-text">
        <span className="org-member-name">
          {member.name}
          {member.disabled && <span className="pill pill-off">{t('org.locked')}</span>}
        </span>
        <span className="hint">
          {member.username} · {t(`org.source.${member.source}`)}
          {note ? <> · {note}</> : null}
        </span>
      </span>
    </span>
  )
}

export function MemberList({
  members,
  empty,
  aside,
  note,
}: {
  members: Member[]
  empty: string
  aside?: (m: Member) => ReactNode
  note?: (m: Member) => ReactNode
}) {
  if (members.length === 0) return <p className="muted">{empty}</p>
  return (
    <ul className="org-members">
      <AnimatePresence initial={false}>
        {members.map((m) => (
          <motion.li
            key={m.id}
            layout
            initial={{ opacity: 0, y: -4 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, height: 0 }}
            transition={{ duration: 0.18 }}
          >
            <MemberLine member={m} note={note?.(m)} />
            {aside && <span className="org-member-aside">{aside(m)}</span>}
          </motion.li>
        ))}
      </AnimatePresence>
    </ul>
  )
}
