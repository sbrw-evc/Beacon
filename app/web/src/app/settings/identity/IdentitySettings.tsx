import { useEffect, useState } from 'react'
import { AppWindow, Check, Copy, Fingerprint, KeyRound, Plus, Trash2 } from 'lucide-react'
import { api } from '../../../api'
import { ask } from '../../../confirm'
import { ErrorBanner } from '../../../connections/ConnectionCard'
import { useResource } from '../../../connections/useRequest'
import { useLocale, useT } from '../../../i18n'
import { Banner, Button, Field, FileInput, formatDate, Input, Modal, Password, Select, Switch, Textarea } from '../../../ui'
import { SetupGuide } from '../../guide/SetupGuide'
import { ProfileCard } from '../../profile/ProfileCard'
import { useAction } from '../../profile/useAction'
import { ProviderMark } from '../../SignIn'
import { useSession } from '../../session'
import type { Refs } from '../../users/model'
import { strings } from './strings'
import '../../connectors/connectors.css'
import './identity.css'

type PasskeySettings = { enabled: boolean; rp_id: string; effective_rp_id: string; users: number; keys: number }

type Provider = {
  enabled: boolean
  title: string
  issuer: string
  client_id: string
  team_id?: string
  key_id?: string
  allowed_domains: string[]
  existing_only: boolean
  scopes?: string
  groups_claim?: string
  admin_group?: string
}
type ProviderView = { kind: 'google' | 'apple' | 'oidc'; config: Provider; client_secret_set: boolean; private_key_set: boolean; redirect_uri: string; users: number }
type Providers = { public_url: string; providers: ProviderView[] }

export type Application = {
  id: string
  kind: string
  name: string
  url: string
  redirect_uri: string
  access: 'all' | 'roles' | 'groups'
  roles: string[]
  groups: string[]
  sync_passkeys: boolean
  client_id: string
  key_issued_at: string
  last_sync_at?: string
  last_sync_ip?: string
  last_sign_in_at?: string
  sign_ins: number
  disabled: boolean
}
type Connection = { application: Application & { people: number }; connection_key: string }

// IdentitySettings: Settings → Sign-in and Umbrella Identity. Passkeys, Google / Apple / OpenID
// Connect sign-in and the applications of the Umbrella family that sign people in through
// Umbrella (docs/identity-federation.md).
export function IdentitySettings() {
  const { can } = useSession()
  const editable = can('settings.identity:edit')
  const [connect, setConnect] = useState(false)
  return (
    <>
      <SetupGuide page="settings.identity" actions={{ connect: () => setConnect(true) }} />
      <div className="cards">
        <PasskeysCard editable={editable} />
        <ProvidersCard editable={editable} />
      </div>
      <ApplicationsCard editable={editable} connect={connect} setConnect={setConnect} />
    </>
  )
}

