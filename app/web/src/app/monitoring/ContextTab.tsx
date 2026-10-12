import { useEffect, useState } from 'react'
import { ArrowUp, Plus, RotateCcw, X } from 'lucide-react'
import { api } from '../../api'
import { ErrorBanner, ErrorFlash } from '../../connections/ConnectionCard'
import { useAction, useResource } from '../../connections/useRequest'
import { useT } from '../../i18n'
import { Button, Field, Input, Modal, Segmented, Select, Stepper, Switch, Textarea } from '../../ui'
import { useSession } from '../session'
import { contextStrings } from './contextStrings'
import { panelTitle } from '../incidents/Chart'
import { Flash } from '../../notify'
import { ask } from '../../confirm'

type LogKind = 'loki' | 'opensearch' | 'graylog'
type Panel = { id: string; title: string; unit: string; promql: string; zabbix_key: string }
type Settings = { window_minutes: number; log_limit: number; panels: Panel[] }
type LogSource = {
  id: string
  name: string
  kind: LogKind
  url: string
  credential_id?: string
  credential_name?: string
  skip_verify: boolean
  enabled: boolean
  query?: string
  index?: string
  host_field?: string
  message_field?: string
  time_field?: string
  level_field?: string
}
type Defaults = {
  window_minutes: number
  log_limit: number
  panels: Panel[]
  loki_query: string
  index: string
  host_field: string
  message_field: string
  time_field: string
  level_field: string
  graylog?: { host_field: string; message_field: string; level_field: string }
}
type View = { settings: Settings; defaults: Defaults; log_sources: LogSource[] }
type Credential = { id: string; name: string; type: string }
type Report = { ok: boolean; error?: string; query?: string; lines: { at: string; level?: string; text: string }[] }

