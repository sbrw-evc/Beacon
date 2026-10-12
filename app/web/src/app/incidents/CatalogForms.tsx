import { useEffect, useState } from 'react'
import { Search } from 'lucide-react'
import { api } from '../../api'
import { ErrorFlash } from '../../connections/ConnectionCard'
import { useAction, useResource } from '../../connections/useRequest'
import { useT } from '../../i18n'
import { Banner, Button, Field, Input, Modal, Select, Textarea } from '../../ui'
import { mergeDicts } from '../../connections/connectionStrings'
import { strings as ciStrings } from '../cis/strings'
import { KINDS, type CIList } from '../cis/types'
import { useSession } from '../session'
import { strings } from './strings'
import { firing, type Incident } from './types'

const errStrings = mergeDicts(ciStrings, strings)

// The forms the incident card switches to: create the item the incident waits for, name an
// existing item, and confirm a manual resolve.

type Done = { bound: string[] }

const IPV4 = /^(\d{1,3}(?:\.\d{1,3}){3})(?::\d+)?$/
const IPV6 = /^\[([0-9a-fA-F:]+)\](?::\d+)?$/

// guessIPs picks the addresses the event gives: the item name itself or label values such as
// instance=10.0.0.5:9100.
export function guessIPs(a: Incident) {
  const out: string[] = []
  for (const v of [a.ci_name, ...Object.values(a.labels ?? {})]) {
    const m = IPV4.exec(v.trim()) ?? IPV6.exec(v.trim())
    if (m && !out.includes(m[1])) out.push(m[1])
  }
  return out.slice(0, 20)
}

type ServiceRef = { id: string; name: string }

