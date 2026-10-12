import { useMemo, useState, type FormEvent } from 'react'
import { api } from '../../api'
import { ErrorBanner, ErrorFlash } from '../../connections/ConnectionCard'
import { useAction, useResource } from '../../connections/useRequest'
import { useT } from '../../i18n'
import { Banner, Button, Field, Input, Modal, Select, Switch, Textarea } from '../../ui'
import { MultiPicker, TagInput, type Option } from '../services/Pickers'
import { strings } from './strings'
import { blankInput, inputOf, KINDS, registrable, STATUSES, type CI, type CIInput, type Kind, type UserRef } from './types'

type Props = { open: boolean; ci: CI | null; tags: string[]; onClose: () => void; onSaved: (ci: CI) => void }

export function CIEditor(props: Props) {
  const t = useT(strings)
  return (
    <Modal open={props.open} title={props.ci ? t('ci.edit.title', { name: props.ci.name }) : t('ci.new')} onClose={props.onClose}>
      <EditorForm {...props} />
    </Modal>
  )
}

function splitIPs(text: string) {
  return text
    .split(/[\s,;]+/)
    .map((x) => x.trim())
    .filter(Boolean)
}

function EditorForm({ ci, tags, onClose, onSaved }: Props) {
  const t = useT(strings)
  const [draft, setDraft] = useState<CIInput>(() => (ci ? inputOf(ci) : blankInput()))
  const [ips, setIps] = useState(() => draft.ips.join(', '))
  const refs = useResource<{ users: UserRef[] }>('/api/refs', 0)
  const saver = useAction()
  const set = (patch: Partial<CIInput>) => setDraft((d) => ({ ...d, ...patch }))

  const users = useMemo(() => (refs.data?.users ?? []).filter((u) => !u.disabled), [refs.data])
  const options: Option[] = users.map((u) => ({ id: u.id, label: u.name && u.name !== u.username ? `${u.name} (${u.username})` : u.username }))
  const ownerLabel = (id: string) => {
    const u = users.find((x) => x.id === id) ?? null
    const o = ci?.owners.find((x) => x.id === id)
    const label = u?.name || u?.username || o?.name || o?.username || t('ci.owner.deleted')
    return { key: id, label, title: label, muted: !u }
  }
  const linked = !!ci?.netbox
  const canRegister = !linked && registrable(draft.kind)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    void saver.run(async () => {
      const body = { ...draft, ips: splitIPs(ips), register: draft.register && canRegister }
      const saved = ci ? await api<CI>('PUT', `/api/cis/${ci.id}`, body) : await api<CI>('POST', '/api/cis', body)
      onSaved(saved)
    })
  }

  return (
    <form className="stack" onSubmit={submit} noValidate>
      <Field label={t('ci.field.name')}>
        {(id) => <Input id={id} value={draft.name} maxLength={200} required onChange={(e) => set({ name: e.target.value })} />}
      </Field>
      <div className="grid-2 svc-grid">
        <Field label={t('ci.field.kind')}>
          {(id) => (
            <Select id={id} value={draft.kind} disabled={linked} onChange={(e) => set({ kind: e.target.value as Kind })}>
              {KINDS.map((k) => (
                <option key={k} value={k}>
                  {t(`ci.kind.${k}`)}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <Field label={t('ci.field.status')}>
          {(id) => (
            <Select id={id} value={draft.status} onChange={(e) => set({ status: e.target.value })}>
              {STATUSES.map((s) => (
                <option key={s} value={s}>
                  {t(`ci.status.${s}`)}
                </option>
              ))}
            </Select>
          )}
        </Field>
      </div>
      <Field label={t('ci.field.description')}>
        {(id) => <Textarea id={id} value={draft.description} maxLength={4000} rows={3} onChange={(e) => set({ description: e.target.value })} />}
      </Field>
      <Field label={t('ci.field.owners')} hint={t('ci.field.owners.hint')}>
        {(id) => (
          <MultiPicker
            id={id}
            selected={draft.owner_ids}
            options={options}
            labelOf={ownerLabel}
            placeholder={t('ci.field.owners.add')}
            onChange={(owner_ids) => set({ owner_ids })}
          />
        )}
      </Field>
      <Field label={t('ci.field.ips')} hint={t('ci.field.ips.hint')}>
        {(id) => <Input id={id} value={ips} spellCheck={false} placeholder="10.0.0.10, 2001:db8::10" onChange={(e) => setIps(e.target.value)} />}
      </Field>
      <Field label={t('ci.field.tags')}>{(id) => <TagInput id={id} value={draft.tags} suggestions={tags} onChange={(v) => set({ tags: v })} />}</Field>
      {!linked &&
        (canRegister ? (
          <Switch checked={draft.register} onChange={(register) => set({ register })} label={t('ci.field.register')} hint={t('ci.field.register.hint')} />
        ) : (
          <p className="hint">{t('ci.field.register.kind')}</p>
        ))}
      <ErrorBanner error={refs.error} strings={strings} />
      <ErrorFlash error={saver.error} strings={strings} />
      <div className="modal-foot">
        <Button type="button" variant="ghost" onClick={onClose}>
          {t('ci.cancel')}
        </Button>
        <Button type="submit" variant="primary" busy={saver.busy} disabled={!draft.name.trim()}>
          {t('ci.save')}
        </Button>
      </div>
    </form>
  )
}

export function DeleteDialog({ ci, onClose, onDeleted }: { ci: CI | null; onClose: () => void; onDeleted: (ci: CI) => void }) {
  const t = useT(strings)
  const remover = useAction()
  const close = () => {
    remover.clear()
    onClose()
  }
  const confirm = () =>
    ci &&
    void remover.run(async () => {
      await api('DELETE', `/api/cis/${ci.id}`)
      onDeleted(ci)
    })
  return (
    <Modal
      open={ci !== null}
      title={t('ci.delete.title')}
      onClose={close}
      footer={
        <>
          <Button variant="ghost" onClick={close}>
            {t('ci.cancel')}
          </Button>
          <Button variant="danger" busy={remover.busy} onClick={confirm}>
            {t('ci.delete.confirm')}
          </Button>
        </>
      }
    >
      {ci && (
        <>
          <p>{t('ci.delete.text', { name: ci.name })}</p>
          {ci.netbox && <Banner kind="warn" title={t('ci.delete.netbox', { kind: ci.netbox.kind, id: ci.netbox.id })} />}
          <ErrorFlash error={remover.error} strings={strings} />
        </>
      )}
    </Modal>
  )
}
