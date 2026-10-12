import { useCallback, useEffect, useState } from 'react'
import { Plus, Search } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { ErrorBanner } from '../../connections/ConnectionCard'
import { useResource } from '../../connections/useRequest'
import { useT } from '../../i18n'
import { Button, Input, Select } from '../../ui'
import { BindServicesDialog, type BindAction } from '../bulk/BulkDialogs'
import { strings as bulkStrings } from '../bulk/strings'
import { mergeDicts } from '../../connections/connectionStrings'
import { useSession } from '../session'
import { clearDeepLink, useDeepLink } from '../deepLink'
import { PresenceCell, SourcePill, StatusPill } from './Badges'
import { CIDetail } from './CIDetail'
import { CIEditor, DeleteDialog } from './CIEditor'
import { strings } from './strings'
import { FLAGS, KINDS, NO_FILTERS, queryOf, SOURCES, STATUSES, type CI, type CIList, type Filters, type Summary } from './types'
import '../services/services.css'
import '../connectors/connectors.css'
import './cis.css'
import '../bulk/bulk.css'
import { SetupGuide } from '../guide/SetupGuide'

const pageStrings = mergeDicts(strings, bulkStrings)

function useDebounced<T>(value: T, ms: number) {
  const [v, setV] = useState(value)
  useEffect(() => {
    const id = window.setTimeout(() => setV(value), ms)
    return () => window.clearTimeout(id)
  }, [value, ms])
  return v
}

export function CIsPage() {
  const t = useT(pageStrings)
  const { can } = useSession()
  const editable = can('cis:edit')
  const binder = can('services:edit') && can('services:view')
  const [checked, setChecked] = useState<Set<string>>(new Set())
  const [binding, setBinding] = useState<BindAction | null>(null)
  const [epoch, setEpoch] = useState(0)
  const [filters, setFilters] = useState<Filters>(NO_FILTERS)
  const [selected, setSelected] = useState<CI | null>(null)
  useDeepLink<CI>('/api/cis', setSelected)
  const [editing, setEditing] = useState<{ ci: CI | null } | null>(null)
  const [deleting, setDeleting] = useState<CI | null>(null)
  const q = useDebounced(filters.q, 250)
  const list = useResource<CIList>(`/api/cis${queryOf({ ...filters, q })}`, epoch)
  const reload = useCallback(() => setEpoch((e) => e + 1), [])
  const set = (patch: Partial<Filters>) => setFilters((f) => ({ ...f, ...patch }))
  const filtered = Object.values(filters).some((v) => v.trim() !== '')

  if (!list.data) {
    return list.error ? <ErrorBanner error={list.error} strings={strings} /> : <p className="muted">{t('loading')}</p>
  }
  const { items, summary } = list.data

  return (
    <div className="ci-page">
      <SetupGuide page="cis" actions={{ create: () => setEditing({ ci: null }) }} />
      <Tiles summary={summary} flag={filters.flag} onFlag={(flag) => set({ flag: filters.flag === flag ? '' : flag })} />
      <div className="card svc-toolbar">
        <div className="svc-toolbar-row">
          <label className="svc-search">
            <Search size={16} aria-hidden />
            <Input type="search" value={filters.q} placeholder={t('ci.search')} aria-label={t('ci.search')} onChange={(e) => set({ q: e.target.value })} />
          </label>
          {editable && (
            <Button variant="primary" onClick={() => setEditing({ ci: null })}>
              <Plus size={16} />
              {t('ci.create')}
            </Button>
          )}
        </div>
        <div className="svc-filters">
          <FilterSelect label={t('ci.filter.kind')} value={filters.kind} values={KINDS} prefix="ci.kind" onChange={(kind) => set({ kind })} />
          <FilterSelect label={t('ci.filter.status')} value={filters.status} values={STATUSES} prefix="ci.status" onChange={(status) => set({ status })} />
          <FilterSelect label={t('ci.filter.source')} value={filters.source} values={SOURCES} prefix="ci.source" onChange={(source) => set({ source })} />
        </div>
        <div className="svc-toolbar-row">
          {binder && checked.size > 0 ? (
            <div className="bulk-bar">
              <span className="muted">{t('bulk.selected', { n: checked.size })}</span>
              <Button onClick={() => setBinding('bind')}>{t('bulk.bind')}</Button>
              <Button onClick={() => setBinding('unbind')}>{t('bulk.unbind')}</Button>
              <Button variant="ghost" onClick={() => setChecked(new Set())}>
                {t('bulk.clear')}
              </Button>
            </div>
          ) : (
            <span />
          )}
          <div className="row">
            {filtered && (
              <Button variant="ghost" onClick={() => setFilters(NO_FILTERS)}>
                {t('ci.filter.reset')}
              </Button>
            )}
            <span className="muted svc-count">{t('ci.count', { shown: items.length, total: summary.total })}</span>
          </div>
        </div>
      </div>

      {!editable && summary.total > 0 && <p className="muted">{t('ci.readonly')}</p>}

      <AnimatePresence mode="wait" initial={false}>
        {items.length === 0 ? (
          <motion.div key="empty" className="card svc-empty" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}>
            <p>{t(filtered ? 'ci.empty.filtered' : 'ci.empty')}</p>
            {!filtered && editable && <p className="muted">{t('ci.empty.edit')}</p>}
          </motion.div>
        ) : (
          <motion.div key="list" className="card cn-table-wrap" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}>
            <CITable
              items={items}
              presence={items.some((ci) => ci.presence.length > 0)}
              onOpen={setSelected}
              checked={binder ? checked : null}
              onCheck={setChecked}
            />
          </motion.div>
        )}
      </AnimatePresence>

      <CIDetail
        ci={selected}
        editable={editable}
        onClose={() => {
          clearDeepLink()
          setSelected(null)
        }}
        onEdit={(ci) => {
          setSelected(null)
          setEditing({ ci })
        }}
        onDelete={(ci) => {
          setSelected(null)
          setDeleting(ci)
        }}
        onChanged={(ci) => {
          setSelected(ci)
          reload()
        }}
      />
      <CIEditor
        open={editing !== null}
        ci={editing?.ci ?? null}
        tags={list.data.tags}
        onClose={() => setEditing(null)}
        onSaved={(ci) => {
          setEditing(null)
          setSelected(ci)
          reload()
        }}
      />
      <BindServicesDialog action={binding} ciIDs={[...checked]} onClose={() => setBinding(null)} onDone={reload} />
      <DeleteDialog
        ci={deleting}
        onClose={() => setDeleting(null)}
        onDeleted={() => {
          const gone = deleting?.id
          setChecked((c) => new Set([...c].filter((id) => id !== gone)))
          setDeleting(null)
          reload()
        }}
      />
    </div>
  )
}

