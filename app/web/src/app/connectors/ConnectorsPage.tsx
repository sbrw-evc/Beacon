import { useMemo, useState } from 'react'
import { FileUp, Lock, Plus, PlugZap } from 'lucide-react'
import { api } from '../../api'
import { ErrorBanner, ErrorFlash } from '../../connections/ConnectionCard'
import { useAction, useResource } from '../../connections/useRequest'
import { useLocale, useT } from '../../i18n'
import { Link, useRouter } from '../../router'
import { Banner, Button, Field, FileInput, formatDate, Input, Modal, Select, Textarea } from '../../ui'
import { useSession } from '../session'
import { CreateToken, useCanCreateToken } from './CreateToken'
import { ConnectorEditor } from './Editor'
import { slugify } from './graph'
import { QuickConnectDialog, SourcesExplainer, useCanQuickConnect } from './QuickConnect'
import { sourcesStrings } from './sourcesStrings'
import { strings } from './strings'
import type { ConnectorList, ConnectorSummary, Connector, CredentialChoice, ImportCheck, Preset, Slot } from './types'
import './connectors.css'
import { Flash } from '../../notify'
import { SetupGuide } from '../guide/SetupGuide'

export function ConnectorsPage() {
  const { path } = useRouter()
  const id = path.startsWith('/connectors/') ? decodeURIComponent(path.slice('/connectors/'.length)) : ''
  return id ? <ConnectorEditor key={id} id={id} /> : <ConnectorListPage />
}

export function StatusPill({ status }: { status: ConnectorSummary['status'] }) {
  const t = useT(strings)
  const kind = status === 'published' ? 'pill-ok' : status === 'publish_failed' ? 'pill-error' : status === 'changed' ? 'pill-warn' : 'pill-off'
  return <span className={`pill ${kind}`}>{t(`cn.status.${status}`)}</span>
}

export function ingestURL(path: string) {
  return `${window.location.origin}${path}`
}

