import { ExternalLink } from 'lucide-react'
import { useLocale, useT } from '../../i18n'
import { Button, formatDate, Modal, Rows } from '../../ui'
import { RoutePreview } from '../routing/RoutePreview'
import { useSession } from '../session'
import { Chips, CriticalityBadge, StatusBadge, TeamName } from './Badges'
import { CIBindings, DependencyBindings, NetBoxLink } from './Bindings'
import { strings } from './strings'
import { teamPath } from './teams'
import type { Service } from './types'

export function ServiceDetail({
  service,
  editable,
  onClose,
  onEdit,
  onDelete,
  onChanged,
}: {
  service: Service | null
  editable: boolean
  onClose: () => void
  onEdit: (s: Service) => void
  onDelete: (s: Service) => void
  onChanged: (s: Service) => void
}) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const deleted = t('svc.team.deleted')
  const none = <span className="muted">{t('svc.none')}</span>
  const footer =
    service && editable ? (
      <>
        <Button variant="danger-soft" className="svc-danger" onClick={() => onDelete(service)}>
          {t('svc.delete')}
        </Button>
        <Button variant="primary" onClick={() => onEdit(service)}>
          {t('svc.edit')}
        </Button>
      </>
    ) : (
      <Button onClick={onClose}>{t('svc.close')}</Button>
    )
  return (
    <Modal open={service !== null} title={service?.name ?? ''} onClose={onClose} footer={footer}>
      {service && (
        <>
          <div className="row">
            <CriticalityBadge value={service.criticality} />
            <StatusBadge value={service.status} />
          </div>
          {service.description && <p className="svc-description">{service.description}</p>}
          <Rows
            rows={[
              [t('svc.field.owner'), <TeamName key="o" team={service.owner} />],
              [
                t('svc.field.teams'),
                service.teams.length ? (
                  <Chips key="t" items={service.teams.map((x) => ({ key: x.id, label: teamPath(x, deleted), muted: x.deleted }))} />
                ) : (
                  none
                ),
              ],
              [t('svc.field.tags'), service.tags.length ? <Chips key="g" items={service.tags.map((x) => ({ key: x, label: `#${x}` }))} /> : none],
              [
                t('svc.field.links'),
                service.links.length ? (
                  <ul key="l" className="svc-link-list">
                    {service.links.map((l, i) => (
                      <li key={i}>
                        <a href={l.url} target="_blank" rel="noopener noreferrer">
                          {l.title || l.url}
                          <ExternalLink size={13} aria-hidden />
                        </a>
                      </li>
                    ))}
                  </ul>
                ) : (
                  none
                ),
              ],
              [
                t('svc.field.dependents'),
                service.dependents.length ? <Chips key="u" items={service.dependents.map((x) => ({ key: x.id, label: x.name }))} /> : none,
              ],
              [t('svc.field.id'), <code key="i">{service.id}</code>],
              [t('svc.field.created'), formatDate(service.created_at, locale, timezone)],
              [t('svc.field.updated'), formatDate(service.updated_at, locale, timezone)],
            ]}
          />
          <RoutePreview kind="services" id={service.id} version={`${service.updated_at}|${service.cis.length}`} />
          <CIBindings service={service} editable={editable} onChanged={onChanged} />
          <DependencyBindings service={service} editable={editable} onChanged={onChanged} />
          <NetBoxLink service={service} editable={editable} onChanged={onChanged} />
        </>
      )}
    </Modal>
  )
}
