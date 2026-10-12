import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ReactFlowProvider, useReactFlow, type Connection } from '@xyflow/react'
import { ArrowLeft, ChevronDown, ChevronUp, Copy, Download, History, Lock, Redo2, Rocket, Send, Settings, Square, Undo2 } from 'lucide-react'
import { api, ApiError } from '../../api'
import { ErrorBanner, ErrorFlash } from '../../connections/ConnectionCard'
import { useAction, useResource } from '../../connections/useRequest'
import { useLocale, useT } from '../../i18n'
import { Link, useRouter } from '../../router'
import { Banner, Button, Field, formatDate, Input, Modal, Switch, Textarea } from '../../ui'
import { useSession } from '../session'
import { Canvas, Palette } from './Canvas'
import { ingestURL, StatusPill } from './ConnectorsPage'
import { downloadJSON, newNode, signatures, staleNodes, uniqueId } from './graph'
import { Inspector } from './Inspector'
import { TestEvent } from './QuickConnect'
import { sourcesStrings } from './sourcesStrings'
import { EventsPanel, FailuresPanel, IssuesPanel, RequestsPanel, SamplesPanel, StatsPanel, TestPanel, type Tab } from './Panels'
import { strings } from './strings'
import {
  typeKey,
  type Connector,
  type CredentialChoice,
  type DraftSaved,
  type Graph,
  type GraphNode,
  type Issue,
  type NodeTypes,
  type Pins,
  type Sample,
  type Status,
  type TestAll,
  type TestRun,
} from './types'

type SaveState = 'saved' | 'saving' | 'error' | 'conflict'
type LockState = 'none' | 'mine' | 'other'

const EMPTY: Graph = { nodes: [], edges: [] }
const HISTORY = 100

export function ConnectorEditor({ id }: { id: string }) {
  return (
    <ReactFlowProvider>
      <EditorInner id={id} />
    </ReactFlowProvider>
  )
}

function editableTarget(e: KeyboardEvent) {
  const el = e.target as HTMLElement | null
  return !!el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT' || el.isContentEditable)
}

