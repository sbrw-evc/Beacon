import { AnimatePresence, motion } from 'motion/react'
import { useLocale, useT } from '../../i18n'
import { formatDate } from '../../ui'
import { useSession } from '../session'
import { Chips, CriticalityBadge, StatusBadge, TeamName } from './Badges'
import { strings } from './strings'
import { teamPath } from './teams'
import { CRITICALITIES, type Service } from './types'

export type Grouping = 'none' | 'owner' | 'criticality'

type Group = { key: string; title: string; items: Service[] }

function groupsOf(services: Service[], by: Grouping, t: (k: string) => string): Group[] {
  if (by === 'none') return [{ key: 'all', title: '', items: services }]
  if (by === 'criticality') {
    return CRITICALITIES.map((c) => ({ key: c, title: t(`svc.crit.${c}`), items: services.filter((s) => s.criticality === c) })).filter((g) => g.items.length)
  }
  const map = new Map<string, Group>()
  for (const s of services) {
    const key = s.owner.deleted ? '' : s.owner_team_id
    const g = map.get(key) ?? { key: key || 'deleted', title: teamPath(s.owner, t('svc.team.deleted')), items: [] }
    g.items.push(s)
    map.set(key, g)
  }
  return [...map.values()].sort((a, b) => (a.key === 'deleted' ? 1 : b.key === 'deleted' ? -1 : a.title.localeCompare(b.title)))
}

export function ServiceCards({ services, grouping, onOpen }: { services: Service[]; grouping: Grouping; onOpen: (s: Service) => void }) {
  const t = useT(strings)
  const groups = groupsOf(services, grouping, t)
  return (
    <div className="svc-groups" aria-label={t('svc.list')}>
      <AnimatePresence initial={false} mode="popLayout">
        {groups.map((g) => (
          <motion.section
            key={`${grouping}:${g.key}`}
            layout
            className="svc-group"
            initial={{ opacity: 0, y: 8 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.22, ease: [0.22, 1, 0.36, 1] }}
          >
            {g.title && (
              <h2 className="svc-group-title">
                {g.title}
                <span className="muted">{g.items.length}</span>
              </h2>
            )}
            <ul className="svc-cards">
              <AnimatePresence initial={false} mode="popLayout">
                {g.items.map((s) => (
                  <ServiceCard key={s.id} service={s} onOpen={onOpen} />
                ))}
              </AnimatePresence>
            </ul>
          </motion.section>
        ))}
      </AnimatePresence>
    </div>
  )
}

function ServiceCard({ service: s, onOpen }: { service: Service; onOpen: (s: Service) => void }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const deleted = t('svc.team.deleted')
  return (
    <motion.li
      layout
      className="svc-card-item"
      initial={{ opacity: 0, scale: 0.97 }}
      animate={{ opacity: 1, scale: 1 }}
      exit={{ opacity: 0, scale: 0.97 }}
      transition={{ duration: 0.2 }}
    >
      <button type="button" className={`card svc-card svc-crit-${s.criticality}`} onClick={() => onOpen(s)}>
        <div className="svc-card-head">
          <span className="svc-card-name">{s.name}</span>
          <CriticalityBadge value={s.criticality} />
        </div>
        {s.description && <p className="svc-card-desc muted">{s.description}</p>}
        <dl className="svc-card-facts">
          <div>
            <dt>{t('svc.col.owner')}</dt>
            <dd>
              <TeamName team={s.owner} />
            </dd>
          </div>
          {s.teams.length > 0 && (
            <div>
              <dt>{t('svc.col.teams')}</dt>
              <dd>
                <Chips items={s.teams.map((x) => ({ key: x.id, label: teamPath(x, deleted), muted: x.deleted }))} />
              </dd>
            </div>
          )}
          {s.cis.length > 0 && (
            <div>
              <dt>{t('svc.cis')}</dt>
              <dd>{s.cis.length}</dd>
            </div>
          )}
          {s.tags.length > 0 && (
            <div>
              <dt>{t('svc.col.tags')}</dt>
              <dd>
                <Chips items={s.tags.map((x) => ({ key: x, label: `#${x}` }))} />
              </dd>
            </div>
          )}
        </dl>
        <div className="svc-card-foot">
          <span className="row">
            <StatusBadge value={s.status} />
            {s.netbox && <span className="pill ci-source ci-source-netbox">NetBox</span>}
          </span>
          <span className="muted">
            {t('svc.col.updated')}: {formatDate(s.updated_at, locale, timezone)}
          </span>
        </div>
      </button>
    </motion.li>
  )
}
