import { useEffect, useState } from 'react'
import { Bot, Copy, KeyRound, Plus, Trash2 } from 'lucide-react'
import { api } from '../../../api'
import { ask } from '../../../confirm'
import { ErrorBanner, ErrorFlash } from '../../../connections/ConnectionCard'
import { useAction, useResource } from '../../../connections/useRequest'
import { useLocale, useT } from '../../../i18n'
import { Banner, Button, Field, formatDate, Input, Modal, Select, Switch, Textarea } from '../../../ui'
import { useSession } from '../../session'
import { roleLabel } from '../../types'
import { AccessFields, ScopeField, type Access, type ScopeMode } from '../../users/AccessFields'
import { adminOf, scopeModeOf, type Refs } from '../../users/model'
import { strings } from './strings'
import '../../connectors/connectors.css'

export type APIToken = {
  id: string
  account_id: string
  name: string
  hint: string
  expires_at?: string
  last_used_at?: string
  last_used_ip?: string
  created_at: string
  created_by: string
  revoked_at?: string
  revoked_by?: string
}

export type ServiceAccount = {
  id: string
  name: string
  description: string
  role: string
  role_name: string
  team_ids: string[]
  scope_mode: ScopeMode
  service_ids: string[]
  disabled: boolean
  created_at: string
  created_by: string
  tokens: APIToken[]
}

type Issued = { token: APIToken; secret: string }

const active = (tk: APIToken, now = Date.now()) => !tk.revoked_at && (!tk.expires_at || Date.parse(tk.expires_at) > now)

function lastUsed(a: ServiceAccount) {
  return a.tokens.reduce<string | undefined>((m, tk) => (tk.last_used_at && (!m || tk.last_used_at > m) ? tk.last_used_at : m), undefined)
}

