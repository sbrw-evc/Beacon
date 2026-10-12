import { useEffect, useState } from 'react'
import { Search } from 'lucide-react'
import { api } from '../../api'
import { ErrorBanner, ErrorFlash } from '../../connections/ConnectionCard'
import { mergeDicts } from '../../connections/connectionStrings'
import { useAction, useResource } from '../../connections/useRequest'
import { useT } from '../../i18n'
import { Button, Field, Input, Modal, Select, Switch } from '../../ui'
import { strings as ciStrings } from '../cis/strings'
import { strings as monStrings } from '../monitoring/strings'
import type { ServiceList } from '../services/types'
import { BulkResults, ciRows, type BulkCIsResult, type BulkHostsResult } from './BulkResults'
import { requestStrings, strings } from './strings'

const dialogStrings = mergeDicts(ciStrings, monStrings, strings, requestStrings)

export type BindAction = 'bind' | 'unbind'

// BindServicesDialog binds the chosen items to the services picked here, or unbinds them, and
// then shows what happened to every pair.
export function BindServicesDialog({
  action,
  ciIDs,
  onClose,
  onDone,
}: {
  action: BindAction | null
  ciIDs: string[]
  onClose: () => void
  onDone: () => void
}) {
  const t = useT(dialogStrings)
  const [q, setQ] = useState('')
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const [result, setResult] = useState<BulkCIsResult | null>(null)
  const act = useAction()
  const all = useResource<ServiceList>(action ? '/api/services' : '', 0)
  useEffect(() => {
    setQ('')
    setPicked(new Set())
    setResult(null)
    act.clear()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [action])
  const services = (all.data?.services ?? []).filter((s) => !q.trim() || s.name.toLowerCase().includes(q.trim().toLowerCase()))
  const toggle = (id: string) =>
    setPicked((s) => {
      const n = new Set(s)
      if (n.has(id)) n.delete(id)
      else n.add(id)
      return n
    })
  const run = () =>
    action &&
    void act.run(async () => {
      setResult(await api<BulkCIsResult>('POST', '/api/services/bulk/cis', { service_ids: [...picked], ci_ids: ciIDs, action }))
      onDone()
    })
  const n = ciIDs.length
  return (
    <Modal
      open={action !== null}
      title={action ? t(`bulk.${action}.title`, { n }) : ''}
      onClose={onClose}
      footer={
        result ? (
          <Button variant="primary" onClick={onClose}>
            {t('bulk.close')}
          </Button>
        ) : (
          <>
            <Button onClick={onClose}>{t('bulk.cancel')}</Button>
            <Button variant="primary" busy={act.busy} disabled={picked.size === 0} onClick={run}>
              {t(`bulk.run.${action ?? 'bind'}`)}
            </Button>
          </>
        )
      }
    >
      {action && !result && (
        <div className="stack">
          <p className="muted">{t(`bulk.${action}.text`)}</p>
          <label className="svc-search mon-link-search">
            <Search size={16} aria-hidden />
            <Input type="search" value={q} placeholder={t('bulk.services.search')} aria-label={t('bulk.services.search')} onChange={(e) => setQ(e.target.value)} />
          </label>
          <span className="muted">{t('bulk.services.chosen', { n: picked.size })}</span>
          <div className="bulk-options">
            {all.data && services.length === 0 && <p className="muted">{t('bulk.services.none')}</p>}
            {services.map((s) => (
              <label key={s.id} className={`bulk-option ${picked.has(s.id) ? 'active' : ''}`}>
                <input type="checkbox" checked={picked.has(s.id)} onChange={() => toggle(s.id)} />
                <span className="cn-name">{s.name}</span>
                {s.netbox && <span className="pill ci-source ci-source-netbox">NetBox</span>}
                <span className="muted">{s.ci_ids.length}</span>
              </label>
            ))}
          </div>
          <ErrorBanner error={all.error} strings={dialogStrings} />
          <ErrorFlash error={act.error} strings={dialogStrings} />
        </div>
      )}
      {result && <BulkResults rows={ciRows(result)} columns={[t('bulk.col.item'), t('bulk.col.service')]} summary={result.summary} />}
    </Modal>
  )
}

export type HostKey = { source_id: string; key: string }

const KINDS = ['device', 'vm', 'service', 'other'] as const

// CreateHostsDialog makes configuration items of the chosen hosts and shows what happened to
// every host.
export function CreateHostsDialog({ hosts, onClose, onDone }: { hosts: HostKey[] | null; onClose: () => void; onDone: () => void }) {
  const t = useT(dialogStrings)
  const [kind, setKind] = useState('device')
  const [register, setRegister] = useState(false)
  const [result, setResult] = useState<BulkHostsResult | null>(null)
  const act = useAction()
  useEffect(() => {
    setKind('device')
    setRegister(false)
    setResult(null)
    act.clear()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hosts])
  const registrable = kind === 'device' || kind === 'vm'
  const run = () =>
    hosts &&
    void act.run(async () => {
      setResult(await api<BulkHostsResult>('POST', '/api/monitoring/ci/bulk', { hosts, kind, register: register && registrable }))
      onDone()
    })
  return (
    <Modal
      open={hosts !== null}
      title={hosts ? t('bulk.hosts.title', { n: hosts.length }) : ''}
      onClose={onClose}
      footer={
        result ? (
          <Button variant="primary" onClick={onClose}>
            {t('bulk.close')}
          </Button>
        ) : (
          <>
            <Button onClick={onClose}>{t('bulk.cancel')}</Button>
            <Button variant="primary" busy={act.busy} onClick={run}>
              {t('bulk.hosts.run')}
            </Button>
          </>
        )
      }
    >
      {hosts && !result && (
        <div className="stack">
          <p className="muted">{t('bulk.hosts.text')}</p>
          <Field label={t('mon.create.kind')}>
            {(id) => (
              <Select id={id} value={kind} onChange={(e) => setKind(e.target.value)}>
                {KINDS.map((k) => (
                  <option key={k} value={k}>
                    {t(`mon.kind.${k}`)}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          {registrable && <Switch checked={register} onChange={setRegister} label={t('bulk.hosts.register')} />}
          <ErrorFlash error={act.error} strings={dialogStrings} />
        </div>
      )}
      {result && (
        <BulkResults
          rows={result.items.map((it) => ({
            key: `${it.source_id}/${it.key}`,
            cells: [it.host, it.source_name],
            result: it.result,
            error: it.error,
            detail: it.detail,
            note: it.ci ? `${it.ci.name} · ${it.ci.id}` : undefined,
          }))}
          columns={[t('bulk.col.host'), t('bulk.col.system')]}
          summary={result.summary}
        />
      )}
    </Modal>
  )
}
