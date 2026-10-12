import { useMemo, useState } from 'react'
import { Check, Copy, Download, Send } from 'lucide-react'
import { api } from '../../api'
import { ErrorBanner, ErrorFlash } from '../../connections/ConnectionCard'
import { mergeDicts } from '../../connections/connectionStrings'
import { useAction, useResource } from '../../connections/useRequest'
import { useLocale, useT } from '../../i18n'
import { Link, useRouter } from '../../router'
import { Banner, Button, Field, Input, Modal, Segmented } from '../../ui'
import { useSession } from '../session'
import { strings as connectorStrings } from './strings'
import { sourcesStrings } from './sourcesStrings'
import type { Connector, Preset } from './types'
import './sources.css'
import { Flash } from '../../notify'

const s = mergeDicts(connectorStrings, sourcesStrings)

export type QuickResult = {
  connector: Connector
  credential_id: string
  ingest_url: string
  token: string
  instructions: { preset: string; ingest_url: string; auth_header: string; snippet?: string; snippet_kind?: string; snippet_file?: string }
  attachment?: { name: string; kind?: string; content: string }
  mediatype_yaml?: string
  monitoring_id?: string
}

type QuickPreset = Preset & { quick: NonNullable<Preset['quick']> }

// useQuickPresets: the presets quick connect offers, in their order, as the server describes
// them; with a monitoring system kind, only those that can be its alert intake.
function useQuickPresets(open: boolean, kind?: string) {
  const presets = useResource<Preset[]>(open ? '/api/connectors/presets' : '', 0)
  const list = useMemo(
    () =>
      (presets.data ?? [])
        .filter((p): p is QuickPreset => !!p.quick && (!kind || p.quick.monitoring_kinds.includes(kind)))
        .sort((a, b) => a.quick.order - b.quick.order),
    [presets.data, kind],
  )
  return { list, loading: !presets.data && !presets.error, error: presets.error }
}

// presetText is the web string of a preset when there is one, otherwise the server's text.
function presetText(t: (k: string) => string, key: string, fallback: string) {
  const v = t(key)
  return v === key ? fallback : v
}

// canQuickConnect: quick connect makes a credential and publishes a connector.
export function useCanQuickConnect() {
  const { can } = useSession()
  return can('connectors:edit') && can('connectors:publish') && can('credentials:edit')
}

// SourcesExplainer is the one line that says which page does what for a monitoring system.
export function SourcesExplainer() {
  const t = useT(s)
  return (
    <p className="muted src-explain">
      {t('src.explain')} <Link to="/connectors">{t('src.explain.connectors')}</Link> · <Link to="/monitoring">{t('src.explain.monitoring')}</Link> ·{' '}
      <Link to="/rules">{t('src.explain.rules')}</Link>
    </p>
  )
}

export function CopyField({ value, label, mono = true }: { value: string; label: string; mono?: boolean }) {
  const t = useT(s)
  const [copied, setCopied] = useState(false)
  const copy = () => {
    void navigator.clipboard?.writeText(value).then(() => {
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    })
  }
  return (
    <Field label={label}>
      {(id) => (
        <div className="row src-copy">
          <Input id={id} readOnly value={value} className={mono ? 'cn-mono' : ''} onFocus={(e) => e.currentTarget.select()} />
          <Button onClick={copy} title={t('src.copy')}>
            {copied ? <Check size={15} aria-hidden /> : <Copy size={15} aria-hidden />}
            {copied ? t('src.copied') : t('src.copy')}
          </Button>
        </div>
      )}
    </Field>
  )
}

function Snippet({ text, file, kind }: { text: string; file?: string; kind?: string }) {
  const t = useT(s)
  const download = () => {
    const url = URL.createObjectURL(new Blob([text], { type: kind === 'yaml' ? 'text/yaml' : 'text/plain' }))
    const a = document.createElement('a')
    a.href = url
    a.download = file ?? 'umbrella.txt'
    a.click()
    URL.revokeObjectURL(url)
  }
  return (
    <div className="src-snippet">
      <pre className="cn-mono">{text}</pre>
      <div className="row">
        <Button onClick={() => void navigator.clipboard?.writeText(text)}>
          <Copy size={15} aria-hidden />
          {t('src.copy')}
        </Button>
        {file && (
          <Button onClick={download}>
            <Download size={15} aria-hidden />
            {t('src.download')}
          </Button>
        )}
      </div>
    </div>
  )
}