// ServiceAccountsPage: Settings → Service accounts. Machine identities with a role, teams and a
// visibility scope, and the API tokens they call the API with (docs/integrations.md §1).
export function ServiceAccountsPage() {
  const t = useT(strings)
  const { locale } = useLocale()
  const { can, timezone } = useSession()
  const editable = can('serviceaccounts:edit')
  const [epoch, setEpoch] = useState(0)
  const [open, setOpen] = useState<{ id: string | null } | null>(null)
  const list = useResource<ServiceAccount[]>('/api/service-accounts', epoch)
  const refs = useResource<Refs>('/api/refs', 0)
  const reload = () => setEpoch((e) => e + 1)
  const scopeText = (a: ServiceAccount) => {
    const mode = scopeModeOf(a)
    return mode === 'services' ? t('sa.scope.services', { n: a.service_ids.length }) : t(`sa.scope.${mode}`)
  }
  const current = open?.id ? (list.data?.find((a) => a.id === open.id) ?? null) : null

  return (
    <div className="cn-page">
      {editable && (
        <div className="row">
          <Button variant="primary" onClick={() => setOpen({ id: null })} disabled={!refs.data}>
            <Plus size={16} />
            {t('sa.create')}
          </Button>
        </div>
      )}
      <ErrorBanner error={list.error ?? refs.error} strings={strings} />
      {list.data && list.data.length === 0 && (
        <div className="card cn-empty">
          <p>{t('sa.empty')}</p>
        </div>
      )}
      {list.data && list.data.length > 0 && (
        <div className="card cn-table-wrap">
          <table className="cn-table">
            <thead>
              <tr>
                <th>{t('sa.col.name')}</th>
                <th>{t('sa.col.role')}</th>
                <th>{t('sa.col.scope')}</th>
                <th>{t('sa.col.tokens')}</th>
                <th>{t('sa.col.lastUsed')}</th>
              </tr>
            </thead>
            <tbody>
              {list.data.map((a) => {
                const used = lastUsed(a)
                return (
                  <tr key={a.id}>
                    <td>
                      <button type="button" className="cn-link cn-name" onClick={() => setOpen({ id: a.id })} disabled={!refs.data}>
                        <Bot size={14} /> {a.name}
                      </button>
                      {a.disabled && <span className="muted"> · {t('sa.disabled')}</span>}
                      {a.description && <div className="muted">{a.description}</div>}
                    </td>
                    <td>{roleLabel(t, a.role, a.role_name)}</td>
                    <td>{scopeText(a)}</td>
                    <td>{a.tokens.filter((tk) => active(tk)).length}</td>
                    <td className="muted">{used ? formatDate(used, locale, timezone) : t('sa.never')}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
      {open && refs.data && (
        <AccountDialog
          key={open.id ?? 'new'}
          account={current}
          refs={refs.data}
          editable={editable}
          onClose={() => setOpen(null)}
          onSaved={(a) => {
            reload()
            setOpen(a ? { id: a.id } : null)
          }}
          onTokens={reload}
        />
      )}
    </div>
  )
}

function AccountDialog({
  account,
  refs,
  editable,
  onClose,
  onSaved,
  onTokens,
}: {
  account: ServiceAccount | null
  refs: Refs
  editable: boolean
  onClose: () => void
  onSaved: (a: ServiceAccount | null) => void
  onTokens: () => void
}) {
  const t = useT(strings)
  const [name, setName] = useState(account?.name ?? '')
  const [description, setDescription] = useState(account?.description ?? '')
  const [access, setAccess] = useState<Access>({ role_id: account?.role ?? refs.roles.find((r) => !r.system)?.id ?? refs.roles[0]?.id ?? '', team_ids: account?.team_ids ?? [] })
  const [mode, setMode] = useState<ScopeMode>(account ? scopeModeOf(account) : 'all')
  const [services, setServices] = useState<string[]>(account?.service_ids ?? [])
  const [disabled, setDisabled] = useState(account?.disabled ?? false)
  const action = useAction()

  const save = async () => {
    const body = { name, description, role_id: access.role_id, team_ids: access.team_ids, scope_mode: mode, service_ids: mode === 'services' ? services : [], disabled }
    const out = await action.run(() =>
      account ? api<ServiceAccount>('PUT', `/api/service-accounts/${account.id}`, body) : api<ServiceAccount>('POST', '/api/service-accounts', body),
    )
    if (out) onSaved(out)
  }
  const remove = async () => {
    if (!account || !(await ask({ text: t('sa.delete.ask', { name: account.name }), danger: true }))) return
    const ok = await action.run(async () => {
      await api('DELETE', `/api/service-accounts/${account.id}`)
      return true
    })
    if (ok) onSaved(null)
  }

  return (
    <Modal
      open
      title={account ? account.name : t('sa.new')}
      onClose={onClose}
      footer={
        editable && (
          <>
            {account && (
              <Button variant="ghost" busy={action.busy} onClick={() => void remove()}>
                <Trash2 size={14} />
                {t('sa.delete')}
              </Button>
            )}
            <Button variant="primary" busy={action.busy} disabled={!name.trim() || !access.role_id} onClick={() => void save()}>
              {t('sa.save')}
            </Button>
          </>
        )
      }
    >
      <fieldset className="plain-fieldset stack" disabled={!editable}>
        <Field label={t('sa.field.name')} hint={t('sa.field.name.hint')}>
          {(id) => <Input id={id} value={name} maxLength={64} autoComplete="off" onChange={(e) => setName(e.target.value)} />}
        </Field>
        <Field label={t('sa.field.description')} optional={t('sa.optional')}>
          {(id) => <Textarea id={id} rows={2} value={description} maxLength={500} onChange={(e) => setDescription(e.target.value)} />}
        </Field>
        <AccessFields refs={refs} value={access} onChange={setAccess} />
        <ScopeField refs={refs} mode={mode} value={services} admin={adminOf({ role: access.role_id })} onMode={setMode} onChange={setServices} />
        <Switch checked={disabled} onChange={setDisabled} label={t('sa.field.disabled')} />
      </fieldset>
      <ErrorFlash error={action.error} strings={strings} />
      {account && <Tokens account={account} editable={editable} onChanged={onTokens} />}
    </Modal>
  )
}

const EXPIRY = ['never', '30', '90', '365'] as const

function Tokens({ account, editable, onChanged }: { account: ServiceAccount; editable: boolean; onChanged: () => void }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const [name, setName] = useState('')
  const [expiry, setExpiry] = useState<(typeof EXPIRY)[number]>('365')
  const [made, setMade] = useState<Issued | null>(null)
  const [copied, setCopied] = useState(false)
  const action = useAction()
  useEffect(() => setCopied(false), [made])
  const date = (v?: string) => formatDate(v, locale, timezone)

  const issue = async () => {
    const expires_at = expiry === 'never' ? null : new Date(Date.now() + Number(expiry) * 86_400_000).toISOString()
    const out = await action.run(() => api<Issued>('POST', `/api/service-accounts/${account.id}/tokens`, { name, expires_at }))
    if (!out) return
    setMade(out)
    setName('')
    onChanged()
  }
  const revoke = async (tk: APIToken) => {
    if (!(await ask({ text: t('sa.token.revoke.ask', { name: tk.name }), ok: t('sa.token.revoke'), danger: true }))) return
    const ok = await action.run(() => api('POST', `/api/service-accounts/${account.id}/tokens/${tk.id}/revoke`))
    if (ok) onChanged()
  }
  const copy = async () => {
    if (!made) return
    try {
      await navigator.clipboard.writeText(made.secret)
      setCopied(true)
    } catch {
      setCopied(false)
    }
  }

  return (
    <section className="stack">
      <h3>{t('sa.tokens')}</h3>
      <p className="hint">{t('sa.tokens.hint')}</p>
      {made && (
        <Banner kind="ok" title={t('sa.token.made', { name: made.token.name })}>
          <p>{t('sa.token.once')}</p>
          <div className="row" style={{ alignItems: 'center', flexWrap: 'wrap' }}>
            <code className="cn-mono" style={{ wordBreak: 'break-all', userSelect: 'all' }}>
              {made.secret}
            </code>
            <Button variant="ghost" onClick={() => void copy()}>
              <Copy size={15} aria-hidden />
              {copied ? t('sa.token.copied') : t('sa.token.copy')}
            </Button>
          </div>
        </Banner>
      )}
      {account.tokens.length === 0 ? (
        <p className="muted">{t('sa.tokens.empty')}</p>
      ) : (
        <ul className="stack" style={{ listStyle: 'none', padding: 0, margin: 0 }}>
          {account.tokens.map((tk) => {
            const on = active(tk)
            const state = tk.revoked_at ? t('sa.token.state.revoked', { at: date(tk.revoked_at) }) : on ? t('sa.token.state.active') : t('sa.token.state.expired')
            return (
              <li key={tk.id} className="row" style={{ alignItems: 'flex-start', justifyContent: 'space-between', gap: 12 }}>
                <div className={on ? '' : 'muted'}>
                  <div>
                    <KeyRound size={14} aria-hidden /> <strong>{tk.name}</strong> <code className="cn-mono">{tk.hint}</code> · {state}
                  </div>
                  <small className="muted">
                    {[
                      t('sa.token.created', { at: date(tk.created_at), by: tk.created_by }),
                      tk.expires_at ? t('sa.token.expiresAt', { at: date(tk.expires_at) }) : '',
                      tk.last_used_at ? t('sa.token.used', { at: date(tk.last_used_at), ip: tk.last_used_ip ?? '' }) : t('sa.token.unused'),
                    ]
                      .filter(Boolean)
                      .join(' · ')}
                  </small>
                </div>
                {editable && on && (
                  <Button variant="ghost" busy={action.busy} onClick={() => void revoke(tk)}>
                    {t('sa.token.revoke')}
                  </Button>
                )}
              </li>
            )
          })}
        </ul>
      )}
      {editable && (
        <div className="grid-2">
          <Field label={t('sa.token.name')}>
            {(id) => <Input id={id} value={name} maxLength={100} onChange={(e) => setName(e.target.value)} />}
          </Field>
          <Field label={t('sa.token.expires')}>
            {(id) => (
              <Select id={id} value={expiry} onChange={(e) => setExpiry(e.target.value as (typeof EXPIRY)[number])}>
                {EXPIRY.map((v) => (
                  <option key={v} value={v}>
                    {t(`sa.token.expires.${v}`)}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <div>
            <Button busy={action.busy} disabled={!name.trim()} onClick={() => void issue()}>
              <KeyRound size={15} aria-hidden />
              {t('sa.token.issue')}
            </Button>
          </div>
        </div>
      )}
      <ErrorFlash error={action.error} strings={strings} />
    </section>
  )
}
