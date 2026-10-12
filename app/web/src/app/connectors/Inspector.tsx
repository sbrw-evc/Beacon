import { useState } from 'react'
import { Eye, Pin, PinOff, Plus, Trash2, X } from 'lucide-react'
import { api } from '../../api'
import { useAction } from '../../connections/useRequest'
import { useLocale, useT } from '../../i18n'
import { Banner, Button, Field, Input, Segmented, Select, Switch, Textarea } from '../../ui'
import { CredentialSelect } from './ConnectorsPage'
import { json } from './graph'
import { strings } from './strings'
import type { CredentialChoice, DataRecord, GraphNode, Issue, NodeTrace, NodeType, OnError, ParamSpec } from './types'

type Preview = { value?: unknown; text?: string; error?: string }

function PreviewButton({ kind, expr, data }: { kind: 'template' | 'cel' | 'path'; expr: string; data?: Record<string, unknown> }) {
  const t = useT(strings)
  const action = useAction()
  const [out, setOut] = useState<Preview | null>(null)
  const run = async () => {
    const r = await action.run(() => api<Preview>('POST', '/api/connectors/preview', { kind, expr, data: data ?? {}, request: {} }))
    if (r) setOut(r)
  }
  return (
    <div className="cn-preview">
      <Button variant="ghost" busy={action.busy} disabled={!expr.trim()} onClick={run} title={data ? t('cn.preview.onInput') : t('cn.preview.empty')}>
        <Eye size={14} />
        {t('cn.preview')}
      </Button>
      {out && (
        <pre className={`cn-preview-out ${out.error ? 'err' : ''}`}>
          {out.error ?? (typeof out.value === 'string' ? out.value : json(out.value))}
          <button type="button" className="icon-btn" aria-label={t('cn.close')} onClick={() => setOut(null)}>
            <X size={12} />
          </button>
        </pre>
      )}
    </div>
  )
}

function asRows(v: unknown): Record<string, string>[] {
  return Array.isArray(v) ? v.map((r) => (r && typeof r === 'object' ? (r as Record<string, string>) : {})) : []
}

function asList(v: unknown): string {
  if (Array.isArray(v)) return v.join('\n')
  return typeof v === 'string' ? v : ''
}

export function ParamInput({
  spec,
  value,
  onChange,
  choices,
  readOnly,
  data,
}: {
  spec: ParamSpec
  value: unknown
  onChange: (v: unknown) => void
  choices: CredentialChoice[]
  readOnly: boolean
  data?: Record<string, unknown>
}) {
  const t = useT(strings)
  const { locale } = useLocale()
  const str = value === undefined || value === null ? '' : typeof value === 'string' ? value : String(value)
  switch (spec.kind) {
    case 'bool':
      return <Switch checked={value === true} onChange={(v) => !readOnly && onChange(v)} label={spec.title[locale]} hint={spec.help?.[locale]} />
    case 'number':
      return (
        <Input
          type="number"
          value={str}
          min={spec.min}
          max={spec.max}
          readOnly={readOnly}
          placeholder={spec.placeholder}
          onChange={(e) => onChange(e.target.value === '' ? undefined : Number(e.target.value))}
        />
      )
    case 'select':
      return (
        <Select value={str} disabled={readOnly} onChange={(e) => onChange(e.target.value)}>
          {!spec.required && <option value="">—</option>}
          {spec.options?.map((o) => (
            <option key={o.value} value={o.value}>
              {o.title[locale]}
            </option>
          ))}
        </Select>
      )
    case 'credential':
      return <CredentialSelect value={str} types={spec.credential_types ?? []} choices={choices} disabled={readOnly} onChange={onChange} />
    case 'text':
      return <Textarea rows={5} value={str} readOnly={readOnly} placeholder={spec.placeholder} className="cn-mono" onChange={(e) => onChange(e.target.value)} />
    case 'list':
      return (
        <Textarea
          rows={3}
          value={asList(value)}
          readOnly={readOnly}
          placeholder={spec.placeholder ?? t('cn.list.placeholder')}
          className="cn-mono"
          onChange={(e) => onChange(e.target.value.split('\n'))}
        />
      )
    case 'table': {
      const rows = asRows(value)
      const cols = spec.columns ?? []
      const set = (i: number, key: string, v: string) => onChange(rows.map((r, j) => (j === i ? { ...r, [key]: v } : r)))
      return (
        <div className="cn-param-table">
          {rows.length > 0 && (
            <div className="cn-param-row head" style={{ gridTemplateColumns: `repeat(${cols.length}, 1fr) 28px` }}>
              {cols.map((c) => (
                <span key={c.key}>{c.title[locale]}</span>
              ))}
              <span />
            </div>
          )}
          {rows.map((r, i) => (
            <div key={i} className="cn-param-row" style={{ gridTemplateColumns: `repeat(${cols.length}, 1fr) 28px` }}>
              {cols.map((c) =>
                c.kind === 'select' ? (
                  <Select key={c.key} value={r[c.key] ?? ''} disabled={readOnly} aria-label={c.title[locale]} onChange={(e) => set(i, c.key, e.target.value)}>
                    <option value="">—</option>
                    {c.options?.map((o) => (
                      <option key={o.value} value={o.value}>
                        {o.title[locale]}
                      </option>
                    ))}
                  </Select>
                ) : (
                  <Input
                    key={c.key}
                    value={r[c.key] ?? ''}
                    readOnly={readOnly}
                    aria-label={c.title[locale]}
                    placeholder={c.placeholder}
                    className={c.kind === 'string' ? '' : 'cn-mono'}
                    onChange={(e) => set(i, c.key, e.target.value)}
                  />
                ),
              )}
              {!readOnly && (
                <button type="button" className="icon-btn" aria-label={t('cn.row.remove')} onClick={() => onChange(rows.filter((_, j) => j !== i))}>
                  <Trash2 size={14} />
                </button>
              )}
            </div>
          ))}
          {!readOnly && (
            <Button variant="ghost" onClick={() => onChange([...rows, {}])}>
              <Plus size={14} />
              {t('cn.row.add')}
            </Button>
          )}
        </div>
      )
    }
    default: {
      const expr = spec.kind === 'template' || spec.kind === 'cel' || spec.kind === 'path'
      return (
        <>
          <Input value={str} readOnly={readOnly} placeholder={spec.placeholder} className={expr ? 'cn-mono' : ''} onChange={(e) => onChange(e.target.value)} />
          {expr && <PreviewButton kind={spec.kind as 'template' | 'cel' | 'path'} expr={str} data={data} />}
        </>
      )
    }
  }
}