// QuickResultView shows what the source needs, once: address, token, steps and the ready
// configuration, then the test event. The preset says how many steps there are; their text is
// in the strings.
export function QuickResultView({ r, steps }: { r: QuickResult; steps: number }) {
  const t = useT(s)
  const p = r.instructions.preset
  const attachment = r.attachment ?? (r.mediatype_yaml ? { name: 'umbrella-mediatype.yaml', kind: 'yaml', content: r.mediatype_yaml } : undefined)
  return (
    <div className="stack">
      <Banner kind="ok" title={t('src.connect.done', { name: r.connector.name })} />
      <CopyField label={t('src.url')} value={r.ingest_url} />
      <CopyField label={t('src.token')} value={r.token} />
      <p className="hint">{t('src.token.once')}</p>
      <h3 className="src-h">{t('src.steps')}</h3>
      <ol className="src-steps">
        {Array.from({ length: steps }, (_, i) => (
          <li key={i}>{t(`src.steps.${p}.${i + 1}`)}</li>
        ))}
      </ol>
      {attachment && <Snippet text={attachment.content} file={attachment.name} kind={attachment.kind} />}
      {r.instructions.snippet && <Snippet text={r.instructions.snippet} file={r.instructions.snippet_file || undefined} kind={r.instructions.snippet_kind} />}
      <TestEvent connectorID={r.connector.id} />
    </div>
  )
}

// QuickConnectDialog: «Подключить источник». With a monitoring system the connector becomes the
// system's alert intake and the templates offered are those made for the system kind.
export function QuickConnectDialog({
  open,
  onClose,
  onDone,
  monitoring,
}: {
  open: boolean
  onClose: () => void
  onDone?: (r: QuickResult) => void
  monitoring?: { id: string; name: string; kind: string }
}) {
  const t = useT(s)
  const { locale } = useLocale()
  const { navigate } = useRouter()
  const presets = useQuickPresets(open, monitoring?.kind)
  const [picked, setPicked] = useState('')
  const [name, setName] = useState('')
  const [result, setResult] = useState<QuickResult | null>(null)
  const action = useAction()
  const choices = presets.list
  const current = choices.find((p) => p.id === picked) ?? choices[0]
  const label = (p: QuickPreset) => presetText(t, `src.preset.${p.id}`, p.title[locale])
  const close = () => {
    setResult(null)
    setName('')
    action.clear()
    onClose()
  }
  const submit = async () => {
    if (!current) return
    const r = await action.run(() =>
      api<QuickResult>('POST', '/api/connectors/quick', { preset: current.id, name: name.trim(), monitoring_id: monitoring?.id ?? '' }),
    )
    if (r) {
      setResult(r)
      onDone?.(r)
    }
  }
  return (
    <Modal
      open={open}
      title={monitoring ? `${t('src.connect.title')}: ${monitoring.name}` : t('src.connect.title')}
      onClose={close}
      footer={
        result ? (
          <>
            <Button onClick={() => (close(), navigate(`/connectors/${encodeURIComponent(result.connector.id)}`))}>{t('src.open')}</Button>
            <Button variant="primary" onClick={close}>
              {t('src.close')}
            </Button>
          </>
        ) : (
          <>
            <Button variant="ghost" onClick={close}>
              {t('src.connect.cancel')}
            </Button>
            <Button variant="primary" busy={action.busy} disabled={!current} onClick={() => void submit()}>
              {t('src.connect.go')}
            </Button>
          </>
        )
      }
    >
      {result ? (
        <QuickResultView r={result} steps={choices.find((p) => p.id === result.instructions.preset)?.quick.steps ?? 0} />
      ) : (
        <>
          <p className="muted">{t('src.connect.hint')}</p>
          {presets.loading && <p className="muted">{t('loading')}</p>}
          {choices.length > 1 && current && (
            <Segmented
              label={t('src.connect.preset')}
              value={current.id}
              onChange={setPicked}
              options={choices.map((p) => ({ value: p.id, label: label(p) }))}
            />
          )}
          {current && <p className="hint">{presetText(t, `src.preset.${current.id}.hint`, current.description[locale])}</p>}
          <Field label={t('src.connect.name')} hint={t('src.connect.name.hint')}>
            {(id) => (
              <Input
                id={id}
                value={name}
                placeholder={monitoring?.name ?? (current ? current.quick.name[locale] : '')}
                onChange={(e) => setName(e.target.value)}
              />
            )}
          </Field>
          <ErrorBanner error={presets.error} strings={s} />
          <ErrorFlash error={action.error} strings={s} />
        </>
      )}
    </Modal>
  )
}

