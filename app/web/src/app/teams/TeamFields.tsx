import { useT } from '../../i18n'
import { Field, Input, Select, Textarea } from '../../ui'
import type { TreeIndex } from '../org/treeIndex'
import type { UserRef } from '../org/types'
import { MAX_DEPTH, type Team, type TeamDraft } from './team'
import { strings } from './strings'

export function ParentSelect({
  id,
  index,
  self,
  value,
  disabled,
  onChange,
}: {
  id: string
  index: TreeIndex<Team>
  self?: string
  value: string
  disabled?: boolean
  onChange: (v: string) => void
}) {
  const t = useT(strings)
  const blocked = self ? index.descendants(self).add(self) : new Set<string>()
  const height = self ? subtreeHeight(index, self) : 1
  return (
    <Select id={id} value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)}>
      <option value="">{t('teams.parent.none')}</option>
      {index
        .flat()
        .filter(({ item }) => !blocked.has(item.id))
        .map(({ item, depth }) => (
          <option key={item.id} value={item.id} disabled={depth + 1 + height > MAX_DEPTH}>
            {'  '.repeat(depth * 2) + item.name}
          </option>
        ))}
    </Select>
  )
}

function subtreeHeight(index: TreeIndex<Team>, id: string): number {
  return 1 + Math.max(0, ...index.children(id).map((c) => subtreeHeight(index, c.id)))
}

export function TeamFields({
  draft,
  onChange,
  index,
  users,
  self,
  disabled,
}: {
  draft: TeamDraft
  onChange: (d: TeamDraft) => void
  index: TreeIndex<Team>
  users: UserRef[]
  self?: string
  disabled?: boolean
}) {
  const t = useT(strings)
  return (
    <div className="grid-2 teams-fields">
      <Field label={t('teams.name')}>
        {(id) => <Input id={id} value={draft.name} maxLength={80} disabled={disabled} onChange={(e) => onChange({ ...draft, name: e.target.value })} />}
      </Field>
      <Field label={t('teams.parent')} hint={t('teams.parent.hint', { n: MAX_DEPTH })}>
        {(id) => (
          <ParentSelect id={id} index={index} self={self} value={draft.parent_id} disabled={disabled} onChange={(v) => onChange({ ...draft, parent_id: v })} />
        )}
      </Field>
      <Field label={t('teams.lead')} optional={t('teams.optional')}>
        {(id) => (
          <Select id={id} value={draft.lead_id} disabled={disabled} onChange={(e) => onChange({ ...draft, lead_id: e.target.value })}>
            <option value="">{t('teams.lead.none')}</option>
            {users.map((u) => (
              <option key={u.id} value={u.id}>
                {u.name === u.username ? u.name : `${u.name} (${u.username})`}
              </option>
            ))}
          </Select>
        )}
      </Field>
      <Field label={t('teams.email')} optional={t('teams.optional')} hint={t('teams.channel.hint')}>
        {(id) => (
          <Input id={id} type="email" value={draft.email} maxLength={200} disabled={disabled} onChange={(e) => onChange({ ...draft, email: e.target.value })} />
        )}
      </Field>
      <Field label={t('teams.telegram')} optional={t('teams.optional')} hint={t('teams.telegram.hint')}>
        {(id) => <Input id={id} value={draft.telegram} maxLength={64} disabled={disabled} onChange={(e) => onChange({ ...draft, telegram: e.target.value })} />}
      </Field>
      <Field label={t('teams.msteams')} optional={t('teams.optional')} hint={t('teams.msteams.hint')}>
        {(id) => (
          <Input id={id} type="url" value={draft.teams} maxLength={2048} spellCheck={false} placeholder="https://…" disabled={disabled} onChange={(e) => onChange({ ...draft, teams: e.target.value })} />
        )}
      </Field>
      <Field label={t('teams.zoom')} optional={t('teams.optional')} hint={t('teams.zoom.hint')}>
        {(id) => (
          <Input id={id} type="url" value={draft.zoom} maxLength={2048} spellCheck={false} placeholder="https://integrations.zoom.us/chat/webhooks/incomingwebhook/…" disabled={disabled} onChange={(e) => onChange({ ...draft, zoom: e.target.value })} />
        )}
      </Field>
      <Field label={t('teams.description')} optional={t('teams.optional')}>
        {(id) => (
          <Textarea id={id} value={draft.description} rows={2} disabled={disabled} onChange={(e) => onChange({ ...draft, description: e.target.value })} />
        )}
      </Field>
    </div>
  )
}
