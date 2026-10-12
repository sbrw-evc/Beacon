import { useMemo } from 'react'
import { useT } from '../../i18n'
import { Field, Select } from '../../ui'
import { ADMIN } from '../roles/permissions'
import { MultiPicker } from '../services/Pickers'
import { useSession } from '../session'
import { roleLabel } from '../types'
import { adminOf, teamOptions, type Refs, type ScopeService } from './model'
import { strings } from './strings'
import '../services/services.css'

export type Access = { role_id: string; team_ids: string[] }

export function RoleSelect({
  id,
  refs,
  value,
  onChange,
  allLabel,
}: {
  id: string
  refs: Refs
  value: string
  onChange: (v: string) => void
  allLabel?: string
}) {
  const t = useT(strings)
  const { user } = useSession()
  return (
    <Select id={id} value={value} onChange={(e) => onChange(e.target.value)}>
      {allLabel !== undefined && <option value="">{allLabel}</option>}
      {refs.roles.map((r) => (
        <option key={r.id} value={r.id} disabled={allLabel === undefined && r.id === ADMIN && !adminOf(user) && value !== r.id}>
          {roleLabel(t, r.id, r.name)}
        </option>
      ))}
    </Select>
  )
}

export function TeamSelect({
  id,
  refs,
  value,
  onChange,
  emptyLabel,
}: {
  id: string
  refs: Refs
  value: string
  onChange: (v: string) => void
  emptyLabel: string
}) {
  const options = useMemo(() => teamOptions(refs.teams), [refs.teams])
  return (
    <Select id={id} value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">{emptyLabel}</option>
      {options.map((o) => (
        <option key={o.id} value={o.id} title={o.path}>
          {'\u2003'.repeat(o.depth) + o.name}
        </option>
      ))}
    </Select>
  )
}

/** TeamsPicker picks the teams of a user: a person may be a member of several teams. */
export function TeamsPicker({ id, refs, value, onChange }: { id: string; refs: Refs; value: string[]; onChange: (ids: string[]) => void }) {
  const t = useT(strings)
  const options = useMemo(() => teamOptions(refs.teams), [refs.teams])
  const paths = useMemo(() => new Map(options.map((o) => [o.id, o.path])), [options])
  const labelOf = (key: string) => {
    const path = paths.get(key)
    return { key, label: path ?? t('usr.team.missing', { id: key }), title: path ?? key, muted: !path }
  }
  return (
    <MultiPicker
      id={id}
      selected={value}
      options={options.map((o) => ({ id: o.id, label: o.name, depth: o.depth }))}
      labelOf={labelOf}
      placeholder={t('usr.teams.add')}
      onChange={onChange}
    />
  )
}

export function AccessFields({
  refs,
  value,
  onChange,
  roleLocked,
  roleHint,
}: {
  refs: Refs
  value: Access
  onChange: (v: Access) => void
  roleLocked?: boolean
  roleHint?: string
}) {
  const t = useT(strings)
  return (
    <div className="grid-2">
      <Field label={t('usr.field.role')} hint={roleHint}>
        {(id) => (
          <fieldset className="plain-fieldset" disabled={roleLocked}>
            <RoleSelect id={id} refs={refs} value={value.role_id} onChange={(role_id) => onChange({ ...value, role_id })} />
          </fieldset>
        )}
      </Field>
      <Field label={t('usr.field.teams')} hint={t('usr.field.teams.hint')}>
        {(id) => <TeamsPicker id={id} refs={refs} value={value.team_ids} onChange={(team_ids) => onChange({ ...value, team_ids })} />}
      </Field>
    </div>
  )
}

export type ScopeMode = 'all' | 'teams' | 'services'

/** ScopeField picks what the user sees: everything, the services of their teams or chosen services. */
export function ScopeField({
  refs,
  mode,
  value,
  known,
  admin,
  onMode,
  onChange,
}: {
  refs: Refs
  mode: ScopeMode
  value: string[]
  known?: ScopeService[]
  admin: boolean
  onMode: (m: ScopeMode) => void
  onChange: (ids: string[]) => void
}) {
  const t = useT(strings)
  const names = useMemo(() => new Map(refs.services.map((s) => [s.id, s.name])), [refs.services])
  const options = useMemo(() => refs.services.map((s) => ({ id: s.id, label: s.name })), [refs.services])
  const labelOf = (id: string) => {
    const name = names.get(id) ?? known?.find((s) => s.id === id)?.name ?? id
    const missing = !names.has(id)
    return { key: id, label: missing ? t('usr.scope.missing', { name }) : name, title: name, muted: missing }
  }
  return (
    <>
      <Field label={t('usr.field.scope')} hint={admin ? t('usr.scope.admin') : t(`usr.scope.mode.${mode}.hint`)}>
        {(id) => (
          <Select id={id} value={mode} onChange={(e) => onMode(e.target.value as ScopeMode)}>
            <option value="all">{t('usr.scope.mode.all')}</option>
            <option value="teams">{t('usr.scope.mode.teams')}</option>
            <option value="services">{t('usr.scope.mode.services')}</option>
          </Select>
        )}
      </Field>
      {mode === 'services' && (
        <Field label={t('usr.field.scope.services')} hint={value.length === 0 ? t('usr.scope.services.need') : undefined}>
          {(id) => <MultiPicker id={id} selected={value} options={options} labelOf={labelOf} placeholder={t('usr.scope.add')} onChange={onChange} />}
        </Field>
      )}
    </>
  )
}