function ConnectorListPage() {
  const t = useT(strings)
  const { locale } = useLocale()
  const { can, timezone } = useSession()
  const [epoch, setEpoch] = useState(0)
  const [creating, setCreating] = useState(false)
  const [importing, setImporting] = useState(false)
  const list = useResource<ConnectorList>('/api/connectors', epoch)
  const editable = can('connectors:edit')
  const quick = useCanQuickConnect()
  const ts = useT(sourcesStrings)
  const [connecting, setConnecting] = useState(() => new URLSearchParams(window.location.search).get('connect') === '1')

  return (
    <div className="cn-page">
      <div className="page-head">
        <div>
          <h1>{t('cn.title')}</h1>
          <p className="muted">{t('cn.subtitle')}</p>
          <SourcesExplainer />
        </div>
        {editable && (
          <div className="row">
            {quick && (
              <Button variant="primary" onClick={() => setConnecting(true)}>
                <PlugZap size={16} />
                {ts('src.connect')}
              </Button>
            )}
            <Button onClick={() => setImporting(true)}>
              <FileUp size={16} />
              {t('cn.import')}
            </Button>
            <Button variant={quick ? 'secondary' : 'primary'} onClick={() => setCreating(true)}>
              <Plus size={16} />
              {t('cn.create')}
            </Button>
          </div>
        )}
      </div>
      <SetupGuide page="connectors" actions={{ ...(quick ? { connect: () => setConnecting(true) } : {}), create: () => setCreating(true) }} />
      {list.error ? <ErrorBanner error={list.error} strings={strings} /> : null}
      {list.data && !list.data.ingest && <Banner kind="warn" title={t('cn.ingestDown')} />}
      {!list.data && !list.error && <p className="muted">{t('loading')}</p>}
      {list.data && list.data.connectors.length === 0 && (
        <div className="card cn-empty">
          <p>{t('cn.empty')}</p>
          {editable && <p className="muted">{t('cn.empty.hint')}</p>}
        </div>
      )}
      {list.data && list.data.connectors.length > 0 && (
        <div className="card cn-table-wrap">
          <table className="cn-table">
            <thead>
              <tr>
                <th>{t('cn.col.name')}</th>
                <th>{t('cn.col.status')}</th>
                <th className="num">{t('cn.col.received')}</th>
                <th className="num">{t('cn.col.events')}</th>
                <th className="num">{t('cn.col.failed')}</th>
                <th>{t('cn.col.last')}</th>
              </tr>
            </thead>
            <tbody>
              {list.data.connectors.map((c) => (
                <tr key={c.id}>
                  <td>
                    <Link to={`/connectors/${encodeURIComponent(c.id)}`} className="cn-name">
                      {c.name}
                    </Link>
                    <div className="muted cn-mono">{c.ingest_path}</div>
                    {c.lock && !c.lock.mine && (
                      <div className="muted cn-lock">
                        <Lock size={12} /> {t('cn.lockedBy', { name: c.lock.name || c.lock.username })}
                      </div>
                    )}
                  </td>
                  <td>
                    <StatusPill status={c.status} />
                    {c.published > 0 && <div className="muted">{t('cn.version', { n: c.published })}</div>}
                  </td>
                  <td className="num">{c.stats?.received ?? '—'}</td>
                  <td className="num">{c.stats?.events ?? '—'}</td>
                  <td className="num">
                    {c.stats ? (
                      <span className={c.stats.open_failures > 0 ? 'cn-bad' : ''}>
                        {c.stats.failed}
                        {c.stats.open_failures > 0 && ` (${t('cn.open', { n: c.stats.open_failures })})`}
                      </span>
                    ) : (
                      '—'
                    )}
                  </td>
                  <td className="muted">{c.stats?.last_received ? formatDate(c.stats.last_received, locale, timezone) : t('cn.never')}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="muted cn-foot">{t('cn.statsHint')}</p>
        </div>
      )}
      <CreateDialog open={creating} onClose={() => setCreating(false)} onCreated={() => setEpoch((e) => e + 1)} />
      <ImportDialog open={importing} onClose={() => setImporting(false)} />
      <QuickConnectDialog open={connecting} onClose={() => setConnecting(false)} onDone={() => setEpoch((e) => e + 1)} />
    </div>
  )
}

export function CredentialSelect({
  value,
  types,
  choices,
  onChange,
  id,
  disabled,
}: {
  value: string
  types: string[]
  choices: CredentialChoice[]
  onChange: (v: string) => void
  id?: string
  disabled?: boolean
}) {
  const t = useT(strings)
  const fit = choices.filter((c) => types.length === 0 || types.includes(c.type))
  return (
    <Select id={id} value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)}>
      <option value="">{t('cn.cred.none')}</option>
      {fit.map((c) => (
        <option key={c.id} value={c.id}>
          {c.name} ({t(`cred.type.${c.type}`)})
        </option>
      ))}
      {value && !fit.some((c) => c.id === value) && <option value={value}>{t('cn.cred.missing')}</option>}
    </Select>
  )
}

function SlotMapping({
  slots,
  choices: loaded,
  value,
  onChange,
  name = '',
}: {
  slots: Slot[]
  choices: CredentialChoice[]
  value: Record<string, string>
  onChange: (v: Record<string, string>) => void
  name?: string
}) {
  const t = useT(strings)
  // Tokens made with «Create token» in this dialog.
  const [made, setMade] = useState<CredentialChoice[]>([])
  const canToken = useCanCreateToken([])
  if (slots.length === 0) return null
  const choices = [...loaded, ...made.filter((m) => !loaded.some((c) => c.id === m.id))]
  return (
    <div className="stack">
      <p className="muted">{t('cn.slots.hint')}</p>
      {slots.map((s) => (
        <SlotField
          key={s.slot}
          slot={s}
          choices={choices}
          value={value[s.slot] ?? ''}
          name={name}
          onChange={(v) => onChange({ ...value, [s.slot]: v })}
          onMade={(c) => setMade((m) => [...m, c])}
        />
      ))}
      {choices.length === 0 && !canToken && <p className="hint">{t('cn.slots.noCreds')}</p>}
    </div>
  )
}

// SlotField picks the credential of one slot; «Create token» makes a Bearer token for it in place.
function SlotField({
  slot: s,
  choices,
  value,
  name,
  onChange,
  onMade,
}: {
  slot: Slot
  choices: CredentialChoice[]
  value: string
  name: string
  onChange: (v: string) => void
  onMade: (c: CredentialChoice) => void
}) {
  const t = useT(strings)
  const canToken = useCanCreateToken(s.types)
  return (
    <>
      <Field label={s.name ? t('cn.slots.named', { slot: s.slot, name: s.name }) : s.slot} optional={t('cn.optional')}>
        {(id) => <CredentialSelect id={id} value={value} types={s.types} choices={choices} onChange={onChange} />}
      </Field>
      {canToken && (
        <CreateToken
          name={name}
          onCreated={(c) => {
            onMade(c)
            onChange(c.id)
          }}
        />
      )}
    </>
  )
}

function mapped(m: Record<string, string>) {
  return Object.fromEntries(Object.entries(m).filter(([, v]) => v))
}

function CreateDialog({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: () => void }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { navigate } = useRouter()
  const presets = useResource<Preset[]>('/api/connectors/presets', open ? 1 : 0)
  const choices = useResource<CredentialChoice[]>('/api/connectors/credentials', open ? 1 : 0)
  const [name, setName] = useState('')
  const [slug, setSlug] = useState('')
  const [slugTouched, setSlugTouched] = useState(false)
  const [description, setDescription] = useState('')
  const [preset, setPreset] = useState('webhook')
  const [creds, setCreds] = useState<Record<string, string>>({})
  const action = useAction()
  const current = useMemo(() => presets.data?.find((p) => p.id === preset), [presets.data, preset])

  const submit = async () => {
    const c = await action.run(() =>
      api<Connector>('POST', '/api/connectors', { name, slug: slug || slugify(name), description, tags: current?.tags ?? [], preset, credentials: mapped(creds) }),
    )
    if (!c) return
    onCreated()
    onClose()
    navigate(`/connectors/${encodeURIComponent(c.id)}`)
  }

  return (
    <Modal
      open={open}
      title={t('cn.create.title')}
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t('cn.cancel')}
          </Button>
          <Button variant="primary" busy={action.busy} disabled={!name.trim()} onClick={submit}>
            {t('cn.create')}
          </Button>
        </>
      }
    >
      <Field label={t('cn.field.preset')} hint={current ? current.description[locale] : t('cn.preset.blank.hint')}>
        {(id) => (
          <Select
            id={id}
            value={preset}
            onChange={(e) => {
              setPreset(e.target.value)
              setCreds({})
            }}
          >
            <option value="">{t('cn.preset.blank')}</option>
            {presets.data?.map((p) => (
              <option key={p.id} value={p.id}>
                {p.title[locale]}
              </option>
            ))}
          </Select>
        )}
      </Field>
      <Field label={t('cn.field.name')}>
        {(id) => (
          <Input
            id={id}
            value={name}
            maxLength={100}
            onChange={(e) => {
              setName(e.target.value)
              if (!slugTouched) setSlug(slugify(e.target.value))
            }}
          />
        )}
      </Field>
      <Field label={t('cn.field.slug')} hint={t('cn.field.slug.hint', { url: ingestURL(`/api/ingest/${slug || '…'}`) })}>
        {(id) => (
          <Input
            id={id}
            value={slug}
            className="cn-mono"
            onChange={(e) => {
              setSlugTouched(true)
              setSlug(e.target.value)
            }}
          />
        )}
      </Field>
      <Field label={t('cn.field.description')} optional={t('cn.optional')}>
        {(id) => <Textarea id={id} rows={2} value={description} onChange={(e) => setDescription(e.target.value)} />}
      </Field>
      {current && <SlotMapping key={preset} slots={current.credentials} choices={choices.data ?? []} value={creds} onChange={setCreds} name={name} />}
      <ErrorFlash error={action.error} strings={strings} />
    </Modal>
  )
}

function ImportDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const t = useT(strings)
  const { navigate } = useRouter()
  const [doc, setDoc] = useState<unknown>(null)
  const [fileError, setFileError] = useState('')
  const [check, setCheck] = useState<ImportCheck | null>(null)
  const [name, setName] = useState('')
  const [slug, setSlug] = useState('')
  const [creds, setCreds] = useState<Record<string, string>>({})
  const action = useAction()

  const reset = () => {
    setDoc(null)
    setCheck(null)
    setFileError('')
    setCreds({})
    action.clear()
  }

  const pick = async (f: File | undefined) => {
    reset()
    if (!f) return
    let parsed: unknown
    try {
      parsed = JSON.parse(await f.text())
    } catch {
      setFileError(t('cn.import.notJSON'))
      return
    }
    const c = await action.run(() => api<ImportCheck>('POST', '/api/connectors/import/check', { document: parsed }))
    if (!c) return
    setDoc(parsed)
    setCheck(c)
    setName(c.name)
    setSlug(slugify(c.name))
  }

  const submit = async () => {
    const c = await action.run(() => api<Connector>('POST', '/api/connectors/import', { document: doc, name, slug, credentials: mapped(creds) }))
    if (!c) return
    onClose()
    reset()
    navigate(`/connectors/${encodeURIComponent(c.id)}`)
  }

  return (
    <Modal
      open={open}
      title={t('cn.import.title')}
      onClose={() => {
        reset()
        onClose()
      }}
      footer={
        check && (
          <Button variant="primary" busy={action.busy} disabled={!name.trim() || !slug.trim()} onClick={submit}>
            {t('cn.import')}
          </Button>
        )
      }
    >
      <Field label={t('cn.import.file')} hint={t('cn.import.file.hint')}>
        {(id) => <FileInput id={id} accept="application/json,.json" onFile={(f) => void pick(f)} />}
      </Field>
      {fileError && <Flash kind="error" title={fileError} />}
      {check && (
        <>
          <p className="muted">{t('cn.import.summary', { nodes: check.nodes, samples: check.samples })}</p>
          <Field label={t('cn.field.name')}>{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} />}</Field>
          <Field label={t('cn.field.slug')}>{(id) => <Input id={id} className="cn-mono" value={slug} onChange={(e) => setSlug(e.target.value)} />}</Field>
          <SlotMapping slots={check.credentials} choices={check.choices} value={creds} onChange={setCreds} name={name} />
        </>
      )}
      <ErrorFlash error={action.error} strings={strings} />
    </Modal>
  )
}
