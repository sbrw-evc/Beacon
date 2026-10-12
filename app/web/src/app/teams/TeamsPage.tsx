import { useCallback, useEffect, useMemo, useState } from 'react'
import { Plus, Search, Users } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { api } from '../../api'
import { Avatar } from '../../Avatar'
import { useT } from '../../i18n'
import { Banner, Button, Input } from '../../ui'
import { useSession } from '../session'
import { Tree } from '../org/TreeView'
import { TreeIndex } from '../org/treeIndex'
import { loadRefs, type UserRef } from '../org/types'
import { useAction } from '../profile/useAction'
import type { Team } from './team'
import { CreateTeamDialog, DeleteTeamDialog } from './TeamDialogs'
import { TeamDetail } from './TeamDetail'
import { strings } from './strings'
import '../org/org.css'
import './teams.css'
import { SetupGuide } from '../guide/SetupGuide'

export function TeamsPage() {
  const t = useT(strings)
  const { can } = useSession()
  const editable = can('teams:edit')
  const loader = useAction(strings)
  const [teams, setTeams] = useState<Team[] | null>(null)
  const [users, setUsers] = useState<UserRef[]>([])
  const [selected, setSelected] = useState('')
  const [query, setQuery] = useState('')
  const [creating, setCreating] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<Team | null>(null)

  const reload = useCallback(async () => {
    const [list, refs] = await Promise.all([api<Team[]>('GET', '/api/teams'), loadRefs()])
    setTeams(list)
    setUsers(refs.users)
    setSelected((cur) => (list.some((x) => x.id === cur) ? cur : (new TreeIndex(list).roots()[0]?.id ?? '')))
  }, [])

  const { run: load } = loader
  useEffect(() => {
    void load(reload)
  }, [load, reload])

  const index = useMemo(() => new TreeIndex(teams ?? []), [teams])
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase()
    return q ? index.visible((x) => x.name.toLowerCase().includes(q) || (x.lead?.name.toLowerCase().includes(q) ?? false)) : undefined
  }, [index, query])

  if (!teams) {
    return loader.error ? (
      <Banner kind="error" title={loader.error.message}>
        {loader.error.detail}
      </Banner>
    ) : null
  }

  const team = index.byId.get(selected)

  return (
    <>
      <SetupGuide page="teams" actions={{ create: () => setCreating('') }} />
      <div className="org-split">
        <aside className="card org-side" aria-label={t('teams.tree')}>
          <div className="org-side-head">
            <span className="section-title">{t('teams.tree')}</span>
            {editable && (
              <Button variant="ghost" onClick={() => setCreating('')}>
                <Plus size={16} aria-hidden />
                {t('teams.new')}
              </Button>
            )}
          </div>
          {teams.length > 0 && (
            <div className="org-search">
              <Search size={16} aria-hidden />
              <Input value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t('teams.search')} aria-label={t('teams.search')} />
            </div>
          )}
          <Tree
            index={index}
            selected={selected}
            onSelect={setSelected}
            visible={visible}
            empty={teams.length === 0 ? t('teams.none') : t('org.nothing')}
            label={(x) => x.name}
            meta={(x) => (
              <>
                {x.lead && (
                  <span title={t('teams.lead.of', { name: x.lead.name })}>
                    <Avatar user={x.lead} size={18} />
                  </span>
                )}
                <span className="org-count" title={t('teams.members')}>
                  <Users size={12} aria-hidden />
                  {x.member_count}
                </span>
              </>
            )}
          />
        </aside>
        <section className="card org-detail" aria-live="polite">
          <AnimatePresence mode="wait" initial={false}>
            <motion.div
              key={team?.id ?? 'none'}
              initial={{ opacity: 0, x: 12 }}
              animate={{ opacity: 1, x: 0 }}
              exit={{ opacity: 0, x: -8 }}
              transition={{ duration: 0.18, ease: [0.22, 1, 0.36, 1] }}
            >
              {team ? (
                <TeamDetail
                  team={team}
                  index={index}
                  users={users}
                  editable={editable}
                  onChanged={reload}
                  onSelect={setSelected}
                  onAddChild={() => setCreating(team.id)}
                  onDelete={() => setDeleting(team)}
                />
              ) : (
                <div className="stack teams-empty">
                  <p className="muted">{t(teams.length === 0 ? (editable ? 'teams.none.edit' : 'teams.none') : 'teams.pick')}</p>
                  {editable && teams.length === 0 && (
                    <div>
                      <Button variant="primary" onClick={() => setCreating('')}>
                        <Plus size={16} aria-hidden />
                        {t('teams.new')}
                      </Button>
                    </div>
                  )}
                </div>
              )}
            </motion.div>
          </AnimatePresence>
        </section>
        <CreateTeamDialog
          parent={creating}
          index={index}
          users={users}
          onClose={() => setCreating(null)}
          onCreated={async (x) => {
            setCreating(null)
            await reload()
            setSelected(x.id)
          }}
        />
        <DeleteTeamDialog
          team={deleting}
          index={index}
          onClose={() => setDeleting(null)}
          onDeleted={async (x) => {
            setDeleting(null)
            setSelected(x.parent_id)
            await reload()
          }}
        />
      </div>
    </>
  )
}
