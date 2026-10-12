import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { ArrowDown, ArrowUp, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { api } from '../../../api'
import { useLocale, useT } from '../../../i18n'
import { Banner, Button, formatDate, Input, Rows, Select, Stepper, Switch } from '../../../ui'
import { loadRefs, type Refs } from '../../org/types'
import { ProfileCard } from '../../profile/ProfileCard'
import { useAction } from '../../profile/useAction'
import { useSession } from '../../session'
import { strings } from './strings'
import { Flash } from '../../../notify'

type Source = 'ldap' | 'entra'

type Mapping = { id: string; source: Source; group: string; label: string; role_id: string; team_id: string; scope?: '' | 'all' | 'teams' }

type GroupConfig = { mappings: Mapping[]; sync_off: boolean; sync_minutes: number }

type SourceState = { checked: boolean; users: number; changed: number; missing: number; error?: string }

type GroupsView = {
  config: GroupConfig
  sync: { started_at: string; finished_at: string; actor: string; ok: boolean; ldap: SourceState; entra: SourceState }
  ldap_enabled: boolean
  entra_enabled: boolean
  mapped: { roles: number; teams: number; scopes?: number }
}

type Row = Mapping & { key: number }
type Draft = { rows: Row[]; sync_off: boolean; sync_minutes: number }

const GUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const DN = /^[^=,]+=.+/

let nextKey = 1

function draftOf(c: GroupConfig): Draft {
  return { rows: c.mappings.map((m) => ({ ...m, key: nextKey++ })), sync_off: c.sync_off, sync_minutes: c.sync_minutes }
}

function configOf(d: Draft): GroupConfig {
  return {
    mappings: d.rows.map(({ key: _key, ...m }) => ({ ...m, group: m.group.trim(), label: m.label.trim() })),
    sync_off: d.sync_off,
    sync_minutes: d.sync_minutes,
  }
}

function groupKey(m: Mapping) {
  const g = m.group.trim().toLowerCase()
  return m.source + '|' + (m.source === 'ldap' ? g.replace(/\s*,\s*/g, ',').replace(/\s*=\s*/g, '=') : g)
}

function rowProblem(m: Mapping, rows: Mapping[]): string | null {
  const g = m.group.trim()
  if (m.source === 'ldap' ? !DN.test(g) : !GUID.test(g)) return `gm.group.invalid.${m.source}`
  if (!m.role_id && !m.team_id && !m.scope) return 'gm.need'
  if (rows.find((x) => groupKey(x) === groupKey(m)) !== m) return 'gm.duplicate'
  return null
}

// Sent by the LDAP and Entra ID cards after a save, so the table sees which directories are on.
export const DIRECTORY_CHANGED = 'umbrella:directory-changed'

export function GroupMappingSettings() {
  const t = useT(strings)
  const { locale } = useLocale()
  const { can, timezone } = useSession()
  const canEdit = can('settings.ldap:edit')
  const [view, setView] = useState<GroupsView | null>(null)
  const [refs, setRefs] = useState<Refs | null>(null)
  const [draft, setDraft] = useState<Draft>({ rows: [], sync_off: false, sync_minutes: 60 })
  const loader = useAction(strings)
  const saver = useAction(strings)
  const syncer = useAction(strings)

  const apply = useCallback((v: GroupsView) => {
    setView(v)
    setDraft(draftOf(v.config))
  }, [])

  const { run: runLoad } = loader
  useEffect(() => {
    const load = () =>
      void runLoad(async () => {
        const [v, r] = await Promise.all([api<GroupsView>('GET', '/api/settings/groups'), loadRefs()])
        setRefs(r)
        // Keep unsaved edits when a directory card reloads the view.
        setView(v)
        setDraft((d) => (d.rows.length === 0 && !d.sync_off && d.sync_minutes === 60 ? draftOf(v.config) : d))
      })
    load()
    // Only the enabled flags are refreshed; the draft stays as it is.
    const refresh = () => void api<GroupsView>('GET', '/api/settings/groups').then((v) => setView((cur) => (cur ? { ...cur, ldap_enabled: v.ldap_enabled, entra_enabled: v.entra_enabled } : v)), () => {})
    window.addEventListener(DIRECTORY_CHANGED, refresh)
    return () => window.removeEventListener(DIRECTORY_CHANGED, refresh)
  }, [runLoad])

  const teamNames = useMemo(() => {
    const byId = new Map((refs?.teams ?? []).map((x) => [x.id, x]))
    const path = (id: string, depth = 0): string => {
      const x = byId.get(id)
      if (!x) return ''
      const up = x.parent_id && depth < 8 ? path(x.parent_id, depth + 1) : ''
      return up ? `${up} / ${x.name}` : x.name
    }
    return [...byId.keys()].map((id) => ({ id, name: path(id) })).sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }))
  }, [refs])

  if (!view || !refs) {
    return loader.error ? (
      <Banner kind="error" title={loader.error.message}>
        {loader.error.detail}
      </Banner>
    ) : null
  }

  const ldapEnabled = view.ldap_enabled
  const entraEnabled = view.entra_enabled
  const sources: Source[] = []
  if (ldapEnabled || draft.rows.some((r) => r.source === 'ldap')) sources.push('ldap')
  if (entraEnabled || draft.rows.some((r) => r.source === 'entra')) sources.push('entra')
  const anyDirectory = ldapEnabled || entraEnabled
  const saved = JSON.stringify(configOf(draftOf(view.config)))
  const current = configOf(draft)
  const dirty = JSON.stringify(current) !== saved
  const problems = current.mappings.map((m) => rowProblem(m, current.mappings))
  const valid = problems.every((p) => p === null)

  const setRow = (key: number, patch: Partial<Mapping>) => setDraft({ ...draft, rows: draft.rows.map((r) => (r.key === key ? { ...r, ...patch } : r)) })
  const move = (i: number, d: number) => {
    const rows = [...draft.rows]
    ;[rows[i], rows[i + d]] = [rows[i + d], rows[i]]
    setDraft({ ...draft, rows })
  }
  const add = () =>
    setDraft({
      ...draft,
      rows: [...draft.rows, { key: nextKey++, id: '', source: ldapEnabled ? 'ldap' : 'entra', group: '', label: '', role_id: '', team_id: '', scope: '' }],
    })

  const save = () =>
    saver.run(async () => {
      apply(await api<GroupsView>('PUT', '/api/settings/groups', current))
      return t('gm.saved')
    })

  const sync = () =>
    syncer.run(async () => {
      const v = await api<GroupsView>('POST', '/api/settings/groups/sync')
      setView(v)
      return t('gm.synced')
    })

  const roleName = (id: string) => refs.roles.find((r) => r.id === id)?.name
  const s = view.sync
  const result = (src: SourceState, title: string): [ReactNode, ReactNode] | null =>
    src.checked
      ? [
          `${t('gm.state.result')} · ${title}`,
          src.error ? (
            <span className="gm-error">{t('gm.state.failed', { error: src.error })}</span>
          ) : (
            t('gm.state.result.value', { users: src.users, changed: src.changed, missing: src.missing })
          ),
        ]
      : null

  return (
    <ProfileCard
      title={t('gm.title')}
      action={saver}
      onSubmit={save}
      wide
      footer={
        <div className="row directory-actions">
          {dirty && <span className="hint">{t('gm.syncSave')}</span>}
          {canEdit && anyDirectory && (
            <Button type="button" onClick={sync} busy={syncer.busy} disabled={dirty}>
              <RefreshCw size={15} aria-hidden /> {syncer.busy ? t('gm.syncing') : t('gm.syncNow')}
            </Button>
          )}
          {canEdit && (
            <Button type="submit" variant="primary" busy={saver.busy} disabled={!dirty || !valid}>
              {t('gm.save')}
            </Button>
          )}
        </div>
      }
    >
      <p className="muted">{t('gm.text')}</p>
      <p className="hint">{t('gm.manual')}</p>
      {!anyDirectory && <Banner kind="info" title={t('gm.off')} />}
      <Rows
        align="end"
        rows={
          [
            [
              t('gm.state.last'),
              s.started_at && !s.started_at.startsWith('0001') ? (
                <span key="l">
                  {formatDate(s.finished_at, locale, timezone)} {t('gm.state.by', { actor: s.actor === 'group-sync' ? t('gm.state.schedule') : s.actor })}
                </span>
              ) : (
                t('gm.state.never')
              ),
            ],
            result(s.ldap, t('gm.source.ldap')),
            result(s.entra, t('gm.source.entra')),
            [t('gm.state.mapped'), t('gm.state.mapped.value', { roles: view.mapped.roles, teams: view.mapped.teams, scopes: view.mapped.scopes ?? 0 })],
          ].filter(Boolean) as [ReactNode, ReactNode][]
        }
      />
      {syncer.notice && <Flash kind="ok" title={syncer.notice} />}
      {syncer.error && (
        <Flash kind="error" title={syncer.error.message}>
          {syncer.error.detail}
        </Flash>
      )}
      <fieldset className="plain-fieldset stack" disabled={!canEdit}>
        {draft.rows.length === 0 ? (
          <p className="muted gm-empty">{t('gm.empty')}</p>
        ) : (
          <ol className="gm-rows">
            {draft.rows.map((r, i) => {
              const problem = problems[i]
              return (
                <li key={r.key} className="gm-row">
                  <div className="gm-fields">
                    {sources.length > 1 && (
                      <label className="gm-field gm-source">
                        <span className="hint">{t('gm.source')}</span>
                        <Select value={r.source} onChange={(e) => setRow(r.key, { source: e.target.value as Source })} aria-label={t('gm.source')}>
                          {sources.map((x) => (
                            <option key={x} value={x}>
                              {t(`gm.source.${x}`)}
                            </option>
                          ))}
                        </Select>
                      </label>
                    )}
                    <label className="gm-field gm-group">
                      <span className="hint">
                        {t('gm.group')}
                        {sources.length === 1 && ` · ${t(`gm.source.${r.source}`)}`}
                      </span>
                      <Input
                        value={r.group}
                        onChange={(e) => setRow(r.key, { group: e.target.value })}
                        placeholder={t(`gm.group.${r.source}`)}
                        spellCheck={false}
                        autoComplete="off"
                      />
                    </label>
                    <label className="gm-field gm-label">
                      <span className="hint">{t('gm.label')}</span>
                      <Input value={r.label} onChange={(e) => setRow(r.key, { label: e.target.value })} placeholder={t('gm.label.placeholder')} maxLength={100} />
                    </label>
                    <label className="gm-field">
                      <span className="hint">{t('gm.role')}</span>
                      <Select value={r.role_id} onChange={(e) => setRow(r.key, { role_id: e.target.value })} aria-label={t('gm.role')}>
                        <option value="">{t('gm.keep')}</option>
                        {r.role_id && !roleName(r.role_id) && <option value={r.role_id}>{`${r.role_id} (${t('gm.missing')})`}</option>}
                        {refs.roles.map((x) => (
                          <option key={x.id} value={x.id}>
                            {x.name}
                          </option>
                        ))}
                      </Select>
                    </label>
                    <label className="gm-field">
                      <span className="hint">{t('gm.team')}</span>
                      <Select value={r.team_id} onChange={(e) => setRow(r.key, { team_id: e.target.value })} aria-label={t('gm.team')}>
                        <option value="">{t('gm.keep')}</option>
                        {r.team_id && !teamNames.some((x) => x.id === r.team_id) && <option value={r.team_id}>{`${r.team_id} (${t('gm.missing')})`}</option>}
                        {teamNames.map((x) => (
                          <option key={x.id} value={x.id}>
                            {x.name}
                          </option>
                        ))}
                      </Select>
                    </label>
                    <label className="gm-field">
                      <span className="hint">{t('gm.scope')}</span>
                      <Select value={r.scope ?? ''} onChange={(e) => setRow(r.key, { scope: e.target.value as Mapping['scope'] })} aria-label={t('gm.scope')}>
                        <option value="">{t('gm.keep')}</option>
                        <option value="all">{t('gm.scope.all')}</option>
                        <option value="teams">{t('gm.scope.teams')}</option>
                      </Select>
                    </label>
                  </div>
                  <div className="gm-tools">
                    <span className="gm-order">{i + 1}</span>
                    <button type="button" className="icon-btn" onClick={() => move(i, -1)} disabled={i === 0} title={t('gm.up')} aria-label={t('gm.up')}>
                      <ArrowUp size={16} />
                    </button>
                    <button
                      type="button"
                      className="icon-btn"
                      onClick={() => move(i, 1)}
                      disabled={i === draft.rows.length - 1}
                      title={t('gm.down')}
                      aria-label={t('gm.down')}
                    >
                      <ArrowDown size={16} />
                    </button>
                    <button
                      type="button"
                      className="icon-btn"
                      onClick={() => setDraft({ ...draft, rows: draft.rows.filter((x) => x.key !== r.key) })}
                      title={t('gm.remove')}
                      aria-label={t('gm.remove')}
                    >
                      <Trash2 size={16} />
                    </button>
                  </div>
                  {problem && r.group.trim() !== '' && <p className="hint gm-error gm-problem">{t(problem)}</p>}
                </li>
              )
            })}
          </ol>
        )}
        {canEdit && anyDirectory && (
          <div>
            <Button type="button" onClick={add}>
              <Plus size={15} aria-hidden /> {t('gm.add')}
            </Button>
          </div>
        )}
        <Switch
          checked={!draft.sync_off}
          onChange={(v) => setDraft({ ...draft, sync_off: !v })}
          label={t('gm.sync')}
          hint={t('gm.sync.hint')}
          aside={
            !draft.sync_off && (
              <div className="gm-every">
                <span className="hint">{t('gm.sync.every')}</span>
                <Stepper
                  value={draft.sync_minutes}
                  min={5}
                  max={10080}
                  suffix={t('gm.sync.minutes')}
                  label={t('gm.sync.every')}
                  onChange={(v) => setDraft({ ...draft, sync_minutes: v })}
                />
              </div>
            )
          }
        />
        {(entraEnabled || draft.rows.some((r) => r.source === 'entra')) && <p className="hint">{t('gm.entra.permission')}</p>}
      </fieldset>
    </ProfileCard>
  )
}
