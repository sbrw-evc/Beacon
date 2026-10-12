import { X } from 'lucide-react'
import type { ReactNode } from 'react'
import { useT } from '../../i18n'
import { Select } from '../../ui'
import { severityText, type Severity } from '../incidents/types'
import { SeverityPill } from '../incidents/IncidentDetail'
import { responseStrings } from './strings'
import type { Assessment, Reason } from './types'

// Toggles is a row of toggle chips: each value on or off.
export function Toggles<T extends string>({
  values,
  selected,
  onChange,
  label,
  word,
  disabled,
  off,
}: {
  values: T[]
  selected: T[]
  onChange: (v: T[]) => void
  label: string
  word: (v: T) => string
  disabled?: boolean
  // off marks values that are on but cannot work now (a channel that is turned off).
  off?: (v: T) => boolean
}) {
  const sel = selected ?? []
  return (
    <div className="rs-toggles" role="group" aria-label={label}>
      {values.map((v) => {
        const on = sel.includes(v)
        return (
          <button
            key={v}
            type="button"
            aria-pressed={on}
            disabled={disabled}
            className={`chip rs-toggle${on ? ' on' : ''}${on && off?.(v) ? ' warn' : ''}`}
            onClick={() => onChange(on ? sel.filter((x) => x !== v) : [...sel, v])}
          >
            {word(v)}
          </button>
        )
      })}
    </div>
  )
}

// PickList is a list of chosen items (people, teams) with a drop-down to add one more.
export function PickList({
  items,
  selected,
  onChange,
  label,
  disabled,
}: {
  items: { id: string; name: string }[]
  selected: string[]
  onChange: (v: string[]) => void
  label: string
  disabled?: boolean
}) {
  const sel = selected ?? []
  const byId = new Map(items.map((i) => [i.id, i.name]))
  const rest = items.filter((i) => !sel.includes(i.id))
  return (
    <div className="rs-picks">
      {sel.map((id) => (
        <span key={id} className="rs-pick">
          {byId.get(id) ?? id}
          {!disabled && (
            <button type="button" className="icon-btn" aria-label={`${label}: ${byId.get(id) ?? id}`} onClick={() => onChange(sel.filter((x) => x !== id))}>
              <X size={12} />
            </button>
          )}
        </span>
      ))}
      {!disabled && rest.length > 0 && (
        <Select aria-label={label} value="" className="rs-pick-add" onChange={(e) => e.target.value && onChange([...sel, e.target.value])}>
          <option value="">{`+ ${label}`}</option>
          {rest.map((i) => (
            <option key={i.id} value={i.id}>
              {i.name}
            </option>
          ))}
        </Select>
      )}
    </div>
  )
}

type T = (key: string, vars?: Record<string, string | number>) => string

// reasonText is a reason of the assessment in words of the interface.
export function reasonText(t: T, r: Reason) {
  const a: Record<string, string> = { ...(r.args ?? {}) }
  for (const k of ['impact', 'from', 'to']) if (a[k]) a[k] = t(`impact.${a[k]}`).toLowerCase()
  for (const k of ['urgency', 'priority']) if (a[k]) a[k] = severityText(t, a[k])
  if (a.criticality) a.criticality = t(`crit.${a.criticality}`).toLowerCase()
  return t(`reason.${r.code}`, a)
}

// AssessmentView shows the impact of an incident and why it got its priority.
export function AssessmentView({ as, extra }: { as: Assessment; extra?: ReactNode }) {
  const t = useT(responseStrings)
  return (
    <div className="rs-assess">
      <div className="rs-assess-head">
        <div>
          <span className="hint">{t('rs.as.priority')}</span>
          <SeverityPill severity={as.priority as Severity} />
        </div>
        <div>
          <span className="hint">{t('rs.as.impact')}</span>
          <span className={`pill rs-impact rs-impact-${as.impact}`}>{t(`impact.${as.impact}`)}</span>
        </div>
        <div>
          <span className="hint">{t('rs.as.urgency')}</span>
          <span className="pill">{severityText(t, as.urgency)}</span>
        </div>
        {extra}
      </div>
      {as.services.length > 0 && (
        <div>
          <span className="hint">{t('rs.as.services')}</span>
          <ul className="rs-services">
            {as.services.map((s) => (
              <li key={s.id}>
                <b>{s.name}</b> <span className={`pill rs-crit rs-crit-${s.criticality}`}>{t(`crit.${s.criticality}`)}</span>{' '}
                <span className="muted">{s.direct ? t('rs.as.direct') : t('rs.as.via', { name: s.via ?? '' })}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
      <div>
        <span className="hint">{t('rs.as.why')}</span>
        <ol className="rs-reasons">
          {as.reasons.map((r, i) => (
            <li key={i}>{reasonText(t, r)}</li>
          ))}
        </ol>
      </div>
    </div>
  )
}
