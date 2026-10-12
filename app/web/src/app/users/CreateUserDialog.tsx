import { useState, type FormEvent } from 'react'
import { api } from '../../api'
import { useT } from '../../i18n'
import { Button, Field, Input, Modal } from '../../ui'
import { ProfileFieldsGrid } from '../profile/ProfileFieldsGrid'
import { useAction } from '../profile/useAction'
import { profileOf, type ProfileFields } from '../types'
import { AccessFields, type Access } from './AccessFields'
import type { ManagedUser, Refs } from './model'
import { freshPassword, NewPasswordFields, usePasswordValid, type NewPassword } from './NewPasswordFields'
import { strings } from './strings'
import { Flash } from '../../notify'

const FORM_ID = 'usr-create-form'
const USERNAME = /^[^\s\p{Cc}]{1,64}$/u

const blankProfile = () => profileOf({} as ProfileFields)

export function CreateUserDialog({ open, refs, onClose, onCreated }: { open: boolean; refs: Refs; onClose: () => void; onCreated: (u: ManagedUser) => void }) {
  const t = useT(strings)
  const action = useAction(strings)
  const [username, setUsername] = useState('')
  const [profile, setProfile] = useState<ProfileFields>(blankProfile)
  const [access, setAccess] = useState<Access>({ role_id: refs.new_user_role || 'user', team_ids: [] })
  const [password, setPassword] = useState<NewPassword>(freshPassword)
  const login = username.trim()
  const passwordOk = usePasswordValid(password, login)
  const valid = USERNAME.test(login) && passwordOk

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!valid) return
    void action.run(async () => {
      onCreated(await api<ManagedUser>('POST', '/api/users', { username: login, ...profile, ...access, ...password }))
    })
  }

  return (
    <Modal
      open={open}
      title={t('usr.create.title')}
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t('cancel')}
          </Button>
          <Button type="submit" form={FORM_ID} variant="primary" busy={action.busy} disabled={!valid}>
            {t('usr.create.submit')}
          </Button>
        </>
      }
    >
      <form id={FORM_ID} className="stack" onSubmit={submit} noValidate>
        <Field label={t('usr.field.username')} hint={t('usr.field.username.hint')}>
          {(id) => <Input id={id} value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" spellCheck={false} maxLength={64} />}
        </Field>
        <ProfileFieldsGrid value={profile} onChange={setProfile} editable />
        <AccessFields refs={refs} value={access} onChange={setAccess} />
        <NewPasswordFields value={password} onChange={setPassword} username={login} />
        {action.error && (
          <Flash kind="error" title={action.error.message}>
            {action.error.detail}
          </Flash>
        )}
      </form>
    </Modal>
  )
}