function EditorInner({ id }: { id: string }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { can, timezone } = useSession()
  const flow = useReactFlow()
  const canEdit = can('connectors:edit')
  const canPublish = can('connectors:publish')
  const canPayload = can('connectors:payload')
  const path = `/api/connectors/${encodeURIComponent(id)}`

  const [conn, setConn] = useState<Connector | null>(null)
  const [loadError, setLoadError] = useState<unknown>(null)
  const [graph, setGraphState] = useState<Graph>(EMPTY)
  const graphRef = useRef<Graph>(EMPTY)
  const [pins, setPinsState] = useState<Pins>({})
  const pinsRef = useRef<Pins>({})
  const revision = useRef(0)
  const past = useRef<Graph[]>([])
  const future = useRef<Graph[]>([])
  const dragging = useRef(false)
  const seq = useRef(0)
  const savedSeq = useRef(0)
  const saving = useRef(false)
  const [changed, setChanged] = useState(0)
  const [save, setSave] = useState<SaveState>('saved')
  const [saveError, setSaveError] = useState<unknown>(null)
  const [status, setStatus] = useState<Status>('draft')
  const [issues, setIssues] = useState<Issue[]>([])
  const [lock, setLock] = useState<LockState>('none')
  const lockRef = useRef<LockState>('none')
  const [selected, setSelected] = useState<string | null>(null)
  const [tab, setTab] = useState<Tab>('test')
  const [panelOpen, setPanelOpen] = useState(true)
  const [testing, setTesting] = useState(false)
  const ts = useT(sourcesStrings)
  const [run, setRun] = useState<TestRun | null>(null)
  const [runSig, setRunSig] = useState<Record<string, string> | null>(null)
  const [all, setAll] = useState<TestAll | null>(null)
  const [sample, setSample] = useState('')
  const [samplesEpoch, setSamplesEpoch] = useState(0)
  const [dialog, setDialog] = useState<'publish' | 'versions' | 'settings' | 'export' | null>(null)
  const testAction = useAction()

  const types = useResource<NodeTypes>('/api/connectors/node-types', 0)
  const choices = useResource<CredentialChoice[]>('/api/connectors/credentials', 0)
  const samples = useResource<Sample[]>(`${path}/samples`, canPayload ? samplesEpoch : -1)
  const typeMap = useMemo(() => new Map((types.data?.types ?? []).map((ty) => [typeKey(ty.type, ty.version), ty])), [types.data])

  const readOnly = !canEdit || lock !== 'mine' || save === 'conflict'

  const load = useCallback(
    async (resetGraph: boolean) => {
      try {
        const c = await api<Connector>('GET', path)
        setConn(c)
        setStatus(c.status)
        setLoadError(null)
        if (resetGraph) {
          const g = c.draft.graph ?? EMPTY
          graphRef.current = g
          setGraphState(g)
          pinsRef.current = c.draft.pins ?? {}
          setPinsState(pinsRef.current)
          revision.current = c.draft.revision
          past.current = []
          future.current = []
          savedSeq.current = seq.current
          setIssues(c.issues)
          setSave('saved')
        }
      } catch (e) {
        setLoadError(e)
      }
    },
    [path],
  )

  useEffect(() => {
    void load(true)
  }, [load])

  useEffect(() => {
    if (!sample && samples.data?.length) setSample(samples.data[samples.data.length - 1].id)
    if (sample && samples.data && !samples.data.some((s) => s.id === sample)) setSample(samples.data[0]?.id ?? '')
  }, [samples.data, sample])

  // The edit lock: one editor at a time, renewed while the page is open.
  useEffect(() => {
    if (!canEdit) return
    let stopped = false
    const take = async () => {
      try {
        await api('POST', `${path}/lock`)
        if (stopped) return
        if (lockRef.current === 'other') await load(true)
        lockRef.current = 'mine'
        setLock('mine')
      } catch (e) {
        if (stopped) return
        if (e instanceof ApiError && e.code === 'locked') {
          lockRef.current = 'other'
          setLock('other')
          void load(seq.current === savedSeq.current)
        }
      }
    }
    void take()
    const timer = window.setInterval(take, 25_000)
    return () => {
      stopped = true
      window.clearInterval(timer)
      void api('DELETE', `${path}/lock`).catch(() => undefined)
    }
  }, [canEdit, path, load])

  const flush = useCallback(async (): Promise<boolean> => {
    if (saving.current) return false
    if (seq.current === savedSeq.current) return true
    saving.current = true
    const mine = seq.current
    setSave('saving')
    try {
      const r = await api<DraftSaved>('PUT', `${path}/draft`, { graph: graphRef.current, pins: pinsRef.current, revision: revision.current })
      revision.current = r.revision
      savedSeq.current = mine
      setIssues(r.issues)
      setStatus(r.status)
      setSave('saved')
      setSaveError(null)
      return true
    } catch (e) {
      if (e instanceof ApiError && e.code === 'draft_conflict') setSave('conflict')
      else if (e instanceof ApiError && e.code === 'locked') {
        lockRef.current = 'other'
        setLock('other')
        setSave('error')
      } else setSave('error')
      setSaveError(e)
      return false
    } finally {
      saving.current = false
      if (seq.current !== savedSeq.current) setChanged((c) => c + 1)
    }
  }, [path])

  useEffect(() => {
    if (readOnly || seq.current === savedSeq.current) return
    const timer = window.setTimeout(() => void flush(), 800)
    return () => window.clearTimeout(timer)
  }, [changed, readOnly, flush])

  useEffect(() => {
    const warn = (e: BeforeUnloadEvent) => {
      if (seq.current !== savedSeq.current) e.preventDefault()
    }
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [])

  const touch = () => {
    seq.current++
    setChanged((c) => c + 1)
  }

  const apply = useCallback((next: Graph, history = true) => {
    if (history) {
      past.current.push(graphRef.current)
      if (past.current.length > HISTORY) past.current.shift()
      future.current = []
    }
    graphRef.current = next
    setGraphState(next)
    seq.current++
    setChanged((c) => c + 1)
  }, [])

  const setPins = (next: Pins) => {
    pinsRef.current = next
    setPinsState(next)
    touch()
  }

  const undo = useCallback(() => {
    const prev = past.current.pop()
    if (!prev) return
    future.current.push(graphRef.current)
    apply(prev, false)
  }, [apply])

  const redo = useCallback(() => {
    const next = future.current.pop()
    if (!next) return
    past.current.push(graphRef.current)
    apply(next, false)
  }, [apply])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (readOnly || editableTarget(e) || !(e.ctrlKey || e.metaKey)) return
      const k = e.key.toLowerCase()
      if (k === 'z' && !e.shiftKey) {
        e.preventDefault()
        undo()
      } else if ((k === 'z' && e.shiftKey) || k === 'y') {
        e.preventDefault()
        redo()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [readOnly, undo, redo])

  const onMove = useCallback(
    (nid: string, pos: { x: number; y: number }) => {
      if (readOnly) return
      if (!dragging.current) {
        dragging.current = true
        past.current.push(graphRef.current)
        future.current = []
      }
      const g = graphRef.current
      apply({ ...g, nodes: g.nodes.map((n) => (n.id === nid ? { ...n, position: { x: Math.round(pos.x), y: Math.round(pos.y) } } : n)) }, false)
    },
    [apply, readOnly],
  )

  const onMoveEnd = useCallback(() => {
    dragging.current = false
  }, [])

  const removeNodes = useCallback(
    (ids: string[]) => {
      if (readOnly) return
      const drop = new Set(ids)
      const g = graphRef.current
      apply({ nodes: g.nodes.filter((n) => !drop.has(n.id)), edges: g.edges.filter((e) => !drop.has(e.source) && !drop.has(e.target)) })
      if (selected && drop.has(selected)) setSelected(null)
      if (ids.some((x) => pinsRef.current[x])) setPins(Object.fromEntries(Object.entries(pinsRef.current).filter(([k]) => !drop.has(k))))
    },
    [apply, readOnly, selected],
  )

  const removeEdges = useCallback(
    (ids: string[]) => {
      if (readOnly) return
      const drop = new Set(ids)
      const g = graphRef.current
      apply({ ...g, edges: g.edges.filter((e) => !drop.has(e.id)) })
    },
    [apply, readOnly],
  )

  const connect = useCallback(
    (c: Connection) => {
      if (readOnly || !c.source || !c.target || c.source === c.target) return
      const g = graphRef.current
      const out = c.sourceHandle ?? 'main'
      if (g.edges.some((e) => e.source === c.source && e.source_output === out && e.target === c.target)) return
      const eid = uniqueId(`e_${c.source}_${c.target}`, g.edges.map((e) => e.id))
      apply({ ...g, edges: [...g.edges, { id: eid, source: c.source, source_output: out, target: c.target }] })
    },
    [apply, readOnly],
  )

  const addNode = (key: string, pos?: { x: number; y: number }) => {
    const ty = typeMap.get(key)
    if (!ty || readOnly) return
    const g = graphRef.current
    let at = pos
    if (!at) {
      const box = document.querySelector('.cn-canvas')?.getBoundingClientRect()
      at = box ? flow.screenToFlowPosition({ x: box.left + box.width / 2, y: box.top + box.height / 2 }) : { x: 0, y: 0 }
    }
    const n = newNode(ty, g, { x: Math.round(at.x), y: Math.round(at.y) })
    const edges = [...g.edges]
    // Connect the new node after the selected one when that is unambiguous.
    const from = g.nodes.find((x) => x.id === selected)
    const fromType = from && typeMap.get(typeKey(from.type, from.type_version))
    if (!pos && from && fromType && fromType.outputs?.length === 1 && !fromType.dynamic_outputs && ty.inputs > 0) {
      n.position = { x: from.position.x + 260, y: from.position.y }
      edges.push({ id: uniqueId(`e_${from.id}_${n.id}`, edges.map((e) => e.id)), source: from.id, source_output: fromType.outputs[0], target: n.id })
    }
    apply({ nodes: [...g.nodes, n], edges })
    setSelected(n.id)
  }

  const updateNode = (n: GraphNode) => {
    const g = graphRef.current
    apply({ ...g, nodes: g.nodes.map((x) => (x.id === n.id ? n : x)) })
  }

  const runTest = async (stopAt?: string) => {
    if (!sample) return
    setTab('test')
    const r = await testAction.run(() =>
      api<TestRun>('POST', `${path}/test-run`, { graph: graphRef.current, pins: pinsRef.current, sample_id: sample, stop_at: stopAt ?? '', use_pins: true }),
    )
    if (!r) return
    setRun(r)
    setRunSig(signatures(graphRef.current))
    setAll(null)
  }

  const runAll = async () => {
    setTab('test')
    const r = await testAction.run(() => api<TestAll>('POST', `${path}/test-all`, { graph: graphRef.current }))
    if (r) setAll(r)
  }

  const stale = useMemo(() => staleNodes(graph, runSig), [graph, runSig])
  const pinned = useMemo(() => new Set(Object.keys(pins)), [pins])
  const node = graph.nodes.find((n) => n.id === selected)
  const trace = run?.result?.trace

  if (loadError) return <ErrorBanner error={loadError} strings={strings} />
  if (!conn || !types.data) return <p className="muted">{t('loading')}</p>

  const errors = issues.filter((i) => i.level === 'error').length
  const saveText = readOnly ? t('cn.readOnly') : seq.current !== savedSeq.current && save === 'saved' ? t('cn.save.pending') : t(`cn.save.${save}`)

  return (
    <div className="cn-editor">
      <header className="cn-editor-head">
        <Link to="/connectors" className="icon-btn" aria-label={t('cn.back')}>
          <ArrowLeft size={18} />
        </Link>
        <div className="cn-editor-title">
          <h1>{conn.name}</h1>
          <div className="row">
            <StatusPill status={status} />
            {conn.published > 0 && <span className="muted">{t('cn.version', { n: conn.published })}</span>}
            <button type="button" className="cn-link cn-mono" title={t('cn.copyURL')} onClick={() => void navigator.clipboard?.writeText(ingestURL(conn.ingest_path))}>
              {conn.ingest_path} <Copy size={12} />
            </button>
          </div>
        </div>
        <span className={`cn-save ${save}`}>{saveText}</span>
        <div className="row cn-editor-actions">
          {canEdit && (
            <>
              <button type="button" className="icon-btn" aria-label={t('cn.undo')} title={t('cn.undo')} disabled={readOnly || past.current.length === 0} onClick={undo}>
                <Undo2 size={16} />
              </button>
              <button type="button" className="icon-btn" aria-label={t('cn.redo')} title={t('cn.redo')} disabled={readOnly || future.current.length === 0} onClick={redo}>
                <Redo2 size={16} />
              </button>
            </>
          )}
          <Button variant="ghost" onClick={() => setDialog('versions')}>
            <History size={16} />
            {t('cn.versions')}
          </Button>
          <Button variant="ghost" onClick={() => setDialog('export')}>
            <Download size={16} />
            {t('cn.export')}
          </Button>
          {canEdit && (
            <Button variant="ghost" onClick={() => setDialog('settings')}>
              <Settings size={16} />
              {t('cn.settings')}
            </Button>
          )}
          {conn.published > 0 && (can('connectors:edit') || can('monitoring:edit')) && (
            <Button variant="ghost" onClick={() => setTesting(true)}>
              <Send size={16} />
              {ts('src.test')}
            </Button>
          )}
          {canPublish && (
            <Button variant="primary" disabled={!canEdit && status === 'published'} onClick={() => setDialog('publish')}>
              <Rocket size={16} />
              {t('cn.publish')}
            </Button>
          )}
        </div>
      </header>
      <Modal open={testing} title={ts('src.test')} onClose={() => setTesting(false)}>
        <TestEvent connectorID={conn.id} />
      </Modal>

      {lock === 'other' && conn.lock && !conn.lock.mine && (
        <Banner kind="warn" title={t('cn.lockedBy', { name: conn.lock.name || conn.lock.username })}>
          <Lock size={14} /> {t('cn.locked.hint')}
        </Banner>
      )}
      {save === 'conflict' && (
        <Banner kind="warn" title={t('cn.conflict')}>
          <Button onClick={() => void load(true)}>{t('cn.reload')}</Button>
        </Banner>
      )}
      {save === 'error' && <ErrorFlash error={saveError} strings={strings} />}
      {conn.publish_error && <Banner kind="error" title={t('cn.publishBroken')}>{conn.publish_error}</Banner>}

      <div className={`cn-workspace ${node ? 'with-inspector' : ''}`}>
        <Palette types={types.data.types} readOnly={readOnly} onAdd={(ty) => addNode(typeKey(ty.type, ty.version))} />
        <Canvas
          graph={graph}
          types={typeMap}
          issues={issues}
          trace={trace}
          pinned={pinned}
          stale={stale}
          selected={selected}
          readOnly={readOnly}
          onSelect={setSelected}
          onMove={onMove}
          onMoveEnd={onMoveEnd}
          onRemove={removeNodes}
          onConnect={connect}
          onRemoveEdges={removeEdges}
          onDropType={(key, pos) => addNode(key, pos)}
        />
        {node && (
          <Inspector
            key={node.id}
            node={node}
            type={typeMap.get(typeKey(node.type, node.type_version))}
            issues={issues.filter((i) => i.node_id === node.id)}
            trace={trace?.[node.id]}
            stale={stale.has(node.id)}
            pinned={pinned.has(node.id)}
            choices={choices.data ?? []}
            readOnly={readOnly}
            onChange={updateNode}
            onRemove={() => removeNodes([node.id])}
            onPin={() => {
              const out = trace?.[node.id]?.output
              if (out) setPins({ ...pinsRef.current, [node.id]: out })
            }}
            onUnpin={() => {
              const next = { ...pinsRef.current }
              delete next[node.id]
              setPins(next)
            }}
            onClose={() => setSelected(null)}
          />
        )}
      </div>

      <section className={`cn-panel card ${panelOpen ? 'open' : ''}`}>
        <div className="cn-tabs" role="tablist">
          {(['test', 'issues', 'samples', 'requests', 'failures', 'events', 'stats'] as Tab[])
            .filter((x) => canPayload || !['samples', 'requests', 'failures', 'test'].includes(x))
            .map((x) => (
              <button
                key={x}
                type="button"
                role="tab"
                aria-selected={tab === x}
                className={tab === x ? 'active' : ''}
                onClick={() => {
                  setTab(x)
                  setPanelOpen(true)
                }}
              >
                {t(`cn.tab.${x}`)}
                {x === 'issues' && issues.length > 0 && <span className={`cn-count ${errors ? 'err' : 'warn'}`}>{issues.length}</span>}
              </button>
            ))}
          <button type="button" className="icon-btn cn-panel-toggle" aria-label={t(panelOpen ? 'cn.panel.hide' : 'cn.panel.show')} onClick={() => setPanelOpen((v) => !v)}>
            {panelOpen ? <ChevronDown size={16} /> : <ChevronUp size={16} />}
          </button>
        </div>
        {panelOpen && (
          <div className="cn-panel-body">
            {tab === 'issues' && <IssuesPanel issues={issues} onSelect={setSelected} />}
            {tab === 'test' && (
              <TestPanel
                samples={samples.data ?? []}
                sample={sample}
                setSample={setSample}
                selected={selected}
                run={run}
                all={all}
                busy={testAction.busy}
                error={testAction.error}
                onRun={(stop) => void runTest(stop)}
                onRunAll={() => void runAll()}
              />
            )}
            {tab === 'samples' && (
              <SamplesPanel
                conn={conn}
                samples={samples.data ?? []}
                editable={canEdit}
                onChanged={() => {
                  setSamplesEpoch((e) => e + 1)
                  void load(false)
                }}
              />
            )}
            {tab === 'requests' && <RequestsPanel conn={conn} editable={canEdit} onSampled={() => setSamplesEpoch((e) => e + 1)} />}
            {tab === 'failures' && <FailuresPanel conn={conn} canReprocess={canPublish} />}
            {tab === 'events' && <EventsPanel conn={conn} />}
            {tab === 'stats' && <StatsPanel conn={conn} />}
          </div>
        )}
      </section>

      <PublishDialog
        open={dialog === 'publish'}
        conn={conn}
        status={status}
        canEdit={canEdit}
        onClose={() => setDialog(null)}
        flush={flush}
        revision={() => revision.current}
        onDone={(c) => {
          setConn(c)
          setStatus(c.status)
          setIssues(c.issues)
        }}
        onFailed={() => {
          setTab('issues')
          setPanelOpen(true)
          void load(false)
        }}
      />
      <VersionsDialog
        open={dialog === 'versions'}
        conn={conn}
        canEdit={canEdit && !readOnly}
        locale={locale}
        timezone={timezone}
        onClose={() => setDialog(null)}
        onRestored={() => void load(true)}
      />
      <ExportDialog open={dialog === 'export'} conn={conn} canPayload={canPayload} onClose={() => setDialog(null)} />
      <SettingsDialog open={dialog === 'settings'} conn={conn} onClose={() => setDialog(null)} onSaved={setConn} />
    </div>
  )
}

function PublishDialog({
  open,
  conn,
  status,
  canEdit,
  onClose,
  flush,
  revision,
  onDone,
  onFailed,
}: {
  open: boolean
  conn: Connector
  status: Status
  canEdit: boolean
  onClose: () => void
  flush: () => Promise<boolean>
  revision: () => number
  onDone: (c: Connector) => void
  onFailed: () => void
}) {
  const t = useT(strings)
  const [name, setName] = useState('')
  const [comment, setComment] = useState('')
  const action = useAction()
  const publish = async () => {
    if (canEdit && !(await flush())) return
    const c = await action.run(() => api<Connector>('POST', `/api/connectors/${conn.id}/publish`, { revision: revision(), name, comment }))
    if (c) {
      onDone(c)
      setName('')
      setComment('')
      onClose()
    } else onFailed()
  }
  const unpublish = async () => {
    const c = await action.run(() => api<Connector>('POST', `/api/connectors/${conn.id}/unpublish`, {}))
    if (c) {
      onDone(c)
      onClose()
    }
  }
  return (
    <Modal
      open={open}
      title={t('cn.publish.title')}
      onClose={onClose}
      footer={
        <>
          {conn.published > 0 && (
            <Button variant="ghost" busy={action.busy} onClick={unpublish}>
              <Square size={14} />
              {t('cn.unpublish')}
            </Button>
          )}
          <Button variant="primary" busy={action.busy} disabled={status === 'published'} onClick={publish}>
            <Rocket size={14} />
            {t('cn.publish')}
          </Button>
        </>
      }
    >
      <p className="muted">{status === 'published' ? t('cn.publish.same') : t('cn.publish.hint', { n: conn.published + 1 })}</p>
      <Field label={t('cn.publish.name')} optional={t('cn.optional')}>
        {(id) => <Input id={id} value={name} maxLength={80} onChange={(e) => setName(e.target.value)} />}
      </Field>
      <Field label={t('cn.publish.comment')} optional={t('cn.optional')}>
        {(id) => <Textarea id={id} rows={3} value={comment} onChange={(e) => setComment(e.target.value)} />}
      </Field>
      {conn.published > 0 && <p className="hint">{t('cn.unpublish.hint')}</p>}
      <ErrorFlash error={action.error} strings={strings} />
    </Modal>
  )
}

function VersionsDialog({
  open,
  conn,
  canEdit,
  locale,
  timezone,
  onClose,
  onRestored,
}: {
  open: boolean
  conn: Connector
  canEdit: boolean
  locale: string
  timezone: string
  onClose: () => void
  onRestored: () => void
}) {
  const t = useT(strings)
  const action = useAction()
  const restore = async (n: number) => {
    const c = await action.run(() => api<Connector>('POST', `/api/connectors/${conn.id}/versions/${n}/restore`, {}))
    if (c) {
      onRestored()
      onClose()
    }
  }
  const versions = [...conn.versions].sort((a, b) => b.number - a.number)
  return (
    <Modal open={open} title={t('cn.versions')} onClose={onClose}>
      {versions.length === 0 && <p className="muted">{t('cn.versions.none')}</p>}
      {versions.map((v) => (
        <div key={v.number} className="cn-version">
          <div>
            <strong>
              v{v.number}
              {v.name && ` · ${v.name}`}
            </strong>
            {v.current && <span className="pill pill-ok">{t('cn.versions.current')}</span>}
            <div className="muted">
              {formatDate(v.created_at, locale, timezone)} · {v.created_by}
            </div>
            {v.comment && <p>{v.comment}</p>}
          </div>
          <div className="row">
            <Button
              variant="ghost"
              onClick={async () => {
                const doc = await action.run(() => api('GET', `/api/connectors/${conn.id}/export?version=${v.number}`))
                if (doc) downloadJSON(`connector-${conn.slug}-v${v.number}.json`, doc)
              }}
            >
              <Download size={14} />
            </Button>
            {canEdit && (
              <Button busy={action.busy} onClick={() => void restore(v.number)} title={t('cn.versions.restore.hint')}>
                {t('cn.versions.restore')}
              </Button>
            )}
          </div>
        </div>
      ))}
      <ErrorFlash error={action.error} strings={strings} />
    </Modal>
  )
}

function ExportDialog({ open, conn, canPayload, onClose }: { open: boolean; conn: Connector; canPayload: boolean; onClose: () => void }) {
  const t = useT(strings)
  const [withSamples, setWithSamples] = useState(false)
  const action = useAction()
  const run = async () => {
    const doc = await action.run(() => api('GET', `/api/connectors/${conn.id}/export${withSamples ? '?samples=true' : ''}`))
    if (!doc) return
    downloadJSON(`connector-${conn.slug}.json`, doc)
    onClose()
  }
  return (
    <Modal
      open={open}
      title={t('cn.export')}
      onClose={onClose}
      footer={
        <Button variant="primary" busy={action.busy} onClick={run}>
          <Download size={14} />
          {t('cn.export.download')}
        </Button>
      }
    >
      <p className="muted">{t('cn.export.hint')}</p>
      {canPayload && <Switch checked={withSamples} onChange={setWithSamples} label={t('cn.export.samples')} hint={t('cn.export.samples.hint')} />}
      <ErrorFlash error={action.error} strings={strings} />
    </Modal>
  )
}

function SettingsDialog({ open, conn, onClose, onSaved }: { open: boolean; conn: Connector; onClose: () => void; onSaved: (c: Connector) => void }) {
  const t = useT(strings)
  const { navigate } = useRouter()
  const [name, setName] = useState(conn.name)
  const [slug, setSlug] = useState(conn.slug)
  const [description, setDescription] = useState(conn.description)
  const [tags, setTags] = useState(conn.tags.join(', '))
  const [confirm, setConfirm] = useState('')
  const action = useAction()
  useEffect(() => {
    if (!open) return
    setName(conn.name)
    setSlug(conn.slug)
    setDescription(conn.description)
    setTags(conn.tags.join(', '))
    setConfirm('')
  }, [open, conn])
  const submit = async () => {
    const c = await action.run(() =>
      api<Connector>('PUT', `/api/connectors/${conn.id}`, {
        name,
        slug,
        description,
        tags: tags
          .split(',')
          .map((x) => x.trim())
          .filter(Boolean),
      }),
    )
    if (c) {
      onSaved(c)
      onClose()
    }
  }
  const remove = async () => {
    const ok = await action.run(async () => {
      await api('DELETE', `/api/connectors/${conn.id}`)
      return true
    })
    if (ok) navigate('/connectors')
  }
  return (
    <Modal
      open={open}
      title={t('cn.settings')}
      onClose={onClose}
      footer={
        <Button variant="primary" busy={action.busy} disabled={!name.trim()} onClick={submit}>
          {t('cn.save')}
        </Button>
      }
    >
      <Field label={t('cn.field.name')}>{(id) => <Input id={id} value={name} maxLength={100} onChange={(e) => setName(e.target.value)} />}</Field>
      <Field label={t('cn.field.slug')} hint={t('cn.field.slug.change')}>
        {(id) => <Input id={id} className="cn-mono" value={slug} onChange={(e) => setSlug(e.target.value)} />}
      </Field>
      <Field label={t('cn.field.description')} optional={t('cn.optional')}>
        {(id) => <Textarea id={id} rows={2} value={description} onChange={(e) => setDescription(e.target.value)} />}
      </Field>
      <Field label={t('cn.field.tags')} hint={t('cn.field.tags.hint')} optional={t('cn.optional')}>
        {(id) => <Input id={id} value={tags} onChange={(e) => setTags(e.target.value)} />}
      </Field>
      <ErrorFlash error={action.error} strings={strings} />
      <details className="cn-danger">
        <summary>{t('cn.delete')}</summary>
        <p className="muted">{t('cn.delete.hint', { slug: conn.slug })}</p>
        <div className="row">
          <Input value={confirm} placeholder={conn.slug} aria-label={t('cn.delete.confirm')} onChange={(e) => setConfirm(e.target.value)} />
          <Button busy={action.busy} disabled={confirm !== conn.slug} onClick={remove}>
            {t('cn.delete')}
          </Button>
        </div>
      </details>
    </Modal>
  )
}
