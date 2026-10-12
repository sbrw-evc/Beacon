import type { ReactNode } from 'react'
import { X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useT } from '../../i18n'
import { strings } from './strings'
import { teamPath } from './teams'
import type { Criticality, Status, TeamLabel } from './types'

const criticalityTone: Record<Criticality, string> = { critical: 'error', high: 'warn', medium: 'info', low: 'off' }
const statusTone: Record<Status, string> = { active: 'ok', planned: 'info', retired: 'off' }

export function CriticalityBadge({ value }: { value: Criticality }) {
  const t = useT(strings)
  return <span className={`pill svc-tone-${criticalityTone[value]}`}>{t(`svc.crit.${value}`)}</span>
}

export function StatusBadge({ value }: { value: Status }) {
  const t = useT(strings)
  return <span className={`pill svc-status svc-tone-${statusTone[value]}`}>{t(`svc.status.${value}`)}</span>
}

export function TeamName({ team }: { team: TeamLabel }) {
  const t = useT(strings)
  return <span className={team.deleted ? 'svc-deleted' : undefined}>{teamPath(team, t('svc.team.deleted'))}</span>
}

export type ChipItem = { key: string; label: ReactNode; muted?: boolean; title?: string }

export function Chips({ items, onRemove, removeLabel }: { items: ChipItem[]; onRemove?: (key: string) => void; removeLabel?: (key: string) => string }) {
  if (items.length === 0) return null
  return (
    <ul className="svc-chips">
      <AnimatePresence initial={false}>
        {items.map((c) => (
          <motion.li
            key={c.key}
            layout
            className={`svc-chip ${c.muted ? 'svc-chip-muted' : ''}`}
            title={c.title}
            initial={{ opacity: 0, scale: 0.85 }}
            animate={{ opacity: 1, scale: 1 }}
            exit={{ opacity: 0, scale: 0.85 }}
            transition={{ duration: 0.16 }}
          >
            <span>{c.label}</span>
            {onRemove && (
              <button type="button" className="svc-chip-x" onClick={() => onRemove(c.key)} aria-label={removeLabel?.(c.key)}>
                <X size={12} />
              </button>
            )}
          </motion.li>
        ))}
      </AnimatePresence>
    </ul>
  )
}
