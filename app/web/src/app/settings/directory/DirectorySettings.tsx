import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { api } from '../../../api'
import type { Check } from '../../../connections/CheckResult'
import { LdapCheckResult, LdapForm } from '../../../connections/LdapForm'
import { ldapCheckKey, ldapComplete, ldapConfig, ldapDraft, ldapTestBody, type LdapConfig, type LdapDraft, type LdapReport } from '../../../connections/ldap'
import { useLocale, useT } from '../../../i18n'
import { Banner, Button, formatDate, Switch } from '../../../ui'
import { ProfileCard } from '../../profile/ProfileCard'
import { SummaryCard } from '../../profile/SummaryCard'
import { useAction } from '../../profile/useAction'
import { useSession } from '../../session'
import { EntraSettings } from './EntraSettings'
import { DIRECTORY_CHANGED, GroupMappingSettings } from './GroupMappingSettings'
import { strings } from './strings'
import './directory.css'
import { Flash } from '../../../notify'

type DirectoryView = {
  config: Partial<LdapConfig>
  bind_password_set: boolean
  local_admins: number
  users: { total: number; admins: number; disabled: number; last_sign_in?: string }
}

function sameAccount(a: LdapDraft, b: LdapDraft) {
  return a.url.trim().toLowerCase() === b.url.trim().toLowerCase() && a.bind_dn.trim().toLowerCase() === b.bind_dn.trim().toLowerCase()
}

export function DirectorySettings() {
  const t = useT(strings)
  const { can } = useSession()
  const canEdit = can('settings.ldap:edit')
  const canTest = can('settings.ldap:test')
  const [view, setView] = useState<DirectoryView | null>(null)
  const [draft, setDraft] = useState<LdapDraft>(() => ldapDraft())
  const [check, setCheck] = useState<Check<LdapReport>>(null)
  const loader = useAction()
  const saver = useAction()
  const tester = useAction()

  const apply = useCallback((v: DirectoryView) => {
    setView(v)
    setDraft(ldapDraft(v.config))
  }, [])

  const { run: runLoad } = loader
  useEffect(() => {
    void runLoad(async () => apply(await api<DirectoryView>('GET', '/api/settings/ldap')))
  }, [runLoad, apply])

  if (!view) {
    return loader.error ? (
      <Banner kind="error" title={loader.error.message}>
        {loader.error.detail}
      </Banner>
    ) : null
  }

  const saved = ldapDraft(view.config)
  const reusable = view.bind_password_set && sameAccount(draft, saved)
  const passwordReady = draft.bind_password !== '' || reusable
  const dirty = draft.enabled !== saved.enabled || (draft.enabled && (ldapCheckKey(draft) !== ldapCheckKey(saved) || draft.bind_password !== ''))
  const turningOff = saved.enabled && !draft.enabled
  const blocked = turningOff && view.local_admins === 0
  const complete = ldapComplete(draft, reusable)

  const test = () =>
    tester.run(async () => {
      const key = ldapCheckKey(draft)
      const r = await api<LdapReport>('POST', '/api/settings/ldap/test', ldapTestBody(draft))
      setCheck({ key, ok: r.ok, result: r, error: r.error })
    })

  const save = () =>
    saver.run(async () => {
      const body = draft.enabled ? { config: ldapConfig(draft), bind_password: draft.bind_password } : { config: { enabled: false } }
      apply(await api<DirectoryView>('PUT', '/api/settings/ldap', body))
      setCheck(null)
      window.dispatchEvent(new Event(DIRECTORY_CHANGED))
      return t('dir.saved')
    })

  const passwordHint = draft.bind_password
    ? undefined
    : view.bind_password_set && !reusable
      ? t('dir.password.again')
      : reusable
        ? t('dir.password.keep')
        : undefined

  return (
    <>
      <SummaryCard
        title={t('dir.title')}
        text={t('dir.text')}
        rows={[
          [t('dir.state'), <span className={`pill pill-${saved.enabled ? 'ok' : 'off'}`}>{t(saved.enabled ? 'dir.state.on' : 'dir.state.off')}</span>],
          ...(saved.url
            ? ([
                [t('ld.kind'), t(`ld.kind.${saved.kind}`)],
                [t('dir.server'), <code key="u">{saved.url}</code>],
                [t('dir.tls'), saved.url.startsWith('ldaps://') ? 'LDAPS' : saved.start_tls ? 'StartTLS' : t('dir.tls.none')],
                [t('ld.baseDn'), saved.base_dn],
                [t('ld.adminGroup'), saved.admin_group_dn],
                [t('dir.password'), t(view.bind_password_set ? 'dir.password.set' : 'dir.password.unset')],
              ] as [string, ReactNode][])
            : []),
          [t('dir.users'), t('dir.users.value', { total: view.users.total, admins: view.users.admins, disabled: view.users.disabled })],
          [t('dir.lastSignIn'), <LastSignIn key="l" at={view.users.last_sign_in} />],
          [t('dir.localAdmins'), String(view.local_admins)],
        ]}
      />
      <ProfileCard
        title={t('dir.settings')}
        action={saver}
        onSubmit={save}
        footer={
          <div className="row directory-actions">
            {draft.enabled && <span className="hint">{t('dir.save.hint')}</span>}
            {draft.enabled && canTest && (
              <Button onClick={test} busy={tester.busy} disabled={!complete || !passwordReady}>
                {tester.busy ? t('dir.checking') : t('dir.check')}
              </Button>
            )}
            {canEdit && (
              <Button type="submit" variant="primary" busy={saver.busy} disabled={!dirty || blocked || (draft.enabled && (!complete || !passwordReady))}>
                {t('dir.save')}
              </Button>
            )}
          </div>
        }
      >
        <fieldset className="plain-fieldset" disabled={!canEdit}>
          <Switch checked={draft.enabled} onChange={(v) => setDraft({ ...draft, enabled: v })} label={t('dir.enable')} hint={t('dir.enable.hint')} />
        </fieldset>
        <AnimatePresence initial={false} mode="wait">
          {draft.enabled ? (
            <motion.div
              key="on"
              className="reveal-box"
              initial={{ opacity: 0, height: 0 }}
              animate={{ opacity: 1, height: 'auto' }}
              exit={{ opacity: 0, height: 0 }}
              transition={{ duration: 0.28, ease: [0.22, 1, 0.36, 1] }}
            >
              <div className="stack">
                <fieldset className="plain-fieldset" disabled={!canEdit && !canTest}>
                  <LdapForm value={draft} onChange={setDraft} passwordHint={passwordHint} />
                </fieldset>
                <LdapCheckResult check={check} draft={draft} />
                {tester.error && (
                  <Flash kind="error" title={tester.error.message}>
                    {tester.error.detail}
                  </Flash>
                )}
              </div>
            </motion.div>
          ) : (
            <motion.div key="off" className="stack" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} transition={{ duration: 0.2 }}>
              {blocked ? (
                <Banner kind="error" title={t('dir.off.noAdmin')} />
              ) : (
                <Banner kind="info" title={t('dir.off')}>
                  {turningOff && t('dir.off.sessions')}
                </Banner>
              )}
            </motion.div>
          )}
        </AnimatePresence>
      </ProfileCard>
      <EntraSettings />
      <GroupMappingSettings />
    </>
  )
}

function LastSignIn({ at }: { at?: string }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  return <span title={t('dir.lastSignIn.hint')}>{formatDate(at, locale, timezone) || '—'}</span>
}
