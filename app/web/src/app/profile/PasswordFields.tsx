import { PolicyChecklist } from '../../PolicyChecklist'
import { useT } from '../../i18n'
import { Field, Password } from '../../ui'
import { useSession } from '../session'
import { strings } from '../strings'
import type { PasswordChange } from './usePasswordChange'

export function PasswordFields({ change }: { change: PasswordChange }) {
  const t = useT(strings)
  const { user, policy } = useSession()
  const { draft, setDraft } = change
  return (
    <>
      <input type="text" name="username" autoComplete="username" value={user.username} readOnly hidden />
      <Field label={t('prefs.password.current')}>
        {(id) => <Password id={id} value={draft.current} onChange={(e) => setDraft({ ...draft, current: e.target.value })} autoComplete="current-password" />}
      </Field>
      <Field label={t('prefs.password.new')}>
        {(id) => <Password id={id} value={draft.next} onChange={(e) => setDraft({ ...draft, next: e.target.value })} autoComplete="new-password" />}
      </Field>
      <Field label={t('prefs.password.confirm')}>
        {(id) => <Password id={id} value={draft.confirm} onChange={(e) => setDraft({ ...draft, confirm: e.target.value })} autoComplete="new-password" />}
      </Field>
      <PolicyChecklist policy={policy} password={draft.next} username={user.username} confirm={draft.confirm} t={t} />
    </>
  )
}
