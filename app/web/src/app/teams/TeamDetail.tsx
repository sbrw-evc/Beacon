import { useMemo, useState } from 'react'
import { ChevronRight, FolderPlus, Trash2, UserMinus, UserPlus, Users } from 'lucide-react'
import { api } from '../../api'
import { useT } from '../../i18n'
import { Button } from '../../ui'
import { MemberList } from '../org/MemberList'
import { MemberPicker } from '../org/MemberPicker'
import type { TreeIndex } from '../org/treeIndex'
import { pickable, type UserRef } from '../org/types'
import { useAction } from '../profile/useAction'
import { MAX_DEPTH, teamChanges, teamDraft, type Team } from './team'
import { TeamFields } from './TeamFields'
import { strings } from './strings'
import { Flash } from '../../notify'

export function TeamDetail({
  team,
  index,
  users,
  editable,
  onChanged,
  onSelect,
  onAddChild,
  onDelete,
}: {
  team: Team
  index: TreeIndex<Team>
  users: UserRef[]
  editable: boolean
  onChanged: () => Promise<void>
  onSelect: (id: string) => void
  onAddChild: () => void
  onDelete: () => void
}) {
  const t = useT(strings)
  const saver = useAction(strings)
  const members = useAction(strings)
  const [draft, setDraft] = useState(() => teamDraft(team))
  const [picking, setPicking] = useState(false)
  const diff = teamChanges(team, draft)
  const dirty = Object.keys(diff).length > 0
  const known = useMemo(() => [...index.byId.values()].flatMap((x) => x.members), [index])
  const candidates = useMemo(() => pickable(users, known), [users, known])
  const ancestors = index.ancestors(team.id)
  const children = index.children(team.id)

  const save = () =>
    saver.run(async () => {
      await api<Team>('PUT', `/api/teams/${encodeURIComponent(team.id)}`, diff)
      await onChanged()
      return t('teams.saved')
    })

  const setMembers = (ids: string[]) =>
    members.run(async () => {
      await api<Team>('PUT', `/api/teams/${encodeURIComponent(team.id)}/members`, { user_ids: ids })
      await onChanged()
      setPicking(false)
    })
  const current = team.members.map((m) => m.id)

  return (
    <div className="stack">
      <div className="org-detail-head">
        <div className="stack teams-title">
          {ancestors.length > 0 && (
            <nav className="teams-crumbs" aria-label={t('teams.path')}>
              {ancestors.map((a) => (
                <span key={a.id} className="teams-crumb">
                  <button type="button" onClick={() => onSelect(a.id)}>
                    {a.name}
                  </button>
                  <ChevronRight size={13} aria-hidden />
                </span>
              ))}
            </nav>
          )}
          <h2>{team.name}</h2>
        </div>
        {editable && (
          <div className="org-detail-actions">
            <Button variant="ghost" onClick={onAddChild} disabled={team.depth >= MAX_DEPTH}>
              <FolderPlus size={16} aria-hidden />
              {t('teams.addChild')}
            </Button>
            <Button variant="ghost" onClick={onDelete}>
              <Trash2 size={16} aria-hidden />
              {t('teams.delete')}
            </Button>
          </div>
        )}
      </div>

      <TeamFields draft={draft} onChange={setDraft} index={index} users={users} self={team.id} disabled={!editable} />
      {saver.notice && <Flash kind="ok" title={saver.notice} />}
      {saver.error && (
        <Flash kind="error" title={saver.error.message}>
          {saver.error.detail}
        </Flash>
      )}
      {editable && dirty && (
        <div className="teams-save">
          <Button variant="ghost" onClick={() => setDraft(teamDraft(team))} disabled={saver.busy}>
            {t('teams.discard')}
          </Button>
          <Button variant="primary" busy={saver.busy} disabled={!draft.name.trim()} onClick={save}>
            {t('teams.save')}
          </Button>
        </div>
      )}

      <section className="stack teams-section" aria-label={t('teams.members')}>
        <div className="org-section-head">
          <span className="section-title">{t('teams.members.title', { n: team.member_count })}</span>
          {editable && (
            <Button onClick={() => setPicking(true)}>
              <UserPlus size={16} aria-hidden />
              {t('teams.members.add')}
            </Button>
          )}
        </div>
        {members.error && (
          <Flash kind="error" title={members.error.message}>
            {members.error.detail}
          </Flash>
        )}
        <MemberList
          members={team.members}
          empty={t('teams.members.empty')}
          note={(m) => (m.id === team.lead_id ? <strong>{t('teams.lead')}</strong> : m.title)}
          aside={
            editable
              ? (m) => (
                  <button
                    type="button"
                    className="icon-btn"
                    disabled={members.busy}
                    onClick={() => void setMembers(current.filter((id) => id !== m.id))}
                    aria-label={t('teams.members.remove', { name: m.name })}
                    title={t('teams.members.remove', { name: m.name })}
                  >
                    <UserMinus size={16} />
                  </button>
                )
              : undefined
          }
        />
      </section>

      <section className="stack teams-section" aria-label={t('teams.children')}>
        <span className="section-title">{t('teams.children.title', { n: children.length })}</span>
        {children.length === 0 ? (
          <p className="muted">{t('teams.children.empty')}</p>
        ) : (
          <ul className="teams-children">
            {children.map((c) => (
              <li key={c.id}>
                <button type="button" onClick={() => onSelect(c.id)}>
                  <span>{c.name}</span>
                  <span className="org-count">
                    <Users size={13} aria-hidden />
                    {c.member_count}
                    <ChevronRight size={14} aria-hidden />
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>

      <MemberPicker
        open={picking}
        title={t('teams.members.addTo', { team: team.name })}
        users={candidates}
        exclude={current}
        note={(u) => {
          const other = (u.team_ids ?? []).filter((id) => index.byId.has(id)).map((id) => index.path(id))
          return other.length ? t('teams.members.from', { team: other.join(', ') }) : undefined
        }}
        action={members}
        confirmLabel={t('teams.members.confirm')}
        onClose={() => setPicking(false)}
        onConfirm={(ids) => void setMembers([...current, ...ids])}
      />
    </div>
  )
}
