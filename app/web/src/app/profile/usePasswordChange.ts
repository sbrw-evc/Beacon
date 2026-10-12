import { useState } from 'react'
import { api } from '../../api'
import { checkPassword } from '../../policy'
import { useSession } from '../session'

const empty = { current: '', next: '', confirm: '' }

export type PasswordDraft = typeof empty

export type PasswordChange = {
  draft: PasswordDraft
  setDraft: (d: PasswordDraft) => void
  valid: boolean
  submit: () => Promise<void>
}

export function usePasswordChange(): PasswordChange {
  const { user, policy, refresh } = useSession()
  const [draft, setDraft] = useState(empty)
  const valid = draft.current !== '' && checkPassword(draft.next, user.username, policy).length === 0 && draft.next === draft.confirm

  const submit = async () => {
    await api('PUT', '/api/auth/me/password', { current_password: draft.current, new_password: draft.next })
    setDraft(empty)
    await refresh()
  }

  return { draft, setDraft, valid, submit }
}