function kindHint(t: (k: string) => string, spec: ParamSpec) {
  if (spec.kind === 'template') return t('cn.hint.template')
  if (spec.kind === 'cel') return t('cn.hint.cel')
  if (spec.kind === 'path') return t('cn.hint.path')
  return ''
}

export function RecordsView({ records }: { records: DataRecord[] }) {
  const t = useT(strings)
  const [mode, setMode] = useState<'json' | 'table'>('json')
  if (records.length === 0) return <p className="muted">{t('cn.data.none')}</p>
  // The trigger's record carries only the raw body: show it as received.
  if (records.every((r) => Object.keys(r.data ?? {}).length === 0 && r.raw)) {
    return <pre className="cn-json">{records.map((r) => r.raw).join('\n\n')}</pre>
  }
  const cols = [...new Set(records.flatMap((r) => Object.keys(r.data ?? {})))].slice(0, 12)
  const cell = (v: unknown) => {
    const s = typeof v === 'string' ? v : JSON.stringify(v)
    return s && s.length > 80 ? s.slice(0, 80) + '…' : s
  }
  return (
    <div className="stack">
      <Segmented
        label={t('cn.data.view')}
        value={mode}
        onChange={setMode}
        options={[
          { value: 'json', label: 'JSON' },
          { value: 'table', label: t('cn.data.table') },
        ]}
      />
      {mode === 'json' ? (
        <pre className="cn-json">{json(records.map((r) => r.data))}</pre>
      ) : (
        <div className="cn-table-wrap">
          <table className="cn-table compact">
            <thead>
              <tr>
                <th>#</th>
                {cols.map((c) => (
                  <th key={c}>{c}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {records.map((r, i) => (
                <tr key={i}>
                  <td className="muted">{r.lineage?.item ?? i}</td>
                  {cols.map((c) => (
                    <td key={c} className="cn-mono">
                      {cell(r.data?.[c])}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

export function Inspector({
  node,
  type,
  issues,
  trace,
  stale,
  pinned,
  choices,
  readOnly,
  onChange,
  onRemove,
  onPin,
  onUnpin,
  onClose,
}: {
  node: GraphNode
  type?: NodeType
  issues: Issue[]
  trace?: NodeTrace
  stale: boolean
  pinned: boolean
  choices: CredentialChoice[]
  readOnly: boolean
  onChange: (n: GraphNode) => void
  onRemove: () => void
  onPin: () => void
  onUnpin: () => void
  onClose: () => void
}) {
  const t = useT(strings)
  const { locale } = useLocale()
  const [tab, setTab] = useState<'settings' | 'data'>('settings')
  const data = trace?.input?.[0]?.data
  const setParam = (key: string, v: unknown) => {
    const params = { ...node.params }
    if (v === undefined) delete params[key]
    else params[key] = v
    onChange({ ...node, params })
  }
  const nodeIssues = issues.filter((i) => !i.param)

  return (
    <aside className="cn-inspector">
      <header className="cn-inspector-head">
        <div>
          <strong>{node.name || type?.title[locale] || node.type}</strong>
          <div className="muted cn-mono">
            {node.id} · {node.type} v{node.type_version}
          </div>
        </div>
        <button type="button" className="icon-btn" aria-label={t('cn.close')} onClick={onClose}>
          <X size={16} />
        </button>
      </header>
      <Segmented
        label={t('cn.inspector')}
        value={tab}
        onChange={setTab}
        options={[
          { value: 'settings', label: t('cn.tab.settings') },
          { value: 'data', label: t('cn.tab.data') },
        ]}
      />
      {tab === 'settings' ? (
        <div className="stack cn-inspector-body">
          {type ? <p className="muted">{type.description[locale]}</p> : <Banner kind="error" title={t('cn.unknownType')} />}
          {nodeIssues.map((i, k) => (
            <Banner key={k} kind={i.level === 'error' ? 'error' : 'warn'} title={i.message} />
          ))}
          <Field label={t('cn.node.name')} optional={t('cn.optional')}>
            {(id) => <Input id={id} value={node.name ?? ''} readOnly={readOnly} maxLength={80} onChange={(e) => onChange({ ...node, name: e.target.value })} />}
          </Field>
          {type?.params.map((p) => {
            const pi = issues.filter((i) => i.param === p.key)
            const hint = (
              <>
                {p.help?.[locale] ?? kindHint(t, p)}
                {pi.map((i, k) => (
                  <span key={k} className={`cn-param-issue ${i.level}`}>
                    {i.message}
                  </span>
                ))}
              </>
            )
            if (p.kind === 'bool')
              return <ParamInput key={p.key} spec={p} value={node.params[p.key]} onChange={(v) => setParam(p.key, v)} choices={choices} readOnly={readOnly} />
            return (
              <Field key={p.key} label={p.title[locale] + (p.required ? ' *' : '')} hint={hint}>
                {() => <ParamInput spec={p} value={node.params[p.key]} onChange={(v) => setParam(p.key, v)} choices={choices} readOnly={readOnly} data={data} />}
              </Field>
            )
          })}
          {type?.can_fail && (
            <Field label={t('cn.onError')} hint={t(`cn.onError.${node.on_error || 'fail_record'}.hint`)}>
              {(id) => (
                <Select id={id} value={node.on_error || 'fail_record'} disabled={readOnly} onChange={(e) => onChange({ ...node, on_error: e.target.value as OnError })}>
                  {(['fail_record', 'skip', 'route_error'] as const).map((o) => (
                    <option key={o} value={o}>
                      {t(`cn.onError.${o}`)}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
          )}
          {type?.category !== 'trigger' && type?.category !== 'config' && (
            <Switch checked={!!node.disabled} onChange={(v) => !readOnly && onChange({ ...node, disabled: v })} label={t('cn.node.disabled')} hint={t('cn.node.disabled.hint')} />
          )}
          {!readOnly && (
            <Button variant="ghost" onClick={onRemove}>
              <Trash2 size={14} />
              {t('cn.node.remove')}
            </Button>
          )}
        </div>
      ) : (
        <div className="stack cn-inspector-body">
          {!trace ? (
            <p className="muted">{t('cn.data.notRun')}</p>
          ) : (
            <>
              {stale && <Banner kind="warn" title={t('cn.node.stale')} />}
              <p className="muted">{t('cn.data.summary', { in: trace.in, us: trace.us })}</p>
              {!readOnly &&
                (pinned ? (
                  <Button onClick={onUnpin}>
                    <PinOff size={14} />
                    {t('cn.unpin')}
                  </Button>
                ) : (
                  trace.output && (
                    <Button onClick={onPin} title={t('cn.pin.hint')}>
                      <Pin size={14} />
                      {t('cn.pin')}
                    </Button>
                  )
                ))}
              {trace.input && (
                <section>
                  <h3 className="section-title">{t('cn.data.input')}</h3>
                  <RecordsView records={trace.input} />
                </section>
              )}
              {Object.entries(trace.output ?? {}).map(([name, recs]) => (
                <section key={name}>
                  <h3 className="section-title">{t('cn.data.output', { name, n: trace.out[name] ?? recs.length })}</h3>
                  <RecordsView records={recs} />
                </section>
              ))}
              {trace.errors && trace.errors.length > 0 && (
                <section>
                  <h3 className="section-title">{t('cn.data.errors')}</h3>
                  {trace.errors.map((e, i) => (
                    <Banner key={i} kind="error" title={e.error}>
                      {t('cn.data.item', { n: e.lineage.item })}
                    </Banner>
                  ))}
                </section>
              )}
            </>
          )}
        </div>
      )}
    </aside>
  )
}
