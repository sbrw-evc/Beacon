import { useMemo, useState, type FormEvent } from 'react'
import { api } from '../../api'
import { ErrorBanner, ErrorFlash } from '../../connections/ConnectionCard'
import { useAction, useResource } from '../../connections/useRequest'
import { useT } from '../../i18n'
import { Button, Field, Input, Modal, Select, Textarea } from '../../ui'
import { LinksEditor, MultiPicker, OptionSelect, TagInput, teamOptions, type Option } from './Pickers'
import { strings } from './strings'
import type { TeamTree } from './teams'
import { blankInput, CRITICALITIES, inputOf, STATUSES, type Service, type ServiceInput, type ServiceList } from './types'

type Props = { open: boolean; service: Service | null; tree: TeamTree; tags: string[]; onClose: () => void; onSaved: (s: Service) => void }

export function ServiceEditor(props: Props) {
  const t = useT(strings)
  const title = props.service ? t('svc.edit.title', { name: props.service.name }) : t('svc.new')
  return (
    <Modal open={props.open} title={title} onClose={props.onClose}>
      <EditorForm {...props} />
    </Modal>
  )
}

export function blockedDependencies(all: Service[], self: string) {
  const users = new Map<string, string[]>()
  for (const s of all) for (const d of s.depends_on) users.set(d, [...(users.get(d) ?? []), s.id])
  const out = new Set<string>()
  const queue = self ? [self] : []
  while (queue.length) {
    const id = queue.shift() as string
    if (out.has(id)) continue
    out.add(id)
    queue.push(...(users.get(id) ?? []))
  }
  return out
}

function EditorForm({ service, tree, tags, onClose, onSaved }: Props) {
  const t = useT(strings)
  const [draft, setDraft] = useState<ServiceInput>(() => (service ? inputOf(service) : blankInput()))
  const all = useResource<ServiceList>('/api/services', 0)
  const saver = useAction()
  const set = (patch: Partial<ServiceInput>) => setDraft((d) => ({ ...d, ...patch }))
  const teams = useMemo(() => teamOptions(tree.options()), [tree])
  const deleted = t('svc.team.deleted')

  const services = all.data?.services ?? []
  const blocked = blockedDependencies(services, service?.id ?? '')
  const depOptions: Option[] = services.filter((s) => !blocked.has(s.id)).map((s) => ({ id: s.id, label: s.name }))
  const serviceName = (id: string) => services.find((s) => s.id === id)?.name ?? service?.dependencies.find((d) => d.id === id)?.name
  const teamChip = (id: string) => {
    const label = tree.has(id) ? tree.path(id).join(' / ') : deleted
    return { key: id, label, title: label, muted: !tree.has(id) }
  }
  const ownerOptions = teams.filter((o) => !draft.team_ids.includes(o.id))
  const ownerMissing = draft.owner_team_id !== '' && !tree.has(draft.owner_team_id)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    void saver.run(async () => {
      const body = { ...draft, links: draft.links.filter((l) => l.title.trim() || l.url.trim()) }
      const saved = service ? await api<Service>('PUT', `/api/services/${service.id}`, body) : await api<Service>('POST', '/api/services', body)
      onSaved(saved)
    })
  }

  return (
    <form className="stack" onSubmit={submit} noValidate>
      <Field label={t('svc.field.name')}>
        {(id) => <Input id={id} value={draft.name} maxLength={120} required onChange={(e) => set({ name: e.target.value })} />}
      </Field>
      <Field label={t('svc.field.description')}>
        {(id) => <Textarea id={id} value={draft.description} maxLength={4000} rows={3} onChange={(e) => set({ description: e.target.value })} />}
      </Field>
      <div className="grid-2 svc-grid">
        <Field label={t('svc.field.criticality')}>
          {(id) => (
            <Select id={id} value={draft.criticality} onChange={(e) => set({ criticality: e.target.value as ServiceInput['criticality'] })}>
              {CRITICALITIES.map((c) => (
                <option key={c} value={c}>
                  {t(`svc.crit.${c}`)}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <Field label={t('svc.field.status')}>
          {(id) => (
            <Select id={id} value={draft.status} onChange={(e) => set({ status: e.target.value as ServiceInput['status'] })}>
              {STATUSES.map((s) => (
                <option key={s} value={s}>
                  {t(`svc.status.${s}`)}
                </option>
              ))}
            </Select>
          )}
        </Field>
      </div>
      <Field label={t('svc.field.owner')} hint={t('svc.field.owner.hint')}>
        {(id) => (
          <OptionSelect
            id={id}
            value={ownerMissing ? '' : draft.owner_team_id}
            options={ownerOptions}
            placeholder={ownerMissing ? deleted : t('svc.field.owner.pick')}
            onChange={(v) => set({ owner_team_id: v })}
          />
        )}
      </Field>
      <Field label={t('svc.field.teams')}>
        {(id) => (
          <MultiPicker
            id={id}
            selected={draft.team_ids}
            options={teams.filter((o) => o.id !== draft.owner_team_id)}
            labelOf={teamChip}
            placeholder={t('svc.field.teams.add')}
            onChange={(ids) => set({ team_ids: ids })}
          />
        )}
      </Field>
      <Field label={t('svc.field.tags')} hint={t('svc.field.tags.hint')}>
        {(id) => <TagInput id={id} value={draft.tags} suggestions={tags} onChange={(v) => set({ tags: v })} />}
      </Field>
      <Field label={t('svc.field.links')} hint={t('svc.field.links.hint')}>
        {() => <LinksEditor value={draft.links} onChange={(links) => set({ links })} />}
      </Field>
      <Field label={t('svc.field.dependencies')} hint={t('svc.field.dependencies.hint')}>
        {(id) => (
          <MultiPicker
            id={id}
            selected={draft.depends_on}
            options={depOptions}
            labelOf={(x) => ({ key: x, label: serviceName(x) ?? x, title: serviceName(x) ?? x })}
            placeholder={t('svc.field.dependencies.add')}
            onChange={(ids) => set({ depends_on: ids })}
          />
        )}
      </Field>
      <ErrorBanner error={all.error} strings={strings} />
      <ErrorFlash error={saver.error} strings={strings} />
      <div className="modal-foot">
        <Button type="button" variant="ghost" onClick={onClose}>
          {t('svc.cancel')}
        </Button>
        <Button type="submit" variant="primary" busy={saver.busy} disabled={!draft.name.trim() || !draft.owner_team_id || ownerMissing}>
          {t('svc.save')}
        </Button>
      </div>
    </form>
  )
}
