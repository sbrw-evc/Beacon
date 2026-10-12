import { useEffect, useState } from 'react'
import { api } from '../../api'
import { useT } from '../../i18n'
import { Button, Field, Input, Modal, Select, Textarea } from '../../ui'
import { roleLabel } from '../types'
import { useAction } from '../profile/useAction'
import type { Role } from './permissions'
import { strings } from './strings'
import { Flash } from '../../notify'

export function CreateRoleDialog({
  open,
  roles,
  source,
  onClose,
  onCreated,
}: {
  open: boolean
  roles: Role[]
  source: string
  onClose: () => void
  onCreated: (r: Role) => void
}) {
  const t = useT(strings)
  const action = useAction(strings)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [copy, setCopy] = useState('')
  const { reset } = action
  useEffect(() => {
    if (!open) return
    const from = roles.find((r) => r.id === source)
    setName(from ? t('roles.copyName', { name: roleLabel(t, from.id, from.name) }) : '')
    setDescription(from?.description ?? '')
    setCopy(source)
    reset()
  }, [open, source, roles, t, reset])
  const create = () =>
    action.run(async () => {
      const from = roles.find((r) => r.id === copy)
      const r = await api<Role>('POST', '/api/roles', { name, description, permissions: from?.permissions ?? [] })
      onCreated(r)
    })
  return (
    <Modal
      open={open}
      title={t(source ? 'roles.duplicate.title' : 'roles.create.title')}
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t('org.cancel')}
          </Button>
          <Button variant="primary" busy={action.busy} disabled={!name.trim()} onClick={create}>
            {t('roles.create')}
          </Button>
        </>
      }
    >
      <Field label={t('roles.name')}>{(id) => <Input id={id} value={name} maxLength={80} onChange={(e) => setName(e.target.value)} />}</Field>
      <Field label={t('roles.description')} optional={t('roles.optional')}>
        {(id) => <Textarea id={id} value={description} rows={3} onChange={(e) => setDescription(e.target.value)} />}
      </Field>
      <Field label={t('roles.copyFrom')} hint={t('roles.copyFrom.hint')}>
        {(id) => (
          <Select id={id} value={copy} onChange={(e) => setCopy(e.target.value)}>
            <option value="">{t('roles.copyFrom.none')}</option>
            {roles.map((r) => (
              <option key={r.id} value={r.id}>
                {roleLabel(t, r.id, r.name)}
              </option>
            ))}
          </Select>
        )}
      </Field>
      {action.error && (
        <Flash kind="error" title={action.error.message}>
          {action.error.detail}
        </Flash>
      )}
    </Modal>
  )
}

export function DeleteRoleDialog({
  role,
  roles,
  onClose,
  onDeleted,
}: {
  role: Role | null
  roles: Role[]
  onClose: () => void
  onDeleted: (id: string, reassignTo: string) => void
}) {
  const t = useT(strings)
  const action = useAction(strings)
  const [target, setTarget] = useState('user')
  const { reset } = action
  useEffect(() => {
    if (!role) return
    setTarget('user')
    reset()
  }, [role, reset])
  const others = roles.filter((r) => r.id !== role?.id)
  const remove = () =>
    action.run(async () => {
      if (!role) return
      const query = role.member_count > 0 ? `?reassign_to=${encodeURIComponent(target)}` : ''
      await api('DELETE', `/api/roles/${encodeURIComponent(role.id)}${query}`)
      onDeleted(role.id, role.member_count > 0 ? target : '')
    })
  return (
    <Modal
      open={role !== null}
      title={t('roles.delete.title')}
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t('org.cancel')}
          </Button>
          <Button variant="primary" busy={action.busy} onClick={remove}>
            {t('roles.delete')}
          </Button>
        </>
      }
    >
      {role && (
        <>
          <p>{t('roles.delete.text', { name: role.name })}</p>
          {role.member_count > 0 ? (
            <Field label={t('roles.delete.reassign', { n: role.member_count })} hint={t('roles.delete.reassign.hint')}>
              {(id) => (
                <Select id={id} value={target} onChange={(e) => setTarget(e.target.value)}>
                  {others.map((r) => (
                    <option key={r.id} value={r.id}>
                      {roleLabel(t, r.id, r.name)}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
          ) : (
            <p className="muted">{t('roles.delete.empty')}</p>
          )}
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
