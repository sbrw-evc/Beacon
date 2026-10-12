import { memo, useEffect, useMemo, useState } from 'react'
import {
  Background,
  Controls,
  Handle,
  MarkerType,
  MiniMap,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useReactFlow,
  type Edge,
  type Node,
  type NodeProps,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { Box, Briefcase, CircleHelp, Cpu, ExternalLink, RefreshCw, Search, Server, Monitor, X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { ErrorBanner } from '../../connections/ConnectionCard'
import { useResource } from '../../connections/useRequest'
import { useLocale, useT } from '../../i18n'
import { SEVERITIES, SEVERITY_PRIORITY, SEVERITY_TONE, severityText, type Severity } from '../incidents/types'
import { useTheme } from '../../theme'
import { Banner, Button, formatDate, Input, Rows, Segmented, Switch } from '../../ui'
import { Link } from '../../router'
import { useSession } from '../session'
import { CI_H, CI_W, layout, SERVICE_H, SERVICE_W } from './layout'
import { strings } from './strings'
import { ProposalsPanel } from './Proposals'
import { LEVELS, problem, rank, type CMDBMap, type Health, type Level, type MapCI, type MapService } from './types'
import './cmdb.css'
import { SetupGuide } from '../guide/SetupGuide'
import { Sheet } from '../mobile/Sheet'
import { mobileStrings } from '../mobile/mobileStrings'
import { useMobile } from '../mobile/useMobile'

const REFRESH_MS = 60_000

type View = 'all' | 'problems'
type Selected = { kind: 'service' | 'ci'; id: string } | null

type ServiceData = { service: MapService; dim: boolean; hit: boolean }
type CIData = { ci: MapCI; dim: boolean; hit: boolean }
type ServiceNode = Node<ServiceData, 'service'>
type CINode = Node<CIData, 'ci'>

const ciIcons: Record<string, typeof Server> = { device: Server, vm: Monitor, service: Cpu, other: Box }

function HealthDot({ level }: { level: Level }) {
  return <span className={`map-dot map-${level}`} aria-hidden />
}

function HealthLabel({ level }: { level: Level }) {
  const t = useT(strings)
  return (
    <span className={`map-health map-${level}`}>
      <HealthDot level={level} />
      {t(`map.level.${level}`)}
    </span>
  )
}

const ServiceBox = memo(function ServiceBox({ data, selected }: NodeProps<ServiceNode>) {
  const t = useT(strings)
  const s = data.service
  return (
    <div className={`map-node map-svc map-${s.health.level} crit-${s.criticality} ${selected ? 'selected' : ''} ${data.dim ? 'dim' : ''} ${data.hit ? 'hit' : ''}`}>
      <Handle type="target" position={Position.Left} />
      <div className="map-node-head">
        <Briefcase size={15} aria-hidden />
        <span className="map-node-name" title={s.name}>
          {s.name}
        </span>
        <HealthDot level={s.health.level} />
      </div>
      <div className="map-node-meta">
        <span className={`map-crit crit-${s.criticality}`}>{t(`map.crit.${s.criticality}`)}</span>
        {s.status !== 'active' && <span className="muted">{t(`map.svc.${s.status}`)}</span>}
        <span className="muted map-node-owner" title={s.owner}>
          {s.owner}
        </span>
      </div>
      <Handle type="source" position={Position.Right} />
    </div>
  )
})

const CIBox = memo(function CIBox({ data, selected }: NodeProps<CINode>) {
  const t = useT(strings)
  const c = data.ci
  const Icon = ciIcons[c.kind] ?? CircleHelp
  const firing = c.events.critical + c.events.error + c.events.warning
  return (
    <div className={`map-node map-ci map-${c.health.level} ${selected ? 'selected' : ''} ${data.dim ? 'dim' : ''} ${data.hit ? 'hit' : ''}`}>
      <Handle type="target" position={Position.Left} />
      <div className="map-node-head">
        <Icon size={15} aria-hidden />
        <span className="map-node-name" title={c.name}>
          {c.name}
        </span>
        <HealthDot level={c.health.level} />
      </div>
      <div className="map-node-meta">
        <span className="muted">{t(`map.ci.${c.kind}`)}</span>
        {firing > 0 && <span className={`map-events ${c.events.critical + c.events.error ? 'map-critical' : 'map-warning'}`}>{firing}</span>}
      </div>
    </div>
  )
})

const nodeTypes = { service: ServiceBox, ci: CIBox }

const toneOf: Record<Level, string> = { critical: 'var(--error)', warning: 'var(--warn)', ok: 'var(--ok)', unknown: 'var(--muted)' }

function miniClass(n: Node) {
  const d = n.data as Partial<ServiceData & CIData>
  return `map-mini map-${d.service?.health.level ?? d.ci?.health.level ?? 'unknown'}`
}

export function CMDBMapPage() {
  return (
    <ReactFlowProvider>
      <MapView />
    </ReactFlowProvider>
  )
}

function matches(name: string, q: string) {
  return q !== '' && name.toLowerCase().includes(q)
}

function MapView() {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const { theme } = useTheme()
  const flow = useReactFlow()
  const [epoch, setEpoch] = useState(0)
  const [view, setView] = useState<View>('all')
  const [showCIs, setShowCIs] = useState(true)
  const [query, setQuery] = useState('')
  const mobile = useMobile()
  // ?focus=<id> opens a service or an item, as the overview of the phone layout links to it.
  const [selected, setSelected] = useState<Selected>(() => {
    const id = new URLSearchParams(window.location.search).get('focus')
    return id ? { kind: id.startsWith('SVC-') ? 'service' : 'ci', id } : null
  })
  const res = useResource<CMDBMap>('/api/cmdb', epoch)
  const map = res.data

  useEffect(() => {
    const id = window.setInterval(() => setEpoch((e) => e + 1), REFRESH_MS)
    return () => window.clearInterval(id)
  }, [])

  const q = query.trim().toLowerCase()

  const graph = useMemo(() => {
    if (!map) return { nodes: [] as Node[], edges: [] as Edge[], shown: 0 }
    const services = new Set(map.services.filter((s) => view === 'all' || problem(s.health.level)).map((s) => s.id))
    const cis = new Set(
      showCIs
        ? map.cis.filter((c) => (view === 'all' ? c.services.some((s) => services.has(s)) : problem(c.health.level))).map((c) => c.id)
        : [],
    )
    if (view === 'problems') {
      // Keep the services of problem items, so every problem shows where it hurts.
      for (const c of map.cis) if (cis.has(c.id)) for (const s of c.services) services.add(s)
    }
    const near = new Set<string>()
    if (selected) {
      near.add(selected.id)
      for (const s of map.services) {
        if (s.id === selected.id) {
          s.ci_ids.forEach((x) => near.add(x))
          s.depends_on.forEach((x) => near.add(x))
        }
        if (s.depends_on.includes(selected.id) || s.ci_ids.includes(selected.id)) near.add(s.id)
      }
    }
    const placed = layout(map, services, cis)
    const byService = new Map(map.services.map((s) => [s.id, s]))
    const byCI = new Map(map.cis.map((c) => [c.id, c]))
    const nodes: Node[] = placed.map((p) => {
      if (p.kind === 'service') {
        const s = byService.get(p.id) as MapService
        const hit = matches(s.name, q)
        const dim = (selected !== null && !near.has(p.id)) || (q !== '' && !hit)
        return { id: p.id, type: 'service', position: { x: p.x, y: p.y }, data: { service: s, dim, hit }, width: SERVICE_W, height: SERVICE_H, selected: selected?.id === p.id }
      }
      const c = byCI.get(p.id) as MapCI
      const hit = matches(c.name, q) || c.ips.some((ip) => matches(ip, q))
      const dim = (selected !== null && !near.has(p.id)) || (q !== '' && !hit)
      return { id: p.id, type: 'ci', position: { x: p.x, y: p.y }, data: { ci: c, dim, hit }, width: CI_W, height: CI_H, selected: selected?.id === p.id }
    })
    const edges: Edge[] = []
    const lit = (a: string, b: string) => !selected || (near.has(a) && near.has(b) && (a === selected.id || b === selected.id))
    for (const s of map.services) {
      if (!services.has(s.id)) continue
      for (const d of s.depends_on) {
        if (!services.has(d)) continue
        const target = byService.get(d) as MapService
        edges.push({
          id: `d:${s.id}:${d}`,
          source: s.id,
          target: d,
          className: `map-edge-dep map-${target.health.level} ${lit(s.id, d) ? '' : 'dim'}`,
          animated: problem(target.health.level),
          markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16, color: problem(target.health.level) ? toneOf[target.health.level] : 'var(--accent)' },
        })
      }
      for (const c of s.ci_ids) {
        if (!cis.has(c)) continue
        const ci = byCI.get(c) as MapCI
        edges.push({ id: `c:${s.id}:${c}`, source: s.id, target: c, className: `map-edge-ci map-${ci.health.level} ${lit(s.id, c) ? '' : 'dim'}` })
      }
    }
    return { nodes, edges, shown: services.size + cis.size }
  }, [map, view, showCIs, selected, q])

  // Fit the view when what is shown changes, not on every refresh.
  const shape = `${view}:${showCIs}:${map?.services.length ?? 0}:${map?.cis.length ?? 0}`
  useEffect(() => {
    const id = window.requestAnimationFrame(() => void flow.fitView({ padding: 0.15, maxZoom: 1.1, duration: 250 }))
    return () => window.cancelAnimationFrame(id)
  }, [shape, flow])

  if (!map) return res.error ? <ErrorBanner error={res.error} strings={strings} /> : <p className="muted">{t('map.loading')}</p>

  const count = (items: { health: Health }[]) => LEVELS.map((l) => [l, items.filter((x) => x.health.level === l).length] as const)
  const detail = selected ? (selected.kind === 'service' ? map.services.find((s) => s.id === selected.id) : map.cis.find((c) => c.id === selected.id)) : undefined

  return (
    <div className="map-page">
      <SetupGuide page="cmdb" />
      <div className="card map-toolbar">
        <label className="svc-search map-search">
          <Search size={16} aria-hidden />
          <Input type="search" value={query} placeholder={t('map.search')} aria-label={t('map.search')} onChange={(e) => setQuery(e.target.value)} />
        </label>
        <Segmented
          label={t('map.view')}
          value={view}
          onChange={setView}
          options={[
            { value: 'all', label: t('map.view.all') },
            { value: 'problems', label: t('map.view.problems') },
          ]}
        />
        <Switch checked={showCIs} onChange={setShowCIs} label={t('map.cis')} />
        <div className="map-toolbar-end">
          <span className="muted map-updated">{t('map.updated', { time: formatDate(map.generated_at, locale, timezone) })}</span>
          <Button variant="ghost" busy={res.busy} onClick={() => setEpoch((e) => e + 1)}>
            <RefreshCw size={16} />
            {t('map.refresh')}
          </Button>
        </div>
      </div>

      <div className="map-summary">
        <Summary title={t('map.services')} counts={count(map.services)} />
        <Summary title={t('map.cis')} counts={count(map.cis)} />
      </div>

      {res.error !== null && <ErrorBanner error={res.error} strings={strings} />}

      <ProposalsPanel epoch={epoch} onDecided={() => setEpoch((e) => e + 1)} onSelect={(id) => setSelected({ kind: 'ci', id })} />

      {map.services.length === 0 ? (
        <div className="card svc-empty">
          <p>{t('map.empty')}</p>
        </div>
      ) : mobile ? (
        <HealthList map={map} view={view} showCIs={showCIs} q={q} selected={selected} onSelect={setSelected} />
      ) : (
        <div className="map-stage">
          <div className="map-canvas">
            {graph.shown === 0 && <p className="map-canvas-empty muted">{t('map.empty.problems')}</p>}
            <ReactFlow
              nodes={graph.nodes}
              edges={graph.edges}
              nodeTypes={nodeTypes}
              nodesDraggable={false}
              nodesConnectable={false}
              elementsSelectable
              onNodeClick={(_, n) => setSelected({ kind: n.type === 'service' ? 'service' : 'ci', id: n.id })}
              onPaneClick={() => setSelected(null)}
              colorMode={theme}
              minZoom={0.15}
              maxZoom={1.6}
              proOptions={{ hideAttribution: true }}
            >
              <Background gap={18} />
              <Controls showInteractive={false} />
              <MiniMap pannable zoomable nodeClassName={miniClass} style={{ width: 160, height: 100 }} />
            </ReactFlow>
          </div>
          <AnimatePresence>
            {detail && selected && (
              <motion.aside
                key={selected.id}
                className="card map-panel"
                initial={{ opacity: 0, x: 16 }}
                animate={{ opacity: 1, x: 0 }}
                exit={{ opacity: 0, x: 16 }}
                transition={{ duration: 0.18 }}
              >
                <header className="map-panel-head">
                  <div>
                    <span className="muted map-panel-kind">{selected.kind === 'service' ? t('map.kind.service') : t(`map.ci.${(detail as MapCI).kind}`)}</span>
                    <h2>{detail.name}</h2>
                  </div>
                  <button type="button" className="icon-btn" onClick={() => setSelected(null)} aria-label={t('map.close')}>
                    <X size={18} />
                  </button>
                </header>
                {selected.kind === 'service' ? (
                  <ServicePanel service={detail as MapService} map={map} onSelect={setSelected} />
                ) : (
                  <CIPanel ci={detail as MapCI} map={map} onSelect={setSelected} />
                )}
              </motion.aside>
            )}
          </AnimatePresence>
        </div>
      )}

      <details className="card map-basis">
        <summary>{t('map.basis.title')}</summary>
        <div className="map-legend">
          {LEVELS.map((l) => (
            <HealthLabel key={l} level={l} />
          ))}
        </div>
        <p>{t('map.basis.ci')}</p>
        <p>{t('map.basis.service')}</p>
        <p className="muted">
          {map.events.available
            ? t('map.basis.events')
            : t('map.basis.noEvents', { reason: map.events.error || t('map.basis.noEvents.db') })}
        </p>
      </details>
      {!map.events.available && map.events.error && <Banner kind="warn" title={t('map.basis.noEvents', { reason: map.events.error })} />}
    </div>
  )
}

// HealthList is the map of the phone layout: the services and items as lists, the worst first,
// and the card of the chosen one in a bottom sheet. A graph is not readable on a phone screen.
function HealthList({
  map,
  view,
  showCIs,
  q,
  selected,
  onSelect,
}: {
  map: CMDBMap
  view: View
  showCIs: boolean
  q: string
  selected: Selected
  onSelect: (s: Selected) => void
}) {
  const t = useT(strings)
  const tm = useT(mobileStrings)
  const keep = (x: { name: string; health: Health }, extra: string[] = []) =>
    (view === 'all' || problem(x.health.level)) && (q === '' || matches(x.name, q) || extra.some((v) => matches(v, q)))
  const order = <T extends { name: string; health: Health }>(a: T, b: T) => rank[b.health.level] - rank[a.health.level] || a.name.localeCompare(b.name)
  const services = map.services.filter((s) => keep(s)).sort(order)
  const cis = showCIs ? map.cis.filter((c) => keep(c, c.ips)).sort(order) : []
  const detail = selected ? (selected.kind === 'service' ? map.services.find((s) => s.id === selected.id) : map.cis.find((c) => c.id === selected.id)) : undefined
  const reasons = (h: Health) => h.reasons.length
  return (
    <>
      {services.length + cis.length === 0 && <p className="muted m-ov-empty">{view === 'problems' && q === '' ? t('map.empty.problems') : tm('m.map.empty')}</p>}
      {services.length > 0 && (
        <section className="m-ov-section">
          <header>
            <h2>
              <Briefcase size={16} aria-hidden />
              {tm('m.map.services')}
            </h2>
          </header>
          <div className="m-list card">
            {services.map((s) => {
              const sick = map.cis.filter((c) => s.ci_ids.includes(c.id) && problem(c.health.level)).length
              return (
                <button key={s.id} type="button" className="m-list-row" onClick={() => onSelect({ kind: 'service', id: s.id })}>
                  <span className={`m-health-dot health-${s.health.level}`} aria-hidden />
                  <span className="m-list-text">
                    <strong>{s.name}</strong>
                    <small className="muted">
                      {[t(`map.crit.${s.criticality}`), s.owner, sick ? tm('m.ov.services.cis', { n: sick }) : tm('m.map.cis.n', { n: s.ci_ids.length })].filter(Boolean).join(' · ')}
                    </small>
                  </span>
                  <span className={`m-health health-${s.health.level}`}>{tm(`m.health.${s.health.level}`)}</span>
                </button>
              )
            })}
          </div>
        </section>
      )}
      {cis.length > 0 && (
        <section className="m-ov-section">
          <header>
            <h2>
              <Server size={16} aria-hidden />
              {tm('m.map.cis')}
            </h2>
          </header>
          <div className="m-list card">
            {cis.map((c) => {
              const Icon = ciIcons[c.kind] ?? CircleHelp
              const firing = SEVERITIES.flatMap((sev) => {
                const n = sev === 'low' ? (c.events.low ?? 0) : c.events[sev]
                return n > 0 ? [`${SEVERITY_PRIORITY[sev]}×${n}`] : []
              }).join(' ')
              return (
                <button key={c.id} type="button" className="m-list-row" onClick={() => onSelect({ kind: 'ci', id: c.id })}>
                  <span className={`m-health-dot health-${c.health.level}`} aria-hidden />
                  <span className="m-list-text">
                    <strong>
                      <Icon size={13} aria-hidden /> {c.name}
                    </strong>
                    <small className="muted">
                      {[firing, t(`map.ci.${c.kind}`), c.ips[0]]
                        .filter(Boolean)
                        .join(' · ')}
                    </small>
                  </span>
                  {reasons(c.health) > 0 && <span className={`m-health health-${c.health.level}`}>{tm(`m.health.${c.health.level}`)}</span>}
                </button>
              )
            })}
          </div>
        </section>
      )}
      <Sheet
        open={!!detail}
        title={
          detail && selected ? (
            <>
              <small className="muted m-sheet-kicker">{selected.kind === 'service' ? t('map.kind.service') : t(`map.ci.${(detail as MapCI).kind}`)}</small>
              {detail.name}
            </>
          ) : (
            ''
          )
        }
        onClose={() => onSelect(null)}
      >
        {detail && selected?.kind === 'service' && <ServicePanel service={detail as MapService} map={map} onSelect={onSelect} />}
        {detail && selected?.kind === 'ci' && <CIPanel ci={detail as MapCI} map={map} onSelect={onSelect} />}
      </Sheet>
    </>
  )
}

function Summary({ title, counts }: { title: string; counts: (readonly [Level, number])[] }) {
  const t = useT(strings)
  return (
    <div className="card map-sum">
      <span className="map-sum-title">{title}</span>
      {counts.map(([l, n]) => (
        <span key={l} className={`map-sum-item map-${l} ${n === 0 ? 'zero' : ''}`} title={t(`map.level.${l}`)}>
          <HealthDot level={l} />
          {n}
        </span>
      ))}
    </div>
  )
}

function Reasons({ health, map, onSelect }: { health: Health; map: CMDBMap; onSelect: (s: Selected) => void }) {
  const t = useT(strings)
  if (health.reasons.length === 0) return <p className="muted">{t('map.reasons.none')}</p>
  return (
    <ul className="map-reasons">
      {health.reasons.map((r, i) => {
        const text = t(`map.reason.${r.code}`, { count: r.count ?? 0, detail: r.detail ?? '' })
        const target = r.ref ? (map.services.some((s) => s.id === r.ref) ? 'service' : 'ci') : null
        return (
          <li key={i}>
            <HealthDot level={r.level} />
            {target && r.ref ? (
              <button type="button" className="link-btn" onClick={() => onSelect({ kind: target, id: r.ref as string })}>
                {text}
              </button>
            ) : (
              <span>{text}</span>
            )}
          </li>
        )
      })}
    </ul>
  )
}

function Links({ items, kind, onSelect }: { items: { id: string; name: string; level: Level }[]; kind: 'service' | 'ci'; onSelect: (s: Selected) => void }) {
  const t = useT(strings)
  if (items.length === 0) return <span className="muted">{t('map.none')}</span>
  return (
    <ul className="map-links">
      {items.map((x) => (
        <li key={x.id}>
          <HealthDot level={x.level} />
          <button type="button" className="link-btn" onClick={() => onSelect({ kind, id: x.id })}>
            {x.name}
          </button>
        </li>
      ))}
    </ul>
  )
}

function ServicePanel({ service: s, map, onSelect }: { service: MapService; map: CMDBMap; onSelect: (s: Selected) => void }) {
  const t = useT(strings)
  const ref = (id: string) => map.services.find((x) => x.id === id)
  const deps = s.depends_on.map(ref).filter((x): x is MapService => !!x)
  const users = map.services.filter((x) => x.depends_on.includes(s.id))
  const cis = map.cis.filter((c) => s.ci_ids.includes(c.id))
  const item = (x: { id: string; name: string; health: Health }) => ({ id: x.id, name: x.name, level: x.health.level })
  return (
    <div className="stack map-panel-body">
      <HealthLabel level={s.health.level} />
      <Reasons health={s.health} map={map} onSelect={onSelect} />
      <Rows
        rows={[
          [t('map.field.criticality'), t(`map.crit.${s.criticality}`)],
          [t('map.field.status'), t(`map.svc.${s.status}`)],
          [t('map.field.owner'), s.owner],
          [t('map.field.cis'), <Links key="c" items={cis.map(item)} kind="ci" onSelect={onSelect} />],
          [t('map.field.dependsOn'), <Links key="d" items={deps.map(item)} kind="service" onSelect={onSelect} />],
          [t('map.field.usedBy'), <Links key="u" items={users.map(item)} kind="service" onSelect={onSelect} />],
        ]}
      />
      {s.netbox_url && <NetBoxLink url={s.netbox_url} />}
    </div>
  )
}

function CIPanel({ ci: c, map, onSelect }: { ci: MapCI; map: CMDBMap; onSelect: (s: Selected) => void }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const { timezone, can } = useSession()
  const services = map.services.filter((s) => c.services.includes(s.id)).map((s) => ({ id: s.id, name: s.name, level: s.health.level }))
  const firing = c.events.critical + c.events.error + c.events.warning + (c.events.low ?? 0) + c.events.info + (c.events.maintenance ?? 0)
  return (
    <div className="stack map-panel-body">
      <HealthLabel level={c.health.level} />
      <Reasons health={c.health} map={map} onSelect={onSelect} />
      <Rows
        rows={[
          [t('map.field.status'), c.status],
          [t('map.field.ips'), c.ips.length ? <span className="cn-mono">{c.ips.join(', ')}</span> : <span className="muted">{t('map.none')}</span>],
          [t('map.field.directory'), c.directory ? t(`map.dir.${c.directory}`) : <span className="muted">{t('map.none')}</span>],
          [t('map.field.services'), <Links key="s" items={services} kind="service" onSelect={onSelect} />],
          [
            t('map.field.events'),
            firing ? (
              <div key="e" className="map-recent">
                <span className="muted">
                  {t('map.events.counts', { critical: c.events.critical, error: c.events.error, warning: c.events.warning, low: c.events.low ?? 0, info: c.events.info })}
                  {(c.events.maintenance ?? 0) > 0 && t('map.events.maintenance', { count: c.events.maintenance ?? 0 })}
                </span>
                <ul>
                  {c.events.recent.map((e) => (
                    <li key={e.incident_id}>
                      <span className={`pill map-sev-${SEVERITY_TONE[e.severity as Severity] ?? 'info'}`}>{severityText(t, e.severity)}</span>{' '}
                      {can('incidents:view') ? <Link to={`/incidents?id=${encodeURIComponent(e.incident_id)}`}>{e.incident_id}</Link> : e.incident_id} {e.title}
                      <span className="muted">
                        {' · '}
                        {e.suppressed ? t('map.events.suppressed') : t(`map.events.status.${e.status}`)} · {formatDate(e.last_seen, locale, timezone)}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            ) : (
              <span className="muted">{t('map.none')}</span>
            ),
          ],
        ]}
      />
      {c.netbox_url && <NetBoxLink url={c.netbox_url} />}
    </div>
  )
}

function NetBoxLink({ url }: { url: string }) {
  const t = useT(strings)
  return (
    <a href={url} target="_blank" rel="noopener noreferrer" className="map-netbox">
      {t('map.netbox')}
      <ExternalLink size={13} aria-hidden />
    </a>
  )
}