type TestResult = { incident_id: string; request_id: string; status: string; error?: string }
type Ref = { id: string; name: string }
type Incident = {
  id: string
  ci_id?: string
  ci_name: string
  route: { services: Ref[]; team?: Ref; people: { name: string }[]; owners: { name: string }[]; via: string }
}

// TestEvent sends a test event through a published connector and shows the incident and its
// route.
export function TestEvent({ connectorID }: { connectorID: string }) {
  const t = useT(s)
  const [ci, setCI] = useState('')
  const [res, setRes] = useState<TestResult | null>(null)
  const [inc, setInc] = useState<Incident | null>(null)
  const action = useAction()
  const send = async () => {
    setRes(null)
    setInc(null)
    const r = await action.run(async () => {
      const r = await api<TestResult>('POST', `/api/connectors/${encodeURIComponent(connectorID)}/test-event`, { ci: ci.trim() })
      if (r.incident_id) {
        try {
          const v = await api<{ alert: Incident }>('GET', `/api/incidents/${encodeURIComponent(r.incident_id)}`)
          setInc(v.alert)
        } catch {
          // The incident page may be closed to this user; the ID is still shown.
        }
      }
      return r
    })
    if (r) setRes(r)
  }
  const people = inc ? (inc.route.people.length > 0 ? inc.route.people : inc.route.owners) : []
  return (
    <div className="card src-test stack">
      <p className="muted">{t('src.test.hint')}</p>
      <Field label={t('src.test.ci')} hint={t('src.test.ci.hint')}>
        {(id) => <Input id={id} value={ci} placeholder="umbrella-test" onChange={(e) => setCI(e.target.value)} />}
      </Field>
      <div className="row">
        <Button busy={action.busy} onClick={() => void send()}>
          <Send size={15} aria-hidden />
          {t('src.test')}
        </Button>
      </div>
      <ErrorFlash error={action.error} strings={s} />
      {res && res.status === 'failed' && <Flash kind="error" title={t('src.test.failed')} trigger={res}>{res.error}</Flash>}
      {res && res.status !== 'failed' && !res.incident_id && <Flash kind="info" title={t('src.test.queued')} trigger={res} />}
      {res && res.incident_id && (
        <Flash kind="ok" title={t('src.test.sent', { id: res.incident_id })} link={`/incidents?id=${encodeURIComponent(res.incident_id)}`} trigger={res}>
          {inc && (
            <ul className="src-route">
              <li>{inc.ci_id ? t('src.test.route.ci', { ci: inc.ci_name }) : t('src.test.route.unknown', { ci: inc.ci_name })}</li>
              {inc.route.services.length > 0 && <li>{t('src.test.route.services', { services: inc.route.services.map((x) => x.name).join(', ') })}</li>}
              {inc.route.team && <li>{t('src.test.route.team', { team: inc.route.team.name })}</li>}
              <li>{people.length > 0 ? t('src.test.route.people', { people: people.map((p) => p.name).join(', ') }) : t('src.test.route.nobody')}</li>
            </ul>
          )}
        </Flash>
      )}
    </div>
  )
}