function PasskeysCard({ editable }: { editable: boolean }) {
  const t = useT(strings)
  const [view, setView] = useState<PasskeySettings | null>(null)
  const [draft, setDraft] = useState({ enabled: false, rp_id: '' })
  const action = useAction(strings)
  useEffect(() => {
    void action.run(async () => {
      const v = await api<PasskeySettings>('GET', '/api/settings/passkeys')
      setView(v)
      setDraft({ enabled: v.enabled, rp_id: v.rp_id })
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  const save = () =>
    action.run(async () => {
      if (view && draft.rp_id !== view.rp_id && view.keys > 0 && !(await ask({ text: t('id.passkeys.domain.hint', { host: view.effective_rp_id }), danger: true }))) return
      const v = await api<PasskeySettings>('PUT', '/api/settings/passkeys', draft)
      setView(v)
      return t('id.saved')
    })
  const dirty = view && (draft.enabled !== view.enabled || draft.rp_id !== view.rp_id)
  return (
    <ProfileCard
      title={t('id.passkeys')}
      badge={<Fingerprint size={18} className="id-badge" aria-hidden />}
      action={action}
      onSubmit={save}
      footer={
        editable && (
          <Button type="submit" variant="primary" busy={action.busy} disabled={!dirty}>
            {t('id.save')}
          </Button>
        )
      }
    >
      <p className="muted">{t('id.passkeys.text')}</p>
      <fieldset className="plain-fieldset stack" disabled={!editable}>
        <Switch checked={draft.enabled} onChange={(v) => setDraft({ ...draft, enabled: v })} label={t('id.passkeys.on')} />
        <Field label={t('id.passkeys.domain')} hint={t('id.passkeys.domain.hint', { host: view?.effective_rp_id || '—' })}>
          {(id) => <Input id={id} value={draft.rp_id} placeholder={view?.effective_rp_id} onChange={(e) => setDraft({ ...draft, rp_id: e.target.value })} />}
        </Field>
      </fieldset>
      {view && view.keys > 0 && <p className="hint">{t('id.passkeys.stats', { keys: view.keys, users: view.users })}</p>}
    </ProfileCard>
  )
}

function ProvidersCard({ editable }: { editable: boolean }) {
  const t = useT(strings)
  const [epoch, setEpoch] = useState(0)
  const data = useResource<Providers>('/api/settings/providers', epoch)
  const [open, setOpen] = useState<ProviderView | null>(null)
  return (
    <ProfileCard title={t('id.providers')} badge={<KeyRound size={18} className="id-badge" aria-hidden />}>
      <p className="muted">{t('id.providers.text')}</p>
      <ErrorBanner error={data.error} strings={strings} />
      {data.data && !data.data.public_url && <Banner kind="warn" title={t('id.provider.nopublic')} />}
      <ul className="id-providers">
        {data.data?.providers.map((p) => (
          <li key={p.kind}>
            <ProviderMark id={p.kind} />
            <div className="id-provider-name">
              <strong>{p.kind === 'oidc' && p.config.title ? p.config.title : t(`id.provider.${p.kind}`)}</strong>
              <span className="hint">
                {p.config.enabled ? t('id.provider.on') : t('id.provider.off')}
                {p.users > 0 && ` · ${t('id.provider.users', { n: p.users })}`}
              </span>
            </div>
            <span className={`pill ${p.config.enabled ? 'pill-ok' : 'pill-off'}`}>{p.config.enabled ? t('id.provider.on') : t('id.provider.off')}</span>
            <Button onClick={() => setOpen(p)}>{t('id.provider.edit')}</Button>
          </li>
        ))}
      </ul>
      {open && (
        <ProviderDialog
          view={open}
          editable={editable}
          onClose={() => setOpen(null)}
          onSaved={() => {
            setOpen(null)
            setEpoch((e) => e + 1)
          }}
        />
      )}
    </ProfileCard>
  )
}

const splitList = (s: string) =>
  s
    .split(/[\s,;]+/)
    .map((x) => x.trim())
    .filter(Boolean)

function ProviderDialog({ view, editable, onClose, onSaved }: { view: ProviderView; editable: boolean; onClose: () => void; onSaved: () => void }) {
  const t = useT(strings)
  const [cfg, setCfg] = useState<Provider>(view.config)
  const [domains, setDomains] = useState(view.config.allowed_domains.join(' '))
  const [secret, setSecret] = useState('')
  const [key, setKey] = useState('')
  const action = useAction(strings)
  const set = (p: Partial<Provider>) => setCfg({ ...cfg, ...p })
  const save = () =>
    action.run(async () => {
      await api('PUT', `/api/settings/providers/${view.kind}`, { config: { ...cfg, allowed_domains: splitList(domains) }, client_secret: secret, private_key: key })
      onSaved()
    })
  const apple = view.kind === 'apple'
  return (
    <Modal
      open
      title={t(`id.provider.${view.kind}`)}
      onClose={onClose}
      footer={
        editable && (
          <Button variant="primary" busy={action.busy} onClick={() => void save()}>
            {t('id.save')}
          </Button>
        )
      }
    >
      <fieldset className="plain-fieldset stack" disabled={!editable}>
        <Switch checked={cfg.enabled} onChange={(v) => set({ enabled: v })} label={t('id.provider.enabled')} />
        {view.kind === 'oidc' && (
          <>
            <Field label={t('id.provider.title')}>{(id) => <Input id={id} value={cfg.title} placeholder="Keycloak" onChange={(e) => set({ title: e.target.value })} />}</Field>
            <Field label={t('id.provider.issuer')} hint={t('id.provider.issuer.hint')}>
              {(id) => <Input id={id} value={cfg.issuer} placeholder="https://sso.example.com/realms/corp" onChange={(e) => set({ issuer: e.target.value })} />}
            </Field>
          </>
        )}
        <Field label={apple ? t('id.provider.client.apple') : t('id.provider.client')}>
          {(id) => <Input id={id} value={cfg.client_id} onChange={(e) => set({ client_id: e.target.value })} autoComplete="off" />}
        </Field>
        {apple ? (
          <>
            <div className="id-two">
              <Field label={t('id.provider.team')}>{(id) => <Input id={id} value={cfg.team_id ?? ''} onChange={(e) => set({ team_id: e.target.value })} />}</Field>
              <Field label={t('id.provider.key')}>{(id) => <Input id={id} value={cfg.key_id ?? ''} onChange={(e) => set({ key_id: e.target.value })} />}</Field>
            </div>
            <Field label={t('id.provider.pkey')} hint={view.private_key_set ? t('id.provider.pkey.set') : undefined}>
              {(id) => <FileInput id={id} accept=".p8,.pem" onFile={(f) => void f.text().then(setKey)} />}
            </Field>
          </>
        ) : (
          <Field label={t('id.provider.secret')} hint={view.client_secret_set ? t('id.provider.secret.set') : undefined}>
            {(id) => <Password id={id} value={secret} onChange={(e) => setSecret(e.target.value)} autoComplete="new-password" />}
          </Field>
        )}
        <Field label={t('id.provider.domains')} hint={t('id.provider.domains.hint')}>
          {(id) => <Input id={id} value={domains} placeholder="example.com" onChange={(e) => setDomains(e.target.value)} />}
        </Field>
        <Switch checked={cfg.existing_only} onChange={(v) => set({ existing_only: v })} label={t('id.provider.existing')} hint={t('id.provider.existing.hint')} />
        {view.kind === 'oidc' && (
          <>
            <Field label={t('id.provider.scopes')}>{(id) => <Input id={id} value={cfg.scopes ?? ''} placeholder="groups" onChange={(e) => set({ scopes: e.target.value })} />}</Field>
            <div className="id-two">
              <Field label={t('id.provider.groups')} hint={t('id.provider.groups.hint')}>
                {(id) => <Input id={id} value={cfg.groups_claim ?? ''} placeholder="groups" onChange={(e) => set({ groups_claim: e.target.value })} />}
              </Field>
              <Field label={t('id.provider.admin')}>{(id) => <Input id={id} value={cfg.admin_group ?? ''} onChange={(e) => set({ admin_group: e.target.value })} />}</Field>
            </div>
          </>
        )}
        {view.redirect_uri && (
          <Field label={t('id.provider.redirect')}>{(id) => <Input id={id} value={view.redirect_uri} readOnly onFocus={(e) => e.target.select()} />}</Field>
        )}
      </fieldset>
      {action.error && (
        <Banner kind="error" title={action.error.message}>
          {action.error.detail}
        </Banner>
      )}
    </Modal>
  )
}

function ApplicationsCard({ editable, connect, setConnect }: { editable: boolean; connect: boolean; setConnect: (v: boolean) => void }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const [epoch, setEpoch] = useState(0)
  const list = useResource<(Application & { people: number })[]>('/api/identity/applications', epoch)
  const refs = useResource<Refs>('/api/refs', 0)
  const [open, setOpen] = useState<Application | null>(null)
  const reload = () => setEpoch((e) => e + 1)
  const roleName = (id: string) => refs.data?.roles.find((r) => r.id === id)?.name ?? id
  const accessText = (a: Application) =>
    a.access === 'roles' ? t('id.access.roles', { list: a.roles.map(roleName).join(', ') }) : a.access === 'groups' ? t('id.access.groups', { list: a.groups.join(', ') }) : t('id.access.all')
  return (
    <section className="card status-card id-apps" aria-label={t('id.apps')}>
      <header>
        <h2>{t('id.apps')}</h2>
        {editable && (
          <Button variant="primary" onClick={() => setConnect(true)} disabled={!refs.data}>
            <Plus size={16} />
            {t('id.apps.connect')}
          </Button>
        )}
      </header>
      <p className="muted">{t('id.apps.text')}</p>
      <ErrorBanner error={list.error} strings={strings} />
      {list.data && list.data.length === 0 && <p className="hint">{t('id.apps.empty')}</p>}
      {list.data && list.data.length > 0 && (
        <div className="cn-table-wrap">
          <table className="cn-table">
            <thead>
              <tr>
                <th>{t('id.apps.col.app')}</th>
                <th>{t('id.apps.col.access')}</th>
                <th>{t('id.apps.col.people')}</th>
                <th>{t('id.apps.col.sync')}</th>
              </tr>
            </thead>
            <tbody>
              {list.data.map((a) => (
                <tr key={a.id}>
                  <td>
                    <button type="button" className="cn-link cn-name" onClick={() => setOpen(a)}>
                      <AppWindow size={14} /> {a.name}
                    </button>
                    {a.disabled && <span className="muted"> · {t('id.apps.disabled')}</span>}
                    <div className="muted">{a.url}</div>
                  </td>
                  <td>{accessText(a)}</td>
                  <td>{a.people}</td>
                  <td className="muted">
                    {a.last_sync_at ? formatDate(a.last_sync_at, locale, timezone) : t('id.apps.never')}
                    {a.last_sync_ip && <div>{t('id.app.from', { ip: a.last_sync_ip })}</div>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {connect && refs.data && (
        <ConnectWizard
          refs={refs.data}
          onClose={() => {
            setConnect(false)
            reload()
          }}
        />
      )}
      {open && refs.data && (
        <ApplicationDialog
          app={open}
          refs={refs.data}
          editable={editable}
          onClose={() => setOpen(null)}
          onSaved={() => {
            setOpen(null)
            reload()
          }}
        />
      )}
    </section>
  )
}

type Draft = { kind: string; name: string; url: string; redirect_uri: string; access: 'all' | 'roles' | 'groups'; roles: string[]; groups: string; sync_passkeys: boolean; disabled: boolean }

function draftOf(a?: Application): Draft {
  return {
    kind: a?.kind ?? 'network-control',
    name: a?.name ?? '',
    url: a?.url ?? '',
    redirect_uri: a?.redirect_uri ?? '',
    access: a?.access ?? 'all',
    roles: a?.roles ?? [],
    groups: (a?.groups ?? []).join('\n'),
    sync_passkeys: a?.sync_passkeys ?? true,
    disabled: a?.disabled ?? false,
  }
}

const bodyOf = (d: Draft) => ({ ...d, groups: d.groups.split('\n').map((g) => g.trim()).filter(Boolean) })

function AppFields({ draft, set, refs, step }: { draft: Draft; set: (p: Partial<Draft>) => void; refs: Refs; step: 1 | 2 | 0 }) {
  const t = useT(strings)
  return (
    <>
      {(step === 1 || step === 0) && (
        <>
          {step === 1 && (
            <Field label={t('id.wiz.kind')}>
              {(id) => (
                <Select id={id} value={draft.kind} onChange={(e) => set({ kind: e.target.value, name: "" })}>
                  {['network-control', 'umbrella-flow', 'inventory-db', 'oidc'].map((k) => (
                    <option key={k} value={k}>
                      {t(`id.kind.${k}`)}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
          )}
          <Field label={t('id.wiz.name')}>{(id) => <Input id={id} value={draft.name} placeholder={draft.kind === 'oidc' ? '' : t(`id.kind.${draft.kind}`)} onChange={(e) => set({ name: e.target.value })} />}</Field>
          <Field label={t('id.wiz.url')} hint={t('id.wiz.url.hint')}>
            {(id) => <Input id={id} value={draft.url} placeholder="https://" onChange={(e) => set({ url: e.target.value })} />}
          </Field>
          <Field label={t('id.wiz.redirect')} hint={t('id.wiz.redirect.hint')}>
            {(id) => (
              <Input
                id={id}
                value={draft.kind === 'oidc' ? draft.redirect_uri : draft.url ? `${draft.url.replace(/\/+$/, '')}/api/auth/umbrella/callback` : ''}
                readOnly={draft.kind !== 'oidc'}
                onChange={(e) => set({ redirect_uri: e.target.value })}
              />
            )}
          </Field>
        </>
      )}
      {(step === 2 || step === 0) && (
        <>
          <Field label={t('id.wiz.access')}>
            {(id) => (
              <Select id={id} value={draft.access} onChange={(e) => set({ access: e.target.value as Draft["access"] })}>
                <option value="all">{t('id.wiz.access.all')}</option>
                <option value="roles">{t('id.wiz.access.roles')}</option>
                <option value="groups">{t('id.wiz.access.groups')}</option>
              </Select>
            )}
          </Field>
          {draft.access === 'roles' && (
            <div className="id-roles">
              {refs.roles.map((r) => (
                <label key={r.id} className="id-check">
                  <input
                    type="checkbox"
                    checked={draft.roles.includes(r.id)}
                    onChange={(e) => set({ roles: e.target.checked ? [...draft.roles, r.id] : draft.roles.filter((x) => x !== r.id) })}
                  />
                  {r.name}
                </label>
              ))}
            </div>
          )}
          {draft.access === 'groups' && (
            <Field label={t('id.wiz.groups')} hint={t('id.wiz.groups.hint')}>
              {(id) => <Textarea id={id} rows={4} value={draft.groups} placeholder="cn=noc,ou=groups,dc=example,dc=com" onChange={(e) => set({ groups: e.target.value })} />}
            </Field>
          )}
          <Switch checked={draft.sync_passkeys} onChange={(v) => set({ sync_passkeys: v })} label={t('id.wiz.passkeys')} hint={t('id.wiz.passkeys.hint')} />
          {step === 0 && <Switch checked={draft.disabled} onChange={(v) => set({ disabled: v })} label={t('id.wiz.disabled')} />}
        </>
      )}
    </>
  )
}

function Steps({ step }: { step: number }) {
  const t = useT(strings)
  return (
    <ol className="id-steps" aria-label={t('id.wiz.step', { n: step })}>
      {[1, 2, 3].map((n) => (
        <li key={n} className={n === step ? 'current' : n < step ? 'done' : ''} aria-current={n === step ? 'step' : undefined}>
          <span className="id-step-n">{n < step ? <Check size={13} /> : n}</span>
          {t(`id.wiz.s${n}`)}
        </li>
      ))}
    </ol>
  )
}

function ConnectWizard({ refs, onClose }: { refs: Refs; onClose: () => void }) {
  const t = useT(strings)
  const [step, setStep] = useState<1 | 2 | 3>(1)
  const [draft, setDraft] = useState<Draft>(draftOf())
  const [conn, setConn] = useState<Connection | null>(null)
  const [copied, setCopied] = useState(false)
  const action = useAction(strings)
  const set = (p: Partial<Draft>) => setDraft({ ...draft, ...p })
  const create = () =>
    action.run(async () => {
      setConn(await api<Connection>('POST', '/api/identity/applications', bodyOf(draft)))
      setStep(3)
    })
  const okStep1 = /^https?:\/\/\S+$/.test(draft.url.trim()) && (draft.kind !== 'oidc' || draft.name.trim() !== '')
  const okStep2 = draft.access === 'all' || (draft.access === 'roles' ? draft.roles.length > 0 : draft.groups.trim() !== '')
  const appName = conn?.application.name ?? (draft.name || t(`id.kind.${draft.kind}`))
  return (
    <Modal
      open
      title={t('id.wiz.title')}
      onClose={onClose}
      footer={
        step === 1 ? (
          <Button variant="primary" disabled={!okStep1} onClick={() => setStep(2)}>
            {t('id.wiz.next')}
          </Button>
        ) : step === 2 ? (
          <>
            <Button variant="ghost" onClick={() => setStep(1)}>
              {t('id.wiz.back')}
            </Button>
            <Button variant="primary" busy={action.busy} disabled={!okStep2} onClick={() => void create()}>
              {t('id.wiz.create')}
            </Button>
          </>
        ) : (
          <Button variant="primary" onClick={onClose}>
            {t('id.wiz.done')}
          </Button>
        )
      }
    >
      <Steps step={step} />
      {step < 3 && <AppFields draft={draft} set={set} refs={refs} step={step as 1 | 2} />}
      {step === 3 && conn && (
        <>
          <Banner kind="warn" title={t('id.wiz.key.title')}>
            {t('id.wiz.key.text', { app: appName })}
          </Banner>
          <div className="id-key">
            <code>{conn.connection_key}</code>
            <Button
              onClick={() => {
                void navigator.clipboard?.writeText(conn.connection_key).then(() => setCopied(true))
              }}
            >
              {copied ? <Check size={14} /> : <Copy size={14} />}
              {copied ? t('id.wiz.copied') : t('id.wiz.copy')}
            </Button>
          </div>
        </>
      )}
      {action.error && (
        <Banner kind="error" title={action.error.message}>
          {action.error.detail}
        </Banner>
      )}
    </Modal>
  )
}

function ApplicationDialog({ app, refs, editable, onClose, onSaved }: { app: Application; refs: Refs; editable: boolean; onClose: () => void; onSaved: () => void }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const [draft, setDraft] = useState<Draft>(draftOf(app))
  const [key, setKey] = useState('')
  const action = useAction(strings)
  const set = (p: Partial<Draft>) => setDraft({ ...draft, ...p })
  const save = () =>
    action.run(async () => {
      await api('PUT', `/api/identity/applications/${app.id}`, bodyOf(draft))
      onSaved()
    })
  const reissue = () =>
    action.run(async () => {
      if (!(await ask({ text: t('id.app.key.ask', { name: app.name }), danger: true }))) return
      setKey((await api<Connection>('POST', `/api/identity/applications/${app.id}/key`)).connection_key)
    })
  const remove = () =>
    action.run(async () => {
      if (!(await ask({ text: t('id.app.delete.ask', { name: app.name }), danger: true }))) return
      await api('DELETE', `/api/identity/applications/${app.id}`)
      onSaved()
    })
  return (
    <Modal
      open
      title={app.name}
      onClose={onClose}
      footer={
        editable && (
          <>
            <Button variant="ghost" busy={action.busy} onClick={() => void remove()}>
              <Trash2 size={14} />
              {t('id.app.delete')}
            </Button>
            <Button busy={action.busy} onClick={() => void reissue()}>
              <KeyRound size={14} />
              {t('id.app.key')}
            </Button>
            <Button variant="primary" busy={action.busy} onClick={() => void save()}>
              {t('id.app.save')}
            </Button>
          </>
        )
      }
    >
      <p className="hint">
        {t('id.app.client')}: <code>{app.client_id}</code>
        {app.last_sign_in_at && <> · {t('id.app.signins', { n: app.sign_ins, at: formatDate(app.last_sign_in_at, locale, timezone) })}</>}
      </p>
      {key && (
        <>
          <Banner kind="warn" title={t('id.wiz.key.title')}>
            {t('id.wiz.key.text', { app: app.name })}
          </Banner>
          <div className="id-key">
            <code>{key}</code>
            <Button onClick={() => void navigator.clipboard?.writeText(key)}>
              <Copy size={14} />
              {t('id.wiz.copy')}
            </Button>
          </div>
        </>
      )}
      <fieldset className="plain-fieldset stack" disabled={!editable}>
        <AppFields draft={draft} set={set} refs={refs} step={0} />
      </fieldset>
      {action.error && (
        <Banner kind="error" title={action.error.message}>
          {action.error.detail}
        </Banner>
      )}
    </Modal>
  )
}
