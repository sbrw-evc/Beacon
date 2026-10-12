import { useEffect, useState } from 'react'
import { KeyRound, Plus, Trash2 } from 'lucide-react'
import { api } from '../../api'
import { ErrorBanner, ErrorFlash } from '../../connections/ConnectionCard'
import { useAction, useResource } from '../../connections/useRequest'
import { useLocale, useT } from '../../i18n'
import { Link } from '../../router'
import { Button, Field, formatDate, Input, Modal, Password, Select, Textarea } from '../../ui'
import { useSession } from '../session'
import { strings } from './strings'
import { credentialUseLink, type Credential, type CredentialKind, type CredentialType, type CredentialUse } from './types'
import './connectors.css'

type Editing = { credential: Credential | null } | null

export function CredentialsPage() {
  const t = useT(strings)
  const { locale } = useLocale()
  const { can, timezone } = useSession()
  const editable = can('credentials:edit')
  const [epoch, setEpoch] = useState(0)
  const [editing, setEditing] = useState<Editing>(null)
  const list = useResource<Credential[]>('/api/credentials', epoch)

  return (
    <div className="cn-page">
      {editable && (
        <div className="row">
          <Button variant="primary" onClick={() => setEditing({ credential: null })}>
            <Plus size={16} />
            {t('cred.create')}
          </Button>
        </div>
      )}
      <ErrorBanner error={list.error} strings={strings} />
      {list.data && list.data.length === 0 && (
        <div className="card cn-empty">
          <p>{t('cred.empty')}</p>
        </div>
      )}
      {list.data && list.data.length > 0 && (
        <div className="card cn-table-wrap">
          <table className="cn-table">
            <thead>
              <tr>
                <th>{t('cn.col.name')}</th>
                <th>{t('cred.type')}</th>
                <th>{t('cred.usedBy')}</th>
                <th>{t('cred.updated')}</th>
              </tr>
            </thead>
            <tbody>
              {list.data.map((c) => (
                <tr key={c.id}>
                  <td>
                    <button type="button" className="cn-link cn-name" onClick={() => setEditing({ credential: c })}>
                      <KeyRound size={14} /> {c.name}
                    </button>
                    {c.description && <div className="muted">{c.description}</div>}
                  </td>
                  <td>{t(`cred.type.${c.type}`)}</td>
                  <td>
                    {c.used_by.length === 0 ? '—' : <UsedBy uses={c.used_by} />}
                  </td>
                  <td className="muted">
                    {formatDate(c.updated_at, locale, timezone)} · {c.updated_by}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <CredentialDialog
        editing={editing}
        editable={editable}
        onClose={() => setEditing(null)}
        onSaved={() => {
          setEditing(null)
          setEpoch((e) => e + 1)
        }}
      />
    </div>
  )
}

function UsedBy({ uses }: { uses: CredentialUse[] }) {
  const t = useT(strings)
  return (
    <>
      {uses.map((u, i) => (
        <span key={u.kind + u.id}>
          {i > 0 && ', '}
          <span className="muted">{t(`cred.use.${u.kind}`)}</span> <Link to={credentialUseLink(u)}>{u.name}</Link>
        </span>
      ))}
    </>
  )
}

function CredentialDialog({ editing, editable, onClose, onSaved }: { editing: Editing; editable: boolean; onClose: () => void; onSaved: () => void }) {
  const t = useT(strings)
  const existing = editing?.credential ?? null
  const [name, setName] = useState('')
  const [type, setType] = useState<CredentialType>('bearer')
  const [description, setDescription] = useState('')
  const [fields, setFields] = useState<Record<string, string>>({})
  const [secrets, setSecrets] = useState<Record<string, string>>({})
  const action = useAction()
  const kinds = useResource<CredentialKind[]>(editing !== null ? '/api/credentials/kinds' : '', 0)

  useEffect(() => {
    if (!editing) return
    setName(existing?.name ?? '')
    setType(existing?.type ?? 'bearer')
    setDescription(existing?.description ?? '')
    setFields(existing?.fields ?? {})
    setSecrets({})
    action.clear()
  }, [editing])

  const kind = kinds.data?.find((k) => k.type === type) ?? { type, fields: [], secrets: [] }
  const submit = async () => {
    const body = { name, type, description, fields, secrets: Object.fromEntries(Object.entries(secrets).filter(([, v]) => v !== '')) }
    const ok = await action.run(() => (existing ? api('PUT', `/api/credentials/${existing.id}`, body) : api('POST', '/api/credentials', body)))
    if (ok) onSaved()
  }
  const remove = async () => {
    if (!existing) return
    const ok = await action.run(async () => {
      await api('DELETE', `/api/credentials/${existing.id}`)
      return true
    })
    if (ok) onSaved()
  }
  const generate = (key: string) => {
    const bytes = new Uint8Array(32)
    crypto.getRandomValues(bytes)
    setSecrets((s) => ({ ...s, [key]: Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('') }))
  }

  return (
    <Modal
      open={editing !== null}
      title={existing ? existing.name : t('cred.new')}
      onClose={onClose}
      footer={
        editable && (
          <>
            {existing && (
              <Button variant="ghost" busy={action.busy} onClick={remove} disabled={existing.used_by.length > 0} title={existing.used_by.length ? t('cred.inUse') : undefined}>
                <Trash2 size={14} />
                {t('cn.delete')}
              </Button>
            )}
            <Button variant="primary" busy={action.busy} disabled={!name.trim()} onClick={submit}>
              {t('cn.save')}
            </Button>
          </>
        )
      }
    >
      <Field label={t('cn.col.name')}>{(id) => <Input id={id} value={name} readOnly={!editable} maxLength={100} onChange={(e) => setName(e.target.value)} />}</Field>
      <Field label={t('cred.type')} hint={t(`cred.type.${type}.hint`)}>
        {(id) => (
          <Select id={id} value={type} disabled={!!existing || !editable} onChange={(e) => setType(e.target.value as CredentialType)}>
            {(kinds.data ?? [kind]).map((k) => (
              <option key={k.type} value={k.type}>
                {t(`cred.type.${k.type}`)}
              </option>
            ))}
          </Select>
        )}
      </Field>
      <Field label={t('cn.field.description')} optional={t('cn.optional')}>
        {(id) => <Textarea id={id} rows={2} value={description} readOnly={!editable} onChange={(e) => setDescription(e.target.value)} />}
      </Field>
      {kind.fields.map((f) => (
        <Field key={f} label={t(`cred.field.${f}`)}>
          {(id) => <Input id={id} value={fields[f] ?? ''} readOnly={!editable} onChange={(e) => setFields({ ...fields, [f]: e.target.value })} />}
        </Field>
      ))}
      {kind.secrets.map((s) => (
        <Field key={s} label={t(`cred.field.${s}`)} hint={existing?.secrets_set.includes(s) ? t('cred.secret.set') : t('cred.secret.hint')}>
          {(id) => (
            <div className="row">
              <Password
                id={id}
                value={secrets[s] ?? ''}
                readOnly={!editable}
                autoComplete="new-password"
                placeholder={existing?.secrets_set.includes(s) ? '••••••••' : ''}
                onChange={(e) => setSecrets({ ...secrets, [s]: e.target.value })}
              />
              {editable && (s === 'token' || s === 'secret') && (
                <Button variant="ghost" onClick={() => generate(s)}>
                  {t('cred.generate')}
                </Button>
              )}
            </div>
          )}
        </Field>
      ))}
      {existing && existing.used_by.length > 0 && (
        <p className="hint">
          {t('cred.inUse')} <UsedBy uses={existing.used_by} />
        </p>
      )}
      <ErrorBanner error={kinds.error} strings={strings} />
      <ErrorFlash error={action.error} strings={strings} />
    </Modal>
  )
}
