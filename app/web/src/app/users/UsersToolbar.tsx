import { useEffect, useState } from 'react'
import { Plus, Search, X } from 'lucide-react'
import { useT } from '../../i18n'
import { Button, Field, Input, Select } from '../../ui'
import { useSession } from '../session'
import { RoleSelect, TeamSelect } from './AccessFields'
import { NO_FILTERS, type Filters, type Refs } from './model'
import { strings } from './strings'

const SEARCH_DELAY = 250

export function UsersToolbar({ refs, filters, onChange, onCreate }: { refs: Refs; filters: Filters; onChange: (f: Filters) => void; onCreate: () => void }) {
  const t = useT(strings)
  const { can } = useSession()
  const [q, setQ] = useState(filters.q)

  useEffect(() => setQ(filters.q), [filters.q])

  useEffect(() => {
    if (q === filters.q) return
    const timer = window.setTimeout(() => onChange({ ...filters, q }), SEARCH_DELAY)
    return () => window.clearTimeout(timer)
  }, [q, filters, onChange])

  const set = (k: keyof Filters) => (v: string) => onChange({ ...filters, [k]: v })
  const any = t('usr.filter.any')
  const filtered = Object.values(filters).some(Boolean)

  return (
    <div className="card usr-toolbar">
      <div className="usr-toolbar-top">
        <div className="usr-search">
          <Search size={16} aria-hidden />
          <Input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t('usr.search')} aria-label={t('usr.search')} />
        </div>
        {filtered && (
          <Button variant="ghost" onClick={() => onChange(NO_FILTERS)}>
            <X size={16} aria-hidden />
            {t('usr.filter.reset')}
          </Button>
        )}
        {can('users:create') && (
          <Button variant="primary" onClick={onCreate}>
            <Plus size={16} aria-hidden />
            {t('usr.create')}
          </Button>
        )}
      </div>
      <div className="usr-filters">
        <Field label={t('usr.filter.source')}>
          {(id) => (
            <Select id={id} value={filters.source} onChange={(e) => set('source')(e.target.value)}>
              <option value="">{any}</option>
              <option value="local">{t('usr.source.local')}</option>
              <option value="ldap">{t('usr.source.ldap')}</option>
              <option value="entra">{t('usr.source.entra')}</option>
              <option value="netbox">{t('usr.source.netbox')}</option>
            </Select>
          )}
        </Field>
        <Field label={t('usr.filter.role')}>{(id) => <RoleSelect id={id} refs={refs} value={filters.role} onChange={set('role')} allLabel={any} />}</Field>
        <Field label={t('usr.filter.team')}>{(id) => <TeamSelect id={id} refs={refs} value={filters.team} onChange={set('team')} emptyLabel={any} />}</Field>
        <Field label={t('usr.filter.status')}>
          {(id) => (
            <Select id={id} value={filters.status} onChange={(e) => set('status')(e.target.value)}>
              <option value="">{any}</option>
              <option value="active">{t('usr.status.active')}</option>
              <option value="locked">{t('usr.status.locked')}</option>
            </Select>
          )}
        </Field>
      </div>
    </div>
  )
}
