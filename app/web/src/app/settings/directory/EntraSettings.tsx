import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { api } from '../../../api'
import { Choices } from '../../../Choice'
import { CheckResult, type Check } from '../../../connections/CheckResult'
import { useLocale, useT } from '../../../i18n'
import { Banner, Button, Field, formatDate, Input, Password, Rows, Switch } from '../../../ui'
import { ProfileCard } from '../../profile/ProfileCard'
import { SummaryCard } from '../../profile/SummaryCard'
import { useAction } from '../../profile/useAction'
import { useSession } from '../../session'
import { DIRECTORY_CHANGED } from './GroupMappingSettings'
import { strings } from './strings'
import { Flash } from '../../../notify'

const CALLBACK = '/api/auth/entra/callback'
const CLOUDS = ['global', 'usgov', 'china'] as const
type Cloud = (typeof CLOUDS)[number]
const GUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

type EntraConfig = {
  enabled: boolean
  cloud: Cloud
  tenant_id: string
  client_id: string
  redirect_url: string
  admin_group_id: string
  user_group_id: string
}

type EntraDraft = EntraConfig & { client_secret: string }

type EntraView = {
  config: Partial<EntraConfig>
  client_secret_set: boolean
  local_admins: number
  users: { total: number; admins: number; disabled: number; last_sign_in?: string }
}

type EntraReport = { ok: boolean; error?: string; probe: { issuer?: string; tenant_id?: string; credentials: boolean } }

function defaultRedirect() {
  return window.location.origin + CALLBACK
}

function draftOf(c: Partial<EntraConfig> = {}): EntraDraft {
  return {
    enabled: c.enabled ?? false,
    cloud: c.cloud || 'global',
    tenant_id: c.tenant_id ?? '',
    client_id: c.client_id ?? '',
    redirect_url: c.redirect_url || defaultRedirect(),
    admin_group_id: c.admin_group_id ?? '',
    user_group_id: c.user_group_id ?? '',
    client_secret: '',
  }
}

function configOf(d: EntraDraft): EntraConfig {
  const { client_secret: _secret, ...c } = d
  return {
    ...c,
    tenant_id: c.tenant_id.trim(),
    client_id: c.client_id.trim(),
    redirect_url: c.redirect_url.trim(),
    admin_group_id: c.admin_group_id.trim(),
    user_group_id: c.user_group_id.trim(),
  }
}

function checkKey(d: EntraDraft) {
  return JSON.stringify({ ...configOf(d), enabled: true, client_secret: d.client_secret })
}

function sameApp(a: EntraDraft, b: EntraDraft) {
  return (
    a.cloud === b.cloud &&
    a.tenant_id.trim().toLowerCase() === b.tenant_id.trim().toLowerCase() &&
    a.client_id.trim().toLowerCase() === b.client_id.trim().toLowerCase()
  )
}

function complete(d: EntraDraft) {
  const groups = [d.admin_group_id, d.user_group_id].map((g) => g.trim()).filter(Boolean)
  return d.tenant_id.trim() !== '' && GUID.test(d.client_id.trim()) && groups.every((g) => GUID.test(g)) && d.redirect_url.trim().endsWith(CALLBACK)
}

