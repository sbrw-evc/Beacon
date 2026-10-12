import { useCallback, useEffect, useMemo, useState } from 'react'
import { Plus, Search } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { ErrorBanner } from '../../connections/ConnectionCard'
import { useResource } from '../../connections/useRequest'
import { useT } from '../../i18n'
import { Banner, Button, Input, Segmented, Select } from '../../ui'
import { useSession } from '../session'
import { clearDeepLink, useDeepLink } from '../deepLink'
import { DeleteDialog } from './DeleteDialog'
import { OptionSelect, teamOptions } from './Pickers'
import { ServiceCards, type Grouping } from './ServiceCards'
import { ServiceDetail } from './ServiceDetail'
import { ServiceEditor } from './ServiceEditor'
import { strings } from './strings'
import { TeamTree } from './teams'
import { CRITICALITIES, NO_FILTERS, queryOf, STATUSES, type Filters, type Refs, type Service, type ServiceList } from './types'
import './services.css'
import { SetupGuide } from '../guide/SetupGuide'

type Editing = { service: Service | null } | null

function useDebounced<T>(value: T, ms: number) {
  const [v, setV] = useState(value)
  useEffect(() => {
    const id = window.setTimeout(() => setV(value), ms)
    return () => window.clearTimeout(id)
  }, [value, ms])
  return v
}

export function ServicesPage() {
  const t = useT(strings)
  const { can } = useSession()
  const editable = can('services:edit')
  const [epoch, setEpoch] = useState(0)
  const [filters, setFilters] = useState<Filters>(NO_FILTERS)
  const [grouping, setGrouping] = useState<Grouping>('none')
  const [selected, setSelected] = useState<Service | null>(null)
  useDeepLink<Service>('/api/services', setSelected)
  const [editing, setEditing] = useState<Editing>(null)
  const [deleting, setDeleting] = useState<Service | null>(null)
  const q = useDebounced(filters.q, 250)
  const list = useResource<ServiceList>(`/api/services${queryOf({ ...filters, q })}`, epoch)
  const refs = useResource<Refs>('/api/refs', epoch)
  const tree = useMemo(() => new TeamTree(refs.data?.teams ?? []), [refs.data])
  const owners = useMemo(() => teamOptions(tree.options()), [tree])
  const reload = useCallback(() => setEpoch((e) => e + 1), [])
  const set = (patch: Partial<Filters>) => setFilters((f) => ({ ...f, ...patch }))
  const filtered = Object.values(filters).some((v) => v.trim() !== '')

  if (!list.data) {
    return list.error ? <ErrorBanner error={list.error} strings={strings} /> : <p className="muted">{t('loading')}</p>
  }
  const { services, tags, total } = list.data
  const noTeams = refs.data !== null && refs.data.teams.length === 0

  return (
    <div className="svc-page">
      <SetupGuide page="services" actions={{ create: () => setEditing({ service: null }) }} />
      <div className="card svc-toolbar">
        <div className="svc-toolbar-row">
          <label className="svc-search">
            <Search size={16} aria-hidden />
            <Input type="search" value={filters.q} placeholder={t('svc.search')} aria-label={t('svc.search')} onChange={(e) => set({ q: e.target.value })} />
          </label>
          {editable && (
            <Button variant="primary" onClick={() => setEditing({ service: null })} disabled={noTeams}>
              <Plus size={16} />
              {t('svc.create')}
            </Button>
          )}
        </div>
        <div className="svc-filters">
          <label title={t('svc.filter.owner.hint')}>
            <span>{t('svc.filter.owner')}</span>
            <OptionSelect value={filters.owner} options={owners} placeholder={t('svc.filter.owner.any')} onChange={(v) => set({ owner: v })} />
          </label>
          <label>
            <span>{t('svc.filter.criticality')}</span>
            <Select value={filters.criticality} onChange={(e) => set({ criticality: e.target.value })}>
              <option value="">{t('svc.filter.any')}</option>
              {CRITICALITIES.map((c) => (
                <option key={c} value={c}>
                  {t(`svc.crit.${c}`)}
                </option>
              ))}
            </Select>
          </label>
          <label>
            <span>{t('svc.filter.status')}</span>
            <Select value={filters.status} onChange={(e) => set({ status: e.target.value })}>
              <option value="">{t('svc.filter.any')}</option>
              {STATUSES.map((s) => (
                <option key={s} value={s}>
                  {t(`svc.status.${s}`)}
                </option>
              ))}
            </Select>
          </label>
          <label>
            <span>{t('svc.filter.tag')}</span>
            <Select value={filters.tag} onChange={(e) => set({ tag: e.target.value })}>
              <option value="">{t('svc.filter.any')}</option>
              {tags.map((x) => (
                <option key={x} value={x}>
                  #{x}
                </option>
              ))}
            </Select>
          </label>
        </div>
        <div className="svc-toolbar-row">
          <Segmented
            label={t('svc.group')}
            value={grouping}
            onChange={setGrouping}
            options={[
              { value: 'none', label: t('svc.group.none') },
              { value: 'owner', label: t('svc.group.owner') },
              { value: 'criticality', label: t('svc.group.criticality') },
            ]}
          />
          <div className="row">
            {filtered && (
              <Button variant="ghost" onClick={() => setFilters(NO_FILTERS)}>
                {t('svc.filter.reset')}
              </Button>
            )}
            <span className="muted svc-count">{t('svc.count', { shown: services.length, total })}</span>
          </div>
        </div>
      </div>

      {editable && noTeams && <Banner kind="info" title={t('svc.noTeams')} />}
      {!editable && total > 0 && <p className="muted">{t('svc.readonly')}</p>}

      <AnimatePresence mode="wait" initial={false}>
        {services.length === 0 ? (
          <motion.div key="empty" className="card svc-empty" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}>
            <p>{t(filtered ? 'svc.empty.filtered' : 'svc.empty')}</p>
            {!filtered && editable && !noTeams && <p className="muted">{t('svc.empty.edit')}</p>}
          </motion.div>
        ) : (
          <motion.div key="list" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}>
            <ServiceCards services={services} grouping={grouping} onOpen={setSelected} />
          </motion.div>
        )}
      </AnimatePresence>

      <ServiceDetail
        service={selected}
        editable={editable}
        onClose={() => {
          clearDeepLink()
          setSelected(null)
        }}
        onEdit={(s) => {
          setSelected(null)
          setEditing({ service: s })
        }}
        onDelete={(s) => {
          setSelected(null)
          setDeleting(s)
        }}
        onChanged={(s) => {
          setSelected(s)
          reload()
        }}
      />
      <ServiceEditor
        open={editing !== null}
        service={editing?.service ?? null}
        tree={tree}
        tags={tags}
        onClose={() => setEditing(null)}
        onSaved={(s) => {
          setEditing(null)
          setSelected(s)
          reload()
        }}
      />
      <DeleteDialog
        service={deleting}
        onClose={() => setDeleting(null)}
        onDeleted={() => {
          setDeleting(null)
          reload()
        }}
      />
    </div>
  )
}