function FilterSelect({
  label,
  value,
  values,
  prefix,
  onChange,
}: {
  label: string
  value: string
  values: readonly string[]
  prefix: string
  onChange: (v: string) => void
}) {
  const t = useT(strings)
  return (
    <label>
      <span>{label}</span>
      <Select value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">{t('ci.filter.any')}</option>
        {values.map((v) => (
          <option key={v} value={v}>
            {t(`${prefix}.${v}`)}
          </option>
        ))}
      </Select>
    </label>
  )
}

function Tiles({ summary, flag, onFlag }: { summary: Summary; flag: string; onFlag: (flag: string) => void }) {
  const t = useT(strings)
  return (
    <div className="ci-tiles">
      <div className="card ci-tile">
        <span className="ci-tile-value">{summary.total}</span>
        <span className="muted">{t('ci.tile.total')}</span>
      </div>
      <div className="card ci-tile">
        <span className="ci-tile-value">{summary.netbox}</span>
        <span className="muted">{t('ci.tile.netbox')}</span>
      </div>
      <div className="card ci-tile">
        <span className="ci-tile-value">{summary.local}</span>
        <span className="muted">
          {t('ci.tile.local')}
          {summary.registered > 0 && <> · {t('ci.tile.registered', { n: summary.registered })}</>}
        </span>
      </div>
      {FLAGS.filter((f) => f !== 'not_monitored' || summary.monitoring_sources > 0).map((f) => (
        <button
          key={f}
          type="button"
          className={`card ci-tile ci-tile-flag ${flag === f ? 'active' : ''} ${summary[f] > 0 ? 'warn' : ''}`}
          aria-pressed={flag === f}
          onClick={() => onFlag(f)}
        >
          <span className="ci-tile-value">{summary[f]}</span>
          <span className="muted">{t(`ci.tile.${f}`)}</span>
        </button>
      ))}
    </div>
  )
}

function CITable({
  items,
  presence,
  onOpen,
  checked,
  onCheck,
}: {
  items: CI[]
  presence: boolean
  onOpen: (ci: CI) => void
  // checked is null when the items cannot be selected.
  checked: Set<string> | null
  onCheck: (s: Set<string>) => void
}) {
  const t = useT(pageStrings)
  const allChecked = checked !== null && items.length > 0 && items.every((ci) => checked.has(ci.id))
  const toggleAll = () => {
    if (!checked) return
    const n = new Set(checked)
    for (const ci of items) {
      if (allChecked) n.delete(ci.id)
      else n.add(ci.id)
    }
    onCheck(n)
  }
  const toggle = (id: string) => {
    if (!checked) return
    const n = new Set(checked)
    if (n.has(id)) n.delete(id)
    else n.add(id)
    onCheck(n)
  }
  return (
    <table className="cn-table ci-table">
      <thead>
        <tr>
          {checked && (
            <th className="bulk-check">
              <input type="checkbox" aria-label={t('bulk.selectAll')} checked={allChecked} onChange={toggleAll} />
            </th>
          )}
          <th>{t('ci.col.name')}</th>
          <th>{t('ci.col.kind')}</th>
          <th>{t('ci.col.status')}</th>
          <th>{t('ci.col.ips')}</th>
          <th>{t('ci.col.owners')}</th>
          {presence && <th>{t('ci.col.presence')}</th>}
          <th>{t('ci.col.source')}</th>
        </tr>
      </thead>
      <tbody>
        {items.map((ci) => (
          <tr key={ci.id}>
            {checked && (
              <td className="bulk-check">
                <input type="checkbox" aria-label={t('bulk.selectOne', { name: ci.name })} checked={checked.has(ci.id)} onChange={() => toggle(ci.id)} />
              </td>
            )}
            <td>
              <button type="button" className="cn-link cn-name" onClick={() => onOpen(ci)}>
                {ci.name}
              </button>
              {(ci.attrs.site || ci.attrs.parent) && <div className="muted">{ci.attrs.parent || ci.attrs.site}</div>}
            </td>
            <td>{t(`ci.kind.${ci.kind}`)}</td>
            <td>
              <StatusPill status={ci.status} />
            </td>
            <td className="cn-mono">{ci.ips.join(', ') || t('ci.none')}</td>
            <td>{ci.owners.length ? ci.owners.map((o) => o.name || o.username || t('ci.owner.deleted')).join(', ') : <span className="muted">{t('ci.none')}</span>}</td>
            {presence && (
              <td>
                <PresenceCell ci={ci} />
              </td>
            )}
            <td>
              <SourcePill ci={ci} />
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