// ContextTab sets up the Machine tab of incidents: the default window, the graphs and the log
// sources.
export function ContextTab() {
  const t = useT(contextStrings)
  const { can } = useSession()
  const [epoch, setEpoch] = useState(0)
  const view = useResource<View>('/api/host-context', epoch)
  const [editing, setEditing] = useState<LogSource | 'new' | null>(null)
  const v = view.data
  const editable = can('monitoring:edit')
  return (
    <div className="stack">
      <p className="muted ctx-explain">{t('ctx.explain')}</p>
      <ErrorBanner error={view.error} strings={contextStrings} />
      {v && <SettingsCard key={epoch} v={v} editable={editable} onSaved={() => setEpoch((e) => e + 1)} />}
      {v && (
        <section className="card ctx-card stack">
          <div className="row ctx-head">
            <div>
              <h3>{t('ctx.logs')}</h3>
              <p className="hint">{t('ctx.logs.hint')}</p>
            </div>
            {editable && (
              <Button variant="primary" onClick={() => setEditing('new')}>
                <Plus size={16} aria-hidden />
                {t('ctx.logs.new')}
              </Button>
            )}
          </div>
          {v.log_sources.length === 0 ? (
            <p className="muted">{t('ctx.logs.empty')}</p>
          ) : (
            <table className="cn-table compact">
              <thead>
                <tr>
                  <th>{t('ctx.logs.col.name')}</th>
                  <th>{t('ctx.kind')}</th>
                  <th>{t('ctx.logs.col.url')}</th>
                </tr>
              </thead>
              <tbody>
                {v.log_sources.map((s) => (
                  <tr key={s.id}>
                    <td>
                      {editable ? (
                        <button type="button" className="cn-link" onClick={() => setEditing(s)}>
                          {s.name}
                        </button>
                      ) : (
                        s.name
                      )}
                      {!s.enabled && <span className="pill pill-off ctx-off">{t('ctx.logs.off')}</span>}
                    </td>
                    <td>{t(`ctx.kind.${s.kind}`)}</td>
                    <td className="cn-mono">{s.url}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </section>
      )}
      {v && <LogEditor value={editing} defaults={v.defaults} onClose={() => setEditing(null)} onSaved={() => (setEditing(null), setEpoch((e) => e + 1))} />}
    </div>
  )
}

function SettingsCard({ v, editable, onSaved }: { v: View; editable: boolean; onSaved: () => void }) {
  const t = useT(contextStrings)
  // Standard graphs get their titles in the language of the interface.
  const local = (list: Panel[]) => list.map((p) => ({ ...p, title: panelTitle(t, p) }))
  const [s, setS] = useState<Settings>(() => ({ ...v.settings, panels: local(v.settings.panels) }))
  const [saved, setSaved] = useState(false)
  const save = useAction()
  const set = (patch: Partial<Settings>) => {
    setS({ ...s, ...patch })
    setSaved(false)
  }
  const setPanel = (i: number, patch: Partial<Panel>) => set({ panels: s.panels.map((p, j) => (j === i ? { ...p, ...patch } : p)) })
  const move = (i: number) => {
    const next = [...s.panels]
    ;[next[i - 1], next[i]] = [next[i], next[i - 1]]
    set({ panels: next })
  }
  const submit = () =>
    save.run(async () => {
      await api('PUT', '/api/host-context', s)
      setSaved(true)
      onSaved()
    })
  return (
    <section className="card ctx-card stack">
      <h3>{t('ctx.settings')}</h3>
      <div className="rl-grid">
        <Field label={t('ctx.window')} hint={t('ctx.window.hint')}>
          {(id) => (
            <div className="nb-stepper">
              <Stepper id={id} value={s.window_minutes} min={1} max={10080} suffix={t('ctx.minutes')} onChange={(window_minutes) => set({ window_minutes })} />
            </div>
          )}
        </Field>
        <Field label={t('ctx.loglimit')} hint={t('ctx.loglimit.hint')}>
          {(id) => (
            <div className="nb-stepper">
              <Stepper id={id} value={s.log_limit} min={10} max={2000} suffix={t('ctx.lines')} onChange={(log_limit) => set({ log_limit })} />
            </div>
          )}
        </Field>
      </div>
      <div>
        <h4 className="ctx-sub">{t('ctx.panels')}</h4>
        <p className="hint">{t('ctx.panels.hint')}</p>
      </div>
      {s.panels.length === 0 && <p className="muted">{t('ctx.panels.none')}</p>}
      <ol className="ctx-panels">
        {s.panels.map((p, i) => (
          <li key={p.id || i} className="ctx-panel">
            <div className="ctx-panel-row">
              <Input value={p.title} aria-label={t('ctx.panel.title')} placeholder={t('ctx.panel.title')} disabled={!editable} onChange={(e) => setPanel(i, { title: e.target.value })} />
              <Input
                className="ctx-unit"
                value={p.unit}
                aria-label={t('ctx.panel.unit')}
                placeholder={t('ctx.panel.unit')}
                title={t('ctx.panel.unit.hint')}
                disabled={!editable}
                onChange={(e) => setPanel(i, { unit: e.target.value })}
              />
              {editable && (
                <>
                  <button type="button" className="icon-btn" aria-label={t('ctx.panel.up')} title={t('ctx.panel.up')} disabled={i === 0} onClick={() => move(i)}>
                    <ArrowUp size={15} aria-hidden />
                  </button>
                  <button
                    type="button"
                    className="icon-btn"
                    aria-label={t('ctx.panel.remove')}
                    title={t('ctx.panel.remove')}
                    onClick={() => set({ panels: s.panels.filter((_, j) => j !== i) })}
                  >
                    <X size={15} aria-hidden />
                  </button>
                </>
              )}
            </div>
            <span className="ctx-label">{t('ctx.panel.promql')}</span>
            <Textarea
              rows={2}
              spellCheck={false}
              value={p.promql}
              aria-label={t('ctx.panel.promql')}
              placeholder={t('ctx.panel.promql')}
              disabled={!editable}
              onChange={(e) => setPanel(i, { promql: e.target.value })}
            />
            <span className="ctx-label">{t('ctx.panel.zabbix')}</span>
            <Input
              className="cn-mono"
              spellCheck={false}
              value={p.zabbix_key}
              aria-label={t('ctx.panel.zabbix')}
              placeholder={t('ctx.panel.zabbix')}
              disabled={!editable}
              onChange={(e) => setPanel(i, { zabbix_key: e.target.value })}
            />
          </li>
        ))}
      </ol>
      {editable && (
        <div className="row ctx-actions">
          <Button disabled={s.panels.length >= 20} onClick={() => set({ panels: [...s.panels, { id: '', title: '', unit: '', promql: '', zabbix_key: '' }] })}>
            <Plus size={16} aria-hidden />
            {t('ctx.panel.add')}
          </Button>
          <Button variant="ghost" onClick={async () => (await ask({ text: t('ctx.panels.defaults.confirm') })) && set({ panels: local(v.defaults.panels) })}>
            <RotateCcw size={15} aria-hidden />
            {t('ctx.panels.defaults')}
          </Button>
          <span className="ctx-spacer" />
          {saved && <span className="muted">{t('ctx.saved')}</span>}
          <Button variant="primary" busy={save.busy} onClick={() => void submit()}>
            {t('ctx.save')}
          </Button>
        </div>
      )}
      <ErrorFlash error={save.error} strings={contextStrings} />
    </section>
  )
}

type Draft = {
  name: string
  kind: LogKind
  url: string
  credential_id: string
  skip_verify: boolean
  enabled: boolean
  query: string
  index: string
  host_field: string
  message_field: string
  time_field: string
  level_field: string
}

const urlPlaceholders: Record<LogKind, string> = {
  loki: 'http://loki:3100',
  opensearch: 'https://opensearch:9200',
  graylog: 'https://graylog.example.com',
}

const blank = (): Draft => ({
  name: '',
  kind: 'loki',
  url: '',
  credential_id: '',
  skip_verify: false,
  enabled: true,
  query: '',
  index: '',
  host_field: '',
  message_field: '',
  time_field: '',
  level_field: '',
})

function LogEditor({ value, defaults, onClose, onSaved }: { value: LogSource | 'new' | null; defaults: Defaults; onClose: () => void; onSaved: () => void }) {
  const t = useT(contextStrings)
  const { can } = useSession()
  const editing = value && value !== 'new' ? value : null
  const [d, setD] = useState<Draft>(blank())
  const [host, setHost] = useState('')
  const [report, setReport] = useState<Report | null>(null)
  const creds = useResource<Credential[]>(value && can('credentials:view') ? '/api/credentials' : '', 0)
  const save = useAction()
  const test = useAction()
  useEffect(() => {
    setD(
      editing
        ? {
            name: editing.name,
            kind: editing.kind,
            url: editing.url,
            credential_id: editing.credential_id ?? '',
            skip_verify: editing.skip_verify,
            enabled: editing.enabled,
            query: editing.query ?? '',
            index: editing.index ?? '',
            host_field: editing.host_field ?? '',
            message_field: editing.message_field ?? '',
            time_field: editing.time_field ?? '',
            level_field: editing.level_field ?? '',
          }
        : blank(),
    )
    setReport(null)
    save.clear()
    test.clear()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value])
  const set = (patch: Partial<Draft>) => {
    setD({ ...d, ...patch })
    setReport(null)
  }
  const usable = (creds.data ?? []).filter((c) => ['bearer', 'basic', 'header'].includes(c.type))
  const submit = () =>
    save.run(async () => {
      await api(editing ? 'PUT' : 'POST', editing ? `/api/host-context/logs/${editing.id}` : '/api/host-context/logs', d)
      onSaved()
    })
  const check = () =>
    test.run(async () => {
      setReport(await api<Report>('POST', '/api/host-context/logs/test', { ...d, host }))
    })
  const remove = async () =>
    editing &&
    (await ask({ text: t('ctx.delete.confirm', { name: editing.name }), danger: true })) &&
    save.run(async () => {
      await api('DELETE', `/api/host-context/logs/${editing.id}`)
      onSaved()
    })
  // Graylog reads its own fields and streams instead of an index.
  const placeholder = (key: 'index' | 'host_field' | 'message_field' | 'time_field' | 'level_field') => {
    if (d.kind !== 'graylog') return defaults[key]
    if (key === 'host_field' || key === 'message_field' || key === 'level_field') return defaults.graylog?.[key] ?? ''
    return ''
  }
  const field = (key: 'index' | 'host_field' | 'message_field' | 'time_field' | 'level_field', label: string, hint?: string) => (
    <Field label={t(label)} hint={hint && t(hint)}>
      {(id) => (
        <Input
          id={id}
          value={d[key]}
          spellCheck={false}
          placeholder={placeholder(key)}
          onChange={(e) => set({ [key]: e.target.value })}
        />
      )}
    </Field>
  )
  return (
    <Modal
      open={value !== null}
      title={editing ? t('ctx.dialog.edit') : t('ctx.dialog.new')}
      onClose={onClose}
      footer={
        <>
          {editing && (
            <Button variant="ghost" busy={save.busy} onClick={() => void remove()}>
              {t('ctx.delete')}
            </Button>
          )}
          <Button onClick={onClose}>{t('ctx.cancel')}</Button>
          <Button variant="primary" busy={save.busy} onClick={() => void submit()}>
            {t('ctx.save')}
          </Button>
        </>
      }
    >
      <div className="stack">
        <Segmented
          label={t('ctx.kind')}
          value={d.kind}
          onChange={(kind) => set({ kind, query: '', index: '', host_field: '', message_field: '', time_field: '', level_field: '' })}
          options={[
            { value: 'loki', label: t('ctx.kind.loki') },
            { value: 'opensearch', label: t('ctx.kind.opensearch') },
            { value: 'graylog', label: t('ctx.kind.graylog') },
          ]}
        />
        <Field label={t('ctx.name')}>{(id) => <Input id={id} value={d.name} placeholder={t(`ctx.kind.${d.kind}`)} onChange={(e) => set({ name: e.target.value })} />}</Field>
        <Field label={t('ctx.url')} hint={t(`ctx.url.${d.kind}.hint`)}>
          {(id) => (
            <Input
              id={id}
              value={d.url}
              spellCheck={false}
              autoComplete="off"
              placeholder={urlPlaceholders[d.kind]}
              onChange={(e) => set({ url: e.target.value })}
            />
          )}
        </Field>
        <Field label={t('ctx.cred')} hint={usable.length === 0 && creds.data ? t('ctx.cred.missing') : t('ctx.cred.hint')}>
          {(id) => (
            <Select id={id} value={d.credential_id} onChange={(e) => set({ credential_id: e.target.value })}>
              <option value="">{t('ctx.cred.none')}</option>
              {editing?.credential_id && !usable.some((c) => c.id === editing.credential_id) && (
                <option value={editing.credential_id}>{editing.credential_name ?? editing.credential_id}</option>
              )}
              {usable.map((c) => (
                <option key={c.id} value={c.id}>
                  {`${c.name} (${c.type})`}
                </option>
              ))}
            </Select>
          )}
        </Field>
        {d.kind === 'loki' ? (
          <Field label={t('ctx.query')} hint={t('ctx.query.hint')}>
            {(id) => <Input id={id} className="cn-mono" value={d.query} spellCheck={false} placeholder={defaults.loki_query} onChange={(e) => set({ query: e.target.value })} />}
          </Field>
        ) : d.kind === 'graylog' ? (
          <>
            <Field label={t('ctx.gquery')} hint={t('ctx.gquery.hint')}>
              {(id) => <Input id={id} className="cn-mono" value={d.query} spellCheck={false} placeholder="NOT level:7" onChange={(e) => set({ query: e.target.value })} />}
            </Field>
            {field('index', 'ctx.streams', 'ctx.streams.hint')}
            <div className="rl-grid">
              {field('host_field', 'ctx.hostfield')}
              {field('message_field', 'ctx.msgfield')}
              {field('level_field', 'ctx.levelfield')}
            </div>
          </>
        ) : (
          <>
            {field('index', 'ctx.index', 'ctx.index.hint')}
            <div className="rl-grid">
              {field('host_field', 'ctx.hostfield')}
              {field('message_field', 'ctx.msgfield')}
              {field('time_field', 'ctx.timefield')}
              {field('level_field', 'ctx.levelfield')}
            </div>
          </>
        )}
        {d.url.trim().startsWith('https') && <Switch checked={d.skip_verify} onChange={(skip_verify) => set({ skip_verify })} label={t('ctx.skip')} />}
        <Switch checked={d.enabled} onChange={(enabled) => set({ enabled })} label={t('ctx.enabled')} hint={t('ctx.enabled.hint')} />
        {can('monitoring:test') && (
          <div className="ctx-test">
            <Field label={t('ctx.test.host')} hint={t('ctx.test.host.hint')}>
              {(id) => <Input id={id} value={host} spellCheck={false} placeholder="web-01" onChange={(e) => (setHost(e.target.value), setReport(null))} />}
            </Field>
            <Button busy={test.busy} disabled={!host.trim()} onClick={() => void check()}>
              {t('ctx.test')}
            </Button>
          </div>
        )}
        {report &&
          (report.ok ? (
            <Flash kind="ok" title={t('ctx.test.ok', { n: report.lines.length })} trigger={report}>
              {report.query && <div className="cn-mono ctx-query">{report.query}</div>}
              {report.lines.slice(0, 5).map((l, i) => (
                <div key={i} className="cn-mono ctx-line">
                  {l.text}
                </div>
              ))}
            </Flash>
          ) : (
            <Flash kind="error" title={t('ctx.test.fail')} trigger={report}>
              {report.error}
            </Flash>
          ))}
        <ErrorFlash error={test.error ?? save.error} strings={contextStrings} />
      </div>
    </Modal>
  )
}