export function EntraSettings() {
  const t = useT(strings)
  const { can } = useSession()
  const canEdit = can('settings.ldap:edit')
  const canTest = can('settings.ldap:test')
  const [view, setView] = useState<EntraView | null>(null)
  const [draft, setDraft] = useState<EntraDraft>(() => draftOf())
  const [check, setCheck] = useState<Check<EntraReport>>(null)
  const loader = useAction()
  const saver = useAction()
  const tester = useAction()

  const apply = useCallback((v: EntraView) => {
    setView(v)
    setDraft(draftOf(v.config))
  }, [])

  const { run: runLoad } = loader
  useEffect(() => {
    void runLoad(async () => apply(await api<EntraView>('GET', '/api/settings/entra')))
  }, [runLoad, apply])

  if (!view) {
    return loader.error ? (
      <Banner kind="error" title={loader.error.message}>
        {loader.error.detail}
      </Banner>
    ) : null
  }

  const saved = draftOf(view.config)
  const reusable = view.client_secret_set && sameApp(draft, saved)
  const secretReady = draft.client_secret !== '' || reusable
  const dirty =
    draft.enabled !== saved.enabled || (draft.enabled && (checkKey({ ...draft, client_secret: '' }) !== checkKey(saved) || draft.client_secret !== ''))
  const turningOff = saved.enabled && !draft.enabled
  const blocked = turningOff && view.local_admins === 0
  const ready = complete(draft)
  const set = (patch: Partial<EntraDraft>) => setDraft({ ...draft, ...patch })

  const test = () =>
    tester.run(async () => {
      const key = checkKey(draft)
      const r = await api<EntraReport>('POST', '/api/settings/entra/test', { config: configOf(draft), client_secret: draft.client_secret })
      setCheck({ key, ok: r.ok, result: r, error: r.error })
    })

  const save = () =>
    saver.run(async () => {
      const body = draft.enabled ? { config: configOf(draft), client_secret: draft.client_secret } : { config: { enabled: false } }
      apply(await api<EntraView>('PUT', '/api/settings/entra', body))
      setCheck(null)
      window.dispatchEvent(new Event(DIRECTORY_CHANGED))
      return t('en.saved')
    })

  const secretHint = draft.client_secret
    ? undefined
    : view.client_secret_set && !reusable
      ? t('en.secret.again')
      : reusable
        ? t('en.secret.keep')
        : t('en.secret.hint')

  return (
    <>
      <SummaryCard
        title={t('en.title')}
        text={t('en.text')}
        rows={[
          [t('en.state'), <span className={`pill pill-${saved.enabled ? 'ok' : 'off'}`}>{t(saved.enabled ? 'dir.state.on' : 'dir.state.off')}</span>],
          ...(saved.tenant_id
            ? ([
                [t('en.tenant'), <code key="t">{saved.tenant_id}</code>],
                [t('en.client'), <code key="c">{saved.client_id}</code>],
                [t('en.adminGroup'), saved.admin_group_id && <code key="a">{saved.admin_group_id}</code>],
                [t('en.userGroup'), saved.user_group_id ? <code key="u">{saved.user_group_id}</code> : t('en.userGroup.any')],
                [t('en.secret'), t(view.client_secret_set ? 'dir.password.set' : 'dir.password.unset')],
              ] as [string, ReactNode][])
            : []),
          [t('en.users'), t('dir.users.value', { total: view.users.total, admins: view.users.admins, disabled: view.users.disabled })],
          [t('en.lastSignIn'), <LastSignIn key="l" at={view.users.last_sign_in} />],
        ]}
      />
      <ProfileCard
        title={t('en.settings')}
        action={saver}
        onSubmit={save}
        footer={
          <div className="row directory-actions">
            {draft.enabled && <span className="hint">{t('en.save.hint')}</span>}
            {draft.enabled && canTest && (
              <Button onClick={test} busy={tester.busy} disabled={!ready || !secretReady}>
                {tester.busy ? t('dir.checking') : t('dir.check')}
              </Button>
            )}
            {canEdit && (
              <Button type="submit" variant="primary" busy={saver.busy} disabled={!dirty || blocked || (draft.enabled && (!ready || !secretReady))}>
                {t('dir.save')}
              </Button>
            )}
          </div>
        }
      >
        <fieldset className="plain-fieldset" disabled={!canEdit}>
          <Switch checked={draft.enabled} onChange={(v) => set({ enabled: v })} label={t('en.enable')} hint={t('en.enable.hint')} />
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
                <fieldset className="plain-fieldset stack" disabled={!canEdit && !canTest}>
                  <p className="hint">{t('en.register')}</p>
                  <Field label={t('en.redirect')} hint={t('en.redirect.hint')}>
                    {(id) => <Input id={id} value={draft.redirect_url} onChange={(e) => set({ redirect_url: e.target.value })} spellCheck={false} />}
                  </Field>
                  <Field label={t('en.cloud')}>
                    {() => (
                      <Choices
                        label={t('en.cloud')}
                        value={draft.cloud}
                        onChange={(c) => set({ cloud: c })}
                        options={CLOUDS.map((c) => ({ value: c, title: t(`en.cloud.${c}`) }))}
                      />
                    )}
                  </Field>
                  <div className="grid-2">
                    <Field label={t('en.tenant')} hint={t('en.tenant.hint')}>
                      {(id) => (
                        <Input id={id} value={draft.tenant_id} onChange={(e) => set({ tenant_id: e.target.value })} spellCheck={false} autoComplete="off" />
                      )}
                    </Field>
                    <Field label={t('en.client')} hint={t('en.client.hint')}>
                      {(id) => (
                        <Input id={id} value={draft.client_id} onChange={(e) => set({ client_id: e.target.value })} spellCheck={false} autoComplete="off" />
                      )}
                    </Field>
                  </div>
                  <Field label={t('en.secret')} hint={secretHint}>
                    {(id) => (
                      <Password id={id} value={draft.client_secret} onChange={(e) => set({ client_secret: e.target.value })} autoComplete="new-password" />
                    )}
                  </Field>
                  <div className="grid-2">
                    <Field label={t('en.adminGroup')} hint={t('en.adminGroup.hint')} optional={t('ld.optional')}>
                      {(id) => (
                        <Input
                          id={id}
                          value={draft.admin_group_id}
                          onChange={(e) => set({ admin_group_id: e.target.value })}
                          spellCheck={false}
                          placeholder="00000000-0000-0000-0000-000000000000"
                        />
                      )}
                    </Field>
                    <Field label={t('en.userGroup')} hint={t('en.userGroup.hint')} optional={t('ld.optional')}>
                      {(id) => (
                        <Input
                          id={id}
                          value={draft.user_group_id}
                          onChange={(e) => set({ user_group_id: e.target.value })}
                          spellCheck={false}
                          placeholder="00000000-0000-0000-0000-000000000000"
                        />
                      )}
                    </Field>
                  </div>
                </fieldset>
                <CheckResult
                  check={check}
                  currentKey={checkKey(draft)}
                  failTitle={t('en.fail')}
                  ok={(r) => (
                    <Flash kind="ok" title={t('en.ok')} trigger={r}>
                      <p>{t('en.ok.text')}</p>
                      <Rows rows={[[t('en.issuer'), r.probe.issuer && <code key="i">{r.probe.issuer}</code>]]} />
                    </Flash>
                  )}
                />
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
                <Banner kind="error" title={t('en.off.noAdmin')} />
              ) : (
                <Banner kind="info" title={t('en.off')}>
                  {turningOff && t('en.off.sessions')}
                </Banner>
              )}
            </motion.div>
          )}
        </AnimatePresence>
      </ProfileCard>
    </>
  )
}

function LastSignIn({ at }: { at?: string }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  return <span title={t('dir.lastSignIn.hint')}>{formatDate(at, locale, timezone) || '—'}</span>
}
