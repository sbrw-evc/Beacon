import type { ReactNode } from 'react'
import { ExternalLink } from 'lucide-react'
import { api } from '../../api'
import { ErrorFlash } from '../../connections/ConnectionCard'
import { useAction } from '../../connections/useRequest'
import { useLocale, useT } from '../../i18n'
import { Banner, Button, formatDate, Modal, Rows } from '../../ui'
import { RoutePreview } from '../routing/RoutePreview'
import { Chips } from '../services/Badges'
import { useSession } from '../session'
import { Aliases } from './Aliases'
import { LedgerCreateLink, useInventorySource } from '../inventorySource'
import { MonitorState, SourcePill, StatusPill } from './Badges'
import { strings } from './strings'
import type { Attrs, CI } from './types'

const ATTRS: (keyof Attrs)[] = ['site', 'role', 'device_type', 'cluster', 'tenant', 'platform', 'parent', 'ports', 'serial']

type Props = {
  ci: CI | null
  editable: boolean
  onClose: () => void
  onEdit: (ci: CI) => void
  onDelete: (ci: CI) => void
  onChanged: (ci: CI) => void
}

export function CIDetail({ ci, editable, onClose, onEdit, onDelete, onChanged }: Props) {
  const t = useT(strings)
  const register = useAction()
  const source = useInventorySource()
  // A device made by hand while Ledger is the source belongs in Ledger.
  const toLedger = source?.source === 'ledger' && ci?.source === 'local' && ci.kind === 'device' && !ci.inventory_db
  const close = () => {
    register.clear()
    onClose()
  }
  const footer =
    ci && editable ? (
      <>
        <Button variant="danger-soft" className="svc-danger" onClick={() => onDelete(ci)}>
          {t('ci.delete')}
        </Button>
        {toLedger && source && <LedgerCreateLink source={source} name={ci.name} />}
        {ci.registrable && (
          <Button
            busy={register.busy}
            onClick={() =>
              void register.run(async () => {
                onChanged(await api<CI>('POST', `/api/cis/${ci.id}/netbox`))
              })
            }
          >
            {t('ci.register')}
          </Button>
        )}
        {ci.editable && (
          <Button variant="primary" onClick={() => onEdit(ci)}>
            {t('ci.edit')}
          </Button>
        )}
      </>
    ) : (
      <Button onClick={close}>{t('ci.close')}</Button>
    )
  return (
    <Modal open={ci !== null} title={ci?.name ?? ''} onClose={close} footer={footer}>
      {ci && <Body ci={ci} editable={editable} onChanged={onChanged} />}
      <ErrorFlash error={register.error} strings={strings} />
    </Modal>
  )
}

