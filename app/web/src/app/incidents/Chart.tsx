import { useEffect, useMemo, useRef, useState } from 'react'

export type ChartSeries = { name: string; unit?: string; points: [number, number][] }
export type ChartMark = { at: number; tone: 'open' | 'resolve' | 'event' | 'incident' | 'source'; label: string }

// Colours of the series: the tokens of the theme, so custom themes colour graphs too.
const COLORS = ['var(--accent)', 'var(--ok)', 'var(--warn)', 'var(--low)', 'var(--error)', 'var(--info)', 'var(--muted)']
export const seriesColor = (i: number) => COLORS[i % COLORS.length]

// The standard graphs (model.DefaultHostPanels) by ID and English title: their title is shown in
// the language of the interface until somebody renames them.
export const DEFAULT_PANELS: Record<string, string> = {
  cpu: 'CPU',
  memory: 'Memory',
  load: 'Load average (1 min)',
  disk: 'Disk space used',
  net_in: 'Network in',
  net_out: 'Network out',
}

export function panelTitle(t: (k: string) => string, p: { id: string; title: string }) {
  return DEFAULT_PANELS[p.id] === p.title ? t(`panel.${p.id}`) : p.title
}

const HEIGHT = 170
const PAD = { top: 10, right: 12, bottom: 22, left: 52 }

// formatValue shows a value in its unit: percent, bits or bytes per second and bytes get
// scaled units, anything else a short number with the unit after it.
export function formatValue(v: number, unit = '') {
  if (!Number.isFinite(v)) return '—'
  const u = unit.trim()
  const short = (x: number) => {
    const s = Math.abs(x) >= 100 ? x.toFixed(0) : Math.abs(x) >= 10 ? x.toFixed(1) : x.toFixed(2)
    return s.includes('.') ? s.replace(/0+$/, '').replace(/\.$/, '') : s
  }
  if (u === '%') return `${short(v)} %`
  const scaled = (base: number, names: string[]) => {
    let i = 0
    let x = v
    while (Math.abs(x) >= base && i < names.length - 1) {
      x /= base
      i++
    }
    return `${short(x)} ${names[i]}`
  }
  if (u === 'bps') return scaled(1000, ['bps', 'Kbps', 'Mbps', 'Gbps', 'Tbps'])
  if (u === 'B' || u === 'bytes') return scaled(1024, ['B', 'KiB', 'MiB', 'GiB', 'TiB'])
  if (u === 'Bps' || u === 'B/s') return scaled(1024, ['B/s', 'KiB/s', 'MiB/s', 'GiB/s', 'TiB/s'])
  if (u === 's') return v < 1 ? `${short(v * 1000)} ms` : `${short(v)} s`
  const plain = scaled(1000, ['', 'K', 'M', 'G', 'T']).trim()
  return u ? `${plain} ${u}` : plain
}

function niceMax(v: number) {
  if (v <= 0) return 1
  const p = 10 ** Math.floor(Math.log10(v))
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p
  return 10 * p
}

function useWidth() {
  const ref = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(600)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const ro = new ResizeObserver(([e]) => setWidth(Math.max(240, Math.floor(e.contentRect.width))))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  return { ref, width }
}

