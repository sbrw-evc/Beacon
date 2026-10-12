import { useEffect, useState } from 'react'
import { api } from '../../api'
import { useT } from '../../i18n'
import { Button, Modal } from '../../ui'
import type { TreeIndex } from '../org/treeIndex'
import type { UserRef } from '../org/types'
import { useAction } from '../profile/useAction'
import { teamDraft, type Team } from './team'
import { TeamFields } from './TeamFields'
import { strings } from './strings'
import { Flash } from '../../notify'

export function CreateTeamDialog({
  parent,
  index,
  users,
  onClose,
  onCreated,
}: {
  parent: string | null
  index: TreeIndex<Team>
  users: UserRef[]
  onClose: () => void
  onCreated: (t: Team) => void
}) {
  const t = useT(strings)
  const action = useAction(strings)
  const [draft, setDraft] = useState(() => teamDraft())
  const { reset } = action
  useEffect(() => {
    if (parent === null) return
    setDraft(teamDraft(undefined, parent))
    reset()
  }, [parent, reset])
  const create = () => action.run(async () => onCreated(await api<Team>('POST', '/api/teams', draft)))
  return (
    <Modal
      open={parent !== null}
      title={t(parent ? 'teams.create.child' : 'teams.create.root')}
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t('org.cancel')}
          </Button>
          <Button variant="primary" busy={action.busy} disabled={!draft.name.trim()} onClick={create}>
            {t('teams.create')}
          </Button>
        </>
      }
    >
      <TeamFields draft={draft} onChange={setDraft} index={index} users={users} />
      {action.error && (
        <Flash kind="error" title={action.error.message}>
          {action.error.detail}
        </Flash>
      )}
    </Modal>
  )
}

export function DeleteTeamDialog({
  team,
  index,
  onClose,
  onDeleted,
}: {
  team: Team | null
  index: TreeIndex<Team>
  onClose: () => void
  onDeleted: (t: Team) => void
}) {
  const t = useT(strings)
  const action = useAction(strings)
  const { reset } = action
  useEffect(() => {
    if (team) reset()
  }, [team, reset])
  const remove = () =>
    action.run(async () => {
      if (!team) return
      await api('DELETE', `/api/teams/${encodeURIComponent(team.id)}`)
      onDeleted(team)
    })
  const parent = team ? index.byId.get(team.parent_id) : undefined
  return (
    <Modal
      open={team !== null}
      title={t('teams.delete.title')}
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t('org.cancel')}
          </Button>
          <Button variant="primary" busy={action.busy} onClick={remove}>
            {t('teams.delete')}
          </Button>
        </>
      }
    >
      {team && (
        <>
          <p>{t('teams.delete.text', { name: index.path(team.id) })}</p>
          <ul className="teams-consequences">
            <li>
              {team.child_count === 0
                ? t('teams.delete.noChildren')
                : parent
                  ? t('teams.delete.children', { n: team.child_count, parent: index.path(parent.id) })
                  : t('teams.delete.childrenRoot', { n: team.child_count })}
            </li>
            <li>{team.member_count === 0 ? t('teams.delete.noMembers') : t('teams.delete.members', { n: team.member_count })}</li>
            <li>{t('teams.delete.services')}</li>
          </ul>
        </>
      )}
      {action.error && (
        <Flash kind="error" title={action.error.message}>
          {action.error.detail}
        </Flash>
      )}
    </Modal>
  )
}