function Body({ ci, editable, onChanged }: { ci: CI; editable: boolean; onChanged: (ci: CI) => void }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const none = <span className="muted">{t('ci.none')}</span>
  const rows: [string, ReactNode][] = [
    [t('ci.field.kind'), t(`ci.kind.${ci.kind}`)],
    [
      t('ci.field.owners'),
      ci.owners.length ? (
        <ul key="o" className="ci-owners">
          {ci.owners.map((o) => (
            <li key={o.id + o.role}>
              <span className={o.deleted ? 'muted' : undefined}>{o.deleted ? t('ci.owner.deleted') : o.name || o.username}</span>
              <span className="muted"> · {o.role || t('ci.owner.default')}</span>
              {o.email && (
                <>
                  {' · '}
                  <a href={`mailto:${o.email}`}>{o.email}</a>
                </>
              )}
            </li>
          ))}
        </ul>
      ) : (
        none
      ),
    ],
    [t('ci.field.services'), ci.services.length ? <Chips key="s" items={ci.services.map((x) => ({ key: x.id, label: x.name }))} /> : none],
    [t('ci.field.ips'), ci.ips.length ? <span className="cn-mono">{ci.ips.join(', ')}</span> : none],
    [t('ci.field.aliases'), <Aliases key={`a-${ci.id}`} ci={ci} editable={editable} onChanged={onChanged} />],
    [t('ci.field.tags'), ci.tags.length ? <Chips key="g" items={ci.tags.map((x) => ({ key: x, label: `#${x}` }))} /> : none],
  ]
  for (const a of ATTRS) if (ci.attrs[a]) rows.push([t(`ci.field.${a}`), ci.attrs[a]])
  rows.push([
    t('ci.field.netbox'),
    ci.netbox ? (
      <a key="n" href={ci.netbox.url} target="_blank" rel="noopener noreferrer">
        {t('ci.netbox.open')} #{ci.netbox.id}
        <ExternalLink size={13} aria-hidden />
      </a>
    ) : (
      t('ci.netbox.none')
    ),
  ])
  if (ci.flow_url)
    rows.push([
      t('ci.field.traffic'),
      <a key="f" href={ci.flow_url} target="_blank" rel="noopener noreferrer">
        {t('ci.flow.open')}
        <ExternalLink size={13} aria-hidden />
      </a>,
    ])
  if (ci.directory) rows.push([t('ci.field.directory'), <Directory key="d" ci={ci} />])
  if (ci.presence.length > 0) rows.push([t('ci.field.presence'), <PresenceList key="p" ci={ci} />])
  rows.push(
    [t('ci.field.id'), <code key="i">{ci.id}</code>],
    [t('ci.field.created'), `${formatDate(ci.created_at, locale, timezone)} · ${ci.created_by}`],
    [t('ci.field.updated'), `${formatDate(ci.updated_at, locale, timezone)} · ${ci.updated_by}`],
  )
  if (ci.synced_at) rows.push([t('ci.field.synced'), formatDate(ci.synced_at, locale, timezone)])
  return (
    <>
      <div className="row">
        <StatusPill status={ci.status} />
        <SourcePill ci={ci} />
      </div>
      {ci.description && <p className="svc-description">{ci.description}</p>}
      {!ci.editable && <Banner kind="info" title={t('ci.imported')} />}
      <Rows rows={rows} />
      <RoutePreview kind="cis" id={ci.id} version={ci.updated_at} />
    </>
  )
}

function Directory({ ci }: { ci: CI }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const d = ci.directory
  if (!d) return null
  if (d.status === 'missing') return <span className="pill pill-warn">{t('ci.dir.missing')}</span>
  return (
    <div className="ci-dir">
      <div className="row">
        <span className="pill pill-ok">{t('ci.dir.matched')}</span>
        {d.disabled && <span className="pill pill-warn">{t('ci.dir.disabled')}</span>}
      </div>
      {d.dns_name && (
        <div>
          <span className="muted">{t('ci.dir.dns')}:</span> <code>{d.dns_name}</code>
        </div>
      )}
      {d.os && (
        <div>
          <span className="muted">{t('ci.dir.os')}:</span> {[d.os, d.os_version].filter(Boolean).join(' ')}
        </div>
      )}
      {d.last_logon && (
        <div>
          <span className="muted">{t('ci.dir.logon')}:</span> {formatDate(d.last_logon, locale, timezone)}
        </div>
      )}
      {d.dn && (
        <div className="ci-dn">
          <span className="muted">{t('ci.dir.dn')}:</span> <code>{d.dn}</code>
        </div>
      )}
    </div>
  )
}

function PresenceList({ ci }: { ci: CI }) {
  const t = useT(strings)
  return (
    <ul className="ci-monitors">
      {ci.presence.map((p) => (
        <li key={p.kind + (p.source_id ?? '')}>
          {p.state === 'present' ? (
            p.host_state && p.kind !== 'netbox' ? (
              <MonitorState state={p.host_state} />
            ) : (
              <span className="pill pill-ok">{t('ci.presence.yes')}</span>
            )
          ) : (
            <span className="pill pill-off">{t('ci.presence.no')}</span>
          )}{' '}
          <span className="cn-name">{p.name}</span>
          {p.detail && (
            <>
              {' · '}
              {p.url ? (
                <a href={p.url} target="_blank" rel="noopener noreferrer">
                  {p.detail}
                  <ExternalLink size={13} aria-hidden />
                </a>
              ) : (
                <span className="muted">{p.detail}</span>
              )}
            </>
          )}
        </li>
      ))}
    </ul>
  )
}