export function CreateCIForm({ a, onCancel, onDone }: { a: Incident; onCancel: () => void; onDone: (d: Done) => void }) {
  const t = useT(strings)
  const { can } = useSession()
  const binder = can('services:edit') && can('services:view')
  const [name, setName] = useState(a.ci_name)
  const [kind, setKind] = useState<string>('other')
  const [ips, setIps] = useState(() => guessIPs(a).join(', '))
  const [description, setDescription] = useState(() => t('inc.ci.description', { id: a.id }))
  const [service, setService] = useState('')
  const services = useResource<{ services: ServiceRef[] }>(binder ? '/api/services' : '', 0)
  const save = useAction()
  const submit = () =>
    void save.run(async () => {
      const out = await api<Done>('POST', `/api/incidents/${encodeURIComponent(a.id)}/create-ci`, {
        name: name.trim(),
        kind,
        description: description.trim(),
        ips: ips
          .split(/[\s,;]+/)
          .map((x) => x.trim())
          .filter(Boolean),
        service_id: service,
      })
      onDone(out)
    })
  const list = [...(services.data?.services ?? [])].sort((x, y) => x.name.localeCompare(y.name))
  return (
    <div className="stack inc-form">
      <h3>{t('inc.ci.create.title')}</h3>
      <p className="muted">{t('inc.ci.create.text', { name: a.ci_name })}</p>
      <Field label={t('inc.ci.name')} hint={name.trim() !== a.ci_name ? t('inc.ci.name.alias', { name: a.ci_name }) : undefined}>
        {(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} />}
      </Field>
      <Field label={t('inc.ci.kind')}>
        {(id) => (
          <Select id={id} value={kind} onChange={(e) => setKind(e.target.value)}>
            {KINDS.map((k) => (
              <option key={k} value={k}>
                {t(`inc.ci.kind.${k}`)}
              </option>
            ))}
          </Select>
        )}
      </Field>
      <Field label={t('inc.ci.ips')} optional={t('inc.optional')}>
        {(id) => <Input id={id} value={ips} placeholder="10.0.0.5" onChange={(e) => setIps(e.target.value)} />}
      </Field>
      {binder && (
        <Field label={t('inc.ci.service')} hint={t('inc.ci.service.hint')} optional={t('inc.optional')}>
          {(id) => (
            <Select id={id} value={service} onChange={(e) => setService(e.target.value)}>
              <option value="">{t('inc.ci.service.none')}</option>
              {list.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </Select>
          )}
        </Field>
      )}
      <Field label={t('inc.ci.descr')} optional={t('inc.optional')}>
        {(id) => <Textarea id={id} rows={2} value={description} onChange={(e) => setDescription(e.target.value)} />}
      </Field>
      <ErrorFlash error={save.error} strings={errStrings} />
      <div className="row inc-form-actions">
        <Button onClick={onCancel}>{t('inc.cancel')}</Button>
        <Button variant="primary" busy={save.busy} disabled={!name.trim()} onClick={submit}>
          {t('inc.ci.create')}
        </Button>
      </div>
    </div>
  )
}

function useDebounced<T>(value: T, ms: number) {
  const [v, setV] = useState(value)
  useEffect(() => {
    const id = window.setTimeout(() => setV(value), ms)
    return () => window.clearTimeout(id)
  }, [value, ms])
  return v
}

export function BindCIForm({ a, onCancel, onDone }: { a: Incident; onCancel: () => void; onDone: (d: Done) => void }) {
  const t = useT(strings)
  const [q, setQ] = useState('')
  const [chosen, setChosen] = useState<{ id: string; name: string } | null>(null)
  const query = useDebounced(q.trim(), 250)
  const found = useResource<CIList>(query ? `/api/cis?q=${encodeURIComponent(query)}` : '', 0)
  const save = useAction()
  const items = query ? (found.data?.items ?? []).slice(0, 20) : []
  const submit = () =>
    chosen &&
    void save.run(async () => {
      onDone(await api<Done>('POST', `/api/incidents/${encodeURIComponent(a.id)}/bind-ci`, { ci_id: chosen.id }))
    })
  return (
    <div className="stack inc-form">
      <h3>{t('inc.ci.bind.title')}</h3>
      <p className="muted">{t('inc.ci.bind.text', { name: a.ci_name })}</p>
      <Field label={t('inc.ci.bind.search')}>
        {(id) => (
          <div className="mw-search">
            <Search size={16} className="mw-search-icon" aria-hidden />
            <Input id={id} type="search" value={q} placeholder={t('inc.ci.bind.ph')} onChange={(e) => setQ(e.target.value)} />
          </div>
        )}
      </Field>
      {query && found.data && items.length === 0 && <p className="muted">{t('inc.ci.bind.nothing')}</p>}
      {items.length > 0 && (
        <ul className="inc-pick" role="listbox" aria-label={t('inc.ci.bind.search')}>
          {items.map((ci) => (
            <li key={ci.id}>
              <button
                type="button"
                role="option"
                aria-selected={chosen?.id === ci.id}
                className={`inc-pick-item ${chosen?.id === ci.id ? 'on' : ''}`}
                onClick={() => setChosen({ id: ci.id, name: ci.name })}
              >
                <span className="cn-name">{ci.name}</span>
                <span className="muted">
                  {' · '}
                  {t(`inc.ci.kind.${ci.kind}`)}
                  {ci.ips.length > 0 && ` · ${ci.ips.slice(0, 2).join(', ')}`}
                  {ci.services.length > 0 && ` · ${ci.services.map((s) => s.name).join(', ')}`}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
      {chosen && <Banner kind="info" title={t('inc.ci.bind.effect', { name: a.ci_name, ci: chosen.name })} />}
      <ErrorFlash error={save.error} strings={errStrings} />
      <div className="row inc-form-actions">
        <Button onClick={onCancel}>{t('inc.cancel')}</Button>
        <Button variant="primary" busy={save.busy} disabled={!chosen} onClick={() => void submit()}>
          {t('inc.ci.bind')}
        </Button>
      </div>
    </div>
  )
}

// ResolveConfirm asks before a manual resolve and warns when sources still fire: the incident
// opens again if their event repeats within the reopen window. An optional comment goes first.
export function ResolveConfirm({ a, busy, onCancel, onResolve }: { a: Incident; busy: boolean; onCancel: () => void; onResolve: (comment: string) => void }) {
  const t = useT(strings)
  const [text, setText] = useState('')
  const n = firing(a)
  return (
    <div className="stack inc-form">
      <h3>{t('inc.resolve.title', { id: a.id })}</h3>
      {n > 0 ? <Banner kind="warn" title={t('inc.resolve.firing', { n })}>{t('inc.resolve.firing.text')}</Banner> : <p className="muted">{t('inc.resolve.calm')}</p>}
      <Field label={t('inc.comment')} optional={t('inc.optional')}>
        {(id) => <Textarea id={id} rows={2} value={text} placeholder={t('inc.resolve.comment.ph')} onChange={(e) => setText(e.target.value)} />}
      </Field>
      <div className="row inc-form-actions">
        <Button onClick={onCancel}>{t('inc.cancel')}</Button>
        <Button variant="primary" busy={busy} onClick={() => onResolve(text.trim())}>
          {t('inc.resolve')}
        </Button>
      </div>
    </div>
  )
}

// BulkConfirm asks before acknowledging or resolving several incidents; for a resolve it says how
// many of them still have firing sources.
export function BulkConfirm({
  action,
  selected,
  busy,
  onCancel,
  onConfirm,
}: {
  action: 'ack' | 'resolve' | null
  selected: Incident[]
  busy: boolean
  onCancel: () => void
  onConfirm: () => void
}) {
  const t = useT(strings)
  const hot = selected.filter((a) => firing(a) > 0).length
  return (
    <Modal
      open={action !== null}
      title={t(action === 'resolve' ? 'inc.bulk.resolve.title' : 'inc.bulk.ack.title', { n: selected.length })}
      onClose={onCancel}
      footer={
        <>
          <Button onClick={onCancel}>{t('inc.cancel')}</Button>
          <Button variant="primary" busy={busy} onClick={onConfirm}>
            {action === 'resolve' ? t('inc.resolve') : t('inc.ack')}
          </Button>
        </>
      }
    >
      {action === 'resolve' && hot > 0 && <Banner kind="warn" title={t('inc.bulk.resolve.firing', { n: hot })} />}
      <ul className="inc-bulk-list">
        {selected.slice(0, 12).map((a) => (
          <li key={a.id}>
            <code>{a.id}</code> {a.title} <span className="muted">· {a.ci_name}</span>
          </li>
        ))}
        {selected.length > 12 && <li className="muted">+{selected.length - 12}</li>}
      </ul>
    </Modal>
  )
}
