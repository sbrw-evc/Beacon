import { memo, useCallback, useMemo, useState, type DragEvent } from 'react'
import {
  Background,
  Controls,
  Handle,
  MiniMap,
  Position,
  ReactFlow,
  useReactFlow,
  type Connection,
  type Edge,
  type EdgeChange,
  type Node,
  type NodeChange,
  type NodeProps,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { AlertTriangle, Ban, Pin, Webhook, FileJson, Filter, GitFork, Wand2, Send, Settings2, CircleDashed } from 'lucide-react'
import { useLocale, useT } from '../../i18n'
import { useTheme } from '../../theme'
import { issuesByNode } from './graph'
import { strings } from './strings'
import { outputsOf, typeKey, type Category, type Graph, type Issue, type NodeTrace, type NodeType } from './types'

export const DRAG_TYPE = 'application/x-umbrella-node'

export const categoryIcon: Record<Category, typeof Webhook> = {
  trigger: Webhook,
  parse: FileJson,
  transform: Wand2,
  route: GitFork,
  output: Send,
  config: Settings2,
}

type NodeData = {
  title: string
  subtitle: string
  category: Category
  inputs: number
  outputs: string[]
  errors: number
  warnings: number
  trace?: NodeTrace
  pinned: boolean
  disabled: boolean
  stale: boolean
}

type FlowNode = Node<NodeData, 'umb'>

const UmbNode = memo(function UmbNode({ data, selected }: NodeProps<FlowNode>) {
  const t = useT(strings)
  const Icon = data.disabled ? Ban : (categoryIcon[data.category] ?? CircleDashed)
  const tr = data.trace
  const out = tr ? Object.values(tr.out).reduce((a, b) => a + b, 0) : 0
  const failed = tr?.errors?.length ?? 0
  return (
    <div
      className={`cn-node cat-${data.category} ${selected ? 'selected' : ''} ${data.disabled ? 'disabled' : ''} ${data.errors ? 'has-error' : ''} ${data.stale ? 'stale' : ''}`}
    >
      {data.inputs > 0 && <Handle type="target" position={Position.Left} id="in" />}
      <div className="cn-node-head">
        <Icon size={15} aria-hidden />
        <span className="cn-node-title">{data.title}</span>
        {data.pinned && <Pin size={13} aria-label={t('cn.node.pinned')} />}
        {(data.errors > 0 || data.warnings > 0) && (
          <span className={`cn-node-issues ${data.errors ? 'err' : 'warn'}`} title={t('cn.node.issues', { n: data.errors + data.warnings })}>
            <AlertTriangle size={12} />
            {data.errors + data.warnings}
          </span>
        )}
      </div>
      <div className="cn-node-sub">{data.subtitle}</div>
      {tr && (
        <div className={`cn-node-trace ${failed ? 'err' : ''}`} title={data.stale ? t('cn.node.stale') : undefined}>
          {data.outputs.length > 0 ? t('cn.node.trace', { in: tr.in, out }) : t('cn.node.traceIn', { in: tr.in })}
          {failed > 0 && ` · ${t('cn.node.failed', { n: failed })}`}
          {!!tr.filtered && ` · ${t('cn.node.filtered', { n: tr.filtered })}`}
        </div>
      )}
      {data.outputs.map((o, i) => (
        <div key={o} className="cn-node-out" style={{ top: `${((i + 1) / (data.outputs.length + 1)) * 100}%` }}>
          {(data.outputs.length > 1 || o !== 'main') && <span className={`cn-out-label out-${o}`}>{o}</span>}
          <Handle type="source" position={Position.Right} id={o} />
        </div>
      ))}
    </div>
  )
})

const nodeTypes = { umb: UmbNode }

export function Canvas({
  graph,
  types,
  issues,
  trace,
  pinned,
  stale,
  selected,
  readOnly,
  onSelect,
  onMove,
  onMoveEnd,
  onRemove,
  onConnect,
  onRemoveEdges,
  onDropType,
}: {
  graph: Graph
  types: Map<string, NodeType>
  issues: Issue[]
  trace?: Record<string, NodeTrace>
  pinned: Set<string>
  stale: Set<string>
  selected: string | null
  readOnly: boolean
  onSelect: (id: string | null) => void
  onMove: (id: string, pos: { x: number; y: number }) => void
  onMoveEnd: () => void
  onRemove: (ids: string[]) => void
  onConnect: (c: Connection) => void
  onRemoveEdges: (ids: string[]) => void
  onDropType: (key: string, pos: { x: number; y: number }) => void
}) {
  const { locale } = useLocale()
  const { theme } = useTheme()
  const flow = useReactFlow()
  const [dims, setDims] = useState<Record<string, { width: number; height: number }>>({})
  const [selectedEdges, setSelectedEdges] = useState<Set<string>>(new Set())
  const byNode = useMemo(() => issuesByNode(issues), [issues])

  const nodes = useMemo<FlowNode[]>(
    () =>
      graph.nodes.map((n) => {
        const ty = types.get(typeKey(n.type, n.type_version))
        const list = byNode.get(n.id) ?? []
        return {
          id: n.id,
          type: 'umb',
          position: n.position,
          selected: n.id === selected,
          measured: dims[n.id],
          deletable: !readOnly,
          draggable: !readOnly,
          data: {
            title: n.name || ty?.title[locale] || n.type,
            subtitle: n.name ? (ty?.title[locale] ?? n.type) : n.id,
            category: ty?.category ?? 'transform',
            inputs: ty?.inputs ?? 1,
            outputs: outputsOf(ty, n),
            errors: list.filter((i) => i.level === 'error').length,
            warnings: list.filter((i) => i.level !== 'error').length,
            trace: trace?.[n.id],
            pinned: pinned.has(n.id),
            disabled: !!n.disabled,
            stale: !!trace?.[n.id] && stale.has(n.id),
          },
        }
      }),
    [graph.nodes, types, byNode, selected, dims, readOnly, locale, trace, pinned, stale],
  )

  const edges = useMemo<Edge[]>(
    () =>
      graph.edges.map((e) => {
        const count = trace?.[e.source]?.out[e.source_output]
        return {
          id: e.id,
          source: e.source,
          sourceHandle: e.source_output,
          target: e.target,
          targetHandle: 'in',
          selected: selectedEdges.has(e.id),
          deletable: !readOnly,
          label: count !== undefined ? String(count) : undefined,
          className: e.source_output === 'error' ? 'cn-edge-error' : undefined,
          animated: count !== undefined && count > 0,
        }
      }),
    [graph.edges, trace, selectedEdges, readOnly],
  )

  const onNodesChange = useCallback(
    (changes: NodeChange<FlowNode>[]) => {
      const removed: string[] = []
      for (const c of changes) {
        if (c.type === 'dimensions' && c.dimensions) {
          const d = c.dimensions
          setDims((prev) => (prev[c.id]?.width === d.width && prev[c.id]?.height === d.height ? prev : { ...prev, [c.id]: d }))
        } else if (c.type === 'position') {
          if (c.position) onMove(c.id, c.position)
          if (c.dragging === false) onMoveEnd()
        } else if (c.type === 'select') {
          if (c.selected) onSelect(c.id)
        } else if (c.type === 'remove') {
          removed.push(c.id)
        }
      }
      if (removed.length) onRemove(removed)
    },
    [onMove, onMoveEnd, onSelect, onRemove],
  )

  const onEdgesChange = useCallback(
    (changes: EdgeChange[]) => {
      const removed: string[] = []
      setSelectedEdges((prev) => {
        const next = new Set(prev)
        for (const c of changes) {
          if (c.type === 'select') {
            if (c.selected) next.add(c.id)
            else next.delete(c.id)
          }
        }
        return next
      })
      for (const c of changes) if (c.type === 'remove') removed.push(c.id)
      if (removed.length) onRemoveEdges(removed)
    },
    [onRemoveEdges],
  )

  const onDrop = (e: DragEvent) => {
    const key = e.dataTransfer.getData(DRAG_TYPE)
    if (!key || readOnly) return
    e.preventDefault()
    onDropType(key, flow.screenToFlowPosition({ x: e.clientX, y: e.clientY }))
  }

  return (
    <div className="cn-canvas" onDragOver={(e) => e.preventDefault()} onDrop={onDrop}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={readOnly ? undefined : onConnect}
        onPaneClick={() => onSelect(null)}
        nodesConnectable={!readOnly}
        colorMode={theme}
        deleteKeyCode={readOnly ? null : ['Delete', 'Backspace']}
        fitView
        fitViewOptions={{ padding: 0.2, maxZoom: 1.2 }}
        minZoom={0.2}
        proOptions={{ hideAttribution: true }}
      >
        <Background gap={16} />
        <Controls showInteractive={false} />
        <MiniMap pannable zoomable />
      </ReactFlow>
    </div>
  )
}

export function Palette({ types, readOnly, onAdd }: { types: NodeType[]; readOnly: boolean; onAdd: (t: NodeType) => void }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const groups = useMemo(() => {
    const out = new Map<Category, NodeType[]>()
    for (const ty of types) out.set(ty.category, [...(out.get(ty.category) ?? []), ty])
    return [...out.entries()]
  }, [types])
  return (
    <aside className="cn-palette" aria-label={t('cn.palette')}>
      {groups.map(([cat, list]) => {
        const Icon = categoryIcon[cat] ?? Filter
        return (
          <div key={cat} className="cn-palette-group">
            <div className="cn-palette-title">{t(`cn.cat.${cat}`)}</div>
            {list.map((ty) => (
              <button
                key={typeKey(ty.type, ty.version)}
                type="button"
                className="cn-palette-item"
                disabled={readOnly}
                draggable={!readOnly}
                title={ty.description[locale]}
                onDragStart={(e) => {
                  e.dataTransfer.setData(DRAG_TYPE, typeKey(ty.type, ty.version))
                  e.dataTransfer.effectAllowed = 'move'
                }}
                onClick={() => onAdd(ty)}
              >
                <Icon size={14} aria-hidden />
                <span>{ty.title[locale]}</span>
              </button>
            ))}
          </div>
        )
      })}
    </aside>
  )
}