// LineChart draws the series of one graph over [from, to] with the marks of the incident: its
// start and end as lines, events and other incidents as ticks.
export function LineChart({
  series,
  from,
  to,
  unit,
  marks,
  formatTime,
  label,
  threshold,
}: {
  series: ChartSeries[]
  from: number
  to: number
  unit?: string
  marks: ChartMark[]
  formatTime: (ms: number, long?: boolean) => string
  label: string
  // threshold: the line the alert fires at.
  threshold?: number
}) {
  const { ref, width } = useWidth()
  const [hover, setHover] = useState<number | null>(null)
  const w = width - PAD.left - PAD.right
  const h = HEIGHT - PAD.top - PAD.bottom
  const { lo, hi } = useMemo(() => {
    let lo = Infinity
    let hi = -Infinity
    for (const s of series)
      for (const [, v] of s.points) {
        lo = Math.min(lo, v)
        hi = Math.max(hi, v)
      }
    if (threshold !== undefined && Number.isFinite(threshold)) {
      lo = Math.min(lo, threshold)
      hi = Math.max(hi, threshold)
    }
    if (!Number.isFinite(lo)) return { lo: 0, hi: 1 }
    if (unit === '%' && hi <= 100 && lo >= 0) return { lo: 0, hi: hi > 50 ? 100 : niceMax(hi) }
    const base = lo >= 0 ? 0 : -niceMax(-lo)
    return { lo: base, hi: hi <= base ? base + 1 : niceMax(hi) }
  }, [series, unit, threshold])
  const span = Math.max(1, to - from)
  const x = (t: number) => PAD.left + ((t - from) / span) * w
  const y = (v: number) => PAD.top + h - ((v - lo) / (hi - lo || 1)) * h
  const ticksY = [0, 0.25, 0.5, 0.75, 1].map((f) => lo + f * (hi - lo))
  const long = span > 36 * 3600_000
  const ticksX = Array.from({ length: 5 }, (_, i) => from + (i * span) / 4)
  const hoverAt = hover === null ? null : from + ((hover - PAD.left) / w) * span
  const nearest = (s: ChartSeries) => {
    if (hoverAt === null || s.points.length === 0) return null
    let best = s.points[0]
    for (const p of s.points) if (Math.abs(p[0] - hoverAt) < Math.abs(best[0] - hoverAt)) best = p
    // Only a point near the cursor: a gap in the data shows no value.
    return Math.abs(best[0] - hoverAt) <= Math.max(span / 60, 120_000) ? best : null
  }
  const path = (s: ChartSeries) => {
    let d = ''
    let prev = -Infinity
    const gap = Math.max(span / 30, 10 * 60_000)
    for (const [t, v] of s.points) {
      d += `${t - prev > gap ? 'M' : 'L'}${x(t).toFixed(1)},${y(v).toFixed(1)}`
      prev = t
    }
    return d
  }
  const tip = hoverAt !== null ? series.map((s, i) => ({ s, i, p: nearest(s) })).filter((r) => r.p) : []
  return (
    <div className="mc-chart" ref={ref}>
      <svg
        width={width}
        height={HEIGHT}
        role="img"
        aria-label={label}
        onMouseMove={(e) => {
          const r = e.currentTarget.getBoundingClientRect()
          const px = e.clientX - r.left
          setHover(px >= PAD.left && px <= PAD.left + w ? px : null)
        }}
        onMouseLeave={() => setHover(null)}
      >
        {ticksY.map((v, i) => (
          <g key={i}>
            <line className="mc-grid" x1={PAD.left} x2={PAD.left + w} y1={y(v)} y2={y(v)} />
            <text className="mc-axis" x={PAD.left - 6} y={y(v)} textAnchor="end" dominantBaseline="middle">
              {formatValue(v, unit)}
            </text>
          </g>
        ))}
        {ticksX.map((t, i) => (
          <text key={i} className="mc-axis" x={x(t)} y={HEIGHT - 6} textAnchor={i === 0 ? 'start' : i === 4 ? 'end' : 'middle'}>
            {formatTime(t, long)}
          </text>
        ))}
        {marks
          .filter((m) => m.at >= from && m.at <= to)
          .map((m, i) =>
            m.tone === 'open' || m.tone === 'resolve' ? (
              <line key={i} className={`mc-mark mc-mark-${m.tone}`} x1={x(m.at)} x2={x(m.at)} y1={PAD.top} y2={PAD.top + h}>
                <title>{m.label}</title>
              </line>
            ) : (
              <rect key={i} className={`mc-tick mc-tick-${m.tone}`} x={x(m.at) - 1.5} y={PAD.top + h - 6} width={3} height={6}>
                <title>{m.label}</title>
              </rect>
            ),
          )}
        {threshold !== undefined && Number.isFinite(threshold) && (
          <line className="mc-threshold" x1={PAD.left} x2={PAD.left + w} y1={y(threshold)} y2={y(threshold)}>
            <title>{formatValue(threshold, unit)}</title>
          </line>
        )}
        {series.map((s, i) => (
          <path key={s.name} d={path(s)} fill="none" stroke={seriesColor(i)} strokeWidth={1.6} strokeLinejoin="round" />
        ))}
        {hover !== null && <line className="mc-cross" x1={hover} x2={hover} y1={PAD.top} y2={PAD.top + h} />}
        {tip.map(({ s, i, p }) => (
          <circle key={s.name} cx={x(p![0])} cy={y(p![1])} r={3} fill={seriesColor(i)} />
        ))}
      </svg>
      {hoverAt !== null && tip.length > 0 && (
        <div className={`mc-tip ${hover! > PAD.left + w / 2 ? 'mc-tip-left' : ''}`} style={{ left: hover! }}>
          <div className="mc-tip-at">{formatTime(hoverAt, true)}</div>
          {tip.map(({ s, i, p }) => (
            <div key={s.name} className="mc-tip-row">
              <span className="mc-dot" style={{ background: seriesColor(i) }} />
              <span className="mc-tip-name">{s.name}</span>
              <b>{formatValue(p![1], s.unit || unit)}</b>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
