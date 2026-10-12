import { Check, Repeat } from 'lucide-react'
import { useT } from '../../i18n'
import { Button } from '../../ui'
import { mobileStrings } from '../mobile/mobileStrings'
import { StatusPill } from './IncidentDetail'
import { ago, severityTone } from './format'
import { strings } from './strings'
import { SEVERITY_PRIORITY, type Incident } from './types'

// IncidentCard is an incident in the phone layout: the priority on a coloured edge, the title,
// where it fires and who owns it, and a quick acknowledgement for an open incident.
export function IncidentCard({
  a,
  now,
  onOpen,
  onAck,
  busy,
  selecting,
  checked,
  onCheck,
}: {
  a: Incident
  now: number
  onOpen: () => void
  onAck?: () => void
  busy?: boolean
  selecting?: boolean
  checked?: boolean
  onCheck?: (v: boolean) => void
}) {
  const t = useT(strings)
  const tm = useT(mobileStrings)
  const tone = severityTone(a.severity)
  const owner = [a.route.services[0]?.name, a.route.team?.name].filter(Boolean).join(' · ')
  const selectable = selecting && a.status !== 'resolved'
  return (
    <article className={`m-inc tone-${tone}${onAck && !selecting && a.status === 'open' ? ' has-ack' : ''}${a.status === 'resolved' ? ' m-inc-resolved' : ''}${checked ? ' checked' : ''}`}>
      {selectable && (
        <label className="m-inc-check">
          <input type="checkbox" aria-label={t('inc.select.one', { id: a.id })} checked={!!checked} onChange={(e) => onCheck?.(e.target.checked)} />
        </label>
      )}
      <button type="button" className="m-inc-main" onClick={selectable ? () => onCheck?.(!checked) : onOpen}>
        <span className="m-inc-top">
          <span className="m-inc-prio">{SEVERITY_PRIORITY[a.severity] ?? a.severity}</span>
          <StatusPill status={a.status} />
          {a.suppressed && <span className="pill pill-off">{t(a.excluded ? 'inc.badge.excluded' : 'inc.badge.suppressed')}</span>}
          {a.fallback && a.status !== 'resolved' && <span className="pill pill-warn">{t('inc.badge.fallback')}</span>}
          {a.labels?.umbrella_test === 'true' && <span className="pill pill-off">{t('inc.badge.test')}</span>}
          <span className="m-inc-ago" title={a.last_seen}>
            {ago(t, a.last_seen, now)}
          </span>
        </span>
        <span className="m-inc-title">{a.title}</span>
        <span className="m-inc-meta">
          <span className={a.ci_id ? 'm-inc-ci' : 'm-inc-ci inc-warn'}>{a.ci_name || t('inc.noci')}</span>
          {a.count > 1 && (
            <span className="m-inc-count" title={t('inc.col.count')}>
              <Repeat size={12} aria-hidden />
              {a.count}
            </span>
          )}
        </span>
        <span className="m-inc-owner">
          <span>{owner || t('inc.noroute')}</span>
        </span>
      </button>
      {onAck && !selecting && a.status === 'open' && (
        <div className="m-inc-actions">
          <Button busy={busy} onClick={onAck}>
            <Check size={15} aria-hidden />
            {tm('m.ack')}
          </Button>
        </div>
      )}
    </article>
  )
}
